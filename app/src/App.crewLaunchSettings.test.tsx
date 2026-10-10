import { openRoute, previewHarness, pickModel, setRouteEffort, routeDialog } from './test/harnessRoute';
import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { soloDesktop, crewMember, type DaemonCrewMember as CrewMember, daemonSession } from './test/daemonFixtures';
import type { CommandMessage } from './test/protocol';
import { gesture, renderApp } from './test/renderApp';
import { type Answer, answerInTurn, HOLD, type Reply, type ScriptedDaemon } from './test/scriptedDaemon';

const member = (id: string, revision: number, values: Partial<CrewMember> = {}) => crewMember(id, { revision, ...values });

const harnesses = [
  { id: 'claude', name: 'Claude Code', available: true, model_pin: true, effort_pin: true, discovery: true },
  { id: 'codex', name: 'Codex', available: true, model_pin: true, effort_pin: true, discovery: true },
];

function codexModel(id: string) {
  return { harness: 'codex', provider: '', id, name: id, description: '', detail: '', access: 'supported' as const, tier_source: 'none' as const, effort_support: 'supported' as const, effort_levels: [] };
}

const saved = (next: CrewMember): Reply => ({ event: 'crew_set_result', success: true, conflict: false, member: next });
const CHANGED_ELSEWHERE = 'the member changed after it was read; reconcile the returned revision and retry';
const conflicted = (current: CrewMember): Reply => ({ event: 'crew_set_result', success: false, conflict: true, error: CHANGED_ELSEWHERE, member: current });

