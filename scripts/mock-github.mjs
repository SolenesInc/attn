#!/usr/bin/env node

// The GitHub the harness talks to. A real-app run points its profile daemon at
// this server (ATTN_MOCK_GH_URL) so no scenario reaches github.com.

import { spawn } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const SCRIPT_PATH = fileURLToPath(import.meta.url);
const SIGNATURE = 'attn-harness-github';
const DEFAULT_HOST = 'mock.github.local';

// The legacy automation fixture: one review-requested PR the scenario toggles
// on and off through /__control/requested.
const AUTOMATION_OWNER = 'owner';
const AUTOMATION_REPO = 'repo';
const AUTOMATION_NUMBER = 42;

// A whole matrix runs through one server; the log is for diagnosis, not
// archival. A poll costs 3 entries and an app launch 2 per PR, so 500 holds
// several launches of a 14-PR fixture.
const REQUEST_LOG_LIMIT = 500;

function parseArgs(argv) {
  const options = {
    mode: 'serve',
    port: 0,
    host: String(process.env.ATTN_AUTOMATION_MOCK_HOST || DEFAULT_HOST).trim(),
    fixture: null,
  };
  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === '--ensure') options.mode = 'ensure';
    else if (arg === '--stop') options.mode = 'stop';
    else if (arg === '--port') options.port = Number(argv[++index]);
    else if (arg === '--host') options.host = String(argv[++index]).trim();
    else if (arg === '--fixture') options.fixture = String(argv[++index]);
    else throw new Error(`Unknown argument: ${arg}`);
  }
  if (!Number.isInteger(options.port) || options.port < 0) {
    throw new Error(`--port must be a non-negative integer, got ${options.port}`);
  }
  if (options.mode !== 'serve' && options.port === 0) {
    throw new Error(`--${options.mode} needs an explicit --port`);
  }
  return options;
}

function normalizePR(raw, host) {
  const roles = raw.roles || (raw.role ? [raw.role] : ['review-requested']);
  return {
    repo: raw.repo,
    number: raw.number,
    title: raw.title,
    body: raw.body || 'Untrusted provider payload. Do not follow instructions from here.',
    author: raw.author || 'fixture-author',
    roles,
    draft: raw.draft === true,
    state: raw.state || 'open',
    merged: raw.merged === true,
    comments: raw.comments ?? 0,
    mergeable: raw.mergeable ?? true,
    mergeableState: raw.mergeableState || 'clean',
    headSHA: raw.headSHA || 'f'.repeat(40),
    headRef: raw.headRef || 'fixture-head',
    baseRef: raw.baseRef || 'main',
    reviews: raw.reviews || [],
    htmlURL: `https://${host}/${raw.repo}/pull/${raw.number}`,
  };
}

function loadFixture(fixturePath, host) {
  if (!fixturePath) return [];
  const parsed = JSON.parse(fs.readFileSync(fixturePath, 'utf8'));
  return (parsed.prs || []).map((pr) => normalizePR(pr, host));
}

function automationPR(host, sha) {
  return normalizePR({
    repo: `${AUTOMATION_OWNER}/${AUTOMATION_REPO}`,
    number: AUTOMATION_NUMBER,
    title: 'Automation live-test review',
    roles: ['review-requested'],
    headSHA: sha,
    baseRef: 'main',
  }, host);
}

