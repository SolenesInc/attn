import { useCallback, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import type { SessionLedgerEntry, SessionReopen } from '../../types/generated';
import type { SessionLedgerPage, SessionLedgerQuery } from '../../hooks/daemonSessionLedgerEvents';
import { useSessionLedger } from '../../hooks/useSessionLedger';
import type { SessionLedgerFilters } from '../../hooks/useSessionLedger';
import {
  SESSION_FILTERS_SETTING_KEY,
  parseSessionFilters,
  serializeSessionFilters,
} from '../../hooks/sessionFiltersSetting';
import { useSettings } from '../../contexts/SettingsContext';
import {
  branchStateLabel,
  closedBySomeone,
  compactRefusalText,
  compactVerdictText,
  directoryStateLabel,
  isClosed,
  ledgerInstant,
  reopenPlacement,
} from '../sessionsLedger';
import type { ReopenVerdictView, SessionScope } from '../sessionsLedger';
import { fullStamp, nameIds, relativeStamp, shortPath, tildePath } from './ledgerTime';
import { formatQuery, matchesDir, matchesWords, parseQuery, removeToken } from './ledgerQuery';
import { Field, Inspector, LedgerList, QueryBar, Segmented, useCopied } from './LedgerPrimitives';
import type { Chip, ListItem, RowGlyph, RowModel, RowNote, RowVerb } from './LedgerPrimitives';

export interface SessionSeedLink {
  id: string;
  title: string;
}

export interface SessionsTabProps {
  listSessions: (query: SessionLedgerQuery) => Promise<SessionLedgerPage>;
  workspaceNames: Record<string, string>;
  liveSessionIds?: Set<string>;
  seedForSession?: (sessionId: string) => SessionSeedLink | null;
  onFocusSession?: (sessionId: string) => void;
  onOpenSeed?: (seedId: string) => void;
  onReopen?: (sessionId: string, actionId: string) => Promise<boolean | void> | boolean | void;
  onShowWorktree?: (path: string) => void;
  closeNotice?: { entry: SessionLedgerEntry; reopen?: SessionReopen; nonce: number };
  verdictNotice?: { verdicts: Record<string, SessionReopen>; nonce: number };
  requestedDir?: { path: string; nonce: number } | null;
  queryRef: React.RefObject<HTMLInputElement | null>;
  now: () => Date;
  onStatus: (status: ReactNode) => void;
}

const SCOPES: { id: SessionScope; label: string }[] = [
  { id: 'live', label: 'Live' },
  { id: 'closed', label: 'Closed' },
  { id: 'all', label: 'All' },
];

const WORKING_STATES = new Set(['working', 'running', 'busy']);
const WAITING_STATES = new Set(['waiting', 'attention', 'needs_attention', 'idle']);

export function SessionsTab({
  listSessions,
  workspaceNames,
  liveSessionIds,
  seedForSession,
  onFocusSession,
  onOpenSeed,
  onReopen,
  onShowWorktree,
  closeNotice,
  verdictNotice,
  requestedDir,
  queryRef,
  now,
  onStatus,
}: SessionsTabProps) {
  const { settings, setSetting } = useSettings();
  // Read at open, never again: a settings echo must not move filters under the user.
  const [restoredFilters] = useState(() => parseSessionFilters(settings[SESSION_FILTERS_SETTING_KEY]));
  const rememberFilters = useCallback((next: SessionLedgerFilters) => {
    setSetting(SESSION_FILTERS_SETTING_KEY, serializeSessionFilters(next));
  }, [setSetting]);
  const ledger = useSessionLedger({
    enabled: true,
    list: listSessions,
    now,
    initialFilters: restoredFilters,
    onFiltersChange: rememberFilters,
  });
  const { filters, setFilters, entries, verdicts, recordClose, recordVerdict, reload } = ledger;

  const workspaceLabel = useCallback((id: string) => workspaceNames[id] ?? id, [workspaceNames]);

  const [text, setText] = useState(() => formatQuery(restoredFilters, workspaceLabel));
  const parsed = useMemo(() => parseQuery(text, ledger.facets, workspaceLabel), [text, ledger.facets, workspaceLabel]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setFilters((current) => {
        const next = { ...current, ...parsed.filters };
        const same = next.range === current.range && next.customFrom === current.customFrom
          && next.customTo === current.customTo && next.workspaceId === current.workspaceId
          && next.repository === current.repository;
        return same ? current : next;
      });
    }, 150);
    return () => window.clearTimeout(timer);
  }, [parsed.filters, setFilters]);

  useEffect(() => {
    if (!requestedDir) return;
    setText(`dir:${requestedDir.path}`);
  }, [requestedDir]);

  useEffect(() => {
    if (!closeNotice) return;
    recordClose(closeNotice.entry, closeNotice.reopen);
  }, [closeNotice, recordClose]);

  useEffect(() => {
    if (!verdictNotice) return;
    for (const [sessionId, reopen] of Object.entries(verdictNotice.verdicts)) recordVerdict(sessionId, reopen);
  }, [verdictNotice, recordVerdict]);

  const visible = useMemo(() => entries.filter((entry) => {
    if (!matchesDir(entry.directory, parsed.dir)) return false;
    return matchesWords(
      [entry.label, entry.id, entry.agent, entry.branch ?? '', entry.directory, workspaceLabel(entry.workspace_id)],
      parsed.words,
    );
  }), [entries, parsed.dir, parsed.words, workspaceLabel]);

  const [selectedId, setSelectedId] = useState<string | null>(null);
  const selected = visible.find((entry) => entry.id === selectedId) ?? visible[0] ?? null;
  const [menuKey, setMenuKey] = useState<string | null>(null);
  const [notices, setNotices] = useState<Record<string, RowNote>>({});
  const [awaiting, setAwaiting] = useState<{ sessionId: string; actionId: string } | null>(null);
  const [copied, copy] = useCopied();

  const setNotice = useCallback((sessionId: string, note: RowNote | null) => {
    setNotices((current) => {
      if (!note) {
        if (!(sessionId in current)) return current;
        const next = { ...current };
        delete next[sessionId];
        return next;
      }
      return { ...current, [sessionId]: note };
    });
  }, []);

  const fire = useCallback((sessionId: string, actionId: string) => {
    if (!onReopen) return;
    setMenuKey(null);
    const refuse = (failure: unknown) => {
      const message = failure instanceof Error ? failure.message : String(failure);
      setNotice(sessionId, { kind: 'refused', text: compactRefusalText(message) });
      reload();
    };
    setNotice(sessionId, { kind: 'busy', text: 'reopening…' });
    let outcome: ReturnType<typeof onReopen>;
    try {
      outcome = onReopen(sessionId, actionId);
    } catch (failure) {
      refuse(failure);
      return;
    }
    Promise.resolve(outcome).then(() => setNotice(sessionId, null)).catch(refuse);
  }, [onReopen, setNotice, reload]);

  // Fires against the verdict that lands, never the stale one that was on screen.
  useEffect(() => {
    if (!awaiting) return;
    const verdict = verdicts[awaiting.sessionId];
    if (!verdict || verdict.refreshing) return;
    if (verdict.actions.some((action) => action.id === awaiting.actionId)) {
      fire(awaiting.sessionId, awaiting.actionId);
    } else {
      setNotice(awaiting.sessionId, { kind: 'refused', text: `The check finished and that is no longer possible: ${verdict.summary}` });
    }
    setAwaiting(null);
  }, [awaiting, verdicts, fire, setNotice]);

  const isLive = useCallback(
    (entry: SessionLedgerEntry) => !isClosed(entry) && (liveSessionIds?.has(entry.id) ?? true),
    [liveSessionIds],
  );

  const labelsBySession = useMemo(() => new Map(entries.map((entry) => [entry.id, entry.label])), [entries]);
  const sessionLabel = useCallback((id: string) => labelsBySession.get(id) || id, [labelsBySession]);
  const nameText = useCallback((text: string) => nameIds(text, (id) => labelsBySession.get(id) || undefined), [labelsBySession]);
  // Unnamed workspaces carry a generated id; that is noise on a row, so they show nothing.
  const workspaceShown = useCallback((id: string) => {
    const shown = workspaceNames[id] ?? id;
    return /[0-9a-f]{8}-[0-9a-f]{4}-/i.test(shown) ? null : shown;
  }, [workspaceNames]);

  const runVerb = useCallback((key: string, verbId: string) => {
    const entry = visible.find((row) => row.id === key);
    if (!entry) return;
    setSelectedId(entry.id);
    setMenuKey(null);
    if (verbId === 'focus') { onFocusSession?.(entry.id); return; }
    if (verbId === 'seed') { const seed = seedForSession?.(entry.id); if (seed) onOpenSeed?.(seed.id); return; }
    if (verbId === 'worktree') { onShowWorktree?.(entry.directory); return; }
    setNotice(entry.id, null);
    const verdict = verdicts[entry.id];
    if (verdict && !verdict.refreshing) { fire(entry.id, verdictId(verbId)); return; }
    setAwaiting({ sessionId: entry.id, actionId: verdictId(verbId) });
  }, [visible, onFocusSession, seedForSession, onOpenSeed, onShowWorktree, setNotice, verdicts, fire]);

  const items = useMemo<ListItem[]>(() => visible.map((entry) => ({
    kind: 'row',
    row: sessionRow(entry, {
      verdict: isClosed(entry) ? verdicts[entry.id] : undefined,
      note: notices[entry.id] ?? (awaiting?.sessionId === entry.id ? { kind: 'info', text: 'waiting for the branch check…' } : undefined),
      live: isLive(entry),
      seed: seedForSession?.(entry.id) ?? null,
      workspaceLabel: workspaceShown,
      sessionLabel,
      nameText,
      actionsAvailable: !!onReopen,
      canShowWorktree: !!onShowWorktree && !!entry.is_worktree && verdicts[entry.id]?.directoryState !== 'missing',
      now: now(),
    }),
  })), [visible, verdicts, notices, awaiting, isLive, seedForSession, workspaceShown, sessionLabel, nameText, onReopen, onShowWorktree, now]);

  // Counts, not arrays, drive the status line: a parent that rerenders on status must not loop it.
  const shown = visible.length;
  const live = visible.filter(isLive).length;
  useEffect(() => {
    const closed = shown - live;
    onStatus(
      <>
        <span>{shown} {shown === 1 ? 'session' : 'sessions'}</span>
        {live > 0 && <span>{live} live</span>}
        {closed > 0 && <span>{closed} closed</span>}
        {shown !== entries.length && <span>{entries.length - shown} hidden by the query</span>}
        {ledger.omitted > 0 && (
          <button type="button" className="ledger-status-link" onClick={ledger.loadMore} disabled={ledger.loadingMore}>
            {ledger.loadingMore ? 'loading…' : `${ledger.omitted} older ↓`}
          </button>
        )}
        {copied && <span className="ledger-status-flash">copied</span>}
      </>,
    );
  }, [shown, live, entries.length, ledger.omitted, ledger.loadMore, ledger.loadingMore, copied, onStatus]);

  const chips = useMemo<Chip[]>(() => {
    const tokens = text.trim().split(/\s+/).filter(Boolean);
    return tokens
      .filter((token) => token.includes(':') || token.toLowerCase() in RANGE_LOOKUP)
      .map((token) => ({
        text: token,
        tone: parsed.unresolved.includes(token) ? 'unresolved' as const : undefined,
        onRemove: () => setText(removeToken(text, token)),
      }));
  }, [text, parsed]);

  const emptyMessage = ledger.filterError
    ? ledger.filterError
    : ledger.error
      ? ledger.error
      : ledger.loading && entries.length === 0
        ? 'Reading the ledger…'
        : entries.length > 0
          ? 'Nothing on this page matches the query.'
          : filters.scope === 'closed'
            ? 'No closed sessions yet. Closing one records it here.'
            : filters.scope === 'live' ? 'No live sessions right now.' : 'The ledger is empty.';

  return (
    <>
      <div className="ledger-toolbar">
        <Segmented
          value={filters.scope}
          options={SCOPES}
          label="Which sessions"
          onChange={(scope) => setFilters((current) => ({ ...current, scope }))}
        />
        <QueryBar
          value={text}
          onChange={setText}
          placeholder="repo:attn  ws:name  7d  from:2026-09-01  dir:~/x  words"
          chips={chips}
          inputRef={queryRef}
        />
      </div>
      <div className="ledger-split">
        <LedgerList
          items={items}
          selectedKey={selected?.id ?? null}
          onSelect={setSelectedId}
          onVerb={runVerb}
          menuKey={menuKey}
          onMenu={setMenuKey}
          onYank={copy}
          empty={<p className={`ledger-empty${ledger.error || ledger.filterError ? ' is-error' : ''}`}>{emptyMessage}</p>}
        />
        {selected
          ? (
            <SessionInspector
              entry={selected}
              verdict={isClosed(selected) ? verdicts[selected.id] : undefined}
              note={notices[selected.id]}
              live={isLive(selected)}
              seed={seedForSession?.(selected.id) ?? null}
              workspaceLabel={workspaceLabel}
              workspaceShown={workspaceShown}
              sessionLabel={sessionLabel}
              nameText={nameText}
              now={now()}
              copied={copied}
              onCopy={copy}
              onVerb={(verbId) => runVerb(selected.id, verbId)}
              actionsAvailable={!!onReopen}
            />
          )
          : <Inspector title="Nothing selected"><p className="ledger-muted">Pick a row to read it here.</p></Inspector>}
      </div>
    </>
  );
}

