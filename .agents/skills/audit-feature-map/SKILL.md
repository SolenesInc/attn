---
name: audit-feature-map
description: Use only when the feature-map audit automation starts you, or the user asks for the audit. A pass drives every door of drive-attn's feature map on a fresh instance, repairs the map's prose where it drifted, and reports each door's result.
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
- each regression and harness failure is on a seed, reused when one already
  exists, and each blocked door names the seed that carries its cause when
  one does;
- every instance it started is cleaned and its checkouts removed;
- your stake is harvested with the pass's tally.

A door is one bullet under a feature file's `## How to get to it`.
`audit.py doors` numbers them, such as `garden.agents.2`. A bullet that names
several ways in, such as verbs, palette commands or controls, is `pass` only
when you drove each one; otherwise it takes the worst outcome among them and
names the one that failed. A bullet that names no way in, such as "Nothing in
the app shows …" or "Where people see what agents do: …", is checked against
what the recipe showed: `pass` with the step that showed it, or `drift`. A
file's `## Contexts` are not doors; drive the ones its recipe drives.

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
  needs activation, system-wide keys, the user's clipboard, Finder, a
  dedicated display, an outpost peer, Pi or the Guardian, a local ingest
  service, or a real external service is `blocked`, naming that
  precondition. Never turn on a real service to get a pass.
- One pass at a time, one instance driving at a time.
- Do not dispatch fixers or message authors. Review main, the sheriff and CI
  keep their jobs; reuse their evidence only for the exact claim it makes.

## Outcomes

| Outcome | When | Evidence | Follow-up |
| --- | --- | --- | --- |
| `pass` | Driven through the door; attn did what the file says | the action, its output and the state read back | — |
| `fixed` | The file was wrong, attn is right; the corrected recipe then passed | as `pass`, plus what the old text did | the map commit, as landed on main |
| `drift` | The file is wrong and you could not correct it this pass | what the file says and what attn does | seed |
| `regression` | attn breaks intended behavior | as `drift`, plus why the behavior is intended | seed |
| `harness` | `bridge`, `wsctl`, the mock agent or a scenario failed, not attn | the failing command and output | seed |
| `blocked` | A precondition the baseline lacks | the recipe's command with its output, or the command alone when running it would break a rule here; and the missing precondition | the seed that carries the cause, when one does |
| `ambiguous` | Intent unclear | what each source says | ask id |

Evidence is a path under the pass's evidence folder or a seed id, never only
your account of it. Write a `|` inside a cell as `\|`.

## Stake and pass notes

Your stake is the seed the automation's opening names. Under the scheduled
trigger it is the same seed every pass, and the next occurrence replants it
after a harvest. A manual run plants a fresh stake, so it has no earlier pass
to recover and `previous` is `none`. Pass progress lives in the stake's notes,
written as it happens, so a fresh session can resume:

```text
pass <pass> start: revision <sha>, previous <sha|none>, folder <runs>, usage <tokens and cost>
pass <pass> instance <name>            before write-env.sh
pass <pass> feature <id>: <tally>, <minutes> min driving   once its rows are in the report
pass <pass> cleaned <name>             once clean.sh succeeds
pass <pass> done: <tally>              once the report is attached
```

`<pass>` is the start time, `date +%Y%m%d-%H%M`. Use
`attn seed note <stake> -m "..."` from a shell without drive-attn's prefix:
the prefix points `attn` at the instance, where the stake does not exist.

## Start

The automation's folder is the control checkout. It holds no work.

