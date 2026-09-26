import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { agentPane, agentWorkspace, daemonSession, daemonWorkspace } from './test/daemonFixtures';
import type { CommandMessage, EventMessage } from './test/protocol';
import { openAttachedTerminals, openSession } from './test/appFixtures';
import { renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

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
const NEXT_TURN = 'Added the retry test and pushed.';
const THE_PARSER = { from: 1, to: 80 };
const THE_RETRY_WRAPPER = { from: 300, to: 500 };

type MessageWindow = Omit<EventMessage<'session_messages_get_result'>, 'event' | 'session_id' | 'request_id'>;

function readyWindow(...messages: Array<{ key: string; markdown: string }>): MessageWindow {
  return { success: true, status: 'ready', messages, truncated: false };
}

function acceptSaves(daemon: ScriptedDaemon) {
  daemon.on('session_annotations_save', ({ session_id, generation }) => ({
    event: 'session_annotations_save_result',
    session_id,
    success: true,
    generation,
  }));
}

function answerSubmits(daemon: ScriptedDaemon, status: 'delivered' | 'skipped_pending_approval' | null) {
  daemon.on('session_annotations_submit', ({ session_id }) => (
    status === null ? undefined : { event: 'session_annotations_submit_result', session_id, success: status === 'delivered', status }
  ));
  daemon.on('session_annotations_clear', ({ session_id, generation }) => ({
    event: 'session_annotations_clear_result',
    session_id,
    success: true,
    generation,
  }));
}

function deliver(daemon: ScriptedDaemon, submit: CommandMessage<'session_annotations_submit'>) {
  daemon.replyTo(submit, {
    event: 'session_annotations_submit_result',
    request_id: submit.request_id,
    session_id: submit.session_id,
    success: true,
    status: 'delivered',
  });
}

async function openTerminalWithWindow(messageWindow: MessageWindow | null, { sessions = ['s1'], apart = false }: { sessions?: string[]; apart?: boolean } = {}) {
  const split = sessions.length > 1 && !apart
    ? daemonWorkspace('ws', {
      root: { type: 'split', split_id: 'split-a', direction: 'vertical', ratio: 0.5, children: sessions.map((id) => ({ type: 'pane', pane_id: `pane-${id}` })) },
      panes: sessions.map((id) => agentPane(id, 'ws')),
    }, { title: 'ws' })
    : null;
  return openAttachedTerminals({
    sessions: sessions.map((id) => daemonSession(id, { state: 'idle', ...(split ? { workspace_id: split.id } : {}) })),
    workspaces: split ? [split] : sessions.map(agentWorkspace),
    output: { s1: AGENT_TEXT },
    script: (daemon) => {
      if (messageWindow) {
        daemon.on('session_messages_get', ({ session_id }) => ({ event: 'session_messages_get_result', session_id, ...messageWindow }));
      }
      daemon.on('session_annotations_get', ({ session_id }) => ({
        event: 'session_annotations_get_result',
        session_id,
        success: true,
        annotations: [],
        generation: 0,
      }));
      acceptSaves(daemon);
    },
  });
}

function terminalCanvas() {
  return document.querySelector('[data-pane-id="pane-s1"] canvas')!;
}

async function markText(daemon: ScriptedDaemon, { from, to }: { from: number; to: number }) {
  fireEvent.mouseDown(terminalCanvas(), { button: 0, altKey: true, clientX: from, clientY: 2 });
  fireEvent.mouseMove(document, { buttons: 1, altKey: true, clientX: to, clientY: 2 });
  fireEvent.mouseUp(document, { button: 0, altKey: true, clientX: to, clientY: 2 });
  await daemon.idle();
}

async function altClick(daemon: ScriptedDaemon, clientX: number) {
  fireEvent.mouseDown(terminalCanvas(), { button: 0, altKey: true, clientX, clientY: 2 });
  fireEvent.mouseUp(document, { button: 0, altKey: true, clientX, clientY: 2 });
  await daemon.idle();
}

async function settleFocus() {
  await act(() => vi.advanceTimersByTimeAsync(20));
}

const panel = () => within(screen.getByTestId('annotation-panel'));
const popup = () => within(screen.getByTestId('annotation-popup'));
const cards = () => Array.from(document.querySelectorAll('.anno-card-open')).map((card) => card.textContent);
const cardButtons = () => Array.from(document.querySelectorAll<HTMLElement>('.anno-card-open'));
const lastSave = (daemon: ScriptedDaemon) => daemon.sentOf('session_annotations_save').slice(-1)[0];
const saved = (daemon: ScriptedDaemon) => daemon.sentOf('session_annotations_save').slice(-1)[0]?.annotations ?? [];
const commentBox = () => screen.getByRole('textbox', { name: 'Annotation comment' }) as HTMLTextAreaElement;
const labelPicker = () => document.querySelector<HTMLElement>('.md-quick-label-picker');
const hint = () => screen.getByTestId('annotation-popup-hint').textContent;

function pickGrouped(text: string) {
  if (!labelPicker()) fireEvent.click(popup().getByRole('button', { name: 'More labels' }));
  fireEvent.click(within(labelPicker()!).getByRole('button', { name: new RegExp(text) }));
}

async function writeComment(daemon: ScriptedDaemon, text: string) {
  fireEvent.click(popup().getByRole('button', { name: 'Write a comment' }));
  fireEvent.change(commentBox(), { target: { value: text } });
  fireEvent.click(popup().getByRole('button', { name: 'Comment' }));
  await daemon.idle();
}

describe('App session annotations', () => {
  it('shows the marks and note stored for a session, reading an old emoji-only mark as its quick label', async () => {
    await openAnnotatedSession({ annotations: [PARSER], note: 'Split this into two PRs.', generation: 7 });

    expect(cards()).toEqual(['❓parserwhy this?']);
    expect(panel().getByLabelText('Note sent with these annotations')).toHaveValue('Split this into two PRs.');
  });

  it('saves the whole remaining list with its note under the next generation when the user removes a mark', async () => {
    const { daemon } = await openAnnotatedSession({ annotations: [PARSER, RETRY], note: 'and land it', generation: 7 });
    acceptSaves(daemon);

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

  it('sends every mark to the session, clears the stored ones once delivered, and lets the confirmation fade', async () => {
    const { daemon } = await openAnnotatedSession({ annotations: [PARSER], generation: 7 });
    answerSubmits(daemon, 'delivered');

    fireEvent.click(panel().getByRole('button', { name: /Send all/ }));
    await daemon.idle();

    const [submit] = daemon.sentOf('session_annotations_submit');
    expect(submit).toMatchObject({ session_id: 's1', text: expect.stringContaining('parser') });
    expect(submit.text).toContain('why this?');
    expect(daemon.sentOf('session_annotations_clear')).toEqual([
      expect.objectContaining({ session_id: 's1', generation: 8 }),
    ]);
    expect(panel().getByText('✓ sent 1 to the session')).toBeInTheDocument();

    await act(() => vi.advanceTimersByTimeAsync(2199));
    expect(panel().getByText('✓ sent 1 to the session')).toBeInTheDocument();
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(screen.queryByTestId('annotation-panel')).toBeNull();
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
    await act(() => vi.advanceTimersByTimeAsync(10_000));

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
});

describe('App session annotations payload', () => {
  const VERIFY_PARSER: StoredAnnotation = { id: 'a', message_key: 'turn-1', start: 4, end: 10, quote: 'parser', quick_label_id: 'verify-this', comment: '' };
  const COMMENT_ONLY: StoredAnnotation = { id: 'b', message_key: 'turn-1', start: 50, end: 57, quote: 'wrapper', comment: 'name it after the verb' };
  const EXACTLY_CRLF: StoredAnnotation = { id: 'c', message_key: 'turn-1', start: 27, end: 31, quote: 'CRLF', quick_label_id: 'exactly-this', comment: '' };
  const RECEIPT_RETRY: StoredAnnotation = { id: 'd', message_key: 'turn-1', start: 40, end: 45, quote: 'retry', quick_label_id: 'show-the-receipt', comment: 'which test?' };

  const MARKS_IN_READING_ORDER = [
    '## 1. 🔍 Verify this\n\n> parser\n\nThis seems like an assumption. Verify by reading the actual code before proceeding.',
    '## 2. 💯 Exactly this\n\n> CRLF\n',
    '## 3. 🧾 Show the receipt\n\n> retry\n\nYou asserted this as fact. Name what backs it — the file and line, the measurement, the command output — or say plainly that it is an assumption.\nwhich test?',
    '## 4. 💬 Comment\n\n> wrapper\n\nname it after the verb',
  ].join('\n\n');

  it.each([
    { note: '', text: `Feedback on your last message.\n\n${MARKS_IN_READING_ORDER}` },
    { note: '   \n  ', text: `Feedback on your last message.\n\n${MARKS_IN_READING_ORDER}` },
    { note: 'Split this into two PRs.', text: `Feedback on your last message.\n\nSplit this into two PRs.\n\n${MARKS_IN_READING_ORDER}` },
  ])('sends the note "$note" ahead of every mark, quoted and labelled in reading order', async ({ note, text }) => {
    const { daemon } = await openAnnotatedSession({ annotations: [COMMENT_ONLY, RECEIPT_RETRY, VERIFY_PARSER, EXACTLY_CRLF], note, generation: 7 });
    answerSubmits(daemon, 'delivered');

    fireEvent.click(panel().getByRole('button', { name: /Send all/ }));
    await daemon.idle();

    expect(daemon.sentOf('session_annotations_submit').map((submit) => submit.text)).toEqual([text]);
  });

  it('sends the marks and note on the send shortcut typed in the note', async () => {
    const { daemon } = await openAnnotatedSession({ annotations: [EXACTLY_CRLF], generation: 7 });
    answerSubmits(daemon, 'delivered');
    const note = panel().getByLabelText('Note sent with these annotations');

    fireEvent.change(note, { target: { value: 'Ship it.' } });
    fireEvent.keyDown(note, { key: 'Enter', metaKey: true });
    await daemon.idle();

    expect(daemon.sentOf('session_annotations_submit').map((submit) => submit.text)).toEqual([
      'Feedback on your last message.\n\nShip it.\n\n## 1. 💯 Exactly this\n\n> CRLF',
    ]);
  });
});

describe('App session annotations in flight', () => {
  async function markAndHoldSend() {
    const view = await openTerminalWithWindow(readyWindow({ key: 'turn-1', markdown: AGENT_TEXT }));
    answerSubmits(view.daemon, null);
    await markText(view.daemon, THE_PARSER);
    fireEvent.click(popup().getByRole('button', { name: 'Clarify this' }));
    await view.daemon.idle();
    return view;
  }

  it('sends once while the first send is unanswered, and spends the note it carried', async () => {
    const { daemon } = await markAndHoldSend();
    const note = () => panel().getByLabelText('Note sent with these annotations');
    fireEvent.change(note(), { target: { value: 'Split this.' } });

    fireEvent.click(panel().getByRole('button', { name: /Send all/ }));
    await daemon.idle();
    expect(panel().getByRole('button', { name: 'Sending…' })).toBeDisabled();
    fireEvent.keyDown(window, { key: 'Enter', metaKey: true });
    fireEvent.click(panel().getByRole('button', { name: 'Sending…' }));
    await daemon.idle();
    const submits = daemon.sentOf('session_annotations_submit');
    expect(submits).toHaveLength(1);

    deliver(daemon, submits[0]);
    await daemon.idle();

    expect(daemon.sentOf('session_annotations_clear')).toHaveLength(1);
    expect(panel().getByText('✓ sent 1 to the session')).toBeInTheDocument();
    expect(note()).toHaveValue('');
  });

  it('keeps a note rewritten while the send was in flight, saving it under the next generation', async () => {
    const { daemon } = await markAndHoldSend();
    const note = () => panel().getByLabelText('Note sent with these annotations');
    fireEvent.change(note(), { target: { value: 'Split this.' } });
    fireEvent.click(panel().getByRole('button', { name: /Send all/ }));
    await daemon.idle();
    const [submit] = daemon.sentOf('session_annotations_submit');

    fireEvent.change(note(), { target: { value: 'Split this, smallest first.' } });
    await act(() => vi.advanceTimersByTimeAsync(400));
    const generationBefore = lastSave(daemon).generation;
    deliver(daemon, submit);
    await daemon.idle();

    expect(note()).toHaveValue('Split this, smallest first.');
    expect(daemon.sentOf('session_annotations_clear')).toEqual([]);
    expect(lastSave(daemon)).toMatchObject({
      note: 'Split this, smallest first.',
      annotations: [],
      generation: generationBefore + 1,
    });
  });

  it('keeps the note when the session refuses the send', async () => {
    const { daemon } = await markAndHoldSend();
    answerSubmits(daemon, 'skipped_pending_approval');
    fireEvent.change(panel().getByLabelText('Note sent with these annotations'), { target: { value: 'Split this.' } });

    fireEvent.click(panel().getByRole('button', { name: /Send all/ }));
    await daemon.idle();

    expect(screen.getByTestId('annotation-send-note')).toBeInTheDocument();
    expect(panel().getByLabelText('Note sent with these annotations')).toHaveValue('Split this.');
    expect(lastSave(daemon)).toMatchObject({ note: 'Split this.' });
  });

  it('keeps a mark made while a send was in flight, and saves it once the send lands', async () => {
    const { daemon } = await markAndHoldSend();
    fireEvent.click(panel().getByRole('button', { name: /Send all/ }));
    await daemon.idle();
    const [submit] = daemon.sentOf('session_annotations_submit');
    expect(submit.text).toContain('> The parser');
    expect(submit.text).not.toContain('the retry wrapper');

    await markText(daemon, THE_RETRY_WRAPPER);
    fireEvent.click(popup().getByRole('button', { name: 'This is wrong' }));
    await daemon.idle();
    deliver(daemon, submit);
    await daemon.idle();

    expect(cards()).toEqual(['❌the retry wrapper is safe.']);
    expect(saved(daemon)).toEqual([expect.objectContaining({ quote: 'the retry wrapper is safe.', quick_label_id: 'this-is-wrong' })]);
    expect(daemon.sentOf('session_annotations_clear')).toEqual([]);
    expect(screen.getByTestId('annotation-send-note')).toHaveTextContent('1 still here');
  });

  it('keeps a mark relabelled while the send carrying it was in flight, and spends the unchanged one beside it', async () => {
    const { daemon } = await markAndHoldSend();
    await markText(daemon, THE_RETRY_WRAPPER);
    fireEvent.click(popup().getByRole('button', { name: 'I agree' }));
    await daemon.idle();
    fireEvent.click(panel().getByRole('button', { name: /Send all/ }));
    await daemon.idle();
    const [submit] = daemon.sentOf('session_annotations_submit');

    await altClick(daemon, 30);
    fireEvent.click(popup().getByRole('button', { name: 'This is wrong' }));
    await daemon.idle();
    deliver(daemon, submit);
    await daemon.idle();

    expect(cards()).toEqual(['❌The parser']);
    expect(saved(daemon)).toEqual([expect.objectContaining({ quote: 'The parser', quick_label_id: 'this-is-wrong' })]);
    expect(daemon.sentOf('session_annotations_clear')).toEqual([]);
  });
});

describe('App session annotations in the terminal', () => {
  it('marks what the agent wrote in the terminal and saves the mark against its message', async () => {
    const { daemon } = await openTerminalWithWindow(readyWindow({ key: 'turn-1', markdown: AGENT_TEXT }));

    await markText(daemon, { from: 1, to: 120 });
    fireEvent.click(popup().getByRole('button', { name: 'Clarify this' }));
    await daemon.idle();

    const annotations = saved(daemon);
    expect(annotations).toEqual([expect.objectContaining({
      message_key: 'turn-1',
      quick_label_id: 'clarify-this',
      quote: expect.stringMatching(/^The parser/),
    })]);
    expect(AGENT_TEXT).toContain(annotations[0].quote);
    expect(screen.queryByTestId('annotation-popup')).toBeNull();
  });

  it('offers the promoted labels, a grouped picker with agreement first, a comment and removal', async () => {
    const { daemon } = await openTerminalWithWindow(readyWindow({ key: 'turn-1', markdown: AGENT_TEXT }));

    await markText(daemon, THE_PARSER);

    expect(popup().getAllByRole('button').map((button) => button.getAttribute('aria-label'))).toEqual([
      'I agree',
      'This is wrong',
      'Clarify this',
      'More labels',
      'Write a comment',
      'Remove this annotation',
    ]);
    expect(hint()).toBe('Pick a label, or write a comment');

    fireEvent.click(popup().getByRole('button', { name: 'More labels' }));
    const [exactlyThis, dontLoveThis] = within(labelPicker()!).getAllByRole('button');
    const [firstSeparator] = within(labelPicker()!).getAllByRole('separator');
    expect(exactlyThis).toHaveAccessibleName(/Exactly this/);
    expect(dontLoveThis).toHaveAccessibleName(/I don't love this/);
    expect(exactlyThis.compareDocumentPosition(firstSeparator) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(firstSeparator.compareDocumentPosition(dontLoveThis) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

    fireEvent.mouseEnter(within(labelPicker()!).getByRole('button', { name: /Show the receipt/ }));
    expect(hint()).toBe('Show the receipt');
  });

  it('toggles a promoted label off to drop the mark rather than leave a blank wash', async () => {
    const { daemon } = await openTerminalWithWindow(readyWindow({ key: 'turn-1', markdown: AGENT_TEXT }));
    await markText(daemon, THE_PARSER);
    fireEvent.click(popup().getByRole('button', { name: 'I agree' }));
    await daemon.idle();
    expect(saved(daemon)).toEqual([expect.objectContaining({ quote: 'The parser', quick_label_id: 'i-agree' })]);

    await altClick(daemon, 30);
    expect(popup().getByRole('button', { name: 'I agree' })).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(popup().getByRole('button', { name: 'I agree' }));
    await daemon.idle();

    expect(saved(daemon)).toEqual([]);
    expect(screen.queryByTestId('annotation-panel')).toBeNull();
  });

  it('reopens a mark from the message, names its label, and lets the user change or remove it', async () => {
    const { daemon } = await openTerminalWithWindow(readyWindow({ key: 'turn-1', markdown: AGENT_TEXT }));
    await markText(daemon, THE_PARSER);
    pickGrouped('Verify this');
    await daemon.idle();
    expect(screen.queryByTestId('annotation-popup')).toBeNull();

    await altClick(daemon, 30);
    expect(popup().getByRole('button', { name: 'More labels' })).toHaveTextContent('🔍');
    expect(popup().getByRole('button', { name: 'More labels' })).toHaveAttribute('aria-pressed', 'true');
    expect(hint()).toBe('Verify this');
    fireEvent.mouseEnter(popup().getByRole('button', { name: 'This is wrong' }));
    expect(hint()).toBe('This is wrong');
    fireEvent.mouseLeave(popup().getByRole('button', { name: 'This is wrong' }));
    expect(hint()).toBe('Verify this');

    pickGrouped('Show the receipt');
    await daemon.idle();
    expect(saved(daemon)).toEqual([expect.objectContaining({ quote: 'The parser', quick_label_id: 'show-the-receipt' })]);

    await altClick(daemon, 30);
    fireEvent.click(popup().getByRole('button', { name: 'Remove this annotation' }));
    await daemon.idle();
    expect(saved(daemon)).toEqual([]);
    expect(screen.queryByTestId('annotation-popup')).toBeNull();
  });

  it('closes the grouped picker from its trigger or Escape, leaving the popup, and picks a grouped label by digit', async () => {
    const { daemon } = await openTerminalWithWindow(readyWindow({ key: 'turn-1', markdown: AGENT_TEXT }));
    await markText(daemon, THE_PARSER);
    const trigger = () => popup().getByRole('button', { name: 'More labels' });

    fireEvent.click(trigger());
    fireEvent.click(trigger());
    expect(labelPicker()).toBeNull();
    expect(screen.getByTestId('annotation-popup')).toBeInTheDocument();

    fireEvent.click(trigger());
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(labelPicker()).toBeNull();
    expect(screen.getByTestId('annotation-popup')).toBeInTheDocument();

    fireEvent.click(trigger());
    fireEvent.keyDown(window, { code: 'Digit3', key: '3' });
    await daemon.idle();
    expect(saved(daemon)).toEqual([expect.objectContaining({ quick_label_id: 'verify-this' })]);
    expect(labelPicker()).toBeNull();
    expect(screen.queryByTestId('annotation-popup')).toBeNull();
  });

  it.each([
    { dismissal: 'Escape', dismiss: () => fireEvent.keyDown(window, { key: 'Escape' }) },
    { dismissal: 'a press outside the popup', dismiss: () => fireEvent.pointerDown(terminalCanvas()) },
  ])('discards a highlight left bare by $dismissal', async ({ dismiss }) => {
    const { daemon } = await openTerminalWithWindow(readyWindow({ key: 'turn-1', markdown: AGENT_TEXT }));
    await markText(daemon, THE_PARSER);
    fireEvent.pointerDown(popup().getByRole('button', { name: 'This is wrong' }));
    expect(screen.getByTestId('annotation-popup')).toBeInTheDocument();

    dismiss();
    await daemon.idle();

    expect(screen.queryByTestId('annotation-popup')).toBeNull();
    expect(saved(daemon)).toEqual([]);
    expect(screen.queryByTestId('annotation-panel')).toBeNull();
  });

  it('keeps a comment draft open through presses and new drags until the user closes it', async () => {
    const { daemon } = await openTerminalWithWindow(readyWindow({ key: 'turn-1', markdown: AGENT_TEXT }));
    await markText(daemon, THE_PARSER);
    fireEvent.click(popup().getByRole('button', { name: 'Write a comment' }));
    fireEvent.change(commentBox(), { target: { value: 'keep this in app memory' } });

    fireEvent.pointerDown(terminalCanvas());
    await markText(daemon, THE_RETRY_WRAPPER);

    expect(commentBox()).toHaveValue('keep this in app memory');
    expect(daemon.sentOf('session_annotations_save')).toEqual([]);

    fireEvent.keyDown(window, { key: 'Escape' });
    await daemon.idle();
    expect(screen.queryByTestId('annotation-popup')).toBeNull();
    expect(saved(daemon)).toEqual([]);
  });

  it('saves a written comment, reopens it prefilled with the caret at its end, and hands the keyboard back to the terminal', async () => {
    const { daemon } = await openTerminalWithWindow(readyWindow({ key: 'turn-1', markdown: AGENT_TEXT }));
    const terminalInput = screen.getByRole('textbox', { name: 'Terminal input' });
    await markText(daemon, THE_PARSER);

    await writeComment(daemon, 'first take');
    expect(saved(daemon)).toEqual([expect.objectContaining({ quote: 'The parser', comment: 'first take', quick_label_id: '' })]);
    expect(document.activeElement).toBe(terminalInput);

    await altClick(daemon, 30);
    await settleFocus();
    expect(commentBox()).toHaveValue('first take');
    expect(document.activeElement).toBe(commentBox());
    expect(commentBox().selectionStart).toBe('first take'.length);
    fireEvent.change(commentBox(), { target: { value: 'second take' } });
    fireEvent.click(popup().getByRole('button', { name: 'Comment' }));
    await daemon.idle();
    expect(saved(daemon)).toEqual([expect.objectContaining({ comment: 'second take' })]);

    fireEvent.click(cardButtons()[0]);
    await settleFocus();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(document.activeElement).toBe(terminalInput);
  });

  it('leaves focus where it is when a press outside the pane closes the popup', async () => {
    const { daemon } = await openTerminalWithWindow(readyWindow({ key: 'turn-1', markdown: AGENT_TEXT }));
    await markText(daemon, THE_PARSER);
    fireEvent.click(popup().getByRole('button', { name: 'Clarify this' }));
    await markText(daemon, THE_RETRY_WRAPPER);
    fireEvent.click(popup().getByRole('button', { name: 'I agree' }));
    await daemon.idle();
    await altClick(daemon, 30);
    expect(screen.getByTestId('annotation-popup')).toBeInTheDocument();

    const sessionRow = screen.getByRole('button', { name: 'Open s1' });
    sessionRow.focus();
    fireEvent.pointerDown(sessionRow);
    fireEvent.mouseDown(sessionRow);
    await daemon.idle();

    expect(screen.queryByTestId('annotation-popup')).toBeNull();
    expect(document.activeElement).toBe(sessionRow);
  });

  it('opens the editor from a panel row, even for a bare reaction, and removes the mark from it', async () => {
    const { daemon } = await openTerminalWithWindow(readyWindow({ key: 'turn-1', markdown: AGENT_TEXT }));
    await markText(daemon, THE_PARSER);
    fireEvent.click(popup().getByRole('button', { name: 'Clarify this' }));
    await markText(daemon, THE_RETRY_WRAPPER);
    fireEvent.click(popup().getByRole('button', { name: 'I agree' }));
    await daemon.idle();

    fireEvent.click(cardButtons()[0]);
    await settleFocus();
    expect(commentBox()).toHaveValue('');
    expect(document.activeElement).toBe(commentBox());

    fireEvent.click(popup().getByRole('button', { name: 'Remove' }));
    await daemon.idle();
    expect(saved(daemon)).toEqual([expect.objectContaining({ quote: 'the retry wrapper is safe.' })]);
    expect(screen.queryByTestId('annotation-popup')).toBeNull();
  });

  it('keeps a comment popup and the panel where the user drags them', async () => {
    const { daemon } = await openTerminalWithWindow(readyWindow({ key: 'turn-1', markdown: AGENT_TEXT }));
    await markText(daemon, THE_PARSER);
    fireEvent.click(popup().getByRole('button', { name: 'Write a comment' }));
    const dialog = screen.getByRole('dialog', { name: 'Edit terminal annotation' });

    fireEvent.mouseDown(popup().getByRole('button', { name: 'Move comment editor with arrow keys' }), { clientX: 100, clientY: 100 });
    fireEvent.mouseMove(window, { clientX: 260, clientY: 340 });
    fireEvent.mouseUp(window);
    fireEvent.click(popup().getByRole('button', { name: 'I agree' }));
    await daemon.idle();

    expect(screen.getByRole('dialog', { name: 'Edit terminal annotation' })).toBe(dialog);
    expect(dialog.style.left).toBe('160px');
    expect(dialog.style.top).toBe('240px');

    const annotationPanel = screen.getByTestId('annotation-panel');
    fireEvent.mouseDown(annotationPanel.querySelector('.anno-panel-head')!, { clientX: 100, clientY: 100 });
    fireEvent.mouseMove(window, { clientX: 260, clientY: 340 });
    fireEvent.mouseUp(window);
    fireEvent.mouseMove(window, { clientX: 700, clientY: 700 });

    expect(annotationPanel.style.left).toBe('160px');
    expect(annotationPanel.style.top).toBe('240px');
  });

  it('shows the marks and note again when the user leaves the session and comes back', async () => {
    const { daemon } = await openTerminalWithWindow(readyWindow({ key: 'turn-1', markdown: AGENT_TEXT }), { sessions: ['s1', 's2'], apart: true });
    daemon.on('session_annotations_get', ({ session_id }) => {
      const stored = daemon.sentOf('session_annotations_save').filter((save) => save.session_id === session_id).slice(-1)[0];
      return { event: 'session_annotations_get_result', session_id, success: true, annotations: stored?.annotations ?? [], note: stored?.note, generation: stored?.generation ?? 0 };
    });
    await markText(daemon, THE_PARSER);
    fireEvent.click(popup().getByRole('button', { name: 'Clarify this' }));
    fireEvent.change(panel().getByLabelText('Note sent with these annotations'), { target: { value: 'Split this into two PRs.' } });
    await act(() => vi.advanceTimersByTimeAsync(400));

    await openSession(daemon, 's2');
    await openSession(daemon, 's1');

    expect(cards()).toEqual(['❓The parser']);
    expect(panel().getByLabelText('Note sent with these annotations')).toHaveValue('Split this into two PRs.');
  });

  it('has no note to write until something is marked', async () => {
    const { daemon } = await openTerminalWithWindow(readyWindow({ key: 'turn-1', markdown: AGENT_TEXT }));
    expect(screen.queryByTestId('annotation-note')).toBeNull();

    await markText(daemon, THE_PARSER);
    fireEvent.click(popup().getByRole('button', { name: 'Clarify this' }));
    await daemon.idle();

    expect(screen.getByTestId('annotation-note')).toBeInTheDocument();
  });
});

describe('App session annotations send shortcut', () => {
  it('sends the marks, with the comment still being typed, from the focused pane', async () => {
    const { daemon } = await openTerminalWithWindow(readyWindow({ key: 'turn-1', markdown: AGENT_TEXT }));
    answerSubmits(daemon, 'delivered');
    await markText(daemon, THE_PARSER);
    fireEvent.click(popup().getByRole('button', { name: 'Write a comment' }));
    fireEvent.change(commentBox(), { target: { value: 'still typing this' } });

    fireEvent.keyDown(window, { key: 'Enter', metaKey: true });
    await daemon.idle();

    expect(daemon.sentOf('session_annotations_submit').map((submit) => submit.text)).toEqual([
      'Feedback on your last message.\n\n## 1. 💬 Comment\n\n> The parser\n\nstill typing this',
    ]);
    expect(screen.queryByTestId('annotation-popup')).toBeNull();
  });

  it('sends nothing when there is nothing to send', async () => {
    const { daemon } = await openTerminalWithWindow(readyWindow({ key: 'turn-1', markdown: AGENT_TEXT }));

    fireEvent.keyDown(screen.getByRole('textbox', { name: 'Terminal input' }), { key: 'Enter', metaKey: true });
    await daemon.idle();

    expect(daemon.sentOf('session_annotations_submit')).toEqual([]);
  });

  it('sends nothing from a pane that does not hold focus', async () => {
    const { daemon } = await openTerminalWithWindow(readyWindow({ key: 'turn-1', markdown: AGENT_TEXT }), { sessions: ['s1', 's2'] });
    await markText(daemon, THE_PARSER);
    fireEvent.click(popup().getByRole('button', { name: 'Clarify this' }));
    fireEvent.mouseDown(document.querySelector('[data-pane-id="pane-s2"] canvas')!, { button: 0, clientX: 1, clientY: 2 });
    fireEvent.mouseUp(document, { button: 0, clientX: 1, clientY: 2 });
    await daemon.idle();

    fireEvent.keyDown(window, { key: 'Enter', metaKey: true });
    await daemon.idle();

    expect(daemon.sentOf('session_annotations_submit')).toEqual([]);
    expect(cards()).toEqual(['❓The parser']);
  });
});

describe('App session annotations across message windows', () => {
  it('keeps the newer message window when an older read answers last', async () => {
    const { daemon } = await openTerminalWithWindow(null);
    daemon.emit({ event: 'session_messages_changed', session_id: 's1' });
    await daemon.idle();
    const [older, newer] = daemon.sentOf('session_messages_get');

    daemon.replyTo(newer, { event: 'session_messages_get_result', request_id: newer.request_id, session_id: 's1', ...readyWindow({ key: 'turn-2', markdown: AGENT_TEXT }) });
    daemon.replyTo(older, { event: 'session_messages_get_result', request_id: older.request_id, session_id: 's1', ...readyWindow({ key: 'turn-1', markdown: 'Something else entirely.' }) });
    await daemon.idle();
    await markText(daemon, THE_PARSER);
    fireEvent.click(popup().getByRole('button', { name: 'Clarify this' }));
    await daemon.idle();

    expect(saved(daemon)).toEqual([expect.objectContaining({ message_key: 'turn-2', quote: 'The parser' })]);
  });

  it('keeps a mark through a repeated window, a new turn and a terminal reset, and lists it once its turn has left the window', async () => {
    let messageWindow = readyWindow({ key: 'turn-1', markdown: AGENT_TEXT });
    const { daemon } = await openTerminalWithWindow(null);
    daemon.on('session_messages_get', ({ session_id }) => ({ event: 'session_messages_get_result', session_id, ...messageWindow }));
    daemon.emit({ event: 'session_messages_changed', session_id: 's1' });
    await daemon.idle();
    await markText(daemon, THE_PARSER);
    fireEvent.click(popup().getByRole('button', { name: 'Clarify this' }));
    await daemon.idle();
    const reopensTheMark = async () => {
      await altClick(daemon, 30);
      const reopened = screen.queryByTestId('annotation-popup') !== null;
      if (reopened) fireEvent.keyDown(window, { key: 'Escape' });
      return reopened;
    };

    daemon.emit({ event: 'session_messages_changed', session_id: 's1' });
    await daemon.idle();
    expect(cards()).toEqual(['❓The parser']);
    expect(await reopensTheMark()).toBe(true);

    messageWindow = readyWindow({ key: 'turn-1', markdown: AGENT_TEXT }, { key: 'turn-2', markdown: NEXT_TURN });
    daemon.emit({ event: 'session_messages_changed', session_id: 's1' });
    daemon.emit({ event: 'pty_output', id: 's1', seq: 2, data: btoa(`\r\n\r\n${NEXT_TURN}`) });
    await daemon.idle();
    expect(cards()).toEqual(['❓The parser']);
    expect(await reopensTheMark()).toBe(true);

    daemon.emit({ event: 'pty_desync', id: 's1', reason: 'desync' });
    await daemon.idle();
    daemon.emit({ event: 'pty_output', id: 's1', seq: 1, data: btoa(AGENT_TEXT) });
    await daemon.idle();
    expect(cards()).toEqual(['❓The parser']);
    expect(await reopensTheMark()).toBe(true);

    messageWindow = readyWindow({ key: 'turn-2', markdown: NEXT_TURN });
    daemon.emit({ event: 'session_messages_changed', session_id: 's1' });
    await daemon.idle();
    expect(cards()).toEqual(['❓The parser']);
    expect(await reopensTheMark()).toBe(false);
  });
});

describe('App session annotations notices', () => {
  it('says the agent has written nothing to mark yet when its message window is empty', async () => {
    const { daemon } = await openTerminalWithWindow({ success: true, status: 'ready', messages: [], truncated: false });

    await markText(daemon, { from: 1, to: 120 });

    expect(screen.getByTestId('annotation-notice')).toHaveTextContent('The agent has not written a message to annotate yet.');
  });

  it('says the agent’s first message is still being recorded while the transcript is being discovered', async () => {
    const { daemon } = await openTerminalWithWindow({ success: true, status: 'discovering', messages: [], truncated: false });

    await markText(daemon, { from: 1, to: 120 });

    expect(screen.getByTestId('annotation-notice')).toHaveTextContent('still being recorded');
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

    await markText(daemon, { from: 1, to: 120 });

    expect(screen.getByTestId('annotation-notice')).toHaveTextContent('No transcript could be read for this session');
  });

  it('says only the agent’s text can be marked, and drops the notice on its own or once a mark lands', async () => {
    let messageWindow = readyWindow({ key: 'turn-1', markdown: 'Nothing on screen matches this message.' });
    const { daemon } = await openTerminalWithWindow(null);
    daemon.on('session_messages_get', ({ session_id }) => ({ event: 'session_messages_get_result', session_id, ...messageWindow }));
    daemon.emit({ event: 'session_messages_changed', session_id: 's1' });
    await daemon.idle();

    await markText(daemon, THE_PARSER);
    expect(screen.getByTestId('annotation-notice')).toHaveTextContent('Only what the agent wrote can be annotated');
    await act(() => vi.advanceTimersByTimeAsync(5000));
    expect(screen.queryByTestId('annotation-notice')).toBeNull();

    await markText(daemon, THE_PARSER);
    expect(screen.getByTestId('annotation-notice')).toBeInTheDocument();
    messageWindow = readyWindow({ key: 'turn-1', markdown: AGENT_TEXT });
    daemon.emit({ event: 'session_messages_changed', session_id: 's1' });
    await daemon.idle();
    await markText(daemon, THE_PARSER);

    expect(screen.queryByTestId('annotation-notice')).toBeNull();
    expect(screen.getByTestId('annotation-popup')).toBeInTheDocument();
  });
});
