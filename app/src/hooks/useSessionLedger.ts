import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import type { SessionLedgerEntry, SessionLedgerFacets } from '../types/generated';
import type {
  SessionLedgerConnectionEvent,
  SessionLedgerPage,
  SessionLedgerQuery,
  SettledReopenResolution,
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

export type ReopenResolution = { closedAt: string; state: 'pending' } | SettledReopenResolution;

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
  resolutions: Record<string, ReopenResolution>;
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

type SettledOutcomes = Record<string, SettledReopenResolution>;

const NO_OUTCOMES: SettledOutcomes = {};

function outcomeKey(sessionId: string, closedAt: string): string {
  return `${sessionId}\u0000${closedAt}`;
}

function applyClose(
  entries: SessionLedgerEntry[],
  entry: SessionLedgerEntry,
  filters: SessionLedgerFilters,
  at: Date,
): SessionLedgerEntry[] {
  const dropsFromView = filters.scope === 'live';
  const existing = entries.findIndex((row) => row.id === entry.id);
  if (existing >= 0) {
    const next = entries.slice();
    next[existing] = entry;
    return dropsFromView ? next.filter((row) => row.id !== entry.id) : next;
  }
  return closeBelongsInView(entry, filters, at) ? [entry, ...entries] : entries;
}

interface LedgerRows {
  entries: SessionLedgerEntry[];
  outcomes: SettledOutcomes;
}

const NO_ROWS: LedgerRows = { entries: [], outcomes: NO_OUTCOMES };

function outcomesForListedRows(entries: SessionLedgerEntry[], outcomes: SettledOutcomes): SettledOutcomes {
  const kept: SettledOutcomes = {};
  for (const entry of entries) {
    if (!entry.closed_at) continue;
    const key = outcomeKey(entry.id, entry.closed_at);
    const outcome = outcomes[key];
    if (outcome) kept[key] = outcome;
  }
  return kept;
}

function listRows(entries: SessionLedgerEntry[], outcomes: SettledOutcomes): LedgerRows {
  return { entries, outcomes: outcomesForListedRows(entries, outcomes) };
}

function isListedGeneration(entries: SessionLedgerEntry[], sessionId: string, closedAt: string): boolean {
  return entries.some((entry) => entry.id === sessionId && entry.closed_at === closedAt);
}

function resolutionsForEntries(
  entries: SessionLedgerEntry[],
  outcomes: SettledOutcomes,
  readFailure: string | null,
): Record<string, ReopenResolution> {
  const resolutions: Record<string, ReopenResolution> = {};
  for (const entry of entries) {
    const closedAt = entry.closed_at;
    if (!closedAt) continue;
    resolutions[entry.id] = outcomes[outcomeKey(entry.id, closedAt)]
      ?? (readFailure === null
        ? { closedAt, state: 'pending' }
        : { closedAt, state: 'failed', error: readFailure });
  }
  return resolutions;
}

export function useSessionLedger({
  enabled,
  connection,
  pageSize = SESSION_PAGE_SIZE,
  now = systemNow,
  initialFilters = EMPTY_SESSION_FILTERS,
  onFiltersChange,
}: UseSessionLedgerOptions): SessionLedgerView {
  const [filters, setFilters] = useState<SessionLedgerFilters>(initialFilters);
  const [{ entries, outcomes }, setRows] = useState<LedgerRows>(NO_ROWS);
  const [readFailure, setReadFailure] = useState<string | null>(null);
  const [facets, setFacets] = useState<SessionLedgerFacets | null>(null);
  const [omitted, setOmitted] = useState(0);
  const [nextBefore, setNextBefore] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadingMoreRead, setLoadingMoreRead] = useState<SessionLedgerEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
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
      if (event.type === 'reopen-resolved') {
        const { sessionId, resolution } = event;
        const pageMayListIt = closesDuringReads.current.size > 0;
        setRows((current) => pageMayListIt || isListedGeneration(current.entries, sessionId, resolution.closedAt)
          ? { ...current, outcomes: { ...current.outcomes, [outcomeKey(sessionId, resolution.closedAt)]: resolution } }
          : current);
        return;
      }
      const entry = event.entry;
      for (const closes of closesDuringReads.current) closes.push(entry);
      const at = now();
      setRows((current) => listRows(applyClose(current.entries, entry, filtersRef.current, at), current.outcomes));
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
    setRows((current) => ({ ...current, outcomes: NO_OUTCOMES }));
    setReadFailure(null);
    if (!lifecycle.connected) {
      setLoading(false);
      return;
    }
    const generation = lifecycle.generation;
    const closes: SessionLedgerEntry[] = [];
    closesDuringReads.current.add(closes);
    const superseded = () => epoch !== readEpoch.current || generation !== lifecycleRef.current.generation;
    setLoading(true);
    setError(null);
    connection.list({ ...(query as SessionLedgerQuery), limit: pageSize, reopen: true })
      .then((page) => {
        if (superseded()) return;
        const at = now();
        const listed = closes.reduce((next, entry) => applyClose(next, entry, filters, at), page.entries ?? []);
        setRows((current) => listRows(listed, current.outcomes));
        setFacets(page.facets ?? null);
        setOmitted(page.omitted ?? 0);
        setNextBefore(page.next_before ?? null);
      })
      .catch((failure: Error) => {
        if (superseded()) return;
        setError(failure.message);
        setReadFailure(failure.message);
      })
      .finally(() => {
        closesDuringReads.current.delete(closes);
        if (epoch === readEpoch.current) setLoading(false);
      });
    return () => {
      if (readEpoch.current === epoch) readEpoch.current += 1;
      closesDuringReads.current.clear();
    };
  }, [enabled, filters, query, filterError, connection.list, lifecycle, pageSize, reloadNonce, now]);

  const reload = useCallback(() => setReloadNonce((n) => n + 1), []);

  const loadMore = useCallback(() => {
    if (!nextBefore || loading || loadingMore || filterError || !lifecycleRef.current.connected) return;
    const epoch = readEpoch.current;
    const generation = lifecycleRef.current.generation;
    const closes: SessionLedgerEntry[] = [];
    closesDuringReads.current.add(closes);
    const superseded = () => epoch !== readEpoch.current || generation !== lifecycleRef.current.generation;
    setLoadingMoreRead(closes);
    connection.list({ ...(sessionLedgerQuery(filtersRef.current, now()) as SessionLedgerQuery), limit: pageSize, before: nextBefore, reopen: true })
      .then((page) => {
        if (superseded()) return;
        const at = now();
        setRows((current) => {
          const present = new Set(current.entries.map((entry) => entry.id));
          const appended = [...current.entries, ...(page.entries ?? []).filter((entry) => !present.has(entry.id))];
          return listRows(
            closes.reduce((next, entry) => applyClose(next, entry, filtersRef.current, at), appended),
            current.outcomes,
          );
        });
        setOmitted(page.omitted ?? 0);
        setNextBefore(page.next_before ?? null);
      })
      .catch((failure: Error) => {
        if (epoch === readEpoch.current) setError(failure.message);
      })
      .finally(() => {
        closesDuringReads.current.delete(closes);
        setLoadingMoreRead((current) => current === closes ? null : current);
      });
  }, [nextBefore, loading, loadingMore, filterError, connection.list, pageSize, now]);

  const resolutions = useMemo(
    () => resolutionsForEntries(entries, outcomes, readFailure),
    [entries, outcomes, readFailure],
  );

  return {
    filters,
    setFilters,
    entries,
    resolutions,
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
