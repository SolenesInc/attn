import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { appPlatformFor } from './platform.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

function xdgDataHome() {
  return (process.env.XDG_DATA_HOME ?? '').trim() || path.join(os.homedir(), '.local', 'share');
}

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe('UiAutomationClient.request', () => {
  it('retries transient session absence for session-scoped actions', async () => {
    vi.useFakeTimers();
    const client = new UiAutomationClient();
    client.requestOnce = vi.fn()
      .mockRejectedValueOnce(new Error('Automation request failed: split_pane: Session not found'))
      .mockResolvedValueOnce({ paneId: 'pane-2' });

    const pending = client.request('split_pane', { sessionId: 'session-1' }, { timeoutMs: 2_000 });
    await vi.advanceTimersByTimeAsync(250);

    await expect(pending).resolves.toEqual({ paneId: 'pane-2' });
    expect(client.requestOnce).toHaveBeenCalledTimes(2);
  });

  it('does not retry transient session absence for non-session actions by default', async () => {
    const client = new UiAutomationClient();
    client.requestOnce = vi.fn().mockRejectedValue(new Error('Automation request failed: list_sessions: Session not found'));

    await expect(client.request('list_sessions', {}, { timeoutMs: 2_000 })).rejects.toThrow(
      'Automation request failed: list_sessions: Session not found',
    );
    expect(client.requestOnce).toHaveBeenCalledTimes(1);
  });
});

describe('UiAutomationClient.waitForManifest', () => {
  it('uses the caller timeout without a platform floor', async () => {
    vi.useFakeTimers();
    const client = new UiAutomationClient({
      manifestPath: path.join(os.tmpdir(), `attn-harness-manifest-that-never-appears-${process.pid}.json`),
    });

    const pending = expect(client.waitForManifest(200)).rejects.toThrow(
      'Timed out waiting for UI automation manifest after 200ms',
    );
    await vi.advanceTimersByTimeAsync(200);
    await pending;
  });
});

describe('UiAutomationClient production safety', () => {
  it('refuses a production app target without the explicit acknowledgement', () => {
    expect(() => new UiAutomationClient({
      appPath: path.join(os.homedir(), 'Applications', 'attn.app'),
      bundleId: 'com.attn.manager',
    })).toThrow('Refusing to run the real-app harness against production');
  });

  it('uses the production bundle and manifest for an acknowledged production app path', () => {
    const originalArgv = process.argv;
    process.argv = [...process.argv, '--run-against-prod'];
    try {
      const client = new UiAutomationClient({
        appPath: path.join(os.homedir(), 'Applications', 'attn.app'),
      });

      const expectedManifestPath = process.platform === 'darwin'
        ? path.join(os.homedir(), 'Library', 'Application Support', 'com.attn.manager', 'debug', 'ui-automation.json')
        : path.join(xdgDataHome(), 'com.attn.manager', 'debug', 'ui-automation.json');

      expect(client.bundleId).toBe('com.attn.manager');
      expect(client.manifestPath).toBe(expectedManifestPath);
    } finally {
      process.argv = originalArgv;
    }
  });
});

describe('UiAutomationClient.waitForFrontendResponsive', () => {
  it('fails fast when get_state reports a daemon version mismatch', async () => {
    const client = new UiAutomationClient();
    client.ensureBuildMatchesCurrentSource = vi.fn().mockResolvedValue(undefined);
    client.request = vi.fn().mockResolvedValue({
      daemonReady: false,
      connectionError: 'Version mismatch: daemon v50, app v49. Restart/reinstall required.',
    });

    await expect(client.waitForFrontendResponsive(200, 'list_sessions')).rejects.toThrow(
      'daemon not ready: Version mismatch: daemon v50, app v49. Restart/reinstall required.',
    );
    expect(client.request).toHaveBeenCalledWith('get_state', {}, { timeoutMs: 200 });
    expect(client.request).not.toHaveBeenCalledWith('list_sessions', {}, { timeoutMs: 200 });
  });

  it('checks daemon readiness before issuing the requested action', async () => {
    const client = new UiAutomationClient();
    client.ensureBuildMatchesCurrentSource = vi.fn().mockResolvedValue(undefined);
    client.request = vi.fn()
      .mockResolvedValueOnce({
        daemonReady: true,
        connectionError: null,
        appBuild: {
          sourceFingerprint: 'git:abc',
        },
      })
      .mockResolvedValueOnce({
        sessions: [],
      });

    await expect(client.waitForFrontendResponsive(500, 'list_sessions')).resolves.toEqual({ sessions: [] });
    expect(client.request.mock.calls).toEqual([
      ['get_state', {}, { timeoutMs: 500 }],
      ['list_sessions', {}, { timeoutMs: 500 }],
    ]);
    expect(client.ensureBuildMatchesCurrentSource).toHaveBeenCalledTimes(1);
  });
});

