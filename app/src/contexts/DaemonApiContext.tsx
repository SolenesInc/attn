import { createContext, useContext, type ReactNode } from 'react';
import type { useDaemonSocket } from '../hooks/useDaemonSocket';

export type DaemonApi = ReturnType<typeof useDaemonSocket>;

// Deliberately not the same context as DaemonContext: folding in its mute + undo bookkeeping would make every consumer of a send function re-render on an unrelated undo.
const DaemonApiContext = createContext<DaemonApi | null>(null);

export function DaemonApiProvider({ api, children }: { api: DaemonApi; children: ReactNode }) {
  return <DaemonApiContext.Provider value={api}>{children}</DaemonApiContext.Provider>;
}

export function useOptionalDaemonApi(): DaemonApi | null {
  return useContext(DaemonApiContext);
}

export function useDaemonApi(): DaemonApi {
  const api = useOptionalDaemonApi();
  if (!api) {
    throw new Error('useDaemonApi must be used within DaemonApiProvider');
  }
  return api;
}
