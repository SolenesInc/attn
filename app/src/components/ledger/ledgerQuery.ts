import type { SessionLedgerFilters } from '../../hooks/useSessionLedger';
import type { SessionLedgerFacets } from '../../types/generated';
import type { SessionRangeId } from '../sessionsLedger';
import { tildePath } from './ledgerTime';

// Grammar: `repo:attn ws:name 7d from:… to:… dir:… words`; `dir:` and words narrow the loaded page only.
export interface ParsedQuery {
  filters: Omit<SessionLedgerFilters, 'scope'>;
  dir: string;
  words: string[];
  unresolved: string[];
}

const RANGE_WORDS: Record<string, SessionRangeId> = {
  today: 'today', yesterday: 'yesterday', '7d': '7d', '30d': '30d', week: '7d', month: '30d',
};

export function parseQuery(
  text: string,
  facets: SessionLedgerFacets | null,
  workspaceLabel: (id: string) => string,
): ParsedQuery {
  const filters: ParsedQuery['filters'] = { range: 'any', customFrom: '', customTo: '', workspaceId: '', repository: '' };
  const words: string[] = [];
  const unresolved: string[] = [];
  let dir = '';
  for (const token of text.trim().split(/\s+/).filter(Boolean)) {
    const lower = token.toLowerCase();
    const colon = token.indexOf(':');
    const key = colon > 0 ? lower.slice(0, colon) : '';
    const value = colon > 0 ? token.slice(colon + 1) : '';
    if (RANGE_WORDS[lower]) {
      filters.range = RANGE_WORDS[lower];
    } else if (key === 'from' || key === 'to') {
      filters.range = 'custom';
      if (key === 'from') filters.customFrom = value; else filters.customTo = value;
    } else if (key === 'repo') {
      const match = (facets?.repositories ?? []).find((facet) =>
        facet.value === value || baseName(facet.value).toLowerCase() === value.toLowerCase());
      if (match) filters.repository = match.value; else unresolved.push(token);
    } else if (key === 'ws') {
      const match = (facets?.workspaces ?? []).find((facet) =>
        facet.value === value || wsToken(workspaceLabel(facet.value)) === value.toLowerCase());
      if (match) filters.workspaceId = match.value; else unresolved.push(token);
    } else if (key === 'dir') {
      dir = value;
    } else {
      words.push(lower);
    }
  }
  if (filters.range === 'custom' && !filters.customTo) filters.customTo = filters.customFrom;
  if (filters.range === 'custom' && !filters.customFrom) filters.customFrom = filters.customTo;
  return { filters, dir, words, unresolved };
}

export function formatQuery(
  filters: SessionLedgerFilters,
  workspaceLabel: (id: string) => string,
): string {
  const tokens: string[] = [];
  if (filters.repository) tokens.push(`repo:${baseName(filters.repository)}`);
  if (filters.workspaceId) tokens.push(`ws:${wsToken(workspaceLabel(filters.workspaceId))}`);
  if (filters.range === 'custom') {
    if (filters.customFrom) tokens.push(`from:${filters.customFrom}`);
    if (filters.customTo) tokens.push(`to:${filters.customTo}`);
  } else if (filters.range !== 'any') {
    tokens.push(filters.range);
  }
  return tokens.join(' ');
}

function wsToken(label: string): string {
  return label.trim().replace(/\s+/g, '-').toLowerCase();
}

export function baseName(path: string): string {
  const trimmed = path.replace(/\/+$/, '');
  const cut = trimmed.lastIndexOf('/');
  return cut < 0 ? trimmed : trimmed.slice(cut + 1);
}

export function matchesWords(haystack: string[], words: string[]): boolean {
  if (words.length === 0) return true;
  const joined = haystack.join(' ').toLowerCase();
  return words.every((word) => joined.includes(word));
}

// `dir:` is typed the way the row shows it (`~/…`) or pasted absolute; the directory answers to both.
export function matchesDir(directory: string, dir: string): boolean {
  const wanted = dir.replace(/\/+$/, '');
  if (!wanted) return true;
  return [directory, tildePath(directory)].some((form) => form === wanted || form.startsWith(`${wanted}/`));
}

export function removeToken(text: string, token: string): string {
  return text.split(/\s+/).filter((part) => part && part !== token).join(' ');
}
