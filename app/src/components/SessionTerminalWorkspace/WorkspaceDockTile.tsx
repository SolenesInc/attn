import type { PointerEvent as ReactPointerEvent, Ref, RefObject } from 'react';
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef } from 'react';
import { browserHostLabel, claimBrowserHostFocus, controlBrowserHost } from '../../browser/host';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import { useAppViewTitleResolver } from '../../hooks/useAppViewTitle';
import type { Seed } from '../../hooks/useDaemonSocket';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { formatShortcut } from '../../shortcuts/formatShortcut';
import {
  parseNotebookTileParams,
  serializeNotebookTileParams,
  type TileContentState,
  type TileLeaf,
} from '../../types/workspace';
import { parseAppViewTileKind } from '../../utils/appBundle';
import { deriveTileTitle } from '../../utils/tilePresentation';
import { AppTileHost } from '../appViews/AppTileHost';
import { GardenIcon } from '../GardenIcon';
import type { MarkdownAnnotationsSendHandle } from '../MarkdownReader';
import { MarkdownReader } from '../MarkdownReader';
import {
  fileMarkdownSource,
  seedMarkdownSource,
  type MarkdownDocumentSource,
} from '../MarkdownReader/documentSource';
import { NotebookTile } from '../notebook/NotebookTile';
import type { NotebookSurfaceHandle } from '../NotebookSurface';
import { SeedDocumentView, type SeedDocument } from '../SeedDocumentView';
import { BrowserAddressForm } from './BrowserAddressForm';
import { BrowserTileBody } from './BrowserTileBody';
import { NotebookRootPicker } from './NotebookRootPicker';
import { TileAnnotationControls } from './TileAnnotationControls';
import { useSeedTileNavigation } from './useSeedTileNavigation';
import { useTileAnnotations } from './useTileAnnotations';
import './WorkspaceDockTile.css';

function bodyKindModifier(tileKind: string): string {
  if (parseAppViewTileKind(tileKind)) return 'workspace-dock-tile-body--app';
  if (tileKind === 'browser') return 'workspace-dock-tile-body--browser';
  if (tileKind === 'notebook') return 'workspace-dock-tile-body--notebook';
  if (tileKind === 'markdown') return 'workspace-dock-tile-body--markdown';
  if (tileKind === 'seed')
    return 'workspace-dock-tile-body--markdown workspace-dock-tile-body--seed';
  return '';
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
  onRevealSeedInGarden?: (seedId: string) => void;
  onBackToCrew?: (returnFocus: HTMLElement) => void;
  onHeaderPointerDown: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onRequestContent: (workspaceId: string, tileId: string) => void;
  bodyRef?: Ref<HTMLDivElement>;
}

const NO_GARDEN_SEEDS: Seed[] = [];
const NO_WORKSPACE_SESSIONS: WorkspaceTileSessionOption[] = [];

