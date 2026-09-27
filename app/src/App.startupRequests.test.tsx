import { describe, expect, it } from 'vitest';
import { renderApp } from './test/renderApp';
import { soloDesktop, daemonSession, defaultProfile } from './test/daemonFixtures';

const sessions = [daemonSession('s1', { state: 'waiting_input' }), daemonSession('s2')];
const desktops = [soloDesktop('s1'), soloDesktop('s2')];

describe('App startup requests', () => {
  it('asks for its startup state once and then stays quiet', async () => {
    const { daemon } = await renderApp({ initialState: { sessions, desktops } });
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

  it('attaches only the agent on the desktop it starts on', async () => {
    const { daemon } = await renderApp({ initialState: { sessions, desktops, profiles: [defaultProfile('desktop-s1')] } });
    await daemon.idle();

    expect(daemon.sentOf('attach_session').map(({ id }) => id)).toEqual(['s1']);
    const settled = daemon.sent.length;
    await daemon.idle();
    expect(daemon.sent).toHaveLength(settled);
  });
});
