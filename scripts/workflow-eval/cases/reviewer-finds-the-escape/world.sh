source "$(dirname "$0")/../../worlds/attn-images.sh"
commit_builder_change defect
plant_images_plot
plant_reported_child "$plot"
plant review "Review child 1 of the ask-images plot" <<BODY
Review the change delivered for \`$child\` (commit \`$(git rev-parse --short HEAD)\` on main) against its brief and its two reported checks. Read the plot \`$plot\` for its decisions. Record a verdict on each check of \`$child\`.
BODY
