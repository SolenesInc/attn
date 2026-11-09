#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshAppAndConnect, parseCommonArgs, pressShortcutKeys } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2).filter((arg) => arg !== '--'));
const instance = currentHarnessInstance();
if (!instance) throw new Error('The self-update scenario requires a named, non-production instance');
const runner = createScenarioRunner(options, { scenarioId: 'SELF-UPDATE', tier: 'tier2-local-mock-agent', prefix: 'scenario-self-update' });
const client = new UiAutomationClient(options);
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const attnBinary = appCliInTree(options.appPath);
const env = instanceCliEnv(instance);
const attn = (...args) => execFileSync(attnBinary, args, { encoding: 'utf8', env, stdio: ['ignore', 'pipe', 'ignore'] });
const status = () => JSON.parse(attn('update', 'status', '--json'));

const BANNER = '[data-testid="update-run-banner"]';
const UPDATE_TIMEOUT_MS = 120_000;
const QUITS_THE_APP = { withoutReceipt: 'the update quits the app, possibly before its webview reports the input' };
const treeDaemon = path.join(options.appPath, 'bin', 'attnd');

const FIXTURE_MAKEFILE = `build-app:
\tpython3 patch.py
install-staged:
\t"${attnBinary}" instance stop-app --instance "$(INSTANCE)"
\tcp attnd.next "${treeDaemon}.next"
\tmv "${treeDaemon}.next" "${treeDaemon}"
\t"${treeDaemon}" daemon ensure
`;

const FIXTURE_PATCH = `import json, pathlib, subprocess
pristine = pathlib.Path('attnd.pristine').read_bytes()
count = pathlib.Path('count')
n = int(count.read_text()) + 1 if count.exists() else 1
count.write_text(str(n))
info = json.loads(subprocess.check_output(['./attnd.pristine', '--build-info-json']))
old = info['sourceFingerprint'].encode()
new = old[:-1] + b'ghijklmnop'[n:n+1]
assert new != old and old in pristine
next_build = pristine.replace(old, new)
pathlib.Path('attnd.next').write_bytes(next_build)
pathlib.Path('attnd.next').chmod(0o755)
pathlib.Path('attnd').write_bytes(next_build)
pathlib.Path('attnd').chmod(0o755)
`;

async function reconnectAfterRelaunch(previousApp) {
  await observer.close();
  client.forgetLaunchedApp();
  await observer.waitFor(() => client.readManifest()?.pid !== previousApp, 'the update to relaunch the app', UPDATE_TIMEOUT_MS);
  await client.waitForManifest(UPDATE_TIMEOUT_MS);
  await client.waitForReady(UPDATE_TIMEOUT_MS);
  await client.waitForFrontendResponsive(UPDATE_TIMEOUT_MS);
  await client.request('dismiss_whats_new', {}).catch(() => {});
  await observer.connect();
}

async function expectUpdateFinished(previousBuild, previousApp, stepLabel) {
  await reconnectAfterRelaunch(previousApp);
  await client.request('dom_wait', { selector: `${BANNER}[data-phase="done"]`, timeoutMs: UPDATE_TIMEOUT_MS });
  const after = status();
  runner.assert(after.run?.phase === 'done' && after.run.target_build !== previousBuild, `${stepLabel}: the run is done on a new build`, JSON.stringify(after.run));
  return after.run.target_build;
}

async function runPaletteCommand(text, enterOptions) {
  await pressShortcutKeys(client, driver, 'ui.commandPalette');
  await driver.typeInto('.unified-palette-input', text);
  await driver.pressEnter({}, enterOptions);
}

runner.registerCleanup('close_observer', () => observer.close());
runner.registerCleanup('quit_app', () => client.quitApp());

try {
  const fixture = path.join(runner.sessionDir, 'update-origin');
  const pristine = path.join(fixture, 'attnd.pristine');
  fs.mkdirSync(fixture, { recursive: true });
  fs.copyFileSync(treeDaemon, pristine);
  fs.writeFileSync(path.join(fixture, 'Makefile'), FIXTURE_MAKEFILE);
  fs.writeFileSync(path.join(fixture, 'patch.py'), FIXTURE_PATCH);
  const originFile = path.join(env.ATTN_DATA_DIR ?? path.join(process.env.HOME, `.attn-${instance}`), 'origin.json');
  const originBefore = fs.existsSync(originFile) ? fs.readFileSync(originFile) : null;
  fs.writeFileSync(originFile, JSON.stringify({ worktree: fixture, recordedAt: new Date().toISOString() }));
  runner.registerCleanup('restore_install', () => {
    if (originBefore) fs.writeFileSync(originFile, originBefore);
    execFileSync(attnBinary, ['instance', 'stop-app', '--instance', instance], { env, stdio: 'ignore' });
    fs.copyFileSync(pristine, `${treeDaemon}.next`);
    fs.renameSync(`${treeDaemon}.next`, treeDaemon);
    execFileSync(treeDaemon, ['daemon', 'ensure'], { env, stdio: 'ignore' });
  });

  await runner.step('launch', async () => {
    attn('daemon', 'stop');
    await launchFreshAppAndConnect(client, observer);
    await client.request('set_setting', { key: 'self_update_enabled', value: 'true' });
    await observer.waitFor(() => observer.getSetting('self_update_enabled') === 'true', 'self-update on');
  });

  let build = '';

  await runner.step('update_from_settings_reaches_the_relaunched_app', async () => {
    const app = client.readManifest().pid;
    await pressShortcutKeys(client, driver, 'ui.openSettings');
    await driver.click('[data-testid="settings-nav-updates"]');
    await driver.click('.settings-update-action .banner-button', QUITS_THE_APP);
    build = await expectUpdateFinished(build, app, 'Settings');
  });

  await runner.step('update_from_the_command_palette', async () => {
    const app = client.readManifest().pid;
    await runPaletteCommand('Update attn', QUITS_THE_APP);
    build = await expectUpdateFinished(build, app, 'Cmd+K');
  });

  await runner.step('an_agent_request_is_confirmed_from_the_notification', async () => {
    const app = client.readManifest().pid;
    attn('update', 'request', '--reason', 'scenario');
    await runPaletteCommand('notifications');
    const update = '.notification-row.is-unread .notification-row-head';
    await client.request('dom_wait', { selector: update, textIncludes: 'Update attn?', timeoutMs: 15_000 });
    await driver.click(update);
    await driver.click('.notification-row-action', QUITS_THE_APP);
    build = await expectUpdateFinished(build, app, 'notification');
  });

  console.log(JSON.stringify(await runner.finishSuccess({}), null, 2));
} catch (error) {
  console.error((await runner.finishFailure(error, {})).error);
  process.exitCode = 1;
}
