import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentPane, agentWorkspace, daemonSession, daemonWorkspace, splitWorkspace } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';

const renderedPaneSessions = () =>
  Array.from(document.querySelectorAll('[data-pane-session-id]')).map((pane) => pane.getAttribute('data-pane-session-id'));

const selectedSession = () => document.querySelector('.session-item.selected .session-label')?.textContent ?? null;

describe('App session and workspace sync', () => {
  it('lists shell sessions next to agents, as the daemon reports them', async () => {
    const shellPane = agentPane('sh1', 'workspace-1');
    const { daemon } = await renderApp({
      initialState: {
        sessions: [
          daemonSession('a1', { workspace_id: 'workspace-1' }),
          daemonSession('sh1', { agent: 'shell', workspace_id: 'workspace-1' }),
        ],
        workspaces: [daemonWorkspace('workspace-1', {
          root: { type: 'split', split_id: 'root', direction: 'vertical', ratio: 0.5, children: [{ type: 'pane', pane_id: 'pane-a1' }, { type: 'pane', pane_id: 'pane-sh1' }] },
          panes: [agentPane('a1', 'workspace-1'), shellPane],
        })],
      },
    });
    expect(screen.getByRole('button', { name: 'Open sh1' })).toBeInTheDocument();

    daemon.emit({ event: 'session_registered', session: daemonSession('sh2', { agent: 'shell', workspace_id: 'workspace-1' }) });
    daemon.emit({
      event: 'workspace_layout_updated',
      workspace_layout: {
        workspace_id: 'workspace-1',
        active_pane_id: 'pane-a1',
        layout_json: JSON.stringify({ type: 'split', split_id: 'root', direction: 'vertical', ratio: 0.5, children: [{ type: 'pane', pane_id: 'pane-a1' }, { type: 'split', split_id: 'right', direction: 'horizontal', ratio: 0.5, children: [{ type: 'pane', pane_id: 'pane-sh1' }, { type: 'pane', pane_id: 'pane-sh2' }] }] }),
        panes: [agentPane('a1', 'workspace-1'), shellPane, agentPane('sh2', 'workspace-1')],
      },
    });

    expect(screen.getByRole('button', { name: 'Open sh2' })).toBeInTheDocument();
  });

  it('updates a renamed session in place without duplicating the sidebar row', async () => {
    const { daemon } = await renderApp({ initialState: { sessions: [daemonSession('s1')], workspaces: [agentWorkspace('s1')] } });

    daemon.emit({ event: 'session_state_changed', session: daemonSession('s1', { label: 'renamed' }) });

    expect(screen.getAllByRole('button', { name: 'Open renamed' })).toHaveLength(1);
    expect(screen.queryByRole('button', { name: 'Open s1' })).toBeNull();
  });

  it('keeps a workspace when the daemon stops listing its agent session, so the session can come back into it', async () => {
    const { daemon } = await renderApp({ initialState: { sessions: [daemonSession('s1')], workspaces: [agentWorkspace('s1')] } });

    daemon.emit({ event: 'sessions_updated', sessions: [] });
    daemon.emit({ event: 'sessions_updated', sessions: [daemonSession('s1')] });

    expect(renderedPaneSessions()).toEqual(['s1']);
  });

  it('keeps the rendered layout when a workspace state change omits it', async () => {
    const { daemon } = await renderApp({ initialState: { sessions: [daemonSession('s1')], workspaces: [agentWorkspace('s1')] } });
    expect(renderedPaneSessions()).toEqual(['s1']);

    const { layout: _omitted, ...withoutLayout } = agentWorkspace('s1');
    daemon.emit({ event: 'workspace_state_changed', workspace: { ...withoutLayout, status: 'working' } });

    expect(renderedPaneSessions()).toEqual(['s1']);
  });

  it('drops a closed session layout but keeps the workspace until the daemon unregisters it', async () => {
    const closed = daemonSession('s1');
    const { daemon } = await renderApp({ initialState: { sessions: [closed], workspaces: [agentWorkspace('s1')] } });
    const reopenedLayout = { event: 'workspace_layout_updated', workspace_layout: agentWorkspace('s1').layout! } as const;

    daemon.emit({ event: 'session_unregistered', session: closed });
    expect(renderedPaneSessions()).toEqual([]);

    const { layout: _omitted, ...withoutLayout } = agentWorkspace('s1');
    daemon.emit({ event: 'workspace_state_changed', workspace: withoutLayout });
    expect(renderedPaneSessions()).toEqual([]);

    daemon.emit({ event: 'session_registered', session: closed });
    daemon.emit(reopenedLayout);
    expect(renderedPaneSessions()).toEqual(['s1']);

    daemon.emit({ event: 'workspace_unregistered', workspace: withoutLayout });
    daemon.emit(reopenedLayout);
    expect(renderedPaneSessions()).toEqual([]);
  });

  it('moves to the session left in the workspace when the open one closes', async () => {
    const { daemon } = await renderApp({
      initialState: {
        sessions: [daemonSession('elsewhere'), daemonSession('root', { workspace_id: 'ws' }), daemonSession('split', { workspace_id: 'ws' })],
        workspaces: [agentWorkspace('elsewhere'), splitWorkspace('ws', ['root', 'split'])],
      },
    });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open split' })));
    expect(selectedSession()).toBe('split');

    daemon.emit({ event: 'session_unregistered', session: daemonSession('split', { workspace_id: 'ws' }) });
    daemon.emit({ event: 'workspace_layout_updated', workspace_layout: splitWorkspace('ws', ['root']).layout! });
    await daemon.idle();

    expect(selectedSession()).toBe('root');
  });

  it('moves to the session the user was on before when a whole workspace closes', async () => {
    const sessions = ['first', 'second', 'closing'].map((id) => daemonSession(id));
    const { daemon } = await renderApp({ initialState: { sessions, workspaces: sessions.map(({ id }) => agentWorkspace(id)) } });
    for (const id of ['second', 'first', 'closing']) {
      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: `Open ${id}` })));
    }

    daemon.emit({ event: 'session_unregistered', session: sessions[2] });
    daemon.emit({ event: 'workspace_unregistered', workspace: agentWorkspace('closing') });
    await daemon.idle();

    expect(selectedSession()).toBe('first');
  });

  it.each([
    ['keeps a session still launching into its pane', 'launching' as const, true],
    ['drops a session that already ran', 'idle' as const, false],
  ])('%s when a daemon snapshot omits it', async (_, state, kept) => {
    const spawning = daemonWorkspace('ws', { root: { type: 'pane', pane_id: 'pane-booting' }, panes: [{ ...agentPane('booting', 'ws'), status: 'spawning' }] });
    const { daemon } = await renderApp({ initialState: { sessions: [daemonSession('booting', { workspace_id: 'ws', state })], workspaces: [spawning] } });

    await gesture(daemon, () => daemon.emit({ event: 'sessions_updated', sessions: [] }));

    expect(screen.queryByRole('button', { name: 'Open booting' }) !== null).toBe(kept);
  });
});
