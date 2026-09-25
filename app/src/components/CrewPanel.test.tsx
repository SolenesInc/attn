import { act, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CrewRestartState, type CrewMember } from '../types/generated';
import {
  agentPane,
  agentWorkspace,
  daemonSeed,
  daemonSession,
  daemonWorkspace,
  type DaemonSeed,
  type DaemonSession,
} from '../test/daemonFixtures';
import { gesture, renderApp } from '../test/renderApp';
import type { CommandMessage, CommandName } from '../test/protocol';
import type { Reply, ScriptedDaemon } from '../test/scriptedDaemon';

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

const panel = () => within(screen.getByTestId('crew-panel'));
const isPanelOpen = () => screen.getByTestId('crew-panel').hasAttribute('open');

async function renderPanel({
  script = {},
  members = [member('trellis', 4)],
  sessions = [],
  isOpen = true,
  seeds = [],
}: {
  script?: Script;
  members?: CrewMember[];
  sessions?: DaemonSession[];
  isOpen?: boolean;
  seeds?: DaemonSeed[];
} = {}) {
  const everySession = [daemonSession('s1'), ...sessions];
  const { daemon } = await renderApp({
    initialState: { crew: members, seeds, sessions: everySession, workspaces: everySession.map((session) => agentWorkspace(session.id)) },
  });
  for (const [cmd, answers] of Object.entries({ ...defaults, ...script })) {
    answerInTurn(daemon, cmd as CrewCommand, answers);
  }
  const openCrew = () => gesture(daemon, () => fireEvent.click(screen.getByTestId('manage-crew')));
  if (isOpen) await openCrew();
  const pushMembers = async (next: CrewMember[]) => {
    daemon.emit({ event: 'crew_updated', members: next });
    await daemon.idle();
  };
  const closeWithEscape = () => gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
  const answer = async (command: CommandMessage & { request_id?: string }, reply: Reply) => {
    daemon.replyTo(command, { ...reply, request_id: command.request_id });
    await daemon.idle();
  };
  return { daemon, openCrew, pushMembers, closeWithEscape, answer };
}

async function click(daemon: ScriptedDaemon, name: string | RegExp) {
  await gesture(daemon, () => fireEvent.click(panel().getByRole('button', { name })));
}

const crewSets = (daemon: ScriptedDaemon) => daemon.sentOf('crew_set').map(({ member: id, expected_revision, agent, model, effort }) => ({ member: id, expected_revision, agent, model, effort }));
const restartGuards = (daemon: ScriptedDaemon) => daemon.sentOf('crew_restart').map(({ member: id, request_id, expected_session_id, expected_revision }) => ({ member: id, request_id, expected_session_id, expected_revision }));
const openedSeeds = (daemon: ScriptedDaemon) => daemon.sentOf('open_seed').map(({ seed_id, session_id, standalone }) => ({ seed_id, session_id, standalone }));

afterEach(() => {
  vi.restoreAllMocks();
});

