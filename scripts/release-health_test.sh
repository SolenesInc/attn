#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$root/scripts/release-health.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-release-health-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"

if [[ "$1" == api ]] && [[ "$*" == *'/commits/v'* ]]; then
  printf '%s\n' "$FAKE_REMOTE_TAG_SHA"
  exit 0
fi
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/issues?'* ]]; then
  if [[ -n "${FAKE_ISSUE_STATE:-}" ]]; then
    printf '{"number":17,"state":"%s","title":"Release health: v1.2.3"}\n' \
      "$FAKE_ISSUE_STATE"
  fi
  exit 0
fi
if [[ "$1 $2" == "release view" ]]; then
  if [[ "${FAKE_RELEASE_MODE:-draft}" == missing ]]; then
    exit 1
  fi
  printf '%s\t%s\n' "${FAKE_RELEASE_DRAFT:-true}" \
    'https://github.com/example/attn/releases/tag/v1.2.3'
  exit 0
fi
if [[ "$1 $2" == "issue create" || "$1 $2" == "issue edit" ]]; then
  while [[ $# -gt 0 ]]; do
    if [[ "$1" == --body-file ]]; then
      cp "$2" "$FAKE_ISSUE_BODY"
      break
    fi
    shift
  done
  printf '%s\n' 'https://github.com/example/attn/issues/17'
  exit 0
fi
if [[ "$1 $2" == "issue reopen" || "$1 $2" == "issue close" ]]; then
  exit 0
fi

echo "unexpected gh command: $*" >&2
exit 2
EOF
chmod +x "$work/bin/gh"

export PATH="$work/bin:$PATH"
export GITHUB_REPOSITORY=example/attn
export RELEASE_HEALTH_ASSIGNEE=victorarias
export FAKE_GH_LOG="$work/gh.log"
export FAKE_ISSUE_BODY="$work/issue-body.md"
export FAKE_REMOTE_TAG_SHA=0123456789abcdef0123456789abcdef01234567
export FAKE_RELEASE_MODE=draft
export FAKE_RELEASE_DRAFT=true
tag=v1.2.3
run_url='https://github.com/example/attn/actions/runs/99'

: >"$FAKE_GH_LOG"
export FAKE_ISSUE_STATE=
"$script" failure "$tag" "$run_url" >"$work/create.out"
grep -q '^issue create ' "$FAKE_GH_LOG"
for value in "$FAKE_REMOTE_TAG_SHA" "$run_url" 'private draft' \
  "gh workflow run release.yml --ref main -f tag=$tag"; do
  grep -Fq "$value" "$FAKE_ISSUE_BODY"
done

: >"$FAKE_GH_LOG"
export FAKE_ISSUE_STATE=open
"$script" timed_out "$tag" "$run_url" >"$work/update.out"
grep -q '^issue edit 17 ' "$FAKE_GH_LOG"
if grep -q '^issue comment ' "$FAKE_GH_LOG"; then
  echo "repeated release failure added a comment" >&2
  exit 1
fi

: >"$FAKE_GH_LOG"
"$script" success "$tag" "$run_url" >"$work/recovery.out"
grep -q '^issue close 17 ' "$FAKE_GH_LOG"
grep -q 'closed #17 after recovery' "$work/recovery.out"

for value in \
  'workflows: [Release]' \
  '${{ github.event.workflow_run.display_title }}' \
  './scripts/release-health.sh'; do
  grep -Fq "$value" "$root/.github/workflows/release-health.yml"
done

echo "release health: OK"
