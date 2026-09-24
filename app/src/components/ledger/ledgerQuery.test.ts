import { describe, expect, it } from 'vitest';
import { baseName, formatQuery, matchesDir, matchesWords, parseQuery, profileChoices, removeToken, renameProfileTokens, repositoryQueryToken } from './ledgerQuery';
import type { SessionLedgerFacets } from '../../types/generated';
import { nameIds, relativeStamp, shortPath, tildePath, untilStamp } from './ledgerTime';

const facets = {
  repositories: [{ value: '/Users/victor/projects/attn', count: 3 }],
  profiles: [
    { profile_id: 'profile-1', name: 'attn work', count: 2 },
    { profile_id: 'profile-old', name: 'Old Side', deleted: true, count: 1 },
  ],
};
const names = { 'profile-1': 'attn work' };
const historyOf = (ledgerFacets: SessionLedgerFacets | null) => profileChoices({}, ledgerFacets);

describe('parseQuery', () => {
  it('splits tokens into daemon filters, a directory, and words', () => {
    const parsed = parseQuery('repo:attn profile:attn-work 7d dir:~/x Ledger  reopen', facets, historyOf(facets));
    expect(parsed.filters).toEqual({ range: '7d', customFrom: '', customTo: '', profileId: 'profile-1', repository: '/Users/victor/projects/attn' });
    expect(parsed.dir).toBe('~/x');
    expect(parsed.words).toEqual(['ledger', 'reopen']);
    expect(parsed.unresolved).toEqual([]);
  });

  it('fills the missing end of a custom range with the other end', () => {
    expect(parseQuery('from:2026-09-01', null, historyOf(null)).filters).toMatchObject({ range: 'custom', customFrom: '2026-09-01', customTo: '2026-09-01' });
    expect(parseQuery('to:2026-09-03', null, historyOf(null)).filters).toMatchObject({ range: 'custom', customFrom: '2026-09-03', customTo: '2026-09-03' });
  });

  it('keeps a token the facets cannot name so the user sees why nothing matched', () => {
    const parsed = parseQuery('repo:nope profile:nobody', facets, historyOf(facets));
    expect(parsed.filters.repository).toBe('');
    expect(parsed.filters.profileId).toBe('');
    expect(parsed.unresolved).toEqual(['repo:nope', 'profile:nobody']);
  });

  it('finds a deleted profile by the name its history carries', () => {
    expect(parseQuery('profile:old-side', facets, historyOf(facets)).filters.profileId).toBe('profile-old');
  });

  it('gives a reused name to the live profile and reaches the deleted namesake by id', () => {
    const reused = { ...facets, profiles: [
      { profile_id: 'profile-old', name: 'Work', deleted: true, count: 3 },
      { profile_id: 'profile-new', name: 'Work', count: 1 },
    ] };
    expect(parseQuery('profile:work', reused, historyOf(reused)).filters.profileId).toBe('profile-new');
    expect(parseQuery('profile:profile-old', reused, historyOf(reused)).filters.profileId).toBe('profile-old');

    const twiceDeleted = { ...facets, profiles: [
      { profile_id: 'profile-a', name: 'Work', deleted: true, count: 1 },
      { profile_id: 'profile-b', name: 'Work', deleted: true, count: 1 },
    ] };
    expect(parseQuery('profile:work', twiceDeleted, historyOf(twiceDeleted)).unresolved).toEqual(['profile:work']);
  });

  it('resolves a live profile that has no rows in the ledger', () => {
    const empty = { ...facets, profiles: [] };
    expect(parseQuery('profile:attn-work', empty, profileChoices(names, empty)).filters.profileId).toBe('profile-1');
    expect(parseQuery('profile:attn-work', null, profileChoices(names, null)).filters.profileId).toBe('profile-1');
  });

  it('follows a renamed profile in the typed query and leaves every other token alone', () => {
    const before = { 'profile-1': 'attn work', 'profile-2': 'side' };
    const after = { 'profile-1': 'Office', 'profile-2': 'side' };
    expect(renameProfileTokens('repo:attn  profile:attn-work 7d', before, after)).toBe('repo:attn  profile:office 7d');
    expect(renameProfileTokens('profile:side profile:gone', before, after)).toBe('profile:side profile:gone');
    expect(renameProfileTokens('profile:attn-work', before, { 'profile-2': 'side' })).toBe('profile:attn-work');
  });

  it('writes a profile by id when its name token is shared', () => {
    const filters = { scope: 'all' as const, range: 'any' as const, customFrom: '', customTo: '', profileId: 'profile-1', repository: '' };
    expect(formatQuery(filters, { 'profile-1': 'attn work', 'profile-2': 'attn-work' })).toBe('profile:profile-1');
    expect(formatQuery(filters, {})).toBe('profile:profile-1');
  });

  it('keeps the selected repository when two paths share a base name', () => {
    const duplicateNames = {
      ...facets,
      repositories: [
        { value: '/Users/victor/projects/attn', count: 3 },
        { value: '/tmp/checkout/attn', count: 2 },
      ],
    };

    expect(parseQuery('repo:attn', duplicateNames, historyOf(duplicateNames), '/tmp/checkout/attn').filters.repository)
      .toBe('/tmp/checkout/attn');
    expect(parseQuery('repo:attn', {
      ...duplicateNames,
      repositories: [{ value: '/Users/victor/projects/attn', count: 3 }],
    }, [], '/tmp/checkout/attn').filters.repository).toBe('/tmp/checkout/attn');
    expect(parseQuery('repo:attn', duplicateNames, historyOf(duplicateNames)).unresolved).toEqual(['repo:attn']);
    expect(parseQuery('repo:/Users/victor/projects/attn', duplicateNames, historyOf(duplicateNames)).filters.repository)
      .toBe('/Users/victor/projects/attn');
  });

  it('carries an exact repository path with spaces in one token', () => {
    const repository = '/tmp/attn run/ledger-repo';
    const token = repositoryQueryToken(repository);
    const spacedFacets = { ...facets, repositories: [{ value: repository, count: 2 }] };

    expect(token).not.toMatch(/\s/);
    expect(parseQuery(token, spacedFacets, historyOf(spacedFacets)).filters.repository).toBe(repository);
    expect(parseQuery(token, null, historyOf(null)).filters.repository).toBe(repository);
  });

  it('keeps an at-prefixed repository name literal', () => {
    const repository = '/work/@scope';
    const scopedFacets = { ...facets, repositories: [{ value: repository, count: 2 }] };

    expect(parseQuery('repo:@scope', scopedFacets, historyOf(scopedFacets), repository).filters.repository).toBe(repository);
  });

  it('round-trips through formatQuery', () => {
    const filters = { scope: 'all' as const, range: 'custom' as const, customFrom: '2026-08-01', customTo: '2026-08-03', profileId: 'profile-1', repository: '/Users/victor/projects/attn' };
    const text = formatQuery(filters, names);
    expect(text).toBe('repo:attn profile:attn-work from:2026-08-01 to:2026-08-03');
    expect(parseQuery(text, facets, historyOf(facets)).filters).toEqual({ range: 'custom', customFrom: '2026-08-01', customTo: '2026-08-03', profileId: 'profile-1', repository: '/Users/victor/projects/attn' });
  });
});

