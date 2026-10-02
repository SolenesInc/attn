import { expect, it } from 'vitest';
import { fireEvent, screen } from '@testing-library/react';
import { namedWorkspaces, openSessionsLedger, page, pages } from './testSupport';
import { SESSION_FILTERS_SETTING_KEY } from '../../hooks/sessionFiltersSetting';
import { closedEntry, liveEntry } from '../../test/sessionLedgerFixtures';
import { daemonSession } from '../../test/daemonFixtures';
import type { SessionUsage } from '../../types/generated';

const usage: SessionUsage = {
  total_tokens: 300,
  cost_usd: 0.12,
  has_unpriced_usage: false,
  models: [{ model: 'gpt-6.1-sol', purpose: 'agent', input_tokens: 100, output_tokens: 200,
    cache_read_tokens: 0, cache_write_5m_tokens: 0, cache_write_1h_tokens: 0,
    cache_write_unclassified_tokens: 0, total_tokens: 300, cost_usd: 0.12, has_unpriced_usage: false }],
};

it('reads the closed owner usage and opens its existing model breakdown', async () => {
  await openSessionsLedger(pages([page({ entries: [closedEntry('cost', { usage })] })]));
  expect(screen.getByText('300 tokens')).toBeInTheDocument();
  fireEvent.click(screen.getByTestId('session-usage-cost'));
  expect(screen.getByRole('dialog', { name: 'Session usage breakdown' })).toHaveTextContent('gpt-6.1-sol');
  expect(screen.getByRole('dialog', { name: 'Session usage breakdown' })).toHaveTextContent('$0.12');
});

it('updates live ledger usage from owner events and keeps the final close total', async () => {
  const view = await openSessionsLedger(pages([page({ entries: [{ ...liveEntry('cost'), usage }] })]));
  fireEvent.click(screen.getByRole('button', { name: 'All' }));
  await view.daemon.idle();
  const next = { ...usage, total_tokens: 400 };
  view.daemon.emit({ event: 'session_state_changed', session: daemonSession('cost', { usage: next }) });
  await view.daemon.idle();
  expect(screen.getByText('400 tokens')).toBeInTheDocument();
  await view.closed(closedEntry('cost', { usage: next }));
  expect(screen.getByText('400 tokens')).toBeInTheDocument();
});

it('shows incomplete native measurement instead of presenting a settled price', async () => {
  await openSessionsLedger(pages([page({ entries: [closedEntry('cost', { usage: { ...usage, measurement_incomplete: true } })] })]));
  expect(screen.getByText('Measurement incomplete; native usage may be missing.')).toBeInTheDocument();
  expect(screen.queryByTestId('session-usage-cost')).not.toBeInTheDocument();
});

it('reopens the displayed ledger owner and accepts its subsequent usage events', async () => {
  const view = await openSessionsLedger(pages([page({ entries: [closedEntry('cost', { usage })] })]));
  fireEvent.click(screen.getByRole('button', { name: 'All' }));
  await view.daemon.idle();
  view.daemon.emit({ event: 'session_state_changed', session: daemonSession('cost', { usage }) });
  await view.daemon.idle();
  view.daemon.emit({ event: 'session_state_changed', session: daemonSession('cost', { usage: { ...usage, total_tokens: 500 } }) });
  await view.daemon.idle();
  expect(screen.getByText('500 tokens')).toBeInTheDocument();
  expect(screen.queryByText('Closed', { selector: '.ledger-field-label' })).not.toBeInTheDocument();
});

it('refreshes a reopened owner location from the daemon', async () => {
  const view = await openSessionsLedger(pages([page({ entries: [closedEntry('cost', { usage,
    directory: '/old', repository: 'old-repository', branch: 'old-branch', is_worktree: true, main_repo: '/old-main' })] })]));
  fireEvent.click(screen.getByRole('button', { name: 'All' }));
  await view.daemon.idle();
  view.daemon.emit({ event: 'session_state_changed', session: daemonSession('cost', { usage,
    directory: '/replacement', workspace_id: 'replacement-workspace', repository: 'replacement-repository',
    branch: 'replacement-branch', is_worktree: false, main_repo: undefined }) });
  await view.daemon.idle();
  expect(screen.getAllByText('/replacement').length).toBeGreaterThan(0);
  expect(screen.getAllByText('replacement-branch').length).toBeGreaterThan(0);
  expect(screen.queryByText('/old')).not.toBeInTheDocument();
  expect(screen.queryByText('old-branch')).not.toBeInTheDocument();
});

