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

describe('GardenPanel lifecycle', () => {
  it('shows a seed s state and its tender in the row', () => {
    const growing = seed({
      id: 's-grow11',
      title: 'being worked on',
      status: 'growing',
      tender_member: 'trellis',
      tender_session: 'sess-a',
    });
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={1} seeds={[growing]} />);

    expect(screen.getByText('growing')).toBeInTheDocument();
    expect(screen.getByText(/tended by Trellis/)).toBeInTheDocument();
  });

  it('falls back to the claiming session when there is no member', () => {
    const growing = seed({
      id: 's-grow22',
      title: 'claimed by a session',
      status: 'growing',
      tender_session: 'sess-b',
    });
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={1} seeds={[growing]} />);

    expect(screen.getByText(/tended by sess-b/)).toBeInTheDocument();
  });

  it('says nothing about a tender when nobody holds the seed', () => {
    render(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seedsTotal={1}
        seeds={[seed({ id: 's-idle11', title: 'unclaimed' })]}
      />,
    );

    expect(screen.queryByText(/tended by/)).not.toBeInTheDocument();
  });

  it('follows a seed through its life as the pushes arrive', () => {
    const planted = seed({ id: 's-life11', title: 'a whole life' });
    const { rerender } = render(
      <GardenPanel isOpen onClose={vi.fn()} seedsTotal={1} seeds={[planted]} />,
    );
    expect(screen.getByRole('button', { name: /a whole life/ })).toBeInTheDocument();
    expect(screen.queryByText('planted')).not.toBeInTheDocument();

    rerender(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seedsTotal={1}
        seeds={[{ ...planted, status: 'growing', tender_member: 'trellis', rev: 2 }]}
      />,
    );
    expect(screen.getByText('growing')).toBeInTheDocument();
    expect(screen.getByText(/tended by Trellis/)).toBeInTheDocument();

    rerender(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seedsTotal={1}
        seeds={[{ ...planted, status: 'harvested', reason: 'shipped it', rev: 3 }]}
      />,
    );
    expect(screen.queryByRole('button', { name: /a whole life/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /1 closed/ }));
    expect(screen.getByText('done')).toBeInTheDocument();
    expect(screen.queryByText(/tended by/)).not.toBeInTheDocument();
  });

  it('shows why a seed closed once it is opened', () => {
    const harvested = seed({
      id: 's-done11',
      title: 'finished',
      status: 'harvested',
      reason: 'shipped it',
    });
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={1} seeds={[harvested]} />);

    fireEvent.click(screen.getByRole('button', { name: /1 closed/ }));
    expect(screen.queryByText('shipped it')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /finished/ }));
    expect(screen.getByText('shipped it')).toBeInTheDocument();
  });

  it('says what is hidden when everything in view is closed', () => {
    const done = seed({ id: 's-done22', title: 'all wrapped', status: 'harvested' });
    const dead = seed({ id: 's-dead11', title: 'went nowhere', status: 'withered' });
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={2} seeds={[done, dead]} />);

    expect(screen.getByText(/Nothing open here\. 2 closed seeds are/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /2 closed/ }));
    expect(screen.getByRole('button', { name: /all wrapped/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /went nowhere/ })).toBeInTheDocument();
    expect(screen.queryByText(/Nothing open here/)).not.toBeInTheDocument();
  });
});
