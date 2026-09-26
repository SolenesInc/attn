import type { SessionLedgerFilters } from '../../hooks/useSessionLedger';
import type { SessionLedgerFacets } from '../../types/generated';
import type { SessionRangeId } from '../sessionsLedger';
import { tildePath } from './ledgerTime';

// Grammar: `repo:attn profile:name 7d from:… to:… dir:… words`; `dir:` and words narrow the loaded page only.
export interface ParsedQuery {
  filters: Omit<SessionLedgerFilters, 'scope'>;
  dir: string;
  words: string[];
  unresolved: string[];
}

export interface ProfileChoice {
  profile_id: string;
  name: string;
  deleted?: boolean;
}

export function profileChoices(
  profileNames: Record<string, string>,
  facets: SessionLedgerFacets | null,
  chosen: ProfileChoice | null = null,
): ProfileChoice[] {
  const live: ProfileChoice[] = Object.entries(profileNames).map(([profile_id, name]) => ({ profile_id, name }));
  const historical = (facets?.profiles ?? []).filter((facet) => !(facet.profile_id in profileNames));
  const known = [...live, ...historical];
  return chosen && !known.some((choice) => choice.profile_id === chosen.profile_id) ? [...known, chosen] : known;
}

const RANGE_WORDS: Record<string, SessionRangeId> = {
  today: 'today', yesterday: 'yesterday', '7d': '7d', '30d': '30d', week: '7d', month: '30d',
};

export function parseQuery(
  text: string,
  facets: SessionLedgerFacets | null,
  profiles: ProfileChoice[],
  preferredRepository = '',
): ParsedQuery {
  const filters: ParsedQuery['filters'] = { range: 'any', customFrom: '', customTo: '', profileId: '', repository: '' };
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
    } else if (key === 'repo' || key === 'repo-path') {
      const repository = key === 'repo-path' ? repositoryTokenValue(value) : value;
      if (key === 'repo-path') {
        filters.repository = repository;
        continue;
      }
      const repositories = facets?.repositories ?? [];
      const exact = repositories.find((facet) => facet.value === repository)?.value;
      const preferred = baseName(preferredRepository).toLowerCase() === repository.toLowerCase()
        ? preferredRepository
        : '';
      const named = repositories.filter((facet) => baseName(facet.value).toLowerCase() === repository.toLowerCase());
      const match = exact
        || preferred
        || (named.length === 1 ? named[0].value : '');
      if (match) filters.repository = match; else unresolved.push(token);
    } else if (key === 'profile') {
      const match = resolveProfileToken(profiles, value);
      if (match) filters.profileId = match; else unresolved.push(token);
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
  profileNames: Record<string, string>,
): string {
  const tokens: string[] = [];
  if (filters.repository) tokens.push(`repo:${baseName(filters.repository)}`);
  if (filters.profileId) tokens.push(`profile:${profileToken(filters.profileId, profileNames)}`);
  if (filters.range === 'custom') {
    if (filters.customFrom) tokens.push(`from:${filters.customFrom}`);
    if (filters.customTo) tokens.push(`to:${filters.customTo}`);
  } else if (filters.range !== 'any') {
    tokens.push(filters.range);
  }
  return tokens.join(' ');
}

function resolveProfileToken(profiles: ProfileChoice[], value: string): string {
  if (profiles.some((choice) => choice.profile_id === value)) return value;
  const namesakes = profiles.filter((choice) => nameToken(choice.name) === value.toLowerCase());
  return onlyProfileOf(namesakes) || onlyProfileOf(namesakes.filter((choice) => !choice.deleted));
}

function onlyProfileOf(profiles: ProfileChoice[]): string {
  return profiles.length === 1 ? profiles[0].profile_id : '';
}

function profileToken(profileId: string, profileNames: Record<string, string>): string {
  const name = profileNames[profileId];
  if (!name) return profileId;
  const token = nameToken(name);
  const shared = Object.entries(profileNames).some(([id, other]) => id !== profileId && nameToken(other) === token);
  return shared ? profileId : token;
}

export function renameProfileTokens(text: string, before: Record<string, string>, after: Record<string, string>): string {
  const livedBefore = profileChoices(before, null);
  return text.split(/(\s+)/).map((part) => {
    if (!/^profile:/i.test(part)) return part;
    const profileId = resolveProfileToken(livedBefore, part.slice('profile:'.length));
    return profileId && profileId in after ? `profile:${profileToken(profileId, after)}` : part;
  }).join('');
}

function nameToken(label: string): string {
  return label.trim().replace(/\s+/g, '-').toLowerCase();
}

export function baseName(path: string): string {
  const trimmed = path.replace(/\/+$/, '');
  const cut = trimmed.lastIndexOf('/');
  return cut < 0 ? trimmed : trimmed.slice(cut + 1);
}

export function repositoryQueryToken(repository: string): string {
  return `repo-path:${encodeURIComponent(repository)}`;
}

function repositoryTokenValue(value: string): string {
  return new URLSearchParams(`repository=${value}`).get('repository') ?? '';
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
