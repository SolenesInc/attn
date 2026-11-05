import type { Seed } from '../hooks/useDaemonSocket';

/** Values `is:` accepts. Anything else is named back at the reader. */
export const IS_VALUES = ['open', 'closed', 'any', 'ready', 'blocked', 'parked', 'standby'] as const;
export type IsValue = (typeof IS_VALUES)[number];

export interface ParsedQuery {
  raw: string;
  /** Free-text terms, lowercased. Every term must hit, somewhere. */
  terms: string[];
  /** `is:` values, unioned. Empty means the default lens: open seeds only. */
  is: IsValue[];
  /** `tender:` values, unioned. */
  tenders: string[];
  /** Operator tokens with no such filter, verbatim — shown, never swallowed. */
  unknown: string[];
  /** The operator the reader is mid-way through typing, for the value hint. */
  partial: 'is' | 'tender' | null;
  /** Whether anything at all filters. */
  active: boolean;
  /** Whether this is a search rather than a lens: text or a tender searches the
   * subtree, a bare `is:` only filters the seeds already on screen. */
  searches: boolean;
}

const EMPTY: ParsedQuery = {
  raw: '',
  terms: [],
  is: [],
  tenders: [],
  unknown: [],
  partial: null,
  active: false,
  searches: false,
};

export function parseQuery(raw: string): ParsedQuery {
  const trimmed = raw.trim();
  if (!trimmed) return { ...EMPTY, raw };
  const tokens = trimmed.split(/\s+/);
  const q: ParsedQuery = { ...EMPTY, raw, terms: [], is: [], tenders: [], unknown: [] };
  tokens.forEach((token, i) => {
    const last = i === tokens.length - 1 && !/\s$/.test(raw);
    const op = /^(is|tender):(.*)$/i.exec(token);
    if (!op) {
      q.terms.push(token.toLowerCase());
      return;
    }
    const name = op[1].toLowerCase() as 'is' | 'tender';
    const value = op[2].toLowerCase();
    if (!value) {
      if (last) q.partial = name;
      else q.unknown.push(token);
      return;
    }
    if (name === 'tender') {
      q.tenders.push(value);
      return;
    }
    if ((IS_VALUES as readonly string[]).includes(value)) {
      q.is.push(value as IsValue);
      return;
    }
    if (last && IS_VALUES.some((v) => v.startsWith(value))) q.partial = 'is';
    else q.unknown.push(token);
  });
  q.searches = q.terms.length > 0 || q.tenders.length > 0 || q.unknown.length > 0;
  q.active = q.searches || q.is.length > 0;
  return q;
}

/** Toggle one operator token in a raw query, preserving the rest of it. */
export function toggleToken(raw: string, token: string): string {
  const tokens = raw.trim().split(/\s+/).filter(Boolean);
  const i = tokens.findIndex((t) => t.toLowerCase() === token.toLowerCase());
  if (i >= 0) tokens.splice(i, 1);
  else tokens.push(token);
  return tokens.join(' ');
}

export type Range = [number, number];

export interface SeedMatch {
  seed: Seed;
  /** Lower sorts first: id 0, title 10+position, tender 30, body 40. */
  rank: number;
  titleRanges: Range[];
  /** Only for a body-only hit — the line that says why this row is here. */
  snippet: { text: string; ranges: Range[] } | null;
  idHit: boolean;
}

/** Lowercased once per seed object, so a change re-derives only the seeds it
 * changed. A seed object is never mutated after it enters the store. Receipts in
 * gardenSearch.bench.ts. */
export interface SeedText {
  seed: Seed;
  titleLower: string;
  bodyLower: string;
  idLower: string;
  closed: boolean;
}

const texts = new WeakMap<Seed, SeedText>();

export function seedText(seed: Seed): SeedText {
  const known = texts.get(seed);
  if (known) return known;
  const text: SeedText = {
    seed,
    titleLower: (seed.title ?? '').toLowerCase(),
    bodyLower: (seed.body ?? '').toLowerCase(),
    idLower: (seed.id ?? '').toLowerCase(),
    closed: seed.status === 'harvested' || seed.status === 'withered',
  };
  texts.set(seed, text);
  return text;
}

/** What a query reads from outside the seed, looked up when the query runs. */
export interface SearchLookups {
  /** Whoever holds the seed, already resolved to a display name. */
  tenderOf: (seed: Seed) => string;
  /** How many open seeds block it. */
  blockersOf: (seed: Seed) => number;
}

export function satisfiesLens(text: SeedText, is: IsValue[], lookups: SearchLookups): boolean {
  if (is.length === 0) return !text.closed;
  return is.some((value) => satisfies(value, text, lookups));
}

