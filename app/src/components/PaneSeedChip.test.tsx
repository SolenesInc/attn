import { describe, expect, it } from 'vitest';
import { fireEvent, screen, within } from '@testing-library/react';
import {
  agentWorkspace,
  daemonSeed,
  daemonSession,
  seedDocument,
  type DaemonSeed,
  type DaemonSeedDocument,
  type DaemonSession,
} from '../test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from '../test/renderApp';
import type { Reply, ScriptedDaemon } from '../test/scriptedDaemon';

async function openAgent(seeds: DaemonSeed[], session: Partial<DaemonSession> = {}) {
  const { daemon } = await renderApp({
    initialState: { sessions: [daemonSession('s1', session)], workspaces: [agentWorkspace('s1')], seeds },
  });
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s1' })));
  return daemon;
}

const chip = () => screen.getByTestId('seed-chip-s1');

async function showSeeds(daemon: ScriptedDaemon) {
  pressShortcut('ui.actionMenu');
  await gesture(daemon, () => fireEvent.click(screen.getByRole('option', { name: /Show s1's seeds/ })));
}

const openedSeeds = (daemon: ScriptedDaemon) =>
  daemon.sentOf('open_seed').map(({ seed_id, session_id }) => ({ seed_id, session_id }));

const tendingTwo = [
  daemonSeed('s-a', { title: 'first', tender_session: 's1' }),
  daemonSeed('s-b', { title: 'second', tender_session: 's1' }),
];

describe('PaneSeedChip', () => {
  it('shows a tended seed and opens it beside the agent on click', async () => {
    const daemon = await openAgent([daemonSeed('s-work11', { title: 'move the wire', tender_session: 's1' })]);

    expect(within(chip()).getByText('move the wire')).toBeInTheDocument();
    expect(within(chip()).getByText('Growing')).toBeInTheDocument();
    expect(chip()).toHaveAttribute('data-seed-id', 's-work11');
    expect(screen.queryByTestId('seed-chip-unread-s1')).not.toBeInTheDocument();

    await gesture(daemon, () => fireEvent.click(chip()));
    expect(openedSeeds(daemon)).toEqual([{ seed_id: 's-work11', session_id: 's1' }]);
  });

  it('falls back to the reporting seed id when the seed is not in the pushed list', async () => {
    await openAgent([], { seed_id: 's-late11', ticket_unread: true });

    expect(within(chip()).getByText('s-late11')).toBeInTheDocument();
    expect(screen.getByTestId('seed-chip-unread-s1')).toBeInTheDocument();
  });

  it('shows the plot with its progress and pins the popover on click', async () => {
    const daemon = await openAgent([
      daemonSeed('s-plot11', {
        title: 'the arc',
        plot_progress: { done: 2, total: 5, ready: 1, growing: 1, blocked: 1, dormant: 0, withered: 0 },
      }),
      daemonSeed('s-a', { title: 'first step', tender_session: 's1', edges: [{ kind: 'part-of', to: 's-plot11' }] }),
      daemonSeed('s-b', { title: 'second step', tender_session: 's1', edges: [{ kind: 'part-of', to: 's-plot11' }] }),
    ]);

    expect(within(chip()).getByText('the arc')).toBeInTheDocument();
    expect(within(chip()).getByText('2/5')).toBeInTheDocument();

    fireEvent.click(chip());
    expect(screen.getByRole('listbox', { name: 'Seeds this agent is tending' })).toBeInTheDocument();
    expect(screen.getByText('first step')).toBeInTheDocument();

    fireEvent.click(chip());
    await daemon.idle();
    expect(screen.queryByRole('listbox', { name: 'Seeds this agent is tending' })).not.toBeInTheDocument();
    expect(daemon.sentOf('open_seed')).toEqual([]);
  });

  it('dismisses the pinned popover on an outside pointerdown and pins it again on request', async () => {
    const daemon = await openAgent(tendingTwo);
    await showSeeds(daemon);
    expect(screen.getByRole('listbox', { name: 'Seeds this agent is tending' })).toBeInTheDocument();

    fireEvent.pointerDown(document.body);
    expect(screen.queryByRole('listbox', { name: 'Seeds this agent is tending' })).not.toBeInTheDocument();

    await showSeeds(daemon);
    expect(screen.getByRole('listbox', { name: 'Seeds this agent is tending' })).toBeInTheDocument();
  });

  it('keeps the pinned popover open when the pointerdown lands inside it', async () => {
    const daemon = await openAgent(tendingTwo);
    await showSeeds(daemon);

    fireEvent.pointerDown(screen.getByRole('listbox', { name: 'Seeds this agent is tending' }));
    expect(screen.getByRole('listbox', { name: 'Seeds this agent is tending' })).toBeInTheDocument();
  });

  it('opens a seed from the pinned popover with the keyboard', async () => {
    const daemon = await openAgent(tendingTwo);
    await showSeeds(daemon);

    expect(within(chip()).getByText('tending 2')).toBeInTheDocument();
    const listbox = screen.getByRole('listbox', { name: 'Seeds this agent is tending' });
    fireEvent.keyDown(listbox, { key: 'ArrowDown' });
    await gesture(daemon, () => fireEvent.keyDown(listbox, { key: 'Enter' }));
    expect(openedSeeds(daemon)).toEqual([{ seed_id: 's-b', session_id: 's1' }]);
    expect(screen.queryByRole('listbox', { name: 'Seeds this agent is tending' })).not.toBeInTheDocument();
  });
});

function documentResult(document: DaemonSeedDocument): Reply {
  return { event: 'seed_document_get_result', success: true, document };
}

function documentFor(value: DaemonSeed, body = 'Leaves look good at header size.'): DaemonSeedDocument {
  return seedDocument(value, {
    notes_total: 1,
    notes: [{ id: 'n-1', seed_id: value.id, body, kind: 'note', author_member: '', author_session: '', created_at: value.updated_at }],
  });
}

function pushSeeds(daemon: ScriptedDaemon, seeds: DaemonSeed[]) {
  daemon.emit({ event: 'garden_seeds_updated', seeds, total: seeds.length });
}

describe('seed lifecycle and context', () => {
  it.each(['planted', 'dormant', 'harvested', 'withered'])('keeps %s visible when tending ends', async (status) => {
    const value = daemonSeed('s-work11', { title: 'Garden icons', tender_session: 's1' });
    const daemon = await openAgent([value], { seed_id: value.id });
    pushSeeds(daemon, [{ ...value, status, tender_session: '' }]);
    await daemon.idle();

    const stateLabel = status[0].toUpperCase() + status.slice(1);
    expect(chip()).toHaveAttribute('data-kind', 'crown');
    expect(chip()).toHaveAttribute('data-status', status);
    expect(within(chip()).getByText(stateLabel)).toBeVisible();
    fireEvent.keyDown(chip(), { key: 'ArrowDown' });
    const context = screen.getByRole('dialog', { name: 'Seed context' });
    expect(within(context).getByText(stateLabel)).toBeVisible();
    expect(within(context).getByText('This agent reports to this seed.')).toBeVisible();
  });

  it('does not invent a state for an unavailable reporting seed', async () => {
    await openAgent([], { seed_id: 's-missing' });
    expect(within(chip()).getByText('Unknown')).toBeVisible();
  });

  it('loads a real note only when opened, filters artifact activity, and keeps the outcome', async () => {
    const value = daemonSeed('s-work11', { title: 'Garden icons', status: 'harvested', reason: 'All five states are legible.' });
    const doc = documentFor(value);
    doc.notes.push({ ...doc.notes[0], id: 'n-2', kind: 'attach', body: 'attached screenshot', created_at: '2099-01-01T00:00:00Z' });
    const daemon = await openAgent([value], { seed_id: value.id });
    daemon.on('seed_document_get', () => documentResult(doc));
    await daemon.idle();
    expect(daemon.sentOf('seed_document_get')).toEqual([]);

    await gesture(daemon, () => fireEvent.keyDown(chip(), { key: 'ArrowDown' }));
    expect(screen.getByText('Leaves look good at header size.')).toBeVisible();
    expect(screen.getByText('All five states are legible.')).toBeVisible();
    expect(screen.queryByText('attached screenshot')).not.toBeInTheDocument();
    expect(daemon.sentOf('seed_document_get').map((command) => command.seed_id)).toEqual([value.id]);
    fireEvent.keyDown(screen.getByRole('dialog', { name: 'Seed context' }), { key: 'Escape' });
    expect(screen.queryByRole('dialog', { name: 'Seed context' })).not.toBeInTheDocument();
  });

  it('ignores a late note response after a lifecycle revision', async () => {
    const value = daemonSeed('s-work11', { title: 'Garden icons' });
    const revised = { ...value, rev: 2, status: 'harvested' };
    const daemon = await openAgent([value], { seed_id: value.id });
    daemon.on('seed_document_get', () => (daemon.sentOf('seed_document_get').length === 1 ? undefined : documentResult(documentFor(revised, 'Finished and verified.'))));
    await showSeeds(daemon);
    pushSeeds(daemon, [revised]);
    await daemon.idle();
    expect(screen.getByText('Finished and verified.')).toBeVisible();

    const [late] = daemon.sentOf('seed_document_get');
    daemon.emit({ ...documentResult(documentFor(value, 'Still exploring.')), request_id: late.request_id });
    await daemon.idle();
    expect(screen.queryByText('Still exploring.')).not.toBeInTheDocument();
    expect(daemon.sentOf('seed_document_get')).toHaveLength(2);
  });

  it('keeps opening the seed available after a context fetch fails', async () => {
    const value = daemonSeed('s-work11', { title: 'Garden icons', tender_session: 's1' });
    const daemon = await openAgent([value]);
    daemon.on('seed_document_get', () => ({ event: 'seed_document_get_result', success: false, error: 'offline' }));
    await showSeeds(daemon);
    expect(screen.getByText('Latest note unavailable.')).toBeVisible();

    await gesture(daemon, () => fireEvent.keyDown(screen.getByRole('dialog', { name: 'Seed context' }), { key: 'Enter' }));
    expect(openedSeeds(daemon)).toEqual([{ seed_id: value.id, session_id: 's1' }]);
  });

  it('refreshes a visible note when a Garden snapshot arrives at the same seed revision', async () => {
    const value = daemonSeed('s-work11', { title: 'Garden icons', tender_session: 's1' });
    const notes = ['First note.', 'A new observation.'];
    const daemon = await openAgent([value]);
    daemon.on('seed_document_get', () => documentResult(documentFor(value, notes.shift())));
    await showSeeds(daemon);
    expect(screen.getByText('First note.')).toBeVisible();

    pushSeeds(daemon, [{ ...value }]);
    await daemon.idle();
    expect(screen.getByText('A new observation.')).toBeVisible();
    expect(daemon.sentOf('seed_document_get')).toHaveLength(2);
  });
});
