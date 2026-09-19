import { useMemo } from 'react';
import type { Seed } from '../hooks/useDaemonSocket';
import type { CrewMember } from '../types/generated';
import { crewDisplayName } from '../utils/crewName';
import { seedsPlantedByMember, seedsTendedByMember, type CrewSeedFilter } from './crewSeedOwnership';
import { SeedPlotIcon, SeedStateIcon } from './SeedStateIcon';
import { seedStateLabel } from './seedStatePresentation';

function plotFor(seed: Seed, byId: Map<string, Seed>): Seed | undefined {
  const parent = seed.edges.find((edge) => edge.kind === 'part-of');
  return parent ? byId.get(parent.to) : undefined;
}

function progress(seed: Seed | undefined): string {
  const value = seed?.plot_progress;
  return value?.total ? `${value.done}/${value.total}` : '';
}

const createdDateFormat = new Intl.DateTimeFormat(undefined, { year: 'numeric', month: 'short', day: 'numeric' });

function createdDate(seed: Seed): string {
  const parsed = new Date(seed.created_at);
  if (Number.isNaN(parsed.getTime())) return seed.created_at;
  return createdDateFormat.format(parsed);
}

function emptySeedsCopy(memberName: string, filter: CrewSeedFilter, capped: boolean): string {
  if (filter === 'tending') return `${memberName} isn't tending a seed${capped ? ' in this Garden snapshot' : ''}.`;
  return `No seeds${capped ? ' in this Garden snapshot are' : ' were'} explicitly planted by ${memberName}.`;
}

function CrewSeedRow({ seed, plot, onOpenSeed }: { seed: Seed; plot?: Seed; onOpenSeed: (seedId: string) => void }) {
  const ownProgress = progress(seed);
  const plotProgress = progress(plot);
  return (
    <li>
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
        <span className="crew-seed-row-state">{ownProgress || seedStateLabel(seed.status)}</span>
      </button>
    </li>
  );
}

function CrewSeedFilters({ filter, tending, planted, capped, onFilterChange }: {
  filter: CrewSeedFilter;
  tending: number;
  planted: number;
  capped: boolean;
  onFilterChange: (filter: CrewSeedFilter) => void;
}) {
  const suffix = capped ? '+' : '';
  return (
    <div className="crew-seed-filters" role="group" aria-label="Seed ownership">
      <button
        type="button"
        data-testid="crew-seed-filter-tending"
        className={filter === 'tending' ? 'is-selected' : ''}
        aria-pressed={filter === 'tending'}
        onClick={() => onFilterChange('tending')}
      >
        Tending <span>{tending}{suffix}</span>
      </button>
      <button
        type="button"
        data-testid="crew-seed-filter-planted"
        className={filter === 'planted' ? 'is-selected' : ''}
        aria-pressed={filter === 'planted'}
        onClick={() => onFilterChange('planted')}
      >
        Planted <span>{planted}{suffix}</span>
      </button>
    </div>
  );
}

function CrewSeedList({ rows, filter, query, byId, onQueryChange, onOpenSeed }: {
  rows: Seed[];
  filter: CrewSeedFilter;
  query: string;
  byId: Map<string, Seed>;
  onQueryChange: (query: string) => void;
  onOpenSeed: (seedId: string) => void;
}) {
  const visibleRows = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase();
    if (!needle) return rows;
    return rows.filter((seed) => `${seed.title} ${seed.id}`.toLocaleLowerCase().includes(needle));
  }, [query, rows]);
  return (
    <>
      <input
        className="crew-seed-search"
        data-testid="crew-seed-search"
        value={query}
        onChange={(event) => onQueryChange(event.target.value)}
        placeholder="Find a seed…"
        aria-label="Find a seed"
      />
      {visibleRows.length > 0 ? (
        <ul className="crew-seed-list" aria-label={`${filter === 'tending' ? 'Tended' : 'Planted'} seeds`}>
          {visibleRows.map((seed) => <CrewSeedRow key={seed.id} seed={seed} plot={plotFor(seed, byId)} onOpenSeed={onOpenSeed} />)}
        </ul>
      ) : (
        <p className="crew-seed-empty">No matching seeds.</p>
      )}
    </>
  );
}

export interface CrewSeedsProps {
  member: CrewMember;
  seeds: Seed[];
  seedsTotal: number;
  filter: CrewSeedFilter;
  onFilterChange: (filter: CrewSeedFilter) => void;
  query: string;
  onQueryChange: (query: string) => void;
  onOpenSeed: (seedId: string) => void;
}

export function CrewSeeds({
  member,
  seeds,
  seedsTotal,
  filter,
  onFilterChange,
  query,
  onQueryChange,
  onOpenSeed,
}: CrewSeedsProps) {
  const tended = useMemo(() => seedsTendedByMember(seeds, member), [member, seeds]);
  const planted = useMemo(() => seedsPlantedByMember(seeds, member.id), [member.id, seeds]);
  const rows = filter === 'tending' ? tended : planted;
  const byId = useMemo(() => new Map(seeds.map((seed) => [seed.id, seed])), [seeds]);
  const capped = seedsTotal > seeds.length;

  return (
    <section className="crew-seeds" aria-labelledby="crew-seeds-heading">
      <div className="crew-section-heading">
        <div>
          <span className="crew-kicker">Garden</span>
          <h3 id="crew-seeds-heading">Seeds</h3>
        </div>
      </div>
      <CrewSeedFilters filter={filter} tending={tended.length} planted={planted.length} capped={capped} onFilterChange={onFilterChange} />
      {capped && (
        <p className="crew-seed-cap" role="status">
          Showing matches in the newest {seeds.length} of {seedsTotal} seeds.
        </p>
      )}
      {rows.length > 0 ? (
        <CrewSeedList rows={rows} filter={filter} query={query} byId={byId} onQueryChange={onQueryChange} onOpenSeed={onOpenSeed} />
      ) : (
        <div className="crew-seed-empty">
          <p>{emptySeedsCopy(crewDisplayName(member.id), filter, capped)}</p>
          {filter === 'tending' && planted.length > 0 && (
            <button type="button" onClick={() => onFilterChange('planted')}>See planted seeds</button>
          )}
        </div>
      )}
    </section>
  );
}
