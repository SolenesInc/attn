import { describe, expect, it } from 'vitest';
import { renderApp } from './test/renderApp';
import type { EventMessage } from './test/protocol';

type Session = EventMessage<'session_state_changed'>['session'];
type Workspace = NonNullable<EventMessage<'initial_state'>['workspaces']>[number];
type Layout = EventMessage<'workspace_layout_updated'>['workspace_layout'];
type Pane = Layout['panes'][number];

const SOURCE = 'ws-source';
const TARGET = 'ws-target';

const AT = '2026-01-01T00:00:00Z';

function session(id: string, label: string, workspaceId: string): Session {
  return {
    id,
    label,
    agent: 'claude',
    directory: '/tmp/repo',
    workspace_id: workspaceId,
    state: 'idle',
    last_seen: AT,
    state_since: AT,
    state_updated_at: AT,
  };
}

function pane(paneId: string, sessionId: string, workspaceId: string, title: string): Pane {
  return {
    pane_id: paneId,
    session_id: sessionId,
    runtime_id: sessionId,
    workspace_id: workspaceId,
    kind: 'agent',
    status: 'ready',
    title,
  };
}

function lonePaneWorkspace(id: string, title: string, paneId: string, sessionId: string): Workspace {
  return {
    id,
    title,
    directory: '/tmp/repo',
    status: 'idle',
    muted: false,
    pinned: false,
    rank: id,
    layout: {
      workspace_id: id,
      active_pane_id: paneId,
      layout_json: JSON.stringify({ type: 'pane', pane_id: paneId }),
      panes: [pane(paneId, sessionId, id, title)],
    },
  };
}

type PaneRef = { paneId: string; sessionId: string };

function nestedSplits(id: string, panes: PaneRef[]): unknown {
  if (panes.length === 1) {
    return { type: 'pane', pane_id: panes[0].paneId };
  }
  return {
    type: 'split',
    split_id: `${id}-split-${panes.length}`,
    direction: 'horizontal',
    ratio: 0.5,
    children: [{ type: 'pane', pane_id: panes[0].paneId }, nestedSplits(id, panes.slice(1))],
  };
}

function workspaceWithPanes(id: string, title: string, panes: PaneRef[]): Workspace & { layout: Layout } {
  return {
    id,
    title,
    directory: '/tmp/repo',
    status: 'idle',
    muted: false,
    pinned: false,
    rank: id,
    layout: {
      workspace_id: id,
      active_pane_id: panes[0].paneId,
      layout_json: JSON.stringify(nestedSplits(id, panes)),
      panes: panes.map((entry) => pane(entry.paneId, entry.sessionId, id, entry.sessionId)),
    },
  };
}

function paneSessionIds(root: ParentNode): string[] {
  return Array.from(root.querySelectorAll('[data-pane-kind="agent"]'))
    .map((node) => node.getAttribute('data-pane-session-id') || '')
    .sort();
}

function renderedPanes(workspaceId: string): string[] {
  const workspace = document.querySelector(
    `.session-terminal-workspace[data-workspace-id="${workspaceId}"]`,
  );
  return workspace ? paneSessionIds(workspace) : [];
}

function renderedWorkspaceIds(): string[] {
  return Array.from(document.querySelectorAll('.session-terminal-workspace'))
    .map((node) => node.getAttribute('data-workspace-id') || '')
    .sort();
}

describe('a pane moved to another workspace', () => {
  it('renders in the target workspace once the layout and its session ownership arrive', async () => {
    const { daemon } = await renderApp({
      initialState: {
        sessions: [
          session('s-source', 'source agent', SOURCE),
          session('s-target', 'target agent', TARGET),
        ],
        workspaces: [
          lonePaneWorkspace(SOURCE, 'Source', 'pane-source', 's-source'),
          lonePaneWorkspace(TARGET, 'Target', 'pane-target', 's-target'),
        ],
      },
    });

    expect(renderedWorkspaceIds()).toEqual([SOURCE, TARGET]);
    expect(renderedPanes(SOURCE)).toEqual(['s-source']);
    expect(renderedPanes(TARGET)).toEqual(['s-target']);

    // The daemon broadcasts the target layout before the session's new owner; the
    // reverse order hides the moved session, which filters through layouts.
    daemon.emit({
      event: 'workspace_layout_updated',
      workspace_layout: {
        workspace_id: TARGET,
        active_pane_id: 'pane-source',
        layout_json: JSON.stringify({
          type: 'split',
          split_id: 'split-1',
          direction: 'horizontal',
          ratio: 0.5,
          children: [
            { type: 'pane', pane_id: 'pane-target' },
            { type: 'pane', pane_id: 'pane-source' },
          ],
        }),
        panes: [
          pane('pane-target', 's-target', TARGET, 'target agent'),
          pane('pane-source', 's-source', TARGET, 'source agent'),
        ],
      },
    });
    daemon.emit({
      event: 'session_state_changed',
      session: session('s-source', 'source agent', TARGET),
    });

    expect(renderedPanes(TARGET)).toEqual(['s-source', 's-target']);
    // The checkpoint above rendered the source workspace holding this pane, so its
    // absence here is the move landing rather than a workspace that never mounted.
    expect(renderedWorkspaceIds()).toEqual([TARGET]);
    expect(paneSessionIds(document)).toEqual(['s-source', 's-target']);
  });

  it('gives a moved session the target layout when its new owner arrives after the layouts', async () => {
    const stay = { paneId: 'pane-stay', sessionId: 's-stay' };
    const stayToo = { paneId: 'pane-stay-too', sessionId: 's-stay-too' };
    const moved = { paneId: 'pane-moved', sessionId: 's-moved' };
    const target = { paneId: 'pane-target', sessionId: 's-target' };
    const { daemon } = await renderApp({
      initialState: {
        sessions: [
          session(stay.sessionId, 'staying agent', SOURCE),
          session(stayToo.sessionId, 'other staying agent', SOURCE),
          session(moved.sessionId, 'moved agent', SOURCE),
          session(target.sessionId, 'target agent', TARGET),
        ],
        workspaces: [
          workspaceWithPanes(SOURCE, 'Source', [stay, stayToo, moved]),
          workspaceWithPanes(TARGET, 'Target', [target]),
        ],
      },
    });

    expect(renderedPanes(SOURCE)).toEqual(['s-moved', 's-stay', 's-stay-too']);
    expect(renderedPanes(TARGET)).toEqual(['s-target']);

    daemon.emit({
      event: 'workspace_layout_updated',
      workspace_layout: workspaceWithPanes(SOURCE, 'Source', [stay, stayToo]).layout,
    });
    daemon.emit({
      event: 'workspace_layout_updated',
      workspace_layout: workspaceWithPanes(TARGET, 'Target', [target, moved]).layout,
    });

    expect(renderedPanes(SOURCE)).toEqual(['s-stay', 's-stay-too']);
    expect(renderedPanes(TARGET)).toEqual(['s-moved', 's-target']);

    daemon.emit({
      event: 'session_state_changed',
      session: session(moved.sessionId, 'moved agent', TARGET),
    });

    expect(paneSessionIds(document)).toEqual(['s-moved', 's-stay', 's-stay-too', 's-target']);
    expect(renderedPanes(SOURCE)).toEqual(['s-stay', 's-stay-too']);
    expect(renderedPanes(TARGET)).toEqual(['s-moved', 's-target']);
  });
});
