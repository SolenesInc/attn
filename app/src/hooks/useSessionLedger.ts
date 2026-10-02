import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import type { SessionLedgerEntry, SessionLedgerFacets } from '../types/generated';
import type {
  SessionLedgerConnectionEvent,
  SessionLedgerPage,
  SessionLedgerQuery,
  SessionLedgerUpdate,
} from './daemonSessionLedgerEvents';
import {
  customSessionRange,
  isRangeError,
  ledgerInstant,
  sessionRangeWindow,
} from '../components/sessionsLedger';
import type { SessionRangeId, SessionScope } from '../components/sessionsLedger';

export interface SessionLedgerFilters {
  scope: SessionScope;
  range: SessionRangeId;
  customFrom: string;
  customTo: string;
  workspaceId: string;
  repository: string;
}

export const EMPTY_SESSION_FILTERS: SessionLedgerFilters = {
  scope: 'all',
  range: 'any',
  customFrom: '',
  customTo: '',
  workspaceId: '',
  repository: '',
};

export const SESSION_PAGE_SIZE = 50;

const systemNow = () => new Date();

export interface UseSessionLedgerOptions {
  enabled: boolean;
  connection: SessionLedgerConnection;
  pageSize?: number;
  now?: () => Date;
  initialFilters?: SessionLedgerFilters;
  onFiltersChange?: (filters: SessionLedgerFilters) => void;
}

export interface SessionLedgerConnection {
  list: (query: SessionLedgerQuery) => Promise<SessionLedgerPage>;
  subscribe: (listener: (event: SessionLedgerConnectionEvent) => void) => () => void;
}

export interface SessionLedgerView {
  filters: SessionLedgerFilters;
  setFilters: Dispatch<SetStateAction<SessionLedgerFilters>>;
  entries: SessionLedgerEntry[];
  facets: SessionLedgerFacets | null;
  omitted: number;
  loading: boolean;
  loadingMore: boolean;
  error: string | null;
  filterError: string | null;
  reload: () => void;
  loadMore: () => void;
}

export function sameFilters(a: SessionLedgerFilters, b: SessionLedgerFilters): boolean {
  return a.scope === b.scope
    && a.range === b.range
    && a.customFrom === b.customFrom
    && a.customTo === b.customTo
    && a.workspaceId === b.workspaceId
    && a.repository === b.repository;
}

export function sessionLedgerQuery(
  filters: SessionLedgerFilters,
  now: Date,
): SessionLedgerQuery | { error: string } {
  const query: SessionLedgerQuery = {};
  if (filters.scope === 'closed') query.closed = true;
  if (filters.scope === 'all') query.all = true;

  const range = filters.range === 'custom'
    ? customSessionRange(filters.customFrom, filters.customTo)
    : sessionRangeWindow(filters.range, now);
  if (isRangeError(range)) return range;
  if (range.since) query.since = range.since;
  if (range.until) query.until = range.until;

  if (filters.workspaceId) query.workspace_id = filters.workspaceId;
  if (filters.repository) query.repository = filters.repository;
  return query;
}

export function closeBelongsInView(
  entry: SessionLedgerEntry,
  filters: SessionLedgerFilters,
  now: Date,
): boolean {
  if (filters.scope === 'live') return false;
  if (filters.workspaceId && entry.workspace_id !== filters.workspaceId) return false;
  if (filters.repository && (entry.repository ?? '') !== filters.repository) return false;
  const range = filters.range === 'custom'
    ? customSessionRange(filters.customFrom, filters.customTo)
    : sessionRangeWindow(filters.range, now);
  if (isRangeError(range)) return false;
  const at = ledgerInstant(entry);
  if (range.since && at < range.since) return false;
  if (range.until && at >= range.until) return false;
  return true;
}

