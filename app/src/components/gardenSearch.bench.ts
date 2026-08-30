// Every steady-state figure is the minimum of three rounds after a long warmup.
import type { Seed } from '../hooks/useDaemonSocket';
import { buildIndex, parseQuery, searchGarden, type SearchEntry } from './gardenSearch.js';

const WORDS = (
  'reconnect socket daemon garden seed panel tender crown plot harvest wither ' +
  'dormant terminal ghostty snapshot restore protocol migration workspace tile ' +
  'notebook annotation delegate session queue attention badge shortcut theme ' +
  'scrollback keyboard focus escape pointer render paint frame budget latency'
).split(' ');

// `jsc` has no `console`, node has no `print`. One line, and the same file runs
// on both engines.
declare const print: ((line: string) => void) | undefined;
const say: (line: string) => void =
  typeof print === 'function' ? print : (line) => console.log(line);

function words(rand: () => number, n: number): string {
  const out: string[] = [];
  for (let i = 0; i < n; i++) out.push(WORDS[Math.floor(rand() * WORDS.length)]);
  return out.join(' ');
}

function lcg(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state * 1664525 + 1013904223) >>> 0;
    return state / 0x100000000;
  };
}

function corpus(count: number, bodyWords: (rand: () => number) => number): Seed[] {
  const rand = lcg(42);
  const members = ['hazel', 'juniper', 'sorrel', ''];
  const statuses = ['planted', 'growing', 'harvested', 'withered', 'dormant'];
  const seeds: Seed[] = [];
  for (let i = 0; i < count; i++) {
    seeds.push({
      id: `s-${(i * 2654435761).toString(36).slice(-6)}`,
      title: words(rand, 6),
      body: words(rand, bodyWords(rand)),
      status: statuses[Math.floor(rand() * statuses.length)],
      ready: rand() < 0.4,
      created_at: '2026-08-19T12:00:00Z',
      updated_at: '2026-08-19T12:00:00Z',
      rev: 1,
      edges: i % 7 === 0 ? [{ kind: 'part-of', to: `s-crown${i % 11}` }] : [],
      gate: false,
      template: false,
      step_slug: '',
      planter_member: '',
      planter_session: '',
      tender_member: members[Math.floor(rand() * members.length)],
      tender_session: '',
      vars: [],
    } as unknown as Seed);
  }
  return seeds;
}

const ctx = {
  tenderOf: (seed: Seed) => seed.tender_member ?? '',
  blockersOf: () => 0,
};

const QUERIES = ['reco', 'reconnect', 'reconnect daemon', 'is:ready', 'tender:hazel', 'zzz'];

/** The minimum over three rounds, so a half-warm JIT cannot be the number. */
function best(rounds: number, runs: number, once: () => void): number {
  let low = Infinity;
  for (let round = 0; round < rounds; round++) {
    const started = performance.now();
    for (let i = 0; i < runs; i++) once();
    low = Math.min(low, (performance.now() - started) / runs);
  }
  return low;
}

function timeOne(pool: SearchEntry[], raw: string): number {
  const q = parseQuery(raw);
  for (let i = 0; i < 300; i++) searchGarden(pool, q);
  return best(3, 100, () => searchGarden(pool, parseQuery(raw)));
}

function timeIndex(seeds: Seed[]): number {
  for (let i = 0; i < 30; i++) buildIndex(seeds, ctx);
  return best(3, 20, () => buildIndex(seeds, ctx));
}

function report(label: string, seeds: Seed[]) {
  const bytes = seeds.reduce((n, s) => n + (s.body?.length ?? 0) + (s.title?.length ?? 0), 0);
  let at = performance.now();
  const pool = buildIndex(seeds, ctx);
  const coldIndex = performance.now() - at;
  at = performance.now();
  searchGarden(pool, parseQuery(QUERIES[QUERIES.length - 3]));
  const coldQuery = performance.now() - at;

  const lines = QUERIES.map((raw) => {
    const ms = timeOne(pool, raw);
    return `    keystroke  ${raw.padEnd(20)} ${ms.toFixed(3)} ms`;
  });
  lines.unshift(`    snapshot   ${'(rebuild the index)'.padEnd(20)} ${timeIndex(seeds).toFixed(3)} ms`);
  lines.unshift(
    `    first      ${'(cold, index + scan)'.padEnd(20)} ${(coldIndex + coldQuery).toFixed(3)} ms`,
  );
  say(
    `\n  ${label}: ${seeds.length} seeds, ${(bytes / 1024).toFixed(0)} KB of text\n${lines.join('\n')}`,
  );
}

const light = (rand: () => number) => 15 + Math.floor(rand() * 25); // ~170 chars
const heavy = (rand: () => number) => (rand() < 0.1 ? 1100 : 180); // ~1.5 KB mean

report('measured  107', corpus(107, light));
report('measured 1000', corpus(1000, light));
report('heavy    1000', corpus(1000, heavy));
report('heavy    5000', corpus(5000, heavy));
