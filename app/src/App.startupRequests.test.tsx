import { describe, expect, it } from 'vitest';
import { renderApp } from './test/renderApp';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';

describe('App startup requests', () => {
  it('asks for its startup state once and then stays quiet', async () => {
    const { daemon } = await renderApp({
      initialState: {
        sessions: [daemonSession('s1', { state: 'waiting_input' }), daemonSession('s2')],
        workspaces: [agentWorkspace('s1'), agentWorkspace('s2')],
      },
    });
    await daemon.idle();

    expect(daemon.sent.map((command) => command.cmd)).toEqual([
      'client_hello',
      'set_terminal_theme',
      'set_client_presence',
      'notification_list',
      'get_presentations',
    ]);

    await daemon.idle();
    expect(daemon.sent).toHaveLength(5);
    expect(daemon.connections).toHaveLength(1);
  });
});
