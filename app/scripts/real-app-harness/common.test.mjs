import os from 'node:os';
import path from 'node:path';
import assert from 'node:assert/strict';
import { afterEach, beforeEach, describe, it } from 'node:test';
import { isDirectoryUnderRoot, parseCommonArgs } from './common.mjs';

function xdgDataHome() {
  return (process.env.XDG_DATA_HOME ?? '').trim() || path.join(os.homedir(), '.local', 'share');
}

function installedAppPath(appName) {
  return process.platform === 'darwin'
    ? path.join(os.homedir(), 'Applications', `${appName}.app`)
    : path.join(xdgDataHome(), appName);
}

const originalHarnessInstance = process.env.ATTN_HARNESS_INSTANCE;
const originalInstance = process.env.ATTN_INSTANCE;
const originalAppPath = process.env.ATTN_REAL_APP_PATH;
const originalWsUrl = process.env.ATTN_REAL_APP_WS_URL;

beforeEach(() => {
  delete process.env.ATTN_HARNESS_INSTANCE;
  delete process.env.ATTN_INSTANCE;
  delete process.env.ATTN_REAL_APP_PATH;
  delete process.env.ATTN_REAL_APP_WS_URL;
});

afterEach(() => {
  for (const [name, value] of [
    ['ATTN_HARNESS_INSTANCE', originalHarnessInstance],
    ['ATTN_INSTANCE', originalInstance],
    ['ATTN_REAL_APP_PATH', originalAppPath],
    ['ATTN_REAL_APP_WS_URL', originalWsUrl],
  ]) {
    if (value === undefined) delete process.env[name];
    else process.env[name] = value;
  }
});

describe('parseCommonArgs production safety', () => {
  it('defaults every real-app command to the isolated dev target', () => {
    const options = parseCommonArgs([]);

    assert.equal(options.appPath, installedAppPath('attn-dev'));
    assert.equal(options.wsUrl, 'ws://127.0.0.1:29849/ws');
  });

  it('refuses the production instance without the explicit acknowledgement', () => {
    process.env.ATTN_HARNESS_INSTANCE = '';

    assert.throws(() => parseCommonArgs([]), /Refusing to run the real-app harness against production/);
  });

  it('allows the production instance only with the explicit acknowledgement', () => {
    process.env.ATTN_HARNESS_INSTANCE = '';

    assert.doesNotThrow(() => parseCommonArgs(['--run-against-prod']));
  });

  it('refuses an explicit production websocket while using the dev app', () => {
    assert.throws(() => parseCommonArgs(['--ws-url', 'ws://127.0.0.1:9849/ws']), /Refusing to run the real-app harness against production/);
  });
});

describe('parseCommonArgs instance selection', () => {
  it('refuses an app path that names another instance than the harness targets', () => {
    assert.throws(
      () => parseCommonArgs(['--app-path', installedAppPath('attn-perf')]),
      /is instance "perf", but the harness targets instance "dev"[\s\S]*ATTN_HARNESS_INSTANCE=perf/,
    );
  });

  it('accepts an app path for the instance the harness targets', () => {
    process.env.ATTN_HARNESS_INSTANCE = 'dev';

    assert.equal(parseCommonArgs(['--app-path', '/tmp/build/attn-dev.app']).appPath, '/tmp/build/attn-dev.app');
  });
});

describe('isDirectoryUnderRoot', () => {
  it('rejects a sibling directory that merely shares the root as a string prefix', () => {
    assert.equal(isDirectoryUnderRoot('/tmp/attn-real-app-sessions-old/keep', ['/tmp/attn-real-app-sessions']), false);
  });

  it('rejects a null or empty directory', () => {
    assert.equal(isDirectoryUnderRoot(null, ['/tmp/attn-real-app-sessions']), false);
    assert.equal(isDirectoryUnderRoot('', ['/tmp/attn-real-app-sessions']), false);
  });
});
