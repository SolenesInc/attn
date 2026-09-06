import { EMPTY_SESSION_FILTERS } from './useSessionLedger';
import type { SessionLedgerFilters } from './useSessionLedger';
import { SESSION_RANGE_CHOICES } from '../components/sessionsLedger';
import type { SessionRangeId, SessionScope } from '../components/sessionsLedger';

export const SESSION_FILTERS_SETTING_KEY = 'sessions.filters';

const SCOPES: SessionScope[] = ['live', 'closed', 'all'];
const DAY = /^\d{4}-\d{2}-\d{2}$/;

export function serializeSessionFilters(filters: SessionLedgerFilters): string {
  return JSON.stringify({
    scope: filters.scope,
    range: filters.range,
    customFrom: filters.customFrom,
    customTo: filters.customTo,
    workspaceId: filters.workspaceId,
    repository: filters.repository,
  });
}

export function parseSessionFilters(raw: string | undefined): SessionLedgerFilters {
  if (!raw || !raw.trim()) return EMPTY_SESSION_FILTERS;
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return EMPTY_SESSION_FILTERS;
  }
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return EMPTY_SESSION_FILTERS;
  const stored = value as Record<string, unknown>;
  const scope = stored.scope as SessionScope;
  const range = stored.range as SessionRangeId;
  if (!SCOPES.includes(scope)) return EMPTY_SESSION_FILTERS;
  if (!SESSION_RANGE_CHOICES.some((choice) => choice.id === range)) return EMPTY_SESSION_FILTERS;
  const customFrom = day(stored.customFrom);
  const customTo = day(stored.customTo);
  if (customFrom === null || customTo === null) return EMPTY_SESSION_FILTERS;
  const workspaceId = text(stored.workspaceId);
  const repository = text(stored.repository);
  if (workspaceId === null || repository === null) return EMPTY_SESSION_FILTERS;
  return { scope, range, customFrom, customTo, workspaceId, repository };
}

function day(value: unknown): string | null {
  if (value === undefined || value === '') return '';
  if (typeof value !== 'string' || !DAY.test(value)) return null;
  return value;
}

function text(value: unknown): string | null {
  if (value === undefined) return '';
  return typeof value === 'string' ? value : null;
}
