import { useMemo } from 'react';
import { type TerminalLayoutNode, type TileLeaf } from '../../types/desktop';
import { delegatesByDispatcher } from '../../utils/delegationLinks';
import type { SessionTerminalDesktopProps } from './desktopTypes';
type Options = Pick<SessionTerminalDesktopProps, 'terminalState'> & {
  desktopSessions: NonNullable<SessionTerminalDesktopProps['desktopSessions']>;
  delegationSessions: NonNullable<SessionTerminalDesktopProps['delegationSessions']>;
};
export function useDesktopPanes({
  terminalState,
  desktopSessions,
  delegationSessions,
}: Options) {
  const paneIds = useMemo(() => {
    const ids: string[] = [];
    if (!terminalState.layoutTree) {
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
    collect(terminalState.layoutTree);
    return ids;
  }, [terminalState.layoutTree]);

  const sessionById = useMemo(
    () => new Map(desktopSessions.map((entry) => [entry.id, entry] as const)),
    [desktopSessions],
  );

  const delegationSessionById = useMemo(
    () => new Map(delegationSessions.map((entry) => [entry.id, entry] as const)),
    [delegationSessions],
  );

  const delegatesByDispatcherId = useMemo(
    () => delegatesByDispatcher(delegationSessions),
    [delegationSessions],
  );

  const agentPanes = useMemo(() => terminalState.agents, [terminalState.agents]);

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
    walk(terminalState.layoutTree);
    return map;
  }, [terminalState.layoutTree]);

  return {
    tileLeafById,
    agentPaneById,
    agentPanes,
    sessionById,
    paneIds,
    delegationSessionById,
    delegatesByDispatcherId,
    tileSessionOptions,
  };
}
