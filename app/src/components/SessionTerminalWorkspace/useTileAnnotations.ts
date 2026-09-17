import type { FocusEvent as ReactFocusEvent } from 'react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useAnnotationSend } from '../../annotations/useAnnotationSend';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { formatShortcut } from '../../shortcuts/formatShortcut';
import type { MarkdownAnnotationsSendHandle } from '../MarkdownReader';
import type { MarkdownAnnotationsDestination } from '../MarkdownReader/annotations/transport';
import { getMarkdownAnnotationsTransport } from '../MarkdownReader/annotations/transport';
import { type MarkdownDocumentSource } from '../MarkdownReader/documentSource';
import { type SeedDocument } from '../SeedDocumentView';

import type { WorkspaceTileSessionOption } from './WorkspaceDockTile';
type MarkdownSendResult =
  | { kind: 'sent'; destination: 'session' | 'seed' }
  | { kind: 'skipped' }
  | { kind: 'warning'; message: string }
  | { kind: 'error'; message: string };

const SEND_SENT_CLEAR_MS = 4000;
const SKIPPED_APPROVAL_MESSAGE = 'Target is waiting for approval — not sent';
const NOT_HYDRATED_MESSAGE = 'Annotations are still syncing — try again in a moment';
interface Options {
  isSeed: boolean;
  visible: boolean;
  path: string;
  documentSource: MarkdownDocumentSource;
  seedDocument: SeedDocument | null;
  workspaceSessions: WorkspaceTileSessionOption[];
  boundSessionIdInput?: string;
  onRetargetTile?: (sessionId: string) => Promise<unknown> | void;
}
export function useTileAnnotations({
  isSeed,
  visible,
  path,
  documentSource,
  seedDocument,
  workspaceSessions,
  boundSessionIdInput,
  onRetargetTile,
}: Options) {
  const seedTenderSessionId = seedDocument?.tender_holds
    ? seedDocument.seed.tender_session.trim()
    : '';
  const annotationsSendRef = useRef<MarkdownAnnotationsSendHandle | null>(null);
  const [annotationCount, setAnnotationCount] = useState(0);
  // Gates the ⌘Enter shortcut's registration: with focus in a terminal pane the shortcut
  // must not exist at all, so the key falls through to the PTY untouched.
  const [hasFocusWithin, setHasFocusWithin] = useState(false);

  const {
    targetSessionId,
    targetSessionLabel,
    retargetSession: requestRetarget,
  } = useAnnotationTarget({ workspaceSessions, boundSessionIdInput, onRetargetTile });
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

  const performAnnotationSend = useCallback(
    (
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
    },
    [annotationCount, documentSource, path],
  );

  const sendEnabled =
    visible && hasFocusWithin && annotationCount > 0 && !!primaryDestination && transportAvailable;
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
  const sendDisabled =
    sending || annotationCount === 0 || !primaryDestination || !transportAvailable;
  const {
    destinationGroupRef,
    destinationCaretRef,
    destinationMenuRef,
    destinationMenuOpen,
    setOpenDestinationMenuKey,
    destinationMenuKey,
    closeDestinationMenu,
  } = useAnnotationDestinationMenu({
    isSeed,
    seedTenderSessionId,
    seedDocument,
    workspaceSessions,
  });

  const retargetSession = (sessionId: string) => {
    closeDestinationMenu();
    if (!sessionId || sessionId === targetSessionId) return;
    clearSendOutcome();
    requestRetarget(sessionId);
  };
  const {
    sendHasProblem,
    sendStatusMessage,
    sendActionLabel,
    showSessionSendAction,
    sendButtonTitle,
  } = annotationSendPresentation(
    sendStatus,
    isSeed,
    seedTenderSessionId,
    annotationCount,
    targetSessionId,
    targetSessionLabel,
  );
  return {
    annotationsSendRef,
    annotationCount,
    setAnnotationCount,
    hasFocusWithin,
    handleTileFocus,
    handleTileBlur,
    clearSendOutcome,
    sendStatusMessage,
    destinationGroupRef,
    seedTenderSessionId,
    sendStatus,
    sendHasProblem,
    sendDisabled,
    sendButtonTitle,
    sendNow,
    sendActionLabel,
    destinationCaretRef,
    destinationMenuOpen,
    setOpenDestinationMenuKey,
    destinationMenuKey,
    destinationMenuRef,
    closeDestinationMenu,
    sendAlternative,
    performAnnotationSend,
    showSessionSendAction,
    targetSessionId,
    targetSessionLabel,
    retargetSession,
  };
}

