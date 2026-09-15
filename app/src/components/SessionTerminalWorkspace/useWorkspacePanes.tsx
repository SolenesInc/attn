import { useMemo } from 'react';
import { type TerminalLayoutNode, type TileLeaf } from '../../types/workspace';
import { delegatesByDispatcher } from '../../utils/delegationLinks';
import type { SessionTerminalWorkspaceProps } from './workspaceTypes';
type Options = Pick<SessionTerminalWorkspaceProps, 'workspace' | 'activePaneId'> & {
  workspaceSessions: NonNullable<SessionTerminalWorkspaceProps['workspaceSessions']>;
  delegationSessions: NonNullable<SessionTerminalWorkspaceProps['delegationSessions']>;
};
export function useWorkspacePanes({
  workspace,
  workspaceSessions,
  delegationSessions,
  activePaneId,
}: Options) {
  const paneIds = useMemo(() => {
    const ids: string[] = [];
    if (!workspace.layoutTree) {
      return ids;
    }
    const collect = (node: TerminalLayoutNode) => {
      if (node.type === 'split') {
        collect(node.children[0]);
        collect(node.children[1]);
        return;
      }
      if (node.type === 'pane') {
        ids.push(node.paneId);
      }
    };
    collect(workspace.layoutTree);
    return ids;
  }, [workspace.layoutTree]);

  const sessionById = useMemo(
    () => new Map(workspaceSessions.map((entry) => [entry.id, entry] as const)),
    [workspaceSessions],
  );

  const delegationSessionById = useMemo(
    () => new Map(delegationSessions.map((entry) => [entry.id, entry] as const)),
    [delegationSessions],
  );

  const delegatesByDispatcherId = useMemo(
    () => delegatesByDispatcher(delegationSessions),
    [delegationSessions],
  );

  const agentPanes = useMemo(() => workspace.agents, [workspace.agents]);

  const agentPaneById = useMemo(
    () => new Map(agentPanes.map((pane) => [pane.id, pane])),
    [agentPanes],
  );

  const tileSessionOptions = useMemo(() => {
    const seen = new Set<string>();
    const options: { sessionId: string; label: string; state?: string }[] = [];
    for (const pane of agentPanes) {
      if (seen.has(pane.sessionId)) {
        continue;
      }
      seen.add(pane.sessionId);
      const session = sessionById.get(pane.sessionId);
      options.push({
        sessionId: pane.sessionId,
        label: session?.label || pane.title || pane.sessionId,
        ...(session?.state ? { state: session.state } : {}),
      });
    }
    return options;
  }, [agentPanes, sessionById]);

  const activePaneSessionId = useMemo(
    () => agentPaneById.get(activePaneId)?.sessionId ?? null,
    [agentPaneById, activePaneId],
  );

  const tileLeafById = useMemo(() => {
    const map = new Map<string, TileLeaf>();
    const walk = (node: TerminalLayoutNode | null) => {
      if (!node) {
        return;
      }
      if (node.type === 'split') {
        walk(node.children[0]);
        walk(node.children[1]);
        return;
      }
      if (node.type === 'tile') {
        map.set(node.tileId, node);
      }
    };
    walk(workspace.layoutTree);
    return map;
  }, [workspace.layoutTree]);

  return {
    tileLeafById,
    agentPaneById,
    agentPanes,
    sessionById,
    paneIds,
    delegationSessionById,
    delegatesByDispatcherId,
    tileSessionOptions,
    activePaneSessionId,
  };
}
