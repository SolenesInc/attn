source "$(dirname "$0")/../../worlds/attn-images.sh"
plant task "Export the ask-image resolver" "${images_checks[@]}" < <(images_brief)