function applyUpdate(
  entries: SessionLedgerEntry[],
  event: SessionLedgerUpdate,
  filters: SessionLedgerFilters,
  at: Date,
): SessionLedgerEntry[] {
  let updated: SessionLedgerEntry;
  if (event.type === 'closed') {
    updated = event.entry;
  } else {
    const session = event.session;
    const entry = entries.find((row) => row.id === session.id);
    const { closed_at: _at, closed_by: _by, close_reason: _reason, ...open } = entry ?? {};
    updated = { ...open, id: session.id, agent: session.agent, codex_mode: session.codex_mode,
      label: session.label, state: session.state,
      last_seen: session.last_seen, usage: session.usage, directory: session.directory,
      workspace_id: session.workspace_id, repository: session.repository, branch: session.branch,
      main_repo: session.main_repo, is_worktree: session.is_worktree };
  }
  const remaining = entries.filter((row) => row.id !== updated.id);
  const belongs = event.type === 'closed'
    ? closeBelongsInView(updated, filters, at)
    : filters.scope !== 'closed' && closeBelongsInView(updated, { ...filters, scope: 'all' }, at);
  if (!belongs) return remaining;
  return sortEntries([...remaining, updated]);
}

function sortEntries(entries: SessionLedgerEntry[]): SessionLedgerEntry[] {
  return [...entries].sort((a, b) => {
    const left = ledgerInstant(a);
    const right = ledgerInstant(b);
    return left < right ? 1 : left > right ? -1 : a.id < b.id ? 1 : a.id > b.id ? -1 : 0;
  });
}

type ReadUpdates = Map<string, SessionLedgerUpdate>;

function overlayUpdates(entries: SessionLedgerEntry[], updates: ReadUpdates, filters: SessionLedgerFilters, at: Date) {
  for (const update of updates.values()) entries = applyUpdate(entries, update, filters, at);
  return sortEntries(entries);
}

interface LedgerRead {
  query: string | null;
  entries: SessionLedgerEntry[];
  facets: SessionLedgerFacets | null;
  error: string | null;
}

const NO_READ: LedgerRead = { query: null, entries: [], facets: null, error: null };

