// See docs/plans/2026-08-20-garden-search.md.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { gardenScrollMemory, useGardenWalk } from '../store/gardenWalk';
import type { Seed } from '../hooks/useDaemonSocket';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { crewDisplayName, crewHolderName } from '../utils/crewName';
import {
  IS_VALUES,
  buildIndex,
  parseQuery,
  satisfiesLens,
  searchGarden,
  splitRanges,
  toggleToken,
  type Range,
  type SearchEntry,
  type SeedMatch,
} from './gardenSearch';
import { Markdown } from './Markdown';
import { MarkdownReader } from './MarkdownReader';
import { seedMarkdownSource } from './MarkdownReader/documentSource';
import { SeedArtifactRows } from './SeedArtifactRows';
import type { SeedDocument } from './SeedDocumentView';
import type { SeedDocumentNote } from './seedArtifacts';
import './GardenPanel.css';

interface GardenPanelProps {
  isOpen: boolean;
  onClose: () => void;
  seeds: Seed[];
  seedsTotal: number;
  fetchSeedDocument?: (seedId: string) => Promise<SeedDocument>;
  onOpenAsTile?: (seedId: string) => void;
  onOpenMarkdownArtifact?: (path: string) => void;
  onResumeSeed?: (seedId: string) => void;
  checkArtifactPath?: (path: string) => Promise<boolean>;
  viewToggle?: React.ReactNode;
  frame?: 'dock' | 'full';
  onToggleFrame?: () => void;
  onEscapeFloor?: () => void;
}

const COLUMNS_MIN = 1160;
const THIRD_COLUMN_MIN = 1460;

function FrameGlyph({ direction }: { direction: 'out' | 'in' }) {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true">
      <path
        d={direction === 'out'
          ? 'M4.5 1.5H1.5V4.5M7.5 10.5H10.5V7.5'
          : 'M1.5 4.5H4.5V1.5M10.5 7.5H7.5V10.5'}
        stroke="currentColor"
        strokeWidth="1.25"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function formatPlantedAt(iso: string): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const deltaSec = Math.round((Date.now() - t) / 1000);
  if (deltaSec < 60) return 'just now';
  if (deltaSec < 3600) return `${Math.round(deltaSec / 60)}m ago`;
  if (deltaSec < 86400) return `${Math.round(deltaSec / 3600)}h ago`;
  return `${Math.round(deltaSec / 86400)}d ago`;
}

function formatTimestamp(iso: string): string {
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString();
}

function isSpoken(note: SeedDocumentNote): boolean {
  return note.kind !== 'attach' && note.kind !== 'detach';
}

function statusClass(status: string): string {
  switch (status) {
    case 'planted':
    case 'growing':
    case 'harvested':
    case 'withered':
    case 'dormant':
      return `is-${status}`;
    default:
      return 'is-unknown';
  }
}

function isClosed(seed: Seed): boolean {
  return seed.status === 'harvested' || seed.status === 'withered';
}

function crownOf(seed: Seed): string {
  return (seed.edges ?? []).find((edge) => edge.kind === 'part-of')?.to ?? '';
}

function isPlot(seed: Seed): boolean {
  return Boolean(seed.plot_progress);
}

function tenderOf(seed: Seed): string {
  return crewHolderName(seed.tender_member, seed.tender_session);
}

interface Relation {
  label: string;
  seed: Seed;
}

interface GardenIndex {
  byID: Map<string, Seed>;
  children: Map<string, Seed[]>;
  inbound: Map<string, Relation[]>;
  blockers: Map<string, number>;
  roots: Seed[];
}

function indexGarden(seeds: Seed[]): GardenIndex {
  const index: GardenIndex = {
    byID: new Map(),
    children: new Map(),
    inbound: new Map(),
    blockers: new Map(),
    roots: [],
  };
  for (const seed of seeds) index.byID.set(seed.id, seed);
  for (const seed of seeds) {
    const crown = crownOf(seed);
    if (crown && index.byID.has(crown)) {
      index.children.set(crown, [...(index.children.get(crown) ?? []), seed]);
    } else {
      index.roots.push(seed);
    }
    for (const edge of seed.edges ?? []) {
      const label = edge.kind === 'blocks'
        ? 'blocked by'
        : edge.kind === 'part-of'
          ? 'has part'
          : edge.kind === 'discovered-from'
            ? 'discovered'
            : '';
      if (!label) continue;
      index.inbound.set(edge.to, [...(index.inbound.get(edge.to) ?? []), { label, seed }]);
      if (edge.kind === 'blocks' && !isClosed(seed)) {
        index.blockers.set(edge.to, (index.blockers.get(edge.to) ?? 0) + 1);
      }
    }
  }
  return index;
}

function relationsOf(index: GardenIndex, id: string): Relation[] {
  const rows: Relation[] = [];
  for (const edge of index.byID.get(id)?.edges ?? []) {
    const other = index.byID.get(edge.to);
    if (!other) continue;
    if (edge.kind === 'blocks') rows.push({ label: 'blocks', seed: other });
    if (edge.kind === 'part-of') rows.push({ label: 'part of', seed: other });
    if (edge.kind === 'discovered-from') rows.push({ label: 'discovered from', seed: other });
  }
  return rows.concat((index.inbound.get(id) ?? []).filter((relation) => relation.label !== 'has part'));
}