it.each(['workspace', 'repository'])('removes a reopened owner outside its %s filter', async (filter) => {
  const view = await openSessionsLedger(pages([page({ entries: [closedEntry('cost', { usage, repository: 'old-repository' })] })]), {
    initialState: { workspaces: namedWorkspaces({ 'ws-1': 'old-workspace', 'ws-2': 'replacement-workspace' }),
      settings: { [SESSION_FILTERS_SETTING_KEY]: JSON.stringify({ scope: 'all', range: 'any', customFrom: '', customTo: '',
        workspaceId: filter === 'workspace' ? 'ws-1' : '', repository: filter === 'repository' ? 'old-repository' : '' }) } },
  });
  expect(screen.getByText('300 tokens')).toBeInTheDocument();
  view.daemon.emit({ event: 'session_state_changed', session: daemonSession('cost', { usage,
    workspace_id: 'ws-2', repository: 'replacement-repository' }) });
  await view.daemon.idle();
  expect(screen.queryByText('300 tokens')).not.toBeInTheDocument();
  expect(document.querySelector('.ledger-row[data-row-key="cost"]')).toBeNull();
});

it.each(['session_registered', 'session_state_changed'] as const)('inserts an absent live owner from %s without reloading', async (event) => {
  const view = await openSessionsLedger(pages([page({ entries: [closedEntry('previous')], facets: { workspaces: [{ value: 'ws-1', count: 1 }], repositories: [] } })]));
  view.daemon.emit({ event, session: daemonSession('new-owner', { usage, label: 'new owner', workspace_id: 'ws-1' }) });
  await view.daemon.idle();
  expect(document.querySelector('.ledger-row[data-row-key="new-owner"]')).toHaveTextContent('new owner');
  fireEvent.click(document.querySelector('.ledger-row[data-row-key="new-owner"]')!);
  expect(screen.getByText('300 tokens')).toBeInTheDocument();
  expect(view.queries()).toHaveLength(1);
});

it('keeps an absent live owner outside the closed ledger', async () => {
  const view = await openSessionsLedger(pages([page({ entries: [closedEntry('previous')] })]));
  fireEvent.click(screen.getByRole('button', { name: 'Closed' }));
  await view.daemon.idle();
  view.daemon.emit({ event: 'session_registered', session: daemonSession('new-owner', { usage }) });
  await view.daemon.idle();
  expect(document.querySelector('.ledger-row[data-row-key="new-owner"]')).toBeNull();
});

it.each(['session_registered', 'session_state_changed'] as const)('preserves %s while an older initial page is pending', async (event) => {
  const view = await openSessionsLedger(() => 'hold');
  view.daemon.emit({ event, session: daemonSession('cost', { usage, label: 'current owner' }) });
  await view.daemon.idle();
  await view.release(0, page({ entries: event === 'session_registered' ? [] : [closedEntry('cost', { usage: { ...usage, total_tokens: 1 } })] }));
  expect(document.querySelector('.ledger-row[data-row-key="cost"]')).toHaveTextContent('current owner');
  expect(screen.getByText('300 tokens')).toBeInTheDocument();
  expect(screen.queryByText('Closed', { selector: '.ledger-field-label' })).not.toBeInTheDocument();
});

it('keeps a close received during an initial ledger read', async () => {
  const view = await openSessionsLedger(() => 'hold');
  await view.closed(closedEntry('cost', { usage }));
  await view.release(0, page({ entries: [liveEntry('cost')] }));
  expect(document.querySelector('.ledger-row[data-row-key="cost"]')).toHaveAttribute('data-state', 'closed');
  expect(screen.getByText('300 tokens')).toBeInTheDocument();
});

