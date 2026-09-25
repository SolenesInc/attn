import type { EventMessage } from './protocol';

export type DaemonSession = EventMessage<'session_state_changed'>['session'];
export type DaemonWorkspace = EventMessage<'workspace_state_changed'>['workspace'];
export type DaemonPane = EventMessage<'workspace_layout_updated'>['workspace_layout']['panes'][number];

const AT = '2026-01-01T00:00:00Z';

export function daemonSession(id: string, overrides: Partial<DaemonSession> = {}): DaemonSession {
  return {
    id,
    label: id,
    agent: 'claude',
    directory: `/tmp/${id}`,
    workspace_id: `workspace-${id}`,
    state: 'working',
    last_seen: AT,
    state_since: AT,
    state_updated_at: AT,
    ...overrides,
  };
}

export function agentPane(sessionId: string, workspaceId: string): DaemonPane {
  return {
    pane_id: `pane-${sessionId}`,
    session_id: sessionId,
    runtime_id: sessionId,
    workspace_id: workspaceId,
    kind: 'agent',
    status: 'ready',
    title: sessionId,
  };
}

export function daemonWorkspace(
  id: string,
  layout: { root: unknown; panes?: DaemonPane[] },
  overrides: Partial<DaemonWorkspace> = {},
): DaemonWorkspace {
  const panes = layout.panes ?? [];
  return {
    id,
    title: id,
    directory: '/tmp',
    status: 'idle',
    muted: false,
    pinned: false,
    rank: id,
    layout: {
      workspace_id: id,
      active_pane_id: panes[0]?.pane_id ?? '',
      layout_json: JSON.stringify(layout.root),
      panes,
    },
    ...overrides,
  };
}

export function agentWorkspace(sessionId: string): DaemonWorkspace {
  const id = `workspace-${sessionId}`;
  return daemonWorkspace(
    id,
    { root: { type: 'pane', pane_id: `pane-${sessionId}` }, panes: [agentPane(sessionId, id)] },
    { title: sessionId, directory: `/tmp/${sessionId}` },
  );
}