describe('CrewPanel', () => {
  it('uses a native dialog inside the sidebar-adjacent panel layer', async () => {
    await renderPanel();
    const dialog = screen.getByTestId('crew-panel');
    expect(dialog.tagName).toBe('DIALOG');
    expect(dialog).toHaveAttribute('open');
    expect(dialog.closest('.crew-panel-layer')).toBeInTheDocument();
  });

  it('keeps member, tab, seed filter and search when a workspace seed returns to Crew', async () => {
    const planted = daemonSeed('s-g9yxwv', { title: 'Artifact presence comes from the daemon', status: 'planted', planter_member: 'keel' });
    const members = [member('alder', 2), member('keel', 3, { binding_session: 'session-keel' })];
    const { daemon } = await renderPanel({ members, seeds: [planted], sessions: [daemonSession('session-keel')] });
    daemon.on('open_seed', ({ seed_id, session_id }) => ({
      event: 'open_seed_result', success: true, seed_id, workspace_id: `workspace-${session_id}`, tile_id: 'tile-seed',
    }));

    expect(panel().getByLabelText('Harness')).toBeEnabled();
    fireEvent.click(panel().getByRole('button', { name: /Keel/ }));
    fireEvent.click(panel().getByRole('button', { name: 'Seeds' }));
    fireEvent.click(panel().getByRole('button', { name: /Planted/ }));
    fireEvent.change(panel().getByLabelText('Find a seed'), { target: { value: 'Artifact presence' } });
    await click(daemon, /Artifact presence comes from the daemon/);
    expect(openedSeeds(daemon)).toEqual([{ seed_id: planted.id, session_id: 'session-keel', standalone: undefined }]);
    expect(isPanelOpen()).toBe(false);

    daemon.emit({
      event: 'workspace_state_changed',
      workspace: daemonWorkspace('workspace-session-keel', {
        root: {
          type: 'split', split_id: 'split-seed', direction: 'vertical',
          children: [
            { type: 'pane', pane_id: 'pane-session-keel' },
            { type: 'tile', tile_id: 'tile-seed', tile_kind: 'seed', tile_params: planted.id },
          ],
        },
        panes: [agentPane('session-keel', 'workspace-session-keel')],
      }, { title: 'session-keel', directory: '/tmp/session-keel' }),
    });
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('crew-seed-back')));

    expect(isPanelOpen()).toBe(true);
    expect(panel().getByRole('heading', { name: 'Keel' })).toBeInTheDocument();
    expect(panel().getByRole('button', { name: 'Seeds' })).toHaveAttribute('aria-current', 'page');
    expect(panel().getByRole('button', { name: /^Planted/ })).toHaveAttribute('aria-pressed', 'true');
    expect(panel().getByLabelText('Find a seed')).toHaveValue('Artifact presence');
  });

  it('keeps actual running values separate from acknowledged next-wake settings', async () => {
    const { daemon } = await renderPanel({
      members: [member('trellis', 4, {
        binding_session: 'session-trellis',
        agent: 'codex', model: 'gpt-6-astra', effort: 'high',
        resolved_agent: 'codex', resolved_model: 'gpt-6-astra', resolved_effort: 'high',
      })],
      sessions: [daemonSession('session-trellis', { agent: 'claude' })],
    });

    const running = panel().getByLabelText('Running now');
    expect(running).toHaveTextContent('Harnessclaude');
    expect(running).toHaveTextContent('ModelNot reported');
    expect(running).toHaveTextContent('EffortNot reported');
    expect(panel().getByText('Acknowledged next wake').parentElement).toHaveTextContent('codex / gpt-6-astra / high');
    expect(panel().getByLabelText('Harness')).toHaveValue('codex');
    expect(panel().getByRole('option', { name: 'openai / Astra' })).toBeInTheDocument();
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

    const harness = panel().getByLabelText('Harness');
    fireEvent.change(harness, { target: { value: '' } });

    expect(crewSets(daemon)).toEqual([{ member: 'alder', expected_revision: 7, agent: '', model: '', effort: '' }]);
    expect(panel().getByRole('status', { name: '' })).toHaveTextContent('Saving…');
    expect(panel().getByRole('button', { name: 'Handoff and restart' })).toBeDisabled();

    await answer(daemon.sentOf('crew_set')[0], saved({ member: member('alder', 8, { binding_session: 'session-alder', resolved_agent: 'claude' }) }));
    expect(panel().getByText('Saved')).toBeInTheDocument();
    expect(panel().getByRole('button', { name: 'Handoff and restart' })).toBeEnabled();
    expect(harness).toHaveValue('');
  });

  it('keeps a failed member edit through roster navigation and retries it', async () => {
    const { daemon, answer } = await renderPanel({
      script: { crew_set: [saved({ success: false, error: 'model discovery is unavailable' }), HOLD] },
      members: [member('alder', 2), member('keel', 3)],
    });

    const effort = panel().getByLabelText('Reasoning effort');
    fireEvent.change(panel().getByLabelText('Harness'), { target: { value: 'codex' } });
    await daemon.idle();
    expect(panel().getByText('Not saved')).toBeInTheDocument();
    expect(panel().getByText('model discovery is unavailable')).toBeInTheDocument();

    fireEvent.click(panel().getByRole('button', { name: /Keel/ }));
    fireEvent.click(panel().getByRole('button', { name: /Alder/ }));
    expect(panel().getByLabelText('Harness')).toHaveValue('codex');
    expect(effort).toHaveValue('');

    fireEvent.click(panel().getByRole('button', { name: 'Retry' }));
    expect(daemon.sentOf('crew_set')).toHaveLength(2);
    await answer(daemon.sentOf('crew_set')[1], saved({ member: member('alder', 3, { agent: 'codex', resolved_agent: 'codex' }) }));
    expect(panel().getByText('Saved')).toBeInTheDocument();
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
    expect(panel().getByText('Restarting Trellis timed out')).toBeInTheDocument();
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

  it('settles a restart only on the answer to its own request', async () => {
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('44444444-4444-4444-8444-444444444444');
    const { daemon, answer } = await renderPanel({
      script: { crew_restart: [HOLD] },
      members: [member('trellis', 9, { binding_session: 'session-trellis', resolved_agent: 'claude' })],
    });
    await click(daemon, 'Handoff and restart');
    await click(daemon, 'Request handoff and restart');

    daemon.emit(restarted({ request_id: 'another-request', success: false, error: 'Another restart failed' }));
    await daemon.idle();
    expect(panel().getByRole('button', { name: 'Restart in progress…' })).toBeDisabled();
    expect(panel().queryByText('Another restart failed')).not.toBeInTheDocument();

    await answer(daemon.sentOf('crew_restart')[0], restarted({
      member: member('trellis', 10, {
        binding_session: 'successor-session',
        resolved_agent: 'claude',
        restart: {
          request_id: '44444444-4444-4444-8444-444444444444',
          session_id: 'session-trellis',
          state: CrewRestartState.Completed,
          successor_session_id: 'successor-session',
        },
      }),
    }));
    expect(panel().getByText(/New day started/)).toBeInTheDocument();
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
    expect(panel().getByText('Session lookup failed')).toBeInTheDocument();
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

      expect(panel().getByRole('button', { name: 'Restart in progress…' })).toBeDisabled();
      await click(daemon, 'Retry restart');
      expect(panel().getByText('Successor probe unavailable')).toBeInTheDocument();
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
    fireEvent.click(panel().getByRole('button', { name: 'Handoff and restart' }));
    const dialog = panel().getByRole('alertdialog');
    expect(dialog.tagName).toBe('DIALOG');
    expect(screen.getByTestId('crew-panel-close').closest('[inert]')).not.toBeNull();
    expect(panel().getByLabelText('Crew roster').closest('[inert]')).not.toBeNull();
    expect(dialog.closest('[inert]')).toBeNull();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.getByTestId('crew-panel-close').closest('[inert]')).toBeNull();
  });

  it.each([
    [CrewRestartState.Completed, 'New day started'],
    [CrewRestartState.Failed, 'Successor launch failed'],
  ])('reconciles a lost restart response with authoritative %s state', async (state, copy) => {
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('22222222-2222-4222-8222-222222222222');
    const { daemon, pushMembers } = await renderPanel({
      script: { crew_restart: [restarted({ success: false, error: 'Restart response was lost' })] },
      members: [member('trellis', 9, { binding_session: 'session-trellis', resolved_agent: 'claude' })],
    });

    await click(daemon, 'Handoff and restart');
    await click(daemon, 'Request handoff and restart');
    expect(panel().getByText('Restart response was lost')).toBeInTheDocument();

    await pushMembers([member('trellis', 10, {
      binding_session: state === CrewRestartState.Completed ? 'successor-session' : 'session-trellis',
      resolved_agent: 'claude',
      restart: {
        request_id: '22222222-2222-4222-8222-222222222222',
        session_id: 'session-trellis',
        state,
        ...(state === CrewRestartState.Completed ? { successor_session_id: 'successor-session' } : { error: 'Successor launch failed' }),
      },
    })]);

    expect(panel().getByText(new RegExp(copy))).toBeInTheDocument();
    expect(panel().queryByRole('button', { name: 'Retry delivery' })).not.toBeInTheDocument();
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
    const { daemon, pushMembers } = await renderPanel({
      script: { crew_restart: [restarted({ member: localCompleted })] },
      members: [member('trellis', 9, { binding_session: 'session-trellis', resolved_agent: 'claude' })],
    });

    await click(daemon, 'Handoff and restart');
    await click(daemon, 'Request handoff and restart');
    expect(panel().getByText(/New day started/)).toBeInTheDocument();

    await pushMembers([member('trellis', 11, {
      binding_session: 'successor-session',
      resolved_agent: 'claude',
      restart: { request_id: '44444444-4444-4444-8444-444444444444', session_id: 'successor-session', state: CrewRestartState.Requested },
    })]);

    expect(panel().getByText('Handoff requested')).toBeInTheDocument();
    expect(panel().queryByText(/New day started/)).not.toBeInTheDocument();
  });

  it('closes with Escape and wakes an asleep member through the guarded restart action', async () => {
    const { daemon, openCrew, closeWithEscape } = await renderPanel({ members: [member('keel', 5)] });

    await closeWithEscape();
    expect(isPanelOpen()).toBe(false);

    await openCrew();
    fireEvent.click(panel().getByRole('button', { name: 'Wake' }));
    const dialog = panel().getByRole('alertdialog');
    await gesture(daemon, () => fireEvent.click(within(dialog).getByRole('button', { name: 'Wake member' })));
    expect(restartGuards(daemon)).toEqual([expect.objectContaining({ member: 'keel', expected_session_id: '', expected_revision: 5 })]);
  });

  it('keeps roster navigation while async harness discovery settles', async () => {
    const { daemon, answer } = await renderPanel({
      script: { delegation_preferences_get: [HOLD] },
      members: [member('alder', 2), member('keel', 3)],
    });

    fireEvent.click(panel().getByRole('button', { name: /Keel/ }));
    expect(panel().getByRole('heading', { name: 'Keel' })).toBeInTheDocument();

    await answer(daemon.sentOf('delegation_preferences_get')[0], preferences([harnesses[0]]));
    expect(panel().getByLabelText('Harness')).toBeEnabled();
    expect(panel().getByRole('heading', { name: 'Keel' })).toBeInTheDocument();
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

    const model = panel().getByLabelText('Model');
    expect(model).toBeEnabled();
    expect(panel().getByRole('option', { name: 'openai / Astra' })).toBeInTheDocument();
    fireEvent.change(model, { target: { value: 'openai/gpt-6-astra' } });
    await daemon.idle();
    expect(panel().getByText('Saved')).toBeInTheDocument();
    const effort = panel().getByLabelText('Reasoning effort');
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
    const { daemon, pushMembers } = await renderPanel({ members: [member('keel', 6)] });
    const model = panel().getByLabelText('Model');
    expect(model).toBeEnabled();
    fireEvent.change(model, { target: { value: '__custom' } });
    const custom = panel().getByTestId('crew-custom-model');
    fireEvent.change(custom, { target: { value: 'gpt-7' } });
    await pushMembers([member('keel', 7)]);
    expect(custom).toHaveValue('gpt-7');
    expect(daemon.sentOf('crew_set')).toEqual([]);
    await gesture(daemon, () => fireEvent.keyDown(custom, { key: 'Enter' }));
    expect(crewSets(daemon)).toEqual([{ member: 'keel', expected_revision: 7, agent: '', model: 'gpt-7', effort: '' }]);
  });

  it('keeps dependent controls pending while an explicit harness pin clears', async () => {
    const { daemon, answer } = await renderPanel({
      script: { crew_set: [HOLD] },
      members: [member('keel', 5, { agent: 'codex', resolved_agent: 'codex', model: 'openai/gpt-6-astra', resolved_model: 'openai/gpt-6-astra' })],
    });

    const model = panel().getByLabelText('Model');
    expect(model).toBeEnabled();
    fireEvent.change(panel().getByLabelText('Harness'), { target: { value: '' } });

    expect(model).toBeDisabled();
    expect(panel().getByLabelText('Reasoning effort')).toBeDisabled();

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

    const model = panel().getByLabelText('Model');
    expect(panel().getByRole('option', { name: 'second / Shared two' })).toBeInTheDocument();
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

    const model = panel().getByLabelText('Model');
    expect(panel().getByRole('option', { name: 'local / Fixed' })).toBeInTheDocument();
    fireEvent.change(model, { target: { value: 'local/fixed' } });
    await daemon.idle();
    expect(panel().getByText('Not saved')).toBeInTheDocument();
    await click(daemon, 'Retry');

    expect(crewSets(daemon)[1]).toEqual({ member: 'keel', expected_revision: 6, agent: 'codex', model: 'local/fixed', effort: '' });
    await answer(daemon.sentOf('crew_set')[1], saved({ member: member('keel', 7, { agent: 'codex', model: 'local/fixed', resolved_agent: 'codex', resolved_model: 'local/fixed' }) }));
    expect(panel().getByText('Saved')).toBeInTheDocument();
  });

  it('loads the full charter on demand and flushes it before tab navigation', async () => {
    const { daemon, answer } = await renderPanel({
      script: {
        crew_charter_get: [charter('# Trellis\n\nFull **Markdown** charter.\n', 'charter-old')],
        crew_charter_set: [HOLD],
      },
    });

    await click(daemon, 'Charter');
    const editor = panel().getByTestId('crew-charter-editor');
    expect(editor).toHaveValue('# Trellis\n\nFull **Markdown** charter.\n');
    fireEvent.change(editor, { target: { value: '# Trellis\n\nChanged while the idea is hot.\n' } });
    expect(panel().getByRole('status')).toHaveTextContent('Waiting to save');

    await click(daemon, 'Handoffs');
    expect(panel().getByTestId('crew-charter-editor')).toBeInTheDocument();
    expect(daemon.sentOf('crew_charter_set')).toEqual([expect.objectContaining({
      member: 'trellis', content: '# Trellis\n\nChanged while the idea is hot.\n', expected_token: 'charter-old',
    })]);
    expect(panel().getByRole('status')).toHaveTextContent('Saving');

    await answer(daemon.sentOf('crew_charter_set')[0], charterSaved('# Trellis\n\nChanged while the idea is hot.\n', 'charter-new'));
    expect(panel().getByText('No handoffs recorded.')).toBeInTheDocument();
    expect(daemon.sentOf('crew_handoffs_get').map((command) => command.member)).toEqual(['trellis']);
  });

  it('uses only the latest navigation intent while one charter flush is pending', async () => {
    const { daemon, answer } = await renderPanel({
      script: { crew_charter_get: [charter('old', 'old-token')], crew_charter_set: [HOLD] },
      members: [member('trellis', 4), member('keel', 5)],
    });
    await click(daemon, 'Charter');
    fireEvent.change(panel().getByTestId('crew-charter-editor'), { target: { value: 'new' } });

    fireEvent.click(panel().getByRole('button', { name: /Keel/ }));
    await click(daemon, 'Handoffs');
    expect(daemon.sentOf('crew_charter_set')).toHaveLength(1);
    await answer(daemon.sentOf('crew_charter_set')[0], charterSaved('new', 'new-token'));

    expect(panel().getByText('No handoffs recorded.')).toBeInTheDocument();
    expect(panel().getByRole('heading', { name: 'Trellis' })).toBeInTheDocument();
    expect(panel().getByRole('button', { name: 'Handoffs' })).toHaveAttribute('aria-current', 'page');
    expect(daemon.sentOf('crew_handoffs_get')).toHaveLength(1);
  });

  it('returns to launch settings on a normal reopen', async () => {
    const { daemon, openCrew, closeWithEscape } = await renderPanel({ members: [member('alder', 2), member('trellis', 3)] });
    fireEvent.click(panel().getByRole('button', { name: /Trellis/ }));
    await click(daemon, 'Handoffs');
    expect(panel().getByRole('button', { name: 'Handoffs' })).toHaveAttribute('aria-current', 'page');

    await closeWithEscape();
    await openCrew();

    expect(panel().getByRole('heading', { name: 'Alder' })).toBeInTheDocument();
    expect(panel().getByRole('button', { name: 'Launch settings' })).toHaveAttribute('aria-current', 'page');
  });

  it('flushes a charter before closing the panel', async () => {
    const { daemon, answer } = await renderPanel({
      script: { crew_charter_get: [charter('old', 'old-token')], crew_charter_set: [HOLD] },
    });
    await click(daemon, 'Charter');
    fireEvent.change(panel().getByTestId('crew-charter-editor'), { target: { value: 'new' } });
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('crew-panel-close')));
    expect(isPanelOpen()).toBe(true);

    await answer(daemon.sentOf('crew_charter_set')[0], charterSaved('new', 'new-token'));
    expect(isPanelOpen()).toBe(false);
  });

  it('keeps a failed navigation flush visible and retries the retained edit', async () => {
    const { daemon } = await renderPanel({
      script: {
        crew_charter_get: [charter('old', 'old')],
        crew_charter_set: [charterRefused('disk is read-only'), charterSaved('local edit', 'new')],
      },
    });
    await click(daemon, 'Charter');
    const editor = panel().getByTestId('crew-charter-editor');
    fireEvent.change(editor, { target: { value: 'local edit' } });
    await click(daemon, 'Launch settings');

    expect(panel().getByText('disk is read-only')).toBeInTheDocument();
    expect(editor).toHaveValue('local edit');
    expect(panel().getByRole('button', { name: 'Charter' })).toHaveAttribute('aria-current', 'page');
    await click(daemon, 'Retry');
    expect(panel().getByRole('status')).toHaveTextContent('Saved');
    await click(daemon, 'Launch settings');
    expect(panel().getByRole('button', { name: 'Launch settings' })).toHaveAttribute('aria-current', 'page');
  });

  it('lets the user leave after a charter save failure has been shown, keeping the edit for later', async () => {
    const { daemon } = await renderPanel({ script: { crew_charter_get: [charter('old', 'old')] } });
    await click(daemon, 'Charter');
    daemon.disconnect();
    await daemon.idle();
    fireEvent.change(panel().getByTestId('crew-charter-editor'), { target: { value: 'offline edit' } });
    await click(daemon, 'Launch settings');
    expect(panel().getByText('WebSocket not connected')).toBeInTheDocument();
    expect(panel().getByRole('button', { name: 'Charter' })).toHaveAttribute('aria-current', 'page');

    await click(daemon, 'Launch settings');
    expect(panel().getByRole('button', { name: 'Launch settings' })).toHaveAttribute('aria-current', 'page');
    await click(daemon, 'Charter');
    expect(panel().getByTestId('crew-charter-editor')).toHaveValue('offline edit');
    expect(panel().getByText('WebSocket not connected')).toBeInTheDocument();
    expect(daemon.sentOf('crew_charter_set')).toEqual([]);
  });

  it('keeps a retained offline charter edit across close and a fresh reopen', async () => {
    const { daemon, openCrew, closeWithEscape } = await renderPanel({ script: { crew_charter_get: [charter('old', 'old')] } });
    await click(daemon, 'Charter');
    daemon.disconnect();
    await daemon.idle();
    fireEvent.change(panel().getByTestId('crew-charter-editor'), { target: { value: 'offline edit' } });
    await click(daemon, 'Launch settings');
    expect(panel().getByText('WebSocket not connected')).toBeInTheDocument();

    await closeWithEscape();
    expect(isPanelOpen()).toBe(false);
    await openCrew();

    expect(panel().getByRole('button', { name: 'Launch settings' })).toHaveAttribute('aria-current', 'page');
    await click(daemon, 'Charter');
    expect(panel().getByTestId('crew-charter-editor')).toHaveValue('offline edit');
    expect(panel().getByTestId('crew-charter-status')).not.toHaveTextContent('Saved');
  });

  it('rereads a saved charter when the tab is entered again', async () => {
    const { daemon } = await renderPanel({
      members: [member('trellis', 4)],
      script: { crew_charter_get: [charter('first', 'first'), charter('edited elsewhere', 'second')] },
    });
    await click(daemon, 'Charter');
    expect(panel().getByTestId('crew-charter-editor')).toHaveValue('first');

    await click(daemon, 'Launch settings');
    await click(daemon, 'Charter');
    expect(panel().getByTestId('crew-charter-editor')).toHaveValue('edited elsewhere');
    expect(daemon.sentOf('crew_charter_get')).toHaveLength(2);
  });

  it('commits a launch field that still has focus when Escape closes the panel', async () => {
    const { daemon, closeWithEscape } = await renderPanel({ members: [member('keel', 6)] });
    const effort = panel().getByLabelText('Reasoning effort');
    effort.focus();
    fireEvent.change(effort, { target: { value: 'high' } });
    expect(daemon.sentOf('crew_set')).toEqual([]);

    await closeWithEscape();
    expect(isPanelOpen()).toBe(false);
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
    const editor = panel().getByTestId('crew-charter-editor');
    fireEvent.change(editor, { target: { value: 'my edit' } });
    await gesture(daemon, () => fireEvent.blur(editor));
    expect(panel().getByText('The file changed outside this editor.')).toBeInTheDocument();
    expect(editor).toHaveValue('my edit');
    fireEvent.click(panel().getByRole('button', { name: 'Use file version' }));
    expect(editor).toHaveValue('external edit');
    expect(panel().getByRole('status')).toHaveTextContent('Saved');
  });

  it.each([
    ['an asleep member opens the seed as a standalone reader', undefined],
    ['an awake member places the seed beside its current day', 'session-trellis'],
  ])('renders complete dated handoffs and %s', async (_, bindingSession) => {
    const body = '# Full handoff\n\nA paragraph at the end that must not be truncated.\n\n[Open the seed](s-w0rk11)\n';
    const { daemon } = await renderPanel({
      members: [member('trellis', 4, bindingSession ? { binding_session: bindingSession } : {})],
      script: {
        crew_handoffs_get: [handoffs('trellis', [{ filename: '2026-09-01T21-37Z-trellis.md', occurred_at: '2026-09-01T21:37:00Z' }])],
        crew_handoff_get: [handoff('2026-09-01T21-37Z-trellis.md', '2026-09-01T21:37:00Z', body, 'letter')],
      },
    });
    await click(daemon, 'Handoffs');
    expect(panel().getByRole('heading', { name: 'Full handoff' })).toBeInTheDocument();
    expect(daemon.sentOf('crew_handoff_get').map(({ member: id, filename }) => [id, filename])).toEqual([['trellis', '2026-09-01T21-37Z-trellis.md']]);
    expect(panel().getByText('A paragraph at the end that must not be truncated.')).toBeInTheDocument();
    expect(panel().getAllByText(/Sep 1, 2026/)).toHaveLength(2);
    await click(daemon, 'Open the seed');
    expect(openedSeeds(daemon)).toEqual([bindingSession
      ? { seed_id: 's-w0rk11', session_id: bindingSession, standalone: undefined }
      : { seed_id: 's-w0rk11', session_id: undefined, standalone: true }]);
  });

  it('shows one handoff read failure and retries to an honest empty history', async () => {
    const { daemon } = await renderPanel({
      members: [member('keel', 5)],
      script: { crew_handoffs_get: [handoffsRefused('handoffs are temporarily unavailable'), handoffs('keel', [])] },
    });
    await click(daemon, 'Handoffs');
    expect(panel().getByText('handoffs are temporarily unavailable')).toBeInTheDocument();
    expect(daemon.sentOf('crew_handoffs_get')).toHaveLength(1);
    await click(daemon, 'Retry');
    expect(panel().getByText('No handoffs recorded.')).toBeInTheDocument();
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
    expect(panel().getByRole('heading', { name: 'Newer reconnect result' })).toBeInTheDocument();

    await act(() => vi.advanceTimersByTimeAsync(60_000));
    await daemon.idle();
    expect(panel().getByRole('heading', { name: 'Newer reconnect result' })).toBeInTheDocument();
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
    expect(panel().getByRole('heading', { name: 'Latest letter' })).toBeInTheDocument();
    expect(daemon.sentOf('crew_handoff_get')).toHaveLength(1);

    await gesture(daemon, () => fireEvent.click(panel().getByTestId('crew-handoff-1')));
    expect(panel().getByText('the older letter is unreadable')).toBeInTheDocument();
    expect(panel().queryByRole('heading', { name: 'Latest letter' })).not.toBeInTheDocument();
    await gesture(daemon, () => fireEvent.click(panel().getByTestId('crew-handoff-letter-retry')));
    expect(panel().getByRole('heading', { name: 'Older letter' })).toBeInTheDocument();
    expect(daemon.sentOf('crew_handoff_get')).toHaveLength(3);
    expect(daemon.sentOf('crew_handoffs_get')).toHaveLength(1);

    await gesture(daemon, () => fireEvent.click(panel().getByTestId('crew-handoff-0')));
    expect(panel().getByRole('heading', { name: 'Latest letter' })).toBeInTheDocument();
    expect(daemon.sentOf('crew_handoff_get')).toHaveLength(4);
  });
});
