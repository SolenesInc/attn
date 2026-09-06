import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import FocusTrap from 'focus-trap-react';
import type { SessionLedgerEntry, SessionReopen } from '../types/generated';
import type { SessionLedgerPage, SessionLedgerQuery } from '../hooks/daemonSessionLedgerEvents';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { useSessionLedger } from '../hooks/useSessionLedger';
import type { SessionLedgerFilters } from '../hooks/useSessionLedger';
import {
  SESSION_FILTERS_SETTING_KEY,
  parseSessionFilters,
  serializeSessionFilters,
} from '../hooks/sessionFiltersSetting';
import { useSettings } from '../contexts/SettingsContext';
import {
  SESSION_RANGE_CHOICES,
  branchStateLabel,
  closedBySomeone,
  directoryStateLabel,
  isClosed,
  ledgerInstant,
  ledgerState,
  reopenPlacement,
  compactRefusalText,
  compactVerdictText,
  shortPath,
} from './sessionsLedger';
import type { ReopenActionView, ReopenVerdictView, SessionRangeId, SessionScope } from './sessionsLedger';
import './SessionsPanel.css';

export interface SessionSeedLink {
  id: string;
  title: string;
}

export interface SessionsPanelProps {
  isOpen: boolean;
  onClose: () => void;
  listSessions: (query: SessionLedgerQuery) => Promise<SessionLedgerPage>;
  workspaceNames?: Record<string, string>;
  liveSessionIds?: Set<string>;
  seedForSession?: (sessionId: string) => SessionSeedLink | null;
  onFocusSession?: (sessionId: string) => void;
  onOpenSeed?: (seedId: string) => void;
  onReopen?: (sessionId: string, actionId: string) => Promise<boolean | void> | boolean | void;
  /** The nonce makes a repeat close of the same session a new notice. */
  closeNotice?: { entry: SessionLedgerEntry; reopen?: SessionReopen; nonce: number };
  verdictNotice?: { verdicts: Record<string, SessionReopen>; nonce: number };
  yieldsFocus?: boolean;
  now?: () => Date;
}

const systemNow = () => new Date();

const NO_WORKSPACE_NAMES: Record<string, string> = {};

const SCOPES: { id: SessionScope; label: string }[] = [
  { id: 'live', label: 'Live' },
  { id: 'closed', label: 'Closed' },
  { id: 'all', label: 'All' },
];

interface RowNotice {
  kind: 'busy' | 'refused';
  text: string;
}

export function SessionsPanel(props: SessionsPanelProps) {
  if (!props.isOpen) return null;
  return <OpenSessionsPanel {...props} />;
}

