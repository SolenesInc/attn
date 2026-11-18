# Sourced by a whole run's world.sh: attn at the parent of 6031f9a5c, which priced Claude fast mode (seed s-7zp490),
# fetched alone so the change and this eval's cases are unreachable, with a local bare origin for scripts/land.sh.
# Its test files are the hidden grader, read from that commit when grading.
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"
init_repository
git fetch -q --no-tags "$EVAL_REPOSITORY" "$(git -C "$EVAL_REPOSITORY" rev-parse 6031f9a5c~1)"
git checkout -q -B main FETCH_HEAD
git init -q --bare ../origin.git
git remote add origin "$(cd .. && pwd)/origin.git"
git push -q origin main
git fetch -q origin
git branch -q -u origin/main

plant request "Price Claude fast mode in session cost" <<'BODY'
Session cost should price Claude responses served in fast mode at fast rates. Today every Claude response is priced at the standard card, so a `/fast` session shows a cost too low.

Claude Code records the speed on each assistant message's usage: `message.usage.speed` is `"standard"` or `"fast"`.

Fast rates, per million input / output tokens:
- Claude Fable 5.2: $16 / $80.
- Claude Opus 5.5: $8 / $40.
- Claude Opus 5 and Opus 4.8: $10 / $50.
- Claude Opus 4.6: $30 / $150.

Prompt caching multipliers apply on top of fast mode pricing, so cache reads and writes scale with the fast rate.

Add a changelog entry.
BODY
