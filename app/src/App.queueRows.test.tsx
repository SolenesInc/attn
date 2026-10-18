import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { crewMember, daemonSession, soloDesktop } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';

describe('App queue rows', () => {
  it('shows crew names in the queue popup and launch destinations in the full palette', async () => {
    const awake = daemonSession('keel-session', { label: 'Keel', crew_member: 'keel', state: 'working' });
    const { daemon } = await renderApp({
      initialState: {
        settings: { queue_mode_enabled: 'true' },
        sessions: [awake],
        desktops: [soloDesktop(awake.id)],
        crew: [crewMember('alder', { launch_desktop: { label: 'Review' } }), crewMember('keel')],
      },
    });

    fireEvent.click(screen.getByRole('button', { name: 'Collapse sidebar' }));
    await daemon.idle();
    const pill = screen.getByTestId('queue-bar-pill');
    fireEvent.pointerEnter(screen.getByTestId('queue-bar-waiting'));
    const peek = screen.getByTestId('queue-bar-waiting-peek');
    expect(within(peek).getByText('Alder')).toHaveTextContent(/^Alder$/);
    expect(within(peek).getByText('Keel')).toHaveTextContent(/^Keel$/);

    fireEvent.click(pill);
    await daemon.idle();
    expect(screen.getByText('Alder')).toHaveTextContent('Review');
  });

  it('names the chief from its member, even when its session has another label', async () => {
    const chief = daemonSession('coordinator', { label: 'Profiles epic coordinator', state: 'idle', chief: true, crew_member: 'chief-member' });
    await renderApp({ initialState: {
      settings: { queue_mode_enabled: 'true' }, sessions: [chief], desktops: [soloDesktop(chief.id)],
      crew: [crewMember('chief-member', { chief: true, name: 'Captain', agent: 'claude', model: 'opus', binding_session: chief.id })],
    } });
    expect(within(screen.getByTestId('chief-slot')).getByRole('button', { name: 'Open Captain' })).toBeInTheDocument();
    expect(within(screen.getByTestId('chief-slot')).queryByText('Profiles epic coordinator')).toBeNull();
  });

  it('keeps a snoozed agent name beside its wake time', async () => {
    const snoozed = daemonSession('check', {
      label: 'Neovim Configuration Check', state: 'idle',
      turn_snoozed_until: '2099-01-01T09:00:00Z',
    });
    const { daemon } = await renderApp({
      initialState: {
        settings: { queue_mode_enabled: 'true' },
        sessions: [snoozed],
        desktops: [soloDesktop(snoozed.id)],
      },
    });

    fireEvent.click(screen.getByTestId('queue-agents-toggle'));
    const row = screen.getByTestId('queue-snoozed-check');
    expect(within(row).getByText('Neovim Configuration Check')).toBeInTheDocument();
    expect(row.querySelector('.queue-row-wake-at')?.textContent).toBeTruthy();
    expect(daemon.sentOf('snooze_turn')).toEqual([]);
  });
});
