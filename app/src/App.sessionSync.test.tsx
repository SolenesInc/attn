import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentPane, agentWorkspace, daemonSession, daemonWorkspace } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';

const renderedPaneSessions = () =>
  Array.from(document.querySelectorAll('[data-pane-session-id]')).map((pane) => pane.getAttribute('data-pane-session-id'));

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
});
