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
    '--checkout': 'checkout', '--instance': 'instance', '--out': 'out', '--scenario': 'scenario' };
  const positional = [];
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === '--') { options.commandArgs = argv.slice(i + 1); break; }
    if (arg === '--help' || arg === '-h') { options.help = true; continue; }
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

async function takeTurn(provider, root) {
  const turn = `${root}.turn`;
  const script = `set -e
mkdir -p ${quote(root)}
exec 8>${quote(turn)}
flock -n 8 || { echo busy; flock 8; }
flock ${quote(`${root}/operation.lock`)} true
echo took
exec cat >/dev/null`;
  const holder = spawn('ssh', sshArgs(provider.connection(), `bash -c ${quote(script)}`), { stdio: ['pipe', 'pipe', 'inherit'] });
  const asked = Date.now();
  for await (const line of readline.createInterface({ input: holder.stdout })) {
    if (line === 'busy') console.error(`Waiting for another run on the guest checkout ${path.posix.basename(root)}`);
    if (line === 'took') {
      console.error(`Waited ${Math.round((Date.now() - asked) / 1000)}s for the guest checkout ${path.posix.basename(root)}`);
      return () => holder.stdin.end();
    }
  }
  throw new Error(`Could not take a turn on the guest checkout ${path.posix.basename(root)}`);
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
  soak        Sync, reinstall a fresh guest instance with the app --scenario needs and run that scenario
              once under Xvfb, as CI's Acceptance soak does; collect artifacts even on failure
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
  --scenario <id>            The scenario soak runs

Examples:
  pnpm --dir app real-app:linux provision
  pnpm --dir app real-app:linux test -- --scenario quick-capture
  pnpm --dir app real-app:linux provision --provider ssh --target tester@linux
  pnpm --dir app real-app:linux soak --provider ssh --target tester@linux --scenario linux-shortcuts
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
  if (options.action === 'soak') resolveScenario(options.scenario ?? '');
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
    const release = await takeTurn(provider, root);
    try { return runScenarios(options, provider, root, destination, execute); }
    finally { release(); }
  }
  if (['sync', 'provision', 'build'].includes(options.action)) syncSource(provider, repo, root);
  if (options.action === 'sync') return;
  if (options.action === 'provision') {
    return execute(['bash', 'app/scripts/real-app-harness/vm/provision.sh']);
  }
  if (options.action === 'build') return execute(['node', 'app/scripts/real-app-harness/vm/build.mjs', 'tauri']);
  if (options.action === 'run') return execute(options.commandArgs);
  if (options.action === 'clean') return execute(['./attn', 'instance', 'clean', options.instance]);
  if (options.action === 'artifacts') return collect(provider, root, destination);
}

function runScenarios(options, provider, root, destination, execute) {
  syncSource(provider, repo, root);
  execute(['bash', '-c', '[[ ! -x ./attn ]] || ./attn instance clean "$ATTN_INSTANCE"']);
  const scenarios = options.action === 'soak'
    ? ['real-app:soak', '--', '--scenario', options.scenario, '--repeat', '1', '--failed-evidence-only']
    : ['real-app:serial-matrix', ...(options.commandArgs || [])];
  execute(['node', 'app/scripts/real-app-harness/vm/build.mjs', options.action === 'soak' ? scenarioApp(resolveScenario(options.scenario)) : 'tauri']);
  let testError;
  try {
    // Window probes must not reset Xvfb while GTK connects during app startup.
    execute(['xvfb-run', '-a', '-s', '-noreset -screen 0 1600x1000x24', 'pnpm', '--dir', 'app', 'run', ...scenarios]);
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
