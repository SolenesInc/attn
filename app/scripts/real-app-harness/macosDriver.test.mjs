import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  assertHarnessWindowAcceptsKeys,
  describeInputDriverFailure,
  MacOSDriver,
  withWindowTitleArgs,
} from './macosDriver.mjs';

afterEach(() => {
  vi.unstubAllEnvs();
});

describe('describeInputDriverFailure', () => {
  it('names a dark display as the cause, and says it is not the product', () => {
    const hint = describeInputDriverFailure(
      '[RealAppHarness] Input cannot be delivered: the screen is locked or the display is off. Wake the display (and unlock the screen) before running packaged-app scenarios.',
    );
    expect(hint).toContain('display was off');
    expect(hint).toContain('not a product failure');
  });

  it('still names the accessibility grant when that is what failed', () => {
    expect(
      describeInputDriverFailure('[RealAppHarness] Accessibility permission is required for the real app harness input driver.'),
    ).toContain('Grant Accessibility access');
  });

  it('falls back to the generic hint for anything else', () => {
    expect(describeInputDriverFailure('[RealAppHarness] App is not running for bundle id x')).toBe('macOS automation failed.');
    expect(describeInputDriverFailure()).toBe('macOS automation failed.');
  });
});

describe('withWindowTitleArgs', () => {
  it('returns the input args unchanged when no windowTitle is given', () => {
    expect(withWindowTitleArgs(['click', '--relative-x', '0.5'])).toEqual([
      'click',
      '--relative-x',
      '0.5',
    ]);
    expect(withWindowTitleArgs(['click', '--relative-x', '0.5'], {})).toEqual([
      'click',
      '--relative-x',
      '0.5',
    ]);
  });

  it('appends --window-title when opts.windowTitle is set', () => {
    expect(withWindowTitleArgs(['windowid'], { windowTitle: 'present' })).toEqual([
      'windowid',
      '--window-title',
      'present',
    ]);
  });

  it('does not mutate the input args array', () => {
    const args = ['windowid'];
    withWindowTitleArgs(args, { windowTitle: 'present' });
    expect(args).toEqual(['windowid']);
  });

  it('ignores an empty-string windowTitle', () => {
    expect(withWindowTitleArgs(['windowid'], { windowTitle: '' })).toEqual(['windowid']);
  });
});

describe('assertHarnessWindowAcceptsKeys', () => {
  it('throws on macOS while the harness window is always-on-top', () => {
    expect(() => assertHarnessWindowAcceptsKeys({}, 'darwin')).toThrow(/ATTN_HARNESS_ALWAYS_ON_TOP/);
    expect(() => assertHarnessWindowAcceptsKeys({ ATTN_HARNESS_ALWAYS_ON_TOP: '1' }, 'darwin'))
      .toThrow(/non-focusable/);
  });

  it('passes on macOS once the scenario opts out', () => {
    expect(() => assertHarnessWindowAcceptsKeys({ ATTN_HARNESS_ALWAYS_ON_TOP: '0' }, 'darwin'))
      .not.toThrow();
  });

  it('passes off macOS whatever the flag says', () => {
    expect(() => assertHarnessWindowAcceptsKeys({ ATTN_HARNESS_ALWAYS_ON_TOP: '1' }, 'linux'))
      .not.toThrow();
  });
});

class StubInputDriver extends MacOSDriver {
  constructor() {
    super({ bundleId: 'test.harness', appPath: '/tmp/attn-harness-test.app' });
    this.execCalls = [];
  }

  async runInputDriver(args) {
    this.execCalls.push(args);
  }
}

describe('MacOSDriver key entry points on a non-focusable window', () => {
  const realPlatform = process.platform;
  beforeEach(() => {
    Object.defineProperty(process, 'platform', { value: 'darwin', configurable: true });
  });
  afterEach(() => {
    Object.defineProperty(process, 'platform', { value: realPlatform, configurable: true });
  });

  it('pressKey fails before touching the input driver', async () => {
    vi.stubEnv('ATTN_HARNESS_ALWAYS_ON_TOP', '1');
    const driver = new StubInputDriver();
    await expect(driver.pressKey('a', {})).rejects.toThrow(/non-focusable/);
    expect(driver.execCalls).toEqual([]);
  });

  it('pressEnter fails through its pressKeyCode delegation', async () => {
    vi.stubEnv('ATTN_HARNESS_ALWAYS_ON_TOP', '1');
    const driver = new StubInputDriver();
    await expect(driver.pressEnter()).rejects.toThrow(/non-focusable/);
    expect(driver.execCalls).toEqual([]);
  });

  it('typeText fails before touching the input driver', async () => {
    vi.stubEnv('ATTN_HARNESS_ALWAYS_ON_TOP', '1');
    const driver = new StubInputDriver();
    await expect(driver.typeText('hello')).rejects.toThrow(/non-focusable/);
    expect(driver.execCalls).toEqual([]);
  });

  it('pressKey reaches the driver once the scenario opts out', async () => {
    vi.stubEnv('ATTN_HARNESS_ALWAYS_ON_TOP', '0');
    const driver = new StubInputDriver();
    driver.actionDelayMs = 0;
    await driver.pressKey('a', {});
    expect(driver.execCalls).toHaveLength(1);
  });
});
