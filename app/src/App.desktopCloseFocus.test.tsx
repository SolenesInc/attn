import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openSession } from './test/appFixtures';
import { agentPane, daemonDesktop, daemonSession, soloDesktop } from './test/daemonFixtures';
import { closeTileAnswer, laidOutDesktop, pane, relayOut, renderDesktop, split } from './test/desktopLayouts';
import { pressShortcut, renderApp } from './test/renderApp';

describe('desktop close focus', () => {
  it.each(['pane shortcut', 'session shortcut', 'sidebar action', 'clean exit'] as const)(
    'accepts the daemon’s previous tile selection after %s',
    async (entry) => {
      const { daemon } = await renderDesktop(
        split('ab', 'vertical', [pane('a'), split('bc', 'vertical', [pane('b'), pane('c')])]),
        ['a', 'b', 'c'],
      );
      for (const id of ['a', 'b', 'c']) await openSession(daemon, id);
      const remaining = split('ab', 'vertical', [pane('a'), pane('b')]);
      daemon.on('unregister', ({ id }) => {
        relayOut(daemon, remaining, ['a', 'b'], { active: 'pane-b' });
        return { event: 'session_unregistered', session: daemonSession(id) };
      });
      daemon.on('desktop_close_tile', (command) => closeTileAnswer(daemon, command, 'c', remaining, ['a', 'b'], { active: 'pane-b' }));

      if (entry === 'pane shortcut') pressShortcut('terminal.close', document.querySelector('[data-pane-id="pane-c"] .terminal-container')!);
      if (entry === 'session shortcut') pressShortcut('session.close');
      if (entry === 'sidebar action') {
        fireEvent.click(screen.getByRole('button', { name: 'Actions for c' }));
        fireEvent.click(screen.getByRole('menuitem', { name: /Close session/ }));
      }
      if (entry === 'clean exit') {
        daemon.emit({ event: 'session_exited', id: 'c', session_id: 'c', exit_code: 0 });
      }
      await daemon.idle();

      expect(daemon.sent.filter(({ cmd }) => cmd === 'unregister' || cmd === 'desktop_close_tile')).toEqual([
        entry === 'pane shortcut'
          ? { cmd: 'desktop_close_tile', request_id: expect.any(String), desktop_id: 'ws', tile_id: 'pane-c' }
          : { cmd: 'unregister', id: 'c' },
      ]);
      expect(document.querySelector('[data-session-terminal-desktop="ws"]')).toHaveAttribute('data-active-leaf-id', 'pane-b');
      expect(document.activeElement?.closest('[data-pane-id]')).toHaveAttribute('data-pane-id', 'pane-b');
      expect(daemon.sentOf('desktop_show_leaf')).toEqual([]);
      expect(daemon.sentOf('desktop_show_session').map((command) => command.session_id)).toEqual(['a', 'b', 'c']);
    },
  );
});

describe('closing a session that two tiles show', () => {
  it('closes only the tile the session shortcut stands on', async () => {
    const { daemon } = await renderApp({
      initialState: {
        sessions: [daemonSession('a', { state: 'idle' }), daemonSession('b', { state: 'idle' })],
        desktops: [
          laidOutDesktop(pane('a'), ['a']),
          daemonDesktop('other', { root: { type: 'pane', pane_id: 'pane-a2' }, panes: [{ ...agentPane('a', 'other', 'term-2'), pane_id: 'pane-a2' }] }, { order_key: 'b' }),
          soloDesktop('b'),
        ],
      },
      script: (d) => {
        d.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true, last_seq: 0 }));
      },
    });
    await openSession(daemon, 'a');

    pressShortcut('session.close');
    await daemon.idle();

    expect(daemon.sent.filter(({ cmd }) => cmd === 'unregister' || cmd === 'desktop_close_tile')).toEqual([
      { cmd: 'desktop_close_tile', request_id: expect.any(String), desktop_id: 'ws', tile_id: 'pane-a' },
    ]);
  });
});
