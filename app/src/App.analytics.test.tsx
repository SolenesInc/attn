import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { daemonSession, soloDesktop, type DaemonSession } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const OWED = { state: 'waiting_input', turn_owed: true } as const;
const SETTLED = { state: 'idle', turn_owed: false } as const;

function agents(turns: Record<string, Partial<DaemonSession>>) {
  return Object.entries(turns).map(([id, overrides], index) =>
    daemonSession(id, { turn_opened_at: `2026-08-03T${String(9 + index).padStart(2, '0')}:00:00Z`, ...overrides }));
}

async function useTheApp(daemon: ScriptedDaemon) {
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s1' })));
  vi.setSystemTime(new Date('2026-08-03T12:00:00Z'));
  await gesture(daemon, () => daemon.emit({ event: 'sessions_updated', sessions: agents({ s1: SETTLED, s2: OWED }) }));
  await gesture(daemon, () => pressShortcut('ui.openSettings'));
  await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-nav-desktop')));
}

async function launch(analytics: 'true' | 'false') {
  const sessions = agents({ s1: OWED, s2: OWED });
  return renderApp({
    initialState: {
      sessions,
      desktops: sessions.map((session) => soloDesktop(session.id)),
      settings: { queue_mode_enabled: 'true', 'analytics.enabled': analytics },
    },
  });
}

describe('app actions in the analytics outbox', () => {
  it('sends nothing while analytics is off', async () => {
    const { daemon } = await launch('false');
    await useTheApp(daemon);
    expect(screen.getByTestId('settings-section-desktop')).toBeInTheDocument();
    expect(daemon.sentOf('record_analytics')).toEqual([]);
  });

  it('records shortcuts, settings sections and queue advances with their wait while analytics is on', async () => {
    const { daemon } = await launch('true');
    await useTheApp(daemon);
    expect(daemon.sentOf('record_analytics')).toEqual([
      { cmd: 'record_analytics', stream: 'app', name: 'queue_advance', props: { reason: 'turn_closed', to: 'session', wait_ms: 2 * 60 * 60 * 1000 } },
      { cmd: 'record_analytics', stream: 'app', name: 'shortcut', props: { id: 'ui.openSettings' } },
      { cmd: 'record_analytics', stream: 'app', name: 'settings_section', props: { section: 'connectivity' } },
      { cmd: 'record_analytics', stream: 'app', name: 'settings_section', props: { section: 'desktop' } },
    ]);
  });
});
