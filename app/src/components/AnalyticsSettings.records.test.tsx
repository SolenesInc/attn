import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture } from '../test/renderApp';
import type { ScriptedDaemon } from '../test/scriptedDaemon';
import { openSection } from '../test/settings';

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
  daemon.on('list_analytics_records', ({ stream }) => ({
    event: 'list_analytics_records_result',
    success: true,
    waiting: 0,
    records: RECORDS.filter((r) => !stream || JSON.parse(r.record).stream === stream),
  }));
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

  it('is absent from the nav while analytics is off', async () => {
    await openSection('connectivity', { settings: { 'analytics.enabled': 'false' } });
    expect(screen.queryByTestId('settings-nav-analytics')).toBeNull();
  });
});
