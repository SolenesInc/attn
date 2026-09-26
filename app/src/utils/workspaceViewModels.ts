import type { Desktop } from '../types/generated';
import {
  collectLayoutLeaves,
  parseLayoutJSON,
  type TileLeaf,
} from '../types/workspace';
import { desktopLabel, orderedDesktops } from './desktops';

export interface WorkspaceViewSession {
  id: string;
  label: string;
  workspaceId?: string;
  workspace_id?: string;
  cwd?: string;
  directory?: string;
  endpointId?: string;
  endpoint_id?: string;
  state?: string;
}

export interface WorkspaceViewWorkspace {
  id: string;
  title: string;
  directory: string;
  status?: string;
  endpointId?: string;
  endpoint_id?: string;
  layout?: {
    layout_json?: string;
    panes?: Array<{
      pane_id?: string;
      session_id?: string;
      kind?: string;
      status?: string;
    }>;
  };
}

export type WorkspaceChild<TSession extends WorkspaceViewSession = WorkspaceViewSession> =
  | {
    kind: 'session';
    id: string;
    paneId?: string;
    session: TSession;
  }
  | {
    kind: 'tile';
    id: string;
    tile: TileLeaf;
  };

export interface WorkspaceWithSessions<TSession extends WorkspaceViewSession = WorkspaceViewSession> {
  id: string;
  title: string;
  directory: string;
  status?: string;
  endpointId?: string;
  sessions: TSession[];
  children: WorkspaceChild<TSession>[];
  firstSessionId: string | null;
  focusedSessionId: string | null;
  hasUnresolvedAgentPanes: boolean;
}

interface WorkspaceViewModelOptions {
  focusedSessionIdByWorkspace?: Record<string, string | null | undefined>;
}



function sessionEndpointId(session: WorkspaceViewSession): string | undefined {
  return session.endpointId || session.endpoint_id;
}

function workspaceEndpointId(workspace: WorkspaceViewWorkspace): string | undefined {
  return workspace.endpointId || workspace.endpoint_id;
}





function toWorkspaceViewModel<TSession extends WorkspaceViewSession>(
  workspace: WorkspaceViewWorkspace,
  sessions: TSession[],
  liveSessionIds: ReadonlySet<string>,
  options: WorkspaceViewModelOptions,
): WorkspaceWithSessions<TSession> {
  const children = workspaceChildren(workspace, sessions);
  const firstSessionId = children.find((child) => child.kind === 'session')?.session.id ?? null;
  const requestedFocus = options.focusedSessionIdByWorkspace?.[workspace.id] || null;
  const focusedSessionId = requestedFocus && sessions.some((session) => session.id === requestedFocus)
    ? requestedFocus
    : firstSessionId;
  const layoutPaneIds = new Set(
    collectLayoutLeaves(parseLayoutJSON(workspace.layout?.layout_json || ''))
      .filter((leaf) => leaf.type === 'pane')
      .map((leaf) => leaf.paneId),
  );
  const hasUnresolvedAgentPanes = (workspace.layout?.panes || []).some((pane) => (
    pane.kind !== 'tile'
    && (pane.status === 'spawning' || pane.status === 'failed')
    && Boolean(pane.pane_id && layoutPaneIds.has(pane.pane_id))
    && Boolean(pane.session_id && !liveSessionIds.has(pane.session_id))
  ));

  return {
    id: workspace.id,
    title: workspace.title,
    directory: workspace.directory,
    status: workspace.status,
    endpointId: workspaceEndpointId(workspace) || (sessions[0] ? sessionEndpointId(sessions[0]) : undefined),
    sessions,
    children,
    firstSessionId,
    focusedSessionId,
    hasUnresolvedAgentPanes,
  };
}

function workspaceChildren<TSession extends WorkspaceViewSession>(
  workspace: WorkspaceViewWorkspace,
  sessions: TSession[],
): WorkspaceChild<TSession>[] {
  const sessionById = new Map(sessions.map((session) => [session.id, session]));
  const sessionIdByPaneId = new Map(
    (workspace.layout?.panes || [])
      .filter((pane): pane is { pane_id: string; session_id: string } => Boolean(pane.pane_id && pane.session_id))
      .map((pane) => [pane.pane_id, pane.session_id]),
  );
  const representedSessionIds = new Set<string>();
  const children: WorkspaceChild<TSession>[] = [];

  for (const leaf of collectLayoutLeaves(parseLayoutJSON(workspace.layout?.layout_json || ''))) {
    if (leaf.type === 'tile') {
      children.push({ kind: 'tile', id: leaf.tileId, tile: leaf });
      continue;
    }
    const sessionId = sessionIdByPaneId.get(leaf.paneId);
    const session = sessionId ? sessionById.get(sessionId) : undefined;
    if (session && !representedSessionIds.has(session.id)) {
      representedSessionIds.add(session.id);
      children.push({ kind: 'session', id: session.id, paneId: leaf.paneId, session });
    }
  }

  for (const session of sessions) {
    if (!representedSessionIds.has(session.id)) {
      children.push({ kind: 'session', id: session.id, session });
    }
  }
  return children;
}

export function firstSessionIdForWorkspace<TSession extends WorkspaceViewSession>(
  workspace: WorkspaceWithSessions<TSession> | undefined | null,
): string | null {
  return workspace?.firstSessionId ?? null;
}


export const UNPLACED_GROUP_ID = 'unplaced';

export function buildDesktopViewModels<TSession extends WorkspaceViewSession>(
  desktops: Desktop[],
  sessions: TSession[],
): WorkspaceWithSessions<TSession>[] {
  const liveSessionIds = new Set(sessions.map((session) => session.id));
  const desktopIdBySessionId = new Map(
    desktops.flatMap((desktop) => desktop.panes.map((pane) => [pane.session_id, desktop.id] as const)),
  );
  const onDesktop = orderedDesktops(desktops).map((desktop) =>
    toWorkspaceViewModel(
      {
        id: desktop.id,
        title: desktopLabel(desktop, desktops),
        directory: '',
        layout: { layout_json: desktop.tree_json, panes: desktop.panes },
      },
      sessions.filter((session) => desktopIdBySessionId.get(session.id) === desktop.id),
      liveSessionIds,
      {},
    ),
  );
  const unplaced = toWorkspaceViewModel(
    { id: UNPLACED_GROUP_ID, title: 'Unplaced', directory: '' },
    sessions.filter((session) => !desktopIdBySessionId.has(session.id)),
    liveSessionIds,
    {},
  );
  return [...onDesktop, unplaced];
}
