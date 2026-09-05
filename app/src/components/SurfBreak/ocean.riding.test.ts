import { describe, expect, it } from 'vitest';
import { beaches, defaultConditions, type SurfConditions } from './beaches';
import { Ocean, restingInput } from './ocean';
import { barrelRoof, createWave, curlPoint, evolveWave, SEA_LEVEL, waveLift, waveSection } from './water';

function incoming(beach: 'point' | 'nazare', id = 0, size: SurfConditions['size'] = 'usual', z = 30) {
  const ocean = new Ocean({ beach, conditions: { size, rhythm: 'quiet' } });
  const wave = createWave(id, beaches[beach], ocean.conditions);
  while (wave.x > (beach === 'point' ? 1700 : 2200)) {
    wave.x -= wave.speed / 120; evolveWave(wave, 1 / 120);
  }
  const section = waveSection(wave, z);
  ocean.waves.push(wave); ocean.z = z;
  ocean.x = section.center - section.frontWidth * 1.2;
  ocean.y = ocean.surface(ocean.x, z);
  return { ocean, wave };
}

function ride(scene: ReturnType<typeof incoming>, action: 'ride' | 'turn' | 'dive' = 'ride') {
  const { ocean, wave } = scene;
  const start = ocean.x;
  let stood = false; let barrel = 0; let continuous = 0; let longest = 0; let speed = 0;
  for (let i = 0; i < 32 * 60; i++) {
    const turn = action === 'turn' && barrel > 1 && wave.breaking > 0.08;
    const dive = action === 'dive' && barrel > 1;
    ocean.step(1 / 60, { ...restingInput(), left: !turn, right: turn, dive,
      posture: !stood && ocean.canStand && -ocean.vx > wave.speed * 0.8 });
    stood ||= ocean.posture === 'standing';
    if (ocean.barrel === wave && ocean.posture === 'standing') { barrel += 1 / 60; continuous += 1 / 60; }
    else continuous = 0;
    longest = Math.max(longest, continuous); speed = Math.max(speed, -ocean.vx);
    if (ocean.recovery) break;
  }
  return { stood, longest, speed, distance: start - ocean.x };
}

describe('catching and riding with player input', () => {
  it.each(['point', 'nazare'] as const)('catches %s from rest, pops up and rides inside a barrel across a viewport', beach => {
    const scene = incoming(beach);
    expect(scene.ocean.boardSpeed).toBe(0);
    expect(scene.ocean.posture).toBe('prone');
    const result = ride(scene);
    expect(result.stood).toBe(true);
    expect(result.longest).toBeGreaterThan(10);
    expect(result.distance).toBeGreaterThan(800);
    expect(result.speed).toBeGreaterThan(40);
    expect(result.speed).toBeLessThan(100);
  });

  it.each([
    ['point', 2, 'small'], ['point', 4, 'large'], ['nazare', 2, 'small'], ['nazare', 4, 'large'],
  ] as const)('can catch %s wave %i with %s conditions', (beach, id, size) => {
    const scene = incoming(beach, id, size);
    const result = ride(scene);
    expect(result.stood).toBe(true);
    expect(result.longest).toBeGreaterThan(8);
    expect(result.distance).toBeGreaterThan(400);
  });

  it('penalizes a slow reversal inside a giant wave and gives paddle control back after the wipeout', () => {
    const scene = incoming('nazare');
    const result = ride(scene, 'turn');
    expect(result.longest).toBeGreaterThan(1);
    expect(scene.ocean.wipeoutCause).toBe('stall');
    expect(scene.ocean.state).toBe('recovering');
    expect(scene.ocean.canStand).toBe(false);
    for (let i = 0; i < 2.2 * 60; i++) scene.ocean.step(1 / 60, { ...restingInput(), right: true });
    expect(scene.ocean.recovery).toBe(0);
    expect(scene.ocean.wipeoutCause).toBeNull();
    for (let i = 0; i < 3 * 60; i++) scene.ocean.step(1 / 60, { ...restingInput(), right: true });
    const x = scene.ocean.x;
    for (let i = 0; i < 60; i++) scene.ocean.step(1 / 60, { ...restingInput(), right: true });
    expect(scene.ocean.x).toBeGreaterThan(x);
    expect(scene.ocean.vx).toBeGreaterThan(20);
    expect(scene.ocean.paddling).toBe(true);
    expect(scene.ocean.posture).toBe('prone');
  });

  it.each([0, 30, 90])('can be caught by a closing giant face on depth line %i', z => {
    const scene = incoming('nazare', 0, 'usual', z);
    const result = ride(scene);
    expect(result.longest).toBeGreaterThan(10);
    expect(scene.ocean.wipeoutCause).toBe('closeout');
    expect(scene.ocean.state).toBe('recovering');
  });

  it('can duck out of the barrel without a forced wipeout or losing paddle control', () => {
    const scene = incoming('nazare');
    expect(ride(scene, 'dive').longest).toBeGreaterThanOrEqual(1);
    expect(scene.ocean.recovery).toBe(0);
    expect(scene.ocean.wipeoutCause).toBeNull();
    expect(scene.ocean.posture).toBe('prone');
    expect(scene.ocean.paddling).toBe(true);
  });
});

describe('room inside the moving barrel', () => {
  it.each(['point', 'nazare'] as const)('%s keeps an opening several board lengths wide with standing headroom before closing', beach => {
    const wave = createWave(0, beaches[beach], defaultConditions());
    let duration = 0; let width = 0;
    for (let i = 0; i < 90 * 60 && wave.x > 0; i++) {
      wave.x -= wave.speed / 60; evolveWave(wave, 1 / 60);
      let left = Infinity; let right = -Infinity;
      for (let j = 1; j < 50; j++) {
        const point = curlPoint(wave, j / 50, -5, 30);
        const headroom = SEA_LEVEL - waveLift(wave, point.x, 30) - point.y;
        if (headroom > 28 && wave.curl > 0.45) { left = Math.min(left, point.x); right = Math.max(right, point.x); }
      }
      width = Math.max(width, right - left);
      if (right - left > 60) duration += 1 / 60;
    }
    expect(width).toBeGreaterThan(beach === 'nazare' ? 280 : 140);
    expect(duration).toBeGreaterThan(12);
    expect(wave.breaking).toBe(1);
    expect(wave.height).toBeLessThan(5);
  });

  it('keeps continuous roof coverage across a broad tube, including between curve samples', () => {
    const wave = createWave(0, beaches.nazare, defaultConditions(), 1400);
    for (const z of [0, 50, 100]) {
      const section = waveSection(wave, z);
      const mouth = curlPoint(wave, 1, -5, z);
      for (let x = mouth.x + 10; x < section.center - 10; x += 1.3) {
        expect(Number.isFinite(barrelRoof(wave, x, z))).toBe(true);
      }
      expect(barrelRoof(wave, section.center + 10, z)).toBe(Infinity);
    }
  });
});
