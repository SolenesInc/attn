import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { gesture } from './test/renderApp';
import type { EventMessage } from './test/protocol';
import type { Reply, ScriptedDaemon } from './test/scriptedDaemon';
import { openSection } from './test/settings';

type BusStatus = EventMessage<'bus_status_result'>;
type Producer = BusStatus['producers'][number];
type Consumer = BusStatus['consumers'][number];

const DAY_MS = 86_400_000;

const producer = (over: Partial<Producer> = {}): Producer => ({
  name: 'session.state.changed',
  events: 232213,
  bytes: 17_900_000,
  subjects: 103,
  share: 0.737,
  recent_per_hour: 1701,
  baseline_per_hour: 1816,
  sustained_per_hour: 680,
  surging: true,
  surge_window_seconds: 86400,
  surge_per_hour: 1816,
  ...over,
});

const consumer = (over: Partial<Consumer> = {}): Consumer => ({
  name: 'notifier',
  cursor: 100,
  lag: 0,
  filter: 'session.*',
  enabled: true,
  updated_at: '2026-08-10T19:00:00Z',
  live: true,
  stalled: '',
  oldest_unread_at: '',
  holds_retention_floor: false,
  pin_alarm: false,
  pinned_bytes: 0,
  ...over,
});

const status = (over: Partial<BusStatus> = {}): Reply => ({
  event: 'bus_status_result',
  success: true,
  earliest: 1,
  head: 315188,
  rows: 315188,
  bytes: 23_815_114,
  oldest_at: '2026-08-02T10:24:55Z',
  newest_at: '2026-08-10T19:28:01Z',
  delivering: true,
  retention_seconds: 30 * 86400,
  recent_window_seconds: 3600,
  baseline_window_seconds: 86400,
  surge_rate_per_hour: 1000,
  pin_alarm_seconds: 3600,
  producers: [producer()],
  consumers: [],
  health: [],
  ...over,
});

const emptyStatus = {
  earliest: 0, head: 0, rows: 0, bytes: 0, oldest_at: '', newest_at: '', delivering: false,
  retention_seconds: 0, recent_window_seconds: 0, baseline_window_seconds: 0, surge_rate_per_hour: 0,
  pin_alarm_seconds: 0, producers: [], consumers: [], health: [],
};

function openBus(over: Partial<BusStatus> = {}, script: (daemon: ScriptedDaemon) => void = () => {}) {
  return openSection('eventBus', {}, (daemon) => {
    daemon.on('bus_status_get', () => status(over));
    script(daemon);
  });
}

const statusReads = (daemon: ScriptedDaemon) => daemon.sentOf('bus_status_get').length;

