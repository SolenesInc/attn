import { agentPane, agentWorkspace, daemonSession, daemonWorkspace, type DaemonWorkspace } from './daemonFixtures';
import { renderApp } from './renderApp';

export function pane(sessionId: string) {
  return { type: 'pane', pane_id: `pane-${sessionId}` };
}

export function split(splitId: string, direction: 'vertical' | 'horizontal', children: unknown[], ratio = 0.5) {
  return { type: 'split', split_id: splitId, direction, ratio, children };
}

export function splitWorkspace(root: unknown, sessionIds: string[]): DaemonWorkspace {
  return daemonWorkspace('ws', { root, panes: sessionIds.map((id) => agentPane(id, 'ws')) }, { title: 'ws' });
}

export async function renderWorkspace(root: unknown, sessionIds: string[], others: string[] = []) {
  const view = await renderApp({
    initialState: {
      sessions: [
        ...sessionIds.map((id) => daemonSession(id, { workspace_id: 'ws', state: 'idle' })),
        ...others.map((id) => daemonSession(id, { state: 'idle' })),
      ],
      workspaces: [splitWorkspace(root, sessionIds), ...others.map(agentWorkspace)],
    },
  });
  view.daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true, last_seq: 0 }));
  return view;
}
