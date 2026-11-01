import { act, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { gesture, pressShortcut } from './test/renderApp';
import { HOLD, answerInTurn, type ScriptedDaemon } from './test/scriptedDaemon';
import { HOME, openPicker, pathInput, press, repoInfo, typePath } from './test/locations';
import type { EventMessage } from './test/protocol';
import { daemonSession, emptyDesktop, soloDesktop } from './test/daemonFixtures';

type OutpostInfo = EventMessage<'outposts_updated'>['outposts'][number];
type RecentLocation = EventMessage<'recent_locations_result'>['recent_locations'][number];

const REPO = `${HOME}/projects/shop`;
const NOTES = `${HOME}/notes`;
const SENSOR = `${HOME}/projects/sensor-hub`;
const AT = '2026-05-16T16:00:00Z';
const sandbox: OutpostInfo = {
  id: 'outpost-ai',
  name: 'ai-sandbox',
  destination: 'ai-sandbox:2200',
  state: 'connected',
  reach: 'reachable',
  state_since: AT,
};
const piLab: OutpostInfo = {
  id: 'outpost-pi',
  name: 'pi-lab',
  destination: 'pi-lab:2200',
  state: 'lost',
  reach: 'unreachable',
  state_since: AT,
};
const settings = {
  new_session_launcher_enabled: 'true',
  new_session_agent: 'claude',
  outposts_enabled: 'true',
};
const INFO = repoInfo(REPO, [{ path: `${REPO}--feature`, branch: 'feature' }]);

const recents: RecentLocation[] = [
  { host: sandbox.id, path: REPO, repo_root: REPO, last_seen: '2026-05-16T16:03:00Z', use_count: 1 },
  { path: REPO, repo_root: REPO, last_seen: '2026-05-16T16:02:00Z', use_count: 1 },
  { host: piLab.id, path: SENSOR, last_seen: '2026-05-16T16:01:00Z', use_count: 1 },
  { path: NOTES, last_seen: AT, use_count: 1 },
];

function serveOutposts(daemon: ScriptedDaemon, sandboxHarnesses = ['claude', 'codex']) {
  daemon.on('get_recent_locations', () => ({
    event: 'recent_locations_result',
    success: true,
    home_path: HOME,
    recent_locations: recents,
  }));
  daemon.on('agent_catalog', ({ host }) => ({
    event: 'agent_catalog_result',
    success: true,
    host,
    catalog: {
      machine: host,
      harnesses: ['claude', 'codex', 'pi'].map((id) => ({
        id,
        name: id,
        available: sandboxHarnesses.includes(id),
        model_pin: false,
        effort_pin: false,
        discovery: false,
      })),
    },
  }));
  daemon.on('get_repo_info', ({ repo }) => ({
    event: 'get_repo_info_result',
    success: true,
    info: { ...INFO, repo },
  }));
  daemon.on('create_worktree', ({ main_repo, branch }) => ({
    event: 'create_worktree_result',
    success: true,
    path: `${main_repo}--${branch}`,
  }));
  daemon.on('delete_worktree', ({ path }) => ({
    event: 'delete_worktree_result',
    success: true,
    path,
  }));
}

async function open(
  extra: Record<string, string> = {},
  outposts: OutpostInfo[] = [sandbox, piLab],
  sandboxHarnesses?: string[],
  initialState: Partial<EventMessage<'initial_state'>> = {},
) {
  const view = await openPicker(
    {},
    { settings: { ...settings, ...extra }, outposts, ...initialState },
  );
  await press(view.daemon, 'Escape');
  serveOutposts(view.daemon, sandboxHarnesses);
  const opened = view.daemon.sent.length;
  await gesture(view.daemon, () => pressShortcut('session.new'));
  return { ...view, sinceOpening: () => view.daemon.sent.slice(opened) };
}

function machines() {
  return screen.getAllByRole('option').map((row) => ({
    name: row.querySelector('.picker-name')?.textContent,
    machine: within(row).queryByTestId('launcher-machine')?.textContent ?? null,
  }));
}
function spawns(daemon: ScriptedDaemon) {
  return daemon
    .sentOf('spawn_session')
    .map(({ cwd, agent, host }) => ({ cwd, agent, host }));
}
async function highlight(daemon: ScriptedDaemon, index: number) {
  for (let step = 0; step < index; step++) await press(daemon, 'ArrowDown');
}

describe('launcher with outposts attached', () => {
  it('lists every machine with its badge and reads harnesses only from reachable outposts', async () => {
    const { daemon, sinceOpening } = await open();
    expect(machines()).toEqual([
      { name: 'shop', machine: 'ai-sandbox' },
      { name: 'shop', machine: 'This Mac' },
      { name: 'sensor-hub', machine: 'pi-lab' },
      { name: 'notes', machine: 'This Mac' },
    ]);
    const opening = sinceOpening();
    expect(opening.filter(({ cmd }) => cmd === 'get_recent_locations')).toEqual([
      { cmd: 'get_recent_locations', request_id: expect.any(String) },
    ]);
    expect(
      opening.flatMap((command) => (command.cmd === 'agent_catalog' ? [command.host] : [])),
    ).toEqual([sandbox.id]);
    expect(screen.getAllByRole('option')[2]).toHaveAttribute('data-reach', 'unreachable');
    await typePath(daemon, 'ai-s');
    expect(machines()).toEqual([{ name: 'shop', machine: 'ai-sandbox' }]);
  });

  it.each([
    ['main_repo', REPO],
    ['new_worktree', undefined],
  ])('Enter starts a remote recent on its machine with its %s memory', async (memory, cwd) => {
    const { daemon } = await open({
      [`new_session_destination_${sandbox.id}_${REPO}`]: memory,
    });
    await press(daemon, 'Enter');
    const creates = daemon.sentOf('create_worktree');
    if (memory === 'new_worktree') {
      expect(daemon.sentOf('get_repo_info')).toEqual([
        expect.objectContaining({ repo: REPO, host: sandbox.id }),
      ]);
      expect(creates).toEqual([expect.objectContaining({ main_repo: REPO, host: sandbox.id })]);
    } else expect(creates).toEqual([]);
    expect(spawns(daemon)).toEqual([
      { cwd: cwd ?? `${REPO}--${creates[0].branch}`, agent: 'claude', host: sandbox.id },
    ]);
    expect(screen.queryByTestId('location-picker')).toBeNull();
  });

  it('runs the where list, CmdEnter and CmdBackspace on the row machine and remembers per machine', async () => {
    const { daemon } = await open();
    await press(daemon, 'Tab');
    expect(daemon.sentOf('get_repo_info')).toEqual([
      expect.objectContaining({ repo: REPO, host: sandbox.id }),
    ]);
    await typePath(daemon, 'feature');
    await press(daemon, 'Backspace', { metaKey: true });
    await press(daemon, 'y');
    expect(daemon.sentOf('delete_worktree')).toEqual([
      expect.objectContaining({ path: `${REPO}--feature`, host: sandbox.id }),
    ]);
    await typePath(daemon, 'fix-login');
    await press(daemon, 'Enter');
    expect(daemon.sentOf('create_worktree')).toEqual([
      expect.objectContaining({ main_repo: REPO, branch: 'fix-login', host: sandbox.id }),
    ]);
    expect(spawns(daemon)).toEqual([
      { cwd: `${REPO}--fix-login`, agent: 'claude', host: sandbox.id },
    ]);
    expect(
      daemon
        .sentOf('set_setting')
        .filter(({ key }) => key.startsWith('new_session_destination'))
        .map(({ key, value }) => ({ key, value })),
    ).toEqual([{ key: `new_session_destination_${sandbox.id}_${REPO}`, value: 'new_worktree' }]);

    await gesture(daemon, () => pressShortcut('session.new'));
    await press(daemon, 'Enter', { metaKey: true });
    expect(spawns(daemon)[1]).toEqual({ cwd: REPO, agent: 'shell', host: sandbox.id });
  });

  it.each([
    [piLab, 'Lost for 5m.'],
    [
      { ...piLab, state: 'refused' as const, last_error: 'pi-lab runs attn 0.41.2 and this Mac runs 0.43.0. Update it in Settings → Outposts.' },
      'pi-lab runs attn 0.41.2 and this Mac runs 0.43.0. Update it in Settings → Outposts.',
    ],
  ])('refuses a start on an unreachable machine without a request ($state)', async (row, reason) => {
    const lostAt = new Date(Date.now() - 5 * 60_000).toISOString();
    const { daemon } = await open({}, [sandbox, { ...row, state_since: lostAt }]);
    await typePath(daemon, 'sensor');
    for (const init of [{}, { metaKey: true }]) {
      const before = daemon.sent.length;
      await press(daemon, 'Enter', init);
      expect(screen.getByRole('alert')).toHaveTextContent(
        `pi-lab is unreachable; this needs it now${reason}`,
      );
      expect(daemon.sent.slice(before)).toEqual([]);
    }
    expect(pathInput()).toHaveValue('sensor');
    expect(screen.getByTestId('location-picker')).toBeInTheDocument();
  });

  it('strikes through a harness the target machine lacks and refuses it', async () => {
    const { daemon } = await open({}, [sandbox, piLab], ['codex']);
    const claude = screen.getByRole('radio', { name: /Claude/ });
    expect(claude).toHaveClass('agent-option--missing');
    await press(daemon, 'Enter');
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Claude is not installed on ai-sandbox. Pick another harness (⌥1–⌥9) or machine.',
    );
    expect(spawns(daemon)).toEqual([]);
    await highlight(daemon, 1);
    expect(claude).not.toHaveClass('agent-option--missing');
    await press(daemon, 'ArrowUp');
    await press(daemon, '2', { altKey: true, code: 'Digit2' });
    await press(daemon, 'Enter');
    expect(spawns(daemon)).toEqual([{ cwd: REPO, agent: 'codex', host: sandbox.id }]);
  });

  it('lets a harness installed only on the outpost be picked there and refuses it on This Mac', async () => {
    const { daemon } = await open({ pi_available: 'false' }, [sandbox, piLab], ['claude', 'pi']);
    const pi = screen.getByRole('radio', { name: /Pi/ });
    expect(pi).toBeEnabled();
    expect(pi).not.toHaveClass('agent-option--missing');
    await press(daemon, '3', { altKey: true, code: 'Digit3' });
    expect(pi).toHaveAttribute('aria-checked', 'true');
    await highlight(daemon, 1);
    expect(pi).toHaveClass('agent-option--missing');
    expect(pi).toHaveAttribute('aria-checked', 'true');
    await press(daemon, 'Enter');
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Pi is not installed on This Mac. Pick another harness (⌥1–⌥9) or machine.',
    );
    expect(spawns(daemon)).toEqual([]);
    await press(daemon, 'ArrowUp');
    await press(daemon, 'Enter');
    expect(spawns(daemon)).toEqual([{ cwd: REPO, agent: 'pi', host: sandbox.id }]);
  });

  it('waits on a stalled machine and lets Esc finish the start in the background', async () => {
    const { daemon } = await open();
    answerInTurn(daemon, 'spawn_session', [HOLD]);
    await press(daemon, 'Enter');
    const stalledAt = new Date(Date.now()).toISOString();
    await gesture(daemon, () =>
      daemon.emit({
        event: 'outposts_updated',
        outposts: [{ ...sandbox, state: 'stalled', state_since: stalledAt }, piLab],
      }),
    );
    await act(() => vi.advanceTimersByTimeAsync(4_000));
    expect(screen.getByRole('status')).toHaveTextContent(
      'Starting on ai-sandbox… no answer for 4s',
    );
    expect(screen.getByText('keep waiting in the background')).toBeInTheDocument();
    await press(daemon, 'Escape');
    expect(screen.queryByTestId('location-picker')).toBeNull();
    const [spawn] = daemon.sentOf('spawn_session');
    expect(spawn).toMatchObject({ host: sandbox.id, cwd: REPO });
    await gesture(daemon, () =>
      daemon.replyTo(spawn, {
        event: 'spawn_result',
        id: spawn.id,
        success: false,
        error: 'ai-sandbox is unreachable; this needs it now',
      }),
    );
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Starting on ai-sandbox failed: ai-sandbox is unreachable; this needs it now',
    );
  });

  it('places a backgrounded start on the desktop it was picked on after the user moves', async () => {
    const { daemon } = await open(
      { [`new_session_destination_${sandbox.id}_${REPO}`]: 'new_worktree' },
      [sandbox, piLab],
      undefined,
      {
        sessions: [daemonSession('s1', { state: 'idle', directory: '/tmp/s1' })],
        desktops: [
          soloDesktop('s1', { shortcut_slot: 1 }),
          emptyDesktop('other', { shortcut_slot: 2 }),
        ],
      },
    );
    const pickedOn = daemon.arrangement.profile.current_desktop_id;
    answerInTurn(daemon, 'get_repo_info', [HOLD]);
    await press(daemon, 'Enter');
    await gesture(daemon, () =>
      daemon.emit({
        event: 'outposts_updated',
        outposts: [{ ...sandbox, state: 'stalled', state_since: new Date().toISOString() }, piLab],
      }),
    );
    await press(daemon, 'Escape');
    await gesture(daemon, () => pressShortcut('desktop.select2'));
    expect(daemon.arrangement.profile.current_desktop_id).not.toBe(pickedOn);
    const [read] = daemon.sentOf('get_repo_info');
    await gesture(daemon, () =>
      daemon.replyTo(read, { event: 'get_repo_info_result', success: true, info: INFO }),
    );
    expect(
      daemon.sentOf('spawn_session').map(({ host, placement }) => [host, placement?.desktop_id]),
    ).toEqual([[sandbox.id, pickedOn]]);
  });
});

