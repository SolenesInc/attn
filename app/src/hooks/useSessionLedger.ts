import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import type { SessionLedgerEntry, SessionLedgerFacets } from '../types/generated';
import type {
  SessionLedgerConnectionEvent,
  SessionLedgerPage,
  SessionLedgerQuery,
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

function applyClose(
  entries: SessionLedgerEntry[],
  entry: SessionLedgerEntry,
  filters: SessionLedgerFilters,
  at: Date,
): SessionLedgerEntry[] {
  if (!entries.some((row) => row.id === entry.id)) {
    return closeBelongsInView(entry, filters, at) ? [entry, ...entries] : entries;
  }
  return filters.scope === 'live'
    ? entries.filter((row) => row.id !== entry.id)
    : entries.map((row) => (row.id === entry.id ? entry : row));
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
  const [loadingMoreRead, setLoadingMoreRead] = useState<SessionLedgerEntry[] | null>(null);
  const [reloadNonce, setReloadNonce] = useState(0);
  const [lifecycle, setLifecycle] = useState({ connected: false, generation: 0 });
  const lifecycleRef = useRef(lifecycle);
  const readEpoch = useRef(0);
  const closesDuringReads = useRef(new Set<SessionLedgerEntry[]>());
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
          closesDuringReads.current.clear();
          setLoading(false);
          setLoadingMoreRead(null);
        }
        return;
      }
      if (!lifecycleRef.current.connected
        || event.connectionGeneration !== lifecycleRef.current.generation) return;
      const entry = event.entry;
      for (const closes of closesDuringReads.current) closes.push(entry);
      const at = now();
      setRead((current) => ({ ...current, entries: applyClose(current.entries, entry, filtersRef.current, at) }));
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
    closesDuringReads.current.clear();
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
    const closes: SessionLedgerEntry[] = [];
    closesDuringReads.current.add(closes);
    const superseded = () => epoch !== readEpoch.current || generation !== lifecycleRef.current.generation;
    setLoading(true);
    setRead((current) => (current.query === queryKey ? { ...current, error: null } : current));
    connection.list({ ...(query as SessionLedgerQuery), limit: pageSize })
      .then((page) => {
        if (superseded()) return;
        const at = now();
        const listed = closes.reduce((next, entry) => applyClose(next, entry, filters, at), page.entries ?? []);
        setRead({ query: queryKey, entries: listed, facets: page.facets ?? null, error: null });
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
        closesDuringReads.current.delete(closes);
        if (epoch === readEpoch.current) setLoading(false);
      });
    return () => {
      if (readEpoch.current === epoch) readEpoch.current += 1;
      closesDuringReads.current.clear();
    };
  }, [enabled, filters, query, queryKey, filterError, connection.list, lifecycle, pageSize, reloadNonce, now]);

  const reload = useCallback(() => setReloadNonce((n) => n + 1), []);

  const loadMore = useCallback(() => {
    if (!nextBefore || loading || loadingMore || filterError || !lifecycleRef.current.connected) return;
    const epoch = readEpoch.current;
    const generation = lifecycleRef.current.generation;
    const closes: SessionLedgerEntry[] = [];
    closesDuringReads.current.add(closes);
    const superseded = () => epoch !== readEpoch.current || generation !== lifecycleRef.current.generation;
    setLoadingMoreRead(closes);
    connection.list({ ...(sessionLedgerQuery(filtersRef.current, now()) as SessionLedgerQuery), limit: pageSize, before: nextBefore })
      .then((page) => {
        if (superseded()) return;
        const at = now();
        setRead((current) => {
          const present = new Set(current.entries.map((entry) => entry.id));
          const appended = [...current.entries, ...(page.entries ?? []).filter((entry) => !present.has(entry.id))];
          return { ...current, entries: closes.reduce((next, entry) => applyClose(next, entry, filtersRef.current, at), appended) };
        });
        setOmitted(page.omitted ?? 0);
        setNextBefore(page.next_before ?? null);
      })
      .catch((failure: Error) => {
        if (epoch === readEpoch.current) setRead((current) => ({ ...current, error: failure.message }));
      })
      .finally(() => {
        closesDuringReads.current.delete(closes);
        setLoadingMoreRead((current) => current === closes ? null : current);
      });
  }, [nextBefore, loading, loadingMore, filterError, connection.list, pageSize, now]);

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
