import { useCallback, useEffect, useMemo, useState } from 'react';
import type { Dispatch, ReactNode, SetStateAction } from 'react';
import type { SessionLedgerEntry, SessionLedgerFacets, SessionReopen } from '../../types/generated';
import type { SessionLedgerPage, SessionLedgerQuery } from '../../hooks/daemonSessionLedgerEvents';
import { useSessionLedger } from '../../hooks/useSessionLedger';
import type { SessionLedgerFilters, SessionLedgerView } from '../../hooks/useSessionLedger';
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
import type { ParsedQuery } from './ledgerQuery';
import { Field, Inspector, LedgerList, QueryBar, Segmented, useCopied } from './LedgerPrimitives';
import type { Chip, ListItem, RowGlyph, RowModel, RowNote, RowVerb } from './LedgerPrimitives';

export interface SessionSeedLink {
  id: string;
  title: string;
}

export interface SessionsTabProps {
  listSessions: (query: SessionLedgerQuery) => Promise<SessionLedgerPage>;
  profileNames: Record<string, string>;
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
  profileNames,
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

  const { text, setText, parsed } = useLedgerQueryText({
    restoredFilters, profileNames, facets: ledger.facets, repository: filters.repository, setFilters, requestedDir,
  });

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
      [entry.label, entry.id, entry.agent, entry.branch ?? '', entry.directory, entry.profile_name],
      parsed.words,
    );
  }), [entries, parsed.dir, parsed.words]);

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
      sessionLabel,
      nameText,
      actionsAvailable: !!onReopen,
      canShowWorktree: !!onShowWorktree && !!entry.is_worktree && verdicts[entry.id]?.directoryState !== 'missing',
      now: now(),
    }),
  })), [visible, verdicts, notices, awaiting, isLive, seedForSession, sessionLabel, nameText, onReopen, onShowWorktree, now]);

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

  const emptyMessage = ledgerEmptyMessage(ledger, filters.scope);

  return (
    <>
      <div
        className="ledger-toolbar"
        data-range={filters.range}
        data-repository={filters.repository}
        data-profile={filters.profileId}
      >
        <Segmented
          value={filters.scope}
          options={SCOPES}
          label="Which sessions"
          onChange={(scope) => setFilters((current) => ({ ...current, scope }))}
        />
        <QueryBar
          value={text}
          onChange={setText}
          placeholder="repo:attn  profile:name  7d  from:2026-09-01  dir:~/x  words"
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

interface LedgerQueryTextOptions {
  restoredFilters: SessionLedgerFilters;
  profileNames: Record<string, string>;
  facets: SessionLedgerFacets | null;
  repository: string;
  setFilters: Dispatch<SetStateAction<SessionLedgerFilters>>;
  requestedDir?: { path: string; nonce: number } | null;
}

function unresolvedWhilePending(parsed: ParsedQuery, facets: SessionLedgerFacets | null, prefix: string): boolean {
  return facets === null && parsed.unresolved.some((token) => token.toLowerCase().startsWith(prefix));
}

function sameQueryFilters(a: SessionLedgerFilters, b: SessionLedgerFilters): boolean {
  return a.range === b.range && a.customFrom === b.customFrom && a.customTo === b.customTo
    && a.profileId === b.profileId && a.repository === b.repository;
}

function useLedgerQueryText({ restoredFilters, profileNames, facets, repository, setFilters, requestedDir }: LedgerQueryTextOptions) {
  const profileLabel = useCallback((id: string) => profileNames[id] ?? id, [profileNames]);
  const [text, setText] = useState(() => formatQuery(restoredFilters, profileLabel));
  const parsed = useMemo(
    () => parseQuery(text, facets, profileLabel, repository),
    [text, facets, profileLabel, repository],
  );
  const keepRepository = unresolvedWhilePending(parsed, facets, 'repo:');
  const keepProfile = unresolvedWhilePending(parsed, facets, 'profile:');

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setFilters((current) => {
        const next = {
          ...current,
          ...parsed.filters,
          repository: keepRepository ? current.repository : parsed.filters.repository,
          profileId: keepProfile ? current.profileId : parsed.filters.profileId,
        };
        return sameQueryFilters(next, current) ? current : next;
      });
    }, 150);
    return () => window.clearTimeout(timer);
  }, [parsed.filters, setFilters, keepRepository, keepProfile]);

  useEffect(() => {
    if (!requestedDir) return;
    setText(`dir:${requestedDir.path}`);
  }, [requestedDir]);

  return { text, setText, parsed };
}

