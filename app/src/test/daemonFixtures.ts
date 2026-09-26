import type { EventMessage } from './protocol';

export type DaemonSession = EventMessage<'session_state_changed'>['session'];
export type DaemonWorkspace = EventMessage<'workspace_state_changed'>['workspace'];
export type DaemonPane = EventMessage<'workspace_layout_updated'>['workspace_layout']['panes'][number];
export type DaemonSeed = EventMessage<'garden_seeds_updated'>['seeds'][number];
export type DaemonSeedDocument = NonNullable<EventMessage<'seed_document_get_result'>['document']>;
export type DaemonCrewMember = EventMessage<'crew_updated'>['members'][number];
export type DaemonPR = NonNullable<EventMessage<'prs_updated'>['prs']>[number];

export interface DaemonTile {
  tile_id: string;
  tile_kind: string;
  tile_params?: string;
  tile_session_id?: string;
}

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

export function dockTiles(root: unknown, tiles: DaemonTile[]): unknown {
  return tiles.reduce<unknown>((left, tile) => ({
    type: 'split',
    split_id: `split-${tile.tile_id}`,
    direction: 'vertical',
    ratio: 0.5,
    children: [left, { type: 'tile', ...tile }],
  }), root);
}

export function agentWorkspace(sessionId: string): DaemonWorkspace {
  const id = `workspace-${sessionId}`;
  return daemonWorkspace(
    id,
    { root: { type: 'pane', pane_id: `pane-${sessionId}` }, panes: [agentPane(sessionId, id)] },
    { title: sessionId, directory: `/tmp/${sessionId}` },
  );
}

export function daemonSeed(id: string, overrides: Partial<DaemonSeed> = {}): DaemonSeed {
  return {
    id,
    title: id,
    body: '',
    status: 'growing',
    step_slug: id,
    planter_session: '',
    planter_member: '',
    tender_session: '',
    tender_member: '',
    edges: [],
    template: false,
    gate: false,
    vars: [],
    rev: 1,
    ready: false,
    state_changed_at: AT,
    state_changed_at_exact: true,
    created_at: AT,
    updated_at: AT,
    ...overrides,
  };
}

export function seedDocument(seed: DaemonSeed, overrides: Partial<DaemonSeedDocument> = {}): DaemonSeedDocument {
  return { seed, artifacts: [], references: [], children: [], notes: [], notes_total: 0, tender_holds: false, ...overrides };
}

export function crewMember(id: string, overrides: Partial<DaemonCrewMember> = {}): DaemonCrewMember {
  return {
    id,
    revision: 1,
    charter_path: `/crew/${id}/CHARTER.md`,
    home_dir: `/crew/${id}`,
    awareness_dirs: [],
    resolved_agent: overrides.agent || 'claude',
    ...overrides,
  };
}

export function daemonPR(id: string, overrides: Partial<DaemonPR> = {}): DaemonPR {
  const number = overrides.number ?? 1;
  const repo = overrides.repo ?? 'victorarias/attn';
  return {
    id,
    host: 'github.com',
    repo,
    number,
    title: `PR ${id}`,
    url: `https://github.com/${repo}/pull/${number}`,
    author: 'someone',
    role: 'reviewer',
    state: 'waiting',
    reason: 'review_needed',
    last_updated: '2026-08-05T10:00:00Z',
    last_polled: '2026-08-05T10:00:00Z',
    muted: false,
    details_fetched: true,
    approved_by_me: false,
    has_new_changes: false,
    ...overrides,
  };
}
