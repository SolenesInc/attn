import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { closedEntry } from '../../test/sessionLedgerFixtures';
import { openSessionsLedger, page, pages } from './testSupport';

const inspector = () => screen.getByRole('complementary', { name: 'Details' });
const usage = (tokens: number) => ({ total_tokens: tokens, has_unpriced_usage: false, measurement_incomplete: true, models: [] });

describe('SessionsTab usage', () => {
  it('re-reads closed rows when a price setting changes', async () => {
    const view = await openSessionsLedger(pages([
      page({ entries: [closedEntry('s1', { usage: usage(1200) })] }),
      page({ entries: [closedEntry('s1', { usage: usage(3400) })] }),
    ]));
    expect(within(inspector()).getByText('1,200 tokens')).toBeInTheDocument();

    view.daemon.emit({ event: 'settings_updated', changed_key: 'session_cost.price.claude-opus', settings: { 'session_cost.price.claude-opus': '{"input":1}' } });
    await view.daemon.idle();

    expect(view.daemon.sentOf('session_list')).toHaveLength(2);
    expect(within(inspector()).getByText('3,400 tokens')).toBeInTheDocument();
  });
});
