import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import { useDaemonStore } from '../../store/daemonSessions';
import { useVentsStore } from '../../store/vents';
import { formatBytes, recordCount } from './analyticsFormat';
import { isDone, recordItems, ventItems, VIEW_LABEL, VIEWS, type BurstItem, type ListItem, type View } from './analyticsItems';
import { RecordDetail, VentDetail } from './AnalyticsInspector';
import { AnalyticsList, RecordsFooter } from './AnalyticsList';
import { AnalyticsMachine, useAnalyticsStatus } from './AnalyticsMachine';
import { useAnalyticsRecords } from './useAnalyticsRecords';
import { UndoToastBar, useUndoToast } from './useUndoToast';
import { useVentTriage } from './useVentTriage';
import '../EventBusSettings.css';
import './AnalyticsSettings.css';

interface AnalyticsSettingsProps {
  onDisableRecording: () => void;
  onOpenSession: (sessionId: string) => void;
  onOpenSeed: (seedId: string) => void;
}

function isTyping(target: EventTarget): boolean {
  return target instanceof HTMLElement && (target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName));
}

export function AnalyticsSettings({ onDisableRecording, onOpenSession, onOpenSeed }: AnalyticsSettingsProps) {
  const { sendClearAnalyticsRecords } = useDaemonApi();
  const vents = useVentsStore((state) => state.vents);
  const seeds = useDaemonStore((state) => state.seeds);
  const [view, setView] = useState<View>('vents');
  const [selected, setSelected] = useState<string | null>(null);
  const [machineOpen, setMachineOpen] = useState(false);
  const [openBursts, setOpenBursts] = useState<ReadonlySet<string>>(new Set());
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const records = useAnalyticsRecords(view);
  const status = useAnalyticsStatus();
  const feedback = useUndoToast();
  const rootRef = useRef<HTMLElement>(null);
  const listRef = useRef<HTMLOListElement>(null);

  const seedsById = useMemo(() => new Map(seeds.map((seed) => [seed.id, seed])), [seeds]);
  const items: ListItem[] = useMemo(
    () => (view === 'vents' ? ventItems(vents, seedsById) : recordItems(records.records?.records ?? [], openBursts)),
    [view, vents, seedsById, records.records, openBursts],
  );
  const index = Math.max(0, items.findIndex((item) => item.key === selected));
  const current = items[index];
  const vent = current?.kind === 'vent' ? current.vent : undefined;
  const newCount = useMemo(() => vents.filter((v) => !isDone(v)).length, [vents]);
  const triage = useVentTriage({ items, index, select: setSelected, feedback, onOpenSession, onOpenSeed });

  useEffect(() => {
    rootRef.current?.focus({ preventScroll: true });
  }, []);

  useEffect(() => {
    listRef.current?.querySelector<HTMLElement>('[aria-current="true"]')?.scrollIntoView?.({ block: 'nearest' });
  }, [current?.key]);

  const switchView = (next: View) => {
    setView(next);
    setSelected(null);
    setOpenBursts(new Set());
  };

  const expandBurst = (burst: BurstItem) => {
    setOpenBursts((open) => new Set(open).add(burst.key));
    setSelected(`record-${burst.records[0].id}`);
  };

  const refresh = () => {
    status.reload();
    void records.reload();
  };

  const clear = () => feedback.run(async () => {
    const cleared = await sendClearAnalyticsRecords();
    feedback.say(`Cleared ${recordCount(cleared)}.`);
    refresh();
  });

  const copyRecord = () => {
    if (!current || current.kind === 'vent') return false;
    const raw = current.kind === 'burst' ? current.records.map((r) => r.raw).join('\n') : current.record.raw;
    void navigator.clipboard.writeText(raw).then(() => feedback.say('Copied the record JSON.'), feedback.fail);
    return true;
  };

  const keys: Record<string, () => boolean | void> = {
    j: () => setSelected(items[Math.min(index + 1, items.length - 1)]?.key ?? null),
    k: () => setSelected(items[Math.max(index - 1, 0)]?.key ?? null),
    ']': () => switchView(VIEWS[(VIEWS.indexOf(view) + 1) % VIEWS.length]),
    '[': () => switchView(VIEWS[(VIEWS.indexOf(view) + VIEWS.length - 1) % VIEWS.length]),
    m: () => setMachineOpen((open) => !open),
    r: refresh,
    u: feedback.undo,
    c: copyRecord,
    Enter: () => current?.kind === 'burst' && (expandBurst(current), true),
  };
  keys.ArrowDown = keys.j;
  keys.ArrowUp = keys.k;

  const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (isTyping(event.target) || event.metaKey || event.ctrlKey || event.altKey) return;
    const handler = keys[event.key];
    const handled = handler ? handler() !== false : Boolean(vent && triage.act(event.key, vent));
    if (handled) event.preventDefault();
  };

  return (
    <section ref={rootRef} className="settings-block analytics" data-testid="settings-analytics" role="region" aria-label="Analytics" tabIndex={-1} onKeyDown={onKeyDown}>
      <div className="analytics-strip">
        <Stat label="Recording" value="On" />
        <Stat label="Sharing" value="Off" dim />
        <Stat label="Vents" value={`${vents.length} kept here`} dim={vents.length === 0} />
        <Stat label="Kept here" value={status.status ? `7 days · ${formatBytes(status.status.bytes)}` : '7 days'} />
        <button type="button" className="settings-action" aria-expanded={machineOpen} onClick={() => setMachineOpen((open) => !open)}>
          {machineOpen ? 'Done' : 'Machine settings'} <kbd>m</kbd>
        </button>
      </div>

      {machineOpen && <AnalyticsMachine status={status.status} onDisableRecording={onDisableRecording} onClear={clear} />}

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
          <AnalyticsList
            label={VIEW_LABEL[view]}
            items={items}
            index={index}
            seeds={seedsById}
            listRef={listRef}
            onSelect={(item, isCurrent) => {
              if (item.kind === 'burst' && isCurrent) expandBurst(item);
              else setSelected(item.key);
              rootRef.current?.focus({ preventScroll: true });
            }}
          />
          {view !== 'vents' && <RecordsFooter state={records.records} empty={items.length === 0} onOlder={records.older} onRefresh={refresh} />}
        </div>
        <div className="analytics-inspector" data-testid="analytics-inspector">
          {vent && (
            <VentDetail
              vent={vent}
              live={triage.isLive(vent)}
              seeds={seedsById}
              expanded={expanded.has(vent.id)}
              onExpand={() => setExpanded((open) => new Set(open).add(vent.id))}
              onAction={(key) => triage.act(key, vent)}
            />
          )}
          {current && current.kind !== 'vent' && <RecordDetail item={current} />}
          {!current && view === 'vents' && (
            <div>
              <p className="settings-row-title">No vents yet</p>
              <p className="settings-row-copy">Agents launched since you turned this on can send one.</p>
            </div>
          )}
        </div>
      </div>

      <KeyHints view={view} />
      <UndoToastBar toast={feedback.toast} />
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

const VENT_KEYS = [['o', 'open session'], ['t', 'transcript'], ['g', 'stake seed'], ['p', 'plant'], ['d', 'dismiss'], ['u', 'undo']];
const RECORD_KEYS = [['↵', 'expand a burst'], ['c', 'copy JSON'], ['r', 'refresh']];

function KeyHints({ view }: { view: View }) {
  return (
    <div className="analytics-keys">
      <span><kbd>j</kbd><kbd>k</kbd> move</span>
      <span><kbd>[</kbd><kbd>]</kbd> switch list</span>
      {(view === 'vents' ? VENT_KEYS : RECORD_KEYS).map(([key, label]) => <span key={key}><kbd>{key}</kbd> {label}</span>)}
      <span><kbd>m</kbd> machine settings</span>
    </div>
  );
}
