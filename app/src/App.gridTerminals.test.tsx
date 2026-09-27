import { act } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { getGridAutomationHandle } from './components/grid/gridAutomation';
import { soloDesktop, daemonSession } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const SESSIONS = ['s1', 's2'];

async function openGrid(script: (daemon: ScriptedDaemon) => void = () => {}) {
  const view = await renderApp({
    initialState: {
      sessions: SESSIONS.map((id) => daemonSession(id, { state: 'idle' })),
      desktops: SESSIONS.map((id) => soloDesktop(id)),
    },
  });
  view.daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true }));
  view.daemon.on('get_screen_snapshot', ({ id }) => ({ event: 'get_screen_snapshot_result', id, success: true, last_seq: 0, screen_cols: 80, screen_rows: 24, screen_snapshot: '' }));
  script(view.daemon);
  await gesture(view.daemon, () => pressShortcut('view.toggleGrid'));
  await settle(view.daemon);
  return view;
}

async function settle(daemon: ScriptedDaemon, ms = 50) {
  await act(() => vi.advanceTimersByTimeAsync(ms));
  await daemon.idle();
}

function output(daemon: ScriptedDaemon, id: string, seq: number, text: string) {
  daemon.emit({ event: 'pty_output', id, seq, data: btoa(text) });
}

const tileText = (id: string) => getGridAutomationHandle()?.getTileText(id)?.trim();
const gridStats = () => getGridAutomationHandle()!.getState().stats!;

describe('App grid terminals', () => {
  it('shows each session’s screen snapshot, then only the output newer than it', async () => {
    const { daemon } = await openGrid((d) => d.on('get_screen_snapshot', () => undefined));

    output(daemon, 's1', 9, 'already-in-snapshot ');
    output(daemon, 's1', 11, 'after-snapshot');
    output(daemon, 's2', 1, 'second session');
    await settle(daemon);
    expect(tileText('s1')).toBe('');

    daemon.emit({ event: 'get_screen_snapshot_result', id: 's1', success: true, last_seq: 10, screen_cols: 80, screen_rows: 24, screen_snapshot: btoa('snapshot ') });
    daemon.emit({ event: 'get_screen_snapshot_result', id: 's2', success: false, error: 'no screen' });
    await settle(daemon);

    expect(tileText('s1')).toBe('snapshot after-snapshot');
    expect(tileText('s2')).toBe('second session');
  });

  it('stops repainting once the grid is at rest, and repaints for new output', async () => {
    const { daemon } = await openGrid();
    await settle(daemon, 2000);
    expect(gridStats()).toMatchObject({ framePending: false });

    await settle(daemon, 1000);
    expect(gridStats().renderIdleMs).toBeGreaterThanOrEqual(1000);

    output(daemon, 's1', 1, 'wake');
    await settle(daemon);
    expect(tileText('s1')).toBe('wake');
    expect(gridStats().renderIdleMs).toBeLessThan(1000);

    await settle(daemon, 2000);
    expect(gridStats()).toMatchObject({ framePending: false });
  });
});
