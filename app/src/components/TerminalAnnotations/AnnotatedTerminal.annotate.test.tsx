
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, createEvent, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { AnnotatedTerminal, type SessionAnnotationApi } from './AnnotatedTerminal';
import type {
  AnnotatableMessage,
  MessageAnchor,
  TerminalAnnotation,
  TerminalAnnotationStore,
} from '../../utils/terminalAnnotations';

interface CapturedProps {
  annotations?: TerminalAnnotationStore;
  onAnnotationAnchor?: (anchor: MessageAnchor, at: { clientX: number; clientY: number }) => void;
  onAnnotationMiss?: (
    reason: 'no-messages' | 'outside-messages',
    at: { clientX: number; clientY: number },
  ) => void;
  onAnnotationActivate?: (annotationId: string, at: { clientX: number; clientY: number }) => void;
}

let terminal: CapturedProps = {};
let terminalFocusCalls = 0;
let terminalBounds: DOMRect | null = null;

vi.mock('../GhosttyTerminal', () => ({
  GhosttyTerminal: React.forwardRef(function MockTerminal(props: CapturedProps, ref: React.Ref<unknown>) {
    terminal = props;
    React.useImperativeHandle(ref, () => ({
      focus: () => {
        terminalFocusCalls += 1;
        return true;
      },
      getBounds: () => terminalBounds,
    }), []);
    return <div data-testid="terminal" />;
  }),
}));

const TURN_1 = 'The parser already handles CRLF, so the retry wrapper is safe to land as is.';
const TURN_2 = 'Added the retry test and pushed; CI is green on the second run.';

// Mirrors internal/store/annotation_drafts.go.
class FakeAnnotationDaemon implements SessionAnnotationApi {
  messages: AnnotatableMessage[] = [{ key: 'turn-1', markdown: TURN_1 }];
  truncated = false;
  annotations: TerminalAnnotation[] = [];
  note = '';
  generation = 0;
  tombstone = 0;
  calls = { fetchMessages: 0, fetchAnnotations: 0, saveAnnotations: 0, clearAnnotations: 0 };
  stealNextSave: TerminalAnnotation[] | null = null;
  submitted: string[] = [];
  nextSubmitStatus: 'delivered' | 'skipped_pending_approval' | 'error' = 'delivered';
  submitRejection: Error | null = null;
  releaseSubmit: (() => void) | null = null;
  messageListeners = new Set<() => void>();

  subscribeMessagesChanged = (_sessionId: string, listener: () => void) => {
    this.messageListeners.add(listener);
    return () => this.messageListeners.delete(listener);
  };

  notifyMessagesChanged() {
    for (const listener of this.messageListeners) listener();
  }

  submitAnnotations = async (_sessionId: string, text: string) => {
    this.submitted.push(text);
    if (this.releaseSubmit !== null) {
      await new Promise<void>((resolve) => {
        this.releaseSubmit = () => {
          this.releaseSubmit = null;
          resolve();
        };
      });
    }
    if (this.submitRejection) throw this.submitRejection;
    return { status: this.nextSubmitStatus };
  };

  fetchMessages: SessionAnnotationApi['fetchMessages'] = async (_sessionId: string) => {
    this.calls.fetchMessages += 1;
    return { messages: this.messages.map((message) => ({ ...message })), status: 'ready' as const, truncated: this.truncated };
  };

  fetchAnnotations = async (_sessionId: string) => {
    this.calls.fetchAnnotations += 1;
    return {
      annotations: this.annotations.map((annotation) => ({ ...annotation })),
      note: this.note,
      generation: this.generation,
    };
  };

  saveAnnotations = async (
    _sessionId: string,
    annotations: readonly TerminalAnnotation[],
    note: string,
    generation: number,
  ) => {
    this.calls.saveAnnotations += 1;
    if (this.stealNextSave) {
      this.annotations = this.stealNextSave.map((annotation) => ({ ...annotation }));
      this.generation = Math.max(this.generation, generation) + 1;
      this.stealNextSave = null;
      return { stale: true };
    }
    if (generation <= this.generation || generation <= this.tombstone) return { stale: true };
    this.annotations = annotations.map((annotation) => ({ ...annotation }));
    this.note = note;
    this.generation = generation;
    return { stale: false };
  };

  clearAnnotations = async (_sessionId: string, generation: number) => {
    this.calls.clearAnnotations += 1;
    if (generation > this.tombstone) this.tombstone = generation;
    if (generation > this.generation) {
      this.annotations = [];
      this.note = '';
      this.generation = generation;
    }
    return { generation: this.generation };
  };
}

function props(overrides: {
  api?: SessionAnnotationApi;
  paneActive?: boolean;
}) {
  return {
    sessionId: 'session-1',
    annotationApi: overrides.api,
    paneActive: overrides.paneActive ?? false,
    fontSize: 13,
    debugName: 'test',
    onInput: () => {},
    onReady: () => {},
    onResize: () => {},
  };
}

function renderTerminal(overrides: {
  api?: FakeAnnotationDaemon;
  paneActive?: boolean;
} = {}) {
  const daemon = overrides.api ?? new FakeAnnotationDaemon();
  const view = render(<AnnotatedTerminal {...props({ ...overrides, api: daemon })} />);
  const rerender = (next: { paneActive?: boolean } = {}) =>
    view.rerender(
      <AnnotatedTerminal {...props({
        paneActive: next.paneActive ?? overrides.paneActive,
        api: daemon,
      })} />,
    );
  return { ...view, rerender, daemon };
}

