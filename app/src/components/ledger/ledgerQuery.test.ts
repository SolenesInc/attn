import { describe, expect, it } from 'vitest';
import type { SessionLedgerFacets } from '../../types/generated';
import { formatQuery, matchesDir, matchesWords, parseQuery, repositoryQueryToken, type ParsedQuery } from './ledgerQuery';
import { relativeStamp, shortPath, tildePath, untilStamp } from './ledgerTime';

const ATTN = '/Users/victor/projects/attn';
const CHECKOUT = '/tmp/checkout/attn';
const SPACED = '/tmp/attn run/ledger-repo';
const SCOPED = '/work/@scope';

function facets(...repositories: string[]): SessionLedgerFacets {
  return {
    repositories: repositories.map((value) => ({ value, count: 1 })),
    workspaces: [{ value: 'ws-1', count: 2 }],
  } as SessionLedgerFacets;
}
const label = (id: string) => (id === 'ws-1' ? 'attn work' : id);
const ANY: ParsedQuery['filters'] = { range: 'any', customFrom: '', customTo: '', workspaceId: '', repository: '' };

describe('the ledger query language', () => {
  it.each<[string, SessionLedgerFacets | null, string, Partial<ParsedQuery> & { filters?: Partial<ParsedQuery['filters']> }]>([
    ['repo:attn ws:attn-work 7d dir:~/x Ledger  reopen', facets(ATTN), '', {
      filters: { range: '7d', customFrom: '', customTo: '', workspaceId: 'ws-1', repository: ATTN },
      dir: '~/x',
      words: ['ledger', 'reopen'],
      unresolved: [],
    }],
    ['today', null, '', { filters: { ...ANY, range: 'today' } }],
    ['yesterday', null, '', { filters: { ...ANY, range: 'yesterday' } }],
    ['week', null, '', { filters: { ...ANY, range: '7d' } }],
    ['month', null, '', { filters: { ...ANY, range: '30d' } }],
    ['from:2026-09-01', null, '', { filters: { ...ANY, range: 'custom', customFrom: '2026-09-01', customTo: '2026-09-01' } }],
    ['to:2026-09-03', null, '', { filters: { ...ANY, range: 'custom', customFrom: '2026-09-03', customTo: '2026-09-03' } }],
    ['ws:ws-1', facets(), '', { filters: { ...ANY, workspaceId: 'ws-1' }, unresolved: [] }],
    ['repo:nope ws:nobody', facets(ATTN), '', { filters: ANY, unresolved: ['repo:nope', 'ws:nobody'] }],
    ['repo:attn', facets(ATTN, CHECKOUT), '', { filters: ANY, unresolved: ['repo:attn'] }],
    ['repo:attn', facets(ATTN, CHECKOUT), CHECKOUT, { filters: { ...ANY, repository: CHECKOUT } }],
    ['repo:attn', facets(ATTN), CHECKOUT, { filters: { ...ANY, repository: CHECKOUT } }],
    [`repo:${ATTN}`, facets(ATTN, CHECKOUT), '', { filters: { ...ANY, repository: ATTN } }],
    [repositoryQueryToken(SPACED), facets(SPACED), '', { filters: { ...ANY, repository: SPACED }, words: [] }],
    [repositoryQueryToken(SPACED), null, '', { filters: { ...ANY, repository: SPACED }, words: [] }],
    ['repo:@scope', facets(SCOPED), SCOPED, { filters: { ...ANY, repository: SCOPED } }],
  ])('parses %j', (text, knownFacets, rememberedRepository, expected) => {
    expect(parseQuery(text, knownFacets, label, rememberedRepository)).toMatchObject(expected);
  });

  it.each([
    [{ range: 'custom' as const, customFrom: '2026-08-01', customTo: '2026-08-03', workspaceId: 'ws-1', repository: ATTN }, 'repo:attn ws:attn-work from:2026-08-01 to:2026-08-03'],
    [{ range: '30d' as const, customFrom: '', customTo: '', workspaceId: '', repository: ATTN }, 'repo:attn 30d'],
    [{ range: 'any' as const, customFrom: '', customTo: '', workspaceId: 'ws-1', repository: '' }, 'ws:attn-work'],
  ])('formats %j as %j and parses it back', (filters, text) => {
    expect(formatQuery({ scope: 'all', ...filters }, label)).toBe(text);
    expect(parseQuery(text, facets(ATTN), label).filters).toEqual(filters);
  });

  it.each([
    [['Ledger Work', '/x/y'], ['ledger', 'y'], true],
    [['Ledger Work'], ['ledger', 'zzz'], false],
    [[], [], true],
  ])('words: %j contains every one of %j: %s', (haystack, words, matches) => {
    expect(matchesWords(haystack, words)).toBe(matches);
  });

  it.each([
    ['/Users/victor/projects/attn/app', '~/projects/attn', true],
    ['/Users/victor/projects/attn/app', '/Users/victor/projects/attn/', true],
    ['/home/victor/projects/attn', '~/projects/attn', true],
    ['/Users/victor/projects/attn', '~/projects/attn/', true],
    ['/Users/victor/projects/attn-two', '~/projects/attn', false],
    ['/srv/work', '~/work', false],
    ['/anything', '', true],
  ])('dir: %j is under %j: %s', (directory, dir, matches) => {
    expect(matchesDir(directory, dir)).toBe(matches);
  });
});

describe('ledger stamps and paths', () => {
  const now = new Date('2026-09-06T12:00:00Z');
  const monthDay = (iso: string) => new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });

  it.each([
    ['relativeStamp', '2026-09-06T11:59:40Z', 'now'],
    ['relativeStamp', '2026-09-06T11:57:00Z', '3m'],
    ['relativeStamp', '2026-09-06T10:00:00Z', '2h'],
    ['relativeStamp', '2026-09-03T10:00:00Z', '3d'],
    ['untilStamp', '2026-09-06T12:00:20Z', 'now'],
    ['untilStamp', '2026-09-06T11:00:00Z', 'now'],
    ['untilStamp', '2026-09-06T12:03:00Z', '3m'],
    ['untilStamp', '2026-09-06T14:00:00Z', '2h'],
    ['untilStamp', '2026-09-10T12:00:00Z', '4d'],
    ['untilStamp', '2026-11-06T12:00:00Z', monthDay('2026-11-06T12:00:00Z')],
  ] as const)('%s stamps %s as %j', (stamp, iso, text) => {
    expect({ relativeStamp, untilStamp }[stamp](iso, now)).toBe(text);
  });

  it.each([
    ['tildePath', '/Users/victor/projects/attn', '~/projects/attn'],
    ['tildePath', '/home/victor', '~'],
    ['shortPath', '/Users/victor/projects/attn', '~/projects/attn'],
    ['shortPath', '/private/tmp/very/long/path/to/some/fixtures/wt/present', '…/wt/present'],
  ] as const)('%s shortens %s to %j', (shorten, path, text) => {
    expect({ tildePath, shortPath }[shorten](path)).toBe(text);
  });
});
