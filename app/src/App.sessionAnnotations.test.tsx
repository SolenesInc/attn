import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import type { EventMessage } from './test/protocol';
import { renderApp } from './test/renderApp';

type StoredAnnotation = NonNullable<EventMessage<'session_annotations_get_result'>['annotations']>[number];

const PARSER: StoredAnnotation = {
  id: 'anno-1',
  message_key: 'turn-1',
  start: 4,
  end: 10,
  quote: 'parser',
  emoji: '❓',
  comment: 'why this?',
};

const RETRY: StoredAnnotation = {
  id: 'anno-2',
  message_key: 'turn-1',
  start: 30,
  end: 35,
  quote: 'retry',
  quick_label_id: 'clarify-this',
  comment: '',
};

async function openAnnotatedSession(stored: { annotations: StoredAnnotation[]; note?: string; generation: number }) {
  const view = await renderApp({
    initialState: {
      sessions: [daemonSession('s1', { state: 'idle' }), daemonSession('s2', { state: 'idle' })],
      workspaces: [agentWorkspace('s1'), agentWorkspace('s2')],
    },
  });
  view.daemon.on('session_messages_get', ({ session_id }) => ({
    event: 'session_messages_get_result',
    session_id,
    success: true,
    status: 'ready',
    messages: [{ key: 'turn-1', markdown: 'The parser already handles CRLF, so the retry wrapper is safe.' }],
    truncated: false,
  }));
  view.daemon.on('session_annotations_get', ({ session_id }) => ({
    event: 'session_annotations_get_result',
    session_id,
    success: true,
    ...stored,
  }));
  fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
  await view.daemon.idle();
  return view;
}

const AGENT_TEXT = 'The parser already handles CRLF, so the retry wrapper is safe.';

type MessageWindow = Omit<EventMessage<'session_messages_get_result'>, 'event' | 'session_id' | 'request_id'>;

async function openTerminalWithWindow(messageWindow: MessageWindow) {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    return this.tagName === 'CANVAS' ? new DOMRect(0, 0, 800, 600) : new DOMRect(0, 0, 0, 0);
  });
  const view = await renderApp({
    initialState: { sessions: [daemonSession('s1', { state: 'idle' })], workspaces: [agentWorkspace('s1')] },
  });
  view.daemon.on('session_messages_get', ({ session_id }) => ({ event: 'session_messages_get_result', session_id, ...messageWindow }));
  view.daemon.on('session_annotations_get', ({ session_id }) => ({
    event: 'session_annotations_get_result',
    session_id,
    success: true,
    annotations: [],
    generation: 0,
  }));
  view.daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true }));
  fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
  await view.daemon.idle();
  view.daemon.emit({ event: 'pty_output', id: 's1', seq: 1, data: btoa(AGENT_TEXT) });
  await view.daemon.idle();
  return view;
}

async function altDragAcrossFirstRow(daemon: Awaited<ReturnType<typeof renderApp>>['daemon'], toX: number) {
  fireEvent.mouseDown(document.querySelector('[data-pane-id="pane-s1"] canvas')!, { button: 0, altKey: true, clientX: 1, clientY: 2 });
  fireEvent.mouseMove(document, { buttons: 1, altKey: true, clientX: toX, clientY: 2 });
  fireEvent.mouseUp(document, { button: 0, altKey: true, clientX: toX, clientY: 2 });
  await daemon.idle();
}

const panel = () => within(screen.getByTestId('annotation-panel'));
const cards = () => Array.from(document.querySelectorAll('.anno-card-open')).map((card) => card.textContent);

