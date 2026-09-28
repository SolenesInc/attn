import { useLayoutEffect, useState, type RefObject } from 'react';

export function useWaitingFit(
  bodyRef: RefObject<HTMLDivElement | null>,
  leadRef: RefObject<HTMLDivElement | null>,
  waiting: number,
  frozen: boolean,
): number {
  const minimum = Math.min(3, waiting);
  const [count, setCount] = useState(minimum);

  useLayoutEffect(() => {
    if (frozen) return;
    const body = bodyRef.current;
    const lead = leadRef.current;
    if (!body || !lead || waiting === 0) {
      setCount(minimum);
      return;
    }

    const measure = () => {
      const row = lead.querySelector<HTMLElement>('.queue-row');
      const rowHeight = row?.offsetHeight ?? 0;
      if (body.clientHeight === 0 || rowHeight === 0) {
        setCount(minimum);
        return;
      }
      const leadGap = Number.parseFloat(getComputedStyle(lead).rowGap) || 0;
      const bodyStyle = getComputedStyle(body);
      let fixed = (Number.parseFloat(bodyStyle.paddingTop) || 0) + (Number.parseFloat(bodyStyle.paddingBottom) || 0);
      for (const child of body.children) {
        const element = child as HTMLElement;
        const style = getComputedStyle(element);
        fixed += element.offsetHeight + (Number.parseFloat(style.marginTop) || 0) + (Number.parseFloat(style.marginBottom) || 0);
      }
      fixed -= lead.offsetHeight;
      const fit = Math.floor((body.clientHeight - fixed + leadGap) / (rowHeight + leadGap));
      setCount(Math.max(minimum, Math.min(waiting, fit)));
    };

    measure();
    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(measure);
    observer.observe(body);
    for (const child of body.children) {
      observer.observe(child);
    }
    return () => observer.disconnect();
  }, [bodyRef, leadRef, waiting, frozen, minimum]);

  return Math.min(waiting, Math.max(minimum, count));
}
