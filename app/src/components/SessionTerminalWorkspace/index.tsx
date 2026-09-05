import { forwardRef, useCallback, useEffect, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { type BlockStateSnapshot, type GhosttyTerminalHandle, type PlacementStateSnapshot } from '../GhosttyTerminal';
import { AnnotatedTerminal, type SessionAnnotationApi } from '../TerminalAnnotations/AnnotatedTerminal';
import { TerminalStaleBuildNotice } from '../TerminalStaleBuildNotice';
import { RenamePopover } from '../RenamePopover';
import { StateIndicator } from '../StateIndicator';
import { useShortcut } from '../../shortcuts/useShortcut';
import {
  getNormalizedPaneBounds,
  collectPreferredSplitIds,
  collectSplitRatios,
  findLeafInDirection,
  leafSlotId,
  tileContentKey,
  type SplitDivider,
  type TerminalNavigationDirection,
  type TerminalLayoutNode,
  type TileLeaf,
  type TileContentState,
  type NormalizedPaneBounds,
  type TerminalSplitDirection,
  type TerminalDockEdge,
  type TerminalWorkspaceState,
} from '../../types/workspace';
import type { SessionAgent } from '../../types/sessionAgent';
import type { UISessionState } from '../../types/sessionState';
import { HeaderNudgeIndicator, deriveNudgeMode } from '../NudgeIndicator';
import { HeaderSettleKeptChip, HeaderSettlingIndicator } from '../SettlingIndicator';
import { HeaderPresentationChip } from '../PresentationChip';
import { PaneSeedChip } from '../PaneSeedChip';
import { derivePaneSeedDisplay } from '../paneSeedDisplay';
import type { Seed } from '../../hooks/useDaemonSocket';
import type { Presentation, SessionUsage } from '../../types/generated';
import { useGhosttyPaneRuntime } from './useGhosttyPaneRuntime';
import type { PaneRuntimeEventRouter } from './paneRuntimeEventRouter';
import { isSuspiciousTerminalSize } from '../../utils/terminalDebug';
import { lockTextSelection } from '../../utils/dragLock';
import { ConversationPane } from '../ConversationPane';
import './SessionTerminalWorkspace.css';
import type { TerminalVisibleContentSnapshot } from '../../utils/terminalVisibleContent';
import type { TerminalVisibleStyleSnapshot } from '../../utils/terminalStyleSummary';
import type { ResolvedTheme } from '../../utils/terminalSizing';
import { WorkspaceLayoutRenderer } from './WorkspaceLayoutRenderer';
import { WorkspaceDockTile, type WorkspaceTileSessionOption } from './WorkspaceDockTile';
import { startLeafDrag, type LeafDropSnapshot } from './leafDrag';
import type { DockTarget } from './dockTarget';
import type { WorkspaceSelectionStyle } from '../../utils/workspaceSelectionStyle';
import { HeaderSessionUsage } from './SessionUsage';
import type {
  AutomationProvenance as AutomationProvenanceValue,
  SessionPullRequest,
} from '../../types/generated';
import { SessionProvenance } from '../SessionProvenance';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import {
  applyDragSuspension,
  dragRatioBounds,
  releaseSuspendedLeaf,
  resolveWorkspaceLayout,
  type AttentionViewport,
} from './attentionLayout';
import { formatShortcut } from '../../shortcuts/formatShortcut';

const RESIZE_MOUSE_SUPPRESSION_MS = 1_500;
// Only swallows the trailing pointerup/synthetic click from the release itself,
// so a deliberate click right after resizing is not dropped.
const RESIZE_MOUSE_RELEASE_GUARD_MS = 150;

function suppressTerminalMouseDuringResize(durationMs = RESIZE_MOUSE_SUPPRESSION_MS): void {
  document.documentElement.dataset.attnWorkspaceMouseSuppressUntil = String(
    Date.now() + durationMs,
  );
}

// An effect dependency, so the no-op stand-in has to keep one identity.
const noRequestContent = () => {};
const EMPTY_SUSPENDED_LEAF_IDS: ReadonlySet<string> = new Set();

export interface SessionTerminalWorkspaceHandle {
  fitPane: (paneId: string) => void;
  fitActivePane: () => void;
  focusLeaf: (leafId: string) => void;
  focusPane: (paneId: string, retries?: number) => void;
  focusActivePane: (retries?: number) => void;
  typePaneTextViaUI: (paneId: string, text: string) => boolean;
  isPaneInputFocused: (paneId: string) => boolean;
  scrollPaneToTop: (paneId: string) => boolean;
  getPaneText: (paneId: string) => string;
  getPaneSize: (paneId: string) => { cols: number; rows: number } | null;
  getPaneVisibleContent: (paneId: string) => TerminalVisibleContentSnapshot;
  getPaneVisibleStyleSummary: (paneId: string) => TerminalVisibleStyleSnapshot;
  getPaneBlockState: (paneId: string) => BlockStateSnapshot | null;
  getPanePlacementState: (paneId: string) => PlacementStateSnapshot | null;
  resetPaneTerminal: (paneId: string) => boolean;
  injectPaneBytes: (paneId: string, bytes: Uint8Array) => Promise<boolean>;
  injectPaneBase64: (paneId: string, payload: string) => Promise<boolean>;
  drainPaneTerminal: (paneId: string) => Promise<boolean>;
  getLeafDropSnapshot: () => LeafDropSnapshot | null;
}

interface SessionTerminalWorkspaceProps {
  workspaceId: string;
  workspaceDirectory?: string;
  workspaceSessions?: Array<{
    id: string;
    label: string;
    agent: SessionAgent;
    cwd: string;
    endpointId?: string;
    state?: UISessionState;
    usage?: SessionUsage;
    ticketUnread?: boolean;
    nudgeFiresAt?: string;
    autoSettleFiresAt?: string;
    autoSettleHeld?: boolean;
    autoSettleDismissArmed?: boolean;
    terminalBuildStale?: boolean;
    isActive?: boolean;
    presentation?: Presentation;
    seedId?: string;
    crewMember?: string;
    automation?: AutomationProvenanceValue;
    pullRequests?: SessionPullRequest[];
  }>;
  seedTargetSessions?: WorkspaceTileSessionOption[];
  gardenSeeds?: Seed[];
  onOpenSeed?: (seedId: string) => void;
  onRevealSeedInGarden?: (seedId: string) => void;
  seedPopoverRequest?: { sessionId: string; nonce: number };
  usagePopoverRequest?: { sessionId: string; nonce: number };
  // The daemon decides this from the driver's `conversation` capability; never
  // recompute it here.
  conversationAgents?: ReadonlySet<string>;
  annotationApi?: SessionAnnotationApi;
  workspace: TerminalWorkspaceState;
  workspaceSelectionStyle?: WorkspaceSelectionStyle;
  activePaneId: string;
  fontSize: number;
  resolvedTheme?: ResolvedTheme;
  focusRequestToken?: number;
  enabled: boolean;
  isActiveSession: boolean;
  isSessionViewVisible?: boolean;
  terminalsLive?: boolean;
  eventRouter: PaneRuntimeEventRouter;
  onSplitPane: (targetPaneId: string, direction: TerminalSplitDirection) => void;
  onClosePane: (paneId: string) => void;
  onFocusPane: (paneId: string) => void;
  onRenameSession?: (sessionId: string, label: string) => Promise<void>;
  onTriggerNudge?: (sessionId: string) => void;
  onCancelCountdown?: (sessionId: string) => void;
  onTerminalPointerActivity?: (sessionId: string) => void;
  onOpenPresentation?: (presentationId: string) => void;
  // Empty sessionId lets the daemon use the selected session.
  onOpenMarkdown?: (path: string, sessionId: string) => void;
  onTerminalModelRecovered?: () => void;
  zoomActive?: boolean;
  onSetZoomActive?: (active: boolean) => void;
  onNavigateOutOfSession: (direction: TerminalNavigationDirection) => void;
  onResizeSplit?: (splitId: string, ratio: number) => Promise<unknown> | void;
  // anchorId '' docks against the whole workspace; ratio is the moved leaf's
  // fraction of the new split.
  onMoveLeaf?: (leafId: string, anchorId: string, edge: TerminalDockEdge, ratio: number) => void;
  getActiveLeafDropSnapshot?: () => LeafDropSnapshot | null;
  onLeafDragStart?: (leafId: string) => void;
  onLeafDragGhostMove?: (clientX: number, clientY: number) => void;
  onLeafDragPreview?: (target: DockTarget | null) => void;
  onLeafDragEnd?: () => void;
  leafDragPreview?: {
    draggingLeafId: string | null;
    dockTarget: DockTarget | null;
    ghostPos: { x: number; y: number } | null;
  } | null;
  onUndockTile?: (tileId: string) => void;
  onUpdateTile?: (tileId: string, tileParams: string, tileSessionId?: string) => Promise<unknown> | void;
  tileContents?: Record<string, TileContentState>;
  allowLocalTileTargets?: boolean;
  onRequestTileContent?: (workspaceId: string, tileId: string) => void;
}

const EMPTY_SEED_TARGET_SESSIONS: WorkspaceTileSessionOption[] = [];
const EMPTY_GARDEN_SEEDS: Seed[] = [];

export const SessionTerminalWorkspace = forwardRef<SessionTerminalWorkspaceHandle, SessionTerminalWorkspaceProps>(
  function SessionTerminalWorkspace({
    workspaceId,
    workspaceDirectory,
    workspaceSessions = [],
    seedTargetSessions = EMPTY_SEED_TARGET_SESSIONS,
    gardenSeeds = EMPTY_GARDEN_SEEDS,
    onOpenSeed,
    onRevealSeedInGarden,
    seedPopoverRequest,
    usagePopoverRequest,
    conversationAgents,
    annotationApi,
    workspace,
    workspaceSelectionStyle = 'rail',
    activePaneId,
    fontSize,
    resolvedTheme,
    focusRequestToken,
    enabled,
    isActiveSession,
    isSessionViewVisible = true,
    terminalsLive = true,
    eventRouter,
    onSplitPane,
    onClosePane,
    onFocusPane,
    onRenameSession,
    onTriggerNudge,
    onCancelCountdown,
    onTerminalPointerActivity,
    onOpenPresentation,
    onOpenMarkdown,
    onTerminalModelRecovered,
    zoomActive = false,
    onSetZoomActive,
    onNavigateOutOfSession,
    onResizeSplit,
    onMoveLeaf,
    getActiveLeafDropSnapshot,
    onLeafDragStart,
    onLeafDragGhostMove,
    onLeafDragPreview,
    onLeafDragEnd,
    leafDragPreview,
    onUndockTile,
    onUpdateTile,
    tileContents,
    allowLocalTileTargets = true,
    onRequestTileContent,
  }, ref) {
    const [maximizedLeafId, setMaximizedLeafId] = useState<string | null>(null);
    const [activeTile, setActiveTile] = useState<{ tileId: string; whileActivePaneId: string } | null>(null);
    const [paneReadyFocusRequest, setPaneReadyFocusRequest] = useState(0);
    const [renamePane, setRenamePane] = useState<{
      sessionId: string;
      name: string;
      anchor: { top: number; left: number };
    } | null>(null);
    const [pendingRatioOverrides, setPendingRatioOverrides] = useState<Map<string, number>>(() => new Map());
    const [resizingSplit, setResizingSplit] = useState<{ splitId: string; direction: TerminalSplitDirection } | null>(null);
    const [staleBuildDismissed, setStaleBuildDismissed] = useState<ReadonlySet<string>>(() => new Set());
    const [pinnedSeedPopover, setPinnedSeedPopover] = useState<string | null>(null);
    useEffect(() => {
      if (seedPopoverRequest) setPinnedSeedPopover(seedPopoverRequest.sessionId);
    }, [seedPopoverRequest]);
    const [pinnedUsagePopover, setPinnedUsagePopover] = useState<string | null>(null);
    useEffect(() => {
      if (usagePopoverRequest) setPinnedUsagePopover(usagePopoverRequest.sessionId);
    }, [usagePopoverRequest]);
    const [draggingLeafId, setDraggingLeafId] = useState<string | null>(null);
    const [dockTarget, setDockTarget] = useState<DockTarget | null>(null);
    const [ghostPos, setGhostPos] = useState<{ x: number; y: number } | null>(null);
    const [attentionViewport, setAttentionViewport] = useState<AttentionViewport>({ width: 0, height: 0 });
    const [attentionRevision, setAttentionRevision] = useState(0);
    const attentionFocusOrderRef = useRef<string[]>([]);
    const suspendedLeafIdsRef = useRef<ReadonlySet<string>>(EMPTY_SUSPENDED_LEAF_IDS);
    const previousAnnotatedTileIdsRef = useRef<ReadonlySet<string> | null>(null);
    const automaticTileFocusRef = useRef<{ tileId: string; whileActivePaneId: string } | null>(null);
    const pendingPaneFocusRef = useRef<{
      leafId: string;
      fromActivePaneId: string;
    } | null>(null);
    const focusLeafRequestRef = useRef<(leafId: string) => void>(() => {});
    const tileDragCleanupRef = useRef<(() => void) | null>(null);
    const tileBodyRefs = useRef(new Map<string, HTMLDivElement>());
    const tileBodyRefCallbacks = useRef(new Map<string, (node: HTMLDivElement | null) => void>());
    const panesContainerRef = useRef<HTMLDivElement | null>(null);
    const draggingSplitRef = useRef<string | null>(null);
    const activePaneIdRef = useRef(activePaneId);
    const layoutTreeRef = useRef<TerminalLayoutNode | null>(null);
    const activeLeafIdRef = useRef('');
    const pinnedLeafIdsRef = useRef<ReadonlySet<string>>(new Set());
    const isActiveSessionRef = useRef(isActiveSession);
    const sessionViewVisibleRef = useRef(isSessionViewVisible);

    activePaneIdRef.current = activePaneId;
    isActiveSessionRef.current = isActiveSession;
    sessionViewVisibleRef.current = isSessionViewVisible;

    const paneIds = useMemo(() => {
      const ids: string[] = [];
      if (!workspace.layoutTree) {
        return ids;
      }
      const collect = (node: TerminalLayoutNode) => {
        if (node.type === 'split') {
          collect(node.children[0]);
          collect(node.children[1]);
          return;
        }
        if (node.type === 'pane') {
          ids.push(node.paneId);
        }
      };
      collect(workspace.layoutTree);
      return ids;
    }, [workspace.layoutTree]);

    const sessionById = useMemo(() => new Map(
      workspaceSessions.map((entry) => [entry.id, entry] as const),
    ), [workspaceSessions]);

    const agentPanes = useMemo(() => workspace.agents, [workspace.agents]);

    const agentPaneById = useMemo(
      () => new Map(agentPanes.map((pane) => [pane.id, pane])),
      [agentPanes],
    );

    const tileSessionOptions = useMemo(() => {
      const seen = new Set<string>();
      const options: { sessionId: string; label: string; state?: string }[] = [];
      for (const pane of agentPanes) {
        if (seen.has(pane.sessionId)) {
          continue;
        }
        seen.add(pane.sessionId);
        const session = sessionById.get(pane.sessionId);
        options.push({
          sessionId: pane.sessionId,
          label: session?.label || pane.title || pane.sessionId,
          ...(session?.state ? { state: session.state } : {}),
        });
      }
      return options;
    }, [agentPanes, sessionById]);

    const activePaneSessionId = useMemo(
      () => agentPaneById.get(activePaneId)?.sessionId ?? null,
      [agentPaneById, activePaneId],
    );

    const tileLeafById = useMemo(() => {
      const map = new Map<string, TileLeaf>();
      const walk = (node: TerminalLayoutNode | null) => {
        if (!node) {
          return;
        }
        if (node.type === 'split') {
          walk(node.children[0]);
          walk(node.children[1]);
          return;
        }
        if (node.type === 'tile') {
          map.set(node.tileId, node);
        }
      };
      walk(workspace.layoutTree);
      return map;
    }, [workspace.layoutTree]);

    const annotatedTileIds = useMemo(() => {
      const ids: string[] = [];
      for (const tile of tileLeafById.values()) {
        if (tile.tileKind === 'markdown' || tile.tileKind === 'seed') {
          ids.push(tile.tileId);
        }
      }
      return ids;
    }, [tileLeafById]);
    const annotatedTileIdSet = useMemo(() => new Set(annotatedTileIds), [annotatedTileIds]);
    let openedAnnotatedTileId: string | null = null;
    const previousAnnotatedTileIds = previousAnnotatedTileIdsRef.current;
    if (previousAnnotatedTileIds) {
      for (let index = annotatedTileIds.length - 1; index >= 0; index -= 1) {
        const tileId = annotatedTileIds[index];
        if (!previousAnnotatedTileIds.has(tileId)) {
          openedAnnotatedTileId = tileId;
          break;
        }
      }
    }
    const automaticTileFocus = openedAnnotatedTileId
      ? { tileId: openedAnnotatedTileId, whileActivePaneId: activePaneId }
      : automaticTileFocusRef.current;

    useLayoutEffect(() => {
      previousAnnotatedTileIdsRef.current = annotatedTileIdSet;
      if (openedAnnotatedTileId) {
        automaticTileFocusRef.current = {
          tileId: openedAnnotatedTileId,
          whileActivePaneId: activePaneId,
        };
      } else if (
        automaticTileFocusRef.current
        && !annotatedTileIdSet.has(automaticTileFocusRef.current.tileId)
      ) {
        automaticTileFocusRef.current = null;
      }
    }, [activePaneId, annotatedTileIdSet, openedAnnotatedTileId]);

    const firstTileId = useMemo(
      () => (tileLeafById.size > 0 ? tileLeafById.keys().next().value ?? null : null),
      [tileLeafById],
    );

    const focusedTileId = automaticTileFocus
      && automaticTileFocus.whileActivePaneId === activePaneId
      && tileLeafById.has(automaticTileFocus.tileId)
      ? automaticTileFocus.tileId
      : activeTile
      && activeTile.whileActivePaneId === activePaneId
      && tileLeafById.has(activeTile.tileId)
        ? activeTile.tileId
        : null;
    const pendingPaneFocus = pendingPaneFocusRef.current;
    const focusedPaneId = pendingPaneFocus
      && agentPaneById.has(pendingPaneFocus.leafId)
      && (
        activePaneId === pendingPaneFocus.fromActivePaneId
        || activePaneId === pendingPaneFocus.leafId
      )
      ? pendingPaneFocus.leafId
      : activePaneId;
    const activeLeafId = focusedTileId || focusedPaneId || firstTileId || '';
    const activeLeafIsTile = tileLeafById.has(activeLeafId);
    useLayoutEffect(() => {
      layoutTreeRef.current = workspace.layoutTree ?? null;
      activeLeafIdRef.current = activeLeafId;
    }, [workspace.layoutTree, activeLeafId]);

    useLayoutEffect(() => {
      const pending = pendingPaneFocusRef.current;
      if (
        pending
        && (
          activePaneId === pending.leafId
          || activePaneId !== pending.fromActivePaneId
          || !agentPaneById.has(pending.leafId)
        )
      ) {
        pendingPaneFocusRef.current = null;
      }
    }, [activePaneId, agentPaneById]);

    // Conversation panes are deliberately absent: they have no PTY, and attaching
    // against a session the daemon has none for fails and takes the pane down.
    const runtimePanes = useMemo(() => {
      const panes = [];
      for (const pane of agentPanes) {
        if (pane.status && pane.status !== 'ready') continue;
        const paneSession = sessionById.get(pane.sessionId);
        if (conversationAgents?.has(paneSession?.agent ?? '')) continue;
        panes.push({
          paneId: pane.id,
          runtimeId: pane.runtimeId,
          paneKind: 'agent' as const,
          agent: paneSession?.agent ?? 'shell',
          sessionId: pane.sessionId,
          testSessionId: pane.sessionId,
          state: paneSession?.state,
        });
      }
      return panes;
    }, [agentPanes, conversationAgents, sessionById]);

    const runtime = useGhosttyPaneRuntime(
      runtimePanes,
      activePaneId,
      eventRouter,
      isActiveSessionRef,
      terminalsLive,
    );
    const setTerminalHandleRef = useRef(runtime.setTerminalHandle);
    const terminalRefCallbacksRef = useRef(new Map<
      string,
      (handle: GhosttyTerminalHandle | null) => void
    >());
    setTerminalHandleRef.current = runtime.setTerminalHandle;
    const terminalRefForPane = useCallback((paneId: string) => {
      const existing = terminalRefCallbacksRef.current.get(paneId);
      if (existing) return existing;
      const callback = (handle: GhosttyTerminalHandle | null) => {
        setTerminalHandleRef.current(paneId, handle);
      };
      terminalRefCallbacksRef.current.set(paneId, callback);
      return callback;
    }, []);
    const fitPane = runtime.fitPane;
    const scheduleTerminalFitAfterResize = useCallback(() => {
      window.requestAnimationFrame(() => {
        for (const pane of runtimePanes) {
          fitPane(pane.paneId);
        }
      });
    }, [fitPane, runtimePanes]);
    const getPaneSize = runtime.getPaneSize;
    const setPaneSurfaceReleased = runtime.setPaneSurfaceReleased;
    const paneOverflowsContainer = runtime.paneOverflowsContainer;
    const splitLayoutActive = workspace.layoutTree?.type === 'split';
    const showPaneHeader = paneIds.length + tileLeafById.size > 1;
    const leafIds = useMemo(
      () => [...paneIds, ...tileLeafById.keys()],
      [paneIds, tileLeafById],
    );
    const leafIdSet = useMemo(() => new Set(leafIds), [leafIds]);
    const attentionActiveLeafId = activeLeafId;
    const attentionFocusOrder = useMemo(() => {
      if (!attentionActiveLeafId) {
        return [];
      }
      return [
        attentionActiveLeafId,
        ...attentionFocusOrderRef.current.filter((id) => id !== attentionActiveLeafId),
      ].filter((id) => leafIdSet.has(id));
    }, [attentionActiveLeafId, leafIdSet]);
    useLayoutEffect(() => {
      attentionFocusOrderRef.current = attentionFocusOrder;
    }, [attentionFocusOrder]);
    const effectivePaneId = maximizedLeafId && leafIdSet.has(maximizedLeafId) ? maximizedLeafId : null;
    const effectiveZoomedPaneId = zoomActive && leafIdSet.has(activeLeafId) ? activeLeafId : null;
    const layoutPlan = useMemo(() => {
      if (!workspace.layoutTree) {
        return null;
      }
      return resolveWorkspaceLayout({
        sourceTree: workspace.layoutTree,
        viewport: attentionViewport,
        activeLeafId: attentionActiveLeafId,
        focusOrder: attentionFocusOrder.slice(1),
        previousSuspendedLeafIds: suspendedLeafIdsRef.current,
        pinnedLeafIds: pinnedLeafIdsRef.current,
        holdRestores: resizingSplit !== null,
        pendingRatioOverrides,
        view: effectivePaneId
          ? { mode: 'focused', leafId: effectivePaneId }
          : effectiveZoomedPaneId
            ? { mode: 'zoomed', leafId: effectiveZoomedPaneId }
            : { mode: 'normal' },
      });
    }, [
      attentionActiveLeafId,
      attentionFocusOrder,
      attentionViewport,
      effectivePaneId,
      effectiveZoomedPaneId,
      pendingRatioOverrides,
      attentionRevision,
      resizingSplit,
      workspace.layoutTree,
    ]);
    const suspendedLeafIds = layoutPlan?.suspendedLeafIds ?? suspendedLeafIdsRef.current;
    useLayoutEffect(() => {
      if (layoutPlan) {
        suspendedLeafIdsRef.current = layoutPlan.suspendedLeafIds;
        const prunedPins = [...pinnedLeafIdsRef.current]
          .filter((id) => layoutPlan.suspendedLeafIds.has(id));
        if (prunedPins.length !== pinnedLeafIdsRef.current.size) {
          pinnedLeafIdsRef.current = new Set(prunedPins);
        }
      }
    }, [layoutPlan]);
    const renderedLayoutTree = layoutPlan?.renderedTree ?? null;

    useLayoutEffect(() => {
      const container = panesContainerRef.current;
      if (!container) {
        return;
      }
      const update = (width: number, height: number) => {
        setAttentionViewport((current) => (
          current.width === width && current.height === height ? current : { width, height }
        ));
      };
      const rect = container.getBoundingClientRect();
      update(Math.round(rect.width), Math.round(rect.height));
      if (typeof ResizeObserver === 'undefined') {
        return;
      }
      const observer = new ResizeObserver(([entry]) => {
        update(Math.round(entry.contentRect.width), Math.round(entry.contentRect.height));
      });
      observer.observe(container);
      return () => observer.disconnect();
    }, [workspaceId, effectivePaneId]);

    const clearRatioOverride = useCallback((splitId: string, expectedRatio?: number) => {
      setPendingRatioOverrides((prev) => {
        const current = prev.get(splitId);
        if (current == null || (expectedRatio != null && Math.abs(current - expectedRatio) >= 0.005)) {
          return prev;
        }
        const next = new Map(prev);
        next.delete(splitId);
        return next;
      });
    }, []);

    // A matching preferred echo settles the optimistic value; different
    // authority supersedes it.
    useEffect(() => {
      setPendingRatioOverrides((prev) => {
        if (prev.size === 0 || !workspace.layoutTree) {
          return prev;
        }
        let changed = false;
        const next = new Map(prev);
        const authoritative = collectSplitRatios(workspace.layoutTree);
        const preferred = collectPreferredSplitIds(workspace.layoutTree);
        for (const splitId of prev.keys()) {
          if (splitId === draggingSplitRef.current) {
            continue;
          }
          const ratio = authoritative.get(splitId);
          const matches = ratio != null && Math.abs(ratio - (prev.get(splitId) ?? ratio)) < 0.005;
          if (!matches || preferred.has(splitId)) {
            next.delete(splitId);
            changed = true;
          }
        }
        return changed ? next : prev;
      });
    }, [workspace.layoutTree]);
    const panePaths = useMemo(() => {
      const paths = new Map<string, string>();
      if (!renderedLayoutTree) {
        return paths;
      }
      const walk = (node: TerminalLayoutNode, path: string) => {
        if (node.type === 'split') {
          walk(node.children[0], path + '/0');
          walk(node.children[1], path + '/1');
          return;
        }
        paths.set(leafSlotId(node), path);
      };
      walk(renderedLayoutTree, 'root');
      return paths;
    }, [renderedLayoutTree]);
    const renderedPaneBounds = useMemo(() => (
      renderedLayoutTree ? getNormalizedPaneBounds(renderedLayoutTree) : new Map<string, NormalizedPaneBounds>()
    ), [renderedLayoutTree]);
    const paneGeometry = useMemo(() => {
      const geometry = new Map<string, string>();
      for (const [paneId, path] of panePaths) {
        const bounds = renderedPaneBounds.get(paneId);
        geometry.set(paneId, bounds
          ? `${path}:${bounds.left}:${bounds.top}:${bounds.right}:${bounds.bottom}`
          : path);
      }
      return geometry;
    }, [panePaths, renderedPaneBounds]);
    const renderedPaneIds = useMemo(() => Array.from(panePaths.keys()), [panePaths]);
    const renderedPaneIdsKey = renderedPaneIds.join('|');
    const suspendedLeafIdsKey = [...suspendedLeafIds].sort().join('|');
    const prevPaneGeometryRef = useRef(paneGeometry);
    const sessionVisibleRef = useRef(false);
    const sessionVisible = enabled && isActiveSession && isSessionViewVisible;

    useImperativeHandle(ref, () => ({
      fitPane: runtime.fitPane,
      fitActivePane: runtime.fitActivePane,
      focusLeaf: (leafId) => focusLeafRequestRef.current(leafId),
      focusPane: runtime.focusPane,
      focusActivePane: runtime.focusPane.bind(null, activePaneId),
      typePaneTextViaUI: runtime.typeTextViaPaneInput,
      isPaneInputFocused: runtime.isPaneInputFocused,
      scrollPaneToTop: runtime.scrollPaneToTop,
      getPaneText: runtime.getPaneText,
      getPaneSize: runtime.getPaneSize,
      getPaneVisibleContent: runtime.getPaneVisibleContent,
      getPaneVisibleStyleSummary: runtime.getPaneVisibleStyleSummary,
      getPaneBlockState: runtime.getPaneBlockState,
      getPanePlacementState: runtime.getPanePlacementState,
      resetPaneTerminal: runtime.resetPaneTerminal,
      injectPaneBytes: runtime.injectPaneBytes,
      injectPaneBase64: runtime.injectPaneBase64,
      drainPaneTerminal: runtime.drainPaneTerminal,
      getLeafDropSnapshot: () => (
        panesContainerRef.current
          ? { container: panesContainerRef.current, paneBounds: renderedPaneBounds }
          : null
      ),
    }), [activePaneId, renderedPaneBounds, runtime]);

    useEffect(() => {
      if (!maximizedLeafId) {
        return;
      }
      if (!leafIds.includes(maximizedLeafId)) {
        setMaximizedLeafId(null);
      }
    }, [maximizedLeafId, leafIds]);

    // activePaneId does not change here, so without releasing the focused tile it
    // stays the active leaf and Cmd+W undocks it while you type in the terminal.
    const focusActivePaneSurface = useCallback(() => {
      runtime.focusPane(activePaneId, 0);
    }, [activePaneId, runtime]);

    const focusActivePane = useCallback(() => {
      const focusOverrideActive = automaticTileFocusRef.current !== null
        || pendingPaneFocusRef.current !== null;
      automaticTileFocusRef.current = null;
      pendingPaneFocusRef.current = null;
      if (focusOverrideActive) {
        setAttentionRevision((current) => current + 1);
      }
      setActiveTile(null);
      focusActivePaneSurface();
    }, [focusActivePaneSurface]);

    // The scrollable body is what satisfies the shortcut dispatcher's
    // terminal-target check, so ⌘W reaches the workspace, not session.close.
    const focusTile = useCallback((tileId: string) => {
      tileBodyRefs.current.get(tileId)?.focus({ preventScroll: true });
    }, []);

    const tileBodyRefFor = useCallback((tileId: string) => {
      const existing = tileBodyRefCallbacks.current.get(tileId);
      if (existing) return existing;
      const callback = (node: HTMLDivElement | null) => {
        if (node) {
          tileBodyRefs.current.set(tileId, node);
        } else {
          tileBodyRefs.current.delete(tileId);
        }
      };
      tileBodyRefCallbacks.current.set(tileId, callback);
      return callback;
    }, []);

    useEffect(() => {
      if (!sessionVisible) {
        return;
      }
      if (activeLeafIsTile) {
        focusTile(activeLeafId);
        return;
      }
      if (activePaneId) {
        focusActivePaneSurface();
      }
    }, [activeLeafId, activeLeafIsTile, activePaneId, focusActivePaneSurface, focusTile, focusRequestToken, isActiveSession, isSessionViewVisible, paneReadyFocusRequest, suspendedLeafIdsKey, workspaceId, sessionVisible]);

    // A pane whose grid overflows its container is not retried by fit()'s reveal
    // path — it stays clipped until something unrelated refits it.
    const refitPanesNowAndIfStillWrong = useCallback((targetPaneIds: string[]) => {
      const paneIdsToFit = Array.from(new Set(targetPaneIds));
      if (paneIdsToFit.length === 0) {
        return undefined;
      }

      for (const paneId of paneIdsToFit) {
        fitPane(paneId);
      }

      const stillWrong = (paneId: string) => {
        const size = getPaneSize(paneId);
        return (size != null && isSuspiciousTerminalSize(size.cols, size.rows)) || paneOverflowsContainer(paneId);
      };

      const lateRefitTimeout = window.setTimeout(() => {
        for (const paneId of paneIdsToFit) {
          if (stillWrong(paneId)) {
            fitPane(paneId);
          }
        }
      }, 75);

      // Covers layout settles the 75ms tick misses.
      const secondLateRefitTimeout = window.setTimeout(() => {
        for (const paneId of paneIdsToFit) {
          if (stillWrong(paneId)) {
            fitPane(paneId);
          }
        }
      }, 400);

      return () => {
        window.clearTimeout(lateRefitTimeout);
        window.clearTimeout(secondLateRefitTimeout);
      };
    }, [fitPane, getPaneSize, paneOverflowsContainer]);

    // Keyed on `isActiveSession`, not `sessionVisible` (which also goes false behind a
    // modal), and declared before the refit effect so a revealed pane measures with its buffer.
    useLayoutEffect(() => {
      for (const paneId of renderedPaneIds) {
        setPaneSurfaceReleased(paneId, !isActiveSession || suspendedLeafIds.has(paneId));
      }
    }, [isActiveSession, renderedPaneIds, renderedPaneIdsKey, setPaneSurfaceReleased, suspendedLeafIds, suspendedLeafIdsKey]);

    // A fold or restore eases pane frames over 160ms; panes measure their old
    // size mid-transition, so the settle refit below is unconditional.
    const suspensionAnimationTimeoutRef = useRef<number | null>(null);
    const prevSuspendedKeyRef = useRef(suspendedLeafIdsKey);
    useLayoutEffect(() => {
      if (prevSuspendedKeyRef.current === suspendedLeafIdsKey) {
        return;
      }
      prevSuspendedKeyRef.current = suspendedLeafIdsKey;
      const container = panesContainerRef.current;
      if (!container || !sessionVisible) {
        return;
      }
      container.dataset.suspensionAnimating = '1';
      if (suspensionAnimationTimeoutRef.current != null) {
        window.clearTimeout(suspensionAnimationTimeoutRef.current);
      }
      const visiblePaneIds = renderedPaneIds.filter((paneId) => !suspendedLeafIds.has(paneId));
      suspensionAnimationTimeoutRef.current = window.setTimeout(() => {
        suspensionAnimationTimeoutRef.current = null;
        delete container.dataset.suspensionAnimating;
        refitPanesNowAndIfStillWrong(visiblePaneIds);
      }, 200);
    }, [refitPanesNowAndIfStillWrong, renderedPaneIds, sessionVisible, suspendedLeafIds, suspendedLeafIdsKey]);
    useEffect(() => () => {
      if (suspensionAnimationTimeoutRef.current != null) {
        window.clearTimeout(suspensionAnimationTimeoutRef.current);
      }
    }, []);

    useLayoutEffect(() => {
      if (!sessionVisible) {
        sessionVisibleRef.current = false;
        return;
      }
      if (sessionVisibleRef.current) {
        return;
      }
      sessionVisibleRef.current = true;
      return refitPanesNowAndIfStillWrong(renderedPaneIds);
    }, [refitPanesNowAndIfStillWrong, renderedPaneIds, renderedPaneIdsKey, sessionVisible]);

    useLayoutEffect(() => {
      if (!sessionVisible) {
        prevPaneGeometryRef.current = paneGeometry;
        return;
      }
      if (resizingSplit) {
        prevPaneGeometryRef.current = paneGeometry;
        return;
      }
      const prev = prevPaneGeometryRef.current;
      prevPaneGeometryRef.current = paneGeometry;
      const changedPanes: string[] = [];
      for (const [paneId, geometry] of paneGeometry) {
        if (prev.get(paneId) !== geometry) {
          changedPanes.push(paneId);
        }
      }
      if (changedPanes.length === 0) {
        return;
      }
      return refitPanesNowAndIfStillWrong(changedPanes);
    }, [paneGeometry, refitPanesNowAndIfStillWrong, resizingSplit, sessionVisible]);

    // Splitting needs an agent pane to anchor on: with a tile focused this is
    // deliberately a no-op.
    const handleSplit = useCallback((direction: TerminalSplitDirection) => {
      if (activeLeafIsTile || !activeLeafId) {
        return;
      }
      onSplitPane(activeLeafId, direction);
    }, [activeLeafId, activeLeafIsTile, onSplitPane]);

    const handleClosePane = useCallback((paneId: string) => {
      onClosePane(paneId);
    }, [onClosePane]);

    const handleCloseFocusedLeaf = useCallback(() => {
      if (!activeLeafId) {
        return;
      }
      if (activeLeafIsTile) {
        onUndockTile?.(activeLeafId);
        return;
      }
      handleClosePane(activeLeafId);
    }, [activeLeafId, activeLeafIsTile, handleClosePane, onUndockTile]);

    const toggleMaximizeActivePane = useCallback(() => {
      onSetZoomActive?.(false);
      setMaximizedLeafId((current) => (current ? null : activeLeafId));
    }, [activeLeafId, onSetZoomActive]);

    const focusDocument = useCallback((tileId: string) => {
      automaticTileFocusRef.current = null;
      pendingPaneFocusRef.current = null;
      onSetZoomActive?.(false);
      setActiveTile({ tileId, whileActivePaneId: activePaneId });
      setMaximizedLeafId(tileId);
      window.requestAnimationFrame(() => focusTile(tileId));
    }, [activePaneId, focusTile, onSetZoomActive]);

    useEscapeStack(() => setMaximizedLeafId(null), effectivePaneId !== null);

    const toggleZoomActivePane = useCallback(() => {
      setMaximizedLeafId(null);
      onSetZoomActive?.(!zoomActive);
    }, [onSetZoomActive, zoomActive]);

    const focusLeaf = useCallback((leafId: string) => {
      suspendedLeafIdsRef.current = releaseSuspendedLeaf(suspendedLeafIdsRef.current, leafId);
      if (pinnedLeafIdsRef.current.has(leafId)) {
        pinnedLeafIdsRef.current = new Set([...pinnedLeafIdsRef.current].filter((id) => id !== leafId));
      }
      automaticTileFocusRef.current = null;
      if (tileLeafById.has(leafId)) {
        pendingPaneFocusRef.current = null;
        setAttentionRevision((current) => current + 1);
        setActiveTile({ tileId: leafId, whileActivePaneId: activePaneId });
        focusTile(leafId);
        return;
      }
      pendingPaneFocusRef.current = { leafId, fromActivePaneId: activePaneId };
      setAttentionRevision((current) => current + 1);
      setActiveTile(null);
      onFocusPane(leafId);
      runtime.focusPane(leafId);
    }, [activePaneId, focusTile, onFocusPane, runtime, tileLeafById]);
    useLayoutEffect(() => {
      focusLeafRequestRef.current = focusLeaf;
    }, [focusLeaf]);

    const handleMovePane = useCallback((direction: TerminalNavigationDirection) => {
      if (!renderedLayoutTree) {
        return;
      }
      const nextLeafId = findLeafInDirection(renderedLayoutTree, activeLeafId, direction);
      if (nextLeafId) {
        focusLeaf(nextLeafId);
        return;
      }
      onNavigateOutOfSession(direction);
    }, [activeLeafId, focusLeaf, onNavigateOutOfSession, renderedLayoutTree]);

    // Deciding here would read leaf state from a ref a child's ready callback can
    // observe before the parent mirrors it, leaving Cmd+W on the wrong leaf.
    const requestFocusForReadyPane = useCallback((paneId: string) => {
      if (!isActiveSessionRef.current || !sessionViewVisibleRef.current || activePaneIdRef.current !== paneId) {
        return;
      }
      setPaneReadyFocusRequest((token) => token + 1);
    }, []);

    const handleGhosttyTerminalReady = useCallback((paneId: string) => (terminal: GhosttyTerminalHandle) => {
      void runtime.handleTerminalReady(paneId)(terminal);
      requestFocusForReadyPane(paneId);
    }, [requestFocusForReadyPane, runtime]);

    useShortcut('terminal.open', focusActivePane, sessionVisible);
    useShortcut('terminal.find', () => { runtime.openFindInActivePane(); }, sessionVisible);
    useShortcut('terminal.splitVertical', () => { handleSplit('vertical'); }, sessionVisible);
    useShortcut('terminal.splitHorizontal', () => { handleSplit('horizontal'); }, sessionVisible);
    useShortcut('terminal.toggleZoom', toggleZoomActivePane, sessionVisible);
    useShortcut('terminal.toggleMaximize', toggleMaximizeActivePane, sessionVisible);
    useShortcut('terminal.close', handleCloseFocusedLeaf, sessionVisible && splitLayoutActive);
    useShortcut('terminal.focusLeft', () => handleMovePane('left'), sessionVisible);
    useShortcut('terminal.focusRight', () => handleMovePane('right'), sessionVisible);
    useShortcut('terminal.focusUp', () => handleMovePane('up'), sessionVisible);
    useShortcut('terminal.focusDown', () => handleMovePane('down'), sessionVisible);

    const paneFrameStyle = useCallback((bounds: NormalizedPaneBounds) => ({
      left: `${bounds.left * 100}%`,
      top: `${bounds.top * 100}%`,
      width: `${bounds.width * 100}%`,
      height: `${bounds.height * 100}%`,
    }), []);

    const beginLeafDrag = useCallback((leafId: string, event: React.PointerEvent<HTMLDivElement>) => {
      if (event.button !== 0) {
        return;
      }
      event.preventDefault();
      const container = (event.target as HTMLElement).closest('.session-terminal-panes') as HTMLElement | null;
      if (!container) {
        return;
      }
      // The press only becomes a drag past startLeafDrag's activation threshold,
      // so every visual side effect is deferred to onActivate.
      let releaseSelectionLock: (() => void) | null = null;
      const teardown = startLeafDrag(leafId, event.clientX, event.clientY, container, renderedPaneBounds, {
        onActivate: () => {
          releaseSelectionLock = lockTextSelection('grabbing');
          onLeafDragStart?.(leafId);
          setDraggingLeafId(leafId);
        },
        onGhostMove: (x, y) => {
          setGhostPos({ x, y });
          onLeafDragGhostMove?.(x, y);
        },
        onPreview: (target) => {
          setDockTarget(target);
          onLeafDragPreview?.(target);
        },
        onDrop: (id, target) => onMoveLeaf?.(id, target.anchorId, target.edge, target.ratio),
        onCleanup: () => {
          releaseSelectionLock?.();
          releaseSelectionLock = null;
          setDraggingLeafId(null);
          setDockTarget(null);
          setGhostPos(null);
          tileDragCleanupRef.current = null;
          onLeafDragEnd?.();
        },
      }, getActiveLeafDropSnapshot);
      tileDragCleanupRef.current = teardown;
    }, [getActiveLeafDropSnapshot, onLeafDragEnd, onLeafDragGhostMove, onLeafDragPreview, onLeafDragStart, renderedPaneBounds, onMoveLeaf]);

    const effectiveDraggingLeafId = leafDragPreview?.draggingLeafId ?? draggingLeafId;
    const effectiveDockTarget = leafDragPreview?.dockTarget ?? dockTarget;
    const effectiveGhostPos = leafDragPreview?.ghostPos ?? ghostPos;

    const renderPaneSurface = useCallback((paneId: string): React.ReactNode => {
      const path = panePaths.get(paneId) || 'root';
      const bounds = renderedPaneBounds.get(paneId);
      if (!bounds) {
        return null;
      }
      const frameStyle = paneFrameStyle(bounds);

      const agentPane = agentPaneById.get(paneId);
      if (agentPane) {
        const paneSession = sessionById.get(agentPane.sessionId);
        const paneTitle = paneSession?.label || agentPane.title || 'Session';
        if (suspendedLeafIds.has(agentPane.id) && !effectivePaneId) {
          const column = bounds.width * attentionViewport.width <= bounds.height * attentionViewport.height;
          return (
            <div
              key={agentPane.id}
              className={`workspace-pane workspace-pane--suspended workspace-pane--suspended-${column ? 'column' : 'row'}`}
              data-pane-session-id={agentPane.sessionId}
              data-pane-id={agentPane.id}
              data-pane-kind="agent"
              data-pane-path={path}
              data-pane-suspended="true"
              style={frameStyle}
            >
              <button
                type="button"
                className="workspace-suspended-leaf"
                aria-label={`Expand ${paneTitle}`}
                title={`Expand ${paneTitle}`}
                onMouseDown={(event) => event.stopPropagation()}
                onClick={() => focusLeaf(agentPane.id)}
              >
                {paneSession?.state ? (
                  <StateIndicator state={paneSession.state} size="sm" seed={agentPane.sessionId} />
                ) : (
                  <span className="workspace-suspended-state" />
                )}
                <span className="workspace-suspended-label">{paneTitle}</span>
              </button>
            </div>
          );
        }
        const paneStatus = agentPane.status || 'ready';
        const isPaneStarting = paneStatus === 'spawning';
        const isPaneFailed = paneStatus === 'failed';
        const nudgeMode = paneSession?.state
          ? deriveNudgeMode({
              ticketUnread: paneSession.ticketUnread,
              nudgeFiresAt: paneSession.nudgeFiresAt,
              state: paneSession.state,
              isActive: Boolean(paneSession.isActive),
            })
          : null;
        const paneSeedDisplay = derivePaneSeedDisplay(gardenSeeds, agentPane.sessionId, paneSession?.seedId, paneSession?.crewMember);
        const autoSettleFiresAt = paneSession?.autoSettleFiresAt;
        const autoSettleHeld = paneSession?.autoSettleHeld;
        const autoSettleDismissArmed = paneSession?.autoSettleDismissArmed;
        return (
          <div
            key={agentPane.id}
            className={`workspace-pane ${activeLeafId === agentPane.id ? 'active' : ''} ${effectiveDraggingLeafId === agentPane.id ? 'workspace-pane--dragging' : ''}`.trim()}
            onMouseDown={() => focusLeaf(agentPane.id)}
            data-pane-session-id={agentPane.sessionId}
            data-pane-id={agentPane.id}
            data-pane-kind="agent"
            data-pane-path={path}
            style={frameStyle}
          >
            <div
              className={`workspace-pane-header ${
                showPaneHeader
                  ? 'workspace-pane-header--draggable'
                  : 'workspace-pane-header--static'
              }`.trim()}
              onPointerDown={showPaneHeader ? (event) => beginLeafDrag(agentPane.id, event) : undefined}
              title={showPaneHeader ? 'Drag to move' : undefined}
            >
              {/* The same dot the sidebar puts beside this session. */}
              {paneSession?.state ? (
                <StateIndicator
                  state={paneSession.state}
                  size="sm"
                  seed={agentPane.sessionId}
                />
              ) : null}
              <span className="workspace-pane-identity">
                <span className="workspace-pane-identity-main">
                  <span className="workspace-pane-title">{paneTitle}</span>
                  <HeaderSessionUsage
                    usage={paneSession?.usage}
                    sessionId={agentPane.sessionId}
                    pinned={pinnedUsagePopover === agentPane.sessionId}
                    onPopoverClosed={() => setPinnedUsagePopover(null)}
                  />
                </span>
                <SessionProvenance
                  automation={paneSession?.automation}
                  pullRequests={paneSession?.pullRequests}
                  interactive
                />
              </span>
              {onRenameSession ? (
                <button
                  type="button"
                  className="workspace-pane-rename-btn"
                  data-testid={`rename-pane-${agentPane.id}`}
                  onPointerDown={(event) => event.stopPropagation()}
                  onClick={(event) => {
                    event.stopPropagation();
                    const rect = event.currentTarget.getBoundingClientRect();
                    setRenamePane({
                      sessionId: agentPane.sessionId,
                      name: paneTitle,
                      anchor: { top: rect.bottom + 4, left: rect.left },
                    });
                  }}
                  title="Rename session"
                  aria-label={`Rename session ${paneTitle}`}
                >
                  ✎
                </button>
              ) : null}
              {paneSession?.presentation ? (
                <HeaderPresentationChip
                  presentation={paneSession.presentation}
                  onOpen={(presentationId) => onOpenPresentation?.(presentationId)}
                />
              ) : null}
              {autoSettleFiresAt || autoSettleHeld ? (
                <HeaderSettlingIndicator
                  firesAt={autoSettleFiresAt}
                  held={autoSettleHeld}
                  onCancel={() => onCancelCountdown?.(agentPane.sessionId)}
                />
              ) : autoSettleDismissArmed ? (
                <HeaderSettleKeptChip onDisarm={() => onCancelCountdown?.(agentPane.sessionId)} />
              ) : null}
              {nudgeMode ? (
                <HeaderNudgeIndicator
                  mode={nudgeMode}
                  firesAt={paneSession?.nudgeFiresAt}
                  onTrigger={() => onTriggerNudge?.(agentPane.sessionId)}
                  onCancel={() => onCancelCountdown?.(agentPane.sessionId)}
                />
              ) : null}
              {paneSeedDisplay.kind !== 'none' && onOpenSeed ? (
                <PaneSeedChip
                  display={paneSeedDisplay}
                  crownSeedId={paneSession?.seedId}
                  unread={Boolean(paneSession?.ticketUnread)}
                  sessionId={agentPane.sessionId}
                  pinned={pinnedSeedPopover === agentPane.sessionId}
                  onOpenSeed={onOpenSeed}
                  onPopoverClosed={() => setPinnedSeedPopover(null)}
                />
              ) : null}
            </div>
            <div className="workspace-pane-body">
              {paneSession?.terminalBuildStale && !staleBuildDismissed.has(agentPane.sessionId) ? (
                <TerminalStaleBuildNotice
                  onDismiss={() => setStaleBuildDismissed((prev) => new Set(prev).add(agentPane.sessionId))}
                />
              ) : null}
              {isPaneStarting || isPaneFailed ? (
                <div className={`workspace-pane-status workspace-pane-status--${paneStatus}`}>
                  <span className="workspace-pane-status-spinner" aria-hidden="true" />
                  <span>{isPaneFailed ? (agentPane.error || 'Session failed to start') : `Starting ${paneTitle}...`}</span>
                </div>
              ) : conversationAgents?.has(paneSession?.agent ?? '') ? (
                <ConversationPane
                  sessionId={agentPane.sessionId}
                  paneActive={isActiveSession && sessionVisible && activeLeafId === agentPane.id}
                  sessionState={paneSession?.state}
                  resolvedTheme={resolvedTheme}
                />
              ) : !terminalsLive ? (
                <div className="workspace-pane-virtualized" aria-hidden="true" data-testid={`pane-virtualized-${agentPane.id}`} />
              ) : (
                <AnnotatedTerminal
                  ref={terminalRefForPane(agentPane.id)}
                  sessionId={agentPane.sessionId}
                  annotationApi={annotationApi}
                  // At most one pane owns ⌘Enter for the annotation send shortcut.
                  paneActive={isActiveSession && sessionVisible && activeLeafId === agentPane.id}
                  fontSize={fontSize}
                  resolvedTheme={resolvedTheme}
                  cwd={paneSession?.cwd}
                  debugName={`agent:${paneTitle}:${paneSession?.agent ?? 'shell'}:${agentPane.sessionId}`}
                  runtimeLogMeta={{ sessionId: agentPane.sessionId, paneId: agentPane.id, runtimeId: agentPane.runtimeId, paneKind: 'agent', isActivePane: activePaneId === agentPane.id, isActiveSession, paneCount: paneIds.length }}
                  onInput={runtime.handleTerminalInput(agentPane.id)}
                  onPointerActivity={() => onTerminalPointerActivity?.(agentPane.sessionId)}
                  onOpenMarkdown={onOpenMarkdown}
                  gardenSeeds={gardenSeeds}
                  onOpenSeed={onOpenSeed}
                  onReady={handleGhosttyTerminalReady(agentPane.id)}
                  onResize={runtime.handleTerminalResize(agentPane.id)}
                  onTerminalModelRecovered={onTerminalModelRecovered}
                />
              )}
            </div>
          </div>
        );
      }

      const tileLeaf = tileLeafById.get(paneId);
      if (tileLeaf) {
        if (suspendedLeafIds.has(tileLeaf.tileId) && !effectivePaneId) {
          const suspendedTitle = (tileLeaf.tileParams ?? '').split('/').filter(Boolean).pop()
            || tileLeaf.tileKind
            || 'Tile';
          const column = bounds.width * attentionViewport.width <= bounds.height * attentionViewport.height;
          return (
            <div
              key={`tile:${tileLeaf.tileId}`}
              className={`workspace-pane workspace-pane--tile workspace-pane--suspended workspace-pane--suspended-${column ? 'column' : 'row'}`}
              data-pane-id={tileLeaf.tileId}
              data-pane-kind="tile"
              data-tile-kind={tileLeaf.tileKind}
              data-pane-path={path}
              data-pane-suspended="true"
              style={frameStyle}
            >
              <button
                type="button"
                className="workspace-suspended-leaf"
                aria-label={`Expand ${suspendedTitle}`}
                title={`Expand ${suspendedTitle}`}
                onMouseDown={(event) => event.stopPropagation()}
                onClick={() => focusLeaf(tileLeaf.tileId)}
              >
                <span className={`workspace-suspended-state workspace-suspended-state--${tileLeaf.tileKind}`} />
                <span className="workspace-suspended-label">{suspendedTitle}</span>
              </button>
            </div>
          );
        }
        return (
          <div
            key={`tile:${tileLeaf.tileId}`}
            className={`workspace-pane workspace-pane--tile ${activeLeafId === tileLeaf.tileId ? 'active' : ''}`.trim()}
            onMouseDown={() => focusLeaf(tileLeaf.tileId)}
            data-pane-id={tileLeaf.tileId}
            data-pane-kind="tile"
            data-tile-kind={tileLeaf.tileKind}
            data-pane-path={path}
            style={frameStyle}
          >
            <WorkspaceDockTile
              tile={tileLeaf}
              workspaceId={workspaceId}
              content={tileContents?.[tileContentKey(workspaceId, tileLeaf.tileId)]}
              allowLocalTargets={allowLocalTileTargets}
              dragging={effectiveDraggingLeafId === tileLeaf.tileId}
              visible={
                isActiveSession
                && isSessionViewVisible
                && enabled
                && renamePane === null
                && effectiveDraggingLeafId === null
              }
              workspaceSessions={tileLeaf.tileKind === 'seed' ? seedTargetSessions : tileSessionOptions}
              gardenSeeds={gardenSeeds}
              workspaceSessionId={activePaneSessionId}
              workspaceDirectory={workspaceDirectory}
              onClose={() => onUndockTile?.(tileLeaf.tileId)}
              onFocusDocument={
                tileLeaf.tileKind === 'markdown' || tileLeaf.tileKind === 'seed'
                  ? () => focusDocument(tileLeaf.tileId)
                  : undefined
              }
              onUpdateParams={(tileParams) => onUpdateTile?.(tileLeaf.tileId, tileParams)}
              onRetargetTile={(sessionId) => (
                onUpdateTile?.(tileLeaf.tileId, tileLeaf.tileParams ?? '', sessionId)
              )}
              onRevealSeedInGarden={onRevealSeedInGarden}
              onHeaderPointerDown={(event) => beginLeafDrag(tileLeaf.tileId, event)}
              onRequestContent={onRequestTileContent ?? noRequestContent}
              bodyRef={tileBodyRefFor(tileLeaf.tileId)}
            />
          </div>
        );
      }

      return null;
    }, [
      activePaneId,
      activePaneSessionId,
      activeLeafId,
      attentionViewport,
      beginLeafDrag,
      effectiveDraggingLeafId,
      onOpenMarkdown,
      onOpenPresentation,
      onUndockTile,
      onUpdateTile,
      onRenameSession,
      onTerminalModelRecovered,
      onTerminalPointerActivity,
      onTriggerNudge,
      tileLeafById,
      tileSessionOptions,
      seedTargetSessions,
      gardenSeeds,
      tileBodyRefFor,
      tileContents,
      allowLocalTileTargets,
      onRequestTileContent,
      workspaceId,
      workspaceDirectory,
      renamePane,
      fontSize,
      paneIds.length,
      isActiveSession,
      isSessionViewVisible,
      enabled,
      focusLeaf,
      focusDocument,
      handleGhosttyTerminalReady,
      staleBuildDismissed,
      suspendedLeafIds,
      effectivePaneId,
      paneFrameStyle,
      panePaths,
      renderedPaneBounds,
      agentPaneById,
      sessionById,
      resolvedTheme,
      runtime,
      showPaneHeader,
      terminalsLive,
      onOpenSeed,
      onRevealSeedInGarden,
      pinnedSeedPopover,
      pinnedUsagePopover,
      conversationAgents,
      annotationApi,
      onCancelCountdown,
    ]);

    const focusModeTitle = useMemo(() => {
      if (!effectivePaneId) {
        return '';
      }
      const agentPane = agentPaneById.get(effectivePaneId);
      if (agentPane) {
        return sessionById.get(agentPane.sessionId)?.label || agentPane.title || 'Session';
      }
      const tile = tileLeafById.get(effectivePaneId);
      if (tile) {
        const base = (tile.tileParams ?? '').split('/').filter(Boolean).pop();
        return base || tile.tileKind || 'Tile';
      }
      return 'Pane';
    }, [agentPaneById, effectivePaneId, sessionById, tileLeafById]);

    const reviewDeckTiles = useMemo(() => (
      [...tileLeafById.values()].filter((tile) => tile.tileKind === 'markdown' || tile.tileKind === 'seed')
    ), [tileLeafById]);

    const draggingLeafLabel = useMemo(() => {
      if (!effectiveDraggingLeafId) {
        return '';
      }
      const agentPane = agentPaneById.get(effectiveDraggingLeafId);
      if (agentPane) {
        return sessionById.get(agentPane.sessionId)?.label || agentPane.title || 'Pane';
      }
      const tile = tileLeafById.get(effectiveDraggingLeafId);
      if (tile) {
        const base = (tile.tileParams ?? '').split('/').filter(Boolean).pop();
        return base || tile.tileKind || 'Tile';
      }
      return 'Pane';
    }, [effectiveDraggingLeafId, agentPaneById, sessionById, tileLeafById]);

    const splitDividers = layoutPlan?.dividers ?? [];

    const ratioRafRef = useRef<number | null>(null);
    const pendingRatioRef = useRef<{ splitId: string; ratio: number } | null>(null);
    const dragCleanupRef = useRef<(() => void) | null>(null);

    const flushRatioOverride = useCallback(() => {
      ratioRafRef.current = null;
      const pending = pendingRatioRef.current;
      if (!pending) {
        return;
      }
      setPendingRatioOverrides((prev) => {
        if (prev.get(pending.splitId) === pending.ratio) {
          return prev;
        }
        const next = new Map(prev);
        next.set(pending.splitId, pending.ratio);
        return next;
      });
    }, []);

    const handleDividerPointerDown = useCallback((divider: SplitDivider, event: React.PointerEvent<HTMLDivElement>) => {
      if (event.button !== 0) {
        return;
      }
      event.preventDefault();
      event.stopPropagation();
      const container = (event.target as HTMLElement).closest('.session-terminal-panes') as HTMLElement | null;
      if (!container) {
        return;
      }
      const dividerElement = event.currentTarget;
      const pointerId = event.pointerId;
      if (typeof dividerElement.setPointerCapture === 'function') {
        try {
          dividerElement.setPointerCapture(pointerId);
        } catch {
        }
      }
      const rect = container.getBoundingClientRect();
      const { splitId, direction, left, top, right, bottom } = divider;
      const resizeToken = `${splitId}:${pointerId}`;
      suppressTerminalMouseDuringResize();
      container.dataset.resizingSplitId = splitId;
      container.dataset.resizingSplitDirection = direction;
      container.dataset.resizingSplitToken = resizeToken;
      document.documentElement.dataset.attnWorkspaceResizing = '1';
      document.documentElement.dataset.attnWorkspaceResizeToken = resizeToken;
      const spanNorm = direction === 'vertical' ? right - left : bottom - top;
      const axisPx = direction === 'vertical' ? rect.width : rect.height;
      const spanPx = spanNorm * axisPx;
      const ratioBounds = layoutTreeRef.current
        ? dragRatioBounds(layoutTreeRef.current, splitId, activeLeafIdRef.current, spanPx)
        : { min: 0.1, max: 0.9 };
      const splitBoxPx = {
        width: (right - left) * rect.width,
        height: (bottom - top) * rect.height,
      };
      const suspensionSnapshot = {
        suspended: suspendedLeafIdsRef.current,
        pinned: pinnedLeafIdsRef.current,
      };
      const updateDragSuspension = (ratio: number) => {
        if (!layoutTreeRef.current) {
          return;
        }
        const result = applyDragSuspension({
          sourceTree: layoutTreeRef.current,
          splitId,
          ratio,
          splitBoxPx,
          viewport: { width: rect.width, height: rect.height },
          suspendedLeafIds: suspendedLeafIdsRef.current,
          pinnedLeafIds: pinnedLeafIdsRef.current,
          protectedLeafId: activeLeafIdRef.current,
          focusOrder: attentionFocusOrderRef.current,
        });
        suspendedLeafIdsRef.current = result.suspendedLeafIds;
        pinnedLeafIdsRef.current = result.pinnedLeafIds;
      };
      draggingSplitRef.current = splitId;
      setResizingSplit({ splitId, direction });
      const releaseSelectionLock = lockTextSelection(
        direction === 'vertical' ? 'col-resize' : 'row-resize',
      );

      const grabOffset = divider.grabRatio != null ? divider.grabRatio - divider.ratio : 0;
      const computeRatio = (clientX: number, clientY: number): number => {
        let ratio = 0.5;
        if (spanNorm > 0) {
          if (direction === 'vertical') {
            ratio = ((clientX - rect.left) / rect.width - left) / spanNorm;
          } else {
            ratio = ((clientY - rect.top) / rect.height - top) / spanNorm;
          }
          ratio -= grabOffset;
        }
        return Math.min(ratioBounds.max, Math.max(ratioBounds.min, ratio));
      };

      const onMove = (ev: PointerEvent) => {
        ev.preventDefault();
        ev.stopPropagation();
        suppressTerminalMouseDuringResize();
        const nextRatio = computeRatio(ev.clientX, ev.clientY);
        updateDragSuspension(nextRatio);
        pendingRatioRef.current = { splitId, ratio: nextRatio };
        if (ratioRafRef.current == null) {
          ratioRafRef.current = window.requestAnimationFrame(flushRatioOverride);
        }
      };
      const teardown = () => {
        window.removeEventListener('pointermove', onMove, true);
        window.removeEventListener('pointerup', onUp, true);
        window.removeEventListener('pointercancel', onCancel, true);
        window.removeEventListener('blur', onCancel);
        if (
          typeof dividerElement.hasPointerCapture === 'function'
          && typeof dividerElement.releasePointerCapture === 'function'
          && dividerElement.hasPointerCapture(pointerId)
        ) {
          try {
            dividerElement.releasePointerCapture(pointerId);
          } catch {
          }
        }
        if (ratioRafRef.current != null) {
          window.cancelAnimationFrame(ratioRafRef.current);
          ratioRafRef.current = null;
        }
        if (container.dataset.resizingSplitToken === resizeToken) {
          delete container.dataset.resizingSplitId;
          delete container.dataset.resizingSplitDirection;
          delete container.dataset.resizingSplitToken;
        }
        if (document.documentElement.dataset.attnWorkspaceResizeToken === resizeToken) {
          delete document.documentElement.dataset.attnWorkspaceResizing;
          delete document.documentElement.dataset.attnWorkspaceResizeToken;
        }
        // The long during-drag window would outlive the drag and swallow normal
        // interaction.
        suppressTerminalMouseDuringResize(RESIZE_MOUSE_RELEASE_GUARD_MS);
        releaseSelectionLock();
        setResizingSplit((current) => (current?.splitId === splitId ? null : current));
        dragCleanupRef.current = null;
      };
      const onCancel = () => {
        teardown();
        suspendedLeafIdsRef.current = suspensionSnapshot.suspended;
        pinnedLeafIdsRef.current = suspensionSnapshot.pinned;
        pendingRatioRef.current = null;
        draggingSplitRef.current = null;
        clearRatioOverride(splitId);
        scheduleTerminalFitAfterResize();
      };
      const onUp = (ev: PointerEvent) => {
        ev.preventDefault();
        ev.stopPropagation();
        const ratio = computeRatio(ev.clientX, ev.clientY);
        teardown();
        updateDragSuspension(ratio);
        pendingRatioRef.current = { splitId, ratio };
        flushRatioOverride();
        draggingSplitRef.current = null;
        scheduleTerminalFitAfterResize();
        const resizeResult = onResizeSplit?.(splitId, ratio);
        if (resizeResult) {
          void resizeResult.catch(() => clearRatioOverride(splitId, ratio));
        }
      };
      dragCleanupRef.current = teardown;
      window.addEventListener('pointermove', onMove, true);
      window.addEventListener('pointerup', onUp, true);
      window.addEventListener('pointercancel', onCancel, true);
      window.addEventListener('blur', onCancel);
    }, [clearRatioOverride, flushRatioOverride, onResizeSplit, scheduleTerminalFitAfterResize]);

    useEffect(() => () => {
      dragCleanupRef.current?.();
      tileDragCleanupRef.current?.();
    }, []);

    if (!renderedLayoutTree) {
      return (
        <div
          className="session-terminal-workspace"
          data-session-terminal-workspace={workspaceId}
          data-workspace-id={workspaceId}
          data-active-pane-id=""
          data-active-leaf-id=""
          data-maximized-pane-id=""
          data-session-visible={sessionVisible ? '1' : '0'}
          data-zoomed-pane-id=""
        />
      );
    }

    return (
      <div
        className={`session-terminal-workspace workspace-selection--${workspaceSelectionStyle} ${effectivePaneId ? 'focus-mode' : ''} ${effectiveZoomedPaneId && !effectivePaneId ? 'zoom-mode' : ''} ${renderedPaneIds.length > 1 ? 'multi-leaf' : ''}`.trim().replace(/\s+/g, ' ')}
        data-session-terminal-workspace={workspaceId}
        data-workspace-id={workspaceId}
        data-active-pane-id={activePaneId}
        data-active-leaf-id={activeLeafId}
        data-maximized-pane-id={effectivePaneId || ''}
        data-session-visible={sessionVisible ? '1' : '0'}
        data-zoomed-pane-id={effectiveZoomedPaneId || ''}
      >
        {effectivePaneId && (
          <div className="workspace-focus-bar">
            <div className="workspace-focus-label">
              <span className="workspace-focus-kicker">Focus</span>
              {tileLeafById.has(effectivePaneId) && reviewDeckTiles.length > 1 ? (
                <div className="workspace-focus-tabs" role="tablist" aria-label="Open review documents">
                  {reviewDeckTiles.map((tile) => {
                    const label = (tile.tileParams ?? '').split('/').filter(Boolean).pop()
                      || tile.tileKind
                      || 'Document';
                    const selected = tile.tileId === effectivePaneId;
                    return (
                      <button
                        key={tile.tileId}
                        type="button"
                        className={`workspace-focus-tab ${selected ? 'workspace-focus-tab--selected' : ''}`.trim()}
                        role="tab"
                        aria-selected={selected}
                        onClick={() => focusDocument(tile.tileId)}
                      >
                        {label}
                      </button>
                    );
                  })}
                </div>
              ) : (
                <span className="workspace-focus-title">{focusModeTitle}</span>
              )}
            </div>
            <button
              type="button"
              className="workspace-focus-exit"
              onClick={() => setMaximizedLeafId(null)}
              title={`Exit focus mode (${formatShortcut('terminal.toggleMaximize')})`}
            >
              Return to split
            </button>
          </div>
        )}
        <WorkspaceLayoutRenderer
          layoutTree={renderedLayoutTree}
          paneIds={renderedPaneIds}
          renderPane={renderPaneSurface}
          containerRef={panesContainerRef}
          dividers={splitDividers}
          onDividerPointerDown={handleDividerPointerDown}
          overlay={(
            <>
              {effectiveDockTarget ? (
                <div
                  className="workspace-dock-target"
                  style={{
                    left: `${effectiveDockTarget.rect.left * 100}%`,
                    top: `${effectiveDockTarget.rect.top * 100}%`,
                    width: `${effectiveDockTarget.rect.width * 100}%`,
                    height: `${effectiveDockTarget.rect.height * 100}%`,
                  }}
                />
              ) : null}
              {resizingSplit ? (
                <div
                  className={`workspace-resize-shield workspace-resize-shield--${resizingSplit.direction}`}
                  data-resizing-split-id={resizingSplit.splitId}
                  onPointerDown={(event) => {
                    event.preventDefault();
                    event.stopPropagation();
                  }}
                  onPointerMove={(event) => {
                    event.preventDefault();
                    event.stopPropagation();
                  }}
                  onMouseDown={(event) => {
                    event.preventDefault();
                    event.stopPropagation();
                  }}
                  onMouseMove={(event) => {
                    event.preventDefault();
                    event.stopPropagation();
                  }}
                  onMouseUp={(event) => {
                    event.preventDefault();
                    event.stopPropagation();
                  }}
                />
              ) : null}
            </>
          )}
        />
        {effectiveGhostPos && effectiveDraggingLeafId && (
          <div
            className="workspace-dock-ghost"
            style={{ left: effectiveGhostPos.x + 12, top: effectiveGhostPos.y + 12 }}
          >
            {draggingLeafLabel}
          </div>
        )}
        {renamePane && onRenameSession && (
          <RenamePopover
            key={renamePane.sessionId}
            initialValue={renamePane.name}
            label="Rename session"
            anchor={renamePane.anchor}
            onSubmit={(value) => onRenameSession(renamePane.sessionId, value)}
            onClose={() => setRenamePane(null)}
          />
        )}
      </div>
    );
  }
);
