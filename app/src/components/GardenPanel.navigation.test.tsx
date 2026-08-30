import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { GardenPanel } from './GardenPanel';
import type { Seed } from '../hooks/useDaemonSocket';

function seed(overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return {
    body: '',
    status: 'planted',
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

function crown(id: string, title: string, progress: Partial<Seed['plot_progress']> = {}): Seed {
  return seed({
    id,
    title,
    plot_progress: { total: 0, done: 0, withered: 0, growing: 0, dormant: 0, ready: 0, blocked: 0, ...progress },
  });
}

function childOf(crownID: string, overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return seed({ ...overrides, edges: [{ kind: 'part-of', to: crownID }, ...(overrides.edges ?? [])] });
}

describe('GardenPanel navigation', () => {
  const shipIt = crown('s-crown1', 'ship the thing', { total: 3, done: 1, growing: 1, ready: 1 });
  const first = childOf('s-crown1', { id: 's-child1', title: 'first step', status: 'harvested' });
  const second = childOf('s-crown1', { id: 's-child2', title: 'second step', ready: true });
  const elsewhere = seed({ id: 's-alone1', title: 'unrelated work' });
  const all = [shipIt, first, second, elsewhere];

  it('opens on crowns and loose seeds, keeping plot children inside their plot', () => {
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={4} seeds={all} />);

    expect(screen.getByText('ship the thing')).toBeInTheDocument();
    expect(screen.getByText('unrelated work')).toBeInTheDocument();
    expect(screen.queryByText('first step')).not.toBeInTheDocument();
    expect(screen.queryByText('second step')).not.toBeInTheDocument();
  });

  it('lists a child at root when its crown is not in the push', () => {
    const orphan = childOf('s-gone99', { id: 's-orph11', title: 'crown got capped away' });
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={9} seeds={[orphan, elsewhere]} />);

    expect(screen.getByText('crown got capped away')).toBeInTheDocument();
  });

  it('counts a crown s plot on its row and spells it out on its page', () => {
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={4} seeds={all} />);

    expect(screen.getByRole('button', { name: /ship the thing/ })).toHaveTextContent('1/3');
    expect(screen.getByRole('button', { name: /unrelated work/ })).not.toHaveTextContent('/');
    expect(screen.queryByText(/1\/3 done/)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /ship the thing/ }));
    expect(screen.getByText('1/3 done · 1 growing · 1 ready')).toBeInTheDocument();
  });

  it('drills into a plot and climbs back out', () => {
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={4} seeds={all} />);

    fireEvent.click(screen.getByRole('button', { name: /ship the thing/ }));

    expect(screen.getByRole('button', { name: /second step/ })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /unrelated work/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /first step/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /1 closed/ }));
    expect(screen.getByRole('button', { name: /first step/ })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Garden' }));

    expect(screen.getByRole('button', { name: /unrelated work/ })).toBeInTheDocument();
  });

  it('walks a plot inside a plot and climbs several levels at once', () => {
    const outer = crown('s-outer1', 'the epic', { total: 1 });
    const middle = childOf('s-outer1', {
      id: 's-mid111',
      title: 'a slice',
      plot_progress: { total: 1, done: 0, withered: 0, growing: 0, dormant: 0, ready: 1, blocked: 0 },
    });
    const leaf = childOf('s-mid111', { id: 's-leaf11', title: 'the actual work' });
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={3} seeds={[outer, middle, leaf]} />);

    fireEvent.click(screen.getByRole('button', { name: /the epic/ }));
    fireEvent.click(screen.getByRole('button', { name: /a slice/ }));

    expect(screen.getByRole('button', { name: /the actual work/ })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^a slice/ })).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'a slice' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Garden' }));

    expect(screen.getByRole('button', { name: /the epic/ })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Garden' })).not.toBeInTheDocument();
  });

  it('climbs out on its own when the crown it is inside disappears', () => {
    const { rerender } = render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={4} seeds={all} />);

    fireEvent.click(screen.getByRole('button', { name: /ship the thing/ }));
    expect(screen.getByRole('button', { name: /second step/ })).toBeInTheDocument();

    rerender(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={1} seeds={[elsewhere]} />);

    expect(screen.getByRole('button', { name: /unrelated work/ })).toBeInTheDocument();
  });

  it('crosses from one plot into a related one', () => {
    const other = crown('s-crown2', 'the next plot', { total: 1, ready: 1 });
    const linked = childOf('s-crown1', {
      id: 's-child3',
      title: 'holds the next plot up',
      edges: [{ kind: 'blocks', to: 's-crown2' }],
    });
    render(
      <GardenPanel isOpen onClose={vi.fn()} seedsTotal={4} seeds={[shipIt, linked, other, elsewhere]} />,
    );

    fireEvent.click(screen.getByRole('button', { name: /ship the thing/ }));
    fireEvent.click(screen.getByRole('button', { name: /holds the next plot up/ }));
    fireEvent.click(screen.getByRole('button', { name: 'the next plot' }));

    expect(screen.getByRole('heading', { name: 'the next plot' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Garden' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'ship the thing' })).toBeInTheDocument();
  });

  it('says what it is not showing when the garden outgrew one push', () => {
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={1421} seeds={[elsewhere]} />);

    expect(screen.getByText(/holds 1421 seeds/)).toBeInTheDocument();
    expect(screen.getByText(/newest 1/)).toBeInTheDocument();
  });

  it('says nothing about a cap when the whole garden fit', () => {
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={4} seeds={all} />);

    expect(screen.queryByText(/holds 4 seeds/)).not.toBeInTheDocument();
  });

  it('names the way in when the garden is empty, and the way into an empty plot', () => {
    const { rerender } = render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={0} seeds={[]} />);

    expect(screen.getByText(/The garden is empty/)).toBeInTheDocument();
    expect(screen.getByText('attn seed plant "what this is"')).toBeInTheDocument();

    const bare = crown('s-crown9', 'nothing in it yet');
    rerender(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={1} seeds={[bare]} />);
    fireEvent.click(screen.getByRole('button', { name: /nothing in it yet/ }));

    expect(screen.getByText(/Nothing planted in this plot yet/)).toBeInTheDocument();
    expect(screen.getByText('attn seed plant "what this is" --part-of s-crown9')).toBeInTheDocument();
  });

  it('opens a seed to its own page, and the trail is the way back', () => {
    const withBody = seed({ id: 's-body11', title: 'has a body', body: 'the plan itself' });
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={2} seeds={[withBody, elsewhere]} />);

    expect(screen.queryByText('the plan itself')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /has a body/ }));

    expect(screen.getByRole('heading', { name: 'has a body' })).toBeInTheDocument();
    expect(screen.getByText('the plan itself')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /unrelated work/ })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Garden' }));
    expect(screen.getByRole('button', { name: /unrelated work/ })).toBeInTheDocument();
  });
});
