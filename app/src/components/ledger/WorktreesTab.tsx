import { useCallback, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import type {
  GitOperation,
  Worktree,
  WorktreeListResult,
  WorktreeRepository,
  WorktreeSweepEntry,
  WorktreeSweepLogResult,
} from '../../types/generated';
import { useWorktreeStore } from '../../store/worktrees';
import { fullStamp, nameIds, relativeStamp, tildePath } from './ledgerTime';
import { baseName, matchesWords } from './ledgerQuery';
import { Field, Inspector, LedgerList, QueryBar, Segmented, useCopied } from './LedgerPrimitives';
import type { Chip, ListItem, RowGlyph, RowModel, RowNote, RowVerb } from './LedgerPrimitives';

export interface WorktreeSessionRef {
  id: string;
  label: string;
  directory: string;
}

export function sessionsInWorktree(sessions: WorktreeSessionRef[], path: string): WorktreeSessionRef[] {
  return sessions.filter((session) => session.directory === path || session.directory.startsWith(`${path}/`));
}

export interface WorktreesTabProps {
  listWorktrees: () => Promise<WorktreeListResult>;
  getSweepLog: (mainRepo?: string, limit?: number) => Promise<WorktreeSweepLogResult>;
  setKeep: (path: string, keep: boolean) => Promise<Worktree>;
  refreshWorktrees: () => Promise<boolean>;
  deleteWorktree: (path: string, force: boolean) => Promise<void>;
  sessions: WorktreeSessionRef[];
  gitOperations: Record<string, GitOperation>;
  onSelectSession: (sessionId: string) => void;
  onShowSessions: (path: string) => void;
  requestedPath?: { path: string; nonce: number } | null;
  queryRef: React.RefObject<HTMLInputElement | null>;
  now: () => Date;
  onStatus: (status: ReactNode) => void;
}

type WorktreeScope = 'active' | 'removed';

const SCOPES: { id: WorktreeScope; label: string }[] = [
  { id: 'active', label: 'Active' },
  { id: 'removed', label: 'Removed' },
];

const sweepLogPageSize = 100;

export function WorktreesTab({
  listWorktrees,
  getSweepLog,
  setKeep,
  refreshWorktrees,
  deleteWorktree,
  sessions,
  gitOperations,
  onSelectSession,
  onShowSessions,
  requestedPath,
  queryRef,
  now,
  onStatus,
}: WorktreesTabProps) {
  const worktrees = useWorktreeStore((store) => store.worktrees);
  const repositories = useWorktreeStore((store) => store.repositories);
  const sweepLog = useWorktreeStore((store) => store.sweepLog);
  const loaded = useWorktreeStore((store) => store.loaded);

  const [scope, setScope] = useState<WorktreeScope>('active');
  const sessionTitle = useCallback((id: string) => sessions.find((session) => session.id === id)?.label || undefined, [sessions]);
  const [text, setText] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [notices, setNotices] = useState<Record<string, RowNote>>({});
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [menuKey, setMenuKey] = useState<string | null>(null);
  const [refreshedAt, setRefreshedAt] = useState<Date | null>(null);
  const [copied, copy] = useCopied();

  const load = useCallback(() => {
    listWorktrees()
      .then((result) => {
        useWorktreeStore.getState().replace(result.worktrees as Worktree[], result.repositories as WorktreeRepository[]);
        setError(null);
      })
      .catch((failure: Error) => setError(failure.message));
  }, [listWorktrees]);

  useEffect(() => { load(); }, [load]);

  useEffect(() => {
    if (scope !== 'removed') return;
    getSweepLog(undefined, sweepLogPageSize)
      .then((result) => useWorktreeStore.getState().replaceSweepLog(result.entries as WorktreeSweepEntry[]))
      .catch((failure: Error) => setError(failure.message));
  }, [scope, getSweepLog]);

  useEffect(() => {
    if (!requestedPath) return;
    setScope('active');
    setText('');
    setSelectedKey(requestedPath.path);
  }, [requestedPath]);

  const integrationByRepo = useMemo(() => {
    const map = new Map<string, string>();
    for (const repository of repositories) {
      if (repository.integration_branch) map.set(repository.main_repo, repository.integration_branch);
    }
    return map;
  }, [repositories]);

  const parsed = useMemo(() => {
    const words: string[] = [];
    let repo = '';
    for (const token of text.trim().split(/\s+/).filter(Boolean)) {
      if (token.toLowerCase().startsWith('repo:')) repo = token.slice(5).toLowerCase();
      else words.push(token.toLowerCase());
    }
    return { repo, words };
  }, [text]);

  const repoMatches = useCallback((mainRepo: string) =>
    !parsed.repo || baseName(mainRepo).toLowerCase() === parsed.repo || mainRepo.toLowerCase() === parsed.repo,
  [parsed.repo]);

  const setNotice = useCallback((path: string, note: RowNote | null) => {
    setNotices((current) => {
      if (!note) {
        if (!(path in current)) return current;
        const next = { ...current };
        delete next[path];
        return next;
      }
      return { ...current, [path]: note };
    });
  }, []);

  const withBusy = useCallback(async (path: string, label: string, work: () => Promise<void>) => {
    setNotice(path, { kind: 'busy', text: label });
    try {
      await work();
      setNotice(path, null);
      setError(null);
    } catch (failure) {
      setNotice(path, { kind: 'refused', text: failure instanceof Error ? failure.message : String(failure) });
    }
  }, [setNotice]);

  const requestRefresh = useCallback(() => {
    refreshWorktrees()
      .then(() => { setError(null); setRefreshedAt(now()); })
      .catch((failure: Error) => setError(failure.message));
  }, [refreshWorktrees, now]);

  const activeRows = useMemo(() => worktrees.filter((worktree) => repoMatches(worktree.main_repo) && matchesWords(
    [worktree.path, worktree.branch, worktree.sweep_status ?? '', worktree.merged_signal ?? '', worktree.pinned ? 'kept pinned' : ''],
    parsed.words,
  )), [worktrees, repoMatches, parsed.words]);

  const removedRows = useMemo(() => sweepLog.filter((entry) => repoMatches(entry.main_repo)
    && matchesWords([entry.path, entry.branch ?? '', entry.action, entry.reason ?? ''], parsed.words)),
  [sweepLog, repoMatches, parsed.words]);

  const items = useMemo<ListItem[]>(() => {
    const out: ListItem[] = [];
    const byRepo = new Map<string, (Worktree | WorktreeSweepEntry)[]>();
    const source: (Worktree | WorktreeSweepEntry)[] = scope === 'active' ? activeRows : removedRows;
    for (const row of source) {
      const rows = byRepo.get(row.main_repo) ?? [];
      rows.push(row);
      byRepo.set(row.main_repo, rows);
    }
    for (const [mainRepo, rows] of [...byRepo.entries()].sort((a, b) => a[0].localeCompare(b[0]))) {
      out.push({
        kind: 'group',
        key: mainRepo,
        title: baseName(mainRepo),
        meta: (
          <>
            {integrationByRepo.has(mainRepo) && <span>merges into <span className="is-mono">{integrationByRepo.get(mainRepo)}</span></span>}
            {runningOperationFor(gitOperations, mainRepo) && <span className="ledger-checking">refreshing…</span>}
          </>
        ),
      });
      const sorted = rows.slice().sort((a, b) => scope === 'active'
        ? a.path.localeCompare(b.path)
        : String((b as WorktreeSweepEntry).at).localeCompare(String((a as WorktreeSweepEntry).at)));
      for (const row of sorted) {
        out.push({
          kind: 'row',
          row: scope === 'active'
            ? worktreeRow(row as Worktree, {
              live: sessionsInWorktree(sessions, row.path),
              refreshing: runningOperationFor(gitOperations, row.path),
              note: notices[row.path],
              confirming: confirmDelete === row.path,
              now: now(),
            })
            : sweptRow(row as WorktreeSweepEntry, now()),
        });
      }
    }
    return out;
  }, [scope, activeRows, removedRows, integrationByRepo, gitOperations, sessions, notices, confirmDelete, now]);

  const rowKeys = useMemo(() => items.filter((item) => item.kind === 'row').map((item) => (item as { row: RowModel }).row.key), [items]);
  const selected = selectedKey && rowKeys.includes(selectedKey) ? selectedKey : rowKeys[0] ?? null;
  const selectedWorktree = scope === 'active' ? worktrees.find((row) => row.path === selected) ?? null : null;
  const selectedSwept = scope === 'removed' ? sweepLog.find((row) => row.id === selected) ?? null : null;

  const runVerb = useCallback((key: string, verbId: string) => {
    setSelectedKey(key);
    setMenuKey(null);
    const worktree = worktrees.find((row) => row.path === key);
    if (verbId === 'sessions') { onShowSessions(key); return; }
    if (verbId.startsWith('session:')) { onSelectSession(verbId.slice(8)); return; }
    if (!worktree) return;
    if (verbId === 'keep' || verbId === 'unpin') {
      void withBusy(key, verbId === 'keep' ? 'keeping…' : 'releasing…', async () => {
        const updated = await setKeep(key, verbId === 'keep');
        useWorktreeStore.getState().observe(updated);
      });
    } else if (verbId === 'delete') {
      setConfirmDelete(key);
    } else if (verbId === 'cancel') {
      setConfirmDelete(null);
    } else if (verbId === 'confirm-delete') {
      setConfirmDelete(null);
      // The daemon's push drops the row and writes the entry; a guess here would be a second one.
      void withBusy(key, 'deleting…', () => deleteWorktree(key, Boolean(worktree.dirty)));
    }
  }, [worktrees, onShowSessions, onSelectSession, withBusy, setKeep, deleteWorktree]);

  // Counts, not arrays, drive the status line: a parent that rerenders on status must not loop it.
  const shown = activeRows.length;
  const dirty = activeRows.filter((row) => row.dirty).length;
  const scheduled = activeRows.filter((row) => row.sweep_status === 'scheduled' && !row.pinned).length;
  const kept = activeRows.filter((row) => row.pinned).length;
  useEffect(() => {
    onStatus(
      <>
        {scope === 'active'
          ? (
            <>
              <span>{shown} {shown === 1 ? 'worktree' : 'worktrees'}</span>
              {kept > 0 && <span>{kept} kept</span>}
              {dirty > 0 && <span>{dirty} dirty</span>}
              {scheduled > 0 && <span>{scheduled} scheduled</span>}
              {shown !== worktrees.length && <span>{worktrees.length - shown} hidden by the query</span>}
            </>
          )
          : <span>{removedRows.length} removed</span>}
        <button type="button" className="ledger-status-link" onClick={requestRefresh}>
          refresh{refreshedAt ? ` · asked ${relativeStamp(refreshedAt.toISOString(), now())}` : ''}
        </button>
        {error && <span className="ledger-status-error" title={error}>{error}</span>}
        {copied && <span className="ledger-status-flash">copied</span>}
      </>,
    );
  }, [scope, shown, dirty, scheduled, kept, worktrees.length, removedRows.length, requestRefresh, refreshedAt, error, copied, now, onStatus]);

  const chips = useMemo<Chip[]>(() => text.trim().split(/\s+/).filter((token) => token.includes(':'))
    .map((token) => ({ text: token, onRemove: () => setText(text.split(/\s+/).filter((part) => part && part !== token).join(' ')) })),
  [text]);

  const empty = !loaded
    ? 'Reading the registry…'
    : scope === 'removed'
      ? (sweepLog.length === 0 ? 'Nothing removed yet.' : 'Nothing removed matches the query.')
      : worktrees.length === 0
        ? 'No worktrees tracked yet. Refresh once a repository has one.'
        : 'Nothing matches the query.';

  return (
    <>
      <div className="ledger-toolbar">
        <Segmented value={scope} options={SCOPES} label="Which worktrees" onChange={(next) => { setScope(next); setSelectedKey(null); }} />
        <QueryBar
          value={text}
          onChange={setText}
          placeholder="repo:attn  branch  dirty  merged  words"
          chips={chips}
          inputRef={queryRef}
        />
      </div>
      <div className="ledger-split">
        <LedgerList
          items={items}
          selectedKey={selected}
          onSelect={setSelectedKey}
          onVerb={runVerb}
          menuKey={menuKey}
          onMenu={setMenuKey}
          onYank={copy}
          empty={<p className="ledger-empty">{empty}</p>}
        />
        {selectedWorktree && (
          <WorktreeInspector
            worktree={selectedWorktree}
            live={sessionsInWorktree(sessions, selectedWorktree.path)}
            note={notices[selectedWorktree.path]}
            refreshing={runningOperationFor(gitOperations, selectedWorktree.path)}
            integration={integrationByRepo.get(selectedWorktree.main_repo)}
            confirming={confirmDelete === selectedWorktree.path}
            now={now()}
            copied={copied}
            onCopy={copy}
            onVerb={(verbId) => runVerb(selectedWorktree.path, verbId)}
            sessionTitle={sessionTitle}
          />
        )}
        {selectedSwept && (
          <Inspector title={baseName(selectedSwept.path)} kicker={<><span className="ledger-glyph is-removed" aria-hidden="true" /><span>{selectedSwept.action}</span></>}>
            <Field label="Path" mono>{tildePath(selectedSwept.path)}</Field>
            <Field label="Repository" mono>{tildePath(selectedSwept.main_repo)}</Field>
            {selectedSwept.branch && <Field label="Branch" mono>{selectedSwept.branch}</Field>}
            <Field label="When">{fullStamp(selectedSwept.at)}</Field>
            {selectedSwept.reason && <Field label="Why">{selectedSwept.reason}</Field>}
          </Inspector>
        )}
        {!selectedWorktree && !selectedSwept && (
          <Inspector title="Nothing selected"><p className="ledger-muted">Pick a row to read it here.</p></Inspector>
        )}
      </div>
    </>
  );
}

function runningOperationFor(gitOperations: Record<string, GitOperation>, path: string): boolean {
  return Object.values(gitOperations).some((operation) => operation.status === 'running' && operation.path === path);
}

interface WorktreeRowContext {
  live: WorktreeSessionRef[];
  refreshing: boolean;
  note: RowNote | undefined;
  confirming: boolean;
  now: Date;
}

function worktreeGlyph(worktree: Worktree, refreshing: boolean): RowGlyph {
  if (refreshing) return 'refreshing';
  if (worktree.refresh_error || worktree.prunable) return 'error';
  if (worktree.pinned) return 'pinned';
  if (worktree.sweep_status === 'scheduled') return 'scheduled';
  if (worktree.dirty || worktree.unpushed || worktree.stashes) return 'dirty';
  return 'clean';
}

function stateWords(worktree: Worktree): ReactNode[] {
  const out: ReactNode[] = [];
  if (worktree.prunable) out.push(<span className="is-no" key="stale">stale</span>);
  if (worktree.dirty) out.push(<span className="is-warn" key="dirty">dirty {worktree.dirty_files ?? 0}</span>);
  if (worktree.stashes) out.push(<span key="stash">{worktree.stashes} stashed</span>);
  if (worktree.unpushed) out.push(<span className="is-warn" key="ahead">{worktree.unpushed} ahead</span>);
  if (worktree.merged_signal) out.push(<span className="is-ok" key="merged">merged · {worktree.merged_signal}</span>);
  if (worktree.refresh_error) out.push(<span className="is-no" key="err" title={worktree.refresh_error}>{worktree.refresh_error}</span>);
  return out;
}

function sweepWord(worktree: Worktree, now: Date): ReactNode {
  if (worktree.pinned) return <span className="is-ok">kept</span>;
  const status = worktree.sweep_status ?? '';
  if (!status) return <span className="ledger-muted">not decided</span>;
  if (status !== 'scheduled') return status.replace(/_/g, ' ');
  if (!worktree.sweep_at) return 'scheduled';
  return <span className="is-warn" title={fullStamp(worktree.sweep_at)}>removing in {relativeStamp(worktree.sweep_at, now).replace(/^-/, '')}</span>;
}

function worktreeRow(worktree: Worktree, context: WorktreeRowContext): RowModel {
  const verbs: RowVerb[] = context.confirming
    ? [
      { id: 'confirm-delete', label: worktree.dirty ? 'Delete, losing changes' : 'Delete for real', danger: true },
      { id: 'cancel', label: 'Cancel' },
    ]
    : [
      worktree.pinned ? { id: 'unpin', label: 'Unpin' } : { id: 'keep', label: 'Keep' },
      { id: 'sessions', label: 'Sessions here' },
      ...context.live.map((session) => ({ id: `session:${session.id}`, label: `Go to ${session.label || session.id}` })),
      { id: 'delete', label: 'Delete…', danger: true },
    ];
  const idle = worktree.last_activity_at ? relativeStamp(worktree.last_activity_at, context.now) : null;
  return {
    key: worktree.path,
    glyph: worktreeGlyph(worktree, context.refreshing),
    title: baseName(worktree.path),
    titleHint: worktree.path,
    meta: [
      <span className="is-mono" key="branch">{worktree.detached ? `detached ${(worktree.head_sha ?? '').slice(0, 8)}` : worktree.branch || '—'}</span>,
      ...stateWords(worktree),
      context.live.length > 0
        ? <span className="is-live" key="live">{context.live.length} live {context.live.length === 1 ? 'session' : 'sessions'}</span>
        : null,
      sweepWord(worktree, context.now),
      context.refreshing ? <span className="ledger-checking" key="refreshing">refreshing…</span> : null,
    ],
    stamp: idle ? { text: `idle ${idle}`, hint: fullStamp(worktree.last_activity_at!) } : undefined,
    note: context.note,
    verbs,
    dim: false,
    yank: worktree.path,
    attrs: {
      pinned: worktree.pinned ? 'true' : 'false',
      reason: worktree.sweep_reason ?? '',
      sweep: worktree.pinned ? 'kept' : worktree.sweep_status ?? '',
      verbs: verbs.map((verb) => verb.label).join('\u001f'),
    },
  };
}

function sweptRow(entry: WorktreeSweepEntry, now: Date): RowModel {
  return {
    key: entry.id,
    glyph: 'removed',
    title: baseName(entry.path),
    titleHint: entry.path,
    meta: [
      entry.branch ? <span className="is-mono" key="branch">{entry.branch}</span> : null,
      entry.action,
      entry.reason ?? null,
    ],
    stamp: { text: relativeStamp(entry.at, now), hint: fullStamp(entry.at) },
    verbs: [],
    dim: true,
    yank: entry.path,
    attrs: { path: entry.path, action: entry.action, reason: entry.reason ?? '' },
  };
}

interface WorktreeInspectorProps {
  worktree: Worktree;
  live: WorktreeSessionRef[];
  note: RowNote | undefined;
  refreshing: boolean;
  integration: string | undefined;
  confirming: boolean;
  now: Date;
  copied: string | null;
  onCopy: (text: string) => void;
  onVerb: (verbId: string) => void;
  sessionTitle: (id: string) => string | undefined;
}

function WorktreeInspector({
  worktree, live, note, refreshing, integration, confirming, now, copied, onCopy, onVerb, sessionTitle,
}: WorktreeInspectorProps) {
  const busy = note?.kind === 'busy';
  return (
    <Inspector
      title={baseName(worktree.path)}
      kicker={(
        <>
          <span className={`ledger-glyph is-${worktreeGlyph(worktree, refreshing)}`} aria-hidden="true" />
          <span>{sweepWord(worktree, now)}</span>
          {refreshing && <span className="ledger-checking">refreshing…</span>}
        </>
      )}
    >
      <Field label="Path" mono>
        <button type="button" className="ledger-copy" title="Copy the path (y)" onClick={() => onCopy(worktree.path)}>
          {tildePath(worktree.path)}{copied === worktree.path && <em> copied</em>}
        </button>
      </Field>
      <Field label="Repository" mono>
        {tildePath(worktree.main_repo)}
        {integration && <div className="ledger-muted">merges into {integration}</div>}
      </Field>
      <Field label="Branch" mono>
        {worktree.detached ? `detached at ${(worktree.head_sha ?? '').slice(0, 12)}` : worktree.branch || '—'}
        {worktree.head_sha && !worktree.detached && <div className="ledger-muted">{worktree.head_sha.slice(0, 12)}</div>}
      </Field>
      <Field label="State">
        {stateWords(worktree).length === 0 ? <span className="is-ok">clean</span> : (
          <div className="ledger-words">{stateWords(worktree)}</div>
        )}
        {worktree.observed_at
          ? <div className="ledger-muted">observed {relativeStamp(worktree.observed_at, now)}</div>
          : <div className="ledger-muted">not refreshed yet</div>}
      </Field>
      {worktree.last_activity_at && (
        <Field label="Last activity">{fullStamp(worktree.last_activity_at)} <span className="ledger-muted">({relativeStamp(worktree.last_activity_at, now)})</span></Field>
      )}
      <Field label="Sweep">
        {sweepWord(worktree, now)}
        {worktree.sweep_reason && <div className="ledger-muted">{nameIds(worktree.sweep_reason, sessionTitle)}</div>}
        {worktree.pinned && worktree.pinned_at && <div className="ledger-muted">kept since {fullStamp(worktree.pinned_at)}</div>}
      </Field>
      <Field label="Sessions">
        {live.length === 0 && <span className="ledger-muted">none live here</span>}
        {live.map((session) => (
          <div key={session.id}>
            <button type="button" className="ledger-link" onClick={() => onVerb(`session:${session.id}`)}>{session.label || session.id}</button>
          </div>
        ))}
        <div><button type="button" className="ledger-link" onClick={() => onVerb('sessions')}>every session that ran here →</button></div>
      </Field>
      {note && note.kind !== 'busy' && <div className={`ledger-row-note is-${note.kind}`} role="status">{note.text}</div>}
      <div className="ledger-verdict-actions">
        {confirming
          ? (
            <>
              <button type="button" className="ledger-verb is-danger" disabled={busy} onClick={() => onVerb('confirm-delete')}>
                <kbd>1</kbd>{worktree.dirty ? 'Delete, losing changes' : 'Delete for real'}
              </button>
              <button type="button" className="ledger-verb" onClick={() => onVerb('cancel')}><kbd>2</kbd>Cancel</button>
            </>
          )
          : (
            <>
              <button type="button" className="ledger-verb is-primary" disabled={busy} onClick={() => onVerb(worktree.pinned ? 'unpin' : 'keep')}>
                <kbd>⏎</kbd>{busy ? note?.text : worktree.pinned ? 'Unpin' : 'Keep'}
              </button>
              <button type="button" className="ledger-verb is-danger" disabled={busy} onClick={() => onVerb('delete')}>Delete…</button>
            </>
          )}
      </div>
    </Inspector>
  );
}
