import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { crewMember, daemonSession, soloDesktop } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';

describe('shared sidebar header', () => {
  it.each([false, true])('opens Commands in desktop/queue flow (%s)', async (queue) => {
    const { daemon } = await renderApp({ initialState: {
      settings: { queue_mode_enabled: String(queue) },
      sessions: [daemonSession('s1')], desktops: [soloDesktop('s1')],
    } });
    await daemon.idle();
    expect(daemon.sent.map(({ cmd }) => cmd)).toEqual(['client_hello', 'set_terminal_theme', 'set_client_presence', 'notification_list', 'get_presentations']);
    const header = document.querySelector('.sidebar-header')!;
    const panels = within(header.querySelector('.sidebar-header-panels') as HTMLElement).getAllByRole('button');
    expect(panels.map((button) => button.title)).toEqual([
      'Show the garden', expect.stringMatching(/^Open Notebook/), expect.stringMatching(/^Open Ledger/), 'Show Automations', 'Show Notifications',
    ]);
    await gesture(daemon, () => fireEvent.click(within(header as HTMLElement).getByRole('button', { name: 'Commands' })));
    await act(() => vi.advanceTimersToNextFrame());
    expect(screen.getByRole('combobox', { name: 'Commands' })).toHaveFocus();
    expect(screen.getByRole('dialog', { name: 'Commands' })).toBeVisible();
  });

  it('keeps Commands and the five panels in the collapsed desktop rail', async () => {
    const { daemon } = await renderApp({ initialState: { sessions: [daemonSession('s1')], desktops: [soloDesktop('s1')] } });
    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));
    const rail = document.querySelector('.icon-rail') as HTMLElement;
    expect(within(rail).getAllByRole('button').slice(0, 7).map((button) => button.title)).toEqual([
      expect.stringMatching(/^Home/), expect.stringMatching(/^Commands/), 'Show the garden', expect.stringMatching(/^Open Notebook/), expect.stringMatching(/^Open Ledger/), 'Show Automations', 'Show Notifications',
    ]);
    await gesture(daemon, () => fireEvent.click(within(rail).getByRole('button', { name: 'Commands' })));
    expect(screen.getByRole('dialog', { name: 'Commands' })).toBeVisible();
  });

  it('shows only sleeping crew above desktops and keeps the wake confirmation', async () => {
    const { daemon } = await renderApp({ initialState: {
      crew: [crewMember('asleep', { resolved_agent: 'codex' }), crewMember('awake', { binding_session: 's1' })],
      sessions: [daemonSession('s1', { crew_member: 'awake' })], desktops: [soloDesktop('s1')],
    } });
    expect(screen.queryByTestId('queue-crew-awake')).toBeNull();
    expect(screen.getByTestId('sidebar-session-s1')).toBeInTheDocument();
    const sleeping = within(screen.getByTestId('queue-crew-asleep'));
    expect(sleeping.getByRole('img', { name: 'Codex · idle' })).toBeInTheDocument();
    const wake = sleeping.getByTestId('queue-crew-wake-asleep');
    await gesture(daemon, () => fireEvent.click(wake));
    expect(daemon.sentOf('crew_wake')).toEqual([]);
    await gesture(daemon, () => fireEvent.click(wake));
    expect(daemon.sentOf('crew_wake')).toEqual([expect.objectContaining({ member: 'member:asleep' })]);
  });

  it('keeps bound crew awake before their session and desktop arrive', async () => {
    await renderApp({ initialState: {
      crew: [crewMember('awake', { binding_session: 'pending-session' })],
    } });
    expect(screen.queryByTestId('queue-crew-awake')).toBeNull();
  });
});
