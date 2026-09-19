import type { CrewCharterDocument, CrewHandoffDocument, CrewMember, CrewRestart } from '../types/generated';
import { pendingRequestKey, settlePendingRequest, type PendingRequests } from './daemonPendingRequests';

export interface CrewMutationOutcome {
  success: boolean;
  conflict: boolean;
  error?: string;
  member?: CrewMember;
  restart?: CrewRestart;
}

interface CrewMutationEvent {
  event?: string;
  request_id?: unknown;
  success?: boolean;
  conflict?: boolean;
  error?: string;
  member?: CrewMember;
  restart?: CrewRestart;
  charter?: CrewCharterDocument;
  handoffs?: CrewHandoffDocument[];
}

export interface CrewCharterGetOutcome {
  member: string;
  charter: CrewCharterDocument;
}

export interface CrewCharterSetOutcome {
  member: string;
  charter: CrewCharterDocument;
  conflict: boolean;
}

export interface CrewHandoffsGetOutcome {
  member: string;
  handoffs: CrewHandoffDocument[];
}

function settleCrewMutation(
  pending: PendingRequests,
  kind: 'crew_set' | 'crew_restart',
  event: CrewMutationEvent,
): void {
  if (typeof event.request_id !== 'string') return;
  const key = pendingRequestKey(kind, event.request_id);
  const waiter = pending.get(key);
  if (!waiter) return;
  pending.delete(key);
  waiter.resolve({
    success: event.success === true,
    conflict: event.conflict === true,
    ...(event.error ? { error: event.error } : {}),
    ...(event.member ? { member: event.member } : {}),
    ...(event.restart ? { restart: event.restart } : {}),
  } satisfies CrewMutationOutcome);
}

export function handleCrewDaemonEvent(event: CrewMutationEvent, pending: PendingRequests): boolean {
  if (event.event === 'crew_charter_get_result') {
    settlePendingRequest(pending, 'crew_charter_get', event, (result) => (
      result.member && result.charter ? { member: result.member, charter: result.charter } : undefined
    ), 'The daemon returned no charter');
    return true;
  }
  if (event.event === 'crew_charter_set_result') {
    settlePendingRequest(pending, 'crew_charter_set', event, (result) => (
      result.member && result.charter
        ? { member: result.member, charter: result.charter, conflict: result.conflict === true }
        : undefined
    ), 'The daemon returned no saved charter');
    return true;
  }
  if (event.event === 'crew_handoffs_get_result') {
    settlePendingRequest(pending, 'crew_handoffs_get', event, (result) => (
      result.member && result.handoffs
        ? { member: result.member, handoffs: result.handoffs }
        : undefined
    ), 'The daemon returned no handoff history');
    return true;
  }
  if (event.event === 'crew_set_result') {
    settleCrewMutation(pending, 'crew_set', event);
    return true;
  }
  if (event.event === 'crew_restart_result') {
    settleCrewMutation(pending, 'crew_restart', event);
    return true;
  }
  return false;
}
