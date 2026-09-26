import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { agentWorkspace, daemonSession, type DaemonSession } from './test/daemonFixtures';
import type { EventMessage } from './test/protocol';
import { gesture, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

type InitialState = EventMessage<'initial_state'>;

const QUEUE = { queue_mode_enabled: 'true' };
const HOUR = 60 * 60 * 1000;

function agent(id: string, overrides: Partial<DaemonSession> = {}): DaemonSession {
  return daemonSession(id, { label: id, ...overrides });
}

function renderHome(sessions: DaemonSession[], initial: Partial<InitialState> = {}) {
  return renderApp({ initialState: { sessions, workspaces: sessions.map((session) => agentWorkspace(session.id)), ...initial } });
}

const group = (name: string) => screen.queryByTestId(`session-group-${name}`);
const row = (id: string) => screen.queryByTestId(`session-${id}`);

function rowsIn(name: string): string[] {
  return Array.from(group(name)!.querySelectorAll('[data-testid^="session-"][data-state]'))
    .map((element) => element.getAttribute('data-testid')!.replace(/^session-/, ''));
}

const fromNow = (ms: number) => new Date(Date.now() + ms).toISOString();

function tomorrowAt(hours: number, minutes: number): string {
  const at = new Date();
  at.setDate(at.getDate() + 1);
  at.setHours(hours, minutes, 0, 0);
  return at.toISOString();
}

describe('App dashboard', () => {
  it('groups sessions by what they are doing', async () => {
    await renderHome([
      agent('busy', { state: 'working' }),
      agent('asking', { state: 'pending_approval' }),
      agent('also-asking', { state: 'pending_approval' }),
      agent('loop', { state: 'scheduled' }),
      agent('restartable', { state: 'recoverable' }),
    ]);

    expect(rowsIn('working')).toEqual(['busy']);
    expect(rowsIn('pending').sort()).toEqual(['also-asking', 'asking']);
    expect(rowsIn('scheduled')).toEqual(['loop']);
    expect(rowsIn('recoverable')).toEqual(['restartable']);
  });

  it('shows where a session came from and where it runs', async () => {
    await renderHome(
      [
        agent('review-1', {
          label: 'feed-nexus-web#101 · gpt-5.6-sol',
          automation: {
            run_id: 'run-1',
            definition_id: 'review-sol',
            definition_name: 'Requested PR review - GPT Sol medium',
            trigger_type: 'github_review_requested',
            pull_request: {
              repository: 'ghe.example.net/audiobook/feed-nexus-web',
              number: 101,
              url: 'https://ghe.example.net/audiobook/feed-nexus-web/pull/101',
              title: 'Fix validation race',
              head_sha: '82f1c7a000000000000000000000000000000000',
            },
          },
        }),
        agent('remote', { endpoint_id: 'ep-1' }),
      ],
      { endpoints: [{ id: 'ep-1', name: 'gpu-box', ssh_target: 'user@gpu-box', status: 'connected', enabled: true }] },
    );

    expect(row('review-1')).toHaveTextContent('GPT Sol medium');
    expect(row('review-1')).toHaveTextContent('feed-nexus-web#101');
    expect(row('review-1')).toHaveTextContent('Fix validation race');
    expect(row('remote')).toHaveTextContent('gpu-box');
  });

  it('marks the chief of staff and takes the user to it from its summary', async () => {
    const { daemon } = await renderHome([
      agent('chief-1', { label: 'planner', state: 'waiting_input', chief_of_staff: true }),
      agent('worker-1', { label: 'parser-worker' }),
    ]);
    expect(within(row('chief-1')!).getByLabelText('Chief of staff')).toBeInTheDocument();
    const summary = screen.getByTestId('chief-session-summary');
    expect(summary).toHaveTextContent('Session: waiting input');

    await gesture(daemon, () => fireEvent.click(within(summary).getByRole('button', { name: /planner/ })));

    expect(daemon.sentOf('session_selected').pop()).toEqual({ cmd: 'session_selected', id: 'chief-1' });
  });

  it('asks for a chief when none is set', async () => {
    await renderHome([agent('worker')]);

    expect(screen.getByText('Assign a session as chief to track delegated work.')).toBeInTheDocument();
    expect(screen.queryByTestId('chief-session-summary')).toBeNull();
  });

  describe('activity', () => {
    it('shows what the agent is doing beside its name, and marks it stale once it stops being refreshed', async () => {
      await renderHome([agent('s1', { activity: 'running the frontend test suite', activity_at: fromNow(0) })]);
      const line = screen.getByTestId('session-activity-s1');
      expect(line).toHaveTextContent('running the frontend test suite');
      expect(line).not.toHaveAttribute('data-stale');

      await act(() => vi.advanceTimersByTimeAsync(16 * 60 * 1000));

      expect(screen.getByTestId('session-activity-s1')).toHaveAttribute('data-stale', 'true');
    });

    it('takes the staleness window from the configured cadence', async () => {
      await renderHome(
        [agent('s1', { activity: 'running the frontend test suite', activity_at: fromNow(0) })],
        { settings: { 'activity.intervals': '{"watching":1800,"present":1800}' } },
      );

      await act(() => vi.advanceTimersByTimeAsync(20 * 60 * 1000));

      expect(screen.getByTestId('session-activity-s1')).not.toHaveAttribute('data-stale');
    });

    it('shows no line for a session without activity', async () => {
      await renderHome([agent('s1')]);

      expect(screen.queryByTestId('session-activity-s1')).toBeNull();
    });
  });

  describe('in queue mode', () => {
    it('leads with the turns owed, oldest first, and keeps them out of the state groups', async () => {
      await renderHome(
        [
          agent('newer', { state: 'waiting_input', turn_owed: true, turn_opened_at: '2026-07-29T10:05:00Z' }),
          agent('older', { state: 'pending_approval', turn_owed: true, turn_opened_at: '2026-07-29T09:00:00Z' }),
          agent('settled-but-waiting', { state: 'waiting_input', turn_owed: false }),
        ],
        { settings: QUEUE },
      );

      expect(rowsIn('turns')).toEqual(['older', 'newer']);
      expect(rowsIn('waiting')).toEqual(['settled-but-waiting']);
      expect(group('settled')).not.toBeNull();
      expect(screen.queryByTestId('all-settled')).toBeNull();
    });

    it('keeps automation and crew sessions out of the turns unless crew joins the queue', async () => {
      const automation = { run_id: 'run-1', definition_id: 'review-sol', definition_name: 'Review with Sol', trigger_type: 'schedule' };
      const sessions = [
        agent('automation-owed', { state: 'waiting_input', turn_owed: true, turn_opened_at: '2026-07-29T08:00:00Z', automation }),
        agent('automation-settled', { state: 'working', automation: { ...automation, run_id: 'run-2' } }),
        agent('crew-owed', { state: 'waiting_input', crew_member: 'keel', turn_owed: true, turn_opened_at: '2026-07-29T09:00:00Z' }),
        agent('crew-settled', { state: 'working', crew_member: 'marlowe' }),
        agent('ordinary', { state: 'waiting_input', turn_owed: true, turn_opened_at: '2026-07-29T10:00:00Z' }),
      ];
      const { daemon } = await renderHome(sessions, { settings: QUEUE });

      expect(rowsIn('turns')).toEqual(['ordinary']);
      expect(rowsIn('waiting')).toEqual(['automation-owed', 'crew-owed']);
      expect(rowsIn('working')).toEqual(['automation-settled', 'crew-settled']);

      daemon.emit({ event: 'settings_updated', settings: { ...QUEUE, queue_crew_enabled: 'true' } });

      expect(rowsIn('turns')).toEqual(['crew-owed', 'ordinary']);
      expect(rowsIn('waiting')).toEqual(['automation-owed']);
      expect(rowsIn('working')).toEqual(['automation-settled', 'crew-settled']);
    });

    it('marks only the state groups as describing what an agent is doing', async () => {
      await renderHome(
        [
          agent('owed', { state: 'waiting_input', turn_owed: true, turn_opened_at: '2026-07-29T09:00:00Z' }),
          agent('busy', { state: 'working' }),
          agent('deferred', { state: 'working', turn_snoozed_until: fromNow(HOUR) }),
        ],
        { settings: QUEUE },
      );

      expect(Array.from(document.querySelectorAll('[data-session-group="state"] [data-state]'), (element) => element.getAttribute('data-testid')))
        .toEqual(['session-busy']);
      expect(group('turns')).not.toHaveAttribute('data-session-group');
      expect(group('snoozed')).not.toHaveAttribute('data-session-group');
    });

    it('announces all settled with what is still running once nothing is owed', async () => {
      await renderHome(
        [agent('busy', { state: 'working' }), agent('busy-too', { state: 'working' }), agent('later', { state: 'scheduled' })],
        { settings: QUEUE },
      );

      expect(screen.getByTestId('all-settled')).toHaveTextContent('2 working · 1 scheduled');
      expect(group('turns')).toBeNull();
    });

    it('says nothing is running when everything settled is parked', async () => {
      await renderHome([agent('parked', { state: 'idle' })], { settings: QUEUE });

      expect(screen.getByTestId('all-settled')).toHaveTextContent('Nothing is running.');
    });

    it('leaves the chief out of the turns', async () => {
      await renderHome(
        [agent('chief', { state: 'waiting_input', chief_of_staff: true, turn_owed: true, turn_opened_at: '2026-07-29T09:00:00Z' })],
        { settings: QUEUE },
      );

      expect(group('turns')).toBeNull();
      expect(screen.getByTestId('all-settled')).toBeInTheDocument();
    });

    it('keeps the plain state grouping when the queue is off', async () => {
      await renderHome([agent('asking', { state: 'waiting_input', turn_owed: true, turn_opened_at: '2026-07-29T09:00:00Z' })]);

      expect(group('turns')).toBeNull();
      expect(group('settled')).toBeNull();
      expect(screen.queryByTestId('all-settled')).toBeNull();
      expect(rowsIn('waiting')).toEqual(['asking']);
    });
  });

  describe('following the next turn', () => {
    const followSwitch = () => within(screen.getByTestId('follow-next-turn')).getByRole('checkbox');

    async function workTheQueueDownToHome() {
      const rendered = await renderHome(
        [agent('s1', { state: 'waiting_input', turn_owed: true, turn_opened_at: '2026-07-29T09:00:00Z' }), agent('s2', { state: 'working' })],
        { settings: QUEUE },
      );
      await gesture(rendered.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s1' })));
      await gesture(rendered.daemon, () => rendered.daemon.emit({ event: 'session_state_changed', session: agent('s1', { state: 'waiting_input', turn_owed: false }) }));
      expect(screen.getByTestId('sidebar-home')).toHaveAttribute('aria-current', 'page');
      return rendered.daemon;
    }

    function owe(daemon: ScriptedDaemon, id: string) {
      return gesture(daemon, () => daemon.emit({ event: 'session_state_changed', session: agent(id, { state: 'waiting_input', turn_owed: true, turn_opened_at: '2026-07-29T11:00:00Z' }) }));
    }

    it('shows that home is waiting for the next turn, and stops waiting when switched off', async () => {
      const daemon = await workTheQueueDownToHome();
      expect(followSwitch()).toBeChecked();

      fireEvent.click(followSwitch());
      expect(followSwitch()).not.toBeChecked();
      await owe(daemon, 's2');

      expect(screen.getByTestId('sidebar-home')).toHaveAttribute('aria-current', 'page');
    });

    it('is off, and still offered, when the user walked home themselves', async () => {
      await renderHome([agent('s1', { state: 'working' })], { settings: QUEUE });

      expect(followSwitch()).not.toBeChecked();
    });

    it('is absent while a turn is owed', async () => {
      await renderHome(
        [agent('s1', { state: 'waiting_input', turn_owed: true, turn_opened_at: '2026-07-29T09:00:00Z' })],
        { settings: QUEUE },
      );

      expect(screen.queryByTestId('follow-next-turn')).toBeNull();
    });
  });

  describe('snoozed agents', () => {
    it('collects deferred agents under a collapsed Snoozed group that lists each wake time, soonest first', async () => {
      await renderHome(
        [
          agent('wakes-later', { state: 'idle', turn_snoozed_until: tomorrowAt(10, 15) }),
          agent('wakes-sooner', { state: 'working', turn_snoozed_until: tomorrowAt(9, 30) }),
          agent('running', { state: 'working' }),
        ],
        { settings: QUEUE },
      );
      expect(rowsIn('working')).toEqual(['running']);
      expect(group('snoozed')).toHaveTextContent('Snoozed2');
      expect(row('wakes-later')).toBeNull();

      fireEvent.click(within(group('snoozed')!).getByRole('button', { name: /Snoozed/ }));

      expect(rowsIn('snoozed')).toEqual(['wakes-sooner', 'wakes-later']);
      expect(row('wakes-sooner')).toHaveTextContent(/tomorrow 0?9:30/);
      expect(row('wakes-later')).toHaveTextContent(/tomorrow 10:15/);
      expect(within(group('snoozed')!).getByRole('button', { name: 'Wake wakes-sooner' })).toBeInTheDocument();
    });

    it('wakes one early, with the queue off too', async () => {
      const { daemon } = await renderHome([agent('deferred', { state: 'idle', turn_snoozed_until: fromNow(HOUR) })]);
      expect(group('idle')).toBeNull();
      fireEvent.click(within(group('snoozed')!).getByRole('button', { name: /Snoozed/ }));

      await gesture(daemon, () => fireEvent.click(within(group('snoozed')!).getByRole('button', { name: 'Wake deferred' })));

      expect(daemon.sentOf('wake_turn')).toEqual([{ cmd: 'wake_turn', session_id: 'deferred' }]);
    });

    it('collects a deferred chief too', async () => {
      await renderHome([agent('chief', { state: 'idle', chief_of_staff: true, turn_snoozed_until: fromNow(HOUR) })], { settings: QUEUE });

      fireEvent.click(within(group('snoozed')!).getByRole('button', { name: /Snoozed/ }));

      expect(rowsIn('snoozed')).toEqual(['chief']);
    });

    it('leaves a lapsed deadline alone', async () => {
      await renderHome([agent('awake', { state: 'working', turn_snoozed_until: fromNow(-60 * 1000) })], { settings: QUEUE });

      expect(group('snoozed')).toBeNull();
      expect(rowsIn('working')).toEqual(['awake']);
    });
  });
});
