import { describe, expect, it } from 'vitest';
import { agentPane, daemonEndpoint, daemonSession, daemonWorkspace, splitWorkspace } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';

const UNRESOLVED = 'Workspace has a pane without an active session';

function sidebar() {
  return Array.from(document.querySelectorAll('.workspace-row'), (row) => {
    const rows = Array.from(row.querySelectorAll('.session-item .session-label'), (label) => label.textContent);
    const placeholder = row.querySelector('[data-testid="workspace-neutral-indicator"]')?.getAttribute('title');
    const endpoint = row.querySelector('.workspace-group-header .session-endpoint-badge')?.textContent;
    return [row.querySelector('.workspace-label')?.textContent, rows, ...(placeholder ? [placeholder] : []), ...(endpoint ? [endpoint] : [])];
  });
}

describe('App workspace sidebar', () => {
  it('lists workspaces by rank, each with its laid-out sessions and tiles in layout order', async () => {
    await renderApp({
      initialState: {
        workspaces: [
          splitWorkspace('c', ['c1'], { rank: 'a2' }),
          daemonWorkspace('a', {
            root: {
              type: 'split',
              split_id: 'split-a',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', pane_id: 'pane-a1' },
                { type: 'tile', tile_id: 'tile-docs', tile_kind: 'browser', tile_params: 'https://docs.test' },
                { type: 'pane', pane_id: 'pane-a2' },
              ],
            },
            panes: [agentPane('a1', 'a'), agentPane('a2', 'a')],
          }, { rank: 'a0' }),
          splitWorkspace('b', ['b1'], { rank: 'a1' }),
          splitWorkspace('d', [], { rank: 'a3', layout: undefined }),
        ],
        sessions: [
          daemonSession('c1', { workspace_id: 'c' }),
          daemonSession('a2', { workspace_id: 'a' }),
          daemonSession('a1', { workspace_id: 'a' }),
          daemonSession('dropped', { workspace_id: 'a' }),
          daemonSession('b1', { workspace_id: 'b' }),
          daemonSession('d1', { workspace_id: 'd' }),
        ],
      },
    });

    expect(sidebar()).toEqual([
      ['a', ['a1', 'docs.test', 'a2']],
      ['b', ['b1']],
      ['c', ['c1']],
      ['d', ['d1']],
    ]);
  });

  it('orders workspaces without a rank by id', async () => {
    await renderApp({
      initialState: {
        workspaces: [splitWorkspace('z', ['z1'], { rank: undefined, title: 'Alpha' }), splitWorkspace('y', ['y1'], { rank: undefined, title: 'Omega' })],
        sessions: [daemonSession('z1', { workspace_id: 'z' }), daemonSession('y1', { workspace_id: 'y' })],
      },
    });

    expect(sidebar()).toEqual([['Omega', ['y1']], ['Alpha', ['z1']]]);
  });

  it('shows only the workspaces the daemon reports, whatever its sessions claim', async () => {
    await renderApp({
      initialState: {
        workspaces: [splitWorkspace('a', ['a1'])],
        sessions: [daemonSession('a1', { workspace_id: 'a' }), daemonSession('elsewhere', { workspace_id: 'unreported' }), daemonSession('orphan', { workspace_id: '' })],
      },
    });

    expect(sidebar()).toEqual([['a', ['a1']]]);
  });

  it('files a session missing its workspace id under the workspace whose layout holds it', async () => {
    await renderApp({
      initialState: {
        workspaces: [splitWorkspace('a', ['a1'])],
        sessions: [daemonSession('a1', { workspace_id: '' })],
      },
    });

    expect(sidebar()).toEqual([['a', ['a1']]]);
  });

  it('marks a workspace whose pane is launching or failed, and drops one whose ready pane lost its session', async () => {
    const { daemon } = await renderApp({
      initialState: {
        workspaces: [
          daemonWorkspace('launching', { root: { type: 'pane', pane_id: 'pane-l1' }, panes: [{ ...agentPane('l1', 'launching'), status: 'spawning' }] }, { status: 'launching' }),
          daemonWorkspace('failed', { root: { type: 'pane', pane_id: 'pane-closed' }, panes: [{ ...agentPane('closed', 'failed'), status: 'failed' }] }),
          splitWorkspace('stale', ['gone']),
          splitWorkspace('live', ['live1']),
        ],
        sessions: [daemonSession('live1', { workspace_id: 'live' })],
      },
    });
    expect(sidebar()).toEqual([
      ['failed', [], UNRESOLVED],
      ['launching', [], UNRESOLVED],
      ['live', ['live1']],
    ]);

    daemon.emit({ event: 'session_unregistered', session: daemonSession('live1', { workspace_id: 'live' }) });
    await daemon.idle();

    expect(sidebar().map(([title]) => title)).toEqual(['failed', 'launching']);
  });

  it('groups remote sessions under their workspace, once per endpoint sharing its id', async () => {
    await renderApp({
      initialState: {
        endpoints: [daemonEndpoint('ep-a', { name: 'box-a' }), daemonEndpoint('ep-b', { name: 'box-b' })],
        workspaces: [
          splitWorkspace('shared', [], { title: 'Remote A', layout: undefined }),
          splitWorkspace('shared', [], { title: 'Remote B', layout: undefined }),
        ],
        sessions: [
          daemonSession('a1', { workspace_id: 'shared', endpoint_id: 'ep-a', directory: '/srv/a' }),
          daemonSession('b1', { workspace_id: 'shared', endpoint_id: 'ep-b', directory: '/srv/b' }),
        ],
      },
    });

    expect(sidebar()).toEqual([
      ['Remote A', ['a1'], 'box-a'],
      ['Remote B', ['b1'], 'box-b'],
    ]);
  });
});
