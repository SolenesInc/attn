#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$root/scripts/ci-pass-records"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-ci-pass-records-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

fail() {
  echo "FAIL $*" >&2
  exit 1
}

repo="$work/repo"
mkdir -p "$repo/.github/workflows" "$repo/app/src/gen" "$repo/cmd" "$repo/docs"
cd "$repo"
git init -q
cat >.github/ci-inputs.yml <<'EOF'
# a comment
every_group:
  - 'docs/**'

app:
  - 'cmd/**'
go:
  - 'app/**'
  - '!app/src/gen/**'
EOF
echo one >app/src/view.ts
echo one >app/src/gen/types.go
echo 'package main' >cmd/main.go
echo notes >docs/notes.md
echo 'name: CI' >.github/workflows/ci.yml
git add -A
git -c user.name=test -c user.email=test@example.com commit -qm base

key() {
  "$script" keys | awk -v group="$1" '$1 == group { print $2 }'
}
stage() {
  echo "$2" >"$1"
  git add -A
}

[ "$("$script" keys | cut -d' ' -f1 | tr '\n' ' ')" = 'app go ' ] || fail "keys lists the groups in file order, without every_group"
app_key="$(key app)"
go_key="$(key go)"
[[ "$app_key" =~ ^[0-9a-f]{64}$ ]] || fail "app key is a sha256: '$app_key'"
[ "$app_key" != "$go_key" ] || fail "groups with different inputs share a key"

clone="$work/clone"
git clone -q "$repo" "$clone"
[ "$(cd "$clone" && "$script" keys)" = "$("$script" keys)" ] || fail "the same inputs in another checkout give other keys"

stage docs/notes.md more
[ "$(key app)/$(key go)" = "$app_key/$go_key" ] || fail "a file every group leaves out changed a key"

stage cmd/main.go 'package main // 2'
[ "$(key app)" = "$app_key" ] || fail "a file app leaves out changed app's key"
[ "$(key go)" != "$go_key" ] || fail "a change to go's inputs kept go's key"
go_key="$(key go)"

stage app/src/view.ts two
[ "$(key go)" = "$go_key" ] || fail "a file go leaves out changed go's key"
[ "$(key app)" != "$app_key" ] || fail "a change to app's inputs kept app's key"
app_key="$(key app)"

stage app/src/gen/types.go two
[ "$(key go)" != "$go_key" ] || fail "a change to a file go keeps with '!' kept go's key"
go_key="$(key go)"
app_key="$(key app)"

stage new-tool.sh 'echo hi'
[ "$(key app)" != "$app_key" ] && [ "$(key go)" != "$go_key" ] || fail "a new file no group leaves out kept a key"
app_key="$(key app)"
go_key="$(key go)"

git mv app/src/gen/types.go app/src/gen/models.go
[ "$(key go)" != "$go_key" ] || fail "a rename among go's inputs kept go's key"
go_key="$(key go)"

chmod +x app/src/gen/models.go
git add -A
[ "$(key go)" != "$go_key" ] || fail "a mode change among go's inputs kept go's key"
go_key="$(key go)"

stage .github/workflows/ci.yml 'name: CI 2'
[ "$(key app)" != "$app_key" ] && [ "$(key go)" != "$go_key" ] || fail "a change under .github/ kept a key"

for glob in "'.github/**'" "'**'" "'**/*.yml'"; do
  printf 'bad:\n  - %s\n' "$glob" >>.github/ci-inputs.yml
  if "$script" keys >/dev/null 2>"$work/err"; then
    fail "keys let a group leave out files under .github/ with $glob"
  fi
  grep -Fq 'which every group reads' "$work/err" || fail "keys refused $glob without saying why"
  git checkout -q -- .github/ci-inputs.yml
done
printf 'bad:\n  - app/**\n' >>.github/ci-inputs.yml
"$script" keys >/dev/null 2>&1 && fail "keys read an unquoted glob"
git checkout -q -- .github/ci-inputs.yml

job_groups='{"view": ["app"], "view-e2e": ["app"], "vet": ["go"], "gate": ["app", "go"]}'
passed() {
  NEEDS_JSON="$1" JOB_GROUPS="$job_groups" "$script" passed
}
[ "$(passed '{"view":{"result":"success"},"view-e2e":{"result":"success"},"vet":{"result":"success"},"gate":{"result":"success"}}')" = "app $(key app)
go $(key go)" ] || fail "passed left out a group whose jobs all succeeded"
[ "$(passed '{"view":{"result":"success"},"view-e2e":{"result":"failure"},"vet":{"result":"success"},"gate":{"result":"success"}}')" = "go $(key go)" ] ||
  fail "passed recorded a group with a failed job"
[ -z "$(passed '{"view":{"result":"skipped"},"view-e2e":{"result":"skipped"},"vet":{"result":"success"},"gate":{"result":"failure"}}')" ] ||
  fail "passed recorded a carried group or one whose shared gate failed"

cd "$root"
workflow=.github/workflows/ci.yml
gated="$(awk '
  /^  [a-z0-9-]+:$/ { job = substr($1, 1, length($1) - 1) }
  /^    if: needs\.changes\.outputs\.[a-z_]+ != .carried.$/ {
    match($0, /outputs\.[a-z_]+/); print job, substr($0, RSTART + 8, RLENGTH - 8)
  }
  /needs\.changes\.outputs\.[a-z_]+ != .carried. \|\|/ {
    line = $0
    while (match(line, /outputs\.[a-z_]+ != /)) { print job, substr(line, RSTART + 8, RLENGTH - 12); line = substr(line, RSTART + RLENGTH) }
  }
' "$workflow" | sort)"
declared="$(awk '/JOB_GROUPS: >-/ { on = 1; next } on && !/^        / { on = 0 } on' "$workflow" |
  jq -r 'to_entries[] | .key + " " + .value[]' | sort)"
[ -n "$gated" ] || fail "found no job in $workflow gated on a carried group"
[ "$gated" = "$declared" ] || {
  diff <(echo "$gated") <(echo "$declared") >&2 || true
  fail "Acceptance's JOB_GROUPS in $workflow differs from the groups the jobs' if: lines name (left: if:, right: JOB_GROUPS)"
}
for group in $(cut -d' ' -f2 <<<"$declared" | sort -u); do
  grep -Fq "group: $group" "$workflow" || fail "$workflow never reads or writes $group's pass record"
  [ "$(grep -c "^          group: $group\$" "$workflow")" = 2 ] || fail "$workflow does not both read and write $group's pass record"
done
"$script" keys >/dev/null || fail "keys cannot read .github/ci-inputs.yml"

echo "ci-pass-records: ok"
