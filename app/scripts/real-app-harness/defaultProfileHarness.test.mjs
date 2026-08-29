import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { assertDefaultProfileHarnessIsolation, defaultProfileHarnessEnv } from './defaultProfileHarness.mjs';

const roots = [];

function fixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-default-harness-'));
  roots.push(root);
  fs.chmodSync(root, 0o700);
  const dataDir = path.join(root, 'data');
  const productionRoot = path.join(root, 'production');
  fs.mkdirSync(dataDir, { mode: 0o700 });
  fs.mkdirSync(productionRoot, { mode: 0o700 });
  return {
    dataDir,
    productionRoot,
    toolHome: path.join(dataDir, 'tool-home'),
    codexHome: path.join(dataDir, 'tool-home', '.codex'),
    notebookRoot: path.join(dataDir, 'notebook'),
    appPath: '/tmp/attn-legacy-recovery.app',
    bundleId: 'com.attn.manager.legacy-recovery',
    wsUrl: 'ws://127.0.0.1:29150/ws',
  };
}

afterEach(() => {
  for (const root of roots.splice(0)) fs.rmSync(root, { recursive: true, force: true });
});

describe('default profile harness isolation', () => {
  it('accepts a direct owner-only world and scrubs inherited routing', () => {
    const target = fixture();
    expect(assertDefaultProfileHarnessIsolation(target).dataDir).toBe(fs.realpathSync(target.dataDir));
    const env = defaultProfileHarnessEnv({ ...target, wsPort: 29150 });
    expect(env.ATTN_PROFILE).toBe('');
    expect(env.ATTN_DATA_DIR).toBe(target.dataDir);
    expect(env.ATTN_HARNESS_DATA_DIR).toBe(target.dataDir);
    expect(env.ATTN_HARNESS_NOTEBOOK_ROOT).toBe(target.notebookRoot);
    expect(env.ATTN_DB_PATH).toBeUndefined();
    expect(env.HOME).toBe(process.env.HOME);
  });

  it('refuses production aliases, roots outside the world, and production endpoints', () => {
    const target = fixture();
    const alias = path.join(path.dirname(target.dataDir), 'alias');
    fs.symlinkSync(target.productionRoot, alias);
    expect(() => assertDefaultProfileHarnessIsolation({ ...target, dataDir: alias }))
      .toThrow(/direct directory/);
    expect(() => assertDefaultProfileHarnessIsolation({ ...target, toolHome: '/tmp/outside' }))
      .toThrow(/outside isolated data root/);
    expect(() => assertDefaultProfileHarnessIsolation({ ...target, wsUrl: 'ws://127.0.0.1:9849/ws' }))
      .toThrow(/unsafe.*websocket/);
    expect(() => assertDefaultProfileHarnessIsolation({ ...target, appPath: '/tmp/attn.app' }))
      .toThrow(/production app bundle/);
  });
});
