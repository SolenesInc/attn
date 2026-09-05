import { describe, expect, it } from 'vitest';
import { beaches, defaultConditions } from './beaches';
import { createWave, curlPoint, Ocean, project, restingInput, SEA_LEVEL, waveSection } from './ocean';

const rest = restingInput();
function advance(ocean: Ocean, seconds: number, input = rest, fps = 60) {
  for (let i = 0; i < Math.round(seconds * fps); i++) ocean.step(1 / fps, input);
}
function tubeScene() {
  const ocean = new Ocean({ beach: 'reef' });
  const wave = createWave(0, beaches.reef, defaultConditions(), 600);
  ocean.waves.push(wave);
  ocean.x = waveSection(wave, ocean.z).center - 35;
  ocean.y = ocean.surface(ocean.x, ocean.z);
  return ocean;
}

describe('the ocean between and beneath waves', () => {
  it('starts calm and sends a moving swell from offshore', () => {
    const ocean = new Ocean();
    advance(ocean, 2);
    expect(ocean.waves).toHaveLength(0);
    expect(Math.abs(ocean.surface(ocean.x) - SEA_LEVEL)).toBeLessThan(2);
    advance(ocean, 5);
    const wave = ocean.waves[0];
    expect(wave.height).toBeGreaterThan(20);
    const x = wave.x;
    advance(ocean, 2);
    expect(wave.x).toBeLessThan(x - 60);
    expect(wave.energy).toBe(1);
    expect(wave.breaking).toBe(0);
  });

  it('lets the surfer stay submerged while a wave passes overhead, then float up', () => {
    const ocean = new Ocean({ beach: 'reef' });
    ocean.x = 1300; ocean.y = ocean.surface(ocean.x, ocean.z);
    advance(ocean, 3, { ...rest, dive: true });
    expect(ocean.state).toBe('submerged');
    expect(ocean.depth).toBeGreaterThan(28);
    const submergedY = ocean.y;
    for (let i = 0; i < 30 * 60 && ocean.surface(ocean.x, ocean.z) >= SEA_LEVEL - 35; i++) {
      ocean.step(1 / 60, { ...rest, dive: true });
    }
    expect(ocean.state).toBe('submerged');
    expect(ocean.surface(ocean.x, ocean.z)).toBeLessThan(SEA_LEVEL - 35);
    expect(ocean.y).toBeGreaterThan(submergedY - 3);
    expect(ocean.particles.some(p => p.kind === 'bubble')).toBe(true);
    advance(ocean, 5);
    expect(ocean.depth).toBeLessThan(5);
    expect(ocean.state).not.toBe('submerged');
  });

  it('allows a short pop while standing and coasting during calm water', () => {
    const ocean = new Ocean();
    advance(ocean, 0.8, { ...rest, left: true });
    ocean.step(1 / 120, { ...rest, posture: true });
    expect(ocean.posture).toBe('standing');
    const x = ocean.x;
    const y = ocean.y;
    ocean.step(1 / 60, { ...rest, jump: true });
    advance(ocean, 0.3, { ...rest, left: true });
    expect(ocean.state).toBe('airborne');
    expect(ocean.y).toBeLessThan(y - 12);
    expect(ocean.x).toBeLessThan(x - 3);
    advance(ocean, 2);
    expect(ocean.depth).toBeLessThan(4);
    expect(ocean.state).toBe('floating');
  });

  it('brings airborne spray back to the surface as foam', () => {
    const ocean = tubeScene();
    advance(ocean, 0.3);
    expect(ocean.particles.some(p => p.kind === 'spray')).toBe(true);
    const spray = ocean.particles.find(p => p.kind === 'spray' && p.vy < 0)!;
    expect(spray).toBeDefined();
    const vy = spray.vy;
    advance(ocean, 0.1);
    expect(spray.vy).toBeGreaterThan(vy);
    advance(ocean, 1.5);
    expect(ocean.particles.some(p => p.kind === 'foam')).toBe(true);
  });

  it('applies a gradual downward force at the lip without overriding lateral control', () => {
    const ocean = tubeScene();
    const lip = curlPoint(ocean.waves[0], 0.85, 0, ocean.z);
    ocean.x = lip.x;
    ocean.y = lip.y + 6;
    ocean.vy = -10;
    ocean.vx = 40;
    ocean.step(1 / 120, rest);
    expect(ocean.vy).toBeGreaterThan(-10);
    expect(ocean.vy).toBeLessThan(0);
    expect(ocean.vx).toBeGreaterThan(35);
    advance(ocean, 5);
    expect(ocean.depth).toBeLessThan(5);
  });

  it('keeps long mixed play finite, above the seabed, and below particle capacity', () => {
    const ocean = new Ocean();
    for (let second = 0; second < 360; second++) {
      advance(ocean, 1, { posture: second % 5 === 0, left: second % 13 < 5, right: second % 17 < 7, dive: second % 19 < 9,
        jump: second % 7 === 0, away: second % 11 < 4, toward: second % 9 < 4, nose: second % 8 < 3, tail: second % 12 < 4 }, 30);
      expect([ocean.x, ocean.y, ocean.z, ocean.vx, ocean.vy, ocean.vz, ocean.angle, ocean.heading, ocean.stance].every(Number.isFinite)).toBe(true);
      expect(ocean.y).toBeLessThanOrEqual(ocean.floor(ocean.x, ocean.z));
      expect(ocean.z).toBeGreaterThanOrEqual(0);
      expect(ocean.z).toBeLessThanOrEqual(100);
      expect(ocean.stance).toBeGreaterThanOrEqual(-0.8);
      expect(ocean.stance).toBeLessThanOrEqual(0.9);
      expect(ocean.particles.length).toBeLessThanOrEqual(320);
      expect(ocean.waves.length).toBeLessThanOrEqual(16);
    }
  }, 15_000);

  it('uses the same motion at different render rates and ignores invalid elapsed time', () => {
    const slow = new Ocean();
    const fast = new Ocean();
    advance(slow, 10, rest, 30);
    advance(fast, 10, rest, 60);
    expect(slow.x).toBeCloseTo(fast.x, 3);
    expect(slow.y).toBeCloseTo(fast.y, 3);
    const time = slow.time;
    for (const dt of [NaN, Infinity, -1, 0]) slow.step(dt, rest);
    expect(slow.time).toBe(time);
  });
});

