import { useMemo, useState } from 'react';
import type { Seed } from '../hooks/useDaemonSocket';
import type { CrewMember } from '../types/generated';
import { crewDisplayName } from '../utils/crewName';
import { tendedSeeds } from './paneSeedDisplay';
import { SeedPlotIcon, SeedStateIcon } from './SeedStateIcon';
import { seedStateLabel } from './seedStatePresentation';

export type CrewSeedFilter = 'tending' | 'planted';

export function seedsTendedByMember(seeds: Seed[], member: CrewMember): Seed[] {
  return tendedSeeds(seeds, member.binding_session ?? '', member.id);
}

export function seedsPlantedByMember(seeds: Seed[], memberId: string): Seed[] {
  // planter_session has no durable crew attribution on the roster. Using the
  // current binding would rewrite history, so this list uses explicit identity.
  return seeds.filter((seed) => seed.planter_member === memberId);
}

function plotFor(seed: Seed, byId: Map<string, Seed>): Seed | undefined {
  const parent = seed.edges.find((edge) => edge.kind === 'part-of');
  return parent ? byId.get(parent.to) : undefined;
}

function progress(seed: Seed | undefined): string {
  const value = seed?.plot_progress;
  return value?.total ? `${value.done}/${value.total}` : '';
}

function createdDate(seed: Seed): string {
  const parsed = new Date(seed.created_at);
  if (Number.isNaN(parsed.getTime())) return seed.created_at;
  return parsed.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

export function CrewSeeds({
  member,
  seeds,
  seedsTotal,
  filter,
  onFilterChange,
  onOpenSeed,
}: {
  member: CrewMember;
  seeds: Seed[];
  seedsTotal: number;
  filter: CrewSeedFilter;
  onFilterChange: (filter: CrewSeedFilter) => void;
  onOpenSeed: (seedId: string) => void;
}) {
  const [query, setQuery] = useState('');
  const tended = useMemo(() => seedsTendedByMember(seeds, member), [member, seeds]);
  const planted = useMemo(() => seedsPlantedByMember(seeds, member.id), [member.id, seeds]);
  const rows = filter === 'tending' ? tended : planted;
  const byId = useMemo(() => new Map(seeds.map((seed) => [seed.id, seed])), [seeds]);
  const visibleRows = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase();
    if (!needle) return rows;
    return rows.filter((seed) => `${seed.title} ${seed.id}`.toLocaleLowerCase().includes(needle));
  }, [query, rows]);
  const capped = seedsTotal > seeds.length;
  const memberName = crewDisplayName(member.id);
  const empty = filter === 'tending'
    ? `${memberName} isn't tending a seed${capped ? ' in this Garden snapshot' : ''}.`
    : `No seeds${capped ? ' in this Garden snapshot are' : ' were'} explicitly planted by ${memberName}.`;

  return (
    <section className="crew-seeds" aria-labelledby="crew-seeds-heading">
      <div className="crew-section-heading">
        <div>
          <span className="crew-kicker">Garden</span>
          <h3 id="crew-seeds-heading">Seeds</h3>
        </div>
      </div>
      <div className="crew-seed-filters" role="group" aria-label="Seed ownership">
        <button
          type="button"
          data-testid="crew-seed-filter-tending"
          className={filter === 'tending' ? 'is-selected' : ''}
          aria-pressed={filter === 'tending'}
          onClick={() => onFilterChange('tending')}
        >
          Tending <span>{tended.length}{capped ? '+' : ''}</span>
        </button>
        <button
          type="button"
          data-testid="crew-seed-filter-planted"
          className={filter === 'planted' ? 'is-selected' : ''}
          aria-pressed={filter === 'planted'}
          onClick={() => onFilterChange('planted')}
        >
          Planted <span>{planted.length}{capped ? '+' : ''}</span>
        </button>
      </div>

      {capped && (
        <p className="crew-seed-cap" role="status">
          Showing matches in the newest {seeds.length} of {seedsTotal} seeds.
        </p>
      )}

      {rows.length > 0 ? (
        <>
          <input
            className="crew-seed-search"
            data-testid="crew-seed-search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Find a seed…"
            aria-label="Find a seed"
          />
          {visibleRows.length > 0 ? (
            <ul className="crew-seed-list" aria-label={`${filter === 'tending' ? 'Tended' : 'Planted'} seeds`}>
              {visibleRows.map((seed) => {
                const plot = plotFor(seed, byId);
                const ownProgress = progress(seed);
                const plotProgress = progress(plot);
                return (
                  <li key={seed.id}>
                    <button
                      type="button"
                      className="crew-seed-row"
                      data-testid={`crew-seed-${seed.id}`}
                      data-seed-id={seed.id}
                      data-seed-state={seed.status}
                      onClick={() => onOpenSeed(seed.id)}
                      onKeyDown={(event) => {
                        if (event.key !== 'Enter') return;
                        event.preventDefault();
                        onOpenSeed(seed.id);
                      }}
                    >
                      {seed.plot_progress ? <SeedPlotIcon /> : <SeedStateIcon status={seed.status} />}
                      <span className="crew-seed-row-content">
                        <span className="crew-seed-row-title">{seed.title.trim() || seed.id}</span>
                        <span className="crew-seed-row-meta">
                          <span>{seed.id}</span>
                          <span>{createdDate(seed)}</span>
                          {plot && <span>{plot.title.trim() || plot.id}{plotProgress ? ` · ${plotProgress}` : ''}</span>}
                        </span>
                      </span>
                      <span className="crew-seed-row-state">
                        {ownProgress || seedStateLabel(seed.status)}
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          ) : (
            <p className="crew-seed-empty">No matching seeds.</p>
          )}
        </>
      ) : (
        <div className="crew-seed-empty">
          <p>{empty}</p>
          {filter === 'tending' && planted.length > 0 && (
            <button type="button" onClick={() => onFilterChange('planted')}>See planted seeds</button>
          )}
        </div>
      )}
    </section>
  );
}
