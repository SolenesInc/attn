import { describe, expect, it } from 'vitest';
import {
  fitRequiresTerminalResize,
  fitShouldBailAsSuspicious,
  geometryOverflowsContainer,
  isWorkspaceSuspensionAnimating,
} from './ghosttyGeometry';

describe('ghosttyGeometry resize policy', () => {
  it('holds fits while a fold or restore animates the pane frame', () => {
    const panes = document.createElement('div');
    panes.className = 'session-terminal-panes';
    const terminal = document.createElement('div');
    panes.appendChild(terminal);

    expect(isWorkspaceSuspensionAnimating(terminal)).toBe(false);
    panes.dataset.suspensionAnimating = '1';
    expect(isWorkspaceSuspensionAnimating(terminal)).toBe(true);
    delete panes.dataset.suspensionAnimating;
    expect(isWorkspaceSuspensionAnimating(terminal)).toBe(false);
  });

  it('treats identical fit geometry as a no-op', () => {
    expect(fitRequiresTerminalResize(
      { cols: 120, rows: 40 },
      { cols: 120, rows: 40 },
    )).toBe(false);
    expect(fitRequiresTerminalResize(
      { cols: 120, rows: 40 },
      { cols: 121, rows: 40 },
    )).toBe(true);
  });
});

describe('geometryOverflowsContainer', () => {
  it('flags a grid one row taller than the container (the bug)', () => {
    expect(geometryOverflowsContainer(26, 21, 540)).toBe(true);
  });

  it('does not flag a grid that fits, including the floor() remainder gap', () => {
    expect(geometryOverflowsContainer(25, 21, 540)).toBe(false);
    expect(geometryOverflowsContainer(25, 21, 525)).toBe(false);
  });

  it('tolerates a 1px sub-pixel container height without a spurious refit', () => {
    expect(geometryOverflowsContainer(27, 21, 566)).toBe(false);
    expect(geometryOverflowsContainer(27, 21, 565)).toBe(true);
  });

  it('never flags degenerate/zero dimensions (pre-measure, hidden pane)', () => {
    expect(geometryOverflowsContainer(27, 21, 0)).toBe(false);
    expect(geometryOverflowsContainer(0, 21, 540)).toBe(false);
    expect(geometryOverflowsContainer(27, 0, 540)).toBe(false);
  });
});

describe('fitShouldBailAsSuspicious', () => {
  it('does NOT bail when a small fit is required to stop the bottom-row clip', () => {
    expect(fitShouldBailAsSuspicious('agent', { cols: 73, rows: 7 }, 73, 13, 9, 21, 720, 147)).toBe(false);
  });

  it('does NOT bail when a small fit is required to stop the right-column clip', () => {
    expect(fitShouldBailAsSuspicious('agent', { cols: 16, rows: 40 }, 40, 40, 9, 21, 150, 840)).toBe(false);
  });

  it('bails on a suspicious fit when the model does not overflow (transient measurement)', () => {
    expect(fitShouldBailAsSuspicious('agent', { cols: 2, rows: 1 }, 13, 13, 9, 21, 0, 0)).toBe(true);
    expect(fitShouldBailAsSuspicious('agent', { cols: 4, rows: 2 }, 24, 24, 9, 21, 720, 540)).toBe(true);
  });

  it('never bails for non-agent panes (utility terminals manage their own size)', () => {
    expect(fitShouldBailAsSuspicious('utility', { cols: 2, rows: 1 }, 24, 24, 9, 21, 720, 540)).toBe(false);
    expect(fitShouldBailAsSuspicious(undefined, { cols: 2, rows: 1 }, 24, 24, 9, 21, 720, 540)).toBe(false);
  });

  it('never bails when the floored fit is a normal usable size', () => {
    expect(fitShouldBailAsSuspicious('agent', { cols: 120, rows: 40 }, 120, 40, 9, 21, 1080, 840)).toBe(false);
  });

  it('does NOT bail when an already-small model grows toward a container that is still in the suspicious range (TR-205 close-after-relaunch stall)', () => {
    expect(fitShouldBailAsSuspicious('agent', { cols: 20, rows: 25 }, 15, 25, 9, 21, 189, 540)).toBe(false);
  });

  it('still bails when a HEALTHY model sees a suspicious fit from an unlaid-out container', () => {
    expect(fitShouldBailAsSuspicious('agent', { cols: 2, rows: 1 }, 62, 27, 9, 21, 0, 0)).toBe(true);
  });

  it('still bails when an already-small model would SHRINK further on one axis', () => {
    expect(fitShouldBailAsSuspicious('agent', { cols: 20, rows: 8 }, 15, 25, 9, 21, 270, 540)).toBe(true);
  });
});
