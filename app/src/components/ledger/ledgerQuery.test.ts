import { describe, expect, it } from 'vitest';
import { baseName, formatQuery, matchesDir, matchesWords, parseQuery, removeToken } from './ledgerQuery';
import { nameIds, relativeStamp, shortPath, tildePath } from './ledgerTime';

const facets = {
  repositories: [{ value: '/Users/victor/projects/attn', count: 3 }],
  workspaces: [{ value: 'ws-1', count: 2 }],
};
const label = (id: string) => (id === 'ws-1' ? 'attn work' : id);

describe('parseQuery', () => {
  it('splits tokens into daemon filters, a directory, and words', () => {
    const parsed = parseQuery('repo:attn ws:attn-work 7d dir:~/x Ledger  reopen', facets, label);
    expect(parsed.filters).toEqual({ range: '7d', customFrom: '', customTo: '', workspaceId: 'ws-1', repository: '/Users/victor/projects/attn' });
    expect(parsed.dir).toBe('~/x');
    expect(parsed.words).toEqual(['ledger', 'reopen']);
    expect(parsed.unresolved).toEqual([]);
  });

  it('fills the missing end of a custom range with the other end', () => {
    expect(parseQuery('from:2026-09-01', null, label).filters).toMatchObject({ range: 'custom', customFrom: '2026-09-01', customTo: '2026-09-01' });
    expect(parseQuery('to:2026-09-03', null, label).filters).toMatchObject({ range: 'custom', customFrom: '2026-09-03', customTo: '2026-09-03' });
  });

  it('keeps a token the facets cannot name so the user sees why nothing matched', () => {
    const parsed = parseQuery('repo:nope ws:nobody', facets, label);
    expect(parsed.filters.repository).toBe('');
    expect(parsed.filters.workspaceId).toBe('');
    expect(parsed.unresolved).toEqual(['repo:nope', 'ws:nobody']);
  });

  it('round-trips through formatQuery', () => {
    const filters = { scope: 'all' as const, range: 'custom' as const, customFrom: '2026-08-01', customTo: '2026-08-03', workspaceId: 'ws-1', repository: '/Users/victor/projects/attn' };
    const text = formatQuery(filters, label);
    expect(text).toBe('repo:attn ws:attn-work from:2026-08-01 to:2026-08-03');
    expect(parseQuery(text, facets, label).filters).toEqual({ range: 'custom', customFrom: '2026-08-01', customTo: '2026-08-03', workspaceId: 'ws-1', repository: '/Users/victor/projects/attn' });
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
    expect(removeToken('repo:a  7d ws:b', '7d')).toBe('repo:a ws:b');
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
