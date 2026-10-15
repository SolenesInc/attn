import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { crewMember, daemonSession, soloDesktop, defaultProfile, emptyDesktop, DEFAULT_DESKTOP_ID } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp, restartApp } from './test/renderApp';
import { harnesses, reportedModel } from './test/harnessCatalogs';

const member = (values = {}) => crewMember('chief-key', { chief: true, name: 'Captain', ...values });
const available = { claude_available: 'true', codex_available: 'true', copilot_available: 'true', pi_available: 'true', claude_cap_launch_instructions: 'true', codex_cap_launch_instructions: 'true', copilot_cap_launch_instructions: 'false', pi_cap_launch_instructions: 'false', new_session_agent: 'claude' };
const plugin = { name: 'attn-pi', dir: '/test/attn-pi', version: '1', availability: 'available', connected: true, running: true, installation_state: 'installed', runtime_state: 'running', health_status: 'healthy', can_install: false, can_uninstall: true, priority: 0 } as const;
const slot = () => within(screen.getByTestId('chief-slot'));
const phase = () => screen.getByTestId('chief-slot').getAttribute('data-chief-phase');

for (const queue of [true, false]) describe(`Chief slot in ${queue ? 'queue' : 'desktop'} flow`, () => {
  const settings = { ...available, queue_mode_enabled: String(queue) };
  it.each([false, true])('keeps the configured model focused when its catalog arrives (awake=%s)', async awake => {
    const current = member({ agent: 'claude', model: 'opus', binding_session: awake ? 'chief-day' : undefined });
    const { daemon } = await renderApp({
      initialState: { settings, crew: [current], sessions: awake ? [daemonSession('chief-day', { chief: true })] : [], desktops: awake ? [soloDesktop('chief-day')] : [] },
      script: daemon => { daemon.on('harness_models', () => {}); },
    });
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Actions for Captain' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('menuitem', { name: 'Harness and model…' })));
    expect(slot().getByRole('radio', { name: /opus/ })).toHaveFocus();
    await gesture(daemon, () => {
      for (const request of daemon.sentOf('harness_models').filter(command => command.harness === 'claude')) daemon.replyTo(request, {
        event: 'harness_models_result', request_id: request.request_id, success: true, models: [reportedModel('claude', 'opus', 'Opus 5.5', 'deep')], tier_defaults: { deep: 'opus' }, detail: 'Reported by the test harness',
      });
    });
    expect(slot().getByRole('radio', { name: /Opus 5.5/ })).toHaveFocus();
  });
  it('keeps an explicit model step focused when a live agent arrives before its models', async () => {
    const { daemon } = await renderApp({
      initialState: { settings, crew: [member()] },
      script: daemon => { daemon.on('harness_models', () => {}); },
    });
    await gesture(daemon, () => fireEvent.keyDown(slot().getByRole('radio', { name: /Claude/ }), { key: 'Enter' }));
    const desktop = soloDesktop('agent');
    await gesture(daemon, () => {
      daemon.emit({ event: 'sessions_updated', sessions: [daemonSession('agent', { state: 'idle' })] });
      daemon.emit({ event: 'profile_arrangement_changed', profile: defaultProfile(desktop.id), desktops: [desktop] });
    });
    await gesture(daemon, () => {
      for (const request of daemon.sentOf('harness_models').filter(command => command.harness === 'claude')) daemon.replyTo(request, {
        event: 'harness_models_result', request_id: request.request_id, success: true, models: [reportedModel('claude', 'opus', 'Opus 5.5', 'deep')], tier_defaults: { deep: 'opus' }, detail: 'Reported by the test harness',
      });
    });
    expect(slot().getByRole('radio', { name: /Opus 5.5/ })).toHaveFocus();
  });
  it('keeps a live terminal focused when setup appears and catalogs refresh, but focuses an explicit setup request', async () => {
    const desktop = soloDesktop('agent');
    const options = { initialState: { settings, crew: [member()], sessions: [daemonSession('agent', { state: 'idle' })], profiles: [defaultProfile(desktop.id)], desktops: [desktop] } };
    const running = await renderApp(options);
    const { daemon } = running;
    await daemon.idle();
    const terminal = screen.getByRole('textbox', { name: 'Terminal input' });
    expect(screen.getByTestId('chief-setup').contains(document.activeElement)).toBe(false);
    terminal.focus();
    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { ...settings, claude_executable: '/updated-mock' } }));
    expect(terminal).toHaveFocus();
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Set up Captain' })));
    expect(slot().getByRole('radio', { name: /Claude/ })).toHaveFocus();
    await gesture(daemon, () => fireEvent.keyDown(slot().getByRole('radio', { name: /Claude/ }), { key: 'Enter' }));
    expect(slot().getByRole('radio', { name: /Opus 5.5/ })).toHaveFocus();
    terminal.focus();
    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { ...settings, claude_executable: '/updated-again' } }));
    expect(terminal).toHaveFocus();
    const reconnected = await restartApp(running, options);
    await reconnected.daemon.idle();
    expect(screen.getByTestId('chief-setup').contains(document.activeElement)).toBe(false);
  });
  it.each(['setup', 'terminal', 'outside', 'close', 'focus-away-and-back'])('keeps the chosen surface focused through catalog replacement (%s)', async surface => {
    const desktop = soloDesktop('agent');
    const { daemon } = await renderApp({ initialState: {
      settings, crew: [member()], sessions: [daemonSession('agent', { state: 'idle' })], desktops: [desktop], profiles: [defaultProfile(desktop.id)],
    } });
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Set up Captain' })));
    await gesture(daemon, () => fireEvent.keyDown(slot().getByRole('radio', { name: /Claude/ }), { key: 'Enter' }));
    expect(slot().getByRole('radio', { name: /Opus 5.5/ })).toHaveFocus();
    daemon.on('harness_models', () => {});
    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { ...settings, claude_executable: '/refreshed-harness' } }));
    const terminal = screen.getByRole('textbox', { name: 'Terminal input' });
    await gesture(daemon, () => {
      if (surface === 'terminal') terminal.focus();
      if (surface === 'outside') fireEvent.pointerDown(document.body);
      if (surface === 'close') slot().getByRole('button', { name: 'Close chief setup' }).focus();
      if (surface === 'focus-away-and-back') { terminal.focus(); terminal.blur(); }
    });
    await gesture(daemon, () => {
      for (const request of daemon.sentOf('harness_models').filter(command => command.harness === 'claude')) daemon.replyTo(request, {
        event: 'harness_models_result', request_id: request.request_id, success: true,
        models: [reportedModel('claude', 'new-deep-model', 'New deep model', 'deep')], tier_defaults: { deep: 'new-deep-model' }, detail: 'Refreshed harness catalog',
      });
    });
    const model = slot().getByRole('radio', { name: /New deep model/ });
    const expected = surface === 'setup' ? model : surface === 'terminal' ? terminal : surface === 'close' ? slot().getByRole('button', { name: 'Close chief setup' }) : document.body;
    expect(document.activeElement).toBe(expected);
  });
  it.each(['setup', 'terminal', 'outside'])('keeps the chosen focus when a plugin update reloads all harnesses (%s)', async surface => {
    const desktop = soloDesktop('agent');
    const { daemon } = await renderApp({ initialState: {
      settings, crew: [member()], sessions: [daemonSession('agent')], desktops: [desktop], profiles: [defaultProfile(desktop.id)],
    } });
    await gesture(daemon, () => fireEvent.keyDown(slot().getByRole('radio', { name: /Claude/ }), { key: 'Enter' }));
    expect(slot().getByRole('radio', { name: /Opus 5.5/ })).toHaveFocus();
    daemon.on('delegation_preferences_get', () => {});
    await gesture(daemon, () => daemon.emit({ event: 'plugins_updated', plugins: [plugin], issues: [] }));
    expect(slot().getByRole('radio', { name: /Opus 5.5/ })).toHaveFocus();
    const wake = slot().getByRole('button', { name: /Wake Captain/ });
    expect(wake).toBeDisabled();
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'Enter' }));
    expect(daemon.sentOf('crew_set')).toEqual([]);
    const terminal = screen.getByRole('textbox', { name: 'Terminal input' });
    await gesture(daemon, () => {
      if (surface === 'terminal') terminal.focus();
      if (surface === 'outside') { fireEvent.pointerDown(document.body); (document.activeElement as HTMLElement).blur(); }
    });
    await gesture(daemon, () => {
      const request = daemon.sentOf('delegation_preferences_get').slice(-1)[0]!;
      daemon.replyTo(request, { event: 'delegation_preferences_result', request_id: request.request_id, success: true,
        preferences: { enabled: false, revision: 0, workflow_skill_enabled: false, roles: [], fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' } },
        harnesses, templates: [], expanded_roles: [],
      });
    });
    const model = slot().getByRole('radio', { name: /Opus 5.5/ });
    expect(document.activeElement).toBe(surface === 'setup' ? model : surface === 'terminal' ? terminal : document.body);
  });
  it('keeps the harness focused when plugin discovery arrives before the first Enter', async () => {
    const desktop = soloDesktop('agent');
    const { daemon } = await renderApp({ initialState: {
      settings, crew: [member()], sessions: [daemonSession('agent')], desktops: [desktop], profiles: [defaultProfile(desktop.id)],
    } });
    const harness = slot().getByRole('radio', { name: /Claude/ });
    harness.focus();
    await gesture(daemon, () => fireEvent.click(harness));
    await gesture(daemon, () => daemon.emit({ event: 'plugins_updated', plugins: [plugin], issues: [] }));
    expect(harness).toHaveFocus();
    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'Enter' }));
    expect(slot().getByRole('radio', { name: /Opus 5.5/ })).toHaveFocus();
  });
  it('shows a registry refresh error and retries before permitting Wake', async () => {
    const { daemon } = await renderApp({ initialState: { settings, crew: [member()] } });
    await gesture(daemon, () => fireEvent.keyDown(slot().getByRole('radio', { name: /Claude/ }), { key: 'Enter' }));
    daemon.on('delegation_preferences_get', () => ({ event: 'delegation_preferences_result', success: false, error: 'Harness lookup failed' }));
    await gesture(daemon, () => daemon.emit({ event: 'plugins_updated', plugins: [plugin], issues: [] }));
    expect(slot().getByRole('alert')).toHaveTextContent('Harness lookup failed');
    expect(slot().getByRole('button', { name: /Wake Captain/ })).toBeDisabled();
    daemon.on('delegation_preferences_get', () => ({ event: 'delegation_preferences_result', success: true,
      preferences: { enabled: false, revision: 0, workflow_skill_enabled: false, roles: [], fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' } },
      harnesses, templates: [], expanded_roles: [],
    }));
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Check again' })));
    expect(slot().queryByRole('alert')).toBeNull();
    expect(slot().getByRole('button', { name: /Wake Captain/ })).toBeEnabled();
    expect(daemon.sentOf('crew_set')).toEqual([]);
  });
  it.each(['terminal', 'outside', 'close'])('cancels a model-step request when the user chooses %s before discovery', async surface => {
    const desktop = soloDesktop('agent');
    const { daemon } = await renderApp({
      initialState: { settings, crew: [member()], sessions: [daemonSession('agent')], desktops: [desktop], profiles: [defaultProfile(desktop.id)] },
      script: daemon => { daemon.on('harness_models', () => {}); },
    });
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Set up Captain' })));
    await gesture(daemon, () => fireEvent.keyDown(slot().getByRole('radio', { name: /Claude/ }), { key: 'Enter' }));
    const terminal = screen.getByRole('textbox', { name: 'Terminal input' });
    const close = slot().getByRole('button', { name: 'Close chief setup' });
    await gesture(daemon, () => {
      if (surface === 'terminal') terminal.focus();
      if (surface === 'outside') fireEvent.pointerDown(document.body);
      if (surface === 'close') close.focus();
    });
    await gesture(daemon, () => {
      for (const request of daemon.sentOf('harness_models').filter(command => command.harness === 'claude')) daemon.replyTo(request, {
        event: 'harness_models_result', request_id: request.request_id, success: true,
        models: [reportedModel('claude', 'opus', 'Opus', 'deep')], tier_defaults: { deep: 'opus' }, detail: 'Delayed catalog',
      });
    });
    expect(document.activeElement).toBe(surface === 'terminal' ? terminal : surface === 'close' ? close : document.body);
  });
  it('keeps a chosen effort control focused when the catalog refreshes', async () => {
    const { daemon } = await renderApp({ initialState: { settings, crew: [member()] } });
    await gesture(daemon, () => fireEvent.keyDown(slot().getByRole('radio', { name: /Claude/ }), { key: 'Enter' }));
    const effort = slot().getByRole('radio', { name: 'low' });
    effort.focus();
    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { ...settings, model_tier_overrides: '[]' } }));
    expect(effort).toHaveFocus();
  });
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
    expect(slot().getByRole('button', { name: 'Save for next wake' })).toBeDisabled();
    expect(daemon.sentOf('crew_set')).toEqual([]);
    await gesture(daemon, () => daemon.emit({ event: 'crew_sleep_result', request_id: daemon.sentOf('crew_sleep')[0].request_id, success: true, member: current.key, session_id: 'chief-day' }));
    expect(phase()).toBe('awake');
    expect(slot().getByRole('button', { name: 'Save for next wake' })).toBeEnabled();
    daemon.on('crew_sleep', () => ({ event: 'crew_sleep_result', success: true, member: current.key, session_id: 'chief-day' }));
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Ask Captain to sleep' })));
    expect(daemon.sentOf('crew_sleep')).toHaveLength(2);
    daemon.on('crew_set', () => ({ event: 'crew_set_result', success: true, conflict: false, member: current }));
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Save for next wake' })));
    expect(daemon.sentOf('crew_set')).toHaveLength(1);
  });

  it('saves a changed route for the next wake without waking an asleep chief', async () => {
    const current = member({ agent: 'claude', model: 'opus', effort: 'high' });
    const { daemon } = await renderApp({ initialState: { settings, crew: [current] } });
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Actions for Captain' })));
    expect(screen.queryByRole('menuitem', { name: /Close session/ })).toBeNull();
    await gesture(daemon, () => fireEvent.click(screen.getByRole('menuitem', { name: 'Harness and model…' })));
    await gesture(daemon, () => fireEvent.click(slot().getByRole('radio', { name: /Sonnet/ })));
    daemon.on('crew_set', () => ({ event: 'crew_set_result', success: true, conflict: false, member: member({ agent: 'claude', model: 'sonnet', effort: 'high', revision: 2 }) }));
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: /Save/ })));
    expect(daemon.sentOf('crew_set')).toEqual([expect.objectContaining({ agent: 'claude', model: 'sonnet', effort: 'high' })]);
    expect(daemon.sentOf('crew_wake')).toEqual([]);
    expect(slot().queryByTestId('chief-setup')).toBeNull();
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
    await gesture(daemon, () => fireEvent.keyDown(slot().getByRole('radio', { name: /Opus/ }), { key: 'Escape' }));
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
    const save = slot().getByRole('button', { name: awake ? 'Save for next wake' : /^Save/ });
    save.focus();
    await gesture(daemon, () => fireEvent.click(save));
    expect(save).toBeDisabled();
    save.blur();
    await gesture(daemon, () => daemon.emit({ event: 'crew_set_result', request_id: daemon.sentOf('crew_set')[0].request_id, success: true, conflict: false, member: current }));
    await act(() => vi.advanceTimersToNextFrame());
    expect(slot().queryByTestId('chief-setup')).toBeNull();
    expect(row()).toHaveFocus();
  });
  it('preserves terminal focus when a delayed configured Save completes', async () => {
    const current = member({ agent: 'claude', model: 'opus', binding_session: 'chief-day' });
    const desktop = soloDesktop('chief-day');
    const { daemon } = await renderApp({ initialState: { settings, crew: [current], sessions: [daemonSession('chief-day', { chief: true })], profiles: [defaultProfile(desktop.id)], desktops: [desktop] } });
    await gesture(daemon, () => fireEvent.click(slot().getByRole('button', { name: 'Actions for Captain' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('menuitem', { name: 'Harness and model…' })));
    daemon.on('crew_set', () => undefined);
    const save = slot().getByRole('button', { name: 'Save for next wake' });
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
    expect(slot().getByRole('button', { name: 'Save for next wake' })).toBeEnabled();
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
