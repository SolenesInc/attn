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
if [[ "$1 $2 $3 $4" == "api repos/example/attn/releases --paginate --jq" ]]; then
  jq -r "$5" <<'JSON'
[
  {"id": 129, "tag_name": "v1.2.9", "draft": true},
  {"id": 1210, "tag_name": "v1.2.10", "draft": true},
  {"id": 125, "tag_name": "v1.2.5", "draft": true},
  {"id": 124, "tag_name": "v1.2.4", "draft": true},
  {"id": 123, "tag_name": "v1.2.3", "draft": false}
]
JSON
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

"$script" v1.2.9 >"$work/numeric-older.out"
"$script" v1.2.10 >"$work/numeric-newer.out"
[[ "$(<"$FAKE_LATEST_TAG")" == "v1.2.10" ]]

rm -f "$FAKE_RELEASE_STATE"/*
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

if "$script" unsafe-tag >"$work/invalid.out" 2>&1; then
  echo "invalid release tag was published" >&2
  exit 1
fi

echo "publish release: OK"
