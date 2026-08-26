import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type {
  ChangeEvent,
  FocusEvent as ReactFocusEvent,
  FormEvent,
  PointerEvent as ReactPointerEvent,
  Ref,
  RefObject,
} from 'react';
import { open } from '@tauri-apps/plugin-dialog';
import { browserHostLabel, claimBrowserHostFocus, controlBrowserHost } from '../../browser/host';
import { parseNotebookTileParams, serializeNotebookTileParams, type TileContentState, type TileLeaf } from '../../types/workspace';
import { deriveTileTitle, tilePathBasename } from '../../utils/tilePresentation';
import { useAppViewTitleResolver } from '../../hooks/useAppViewTitle';
import { BrowserTileBody } from './BrowserTileBody';
import { MarkdownReader } from '../MarkdownReader';
import type { MarkdownAnnotationsSendHandle } from '../MarkdownReader';
import {
  fileMarkdownSource,
  seedMarkdownSource,
  type MarkdownDocumentSource,
} from '../MarkdownReader/documentSource';
import { getMarkdownAnnotationsTransport } from '../MarkdownReader/annotations/transport';
import type { MarkdownAnnotationsDestination } from '../MarkdownReader/annotations/transport';
import { useAnnotationSend } from '../../annotations/useAnnotationSend';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { useNotebookSurfaceContext } from '../../contexts/NotebookSurfaceContext';
import { NotebookTile } from '../notebook/NotebookTile';
import { AppTileHost } from '../appViews/AppTileHost';
import { parseAppViewTileKind } from '../../utils/appBundle';
import type { NotebookSurfaceHandle } from '../NotebookSurface';
import { SeedDocumentView, type SeedDocument } from '../SeedDocumentView';
import { useDaemonApi, useOptionalDaemonApi } from '../../contexts/DaemonApiContext';
import type { Seed } from '../../hooks/useDaemonSocket';
import './WorkspaceDockTile.css';

export { resolveMarkdownTarget } from '../MarkdownReader/markdownLinks';

function bodyKindModifier(tileKind: string): string {
  if (parseAppViewTileKind(tileKind)) return 'workspace-dock-tile-body--app';
  if (tileKind === 'browser') return 'workspace-dock-tile-body--browser';
  if (tileKind === 'notebook') return 'workspace-dock-tile-body--notebook';
  if (tileKind === 'markdown') return 'workspace-dock-tile-body--markdown';
  if (tileKind === 'seed') return 'workspace-dock-tile-body--markdown';
  return '';
}

