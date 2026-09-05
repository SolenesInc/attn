import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { quote, run } from './providers.mjs';

export function makeSourceArchive(repo, directory, execute = run) {
  const git = (args) => execute('git', ['-C', repo, ...args], { stdio: ['ignore', 'pipe', 'inherit'] });
  git(['bundle', 'create', path.join(directory, 'source.bundle'), 'HEAD']);
  fs.writeFileSync(path.join(directory, 'staged.patch'), git(['diff', '--binary', '--cached', 'HEAD']));
  fs.writeFileSync(path.join(directory, 'working.patch'), git(['diff', '--binary']));
  const files = git(['ls-files', '--others', '--exclude-standard', '-z']);
  execute('tar', ['--no-xattrs', '-czf', path.join(directory, 'untracked.tgz'), '-C', repo, '--null', '-T', '-'], {
    input: files, stdio: ['pipe', 'inherit', 'inherit'], env: { ...process.env, COPYFILE_DISABLE: '1' },
  });
  const archive = path.join(directory, 'source.tgz');
  execute('tar', ['--no-xattrs', '-czf', archive, '-C', directory, 'source.bundle', 'staged.patch', 'working.patch', 'untracked.tgz']);
  return archive;
}

export function syncScript(root) {
  return `set -euo pipefail
root=${quote(root)}
mkdir -p "$root"
exec 9>"$root/operation.lock"
flock -n 9 || { echo 'Runner is busy; wait for its active command before syncing' >&2; exit 1; }
if [[ -d "$root/source" && ! -f "$root/managed-source" ]]; then
  echo 'Refusing to replace a checkout not created by this runner' >&2; exit 1
fi
temp=$(mktemp -d "$root/upload.XXXXXX")
trap 'rm -rf "$temp"' EXIT
tar -xzf - -C "$temp"
if [[ ! -d "$root/source" ]]; then
  git -c core.hooksPath=/dev/null clone --quiet "$temp/source.bundle" "$root/source"
  touch "$root/managed-source"
fi
cd "$root/source"
git -c core.hooksPath=/dev/null fetch --quiet "$temp/source.bundle" HEAD
git -c core.hooksPath=/dev/null reset --hard --quiet FETCH_HEAD
git clean -fdq
git remote remove origin 2>/dev/null || true
if [[ -s "$temp/staged.patch" ]]; then git apply --index "$temp/staged.patch"; fi
if [[ -s "$temp/working.patch" ]]; then git apply "$temp/working.patch"; fi
tar -xzf "$temp/untracked.tgz"
printf 'Linux checkout: %s\\n' "$PWD"
git rev-parse HEAD
`;
}

export function syncSource(provider, repo, root) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-linux-source-'));
  try {
    const archive = makeSourceArchive(repo, directory);
    provider.ssh(`bash -c ${quote(syncScript(root))}`, {
      input: fs.readFileSync(archive), stdio: ['pipe', 'inherit', 'inherit'],
    });
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
}
