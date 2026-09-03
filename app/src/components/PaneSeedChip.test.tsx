import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { PaneSeedChip } from './PaneSeedChip';
import type { Seed, SeedDocument } from '../hooks/useDaemonSocket';
import { DaemonApiProvider, type DaemonApi } from '../contexts/DaemonApiContext';
import { derivePaneSeedDisplay } from './paneSeedDisplay';

function seed(overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return {
    body: '',
    status: 'growing',
    state_changed_at: new Date().toISOString(),
    state_changed_at_exact: true,
    step_slug: overrides.title,
    planter_session: '',
    planter_member: '',
    tender_session: '',
    tender_member: '',
    edges: [],
    ready: false,
    template: false,
    gate: false,
    vars: [],
    rev: 1,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  };
}

describe('PaneSeedChip', () => {
  it('shows a tended seed and opens it on click', () => {
    const onOpenSeed = vi.fn();
    render(
      <PaneSeedChip
        display={{ kind: 'seed', seed: seed({ id: 's-work11', title: 'move the wire' }) }}
        unread={false}
        sessionId="sess-a"
        pinned={false}
        onOpenSeed={onOpenSeed}
        onPopoverClosed={vi.fn()}
      />,
    );

    expect(screen.getByText('move the wire')).toBeInTheDocument();
    expect(screen.getByText('Growing')).toBeInTheDocument();
    expect(screen.getByTestId('seed-chip-sess-a')).toHaveAttribute('data-seed-id', 's-work11');
    expect(screen.queryByTestId('seed-chip-unread-sess-a')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('seed-chip-sess-a'));
    expect(onOpenSeed).toHaveBeenCalledWith('s-work11');
  });

  it('falls back to the crown id when the seed is not in the pushed list', () => {
    render(
      <PaneSeedChip
        display={{ kind: 'crown', seedId: 's-late11' }}
        crownSeedId="s-late11"
        unread
        sessionId="sess-b"
        pinned={false}
        onOpenSeed={vi.fn()}
        onPopoverClosed={vi.fn()}
      />,
    );

    expect(screen.getByText('s-late11')).toBeInTheDocument();
    expect(screen.getByTestId('seed-chip-unread-sess-b')).toBeInTheDocument();
  });

  it('shows the plot with its progress and pins the popover on click', () => {
    const onOpenSeed = vi.fn();
    render(
      <PaneSeedChip
        display={{
          kind: 'plot',
          plot: seed({
            id: 's-plot11',
            title: 'the arc',
            plot_progress: { done: 2, total: 5, ready: 1, growing: 1, blocked: 1, dormant: 0, withered: 0 },
          }),
          tended: [seed({ id: 's-a', title: 'first step', tender_session: 'sess-c' })],
        }}
        unread={false}
        sessionId="sess-c"
        pinned={false}
        onOpenSeed={onOpenSeed}
        onPopoverClosed={vi.fn()}
      />,
    );

    expect(screen.getByText('the arc')).toBeInTheDocument();
    expect(screen.getByText('2/5')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('seed-chip-sess-c'));
    expect(onOpenSeed).not.toHaveBeenCalled();
    expect(screen.getByRole('listbox')).toBeInTheDocument();
    expect(screen.getByText('first step')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('seed-chip-sess-c'));
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
    expect(onOpenSeed).not.toHaveBeenCalled();
  });

  it('dismisses the pinned popover on an outside pointerdown', () => {
    const onPopoverClosed = vi.fn();
    render(
      <PaneSeedChip
        display={{
          kind: 'multi',
          tended: [
            seed({ id: 's-a', title: 'first', tender_session: 'sess-e' }),
            seed({ id: 's-b', title: 'second', tender_session: 'sess-e' }),
          ],
        }}
        unread={false}
        sessionId="sess-e"
        pinned
        onOpenSeed={vi.fn()}
        onPopoverClosed={onPopoverClosed}
      />,
    );

    expect(screen.getByRole('listbox')).toBeInTheDocument();
    fireEvent.pointerDown(document.body);
    expect(onPopoverClosed).toHaveBeenCalled();
  });

  it('keeps the pinned popover open when the pointerdown lands inside it', () => {
    const onPopoverClosed = vi.fn();
    render(
      <PaneSeedChip
        display={{
          kind: 'multi',
          tended: [
            seed({ id: 's-a', title: 'first', tender_session: 'sess-f' }),
            seed({ id: 's-b', title: 'second', tender_session: 'sess-f' }),
          ],
        }}
        unread={false}
        sessionId="sess-f"
        pinned
        onOpenSeed={vi.fn()}
        onPopoverClosed={onPopoverClosed}
      />,
    );

    fireEvent.pointerDown(screen.getByRole('listbox'));
    expect(onPopoverClosed).not.toHaveBeenCalled();
  });

  it('renders the pinned popover with keyboard navigation', () => {
    const onOpenSeed = vi.fn();
    const onPopoverClosed = vi.fn();
    render(
      <PaneSeedChip
        display={{
          kind: 'multi',
          tended: [
            seed({ id: 's-a', title: 'first', tender_session: 'sess-d' }),
            seed({ id: 's-b', title: 'second', tender_session: 'sess-d' }),
          ],
        }}
        unread={false}
        sessionId="sess-d"
        pinned
        onOpenSeed={onOpenSeed}
        onPopoverClosed={onPopoverClosed}
      />,
    );

    expect(screen.getByText('tending 2')).toBeInTheDocument();
    const listbox = screen.getByRole('listbox');
    fireEvent.keyDown(listbox, { key: 'ArrowDown' });
    fireEvent.keyDown(listbox, { key: 'Enter' });
    expect(onOpenSeed).toHaveBeenCalledWith('s-b');
    expect(onPopoverClosed).toHaveBeenCalled();
  });
});

