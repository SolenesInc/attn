---
name: audit-feature-map
description: Use when running the recurring feature-map audit, the pass that drives every way in drive-attn's feature map lists on a fresh instance, fixes the map where it drifted, and reports each one's result.
---

# Audit the feature map

The drive-attn feature map (`.agents/skills/drive-attn/features`) tells
agents how to reach and drive each attn feature. A change keeps the files it
touches current; this audit catches the rest: a commit made without the map, a
dependency or environment that changed on its own, a way in no file lists. The
audit automation starts the agent that runs it. No other agent owes a pass over
the whole map.

A pass audits one pinned revision of `main`. It is done when:

- its report has one row per door, `audit.py check` accepts it, and it is
  attached to your stake;
- the map drift it proved is landed as map fixes, or on a seed;
- each regression, harness failure and unmet precondition is on a seed,
  reused when one already exists;
- every instance it started is cleaned and its work checkout removed;
- your stake is harvested with the pass's tally.

A door is one bullet under a feature file's `## How to get to it`, with the
contexts its file lists. `audit.py doors` numbers them, such as
`garden.agents.2`.

## Rules

- drive-attn's rules hold throughout: production stays out of every run, and
  only `attn instance clean` stops what an instance runs. The `attn` on `PATH`
  is for your stake and seeds alone.
- You change only map prose: `.agents/skills/drive-attn/features/*.md` and the
  driving instructions in `.agents/skills/drive-attn/SKILL.md`. Product code,
  tests, harnesses, scripts, this skill and the automation go on seeds.
- When the map and attn disagree, find which is wrong before writing: the
  commit that changed the behavior, its seed, `docs/guide`, the glossary. Fix
  the map when the new behavior is intended. Otherwise it is a regression:
  report it and leave the map saying what attn should do. When intent stays
  unclear, ask with `attn ask`, mark the row `ambiguous`, and drive other
  doors meanwhile.
- Never weaken a recipe to make it pass, and never count a door as driven
  through another door. Only success observed through the door itself is a
  `pass`.
- On this shared Mac, use only drive-attn's focus-free input. A door that
  needs activation, system-wide keys, Finder, a dedicated display, an outpost
  peer, Pi or the Guardian, a local ingest service, or a real external service
  is `blocked`, naming that precondition. Never turn on a real service to
  get a pass.
- One pass at a time, one instance driving at a time.
- Do not dispatch fixers or message authors. Review main, the sheriff and CI
  keep their jobs; reuse their evidence only for the exact claim it makes.

## Outcomes

| Outcome | When | Evidence | Follow-up |
| --- | --- | --- | --- |
| `pass` | Driven through the door; attn did what the file says | the action, its output and the state read back | — |
| `fixed` | The file was wrong, attn is right; the corrected recipe then passed | as `pass`, plus what the old text did | map commit |
| `drift` | The file is wrong and you could not correct it this pass | what the file says and what attn does | seed |
| `regression` | attn breaks intended behavior | as `drift`, plus why the behavior is intended | seed |
| `harness` | `bridge`, `wsctl`, the mock agent or a scenario failed, not attn | the failing command and output | seed |
| `blocked` | A precondition the baseline lacks | the command tried and the missing precondition | seed if one exists |
| `ambiguous` | Intent unclear | what each source says | ask id |

Evidence is a path under the pass's evidence folder or a seed id, never only
your account of it.

## Stake and pass notes

Your stake is the seed the automation's opening names. It is the same seed
every pass; the next occurrence replants it after a harvest. Pass progress
lives in its notes, written as it happens, so a fresh session can resume:

```text
pass <pass> start: revision <sha>, previous <sha|none>, folder <runs>/<pass>, usage <tokens and cost>
pass <pass> instance <name>            before write-env.sh
pass <pass> feature <id>: <tally>      once its rows are in the report
pass <pass> cleaned <name>             once clean.sh succeeds
pass <pass> done: <tally>              once the report is attached
```

