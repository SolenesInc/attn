import { describe, expect, it } from 'vitest';
import { Ocean } from './ocean';
import { drawSurfer } from './paintSurfer';

function shirtBounds(ocean: Ocean) {
  const points: number[] = [];
  const ctx = {
    fillStyle: '', beginPath() {}, closePath() {}, fill() {}, moveTo() {}, lineTo() {},
    fillRect(_x: number, y: number, _w: number, h: number) {
      if (this.fillStyle === '#e97767') points.push(y, y + h);
    },
  };
  drawSurfer(ctx as unknown as CanvasRenderingContext2D, ocean, 0);
  return { top: Math.min(...points), bottom: Math.max(...points) };
}

describe('surfer posture drawing', () => {
  it('lies along the board at rest and raises the body through the pop-up transition', () => {
    const ocean = new Ocean();
    const prone = shirtBounds(ocean);
    ocean.standingBlend = 0.5;
    const halfway = shirtBounds(ocean);
    ocean.standingBlend = 1;
    const standing = shirtBounds(ocean);
    expect(prone.bottom - prone.top).toBeLessThan(6);
    expect(standing.top).toBeLessThan(prone.top - 9);
    expect(halfway.top).toBeGreaterThan(standing.top);
    expect(halfway.top).toBeLessThan(prone.top);
  });

  it('draws the horizontal swimming pose underwater even while the transition finishes', () => {
    const ocean = new Ocean();
    ocean.standingBlend = 1;
    ocean.y += 35;
    const bounds = shirtBounds(ocean);
    expect(bounds.bottom - bounds.top).toBeLessThan(6);
  });
});
