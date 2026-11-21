#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/libghostty-vt.sh"

readonly usage="usage: rust-build-store.sh provide <recipe> [--target TRIPLE]
       rust-build-store.sh provide-variants <list-file>
       rust-build-store.sh key <recipe> [--target TRIPLE]
       rust-build-store.sh entry <recipe> [--target TRIPLE]
       rust-build-store.sh ensure-toolchain <recipe>
recipes: attn attn-pty-host attn-app"
readonly idle_minutes=$((7 * 24 * 60))
readonly repo="$vt_repo_dir"
store="$(dirname "$(vt_cache_root)")/rust-builds"
readonly store

die() {
  echo "error: $*" >&2
  exit 1
}

host_triple() {
  case "$(uname -s)-$(uname -m)" in
    Darwin-arm64) printf 'aarch64-apple-darwin' ;;
    Linux-x86_64) printf 'x86_64-unknown-linux-gnu' ;;
    Linux-aarch64) printf 'aarch64-unknown-linux-gnu' ;;
    *) die "no Rust triple known for host $(uname -s)-$(uname -m)" ;;
  esac
}

zig_link_target() {
  case "$1" in
    x86_64-unknown-linux-gnu) printf 'x86_64-linux-gnu.2.28' ;;
    aarch64-unknown-linux-gnu) printf 'aarch64-linux-gnu.2.28' ;;
    *) die "cannot cross-build for '$1': --target takes x86_64-unknown-linux-gnu or aarch64-unknown-linux-gnu" ;;
  esac
}

vt_platform() {
  case "$triple" in
    aarch64-apple-darwin) printf 'darwin_arm64' ;;
    x86_64-unknown-linux-gnu) printf 'linux_amd64' ;;
    aarch64-unknown-linux-gnu) printf 'linux_arm64' ;;
  esac
}

load_recipe() {
  recipe="$1"
  declared=()
  target_dir=""
  local vt="third_party/ghostty-vt/$(vt_platform)"
  case "$recipe" in
    attn)
      workspace=cli binary=attn profile=release
      cargo_args=(--release)
      roots=(cli)
      ;;
    attn-pty-host)
      workspace=pty-host binary=attn-pty-host profile=release
      cargo_args=(--release)
      roots=(pty-host ghostty-vt.pin "$vt/lib" "$vt/include")
      declared=("ATTN_PTY_HOST_SNAPSHOT_FORMAT=$(bash "$repo/scripts/snapshot-format.sh")")
      ;;
    attn-pty-host-variant)
      workspace=pty-host binary=attn-pty-host profile=debug
      cargo_args=()
      [[ -z "$variant_fault" ]] || cargo_args=(--features adopt-fault)
      target_dir="$repo/pty-host/target/test-variants/$variant_name"
      roots=(pty-host ghostty-vt.pin "$vt/lib" "$vt/include")
      declared=("ATTN_PTY_HOST_SNAPSHOT_FORMAT=$variant_format")
      ;;
    attn-app)
      workspace=native binary=attn-app profile=debug
      cargo_args=(-p attn-app)
      roots=(native cli/protocol
        internal/colors/schemes ghostty-vt-native.lock ghostty-vt-wasm.lock scripts/snapshot-format.sh "$vt/lib" "$vt/include")
      ;;
    *) die "unknown recipe '$recipe'"$'\n'"$usage" ;;
  esac
  target_dir="${target_dir:-$repo/$workspace/target}"
}

recipe_key() {
  local root
  for root in "${roots[@]}"; do
    [[ -e "$repo/$root" ]] || die "$recipe's input root $root is missing"
  done
  (
    cd "$repo"
    printf '%s\n' "$recipe" "$triple" "$profile" ${cargo_args[@]+"${cargo_args[@]}"}
    printf 'env %s\n' ${declared[@]+"${declared[@]}"}
    cat rust-toolchain.toml
    if [[ -n "$link_target" ]]; then
      printf '%s\n' "$link_target"
      cat scripts/rust-zig-linker.sh scripts/rust-zig-ar.sh
    fi
    find -L "${roots[@]}" -path "$workspace/target" -prune -o -type f -print0 | LC_ALL=C sort -z | xargs -0 shasum -a 256
  ) | shasum -a 256 | cut -d' ' -f1
}

