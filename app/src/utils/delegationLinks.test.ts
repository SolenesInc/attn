import { describe, expect, it } from 'vitest';
import { delegatesByDispatcher, dispatcherOf, type DelegationSession } from './delegationLinks';

const sessions: DelegationSession[] = [
  { id: 'root', label: 'attn next' },
  { id: 'middle', label: 'docs sweep', dispatcher_session_id: 'root' },
  { id: 'leaf-a', label: 'glossary', dispatcher_session_id: 'middle' },
  { id: 'leaf-b', label: 'stale plans', dispatcher_session_id: 'middle' },
];

describe('delegation links', () => {
  it('resolves a live dispatcher and prefers its crew member name', () => {
    expect(dispatcherOf({
      id: 'delegate',
      label: 'delegate',
      dispatcher_session_id: 'root',
      dispatcher_member: 'alder',
    }, sessions)).toEqual({ session: sessions[0], name: 'Alder' });
  });

  it('uses the live dispatcher label when no member is recorded', () => {
    expect(dispatcherOf(sessions[1], sessions)).toEqual({
      session: sessions[0],
      name: 'attn next',
    });
  });

  it('keeps the member name when the dispatching session has ended', () => {
    expect(dispatcherOf({
      id: 'delegate',
      label: 'delegate',
      dispatcher_member: 'alder',
    }, sessions)).toEqual({ session: null, name: 'Alder' });
  });

  it('groups only live delegates by their direct dispatcher', () => {
    const grouped = delegatesByDispatcher(sessions);
    expect(grouped.get('root')?.map((session) => session.id)).toEqual(['middle']);
    expect(grouped.get('middle')?.map((session) => session.id)).toEqual(['leaf-a', 'leaf-b']);
    expect(grouped.has('leaf-a')).toBe(false);
  });
});
