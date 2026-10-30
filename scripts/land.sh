#!/usr/bin/env bash
set -euo pipefail

[ "$#" -eq 0 ] || { echo "usage: scripts/land.sh" >&2; exit 2; }

cd "$(git rev-parse --show-toplevel)"

max_attempts=5
protocol_outputs=(
  internal/protocol/generated.go
  internal/protocol/protocol_version.go
  app/src/types/generated.ts
  app/src/types/protocolVersion.ts
)
prompt_outputs=(
  internal/prompts/catalog.generated.json
  app/src/prompts/catalog.generated.json
  plugins/attn-pi/automode/prompts.generated.json
)

fail() {
  echo "land: $*" >&2
  exit 1
}

generator_for() {
  local output
  for output in "${protocol_outputs[@]}"; do
    [ "$1" = "$output" ] && { echo generate-types; return; }
  done
  for output in "${prompt_outputs[@]}"; do
    [ "$1" = "$output" ] && { echo generate-prompts; return; }
  done
  return 1
}

rebasing() {
  [ -e "$(git rev-parse --git-path rebase-merge)" ] || [ -e "$(git rev-parse --git-path rebase-apply)" ]
}

conflicted_files() {
  git diff --name-only --diff-filter=U
}

generators_for_conflicts() {
  local file
  while IFS= read -r file; do
    generator_for "$file" || return 1
  done < <(conflicted_files) | sort -u
}

regenerate() {
  local file generator
  while IFS= read -r file; do
    git checkout --theirs -- "$file"
  done < <(conflicted_files)
  for generator in "$@"; do
    make "$generator" || { git rebase --abort; fail "make $generator failed while rebasing"; }
    case "$generator" in
      generate-types) git add -- "${protocol_outputs[@]}" ;;
      generate-prompts) git add -- "${prompt_outputs[@]}" ;;
    esac
  done
}

regenerated=0
rebase_onto_main() {
  local generators conflicted generator
  local -a generator_list
  regenerated=0
  git rebase --quiet origin/main >/dev/null 2>&1 || true
  while rebasing; do
    if ! generators="$(generators_for_conflicts)" || [ -z "$generators" ]; then
      conflicted="$(conflicted_files)"
      git rebase --abort
      [ -n "$conflicted" ] || fail "rebasing onto origin/main stopped; run git rebase origin/main to see why"
      fail "rebasing onto origin/main conflicts in:
$conflicted
Rebase yourself with git rebase origin/main, then run scripts/land.sh again."
    fi
    generator_list=()
    while IFS= read -r generator; do
      generator_list+=("$generator")
    done <<<"$generators"
    regenerate "${generator_list[@]}"
    regenerated=1
    GIT_EDITOR=true git rebase --continue >/dev/null 2>&1 || true
  done
  git merge-base --is-ancestor origin/main HEAD || fail "could not rebase onto origin/main; run git rebase origin/main to see why"
}

run_checks() {
  local changed file dir packages targets=()
  changed="$(git diff --name-only origin/main...HEAD)"
  touches() { grep -qE "$1" <<<"$changed"; }
  if touches '\.go$|^go\.(mod|sum)$'; then
    packages='./...'
    if ! touches '^go\.(mod|sum)$'; then
      packages="$(while IFS= read -r file; do
        case "$file" in
          *.go)
            dir="$(dirname "$file")"
            if compgen -G "$dir/*.go" >/dev/null; then
              printf './%s\n' "$dir"
            fi
            ;;
        esac
      done <<<"$changed" | sort -u | paste -sd ' ' -)"
      packages="${packages:-./...}"
    fi
    targets+=(check-go "GO_PACKAGES=$packages")
  fi
  touches '^internal/protocol/schema/' && targets+=(check-types)
  touches '^internal/prompts/|^cmd/promptgen/' && targets+=(check-prompts)
  touches '^app/' && targets+=(check-frontend)
  touches '^scripts/' && targets+=(test-scripts)
  if [ "${#targets[@]}" -gt 0 ]; then
    make "${targets[@]}"
  fi
}

git diff --quiet && git diff --cached --quiet || fail "commit or stash your changes first"
rebasing && fail "finish the rebase in progress first"

for attempt in $(seq "$max_attempts"); do
  git fetch --quiet origin main
  [ -n "$(git rev-list origin/main..HEAD)" ] || { echo "land: nothing to land"; exit 0; }
  rebase_onto_main
  if [ "$attempt" -eq 1 ] || [ "$regenerated" -eq 1 ]; then
    run_checks
  fi
  if ATTN_LANDING=1 git push --quiet origin HEAD:main; then
    echo "land: main is at $(git rev-parse --short HEAD)"
    exit 0
  fi
  git fetch --quiet origin main
  git merge-base --is-ancestor origin/main HEAD && fail "git push failed, and not because main moved"
  echo "land: main moved; rebasing again" >&2
done
fail "main moved $max_attempts times while landing; run scripts/land.sh again"
