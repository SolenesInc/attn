import { useCallback, useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react';
import type { Box, DropEdge } from './migrationDraft';
import { useLatest } from './useLatest';

const DRAG_ACTIVATION_PX = 4;

export interface GroupDropTarget {
  desktopKey: string;
  anchorGroupId: string | null;
  edge: DropEdge;
  box: Box;
  empty: boolean;
  label: string;
}

export interface GroupDragView {
  groupId: string;
  x: number;
  y: number;
  target: GroupDropTarget | null;
}

interface Gesture {
  groupId: string;
  pointerId: number;
  startX: number;
  startY: number;
  element: HTMLElement;
  active: boolean;
}

interface Options {
  resolveTarget: (groupId: string, x: number, y: number) => GroupDropTarget | null;
  onDrop: (groupId: string, target: GroupDropTarget) => void;
}

export function useGroupDrag({ resolveTarget, onDrop }: Options) {
  const [drag, setDrag] = useState<GroupDragView | null>(null);
  const gestureRef = useRef<Gesture | null>(null);
  const suppressClickRef = useRef(false);
  const optionsRef = useLatest({ resolveTarget, onDrop });

  const cancel = useCallback(() => {
    const gesture = gestureRef.current;
    gestureRef.current = null;
    if (gesture?.element.hasPointerCapture?.(gesture.pointerId)) {
      gesture.element.releasePointerCapture(gesture.pointerId);
    }
    setDrag(null);
  }, []);

  useEffect(() => {
    const onMove = (event: PointerEvent) => {
      const gesture = gestureRef.current;
      if (!gesture || event.pointerId !== gesture.pointerId) return;
      if (!gesture.active) {
        if (Math.hypot(event.clientX - gesture.startX, event.clientY - gesture.startY) < DRAG_ACTIVATION_PX) return;
        gesture.active = true;
        suppressClickRef.current = true;
        gesture.element.setPointerCapture?.(gesture.pointerId);
      }
      event.preventDefault();
      setDrag({
        groupId: gesture.groupId,
        x: event.clientX,
        y: event.clientY,
        target: optionsRef.current.resolveTarget(gesture.groupId, event.clientX, event.clientY),
      });
    };
    const onUp = (event: PointerEvent) => {
      const gesture = gestureRef.current;
      if (!gesture || event.pointerId !== gesture.pointerId) return;
      const target = gesture.active
        ? optionsRef.current.resolveTarget(gesture.groupId, event.clientX, event.clientY)
        : null;
      if (gesture.active) event.preventDefault();
      cancel();
      if (target) optionsRef.current.onDrop(gesture.groupId, target);
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || !gestureRef.current?.active) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      cancel();
    };
    const onClick = (event: MouseEvent) => {
      if (!suppressClickRef.current) return;
      suppressClickRef.current = false;
      event.preventDefault();
      event.stopImmediatePropagation();
    };
    const resetClick = () => {
      suppressClickRef.current = false;
    };
    document.addEventListener('pointermove', onMove, { passive: false });
    document.addEventListener('pointerup', onUp);
    document.addEventListener('pointercancel', cancel);
    document.addEventListener('pointerdown', resetClick, true);
    document.addEventListener('click', onClick, true);
    document.addEventListener('keydown', onKey, true);
    window.addEventListener('blur', cancel);
    return () => {
      document.removeEventListener('pointermove', onMove);
      document.removeEventListener('pointerup', onUp);
      document.removeEventListener('pointercancel', cancel);
      document.removeEventListener('pointerdown', resetClick, true);
      document.removeEventListener('click', onClick, true);
      document.removeEventListener('keydown', onKey, true);
      window.removeEventListener('blur', cancel);
    };
  }, [cancel, optionsRef]);

  const onPointerDown = useCallback((event: ReactPointerEvent<HTMLElement>) => {
    if (event.button !== 0) return;
    const source = (event.target as HTMLElement).closest<HTMLElement>('[data-drag-group]');
    if (!source || (event.target as HTMLElement).closest('[data-no-drag]')) return;
    gestureRef.current = {
      groupId: source.dataset.dragGroup ?? '',
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      element: source,
      active: false,
    };
  }, []);

  const onLostPointerCapture = useCallback(() => {
    if (gestureRef.current?.active) cancel();
  }, [cancel]);

  return { drag, onPointerDown, onLostPointerCapture };
}
