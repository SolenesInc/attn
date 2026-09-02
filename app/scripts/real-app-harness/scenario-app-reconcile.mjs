#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  DEFAULT_REMOTE_SSH_TARGET,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import {
  currentHarnessProfile,
  dataDirForProfile,
  profileCliEnv as profileEnv,
  resolveHarnessResources,
} from './harnessProfile.mjs';
import { runSSH } from './scenarioRemote.mjs';
import { sleep } from './scenarioAssertions.mjs';
import {
  historianManifest,
  stewardManifest,
  stewardEntrypoint,
  historianEntrypoint,
  STEWARD_V1_DERIVE,
  STEWARD_V2_DERIVE,
} from './appFixtures.mjs';
import { appDaemonInTree } from './platform.mjs';

// The shipped default is thirty days: without moving it a trim over a fresh
// profile removes nothing, and the gap leg has nothing to stand on.
const TRIM_WINDOW = '1s';
const STALL_WINDOW = '25s';
const DISPATCH_TIMEOUT = '5s';

const SETTLE_MS = 20_000;
// Absence needs a budget: a rebuild that is going to run has always started
// within a second of the trigger in this scenario's own runs.
const ABSENCE_MS = 10_000;

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const skipRemote = args.includes('--skip-remote');
  // parseCommonArgs rejects flags it does not know, so consume this one first.
  const options = parseCommonArgs(args.filter((arg) => arg !== '--skip-remote'));
  return {
    options,
    help: args.includes('--help') || args.includes('-h'),
    skipRemote,
  };
}

function run(binary, args, env, cwd = undefined) {
  return execFileSync(binary, args, {
    encoding: 'utf8',
    env,
    cwd,
    stdio: ['ignore', 'pipe', 'pipe'],
    timeout: 120_000,
  });
}