function subtreeOf(index: GardenIndex, rootId: string): Set<string> {
  const wanted = new Set<string>();
  const queue = [rootId];
  while (queue.length > 0) {
    for (const child of index.children.get(queue.pop() as string) ?? []) {
      if (wanted.has(child.id)) continue;
      wanted.add(child.id);
      queue.push(child.id);
    }
  }
  return wanted;
}

function pathTo(index: GardenIndex, id: string): string[] {
  const path: string[] = [];
  const guard = new Set<string>();
  let cursor = index.byID.get(id);
  while (cursor && !guard.has(cursor.id)) {
    guard.add(cursor.id);
    path.unshift(cursor.id);
    const parent = crownOf(cursor);
    cursor = parent ? index.byID.get(parent) : undefined;
  }
  return path;
}

function signalOf(seed: Seed, blockers: number): { text: string; tone: string } | null {
  if (blockers > 0) return { text: `blocked by ${blockers}`, tone: 'blocked' };
  if (seed.status === 'growing') return { text: 'growing', tone: 'active' };
  if (seed.status === 'dormant') return { text: 'parked', tone: 'parked' };
  if (seed.status === 'withered') return { text: 'withered', tone: 'closed' };
  if (seed.status === 'harvested') return { text: 'done', tone: 'closed' };
  return null;
}

function progressWords(seed: Seed): string {
  const p = seed.plot_progress;
  if (!p) return '';
  const parts = [`${p.done}/${p.total} done`];
  if (p.growing) parts.push(`${p.growing} growing`);
  if (p.ready) parts.push(`${p.ready} ready`);
  if (p.blocked) parts.push(`${p.blocked} blocked`);
  if (p.dormant) parts.push(`${p.dormant} parked`);
  return parts.join(' · ');
}

function Marked({ text, ranges }: { text: string; ranges: Range[] }) {
  if (ranges.length === 0) return <>{text}</>;
  return (
    <>
      {splitRanges(text, ranges).map((part, i) =>
        part.hit ? (
          <mark key={i} className="garden-hit">{part.text}</mark>
        ) : (
          <span key={i}>{part.text}</span>
        ),
      )}
    </>
  );
}

interface RowProps {
  seed: Seed;
  blockers: number;
  onOpen: (id: string) => void;
  selected?: boolean;
  active?: boolean;
  match?: SeedMatch;
  home?: Seed;
  option?: boolean;
}

function SeedRow({ seed, blockers, onOpen, selected, active, match, home, option }: RowProps) {
  const progress = seed.plot_progress;
  const signal = signalOf(seed, blockers);
  const tender = tenderOf(seed);
  return (
    <li
      className={`garden-row ${statusClass(seed.status)}${isClosed(seed) ? ' is-closed' : ''}${selected ? ' is-selected' : ''}${active ? ' is-active' : ''}`}
    >
      <button
        type="button"
        id={`garden-row-${seed.id}`}
        role={option ? 'option' : undefined}
        aria-selected={option ? Boolean(active) : undefined}
        data-seed-row={seed.id}
        onClick={() => onOpen(seed.id)}
      >
        <span className="garden-row__pip" aria-hidden="true" />
        <span className="garden-row__title">
          <Marked text={seed.title} ranges={match?.titleRanges ?? []} />
        </span>
        {signal && <span className={`garden-row__signal is-${signal.tone}`}>{signal.text}</span>}
        {home && <span className="garden-row__home">in {home.title}</span>}
        {tender && <span className="garden-row__tender">tended by {tender}</span>}
        <span className={`garden-row__id${match?.idHit ? ' garden-hit' : ''}`}>{seed.id}</span>
        {progress && (
          <span className="garden-row__plot">
            {progress.done}/{progress.total}
            <span className="garden-row__chevron" aria-hidden="true">›</span>
          </span>
        )}
      </button>
      {/* Why this row is here, when its title does not say. */}
      {match?.snippet && (
        <p className="garden-row__snippet">
          <Marked text={match.snippet.text} ranges={match.snippet.ranges} />
        </p>
      )}
    </li>
  );
}

interface ListProps {
  seeds: Seed[];
  index: GardenIndex;
  onOpen: (id: string) => void;
  selectedId?: string;
  activeId?: string;
  matchByID?: Map<string, SeedMatch>;
  homes?: boolean;
  hereId?: string | null;
  emptyMessage?: React.ReactNode;
  listId?: string;
  options?: boolean;
}

function SeedList({ seeds, index, onOpen, selectedId, activeId, matchByID, homes, hereId, emptyMessage, listId, options }: ListProps) {
  if (seeds.length === 0) return emptyMessage ? <p className="garden-empty">{emptyMessage}</p> : null;
  return (
    <ul className="garden-list" id={listId} role={options ? 'listbox' : undefined}>
      {seeds.map((seed) => (
        <SeedRow
          key={seed.id}
          seed={seed}
          blockers={index.blockers.get(seed.id) ?? 0}
          onOpen={onOpen}
          selected={seed.id === selectedId}
          active={seed.id === activeId}
          match={matchByID?.get(seed.id)}
          home={homes && crownOf(seed) !== hereId ? index.byID.get(crownOf(seed)) : undefined}
          option={options}
        />
      ))}
    </ul>
  );
}

