// The daemon always interprets fs paths root-relative (fsdoc cleanRel), so directory-relative
// resolution has to happen here, before a path ever reaches the daemon.

export type ResolvedLink =
  | { kind: 'note'; path: string; anchor?: string }
  | { kind: 'fragment'; anchor: string }
  | { kind: 'external'; href: string };

const SCHEME = /^[a-z][a-z0-9+.-]*:/i;

function decodeAnchor(raw: string): string {
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}

// Split '#anchor' before '?query': an anchor always follows any query, so a literal '?' inside the anchor text is preserved.
function stripAnchorAndQuery(href: string): { path: string; anchor?: string } {
  const hashIdx = href.indexOf('#');
  const path = hashIdx === -1 ? href : href.slice(0, hashIdx);
  const anchor = hashIdx === -1 ? undefined : decodeAnchor(href.slice(hashIdx + 1));
  const queryIdx = path.indexOf('?');
  return { path: queryIdx === -1 ? path : path.slice(0, queryIdx), anchor };
}

// A '..' above the notebook root clamps there rather than escaping or throwing, matching the daemon's cleanRel.
function normalizeJoin(baseDir: string, path: string): string {
  const parts: string[] = [];
  for (const segment of `${baseDir}/${path}`.split('/')) {
    if (segment === '' || segment === '.') continue;
    if (segment === '..') parts.pop();
    else parts.push(segment);
  }
  return parts.join('/');
}

export function noteDir(notePath: string): string {
  const idx = notePath.lastIndexOf('/');
  return idx === -1 ? '' : notePath.slice(0, idx);
}

export function headingSlug(text: string): string {
  return text
    .toLowerCase()
    .replace(/[^a-z0-9 -]/g, '')
    .trim()
    .replace(/\s+/g, '-');
}

export function resolveNotebookLink(href: string, baseDir: string): ResolvedLink {
  const trimmed = href.trim();
  if (!trimmed) return { kind: 'external', href: '' };
  if (trimmed.startsWith('#')) {
    return { kind: 'fragment', anchor: decodeAnchor(trimmed.slice(1)) };
  }
  if (trimmed.startsWith('//') || SCHEME.test(trimmed)) {
    return { kind: 'external', href: trimmed };
  }

  const { path, anchor } = stripAnchorAndQuery(trimmed);
  const resolved = path.startsWith('/')
    ? normalizeJoin('', path.replace(/^\/+/, ''))
    : normalizeJoin(baseDir, path);

  if (!resolved) return { kind: 'external', href: '' };
  return { kind: 'note', path: resolved, anchor };
}
