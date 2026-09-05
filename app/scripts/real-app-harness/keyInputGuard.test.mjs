import { describe, expect, it } from 'vitest';
import {
  ALWAYS_ON_TOP_VAR,
  createKeyInputGuard,
  keyInputVerdict,
} from './keyInputGuard.mjs';

const APP_ENV_ALWAYS_ON_TOP = `ATTN_HARNESS_PARK_VISIBLE_PX=20 ${ALWAYS_ON_TOP_VAR}=1 HOME=/Users/x`;
const APP_ENV_OPTED_OUT = `ATTN_HARNESS_PARK_VISIBLE_PX=0 ${ALWAYS_ON_TOP_VAR}=0 HOME=/Users/x`;
const APP_ENV_UNSET = 'ATTN_HARNESS_PARK_VISIBLE_PX=0 HOME=/Users/x';

function guardOver(appEnvironment, { platform = 'darwin', pid = 4242 } = {}) {
  return createKeyInputGuard({
    platform,
    manifestPath: '/does/not/matter',
    readAppPid: () => pid,
    readEnvironment: () => appEnvironment,
  });
}

describe('keyInputVerdict', () => {
  it('says keys land wherever the window stayed focusable', () => {
    expect(keyInputVerdict({ platform: 'darwin', appRunning: true, appEnvironment: APP_ENV_OPTED_OUT }).reaches).toBe(true);
    expect(keyInputVerdict({ platform: 'darwin', appRunning: true, appEnvironment: APP_ENV_UNSET }).reaches).toBe(true);
    expect(keyInputVerdict({ platform: 'linux', appRunning: true, appEnvironment: APP_ENV_ALWAYS_ON_TOP }).reaches).toBe(true);
  });

  it('says keys land nowhere against a macOS always-on-top window', () => {
    const verdict = keyInputVerdict({ platform: 'darwin', appRunning: true, appEnvironment: APP_ENV_ALWAYS_ON_TOP });
    expect(verdict.reaches).toBe(false);
    expect(verdict.reason).toContain(`${ALWAYS_ON_TOP_VAR}=1`);
  });

  it('refuses the press when a running app will not say how it was launched', () => {
    const verdict = keyInputVerdict({ platform: 'darwin', appRunning: true, appEnvironment: null });
    expect(verdict.reaches).toBe(false);
    expect(verdict.reason).toContain('could not be read');
  });

  it('does not read a bare 1 out of a neighbouring variable', () => {
    const environment = `ATTN_HARNESS_PARK_VISIBLE_PX=1 ${ALWAYS_ON_TOP_VAR}=0`;
    expect(keyInputVerdict({ platform: 'darwin', appRunning: true, appEnvironment: environment }).reaches).toBe(true);
  });
});

describe('createKeyInputGuard', () => {
  it('names the variable, the app pid and the fix', () => {
    const guard = guardOver(APP_ENV_ALWAYS_ON_TOP);
    expect(() => guard.assertReaches('pressKey Escape')).toThrow(/pressKey Escape cannot reach attn/);
    expect(() => guard.assertReaches('pressKey Escape')).toThrow(new RegExp(`${ALWAYS_ON_TOP_VAR} = '0'`));
    expect(() => guard.assertReaches('pressKey Escape')).toThrow(/pid 4242/);
  });

  it('lets a press through once the scenario opted out', () => {
    expect(() => guardOver(APP_ENV_OPTED_OUT).assertReaches('pressEnter')).not.toThrow();
  });

  it('reads the app environment once per pid', () => {
    let reads = 0;
    let pid = 11;
    const guard = createKeyInputGuard({
      platform: 'darwin',
      manifestPath: '/does/not/matter',
      readAppPid: () => pid,
      readEnvironment: () => {
        reads += 1;
        return APP_ENV_ALWAYS_ON_TOP;
      },
    });
    guard.verdict();
    guard.verdict();
    expect(reads).toBe(1);
    pid = 12;
    guard.verdict();
    expect(reads).toBe(2);
  });

  it('fails the press when the running app environment cannot be read', () => {
    const guard = createKeyInputGuard({
      platform: 'darwin',
      manifestPath: '/does/not/matter',
      readAppPid: () => 4242,
      readEnvironment: () => { throw new Error('ps: no such process'); },
    });
    expect(() => guard.assertReaches('pressKey Escape')).toThrow(/could not be read/);
    expect(() => guard.assertReaches('pressKey Escape')).toThrow(/pid 4242/);
  });

  it('retries the read instead of caching an unreadable environment', () => {
    let reads = 0;
    const guard = createKeyInputGuard({
      platform: 'darwin',
      manifestPath: '/does/not/matter',
      readAppPid: () => 4242,
      readEnvironment: () => {
        reads += 1;
        if (reads === 1) {
          throw new Error('ps: transient');
        }
        return APP_ENV_OPTED_OUT;
      },
    });
    expect(() => guard.assertReaches('pressKey Escape')).toThrow(/could not be read/);
    expect(() => guard.assertReaches('pressKey Escape')).not.toThrow();
  });

  it('stays out of the way when no app is running to ask', () => {
    const guard = createKeyInputGuard({
      platform: 'darwin',
      manifestPath: '/does/not/matter',
      readAppPid: () => { throw new Error('no manifest'); },
      readEnvironment: () => { throw new Error('unreachable'); },
    });
    expect(() => guard.assertReaches('pressKey w')).not.toThrow();
  });

  it('reads the pid the manifest names and asks ps about that process', () => {
    const asked = [];
    const guard = createKeyInputGuard({
      platform: 'darwin',
      manifestPath: '/does/not/matter',
      readAppPid: () => 777,
      readEnvironment: (pid) => {
        asked.push(pid);
        return APP_ENV_ALWAYS_ON_TOP;
      },
    });
    expect(guard.verdict().pid).toBe(777);
    expect(asked).toEqual([777]);
  });
});
