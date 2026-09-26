import { fireEvent, screen, within } from '@testing-library/react';
import { openUrl } from '@tauri-apps/plugin-opener';
import { describe, expect, it } from 'vitest';
import {
  agentPane,
  agentWorkspace,
  daemonSeed,
  daemonSession,
  daemonWorkspace,
  dockTiles,
  seedDocument,
  type DaemonSeed,
  type DaemonSeedDocument,
} from './test/daemonFixtures';
import { openGarden } from './test/garden';
import { gesture, renderApp } from './test/renderApp';
import { initialState } from './test/scriptedDaemon';

const layout = { sessions: [daemonSession('s1')], workspaces: [agentWorkspace('s1')] };

const PLAN = daemonSeed('s-plan11', { title: 'The plan', body: '## Rendered plan\n\nRead **this**.', tender_member: 'trellis' });

function tiledWorkspace(tiles: Parameters<typeof dockTiles>[1]) {
  return daemonWorkspace('ws', { root: dockTiles({ type: 'pane', pane_id: 'pane-s1' }, tiles), panes: [agentPane('s1', 'ws')] }, { title: 'ws' });
}

async function openSeedTile(seed: DaemonSeed, document: Partial<DaemonSeedDocument> = {}) {
  const view = await renderApp({
    initialState: { sessions: [daemonSession('s1', { workspace_id: 'ws' })], workspaces: [tiledWorkspace([])], seeds: [seed] },
  });
  view.daemon.on('seed_document_get', () => ({ event: 'seed_document_get_result', success: true, document: seedDocument(seed, document) }));
  await gesture(view.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s1' })));
  await gesture(view.daemon, () => view.daemon.emit({
    event: 'workspace_layout_updated',
    workspace_layout: tiledWorkspace([{ tile_id: 'tile-seed', tile_kind: 'seed', tile_params: seed.id }]).layout!,
  }));
  return view.daemon;
}

const seedDetails = () => screen.getByLabelText('Seed details');
const artifacts = () => screen.queryByRole('region', { name: 'Artifacts' });

function note(id: string, overrides: Partial<DaemonSeedDocument['notes'][number]> = {}): DaemonSeedDocument['notes'][number] {
  return { id, seed_id: PLAN.id, kind: 'note', body: '', author_session: '', author_member: '', created_at: '2026-08-15T09:00:00Z', ...overrides };
}

describe('App garden seeds', () => {
  it('seeds the garden from initial_state', async () => {
    await openGarden([daemonSeed('s-aaa111', { title: 'already planted' })]);

    expect(screen.getByText('already planted')).toBeInTheDocument();
  });

  it('replaces the garden on every planting broadcast', async () => {
    const { daemon } = await openGarden([daemonSeed('s-aaa111', { title: 'already planted' })]);

    daemon.emit({
      event: 'garden_seeds_updated',
      seeds: [daemonSeed('s-bbb222', { title: 'just planted' }), daemonSeed('s-aaa111', { title: 'already planted' })],
      total: 2,
    });

    expect(screen.getByText('just planted')).toBeInTheDocument();
    expect(screen.getByText('already planted')).toBeInTheDocument();
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

    daemon.emit({ event: 'garden_seeds_updated', seeds: [daemonSeed('s-bbb222', { title: 'the newest one' })], total: 1421 });

    expect(screen.getByText('The garden holds 1421 seeds; this panel has the newest 1.')).toBeInTheDocument();

    daemon.emit({ event: 'garden_seeds_updated', seeds: [daemonSeed('s-bbb222', { title: 'the newest one' })], total: 1 });

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
    await openSeedTile({ ...PLAN, status: 'dormant', tender_member: '', harvest_when: { pull_request: 'github.com:victorarias/attn#42', url, set_at: '2026-09-02T10:00:00Z' } });

    const link = within(seedDetails()).getByRole('link', { name: /harvests when victorarias\/attn#42 merges/ });
    expect(link).toHaveAttribute('href', url);
    fireEvent.click(link);

    expect(openUrl).toHaveBeenCalledWith(url);
  });
});