// Every CLI command prints a routing banner first, and it is not JSON.
function parseJSON(output) {
  const start = output.search(/[[{]/);
  if (start < 0) throw new Error(`no JSON in CLI output: ${output.slice(0, 200)}`);
  return JSON.parse(output.slice(start));
}

// The CLI refuses on the daemon side by writing to stdout and exiting
// non-zero; a refusal is the assertion in two legs.
function runAllowingFailure(binary, args, env) {
  try {
    return { ok: true, output: run(binary, args, env) };
  } catch (error) {
    const stdout = error?.stdout ?? '';
    const stderr = error?.stderr ?? '';
    return { ok: false, output: `${stdout}${stderr}` || String(error?.message || error) };
  }
}

async function poll(fn, description, timeoutMs) {
  const started = Date.now();
  let last = null;
  while (Date.now() - started < timeoutMs) {
    last = await fn();
    if (last) return last;
    await sleep(250);
  }
  throw new Error(`timed out waiting for ${description}; last=${JSON.stringify(last)}`);
}

function writeApp(root, name, manifest, entrypoint) {
  const dir = path.join(root, name);
  fs.writeFileSync(path.join(dir, 'attn-app.toml'), manifest, 'utf8');
  fs.writeFileSync(path.join(dir, 'src', 'index.ts'), entrypoint, 'utf8');
  return dir;
}

async function main() {
  const { options, help, skipRemote } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-app-reconcile.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('reconcile scenario requires a named non-production profile (it restarts the profile daemon and trims its bus)');
  }
  const resources = resolveHarnessResources(profile);
  const binary = appDaemonInTree(resources.appPath);
  const dbPath = path.join(dataDirForProfile(profile), 'attn.db');

  const runner = createScenarioRunner(options, {
    scenarioId: 'APP-RECONCILE',
    tier: 'tier2-app-runtime',
    prefix: 'app-reconcile',
    metadata: {
      profile,
      trimWindow: TRIM_WINDOW,
      focus: 'a derived view is rebuilt when — and only when — facts stop being enough',
    },
  });

  // The app spawns a daemon only when none is running, so ensure this one
  // BEFORE the app launches.
  const defaultEnv = profileEnv(profile);
  const walkEnv = profileEnv(profile, {
    ATTN_BUS_RETENTION: TRIM_WINDOW,
    ATTN_APP_AUTO_DISABLE_STALL: STALL_WINDOW,
    ATTN_APP_DISPATCH_TIMEOUT: DISPATCH_TIMEOUT,
  });

  // An app's documents and version history survive `attn app remove`, so the
  // names are scoped to this run.
  const runSlug = runner.runId.replace(/[^a-z0-9]/gi, '').toLowerCase().slice(-10);
  const STEWARD = `steward-${runSlug}`;
  const HISTORIAN = `historian-${runSlug}`;

  const appsRoot = path.join(runner.sessionDir, 'apps');
  fs.mkdirSync(appsRoot, { recursive: true });
  const releaseTitle = `release the rebuild ${runSlug}`;

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  const evidence = {};
  let seedSeq = 0;

  runner.log('run context', {
    runDir: runner.runDir, sessionDir: runner.sessionDir, wsUrl: options.wsUrl, profile, dbPath,
  });

  const plantSeed = (title) => {
    const planted = run(binary, ['seed', 'plant', title, '-m', 'A fact for the reconcile proof.'], walkEnv);
    const id = /^\s*(s-[a-z0-9]{6})\b/m.exec(planted)?.[1];
    if (!id) throw new Error(`plant answered without a seed id:\n${planted}`);
    return id;
  };
  const publishSeed = (label) => {
    seedSeq += 1;
    return plantSeed(`${label} ${seedSeq} ${runSlug}`);
  };

  const appStatus = (name) => parseJSON(run(binary, ['app', 'status', name, '--json'], walkEnv));
  const consumerOf = (name) => appStatus(name).app.consumer;
  const documents = (name, collection) =>
    parseJSON(run(binary, ['doc', 'query', `app/${name}`, collection, '--json'], walkEnv));
  const invocations = (name) => appStatus(name).recent || [];
  const reconciles = (name) => invocations(name).filter((row) => row.kind === 'reconcile');
  const causedBy = (row, cause) => (row?.reconcile?.causes || []).includes(cause);

  const sqlite = (statement) =>
    execFileSync('sqlite3', [dbPath, statement], { encoding: 'utf8', timeout: 30_000 }).trim();
  const readOnlySqlite = (statement) =>
    execFileSync('sqlite3', [`file:${dbPath}?immutable=1`, statement], { encoding: 'utf8', timeout: 30_000 }).trim();

  const restartDaemon = (env) => {
    try { run(binary, ['daemon', 'stop'], env); } catch { }
    run(binary, ['daemon', 'ensure'], env);
  };

  // Cleanups run in reverse registration order.
  runner.registerCleanup('restore_default_daemon', () => restartDaemon(defaultEnv));
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('remove_apps', () => {
    for (const name of [STEWARD, HISTORIAN]) {
      try { run(binary, ['app', 'remove', name], walkEnv); } catch { }
    }
  });

  try {
    await runner.step('daemon:tripwires', { trim: TRIM_WINDOW, stall: STALL_WINDOW }, async () => {
      restartDaemon(walkEnv);
      await sleep(2000);
    });

    await runner.step('app:launch', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('scaffold:new', async () => {
      for (const name of [STEWARD, HISTORIAN]) {
        run(binary, ['app', 'new', path.join(appsRoot, name)], walkEnv);
      }
      const scaffoldDoc = fs.readFileSync(path.join(appsRoot, STEWARD, 'AGENTS.md'), 'utf8');
      runner.assert(
        scaffoldDoc.includes('## Rebuilding what you derive: reconcile'),
        'the scaffold brief an app author reads does not teach reconcile',
      );
      runner.assert(
        scaffoldDoc.includes('Re-enabling is **not** a rebuild'),
        'the scaffold brief does not say that re-enabling is not a rebuild',
      );
    });

    await runner.step('leg1:before', async () => {
      writeApp(appsRoot, STEWARD, stewardManifest({ name: STEWARD }), stewardEntrypoint(STEWARD_V1_DERIVE));
      run(binary, ['app', 'apply', path.join(appsRoot, STEWARD)], walkEnv);
      for (let i = 0; i < 3; i += 1) publishSeed('before');
      const docs = await poll(
        () => {
          const rows = documents(STEWARD, 'seeds');
          return rows.length >= 3 ? rows : null;
        },
        'steward to derive a document per seed',
        SETTLE_MS,
      );
      runner.assert(
        docs.every((row) => row.body.title && row.body.status === undefined),
        'version 1 wrote a `status` field it does not derive',
        docs[0],
      );
      runner.assert(reconciles(STEWARD).length === 0, 'a first install reconciled; installing is not a trigger');
      evidence.before = docs;
      runner.writeJson('leg1-before.json', docs);
    });

    await runner.step('leg2:resume-is-not-a-rebuild', async () => {
      run(binary, ['app', 'disable', STEWARD], walkEnv);
      const backlog = [publishSeed('backlog'), publishSeed('backlog')];
      const behind = await poll(
        () => {
          const consumer = consumerOf(STEWARD);
          return consumer.lag >= backlog.length ? consumer : null;
        },
        'the disabled consumer to fall behind by the facts it missed',
        SETTLE_MS,
      );
      runner.assert(behind.enabled === false, 'disable left the consumer enabled', behind);

      run(binary, ['app', 'enable', STEWARD], walkEnv);
      const caughtUp = await poll(
        () => {
          const rows = documents(STEWARD, 'seeds');
          return backlog.every((id) => rows.some((row) => row.id === id)) ? rows : null;
        },
        'the retained backlog to deliver after enable',
        SETTLE_MS,
      );
      await sleep(ABSENCE_MS);
      runner.assert(
        reconciles(STEWARD).length === 0,
        're-enabling triggered a rebuild; a retained backlog is delivered, not recomputed',
        reconciles(STEWARD),
      );
      const delivered = invocations(STEWARD)
        .filter((row) => row.kind === 'subscription')
        .map((row) => row.work);
      runner.writeJson('leg2-backlog.json', { backlog, delivered, documents: caughtUp.length });
    });

    await runner.step('leg3:version-move-rebuilds', async () => {
      const beforeRevs = new Map(documents(STEWARD, 'seeds').map((row) => [row.id, row.rev]));
      writeApp(appsRoot, STEWARD, stewardManifest({ name: STEWARD }), stewardEntrypoint(STEWARD_V2_DERIVE));
      run(binary, ['app', 'apply', path.join(appsRoot, STEWARD)], walkEnv);

      const rebuilt = await poll(
        () => {
          const rows = documents(STEWARD, 'seeds');
          return rows.length > 0 && rows.every((row) => row.body.status !== undefined) ? rows : null;
        },
        'every document to carry the field the new version derives',
        SETTLE_MS,
      );
      runner.assert(
        rebuilt.every((row) => beforeRevs.get(row.id) === undefined || row.rev > beforeRevs.get(row.id)),
        'documents carry the new field without having been rewritten',
      );
      // The documents can be right while the invocation row still says
      // `running`: the handler returns before the daemon writes its outcome.
      const rebuild = await poll(
        () => reconciles(STEWARD).find((row) => row.status !== 'running') || null,
        'the rebuild invocation to reach an outcome',
        SETTLE_MS,
      );
      runner.assert(
        rebuild.status === 'ok' && causedBy(rebuild, 'version_changed'),
        `the rebuild was not attributed to the version move: ${JSON.stringify(rebuild)}`,
      );
      const settled = await poll(
        () => (appStatus(STEWARD).reconcile?.state === 'idle' ? appStatus(STEWARD).reconcile : null),
        'the app to stop owing a rebuild after a successful one',
        SETTLE_MS,
      );
      runner.log('rebuild settled', settled);
      evidence.rebuilt = rebuilt;
      runner.writeJson('leg3-rebuilt.json', { rebuild, documents: rebuilt });
    });

    await runner.step('leg4:no-handler-no-move', async () => {
      writeApp(appsRoot, HISTORIAN, historianManifest({ name: HISTORIAN }), historianEntrypoint());
      run(binary, ['app', 'apply', path.join(appsRoot, HISTORIAN)], walkEnv);
      publishSeed('historian-sees');
      await poll(
        () => (documents(HISTORIAN, 'seen').length > 0 ? true : null),
        'historian to record a fact',
        SETTLE_MS,
      );

      const moved = runAllowingFailure(
        binary,
        ['app', 'apply', path.join(appsRoot, HISTORIAN)],
        walkEnv,
      );
      // A second apply of a byte-identical version is not a move at all.
      writeApp(
        appsRoot,
        HISTORIAN,
        historianManifest({ name: HISTORIAN, description: 'Accumulates what it is told. Second version.' }),
        historianEntrypoint(),
      );
      const refused = runAllowingFailure(
        binary,
        ['app', 'apply', path.join(appsRoot, HISTORIAN)],
        walkEnv,
      );
      runner.assert(!refused.ok || /refusing to move/.test(refused.output), 'a subscribed app with no reconcile was moved anyway', refused);
      runner.assert(
        /does not declare reconcile/.test(refused.output),
        `the refusal does not say what is missing: ${refused.output}`,
      );
      runner.writeJson('leg4-refusal.json', { unchangedApply: moved.output, refusal: refused.output });
      writeApp(appsRoot, HISTORIAN, historianManifest({ name: HISTORIAN }), historianEntrypoint());
    });

    await runner.step('leg5:gap-disables-loudly', async () => {
      const cursor = Number(consumerOf(HISTORIAN).cursor);
      runner.assert(Number.isInteger(cursor) && cursor > 0, `historian has no cursor to be trimmed past: ${cursor}`);

      run(binary, ['app', 'remove', HISTORIAN], walkEnv);
      for (let i = 0; i < 3; i += 1) publishSeed('past-the-cursor');
      await sleep(2000);

      const trimmed = run(binary, ['bus', 'trim'], walkEnv);
      runner.assert(/removed \d+ event/.test(trimmed), `the trim removed nothing: ${trimmed}`);
      for (let i = 0; i < 2; i += 1) publishSeed('after-the-trim');
      await sleep(2000);
      const earliest = Number(readOnlySqlite('select coalesce(min(seq), 0) from bus_events;'));
      runner.assert(
        earliest > cursor,
        `the trim did not move the oldest surviving fact past historian's cursor (earliest ${earliest}, cursor ${cursor})`,
      );

      // The one hand-made row: the cursor a removed install left behind. The
      // daemon is down for it so nothing holds the row in memory.
      run(binary, ['daemon', 'stop'], walkEnv);
      sqlite(
        'insert into bus_consumers (name, cursor, filter, enabled, updated_at) values ('
        + `'app:${HISTORIAN}', ${cursor}, 'garden.*', 1, '${new Date().toISOString()}');`,
      );
      run(binary, ['daemon', 'ensure'], walkEnv);
      await sleep(2000);

      run(binary, ['app', 'apply', path.join(appsRoot, HISTORIAN)], walkEnv);
      const disabled = await poll(
        () => {
          const consumer = consumerOf(HISTORIAN);
          return consumer.enabled === false ? consumer : null;
        },
        'the gap to disable an app that cannot rebuild',
        SETTLE_MS,
      );
      runner.assert(
        Number(disabled.cursor) === cursor,
        `the disable moved the cursor from ${cursor} to ${disabled.cursor}; a gap must not be skipped past`,
      );
      const refusal = reconciles(HISTORIAN).at(0);
      runner.assert(Boolean(refusal), 'the gap produced no invocation row to read afterwards');
      runner.assert(
        refusal.handler === 'missing_reconcile' && refusal.status === 'error',
        `the gap was not recorded as a missing handler: ${JSON.stringify(refusal)}`,
      );
      runner.assert(causedBy(refusal, 'gap'), `the invocation is not attributed to a gap: ${JSON.stringify(refusal.reconcile)}`);
      runner.assert(
        Number(refusal.reconcile?.gap?.earliest) > Number(refusal.reconcile?.gap?.cursor),
        `the gap does not describe facts the app can never receive: ${JSON.stringify(refusal.reconcile?.gap)}`,
      );
      runner.assert(
        appStatus(HISTORIAN).reconcile?.state === 'unsupported',
        'the app that cannot rebuild does not say so on its status surface',
        appStatus(HISTORIAN).reconcile,
      );

      const notification = readOnlySqlite(
        "select body from notifications where kind = 'app_auto_disabled' order by rowid desc limit 1;",
      );
      runner.assert(
        notification.includes(HISTORIAN) && notification.includes('reconcile'),
        `nothing told the user why historian went quiet: ${notification}`,
      );
      evidence.gap = { cursor, earliest, refusal, notification };
      runner.writeJson('leg5-gap.json', evidence.gap);
    });

    await runner.step('leg6:interrupted-rebuild-repairs', async () => {
      writeApp(
        appsRoot,
        STEWARD,
        stewardManifest({ name: STEWARD }),
        stewardEntrypoint(STEWARD_V2_DERIVE, { blockUntilSeed: releaseTitle }),
      );
      run(binary, ['app', 'apply', path.join(appsRoot, STEWARD)], walkEnv);
      const running = await poll(
        () => {
          const state = appStatus(STEWARD).reconcile?.state;
          return state === 'running' || state === 'owed' ? state : null;
        },
        'the rebuild to be in flight',
        SETTLE_MS,
      );
      runner.log('rebuild in flight', { state: running });

      restartDaemon(walkEnv);
      await sleep(2000);
      const owedAfterRestart = await poll(
        () => {
          const state = appStatus(STEWARD).reconcile?.state;
          return state === 'owed' || state === 'running' ? state : null;
        },
        'the interrupted rebuild to still be owed after the restart',
        SETTLE_MS,
      );
      runner.assert(
        owedAfterRestart === 'owed' || owedAfterRestart === 'running',
        `an interrupted rebuild was forgotten across the restart: ${owedAfterRestart}`,
      );

      plantSeed(releaseTitle);
      const settled = await poll(
        () => (appStatus(STEWARD).reconcile?.state === 'idle' ? appStatus(STEWARD) : null),
        'the repaired rebuild to finish',
        60_000,
      );
      const attempts = reconciles(STEWARD);
      const interrupted = attempts.find((row) => row.status === 'interrupted');
      runner.assert(
        Boolean(interrupted),
        'no rebuild was interrupted; the restart raced a rebuild that had already finished',
        attempts,
      );
      const repaired = attempts.find(
        (row) => row.status === 'ok' && row.through_request_id === interrupted?.through_request_id,
      );
      runner.assert(
        Boolean(repaired),
        'the interrupted rebuild was never repaired; nothing completed its request',
        attempts,
      );
      evidence.repaired = { state: settled.reconcile, interrupted, repaired };
      runner.writeJson('leg6-repaired.json', { state: settled.reconcile, invocations: attempts });
    });

    await runner.step('leg7:converges-before-later-facts', async () => {
      const id = publishSeed('after-everything');
      const doc = await poll(
        () => {
          const rows = documents(STEWARD, 'seeds');
          return rows.find((row) => row.id === id) || null;
        },
        'a fact published after every leg to be handled',
        SETTLE_MS,
      );
      runner.assert(
        doc.body.status !== undefined,
        'a fact published after the rebuild was handled by the version that no longer serves',
        doc,
      );
      const status = await poll(
        () => {
          const candidate = appStatus(STEWARD);
          return candidate.app.consumer.lag === 0 && candidate.reconcile.state === 'idle'
            ? candidate
            : null;
        },
        `${STEWARD} to consume the published fact and finish reconciling`,
        SETTLE_MS,
      );
      evidence.converged = { id, doc, consumer: status.app.consumer };
      runner.writeJson('leg7-converged.json', evidence.converged);
    });

    await runner.step('visible:status-surfaces', async () => {
      runner.writeText('app-status-steward.txt', run(binary, ['app', 'status', STEWARD], walkEnv));
      runner.writeText('app-status-historian.txt', run(binary, ['app', 'status', HISTORIAN], walkEnv));
      runner.writeText('bus-status.txt', run(binary, ['bus', 'status'], walkEnv));
      const historian = run(binary, ['app', 'status', HISTORIAN], walkEnv);
      runner.assert(
        /reconcile:\s+unsupported/.test(historian),
        'the human-readable status does not say the app cannot rebuild',
      );
      await client.request('capture_native_window_screenshot', {
        path: path.join(runner.runDir, 'app-window.png'),
      }).catch((error) => runner.log('ui:capture_failed', { error: String(error) }));
    });

    if (!skipRemote) {
      await runner.step('linux:reconcile-witness', async () => {
        const target = process.env.ATTN_HARNESS_REMOTE_SSH_TARGET || DEFAULT_REMOTE_SSH_TARGET;
        // `attn app apply` bundles with bun and a non-interactive ssh shell
        // has not run .bashrc, so ~/.bun/bin goes on PATH explicitly.
        const remoteAttn = process.env.ATTN_HARNESS_REMOTE_ATTN || 'attn';
        const remoteProfile = process.env.ATTN_HARNESS_REMOTE_PROFILE || '';
        const prefix = `export PATH="$HOME/.bun/bin:$HOME/.local/bin:$PATH"; `
          + (remoteProfile ? `export ATTN_PROFILE=${remoteProfile}; ` : '');
        const remote = (command, timeoutMs = 120_000) => runSSH(target, prefix + command, timeoutMs);

        const reachable = await remote(`command -v ${remoteAttn} && echo READY`, 20_000).catch(() => null);
        if (!reachable || !String(reachable).includes('READY')) {
          runner.log('linux:unavailable', { target, remoteAttn });
          evidence.linux = { target, skipped: 'no attn on the remote' };
          return;
        }

        const witnessName = `linuxwitness-${runSlug}`;
        const remoteRoot = `/tmp/attn-reconcile-${runSlug}`;
        await remote(`${remoteAttn} daemon ensure`, 120_000);
        await remote(`mkdir -p ${remoteRoot}`);
        await remote(`${remoteAttn} app new ${remoteRoot}/${witnessName}`, 300_000);
        const writeRemote = (file, body) =>
          remote(`cat > ${remoteRoot}/${witnessName}/${file} <<'ATTNEOF'\n${body}\nATTNEOF`);
        await writeRemote('attn-app.toml', stewardManifest({ name: witnessName }));
        await writeRemote('src/index.ts', stewardEntrypoint(STEWARD_V1_DERIVE));
        await remote(`${remoteAttn} app apply ${remoteRoot}/${witnessName}`, 300_000);

        const remoteStatus = async () => parseJSON(await remote(`${remoteAttn} app status ${witnessName} --json`, 60_000));
        await remote(`${remoteAttn} seed plant 'linux witness ${runSlug}' -m 'A fact for the reconcile proof.'`, 60_000);
        const dispatched = await poll(
          async () => {
            const status = await remoteStatus().catch(() => null);
            return (status?.recent || []).some((row) => row.kind === 'subscription' && row.status === 'ok')
              ? status
              : null;
          },
          'the Linux daemon to dispatch a fact into the app runtime',
          90_000,
        );

        await writeRemote('src/index.ts', stewardEntrypoint(STEWARD_V2_DERIVE));
        await remote(`${remoteAttn} app apply ${remoteRoot}/${witnessName}`, 300_000);
        const rebuilt = await poll(
          async () => {
            const status = await remoteStatus().catch(() => null);
            const row = (status?.recent || []).find((r) => r.kind === 'reconcile' && r.status !== 'running');
            return row ? { status, row } : null;
          },
          'the Linux daemon to rebuild the app across a version move',
          90_000,
        );
        runner.assert(
          rebuilt.row.status === 'ok' && causedBy(rebuilt.row, 'version_changed'),
          `the Linux rebuild did not complete on the version move: ${JSON.stringify(rebuilt.row)}`,
        );
        runner.assert(
          rebuilt.status.reconcile?.state === 'idle',
          `the Linux app still owes a rebuild: ${JSON.stringify(rebuilt.status.reconcile)}`,
        );

        evidence.linux = {
          target,
          remoteAttn,
          dispatched: dispatched.recent[0],
          rebuild: rebuilt.row,
        };
        runner.writeJson('linux-witness.json', evidence.linux);
        await remote(`${remoteAttn} app remove ${witnessName} || true`, 60_000).catch(() => {});
        await remote(`rm -rf ${remoteRoot}`).catch(() => {});
      });
    }

    const result = await runner.finishSuccess({
      profile,
      trimWindow: TRIM_WINDOW,
      gap: evidence.gap,
      linux: evidence.linux ?? null,
    });
    console.log('[verify] PASS — rebuilt on a version move and a gap, delivered on a resume, repaired after an interruption.');
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    try {
      runner.writeText('app-status-steward.txt', runAllowingFailure(binary, ['app', 'status', STEWARD], walkEnv).output);
      runner.writeText('app-status-historian.txt', runAllowingFailure(binary, ['app', 'status', HISTORIAN], walkEnv).output);
      runner.writeText('bus-status.txt', runAllowingFailure(binary, ['bus', 'status'], walkEnv).output);
    } catch { }
    await client.request('capture_native_window_screenshot', {
      path: path.join(runner.runDir, 'failure.png'),
    }).catch(() => {});
    const result = await runner.finishFailure(error, { profile });
    console.error(result.error);
    process.exitCode = 1;
  } finally {
    for (const name of [STEWARD, HISTORIAN]) {
      try { run(binary, ['app', 'remove', name], walkEnv); } catch { }
    }
    await client.quitApp().catch(() => {});
    await observer.close();
    restartDaemon(defaultEnv);
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
