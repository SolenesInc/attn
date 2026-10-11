import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { crewMember, daemonSession, soloDesktop, defaultProfile } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';

const member = (values = {}) => crewMember('chief-key', { chief: true, name: 'Captain', ...values });
const available = { claude_available: 'true', codex_available: 'true', copilot_available: 'true', pi_available: 'true', claude_cap_launch_instructions: 'true', codex_cap_launch_instructions: 'true', copilot_cap_launch_instructions: 'false', pi_cap_launch_instructions: 'false', new_session_agent: 'claude' };
const slot = () => within(screen.getByTestId('chief-slot'));
const phase = () => screen.getByTestId('chief-slot').getAttribute('data-chief-phase');

for (const queue of [true, false]) describe(`Chief slot in ${queue ? 'queue' : 'desktop'} flow`, () => {
  const settings = { ...available, queue_mode_enabled: String(queue) };
  it('sets up with two Enters, sends one complete route and focuses the returned session', async () => {
    const current = member();
    const { daemon } = await renderApp({ initialState: { settings, crew: [current] } });
    expect(phase()).toBe('unconfigured');
    expect(document.activeElement).toBe(slot().getByRole('radio', { name: /Claude/ }));
    expect(slot().getByRole('radiogroup', { name: 'Chief harness' }).querySelectorAll('[role="radio"]')).toHaveLength(2);
    expect(slot().queryByText('Copilot')).toBeNull();
    expect(slot().queryByText('Pi')).toBeNull();
    await gesture(daemon, () => fireEvent.keyDown(slot().getByRole('radio', { name: /Claude/ }), { key: 'Enter' }));
    expect(slot().getByRole('radio', { name: /Opus 5.5/ })).toHaveAttribute('aria-checked', 'true');
    expect(document.activeElement).toBe(slot().getByRole('radio', { name: /Opus 5.5/ }));
    expect(slot().getByRole('radio', { name: 'low' })).toHaveAttribute('aria-checked', 'true');
    daemon.on('crew_set', () => [
      { event: 'sessions_updated', sessions: [daemonSession('chief-day', { chief: true, crew_member: current.key, state: 'idle' })] },
      { event: 'profile_arrangement_changed', profile: defaultProfile('desktop-chief-day'), desktops: [soloDesktop('chief-day')] },
      { event: 'crew_updated', profile_id: current.profile_id, members: [member({ agent: 'claude', model: 'opus', binding_session: 'chief-day', revision: 2 })] },
      { event: 'crew_set_result', success: true, conflict: false, member: member({ agent: 'claude', model: 'opus', binding_session: 'chief-day', revision: 2 }), woke_session_id: 'chief-day' },
    ]);
    await gesture(daemon, () => fireEvent.keyDown(slot().getByRole('radio', { name: /Opus 5.5/ }), { key: 'Enter' }));
    expect(daemon.sentOf('crew_set')).toEqual([expect.objectContaining({ member: 'member:chief-key', agent: 'claude', model: 'opus', effort: 'low', expected_revision: 1 })]);
    expect(daemon.sentOf('crew_wake')).toEqual([]);
    expect(phase()).toBe('awake');
    expect(slot().getByRole('button', { name: 'Open Captain' })).toBeInTheDocument();
    expect(daemon.sentOf('desktop_show_session').slice(-1)[0]).toMatchObject({ session_id: 'chief-day' });
    expect(screen.queryByTestId('chief-of-staff-card')).toBeNull();
  });
  it('keeps the route after a setup error and permits retry', async () => {
    const { daemon } = await renderApp({ initialState: { settings, crew: [member()] } });
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: /Continue/ })));
    await gesture(daemon, () => fireEvent.click(slot().getByRole('radio', { name: /Sonnet/ })));
    daemon.on('crew_set', () => ({ event: 'crew_set_result', success: false, conflict: false, error: 'Cannot start here' }));
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: /Wake Captain/ })));
    expect(slot().getByRole('alert')).toHaveTextContent('Cannot start here');
    expect(slot().getByRole('radio', { name: /Sonnet/ })).toHaveAttribute('aria-checked', 'true');
    expect(daemon.sentOf('crew_set')).toHaveLength(1);
    expect(daemon.sentOf('crew_wake')).toEqual([]);
  });
  it('rechecks availability and skips the harness step when only one fits', async () => {
    const { daemon } = await renderApp({ initialState: { settings: { ...settings, claude_available: 'false', codex_available: 'false' }, crew: [member()] } });
    expect(slot().getByText('No harness here can run the chief')).toBeInTheDocument();
    expect(slot().queryByRole('button', { name: /Wake/ })).toBeNull();
    daemon.on('get_settings', () => ({ event: 'settings_updated', settings: { ...settings, claude_available: 'false' } }));
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Check again' })));
    expect(daemon.sentOf('get_settings')).toHaveLength(1);
    expect(slot().getByText('the only one installed')).toBeInTheDocument();
    expect(slot().queryByRole('radiogroup', { name: 'Chief harness' })).toBeNull();
    expect(slot().getByRole('radio', { name: /gpt-6.1-sol/ })).toHaveAttribute('aria-checked', 'true');
    expect(daemon.sentOf('crew_set')).toEqual([]);
  });
  it('wakes with one click, sleeps until unbound, then wakes again', async () => {
    const sleeping = member({ agent: 'claude', model: 'opus' });
    const awake = { ...sleeping, binding_session: 'chief-day' };
    const { daemon } = await renderApp({ initialState: { settings, crew: [sleeping] } });
    daemon.on('crew_wake', () => [
      { event: 'sessions_updated', sessions: [daemonSession('chief-day', { chief: true, crew_member: sleeping.key, state: 'idle' })] },
      { event: 'profile_arrangement_changed', profile: defaultProfile('desktop-chief-day'), desktops: [soloDesktop('chief-day')] },
      { event: 'crew_updated', profile_id: sleeping.profile_id, members: [awake] },
      { event: 'crew_wake_result', success: true, session_id: 'chief-day', member: sleeping.key },
    ]);
    await gesture(daemon, () => fireEvent.click(slot().getAllByRole('button', { name: 'Wake Captain' })[0]));
    expect(daemon.sentOf('crew_wake')).toHaveLength(1);
    expect(phase()).toBe('awake');
    expect(daemon.sentOf('desktop_show_session').slice(-1)[0]).toMatchObject({ session_id: 'chief-day' });
    daemon.on('crew_sleep', () => undefined);
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Ask Captain to sleep' })));
    expect(phase()).toBe('sleeping');
    expect(slot().getByText('going to sleep')).toBeInTheDocument();
    await gesture(daemon, () => daemon.emit({ event: 'crew_updated', profile_id: sleeping.profile_id, members: [sleeping] }));
    expect(phase()).toBe('asleep');
    await gesture(daemon, () => fireEvent.click(slot().getAllByRole('button', { name: 'Wake Captain' })[0]));
    expect(daemon.sentOf('crew_wake')).toHaveLength(2);
  });
  it('saves a changed route for the next wake without waking an asleep chief', async () => {
    const current = member({ agent: 'claude', model: 'opus', effort: 'low' });
    const { daemon } = await renderApp({ initialState: { settings, crew: [current] } });
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Actions for Captain' })));
    expect(slot().getByRole('menu')).toHaveTextContent("The chief can't be closed or retired.");
    expect(slot().queryByRole('menuitem', { name: /Close session/ })).toBeNull();
    await gesture(daemon, () => fireEvent.click(slot().getByRole('menuitem', { name: 'Harness and model…' })));
    await gesture(daemon, () => fireEvent.click(slot().getByRole('radio', { name: /Sonnet/ })));
    daemon.on('crew_set', () => ({ event: 'crew_set_result', success: true, conflict: false, member: member({ agent: 'claude', model: 'sonnet', effort: 'low', revision: 2 }) }));
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: /Save/ })));
    expect(daemon.sentOf('crew_set')).toEqual([expect.objectContaining({ agent: 'claude', model: 'sonnet', effort: 'low' })]);
    expect(daemon.sentOf('crew_wake')).toEqual([]);
    expect(daemon.sentOf('crew_restart')).toEqual([]);
    expect(slot().queryByTestId('chief-setup')).toBeNull();
  });
  it('offers setup in the agent and command palettes and sends no idle mutations', async () => {
    const { daemon } = await renderApp({ initialState: { settings, crew: [member()] } });
    await gesture(daemon, () => fireEvent.keyDown(slot().getByRole('radio', { name: /Claude/ }), { key: 'Escape' }));
    expect(slot().queryByTestId('chief-setup')).toBeNull();
    await gesture(daemon, () => pressShortcut('ui.actionMenu'));
    expect(screen.getByText('not set up')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' }));
    expect(slot().getByTestId('chief-setup')).toBeInTheDocument();
    expect(daemon.sentOf('crew_set')).toEqual([]);
    expect(daemon.sentOf('crew_wake')).toEqual([]);
    expect(daemon.sentOf('crew_sleep')).toEqual([]);
  });
});
