import type { ReactNode } from 'react';
import { VentSend, type Seed, type Vent } from '../../types/generated';
import { harnessLabel } from '../harnessLabel';
import { buildLabel, longAgo, settingName, when } from './analyticsFormat';
import type { RecordItem } from './analyticsItems';

const LONG_VENT_CHARS = 420;

export function RecordDetail({ item }: { item: RecordItem }) {
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

function ventStatus(vent: Vent, slug: (id: string) => string): ReactNode {
  if (vent.withdrawn_at) return <span className="settings-pill">Withdrawn by agent</span>;
  if (vent.planted_seed_id) return <span className="settings-pill good">Planted · {slug(vent.planted_seed_id)}</span>;
  if (vent.dismissed_at) return <span className="settings-pill">Dismissed</span>;
  return null;
}

export function VentDetail({ vent, live, sharing, seeds, expanded, onExpand, onAction }: {
  vent: Vent;
  live: boolean;
  sharing: boolean;
  seeds: ReadonlyMap<string, Seed>;
  expanded: boolean;
  onExpand: () => void;
  onAction: (key: string) => void;
}) {
  const withdrawn = Boolean(vent.withdrawn_at);
  const slug = (id: string) => seeds.get(id)?.step_slug ?? id;
  const long = vent.text.length > LONG_VENT_CHARS && !expanded;
  return (
    <>
      <div className="analytics-vent-head">
        <b>{vent.context.session_label}</b>
        {ventStatus(vent, slug)}
      </div>
      <div className={`analytics-vent-text${long ? ' clamped' : ''}${withdrawn ? ' struck' : ''}`}>{vent.text}</div>
      {long && <button type="button" className="analytics-more" onClick={onExpand}>Show all {vent.text.length.toLocaleString()} characters</button>}
      <VentContext vent={vent} slug={slug} />
      {!withdrawn && <VentActions vent={vent} live={live} onAction={onAction} />}
      <VentWhere vent={vent} sharing={sharing} onAction={onAction} />
    </>
  );
}

function VentWhere({ vent, sharing, onAction }: { vent: Vent; sharing: boolean; onAction: (key: string) => void }) {
  const withdrawn = Boolean(vent.withdrawn_at);
  const sent = vent.send === VentSend.Sent;
  const queued = !withdrawn && vent.send === VentSend.Queued;
  const sending = queued;
  const label = sent ? "Sent to attn's maintainers" : !queued ? 'Kept on this machine' : sharing ? 'Sending with the next batch' : 'Sending once sharing is on';
  const note = withdrawn ? 'withdrawn' : !sharing && !sent && !queued ? 'sharing is off' : '';
  const movable = !withdrawn && !sent && (sharing || queued);
  return (
    <div className="analytics-where">
      <span className={`analytics-where-dot${sent || sending ? ' sent' : ''}`} />
      <b>{label}</b>
      {note && <span className="dim">· {note}</span>}
      {movable && <button type="button" className="settings-action analytics-move" onClick={() => onAction('x')}><kbd>x</kbd> {vent.send ? 'Keep here' : 'Send'}</button>}
    </div>
  );
}

function VentContext({ vent: { created_at, context }, slug }: { vent: Vent; slug: (id: string) => string }) {
  const none = <span className="dim">none</span>;
  return (
    <dl className="analytics-context">
      <dt>When</dt><dd>{when(created_at)} <span className="dim">· {longAgo(created_at, Date.now())}</span></dd>
      <dt>Agent</dt><dd>{[harnessLabel(context.harness), context.harness_version].filter(Boolean).join(' ')}{context.model && <> <span className="dim">·</span> {context.model}</>}</dd>
      <dt>Stake seed</dt><dd>{context.seed_id ? slug(context.seed_id) : none}</dd>
      <dt>Build</dt><dd>{buildLabel(context.build)}</dd>
      <dt>Experimental</dt><dd>{context.experimental.length ? context.experimental.map(settingName).join(', ') : none}</dd>
    </dl>
  );
}

function VentActions({ vent, live, onAction }: { vent: Vent; live: boolean; onAction: (key: string) => void }) {
  const action = (key: string, label: string) => (
    <button type="button" onClick={() => onAction(key)}><kbd>{key}</kbd> {label}</button>
  );
  return (
    <div className="analytics-actions">
      {live && action('o', 'Open session')}
      {vent.context.transcript && action('t', 'Transcript')}
      {vent.context.seed_id && action('g', 'Stake seed')}
      {!vent.planted_seed_id && action('p', 'Plant seed')}
      {action('d', vent.dismissed_at ? 'Restore' : 'Dismiss')}
    </div>
  );
}
