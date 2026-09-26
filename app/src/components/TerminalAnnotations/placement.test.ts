import { describe, expect, it } from 'vitest';
import { clampToBounds, clampToViewport, placePopup, type Placement, type Point, type Rect, type Size } from './placement';

const VIEWPORT = { width: 1200, height: 800 };
const SCREEN = { left: 0, top: 0, ...VIEWPORT };
const POPUP = { width: 320, height: 120 };
const TALL = { width: 320, height: 300 };
const PANE = { left: 240, top: 0, width: 960, height: 800 };
const NARROW_PANE = { left: 240, top: 0, width: 200, height: 800 };
const PANEL = { left: 880, top: 380, width: 300, height: 400 };

function inside(at: Placement, size: Size, region: Rect): boolean {
  return at.left >= region.left
    && at.top >= region.top
    && at.left + size.width <= region.left + region.width
    && at.top + size.height <= region.top + region.height;
}

function overlaps(at: Placement, size: Size, other: Rect): boolean {
  return at.left < other.left + other.width
    && at.left + size.width > other.left
    && at.top < other.top + other.height
    && at.top + size.height > other.top;
}

interface PopupCase {
  anchor: Point;
  size?: Size;
  viewport?: Size;
  bounds?: Rect;
  avoid?: Rect;
  within: Rect;
  side?: 'above' | 'below';
  centred?: boolean;
  coversAvoid?: boolean;
}

describe('placePopup', () => {
  it.each<[string, PopupCase]>([
    ['sits above the anchor and centred on it when there is room', { anchor: { x: 600, y: 400 }, within: SCREEN, side: 'above', centred: true }],
    ['stays on screen when anchored at the right edge', { anchor: { x: 1196, y: 400 }, within: SCREEN, side: 'above' }],
    ['stays on screen when anchored at the left edge', { anchor: { x: 2, y: 400 }, within: SCREEN, side: 'above' }],
    ['flips below the anchor when it will not fit above', { anchor: { x: 600, y: 12 }, within: SCREEN, side: 'below', centred: true }],
    ['stays on screen when it fits neither above nor below', { anchor: { x: 600, y: 180 }, size: TALL, viewport: { width: 1200, height: 360 }, within: { left: 0, top: 0, width: 1200, height: 360 } }],
    ['stays inside its pane when anchored at the pane’s leading column', { anchor: { x: 252, y: 400 }, bounds: PANE, within: PANE, side: 'above' }],
    ['stays inside its pane when anchored at the pane’s trailing column', { anchor: { x: 1196, y: 400 }, bounds: PANE, within: PANE, side: 'above' }],
    ['spills out of a pane too narrow to hold it, but not off screen', { anchor: { x: 300, y: 400 }, bounds: NARROW_PANE, within: SCREEN, side: 'above' }],
    ['steps off the annotation panel rather than covering it', { anchor: { x: 1000, y: 600 }, bounds: PANE, avoid: PANEL, within: PANE, coversAvoid: false }],
    ['ignores a panel it already clears', { anchor: { x: 600, y: 400 }, bounds: PANE, avoid: PANEL, within: PANE, side: 'above', centred: true, coversAvoid: false }],
    ['covers the panel when nothing else fits in its pane', { anchor: { x: 700, y: 400 }, bounds: PANE, avoid: PANE, within: PANE, side: 'above', centred: true, coversAvoid: true }],
  ])('%s', (_name, c) => {
    const size = c.size ?? POPUP;
    const at = placePopup(c.anchor, size, c.viewport ?? VIEWPORT, { bounds: c.bounds, avoid: c.avoid });

    expect(inside(at, size, c.within)).toBe(true);
    if (c.side === 'above') expect(at.top + size.height).toBeLessThanOrEqual(c.anchor.y);
    if (c.side === 'below') expect(at.top).toBeGreaterThanOrEqual(c.anchor.y);
    if (c.centred) expect(at.left + size.width / 2).toBe(c.anchor.x);
    if (c.avoid) expect(overlaps(at, size, c.avoid)).toBe(c.coversAvoid);
  });
});

describe('dragged box clamping', () => {
  it.each<[string, (at: Placement) => Placement, Placement, Rect, boolean]>([
    ['leaves a box that fits the viewport where it is', (at) => clampToViewport(at, POPUP, VIEWPORT), { left: 300, top: 200 }, SCREEN, true],
    ['pulls a box past the bottom-right back into the viewport', (at) => clampToViewport(at, POPUP, VIEWPORT), { left: 1400, top: 900 }, SCREEN, false],
    ['pulls a box past the top-left back into the viewport', (at) => clampToViewport(at, POPUP, VIEWPORT), { left: -200, top: -80 }, SCREEN, false],
    ['leaves a box that fits its pane where it is', (at) => clampToBounds(at, POPUP, VIEWPORT, PANE), { left: 300, top: 200 }, PANE, true],
    ['keeps a box dragged out of its pane inside it', (at) => clampToBounds(at, POPUP, VIEWPORT, PANE), { left: 1400, top: 900 }, PANE, false],
    ['keeps a box left of its pane inside it', (at) => clampToBounds(at, POPUP, VIEWPORT, PANE), { left: 20, top: 200 }, PANE, false],
    ['uses the viewport when the pane cannot hold the box', (at) => clampToBounds(at, POPUP, VIEWPORT, { left: 240, top: 0, width: 200, height: 80 }), { left: 1400, top: 900 }, SCREEN, false],
    ['uses the viewport when there is no pane', (at) => clampToBounds(at, POPUP, VIEWPORT, null), { left: -200, top: 900 }, SCREEN, false],
  ])('%s', (_name, clamp, from, within, unchanged) => {
    const at = clamp(from);

    expect(inside(at, POPUP, within)).toBe(true);
    expect(at.left === from.left && at.top === from.top).toBe(unchanged);
  });
});
