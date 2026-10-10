import { useDaemonStore } from '../../store/daemonSessions';
import { SessionPriority } from '../SessionPriority';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, ReactNode, SetStateAction } from 'react';
import type { SessionLedgerEntry, SessionLedgerFacets, SessionUsage } from '../../types/generated';
import { useSessionLedger } from '../../hooks/useSessionLedger';
import type { SessionLedgerConnection, SessionLedgerFilters, SessionLedgerView } from '../../hooks/useSessionLedger';
import { SessionReopenRefusal } from '../../hooks/daemonSessionLedgerEvents';
import {
  SESSION_FILTERS_SETTING_KEY,
  parseSessionFilters,
  serializeSessionFilters,
} from '../../hooks/sessionFiltersSetting';
import { useSettings } from '../../contexts/SettingsContext';
import {
  branchStateLabel,
  closedBySomeone,
  compactVerdictText,
  directoryStateLabel,
  isClosed,
  ledgerInstant,
  refusalNote,
  reopenPlacement,
} from '../sessionsLedger';
import type { ReopenActionView, ReopenVerdictView, SessionScope } from '../sessionsLedger';
import { fullStamp, nameIds, relativeStamp, shortPath, tildePath } from './ledgerTime';
import { formatQuery, matchesDir, matchesWords, parseQuery, profileChoices, removeToken, renameProfileTokens } from './ledgerQuery';
import type { ParsedQuery, ProfileChoice } from './ledgerQuery';
import { Field, Inspector, LedgerList, QueryBar, Segmented, useCopied } from './LedgerPrimitives';
import type { Chip, LedgerMenu, ListItem, RowGlyph, RowModel, RowNote, RowVerb } from './LedgerPrimitives';
import { HeaderSessionUsage } from '../SessionTerminalDesktop/SessionUsage';

export interface SessionSeedLink {
  id: string;
  title: string;
}

