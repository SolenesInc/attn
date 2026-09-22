import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import type { SessionLedgerEntry, SessionLedgerFacets, SessionReopen } from '../types/generated';
import type {
  SessionLedgerConnectionEvent,
  SessionLedgerPage,
  SessionLedgerQuery,
  SessionReopenResolutionEvent,
  SessionLedgerUpdate,
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

export type ReopenResolution =
  | { closedAt: string; state: 'pending' }
  | { closedAt: string; state: 'ready'; reopen: SessionReopen }
  | { closedAt: string; state: 'failed'; error: string };

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
  connected: boolean;
  generation: number;
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

function pendingResolutions(entries: SessionLedgerEntry[]): Record<string, ReopenResolution> {
  const next: Record<string, ReopenResolution> = {};
  for (const entry of entries) {
    if (entry.closed_at) next[entry.id] = { closedAt: entry.closed_at, state: 'pending' };
  }
  return next;
}

function eventResolution(event: SessionReopenResolutionEvent): ReopenResolution {
  return event.success && event.reopen
    ? { closedAt: event.closedAt, state: 'ready', reopen: event.reopen }
    : { closedAt: event.closedAt, state: 'failed', error: event.error ?? 'Eligibility could not be checked' };
}

function applyUpdateToEntries(
  entries: SessionLedgerEntry[],
  update: SessionLedgerUpdate,
  filters: SessionLedgerFilters,
  at: Date,
): SessionLedgerEntry[] {
  if (update.type !== 'closed') return entries;
  const entry = update.entry;
  const dropsFromView = filters.scope === 'live';
  const belongs = closeBelongsInView(entry, filters, at);
  const existing = entries.findIndex((row) => row.id === entry.id);
  if (existing >= 0) {
    const next = entries.slice();
    next[existing] = entry;
    return dropsFromView ? next.filter((row) => row.id !== entry.id) : next;
  }
  return belongs ? [entry, ...entries] : entries;
}

function applyUpdateToResolutions(
  resolutions: Record<string, ReopenResolution>,
  update: SessionLedgerUpdate,
  filters: SessionLedgerFilters,
): Record<string, ReopenResolution> {
  if (update.type === 'reopen-resolved') {
    const event = update.resolution;
    if (resolutions[event.sessionId]?.closedAt !== event.closedAt) return resolutions;
    return { ...resolutions, [event.sessionId]: eventResolution(event) };
  }
  const entry = update.entry;
  const next = { ...resolutions };
  // Entries keep a listed row even when its close falls outside the range, so it must still settle.
  if (filters.scope === 'live' || !entry.closed_at) delete next[entry.id];
  else next[entry.id] = { closedAt: entry.closed_at, state: 'pending' };
  return next;
}

function failPendingResolutions(
  resolutions: Record<string, ReopenResolution>,
  error: string,
): Record<string, ReopenResolution> {
  const next: Record<string, ReopenResolution> = {};
  for (const [id, resolution] of Object.entries(resolutions)) {
    next[id] = resolution.state === 'pending' ? { closedAt: resolution.closedAt, state: 'failed', error } : resolution;
  }
  return next;
}

interface ActiveRead {
  epoch: number;
  generation: number;
  updates: SessionLedgerUpdate[];
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
  const [entries, setEntries] = useState<SessionLedgerEntry[]>([]);
  const [resolutions, setResolutions] = useState<Record<string, ReopenResolution>>({});
  const [facets, setFacets] = useState<SessionLedgerFacets | null>(null);
  const [omitted, setOmitted] = useState(0);
  const [nextBefore, setNextBefore] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadingMoreRequest, setLoadingMoreRequest] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [reloadNonce, setReloadNonce] = useState(0);
  const [lifecycle, setLifecycle] = useState({
    connected: connection.connected,
    generation: connection.generation,
  });
  const lifecycleRef = useRef(lifecycle);
  const entriesRef = useRef(entries);
  const readEpoch = useRef(0);
  const requestSequence = useRef(0);
  const activeReads = useRef(new Map<number, ActiveRead>());
  const loadingMore = loadingMoreRequest !== null;

  useEffect(() => {
    entriesRef.current = entries;
  }, [entries]);
  const filtersRef = useRef(filters);
  useEffect(() => {
    filtersRef.current = filters;
  }, [filters]);

  const markVisibleEligibilityPending = useCallback(() => {
    setResolutions(pendingResolutions(entriesRef.current));
  }, []);

  useEffect(() => {
    const next = { connected: connection.connected, generation: connection.generation };
    lifecycleRef.current = next;
    setLifecycle((current) => current.connected === next.connected && current.generation === next.generation
      ? current
      : next);
  }, [connection.connected, connection.generation]);

  useEffect(() => {
    if (!enabled) return;
    return connection.subscribe((event) => {
      if (event.type === 'connection') {
        const next = { connected: event.connected, generation: event.connectionGeneration };
        lifecycleRef.current = next;
        setLifecycle(next);
        if (!event.connected) {
          readEpoch.current += 1;
          activeReads.current.clear();
          setLoading(false);
          setLoadingMoreRequest(null);
          markVisibleEligibilityPending();
        }
        return;
      }
      if (!lifecycleRef.current.connected
        || event.connectionGeneration !== lifecycleRef.current.generation) return;
      const update: SessionLedgerUpdate = event.type === 'closed'
        ? { type: 'closed', entry: event.entry }
        : { type: 'reopen-resolved', resolution: event.resolution };
      for (const read of activeReads.current.values()) {
        if (read.epoch === readEpoch.current && read.generation === event.connectionGeneration) {
          read.updates.push(update);
        }
      }
      const at = now();
      setEntries((current) => applyUpdateToEntries(current, update, filtersRef.current, at));
      setResolutions((current) => applyUpdateToResolutions(current, update, filtersRef.current));
    });
  }, [connection.subscribe, enabled, markVisibleEligibilityPending, now]);

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
    activeReads.current.clear();
    setLoadingMoreRequest(null);
    setNextBefore(null);
    setOmitted(0);
    if (!enabled || filterError || !lifecycle.connected) {
      setLoading(false);
      return;
    }
    markVisibleEligibilityPending();
    const request = ++requestSequence.current;
    const read: ActiveRead = { epoch, generation: lifecycle.generation, updates: [] };
    activeReads.current.set(request, read);
    setLoading(true);
    setError(null);
    connection.list({ ...(query as SessionLedgerQuery), limit: pageSize, reopen: true })
      .then((page) => {
        if (epoch !== readEpoch.current || read.generation !== lifecycleRef.current.generation) return;
        const at = now();
        let nextEntries = page.entries ?? [];
        let nextResolutions = pendingResolutions(nextEntries);
        for (const update of read.updates) {
          nextEntries = applyUpdateToEntries(nextEntries, update, filters, at);
          nextResolutions = applyUpdateToResolutions(nextResolutions, update, filters);
        }
        entriesRef.current = nextEntries;
        setEntries(nextEntries);
        setResolutions(nextResolutions);
        setFacets(page.facets ?? null);
        setOmitted(page.omitted ?? 0);
        setNextBefore(page.next_before ?? null);
      })
      .catch((failure: Error) => {
        if (epoch !== readEpoch.current || read.generation !== lifecycleRef.current.generation) return;
        setError(failure.message);
        // No verdict is coming for these rows; a pending row would spin until reload.
        setResolutions((current) => failPendingResolutions(current, failure.message));
      })
      .finally(() => {
        activeReads.current.delete(request);
        if (epoch === readEpoch.current) setLoading(false);
      });
    return () => {
      if (readEpoch.current === epoch) readEpoch.current += 1;
      activeReads.current.clear();
    };
  }, [enabled, filters, query, filterError, connection.list, lifecycle, pageSize, reloadNonce, markVisibleEligibilityPending, now]);

  const reload = useCallback(() => {
    readEpoch.current += 1;
    activeReads.current.clear();
    setLoadingMoreRequest(null);
    setReloadNonce((n) => n + 1);
  }, []);

  const loadMore = useCallback(() => {
    if (!nextBefore || loading || loadingMore || filterError || !lifecycleRef.current.connected) return;
    const epoch = readEpoch.current;
    const request = ++requestSequence.current;
    const read: ActiveRead = { epoch, generation: lifecycleRef.current.generation, updates: [] };
    activeReads.current.set(request, read);
    setLoadingMoreRequest(request);
    connection.list({ ...(sessionLedgerQuery(filtersRef.current, now()) as SessionLedgerQuery), limit: pageSize, before: nextBefore, reopen: true })
      .then((page) => {
        if (epoch !== readEpoch.current || read.generation !== lifecycleRef.current.generation) return;
        const at = now();
        setEntries((current) => {
          const present = new Set(current.map((entry) => entry.id));
          let next = [...current, ...(page.entries ?? []).filter((entry) => !present.has(entry.id))];
          for (const update of read.updates) next = applyUpdateToEntries(next, update, filtersRef.current, at);
          entriesRef.current = next;
          return next;
        });
        setResolutions((current) => {
          let next = { ...current, ...pendingResolutions(page.entries ?? []) };
          for (const update of read.updates) next = applyUpdateToResolutions(next, update, filtersRef.current);
          return next;
        });
        setOmitted(page.omitted ?? 0);
        setNextBefore(page.next_before ?? null);
      })
      .catch((failure: Error) => {
        if (epoch === readEpoch.current) setError(failure.message);
      })
      .finally(() => {
        activeReads.current.delete(request);
        setLoadingMoreRequest((current) => current === request ? null : current);
      });
  }, [nextBefore, loading, loadingMore, filterError, connection.list, pageSize, now]);

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
  };
}