function ledgerEmptyMessage(ledger: SessionLedgerView, scope: SessionScope): string {
  if (ledger.filterError) return ledger.filterError;
  if (ledger.error) return ledger.error;
  if (ledger.loading && ledger.entries.length === 0) return 'Reading the ledger…';
  if (ledger.entries.length > 0) return 'Nothing on this page matches the query.';
  if (scope === 'closed') return 'No closed sessions yet. Closing one records it here.';
  if (scope === 'live') return 'No live sessions right now.';
  return 'The ledger is empty.';
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
    profileText(entry) || null,
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

function profileText(entry: SessionLedgerEntry): string {
  if (!entry.profile_name) return '';
  return entry.profile_deleted ? `${entry.profile_name} (deleted)` : entry.profile_name;
}

function sessionGlyph(entry: SessionLedgerEntry, live: boolean, verdict: ReopenVerdictView | undefined): RowGlyph {
  if (isClosed(entry)) return verdict?.refreshing ? 'refreshing' : 'closed';
  if (!live) return 'closed';
  if (WORKING_STATES.has(entry.state)) return 'working';
  if (WAITING_STATES.has(entry.state)) return 'waiting';
  return 'live';
}

interface ReopenVerdictProps {
  verdict: ReopenVerdictView | undefined;
  note: RowNote | undefined;
  nameText: (text: string) => string;
  onVerb: (verbId: string) => void;
  actionsAvailable: boolean;
}

function ReopenVerdict({ verdict, note, nameText, onVerb, actionsAvailable }: ReopenVerdictProps) {
  const busy = note?.kind === 'busy';
  return (
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
          <div className="ledger-muted">{reopenPlacement(verdict)}</div>
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
  );
}

interface SessionInspectorProps {
  entry: SessionLedgerEntry;
  verdict: ReopenVerdictView | undefined;
  note: RowNote | undefined;
  live: boolean;
  seed: SessionSeedLink | null;
  sessionLabel: (id: string) => string;
  nameText: (text: string) => string;
  now: Date;
  copied: string | null;
  onCopy: (text: string) => void;
  onVerb: (verbId: string) => void;
  actionsAvailable: boolean;
}

function SessionKicker({ entry, live, verdict }: { entry: SessionLedgerEntry; live: boolean; verdict: ReopenVerdictView | undefined }) {
  return (
    <>
      <span className={`ledger-glyph is-${sessionGlyph(entry, live, verdict)}`} aria-hidden="true" />
      <span>{isClosed(entry) ? 'closed' : entry.state}</span>
      <span>·</span>
      <span>{entry.agent}</span>
    </>
  );
}

function DirectoryField({ entry, verdict, copied, onCopy }: {
  entry: SessionLedgerEntry; verdict: ReopenVerdictView | undefined; copied: string | null; onCopy: (text: string) => void;
}) {
  return (
    <Field label="Directory" mono>
      <button type="button" className="ledger-copy" title="Copy the path (y)" onClick={() => onCopy(entry.directory)}>
        {tildePath(entry.directory)}{copied === entry.directory && <em> copied</em>}
      </button>
      {verdict && <div className="ledger-muted">{directoryStateLabel(verdict.directoryState)}</div>}
    </Field>
  );
}

function BranchField({ entry, verdict }: { entry: SessionLedgerEntry; verdict: ReopenVerdictView | undefined }) {
  const state = branchStateLabel(verdict?.branchState);
  if (!entry.branch && !verdict?.branchState) return null;
  return (
    <Field label="Branch" mono>
      {entry.branch || '—'}
      {state && <div className="ledger-muted">{state}</div>}
    </Field>
  );
}

function InstantField({ entry, now, sessionLabel, nameText }: {
  entry: SessionLedgerEntry; now: Date; sessionLabel: (id: string) => string; nameText: (text: string) => string;
}) {
  const closed = isClosed(entry);
  return (
    <Field label={closed ? 'Closed' : 'Last seen'}>
      {fullStamp(ledgerInstant(entry))} <span className="ledger-muted">({relativeStamp(ledgerInstant(entry), now)})</span>
      {closed && (
        <div className="ledger-muted">
          by {closedBySomeone(entry, sessionLabel)}{entry.close_reason ? `: ${nameText(entry.close_reason)}` : ''}
        </div>
      )}
    </Field>
  );
}

function SessionInspector({
  entry, verdict, note, live, seed, sessionLabel, nameText, now, copied, onCopy, onVerb, actionsAvailable,
}: SessionInspectorProps) {
  return (
    <Inspector title={entry.label || 'untitled session'} kicker={<SessionKicker entry={entry} live={live} verdict={verdict} />}>
      <Field label="Profile">{profileText(entry) || '—'}</Field>
      <DirectoryField entry={entry} verdict={verdict} copied={copied} onCopy={onCopy} />
      <BranchField entry={entry} verdict={verdict} />
      <InstantField entry={entry} now={now} sessionLabel={sessionLabel} nameText={nameText} />
      {seed && (
        <Field label="Seed">
          <button type="button" className="ledger-link" onClick={() => onVerb('seed')}>{seed.title}</button>
        </Field>
      )}
      {isClosed(entry) && (
        <ReopenVerdict verdict={verdict} note={note} nameText={nameText} onVerb={onVerb} actionsAvailable={actionsAvailable} />
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
