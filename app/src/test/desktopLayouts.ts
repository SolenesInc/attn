import { agentPane, daemonDesktop, daemonSession, soloDesktop, type DaemonDesktop } from './daemonFixtures';
import type { CommandMessage } from './protocol';
import { renderApp } from './renderApp';
import type { Reply, ScriptedDaemon } from './scriptedDaemon';

export function pane(sessionId: string) {
  return { type: 'pane', pane_id: `pane-${sessionId}` };
}

export function split(splitId: string, direction: 'vertical' | 'horizontal', children: unknown[], ratio = 0.5) {
  return { type: 'split', split_id: splitId, direction, ratio, children };
}

export function laidOutDesktop(root: unknown, sessionIds: string[], id = 'ws'): DaemonDesktop {
  return daemonDesktop(id, { root, panes: sessionIds.map((sessionId) => agentPane(sessionId, id)) }, { order_key: 'a' });
}

export async function renderDesktop(root: unknown, sessionIds: string[], others: string[] = []) {
  return renderApp({
    initialState: {
      sessions: [
        ...sessionIds.map((id) => daemonSession(id, { state: 'idle' })),
        ...others.map((id) => daemonSession(id, { state: 'idle' })),
      ],
      desktops: [laidOutDesktop(root, sessionIds), ...others.map((id) => soloDesktop(id))],
    },
    script: (daemon) => {
      daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true, last_seq: 0 }));
    },
  });
}

export function relayOut(
  daemon: { arrange: (change: (desktops: DaemonDesktop[]) => DaemonDesktop[]) => void },
  root: unknown,
  sessionIds: string[],
  { id = 'ws', active }: { id?: string; active?: string } = {},
) {
  daemon.arrange((desktops) => desktops.map((desktop) => {
    if (desktop.id !== id) return desktop;
    const next = laidOutDesktop(root, sessionIds, id);
    const kept = active ?? (JSON.stringify(root).includes(`"${desktop.active_pane_id}"`) ? desktop.active_pane_id : next.active_pane_id);
    return { ...next, active_pane_id: kept, revision: desktop.revision + 1 };
  }));
}

export function closeTileAnswer(
  daemon: ScriptedDaemon,
  command: CommandMessage<'desktop_close_tile'>,
  closedSessionId: string,
  root: unknown,
  sessionIds: string[],
  options: { id?: string; active?: string } = {},
): Reply[] {
  relayOut({ arrange: (change) => { daemon.arrangement.desktops = change(daemon.arrangement.desktops); } }, root, sessionIds, options);
  return [
    { event: 'session_unregistered', session: daemonSession(closedSessionId) },
    daemon.arrangement.answer(command),
    { event: 'profile_action_result', action: command.cmd, success: true },
  ];
}