export function normalizeBrowserAddress(value: string): string {
  const trimmed = value.trim();
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed)) return trimmed;
  const localHost = /^(?:localhost|127(?:\.\d{1,3}){3}|\[::1\])(?::\d+)?(?:[/?#]|$)/i.test(trimmed);
  return `${localHost ? 'http' : 'https'}://${trimmed}`;
}

export interface WorkspaceTileSessionOption {
  sessionId: string;
  label: string;
  state?: string;
}

interface WorkspaceDockTileProps {
  tile: TileLeaf;
  workspaceId: string;
  content?: TileContentState;
  allowLocalTargets?: boolean;
  dragging: boolean;
  visible?: boolean;
  workspaceSessions?: WorkspaceTileSessionOption[];
  gardenSeeds?: Seed[];
  workspaceSessionId?: string | null;
  workspaceDirectory?: string;
  onClose: () => void;
  onFocusDocument?: () => void;
  onUpdateParams?: (tileParams: string) => Promise<unknown> | void;
  onRetargetTile?: (sessionId: string) => Promise<unknown> | void;
  onHeaderPointerDown: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onRequestContent: (workspaceId: string, tileId: string) => void;
  bodyRef?: Ref<HTMLDivElement>;
}

type MarkdownSendResult =
  | { kind: 'sent'; destination: 'session' | 'seed' }
  | { kind: 'skipped' }
  | { kind: 'warning'; message: string }
  | { kind: 'error'; message: string };

const SEND_SENT_CLEAR_MS = 4000;
const SKIPPED_APPROVAL_MESSAGE = 'Target is waiting for approval — not sent';
const NOT_HYDRATED_MESSAGE = 'Annotations are still syncing — try again in a moment';
const NO_GARDEN_SEEDS: Seed[] = [];

export function WorkspaceDockTile({
  tile,
  workspaceId,
  content,
  allowLocalTargets = true,
  dragging,
  visible = true,
  workspaceSessions = [],
  gardenSeeds = NO_GARDEN_SEEDS,
  workspaceSessionId = null,
  workspaceDirectory,
  onClose,
  onFocusDocument,
  onUpdateParams,
  onRetargetTile,
  onHeaderPointerDown,
  onRequestContent,
  bodyRef,
}: WorkspaceDockTileProps) {
  useEffect(() => {
    if (tile.tileKind === 'markdown') {
      onRequestContent(workspaceId, tile.tileId);
    }
  }, [workspaceId, tile.tileId, tile.tileKind, tile.tileParams, onRequestContent]);

  // Notebook tileParams may be the legacy bare-path string or the {root, path} JSON
  // envelope — parse either way so consumers see the plain open path, not raw JSON.
  const path = content?.path
    || (tile.tileKind === 'notebook' ? parseNotebookTileParams(tile.tileParams).path : tile.tileParams)
    || '';
  const appView = parseAppViewTileKind(tile.tileKind);
  const appViewTitle = useAppViewTitleResolver();
  const baseTitle = deriveTileTitle(tile, content, appViewTitle);
  const browserLabel = browserHostLabel(workspaceId, tile.tileId);
  const [browserAddress, setBrowserAddress] = useState(tile.tileParams || '');
  const pendingBrowserParamsRef = useRef<string | null>(null);
  // Lets the root switcher below flush a dirty buffer to the OLD root before swapping
  // params: the 700ms autosave debounce would otherwise lose an in-flight edit.
  const notebookSurfaceRef = useRef<NotebookSurfaceHandle | null>(null);

  const isMarkdown = tile.tileKind === 'markdown';
  const isSeed = tile.tileKind === 'seed';
  const isAnnotatedDocument = isMarkdown || isSeed;
  const {
    document: seedDocument,
    error: seedDocumentError,
  } = useLiveSeedDocument(path, gardenSeeds, isSeed);
  const title = isSeed ? (seedDocument?.seed.title || path || baseTitle) : baseTitle;
  const documentSource = useMemo(
    () => (isSeed ? seedMarkdownSource(path) : fileMarkdownSource(workspaceId, path)),
    [isSeed, path, workspaceId],
  );
  const annotationsSendRef = useRef<MarkdownAnnotationsSendHandle | null>(null);
  const [annotationCount, setAnnotationCount] = useState(0);
  // Gates the ⌘Enter shortcut's registration: with focus in a terminal pane the shortcut
  // must not exist at all, so the key falls through to the PTY untouched.
  const [hasFocusWithin, setHasFocusWithin] = useState(false);

  // `tile.tileSessionId` only updates when the daemon's layout broadcast echoes the
  // rebind back, which can lag the click; the user's pick is held locally meanwhile.
  const boundSessionId = tile.tileSessionId ?? '';
  const [pendingTargetSessionId, setPendingTargetSessionId] = useState<string | null>(null);
  useEffect(() => {
    if (pendingTargetSessionId !== null && boundSessionId === pendingTargetSessionId) {
      setPendingTargetSessionId(null);
    }
  }, [boundSessionId, pendingTargetSessionId]);
  const pendingInWorkspace = pendingTargetSessionId !== null
    && workspaceSessions.some((s) => s.sessionId === pendingTargetSessionId);
  const boundInWorkspace = workspaceSessions.some((s) => s.sessionId === boundSessionId);
  const targetSessionId = pendingInWorkspace
    ? (pendingTargetSessionId as string)
    : boundInWorkspace
      ? boundSessionId
      : '';
  const targetSession = workspaceSessions.find((session) => session.sessionId === targetSessionId);
  const targetSessionLabel = targetSession?.label ?? 'No session';
  const seedTenderSessionId = seedDocument?.tender_holds
    ? seedDocument.seed.tender_session.trim()
    : '';
  const primaryDestination = useMemo<MarkdownAnnotationsDestination | null>(() => {
    if (isSeed) {
      if (!seedDocument || !path) return null;
      return seedTenderSessionId
        ? { kind: 'session', sessionId: seedTenderSessionId }
        : { kind: 'seed', seedId: path };
    }
    return targetSessionId ? { kind: 'session', sessionId: targetSessionId } : null;
  }, [isSeed, path, seedDocument, seedTenderSessionId, targetSessionId]);
  const transportAvailable = getMarkdownAnnotationsTransport() !== null;

  const performAnnotationSend = useCallback((
    destination: MarkdownAnnotationsDestination | null,
  ): MarkdownSendResult | null | Promise<MarkdownSendResult> => {
    const handle = annotationsSendRef.current;
    const transport = getMarkdownAnnotationsTransport();
    if (annotationCount === 0 || !destination || !handle || !transport || !path) {
      return null;
    }
    if (!handle.isHydrated()) {
      // The daemon draft has not been loaded (hydrate in flight or failed), so the daemon would
      // format a STALE stored draft. Refuse rather than mis-deliver.
      return { kind: 'error', message: NOT_HYDRATED_MESSAGE };
    }
    return (async () => {
      // Flush the 500ms save debounce first so the daemon formats a draft that includes the
      // last keystroke's edit.
      await handle.flushPendingSave();
      const result = await transport.submitMarkdownAnnotations(
        documentSource,
        destination,
        handle.getOrphanedIds(),
      );
      if ((result.status === 'delivered' || result.status === 'noted') && result.error) {
        return { kind: 'warning', message: result.error };
      }
      if (result.status === 'delivered' || result.status === 'noted') {
        handle.applyDeliveredClear(result.generation ?? 0);
        return { kind: 'sent', destination: result.status === 'noted' ? 'seed' : 'session' };
      }
      if (result.status === 'skipped_pending_approval') {
        return { kind: 'skipped' };
      }
      return { kind: 'error', message: result.error || 'Send failed' };
    })();
  }, [annotationCount, documentSource, path]);

  const sendEnabled = isAnnotatedDocument
      && visible
      && hasFocusWithin
      && annotationCount > 0
      && !!primaryDestination
      && transportAvailable;
  const {
    outcome: sendOutcome,
    send: sendNow,
    sendAlternative,
    clearOutcome: clearSendOutcome,
  } = useAnnotationSend<MarkdownSendResult>({
    send: () => performAnnotationSend(primaryDestination),
    shortcutId: 'markdown.sendAnnotations',
    enabled: sendEnabled,
    sentClearMs: SEND_SENT_CLEAR_MS,
  });
  const sendStatus = sendOutcome ?? { kind: 'idle' as const };

  const handleTileFocus = useCallback(() => {
    setHasFocusWithin(true);
  }, []);
  const handleTileBlur = useCallback((event: ReactFocusEvent<HTMLDivElement>) => {
    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
      setHasFocusWithin(false);
    }
  }, []);

  const sending = sendStatus.kind === 'sending';
  const sendDisabled = sending || annotationCount === 0 || !primaryDestination || !transportAvailable;
  const destinationMenuKey = isSeed
    ? seedTenderSessionId && seedDocument
      ? `seed:${seedTenderSessionId}:${seedDocument.seed.rev}`
      : null
    : workspaceSessions.length > 0
      ? `session:${workspaceSessions.map((session) => `${session.sessionId}:${session.state ?? ''}`).join('|')}`
      : null;
  const [openDestinationMenuKey, setOpenDestinationMenuKey] = useState<string | null>(null);
  const destinationMenuOpen = destinationMenuKey !== null
    && openDestinationMenuKey === destinationMenuKey;
  const destinationGroupRef = useRef<HTMLDivElement>(null);
  const destinationCaretRef = useRef<HTMLButtonElement>(null);
  const destinationMenuRef = useRef<HTMLDivElement>(null);
  const closeDestinationMenu = useCallback((restoreFocus = false) => {
    setOpenDestinationMenuKey(null);
    if (restoreFocus) {
      window.requestAnimationFrame(() => destinationCaretRef.current?.focus());
    }
  }, []);
  useEscapeStack(() => closeDestinationMenu(true), destinationMenuOpen);
  useEffect(() => {
    if (!destinationMenuOpen) return;
    destinationMenuRef.current
      ?.querySelector<HTMLButtonElement>('[role="menuitem"], [role="menuitemradio"]')
      ?.focus();
    const handleMouseDown = (event: MouseEvent) => {
      if (!destinationGroupRef.current?.contains(event.target as Node)) {
        closeDestinationMenu();
      }
    };
    document.addEventListener('mousedown', handleMouseDown);
    return () => document.removeEventListener('mousedown', handleMouseDown);
  }, [closeDestinationMenu, destinationMenuOpen]);

  const retargetSession = useCallback((sessionId: string) => {
    closeDestinationMenu();
    if (!sessionId || sessionId === targetSessionId) {
      return;
    }
    setPendingTargetSessionId(sessionId);
    clearSendOutcome();
    void Promise.resolve(onRetargetTile?.(sessionId)).catch((error) => {
      console.warn('[WorkspaceDockTile] Failed to retarget tile session:', error);
      setPendingTargetSessionId((prev) => (prev === sessionId ? null : prev));
    });
  }, [clearSendOutcome, closeDestinationMenu, onRetargetTile, targetSessionId]);
  const sendHasProblem = sendStatus.kind === 'skipped'
    || sendStatus.kind === 'warning'
    || sendStatus.kind === 'error';
  const sendStatusMessage = sendStatus.kind === 'sending'
    ? (isSeed && !seedTenderSessionId ? 'Noting…' : 'Sending…')
    : sendStatus.kind === 'sent'
      ? (sendStatus.destination === 'seed' ? 'Noted ✓' : 'Sent ✓')
      : sendStatus.kind === 'skipped'
        ? SKIPPED_APPROVAL_MESSAGE
        : sendStatus.kind === 'warning' || sendStatus.kind === 'error'
          ? sendStatus.message
          : null;
  const sendActionLabel = sendStatus.kind === 'sending' || sendStatus.kind === 'sent'
    ? (sendStatusMessage as string)
    : sendStatus.kind === 'skipped'
      ? 'Approval needed'
      : sendStatus.kind === 'warning'
        ? 'Needs attention'
        : sendStatus.kind === 'error'
          ? 'Send failed'
          : isSeed && !seedTenderSessionId
            ? `Note on seed ${annotationCount}`
            : `Send ${annotationCount}`;
  const showSessionSendAction = annotationCount > 0 || sendStatus.kind !== 'idle';
  const sendButtonTitle = sendHasProblem
    ? sendStatusMessage ?? undefined
    : isSeed
      ? seedTenderSessionId
        ? 'Send annotations to the tending session (⌘Enter)'
        : 'Leave annotations as a note on the seed (⌘Enter)'
      : targetSessionId
        ? `Send annotations to ${targetSessionLabel} (⌘Enter)`
        : 'Choose a session before sending annotations';

  useEffect(() => {
    setBrowserAddress(tile.tileParams || '');
    if (pendingBrowserParamsRef.current === tile.tileParams) {
      pendingBrowserParamsRef.current = null;
    }
  }, [tile.tileParams]);

  useEffect(() => {
    if (tile.tileKind !== 'browser') {
      return;
    }
    const handleLocation = (event: Event) => {
      const detail = (event as CustomEvent<unknown>).detail;
      if (
        typeof detail === 'object'
        && detail !== null
        && 'label' in detail
        && 'url' in detail
        && detail.label === browserLabel
        && typeof detail.url === 'string'
      ) {
        setBrowserAddress(detail.url);
        if (
          detail.url !== tile.tileParams
          && detail.url !== pendingBrowserParamsRef.current
        ) {
          pendingBrowserParamsRef.current = detail.url;
          void Promise.resolve(onUpdateParams?.(detail.url)).catch((error) => {
            if (pendingBrowserParamsRef.current === detail.url) {
              pendingBrowserParamsRef.current = null;
            }
            console.warn('[WorkspaceDockTile] Failed to persist browser location:', error);
          });
        }
      }
    };
    window.addEventListener('attn:browser-location', handleLocation);
    return () => {
      window.removeEventListener('attn:browser-location', handleLocation);
    };
  }, [browserLabel, onUpdateParams, tile.tileKind, tile.tileParams]);

  const { effectiveNotebookRoot } = useNotebookSurfaceContext();
  const isNotebook = tile.tileKind === 'notebook';
  const currentRoot = isNotebook ? parseNotebookTileParams(tile.tileParams).root : undefined;
  const ROOT_BROWSE_VALUE = '__browse__';
  const workspaceDirIsRoot = !!workspaceDirectory && workspaceDirectory !== effectiveNotebookRoot;
  const currentRootIsOther = !!currentRoot
    && currentRoot !== effectiveNotebookRoot
    && currentRoot !== workspaceDirectory;

  const handleRootChange = useCallback((event: ChangeEvent<HTMLSelectElement>) => {
    const value = event.target.value;
    if (value === ROOT_BROWSE_VALUE) {
      void open({ directory: true, multiple: false, title: 'Choose editor root' }).then(async (selected) => {
        if (!selected || typeof selected !== 'string') {
          return;
        }
        // Flush the outgoing root's dirty buffer BEFORE the param swap: it remounts NotebookSurface
        // onto the new root, and only this instance can still persist to the old one.
        const outcome = notebookSurfaceRef.current ? await notebookSurfaceRef.current.flushPendingSave() : 'noop';
        if (outcome === 'conflict' || outcome === 'error') {
          return;
        }
        void Promise.resolve(onUpdateParams?.(serializeNotebookTileParams({ root: selected }))).catch((error) => {
          console.warn('[WorkspaceDockTile] Failed to persist browsed notebook root:', error);
        });
      }).catch((error) => {
        console.warn('[WorkspaceDockTile] Failed to open root browse dialog:', error);
      });
      return;
    }
    void (async () => {
      const outcome = notebookSurfaceRef.current ? await notebookSurfaceRef.current.flushPendingSave() : 'noop';
      if (outcome === 'conflict' || outcome === 'error') {
        return;
      }
      void Promise.resolve(onUpdateParams?.(serializeNotebookTileParams({ root: value || undefined }))).catch((error) => {
        console.warn('[WorkspaceDockTile] Failed to persist notebook root:', error);
      });
    })();
  }, [onUpdateParams]);

  const reloadBrowser = () => {
    void controlBrowserHost(workspaceId, tile.tileId, 'reload').catch((error) => {
      console.warn('[WorkspaceDockTile] Failed to reload browser:', error);
    });
  };
  const navigateBrowser = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const trimmed = browserAddress.trim();
    if (!trimmed) {
      return;
    }
    const target = normalizeBrowserAddress(trimmed);
    setBrowserAddress(target);
    void controlBrowserHost(
      workspaceId,
      tile.tileId,
      'navigate',
      JSON.stringify({ url: target }),
    ).catch((error) => {
      console.warn('[WorkspaceDockTile] Failed to navigate browser:', error);
    });
  };

  return (
    <div
      className={`workspace-dock-tile ${dragging ? 'workspace-dock-tile--dragging' : ''}`.trim()}
      data-browser-host-owner={tile.tileKind === 'browser' ? true : undefined}
      onPointerDownCapture={tile.tileKind === 'browser' ? () => claimBrowserHostFocus(browserLabel) : undefined}
      onFocus={isAnnotatedDocument ? handleTileFocus : undefined}
      onBlur={isAnnotatedDocument ? handleTileBlur : undefined}
    >
      <div
        className="workspace-dock-tile-header"
        onPointerDown={onHeaderPointerDown}
        title={path || 'Drag to re-dock'}
      >
        {tile.tileKind === 'browser' ? (
          <form
            className="workspace-dock-tile-address-form"
            onSubmit={navigateBrowser}
            onPointerDown={(event) => event.stopPropagation()}
          >
            <input
              className="workspace-dock-tile-address"
              type="text"
              value={browserAddress}
              aria-label="Browser address"
              spellCheck={false}
              onChange={(event) => setBrowserAddress(event.target.value)}
              onFocus={(event) => event.currentTarget.select()}
            />
          </form>
        ) : (
          <span className="workspace-dock-tile-title">{title}</span>
        )}
        {isNotebook ? (
          <select
            className="workspace-dock-tile-root-picker"
            aria-label="Editor root"
            value={currentRoot ?? ''}
            // The header is the drag handle; interacting with the picker must not start a re-dock drag.
            onPointerDown={(event) => event.stopPropagation()}
            onChange={handleRootChange}
          >
            <option value="">Notebook</option>
            {workspaceDirIsRoot && (
              <option value={workspaceDirectory}>Workspace — {tilePathBasename(workspaceDirectory as string)}</option>
            )}
            {currentRootIsOther && (
              <option value={currentRoot}>{tilePathBasename(currentRoot as string)}</option>
            )}
            <option value={ROOT_BROWSE_VALUE}>Browse…</option>
          </select>
        ) : null}
        {isAnnotatedDocument ? (
          <div
            className="workspace-dock-tile-send"
            // The header is the drag handle; interacting with the send controls must not start a drag.
            onPointerDown={(event) => event.stopPropagation()}
          >
            <button
              type="button"
              className="workspace-dock-tile-review-button workspace-dock-tile-review-button--overall"
              title="Add an overall note"
              onClick={(event) => annotationsSendRef.current?.openGlobalComment(event.currentTarget)}
            >
              Overall note
            </button>
            <button
              type="button"
              className="workspace-dock-tile-review-button"
              title="Show review notes"
              aria-label={`Notes ${annotationCount}`}
              onClick={() => annotationsSendRef.current?.openInspector()}
            >
              <NotesIcon />
              <span className="workspace-dock-tile-review-label">Notes</span>
              <span className="workspace-dock-tile-review-count">{annotationCount}</span>
            </button>
            {sendStatusMessage ? (
              <span className="workspace-dock-tile-send-status" role="status">{sendStatusMessage}</span>
            ) : null}
            {isSeed ? (
              <div
                ref={destinationGroupRef}
                className="workspace-dock-tile-destination-submit"
                role="group"
                aria-label="Submit seed annotations"
              >
                <button
                  type="button"
                  className={[
                    'workspace-dock-tile-send-button',
                    seedTenderSessionId ? 'workspace-dock-tile-send-button--split-primary' : '',
                    sendStatus.kind === 'sent' ? 'workspace-dock-tile-send-button--ok' : '',
                    sendHasProblem ? `workspace-dock-tile-send-button--${sendStatus.kind}` : '',
                  ].filter(Boolean).join(' ')}
                  disabled={sendDisabled}
                  title={sendButtonTitle}
                  onClick={sendNow}
                >
                  {sendActionLabel}
                  {sendHasProblem ? <span className="workspace-dock-tile-send-alert" aria-hidden="true">!</span> : null}
                </button>
                {seedTenderSessionId ? (
                  <>
                    <button
                      ref={destinationCaretRef}
                      type="button"
                      className="workspace-dock-tile-send-button workspace-dock-tile-send-button--split-caret"
                      aria-label="More annotation destinations"
                      aria-haspopup="menu"
                      aria-expanded={destinationMenuOpen}
                      disabled={sendDisabled}
                      onClick={() => setOpenDestinationMenuKey((openKey) => (
                        openKey === destinationMenuKey ? null : destinationMenuKey
                      ))}
                    >
                      ▾
                    </button>
                    {destinationMenuOpen ? (
                      <div
                        ref={destinationMenuRef}
                        className="workspace-dock-tile-destination-menu"
                        role="menu"
                      >
                        <button
                          type="button"
                          role="menuitem"
                          onClick={() => {
                            closeDestinationMenu();
                            sendAlternative(() => performAnnotationSend({ kind: 'seed', seedId: path }));
                          }}
                        >
                          Note on seed
                        </button>
                      </div>
                    ) : null}
                  </>
                ) : null}
              </div>
            ) : (
              <div
                ref={destinationGroupRef}
                className="workspace-dock-tile-destination-submit"
                role="group"
                aria-label="Send annotation destination"
              >
                {showSessionSendAction ? (
                  <button
                    type="button"
                    className={[
                      'workspace-dock-tile-send-button',
                      destinationMenuKey ? 'workspace-dock-tile-send-button--split-primary' : '',
                      sendStatus.kind === 'sent' ? 'workspace-dock-tile-send-button--ok' : '',
                      sendHasProblem ? `workspace-dock-tile-send-button--${sendStatus.kind}` : '',
                    ].filter(Boolean).join(' ')}
                    disabled={sendDisabled}
                    title={sendButtonTitle}
                    aria-label={sendStatus.kind === 'sending' || sendStatus.kind === 'sent'
                      ? sendActionLabel
                      : targetSessionId
                        ? `${sendActionLabel} to ${targetSessionLabel}`
                        : sendActionLabel}
                    onClick={sendNow}
                  >
                    <span className="workspace-dock-tile-send-action">{sendActionLabel}</span>
                    {sendStatus.kind !== 'sending' && sendStatus.kind !== 'sent' ? (
                      <span className="workspace-dock-tile-send-target">
                        <span className="workspace-dock-tile-send-target-prefix">to</span>
                        <span className="workspace-dock-tile-send-target-name">{targetSessionLabel}</span>
                      </span>
                    ) : null}
                    {sendHasProblem ? <span className="workspace-dock-tile-send-alert" aria-hidden="true">!</span> : null}
                  </button>
                ) : (
                  <button
                    ref={destinationCaretRef}
                    type="button"
                    className="workspace-dock-tile-target-button"
                    title={`Annotations will be sent to ${targetSessionLabel}`}
                    aria-label={`Annotation destination: ${targetSessionLabel}`}
                    aria-haspopup="menu"
                    aria-expanded={destinationMenuOpen}
                    disabled={!destinationMenuKey}
                    onClick={() => setOpenDestinationMenuKey((openKey) => (
                      openKey === destinationMenuKey ? null : destinationMenuKey
                    ))}
                  >
                    <span className="workspace-dock-tile-send-target-prefix">To</span>
                    <span className="workspace-dock-tile-send-target-name">{targetSessionLabel}</span>
                    <span aria-hidden="true">▾</span>
                  </button>
                )}
                {showSessionSendAction && destinationMenuKey ? (
                  <button
                    ref={destinationCaretRef}
                    type="button"
                    className="workspace-dock-tile-send-button workspace-dock-tile-send-button--split-caret"
                    aria-label="Change annotation destination"
                    aria-haspopup="menu"
                    aria-expanded={destinationMenuOpen}
                    onClick={() => setOpenDestinationMenuKey((openKey) => (
                      openKey === destinationMenuKey ? null : destinationMenuKey
                    ))}
                  >
                    ▾
                  </button>
                ) : null}
                {destinationMenuOpen ? (
                  <div
                    ref={destinationMenuRef}
                    className="workspace-dock-tile-destination-menu"
                    role="menu"
                    aria-label="Send annotations to session"
                    onKeyDown={(event) => {
                      if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
                      event.preventDefault();
                      const items = Array.from(
                        event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]'),
                      );
                      const current = items.indexOf(document.activeElement as HTMLButtonElement);
                      const step = event.key === 'ArrowDown' ? 1 : -1;
                      items[(current + step + items.length) % items.length]?.focus();
                    }}
                  >
                    {workspaceSessions.map((session) => (
                      <button
                        key={session.sessionId}
                        type="button"
                        role="menuitemradio"
                        aria-checked={session.sessionId === targetSessionId}
                        onClick={() => retargetSession(session.sessionId)}
                      >
                        <span className="workspace-dock-tile-destination-check" aria-hidden="true">
                          {session.sessionId === targetSessionId ? '✓' : ''}
                        </span>
                        <span className="workspace-dock-tile-destination-label">{session.label}</span>
                        {session.state === 'pending_approval' ? (
                          <span className="workspace-dock-tile-destination-state">approval</span>
                        ) : null}
                      </button>
                    ))}
                  </div>
                ) : null}
              </div>
            )}
          </div>
        ) : null}
        <div className="workspace-dock-tile-actions">
          {isAnnotatedDocument && onFocusDocument ? (
            <button
              type="button"
              className="workspace-dock-tile-focus-action"
              title="Focus document (⌘⇧Enter)"
              aria-label="Focus document"
              onPointerDown={(event) => event.stopPropagation()}
              onClick={onFocusDocument}
            >
              <FocusDocumentIcon />
              <span className="workspace-dock-tile-focus-label">Focus</span>
            </button>
          ) : null}
          {tile.tileKind === 'browser' ? (
            <button
              type="button"
              className="workspace-dock-tile-action"
              title="Reload browser"
              aria-label="Reload browser"
              onPointerDown={(event) => event.stopPropagation()}
              onClick={reloadBrowser}
            >
              ↻
            </button>
          ) : null}
          <button
            type="button"
            className="workspace-dock-tile-action"
            title="Close tile"
            aria-label="Close tile"
            onPointerDown={(event) => event.stopPropagation()}
            onClick={onClose}
          >
            ×
          </button>
        </div>
      </div>
      <div
        className={`workspace-dock-tile-body ${bodyKindModifier(tile.tileKind)}`.trim()}
        ref={bodyRef}
        tabIndex={-1}
      >
        {tile.tileKind === 'markdown' ? (
          <MarkdownBody
            content={content}
            source={documentSource}
            allowLocalTargets={allowLocalTargets}
            onAnnotationsCountChange={setAnnotationCount}
            annotationsSendRef={annotationsSendRef}
          />
        ) : tile.tileKind === 'seed' ? (
          <SeedTileBody
            document={seedDocument}
            error={seedDocumentError}
            onAnnotationsCountChange={setAnnotationCount}
            annotationsSendRef={annotationsSendRef}
          />
        ) : tile.tileKind === 'browser' ? (
          <BrowserTileBody
            workspaceId={workspaceId}
            tileId={tile.tileId}
            url={tile.tileParams || ''}
            dragging={dragging}
            visible={visible}
            onClose={onClose}
          />
        ) : tile.tileKind === 'notebook' ? (
          // tileParams may be the legacy bare-path string (rootless tile) or the
          // {root, path} JSON envelope.
          (() => {
            const { root, path: openPath } = parseNotebookTileParams(tile.tileParams);
            return (
              <NotebookTile
                ref={notebookSurfaceRef}
                initialPath={openPath || null}
                root={root}
                onOpenFile={(openedPath) => {
                  const nextParams = serializeNotebookTileParams({ root, path: openedPath });
                  void Promise.resolve(onUpdateParams?.(nextParams)).catch((error) => {
                    console.warn('[WorkspaceDockTile] Failed to persist notebook path:', error);
                  });
                }}
              />
            );
          })()
        ) : appView ? (
          <AppTileHost
            app={appView.app}
            view={appView.view}
            workspaceId={workspaceId}
            sessionId={workspaceSessionId}
            tileId={tile.tileId}
            params={tile.tileParams || ''}
          />
        ) : (
          <div className="workspace-dock-tile-message">Unsupported tile: {tile.tileKind}</div>
        )}
      </div>
    </div>
  );
}

