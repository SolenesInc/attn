import { useCallback, useEffect, useRef, useState } from 'react';
import type { DockTarget } from '../components/SessionTerminalWorkspace/dockTarget';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { withFreshDesktopRevisions } from '../hooks/desktopRevisions';
import type { useDesktopRuntimeController } from '../hooks/useDesktopRuntimeController';
import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { SIDEBAR_LEAF_DROP_PLACEMENT, type LeafDesktopDragState, type LeafDragPreviewState } from './appSupport';

const HOVER_SWITCH_DELAY_MS = 320;

interface Options {
  currentDesktopIdRef: React.RefObject<string | null>;
  getDesktopLeafDropSnapshot: ReturnType<typeof useDesktopRuntimeController>['getDesktopLeafDropSnapshot'];
  handleSelectDesktop: (desktopId: string) => void;
  showError: (message: string) => void;
}

function failureMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function isDesktopOfProfile(desktopId: string): boolean {
  return useProfilesStore.getState().desktops.some((desktop) => desktop.id === desktopId);
}

export function useLeafDrag({
  currentDesktopIdRef,
  getDesktopLeafDropSnapshot,
  handleSelectDesktop,
  showError,
}: Options) {
  const { sendDesktopMoveLeaf, sendDesktopCreate, sendDesktopSetCurrent } = useDaemonApi();
  const getActiveLeafDropSnapshot = useCallback(
    () => getDesktopLeafDropSnapshot(currentDesktopIdRef.current),
    [getDesktopLeafDropSnapshot, currentDesktopIdRef],
  );
  const [leafDragPreview, setLeafDragPreview] = useState<LeafDragPreviewState | null>(null);
  const [leafDesktopDrag, setLeafDesktopDrag] = useState<LeafDesktopDragState | null>(null);
  const [dragHoverDesktopId, setDragHoverDesktopId] = useState<string | null>(null);
  const leafDesktopDragRef = useRef<LeafDesktopDragState | null>(null);
  const hoverTimerRef = useRef<number | null>(null);
  const dragEndTimerRef = useRef<number | null>(null);
  const mountedRef = useRef(true);

  const clearDragEndTimer = useCallback(() => {
    if (dragEndTimerRef.current !== null) {
      window.clearTimeout(dragEndTimerRef.current);
      dragEndTimerRef.current = null;
    }
  }, []);

  const clearHoverTimer = useCallback(() => {
    if (hoverTimerRef.current !== null) {
      window.clearTimeout(hoverTimerRef.current);
      hoverTimerRef.current = null;
    }
  }, []);

  const beginDrag = useCallback(
    (sourceDesktopId: string | null, leafId: string) => {
      clearHoverTimer();
      clearDragEndTimer();
      const drag = sourceDesktopId ? { sourceDesktopId, leafId } : null;
      leafDesktopDragRef.current = drag;
      setLeafDesktopDrag(drag);
      setDragHoverDesktopId(null);
      setLeafDragPreview({ draggingLeafId: leafId, dockTarget: null, ghostPos: null });
    },
    [clearDragEndTimer, clearHoverTimer],
  );

  const handleLeafDragStart = useCallback(
    (leafId: string) => beginDrag(currentDesktopIdRef.current, leafId),
    [beginDrag, currentDesktopIdRef],
  );

  const handleSessionDragStart = useCallback(
    (desktopId: string, _endpointId: string | undefined, paneId: string) => beginDrag(desktopId, paneId),
    [beginDrag],
  );

  const handleLeafDragGhostMove = useCallback((x: number, y: number) => {
    setLeafDragPreview((prev) => (prev ? { ...prev, ghostPos: { x, y } } : prev));
  }, []);

  const handleLeafDragPreview = useCallback((target: DockTarget | null) => {
    setLeafDragPreview((prev) => (prev ? { ...prev, dockTarget: target } : prev));
  }, []);

  const handleLeafDragEnd = useCallback(() => {
    clearHoverTimer();
    clearDragEndTimer();
    if (!mountedRef.current) return;
    dragEndTimerRef.current = window.setTimeout(() => {
      dragEndTimerRef.current = null;
      leafDesktopDragRef.current = null;
      setLeafDesktopDrag(null);
      setLeafDragPreview(null);
      setDragHoverDesktopId(null);
    }, 0);
  }, [clearDragEndTimer, clearHoverTimer]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      clearHoverTimer();
      clearDragEndTimer();
      leafDesktopDragRef.current = null;
    };
  }, [clearDragEndTimer, clearHoverTimer]);

  const acceptsDrop = useCallback((desktopId: string) => {
    const drag = leafDesktopDragRef.current;
    return Boolean(drag && desktopId !== drag.sourceDesktopId && isDesktopOfProfile(desktopId));
  }, []);

  const handleDesktopDragEnter = useCallback(
    (desktop: { id: string }) => {
      if (!acceptsDrop(desktop.id)) return;
      clearHoverTimer();
      setDragHoverDesktopId(desktop.id);
      hoverTimerRef.current = window.setTimeout(() => {
        hoverTimerRef.current = null;
        handleSelectDesktop(desktop.id);
      }, HOVER_SWITCH_DELAY_MS);
    },
    [acceptsDrop, clearHoverTimer, handleSelectDesktop],
  );

  const handleDesktopDragLeave = useCallback(
    (desktop: { id: string }) => {
      if (dragHoverDesktopId !== desktop.id) return;
      clearHoverTimer();
      setDragHoverDesktopId(null);
    },
    [clearHoverTimer, dragHoverDesktopId],
  );

  const sendLeafToDesktop = useCallback(
    (drag: LeafDesktopDragState, targetDesktopId: string, targetRevision?: number) =>
      withFreshDesktopRevisions(
        targetRevision === undefined ? [drag.sourceDesktopId, targetDesktopId] : [drag.sourceDesktopId],
        (revisionOf) =>
          sendDesktopMoveLeaf({
            sourceDesktopId: drag.sourceDesktopId,
            targetDesktopId,
            leafId: drag.leafId,
            edge: SIDEBAR_LEAF_DROP_PLACEMENT.edge,
            leafShare: SIDEBAR_LEAF_DROP_PLACEMENT.leafShare,
            expectedSourceRevision: revisionOf(drag.sourceDesktopId),
            expectedTargetRevision: targetRevision ?? revisionOf(targetDesktopId),
          }),
      ),
    [sendDesktopMoveLeaf],
  );

  const handleSurfaceLeafDrop = useCallback(
    (
      sourceDesktopId: string,
      leafId: string,
      anchorId: string | undefined,
      edge: 'left' | 'right' | 'top' | 'bottom',
      leafShare: number | undefined,
    ) => {
      const targetDesktopId = currentDesktopIdRef.current ?? sourceDesktopId;
      void withFreshDesktopRevisions([...new Set([sourceDesktopId, targetDesktopId])], (revisionOf) =>
        sendDesktopMoveLeaf({
          sourceDesktopId,
          targetDesktopId,
          leafId,
          anchorId,
          edge,
          leafShare,
          expectedSourceRevision: revisionOf(sourceDesktopId),
          expectedTargetRevision: revisionOf(targetDesktopId),
        }),
      ).catch((error) => {
        showError(`Could not move that pane: ${failureMessage(error)}`);
      });
    },
    [currentDesktopIdRef, sendDesktopMoveLeaf, showError],
  );

  const handleDesktopDragDrop = useCallback(
    (desktop: { id: string }) => {
      const drag = leafDesktopDragRef.current;
      if (!drag || !acceptsDrop(desktop.id)) return;
      clearHoverTimer();
      setDragHoverDesktopId(null);
      handleSelectDesktop(desktop.id);
      void sendLeafToDesktop(drag, desktop.id).catch((error) => {
        showError(`Could not move that pane: ${failureMessage(error)}`);
      });
    },
    [acceptsDrop, clearHoverTimer, handleSelectDesktop, sendLeafToDesktop, showError],
  );

  const handleNewDesktopDrop = useCallback(() => {
    const drag = leafDesktopDragRef.current;
    const profileId = useProfilesStore.getState().selectedProfileId;
    if (!drag || !profileId) return;
    clearHoverTimer();
    setDragHoverDesktopId(null);
    void sendDesktopCreate(profileId)
      .then(async (result) => {
        const created = result.desktops?.[0];
        if (!created) throw new Error('The daemon created no desktop.');
        await sendLeafToDesktop(drag, created.id, created.revision);
        useSessionStore.getState().setView('session');
        await sendDesktopSetCurrent(profileId, created.id);
      })
      .catch((error) => {
        showError(`Could not move that pane to a new desktop: ${failureMessage(error)}`);
      });
  }, [clearHoverTimer, sendDesktopCreate, sendDesktopSetCurrent, sendLeafToDesktop, showError]);

  return {
    getActiveLeafDropSnapshot,
    leafDragPreview,
    leafDesktopDrag,
    dragHoverDesktopId,
    handleLeafDragStart,
    handleSessionDragStart,
    handleLeafDragGhostMove,
    handleLeafDragPreview,
    handleLeafDragEnd,
    handleSurfaceLeafDrop,
    handleDesktopDragEnter,
    handleDesktopDragLeave,
    handleDesktopDragDrop,
    handleNewDesktopDrop,
  };
}
