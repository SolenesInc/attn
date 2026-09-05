import { describe, expect, it } from 'vitest';
import { Ocean, restingInput } from './ocean';

const rest = restingInput();
function advance(ocean: Ocean, seconds: number, input = rest) {
  for (let i = 0; i < Math.round(seconds * 120); i++) ocean.step(1 / 120, input);
}
function coasting() {
  const ocean = new Ocean();
  ocean.x = 4000;
  ocean.vx = 48;
  ocean.y = ocean.surface(ocean.x, ocean.z);
  ocean.step(1 / 120, { ...rest, posture: true });
  expect(ocean.posture).toBe('standing');
  return ocean;
}

describe('paddle, stand, carve and settle', () => {
  it('starts lying down, cannot stand or jump at rest, and paddles in either direction', () => {
    for (const direction of [-1, 1]) {
      const ocean = new Ocean();
      ocean.step(1 / 120, { ...rest, posture: true, jump: true });
      expect(ocean.posture).toBe('prone');
      expect(ocean.state).not.toBe('airborne');
      expect(ocean.canStand).toBe(false);
      advance(ocean, 0.8, { ...rest, left: direction < 0, right: direction > 0 });
      expect(ocean.vx * direction).toBeGreaterThan(40);
      expect(ocean.canStand).toBe(true);
      expect(ocean.paddling).toBe(true);
      expect(ocean.posture).toBe('prone');
    }
  });

  it('pops up once per request and animates smoothly without moving the board', () => {
    const ocean = coasting();
    const x = ocean.x;
    expect(ocean.standingBlend).toBeGreaterThan(0);
    expect(ocean.standingBlend).toBeLessThan(0.1);
    advance(ocean, 0.5);
    expect(ocean.posture).toBe('standing');
    expect(ocean.standingBlend).toBeGreaterThan(0.95);
    expect(ocean.x - x).toBeGreaterThan(18);
    expect(ocean.depth).toBeLessThan(3);
  });

  it('standing input redirects speed without supplying flat-water propulsion', () => {
    const ocean = coasting();
    const initial = ocean.boardSpeed;
    advance(ocean, 1.5, { ...rest, right: true });
    expect(ocean.posture).toBe('standing');
    expect(ocean.boardSpeed).toBeLessThan(initial * 0.7);
    expect(ocean.boardSpeed).toBeGreaterThan(initial * 0.5);
    expect(ocean.paddling).toBe(false);
    advance(ocean, 4, { ...rest, right: true });
    expect(ocean.posture).toBe('prone');
    expect(ocean.paddling).toBe(true);
  });

  it('can lie down at speed, then paddle with different drag and propulsion', () => {
    const ocean = coasting();
    ocean.step(1 / 120, { ...rest, posture: true });
    expect(ocean.posture).toBe('prone');
    advance(ocean, 0.8);
    expect(ocean.vx).toBeLessThan(20);
    expect(ocean.standingBlend).toBeLessThan(0.01);
    advance(ocean, 0.8, { ...rest, right: true });
    expect(ocean.vx).toBeGreaterThan(40);
    expect(ocean.posture).toBe('prone');
  });

  it('gives low speed a short grace period, then settles and requires a new stand request', () => {
    const ocean = coasting();
    ocean.vx = 8;
    advance(ocean, 0.3);
    expect(ocean.posture).toBe('standing');
    advance(ocean, 0.6);
    expect(ocean.posture).toBe('prone');
    advance(ocean, 0.8, { ...rest, right: true });
    expect(ocean.canStand).toBe(true);
    expect(ocean.posture).toBe('prone');
  });

  it('does not settle during a brief slowdown that recovers', () => {
    const ocean = coasting();
    ocean.vx = 8;
    advance(ocean, 0.3);
    ocean.vx = 30;
    advance(ocean, 0.6);
    expect(ocean.posture).toBe('standing');
  });

  it('lies down to dive and stays prone after surfacing', () => {
    const ocean = coasting();
    advance(ocean, 2.5, { ...rest, dive: true, right: true });
    expect(ocean.posture).toBe('prone');
    expect(ocean.state).toBe('submerged');
    ocean.step(1 / 120, { ...rest, posture: true, dive: true });
    expect(ocean.posture).toBe('prone');
    advance(ocean, 3);
    expect(ocean.depth).toBeLessThan(4);
    expect(ocean.posture).toBe('prone');
  });

  it('keeps board walking for standing and resets foot placement when lying down', () => {
    const ocean = coasting();
    advance(ocean, 0.8, { ...rest, nose: true });
    expect(ocean.stance).toBeGreaterThan(0.6);
    ocean.step(1 / 120, { ...rest, posture: true });
    advance(ocean, 0.5, { ...rest, nose: true });
    expect(ocean.stance).toBe(0);
    expect(ocean.walking).toBe(false);
  });
});
