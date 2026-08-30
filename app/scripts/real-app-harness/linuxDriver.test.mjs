import { describe, expect, it } from 'vitest';
import {
  describeLinuxInputFailure,
  linuxKeyCodeName,
  linuxKeyName,
  linuxModifierNames,
  parseXdotoolGeometry,
} from './linuxDriver.mjs';

describe('Linux input failures', () => {
  it('names a missing DISPLAY and the Xvfb remedy', () => {
    const hint = describeLinuxInputFailure(null, {});
    expect(hint).toContain('DISPLAY is not set');
    expect(hint).toContain('xvfb-run');
  });

  it('names a missing xdotool binary', () => {
    const error = Object.assign(new Error('spawn xdotool ENOENT'), {
      code: 'ENOENT',
      path: 'xdotool',
    });
    expect(describeLinuxInputFailure(error, { DISPLAY: ':99' })).toContain('xdotool is not installed');
  });

  it('uses a generic failure for other xdotool errors', () => {
    expect(describeLinuxInputFailure(new Error('bad window'), { DISPLAY: ':99' }))
      .toBe('Linux input driver failed.');
  });
});

describe('Linux key names', () => {
  it('maps app shortcut modifiers to X11 names', () => {
    expect(linuxModifierNames({ command: true, option: true, shift: true })).toEqual([
      'ctrl',
      'alt',
      'shift',
    ]);
    expect(linuxKeyName('p', { command: true })).toBe('ctrl+p');
    expect(linuxKeyName('.', { command: true })).toBe('ctrl+period');
  });

  it('deduplicates command and control because both are Ctrl on Linux', () => {
    expect(linuxModifierNames({ command: true, control: true })).toEqual(['ctrl']);
  });

  it('maps the macOS virtual key codes used by scenarios', () => {
    expect([36, 53, 123, 124].map(linuxKeyCodeName)).toEqual([
      'Return',
      'Escape',
      'Left',
      'Right',
    ]);
  });

  it('names an unsupported virtual key code', () => {
    expect(() => linuxKeyCodeName(999)).toThrow('Unsupported Linux input key code: 999');
  });
});

describe('parseXdotoolGeometry', () => {
  it('reads xdotool getwindowgeometry --shell output', () => {
    expect(parseXdotoolGeometry('WINDOW=42\nX=10\nY=20\nWIDTH=1200\nHEIGHT=800\n'))
      .toEqual({ x: 10, y: 20, width: 1200, height: 800 });
  });

  it('rejects incomplete geometry', () => {
    expect(() => parseXdotoolGeometry('X=10\nY=20\n')).toThrow('Failed to parse xdotool window geometry');
  });
});
