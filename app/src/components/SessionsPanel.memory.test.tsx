import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { SessionsPanel } from './SessionsPanel';
import { SettingsProvider } from '../contexts/SettingsContext';
import { SESSION_FILTERS_SETTING_KEY } from '../hooks/sessionFiltersSetting';
import type { SessionLedgerPage, SessionLedgerQuery } from '../hooks/daemonSessionLedgerEvents';

const NOW = new Date('2026-09-05T14:30:00Z');
const now = () => NOW;

function listing() {
  const calls: SessionLedgerQuery[] = [];
  const list = vi.fn(async (query: SessionLedgerQuery): Promise<SessionLedgerPage> => {
    calls.push(query);
    return { entries: [], omitted: 0 };
  });
  return { list, calls };
}

async function open(stored: string | undefined) {
  const { list, calls } = listing();
  const setSetting = vi.fn();
  const settings: Record<string, string> = stored === undefined ? {} : { [SESSION_FILTERS_SETTING_KEY]: stored };
  render(
    <SettingsProvider settings={settings} setSetting={setSetting}>
      <SessionsPanel isOpen onClose={() => {}} listSessions={list} now={now} />
    </SettingsProvider>,
  );
  await waitFor(() => expect(screen.queryByText('Reading the ledger…')).toBeNull());
  return { calls, setSetting };
}

function written(setSetting: ReturnType<typeof vi.fn>) {
  const calls = setSetting.mock.calls.filter(([key]) => key === SESSION_FILTERS_SETTING_KEY);
  return calls.map(([, value]) => JSON.parse(value as string));
}

const CLOSED_LAST_WEEK = JSON.stringify({
  scope: 'closed',
  range: '7d',
  customFrom: '',
  customTo: '',
  workspaceId: 'ws-2',
  repository: '/Users/victor/projects/attn',
});

describe('SessionsPanel filter memory', () => {
  it('queries with the remembered filters on the first read', async () => {
    const { calls, setSetting } = await open(CLOSED_LAST_WEEK);

    const since = new Date(NOW.getFullYear(), NOW.getMonth(), NOW.getDate() - 6).toISOString();
    expect(calls).toEqual([{
      closed: true,
      since,
      workspace_id: 'ws-2',
      repository: '/Users/victor/projects/attn',
      limit: 50,
      reopen: true,
    }]);
    // Restoring is not a change: opening the list must not write the setting back.
    expect(written(setSetting)).toEqual([]);
  });

  it('restores a custom range exactly as it was left', async () => {
    const stored = JSON.stringify({
      scope: 'all',
      range: 'custom',
      customFrom: '2026-08-01',
      customTo: '2026-08-03',
      workspaceId: '',
      repository: '',
    });
    const { calls } = await open(stored);

    expect(calls).toHaveLength(1);
    expect(calls[0].since).toBe(new Date(2026, 7, 1).toISOString());
    expect(calls[0].until).toBe(new Date(2026, 7, 4).toISOString());
    expect((screen.getByLabelText('From') as HTMLInputElement).value).toBe('2026-08-01');
    expect((screen.getByLabelText('To') as HTMLInputElement).value).toBe('2026-08-03');
  });

  it('remembers a filter the moment it changes', async () => {
    const { setSetting } = await open(undefined);

    fireEvent.click(screen.getByRole('button', { name: 'Closed' }));
    await waitFor(() => expect(written(setSetting)).toHaveLength(1));
    expect(written(setSetting)[0]).toEqual({
      scope: 'closed',
      range: 'any',
      customFrom: '',
      customTo: '',
      workspaceId: '',
      repository: '',
    });

    fireEvent.change(screen.getByLabelText('When'), { target: { value: '30d' } });
    await waitFor(() => expect(written(setSetting)).toHaveLength(2));
    expect(written(setSetting)[1].range).toBe('30d');

    // Picking the scope that is already on is not a change worth a round trip.
    fireEvent.click(screen.getByRole('button', { name: 'Closed' }));
    await act(async () => { await Promise.resolve(); });
    expect(written(setSetting)).toHaveLength(2);
  });

  it.each([
    ['absent', undefined],
    ['not JSON', '{scope: closed}'],
    ['an unknown scope', JSON.stringify({ scope: 'archived', range: 'any' })],
    ['an unknown range', JSON.stringify({ scope: 'all', range: 'last-week' })],
    ['an unreadable date', JSON.stringify({ scope: 'all', range: 'custom', customFrom: 'yesterday' })],
  ])('opens on the defaults when the setting is %s', async (_label, stored) => {
    const { calls } = await open(stored);

    expect(calls).toEqual([{ all: true, limit: 50, reopen: true }]);
  });
});