function NotesIcon() {
  return (
    <svg
      className="workspace-dock-tile-review-icon"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      aria-hidden="true"
    >
      <path d="M3.25 3.25h9.5v7H7l-3.75 2.5v-9.5Z" strokeLinejoin="round" />
    </svg>
  );
}

function FocusDocumentIcon() {
  return (
    <svg
      className="workspace-dock-tile-focus-icon"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      aria-hidden="true"
    >
      <path d="M2.75 6V2.75H6M10 2.75h3.25V6M13.25 10v3.25H10M6 13.25H2.75V10" />
    </svg>
  );
}

function MarkdownBody({
  content,
  source,
  allowLocalTargets,
  onAnnotationsCountChange,
  annotationsSendRef,
}: {
  content?: TileContentState;
  source: MarkdownDocumentSource;
  allowLocalTargets: boolean;
  onAnnotationsCountChange: (count: number) => void;
  annotationsSendRef: RefObject<MarkdownAnnotationsSendHandle | null>;
}) {
  if (content === undefined) {
    return <div className="workspace-dock-tile-message">Loading…</div>;
  }
  if (content.error) {
    return <div className="workspace-dock-tile-message workspace-dock-tile-error">{content.error}</div>;
  }
  if (content.content.trim().length === 0) {
    return <div className="workspace-dock-tile-message">This file is empty.</div>;
  }
  return (
    <MarkdownReader
      content={content.content}
      source={source}
      allowLocalTargets={allowLocalTargets}
      annotationsEnabled
      onAnnotationsCountChange={onAnnotationsCountChange}
      annotationsSendRef={annotationsSendRef}
    />
  );
}

