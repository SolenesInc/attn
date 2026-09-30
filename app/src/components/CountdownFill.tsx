import { useLayoutEffect, useRef } from 'react';

/** A one-shot bar animated against an absolute deadline with a CSS transition, so no
 * per-tick re-render. 'fill' grows 0% -> 100%; 'drain' shrinks 100% -> 0%. */
export function CountdownFill({
  firesAt,
  className,
  direction = 'fill',
}: {
  firesAt: string;
  className: string;
  direction?: 'fill' | 'drain';
}) {
  const ref = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const from = direction === 'drain' ? 'scaleX(1)' : 'scaleX(0)';
    const to = direction === 'drain' ? 'scaleX(0)' : 'scaleX(1)';
    el.style.transformOrigin = direction === 'drain' ? 'right' : 'left';
    const remainingMs = new Date(firesAt).getTime() - Date.now();
    if (!Number.isFinite(remainingMs) || remainingMs <= 0) {
      el.style.transition = 'none';
      el.style.transform = to;
      return;
    }
    el.style.transition = 'none';
    el.style.transform = from;
    // Flush the starting style before transitioning to the deadline.
    void el.offsetWidth;
    el.style.transition = `transform ${remainingMs}ms linear`;
    el.style.transform = to;
  }, [firesAt, direction]);
  return <div ref={ref} className={className} />;
}
