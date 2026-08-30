import fs from 'node:fs';
import os from 'node:os';
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
  it('quits by signalling the pids it was handed', async () => {
    const kill = vi.spyOn(process, 'kill').mockImplementation(() => true);
    await linux.requestQuit({ bundleId: 'com.attn.manager.dev', pids: [4242, 4243] });
    expect(kill.mock.calls).toEqual([[4242, 'SIGTERM'], [4243, 'SIGTERM']]);
  });

  it('signals nothing without an owned pid', async () => {
    const kill = vi.spyOn(process, 'kill').mockImplementation(() => true);
    await linux.requestQuit({ bundleId: 'com.attn.manager.dev', pids: [] });
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

const procTreeRoots = [];

afterEach(() => {
  while (procTreeRoots.length > 0) {
    fs.rmSync(procTreeRoots.pop(), { recursive: true, force: true });
  }
});

function procTree() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-platform-'));
  procTreeRoots.push(root);
  const procRoot = path.join(root, 'proc');
  fs.mkdirSync(procRoot);
  return {
    root,
    procRoot,
    installTree(name) {
      const appPath = path.join(root, name);
      fs.mkdirSync(path.join(appPath, 'bin'), { recursive: true });
      fs.writeFileSync(path.join(appPath, 'bin', 'attn-app'), '#!/bin/sh\n');
      return appPath;
    },
    runs(pid, executablePath) {
      fs.mkdirSync(path.join(procRoot, String(pid)));
      fs.symlinkSync(executablePath, path.join(procRoot, String(pid), 'exe'));
    },
  };
}

const aliveChild = { exitCode: null, signalCode: null };

describe('linux pid ownership', () => {
  it('owns the pid it spawned even when no manifest ever appeared', () => {
    expect(linux.ownedPids({
      appPath: '/home/someone/.local/share/attn-dev',
      manifestPid: null,
      launch: { spawned: true, pid: 4242, child: aliveChild },
    })).toEqual({ pids: [4242], staleManifest: false });
  });

  it('drops a spawn pid whose child handle already exited', () => {
    expect(linux.ownedPids({
      appPath: '/home/someone/.local/share/attn-dev',
      launch: { spawned: true, pid: 4242, child: { exitCode: 0, signalCode: null } },
    })).toEqual({ pids: [], staleManifest: false });
  });

  it('owns a manifest pid that still runs the install tree executable', () => {
    const tree = procTree();
    const appPath = tree.installTree('attn-dev');
    tree.runs(4242, path.join(appPath, 'bin', 'attn-app'));

    expect(linux.ownedPids({ appPath, manifestPid: 4242, procRoot: tree.procRoot })).toEqual({
      pids: [4242],
      staleManifest: false,
    });
  });

  it('leaves a reused manifest pid running something else alone', () => {
    const tree = procTree();
    const appPath = tree.installTree('attn-dev');
    const stranger = tree.installTree('someone-else');
    tree.runs(4242, path.join(stranger, 'bin', 'attn-app'));

    expect(linux.ownedPids({ appPath, manifestPid: 4242, procRoot: tree.procRoot })).toEqual({
      pids: [],
      staleManifest: true,
    });
  });

  it('leaves a manifest pid that is gone alone', () => {
    const tree = procTree();
    const appPath = tree.installTree('attn-dev');

    expect(linux.ownedPids({ appPath, manifestPid: 4242, procRoot: tree.procRoot })).toEqual({
      pids: [],
      staleManifest: true,
    });
  });

  it('still owns an app running out of a replaced install tree', () => {
    const tree = procTree();
    const appPath = tree.installTree('attn-dev');
    tree.runs(4242, `${path.join(appPath, 'bin', 'attn-app')} (deleted)`);

    expect(linux.ownedPids({ appPath, manifestPid: 4242, procRoot: tree.procRoot })).toEqual({
      pids: [4242],
      staleManifest: false,
    });
  });

  it('matches an install tree reached through a symlink', () => {
    const tree = procTree();
    const realPath = tree.installTree('attn-dev-real');
    const linkedPath = path.join(tree.root, 'attn-dev-link');
    fs.symlinkSync(realPath, linkedPath);
    tree.runs(4242, path.join(realPath, 'bin', 'attn-app'));

    expect(linux.ownedPids({ appPath: linkedPath, manifestPid: 4242, procRoot: tree.procRoot })).toEqual({
      pids: [4242],
      staleManifest: false,
    });
  });

  it('keeps a spawn pid alongside a fenced-out manifest pid', () => {
    const tree = procTree();
    const appPath = tree.installTree('attn-dev');

    expect(linux.ownedPids({
      appPath,
      manifestPid: 4242,
      launch: { spawned: true, pid: 909, child: aliveChild },
      procRoot: tree.procRoot,
    })).toEqual({ pids: [909], staleManifest: true });
  });
});

describe('darwin pid ownership', () => {
  it('addresses the bundle, so only the manifest pid feeds the wait loop', () => {
    expect(darwin.ownedPids({
      appPath: '/Users/someone/Applications/attn-dev.app',
      manifestPid: 4242,
      launch: { spawned: true, pid: 909, child: aliveChild },
    })).toEqual({ pids: [4242], staleManifest: false });
    expect(darwin.ownedPids({ manifestPid: null })).toEqual({ pids: [], staleManifest: false });
  });
});
