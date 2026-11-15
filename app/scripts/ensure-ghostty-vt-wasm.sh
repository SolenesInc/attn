#!/bin/bash
exec "$(dirname "${BASH_SOURCE[0]}")/../../scripts/ensure-ghostty-vt-wasm.sh" "$@"
