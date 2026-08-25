
import { useCallback, useEffect, useRef, useState } from 'react';

const SAVED_FLASH_MS = 1600;

export interface SavedFlash {
  flash: (key: string) => void;
  saved: (key: string) => boolean;
}

export function useSavedFlash(): SavedFlash {
  const [marks, setMarks] = useState<Record<string, number>>({});
  const timers = useRef(new Map<string, ReturnType<typeof setTimeout>>());

  useEffect(() => () => {
    for (const timer of timers.current.values()) clearTimeout(timer);
    timers.current.clear();
  }, []);

  const flash = useCallback((key: string) => {
    const existing = timers.current.get(key);
    if (existing) clearTimeout(existing);
    setMarks((prev) => ({ ...prev, [key]: (prev[key] ?? 0) + 1 }));
    timers.current.set(key, setTimeout(() => {
      timers.current.delete(key);
      setMarks((prev) => {
        if (!(key in prev)) return prev;
        const next = { ...prev };
        delete next[key];
        return next;
      });
    }, SAVED_FLASH_MS));
  }, []);

  const saved = useCallback((key: string) => key in marks, [marks]);

  return { flash, saved };
}

export function SavedMark({ shown, testID }: { shown: boolean; testID?: string }) {
  if (!shown) return null;
  return (
    <span className="settings-saved-mark" data-testid={testID} role="status">
      Saved
    </span>
  );
}
