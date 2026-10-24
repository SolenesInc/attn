import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { openPath } from '@tauri-apps/plugin-opener';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import type { AnalyticsRecordPage } from '../hooks/daemonAnalyticsEvents';
import { useDaemonStore } from '../store/daemonSessions';
import { useVentsStore } from '../store/vents';
import type { AnalyticsBuild, Vent } from '../types/generated';
import { harnessLabel } from './harnessLabel';
import './EventBusSettings.css';
import './AnalyticsSettings.css';

interface AnalyticsSettingsProps {
  onDisableRecording: () => void;
  onOpenSession: (sessionId: string) => void;
  onOpenSeed: (seedId: string) => void;
}

const VIEWS = ['vents', 'all', 'bus', 'app', 'cli', 'sample'] as const;
type View = (typeof VIEWS)[number];
const VIEW_LABEL: Record<View, string> = { vents: 'Vents', all: 'All records', bus: 'bus', app: 'app', cli: 'cli', sample: 'sample' };

const PAGE_SIZE = 200;
const LONG_VENT_CHARS = 420;
const SEED_TITLE_CHARS = 100;

interface StoredRecord {
  id: number;
  raw: string;
  stream: string;
  seq: number;
  name: string;
  at: string;
}

type RecordItem =
  | { kind: 'record'; key: string; record: StoredRecord }
  | { kind: 'burst'; key: string; records: StoredRecord[] };

type VentItem = { kind: 'vent'; key: string; vent: Vent; done: boolean };
type ListItem = VentItem | RecordItem;

interface RecordsState {
  view: View;
  records: StoredRecord[];
  more: boolean;
  loading: boolean;
  error: string | null;
}

interface Toast {
  message: ReactNode;
  undo?: () => Promise<unknown>;
}

const isDone = (vent: Vent) => Boolean(vent.dismissed_at || vent.withdrawn_at);
const doneAt = (vent: Vent) => vent.withdrawn_at || vent.dismissed_at || '';

function parseRecord({ id, record }: { id: number; record: string }): StoredRecord {
  try {
    const parsed = JSON.parse(record) as Partial<StoredRecord>;
    return { id, raw: record, stream: String(parsed.stream ?? ''), seq: Number(parsed.seq ?? 0), name: String(parsed.name ?? ''), at: String(parsed.at ?? '') };
  } catch {
    return { id, raw: record, stream: '', seq: 0, name: '', at: '' };
  }
}

function recordItems(records: StoredRecord[], openBursts: ReadonlySet<string>): RecordItem[] {
  const items: RecordItem[] = [];
  for (let i = 0; i < records.length; i++) {
    let j = i;
    while (j + 1 < records.length && records[j + 1].name === records[i].name && records[j + 1].at.slice(0, 19) === records[i].at.slice(0, 19)) j++;
    const run = records.slice(i, j + 1);
    const key = `burst-${records[i].id}`;
    if (run.length > 1 && !openBursts.has(key)) items.push({ kind: 'burst', key, records: run });
    else items.push(...run.map((record): RecordItem => ({ kind: 'record', key: `record-${record.id}`, record })));
    i = j;
  }
  return items;
}

function ventItems(vents: Vent[]): VentItem[] {
  const fresh = vents.filter((vent) => !isDone(vent));
  const done = vents.filter(isDone).sort((a, b) => doneAt(b).localeCompare(doneAt(a)));
  return [...fresh, ...done].map((vent) => ({ kind: 'vent', key: vent.id, vent, done: isDone(vent) }));
}

const clockTime = (iso: string) => {
  const t = new Date(iso);
  return Number.isNaN(t.getTime()) ? '' : t.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
};

function age(iso: string, now: number): string {
  const seconds = Math.max(0, (now - Date.parse(iso)) / 1000);
  if (!Number.isFinite(seconds)) return '';
  if (seconds < 60) return 'now';
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  return `${Math.floor(seconds / 86400)}d`;
}

function longAgo(iso: string, now: number): string {
  const short = age(iso, now);
  if (short === 'now' || short === '') return 'just now';
  const n = short.slice(0, -1);
  return { m: `${n} min ago`, h: `${n} h ago`, d: `${n} days ago` }[short.slice(-1)] ?? '';
}

function when(iso: string): string {
  const t = new Date(iso);
  if (Number.isNaN(t.getTime())) return iso;
  const day = t.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' });
  return `${day}, ${clockTime(iso)}`;
}

