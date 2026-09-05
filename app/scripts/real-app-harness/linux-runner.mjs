#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { createHash } from 'node:crypto';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createProvider, quote, run } from './vm/providers.mjs';
import { syncSource } from './vm/source.mjs';

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const key = createHash('sha256').update(fs.realpathSync(repo)).digest('hex').slice(0, 10);

export function parseArgs(argv, env = process.env) {
  const options = { provider: env.ATTN_LINUX_PROVIDER || 'lima', name: env.ATTN_LINUX_VM || 'attn-linux',
    target: env.ATTN_LINUX_SSH_TARGET, sshConfig: env.ATTN_LINUX_SSH_CONFIG, profile: `linux-${key}` };
  const values = { '--provider': 'provider', '--name': 'name', '--target': 'target', '--ssh-config': 'sshConfig', '--profile': 'profile', '--out': 'out' };
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
  if (!/^[a-z][a-z0-9-]{0,39}$/.test(options.profile) || ['default', 'prod', 'production'].includes(options.profile)) {
    throw new Error('--profile must name a non-production profile');
  }
  return options;
}

export function guestCommand(root, profile, argv) {
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
  PATH="$root/bin:$PWD/plugins/attn-pi/node_modules/.bin:$HOME/.local/bin:$HOME/.bun/bin:$HOME/.cargo/bin:/usr/local/bin:/usr/bin:/bin" \
  ATTN_PROFILE=${quote(profile)} ATTN_HARNESS_PROFILE=${quote(profile)} CI=true \
  ATTN_REAL_APP_ARTIFACTS_DIR="$root/artifacts" \
  ${argv.map(quote).join(' ')} 9>&-
`;
}

function guestRoot(provider) {
  const home = provider.ssh('printf %s "$HOME"', { stdio: ['ignore', 'pipe', 'inherit'] }).trim();
  if (!home.startsWith('/')) throw new Error('Guest did not report an absolute home directory');
  return path.posix.join(home, '.local/share/attn-linux-runner', key);
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

export function main(argv = process.argv.slice(2)) {
  const options = parseArgs(argv);
  if (options.help) {
    console.log(`Usage: pnpm --dir app real-app:linux <action> [options] [-- command args...]

Actions:
  up          Create/start VM (Lima or OrbStack); check SSH
  status      Inspect VM or SSH machine
  stop        Stop managed VM; preserves disk and caches
  connection  Print provider/SSH connection as JSON
  sync        Copy HEAD plus staged, unstaged and non-ignored new files
  provision   Sync and install Ubuntu runner toolchains, sandbox and fixtures
  fixtures    Sync and install only remote scenario tools and mock agents
  run         Run a command in the synced Linux checkout; preserve its exit code
  build       Sync and install the packaged app in the named guest profile
  test        Run the serial app matrix under Xvfb; collect artifacts even on failure
  artifacts   Copy the guest artifacts directory to --out
  clean       Remove the installed guest profile using its own CLI

Options:
  --provider lima|orb|ssh     Default: ATTN_LINUX_PROVIDER or lima
  --name <name>              Default: ATTN_LINUX_VM or attn-linux
  --target user@host         Required for ssh; ATTN_LINUX_SSH_TARGET
  --ssh-config <file>        Optional SSH config for ssh; ATTN_LINUX_SSH_CONFIG
  --profile <name>           Default: linux-<checkout hash>; never production
  --out <directory>          Local artifacts destination

Examples:
  pnpm --dir app real-app:linux provision
  pnpm --dir app real-app:linux run -- bun test --cwd plugins/attn-pi
  pnpm --dir app real-app:linux build
  pnpm --dir app real-app:linux test -- --scenario pi-automode
  pnpm --dir app real-app:linux provision --provider ssh --target tester@linux
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
  if (!['sync', 'provision', 'fixtures', 'run', 'build', 'test', 'artifacts', 'clean'].includes(options.action)) {
    throw new Error(`Unknown action: ${options.action}`);
  }
  if (['provision', 'fixtures'].includes(options.action)) {
    provider.up();
    provider.ssh('bash -s', {
      input: fs.readFileSync(path.join(repo, 'app/scripts/real-app-harness/vm/bootstrap.sh')),
      stdio: ['pipe', 'inherit', 'inherit'],
    });
  }
  const root = guestRoot(provider);
  const destination = path.resolve(options.out || path.join(os.tmpdir(), 'attn-linux-artifacts', options.provider, key));
  const execute = (args) => provider.ssh(`bash -c ${quote(guestCommand(root, options.profile, args))}`);
  if (['sync', 'provision', 'fixtures', 'build'].includes(options.action)) syncSource(provider, repo, root);
  if (options.action === 'sync') return;
  if (['provision', 'fixtures'].includes(options.action)) {
    return execute(['bash', 'app/scripts/real-app-harness/vm/provision.sh', options.action === 'fixtures' ? 'fixtures' : 'runner']);
  }
  if (options.action === 'build') {
    return execute(['node', 'app/scripts/real-app-harness/vm/build.mjs']);
  }
  if (options.action === 'run') return execute(options.commandArgs);
  if (options.action === 'clean') return execute(['./attn', 'profile', 'clean', options.profile]);
  if (options.action === 'artifacts') return collect(provider, root, destination);
  let testError;
  try {
    execute(['xvfb-run', '-a', '-s', '-screen 0 1600x1000x24', 'pnpm', '--dir', 'app', 'run', 'real-app:serial-matrix', ...(options.commandArgs || [])]);
  } catch (error) { testError = error; }
  try { collect(provider, root, destination); }
  catch (error) { if (!testError) throw error; console.error(`Artifact collection failed: ${error.message}`); }
  if (testError) throw testError;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { main(); }
  catch (error) { console.error(error.message); process.exitCode = error.exitCode || 1; }
}
