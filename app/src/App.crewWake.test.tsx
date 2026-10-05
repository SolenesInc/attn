import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { WAKE_ARM_TIMEOUT_MS } from './components/CrewWake';
import { soloDesktop, crewMember, daemonSession, type DaemonSession } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const keel = crewMember('keel');

function renderCrewQueue(keelDay: Partial<DaemonSession>) {
  return renderApp({
    initialState: {
      settings: { queue_mode_enabled: 'true' },
      crew: [keel],
      sessions: [daemonSession('s1'), daemonSession('sess-keel', keelDay)],
      desktops: [soloDesktop('s1'), soloDesktop('sess-keel')],
    },
  });
}

async function wakeKeel(daemon: ScriptedDaemon) {
  const wake = screen.getByTestId('queue-crew-wake-keel');
  await gesture(daemon, () => fireEvent.click(wake));
  await gesture(daemon, () => fireEvent.click(wake));
}

async function askKeelToSleep(daemon: ScriptedDaemon) {
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Ask Keel to sleep' })));
}

function shownDesktops() {
  return Array.from(document.querySelectorAll('.session-terminal-desktop[data-session-visible="1"]'))
    .map((desktop) => desktop.getAttribute('data-desktop-id'));
}

describe('App crew wake and sleep', () => {
  it('wakes a sleeping member and focuses the day the daemon names', async () => {
    const { daemon } = await renderCrewQueue({ label: 'keel day' });
    daemon.on('crew_wake', () => ({
      event: 'crew_wake_result', success: true, member: 'keel', session_id: 'sess-keel',
    }));

    await wakeKeel(daemon);

    expect(daemon.sentOf('crew_wake')).toEqual([expect.objectContaining({ member: 'keel' })]);
    expect(shownDesktops()).toEqual(['desktop-sess-keel']);
  });

  it('reports a show error separately from a successful wake', async () => {
    const { daemon } = await renderCrewQueue({ label: 'keel day' });
    daemon.on('crew_wake', () => ({
      event: 'crew_wake_result', success: true, member: 'keel', session_id: 'sess-keel',
      show_error: 'Session closed before it could be shown',
    }));

    await wakeKeel(daemon);

    expect(screen.getByRole('alert')).toHaveTextContent('Member is awake, but showing it failed: Session closed before it could be shown');
    expect(daemon.sentOf('crew_wake')).toHaveLength(1);
  });

  it('shows what the daemon said when it refuses a wake', async () => {
    const { daemon } = await renderCrewQueue({ label: 'keel day' });
    daemon.on('crew_wake', () => ({
      event: 'crew_wake_result', success: false, error: 'keel launches in /gone, which is not there',
    }));

    await wakeKeel(daemon);

    expect(screen.getByRole('alert')).toHaveTextContent('keel launches in /gone, which is not there');
    expect(shownDesktops()).toEqual([]);
  });

  it('asks an awake member to sleep', async () => {
    const { daemon } = await renderCrewQueue({ crew_member: 'keel' });
    daemon.on('crew_sleep', () => ({
      event: 'crew_sleep_result', success: true, member: 'keel', session_id: 'sess-keel', delivery_status: 'notified',
    }));

    await askKeelToSleep(daemon);

    expect(daemon.sentOf('crew_sleep')).toEqual([expect.objectContaining({ member: 'keel' })]);
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('shows what the daemon said when it refuses a sleep request', async () => {
    const { daemon } = await renderCrewQueue({ crew_member: 'keel' });
    daemon.on('crew_sleep', () => ({
      event: 'crew_sleep_result', success: false, error: 'session sess-keel cannot receive agent messages',
    }));

    await askKeelToSleep(daemon);

    expect(screen.getByRole('alert')).toHaveTextContent('session sess-keel cannot receive agent messages');
  });
});

const ROSTER = [crewMember('alder'), crewMember('keel'), crewMember('trellis')];

function renderRoster({ days = [] as DaemonSession[], settings = { queue_mode_enabled: 'true' } as Record<string, string> } = {}) {
  const sessions = [daemonSession('s1', { state: 'idle' }), ...days];
  return renderApp({
    initialState: { settings, crew: ROSTER, sessions, desktops: sessions.map((session) => soloDesktop(session.id)) },
  });
}

const keelDay = (overrides: Partial<DaemonSession> = {}) =>
  daemonSession('sess-keel', { label: 'keel of the day', crew_member: 'keel', ...overrides });

function queueRows(): string[] {
  return Array.from(screen.getByTestId('sidebar-queue').querySelectorAll('.queue-row'))
    .map((row) => row.getAttribute('data-testid')!);
}

const crewRow = (member: string) => screen.getByTestId(`queue-crew-${member}`);
const armed = (member: string) => crewRow(member).getAttribute('data-crew-wake');
const sun = (member: string) => screen.getByTestId(`queue-crew-wake-${member}`);

async function rosterWithWake() {
  const view = await renderRoster();
  view.daemon.on('crew_wake', ({ member }) => ({
    event: 'crew_wake_result', success: true, member, session_id: `sess-${member}`,
  }));
  return view;
}

describe('App crew in the queue', () => {
  it('draws every member in the crew block by name, awake or asleep, with crew management above them', async () => {
    await renderRoster({ days: [keelDay()] });

    expect(queueRows().filter((id) => id.startsWith('queue-crew-')))
      .toEqual(['queue-crew-alder', 'queue-crew-keel', 'queue-crew-trellis']);
    expect(screen.getByTestId('manage-crew')).toHaveTextContent('manage');
    expect(crewRow('keel')).toHaveAttribute('data-crew-state', 'awake');
    expect(crewRow('alder')).toHaveAttribute('data-crew-state', 'asleep');
    expect(crewRow('trellis')).toHaveTextContent('Trellis');
    expect(crewRow('trellis')).not.toHaveTextContent('trellis');
    expect(sun('trellis')).toHaveAccessibleName('Wake Trellis');
    expect(screen.queryByTestId('queue-crew-wake-keel')).toBeNull();
    expect(screen.queryByTestId('queue-crew-sleep-trellis')).toBeNull();
  });

  it('shows an awake member once, on its own row, even while its turn is owed', async () => {
    await renderRoster({ days: [keelDay({ turn_owed: true, turn_opened_at: '2026-07-26T08:00:00Z' })] });

    expect(queueRows().filter((id) => id.includes('keel'))).toEqual(['queue-crew-keel']);
  });

  it('queues an awake member owing a turn when crew joins the queue, and keeps every crew row', async () => {
    await renderRoster({
      days: [keelDay({ turn_owed: true, turn_opened_at: '2026-07-26T08:00:00Z' })],
      settings: { queue_mode_enabled: 'true', queue_crew_enabled: 'true' },
    });

    expect(screen.getByTestId('queue-turn-sess-keel')).toBeInTheDocument();
    expect(crewRow('keel')).toHaveAttribute('data-crew-state', 'awake');
    expect(crewRow('alder')).toHaveAttribute('data-crew-state', 'asleep');
  });

  it('still draws a day whose member left the roster', async () => {
    await renderRoster({ days: [daemonSession('sess-ghost', { crew_member: 'sable' })] });

    expect(crewRow('sable')).toHaveAttribute('data-crew-state', 'awake');
  });

  it('shows sleeping crew above desktops while the queue is off', async () => {
    await renderApp({ initialState: {
      crew: [crewMember('alder'), crewMember('keel', { binding_session: 'sess-keel' })],
      sessions: [keelDay()], desktops: [soloDesktop('sess-keel')],
    } });

    expect(crewRow('alder')).toHaveAttribute('data-crew-state', 'asleep');
    expect(screen.queryByTestId('queue-crew-keel')).toBeNull();
    expect(screen.getByTestId('sidebar-session-sess-keel')).toBeInTheDocument();
  });

  it('focuses an awake member’s day from its row', async () => {
    const { daemon } = await renderRoster({ days: [keelDay()] });

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('queue-crew-select-keel')));

    expect(shownDesktops()).toEqual(['desktop-sess-keel']);
  });

  it.each([
    ['sleeping', 'alder', 'crew-actions-alder', 'Alder'],
    ['awake', 'keel', 'session-actions-sess-keel', 'Keel'],
  ])('opens a %s member’s details from its row', async (_, _member, trigger, name) => {
    const { daemon } = await renderRoster({ days: [keelDay()] });

    fireEvent.click(screen.getByTestId(trigger));
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('crew-member-details-action')));

    expect(within(screen.getByTestId('crew-panel')).getByRole('heading', { name })).toBeInTheDocument();
  });

  describe('arming a wake', () => {
    it('asks for a second click, and says so in words', async () => {
      const { daemon } = await rosterWithWake();

      await gesture(daemon, () => fireEvent.click(sun('trellis')));

      expect(daemon.sentOf('crew_wake')).toEqual([]);
      expect(armed('trellis')).toBe('armed');
      expect(sun('trellis')).toHaveAccessibleName('Wake Trellis — click again to confirm');
      expect(screen.getByTestId('queue-crew-select-trellis')).toHaveAccessibleName('Wake Trellis — click again to confirm');
      expect(crewRow('trellis')).toHaveTextContent('confirm');
    });

    it('arms from the row and confirms on the sun, as one gesture', async () => {
      const { daemon } = await rosterWithWake();

      await gesture(daemon, () => fireEvent.click(screen.getByTestId('queue-crew-select-trellis')));
      expect(daemon.sentOf('crew_wake')).toEqual([]);
      await gesture(daemon, () => fireEvent.click(sun('trellis')));

      expect(daemon.sentOf('crew_wake')).toEqual([expect.objectContaining({ member: 'trellis' })]);
    });

    it('wakes once however often the breaking sun is clicked', async () => {
      const { daemon } = await rosterWithWake();

      for (let click = 0; click < 4; click += 1) fireEvent.click(sun('trellis'));
      await daemon.idle();

      expect(daemon.sentOf('crew_wake')).toEqual([expect.objectContaining({ member: 'trellis' })]);
    });

    it('keeps the arm while focus moves within its own row', async () => {
      const { daemon } = await rosterWithWake();

      fireEvent.click(screen.getByTestId('queue-crew-select-trellis'));
      fireEvent.focusIn(sun('trellis'));
      await gesture(daemon, () => fireEvent.click(sun('trellis')));

      expect(daemon.sentOf('crew_wake')).toEqual([expect.objectContaining({ member: 'trellis' })]);
    });

    it.each([
      ['a press lands elsewhere', () => fireEvent.pointerDown(document.body)],
      ['another member is pressed', () => fireEvent.pointerDown(sun('alder'))],
      ['focus moves to another member', () => fireEvent.focusIn(sun('alder'))],
      ['the user presses Escape', () => fireEvent.keyDown(document, { key: 'Escape' })],
      ['nobody confirms in time', () => act(() => { vi.advanceTimersByTime(WAKE_ARM_TIMEOUT_MS); })],
    ])('stands down, waking nobody, when %s', async (_, standDown) => {
      const { daemon } = await rosterWithWake();
      fireEvent.click(sun('trellis'));

      standDown();

      expect(armed('trellis')).toBeNull();
      await gesture(daemon, () => fireEvent.click(sun('trellis')));
      expect(daemon.sentOf('crew_wake')).toEqual([]);
    });

    it('holds the arm until just before the timeout', async () => {
      await rosterWithWake();
      fireEvent.click(sun('trellis'));

      act(() => { vi.advanceTimersByTime(WAKE_ARM_TIMEOUT_MS - 1); });

      expect(armed('trellis')).toBe('armed');
    });
  });
});
