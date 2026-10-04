import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { soloDesktop, crewMember, type DaemonCrewMember, daemonSession } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';
import { initialState } from './test/scriptedDaemon';

const manageCrew = () => screen.queryByTestId('manage-crew');

function renderWithCrew(crew: DaemonCrewMember[]) {
  return renderApp({ initialState: { crew, sessions: [daemonSession('s1')], desktops: [soloDesktop('s1')] } });
}

describe('App crew roster', () => {
  it('draws the roster from initial_state', async () => {
    await renderWithCrew([crewMember('keel'), crewMember('trellis')]);

    expect(manageCrew()).toHaveTextContent('manage');
  });

  it('replaces the roster on every crew broadcast', async () => {
    const { daemon } = await renderWithCrew([crewMember('keel'), crewMember('trellis')]);

    daemon.emit({ event: 'crew_updated', members: [crewMember('keel')] });

    expect(manageCrew()).toHaveTextContent('manage');
  });

  it('reads a crew-less daemon, such as an outpost, as an empty roster', async () => {
    const { daemon } = await renderWithCrew([crewMember('keel')]);
    expect(manageCrew()).not.toBeNull();

    daemon.on('client_hello', () => initialState({ sessions: [daemonSession('s1')], desktops: [soloDesktop('s1')] }));
    await daemon.reconnect();

    expect(manageCrew()).toBeNull();
  });
});
