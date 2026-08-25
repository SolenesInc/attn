
import { useCallback, useState } from 'react';

export interface PanelAction {
  error: string | null;
  /** Refuse before doing anything — a required field left blank. */
  fail: (message: string) => void;
  clearError: () => void;
  /** Which row is mid-flight: an endpoint or plugin id, or null. */
  busyKey: string | null;
  busy: boolean;
  run: (key: string, fallbackMessage: string, action: () => Promise<void>) => Promise<void>;
}

export function usePanelAction(): PanelAction {
  const [error, setError] = useState<string | null>(null);
  const [busyKey, setBusyKey] = useState<string | null>(null);

  const fail = useCallback((message: string) => setError(message), []);
  const clearError = useCallback(() => setError(null), []);

  const run = useCallback(async (
    key: string,
    fallbackMessage: string,
    action: () => Promise<void>,
  ) => {
    setError(null);
    setBusyKey(key);
    try {
      await action();
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : fallbackMessage);
    } finally {
      setBusyKey(null);
    }
  }, []);

  return { error, fail, clearError, busyKey, busy: busyKey !== null, run };
}
