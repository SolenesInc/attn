import { useCallback, useEffect, useRef, useState } from 'react';
import type { DockTarget } from '../components/SessionTerminalWorkspace/dockTarget';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useSessionWorkspaceController } from '../hooks/useSessionWorkspaceController';
import {
  LeafDragPreviewState,
  LeafWorkspaceDragState,
  SIDEBAR_LEAF_DROP_PLACEMENT,
} from './appSupport';

interface Options {
  activeWorkspaceIdRef: React.RefObject<string | null>;
  getWorkspaceLeafDropSnapshot: ReturnType<
    typeof useSessionWorkspaceController
  >['getWorkspaceLeafDropSnapshot'];
  handleSelectWorkspace: (id: string) => void;
}
export function useWorkspaceDrag({
  activeWorkspaceIdRef,
  getWorkspaceLeafDropSnapshot,
  handleSelectWorkspace,
}: Options) {
  const { sendWorkspaceMoveLeafToWorkspace, sendWorkspaceMoveLeafToNewWorkspace } = useDaemonApi();
  const getActiveLeafDropSnapshot = useCallback(
    () => getWorkspaceLeafDropSnapshot(activeWorkspaceIdRef.current),
    [getWorkspaceLeafDropSnapshot, activeWorkspaceIdRef],
  );
  const [leafWorkspaceDrag, setLeafWorkspaceDrag] = useState<LeafWorkspaceDragState | null>(null);
  const [leafDragPreview, setLeafDragPreview] = useState<LeafDragPreviewState | null>(null);
  const [dragHoverWorkspaceId, setDragHoverWorkspaceId] = useState<string | null>(null);
  const leafWorkspaceDragRef = useRef<LeafWorkspaceDragState | null>(null);
  const dragHoverTimerRef = useRef<number | null>(null);
  const dragEndTimerRef = useRef<number | null>(null);
  const mountedRef = useRef(true);

  const clearDragEndTimer = useCallback(() => {
    if (dragEndTimerRef.current !== null) {
      window.clearTimeout(dragEndTimerRef.current);
      dragEndTimerRef.current = null;
    }
  }, []);

  const clearWorkspaceDragHoverTimer = useCallback(() => {
    if (dragHoverTimerRef.current != null) {
      window.clearTimeout(dragHoverTimerRef.current);
      dragHoverTimerRef.current = null;
    }
  }, []);

  const handleLeafDragStart = useCallback(
    (sourceWorkspaceId: string, sourceEndpointId: string | undefined, leafId: string) => {
      clearWorkspaceDragHoverTimer();
      clearDragEndTimer();
      const next = { sourceWorkspaceId, sourceEndpointId, leafId };
      leafWorkspaceDragRef.current = next;
      setLeafWorkspaceDrag(next);
      setLeafDragPreview({ draggingLeafId: leafId, dockTarget: null, ghostPos: null });
      setDragHoverWorkspaceId(null);
    },
    [clearWorkspaceDragHoverTimer, clearDragEndTimer],
  );

  const handleLeafDragGhostMove = useCallback((x: number, y: number) => {
    setLeafDragPreview((prev) => (prev ? { ...prev, ghostPos: { x, y } } : prev));
  }, []);

  const handleLeafDragPreview = useCallback((target: DockTarget | null) => {
    setLeafDragPreview((prev) => (prev ? { ...prev, dockTarget: target } : prev));
  }, []);

  const handleLeafDragEnd = useCallback(() => {
    clearWorkspaceDragHoverTimer();
    clearDragEndTimer();
    if (!mountedRef.current) return;
    dragEndTimerRef.current = window.setTimeout(() => {
      dragEndTimerRef.current = null;
      leafWorkspaceDragRef.current = null;
      setLeafWorkspaceDrag(null);
      setLeafDragPreview(null);
      setDragHoverWorkspaceId(null);
    }, 0);
  }, [clearWorkspaceDragHoverTimer, clearDragEndTimer]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      clearWorkspaceDragHoverTimer();
      clearDragEndTimer();
      leafWorkspaceDragRef.current = null;
    };
  }, [clearWorkspaceDragHoverTimer, clearDragEndTimer]);

  const canMoveDraggedLeafToWorkspace = useCallback(
    (workspace: { id: string; endpointId?: string }) => {
      const drag = leafWorkspaceDragRef.current;
      return Boolean(
        drag &&
          workspace.id !== drag.sourceWorkspaceId &&
          (workspace.endpointId || '') === (drag.sourceEndpointId || ''),
      );
    },
    [],
  );

  const handleWorkspaceDragEnter = useCallback(
    (workspace: { id: string; endpointId?: string }) => {
      if (!canMoveDraggedLeafToWorkspace(workspace)) {
        return;
      }
      clearWorkspaceDragHoverTimer();
      setDragHoverWorkspaceId(workspace.id);
      dragHoverTimerRef.current = window.setTimeout(() => {
        dragHoverTimerRef.current = null;
        handleSelectWorkspace(workspace.id);
      }, 320);
    },
    [canMoveDraggedLeafToWorkspace, clearWorkspaceDragHoverTimer, handleSelectWorkspace],
  );

  const handleWorkspaceDragLeave = useCallback(
    (workspace: { id: string; endpointId?: string }) => {
      if (dragHoverWorkspaceId !== workspace.id) {
        return;
      }
      clearWorkspaceDragHoverTimer();
      setDragHoverWorkspaceId(null);
    },
    [clearWorkspaceDragHoverTimer, dragHoverWorkspaceId],
  );

  const handleWorkspaceDragDrop = useCallback(
    (workspace: { id: string; endpointId?: string }) => {
      const drag = leafWorkspaceDragRef.current;
      if (!drag || !canMoveDraggedLeafToWorkspace(workspace)) {
        return;
      }
      clearWorkspaceDragHoverTimer();
      setDragHoverWorkspaceId(null);
      handleSelectWorkspace(workspace.id);
      void sendWorkspaceMoveLeafToWorkspace(
        drag.sourceWorkspaceId,
        workspace.id,
        drag.leafId,
        SIDEBAR_LEAF_DROP_PLACEMENT,
      ).catch(() => {});
    },
    [
      canMoveDraggedLeafToWorkspace,
      clearWorkspaceDragHoverTimer,
      handleSelectWorkspace,
      sendWorkspaceMoveLeafToWorkspace,
    ],
  );

  const handleNewWorkspaceDrop = useCallback(() => {
    const drag = leafWorkspaceDragRef.current;
    if (!drag) {
      return;
    }
    clearWorkspaceDragHoverTimer();
    setDragHoverWorkspaceId(null);
    void sendWorkspaceMoveLeafToNewWorkspace(
      drag.sourceWorkspaceId,
      drag.leafId,
      SIDEBAR_LEAF_DROP_PLACEMENT,
    ).catch(() => {});
  }, [clearWorkspaceDragHoverTimer, sendWorkspaceMoveLeafToNewWorkspace]);

  return {
    getActiveLeafDropSnapshot,
    leafWorkspaceDrag,
    leafDragPreview,
    dragHoverWorkspaceId,
    handleLeafDragStart,
    handleLeafDragGhostMove,
    handleLeafDragPreview,
    handleLeafDragEnd,
    handleWorkspaceDragEnter,
    handleWorkspaceDragLeave,
    handleWorkspaceDragDrop,
    handleNewWorkspaceDrop,
  };
}