export interface SessionsTabProps {
  connection: SessionLedgerConnection;
  profileNames: Record<string, string>;
  profileMembership: string;
  liveSessionIds?: Set<string>;
  liveSessionUsage?: ReadonlyMap<string, SessionUsage>;
  seedForSession?: (sessionId: string) => SessionSeedLink | null;
  onFocusSession?: (sessionId: string) => void;
  onOpenSeed?: (seedId: string) => void;
  onReopen?: (sessionId: string, actionId: string) => Promise<boolean | void> | boolean | void;
  setConversationKeep?: (sessionId: string, keep: boolean) => Promise<boolean>;
  conversationChangeSignal?: number;
  onShowWorktree?: (path: string) => void;
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
  connection,
  profileNames,
  profileMembership,
  liveSessionIds,
  liveSessionUsage,
  seedForSession,
  onFocusSession,
  onOpenSeed,
  onReopen,
  onShowWorktree,
  setConversationKeep,
  conversationChangeSignal = 0,
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
    connection,
    now,
    initialFilters: restoredFilters,
    onFiltersChange: rememberFilters,
  });
  const { filters, setFilters, entries: storedEntries, reload } = ledger;
  const crew = useDaemonStore((state) => state.crew);
  const entries = useMemo(() => storedEntries.map((entry) => {
    const member = crew.find((candidate) => candidate.key === entry.member_key);
    return member ? { ...entry, member_name: member.name } : entry;
  }), [storedEntries, crew]);
  const { keepNotices, runKeepVerb, clearKeepNotice } = useConversationPins(setConversationKeep, conversationChangeSignal, reload);

  const { text, setText, parsed } = useLedgerQueryText({
    restoredFilters, profileNames, facets: ledger.facets, repository: filters.repository, setFilters, requestedDir,
  });

  useReloadWhenChanged(profileMembership, reload);
  useReloadWhenChanged(sessionCostPricing(settings), reload);

  const visible = useMemo(() => entries.filter((entry) => {
    if (!matchesDir(entry.directory, parsed.dir)) return false;
    return matchesWords(
      [entry.member_name ?? entry.label, entry.id, entry.agent, entry.branch ?? '', entry.directory, entry.profile_name],
      parsed.words,
    );
  }), [entries, parsed.dir, parsed.words]);

  const [selectedId, setSelectedId] = useState<string | null>(null);
  const selected = visible.find((entry) => entry.id === selectedId) ?? visible[0] ?? null;
  const [menu, setMenu] = useState<LedgerMenu | null>(null);
  const [attempts, setAttempts] = useState<Record<string, ReopenAttempt>>({});
  const attemptFor = useCallback((entry: SessionLedgerEntry): ReopenAttempt | undefined => {
    const attempt = attempts[entry.id];
    return attempt && attempt.closedAt === (entry.closed_at ?? '') ? attempt : undefined;
  }, [attempts]);
  const [copied, copy] = useCopied();

  const recordAttempt = useCallback((entry: SessionLedgerEntry, change: Partial<Omit<ReopenAttempt, 'closedAt'>>) => {
    const closedAt = entry.closed_at ?? '';
    setAttempts((current) => {
      const previous = current[entry.id]?.closedAt === closedAt ? current[entry.id] : { closedAt };
      return { ...current, [entry.id]: { ...previous, ...change } };
    });
  }, []);

  const fire = useCallback((entry: SessionLedgerEntry, actionId: string) => {
    if (!onReopen) return;
    setMenu(null);
    const refuse = (failure: unknown) => {
      if (failure instanceof SessionReopenRefusal) {
        recordAttempt(entry, { note: { kind: 'refused', text: refusalNote(actionId, failure.verdict) }, verdict: failure.verdict });
      } else {
        recordAttempt(entry, { note: { kind: 'refused', text: compactVerdictText(failureText(failure)) } });
      }
      reload();
    };
    recordAttempt(entry, { note: { kind: 'busy', text: 'reopening…' } });
    let outcome: ReturnType<typeof onReopen>;
    try {
      outcome = onReopen(entry.id, actionId);
    } catch (failure) {
      refuse(failure);
      return;
    }
    Promise.resolve(outcome).then(() => recordAttempt(entry, { note: undefined })).catch(refuse);
  }, [onReopen, recordAttempt, reload]);

  const isLive = useCallback(
    (entry: SessionLedgerEntry) => !isClosed(entry) && (liveSessionIds?.has(entry.id) ?? true),
    [liveSessionIds],
  );

  const labelsBySession = useMemo(() => new Map(entries.map((entry) => [entry.id, entry.member_name ?? entry.label])), [entries]);
  const sessionLabel = useCallback((id: string) => labelsBySession.get(id) || id, [labelsBySession]);
  const nameText = useCallback((text: string) => nameIds(text, (id) => labelsBySession.get(id) || undefined), [labelsBySession]);
  const runVerb = useCallback((key: string, verbId: string) => {
    const entry = visible.find((row) => row.id === key);
    if (!entry) return;
    setSelectedId(entry.id);
    setMenu(null);
    if (verbId === 'focus') { onFocusSession?.(entry.id); return; }
    if (verbId === 'seed') { const seed = seedForSession?.(entry.id); if (seed) onOpenSeed?.(seed.id); return; }
    if (runKeepVerb(entry.id, verbId)) return;
    if (verbId === 'worktree') { onShowWorktree?.(entry.directory); return; }
    clearKeepNotice(entry.id);
    fire(entry, verdictId(verbId));
  }, [visible, onFocusSession, seedForSession, onOpenSeed, onShowWorktree, fire, runKeepVerb, clearKeepNotice]);

  const items = useMemo<ListItem[]>(() => visible.map((entry) => ({
    kind: 'row',
    row: sessionRow(entry, {
      verdict: attemptFor(entry)?.verdict,
      note: rowNote(keepNotices, entry.id, attemptFor(entry)),
      live: isLive(entry),
      seed: seedForSession?.(entry.id) ?? null,
      sessionLabel,
      nameText,
      canKeepConversation: !!setConversationKeep,
      actionsAvailable: !!onReopen,
      canShowWorktree: !!onShowWorktree && !!entry.is_worktree && attemptFor(entry)?.verdict?.directoryState !== 'missing',
      now: now(),
    }),
  })), [visible, attemptFor, isLive, seedForSession, sessionLabel, nameText, onReopen, onShowWorktree, now, setConversationKeep, keepNotices]);

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
          <button type="button" className="ledger-status-link" onClick={ledger.loadMore} disabled={ledger.loading || ledger.loadingMore}>
            {ledger.loadingMore ? 'loading…' : `${ledger.omitted} older ↓`}
          </button>
        )}
        {copied && <span className="ledger-status-flash">copied</span>}
      </>,
    );
  }, [shown, live, entries.length, ledger.omitted, ledger.loadMore, ledger.loading, ledger.loadingMore, copied, onStatus]);

  const chips = useMemo<Chip[]>(() => {
    const tokens = text.trim().split(/\s+/).filter(Boolean);
    return tokens
      .filter((token) => token.includes(':') || token.toLowerCase() in RANGE_LOOKUP)
      .map((token) => ({
        text: token,
        tone: parsed.unresolved.includes(token) ? 'unresolved' as const : undefined,
        onRemove: () => setText(removeToken(text, token)),
      }));
  }, [text, parsed, setText]);

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
          menu={menu}
          onMenu={setMenu}
          onYank={copy}
          empty={<p className={`ledger-empty${ledger.error || ledger.filterError ? ' is-error' : ''}`}>{emptyMessage}</p>}
        />
        {selected
          ? (
            <SessionInspector
              entry={selected}
              verdict={attemptFor(selected)?.verdict}
              note={rowNote(keepNotices, selected.id, attemptFor(selected))}
              live={isLive(selected)}
              usage={(!isClosed(selected) && liveSessionUsage?.get(selected.id)) || selected.usage}
              seed={seedForSession?.(selected.id) ?? null}
              sessionLabel={sessionLabel}
              nameText={nameText}
              now={now()}
              copied={copied}
              onCopy={copy}
              onVerb={(verbId) => runVerb(selected.id, verbId)}
              canKeepConversation={!!setConversationKeep}
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
  const [text, setText] = useState(() => formatQuery(restoredFilters, profileNames));
  const [chosenProfile, setChosenProfile] = useState<ProfileChoice | null>(() => (
    restoredFilters.profileId ? { profile_id: restoredFilters.profileId, name: profileNames[restoredFilters.profileId] ?? '' } : null
  ));
  const profiles = useMemo(() => profileChoices(profileNames, facets, chosenProfile), [profileNames, facets, chosenProfile]);
  const parsed = useMemo(() => parseQuery(text, facets, profiles, repository), [text, facets, profiles, repository]);
  const namedWith = useRef(profileNames);
  useEffect(() => {
    const before = namedWith.current;
    namedWith.current = profileNames;
    if (before !== profileNames) setText((current) => renameProfileTokens(current, before, profileNames));
  }, [profileNames]);
  const keepRepository = unresolvedWhilePending(parsed, facets, 'repo:');
  const keepProfile = unresolvedWhilePending(parsed, facets, 'profile:');

  useEffect(() => {
    const timer = window.setTimeout(() => {
      if (!keepProfile) {
        const chosenId = parsed.filters.profileId;
        setChosenProfile((current) => (
          current?.profile_id === chosenId ? current : profiles.find((choice) => choice.profile_id === chosenId) ?? null
        ));
      }
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
  }, [parsed.filters, profiles, setFilters, keepRepository, keepProfile]);

  const [appliedDir, setAppliedDir] = useState<typeof requestedDir>(null);
  if (requestedDir && requestedDir !== appliedDir) {
    setAppliedDir(requestedDir);
    setText(`dir:${requestedDir.path}`);
  }

  return { text, setText, parsed };
}

function sessionCostPricing(settings: Record<string, string>): string {
  return JSON.stringify(Object.entries(settings).filter(([key]) => key.startsWith('session_cost.')).sort());
}

function useReloadWhenChanged(value: string, reload: () => void) {
  const loadedWith = useRef(value);
  useEffect(() => {
    if (loadedWith.current === value) return;
    loadedWith.current = value;
    reload();
  }, [value, reload]);
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

function failureText(failure: unknown): string {
  return failure instanceof Error ? failure.message : String(failure);
}

interface ReopenAttempt {
  closedAt: string;
  note?: RowNote;
  verdict?: ReopenVerdictView;
}

const PLAIN_REOPEN: ReopenActionView = { id: 'reopen', label: 'Reopen' };

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
  canKeepConversation: boolean;
  actionsAvailable: boolean;
  canShowWorktree: boolean;
  now: Date;
}

function sessionRow(entry: SessionLedgerEntry, context: RowContext): RowModel {
  const closed = isClosed(entry);
  const { verdict } = context;
  const verbs: RowVerb[] = [];
  if (context.live) verbs.push({ id: 'focus', label: 'Focus' });
  if (closed && context.actionsAvailable && !entry.profile_deleted) {
    for (const action of verdict?.actions ?? [PLAIN_REOPEN]) {
      verbs.push({
        id: `act:${action.id}`,
        label: action.label,
      });
    }
  }
  if (context.seed) verbs.push({ id: 'seed', label: `Seed · ${context.seed.title}` });
  if (context.canShowWorktree) verbs.push({ id: 'worktree', label: 'Show worktree' });
  if (context.canKeepConversation && entry.agent === 'claude') verbs.push(conversationVerb(entry));

  const meta: ReactNode[] = [
    entry.priority ? <SessionPriority key="priority" priority /> : null,
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
    glyph: sessionGlyph(entry, context.live),
    title: entry.member_name ?? (entry.label || 'untitled session'),
    meta,
    stamp: { text: relativeStamp(stampAt, context.now), hint: fullStamp(stampAt) },
    note: context.note,
    verbs,
    dim: closed,
    yank: entry.directory,
    attrs: {
      state: closed ? 'closed' : entry.state,
      verbs: JSON.stringify(verbs.map((verb) => verb.label)),
      profile: entry.profile_id,
      'profile-label': profileText(entry),
    },
  };
}

function profileText(entry: SessionLedgerEntry): string {
  if (!entry.profile_name) return '';
  return entry.profile_deleted ? `${entry.profile_name} (deleted)` : entry.profile_name;
}

function sessionGlyph(entry: SessionLedgerEntry, live: boolean): RowGlyph {
  if (isClosed(entry) || !live) return 'closed';
  if (WORKING_STATES.has(entry.state)) return 'working';
  if (WAITING_STATES.has(entry.state)) return 'waiting';
  return 'live';
}

interface ReopenVerdictProps {
  profileDeleted: boolean;
  verdict: ReopenVerdictView | undefined;
  note: RowNote | undefined;
  nameText: (text: string) => string;
  onVerb: (verbId: string) => void;
  actionsAvailable: boolean;
}

function ReopenVerdict({ profileDeleted, verdict, note, nameText, onVerb, actionsAvailable }: ReopenVerdictProps) {
  const busy = note?.kind === 'busy';
  const actions = profileDeleted ? [] : verdict?.actions ?? [PLAIN_REOPEN];
  return (
    <div className={`ledger-verdict${verdict ? (verdict.reopenable ? ' is-ok' : ' is-no') : ''}`}>
      <div className="ledger-field-label">Reopen</div>
      {profileDeleted && <div className="ledger-verdict-text">Its profile was deleted. This session cannot be reopened.</div>}
      {verdict && !profileDeleted && (
        <>
          <div className="ledger-verdict-text" title={verdict.reason ?? verdict.summary}>
            {nameText(compactVerdictText(verdict.reason ?? 'It can be reopened where it ran.'))}
          </div>
          {verdict.warning && <div className="ledger-muted" title={verdict.warning}>{nameText(compactVerdictText(verdict.warning))}</div>}
          <div className="ledger-muted">{reopenPlacement(verdict)}</div>
        </>
      )}
      {note && note.kind !== 'busy' && (
        <div className={`ledger-row-note is-${note.kind}`} role="status">{note.text}</div>
      )}
      {actionsAvailable && (
        <div className="ledger-verdict-actions">
          {actions.map((action, index) => (
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
          {actions.length === 0 && <span className="ledger-muted">Nothing here brings it back.</span>}
        </div>
      )}
    </div>
  );
}

interface SessionInspectorProps {
  entry: SessionLedgerEntry;
  verdict: ReopenVerdictView | undefined;
  note: RowNote | undefined;
  live: boolean;
  usage: SessionUsage | undefined;
  seed: SessionSeedLink | null;
  sessionLabel: (id: string) => string;
  nameText: (text: string) => string;
  now: Date;
  copied: string | null;
  onCopy: (text: string) => void;
  onVerb: (verbId: string) => void;
  canKeepConversation: boolean;
  actionsAvailable: boolean;
}

function SessionKicker({ entry, live }: { entry: SessionLedgerEntry; live: boolean }) {
  return (
    <>
      <span className={`ledger-glyph is-${sessionGlyph(entry, live)}`} aria-hidden="true" />
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
  entry, verdict, note, live, usage, seed, sessionLabel, nameText, now, copied, onCopy, onVerb, actionsAvailable, canKeepConversation,
}: SessionInspectorProps) {
  return (
    <Inspector title={entry.member_name ?? (entry.label || 'untitled session')} kicker={<SessionKicker entry={entry} live={live} />}>
      <Field label="Profile">{profileText(entry) || '—'}</Field>
      <DirectoryField entry={entry} verdict={verdict} copied={copied} onCopy={onCopy} />
      <BranchField entry={entry} verdict={verdict} />
      <InstantField entry={entry} now={now} sessionLabel={sessionLabel} nameText={nameText} />
      <UsageField usage={usage} sessionId={entry.id} />
      {seed && (
        <Field label="Seed">
          <button type="button" className="ledger-link" onClick={() => onVerb('seed')}>{seed.title}</button>
        </Field>
      )}
      {isClosed(entry) && (
        <ReopenVerdict profileDeleted={!!entry.profile_deleted} verdict={verdict} note={note} nameText={nameText} onVerb={onVerb} actionsAvailable={actionsAvailable} />
      )}
      <ConversationPinAction available={canKeepConversation} entry={entry} note={note} closed={isClosed(entry)} onVerb={onVerb} />
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

const keepPopoverUnpinned = () => {};

function UsageField({ usage, sessionId }: { usage: SessionUsage | undefined; sessionId: string }) {
  if (!usage) return null;
  const tokens = `${usage.total_tokens.toLocaleString('en-US')} tokens`;
  if (usage.measurement_incomplete) {
    return <Field label="Usage">
      {tokens}
      <div className="ledger-muted">Measurement incomplete; some usage may be missing.</div>
    </Field>;
  }
  return <Field label="Usage">
    <HeaderSessionUsage usage={usage} sessionId={sessionId} pinned={false} onPopoverClosed={keepPopoverUnpinned} popoverClassName="ledger-usage-popover" />
    {usage.cost_usd !== undefined && <span className="ledger-muted"> · {tokens}</span>}
  </Field>;
}

function conversationVerb(entry: SessionLedgerEntry): RowVerb {
  return entry.conversation_pinned_at
    ? { id: 'unkeep-conversation', label: 'Unkeep' }
    : { id: 'keep-conversation', label: 'Keep conversation' };
}

function useConversationPins(setKeep: SessionsTabProps['setConversationKeep'], changeSignal: number, reload: () => void) {
  const [keepNotices, setKeepNotices] = useState<Record<string, RowNote | undefined>>({});
  const observedSignal = useRef(changeSignal);
  useEffect(() => {
    if (observedSignal.current === changeSignal) return;
    observedSignal.current = changeSignal;
    reload();
  }, [changeSignal, reload]);
  const clearKeepNotice = useCallback((id: string) => {
    setKeepNotices((current) => current[id] ? { ...current, [id]: undefined } : current);
  }, []);
  const runKeepVerb = useCallback((id: string, verb: string): boolean => {
    if (!setKeep || (verb !== 'keep-conversation' && verb !== 'unkeep-conversation')) return false;
    if (keepNotices[id]?.kind === 'busy') return true;
    setKeepNotices((current) => ({ ...current, [id]: { kind: 'busy', text: 'changing conversation pin…' } }));
    void setKeep(`session:${id}`, verb === 'keep-conversation').then(() => {
      setKeepNotices((current) => ({ ...current, [id]: undefined }));
    }).catch((failure: Error) => {
      setKeepNotices((current) => ({ ...current, [id]: { kind: 'refused', text: failure.message } }));
    });
    return true;
  }, [setKeep, keepNotices]);
  return { keepNotices, runKeepVerb, clearKeepNotice };
}

function ConversationPinAction({ available, entry, note, closed, onVerb }: {
  available: boolean;
  entry: SessionLedgerEntry;
  note?: RowNote;
  closed: boolean;
  onVerb: (id: string) => void;
}) {
  if (!available || entry.agent !== 'claude') return null;
  return <Field label="Conversation">
    <div className="ledger-muted">{entry.conversation_pinned_at ? 'Kept forever' : 'Keep attn’s copy forever'}</div>
    {note && note.kind !== 'busy' && !closed && <div className="ledger-row-note is-refused" role="status">{note.text}</div>}
    <button type="button" className="ledger-verb" disabled={note?.kind === 'busy'} onClick={() => onVerb(conversationVerb(entry).id)}>{conversationVerb(entry).label}</button>
  </Field>;
}

function rowNote(notices: Record<string, RowNote | undefined>, id: string, attempt?: ReopenAttempt): RowNote | undefined {
  return notices[id] ?? attempt?.note;
}
