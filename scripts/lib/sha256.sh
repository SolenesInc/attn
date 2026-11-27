#!/bin/bash
# Defines sha256, which runs shasum -a 256 or, where shasum is not on PATH, sha256sum, with the same arguments and
# output. Arch keeps shasum in /usr/bin/core_perl, which only a login shell puts on PATH.

sha256_command() {
  if command -v shasum >/dev/null 2>&1; then
    printf 'shasum -a 256'
  elif command -v sha256sum >/dev/null 2>&1; then
    printf 'sha256sum'
  else
    echo "error: no SHA-256 implementation found (tried shasum, sha256sum)" >&2
    return 1
  fi
}

sha256() {
  local command
  local -a sum
  command="$(sha256_command)" || return 1
  read -ra sum <<<"$command"
  "${sum[@]}" "$@"
}
