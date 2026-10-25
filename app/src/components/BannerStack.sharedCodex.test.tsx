import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openActionMenu } from '../test/appFixtures';
import { DEFAULT_DESKTOP_ID, DEFAULT_PROFILE_ID, daemonSession, defaultProfile, emptyDesktop, soloDesktop } from '../test/daemonFixtures';
import type { EventMessage } from '../test/protocol';
import { gesture, renderApp, restartApp, type AppRender } from '../test/renderApp';
import type { ScriptedDaemon } from '../test/scriptedDaemon';

type SharedCodex = NonNullable<EventMessage<'shared_codex_changed'>['shared_codex']>;

const FAILURE = 'approval_policy = "untrusted" is no longer supported';

function codex(overrides: Partial<SharedCodex> = {}): SharedCodex {
  return { running_version: '0.162.1', installed_version: '0.162.1', latest_version: '0.162.1', restart: 'none', ...overrides };
}

const sessions = [
  daemonSession('fix-auth', { agent: 'codex' }),
  daemonSession('docs-sweep', { agent: 'codex' }),
  daemonSession('plan-cache', { agent: 'codex' }),
  daemonSession('nightly-triage', { agent: 'codex', hidden: true }),
];

async function renderCodex(sharedCodex?: SharedCodex): Promise<AppRender> {
  const view = await renderApp({
    initialState: {
      sessions,
      desktops: [soloDesktop('fix-auth'), soloDesktop('docs-sweep'), soloDesktop('plan-cache')],
      ...(sharedCodex ? { shared_codex: sharedCodex } : {}),
    },
  });
  view.daemon.on('restart_shared_codex', ({ profile_id }) => ({ event: 'restart_shared_codex_result', profile_id, success: true }));
  view.daemon.on('reload_session', ({ id }) => ({ event: 'reload_session_result', id, success: true }));
  return view;
}

async function change(daemon: ScriptedDaemon, sharedCodex: SharedCodex | undefined, profileId = DEFAULT_PROFILE_ID) {
  daemon.emit({ event: 'shared_codex_changed', profile_id: profileId, ...(sharedCodex ? { shared_codex: sharedCodex } : {}) });
  await daemon.idle();
}

const banner = () => screen.queryByRole('region', { name: 'Codex updates' });
const inBanner = () => within(screen.getByRole('region', { name: 'Codex updates' }));

async function click(daemon: ScriptedDaemon, button: HTMLElement) {
  await gesture(daemon, () => fireEvent.click(button));
}

async function runCommand(daemon: ScriptedDaemon, title: string) {
  const input = await openActionMenu(daemon);
  await gesture(daemon, () => fireEvent.change(input, { target: { value: `>${title}` } }));
  await gesture(daemon, () => fireEvent.keyDown(input, { key: 'Enter' }));
}

async function paletteTitles(daemon: ScriptedDaemon, query: string): Promise<string[]> {
  const input = await openActionMenu(daemon);
  await gesture(daemon, () => fireEvent.change(input, { target: { value: `>${query}` } }));
  const titles = within(screen.getByRole('dialog', { name: 'Commands' })).queryAllByRole('option')
    .map((row) => row.querySelector('.unified-palette-name')?.firstChild?.textContent ?? '');
  await gesture(daemon, () => fireEvent.keyDown(input, { key: 'Escape' }));
  return titles;
}

const lowerStages = { latest_version: '0.164.0', stale_sessions: ['fix-auth', 'docs-sweep'] };

