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
  applyRatioOverrides,
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
} from '../../types/desktop';
import { beginPointerResize, suppressTerminalMouseDuringResize } from '../../utils/resizeGesture';
import { isSuspiciousTerminalSize } from '../../utils/terminalDebug';
import { type GhosttyTerminalHandle } from '../GhosttyTerminal';
import { type DesktopTileSessionOption } from './DesktopDockTile';
import { annotationSurfaceOwnsFocus } from './annotationFocus';
import type { DesktopLayoutView } from './desktopLayout';
import { fitColumnOnEdgeDrop, type DockTarget } from './dockTarget';
import type { ShelfDropTarget } from './leafDrag';
import {
  applyColumnDrag,
  columnDividerIndex,
  columnStackNeedPx,
  areaInCols,
  columnIndexOf,
  lastUsedLeaf,
  narrowestWidthPx,
  planSendToShelf,
  planSlotSwap,
  readRow,
  resolveRowLayout,
  rowDragRatioBounds,
  shownRow,
  type RowColumn,
  type RowLayoutInput,
  type RowLayoutPlan,
} from './rowLayout';
import { useRowMotion, type RowMotionLayout } from './useRowMotion';
import { useRowSettling, useShelfControls, useShelfPublisher } from './useShelf';
import { useFocusedLeaf } from './useFocusedLeaf';
import { useGhosttyPaneRuntime } from './useGhosttyPaneRuntime';
import { useSessionPopoverRequest } from './useSessionPopoverRequest';
import { useDesktopLeafDrag } from './useDesktopLeafDrag';
import { useDesktopPanes } from './useDesktopPanes';
import type {
  SessionTerminalDesktopHandle,
  SessionTerminalDesktopProps,
} from './desktopTypes';
import { useDesktopShelfStore } from '../../store/desktopShelf';
import { useSessionStore } from '../../store/sessions';
import { FONT_FAMILY, measureCellWidth } from '../../utils/terminalSizing';
import { sessionDisplayName } from '../../utils/sessionDisplayName';
import { isDomInstance } from '../../utils/realm';

const COLUMN_DRAG_ACTIVATION_PX = 4;

const NOTHING_HELD: readonly string[] = [];

const NO_PENDING_WIDTHS: ReadonlyMap<string, number> = new Map();

function beginResizeGesture(
  container: HTMLElement,
  dividerElement: HTMLElement,
  pointerId: number,
  splitId: string,
  direction: TerminalSplitDirection,
): () => void {
  const endPointerResize = beginPointerResize(
    dividerElement,
    pointerId,
    direction === 'vertical' ? 'col-resize' : 'row-resize',
  );
  const resizeToken = `${splitId}:${pointerId}`;
  container.dataset.resizingSplitId = splitId;
  container.dataset.resizingSplitDirection = direction;
  container.dataset.resizingSplitToken = resizeToken;
  return () => {
    if (container.dataset.resizingSplitToken === resizeToken) {
      delete container.dataset.resizingSplitId;
      delete container.dataset.resizingSplitDirection;
      delete container.dataset.resizingSplitToken;
    }
    endPointerResize();
  };
}

const EMPTY_DESKTOP_SESSIONS: NonNullable<SessionTerminalDesktopProps['desktopSessions']> =
  [];

const EMPTY_SEED_TARGET_SESSIONS: DesktopTileSessionOption[] = [];

const EMPTY_GARDEN_SEEDS: Seed[] = [];

function focusIsFree(desktop: HTMLElement | null): boolean {
  const active = document.activeElement;
  if (!active || active === document.body) return true;
  if (desktop?.contains(active)) return !active.closest('.desktop-dock-tile-body');
  return Boolean(active.closest('[data-session-terminal-desktop], .anno-popup, .anno-panel'));
}

const EMPTY_DELEGATION_SESSIONS: NonNullable<SessionTerminalDesktopProps['delegationSessions']> =
  [];