async function windowReady(...keys: string[]) {
  await waitFor(() => expect(terminal.annotations?.messageKeys()).toEqual(keys));
}

function anchor(messageKey: string, start: number, end: number) {
  const quote = terminal.annotations?.markdownFor(messageKey)?.slice(start, end) ?? '';
  act(() => {
    terminal.onAnnotationAnchor?.({ messageKey, start, end, quote }, { clientX: 120, clientY: 200 });
  });
}

function miss(reason: 'no-messages' | 'outside-messages') {
  act(() => {
    terminal.onAnnotationMiss?.(reason, { clientX: 120, clientY: 200 });
  });
}

function notice(): string | null {
  return screen.queryByTestId('annotation-notice')?.textContent ?? null;
}

function activate(annotationId: string) {
  act(() => {
    terminal.onAnnotationActivate?.(annotationId, { clientX: 140, clientY: 220 });
  });
}

function stored() {
  return terminal.annotations?.list() ?? [];
}

function openLabelPicker() {
  fireEvent.click(screen.getByLabelText('More labels'));
  return document.querySelector<HTMLElement>('.md-quick-label-picker')!;
}

function pickLabel(text: string) {
  const promoted = screen.queryByLabelText(text);
  if (promoted) {
    fireEvent.click(promoted);
    return;
  }
  if (!document.querySelector('.md-quick-label-picker')) openLabelPicker();
  const picker = document.querySelector('.md-quick-label-picker')!;
  const row = Array.from(picker.querySelectorAll<HTMLElement>('.md-quick-label-row'))
    .find((candidate) => candidate.querySelector('.md-quick-label-text')?.textContent === text);
  expect(row, `grouped quick label "${text}"`).toBeDefined();
  fireEvent.click(row!);
}

function card(index = 0) {
  const cards = document.querySelectorAll('.anno-card');
  const node = cards[index] as HTMLElement;
  return {
    node,
    open: node.querySelector('.anno-card-open') as HTMLElement,
    remove: node.querySelector('.anno-card-remove') as HTMLElement,
  };
}

