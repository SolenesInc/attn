import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Seed } from '../hooks/useDaemonSocket';
import type { CrewMember } from '../types/generated';
import { CrewSeeds, seedsPlantedByMember, seedsTendedByMember } from './CrewSeeds';

function member(id: string, bindingSession = ''): CrewMember {
  return {
    id,
    revision: 1,
    charter_path: `/crew/${id}/CHARTER.md`,
    home_dir: `/crew/${id}`,
    awareness_dirs: [],
    resolved_agent: 'claude',
    ...(bindingSession ? { binding_session: bindingSession } : {}),
  };
}

function seed(overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return {
    body: '',
    status: 'planted',
    state_changed_at: '2026-09-06T16:50:50Z',
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
    created_at: '2026-09-06T16:50:50Z',
    updated_at: '2026-09-06T16:50:50Z',
    ...overrides,
  };
}

const alderSnapshot = [
  seed({ id: 's-rqnbmx', title: 'Alder maps the pi extension home and how codex does compaction and search', status: 'harvested', planter_member: 'alder', planter_session: 'day-old' }),
  seed({ id: 's-gsj4mh', title: 'remove-nisse', status: 'harvested', planter_member: 'alder', planter_session: 'day-old' }),
  seed({ id: 's-ce8999', title: 'automode-edit', status: 'harvested', planter_member: 'alder', planter_session: 'day-older' }),
  seed({ id: 's-a4', title: 'A fourth explicitly attributed seed', status: 'dormant', planter_member: 'alder', planter_session: 'day-older' }),
];

describe('Crew seed attribution', () => {
  it('keeps an asleep member claim, includes its current-day session claim, and lists one seed when both identities occur', () => {
    const memberOnly = seed({ id: 's-member', title: 'Member claim', status: 'growing', tender_member: 'alder' });
    const currentDay = seed({ id: 's-day', title: 'Day claim', status: 'growing', tender_session: 'day-current' });
    const duplicateIdentity = seed({
      id: 's-both',
      title: 'Both identities',
      status: 'growing',
      tender_member: 'alder',
      tender_session: 'day-current',
    });
    const oldDay = seed({ id: 's-old', title: 'Old day', status: 'growing', tender_session: 'day-old' });

    expect(seedsTendedByMember([memberOnly], member('alder'))).toEqual([memberOnly]);
    expect(seedsTendedByMember(
      [memberOnly, currentDay, duplicateIdentity, oldDay],
      member('alder', 'day-current'),
    ).map((entry) => entry.id)).toEqual(['s-member', 's-day', 's-both']);
  });

  it('uses explicit planter_member and never a current or historical session guess', () => {
    const explicit = alderSnapshot[0];
    const currentSessionOnly = seed({ id: 's-current', title: 'Current day planted it', planter_session: 'day-current' });
    const historicalSessionOnly = seed({ id: 's-history', title: 'An old day planted it', planter_session: 'day-old' });

    expect(seedsPlantedByMember([explicit, currentSessionOnly, historicalSessionOnly], 'alder')).toEqual([explicit]);
  });
});

describe('CrewSeeds', () => {
  it('shows copied snapshot attribution, state icons, plot context and progress, then opens the native seed action', () => {
    const onOpenSeed = vi.fn();
    const plot = seed({
      id: 's-dssvxq',
      title: 'Garden follow-up work',
      status: 'growing',
      plot_progress: { total: 5, done: 2, withered: 0, growing: 1, dormant: 1, ready: 1, blocked: 0 },
    });
    const child = seed({
      id: 's-g9yxwv',
      title: 'Artifact presence comes from the daemon',
      planter_member: 'trellis',
      edges: [{ kind: 'part-of', to: plot.id }],
    });
    render(
      <CrewSeeds
        member={member('trellis')}
        seeds={[child, plot]}
        seedsTotal={2}
        filter="planted"
        onFilterChange={vi.fn()}
        onOpenSeed={onOpenSeed}
      />,
    );

    const row = screen.getByRole('button', { name: /Artifact presence comes from the daemon/ });
    expect(row).toHaveAttribute('data-seed-id', child.id);
    expect(row).toHaveAttribute('data-seed-state', 'planted');
    expect(row).toHaveTextContent('Garden follow-up work · 2/5');
    fireEvent.click(row);
    expect(onOpenSeed).toHaveBeenCalledWith(child.id);
  });

  it('tracks explicit planting and claim release from replacement Garden broadcasts', () => {
    const alder = member('alder', 'day-current');
    const claimed = seed({ id: 's-live', title: 'Live claim', status: 'growing', tender_session: 'day-current' });
    const view = render(
      <CrewSeeds member={alder} seeds={[claimed]} seedsTotal={1} filter="tending" onFilterChange={vi.fn()} onOpenSeed={vi.fn()} />,
    );
    expect(screen.getByRole('button', { name: /Live claim/ })).toBeInTheDocument();

    view.rerender(
      <CrewSeeds member={alder} seeds={[{ ...claimed, status: 'dormant', tender_session: '' }]} seedsTotal={1} filter="tending" onFilterChange={vi.fn()} onOpenSeed={vi.fn()} />,
    );
    expect(screen.getByText("Alder isn't tending a seed.")).toBeInTheDocument();

    view.rerender(
      <CrewSeeds member={alder} seeds={[{ ...claimed, status: 'planted', tender_session: '', planter_member: 'alder' }]} seedsTotal={1} filter="planted" onFilterChange={vi.fn()} onOpenSeed={vi.fn()} />,
    );
    expect(screen.getByRole('button', { name: /Live claim/ })).toBeInTheDocument();
  });

  it('keeps honest empty and capped states', () => {
    const { rerender } = render(
      <CrewSeeds member={member('keel')} seeds={[]} seedsTotal={0} filter="planted" onFilterChange={vi.fn()} onOpenSeed={vi.fn()} />,
    );
    expect(screen.getByText('No seeds were explicitly planted by Keel.')).toBeInTheDocument();

    rerender(
      <CrewSeeds member={member('keel')} seeds={alderSnapshot} seedsTotal={481} filter="planted" onFilterChange={vi.fn()} onOpenSeed={vi.fn()} />,
    );
    expect(screen.getByText('Showing matches in the newest 4 of 481 seeds.')).toBeInTheDocument();
    expect(screen.getByText('No seeds in this Garden snapshot are explicitly planted by Keel.')).toBeInTheDocument();
    expect(within(screen.getByRole('button', { name: /Planted/ })).getByText('0+')).toBeInTheDocument();
  });
});
