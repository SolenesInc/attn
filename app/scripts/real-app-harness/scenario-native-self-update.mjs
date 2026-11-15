#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { launchFreshNativeAppAndConnect, parseCommonArgs } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, instanceCliEnv } from './harnessInstance.mjs';
import { appCliInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2).filter((arg) => arg !== '--'));
const instance = currentHarnessInstance();
if (!instance) throw new Error('The native self-update scenario requires a named, non-production instance');
const runner = createScenarioRunner(options, {
  scenarioId: 'NATIVE-SELF-UPDATE', tier: 'tier2-local-mock-agent', prefix: 'native-self-update',
  metadata: { focus: 'Update attn from native Settings and from the palette each builds, applies and relaunches a named instance, and the relaunched app\'s band reads the run done' },
});
const client = new UiAutomationClient(options);
const observer = new DaemonObserver({ wsUrl: options.wsUrl });
const driver = createWindowDriver({ appPath: options.appPath, client });
const attnBinary = appCliInTree(options.appPath);
const env = instanceCliEnv(instance);
const attn = (...args) => execFileSync(attnBinary, args, { encoding: 'utf8', env, stdio: ['ignore', 'pipe', 'ignore'] });
const status = () => JSON.parse(attn('update', 'status', '--json'));
const mac = process.platform === 'darwin';
const PALETTE = mac ? 'mod+shift+k' : 'mod+alt+k';

const UPDATE_TIMEOUT_MS = 120_000;
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
  await observer.connect();
}

async function expectUpdateFinished(previousBuild, previousApp, stepLabel) {
  await reconnectAfterRelaunch(previousApp);
  const done = await driver.waitFor({ key: 'update-run.countdown' }, { timeoutMs: UPDATE_TIMEOUT_MS });
  runner.assert(/Closing in/.test(done.name ?? ''), `${stepLabel}: the band counts down to closing once the run is done`, done);
  const text = await driver.find({ key: 'update-run.text' });
  runner.assert(/attn is now running/.test(text?.name ?? ''), `${stepLabel}: the band says what attn now runs`, text);
  const after = status();
  runner.assert(after.run?.phase === 'done' && after.run.target_build !== previousBuild, `${stepLabel}: the run is done on a new build`, JSON.stringify(after.run));
  await driver.screenshot(path.join(runner.runDir, `native-update-done-${stepLabel}.png`));
  return after.run.target_build;
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
    await launchFreshNativeAppAndConnect(client, observer);
    const on = observer.waitForMessage((m) => m.event === 'settings_updated' && m.settings?.self_update_enabled === 'true' && m, 'self-update on', 15_000);
    attn('settings', 'set', 'self_update_enabled', 'true');
    await on;
  });

  let build = '';

  await runner.step('update_from_settings_reaches_the_relaunched_app', async () => {
    const app = client.readManifest().pid;
    await driver.run('ui.openSettings');
    await driver.waitFor({ key: 'settings-dialog' });
    await driver.click({ key: 'settings.nav.updates' });
    await driver.click({ key: 'settings.updates.start' });
    build = await expectUpdateFinished(build, app, 'settings');
  });

  await runner.step('update_from_the_command_palette', async () => {
    const app = client.readManifest().pid;
    await driver.pressKey(PALETTE);
    await driver.waitFor({ key: 'palette.dialog' });
    await driver.typeInto(null, 'Update attn');
    await driver.waitFor({ key: 'palette.row.command:update-attn' });
    await driver.pressKey('Enter');
    build = await expectUpdateFinished(build, app, 'palette');
  });

  await runner.finishSuccess({});
} catch (error) {
  await driver.screenshot(path.join(runner.runDir, 'failure.png')).catch(() => {});
  console.error((await runner.finishFailure(error, {})).error);
  process.exitCode = 1;
}
