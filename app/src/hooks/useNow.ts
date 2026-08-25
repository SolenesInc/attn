import { useEffect, useState } from 'react';

/** A wall clock that re-renders on an interval, so an age shown against a timestamp keeps
 * counting while the daemon is quiet rather than reading from a prop. */
export function useNow(intervalMs: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(timer);
  }, [intervalMs]);
  return now;
}

/** How often turn ages re-read the clock. Shared by the queue band and home. */
export const TURN_AGE_TICK_MS = 30_000;
