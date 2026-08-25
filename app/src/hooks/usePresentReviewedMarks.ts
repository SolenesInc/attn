
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

function storageKey(presentationId: string, roundId: string): string {
  return `attn.present.reviewed.${presentationId}.${roundId}`;
}

function readMarks(key: string): Set<string> {
  try {
    const raw = window.localStorage.getItem(key);
    if (!raw) return new Set();
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return new Set();
    return new Set(parsed.filter((p): p is string => typeof p === 'string'));
  } catch (err) {
    console.warn('[usePresentReviewedMarks] Failed to read marks:', err);
    return new Set();
  }
}

function writeMarks(key: string, marks: Set<string>): void {
  try {
    window.localStorage.setItem(key, JSON.stringify(Array.from(marks)));
  } catch (err) {
    console.warn('[usePresentReviewedMarks] Failed to persist marks:', err);
  }
}

export interface PresentReviewedMarksControls {
  reviewed: ReadonlySet<string>;
  toggleReviewed(path: string): void;
  markReviewed(path: string): void;
}

export function usePresentReviewedMarks(
  presentationId: string | null,
  roundId: string | null,
  filePaths: string[]
): PresentReviewedMarksControls {
  const key = presentationId && roundId ? storageKey(presentationId, roundId) : null;
  const [reviewed, setReviewed] = useState<Set<string>>(new Set());

  const filePathsKey = filePaths.join('\0');
  const filePathsRef = useRef(filePaths);
  filePathsRef.current = filePaths;

  useEffect(() => {
    if (!key) {
      setReviewed(new Set());
      return;
    }
    const loaded = readMarks(key);
    const validPaths = new Set(filePathsRef.current);
    const pruned = new Set(Array.from(loaded).filter((p) => validPaths.has(p)));
    if (pruned.size !== loaded.size) writeMarks(key, pruned);
    setReviewed(pruned);
    // filePathsKey (not filePaths) is the real dependency: filePaths is a fresh array
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, filePathsKey]);

  const toggleReviewed = useCallback(
    (path: string) => {
      if (!key) return;
      setReviewed((current) => {
        const next = new Set(current);
        if (next.has(path)) next.delete(path);
        else next.add(path);
        writeMarks(key, next);
        return next;
      });
    },
    [key]
  );

  const markReviewed = useCallback(
    (path: string) => {
      if (!key) return;
      setReviewed((current) => {
        if (current.has(path)) return current;
        const next = new Set(current);
        next.add(path);
        writeMarks(key, next);
        return next;
      });
    },
    [key]
  );

  return useMemo(() => ({ reviewed, toggleReviewed, markReviewed }), [reviewed, toggleReviewed, markReviewed]);
}
