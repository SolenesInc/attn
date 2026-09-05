import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';

const HOVER_OPEN_MS = 160;
const HOVER_CLOSE_MS = 240;

export function useAnchoredPopover(pinned: boolean, onPopoverClosed: () => void) {
  const anchorRef = useRef<HTMLButtonElement>(null);
  const [hoverOpen, setHoverOpen] = useState(false);
  const [clickPinned, setClickPinned] = useState(false);
  const [anchor, setAnchor] = useState<{ top: number; right: number } | null>(null);
  const openTimer = useRef<number | undefined>(undefined);
  const closeTimer = useRef<number | undefined>(undefined);

  const popoverPinned = pinned || clickPinned;
  const popoverOpen = popoverPinned || hoverOpen;

  useLayoutEffect(() => {
    if (!popoverOpen) {
      setAnchor(null);
      return;
    }
    const rect = anchorRef.current?.getBoundingClientRect();
    if (rect) setAnchor({ top: rect.bottom + 4, right: rect.right });
  }, [popoverOpen]);

  useEffect(() => () => {
    window.clearTimeout(openTimer.current);
    window.clearTimeout(closeTimer.current);
  }, []);

  const scheduleOpen = useCallback(() => {
    window.clearTimeout(closeTimer.current);
    window.clearTimeout(openTimer.current);
    openTimer.current = window.setTimeout(() => setHoverOpen(true), HOVER_OPEN_MS);
  }, []);

  const openNow = useCallback(() => {
    window.clearTimeout(closeTimer.current);
    window.clearTimeout(openTimer.current);
    setHoverOpen(true);
  }, []);

  const scheduleClose = useCallback(() => {
    window.clearTimeout(openTimer.current);
    window.clearTimeout(closeTimer.current);
    closeTimer.current = window.setTimeout(() => setHoverOpen(false), HOVER_CLOSE_MS);
  }, []);

  const closePopover = useCallback(() => {
    window.clearTimeout(openTimer.current);
    window.clearTimeout(closeTimer.current);
    setHoverOpen(false);
    setClickPinned(false);
    if (pinned) onPopoverClosed();
  }, [pinned, onPopoverClosed]);

  return {
    anchorRef,
    anchor,
    open: popoverOpen,
    pinned: popoverPinned,
    scheduleOpen,
    openNow,
    scheduleClose,
    close: closePopover,
    pin: () => setClickPinned(true),
    cancelClose: () => window.clearTimeout(closeTimer.current),
  };
}
