import type { WorkspaceAgentProps } from './workspaceTypes';

type PaneNotice = { tone: 'spawning' | 'failed'; text: string };

export function paneNotice(
  agentPane: WorkspaceAgentProps['agentPane'],
  paneSession: WorkspaceAgentProps['paneSession'],
  paneTitle: string,
): PaneNotice | null {
  const paneStatus = agentPane.status || 'ready';
  if (paneStatus === 'failed') {
    return { tone: 'failed', text: agentPane.error || 'Session failed to start' };
  }
  if (paneStatus === 'spawning') {
    return { tone: 'spawning', text: `Starting ${paneTitle}...` };
  }
  if (!paneSession && paneStatus === 'ready') {
    return { tone: 'spawning', text: `Waiting for ${paneTitle}...` };
  }
  return null;
}
