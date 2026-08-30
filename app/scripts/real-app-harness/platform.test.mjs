import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { appPlatformFor, LinuxWindowDriver } from './platform.mjs';

const darwin = appPlatformFor('darwin');
const linux = appPlatformFor('linux');

afterEach(() => {
  vi.restoreAllMocks();
});

describe('app tree layout', () => {
  it('reads a macOS bundle through Contents', () => {
    const appPath = path.join('/Users/someone', 'Applications', 'attn-dev.app');
    expect(darwin.appExecutableInTree(appPath)).toBe(path.join(appPath, 'Contents', 'MacOS', 'app'));
    expect(darwin.appDaemonInTree(appPath)).toBe(path.join(appPath, 'Contents', 'MacOS', 'attn'));
    expect(darwin.appBuildIdentityInTree(appPath)).toBe(
      path.join(appPath, 'Contents', 'Resources', 'build-identity.json'),
    );
  });

  it('reads a Linux install tree through bin and resources', () => {
    const appPath = path.join('/home/someone', '.local', 'share', 'attn-dev');
    expect(linux.appExecutableInTree(appPath)).toBe(path.join(appPath, 'bin', 'attn-app'));
    expect(linux.appDaemonInTree(appPath)).toBe(path.join(appPath, 'bin', 'attn'));
    expect(linux.appBuildIdentityInTree(appPath)).toBe(
      path.join(appPath, 'resources', 'build-identity.json'),
    );
  });

  it('treats a non-bundle macOS app path as the executable itself', () => {
    const executable = '/tmp/build/attn-dev.app.debug/app';
    expect(darwin.appExecutableInTree(executable)).toBe(executable);
    expect(darwin.appDaemonInTree(executable)).toBe('/tmp/build/attn-dev.app.debug/attn');
  });
});

describe('manifest wait floor', () => {
  it('leaves a caller budget alone on macOS and raises it on Linux', () => {
    expect(darwin.manifestWaitFloorMs).toBe(0);
    expect(linux.manifestWaitFloorMs).toBeGreaterThan(30_400);
  });
});

describe('linux app control', () => {
  it('quits by signalling the pid it was handed', async () => {
    const kill = vi.spyOn(process, 'kill').mockImplementation(() => true);
    await linux.requestQuit({ bundleId: 'com.attn.manager.dev', pid: 4242 });
    expect(kill).toHaveBeenCalledWith(4242, 'SIGTERM');
  });

  it('signals nothing without a pid', async () => {
    const kill = vi.spyOn(process, 'kill').mockImplementation(() => true);
    await linux.requestQuit({ bundleId: 'com.attn.manager.dev', pid: null });
    expect(kill).not.toHaveBeenCalled();
  });

  it('never discovers pids by command-line pattern', async () => {
    await expect(linux.listAppPids({ appPath: '/home/someone/.local/share/attn-dev' })).resolves.toEqual([]);
  });
});

describe('LinuxWindowDriver.waitForMainWindow', () => {
  it('returns the first mapped window xdotool reports for the pid', async () => {
    const run = vi.fn().mockResolvedValue({ stdout: '31457282\n31457284\n' });
    const driver = new LinuxWindowDriver({ appPath: '/home/someone/.local/share/attn-dev', run });

    await expect(driver.waitForMainWindow(1_000, 10, { pid: 4242 })).resolves.toBe(31457282);
    expect(run).toHaveBeenCalledWith('xdotool', ['search', '--onlyvisible', '--pid', '4242']);
  });

  it('skips observation when xdotool is not installed', async () => {
    const run = vi.fn().mockRejectedValue(Object.assign(new Error('spawn xdotool ENOENT'), { code: 'ENOENT' }));
    const driver = new LinuxWindowDriver({ run });

    await expect(driver.waitForMainWindow(1_000, 10, { pid: 4242 })).resolves.toBeNull();
    expect(run).toHaveBeenCalledTimes(1);
  });

  it('gives up when no window ever carries the pid', async () => {
    const run = vi.fn().mockRejectedValue(Object.assign(new Error('exit 1'), { code: 1 }));
    const driver = new LinuxWindowDriver({ run });

    await expect(driver.waitForMainWindow(60, 10, { pid: 4242 })).resolves.toBeNull();
    expect(run.mock.calls.length).toBeGreaterThan(1);
  });

  it('observes nothing without a pid', async () => {
    const run = vi.fn();
    const driver = new LinuxWindowDriver({ run });

    await expect(driver.waitForMainWindow(1_000, 10, {})).resolves.toBeNull();
    expect(run).not.toHaveBeenCalled();
  });
});
