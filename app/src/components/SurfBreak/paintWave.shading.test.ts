import { describe, expect, it } from 'vitest';
import { createWave, evolveWave } from './ocean';
import { beaches } from './beaches';
import { drawLip, drawTube, tubeOpening, type WaterRect } from './paintWave';

function paintRecorder() {
  const colors: string[] = [];
  const alpha: number[] = [];
  const stack: { fillStyle: string; globalAlpha: number }[] = [];
  const polygons: { color: string; area: number }[] = [];
  let path: [number, number][] = [];
  let clips = 0;
  const context = {
    fillStyle: '', globalAlpha: 1,
    beginPath() { path = []; }, closePath() {},
    moveTo(x: number, y: number) { path.push([x, y]); },
    lineTo(x: number, y: number) { path.push([x, y]); },
    save() { stack.push({ fillStyle: this.fillStyle, globalAlpha: this.globalAlpha }); },
    restore() { Object.assign(this, stack.pop()); },
    clip() { clips++; },
    fill() {
      colors.push(this.fillStyle); alpha.push(this.globalAlpha);
      const area = Math.abs(path.reduce((sum, [x, y], i) => {
        const next = path[(i + 1) % path.length]; return sum + x * next[1] - y * next[0];
      }, 0)) / 2;
      polygons.push({ color: this.fillStyle, area });
    },
  };
  const rect: WaterRect = (x, y, w, h, color) => {
    expect([x, y, w, h].every(Number.isFinite)).toBe(true);
    context.fillStyle = color;
    colors.push(color); alpha.push(context.globalAlpha);
  };
  return { ctx: context as unknown as CanvasRenderingContext2D, rect, colors, alpha, stack, polygons, clips: () => clips };
}

const brightness = (hex: string) => {
  const channels = hex.slice(1).match(/.{2}/g)!.map(value => parseInt(value, 16));
  return channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722;
};

describe('the shadow below the wave lip', () => {
  it('fills a thick crest with water and textured foam instead of tracing narrow rims', () => {
    const wave = createWave(0, beaches.nazare);
    wave.x = 1400; evolveWave(wave);
    const mouth = paintRecorder();
    drawLip(mouth.ctx, mouth.rect, wave, 10, 0);
    const body = mouth.polygons.filter(p => p.color === '#3198ac');
    expect(body).toHaveLength(1);
    expect(body[0].area).toBeGreaterThan(wave.height ** 2 * 0.15);
    expect(mouth.colors).toContain('#e3fff0');
    expect(mouth.colors).toContain('#62cfcc');
    expect(mouth.stack).toHaveLength(0);
  });

  it('paints a dark clipped interior and a clearly brighter mouth', () => {
    const wave = createWave(0);
    wave.height = wave.amplitude; wave.curl = 1;
    const interior = paintRecorder();
    drawTube(interior.ctx, interior.rect, wave, 10);
    const mouth = paintRecorder();
    drawLip(mouth.ctx, mouth.rect, wave, 10, 0);
    expect(interior.clips()).toBe(1);
    expect(Math.min(...interior.colors.map(brightness))).toBeLessThan(65);
    expect(Math.max(...mouth.colors.map(brightness))).toBeGreaterThan(230);
    expect(interior.stack).toHaveLength(0);
    expect(interior.ctx.globalAlpha).toBe(1);
  });

  it('fades the shadow in as a tube forms and leaves low rollers open', () => {
    const wave = createWave(0);
    wave.height = wave.amplitude; wave.curl = 0.21;
    const forming = paintRecorder();
    drawTube(forming.ctx, forming.rect, wave, 10);
    expect(forming.alpha.length).toBeGreaterThan(0);
    expect(Math.max(...forming.alpha)).toBeLessThan(0.05);
    const roller = createWave(1);
    roller.height = roller.amplitude; roller.curl = roller.shape.maxCurl;
    const low = paintRecorder();
    drawTube(low.ctx, low.rect, roller, 10);
    expect(low.colors).toHaveLength(0);
  });

  it('keeps shadow outlines finite and lower than their roof across profiles and depth', () => {
    for (let id = 0; id < 12; id++) {
      const wave = createWave(id);
      wave.height = wave.amplitude; wave.curl = wave.shape.maxCurl;
      for (const z of [0, 40, 100]) {
        const { ctx } = paintRecorder();
        const bounds = tubeOpening(ctx, wave, z);
        expect(Object.values(bounds).every(Number.isFinite)).toBe(true);
        expect(bounds.right).toBeGreaterThan(bounds.left);
        expect(bounds.bottom).toBeGreaterThan(bounds.top);
      }
    }
  });

  it('changes the cavity with the wave and fades its shadow during collapse', () => {
    const wave = createWave(0, beaches.reef);
    wave.x = 900; evolveWave(wave);
    const wall = tubeOpening(paintRecorder().ctx, wave, 0);
    wave.x = 550; evolveWave(wave);
    const hollow = tubeOpening(paintRecorder().ctx, wave, 0);
    expect(hollow.right - hollow.left).toBeGreaterThan((wall.right - wall.left) * 1.1);
    const open = paintRecorder();
    drawTube(open.ctx, open.rect, wave, 16);
    for (let i = 0; i < 8 * 60; i++) { wave.x -= wave.speed / 60; evolveWave(wave, 1 / 60); }
    const breaking = paintRecorder();
    drawTube(breaking.ctx, breaking.rect, wave, 22);
    expect(Math.max(0, ...breaking.alpha)).toBeLessThan(Math.max(...open.alpha) * 0.5);
  });
});
