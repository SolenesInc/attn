import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { GardenPanel } from './GardenPanel';
import type { Seed } from '../hooks/useDaemonSocket';

function seed(overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return {
    body: '',
    status: 'planted',
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
const fourth = plot('s-fou111', 'the overflow menu', 's-inn111');
const fifth = plot('s-fif111', 'the fold threshold', 's-fou111');
const leaf = seed({ id: 's-leaf11', title: 'the actual work', edges: [{ kind: 'part-of', to: 's-fif111' }] });
const loose = seed({ id: 's-alone1', title: 'unrelated work' });
const world = [crown, middle, inner, fourth, fifth, leaf, loose];

// The panel measures its own box and happy-dom lays nothing out, so the width the rule reads has to be stated.
function atWidth(width: number) {
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(width);
}

function columns() {
  return Array.from(document.querySelectorAll('.garden-column:not(.garden-column--reader)'));
}
function trailSteps() {
  return Array.from(document.querySelectorAll('.garden-trail__step')).map((s) => s.textContent?.trim());
}
function open(title: RegExp | string) {
  fireEvent.click(screen.getByRole('button', { name: title }));
}

describe('GardenPanel columns', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('draws as many trailing levels as the width holds, and no more', () => {
    atWidth(1200);
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={7} seeds={world} />);

    expect(columns()).toHaveLength(1);

    open(/the migration/);
    expect(columns()).toHaveLength(2);

    open(/one panel/);
    open(/its header/);
    expect(columns()).toHaveLength(2);
  });

  it('shows a third level once there is room for it', () => {
    atWidth(1780);
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={7} seeds={world} />);

    open(/the migration/);
    open(/one panel/);
    expect(columns()).toHaveLength(3);
  });

  it('names only the ancestors no column is showing', () => {
    atWidth(1780);
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={7} seeds={world} />);

    open(/the migration/);
    open(/one panel/);
    expect(trailSteps()).toEqual(['Garden']);

    open(/its header/);
    expect(trailSteps()).toEqual(['Garden', 'the migration']);
  });

  it('folds the trail once it outruns three steps, and opens it again', () => {
    atWidth(1200);
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={7} seeds={world} />);

    open(/the migration/);
    open(/one panel/);
    open(/its header/);
    open(/the overflow menu/);
    open(/the fold threshold/);
    expect(trailSteps()).toEqual(['Garden', '…', 'its header', 'the overflow menu']);

    fireEvent.click(screen.getByRole('button', { name: /Show 2 more steps/ }));
    expect(trailSteps()).toEqual([
      'Garden', 'the migration', 'one panel', 'its header', 'the overflow menu',
    ]);
  });

  it('switches siblings when a row in an earlier column is clicked', () => {
    atWidth(1780);
    const sibling = plot('s-mid222', 'another panel', 's-crown1');
    render(
      <GardenPanel isOpen onClose={vi.fn()} seedsTotal={8} seeds={[...world, sibling]} />,
    );

    open(/the migration/);
    open(/one panel/);
    expect(screen.getByRole('heading', { name: 'one panel' })).toBeInTheDocument();

    const crownChildren = columns()[1];
    fireEvent.click(within(crownChildren as HTMLElement).getByRole('button', { name: /another panel/ }));

    expect(screen.getByRole('heading', { name: 'another panel' })).toBeInTheDocument();
    expect(trailSteps()).toEqual(['Garden']);
  });

  it('hands Escape to the frame without climbing the walk', () => {
    atWidth(1200);
    const onEscapeFloor = vi.fn();
    render(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        onEscapeFloor={onEscapeFloor}
        seedsTotal={7}
        seeds={world}
      />,
    );

    open(/the migration/);
    open(/one panel/);
    expect(screen.getByRole('heading', { name: 'one panel' })).toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.getByRole('heading', { name: 'one panel' })).toBeInTheDocument();
    expect(onEscapeFloor).toHaveBeenCalledTimes(1);
  });
});
