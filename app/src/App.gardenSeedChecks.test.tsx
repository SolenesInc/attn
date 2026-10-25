import { fireEvent, screen, within } from '@testing-library/react';
import { invoke } from '@tauri-apps/api/core';
import { openUrl } from '@tauri-apps/plugin-opener';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { daemonSession, type DaemonSeed } from './test/daemonFixtures';
import { gardenRegion, openGarden, openRow, partOf, planted, plot, row, seedHeading, type Garden } from './test/garden';
import { gesture, pressShortcut } from './test/renderApp';

type Check = NonNullable<DaemonSeed['checks']>[number];

const AT = '2026-10-19T21:10:00Z';
const BUILDER = 'sess-builder';
const PR_736 = 'https://github.com/victorarias/attn/pull/736';

const C8: Check = {
  id: 'C8', text: 'A database from the previous build opens with its sessions intact.', removed: false, state: 'confirmed',
  report: {
    summary: 'Migration test opens a previous-build database intact',
    evidence: [{ artifact: 'verify-migration.tar.gz' }, { url: PR_736 }],
    commit: 'da2b3b9c0ffee0000000000000000000000000000', by_session: BUILDER, at: AT,
  },
  verdict: { confirmed: true, by_member: 'trellis', at: AT },
};
const C4: Check = {
  id: 'C4', text: 'Packaged-app CI is green on the pull request head.', removed: false, state: 'rejected', from: 's-plot11',
  report: { summary: 'Acceptance shards 1-4 green', evidence: [], by_session: BUILDER, at: AT },
  verdict: { confirmed: false, reason: 'That run is on 869dc03, before the review fixes.', by_member: 'trellis', at: AT },
};
const C1: Check = {
  id: 'C1', text: 'Any promise the child touches is guarded by a wire test.', removed: false, state: 'reported',
  report: { summary: 'Wire tests cover the reactions', evidence: [], by_session: BUILDER, at: AT },
};
const C5: Check = { id: 'C5', text: 'A manual run on a non-production instance.', removed: false, state: 'open', from: 's-plot11' };
const C2: Check = {
  id: 'C2', text: 'The glossary defines session monitor.', removed: false, state: 'confirmed',
  verdict: { confirmed: true, by_session: 'c9b177e1-4b6f-4c0e-9d1e-1b2c3d4e5f60', at: AT },
};
const C9: Check = { id: 'C9', text: 'An old statement nobody needs.', removed: true, state: 'open' };

const TALLY = { total: 5, reported: 1, confirmed: 2, rejected: 1 };

const leaf = (checks: Check[] = [C8, C4, C1, C5, C2, C9], overrides: Partial<DaemonSeed> = {}) =>
  partOf('s-plot11', 's-leaf11', 'View and home reactions', { status: 'growing', checks, ...overrides });
const done = partOf('s-done11', 's-done11', 'Monitor container', { status: 'harvested', edges: [{ kind: 'part-of', to: 's-plot11' }] });
const table = partOf('s-plot11', 's-tbl111', 'Policy and table');

const monitorPlot = (tally = TALLY) => plot('s-plot11', 'The monitor plot', '', {
  total: 3, done: 1, growing: 1, ready: 1, checks: tally,
}, {
  checks: [{ id: 'C6', text: 'The plot glossary entry is written.', removed: false, state: 'open' }],
  shared_checks: [{
    id: 'C4', text: C4.text, removed: false,
    seeds: [
      { seed_id: 's-done11', state: 'confirmed', has_report: true },
      { seed_id: 's-leaf11', state: 'rejected', has_report: true },
      { seed_id: 's-tbl111', state: 'open', has_report: false },
    ],
  }],
});

const checks = () => within(screen.getByRole('region', { name: 'Checks' }));
const checkRow = (id: string) => gardenRegion().querySelector<HTMLElement>(`[data-check-row="${id}"]`)!;
const log = () => within(screen.getByRole('heading', { name: 'Log' }).closest('section')!);

function answerChecks(garden: Garden) {
  garden.daemon.on('seed_check', ({ seed_id }) => ({
    event: 'seed_check_result', success: true, seed: garden.seeds.find((seed) => seed.id === seed_id)!,
  }));
}

function withCheck(seed: DaemonSeed, next: Check): DaemonSeed {
  return { ...seed, rev: seed.rev + 1, checks: (seed.checks ?? []).map((check) => (check.id === next.id ? next : check)) };
}

async function openLeaf(seeds: DaemonSeed[] = [leaf(), monitorPlot(), done, table], document: Parameters<typeof openGarden>[1] = {}) {
  const garden = await openGarden(seeds, { sessions: [daemonSession('s1'), daemonSession(BUILDER, { label: 'Builder' })], ...document });
  answerChecks(garden);
  await openRow(garden.daemon, 'The monitor plot');
  await openRow(garden.daemon, 'View and home reactions');
  return garden;
}

