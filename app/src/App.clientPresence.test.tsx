import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, onTestFinished, vi } from 'vitest';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const HEARTBEAT_MS = 30_000;

function launch() {
  return renderApp({ initialState: { sessions: [daemonSession('s1')], workspaces: [agentWorkspace('s1')] } });
}

const reports = (daemon: ScriptedDaemon) => daemon.sentOf('set_client_presence');

async function elapse(daemon: ScriptedDaemon, ms: number) {
  await act(() => vi.advanceTimersByTimeAsync(ms));
  await daemon.idle();
}

function hideWindow() {
  Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true });
  onTestFinished(() => {
    Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true });
  });
  act(() => {
    document.dispatchEvent(new Event('visibilitychange'));
  });
}

describe('App client presence', () => {
  it('reports that home is on screen as soon as it connects, and keeps reporting while nothing changes', async () => {
    const { daemon } = await launch();
    expect(reports(daemon)).toEqual([{ cmd: 'set_client_presence', visible: true, dashboard_visible: true }]);

    await elapse(daemon, HEARTBEAT_MS * 2 + 1);

    expect(reports(daemon)).toHaveLength(3);
  });

  it('reports the moment the window is hidden, without waiting for the heartbeat', async () => {
    const { daemon } = await launch();

    hideWindow();

    expect(reports(daemon).slice(1)).toEqual([{ cmd: 'set_client_presence', visible: false, dashboard_visible: true }]);
  });

  it('reports leaving home for an agent', async () => {
    const { daemon } = await launch();

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s1' })));

    expect(reports(daemon).slice(-1)).toEqual([{ cmd: 'set_client_presence', visible: true, dashboard_visible: false }]);
  });

  it('says how long the user has been idle only once it has seen input', async () => {
    const { daemon } = await launch();
    fireEvent.keyDown(window, { key: 'Shift' });

    await elapse(daemon, HEARTBEAT_MS);

    expect(reports(daemon)[0]).not.toHaveProperty('idle_seconds');
    expect(reports(daemon).slice(-1)[0].idle_seconds).toBeCloseTo(HEARTBEAT_MS / 1000, 0);
  });

  it('collapses a burst of input into a single report', async () => {
    const { daemon } = await launch();
    await elapse(daemon, 11_000);
    const before = reports(daemon).length;

    for (let key = 0; key < 20; key += 1) fireEvent.keyDown(window, { key: 'Shift' });
    await daemon.idle();

    expect(reports(daemon).length - before).toBe(1);
  });
});