function annotationSendPresentation(
  sendStatus: MarkdownSendResult | { kind: 'idle' | 'sending' },
  isSeed: boolean,
  seedTenderSessionId: string,
  annotationCount: number,
  targetSessionId: string,
  targetSessionLabel: string,
) {
  const sendHasProblem =
    sendStatus.kind === 'skipped' || sendStatus.kind === 'warning' || sendStatus.kind === 'error';
  const sendStatusMessage =
    sendStatus.kind === 'sending'
      ? isSeed && !seedTenderSessionId
        ? 'Noting…'
        : 'Sending…'
      : sendStatus.kind === 'sent'
        ? sendStatus.destination === 'seed'
          ? 'Noted ✓'
          : 'Sent ✓'
        : sendStatus.kind === 'skipped'
          ? SKIPPED_APPROVAL_MESSAGE
          : sendStatus.kind === 'warning' || sendStatus.kind === 'error'
            ? sendStatus.message
            : null;
  const sendActionLabel =
    sendStatus.kind === 'sending' || sendStatus.kind === 'sent'
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
    ? (sendStatusMessage ?? undefined)
    : isSeed
      ? seedTenderSessionId
        ? `Send annotations to the tending session (${formatShortcut('markdown.sendAnnotations')})`
        : `Leave annotations as a note on the seed (${formatShortcut('markdown.sendAnnotations')})`
      : targetSessionId
        ? `Send annotations to ${targetSessionLabel} (${formatShortcut('markdown.sendAnnotations')})`
        : 'Choose a session before sending annotations';

  return {
    sendHasProblem,
    sendStatusMessage,
    sendActionLabel,
    showSessionSendAction,
    sendButtonTitle,
  };
}

function useAnnotationDestinationMenu({
  isSeed,
  seedTenderSessionId,
  seedDocument,
  workspaceSessions,
}: Pick<Options, 'isSeed' | 'seedDocument' | 'workspaceSessions'> & {
  seedTenderSessionId: string;
}) {
  const destinationMenuKey = isSeed
    ? seedTenderSessionId && seedDocument
      ? `seed:${seedTenderSessionId}:${seedDocument.seed.rev}`
      : null
    : workspaceSessions.length > 0
      ? `session:${workspaceSessions.map((session) => `${session.sessionId}:${session.state ?? ''}`).join('|')}`
      : null;
  const [openDestinationMenuKey, setOpenDestinationMenuKey] = useState<string | null>(null);
  const destinationMenuOpen =
    destinationMenuKey !== null && openDestinationMenuKey === destinationMenuKey;
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

  return {
    destinationGroupRef,
    destinationCaretRef,
    destinationMenuRef,
    destinationMenuOpen,
    setOpenDestinationMenuKey,
    destinationMenuKey,
    closeDestinationMenu,
  };
}

function useAnnotationTarget({
  workspaceSessions,
  boundSessionIdInput,
  onRetargetTile,
}: Pick<Options, 'workspaceSessions' | 'boundSessionIdInput' | 'onRetargetTile'>) {
  // `tile.tileSessionId` only updates when the daemon's layout broadcast echoes the
  // rebind back, which can lag the click; the user's pick is held locally meanwhile.
  const boundSessionId = boundSessionIdInput ?? '';
  const [pendingTargetSessionId, setPendingTargetSessionId] = useState<string | null>(null);
  if (pendingTargetSessionId !== null && boundSessionId === pendingTargetSessionId) {
    setPendingTargetSessionId(null);
  }
  const pendingInWorkspace =
    pendingTargetSessionId !== null &&
    workspaceSessions.some((s) => s.sessionId === pendingTargetSessionId);
  const boundInWorkspace = workspaceSessions.some((s) => s.sessionId === boundSessionId);
  const targetSessionId = pendingInWorkspace
    ? (pendingTargetSessionId as string)
    : boundInWorkspace
      ? boundSessionId
      : '';
  const targetSession = workspaceSessions.find((session) => session.sessionId === targetSessionId);
  const targetSessionLabel = targetSession?.label ?? 'No session';
  const retargetSession = useCallback(
    (sessionId: string) => {
      if (!sessionId || sessionId === targetSessionId) {
        return;
      }
      setPendingTargetSessionId(sessionId);
      void Promise.resolve(onRetargetTile?.(sessionId)).catch((error) => {
        console.warn('[WorkspaceDockTile] Failed to retarget tile session:', error);
        setPendingTargetSessionId((prev) => (prev === sessionId ? null : prev));
      });
    },
    [onRetargetTile, targetSessionId],
  );
  return { targetSessionId, targetSessionLabel, retargetSession };
}