beforeEach(() => {
  terminal = {};
  terminalFocusCalls = 0;
  terminalBounds = null;
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('AnnotatedTerminal', () => {
  it('hands the terminal the current annotatable window on mount', async () => {
    const { daemon } = renderTerminal();

    await windowReady('turn-1');
    expect(terminal.annotations?.markdownFor('turn-1')).toBe(TURN_1);
    expect(daemon.calls.fetchMessages).toBe(1);
  });

  it('refreshes completed commentary while the agent remains working', async () => {
    const daemon = new FakeAnnotationDaemon();
    daemon.messages = [];
    renderTerminal({ api: daemon });
    await waitFor(() => expect(daemon.calls.fetchMessages).toBe(1));

    daemon.messages = [{ key: 'turn-1', markdown: TURN_1 }];
    act(() => daemon.notifyMessagesChanged());

    await windowReady('turn-1');
    expect(daemon.calls.fetchMessages).toBe(2);
  });

  it('does not let an older refresh replace a newer message window', async () => {
    const daemon = new FakeAnnotationDaemon();
    const resolveFetches: Array<(result: {
      messages: AnnotatableMessage[];
      status: 'discovering' | 'ready' | 'unavailable';
      truncated: boolean;
    }) => void> = [];
    daemon.fetchMessages = async () => new Promise((resolve) => resolveFetches.push(resolve));
    renderTerminal({ api: daemon });
    await waitFor(() => expect(resolveFetches).toHaveLength(1));

    act(() => daemon.notifyMessagesChanged());
    await waitFor(() => expect(resolveFetches).toHaveLength(2));
    await act(async () => {
      resolveFetches[1]({
        messages: [{ key: 'turn-2', markdown: TURN_2 }], status: 'ready', truncated: false,
      });
    });
    await windowReady('turn-2');

    await act(async () => {
      resolveFetches[0]({
        messages: [{ key: 'turn-1', markdown: TURN_1 }], status: 'ready', truncated: false,
      });
    });
    expect(terminal.annotations?.messageKeys()).toEqual(['turn-2']);
  });

  it('keeps annotations when a re-fetch returns the same window', async () => {
    const { daemon } = renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 4, 10);
    pickLabel('Show the receipt');
    expect(stored()).toHaveLength(1);

    act(() => daemon.notifyMessagesChanged());

    await waitFor(() => expect(daemon.calls.fetchMessages).toBe(2));
    expect(stored()).toHaveLength(1);
  });

  it('keeps a past turn annotated when a new turn arrives', async () => {
    const { daemon } = renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 4, 10);
    pickLabel('Show the receipt');
    const before = stored()[0];

    daemon.messages = [
      { key: 'turn-1', markdown: TURN_1 },
      { key: 'turn-2', markdown: TURN_2 },
    ];
    act(() => daemon.notifyMessagesChanged());

    await windowReady('turn-1', 'turn-2');
    expect(stored()).toHaveLength(1);
    expect(stored()[0]).toEqual(before);
    expect(screen.getByTestId('annotation-panel')).toBeTruthy();
  });

  it('keeps an annotation whose turn has scrolled out of the window', async () => {
    const { daemon } = renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 4, 10);
    pickLabel('Show the receipt');

    daemon.messages = [{ key: 'turn-2', markdown: TURN_2 }];
    act(() => daemon.notifyMessagesChanged());

    await windowReady('turn-2');
    expect(stored()).toHaveLength(1);
    expect(stored()[0].quote).toBe(TURN_1.slice(4, 10));
  });

  it('opens the label popup on an anchor and files the annotation on a pick', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    expect(screen.getByTestId('annotation-popup')).toBeTruthy();

    pickLabel('Verify this');

    expect(screen.queryByTestId('annotation-popup')).toBeNull();
    expect(screen.getByTestId('annotation-panel')).toBeTruthy();
    expect(stored()[0]?.quickLabelId).toBe('verify-this');
    expect(stored()[0]?.quote).toBe(TURN_1.slice(0, 26));
  });

  it('renders the promoted trio and keeps their selected-state toggle behavior', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    const popup = screen.getByTestId('annotation-popup');
    expect(Array.from(popup.querySelectorAll('.anno-popup-label'), (button) => button.getAttribute('aria-label')))
      .toEqual([
        'I agree',
        'This is wrong',
        'Clarify this',
        'More labels',
        'Write a comment',
        'Remove this annotation',
      ]);

    pickLabel('I agree');
    expect(stored()[0]?.quickLabelId).toBe('i-agree');

    activate(stored()[0].id);
    expect(screen.getByLabelText('I agree').classList.contains('anno-popup-label--on')).toBe(true);
    pickLabel('I agree');
    expect(stored()).toHaveLength(0);
  });

  it('shows a selected grouped label on the trigger', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    activate(stored()[0].id);

    const trigger = screen.getByLabelText('More labels');
    expect(trigger.textContent).toBe('🔍');
    expect(trigger.classList.contains('anno-popup-label--on')).toBe(true);
  });

  it('closes only the grouped picker on Escape', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    openLabelPicker();
    expect(document.querySelector('.md-quick-label-picker')).not.toBeNull();

    fireEvent.keyDown(window, { key: 'Escape' });

    expect(document.querySelector('.md-quick-label-picker')).toBeNull();
    expect(screen.getByTestId('annotation-popup')).toBeTruthy();
    expect(stored()).toHaveLength(1);
  });

  it('toggles the grouped picker from its trigger', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    openLabelPicker();
    expect(document.querySelector('.md-quick-label-picker')).not.toBeNull();

    fireEvent.click(screen.getByLabelText('More labels'));

    expect(document.querySelector('.md-quick-label-picker')).toBeNull();
    expect(screen.getByTestId('annotation-popup')).toBeTruthy();
  });

  it('applies the grouped picker digit shortcut and closes both layers', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    openLabelPicker();
    fireEvent.keyDown(window, { code: 'Digit3', key: '3' });

    expect(stored()[0]?.quickLabelId).toBe('verify-this');
    expect(document.querySelector('.md-quick-label-picker')).toBeNull();
    expect(screen.queryByTestId('annotation-popup')).toBeNull();
  });

  it('discards a highlight dismissed without a label or a comment', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    expect(stored()).toHaveLength(1);

    fireEvent.keyDown(window, { key: 'Escape' });

    expect(stored()).toHaveLength(0);
    expect(screen.queryByTestId('annotation-popup')).toBeNull();
  });

  it('carries a written comment onto the annotation', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    fireEvent.change(screen.getByPlaceholderText('What should change here?'), {
      target: { value: 'CRLF is handled downstream, not here.' },
    });
    fireEvent.click(screen.getByText('Comment'));

    expect(stored()[0]?.comment).toBe('CRLF is handled downstream, not here.');
    expect(screen.queryByTestId('annotation-popup')).toBeNull();
  });

  it('sends the whole set to the session and clears it', async () => {
    const { daemon } = renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    anchor('turn-1', 31, 55);
    pickLabel('Show the receipt');

    fireEvent.click(screen.getByText('Send all'));

    await waitFor(() => expect(screen.getByText(/sent 2 to the session/)).toBeTruthy());
    expect(daemon.submitted).toHaveLength(1);
    expect(daemon.submitted[0]).toContain(TURN_1.slice(0, 26));
    expect(daemon.submitted[0]).toContain(TURN_1.slice(31, 55));
    expect(stored()).toHaveLength(0);
  });

  it('reopens a reaction from the message, and lets it be changed', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');

    activate(stored()[0].id);
    expect(screen.getByTestId('annotation-popup')).toBeTruthy();

    pickLabel('Show the receipt');

    expect(stored()).toHaveLength(1);
    expect(stored()[0].quickLabelId).toBe('show-the-receipt');
  });

  it('reopens a comment straight into its editor, prefilled', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    fireEvent.change(screen.getByPlaceholderText('What should change here?'), {
      target: { value: 'first take' },
    });
    fireEvent.click(screen.getByText('Comment'));

    activate(stored()[0].id);

    const box = screen.getByPlaceholderText('What should change here?') as HTMLTextAreaElement;
    expect(box.value).toBe('first take');

    fireEvent.change(box, { target: { value: 'second take' } });
    fireEvent.click(screen.getByText('Comment'));
    expect(stored()[0].comment).toBe('second take');
  });

  it('removes a reopened annotation from its own popup', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    activate(stored()[0].id);

    fireEvent.click(screen.getByLabelText('Remove this annotation'));

    expect(stored()).toHaveLength(0);
    expect(screen.queryByTestId('annotation-popup')).toBeNull();
  });

  it('drops a reaction toggled back off rather than leaving a blank wash', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    activate(stored()[0].id);

    pickLabel('Verify this');

    expect(stored()).toHaveLength(0);
  });

  it('discards a bare highlight when the press lands outside the popup', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    expect(stored()).toHaveLength(1);

    fireEvent.pointerDown(screen.getByTestId('terminal'));

    expect(screen.queryByTestId('annotation-popup')).toBeNull();
    expect(stored()).toHaveLength(0);
  });

  it('keeps the popup open for a press inside it', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.pointerDown(screen.getByLabelText('This is wrong'));

    expect(screen.getByTestId('annotation-popup')).toBeTruthy();
  });

  it('keeps a comment draft open until it is explicitly closed', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    const box = screen.getByPlaceholderText('What should change here?') as HTMLTextAreaElement;
    fireEvent.change(box, { target: { value: 'keep this in app memory' } });

    fireEvent.pointerDown(screen.getByTestId('terminal'));
    anchor('turn-1', 31, 55);

    expect(screen.getByTestId('annotation-popup')).toBeTruthy();
    expect(box.value).toBe('keep this in app memory');
    expect(stored()).toHaveLength(1);

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByTestId('annotation-popup')).toBeNull();
    expect(stored()).toHaveLength(0);
  });

  it('lets the comment box regain focus and focuses it from the popup background', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    const popup = screen.getByTestId('annotation-popup');
    const box = screen.getByPlaceholderText('What should change here?') as HTMLTextAreaElement;
    box.blur();

    const boxPress = createEvent.mouseDown(box);
    fireEvent(box, boxPress);
    expect(boxPress.defaultPrevented).toBe(false);

    const quote = popup.querySelector('.anno-popup-quote')!;
    fireEvent.mouseDown(quote);
    fireEvent.mouseUp(quote);
    expect(document.activeElement).toBe(box);
  });

  it('moves the comment popup with its handle and keeps the manual position', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    const popup = screen.getByTestId('annotation-popup');

    fireEvent.mouseDown(screen.getByTestId('annotation-popup-drag-handle'), { clientX: 100, clientY: 100 });
    fireEvent.mouseMove(window, { clientX: 260, clientY: 340 });
    fireEvent.mouseUp(window);

    expect(popup.style.left).toBe('160px');
    expect(popup.style.top).toBe('240px');

    fireEvent.click(screen.getByLabelText('I agree'));
    expect(screen.getByTestId('annotation-popup')).toBe(popup);
    expect(popup.style.left).toBe('160px');
    expect(popup.style.top).toBe('240px');
  });

  it('keeps a dragged comment popup inside its terminal pane', async () => {
    terminalBounds = DOMRect.fromRect({ x: 300, y: 200, width: 500, height: 400 });
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    const popup = screen.getByRole('dialog', { name: 'Edit terminal annotation' });
    vi.spyOn(popup, 'getBoundingClientRect').mockReturnValue(
      DOMRect.fromRect({ x: 360, y: 260, width: 200, height: 120 }),
    );

    fireEvent.mouseDown(screen.getByRole('button', { name: 'Move comment editor with arrow keys' }), {
      clientX: 400,
      clientY: 280,
    });
    fireEvent.mouseMove(window, { clientX: 1200, clientY: 900 });
    fireEvent.mouseUp(window);

    expect(popup.style.left).toBe('592px');
    expect(popup.style.top).toBe('472px');
  });

  it('moves the comment popup with the keyboard and keeps it inside its terminal pane', async () => {
    terminalBounds = DOMRect.fromRect({ x: 300, y: 200, width: 500, height: 400 });
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    const popup = screen.getByRole('dialog', { name: 'Edit terminal annotation' });
    vi.spyOn(popup, 'getBoundingClientRect').mockReturnValue(
      DOMRect.fromRect({ x: 360, y: 260, width: 200, height: 120 }),
    );
    const handle = screen.getByRole('button', { name: 'Move comment editor with arrow keys' });

    const initialLeft = Number.parseFloat(popup.style.left);
    fireEvent.keyDown(handle, { key: 'ArrowRight' });
    expect(Number.parseFloat(popup.style.left)).toBe(initialLeft + 10);
    const initialTop = Number.parseFloat(popup.style.top);
    fireEvent.keyDown(handle, { key: 'ArrowDown', shiftKey: true });
    expect(Number.parseFloat(popup.style.top)).toBe(Math.min(initialTop + 40, 472));
    for (let step = 0; step < 10; step += 1) {
      fireEvent.keyDown(handle, { key: 'ArrowRight', shiftKey: true });
    }
    expect(popup.style.left).toBe('592px');
  });

  it('moves the panel with a drag on its header', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    const panel = screen.getByTestId('annotation-panel');
    expect(panel.style.left).toBe('');

    fireEvent.mouseDown(panel.querySelector('.anno-panel-head')!, { clientX: 100, clientY: 100 });
    fireEvent.mouseMove(window, { clientX: 260, clientY: 340 });
    fireEvent.mouseUp(window);

    expect(panel.style.left).toBe('160px');
    expect(panel.style.top).toBe('240px');
    fireEvent.mouseMove(window, { clientX: 700, clientY: 700 });
    expect(panel.style.left).toBe('160px');
  });

  it('removes an annotation from the panel', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    fireEvent.click(screen.getByLabelText('Remove annotation'));

    expect(stored()).toHaveLength(0);
    expect(screen.queryByTestId('annotation-panel')).toBeNull();
  });

  it('keeps the panel row\'s remove control beside the row, not below it', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    fireEvent.change(screen.getByPlaceholderText('What should change here?'), {
      target: { value: 'long enough to wrap onto its own line in the row' },
    });
    fireEvent.click(screen.getByText('Comment'));

    const { node, open, remove } = card();
    expect(open).toBeTruthy();
    expect(remove.parentElement).toBe(node);
    expect(open.querySelector('.anno-card-comment')).toBeTruthy();
  });

  it('opens the editor when a panel row is clicked, even for a bare reaction', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');

    fireEvent.click(card().open);

    const box = screen.getByPlaceholderText('What should change here?') as HTMLTextAreaElement;
    expect(box.value).toBe('');
    expect(document.activeElement).toBe(box);
  });

  it('puts the caret after a prefilled comment rather than at its start', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    fireEvent.change(screen.getByPlaceholderText('What should change here?'), {
      target: { value: 'first take' },
    });
    fireEvent.click(screen.getByText('Comment'));

    fireEvent.click(card().open);

    const box = screen.getByPlaceholderText('What should change here?') as HTMLTextAreaElement;
    expect(document.activeElement).toBe(box);
    expect(box.selectionStart).toBe('first take'.length);
  });

  it('removes the annotation from the open editor by name', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    fireEvent.change(screen.getByPlaceholderText('What should change here?'), {
      target: { value: 'wrong on reflection' },
    });
    fireEvent.click(screen.getByText('Comment'));

    fireEvent.click(card().open);
    fireEvent.click(screen.getByText('Remove'));

    expect(stored()).toHaveLength(0);
    expect(screen.queryByTestId('annotation-popup')).toBeNull();
  });

  it('hands the keyboard back to the terminal when the editor closes', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    const box = screen.getByPlaceholderText('What should change here?');
    expect(document.activeElement).toBe(box);
    fireEvent.change(box, { target: { value: 'say this' } });

    terminalFocusCalls = 0;
    fireEvent.click(screen.getByText('Comment'));
    expect(terminalFocusCalls).toBe(1);

    fireEvent.click(card().open);
    terminalFocusCalls = 0;
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(terminalFocusCalls).toBe(1);
  });

  it('leaves focus alone when the press that closed the popup landed elsewhere', async () => {
    renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    anchor('turn-1', 31, 55);
    pickLabel('Show the receipt');

    fireEvent.click(card(0).open);
    terminalFocusCalls = 0;
    fireEvent.mouseDown(card(1).open);

    expect(terminalFocusCalls).toBe(0);
  });

  it('sends the set on the send shortcut while the pane holds focus', async () => {
    const { daemon } = renderTerminal({ paneActive: true });
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');

    fireEvent.keyDown(window, { key: 'Enter', metaKey: true });

    await waitFor(() => expect(stored()).toHaveLength(0));
    expect(daemon.submitted).toHaveLength(1);
    expect(daemon.submitted[0]).toContain(TURN_1.slice(0, 26));
  });

  it('commits the comment being typed when the send shortcut fires', async () => {
    const { daemon } = renderTerminal({ paneActive: true });
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    fireEvent.click(screen.getByLabelText('Write a comment'));
    fireEvent.change(screen.getByPlaceholderText('What should change here?'), {
      target: { value: 'still typing this' },
    });

    fireEvent.keyDown(window, { key: 'Enter', metaKey: true });

    await waitFor(() => expect(daemon.submitted).toHaveLength(1));
    expect(daemon.submitted[0]).toContain('still typing this');
  });

  it('leaves the send keystroke to the PTY when the pane is not the focused one', async () => {
    const { daemon } = renderTerminal({ paneActive: false });
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');

    fireEvent.keyDown(window, { key: 'Enter', metaKey: true });

    expect(daemon.submitted).toEqual([]);
    expect(stored()).toHaveLength(1);
  });

  it('leaves the send keystroke to the PTY when there is nothing to send', async () => {
    const { daemon, rerender } = renderTerminal({ paneActive: true });
    await windowReady('turn-1');
    rerender({ paneActive: true });

    fireEvent.keyDown(window, { key: 'Enter', metaKey: true });

    expect(daemon.submitted).toEqual([]);
  });

  it('offers no annotation surface without a daemon to hold the marks', async () => {
    render(<AnnotatedTerminal {...props({ api: undefined })} />);

    await act(async () => {});
    expect(terminal.annotations).toBeUndefined();
    expect(terminal.onAnnotationAnchor).toBeUndefined();
  });
});

