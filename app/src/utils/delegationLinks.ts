import { crewDisplayName } from './crewName';
import type { SessionDelegationRole } from '../types/generated';

export interface DelegationSession {
  id: string;
  label: string;
  dispatcher_session_id?: string;
  dispatcher_member?: string;
  delegation_role?: SessionDelegationRole;
  endpoint_id?: string;
}

export function hasDelegationChain(session: DelegationSession, hasDelegates = false): boolean {
  return Boolean(session.delegation_role || session.dispatcher_session_id || session.dispatcher_member || hasDelegates);
}

export function delegationTree<TSession extends DelegationSession>(
  sessionId: string,
  sessions: readonly TSession[],
): { rows: Array<{ session: TSession; depth: number }>; earlierDispatcher: string | null } {
  const current = sessions.find((session) => session.id === sessionId);
  if (!current) return { rows: [], earlierDispatcher: null };
  const local = sessions.filter((session) => (session.endpoint_id ?? '') === (current.endpoint_id ?? ''));
  const byId = new Map(local.map((session) => [session.id, session]));
  const children = delegatesByDispatcher(local);
  const ancestors = new Set<string>();
  let root = current;
  while (root.dispatcher_session_id && !ancestors.has(root.dispatcher_session_id)) {
    ancestors.add(root.id);
    const parent = byId.get(root.dispatcher_session_id);
    if (!parent) break;
    root = parent;
  }
  const missingParent = root.dispatcher_session_id && !byId.has(root.dispatcher_session_id);
  const earlierDispatcher = missingParent || (!root.dispatcher_session_id && root.dispatcher_member)
    ? (root.dispatcher_member ? crewDisplayName(root.dispatcher_member) : 'Earlier session')
    : null;
  const roots = missingParent ? children.get(root.dispatcher_session_id!) ?? [root] : [root];
  const pending = roots.map((session) => ({ session, depth: 0 })).reverse();
  const rows: Array<{ session: TSession; depth: number }> = [];
  const visited = new Set<string>();
  while (pending.length > 0) {
    const entry = pending.pop()!;
    if (visited.has(entry.session.id)) continue;
    visited.add(entry.session.id);
    rows.push(entry);
    const delegates = children.get(entry.session.id) ?? [];
    for (let index = delegates.length - 1; index >= 0; index -= 1) {
      pending.push({ session: delegates[index], depth: entry.depth + 1 });
    }
  }
  return { rows, earlierDispatcher };
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
