import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';
import { SurfBreak } from './SurfBreak';

const scene = vi.hoisted(() => ({
  draw: vi.fn(), step: vi.fn(), canStand: false, posture: 'prone' as 'prone' | 'standing',
  state: 'floating', waves: [], depth: 0, x: 500, z: 30, heading: 0, stance: 0, cover: 0, speed: 0, time: 0, armsCrossed: false,
  beach: { id: 'cove' },
  wipeoutCause: null as 'lip' | 'stall' | 'closeout' | null,
}));
const construct = vi.hoisted(() => vi.fn());
vi.mock('./ocean', async importOriginal => ({
  ...await importOriginal<typeof import('./ocean')>(),
  Ocean: class { constructor(options: unknown) { construct(options); return scene; } },
}));
vi.mock('focus-trap-react', () => ({ default: ({ children }: { children: ReactNode }) => children }));
vi.mock('./sound', () => ({ SurfSound: class {
  play = vi.fn().mockResolvedValue(undefined);
  pause = vi.fn().mockResolvedValue(undefined);
  dispose = vi.fn().mockResolvedValue(undefined);
  setSurf = vi.fn();
} }));

let frame: FrameRequestCallback | undefined;
let now = 0;
beforeEach(() => {
  scene.canStand = false; scene.posture = 'prone'; scene.step.mockReset(); scene.draw.mockClear();
  scene.state = 'floating'; scene.wipeoutCause = null;
  construct.mockClear();
  now = 0; frame = undefined;
  vi.spyOn(document, 'hasFocus').mockReturnValue(true);
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({} as CanvasRenderingContext2D);
  vi.spyOn(performance, 'now').mockImplementation(() => now);
  vi.stubGlobal('requestAnimationFrame', vi.fn(callback => { frame = callback; return 1; }));
  vi.stubGlobal('cancelAnimationFrame', vi.fn(() => { frame = undefined; }));
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
function tick() {
  now += 40;
  act(() => frame?.(now));
}
function start() {
  render(<SurfBreak waitingCount={0} onClose={vi.fn()} onReturnToWaiting={vi.fn()} />);
  fireEvent.click(screen.getByRole('button', { name: /Into the water/ }));
  return screen.getByRole('dialog').querySelector('canvas')!;
}

describe('posture controls and resource suspension', () => {
  it('shows the speed requirement and enables manual posture when the simulation permits it', () => {
    start();
    expect(screen.getByRole('button', { name: 'f stand up' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'space jump' })).toBeDisabled();
    scene.canStand = true;
    tick();
    expect(screen.getByRole('button', { name: 'f stand up' })).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: 'f stand up' }));
    scene.step.mockImplementation((_dt, input) => { if (input.posture) scene.posture = 'standing'; });
    tick();
    expect(screen.getByRole('button', { name: 'f lie down' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'space jump' })).toBeEnabled();
    scene.posture = 'prone'; scene.canStand = false;
    tick();
    expect(screen.getByRole('button', { name: 'f stand up' })).toBeDisabled();
  });

  it('sends F once, keeps it separate from Space, and ignores key repeat', () => {
    const canvas = start();
    const inputs: { posture: boolean; jump: boolean }[] = [];
    scene.step.mockImplementation((_dt, input) => inputs.push({ posture: input.posture, jump: input.jump }));
    fireEvent.keyDown(canvas, { code: 'KeyF', key: 'f' }); tick();
    fireEvent.keyDown(canvas, { code: 'KeyF', key: 'f', repeat: true }); tick();
    fireEvent.keyDown(canvas, { code: 'Space', key: ' ' }); tick();
    expect(inputs).toEqual([{ posture: true, jump: false }, { posture: false, jump: false }, { posture: false, jump: true }]);
  });

  it.each([
    ['lip', 'Caught by the lip.'], ['stall', 'Lost speed in the breaking face.'], ['closeout', 'The wave closed over you.'],
  ] as const)('explains a %s wipeout and returns to catch guidance after recovery', (cause, message) => {
    start();
    scene.state = 'recovering'; scene.wipeoutCause = cause;
    tick();
    expect(screen.getByText(message, { exact: false })).toBeVisible();
    scene.state = 'floating'; scene.wipeoutCause = null; scene.canStand = true;
    tick();
    expect(screen.queryByText(message, { exact: false })).not.toBeInTheDocument();
    expect(screen.getByText('Paddle left with the wave. When it carries you, F stands up.')).toBeVisible();
  });

  it('drops queued actions and stops painting on blur and pause', () => {
    const canvas = start();
    fireEvent.keyDown(canvas, { code: 'KeyF', key: 'f' });
    fireEvent.keyDown(canvas, { code: 'ArrowLeft', key: 'ArrowLeft' });
    fireEvent.blur(window);
    const draws = scene.draw.mock.calls.length;
    tick();
    expect(scene.draw).toHaveBeenCalledTimes(draws);
    fireEvent.focus(window);
    scene.step.mockImplementation((_dt, input) => {
      expect(input.posture).toBe(false); expect(input.left).toBe(false);
    });
    tick();
    expect(scene.step).toHaveBeenCalledOnce();
    fireEvent.keyDown(canvas, { code: 'KeyP', key: 'p' });
    tick();
    expect(scene.step).toHaveBeenCalledOnce();
  });

  it('pauses for beach selection, preserves native select keys and applies independent conditions', () => {
    const canvas = start();
    expect(construct).toHaveBeenCalledExactlyOnceWith({ beach: 'cove', start: 'beach' });
    fireEvent.keyDown(canvas, { code: 'ArrowLeft', key: 'ArrowLeft' });
    fireEvent.click(screen.getByRole('button', { name: /Sheltered cove/ }));
    expect(screen.getByRole('dialog')).toHaveAttribute('data-playing', 'false');
    tick(); expect(scene.step).not.toHaveBeenCalled();
    const select = screen.getByRole('combobox', { name: 'Beach' });
    expect(fireEvent.keyDown(select, { code: 'ArrowDown', key: 'ArrowDown', cancelable: true })).toBe(true);
    fireEvent.change(select, { target: { value: 'nazare' } });
    fireEvent.change(screen.getByRole('combobox', { name: 'Wave size' }), { target: { value: 'large' } });
    fireEvent.change(screen.getByRole('combobox', { name: 'Time between sets' }), { target: { value: 'quiet' } });
    fireEvent.click(screen.getByRole('button', { name: 'Start on this beach' }));
    expect(construct).toHaveBeenLastCalledWith({ beach: 'nazare', start: 'beach', conditions: { size: 'large', rhythm: 'quiet' } });
    scene.step.mockImplementation((_dt, input) => { expect(input.left).toBe(false); expect(input.toward).toBe(false); });
    tick();
    expect(scene.step).toHaveBeenCalledOnce();
    expect(construct).toHaveBeenCalledTimes(2);
    expect(screen.getByRole('button', { name: /Nazaré/ })).toBeVisible();
  });

  it('can cancel a beach change without replacing the ocean', () => {
    start();
    fireEvent.click(screen.getByRole('button', { name: /Sheltered cove/ }));
    fireEvent.change(screen.getByRole('combobox', { name: 'Beach' }), { target: { value: 'reef' } });
    fireEvent.click(screen.getByRole('button', { name: 'Stay here' }));
    expect(construct).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('dialog')).toHaveAttribute('data-playing', 'true');
    expect(screen.getByRole('button', { name: /Sheltered cove/ })).toBeVisible();
  });
});
