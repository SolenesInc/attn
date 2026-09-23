import { useCallback, useEffect, useRef, useState } from 'react';
import type { DockTarget } from '../components/SessionTerminalWorkspace/dockTarget';
import type { useDesktopRuntimeController } from '../hooks/useDesktopRuntimeController';
import type { LeafDragPreviewState } from './appSupport';

interface Options {
  currentDesktopIdRef: React.RefObject<string | null>;
  getDesktopLeafDropSnapshot: ReturnType<typeof useDesktopRuntimeController>['getDesktopLeafDropSnapshot'];
}
export function useLeafDrag({ currentDesktopIdRef, getDesktopLeafDropSnapshot }: Options) {
  const getActiveLeafDropSnapshot = useCallback(
    () => getDesktopLeafDropSnapshot(currentDesktopIdRef.current),
    [getDesktopLeafDropSnapshot, currentDesktopIdRef],
  );
  const [leafDragPreview, setLeafDragPreview] = useState<LeafDragPreviewState | null>(null);
  const dragEndTimerRef = useRef<number | null>(null);
  const mountedRef = useRef(true);

  const clearDragEndTimer = useCallback(() => {
    if (dragEndTimerRef.current !== null) {
      window.clearTimeout(dragEndTimerRef.current);
      dragEndTimerRef.current = null;
    }
  }, []);

  const handleLeafDragStart = useCallback(
    (leafId: string) => {
      clearDragEndTimer();
      setLeafDragPreview({ draggingLeafId: leafId, dockTarget: null, ghostPos: null });
    },
    [clearDragEndTimer],
  );

  const handleLeafDragGhostMove = useCallback((x: number, y: number) => {
    setLeafDragPreview((prev) => (prev ? { ...prev, ghostPos: { x, y } } : prev));
  }, []);

  const handleLeafDragPreview = useCallback((target: DockTarget | null) => {
    setLeafDragPreview((prev) => (prev ? { ...prev, dockTarget: target } : prev));
  }, []);

  const handleLeafDragEnd = useCallback(() => {
    clearDragEndTimer();
    if (!mountedRef.current) return;
    dragEndTimerRef.current = window.setTimeout(() => {
      dragEndTimerRef.current = null;
      setLeafDragPreview(null);
    }, 0);
  }, [clearDragEndTimer]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      clearDragEndTimer();
    };
  }, [clearDragEndTimer]);

  return {
    getActiveLeafDropSnapshot,
    leafDragPreview,
    handleLeafDragStart,
    handleLeafDragGhostMove,
    handleLeafDragPreview,
    handleLeafDragEnd,
  };
}
