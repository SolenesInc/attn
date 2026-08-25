import { StrictMode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { ConversationPane } from './index';
import { DaemonApiProvider, type DaemonApi } from '../../contexts/DaemonApiContext';
import { useConversationsStore } from '../../store/conversations';

const SESSION = 'sess-1';

function renderPane({ strict = false } = {}) {
  const sendAgentHistory = vi.fn();
  const sendAgentSetModel = vi.fn();
  const api = {
    sendAgentPrompt: vi.fn(),
    sendAgentToolDetail: vi.fn(),
    sendAgentClearQueue: vi.fn(),
    sendAgentAttach: vi.fn(),
    sendAgentHistory,
    sendAgentSetModel,
  } as unknown as DaemonApi;
  const pane = (
    <DaemonApiProvider api={api}>
      <ConversationPane sessionId={SESSION} paneActive sessionState="idle" />
    </DaemonApiProvider>
  );
  render(strict ? <StrictMode>{pane}</StrictMode> : pane);
  return { sendAgentHistory, sendAgentSetModel };
}

/** Gives a jsdom element the geometry a scrollable list would have. */
function sizeList(list: HTMLElement, size: { scrollHeight: number; clientHeight: number }) {
  Object.defineProperty(list, 'scrollHeight', { value: size.scrollHeight, configurable: true });
  Object.defineProperty(list, 'clientHeight', { value: size.clientHeight, configurable: true });
}

function apply(kind: string, body: Record<string, unknown>, seq: number) {
  act(() => {
    useConversationsStore.getState().applyEnvelope(SESSION, seq, kind, body);
  });
}

function windowed(hasMore = true, seq = 2) {
  apply('conversation_snapshot', {
    epoch: 'e1',
    items: [
      { kind: 'message', id: 'm9', role: 'assistant', text: 'nine', streaming: false },
      { kind: 'message', id: 'm10', role: 'assistant', text: 'ten', streaming: false },
    ],
    has_more: hasMore,
    running: false,
    queue: { steering: [], followUp: [] },
  }, seq);
}

describe('ConversationPane history', () => {
  beforeEach(() => {
    useConversationsStore.setState({ conversations: {} });
  });

  it('offers a way back only while the host holds more', () => {
    renderPane();
    apply('session_ready', { state: 'idle' }, 1);
    windowed(false);
    expect(screen.queryByTestId('conversation-load-earlier')).toBeNull();

    windowed(true, 3);
    expect(screen.getByTestId('conversation-load-earlier')).toBeInTheDocument();
  });

  it('asks for the page before the oldest item it is showing', () => {
    const { sendAgentHistory } = renderPane();
    apply('session_ready', { state: 'idle' }, 1);
    windowed();

    fireEvent.click(screen.getByTestId('conversation-load-earlier'));
    expect(sendAgentHistory).toHaveBeenCalledWith(SESSION, 'message:m9');
  });

  it('holds the reader in place when older messages arrive above them', () => {
    renderPane({ strict: true });
    apply('session_ready', { state: 'idle' }, 1);
    windowed();

    const list = screen.getByTestId('conversation-messages');
    sizeList(list, { scrollHeight: 600, clientHeight: 100 });
    fireEvent.scroll(list);
    expect(list.scrollTop).toBe(0);

    sizeList(list, { scrollHeight: 1000, clientHeight: 100 });
    apply('conversation_page', {
      epoch: 'e1',
      before: 'message:m9',
      items: [{ kind: 'message', id: 'm8', role: 'assistant', text: 'eight', streaming: false }],
      has_more: false,
    }, 3);

    expect(list.scrollHeight - list.scrollTop).toBe(600);
  });

  it('does not ask again while a page is still in flight', () => {
    const { sendAgentHistory } = renderPane();
    apply('session_ready', { state: 'idle' }, 1);
    windowed();

    fireEvent.click(screen.getByTestId('conversation-load-earlier'));
    fireEvent.click(screen.getByTestId('conversation-load-earlier'));
    expect(sendAgentHistory).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('conversation-load-earlier')).toBeDisabled();
  });

  it('draws the page it gets back above what was already there', () => {
    renderPane();
    apply('session_ready', { state: 'idle' }, 1);
    windowed();
    fireEvent.click(screen.getByTestId('conversation-load-earlier'));

    apply('conversation_page', {
      epoch: 'e1',
      before: 'message:m9',
      items: [{ kind: 'message', id: 'm8', role: 'assistant', text: 'eight', streaming: false }],
      has_more: false,
    }, 3);

    const drawn = screen.getAllByTestId(/^conversation-message-/).map((node) => node.getAttribute('data-testid'));
    expect(drawn).toEqual(['conversation-message-m8', 'conversation-message-m9', 'conversation-message-m10']);
    expect(screen.queryByTestId('conversation-load-earlier')).toBeNull();
  });

  it('draws a notice as its own row, and says whether it is still happening', () => {
    renderPane();
    apply('session_ready', { state: 'idle' }, 1);
    apply('notice', { id: 'n1', level: 'info', text: 'Compacting the conversation...', done: false }, 2);

    const row = screen.getByTestId('conversation-notice-n1');
    expect(row).toHaveTextContent('Compacting the conversation...');
    expect(row).toHaveAttribute('data-done', 'false');

    apply('notice', { id: 'n1', level: 'warn', text: 'Compaction was cancelled', done: true }, 3);
    const settled = screen.getByTestId('conversation-notice-n1');
    expect(settled).toHaveTextContent('Compaction was cancelled');
    expect(settled).toHaveAttribute('data-done', 'true');
    expect(settled).toHaveAttribute('data-level', 'warn');
    expect(screen.getAllByTestId(/^conversation-notice-/)).toHaveLength(1);
  });

  it('shows no model picker until the host says what is available', () => {
    renderPane();
    apply('session_ready', { state: 'idle' }, 1);
    expect(screen.queryByTestId('conversation-model')).toBeNull();
  });

  it('switches the model, and shows the host answer rather than the click', () => {
    const { sendAgentSetModel } = renderPane();
    apply('session_ready', { state: 'idle', model: 'openai/luna', models: ['openai/luna', 'anthropic/claude'] }, 1);

    const picker = screen.getByTestId('conversation-model');
    expect(picker).toHaveValue('openai/luna');

    fireEvent.change(picker, { target: { value: 'anthropic/claude' } });
    expect(sendAgentSetModel).toHaveBeenCalledWith(SESSION, 'anthropic/claude');
    apply('model_changed', { model: 'openai/luna', error: 'no credentials' }, 2);
    expect(screen.getByTestId('conversation-model')).toHaveValue('openai/luna');
  });

  it('keeps a model outside the catalog visible instead of showing a wrong one', () => {
    renderPane();
    apply('session_ready', { state: 'idle', model: 'local/experimental', models: ['openai/luna'] }, 1);
    expect(screen.getByTestId('conversation-model')).toHaveTextContent('local/experimental');
  });
});