async function openLaunchSettings(members: CrewMember[], saves: Answer[], settings: Record<string, string> = {}) {
  const { daemon } = await renderApp({
    initialState: { settings, crew: members, sessions: [daemonSession('s1')], desktops: [soloDesktop('s1')] },
  });
  answerInTurn(daemon, 'crew_set', saves);
  daemon.on('delegation_preferences_get', () => ({
    event: 'delegation_preferences_result',
    success: true,
    preferences: { enabled: false, revision: 0, workflow_skill_enabled: false, roles: [], fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' } },
    templates: [],
    harnesses,
  }));
  daemon.on('harness_models', ({ harness }) => ({
    event: 'harness_models_result', tier_defaults: {},
    success: true,
    detail: '',
    models: harness === 'codex' ? [codexModel('saved-model'), codexModel('local-model')] : [],
  }));
  await gesture(daemon, () => fireEvent.click(screen.getByTestId('manage-crew')));
  return daemon;
}

const panel = () => within(screen.getByTestId('crew-panel'));
const saveState = () => within(panel().getByRole('heading', { name: 'Launch settings' }).closest('section')!).getAllByRole('status')[0];
const nextWake = () => panel().getByText('Acknowledged next wake').parentElement!;
const crewSets = (daemon: ScriptedDaemon) => daemon.sentOf('crew_set').map(({ expected_revision, agent, model, effort }) => ({ expected_revision, agent, model, effort }));

async function setEffort(daemon: ScriptedDaemon, effort: string) {
  await setRouteEffort(daemon, 'Crew launch model', effort);
}

async function roster(daemon: ScriptedDaemon, members: CrewMember[]) {
  await gesture(daemon, () => daemon.emit({ event: 'crew_updated', profile_id: 'profile-default', members }));
}

async function answer(daemon: ScriptedDaemon, save: CommandMessage<'crew_set'>, reply: Reply) {
  await gesture(daemon, () => daemon.replyTo(save, { ...reply, request_id: save.request_id } as Reply));
}

describe('App crew launch settings', () => {
  it('disables unavailable alternatives and observes availability changes without rereading the harness list', async () => {
    const daemon = await openLaunchSettings([member('trellis', 1, { agent: 'codex', resolved_agent: 'codex' })], [], { claude_available: 'false' });
    await openRoute(daemon, 'Crew launch model');
    expect(routeDialog().getByRole('option', { name: /^Claude Code/ })).toBeDisabled();
    expect(daemon.sentOf('crew_set')).toEqual([]);
    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { claude_available: 'true' } }));
    expect(routeDialog().getByRole('option', { name: /^Claude Code/ })).toBeEnabled();
    await previewHarness(daemon, 'Claude Code');
    expect(routeDialog().getByRole('option', { name: 'Claude Code default' })).toBeInTheDocument();
    expect(daemon.sentOf('delegation_preferences_get')).toHaveLength(1);
  });

  it('follows plugin health and recovery while ignoring health timestamps', async () => {
    const daemon = await openLaunchSettings([member('trellis', 1)], [], { demo_available: 'true' });
    let available = true;
    daemon.on('delegation_preferences_get', () => ({
      event: 'delegation_preferences_result', success: true,
      preferences: { enabled: false, revision: 0, workflow_skill_enabled: false, roles: [], fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' } },
      templates: [], harnesses: [...harnesses, { id: 'demo', name: 'Demo', available, model_pin: true, effort_pin: true, discovery: false }],
    }));
    const plugin = { name: 'demo', dir: '/tmp/demo', version: '1', availability: 'available', connected: true, running: true, installation_state: 'installed', runtime_state: 'running', health_status: 'healthy', can_install: false, can_uninstall: true, priority: 0 };
    await gesture(daemon, () => daemon.emit({ event: 'plugins_updated', plugins: [plugin], issues: [] }));
    await openRoute(daemon, 'Crew launch model');
    expect(routeDialog().getByRole('option', { name: /^Demo/ })).toBeEnabled();
    available = false;
    await gesture(daemon, () => daemon.emit({ event: 'plugins_updated', plugins: [{ ...plugin, health_status: 'unhealthy' }], issues: [] }));
    expect(routeDialog().getByRole('option', { name: /^Demo/ })).toBeDisabled();
    const reads = daemon.sentOf('delegation_preferences_get').length;
    await gesture(daemon, () => daemon.emit({ event: 'plugins_updated', plugins: [{ ...plugin, health_status: 'unhealthy', last_health_at: '2026-10-09T03:00:00Z' }], issues: [] }));
    expect(daemon.sentOf('delegation_preferences_get')).toHaveLength(reads);
    available = true;
    await gesture(daemon, () => daemon.emit({ event: 'plugins_updated', plugins: [plugin], issues: [] }));
    expect(routeDialog().getByRole('option', { name: /^Demo/ })).toBeEnabled();
    expect(daemon.sentOf('crew_set')).toEqual([]);
  });
  it('keeps an unavailable stored route visible and permits clearing all launch pins', async () => {
    const current = member('trellis', 1, { agent: 'codex', resolved_agent: 'codex', model: 'saved-model', resolved_model: 'saved-model', effort: 'high', resolved_effort: 'high' });
    const daemon = await openLaunchSettings([current], [saved({ ...current, revision: 2, agent: '', model: '', effort: '', resolved_model: '', resolved_effort: '' })], { codex_available: 'false' });
    await openRoute(daemon, 'Crew launch model');
    expect(panel().getByRole('button', { name: 'Crew launch model' })).toHaveTextContent('saved-model');
    expect(panel().getByRole('button', { name: 'Crew launch model' })).toHaveTextContent('high');
    expect(within(routeDialog().getByRole('listbox', { name: 'Harness' })).getByRole('option', { name: /^Codex/ })).toBeDisabled();
    expect(routeDialog().queryByRole('textbox', { name: 'Filter models or enter an ID' })).toBeNull();
    expect(routeDialog().getByRole('textbox', { name: 'Effort' })).toHaveValue('high');
    expect(routeDialog().getByRole('textbox', { name: 'Effort' })).toHaveAttribute('readonly');
    expect(routeDialog().getByRole('option', { name: 'Codex default' })).toBeDisabled();
    await gesture(daemon, () => fireEvent.click(within(routeDialog().getByRole('listbox', { name: 'Harness' })).getByRole('option', { name: 'Crew default' })));
    expect(crewSets(daemon)).toEqual([{ expected_revision: 1, effort: '', agent: '', model: '' }]);
  });
  it('shows the next wake the roster reports while a save is out, and ignores an older roster', async () => {
    const daemon = await openLaunchSettings([member('trellis', 1)], [HOLD]);
    await setEffort(daemon, 'high');
    expect(saveState()).toHaveTextContent('Saving…');

    await roster(daemon, [member('trellis', 2, { effort: 'high', resolved_effort: 'high' })]);
    expect(nextWake()).toHaveTextContent(/Claude.*default.*high/);

    await roster(daemon, [member('trellis', 1)]);
    expect(nextWake()).toHaveTextContent(/Claude.*default.*high/);
  });

  it('keeps a write whose answer was lost as not saved, even when the roster shows the same values', async () => {
    const daemon = await openLaunchSettings([member('keel', 1)], [HOLD]);
    await setEffort(daemon, 'high');

    await act(() => vi.advanceTimersByTimeAsync(70_000));
    await daemon.idle();
    expect(saveState()).toHaveTextContent('Not saved');

    await roster(daemon, [member('keel', 2, { effort: 'high', resolved_effort: 'high' })]);
    expect(saveState()).toHaveTextContent('Not saved');
    expect(panel().getByRole('button', { name: 'Crew launch model' })).toHaveTextContent('high');
  });

  it('follows what the daemon derives at the same revision, and drops a write answer older than the roster', async () => {
    const daemon = await openLaunchSettings([member('alder', 3)], [HOLD, saved(member('alder', 7, { effort: 'latest', resolved_effort: 'latest' }))]);
    await roster(daemon, [member('alder', 3, { binding_session: 'session-new', resolved_agent: 'codex' })]);
    expect(nextWake()).toHaveTextContent(/Codex.*default/);

    await setEffort(daemon, 'first');
    await setEffort(daemon, 'latest');
    await roster(daemon, [member('alder', 6, { model: 'external', resolved_model: 'external' })]);
    await answer(daemon, daemon.sentOf('crew_set')[0], saved(member('alder', 4, { effort: 'first', resolved_effort: 'first' })));

    expect(crewSets(daemon)[1]).toEqual({ expected_revision: 6, agent: '', model: 'external', effort: 'latest' });
    expect(saveState()).toHaveTextContent('Saved');
    expect(nextWake()).toHaveTextContent(/Claude.*default.*latest/);
  });

  it('merges what changed elsewhere into a conflicting edit, keeps what the user cleared, and asks for a retry', async () => {
    const codex = { agent: 'codex', resolved_agent: 'codex' };
    const daemon = await openLaunchSettings(
      [member('keel', 7, { ...codex, model: 'saved-model', effort: 'low', resolved_model: 'saved-model', resolved_effort: 'low' })],
      [
        conflicted(member('keel', 8, { ...codex, model: 'saved-model', effort: 'high', resolved_model: 'saved-model', resolved_effort: 'high' })),
        saved(member('keel', 9, { ...codex, model: 'local-model', effort: 'high', resolved_model: 'local-model', resolved_effort: 'high' })),
        conflicted(member('keel', 10, { ...codex, model: 'local-model', effort: 'high', resolved_model: 'local-model', resolved_effort: 'high' })),
        saved(member('keel', 11)),
      ],
    );

    await openRoute(daemon, 'Crew launch model');
    await pickModel(daemon, 'local-model');
    expect(saveState()).toHaveTextContent('Not saved');
    expect(panel().getByText(CHANGED_ELSEWHERE)).toBeInTheDocument();
    expect(panel().getByRole('button', { name: 'Crew launch model' })).toHaveTextContent('high');
    await gesture(daemon, () => fireEvent.click(panel().getByRole('button', { name: 'Retry' })));
    expect(crewSets(daemon)[1]).toEqual({ expected_revision: 8, agent: 'codex', model: 'local-model', effort: 'high' });
    expect(saveState()).toHaveTextContent('Saved');

    if (!screen.queryByRole('dialog', { name: 'Choose a model' })) await openRoute(daemon, 'Crew launch model');
    await previewHarness(daemon, 'Claude');
    await pickModel(daemon, 'Claude Code default');
    expect(saveState()).toHaveTextContent('Not saved');
    await gesture(daemon, () => fireEvent.click(panel().getByRole('button', { name: 'Retry' })));

    expect(crewSets(daemon)[3]).toEqual({ expected_revision: 10, agent: 'claude', model: '', effort: '' });
    expect(saveState()).toHaveTextContent('Saved');
  });
});
