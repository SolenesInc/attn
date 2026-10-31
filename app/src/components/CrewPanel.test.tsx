import { openRoute, previewHarness, pickModel, pickEffort, enterModel, routeDialog } from '../test/harnessRoute';
import { act, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { type CrewMember } from '../types/generated';
import {
  agentPane,
  soloDesktop,
  daemonSeed,
  daemonSession,
  daemonDesktop,
  memberActor,
  memberClaim,
  sessionClaim,
  type DaemonSeed,
  type DaemonSession,
  DEFAULT_DESKTOP_ID,
  DEFAULT_PROFILE_ID,
} from '../test/daemonFixtures';
import { gesture, renderApp } from '../test/renderApp';
import type { CommandMessage } from '../test/protocol';
import { type Answer, answerInTurn, HOLD, type Reply, type ScriptedDaemon } from '../test/scriptedDaemon';

function member(id: string, revision: number, values: Partial<CrewMember> = {}): CrewMember {
  return {
    id,
    name: id.charAt(0).toUpperCase() + id.slice(1),
    revision,
    charter_path: `/crew/${id}/CHARTER.md`,
    home_dir: `/crew/${id}`,
    awareness_dirs: [],
    profile_id: DEFAULT_PROFILE_ID,
    retired: false,
    resolved_agent: 'claude',
    ...values,
  };
}


const saved = (values: Partial<Extract<Reply, { event: 'crew_set_result' }>> = {}): Reply => ({ event: 'crew_set_result', success: true, conflict: false, ...values });
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

const models = (list: Extract<Reply, { event: 'harness_models_result' }>['models']): Reply => ({ event: 'harness_models_result', tier_defaults: {}, success: true, detail: '', models: list });

const astra = models([
  { harness: 'codex', provider: 'openai', id: 'gpt-6-astra', name: 'Astra', description: '', detail: '', access: 'supported', tier_source: 'none', effort_support: 'supported', effort_levels: ['medium', 'high'] },
  { harness: 'codex', provider: 'openai', id: 'retired', name: 'Retired', description: '', detail: '', access: 'unsupported', tier_source: 'none', effort_support: 'unknown', effort_levels: [] },
]);

type CrewCommand = 'crew_set' | 'crew_charter_get' | 'crew_charter_set' | 'crew_handoffs_get' | 'crew_handoff_get' | 'delegation_preferences_get' | 'harness_models';
type Script = Partial<Record<CrewCommand, Answer[]>>;

const defaults: Record<CrewCommand, Answer[]> = {
  crew_set: [saved()],
  crew_charter_get: [charter('# Trellis\n', 'charter-1')],
  crew_charter_set: [charterSaved('# Trellis\n', 'charter-2')],
  crew_handoffs_get: [handoffs('trellis', [])],
  crew_handoff_get: [handoffRefused('no handoff body in this fixture')],
  delegation_preferences_get: [preferences()],
  harness_models: [astra],
};

const panel = () => within(screen.getByTestId('crew-panel'));
const isPanelOpen = () => screen.getByTestId('crew-panel').hasAttribute('open');

async function renderPanel({
  script = {},
  members = [member('trellis', 4)],
  sessions = [],
  isOpen = true,
  seeds = [],
  settings = {},
}: {
  script?: Script;
  members?: CrewMember[];
  sessions?: DaemonSession[];
  isOpen?: boolean;
  seeds?: DaemonSeed[];
  settings?: Record<string, string>;
} = {}) {
  const everySession = [daemonSession('s1'), ...sessions];
  const { daemon } = await renderApp({
    initialState: { crew: members, seeds, sessions: everySession, desktops: everySession.map((session) => soloDesktop(session.id)), settings },
  });
  for (const [cmd, answers] of Object.entries({ ...defaults, ...script })) {
    answerInTurn(daemon, cmd as CrewCommand, answers, { repeatLast: true });
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

  it('keeps member, tab, seed filter and search when a desktop seed returns to Crew', async () => {
    const planted = daemonSeed('s-g9yxwv', { title: 'Artifact presence comes from the daemon', status: 'planted', planter: memberActor('keel') });
    const members = [member('alder', 2), member('keel', 3, { binding_session: 'session-keel' })];
    const { daemon } = await renderPanel({ members, seeds: [planted], sessions: [daemonSession('session-keel')] });
    daemon.on('open_seed', ({ seed_id }) => ({
      event: 'open_seed_result', success: true, seed_id, desktop_id: DEFAULT_DESKTOP_ID, tile_id: 'tile-seed',
    }));

    expect(panel().getByRole('button', { name: 'Crew launch model' })).toBeEnabled();
    fireEvent.click(panel().getByRole('button', { name: /Keel/ }));
    fireEvent.click(panel().getByRole('button', { name: 'Seeds' }));
    fireEvent.click(panel().getByRole('button', { name: /Planted/ }));
    fireEvent.change(panel().getByLabelText('Find a seed'), { target: { value: 'Artifact presence' } });
    await click(daemon, /Artifact presence comes from the daemon/);
    expect(openedSeeds(daemon)).toEqual([{ seed_id: planted.id, session_id: 'session-keel', standalone: undefined }]);

    daemon.arrange((desktops) => desktops.map((desktop) => (desktop.id === DEFAULT_DESKTOP_ID ? daemonDesktop(DEFAULT_DESKTOP_ID, {
      root: {
        type: 'split', split_id: 'split-seed', direction: 'vertical',
        children: [
          { type: 'pane', pane_id: 'pane-session-keel' },
          { type: 'tile', tile_id: 'tile-seed', tile_kind: 'seed', tile_params: planted.id },
        ],
      },
      panes: [agentPane('session-keel', DEFAULT_DESKTOP_ID)],
    }, { revision: desktop.revision + 1 }) : desktop)));
    await daemon.idle();
    expect(isPanelOpen()).toBe(false);
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('crew-seed-back')));

    expect(isPanelOpen()).toBe(true);
    expect(panel().getByRole('heading', { name: 'Keel' })).toBeInTheDocument();
    expect(panel().getByRole('button', { name: 'Seeds' })).toHaveAttribute('aria-current', 'page');
    expect(panel().getByRole('button', { name: /^Planted/ })).toHaveAttribute('aria-pressed', 'true');
    expect(panel().getByLabelText('Find a seed')).toHaveValue('Artifact presence');
  });

it('keeps actual running values separate from acknowledged next-wake settings', async () => {
    const { daemon } = await renderPanel({ members: [member('trellis', 4, { binding_session: 'session-trellis', agent: 'codex', model: 'gpt-6-astra', effort: 'high', resolved_agent: 'codex', resolved_model: 'gpt-6-astra', resolved_effort: 'high' })], sessions: [daemonSession('session-trellis', { agent: 'claude' })] });
    expect(panel().getByLabelText('Running now')).toHaveTextContent(/Claude.*Model not reported.*Effort not reported/);
    expect(panel().getByText('Acknowledged next wake').parentElement).toHaveTextContent(/Codex.*gpt-6-astra.*high/);
    await openRoute(daemon, 'Crew launch model');
    expect(routeDialog().getByRole('option', { name: /^Astra/ })).toBeInTheDocument();
    expect(daemon.sentOf('harness_models').map(command => command.harness)).toEqual(['codex']);
    expect(daemon.sentOf('delegation_preferences_get')).toHaveLength(1);
  });

  it('keeps the acknowledged new desktop when another launch field changes during its save', async () => {
    const { daemon, answer } = await renderPanel({ members: [member('keel', 5, { resolved_agent: 'codex' })], script: { crew_set: [HOLD] } });
    await gesture(daemon, () => fireEvent.change(panel().getByLabelText('Desktop'), { target: { value: '__new' } }));
    const dialog = screen.getByRole('dialog', { name: 'A new desktop' });
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Name' }), { target: { value: 'Review' } });
    await gesture(daemon, () => fireEvent.click(within(dialog).getByRole('button', { name: 'Use this name' })));
    await openRoute(daemon, 'Crew launch model');
    await pickModel(daemon, 'Astra');
    const launch_desktop = { desktop_id: 'desktop-review', label: 'Review (no ⌘ number)' };
    await answer(daemon.sentOf('crew_set')[0], saved({ member: member('keel', 6, { resolved_agent: 'codex', launch_desktop }) }));
    expect(daemon.sentOf('crew_set')).toHaveLength(2);
    expect(daemon.sentOf('crew_set')[1].launch_desktop_setting).toEqual(launch_desktop);
    expect(daemon.sentOf('crew_set')[1].model).toBe('openai/gpt-6-astra');
  });

it('saves a full atomic selection, leaves Sleep available while saving, and clears to defaults', async () => {
    const { daemon, answer } = await renderPanel({ script: { crew_set: [HOLD] }, members: [member('alder', 7, { binding_session: 'session-alder', agent: 'codex', model: 'gpt-6-astra', effort: 'high', resolved_agent: 'codex', resolved_model: 'gpt-6-astra', resolved_effort: 'high' })] });
    await openRoute(daemon, 'Crew launch model');
    await gesture(daemon, () => fireEvent.click(routeDialog().getByRole('option', { name: 'Crew default' })));
    expect(crewSets(daemon)).toEqual([{ member: 'member:alder', expected_revision: 7, agent: '', model: '', effort: '' }]);
    expect(panel().getByRole('button', { name: 'Sleep' })).toBeEnabled();
    await answer(daemon.sentOf('crew_set')[0], saved({ member: member('alder', 8, { binding_session: 'session-alder', resolved_agent: 'claude' }) }));
    expect(panel().getByRole('button', { name: 'Sleep' })).toBeEnabled();
    expect(panel().getByRole('button', { name: 'Crew launch model' })).toHaveTextContent('Crew default');
  });

it('keeps a failed member edit through roster navigation and retries it', async () => {
    const { daemon, answer } = await renderPanel({ script: { crew_set: [saved({ success: false, error: 'model discovery is unavailable' }), HOLD] }, members: [member('alder', 2), member('keel', 3)] });
    await openRoute(daemon, 'Crew launch model');
    await previewHarness(daemon, 'Codex');
    await pickModel(daemon, 'Codex default');
    expect(panel().getByText('Not saved')).toBeInTheDocument();
    expect(panel().getByText('model discovery is unavailable')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.mouseDown(document.body));
    await click(daemon, /Keel/); await click(daemon, /Alder/);
    expect(panel().getByRole('button', { name: 'Crew launch model' })).toHaveTextContent('Codex');
    await click(daemon, 'Retry');
    expect(daemon.sentOf('crew_set')).toHaveLength(2);
    await answer(daemon.sentOf('crew_set')[1], saved({ member: member('alder', 3, { agent: 'codex', resolved_agent: 'codex' }) }));
    expect(panel().getByText('Saved')).toBeInTheDocument();
  });

  it('renames a member from its heading with crew v2 on, keeping its key', async () => {
    const { daemon, pushMembers } = await renderPanel({ members: [member('keel', 4)], settings: { crew_v2_enabled: 'true' } });
    daemon.on('crew_rename', () => ({ event: 'crew_rename_result', success: false, error: 'the name "Trellis" is already taken by Trellis in this profile' }));
    await click(daemon, 'Rename');
    const input = panel().getByTestId('crew-rename-input');
    await gesture(daemon, () => fireEvent.change(input, { target: { value: 'Trellis' } }));
    await gesture(daemon, () => fireEvent.submit(input));
    expect(panel().getByRole('alert')).toHaveTextContent('already taken');
    daemon.on('crew_rename', () => ({ event: 'crew_rename_result', success: true, result: { member: 'keel', name: 'Alfred', previous_name: 'Keel' } }));
    await gesture(daemon, () => fireEvent.change(input, { target: { value: 'Alfred' } }));
    await gesture(daemon, () => fireEvent.submit(input));
    await pushMembers([member('keel', 4, { name: 'Alfred' })]);
    expect(daemon.sentOf('crew_rename').map(({ member: ref, name }) => [ref, name])).toEqual([['member:keel', 'Trellis'], ['member:keel', 'Alfred']]);
    expect(panel().getByRole('heading', { level: 2 })).toHaveTextContent('Alfred');
    expect(panel().queryByTestId('crew-rename-input')).toBeNull();
  });

  it('cancels a rename on Escape and keeps Crew open', async () => {
    const { daemon, closeWithEscape } = await renderPanel({ members: [member('keel', 4)], settings: { crew_v2_enabled: 'true' } });
    await click(daemon, 'Rename');
    await closeWithEscape();
    expect(panel().queryByTestId('crew-rename-input')).toBeNull();
    expect(panel().getByRole('heading', { level: 2 })).toHaveTextContent('Keel');
    expect(daemon.sentOf('crew_rename')).toEqual([]);
  });

  it('offers no rename while crew v2 is off', async () => {
    await renderPanel({ members: [member('keel', 4)] });
    expect(panel().queryByTestId('crew-rename')).toBeNull();
  });

  it('asks an awake member to sleep through the ordinary crew action', async () => {
    const { daemon } = await renderPanel({ members: [member('trellis', 4, { binding_session: 'session-trellis' })] });
    daemon.on('crew_sleep', () => ({
      event: 'crew_sleep_result', success: true, member: 'trellis', session_id: 'session-trellis',
      delivery_status: 'queued', detail: 'The request is queued',
    }));

    await click(daemon, 'Sleep');
    expect(daemon.sentOf('crew_sleep')).toEqual([expect.objectContaining({ member: 'member:trellis' })]);
    expect(daemon.sentOf('crew_wake')).toEqual([]);
    expect(screen.getByTestId('crew-panel').querySelector('.crew-member-heading')).toHaveTextContent('Awake');
  });

  it('applies an awake member’s saved settings and shows what happened', async () => {
    const { daemon } = await renderPanel({ members: [member('trellis', 4, { binding_session: 'session-trellis' })] });
    daemon.on('crew_apply', () => ({
      event: 'crew_apply_result', success: true,
      result: { member: 'trellis', name: 'Trellis', outcome: 'letter_requested', session_id: 'session-trellis', delivery_status: 'notified', detail: 'Asked Trellis in session session- to file its letter; its next session starts on codex from it' },
    }));

    await click(daemon, 'Apply');
    expect(daemon.sentOf('crew_apply')).toEqual([expect.objectContaining({ member: 'member:trellis' })]);
    expect(panel().getByText(/its next session starts on codex from it/)).toBeInTheDocument();
    expect(daemon.sentOf('crew_sleep')).toEqual([]);
  });

  it('offers no apply while the member is asleep', async () => {
    await renderPanel({ members: [member('trellis', 4)] });
    expect(panel().queryByTestId('crew-apply')).toBeNull();
  });

  it('closes with Escape and an app wake shows the sleeping member’s day', async () => {
    const { daemon, openCrew, closeWithEscape } = await renderPanel({
      members: [member('keel', 5)],
      sessions: [daemonSession('session-keel')],
    });
    daemon.on('crew_wake', () => ({
      event: 'crew_wake_result', success: true, member: 'keel', session_id: 'session-keel',
    }));

    await closeWithEscape();
    expect(isPanelOpen()).toBe(false);

    await openCrew();
    await click(daemon, 'Wake');
    expect(daemon.sentOf('crew_wake')).toEqual([expect.objectContaining({ member: 'member:keel' })]);
    expect(isPanelOpen()).toBe(false);
    expect(document.querySelector('.session-terminal-desktop[data-session-visible="1"]'))
      .toHaveAttribute('data-desktop-id', 'desktop-session-keel');
  });

  it('keeps roster navigation while async harness discovery settles', async () => {
    const { daemon, answer } = await renderPanel({
      script: { delegation_preferences_get: [HOLD] },
      members: [member('alder', 2), member('keel', 3)],
    });

    fireEvent.click(panel().getByRole('button', { name: /Keel/ }));
    expect(panel().getByRole('heading', { name: 'Keel' })).toBeInTheDocument();

    await answer(daemon.sentOf('delegation_preferences_get')[0], preferences([harnesses[0]]));
    expect(panel().getByRole('button', { name: 'Crew launch model' })).toBeEnabled();
    expect(panel().getByRole('heading', { name: 'Keel' })).toBeInTheDocument();
    expect(daemon.sentOf('delegation_preferences_get')).toHaveLength(1);
  });

it('allows model and effort pins while the harness follows the crew default', async () => {
    const { daemon } = await renderPanel({ script: { crew_set: [saved({ member: member('keel', 6, { model: 'openai/gpt-6-astra', resolved_agent: 'codex', resolved_model: 'openai/gpt-6-astra' }) }), saved({ member: member('keel', 7, { model: 'openai/gpt-6-astra', effort: 'high', resolved_agent: 'codex', resolved_model: 'openai/gpt-6-astra', resolved_effort: 'high' }) })] }, members: [member('keel', 5, { resolved_agent: 'codex' })] });
    await openRoute(daemon, 'Crew launch model');
    await pickModel(daemon, 'Astra');
    await pickEffort(daemon, 'high');
    expect(crewSets(daemon)).toEqual([{ member: 'member:keel', expected_revision: 5, agent: '', model: 'openai/gpt-6-astra', effort: '' }, { member: 'member:keel', expected_revision: 6, agent: '', model: 'openai/gpt-6-astra', effort: 'high' }]);
    expect(panel().getByRole('button', { name: 'Crew launch model' })).toHaveTextContent('Astra');
  });

it('commits a typed model id on Enter as one write and keeps the draft over roster pushes', async () => {
    const { daemon, pushMembers } = await renderPanel({ members: [member('keel', 6)] });
    await openRoute(daemon, 'Crew launch model');
    const field = routeDialog().getByRole('textbox', { name: 'Filter models or enter an ID' });
    fireEvent.change(field, { target: { value: 'gpt-7' } });
    await pushMembers([member('keel', 7)]);
    expect(field).toHaveValue('gpt-7');
    expect(daemon.sentOf('crew_set')).toEqual([]);
    await gesture(daemon, () => fireEvent.keyDown(field, { key: 'Enter' }));
    expect(crewSets(daemon)).toEqual([{ member: 'member:keel', expected_revision: 7, agent: '', model: 'gpt-7', effort: '' }]);
  });

it('keeps dependent controls pending while an explicit harness pin clears', async () => {
    const { daemon, answer } = await renderPanel({ script: { crew_set: [HOLD] }, members: [member('keel', 5, { agent: 'codex', resolved_agent: 'codex', model: 'openai/gpt-6-astra', resolved_model: 'openai/gpt-6-astra' })] });
    await openRoute(daemon, 'Crew launch model');
    await gesture(daemon, () => fireEvent.click(routeDialog().getByRole('option', { name: 'Crew default' })));
    await openRoute(daemon, 'Crew launch model');
    expect(routeDialog().queryByRole('listbox', { name: 'Model' })).toBeNull();
    await answer(daemon.sentOf('crew_set')[0], saved({ member: member('keel', 6, { resolved_agent: 'claude' }) }));
    expect(routeDialog().getByRole('listbox', { name: 'Model' })).toBeInTheDocument();
  });

  it('selects provider-qualified model identities when bare ids collide', async () => {
    const { daemon } = await renderPanel({
      script: {
        crew_set: [saved({ member: member('keel', 6, { agent: 'codex', model: 'second/shared', resolved_agent: 'codex', resolved_model: 'second/shared' }) })],
        harness_models: [models([
          { harness: 'codex', provider: 'first', id: 'shared', name: 'Shared one', description: '', detail: '', access: 'supported', tier_source: 'none', effort_support: 'supported', effort_levels: ['low'] },
          { harness: 'codex', provider: 'second', id: 'shared', name: 'Shared two', description: '', detail: '', access: 'supported', tier_source: 'none', effort_support: 'supported', effort_levels: ['high'] },
        ])],
      },
      members: [member('keel', 5, { agent: 'codex', resolved_agent: 'codex' })],
    });

    await openRoute(daemon, 'Crew launch model');
    await pickModel(daemon, 'Shared two');

    expect(crewSets(daemon)).toEqual([{ member: 'member:keel', expected_revision: 5, agent: 'codex', model: 'second/shared', effort: '' }]);
    expect(panel().getByRole('button', { name: 'Crew launch model' })).toHaveTextContent('Shared two');
  });

  it('keeps an unsupported models explicit effort clear through a concurrent update', async () => {
    const { daemon, answer } = await renderPanel({
      script: {
        crew_set: [
          saved({ success: false, conflict: true, error: 'revision conflict', member: member('keel', 6, { agent: 'codex', effort: 'high', resolved_agent: 'codex', resolved_effort: 'high' }) }),
          HOLD,
        ],
        harness_models: [models([
          { harness: 'codex', provider: 'local', id: 'fixed', name: 'Fixed', description: '', detail: '', access: 'supported', tier_source: 'none', effort_support: 'unsupported', effort_levels: [] },
        ])],
      },
      members: [member('keel', 5, { agent: 'codex', resolved_agent: 'codex' })],
    });

    await openRoute(daemon, 'Crew launch model');
    await pickModel(daemon, 'Fixed');
    expect(panel().getByText('Not saved')).toBeInTheDocument();
    await click(daemon, 'Retry');

    expect(crewSets(daemon)[1]).toEqual({ member: 'member:keel', expected_revision: 6, agent: 'codex', model: 'local/fixed', effort: '' });
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
      member: 'member:trellis', content: '# Trellis\n\nChanged while the idea is hot.\n', expected_token: 'charter-old',
    })]);
    expect(panel().getByRole('status')).toHaveTextContent('Saving');

    await answer(daemon.sentOf('crew_charter_set')[0], charterSaved('# Trellis\n\nChanged while the idea is hot.\n', 'charter-new'));
    expect(panel().getByText('No handoffs recorded.')).toBeInTheDocument();
    expect(daemon.sentOf('crew_handoffs_get').map((command) => command.member)).toEqual(['member:trellis']);
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

