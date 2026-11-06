import { useCallback, useLayoutEffect, useRef, type PointerEvent as ReactPointerEvent } from 'react';
import { clampSidebarPx, renderedSidebarWidth, sidebarWidthLabel, storedSidebarPx } from '../components/sidebarWidth';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { useSidebarDrag } from '../store/sidebarDrag';
import { beginPointerResize } from '../utils/resizeGesture';
import { useAppInputs } from './AppContexts';

const WIDTH_VARIABLE = '--sidebar-width';

function showSidebarWidth(px: number) {
  document.documentElement.style.setProperty(WIDTH_VARIABLE, renderedSidebarWidth(px));
}

export function useSidebarWidth() {
  const { settings } = useAppInputs();
  const { sendSetSetting } = useDaemonApi();
  const resizable = settings.shelf_layout_enabled === 'true';
  const storedPx = storedSidebarPx(settings.sidebar_width);
  const dragging = useSidebarDrag((state) => state.dragging);
  const labelRef = useRef<HTMLDivElement>(null);
  const storedPxRef = useRef(storedPx);
  storedPxRef.current = storedPx;
  const cancelLiveDrag = useRef<(() => void) | null>(null);

  useLayoutEffect(() => {
    if (!resizable) {
      document.documentElement.style.removeProperty(WIDTH_VARIABLE);
      return;
    }
    if (!cancelLiveDrag.current) showSidebarWidth(storedPx);
  }, [resizable, storedPx]);

  const cancelDrag = useCallback(() => cancelLiveDrag.current?.(), []);
  useEscapeStack(cancelDrag, dragging);

  const startDrag = useCallback((event: ReactPointerEvent<HTMLElement>) => {
    const tileArea = document.querySelector<HTMLElement>('.view-stack');
    if (event.button !== 0 || !tileArea) return;
    const startX = event.clientX;
    const startPx = clampSidebarPx(storedPxRef.current, window.innerWidth);
    let draftPx = startPx;
    const showDraft = () => {
      showSidebarWidth(draftPx);
      if (labelRef.current) labelRef.current.textContent = sidebarWidthLabel(draftPx, window.innerWidth);
    };

    const endGesture = beginPointerResize(event.currentTarget, event.pointerId, 'col-resize');
    tileArea.style.flex = `0 0 ${tileArea.getBoundingClientRect().width}px`;
    useSidebarDrag.setState({ dragging: true });
    showDraft();

    const end = () => {
      window.removeEventListener('pointermove', onMove, true);
      window.removeEventListener('pointerup', onUp, true);
      window.removeEventListener('pointercancel', onCancel, true);
      window.removeEventListener('blur', onCancel);
      cancelLiveDrag.current = null;
      tileArea.style.flex = '';
      useSidebarDrag.setState({ dragging: false });
      endGesture();
    };
    const onMove = (move: PointerEvent) => {
      draftPx = clampSidebarPx(startPx + move.clientX - startX, window.innerWidth);
      showDraft();
    };
    const onCancel = () => {
      end();
      showSidebarWidth(storedPxRef.current);
    };
    const onUp = (up: PointerEvent) => {
      up.preventDefault();
      up.stopPropagation();
      end();
      if (draftPx === startPx || draftPx === storedPxRef.current) showSidebarWidth(storedPxRef.current);
      else sendSetSetting('sidebar_width', String(draftPx));
    };
    cancelLiveDrag.current = onCancel;
    window.addEventListener('pointermove', onMove, true);
    window.addEventListener('pointerup', onUp, true);
    window.addEventListener('pointercancel', onCancel, true);
    window.addEventListener('blur', onCancel);
  }, [sendSetSetting]);

  const clearWidth = useCallback(() => sendSetSetting('sidebar_width', ''), [sendSetSetting]);

  return { resizable, dragging, labelRef, startDrag, cancelDrag, clearWidth };
}

export type SidebarWidth = ReturnType<typeof useSidebarWidth>;