ensure_toolchain() {
  local channel installed
  command -v rustup >/dev/null || return 0
  channel="$(sed -n 's/^channel = "\(.*\)"$/\1/p' "$repo/rust-toolchain.toml")"
  [[ -n "$channel" ]] || die "rust-toolchain.toml names no channel"
  installed="$(rustup toolchain list)"
  [[ $'\n'"$installed" == *$'\n'"$channel"-* ]] && return 0
  (cd "$repo/$workspace" && env -u RUSTUP_TOOLCHAIN rustup toolchain install) >&2
}

entry_for() {
  printf '%s/%s/%s' "$store" "$recipe" "$1"
}

link_env() {
  [[ -n "$link_target" ]] || return 0
  local triple_env upper
  triple_env="$(printf '%s' "$triple" | tr '-' '_')"
  upper="$(printf '%s' "$triple_env" | tr '[:lower:]' '[:upper:]')"
  printf '%s\n' \
    "ATTN_RUST_LINK_TARGET=$link_target" \
    "CC_$triple_env=$repo/scripts/rust-zig-linker.sh" \
    "AR_$triple_env=$repo/scripts/rust-zig-ar.sh" \
    "CARGO_TARGET_${upper}_LINKER=$repo/scripts/rust-zig-linker.sh"
}

cargo_build() {
  local messages="$1" build_env=() line
  while IFS= read -r line; do build_env+=("$line"); done < <(link_env)
  build_env+=(${declared[@]+"${declared[@]}"})
  local args=(cargo build --locked --message-format=json-render-diagnostics --target-dir "$target_dir" ${cargo_args[@]+"${cargo_args[@]}"})
  [[ -z "$link_target" ]] || args+=(--target "$triple")
  (
    cd "$repo/$workspace"
    env -u RUSTUP_TOOLCHAIN ${build_env[@]+"${build_env[@]}"} "${args[@]}"
  ) > "$messages"
}

output_dir() {
  if [[ -n "$link_target" ]]; then
    printf '%s/%s/%s' "$target_dir" "$triple" "$profile"
  else
    printf '%s/%s' "$target_dir" "$profile"
  fi
}

dep_info_paths() {
  awk '
    {
      line = $0
      if (!started) { sub(/^([^:]|\\:)*: */, "", line); started = 1 }
      gsub(/\\ /, "\001", line)
      n = split(line, words, / +/)
      for (i = 1; i <= n; i++) {
        if (words[i] == "" || words[i] == "\\") continue
        gsub("\001", " ", words[i])
        print words[i]
      }
      if ($0 !~ /\\$/) exit
    }
  ' "$1"
}

normalize_paths() {
  awk '
    {
      n = split($0, parts, "/")
      depth = 0
      for (i = 1; i <= n; i++) {
        if (parts[i] == "" || parts[i] == ".") continue
        if (parts[i] == "..") { if (depth > 0) depth--; continue }
        kept[++depth] = parts[i]
      }
      path = ""
      for (i = 1; i <= depth; i++) path = path "/" kept[i]
      print path
    }
  '
}