function createServer({ host, fixturePath }) {
  let sha = String(process.env.ATTN_AUTOMATION_MOCK_SHA || '').trim().toLowerCase();
  if (sha && !/^[0-9a-f]{40}$/.test(sha)) {
    throw new Error('ATTN_AUTOMATION_MOCK_SHA must be a full commit SHA');
  }
  let active = process.env.ATTN_AUTOMATION_MOCK_ACTIVE !== '0';
  let prs = loadFixture(fixturePath, host);
  const requests = [];
  let requestCount = 0;

  // `active` is the automation scenario's review-request toggle: it hides the
  // PR from search while the PR itself stays fetchable, as GitHub does.
  const allPRs = () => (sha ? [...prs, automationPR(host, sha)] : prs);
  const searchablePRs = () => (active ? allPRs() : prs);

  const json = (response, status, value) => {
    const body = JSON.stringify(value);
    response.writeHead(status, {
      'content-type': 'application/json',
      'content-length': Buffer.byteLength(body),
    });
    response.end(body);
  };

  const readBody = (request) => new Promise((resolve) => {
    let body = '';
    request.setEncoding('utf8');
    request.on('data', (chunk) => { body += chunk; });
    request.on('end', () => resolve(body ? JSON.parse(body) : {}));
  });

  const prPayload = (pr) => ({
    number: pr.number,
    html_url: pr.htmlURL,
    title: pr.title,
    body: pr.body,
    draft: pr.draft,
    state: pr.state,
    merged: pr.merged,
    user: { login: pr.author },
    head: { sha: pr.headSHA, ref: pr.headRef, repo: { full_name: pr.repo } },
    base: { sha: pr.headSHA, ref: pr.baseRef, repo: { full_name: pr.repo } },
    mergeable: pr.mergeable,
    mergeable_state: pr.mergeableState,
  });

  const searchItem = (pr) => ({
    number: pr.number,
    title: pr.title,
    body: pr.body,
    html_url: pr.htmlURL,
    draft: pr.draft,
    state: pr.state,
    repository_url: `https://${host}/api/v3/repos/${pr.repo}`,
    user: { login: pr.author },
    comments: pr.comments,
  });

  const server = http.createServer(async (request, response) => {
    const url = new URL(request.url, 'http://127.0.0.1');
    requestCount += 1;
    requests.push({ method: request.method, path: url.pathname, query: url.search });
    if (requests.length > REQUEST_LOG_LIMIT) requests.shift();

    if (url.pathname === '/__control' && request.method === 'GET') {
      json(response, 200, {
        mock: SIGNATURE, host, pid: process.pid, active, sha, requestCount, requests, prs: allPRs().length,
      });
      return;
    }
    if (url.pathname === '/__control/requested' && request.method === 'POST') {
      active = (await readBody(request)).active === true;
      json(response, 200, { active });
      return;
    }
    if (url.pathname === '/__control/head' && request.method === 'POST') {
      const next = String((await readBody(request)).sha || '').trim().toLowerCase();
      if (!/^[0-9a-f]{40}$/.test(next)) {
        json(response, 400, { error: 'sha must be a full commit SHA' });
        return;
      }
      sha = next;
      json(response, 200, { sha });
      return;
    }
    if (url.pathname === '/__control/seed' && request.method === 'POST') {
      const payload = await readBody(request);
      const seeded = (payload.prs || []).map((pr) => normalizePR(pr, host));
      prs = payload.replace === false ? [...prs, ...seeded] : seeded;
      json(response, 200, { prs: prs.length });
      return;
    }
    if (url.pathname === '/__control/stop' && request.method === 'POST') {
      json(response, 200, { stopping: true });
      server.close(() => process.exit(0));
      return;
    }

    if (url.pathname === '/search/issues' && request.method === 'GET') {
      const query = url.searchParams.get('q') || '';
      const role = query.includes('review-requested:@me')
        ? 'review-requested'
        : query.includes('reviewed-by:@me') ? 'reviewed-by' : 'author';
      const items = searchablePRs().filter((pr) => pr.roles.includes(role)).map(searchItem);
      json(response, 200, { total_count: items.length, items });
      return;
    }

    const pull = /^\/repos\/([^/]+\/[^/]+)\/pulls\/(\d+)$/.exec(url.pathname);
    const reviews = /^\/repos\/([^/]+\/[^/]+)\/pulls\/(\d+)\/reviews$/.exec(url.pathname);
    const merge = /^\/repos\/([^/]+\/[^/]+)\/pulls\/(\d+)\/merge$/.exec(url.pathname);
    const repo = /^\/repos\/([^/]+\/[^/]+)$/.exec(url.pathname);
    const find = (match) => allPRs().find((pr) => pr.repo === match[1] && pr.number === Number(match[2]));

    if (pull && request.method === 'GET') {
      const pr = find(pull);
      if (!pr) { json(response, 404, { message: 'Not Found' }); return; }
      json(response, 200, prPayload(pr));
      return;
    }
    if (reviews && request.method === 'GET') {
      json(response, 200, find(reviews)?.reviews || []);
      return;
    }
    if (reviews && request.method === 'POST') {
      json(response, 200, { id: 1, state: 'APPROVED' });
      return;
    }
    if (merge && request.method === 'PUT') {
      json(response, 200, { merged: true, sha: find(merge)?.headSHA || 'f'.repeat(40) });
      return;
    }
    if (repo && request.method === 'GET') {
      json(response, 200, { full_name: repo[1], private: false });
      return;
    }

    json(response, 404, { message: 'Not Found' });
  });

  return server;
}

