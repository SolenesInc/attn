#!/usr/bin/env bash

# Fixture repos are fresh directories, so `go run ./cmd/<name>` recompiles in
# each one. Build the working tree's commands once and let a `go` shim run them.
install_prebuilt_go_run() {
  local root="$1" bin_dir="$2"
  shift 2
  local real_go
  real_go="$(command -v go)"
  mkdir -p "$bin_dir/prebuilt"
  local name
  for name in "$@"; do
    (cd "$root" && "$real_go" build -o "$bin_dir/prebuilt/$name" "./cmd/$name") || return 1
  done
  cat >"$bin_dir/go" <<EOF
#!/usr/bin/env bash
if [ "\${1:-}" = run ] && [[ "\${2:-}" == ./cmd/* ]] && [ -x "$bin_dir/prebuilt/\${2#./cmd/}" ]; then
  name="\${2#./cmd/}"
  shift 2
  exec "$bin_dir/prebuilt/\$name" "\$@"
fi
exec "$real_go" "\$@"
EOF
  chmod +x "$bin_dir/go"
}
