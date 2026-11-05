import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openActionMenu } from './test/appFixtures';
import { DEFAULT_PROFILE_ID, defaultProfile, emptyDesktop, soloDesktop, daemonSeed, daemonSession } from './test/daemonFixtures';
import type { EventMessage } from './test/protocol';
import { gardenRegion, openGarden, seedHeading } from './test/garden';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

type Notification = NonNullable<EventMessage<'notification_list_result'>['notifications']>[number];

function notification(overrides: Partial<Notification>): Notification {
  return {
    id: 'n1',
    kind: 'task_failed',
    severity: 'info',
    title: 'Something happened',
    body: 'body',
    detail: '',
    trigger: '',
    impact: '',
    cause: '',
    diagnostic: '',
    source_kind: '',
    source_id: '',
    created_at: '2026-08-15T09:00:00Z',
    read_at: '',
    ...overrides,
  };
}

async function openNotifications(notifications: Notification[], seeds = [daemonSeed('s-wake', { title: 'Watch the deploy' })]) {
  const { daemon } = await renderApp({
    initialState: { sessions: [daemonSession('s1'), daemonSession('session-1')], desktops: [soloDesktop('s1'), soloDesktop('session-1')], seeds },
  });
  daemon.on('notification_list', () => ({
    event: 'notification_list_result',
    success: true,
    notifications,
    unread_count: notifications.filter((n) => !n.read_at).length,
    unread_critical_count: 0,
  }));
  const listed = daemon.sentOf('notification_list').length;
  await click(daemon, 'Show Notifications');
  expect(daemon.sentOf('notification_list')).toHaveLength(listed + 1);
  return daemon;
}

const blockedWake = notification({
  id: 'wake',
  kind: 'tender_wake_blocked',
  severity: 'critical',
  title: 's-wake could not wake its tender',
  source_kind: 'seed',
  source_id: 's-wake',
  actions: [{ kind: 'open_seed', label: 'Open seed', target_id: 's-wake' }],
});

const searchField = () => within(gardenRegion()).getByRole('combobox');

async function openGardenWithSearch(search: string) {
  const { daemon } = await openGarden([daemonSeed('s-wake', { title: 'Watch the deploy' })]);
  daemon.on('notification_list', () => ({
    event: 'notification_list_result',
    success: true,
    notifications: [blockedWake],
    unread_count: 1,
    unread_critical_count: 1,
  }));
  fireEvent.change(searchField(), { target: { value: search } });
  return daemon;
}

async function openBlockedSeedFromNotification(daemon: ScriptedDaemon) {
  await click(daemon, 'Show Notifications');
  await gesture(daemon, () => fireEvent.click(row(blockedWake.title)));
  await click(daemon, 'Open seed');
}

async function click(daemon: ScriptedDaemon, name: string) {
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name })));
}

const panel = () => within(screen.getByRole('dialog', { name: 'Notifications' }));
const row = (title: string) => panel().getByRole('button', { name: new RegExp(title) });
const item = (title: string) => row(title).closest('li');

