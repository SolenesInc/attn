import { act, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CrewRestartState, type CrewMember } from '../types/generated';
import type { Seed } from '../hooks/useDaemonSocket';
import { renderWithDaemon } from '../test/renderApp';
import type { CommandMessage, CommandName } from '../test/protocol';
import type { Reply, ScriptedDaemon } from '../test/scriptedDaemon';
import { CrewPanel } from './CrewPanel';

function member(id: string, revision: number, values: Partial<CrewMember> = {}): CrewMember {
  return {
    id,
    revision,
    charter_path: `/crew/${id}/CHARTER.md`,
    home_dir: `/crew/${id}`,
    awareness_dirs: [],
    resolved_agent: 'claude',
    ...values,
  };
}

function seed(overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return {
    body: '', status: 'planted', state_changed_at: '2026-09-06T16:50:50Z', state_changed_at_exact: true,
    step_slug: overrides.title, planter_session: '', planter_member: '', tender_session: '', tender_member: '',
    edges: [], ready: false, template: false, gate: false, vars: [], rev: 1,
    created_at: '2026-09-06T16:50:50Z', updated_at: '2026-09-06T16:50:50Z', ...overrides,
  };
}

const HOLD = 'hold';
type Answer = Reply | typeof HOLD;

const saved = (values: Partial<Extract<Reply, { event: 'crew_set_result' }>> = {}): Reply => ({ event: 'crew_set_result', success: true, conflict: false, ...values });
const restarted = (values: Partial<Extract<Reply, { event: 'crew_restart_result' }>> = {}): Reply => ({ event: 'crew_restart_result', success: true, conflict: false, ...values });
const charter = (content: string, token: string): Reply => ({ event: 'crew_charter_get_result', success: true, member: 'trellis', charter: { content, token } });
const charterSaved = (content: string, token: string, conflict = false): Reply => ({ event: 'crew_charter_set_result', success: true, member: 'trellis', conflict, charter: { content, token } });
const handoffs = (member: string, list: { filename: string; occurred_at: string }[]): Reply => ({ event: 'crew_handoffs_get_result', success: true, member, handoffs: list });
const handoff = (filename: string, occurredAt: string, content: string, token: string): Reply => ({ event: 'crew_handoff_get_result', success: true, member: 'trellis', handoff: { filename, occurred_at: occurredAt, content, token } });
const charterRefused = (error: string): Reply => ({ event: 'crew_charter_set_result', success: false, conflict: false, error });
const handoffsRefused = (error: string): Reply => ({ event: 'crew_handoffs_get_result', success: false, handoffs: [], error });
const handoffRefused = (error: string): Reply => ({ event: 'crew_handoff_get_result', success: false, error });

const harnesses = [
  { id: 'claude', name: 'Claude Code', available: true, model_pin: true, effort_pin: true, discovery: true },
  { id: 'codex', name: 'Codex', available: true, model_pin: true, effort_pin: true, discovery: true },
  { id: 'fixture', name: 'Fixture', available: false, model_pin: false, effort_pin: false, discovery: false },
];

