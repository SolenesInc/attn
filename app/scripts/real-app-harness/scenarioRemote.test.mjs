import { describe, expect, it } from 'vitest';

import { buildRemoteHarnessPaths } from './scenarioRemote.mjs';

describe('remote harness paths', () => {
  it('keeps the daemon and worker sockets inside the Linux sockaddr limit', () => {
    const runId = 'scenario-tr205-remote-relaunch-close-redraw-2026-08-30T16-35-54-769Z';
    const paths = buildRemoteHarnessPaths('/home/victor', runId);
    const workerSocketDir = `${paths.remoteHarnessRoot}/workers/d-${'0'.repeat(32)}/sock`;

    expect(paths.remoteHarnessRoot).not.toContain(runId);
    expect(Buffer.byteLength(`${paths.remoteHarnessSocket}.listen`)).toBeLessThan(108);
    expect(Buffer.byteLength(workerSocketDir)).toBeLessThan(108);
  });
});
