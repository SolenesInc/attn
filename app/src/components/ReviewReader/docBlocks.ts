import remarkFrontmatter from 'remark-frontmatter';
import remarkGfm from 'remark-gfm';
import remarkParse from 'remark-parse';
import { unified } from 'unified';
import type { ReviewRow } from '../../types/generated';

/** One block of a document stop: its lines now and, when it changed, the whole block it was. */
export interface DocBlock {
  from: number;
  to: number;
  source: string;
  kind: string;
  node: RootContent;
  added: boolean;
  was: string | null;
}

const parser = unified().use(remarkParse).use(remarkGfm).use(remarkFrontmatter);

type Root = ReturnType<typeof parser.parse>;
type RootContent = Root['children'][number];

/** The parts of a markdown node the word walk reads. */
interface InlineNode {
  type: string;
  value?: unknown;
  url?: string;
  children?: InlineNode[];
}

function blockNodes(root: Root): RootContent[] {
  return root.children.flatMap((node): RootContent[] => (node.type === 'list' ? node.children : [node]));
}

/**
 * The blocks of a document stop's rows: each top-level block, and each item of a
 * list, with the lines it holds now. Removed rows belong to the block of the
 * next line still in the file; a block with added lines or removed lines
 * changed, and its old text is its unchanged lines with the removed ones.
 */
export function docBlocks(rows: readonly ReviewRow[]): { blocks: DocBlock[]; removedAfter: string | null } {
  const shown = rows.filter((row) => row.now !== undefined);
  const source = shown.map((row) => row.text).join('\n');
  const root = parser.parse(source);
  const nowOf = (line: number) => shown[line - 1]?.now ?? 0;
  const blocks: DocBlock[] = blockNodes(root).map((node) => {
    const start = node.position?.start.line ?? 1;
    const end = node.position?.end.line ?? start;
    return {
      from: nowOf(start),
      to: nowOf(end),
      source: shown.slice(start - 1, end).map((row) => row.text).join('\n'),
      kind: node.type === 'heading' ? `h${node.depth}` : node.type,
      node,
      added: false,
      was: null,
    };
  });
  const old = new Map<DocBlock, string[]>();
  const lostRows = new Set<DocBlock>();
  let next = 0;
  let removed: string[] = [];
  for (const row of rows) {
    if (row.now === undefined) {
      if (row.kind === '-' && row.text.trim() !== '') removed.push(row.text);
      continue;
    }
    while (next < blocks.length && blocks[next].to < row.now) next += 1;
    const block = blocks[next];
    if (!block) continue;
    const before = old.get(block) ?? [];
    if (removed.length > 0) {
      lostRows.add(block);
      before.push(...removed);
      removed = [];
    }
    if (row.now >= block.from) {
      if (row.kind === '+') block.added = true;
      else before.push(row.text);
    }
    old.set(block, before);
  }
  for (const block of blocks) {
    const before = old.get(block) ?? [];
    if ((block.added || lostRows.has(block)) && before.length > 0) block.was = before.join('\n');
  }
  return { blocks, removedAfter: removed.length > 0 ? removed.join('\n') : null };
}

/** A word of rendered text with the inline marks around it. */
export interface Word {
  text: string;
  marks: readonly string[];
  href?: string;
}

function pushWords(value: string, marks: readonly string[], href: string | undefined, out: Word[]) {
  for (const word of value.split(/\s+/)) if (word) out.push({ text: word, marks, href });
}

function inlineWords(nodes: readonly InlineNode[], marks: readonly string[], href: string | undefined, out: Word[]) {
  for (const node of nodes) {
    if (node.type === 'inlineCode' && typeof node.value === 'string') pushWords(node.value, [...marks, 'code'], href, out);
    else if (node.type === 'emphasis' || node.type === 'strong' || node.type === 'delete') inlineWords(node.children ?? [], [...marks, node.type], href, out);
    else if (node.type === 'link') inlineWords(node.children ?? [], [...marks, 'link'], node.url, out);
    else if (node.children) inlineWords(node.children, marks, href, out);
    else if (typeof node.value === 'string') pushWords(node.value, marks, href, out);
  }
}

/** The words a block reads as once rendered, with their marks. */
export function renderedWords(source: string): Word[] {
  const out: Word[] = [];
  inlineWords(blockNodes(parser.parse(source)) as InlineNode[], [], undefined, out);
  return out;
}

export type WordOp = { op: '=' | '+' | '-'; word: Word };

/** Cells of the word table a diff may fill; the largest prose block in attn's
 * 332 markdown files is 588 words, about 345,000 cells against itself. */
export const MAX_WORD_DIFF_CELLS = 4_000_000;

const keyOf = (word: Word) => `${word.marks.join('+')}\u0000${word.text}`;

/** The word changes from was to now, or null when the table would pass MAX_WORD_DIFF_CELLS. */
export function diffWords(was: readonly Word[], now: readonly Word[]): WordOp[] | null {
  const n = was.length;
  const m = now.length;
  if ((n + 1) * (m + 1) > MAX_WORD_DIFF_CELLS) return null;
  const width = m + 1;
  const table = new Uint32Array((n + 1) * width);
  for (let i = n - 1; i >= 0; i -= 1) {
    for (let j = m - 1; j >= 0; j -= 1) {
      table[i * width + j] = keyOf(was[i]) === keyOf(now[j])
        ? table[(i + 1) * width + j + 1] + 1
        : Math.max(table[(i + 1) * width + j], table[i * width + j + 1]);
    }
  }
  const ops: WordOp[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (keyOf(was[i]) === keyOf(now[j])) {
      ops.push({ op: '=', word: now[j] });
      i += 1;
      j += 1;
    } else if (table[(i + 1) * width + j] >= table[i * width + j + 1]) {
      ops.push({ op: '-', word: was[i] });
      i += 1;
    } else {
      ops.push({ op: '+', word: now[j] });
      j += 1;
    }
  }
  while (i < n) ops.push({ op: '-', word: was[i++] });
  while (j < m) ops.push({ op: '+', word: now[j++] });
  return ops;
}

/** A changed block reads as a light rewording when at most half its words changed. */
export function isRewording(ops: readonly WordOp[]): boolean {
  const changed = ops.filter((entry) => entry.op !== '=').length;
  return changed * 2 <= ops.length;
}
