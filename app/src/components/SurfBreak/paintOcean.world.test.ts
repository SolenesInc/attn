import { describe, expect, it } from 'vitest';
import { beaches } from './beaches';
import { Ocean } from './ocean';
import { drawOcean } from './paintOcean';
import { createWave, evolveWave, waveSection } from './water';

function canvasContract() {
  const rectangles: { x: number; y: number; w: number; h: number; color: string }[] = [];
  const stack: { fillStyle: string; globalAlpha: number }[] = [];
  let operations = 0;
  const check = (...numbers: number[]) => {
    if (!numbers.every(Number.isFinite)) throw new Error('Non-finite canvas coordinate');
    operations++;
  };
  const ctx = {
    fillStyle: '', globalAlpha: 1, imageSmoothingEnabled: true,
    beginPath() {}, closePath() {}, fill() {}, clip() {},
    moveTo: check, lineTo: check, translate: check,
    save() { stack.push({ fillStyle: this.fillStyle, globalAlpha: this.globalAlpha }); },
    restore() { Object.assign(this, stack.pop()); },
    fillRect(x: number, y: number, w: number, h: number) {
      check(x, y, w, h);
      if (w <= 0 || h <= 0) throw new Error('Negative canvas rectangle');
      rectangles.push({ x, y, w, h, color: this.fillStyle });
    },
  };
  return { ctx: ctx as unknown as CanvasRenderingContext2D, rectangles, stack, operations: () => operations };
}

describe('world-space ocean drawing', () => {
  it('projects the surfer and seabed into the same scrolling camera', () => {
    const ocean = new Ocean({ beach: 'reef' });
    ocean.x = 950; ocean.camera.x = 600; ocean.y = ocean.surface(ocean.x, ocean.z);
    const canvas = canvasContract(); drawOcean(canvas.ctx, ocean, false);
    const shirt = canvas.rectangles.filter(rect => rect.color === '#e97767');
    const surfer = ocean.project(ocean.x, ocean.y, ocean.z);
    expect(shirt.length).toBeGreaterThan(0);
    expect(shirt.every(rect => Math.abs(rect.x - surfer.x) < 25)).toBe(true);
    expect(canvas.rectangles.some(rect => rect.x === 0 && rect.w === 4 && rect.y === Math.round(ocean.floor(ocean.camera.x)))).toBe(true);
    expect(canvas.stack).toHaveLength(0);
    expect(canvas.ctx.globalAlpha).toBe(1);
  });

  it('keeps draw work local on a distant coast and handles a high camera', () => {
    const ocean = new Ocean({ beach: 'nazare' });
    ocean.x = 12000; ocean.camera.x = 11600; ocean.camera.y = -100;
    ocean.y = ocean.surface(ocean.x, ocean.z);
    const wave = createWave(0, beaches.nazare); wave.x = 1100; evolveWave(wave);
    ocean.waves.push(wave);
    const canvas = canvasContract(); drawOcean(canvas.ctx, ocean, false);
    expect(canvas.operations()).toBeLessThan(12000);
    expect(canvas.stack).toHaveLength(0);
    expect(canvas.rectangles.filter(rect => rect.color === '#e97767').every(rect => rect.x > 250 && rect.x < 500)).toBe(true);
  });

  it('bounds the textured wave drawing even with overlapping giant crests', () => {
    const ocean = new Ocean({ beach: 'nazare' });
    ocean.x = 1100; ocean.camera.x = 750;
    for (const x of [1300, 1900]) {
      const wave = createWave(x, beaches.nazare); wave.x = x; evolveWave(wave);
      ocean.waves.push(wave);
    }
    ocean.y = ocean.surface(ocean.x, ocean.z);
    const canvas = canvasContract(); drawOcean(canvas.ctx, ocean, false);
    // This overlapping-crest scene uses about 19,400 canvas operations.
    expect(canvas.operations()).toBeLessThan(25000);
    expect(canvas.stack).toHaveLength(0);
  });

  it.each(Object.values(beaches))('draws $name from walking through a shadowed wave without invalid geometry', beach => {
    const ocean = new Ocean({ beach: beach.id, start: 'beach' });
    drawOcean(canvasContract().ctx, ocean, false);
    const wave = createWave(0, beach); wave.x = beach.offshore * 0.3; evolveWave(wave);
    ocean.waves.push(wave); ocean.onFoot = false;
    ocean.x = waveSection(wave, ocean.z).center - 35;
    ocean.y = ocean.surface(ocean.x, ocean.z); ocean.camera.x = ocean.x - 400;
    const canvas = canvasContract(); drawOcean(canvas.ctx, ocean, false);
    expect(canvas.stack).toHaveLength(0);
    expect(canvas.ctx.imageSmoothingEnabled).toBe(false);
  });
});
