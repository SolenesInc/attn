import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentWorkspace, daemonSession, type DaemonSession } from './test/daemonFixtures';
import type { EventMessage } from './test/protocol';
import { gesture, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

type CrewMember = EventMessage<'crew_updated'>['members'][number];

const keel: CrewMember = {
  id: 'keel',
  charter_path: '/homes/keel/CHARTER.md',
  home_dir: '/homes/keel',
  awareness_dirs: [],
  resolved_agent: 'claude',
  revision: 1,
};

function renderCrewQueue(keelDay: Partial<DaemonSession>) {
  return renderApp({
    initialState: {
      settings: { queue_mode_enabled: 'true' },
      crew: [keel],
      sessions: [daemonSession('s1'), daemonSession('sess-keel', keelDay)],
      workspaces: [agentWorkspace('s1'), agentWorkspace('sess-keel')],
    },
  });
}

async function wakeKeel(daemon: ScriptedDaemon) {
  const wake = screen.getByTestId('queue-crew-wake-keel');
  await gesture(daemon, () => fireEvent.click(wake));
  await gesture(daemon, () => fireEvent.click(wake));
}

async function askKeelToSleep(daemon: ScriptedDaemon) {
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Ask Keel to sleep' })));
}

function shownWorkspaces() {
  return Array.from(document.querySelectorAll('.session-terminal-workspace[data-session-visible="1"]'))
    .map((workspace) => workspace.getAttribute('data-workspace-id'));
}

describe('App crew wake and sleep', () => {
  it('wakes a sleeping member and focuses the day the daemon names', async () => {
    const { daemon } = await renderCrewQueue({ label: 'keel day' });
    daemon.on('crew_wake', () => ({
      event: 'crew_wake_result', success: true, member: 'keel', session_id: 'sess-keel',
    }));

    await wakeKeel(daemon);

    expect(daemon.sentOf('crew_wake')).toEqual([expect.objectContaining({ member: 'keel' })]);
    expect(shownWorkspaces()).toEqual(['workspace-sess-keel']);
  });

  it('shows what the daemon said when it refuses a wake', async () => {
    const { daemon } = await renderCrewQueue({ label: 'keel day' });
    daemon.on('crew_wake', () => ({
      event: 'crew_wake_result', success: false, error: 'keel launches in /gone, which is not there',
    }));

    await wakeKeel(daemon);

    expect(screen.getByRole('alert')).toHaveTextContent('keel launches in /gone, which is not there');
    expect(shownWorkspaces()).toEqual([]);
  });

  it('asks an awake member to sleep', async () => {
    const { daemon } = await renderCrewQueue({ crew_member: 'keel' });
    daemon.on('crew_sleep', () => ({
      event: 'crew_sleep_result', success: true, member: 'keel', session_id: 'sess-keel', delivery_status: 'notified',
    }));

    await askKeelToSleep(daemon);

    expect(daemon.sentOf('crew_sleep')).toEqual([expect.objectContaining({ member: 'keel' })]);
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('shows what the daemon said when it refuses a sleep request', async () => {
    const { daemon } = await renderCrewQueue({ crew_member: 'keel' });
    daemon.on('crew_sleep', () => ({
      event: 'crew_sleep_result', success: false, error: 'session sess-keel cannot receive agent messages',
    }));

    await askKeelToSleep(daemon);

    expect(screen.getByRole('alert')).toHaveTextContent('session sess-keel cannot receive agent messages');
  });
});