function buildLabel(build: AnalyticsBuild): string {
  const channel = build.channel ? build.channel[0].toUpperCase() + build.channel.slice(1) : 'Build';
  const version = build.channel === 'source' && build.commit ? build.commit.slice(0, 7) : build.version;
  return `${channel} ${version}${build.dirty ? ', uncommitted changes' : ''}`;
}

function settingName(key: string): string {
  const words = key.replace(/[._]enabled$/, '').replace(/[._]/g, ' ');
  return words.charAt(0).toUpperCase() + words.slice(1);
}

function seedTitle(text: string): string {
  const line = text.trim().split('\n')[0].trim();
  if (line.length <= SEED_TITLE_CHARS) return line;
  const cut = line.slice(0, SEED_TITLE_CHARS);
  return `${cut.slice(0, Math.max(cut.lastIndexOf(' '), SEED_TITLE_CHARS / 2))}…`;
}

function seedBody(vent: Vent): string {
  const agent = [harnessLabel(vent.context.harness), vent.context.model].filter(Boolean).join(' · ');
  return `${vent.text}\n\nFrom vent ${vent.id} by ${vent.context.session_label} (${agent}), ${when(vent.created_at)}.`;
}

function isTyping(target: EventTarget): boolean {
  return target instanceof HTMLElement && (target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName));
}

