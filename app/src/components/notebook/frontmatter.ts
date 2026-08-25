// Parses only the YAML subset notebook frontmatter uses: `key: value`, flow lists
// `[a, b]`, block lists. Nested maps and block scalars are out of scope.

import type { Text } from '@codemirror/state';

export type FrontmatterValue = string | string[];

export interface Frontmatter {
  fields: Record<string, FrontmatterValue>;
  // `from` is always 0; `to` is the start of the body, the whole-line boundary a
  // CodeMirror block decoration needs.
  from: number;
  to: number;
}

function isFence(line: string): boolean {
  const t = line.trim();
  return t === '---' || t === '...';
}

function stripQuotes(s: string): string {
  if (s.length >= 2) {
    const q = s[0];
    if ((q === '"' || q === "'") && s[s.length - 1] === q) return s.slice(1, -1);
  }
  return s;
}

function parseFields(lines: string[]): Record<string, FrontmatterValue> {
  const out: Record<string, FrontmatterValue> = {};
  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i];
    if (!line.trim() || line.trimStart().startsWith('#')) continue;
    const m = /^([A-Za-z0-9_][\w-]*):[ \t]?(.*)$/.exec(line);
    if (!m) continue;
    const key = m[1];
    const rest = m[2].trim();
    if (rest === '') {
      const items: string[] = [];
      while (i + 1 < lines.length && /^\s*-\s+/.test(lines[i + 1])) {
        items.push(stripQuotes(lines[i + 1].replace(/^\s*-\s+/, '').trim()));
        i += 1;
      }
      out[key] = items.length ? items : '';
    } else if (rest.startsWith('[') && rest.endsWith(']')) {
      out[key] = rest
        .slice(1, -1)
        .split(',')
        .map((s) => stripQuotes(s.trim()))
        .filter((s) => s.length > 0);
    } else {
      out[key] = stripQuotes(rest);
    }
  }
  return out;
}

export function parseFrontmatter(doc: string): Frontmatter | null {
  const lines = doc.split('\n');
  // Frontmatter must OPEN on the very first line; a `---` mid-document is a rule.
  if (lines.length === 0 || lines[0].trim() !== '---') return null;
  let close = -1;
  for (let i = 1; i < lines.length; i += 1) {
    if (isFence(lines[i])) {
      close = i;
      break;
    }
  }
  if (close === -1) return null; // unterminated → treat as ordinary content, not frontmatter
  let to = 0;
  for (let i = 0; i <= close; i += 1) to += lines[i].length + 1;
  to = Math.min(to, doc.length);
  return { fields: parseFields(lines.slice(1, close)), from: 0, to };
}

// Shared by every editor extension, so a fence that closes beyond it is
// not-frontmatter everywhere.
export const FRONTMATTER_SCAN_LIMIT = 4096;

export function parseFrontmatterFromDoc(doc: Text): Frontmatter | null {
  return parseFrontmatter(doc.sliceString(0, Math.min(doc.length, FRONTMATTER_SCAN_LIMIT)));
}
