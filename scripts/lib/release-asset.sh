# Sourced by the ghostty-vt fetch scripts. The repo is private, so the public
# releases/download URL 404s; `gh` downloads through the API with the caller's login or GH_TOKEN.
release_asset_download() {
  local repo="$1" tag="$2" asset="$3" out="$4"
  if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    echo "    gh release download $tag --repo $repo --pattern $asset"
    gh release download "$tag" --repo "$repo" --pattern "$asset" --output "$out" --clobber
    return
  fi
  local url="https://github.com/${repo}/releases/download/${tag}/${asset}"
  echo "    $url (no gh login; install gh and run gh auth login for a private repo)"
  curl -fL --retry 3 --retry-delay 1 -o "$out" "$url"
}
