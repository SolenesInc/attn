#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-land-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin" "$work/repo"
cat >"$work/bin/make" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$@" >>"$LAND_CHECK_LOG"
exit "${LAND_CHECK_EXIT:-0}"
EOF
chmod +x "$work/bin/make"
export PATH="$work/bin:$PATH"
export LAND_CHECK_LOG="$work/checks"
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
git init -q --bare "$work/remote"
cd "$work/repo"
git init -q
git config user.name 'Landing Test'
git config user.email 'landing@example.invalid'
git config core.hooksPath /dev/null
git switch -qc work
mkdir -p internal/example internal/removed app/src scripts internal/protocol/schema internal/prompts
printf 'package example\n' >internal/example/example.go
printf 'package example\n' >internal/example/other.go
printf 'package removed\n' >internal/removed/removed.go
printf 'module example.invalid/landing\n' >go.mod
printf 'baseline\n' >README.md
git add .
git commit -qm baseline
git remote add origin "$work/remote"
git push -q origin HEAD:main

land() {
  : >"$LAND_CHECK_LOG"
  git add -A
  git commit -qm "$1"
  bash "$root/scripts/land.sh" >"$work/output" 2>&1 || { cat "$work/output" >&2; return 1; }
  [ "$(git rev-parse HEAD)" = "$(git --git-dir="$work/remote" rev-parse main)" ]
}

expect_checks() {
  printf '%s\n' "$@" >"$work/expected"
  diff -u "$work/expected" "$LAND_CHECK_LOG"
}

printf '\n' >>internal/example/example.go
land 'Go change'
expect_checks check-go 'GO_PACKAGES=./internal/example'

git rm -q internal/example/other.go internal/removed/removed.go
land 'Go deletions'
expect_checks check-go 'GO_PACKAGES=./internal/example'

printf '\n' >>go.mod
land 'Module change'
expect_checks check-go 'GO_PACKAGES=./...'

mkdir -p app/src
printf 'export {};\n' >app/src/example.ts
land 'App change'
expect_checks check-frontend

mkdir -p scripts internal/protocol/schema internal/prompts
printf 'true\n' >scripts/example.sh
printf 'schema\n' >internal/protocol/schema/example.tsp
printf 'prompt\n' >internal/prompts/example.md
land 'Scripts and generated inputs'
expect_checks check-types check-prompts test-scripts

printf '\n' >>README.md
land 'Documentation change'
[ ! -s "$LAND_CHECK_LOG" ]

printf '\n' >>app/src/example.ts
git add -A
git commit -qm 'Failing app check'
before="$(git --git-dir="$work/remote" rev-parse main)"
if LAND_CHECK_EXIT=1 bash "$root/scripts/land.sh" >"$work/failure" 2>&1; then
  echo 'landing pushed despite a failed check' >&2
  exit 1
fi
[ "$before" = "$(git --git-dir="$work/remote" rev-parse main)" ]

echo 'landing fast gate: OK'
