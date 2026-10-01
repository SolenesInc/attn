import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openSession } from './test/appFixtures';
import { daemonSession, emptyDesktop, soloDesktop } from './test/daemonFixtures';
import { launchedAt, pathInput, repoInfo, serveLaunches, serveMachine, submitPath } from './test/locations';
import { gesture, pressShortcut, renderApp } from './test/renderApp';

const launcher = () => screen.queryByTestId('empty-desktop-launcher');

async function onAgentBesideEmptyDesktop() {
  const view = await renderApp({ initialState: {
    sessions: [daemonSession('s1', { state: 'idle', directory: '/tmp/s1' })],
    desktops: [soloDesktop('s1', { shortcut_slot: 1 }), emptyDesktop('other', { shortcut_slot: 2 })],
  } });
  serveMachine(view.daemon, { recent: ['/home/me/projects/repo'], repos: [repoInfo('/home/me/projects/repo')] });
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

  it('stays off Home', async () => {
    const { daemon } = await onAgentBesideEmptyDesktop();
    await gesture(daemon, () => pressShortcut('desktop.select2'));

    await gesture(daemon, () => pressShortcut('session.goToDashboard'));

    expect(launcher()).toBeNull();
  });
});
