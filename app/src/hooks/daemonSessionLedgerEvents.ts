import type {
  SessionLedgerEntry,
  SessionLedgerFacets,
  SessionReopen,
  SessionReopenEntry,
  SessionReopenResult,
} from '../types/generated';
import type { PendingRequests } from './daemonPendingRequests';
import { settlePendingRequest } from './daemonPendingRequests';

/** The generator flattens the result embedded in a response to anonymous `Entry`
 * and `Facets` names, so the page is restated here against the standalone models. */
export interface SessionLedgerPage {
  entries: SessionLedgerEntry[];
  facets?: SessionLedgerFacets;
  next_before?: string;
  omitted: number;
  reopen?: SessionReopenEntry[];
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
  reopen?: boolean;
  reopen_delivery?: 'inline' | 'stream';
}

export interface SessionReopenResolutionEvent {
  sessionId: string;
  closedAt: string;
  success: boolean;
  reopen?: SessionReopen;
  error?: string;
}

export type SessionLedgerUpdate =
  | { type: 'closed'; entry: SessionLedgerEntry }
  | { type: 'reopen-resolved'; resolution: SessionReopenResolutionEvent };

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
  session_id?: unknown;
  closed_at?: unknown;
  reopen?: unknown;
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
      );
      return true;
    case 'session_reopen_resolved': {
      const sessionId = typeof event.session_id === 'string' ? event.session_id : '';
      const closedAt = typeof event.closed_at === 'string' ? event.closed_at : '';
      const reopen = event.reopen as SessionReopen | undefined;
      const error = typeof event.error === 'string' ? event.error : undefined;
      if (sessionId && closedAt && ((event.success === true && reopen) || (event.success === false && error))) {
        context.onUpdate?.({
          type: 'reopen-resolved',
          resolution: {
            sessionId,
            closedAt,
            success: event.success === true,
            ...(reopen ? { reopen } : {}),
            ...(error ? { error } : {}),
          },
        });
      }
      return true;
    }
    case 'session_closed': {
      const entry = event.session_ledger_entry as SessionLedgerEntry | undefined;
      if (entry) context.onUpdate?.({ type: 'closed', entry });
      return true;
    }
    default:
      return false;
  }
}
