#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import readline from 'node:readline';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createProvider, quote, run, sshArgs } from './vm/providers.mjs';
import { syncSource } from './vm/source.mjs';
import { resolveScenario, scenarioApp } from './scenarioCatalog.mjs';

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const checkoutHash = createHash('sha256').update(fs.realpathSync(repo)).digest('hex').slice(0, 10);

export function parseArgs(argv, env = process.env) {
  const options = { provider: env.ATTN_LINUX_PROVIDER || 'lima', name: env.ATTN_LINUX_VM || 'attn-linux',
    target: env.ATTN_LINUX_SSH_TARGET, sshConfig: env.ATTN_LINUX_SSH_CONFIG, checkout: checkoutHash };
  const values = { '--provider': 'provider', '--name': 'name', '--target': 'target', '--ssh-config': 'sshConfig',
    '--checkout': 'checkout', '--instance': 'instance', '--out': 'out', '--scenario': 'scenario', '--turn-limit': 'turnLimit' };
  const positional = [];
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === '--') { options.commandArgs = argv.slice(i + 1); break; }
    if (arg === '--help' || arg === '-h') { options.help = true; continue; }
    if (arg === '--native-wire') { options.nativeWire = true; continue; }
    if (values[arg]) {
      const value = argv[++i];
      if (!value || value.startsWith('--')) throw new Error(`${arg} needs a value`);
      options[values[arg]] = value;
    } else if (arg.startsWith('-')) throw new Error(`Unknown option: ${arg}`);
    else positional.push(arg);
  }
  if (positional.length > 1) throw new Error('Put guest command arguments after --');
  options.action = positional[0] || 'status';
  if (!/^[a-z0-9][a-z0-9-]{0,9}$/.test(options.checkout)) throw new Error('--checkout must be at most 10 lowercase letters, digits and dashes');
  if (options.turnLimit !== undefined && !/^(0|[1-9][0-9]*)$/.test(options.turnLimit)) throw new Error('--turn-limit must be a whole number of seconds');
  options.instance ??= `linux-${options.checkout}`;
  if (!/^[a-z][a-z0-9-]{0,39}$/.test(options.instance) || ['default', 'prod', 'production'].includes(options.instance)) {
    throw new Error('--instance must name a non-production instance');
  }
  return options;
}

export function guestCommand(root, instance, argv) {
  if (!argv?.length) throw new Error('run needs a command after --');
  return `set -euo pipefail
root=${quote(root)}
[[ -f "$root/managed-source" ]] || { echo 'Run sync first' >&2; exit 1; }
exec 9>"$root/operation.lock"
flock -n 9 || { echo 'Runner is busy; wait for its active command' >&2; exit 75; }
cd "$root/source"
mkdir -p "$root/artifacts" "$root/bin"
install -m 0755 app/scripts/real-app-harness/ci-xdg-open "$root/bin/xdg-open"
env -i HOME="$HOME" USER="$(id -un)" LANG=C.UTF-8 \
  PATH="$root/bin:$HOME/.local/bin:$HOME/.bun/bin:$HOME/.cargo/bin:/usr/local/bin:/usr/bin:/bin" \
  ATTN_INSTANCE=${quote(instance)} ATTN_HARNESS_INSTANCE=${quote(instance)} CI=true \
  ATTN_REAL_APP_ARTIFACTS_DIR="$root/artifacts" \
  ${argv.map(quote).join(' ')} 9>&-
`;
}

function guestRoot(provider, checkout) {
  const home = provider.ssh('printf %s "$HOME"', { stdio: ['ignore', 'pipe', 'inherit'] }).trim();
  if (!home.startsWith('/')) throw new Error('Guest did not report an absolute home directory');
  return path.posix.join(home, '.local/share/attn-linux-runner', checkout);
}

async function takeTurn(provider, root, limit) {
  const turn = `${root}.turn`;
  const checkout = path.posix.basename(root);
  const wait = limit ? `flock -w ${limit} 8 || { echo gave-up; exit; }` : 'flock 8';
  const script = `set -e
mkdir -p ${quote(root)}
exec 8>${quote(turn)}
flock -n 8 || { echo busy; ${wait}; }
flock ${quote(`${root}/operation.lock`)} true
echo took
exec cat >/dev/null`;
  const holder = spawn('ssh', sshArgs(provider.connection(), `bash -c ${quote(script)}`), { stdio: ['pipe', 'pipe', 'inherit'] });
  const asked = Date.now();
  for await (const line of readline.createInterface({ input: holder.stdout })) {
    if (line === 'busy') console.error(`Waiting for another run on the guest checkout ${checkout}`);
    if (line === 'gave-up') {
      throw Object.assign(new Error(`Gave up after ${Math.round((Date.now() - asked) / 1000)}s waiting for the guest checkout ${checkout}`), { exitCode: 75 });
    }
    if (line === 'took') {
      console.error(`Waited ${Math.round((Date.now() - asked) / 1000)}s for the guest checkout ${checkout}`);
      return () => holder.stdin.end();
    }
  }
  throw new Error(`Could not take a turn on the guest checkout ${checkout}`);
}

