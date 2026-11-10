import { act, screen } from '@testing-library/react';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { listen } from '@tauri-apps/api/event';
import { describe, expect, it, vi } from 'vitest';
import { daemonSession } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';
import { initialState } from './test/scriptedDaemon';

const REQUEST_EVENT = 'attn://ui-automation/request';

describe('automation bridge from first paint', () => {
  it('reports ready once, after it listens, and answers on the connecting screen and the shell', async () => {
    vi.mocked(isTauri).mockReturnValue(true);
    (window as { __ATTN_AUTOMATION_ENABLED?: boolean }).__ATTN_AUTOMATION_ENABLED = true;
    const listeners = new Set<(event: { payload: unknown }) => void>();
    const listenersAtReady: number[] = [];
    vi.mocked(listen).mockImplementation(async (event, handler) => {
      if (event !== REQUEST_EVENT) return () => {};
      const listener = handler as (event: { payload: unknown }) => void;
      listeners.add(listener);
      return () => listeners.delete(listener);
    });
    vi.mocked(invoke).mockImplementation(async (command) => {
      if (command === 'ui_automation_ready') listenersAtReady.push(listeners.size);
    });
    const responses = () => vi.mocked(invoke).mock.calls
      .filter(([command]) => command === 'ui_automation_respond')
      .map(([, args]) => (args as { response: { request_id: string; ok: boolean; result?: { sessions?: unknown[] } } }).response);
    const getState = async (requestId: string) => {
      act(() => [...listeners].forEach((handler) => handler({ payload: { request_id: requestId, action: 'get_state' } })));
      await act(() => vi.advanceTimersByTimeAsync(100));
      return responses().find((response) => response.request_id === requestId);
    };

    const { daemon } = await renderApp({ initialState: false });
    expect(screen.getByRole('status').textContent).toContain('Connecting to the attn daemon');
    expect(await getState('connecting')).toMatchObject({ ok: true, result: { sessions: [] } });

    act(() => daemon.emit(initialState({ sessions: [daemonSession('s1')] })));
    await daemon.idle();
    expect(screen.queryByText('Connecting to the attn daemon…')).toBeNull();
    const shellAnswer = await getState('shell');
    expect(shellAnswer).toMatchObject({ ok: true });
    expect(shellAnswer?.result?.sessions).toHaveLength(1);

    expect(listenersAtReady).toEqual([1]);
  });
});
