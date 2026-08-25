import { extractBlockTexts, ownerBlockFor } from './extractBlocks';
import { fnv1a32 } from './hash';
import type { AnchorRecord, BlockText } from './types';

/** Context window size for prefix/suffix (chars of rendered text). */
export const CONTEXT_CHARS = 32;

/** Rebase re-baselining must not fuzz-compound: every rebased record is rebuilt
 * from scratch against the new content. */
export function buildAnchor(
  blocks: BlockText[],
  contentHash: string,
  blockId: string,
  start: number,
  end: number,
): AnchorRecord | null {
  if (!blocks.some((b) => b.blockId === blockId)) {
    return null;
  }
  const owner = ownerBlockFor(blocks, blockId, start, end);
  const { block } = owner;
  if (
    owner.start < 0 ||
    owner.end > block.text.length ||
    owner.end <= owner.start
  ) {
    return null;
  }
  const exact = block.text.slice(owner.start, owner.end);
  if (exact.trim() === '') {
    return null;
  }
  return {
    blockId: block.blockId,
    startLine: block.startLine,
    endLine: block.endLine,
    exact,
    prefix: block.text.slice(Math.max(0, owner.start - CONTEXT_CHARS), owner.start),
    suffix: block.text.slice(owner.end, owner.end + CONTEXT_CHARS),
    start: owner.start,
    end: owner.end,
    contentHash,
  };
}

/** Null when the block is missing, the range is out of bounds or empty, or it
 * spans two sibling blocks. `blocks` must be `extractBlockTexts(content)`. */
export function createAnchor(
  content: string,
  blockId: string,
  start: number,
  end: number,
  blocks?: BlockText[],
): AnchorRecord | null {
  return buildAnchor(blocks ?? extractBlockTexts(content), fnv1a32(content), blockId, start, end);
}