function collect(provider, root, destination) {
  fs.mkdirSync(destination, { recursive: true });
  const archive = path.join(destination, 'artifacts.tgz');
  const fd = fs.openSync(archive, 'w');
  try {
    provider.ssh(`tar -czf - -C ${quote(root)} artifacts`, { stdio: ['ignore', fd, 'inherit'] });
  } finally { fs.closeSync(fd); }
  run('tar', ['-xzf', archive, '-C', destination]);
  console.log(`Artifacts: ${destination}`);
}

export async function main(argv = process.argv.slice(2)) {
  const options = parseArgs(argv);
  if (options.help) {
    console.log(`Usage: pnpm --dir app real-app:linux <action> [options] [-- command args...]

Actions:
  up          Create/start VM (Lima or OrbStack); check SSH
  status      Inspect VM or SSH machine
  stop        Stop managed VM; preserves disk and caches
  connection  Print provider/SSH connection as JSON
  sync        Copy HEAD plus staged, unstaged and non-ignored new files
  provision   Sync and install Ubuntu runner toolchains and sandbox
  run         Run a command in the synced Linux checkout; preserve its exit code
  build       Sync and install the packaged app in the named guest instance
  test        Sync, reinstall a fresh guest instance and run the serial app matrix under Xvfb;
              collect artifacts even on failure
  soak        Sync, build each app the --scenario list needs once, then run each scenario once under
              Xvfb in a freshly installed guest instance, as CI's Acceptance soak does; print a
              "Scenario <id> passed|failed in <n>s" line per scenario; collect artifacts even on failure.
              With --native-wire, first run the native wire suite (make test-native-wire) and print
              "Native wire tests passed|failed in <n>s"; a failure stops the run before the scenarios
  artifacts   Copy the guest artifacts directory to --out
  clean       Remove the installed guest instance using its own CLI

Options:
  --provider lima|orb|ssh     Default: ATTN_LINUX_PROVIDER or lima
  --name <name>              Default: ATTN_LINUX_VM or attn-linux
  --target user@host         Required for ssh; ATTN_LINUX_SSH_TARGET
  --ssh-config <file>        Optional SSH config for ssh; ATTN_LINUX_SSH_CONFIG
  --checkout <name>          Guest checkout to sync into. Default: a hash of this checkout's path
  --instance <name>           Default: linux-<checkout>; never production
  --out <directory>          Local artifacts destination
  --scenario <id>[,<id>...]  The scenarios soak runs, in order
  --native-wire              soak: run the native wire suite before the scenarios
  --turn-limit <seconds>     test and soak: wait at most this long for another run on the guest
                             checkout to finish, then exit 75. Default: wait until it finishes

Examples:
  pnpm --dir app real-app:linux provision
  pnpm --dir app real-app:linux test -- --scenario quick-capture
  pnpm --dir app real-app:linux provision --provider ssh --target tester@linux
  pnpm --dir app real-app:linux soak --provider ssh --target tester@linux --scenario linux-shortcuts,shelf-moves
`);
    return;
  }
  const provider = createProvider(options);
  if (['status', 'stop'].includes(options.action)) return provider[options.action]();
  if (options.action === 'connection') return console.log(JSON.stringify(provider.describe(), null, 2));
  if (options.action === 'up') {
    provider.up();
    return provider.ssh('uname -srmo');
  }
  if (!['sync', 'provision', 'run', 'build', 'test', 'soak', 'artifacts', 'clean'].includes(options.action)) {
    throw new Error(`Unknown action: ${options.action}`);
  }
  if (options.action === 'soak') {
    if (!options.scenario && !options.nativeWire) throw new Error('soak needs --scenario, --native-wire or both');
    soakScenarios(options.scenario);
  }
  if (options.action === 'provision') {
    provider.up();
    provider.ssh('bash -s', {
      input: fs.readFileSync(path.join(repo, 'app/scripts/real-app-harness/vm/bootstrap.sh')),
      stdio: ['pipe', 'inherit', 'inherit'],
    });
  }
  const root = guestRoot(provider, options.checkout);
  const destination = path.resolve(options.out || path.join(os.tmpdir(), 'attn-linux-artifacts', options.provider, options.checkout));
  const execute = (args) => provider.ssh(`bash -c ${quote(guestCommand(root, options.instance, args))}`);
  if (['test', 'soak'].includes(options.action)) {
    const release = await takeTurn(provider, root, options.turnLimit);
    try { return runScenarios(options, provider, root, destination, execute); }
    finally { release(); }
  }
  if (['sync', 'provision', 'build'].includes(options.action)) syncSource(provider, repo, root);
  if (options.action === 'sync') return;
  if (options.action === 'provision') {
    return execute(['bash', 'app/scripts/real-app-harness/vm/provision.sh']);
  }
  if (options.action === 'build') {
    buildApp(execute, options.instance, 'tauri');
    return installApp(execute, options.instance, 'tauri');
  }
  if (options.action === 'run') return execute(options.commandArgs);
  if (options.action === 'clean') return execute(['./attn', 'instance', 'clean', options.instance]);
  if (options.action === 'artifacts') return collect(provider, root, destination);
}

