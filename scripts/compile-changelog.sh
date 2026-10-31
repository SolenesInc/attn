#!/usr/bin/env bash
set -euo pipefail

# Compile the commits since the last release into a dated CHANGELOG.md section.
#
# Renders those commits grouped by their Attn-Seed trailers, asks claude to
# write the user-facing section per the style rules below, inserts it at the
# top of CHANGELOG.md with a marker naming the commits it covers, and deletes
# changelog.d. The result is left staged for review: read the section, fix the
# copy where it misses, then commit and open a PR. See docs/internal/making-a-release.md.
#
# usage: compile-changelog.sh [--dry-run]
#   --dry-run  print the prompt that would be sent and exit without changes

DRY_RUN=0
[[ "${1:-}" == "--dry-run" ]] && DRY_RUN=1

cd "$(git rev-parse --show-toplevel)"

if [[ "$DRY_RUN" -eq 0 ]] && ! command -v claude >/dev/null 2>&1; then
  echo "error: claude CLI is required (https://claude.ai/code)" >&2
  exit 1
fi

TODAY="$(date +%Y-%m-%d)"

FACTS="$(go run ./cmd/release-train notes render)"
MARKER="$(go run ./cmd/release-train notes range)"
MARKER_PATTERN='^<!-- changelog-(fragments-sha256: [0-9a-f]{64}|commits: [0-9a-f]*\.\.[0-9a-f]+) -->$'

TOP_DATE="$(grep -m1 -o '^## \[[0-9]\{4\}-[0-9]\{2\}-[0-9]\{2\}\]' CHANGELOG.md | tr -d '#[] ' || true)"
EXISTING_SECTION=""
EXISTING_MARKERS=""
REPLACE_TOP=0
if [[ "$TOP_DATE" == "$TODAY" ]]; then
  REPLACE_TOP=1
  EXISTING_SECTION="$(awk '/^## \[/{n++} n==1 && !/^---$/' CHANGELOG.md)"
  EXISTING_MARKERS="$(printf '%s\n' "$EXISTING_SECTION" | grep -E "$MARKER_PATTERN" || true)"
  EXISTING_SECTION="$(printf '%s\n' "$EXISTING_SECTION" | grep -Ev "$MARKER_PATTERN" || true)"
fi

PROMPT="You are writing the changelog for attn, a macOS app that orchestrates
coding agents. Below are the commits that landed since the last release. They
are grouped by the seed (attn's unit of work) each commit names in its
Attn-Seed trailer: a group opens with the seed's title, state, description and
notes, then lists its commits. A seed that could not be read is marked unknown;
judge it from its commits. Commits without a seed are listed last, each on its
own. Some commits carry a release note their author wrote for users; prefer its
wording. Compile all of it into one dated CHANGELOG.md section.

Style rules — these matter more than fidelity to the commit wording:
- Write for the app's user, not the codebase. Lead every bullet with a bold
  sentence stating the change in terms of what the user experiences, then
  explain in plain prose. Clarity beats technical correctness.
- Category headers in this order, only when non-empty: ### Added, ### Changed,
  ### Fixed, ### Removed. Choose by what users experience; the commit type
  (feat, fix, perf, ...) is a hint.
- One bullet per change users would notice: usually one per seed, and one per
  commit without a seed that users would notice. Merge bullets that tell one
  story.
- Leave out work users cannot observe: refactors, tests, CI, docs, tooling,
  and release notes marked kind: internal.
- Wrap lines at roughly 80 columns.

Match the voice of these examples from the existing changelog:

### Fixed
- **⌘J jumps to the agent that has waited longest.** Jump-to-waiting used to
  pick the first waiting session in sidebar workspace order, which made its
  target feel arbitrary. It now follows the queue's own order — the turn owed
  longest, the same row the \"Your turn\" band lists first.

Output format: ONLY the markdown section, starting with the line
## [${TODAY}]
and nothing after the last bullet. No preamble, no code fences.
If no change merits a user-facing bullet, output exactly: EMPTY

Commits:

${FACTS}"

if [[ "$REPLACE_TOP" -eq 1 ]]; then
  PROMPT+="

CHANGELOG.md already has a section for ${TODAY}. Fold its bullets and the new
commits into one merged section (rewriting bullets is fine):

${EXISTING_SECTION}"
fi

if [[ "$DRY_RUN" -eq 1 ]]; then
  echo "--- prompt (dry run, no changes made) ---"
  echo "$PROMPT"
  exit 0
fi

echo "writing section with claude..."
SECTION="$(printf '%s' "$PROMPT" | claude --strict-mcp-config -p)"

if [[ "$SECTION" == "EMPTY" ]]; then
  if [[ "$REPLACE_TOP" -eq 1 ]]; then
    SECTION="$EXISTING_SECTION"
  else
    SECTION="## [${TODAY}]"$'\n\n'"No changes users will notice."
  fi
fi

if [[ "$SECTION" != "## [${TODAY}]"* ]]; then
  if ! NORMALIZED_SECTION="$(printf '%s\n' "$SECTION" | awk -v header="## [${TODAY}]" '
    $0 == header { found = 1 }
    found { print }
    END { if (!found) exit 1 }
  ')"; then
    echo "error: unexpected writer output (expected a section starting with '## [${TODAY}]'):" >&2
    printf '%s\n' "$SECTION" >&2
    exit 1
  fi
  SECTION="$NORMALIZED_SECTION"
fi

if [[ -n "$EXISTING_MARKERS" ]]; then
  SECTION+=$'\n\n'"$EXISTING_MARKERS"
fi
SECTION+=$'\n\n'"$MARKER"

SECTION="$SECTION" REPLACE_TOP="$REPLACE_TOP" python3 - <<'PY'
import os, re

section = os.environ["SECTION"].strip()
replace_top = os.environ["REPLACE_TOP"] == "1"

with open("CHANGELOG.md") as fh:
    content = fh.read()

m = re.search(r"(?m)^## \[", content)
if not m:
    raise SystemExit("CHANGELOG.md has no dated sections; refusing to guess")

head, rest = content[: m.start()], content[m.start() :]
if replace_top:
    # Drop the existing top section up to (and including) its trailing --- line.
    parts = rest.split("\n---\n", 1)
    rest = parts[1].lstrip("\n") if len(parts) > 1 else ""

with open("CHANGELOG.md", "w") as fh:
    fh.write(head + section + "\n\n---\n\n" + rest)
PY

git rm -rq --ignore-unmatch changelog.d
rm -rf changelog.d
git add CHANGELOG.md

echo
echo "compiled the commits since the last release into CHANGELOG.md section [${TODAY}]"
echo "next: review the section (git diff --cached), fix copy where it misses,"
echo "commit, and open a PR"
