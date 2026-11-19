#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { launchFreshAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { bundleIdentifierForAppPath, currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appCliInTree } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const exec = promisify(execFile);
const options = parseCommonArgs(process.argv.slice(2));
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-BROWSER-SESSION-COOKIES', tier: 'tier1-local-shell', prefix: 'native-browser-session-cookies',
  metadata: { focus: "A browser tile's session cookies outlive the native app: the first launch on a home the Tauri app used sends the Tauri app's session sign-in with the page's first request, a session cookie set in the native app is sent again after a relaunch, a sign-out the page does in place, with no navigation, stays signed out after a relaunch, and so does one a busy page does just before Quit" },
});
const client = new UiAutomationClient({ appPath: options.appPath });
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const receipts = [];

const jar = path.join(os.homedir(), 'Library', 'Application Support', bundleIdentifierForAppPath(options.appPath), 'browser', 'cookies.json');
const tauriJar = [
  { name: 'tauri_sess', value: 'from-tauri', domain: '127.0.0.1', path: '/', secure: false, httpOnly: true, sameSite: 'Lax', expiry: null },
];

const signOutInPlace = `<button id="forget">Forget</button><button id="signout">Sign out</button><script>
document.cookie = 'js_sess=from-script; path=/';
document.getElementById('forget').onclick = () => {
  document.cookie = 'js_sess=; path=/; max-age=0';
  document.body.insertAdjacentHTML('beforeend', '<p id="forgotten">forgotten</p>');
};
document.getElementById('signout').onclick = async () => {
  await fetch('/signout');
  document.body.insertAdjacentHTML('beforeend', '<p id="signedout">signed out</p>');
};
</script>`;

const busy = `<button id="signout">Sign out</button><script>
(async () => { for (;;) await fetch('/tick'); })();
document.getElementById('signout').onclick = async () => {
  await fetch('/busy-signout');
  await fetch('/busy-signed-out');
};
</script>`;

const requests = [];
let signedOut = () => {};
const server = http.createServer((request, response) => {
  if (request.url === '/tick') return response.end();
  if (request.url === '/busy-signed-out') signedOut();
  requests.push({ url: request.url, cookie: request.headers.cookie ?? '' });
  runner.writeJson('requests.json', requests);
  response.setHeader('content-type', 'text/html');
  if (request.url === '/signin') response.setHeader('set-cookie', 'native_sess=from-native; Path=/');
  if (request.url === '/signout') response.setHeader('set-cookie', 'native_sess=; Path=/; Max-Age=0');
  if (request.url === '/busy-signin') response.setHeader('set-cookie', 'busy_sess=from-busy; Path=/');
  if (request.url === '/busy-signout') response.setHeader('set-cookie', 'busy_sess=; Path=/; Max-Age=0');
  const page = request.url.slice(1);
  response.end(`<title>${page}</title><h1 id="${page}">${request.url}</h1>${{ '/app': signOutInPlace, '/busy': busy }[request.url] ?? ''}`);
});
const firstRequestTo = (page, since) => requests.slice(since).find((request) => request.url === `/${page}`);

const cli = async (...args) => {
  try {
    const result = await exec(appCliInTree(options.appPath), args, { env: instanceCliEnv(currentHarnessInstance()), cwd: runner.sessionDir });
    receipts.push({ args, ...result });
    return result.stdout;
  } catch (error) {
    receipts.push({ args, stdout: error.stdout, stderr: error.stderr, code: error.code });
    throw error;
  } finally {
    runner.writeJson('cli.json', receipts);
  }
};

const jarNames = () => {
  try {
    return JSON.parse(fs.readFileSync(jar, 'utf8')).map((cookie) => cookie.name);
  } catch {
    return [];
  }
};

runner.registerCleanup('close_page_server', () => { server.closeAllConnections(); return new Promise((resolve) => server.close(resolve)); });
runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());
runner.registerCleanup('remove_cookie_jar', () => fs.rmSync(jar, { force: true }));

