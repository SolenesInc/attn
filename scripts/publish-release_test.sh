#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$root/scripts/publish-release.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-publish-release-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"

if [[ "$1 $2" == "release edit" || "$1 $2" == "release view" ]]; then
  exit 0
fi
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/releases?per_page=100'* ]]; then
  printf '%s\n' "$FAKE_RELEASE_TAGS"
  exit 0
fi
echo "unexpected gh command: $*" >&2
exit 2
EOF
chmod +x "$work/bin/gh"

export PATH="$work/bin:$PATH"
export GITHUB_REPOSITORY=example/attn
export FAKE_GH_LOG="$work/gh.log"

export FAKE_RELEASE_TAGS=$'v1.2.5\nv1.2.4\nnot-semver'
: >"$FAKE_GH_LOG"
"$script" v1.2.4 >"$work/older.out"
grep -Fq 'release edit v1.2.4 --repo example/attn --draft=false --latest=false' "$FAKE_GH_LOG"
grep -Fq 'release edit v1.2.5 --repo example/attn --latest' "$FAKE_GH_LOG"
grep -Fq 'latest remains newer v1.2.5' "$work/older.out"

export FAKE_RELEASE_TAGS=$'v1.2.5\nv1.2.10\nv1.2.9'
: >"$FAKE_GH_LOG"
"$script" v1.2.10 >"$work/newer.out"
grep -Fq 'release edit v1.2.10 --repo example/attn --latest' "$FAKE_GH_LOG"
grep -Fq 'published v1.2.10 as the latest stable version' "$work/newer.out"

if "$script" unsafe-tag >"$work/invalid.out" 2>&1; then
  echo "invalid release tag was published" >&2
  exit 1
fi

echo "publish release: OK"
