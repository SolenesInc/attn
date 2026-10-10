import type { EventMessage } from './protocol';

export type DaemonSession = EventMessage<'session_state_changed'>['session'];
export type DaemonProfile = EventMessage<'profiles_changed'>['profiles'][number];
export type DaemonDesktop = EventMessage<'profile_arrangement_changed'>['desktops'][number];
export type DaemonPane = DaemonDesktop['panes'][number];
export type DaemonSeed = EventMessage<'garden_seeds_updated'>['seeds'][number];
export type DaemonSeedDocument = NonNullable<EventMessage<'seed_document_get_result'>['document']>;
export type DaemonCrewMember = EventMessage<'crew_updated'>['members'][number];
export type DaemonPR = NonNullable<EventMessage<'prs_updated'>['prs']>[number];
export type DaemonEndpoint = EventMessage<'endpoints_updated'>['endpoints'][number];

export interface DaemonTile {
  tile_id: string;
  tile_kind: string;
  tile_params?: string;
  tile_session_id?: string;
}

const AT = '2026-01-01T00:00:00Z';

export const DEFAULT_PROFILE_ID = 'profile-default';
export const DEFAULT_DESKTOP_ID = 'desktop-1';

export function defaultProfile(currentDesktopId: string, overrides: Partial<DaemonProfile> = {}): DaemonProfile {
  return { id: DEFAULT_PROFILE_ID, name: 'Default', current_desktop_id: currentDesktopId, revision: 1, ...overrides };
}

export function daemonSession(id: string, overrides: Partial<DaemonSession> = {}): DaemonSession {
  return {
    id,
    label: id,
    agent: 'claude',
    directory: `/tmp/${id}`,
    profile_id: DEFAULT_PROFILE_ID,
    state: 'working',
    last_seen: AT,
    state_since: AT,
    state_updated_at: AT,
    ...overrides,
  };
}

export function agentPane(sessionId: string, desktopId: string, runtimeId = sessionId): DaemonPane {
  return {
    pane_id: `pane-${sessionId}`,
    session_id: sessionId,
    runtime_id: runtimeId,
    desktop_id: desktopId,
    kind: 'agent',
    status: 'ready',
    title: sessionId,
  } as DaemonPane;
}

export function daemonDesktop(
  id: string,
  layout: { root: unknown; panes?: DaemonPane[] },
  overrides: Partial<DaemonDesktop> = {},
): DaemonDesktop {
  const panes = layout.panes ?? [];
  return {
    id,
    profile_id: DEFAULT_PROFILE_ID,
    name: '',
    order_key: id,
    tree_json: layout.root ? JSON.stringify(layout.root) : '',
    active_pane_id: panes[0]?.pane_id ?? '',
    revision: 1,
    panes,
    ...overrides,
  };
}

export function emptyDesktop(id: string, overrides: Partial<DaemonDesktop> = {}): DaemonDesktop {
  return daemonDesktop(id, { root: null }, overrides);
}

export function splitDesktop(id: string, sessionIds: string[], overrides: Partial<DaemonDesktop> = {}): DaemonDesktop {
  const leaves = sessionIds.map((sessionId) => ({ type: 'pane', pane_id: `pane-${sessionId}` }));
  return daemonDesktop(id, {
    root: leaves.length === 1 ? leaves[0] : { type: 'split', split_id: `split-${id}`, direction: 'vertical', ratio: 0.5, children: leaves },
    panes: sessionIds.map((sessionId) => agentPane(sessionId, id)),
  }, overrides);
}

export function daemonEndpoint(id: string, overrides: Partial<DaemonEndpoint> = {}): DaemonEndpoint {
  const name = overrides.name ?? 'gpu-box';
  return { id, name, ssh_target: `me@${name}`, status: 'connected', enabled: true, ...overrides };
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

export function desktopWithTiles(tiles: DaemonTile[], overrides: Partial<DaemonDesktop> = {}): DaemonDesktop {
  const id = overrides.id ?? DEFAULT_DESKTOP_ID;
  return daemonDesktop(
    id,
    { root: dockTiles({ type: 'pane', pane_id: 'pane-s1' }, tiles), panes: [agentPane('s1', id)] },
    overrides,
  );
}

export function soloDesktop(sessionId: string, overrides: Partial<DaemonDesktop> = {}): DaemonDesktop {
  return terminalDesktop(sessionId, sessionId, overrides);
}

// A pane whose terminal has its own id. soloDesktop's terminal reuses the session id,
// as panes from before terminal ids do.
export function terminalDesktop(sessionId: string, runtimeId: string, overrides: Partial<DaemonDesktop> = {}): DaemonDesktop {
  const id = overrides.id ?? `desktop-${sessionId}`;
  return daemonDesktop(id, { root: { type: 'pane', pane_id: `pane-${sessionId}` }, panes: [agentPane(sessionId, id, runtimeId)] }, overrides);
}

export function daemonSeed(id: string, overrides: Partial<DaemonSeed> = {}): DaemonSeed {
  return {
    id,
    profile_id: DEFAULT_PROFILE_ID,
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
    key: id,
    name: id[0].toUpperCase() + id.slice(1),
    revision: 1,
    retired: false,
    charter_path: `/crew/${id}/CHARTER.md`,
    home_dir: `/crew/${id}`,
    awareness_dirs: [],
    profile_id: DEFAULT_PROFILE_ID,
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
