import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { GardenPanel } from './GardenPanel';
import type { Seed } from '../hooks/useDaemonSocket';

function seed(overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return {
    body: '',
    status: 'planted',
    state_changed_at: '2026-08-20T09:00:00Z',
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
    created_at: '2026-08-20T09:00:00Z',
    updated_at: '2026-08-20T09:00:00Z',
    ...overrides,
  };
}
function plot(id: string, title: string, parent?: string): Seed {
  return seed({
    id,
    title,
    edges: parent ? [{ kind: 'part-of', to: parent }] : [],
    plot_progress: { total: 1, done: 0, withered: 0, growing: 0, dormant: 0, ready: 1, blocked: 0 },
  });
}

const crown = plot('s-crown1', 'the migration');
const middle = plot('s-mid111', 'one panel', 's-crown1');
const inner = plot('s-inn111', 'its header', 's-mid111');
const leaf = seed({ id: 's-leaf11', title: 'the actual work', edges: [{ kind: 'part-of', to: 's-inn111' }] });
const done = seed({
  id: 's-done11',
  title: 'already shipped',
  status: 'harvested',
  edges: [{ kind: 'part-of', to: 's-crown1' }],
});
const world = [crown, middle, inner, leaf, done];

function open(name: RegExp | string) {
  fireEvent.click(screen.getByRole('button', { name }));
}
function columns() {
  return Array.from(document.querySelectorAll('.garden-column:not(.garden-column--reader)'));
}

describe('GardenPanel keeps the reader s place', () => {
  it('keeps the closed lens across a walk that leaves the column behind', () => {
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(1200);
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={5} seeds={world} />);

    open(/the migration/);
    fireEvent.click(screen.getByRole('button', { name: /1 closed/ }));
    const crownChildren = columns()[1] as HTMLElement;
    expect(within(crownChildren).getByRole('button', { name: /already shipped/ })).toBeInTheDocument();

    open(/one panel/);
    open(/its header/);
    expect(document.querySelector('.garden-column[data-column="s-crown1"]')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'the migration' }));

    const returned = document.querySelector('.garden-column[data-column="s-crown1"]') as HTMLElement;
    expect(returned).not.toBeNull();
    expect(within(returned).getByRole('button', { name: /already shipped/ })).toBeInTheDocument();
  });

  it('is the same walk in both renderers', () => {
    const { unmount } = render(
      <GardenPanel isOpen onClose={vi.fn()} seedsTotal={5} seeds={world} />,
    );
    open(/the migration/);
    open(/one panel/);
    expect(screen.getByRole('heading', { name: 'one panel' })).toBeInTheDocument();
    unmount();

    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(1200);
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={5} seeds={world} />);
    expect(screen.getByRole('heading', { name: 'one panel' })).toBeInTheDocument();
  });
});
