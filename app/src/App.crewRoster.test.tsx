import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { EventMessage } from './test/protocol';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';
import { initialState } from './test/scriptedDaemon';

type CrewMember = EventMessage<'crew_updated'>['members'][number];

function member(id: string): CrewMember {
  return {
    id,
    charter_path: `/homes/${id}/CHARTER.md`,
    home_dir: `/homes/${id}`,
    awareness_dirs: [],
    resolved_agent: 'claude',
    revision: 1,
  };
}

const manageCrew = () => screen.queryByTestId('manage-crew');

function renderWithCrew(crew: CrewMember[]) {
  return renderApp({ initialState: { crew, sessions: [daemonSession('s1')], workspaces: [agentWorkspace('s1')] } });
}

describe('App crew roster', () => {
  it('draws the roster from initial_state', async () => {
    await renderWithCrew([member('keel'), member('trellis')]);

    expect(manageCrew()).toHaveTextContent('Manage crew2');
  });

  it('replaces the roster on every crew broadcast', async () => {
    const { daemon } = await renderWithCrew([member('keel'), member('trellis')]);

    daemon.emit({ event: 'crew_updated', members: [member('keel')] });

    expect(manageCrew()).toHaveTextContent('Manage crew1');
  });

  it('reads a crew-less daemon, such as an outpost, as an empty roster', async () => {
    const { daemon } = await renderWithCrew([member('keel')]);
    expect(manageCrew()).not.toBeNull();

    daemon.on('client_hello', () => initialState({ sessions: [daemonSession('s1')], workspaces: [agentWorkspace('s1')] }));
    await daemon.reconnect();

    expect(manageCrew()).toBeNull();
  });
});
