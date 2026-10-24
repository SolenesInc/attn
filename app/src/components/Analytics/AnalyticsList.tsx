import type { RefObject } from 'react';
import type { Seed } from '../../types/generated';
import { age, clockTime } from './analyticsFormat';
import type { ListItem, RecordItem, VentItem } from './analyticsItems';
import type { RecordsState } from './useAnalyticsRecords';

export function AnalyticsList({ label, items, index, seeds, listRef, onSelect }: {
  label: string;
  items: ListItem[];
  index: number;
  seeds: ReadonlyMap<string, Seed>;
  listRef: RefObject<HTMLOListElement | null>;
  onSelect: (item: ListItem, current: boolean) => void;
}) {
  return (
    <ol ref={listRef} className="analytics-list" aria-label={label}>
      {items.map((item, i) => (
        <li key={item.key}>
          {item.kind === 'vent' && <GroupHead item={item} previous={items[i - 1] as VentItem | undefined} />}
          {item.kind === 'vent'
            ? <VentRow item={item} seeds={seeds} current={i === index} onSelect={() => onSelect(item, i === index)} />
            : <RecordRow item={item} current={i === index} onSelect={() => onSelect(item, i === index)} />}
        </li>
      ))}
    </ol>
  );
}

function GroupHead({ item, previous }: { item: VentItem; previous?: VentItem }) {
  if (previous && previous.done === item.done) return null;
  return (
    <div className="analytics-group">
      {item.done ? 'Done' : 'New'}
      {!item.done && <span className="analytics-group-keys"><kbd>p</kbd> plant a seed <kbd>d</kbd> dismiss</span>}
    </div>
  );
}

function VentRow({ item: { vent, done }, seeds, current, onSelect }: { item: VentItem; seeds: ReadonlyMap<string, Seed>; current: boolean; onSelect: () => void }) {
  const planted = vent.planted_seed_id;
  const outcome = vent.withdrawn_at ? 'withdrawn' : planted ? 'planted' : 'dismissed';
  return (
    <button type="button" className={`analytics-row vent${done ? ' done' : ''}`} aria-current={current} onClick={onSelect}>
      {!done && <span className="analytics-dot" />}
      <span className="analytics-row-text"><b>{vent.context.session_label}</b>{vent.text}</span>
      {done && <span className={`analytics-outcome ${outcome}`}>{planted && !vent.withdrawn_at ? seeds.get(planted)?.step_slug ?? planted : outcome}</span>}
      <span className="analytics-age">{age(vent.created_at, Date.now())}</span>
    </button>
  );
}

function RecordRow({ item, current, onSelect }: { item: RecordItem; current: boolean; onSelect: () => void }) {
  const first = item.kind === 'burst' ? item.records[0] : item.record;
  return (
    <button type="button" className="analytics-row record" aria-current={current} onClick={onSelect}>
      <span>{clockTime(first.at)}</span>
      <span className="analytics-row-text">
        {first.name}
        {item.kind === 'burst' && <> <span className="analytics-burst">×{item.records.length}</span></>}
      </span>
      <span className="analytics-age">{first.stream}</span>
    </button>
  );
}

export function RecordsFooter({ state, empty, onOlder, onRefresh }: { state: RecordsState | null; empty: boolean; onOlder: () => void; onRefresh: () => void }) {
  if (!state || (state.loading && empty)) return <p className="analytics-footer">Reading the record…</p>;
  const summary = state.error
    ? <span className="settings-warning">{state.error}</span>
    : <span>{empty ? 'Nothing recorded yet.' : `${state.records.length.toLocaleString()} newest first`}</span>;
  return (
    <div className="analytics-footer">
      {summary}
      <span className="analytics-footer-actions">
        {state.more && <button type="button" className="bus-more" disabled={state.loading} onClick={onOlder}>Older</button>}
        <button type="button" className="bus-more" disabled={state.loading} onClick={onRefresh}>Refresh</button>
      </span>
    </div>
  );
}
