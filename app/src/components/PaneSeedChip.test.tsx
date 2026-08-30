import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { PaneSeedChip } from './PaneSeedChip';
import type { Seed } from '../hooks/useDaemonSocket';

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
    expect(screen.getByText('s-work11')).toBeInTheDocument();
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
