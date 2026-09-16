import type { SidebarProps, SidebarWorkspace } from './sidebarTypes';
import type { MouseEvent as ReactMouseEvent, PointerEvent as ReactPointerEvent } from 'react';
import { useCallback, useEffect, useRef, useState } from 'react';

function reachedDragThreshold(
  origin: { startX: number; startY: number },
  event: PointerEvent,
  threshold: number,
) {
  return Math.hypot(event.clientX - origin.startX, event.clientY - origin.startY) >= threshold;
}

export function useSidebarDrag({
  visibleVisualOrder,
  onWorkspaceReorder,
  onSessionDragStart,
  onSessionDragEnd,
}: Pick<SidebarProps, 'onWorkspaceReorder' | 'onSessionDragStart' | 'onSessionDragEnd'> & {
  visibleVisualOrder: SidebarWorkspace[];
}) {
  const activeGestureCleanup = useRef<(() => void) | null>(null);
  const cancelActiveGesture = useCallback(() => {
    const cleanup = activeGestureCleanup.current;
    activeGestureCleanup.current = null;
    cleanup?.();
  }, []);
  useEffect(() => cancelActiveGesture, [cancelActiveGesture]);
  const REORDER_THRESHOLD = 6;
  const [reorderDrag, setReorderDrag] = useState<{
    workspaceId: string;
    endpointId?: string;
  } | null>(null);
  const [sessionDragGhost, setSessionDragGhost] = useState<{
    x: number;
    y: number;
    label: string;
  } | null>(null);
  const [draggingSessionId, setDraggingSessionId] = useState<string | null>(null);
  const sessionDragRef = useRef<{
    pointerId: number;
    startX: number;
    startY: number;
    armed: boolean;
  } | null>(null);
  const suppressNextSessionClickRef = useRef(false);
  const [reorderSeamIndex, setReorderSeamIndex] = useState<number | null>(null);
  const reorderDragRef = useRef<{
    workspaceId: string;
    endpointId?: string;
    pointerId: number;
    startX: number;
    startY: number;
    armed: boolean;
    sourceEl: HTMLElement;
  } | null>(null);
  const reorderSeamIndexRef = useRef<number | null>(null);
  const suppressNextHeaderClickRef = useRef(false);

  const reorderParticipants = (endpointId?: string): SidebarWorkspace[] =>
    visibleVisualOrder.filter((workspace) => (workspace.endpointId || '') === (endpointId || ''));

  const updateReorderSeam = useCallback((index: number | null) => {
    reorderSeamIndexRef.current = index;
    setReorderSeamIndex(index);
  }, []);

  const nearestSeamIndex = useCallback((clientY: number): number | null => {
    const list = document.querySelector('.session-list');
    if (!list) {
      return null;
    }
    const seams = Array.from(list.querySelectorAll<HTMLElement>('.workspace-reorder-seam'));
    let best: number | null = null;
    let bestDist = Infinity;
    for (const seam of seams) {
      const index = Number(seam.dataset.seamIndex);
      if (Number.isNaN(index)) {
        continue;
      }
      const rect = seam.getBoundingClientRect();
      const center = (rect.top + rect.bottom) / 2;
      const dist = Math.abs(clientY - center);
      if (dist < bestDist) {
        bestDist = dist;
        best = index;
      }
    }
    return best;
  }, []);

  const endReorderDrag = useCallback(() => {
    reorderDragRef.current = null;
    setReorderDrag(null);
    updateReorderSeam(null);
  }, [updateReorderSeam]);

  const commitReorder = useCallback(
    (workspaceId: string, endpointId: string | undefined, seamIndex: number | null) => {
      if (seamIndex == null || !onWorkspaceReorder) {
        return;
      }
      const participants = visibleVisualOrder.filter(
        (workspace) => (workspace.endpointId || '') === (endpointId || ''),
      );
      const fromIndex = participants.findIndex((workspace) => workspace.id === workspaceId);
      if (fromIndex < 0) {
        return;
      }
      // Removing the moved row shifts later rows up by one, so fromIndex and
      // fromIndex+1 both land it back where it started.
      if (seamIndex === fromIndex || seamIndex === fromIndex + 1) {
        return;
      }
      const remaining = participants.filter((workspace) => workspace.id !== workspaceId);
      const insertAt = seamIndex > fromIndex ? seamIndex - 1 : seamIndex;
      const prevWorkspaceId = insertAt > 0 ? remaining[insertAt - 1]?.id : undefined;
      const nextWorkspaceId = insertAt < remaining.length ? remaining[insertAt]?.id : undefined;
      onWorkspaceReorder({ workspaceId, prevWorkspaceId, nextWorkspaceId });
    },
    [onWorkspaceReorder, visibleVisualOrder],
  );

  const handleHeaderPointerDown = useCallback(
    (workspace: SidebarWorkspace, event: ReactPointerEvent<HTMLButtonElement>) => {
      if (event.button !== 0 || !onWorkspaceReorder) {
        return;
      }
      cancelActiveGesture();
      const sourceEl = event.currentTarget;
      reorderDragRef.current = {
        workspaceId: workspace.id,
        endpointId: workspace.endpointId,
        pointerId: event.pointerId,
        startX: event.clientX,
        startY: event.clientY,
        armed: false,
        sourceEl,
      };

      const onMove = (moveEvent: PointerEvent) => {
        const drag = reorderDragRef.current;
        if (!drag || moveEvent.pointerId !== drag.pointerId) {
          return;
        }
        if (!drag.armed) {
          if (!reachedDragThreshold(drag, moveEvent, REORDER_THRESHOLD)) {
            return;
          }
          drag.armed = true;
          suppressNextHeaderClickRef.current = true;
          try {
            sourceEl.setPointerCapture(drag.pointerId);
          } catch {
            // setPointerCapture can throw if the pointer is already gone; ignore.
          }
          setReorderDrag({ workspaceId: drag.workspaceId, endpointId: drag.endpointId });
        }
        updateReorderSeam(nearestSeamIndex(moveEvent.clientY));
      };

      const finish = () => {
        window.removeEventListener('pointermove', onMove);
        window.removeEventListener('pointerup', onUp);
        window.removeEventListener('pointercancel', onCancel);
        if (activeGestureCleanup.current === onCancel) activeGestureCleanup.current = null;
        const drag = reorderDragRef.current;
        if (drag) {
          try {
            sourceEl.releasePointerCapture(drag.pointerId);
          } catch {
            // releasePointerCapture throws when capture was never taken; ignore.
          }
        }
      };

      const onUp = (upEvent: PointerEvent) => {
        const drag = reorderDragRef.current;
        finish();
        if (!drag || upEvent.pointerId !== drag.pointerId) {
          endReorderDrag();
          return;
        }
        if (drag.armed) {
          commitReorder(drag.workspaceId, drag.endpointId, reorderSeamIndexRef.current);
        }
        endReorderDrag();
      };

      const onCancel = () => {
        finish();
        endReorderDrag();
      };

      activeGestureCleanup.current = onCancel;
      window.addEventListener('pointermove', onMove);
      window.addEventListener('pointerup', onUp);
      window.addEventListener('pointercancel', onCancel);
    },
    [
      onWorkspaceReorder,
      nearestSeamIndex,
      updateReorderSeam,
      endReorderDrag,
      commitReorder,
      cancelActiveGesture,
    ],
  );

  const reorderActiveParticipants = reorderDrag ? reorderParticipants(reorderDrag.endpointId) : [];
  const reorderSeamIndexByWorkspaceId = reorderDrag
    ? new Map(reorderActiveParticipants.map((workspace, index) => [workspace.id, index]))
    : null;
  const reorderTrailingSeamIndex = reorderActiveParticipants.length;
  const lastReorderParticipantId =
    reorderActiveParticipants[reorderActiveParticipants.length - 1]?.id;

  const renderReorderSeam = (index: number) => (
    <div
      className={`workspace-reorder-seam ${reorderSeamIndex === index ? 'active' : ''}`.trim()}
      data-testid={`workspace-reorder-seam-${index}`}
      data-seam-index={index}
      aria-hidden="true"
      onPointerEnter={() => {
        if (reorderDragRef.current?.armed) {
          updateReorderSeam(index);
        }
      }}
    >
      <span className="workspace-reorder-seam-line" />
    </div>
  );

  const handleHeaderClickCapture = useCallback((event: ReactMouseEvent) => {
    if (suppressNextHeaderClickRef.current) {
      suppressNextHeaderClickRef.current = false;
      event.preventDefault();
      event.stopPropagation();
    }
  }, []);

  useEffect(() => endReorderDrag, [endReorderDrag]);

  // Unlike the workspace reorder, this takes NO pointer capture: the drop targets
  // rely on their own pointer handlers firing as the cursor moves over them.
  const SESSION_DRAG_THRESHOLD = 6;
  const handleSessionPointerDown = useCallback(
    (
      workspace: SidebarWorkspace,
      paneId: string,
      sessionId: string,
      label: string,
      event: ReactPointerEvent<HTMLButtonElement>,
    ) => {
      if (event.button !== 0 || !onSessionDragStart) {
        return;
      }
      // A fresh press always starts un-suppressed, so a drag that ended elsewhere can't swallow this row's next click.
      cancelActiveGesture();
      suppressNextSessionClickRef.current = false;
      sessionDragRef.current = {
        pointerId: event.pointerId,
        startX: event.clientX,
        startY: event.clientY,
        armed: false,
      };

      const onMove = (moveEvent: PointerEvent) => {
        const drag = sessionDragRef.current;
        if (!drag || moveEvent.pointerId !== drag.pointerId) {
          return;
        }
        if (!drag.armed) {
          if (!reachedDragThreshold(drag, moveEvent, SESSION_DRAG_THRESHOLD)) {
            return;
          }
          drag.armed = true;
          suppressNextSessionClickRef.current = true;
          setDraggingSessionId(sessionId);
          onSessionDragStart(workspace.id, workspace.endpointId, paneId);
        }
        setSessionDragGhost({ x: moveEvent.clientX, y: moveEvent.clientY, label });
      };

      const finish = () => {
        window.removeEventListener('pointermove', onMove);
        window.removeEventListener('pointerup', onUp);
        window.removeEventListener('pointercancel', onCancel);
        if (activeGestureCleanup.current === onCancel) activeGestureCleanup.current = null;
      };

      const teardownDrag = (armed: boolean) => {
        sessionDragRef.current = null;
        setSessionDragGhost(null);
        setDraggingSessionId(null);
        if (armed) {
          onSessionDragEnd?.();
        }
      };

      const onUp = () => {
        const drag = sessionDragRef.current;
        finish();
        teardownDrag(Boolean(drag?.armed));
      };

      const onCancel = () => {
        const drag = sessionDragRef.current;
        finish();
        teardownDrag(Boolean(drag?.armed));
      };

      activeGestureCleanup.current = onCancel;
      window.addEventListener('pointermove', onMove);
      window.addEventListener('pointerup', onUp);
      window.addEventListener('pointercancel', onCancel);
    },
    [onSessionDragStart, onSessionDragEnd, cancelActiveGesture],
  );

  const handleSessionClickCapture = useCallback((event: ReactMouseEvent) => {
    if (suppressNextSessionClickRef.current) {
      suppressNextSessionClickRef.current = false;
      event.preventDefault();
      event.stopPropagation();
    }
  }, []);

  return {
    reorderDrag,
    sessionDragGhost,
    draggingSessionId,
    reorderSeamIndexByWorkspaceId,
    reorderTrailingSeamIndex,
    lastReorderParticipantId,
    renderReorderSeam,
    handleHeaderPointerDown,
    handleHeaderClickCapture,
    handleSessionPointerDown,
    handleSessionClickCapture,
  };
}
