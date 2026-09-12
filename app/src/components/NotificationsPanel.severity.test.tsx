import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { NotificationsPanel } from './NotificationsPanel';
import type { DaemonNotification } from '../hooks/useDaemonSocket';
import { NotificationSeverity } from '../types/generated';

function notification(over: Partial<DaemonNotification>): DaemonNotification {
  return {
    id: 'n1',
    kind: 'task_failed',
    severity: NotificationSeverity.Info,
    title: 'Something happened',
    body: 'body',
    detail: '',
    trigger: '',
    impact: '',
    cause: '',
    diagnostic: '',
    source_kind: '',
    source_id: '',
    created_at: new Date().toISOString(),
    read_at: '',
    ...over,
  } as DaemonNotification;
}

function renderPanel(notifications: DaemonNotification[]) {
  const listNotifications = vi.fn().mockResolvedValue({
    notifications,
    unreadCount: notifications.filter((n) => !n.read_at).length,
    critical: { count: 0, title: '' },
  });
  const retryTask = vi.fn().mockResolvedValue(null);
  const onOpenSession = vi.fn();
  const onClose = vi.fn();
  render(
    <NotificationsPanel
      open
      onClose={onClose}
      listNotifications={listNotifications}
      markRead={vi.fn().mockResolvedValue(0)}
      retryTask={retryTask}
      onOpenSession={onOpenSession}
      changeSignal={0}
    />,
  );
  return { listNotifications, retryTask, onOpenSession, onClose };
}

function rowFor(title: string): HTMLElement {
  const row = screen.getByText(title).closest('li');
  if (!row) throw new Error(`no row for ${title}`);
  return row;
}

describe('NotificationsPanel severity', () => {
  it('styles each row by its severity', async () => {
    renderPanel([
      notification({ id: 'a', severity: NotificationSeverity.Critical, title: 'Plugin stopped' }),
      notification({ id: 'b', severity: NotificationSeverity.Warning, title: 'Ticket reconciliation failed' }),
      notification({ id: 'c', severity: NotificationSeverity.Info, title: 'Compaction finished' }),
    ]);

    await waitFor(() => expect(screen.getByText('Plugin stopped')).toBeInTheDocument());

    expect(rowFor('Plugin stopped')).toHaveClass('sev-critical');
    expect(rowFor('Ticket reconciliation failed')).toHaveClass('sev-warning');
    expect(rowFor('Compaction finished')).toHaveClass('sev-info');
  });

  it('treats an unrecognized severity as info rather than leaving a row unstyled', async () => {
    renderPanel([
      notification({ id: 'a', severity: 'catastrophic' as DaemonNotification['severity'], title: 'From the future' }),
    ]);

    await waitFor(() => expect(screen.getByText('From the future')).toBeInTheDocument());

    expect(rowFor('From the future')).toHaveClass('sev-info');
  });

  it('keeps severity on a row after it is read', async () => {
    renderPanel([
      notification({
        id: 'a',
        severity: NotificationSeverity.Critical,
        title: 'Plugin stopped',
        read_at: new Date().toISOString(),
      }),
    ]);

    await waitFor(() => expect(screen.getByText('Plugin stopped')).toBeInTheDocument());

    const row = rowFor('Plugin stopped');
    expect(row).toHaveClass('sev-critical');
    expect(row).not.toHaveClass('is-unread');
  });
});

describe('NotificationsPanel failure details', () => {
  it('shows structured evidence and only the actions supplied by the daemon', async () => {
    const user = userEvent.setup();
    const rendered = renderPanel([
      notification({
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

    await user.click(await screen.findByText('Couldn’t update activity for “ci stuff”'));
    expect(screen.getByText('New output triggered a Home activity summary.')).toBeInTheDocument();
    expect(screen.getByText('The Home activity summary may be stale.')).toBeInTheDocument();
    expect(screen.getByText('The agent process exited with status 2.')).toBeInTheDocument();
    await user.click(screen.getByText('Diagnostic output'));
    expect(screen.getByText('stderr: authentication failed')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Retry' }));
    expect(rendered.retryTask).toHaveBeenCalledWith('task-1');
    await user.click(screen.getByRole('button', { name: 'Open session' }));
    expect(rendered.onClose).toHaveBeenCalled();
    expect(rendered.onOpenSession).toHaveBeenCalledWith('session-1');
  });

  it('does not infer Retry from a task source', async () => {
    const user = userEvent.setup();
    renderPanel([notification({ source_kind: 'task', source_id: 'task-1', actions: [] })]);
    await user.click(await screen.findByText('Something happened'));
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
  });
});
