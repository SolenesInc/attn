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

if [[ "$1 $2" == "release view" ]]; then
  exit 0
fi
if [[ "$1" == "api" && "$2" == repos/*/releases/tags/* ]]; then
  tag="${2##*/}"
  case "$tag" in
    v1.2.4) printf '124\n' ;;
    v1.2.5) printf '125\n' ;;
    v1.2.9) printf '129\n' ;;
    v1.2.10) printf '1210\n' ;;
    *) echo "unexpected release tag: $tag" >&2; exit 2 ;;
  esac
  exit 0
fi
if [[ "$1 $2" == "api --method" && "$3" == "PATCH" && "$4" == repos/*/releases/* ]]; then
  case "${4##*/}" in
    124) tag=v1.2.4 ;;
    125) tag=v1.2.5 ;;
    129) tag=v1.2.9 ;;
    1210) tag=v1.2.10 ;;
    *) echo "unexpected release id: ${4##*/}" >&2; exit 2 ;;
  esac
  [[ "$*" == *'-F draft=false'* ]]
  [[ "$*" == *'-f make_latest=legacy'* ]]

  if [[ "$tag" == "v1.2.4" && -p "$FAKE_OLDER_STARTED" ]]; then
    printf 'started\n' >"$FAKE_OLDER_STARTED"
    read -r _ <"$FAKE_OLDER_CONTINUE"
  fi

  touch "$FAKE_RELEASE_STATE/$tag"
  latest="$(
    for release_path in "$FAKE_RELEASE_STATE"/*; do
      [[ -f "$release_path" ]] || continue
      basename "$release_path"
    done | sort -V | tail -n 1
  )"
  printf '%s\n' "$latest" >"$FAKE_LATEST_TAG"
  exit 0
fi
echo "unexpected gh command: $*" >&2
exit 2
EOF
chmod +x "$work/bin/gh"

export PATH="$work/bin:$PATH"
export GITHUB_REPOSITORY=example/attn
export FAKE_GH_LOG="$work/gh.log"
export FAKE_RELEASE_STATE="$work/releases"
export FAKE_LATEST_TAG="$work/latest"
export FAKE_OLDER_STARTED="$work/older-started"
export FAKE_OLDER_CONTINUE="$work/older-continue"
mkdir -p "$FAKE_RELEASE_STATE"
mkfifo "$FAKE_OLDER_STARTED" "$FAKE_OLDER_CONTINUE"

: >"$FAKE_GH_LOG"
"$script" v1.2.4 >"$work/older.out" &
older_pid=$!
read -r _ <"$FAKE_OLDER_STARTED"
"$script" v1.2.5 >"$work/newer.out"
printf 'continue\n' >"$FAKE_OLDER_CONTINUE"
wait "$older_pid"
[[ "$(<"$FAKE_LATEST_TAG")" == "v1.2.5" ]]
grep -Fq 'api --method PATCH repos/example/attn/releases/124 -F draft=false -f make_latest=legacy' "$FAKE_GH_LOG"
grep -Fq 'api --method PATCH repos/example/attn/releases/125 -F draft=false -f make_latest=legacy' "$FAKE_GH_LOG"

rm -f "$FAKE_RELEASE_STATE"/*
rm -f "$FAKE_OLDER_STARTED" "$FAKE_OLDER_CONTINUE"
"$script" v1.2.9 >"$work/numeric-older.out"
"$script" v1.2.10 >"$work/numeric-newer.out"
[[ "$(<"$FAKE_LATEST_TAG")" == "v1.2.10" ]]

if "$script" unsafe-tag >"$work/invalid.out" 2>&1; then
  echo "invalid release tag was published" >&2
  exit 1
fi

echo "publish release: OK"
