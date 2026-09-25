import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderApp } from './test/renderApp';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';

const WORKER_NOT_LISTENING = 'dial unix /Users/test/.attn/workers/d-test/sock/s1.sock: connect: no such file or directory';

async function renderSessions() {
  return renderApp({
    initialState: {
      sessions: [daemonSession('s1'), daemonSession('s2')],
      workspaces: [agentWorkspace('s1'), agentWorkspace('s2')],
    },
  });
}

describe('App pane attach', () => {
  it('attaches the terminal of the session the user opens, and only that one', async () => {
    const { daemon } = await renderSessions();
    await daemon.idle();
    expect(daemon.sentOf('attach_session')).toEqual([]);

    fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
    await daemon.idle();

    expect(daemon.sentOf('attach_session').map((command) => command.id)).toEqual(['s1']);
  });

  it('retries an attach the daemon refused while the session worker was respawning', async () => {
    const { daemon } = await renderSessions();
    let refusals = 1;
    daemon.on('attach_session', ({ id }) => (
      refusals-- > 0
        ? { event: 'attach_result', id, success: false, error: WORKER_NOT_LISTENING }
        : { event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true }
    ));

    fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
    await daemon.idle();
    expect(daemon.sentOf('attach_session')).toHaveLength(1);

    await act(() => vi.advanceTimersByTimeAsync(1000));
    await daemon.idle();

    expect(daemon.sentOf('attach_session').map((command) => command.id)).toEqual(['s1', 's1']);
  });
});