export function AnalyticsSettings({ onDisableRecording, onOpenSession, onOpenSeed }: AnalyticsSettingsProps) {
  const { sendListAnalyticsRecords, sendSetVentDismissed, sendSeedPlant, sendSeedTransition } = useDaemonApi();
  const vents = useVentsStore((state) => state.vents);
  const sessions = useDaemonStore((state) => state.daemonSessions);
  const seeds = useDaemonStore((state) => state.seeds);
  const [view, setView] = useState<View>('vents');
  const [selected, setSelected] = useState<string | null>(null);
  const [machineOpen, setMachineOpen] = useState(false);
  const [openBursts, setOpenBursts] = useState<ReadonlySet<string>>(new Set());
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const [records, setRecords] = useState<RecordsState | null>(null);
  const [toast, setToast] = useState<Toast | null>(null);
  const loadSeq = useRef(0);
  const rootRef = useRef<HTMLElement>(null);
  const listRef = useRef<HTMLOListElement>(null);

  const load = useCallback(async (target: View, before: StoredRecord[]) => {
    const seq = ++loadSeq.current;
    setRecords({ view: target, records: before, more: false, loading: true, error: null });
    try {
      const page: AnalyticsRecordPage = await sendListAnalyticsRecords(
        target === 'all' ? null : target,
        before.length ? before[before.length - 1].id : null,
        PAGE_SIZE,
      );
      if (loadSeq.current !== seq) return;
      setRecords({ view: target, records: [...before, ...page.records.map(parseRecord)], more: page.records.length === PAGE_SIZE, loading: false, error: null });
    } catch (err) {
      if (loadSeq.current !== seq) return;
      setRecords({ view: target, records: before, more: before.length > 0, loading: false, error: err instanceof Error ? err.message : String(err) });
    }
  }, [sendListAnalyticsRecords]);

  useEffect(() => {
    rootRef.current?.focus({ preventScroll: true });
  }, []);

  useEffect(() => {
    if (view !== 'vents') void load(view, []);
  }, [view, load]);

  const items: ListItem[] = useMemo(() => {
    if (view === 'vents') return ventItems(vents);
    return records?.view === view ? recordItems(records.records, openBursts) : [];
  }, [view, vents, records, openBursts]);

  const index = Math.max(0, items.findIndex((item) => item.key === selected));
  const current = items[index];
  const vent = current?.kind === 'vent' ? current.vent : undefined;
  const newCount = useMemo(() => vents.filter((v) => !isDone(v)).length, [vents]);
  const live = useMemo(() => new Set(sessions.map((session) => session.id)), [sessions]);

  useEffect(() => {
    listRef.current?.querySelector<HTMLElement>('[aria-current="true"]')?.scrollIntoView?.({ block: 'nearest' });
  }, [current?.key]);

  const say = (message: ReactNode, undo?: () => Promise<unknown>) => setToast({ message, undo });
  const fail = (err: unknown) => say(<span className="settings-warning">{err instanceof Error ? err.message : String(err)}</span>);
  const run = (action: () => Promise<void>) => { void action().catch(fail); };

  const expandBurst = (burst: RecordItem & { kind: 'burst' }) => {
    setOpenBursts((open) => new Set(open).add(burst.key));
    setSelected(`record-${burst.records[0].id}`);
  };

  const switchView = (next: View) => {
    setView(next);
    setSelected(null);
    setOpenBursts(new Set());
  };

  const setDismissed = (target: Vent, dismissed: boolean) => run(async () => {
    const nextNew = items.slice(index + 1).find((item) => item.kind === 'vent' && !item.done);
    setSelected(dismissed && nextNew ? nextNew.key : target.id);
    await sendSetVentDismissed(target.id, dismissed);
    const name = `“${target.context.session_label}”`;
    say(dismissed ? `Dismissed ${name}.` : `Restored ${name}.`, () => sendSetVentDismissed(target.id, !dismissed));
  });

  const plant = (target: Vent) => run(async () => {
    const seed = await sendSeedPlant(seedTitle(target.text), seedBody(target));
    say(<>Planted <b>{seed.step_slug}</b> from “{target.context.session_label}”.</>, () => sendSeedTransition(seed.id, 'wither', 'Planted from a vent by mistake'));
  });

  const undo = () => {
    const action = toast?.undo;
    if (!action) return false;
    setToast(null);
    void action().then(() => say('Undone.')).catch(fail);
    return true;
  };

  const copyRecord = (item: RecordItem) => {
    const raw = item.kind === 'burst' ? item.records.map((r) => r.raw).join('\n') : item.record.raw;
    void navigator.clipboard.writeText(raw).then(() => say('Copied the record JSON.'), fail);
  };

  const ventAction = (key: string, target: Vent): boolean => {
    const withdrawn = Boolean(target.withdrawn_at);
    if (key === 'o' && live.has(target.session_id)) onOpenSession(target.session_id);
    else if (key === 't' && target.context.transcript) void openPath(target.context.transcript.path).catch(fail);
    else if (key === 'g' && target.context.seed_id) onOpenSeed(target.context.seed_id);
    else if (key === 'p' && !withdrawn) plant(target);
    else if (key === 'd' && !withdrawn) setDismissed(target, !target.dismissed_at);
    else return false;
    return true;
  };

  const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (isTyping(event.target) || event.metaKey || event.ctrlKey || event.altKey) return;
    const { key } = event;
    let handled = true;
    if (key === 'j' || key === 'ArrowDown') setSelected(items[Math.min(index + 1, items.length - 1)]?.key ?? null);
    else if (key === 'k' || key === 'ArrowUp') setSelected(items[Math.max(index - 1, 0)]?.key ?? null);
    else if (key === ']' || key === '[') switchView(VIEWS[(VIEWS.indexOf(view) + (key === ']' ? 1 : VIEWS.length - 1)) % VIEWS.length]);
    else if (key === 'm') setMachineOpen((open) => !open);
    else if (key === 'u') handled = undo();
    else if (key === 'r' && view !== 'vents') void load(view, []);
    else if (key === 'Enter' && current?.kind === 'burst') expandBurst(current);
    else if (key === 'c' && current && current.kind !== 'vent') copyRecord(current);
    else if (vent) handled = ventAction(key, vent);
    else handled = false;
    if (handled) event.preventDefault();
  };

  return (
    <section ref={rootRef} className="settings-block analytics" data-testid="settings-analytics" tabIndex={-1} onKeyDown={onKeyDown}>
      <div className="analytics-strip">
        <Stat label="Recording" value="On" />
        <Stat label="Sharing" value="Off" dim />
        <Stat label="Vents" value={`${vents.length} kept here`} dim={vents.length === 0} />
        <Stat label="Kept here" value="7 days" />
        <button type="button" className="settings-action" aria-expanded={machineOpen} onClick={() => setMachineOpen((open) => !open)}>
          {machineOpen ? 'Done' : 'Machine settings'} <kbd>m</kbd>
        </button>
      </div>

      {machineOpen && (
        <div className="analytics-machine" data-testid="analytics-machine">
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">Record on this machine</p>
              <p className="settings-row-copy">Redacted bus facts, app actions, CLI outcomes and resource samples. New sessions get <code>attn vent</code>.</p>
            </div>
            <button type="button" className="settings-action" onClick={onDisableRecording}>Disable</button>
          </div>
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">Kept on this machine for 7 days</p>
              <p className="settings-row-copy">Dismissed and withdrawn vents are kept 30 days.</p>
            </div>
            <span />
          </div>
        </div>
      )}

      <div className="analytics-views" role="tablist" aria-label="Show">
        {VIEWS.map((v) => (
          <button key={v} type="button" role="tab" className="analytics-view" aria-selected={view === v} onClick={() => switchView(v)}>
            {VIEW_LABEL[v]}
            {v === 'vents' && newCount > 0 && <span className="analytics-count">{newCount}</span>}
          </button>
        ))}
      </div>

      <div className="analytics-split">
        <div className="analytics-list-column">
          <ol ref={listRef} className="analytics-list" aria-label={VIEW_LABEL[view]}>
            {items.map((item, i) => (
              <ListRow
                key={item.key}
                item={item}
                current={i === index}
                groupHead={item.kind === 'vent' && (i === 0 || (items[i - 1] as VentItem).done !== item.done) ? (item.done ? 'Done' : 'New') : null}
                onSelect={() => {
                  if (item.kind === 'burst' && i === index) expandBurst(item);
                  else setSelected(item.key);
                  rootRef.current?.focus({ preventScroll: true });
                }}
              />
            ))}
          </ol>
          {view !== 'vents' && <RecordsFooter state={records} empty={items.length === 0} onOlder={() => records && void load(view, records.records)} onRefresh={() => void load(view, [])} />}
        </div>
        <div className="analytics-inspector" data-testid="analytics-inspector">
          {vent ? (
            <VentDetail
              vent={vent}
              live={live.has(vent.session_id)}
              seedSlug={seeds.find((seed) => seed.id === vent.context.seed_id)?.step_slug}
              expanded={expanded.has(vent.id)}
              onExpand={() => setExpanded((open) => new Set(open).add(vent.id))}
              onAction={(key) => ventAction(key, vent)}
            />
          ) : current && current.kind !== 'vent' ? (
            <RecordDetail item={current} />
          ) : view === 'vents' ? (
            <div>
              <p className="settings-row-title">No vents yet</p>
              <p className="settings-row-copy">Agents launched since you turned this on can send one.</p>
            </div>
          ) : null}
        </div>
      </div>

      <div className="analytics-keys">
        <span><kbd>j</kbd><kbd>k</kbd> move</span>
        <span><kbd>[</kbd><kbd>]</kbd> switch list</span>
        {view === 'vents' ? (
          <>
            <span><kbd>o</kbd> open session</span>
            <span><kbd>t</kbd> transcript</span>
            <span><kbd>g</kbd> stake seed</span>
            <span><kbd>p</kbd> plant</span>
            <span><kbd>d</kbd> dismiss</span>
            <span><kbd>u</kbd> undo</span>
          </>
        ) : (
          <>
            <span><kbd>↵</kbd> expand a burst</span>
            <span><kbd>c</kbd> copy JSON</span>
            <span><kbd>r</kbd> refresh</span>
          </>
        )}
        <span><kbd>m</kbd> machine settings</span>
      </div>

      {toast && (
        <div className="analytics-toast" role="status">
          {toast.message}
          {toast.undo && <span className="analytics-toast-undo"><kbd>u</kbd> Undo</span>}
        </div>
      )}
    </section>
  );
}

