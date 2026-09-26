import { describe, expect, it } from 'vitest';
import { placeQuickLabelPicker } from './QuickLabelPicker';

const VIEWPORT = { width: 1024, height: 768 };
const MID_SCREEN = { top: 100, bottom: 120, right: 230 };
const NEAR_BOTTOM = { top: 728, bottom: 748, right: 230 };

describe('placeQuickLabelPicker', () => {
  it.each([
    { placement: 'below the anchor, 28px left of the cursor', anchor: MID_SCREEN, cursor: { x: 300 }, height: 0, expected: { top: 126, left: 272 } },
    { placement: 'below the anchor when its height fits', anchor: MID_SCREEN, cursor: { x: 300 }, height: 300, expected: { top: 126, left: 272 } },
    { placement: 'centred on the anchor’s right edge without a cursor', anchor: MID_SCREEN, cursor: null, height: 0, expected: { top: 126, left: 134 } },
    { placement: 'clear of the left edge by 12px', anchor: MID_SCREEN, cursor: { x: 2 }, height: 0, expected: { top: 126, left: 12 } },
    { placement: 'clear of the right edge by 12px', anchor: MID_SCREEN, cursor: { x: 1000 }, height: 0, expected: { top: 126, left: 820 } },
    { placement: 'above the anchor when its height does not fit below', anchor: NEAR_BOTTOM, cursor: { x: 300 }, height: 300, expected: { top: 422, left: 272 } },
    { placement: 'clamped to the top padding when it fits neither above nor below', anchor: NEAR_BOTTOM, cursor: { x: 300 }, height: 768, expected: { top: 12, left: 272 } },
  ])('opens $placement', ({ anchor, cursor, height, expected }) => {
    expect(placeQuickLabelPicker(anchor, cursor, height, VIEWPORT)).toEqual(expected);
  });
});