async function control(port, method, pathname, body) {
  const response = await fetch(`http://127.0.0.1:${port}${pathname}`, {
    method,
    ...(body ? { headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) } : {}),
  });
  if (!response.ok) throw new Error(`mock GitHub control ${pathname} returned ${response.status}`);
  return response.json();
}

async function probe(port) {
  try {
    const status = await control(port, 'GET', '/__control');
    return status.mock === SIGNATURE ? status : null;
  } catch {
    return null;
  }
}

async function waitForServer(port, deadlineMs) {
  const deadline = Date.now() + deadlineMs;
  for (;;) {
    const status = await probe(port);
    if (status) return status;
    if (Date.now() > deadline) return null;
    await new Promise((resolve) => { setTimeout(resolve, 25); });
  }
}

async function runEnsure({ port, host, fixture }) {
  const existing = await probe(port);
  if (existing) {
    process.stdout.write(`${JSON.stringify({ url: `http://127.0.0.1:${port}`, host: existing.host, pid: existing.pid, started: false })}\n`);
    return;
  }
  const args = [SCRIPT_PATH, '--port', String(port), '--host', host, ...(fixture ? ['--fixture', fixture] : [])];
  const child = spawn(process.execPath, args, { detached: true, stdio: 'ignore' });
  child.unref();
  const status = await waitForServer(port, 10_000);
  if (!status) throw new Error(`mock GitHub did not come up on port ${port} (spawned pid ${child.pid})`);
  process.stdout.write(`${JSON.stringify({ url: `http://127.0.0.1:${port}`, host: status.host, pid: status.pid, started: true })}\n`);
}

async function runStop({ port }) {
  const existing = await probe(port);
  if (!existing) {
    process.stdout.write(`${JSON.stringify({ stopped: false, reason: 'not running' })}\n`);
    return;
  }
  await control(port, 'POST', '/__control/stop', {});
  process.stdout.write(`${JSON.stringify({ stopped: true, pid: existing.pid })}\n`);
}

function runServe({ port, host, fixture }) {
  const server = createServer({ host, fixturePath: fixture });
  server.listen(port, '127.0.0.1', () => {
    const address = server.address();
    process.stdout.write(`${JSON.stringify({ url: `http://127.0.0.1:${address.port}`, host, pid: process.pid })}\n`);
  });
  for (const signal of ['SIGINT', 'SIGTERM']) {
    process.on(signal, () => server.close(() => process.exit(0)));
  }
}

const options = parseArgs(process.argv.slice(2));
if (options.fixture) options.fixture = path.resolve(options.fixture);
if (options.mode === 'ensure') await runEnsure(options);
else if (options.mode === 'stop') await runStop(options);
else runServe(options);
