#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-test-git-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/wrapper" "$work/fake"
cat >"$work/wrapper/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'wrapper\n' >>"$WRAPPER_GIT_LOG"
exec git "$@"
EOF
cat >"$work/fake/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'fake\n' >>"$FAKE_GIT_LOG"
if [[ "$(wc -l <"$FAKE_GIT_LOG")" -gt 1 ]]; then
  echo "wrapping Git recursed into the fake" >&2
  exit 2
fi
exec "$REAL_GIT" "$@"
EOF
chmod +x "$work/wrapper/git" "$work/fake/git"

export WRAPPER_GIT_LOG="$work/wrapper.log"
export FAKE_GIT_LOG="$work/fake.log"
unset ATTN_TEST_GIT
source "$root/scripts/lib/test-git.sh"
PATH="$work/wrapper:$PATH"
export PATH
REAL_GIT="$(resolve_test_git)"
export REAL_GIT

PATH="$work/fake:$PATH" git --version >/dev/null
[[ "$(wc -l <"$FAKE_GIT_LOG")" -eq 1 ]]
if [ -e "$WRAPPER_GIT_LOG" ]; then
  echo "test Git resolved through the wrapping executable" >&2
  exit 1
fi

echo "test Git: OK"