function OpenSessionsPanel({
  onClose,
  listSessions,
  workspaceNames = NO_WORKSPACE_NAMES,
  liveSessionIds,
  seedForSession,
  onFocusSession,
  onOpenSeed,
  onReopen,
  yieldsFocus = false,
  closeNotice,
  verdictNotice,
  now = systemNow,
}: SessionsPanelProps) {
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
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [menuFor, setMenuFor] = useState<string | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  // Fires against the verdict that lands, never the stale one that was on screen.
  const [awaiting, setAwaiting] = useState<{ sessionId: string; actionId: string } | null>(null);
  const [notices, setNotices] = useState<Record<string, RowNotice>>({});
  const rowsRef = useRef<HTMLTableSectionElement>(null);

  useEscapeStack(onClose, true);
  useEscapeStack(() => setMenuFor(null), menuFor !== null);

  useEffect(() => {
    if (!closeNotice) return;
    recordClose(closeNotice.entry, closeNotice.reopen);
  }, [closeNotice, recordClose]);

  useEffect(() => {
    if (!verdictNotice) return;
    for (const [sessionId, reopen] of Object.entries(verdictNotice.verdicts)) recordVerdict(sessionId, reopen);
  }, [verdictNotice, recordVerdict]);

  const selected = entries.find((entry) => entry.id === selectedId) ?? entries[0] ?? null;

  const setNotice = useCallback((sessionId: string, notice: RowNotice | null) => {
    setNotices((current) => {
      if (!notice) {
        if (!(sessionId in current)) return current;
        const next = { ...current };
        delete next[sessionId];
        return next;
      }
      return { ...current, [sessionId]: notice };
    });
  }, []);

  const fire = useCallback((sessionId: string, actionId: string) => {
    if (!onReopen) return;
    setMenuFor(null);
    const refuse = (failure: unknown) => {
      const text = failure instanceof Error ? failure.message : String(failure);
      setNotice(sessionId, { kind: 'refused', text });
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

  useEffect(() => {
    if (!awaiting) return;
    const verdict = verdicts[awaiting.sessionId];
    if (!verdict || verdict.refreshing) return;
    const stillOffered = verdict.actions.some((action) => action.id === awaiting.actionId);
    if (stillOffered) {
      fire(awaiting.sessionId, awaiting.actionId);
    } else {
      setNotice(awaiting.sessionId, {
        kind: 'refused',
        text: `The check finished and that is no longer possible: ${verdict.summary}`,
      });
    }
    setAwaiting(null);
  }, [awaiting, verdicts, fire, setNotice]);

  const runAction = useCallback((entry: SessionLedgerEntry, actionId: string) => {
    setNotice(entry.id, null);
    const verdict = verdicts[entry.id];
    if (verdict && !verdict.refreshing) {
      fire(entry.id, actionId);
      return;
    }
    setAwaiting({ sessionId: entry.id, actionId });
  }, [verdicts, fire, setNotice]);

  const moveSelection = useCallback((offset: number) => {
    if (entries.length === 0) return;
    const current = Math.max(0, entries.findIndex((entry) => entry.id === selected?.id));
    const next = Math.min(entries.length - 1, Math.max(0, current + offset));
    setSelectedId(entries[next].id);
    setMenuFor(null);
    rowsRef.current?.querySelectorAll<HTMLTableRowElement>('tr[data-session-id]')[next]?.focus();
  }, [entries, selected]);

  const update = useCallback((patch: Partial<SessionLedgerFilters>) => {
    setFilters((current) => ({ ...current, ...patch }));
  }, [setFilters]);

  const repositories = ledger.facets?.repositories ?? [];
  const workspaces = ledger.facets?.workspaces ?? [];
  const workspaceLabel = useCallback(
    (id: string) => workspaceNames[id] ?? id,
    [workspaceNames],
  );

  const labelsBySession = useMemo(
    () => new Map(entries.map((entry) => [entry.id, entry.label])),
    [entries],
  );
  const sessionLabel = useCallback(
    (id: string) => labelsBySession.get(id) || id,
    [labelsBySession],
  );

  const isLive = useCallback(
    (entry: SessionLedgerEntry) => !isClosed(entry) && (liveSessionIds?.has(entry.id) ?? true),
    [liveSessionIds],
  );

  const body = useMemo(() => {
    if (ledger.filterError) {
      return <p className="sessions-state sessions-state-error">{ledger.filterError}</p>;
    }
    if (ledger.loading && entries.length === 0) {
      return <p className="sessions-state">Reading the ledger…</p>;
    }
    if (ledger.error) {
      return (
        <p className="sessions-state sessions-state-error">
          {ledger.error}
          <button type="button" onClick={ledger.reload}>Try again</button>
        </p>
      );
    }
    if (entries.length === 0) {
      return <p className="sessions-state">{emptyMessage(filters)}</p>;
    }
    return null;
  }, [ledger, entries.length, filters]);

  return (
    <div className="sessions-shell">
      <FocusTrap paused={yieldsFocus} focusTrapOptions={{ escapeDeactivates: false }}>
        <div className="sessions-panel" role="dialog" aria-modal="true" aria-labelledby="sessions-title">
          <header className="sessions-header">
            <h1 id="sessions-title">Sessions</h1>
            <div className="sessions-filters">
              <div className="sessions-scope" role="group" aria-label="Which sessions">
                {SCOPES.map((scope) => (
                  <button
                    key={scope.id}
                    type="button"
                    className={filters.scope === scope.id ? 'is-selected' : undefined}
                    aria-pressed={filters.scope === scope.id}
                    onClick={() => update({ scope: scope.id })}
                  >
                    {scope.label}
                  </button>
                ))}
              </div>

              <label className="sessions-filter">
                <span>When</span>
                <select
                  value={filters.range}
                  onChange={(event) => update({ range: event.target.value as SessionRangeId })}
                >
                  {SESSION_RANGE_CHOICES.map((choice) => (
                    <option key={choice.id} value={choice.id}>{choice.label}</option>
                  ))}
                </select>
              </label>

              {filters.range === 'custom' && (
                <div className="sessions-custom-range">
                  <label>
                    <span>From</span>
                    <input
                      type="date"
                      value={filters.customFrom}
                      onChange={(event) => update({ customFrom: event.target.value })}
                    />
                  </label>
                  <label>
                    <span>To</span>
                    <input
                      type="date"
                      value={filters.customTo}
                      onChange={(event) => update({ customTo: event.target.value })}
                    />
                  </label>
                </div>
              )}

              <label className="sessions-filter">
                <span>Workspace</span>
                <select
                  value={filters.workspaceId}
                  onChange={(event) => update({ workspaceId: event.target.value })}
                >
                  <option value="">Every workspace</option>
                  {workspaces.map((facet) => (
                    <option key={facet.value} value={facet.value}>
                      {workspaceLabel(facet.value)} ({facet.count})
                    </option>
                  ))}
                </select>
              </label>

              <label className="sessions-filter">
                <span>Repository</span>
                <select
                  value={filters.repository}
                  onChange={(event) => update({ repository: event.target.value })}
                >
                  <option value="">Every repository</option>
                  {repositories.map((facet) => (
                    <option key={facet.value} value={facet.value}>
                      {shortPath(facet.value, 1)} ({facet.count})
                    </option>
                  ))}
                </select>
              </label>

            </div>
            <button type="button" className="sessions-close" onClick={onClose}>
              <span>Close</span><kbd>esc</kbd>
            </button>
          </header>

          <div className="sessions-body">
            {body}
            {entries.length > 0 && (
              <table className="sessions-table">
                <thead>
                  <tr>
                    <th scope="col">Session</th>
                    <th scope="col">Agent</th>
                    <th scope="col">State</th>
                    <th scope="col">Workspace</th>
                    <th scope="col">Where</th>
                    <th scope="col">Seed</th>
                    <th scope="col">When</th>
                    <th scope="col">Reopen</th>
                    <th scope="col"><span className="sessions-visually-hidden">Actions</span></th>
                  </tr>
                </thead>
                <tbody ref={rowsRef}>
                  {entries.map((entry) => {
                    const closed = isClosed(entry);
                    const verdict = closed ? verdicts[entry.id] : undefined;
                    const isSelected = entry.id === selected?.id;
                    // One fragment per entry: a detail that comes and goes must not remount the row.
                    return (
                      <Fragment key={entry.id}>
                      <SessionRow
                        entry={entry}
                        seed={seedForSession?.(entry.id) ?? null}
                        verdict={verdict}
                        notice={notices[entry.id]}
                        waiting={awaiting?.sessionId === entry.id}
                        live={isLive(entry)}
                        selected={isSelected}
                        detailOpen={detailOpen && isSelected && closed}
                        menuOpen={menuFor === entry.id}
                        workspaceLabel={workspaceLabel}
                        sessionLabel={sessionLabel}
                        actionsAvailable={!!onReopen}
                        onSelect={setSelectedId}
                        onMoveSelection={moveSelection}
                        onToggleMenu={(open) => setMenuFor(open ? entry.id : null)}
                        onToggleDetail={() => setDetailOpen((open) => !open)}
                        onFocusSession={onFocusSession}
                        onOpenSeed={onOpenSeed}
                        onRunAction={runAction}
                      />
                      {detailOpen && isSelected && closed && (
                        <DetailRow
                          entry={entry}
                          verdict={verdict}
                          notice={notices[entry.id]}
                          workspaceLabel={workspaceLabel}
                          actionsAvailable={!!onReopen}
                          onRunAction={runAction}
                        />
                      )}
                      </Fragment>
                    );
                  })}
                </tbody>
              </table>
            )}
          </div>

          <footer className="sessions-footer">
            <span>
              {entries.length === 0
                ? 'Nothing to show'
                : `showing ${entries.length}${ledger.omitted > 0 ? `, ${ledger.omitted} older` : ''}`}
            </span>
            {ledger.omitted > 0 && (
              <button type="button" onClick={ledger.loadMore} disabled={ledger.loadingMore}>
                {ledger.loadingMore ? 'Loading…' : 'Load more'}
              </button>
            )}
            <span className="sessions-keys">
              <kbd>↑</kbd><kbd>↓</kbd> move
              <kbd>⏎</kbd> focus or first action
              <kbd>1</kbd>–<kbd>9</kbd> nth action
              <kbd>→</kbd> more
              <kbd>␣</kbd> why
            </span>
          </footer>
        </div>
      </FocusTrap>
    </div>
  );
}

interface SessionRowProps {
  entry: SessionLedgerEntry;
  seed: SessionSeedLink | null;
  verdict: ReopenVerdictView | undefined;
  notice: RowNotice | undefined;
  waiting: boolean;
  live: boolean;
  selected: boolean;
  detailOpen: boolean;
  menuOpen: boolean;
  workspaceLabel: (workspaceId: string) => string;
  sessionLabel: (sessionId: string) => string;
  actionsAvailable: boolean;
  onSelect: (sessionId: string) => void;
  onMoveSelection: (offset: number) => void;
  onToggleMenu: (open: boolean) => void;
  onToggleDetail: () => void;
  onFocusSession?: (sessionId: string) => void;
  onOpenSeed?: (seedId: string) => void;
  onRunAction: (entry: SessionLedgerEntry, actionId: string) => void;
}

function SessionRow({
  entry,
  seed,
  verdict,
  notice,
  waiting,
  live,
  selected,
  detailOpen,
  menuOpen,
  workspaceLabel,
  sessionLabel,
  actionsAvailable,
  onSelect,
  onMoveSelection,
  onToggleMenu,
  onToggleDetail,
  onFocusSession,
  onOpenSeed,
  onRunAction,
}: SessionRowProps) {
  const closed = isClosed(entry);
  const actions = closed && actionsAvailable ? verdict?.actions ?? [] : [];
  const busy = notice?.kind === 'busy';

  return (
    <tr
      tabIndex={0}
      data-session-id={entry.id}
      aria-selected={selected}
      aria-busy={busy || undefined}
      aria-expanded={closed ? detailOpen : undefined}
      className={selected ? 'is-selected' : undefined}
      onFocus={() => onSelect(entry.id)}
      onClick={() => onSelect(entry.id)}
      onKeyDown={(event) => {
        if (event.key === 'ArrowDown') {
          event.preventDefault();
          onMoveSelection(1);
        } else if (event.key === 'ArrowUp') {
          event.preventDefault();
          onMoveSelection(-1);
        } else if (event.key === 'Enter') {
          event.preventDefault();
          if (live) onFocusSession?.(entry.id);
          else if (!busy && actions[0]) onRunAction(entry, actions[0].id);
        } else if (/^[1-9]$/.test(event.key)) {
          const action = actions[Number(event.key) - 1];
          if (action && !busy) {
            event.preventDefault();
            onRunAction(entry, action.id);
          }
        } else if (event.key === ' ' && closed) {
          event.preventDefault();
          onToggleDetail();
        } else if (event.key === 'ArrowRight' && actions.length > 1) {
          event.preventDefault();
          onToggleMenu(true);
        } else if (event.key === 'ArrowLeft' && menuOpen) {
          event.preventDefault();
          onToggleMenu(false);
        }
      }}
    >
      <td>
        <span className="sessions-label">{entry.label || entry.id}</span>
        <span className="sessions-id">{entry.id}</span>
      </td>
      <td>{entry.agent}</td>
      <td><span className={`sessions-state-chip is-${ledgerState(entry)}`}>{ledgerState(entry)}</span></td>
      <td>{workspaceLabel(entry.workspace_id) || '—'}</td>
      <td>
        <span title={entry.directory}>{shortPath(entry.directory)}</span>
        {entry.branch && <span className="sessions-branch">{entry.branch}</span>}
      </td>
      <td>
        {seed
          ? <button type="button" className="sessions-link" onClick={() => onOpenSeed?.(seed.id)}>{seed.title}</button>
          : '—'}
      </td>
      <td>
        <span title={ledgerInstant(entry)}>{shortStamp(ledgerInstant(entry))}</span>
        {closed && (
          <span className="sessions-closed-by" title={entry.closed_by ?? undefined}>
            closed by {closedBySomeone(entry, sessionLabel)}
            {entry.close_reason ? `: ${entry.close_reason}` : ''}
          </span>
        )}
      </td>
      <td className="sessions-verdict-cell" onClick={closed ? onToggleDetail : undefined}>
        {renderVerdict(verdict, closed, waiting, notice)}
      </td>
      <td className="sessions-actions">
        {live && (
          <button type="button" onClick={() => onFocusSession?.(entry.id)}>Focus</button>
        )}
        {actions[0] && (
          <ActionButton action={actions[0]} busy={busy} primary onRun={() => onRunAction(entry, actions[0].id)} />
        )}
        {actions.length > 1 && (
          <span className="sessions-menu-anchor">
            <button
              type="button"
              className="sessions-menu-toggle"
              aria-label={`More ways to bring back ${entry.label || entry.id}`}
              aria-expanded={menuOpen}
              disabled={busy}
              onClick={(event) => {
                event.stopPropagation();
                onToggleMenu(!menuOpen);
              }}
            >
              ▾
            </button>
            {menuOpen && (
              <ul className="sessions-menu" role="menu">
                {actions.slice(1).map((action, index) => (
                  <li key={action.id} role="none">
                    <button
                      type="button"
                      role="menuitem"
                      onClick={() => onRunAction(entry, action.id)}
                    >
                      <kbd>{index + 2}</kbd> {action.label}
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </span>
        )}
      </td>
    </tr>
  );
}

function ActionButton({
  action,
  busy,
  primary,
  onRun,
}: {
  action: ReopenActionView;
  busy: boolean;
  primary?: boolean;
  onRun: () => void;
}) {
  return (
    <button
      type="button"
      className={primary ? 'sessions-action-primary' : undefined}
      disabled={busy}
      onClick={onRun}
    >
      {busy && primary && <span className="sessions-pulse" aria-hidden="true" />}
      {busy && primary ? 'Reopening…' : action.label}
    </button>
  );
}

interface DetailRowProps {
  entry: SessionLedgerEntry;
  verdict: ReopenVerdictView | undefined;
  notice: RowNotice | undefined;
  workspaceLabel: (workspaceId: string) => string;
  actionsAvailable: boolean;
  onRunAction: (entry: SessionLedgerEntry, actionId: string) => void;
}

function DetailRow({ entry, verdict, notice, workspaceLabel, actionsAvailable, onRunAction }: DetailRowProps) {
  const busy = notice?.kind === 'busy';
  const rowRef = useRef<HTMLTableRowElement>(null);
  useEffect(() => {
    rowRef.current?.scrollIntoView?.({ block: 'nearest' });
  }, [entry.id]);
  return (
    <tr ref={rowRef} className="sessions-detail-row" data-detail-for={entry.id}>
      <td colSpan={9}>
        {!verdict && <span className="sessions-verdict-none">No verdict yet.</span>}
        {verdict && (
          <div className="sessions-detail">
            <dl>
              <dt>Verdict</dt>
              <dd className={verdict.reopenable ? 'sessions-verdict-ok' : 'sessions-verdict-no'} title={verdict.reason}>
                {compactVerdictText(verdict.reason ?? 'it can be reopened where it ran')}
                {verdict.refreshing && <em> checking…</em>}
              </dd>
              {verdict.warning && (
                <>
                  <dt>Warning</dt>
                  <dd title={verdict.warning}>{compactVerdictText(verdict.warning)}</dd>
                </>
              )}
              <dt>Directory</dt>
              <dd>
                {directoryStateLabel(verdict.directoryState)}
                <span className="sessions-detail-path" title={entry.directory}> {entry.directory}</span>
              </dd>
              {branchStateLabel(verdict.branchState) && (
                <>
                  <dt>Branch</dt>
                  <dd>{branchStateLabel(verdict.branchState)}{entry.branch ? `: ${entry.branch}` : ''}</dd>
                </>
              )}
              <dt>Lands</dt>
              <dd>{reopenPlacement(verdict, workspaceLabel)}</dd>
            </dl>
            {actionsAvailable && (
              <div className="sessions-detail-actions">
                {verdict.actions.map((action, index) => (
                  <button
                    key={action.id}
                    type="button"
                    className={index === 0 ? 'sessions-action-primary' : undefined}
                    disabled={busy}
                    onClick={() => onRunAction(entry, action.id)}
                  >
                    <kbd>{index === 0 ? '⏎' : index + 1}</kbd> {action.label}
                  </button>
                ))}
                {verdict.actions.length === 0 && (
                  <span className="sessions-verdict-no">Nothing here brings it back.</span>
                )}
              </div>
            )}
          </div>
        )}
      </td>
    </tr>
  );
}

function renderVerdict(
  verdict: ReopenVerdictView | undefined,
  closed: boolean,
  waiting: boolean,
  notice: RowNotice | undefined,
): React.ReactNode {
  if (!closed || !verdict) return <span className="sessions-verdict-none">—</span>;
  const tail = (
    <>
      {waiting && <em> waiting for the check…</em>}
      {notice?.kind === 'refused' && (
        <span className="sessions-row-refusal" role="status" title={notice.text}>{compactRefusalText(notice.text)}</span>
      )}
    </>
  );
  if (verdict.refreshing) {
    return (
      <span className="sessions-verdict-refreshing" title={verdict.summary}>
        {compactVerdictText(verdict.summary)} <em><span className="sessions-pulse" aria-hidden="true" />checking…</em>
        {tail}
      </span>
    );
  }
  return (
    <span className={verdict.reopenable ? 'sessions-verdict-ok' : 'sessions-verdict-no'} title={verdict.summary}>
      {compactVerdictText(verdict.summary)}
      {tail}
    </span>
  );
}

function emptyMessage(filters: SessionLedgerFilters): string {
  if (filters.workspaceId || filters.repository || filters.range !== 'any') {
    return 'No sessions match those filters — widen one to see more.';
  }
  if (filters.scope === 'closed') return 'No closed sessions yet. Closing one records it here.';
  if (filters.scope === 'live') return 'No live sessions right now.';
  return 'The ledger is empty.';
}

function shortStamp(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return iso;
  return at.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}
