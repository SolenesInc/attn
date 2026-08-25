import { beforeEach, describe, expect, it } from 'vitest';
import { useConversationsStore } from './conversations';


const SESSION = 'sess-1';

function apply(kind: string, body: Record<string, unknown>, seq: number) {
  useConversationsStore.getState().applyEnvelope(SESSION, seq, kind, body);
}

function conversation() {
  return useConversationsStore.getState().conversations[SESSION];
}

const snapshotBody = (items: unknown[], extra: Record<string, unknown> = {}) => ({
  items,
  total: items.length,
  truncated: false,
  running: false,
  queue: { steering: [], followUp: [] },
  ...extra,
});

describe('conversations store: revive', () => {
  beforeEach(() => {
    useConversationsStore.setState({ conversations: {} });
  });

  it('draws a transcript from a snapshot', () => {
    apply('session_ready', {}, 1);
    apply('conversation_snapshot', snapshotBody([
      { kind: 'message', id: 'h:1', role: 'user', text: 'read the file', streaming: false },
      {
        kind: 'tool',
        call_id: 'c1',
        name: 'read',
        summary: 'main.go',
        files: ['main.go'],
        status: 'ok',
        detail: true,
        patch: false,
        truncated: true,
        full_output: true,
      },
      { kind: 'message', id: 'h:2', role: 'assistant', text: 'done', streaming: false },
    ]), 2);

    expect(conversation().items).toEqual([
      { kind: 'message', id: 'h:1', role: 'user', text: 'read the file', streaming: false },
      {
        kind: 'tool',
        callId: 'c1',
        name: 'read',
        summary: 'main.go',
        files: ['main.go'],
        status: 'ok',
        error: undefined,
        hasDetail: true,
        hasPatch: false,
        truncated: true,
        fullOutput: true,
      },
      { kind: 'message', id: 'h:2', role: 'assistant', text: 'done', streaming: false },
    ]);
    expect(conversation().ready).toBe(true);
  });

  it('replaces the transcript rather than appending to it', () => {
    apply('session_ready', {}, 1);
    apply('message_end', { id: 'stale', role: 'assistant', text: 'from before' }, 2);
    apply('conversation_snapshot', snapshotBody([
      { kind: 'message', id: 'fresh', role: 'assistant', text: 'the truth', streaming: false },
    ]), 3);

    expect(conversation().items).toEqual([
      { kind: 'message', id: 'fresh', role: 'assistant', text: 'the truth', streaming: false },
    ]);
  });

  it('carries the open run and the unread queue', () => {
    apply('session_ready', {}, 1);
    apply('conversation_snapshot', snapshotBody(
      [{ kind: 'message', id: 'm1', role: 'assistant', text: 'working', streaming: true }],
      { running: true, queue: { steering: ['hurry up'], followUp: ['then this'] } },
    ), 2);

    expect(conversation().running).toBe(true);
    expect(conversation().queue).toEqual({ steering: ['hurry up'], followUp: ['then this'] });
  });

  it('drops a snapshot item shape it does not draw', () => {
    apply('session_ready', {}, 1);
    apply('conversation_snapshot', snapshotBody([
      { kind: 'reasoning', id: 'r1', text: 'thinking' },
      { kind: 'message', id: 'm1', role: 'assistant', text: 'hi', streaming: false },
    ]), 2);

    expect(conversation().items).toEqual([
      { kind: 'message', id: 'm1', role: 'assistant', text: 'hi', streaming: false },
    ]);
  });

  it('resets the envelope spine when a new host announces itself', () => {
    apply('session_ready', {}, 1);
    apply('message_end', { id: 'm1', role: 'assistant', text: 'before the crash' }, 40);
    expect(conversation().lastSeq).toBe(40);

    apply('session_ready', {}, 1);
    expect(conversation().lastSeq).toBe(1);

    apply('conversation_snapshot', snapshotBody([
      { kind: 'message', id: 'h:1', role: 'assistant', text: 'before the crash', streaming: false },
    ]), 2);
    apply('message_end', { id: 'm2', role: 'assistant', text: 'after it' }, 3);

    expect(conversation().items.map((item) => (item.kind === 'message' ? item.text : ''))).toEqual([
      'before the crash',
      'after it',
    ]);
  });

  it('still drops a duplicate on the new spine', () => {
    apply('session_ready', {}, 1);
    apply('message_end', { id: 'm1', role: 'assistant', text: 'one' }, 5);
    apply('message_end', { id: 'm2', role: 'assistant', text: 'two' }, 5);

    expect(conversation().items).toHaveLength(1);
  });

  it('closes a run the dead host never settled', () => {
    apply('session_ready', {}, 1);
    apply('run_started', {}, 2);
    expect(conversation().running).toBe(true);

    apply('session_ready', {}, 1);
    expect(conversation().running).toBe(false);
    expect(conversation().awaitingRun).toBe(false);
  });

  it('stops claiming a host when the session exits, keeping the transcript', () => {
    apply('session_ready', {}, 1);
    apply('run_started', {}, 2);
    apply('message_end', { id: 'm1', role: 'assistant', text: 'said something' }, 3);

    useConversationsStore.getState().hostExited(SESSION);

    expect(conversation().ready).toBe(false);
    expect(conversation().running).toBe(false);
    expect(conversation().items).toHaveLength(1);
  });

  it('leaves a session it has never seen alone on exit', () => {
    useConversationsStore.getState().hostExited('never-seen');
    expect(useConversationsStore.getState().conversations['never-seen']).toBeUndefined();
  });
});
