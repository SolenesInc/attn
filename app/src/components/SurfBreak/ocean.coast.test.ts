import { describe, expect, it } from 'vitest';
import { beaches, bottomDepth, defaultConditions, type BeachId } from './beaches';
import { Ocean, restingInput } from './ocean';
import { barrelRoof, createWave, curlPoint, evolveWave, GRAVITY, propagation, sampleWater, SEA_LEVEL, waveSection } from './water';

const rest = restingInput();
function advance(ocean: Ocean, seconds: number, input = rest) {
  for (let i = 0; i < seconds * 60; i++) ocean.step(1 / 60, input);
}

describe('a connected coast', () => {
  it('walks from dry beach through wading to paddling, and can walk back out', () => {
    const ocean = new Ocean({ beach: 'cove', start: 'beach' });
    expect(ocean.state).toBe('walking');
    expect(ocean.canStand).toBe(false);
    const states = new Set<string>();
    for (let i = 0; i < 12 * 60; i++) {
      ocean.step(1 / 60, { ...rest, right: true }); states.add(ocean.state);
    }
    expect(states.has('wading')).toBe(true);
    expect(ocean.onFoot).toBe(false);
    expect(ocean.posture).toBe('prone');
    expect(ocean.paddling).toBe(true);
    advance(ocean, 15, { ...rest, left: true });
    expect(ocean.onFoot).toBe(true);
    expect(ocean.state).toBe('walking');
  });

  it('passes the old viewport edge without losing movement, and the camera follows', () => {
    const ocean = new Ocean();
    advance(ocean, 30, { ...rest, right: true });
    expect(ocean.x).toBeGreaterThan(1500);
    expect(ocean.vx).toBeGreaterThan(30);
    expect(ocean.camera.x).toBeGreaterThan(1000);
    const screen = ocean.project(ocean.x, ocean.y, ocean.z);
    expect(screen.x).toBeGreaterThan(180);
    expect(screen.x).toBeLessThan(600);
  });

  it('keeps camera movement out of physics and water coordinates', () => {
    const a = new Ocean(); const b = new Ocean();
    b.camera.x = 900;
    advance(a, 2, { ...rest, right: true }); advance(b, 2, { ...rest, right: true });
    expect([a.x, a.y, a.vx, a.vy]).toEqual([b.x, b.y, b.vx, b.vy]);
  });

  it('carries a standing ride across a screen width while its wave continues shoreward', () => {
    const ocean = new Ocean({ beach: 'point' });
    const wave = createWave(0, beaches.point, defaultConditions(), 1500);
    const section = waveSection(wave, ocean.z);
    ocean.waves.push(wave); ocean.x = section.center - section.frontWidth * 0.65;
    ocean.y = ocean.surface(ocean.x, ocean.z); ocean.vx = -wave.speed;
    ocean.step(1 / 120, { ...rest, posture: true });
    const start = ocean.x; let riding = 0;
    for (let i = 0; i < 20 * 60; i++) {
      ocean.step(1 / 60, { ...rest, left: true });
      if (ocean.state === 'riding' && ocean.posture === 'standing') riding++;
    }
    expect(start - ocean.x).toBeGreaterThan(800);
    expect(riding).toBeGreaterThan(8 * 60);
    expect(wave.breaking).toBeGreaterThan(0.5);
  });
});

