import type { Session } from '../store/sessions';
import { findPaneInDirection, hasPane, type TerminalLayoutNode } from '../types/workspace';

export interface WorkspacePaneSelection {
  topology: string;
  activePaneId: string;
  history: string[];
  closingFallback?: string;
}

export type WorkspacePaneSelections = Record<string, WorkspacePaneSelection>;

function topology(node: TerminalLayoutNode | null | undefined): unknown {
  if (!node) return null;
  return node.type === 'split'
    ? [node.splitId, node.direction, topology(node.children[0]), topology(node.children[1])]
    : node.type === 'pane'
      ? node.paneId
      : node.tileId;
}

function signature(session: Session): string {
  return JSON.stringify([
    topology(session.workspace.layoutTree),
    session.workspace.agents.map((p) => p.id),
  ]);
}

function daemonPane(session: Session): string {
  const layout = session.workspace.layoutTree;
  if (!layout) return '';
  return hasPane(layout, session.daemonActivePaneId)
    ? session.daemonActivePaneId
    : session.workspace.agents[0]?.id || '';
}

function visit(state: WorkspacePaneSelection, activePaneId: string): WorkspacePaneSelection {
  if (
    state.activePaneId === activePaneId &&
    state.history[state.history.length - 1] === activePaneId
  )
    return state;
  return {
    ...state,
    activePaneId,
    history: activePaneId
      ? [...state.history.filter((id) => id !== activePaneId), activePaneId]
      : state.history,
  };
}

export function reconcileWorkspacePanes(
  previous: WorkspacePaneSelections,
  sessions: Session[],
): WorkspacePaneSelections {
  const next: WorkspacePaneSelections = {};
  for (const session of sessions) {
    const key = session.workspaceId;
    if (next[key]) continue;
    const current = previous[key];
    const nextTopology = signature(session);
    const layout = session.workspace.layoutTree;
    const changed = current?.topology !== nextTopology;
    const valid = (id: string | undefined) => Boolean(id && layout && hasPane(layout, id));
    const activePaneId =
      changed && valid(current?.closingFallback)
        ? current!.closingFallback!
        : !changed && valid(current?.activePaneId)
          ? current!.activePaneId
          : daemonPane(session);
    const liveHistory = (current?.history ?? []).filter(valid);
    next[key] = visit(
      {
        topology: nextTopology,
        activePaneId: current?.activePaneId ?? '',
        history: liveHistory,
        closingFallback: changed ? undefined : current?.closingFallback,
      },
      activePaneId,
    );
  }
  return next;
}

export function selectWorkspacePane(
  selections: WorkspacePaneSelections,
  session: Session,
  paneId: string,
): WorkspacePaneSelections {
  const current =
    selections[session.workspaceId] ?? reconcileWorkspacePanes({}, [session])[session.workspaceId];
  return { ...selections, [session.workspaceId]: visit(current, paneId) };
}

export function activeWorkspacePane(
  selections: WorkspacePaneSelections,
  session: Session | null | undefined,
): string {
  if (!session) return '';
  return selections[session.workspaceId]?.activePaneId ?? daemonPane(session);
}

export function closingPaneFallback(
  selections: WorkspacePaneSelections,
  session: Session,
  paneId: string,
): string {
  const layout = session.workspace.layoutTree;
  if (!paneId || !layout) return '';
  const current = selections[session.workspaceId];
  const active = activeWorkspacePane(selections, session);
  if (active !== paneId && hasPane(layout, active)) return active;
  return (
    [...(current?.history ?? [])].reverse().find((id) => id !== paneId && hasPane(layout, id)) ||
    findPaneInDirection(layout, paneId, 'left') ||
    session.workspace.agents[0]?.id ||
    ''
  );
}