describe('App session annotations', () => {
  it('shows the marks and note stored for a session, reading an old emoji-only mark as its quick label', async () => {
    await openAnnotatedSession({ annotations: [PARSER], note: 'Split this into two PRs.', generation: 7 });

    expect(cards()).toEqual(['❓parserwhy this?']);
    expect(panel().getByLabelText('Note sent with these annotations')).toHaveValue('Split this into two PRs.');
  });

  it('saves the whole remaining list with its note under the next generation when the user removes a mark', async () => {
    const { daemon } = await openAnnotatedSession({ annotations: [PARSER, RETRY], note: 'and land it', generation: 7 });
    daemon.on('session_annotations_save', ({ session_id, generation }) => ({
      event: 'session_annotations_save_result',
      session_id,
      success: true,
      generation,
    }));

    fireEvent.click(panel().getAllByRole('button', { name: 'Remove annotation' })[0]);
    await daemon.idle();

    expect(daemon.sentOf('session_annotations_save')).toEqual([expect.objectContaining({
      session_id: 's1',
      note: 'and land it',
      generation: 8,
      annotations: [expect.objectContaining({
        id: 'anno-2',
        message_key: 'turn-1',
        start: 30,
        end: 35,
        quote: 'retry',
        quick_label_id: 'clarify-this',
      })],
    })]);
    expect(daemon.sentOf('session_annotations_get')).toHaveLength(1);
    expect(cards()).toEqual(['❓retry']);
  });

  it('reads the marks again when the daemon says a save was stale', async () => {
    const { daemon } = await openAnnotatedSession({ annotations: [PARSER, RETRY], generation: 7 });
    daemon.on('session_annotations_save', ({ session_id }) => ({
      event: 'session_annotations_save_result',
      session_id,
      success: false,
      stale: true,
      generation: 9,
    }));
    daemon.on('session_annotations_get', ({ session_id }) => ({
      event: 'session_annotations_get_result',
      session_id,
      success: true,
      annotations: [PARSER],
      note: 'written elsewhere',
      generation: 9,
    }));

    fireEvent.click(panel().getAllByRole('button', { name: 'Remove annotation' })[0]);
    await daemon.idle();

    expect(daemon.sentOf('session_annotations_get')).toHaveLength(2);
    expect(cards()).toEqual(['❓parserwhy this?']);
    expect(panel().getByLabelText('Note sent with these annotations')).toHaveValue('written elsewhere');
  });

  it('keeps the user’s marks without re-reading when a save fails for any other reason', async () => {
    const { daemon } = await openAnnotatedSession({ annotations: [PARSER, RETRY], generation: 7 });
    daemon.on('session_annotations_save', ({ session_id }) => ({
      event: 'session_annotations_save_result',
      session_id,
      success: false,
      error: 'database is locked',
      generation: 0,
    }));

    fireEvent.click(panel().getAllByRole('button', { name: 'Remove annotation' })[0]);
    await daemon.idle();

    expect(daemon.sentOf('session_annotations_get')).toHaveLength(1);
    expect(cards()).toEqual(['❓retry']);
  });

  it('sends every mark to the session and clears the stored ones once delivered', async () => {
    const { daemon } = await openAnnotatedSession({ annotations: [PARSER], generation: 7 });
    daemon.on('session_annotations_submit', ({ session_id }) => ({
      event: 'session_annotations_submit_result',
      session_id,
      success: true,
      status: 'delivered',
    }));
    daemon.on('session_annotations_clear', ({ session_id, generation }) => ({
      event: 'session_annotations_clear_result',
      session_id,
      success: true,
      generation,
    }));

    fireEvent.click(panel().getByRole('button', { name: /Send all/ }));
    await daemon.idle();

    const [submit] = daemon.sentOf('session_annotations_submit');
    expect(submit).toMatchObject({ session_id: 's1', text: expect.stringContaining('parser') });
    expect(submit.text).toContain('why this?');
    expect(daemon.sentOf('session_annotations_clear')).toEqual([
      expect.objectContaining({ session_id: 's1', generation: 8 }),
    ]);
    expect(panel().getByText('✓ sent 1 to the session')).toBeInTheDocument();
  });

  it('keeps the marks and explains when the session is waiting on an approval', async () => {
    const { daemon } = await openAnnotatedSession({ annotations: [PARSER], generation: 7 });
    daemon.on('session_annotations_submit', ({ session_id }) => ({
      event: 'session_annotations_submit_result',
      session_id,
      success: false,
      status: 'skipped_pending_approval',
    }));

    fireEvent.click(panel().getByRole('button', { name: /Send all/ }));
    await daemon.idle();

    expect(screen.getByTestId('annotation-send-note')).toHaveTextContent('Not sent — the session is waiting on an approval');
    expect(cards()).toEqual(['❓parserwhy this?']);
    expect(daemon.sentOf('session_annotations_clear')).toEqual([]);
  });

  it('says why the daemon could not deliver the marks', async () => {
    const { daemon } = await openAnnotatedSession({ annotations: [PARSER], generation: 7 });
    daemon.on('session_annotations_submit', ({ session_id }) => ({
      event: 'session_annotations_submit_result',
      session_id,
      success: false,
      status: 'error',
      error: 'pty write failed',
    }));

    fireEvent.click(panel().getByRole('button', { name: /Send all/ }));
    await daemon.idle();

    expect(screen.getByTestId('annotation-send-note')).toHaveTextContent('pty write failed');
    expect(cards()).toEqual(['❓parserwhy this?']);
  });

  it('saves the note once the user pauses typing', async () => {
    const { daemon } = await openAnnotatedSession({ annotations: [PARSER], generation: 7 });

    fireEvent.change(panel().getByLabelText('Note sent with these annotations'), { target: { value: 'and land it' } });
    await daemon.idle();
    expect(daemon.sentOf('session_annotations_save')).toEqual([]);
    await act(() => vi.advanceTimersByTimeAsync(400));

    expect(daemon.sentOf('session_annotations_save')).toEqual([
      expect.objectContaining({ session_id: 's1', note: 'and land it', generation: 8 }),
    ]);
  });

  it('reads a session’s message window again only when the daemon says that session’s window changed', async () => {
    const { daemon } = await openAnnotatedSession({ annotations: [], generation: 0 });
    const windowReads = () => daemon.sentOf('session_messages_get').map((command) => command.session_id);
    expect(windowReads()).toEqual(['s1']);

    daemon.emit({ event: 'session_messages_changed', session_id: 's2' });
    await daemon.idle();
    expect(windowReads()).toEqual(['s1']);

    daemon.emit({ event: 'session_messages_changed', session_id: 's1' });
    await daemon.idle();
    expect(windowReads()).toEqual(['s1', 's1']);
  });

  it('marks what the agent wrote in the terminal and saves the mark against its message', async () => {
    const { daemon } = await openTerminalWithWindow({
      success: true,
      status: 'ready',
      messages: [{ key: 'turn-1', markdown: AGENT_TEXT }],
      truncated: false,
    });
    daemon.on('session_annotations_save', ({ session_id, generation }) => ({
      event: 'session_annotations_save_result',
      session_id,
      success: true,
      generation,
    }));

    await altDragAcrossFirstRow(daemon, 120);
    fireEvent.click(within(screen.getByTestId('annotation-popup')).getByRole('button', { name: 'Clarify this' }));
    await daemon.idle();

    const saved = daemon.sentOf('session_annotations_save').pop()!;
    expect(saved.annotations).toEqual([expect.objectContaining({
      message_key: 'turn-1',
      quick_label_id: 'clarify-this',
      quote: expect.stringMatching(/^The parser/),
    })]);
    expect(AGENT_TEXT).toContain(saved.annotations[0].quote);
  });

  it('says the agent has written nothing to mark yet when its message window is empty', async () => {
    const { daemon } = await openTerminalWithWindow({ success: true, status: 'ready', messages: [], truncated: false });

    await altDragAcrossFirstRow(daemon, 120);

    expect(screen.getByTestId('annotation-notice')).toHaveTextContent('The agent has not written a message to annotate yet.');
  });

  it('says no transcript could be read when the daemon cannot give the message window', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { daemon } = await openTerminalWithWindow({
      success: false,
      status: 'unavailable',
      messages: [],
      truncated: false,
      error: 'no transcript for session s1',
    });

    await altDragAcrossFirstRow(daemon, 120);

    expect(screen.getByTestId('annotation-notice')).toHaveTextContent('No transcript could be read for this session');
  });
});
