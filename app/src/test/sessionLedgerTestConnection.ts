import type {
  SessionLedgerConnectionEvent,
  SessionLedgerPage,
  SessionLedgerQuery,
  SessionLedgerUpdate,
} from '../hooks/daemonSessionLedgerEvents';
import type { SessionLedgerConnection } from '../hooks/useSessionLedger';

export function createSessionLedgerTestConnection(
  list: (query: SessionLedgerQuery) => Promise<SessionLedgerPage>,
) {
  const listeners = new Set<(event: SessionLedgerConnectionEvent) => void>();
  let connected = true;
  let generation = 1;
  const connection: SessionLedgerConnection = {
    list,
    subscribe(listener) {
      listeners.add(listener);
      listener({ type: 'connection', connected, connectionGeneration: generation });
      return () => listeners.delete(listener);
    },
  };
  const emit = (update: SessionLedgerUpdate) => {
    for (const listener of listeners) listener({ ...update, connectionGeneration: generation });
  };
  const setConnected = (nextConnected: boolean, nextGeneration = generation) => {
    connected = nextConnected;
    generation = nextGeneration;
    for (const listener of listeners) {
      listener({ type: 'connection', connected, connectionGeneration: generation });
    }
  };
  return { connection, emit, setConnected };
}