function useLiveSeedDocument(seedId: string, gardenSeeds: Seed[], enabled: boolean) {
  const sendSeedDocumentGet = useOptionalDaemonApi()?.sendSeedDocumentGet;
  const [document, setDocument] = useState<SeedDocument | null>(null);
  const [error, setError] = useState<string | null>(null);
  const liveSeed = useMemo(
    () => gardenSeeds.find((seed) => seed.id === seedId) ?? null,
    [gardenSeeds, seedId],
  );
  const displayedDocument = useMemo(() => {
    if (!document || !liveSeed || liveSeed.rev < document.seed.rev) return document;
    const tenderChanged = liveSeed.tender_session !== document.seed.tender_session
      || liveSeed.tender_member !== document.seed.tender_member;
    return {
      ...document,
      seed: liveSeed,
      tender_holds: tenderChanged
        ? Boolean(liveSeed.tender_session || liveSeed.tender_member)
        : document.tender_holds,
    };
  }, [document, liveSeed]);

  // Every garden fact re-pushes the seeds snapshot, and notes or child changes need not
  // touch this seed's own revision, so the array identity is the live invalidation signal.
  useEffect(() => {
    if (!enabled) return;
    if (!seedId) {
      setDocument(null);
      setError('No seed is associated with this tile.');
      return;
    }
    if (!sendSeedDocumentGet) {
      setDocument(null);
      setError('The daemon API is unavailable.');
      return;
    }
    let ignore = false;
    setError(null);
    void sendSeedDocumentGet(seedId)
      .then((next) => {
        if (ignore) return;
        if (liveSeed && liveSeed.rev >= next.seed.rev) {
          const tenderChanged = liveSeed.tender_session !== next.seed.tender_session
            || liveSeed.tender_member !== next.seed.tender_member;
          setDocument({
            ...next,
            seed: liveSeed,
            tender_holds: tenderChanged
              ? Boolean(liveSeed.tender_session || liveSeed.tender_member)
              : next.tender_holds,
          });
        } else {
          setDocument(next);
        }
      })
      .catch((readError) => {
        if (ignore) return;
        setError(readError instanceof Error ? readError.message : `Could not read ${seedId}`);
      });
    return () => {
      ignore = true;
    };
  }, [enabled, gardenSeeds, liveSeed, seedId, sendSeedDocumentGet]);

  return { document: displayedDocument, error };
}

function SeedTileBody({
  document,
  error,
  onAnnotationsCountChange,
  annotationsSendRef,
}: {
  document: SeedDocument | null;
  error: string | null;
  onAnnotationsCountChange: (count: number) => void;
  annotationsSendRef: RefObject<MarkdownAnnotationsSendHandle | null>;
}) {
  const { sendOpenMarkdown } = useDaemonApi();

  if (!document) {
    return (
      <div className={`workspace-dock-tile-message${error ? ' workspace-dock-tile-error' : ''}`}>
        {error || 'Loading seed…'}
      </div>
    );
  }

  return (
    <SeedDocumentView
      document={document}
      annotationsEnabled
      onAnnotationsCountChange={onAnnotationsCountChange}
      annotationsSendRef={annotationsSendRef}
      onOpenMarkdownArtifact={(path) => {
        void sendOpenMarkdown(path, '').catch((openError) => {
          console.error('[SeedDocument] Could not open markdown artifact:', openError);
        });
      }}
    />
  );
}