describe('App notifications', () => {
  it('shows each notification at its severity, an unknown one as info, and marks only unread rows unread', async () => {
    await openNotifications([
      notification({ id: 'a', severity: 'critical', title: 'Plugin stopped', read_at: '2026-08-15T09:05:00Z' }),
      notification({ id: 'b', severity: 'warning', title: 'Ticket reconciliation failed' }),
      notification({ id: 'c', severity: 'info', title: 'Compaction finished' }),
      notification({ id: 'd', severity: 'catastrophic' as Notification['severity'], title: 'From the future' }),
    ]);

    expect(item('Plugin stopped')).toHaveAttribute('data-severity', 'critical');
    expect(item('Plugin stopped')).toHaveAttribute('data-unread', 'false');
    expect(item('Ticket reconciliation failed')).toHaveAttribute('data-severity', 'warning');
    expect(item('Ticket reconciliation failed')).toHaveAttribute('data-unread', 'true');
    expect(item('Compaction finished')).toHaveAttribute('data-severity', 'info');
    expect(item('From the future')).toHaveAttribute('data-severity', 'info');
  });

  it('shows a failure’s evidence and only the actions the daemon supplied, wired to their commands', async () => {
    const daemon = await openNotifications([
      notification({
        id: 'failed',
        title: 'Couldn’t update activity for “ci stuff”',
        body: 'legacy impact',
        trigger: 'New output triggered a Home activity summary.',
        impact: 'The Home activity summary may be stale.',
        cause: 'The agent process exited with status 2.',
        diagnostic: 'stderr: authentication failed',
        actions: [
          { kind: 'open_session', label: 'Open session', target_id: 'session-1' },
          { kind: 'retry_task', label: 'Retry', target_id: 'task-1' },
        ],
      }),
    ]);
    daemon.on('task_retry', () => ({ event: 'task_retry_result', success: true }));

    await gesture(daemon, () => fireEvent.click(row('Couldn’t update activity')));
    expect(panel().getByText('New output triggered a Home activity summary.')).toBeInTheDocument();
    expect(panel().getByText('The Home activity summary may be stale.')).toBeInTheDocument();
    expect(panel().getByText('The agent process exited with status 2.')).toBeInTheDocument();
    fireEvent.click(panel().getByText('Diagnostic output'));
    expect(panel().getByText('stderr: authentication failed')).toBeInTheDocument();

    await click(daemon, 'Retry');
    expect(daemon.sentOf('task_retry')).toEqual([expect.objectContaining({ task_id: 'task-1' })]);

    await click(daemon, 'Open session');
    expect(screen.queryByRole('dialog', { name: 'Notifications' })).toBeNull();
    expect(daemon.sentOf('desktop_show_session').pop()).toMatchObject({ session_id: 'session-1' });
  });

  it('opens the seed of a blocked tender wake in the Garden', async () => {
    const daemon = await openNotifications([blockedWake]);

    await gesture(daemon, () => fireEvent.click(row(blockedWake.title)));
    await click(daemon, 'Open seed');
    expect(screen.queryByRole('dialog', { name: 'Notifications' })).toBeNull();
    expect(within(screen.getByTestId('garden-frame')).getAllByText('Watch the deploy').length).toBeGreaterThan(0);
  });

  it('reveals the seed past a search left in a closed Garden, which an ordinary reopen keeps', async () => {
    const daemon = await openGardenWithSearch('unrelated');
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
    await gesture(daemon, () => pressShortcut('board.open'));
    expect(searchField()).toHaveValue('unrelated');
    expect(seedHeading()).toBeNull();
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));

    await openBlockedSeedFromNotification(daemon);

    expect(seedHeading()).toBe('Watch the deploy');
    expect(searchField()).toHaveValue('');
  });

  it('reveals the seed past a search in a Garden that is already open', async () => {
    const daemon = await openGardenWithSearch('unrelated');

    await openBlockedSeedFromNotification(daemon);

    expect(seedHeading()).toBe('Watch the deploy');
    expect(searchField()).toHaveValue('');
  });

  it('installs a Codex update from its notification on the current desktop, and shows it installing until it finishes', async () => {
    const daemon = await openNotifications([
      notification({
        id: 'codex',
        kind: 'codex_update_available',
        title: 'Codex 0.163.0 is available',
        actions: [{ kind: 'install_codex_update', label: 'Install', target_id: '' }],
      }),
    ]);
    daemon.on('install_codex_update', () => undefined);

    await gesture(daemon, () => fireEvent.click(row('Codex 0.163.0 is available')));
    await click(daemon, 'Install');
    expect(screen.queryByRole('dialog', { name: 'Notifications' })).toBeNull();
    const [install] = daemon.sentOf('install_codex_update');
    expect(daemon.sentOf('install_codex_update')).toEqual([
      { cmd: 'install_codex_update', request_id: expect.any(String), desktop_id: daemon.arrangement.profile.current_desktop_id },
    ]);

    await click(daemon, 'Show Notifications');
    await gesture(daemon, () => fireEvent.click(row('Codex 0.163.0 is available')));
    expect(panel().getByRole('button', { name: 'Installing…' })).toBeDisabled();

    await gesture(daemon, () => daemon.replyTo(install, { event: 'install_codex_update_result', success: true, profile_id: daemon.arrangement.profile.id }));
    expect(panel().getByRole('button', { name: 'Install' })).toBeEnabled();
    expect(daemon.sentOf('install_codex_update')).toHaveLength(1);
  });

  it('offers no Retry the daemon did not supply, even for a task’s notification', async () => {
    const daemon = await openNotifications([notification({ source_kind: 'task', source_id: 'task-1', actions: [] })]);

    await gesture(daemon, () => fireEvent.click(row('Something happened')));

    expect(panel().queryByRole('button', { name: 'Retry' })).toBeNull();
  });

  it('shows none of the previous profile’s notifications after a switch, while the new list loads or when it fails', async () => {
    const side = defaultProfile('side-desktop', { id: 'profile-side', name: 'Side' });
    const { daemon } = await renderApp({
      initialState: {
        profiles: [defaultProfile('desktop-1'), side],
        desktops: [emptyDesktop('desktop-1'), emptyDesktop('side-desktop', { profile_id: side.id })],
      },
    });
    const defaultConflict = notification({ id: 'default-conflict', title: 'Default’s conflict', body: '/default/notes.md' });
    daemon.on('notification_list', () =>
      daemon.arrangement.selectedProfileId === DEFAULT_PROFILE_ID
        ? { event: 'notification_list_result', success: true, notifications: [defaultConflict], unread_count: 1, unread_critical_count: 0 }
        : undefined,
    );
    await click(daemon, 'Show Notifications');
    expect(row('Default’s conflict')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.click(panel().getByRole('button', { name: 'Close' })));

    await openActionMenu(daemon);
    await gesture(daemon, () => fireEvent.change(screen.getByRole('combobox'), { target: { value: '>Switch to Side' } }));
    await gesture(daemon, () => fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' }));
    expect(daemon.sentOf('profile_select').pop()?.profile_id).toBe(side.id);

    await click(daemon, 'Show Notifications');
    const sideList = daemon.sentOf('notification_list').pop()!;
    expect(panel().getByText('Loading…')).toBeInTheDocument();
    expect(panel().queryByText('Default’s conflict')).toBeNull();

    await gesture(daemon, () => daemon.replyTo(sideList, {
      event: 'notification_list_result', success: false, error: 'temporary list failure', unread_count: 0, unread_critical_count: 0,
    }));
    expect(panel().getByText('temporary list failure')).toBeInTheDocument();
    expect(panel().queryByText('Default’s conflict')).toBeNull();
    expect(daemon.sentOf('notification_mark_read')).toEqual([]);
  });
});