it('does not restore a filtered-out owner from a pending older page', async () => {
  const view = await openSessionsLedger((_query, index) => index === 0
    ? page({ entries: [closedEntry('current')], next_before: 'older', omitted: 1 }) : 'hold', {
    initialState: { settings: { [SESSION_FILTERS_SETTING_KEY]: JSON.stringify({ scope: 'all', range: 'any', customFrom: '', customTo: '', workspaceId: 'ws-1', repository: '' }) } },
  });
  fireEvent.click(screen.getByRole('button', { name: '1 older ↓' }));
  await view.daemon.idle();
  view.daemon.emit({ event: 'session_state_changed', session: daemonSession('cost', { usage, workspace_id: 'ws-2' }) });
  await view.daemon.idle();
  await view.release(0, page({ entries: [closedEntry('cost', { usage, workspace_id: 'ws-1' })] }));
  expect(document.querySelector('.ledger-row[data-row-key="cost"]')).toBeNull();
});

it('orders updated rows by the daemon ledger timestamp and id', async () => {
  const view = await openSessionsLedger(pages([page({ entries: [
    closedEntry('newest', { closed_at: '2026-09-05T14:00:00Z' }),
    liveEntry('z-owner'), liveEntry('a-owner'),
  ] })]));
  const ids = () => [...document.querySelectorAll('.ledger-row[data-row-key]')].map((row) => row.getAttribute('data-row-key'));
  view.daemon.emit({ event: 'session_state_changed', session: daemonSession('a-owner', { usage, last_seen: '2026-09-05T14:15:00Z' }) });
  await view.daemon.idle();
  expect(ids()).toEqual(['a-owner', 'newest', 'z-owner']);
  view.daemon.emit({ event: 'session_state_changed', session: daemonSession('z-owner', { usage, last_seen: '2026-09-05T14:15:00Z' }) });
  await view.daemon.idle();
  expect(ids()).toEqual(['z-owner', 'a-owner', 'newest']);
});

it('keeps successor events when a superseded read resolves', async () => {
  const view = await openSessionsLedger(() => 'hold');
  fireEvent.click(screen.getByRole('button', { name: 'All' }));
  await view.daemon.idle();
  view.daemon.emit({ event: 'session_state_changed', session: daemonSession('cost', { usage }) });
  await view.daemon.idle();
  await view.release(0, page({ entries: [] }));
  await view.release(1, page({ entries: [closedEntry('cost')] }));
  expect(screen.getByText('300 tokens')).toBeInTheDocument();
  expect(document.querySelector('.ledger-row[data-row-key="cost"]')).not.toHaveAttribute('data-state', 'closed');
});

it.each(['during read', 'after read'])('paginates from the oldest displayed row when the cursor moves %s', async (timing) => {
  const first = page({ entries: [liveEntry('newer'), { ...liveEntry('cursor'), last_seen: '2026-09-05T12:00:00Z' }], next_before: 'cursor', omitted: 1 });
  const view = await openSessionsLedger((_query, index) => index === 0
    ? timing === 'during read' ? 'hold' : first
    : page({ entries: [closedEntry('older')] }));
  view.daemon.emit({ event: 'session_state_changed', session: daemonSession('cursor', { usage, last_seen: '2026-09-05T14:15:00Z' }) });
  await view.daemon.idle();
  if (timing === 'during read') await view.release(0, first);
  fireEvent.click(screen.getByRole('button', { name: '1 older ↓' }));
  await view.daemon.idle();
  expect(view.queries()[1].before).toBe('newer');
  expect(document.querySelector('.ledger-row[data-row-key="older"]')).toBeInTheDocument();
});

it.each(['session_cost.price.gpt-6.1-sol', 'session_cost.billed_as.codex-auto-review'])('refreshes displayed closed costs after %s changes', async (key) => {
  const repriced = { ...usage, cost_usd: 0.24, models: usage.models?.map((model) => ({ ...model, cost_usd: 0.24 })) };
  const view = await openSessionsLedger(pages([
    page({ entries: [closedEntry('cost', { usage })] }),
    page({ entries: [closedEntry('cost', { usage: repriced })] }),
  ]));
  fireEvent.click(screen.getByTestId('session-usage-cost'));
  expect(screen.getByRole('dialog', { name: 'Session usage breakdown' })).toHaveTextContent('$0.12');
  view.daemon.emit({ event: 'settings_updated', success: true, changed_key: key, settings: { [key]: 'updated' } });
  await view.daemon.idle();
  expect(screen.getByRole('dialog', { name: 'Session usage breakdown' })).toHaveTextContent('$0.24');
  expect(view.queries()).toHaveLength(2);
});
