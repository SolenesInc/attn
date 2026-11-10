import remarkFrontmatter from 'remark-frontmatter';
import remarkGfm from 'remark-gfm';
import remarkParse from 'remark-parse';
import { unified } from 'unified';
import type { ReviewRow } from '../../types/generated';

/** One block of a document stop: its lines now and, when it changed, the
 * blocks it was. Blocks deleted just before it show above it. */
export interface DocBlock {
  from: number;
  to: number;
  source: string;
  kind: string;
  node: RootContent;
  changed: boolean;
  was: string | null;
  deletedBefore: string | null;
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

/** The blocks of one side of the rows: each with its source and the rows it holds. */
function sideBlocks(rows: readonly ReviewRow[]): Array<{ node: RootContent; rows: ReviewRow[]; source: string }> {
  return blockNodes(parser.parse(rows.map((row) => row.text).join('\n'))).map((node) => {
    const start = node.position?.start.line ?? 1;
    const end = node.position?.end.line ?? start;
    const held = rows.slice(start - 1, end);
    return { node, rows: held, source: held.map((row) => row.text).join('\n') };
  });
}

/**
 * The blocks of a document stop's rows: each top-level block, and each item of
 * a list, with the lines it holds now. A block now and a block before are one
 * block when they share an unchanged line, or when nothing but changed or
 * blank lines stands between the old one and the new one that replaced it. A
 * block changed when it has an added line, or the block it was has a removed
 * line; its old text is that whole block. A block before with no block now
 * was deleted, and shows above the next block, or after the last.
 */
export function docBlocks(rows: readonly ReviewRow[]): { blocks: DocBlock[]; removedAfter: string | null } {
  const now = sideBlocks(rows.filter((row) => row.now !== undefined));
  const before = sideBlocks(rows.filter((row) => row.old !== undefined));
  const position = new Map(rows.map((row, index) => [row, index]));
  const blockOfRow = new Map<ReviewRow, number>();
  now.forEach((block, index) => block.rows.forEach((row) => blockOfRow.set(row, index)));
  const blocks: DocBlock[] = now.map(({ node, rows: held, source }) => ({
    from: held[0]?.now ?? 0,
    to: held[held.length - 1]?.now ?? 0,
    source,
    kind: node.type === 'heading' ? `h${node.depth}` : node.type,
    node,
    changed: held.some((row) => row.kind === '+'),
    was: null,
    deletedBefore: null,
  }));
  const pairs = before.map((old) => [...new Set(old.rows.flatMap((row) => (blockOfRow.has(row) ? [blockOfRow.get(row)!] : [])))]);
  const pairedNow = new Set(pairs.flat());
  const span = (held: readonly ReviewRow[]) => [position.get(held[0]) ?? 0, position.get(held[held.length - 1]) ?? 0];
  now.forEach((block, index) => {
    if (pairedNow.has(index) || block.rows.length === 0) return;
    const [first] = span(block.rows);
    let replaced = -1;
    before.forEach((old, oldIndex) => {
      if (pairs[oldIndex].length === 0 && old.rows.length > 0 && span(old.rows)[1] < first) replaced = oldIndex;
    });
    if (replaced < 0) return;
    const between = rows.slice(span(before[replaced].rows)[1] + 1, first);
    if (between.some((row) => blockOfRow.has(row) || (row.kind === ' ' && row.text.trim() !== ''))) return;
    pairs[replaced] = [index];
  });
  const wasOf = new Map<DocBlock, string[]>();
  let deleted: string[] = [];
  before.forEach((old, oldIndex) => {
    const paired = pairs[oldIndex].map((index) => blocks[index]);
    if (paired.length === 0) {
      deleted.push(old.source);
      return;
    }
    const lost = old.rows.some((row) => row.kind === '-');
    for (const block of paired) {
      if (lost || paired.length > 1) block.changed = true;
      wasOf.set(block, [...(wasOf.get(block) ?? []), old.source]);
    }
    if (deleted.length > 0) {
      paired[0].deletedBefore = deleted.join('\n\n');
      deleted = [];
    }
  });
  for (const block of blocks) {
    if (block.changed && wasOf.has(block)) block.was = wasOf.get(block)!.join('\n\n');
  }
  return { blocks, removedAfter: deleted.length > 0 ? deleted.join('\n\n') : null };
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

const keyOf = (word: Word) => `${word.marks.join('+')}\u0000${word.href ?? ''}\u0000${word.text}`;

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