export function useSessionLedger({
  enabled,
  connection,
  pageSize = SESSION_PAGE_SIZE,
  now = systemNow,
  initialFilters = EMPTY_SESSION_FILTERS,
  onFiltersChange,
}: UseSessionLedgerOptions): SessionLedgerView {
  const [filters, setFilters] = useState<SessionLedgerFilters>(initialFilters);
  const [read, setRead] = useState<LedgerRead>(NO_READ);
  const [omitted, setOmitted] = useState(0);
  const [nextBefore, setNextBefore] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadingMoreRead, setLoadingMoreRead] = useState<object | null>(null);
  const [reloadNonce, setReloadNonce] = useState(0);
  const [lifecycle, setLifecycle] = useState({ connected: false, generation: 0 });
  const lifecycleRef = useRef(lifecycle);
  const readEpoch = useRef(0);
  const pendingUpdates = useRef<ReadUpdates | null>(null);
  const loadingMore = loadingMoreRead !== null;

  const filtersRef = useRef(filters);
  useEffect(() => {
    filtersRef.current = filters;
  }, [filters]);

  useEffect(() => {
    if (!enabled) return;
    return connection.subscribe((event) => {
      if (event.type === 'connection') {
        const next = { connected: event.connected, generation: event.connectionGeneration };
        lifecycleRef.current = next;
        setLifecycle((current) => current.connected === next.connected && current.generation === next.generation
          ? current
          : next);
        if (!event.connected) {
          readEpoch.current += 1;
          pendingUpdates.current = null;
          setLoading(false);
          setLoadingMoreRead(null);
        }
        return;
      }
      if (!lifecycleRef.current.connected
        || event.connectionGeneration !== lifecycleRef.current.generation) return;
      if (event.type === 'invalidate') {
        readEpoch.current += 1;
        pendingUpdates.current = null;
        setReloadNonce((n) => n + 1);
        return;
      }
      const id = event.type === 'live' ? event.session.id : event.entry.id;
      pendingUpdates.current?.set(id, event);
      setRead((current) => ({ ...current, entries: applyUpdate(current.entries, event, filtersRef.current, now()) }));
    });
  }, [connection.subscribe, enabled, now]);

  const reportedRef = useRef(filters);
  useEffect(() => {
    if (sameFilters(filters, reportedRef.current)) return;
    reportedRef.current = filters;
    onFiltersChange?.(filters);
  }, [filters, onFiltersChange]);

  const query = useMemo(() => sessionLedgerQuery(filters, now()), [filters, now]);
  const filterError = 'error' in query ? query.error : null;
  const queryKey = useMemo(() => JSON.stringify(query), [query]);

  useEffect(() => {
    const epoch = ++readEpoch.current;
    pendingUpdates.current = null;
    setLoadingMoreRead(null);
    setNextBefore(null);
    setOmitted(0);
    if (!enabled || filterError) {
      setLoading(false);
      return;
    }
    if (!lifecycle.connected) {
      setLoading(false);
      return;
    }
    const generation = lifecycle.generation;
    const superseded = () => epoch !== readEpoch.current || generation !== lifecycleRef.current.generation;
    const updates: ReadUpdates = new Map();
    pendingUpdates.current = updates;
    setLoading(true);
    setRead((current) => (current.query === queryKey ? { ...current, error: null } : current));
    connection.list({ ...(query as SessionLedgerQuery), limit: pageSize })
      .then((page) => {
        if (superseded()) return;
        setRead({ query: queryKey, entries: overlayUpdates(page.entries ?? [], updates, filtersRef.current, now()), facets: page.facets ?? null, error: null });
        setOmitted(page.omitted ?? 0);
        setNextBefore(page.next_before ?? null);
      })
      .catch((failure: Error) => {
        if (superseded()) return;
        setRead((current) => (current.query === queryKey
          ? { ...current, error: failure.message }
          : { ...NO_READ, query: queryKey, error: failure.message }));
      })
      .finally(() => {
        if (pendingUpdates.current === updates) pendingUpdates.current = null;
        if (epoch === readEpoch.current) setLoading(false);
      });
    return () => {
      if (pendingUpdates.current === updates) pendingUpdates.current = null;
      if (readEpoch.current === epoch) readEpoch.current += 1;
    };
  }, [enabled, query, queryKey, filterError, connection.list, lifecycle, pageSize, reloadNonce]);

  const reload = useCallback(() => setReloadNonce((n) => n + 1), []);

  const loadMore = useCallback(() => {
    if (pendingUpdates.current || !nextBefore || loading || loadingMore || filterError || !lifecycleRef.current.connected) return;
    const before = read.entries[read.entries.length - 1]?.id ?? nextBefore;
    const epoch = readEpoch.current;
    const generation = lifecycleRef.current.generation;
    const token = {};
    const updates: ReadUpdates = new Map();
    pendingUpdates.current = updates;
    const superseded = () => epoch !== readEpoch.current || generation !== lifecycleRef.current.generation;
    setLoadingMoreRead(token);
    connection.list({ ...(sessionLedgerQuery(filtersRef.current, now()) as SessionLedgerQuery), limit: pageSize, before })
      .then((page) => {
        if (superseded()) return;
        const snapshot = new Map(updates);
        setRead((current) => {
          const present = new Set(current.entries.map((entry) => entry.id));
          return { ...current, entries: overlayUpdates([...current.entries, ...(page.entries ?? []).filter((entry) => !present.has(entry.id))], snapshot, filtersRef.current, now()) };
        });
        setOmitted(page.omitted ?? 0);
        setNextBefore(page.next_before ?? null);
      })
      .catch((failure: Error) => {
        if (epoch === readEpoch.current) setRead((current) => ({ ...current, error: failure.message }));
      })
      .finally(() => {
        if (pendingUpdates.current === updates) pendingUpdates.current = null;
        setLoadingMoreRead((current) => current === token ? null : current);
      });
  }, [nextBefore, read.entries, loading, loadingMore, filterError, connection.list, pageSize, now]);

  const { entries, facets, error } = read.query === queryKey ? read : NO_READ;

  return {
    filters,
    setFilters,
    entries,
    facets,
    omitted,
    loading,
    loadingMore,
    error,
    filterError,
    reload,
    loadMore,
  };
}