describe('the Codex banner', () => {
  it('shows nothing while Shared Codex is off or everything runs the installed Codex', async () => {
    const { daemon } = await renderCodex();
    expect(banner()).toBeNull();
    await change(daemon, codex());
    expect(banner()).toBeNull();
    await change(daemon, codex({ ...lowerStages, running_version: '0.163.0', installed_version: '0.163.0' }));
    expect(banner()).not.toBeNull();
    await change(daemon, undefined);
    expect(banner()).toBeNull();
  });

  it.each([
    {
      stage: 'failed',
      state: codex({ ...lowerStages, installed_version: '0.163.0', restart: 'failed', error: `${FAILURE}.` }),
      text: `Codex 0.163.0 didn’t start: ${FAILURE}. Still on 0.162.1, nothing was interrupted.Retry`,
    },
    {
      stage: 'restarting now',
      state: codex({ ...lowerStages, installed_version: '0.163.0', restart: 'restarting', force_at: '2026-10-25T12:00:42Z', blockers: [{ session_id: 'fix-auth', reason: 'turn' }] }),
      text: 'Restarting Codex now: waiting for fix-auth to finish. Force stop in 0:42.',
    },
    {
      stage: 'restarting',
      state: codex({ ...lowerStages, installed_version: '0.163.0', restart: 'restarting' }),
      text: 'Restarting Codex on 0.163.0. Tiles reconnect on their own.',
    },
    {
      stage: 'pending without blockers',
      state: codex({ ...lowerStages, installed_version: '0.163.0', restart: 'pending' }),
      text: 'Codex 0.163.0 installed. Restarts when agents are idle.',
    },
    {
      stage: 'available',
      state: codex({ latest_version: '0.163.0', stale_sessions: ['fix-auth'] }),
      text: 'Codex 0.163.0 is available.Install×',
    },
    {
      stage: 'old tiles',
      state: codex({ running_version: '0.163.0', installed_version: '0.163.0', latest_version: '0.163.0', stale_sessions: ['fix-auth', 'docs-sweep'] }),
      text: 'Codex 0.163.0 is running. 2 tiles still run an older Codex.Reload tiles×',
    },
  ])('shows the $stage stage over every lower one', async ({ state, text }) => {
    vi.setSystemTime(new Date('2026-10-25T12:00:00Z'));
    await renderCodex(state);
    expect(banner()).toHaveTextContent(text);
  });

  it('closes only the available and old tiles stages', async () => {
    const { daemon } = await renderCodex(codex({ installed_version: '0.163.0', restart: 'failed', error: FAILURE }));
    for (const state of [
      codex({ installed_version: '0.163.0', restart: 'failed', error: FAILURE }),
      codex({ installed_version: '0.163.0', restart: 'restarting', force_at: '2026-10-25T12:00:42Z' }),
      codex({ installed_version: '0.163.0', restart: 'restarting' }),
      codex({ installed_version: '0.163.0', restart: 'pending', blockers: [{ session_id: 'fix-auth', reason: 'turn' }] }),
    ]) {
      await change(daemon, state);
      expect(inBanner().queryByRole('button', { name: /Dismiss/ })).toBeNull();
    }
  });

  it('says why each blocker holds the restart, opens its tile, and relaunches one still starting', async () => {
    const { daemon } = await renderCodex(codex({
      installed_version: '0.163.0',
      restart: 'pending',
      blockers: [
        { session_id: 'fix-auth', reason: 'turn' },
        { session_id: 'docs-sweep', reason: 'approval' },
        { session_id: 'nightly-triage', reason: 'question' },
        { session_id: 'plan-cache', reason: 'starting' },
      ],
    }));
    expect(banner()).toHaveTextContent(
      'Codex 0.163.0 installed. Restarts when agents are idle. Waiting on fix-auth (working), docs-sweep (waiting on an approval), nightly-triage (asking a question), plan-cache (still starting or asking whether to trust its folder) Relaunch.Restart now',
    );
    expect(inBanner().queryByRole('button', { name: 'Relaunch fix-auth' })).toBeNull();

    await click(daemon, inBanner().getByRole('button', { name: 'Go to docs-sweep' }));
    expect(daemon.sentOf('desktop_show_session').pop()).toMatchObject({ session_id: 'docs-sweep' });

    await click(daemon, inBanner().getByRole('button', { name: 'Relaunch plan-cache' }));
    expect(daemon.sentOf('reload_session')).toEqual([expect.objectContaining({ id: 'plan-cache' })]);
    expect(daemon.sentOf('restart_shared_codex')).toEqual([]);
  });

  it('restarts now only after the confirm, which lists what each blocker loses', async () => {
    const { daemon } = await renderCodex(codex({
      installed_version: '0.163.0',
      restart: 'pending',
      blockers: [
        { session_id: 'fix-auth', reason: 'turn' },
        { session_id: 'docs-sweep', reason: 'approval' },
        { session_id: 'nightly-triage', reason: 'question' },
        { session_id: 'plan-cache', reason: 'starting' },
      ],
    }));

    await click(daemon, inBanner().getByRole('button', { name: 'Restart now' }));
    const dialog = within(screen.getByRole('dialog', { name: 'Restart shared Codex now?' }));
    expect(dialog.getByText('Every Codex tile in this profile goes offline until 0.163.0 is up. What you typed stays.')).toBeVisible();
    expect(dialog.getAllByRole('listitem').map((row) => row.textContent)).toEqual([
      'fix-authTurn running. Gets up to 60s to finish, then it is stopped.',
      'docs-sweepWaiting for your approval. The request is lost.',
      'nightly-triage (hidden)Asking you a question. The question is lost.',
      'plan-cacheStill starting. Its tile relaunches.',
    ]);
    expect(dialog.getByRole('button', { name: 'Cancel' })).toHaveFocus();

    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
    expect(screen.queryByRole('dialog', { name: 'Restart shared Codex now?' })).toBeNull();
    await click(daemon, inBanner().getByRole('button', { name: 'Restart now' }));
    await click(daemon, screen.getByRole('button', { name: 'Cancel' }));
    expect(daemon.sentOf('restart_shared_codex')).toEqual([]);

    await click(daemon, inBanner().getByRole('button', { name: 'Restart now' }));
    await click(daemon, within(screen.getByRole('dialog', { name: 'Restart shared Codex now?' })).getByRole('button', { name: 'Restart now' }));
    expect(screen.queryByRole('dialog', { name: 'Restart shared Codex now?' })).toBeNull();
    expect(daemon.sentOf('restart_shared_codex')).toEqual([
      { cmd: 'restart_shared_codex', request_id: expect.any(String), profile_id: DEFAULT_PROFILE_ID, now: true },
    ]);
  });

  it('closes the restart confirm once the restart no longer waits on anyone', async () => {
    const { daemon } = await renderCodex(codex({ installed_version: '0.163.0', restart: 'pending', blockers: [{ session_id: 'fix-auth', reason: 'turn' }] }));
    await click(daemon, inBanner().getByRole('button', { name: 'Restart now' }));
    expect(screen.getByRole('dialog', { name: 'Restart shared Codex now?' })).toBeVisible();

    await change(daemon, codex({ installed_version: '0.163.0', restart: 'restarting' }));

    expect(screen.queryByRole('dialog', { name: 'Restart shared Codex now?' })).toBeNull();
    expect(daemon.sentOf('restart_shared_codex')).toEqual([]);
  });

  it('retries a failed restart once agents are idle', async () => {
    const { daemon } = await renderCodex(codex({ installed_version: '0.163.0', restart: 'failed', error: FAILURE }));
    await click(daemon, inBanner().getByRole('button', { name: 'Retry' }));
    expect(daemon.sentOf('restart_shared_codex')).toEqual([
      { cmd: 'restart_shared_codex', request_id: expect.any(String), profile_id: DEFAULT_PROFILE_ID, now: false },
    ]);
  });

  it('counts down to the force stop while it waits, and stops counting once the restart moves on', async () => {
    vi.setSystemTime(new Date('2026-10-25T12:00:00Z'));
    const { daemon } = await renderCodex(codex({
      installed_version: '0.163.0',
      restart: 'restarting',
      force_at: '2026-10-25T12:01:05Z',
      blockers: [{ session_id: 'fix-auth', reason: 'turn' }, { session_id: 'docs-sweep', reason: 'approval' }],
    }));
    expect(banner()).toHaveTextContent('Restarting Codex now: waiting for fix-auth, docs-sweep to finish. Force stop in 1:05.');
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(banner()).toHaveTextContent('Force stop in 1:04.');
    await act(() => vi.advanceTimersByTimeAsync(64_000));
    expect(banner()).toHaveTextContent('Force stop in 0:00.');
    const ticking = vi.getTimerCount();

    await change(daemon, codex({ installed_version: '0.163.0', restart: 'restarting' }));

    expect(banner()).toHaveTextContent('Restarting Codex on 0.163.0. Tiles reconnect on their own.');
    expect(vi.getTimerCount()).toBe(ticking - 1);
  });

  it('installs from the banner in a tile on the current desktop and shows a failure where it offered the update', async () => {
    const { daemon } = await renderCodex(codex({ latest_version: '0.163.0' }));
    daemon.on('install_codex_update', () => undefined);

    await click(daemon, inBanner().getByRole('button', { name: 'Install' }));
    expect(daemon.sentOf('install_codex_update')).toEqual([
      { cmd: 'install_codex_update', request_id: expect.any(String), desktop_id: DEFAULT_DESKTOP_ID },
    ]);
    expect(banner()).toHaveTextContent('Installing Codex 0.163.0 in a terminal tile…');
    await act(() => vi.advanceTimersByTimeAsync(5 * 60_000));
    expect(banner()).toHaveTextContent('Installing Codex 0.163.0 in a terminal tile…');

    const [first] = daemon.sentOf('install_codex_update');
    await gesture(daemon, () => daemon.replyTo(first, { event: 'install_codex_update_result', success: false, error: 'codex update exited 1', profile_id: DEFAULT_PROFILE_ID, shared_codex: codex({ latest_version: '0.163.0' }) }));
    expect(banner()).toHaveTextContent('Codex 0.163.0 is available. Install failed: codex update exited 1.Install×');

    await click(daemon, inBanner().getByRole('button', { name: 'Install' }));
    expect(banner()).toHaveTextContent('Installing Codex 0.163.0 in a terminal tile…');
    const [, second] = daemon.sentOf('install_codex_update');
    const installed = codex({ installed_version: '0.163.0', latest_version: '0.163.0', restart: 'pending' });
    await gesture(daemon, () => daemon.replyTo(second, { event: 'install_codex_update_result', success: true, profile_id: DEFAULT_PROFILE_ID, shared_codex: installed }));
    expect(banner()).toHaveTextContent('Codex 0.163.0 installed. Restarts when agents are idle.');
  });

  it('shows a restart over an outstanding install, which lasts until its result', async () => {
    const { daemon } = await renderCodex(codex({ latest_version: '0.163.0' }));
    daemon.on('install_codex_update', () => undefined);
    await click(daemon, inBanner().getByRole('button', { name: 'Install' }));

    await change(daemon, codex({ installed_version: '0.163.0', latest_version: '0.163.0', restart: 'pending' }));
    expect(banner()).toHaveTextContent('Codex 0.163.0 installed. Restarts when agents are idle.');
    await change(daemon, codex({ running_version: '0.163.0', installed_version: '0.163.0', latest_version: '0.163.0' }));
    expect(banner()).toHaveTextContent('Installing Codex 0.163.0 in a terminal tile…');
  });

  it('forgets an outstanding install when the daemon disconnects', async () => {
    const view = await renderCodex(codex({ latest_version: '0.163.0' }));
    view.daemon.on('install_codex_update', () => undefined);
    await click(view.daemon, inBanner().getByRole('button', { name: 'Install' }));

    await view.daemon.reconnect();
    await view.daemon.idle();

    expect(banner()).toHaveTextContent('Codex 0.163.0 is available.Install×');
  });

  it('keeps a closed update closed for that version, across restarts, until a newer one', async () => {
    const view = await renderCodex(codex({ latest_version: '0.163.0' }));
    await click(view.daemon, inBanner().getByRole('button', { name: 'Dismiss Codex update' }));
    expect(banner()).toBeNull();

    const restarted = await restartApp(view, { initialState: { sessions, shared_codex: codex({ latest_version: '0.163.0' }) } });
    expect(banner()).toBeNull();
    expect(await paletteTitles(restarted.daemon, 'Install Codex update')).toEqual(['Install Codex update']);

    await change(restarted.daemon, codex({ latest_version: '0.164.0' }));
    expect(banner()).toHaveTextContent('Codex 0.164.0 is available.');
  });

  it('brings a closed update back to show why an install from elsewhere failed', async () => {
    const { daemon } = await renderCodex(codex({ latest_version: '0.163.0' }));
    daemon.on('install_codex_update', () => ({ event: 'install_codex_update_result', success: false, error: 'codex update exited 1', profile_id: DEFAULT_PROFILE_ID, shared_codex: codex({ latest_version: '0.163.0' }) }));
    await click(daemon, inBanner().getByRole('button', { name: 'Dismiss Codex update' }));

    await runCommand(daemon, 'Install Codex update');

    expect(banner()).toHaveTextContent('Codex 0.163.0 is available. Install failed: codex update exited 1.');
    await click(daemon, inBanner().getByRole('button', { name: 'Dismiss Codex update' }));
    expect(banner()).toBeNull();
  });

  it('keeps old tiles closed until more tiles fall behind or the running Codex changes', async () => {
    const running = { running_version: '0.163.0', installed_version: '0.163.0', latest_version: '0.163.0' };
    const { daemon } = await renderCodex(codex({ ...running, stale_sessions: ['docs-sweep', 'fix-auth'] }));
    await click(daemon, inBanner().getByRole('button', { name: 'Dismiss old Codex tiles notice' }));
    expect(banner()).toBeNull();

    await change(daemon, codex({ ...running, stale_sessions: ['fix-auth'] }));
    expect(banner()).toBeNull();
    await change(daemon, codex({ ...running, stale_sessions: ['docs-sweep', 'fix-auth', 'plan-cache'] }));
    expect(banner()).toHaveTextContent('3 tiles still run an older Codex.');

    await click(daemon, inBanner().getByRole('button', { name: 'Dismiss old Codex tiles notice' }));
    await change(daemon, codex({ running_version: '0.164.0', installed_version: '0.164.0', latest_version: '0.164.0', stale_sessions: ['fix-auth'] }));
    expect(banner()).toHaveTextContent('Codex 0.164.0 is running. 1 tile still runs an older Codex.');
  });

  it('reloads each old tile after the confirm', async () => {
    const { daemon } = await renderCodex(codex({ running_version: '0.163.0', installed_version: '0.163.0', latest_version: '0.163.0', stale_sessions: ['docs-sweep', 'fix-auth'] }));

    await click(daemon, inBanner().getByRole('button', { name: 'Reload tiles' }));
    const dialog = within(screen.getByRole('dialog', { name: 'Reload 2 Codex tiles?' }));
    expect(dialog.getByText('Each tile restarts its Codex on 0.163.0 and resumes its conversation. Text you typed and haven’t sent in them is lost.')).toBeVisible();
    expect(dialog.getAllByRole('listitem').map((row) => row.textContent)).toEqual(['docs-sweep', 'fix-auth']);
    await click(daemon, dialog.getByRole('button', { name: 'Cancel' }));
    expect(daemon.sentOf('reload_session')).toEqual([]);

    await click(daemon, inBanner().getByRole('button', { name: 'Reload tiles' }));
    await click(daemon, within(screen.getByRole('dialog', { name: 'Reload 2 Codex tiles?' })).getByRole('button', { name: 'Reload tiles' }));
    expect(screen.queryByRole('dialog', { name: 'Reload 2 Codex tiles?' })).toBeNull();
    expect(daemon.sentOf('reload_session').map((command) => command.id)).toEqual(['docs-sweep', 'fix-auth']);
  });

  it('offers each action in the command palette only while it applies', async () => {
    const { daemon } = await renderCodex(codex());
    const titles = () => paletteTitles(daemon, 'codex update');
    expect(await titles()).toEqual([]);

    await change(daemon, codex({ latest_version: '0.163.0' }));
    expect(await titles()).toEqual(['Install Codex update']);
    daemon.on('install_codex_update', () => undefined);
    await runCommand(daemon, 'Install Codex update');
    const [install] = daemon.sentOf('install_codex_update');
    expect(install).toMatchObject({ desktop_id: DEFAULT_DESKTOP_ID });
    expect(await titles()).toEqual([]);
    await gesture(daemon, () => daemon.replyTo(install, { event: 'install_codex_update_result', success: true, profile_id: DEFAULT_PROFILE_ID, shared_codex: codex({ installed_version: '0.163.0', latest_version: '0.163.0', restart: 'pending' }) }));

    await change(daemon, codex({ installed_version: '0.163.0', latest_version: '0.163.0', restart: 'pending', blockers: [{ session_id: 'fix-auth', reason: 'turn' }] }));
    expect(await titles()).toEqual(['Restart shared Codex now']);
    await runCommand(daemon, 'Restart shared Codex now');
    await click(daemon, within(screen.getByRole('dialog', { name: 'Restart shared Codex now?' })).getByRole('button', { name: 'Restart now' }));
    expect(daemon.sentOf('restart_shared_codex')).toEqual([expect.objectContaining({ now: true })]);

    await change(daemon, codex({ running_version: '0.163.0', installed_version: '0.163.0', latest_version: '0.163.0', stale_sessions: ['fix-auth'] }));
    await click(daemon, inBanner().getByRole('button', { name: 'Dismiss old Codex tiles notice' }));
    expect(await titles()).toEqual(['Reload old Codex tiles']);
    await runCommand(daemon, 'Reload old Codex tiles');
    await click(daemon, within(screen.getByRole('dialog', { name: 'Reload 1 Codex tile?' })).getByRole('button', { name: 'Reload tile' }));
    expect(daemon.sentOf('reload_session').map((command) => command.id)).toEqual(['fix-auth']);
  });

  it('shows only the profile on screen', async () => {
    const side = defaultProfile('side-desktop', { id: 'profile-side', name: 'Side' });
    const { daemon } = await renderApp({
      initialState: {
        profiles: [defaultProfile(DEFAULT_DESKTOP_ID), side],
        desktops: [emptyDesktop(DEFAULT_DESKTOP_ID), emptyDesktop('side-desktop', { profile_id: side.id })],
        shared_codex: codex({ latest_version: '0.163.0' }),
      },
    });
    expect(banner()).toHaveTextContent('Codex 0.163.0 is available.');

    await runCommand(daemon, 'Switch to Side');
    expect(daemon.sentOf('profile_select')).toEqual([expect.objectContaining({ profile_id: side.id })]);
    expect(banner()).toBeNull();

    await change(daemon, codex({ installed_version: '0.163.0', latest_version: '0.163.0', restart: 'pending' }), side.id);
    expect(banner()).toHaveTextContent('Codex 0.163.0 installed. Restarts when agents are idle.');
  });

  it('keeps the profile on screen when an install started in another profile finishes', async () => {
    const side = defaultProfile('side-desktop', { id: 'profile-side', name: 'Side' });
    const { daemon } = await renderApp({
      initialState: {
        profiles: [defaultProfile(DEFAULT_DESKTOP_ID), side],
        desktops: [emptyDesktop(DEFAULT_DESKTOP_ID), emptyDesktop('side-desktop', { profile_id: side.id })],
        shared_codex: codex({ latest_version: '0.163.0' }),
      },
    });
    daemon.on('install_codex_update', () => undefined);
    await click(daemon, inBanner().getByRole('button', { name: 'Install' }));
    const [install] = daemon.sentOf('install_codex_update');
    await runCommand(daemon, 'Switch to Side');
    await change(daemon, codex({ latest_version: '0.164.0' }), side.id);

    await gesture(daemon, () => daemon.replyTo(install, {
      event: 'install_codex_update_result', success: false, error: 'codex update exited 1', profile_id: DEFAULT_PROFILE_ID, shared_codex: codex({ latest_version: '0.163.0' }),
    }));

    expect(banner()).toHaveTextContent('Codex 0.164.0 is available. Install failed: codex update exited 1.');
  });

  it('offers Retry in the command palette while the installed Codex refuses to start', async () => {
    const { daemon } = await renderCodex(codex({ installed_version: '0.163.0', latest_version: '0.163.0', restart: 'failed', error: FAILURE }));
    await runCommand(daemon, 'Retry shared Codex restart');
    expect(daemon.sentOf('restart_shared_codex')).toEqual([expect.objectContaining({ now: false })]);
  });
});
