import { describe, it, expect } from 'vitest';
import { rankOpenerFiles, type OpenerFile } from './openerRank';

const recent = (label: string, at: string): OpenerFile => ({ absPath: `/repo/${label}`, label, recentAt: at });
const indexed = (label: string): OpenerFile => ({ absPath: `/repo/${label}`, label });

describe('rankOpenerFiles', () => {
  it.each([
    {
      ranking: 'only recents, in the order given, for an empty query',
      files: [recent('b.md', '2026-07-24T09:00:00Z'), indexed('a.md'), recent('c.md', '2026-07-24T10:00:00Z')],
      query: '',
      labels: ['b.md', 'c.md'],
    },
    {
      ranking: 'recents and index entries in one list, the recent first on an equal match',
      files: [indexed('docs/plan.md'), recent('notes/plan.md', '2026-07-24T10:00:00Z')],
      query: 'plan',
      labels: ['notes/plan.md', 'docs/plan.md'],
    },
    {
      ranking: 'a clearly better match above a recent',
      files: [recent('pile/of/long/anthologies.md', '2026-07-24T10:00:00Z'), indexed('plan.md')],
      query: 'plan',
      labels: ['plan.md', 'pile/of/long/anthologies.md'],
    },
  ])('ranks $ranking', ({ files, query, labels }) => {
    expect(rankOpenerFiles(files, query).map((file) => file.label)).toEqual(labels);
  });
});
