import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import type { SessionLedgerEntry, SessionLedgerFacets, SessionReopen } from '../types/generated';
import type { SessionLedgerPage, SessionLedgerQuery } from './daemonSessionLedgerEvents';
import {
  customSessionRange,
  isRangeError,
  ledgerInstant,
  reopenVerdictView,
  reopenVerdictsById,
  sessionRangeWindow,
} from '../components/sessionsLedger';
import type { ReopenVerdictView, SessionRangeId, SessionScope } from '../components/sessionsLedger';

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

const NO_VERDICTS: Record<string, ReopenVerdictView> = {};

export interface UseSessionLedgerOptions {
  enabled: boolean;
  list: (query: SessionLedgerQuery) => Promise<SessionLedgerPage>;
  pageSize?: number;
  now?: () => Date;
  /** Read once, on the first render, so the first query is already the restored one. */
  initialFilters?: SessionLedgerFilters;
  onFiltersChange?: (filters: SessionLedgerFilters) => void;
}

export interface SessionLedgerView {
  filters: SessionLedgerFilters;
  setFilters: Dispatch<SetStateAction<SessionLedgerFilters>>;
  entries: SessionLedgerEntry[];
  verdicts: Record<string, ReopenVerdictView>;
  facets: SessionLedgerFacets | null;
  omitted: number;
  loading: boolean;
  loadingMore: boolean;
  error: string | null;
  filterError: string | null;
  reload: () => void;
  loadMore: () => void;
  recordClose: (entry: SessionLedgerEntry, reopen?: SessionReopen) => void;
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

export function useSessionLedger({
  enabled,
  list,
  pageSize = SESSION_PAGE_SIZE,
  now = systemNow,
  initialFilters = EMPTY_SESSION_FILTERS,
  onFiltersChange,
}: UseSessionLedgerOptions): SessionLedgerView {
  const [filters, setFilters] = useState<SessionLedgerFilters>(initialFilters);
  const [entries, setEntries] = useState<SessionLedgerEntry[]>([]);
  const [verdicts, setVerdicts] = useState<Record<string, ReopenVerdictView>>(NO_VERDICTS);
  const [facets, setFacets] = useState<SessionLedgerFacets | null>(null);
  const [omitted, setOmitted] = useState(0);
  const [nextBefore, setNextBefore] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [reloadNonce, setReloadNonce] = useState(0);
  const readSeq = useRef(0);
  // Written after commit: a render React discards must not steer the committed surface.
  const filtersRef = useRef(filters);
  useEffect(() => {
    filtersRef.current = filters;
  }, [filters]);

  // What the caller already knows about, so the restored filters are not written
  // straight back and an unchanged pick costs no round trip.
  const reportedRef = useRef(filters);
  useEffect(() => {
    if (sameFilters(filters, reportedRef.current)) return;
    reportedRef.current = filters;
    onFiltersChange?.(filters);
  }, [filters, onFiltersChange]);

  const query = useMemo(() => sessionLedgerQuery(filters, now()), [filters, now]);
  const filterError = 'error' in query ? query.error : null;

  useEffect(() => {
    if (!enabled || filterError) return;
    const seq = ++readSeq.current;
    setLoading(true);
    setError(null);
    list({ ...(query as SessionLedgerQuery), limit: pageSize, reopen: true })
      .then((page) => {
        if (seq !== readSeq.current) return;
        setEntries(page.entries ?? []);
        setVerdicts(reopenVerdictsById(page.reopen));
        setFacets(page.facets ?? null);
        setOmitted(page.omitted ?? 0);
        setNextBefore(page.next_before ?? null);
      })
      .catch((failure: Error) => {
        if (seq !== readSeq.current) return;
        setEntries([]);
        setVerdicts(NO_VERDICTS);
        setFacets(null);
        setOmitted(0);
        setNextBefore(null);
        setError(failure.message);
      })
      .finally(() => {
        if (seq === readSeq.current) setLoading(false);
      });
    // `query` holds a fresh `now`, so depending on it would refetch every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, filters, filterError, list, pageSize, reloadNonce]);

  const reload = useCallback(() => setReloadNonce((n) => n + 1), []);

  const loadMore = useCallback(() => {
    if (!nextBefore || loadingMore || filterError) return;
    const seq = readSeq.current;
    setLoadingMore(true);
    list({ ...(sessionLedgerQuery(filtersRef.current, now()) as SessionLedgerQuery), limit: pageSize, before: nextBefore, reopen: true })
      .then((page) => {
        if (seq !== readSeq.current) return;
        setEntries((current) => [...current, ...(page.entries ?? [])]);
        setVerdicts((current) => ({ ...current, ...reopenVerdictsById(page.reopen) }));
        setOmitted(page.omitted ?? 0);
        setNextBefore(page.next_before ?? null);
      })
      .catch((failure: Error) => {
        if (seq === readSeq.current) setError(failure.message);
      })
      .finally(() => {
        if (seq === readSeq.current) setLoadingMore(false);
      });
  }, [nextBefore, loadingMore, filterError, list, pageSize, now]);

  const recordClose = useCallback((entry: SessionLedgerEntry, reopen?: SessionReopen) => {
    // Read outside the updater: React may replay one, and the clock would move under it.
    const dropsFromView = filtersRef.current.scope === 'live';
    const belongs = closeBelongsInView(entry, filtersRef.current, now());
    setEntries((current) => {
      const at = current.findIndex((row) => row.id === entry.id);
      if (at >= 0) {
        const next = current.slice();
        next[at] = entry;
        return dropsFromView ? next.filter((row) => row.id !== entry.id) : next;
      }
      if (!belongs) return current;
      return [entry, ...current];
    });
    if (reopen) setVerdicts((current) => ({ ...current, [entry.id]: reopenVerdictView(reopen) }));
  }, [now]);

  return {
    filters,
    setFilters,
    entries,
    verdicts,
    facets,
    omitted,
    loading,
    loadingMore,
    error,
    filterError,
    reload,
    loadMore,
    recordClose,
  };
}
