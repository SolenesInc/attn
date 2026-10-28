import { act, fireEvent, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { crewMember, daemonSession, soloDesktop, defaultProfile, emptyDesktop, DEFAULT_DESKTOP_ID } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp, restartApp } from './test/renderApp';
import { harnesses, reportedModel } from './test/harnessCatalogs';

const member = (values = {}) => crewMember('chief-key', { chief: true, name: 'Captain', ...values });
const available = { claude_available: 'true', codex_available: 'true', pi_available: 'true', claude_cap_launch_instructions: 'true', codex_cap_launch_instructions: 'true', pi_cap_launch_instructions: 'false', new_session_agent: 'claude' };
const slot = () => within(screen.getByTestId('chief-slot'));
const setup = () => within(screen.getByTestId('chief-setup'));
const phase = () => screen.getByTestId('chief-slot').getAttribute('data-chief-phase');

for (const queue of [true, false]) describe(`Chief slot in ${queue ? 'queue' : 'desktop'} flow`, () => {
  const settings = { ...available, queue_mode_enabled: String(queue), 'notebook.root': '~/attn-notebook-work' };
  it('opens first-run setup with the primary action focused and submits the preselected route', async () => {
    const current = member();
    const { daemon } = await renderApp({ initialState: { settings, crew: [current] } });
    expect(phase()).toBe('unconfigured');
    const wake = setup().getByRole('button', { name: /Wake Captain/ });
    expect(wake).toHaveFocus();
    expect(setup().getAllByText('recommended')).toHaveLength(1);
    expect(setup().queryByRole('radio', { name: /Default/ })).toBeNull();
    expect(setup().getByRole('radio', { name: /Claude/ })).toHaveAttribute('aria-checked', 'true');
    expect(setup().getByRole('radio', { name: /Opus/ })).toHaveAttribute('aria-checked', 'true');
    expect(setup().getByRole('radio', { name: 'low' })).toHaveAttribute('aria-checked', 'true');
    expect(setup().queryByText('Pi')).toBeNull();
    const folder = setup().getByRole('textbox', { name: 'Notebook folder' });
    expect(folder).toHaveValue('~/attn-notebook-work');
    await gesture(daemon, () => fireEvent.change(folder, { target: { value: '~/Notes/Work' } }));
    daemon.on('crew_set', () => [
      { event: 'sessions_updated', sessions: [daemonSession('chief-day', { chief: true, crew_member: current.key, state: 'idle' })] },
      { event: 'profile_arrangement_changed', profile: defaultProfile('desktop-chief-day'), desktops: [soloDesktop('chief-day')] },
      { event: 'crew_updated', profile_id: current.profile_id, members: [member({ agent: 'claude', model: 'opus', binding_session: 'chief-day', revision: 2 })] },
      { event: 'crew_set_result', success: true, conflict: false, member: member({ agent: 'claude', model: 'opus', binding_session: 'chief-day', revision: 2 }), woke_session_id: 'chief-day' },
    ]);
    await gesture(daemon, () => fireEvent.click(wake));
    expect(daemon.sentOf('crew_set')).toEqual([expect.objectContaining({ member: 'member:chief-key', agent: 'claude', model: 'opus', effort: 'low', expected_revision: 1, notebook_root: '~/Notes/Work' })]);
    expect(daemon.sentOf('crew_wake')).toEqual([]);
    expect(phase()).toBe('awake');
    expect(screen.queryByTestId('chief-setup')).toBeNull();
    expect(slot().getByRole('button', { name: 'Open Captain' })).toBeInTheDocument();
    expect(daemon.sentOf('desktop_show_session').slice(-1)[0]).toMatchObject({ session_id: 'chief-day' });
  });
  it('waits for an explicit request while agents run, including after restart and closing the last agent', async () => {
    const desktop = soloDesktop('agent');
    const options = { initialState: { settings, crew: [member()], sessions: [daemonSession('agent', { state: 'idle' })], profiles: [defaultProfile(desktop.id)], desktops: [desktop] } };
    const running = await renderApp(options);
    const { daemon } = running;
    const terminal = screen.getByRole('textbox', { name: 'Terminal input' });
    terminal.focus();
    expect(screen.queryByTestId('chief-setup')).toBeNull();
    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { ...settings, claude_executable: '/updated-mock' } }));
    expect(terminal).toHaveFocus();
    expect(screen.queryByTestId('chief-setup')).toBeNull();
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Set up Captain' })));
    expect(setup().getByRole('button', { name: /Wake Captain/ })).toHaveFocus();
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'Escape' }));
    expect(slot().getByRole('button', { name: 'Set up Captain' })).toHaveFocus();
    const reconnected = await restartApp(running, options);
    expect(screen.queryByTestId('chief-setup')).toBeNull();
    await gesture(reconnected.daemon, () => {
      reconnected.daemon.emit({ event: 'sessions_updated', sessions: [] });
      (document.activeElement as HTMLElement).blur();
    });
    expect(screen.queryByTestId('chief-setup')).toBeNull();
  });
  it.each(['ready', 'terminal'])('settles delayed discovery without taking focus from the user (%s)', async destination => {
    const { daemon } = await renderApp({ initialState: { settings, crew: [member()] }, script: daemon => { daemon.on('harness_models', () => {}); } });
    expect(setup().getByRole('button', { name: /Wake Captain/ })).toBeDisabled();
    expect(screen.getByTestId('chief-setup')).toHaveFocus();
    let terminal: HTMLElement | undefined;
    if (destination === 'terminal') {
      const desktop = soloDesktop('agent');
      await gesture(daemon, () => {
        daemon.emit({ event: 'sessions_updated', sessions: [daemonSession('agent', { state: 'idle' })] });
        daemon.emit({ event: 'profile_arrangement_changed', profile: defaultProfile(desktop.id), desktops: [desktop] });
      });
      terminal = screen.getByRole('textbox', { name: 'Terminal input' });
      terminal.focus();
    }
    await gesture(daemon, () => {
      for (const request of daemon.sentOf('harness_models').filter(command => command.harness === 'claude')) daemon.replyTo(request, {
        event: 'harness_models_result', request_id: request.request_id, success: true, models: [reportedModel('claude', 'opus', 'Opus 5.5', 'deep')], tier_defaults: { deep: 'opus' }, detail: 'Reported by the test harness',
      });
    });
    expect(destination === 'terminal' ? terminal : setup().getByRole('button', { name: /Wake Captain/ })).toHaveFocus();
  });
  it.each(['model', 'terminal', 'close'])('keeps the chosen focus through catalog replacement (%s)', async destination => {
    const desktop = soloDesktop('agent');
    const { daemon } = await renderApp({ initialState: { settings, crew: [member()], sessions: [daemonSession('agent')], desktops: [desktop], profiles: [defaultProfile(desktop.id)] } });
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Set up Captain' })));
    await gesture(daemon, () => setup().getByRole('radio', { name: /Opus/ }).focus());
    daemon.on('harness_models', () => {});
    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { ...settings, claude_executable: '/refreshed-harness' } }));
    const terminal = screen.getByRole('textbox', { name: 'Terminal input' });
    const close = setup().getByRole('button', { name: 'Close Chief setup' });
    await gesture(daemon, () => {
      if (destination === 'terminal') terminal.focus();
      if (destination === 'close') close.focus();
    });
    await gesture(daemon, () => {
      for (const request of daemon.sentOf('harness_models').filter(command => command.harness === 'claude')) daemon.replyTo(request, {
        event: 'harness_models_result', request_id: request.request_id, success: true,
        models: [reportedModel('claude', 'new-deep', 'New deep model', 'deep')], tier_defaults: { deep: 'new-deep' }, detail: 'Refreshed catalog',
      });
    });
    expect(destination === 'model' ? setup().getByRole('radio', { name: /New deep model/ }) : destination === 'terminal' ? terminal : close).toHaveFocus();
    expect(daemon.sentOf('crew_set')).toEqual([]);
  });
  it('keeps radio navigation local and Enter on a radio submits', async () => {
    const { daemon } = await renderApp({ initialState: { settings, crew: [member()] } });
    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { ...settings, new_session_agent: 'codex' } }));
    const harness = setup().getByRole('radio', { name: /Claude/ });
    expect(harness).toHaveAttribute('aria-checked', 'true');
    harness.focus();
    await gesture(daemon, () => fireEvent.keyDown(harness, { key: 'ArrowRight' }));
    expect(setup().getByRole('radio', { name: 'Codex' })).toHaveFocus();
    expect(setup().getByRole('radio', { name: /gpt-6.1-sol/ })).toHaveAttribute('aria-checked', 'true');
    const effort = setup().getByRole('radio', { name: 'low' });
    effort.focus();
    await gesture(daemon, () => fireEvent.keyDown(effort, { key: 'ArrowRight' }));
    expect(setup().getByRole('radio', { name: 'medium' })).toHaveFocus();
    daemon.on('crew_set', () => undefined);
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'Enter' }));
    expect(daemon.sentOf('crew_set')).toEqual([expect.objectContaining({ agent: 'codex', model: 'gpt-6.1-sol', effort: 'medium' })]);
    expect(slot().getByText('waking…')).toBeInTheDocument();
    await gesture(daemon, () => daemon.emit({ event: 'crew_updated', profile_id: member().profile_id, members: [member({ agent: 'codex', model: 'gpt-6.1-sol' })] }));
    expect(setup().getByRole('heading', { name: 'Set up Captain' })).toBeInTheDocument();
    expect(setup().getByRole('textbox', { name: 'Notebook folder' })).toBeDisabled();
    expect(setup().getByRole('button', { name: 'Waking…' })).toBeDisabled();
  });
  it('ignores the new-agent picker Option+digit shortcuts behind its dialog', async () => {
    const { daemon } = await renderApp({ initialState: { settings, crew: [member()] } });
    await gesture(daemon, () => pressShortcut('session.new'));
    const picker = screen.getByTestId('location-picker');
    await gesture(daemon, () => fireEvent.keyDown(within(picker).getByRole('textbox'), { key: '2', code: 'Digit2', altKey: true }));
    expect(screen.queryByTestId('chief-setup')).toBeNull();
    expect(within(picker).getByRole('radio', { name: /Codex/ })).toHaveAttribute('aria-checked', 'true');
    expect(daemon.sentOf('crew_set')).toEqual([]);
  });
  it.each(['escape', 'outside'])('dismisses setup and returns focus to the chief row (%s)', async method => {
    const { daemon } = await renderApp({ initialState: { settings, crew: [member()] } });
    await gesture(daemon, () => method === 'escape' ? fireEvent.keyDown(document.activeElement!, { key: 'Escape' }) : fireEvent.mouseDown(document.body));
    expect(screen.queryByTestId('chief-setup')).toBeNull();
    expect(slot().getByRole('button', { name: 'Set up Captain' })).toHaveFocus();
    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));
    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));
    expect(screen.queryByTestId('chief-setup')).toBeNull();
  });
  it('keeps the selected route and Notebook folder after a save error', async () => {
    const { daemon } = await renderApp({ initialState: { settings, crew: [member()] } });
    await gesture(daemon, () => fireEvent.click(setup().getByRole('radio', { name: /Sonnet/ })));
    daemon.on('crew_set', () => ({ event: 'crew_set_result', success: false, conflict: false, error: 'Cannot start here' }));
    await gesture(daemon, () => fireEvent.click(setup().getByRole('button', { name: /Wake Captain/ })));
    expect(setup().getByRole('alert')).toHaveTextContent('Cannot start here');
    expect(setup().getByRole('radio', { name: /Sonnet/ })).toHaveAttribute('aria-checked', 'true');
    expect(setup().getByRole('textbox', { name: 'Notebook folder' })).toHaveValue('~/attn-notebook-work');
    expect(setup().getByRole('button', { name: /Wake Captain/ })).toBeEnabled();
    expect(daemon.sentOf('crew_set')).toHaveLength(1);
    expect(daemon.sentOf('crew_wake')).toEqual([]);
  });
  it('rechecks available harnesses and hides the selector when only one fits', async () => {
    const { daemon } = await renderApp({ initialState: { settings: { ...settings, claude_available: 'false', codex_available: 'false' }, crew: [member()] } });
    expect(setup().getByText(/No harness here can run the Chief/)).toBeInTheDocument();
    expect(setup().getByRole('button', { name: 'Check again' })).toHaveFocus();
    expect(setup().getByRole('button', { name: 'Settings › Agents' })).toBeInTheDocument();
    expect(setup().queryByRole('button', { name: /Wake/ })).toBeNull();
    daemon.on('get_settings', () => ({ event: 'settings_updated', settings: { ...settings, claude_available: 'false' } }));
    await gesture(daemon, () => fireEvent.click(setup().getByRole('button', { name: 'Check again' })));
    expect(daemon.sentOf('get_settings')).toHaveLength(1);
    expect(setup().queryByRole('radiogroup', { name: 'Chief harness' })).toBeNull();
    expect(setup().getByRole('radio', { name: /gpt-6.1-sol/ })).toHaveAttribute('aria-checked', 'true');
    expect(setup().getByRole('button', { name: /Wake Captain/ })).toHaveFocus();
    expect(daemon.sentOf('crew_set')).toEqual([]);
  });
  it('retries failed model discovery with Enter', async () => {
    const { daemon } = await renderApp({ initialState: { settings, crew: [member()] }, script: daemon => {
      daemon.on('harness_models', () => ({ event: 'harness_models_result', success: false, error: 'Model lookup failed', models: [], tier_defaults: {}, detail: '' }));
    } });
    expect(setup().getByRole('alert')).toHaveTextContent('Model lookup failed');
    daemon.on('harness_models', () => ({ event: 'harness_models_result', success: true, models: [reportedModel('claude', 'opus', 'Opus', 'deep')], tier_defaults: { deep: 'opus' }, detail: '' }));
    setup().getByRole('button', { name: 'Check again' }).focus();
    await Promise.all([userEvent.setup({ delay: null }).keyboard('{Enter}'), vi.advanceTimersByTimeAsync(10)]);
    await daemon.idle();
    expect(setup().queryByRole('alert')).toBeNull();
    expect(setup().getByRole('button', { name: /Wake Captain/ })).toBeEnabled();
    expect(daemon.sentOf('crew_set')).toEqual([]);
  });
  it('can switch to another harness when the selected model lookup fails', async () => {
    const { daemon } = await renderApp({ initialState: { settings, crew: [member()] }, script: daemon => {
      daemon.on('harness_models', ({ harness }) => harness === 'claude'
        ? { event: 'harness_models_result', success: false, error: 'Claude model lookup failed', models: [], tier_defaults: {}, detail: '' }
        : { event: 'harness_models_result', success: true, models: [reportedModel('codex', 'gpt-6.1-sol', 'Sol', 'deep')], tier_defaults: { deep: 'gpt-6.1-sol' }, detail: '' });
    } });
    expect(setup().getByRole('alert')).toHaveTextContent('Claude model lookup failed');
    expect(setup().getByRole('button', { name: /Wake Captain/ })).toBeDisabled();
    await gesture(daemon, () => fireEvent.click(setup().getByRole('radio', { name: 'Codex' })));
    expect(setup().queryByRole('alert')).toBeNull();
    expect(setup().getByRole('radio', { name: /Sol/ })).toHaveAttribute('aria-checked', 'true');
    daemon.on('crew_set', () => undefined);
    await gesture(daemon, () => fireEvent.click(setup().getByRole('button', { name: /Wake Captain/ })));
    expect(daemon.sentOf('crew_set')).toEqual([expect.objectContaining({ agent: 'codex', model: 'gpt-6.1-sol' })]);
  });
  it('shows a failed registry lookup and retries before permitting Wake', async () => {
    const { daemon } = await renderApp({ initialState: { settings, crew: [member()] }, script: daemon => {
      daemon.on('delegation_preferences_get', () => ({ event: 'delegation_preferences_result', success: false, error: 'Harness lookup failed' }));
    } });
    expect(setup().getByRole('alert')).toHaveTextContent('Harness lookup failed');
    expect(setup().queryByRole('button', { name: /Wake/ })).toBeNull();
    daemon.on('delegation_preferences_get', () => ({ event: 'delegation_preferences_result', success: true,
      preferences: { enabled: false, revision: 0, workflow_skill_enabled: false, roles: [], fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' } },
      harnesses, templates: [], expanded_roles: [],
    }));
    await gesture(daemon, () => fireEvent.click(setup().getByRole('button', { name: 'Check again' })));
    expect(setup().queryByRole('alert')).toBeNull();
    expect(setup().getByRole('button', { name: /Wake Captain/ })).toHaveFocus();
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
    await gesture(daemon, () => daemon.emit({ event: 'crew_sleep_result', request_id: daemon.sentOf('crew_sleep')[0].request_id, success: true, member: sleeping.key, session_id: 'chief-day' }));
    expect(phase()).toBe('awake');
    await gesture(daemon, () => daemon.emit({ event: 'crew_updated', profile_id: sleeping.profile_id, members: [sleeping] }));
    expect(phase()).toBe('asleep');
    await gesture(daemon, () => fireEvent.click(slot().getAllByRole('button', { name: 'Wake Captain' })[0]));
    expect(daemon.sentOf('crew_wake')).toHaveLength(2);
  });
  it('allows another Sleep and a route Save after the sleep reply while the binding remains', async () => {
    const current = member({ agent: 'claude', model: 'opus', effort: 'low', binding_session: 'chief-day' });
    const { daemon } = await renderApp({ initialState: { settings, crew: [current], sessions: [daemonSession('chief-day', { chief: true, crew_member: current.key })], desktops: [soloDesktop('chief-day')] } });
    daemon.on('crew_sleep', () => undefined);
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Ask Captain to sleep' })));
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Actions for Captain' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('menuitem', { name: 'Harness and model…' })));
    expect(setup().getByRole('button', { name: 'Save for next wake' })).toBeDisabled();
    expect(daemon.sentOf('crew_set')).toEqual([]);
    await gesture(daemon, () => daemon.emit({ event: 'crew_sleep_result', request_id: daemon.sentOf('crew_sleep')[0].request_id, success: true, member: current.key, session_id: 'chief-day' }));
    expect(phase()).toBe('awake');
    expect(setup().getByRole('button', { name: 'Save for next wake' })).toBeEnabled();
    daemon.on('crew_sleep', () => ({ event: 'crew_sleep_result', success: true, member: current.key, session_id: 'chief-day' }));
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Ask Captain to sleep' })));
    expect(daemon.sentOf('crew_sleep')).toHaveLength(2);
    daemon.on('crew_set', () => ({ event: 'crew_set_result', success: true, conflict: false, member: current }));
    await gesture(daemon, () => fireEvent.click(setup().getByRole('button', { name: 'Save for next wake' })));
    expect(daemon.sentOf('crew_set')).toHaveLength(1);
  });

  it('saves a changed route for the next wake without waking an asleep chief', async () => {
    const current = member({ agent: 'claude', model: 'opus', effort: 'high' });
    const { daemon } = await renderApp({ initialState: { settings, crew: [current] } });
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Actions for Captain' })));
    expect(screen.queryByRole('menuitem', { name: /Close session/ })).toBeNull();
    await gesture(daemon, () => fireEvent.click(screen.getByRole('menuitem', { name: 'Harness and model…' })));
    await gesture(daemon, () => fireEvent.click(setup().getByRole('radio', { name: /Sonnet/ })));
    daemon.on('crew_set', () => ({ event: 'crew_set_result', success: true, conflict: false, member: member({ agent: 'claude', model: 'sonnet', effort: 'high', revision: 2 }) }));
    await gesture(daemon, () => fireEvent.click(setup().getByRole('button', { name: /Save/ })));
    expect(daemon.sentOf('crew_set')).toEqual([expect.objectContaining({ agent: 'claude', model: 'sonnet', effort: 'high' })]);
    expect(daemon.sentOf('crew_wake')).toEqual([]);
    expect(screen.queryByTestId('chief-setup')).toBeNull();
  });
  it.each([false, true])('returns focus to the row after Escape and a delayed configured Save (awake=%s)', async awake => {
    const current = member({ agent: 'claude', model: 'opus', binding_session: awake ? 'chief-day' : undefined });
    const { daemon } = await renderApp({ initialState: { settings, crew: [current], sessions: awake ? [daemonSession('chief-day', { chief: true }), daemonSession('agent')] : [], desktops: awake ? [soloDesktop('chief-day'), soloDesktop('agent', { shortcut_slot: 2 })] : [] } });
    const row = () => slot().getByTestId('queue-crew-select-chief-key');
    if (queue && awake) {
      await gesture(daemon, () => pressShortcut('sidebar.agentList'));
      await act(() => vi.advanceTimersToNextFrame());
    }
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Actions for Captain' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('menuitem', { name: 'Harness and model…' })));
    await gesture(daemon, () => fireEvent.keyDown(setup().getByRole('radio', { name: /Opus/ }), { key: 'Escape' }));
    await act(() => vi.advanceTimersToNextFrame());
    expect(row()).toHaveFocus();
    if (queue && awake) {
      await gesture(daemon, () => fireEvent.keyDown(row(), { key: 'ArrowDown' }));
      expect(document.activeElement).not.toBe(row());
      expect(document.activeElement?.closest('.queue-sidebar')).not.toBeNull();
    }
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Actions for Captain' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('menuitem', { name: 'Harness and model…' })));
    daemon.on('crew_set', () => undefined);
    const save = setup().getByRole('button', { name: awake ? 'Save for next wake' : /^Save/ });
    save.focus();
    await gesture(daemon, () => fireEvent.click(save));
    expect(save).toBeDisabled();
    save.blur();
    await gesture(daemon, () => daemon.emit({ event: 'crew_set_result', request_id: daemon.sentOf('crew_set')[0].request_id, success: true, conflict: false, member: current }));
    await act(() => vi.advanceTimersToNextFrame());
    expect(screen.queryByTestId('chief-setup')).toBeNull();
    expect(row()).toHaveFocus();
  });
  it.each([false, true])('returns focus after configured Save when a frame runs before the row is ready (awake=%s)', async awake => {
    const current = member({ agent: 'claude', model: 'opus', binding_session: awake ? 'chief-day' : undefined });
    const { daemon } = await renderApp({ initialState: { settings, crew: [current], sessions: awake ? [daemonSession('chief-day', { chief: true })] : [], desktops: awake ? [soloDesktop('chief-day')] : [] } });
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Actions for Captain' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('menuitem', { name: 'Harness and model…' })));
    daemon.on('crew_set', () => undefined);
    await gesture(daemon, () => fireEvent.keyDown(setup().getByRole('radio', { name: /Opus/ }), { key: 'Enter' }));
    expect(slot().getByRole('button', { name: 'Set up Captain' })).toBeDisabled();
    const frame = vi.spyOn(window, 'requestAnimationFrame').mockImplementation(callback => {
      callback(performance.now());
      return 0;
    });
    try {
      await gesture(daemon, () => daemon.replyTo(daemon.sentOf('crew_set')[0], { event: 'crew_set_result', request_id: daemon.sentOf('crew_set')[0].request_id, success: true, conflict: false, member: current }));
    } finally { frame.mockRestore(); }
    expect(screen.queryByTestId('chief-setup')).toBeNull();
    expect(slot().getByTestId('queue-crew-select-chief-key')).toHaveFocus();
    expect(daemon.sentOf('crew_set')).toEqual([expect.objectContaining({ member: 'member:chief-key', agent: 'claude', model: 'opus', effort: 'low' })]);
    expect(daemon.sentOf('crew_wake')).toEqual([]);
  });
  it('preserves terminal focus when a delayed configured Save completes', async () => {
    const current = member({ agent: 'claude', model: 'opus', binding_session: 'chief-day' });
    const desktop = soloDesktop('chief-day');
    const { daemon } = await renderApp({ initialState: { settings, crew: [current], sessions: [daemonSession('chief-day', { chief: true })], profiles: [defaultProfile(desktop.id)], desktops: [desktop] } });
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Actions for Captain' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('menuitem', { name: 'Harness and model…' })));
    daemon.on('crew_set', () => undefined);
    const save = setup().getByRole('button', { name: 'Save for next wake' });
    save.focus();
    await gesture(daemon, () => fireEvent.click(save));
    const terminal = screen.getByRole('textbox', { name: 'Terminal input' });
    terminal.focus();
    await gesture(daemon, () => daemon.emit({ event: 'crew_set_result', request_id: daemon.sentOf('crew_set')[0].request_id, success: true, conflict: false, member: current }));
    await act(() => vi.advanceTimersToNextFrame());
    expect(terminal).toHaveFocus();
  });
  it('exposes the shared awake and asleep actions', async () => {
    const current = member({ agent: 'claude', model: 'opus', binding_session: 'chief-day' });
    const { daemon } = await renderApp({ initialState: { settings, crew: [current], sessions: [daemonSession('chief-day', { chief: true })], desktops: [soloDesktop('chief-day')] } });
    expect(phase()).toBe('awake');
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Actions for Captain' })));
    for (const name of ['Harness and model…', 'Edit charter', 'Rename session', 'Reload session', 'Mark priority', 'Sleep Captain']) expect(screen.getByRole('menuitem', { name })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Close session' })).toBeNull();
    await gesture(daemon, () => fireEvent.click(screen.getByRole('menuitem', { name: 'Harness and model…' })));
    expect(setup().getByRole('button', { name: 'Save for next wake' })).toBeEnabled();
    daemon.on('crew_sleep', () => ({ event: 'crew_sleep_result', success: true, member: current.key, session_id: 'chief-day' }));
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Ask Captain to sleep' })));
    expect(daemon.sentOf('crew_sleep')).toHaveLength(1);
    await gesture(daemon, () => daemon.emit({ event: 'crew_updated', profile_id: current.profile_id, members: [{ ...current, binding_session: undefined }] }));
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Actions for Captain' })));
    for (const name of ['Harness and model…', 'Edit charter', 'Wake Captain']) expect(screen.getByRole('menuitem', { name })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Close session' })).toBeNull();
  });
  it('preserves the user collapse through a configured profile switch and its roster arrival', async () => {
    const current = member({ agent: 'claude', model: 'opus' });
    const { daemon } = await renderApp({ initialState: { settings, crew: [current] } });
    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));
    expect(screen.queryByTestId('chief-slot')).toBeNull();
    const profile = defaultProfile('second-desktop', { id: 'second-profile', name: 'Second' });
    await gesture(daemon, () => daemon.emit({ event: 'profiles_changed', profiles: [defaultProfile(DEFAULT_DESKTOP_ID), profile] }));
    await gesture(daemon, () => daemon.emit({ event: 'profile_arrangement_changed', profile, desktops: [emptyDesktop('second-desktop', { profile_id: profile.id })] }));
    expect(screen.queryByTestId('chief-slot')).toBeNull();
    await gesture(daemon, () => daemon.emit({ event: 'crew_updated', profile_id: profile.id, members: [member({ ...current, key: 'second-chief', profile_id: profile.id })] }));
    expect(screen.queryByTestId('chief-slot')).toBeNull();
    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));
    expect(screen.getByTestId('chief-slot')).toHaveAttribute('data-chief-member', 'second-chief');
  });
  it.each([false, true])('opens the charter and returns focus to the persistent menu trigger (awake=%s)', async awake => {
    const current = member({ agent: 'claude', model: 'opus', binding_session: awake ? 'chief-day' : undefined });
    const { daemon } = await renderApp({ initialState: { settings, crew: [current], sessions: awake ? [daemonSession('chief-day', { chief: true })] : [], desktops: awake ? [soloDesktop('chief-day')] : [] } });
    daemon.on('crew_charter_get', () => ({ event: 'crew_charter_get_result', success: true, member: current.key, charter: { content: '# Captain', token: 'charter-one' } }));
    const trigger = slot().getByRole('button', { name: 'Actions for Captain' });
    await gesture(daemon, () => fireEvent.click(trigger));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('menuitem', { name: 'Edit charter' })));
    expect(screen.getByTestId('crew-tab-charter')).toHaveAttribute('aria-current', 'page');
    const editor = screen.getByRole('textbox', { name: 'Charter for Captain' });
    editor.focus();
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('crew-panel-close')));
    await act(() => vi.advanceTimersToNextFrame());
    expect(screen.queryByRole('dialog', { name: 'Manage crew' })).toBeNull();
    expect(trigger).toHaveFocus();
  });
  it('offers setup in the agent and command palettes and sends no idle mutations', async () => {
    const { daemon } = await renderApp({ initialState: { settings, crew: [member()] } });
    await gesture(daemon, () => fireEvent.keyDown(setup().getByRole('radio', { name: /Claude/ }), { key: 'Escape' }));
    expect(screen.queryByTestId('chief-setup')).toBeNull();
    await gesture(daemon, () => pressShortcut('ui.actionMenu'));
    expect(screen.getByText('not set up')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' }));
    expect(screen.getByTestId('chief-setup')).toBeInTheDocument();
    expect(daemon.sentOf('crew_set')).toEqual([]);
    expect(daemon.sentOf('crew_wake')).toEqual([]);
    expect(daemon.sentOf('crew_sleep')).toEqual([]);
  });
});
