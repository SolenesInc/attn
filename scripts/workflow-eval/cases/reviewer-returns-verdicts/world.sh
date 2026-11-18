source "$(dirname "$0")/../../worlds/wordstats.sh"
plant_reported_top_words
attn seed note "$child" -m "Builder: done in $(git rev-parse --short HEAD) on main. Both checks reported." >/dev/null
start_stub_builder "$child"
plant review "Review top_words" <<BODY
Review the change delivered for \`$child\` (commit \`$(git rev-parse --short HEAD)\` on main) against its brief and its two reported checks. Record a verdict on each check of \`$child\`.
BODY