describe('App garden seed checks', () => {
  beforeEach(() => {
    vi.mocked(invoke).mockClear();
    vi.mocked(openUrl).mockClear();
  });

  it('renders every state under the body, naming authors and inherited checks', async () => {
    await openLeaf();

    expect(checkRow('C8')).toHaveTextContent('Migration test opens a previous-build database intact');
    expect(checkRow('C8')).toHaveTextContent('@da2b3b9 · Builder');
    expect(checkRow('C8')).toHaveTextContent('confirmed by Trellis');
    expect(checkRow('C4')).toHaveTextContent('from s-plot11');
    expect(checkRow('C4')).toHaveTextContent('rejected by Trellis: That run is on 869dc03, before the review fixes.');
    expect(within(checkRow('C1')).getByRole('button', { name: 'Confirm C1' })).toHaveTextContent('?');
    expect(within(checkRow('C5')).getByRole('button', { name: 'Confirm C5' })).toHaveTextContent('');
    expect(checkRow('C2')).toHaveTextContent('confirmed by c9b177e1 · no report');
    expect(within(checkRow('C8')).getByRole('button', { name: 'Uncheck C8' })).toHaveTextContent('✓');

    const body = within(gardenRegion()).getByRole('heading', { name: 'Checks' });
    expect(body.compareDocumentPosition(log().getByText(/Nothing on this seed/)) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('folds removed checks away and keeps them read-only', async () => {
    const garden = await openLeaf();
    expect(checkRow('C9')).toBeNull();

    await gesture(garden.daemon, () => fireEvent.click(checks().getByRole('button', { name: /1 removed/ })));

    expect(checkRow('C9')).toHaveTextContent('An old statement nobody needs.');
    expect(within(checkRow('C9')).queryAllByRole('button')).toHaveLength(0);
  });

  it('opens artifact evidence through the seed artifact actions and URL evidence in the browser', async () => {
    const garden = await openGarden([leaf(), monitorPlot(), done, table]);
    garden.documents['s-leaf11'] = {
      artifacts: [{ filename: 'verify-migration.tar.gz', relative_target: 'verify-migration.tar.gz', size: 12, modified_at: AT }],
    };
    garden.daemon.on('seed_artifact_target', ({ relative_target }) => ({
      event: 'seed_artifact_target_result', success: true,
      result: { relative_target, path: `/notebook/seeds/s-leaf11/${relative_target}` },
    }));
    await openRow(garden.daemon, 'The monitor plot');
    await openRow(garden.daemon, 'View and home reactions');

    await gesture(garden.daemon, () => fireEvent.click(within(checkRow('C8')).getByRole('button', { name: 'verify-migration.tar.gz' })));
    expect(invoke).toHaveBeenCalledWith('open_safe_seed_artifact_target', {
      path: '/notebook/seeds/s-leaf11/verify-migration.tar.gz', reveal: true,
    });

    await gesture(garden.daemon, () => fireEvent.click(within(checkRow('C8')).getByRole('button', { name: 'PR 736' })));
    expect(openUrl).toHaveBeenCalledWith(PR_736);
  });

  it('confirms open and reported checks and unchecks a confirmed one, showing each result once the push lands', async () => {
    const garden = await openLeaf();

    await gesture(garden.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Confirm C5' })));
    await gesture(garden.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Confirm C1' })));
    await gesture(garden.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Uncheck C8' })));

    expect(garden.daemon.sentOf('seed_check')).toEqual([
      { cmd: 'seed_check', request_id: expect.any(String), seed_id: 's-leaf11', check_id: 'C5', verb: 'confirm' },
      { cmd: 'seed_check', request_id: expect.any(String), seed_id: 's-leaf11', check_id: 'C1', verb: 'confirm' },
      { cmd: 'seed_check', request_id: expect.any(String), seed_id: 's-leaf11', check_id: 'C8', verb: 'uncheck' },
    ]);
    expect(screen.getByRole('button', { name: 'Confirm C5' })).toBeInTheDocument();

    let next = withCheck(leaf(), { ...C5, state: 'confirmed', verdict: { confirmed: true, at: AT } });
    next = withCheck(next, { ...C1, state: 'confirmed', verdict: { confirmed: true, at: AT } });
    next = withCheck(next, { ...C8, state: 'reported', verdict: undefined });
    await gesture(garden.daemon, () => garden.push([next, monitorPlot(), done, table]));

    expect(within(checkRow('C5')).getByRole('button', { name: 'Uncheck C5' })).toHaveTextContent('✓');
    expect(checkRow('C5')).toHaveTextContent('confirmed by you · no report');
    expect(within(checkRow('C1')).getByRole('button', { name: 'Uncheck C1' })).toBeInTheDocument();
    expect(within(checkRow('C8')).getByRole('button', { name: 'Confirm C8' })).toHaveTextContent('?');
  });

  it('rejects only with a reason, and Escape puts the composer away', async () => {
    const garden = await openLeaf();

    await gesture(garden.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Reject C1' })));
    const reason = within(checkRow('C1')).getByRole('textbox', { name: /Reject C1/ });
    expect(reason).toHaveFocus();
    await gesture(garden.daemon, () => fireEvent.keyDown(reason, { key: 'Enter' }));

    expect(within(checkRow('C1')).getByRole('alert')).toHaveTextContent('rejecting C1 needs a reason');
    expect(garden.daemon.sentOf('seed_check')).toEqual([]);

    fireEvent.change(reason, { target: { value: 'Cite the run on da2b3b9.' } });
    await gesture(garden.daemon, () => fireEvent.keyDown(reason, { key: 'Enter' }));
    expect(garden.daemon.sentOf('seed_check')).toEqual([
      { cmd: 'seed_check', request_id: expect.any(String), seed_id: 's-leaf11', check_id: 'C1', verb: 'reject', summary: 'Cite the run on da2b3b9.' },
    ]);
    expect(within(checkRow('C1')).queryByRole('textbox')).toBeNull();
    expect(within(checkRow('C1')).getByRole('button', { name: 'Confirm C1' })).toHaveFocus();
    await gesture(garden.daemon, () => garden.push([withCheck(leaf(), {
      ...C1, state: 'rejected', verdict: { confirmed: false, reason: 'Cite the run on da2b3b9.', at: AT },
    }), monitorPlot(), done, table]));
    expect(checkRow('C1')).toHaveTextContent('rejected by you: Cite the run on da2b3b9.');

    await gesture(garden.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Reject C5' })));
    await gesture(garden.daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
    expect(within(checkRow('C5')).queryByRole('textbox')).toBeNull();
    expect(within(checkRow('C5')).getByRole('button', { name: 'Confirm C5' })).toHaveFocus();
    expect(seedHeading()).toBe('View and home reactions');
  });

  it('reopens a reported check and unchecks a rejected one', async () => {
    const garden = await openLeaf();

    await gesture(garden.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Reopen C1' })));
    await gesture(garden.daemon, () => fireEvent.click(within(checkRow('C4')).getByRole('button', { name: 'Uncheck' })));

    expect(garden.daemon.sentOf('seed_check').map(({ check_id, verb }) => [check_id, verb])).toEqual([['C1', 'reopen'], ['C4', 'uncheck']]);

    let next = withCheck(leaf(), { ...C1, state: 'open', report: undefined });
    next = withCheck(next, { ...C4, state: 'reported', verdict: undefined });
    await gesture(garden.daemon, () => garden.push([next, monitorPlot(), done, table]));
    expect(within(checkRow('C1')).getByRole('button', { name: 'Confirm C1' })).toHaveTextContent('');
    expect(checkRow('C1')).not.toHaveTextContent('report');
    expect(within(checkRow('C4')).getByRole('button', { name: 'Confirm C4' })).toHaveTextContent('?');
    expect(checkRow('C4')).not.toHaveTextContent('rejected by');
  });

  it('shows the daemon’s refusal on the check it refused', async () => {
    const garden = await openLeaf();
    garden.daemon.on('seed_check', () => ({ event: 'seed_check_result', success: false, error: 'C5 is open: nothing to uncheck' }));

    await gesture(garden.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Confirm C5' })));

    expect(within(checkRow('C5')).getByRole('alert')).toHaveTextContent('C5 is open: nothing to uncheck');
  });

  it('rolls a plot’s shared check up over every inheriting seed, a harvested one included, and opens a seed’s check', async () => {
    const garden = await openGarden([leaf(), monitorPlot(), done, table]);
    answerChecks(garden);
    await openRow(garden.daemon, 'The monitor plot');

    expect(checkRow('C6')).toHaveTextContent('The plot glossary entry is written.');
    const shared = within(checkRow('C4')).getByRole('button', { name: /C4/ });
    expect(shared).toHaveTextContent('1 of 3 confirmed · 1 rejected');
    expect(shared).toHaveAttribute('aria-expanded', 'false');

    await gesture(garden.daemon, () => fireEvent.click(shared));
    const inheritors = within(screen.getByRole('list', { name: 'Seeds that prove C4' })).getAllByRole('button');
    expect(inheritors.map((line) => line.textContent)).toEqual([
      '✓Monitor containerharvested',
      '✕View and home reactionsgrowing',
      'Policy and tableplanted',
    ]);

    await gesture(garden.daemon, () => fireEvent.click(inheritors[1]));
    expect(seedHeading()).toBe('View and home reactions');
    expect(within(checkRow('C4')).getByRole('button', { name: 'Uncheck C4' })).toHaveFocus();
  });

  it('scrolls from the header tally to the checks section', async () => {
    if (!HTMLElement.prototype.scrollIntoView) HTMLElement.prototype.scrollIntoView = () => {};
    const scrolled = vi.spyOn(HTMLElement.prototype, 'scrollIntoView').mockImplementation(() => {});
    await openLeaf();

    const tally = within(gardenRegion()).getByRole('button', { name: 'Checks: 2 of 5 confirmed · 1 to judge · 1 rejected' });
    expect(tally).toHaveTextContent('✓2/5 · 1 to judge · 1 rejected');
    fireEvent.click(tally);

    expect(scrolled.mock.contexts[scrolled.mock.contexts.length - 1]).toBe(screen.getByRole('region', { name: 'Checks' }));
    expect(within(gardenRegion()).getByRole('heading', { name: 'Checks' })).toHaveFocus();
    scrolled.mockRestore();
  });

  it('counts a fresh report as to judge on child rows, plot progress and board cards', async () => {
    const quiet = { total: 5, reported: 0, confirmed: 2, rejected: 1 };
    const before = leaf([C8, C4, { ...C1, state: 'open', report: undefined }, C5, C2]);
    const garden = await openGarden([before, monitorPlot(quiet), done, table]);
    expect(row('The monitor plot')).toHaveTextContent('✓2/5 · 1 rejected');

    await openRow(garden.daemon, 'The monitor plot');
    expect(row('View and home reactions')).toHaveTextContent('✓2/5 · 1 rejected');
    expect(row('View and home reactions')).not.toHaveTextContent('to judge');

    await gesture(garden.daemon, () => garden.push([leaf([C8, C4, C1, C5, C2]), monitorPlot(), done, table]));
    expect(row('View and home reactions')).toHaveTextContent('✓2/5 · 1 to judge · 1 rejected');
    expect(within(gardenRegion()).getByRole('button', { name: /^Checks: 2 of 5 confirmed · 1 to judge/ }).closest('.garden-head__progress'))
      .toHaveTextContent('1/3 done');

    await gesture(garden.daemon, () => pressShortcut('board.open'));
    await gesture(garden.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'board' })));
    const card = document.querySelector<HTMLElement>('[data-seed="s-plot11"]');
    expect(card).toHaveTextContent('✓2/5 · 1 to judge · 1 rejected');
  });

  it('logs check notes as ordinary entries, outside the attachment bookkeeping', async () => {
    const garden = await openGarden([leaf(), monitorPlot(), done, table]);
    garden.documents['s-leaf11'] = {
      notes: [
        { id: 'n-check', seed_id: 's-leaf11', kind: 'check', body: 'C4 rejected: That run is on 869dc03.', author_session: '', author_member: 'trellis', created_at: AT },
        { id: 'n-attach', seed_id: 's-leaf11', kind: 'attach', body: 'attached run.log', author_session: '', author_member: '', created_at: AT },
      ],
      notes_total: 2,
    };
    await openRow(garden.daemon, 'The monitor plot');
    await openRow(garden.daemon, 'View and home reactions');

    const entry = log().getByText('C4 rejected: That run is on 869dc03.').closest('li')!;
    expect(entry).toHaveTextContent('trellischeck');
    expect(log().queryByText('attached run.log')).toBeNull();
  });

  it('counts a plot’s descendants without a link when the plot has no checks section of its own', async () => {
    const bare = plot('s-plot11', 'The monitor plot', '', { total: 3, done: 1, growing: 1, ready: 1, checks: TALLY });
    const garden = await openGarden([leaf(), bare, done, table]);

    await openRow(garden.daemon, 'The monitor plot');

    expect(within(gardenRegion()).queryByRole('heading', { name: 'Checks' })).toBeNull();
    expect(within(gardenRegion()).queryByRole('button', { name: /^Checks:/ })).toBeNull();
    expect(gardenRegion().querySelector('.garden-head__progress')).toHaveTextContent('✓2/5 · 1 to judge · 1 rejected');
  });

  it('renders no checks section and no tally for a seed without checks', async () => {
    const garden = await openGarden([planted('s-plain1', 'A plain seed', { status: 'growing' })]);
    expect(row('A plain seed')).not.toHaveTextContent('✓');

    await openRow(garden.daemon, 'A plain seed');

    expect(within(gardenRegion()).queryByRole('heading', { name: 'Checks' })).toBeNull();
    expect(within(gardenRegion()).queryByRole('button', { name: /^Checks:/ })).toBeNull();
  });
});
