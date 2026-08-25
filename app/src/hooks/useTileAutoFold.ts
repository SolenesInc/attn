import { useLayoutEffect, useState, type RefObject } from 'react';

export const RAIL_FOLD_BELOW = 900;
export const TREE_FOLD_BELOW = 620;

export interface TileAutoFold {
  treeAutoFold: boolean;
  railAutoFold: boolean;
}

// A non-positive width means not measured yet: fold nothing, so a tile never flashes collapsed.
export function foldsForWidth(width: number): TileAutoFold {
  if (width <= 0) return { treeAutoFold: false, railAutoFold: false };
  return {
    railAutoFold: width < RAIL_FOLD_BELOW,
    treeAutoFold: width < TREE_FOLD_BELOW,
  };
}

// The initial measurement runs in a layout effect, before paint, so a narrow tile never flashes open.
export function useTileAutoFold(ref: RefObject<HTMLElement | null>, enabled: boolean): TileAutoFold {
  const [folds, setFolds] = useState<TileAutoFold>({ treeAutoFold: false, railAutoFold: false });

  useLayoutEffect(() => {
    if (!enabled) {
      setFolds({ treeAutoFold: false, railAutoFold: false });
      return;
    }
    const el = ref.current;
    if (!el) return;

// Observe the body CONTAINER, not its panes: folding must not shrink the observed box and oscillate.
    const apply = (width: number) => {
      setFolds((prev) => {
        const next = foldsForWidth(width);
        return prev.treeAutoFold === next.treeAutoFold && prev.railAutoFold === next.railAutoFold
          ? prev
          : next;
      });
    };
    apply(el.clientWidth);

    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver((entries) => {
      for (const entry of entries) {
        apply(entry.contentRect.width);
      }
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, [ref, enabled]);

  return folds;
}