function BookkeepingDisclosure({ notes }: { notes: SeedDocumentNote[] }) {
  const [shown, setShown] = useState(false);
  return (
    <>
      <button
        type="button"
        className="garden-closed-toggle"
        aria-expanded={shown}
        onClick={() => setShown((open) => !open)}
      >
        <span className="garden-closed-toggle__caret" aria-hidden="true">{shown ? '⌄' : '›'}</span>
        {notes.length} attachment {notes.length === 1 ? 'change' : 'changes'}
      </button>
      {shown && (
        <ol className="garden-log garden-log--quiet">
          {notes.map((note) => (
            <li key={note.id} data-kind={note.kind}>
              <div className="garden-log__head">
                <span className="garden-log__kind">{note.kind}</span>
                <span className="garden-log__who">{note.body || '—'}</span>
                <time dateTime={note.created_at} title={formatTimestamp(note.created_at)}>
                  {formatPlantedAt(note.created_at)}
                </time>
              </div>
            </li>
          ))}
        </ol>
      )}
    </>
  );
}

function ColumnList({
  levelKey,
  seeds,
  index,
  selectedId,
  memory,
  onOpen,
}: {
  levelKey: string;
  seeds: Seed[];
  index: GardenIndex;
  selectedId: string;
  memory: React.MutableRefObject<Map<string, number>>;
  onOpen: (id: string) => void;
}) {
  const ref = useRef<HTMLDivElement | null>(null);
  useLayoutEffect(() => {
    const el = ref.current;
    if (el) el.scrollTop = memory.current.get(`col:${levelKey}`) ?? 0;
  }, [levelKey, memory, seeds]);
  return (
    <div
      className="garden-column"
      data-column={levelKey}
      ref={ref}
      onScroll={() => {
        const el = ref.current;
        if (el) memory.current.set(`col:${levelKey}`, el.scrollTop);
      }}
    >
      <SeedList
        seeds={seeds}
        index={index}
        onOpen={onOpen}
        selectedId={selectedId}
        emptyMessage={<>Nothing here yet.</>}
      />
    </div>
  );
}

function ProgressBar({ seed }: { seed: Seed }) {
  const p = seed.plot_progress;
  if (!p || p.total === 0) return null;
  const rest = Math.max(0, p.total - p.done - p.growing - p.dormant - p.withered);
  const segments: Array<[string, number]> = [
    ['done', p.done - p.withered > 0 ? p.done - p.withered : p.done],
    ['growing', p.growing],
    ['parked', p.dormant],
    ['withered', p.withered],
    ['rest', rest],
  ];
  return (
    <div className="garden-progress" aria-hidden="true">
      {segments
        .filter(([, count]) => count > 0)
        .map(([name, count]) => (
          <span key={name} className={`garden-progress__seg is-${name}`} style={{ flexGrow: count }} />
        ))}
    </div>
  );
}

