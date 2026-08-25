import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { EventBusSettings } from './EventBusSettings';
import type { BusStatus } from '../hooks/daemonBusEvents';

const producer = (over: Partial<BusStatus['producers'][number]> = {}) => ({
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

const consumer = (over: Partial<BusStatus['consumers'][number]> = {}) => ({
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

const status = (over: Partial<BusStatus> = {}): BusStatus => ({
  earliest: 1,
  head: 315188,
  rows: 315188,
  bytes: 23_815_114,
  oldestAt: '2026-08-02T10:24:55Z',
  newestAt: '2026-08-10T19:28:01Z',
  delivering: true,
  retentionSeconds: 30 * 86400,
  recentWindowSeconds: 3600,
  baselineWindowSeconds: 86400,
  surgeRatePerHour: 1000,
  pinAlarmSeconds: 3600,
  producers: [producer()],
  consumers: [],
  health: [],
  ...over,
});

const renderPane = (value: BusStatus, setEnabled = vi.fn().mockResolvedValue({ consumer: 'x' })) => {
  const getBusStatus = vi.fn().mockResolvedValue(value);
  render(<EventBusSettings getBusStatus={getBusStatus} setConsumerEnabled={setEnabled} />);
  return { getBusStatus, setEnabled };
};

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  vi.setSystemTime(new Date('2026-08-10T19:30:00Z'));
});

afterEach(() => {
  vi.useRealTimers();
});

describe('EventBusSettings', () => {
  it('reads the bus once on open and does not loop', async () => {
    const { getBusStatus } = renderPane(status());
    await waitFor(() => screen.getByTestId('bus-producers'));

    expect(getBusStatus).toHaveBeenCalledTimes(1);
    await act(() => vi.advanceTimersByTimeAsync(5_000));
    expect(getBusStatus).toHaveBeenCalledTimes(1);
  });

  it('refreshes on a slow interval while it stays open', async () => {
    const { getBusStatus } = renderPane(status());
    await waitFor(() => screen.getByTestId('bus-producers'));

    await act(() => vi.advanceTimersByTimeAsync(30_000));
    expect(getBusStatus).toHaveBeenCalledTimes(2);
  });

  it('shows each producer with its share, subjects and rates, loudest first', async () => {
    renderPane(status({
      producers: [producer(), producer({
        name: 'pr.updated', events: 53182, share: 0.169, subjects: 107,
        recent_per_hour: 279, baseline_per_hour: 329, sustained_per_hour: 148,
        surging: false, surge_window_seconds: 0, surge_per_hour: 0,
      })],
    }));

    const loud = await screen.findByTestId('bus-producer-session.state.changed');
    expect(loud).toHaveTextContent('232,213');
    expect(loud).toHaveTextContent('73.7%');
    expect(loud).toHaveTextContent('103');
    expect(loud).toHaveTextContent('Loud');

    expect(screen.getByTestId('bus-producer-pr.updated')).not.toHaveTextContent('Loud');
  });

  it('states what the collapsed tail holds and can expand it', async () => {
    renderPane(status({
      producers: [
        producer(),
        producer({ name: 'ticket.created', events: 4, share: 0.002, surging: false }),
        producer({ name: 'plugin.installed', events: 1, share: 0.001, surging: false }),
      ],
    }));
    await screen.findByTestId('bus-producers');

    expect(screen.queryByTestId('bus-producer-ticket.created')).toBeNull();
    const toggle = screen.getByTestId('bus-toggle-quiet');
    expect(toggle).toHaveTextContent('2 quieter classes');
    expect(toggle).toHaveTextContent('5 events');
    expect(toggle).toHaveTextContent('0.3% of the log');

    fireEvent.click(toggle);
    expect(screen.getByTestId('bus-producer-ticket.created')).toBeInTheDocument();
  });

  it('renders the daemon health findings verbatim', async () => {
    renderPane(status({
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
    }));

    const health = await screen.findByTestId('bus-health');
    expect(health).toHaveTextContent('41,000 events behind and not advancing');
    expect(health).toHaveTextContent('past the 1000/hour tripwire');
    expect(health).toHaveTextContent('Error');
    expect(health).toHaveTextContent('Warning');
  });

  it('explains an empty consumer list rather than showing a blank table', async () => {
    renderPane(status());
    const empty = await screen.findByTestId('bus-no-consumers');
    expect(empty).toHaveTextContent('No durable consumers are registered');
    expect(screen.queryByTestId('bus-consumers')).toBeNull();
  });

  it('marks a disabled consumer, the retention floor, and one that is not running', async () => {
    renderPane(status({
      consumers: [
        consumer({ name: 'killed', enabled: false, cursor: 12, lag: 315176 }),
        consumer({ name: 'pinner', holds_retention_floor: true, lag: 41000, oldest_unread_at: '2026-08-03T19:30:00Z' }),
        consumer({ name: 'absent', live: false }),
        consumer({ name: 'failing', stalled: 'handler blew up' }),
      ],
    }));
    await screen.findByTestId('bus-consumers');

    expect(screen.getByTestId('bus-consumer-killed')).toHaveTextContent('Disabled');
    expect(screen.getByTestId('bus-consumer-pinner')).toHaveTextContent('Retention floor');
    expect(screen.getByTestId('bus-consumer-pinner')).toHaveTextContent('41,000');
    expect(screen.getByTestId('bus-consumer-pinner')).toHaveTextContent('7d');
    expect(screen.getByTestId('bus-consumer-absent')).toHaveTextContent('Not running');
    expect(screen.getByTestId('bus-consumer-failing')).toHaveTextContent('Stalled');
  });

  it('separates a normal retention floor from one past the tripwire', async () => {
    renderPane(status({
      consumers: [
        consumer({ name: 'ordinary', holds_retention_floor: true, lag: 12 }),
        consumer({
          name: 'app:ticketwatch',
          holds_retention_floor: true,
          pin_alarm: true,
          pinned_bytes: 31_000,
          lag: 41000,
          oldest_unread_at: '2026-08-03T19:30:00Z',
        }),
      ],
    }));
    await screen.findByTestId('bus-consumers');

    expect(screen.getByTestId('bus-consumer-ordinary')).toHaveTextContent('Retention floor');
    expect(screen.getByTestId('bus-consumer-ordinary')).not.toHaveTextContent('Pinning');
    const alarming = screen.getByTestId('bus-consumer-app:ticketwatch');
    expect(alarming).toHaveTextContent('Pinning 30.3 KB');
    expect(alarming).not.toHaveTextContent('Retention floor');
  });

  it('names the tripwire exactly rather than rounding it', async () => {
    renderPane(status({
      pinAlarmSeconds: 90,
      consumers: [
        consumer({ name: 'app:ticketwatch', holds_retention_floor: true, pin_alarm: true, pinned_bytes: 1024 }),
      ],
    }));
    await screen.findByTestId('bus-consumers');

    const pill = screen.getByTestId('bus-consumer-app:ticketwatch').querySelector('.settings-pill.bad');
    expect(pill?.getAttribute('title')).toContain('longer than 1m30s');
  });

  it('does not claim a consumer is down when delivery is not observable', async () => {
    renderPane(status({ delivering: false, consumers: [consumer({ name: 'absent', live: false })] }));
    await screen.findByTestId('bus-consumers');

    expect(screen.getByTestId('bus-consumer-absent')).not.toHaveTextContent('Not running');
  });

  it('says the daemon has no delivery loops rather than claiming it read the database', async () => {
    renderPane(status({ delivering: false }));
    await screen.findByTestId('bus-producers');

    const footer = screen.getByTestId('bus-refresh').parentElement as HTMLElement;
    expect(footer).toHaveTextContent('Read from the daemon');
    expect(footer).not.toHaveTextContent('from the database');
  });

  it('toggles a consumer and re-reads the result', async () => {
    const setEnabled = vi.fn().mockResolvedValue({ consumer: 'notifier' });
    const { getBusStatus } = renderPane(status({ consumers: [consumer()] }), setEnabled);
    await screen.findByTestId('bus-consumers');

    fireEvent.click(screen.getByTestId('bus-consumer-toggle-notifier'));
    await waitFor(() => expect(setEnabled).toHaveBeenCalledWith('notifier', false));
    await waitFor(() => expect(getBusStatus).toHaveBeenCalledTimes(2));
  });

  it('shows the failure when the toggle is refused', async () => {
    const setEnabled = vi.fn().mockRejectedValue(new Error('no consumer named notifier'));
    renderPane(status({ consumers: [consumer()] }), setEnabled);
    await screen.findByTestId('bus-consumers');

    fireEvent.click(screen.getByTestId('bus-consumer-toggle-notifier'));
    await waitFor(() => screen.getByText('no consumer named notifier'));
  });

  it('offers a retry when the bus cannot be read at all', async () => {
    const getBusStatus = vi.fn().mockRejectedValue(new Error('disk gone'));
    render(<EventBusSettings getBusStatus={getBusStatus} setConsumerEnabled={vi.fn()} />);

    await waitFor(() => screen.getByText('disk gone'));
    getBusStatus.mockResolvedValue(status());
    fireEvent.click(screen.getByText('Try again'));
    await waitFor(() => screen.getByTestId('bus-producers'));
  });
});
