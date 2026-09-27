export const QUICK_LABEL_PICKER_WIDTH = 192;
const GAP = 6;
const VIEWPORT_PADDING = 12;

export function placeQuickLabelPicker(
  anchor: Pick<DOMRect, 'top' | 'bottom' | 'right'>,
  cursorHint: { x: number } | null | undefined,
  height: number,
  viewport: { width: number; height: number },
): { top: number; left: number } {
  const below = anchor.bottom + GAP;
  const above = anchor.top - GAP - height;
  const lowestTop = viewport.height - VIEWPORT_PADDING - height;
  let top = below;
  if (height > 0 && below > lowestTop) {
    top = above >= VIEWPORT_PADDING ? above : Math.max(VIEWPORT_PADDING, lowestTop);
  }

  let left = cursorHint ? cursorHint.x - 28 : anchor.right - QUICK_LABEL_PICKER_WIDTH / 2;
  left = Math.max(
    VIEWPORT_PADDING,
    Math.min(left, viewport.width - QUICK_LABEL_PICKER_WIDTH - VIEWPORT_PADDING),
  );
  return { top, left };
}
