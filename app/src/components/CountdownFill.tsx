import { useEffect, useRef } from 'react';

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
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const from = direction === 'drain' ? '100%' : '0%';
    const to = direction === 'drain' ? '0%' : '100%';
    const remainingMs = new Date(firesAt).getTime() - Date.now();
    if (!Number.isFinite(remainingMs) || remainingMs <= 0) {
      el.style.transition = 'none';
      el.style.width = to;
      return;
    }
    el.style.transition = 'none';
    el.style.width = from;
    // Force a reflow so the width change below actually animates from `from`.
    void el.offsetWidth;
    el.style.transition = `width ${remainingMs}ms linear`;
    el.style.width = to;
  }, [firesAt, direction]);
  return <div ref={ref} className={className} />;
}
