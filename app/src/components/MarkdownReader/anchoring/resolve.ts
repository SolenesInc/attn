import { extractBlockTexts } from './extractBlocks';
import { fnv1a32 } from './hash';
import { rebaseAnchor } from './rebase';
import type {
  AnchorRecord,
  BlockText,
  OrphanReason,
  ResolveOrRebaseResult,
  ResolveResult,
} from './types';

// Fast path when the hash matches; every other case delegates to `rebaseAnchor`. The
// re-baselined record is NOT returned here — use `resolveOrRebase` when persistence matters.
export function resolveAnchor(
  content: string,
  anchor: AnchorRecord,
  blocks?: BlockText[],
): ResolveResult {
  const result = resolveOrRebase(content, anchor, blocks);
  if (result.state === 'orphan') {
    return result;
  }
  return { state: 'exact', blockId: result.blockId, start: result.start, end: result.end };
}

// On `rebased` the caller must persist `anchor` so fuzz never compounds across edits.
// `blocks` must be `extractBlockTexts(content)`; without it every call re-runs the pipeline.
export function resolveOrRebase(
  content: string,
  anchor: AnchorRecord,
  blocks?: BlockText[],
): ResolveOrRebaseResult {
  const hash = fnv1a32(content);
  const allBlocks = blocks ?? extractBlockTexts(content);

  if (hash === anchor.contentHash) {
    const block = allBlocks.find((b) => b.blockId === anchor.blockId);
    if (block && block.text.slice(anchor.start, anchor.end) === anchor.exact) {
      return {
        state: 'exact',
        blockId: anchor.blockId,
        start: anchor.start,
        end: anchor.end,
        anchor,
      };
    }
    // Hash contract violated (pipeline drift?) — do not lie; try recovery.
    console.warn(
      '[md-anchoring] hash matched but coordinates are stale — pipeline change?',
      anchor.blockId,
      anchor.exact,
    );
    const violation: OrphanReason = block ? 'offset-mismatch' : 'block-missing';
    const recovered = rebaseAnchor(anchor, content, allBlocks);
    if (recovered.state === 'orphan') {
      return { state: 'orphan', reason: violation };
    }
    return rebasedResult(recovered.anchor);
  }

  const rebased = rebaseAnchor(anchor, content, allBlocks);
  if (rebased.state === 'orphan') {
    return rebased;
  }
  return rebasedResult(rebased.anchor);
}

function rebasedResult(anchor: AnchorRecord): ResolveOrRebaseResult {
  return {
    state: 'rebased',
    blockId: anchor.blockId,
    start: anchor.start,
    end: anchor.end,
    anchor,
  };
}
