import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import attn from '../../internal/colors/schemes/attn.json';
import { agentPane, daemonSession, daemonDesktop, DEFAULT_PROFILE_ID } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';
import { initialState } from './test/scriptedDaemon';

const ANSI = [
  'black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white',
  'bright_black', 'bright_red', 'bright_green', 'bright_yellow', 'bright_blue', 'bright_magenta', 'bright_cyan', 'bright_white',
] as const;
const THEME = {
  cmd: 'set_terminal_theme',
  foreground: attn.dark.terminal.fg,
  background: attn.dark.terminal.bg,
  cursor: attn.dark.terminal.cursor,
  ansi_palette: ANSI.map((name) => attn.dark.terminal[name]),
};

describe('App daemon reconnect', () => {
  it('holds its terminal theme until the daemon handshake completes', async () => {
    const { daemon } = await renderApp({ initialState: false });
    await daemon.idle();
    expect(daemon.sentOf('set_terminal_theme')).toEqual([]);

    daemon.emit(initialState());
    await daemon.idle();

    expect(daemon.sentOf('set_terminal_theme')).toEqual([THEME]);
  });

  it('sends the terminal colors of the reconnected daemon\'s settings once its handshake completes', async () => {
    const { daemon } = await renderApp();
    await gesture(daemon, () => daemon.disconnect());
    daemon.on('client_hello', () => undefined);

    const reconnected = await daemon.reconnect();
    await daemon.idle();
    expect(reconnected.sent.filter(({ cmd }) => cmd === 'set_terminal_theme')).toEqual([]);

    reconnected.emit(initialState({ settings: { theme: 'light' } }));
    await daemon.idle();
    expect(reconnected.sent.filter(({ cmd }) => cmd === 'set_terminal_theme')).toEqual([expect.objectContaining({ background: attn.light.terminal.bg })]);
  });

  it('tells a reconnected daemon its profile and theme, and asks again for what it shows', async () => {
    const desktop = daemonDesktop('ws', {
      root: {
        type: 'split',
        split_id: 'split-a',
        direction: 'vertical',
        ratio: 0.5,
        children: [
          { type: 'pane', pane_id: 'pane-s1' },
          { type: 'tile', tile_id: 'tile-md', tile_kind: 'markdown', tile_params: '/tmp/notes.md' },
        ],
      },
      panes: [agentPane('s1', 'ws')],
    }, { name: 'notes desk' });
    const { daemon } = await renderApp({
      initialState: { sessions: [daemonSession('s1')], desktops: [desktop] },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
    await daemon.idle();

    const reconnected = await daemon.reconnect();
    await daemon.idle();

    expect(reconnected.sent).toEqual(expect.arrayContaining([
      expect.objectContaining({ cmd: 'client_hello', profile_id: DEFAULT_PROFILE_ID }),
      THEME,
      expect.objectContaining({ cmd: 'session_messages_get', session_id: 's1' }),
    ]));
  });
});