function Stat({ label, value, dim = false }: { label: string; value: string; dim?: boolean }) {
  return (
    <div className="bus-stat">
      <span className="bus-stat-label">{label}</span>
      <span className={`bus-stat-value${dim ? ' dim' : ''}`}>{value}</span>
    </div>
  );
}

function ListRow({ item, current, groupHead, onSelect }: { item: ListItem; current: boolean; groupHead: 'New' | 'Done' | null; onSelect: () => void }) {
  const now = Date.now();
  let row: ReactNode;
  if (item.kind === 'vent') {
    const { vent, done } = item;
    const outcome = vent.withdrawn_at ? 'withdrawn' : 'dismissed';
    row = (
      <button type="button" className={`analytics-row vent${done ? ' done' : ''}`} aria-current={current} onClick={onSelect}>
        {!done && <span className="analytics-dot" />}
        <span className="analytics-row-text"><b>{vent.context.session_label}</b>{vent.text}</span>
        {done && <span className={`analytics-outcome ${outcome}`}>{outcome}</span>}
        <span className="analytics-age">{age(vent.created_at, now)}</span>
      </button>
    );
  } else if (item.kind === 'burst') {
    const first = item.records[0];
    row = (
      <button type="button" className="analytics-row record" aria-current={current} onClick={onSelect}>
        <span>{clockTime(first.at)}</span>
        <span className="analytics-row-text">{first.name} <span className="analytics-burst">×{item.records.length}</span></span>
        <span className="analytics-age">{first.stream}</span>
      </button>
    );
  } else {
    row = (
      <button type="button" className="analytics-row record" aria-current={current} onClick={onSelect}>
        <span>{clockTime(item.record.at)}</span>
        <span className="analytics-row-text">{item.record.name}</span>
        <span className="analytics-age">{item.record.stream}</span>
      </button>
    );
  }
  return (
    <li>
      {groupHead && (
        <div className="analytics-group">
          {groupHead}
          {groupHead === 'New' && <span className="analytics-group-keys"><kbd>p</kbd> plant a seed <kbd>d</kbd> dismiss</span>}
        </div>
      )}
      {row}
    </li>
  );
}

