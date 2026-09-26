import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import type { EventMessage } from './test/protocol';
import { gesture, renderApp } from './test/renderApp';
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

async function openNotifications(notifications: Notification[]) {
  const { daemon } = await renderApp({
    initialState: { sessions: [daemonSession('s1'), daemonSession('session-1')], workspaces: [agentWorkspace('s1'), agentWorkspace('session-1')] },
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
    expect(daemon.sentOf('session_selected').pop()).toEqual({ cmd: 'session_selected', id: 'session-1' });
  });

  it('offers no Retry the daemon did not supply, even for a task’s notification', async () => {
    const daemon = await openNotifications([notification({ source_kind: 'task', source_id: 'task-1', actions: [] })]);

    await gesture(daemon, () => fireEvent.click(row('Something happened')));

    expect(panel().queryByRole('button', { name: 'Retry' })).toBeNull();
  });
});
