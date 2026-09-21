import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import type { SessionLedgerEntry, SessionLedgerFacets, SessionReopen } from '../types/generated';
import type {
  SessionLedgerPage,
  SessionLedgerQuery,
  SessionReopenResolutionEvent,
} from './daemonSessionLedgerEvents';
import {
  customSessionRange,
  isRangeError,
  ledgerInstant,
  reopenVerdictView,
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
const EARLY_RESOLUTION_LIMIT = 100;

export type ReopenResolution =
  | { closedAt: string; state: 'pending' }
  | { closedAt: string; state: 'ready'; reopen: SessionReopen }
  | { closedAt: string; state: 'failed'; error: string };

export interface UseSessionLedgerOptions {
  enabled: boolean;
  list: (query: SessionLedgerQuery) => Promise<SessionLedgerPage>;
  connectionGeneration?: number;
  pageSize?: number;
  now?: () => Date;
  initialFilters?: SessionLedgerFilters;
  onFiltersChange?: (filters: SessionLedgerFilters) => void;
}

export interface SessionLedgerView {
  filters: SessionLedgerFilters;
  setFilters: Dispatch<SetStateAction<SessionLedgerFilters>>;
  entries: SessionLedgerEntry[];
  verdicts: Record<string, ReopenVerdictView>;
  resolutions: Record<string, ReopenResolution>;
  facets: SessionLedgerFacets | null;
  omitted: number;
  loading: boolean;
  loadingMore: boolean;
  error: string | null;
  filterError: string | null;
  reload: () => void;
  loadMore: () => void;
  recordClose: (entry: SessionLedgerEntry) => void;
  recordResolution: (resolution: SessionReopenResolutionEvent) => void;
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
  connectionGeneration = 0,
  pageSize = SESSION_PAGE_SIZE,
  now = systemNow,
  initialFilters = EMPTY_SESSION_FILTERS,
  onFiltersChange,
}: UseSessionLedgerOptions): SessionLedgerView {
  const [filters, setFilters] = useState<SessionLedgerFilters>(initialFilters);
  const [entries, setEntries] = useState<SessionLedgerEntry[]>([]);
  const [resolutions, setResolutions] = useState<Record<string, ReopenResolution>>({});
  const [facets, setFacets] = useState<SessionLedgerFacets | null>(null);
  const [omitted, setOmitted] = useState(0);
  const [nextBefore, setNextBefore] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadingMoreSeq, setLoadingMoreSeq] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [reloadNonce, setReloadNonce] = useState(0);
  const readSeq = useRef(0);
  const loadingMore = loadingMoreSeq === readSeq.current;
  const early = useRef(new Map<string, ReopenResolution>());
  useEffect(() => {
    if (!enabled) early.current.clear();
  }, [enabled]);

  const resolutionForEntry = useCallback((entry: SessionLedgerEntry): ReopenResolution | null => {
    const closedAt = entry.closed_at ?? '';
    if (!closedAt) return null;
    const key = `${entry.id}\u0000${closedAt}`;
    const arrived = early.current.get(key);
    if (arrived) {
      early.current.delete(key);
      return arrived;
    }
    return { closedAt, state: 'pending' };
  }, []);

  const replacePageResolutions = useCallback((pageEntries: SessionLedgerEntry[]) => {
    const next: Record<string, ReopenResolution> = {};
    for (const entry of pageEntries) {
      const resolution = resolutionForEntry(entry);
      if (resolution) next[entry.id] = resolution;
    }
    setResolutions(next);
  }, [resolutionForEntry]);

  const appendPageResolutions = useCallback((pageEntries: SessionLedgerEntry[]) => {
    const additions: Record<string, ReopenResolution | null> = {};
    for (const entry of pageEntries) additions[entry.id] = resolutionForEntry(entry);
    setResolutions((current) => {
      const next = { ...current };
      for (const entry of pageEntries) {
        const resolution = additions[entry.id];
        if (resolution) next[entry.id] = resolution;
        else delete next[entry.id];
      }
      return next;
    });
  }, [resolutionForEntry]);
  // Written after commit: a render React discards must not steer the committed surface.
  const filtersRef = useRef(filters);
  useEffect(() => {
    filtersRef.current = filters;
  }, [filters]);

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
        replacePageResolutions(page.entries ?? []);
        setFacets(page.facets ?? null);
        setOmitted(page.omitted ?? 0);
        setNextBefore(page.next_before ?? null);
      })
      .catch((failure: Error) => {
        if (seq !== readSeq.current) return;
        setEntries([]);
        setResolutions({});
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
  }, [enabled, filters, filterError, list, connectionGeneration, pageSize, replacePageResolutions, reloadNonce]);

  const reload = useCallback(() => {
    early.current.clear();
    setReloadNonce((n) => n + 1);
  }, []);

  const loadMore = useCallback(() => {
    if (!nextBefore || loadingMore || filterError) return;
    const seq = readSeq.current;
    setLoadingMoreSeq(seq);
    list({ ...(sessionLedgerQuery(filtersRef.current, now()) as SessionLedgerQuery), limit: pageSize, before: nextBefore, reopen: true })
      .then((page) => {
        if (seq !== readSeq.current) return;
        setEntries((current) => [...current, ...(page.entries ?? [])]);
        appendPageResolutions(page.entries ?? []);
        setOmitted(page.omitted ?? 0);
        setNextBefore(page.next_before ?? null);
      })
      .catch((failure: Error) => {
        if (seq === readSeq.current) setError(failure.message);
      })
      .finally(() => {
        if (seq === readSeq.current) setLoadingMoreSeq(null);
      });
  }, [nextBefore, loadingMore, filterError, list, pageSize, appendPageResolutions, now]);

  const recordClose = useCallback((entry: SessionLedgerEntry) => {
    // Read outside the updater: React may replay one, and the clock would move under it.
    const dropsFromView = filtersRef.current.scope === 'live';
    const belongs = closeBelongsInView(entry, filtersRef.current, now());
    const resolution = resolutionForEntry(entry);
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
    setResolutions((current) => {
      const next = { ...current };
      if (dropsFromView || !belongs || !entry.closed_at) delete next[entry.id];
      else next[entry.id] = resolution ?? { closedAt: entry.closed_at, state: 'pending' };
      return next;
    });
  }, [now, resolutionForEntry]);

  const recordResolution = useCallback((event: SessionReopenResolutionEvent) => {
    const resolution: ReopenResolution = event.success && event.reopen
      ? { closedAt: event.closedAt, state: 'ready', reopen: event.reopen }
      : { closedAt: event.closedAt, state: 'failed', error: event.error ?? 'Eligibility could not be checked' };
    const key = `${event.sessionId}\u0000${event.closedAt}`;
    early.current.delete(key);
    early.current.set(key, resolution);
    while (early.current.size > EARLY_RESOLUTION_LIMIT) {
      const oldest = early.current.keys().next().value;
      if (typeof oldest !== 'string') break;
      early.current.delete(oldest);
    }
    setResolutions((current) => {
      if (current[event.sessionId]?.closedAt !== event.closedAt) return current;
      return { ...current, [event.sessionId]: resolution };
    });
  }, []);

  const verdicts = useMemo(() => {
    const next: Record<string, ReopenVerdictView> = {};
    for (const [sessionId, resolution] of Object.entries(resolutions)) {
      if (resolution.state === 'ready') next[sessionId] = reopenVerdictView(resolution.reopen);
    }
    return Object.keys(next).length === 0 ? NO_VERDICTS : next;
  }, [resolutions]);

  return {
    filters,
    setFilters,
    entries,
    verdicts,
    resolutions,
    facets,
    omitted,
    loading,
    loadingMore,
    error,
    filterError,
    reload,
    loadMore,
    recordClose,
    recordResolution,
  };
}
