
export interface FrontmatterEntry {
  key: string;
  value: string | string[];
}

export interface ExtractedFrontmatter {
  entries: FrontmatterEntry[];
  lineCount: number;
}

const NONE: ExtractedFrontmatter = { entries: [], lineCount: 0 };

function unquote(value: string): string {
  if (value.length >= 2 && ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'")))) {
    return value.slice(1, -1);
  }
  return value;
}

function parseInlineArray(value: string): string[] | null {
  if (!value.startsWith('[') || !value.endsWith(']')) {
    return null;
  }
  const inner = value.slice(1, -1).trim();
  if (!inner) {
    return [];
  }
  return inner.split(',').map((item) => unquote(item.trim())).filter((item) => item.length > 0);
}

// Must match micromark-extension-frontmatter, which remark-frontmatter uses to
// swallow the block: accepting anything it rejects renders the card AND the raw YAML.
const FENCE = /^---[ \t]*$/;

export function extractFrontmatter(content: string): ExtractedFrontmatter {
  // Normalize CRLF once: JS `.` and `$` both stop at a carriage return.
  const lines = content.split('\n').map((line) => (line.endsWith('\r') ? line.slice(0, -1) : line));
  if (!FENCE.test(lines[0] ?? '')) {
    return NONE;
  }
  let closing = -1;
  for (let i = 1; i < lines.length; i++) {
    if (FENCE.test(lines[i])) {
      closing = i;
      break;
    }
  }
  if (closing === -1) {
    return NONE;
  }

  const entries: FrontmatterEntry[] = [];
  let pendingListKey: string | null = null;
  let pendingList: string[] = [];
  const flushPendingList = () => {
    // A pending key with no dash items was a nested object or empty value.
    if (pendingListKey !== null && pendingList.length > 0) {
      entries.push({ key: pendingListKey, value: pendingList });
    }
    pendingListKey = null;
    pendingList = [];
  };

  for (let i = 1; i < closing; i++) {
    const line = lines[i];
    if (!line.trim() || line.trim().startsWith('#')) {
      continue;
    }
    const dashItem = line.match(/^\s+-\s+(.*)$/);
    if (dashItem && pendingListKey !== null) {
      pendingList.push(unquote(dashItem[1].trim()));
      continue;
    }
    flushPendingList();
    // Only top-level `key: value` lines; indented lines (nested objects) skip.
    const keyed = line.match(/^([A-Za-z0-9_-]+)\s*:(.*)$/);
    if (!keyed) {
      continue;
    }
    const key = keyed[1];
    const rawValue = keyed[2].trim();
    if (!rawValue) {
      pendingListKey = key;
      pendingList = [];
      continue;
    }
    const inlineArray = parseInlineArray(rawValue);
    entries.push({ key, value: inlineArray ?? unquote(rawValue) });
  }
  flushPendingList();

  return { entries, lineCount: closing + 1 };
}
