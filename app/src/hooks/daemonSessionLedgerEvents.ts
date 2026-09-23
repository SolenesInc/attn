import type {
  SessionLedgerEntry,
  SessionLedgerFacets,
  SessionReopen,
  SessionReopenResult,
} from '../types/generated';
import { reopenVerdictView } from '../components/sessionsLedger';
import type { ReopenVerdictView } from '../components/sessionsLedger';
import type { PendingRequests } from './daemonPendingRequests';
import { settlePendingRequest } from './daemonPendingRequests';

/** The generator flattens the result embedded in a response to anonymous `Entry`
 * and `Facets` names, so the page is restated here against the standalone models. */
export interface SessionLedgerPage {
  entries: SessionLedgerEntry[];
  facets?: SessionLedgerFacets;
  next_before?: string;
  omitted: number;
}

export interface SessionLedgerQuery {
  closed?: boolean;
  all?: boolean;
  limit?: number;
  before?: string;
  workspace_id?: string;
  repository?: string;
  since?: string;
  until?: string;
}

export class SessionReopenRefusal extends Error {
  readonly verdict: ReopenVerdictView;

  constructor(message: string, reopen: SessionReopen) {
    super(message);
    this.name = 'SessionReopenRefusal';
    this.verdict = reopenVerdictView(reopen);
  }
}

export type SessionLedgerUpdate = { type: 'closed'; entry: SessionLedgerEntry };

export type SessionLedgerConnectionEvent =
  | { type: 'connection'; connected: boolean; connectionGeneration: number }
  | (SessionLedgerUpdate & { connectionGeneration: number });

export interface SessionLedgerEventContext {
  pending: PendingRequests;
  onUpdate?: (update: SessionLedgerUpdate) => void;
}

type SessionLedgerEvent = {
  event: string;
  request_id?: unknown;
  success?: boolean;
  error?: string;
  result?: unknown;
  entry?: unknown;
  session_ledger_entry?: unknown;
  reopen?: SessionReopen;
};

export function handleSessionLedgerDaemonEvent(
  event: SessionLedgerEvent,
  context: SessionLedgerEventContext,
): boolean {
  switch (event.event) {
    case 'session_list_result':
      settlePendingRequest(
        context.pending,
        'session_list',
        event,
        (value) => value.result as SessionLedgerPage | undefined,
        'Reading the session ledger failed',
      );
      return true;
    case 'session_show_result':
      settlePendingRequest(
        context.pending,
        'session_show',
        event,
        (value) => value.entry as SessionLedgerEntry | undefined,
        'Reading that session failed',
      );
      return true;
    case 'session_reopen_result':
      settlePendingRequest(
        context.pending,
        'session_reopen',
        event,
        (value) => value.result as SessionReopenResult | undefined,
        'Reopening that session failed',
        event.reopen ? new SessionReopenRefusal(event.error || 'Reopening that session was refused', event.reopen) : undefined,
      );
      return true;
    case 'session_closed': {
      const entry = event.session_ledger_entry as SessionLedgerEntry | undefined;
      if (entry) context.onUpdate?.({ type: 'closed', entry });
      return true;
    }
    default:
      return false;
  }
}
