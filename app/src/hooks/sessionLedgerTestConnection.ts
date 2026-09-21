import type {
  SessionLedgerConnectionEvent,
  SessionLedgerPage,
  SessionLedgerQuery,
  SessionLedgerUpdate,
} from './daemonSessionLedgerEvents';
import type { SessionLedgerConnection } from './useSessionLedger';

export function createSessionLedgerTestConnection(
  list: (query: SessionLedgerQuery) => Promise<SessionLedgerPage>,
  generation = 1,
) {
  const listeners = new Set<(event: SessionLedgerConnectionEvent) => void>();
  const connection: SessionLedgerConnection = {
    list,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    connected: true,
    generation,
  };
  const emit = (update: SessionLedgerUpdate) => {
    for (const listener of listeners) listener({ ...update, connectionGeneration: connection.generation });
  };
  const setConnected = (connected: boolean, nextGeneration = connection.generation) => {
    connection.connected = connected;
    connection.generation = nextGeneration;
    for (const listener of listeners) {
      listener({ type: 'connection', connected, connectionGeneration: nextGeneration });
    }
  };
  return { connection, emit, setConnected };
}