try {
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const at = (page) => `http://127.0.0.1:${server.address().port}/${page}`;

  await runner.step('the_tauri_app_left_a_session_sign_in', async () => {
    await client.quitApp();
    fs.mkdirSync(path.dirname(jar), { recursive: true });
    fs.writeFileSync(jar, JSON.stringify(tauriJar, null, 2), { mode: 0o600 });
  });
  await runner.step('launch_app', () => launchFreshAppAndConnect(client, observer));

  let desktop;
  await runner.step('make_a_desktop', async () => {
    const before = observer.currentDesktopId();
    desktop = await observer.createDesktop();
    runner.registerCleanup('close_desktop', () => {
      const left = observer.desktop(desktop.id);
      return left && observer.profileCommand('desktop_close', { desktop_id: left.id, expected_revision: left.revision });
    });
    if (before) runner.registerCleanup('restore_current_desktop', () => observer.returnToDesktop(before));
    await observer.setCurrentDesktop(desktop.id);
  });

  let shell;
  await runner.step('a_shell_to_open_the_browser_beside', async () => {
    const id = crypto.randomUUID();
    const cwd = path.join(runner.sessionDir, 'shell');
    fs.mkdirSync(cwd, { recursive: true });
    const spawned = observer.waitForMessage((message) => message.event === 'spawn_result' && message.id === id && message, 'shell spawned');
    observer.send({ cmd: 'spawn_session', id, agent: 'shell', cwd, profile_id: observer.profileId, placement: { desktop_id: desktop.id }, label: 'shell', cols: 80, rows: 24 });
    const result = await spawned;
    runner.assert(result.success && result.pane_id, 'spawn_session starts the shell', result);
    runner.registerCleanup('unregister_shell', () => observer.unregisterSession(id));
    shell = { id };
  });

  const browser = (...args) => cli('browser', ...args, '--session', shell.id);
  const waitForPage = (page) => browser('wait', '--using', 'css', '--value', `#${page}`, '--state', 'visible');

  await runner.step('the_first_request_carries_the_tauri_session_sign_in', async () => {
    await browser('open', at('first'));
    await waitForPage('first');
    const first = firstRequestTo('first', 0);
    runner.assert(first?.cookie.includes('tauri_sess=from-tauri'), "the page's first request sends the Tauri app's session cookie", first);
  });

  await runner.step('a_session_sign_in_in_the_native_app_is_kept', async () => {
    await browser('open', at('signin'));
    await waitForPage('signin');
    await browser('open', at('after'));
    await waitForPage('after');
    await observer.waitFor(() => jarNames().includes('native_sess'), 'the jar keeps the native session cookie');
  });

  await runner.step('a_relaunch_sends_both_session_cookies', async () => {
    const since = requests.length;
    await client.quitApp();
    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await observer.waitFor(() => firstRequestTo('after', since), 'the relaunched tile loads its page');
    const restored = firstRequestTo('after', since);
    runner.assert(restored.cookie.includes('native_sess=from-native'), "the restored page's first request sends the native session cookie", restored);
    runner.assert(restored.cookie.includes('tauri_sess=from-tauri'), "the restored page's first request still sends the Tauri session cookie", restored);
  });

  await runner.step('a_sign_out_in_place_stays_signed_out_after_a_relaunch', async () => {
    await browser('open', at('app'));
    await waitForPage('app');
    await observer.waitFor(() => jarNames().includes('js_sess'), "the jar keeps the page script's session cookie");
    await browser('click', '--selector', '#forget');
    await waitForPage('forgotten');
    await observer.waitFor(() => !jarNames().includes('js_sess'), 'the jar lets go of the cookie a script deleted');
    await browser('click', '--selector', '#signout');
    await waitForPage('signedout');
    await observer.waitFor(() => !jarNames().includes('native_sess'), 'the jar lets go of the cookie a request deleted');
    const since = requests.length;
    await client.quitApp();
    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await observer.waitFor(() => firstRequestTo('app', since), 'the relaunched tile loads its page');
    const restored = firstRequestTo('app', since);
    runner.assert(!restored.cookie.includes('native_sess'), "the restored page's first request does not send the session cookie a request deleted", restored);
    runner.assert(!restored.cookie.includes('js_sess'), "the restored page's first request does not send the session cookie a script deleted", restored);
  });

  await runner.step('a_sign_out_just_before_quit_stays_signed_out_after_a_relaunch', async () => {
    await browser('open', at('busy-signin'));
    await waitForPage('busy-signin');
    await observer.waitFor(() => jarNames().includes('busy_sess'), "the jar keeps the busy page's session cookie");
    await browser('open', at('busy'));
    await waitForPage('busy');
    const signOutDone = new Promise((resolve) => { signedOut = resolve; });
    await browser('click', '--selector', '#signout');
    await signOutDone;
    const since = requests.length;
    await client.quitApp();
    runner.writeJson('jar-after-quick-quit.json', jarNames());
    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await observer.waitFor(() => firstRequestTo('busy', since), 'the relaunched tile loads its page');
    const restored = firstRequestTo('busy', since);
    runner.assert(!restored.cookie.includes('busy_sess'), "the restored page's first request does not send the session cookie deleted just before Quit", restored);
  });

  await runner.finishSuccess({ desktopId: desktop.id, shell, jar });
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
