#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-pre-commit-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/repo/app"

cat >"$work/bin/git" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
case "$*" in
  'rev-parse --show-toplevel') printf '%s\n' "$HOOK_TEST_ROOT" ;;
  'diff --cached --name-only --diff-filter=ACMR') echo app/src/example.ts ;;
  *) echo "unexpected git command: $*" >&2; exit 1 ;;
esac
SH
cat >"$work/bin/pnpm" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf '%s|%s\n' "$*" "${NODE_OPTIONS-unset}" >>"$HOOK_TEST_LOG"
SH
printf '#!/usr/bin/env bash\nexit 0\n' >"$work/repo/attn"
chmod +x "$work/bin/git" "$work/bin/pnpm" "$work/repo/attn"

export PATH="$work/bin:$PATH" HOOK_TEST_ROOT="$work/repo"
env -u NODE_OPTIONS HOOK_TEST_LOG="$work/default.log" bash "$root/scripts/pre-commit.sh" &
default_pid=$!
NODE_OPTIONS=--max-old-space-size=2048 HOOK_TEST_LOG="$work/options.log" bash "$root/scripts/pre-commit.sh" &
options_pid=$!
wait "$default_pid"
wait "$options_pid"

printf 'run test|unset\nrun build|unset\nrun e2e|unset\n' >"$work/default.expected"
printf 'run test|--max-old-space-size=2048\nrun build|--max-old-space-size=2048\nrun e2e|--max-old-space-size=2048\n' >"$work/options.expected"
diff -u "$work/default.expected" "$work/default.log"
diff -u "$work/options.expected" "$work/options.log"
echo 'pre-commit environment: OK'