describe('AnnotatedTerminal label hint', () => {
  function hint(): string {
    return screen.getByTestId('annotation-popup-hint').textContent ?? '';
  }

  it('names a grouped picker row while the pointer is on it', async () => {
    renderTerminal();
    await windowReady('turn-1');
    anchor('turn-1', 0, 26);

    openLabelPicker();
    fireEvent.mouseEnter(screen.getByText('Show the receipt').closest('button')!);

    expect(hint()).toBe('Show the receipt');
  });

  it('goes back to naming the mark that is already on this annotation', async () => {
    renderTerminal();
    await windowReady('turn-1');
    anchor('turn-1', 0, 26);
    pickLabel('Show the receipt');
    activate(stored()[0].id);

    expect(hint()).toBe('Show the receipt');
    fireEvent.mouseEnter(screen.getByLabelText('This is wrong'));
    expect(hint()).toBe('This is wrong');
    fireEvent.mouseLeave(screen.getByLabelText('This is wrong'));

    expect(hint()).toBe('Show the receipt');
  });

  it('says what to do when nothing is marked or hovered', async () => {
    renderTerminal();
    await windowReady('turn-1');
    anchor('turn-1', 0, 26);

    expect(hint()).toBe('Pick a label, or write a comment');
  });
});

describe('AnnotatedTerminal note', () => {
  function noteBox(): HTMLTextAreaElement {
    return screen.getByTestId('annotation-note') as HTMLTextAreaElement;
  }

  function writeNote(text: string) {
    fireEvent.change(noteBox(), { target: { value: text } });
  }

  it('has nowhere to be written until something is marked', async () => {
    renderTerminal();
    await windowReady('turn-1');

    expect(screen.queryByTestId('annotation-note')).toBeNull();
  });

  it('is written through to the daemon on a pause in typing', async () => {
    const { daemon } = renderTerminal();
    await windowReady('turn-1');
    anchor('turn-1', 0, 26);
    pickLabel('Verify this');

    writeNote('Split this into two PRs.');

    await waitFor(() => expect(daemon.note).toBe('Split this into two PRs.'));
  });

  it('survives the pane it was typed in', async () => {
    const daemon = new FakeAnnotationDaemon();
    const first = renderTerminal({ api: daemon });
    await windowReady('turn-1');
    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    writeNote('Split this into two PRs.');
    await waitFor(() => expect(daemon.note).toBe('Split this into two PRs.'));
    first.unmount();

    renderTerminal({ api: daemon });

    await waitFor(() => expect(noteBox().value).toBe('Split this into two PRs.'));
  });

  it('goes out ahead of the marks in one keystroke', async () => {
    const { daemon } = renderTerminal({ paneActive: true });
    await windowReady('turn-1');
    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    writeNote('Split this into two PRs.');

    fireEvent.keyDown(noteBox(), { key: 'Enter', metaKey: true });

    await waitFor(() => expect(daemon.submitted).toHaveLength(1));
    const payload = daemon.submitted[0];
    expect(payload.indexOf('Split this into two PRs.')).toBeLessThan(payload.indexOf('## 1.'));
  });

  it('is spent by the send that delivered it', async () => {
    const { daemon } = renderTerminal({ paneActive: true });
    await windowReady('turn-1');
    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    writeNote('Split this into two PRs.');
    await waitFor(() => expect(daemon.note).toBe('Split this into two PRs.'));

    fireEvent.click(screen.getByText('Send all'));

    await waitFor(() => expect(daemon.submitted).toHaveLength(1));
    await waitFor(() => expect(daemon.note).toBe(''));
  });

  it('is kept on the daemon when it was typed over while the send was in flight', async () => {
    const { daemon } = renderTerminal({ paneActive: true });
    daemon.releaseSubmit = () => {};
    await windowReady('turn-1');
    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    writeNote('Split this into two PRs.');
    await waitFor(() => expect(daemon.note).toBe('Split this into two PRs.'));

    fireEvent.click(screen.getByText('Send all'));
    await waitFor(() => expect(daemon.submitted).toHaveLength(1));

    writeNote('Split this into two PRs, smallest first.');
    await waitFor(() => expect(daemon.note).toBe('Split this into two PRs, smallest first.'));

    await act(async () => {
      daemon.releaseSubmit?.();
    });

    await waitFor(() => expect(daemon.annotations).toHaveLength(0));
    expect(noteBox().value).toBe('Split this into two PRs, smallest first.');
    await waitFor(() => expect(daemon.note).toBe('Split this into two PRs, smallest first.'));
  });

  it('is kept by a send the session refused', async () => {
    const { daemon } = renderTerminal({ paneActive: true });
    daemon.nextSubmitStatus = 'skipped_pending_approval';
    await windowReady('turn-1');
    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    writeNote('Split this into two PRs.');

    fireEvent.click(screen.getByText('Send all'));

    await waitFor(() => expect(screen.getByTestId('annotation-send-note')).toBeTruthy());
    expect(noteBox().value).toBe('Split this into two PRs.');
    await waitFor(() => expect(daemon.note).toBe('Split this into two PRs.'));
  });
});

