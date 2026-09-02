import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { GardenPanel } from './GardenPanel';
import type { Seed } from '../hooks/useDaemonSocket';

function seed(overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return {
    body: '',
    status: 'planted',
    state_changed_at: '2026-09-02T09:00:00Z',
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
    created_at: '2026-09-02T09:00:00Z',
    updated_at: '2026-09-02T09:00:00Z',
    ...overrides,
  };
}

const armed = seed({
  id: 's-armed1',
  title: 'waiting on the merge',
  status: 'dormant',
  harvest_when: {
    pull_request: 'github.com:victorarias/attn#42',
    url: 'https://github.com/victorarias/attn/pull/42',
    set_at: '2026-09-02T10:00:00Z',
  },
});
const parked = seed({ id: 's-park11', title: 'put down for now', status: 'dormant' });

function renderPanel() {
  render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={2} seeds={[armed, parked]} />);
}

describe('GardenPanel harvest condition', () => {
  it('marks an armed row so it reads apart from an ordinary parked one', () => {
    renderPanel();

    const row = document.querySelector('[data-seed-row="s-armed1"]');
    expect(row).toHaveTextContent('harvests on #42');
    expect(row?.querySelector('.is-armed')).toHaveAttribute(
      'title',
      'harvests when victorarias/attn#42 merges',
    );
    expect(document.querySelector('[data-seed-row="s-park11"]')).not.toHaveTextContent('harvests on');
  });

  it('says the whole condition on the seed it opens', () => {
    renderPanel();

    fireEvent.click(screen.getByRole('button', { name: /waiting on the merge/ }));

    const link = screen.getByRole('link', { name: /harvests when victorarias\/attn#42 merges/ });
    expect(link).toHaveAttribute('href', 'https://github.com/victorarias/attn/pull/42');
  });
});
