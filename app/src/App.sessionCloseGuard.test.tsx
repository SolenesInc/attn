import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentWorkspace, daemonSession, type DaemonSession } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

async function renderOrchestrator(overrides: Partial<DaemonSession> = {}) {
  const rendered = await renderApp({
    initialState: {
      sessions: [daemonSession('s1', { label: 'orchestrator', ...overrides })],
      workspaces: [agentWorkspace('s1')],
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
    (command) => command.cmd === 'workspace_layout_close_pane' || command.cmd === 'unregister',
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

  it('closes an ordinary session normally and shows no hint', async () => {
    const { daemon } = await renderOrchestrator();

    closeFromSidebar();

    expect(closeCommands(daemon)).toEqual([
      expect.objectContaining({
        cmd: 'workspace_layout_close_pane',
        workspace_id: 'workspace-s1',
        pane_id: 'pane-s1',
      }),
    ]);
    expect(toast()).toBeNull();
  });
});
