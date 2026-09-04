import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { attachTerminalInput } from '../ghostty/input';
import { noteTerminalInputReceipt, noteTerminalInputTransport, observeTerminalInput } from './terminalInputDiagnostics';

const { recordDiag } = vi.hoisted(() => ({ recordDiag: vi.fn() }));
vi.mock('./terminalDiagnosticsLog', () => ({ recordDiag }));

const disposers: Array<() => void> = [];

function setup(runtimeId: string, active = true) {
  const element = document.createElement('div');
  element.className = 'terminal-container';
  document.body.append(element);
  const diagnostics = observeTerminalInput(element, () => ({
    runtimeId, sessionId: `session-${runtimeId}`, paneId: `pane-${runtimeId}`,
    active, ready: true, model: true, lastWriteAt: 10, lastPaintAt: 20,
  }));
  const send = vi.fn();
  const detach = attachTerminalInput({
    element,
    terminal: () => ({ encodeKey: (key) => key.utf8 ?? '', formatPaste: (text) => text }),
    send, interceptKey: () => false, onError: vi.fn(), onDiagnostic: diagnostics.record,
  });
  disposers.push(() => { detach(); diagnostics.dispose(); element.remove(); });
  return { element, send };
}

function type(element: HTMLElement, key = 'a') {
  element.dispatchEvent(new KeyboardEvent('keydown', {
    key, code: 'KeyA', bubbles: true, cancelable: true,
  }));
}

function records(runtimeId: string) {
  return recordDiag.mock.calls.map(([event]) => event).filter((event) => event.runtimeId === runtimeId);
}

function lastRecord(runtimeId: string) {
  const entries = records(runtimeId);
  return entries[entries.length - 1];
}

describe('terminal input evidence', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'setInterval'] });
    vi.setSystemTime(100_000);
    recordDiag.mockClear();
  });

  afterEach(() => {
    disposers.splice(0).forEach((dispose) => dispose());
    document.body.replaceChildren();
    vi.useRealTimers();
  });

  it('records the missing-composition-end failure before interception while another pane works', async () => {
    const first = setup('first');
    const second = setup('second', false);
    await Promise.resolve();
    first.element.focus();
    first.element.dispatchEvent(new CompositionEvent('compositionstart', { data: 'private preedit' }));
    second.element.focus();
    first.element.focus();
    type(first.element);
    type(second.element);
    await Promise.resolve();

    expect(first.send).not.toHaveBeenCalled();
    expect(second.send).toHaveBeenCalledWith('a');
    const record = lastRecord('first');
    expect(record).toMatchObject({
      kind: 'input', reasons: expect.arrayContaining(['composition_mismatch']), composing: true,
      compositionStartedAt: 100_000, compositionEndedAt: null,
      counts: { 'keydown:composing': 1 },
      focus: { terminalFocused: true },
      state: { lastWriteAt: 10, lastPaintAt: 20 },
    });
    expect(record.recent).toContainEqual(expect.objectContaining({
      event: 'keydown', outcome: 'composing', browserComposing: false, keyClass: 'text',
    }));
    expect(JSON.stringify(record)).not.toContain('private preedit');
  });

  it('captures keys directed to a find field without storing keys, preedit, clipboard or field contents', async () => {
    const input = setup('privacy');
    const find = document.createElement('input');
    find.className = 'ghostty-find-input';
    find.value = 'PRIVATE_FIELD_CONTENT';
    find.id = 'PRIVATE_ELEMENT_ID';
    document.body.append(find);
    find.focus();
    type(find, 'Ω');
    type(input.element, 'Ω');
    input.element.dispatchEvent(new CompositionEvent('compositionstart', { data: 'PRIVATE_PREEDIT' }));
    const end = new Event('compositionend');
    Object.defineProperty(end, 'data', { value: 'PRIVATE_COMMIT' });
    input.element.dispatchEvent(end);
    const paste = new Event('paste', { bubbles: true, cancelable: true });
    Object.defineProperty(paste, 'clipboardData', { value: { getData: () => 'PRIVATE_CLIPBOARD' } });
    input.element.dispatchEvent(paste);
    await Promise.resolve();

    const record = lastRecord('privacy');
    expect(record.focus.activeElement).toBe('terminal_find');
    expect(record.recent).toContainEqual(expect.objectContaining({
      event: 'document_keydown', inTerminal: false, target: 'terminal_find',
    }));
    expect(record.recent).toContainEqual(expect.objectContaining({ event: 'paste', outcome: 'sent' }));
    expect(JSON.stringify(record)).not.toMatch(/PRIVATE_|Ω/);
    expect(input.send.mock.calls.flat()).toEqual(['Ω', 'PRIVATE_COMMIT', 'PRIVATE_CLIPBOARD']);
  });

  it('bounds a long failed-typing burst and records cumulative counts without polling', async () => {
    const input = setup('burst');
    input.element.dispatchEvent(new CompositionEvent('compositionstart'));
    type(input.element);
    await Promise.resolve();
    const before = records('burst').length;
    for (let i = 0; i < 10_000; i++) type(input.element);
    await Promise.resolve();
    expect(records('burst')).toHaveLength(before);
    expect(vi.getTimerCount()).toBe(0);

    vi.setSystemTime(130_001);
    type(input.element);
    await Promise.resolve();
    const record = lastRecord('burst');
    expect(record.recent).toHaveLength(32);
    expect(record.counts['keydown:composing']).toBe(10_002);
    expect(record.counts.document_keydown).toBe(10_002);
    expect(records('burst')).toHaveLength(before + 1);
  });

  it('correlates transport state and PTY acknowledgements and releases observers on disposal', async () => {
    setup('transport');
    noteTerminalInputTransport('transport', { socketState: 3, initialStateReceived: false, probeId: 'probe-1' });
    await Promise.resolve();
    expect(lastRecord('transport').reasons).toContain('transport_unready');
    noteTerminalInputReceipt('transport', {
      probeId: 'probe-1', success: false, roundTripMs: 350, daemonWriteMs: 5,
    });
    await Promise.resolve();
    expect(lastRecord('transport')).toMatchObject({
      reasons: expect.arrayContaining(['pty_write_failed']),
      recent: expect.arrayContaining([expect.objectContaining({
        event: 'pty_receipt', probeId: 'probe-1', success: false,
      })]),
    });

    disposers.splice(0).forEach((dispose) => dispose());
    recordDiag.mockClear();
    type(document.body);
    noteTerminalInputReceipt('transport', {
      probeId: 'late', success: true, roundTripMs: 1, daemonWriteMs: 1,
    });
    await Promise.resolve();
    expect(recordDiag).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });
});
