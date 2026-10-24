import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture } from '../../test/renderApp';
import type { ScriptedDaemon } from '../../test/scriptedDaemon';
import { openSection } from '../../test/settings';

const record = (id: number, stream: string, name: string, at: string, extra: Record<string, unknown> = {}) =>
  ({ id, record: JSON.stringify({ stream, seq: id, name, at, ...extra }) });

const RECORDS = [
  record(9, 'bus', 'session.state.changed', '2026-10-24T14:03:12Z', { subject: 'h:77c2e01fa4d3' }),
  record(8, 'bus', 'pr.updated', '2026-10-24T14:02:58Z', { subject: 'h:0d7e44a1b9c2' }),
  record(7, 'bus', 'pr.updated', '2026-10-24T14:02:58Z', { subject: 'h:3a19f0c27e18' }),
  record(6, 'bus', 'pr.updated', '2026-10-24T14:02:58Z', { subject: 'h:c4e1022b9d07' }),
  record(5, 'app', 'shortcut', '2026-10-24T14:00:44Z', { props: { id: 'queue.next' } }),
];

function serveRecords(daemon: ScriptedDaemon) {
  let stored = RECORDS;
  daemon.on('list_analytics_records', ({ stream }) => ({
    event: 'list_analytics_records_result',
    success: true,
    waiting: stored.length,
    records: stored.filter((r) => !stream || JSON.parse(r.record).stream === stream),
  }));
  daemon.on('get_analytics_status', () => ({
    event: 'get_analytics_status_result',
    success: true,
    records: stored.length,
    bytes: stored.reduce((sum, r) => sum + r.record.length, 0),
    waiting: stored.length,
  }));
  daemon.on('clear_analytics_records', () => {
    const records = stored.length;
    stored = [];
    return { event: 'clear_analytics_records_result', success: true, records, vents: 1 };
  });
}

const page = () => screen.getByTestId('settings-analytics');
const press = (daemon: ScriptedDaemon, key: string) => gesture(daemon, () => fireEvent.keyDown(page(), { key }));
const listed = (name: string) => within(screen.getByRole('list', { name })).getAllByRole('listitem').map((row) => row.textContent);

describe('Settings › Analytics records', () => {
  it('loads nothing until a record list is shown, then one page per list', async () => {
    const daemon = await openSection('analytics', { settings: { 'analytics.enabled': 'true' } }, serveRecords);
    expect(daemon.sentOf('list_analytics_records')).toEqual([]);
    await press(daemon, ']');
    await press(daemon, ']');
    expect(daemon.sentOf('list_analytics_records').map(({ stream, limit, before_id }) => [stream, limit, before_id])).toEqual([
      [undefined, 200, undefined],
      ['bus', 200, undefined],
    ]);
  });

  it('collapses a same-second burst, expands it with Enter, and shows the record exactly as stored', async () => {
    const daemon = await openSection('analytics', { settings: { 'analytics.enabled': 'true' } }, serveRecords);
    await press(daemon, ']');
    expect(listed('All records')).toHaveLength(3);
    expect(listed('All records')[1]).toMatch(/pr\.updated ×3/);
    await press(daemon, 'j');
    expect(screen.getByTestId('analytics-inspector')).toHaveTextContent('3 records in the same second');
    await press(daemon, 'Enter');
    expect(listed('All records')).toHaveLength(5);
    expect(screen.getByTestId('analytics-inspector')).toHaveTextContent(RECORDS[1].record);
    await press(daemon, 'j');
    await press(daemon, 'j');
    await press(daemon, 'j');
    expect(screen.getByTestId('analytics-inspector')).toHaveTextContent(RECORDS[4].record);
  });

  it('refreshes only when asked', async () => {
    const daemon = await openSection('analytics', { settings: { 'analytics.enabled': 'true' } }, serveRecords);
    await press(daemon, ']');
    await press(daemon, 'r');
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Refresh' })));
    expect(daemon.sentOf('list_analytics_records')).toHaveLength(3);
  });

  it('shows the record count and size, and clears them after a confirm', async () => {
    const daemon = await openSection('analytics', { settings: { 'analytics.enabled': 'true' } }, serveRecords);
    const size = `${RECORDS.reduce((sum, r) => sum + r.record.length, 0)} B`;
    expect(screen.getByText(`7 days · ${size}`)).toBeInTheDocument();
    await press(daemon, 'm');
    expect(screen.getByTestId('analytics-machine')).toHaveTextContent(`5 records, ${size}. Dismissed and withdrawn vents are kept 30 days.`);
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Clear…' })));
    expect(daemon.sentOf('clear_analytics_records')).toEqual([]);
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Clear' })));
    expect(daemon.sentOf('clear_analytics_records')).toHaveLength(1);
    expect(screen.getByRole('status')).toHaveTextContent('Cleared 5 records and 1 dismissed vent.');
    expect(screen.getByTestId('analytics-machine')).toHaveTextContent('0 records, 0 B.');
    expect(daemon.sentOf('get_analytics_status')).toHaveLength(2);
  });

  it('is absent from the nav while analytics is off', async () => {
    await openSection('connectivity', { settings: { 'analytics.enabled': 'false' } });
    expect(screen.queryByTestId('settings-nav-analytics')).toBeNull();
  });
});
