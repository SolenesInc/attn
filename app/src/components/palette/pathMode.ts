// A relative path would land on the daemon's own working directory, so `./` and `../`
// are expanded here against the opener's root before the query is sent.

/** True when the query names a path rather than a file to fuzzy-match. */
export function isPathQuery(query: string): boolean {
  const trimmed = query.trimStart();
  return (
    trimmed.startsWith('/') ||
    trimmed.startsWith('~') ||
    trimmed === '.' ||
    trimmed.startsWith('./') ||
    trimmed.startsWith('../')
  );
}

export function toBrowseInput(query: string, root: string | null): string | null {
  const trimmed = query.trimStart();
  if (!isPathQuery(trimmed)) return null;
  if (trimmed.startsWith('/') || trimmed.startsWith('~')) return trimmed;
  if (!root) return null;
  return normalizePath(`${root}/${trimmed}`);
}

function normalizePath(path: string): string {
  const trailingSlash = path.endsWith('/');
  const absolute = path.startsWith('/');
  const resolved: string[] = [];
  for (const segment of path.split('/')) {
    if (segment === '' || segment === '.') continue;
    if (segment === '..') {
      if (resolved.length > 0 && resolved[resolved.length - 1] !== '..') {
        resolved.pop();
        continue;
      }
      if (absolute) continue; // can't go above /
    }
    resolved.push(segment);
  }
  const joined = (absolute ? '/' : '') + resolved.join('/');
  if (joined === '/' || joined === '') return absolute ? '/' : '.';
  return trailingSlash ? `${joined}/` : joined;
}

export function descendQuery(displayPath: string): string {
  return displayPath.endsWith('/') ? displayPath : `${displayPath}/`;
}
