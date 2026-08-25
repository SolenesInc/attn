import { scoreFile } from './rank';

export interface OpenerFile {
  absPath: string;
  label: string;
  recentAt?: string;
}

// Enough to win ties and near-ties against an equally good match you've never opened, not
// enough to keep a weak match above a clearly better one.
export const RECENT_BONUS = 1.4;

function joinRoot(root: string, rel: string): string {
  return root.endsWith('/') ? `${root}${rel}` : `${root}/${rel}`;
}

export function mergeOpenerFiles(
  recents: { path: string; lastAt: string }[],
  root: string | null,
  indexFiles: string[],
): OpenerFile[] {
  const prefix = root ? (root.endsWith('/') ? root : `${root}/`) : null;
  const merged: OpenerFile[] = recents.map((recent) => ({
    absPath: recent.path,
    label: prefix && recent.path.startsWith(prefix) ? recent.path.slice(prefix.length) : recent.path,
    recentAt: recent.lastAt,
  }));
  const seen = new Set(merged.map((file) => file.absPath));
  if (!root) return merged;
  for (const rel of indexFiles) {
    const absPath = joinRoot(root, rel);
    if (seen.has(absPath)) continue;
    seen.add(absPath);
    merged.push({ absPath, label: rel });
  }
  return merged;
}

export function rankOpenerFiles(files: OpenerFile[], query: string, limit = 50): OpenerFile[] {
  if (query.trim() === '') {
    return files.filter((file) => file.recentAt).slice(0, limit);
  }
  return files
    .map((file) => ({
      file,
      score: scoreFile({ path: file.label, updated: file.recentAt }, query) * (file.recentAt ? RECENT_BONUS : 1),
    }))
    .filter(({ score }) => score > 0)
    .sort((left, right) => {
      if (right.score !== left.score) return right.score - left.score;
      const leftAt = left.file.recentAt ?? '';
      const rightAt = right.file.recentAt ?? '';
      if (leftAt !== rightAt) return leftAt < rightAt ? 1 : -1;
      return left.file.label < right.file.label ? -1 : left.file.label > right.file.label ? 1 : 0;
    })
    .slice(0, limit)
    .map(({ file }) => file);
}
