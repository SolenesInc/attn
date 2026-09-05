import { describe, expect, it } from 'vitest';
import {
  describeInputDriverFailure,
  MacOSDriver,
  withWindowTitleArgs,
} from './macosDriver.mjs';
import { ALWAYS_ON_TOP_VAR, createKeyInputGuard } from './keyInputGuard.mjs';

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

const APP = '/tmp/attn-harness-test.app/Contents/MacOS/app';
const APP_ALWAYS_ON_TOP = `${APP} ${ALWAYS_ON_TOP_VAR}=1 HOME=/Users/x`;
const APP_OPTED_OUT = `${APP} ${ALWAYS_ON_TOP_VAR}=0 HOME=/Users/x`;

class StubInputDriver extends MacOSDriver {
  constructor(appCommand) {
    super({
      bundleId: 'test.harness',
      appPath: '/tmp/attn-harness-test.app',
      keyInputGuard: createKeyInputGuard({
        platform: 'darwin',
        appExecutable: APP,
        manifestPath: '/does/not/matter',
        readAppPid: () => 4242,
        readCommand: () => appCommand,
      }),
    });
    this.actionDelayMs = 0;
    this.execCalls = [];
  }

  async runInputDriver(args) {
    this.execCalls.push(args);
  }
}

describe('MacOSDriver key entry points on a non-focusable window', () => {
  it('pressKey fails before touching the input driver', async () => {
    const driver = new StubInputDriver(APP_ALWAYS_ON_TOP);
    await expect(driver.pressKey('a', {})).rejects.toThrow(/pressKey\(a, no modifiers\) cannot reach attn/);
    expect(driver.execCalls).toEqual([]);
  });

  it('pressEnter fails through its pressKeyCode delegation', async () => {
    const driver = new StubInputDriver(APP_ALWAYS_ON_TOP);
    await expect(driver.pressEnter()).rejects.toThrow(/pressKeyCode\(36, no modifiers\) cannot reach attn/);
    expect(driver.execCalls).toEqual([]);
  });

  it('typeText fails before touching the input driver', async () => {
    const driver = new StubInputDriver(APP_ALWAYS_ON_TOP);
    await expect(driver.typeText('hello')).rejects.toThrow(/typeText\("hello"\) cannot reach attn/);
    expect(driver.execCalls).toEqual([]);
  });

  it('pressKey reaches the driver once the launched app opted out', async () => {
    const driver = new StubInputDriver(APP_OPTED_OUT);
    await driver.pressKey('a', {});
    expect(driver.execCalls).toHaveLength(1);
  });
});
