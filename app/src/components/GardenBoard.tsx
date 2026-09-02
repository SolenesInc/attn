// Drag is pointer events, not HTML5 drag-and-drop: WebKit's native drag session
// can be driven by nothing but a human hand, so the harness could not walk it.
import {
  Fragment,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import type { PointerEvent as ReactPointerEvent, ReactNode } from 'react';
import type { Seed } from '../hooks/useDaemonSocket';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { harvestWhenDisplay, type HarvestWhenDisplay } from '../utils/harvestWhen';
import {
  columnOf,
  heldByOther,
  legalVerbs,
  tenderOf,
  VERBS,
  verbsFor,
  type ColumnKey,
  type Verb,
} from './gardenBoardModel';
import './GardenBoard.css';

export interface GardenBoardProps {
  seeds: Seed[];
  seedsTotal: number;
  liveSessions: Set<string>;
  loaded: boolean;
  onTransition: (seedId: string, verb: Verb, reason?: string, force?: boolean, comment?: string) => Promise<unknown>;
  onNote: (seedId: string, body: string) => Promise<unknown>;
  viewToggle?: ReactNode;
  onClose: () => void;
  onEscapeFloor: () => void;
}

function carryTransform(point: { x: number; y: number }): string {
  const flip = point.x > window.innerWidth - 360;
  const dx = flip ? 'calc(-100% - 16px)' : '16px';
  return `translate3d(${point.x}px, ${point.y}px, 0) translate(${dx}, 12px)`;
}

function hitTest(x: number, y: number): { column: ColumnKey | null; verb: Verb | null } {
  const el = document.elementFromPoint(x, y);
  const zone = el?.closest('[data-zone]');
  const column = el?.closest('[data-column]');
  return {
    column: ((column as HTMLElement | null)?.dataset.column as ColumnKey) ?? null,
    verb: ((zone as HTMLElement | null)?.dataset.zone as Verb) ?? null,
  };
}

function crownOf(seed: Seed): string {
  return (seed.edges ?? []).find((edge) => edge.kind === 'part-of')?.to ?? '';
}

const COLUMNS: Array<{ key: ColumnKey; label: string }> = [
  { key: 'ready', label: 'Ready' },
  { key: 'growing', label: 'In progress' },
  { key: 'parked', label: 'Parked' },
  { key: 'closed', label: 'Closed' },
];

function ageOf(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const seconds = Math.round((Date.now() - t) / 1000);
  if (seconds < 60) return 'now';
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 86400)}d`;
}

function plotCounts(seed: Seed): string {
  const p = seed.plot_progress;
  if (!p) return '';
  const parts: string[] = [];
  if (p.growing) parts.push(`${p.growing} in progress`);
  if (p.ready) parts.push(`${p.ready} ready`);
  if (p.blocked) parts.push(`${p.blocked} blocked`);
  if (p.dormant) parts.push(`${p.dormant} parked`);
  parts.push(`${p.done}/${p.total} done`);
  return parts.join(' · ');
}

export function GardenBoard({
  seeds,
  seedsTotal,
  liveSessions,
  loaded,
  onTransition,
  onNote,
  viewToggle,
  onClose,
  onEscapeFloor,
}: GardenBoardProps) {
  const [trail, setTrail] = useState<string[]>([]);
  const [closedOpen, setClosedOpen] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const [menuFor, setMenuFor] = useState<string | null>(null);
  const [dragging, setDragging] = useState<Seed | null>(null);
  const [dragPoint, setDragPoint] = useState<{ x: number; y: number } | null>(null);
  const [hover, setHover] = useState<ColumnKey | null>(null);
  const [zoneHover, setZoneHover] = useState<Verb | null>(null);
  const [compose, setCompose] = useState<{ seed: Seed; verb: Verb; column: ColumnKey } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const composeInput = useRef<HTMLInputElement | null>(null);

  const byID = useMemo(() => {
    const map = new Map<string, Seed>();
    for (const seed of seeds) map.set(seed.id, seed);
    return map;
  }, [seeds]);

  const blockers = useMemo(() => {
    const counts = new Map<string, number>();
    for (const seed of seeds) {
      if (columnOf(seed) === 'closed') continue;
      for (const edge of seed.edges ?? []) {
        if (edge.kind === 'blocks') counts.set(edge.to, (counts.get(edge.to) ?? 0) + 1);
      }
    }
    return counts;
  }, [seeds]);

  const livingTrail = useMemo(() => {
    const alive: string[] = [];
    for (const id of trail) {
      if (!byID.has(id)) break;
      alive.push(id);
    }
    return alive;
  }, [trail, byID]);
  const plotId = livingTrail.length > 0 ? livingTrail[livingTrail.length - 1] : null;

  const scoped = useMemo(() => {
    if (!plotId) {
      return seeds.filter((seed) => {
        const parent = crownOf(seed);
        return !parent || !byID.has(parent);
      });
    }
    return seeds.filter((seed) => crownOf(seed) === plotId);
  }, [seeds, plotId, byID]);

  const columns = useMemo(() => {
    const out: Record<ColumnKey, Seed[]> = { ready: [], growing: [], parked: [], closed: [] };
    for (const seed of scoped) out[columnOf(seed)].push(seed);
    const newestFirst = (a: Seed, b: Seed) => Date.parse(b.updated_at) - Date.parse(a.updated_at);
    out.ready.sort((a, b) => {
      const pickable = (seed: Seed) => (seed.ready ? 0 : 1);
      if (pickable(a) !== pickable(b)) return pickable(a) - pickable(b);
      return Date.parse(a.created_at) - Date.parse(b.created_at);
    });
    out.growing.sort(newestFirst);
    out.parked.sort(newestFirst);
    out.closed.sort(newestFirst);
    return out;
  }, [scoped]);

  const readyCount = columns.ready.filter((seed) => seed.ready).length;
  const counts: Record<ColumnKey, number> = {
    ready: readyCount,
    growing: columns.growing.length,
    parked: columns.parked.length,
    closed: columns.closed.length,
  };

  const walkOrder = useMemo(() => {
    const order: Array<{ id: string; column: ColumnKey; row: number }> = [];
    for (const { key } of COLUMNS) {
      if (key === 'closed' && !closedOpen) continue;
      columns[key].forEach((seed, row) => order.push({ id: seed.id, column: key, row }));
    }
    return order;
  }, [columns, closedOpen]);

  useEffect(() => {
    if (selected && !byID.has(selected)) setSelected(null);
  }, [selected, byID]);

  useEffect(() => {
    if (compose) composeInput.current?.focus();
  }, [compose]);

  useEscapeStack(onEscapeFloor, true);
  useEscapeStack(() => setMenuFor(null), menuFor !== null);
  useEscapeStack(() => {
    setCompose(null);
    setError(null);
  }, compose !== null);

  const drillInto = useCallback((id: string) => {
    setSelected(null);
    setMenuFor(null);
    setTrail((prev) => [...prev, id]);
  }, []);
  const climbTo = useCallback((depth: number) => {
    setSelected(null);
    setMenuFor(null);
    setTrail((prev) => prev.slice(0, depth));
  }, []);

  const endDrag = useCallback(() => {
    setDragging(null);
    setDragPoint(null);
    setHover(null);
    setZoneHover(null);
  }, []);

  const beginVerb = useCallback((seed: Seed, verb: Verb, column: ColumnKey) => {
    setMenuFor(null);
    setError(null);
    endDrag();
    setCompose({ seed, verb, column });
  }, [endDrag]);

  const drag = useRef<{ seed: Seed; x: number; y: number; armed: boolean } | null>(null);
  const dragEndedAt = useRef(0);

  const beginPointerDrag = useCallback((seed: Seed, event: ReactPointerEvent<HTMLElement>) => {
    if (event.button !== 0) return;
    // WebKit leaves a clicked button unfocused; without this the focus ring and
    // the selection would disagree about where the reader is.
    (event.target as HTMLElement | null)?.closest?.('button')?.focus();
    drag.current = { seed, x: event.clientX, y: event.clientY, armed: false };

    const move = (ev: PointerEvent) => {
      const held = drag.current;
      if (!held) return;
      if (!held.armed) {
        if (Math.abs(ev.clientX - held.x) + Math.abs(ev.clientY - held.y) < 5) return;
        held.armed = true;
        setDragging(held.seed);
        setSelected(held.seed.id);
        setMenuFor(null);
      }
      setDragPoint({ x: ev.clientX, y: ev.clientY });
      const under = hitTest(ev.clientX, ev.clientY);
      setHover(under.column);
      setZoneHover(under.verb);
    };

    const release = (ev: PointerEvent) => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', release);
      window.removeEventListener('pointercancel', release);
      const held = drag.current;
      drag.current = null;
      if (!held?.armed) return;
      dragEndedAt.current = ev.timeStamp;
      const under = hitTest(ev.clientX, ev.clientY);
      if (under.column && under.verb) beginVerb(held.seed, under.verb, under.column);
      else endDrag();
    };

    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', release);
    window.addEventListener('pointercancel', release);
  }, [beginVerb, endDrag]);

  useEscapeStack(() => {
    drag.current = null;
    endDrag();
  }, dragging !== null);

  const commit = useCallback(async () => {
    if (!compose || busy) return;
    const text = (composeInput.current?.value ?? '').trim();
    const spec = VERBS[compose.verb];
    if (spec.required && !text) {
      setError(`${spec.label.toLowerCase()}ing ${compose.seed.id} records ${spec.prompt}`);
      composeInput.current?.focus();
      return;
    }
    setBusy(true);
    try {
      if (!spec.reasonOnSeed && compose.verb !== 'park' && text) await onNote(compose.seed.id, text);
      await onTransition(
        compose.seed.id,
        compose.verb,
        spec.reasonOnSeed ? text : undefined,
        heldByOther(compose.seed, liveSessions) !== '',
        compose.verb === 'park' ? text : undefined,
      );
      setCompose(null);
      setSelected(compose.seed.id);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }, [compose, busy, liveSessions, onNote, onTransition]);

  const onKeyDown = (event: KeyboardEvent) => {
    if (compose) return;
    if (menuFor) return;
    const target = event.target as HTMLElement | null;
    if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) return;
    const key = event.key;
    if (walkOrder.length === 0) return;
    const at = walkOrder.findIndex((entry) => entry.id === selected);
    const here = at >= 0 ? walkOrder[at] : null;

    if (key === 'ArrowDown' || key === 'ArrowUp') {
      event.preventDefault();
      if (!here) {
        setSelected(walkOrder[0].id);
        return;
      }
      const inColumn = walkOrder.filter((entry) => entry.column === here.column);
      const next = inColumn[Math.min(inColumn.length - 1, Math.max(0, here.row + (key === 'ArrowDown' ? 1 : -1)))];
      setSelected(next.id);
      return;
    }
    if (key === 'ArrowRight' || key === 'ArrowLeft') {
      const seed = selected ? byID.get(selected) : null;
      if (key === 'ArrowRight' && seed?.plot_progress) {
        event.preventDefault();
        drillInto(seed.id);
        return;
      }
      if (key === 'ArrowLeft' && !here) {
        if (livingTrail.length > 0) {
          event.preventDefault();
          climbTo(livingTrail.length - 1);
        }
        return;
      }
      event.preventDefault();
      if (!here) {
        setSelected(walkOrder[0].id);
        return;
      }
      const lane = COLUMNS.filter(({ key: k }) => k !== 'closed' || closedOpen).map(({ key: k }) => k);
      const step = key === 'ArrowRight' ? 1 : -1;
      for (let i = lane.indexOf(here.column) + step; i >= 0 && i < lane.length; i += step) {
        const target = columns[lane[i]];
        if (target.length > 0) {
          setSelected(target[Math.min(here.row, target.length - 1)].id);
          return;
        }
      }
      if (key === 'ArrowLeft' && livingTrail.length > 0) climbTo(livingTrail.length - 1);
      return;
    }
    if (key === 'Enter' && selected) {
      event.preventDefault();
      setMenuFor((cur) => (cur === selected ? null : selected));
    }
  };

  // WebKit does not focus a button when it is clicked, so a handler on the board's
  // own element would go deaf the moment the mouse touched a card.
  const keys = useRef(onKeyDown);
  useLayoutEffect(() => {
    keys.current = onKeyDown;
  });
  useEffect(() => {
    const listener = (event: KeyboardEvent) => keys.current(event);
    document.addEventListener('keydown', listener);
    return () => document.removeEventListener('keydown', listener);
  }, []);

  const capped = seedsTotal > seeds.length;
  const crown = plotId ? byID.get(plotId) : undefined;

  return (
    <div className="garden-board" role="region" aria-label="The garden board">
      <div className="garden-board__header">
        <span className="garden-board__kicker">The garden</span>
        <div className="garden-board__header-actions">
          {viewToggle}
          <span className="garden-board__total">{scoped.length}</span>
          <button type="button" className="garden-board__close" onClick={onClose} aria-label="Close">
            ×
          </button>
        </div>
      </div>

      {livingTrail.length > 0 && (
        <nav className="garden-board__trail" aria-label="Where you are">
          <button type="button" className="garden-board__trail-step" onClick={() => climbTo(0)}>
            Garden
          </button>
          {livingTrail.map((id, depth) => (
            <span key={id} className="garden-board__trail-segment">
              <span className="garden-board__trail-sep" aria-hidden="true">›</span>
              <button
                type="button"
                className="garden-board__trail-step"
                onClick={() => climbTo(depth + 1)}
                disabled={depth === livingTrail.length - 1}
              >
                {byID.get(id)?.title ?? id}
              </button>
            </span>
          ))}
          {crown && <span className="garden-board__trail-id">{crown.id}</span>}
        </nav>
      )}

      {error && (
        <p className="garden-board__error" role="alert">
          {error}
          <button type="button" onClick={() => setError(null)} aria-label="Dismiss">×</button>
        </p>
      )}

      {!loaded ? (
        <p className="garden-board__state">Reading the garden…</p>
      ) : seeds.length === 0 ? (
        <p className="garden-board__state">
          The garden is empty. <code>attn seed plant "what this is"</code> puts something in it.
        </p>
      ) : (
        <div className={`garden-board__columns${dragging ? ' is-dragging' : ''}`}>
          {COLUMNS.map(({ key, label }) => {
            const zones = dragging ? legalVerbs(dragging, key) : [];
            const collapsed = key === 'closed' && !closedOpen && compose?.column !== 'closed';
            const composing = compose?.column === key ? compose : null;
            return (
              <section
                key={key}
                className={[
                  'garden-board__column',
                  `is-${key}`,
                  collapsed ? 'is-collapsed' : '',
                  dragging && zones.length === 0 ? 'is-inert' : '',
                  hover === key && zones.length > 0 ? 'is-hovered' : '',
                ].filter(Boolean).join(' ')}
                aria-label={`${label}, ${counts[key]}`}
                data-column={key}
              >
                <header className="garden-board__head">
                  <h2 className="garden-board__label">{label}</h2>
                  <span className="garden-board__count">{counts[key]}</span>
                  {key === 'closed' && columns.closed.length > 0 && (
                    <button
                      type="button"
                      className="garden-board__reveal"
                      onClick={() => setClosedOpen((cur) => !cur)}
                      aria-expanded={closedOpen}
                    >
                      {closedOpen ? 'hide' : 'show'}
                    </button>
                  )}
                </header>

                <div className="garden-board__body">
                  {composing && (
                    <Composer
                      compose={composing}
                      takenFrom={heldByOther(composing.seed, liveSessions)}
                      busy={busy}
                      inputRef={composeInput}
                      onCommit={commit}
                      onCancel={() => {
                        setCompose(null);
                        setError(null);
                      }}
                    />
                  )}

                  {collapsed ? (
                    <ClosedSummary seeds={columns.closed} />
                  ) : columns[key].length === 0 ? (
                    <p className="garden-board__empty">{emptyLine(key, columns, scoped.length)}</p>
                  ) : (
                    <ul className="garden-board__cards">
                      {columns[key].map((seed, row) => (
                        <Fragment key={seed.id}>
                          {/* Ready's not-ready remainder is named rather than hidden:
                              a board that drops work is worse than a long column. */}
                          {key === 'ready' && row === readyCount && readyCount < columns.ready.length && (
                            <li className="garden-board__split" aria-hidden="true">
                              {columns.ready.length - readyCount} not ready yet
                            </li>
                          )}
                        <li>
                          <Card
                            seed={seed}
                            column={key}
                            selected={selected === seed.id}
                            menuOpen={menuFor === seed.id}
                            blockers={blockers.get(seed.id) ?? 0}
                            tenderLive={
                              !seed.tender_session || liveSessions.has(seed.tender_session)
                            }
                            onSelect={() => setSelected(seed.id)}
                            onPrimary={() => {
                              setSelected(seed.id);
                              if (seed.plot_progress) drillInto(seed.id);
                              else setMenuFor((cur) => (cur === seed.id ? null : seed.id));
                            }}
                            onDrill={() => drillInto(seed.id)}
                            onMenu={() => setMenuFor((cur) => (cur === seed.id ? null : seed.id))}
                            onVerb={(verb) => beginVerb(seed, verb, targetColumn(verb))}
                            onDragPointerDown={(event) => beginPointerDrag(seed, event)}
                            wasDragged={() => performance.now() - dragEndedAt.current < 250}
                          />
                        </li>
                        </Fragment>
                      ))}
                    </ul>
                  )}

                  {/* Zones exist only for the verbs this card's state would accept, so
                      the board can never offer a move the daemon would refuse. */}
                  {hover === key && zones.length > 0 && dragging && (
                    <div className="garden-board__zones">
                      {zones.map((verb) => (
                        <button
                          key={verb}
                          type="button"
                          className={`garden-board__zone${zoneHover === verb ? ' is-over' : ''}`}
                          data-zone={verb}
                          onClick={() => beginVerb(dragging, verb, key)}
                        >
                          <span className="garden-board__zone-verb">{VERBS[verb].label}</span>
                          <span className="garden-board__zone-id">{dragging.id}</span>
                        </button>
                      ))}
                    </div>
                  )}
                </div>
              </section>
            );
          })}
        </div>
      )}

      {/* Ours, not WebKit's snapshot. */}
      {dragging && dragPoint && (
        <div
          className="garden-board__carry"
          style={{ transform: carryTransform(dragPoint) }}
          aria-hidden="true"
        >
          <span className="garden-board__carry-title">{dragging.title}</span>
          <span className="garden-board__carry-id">{dragging.id}</span>
        </div>
      )}

      {capped && (
        <p className="garden-board__capped">
          The garden holds {seedsTotal} seeds; this board has the newest {seeds.length}.
        </p>
      )}

    </div>
  );
}

function targetColumn(verb: Verb): ColumnKey {
  switch (verb) {
    case 'harvest':
    case 'wither':
      return 'closed';
    case 'park':
      return 'parked';
    case 'replant':
      return 'ready';
  }
}

function emptyLine(key: ColumnKey, columns: Record<ColumnKey, Seed[]>, here: number): string {
  if (here === 0) return 'Nothing planted here yet.';
  switch (key) {
    case 'ready':
      if (columns.growing.length > 0) return 'Nothing to pick up — everything open is being worked.';
      if (columns.parked.length > 0) return `Nothing to pick up. ${columns.parked.length} parked.`;
      return 'Nothing to pick up here.';
    case 'growing':
      return 'Nobody is working on anything here.';
    case 'parked':
      return 'Nothing put down.';
    case 'closed':
      return 'Nothing has finished here yet.';
  }
}

function ClosedSummary({ seeds }: { seeds: Seed[] }) {
  const harvested = seeds.filter((seed) => seed.status === 'harvested').length;
  const withered = seeds.length - harvested;
  if (seeds.length === 0) return <p className="garden-board__empty">Nothing has finished here yet.</p>;
  return (
    <p className="garden-board__summary">
      <span>{harvested} harvested</span>
      {withered > 0 && <span className="is-withered">{withered} withered</span>}
    </p>
  );
}

interface CardProps {
  seed: Seed;
  column: ColumnKey;
  selected: boolean;
  menuOpen: boolean;
  blockers: number;
  tenderLive: boolean;
  onSelect: () => void;
  onPrimary: () => void;
  onDrill: () => void;
  onMenu: () => void;
  onVerb: (verb: Verb) => void;
  onDragPointerDown: (event: ReactPointerEvent<HTMLElement>) => void;
  wasDragged: () => boolean;
}

function Card({
  seed, column, selected, menuOpen, blockers, tenderLive,
  onSelect, onPrimary, onDrill, onMenu, onVerb, onDragPointerDown, wasDragged,
}: CardProps) {
  const verbs = verbsFor(seed);
  const plot = seed.plot_progress ? plotCounts(seed) : '';
  const armed = harvestWhenDisplay(seed.harvest_when);
  const menu = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (!menuOpen) return;
    menu.current?.querySelector<HTMLButtonElement>('[role="menuitem"]')?.focus();
  }, [menuOpen]);
  return (
    <div
      className={[
        'garden-card',
        selected ? 'is-selected' : '',
        seed.plot_progress ? 'is-crown' : '',
        seed.ready ? 'is-ready' : 'is-not-ready',
        column === 'closed' ? `is-${seed.status}` : '',
      ].filter(Boolean).join(' ')}
      data-seed={seed.id}
      onPointerDown={onDragPointerDown}
    >
      <button
        type="button"
        className="garden-card__body"
        aria-expanded={menuOpen}
        onFocus={onSelect}
        onClick={() => {
          if (wasDragged()) return;
          onPrimary();
        }}
      >
        <span className="garden-card__title">{seed.title}</span>
        <span className="garden-card__meta">
          <CardMeta seed={seed} column={column} blockers={blockers} tenderLive={tenderLive} plot={plot} armed={armed} />
          <span className="garden-card__id">{seed.id}</span>
          <span className="garden-card__age">{ageOf(seed.updated_at || seed.created_at)}</span>
        </span>
      </button>
      {seed.plot_progress && (
        <button type="button" className="garden-card__drill" onClick={onDrill} aria-label={`Open the plot under ${seed.title}`}>
          ›
        </button>
      )}
      {menuOpen && (
        <div
          className="garden-card__menu"
          role="menu"
          ref={menu}
          onKeyDown={(event) => {
            if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
            event.preventDefault();
            event.stopPropagation();
            const items = Array.from(
              event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="menuitem"]'),
            );
            const at = items.indexOf(document.activeElement as HTMLButtonElement);
            const step = event.key === 'ArrowDown' ? 1 : -1;
            items[Math.min(items.length - 1, Math.max(0, at + step))]?.focus();
          }}
        >
          {verbs.length === 0 ? (
            <p className="garden-card__menu-empty">Nothing moves a {seed.status} seed from here.</p>
          ) : (
            verbs.map((verb) => (
              <button key={verb} type="button" role="menuitem" onClick={() => onVerb(verb)}>
                {VERBS[verb].label}
                <span aria-hidden="true">…</span>
              </button>
            ))
          )}
        </div>
      )}
      {menuOpen && verbs.length > 0 && <span className="garden-card__menu-hint">esc</span>}
      {!menuOpen && selected && verbs.length > 0 && (
        <button type="button" className="garden-card__more" onClick={onMenu} aria-label={`Move ${seed.title}`}>
          ⏎
        </button>
      )}
    </div>
  );
}

function CardMeta({
  seed, column, blockers, tenderLive, plot, armed,
}: {
  seed: Seed; column: ColumnKey; blockers: number; tenderLive: boolean; plot: string;
  armed: HarvestWhenDisplay | null;
}) {
  if (plot) return <span className="garden-card__plot">{plot}</span>;
  const marker = armed ? <span className="garden-card__armed" title={armed.sentence}>{armed.marker}</span> : null;
  switch (column) {
    case 'ready':
      if (seed.ready) {
        return <>
          <span className="garden-card__ready">ready</span>
          {marker}
        </>;
      }
      if (blockers > 0) return <span className="garden-card__held">blocked by {blockers}</span>;
      if (marker) return marker;
      if (seed.template) return <span className="garden-card__held">packet</span>;
      if (seed.gate) return <span className="garden-card__held">gate</span>;
      return <span className="garden-card__held">not ready</span>;
    case 'growing':
      return (
        <span className={`garden-card__tender${tenderLive ? '' : ' is-gone'}`}>
          {tenderOf(seed) || 'held'}
          {!tenderLive && <span className="garden-card__gone"> · session gone</span>}
        </span>
      );
    case 'parked':
      return marker ?? <span className="garden-card__held">parked</span>;
    case 'closed':
      return (
        <span className={`garden-card__closed is-${seed.status}`}>
          {seed.reason ? seed.reason : seed.status}
        </span>
      );
  }
}

function Composer({
  compose, takenFrom, busy, inputRef, onCommit, onCancel,
}: {
  compose: { seed: Seed; verb: Verb; column: ColumnKey };
  takenFrom: string;
  busy: boolean;
  inputRef: React.RefObject<HTMLInputElement | null>;
  onCommit: () => void;
  onCancel: () => void;
}) {
  const spec = VERBS[compose.verb];
  return (
    <div className="garden-compose">
      <span className="garden-compose__verb">{spec.label.toLowerCase()}</span>
      <span className="garden-compose__id">{compose.seed.id}</span>
      <input
        ref={inputRef}
        className="garden-compose__input"
        type="text"
        autoComplete="off"
        spellCheck={false}
        disabled={busy}
        placeholder={spec.required ? spec.prompt : `${spec.prompt} — optional`}
        aria-label={`${spec.label} ${compose.seed.id}: ${spec.prompt}`}
        onKeyDown={(event) => {
          event.stopPropagation();
          if (event.key === 'Enter') {
            event.preventDefault();
            onCommit();
          }
          if (event.key === 'Escape') {
            event.preventDefault();
            onCancel();
          }
        }}
      />
      <span className="garden-compose__keys">
        <kbd>⏎</kbd>
        <kbd>esc</kbd>
      </span>
      {/* Park commits this with the move; replant writes it as an ordinary log note. */}
      {!spec.reasonOnSeed && <span className="garden-compose__where">goes on the log</span>}
      {takenFrom !== '' && (
        <span className="garden-compose__taking">takes it from {takenFrom}</span>
      )}
    </div>
  );
}
