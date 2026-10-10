import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { soloDesktop, crewMember, type DaemonCrewMember, daemonSession, DEFAULT_PROFILE_ID, defaultProfile } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';
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

    daemon.emit({ event: 'crew_updated', profile_id: 'profile-default', members: [crewMember('keel')] });

    expect(manageCrew()).toHaveTextContent('manage');
  });

  it('shows stored names, sends permanent keys, and ignores other profiles', async () => {
    const { daemon } = await renderWithCrew([crewMember('keel', { name: 'Alfred' })]);
    expect(screen.getByTestId('queue-crew-keel')).toHaveTextContent('Alfred');
    await gesture(daemon, () => daemon.emit({ event: 'crew_updated', profile_id: 'side', members: [crewMember('bob', { name: 'Bob', profile_id: 'side' })] }));
    expect(screen.getByTestId('queue-crew-keel')).toHaveTextContent('Alfred');
    expect(screen.queryByTestId('queue-crew-bob')).toBeNull();
    await gesture(daemon, () => fireEvent.click(manageCrew()!));
    expect(screen.getByTestId('crew-roster-keel')).toHaveTextContent('Alfred');
    daemon.on('crew_wake', () => ({ event: 'crew_wake_result', success: true, session_id: 'awake' }));
    await gesture(daemon, () => fireEvent.click(within(screen.getByTestId('crew-panel')).getByRole('button', { name: 'Wake' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Wake member' })));
    expect(daemon.sentOf('crew_wake')[0].member).toBe('member:keel');
  });

  it('clears the old roster when the selected profile changes', async () => {
    const { daemon } = await renderWithCrew([crewMember('keel', { name: 'Alfred' })]);
    await gesture(daemon, () => daemon.emit({ event: 'profile_arrangement_changed', profile: defaultProfile('', { id: 'side', name: 'Side' }), desktops: [] }));
    expect(screen.queryByTestId('queue-crew-keel')).toBeNull();
    await gesture(daemon, () => daemon.emit({ event: 'crew_updated', profile_id: 'side', members: [crewMember('bob', { profile_id: 'side', name: 'Robert' })] }));
    expect(screen.getByTestId('queue-crew-bob')).toHaveTextContent('Robert');
    await gesture(daemon, () => daemon.emit({ event: 'crew_updated', profile_id: DEFAULT_PROFILE_ID, members: [crewMember('keel')] }));
    expect(screen.queryByTestId('queue-crew-keel')).toBeNull();
  });

  it('reads a crew-less daemon, such as an outpost, as an empty roster', async () => {
    const { daemon } = await renderWithCrew([crewMember('keel')]);
    expect(manageCrew()).not.toBeNull();

    daemon.on('client_hello', () => initialState({ sessions: [daemonSession('s1')], desktops: [soloDesktop('s1')] }));
    await daemon.reconnect();

    expect(manageCrew()).toBeNull();
  });
});
