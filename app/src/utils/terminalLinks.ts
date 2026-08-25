// Detection is hover-lazy: nothing scans scrollback or runs per-write.

export const URL_RE = /\b(?:https?:\/\/|file:\/\/|mailto:|ftp:\/\/|ssh:\/\/|git:\/\/|tel:|magnet:|gemini:\/\/|gopher:\/\/|news:)[^\s<>()]+/g;

export interface ColumnRange {
  startCol: number;
  endCol: number;
}

export interface UrlAtColumn extends ColumnRange {
  uri: string;
}

export function urlAtColumn(line: string, col: number): UrlAtColumn | null {
  for (const match of line.matchAll(URL_RE)) {
    const start = match.index ?? -1;
    const uri = match[0].replace(/[.,;:!?]+$/, '');
    if (col >= start && col < start + uri.length) {
      return { uri, startCol: start, endCol: start + uri.length };
    }
  }
  return null;
}

// An OSC 8 label can contain arbitrary text, so its range comes from the hidden
// URI: the run of indices around `index` resolving to the SAME uri.
export function hyperlinkRangeAt(
  uriAtIndex: (index: number) => string | null,
  index: number,
  length: number,
): UrlAtColumn | null {
  const uri = uriAtIndex(index);
  if (!uri) return null;
  let startCol = index;
  while (startCol > 0 && uriAtIndex(startCol - 1) === uri) startCol -= 1;
  let endCol = index + 1;
  while (endCol < length && uriAtIndex(endCol) === uri) endCol += 1;
  return { uri, startCol, endCol };
}

export function fragmentAtColumn(line: string, col: number): ColumnRange | null {
  const character = line[col];
  if (!character || /\s/.test(character)) return null;
  let startCol = col;
  while (startCol > 0 && !/\s/.test(line[startCol - 1])) startCol -= 1;
  let endCol = col + 1;
  while (endCol < line.length && !/\s/.test(line[endCol])) endCol += 1;
  return { startCol, endCol };
}

export interface PathCandidate extends ColumnRange {
  path: string;
  line?: number;
  column?: number;
}

