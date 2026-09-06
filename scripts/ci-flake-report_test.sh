#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
report="$root/scripts/ci-flake-report.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-flake-report-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin" "$work/logs"

# Two runs on the same commit: the first red, the second green. That is the
# shape the report calls a flake, so whatever the log says is a flake.
cat >"$work/runs.json" <<'EOF'
[
  {"databaseId":901,"headSha":"aaaa1111","headBranch":"next","conclusion":"failure",
   "createdAt":"2026-09-06T01:00:00Z","attempt":1,"event":"push","status":"completed"},
  {"databaseId":902,"headSha":"aaaa1111","headBranch":"next","conclusion":"success",
   "createdAt":"2026-09-06T02:00:00Z","attempt":1,"event":"push","status":"completed"}
]
EOF

cat >"$work/jobs-901.json" <<'EOF'
{"jobs":[{"id":5001,"name":"App acceptance","conclusion":"failure"}]}
EOF
cat >"$work/jobs-902.json" <<'EOF'
{"jobs":[]}
EOF

# A serial-matrix digest as the job actually prints it, timestamps included, so
# the extractor is exercised on the real shape.
printf '%s\n' \
  '2026-09-06T03:16:57.9648747Z PASS  garden-plot-dispatch                          61.2s' \
  '2026-09-06T03:16:57.9648747Z FAIL  worktree-surface                              97.8s' \
  '2026-09-06T03:16:57.9648747Z SKIP  terminal-block-copy                           needs macOS' \
  >"$work/logs/5001.log"

# gh 2.98 refuses to write a job log — ANSI throughout — without being asked to.
# `advertises` is whether `gh api --help` offers the flag at all.
fake_gh() {
  local advertises="$1"
  cat >"$work/bin/gh" <<EOF
#!/usr/bin/env bash
set -euo pipefail
if [ "\$1" = run ] && [ "\$2" = list ]; then cat "$work/runs.json"; exit 0; fi
if [ "\$1" = api ] && [ "\$2" = --help ]; then
  if [ "$advertises" = yes ]; then
    echo '      --allow-escape-sequences   Allow printing terminal escape sequences'
  fi
  exit 0
fi
allowed=no
for arg in "\$@"; do [ "\$arg" = --allow-escape-sequences ] && allowed=yes; done
for arg in "\$@"; do
  case "\$arg" in
    */attempts/*/jobs*)
      run="\${arg#repos/*/actions/runs/}"; run="\${run%%/*}"
      cat "$work/jobs-\$run.json"; exit 0 ;;
    */jobs/*/logs)
      job="\${arg#repos/*/actions/jobs/}"; job="\${job%/logs}"
      if [ "\$allowed" = no ]; then
        echo 'the response contains terminal escape sequences; pass --allow-escape-sequences to output it anyway' >&2
        exit 1
      fi
      cat "$work/logs/\$job.log"; exit 0 ;;
  esac
done
echo "unexpected gh command: \$*" >&2
exit 2
EOF
  chmod +x "$work/bin/gh"
}

export PATH="$work/bin:$PATH"
fail() { echo "ci-flake-report test: $1" >&2; exit 1; }

run_report() {
  rm -rf "$work/cache"
  ATTN_FLAKE_CACHE="$work/cache" "$report" --repo example/attn "$@"
}

# The log is readable, and the App acceptance scenario is named rather than
# only the job that ran it.
fake_gh yes
out="$(run_report --limit 10 --format summary)" || fail "the report should succeed when logs are readable"
grep -q 'serial-matrix › worktree-surface' <<<"$out" \
  || fail "the failing scenario is missing from the ledger: $out"
if grep -q 'garden-plot-dispatch' <<<"$out"; then fail "a passing scenario was counted as a failure"; fi
if grep -q 'terminal-block-copy' <<<"$out"; then fail "a skipped scenario was counted as a failure"; fi

out="$(run_report --limit 10 --format table)" || fail "the table format should succeed"
grep -q '1 logs read, 0 unreadable' <<<"$out" || fail "the table never says how many logs it read: $out"

# The regression this guard exists for: every log fetch refused, so the report
# sees no failure at all. Publishing a clean ledger from that is worse than
# publishing nothing.
fake_gh no
status=0
out="$(run_report --limit 10 --format markdown 2>"$work/err.txt")" || status=$?
[ "$status" -ne 0 ] || fail "a report that read no log must not publish a verdict"
grep -q 'read 0 of 1 failed job logs' "$work/err.txt" \
  || fail "going blind was not reported: $(cat "$work/err.txt")"
grep -q 'escape sequences' "$work/err.txt" \
  || fail "the underlying gh error was swallowed: $(cat "$work/err.txt")"
if grep -q '## Active' <<<"$out"; then fail "a blind report still printed an Active section"; fi

echo "ci-flake-report: all tests passed"
