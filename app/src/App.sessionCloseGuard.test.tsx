import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openSession } from './test/appFixtures';
import { agentPane, daemonDesktop, soloDesktop, daemonSession, type DaemonSession } from './test/daemonFixtures';
import { laidOutDesktop, pane } from './test/desktopLayouts';
import { pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

async function renderOrchestrator(overrides: Partial<DaemonSession> = {}) {
  const rendered = await renderApp({
    initialState: {
      sessions: [daemonSession('s1', { label: 'orchestrator', ...overrides })],
      desktops: [soloDesktop('s1')],
    },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Open orchestrator' }));
  return rendered;
}

function closeFromSidebar() {
  fireEvent.click(screen.getByRole('button', { name: 'Actions for orchestrator' }));
  fireEvent.click(screen.getByRole('menuitem', { name: /Close session/ }));
}

function pressCmdW() {
  fireEvent.keyDown(window, { key: 'w', metaKey: true });
}

function closeCommands(daemon: ScriptedDaemon) {
  return daemon.sent.filter(
    (command) => command.cmd === 'desktop_remove_leaf' || command.cmd === 'unregister',
  );
}

function toast() {
  return screen.queryByRole('alert');
}

describe('chief and crew sessions are protected from close', () => {
  it.each([
    ['the close action', closeFromSidebar],
    ['⌘W', pressCmdW],
  ])('no-ops %s on the chief session and shows the protected hint', async (_, close) => {
    const { daemon } = await renderOrchestrator({ chief_of_staff: true });

    close();

    expect(closeCommands(daemon)).toEqual([]);
    expect(toast()).toHaveTextContent('Chief of staff is protected');
  });

  it.each([
    ['the close action', closeFromSidebar],
    ['⌘W', pressCmdW],
  ])('no-ops %s on a crew member and points to Sleep', async (_, close) => {
    const { daemon } = await renderOrchestrator({ crew_member: 'coda' });

    close();

    expect(closeCommands(daemon)).toEqual([]);
    expect(toast()).toHaveTextContent('Coda is protected — put Coda to sleep to close the day.');
  });

  it('closes only the tile when another tile still shows the chief', async () => {
    const { daemon } = await renderApp({
      initialState: {
        sessions: [daemonSession('a', { state: 'idle', chief_of_staff: true })],
        desktops: [
          laidOutDesktop(pane('a'), ['a']),
          daemonDesktop('other', { root: { type: 'pane', pane_id: 'pane-a2' }, panes: [{ ...agentPane('a', 'other', 'term-2'), pane_id: 'pane-a2' }] }, { order_key: 'b' }),
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
    expect(toast()).toBeNull();
  });

  it('closes an ordinary session normally and shows no hint', async () => {
    const { daemon } = await renderOrchestrator();

    closeFromSidebar();

    expect(closeCommands(daemon)).toEqual([{ cmd: 'unregister', id: 's1' }]);
    expect(toast()).toBeNull();
  });
});