describe('launcher without outposts', () => {
  it.each([
    ['no outpost attached', [], 'true'],
    ['the Outposts switch off', [sandbox], 'false'],
  ])('sends only the requests it sent before machines existed and renders no badge with %s', async (_, outposts, enabled) => {
    const { daemon } = await openPicker(
      {
        recent: [REPO, NOTES],
        repos: [INFO],
        directories: { [`${HOME}/projects`]: ['shop', 'other'] },
      },
      {
        settings: {
          new_session_launcher_enabled: 'true',
          new_session_agent: 'claude',
          outposts_enabled: enabled,
        },
        outposts,
      },
    );
    expect(screen.queryByTestId('launcher-machine')).toBeNull();
    await typePath(daemon, 'note');
    await typePath(daemon, '~/projects/');
    await typePath(daemon, '');
    await press(daemon, 'Tab');
    await typePath(daemon, 'fix');
    await press(daemon, 'Enter');
    expect(spawns(daemon)).toEqual([{ cwd: `${REPO}--fix`, agent: 'claude', host: undefined }]);
    expect(daemon.sentOf('agent_catalog')).toEqual([]);
    expect(daemon.sent.filter((command) => 'host' in command)).toEqual([]);
    expect(daemon.sentOf('get_recent_locations')).toEqual([
      { cmd: 'get_recent_locations', home_only: true, request_id: expect.any(String) },
    ]);
    expect(daemon.sentOf('browse_directory').map(({ input_path }) => input_path)).toEqual([
      '~/projects/',
    ]);
  });
});