describe('ConversationPane dropped history', () => {
  beforeEach(() => {
    useConversationsStore.setState({ conversations: {} });
  });

  function dropped(count: number, hasMore: boolean, seq = 2) {
    apply('conversation_snapshot', {
      epoch: 'e1',
      items: [{ kind: 'message', id: 'm900', role: 'assistant', text: 'nine hundred', streaming: false }],
      has_more: hasMore,
      dropped: count,
      running: false,
      queue: { steering: [], followUp: [] },
    }, seq);
  }

  it('says how much of the conversation is gone once there is nothing left to page', () => {
    renderPane();
    apply('session_ready', { state: 'idle' }, 1);
    dropped(1_240, false);

    const row = screen.getByTestId('conversation-history-dropped');
    expect(row).toHaveTextContent('1,240 earlier items are no longer kept');
    expect(row.getAttribute('data-dropped')).toBe('1240');
  });

  it('stays quiet while the way back is still offered', () => {
    renderPane();
    apply('session_ready', { state: 'idle' }, 1);
    dropped(1_240, true);

    expect(screen.queryByTestId('conversation-history-dropped')).toBeNull();
    expect(screen.getByTestId('conversation-load-earlier')).toBeInTheDocument();
  });

  it('draws nothing for a conversation that simply reached its start', () => {
    renderPane();
    apply('session_ready', { state: 'idle' }, 1);
    dropped(0, false);

    expect(screen.queryByTestId('conversation-history-dropped')).toBeNull();
  });

  it('does not double-count under StrictMode', () => {
    renderPane({ strict: true });
    apply('session_ready', { state: 'idle' }, 1);
    dropped(1_240, false);

    expect(screen.getByTestId('conversation-history-dropped').getAttribute('data-dropped')).toBe('1240');
  });

  it('keeps the count when a later snapshot splices onto scroll-back', () => {
    renderPane();
    apply('session_ready', { state: 'idle' }, 1);
    apply('conversation_snapshot', {
      epoch: 'e1',
      items: [{ kind: 'message', id: 'm900', role: 'assistant', text: 'nine hundred', streaming: false }],
      has_more: true,
      dropped: 1_240,
      running: false,
      queue: { steering: [], followUp: [] },
    }, 2);
    apply('conversation_page', {
      epoch: 'e1',
      before: 'message:m900',
      items: [{ kind: 'message', id: 'm899', role: 'assistant', text: 'eight ninety nine', streaming: false }],
      has_more: false,
    }, 3);
    expect(screen.getByTestId('conversation-history-dropped')).toBeInTheDocument();

    apply('conversation_snapshot', {
      epoch: 'e1',
      items: [
        { kind: 'message', id: 'm900', role: 'assistant', text: 'nine hundred', streaming: false },
        { kind: 'message', id: 'm901', role: 'assistant', text: 'nine oh one', streaming: false },
      ],
      has_more: true,
      dropped: 1_250,
      running: false,
      queue: { steering: [], followUp: [] },
    }, 4);

    expect(screen.getByTestId('conversation-message-m899')).toBeInTheDocument();
    expect(screen.getByTestId('conversation-message-m901')).toBeInTheDocument();
    expect(screen.getByTestId('conversation-history-dropped').getAttribute('data-dropped')).toBe('1250');
  });

  it('forgets it when a rebuilt host reads the whole conversation back', () => {
    renderPane();
    apply('session_ready', { state: 'idle' }, 1);
    dropped(1_240, false);
    expect(screen.getByTestId('conversation-history-dropped')).toBeInTheDocument();

    apply('conversation_snapshot', {
      epoch: 'e2',
      items: [{ kind: 'message', id: 'r1', role: 'assistant', text: 'rebuilt', streaming: false }],
      has_more: false,
      dropped: 0,
      running: false,
      queue: { steering: [], followUp: [] },
    }, 3);

    expect(screen.queryByTestId('conversation-history-dropped')).toBeNull();
  });
});
