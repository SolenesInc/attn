#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { parseCommonArgs, queueDaemonSettingRestore, writeDaemonSettings } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessInstance, dataDirForInstance } from './harnessInstance.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const options = parseCommonArgs(process.argv.slice(2));
const instance = currentHarnessInstance();
if (!instance) throw new Error('Daemon fallback recovery verification requires a named instance');
const dataDir = dataDirForInstance(instance);
const pidPath = path.join(dataDir, 'attn.pid');
const stopNotePath = path.join(dataDir, 'daemon-stop.json');
const ensureLockPath = `${pidPath}.ensure`;
const runner = createScenarioRunner(options, {
  scenarioId: 'DAEMON-FALLBACK-RECOVERY', tier: 'local', prefix: 'daemon-fallback-recovery',
  metadata: { focus: 'A daemon the app stops after a failed ensure reads as stopped by app_recovery, not as a crash.' },
});
const client = new UiAutomationClient(options);

const readIfPresent = file => {
  try {
    return fs.readFileSync(file, 'utf8').trim();
  } catch {
    return '';
  }
};

async function waitFor(description, read, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const value = await read();
    if (value) return value;
    await new Promise(resolve => setTimeout(resolve, 20));
  }
  throw new Error(`Timed out after ${timeoutMs}ms waiting for ${description}`);
}

async function launchApp() {
  await client.launchFreshApp();
  await client.waitForManifest(20_000);
  await client.waitForReady(20_000);
}

async function withObserver(work) {
  const observer = new DaemonObserver(options);
  await observer.connect();
  try {
    return await work(observer);
  } finally {
    await observer.close();
  }
}

const daemonStarts = observer => observer
  .requestResult({ cmd: 'list_analytics_records', limit: 1000, stream: 'sample' }, 'list_analytics_records_result')
  .then(result => (result.records ?? []).map(row => JSON.parse(row.record)).filter(record => record.name === 'daemon_start'));

function makeEnsureFail() {
  fs.rmSync(ensureLockPath, { force: true });
  fs.mkdirSync(ensureLockPath);
}

const letEnsureSucceed = () => fs.rmSync(ensureLockPath, { recursive: true, force: true });

async function recoverAndCheck(step, prepare = () => {}) {
  await runner.step(step, async () => {
    const before = await withObserver(daemonStarts);
    const stoppedPid = readIfPresent(pidPath);
    await client.quitApp();
    prepare();
    makeEnsureFail();
    const replaced = () => {
      const pid = readIfPresent(pidPath);
      return pid && pid !== stoppedPid;
    };
    const fallbackStarted = waitFor('the app to fall back after the failed ensure', () => {
      if (!readIfPresent(stopNotePath).includes('app_recovery') && !replaced()) return false;
      letEnsureSucceed();
      return true;
    }, 30_000);
    await launchApp();
    await fallbackStarted;
    await waitFor('the app to replace the daemon', replaced, 30_000);
    const starts = await withObserver(observer => waitFor('the replacement run to record daemon_start', async () => {
      const current = await daemonStarts(observer);
      return current[0]?.subject !== before[0]?.subject ? current : null;
    }, 20_000));
    runner.writeJson(`${step}-daemon-starts.json`, starts);
    const { props } = starts[0];
    runner.assert(
      props.previous_end === 'stopped' && props.stop_reason === 'app_recovery',
      `${step}: the next daemon_start says the app stopped the previous run`,
      { props },
    );
  });
}

runner.registerCleanup('let_ensure_succeed', letEnsureSucceed);
runner.registerCleanup('quit_app', () => client.quitApp());

try {
  await runner.step('record_analytics', async () => {
    await launchApp();
    await withObserver(async observer => queueDaemonSettingRestore(observer, 'analytics.enabled'));
    await writeDaemonSettings([{ key: 'analytics.enabled', value: 'true' }]);
  });
  await recoverAndCheck('stop_the_pid_file_daemon');
  await recoverAndCheck('stop_the_port_scan_daemon', () => fs.rmSync(pidPath));
  await runner.finishSuccess();
} catch (error) {
  await runner.finishFailure(error);
  process.exitCode = 1;
}
