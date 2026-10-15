import { useCallback, useEffect, useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import type { KeptConversationListResult, KeptConversationRow } from '../../types/generated';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { matchesWords } from './ledgerQuery';
import { fullStamp } from './ledgerTime';
import { Field, Inspector, LedgerList, QueryBar, useCopied } from './LedgerPrimitives';
import type { LedgerMenu, ListItem, RowNote, RowVerb } from './LedgerPrimitives';

export interface ConversationsTabProps {
  listConversations: (includeDeleted: boolean) => Promise<KeptConversationListResult>;
  setKeep: (conversationId: string, keep: boolean) => Promise<boolean>;
  forget: (conversationId: string) => Promise<boolean>;
  changeSignal: number;
  connectionGeneration: number;
  onOpenSeed: (seedId: string) => void;
  queryRef: React.RefObject<HTMLInputElement | null>;
  statusTarget: HTMLDivElement | null;
}

function amount(bytes: number): string {
  return bytes >= 1e6 ? `${(bytes / 1e6).toFixed(1)} MB`
    : bytes >= 1e3 ? `${(bytes / 1e3).toFixed(1)} KB` : `${bytes} B`;
}

function date(iso: string): string {
  return iso.slice(0, 10);
}

function pinned(row: KeptConversationRow): boolean {
  return Boolean(row.pinned_at || row.kept?.pinned_at);
}

function retention(row: KeptConversationRow): string {
  const kept = row.kept;
  if (kept?.deleted_at) return `${kept.deleted_by?.ref === 'user' ? 'You deleted attn’s copy' : 'attn deleted its copy'} on ${date(kept.deleted_at)}`;
  const pin = row.pinned_at || kept?.pinned_at;
  if (pin) return `Forever · pinned ${date(pin)}`;
  if (row.seeds.length) return `Open: ${row.seeds.map((seed) => seed.slug || seed.title).join(', ')}`;
  if (kept?.delete_after) return `Deletes ${date(kept.delete_after)}`;
  return row.pending_reason || 'Waiting for the next keep pass';
}

function verbs(row: KeptConversationRow, confirming: boolean): RowVerb[] {
  if (confirming) return [{ id: 'cancel', label: 'Cancel' }, { id: 'confirm-forget', label: 'Forget now', danger: true }];
  const actions: RowVerb[] = [{ id: pinned(row) ? 'unkeep' : 'keep', label: pinned(row) ? 'Unkeep' : 'Keep forever' }];
  if (row.kept && !row.kept.deleted_at && row.seeds.length === 0) actions.push({ id: 'forget', label: 'Forget…', danger: true });
  return actions;
}

export function ConversationsTab({
  listConversations, setKeep, forget, changeSignal, connectionGeneration, onOpenSeed, queryRef, statusTarget,
}: ConversationsTabProps) {
  const [includeDeleted, setIncludeDeleted] = useState(false);
  const [text, setText] = useState('');
  const [result, setResult] = useState<KeptConversationListResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [menu, setMenu] = useState<LedgerMenu | null>(null);
  const [confirmForget, setConfirmForget] = useState<string | null>(null);
  const [notices, setNotices] = useState<Record<string, RowNote | undefined>>({});
  const [copied, copy] = useCopied();
  useEscapeStack(() => setConfirmForget(null), confirmForget !== null);

  useEffect(() => {
    let ignore = false;
    void listConversations(includeDeleted).then((next) => {
      if (ignore) return;
      setResult(next);
      setError(null);
    }).catch((failure: Error) => { if (!ignore) setError(failure.message); });
    return () => { ignore = true; };
  }, [listConversations, includeDeleted, changeSignal, connectionGeneration]);

  const rows = useMemo(() => (result?.rows ?? []).filter((row) => (includeDeleted || !row.kept?.deleted_at) && matchesWords(
    [row.title, row.agent, row.resume_id, ...row.session_ids, retention(row), row.kept ? '' : 'pending', ...row.seeds.map((seed) => seed.title)],
    text.toLowerCase().trim().split(/\s+/).filter(Boolean),
  )), [result, includeDeleted, text]);
  const keyFor = (row: KeptConversationRow) => `${row.agent}:${row.resume_id}`;
  const selected = rows.find((row) => keyFor(row) === selectedKey) ?? rows[0] ?? null;

  const runVerb = useCallback((key: string, verb: string) => {
    const row = rows.find((candidate) => `${candidate.agent}:${candidate.resume_id}` === key);
    if (!row || notices[key]?.kind === 'busy') return;
    setSelectedKey(key);
    setMenu(null);
    if (verb === 'cancel') { setConfirmForget(null); return; }
    if (verb === 'forget') { setConfirmForget(key); return; }
    if (verb === 'confirm-forget' && confirmForget !== key) return;
    setConfirmForget(null);
    setNotices((current) => ({ ...current, [key]: { kind: 'busy', text: verb === 'confirm-forget' ? 'forgetting…' : 'changing pin…' } }));
    const id = `conversation:${row.resume_id}`;
    const request = verb === 'confirm-forget' ? forget(id) : setKeep(id, verb === 'keep');
    void request.then(() => {
      setNotices((current) => ({ ...current, [key]: undefined }));
    }).catch((failure: Error) => {
      setNotices((current) => ({ ...current, [key]: { kind: 'refused', text: failure.message } }));
    });
  }, [rows, notices, confirmForget, forget, setKeep]);

  const items: ListItem[] = rows.map((row) => {
    const key = keyFor(row);
    return { kind: 'row', row: {
      key, title: row.title || row.resume_id, titleHint: row.resume_id,
      glyph: row.kept?.deleted_at ? 'removed' : pinned(row) ? 'pinned' : row.kept?.delete_after ? 'scheduled' : 'clean',
      meta: [row.agent, row.kept?.deleted_at ? 'Deleted copy' : row.kept ? amount(row.kept.bytes) : 'Pending copy', retention(row)],
      attrs: { verbs: JSON.stringify(verbs(row, confirmForget === key).map((verb) => verb.label)) },
      verbs: verbs(row, confirmForget === key), note: notices[key], dim: Boolean(row.kept?.deleted_at), yank: row.resume_id,
    } };
  });

  const confirming = selected && confirmForget === keyFor(selected);
  const note = selected ? notices[keyFor(selected)] : undefined;
  return <>
    {statusTarget && createPortal(<ConversationTotals result={result} error={error} copied={copied} />, statusTarget)}
    <div className="ledger-toolbar">
      <label className="ledger-deleted-toggle"><input type="checkbox" checked={includeDeleted} onChange={(event) => {
        setIncludeDeleted(event.target.checked); setConfirmForget(null);
      }} /> Show deleted</label>
      <QueryBar value={text} onChange={(value) => { setText(value); setConfirmForget(null); }} placeholder="title  seed  pinned  pending  words" chips={[]} inputRef={queryRef} />
    </div>
    <div className="ledger-split">
      <LedgerList items={items} selectedKey={selected ? keyFor(selected) : null} onSelect={(key) => {
        setSelectedKey(key); if (key !== confirmForget) setConfirmForget(null);
      }} onVerb={runVerb} menu={menu} onMenu={setMenu} onYank={copy}
      empty={<p className="ledger-empty">{error || (!result ? 'Reading kept conversations…' : 'No conversations match this view.')}</p>} />
      <ConversationInspector row={selected} confirming={Boolean(confirming)} note={note} onCopy={copy} onOpenSeed={onOpenSeed}
        onVerb={(verb) => { if (selected) runVerb(keyFor(selected), verb); }} />
    </div>
  </>;
}

function ConversationInspector({ row, confirming, note, onCopy, onOpenSeed, onVerb }: {
  row: KeptConversationRow | null;
  confirming: boolean;
  note?: RowNote;
  onCopy: (text: string) => void;
  onOpenSeed: (id: string) => void;
  onVerb: (verb: string) => void;
}) {
  if (!row) return <Inspector title="Nothing selected"><p className="ledger-muted">Pick a conversation to read it here.</p></Inspector>;
  return <Inspector title={row.title || row.resume_id} kicker={<span>{row.agent} conversation</span>}>
        <Field label="Retention">{retention(row)}</Field>
        <Field label="attn’s copy">{row.kept?.deleted_at ? 'Deleted' : row.kept ? amount(row.kept.bytes) : 'Pending copy'}
          {!row.kept && <div className="ledger-muted">{row.pending_reason}</div>}
        </Field>
        {row.kept && <Field label="Last copied">{fullStamp(row.kept.copied_at)}</Field>}
        {row.source_bytes !== undefined && <Field label="Source at last copy">{amount(row.source_bytes)}</Field>}
        <Field label="Conversation" mono><button type="button" className="ledger-copy" onClick={() => onCopy(row.resume_id)}>{row.resume_id}</button></Field>
        {row.seeds.length > 0 && <Field label="Open seeds">{row.seeds.map((seed) => <div key={seed.id}>
          <button type="button" className="ledger-link" onClick={() => onOpenSeed(seed.id)}>{seed.slug || seed.title}</button>
        </div>)}<div className="ledger-muted">Harvest or wither these seeds before forgetting the copy.</div></Field>}
        {confirming && <p className="ledger-verdict-text">Delete attn’s copy of {row.title || row.resume_id} ({amount(row.kept?.bytes ?? 0)})? Claude’s own files are not touched.</p>}
        {note && note.kind !== 'busy' && <p className="ledger-row-note is-refused" role="status">{note.text}</p>}
        <div className="ledger-verdict-actions">{verbs(row, confirming).map((verb) => <button key={verb.id} type="button"
          className={`ledger-verb${verb.danger ? ' is-danger' : ''}`} disabled={note?.kind === 'busy'} onClick={() => onVerb(verb.id)}>{verb.label}</button>)}</div>
      </Inspector>;
}

function ConversationTotals({ result, error, copied }: {
  result: KeptConversationListResult | null;
  error: string | null;
  copied: string | null;
}) {
  return <>
    {result && <span>{result.count} kept · {amount(result.stored_bytes)}</span>}
    {!!result?.pending_count && <span>{result.pending_count} pending</span>}
    {result?.next_delete_after && <span>next deletion {date(result.next_delete_after)}</span>}
    {error && <span className="ledger-status-error">{error}</span>}
    {copied && <span className="ledger-status-flash">copied</span>}
  </>;
}
