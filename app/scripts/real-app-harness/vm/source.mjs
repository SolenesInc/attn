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

// attn-linux, 2026-11-27: 39 checkouts' git reflogs held 109 gaps between syncs of one checkout. The longest
// was 38 h, on the land checkout that every landing shares; no other checkout went past 3.6 h.
const idleCheckoutDays = 7;

function evictIdleCheckouts() {
  return `runner=$(dirname "$root")
shopt -s nullglob
rm -rf "$runner"/.evicting.* || echo "Could not remove an earlier evicted checkout in $runner" >&2
cwds=$(for link in /proc/[0-9]*/cwd; do readlink "$link" 2>/dev/null || true; done)
recent() { [[ -n $(find "$@" -maxdepth 0 -mmin -${idleCheckoutDays * 24 * 60} 2>/dev/null) ]]; }
for dir in "$runner"/*/; do
  dir=\${dir%/}
  [[ $dir == "$root" ]] && continue
  recent "$dir" "$dir/operation.lock" "$dir.turn" && continue
  grep -qxF "$dir" <<<"$cwds" && continue
  grep -qF "$dir/" <<<"$cwds" && continue
  (
    exec 6>>"$dir/operation.lock" 7>>"$dir.turn" || exit 0
    flock -n 7 && flock -n 6 && [[ -d $dir ]] || exit 0
    trash="$runner/.evicting.$(basename "$dir").$BASHPID"
    mv "$dir" "$trash"
    rm -f "$dir.turn"
    if rm -rf "$trash"; then
      echo "Evicted runner checkout $(basename "$dir"), unused for ${idleCheckoutDays} days"
    else
      echo "Could not remove evicted runner checkout $trash" >&2
    fi
  )
done
for turn in "$runner"/*.turn; do
  [[ -d \${turn%.turn} ]] || recent "$turn" || rm -f "$turn"
done
`;
}

export function syncScript(root) {
  return `set -euo pipefail
root=${quote(root)}
mkdir -p "$root"
exec 9>"$root/operation.lock"
flock -n 9 || { echo 'Runner is busy; wait for its active command before syncing' >&2; exit 1; }
${evictIdleCheckouts()}if [[ -d "$root/source" && ! -f "$root/managed-source" ]]; then
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
