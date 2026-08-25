
export interface FileCandidate {
  path: string;
  title?: string;
  updated?: string;
}

// Index paths are always forward-slashed (daemon-normalized), so no platform handling.
export function finderBasename(path: string): string {
  const slash = path.lastIndexOf('/');
  return slash === -1 ? path : path.slice(slash + 1);
}

const WORD_BOUNDARY = /[/\-_. ]/;

// Both arguments must already be lowercased. 0 means `query` is not a subsequence
function subsequenceScore(text: string, query: string): number {
  if (query === '') return 1;
  let score = 0;
  let from = 0;
  let prevMatch = -2;
  let run = 0;
  for (let qi = 0; qi < query.length; qi++) {
    const target = query[qi];
    let found = -1;
    for (let i = from; i < text.length; i++) {
      if (text[i] === target) {
        found = i;
        break;
      }
    }
    if (found === -1) return 0;
    score += 1;
    if (found === prevMatch + 1) {
      run += 1;
      score += run * 2;
    } else {
      run = 0;
    }
    if (found === 0 || WORD_BOUNDARY.test(text[found - 1])) {
      score += 3;
    }
    prevMatch = found;
    from = found + 1;
  }
  return score;
}

export function scoreFile(entry: FileCandidate, query: string): number {
  const q = query.toLowerCase().trim();
  if (q === '') return 1;
  const path = entry.path.toLowerCase();
  const base = finderBasename(path);
  const title = (entry.title ?? '').toLowerCase();
  const pathScore = subsequenceScore(path, q);
  const baseScore = subsequenceScore(base, q);
  const titleScore = subsequenceScore(title, q);
  return Math.max(pathScore, baseScore * 1.5, titleScore * 0.9);
}

function tieBreak(a: FileCandidate, b: FileCandidate): number {
  const au = a.updated ?? '';
  const bu = b.updated ?? '';
  if (au !== bu) return au < bu ? 1 : -1;
  return a.path < b.path ? -1 : a.path > b.path ? 1 : 0;
}

export function rankFiles<T extends FileCandidate>(
  files: T[],
  query: string,
  limit = 50,
): T[] {
  return files
    .map((entry) => ({ entry, score: scoreFile(entry, query) }))
    .filter(({ score }) => score > 0)
    .sort((left, right) => (right.score - left.score) || tieBreak(left.entry, right.entry))
    .slice(0, limit)
    .map(({ entry }) => entry);
}
