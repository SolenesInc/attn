import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderApp } from './test/renderApp';
import { agentPane, agentWorkspace, daemonSession, daemonWorkspace } from './test/daemonFixtures';

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

  it('tells the user why a pane has no terminal yet: failed, starting, or waiting for its session', async () => {
    const panes = [
      agentPane('s1', 'ws'),
      { ...agentPane('refused', 'ws'), title: 'claude', status: 'failed' as const, error: 'spawn refused' },
      { ...agentPane('bare-failure', 'ws'), title: 'claude', status: 'failed' as const },
      { ...agentPane('booting', 'ws'), title: 'codex', status: 'spawning' as const },
      { ...agentPane('late', 'ws'), title: 'copilot' },
    ];
    const root = panes.slice(1).reduce<unknown>(
      (left, { pane_id }, index) => ({ type: 'split', split_id: `split-${index}`, direction: 'vertical', ratio: 0.5, children: [left, { type: 'pane', pane_id }] }),
      { type: 'pane', pane_id: panes[0].pane_id },
    );
    const { daemon } = await renderApp({
      initialState: {
        sessions: [daemonSession('s1', { workspace_id: 'ws' })],
        workspaces: [daemonWorkspace('ws', { root, panes })],
      },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
    await daemon.idle();
    const notice = (sessionId: string) => document.querySelector(`[data-pane-id="pane-${sessionId}"] .workspace-pane-body`)?.textContent;

    expect(notice('refused')).toBe('spawn refused');
    expect(notice('bare-failure')).toBe('Session failed to start');
    expect(notice('booting')).toBe('Starting codex...');
    expect(notice('late')).toBe('Waiting for copilot...');
    expect(document.querySelector('[data-pane-id="pane-refused"] [aria-label^="Rename session"]')).toBeNull();

    daemon.emit({ event: 'session_registered', session: daemonSession('late', { workspace_id: 'ws' }) });
    await daemon.idle();

    expect(notice('late')).not.toContain('Waiting');
    expect(document.querySelector('[data-pane-id="pane-late"] [aria-label="Terminal input"]')).not.toBeNull();
  });
});
