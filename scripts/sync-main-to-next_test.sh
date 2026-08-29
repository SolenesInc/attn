#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$root/scripts/sync-main-to-next.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-main-sync-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"

case "$1 $2" in
  "auth status")
    exit 0
    ;;
  "api --paginate")
    if [[ "$*" != *'/pulls?state=open&base=main&per_page=100'* ]]; then
      echo "unexpected paginated API command: $*" >&2
      exit 2
    fi
    printf '%s' "${FAKE_ACTIVE_CANDIDATE:-}"
    ;;
  "pr create")
    printf '%s\n' 'https://github.com/example/attn/pull/1'
    ;;
  *)
    echo "unexpected gh command: $*" >&2
    exit 2
    ;;
esac
EOF
chmod +x "$work/bin/gh"

version="$(jq -r .version "$root/app/package.json")"

setup_fixture() {
  local name="$1"
  local rewrite_fragment="${2:-0}"
  fixture_origin="$work/${name}-origin.git"
  fixture_repo="$work/${name}-repo"
  fixture_log="$work/${name}-gh.log"

  git init -q --bare "$fixture_origin"
  git --git-dir="$fixture_origin" config receive.shallowUpdate true
  git clone -q "$root" "$fixture_repo"
  git -C "$fixture_repo" config user.name 'Release Train Test'
  git -C "$fixture_repo" config user.email 'release-train@example.com'
  git -C "$fixture_repo" remote set-url origin "$fixture_origin"
  git -C "$fixture_repo" switch -q -C main
  git -C "$fixture_repo" push -q -u origin main

  local main_at_cut
  main_at_cut="$(git -C "$fixture_repo" rev-parse HEAD)"
  git -C "$fixture_repo" switch -q -c next
  printf '%s\n' 'kind: added' 'area: release' 'change: frozen change' \
    >"$fixture_repo/changelog.d/frozen.yaml"
  printf '%s\n' 'frozen feature' >"$fixture_repo/frozen-feature.txt"
  git -C "$fixture_repo" add changelog.d/frozen.yaml frozen-feature.txt
  git -C "$fixture_repo" commit -q -m 'feat(release): add frozen change'
  local source_sha
  source_sha="$(git -C "$fixture_repo" rev-parse HEAD)"
  git -C "$fixture_repo" push -q -u origin next

  git -C "$fixture_repo" switch -q -c "release/v${version}"
  git -C "$fixture_repo" rm -q changelog.d/frozen.yaml
  printf '\n## Test release\n\n- Frozen change.\n' \
    >>"$fixture_repo/CHANGELOG.md"
  cat >"$fixture_repo/.github/release-candidate.yml" <<EOF
version: ${version}
kind: promotion
source_sha: ${source_sha}
main_sha: ${main_at_cut}
EOF
  git -C "$fixture_repo" add CHANGELOG.md .github/release-candidate.yml
  git -C "$fixture_repo" commit -q -m "release: v${version}"

  git -C "$fixture_repo" switch -q main
  git -C "$fixture_repo" merge -q --squash "release/v${version}"
  git -C "$fixture_repo" commit -q -m "release: v${version}"
  git -C "$fixture_repo" push -q origin main

  git -C "$fixture_repo" switch -q next
  if [[ "$rewrite_fragment" -eq 1 ]]; then
    printf '%s\n' 'kind: added' 'area: release' 'change: rewritten later' \
      >"$fixture_repo/changelog.d/frozen.yaml"
  fi
  printf '%s\n' 'kind: fixed' 'area: queue' 'change: later change' \
    >"$fixture_repo/changelog.d/later.yaml"
  printf '%s\n' 'later feature' >"$fixture_repo/later-feature.txt"
  git -C "$fixture_repo" add changelog.d frozen-feature.txt later-feature.txt
  git -C "$fixture_repo" commit -q -m 'fix(queue): land after the freeze'
  git -C "$fixture_repo" push -q origin next
}

run_sync() {
  (
    cd "$fixture_repo"
    PATH="$work/bin:$PATH" GOCACHE="$work/go-cache" FAKE_GH_LOG="$fixture_log" \
      FAKE_ACTIVE_CANDIDATE="${1:-}" "$script"
  )
}

setup_fixture success
for active_candidate in \
  $'release/v9.9.9\thttps://github.com/example/attn/pull/9' \
  $'hotfix/startup-crash\thttps://github.com/example/attn/pull/10'; do
  if run_sync "$active_candidate" >"$work/active.out" 2>&1; then
    echo "expected an active candidate to block sync" >&2
    exit 1
  fi
done
if git --git-dir="$fixture_origin" for-each-ref --format='%(refname)' \
  'refs/heads/sync/main-into-next-*' | grep -q .; then
  echo "active candidate created a sync branch" >&2
  exit 1
fi
grep -Fq 'hotfix/.+' "$fixture_log"

run_sync >"$work/success.out"
sync_ref="$(
  git --git-dir="$fixture_origin" for-each-ref --format='%(refname)' \
    'refs/heads/sync/main-into-next-*'
)"
if [[ -z "$sync_ref" ]]; then
  echo "sync branch was not pushed" >&2
  exit 1
fi
git --git-dir="$fixture_origin" merge-base --is-ancestor \
  refs/heads/main "$sync_ref"
if git --git-dir="$fixture_origin" cat-file -e \
  "$sync_ref:changelog.d/frozen.yaml" 2>/dev/null; then
  echo "released fragment survived sync" >&2
  exit 1
fi
git --git-dir="$fixture_origin" cat-file -e \
  "$sync_ref:changelog.d/later.yaml"
parent_line="$(git --git-dir="$fixture_origin" rev-list --parents -n 1 "$sync_ref")"
if [[ "$(wc -w <<<"$parent_line" | tr -d ' ')" -ne 3 ]]; then
  echo "sync head is not a two-parent merge commit: $parent_line" >&2
  exit 1
fi
grep -q 'pr create --base next --head sync/main-into-next-' "$fixture_log"

setup_fixture rewritten 1
if run_sync >"$work/rewritten.out" 2>&1; then
  echo "expected rewritten released fragment to block sync" >&2
  exit 1
fi
grep -q 'changed after the candidate was cut' "$work/rewritten.out"
if git --git-dir="$fixture_origin" for-each-ref --format='%(refname)' \
  'refs/heads/sync/main-into-next-*' | grep -q .; then
  echo "failed rewritten-fragment sync pushed a branch" >&2
  exit 1
fi

echo "main to next sync: OK"