export function GardenPanel({
  isOpen,
  onClose,
  seeds,
  seedsTotal,
  fetchSeedDocument,
  onOpenAsTile,
  onOpenMarkdownArtifact,
  onResumeSeed,
  checkArtifactPath,
  viewToggle,
  frame,
  onToggleFrame,
  onEscapeFloor,
}: GardenPanelProps) {
  const trail = useGardenWalk((walk) => walk.trail);
  const setTrail = useGardenWalk((walk) => walk.setTrail);
  const [query, setQuery] = useState('');
  const [wideIn, setWideIn] = useState<string | null>(null);
  const [walk, setWalk] = useState<{ of: string; index: number }>({ of: '', index: 0 });
  const [seedDocument, setSeedDocument] = useState<SeedDocument | null>(null);
  const [documentError, setDocumentError] = useState<string | null>(null);
  const [titlePinned, setTitlePinned] = useState(false);
  const [trailOpen, setTrailOpen] = useState(false);
  const [panelWidth, setPanelWidth] = useState(0);
  const columnsRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);

  const index = useMemo(() => indexGarden(seeds), [seeds]);

  useEffect(() => setTrailOpen(false), [trail.length]);

  const measurePanel = useCallback((node: HTMLDivElement) => {
    setPanelWidth(node.clientWidth);
    const observer = new ResizeObserver(([entry]) => setPanelWidth(entry.contentRect.width));
    observer.observe(node);
    return () => observer.disconnect();
  }, []);
  const layout = panelWidth >= COLUMNS_MIN ? 'columns' : 'stack';

  const livingTrail = useMemo(() => {
    const alive: string[] = [];
    for (const id of trail) {
      if (!index.byID.has(id)) break;
      alive.push(id);
    }
    return alive;
  }, [trail, index]);

  const here = livingTrail.length > 0 ? index.byID.get(livingTrail[livingTrail.length - 1]) : undefined;
  const plotId = livingTrail.length > 0 ? livingTrail[livingTrail.length - 1] : null;
  const pageKey = livingTrail.length > 0 ? livingTrail.join('>') : 'root';

  const viewportRef = useRef<HTMLDivElement | null>(null);
  const headerRef = useRef<HTMLDivElement | null>(null);
  const pageKeyRef = useRef(pageKey);
  const scrollMemory = useRef<Map<string, number>>(gardenScrollMemory);
  const arrival = useRef<{ direction: 'in' | 'out'; fromRow: string }>({ direction: 'in', fromRow: '' });

  const parsed = useMemo(() => parseQuery(query), [query]);
  const filtering = parsed.active;
  const searching = parsed.searches;
  const wide = searching && wideIn !== null && wideIn === plotId;
  const question = `${plotId ?? ''} ${query}`;
  const activeIndex = walk.of === question ? walk.index : 0;
  // Lowercased once per snapshot: doing it per keystroke is what makes
  // client-side search feel slow; receipts in gardenSearch.bench.ts.
  const entries = useMemo(
    () => buildIndex(seeds, { tenderOf, blockersOf: (seed: Seed) => index.blockers.get(seed.id) ?? 0 }),
    [seeds, index],
  );
  const entryByID = useMemo(() => {
    const map = new Map<string, SearchEntry>();
    for (const entry of entries) map.set(entry.seed.id, entry);
    return map;
  }, [entries]);

  const lens = useCallback(
    (rows: Seed[]) =>
      rows.filter((seed) => {
        const entry = entryByID.get(seed.id);
        return entry ? satisfiesLens(entry, parsed.is) : !isClosed(seed);
      }),
    [entryByID, parsed],
  );

  const plotPool = useMemo(() => {
    if (!plotId) return entries;
    const wanted = subtreeOf(index, plotId);
    return entries.filter((entry) => wanted.has(entry.seed.id));
  }, [plotId, index, entries]);
  const pool = wide ? entries : plotPool;
  const results = useMemo(() => (searching ? searchGarden(pool, parsed) : []), [searching, pool, parsed]);
  const matchByID = useMemo(() => {
    const map = new Map<string, SeedMatch>();
    for (const match of results) map.set(match.seed.id, match);
    return map;
  }, [results]);
  const gardenWide = useMemo(
    () => (searching && plotId && !wide ? searchGarden(entries, parsed).length : 0),
    [searching, plotId, wide, entries, parsed],
  );
  const inPlot = useMemo(
    () => (searching && wide ? searchGarden(plotPool, parsed).length : 0),
    [searching, wide, plotPool, parsed],
  );
  const withClosed = useMemo(() => {
    if (!searching || parsed.is.length > 0) return 0;
    return searchGarden(pool, parseQuery(`${query} is:any`)).length;
  }, [searching, parsed, pool, query]);
  const outside = Math.max(0, gardenWide - results.length);
  const hiddenClosed = Math.max(0, withClosed - results.length);

  const rememberScroll = useCallback(() => {
    const el = viewportRef.current;
    if (el) scrollMemory.current.set(pageKeyRef.current, el.scrollTop);
  }, []);

  const drillInto = useCallback((id: string) => {
    rememberScroll();
    arrival.current = { direction: 'in', fromRow: '' };
    setTrail((prev) => [...prev, id]);
  }, [rememberScroll, setTrail]);

  const climbTo = useCallback((depth: number) => {
    rememberScroll();
    setTrail((prev) => {
      arrival.current = { direction: 'out', fromRow: prev[depth] ?? '' };
      return prev.slice(0, depth);
    });
  }, [rememberScroll, setTrail]);

  const selectAtLevel = useCallback((level: number, id: string) => {
    setTrail((prev) => {
      arrival.current = { direction: prev.length > level ? 'out' : 'in', fromRow: '' };
      return [...prev.slice(0, level), id];
    });
  }, [setTrail]);

  const openResult = useCallback((id: string) => {
    rememberScroll();
    arrival.current = { direction: 'in', fromRow: '' };
    setTrail(pathTo(index, id));
    setQuery('');
    setWideIn(null);
  }, [index, rememberScroll, setTrail]);

  const climbOne = useCallback(() => {
    climbTo(Math.max(0, livingTrail.length - 1));
  }, [climbTo, livingTrail.length]);

  const focusSearch = useCallback(() => {
    const input = inputRef.current;
    if (!input) return;
    input.focus();
    input.select();
  }, []);
  const toggleWide = useCallback(() => {
    setWideIn((cur) => (cur === null ? plotId : null));
    focusSearch();
  }, [focusSearch, plotId]);
  const toggleClosedLens = useCallback(() => {
    setQuery((cur) => toggleToken(cur, 'is:any'));
    focusSearch();
  }, [focusSearch]);

  // Keep the frame below controls that register after opening, such as a lightbox.
  useEscapeStack(onEscapeFloor ?? (() => {}), isOpen && !!onEscapeFloor);

  const hereId = here?.id ?? '';
  useEffect(() => {
    if (!isOpen || !hereId || !fetchSeedDocument) {
      setSeedDocument(null);
      setDocumentError(null);
      return;
    }
    let ignore = false;
    setDocumentError(null);
    fetchSeedDocument(hereId)
      .then((document) => {
        if (!ignore) setSeedDocument(document);
      })
      .catch((error) => {
        if (!ignore) setDocumentError(error instanceof Error ? error.message : `Could not read ${hereId}`);
      });
    return () => {
      ignore = true;
    };
  }, [hereId, fetchSeedDocument, isOpen, seeds]);

  const onPageKeyDown = useCallback((event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp' && event.key !== 'ArrowLeft') return;
    if (event.key === 'ArrowLeft') {
      if (livingTrail.length === 0) return;
      event.preventDefault();
      climbOne();
      return;
    }
    const el = viewportRef.current;
    if (!el) return;
    const rows = Array.from(el.querySelectorAll<HTMLElement>('[data-seed-row]'));
    if (rows.length === 0) return;
    event.preventDefault();
    const at = rows.indexOf(document.activeElement as HTMLElement);
    const next = event.key === 'ArrowDown'
      ? (at < 0 ? 0 : Math.min(rows.length - 1, at + 1))
      : (at < 0 ? rows.length - 1 : Math.max(0, at - 1));
    rows[next].focus({ preventScroll: true });
    rows[next].scrollIntoView({ block: 'nearest' });
  }, [climbOne, livingTrail.length]);

  const onColumnsKeyDown = useCallback((event: React.KeyboardEvent<HTMLDivElement>) => {
    const keys = ['ArrowDown', 'ArrowUp', 'ArrowLeft', 'ArrowRight'];
    if (!keys.includes(event.key)) return;
    const active = document.activeElement as HTMLElement | null;
    const walked = columnsRef.current?.querySelectorAll<HTMLElement>('[data-column]');
    const column =
      active?.closest<HTMLElement>('[data-column]') ?? (walked?.length ? walked[walked.length - 1] : null);

    if (event.key === 'ArrowLeft') {
      if (livingTrail.length === 0) return;
      event.preventDefault();
      climbOne();
      return;
    }
    if (event.key === 'ArrowRight') {
      if (!active?.dataset.seedRow) return;
      event.preventDefault();
      active.click();
      return;
    }
    if (!column) return;
    const rows = Array.from(column.querySelectorAll<HTMLElement>('[data-seed-row]'));
    if (rows.length === 0) return;
    event.preventDefault();
    const at = rows.indexOf(active as HTMLElement);
    const next = event.key === 'ArrowDown'
      ? (at < 0 ? 0 : Math.min(rows.length - 1, at + 1))
      : (at < 0 ? rows.length - 1 : Math.max(0, at - 1));
    rows[next].focus({ preventScroll: true });
    rows[next].scrollIntoView({ block: 'nearest' });
  }, [climbOne, livingTrail.length]);

  const resultSeeds = useMemo(() => results.map((match) => match.seed), [results]);
  const activeSeed = resultSeeds.length > 0 ? resultSeeds[Math.min(activeIndex, resultSeeds.length - 1)] : undefined;

  const onSearchKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      if (resultSeeds.length === 0) {
        if (layout === 'columns') onColumnsKeyDown(event as React.KeyboardEvent<HTMLDivElement>);
        else onPageKeyDown(event as React.KeyboardEvent<HTMLDivElement>);
        return;
      }
      event.preventDefault();
      const step = event.key === 'ArrowDown' ? 1 : -1;
      setWalk({ of: question, index: Math.min(resultSeeds.length - 1, Math.max(0, activeIndex + step)) });
      return;
    }
    if (event.key === 'Enter' && event.altKey) {
      if (plotId && searching && (wide || outside > 0)) {
        event.preventDefault();
        toggleWide();
      }
      return;
    }
    if (event.key === 'Enter' && activeSeed) {
      event.preventDefault();
      openResult(activeSeed.id);
    }
  };

  const onPanelKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    const target = event.target as HTMLElement | null;
    const typing = target?.tagName === 'INPUT' || target?.tagName === 'TEXTAREA' || target?.isContentEditable;
    if ((event.key === 'f' && event.metaKey) || (event.key === '/' && !typing)) {
      event.preventDefault();
      event.stopPropagation();
      focusSearch();
      return;
    }
    if (typing) return;
    if (layout === 'columns') onColumnsKeyDown(event);
    else onPageKeyDown(event);
  };

  useLayoutEffect(() => {
    pageKeyRef.current = pageKey;
    const el = viewportRef.current;
    if (!el) return;
    el.scrollTop = scrollMemory.current.get(pageKey) ?? 0;
    setTitlePinned(false);
    const { direction, fromRow } = arrival.current;
    if (direction === 'out' && fromRow) {
      const row = el.querySelector<HTMLElement>(`[data-seed-row="${fromRow}"]`);
      if (row) {
        row.focus({ preventScroll: true });
        return;
      }
    }
    el.focus({ preventScroll: true });
  }, [pageKey]);

  const onViewportScroll = useCallback(() => {
    const el = viewportRef.current;
    if (!el) return;
    scrollMemory.current.set(pageKeyRef.current, el.scrollTop);
    const header = headerRef.current;
    const pinned = Boolean(header) && el.scrollTop > header!.offsetTop + header!.clientHeight - 12;
    setTitlePinned((prev) => (prev === pinned ? prev : pinned));
  }, []);

  if (!isOpen) return null;

  const children = here ? index.children.get(here.id) ?? [] : [];
  const levelSeeds = here ? children : index.roots;
  const closedHere = levelSeeds.reduce((n, seed) => (isClosed(seed) ? n + 1 : n), 0);
  const closedOn = parsed.is.includes('any');
  const otherLens = parsed.is.some((value) => value !== 'any');
  const closedCount = !searching
    ? closedHere
    : closedOn
      ? results.reduce((n, match) => (isClosed(match.seed) ? n + 1 : n), 0)
      : hiddenClosed;
  const closedToggle = otherLens || (!closedOn && closedCount === 0) ? null : { count: closedCount, on: closedOn };

  const seedDoc = seedDocument && here && seedDocument.seed.id === here.id ? seedDocument : null;
  const artifacts = seedDoc?.artifacts ?? [];
  const notes = seedDoc?.notes ?? [];
  const notesTotal = seedDoc?.notes_total ?? 0;
  const withheld = Math.max(0, notesTotal - notes.length);
  const spoken = notes.filter(isSpoken);
  const bookkeeping = notes.length - spoken.length;
  const relations = here ? relationsOf(index, here.id) : [];
  const levels: { key: string; seeds: Seed[]; selectedId: string }[] = [
    { key: 'root', seeds: lens(index.roots), selectedId: livingTrail[0] ?? '' },
  ];
  for (let depth = 0; depth < livingTrail.length; depth++) {
    const parent = index.byID.get(livingTrail[depth]);
    const kids = parent ? index.children.get(parent.id) ?? [] : [];
    if (kids.length === 0) break;
    levels.push({
      key: livingTrail.slice(0, depth + 1).join('>'),
      seeds: lens(kids),
      selectedId: livingTrail[depth + 1] ?? '',
    });
  }
  const maxColumns = panelWidth >= THIRD_COLUMN_MIN ? 3 : 2;
  const visibleLevels = levels.slice(Math.max(0, levels.length - maxColumns));
  const firstVisibleLevel = levels.length - visibleLevels.length;

  const trailAncestors =
    layout === 'columns' && !searching
      ? livingTrail.slice(0, Math.min(firstVisibleLevel, livingTrail.length - 1))
      : livingTrail.slice(0, -1);
  const foldTrail = !trailOpen && trailAncestors.length > 3;
  const shownAncestors = foldTrail ? trailAncestors.slice(-2) : trailAncestors;
  const foldedCount = trailAncestors.length - shownAncestors.length;
  const where = plotId && !wide ? 'this plot' : 'the garden';

  const trailNav = (
    <div className="garden-chrome">
      <nav
        className={`garden-trail${wide ? ' is-wide' : ''}`}
        aria-label={wide ? 'Standing here, searching the whole garden' : 'The way back'}
      >
        {livingTrail.length === 0 ? (
          <span className="garden-trail__here">The garden</span>
        ) : (
          <>
            <button type="button" className="garden-trail__step" data-trail-depth="0" onClick={() => climbTo(0)}>
              Garden
            </button>
            {foldTrail && (
              <span className="garden-trail__seg">
                <span className="garden-trail__sep" aria-hidden="true">›</span>
                <button
                  type="button"
                  className="garden-trail__step garden-trail__fold"
                  onClick={() => setTrailOpen(true)}
                  aria-label={`Show ${foldedCount} more steps`}
                >
                  …
                </button>
              </span>
            )}
            {shownAncestors.map((id, offset) => {
              const depth = foldedCount + offset;
              return (
                <span key={id} className="garden-trail__seg">
                  <span className="garden-trail__sep" aria-hidden="true">›</span>
                  <button
                    type="button"
                    className="garden-trail__step"
                    data-trail-depth={depth + 1}
                    onClick={() => climbTo(depth + 1)}
                  >
                    {index.byID.get(id)?.title ?? id}
                  </button>
                </span>
              );
            })}
            {(titlePinned || searching) && here && (
              <span className="garden-trail__seg">
                <span className="garden-trail__sep" aria-hidden="true">›</span>
                <span className="garden-trail__here">{here.title}</span>
              </span>
            )}
          </>
        )}
      </nav>
      {viewToggle}
      {closedToggle && (
        <button
          type="button"
          className="garden-chrome__scope"
          aria-pressed={closedToggle.on}
          onClick={toggleClosedLens}
        >
          {closedToggle.on
            ? closedToggle.count > 0
              ? `hide ${closedToggle.count} closed`
              : 'hide closed'
            : `${closedToggle.count} closed`}
        </button>
      )}
      {/* The way between the two frames, quiet, beside the way out. */}
      {onToggleFrame && (
        <button
          type="button"
          className="garden-chrome__frame"
          onClick={onToggleFrame}
          aria-label={frame === 'full' ? 'Return the garden to the dock' : 'Expand the garden'}
          title={frame === 'full' ? 'Return to the dock (Esc)' : 'Expand (⌘⇧T)'}
        >
          <FrameGlyph direction={frame === 'full' ? 'in' : 'out'} />
        </button>
      )}
      <button type="button" className="garden-chrome__close" onClick={onClose} aria-label="Close">×</button>
    </div>
  );

  const searchLine = (
    <div className={`garden-search${filtering ? ' is-active' : ''}`}>
      <span className="garden-search__glyph" aria-hidden="true">/</span>
      <input
        ref={inputRef}
        className="garden-search__input"
        type="text"
        role="combobox"
        aria-expanded={filtering}
        aria-controls="garden-results"
        aria-activedescendant={searching && activeSeed ? `garden-row-${activeSeed.id}` : undefined}
        aria-label={`Search ${where}`}
        placeholder="search — or is:ready, tender:…"
        spellCheck={false}
        autoComplete="off"
        value={query}
        onChange={(event) => setQuery(event.target.value)}
        onKeyDown={onSearchKeyDown}
      />
      <span className="garden-search__meta">
        {parsed.unknown.length > 0 ? (
          <span className="garden-search__unknown">no filter called {parsed.unknown.join(' ')}</span>
        ) : parsed.partial ? (
          <span className="garden-search__values">
            {parsed.partial === 'is' ? (
              IS_VALUES.map((value) => (
                <button
                  key={value}
                  type="button"
                  className="garden-search__value"
                  onClick={() => {
                    setQuery((cur) => `${cur.replace(/is:\S*$/i, '')}is:${value} `);
                    focusSearch();
                  }}
                >
                  {value}
                </button>
              ))
            ) : (
              <span className="garden-search__value-hint">a crew member or session</span>
            )}
          </span>
        ) : (
          filtering && (
            <>
              {plotId && !wide && outside > 0 && (
                <button type="button" className="garden-search__widen" onClick={toggleWide}>
                  +{outside} in the whole garden <kbd>⌥↵</kbd>
                </button>
              )}
              {plotId && wide && (
                <button type="button" className="garden-search__widen" onClick={toggleWide}>
                  {inPlot} in this plot <kbd>⌥↵</kbd>
                </button>
              )}
            </>
          )
        )}
      </span>
    </div>
  );

  const nothingFound = (
    <div className="garden-nothing">
      <p className="garden-nothing__line">
        Nothing in {where} matches <span className="garden-nothing__echo">{query.trim()}</span>.
      </p>
      <ul className="garden-nothing__moves">
        {plotId && !wide && outside > 0 && (
          <li>
            <button type="button" onClick={toggleWide}>
              Search the whole garden <span className="garden-nothing__count">{outside}</span>
            </button>
            <kbd>⌥↵</kbd>
          </li>
        )}
        {plotId && wide && (
          <li>
            <button type="button" onClick={toggleWide}>
              Search this plot only <span className="garden-nothing__count">{inPlot}</span>
            </button>
            <kbd>⌥↵</kbd>
          </li>
        )}
        {hiddenClosed > 0 && (
          <li>
            <button type="button" onClick={toggleClosedLens}>
              Include closed seeds <span className="garden-nothing__count">{hiddenClosed}</span>
            </button>
          </li>
        )}
        <li>
          <button
            type="button"
            onClick={() => {
              setQuery('');
              focusSearch();
            }}
          >
            Clear the search
          </button>
          <kbd>esc</kbd>
        </li>
      </ul>
    </div>
  );

  const resultsNode = resultSeeds.length === 0 ? nothingFound : (
    <SeedList
      seeds={resultSeeds}
      index={index}
      onOpen={openResult}
      activeId={activeSeed?.id}
      matchByID={matchByID}
      homes
      hereId={plotId}
      options
      listId="garden-results"
    />
  );

  const readerNode = here ? (
    <>
      <div className="garden-head" ref={headerRef}>
        <div className="garden-head__row">
          <h2 className="garden-head__title">{here.title}</h2>
          <div className="garden-head__actions">
            {onOpenAsTile && (
              <button type="button" onClick={() => onOpenAsTile(here.id)}>Open as tile</button>
            )}
            {onResumeSeed && (here.tender_session || here.resume_session_id) && (
              <button type="button" data-testid={`seed-reopen-${here.id}`} onClick={() => onResumeSeed(here.id)}>
                Reopen agent
              </button>
            )}
          </div>
        </div>
        <div className="garden-head__meta">
          <span className={`garden-head__state ${statusClass(here.status)}`}>
            <span className="garden-row__pip" aria-hidden="true" />
            {here.status}
          </span>
          {tenderOf(here) && <span>tended by {tenderOf(here)}</span>}
          {here.planter_member && <span>by {crewDisplayName(here.planter_member)}</span>}
          <span>{formatPlantedAt(here.created_at)}</span>
          <span className="garden-head__id">{here.id}</span>
        </div>
        {/* Only a closed seed has one, so it is an exception by construction —
            and it is the one thing the reader of a closed seed came for. */}
        {here.reason && <p className="garden-head__reason">{here.reason}</p>}
        {isPlot(here) && (
          <div className="garden-head__progress">
            <ProgressBar seed={here} />
            <span>{progressWords(here)}</span>
          </div>
        )}
      </div>

      {here.body.trim() ? (
        <div className="garden-body">
          <MarkdownReader content={here.body} source={seedMarkdownSource(here.id)} allowLocalTargets={false} />
        </div>
      ) : (
        <p className="garden-note-empty">No body — the title is the whole seed.</p>
      )}

      {layout === 'stack' && isPlot(here) && (
        <section className="garden-section">
          <h3>Plot</h3>
          {lens(children).length === 0 && closedHere > 0 ? (
            <p className="garden-empty">
              Nothing open here. {closedHere} closed {closedHere === 1 ? 'seed is' : 'seeds are'} behind the
              closed toggle above.
            </p>
          ) : (
            <SeedList
              seeds={lens(children)}
              index={index}
              onOpen={drillInto}
              emptyMessage={<>Nothing planted in this plot yet. <code>attn seed plant &quot;what this is&quot; --part-of {here.id}</code> puts something in it.</>}
            />
          )}
        </section>
      )}

      {relations.length > 0 && (
        <section className="garden-section">
          <h3>Related</h3>
          <ul className="garden-relations">
            {relations.map((relation) => (
              <li key={`${relation.label}:${relation.seed.id}`}>
                <span className="garden-relations__label">{relation.label}</span>
                <button type="button" onClick={() => drillInto(relation.seed.id)}>{relation.seed.title}</button>
                <span className="garden-relations__state">{relation.seed.status}</span>
              </li>
            ))}
          </ul>
        </section>
      )}

      {artifacts.length > 0 && (
        <section className="garden-section">
          <h3>Artifacts</h3>
          <SeedArtifactRows
            artifacts={artifacts}
            onOpenMarkdownArtifact={onOpenMarkdownArtifact}
            checkArtifactPath={checkArtifactPath}
          />
        </section>
      )}

      <section className="garden-section">
        <h3>Log</h3>
        {documentError ? (
          <p className="garden-note-empty garden-note-empty--error">{documentError}</p>
        ) : notes.length === 0 ? (
          <p className="garden-note-empty">Nothing on this seed’s log yet.</p>
        ) : (
          <>
            <ol className="garden-log">
              {spoken.map((note) => (
                <li key={note.id} data-kind={note.kind} className={note.kind === 'handoff' ? 'is-handoff' : ''}>
                  <div className="garden-log__head">
                    <span className="garden-log__who">{note.author_member || note.author_session || '—'}</span>
                    {note.kind !== 'note' && <span className="garden-log__kind">{note.kind}</span>}
                    <time dateTime={note.created_at} title={formatTimestamp(note.created_at)}>
                      {formatPlantedAt(note.created_at)}
                    </time>
                  </div>
                  {note.body && <Markdown className="garden-log__body" breaks>{note.body}</Markdown>}
                </li>
              ))}
            </ol>
            {bookkeeping > 0 && <BookkeepingDisclosure notes={notes.filter((note) => !isSpoken(note))} />}
          </>
        )}
        {withheld > 0 && (
          <p className="garden-note-empty">{withheld} more {withheld === 1 ? 'entry' : 'entries'} on the log.</p>
        )}
      </section>
    </>
  ) : null;

  const capped = seedsTotal > seeds.length && (
    <p className="garden-capped">
      {filtering
        ? `Search covers the newest ${seeds.length} of ${seedsTotal} seeds.`
        : `The garden holds ${seedsTotal} seeds; this panel has the newest ${seeds.length}.`}
    </p>
  );

  if (layout === 'columns') {
    const panes = searching ? 1 + (here ? 1 : 0) : visibleLevels.length + (here ? 1 : 0);
    return (
      <div ref={measurePanel} className="garden-panel is-columns" role="region" aria-label="The garden" onKeyDown={onPanelKeyDown}>
        {trailNav}
        {searchLine}
        <div
          className="garden-columns"
          data-panes={panes}
          data-mode={searching ? 'search' : 'walk'}
          ref={columnsRef}
        >
          {searching ? (
            <div className="garden-column garden-column--results">{resultsNode}</div>
          ) : (
            visibleLevels.map((level, offset) => (
              <ColumnList
                key={level.key}
                levelKey={level.key}
                seeds={level.seeds}
                index={index}
                selectedId={level.selectedId}
                memory={scrollMemory}
                onOpen={(id) => selectAtLevel(firstVisibleLevel + offset, id)}
              />
            ))
          )}
          {here && (
            <div
              className="garden-column garden-column--reader"
              ref={viewportRef}
              tabIndex={-1}
              onScroll={onViewportScroll}
            >
              <div className="garden-page" key={pageKey} data-arrival={arrival.current.direction}>
                {readerNode}
              </div>
            </div>
          )}
          {!here && capped}
        </div>
      </div>
    );
  }

  return (
    <div ref={measurePanel} className="garden-panel" role="region" aria-label="The garden" onKeyDown={onPanelKeyDown}>
      {trailNav}
      {searchLine}
      <div
        className="garden-viewport"
        ref={viewportRef}
        tabIndex={-1}
        onScroll={onViewportScroll}
      >
        <div
          className="garden-page"
          key={searching ? 'results' : pageKey}
          data-arrival={arrival.current.direction}
        >
          {searching ? (
            <>
              {capped}
              {resultsNode}
            </>
          ) : !here ? (
            <>
              {capped}
              {lens(index.roots).length === 0 && closedHere > 0 ? (
                <p className="garden-empty">
                  Nothing open here. {closedHere} closed {closedHere === 1 ? 'seed is' : 'seeds are'} behind the
                  closed toggle above.
                </p>
              ) : (
                <SeedList
                  seeds={lens(index.roots)}
                  index={index}
                  onOpen={drillInto}
                  selectedId={livingTrail[0] ?? ''}
                  emptyMessage={<>The garden is empty. <code>attn seed plant &quot;what this is&quot;</code> puts something in it.</>}
                />
              )}
            </>
          ) : (
            readerNode
          )}
        </div>
      </div>
    </div>
  );
}
