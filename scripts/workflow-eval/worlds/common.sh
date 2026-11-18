# Sourced by every world: the helpers that plant seeds and start stubs on the run's instance.
set -euo pipefail

seed_id() { python3 -I -c 'import json,sys; print(json.load(sys.stdin)["id"])'; }

# plant <name> <attn seed plant arguments...>: plants a seed with its body on stdin and records it for the graders.
plant() {
  local name=$1; shift
  local id
  id=$(attn seed plant "$@" --json -m - | seed_id)
  echo "$name $id" >> "$EVAL_SEEDS"
  eval "$name=$id"
}

# start_stub_builder <seed>: a stub Builder tends the seed in this checkout, so a verdict's ring has someone to reach.
start_stub_builder() {
  attn delegate --seed "$1" --role builder --cwd "$PWD" --reuse-checkout --branch main >/dev/null
}

init_repository() {
  git init -q -b main
  git config user.name eval
  git config user.email eval@example.invalid
}
