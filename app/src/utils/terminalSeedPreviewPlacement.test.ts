import { describe, expect, it } from 'vitest';
import {
  terminalSeedPreviewPlacement,
  type TerminalSeedAnchor,
  type TerminalSeedPreviewSide,
} from './terminalSeedPreviewPlacement';

const viewport = { width: 1440, height: 900 };
const bounds = { left: 80, right: 1360, top: 70, bottom: 830 };
const size = { width: 390, height: 215 };

function anchorAt(edge: 'left' | 'right' | 'top' | 'bottom'): TerminalSeedAnchor {
  const positions = {
    left: { left: 92, right: 164, top: 430, bottom: 448 },
    right: { left: 1276, right: 1348, top: 430, bottom: 448 },
    top: { left: 684, right: 756, top: 82, bottom: 100 },
    bottom: { left: 684, right: 756, top: 800, bottom: 818 },
  };
  return { ...positions[edge], bounds };
}

describe('terminalSeedPreviewPlacement', () => {
  it.each([
    ['left', 'right'],
    ['right', 'left'],
    ['top', 'below'],
    ['bottom', 'above'],
  ] as const)('opens inward from the %s edge', (edge, expectedSide) => {
    const placement = terminalSeedPreviewPlacement(anchorAt(edge), size, viewport);

    expect(placement.side).toBe<TerminalSeedPreviewSide>(expectedSide);
    expect(placement.left).toBeGreaterThanOrEqual(bounds.left + 8);
    expect(placement.top).toBeGreaterThanOrEqual(bounds.top + 8);
    expect(placement.left + size.width).toBeLessThanOrEqual(bounds.right - 8);
    expect(placement.top + size.height).toBeLessThanOrEqual(bounds.bottom - 8);
    expect(placement.path).toMatch(/^M /);
  });

  it('defaults a roomy transcript position to the right', () => {
    const anchor: TerminalSeedAnchor = {
      left: 430,
      right: 502,
      top: 380,
      bottom: 398,
      bounds,
    };

    expect(terminalSeedPreviewPlacement(anchor, size, viewport).side).toBe('right');
  });

  it('falls back to the viewport when the terminal is smaller than the card', () => {
    const anchor: TerminalSeedAnchor = {
      left: 30,
      right: 102,
      top: 90,
      bottom: 108,
      bounds: { left: 20, right: 300, top: 50, bottom: 260 },
    };
    const placement = terminalSeedPreviewPlacement(
      anchor,
      { width: 304, height: 215 },
      { width: 320, height: 480 },
    );

    expect(placement.left).toBe(8);
    expect(placement.top).toBeGreaterThanOrEqual(8);
    expect(placement.left + 304).toBeLessThanOrEqual(312);
  });
});
