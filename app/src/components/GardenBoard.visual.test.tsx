import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { GardenBoard } from './GardenBoard';
import type { Seed } from '../hooks/useDaemonSocket';
import { _resetEscapeStackForTest } from '../hooks/useEscapeStack';

function seed(overrides: Partial<Seed> & Pick<Seed, 'id' | 'title'>): Seed {
  return {
    body: '',
    status: 'planted',
    state_changed_at: '2026-08-27T09:00:00Z',
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
    created_at: '2026-08-27T09:00:00Z',
    updated_at: '2026-08-27T09:00:00Z',
    ...overrides,
  };
}

const world = [
  seed({ id: 's-ready1', title: 'pick this up', ready: true }),
  seed({ id: 's-wait11', title: 'waiting on work' }),
  seed({
    id: 's-plot11',
    title: 'a plot in motion',
    ready: true,
    plot_progress: { total: 3, done: 1, withered: 0, growing: 1, dormant: 0, ready: 1, blocked: 0 },
  }),
  seed({
    id: 's-work11',
    title: 'owned work',
    status: 'growing',
    tender_member: 'trellis',
  }),
  seed({ id: 's-park11', title: 'paused on purpose', status: 'dormant' }),
  seed({
    id: 's-armed1',
    title: 'waiting on the merge',
    status: 'dormant',
    harvest_when: {
      pull_request: 'github.com:victorarias/attn#42',
      url: 'https://github.com/victorarias/attn/pull/42',
      set_at: '2026-08-27T09:00:00Z',
    },
  }),
  seed({ id: 's-done11', title: 'finished work', status: 'harvested' }),
];

function renderBoard() {
  render(
    <GardenBoard
      seeds={world}
      seedsTotal={world.length}
      liveSessions={new Set()}
      loaded
      onTransition={vi.fn()}
      onNote={vi.fn()}
      onClose={vi.fn()}
      onEscapeFloor={vi.fn()}
    />,
  );
}

afterEach(() => _resetEscapeStackForTest());

describe('GardenBoard visual language', () => {
  it('names active work In progress while keeping the Garden state internal', () => {
    renderBoard();

    expect(screen.getByRole('heading', { name: 'In progress' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Growing' })).not.toBeInTheDocument();
    expect(screen.getByText('1 in progress · 1 ready · 1/3 done')).toBeInTheDocument();
  });

  it('marks ready and quiet work separately for the board palette', () => {
    renderBoard();

    expect(document.querySelector('[data-seed="s-ready1"]')).toHaveClass('is-ready');
    expect(document.querySelector('[data-seed="s-wait11"]')).toHaveClass('is-not-ready');
    expect(document.querySelector('[data-column="parked"] [data-seed="s-park11"]')).not.toBeNull();
    expect(screen.getAllByText('parked')).toHaveLength(2);
  });

  it('tells an armed card apart from a card somebody merely put down', () => {
    renderBoard();

    const card = document.querySelector('[data-seed="s-armed1"]');
    expect(card).toHaveTextContent('harvests on #42');
    expect(card?.querySelector('.garden-card__armed')).toHaveAttribute(
      'title',
      'harvests when victorarias/attn#42 merges',
    );
    expect(document.querySelector('[data-seed="s-park11"]')).not.toHaveTextContent('harvests on');
  });
});
