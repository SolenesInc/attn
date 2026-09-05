import { describe, expect, it } from 'vitest';
import {
  ALWAYS_ON_TOP_VAR,
  createKeyInputGuard,
  keyInputVerdict,
} from './keyInputGuard.mjs';

const APP = '/Users/x/Applications/attn-dev.app/Contents/MacOS/app';
const APP_ALWAYS_ON_TOP = `${APP} ATTN_HARNESS_PARK_VISIBLE_PX=20 ${ALWAYS_ON_TOP_VAR}=1 HOME=/Users/x`;
const APP_OPTED_OUT = `${APP} ATTN_HARNESS_PARK_VISIBLE_PX=0 ${ALWAYS_ON_TOP_VAR}=0 HOME=/Users/x`;
const APP_LAUNCHED_BY_HAND = `${APP} HOME=/Users/x`;
const A_STRANGER = `/opt/homebrew/bin/node server.mjs HOME=/Users/x APP_PATH=${APP}`;

function verdictFor(appCommand, platform = 'darwin') {
  return keyInputVerdict({ platform, appRunning: true, appCommand, appExecutable: APP });
}

function guardOver(appCommand, { platform = 'darwin', pid = 4242 } = {}) {
  return createKeyInputGuard({
    platform,
    appExecutable: APP,
    manifestPath: '/does/not/matter',
    readAppPid: () => pid,
    readCommand: () => appCommand,
  });
}

describe('keyInputVerdict', () => {
  it('says keys land wherever the window stayed focusable', () => {
    expect(verdictFor(APP_OPTED_OUT).reaches).toBe(true);
    expect(verdictFor(APP_LAUNCHED_BY_HAND).reaches).toBe(true);
    expect(verdictFor(APP_ALWAYS_ON_TOP, 'linux').reaches).toBe(true);
  });

  it('says keys land nowhere against a macOS always-on-top window', () => {
    const verdict = verdictFor(APP_ALWAYS_ON_TOP);
    expect(verdict.reaches).toBe(false);
    expect(verdict.reason).toContain(`${ALWAYS_ON_TOP_VAR}=1`);
  });

  it('refuses the press when a running app will not say how it was launched', () => {
    expect(verdictFor(null).reaches).toBe(false);
    expect(verdictFor(null).reason).toContain('could not be read');
    expect(verdictFor('').reaches).toBe(false);
  });

  it('does not read a bare 1 out of a neighbouring variable', () => {
    expect(verdictFor(`${APP} ATTN_HARNESS_PARK_VISIBLE_PX=1 ${ALWAYS_ON_TOP_VAR}=0`).reaches).toBe(true);
  });

  it('refuses a manifest pid that macOS reused for something that is not the app', () => {
    const verdict = verdictFor(A_STRANGER);
    expect(verdict.reaches).toBe(false);
    expect(verdict.reason).toContain('/opt/homebrew/bin/node');
    expect(verdict.reason).toContain('manifest is stale');
    expect(verdict.fix).toContain('launchFreshAppAndConnect');
  });

  it('does not take the app path out of a stranger environment as proof', () => {
    expect(A_STRANGER).toContain(APP);
    expect(verdictFor(A_STRANGER).reaches).toBe(false);
  });

  it('refuses another executable whose path merely starts with this one', () => {
    expect(verdictFor('/Users/x/Applications/attn-dev2.app/Contents/MacOS/app HOME=/Users/x').reaches).toBe(false);
    expect(verdictFor(`${APP}-helper ${ALWAYS_ON_TOP_VAR}=1`).reaches).toBe(false);
  });

  it('accepts the app running with no arguments or environment at all', () => {
    expect(verdictFor(APP).reaches).toBe(true);
  });
});

describe('createKeyInputGuard', () => {
  it('names the variable, the app pid and the fix', () => {
    const guard = guardOver(APP_ALWAYS_ON_TOP);
    expect(() => guard.assertReaches('pressKey Escape')).toThrow(/pressKey Escape cannot reach attn/);
    expect(() => guard.assertReaches('pressKey Escape')).toThrow(new RegExp(`${ALWAYS_ON_TOP_VAR} = '0'`));
    expect(() => guard.assertReaches('pressKey Escape')).toThrow(/pid 4242/);
  });

  it('lets a press through once the scenario opted out', () => {
    expect(() => guardOver(APP_OPTED_OUT).assertReaches('pressEnter')).not.toThrow();
  });

  it('reads the app command once per pid', () => {
    let reads = 0;
    let pid = 11;
    const guard = createKeyInputGuard({
      platform: 'darwin',
      appExecutable: APP,
      manifestPath: '/does/not/matter',
      readAppPid: () => pid,
      readCommand: () => {
        reads += 1;
        return APP_ALWAYS_ON_TOP;
      },
    });
    guard.verdict();
    guard.verdict();
    expect(reads).toBe(1);
    pid = 12;
    guard.verdict();
    expect(reads).toBe(2);
  });

  it('fails the press when the running app command cannot be read', () => {
    const guard = createKeyInputGuard({
      platform: 'darwin',
      appExecutable: APP,
      manifestPath: '/does/not/matter',
      readAppPid: () => 4242,
      readCommand: () => { throw new Error('ps: no such process'); },
    });
    expect(() => guard.assertReaches('pressKey Escape')).toThrow(/could not be read/);
    expect(() => guard.assertReaches('pressKey Escape')).toThrow(/pid 4242/);
  });

  it('retries the read instead of caching an unreadable command', () => {
    let reads = 0;
    const guard = createKeyInputGuard({
      platform: 'darwin',
      appExecutable: APP,
      manifestPath: '/does/not/matter',
      readAppPid: () => 4242,
      readCommand: () => {
        reads += 1;
        if (reads === 1) {
          throw new Error('ps: transient');
        }
        return APP_OPTED_OUT;
      },
    });
    expect(() => guard.assertReaches('pressKey Escape')).toThrow(/could not be read/);
    expect(() => guard.assertReaches('pressKey Escape')).not.toThrow();
  });

  it('refuses every press while the manifest names a reused pid, and lets them through once it names the app', () => {
    let command = A_STRANGER;
    let pid = 4242;
    const guard = createKeyInputGuard({
      platform: 'darwin',
      appExecutable: APP,
      manifestPath: '/does/not/matter',
      readAppPid: () => pid,
      readCommand: () => command,
    });
    expect(() => guard.assertReaches('pressKey w')).toThrow(/manifest is stale/);
    expect(() => guard.assertReaches('typeText("hi")')).toThrow(/manifest is stale/);
    pid = 4243;
    command = APP_OPTED_OUT;
    expect(() => guard.assertReaches('pressKey w')).not.toThrow();
  });

  it('stays out of the way when no app is running to ask', () => {
    const guard = createKeyInputGuard({
      platform: 'darwin',
      appExecutable: APP,
      manifestPath: '/does/not/matter',
      readAppPid: () => { throw new Error('no manifest'); },
      readCommand: () => { throw new Error('unreachable'); },
    });
    expect(() => guard.assertReaches('pressKey w')).not.toThrow();
  });

  it('asks ps about the pid the manifest names', () => {
    const asked = [];
    const guard = createKeyInputGuard({
      platform: 'darwin',
      appExecutable: APP,
      manifestPath: '/does/not/matter',
      readAppPid: () => 777,
      readCommand: (pid) => {
        asked.push(pid);
        return APP_ALWAYS_ON_TOP;
      },
    });
    expect(guard.verdict().pid).toBe(777);
    expect(asked).toEqual([777]);
  });
});