export function useDesktopController(
  {
    desktopId,
    desktopDirectory,
    desktopSessions = EMPTY_DESKTOP_SESSIONS,
    delegationSessions = EMPTY_DELEGATION_SESSIONS,
    seedTargetSessions = EMPTY_SEED_TARGET_SESSIONS,
    gardenSeeds = EMPTY_GARDEN_SEEDS,
    onOpenSeed,
    onRevealSeedInGarden,
    onOpenAsk,
    backToCrewTileId,
    onBackToCrew,
    seedPopoverRequest,
    usagePopoverRequest,
    annotationApi,
    terminalState,
    desktopSelectionStyle = 'spotlight',
    activePaneId,
    selectedSessionId,
    fontSize,
    resolvedTheme,
    focusRequestToken,
    focusClaim,
    enabled,
    shortcutsEnabled = true,
    isActiveSession,
    isSessionViewVisible = true,
    terminalsLive = true,
    attachOnlyDrawn = false,
    connected = true,
    eventRouter,
    onSplitPane,
    onClosePane,
    onFocusPane,
    onRenameSession,
    onCancelCountdown,
    onTerminalPointerActivity,
    onTerminalFocus,
    onOpenMarkdown,
    onTerminalPathRefused,
    shelfNarrowestCols,
    onSetNarrowestCols,
    onShelfLimit,
    onMoveRowLeaf,
    onSetWantedCols,
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
    onRequestTileContent,
  }: SessionTerminalDesktopProps,
  ref: ForwardedRef<SessionTerminalDesktopHandle>,
) {
  const [paneReadyFocusRequest, setPaneReadyFocusRequest] = useState(0);
  const [renamePane, setRenamePane] = useState<{
    sessionId: string;
    name: string;
    defaultName?: string;
    anchor: { top: number; left: number };
  } | null>(null);
  const [pendingRatioOverrides, setPendingRatioOverrides] = useState<Map<string, number>>(
    () => new Map(),
  );
  const [pendingWantedCols, setPendingWantedCols] = useState(NO_PENDING_WIDTHS);
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
  const [rowAreaPx, setRowAreaPx] = useState(0);
  const [rowAreaHeightPx, setRowAreaHeightPx] = useState(0);
  const firstColumn = useDesktopShelfStore((state) => state.firstColumnByDesktop[desktopId] ?? null);
  const heldLeafIds = useDesktopShelfStore((state) => state.heldByDesktop[desktopId] ?? NOTHING_HELD);
  const pxPerCol = useMemo(() => measureCellWidth(fontSize, FONT_FAMILY), [fontSize]);
  const focusOrderRef = useRef<string[]>([]);
  const tileBodyRefs = useRef(new Map<string, HTMLDivElement>());
  const tileBodyRefCallbacks = useRef(new Map<string, (node: HTMLDivElement | null) => void>());
  const panesContainerRef = useRef<HTMLDivElement | null>(null);
  const draggingSplitRef = useRef<string | null>(null);
  const columnDragActiveRef = useRef(false);
  const activeAgentPaneIdRef = useRef('');
  const layoutTreeRef = useRef<TerminalLayoutNode | null>(null);
  const activeLeafIdRef = useRef('');
  const isActiveSessionRef = useRef(isActiveSession);
  const sessionViewVisibleRef = useRef(isSessionViewVisible);

  useLayoutEffect(() => {
    isActiveSessionRef.current = isActiveSession;
    sessionViewVisibleRef.current = isSessionViewVisible;
  }, [isActiveSession, isSessionViewVisible]);

  const {
    tileLeafById,
    agentPaneById,
    agentPanes,
    sessionById,
    paneIds,
    delegationSessionById,
    delegatesByDispatcherId,
    tileSessionOptions,
  } = useDesktopPanes({ terminalState, desktopSessions, delegationSessions });

  const leafIds = useMemo(() => [...paneIds, ...tileLeafById.keys()], [paneIds, tileLeafById]);
  const activeLeafId = activePaneId && leafIds.includes(activePaneId) ? activePaneId : '';
  const activeLeafIsTile = tileLeafById.has(activeLeafId);
  const activeAgentPaneId = agentPaneById.has(activeLeafId) ? activeLeafId : '';
  useLayoutEffect(() => {
    layoutTreeRef.current = terminalState.layoutTree ?? null;
    activeLeafIdRef.current = activeLeafId;
    activeAgentPaneIdRef.current = activeAgentPaneId;
  }, [terminalState.layoutTree, activeLeafId, activeAgentPaneId]);

  const splitLayoutActive = terminalState.layoutTree?.type === 'split';
  const showPaneHeader = paneIds.length + tileLeafById.size > 1;
  const leafIdSet = useMemo(() => new Set(leafIds), [leafIds]);
  const focusOrder = useMemo(() => {
    if (!activeLeafId) {
      return [];
    }
    return [
      activeLeafId,
      ...focusOrderRef.current.filter((id) => id !== activeLeafId),
    ].filter((id) => leafIdSet.has(id));
  }, [activeLeafId, leafIdSet]);
  useLayoutEffect(() => {
    focusOrderRef.current = focusOrder;
  }, [focusOrder]);
  const selectedDesktopSessionId =
    desktopSessions.find((session) => session.isActive)?.id ?? null;
  const [effectivePaneId, setMaximizedLeafId] = useFocusedLeaf(
    desktopId,
    leafIdSet,
    agentPaneById,
    selectedSessionId === undefined ? selectedDesktopSessionId : selectedSessionId,
  );
  useLayoutEffect(() => {
    if (focusClaim?.announce && focusClaim.desktopId === desktopId
      && effectivePaneId && tileLeafById.has(effectivePaneId)
      && focusClaim.leafId !== effectivePaneId) setMaximizedLeafId(null);
  }, [desktopId, effectivePaneId, focusClaim, setMaximizedLeafId, tileLeafById]);
  const effectiveZoomedPaneId = zoomActive && leafIdSet.has(activeLeafId) ? activeLeafId : null;

  const hasLayout = terminalState.layoutTree != null;
  const rowInput = useMemo<Omit<RowLayoutInput, 'view'> | null>(() => {
    if (!terminalState.layoutTree) return null;
    return {
      tree: applyRatioOverrides(terminalState.layoutTree, pendingRatioOverrides),
      activeLeafId,
      firstColumn,
      heldLeafIds,
      areaPx: rowAreaPx,
      narrowestPx: narrowestWidthPx(shelfNarrowestCols, pxPerCol),
      pxPerCol,
      pendingWantedCols,
    };
  }, [activeLeafId, firstColumn, heldLeafIds, pendingRatioOverrides, pendingWantedCols, pxPerCol, rowAreaPx, shelfNarrowestCols, terminalState.layoutTree]);
  const rowPlan = useMemo<RowLayoutPlan | null>(() => {
    if (!rowInput) return null;
    const view: DesktopLayoutView = effectivePaneId
      ? { mode: 'focused', leafId: effectivePaneId }
      : effectiveZoomedPaneId
        ? { mode: 'zoomed', leafId: effectiveZoomedPaneId }
        : { mode: 'normal' };
    return resolveRowLayout({ ...rowInput, view });
  }, [rowInput, effectivePaneId, effectiveZoomedPaneId]);
  const renderedLayoutTree = rowPlan?.renderedTree ?? null;
  const rowPlanRef = useRef(rowPlan);
  const rowInputRef = useRef(rowInput);
  useLayoutEffect(() => {
    rowPlanRef.current = rowPlan;
    rowInputRef.current = rowInput;
  }, [rowInput, rowPlan]);
  useRowSettling({
    desktopId,
    rowPlan,
    rowInput,
    measured: rowAreaPx > 0,
    visible: isActiveSession && isSessionViewVisible,
    activeLeafId,
    agentPaneById,
    tileLeafById,
  });
  useShelfPublisher({
    desktopId,
    rowPlan,
    showShelf: !effectivePaneId && isActiveSession && isSessionViewVisible,
    narrowestCols: shelfNarrowestCols,
    tileHeightPx: rowAreaHeightPx,
    activeLeafId,
    focusOrder,
    agentPaneById,
    sessionById,
    tileLeafById,
    tileContents,
    gardenSeeds,
  });

  useLayoutEffect(() => {
    const container = panesContainerRef.current;
    if (!container) {
      return;
    }
    const update = (width: number, height: number) => {
      setRowAreaHeightPx(height);
      if (width > 0) setRowAreaPx(width);
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
  }, [desktopId, effectivePaneId, hasLayout]);

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
      if (prev.size === 0 || !terminalState.layoutTree) {
        return prev;
      }
      let changed = false;
      const next = new Map(prev);
      const authoritative = collectSplitRatios(terminalState.layoutTree);
      const preferred = collectPreferredSplitIds(terminalState.layoutTree);
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
  }, [terminalState.layoutTree]);

  useEffect(() => {
    if (!terminalState.layoutTree || columnDragActiveRef.current) return;
    const row = readRow(terminalState.layoutTree);
    setPendingWantedCols((pending) => {
      const unconfirmed = new Map([...pending].filter(([leafId, cols]) => {
        const column = row.find((candidate) => candidate.leafIds[0] === leafId);
        return column && (column.wantedCols ?? 0) !== cols;
      }));
      return unconfirmed.size === pending.size ? pending : unconfirmed;
    });
  }, [terminalState.layoutTree]);
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
  const rowMotionLayout = useMemo<RowMotionLayout | null>(() => (rowPlan
    ? {
      bounds: renderedPaneBounds,
      rowKey: [...rowPlan.before, ...rowPlan.screen, ...rowPlan.after].map((column) => column.leafIds.join('+')).join('|'),
      firstColumnIndex: rowPlan.firstColumn.index,
      widthPx: rowAreaPx,
      heightPx: rowAreaHeightPx,
    }
    : null), [rowAreaHeightPx, renderedPaneBounds, rowAreaPx, rowPlan]);
  const { keepLeavingFrame } = useRowMotion({
    containerRef: panesContainerRef,
    layout: rowMotionLayout,
    paused: !isActiveSession || !isSessionViewVisible || !enabled || resizingSplit !== null || effectivePaneId !== null,
    mountedLeavesKey: leafIds.join('|'),
  });
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
  const offScreenBrowserIds = useMemo(
    () => [...tileLeafById.values()]
      .filter((tile) => tile.tileKind === 'browser' && !panePaths.has(tile.tileId))
      .map((tile) => tile.tileId),
    [panePaths, tileLeafById],
  );
  const terminalPaneIds = useMemo(() => new Set(renderedPaneIds), [renderedPaneIds]);
  const desktopDrawn = isActiveSession && isSessionViewVisible;
  const runtimePanes = useMemo(() => {
    const panes = [];
    for (const pane of agentPanes) {
      if (pane.status && pane.status !== 'ready') continue;
      if (!terminalPaneIds.has(pane.id)) continue;
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
        reach: paneSession?.reach,
        state_reason: paneSession?.state_reason,
        terminalExit: paneSession?.terminalExit,
        drawn: desktopDrawn,
      });
    }
    return panes;
  }, [agentPanes, desktopDrawn, sessionById, terminalPaneIds]);

  const runtime = useGhosttyPaneRuntime(
    runtimePanes,
    activeAgentPaneId,
    eventRouter,
    isActiveSessionRef,
    terminalsLive,
    attachOnlyDrawn,
    connected,
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
  const prevPaneGeometryRef = useRef(paneGeometry);
  const sessionVisibleRef = useRef(false);
  const sessionVisible = enabled && isActiveSession && isSessionViewVisible;
  useLayoutEffect(() => {
    const focused = document.activeElement;
    if (!sessionVisible && isDomInstance(focused, 'HTMLElement') && panesContainerRef.current?.contains(focused)) focused.blur();
  }, [sessionVisible]);

  useImperativeHandle(
    ref,
    () => ({
      fitPane: runtime.fitPane,
      fitActivePane: runtime.fitActivePane,
      focusPane: runtime.focusPane,
      focusActivePane: runtime.focusPane.bind(null, activeAgentPaneId),
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
    }),
    [activeAgentPaneId, renderedPaneBounds, runtime],
  );

  const focusActivePaneSurface = useCallback(() => {
    runtime.focusPane(activeAgentPaneId, 0);
  }, [activeAgentPaneId, runtime]);

  // The scrollable body is what satisfies the shortcut dispatcher's
  // terminal-target check, so ⌘W reaches the desktop, not session.close.
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

  const focusShownLeaf = useCallback((leafId: string) => {
    if (tileLeafById.has(leafId)) {
      focusTile(leafId);
      return true;
    }
    if (!agentPaneById.has(leafId) || annotationSurfaceOwnsFocus(desktopId)) return false;
    runtime.focusPane(leafId, 0);
    return true;
  }, [agentPaneById, desktopId, focusTile, runtime, tileLeafById]);

  const focusShownLeafRef = useRef(focusShownLeaf);
  useLayoutEffect(() => {
    focusShownLeafRef.current = focusShownLeaf;
  }, [focusShownLeaf]);

  useEffect(() => {
    if (!sessionVisible || !focusIsFree(panesContainerRef.current)) return;
    focusShownLeafRef.current(activeLeafIdRef.current);
  }, [focusRequestToken, paneReadyFocusRequest, sessionVisible]);

  const focusClaimRef = useRef(focusClaim);
  useLayoutEffect(() => {
    focusClaimRef.current = focusClaim;
  }, [focusClaim]);

  const lastAnnouncedClaim = useRef(0);
  useLayoutEffect(() => {
    const container = panesContainerRef.current;
    if (!container) return;
    if (!sessionVisible) {
      container.querySelectorAll('.desktop-pane.leaf-arrival').forEach((pane) => pane.classList.remove('leaf-arrival'));
      return;
    }
    if (!focusClaim?.announce || focusClaim.desktopId !== desktopId
      || focusClaim.leafId !== activeLeafId || lastAnnouncedClaim.current === focusClaim.id) return;
    const pane = Array.from(container.querySelectorAll<HTMLElement>('[data-pane-id]'))
      .find((element) => element.dataset.paneId === focusClaim.leafId);
    if (!pane) return;

    lastAnnouncedClaim.current = focusClaim.id;
    container.querySelectorAll('.desktop-pane.leaf-arrival').forEach((arrived) => arrived.classList.remove('leaf-arrival'));
    void pane.offsetWidth;
    pane.classList.add('leaf-arrival');
  }, [activeLeafId, desktopId, focusClaim, paneReadyFocusRequest, renderedPaneIdsKey, sessionVisible]);

  const previousLeaves = useRef({ desktopId, ids: new Set(leafIds), visible: sessionVisible });
  useLayoutEffect(() => {
    const previous = previousLeaves.current;
    previousLeaves.current = { desktopId, ids: new Set(leafIds), visible: sessionVisible };
    if (!sessionVisible || !previous.visible || previous.desktopId !== desktopId) return;
    const container = panesContainerRef.current;
    if (!container || useSessionStore.getState().intent) return;
    for (const id of leafIds) {
      if (previous.ids.has(id) || focusClaim?.leafId === id) continue;
      const pane = Array.from(container.querySelectorAll<HTMLElement>('[data-pane-id]'))
        .find((element) => element.dataset.paneId === id);
      if (pane) pane.classList.add('leaf-arrival');
    }
  }, [desktopId, focusClaim, leafIds, sessionVisible]);

  useEffect(() => {
    const container = panesContainerRef.current;
    if (!container) return;
    const onAnimationEnd = (event: AnimationEvent) => {
      if ((event.animationName === 'leaf-arrival-pulse' || event.animationName === 'leaf-arrival-outline')
        && isDomInstance(event.target, 'HTMLElement')) event.target.classList.remove('leaf-arrival');
    };
    container.addEventListener('animationend', onAnimationEnd);
    return () => {
      container.removeEventListener('animationend', onAnimationEnd);
    };
  }, [renderedPaneIdsKey]);

  useLayoutEffect(() => {
    if (!focusClaim || !sessionVisible || focusClaim.leafId !== activeLeafId) return;
    const active = document.activeElement;
    const userMovedOn = active && active !== focusClaim.focusOwner && !focusIsFree(panesContainerRef.current);
    if (!userMovedOn) focusShownLeaf(activeLeafId);
    const focused = document.activeElement;
    const landed = focused?.closest('[data-pane-id]')?.getAttribute('data-pane-id') === activeLeafId
      && focused.closest('[data-desktop-id]')?.getAttribute('data-desktop-id') === focusClaim.desktopId;
    if (userMovedOn || landed) useSessionStore.getState().focusClaimDelivered(focusClaim.id);
  }, [activeLeafId, focusClaim, focusShownLeaf, paneReadyFocusRequest, sessionVisible]);

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
      setPaneSurfaceReleased(paneId, !isActiveSession);
    }
  }, [isActiveSession, renderedPaneIds, renderedPaneIdsKey, setPaneSurfaceReleased]);

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


  useEscapeStack(() => setMaximizedLeafId(null), effectivePaneId !== null);

  const toggleZoomActivePane = useCallback(() => {
    setMaximizedLeafId(null);
    onSetZoomActive?.(!zoomActive);
  }, [onSetZoomActive, zoomActive, setMaximizedLeafId]);

  const focusLeaf = useCallback(
    (leafId: string) => {
      onFocusPane(leafId);
      if (tileLeafById.has(leafId)) {
        focusTile(leafId);
        return;
      }
      runtime.focusPane(leafId);
    },
    [focusTile, onFocusPane, runtime, tileLeafById],
  );

  const focusActivePane = useCallback(() => {
    if (activeLeafIsTile) {
      focusTile(activeLeafId);
      return;
    }
    focusActivePaneSurface();
  }, [activeLeafId, activeLeafIsTile, focusActivePaneSurface, focusTile]);

  const focusDocument = useCallback(
    (tileId: string) => {
      onSetZoomActive?.(false);
      focusLeaf(tileId);
      setMaximizedLeafId(tileId);
    },
    [focusLeaf, onSetZoomActive, setMaximizedLeafId],
  );

  const handleMovePane = useCallback(
    (direction: TerminalNavigationDirection) => {
      const tree = layoutTreeRef.current;
      if (tree && !effectivePaneId && (direction === 'left' || direction === 'right')) {
        const row = shownRow(readRow(tree), heldLeafIds, activeLeafId);
        const index = columnIndexOf(row, activeLeafId);
        const inColumn = index >= 0 ? findLeafInDirection(row[index].node, activeLeafId, direction) : null;
        const neighbour = index >= 0 ? row[index + (direction === 'left' ? -1 : 1)] : undefined;
        const target = inColumn ?? (neighbour ? lastUsedLeaf(neighbour, focusOrderRef.current) : null);
        if (target) focusLeaf(target);
        else onNavigateOutOfSession(direction);
        return;
      }
      const nextLeafId = renderedLayoutTree && findLeafInDirection(renderedLayoutTree, activeLeafId, direction);
      if (nextLeafId) {
        focusLeaf(nextLeafId);
        return;
      }
      onNavigateOutOfSession(direction);
    },
    [activeLeafId, effectivePaneId, focusLeaf, heldLeafIds, onNavigateOutOfSession, renderedLayoutTree],
  );

  const carryActiveColumn = (delta: -1 | 1) => {
    const tree = layoutTreeRef.current;
    if (!tree || effectivePaneId || !activeLeafId) return;
    const row = shownRow(readRow(tree), heldLeafIds, activeLeafId);
    const neighbour = row[columnIndexOf(row, activeLeafId) + delta];
    if (!neighbour) {
      onShelfLimit?.(`This tile is already at the ${delta < 0 ? 'start' : 'end'} of the row.`);
      return;
    }
    const firstShown = rowPlanRef.current?.firstColumn;
    if (firstShown && columnIndexOf(row, firstShown.leafId) === columnIndexOf(row, activeLeafId) && delta > 0) {
      useDesktopShelfStore.getState().setFirstColumn(desktopId, { leafId: neighbour.leafIds[0], index: firstShown.index });
    }
    void onMoveRowLeaf?.({ leafId: activeLeafId, anchorId: neighbour.leafIds[0], edge: delta < 0 ? 'left' : 'right', wholeColumn: true, activate: true })
      .catch((error: unknown) => onShelfLimit?.(`Could not carry that tile: ${error instanceof Error ? error.message : String(error)}`));
  };

  const sendActiveToShelf = () => {
    if (!rowInput || !rowPlan || effectivePaneId || !activeLeafId) return;
    const shelf = useDesktopShelfStore.getState();
    const report = (error: unknown) => onShelfLimit?.(`Could not send that tile to the shelf: ${error instanceof Error ? error.message : String(error)}`);
    const send = planSendToShelf(rowInput, rowPlan.screen, focusOrderRef.current);
    switch (send.kind) {
      case 'alone':
        onShelfLimit?.('This is the only tile on screen.');
        return;
      case 'unstack':
        focusLeaf(send.nextLeafId);
        void onMoveRowLeaf?.({ leafId: send.leafId, anchorId: send.anchorId, edge: 'right', wholeColumn: false, activate: false }).catch(report);
        return;
      case 'hold':
        focusLeaf(send.nextLeafId);
        shelf.holdLeaf(desktopId, send.leafId);
        return;
      case 'move':
        focusLeaf(send.nextLeafId);
        shelf.holdLeaf(desktopId, send.leafId);
        void onMoveRowLeaf?.({ leafId: send.leafId, anchorId: send.anchorId, edge: send.edge, wholeColumn: true, activate: false })
          .catch(report)
          .finally(() => useDesktopShelfStore.getState().releaseLeaf(desktopId, send.leafId));
    }
  };

  // Deciding here would read leaf state from a ref a child's ready callback can
  // observe before the parent mirrors it, leaving Cmd+W on the wrong leaf.
  const requestFocusForReadyPane = useCallback((paneId: string) => {
    if (
      !isActiveSessionRef.current ||
      !sessionViewVisibleRef.current ||
      activeAgentPaneIdRef.current !== paneId
    ) {
      return;
    }
    const active = document.activeElement;
    const claimed = focusClaimRef.current?.leafId === paneId;
    if (!claimed && active && active !== document.body) return;
    setPaneReadyFocusRequest((token) => token + 1);
  }, []);

  const handleGhosttyTerminalReady = useCallback(
    (paneId: string) => (terminal: GhosttyTerminalHandle) => {
      const ready = runtime.handleTerminalReady(paneId)(terminal);
      requestFocusForReadyPane(paneId);
      return ready;
    },
    [requestFocusForReadyPane, runtime],
  );

  const workspaceShortcutsActive = sessionVisible && shortcutsEnabled;
  useShortcut('terminal.open', focusActivePane, workspaceShortcutsActive);
  useShortcut(
    'terminal.find',
    () => {
      runtime.openFindInActivePane();
    },
    workspaceShortcutsActive,
  );
  useShortcut(
    'terminal.splitVertical',
    () => {
      handleSplit('vertical');
    },
    workspaceShortcutsActive,
  );
  useShortcut(
    'terminal.splitHorizontal',
    () => {
      handleSplit('horizontal');
    },
    workspaceShortcutsActive,
  );
  useShortcut('terminal.toggleZoom', toggleZoomActivePane, workspaceShortcutsActive);
  useShortcut('terminal.toggleMaximize', toggleMaximizeActivePane, workspaceShortcutsActive);
  useShortcut('terminal.close', handleCloseFocusedLeaf, workspaceShortcutsActive && splitLayoutActive);
  useShortcut('terminal.focusLeft', () => handleMovePane('left'), workspaceShortcutsActive);
  useShortcut('terminal.focusRight', () => handleMovePane('right'), workspaceShortcutsActive);
  useShortcut('terminal.focusUp', () => handleMovePane('up'), workspaceShortcutsActive);
  useShortcut('terminal.focusDown', () => handleMovePane('down'), workspaceShortcutsActive);
  const shelfMovesActive = desktopDrawn && shortcutsEnabled;
  useShortcut('shelf.carryLeft', () => carryActiveColumn(-1), shelfMovesActive);
  useShortcut('shelf.carryRight', () => carryActiveColumn(1), shelfMovesActive);
  useShortcut('shelf.sendToShelf', sendActiveToShelf, shelfMovesActive);

  const onShelfLimitRef = useRef(onShelfLimit);
  useLayoutEffect(() => {
    onShelfLimitRef.current = onShelfLimit;
  }, [onShelfLimit]);

  const paneFrameStyle = useCallback(
    (bounds: NormalizedPaneBounds) => ({
      left: `${bounds.left * 100}%`,
      top: `${bounds.top * 100}%`,
      width: `${bounds.width * 100}%`,
      height: `${bounds.height * 100}%`,
    }),
    [],
  );

  const reportShelfMove = useCallback((what: string) => (error: unknown) => {
    onShelfLimit?.(`Could not ${what}: ${error instanceof Error ? error.message : String(error)}`);
  }, [onShelfLimit]);

  const moveTileToShelf = (leafId: string, target: ShelfDropTarget) => {
    const input = rowInputRef.current;
    const plan = rowPlanRef.current;
    if (!input || !plan) return;
    if (leafId === activeLeafIdRef.current) {
      const send = planSendToShelf(input, plan.screen, focusOrderRef.current);
      if (send.kind === 'alone') return;
      focusLeaf(send.nextLeafId);
    }
    const shelf = useDesktopShelfStore.getState();
    shelf.holdLeaf(desktopId, leafId);
    void onMoveRowLeaf?.({ leafId, anchorId: target.anchorId, edge: target.edge, wholeColumn: false, activate: false })
      ?.catch((error: unknown) => {
        useDesktopShelfStore.getState().releaseLeaf(desktopId, leafId);
        reportShelfMove('move that tile to the shelf')(error);
      });
  };

  const reorderChip = (leafId: string, target: ShelfDropTarget) => {
    void onMoveRowLeaf?.({ leafId, anchorId: target.anchorId, edge: target.edge, wholeColumn: true, activate: false })
      ?.catch(reportShelfMove('reorder the shelf'));
  };

  const swapChipIntoSlot = async (chipLeafId: string, slotLeafId: string) => {
    const tree = layoutTreeRef.current;
    if (!tree) return;
    const shelf = useDesktopShelfStore.getState();
    const swap = planSlotSwap(tree, chipLeafId, slotLeafId);
    shelf.holdLeaf(desktopId, slotLeafId);
    try {
      await onMoveRowLeaf?.({ leafId: chipLeafId, anchorId: slotLeafId, edge: swap.chipEdge, wholeColumn: true, activate: true });
      if (swap.slotReturn) {
        await onMoveRowLeaf?.({ leafId: slotLeafId, ...swap.slotReturn, wholeColumn: true, activate: false });
      }
    } catch (error) {
      reportShelfMove('put that tile in the slot')(error);
    } finally {
      useDesktopShelfStore.getState().releaseLeaf(desktopId, slotLeafId);
    }
  };

  const dropChipOnTile = (leafId: string, target: DockTarget) => {
    if (target.replacesAnchor) {
      void swapChipIntoSlot(leafId, target.anchorId);
      return;
    }
    if (target.limitMessage) onShelfLimit?.(target.limitMessage);
    onMoveLeaf?.(leafId, target.anchorId, target.edge, target.ratio, true);
  };

  const rowDropsRef = useRef({ moveTileToShelf, reorderChip, dropChipOnTile });
  useLayoutEffect(() => {
    rowDropsRef.current = { moveTileToShelf, reorderChip, dropChipOnTile };
  });
  const rowDragOptions = useMemo(() => ({
    desktopId,
    panesContainer: () => panesContainerRef.current,
    showing: () => isActiveSessionRef.current,
    onTileToShelf: (leafId: string, target: ShelfDropTarget) => rowDropsRef.current.moveTileToShelf(leafId, target),
    onChipToShelf: (leafId: string, target: ShelfDropTarget) => rowDropsRef.current.reorderChip(leafId, target),
    onChipToTile: (leafId: string, target: DockTarget) => rowDropsRef.current.dropChipOnTile(leafId, target),
    fitChipOnEdge: (leafId: string, target: DockTarget, anchorBounds: NormalizedPaneBounds, containerHeightPx: number) => {
      const tree = layoutTreeRef.current;
      return tree ? fitColumnOnEdgeDrop(target, anchorBounds, containerHeightPx, columnStackNeedPx(tree, leafId)) : target;
    },
  }), [desktopId]);

  const { beginLeafDrag, beginChipDrag, effectiveDraggingLeafId, effectiveDockTarget, effectiveGhostPos } =
    useDesktopLeafDrag({
      row: rowDragOptions,
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
      const session = sessionById.get(agentPane.sessionId);
      return session ? sessionDisplayName(session) : agentPane.title || 'Session';
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
      const session = sessionById.get(agentPane.sessionId);
      return session ? sessionDisplayName(session) : agentPane.title || 'Pane';
    }
    const tile = tileLeafById.get(effectiveDraggingLeafId);
    if (tile) {
      const base = (tile.tileParams ?? '').split('/').filter(Boolean).pop();
      return base || tile.tileKind || 'Tile';
    }
    return 'Pane';
  }, [effectiveDraggingLeafId, agentPaneById, sessionById, tileLeafById]);

  const splitDividers = rowPlan?.dividers ?? [];

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
      const rect = container.getBoundingClientRect();
      const { splitId, direction, left, top, right, bottom } = divider;
      const endResizeGesture = beginResizeGesture(container, event.currentTarget, event.pointerId, splitId, direction);
      const spanNorm = direction === 'vertical' ? right - left : bottom - top;
      const axisPx = direction === 'vertical' ? rect.width : rect.height;
      const spanPx = spanNorm * axisPx;
      const ratioBounds = rowDragRatioBounds(direction, spanPx);
      draggingSplitRef.current = splitId;
      setResizingSplit({ splitId, direction });

      const computeRatio = (clientX: number, clientY: number): number => {
        let ratio = 0.5;
        if (spanNorm > 0) {
          if (direction === 'vertical') {
            ratio = ((clientX - rect.left) / rect.width - left) / spanNorm;
          } else {
            ratio = ((clientY - rect.top) / rect.height - top) / spanNorm;
          }
        }
        return Math.min(ratioBounds.max, Math.max(ratioBounds.min, ratio));
      };

      const onMove = (ev: PointerEvent) => {
        ev.preventDefault();
        ev.stopPropagation();
        suppressTerminalMouseDuringResize();
        const nextRatio = computeRatio(ev.clientX, ev.clientY);
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
        if (ratioRafRef.current != null) {
          window.cancelAnimationFrame(ratioRafRef.current);
          ratioRafRef.current = null;
        }
        endResizeGesture();
        setResizingSplit((current) => (current?.splitId === splitId ? null : current));
        dragCleanupRef.current = null;
      };
      const onCancel = () => {
        teardown();
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

  const sendWantedCols = useCallback((wanted: ReadonlyMap<string, number>) => {
    const dropPending = () => setPendingWantedCols((pending) => {
      const kept = new Map([...pending].filter(([leafId, cols]) => wanted.get(leafId) !== cols));
      return kept.size === pending.size ? pending : kept;
    });
    const input = rowInputRef.current;
    const areaCols = input ? areaInCols(input) : 0;
    void Promise.all([...wanted].map(([leafId, cols]) => onSetWantedCols?.(leafId, cols, cols ? areaCols : 0)))
      .catch(reportShelfMove('save that width'))
      .finally(dropPending);
  }, [onSetWantedCols, reportShelfMove]);

  const columnDragCancelRef = useRef<(() => void) | null>(null);
  useEscapeStack(
    () => columnDragCancelRef.current?.(),
    resizingSplit !== null && resizingSplit.direction === 'vertical',
  );

  const handleColumnDividerPointerDown = useCallback(
    (dividerIndex: number, splitId: string, event: React.PointerEvent<HTMLDivElement>) => {
      const container = panesContainerRef.current;
      const plan = rowPlanRef.current;
      const input = rowInputRef.current;
      if (event.button !== 0 || !container || !plan || !input) return;
      event.stopPropagation();
      const dividerElement = event.currentTarget;
      const { pointerId, clientX: startX } = event;
      const start = { screen: plan.screen, widths: plan.widths, firstColumn: plan.firstColumn };
      const measures = { activeLeafId: input.activeLeafId, areaPx: input.areaPx, narrowestPx: input.narrowestPx, pxPerCol: input.pxPerCol };
      const pendingAtStart = input.pendingWantedCols ?? NO_PENDING_WIDTHS;
      let endResizeGesture: (() => void) | null = null;

      const applyAt = (clientX: number) => {
        const drag = applyColumnDrag(start, dividerIndex, clientX - startX, measures);
        setPendingWantedCols(new Map([...pendingAtStart, ...drag.wantedCols]));
        useDesktopShelfStore.getState().setFirstColumn(desktopId, drag.firstColumn);
        return drag;
      };
      const onMove = (ev: PointerEvent) => {
        ev.preventDefault();
        ev.stopPropagation();
        if (!endResizeGesture) {
          if (Math.abs(ev.clientX - startX) < COLUMN_DRAG_ACTIVATION_PX) return;
          endResizeGesture = beginResizeGesture(container, dividerElement, pointerId, splitId, 'vertical');
          columnDragActiveRef.current = true;
          setResizingSplit({ splitId, direction: 'vertical' });
        }
        suppressTerminalMouseDuringResize();
        applyAt(ev.clientX);
      };
      const teardown = () => {
        window.removeEventListener('pointermove', onMove, true);
        window.removeEventListener('pointerup', onUp, true);
        window.removeEventListener('pointercancel', onCancel, true);
        window.removeEventListener('blur', onCancel);
        endResizeGesture?.();
        columnDragActiveRef.current = false;
        columnDragCancelRef.current = null;
        dragCleanupRef.current = null;
        setResizingSplit((current) => (current?.splitId === splitId ? null : current));
      };
      const onCancel = () => {
        const dragged = endResizeGesture !== null;
        teardown();
        if (!dragged) return;
        setPendingWantedCols(pendingAtStart);
        useDesktopShelfStore.getState().setFirstColumn(desktopId, start.firstColumn);
        scheduleTerminalFitAfterResize();
      };
      const onUp = (ev: PointerEvent) => {
        if (!endResizeGesture) {
          teardown();
          return;
        }
        ev.preventDefault();
        ev.stopPropagation();
        const drag = applyAt(ev.clientX);
        teardown();
        scheduleTerminalFitAfterResize();
        if (drag.wantedCols.size > 0) sendWantedCols(drag.wantedCols);
      };
      columnDragCancelRef.current = onCancel;
      dragCleanupRef.current = teardown;
      window.addEventListener('pointermove', onMove, true);
      window.addEventListener('pointerup', onUp, true);
      window.addEventListener('pointercancel', onCancel, true);
      window.addEventListener('blur', onCancel);
    },
    [desktopId, scheduleTerminalFitAfterResize, sendWantedCols],
  );

  const handleRenderedDividerPointerDown = useCallback(
    (divider: SplitDivider, event: React.PointerEvent<HTMLDivElement>) => {
      const dividerIndex = rowPlanRef.current ? columnDividerIndex(rowPlanRef.current.screen, divider.splitId) : -1;
      if (dividerIndex >= 0) handleColumnDividerPointerDown(dividerIndex, divider.splitId, event);
      else handleDividerPointerDown(divider, event);
    },
    [handleColumnDividerPointerDown, handleDividerPointerDown],
  );

  const clearWantedCols = useCallback((columns: readonly RowColumn[]) => {
    const wanted = new Map(columns.filter((column) => column.wantedCols).map((column) => [column.leafIds[0], 0]));
    if (wanted.size === 0) return false;
    setPendingWantedCols((pending) => new Map([...pending, ...wanted]));
    sendWantedCols(wanted);
    return true;
  }, [sendWantedCols]);

  const shelfWidthLabel = useShelfControls({
    active: desktopDrawn && shortcutsEnabled,
    rowInput,
    narrowestCols: shelfNarrowestCols,
    onSetNarrowestCols,
    onClearWantedCols: clearWantedCols,
    onShelfLimit,
  });

  const handleColumnDividerDoubleClick = useCallback((divider: SplitDivider) => {
    const screen = rowPlanRef.current?.screen;
    const dividerIndex = screen ? columnDividerIndex(screen, divider.splitId) : -1;
    if (screen && dividerIndex >= 0) clearWantedCols(screen.slice(dividerIndex, dividerIndex + 2));
  }, [clearWantedCols]);

  const beginChipDragRef = useRef(beginChipDrag);
  const clearWantedColsRef = useRef(clearWantedCols);
  useLayoutEffect(() => {
    beginChipDragRef.current = beginChipDrag;
    clearWantedColsRef.current = clearWantedCols;
  });
  useLayoutEffect(() => {
    const shelf = useDesktopShelfStore.getState();
    shelf.setRowActions(desktopId, {
      beginChipDrag: (column, event) => beginChipDragRef.current(column, event),
      clearDraggedWidths: () => {
        const input = rowInputRef.current;
        if (input && !clearWantedColsRef.current(readRow(input.tree))) onShelfLimitRef.current?.('This desktop has no dragged widths.');
      },
    });
    return () => useDesktopShelfStore.getState().setRowActions(desktopId, null);
  }, [desktopId]);

  useEffect(
    () => () => {
      dragCleanupRef.current?.();
    },
    [],
  );

  return {
    focusActiveLeaf: () => focusShownLeafRef.current(activeLeafIdRef.current),
    desktopId,
    desktopSelectionStyle,
    activeAgentPaneId,
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
    offScreenBrowserIds,
    sessionVisible,
    focusDocument,
    effectiveDraggingLeafId,
    effectiveDockTarget,
    effectiveGhostPos,
    focusModeTitle,
    reviewDeckTiles,
    draggingLeafLabel,
    splitDividers,
    handleRenderedDividerPointerDown,
    handleColumnDividerDoubleClick,
    desktopDirectory,
    desktopSessions,
    seedTargetSessions,
    gardenSeeds,
    onOpenSeed,
    onRevealSeedInGarden,
    onOpenAsk,
    backToCrewTileId,
    onBackToCrew,
    annotationApi,
    fontSize,
    resolvedTheme,
    enabled,
    shortcutsEnabled,
    isActiveSession,
    isSessionViewVisible,
    terminalsLive,
    onCancelCountdown,
    onTerminalPointerActivity,
    onTerminalFocus,
    onOpenMarkdown,
    onTerminalPathRefused,
    onSetZoomActive,
    onUndockTile,
    onUpdateTile,
    tileContents,
    onRequestTileContent,
    paneGeometry,
    shelfWidthLabel,
    keepLeavingFrame,
    staleBuildDismissed,
    setStaleBuildDismissed,
    pinnedSeedPopover,
    dismissSeedPopover,
    pinnedUsagePopover,
    dismissUsagePopover,
    provenancePopoverOwner,
    setProvenancePopoverOwner,
    sessionById,
    delegationSessionById,
    delegatesByDispatcherId,
    tileSessionOptions,
    runtime,
    terminalRefForPane,
    showPaneHeader,
    panePaths,
    renderedPaneBounds,
    tileBodyRefFor,
    focusLeaf,
    handleGhosttyTerminalReady,
    handleClosePane,
    paneFrameStyle,
    beginLeafDrag,
  };
}