const RANGE_LOOKUP: Record<string, true> = { today: true, yesterday: true, '7d': true, '30d': true, week: true, month: true };

function verdictId(verbId: string): string {
  return verbId.startsWith('act:') ? verbId.slice(4) : verbId;
}

interface RowContext {
  nameText: (text: string) => string;
  verdict: ReopenVerdictView | undefined;
  note: RowNote | undefined;
  live: boolean;
  seed: SessionSeedLink | null;
  workspaceLabel: (id: string) => string | null;
  sessionLabel: (id: string) => string;
  actionsAvailable: boolean;
  canShowWorktree: boolean;
  now: Date;
}

function sessionRow(entry: SessionLedgerEntry, context: RowContext): RowModel {
  const closed = isClosed(entry);
  const { verdict } = context;
  const verbs: RowVerb[] = [];
  if (context.live) verbs.push({ id: 'focus', label: 'Focus' });
  if (closed && context.actionsAvailable && verdict) {
    for (const action of verdict.actions) verbs.push({ id: `act:${action.id}`, label: action.label });
  }
  if (context.seed) verbs.push({ id: 'seed', label: `Seed · ${context.seed.title}` });
  if (context.canShowWorktree) verbs.push({ id: 'worktree', label: 'Show worktree' });

  const meta: ReactNode[] = [
    entry.agent,
    context.workspaceLabel(entry.workspace_id) || null,
    <span className="is-mono is-path" title={entry.directory} key="dir">{shortPath(entry.directory)}</span>,
    entry.branch ? <span className="is-mono" key="branch">{entry.branch}</span> : null,
  ];
  if (closed) {
    meta.push(`closed by ${closedBySomeone(entry, context.sessionLabel)}${entry.close_reason ? `: ${context.nameText(entry.close_reason)}` : ''}`);
  }
  // A verdict with actions speaks through its verb; only a dead end needs words on the row.
  if (closed && verdict && verdict.actions.length === 0) {
    meta.push(<span className="is-no" title={verdict.summary} key="verdict">{context.nameText(compactVerdictText(verdict.summary))}</span>);
  }

  const stampAt = ledgerInstant(entry);
  return {
    key: entry.id,
    glyph: sessionGlyph(entry, context.live, verdict),
    title: entry.label || 'untitled session',
    meta,
    stamp: { text: relativeStamp(stampAt, context.now), hint: fullStamp(stampAt) },
    note: context.note,
    verbs,
    dim: closed,
    yank: entry.directory,
    attrs: { state: closed ? 'closed' : entry.state, verbs: verbs.map((verb) => verb.label).join('\u001f') },
  };
}

