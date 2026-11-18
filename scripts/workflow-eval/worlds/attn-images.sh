# Sourced by a case's world.sh: attn at the parent of 000c32eb4, where the ask-image resolver is still private, with
# only that commit's history, so neither the fix nor the eval's own cases are reachable.
# The defect review on main found in 000c32eb4 (seed s-85tsdm): ResolveImage entity-decodes every target, but HTML
# img sources arrive already decoded, so a&amp;copy;.png became a©.png. bd0f09268 fixed it and added the
# regression test in hidden/.
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"
init_repository
git fetch -q --no-tags "$EVAL_REPOSITORY" "$(git -C "$EVAL_REPOSITORY" rev-parse 000c32eb4~1)"
git checkout -q -B main FETCH_HEAD

images_brief() {
  cat <<'BODY'
Export the ask-image resolver in `internal/ask/images.go` as `ResolveImage(baseDir, target string) (string, bool)`, so the phone gateway's `ask_image` (a later seed) can resolve an image's `src` against the ask's `base_dir` exactly as `ImagePaths` resolves the images the ask's markdown names. `ImagePaths` resolves through `ResolveImage`, so the two cannot disagree.

`ImagePaths` keeps returning the same paths as today for every image: markdown image destinations and raw HTML `<img>` sources alike, including escaped characters and entity references.

Out of scope: the gateway's `ask_image` itself.
BODY
}
images_checks=(
  --check "ImagePaths returns the same path as before this change for every image, markdown destinations and raw HTML img sources alike, and go test ./internal/ask passes."
  --check "ResolveImage is exported and ImagePaths resolves through it."
)

# commit_builder_change defect|clean: commits the change a Builder delivered for the brief above. The defect is
# 000c32eb4's images.go; the clean change only renames the private resolver, so every path stays as it was.
commit_builder_change() {
  if [ "$1" = defect ]; then
    git -C "$EVAL_REPOSITORY" show "000c32eb4:internal/ask/images.go" > internal/ask/images.go
  else
    perl -pi -e 's/\blocalImagePath\b/ResolveImage/g' internal/ask/images.go
  fi
  git commit -qam "Export the ask-image resolver as ResolveImage for the phone gateway"
}

# plant_reported_child <plot>: plants the brief as a child of the plot, reported on both checks with real test output.
plant_reported_child() {
  plant child "Export the ask-image resolver" --part-of "$1" "${images_checks[@]}" < <(images_brief)
  local evidence
  evidence=$(mktemp -d)/go-test-internal-ask.txt
  ATTN_DATA_DIR=$(mktemp -d) go test -count=1 -v ./internal/ask > "$evidence" 2>&1
  local commit
  commit=$(git rev-parse --short HEAD)
  attn seed check report "$child" C1 -m "go test ./internal/ask passes; ImagePaths keeps its behavior." --evidence "$evidence" --commit "$commit" >/dev/null
  attn seed check report "$child" C2 -m "ResolveImage is exported; ImagePaths calls it." --evidence "$evidence" --commit "$commit" >/dev/null
  attn seed note "$child" -m "Builder: done in $commit on main. Both checks reported with the package's test output." >/dev/null
  start_stub_builder "$child"
}

plant_images_plot() {
  plant plot "Phone gateway: ask images" <<'BODY'
## Goal

The phone shows an ask's images. The gateway's `ask_image` reads the home's copy of an image the ask names, resolving its `src` as the desktop does.

## Decisions

- D1. One resolver for desktop and phone: `internal/ask` exports it, and `ImagePaths` uses it, so the two cannot disagree about which file an image is.

## Execution

Child 1 exports the resolver. The gateway's `ask_image` follows in a later child. Review each child against its checks before the next starts.
BODY
}
