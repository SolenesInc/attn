#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
gate="$root/scripts/candidate-gate.sh"
changelog_gate="$root/scripts/changelog-gate.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-candidate-gate-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"

case "$1 $2" in
  "pr list")
    printf '%s\n' "${FAKE_CANDIDATES:-[]}"
    ;;
  "api --method")
    if [[ "${FAKE_ACCEPTANCE_MODE:-success}" != missing ]]; then
      printf '%s\t%s\t%s\t%s\n' "$FAKE_ACCEPTANCE_SHA" \
        "${FAKE_ACCEPTANCE_STATUS:-completed}" \
        "${FAKE_ACCEPTANCE_CONCLUSION:-success}" \
        'https://github.com/example/attn/actions/runs/42/job/7'
    fi
    ;;
  *)
    echo "unexpected gh command: $*" >&2
    exit 2
    ;;
esac
EOF
chmod +x "$work/bin/gh"

export PATH="$work/bin:$PATH"
export GOCACHE="$work/go-cache"
export FAKE_GH_LOG="$work/gh.log"
export FAKE_CANDIDATES='[]'
export FAKE_ACCEPTANCE_MODE=success
export FAKE_ACCEPTANCE_STATUS=completed
export FAKE_ACCEPTANCE_CONCLUSION=success

repo="$work/repo"
git clone -q "$root" "$repo"
git -C "$repo" config user.name 'Candidate Gate Test'
git -C "$repo" config user.email 'candidate-gate@example.com'
git -C "$repo" switch -q -C main
git -C "$repo" rm -q -- 'changelog.d/*.yaml'
git -C "$repo" commit -q -m 'release baseline'
main_sha="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" update-ref refs/remotes/origin/main "$main_sha"

expect_failure() {
  local expected="$1"
  shift
  if "$@" >"$work/failure.out" 2>&1; then
    echo "expected candidate gate failure: $*" >&2
    exit 1
  fi
  if ! grep -Fq "$expected" "$work/failure.out"; then
    echo "failure did not contain '$expected':" >&2
    cat "$work/failure.out" >&2
    exit 1
  fi
}

run_gate() (
  cd "$repo"
  "$gate" "$@"
)

git -C "$repo" switch -q -c next
printf '%s\n' 'promotion feature' >"$repo/promotion.txt"
printf '%s\n' 'kind: internal' 'area: release' 'change: promotion fixture' \
  >"$repo/changelog.d/promotion.yaml"
git -C "$repo" add promotion.txt changelog.d/promotion.yaml
git -C "$repo" commit -q -m 'feat(release): add promotion fixture'
promotion_source="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" switch -q -c release/v99.98.97
(cd "$repo" && go run ./cmd/release-train version set v99.98.97)
(cd "$repo" && go run ./cmd/release-train manifest write \
  --version v99.98.97 --kind promotion \
  --source "$promotion_source" --main "$main_sha")
git -C "$repo" rm -q changelog.d/promotion.yaml
git -C "$repo" add .github/release-candidate.yml app
git -C "$repo" commit -q -m 'chore(release): prepare v99.98.97'
export FAKE_ACCEPTANCE_SHA="$promotion_source"
export FAKE_CANDIDATES='[{"headRefName":"release/v99.98.97","url":"https://github.com/example/attn/pull/1"}]'
run_gate promotion origin/main HEAD release/v99.98.97 >"$work/promotion.out"
grep -Fq 'source Acceptance is green' "$work/promotion.out"
(cd "$repo" && "$changelog_gate" main release/v99.98.97) >"$work/promotion-changelog.out"
grep -Fq 'validated promotion candidate' "$work/promotion-changelog.out"

export FAKE_ACCEPTANCE_CONCLUSION=failure
expect_failure 'Acceptance is completed/failure' \
  run_gate promotion origin/main HEAD release/v99.98.97
export FAKE_ACCEPTANCE_CONCLUSION=success

export FAKE_CANDIDATES='[{"headRefName":"hotfix/other","url":"https://github.com/example/attn/pull/2"}]'
expect_failure 'another release candidate is open' \
  run_gate promotion origin/main HEAD release/v99.98.97
export FAKE_CANDIDATES='[]'

printf '%s\n' 'not release metadata' >"$repo/late-product-edit.txt"
git -C "$repo" add late-product-edit.txt
git -C "$repo" commit -q -m 'fix(release): mutate frozen candidate'
expect_failure 'candidate changes non-release file' \
  run_gate promotion origin/main HEAD release/v99.98.97

git -C "$repo" switch -q main
git -C "$repo" switch -q -c hotfix/startup-crash
printf '%s\n' 'hotfix' >"$repo/hotfix.txt"
printf '%s\n' 'kind: internal' 'area: release' 'change: hotfix fixture' \
  >"$repo/changelog.d/hotfix.yaml"
git -C "$repo" add hotfix.txt changelog.d/hotfix.yaml
git -C "$repo" commit -q -m 'fix(app): repair startup crash'
hotfix_source="$(git -C "$repo" rev-parse HEAD)"
(cd "$repo" && go run ./cmd/release-train version set v99.98.98)
(cd "$repo" && go run ./cmd/release-train manifest write \
  --version v99.98.98 --kind hotfix \
  --source "$hotfix_source" --main "$main_sha")
git -C "$repo" rm -q changelog.d/hotfix.yaml
git -C "$repo" add .github/release-candidate.yml app
git -C "$repo" commit -q -m 'chore(release): prepare v99.98.98'
: >"$FAKE_GH_LOG"
run_gate hotfix origin/main HEAD hotfix/startup-crash >"$work/hotfix.out"
if grep -q '^api --method ' "$FAKE_GH_LOG"; then
  echo "hotfix candidate queried next Acceptance" >&2
  exit 1
fi

echo "candidate gate: OK"
