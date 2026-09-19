import { describe, expect, it } from 'vitest';
import { delegatesByDispatcher, delegationTree, dispatcherOf } from './delegationLinks';

interface LinkedSession {
  id: string;
  label: string;
  dispatcher_session_id?: string;
  dispatcher_member?: string;
}

describe('delegation links', () => {
  const dispatcher: LinkedSession = { id: 'root', label: 'root session' };
  const delegate: LinkedSession = {
    id: 'child',
    label: 'child',
    dispatcher_session_id: 'root',
    dispatcher_member: 'alder',
  };

  it('uses the member display name and resolves its live session', () => {
    expect(dispatcherOf(delegate, [dispatcher, delegate])).toEqual({
      session: dispatcher,
      name: 'Alder',
    });
  });

  it('falls back to the live dispatcher label outside crew', () => {
    expect(dispatcherOf({ ...delegate, dispatcher_member: undefined }, [dispatcher, delegate])).toEqual({
      session: dispatcher,
      name: 'root session',
    });
  });

  it('keeps the member name when the dispatching session ended', () => {
    expect(dispatcherOf({ ...delegate, dispatcher_session_id: 'ended' }, [delegate])).toEqual({
      session: null,
      name: 'Alder',
    });
  });

  it('groups direct delegates by dispatcher', () => {
    const sibling = { ...delegate, id: 'sibling' };
    const orphan = { ...delegate, id: 'orphan', dispatcher_session_id: 'ended' };
    expect(delegatesByDispatcher([dispatcher, delegate, sibling, orphan])).toEqual(new Map([
      ['root', [delegate, sibling]],
      ['ended', [orphan]],
    ]));
  });

  it('walks the connected tree from its root, including peers and deeper delegates', () => {
    const sessions = [dispatcher, delegate,
      { id: 'grandchild', label: 'grandchild', dispatcher_session_id: 'child' },
      { id: 'peer', label: 'peer', dispatcher_session_id: 'root' },
      { id: 'unrelated', label: 'unrelated' },
    ];
    const tree = delegationTree('grandchild', sessions);
    expect(tree.rows.map(({ session, depth }) => [session.id, depth])).toEqual([
      ['root', 0], ['child', 1], ['grandchild', 2], ['peer', 1],
    ]);
    expect(tree.earlierDispatcher).toBeNull();
  });

  it('shows an unavailable dispatcher without fabricating a clickable session', () => {
    const orphan = { ...delegate, dispatcher_session_id: 'ended' };
    const tree = delegationTree('child', [orphan, { ...orphan, id: 'peer' }]);
    expect(tree.earlierDispatcher).toBe('Alder');
    expect(tree.rows.map(({ session }) => session.id)).toEqual(['child', 'peer']);
  });

  it('does not cross endpoints, revisit cycles, or invent missing sessions', () => {
    expect(delegationTree('missing', [dispatcher])).toEqual({ rows: [], earlierDispatcher: null });
    const cycle = [{ ...dispatcher, dispatcher_session_id: 'child' }, delegate];
    expect(delegationTree('child', cycle).rows.map(({ session }) => session.id).sort()).toEqual(['child', 'root']);
    expect(delegationTree('child', [delegate, { ...dispatcher, endpoint_id: 'remote' }]).rows.map(({ session }) => session.id)).toEqual(['child']);
  });
});
