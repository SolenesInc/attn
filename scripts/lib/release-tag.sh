require_remote_tag_absent() {
  local remote="$1"
  local tag="$2"
  local label="$3"
  local existing_message="$4"
  local status=0
  git ls-remote --exit-code "$remote" "refs/tags/${tag}" \
    >/dev/null 2>&1 || status=$?
  case "$status" in
    0)
      echo "${label}: ${existing_message}" >&2
      return 1
      ;;
    2) ;;
    *)
      echo "${label}: could not check remote tag ${tag}" >&2
      return 1
      ;;
  esac
}