1. Run `git pull --ff-only`. If this file changed, read it again.
2. Read `attn seed notes <stake>`. When the newest `pass … start` has no
   `done`, that pass is unfinished: [Recover](#recover) it. An occurrence
   accepted while a pass is unfinished is answered by finishing that pass.
3. Otherwise start a pass. This writes the pass's env file, which later
   commands source:

```bash
pass=$(date +%Y%m%d-%H%M) runs=$PWD.runs/$pass rev=$(git rev-parse origin/main) &&
mkdir -p $runs/evidence && git worktree add --detach $runs/work $rev &&
git worktree add -b map-audit-$pass $runs/repair $rev && cat > $runs/pass.env <<EOF
control=$PWD pass=$pass runs=$runs rev=$rev repair=$runs/repair
S=$PWD/.agents/skills/audit-feature-map/scripts/audit.py
cd $runs/work
EOF
echo $runs
```

The work checkout is what you build and drive. It never changes during the
pass: the instance must run the source it was built from, and `bridge` refuses
an app whose source moved. Map fixes go in the repair checkout, `$repair`.
`$S` is the control checkout's `audit.py`, which acts on the checkout you run
it in. The control checkout's skill governs a resumed pass too; the pass keeps
its revision.

Each command after this starts in a fresh shell, so it begins with
`. <runs>/pass.env &&`, `<runs>` written out; an instance command adds
drive-attn's prefix after it. `previous` is the revision of the newest pass
that finished with a report, not an abandoned one, or `none`. Usage is the `usage` line of `attn session show <session>`,
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
feature that owns it: add it to that file in `$repair`, as a new bullet and a
recipe step, and drive it. The table keeps the pinned revision's doors, so the
new door goes in the report's Map changes with its evidence, not in a row.

Build and launch with drive-attn's Launch, measuring each setup command:

```bash
. <runs>/pass.env && /usr/bin/time -p make build > $runs/evidence/build.log 2>&1
```

Its last three lines are wall, user and system seconds for the command and the
children it waited for. Do the same for `make install INSTANCE=<name>` and for
cleanup. The instance is `ma${pass#20}`, such as `ma261031-1214`, unique to the
pass so no earlier pass's notebook becomes its baseline; a replacement within
the pass adds a letter, `b` and on, after the names its notes already list. Record each instance note before `write-env.sh`. Run the doctor
before the first action.

A setup command that fails because of the machine, such as a shared cache
removed under it or a full disk, is no door's outcome. Keep its log under a
new name, find the cause, retry once it is gone, and name it in the report.

## Drive

Take the features in `rows.md` order. Drive each one in its own subagent,
one at a time, so each starts with an empty context; your own context keeps
only what they return. Give the subagent the path of `<runs>/pass.env`, the
instance name, the feature id, its rows from `rows.md`, its lines from
`sources.txt`, the Rules and Outcomes sections of this file, and steps 1–4.
Tell it that the instance keeps what earlier features left on it, and that a
step the permissions deny is `blocked`, naming the denial, never worked
around.

1. Read the feature's file whole, and the commits `sources.txt` lists for it.
2. Drive every door through its file's recipe, as drive-attn's Evidence says,
   with `$runs/evidence/<feature>` as the recipe's proof folder `P`, keeping
   each command's output there. Start from the baseline as the README's Runs
   section says.
3. When a step fails, read the source and the commits before deciding. Retry
   a step whose own deadline expired once, then classify it.
4. Commit each map fix in `$repair`, and return the feature's filled rows
   (outcome, evidence and follow-up), its minutes of driving, and whether the
   instance can still give a baseline.

Then fill the rows in `rows.md` and write the `feature` note. When the
instance can no longer give a baseline, clean it and start another before the
next feature.

A native scenario waits for the shared display lock. Record when it waited, so
the report keeps waiting apart from driving.

## Clean up

When the last feature is driven, and in a pass you abandon, clean every
instance the pass started:

```bash
. <runs>/pass.env && /usr/bin/time -p .agents/skills/drive-attn/scripts/clean.sh <name> > $runs/evidence/clean-<name>.log 2>&1
```

Write the `cleaned` note after each. When clean fails, stop launching, keep
its output and the instance's env file, and ask the user with the instance
name and the exact error.

`clean` keeps `~/attn-notebook-<name>` when the instance made one. The pass
made everything in it, so after a clean that succeeded, record its size and
remove it:
`du -sh ~/attn-notebook-<name> >> $runs/evidence/clean-<name>.log && rm -r ~/attn-notebook-<name>`.

## Repair the map

Edit and commit map fixes in `$repair`, on its branch `map-audit-$pass`, as
you prove them. Land them after cleanup, before you write the report:

1. `. <runs>/pass.env && cd $repair && $S scope origin/main` must print
   `map-only` and name the repair checkout. `nothing to land` means the pass
   made no fixes: skip the rest. When it refuses other paths, find the change
   that is not map prose and remove it.
2. Land with the `land-on-main` skill, from `$repair`:
   `. <runs>/pass.env && cd $repair && scripts/land.sh`. Its review for map
   prose is `verify-docs`. Stop at its push: the pass reports once, in Report,
   and harvests the stake in Finish. It landed only when `land.sh` printed
   `land: main is at <sha>`. When the rebase conflicts with someone else's
   edit to the same file, keep their edit and reapply yours where the drift
   remains. Landing rebases your commits, so the hashes in `rows.md` are not
   on main: `. <runs>/pass.env && $S relink $runs/evidence/rows.md $rev`
   replaces each with the landed commit of the same subject. When it cannot
   land, attach `git -C $repair format-patch origin/main --stdout` to a seed
   for the drift, then `git -C $repair reset --hard origin/main`, remove the
   map commits from the rows, and turn the `fixed` rows into `drift` on that
   seed.

## Seeds

Search before planting: `attn seed search <words>`. Reuse a seed that already
names the failure with `attn seed note <seed> -m "<pass, door, evidence>"`.
Otherwise plant one with `--discovered-from <stake>`, its body a work prompt
with the door, the commands, the output and what would fix it. Plant one seed
per cause, not per door.

## Report

Write `$runs/evidence/report-$pass.md`:

```text
# Feature map audit <pass>

Revision <sha>, previous <sha|none>. Machine <model, cores, memory>.
Started <time>, finished <time>. Instances <names>.
<n> doors: <tally by outcome>. Not a complete sweep when anything is blocked.

## Cost
setup: build, install (wall s, user+sys s each)
display queue: <total wait, or none>
driving: <sum of the feature notes' minutes>
cleanup: <wall s, user+sys s>
agent usage: <usage at done minus at start, or unavailable>
CPU covers commands and their waited-for children only; the app, daemon and
workers run detached and are not counted.

## Map changes
<landed commits, and each way in this pass added with its result>

## Seeds
<seed: doors, new or reused>

## Coverage gaps
<the output of `$S gaps` on this report>

## Cleanup
<instance: clean.sh result, notebook folder removed and its size>; anything else left behind

## Doors
<the table from rows.md>
```

`$S check $runs/evidence/report-$pass.md` must pass before you attach it. It
refuses a `fixed` row without a commit on `origin/main`, and a row that cites
a commit not on it.

## Finish

Attach the report and the evidence, then remove the checkouts and the pass's
folder; the seed keeps the evidence. The first line refuses while the repair
branch holds commits `main` lacks:

```bash
. <runs>/pass.env && git -C $repair fetch -q origin &&
test -z "$(git -C $repair rev-list origin/main..HEAD)" &&
tar -czf $runs/evidence-$pass.tar.gz -C $runs evidence &&
attn seed attach <stake> --path $runs/evidence/report-$pass.md --copy &&
attn seed attach <stake> --path $runs/evidence-$pass.tar.gz --move &&
git -C $control worktree remove --force $runs/work &&
git -C $control worktree remove --force $repair &&
git -C $control branch -D map-audit-$pass &&
rm -r $runs
```

Write the `done` note, then `attn seed harvest <stake> -m "pass <pass> at
<sha>: <tally>; report attached"`.

## Recover

The notes of the unfinished pass name its revision, folder, instances and
finished features.

1. When the work or repair checkout is gone, add it again at the recorded
   revision, and the repair checkout on its branch when the branch survives.
   When a finished feature's evidence is gone, drive it again and say so in
   its note.
2. A recipe that started a background process kept its PID in the instance's
   env file, such as `SRV`. For each one, stop it only when `ps -p <pid> -o
   command=` still shows the command the recipe started; otherwise leave it
   and name it in your ask.
3. Clean each instance without a `cleaned` note, as in [Clean up](#clean-up).
   When its env file is gone, write it again first with
   `. <runs>/pass.env && .agents/skills/drive-attn/scripts/write-env.sh <name>`,
   after `make build` when `./attn` is missing. Launch nothing until every
   one is clean.
4. Continue with the first feature without a `feature` note, on a new
   instance, and keep the pass's revision and folder.

A pass is never replaced. To abandon one, clean it, attach what it has, remove
its checkouts and folder as [Finish](#finish) does, and write
`pass <pass> done: abandoned, <reason>` before starting the next.

## Install

Only the user installs the automation; a pass skips this section. The
automation runs this skill from a control checkout of its own, made with
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

The harness must already trust the control checkout: an unattended Claude
Code run stops at its folder trust prompt, while attn still reads it as
working. Start the harness once in that folder, accept the prompt and quit.

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

Applying it again keeps it disabled: the definition prints with
`"enabled": false` until `attn automation enable <id>`. It has no guard: a
pass is due even when `main` has not moved.
