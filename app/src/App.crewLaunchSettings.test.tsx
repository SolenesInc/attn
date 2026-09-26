import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { agentWorkspace, crewMember, type DaemonCrewMember as CrewMember, daemonSession } from './test/daemonFixtures';
import type { CommandMessage } from './test/protocol';
import { gesture, renderApp } from './test/renderApp';
import { type Answer, answerInTurn, HOLD, type Reply, type ScriptedDaemon } from './test/scriptedDaemon';

const member = (id: string, revision: number, values: Partial<CrewMember> = {}) => crewMember(id, { revision, ...values });

const harnesses = [
  { id: 'claude', name: 'Claude Code', available: true, model_pin: true, effort_pin: true, discovery: true },
  { id: 'codex', name: 'Codex', available: true, model_pin: true, effort_pin: true, discovery: true },
];

function codexModel(id: string) {
  return { harness: 'codex', provider: '', id, name: id, description: '', detail: '', access: 'supported' as const, effort_support: 'supported' as const, effort_levels: [] };
}

const saved = (next: CrewMember): Reply => ({ event: 'crew_set_result', success: true, conflict: false, member: next });
const CHANGED_ELSEWHERE = 'the member changed after it was read; reconcile the returned revision and retry';
const conflicted = (current: CrewMember): Reply => ({ event: 'crew_set_result', success: false, conflict: true, error: CHANGED_ELSEWHERE, member: current });

async function openLaunchSettings(members: CrewMember[], saves: Answer[]) {
  const { daemon } = await renderApp({
    initialState: { crew: members, sessions: [daemonSession('s1')], workspaces: [agentWorkspace('s1')] },
  });
  answerInTurn(daemon, 'crew_set', saves);
  daemon.on('delegation_preferences_get', () => ({
    event: 'delegation_preferences_result',
    success: true,
    preferences: { enabled: false, revision: 0, workflow_skill_enabled: false, roles: [], fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' } },
    templates: [],
    harnesses,
  }));
  daemon.on('delegation_models', ({ harness }) => ({
    event: 'delegation_models_result',
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
  const field = panel().getByLabelText('Reasoning effort');
  field.focus();
  fireEvent.change(field, { target: { value: effort } });
  await gesture(daemon, () => fireEvent.blur(field));
}

async function roster(daemon: ScriptedDaemon, members: CrewMember[]) {
  await gesture(daemon, () => daemon.emit({ event: 'crew_updated', members }));
}

async function answer(daemon: ScriptedDaemon, save: CommandMessage<'crew_set'>, reply: Reply) {
  await gesture(daemon, () => daemon.replyTo(save, { ...reply, request_id: save.request_id } as Reply));
}

describe('App crew launch settings', () => {
  it('shows the next wake the roster reports while a save is out, and ignores an older roster', async () => {
    const daemon = await openLaunchSettings([member('trellis', 1)], [HOLD]);
    await setEffort(daemon, 'high');
    expect(saveState()).toHaveTextContent('Saving…');

    await roster(daemon, [member('trellis', 2, { effort: 'high', resolved_effort: 'high' })]);
    expect(nextWake()).toHaveTextContent('claude / default model / high');

    await roster(daemon, [member('trellis', 1)]);
    expect(nextWake()).toHaveTextContent('claude / default model / high');
  });

  it('keeps a write whose answer was lost as not saved, even when the roster shows the same values', async () => {
    const daemon = await openLaunchSettings([member('keel', 1)], [HOLD]);
    await setEffort(daemon, 'high');

    await act(() => vi.advanceTimersByTimeAsync(70_000));
    await daemon.idle();
    expect(saveState()).toHaveTextContent('Not saved');

    await roster(daemon, [member('keel', 2, { effort: 'high', resolved_effort: 'high' })]);
    expect(saveState()).toHaveTextContent('Not saved');
    expect(panel().getByLabelText('Reasoning effort')).toHaveValue('high');
  });

  it('follows what the daemon derives at the same revision, and drops a write answer older than the roster', async () => {
    const daemon = await openLaunchSettings([member('alder', 3)], [HOLD, saved(member('alder', 7, { effort: 'latest', resolved_effort: 'latest' }))]);
    await roster(daemon, [member('alder', 3, { binding_session: 'session-new', resolved_agent: 'codex' })]);
    expect(nextWake()).toHaveTextContent('codex / default model / default effort');

    await setEffort(daemon, 'first');
    await setEffort(daemon, 'latest');
    await roster(daemon, [member('alder', 6, { model: 'external', resolved_model: 'external' })]);
    await answer(daemon, daemon.sentOf('crew_set')[0], saved(member('alder', 4, { effort: 'first', resolved_effort: 'first' })));

    expect(crewSets(daemon)[1]).toEqual({ expected_revision: 6, agent: '', model: 'external', effort: 'latest' });
    expect(saveState()).toHaveTextContent('Saved');
    expect(nextWake()).toHaveTextContent('claude / default model / latest');
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

    await gesture(daemon, () => fireEvent.change(panel().getByLabelText('Model'), { target: { value: 'local-model' } }));
    expect(saveState()).toHaveTextContent('Not saved');
    expect(panel().getByText(CHANGED_ELSEWHERE)).toBeInTheDocument();
    expect(panel().getByLabelText('Reasoning effort')).toHaveValue('high');
    await gesture(daemon, () => fireEvent.click(panel().getByRole('button', { name: 'Retry' })));
    expect(crewSets(daemon)[1]).toEqual({ expected_revision: 8, agent: 'codex', model: 'local-model', effort: 'high' });
    expect(saveState()).toHaveTextContent('Saved');

    await gesture(daemon, () => fireEvent.change(panel().getByLabelText('Harness'), { target: { value: 'claude' } }));
    expect(saveState()).toHaveTextContent('Not saved');
    await gesture(daemon, () => fireEvent.click(panel().getByRole('button', { name: 'Retry' })));

    expect(crewSets(daemon)[3]).toEqual({ expected_revision: 10, agent: 'claude', model: '', effort: '' });
    expect(saveState()).toHaveTextContent('Saved');
  });
});
