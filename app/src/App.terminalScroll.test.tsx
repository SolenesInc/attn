import { act, createEvent, fireEvent } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openAttachedTerminals } from './test/appFixtures';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';

const HISTORY = Array.from({ length: 40 }, (_, line) => `line-${line}`);

async function openScrolledTerminal() {
  const view = await openAttachedTerminals({
    sessions: [daemonSession('s1', { state: 'idle' })],
    workspaces: [agentWorkspace('s1')],
    output: { s1: HISTORY.join('\r\n') },
  });
  await act(() => vi.advanceTimersToNextFrame());
  const surface = document.querySelector('[data-pane-id="pane-s1"] .terminal-container')!;
  const wheelUp = (times: number) => act(() => {
    for (let turn = 0; turn < times; turn += 1) {
      const wheel = createEvent.wheel(surface, { deltaY: -1, deltaMode: 1 });
      Object.defineProperties(wheel, { clientX: { value: 20 }, clientY: { value: 20 } });
      fireEvent(surface, wheel);
    }
  });
  return { ...view, wheelUp };
}

function visibleRows() {
  return window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('s1')?.split('\n');
}

function paints() {
  return window.__ATTN_TERMINAL_PERF_DUMP?.()[0]?.renderCount;
}

describe('App terminal scroll', () => {
  it('scrolls a burst of wheel turns into history and paints them in one frame', async () => {
    const { daemon, wheelUp } = await openScrolledTerminal();
    expect(visibleRows()).toEqual(HISTORY.slice(16));
    const paintsBefore = paints()!;

    await wheelUp(12);
    await act(() => vi.advanceTimersToNextFrame());

    expect(visibleRows()).toEqual(HISTORY.slice(4, 28));
    expect(paints()).toBe(paintsBefore + 1);
    expect(daemon.sentOf('pty_input')).toEqual([]);
  });

  it('hands the wheel to a program tracking the mouse and keeps the view at the bottom', async () => {
    const { daemon, wheelUp } = await openScrolledTerminal();
    daemon.emit({ event: 'pty_output', id: 's1', seq: 2, data: btoa('\x1b[?1000h\x1b[?1006h') });
    await daemon.idle();

    await wheelUp(3);
    await act(() => vi.advanceTimersToNextFrame());
    await daemon.idle();

    expect(visibleRows()).toEqual(HISTORY.slice(16));
    expect(daemon.sentOf('pty_input')).toEqual(Array.from({ length: 3 }, () => ({
      cmd: 'pty_input',
      id: 's1',
      data: '\x1b[<64;3;1M',
      source: 'pointer',
    })));
  });
});
