import { useLayoutEffect, type RefObject } from 'react';

export function useDelegationChainPosition(card: RefObject<HTMLElement | null>, anchor: HTMLElement | null, rowCount: number) {
  useLayoutEffect(() => {
    const position = () => {
      const element = card.current;
      if (!element) return;
      const bounds = element.getBoundingClientRect();
      const rect = anchor?.getBoundingClientRect();
      const sidebar = anchor?.closest('.sidebar') ?? anchor?.closest('.session-item');
      const left = rect ? (sidebar ? sidebar.getBoundingClientRect().right + 4 : rect.left) : (window.innerWidth - bounds.width) / 2;
      const top = rect ? (sidebar ? rect.top : rect.bottom + 4) : (window.innerHeight - bounds.height) / 2;
      element.style.left = `${Math.max(8, Math.min(left, window.innerWidth - bounds.width - 8))}px`;
      element.style.top = `${Math.max(8, Math.min(top, window.innerHeight - bounds.height - 8))}px`;
    };
    position();
    window.addEventListener('resize', position);
    return () => window.removeEventListener('resize', position);
  }, [card, anchor, rowCount]);
}
