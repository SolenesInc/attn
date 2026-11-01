import { act, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { gesture, pressShortcut } from './test/renderApp';
import { HOLD, answerInTurn, type ScriptedDaemon } from './test/scriptedDaemon';
import { HOME, inspection, openPicker, pathInput, press, repoInfo, typePath } from './test/locations';
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
    expect(machines()).toEqual([
      { name: 'ai-sandbox:', machine: null },
      { name: 'shop', machine: 'ai-sandbox' },
    ]);
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

type Disk = Record<string, string[]>;
const SCRATCH = `${HOME}/scratch`;

function absolutePath(path: string) {
  const expanded = path === '~' || path.startsWith('~/') ? `${HOME}${path.slice(1)}` : path;
  return expanded.length > 1 ? expanded.replace(/\/+$/, '') : expanded;
}

function serveDisks(daemon: ScriptedDaemon, disks: Record<string, Disk>) {
  const diskOf = (host?: string) => disks[host ?? ''] ?? {};
  daemon.on('browse_directory', ({ input_path, host }) => {
    const directory = absolutePath(input_path);
    const names = diskOf(host)[directory];
    if (!names)
      return {
        event: 'browse_directory_result',
        success: false,
        input_path,
        directory: '',
        entries: [],
        home_path: HOME,
        error: `open ${directory}: no such file or directory`,
      };
    return {
      event: 'browse_directory_result',
      success: true,
      input_path,
      home_path: HOME,
      directory,
      entries: names.map((name) => ({
        name,
        path: `${directory}/${name}`,
        is_dir: true,
        is_repo: `${directory}/${name}` === REPO,
      })),
    };
  });
  daemon.on('inspect_path', ({ path, host }) => {
    const target = absolutePath(path);
    const disk = diskOf(host);
    const exists =
      target in disk ||
      Object.entries(disk).some(([dir, names]) => names.some((name) => `${dir}/${name}` === target));
    return inspection(path, { exists, repoRoot: exists && target === REPO ? REPO : undefined });
  });
}

const sandboxDisk: Disk = {
  [HOME]: ['projects', 'scratch'],
  [`${HOME}/projects`]: ['evals', 'shop'],
  [SCRATCH]: [],
};
const macDisk: Disk = {
  [HOME]: ['notes', 'projects'],
  [`${HOME}/projects`]: ['shop'],
  [NOTES]: [],
};

function rowNames() {
  return screen.getAllByRole('option').map((row) => ({
    name: row.querySelector('.picker-name')?.textContent,
    machine: within(row).queryByTestId('launcher-machine')?.textContent ?? null,
    selected: row.getAttribute('aria-selected') === 'true',
  }));
}
function browses(daemon: ScriptedDaemon) {
  return daemon.sentOf('browse_directory').map(({ input_path, host }) => ({ input_path, host }));
}
function status() {
  return screen.queryByTestId('launcher-status')?.textContent ?? null;
}

describe('launcher addresses', () => {
  async function openWithDisks(
    disks: Record<string, Disk> = { '': macDisk, [sandbox.id]: sandboxDisk },
    outposts: OutpostInfo[] = [sandbox, piLab],
  ) {
    const view = await openPicker({}, { settings, outposts });
    await press(view.daemon, 'Escape');
    serveOutposts(view.daemon);
    serveDisks(view.daemon, disks);
    return view;
  }
  async function reopen(daemon: ScriptedDaemon, shortcut: 'session.new' | 'session.newTerminal' = 'session.new') {
    await gesture(daemon, () => pressShortcut(shortcut));
  }

  it('suggests a machine for its name and Tab browses it from the opening prefetch', async () => {
    const { daemon } = await openWithDisks();
    const earlier = browses(daemon).length;
    await reopen(daemon);
    const sinceOpening = () => browses(daemon).slice(earlier);
    expect(sinceOpening()).toEqual([
      { input_path: '~/', host: sandbox.id },
      { input_path: `${HOME}/projects/`, host: sandbox.id },
    ]);
    expect(pathInput()).toHaveAttribute('placeholder', 'Project, path, or machine:path');
    expect(screen.getByText('name:')).toBeInTheDocument();

    await typePath(daemon, 'ai');
    expect(rowNames()).toEqual([
      { name: 'ai-sandbox:', machine: null, selected: true },
      { name: 'shop', machine: 'ai-sandbox', selected: false },
    ]);
    expect(screen.getByText('Browse a machine')).toBeInTheDocument();
    expect(screen.getByText('connected · Tab browses ai-sandbox:~/')).toBeInTheDocument();
    expect(screen.getByText('-sandbox:~/')).toHaveClass('path-ghost');

    await press(daemon, 'Tab');
    expect(pathInput()).toHaveValue('ai-sandbox:~/');
    expect(document.querySelector('.path-input-accent')).toHaveTextContent('ai-sandbox:');
    expect(status()).toBeNull();
    expect(rowNames().map(({ name }) => name)).toEqual(['shop', 'projects/', 'scratch/']);
    expect(screen.getByText(`Folders in ~/ on ai-sandbox`)).toBeInTheDocument();
    expect(sinceOpening()).toHaveLength(2);

    await typePath(daemon, 'ai-sandbox:~/projects/e');
    expect(rowNames().map(({ name }) => name)).toEqual(['evals/']);
    await press(daemon, 'Tab');
    expect(pathInput()).toHaveValue('ai-sandbox:~/projects/evals/');
    expect(sinceOpening()).toEqual([
      { input_path: '~/', host: sandbox.id },
      { input_path: `${HOME}/projects/`, host: sandbox.id },
      { input_path: '~/projects/evals/', host: sandbox.id },
    ]);
  });

  it('puts a remote recent address in the input on Tab', async () => {
    const { daemon } = await openWithDisks();
    await reopen(daemon, 'session.newTerminal');
    await press(daemon, 'Tab');
    expect(pathInput()).toHaveValue('ai-sandbox:~/projects/shop/');
  });

  it('searches recents aimed at a machine and holds the highlight while confirmed rows arrive', async () => {
    const { daemon } = await openWithDisks();
    daemon.on('inspect_path', () => undefined);
    await reopen(daemon);
    await typePath(daemon, 'ai-sandbox:e');
    const asked = daemon.sentOf('inspect_path');
    expect(asked.map(({ path, host }) => ({ path, host }))).toEqual([
      { path: '~/projects/shop', host: sandbox.id },
      { path: SENSOR, host: sandbox.id },
      { path: '~/notes', host: sandbox.id },
    ]);
    expect(rowNames()).toEqual([{ name: 'shop', machine: 'ai-sandbox', selected: true }]);

    const answer = (index: number, exists: boolean) =>
      gesture(daemon, () =>
        daemon.replyTo(asked[index], inspection(asked[index].path, { exists, repoRoot: index === 0 ? REPO : undefined })),
      );
    await answer(2, true);
    await press(daemon, 'ArrowDown');
    expect(rowNames()).toEqual([
      { name: 'shop', machine: 'ai-sandbox', selected: false },
      { name: 'notes', machine: 'ai-sandbox', selected: true },
    ]);
    await answer(1, false);
    await answer(0, true);
    expect(rowNames()).toEqual([
      { name: 'shop', machine: 'ai-sandbox', selected: false },
      { name: 'notes', machine: 'ai-sandbox', selected: true },
    ]);

    await press(daemon, 'Enter');
    expect(spawns(daemon)).toEqual([{ cwd: NOTES, agent: 'claude', host: sandbox.id }]);
  });

  it('keeps a highlighted confirmed row when an earlier candidate answers later', async () => {
    const { daemon } = await openWithDisks();
    daemon.on('inspect_path', () => undefined);
    await reopen(daemon);
    await typePath(daemon, 'ai-sandbox:o');
    const asked = daemon.sentOf('inspect_path');
    const answer = (path: string) => {
      const command = asked.find((candidate) => candidate.path === path)!;
      return gesture(daemon, () => daemon.replyTo(command, inspection(path)));
    };
    await answer('~/notes');
    await press(daemon, 'ArrowDown');
    await answer(SENSOR);
    expect(rowNames()).toEqual([
      { name: 'shop', machine: 'ai-sandbox', selected: false },
      { name: 'notes', machine: 'ai-sandbox', selected: true },
      { name: 'sensor-hub', machine: 'ai-sandbox', selected: false },
    ]);
  });

  it('finds a folder missing on the target on another machine and rewrites the input to it', async () => {
    const { daemon } = await openWithDisks();
    await reopen(daemon);
    await typePath(daemon, '~/scratch/');
    expect(rowNames()).toEqual([
      { name: '~/scratch/ is on ai-sandbox', machine: 'ai-sandbox', selected: false },
    ]);
    expect(screen.getByText('Enter browses ai-sandbox:~/scratch/')).toBeInTheDocument();
    expect(browses(daemon).filter(({ host }) => host === piLab.id)).toEqual([]);
    await press(daemon, 'ArrowDown');
    await press(daemon, 'Enter');
    expect(pathInput()).toHaveValue('ai-sandbox:~/scratch/');
    expect(spawns(daemon)).toEqual([]);

    await typePath(daemon, 'ai-sandbox:~/notes/');
    expect(status()).toBe('~/notes is not on ai-sandbox');
    expect(rowNames()).toEqual([
      { name: '~/notes/ is on This Mac', machine: 'This Mac', selected: false },
    ]);
    await press(daemon, 'ArrowDown');
    await press(daemon, 'Tab');
    expect(pathInput()).toHaveValue('~/notes/');

    await typePath(daemon, 'ai-sandbox:~/projects/');
    expect(screen.queryByText(/is on/)).toBeNull();
  });

  it('keeps saying a folder is missing after the machine reports its home', async () => {
    const { daemon } = await openPicker({}, { settings, outposts: [] });
    await press(daemon, 'Escape');
    serveOutposts(daemon);
    serveDisks(daemon, { '': macDisk, [sandbox.id]: sandboxDisk });
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [sandbox, piLab] }));
    daemon.on('browse_directory', ({ input_path, host }) =>
      host && input_path !== '~/nope/'
        ? undefined
        : {
            event: 'browse_directory_result',
            success: false,
            input_path,
            directory: '',
            entries: [],
            home_path: HOME,
            error: `open ${HOME}/nope: no such file or directory`,
          },
    );
    await reopen(daemon);
    await typePath(daemon, 'ai-sandbox:~/nope/');
    expect(status()).toBe('~/nope is not on ai-sandbox');
    const prefetch = daemon.sentOf('browse_directory').find(({ input_path }) => input_path === '~/')!;
    await gesture(daemon, () =>
      daemon.replyTo(prefetch, {
        event: 'browse_directory_result',
        success: true,
        input_path: '~/',
        home_path: HOME,
        directory: HOME,
        entries: [],
      }),
    );
    expect(status()).toBe('~/nope is not on ai-sandbox');
  });

  it('shows how a slow and an unreachable machine answer under the input', async () => {
    const { daemon } = await openWithDisks();
    const served = daemon.sentOf('browse_directory').length;
    daemon.on('browse_directory', () => undefined);
    await reopen(daemon);
    await typePath(daemon, 'ai-sandbox:~/notes/');
    expect(status()).toBe('asking ai-sandbox for ~/notes/ · 150 ms');
    expect(screen.queryByTestId('location-picker-empty')).toBeNull();
    await act(() => vi.advanceTimersByTimeAsync(250));
    expect(status()).toBe('asking ai-sandbox for ~/notes/ · 400 ms');

    const [prefetchHome] = daemon.sentOf('browse_directory').slice(served);
    serveDisks(daemon, { [sandbox.id]: sandboxDisk });
    await gesture(daemon, () =>
      daemon.replyTo(prefetchHome, {
        event: 'browse_directory_result',
        success: true,
        input_path: '~/',
        home_path: HOME,
        directory: HOME,
        entries: [{ name: 'projects', path: `${HOME}/projects`, is_dir: true, is_repo: false }],
      }),
    );
    const lostAt = new Date(Date.now() - 2 * 60_000).toISOString();
    await gesture(daemon, () =>
      daemon.emit({
        event: 'outposts_updated',
        outposts: [{ ...sandbox, state: 'lost', reach: 'unreachable', state_since: lostAt }, piLab],
      }),
    );
    await typePath(daemon, 'ai-sandbox:~/');
    expect(status()).toBe('ai-sandbox unreachable · folders as of just now');
    expect(rowNames().map(({ name }) => name)).toEqual(['shop', 'projects/']);

    const before = daemon.sent.length;
    await typePath(daemon, 'pi-lab:~/');
    expect(status()).toBe('pi-lab unreachable · no folders cached for ~');
    expect(daemon.sent.slice(before)).toEqual([]);
  });

  it('finishes a typed start in the background after Esc on a stalled machine', async () => {
    const { daemon } = await openWithDisks();
    daemon.on('inspect_path', () => undefined);
    await reopen(daemon);
    await typePath(daemon, 'ai-sandbox:~/projects/');
    await press(daemon, 'Enter');
    await gesture(daemon, () =>
      daemon.emit({
        event: 'outposts_updated',
        outposts: [{ ...sandbox, state: 'stalled', state_since: new Date(Date.now()).toISOString() }, piLab],
      }),
    );
    await act(() => vi.advanceTimersByTimeAsync(4_000));
    await press(daemon, 'Escape');
    expect(screen.queryByTestId('location-picker')).toBeNull();
    const [asked] = daemon.sentOf('inspect_path');
    await gesture(daemon, () => daemon.replyTo(asked, inspection(asked.path)));
    expect(spawns(daemon)).toEqual([{ cwd: `${HOME}/projects`, agent: 'claude', host: sandbox.id }]);
  });

  it('lists every machine a shared name means and browses the one picked', async () => {
    const twin: OutpostInfo = { ...sandbox, id: 'outpost-ai-2', name: 'AI-Sandbox', destination: 'ai-sandbox-2' };
    const { daemon } = await openWithDisks({ '': macDisk, [sandbox.id]: sandboxDisk, [twin.id]: sandboxDisk }, [sandbox, twin]);
    await reopen(daemon);
    const before = browses(daemon).length;
    await typePath(daemon, 'ai-sandbox:~/');
    expect(status()).toBe('ai-sandbox names 2 machines · pick one below');
    expect(rowNames().map(({ name }) => name)).toEqual(['ai-sandbox:', 'AI-Sandbox:']);
    expect(screen.getByText(`connected · Tab browses ${twin.id}:~/`)).toBeInTheDocument();
    expect(browses(daemon).slice(before)).toEqual([]);

    await press(daemon, 'ArrowDown');
    await press(daemon, 'Tab');
    expect(pathInput()).toHaveValue(`${twin.id}:~/`);
    expect(screen.getByText('Folders in ~/ on AI-Sandbox')).toBeInTheDocument();
    expect(browses(daemon).slice(before).map(({ host }) => host)).not.toContain(sandbox.id);
  });

  it('opens a terminal at an address on its machine', async () => {
    const { daemon } = await openWithDisks();
    await reopen(daemon, 'session.newTerminal');
    await typePath(daemon, 'ai-sandbox:~/projects/');
    await press(daemon, 'Enter');
    expect(daemon.sentOf('inspect_path').map(({ path, host }) => ({ path, host }))).toEqual([
      { path: `${HOME}/projects`, host: sandbox.id },
    ]);
    expect(spawns(daemon)).toEqual([{ cwd: `${HOME}/projects`, agent: 'shell', host: sandbox.id }]);
  });
});
