import { describe, expect, it } from 'vitest';
import { delegatesByDispatcher, dispatcherOf } from './delegationLinks';

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
});