function buildApp(execute, instance, app) {
  execute(['make', 'build-app', `INSTANCE=${instance}`, `APP=${app}`]);
}

function installApp(execute, instance, app) {
  execute(['make', 'install-staged', `INSTANCE=${instance}`, `APP=${app}`]);
}

function installFresh(execute, instance, app) {
  execute(['./attn', 'instance', 'clean', instance, '--keep-app']);
  installApp(execute, instance, app);
}

function underXvfb(args) {
  // Window probes must not reset Xvfb while GTK connects during app startup.
  return ['xvfb-run', '-a', '-s', '-noreset -screen 0 1600x1000x24', 'pnpm', '--dir', 'app', 'run', ...args];
}

function runMatrix(options, execute) {
  buildApp(execute, options.instance, 'tauri');
  installFresh(execute, options.instance, 'tauri');
  execute(underXvfb(['real-app:serial-matrix', ...(options.commandArgs || [])]));
}

function runNativeWire(execute) {
  const started = Date.now();
  let verdict = 'passed';
  try { execute(['make', 'test-native-wire']); } catch { verdict = 'failed'; }
  console.log(`Native wire tests ${verdict} in ${Math.round((Date.now() - started) / 1000)}s`);
  if (verdict === 'failed') throw Object.assign(new Error('The native wire suite failed'), { exitCode: 1 });
}

function runSoak(options, execute) {
  const scenarios = soakScenarios(options.scenario);
  if (options.nativeWire) runNativeWire(execute);
  for (const app of new Set(scenarios.map(scenarioApp))) buildApp(execute, options.instance, app);
  const failed = [];
  for (const scenario of scenarios) {
    const started = Date.now();
    let verdict = 'passed';
    try {
      installFresh(execute, options.instance, scenarioApp(scenario));
      execute(underXvfb(['real-app:soak', '--', '--scenario', scenario.id, '--repeat', '1', '--failed-evidence-only']));
    } catch {
      verdict = 'failed';
      failed.push(scenario.id);
    }
    console.log(`Scenario ${scenario.id} ${verdict} in ${Math.round((Date.now() - started) / 1000)}s`);
  }
  if (failed.length) throw Object.assign(new Error(`Scenarios failed: ${failed.join(', ')}`), { exitCode: 1 });
}

function soakScenarios(list) {
  return list ? list.split(',').map((id) => resolveScenario(id)) : [];
}

function runScenarios(options, provider, root, destination, execute) {
  syncSource(provider, repo, root);
  let testError;
  try {
    execute(['make', 'app/node_modules/.attn-installed']);
    if (options.action === 'soak') runSoak(options, execute);
    else runMatrix(options, execute);
  } catch (error) { testError = error; }
  try { execute(['scripts/native-build-clean']); }
  catch (error) { console.error(`Pruning native build output failed: ${error.message}`); }
  try { collect(provider, root, destination); }
  catch (error) { if (!testError) throw error; console.error(`Artifact collection failed: ${error.message}`); }
  if (testError) throw testError;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => { console.error(error.message); process.exitCode = error.exitCode || 1; });
}