describe('AnnotatedTerminal sending', () => {
  it('keeps the marks and says why when the session is on an approval prompt', async () => {
    const { daemon } = renderTerminal();
    daemon.nextSubmitStatus = 'skipped_pending_approval';
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    fireEvent.click(screen.getByText('Send all'));

    await waitFor(() => expect(screen.getByTestId('annotation-send-note')).toBeTruthy());
    expect(screen.getByTestId('annotation-send-note').textContent).toMatch(/waiting on an approval/i);
    expect(daemon.submitted).toHaveLength(1);
    expect(stored()).toHaveLength(1);
    expect(screen.getByText('Send all')).toBeTruthy();
  });

  it('keeps the marks and shows the failure when delivery fails', async () => {
    const { daemon } = renderTerminal();
    daemon.submitRejection = new Error('Session annotation send timed out');
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    fireEvent.click(screen.getByText('Send all'));

    await waitFor(() => expect(screen.getByTestId('annotation-send-note')).toBeTruthy());
    expect(screen.getByTestId('annotation-send-note').textContent).toContain('timed out');
    expect(stored()).toHaveLength(1);
    expect(daemon.calls.clearAnnotations).toBe(0);
  });

  it('refuses a second send while the first is still in flight', async () => {
    const { daemon } = renderTerminal({ paneActive: true });
    daemon.releaseSubmit = () => {};
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    fireEvent.click(screen.getByText('Send all'));

    await waitFor(() => expect(screen.getByText('Sending…')).toBeTruthy());
    fireEvent.keyDown(window, { key: 'Enter', metaKey: true });
    fireEvent.click(screen.getByText('Sending…'));

    expect(daemon.submitted).toHaveLength(1);

    await act(async () => {
      daemon.releaseSubmit?.();
    });
    await waitFor(() => expect(stored()).toHaveLength(0));
  });

  it('keeps an annotation made while an earlier send was in flight', async () => {
    const { daemon } = renderTerminal();
    daemon.releaseSubmit = () => {};
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    const sentId = stored()[0].id;

    fireEvent.click(screen.getByText('Send all'));
    await waitFor(() => expect(daemon.submitted).toHaveLength(1));
    anchor('turn-1', 31, 55);
    pickLabel('Show the receipt');
    const keptId = stored().find((entry) => entry.id !== sentId)!.id;

    await act(async () => {
      daemon.releaseSubmit?.();
    });

    await waitFor(() => expect(stored().map((entry) => entry.id)).toEqual([keptId]));
    expect(daemon.submitted[0]).toContain(TURN_1.slice(0, 26));
    expect(daemon.submitted[0]).not.toContain(TURN_1.slice(31, 55));
    await waitFor(() => expect(daemon.annotations.map((entry) => entry.id)).toEqual([keptId]));
    expect(daemon.calls.clearAnnotations).toBe(0);
    expect(screen.getByTestId('annotation-send-note').textContent).toMatch(/1 still here/);
    expect(screen.getByText('Send all')).toBeTruthy();
  });

  it('keeps an annotation edited while the send carrying it was in flight', async () => {
    const { daemon } = renderTerminal();
    daemon.releaseSubmit = () => {};
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    const id = stored()[0].id;

    fireEvent.click(screen.getByText('Send all'));
    await waitFor(() => expect(daemon.submitted).toHaveLength(1));
    expect(daemon.submitted[0]).toContain('🔍 Verify this');

    activate(id);
    pickLabel('Show the receipt');

    await act(async () => {
      daemon.releaseSubmit?.();
    });

    await waitFor(() => expect(screen.getByTestId('annotation-send-note')).toBeTruthy());
    expect(stored()).toHaveLength(1);
    expect(stored()[0].id).toBe(id);
    expect(stored()[0].quickLabelId).toBe('show-the-receipt');
    await waitFor(() => expect(daemon.annotations.map((entry) => entry.quickLabelId)).toEqual(['show-the-receipt']));
    expect(daemon.calls.clearAnnotations).toBe(0);
  });

  it('spends a mark the send carried unchanged, even beside an edited one', async () => {
    const { daemon } = renderTerminal();
    daemon.releaseSubmit = () => {};
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    const editedId = stored()[0].id;
    anchor('turn-1', 31, 55);
    pickLabel('Show the receipt');

    fireEvent.click(screen.getByText('Send all'));
    await waitFor(() => expect(daemon.submitted).toHaveLength(1));

    activate(editedId);
    fireEvent.click(screen.getByLabelText('This is wrong'));

    await act(async () => {
      daemon.releaseSubmit?.();
    });

    await waitFor(() => expect(stored().map((entry) => entry.id)).toEqual([editedId]));
    expect(stored()[0].quickLabelId).toBe('this-is-wrong');
  });

  it('tombstones the daemon draft only once the send is delivered', async () => {
    const { daemon } = renderTerminal();
    daemon.releaseSubmit = () => {};
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    await waitFor(() => expect(daemon.annotations).toHaveLength(1));

    fireEvent.click(screen.getByText('Send all'));
    await waitFor(() => expect(daemon.submitted).toHaveLength(1));
    expect(daemon.annotations).toHaveLength(1);
    expect(daemon.calls.clearAnnotations).toBe(0);

    await act(async () => {
      daemon.releaseSubmit?.();
    });
    await waitFor(() => expect(daemon.calls.clearAnnotations).toBe(1));
    expect(daemon.annotations).toHaveLength(0);
  });
});