within() {
  local path="$1" prefix="$2"
  [[ "$path" == "$prefix" || "$path" == "$prefix"/* ]]
}

build_script_stdout() {
  local run_dir="$1"
  if [[ -f "$run_dir/output" ]]; then
    cat "$run_dir/output"
  elif [[ -f "$run_dir/run/stdout" ]]; then
    cat "$run_dir/run/stdout"
  else
    die "no build script output beside $run_dir/out, so $recipe's build script variables cannot be read"
  fi
}

check_inputs() {
  local messages="$1" built_dir="$2" profile_rel build_dir path paths root allowed outside=() name undeclared=() declared_names=" " line
  profile_rel="${built_dir##*"$target_dir"/}"
  build_dir="$(grep -o '"filenames":\[[^]]*\]' "$messages" | grep -oE "\"[^\"]*/${profile_rel}/(deps|build)/" | head -n1 | tr -d '"')" || true
  [[ -n "$build_dir" ]] || die "cargo reported no artifact under $profile_rel/deps or $profile_rel/build, so $recipe's rustc dep-info cannot be read"
  build_dir="${build_dir%/*/}"
  [[ -f "$built_dir/$binary.d" ]] || die "cargo wrote no dep-info at $built_dir/$binary.d, so $recipe's inputs cannot be checked"
  local repo_real build_outputs
  repo_real="$(cd "$repo" && pwd -P)"
  build_outputs="$(printf '%s\n' "$build_dir" | normalize_paths)"
  paths="$(dep_info_paths "$built_dir/$binary.d" | normalize_paths | LC_ALL=C sort -u)"
  [[ -n "$paths" ]] || die "no inputs read from $built_dir/$binary.d, so $recipe's inputs cannot be checked"
  while IFS= read -r path; do
    allowed=""
    if within "$path" "$build_outputs" || within "$path" "$(printf '%s\n' "$target_dir" | normalize_paths)"; then
      allowed=1
    else
      for root in "${roots[@]}"; do
        if within "$path" "$repo/$root" || within "$path" "$repo_real/$root"; then
          allowed=1
          break
        fi
      done
    fi
    [[ -n "$allowed" ]] || outside+=("$path")
  done <<< "$paths"
  if [[ ${#outside[@]} -gt 0 ]]; then
    {
      echo "error: $recipe read files outside its input roots, so the store refuses to keep this build:"
      printf '  %s\n' "${outside[@]}"
      echo "Add each file's root to the $recipe recipe in scripts/lib/rust-build-store.sh, or stop reading it."
    } >&2
    exit 1
  fi

  local entry run_dir script_vars=""
  while IFS= read -r run_dir; do
    script_vars+="$(build_script_stdout "$run_dir" | sed -n 's/^cargo::*rerun-if-env-changed=//p')"$'\n'
  done < <(grep -o '"out_dir":"[^"]*"' "$messages" | sed 's/^"out_dir":"\(.*\)\/out"$/\1/')
  for entry in ${declared[@]+"${declared[@]}"}; do declared_names+="${entry%%=*} "; done
  while IFS= read -r line; do declared_names+="${line%%=*} "; done < <(link_env)
  while IFS= read -r name; do
    [[ -n "$name" && "$name" != RUSTUP_TOOLCHAIN ]] || continue
    [[ "$declared_names" != *" $name "* ]] || continue
    printenv "$name" >/dev/null || continue
    undeclared+=("$name")
  done < <(
    {
      { grep -rl -F --include='*.d' --exclude-dir=incremental -e "$repo/" -e "$repo_real/" "$build_dir" || true; } |
        while IFS= read -r file; do
          sed -n 's/^# env-dep:\([^=]*\).*/\1/p' "$file"
        done
      printf '%s' "$script_vars"
    } | LC_ALL=C sort -u
  )
  if [[ ${#undeclared[@]} -gt 0 ]]; then
    {
      echo "error: $recipe's build reads environment variables its recipe does not declare, so the store refuses to keep this build:"
      printf '  %s\n' "${undeclared[@]}"
      echo "Unset each one, or declare it in the $recipe recipe in scripts/lib/rust-build-store.sh."
    } >&2
    exit 1
  fi
}

place_by_rename() {
  local source="$1" destination="$2" pending
  mkdir -p "$(dirname "$destination")"
  pending="$(mktemp "$destination.XXXXXX")"
  cp "$source" "$pending"
  chmod 755 "$pending"
  mv -f "$pending" "$destination"
}

evict_idle_entries() {
  find "$store" -mindepth 2 -maxdepth 2 -type d -mmin "+$idle_minutes" -exec rm -rf {} +
}

provide() {
  local key entry built_dir
  key="$(recipe_key)"
  entry="$(entry_for "$key")"
  provided="$entry/$binary" provided_stored=1
  if [[ -f "$entry/$binary" ]]; then
    touch "$entry"
    return
  fi
  mkdir -p "$store"
  messages="$(mktemp "$store/.cargo-messages.XXXXXX")"
  trap 'rm -f "$messages"' EXIT
  cargo_build "$messages"
  built_dir="$(output_dir)"
  check_inputs "$messages" "$built_dir"
  rm -f "$messages"
  if [[ "$(recipe_key)" != "$key" ]]; then
    echo "warning: $recipe's inputs changed during the build, so this binary is not stored" >&2
    provided="$built_dir/$binary" provided_stored=""
    return
  fi
  place_by_rename "$built_dir/$binary" "$entry/$binary"
  evict_idle_entries
}

provide_variants() {
  local list="$1" name format fault extra keys="" set_dir pending
  local names=() binaries=()
  [[ -f "$list" ]] || die "no variant list at $list"
  while read -r name format fault extra <&3 || [[ -n "$name" ]]; do
    [[ -n "$name" && "$name" != \#* ]] || continue
    [[ -n "$format" && -z "$extra" && ( -z "$fault" || "$fault" == adopt-fault ) ]] ||
      die "$list: '$name $format $fault $extra' is not '<name> <snapshot format> [adopt-fault]'"
    variant_name="$name" variant_format="$format" variant_fault="$fault"
    load_recipe attn-pty-host-variant
    provide
    [[ -n "$provided_stored" ]] || die "pty-host changed while variant $name was building; run again"
    names+=("$name")
    binaries+=("$provided")
    keys+="$name $(basename "$(dirname "$provided")")"$'\n'
  done 3< "$list"
  [[ ${#names[@]} -gt 0 ]] || die "$list lists no variants"
  set_dir="$store/variant-sets/$(printf '%s' "$keys" | shasum -a 256 | cut -d' ' -f1)"
  local i
  for i in "${!names[@]}"; do
    if [[ ! -f "$set_dir/${names[$i]}/$binary" ]]; then
      mkdir -p "$set_dir/${names[$i]}"
      pending="$(mktemp "$set_dir/${names[$i]}/$binary.XXXXXX")"
      ln -f "${binaries[$i]}" "$pending"
      mv -f "$pending" "$set_dir/${names[$i]}/$binary"
    fi
  done
  mkdir -p "$set_dir"
  touch "$set_dir"
  printf '%s\n' "$set_dir"
}

command="${1:-}"
[[ $# -gt 0 ]] && shift
name="${1:-}"
[[ $# -gt 0 ]] && shift
target=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --target) [[ $# -ge 2 ]] || die "--target needs a triple"; target="$2"; shift 2 ;;
    *) die "unknown argument '$1'"$'\n'"$usage" ;;
  esac
done
[[ -n "$name" ]] || die "$usage"

link_target=""
triple="$(host_triple)"
if [[ -n "$target" ]]; then
  link_target="$(zig_link_target "$target")"
  triple="$target"
fi

case "$command" in
  provide-variants)
    [[ -z "$target" ]] || die "provide-variants builds for the host only"
    provide_variants "$name"
    ;;
  provide | key | entry | ensure-toolchain)
    [[ "$name" != attn-pty-host-variant ]] || die "variants come from a list: provide-variants <list-file>"
    load_recipe "$name"
    case "$command" in
      provide) provide; printf '%s\n' "$provided" ;;
      key) recipe_key ;;
      entry) entry_for "$(recipe_key)"; echo ;;
      ensure-toolchain) ensure_toolchain ;;
    esac
    ;;
  *) die "$usage" ;;
esac
