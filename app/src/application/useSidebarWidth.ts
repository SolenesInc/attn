import { useCallback, useEffect, useLayoutEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react';
import { WIDTH_LABEL_MS } from '../components/SessionTerminalDesktop/useShelf';
import { clampSidebarPx, renderedSidebarWidth, sidebarWidthLabel, stepSidebarWidth, storedSidebarPx } from '../components/sidebarWidth';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { useShortcut } from '../shortcuts/useShortcut';
import { useSidebarDrag } from '../store/sidebarDrag';
import { beginPointerResize } from '../utils/resizeGesture';
import { useAppErrorsContext, useAppInputs } from './AppContexts';
import { BoolSetting, isOn } from '../utils/settings';

const WIDTH_VARIABLE = '--sidebar-width';

function showSidebarWidth(px: number) {
  document.documentElement.style.setProperty(WIDTH_VARIABLE, renderedSidebarWidth(px));
}

export function useSidebarWidth({ sidebarShown }: { sidebarShown: boolean }) {
  const { settings } = useAppInputs();
  const { sendSetSetting } = useDaemonApi();
  const { showError } = useAppErrorsContext();
  const resizable = isOn(settings, BoolSetting.shelfLayoutEnabled);
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

  const [stepLabelShown, setStepLabelShown] = useState(false);
  const stepLabelTimer = useRef<number | null>(null);
  useEffect(() => () => {
    if (stepLabelTimer.current !== null) window.clearTimeout(stepLabelTimer.current);
  }, []);

  const step = (direction: 'wider' | 'narrower') => {
    if (!sidebarShown) {
      showError('The sidebar is hidden. Show it to change its width.');
      return;
    }
    const result = stepSidebarWidth(storedPxRef.current, direction, window.innerWidth);
    if (result.kind === 'limit') {
      showError(result.message);
      return;
    }
    sendSetSetting('sidebar_width', String(result.px));
    if (labelRef.current) labelRef.current.textContent = sidebarWidthLabel(result.px, window.innerWidth);
    setStepLabelShown(true);
    if (stepLabelTimer.current !== null) window.clearTimeout(stepLabelTimer.current);
    stepLabelTimer.current = window.setTimeout(() => setStepLabelShown(false), WIDTH_LABEL_MS);
  };
  useShortcut('sidebar.wider', () => step('wider'), resizable);
  useShortcut('sidebar.narrower', () => step('narrower'), resizable);

  return { resizable, dragging, stepLabelShown, labelRef, startDrag, cancelDrag, clearWidth };
}

export type SidebarWidth = ReturnType<typeof useSidebarWidth>;