describe('AnnotatedTerminal persistence', () => {
  it('writes every mutation through to the daemon as it happens', async () => {
    const { daemon } = renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    await waitFor(() => expect(daemon.annotations).toHaveLength(1));
    expect(daemon.annotations[0].quickLabelId).toBe('verify-this');
    expect(daemon.annotations[0].quote).toBe(TURN_1.slice(0, 26));

    fireEvent.click(screen.getByLabelText('Remove annotation'));
    await waitFor(() => expect(daemon.annotations).toHaveLength(0));
  });

  it('shows what an earlier app run left behind, before anything is drawn', async () => {
    const daemon = new FakeAnnotationDaemon();
    daemon.annotations = [{
      id: 'stored-1',
      messageKey: 'turn-1',
      start: 4,
      end: 10,
      quote: TURN_1.slice(4, 10),
      quickLabelId: 'clarify-this',
      comment: 'why this?',
    }];
    daemon.generation = 7;

    renderTerminal({ api: daemon });

    await waitFor(() => expect(stored()).toHaveLength(1));
    expect(stored()[0].comment).toBe('why this?');
    expect(screen.getByTestId('annotation-panel')).toBeTruthy();
  });

  it('survives the pane being unmounted and mounted again', async () => {
    const daemon = new FakeAnnotationDaemon();
    const first = renderTerminal({ api: daemon });
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    await waitFor(() => expect(daemon.annotations).toHaveLength(1));

    first.unmount();
    terminal = {};
    renderTerminal({ api: daemon });

    await waitFor(() => expect(stored()).toHaveLength(1));
    expect(stored()[0].quickLabelId).toBe('verify-this');
    expect(stored()[0].quote).toBe(TURN_1.slice(0, 26));
  });

  it('takes the daemon\'s list when a save is refused as stale', async () => {
    const { daemon } = renderTerminal();
    await windowReady('turn-1');

    daemon.stealNextSave = [{
      id: 'theirs-1',
      messageKey: 'turn-1',
      start: 31,
      end: 55,
      quote: TURN_1.slice(31, 55),
      quickLabelId: 'show-the-receipt',
      comment: '',
    }];

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');

    await waitFor(() => expect(stored().map((entry) => entry.id)).toEqual(['theirs-1']));

    fireEvent.click(screen.getByLabelText('Remove annotation'));
    await waitFor(() => expect(daemon.annotations).toHaveLength(0));
  });

  it('tombstones the set it typed into the session', async () => {
    const { daemon } = renderTerminal();
    await windowReady('turn-1');

    anchor('turn-1', 0, 26);
    pickLabel('Verify this');
    await waitFor(() => expect(daemon.annotations).toHaveLength(1));

    fireEvent.click(screen.getByText('Send all'));

    await waitFor(() => expect(daemon.annotations).toHaveLength(0));
    expect(daemon.tombstone).toBeGreaterThan(0);
    const late = await daemon.saveAnnotations('session-1', [{
      id: 'in-flight',
      messageKey: 'turn-1',
      start: 0,
      end: 26,
      quote: TURN_1.slice(0, 26),
      quickLabelId: 'verify-this',
      comment: '',
    }], '', daemon.tombstone);
    expect(late.stale).toBe(true);
    expect(daemon.annotations).toHaveLength(0);
  });

  describe('when a gesture cannot be annotated', () => {
    it('says the text was not the agent’s when there is a window it missed', async () => {
      renderTerminal();
      await windowReady('turn-1');

      miss('outside-messages');

      expect(notice()).toContain('Only what the agent wrote can be annotated');
    });

    it('names the unreadable transcript rather than blaming the selection', async () => {
      const daemon = new FakeAnnotationDaemon();
      const fetch = vi.fn(async () => ({
        messages: [],
        status: 'unavailable' as const,
        detail: 'No exact transcript is available for this session.',
        truncated: false,
      }));
      daemon.fetchMessages = fetch;
      renderTerminal({ api: daemon });
      await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));

      miss('no-messages');

      expect(notice()).toContain('No exact transcript is available');
    });

    it('says the agent has not spoken when the window came back empty', async () => {
      const daemon = new FakeAnnotationDaemon();
      daemon.messages = [];
      renderTerminal({ api: daemon });
      await waitFor(() => expect(daemon.calls.fetchMessages).toBe(1));

      miss('no-messages');

      expect(notice()).toContain('has not written a message');
    });

    it('describes transcript startup as discovering rather than broken', async () => {
      const daemon = new FakeAnnotationDaemon();
      const fetch = vi.fn(async () => ({ messages: [], status: 'discovering' as const, truncated: false }));
      daemon.fetchMessages = fetch;
      renderTerminal({ api: daemon });
      await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));

      miss('no-messages');

      expect(notice()).toContain('still being recorded');
    });

    it('clears itself once an annotation actually lands', async () => {
      renderTerminal();
      await windowReady('turn-1');
      miss('outside-messages');
      expect(notice()).not.toBeNull();

      anchor('turn-1', 0, 26);

      expect(notice()).toBeNull();
      expect(screen.getByTestId('annotation-popup')).toBeTruthy();
    });

    it('goes away on its own rather than becoming something to dismiss', async () => {
      vi.useFakeTimers();
      try {
        renderTerminal();
        await act(async () => {
          await vi.advanceTimersByTimeAsync(0);
        });
        miss('outside-messages');
        expect(notice()).not.toBeNull();

        act(() => {
          vi.advanceTimersByTime(5000);
        });

        expect(notice()).toBeNull();
      } finally {
        vi.useRealTimers();
      }
    });
  });
});