`<pass>` is the start time, `date +%Y%m%d-%H%M`. Use
`attn seed note <stake> -m "..."`.

## Start

The automation's folder is the control checkout. It holds no work.

1. Run `git pull --ff-only`. If this file changed, read it again.
2. Read `attn seed notes <stake>`. When the newest `pass … start` has no
   `done`, that pass is unfinished: [Recover](#recover) it. An occurrence
   accepted while a pass is unfinished is answered by finishing that pass.
3. Otherwise start a pass. This writes the pass's env file, which later
   commands source:

```bash
pass=$(date +%Y%m%d-%H%M) runs=$PWD.runs/$pass && mkdir -p $runs/evidence &&
git worktree add --detach $runs/work origin/main && cat > $runs/pass.env <<EOF
control=$PWD pass=$pass runs=$runs rev=$(git rev-parse origin/main)
S=$runs/work/.agents/skills/audit-feature-map/scripts/audit.py
cd $runs/work
EOF
echo $runs
```

Each command after this starts in a fresh shell, so it begins with
`. <runs>/pass.env &&`, `<runs>` written out; an instance command adds
drive-attn's prefix after it. `previous` is the revision of the last `done`
pass, or `none`. Usage is the `usage` line of `attn session show <session>`,
where `attn presence --session-id` prints your session. Write the start note
before anything else.

## Prepare

Write the report's table and read what moved:

```bash
. <runs>/pass.env && $S doors > $runs/evidence/rows.md && $S sources > $runs/evidence/sources.txt
. <runs>/pass.env && git diff --stat <previous>..HEAD -- cmd/attn app/src/shortcuts app/src/application internal/prompts/content > $runs/evidence/entry-points.txt
```

`sources` lists, per feature, the commits to the sources its doors name since
its file last changed. Read each one that could add, remove or change a way in
before you drive that feature. `entry-points.txt` shows changes since the
previous pass in places a new way in appears; skip it on the first pass and say
so in the report. A way in attn has and no file lists is `drift` against the
feature that owns it: add it to that file, as a new bullet and a recipe step,
and drive it; its row follows when you rerun `doors` after the fix.

Build and launch with drive-attn's Launch, measuring each setup command:

```bash
. <runs>/pass.env && /usr/bin/time -p make build > $runs/evidence/build.log 2>&1
```

Its last three lines are wall, user and system seconds for the command and the
children it waited for. Do the same for `make install INSTANCE=<name>` and for
cleanup. The instance is `mapaudit-<MMDD>`, with a letter after it for a
second one. Record each instance note before `write-env.sh`. Run the doctor
before the first action.

## Drive

Take the features in `rows.md` order. For each one:

1. Read its file whole, and the commits `sources.txt` lists for it.
2. Drive every door through its file's recipe, as drive-attn's Evidence says,
   into `$runs/evidence/<feature>/`. Start from the baseline as the README's
   Runs section says; when the instance can no longer give one, clean it and
   start another.
3. When a step fails, read the source and the commits before deciding. Retry
   a step whose own deadline expired once, then classify it.
4. Fill the feature's rows in `rows.md`: outcome, evidence and follow-up.
5. Write the `feature` note.

A native scenario waits for the shared display lock. Record when it waited, so
the report keeps waiting apart from driving.

## Repair the map

Commit map fixes in the work checkout on a branch, `git switch -c
map-audit-$pass`, as you prove them. Land them once, after cleanup, so a
rebase never changes the source under a live instance:

1. `$S scope origin/main` must print `map-only`. When it refuses, find the
   change that is not map prose and remove it, keeping edits that are not
   yours.
2. Run the `verify-docs` skill on the diff and fix its findings.
3. Land with the `land-on-main` skill. When the rebase conflicts with someone
   else's edit to the same file, keep their edit and reapply yours.

## Seeds

Search before planting: `attn seed search <words>`. Reuse a seed that already
names the failure with `attn seed note <seed> -m "<pass, door, evidence>"`.
Otherwise plant one with `--discovered-from <stake>`, its body a work prompt
with the door, the commands, the output and what would fix it. Plant one seed
per cause, not per door.

## Report

Write `$runs/evidence/report.md`:

```text
# Feature map audit <pass>

Revision <sha>, previous <sha|none>. Machine <model, cores, memory>.
Started <time>, finished <time>. Instances <names>.
<n> doors: <tally by outcome>. Not a complete sweep when anything is blocked.

## Cost
setup: build, install (wall s, user+sys s each)
display queue: <total wait, or none>
driving: <wall from the first to the last feature note>
cleanup: <wall s, user+sys s>
agent usage: <usage at done minus at start, or unavailable>
CPU covers commands and their waited-for children only; the app, daemon and
workers run detached and are not counted.

## Map changes
<landed commits>

## Seeds
<seed: doors, new or reused>

## Coverage gaps
<each door that is not pass or fixed: door, outcome, reason>

## Cleanup
<instance: clean.sh result>; work checkout removed; residual paths and size

## Doors
<the table from rows.md>
```

`$S check $runs/evidence/report.md` must pass before you attach it.

## Clean up

Every pass ends here, finished or abandoned. For each instance, run
`. <runs>/pass.env && .agents/skills/drive-attn/scripts/clean.sh <name>` and
write the `cleaned` note.
When clean fails, stop launching, keep its output and the instance's env file,
and ask the user with the instance name and the exact error. Then land, attach
and remove the work checkout:

```bash
. <runs>/pass.env &&
tar -czf $runs/evidence-$pass.tar.gz -C $runs evidence &&
attn seed attach <stake> --path $runs/evidence/report.md --copy &&
attn seed attach <stake> --path $runs/evidence-$pass.tar.gz --move &&
git -C $control worktree remove --force $runs/work &&
git -C $control branch -D map-audit-$pass
```

`clean` keeps `~/attn-notebook-<name>`. List each one in the report with its
size and leave it in place. Write the `done` note, then `attn seed harvest
<stake> -m "pass <pass> at <sha>: <tally>; report attached"`.

## Recover

The notes of the unfinished pass name its revision, folder, instances and
finished features.

1. Clean each instance without a `cleaned` note, as in [Clean up](#clean-up).
   When its env file is gone, run
   `. <runs>/pass.env && ./attn instance clean <name>`. Launch nothing until every one is clean.
2. When the work checkout is gone, add it again at the recorded revision.
   When a finished feature's evidence is gone, drive it again and say so in
   its note.
3. Continue with the first feature without a `feature` note, on a new
   instance, and keep the pass's revision and folder.

A pass is never replaced. To abandon one, clean it, attach what it has, and
write `pass <pass> done: abandoned, <reason>` before starting the next.

## Install

The automation runs this skill from a control checkout of its own, made with
`git clone`, never the user's working checkout. Its definition, written with
the control checkout's path and the launch choices of the Builder role in
`attn delegate roles`:

```yaml
api_version: attn.dev/automations/v1alpha1
name: Feature map audit
trigger:
  type: manual
prompt: In this folder, run git pull --ff-only, then read .agents/skills/audit-feature-map/SKILL.md and run a feature-map audit pass.
launch:
  driver: <harness>
  model: <model>
  effort: <effort>
location:
  type: directory
  path: <control checkout>
```

Stage it with `attn automation apply --file <file> --launch-desktop own
--desktop-name "Feature map audit"`, run it with `attn automation run <id>`,
and read the report. Then `attn automation disable <id>`, and apply it again
with `id: <id>` and the scheduled trigger:

```yaml
trigger:
  type: scheduled
  schedule:
    cron: <cron>
    time_zone: Europe/Stockholm
  continuity: singleton
  catch_up: latest
```

`attn automation show <id>` keeps it disabled until `attn automation enable
<id>`. It has no guard: a pass is due even when `main` has not moved.
