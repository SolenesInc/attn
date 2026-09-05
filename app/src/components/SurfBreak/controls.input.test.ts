import { describe, expect, it } from 'vitest';
import { SurfControls } from './controls';
import { restingInput } from './ocean';

describe('surf controls', () => {
  it('keeps depth steering separate from underwater movement and stepping', () => {
    const controls = new SurfControls();
    controls.press('ArrowDown');
    expect(controls.input.toward).toBe(true);
    expect(controls.input.dive).toBe(false);
    controls.press('ShiftLeft');
    controls.press('KeyE');
    expect(controls.input.dive).toBe(true);
    expect(controls.input.nose).toBe(true);
    controls.release('ShiftLeft');
    expect(controls.input.dive).toBe(false);
    expect(controls.input.toward).toBe(true);
  });

  it('does not cancel a held key when its alternate key or pointer is released', () => {
    const controls = new SurfControls();
    controls.press('KeyA');
    controls.press('ArrowLeft');
    controls.holdPointer(1, 'left');
    controls.release('ArrowLeft');
    controls.releasePointer(1);
    expect(controls.input.left).toBe(true);
    controls.release('KeyA');
    expect(controls.input.left).toBe(false);
  });

  it('clears every held action and queued jump on pause, blur or exit', () => {
    const controls = new SurfControls();
    controls.press('KeyW');
    controls.press('KeyQ');
    controls.holdPointer(12, 'dive');
    controls.input.jump = true;
    controls.input.posture = true;
    controls.clear();
    expect(controls.input).toEqual(restingInput());
    controls.release('KeyW');
    controls.releasePointer(12);
    expect(controls.input).toEqual(restingInput());
    expect(controls.press('KeyM')).toBe(false);
  });

  it('keeps queued posture and jump requests when held keys change', () => {
    const controls = new SurfControls();
    controls.input.posture = true;
    controls.input.jump = true;
    controls.press('ArrowLeft');
    controls.release('ArrowLeft');
    controls.holdPointer(1, 'away');
    controls.releasePointer(1);
    expect(controls.input.posture).toBe(true);
    expect(controls.input.jump).toBe(true);
  });
});