describe('query helpers', () => {
  it('matches every word somewhere in the haystack, case-blind', () => {
    expect(matchesWords(['Ledger Work', '/x/y'], ['ledger', 'y'])).toBe(true);
    expect(matchesWords(['Ledger Work'], ['ledger', 'zzz'])).toBe(false);
    expect(matchesWords([], [])).toBe(true);
  });

  it('matches dir: as typed with a tilde, pasted absolute, or with a trailing slash', () => {
    expect(matchesDir('/Users/victor/projects/attn/app', '~/projects/attn')).toBe(true);
    expect(matchesDir('/Users/victor/projects/attn/app', '/Users/victor/projects/attn/')).toBe(true);
    expect(matchesDir('/Users/victor/projects/attn-two', '~/projects/attn')).toBe(false);
    expect(matchesDir('/srv/work', '~/work')).toBe(false);
    expect(matchesDir('/anything', '')).toBe(true);
  });

  it('takes a base name and removes one token', () => {
    expect(baseName('/a/b/c/')).toBe('c');
    expect(baseName('plain')).toBe('plain');
    expect(removeToken('repo:a  7d profile:b', '7d')).toBe('repo:a profile:b');
  });
});

describe('ledger time and names', () => {
  const now = new Date('2026-09-06T12:00:00Z');

  it('stamps relative time in the fewest characters that still say when', () => {
    expect(relativeStamp('2026-09-06T11:59:40Z', now)).toBe('now');
    expect(relativeStamp('2026-09-06T11:57:00Z', now)).toBe('3m');
    expect(relativeStamp('2026-09-06T10:00:00Z', now)).toBe('2h');
    expect(relativeStamp('2026-09-03T10:00:00Z', now)).toBe('3d');
  });

  it('stamps a future instant as time until it, never as elapsed time', () => {
    expect(untilStamp('2026-09-06T12:00:20Z', now)).toBe('now');
    expect(untilStamp('2026-09-06T11:00:00Z', now)).toBe('now');
    expect(untilStamp('2026-09-06T12:03:00Z', now)).toBe('3m');
    expect(untilStamp('2026-09-06T14:00:00Z', now)).toBe('2h');
    expect(untilStamp('2026-09-10T12:00:00Z', now)).toBe('4d');
    expect(untilStamp('2026-11-06T12:00:00Z', now)).toBe(new Date('2026-11-06T12:00:00Z').toLocaleDateString(undefined, { month: 'short', day: 'numeric' }));
  });

  it('shortens paths to a home tilde and then to the last two components', () => {
    expect(tildePath('/Users/victor/projects/attn')).toBe('~/projects/attn');
    expect(tildePath('/home/victor')).toBe('~');
    expect(shortPath('/Users/victor/projects/attn')).toBe('~/projects/attn');
    expect(shortPath('/private/tmp/very/long/path/to/some/fixtures/wt/present')).toBe('…/wt/present');
  });

  it('rewrites daemon prose so people read titles, never ids', () => {
    const title = (id: string) => (id === 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee' ? 'Fixture run' : undefined);
    expect(nameIds('aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee is running in it', title)).toBe('Fixture run is running in it');
    expect(nameIds('11111111-2222-3333-4444-555555555555 is running in it', title)).toBe('a session is running in it');
    expect(nameIds('conversation 12345678-1234-1234-1234-123456789abc is gone', title)).toBe('its conversation is gone');
  });
});
