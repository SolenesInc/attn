import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { crewMember, soloDesktop, daemonSession, type DaemonSession } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

async function renderOrchestrator(overrides: Partial<DaemonSession> = {}) {
  const rendered = await renderApp({
    initialState: {
      crew: [crewMember('coda', { name: 'Coda', agent: 'claude', binding_session: 's1', chief: overrides.chief === true })],
      sessions: [daemonSession('s1', { label: 'orchestrator', ...overrides, ...(overrides.chief ? { crew_member: 'coda' } : {}) })],
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
  it.each([true, false])('protects a bound member from ⌘W (chief=%s)', async (chief) => {
    const { daemon } = await renderOrchestrator({ chief, crew_member: 'coda' });
    pressCmdW();
    expect(closeCommands(daemon)).toEqual([]);
    expect(toast()).toHaveTextContent('Coda is protected. Put Coda to sleep to close its session.');
  });

  it.each([true, false])('offers Sleep for a bound member (chief=%s)', async (chief) => {
    const { daemon } = await renderOrchestrator({ chief, crew_member: 'coda' });
    daemon.on('crew_sleep', () => ({ event: 'crew_sleep_result', success: true, member: 'coda', session_id: 's1' }));
    fireEvent.click(screen.getByRole('button', { name: 'Actions for orchestrator' }));
    expect(screen.queryByRole('menuitem', { name: /Close session/ })).toBeNull();
    fireEvent.click(screen.getByRole('menuitem', { name: 'Sleep Coda' }));
    await daemon.idle();
    expect(daemon.sentOf('crew_sleep')).toEqual([expect.objectContaining({ member: 'member:coda' })]);
    if (chief) expect(screen.getByTestId('chief-slot')).toHaveAttribute('data-chief-phase', 'sleeping');
    expect(closeCommands(daemon)).toEqual([]);
  });

  it('closes an ordinary session normally and shows no hint', async () => {
    const { daemon } = await renderOrchestrator();

    closeFromSidebar();

    expect(closeCommands(daemon)).toEqual([{ cmd: 'unregister', id: 's1' }]);
    expect(toast()).toBeNull();
  });
});
