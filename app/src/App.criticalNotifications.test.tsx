import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';

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
});
