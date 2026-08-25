// Receipt (2026-08-19, two recorded nisse streams replayed delta by delta): a 7,845-char
// reply leaked a bare backtick in 21 prefixes; everything above the open tail was stable.

export const PENDING_DIAGRAM_LANGUAGE = 'attn-pending-diagram';

const FENCE = /^(\s*)(`{3,}|~{3,})(.*)$/;
const BARE_MARKER = /^\s*(#{1,6}|[-*+>]|\d{1,9}[.)]|\|)\s*$/;
const TABLE_ROW = /^\s*\|/;
const TABLE_DELIMITER = /^\s*\|?\s*:?-{1,}:?\s*(\|\s*:?-{1,}:?\s*)*\|?\s*$/;
const PARTIAL_TABLE_DELIMITER = /^\s*\|[\s:|-]*$/;

interface FenceState {
  openedAt: number;
  marker: string;
  language: string;
}

function openFence(lines: string[]): FenceState | null {
  let open: FenceState | null = null;
  for (let i = 0; i < lines.length; i++) {
    const match = FENCE.exec(lines[i]);
    if (!match) continue;
    const [, , marker, info] = match;
    if (open === null) {
      open = { openedAt: i, marker, language: info.trim().split(/\s+/)[0] ?? '' };
    } else if (marker[0] === open.marker[0] && marker.length >= open.marker.length && info.trim() === '') {
      open = null;
    }
  }
  return open;
}

function closeInline(line: string): string {
  let out = line;

  const runs: Array<{ index: number; length: number }> = [];
  for (let i = 0; i < out.length; i++) {
    if (out[i] !== '`') continue;
    let j = i;
    while (j < out.length && out[j] === '`') j++;
    runs.push({ index: i, length: j - i });
    i = j - 1;
  }
  const openRun = unpairedRun(runs);
  const codeRanges: Array<[number, number]> = [];
  for (let i = 0; i + 1 < runs.length; i += 1) {
    if (runs[i].length === runs[i + 1].length) {
      codeRanges.push([runs[i].index, runs[i + 1].index + runs[i + 1].length]);
      i += 1;
    }
  }
  if (openRun !== null) {
    if (openRun.index + openRun.length === out.length) return out.slice(0, openRun.index);
    return `${out}${'`'.repeat(openRun.length)}`;
  }

  const outside = (index: number) => !codeRanges.some(([start, end]) => index >= start && index < end);
  for (const delimiter of ['**', '__', '~~']) {
    let last = -1;
    let count = 0;
    for (let i = 0; i + delimiter.length <= out.length; i++) {
      if (out.startsWith(delimiter, i) && outside(i)) { count++; last = i; i += delimiter.length - 1; }
    }
    if (count % 2 === 0) continue;
    out = last + delimiter.length === out.length
      ? out.slice(0, last)
      : `${out}${delimiter}`;
  }

  const linkOpen = out.lastIndexOf('](');
  if (linkOpen !== -1 && outside(linkOpen) && out.indexOf(')', linkOpen) === -1) out = `${out})`;
  return out;
}

function unpairedRun(runs: Array<{ index: number; length: number }>): { index: number; length: number } | null {
  const stack: Array<{ index: number; length: number }> = [];
  for (const run of runs) {
    const match = stack.findIndex((candidate) => candidate.length === run.length);
    if (match === -1) stack.push(run);
    else stack.splice(match, 1);
  }
  return stack.length > 0 ? stack[stack.length - 1] : null;
}

function cellCount(row: string): number {
  const trimmed = row.trim().replace(/^\|/, '').replace(/\|$/, '');
  return trimmed.split('|').length;
}

export function prepareStreamingMarkdown(text: string): string {
  if (text === '') return text;
  const lines = text.split('\n');
  const fence = openFence(lines);

  if (fence) {
    if (fence.language === 'mermaid') {
      lines[fence.openedAt] = lines[fence.openedAt].replace(/mermaid/, PENDING_DIAGRAM_LANGUAGE);
    }
    return `${lines.join('\n')}\n${fence.marker}`;
  }

  const lastIndex = lines.length - 1;
  const last = lines[lastIndex];
  if (last === '') return text;

  if (BARE_MARKER.test(last)) return lines.slice(0, lastIndex).join('\n');

  // A fence on the last line here is a CLOSING one — an opening fence took the
  // case above — so its backticks are block syntax the inline pass would misread.
  if (FENCE.test(last)) return text;

  const previousLine = lastIndex > 0 ? lines[lastIndex - 1] : '';
  lines[lastIndex] = closeInline(last);
  const tail = lines[lastIndex];

  // GFM matches the delimiter row against the header's width, so a delimiter
  // that is complete but too narrow is still not a table.
  if (PARTIAL_TABLE_DELIMITER.test(tail) && TABLE_ROW.test(previousLine)
      && cellCount(tail) < cellCount(previousLine)) {
    lines[lastIndex] = `|${' --- |'.repeat(cellCount(previousLine))}`;
    return lines.join('\n');
  }

  if (TABLE_ROW.test(tail) && cellCount(tail) >= 2
      && !TABLE_ROW.test(previousLine) && !TABLE_DELIMITER.test(previousLine)) {
    return `${lines.join('\n')}\n|${' --- |'.repeat(cellCount(tail))}`;
  }

  return lines.join('\n');
}

// Measured on the recorded 27,540-char reply: parse is 8.84 ms of the bill, so not
// re-parsing the settled prefix is the win. A loose list's blank line is NOT a safe cut.

const HEADING_AT_MARGIN = /^#{1,6} /;
const CLOSING_FENCE = /^(`{3,}|~{3,})\s*$/;
const OPENING_FENCE = /^(`{3,}|~{3,})/;
const CROSS_REFERENCING = /^\[[^\]]+\]:|\[\^[^\]]+\]/m;

export interface SplitMarkdown {
  settled: string;
  tail: string;
}

export function splitStreamingMarkdown(text: string): SplitMarkdown {
  if (CROSS_REFERENCING.test(text)) return { settled: '', tail: text };
  const lines = text.split('\n');
  let cut = -1;
  let fence: string | null = null;
  for (let i = 0; i < lines.length - 1; i++) {
    const line = lines[i];
    if (fence !== null) {
      if (CLOSING_FENCE.test(line) && line.startsWith(fence)) {
        fence = null;
        if (lines[i + 1] === '') cut = i + 1;
      }
      continue;
    }
    const opening = OPENING_FENCE.exec(line);
    if (opening) { fence = opening[1]; continue; }
    if (i > 0 && lines[i - 1] === '' && HEADING_AT_MARGIN.test(line)) cut = i - 1;
  }
  if (cut <= 0) return { settled: '', tail: text };
  return { settled: lines.slice(0, cut).join('\n'), tail: lines.slice(cut + 1).join('\n') };
}