function satisfies(value: IsValue, text: SeedText, lookups: SearchLookups): boolean {
  switch (value) {
    case 'any':
      return true;
    case 'open':
      return !text.closed;
    case 'closed':
      return text.closed;
    case 'ready':
      return Boolean(text.seed.ready) && !text.closed;
    case 'blocked':
      return !text.closed && lookups.blockersOf(text.seed) > 0;
    case 'parked':
      return text.seed.status === 'parked';
    case 'standby':
      return Boolean(text.seed.standby) && !text.closed;
  }
}

/** Every occurrence of every term, merged so overlaps highlight once. */
function rangesOf(haystack: string, terms: string[]): Range[] {
  const lower = haystack.toLowerCase();
  const found: Range[] = [];
  for (const term of terms) {
    let from = lower.indexOf(term);
    while (from !== -1) {
      found.push([from, from + term.length]);
      from = lower.indexOf(term, from + term.length);
    }
  }
  if (found.length < 2) return found;
  found.sort((a, b) => a[0] - b[0]);
  const merged: Range[] = [found[0]];
  for (const range of found.slice(1)) {
    const tail = merged[merged.length - 1];
    if (range[0] <= tail[1]) tail[1] = Math.max(tail[1], range[1]);
    else merged.push(range);
  }
  return merged;
}

const SNIPPET_WIDTH = 132;

// The window is cut before whitespace is collapsed: collapsing an 8KB brief to
// show 132 characters of it is cost that only shows up once a garden is big.
function snippetOf(body: string, bodyLower: string, terms: string[]): { text: string; ranges: Range[] } | null {
  let at = -1;
  for (const term of terms) {
    const i = bodyLower.indexOf(term);
    if (i !== -1 && (at === -1 || i < at)) at = i;
  }
  if (at === -1) return null;
  const from = Math.max(0, at - Math.floor(SNIPPET_WIDTH / 3));
  const to = Math.min(body.length, from + SNIPPET_WIDTH + 40);
  const window = body.slice(from, to).replace(/\s+/g, ' ').trim();
  const text = (from > 0 ? '…' : '') + window + (to < body.length ? '…' : '');
  return { text, ranges: rangesOf(text, terms) };
}

export function matchSeed(text: SeedText, q: ParsedQuery, lookups: SearchLookups): SeedMatch | null {
  if (q.unknown.length > 0) return null;
  if (!satisfiesLens(text, q.is, lookups)) return null;

  const tenderLower = lookups.tenderOf(text.seed).toLowerCase();
  if (q.tenders.length > 0) {
    if (!tenderLower || !q.tenders.some((name) => tenderLower.includes(name))) return null;
  }

  if (q.terms.length === 0) {
    return { seed: text.seed, rank: 20, titleRanges: [], snippet: null, idHit: false };
  }

  for (const term of q.terms) {
    const hit =
      text.titleLower.includes(term) ||
      text.idLower.includes(term) ||
      tenderLower.includes(term) ||
      text.bodyLower.includes(term);
    if (!hit) return null;
  }

  const idHit = q.terms.every((term) => text.idLower.includes(term));
  const titleHit = q.terms.every((term) => text.titleLower.includes(term));
  const tenderHit = q.terms.every((term) => tenderLower.includes(term));

  let rank = 40;
  if (idHit) rank = 0;
  else if (titleHit) rank = 10 + Math.min(...q.terms.map((term) => text.titleLower.indexOf(term)));
  else if (tenderHit) rank = 30;

  return {
    seed: text.seed,
    rank,
    titleRanges: titleHit || rank === 0 ? rangesOf(text.seed.title ?? '', q.terms) : [],
    snippet: rank === 40 ? snippetOf(text.seed.body ?? '', text.bodyLower, q.terms) : null,
    idHit,
  };
}

/** Every seed in `pool` that answers the query, best first; ties keep pool order. */
export function searchGarden(pool: SeedText[], q: ParsedQuery, lookups: SearchLookups): SeedMatch[] {
  const matches: Array<{ match: SeedMatch; i: number }> = [];
  pool.forEach((text, i) => {
    const match = matchSeed(text, q, lookups);
    if (match) matches.push({ match, i });
  });
  return matches
    .sort((a, b) => a.match.rank - b.match.rank || a.i - b.i)
    .map((entry) => entry.match);
}

/** Split a string into its highlighted and plain runs, in order. */
export function splitRanges(text: string, ranges: Range[]): Array<{ text: string; hit: boolean }> {
  if (ranges.length === 0) return [{ text, hit: false }];
  const parts: Array<{ text: string; hit: boolean }> = [];
  let at = 0;
  for (const [from, to] of ranges) {
    if (from > at) parts.push({ text: text.slice(at, from), hit: false });
    parts.push({ text: text.slice(from, to), hit: true });
    at = to;
  }
  if (at < text.length) parts.push({ text: text.slice(at), hit: false });
  return parts;
}
