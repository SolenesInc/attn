#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
report="$root/scripts/ci-flake-report.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-flake-report-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin" "$work/gh" "$work/repo"
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
cd "$work/repo"
git init -q -b main
for n in 1 2 3; do
  git -c user.name=t -c user.email=t@example.invalid commit -q --allow-empty -m "commit $n"
  commit[n]="$(git rev-parse HEAD)"
done

unit() { printf 'go ./internal/example %s' "$1"; }
result() { jq -nc --arg unit "$(unit "$1")" --argjson attempt "$2" --arg outcome "$3" --argjson cached "${4:-false}" \
  '{unit: $unit, attempt: $attempt, outcome: $outcome, cached: $cached}'; }
artifact() {
  local run="$1" commit="$2"
  shift 2
  mkdir -p "$work/receipt-$run"
  jq -n --arg id "ci-$run" --arg run "$run" --arg commit "$commit" --slurpfile results <(printf '%s\n' "$@") '{
    schema: "attn.test-receipt/v1", repo: "github.com/example/attn", invocation_id: $id, runner: "go",
    source: {kind: "ci", run_id: $run, attempt: "1", job: "packages"},
    revision: {commit: $commit, tree: "t"}, lane: {os: "linux", arch: "amd64", race: false},
    diagnostics: {machine_class: "github-hosted"},
    command: {argv: ["test"], started_at: "2026-10-31T10:00:00Z", finished_at: "2026-10-31T10:01:00Z", exit: {code: 1}},
    results: $results, complete: true}' >"$work/receipt-$run/ci-$run.json"
  (cd "$work/receipt-$run" && zip -qr "$work/gh/zip-$run.zip" .)
}

artifact 901 "${commit[1]}" "$(result TestFlaky 1 fail)" "$(result TestFlaky 2 pass)" \
  "$(result TestBroken 1 fail)" "$(result TestSkipped 1 skip)" "$(result TestRelapsed 1 fail)" "$(result TestRelapsed 2 pass)"
artifact 902 "${commit[2]}" "$(result TestFlaky 1 pass)" "$(result TestBroken 1 pass true)" "$(result TestRelapsed 1 pass)"
artifact 903 "${commit[3]}" "$(result TestFlaky 1 pass)" "$(result TestBroken 1 pass)" "$(result TestRelapsed 1 fail)" "$(result TestRelapsed 2 fail)"
artifact 904 "${commit[3]}" "$(result TestHidden 1 fail)"

listing() { printf '{"artifacts":[{"id":%s,"name":"%s","expired":%s}]}\n' "$2" "$3" "$4" >"$work/gh/artifacts-$1.json"; }
listing 901 901 test-receipts-packages-1 false
listing 902 902 test-receipts-packages-1 false
listing 903 903 test-receipts-packages-1 false
listing 904 904 test-receipts-packages-1 true
listing 905 9050 app-acceptance-digest-1 false
cat >"$work/gh/runs.json" <<'EOF'
[{"databaseId":906,"status":"in_progress"},{"databaseId":905,"status":"completed"},
 {"databaseId":904,"status":"completed"},{"databaseId":903,"status":"completed"},
 {"databaseId":902,"status":"completed"},{"databaseId":901,"status":"completed"}]
EOF

cat >"$work/bin/gh" <<EOF
#!/usr/bin/env bash
set -euo pipefail
if [ "\$1 \$2" = "run list" ]; then
  [[ " \$* " == *" --workflow ci.yml "* ]] || { echo "run list without --workflow ci.yml: \$*" >&2; exit 2; }
  limit="\$(sed -E 's/.*--limit ([0-9]+).*/\\1/' <<<"\$*")"
  jq ".[:\$limit]" "$work/gh/runs.json"; exit 0
fi
for arg in "\$@"; do
  case "\$arg" in
    repos/example/attn/actions/runs/*/artifacts*)
      run="\${arg#repos/example/attn/actions/runs/}"; run="\${run%%/*}"
      cat "$work/gh/artifacts-\$run.json"; exit 0 ;;
    repos/example/attn/actions/artifacts/*/zip)
      [ -z "\${FAKE_GH_DOWNLOADS_FAIL:-}" ] || { echo "HTTP 401" >&2; exit 1; }
      id="\${arg#repos/example/attn/actions/artifacts/}"; id="\${id%/zip}"
      cat "$work/gh/zip-\$id.zip"; exit 0 ;;
  esac
done
echo "unexpected gh command: \$*" >&2
exit 2
EOF
chmod +x "$work/bin/gh"
export PATH="$work/bin:$PATH"

fail() { echo "ci-flake-report test: $1" >&2; exit 1; }
run_report() { ATTN_FLAKE_CACHE="$work/cache" "$report" --repo example/attn --main main "$@"; }
expect() { grep -qF -- "$2" <<<"$1" || fail "missing '$2' from:
$1"; }
refuse() { if grep -qF -- "$2" <<<"$1"; then fail "unexpected '$2' in:
$1"; fi; }
short() { git rev-parse --short=9 "$1"; }

out="$(run_report --format json --dormant-after 2)"
records="$(jq -c '[.failures[] | [.unit, .flaky_commits, .failed_commits, .passes_since]]' <<<"$out")"
[ "$records" = '[["go ./internal/example TestRelapsed",1,2,0],["go ./internal/example TestFlaky",1,1,2],["go ./internal/example TestBroken",0,1,1]]' ] \
  || fail "records: $records; a retry's pass makes a flake, a cached pass clears nothing, a skip or an expired artifact is no result"
expect "$(jq -r .source <<<"$out")" "the last 5 completed ci.yml runs of example/attn (3 read, 1 expired, 0 unreadable; 1 runs uploaded none)"

out="$(run_report --dormant-after 2)"
expect "$out" "3 receipts from 3 CI runs; 3 commits of main from $(short "${commit[1]}") to $(short "${commit[3]}")"
expect "$(sed -n '/^DORMANT/,/^$/p' <<<"$out")" "TestFlaky"
expect "$(sed -n '/^UNRESOLVED/,$p' <<<"$out")" "TestBroken"
expect "$(sed -n '/^UNRESOLVED/,$p' <<<"$out")" "TestRelapsed"
refuse "$(sed -n '/^ACTIVE/,/^$/p' <<<"$out")" "TestRelapsed"

out="$(run_report --format json --limit 3)"
expect "$(jq -r .source <<<"$out")" "the last 2 completed ci.yml runs of example/attn (0 read, 1 expired, 0 unreadable; 1 runs uploaded none)"

out="$(run_report --format markdown --dormant-after 3)"
expect "$out" "<!-- ci-flake-ledger -->"
expect "$(sed -n '/^## Active/,/^## /p' <<<"$out")" "| 1 | 1 | 2 | $(short "${commit[1]}") | linux/amd64 | go ./internal/example TestFlaky |"
refuse "$out" "## Dormant"

out="$(FAKE_GH_DOWNLOADS_FAIL=1 run_report --format json 2>/dev/null)"
[ "$(jq '.failures | length' <<<"$out")" -eq 3 ] || fail "cached artifacts were not read again: $out"

rm -rf "$work/cache"
out="$(FAKE_GH_DOWNLOADS_FAIL=1 run_report --format json 2>"$work/stderr")"
expect "$(cat "$work/stderr")" "HTTP 401"
expect "$(jq -r .source <<<"$out")" "(0 read, 1 expired, 3 unreadable; 1 runs uploaded none)"
[ "$(jq '.failures | length' <<<"$out")" -eq 0 ] || fail "an unreadable artifact produced records: $out"

echo "ci-flake-report: all tests passed"
