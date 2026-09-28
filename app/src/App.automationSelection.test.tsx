import { act } from '@testing-library/react';
import { isTauri } from '@tauri-apps/api/core';
import { emit, listen } from '@tauri-apps/api/event';
import { describe, expect, it, vi } from 'vitest';
import { daemonSession, soloDesktop, splitDesktop } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

interface AutomationResponse {
  request_id: string;
  ok: boolean;
  result?: unknown;
  error?: string;
}

async function renderAutomatedApp(desktops = [soloDesktop('s1'), soloDesktop('s2')]) {
  vi.mocked(isTauri).mockReturnValue(true);
  (window as { __ATTN_AUTOMATION_ENABLED?: boolean }).__ATTN_AUTOMATION_ENABLED = true;
  const listeners = new Set<(event: { payload: unknown }) => void>();
  vi.mocked(listen).mockImplementation(async (event, handler) => {
    if (event !== 'attn://ui-automation/request') return () => {};
    const listener = handler as (event: { payload: unknown }) => void;
    listeners.add(listener);
    return () => listeners.delete(listener);
  });
  const view = await renderApp({ initialState: {
    sessions: [daemonSession('s1'), daemonSession('s2')],
    desktops,
  } });
  const request = (event: { payload: unknown }) => [...listeners].forEach((handler) => handler(event));
  const responses = () => vi.mocked(emit).mock.calls
    .filter(([event]) => event === 'attn://ui-automation/response')
    .map(([, payload]) => payload as AutomationResponse);
  const ask = async (action: string, payload: Record<string, unknown>) => {
    const requestId = `${action}-${responses().length}`;
    act(() => request({ payload: { request_id: requestId, action, payload } }));
    await act(() => vi.advanceTimersByTimeAsync(100));
    return () => responses().find((response) => response.request_id === requestId);
  };
  const select = (sessionId: string) => ask('select_session', { sessionId });
  return { ...view, select, ask };
}

function holdShows(daemon: ScriptedDaemon) {
  const held: Array<{ request_id: string; cmd: 'desktop_show_session' }> = [];
  daemon.on('desktop_show_session', (command) => {
    held.push(command);
    return undefined;
  });
  return held;
}

describe('automation select_session', () => {
  it('answers once the daemon made the session’s pane the active leaf', async () => {
    const { daemon, select } = await renderAutomatedApp();

    const answer = await select('s2');
    await daemon.idle();
    await act(() => vi.advanceTimersByTimeAsync(100));

    expect(daemon.sentOf('desktop_show_session')).toEqual([expect.objectContaining({ session_id: 's2' })]);
    expect(answer()).toMatchObject({ ok: true, result: { sessionId: 's2' } });
  });

  it('fails when the daemon refuses the show', async () => {
    const { daemon, select } = await renderAutomatedApp();
    const held = holdShows(daemon);

    const answer = await select('s2');
    expect(answer()).toBeUndefined();
    await act(async () => {
      for (const command of held.splice(0)) {
        daemon.replyTo(command as never, {
          event: 'profile_action_result',
          action: command.cmd,
          request_id: command.request_id,
          success: false,
          error: 'session s2 closed',
          error_code: 'session_closed',
        });
      }
    });
    await act(() => vi.advanceTimersByTimeAsync(100));

    expect(answer()).toMatchObject({ ok: false, error: expect.stringContaining('dropped the request') });
  });

  it('fails when the connection to the daemon drops before the show lands', async () => {
    const { daemon, select } = await renderAutomatedApp();
    holdShows(daemon);

    const answer = await select('s2');
    expect(answer()).toBeUndefined();
    act(() => daemon.disconnect());
    await act(() => vi.advanceTimersByTimeAsync(100));

    expect(answer()).toMatchObject({ ok: false, error: expect.stringContaining('dropped the request') });
  });

  it('answers focus_pane only once the daemon shows that pane', async () => {
    const { daemon, ask } = await renderAutomatedApp([splitDesktop('ws', ['s1', 's2'])]);
    const held: Array<{ request_id: string; cmd: 'desktop_show_leaf'; desktop_id: string; leaf_id: string }> = [];
    daemon.on('desktop_show_leaf', (command) => {
      held.push(command);
      return undefined;
    });

    const answer = await ask('focus_pane', { sessionId: 's1', paneId: 'pane-s2' });
    await daemon.idle();
    await act(() => vi.advanceTimersByTimeAsync(100));
    expect(held.map((command) => command.leaf_id)).toEqual(['pane-s2']);
    expect(answer()).toBeUndefined();

    await act(async () => {
      for (const command of held.splice(0)) {
        daemon.arrangement.show(command.desktop_id, command.leaf_id);
        daemon.replyTo(command as never, { event: 'profile_action_result', action: command.cmd, request_id: command.request_id, success: true });
        daemon.emit(daemon.arrangement.changed());
      }
    });
    await act(() => vi.advanceTimersByTimeAsync(100));

    expect(answer()).toMatchObject({ ok: true, result: { paneId: 'pane-s2' } });
  });
});
