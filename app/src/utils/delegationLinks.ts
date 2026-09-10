import { crewDisplayName } from './crewName';

export interface DelegationSession {
  id: string;
  label: string;
  dispatcher_session_id?: string;
  dispatcher_member?: string;
}

export interface DispatcherLink<TSession extends DelegationSession> {
  session: TSession | null;
  name: string;
}

export function dispatcherOf<TSession extends DelegationSession>(
  session: TSession,
  sessions: readonly TSession[],
): DispatcherLink<TSession> | null {
  const dispatcher = session.dispatcher_session_id
    ? sessions.find((candidate) => candidate.id === session.dispatcher_session_id) ?? null
    : null;
  const member = session.dispatcher_member?.trim();
  const name = member ? crewDisplayName(member) : dispatcher?.label.trim();
  return name ? { session: dispatcher, name } : null;
}

export function delegatesByDispatcher<TSession extends DelegationSession>(
  sessions: readonly TSession[],
): Map<string, TSession[]> {
  const delegates = new Map<string, TSession[]>();
  for (const session of sessions) {
    const dispatcherId = session.dispatcher_session_id;
    if (!dispatcherId) continue;
    const siblings = delegates.get(dispatcherId);
    if (siblings) {
      siblings.push(session);
    } else {
      delegates.set(dispatcherId, [session]);
    }
  }
  return delegates;
}
