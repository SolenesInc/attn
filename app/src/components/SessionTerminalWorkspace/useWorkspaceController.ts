import type { ForwardedRef } from 'react';
import {
  useCallback,
  useEffect,
  useImperativeHandle,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import type { Seed } from '../../hooks/useDaemonSocket';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { useShortcut } from '../../shortcuts/useShortcut';
import {
  collectPreferredSplitIds,
  collectSplitRatios,
  findLeafInDirection,
  getNormalizedPaneBounds,
  leafSlotId,
  type NormalizedPaneBounds,
  type SplitDivider,
  type TerminalLayoutNode,
  type TerminalNavigationDirection,
  type TerminalSplitDirection,
} from '../../types/workspace';
import { lockTextSelection } from '../../utils/dragLock';
import { isSuspiciousTerminalSize } from '../../utils/terminalDebug';
import { type GhosttyTerminalHandle } from '../GhosttyTerminal';
import { type WorkspaceTileSessionOption } from './WorkspaceDockTile';
import { annotationSurfaceOwnsFocus } from './annotationFocus';
import {
  applyDragSuspension,
  dragRatioBounds,
  releaseSuspendedLeaf,
  resolveWorkspaceLayout,
  type AttentionViewport,
} from './attentionLayout';
import { newestOpenedTile, selectedPaneId, selectedTileId } from './leafSelection';
import { useFocusedLeaf } from './useFocusedLeaf';
import { useGhosttyPaneRuntime } from './useGhosttyPaneRuntime';
import { useSessionPopoverRequest } from './useSessionPopoverRequest';
import { useWorkspaceLeafDrag } from './useWorkspaceLeafDrag';
import { useWorkspacePanes } from './useWorkspacePanes';
import type {
  SessionTerminalWorkspaceHandle,
  SessionTerminalWorkspaceProps,
} from './workspaceTypes';

const RESIZE_MOUSE_SUPPRESSION_MS = 1_500;

const RESIZE_MOUSE_RELEASE_GUARD_MS = 150;

function suppressTerminalMouseDuringResize(durationMs = RESIZE_MOUSE_SUPPRESSION_MS): void {
  document.documentElement.dataset.attnWorkspaceMouseSuppressUntil = String(
    Date.now() + durationMs,
  );
}

const EMPTY_SUSPENDED_LEAF_IDS: ReadonlySet<string> = new Set();

const EMPTY_WORKSPACE_SESSIONS: NonNullable<SessionTerminalWorkspaceProps['workspaceSessions']> =
  [];

const EMPTY_SEED_TARGET_SESSIONS: WorkspaceTileSessionOption[] = [];

const EMPTY_GARDEN_SEEDS: Seed[] = [];

const EMPTY_DELEGATION_SESSIONS: NonNullable<SessionTerminalWorkspaceProps['delegationSessions']> =
  [];

export function useWorkspaceController(
  {
    workspaceId,
    workspaceDirectory,
    workspaceSessions = EMPTY_WORKSPACE_SESSIONS,
    delegationSessions = EMPTY_DELEGATION_SESSIONS,
    seedTargetSessions = EMPTY_SEED_TARGET_SESSIONS,
    gardenSeeds = EMPTY_GARDEN_SEEDS,
    onOpenSeed,
    onRevealSeedInGarden,
    backToCrewTileId,
    onBackToCrew,
    seedPopoverRequest,
    usagePopoverRequest,
    annotationApi,
    workspace,
    workspaceSelectionStyle = 'rail',
    activePaneId,
    selectedSessionId,
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
  }: SessionTerminalWorkspaceProps,
  ref: ForwardedRef<SessionTerminalWorkspaceHandle>,
) {
  const [activeTile, setActiveTile] = useState<{
    tileId: string;
    whileActivePaneId: string;
  } | null>(null);
  const [paneReadyFocusRequest, setPaneReadyFocusRequest] = useState(0);
  const [renamePane, setRenamePane] = useState<{
    sessionId: string;
    name: string;
    anchor: { top: number; left: number };
  } | null>(null);
  const [pendingRatioOverrides, setPendingRatioOverrides] = useState<Map<string, number>>(
    () => new Map(),
  );
  const [resizingSplit, setResizingSplit] = useState<{
    splitId: string;
    direction: TerminalSplitDirection;
  } | null>(null);
  const [staleBuildDismissed, setStaleBuildDismissed] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const [pinnedSeedPopover, dismissSeedPopover] = useSessionPopoverRequest(seedPopoverRequest);
  const [pinnedUsagePopover, dismissUsagePopover] = useSessionPopoverRequest(usagePopoverRequest);
  const [provenancePopoverOwner, setProvenancePopoverOwner] = useState<string | null>(null);
  const [attentionViewport, setAttentionViewport] = useState<AttentionViewport>({
    width: 0,
    height: 0,
  });
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

  useLayoutEffect(() => {
    activePaneIdRef.current = activePaneId;
    isActiveSessionRef.current = isActiveSession;
    sessionViewVisibleRef.current = isSessionViewVisible;
  }, [activePaneId, isActiveSession, isSessionViewVisible]);

  const {
    tileLeafById,
    agentPaneById,
    agentPanes,
    sessionById,
    paneIds,
    delegationSessionById,
    delegatesByDispatcherId,
    tileSessionOptions,
    activePaneSessionId,
  } = useWorkspacePanes({ workspace, workspaceSessions, delegationSessions, activePaneId });

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
  const openedAnnotatedTileId = newestOpenedTile(
    annotatedTileIds,
    previousAnnotatedTileIdsRef.current,
  );
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
      automaticTileFocusRef.current &&
      !annotatedTileIdSet.has(automaticTileFocusRef.current.tileId)
    ) {
      automaticTileFocusRef.current = null;
    }
  }, [activePaneId, annotatedTileIdSet, openedAnnotatedTileId]);

  const firstTileId = useMemo(
    () => (tileLeafById.size > 0 ? (tileLeafById.keys().next().value ?? null) : null),
    [tileLeafById],
  );

  const focusedTileId = selectedTileId(activePaneId, tileLeafById, automaticTileFocus, activeTile);
  const focusedPaneId = selectedPaneId(activePaneId, agentPaneById, pendingPaneFocusRef.current);
  const activeLeafId = focusedTileId || focusedPaneId || firstTileId || '';
  const activeLeafIsTile = tileLeafById.has(activeLeafId);
  useLayoutEffect(() => {
    layoutTreeRef.current = workspace.layoutTree ?? null;
    activeLeafIdRef.current = activeLeafId;
  }, [workspace.layoutTree, activeLeafId]);

  useLayoutEffect(() => {
    const pending = pendingPaneFocusRef.current;
    if (
      pending &&
      (activePaneId === pending.leafId ||
        activePaneId !== pending.fromActivePaneId ||
        !agentPaneById.has(pending.leafId))
    ) {
      pendingPaneFocusRef.current = null;
    }
  }, [activePaneId, agentPaneById]);

  const runtimePanes = useMemo(() => {
    const panes = [];
    for (const pane of agentPanes) {
      if (pane.status && pane.status !== 'ready') continue;
      const paneSession = sessionById.get(pane.sessionId);
      if (!paneSession) continue;
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
  }, [agentPanes, sessionById]);

  const runtime = useGhosttyPaneRuntime(
    runtimePanes,
    activePaneId,
    eventRouter,
    isActiveSessionRef,
    terminalsLive,
  );
  const setTerminalHandle = runtime.setTerminalHandle;
  const terminalRefCallbacksRef = useRef(
    new Map<string, (handle: GhosttyTerminalHandle | null) => void>(),
  );
  const terminalRefForPane = useCallback(
    (paneId: string) => {
      const existing = terminalRefCallbacksRef.current.get(paneId);
      if (existing) return existing;
      const callback = (handle: GhosttyTerminalHandle | null) => {
        setTerminalHandle(paneId, handle);
      };
      terminalRefCallbacksRef.current.set(paneId, callback);
      return callback;
    },
    [setTerminalHandle],
  );
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
  const leafIds = useMemo(() => [...paneIds, ...tileLeafById.keys()], [paneIds, tileLeafById]);
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
  const selectedWorkspaceSessionId =
    workspaceSessions.find((session) => session.isActive)?.id ?? null;
  const [effectivePaneId, setMaximizedLeafId] = useFocusedLeaf(
    leafIdSet,
    agentPaneById,
    selectedSessionId === undefined ? selectedWorkspaceSessionId : selectedSessionId,
  );
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
      const prunedPins = [...pinnedLeafIdsRef.current].filter((id) =>
        layoutPlan.suspendedLeafIds.has(id),
      );
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
      setAttentionViewport((current) =>
        current.width === width && current.height === height ? current : { width, height },
      );
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
      if (
        current == null ||
        (expectedRatio != null && Math.abs(current - expectedRatio) >= 0.005)
      ) {
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
  const renderedPaneBounds = useMemo(
    () =>
      renderedLayoutTree
        ? getNormalizedPaneBounds(renderedLayoutTree)
        : new Map<string, NormalizedPaneBounds>(),
    [renderedLayoutTree],
  );
  const paneGeometry = useMemo(() => {
    const geometry = new Map<string, string>();
    for (const [paneId, path] of panePaths) {
      const bounds = renderedPaneBounds.get(paneId);
      geometry.set(
        paneId,
        bounds ? `${path}:${bounds.left}:${bounds.top}:${bounds.right}:${bounds.bottom}` : path,
      );
    }
    return geometry;
  }, [panePaths, renderedPaneBounds]);
  const renderedPaneIds = useMemo(() => Array.from(panePaths.keys()), [panePaths]);
  const renderedPaneIdsKey = renderedPaneIds.join('|');
  const suspendedLeafIdsKey = [...suspendedLeafIds].sort().join('|');
  const prevPaneGeometryRef = useRef(paneGeometry);
  const sessionVisibleRef = useRef(false);
  const sessionVisible = enabled && isActiveSession && isSessionViewVisible;

  useImperativeHandle(
    ref,
    () => ({
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
      getLeafDropSnapshot: () =>
        panesContainerRef.current
          ? { container: panesContainerRef.current, paneBounds: renderedPaneBounds }
          : null,
      getActiveLeafId: () => activeLeafIdRef.current,
    }),
    [activePaneId, renderedPaneBounds, runtime],
  );

  // activePaneId does not change here, so without releasing the focused tile it
  // stays the active leaf and Cmd+W undocks it while you type in the terminal.
  const focusActivePaneSurface = useCallback(() => {
    runtime.focusPane(activePaneId, 0);
  }, [activePaneId, runtime]);

  const focusActivePane = useCallback(() => {
    const focusOverrideActive =
      automaticTileFocusRef.current !== null || pendingPaneFocusRef.current !== null;
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
      if (annotationSurfaceOwnsFocus(workspaceId)) return;
      focusActivePaneSurface();
    }
  }, [
    activeLeafId,
    activeLeafIsTile,
    activePaneId,
    focusActivePaneSurface,
    focusTile,
    focusRequestToken,
    isActiveSession,
    isSessionViewVisible,
    paneReadyFocusRequest,
    suspendedLeafIdsKey,
    workspaceId,
    sessionVisible,
  ]);

  // A pane whose grid overflows its container is not retried by fit()'s reveal
  // path — it stays clipped until something unrelated refits it.
  const refitPanesNowAndIfStillWrong = useCallback(
    (targetPaneIds: string[]) => {
      const paneIdsToFit = Array.from(new Set(targetPaneIds));
      if (paneIdsToFit.length === 0) {
        return undefined;
      }

      for (const paneId of paneIdsToFit) {
        fitPane(paneId);
      }

      const stillWrong = (paneId: string) => {
        const size = getPaneSize(paneId);
        return (
          (size != null && isSuspiciousTerminalSize(size.cols, size.rows)) ||
          paneOverflowsContainer(paneId)
        );
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
    },
    [fitPane, getPaneSize, paneOverflowsContainer],
  );

  // Keyed on `isActiveSession`, not `sessionVisible` (which also goes false behind a
  // modal), and declared before the refit effect so a revealed pane measures with its buffer.
  useLayoutEffect(() => {
    for (const paneId of renderedPaneIds) {
      setPaneSurfaceReleased(paneId, !isActiveSession || suspendedLeafIds.has(paneId));
    }
  }, [
    isActiveSession,
    renderedPaneIds,
    renderedPaneIdsKey,
    setPaneSurfaceReleased,
    suspendedLeafIds,
    suspendedLeafIdsKey,
  ]);

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
  }, [
    refitPanesNowAndIfStillWrong,
    renderedPaneIds,
    sessionVisible,
    suspendedLeafIds,
    suspendedLeafIdsKey,
  ]);
  useEffect(
    () => () => {
      if (suspensionAnimationTimeoutRef.current != null) {
        window.clearTimeout(suspensionAnimationTimeoutRef.current);
      }
    },
    [],
  );

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
  const handleSplit = useCallback(
    (direction: TerminalSplitDirection) => {
      if (activeLeafIsTile || !activeLeafId) {
        return;
      }
      onSplitPane(activeLeafId, direction);
    },
    [activeLeafId, activeLeafIsTile, onSplitPane],
  );

  const handleClosePane = useCallback(
    (paneId: string) => {
      onClosePane(paneId);
    },
    [onClosePane],
  );

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
  }, [activeLeafId, onSetZoomActive, setMaximizedLeafId]);

  const focusDocument = useCallback(
    (tileId: string) => {
      automaticTileFocusRef.current = null;
      pendingPaneFocusRef.current = null;
      onSetZoomActive?.(false);
      setActiveTile({ tileId, whileActivePaneId: activePaneId });
      setMaximizedLeafId(tileId);
      window.requestAnimationFrame(() => focusTile(tileId));
    },
    [activePaneId, focusTile, onSetZoomActive, setMaximizedLeafId],
  );

  useEscapeStack(() => setMaximizedLeafId(null), effectivePaneId !== null);

  const toggleZoomActivePane = useCallback(() => {
    setMaximizedLeafId(null);
    onSetZoomActive?.(!zoomActive);
  }, [onSetZoomActive, zoomActive, setMaximizedLeafId]);

  const focusLeaf = useCallback(
    (leafId: string) => {
      suspendedLeafIdsRef.current = releaseSuspendedLeaf(suspendedLeafIdsRef.current, leafId);
      if (pinnedLeafIdsRef.current.has(leafId)) {
        pinnedLeafIdsRef.current = new Set(
          [...pinnedLeafIdsRef.current].filter((id) => id !== leafId),
        );
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
    },
    [activePaneId, focusTile, onFocusPane, runtime, tileLeafById],
  );
  useLayoutEffect(() => {
    focusLeafRequestRef.current = focusLeaf;
  }, [focusLeaf]);

  const handleMovePane = useCallback(
    (direction: TerminalNavigationDirection) => {
      if (!renderedLayoutTree) {
        return;
      }
      const nextLeafId = findLeafInDirection(renderedLayoutTree, activeLeafId, direction);
      if (nextLeafId) {
        focusLeaf(nextLeafId);
        return;
      }
      onNavigateOutOfSession(direction);
    },
    [activeLeafId, focusLeaf, onNavigateOutOfSession, renderedLayoutTree],
  );

  // Deciding here would read leaf state from a ref a child's ready callback can
  // observe before the parent mirrors it, leaving Cmd+W on the wrong leaf.
  const requestFocusForReadyPane = useCallback((paneId: string) => {
    if (
      !isActiveSessionRef.current ||
      !sessionViewVisibleRef.current ||
      activePaneIdRef.current !== paneId
    ) {
      return;
    }
    const active = document.activeElement;
    if (active && active !== document.body) return;
    setPaneReadyFocusRequest((token) => token + 1);
  }, []);

  const handleGhosttyTerminalReady = useCallback(
    (paneId: string) => (terminal: GhosttyTerminalHandle) => {
      void runtime.handleTerminalReady(paneId)(terminal);
      requestFocusForReadyPane(paneId);
    },
    [requestFocusForReadyPane, runtime],
  );

  useShortcut('terminal.open', focusActivePane, sessionVisible);
  useShortcut(
    'terminal.find',
    () => {
      runtime.openFindInActivePane();
    },
    sessionVisible,
  );
  useShortcut(
    'terminal.splitVertical',
    () => {
      handleSplit('vertical');
    },
    sessionVisible,
  );
  useShortcut(
    'terminal.splitHorizontal',
    () => {
      handleSplit('horizontal');
    },
    sessionVisible,
  );
  useShortcut('terminal.toggleZoom', toggleZoomActivePane, sessionVisible);
  useShortcut('terminal.toggleMaximize', toggleMaximizeActivePane, sessionVisible);
  useShortcut('terminal.close', handleCloseFocusedLeaf, sessionVisible && splitLayoutActive);
  useShortcut('terminal.focusLeft', () => handleMovePane('left'), sessionVisible);
  useShortcut('terminal.focusRight', () => handleMovePane('right'), sessionVisible);
  useShortcut('terminal.focusUp', () => handleMovePane('up'), sessionVisible);
  useShortcut('terminal.focusDown', () => handleMovePane('down'), sessionVisible);

  const paneFrameStyle = useCallback(
    (bounds: NormalizedPaneBounds) => ({
      left: `${bounds.left * 100}%`,
      top: `${bounds.top * 100}%`,
      width: `${bounds.width * 100}%`,
      height: `${bounds.height * 100}%`,
    }),
    [],
  );

  const { beginLeafDrag, effectiveDraggingLeafId, effectiveDockTarget, effectiveGhostPos } =
    useWorkspaceLeafDrag({
      renderedPaneBounds,
      getActiveLeafDropSnapshot,
      onLeafDragStart,
      onLeafDragGhostMove,
      onLeafDragPreview,
      onLeafDragEnd,
      onMoveLeaf,
      leafDragPreview,
    });

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

  const reviewDeckTiles = useMemo(
    () =>
      [...tileLeafById.values()].filter(
        (tile) => tile.tileKind === 'markdown' || tile.tileKind === 'seed',
      ),
    [tileLeafById],
  );

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

  const handleDividerPointerDown = useCallback(
    (divider: SplitDivider, event: React.PointerEvent<HTMLDivElement>) => {
      if (event.button !== 0) {
        return;
      }
      event.preventDefault();
      event.stopPropagation();
      const container = (event.target as HTMLElement).closest(
        '.session-terminal-panes',
      ) as HTMLElement | null;
      if (!container) {
        return;
      }
      const dividerElement = event.currentTarget;
      const pointerId = event.pointerId;
      if (typeof dividerElement.setPointerCapture === 'function') {
        try {
          dividerElement.setPointerCapture(pointerId);
        } catch {}
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
          typeof dividerElement.hasPointerCapture === 'function' &&
          typeof dividerElement.releasePointerCapture === 'function' &&
          dividerElement.hasPointerCapture(pointerId)
        ) {
          try {
            dividerElement.releasePointerCapture(pointerId);
          } catch {}
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
    },
    [clearRatioOverride, flushRatioOverride, onResizeSplit, scheduleTerminalFitAfterResize],
  );

  useEffect(
    () => () => {
      dragCleanupRef.current?.();
    },
    [],
  );

  return {
    workspaceId,
    workspaceSelectionStyle,
    activePaneId,
    onRenameSession,
    renamePane,
    setRenamePane,
    resizingSplit,
    panesContainerRef,
    paneIds,
    agentPaneById,
    tileLeafById,
    activeLeafId,
    effectivePaneId,
    setMaximizedLeafId,
    effectiveZoomedPaneId,
    renderedLayoutTree,
    renderedPaneIds,
    sessionVisible,
    focusDocument,
    effectiveDraggingLeafId,
    effectiveDockTarget,
    effectiveGhostPos,
    focusModeTitle,
    reviewDeckTiles,
    draggingLeafLabel,
    splitDividers,
    handleDividerPointerDown,
    workspaceDirectory,
    workspaceSessions,
    seedTargetSessions,
    gardenSeeds,
    onOpenSeed,
    onRevealSeedInGarden,
    backToCrewTileId,
    onBackToCrew,
    annotationApi,
    fontSize,
    resolvedTheme,
    enabled,
    isActiveSession,
    isSessionViewVisible,
    terminalsLive,
    onTriggerNudge,
    onCancelCountdown,
    onTerminalPointerActivity,
    onOpenPresentation,
    onOpenMarkdown,
    onTerminalModelRecovered,
    onSetZoomActive,
    onUndockTile,
    onUpdateTile,
    tileContents,
    allowLocalTileTargets,
    onRequestTileContent,
    staleBuildDismissed,
    setStaleBuildDismissed,
    pinnedSeedPopover,
    dismissSeedPopover,
    pinnedUsagePopover,
    dismissUsagePopover,
    provenancePopoverOwner,
    setProvenancePopoverOwner,
    attentionViewport,
    sessionById,
    delegationSessionById,
    delegatesByDispatcherId,
    tileSessionOptions,
    activePaneSessionId,
    runtime,
    terminalRefForPane,
    showPaneHeader,
    suspendedLeafIds,
    panePaths,
    renderedPaneBounds,
    tileBodyRefFor,
    focusLeaf,
    handleGhosttyTerminalReady,
    paneFrameStyle,
    beginLeafDrag,
  };
}
