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
if [ -e "$LAND_MOVE_MAIN" ]; then
  rm "$LAND_MOVE_MAIN"
  git -C "$LAND_OTHER" pull -q origin main
  git -C "$LAND_OTHER" commit -q --allow-empty -m 'Landed during the gate'
  git -C "$LAND_OTHER" push -q origin HEAD:main
fi
exit "${LAND_CHECK_EXIT:-0}"
EOF
chmod +x "$work/bin/make"
export PATH="$work/bin:$PATH"
export LAND_CHECK_LOG="$work/checks" LAND_MOVE_MAIN="$work/move-main" LAND_OTHER="$work/other"
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
cp "$root/scripts/landing-tests" scripts/landing-tests
cat >scripts/landing-tests.sh <<'EOF'
#!/usr/bin/env bash
printf '%s\n' landing-tests "$@" >>"$LAND_CHECK_LOG"
exit "${LAND_TESTS_EXIT:-0}"
EOF
chmod +x scripts/landing-tests.sh
git add .
git commit -qm baseline
git remote add origin "$work/remote"
git push -q origin HEAD:main
git clone -q -b main "$work/remote" "$LAND_OTHER"
git -C "$LAND_OTHER" config user.name 'Other Landing'
git -C "$LAND_OTHER" config user.email 'other@example.invalid'

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
expect_checks check-go 'GO_PACKAGES=./internal/example' landing-tests go prompts

git rm -q internal/example/other.go internal/removed/removed.go
land 'Go deletions'
expect_checks check-go 'GO_PACKAGES=./internal/example' landing-tests go prompts

printf '\n' >>go.mod
land 'Module change'
expect_checks check-go 'GO_PACKAGES=./...' landing-tests go prompts

mkdir -p app/src
printf 'export {};\n' >app/src/example.ts
land 'App change'
expect_checks check-frontend landing-tests app

mkdir -p scripts internal/protocol/schema internal/prompts
printf 'true\n' >scripts/example.sh
printf 'schema\n' >internal/protocol/schema/example.tsp
printf 'prompt\n' >internal/prompts/example.md
land 'Scripts and generated inputs'
expect_checks check-types check-prompts test-scripts landing-tests prompts

printf '\n' >>README.md
land 'Documentation change'
[ ! -s "$LAND_CHECK_LOG" ]

touch "$LAND_MOVE_MAIN"
printf '\n' >>internal/example/example.go
land 'Go change while main moves'
expect_checks check-go 'GO_PACKAGES=./internal/example' landing-tests go prompts \
  check-go 'GO_PACKAGES=./internal/example' landing-tests go prompts

refuses() {
  local before
  printf '\n' >>app/src/example.ts
  git add -A
  git commit -qm "$1"
  before="$(git --git-dir="$work/remote" rev-parse main)"
  if env "$2" bash "$root/scripts/land.sh" >"$work/failure" 2>&1; then
    echo "landing pushed despite $1" >&2
    exit 1
  fi
  [ "$before" = "$(git --git-dir="$work/remote" rev-parse main)" ]
  git reset -q --hard origin/main
}

refuses 'a failed app check' LAND_CHECK_EXIT=1
refuses 'a failed landing test' LAND_TESTS_EXIT=1

echo 'landing fast gate: OK'