describe('UiAutomationClient.ensureBuildMatchesCurrentSource', () => {
  it('rejects a packaged app built from a different source fingerprint', async () => {
    const client = new UiAutomationClient();
    client.getCurrentSourceIdentity = vi.fn().mockResolvedValue({ fingerprint: 'tree:current' });

    await expect(client.ensureBuildMatchesCurrentSource({
      appBuild: {
        sourceFingerprint: 'git:old',
      },
    })).rejects.toThrow(
      'packaged app source mismatch: app reports git:old, current source is tree:current; rebuild and reinstall attn.app',
    );
  });

  it('rejects a resolved daemon binary built from a different source fingerprint', async () => {
    const client = new UiAutomationClient();
    client.getCurrentSourceIdentity = vi.fn().mockResolvedValue({ fingerprint: 'tree:current' });
    client.resolveDaemonBinaryPath = vi.fn().mockReturnValue('/tmp/attn');
    client.readBinaryBuildInfo = vi.fn().mockResolvedValue({
      sourceFingerprint: 'git:stale-daemon',
    });

    await expect(client.ensureBuildMatchesCurrentSource({
      appBuild: {
        sourceFingerprint: 'tree:current',
      },
    })).rejects.toThrow(
      'daemon source mismatch: /tmp/attn reports git:stale-daemon, current source is tree:current; rebuild the resolved daemon binary before running real-app scenarios',
    );
  });
});

describe('UiAutomationClient.quitApp ownership', () => {
  it('reaps the pid it spawned on Linux even when no manifest ever appeared', async () => {
    const client = new UiAutomationClient({
      manifestPath: path.join(os.tmpdir(), 'attn-harness-manifest-that-never-appeared.json'),
      platform: appPlatformFor('linux'),
    });
    client.launch = { spawned: true, pid: 4242, child: { exitCode: null, signalCode: null } };
    const signals = [];
    vi.spyOn(process, 'kill').mockImplementation((pid, signal) => {
      if (signal === 0) {
        throw Object.assign(new Error('ESRCH'), { code: 'ESRCH' });
      }
      signals.push([pid, signal]);
      return true;
    });

    await client.quitApp(5_000);

    expect(signals).toEqual([[4242, 'SIGTERM']]);
    expect(client.launch).toBeNull();
  });

  it('treats a pid that stopped passing the fence as gone while it still answers signal 0', async () => {
    const { platform, disown } = disownablePlatform(4242);
    const client = disownableClient(platform);
    const signals = recordSignals({ alive: true });

    await client.quitApp(5_000);

    expect(signals).toEqual([[4242, 'SIGTERM']]);
    expect(client.launch).toBeNull();
    expect(disown).toHaveBeenCalledTimes(1);
  });

  it('witnesses ownership lost between the quit request and escalation', async () => {
    vi.useFakeTimers();
    const owner = { owned: true };
    const platform = {
      ...appPlatformFor('linux'),
      ownedPids: () => ({ pids: owner.owned ? [4242] : [], staleManifest: false }),
    };
    const client = disownableClient(platform);
    const signals = recordSignals({ alive: true });
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});

    const pending = client.quitApp(1_000);
    await vi.advanceTimersByTimeAsync(800);
    owner.owned = false;
    await vi.advanceTimersByTimeAsync(2_000);
    await pending;

    expect(signals).toEqual([[4242, 'SIGTERM']]);
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('pid 4242 is no longer ours'));
    expect(client.launch).toBeNull();
  });
});

function disownableClient(platform) {
  const client = new UiAutomationClient({
    manifestPath: path.join(os.tmpdir(), 'attn-harness-manifest-that-never-appeared.json'),
    platform,
  });
  client.launch = { spawned: true, pid: 4242, child: { exitCode: null, signalCode: null } };
  return client;
}

function disownablePlatform(pid) {
  const owner = { owned: true };
  const disown = vi.fn(async ({ pids }) => {
    for (const signalled of pids) {
      process.kill(signalled, 'SIGTERM');
    }
    owner.owned = false;
  });
  return {
    disown,
    platform: {
      ...appPlatformFor('linux'),
      ownedPids: () => ({ pids: owner.owned ? [pid] : [], staleManifest: false }),
      requestQuit: disown,
    },
  };
}

function recordSignals({ alive }) {
  const signals = [];
  vi.spyOn(process, 'kill').mockImplementation((pid, signal) => {
    if (signal === 0) {
      if (alive) {
        return true;
      }
      throw Object.assign(new Error('ESRCH'), { code: 'ESRCH' });
    }
    signals.push([pid, signal]);
    return true;
  });
  return signals;
}

describe('UiAutomationClient.requestOnce timeouts', () => {
  it('names occlusion when a screenshot stalls to its cap', async () => {
    const net = await import('node:net');
    const server = net.createServer((socket) => {
      socket.on('data', () => {});
    });
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const manifestPath = path.join(
      os.tmpdir(),
      `attn-harness-manifest-silent-${process.pid}-${Date.now()}.json`,
    );
    const manifest = { token: 'test-token', port: server.address().port };
    const fs = await import('node:fs');
    fs.writeFileSync(manifestPath, JSON.stringify(manifest));
    try {
      const client = new UiAutomationClient({ manifestPath });
      await expect(client.requestOnce('capture_screenshot_data', {}, 100)).rejects.toThrow(
        /occluded and rAF-throttled/,
      );
      await expect(client.requestOnce('list_sessions', {}, 100)).rejects.toThrow(
        /Automation request timed out: list_sessions/,
      );
      await expect(client.requestOnce('list_sessions', {}, 100)).rejects.not.toThrow(
        /occluded/,
      );
    } finally {
      fs.rmSync(manifestPath, { force: true });
      await new Promise((resolve) => server.close(resolve));
    }
  });
});