export function WorkspaceDockTile({
  tile,
  workspaceId,
  content,
  allowLocalTargets = true,
  dragging,
  visible = true,
  workspaceSessions = NO_WORKSPACE_SESSIONS,
  gardenSeeds = NO_GARDEN_SEEDS,
  workspaceSessionId = null,
  workspaceDirectory,
  onClose,
  onFocusDocument,
  onUpdateParams,
  onRetargetTile,
  onRevealSeedInGarden,
  onBackToCrew,
  onHeaderPointerDown,
  onRequestContent,
  bodyRef,
}: WorkspaceDockTileProps) {
  useEffect(() => {
    if (tile.tileKind === 'markdown') {
      onRequestContent(workspaceId, tile.tileId);
    }
  }, [workspaceId, tile.tileId, tile.tileKind, tile.tileParams, onRequestContent]);

  const persistedPath = persistedTilePath(tile, content);
  const isMarkdown = tile.tileKind === 'markdown';
  const isSeed = tile.tileKind === 'seed';
  const appView = parseAppViewTileKind(tile.tileKind);
  const appViewTitle = useAppViewTitleResolver();
  const baseTitle = deriveTileTitle(tile, content, appViewTitle);
  const browserLabel = browserHostLabel(workspaceId, tile.tileId);
  // Lets the root switcher below flush a dirty buffer to the OLD root before swapping
  // params: the 700ms autosave debounce would otherwise lose an in-flight edit.
  const notebookSurfaceRef = useRef<NotebookSurfaceHandle | null>(null);

  const isAnnotatedDocument = isMarkdown || isSeed;
  const seedNavigation = useSeedTileNavigation({
    isSeed,
    persistedPath,
    baseTitle,
    gardenSeeds,
    onUpdateParams,
  });
  const {
    path,
    seedNavigationError,
    seedDocument,
    seedDocumentError,
    title,
    parentSeed,
    seedLocationTitle,
    seedArrival,
  } = seedNavigation;
  const documentSource = useMemo(
    () => (isSeed ? seedMarkdownSource(path) : fileMarkdownSource(workspaceId, path)),
    [isSeed, path, workspaceId],
  );
  const annotations = useTileAnnotations({
    isSeed,
    visible: visible && isAnnotatedDocument,
    path,
    documentSource,
    seedDocument,
    workspaceSessions,
    boundSessionIdInput: tile.tileSessionId,
    onRetargetTile,
  });
  const {
    annotationsSendRef,
    setAnnotationCount,
    hasFocusWithin,
    handleTileFocus,
    handleTileBlur,
    clearSendOutcome,
  } = annotations;

  const isNotebook = tile.tileKind === 'notebook';

  const navigateSeed = (nextSeedID: string) => {
    if (!seedNavigation.navigateSeed(nextSeedID)) return;
    setAnnotationCount(0);
    clearSendOutcome();
  };

  // Escape is ordinary input in the terminal next door, so claim it only while
  // this tile has focus; the stack keeps popovers above trail navigation.
  useEscapeStack(
    () => {
      if (parentSeed) navigateSeed(parentSeed.id);
    },
    isSeed && visible && hasFocusWithin && parentSeed !== null && Boolean(onUpdateParams),
  );

  const scrollBodyRef = useRef<HTMLDivElement | null>(null);
  const setBodyRef = useCallback(
    (node: HTMLDivElement | null) => {
      scrollBodyRef.current = node;
      if (typeof bodyRef === 'function') bodyRef(node);
      else if (bodyRef) (bodyRef as { current: HTMLDivElement | null }).current = node;
    },
    [bodyRef],
  );
  useLayoutEffect(() => {
    if (isSeed && scrollBodyRef.current) scrollBodyRef.current.scrollTop = 0;
  }, [isSeed, path]);

  return (
    <div
      className={`workspace-dock-tile ${dragging ? 'workspace-dock-tile--dragging' : ''}`.trim()}
      data-browser-host-owner={tile.tileKind === 'browser' ? true : undefined}
      onPointerDownCapture={
        tile.tileKind === 'browser' ? () => claimBrowserHostFocus(browserLabel) : undefined
      }
      onFocus={isAnnotatedDocument ? handleTileFocus : undefined}
      onBlur={isAnnotatedDocument ? handleTileBlur : undefined}
    >
      <div
        className="workspace-dock-tile-header"
        onPointerDown={onHeaderPointerDown}
        title={path || 'Drag to re-dock'}
      >
        <WorkspaceTileTitle
          tile={tile}
          workspaceId={workspaceId}
          onUpdateParams={onUpdateParams}
          isSeed={isSeed}
          parentSeed={parentSeed}
          seedLocationTitle={seedLocationTitle}
          title={title}
          navigateSeed={navigateSeed}
        />
        {isNotebook ? (
          <NotebookRootPicker
            tileParams={tile.tileParams}
            workspaceDirectory={workspaceDirectory}
            notebookSurfaceRef={notebookSurfaceRef}
            onUpdateParams={onUpdateParams}
          />
        ) : null}
        {isAnnotatedDocument ? (
          <TileAnnotationControls
            annotations={annotations}
            isSeed={isSeed}
            path={path}
            workspaceSessions={workspaceSessions}
            seedNavigationError={seedNavigationError}
          />
        ) : null}
        <WorkspaceTileActions
          isAnnotatedDocument={isAnnotatedDocument}
          isSeed={isSeed}
          onFocusDocument={onFocusDocument}
          onRevealSeedInGarden={onRevealSeedInGarden}
          onBackToCrew={onBackToCrew}
          path={path}
          tile={tile}
          workspaceId={workspaceId}
          onClose={onClose}
        />
      </div>
      <div
        className={`workspace-dock-tile-body ${bodyKindModifier(tile.tileKind)}`.trim()}
        ref={setBodyRef}
        tabIndex={-1}
      >
        <WorkspaceTileContent
          tile={tile}
          workspaceId={workspaceId}
          content={content}
          allowLocalTargets={allowLocalTargets}
          annotationsSendRef={annotationsSendRef}
          setAnnotationCount={setAnnotationCount}
          seedDocument={seedDocument}
          seedDocumentError={seedDocumentError}
          onUpdateParams={onUpdateParams}
          navigateSeed={navigateSeed}
          seedArrival={seedArrival}
          dragging={dragging}
          visible={visible}
          onClose={onClose}
          notebookSurfaceRef={notebookSurfaceRef}
          appView={appView}
          workspaceSessionId={workspaceSessionId}
          documentSource={documentSource}
        />
      </div>
    </div>
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
    return (
      <div className="workspace-dock-tile-message workspace-dock-tile-error">{content.error}</div>
    );
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

function SeedTileBody({
  document,
  error,
  onAnnotationsCountChange,
  annotationsSendRef,
  onOpenSeed,
  arrival,
}: {
  document: SeedDocument | null;
  error: string | null;
  onAnnotationsCountChange: (count: number) => void;
  annotationsSendRef: RefObject<MarkdownAnnotationsSendHandle | null>;
  onOpenSeed?: (seedId: string) => void;
  arrival: 'in' | 'out';
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
      key={document.seed.id}
      document={document}
      annotationsEnabled
      onAnnotationsCountChange={onAnnotationsCountChange}
      annotationsSendRef={annotationsSendRef}
      onOpenSeed={onOpenSeed}
      arrival={arrival}
      onOpenMarkdownArtifact={(path) => {
        void sendOpenMarkdown(path, '').catch((openError) => {
          console.error('[SeedDocument] Could not open markdown artifact:', openError);
        });
      }}
    />
  );
}

interface ContentProps {
  tile: TileLeaf;
  workspaceId: string;
  content?: TileContentState;
  allowLocalTargets: boolean;
  annotationsSendRef: RefObject<MarkdownAnnotationsSendHandle | null>;
  setAnnotationCount: (count: number) => void;
  seedDocument: SeedDocument | null;
  seedDocumentError: string | null;
  onUpdateParams?: (params: string) => Promise<unknown> | void;
  navigateSeed: (id: string) => void;
  seedArrival: 'in' | 'out';
  dragging: boolean;
  visible: boolean;
  onClose: () => void;
  notebookSurfaceRef: RefObject<NotebookSurfaceHandle | null>;
  appView: ReturnType<typeof parseAppViewTileKind>;
  workspaceSessionId: string | null;
  documentSource: MarkdownDocumentSource;
}
function WorkspaceTileContent({
  tile,
  workspaceId,
  content,
  allowLocalTargets,
  annotationsSendRef,
  setAnnotationCount,
  seedDocument,
  seedDocumentError,
  onUpdateParams,
  navigateSeed,
  seedArrival,
  dragging,
  visible,
  onClose,
  notebookSurfaceRef,
  appView,
  workspaceSessionId,
  documentSource,
}: ContentProps) {
  if (tile.tileKind === 'markdown')
    return (
      <MarkdownBody
        content={content}
        source={documentSource}
        allowLocalTargets={allowLocalTargets}
        onAnnotationsCountChange={setAnnotationCount}
        annotationsSendRef={annotationsSendRef}
      />
    );
  if (tile.tileKind === 'seed')
    return (
      <SeedTileBody
        document={seedDocument}
        error={seedDocumentError}
        onAnnotationsCountChange={setAnnotationCount}
        annotationsSendRef={annotationsSendRef}
        onOpenSeed={onUpdateParams ? navigateSeed : undefined}
        arrival={seedArrival}
      />
    );
  if (tile.tileKind === 'browser')
    return (
      <BrowserTileBody
        workspaceId={workspaceId}
        tileId={tile.tileId}
        url={tile.tileParams || ''}
        dragging={dragging}
        visible={visible}
        onClose={onClose}
      />
    );
  if (tile.tileKind === 'notebook') {
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
  }
  if (appView)
    return (
      <AppTileHost
        app={appView.app}
        view={appView.view}
        workspaceId={workspaceId}
        sessionId={workspaceSessionId}
        tileId={tile.tileId}
        params={tile.tileParams || ''}
      />
    );
  return <div className="workspace-dock-tile-message">Unsupported tile: {tile.tileKind}</div>;
}

function WorkspaceTileActions({
  isAnnotatedDocument,
  isSeed,
  onFocusDocument,
  onRevealSeedInGarden,
  onBackToCrew,
  path,
  tile,
  workspaceId,
  onClose,
}: Pick<
  WorkspaceDockTileProps,
  'tile' | 'workspaceId' | 'onClose' | 'onFocusDocument' | 'onRevealSeedInGarden' | 'onBackToCrew'
> & { isAnnotatedDocument: boolean; isSeed: boolean; path: string }) {
  const reloadBrowser = () => {
    void controlBrowserHost(workspaceId, tile.tileId, 'reload').catch((error) => {
      console.warn('[WorkspaceDockTile] Failed to reload browser:', error);
    });
  };
  return (
    <div className="workspace-dock-tile-actions">
      {isSeed && onBackToCrew ? (
        <button
          type="button"
          className="workspace-dock-tile-back-crew"
          data-testid="crew-seed-back"
          aria-label="Back to Crew"
          title="Back to Crew"
          onPointerDown={(event) => event.stopPropagation()}
          onClick={(event) => onBackToCrew(event.currentTarget)}
        >
          <span className="workspace-dock-tile-back-crew-icon" aria-hidden="true">
            ←
          </span>
          <span className="workspace-dock-tile-back-crew-label">Back to Crew</span>
        </button>
      ) : null}
      {isAnnotatedDocument && onFocusDocument ? (
        <button
          type="button"
          className="workspace-dock-tile-focus-action"
          title={`Focus document (${formatShortcut('terminal.toggleMaximize')})`}
          aria-label="Focus document"
          onPointerDown={(event) => event.stopPropagation()}
          onClick={onFocusDocument}
        >
          <FocusDocumentIcon />
          <span className="workspace-dock-tile-focus-label">Focus</span>
        </button>
      ) : null}
      {isSeed && onRevealSeedInGarden ? (
        <button
          type="button"
          className="workspace-dock-tile-action"
          title="Reveal in Garden"
          aria-label="Reveal in Garden"
          onPointerDown={(event) => event.stopPropagation()}
          onClick={() => onRevealSeedInGarden(path)}
        >
          <GardenIcon />
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
  );
}

function SeedTileLocation({
  parentSeed,
  seedLocationTitle,
  title,
  navigateSeed,
}: {
  parentSeed: Seed | null;
  seedLocationTitle: string;
  title: string;
  navigateSeed: (id: string) => void;
}) {
  return (
    <div className="workspace-dock-tile-seed-location" title={seedLocationTitle || title}>
      {parentSeed && (
        <>
          <button
            type="button"
            className="workspace-dock-tile-seed-parent"
            aria-label={`Back to ${parentSeed.title}`}
            title={`Back to ${parentSeed.title}`}
            onPointerDown={(event) => event.stopPropagation()}
            onClick={() => navigateSeed(parentSeed.id)}
          >
            <span aria-hidden="true">‹</span>
            <span>{parentSeed.title}</span>
          </button>
          <span className="workspace-dock-tile-seed-separator" aria-hidden="true">
            ›
          </span>
        </>
      )}
      <span className="workspace-dock-tile-title">{title}</span>
    </div>
  );
}

function persistedTilePath(tile: TileLeaf, content?: TileContentState) {
  if (content?.path) return content.path;
  if (tile.tileKind === 'notebook') return parseNotebookTileParams(tile.tileParams).path || '';
  return tile.tileParams || '';
}

function WorkspaceTileTitle({
  tile,
  workspaceId,
  onUpdateParams,
  isSeed,
  parentSeed,
  seedLocationTitle,
  title,
  navigateSeed,
}: Pick<WorkspaceDockTileProps, 'tile' | 'workspaceId' | 'onUpdateParams'> & {
  isSeed: boolean;
  parentSeed: Seed | null;
  seedLocationTitle: string;
  title: string;
  navigateSeed: (id: string) => void;
}) {
  return (
    <>
      {tile.tileKind === 'browser' ? (
        <BrowserAddressForm tile={tile} workspaceId={workspaceId} onUpdateParams={onUpdateParams} />
      ) : isSeed ? (
        <SeedTileLocation
          parentSeed={parentSeed}
          seedLocationTitle={seedLocationTitle}
          title={title}
          navigateSeed={navigateSeed}
        />
      ) : (
        <span className="workspace-dock-tile-title">{title}</span>
      )}
    </>
  );
}