it('commits a launch field on the inner Escape, then closes Crew on the next Escape', async () => {
    const { daemon, closeWithEscape } = await renderPanel({ members: [member('keel', 6)] });
    await openRoute(daemon, 'Crew launch model');
    await enterModel(daemon, 'unlisted');
    const before = daemon.sentOf('crew_set').length;
    const effort = routeDialog().getByRole('textbox', { name: 'Effort' });
    effort.focus(); fireEvent.change(effort, { target: { value: 'high' } });
    await closeWithEscape();
    expect(isPanelOpen()).toBe(true);
    expect(daemon.sentOf('crew_set')).toHaveLength(before + 1);
    expect(crewSets(daemon).slice(-1)[0]).toMatchObject({ member: 'member:keel', effort: 'high' });
    await closeWithEscape();
    expect(isPanelOpen()).toBe(false);
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
    expect(daemon.sentOf('crew_handoff_get').map(({ member: id, filename }) => [id, filename])).toEqual([['member:trellis', '2026-09-01T21-37Z-trellis.md']]);
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
    expect(daemon.sentOf('crew_handoff_get').map(({ member: id, filename }) => [id, filename])).toEqual([['member:trellis', '2026-09-02T09-00Z-trellis.md']]);
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

describe('CrewPanel seeds', () => {
  async function showSeeds(members: CrewMember[], seeds: DaemonSeed[], name: RegExp, filter: RegExp) {
    const view = await renderPanel({ members, seeds, sessions: [daemonSession('day-current')] });
    fireEvent.click(panel().getByRole('button', { name }));
    fireEvent.click(panel().getByRole('button', { name: 'Seeds' }));
    fireEvent.click(panel().getByRole('button', { name: filter }));
    return view;
  }

  const pushSeeds = async (daemon: ScriptedDaemon, seeds: DaemonSeed[], total = seeds.length) => {
    daemon.emit({ event: 'garden_seeds_updated', profile_id: 'profile-default', seeds, total });
    await daemon.idle();
  };

  it('lists a member’s planted seeds with their state and plot, and opens one from the keyboard', async () => {
    const plot = daemonSeed('s-dssvxq', {
      title: 'Garden follow-up work',
      plot_progress: { total: 5, done: 2, withered: 0, growing: 1, dormant: 1, ready: 1, blocked: 0 },
    });
    const child = daemonSeed('s-g9yxwv', {
      title: 'Artifact presence comes from the daemon',
      status: 'planted',
      planter: memberActor('trellis'),
      edges: [{ kind: 'part-of', to: plot.id }],
    });
    const { daemon } = await showSeeds([member('trellis', 4)], [child, plot], /Trellis/, /Planted/);

    const row = panel().getByRole('button', { name: /Artifact presence comes from the daemon/ });
    expect(row).toHaveAttribute('data-seed-state', 'planted');
    expect(row).toHaveTextContent('Garden follow-up work · 2/5');
    await gesture(daemon, () => fireEvent.keyDown(row, { key: 'Enter' }));

    expect(openedSeeds(daemon)).toEqual([expect.objectContaining({ seed_id: child.id })]);
  });

  it('follows what a member tends as Garden broadcasts replace the seeds, its day’s claims and its own alike', async () => {
    const claimed = daemonSeed('s-live', { title: 'Live claim', ...sessionClaim('day-current') });
    const ownClaim = daemonSeed('s-member', { title: 'Member claim', ...memberClaim('alder', 'day-current') });
    const alder = member('alder', 1, { binding_session: 'day-current' });
    const { daemon } = await showSeeds([alder], [claimed, ownClaim], /Alder/, /Tending/);

    expect(panel().getByRole('button', { name: /Live claim/ })).toBeInTheDocument();
    expect(panel().getByRole('button', { name: /Member claim/ })).toBeInTheDocument();

    await pushSeeds(daemon, [{ ...claimed, status: 'dormant', tender: undefined, claimed: false }, { ...ownClaim, status: 'harvested', tender: undefined, claimed: false }]);
    expect(panel().getByText("Alder isn't tending a seed.")).toBeInTheDocument();

    fireEvent.click(panel().getByRole('button', { name: /Planted/ }));
    await pushSeeds(daemon, [{ ...claimed, status: 'planted', planter: memberActor('alder') }]);
    expect(panel().getByRole('button', { name: /Live claim/ })).toBeInTheDocument();
  });

  it('says plainly when a member planted nothing, and when the list is only the newest part of the Garden', async () => {
    const { daemon } = await showSeeds([member('keel', 1)], [], /Keel/, /Planted/);
    expect(panel().getByText('No seeds were explicitly planted by Keel.')).toBeInTheDocument();

    await pushSeeds(daemon, ['a', 'b', 'c', 'd'].map((id) => daemonSeed(`s-${id}`, { planter: memberActor('alder') })), 481);

    expect(panel().getByText('Showing matches in the newest 4 of 481 seeds.')).toBeInTheDocument();
    expect(panel().getByText('No seeds in this Garden snapshot are explicitly planted by Keel.')).toBeInTheDocument();
    expect(panel().getByRole('button', { name: /Planted/ })).toHaveTextContent('Planted 0+');
  });
});
