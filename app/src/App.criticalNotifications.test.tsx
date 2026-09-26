import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';

function renderWithAnAgent() {
  return renderApp({
    initialState: { sessions: [daemonSession('s1')], workspaces: [agentWorkspace('s1')] },
  });
}

function criticalStrip() {
  return screen.queryByRole('button', { name: /unread critical notification/ });
}

describe('critical notifications in the sidebar', () => {
  it('names the newest critical notification the daemon broadcasts', async () => {
    const { daemon } = await renderWithAnAgent();
    expect(criticalStrip()).toBeNull();

    daemon.emit({
      event: 'notifications_updated',
      unread_count: 4,
      unread_critical_count: 1,
      critical_title: 'Plugin stopped',
    });
    expect(criticalStrip()).toHaveAccessibleName(
      '1 unread critical notification: Plugin stopped. Open notifications.',
    );

    daemon.emit({
      event: 'notifications_updated',
      unread_count: 5,
      unread_critical_count: 2,
      critical_title: 'App runtime parked',
    });
    expect(criticalStrip()).toHaveAccessibleName(
      '2 unread critical notifications, newest: App runtime parked. Open notifications.',
    );
  });

  it('clears once the last critical notification is read', async () => {
    const { daemon } = await renderWithAnAgent();
    daemon.emit({
      event: 'notifications_updated',
      unread_count: 4,
      unread_critical_count: 2,
      critical_title: 'Plugin stopped',
    });
    expect(criticalStrip()).not.toBeNull();

    daemon.emit({ event: 'notifications_updated', unread_count: 2, unread_critical_count: 0 });

    expect(criticalStrip()).toBeNull();
  });

  it('shows a count only when there is more than one', async () => {
    const { daemon } = await renderWithAnAgent();

    daemon.emit({ event: 'notifications_updated', unread_count: 1, unread_critical_count: 1, critical_title: 'Plugin stopped' });
    expect(criticalStrip()).toHaveTextContent(/^Plugin stopped$/);

    daemon.emit({ event: 'notifications_updated', unread_count: 3, unread_critical_count: 3, critical_title: 'Plugin stopped' });
    expect(criticalStrip()).toHaveTextContent(/^Plugin stopped3$/);
  });

  it('names a critical notification without a title generically', async () => {
    const { daemon } = await renderWithAnAgent();

    daemon.emit({ event: 'notifications_updated', unread_count: 1, unread_critical_count: 1, critical_title: '' });

    expect(criticalStrip()).toHaveAccessibleName('1 unread critical notification: Critical notification. Open notifications.');
  });

  it('opens the notifications when clicked', async () => {
    const { daemon } = await renderWithAnAgent();
    daemon.on('notification_list', () => ({ event: 'notification_list_result', success: true, notifications: [], unread_count: 0, unread_critical_count: 0 }));
    daemon.emit({ event: 'notifications_updated', unread_count: 1, unread_critical_count: 1, critical_title: 'Plugin stopped' });

    const listed = daemon.sentOf('notification_list').length;

    await gesture(daemon, () => fireEvent.click(criticalStrip()!));

    expect(screen.getByRole('dialog', { name: 'Notifications' })).toBeInTheDocument();
    expect(daemon.sentOf('notification_list')).toHaveLength(listed + 1);
  });
});
