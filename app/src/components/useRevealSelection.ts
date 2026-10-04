import { useLayoutEffect, useRef, type MouseEvent, type RefObject } from 'react';

export function useRevealSelection(
  listRef: RefObject<HTMLElement | null>,
  selectionKey: string | null,
  layoutKey: string,
) {
  const clickedKey = useRef<string | null>(null);

  useLayoutEffect(() => {
    const clicked = clickedKey.current;
    clickedKey.current = null;
    if (clicked && selectionKey?.startsWith(clicked)) return;
    revealSelection(listRef.current, false);
  }, [listRef, selectionKey]);

  useLayoutEffect(() => {
    const list = listRef.current;
    if (!list) return;
    const revealHiddenSelection = () => revealSelection(list, true);
    revealHiddenSelection();
    const observer = new ResizeObserver(revealHiddenSelection);
    observer.observe(list);
    return () => observer.disconnect();
  }, [listRef, layoutKey]);

  return {
    onClickCapture(event: MouseEvent) {
      clickedKey.current = null;
      if (event.detail === 0 || !(event.target instanceof Element)) return;
      const control = event.target.closest('[data-select-key]');
      const key = control?.getAttribute('data-select-key');
      if (key && !selectionKey?.startsWith(key)) clickedKey.current = key;
    },
  };
}

function revealSelection(list: HTMLElement | null, onlyWhenHidden: boolean) {
  const row = list?.querySelector<HTMLElement>('[aria-current="true"]');
  if (!list || !row) return;
  const listBox = list.getBoundingClientRect();
  const rowBox = row.getBoundingClientRect();
  const viewportTop = listBox.top + list.clientTop;
  if (onlyWhenHidden && rowBox.top >= viewportTop && rowBox.bottom <= viewportTop + list.clientHeight) return;
  const top = list.scrollTop + rowBox.top - viewportTop + rowBox.height / 2 - list.clientHeight / 2;
  list.scrollTo({
    top,
    behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth',
  });
}