const preferences = (available = harnesses): Reply => ({
  event: 'delegation_preferences_result',
  success: true,
  preferences: { enabled: false, revision: 0, workflow_skill_enabled: false, roles: [], fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' } },
  templates: [],
  harnesses: available,
});

const models = (list: Extract<Reply, { event: 'delegation_models_result' }>['models']): Reply => ({ event: 'delegation_models_result', success: true, detail: '', models: list });

const astra = models([
  { harness: 'codex', provider: 'openai', id: 'gpt-6-astra', name: 'Astra', description: '', detail: '', access: 'supported', effort_support: 'supported', effort_levels: ['medium', 'high'] },
  { harness: 'codex', provider: 'openai', id: 'retired', name: 'Retired', description: '', detail: '', access: 'unsupported', effort_support: 'unknown', effort_levels: [] },
]);

type CrewCommand = 'crew_set' | 'crew_restart' | 'crew_charter_get' | 'crew_charter_set' | 'crew_handoffs_get' | 'crew_handoff_get' | 'delegation_preferences_get' | 'delegation_models';
type Script = Partial<Record<CrewCommand, Answer[]>>;

const defaults: Record<CrewCommand, Answer[]> = {
  crew_set: [saved()],
  crew_restart: [restarted()],
  crew_charter_get: [charter('# Trellis\n', 'charter-1')],
  crew_charter_set: [charterSaved('# Trellis\n', 'charter-2')],
  crew_handoffs_get: [handoffs('trellis', [])],
  crew_handoff_get: [handoffRefused('no handoff body in this fixture')],
  delegation_preferences_get: [preferences()],
  delegation_models: [astra],
};

function answerInTurn(daemon: ScriptedDaemon, cmd: CommandName, answers: Answer[]) {
  let turn = 0;
  daemon.on(cmd, () => {
    const answer = answers[Math.min(turn++, answers.length - 1)];
    return answer === HOLD ? undefined : answer;
  });
}

async function renderPanel({
  script = {},
  members = [member('trellis', 4)],
  sessions = [],
  initialMember,
  isOpen = true,
  seeds = [],
}: {
  script?: Script;
  members?: CrewMember[];
  sessions?: Parameters<typeof CrewPanel>[0]['sessions'];
  initialMember?: string;
  isOpen?: boolean;
  seeds?: Seed[];
} = {}) {
  const onClose = vi.fn();
  const onOpenSeed = vi.fn<(seedId: string, placementSessionId?: string) => void>();
  let visit = 1;
  let open = isOpen;
  const panel = (nextMembers: CrewMember[], nextOpen: boolean) => (
    <CrewPanel
      visit={visit}
      isOpen={nextOpen}
      initialMember={initialMember}
      members={nextMembers}
      sessions={sessions}
      seeds={seeds}
      seedsTotal={seeds.length}
      onClose={onClose}
      onOpenSeed={onOpenSeed}
    />
  );
  const view = await renderWithDaemon();
  const { daemon } = view;
  for (const [cmd, answers] of Object.entries({ ...defaults, ...script })) {
    answerInTurn(daemon, cmd as CrewCommand, answers);
  }
  view.rerender(panel(members, isOpen));
  await daemon.idle();
  const rerenderPanel = async (nextMembers: CrewMember[], nextOpen = open, preserve = false) => {
    if (nextOpen && !open && !preserve) visit += 1;
    open = nextOpen;
    view.rerender(panel(nextMembers, nextOpen));
    await daemon.idle();
  };
  const answer = async (command: CommandMessage & { request_id?: string }, reply: Reply) => {
    daemon.replyTo(command, { ...reply, request_id: command.request_id });
    await daemon.idle();
  };
  const settle = () => daemon.idle();
  return { ...view, daemon, onClose, onOpenSeed, rerenderPanel, answer, settle };
}

async function click(daemon: ScriptedDaemon, name: string | RegExp) {
  fireEvent.click(screen.getByRole('button', { name }));
  await daemon.idle();
}

const crewSets = (daemon: ScriptedDaemon) => daemon.sentOf('crew_set').map(({ member: id, expected_revision, agent, model, effort }) => ({ member: id, expected_revision, agent, model, effort }));
const restartGuards = (daemon: ScriptedDaemon) => daemon.sentOf('crew_restart').map(({ member: id, request_id, expected_session_id, expected_revision }) => ({ member: id, request_id, expected_session_id, expected_revision }));

afterEach(() => {
  vi.restoreAllMocks();
});

describe('CrewPanel', () => {
  it('uses a native dialog inside the sidebar-adjacent panel layer', async () => {
    await renderPanel();
    const panel = screen.getByTestId('crew-panel');
    expect(panel.tagName).toBe('DIALOG');
    expect(panel).toHaveAttribute('open');
    expect(panel.closest('.crew-panel-layer')).toBeInTheDocument();
  });

  it('keeps member, tab, seed filter and search when a workspace seed returns to Crew', async () => {
    const planted = seed({ id: 's-g9yxwv', title: 'Artifact presence comes from the daemon', planter_member: 'keel' });
    const members = [member('alder', 2), member('keel', 3, { binding_session: 'session-keel' })];
    const { onOpenSeed, rerenderPanel } = await renderPanel({ members, seeds: [planted] });

    expect(screen.getByLabelText('Harness')).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: /Keel/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Seeds' }));
    fireEvent.click(screen.getByRole('button', { name: /Planted/ }));
    fireEvent.change(screen.getByLabelText('Find a seed'), { target: { value: 'Artifact presence' } });
    fireEvent.click(screen.getByRole('button', { name: /Artifact presence comes from the daemon/ }));
    expect(onOpenSeed).toHaveBeenCalledWith(planted.id, 'session-keel');

    await rerenderPanel(members, false);
    await rerenderPanel(members, true, true);
    expect(screen.getByRole('heading', { name: 'Keel' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Seeds' })).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('button', { name: /^Planted/ })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByLabelText('Find a seed')).toHaveValue('Artifact presence');
  });

  it('keeps actual running values separate from acknowledged next-wake settings', async () => {
    const { daemon } = await renderPanel({
      members: [member('trellis', 4, {
        binding_session: 'session-trellis',
        agent: 'codex', model: 'gpt-6-astra', effort: 'high',
        resolved_agent: 'codex', resolved_model: 'gpt-6-astra', resolved_effort: 'high',
      })],
      sessions: [{ id: 'session-trellis', agent: 'claude' } as Parameters<typeof CrewPanel>[0]['sessions'][number]],
    });

    const running = screen.getByLabelText('Running now');
    expect(running).toHaveTextContent('Harnessclaude');
    expect(running).toHaveTextContent('ModelNot reported');
    expect(running).toHaveTextContent('EffortNot reported');
    expect(screen.getByText('Acknowledged next wake').parentElement).toHaveTextContent('codex / gpt-6-astra / high');
    expect(screen.getByLabelText('Harness')).toHaveValue('codex');
    expect(screen.getByRole('option', { name: 'openai / Astra' })).toBeInTheDocument();
    expect(daemon.sentOf('delegation_models').map((command) => command.harness)).toEqual(['codex']);
    expect(daemon.sentOf('delegation_preferences_get')).toHaveLength(1);
  });

  it('saves a full atomic selection, blocks restart until acknowledgment, and clears to defaults', async () => {
    const { daemon, answer } = await renderPanel({
      script: { crew_set: [HOLD] },
      members: [member('alder', 7, {
        binding_session: 'session-alder', agent: 'codex', model: 'gpt-6-astra', effort: 'high',
        resolved_agent: 'codex', resolved_model: 'gpt-6-astra', resolved_effort: 'high',
      })],
    });

    const harness = screen.getByLabelText('Harness');
    fireEvent.change(harness, { target: { value: '' } });

    expect(crewSets(daemon)).toEqual([{ member: 'alder', expected_revision: 7, agent: '', model: '', effort: '' }]);
    expect(screen.getByRole('status', { name: '' })).toHaveTextContent('Saving…');
    expect(screen.getByRole('button', { name: 'Handoff and restart' })).toBeDisabled();

    await answer(daemon.sentOf('crew_set')[0], saved({ member: member('alder', 8, { binding_session: 'session-alder', resolved_agent: 'claude' }) }));
    expect(screen.getByText('Saved')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Handoff and restart' })).toBeEnabled();
    expect(harness).toHaveValue('');
  });

  it('keeps a failed member edit through roster navigation and retries it', async () => {
    const { daemon, answer } = await renderPanel({
      script: { crew_set: [saved({ success: false, error: 'model discovery is unavailable' }), HOLD] },
      members: [member('alder', 2), member('keel', 3)],
    });

    const effort = screen.getByLabelText('Reasoning effort');
    fireEvent.change(screen.getByLabelText('Harness'), { target: { value: 'codex' } });
    await daemon.idle();
    expect(screen.getByText('Not saved')).toBeInTheDocument();
    expect(screen.getByText('model discovery is unavailable')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /Keel/ }));
    fireEvent.click(screen.getByRole('button', { name: /Alder/ }));
    expect(screen.getByLabelText('Harness')).toHaveValue('codex');
    expect(effort).toHaveValue('');

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(daemon.sentOf('crew_set')).toHaveLength(2);
    await answer(daemon.sentOf('crew_set')[1], saved({ member: member('alder', 3, { agent: 'codex', resolved_agent: 'codex' }) }));
    expect(screen.getByText('Saved')).toBeInTheDocument();
  });

  it('retries an unacknowledged restart with the same identity and original guards', async () => {
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('11111111-1111-4111-8111-111111111111');
    const { daemon } = await renderPanel({
      script: {
        crew_restart: [HOLD, restarted({
          member: member('trellis', 10, {
            binding_session: 'session-trellis', resolved_agent: 'claude',
            restart: { request_id: '11111111-1111-4111-8111-111111111111', session_id: 'session-trellis', state: CrewRestartState.Queued },
          }),
        })],
      },
      members: [member('trellis', 9, { binding_session: 'session-trellis', resolved_agent: 'claude' })],
    });

    await click(daemon, 'Handoff and restart');
    await click(daemon, 'Request handoff and restart');
    await act(() => vi.advanceTimersByTimeAsync(120_000));
    expect(screen.getByText('Restarting Trellis timed out')).toBeInTheDocument();
    await click(daemon, 'Retry delivery');

    const guards = restartGuards(daemon);
    expect(guards).toHaveLength(2);
    expect(guards[0]).toEqual(guards[1]);
    expect(guards[1]).toEqual({
      member: 'trellis',
      request_id: '11111111-1111-4111-8111-111111111111',
      expected_session_id: 'session-trellis',
      expected_revision: 9,
    });
  });

  it('retries an accepted queued restart whose first delivery failed', async () => {
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('12121212-1212-4212-8212-121212121212');
    const queued = member('trellis', 10, {
      binding_session: 'session-trellis', resolved_agent: 'claude',
      restart: { request_id: '12121212-1212-4212-8212-121212121212', session_id: 'session-trellis', state: CrewRestartState.Queued },
    });
    const { daemon } = await renderPanel({
      script: { crew_restart: [restarted({ success: false, error: 'Session lookup failed', member: queued }), restarted({ member: queued })] },
      members: [member('trellis', 9, { binding_session: 'session-trellis', resolved_agent: 'claude' })],
    });

    await click(daemon, 'Handoff and restart');
    await click(daemon, 'Request handoff and restart');
    expect(screen.getByText('Session lookup failed')).toBeInTheDocument();
    await click(daemon, 'Retry delivery');

    const guards = restartGuards(daemon);
    expect(guards).toHaveLength(2);
    expect(guards[0]).toEqual(guards[1]);
  });

  it.each([CrewRestartState.Queued, CrewRestartState.Requested])(
    'retries an authoritative %s restart after the panel reloads',
    async (state) => {
      const { daemon } = await renderPanel({
        script: { crew_restart: [restarted({ success: false, error: 'Successor probe unavailable' }), restarted()] },
        members: [member('trellis', 10, {
          binding_session: 'session-trellis',
          resolved_agent: 'claude',
          restart: { request_id: '13131313-1313-4313-8313-131313131313', session_id: 'session-trellis', state },
        })],
      });

      expect(screen.getByRole('button', { name: 'Restart in progress…' })).toBeDisabled();
      await click(daemon, 'Retry restart');
      expect(screen.getByText('Successor probe unavailable')).toBeInTheDocument();
      await click(daemon, 'Retry delivery');

      const guards = restartGuards(daemon);
      expect(guards).toHaveLength(2);
      expect(guards[0]).toEqual(guards[1]);
      expect(guards[1]).toEqual({
        member: 'trellis',
        request_id: '13131313-1313-4313-8313-131313131313',
        expected_session_id: 'session-trellis',
        expected_revision: 10,
      });
    },
  );

  it('keeps the panel behind the restart confirmation out of the tab order', async () => {
    await renderPanel({ members: [member('trellis', 9, { binding_session: 'session-trellis', resolved_agent: 'claude' })] });
    fireEvent.click(screen.getByRole('button', { name: 'Handoff and restart' }));
    const dialog = screen.getByRole('alertdialog');
    expect(dialog.tagName).toBe('DIALOG');
    expect(screen.getByTestId('crew-panel-close').closest('[inert]')).not.toBeNull();
    expect(screen.getByLabelText('Crew roster').closest('[inert]')).not.toBeNull();
    expect(dialog.closest('[inert]')).toBeNull();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.getByTestId('crew-panel-close').closest('[inert]')).toBeNull();
  });

  it.each([
    [CrewRestartState.Completed, 'New day started'],
    [CrewRestartState.Failed, 'Successor launch failed'],
  ])('reconciles a lost restart response with authoritative %s state', async (state, copy) => {
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('22222222-2222-4222-8222-222222222222');
    const { daemon, rerenderPanel } = await renderPanel({
      script: { crew_restart: [restarted({ success: false, error: 'Restart response was lost' })] },
      members: [member('trellis', 9, { binding_session: 'session-trellis', resolved_agent: 'claude' })],
    });

    await click(daemon, 'Handoff and restart');
    await click(daemon, 'Request handoff and restart');
    expect(screen.getByText('Restart response was lost')).toBeInTheDocument();

    await rerenderPanel([member('trellis', 10, {
      binding_session: state === CrewRestartState.Completed ? 'successor-session' : 'session-trellis',
      resolved_agent: 'claude',
      restart: {
        request_id: '22222222-2222-4222-8222-222222222222',
        session_id: 'session-trellis',
        state,
        ...(state === CrewRestartState.Completed ? { successor_session_id: 'successor-session' } : { error: 'Successor launch failed' }),
      },
    })]);

    expect(screen.getByText(new RegExp(copy))).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Retry delivery' })).not.toBeInTheDocument();
  });

  it('shows a newer authoritative restart after the local attempt completed', async () => {
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('33333333-3333-4333-8333-333333333333');
    const localCompleted = member('trellis', 10, {
      binding_session: 'successor-session',
      resolved_agent: 'claude',
      restart: {
        request_id: '33333333-3333-4333-8333-333333333333',
        session_id: 'session-trellis',
        state: CrewRestartState.Completed,
        successor_session_id: 'successor-session',
      },
    });
    const { daemon, rerenderPanel } = await renderPanel({
      script: { crew_restart: [restarted({ member: localCompleted })] },
      members: [member('trellis', 9, { binding_session: 'session-trellis', resolved_agent: 'claude' })],
    });

    await click(daemon, 'Handoff and restart');
    await click(daemon, 'Request handoff and restart');
    expect(screen.getByText(/New day started/)).toBeInTheDocument();

    await rerenderPanel([member('trellis', 11, {
      binding_session: 'successor-session',
      resolved_agent: 'claude',
      restart: { request_id: '44444444-4444-4444-8444-444444444444', session_id: 'successor-session', state: CrewRestartState.Requested },
    })]);

    expect(screen.getByText('Handoff requested')).toBeInTheDocument();
    expect(screen.queryByText(/New day started/)).not.toBeInTheDocument();
  });

  it('closes with Escape and wakes an asleep member through the guarded restart action', async () => {
    const { daemon, onClose } = await renderPanel({ members: [member('keel', 5)] });

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('button', { name: 'Wake' }));
    const dialog = screen.getByRole('alertdialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Wake member' }));
    await daemon.idle();
    expect(restartGuards(daemon)).toEqual([expect.objectContaining({ member: 'keel', expected_session_id: '', expected_revision: 5 })]);
  });

  it('keeps roster navigation while async harness discovery settles', async () => {
    const { daemon, answer } = await renderPanel({
      script: { delegation_preferences_get: [HOLD] },
      initialMember: 'alder',
      members: [member('alder', 2), member('keel', 3)],
    });

    fireEvent.click(screen.getByRole('button', { name: /Keel/ }));
    expect(screen.getByRole('heading', { name: 'Keel' })).toBeInTheDocument();

    await answer(daemon.sentOf('delegation_preferences_get')[0], preferences([harnesses[0]]));
    expect(screen.getByLabelText('Harness')).toBeEnabled();
    expect(screen.getByRole('heading', { name: 'Keel' })).toBeInTheDocument();
    expect(daemon.sentOf('delegation_preferences_get')).toHaveLength(1);
  });

  it('allows model and effort pins while the harness follows the crew default', async () => {
    const { daemon } = await renderPanel({
      script: {
        crew_set: [
          saved({ member: member('keel', 6, { model: 'openai/gpt-6-astra', resolved_agent: 'codex', resolved_model: 'openai/gpt-6-astra' }) }),
          saved({ member: member('keel', 7, { model: 'openai/gpt-6-astra', effort: 'high', resolved_agent: 'codex', resolved_model: 'openai/gpt-6-astra', resolved_effort: 'high' }) }),
        ],
      },
      members: [member('keel', 5, { resolved_agent: 'codex' })],
    });

    const model = screen.getByLabelText('Model');
    expect(model).toBeEnabled();
    expect(screen.getByRole('option', { name: 'openai / Astra' })).toBeInTheDocument();
    fireEvent.change(model, { target: { value: 'openai/gpt-6-astra' } });
    await daemon.idle();
    expect(screen.getByText('Saved')).toBeInTheDocument();
    const effort = screen.getByLabelText('Reasoning effort');
    fireEvent.change(effort, { target: { value: 'hig' } });
    fireEvent.change(effort, { target: { value: 'high' } });
    expect(daemon.sentOf('crew_set')).toHaveLength(1);
    fireEvent.blur(effort);
    await daemon.idle();

    expect(crewSets(daemon)).toEqual([
      { member: 'keel', expected_revision: 5, agent: '', model: 'openai/gpt-6-astra', effort: '' },
      { member: 'keel', expected_revision: 6, agent: '', model: 'openai/gpt-6-astra', effort: 'high' },
    ]);
  });

  it('commits a typed model id on Enter as one write and keeps the draft over roster pushes', async () => {
    const { daemon, rerenderPanel } = await renderPanel({ members: [member('keel', 6)] });
    const model = screen.getByLabelText('Model');
    expect(model).toBeEnabled();
    fireEvent.change(model, { target: { value: '__custom' } });
    const custom = screen.getByTestId('crew-custom-model');
    fireEvent.change(custom, { target: { value: 'gpt-7' } });
    await rerenderPanel([member('keel', 7)]);
    expect(custom).toHaveValue('gpt-7');
    expect(daemon.sentOf('crew_set')).toEqual([]);
    fireEvent.keyDown(custom, { key: 'Enter' });
    await daemon.idle();
    expect(crewSets(daemon)).toEqual([{ member: 'keel', expected_revision: 7, agent: '', model: 'gpt-7', effort: '' }]);
  });

  it('keeps dependent controls pending while an explicit harness pin clears', async () => {
    const { daemon, answer } = await renderPanel({
      script: { crew_set: [HOLD] },
      members: [member('keel', 5, { agent: 'codex', resolved_agent: 'codex', model: 'openai/gpt-6-astra', resolved_model: 'openai/gpt-6-astra' })],
    });

    const model = screen.getByLabelText('Model');
    expect(model).toBeEnabled();
    fireEvent.change(screen.getByLabelText('Harness'), { target: { value: '' } });

    expect(model).toBeDisabled();
    expect(screen.getByLabelText('Reasoning effort')).toBeDisabled();

    await answer(daemon.sentOf('crew_set')[0], saved({ member: member('keel', 6, { resolved_agent: 'claude' }) }));
    expect(model).toBeEnabled();
  });

  it('selects provider-qualified model identities when bare ids collide', async () => {
    const { daemon } = await renderPanel({
      script: {
        crew_set: [saved({ member: member('keel', 6, { agent: 'codex', model: 'second/shared', resolved_agent: 'codex', resolved_model: 'second/shared' }) })],
        delegation_models: [models([
          { harness: 'codex', provider: 'first', id: 'shared', name: 'Shared one', description: '', detail: '', access: 'supported', effort_support: 'supported', effort_levels: ['low'] },
          { harness: 'codex', provider: 'second', id: 'shared', name: 'Shared two', description: '', detail: '', access: 'supported', effort_support: 'supported', effort_levels: ['high'] },
        ])],
      },
      members: [member('keel', 5, { agent: 'codex', resolved_agent: 'codex' })],
    });

    const model = screen.getByLabelText('Model');
    expect(screen.getByRole('option', { name: 'second / Shared two' })).toBeInTheDocument();
    fireEvent.change(model, { target: { value: 'second/shared' } });
    await daemon.idle();

    expect(crewSets(daemon)).toEqual([{ member: 'keel', expected_revision: 5, agent: 'codex', model: 'second/shared', effort: '' }]);
    expect(model).toHaveValue('second/shared');
  });

  it('keeps an unsupported models explicit effort clear through a concurrent update', async () => {
    const { daemon, answer } = await renderPanel({
      script: {
        crew_set: [
          saved({ success: false, conflict: true, error: 'revision conflict', member: member('keel', 6, { agent: 'codex', effort: 'high', resolved_agent: 'codex', resolved_effort: 'high' }) }),
          HOLD,
        ],
        delegation_models: [models([
          { harness: 'codex', provider: 'local', id: 'fixed', name: 'Fixed', description: '', detail: '', access: 'supported', effort_support: 'unsupported', effort_levels: [] },
        ])],
      },
      members: [member('keel', 5, { agent: 'codex', resolved_agent: 'codex' })],
    });

    const model = screen.getByLabelText('Model');
    expect(screen.getByRole('option', { name: 'local / Fixed' })).toBeInTheDocument();
    fireEvent.change(model, { target: { value: 'local/fixed' } });
    await daemon.idle();
    expect(screen.getByText('Not saved')).toBeInTheDocument();
    await click(daemon, 'Retry');

    expect(crewSets(daemon)[1]).toEqual({ member: 'keel', expected_revision: 6, agent: 'codex', model: 'local/fixed', effort: '' });
    await answer(daemon.sentOf('crew_set')[1], saved({ member: member('keel', 7, { agent: 'codex', model: 'local/fixed', resolved_agent: 'codex', resolved_model: 'local/fixed' }) }));
    expect(screen.getByText('Saved')).toBeInTheDocument();
  });

  it('loads the full charter on demand and flushes it before tab navigation', async () => {
    const { daemon, answer } = await renderPanel({
      script: {
        crew_charter_get: [charter('# Trellis\n\nFull **Markdown** charter.\n', 'charter-old')],
        crew_charter_set: [HOLD],
      },
    });

    await click(daemon, 'Charter');
    const editor = screen.getByTestId('crew-charter-editor');
    expect(editor).toHaveValue('# Trellis\n\nFull **Markdown** charter.\n');
    fireEvent.change(editor, { target: { value: '# Trellis\n\nChanged while the idea is hot.\n' } });
    expect(screen.getByRole('status')).toHaveTextContent('Waiting to save');

    await click(daemon, 'Handoffs');
    expect(screen.getByTestId('crew-charter-editor')).toBeInTheDocument();
    expect(daemon.sentOf('crew_charter_set')).toEqual([expect.objectContaining({
      member: 'trellis', content: '# Trellis\n\nChanged while the idea is hot.\n', expected_token: 'charter-old',
    })]);
    expect(screen.getByRole('status')).toHaveTextContent('Saving');

    await answer(daemon.sentOf('crew_charter_set')[0], charterSaved('# Trellis\n\nChanged while the idea is hot.\n', 'charter-new'));
    expect(screen.getByText('No handoffs recorded.')).toBeInTheDocument();
    expect(daemon.sentOf('crew_handoffs_get').map((command) => command.member)).toEqual(['trellis']);
  });

  it('uses only the latest navigation intent while one charter flush is pending', async () => {
    const { daemon, answer } = await renderPanel({
      script: { crew_charter_get: [charter('old', 'old-token')], crew_charter_set: [HOLD] },
      members: [member('trellis', 4), member('keel', 5)],
    });
    await click(daemon, 'Charter');
    fireEvent.change(screen.getByTestId('crew-charter-editor'), { target: { value: 'new' } });

    fireEvent.click(screen.getByRole('button', { name: /Keel/ }));
    await click(daemon, 'Handoffs');
    expect(daemon.sentOf('crew_charter_set')).toHaveLength(1);
    await answer(daemon.sentOf('crew_charter_set')[0], charterSaved('new', 'new-token'));

    expect(screen.getByText('No handoffs recorded.')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Trellis' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Handoffs' })).toHaveAttribute('aria-current', 'page');
    expect(daemon.sentOf('crew_handoffs_get')).toHaveLength(1);
  });

  it('returns to launch settings on a normal reopen', async () => {
    const members = [member('alder', 2), member('trellis', 3)];
    const { daemon, rerenderPanel } = await renderPanel({ members, initialMember: 'alder' });
    await click(daemon, 'Handoffs');
    expect(screen.getByRole('button', { name: 'Handoffs' })).toHaveAttribute('aria-current', 'page');

    await rerenderPanel(members, false);
    await rerenderPanel(members, true);

    expect(screen.getByRole('heading', { name: 'Alder' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Launch settings' })).toHaveAttribute('aria-current', 'page');
  });

  it('flushes a charter before closing the panel', async () => {
    const { daemon, onClose, answer } = await renderPanel({
      script: { crew_charter_get: [charter('old', 'old-token')], crew_charter_set: [HOLD] },
    });
    await click(daemon, 'Charter');
    fireEvent.change(screen.getByTestId('crew-charter-editor'), { target: { value: 'new' } });
    fireEvent.click(screen.getByTestId('crew-panel-close'));
    await daemon.idle();
    expect(onClose).not.toHaveBeenCalled();

    await answer(daemon.sentOf('crew_charter_set')[0], charterSaved('new', 'new-token'));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('keeps a failed navigation flush visible and retries the retained edit', async () => {
    const { daemon } = await renderPanel({
      script: {
        crew_charter_get: [charter('old', 'old')],
        crew_charter_set: [charterRefused('disk is read-only'), charterSaved('local edit', 'new')],
      },
    });
    await click(daemon, 'Charter');
    const editor = screen.getByTestId('crew-charter-editor');
    fireEvent.change(editor, { target: { value: 'local edit' } });
    await click(daemon, 'Launch settings');

    expect(screen.getByText('disk is read-only')).toBeInTheDocument();
    expect(editor).toHaveValue('local edit');
    expect(screen.getByRole('button', { name: 'Charter' })).toHaveAttribute('aria-current', 'page');
    await click(daemon, 'Retry');
    expect(screen.getByRole('status')).toHaveTextContent('Saved');
    await click(daemon, 'Launch settings');
    expect(screen.getByRole('button', { name: 'Launch settings' })).toHaveAttribute('aria-current', 'page');
  });

  it('lets the user leave after a charter save failure has been shown, keeping the edit for later', async () => {
    const { daemon } = await renderPanel({ script: { crew_charter_get: [charter('old', 'old')] } });
    await click(daemon, 'Charter');
    daemon.disconnect();
    await daemon.idle();
    fireEvent.change(screen.getByTestId('crew-charter-editor'), { target: { value: 'offline edit' } });
    await click(daemon, 'Launch settings');
    expect(screen.getByText('WebSocket not connected')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Charter' })).toHaveAttribute('aria-current', 'page');

    await click(daemon, 'Launch settings');
    expect(screen.getByRole('button', { name: 'Launch settings' })).toHaveAttribute('aria-current', 'page');
    await click(daemon, 'Charter');
    expect(screen.getByTestId('crew-charter-editor')).toHaveValue('offline edit');
    expect(screen.getByText('WebSocket not connected')).toBeInTheDocument();
    expect(daemon.sentOf('crew_charter_set')).toEqual([]);
  });

  it('keeps a retained offline charter edit across close and a fresh reopen', async () => {
    const members = [member('trellis', 4)];
    const { daemon, onClose, rerenderPanel } = await renderPanel({ members, script: { crew_charter_get: [charter('old', 'old')] } });
    await click(daemon, 'Charter');
    daemon.disconnect();
    await daemon.idle();
    fireEvent.change(screen.getByTestId('crew-charter-editor'), { target: { value: 'offline edit' } });
    await click(daemon, 'Launch settings');
    expect(screen.getByText('WebSocket not connected')).toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
    await rerenderPanel(members, false);
    await rerenderPanel(members, true);

    expect(screen.getByRole('button', { name: 'Launch settings' })).toHaveAttribute('aria-current', 'page');
    await click(daemon, 'Charter');
    expect(screen.getByTestId('crew-charter-editor')).toHaveValue('offline edit');
    expect(screen.getByTestId('crew-charter-status')).not.toHaveTextContent('Saved');
  });

  it('rereads a saved charter when the tab is entered again', async () => {
    const { daemon } = await renderPanel({
      members: [member('trellis', 4)],
      script: { crew_charter_get: [charter('first', 'first'), charter('edited elsewhere', 'second')] },
    });
    await click(daemon, 'Charter');
    expect(screen.getByTestId('crew-charter-editor')).toHaveValue('first');

    await click(daemon, 'Launch settings');
    await click(daemon, 'Charter');
    expect(screen.getByTestId('crew-charter-editor')).toHaveValue('edited elsewhere');
    expect(daemon.sentOf('crew_charter_get')).toHaveLength(2);
  });

  it('commits a launch field that still has focus when Escape closes the panel', async () => {
    const { daemon, onClose, rerenderPanel } = await renderPanel({ members: [member('keel', 6)] });
    const effort = screen.getByLabelText('Reasoning effort');
    effort.focus();
    fireEvent.change(effort, { target: { value: 'high' } });
    expect(daemon.sentOf('crew_set')).toEqual([]);

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
    await rerenderPanel([member('keel', 6)], false);

    expect(crewSets(daemon)).toEqual([expect.objectContaining({ member: 'keel', effort: 'high' })]);
  });

  it('renders no roster or member content while closed', async () => {
    await renderPanel({ isOpen: false });
    expect(screen.queryByRole('region', { name: 'Crew roster' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Crew roster')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Harness')).not.toBeInTheDocument();
  });

  it('returns the authoritative charter on conflict and requires an explicit choice', async () => {
    const { daemon } = await renderPanel({
      script: { crew_charter_get: [charter('old', 'old')], crew_charter_set: [charterSaved('external edit', 'external', true)] },
    });
    await click(daemon, 'Charter');
    const editor = screen.getByTestId('crew-charter-editor');
    fireEvent.change(editor, { target: { value: 'my edit' } });
    fireEvent.blur(editor);
    await daemon.idle();
    expect(screen.getByText('The file changed outside this editor.')).toBeInTheDocument();
    expect(editor).toHaveValue('my edit');
    fireEvent.click(screen.getByRole('button', { name: 'Use file version' }));
    expect(editor).toHaveValue('external edit');
    expect(screen.getByRole('status')).toHaveTextContent('Saved');
  });

  it.each([
    ['an asleep member opens the seed without placement', undefined],
    ['an awake member places the seed beside its current day', 'session-trellis'],
  ])('renders complete dated handoffs and %s', async (_, bindingSession) => {
    const body = '# Full handoff\n\nA paragraph at the end that must not be truncated.\n\n[Open the seed](s-w0rk11)\n';
    const { daemon, onOpenSeed } = await renderPanel({
      members: [member('trellis', 4, bindingSession ? { binding_session: bindingSession } : {})],
      script: {
        crew_handoffs_get: [handoffs('trellis', [{ filename: '2026-09-01T21-37Z-trellis.md', occurred_at: '2026-09-01T21:37:00Z' }])],
        crew_handoff_get: [handoff('2026-09-01T21-37Z-trellis.md', '2026-09-01T21:37:00Z', body, 'letter')],
      },
    });
    await click(daemon, 'Handoffs');
    expect(screen.getByRole('heading', { name: 'Full handoff' })).toBeInTheDocument();
    expect(daemon.sentOf('crew_handoff_get').map(({ member: id, filename }) => [id, filename])).toEqual([['trellis', '2026-09-01T21-37Z-trellis.md']]);
    expect(screen.getByText('A paragraph at the end that must not be truncated.')).toBeInTheDocument();
    expect(screen.getAllByText(/Sep 1, 2026/)).toHaveLength(2);
    fireEvent.click(screen.getByRole('button', { name: 'Open the seed' }));
    expect(onOpenSeed).toHaveBeenCalledWith('s-w0rk11', bindingSession);
  });

  it('shows one handoff read failure and retries to an honest empty history', async () => {
    const { daemon } = await renderPanel({
      members: [member('keel', 5)],
      script: { crew_handoffs_get: [handoffsRefused('handoffs are temporarily unavailable'), handoffs('keel', [])] },
    });
    await click(daemon, 'Handoffs');
    expect(screen.getByText('handoffs are temporarily unavailable')).toBeInTheDocument();
    expect(daemon.sentOf('crew_handoffs_get')).toHaveLength(1);
    await click(daemon, 'Retry');
    expect(screen.getByText('No handoffs recorded.')).toBeInTheDocument();
    expect(daemon.sentOf('crew_handoffs_get')).toHaveLength(2);
  });

  it('keeps a newer reconnect handoff result when the read from the dropped connection never returns', async () => {
    const { daemon, answer } = await renderPanel({
      script: {
        crew_handoffs_get: [HOLD],
        crew_handoff_get: [handoff('2026-09-02T09-00Z-trellis.md', '2026-09-02T09:00:00Z', '# Newer reconnect result\n', 'newer')],
      },
    });
    await click(daemon, 'Handoffs');
    expect(daemon.sentOf('crew_handoffs_get')).toHaveLength(1);

    await daemon.reconnect();
    await daemon.idle();
    const [, newer] = daemon.sentOf('crew_handoffs_get');
    expect(newer).toBeDefined();
    await answer(newer, handoffs('trellis', [{ filename: '2026-09-02T09-00Z-trellis.md', occurred_at: '2026-09-02T09:00:00Z' }]));
    expect(screen.getByRole('heading', { name: 'Newer reconnect result' })).toBeInTheDocument();

    await act(() => vi.advanceTimersByTimeAsync(60_000));
    await daemon.idle();
    expect(screen.getByRole('heading', { name: 'Newer reconnect result' })).toBeInTheDocument();
    expect(daemon.sentOf('crew_handoff_get').map(({ member: id, filename }) => [id, filename])).toEqual([['trellis', '2026-09-02T09-00Z-trellis.md']]);
  });

  it('reads one letter at a time and retries a failed letter without reloading the history', async () => {
    const latest = handoff('2026-09-02T09-00Z-trellis.md', '2026-09-02T09:00:00Z', '# Latest letter\n', 'a');
    const { daemon } = await renderPanel({
      script: {
        crew_handoffs_get: [handoffs('trellis', [
          { filename: '2026-09-02T09-00Z-trellis.md', occurred_at: '2026-09-02T09:00:00Z' },
          { filename: '2026-09-01T09-00Z-trellis.md', occurred_at: '2026-09-01T09:00:00Z' },
        ])],
        crew_handoff_get: [
          latest,
          handoffRefused('the older letter is unreadable'),
          handoff('2026-09-01T09-00Z-trellis.md', '2026-09-01T09:00:00Z', '# Older letter\n', 'b'),
          latest,
        ],
      },
    });
    await click(daemon, 'Handoffs');
    expect(screen.getByRole('heading', { name: 'Latest letter' })).toBeInTheDocument();
    expect(daemon.sentOf('crew_handoff_get')).toHaveLength(1);

    fireEvent.click(screen.getByTestId('crew-handoff-1'));
    await daemon.idle();
    expect(screen.getByText('the older letter is unreadable')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Latest letter' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('crew-handoff-letter-retry'));
    await daemon.idle();
    expect(screen.getByRole('heading', { name: 'Older letter' })).toBeInTheDocument();
    expect(daemon.sentOf('crew_handoff_get')).toHaveLength(3);
    expect(daemon.sentOf('crew_handoffs_get')).toHaveLength(1);

    fireEvent.click(screen.getByTestId('crew-handoff-0'));
    await daemon.idle();
    expect(screen.getByRole('heading', { name: 'Latest letter' })).toBeInTheDocument();
    expect(daemon.sentOf('crew_handoff_get')).toHaveLength(4);
  });
});
