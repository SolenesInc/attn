import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  describeLinuxInputFailure,
  LinuxDriver,
  linuxKeyCodeName,
  linuxKeyName,
  linuxModifierNames,
  parseXdotoolGeometry,
} from './linuxDriver.mjs';

const tempDirs = [];

afterEach(() => {
  vi.restoreAllMocks();
  while (tempDirs.length > 0) {
    fs.rmSync(tempDirs.pop(), { recursive: true, force: true });
  }
});

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
    expect(linuxKeyName('Enter', { command: true, shift: true })).toBe('ctrl+shift+Return');
    expect(linuxKeyName('ArrowLeft')).toBe('Left');
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

describe('Linux window screenshots', () => {
  it('converts an xwd fallback into explicit PNG output', async () => {
    const root = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-linux-capture-'));
    tempDirs.push(root);
    const outputPath = path.join(root, 'evidence.png');
    const missingImport = Object.assign(new Error('spawn import ENOENT'), {
      code: 'ENOENT',
      path: 'import',
    });
    const run = vi.fn(async (command) => {
      if (command === 'import') throw missingImport;
      return { stdout: '' };
    });
    const driver = new LinuxDriver({
      appPath: '/home/someone/.local/share/attn-dev',
      run,
      env: { DISPLAY: ':99' },
    });
    driver.requireWindow = vi.fn().mockResolvedValue(42);

    await driver.screenshot(outputPath);

    const xwdCall = run.mock.calls.find(([command]) => command === 'xwd');
    const convertCall = run.mock.calls.find(([command]) => command === 'convert');
    const xwdPath = xwdCall[1].at(-1);
    expect(xwdCall[1]).toEqual(['-silent', '-id', '42', '-out', xwdPath]);
    expect(xwdPath).toMatch(/window\.xwd$/);
    expect(xwdPath).not.toBe(outputPath);
    expect(convertCall[1]).toEqual([xwdPath, `png:${outputPath}`]);
    expect(fs.existsSync(path.dirname(xwdPath))).toBe(false);
  });

  it('fails honestly when neither PNG capture route is installed', async () => {
    const root = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-linux-capture-'));
    tempDirs.push(root);
    const run = vi.fn(async (command) => {
      if (command === 'import' || command === 'convert') {
        throw Object.assign(new Error(`spawn ${command} ENOENT`), { code: 'ENOENT', path: command });
      }
      return { stdout: '' };
    });
    const driver = new LinuxDriver({
      appPath: '/home/someone/.local/share/attn-dev',
      run,
      env: { DISPLAY: ':99' },
    });
    driver.requireWindow = vi.fn().mockResolvedValue(42);

    await expect(driver.screenshot(path.join(root, 'evidence.png')))
      .rejects.toThrow('ImageMagick import and convert are both missing; xwd cannot emit PNG');
  });
});
