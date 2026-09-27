import os from 'node:os';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
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

    expect(options.appPath).toBe(installedAppPath('attn-dev'));
    expect(options.wsUrl).toBe('ws://127.0.0.1:29849/ws');
  });

  it('refuses the production instance without the explicit acknowledgement', () => {
    process.env.ATTN_HARNESS_INSTANCE = '';

    expect(() => parseCommonArgs([])).toThrow(
      'Refusing to run the real-app harness against production',
    );
  });

  it('allows the production instance only with the explicit acknowledgement', () => {
    process.env.ATTN_HARNESS_INSTANCE = '';

    expect(() => parseCommonArgs(['--run-against-prod'])).not.toThrow();
  });

  it('refuses an explicit production websocket while using the dev app', () => {
    expect(() => parseCommonArgs(['--ws-url', 'ws://127.0.0.1:9849/ws'])).toThrow(
      'Refusing to run the real-app harness against production',
    );
  });
});

describe('isDirectoryUnderRoot', () => {
  it('rejects a sibling directory that merely shares the root as a string prefix', () => {
    expect(isDirectoryUnderRoot('/tmp/attn-real-app-sessions-old/keep', ['/tmp/attn-real-app-sessions'])).toBe(false);
  });

  it('rejects an unrelated path', () => {
    expect(isDirectoryUnderRoot('/Users/victor/projects/other', ['/tmp/attn-real-app-sessions'])).toBe(false);
  });

  it('rejects a null or empty directory', () => {
    expect(isDirectoryUnderRoot(null, ['/tmp/attn-real-app-sessions'])).toBe(false);
    expect(isDirectoryUnderRoot('', ['/tmp/attn-real-app-sessions'])).toBe(false);
  });
});
