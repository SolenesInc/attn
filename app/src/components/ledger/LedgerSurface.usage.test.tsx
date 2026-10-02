import { expect, it } from 'vitest';
import { fireEvent, screen } from '@testing-library/react';
import { openSessionsLedger, page, pages } from './testSupport';
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