function sessionGlyph(entry: SessionLedgerEntry, live: boolean, verdict: ReopenVerdictView | undefined): RowGlyph {
  if (isClosed(entry)) return verdict?.refreshing ? 'refreshing' : 'closed';
  if (!live) return 'closed';
  if (WORKING_STATES.has(entry.state)) return 'working';
  if (WAITING_STATES.has(entry.state)) return 'waiting';
  return 'live';
}

interface SessionInspectorProps {
  entry: SessionLedgerEntry;
  verdict: ReopenVerdictView | undefined;
  note: RowNote | undefined;
  live: boolean;
  seed: SessionSeedLink | null;
  workspaceLabel: (id: string) => string;
  workspaceShown: (id: string) => string | null;
  sessionLabel: (id: string) => string;
  nameText: (text: string) => string;
  now: Date;
  copied: string | null;
  onCopy: (text: string) => void;
  onVerb: (verbId: string) => void;
  actionsAvailable: boolean;
}

function SessionInspector({
  entry, verdict, note, live, seed, workspaceLabel, workspaceShown, sessionLabel, nameText, now, copied, onCopy, onVerb, actionsAvailable,
}: SessionInspectorProps) {
  const closed = isClosed(entry);
  const busy = note?.kind === 'busy';
  return (
    <Inspector
      title={entry.label || 'untitled session'}
      kicker={(
        <>
          <span className={`ledger-glyph is-${sessionGlyph(entry, live, verdict)}`} aria-hidden="true" />
          <span>{closed ? 'closed' : entry.state}</span>
          <span>·</span>
          <span>{entry.agent}</span>
        </>
      )}
    >
      <Field label="Workspace">{workspaceShown(entry.workspace_id) || '—'}</Field>
      <Field label="Directory" mono>
        <button type="button" className="ledger-copy" title="Copy the path (y)" onClick={() => onCopy(entry.directory)}>
          {tildePath(entry.directory)}{copied === entry.directory && <em> copied</em>}
        </button>
        {verdict && <div className="ledger-muted">{directoryStateLabel(verdict.directoryState)}</div>}
      </Field>
      {(entry.branch || verdict?.branchState) && (
        <Field label="Branch" mono>
          {entry.branch || '—'}
          {branchStateLabel(verdict?.branchState) && <div className="ledger-muted">{branchStateLabel(verdict?.branchState)}</div>}
        </Field>
      )}
      <Field label={closed ? 'Closed' : 'Last seen'}>
        {fullStamp(ledgerInstant(entry))} <span className="ledger-muted">({relativeStamp(ledgerInstant(entry), now)})</span>
        {closed && (
          <div className="ledger-muted">
            by {closedBySomeone(entry, sessionLabel)}{entry.close_reason ? `: ${nameText(entry.close_reason)}` : ''}
          </div>
        )}
      </Field>
      {seed && (
        <Field label="Seed">
          <button type="button" className="ledger-link" onClick={() => onVerb('seed')}>{seed.title}</button>
        </Field>
      )}
      {closed && (
        <div className={`ledger-verdict${verdict ? (verdict.reopenable ? ' is-ok' : ' is-no') : ''}`}>
          <div className="ledger-field-label">Reopen</div>
          {!verdict && <div className="ledger-muted">No verdict yet.</div>}
          {verdict && (
            <>
              <div className="ledger-verdict-text" title={verdict.reason ?? verdict.summary}>
                {nameText(compactVerdictText(verdict.reason ?? 'It can be reopened where it ran.'))}
                {verdict.refreshing && <em className="ledger-checking"> checking the branch…</em>}
              </div>
              {verdict.warning && <div className="ledger-muted" title={verdict.warning}>{nameText(compactVerdictText(verdict.warning))}</div>}
              <div className="ledger-muted">{reopenPlacement(verdict, workspaceLabel)}</div>
              {note && note.kind !== 'busy' && (
                <div className={`ledger-row-note is-${note.kind}`} role="status">{note.text}</div>
              )}
              {actionsAvailable && (
                <div className="ledger-verdict-actions">
                  {verdict.actions.map((action, index) => (
                    <button
                      key={action.id}
                      type="button"
                      className={index === 0 ? 'ledger-verb is-primary' : 'ledger-verb'}
                      disabled={busy}
                      onClick={() => onVerb(`act:${action.id}`)}
                    >
                      <kbd>{index === 0 ? '⏎' : index + 1}</kbd>{busy && index === 0 ? note?.text : action.label}
                    </button>
                  ))}
                  {verdict.actions.length === 0 && <span className="ledger-muted">Nothing here brings it back.</span>}
                </div>
              )}
            </>
          )}
        </div>
      )}
      {live && (
        <div className="ledger-verdict-actions">
          <button type="button" className="ledger-verb is-primary" onClick={() => onVerb('focus')}>
            <kbd>⏎</kbd>Focus
          </button>
        </div>
      )}
    </Inspector>
  );
}
