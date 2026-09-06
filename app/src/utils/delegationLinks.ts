import { crewDisplayName } from './crewName';

export interface DelegationSession {
  id: string;
  label: string;
  dispatcher_session_id?: string;
  dispatcher_member?: string;
}

export interface DispatcherLink<T extends DelegationSession> {
  session: T | null;
  name: string;
}

export function dispatcherOf<T extends DelegationSession>(
  session: T,
  sessions: readonly T[],
): DispatcherLink<T> | null {
  const dispatcher = session.dispatcher_session_id
    ? sessions.find((candidate) => candidate.id === session.dispatcher_session_id) ?? null
    : null;
  const name = session.dispatcher_member
    ? crewDisplayName(session.dispatcher_member)
    : dispatcher?.label;
  return name ? { session: dispatcher, name } : null;
}

export function delegatesByDispatcher<T extends DelegationSession>(
  sessions: readonly T[],
): Map<string, T[]> {
  const grouped = new Map<string, T[]>();
  for (const session of sessions) {
    if (!session.dispatcher_session_id) continue;
    const delegates = grouped.get(session.dispatcher_session_id) ?? [];
    delegates.push(session);
    grouped.set(session.dispatcher_session_id, delegates);
  }
  return grouped;
}
