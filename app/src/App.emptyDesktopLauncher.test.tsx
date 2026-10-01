import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openSession } from './test/appFixtures';
import { daemonSession, emptyDesktop, soloDesktop } from './test/daemonFixtures';
import { inspection, launchedAt, pathInput, repoInfo, serveLaunches, serveMachine, submitPath } from './test/locations';
import type { CommandMessage } from './test/protocol';
import { gesture, pressShortcut, renderApp } from './test/renderApp';

const launcher = () => screen.queryByTestId('empty-desktop-launcher');

async function onAgentBesideEmptyDesktop() {
  const view = await renderApp({ initialState: {
    sessions: [daemonSession('s1', { state: 'idle', directory: '/tmp/s1' })],
    desktops: [soloDesktop('s1', { shortcut_slot: 1 }), emptyDesktop('other', { shortcut_slot: 2 })],
  } });
  serveMachine(view.daemon, { recent: ['/home/me/projects/repo'], repos: [repoInfo('/home/me/projects/repo', [{ path: '/home/me/projects/repo--feature', branch: 'feature' }])] });
  serveLaunches(view.daemon);
  await openSession(view.daemon, 's1');
  return view;
}

describe('App empty desktop launcher', () => {
  it('offers the new agent launcher, focused, on a desktop with no leaves', async () => {
    const { daemon } = await onAgentBesideEmptyDesktop();
    expect(launcher()).toBeNull();

    await gesture(daemon, () => pressShortcut('desktop.select2'));

    expect(screen.getByTestId('location-picker-title')).toHaveTextContent('New agent on Desktop 2');
    expect(pathInput()).toHaveFocus();
    expect(screen.getByRole('radiogroup', { name: /agent/i })).toBeInTheDocument();
  });

  it('launches onto the empty desktop and gives way to the new pane', async () => {
    const { daemon } = await onAgentBesideEmptyDesktop();
    await gesture(daemon, () => pressShortcut('desktop.select2'));

    await submitPath(daemon, '/home/me/projects/scratch');

    expect(launchedAt(daemon)).toEqual([{ cwd: '/home/me/projects/scratch', agent: 'claude' }]);
    expect(daemon.sentOf('spawn_session')[0].placement?.desktop_id).toBe('other');
    expect(launcher()).toBeNull();
  });

  it('returns when the last leaf leaves, and goes when one arrives', async () => {
    const { daemon } = await onAgentBesideEmptyDesktop();

    await gesture(daemon, () => pressShortcut('desktop.sendStay2'));
    expect(screen.getByTestId('location-picker-title')).toHaveTextContent('New agent on Desktop 1');

    await gesture(daemon, () => pressShortcut('desktop.select2'));
    expect(launcher()).toBeNull();
  });

  it('takes New Session itself instead of opening the dialog over it', async () => {
    const { daemon } = await onAgentBesideEmptyDesktop();
    await gesture(daemon, () => pressShortcut('desktop.select2'));
    pathInput().blur();

    await gesture(daemon, () => pressShortcut('session.new'));

    expect(screen.queryByTestId('location-picker-overlay')).toBeNull();
    expect(pathInput()).toHaveFocus();
  });

  it('takes New Session while it offers a repository’s options', async () => {
    const { daemon } = await onAgentBesideEmptyDesktop();
    await gesture(daemon, () => pressShortcut('desktop.select2'));
    await submitPath(daemon, '/home/me/projects/repo');
    (document.activeElement as HTMLElement).blur();

    await gesture(daemon, () => pressShortcut('session.new'));

    expect(screen.queryByTestId('location-picker-overlay')).toBeNull();
    expect(screen.getByTestId('repo-options').contains(document.activeElement)).toBe(true);
  });

  it('takes the keyboard back in a repository’s options when an overlay closes', async () => {
    const { daemon } = await onAgentBesideEmptyDesktop();
    await gesture(daemon, () => pressShortcut('desktop.select2'));
    await submitPath(daemon, '/home/me/projects/repo');

    await gesture(daemon, () => pressShortcut('desktop.overview'));
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));

    expect(screen.queryByRole('dialog', { name: 'Desktop overview' })).toBeNull();
    expect(screen.getByTestId('repo-options').contains(document.activeElement)).toBe(true);
  });

  it('takes the keyboard back when the markdown opener closes', async () => {
    const { daemon } = await onAgentBesideEmptyDesktop();
    await gesture(daemon, () => pressShortcut('desktop.select2'));

    await gesture(daemon, () => pressShortcut('file.open'));
    const opener = screen.getByRole('dialog', { name: 'Open a markdown file' });
    await gesture(daemon, () => fireEvent.keyDown(within(opener).getByRole('combobox'), { key: 'Escape' }));

    expect(screen.queryByRole('dialog', { name: 'Open a markdown file' })).toBeNull();
    expect(pathInput()).toHaveFocus();
  });

  it('drops a pick still being looked up when the user leaves the desktop', async () => {
    const { daemon } = await onAgentBesideEmptyDesktop();
    await gesture(daemon, () => pressShortcut('desktop.select2'));
    const held: CommandMessage[] = [];
    daemon.on('inspect_path', (command) => {
      held.push(command);
      return undefined;
    });
    await submitPath(daemon, '/home/me/projects/scratch');

    await gesture(daemon, () => pressShortcut('desktop.select1'));
    await gesture(daemon, () => daemon.replyTo(held[0], { ...inspection('/home/me/projects/scratch'), request_id: (held[0] as { request_id?: string }).request_id }));

    expect(daemon.sentOf('spawn_session')).toEqual([]);
  });

  it('starts a worktree agent on the desktop it was asked for, even after the user moves on', async () => {
    const { daemon } = await onAgentBesideEmptyDesktop();
    await gesture(daemon, () => pressShortcut('desktop.select2'));
    daemon.on('create_worktree', () => undefined);
    await submitPath(daemon, '/home/me/projects/repo');
    await gesture(daemon, () => fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'ArrowUp' }));
    fireEvent.change(screen.getByTestId('repo-new-worktree-input'), { target: { value: 'fresh' } });
    await gesture(daemon, () => fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'Enter' }));
    const [create] = daemon.sentOf('create_worktree');

    await gesture(daemon, () => pressShortcut('desktop.select1'));
    await gesture(daemon, () => daemon.replyTo(create, { event: 'create_worktree_result', success: true, path: '/home/me/projects/repo--fresh' } as never));

    expect(daemon.sentOf('spawn_session').map((spawn) => [spawn.cwd, spawn.placement?.desktop_id])).toEqual([['/home/me/projects/repo--fresh', 'other']]);
  });

  it('leaves the keyboard with an overlay when repository options arrive under it', async () => {
    const { daemon } = await onAgentBesideEmptyDesktop();
    await gesture(daemon, () => pressShortcut('desktop.select2'));
    daemon.on('get_repo_info', () => undefined);
    await submitPath(daemon, '/home/me/projects/repo');
    const [lookup] = daemon.sentOf('get_repo_info');

    await gesture(daemon, () => pressShortcut('desktop.overview'));
    await gesture(daemon, () => daemon.replyTo(lookup, { event: 'get_repo_info_result', success: true, info: repoInfo('/home/me/projects/repo') } as never));

    expect(screen.getByTestId('repo-options').contains(document.activeElement)).toBe(false);
    expect(screen.getByRole('dialog', { name: 'Desktop overview' }).contains(document.activeElement)).toBe(true);
  });

  it('leaves the keyboard in the sidebar when repository options arrive after the user went there', async () => {
    const { daemon } = await onAgentBesideEmptyDesktop();
    await gesture(daemon, () => pressShortcut('desktop.select2'));
    daemon.on('get_repo_info', () => undefined);
    await submitPath(daemon, '/home/me/projects/repo');
    const [lookup] = daemon.sentOf('get_repo_info');
    const row = screen.getByRole('button', { name: 'Open s1' });
    row.focus();

    await gesture(daemon, () => daemon.replyTo(lookup, { event: 'get_repo_info_result', success: true, info: repoInfo('/home/me/projects/repo') } as never));

    expect(screen.getByTestId('repo-options')).toBeInTheDocument();
    expect(document.activeElement).toBe(row);
  });

  it('stays put on Escape', async () => {
    const { daemon } = await onAgentBesideEmptyDesktop();
    await gesture(daemon, () => pressShortcut('desktop.select2'));
    await gesture(daemon, () => fireEvent.keyDown(pathInput(), { key: 'ArrowDown' }));

    await gesture(daemon, () => fireEvent.keyDown(pathInput(), { key: 'Escape' }));
    await gesture(daemon, () => fireEvent.keyDown(pathInput(), { key: 'Escape' }));

    expect(launcher()).not.toBeNull();
    expect(document.querySelector('[data-session-visible="1"]')?.getAttribute('data-desktop-id')).toBe('other');
  });

  it('leaves Escape to the sidebar when the keyboard is there', async () => {
    const { daemon } = await onAgentBesideEmptyDesktop();
    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { queue_mode_enabled: 'true' } }));
    await gesture(daemon, () => pressShortcut('desktop.select2'));
    await gesture(daemon, () => fireEvent.keyDown(pathInput(), { key: 'ArrowDown' }));
    await gesture(daemon, () => pressShortcut('sidebar.agentList'));
    const filter = screen.getByTestId('queue-agent-filter');
    await gesture(daemon, () => fireEvent.change(filter, { target: { value: 's1' } }));

    await gesture(daemon, () => fireEvent.keyDown(filter, { key: 'Escape' }));

    expect(filter).toHaveValue('');
    expect(launcher()).not.toBeNull();
  });

  it('leaves Escape to the sidebar while a worktree delete waits for an answer', async () => {
    const { daemon } = await onAgentBesideEmptyDesktop();
    await gesture(daemon, () => daemon.emit({ event: 'settings_updated', settings: { queue_mode_enabled: 'true' } }));
    await gesture(daemon, () => pressShortcut('desktop.select2'));
    await submitPath(daemon, '/home/me/projects/repo--feature');
    await gesture(daemon, () => fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'd' }));
    expect(screen.getByText(/Delete repo--feature/)).toBeInTheDocument();
    await gesture(daemon, () => pressShortcut('sidebar.agentList'));
    const filter = screen.getByTestId('queue-agent-filter');
    await gesture(daemon, () => fireEvent.change(filter, { target: { value: 's1' } }));

    await gesture(daemon, () => fireEvent.keyDown(filter, { key: 'Escape' }));

    expect(filter).toHaveValue('');
  });

  it('stays off Home', async () => {
    const { daemon } = await onAgentBesideEmptyDesktop();
    await gesture(daemon, () => pressShortcut('desktop.select2'));

    await gesture(daemon, () => pressShortcut('session.goToDashboard'));

    expect(launcher()).toBeNull();
  });
});