const props = { unread: false, sessionId: 'sess-a', pinned: false, onOpenSeed: vi.fn(), onPopoverClosed: vi.fn() };

function documentFor(value: Seed, body = 'Leaves look good at header size.'): SeedDocument {
  return {
    seed: value, children: [], artifacts: [], references: [], notes_total: 1, tender_holds: false,
    notes: [{ id: 'n-1', seed_id: value.id, body, kind: 'note', author_member: '', author_session: '', created_at: value.updated_at }],
  };
}

describe('seed lifecycle and context', () => {
  it.each(['planted', 'dormant', 'harvested', 'withered'])('keeps %s visible when tending ends', (status) => {
    const value = seed({ id: 's-work11', title: 'Garden icons', tender_session: props.sessionId });
    const { rerender } = render(<PaneSeedChip {...props} crownSeedId={value.id} display={derivePaneSeedDisplay([value], props.sessionId, value.id)} />);
    const ended = { ...value, status, tender_session: '' };
    rerender(<PaneSeedChip {...props} crownSeedId={value.id} display={derivePaneSeedDisplay([ended], props.sessionId, value.id)} />);
    const chip = screen.getByTestId('seed-chip-sess-a');
    expect(chip).toHaveAttribute('data-kind', 'crown');
    expect(chip).toHaveAttribute('data-status', status);
    expect(within(chip).getByText(status[0].toUpperCase() + status.slice(1))).toBeVisible();
    fireEvent.keyDown(chip, { key: 'ArrowDown' });
    const context = screen.getByRole('dialog', { name: 'Seed context' });
    expect(within(context).getByText(status[0].toUpperCase() + status.slice(1))).toBeVisible();
    expect(within(context).getByText('This agent reports to this seed.')).toBeVisible();
  });

  it('does not invent a state for an unavailable reporting seed', () => {
    render(<PaneSeedChip {...props} display={{ kind: 'crown', seedId: 's-missing' }} />);
    expect(screen.getByText('Unknown')).toBeVisible();
  });

  it('loads a real note only when opened, filters artifact activity, and keeps the outcome', async () => {
    const value = seed({ id: 's-work11', title: 'Garden icons', status: 'harvested', reason: 'All five states are legible.' });
    const doc = documentFor(value);
    doc.notes.push({ ...doc.notes[0], id: 'n-2', kind: 'attach', body: 'attached screenshot', created_at: '2099-01-01T00:00:00Z' });
    const fetchDocument = vi.fn().mockResolvedValue(doc);
    render(
      <DaemonApiProvider api={{ sendSeedDocumentGet: fetchDocument } as unknown as DaemonApi}>
        <PaneSeedChip {...props} display={{ kind: 'crown', seedId: value.id, seed: value }} />
      </DaemonApiProvider>,
    );
    expect(fetchDocument).not.toHaveBeenCalled();
    fireEvent.keyDown(screen.getByTestId('seed-chip-sess-a'), { key: 'ArrowDown' });
    expect(await screen.findByText('Leaves look good at header size.')).toBeVisible();
    expect(screen.getByText('All five states are legible.')).toBeVisible();
    expect(screen.queryByText('attached screenshot')).not.toBeInTheDocument();
    expect(fetchDocument).toHaveBeenCalledExactlyOnceWith(value.id);
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('ignores a late note response after a lifecycle revision', async () => {
    const value = seed({ id: 's-work11', title: 'Garden icons' });
    let resolveOld!: (value: SeedDocument) => void;
    const fetchDocument = vi.fn().mockImplementationOnce(() => new Promise((resolve) => { resolveOld = resolve; }))
      .mockResolvedValueOnce(documentFor({ ...value, rev: 2, status: 'harvested' }, 'Finished and verified.'));
    const view = (current: Seed) => (
      <DaemonApiProvider api={{ sendSeedDocumentGet: fetchDocument } as unknown as DaemonApi}>
        <PaneSeedChip {...props} pinned display={{ kind: 'crown', seedId: current.id, seed: current }} />
      </DaemonApiProvider>
    );
    const { rerender } = render(view(value));
    rerender(view({ ...value, rev: 2, status: 'harvested' }));
    expect(await screen.findByText('Finished and verified.')).toBeVisible();
    await act(async () => resolveOld(documentFor(value, 'Still exploring.')));
    expect(screen.queryByText('Still exploring.')).not.toBeInTheDocument();
    expect(fetchDocument).toHaveBeenCalledTimes(2);
  });

  it('keeps opening the seed available after a context fetch fails', async () => {
    const value = seed({ id: 's-work11', title: 'Garden icons' });
    const onOpenSeed = vi.fn();
    render(
      <DaemonApiProvider api={{ sendSeedDocumentGet: vi.fn().mockRejectedValue(new Error('offline')) } as unknown as DaemonApi}>
        <PaneSeedChip {...props} onOpenSeed={onOpenSeed} pinned display={{ kind: 'seed', seed: value }} />
      </DaemonApiProvider>,
    );
    expect(await screen.findByText('Latest note unavailable.')).toBeVisible();
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Enter' });
    expect(onOpenSeed).toHaveBeenCalledWith(value.id);
  });

  it('refreshes a visible note when a Garden snapshot arrives at the same seed revision', async () => {
    const value = seed({ id: 's-work11', title: 'Garden icons' });
    const fetchDocument = vi.fn().mockResolvedValueOnce(documentFor(value, 'First note.'))
      .mockResolvedValueOnce(documentFor(value, 'A new observation.'));
    const api = { sendSeedDocumentGet: fetchDocument } as unknown as DaemonApi;
    const view = (current: Seed) => (
      <DaemonApiProvider api={api}>
        <PaneSeedChip {...props} pinned display={{ kind: 'seed', seed: current }} />
      </DaemonApiProvider>
    );
    const { rerender } = render(view(value));
    expect(await screen.findByText('First note.')).toBeVisible();
    rerender(view({ ...value }));
    expect(await screen.findByText('A new observation.')).toBeVisible();
    expect(fetchDocument).toHaveBeenCalledTimes(2);
  });
});
