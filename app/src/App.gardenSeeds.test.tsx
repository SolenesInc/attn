import { fireEvent, screen, within } from '@testing-library/react';
import { openUrl } from '@tauri-apps/plugin-opener';
import { describe, expect, it } from 'vitest';
import {
  defaultProfile,
  emptyDesktop,
  agentPane,
  soloDesktop,
  daemonSeed,
  daemonSession,
  daemonDesktop,
  dockTiles,
  seedDocument,
  type DaemonSeed,
  type DaemonSeedDocument,
} from './test/daemonFixtures';
import { openGarden } from './test/garden';
import { gesture, renderApp } from './test/renderApp';
import { initialState } from './test/scriptedDaemon';

const layout = { sessions: [daemonSession('s1')], desktops: [soloDesktop('s1')] };

const PLAN = daemonSeed('s-plan11', { title: 'The plan', body: '## Rendered plan\n\nRead **this**.',
tender: { ref: 'member:trellis', name: "Trellis" },
claimed: true });

function tiledDesktop(tiles: Parameters<typeof dockTiles>[1]) {
  return daemonDesktop('ws', { root: dockTiles({ type: 'pane', pane_id: 'pane-s1' }, tiles), panes: [agentPane('s1', 'ws')] }, { name: 'ws' });
}

