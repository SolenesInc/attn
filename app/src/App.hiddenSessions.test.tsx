import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { daemonSession, soloDesktop } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';

const OWED = '2026-08-03T10:00:00Z';

async function launch(settings: Record<string, string> = {}) {
  const sessions = [
    daemonSession('s1', { state: 'idle', turn_owed: true, turn_opened_at: '2026-08-03T11:00:00Z' }),
    daemonSession('h1', { state: 'idle', turn_owed: true, turn_opened_at: OWED, hidden: true }),
  ];
  return renderApp({
    initialState: { settings: { queue_mode_enabled: 'true', ...settings }, sessions, desktops: [soloDesktop('s1')] },
  });
}

describe('hidden sessions in the queue', () => {
  it('queues a hidden session without a desktop chip and opens it in a tile when chosen', async () => {
    const { daemon } = await launch();
    const row = screen.getByTestId('queue-turn-h1');
    expect(row.querySelector('.queue-lead-badge')).toBeNull();
    expect(screen.getByTestId('queue-turn-s1').querySelector('.queue-lead-badge')).not.toBeNull();

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('queue-select-h1')));

    expect(daemon.sentOf('desktop_show_session')).toEqual([expect.objectContaining({ session_id: 'h1' })]);
    expect(document.querySelector('[data-session-visible="1"] [data-pane-session-id="h1"]')).not.toBeNull();
  });

  it('leaves hidden sessions out of the queue when the setting is off', async () => {
    await launch({ queue_show_hidden_sessions: 'false' });
    expect(screen.getByTestId('queue-turn-s1')).toBeInTheDocument();
    expect(screen.queryByTestId('queue-turn-h1')).toBeNull();
  });

  it('does not give a hidden session a place on the desktop strip', async () => {
    await launch({ queue_mode_enabled: 'false' });
    expect(screen.queryByTestId('queue-turn-h1')).toBeNull();
    expect(screen.queryByText('h1')).toBeNull();
  });
});