describe('choosing a line and holding a pose', () => {
  it('reverses promptly in both directions, including against a moving wave', () => {
    for (const direction of [-1, 1]) {
      const ocean = new Ocean();
      advance(ocean, 10);
      ocean.x = waveSection(ocean.waves[0], ocean.z).center - 45;
      ocean.y = ocean.surface(ocean.x, ocean.z);
      ocean.vx = -direction * 60;
      ocean.step(1 / 120, { ...rest, posture: true });
      advance(ocean, 0.4, { ...rest, left: direction < 0, right: direction > 0 });
      expect(ocean.posture).toBe('standing');
      expect(ocean.depth).toBeLessThan(5);
      expect(ocean.vx * direction).toBeGreaterThan(30);
      advance(ocean, 0.3, { ...rest, left: direction < 0, right: direction > 0 });
      expect(Math.cos(ocean.heading) * direction).toBeGreaterThan(0.8);
    }
  });

  it('moves slowly in depth without diving, then settles on the chosen line', () => {
    const ocean = new Ocean();
    const start = ocean.z;
    advance(ocean, 1, { ...rest, away: true });
    expect(ocean.z).toBeGreaterThan(start + 10);
    expect(ocean.z).toBeLessThan(start + 20);
    expect(ocean.depth).toBeLessThan(3);
    expect(Math.sin(ocean.heading)).toBeLessThan(-0.9);
    advance(ocean, 1.5);
    const line = ocean.z;
    advance(ocean, 0.5);
    expect(ocean.z - line).toBeLessThan(0.2);
    advance(ocean, 1, { ...rest, toward: true });
    expect(ocean.z).toBeLessThan(line - 10);
    expect(Math.sin(ocean.heading)).toBeGreaterThan(0.9);
  });

  it('eases into diving and floats up without snapping to the surface', () => {
    const ocean = new Ocean();
    ocean.x = 1300; ocean.y = ocean.surface(ocean.x, ocean.z);
    const start = ocean.y;
    advance(ocean, 0.5, { ...rest, dive: true });
    expect(ocean.y - start).toBeGreaterThan(3);
    expect(ocean.y - start).toBeLessThan(8);
    expect(ocean.vy).toBeLessThanOrEqual(18);
    advance(ocean, 2.5, { ...rest, dive: true });
    const underwater = ocean.y;
    advance(ocean, 0.25);
    expect(underwater - ocean.y).toBeLessThan(8);
    expect(ocean.depth).toBeGreaterThan(20);
    advance(ocean, 2);
    expect(ocean.depth).toBeLessThan(3);
  });

  it('keeps horizontal and depth momentum when jumping in either direction', () => {
    for (const direction of [-1, 1]) {
      const ocean = new Ocean();
      ocean.vx = direction * 50;
      ocean.vz = direction * 15;
      ocean.step(1 / 120, { ...rest, posture: true });
      const launchDepthSpeed = ocean.vz * direction;
      ocean.step(1 / 120, { ...rest, jump: true });
      expect(ocean.vx * direction).toBeGreaterThan(48);
      expect(ocean.vz * direction).toBeGreaterThan(launchDepthSpeed * 0.95);
      expect(ocean.vy).toBeLessThan(-55);
      const x = ocean.x;
      const z = ocean.z;
      advance(ocean, 0.3);
      expect((ocean.x - x) * direction).toBeGreaterThan(12);
      expect((ocean.z - z) * direction).toBeGreaterThan(3.5);
    }
  });

  it('carries the slope and moving water into the jump arc', () => {
    const launch = (direction: number) => {
      const ocean = tubeScene();
      const wave = ocean.waves[0];
      const section = waveSection(wave, ocean.z);
      ocean.x = section.center + section.crestLength + section.backWidth * 0.6;
      ocean.y = ocean.surface(ocean.x, ocean.z);
      ocean.vx = direction * 50;
      ocean.step(1 / 120, { ...rest, posture: true });
      ocean.step(1 / 120, { ...rest, jump: true });
      return ocean.vy;
    };
    expect(launch(-1)).toBeLessThan(launch(1) - 20);
  });

  it('lets the surfer walk to the nose or tail and keep crossed arms after releasing', () => {
    const ocean = new Ocean();
    ocean.x = 4000;
    ocean.y = ocean.surface(ocean.x, ocean.z);
    ocean.vx = -120;
    ocean.step(1 / 120, { ...rest, posture: true });
    ocean.armsCrossed = true;
    advance(ocean, 1, { ...rest, nose: true });
    expect(ocean.stance).toBeCloseTo(0.8);
    const stance = ocean.stance;
    advance(ocean, 2);
    expect(ocean.stance).toBe(stance);
    expect(ocean.armsCrossed).toBe(true);
    expect(ocean.walking).toBe(false);
    advance(ocean, 2, { ...rest, tail: true });
    expect(ocean.stance).toBeCloseTo(-0.8);
  });

  it('projects a farther rider smaller and higher, with depth-dependent wave geometry and cover', () => {
    const near = project(400, SEA_LEVEL, 0);
    const far = project(400, SEA_LEVEL, 100);
    expect(far.scale).toBeLessThan(near.scale);
    expect(far.y).toBeLessThan(near.y - 35);
    const ocean = tubeScene();
    const wave = ocean.waves[0];
    const onLine = (z: number) => {
      ocean.z = z;
      ocean.x = waveSection(wave, z).center - 35;
      ocean.y = ocean.surface(ocean.x, z);
      return ocean.cover;
    };
    expect(onLine(0)).toBe(0);
    expect(onLine(75)).toBeGreaterThan(0.9);
    expect(ocean.barrel).toBe(wave);
    expect(ocean.surface(wave.x, 100)).toBeGreaterThan(ocean.surface(wave.x, 0) + 20);
    ocean.y += 30;
    expect(ocean.cover).toBe(0);
  });
});
