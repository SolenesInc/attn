import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openSession } from './test/appFixtures';
import { daemonSession } from './test/daemonFixtures';
import { pane, relayOut, renderDesktop, split } from './test/desktopLayouts';
import { pressShortcut } from './test/renderApp';

describe('desktop close focus', () => {
  it.each(['pane shortcut', 'session shortcut', 'sidebar action', 'clean exit'] as const)(
    'accepts the daemon’s previous tile selection after %s',
    async (entry) => {
      const { daemon } = await renderDesktop(
        split('ab', 'vertical', [pane('a'), split('bc', 'vertical', [pane('b'), pane('c')])]),
        ['a', 'b', 'c'],
      );
      for (const id of ['a', 'b', 'c']) await openSession(daemon, id);
      daemon.on('unregister', ({ id }) => {
        relayOut(daemon, split('ab', 'vertical', [pane('a'), pane('b')]), ['a', 'b'], { active: 'pane-b' });
        return { event: 'session_unregistered', session: daemonSession(id) };
      });

      if (entry === 'pane shortcut') pressShortcut('terminal.close');
      if (entry === 'session shortcut') pressShortcut('session.close');
      if (entry === 'sidebar action') {
        fireEvent.click(screen.getByRole('button', { name: 'Actions for c' }));
        fireEvent.click(screen.getByRole('menuitem', { name: /Close session/ }));
      }
      if (entry === 'clean exit') {
        daemon.emit({ event: 'session_exited', id: 'c', session_id: 'c', exit_code: 0 });
        relayOut(daemon, split('ab', 'vertical', [pane('a'), pane('b')]), ['a', 'b'], { active: 'pane-b' });
        daemon.emit({ event: 'session_unregistered', session: daemonSession('c') });
      }
      await daemon.idle();

      expect(daemon.sentOf('unregister')).toEqual(entry === 'clean exit' ? [] : [{ cmd: 'unregister', id: 'c' }]);
      expect(document.querySelector('[data-session-terminal-desktop="ws"]')).toHaveAttribute('data-active-leaf-id', 'pane-b');
      expect(document.activeElement?.closest('[data-pane-id]')).toHaveAttribute('data-pane-id', 'pane-b');
      expect(daemon.sentOf('desktop_show_leaf')).toEqual([]);
      expect(daemon.sentOf('desktop_show_session').map((command) => command.session_id)).toEqual(['a', 'b', 'c']);
    },
  );
});