async function openSeedTile(seed: DaemonSeed, document: Partial<DaemonSeedDocument> = {}) {
  const view = await renderApp({
    initialState: { sessions: [daemonSession('s1')], desktops: [tiledDesktop([])], seeds: [seed] },
  });
  view.daemon.on('seed_document_get', () => ({ event: 'seed_document_get_result', success: true, document: seedDocument(seed, document) }));
  await gesture(view.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s1' })));
  await gesture(view.daemon, () => view.daemon.arrange((desktops) => desktops.map((desktop) => (
    desktop.id === 'ws' ? { ...tiledDesktop([{ tile_id: 'tile-seed', tile_kind: 'seed', tile_params: seed.id }]), revision: desktop.revision + 1 } : desktop
  ))));
  return view.daemon;
}

const seedDetails = () => screen.getByLabelText('Seed details');
const artifacts = () => screen.queryByRole('region', { name: 'Artifacts' });

function note(id: string, overrides: Partial<DaemonSeedDocument['notes'][number]> = {}): DaemonSeedDocument['notes'][number] {
  return { id, seed_id: PLAN.id, kind: 'note', body: '',  author: { ref: 'user', name: 'the user' }, created_at: '2026-08-15T09:00:00Z', ...overrides };
}

describe('App garden seeds', () => {
  it('seeds the garden from initial_state', async () => {
    await openGarden([daemonSeed('s-aaa111', { title: 'already planted' })]);

    expect(screen.getByText('already planted')).toBeInTheDocument();
  });

  it('replaces the garden on every planting broadcast', async () => {
    const { daemon } = await openGarden([daemonSeed('s-aaa111', { title: 'already planted' })]);

    daemon.emit({
      event: 'garden_seeds_updated', profile_id: 'profile-default',
      seeds: [daemonSeed('s-bbb222', { title: 'just planted' }), daemonSeed('s-aaa111', { title: 'already planted' })],
      total: 2,
    });

    expect(screen.getByText('just planted')).toBeInTheDocument();
    expect(screen.getByText('already planted')).toBeInTheDocument();
  });

  it('replaces the garden on profile switches and ignores late snapshots from the old profile', async () => {
    const alpha = daemonSeed('s-alpha1', { title: 'Default work' });
    const beta = daemonSeed('s-beta11', { title: 'Side work', profile_id: 'profile-side' });
    const { daemon } = await openGarden([alpha]);
    expect(screen.getByText('Default work')).toBeInTheDocument();

    await gesture(daemon, () => daemon.emit({
      event: 'profile_arrangement_changed',
      profile: defaultProfile('side-desktop', { id: 'profile-side', name: 'Side' }),
      desktops: [emptyDesktop('side-desktop', { profile_id: 'profile-side' })],
    }));
    expect(screen.queryByText('Default work')).toBeNull();
    daemon.emit({ event: 'garden_seeds_updated', profile_id: 'profile-side', seeds: [beta], total: 1 });
    expect(screen.getByText('Side work')).toBeInTheDocument();
    daemon.emit({ event: 'garden_seeds_updated', profile_id: 'profile-default', seeds: [alpha], total: 1 });
    expect(screen.queryByText('Default work')).toBeNull();
    expect(screen.getByText('Side work')).toBeInTheDocument();

    daemon.emit({ event: 'garden_seeds_updated', profile_id: 'profile-side', seeds: [], total: 0 });
    expect(screen.queryByText('Side work')).toBeNull();
  });

  it('applies a change onto the garden it holds, in the order the daemon sends', async () => {
    const older = daemonSeed('s-aaa111', { title: 'older seed' });
    const newer = daemonSeed('s-bbb222', { title: 'newer seed' });
    const { daemon } = await openGarden([newer, older]);

    await gesture(daemon, () => daemon.emit({
      event: 'garden_seeds_changed', profile_id: 'profile-default', order: ['s-ccc333', newer.id, older.id],
      seeds: [daemonSeed('s-ccc333', { title: 'just planted' }), { ...newer, title: 'newer seed, renamed' }], total: 3,
    }));
    expect(screen.queryByText('newer seed')).toBeNull();
    const titles = ['just planted', 'newer seed, renamed', 'older seed'].map((title) => screen.getByText(title));
    expect(titles[0].compareDocumentPosition(titles[1]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(titles[1].compareDocumentPosition(titles[2]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

    await gesture(daemon, () => daemon.emit({
      event: 'garden_seeds_changed', profile_id: 'profile-default', order: ['s-ccc333', older.id], seeds: [], total: 2,
    }));
    expect(screen.queryByText('newer seed, renamed')).toBeNull();
    expect(screen.getByText('just planted')).toBeInTheDocument();
    expect(screen.getByText('older seed')).toBeInTheDocument();
  });

  it('reads a garden-less daemon as an empty garden', async () => {
    const { daemon } = await openGarden([daemonSeed('s-aaa111', { title: 'already planted' })]);
    expect(screen.getByText('already planted')).toBeInTheDocument();

    daemon.on('client_hello', () => initialState(layout));
    await daemon.reconnect();

    expect(screen.queryByText('already planted')).toBeNull();
  });

  it('carries how many seeds the garden holds, not just the ones it sent', async () => {
    const { daemon } = await openGarden([]);

    daemon.emit({ event: 'garden_seeds_updated', profile_id: 'profile-default', seeds: [daemonSeed('s-bbb222', { title: 'the newest one' })], total: 1421 });

    expect(screen.getByText('The garden holds 1421 seeds; this panel has the newest 1.')).toBeInTheDocument();

    daemon.emit({ event: 'garden_seeds_updated', profile_id: 'profile-default', seeds: [daemonSeed('s-bbb222', { title: 'the newest one' })], total: 1 });

    expect(screen.queryByText(/The garden holds/)).toBeNull();
  });

  it('reads a seed tile from its status, through its plot, to its plan and a log of what is withheld', async () => {
    const child = daemonSeed('s-step11', { title: 'Build the reader', status: 'harvested' });
    const plot = { ...PLAN, plot_progress: { total: 1, done: 1, withered: 0, growing: 0, dormant: 0, ready: 0, blocked: 0 } };
    await openSeedTile(plot, { children: [child], notes: [note('n-one111', { body: 'Verified the **reader**.' })], notes_total: 2 });

    const plotHeading = screen.getByRole('heading', { name: 'Plot' });
    const body = screen.getByRole('heading', { name: 'Rendered plan' });
    expect(seedDetails().compareDocumentPosition(plotHeading) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(plotHeading.compareDocumentPosition(body) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByRole('button', { name: /Build the reader/ })).toHaveTextContent('done');

    fireEvent.click(screen.getByText('Log').closest('summary')!);
    expect(screen.getByText('reader', { selector: 'strong' })).toBeInTheDocument();
    expect(screen.getByText('1 more entry on the log.')).toBeInTheDocument();
  });

  it('lists a seed tile’s checks under its plan, with the header tally, and moves one from the tile', async () => {
    const checked = { ...PLAN, checks: [
      { id: 'C1', text: 'Wire tests cover the reader.', removed: false, state: 'reported',
        report: { summary: 'Reader wire tests pass', evidence: [], by: { ref: 'member:trellis', name: 'Trellis' }, at: '2026-08-15T09:00:00Z' } },
      { id: 'C2', text: 'The reader is recorded.', removed: false, state: 'open' },
    ] };
    const daemon = await openSeedTile(checked);
    daemon.on('seed_check', () => ({ event: 'seed_check_result', success: true, seed: checked }));

    expect(within(seedDetails()).getByRole('button', { name: 'Checks: 0 of 2 confirmed · 1 to judge' })).toHaveTextContent('✓0/2 · 1 to judge');
    const checks = within(screen.getByRole('region', { name: 'Checks' }));
    expect(screen.getByRole('heading', { name: 'Rendered plan' }).compareDocumentPosition(checks.getByText('Wire tests cover the reader.'))
      & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(checks.getByText('Reader wire tests pass')).toBeInTheDocument();

    await gesture(daemon, () => fireEvent.click(checks.getByRole('button', { name: 'Confirm C1' })));
    expect(daemon.sentOf('seed_check')).toEqual([
      { cmd: 'seed_check', request_id: expect.any(String), seed_id: PLAN.id, check_id: 'C1', verb: 'confirm' },
    ]);
  });

  it('opens an inheriting seed’s check from a plot tile’s shared-check row', async () => {
    const leaf = daemonSeed('s-leaf11', { title: 'The view', edges: [{ kind: 'part-of', to: 's-plot11' }], checks: [
      { id: 'C4', text: 'CI is green on the head.', removed: false, state: 'open', from: 's-plot11' },
    ] });
    const crown = daemonSeed('s-plot11', {
      title: 'The monitor plot', body: '# A long plan',
      plot_progress: { total: 1, done: 0, withered: 0, growing: 1, dormant: 0, ready: 0, blocked: 0, checks: { total: 1, reported: 0, confirmed: 0, rejected: 0 } },
      shared_checks: [{ id: 'C4', text: 'CI is green on the head.', removed: false, seeds: [{ seed_id: leaf.id, state: 'open', has_report: false }] }],
    });
    const daemon = await openSeedTile(crown);
    daemon.emit({ event: 'garden_seeds_changed', profile_id: crown.profile_id, order: [crown.id, leaf.id], seeds: [leaf], total: 2 });
    daemon.on('seed_document_get', ({ seed_id }) => ({ event: 'seed_document_get_result', success: true,
      document: seedDocument(seed_id === leaf.id ? leaf : crown, seed_id === crown.id ? { children: [leaf] } : {}) }));
    daemon.on('desktop_update_tile', () => undefined);

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /C4.*0 of 1 confirmed/ })));
    await gesture(daemon, () => fireEvent.click(within(screen.getByRole('list', { name: 'Seeds that prove C4' })).getByRole('button')));

    expect(daemon.sentOf('desktop_update_tile')).toEqual([expect.objectContaining({ tile_params: leaf.id })]);
    expect(screen.getByRole('button', { name: 'Confirm C4' })).toHaveFocus();
  });

  it('refreshes a seed tile on garden changes that carry no seed, on reconnect, and ignores obsolete responses', async () => {
    const daemon = await openSeedTile(PLAN, { notes: [note('n-before', { body: 'Before the keep pass' })], notes_total: 1 });
    fireEvent.click(screen.getByText('Log').closest('summary')!);
    daemon.on('seed_document_get', () => undefined);
    await gesture(daemon, () => daemon.emit({ event: 'kept_conversations_changed' }));
    expect(daemon.sentOf('seed_document_get')).toHaveLength(1);
    await gesture(daemon, () => daemon.emit({ event: 'garden_seeds_changed', profile_id: PLAN.profile_id, order: [PLAN.id], seeds: [], total: 1 }));
    const older = daemon.sentOf('seed_document_get')[1];
    daemon.on('seed_document_get', () => ({ event: 'seed_document_get_result', success: true,
      document: seedDocument(PLAN, { notes: [note('n-after', { body: 'After the keep pass' })], notes_total: 1 }) }));
    await gesture(daemon, () => daemon.emit({ event: 'kept_conversations_changed' }));
    expect(daemon.sentOf('seed_document_get')).toHaveLength(2);
    await gesture(daemon, () => daemon.emit({ event: 'garden_seeds_changed', profile_id: PLAN.profile_id, order: [PLAN.id], seeds: [], total: 1 }));
    await gesture(daemon, () => daemon.replyTo(older, { event: 'seed_document_get_result', success: true,
      request_id: older.request_id,
      document: seedDocument(PLAN, { notes: [note('n-old', { body: 'Superseded detail' })], notes_total: 1 }) }));
    expect(screen.getByText('After the keep pass')).toBeInTheDocument();
    expect(screen.queryByText('Superseded detail')).toBeNull();
    expect(daemon.sentOf('seed_document_get')).toEqual(Array.from({ length: 3 }, () => ({
      cmd: 'seed_document_get', seed_id: PLAN.id, request_id: expect.any(String),
    })));

    daemon.on('seed_document_get', () => ({ event: 'seed_document_get_result', success: true,
      document: seedDocument(PLAN, { notes: [note('n-reconnected', { body: 'After reconnect' })], notes_total: 1 }) }));
    await daemon.reconnect();
    expect(screen.getByText('After reconnect')).toBeInTheDocument();
    expect(daemon.sentOf('seed_document_get').slice(3)).toEqual([
      { cmd: 'seed_document_get', seed_id: PLAN.id, request_id: expect.any(String) },
    ]);
  });

  it('lists only the daemon’s current artifacts and opens a markdown one', async () => {
    const current = { kind: 'markdown_file', path: '/repo/current.md' };
    const daemon = await openSeedTile(PLAN, {
      notes: [
        note('n-detach', { kind: 'detach', artifact: { kind: 'markdown_file', path: '/repo/old.md' } }),
        note('n-current', { kind: 'attach', artifact: current }),
      ],
      notes_total: 2,
      references: [current, { kind: 'notebook', notebook_document_id: 'nb-plan-7' }, { kind: 'url', url: 'https://example.test/pr/1' }],
    });

    const listed = within(artifacts()!);
    expect(listed.queryByRole('button', { name: /old\.md/ })).toBeNull();
    expect(listed.getByText('nb-plan-7').closest('li')).toHaveTextContent('notebook');
    expect(listed.getByRole('link', { name: /example\.test/ })).toHaveAttribute('href', 'https://example.test/pr/1');
    const artifact = listed.getByRole('button', { name: /current\.md/ });
    expect(artifact.closest('li')).toHaveTextContent('linked file');
    await gesture(daemon, () => fireEvent.click(artifact));

    expect(daemon.sentOf('open_markdown')).toEqual([expect.objectContaining({ path: '/repo/current.md' })]);
  });

  it('shows no artifacts and no harvest condition for a seed that has neither', async () => {
    await openSeedTile(PLAN, { notes: [note('n-plain1', { body: 'No attachment here.' })], notes_total: 1 });

    expect(artifacts()).toBeNull();
    expect(seedDetails()).not.toHaveTextContent(/harvests when/);
  });

  it('says the harvest condition beside the state and opens the pull request', async () => {
    const url = 'https://github.com/victorarias/attn/pull/42';
    await openSeedTile({ ...PLAN, status: 'dormant',
    tender: undefined,
    claimed: false,
    resume_available: false, harvest_when: { set_by: {ref:'user',name:'the user'},  pull_request: 'github.com:victorarias/attn#42', url, set_at: '2026-09-02T10:00:00Z' } });

    const link = within(seedDetails()).getByRole('link', { name: /harvests when victorarias\/attn#42 merges/ });
    expect(link).toHaveAttribute('href', url);
    fireEvent.click(link);

    expect(openUrl).toHaveBeenCalledWith(url);
  });
});