describe('seabed-driven wave motion', () => {
  it('slows and shortens waves in shallow water using the dispersion relation', () => {
    const deep = propagation(300, 8); const shallow = propagation(10, 8);
    expect(shallow.speed).toBeLessThan(deep.speed / 2);
    expect(shallow.wavelength).toBeLessThan(deep.wavelength / 2);
    for (const depth of [1, 10, 50, 300, 1000]) {
      const motion = propagation(depth, 8); const k = 2 * Math.PI / motion.wavelength;
      expect(GRAVITY * k * Math.tanh(k * depth)).toBeCloseTo((2 * Math.PI / 8) ** 2, 7);
    }
  });

  it('travels at half the physical-model speed without shrinking the wavelength or crest', () => {
    for (const beach of [beaches.point, beaches.nazare]) {
      const wave = createWave(0, beach);
      const motion = propagation(bottomDepth(beach, wave.x, 40), wave.period);
      expect(wave.speed).toBeCloseTo(motion.speed * 0.5, 8);
      expect(wave.shape.crestLength).toBeGreaterThan(motion.wavelength * 0.1);
    }
  });

  it('evolves with location rather than expiring on an age timer', () => {
    const wave = createWave(0, beaches.point);
    const initial = { height: wave.height, speed: wave.speed, curl: wave.curl };
    wave.age = 1000; evolveWave(wave);
    expect({ height: wave.height, speed: wave.speed, curl: wave.curl }).toEqual(initial);
    wave.x = 650; evolveWave(wave);
    expect(wave.speed).toBeLessThan(initial.speed);
    expect(wave.curl).toBeGreaterThan(initial.curl);
  });

  it.each(Object.keys(beaches) as BeachId[])('%s waves steepen, break, dissipate and reach the shore', id => {
    const wave = createWave(0, beaches[id]);
    let maxCurl = 0; let maxHeight = 0; let previousEnergy = wave.energy;
    for (let i = 0; i < 180 * 60 && wave.x > -30; i++) {
      wave.x -= wave.speed / 60; evolveWave(wave, 1 / 60);
      maxCurl = Math.max(maxCurl, wave.curl); maxHeight = Math.max(maxHeight, wave.height);
      expect(wave.energy).toBeLessThanOrEqual(previousEnergy);
      previousEnergy = wave.energy;
    }
    expect(wave.x).toBeLessThanOrEqual(-30);
    expect(wave.breaking).toBe(1);
    expect(wave.height).toBeLessThan(maxHeight * 0.1);
    if (id === 'cove') expect(maxCurl).toBeLessThan(0.3);
    if (id === 'reef' || id === 'nazare') expect(maxCurl).toBeGreaterThan(0.8);
  });

  it('separates wave size from frequency and changes the actual seabed', () => {
    const quiet = new Ocean({ beach: 'cove', conditions: { size: 'usual', rhythm: 'quiet' } });
    const busy = new Ocean({ beach: 'cove', conditions: { size: 'usual', rhythm: 'frequent' } });
    const seen = [new Set<number>(), new Set<number>()];
    for (let i = 0; i < 240 * 30; i++) {
      [quiet, busy].forEach((ocean, index) => { ocean.step(1 / 30, rest); ocean.waves.forEach(wave => seen[index].add(wave.id)); });
    }
    expect(seen[1].size).toBeGreaterThan(seen[0].size * 1.5);
    expect(createWave(0, beaches.cove, quiet.conditions).amplitude).toBe(createWave(0, beaches.cove, busy.conditions).amplitude);
    expect(createWave(0, beaches.nazare).amplitude).toBeGreaterThan(createWave(0, beaches.cove).amplitude * 5);
    expect(bottomDepth(beaches.reef, 1000)).toBeGreaterThan(bottomDepth(beaches.cove, 1000) + 50);
    expect(bottomDepth(beaches.point, 900, 90)).not.toBe(bottomDepth(beaches.point, 900, 0));
  });

  it('shares local water velocity with foam and attenuates motion below the wave', () => {
    const wave = createWave(0, beaches.reef, defaultConditions(), 700);
    const x = waveSection(wave).center;
    const upper = sampleWater([wave], beaches.reef, x, 0, 0, SEA_LEVEL - wave.height);
    const lower = sampleWater([wave], beaches.reef, x, 0, 0, SEA_LEVEL + 40);
    expect(upper.vx).toBeLessThan(-10);
    expect(Math.abs(lower.vx)).toBeLessThan(Math.abs(upper.vx) * 0.6);
    const ocean = new Ocean({ beach: 'reef' }); ocean.x = x; ocean.waves.push(wave);
    const particle = { kind: 'foam' as const, x, y: upper.surface, z: 0, vx: 0, vy: 0, life: 4, size: 1 };
    ocean.particles.push(particle);
    advance(ocean, 0.2);
    expect(particle.vx).toBeLessThan(0);
    expect(particle.y).toBeCloseTo(ocean.surface(particle.x, particle.z) + 1, 1);
  });
});

describe('skill-dependent giant waves', () => {
  it('costs more speed to turn sharply at Nazare, without changing input direction', () => {
    const turn = (beach: BeachId) => {
      const ocean = new Ocean({ beach }); ocean.x = 4500; ocean.y = ocean.surface(ocean.x, ocean.z);
      ocean.vx = 80; ocean.step(1 / 120, { ...rest, posture: true });
      advance(ocean, 0.4, { ...rest, left: true });
      expect(ocean.vx).toBeLessThan(0);
      return ocean.boardSpeed;
    };
    expect(turn('nazare')).toBeLessThan(turn('cove') * 0.6);
  });

  it('recovers from a collapsing giant lip without resetting the coast or removing paddle control', () => {
    const ocean = new Ocean({ beach: 'nazare' });
    const wave = createWave(0, beaches.nazare, defaultConditions(), 1050);
    wave.breaking = 0.2; evolveWave(wave);
    const lip = curlPoint(wave, 0.75, 0, ocean.z);
    ocean.waves.push(wave); ocean.x = lip.x; ocean.y = barrelRoof(wave, lip.x, ocean.z) + 12;
    ocean.posture = 'standing'; ocean.vx = -80;
    ocean.step(1 / 120, rest);
    expect(ocean.state).toBe('recovering');
    expect(ocean.canStand).toBe(false);
    const x = ocean.x;
    advance(ocean, 6, { ...rest, right: true });
    expect(ocean.recovery).toBe(0);
    expect(ocean.posture).toBe('prone');
    expect(ocean.paddling).toBe(true);
    expect(ocean.x).toBeGreaterThan(x);
  });
});
