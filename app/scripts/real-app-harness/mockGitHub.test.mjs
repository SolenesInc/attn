import { execFileSync } from 'node:child_process';
import http from 'node:http';
import { describe, expect, it, vi } from 'vitest';
import {
  MOCK_GITHUB_FIXTURE,
  MOCK_GITHUB_HOST,
  MOCK_GITHUB_SERVER,
  MOCK_GITHUB_TOKEN,
  MOCK_GITHUB_VARS,
  ensureMockGitHubServer,
  mockGitHubLaunchEnv,
  mockGitHubTarget,
  stopMockGitHubServer,
} from './mockGitHub.mjs';

const devTarget = { profile: 'dev', appPath: '/tmp/attn-dev.app' };

describe('the GitHub the harness daemon talks to', () => {
  it('derives a stable per-profile URL, so one daemon serves the whole run', () => {
    const first = mockGitHubTarget('dev');
    expect(mockGitHubTarget('dev')).toEqual(first);
    expect(first.url).toBe(`http://127.0.0.1:${first.port}`);
    expect(first.host).toBe(MOCK_GITHUB_HOST);
    expect(first.token).toBe(MOCK_GITHUB_TOKEN);
  });

  it('carries every variable the daemon reads into the environment it inherits', () => {
    const run = vi.fn(() => JSON.stringify({ pid: 4242, started: true }));
    const env = { PATH: '/usr/bin' };

    const started = ensureMockGitHubServer({ ...devTarget, env, run, log: () => {} });

    expect(env).toEqual({
      PATH: '/usr/bin',
      ATTN_MOCK_GH_URL: started.url,
      ATTN_MOCK_GH_TOKEN: MOCK_GITHUB_TOKEN,
      ATTN_MOCK_GH_HOST: MOCK_GITHUB_HOST,
    });
    expect(MOCK_GITHUB_VARS.every((name) => name in env)).toBe(true);
    expect(mockGitHubLaunchEnv(env)).toEqual({
      ATTN_MOCK_GH_URL: started.url,
      ATTN_MOCK_GH_TOKEN: MOCK_GITHUB_TOKEN,
      ATTN_MOCK_GH_HOST: MOCK_GITHUB_HOST,
    });
  });

  it('hands the app launch nothing when no mock is running', () => {
    expect(mockGitHubLaunchEnv({ PATH: '/usr/bin' })).toEqual({});
  });

  it('asks the server to ensure exactly once, on the profile port and fixture', () => {
    const run = vi.fn(() => JSON.stringify({ pid: 7, started: false }));

    ensureMockGitHubServer({ ...devTarget, env: {}, run, log: () => {} });

    expect(run).toHaveBeenCalledTimes(1);
    const [, args] = run.mock.calls[0];
    expect(args).toEqual([
      MOCK_GITHUB_SERVER,
      '--ensure', '--port', String(mockGitHubTarget('dev').port),
      '--host', MOCK_GITHUB_HOST,
      '--fixture', MOCK_GITHUB_FIXTURE,
    ]);
  });

  it('never points a production daemon away from the real github.com', () => {
    const run = vi.fn();
    const env = {};

    expect(ensureMockGitHubServer({ profile: '', appPath: '/tmp/attn.app', env, run, log: () => {} })).toBeNull();
    expect(stopMockGitHubServer({ profile: '', appPath: '/tmp/attn.app', run, log: () => {} })).toBeNull();
    expect(run).not.toHaveBeenCalled();
    expect(env).toEqual({});
  });
});

describe('the mock server itself', () => {
  // A port outside every derived band, so a live profile mock is never touched.
  const port = 39917;
  // vitest's happy-dom blocks cross-origin fetch; the mock speaks plain HTTP.
  const control = (pathname) => new Promise((resolve, reject) => {
    http.get(`http://127.0.0.1:${port}${pathname}`, (response) => {
      let body = '';
      response.setEncoding('utf8');
      response.on('data', (chunk) => { body += chunk; });
      response.on('end', () => resolve(JSON.parse(body)));
    }).on('error', reject);
  });
  const ensure = () => JSON.parse(execFileSync(process.execPath, [
    MOCK_GITHUB_SERVER, '--ensure', '--port', String(port), '--host', MOCK_GITHUB_HOST, '--fixture', MOCK_GITHUB_FIXTURE,
  ], { encoding: 'utf8' }).trim());

  it('starts once and is reused by every later ensure', async () => {
    const first = ensure();
    try {
      expect(first.started).toBe(true);
      const second = ensure();
      expect(second).toEqual({ ...first, started: false });

      const search = await control('/search/issues?q=is%3Apr+is%3Aopen+review-requested%3A%40me');
      expect(search.total_count).toBeGreaterThan(0);
      expect(search.items.every((item) => item.repository_url.includes(MOCK_GITHUB_HOST))).toBe(true);

      const pr = search.items[0];
      const repo = pr.repository_url.split('/repos/')[1];
      const detail = await control(`/repos/${repo}/pulls/${pr.number}`);
      expect(detail.head.sha).toMatch(/^[0-9a-f]{40}$/);
      expect(detail.mergeable_state).toBeTruthy();
    } finally {
      execFileSync(process.execPath, [MOCK_GITHUB_SERVER, '--stop', '--port', String(port)], { encoding: 'utf8' });
    }
  });
});
