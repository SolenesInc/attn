#!/usr/bin/env bash
# usage: apt-layer.sh build|install <layer-dir>
set -euo pipefail

verb="$1"
layer="$(realpath -m "$2")"
here="$(cd "$(dirname "$0")" && pwd)"
mapfile -t packages < <(grep -v '^#' "$here/packages.txt")

build() {
  # Do not drop the PPA: terminal-block-copy and terminal-context-menu
  # need fish >= 4 for its OSC 133 markers, and 24.04 ships 3.7.
  ./scripts/ci-retry.sh -- sudo add-apt-repository -y ppa:fish-shell/release-4
  ./scripts/ci-retry.sh -- sudo apt-get update

  mkdir -p "$layer/partial"
  # An empty dpkg state resolves the full closure, so the layer does not depend
  # on which packages a given runner image already has.
  local empty_status
  empty_status="$(mktemp)"
  # 56s for 311 MB on a healthy runner (run 41922464615).
  local download_timeout=300
  local status=0
  sudo timeout "$download_timeout" apt-get install -y --download-only -o Acquire::Retries=3 \
    -o Dir::State::status="$empty_status" -o Dir::Cache::Archives="$layer" "${packages[@]}" || status=$?
  if [ "$status" = 124 ]; then
    printf '\n::error::apt download exceeded %ss (healthy: 56s for 311 MB); the Ubuntu mirror is slow from this runner: a network failure, not a commit\n' "$download_timeout"
  fi
  [ "$status" = 0 ] || exit "$status"

  sudo rm -rf "$layer/partial" "$layer/lock"
  sudo chown -R "$(id -u):$(id -g)" "$layer"
  (cd "$layer" && apt-ftparchive packages . >Packages)
}

install_from_layer() {
  local apt_state
  apt_state="$(mktemp -d)"
  mkdir -p "$apt_state/lists/partial"
  echo "deb [trusted=yes] file:$layer ./" >"$apt_state/sources.list"
  # The layer is frozen, so a newer runner image can already hold newer versions
  # of its packages. Only this priority lets apt satisfy "=" dependencies by
  # moving those back to the layer's versions.
  printf 'Package: *\nPin: origin ""\nPin-Priority: 1001\n' >"$apt_state/preferences"
  local only_the_layer=(
    -o Dir::Etc::sourcelist="$apt_state/sources.list"
    -o Dir::Etc::sourceparts=-
    -o Dir::Etc::preferences="$apt_state/preferences"
    -o Dir::Etc::preferencesparts=-
    -o Dir::State::lists="$apt_state/lists"
  )
  if ! sudo apt-get update "${only_the_layer[@]}" ||
    ! sudo apt-get install -y --allow-downgrades "${only_the_layer[@]}" "${packages[@]}"; then
    printf '\n::error::the apt layer at %s could not install its packages (cache key %s). A corrupt layer stays broken until its cache entry is deleted: gh cache delete %s\n' \
      "$layer" "${APT_LAYER_KEY:-unknown}" "${APT_LAYER_KEY:-unknown}"
    exit 1
  fi
}

case "$verb" in
  build) build ;;
  install) install_from_layer ;;
  *)
    echo "unknown verb '$verb': expected build or install" >&2
    exit 2
    ;;
esac
