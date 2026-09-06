import type { CrewMember, CrewRestart } from '../types/generated';
import { pendingRequestKey, type PendingRequests } from './daemonPendingRequests';

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