describe('App event bus settings', () => {
  it('reads the bus once on open, then again only on its slow refresh', async () => {
    const daemon = await openBus();
    expect(statusReads(daemon)).toBe(1);

    await act(() => vi.advanceTimersByTimeAsync(29_000));
    await daemon.idle();
    expect(statusReads(daemon)).toBe(1);

    await act(() => vi.advanceTimersByTimeAsync(1_000));
    await daemon.idle();
    expect(statusReads(daemon)).toBe(2);
  });

  it('shows each producer with its share, subjects and rates, loudest first, and folds the quiet tail', async () => {
    await openBus({
      producers: [
        producer(),
        producer({
          name: 'pr.updated', events: 53182, share: 0.169, subjects: 107,
          recent_per_hour: 279, baseline_per_hour: 329, sustained_per_hour: 148,
          surging: false, surge_window_seconds: 0, surge_per_hour: 0,
        }),
        producer({ name: 'ticket.created', events: 4, share: 0.002, surging: false }),
        producer({ name: 'plugin.installed', events: 1, share: 0.001, surging: false }),
      ],
    });

    const loud = screen.getByTestId('bus-producer-session.state.changed');
    expect(loud).toHaveTextContent('232,213');
    expect(loud).toHaveTextContent('73.7%');
    expect(loud).toHaveTextContent('103');
    expect(loud).toHaveTextContent('Loud');
    expect(screen.getByTestId('bus-producer-pr.updated')).not.toHaveTextContent('Loud');

    expect(screen.queryByTestId('bus-producer-ticket.created')).toBeNull();
    const toggle = screen.getByTestId('bus-toggle-quiet');
    expect(toggle).toHaveTextContent('2 quieter classes');
    expect(toggle).toHaveTextContent('5 events');
    expect(toggle).toHaveTextContent('0.3% of the log');
    fireEvent.click(toggle);
    expect(screen.getByTestId('bus-producer-ticket.created')).toBeInTheDocument();
  });

  it('shows the daemon’s health findings verbatim', async () => {
    await openBus({
      health: [
        {
          level: 'error',
          kind: 'consumer_lagging',
          subject: 'notifier',
          message: 'consumer notifier is 41,000 events behind and not advancing; its cursor has not moved for 2h',
        },
        {
          level: 'warn',
          kind: 'producer_surging',
          subject: 'session.state.changed',
          message: 'producer session.state.changed is publishing 1816 events/hour sustained over the last 24h, past the 1000/hour tripwire',
        },
      ],
    });

    const health = screen.getByTestId('bus-health');
    expect(health).toHaveTextContent('41,000 events behind and not advancing');
    expect(health).toHaveTextContent('past the 1000/hour tripwire');
    expect(health).toHaveTextContent('Error');
    expect(health).toHaveTextContent('Warning');
  });

  it('says no durable consumers are registered rather than showing an empty table', async () => {
    await openBus();

    expect(screen.getByTestId('bus-no-consumers')).toHaveTextContent('No durable consumers are registered');
    expect(screen.queryByTestId('bus-consumers')).toBeNull();
  });

  it('marks a disabled, a pinning, an absent and a stalled consumer', async () => {
    await openBus({
      consumers: [
        consumer({ name: 'killed', enabled: false, cursor: 12, lag: 315176 }),
        consumer({ name: 'pinner', holds_retention_floor: true, lag: 41000, oldest_unread_at: new Date(Date.now() - 7 * DAY_MS).toISOString() }),
        consumer({ name: 'absent', live: false }),
        consumer({ name: 'failing', stalled: 'handler blew up' }),
      ],
    });

    expect(screen.getByTestId('bus-consumer-killed')).toHaveTextContent('Disabled');
    expect(screen.getByTestId('bus-consumer-pinner')).toHaveTextContent('Retention floor');
    expect(screen.getByTestId('bus-consumer-pinner')).toHaveTextContent('41,000');
    expect(screen.getByTestId('bus-consumer-pinner')).toHaveTextContent('7d');
    expect(screen.getByTestId('bus-consumer-absent')).toHaveTextContent('Not running');
    expect(screen.getByTestId('bus-consumer-failing')).toHaveTextContent('Stalled');
  });

  it('tells an ordinary retention floor from one pinning past the tripwire, and names the tripwire exactly', async () => {
    await openBus({
      pin_alarm_seconds: 90,
      consumers: [
        consumer({ name: 'ordinary', holds_retention_floor: true, lag: 12 }),
        consumer({ name: 'app:ticketwatch', holds_retention_floor: true, pin_alarm: true, pinned_bytes: 31_000, lag: 41000 }),
      ],
    });

    expect(screen.getByTestId('bus-consumer-ordinary')).toHaveTextContent('Retention floor');
    expect(screen.getByTestId('bus-consumer-ordinary')).not.toHaveTextContent('Pinning');
    const alarming = screen.getByTestId('bus-consumer-app:ticketwatch');
    expect(alarming).toHaveTextContent('Pinning 30.3 KB');
    expect(alarming).not.toHaveTextContent('Retention floor');
    expect(alarming.querySelector('.settings-pill.bad')?.getAttribute('title')).toContain('longer than 1m30s');
  });

  it('claims nothing about delivery the daemon cannot observe', async () => {
    await openBus({ delivering: false, consumers: [consumer({ name: 'absent', live: false })] });

    expect(screen.getByTestId('bus-consumer-absent')).not.toHaveTextContent('Not running');
    const footer = screen.getByTestId('bus-refresh').parentElement!;
    expect(footer).toHaveTextContent('Read from the daemon');
    expect(footer).not.toHaveTextContent('from the database');
  });

  it('turns a consumer off and reads the bus again', async () => {
    const daemon = await openBus({ consumers: [consumer()] }, (scripted) => {
      scripted.on('bus_set_consumer_enabled', ({ consumer: name }) => ({ event: 'bus_set_consumer_enabled_result', success: true, consumer: name }));
    });

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('bus-consumer-toggle-notifier')));

    expect(daemon.sentOf('bus_set_consumer_enabled').map(({ consumer: name, enabled }) => [name, enabled])).toEqual([['notifier', false]]);
    expect(statusReads(daemon)).toBe(2);
  });

  it('shows why the daemon refused a consumer toggle', async () => {
    const daemon = await openBus({ consumers: [consumer()] }, (scripted) => {
      scripted.on('bus_set_consumer_enabled', ({ consumer: name }) => ({ event: 'bus_set_consumer_enabled_result', success: false, consumer: name, error: 'no consumer named notifier' }));
    });

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('bus-consumer-toggle-notifier')));

    expect(screen.getByText('no consumer named notifier')).toBeInTheDocument();
  });

  it('offers a retry when the bus cannot be read at all', async () => {
    let readable = false;
    const daemon = await openSection('eventBus', {}, (scripted) => {
      scripted.on('bus_status_get', () => (readable ? status() : { event: 'bus_status_result', ...emptyStatus, success: false, error: 'disk gone' }));
    });
    expect(screen.getByText('disk gone')).toBeInTheDocument();

    readable = true;
    await gesture(daemon, () => fireEvent.click(screen.getByText('Try again')));

    expect(screen.getByTestId('bus-producers')).toBeInTheDocument();
  });
});

