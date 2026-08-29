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

// Edges are stored on the seed they point FROM, so "who blocks me" only exists by
// reading the whole garden.
describe('GardenPanel edges', () => {
  const blocker = seed({ id: 's-aaa111', title: 'the blocker', ready: true });
  const blocked = seed({
    id: 's-bbb111',
    title: 'the blocked one',
    edges: [{ kind: 'part-of', to: 's-ccc111' }],
  });
  const crown = seed({
    id: 's-ccc111',
    title: 'the crown',
    plot_progress: { total: 1, done: 0, withered: 0, growing: 0, dormant: 0, ready: 0, blocked: 1 },
  });

  function chain(blockerStatus: string): Seed[] {
    return [
      { ...blocker, status: blockerStatus, ready: blockerStatus === 'planted', edges: [{ kind: 'blocks', to: 's-bbb111' }] },
      blocked,
      crown,
    ];
  }

  it('says how many block a seed, and says nothing about one that is free', () => {
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={3} seeds={chain('planted')} />);

    expect(screen.getByRole('button', { name: /the blocker/ })).toBeInTheDocument();
    expect(screen.queryByText('ready')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /the crown/ }));
    expect(screen.getByText('blocked by 1')).toBeInTheDocument();
  });

  it('stops counting a harvested blocker', () => {
    const { rerender } = render(
      <GardenPanel isOpen onClose={vi.fn()} seedsTotal={3} seeds={chain('planted')} />,
    );
    fireEvent.click(screen.getByRole('button', { name: /the crown/ }));
    expect(screen.getByText('blocked by 1')).toBeInTheDocument();

    rerender(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={3} seeds={chain('harvested')} />);

    expect(screen.queryByText('blocked by 1')).not.toBeInTheDocument();
  });

  it('lists a seed’s edges in both directions when opened', () => {
    const { container } = render(
      <GardenPanel isOpen onClose={vi.fn()} seedsTotal={3} seeds={chain('planted')} />,
    );

    fireEvent.click(screen.getByRole('button', { name: /the crown/ }));
    fireEvent.click(screen.getByRole('button', { name: /the blocked one/ }));

    const relations = container.querySelector('.garden-relations');
    expect(relations?.textContent).toContain('part of');
    expect(relations?.textContent).toContain('the crown');
    expect(relations?.textContent).toContain('blocked by');
    expect(relations?.textContent).toContain('the blocker');
  });

  it('renders a discovered-from edge from the work and its origin', () => {
    const origin = seed({ id: 's-origin1', title: 'the origin' });
    const found = seed({
      id: 's-found11',
      title: 'the discovered work',
      edges: [{ kind: 'discovered-from', to: origin.id }],
    });
    const { container } = render(
      <GardenPanel isOpen onClose={vi.fn()} seedsTotal={2} seeds={[found, origin]} />,
    );

    fireEvent.click(screen.getByRole('button', { name: /the discovered work/ }));
    expect(container.querySelector('.garden-relations')?.textContent).toContain('discovered from');
    expect(container.querySelector('.garden-relations')?.textContent).toContain('the origin');

    fireEvent.click(screen.getByRole('button', { name: /the origin/ }));
    expect(container.querySelector('.garden-relations')?.textContent).toContain('discovered');
    expect(container.querySelector('.garden-relations')?.textContent).toContain('the discovered work');
  });

  it('counts a blocker from outside the plot it is standing in', () => {
    const away = seed({
      id: 's-ddd111',
      title: 'blocker elsewhere',
      edges: [{ kind: 'blocks', to: 's-bbb111' }],
    });
    const withPlot = {
      ...crown,
      plot_progress: { total: 1, done: 0, withered: 0, growing: 0, dormant: 0, ready: 0, blocked: 1 },
    };
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={3} seeds={[blocked, away, withPlot]} />);

    fireEvent.click(screen.getByRole('button', { name: /the crown/ }));

    expect(screen.queryByText('blocker elsewhere')).not.toBeInTheDocument();
    expect(screen.getByText('blocked by 1')).toBeInTheDocument();
  });
});
