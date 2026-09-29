import type { Desktop } from '../types/generated';
import {
  collectLayoutLeaves,
  parseLayoutJSON,
  type TileLeaf,
} from '../types/desktop';
import { defaultDesktopLabel, desktopLabel, desktopNumber, orderedDesktops } from './desktops';

export interface DesktopViewSession {
  id: string;
  label: string;
  cwd?: string;
  directory?: string;
  endpointId?: string;
  endpoint_id?: string;
  state?: string;
}

export interface DesktopViewDesktop {
  id: string;
  title: string;
  directory: string;
  status?: string;
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

export type DesktopChild<TSession extends DesktopViewSession = DesktopViewSession> =
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

export interface DesktopWithSessions<TSession extends DesktopViewSession = DesktopViewSession> {
  id: string;
  title: string;
  desktop?: { name: string; defaultLabel: string; number?: number };
  directory: string;
  status?: string;
  sessions: TSession[];
  children: DesktopChild<TSession>[];
  firstSessionId: string | null;
  focusedSessionId: string | null;
  hasUnresolvedAgentPanes: boolean;
}

interface DesktopViewModelOptions {
  focusedSessionIdByDesktop?: Record<string, string | null | undefined>;
}



function toDesktopViewModel<TSession extends DesktopViewSession>(
  desktopView: DesktopViewDesktop,
  sessions: TSession[],
  liveSessionIds: ReadonlySet<string>,
  options: DesktopViewModelOptions,
): DesktopWithSessions<TSession> {
  const children = desktopChildren(desktopView, sessions);
  const firstSessionId = children.find((child) => child.kind === 'session')?.session.id ?? null;
  const requestedFocus = options.focusedSessionIdByDesktop?.[desktopView.id] || null;
  const focusedSessionId = requestedFocus && sessions.some((session) => session.id === requestedFocus)
    ? requestedFocus
    : firstSessionId;
  const layoutPaneIds = new Set(
    collectLayoutLeaves(parseLayoutJSON(desktopView.layout?.layout_json || ''))
      .filter((leaf) => leaf.type === 'pane')
      .map((leaf) => leaf.paneId),
  );
  const hasUnresolvedAgentPanes = (desktopView.layout?.panes || []).some((pane) => (
    pane.kind !== 'tile'
    && (pane.status === 'spawning' || pane.status === 'failed')
    && Boolean(pane.pane_id && layoutPaneIds.has(pane.pane_id))
    && Boolean(pane.session_id && !liveSessionIds.has(pane.session_id))
  ));

  return {
    id: desktopView.id,
    title: desktopView.title,
    directory: desktopView.directory,
    status: desktopView.status,
    sessions,
    children,
    firstSessionId,
    focusedSessionId,
    hasUnresolvedAgentPanes,
  };
}

function desktopChildren<TSession extends DesktopViewSession>(
  desktopView: DesktopViewDesktop,
  sessions: TSession[],
): DesktopChild<TSession>[] {
  const sessionById = new Map(sessions.map((session) => [session.id, session]));
  const sessionIdByPaneId = new Map(
    (desktopView.layout?.panes || [])
      .filter((pane): pane is { pane_id: string; session_id: string } => Boolean(pane.pane_id && pane.session_id))
      .map((pane) => [pane.pane_id, pane.session_id]),
  );
  const representedSessionIds = new Set<string>();
  const children: DesktopChild<TSession>[] = [];

  for (const leaf of collectLayoutLeaves(parseLayoutJSON(desktopView.layout?.layout_json || ''))) {
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

export function firstSessionIdForDesktop<TSession extends DesktopViewSession>(
  desktopView: DesktopWithSessions<TSession> | undefined | null,
): string | null {
  return desktopView?.firstSessionId ?? null;
}


export const UNPLACED_GROUP_ID = 'unplaced';

export function buildDesktopViewModels<TSession extends DesktopViewSession>(
  desktops: Desktop[],
  sessions: TSession[],
): DesktopWithSessions<TSession>[] {
  const liveSessionIds = new Set(sessions.map((session) => session.id));
  const desktopIdBySessionId = new Map(
    desktops.flatMap((desktop) => desktop.panes.map((pane) => [pane.session_id, desktop.id] as const)),
  );
  const onDesktop = orderedDesktops(desktops).map((desktop) => ({
    ...toDesktopViewModel(
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
    desktop: { name: desktop.name.trim(), defaultLabel: defaultDesktopLabel(desktop, desktops), number: desktopNumber(desktop, desktops) },
  }));
  const unplacedSessions = sessions.filter((session) => !desktopIdBySessionId.has(session.id));
  if (unplacedSessions.length === 0) return onDesktop;
  const unplaced = toDesktopViewModel(
    { id: UNPLACED_GROUP_ID, title: 'Not on a desktop', directory: '' },
    unplacedSessions,
    liveSessionIds,
    {},
  );
  return [...onDesktop, unplaced];
}