const LEADING_WRAPPERS = '([{<"\'`';
const TRAILING_NOISE_RE = /['")\]}>,;!?]+$/;
const LINE_COL_RE = /:(\d{1,7})(?:[:.](\d{1,7}))?:?$/;

function looksLikePath(text: string): boolean {
  if (!text || text.includes('://')) return false;
  // `//host/...` is a URL remainder (scheme stripped at a mid-fragment start), not a path.
  if (text.startsWith('//')) return false;
  if (text.includes('/')) return true;
  if (text.startsWith('~')) return true;
  return /\.[A-Za-z0-9_]{1,8}$/.test(text);
}

export function pathCandidatesForFragment(fragment: string, fragmentStartCol: number): PathCandidate[] {
  const candidates: PathCandidate[] = [];
  const pushFrom = (offset: number) => {
    let core = fragment.slice(offset);
    const noise = core.match(TRAILING_NOISE_RE);
    if (noise) core = core.slice(0, core.length - noise[0].length);
    while (core.endsWith('.')) core = core.slice(0, -1);
    if (!core) return;
    const startCol = fragmentStartCol + offset;
    const endCol = startCol + core.length;
    const lineCol = core.match(LINE_COL_RE);
    if (lineCol && lineCol.index !== undefined && lineCol.index > 0) {
      candidates.push({
        path: core.slice(0, lineCol.index),
        line: Number.parseInt(lineCol[1], 10),
        column: lineCol[2] ? Number.parseInt(lineCol[2], 10) : undefined,
        startCol,
        endCol,
      });
    }
    candidates.push({ path: core, startCol, endCol });
  };

  let lead = 0;
  while (lead < fragment.length - 1 && LEADING_WRAPPERS.includes(fragment[lead])) lead += 1;
  pushFrom(lead);
  for (let i = lead + 1; i < fragment.length; i += 1) {
    const character = fragment[i];
    if ((character === '/' || character === '~') && !/[A-Za-z0-9._~-]/.test(fragment[i - 1])) {
      pushFrom(i);
      break;
    }
  }
  return candidates.filter((candidate) => looksLikePath(candidate.path)).slice(0, 4);
}

export function resolveDetectedPath(path: string, cwd?: string, home?: string): string | null {
  let resolved: string;
  if (path.startsWith('/')) {
    resolved = path;
  } else if (path === '~' || path.startsWith('~/')) {
    if (!home) return null;
    resolved = home.replace(/\/$/, '') + path.slice(1);
  } else if (path.startsWith('~')) {
    // ~user expansion is not supported.
    return null;
  } else {
    if (!cwd) return null;
    resolved = `${cwd.replace(/\/$/, '')}/${path}`;
  }
  const segments: string[] = [];
  for (const segment of resolved.split('/')) {
    if (segment === '' || segment === '.') continue;
    if (segment === '..') {
      if (segments.length === 0) return null;
      segments.pop();
      continue;
    }
    segments.push(segment);
  }
  return `/${segments.join('/')}`;
}

export interface DetectedTerminalLink extends ColumnRange {
  kind: 'url' | 'path';
  uri?: string;
  absolutePath?: string;
  line?: number;
  column?: number;
}

export function isMarkdownPath(path: string): boolean {
  return /\.(md|markdown)$/i.test(path);
}

export type TerminalLinkOpenAction =
  | { action: 'open-url'; uri: string }
  | { action: 'open-path'; path: string }
  | { action: 'open-markdown'; path: string };

export function terminalLinkOpenAction(
  link: Pick<DetectedTerminalLink, 'kind' | 'uri' | 'absolutePath'>,
): TerminalLinkOpenAction | null {
  if (link.kind === 'url' && link.uri) {
    if (link.uri.startsWith('file://')) {
      const rest = link.uri.slice('file://'.length);
      const slashIndex = rest.indexOf('/');
      const path = decodeURIComponent(slashIndex === -1 ? rest : rest.slice(slashIndex));
      return isMarkdownPath(path)
        ? { action: 'open-markdown', path }
        : { action: 'open-path', path };
    }
    return { action: 'open-url', uri: link.uri };
  }
  if (link.kind === 'path' && link.absolutePath) {
    return isMarkdownPath(link.absolutePath)
      ? { action: 'open-markdown', path: link.absolutePath }
      : { action: 'open-path', path: link.absolutePath };
  }
  return null;
}

// Every row is padded to the grid width, so logical index i maps exactly to
// (firstRow + floor(i / cols), i % cols) and back.
export interface LogicalLine {
  text: string;
  firstRow: number;
  rowCount: number;
  cols: number;
}

// The cap bounds hover work, not correctness: a path spanning more rows would be
// hundreds of characters long.
export const MAX_WRAP_JOIN_ROWS = 6;

// isContinuationRow(r) answers "does row r continue the line started on row r-1"
// — the opposite direction from ghostty's own wrap flag.
export function logicalLineAt(
  rowTextAt: (viewportRow: number) => string,
  isContinuationRow: (viewportRow: number) => boolean,
  row: number,
  cols: number,
  rowCount: number,
): LogicalLine {
  let first = row;
  while (first > 0 && row - first < MAX_WRAP_JOIN_ROWS - 1 && isContinuationRow(first)) first -= 1;
  let last = row;
  while (last + 1 < rowCount && last - first < MAX_WRAP_JOIN_ROWS - 1 && isContinuationRow(last + 1)) last += 1;
  const parts: string[] = [];
  for (let current = first; current <= last; current += 1) {
    const text = rowTextAt(current);
    parts.push(current < last ? text.padEnd(cols, ' ') : text);
  }
  return { text: parts.join(''), firstRow: first, rowCount: last - first + 1, cols };
}

export function logicalIndexForCell(line: LogicalLine, row: number, col: number): number | null {
  if (row < line.firstRow || row >= line.firstRow + line.rowCount) return null;
  if (col < 0 || col >= line.cols) return null;
  return (row - line.firstRow) * line.cols + col;
}

// Rows strictly between startRow and endRow cover the full grid width (matches WebGlOverlay).
export interface LogicalSpan {
  startRow: number;
  startCol: number;
  endRow: number;
  endCol: number;
}

export function spanFromLogicalRange(line: LogicalLine, startIndex: number, endIndex: number): LogicalSpan {
  const lastIndex = Math.max(startIndex, endIndex - 1);
  return {
    startRow: line.firstRow + Math.floor(startIndex / line.cols),
    startCol: startIndex % line.cols,
    endRow: line.firstRow + Math.floor(lastIndex / line.cols),
    endCol: (lastIndex % line.cols) + 1,
  };
}
