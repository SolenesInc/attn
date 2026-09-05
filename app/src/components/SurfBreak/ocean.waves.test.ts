import { describe, expect, it } from 'vitest';
import { beaches, type BeachId } from './beaches';
import { createWave, curlPoint, evolveWave, Ocean, OCEAN_HEIGHT, OCEAN_WIDTH, restingInput, SEA_LEVEL, waveLift, waveSection } from './ocean';

describe('wave profiles', () => {
  it('gives each beach a characteristic shape with variation within sets', () => {
    const cove = createWave(0, beaches.cove);
    const reef = createWave(0, beaches.reef);
    const point = createWave(0, beaches.point);
    expect(cove.amplitude).toBeLessThan(reef.amplitude / 2);
    expect(point.shape.crestLength).toBeGreaterThan(reef.shape.crestLength * 3);
    expect(cove.shape.maxCurl).toBeLessThan(0.3);
    expect(reef.shape.maxCurl).toBe(1);
    expect(createWave(4).shape.frontWidth).not.toBe(createWave(0).shape.frontWidth);
    expect(createWave(4)).toEqual(createWave(4));
  });

  it('joins both shoulders continuously and includes a trough ahead of the face', () => {
    for (const beach of Object.values(beaches)) {
      const wave = createWave(0, beach); wave.x = beach.offshore * 0.3; evolveWave(wave);
      for (const z of [0, 50, 100]) {
        const section = waveSection(wave, z);
        expect(waveLift(wave, section.center + section.crestLength / 2, z)).toBe(section.height);
        for (const edge of [section.center, section.center + section.crestLength]) {
          expect(Math.abs(waveLift(wave, edge - 0.01, z) - waveLift(wave, edge + 0.01, z))).toBeLessThan(0.01);
        }
        expect(waveLift(wave, section.center - section.frontWidth * 1.8, z)).toBeLessThan(0);
      }
    }
  });

  it('changes tube height and curl across the seabed, keeping its root attached', () => {
    const wave = createWave(0, beaches.point); wave.x = 800; evolveWave(wave);
    const near = waveSection(wave, 0); const far = waveSection(wave, 100);
    expect(far.center).toBeGreaterThan(near.center + 100);
    expect(far.height).toBeLessThan(near.height);
    expect(far.curl).toBeLessThan(near.curl);
    for (const z of [0, 50, 100]) {
      const root = curlPoint(wave, 0, 0, z);
      expect(root.y).toBeCloseTo(SEA_LEVEL - waveLift(wave, root.x, z), 8);
    }
  });

  it('forms a tube by carrying one roof left before that same lip falls', () => {
    const wave = createWave(0, beaches.nazare, undefined, 1400);
    const root = curlPoint(wave, 0);
    const roof = curlPoint(wave, 0.72);
    const tip = curlPoint(wave, 1);
    expect(root.x - roof.x).toBeGreaterThan((root.x - tip.x) * 0.85);
    expect(roof.y - root.y).toBeLessThan((tip.y - root.y) * 0.12);
    expect(tip.y - roof.y).toBeGreaterThan((tip.y - root.y) * 0.8);
    let previous = root;
    for (let i = 1; i <= 40; i++) {
      const point = curlPoint(wave, i / 40);
      expect(point.x).toBeLessThanOrEqual(previous.x);
      previous = point;
    }
  });

  it('varies sandbar sets and leaves calm local water between them', () => {
    const ocean = new Ocean({ conditions: { size: 'usual', rhythm: 'quiet' } });
    const shapes = new Set<string>();
    let calm = 0;
    for (let frame = 0; frame < 150 * 30; frame++) {
      ocean.step(1 / 30, restingInput());
      for (const wave of ocean.waves) shapes.add(wave.shape.kind);
      if (ocean.time > 30 && Math.abs(ocean.surface(500) - SEA_LEVEL) < 2) calm++;
    }
    expect(shapes).toEqual(new Set(['peeler', 'roller', 'hollow', 'runner']));
    expect(calm).toBeGreaterThan(10 * 30);
  });

  it('steepens the body over the reef, then broadens and loses height during collapse', () => {
    const wave = createWave(0, beaches.reef);
    const swell = waveSection(wave);
    wave.x = 550; evolveWave(wave);
    const hollow = waveSection(wave);
    const height = wave.height;
    expect(hollow.frontWidth).toBeLessThan(swell.frontWidth * 0.6);
    expect(wave.curl).toBeGreaterThan(0.8);
    for (let i = 0; i < 12 * 60; i++) { wave.x -= wave.speed / 60; evolveWave(wave, 1 / 60); }
    const broken = waveSection(wave);
    expect(broken.frontWidth).toBeGreaterThan(hollow.frontWidth * 2);
    expect(wave.height).toBeLessThan(height * 0.2);
  });

  it.each(Object.keys(beaches) as BeachId[])('keeps the %s lip and surface finite through the full ride', id => {
    const wave = createWave(0, beaches[id]);
    for (let frame = 0; frame < 90 * 30 && wave.x > 0; frame++) {
      wave.x -= wave.speed / 30; evolveWave(wave, 1 / 30);
      if (frame % 15) continue;
      for (const z of [0, 50, 100]) {
        const root = curlPoint(wave, 0, 7, z);
        expect(root.y).toBeCloseTo(SEA_LEVEL - waveLift(wave, root.x, z), 6);
        for (let i = 0; i <= 10; i++) expect(Object.values(curlPoint(wave, i / 10, -5, z)).every(Number.isFinite)).toBe(true);
      }
    }
  });

  it('retains the wider fixed-resolution view while the camera scrolls', () => {
    expect([OCEAN_WIDTH, OCEAN_HEIGHT]).toEqual([800, 450]);
    const ocean = new Ocean();
    const size = ocean.project(ocean.x, ocean.y, 30).scale;
    ocean.camera.x = 12000;
    expect(ocean.project(12500, ocean.y, 30).scale).toBe(size);
  });
});