function RecordsFooter({ state, empty, onOlder, onRefresh }: { state: RecordsState | null; empty: boolean; onOlder: () => void; onRefresh: () => void }) {
  if (!state || (state.loading && empty)) return <p className="analytics-footer">Reading the record…</p>;
  return (
    <div className="analytics-footer">
      {state.error ? <span className="settings-warning">{state.error}</span> : empty ? <span>Nothing recorded yet.</span> : <span>{state.records.length.toLocaleString()} newest first</span>}
      <span className="analytics-footer-actions">
        {state.more && <button type="button" className="bus-more" disabled={state.loading} onClick={onOlder}>Older</button>}
        <button type="button" className="bus-more" disabled={state.loading} onClick={onRefresh}>Refresh</button>
      </span>
    </div>
  );
}

function RecordDetail({ item }: { item: RecordItem }) {
  const rows = item.kind === 'burst' ? item.records : [item.record];
  const first = rows[0];
  const last = rows[rows.length - 1];
  return (
    <>
      <div>
        <h4>{item.kind === 'burst' ? `${rows.length} records in the same second` : 'Record'}</h4>
        <dl className="analytics-context">
          <dt>Stream</dt><dd>{first.stream}</dd>
          <dt>Seq</dt><dd>{rows.length > 1 ? `${last.seq}–${first.seq}` : first.seq}</dd>
          <dt>At</dt><dd>{when(first.at)}</dd>
        </dl>
      </div>
      <div>
        <h4>Exactly as stored</h4>
        {rows.map((row) => <pre key={row.id} className="analytics-json">{row.raw}</pre>)}
      </div>
    </>
  );
}

function VentDetail({ vent, live, seedSlug, expanded, onExpand, onAction }: {
  vent: Vent;
  live: boolean;
  seedSlug?: string;
  expanded: boolean;
  onExpand: () => void;
  onAction: (key: string) => void;
}) {
  const now = Date.now();
  const { context } = vent;
  const withdrawn = Boolean(vent.withdrawn_at);
  const long = vent.text.length > LONG_VENT_CHARS && !expanded;
  const action = (key: string, label: string) => (
    <button type="button" onClick={() => onAction(key)}><kbd>{key}</kbd> {label}</button>
  );
  return (
    <>
      <div className="analytics-vent-head">
        <b>{context.session_label}</b>
        {withdrawn ? <span className="settings-pill">Withdrawn by agent</span> : vent.dismissed_at ? <span className="settings-pill">Dismissed</span> : null}
      </div>
      <div className={`analytics-vent-text${long ? ' clamped' : ''}${withdrawn ? ' struck' : ''}`}>{vent.text}</div>
      {long && <button type="button" className="analytics-more" onClick={onExpand}>Show all {vent.text.length.toLocaleString()} characters</button>}
      <dl className="analytics-context">
        <dt>When</dt><dd>{when(vent.created_at)} <span className="dim">· {longAgo(vent.created_at, now)}</span></dd>
        <dt>Agent</dt><dd>{[harnessLabel(context.harness), context.harness_version].filter(Boolean).join(' ')}{context.model && <> <span className="dim">·</span> {context.model}</>}</dd>
        <dt>Stake seed</dt><dd>{context.seed_id ? seedSlug ?? context.seed_id : <span className="dim">none</span>}</dd>
        <dt>Build</dt><dd>{buildLabel(context.build)}</dd>
        <dt>Experimental</dt><dd>{context.experimental.length ? context.experimental.map(settingName).join(', ') : <span className="dim">none</span>}</dd>
      </dl>
      {!withdrawn && (
        <div className="analytics-actions">
          {live && action('o', 'Open session')}
          {context.transcript && action('t', 'Transcript')}
          {context.seed_id && action('g', 'Stake seed')}
          {action('p', 'Plant seed')}
          {action('d', vent.dismissed_at ? 'Restore' : 'Dismiss')}
        </div>
      )}
      <div className="analytics-where">
        <span className="analytics-where-dot" />
        <b>Kept on this machine</b>
        {withdrawn && <span className="dim">· withdrawn</span>}
      </div>
    </>
  );
}
