---
name: improve-workflow
description: Use when asked to run the improvement cycle for attn-workflow and the agent guidance around it, or to judge the last cycle's change.
---

# Improve workflow

A cycle reads what delegated agents did in real work since the last cycle,
judges the last cycle's change, and proposes one new change. The plot
`improve-attn-workflow-record-real-work` (`s-4z5bcq`) explains why: its terms,
decisions D2, D6 and D7, and its Behavior diagram. Run a cycle every two weeks.
You are done when the last cycle's seed is harvested, the user has the proposal
in an ask, and this cycle's seed holds the proposal, the scorecard and the
escapes it read.

Run every command from a clone of attn at the latest `origin/main`, and write
the files below to a scratch directory, not the clone. The scripts read the
sessions of every host through the attn CLI, so any connected host serves.

0. Find the last cycle. `attn seed ls --plot s-4z5bcq` lists the plot's
   children, and the newest one titled `Workflow cycle <date>` is the last
   cycle. Call its date `<last>`, today `<today>`, and the same span before
   `<last>` `<before>`. Its body names the change, what the scorecard should
   show, the window and the pstack commit it read; its notes name the landed
   commit. The first cycle has no such
   seed: its change is `builders-load-skill` (`s-7ty0ca`), its expectation is
   the note on that seed, `<last>` is the day that change reached the hosts,
   and its pstack commit is `2f87c85` (pstack 0.15.43, adopted by
   `s-mfx304`).
1. Run the scorecard. `collect` first, so the history file holds every
   session that has closed:

   ```sh
   node scripts/workflow-scorecard.mjs collect
   node scripts/workflow-scorecard.mjs --since <before> --until <last>  > scorecard-before.txt
   node scripts/workflow-scorecard.mjs --since <last> --until <today>   > scorecard-after.txt
   node scripts/workflow-scorecard.mjs --since <before> --until <today> --by skill > scorecard-by-skill.txt
   ```

   A change reaches agents when production is reinstalled on each host, not
   when it lands, so the window starts there. `--by skill` dates a change to
   the attn-workflow text: its table starts on the first day a session loaded
   the new text. In those tables the load rate is 100% by construction; read
   the load rate from the period tables. For a change elsewhere, such as a
   role instruction or another skill, `attn update status --json` on each
   host names the installed commit, and
   `git merge-base --is-ancestor <landed> <commit>` says whether it holds the
   change. When the window started later than `<last>`, run the after table
   from that day.
2. Judge the last change. Compare each number the last cycle expected with
   the after table, and with the before table for context. Harvest its seed:
   `attn seed harvest <last cycle> -m "<expected, measured, kept or reverted>"`,
   after a note with the numbers. If the change did
   not move what it should have, say whether to revert it in this cycle's
   proposal. For the first cycle, write the judgment as a note on
   `builders-load-skill` instead, and harvest nothing.
3. Read the escapes. An escape is a P0 or P1 defect found in work a Builder
   reported done: a `finding:` line from review on main, a rejected check, or
   a red `main` that the Sheriff traced to a commit.

   ```sh
   node scripts/workflow-scorecard.mjs escapes --since <last> --until <today> > escapes.txt
   wc -c escapes.txt
   ```

   The garden keeps a seed's notes for 30 days, so a cycle that runs more
   than four weeks after `<last>` loses escapes; the output says from which
   day it still has them. Give `escapes.txt` to one subagent of your harness
   on a cheap model, such as Sonnet, with this task: group the escapes by
   what would have caught each before it landed (a test kind, a review step,
   a check, a rule an agent skipped), count each group, quote one example per
   group, and name the largest. Keep the reading under about 400,000 tokens:
   two weeks measured 410 KB on 18 November, about 105,000 tokens. Over
   1.6 MB, cut the Sheriff notes from the file, give the reader the rest, and
   record the size on this cycle's seed.
4. Read what pstack changed. pstack is the workflow attn-workflow adopted
   rules from, in folder `pstack` of `cursor/plugins`.

   ```sh
   git -C ~/projects/plugins pull --ff-only || git clone https://github.com/cursor/plugins ~/projects/plugins
   git -C ~/projects/plugins log --oneline <pstack commit>..HEAD -- pstack
   git -C ~/projects/plugins diff <pstack commit>..HEAD -- pstack/skills pstack/agents pstack/docs
   ```

   List each change that attn-workflow lacks and that would have caught an
   escape group, or improves a number the scorecard reads. Leave out changes
   attn already has and changes that serve only Cursor.
5. Choose one change. Take it from the largest escape group, a number that
   fell, or the pstack list. Prefer a check, a script or a rule at the point
   of use over more prose an agent might skip. Write:
   - the exact text or diff, and the file it lands in;
   - what the scorecard should show afterwards, as numbers, and which table
     shows them;
   - the window: from when the change reaches the hosts to the next cycle.

   One change per cycle, so its effect can be told apart (D2). To test the
   change before it lands, you can run
   `node scripts/workflow-eval/run.mjs --base origin/main --candidate <ref> --only <case,...> --runs <n>`
   on the cases in `scripts/workflow-eval/cases` that cover it. Estimate its
   cost first: two arms, times the runs, times each case's mean `cost_usd` in
   `workflow-eval-runs.jsonl` in the plot's artifact folder, or its
   `limits.usd` when it has no runs yet. Ask the user before a run estimated
   above $30.
6. Plant this cycle's seed and ask the user.

   ```sh
   attn seed plant "Workflow cycle <today>" --part-of s-4z5bcq -m "<body>"
   ```

   The body is a work prompt for the Builder who lands the change: the
   change and its file, what the scorecard should show and the window, the
   escape groups with their counts, the pstack list, `pstack: <HEAD short
   sha>`, and the landing with `land-on-main`. End it with: "When the change
   has landed, note the landed commit on this seed and park it with
   `attn seed park`. Do not harvest it: the next cycle harvests it with what
   the scorecard showed." Tend it, and attach
   `scorecard-before.txt`, `scorecard-after.txt`, `scorecard-by-skill.txt` and
   `escapes.txt` with `attn seed attach <this cycle> --path <file> --move`.
   Then ask the user to approve the change with
   `attn ask --seed <this cycle>`, quoting the exact text. When they approve,
   delegate a Builder on the seed. Do not land it yourself.

## Gotchas

- `collect` prints how many closed sessions it could not read. Those whose
  transcript is gone stay unread for good; the rest are retried next run.
- A cycle that skips a period leaves no seed for it. The next cycle measures
  from the last seed's date, whatever the gap.
- The history file and the eval's runs live in the artifact folder of
  `s-4z5bcq`. Only `collect` and `run.mjs` write them.
