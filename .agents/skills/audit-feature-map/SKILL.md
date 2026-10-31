---
name: audit-feature-map
description:
  Use only when the feature-map audit automation starts you, or the user asks
  for the audit.
---

# Audit the feature map

The drive-attn feature map (`.agents/skills/drive-attn/features`) describes how
to reach and drive each attn feature. Whoever changes a feature should update
its map file. This audit finds the map files they missed.

Each run of the audit is a pass. In a pass, you pin one revision of `main`,
drive every entrypoint in the feature map on a fresh instance built from that
revision, and record each result as one of the [Outcomes](#outcomes). The pass
is done when:

- The report has one row per entrypoint, passes `audit.py check`, and is
  attached to your stake.
- Every map error you proved is fixed on `main` (`fixed`), or recorded on a
  seed when the fix could not land (`drift`).
- Every attn bug (`regression`) and every broken driving tool (`harness`) is on
  a seed. When a seed for the same problem already existed, you added a note to
  it instead of planting a new one.
- Every `blocked` row links the seed that explains why it was blocked, when
  such a seed exists.
- You cleaned every instance you started and removed the pass's worktrees.
- You harvested your stake with the tally: how many rows got each outcome.

## Entrypoints

An entrypoint is one bullet under `## How to get to it` in a feature file.
`audit.py entrypoints` numbers them, for example `garden.agents.2`, and the
report has one row for each.

Some bullets list several ways to reach the feature, such as CLI verbs, palette
commands or controls. Mark such a bullet `pass` only when you drove every one of
them. Otherwise give it the worst outcome among them, and say which one failed.

Some bullets say where a feature shows up instead of how to reach it, such as
"Nothing in the app shows …" or "Where people see what agents do: …". Check
those against what the recipe showed you. Mark the row `pass` and cite the
recipe step that showed it, or mark it `drift`.

A feature file's `## Contexts` are not entrypoints. Drive the contexts that the
file's recipe drives.

## Rules

- Follow drive-attn's rules throughout. Keep production out of every run, and
  stop what an instance runs only with `attn instance clean`. Use the `attn` on
  `PATH` only for your stake and seeds.
- Change only map prose: the feature files in
  `.agents/skills/drive-attn/features/*.md` and the driving instructions in
  `.agents/skills/drive-attn/SKILL.md`. When product code, tests, harnesses,
  scripts, this skill or the automation need a change, put it on a seed.
- When the map and attn disagree, find out which one is wrong before you write
  anything. Check the commit that changed the behavior, its seed, `docs/guide`
  and the glossary. If the new behavior is intended, fix the map. If it isn't,
  it's a regression: report it, and leave the map describing what attn should
  do. If you still can't tell what was intended, ask the user with `attn ask`,
  mark the row `ambiguous`, and drive other entrypoints while you wait.
- Never weaken a recipe to make it pass. Never count an entrypoint as driven
  because you drove a different one. Mark a row `pass` only when you saw that
  entrypoint itself succeed.
- On this shared Mac, use only drive-attn's focus-free input. Mark an
  entrypoint `blocked`, and name what it needs, when it needs any of these: app
  activation, system-wide keys, the user's clipboard, Finder, a dedicated
  display, an outpost peer, Pi or the Guardian, a local ingest service, or a
  real external service. Never turn on a real service to get a pass.
- When the permissions deny a step, mark the row `blocked` and name the denial.
  Never work around a denial.
- Run one pass at a time, and drive one instance at a time.
- Don't dispatch agents to fix what you find, and don't message commit authors.
  Review main, the sheriff and CI keep doing their own work. You can reuse their
  evidence, but only for the exact claim it supports.

## Outcomes

| Outcome      | When                                                                              | Evidence                                                                                                                  | Follow-up                                    |
| ------------ | --------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------- |
| `pass`       | You drove the entrypoint, and attn did what the file says                         | the action, its output and the state you read back                                                                        | —                                            |
| `fixed`      | The file was wrong and attn was right; you corrected the recipe and it then passed | same as `pass`, plus what the old text did                                                                                | the map commit, as landed on main            |
| `drift`      | The file is wrong and you couldn't correct it in this pass                        | what the file says and what attn does                                                                                     | seed                                         |
| `regression` | attn breaks intended behavior                                                     | same as `drift`, plus why the behavior is intended                                                                        | seed                                         |
| `harness`    | `bridge`, `wsctl`, the mock agent or a scenario failed, not attn                  | the failing command and its output                                                                                        | seed                                         |
| `blocked`    | The baseline lacks something the entrypoint needs, or the permissions denied a step | the recipe's command and its output, or only the command when running it would break a rule above; and what was missing | the seed that explains the cause, if one exists |
| `ambiguous`  | You can't tell which behavior is intended                                         | what each source says                                                                                                     | the ask id                                   |

Evidence is a path under the pass's evidence folder, or a seed id. Your own
description of what happened is not evidence on its own. Inside a table cell,
write `|` as `\|`.

## Stake and pass notes

Your stake is the seed named in the automation's opening message. Each run of
the automation starts a new agent with a new stake. Record a pass's progress as
notes on the stake of the run that started the pass, as each step happens. The
pass's `pass.env` names that stake, so a later run can read the notes and resume
the pass.

```text
pass <pass> start: revision <sha>, previous <sha|none>, folder <runs>, usage <tokens and cost>
pass <pass> instance <name>                                 before you run write-env.sh
pass <pass> feature <id>: <tally>, <minutes> min driving    after the feature's rows are in the report
pass <pass> cleaned <name>                                  after clean.sh succeeds
pass <pass> done: <tally>                                   after you attach the report
```

`<pass>` is the time the pass started, from `date +%Y%m%d-%H%M`. Write notes
with `attn seed note <stake> -m "..."` in a shell without drive-attn's prefix.
The prefix points `attn` at the instance, and the stake doesn't exist there.

## Start

The automation runs in its own clone, the control checkout. Never build, drive
or edit in it.

1. Run `git pull --ff-only`. If this file changed, read it again.
2. If `$PWD.runs` contains a pass folder, that pass is unfinished. Look for the
   session named in its `pass.env` in `attn agent list`.
   - If the session is listed, it still owns the pass, and it may be waiting on
     the user. Leave everything of that pass alone. Ask the user with
     `attn ask` to answer or close that session, naming the session and the
     pass's stake. Then end your turn with your stake still open, because an
     ask closes when its seed or its session closes. When the answer arrives,
     run Start again.
   - If the session is not listed, take the pass over as [Recover](#recover)
     says.
3. If there is no pass folder, start a pass. Fill in `<stake>`, and `<session>`
   from `attn presence --session-id`. The command creates the pass's checkouts
   and writes its env file, which every later command sources:

```bash
pass=$(date +%Y%m%d-%H%M) runs=$PWD.runs/$pass rev=$(git rev-parse origin/main) &&
mkdir -p $runs/evidence && git worktree add --detach $runs/work $rev &&
git worktree add -b map-audit-$pass $runs/repair $rev && cat > $runs/pass.env <<EOF
control=$PWD pass=$pass runs=$runs rev=$rev repair=$runs/repair stake=<stake> session=<session>
S=$PWD/.agents/skills/audit-feature-map/scripts/audit.py
cd $runs/work
EOF
echo $runs
```

The pass uses two checkouts of the pinned revision:

- The work checkout, `$runs/work`, is what you build and drive. Never change it
  during the pass. The instance must run the source it was built from, and
  `bridge` refuses to drive an app whose source changed.
- The repair checkout, `$repair`, is where you commit map fixes.

`$S` is the control checkout's `audit.py`. It works on whichever checkout you
run it in. When you resume a pass, follow the control checkout's current copy of
this skill, but keep the pass's original revision.

Run every later command in a fresh shell, starting with `. <runs>/pass.env &&`
with the `<runs>` path written out. For a command against the instance, add
drive-attn's prefix after that.

Write the start note before anything else. `previous` is the revision in
`$PWD.runs/previous`, which Finish writes, or `none` on the first pass. `usage`
is the `usage` line of `attn session show <session>`.

## Prepare

Write the report's table, the commits to read, and the changes since the
previous pass:

```bash
. <runs>/pass.env && $S entrypoints > $runs/evidence/rows.md && $S sources > $runs/evidence/sources.txt
. <runs>/pass.env && git diff --stat <previous>..HEAD -- cmd/attn app/src/shortcuts app/src/application internal/prompts/content > $runs/evidence/entrypoint-changes.txt
```

`sources.txt` lists, for each feature, the commits that touched the sources its
entrypoints name since the feature's file last changed. Before you drive a
feature, read each of its commits that could add, remove or change a way to
reach it.

`entrypoint-changes.txt` lists changes since the previous pass in the places
where new ways to reach a feature usually appear. On the first pass there is no
previous revision: skip it, and say so in the report.

When attn has a way to reach a feature that no map file lists, that's `drift`
against the feature it belongs to. In `$repair`, add it to that feature's file
as a new bullet and a recipe step, then drive it. The report's table has only
the pinned revision's entrypoints, so record the new entrypoint and its
evidence under the report's Map changes, not as a row.

Build and launch the instance as drive-attn's Launch says, and time each setup
command:

```bash
. <runs>/pass.env && /usr/bin/time -p make build > $runs/evidence/build.log 2>&1
```

The last three lines of the log are wall, user and system seconds, counting the
command and the child processes it waited for. Time
`make install INSTANCE=<name>` and cleanup the same way.

Name the instance `ma${pass#20}`, for example `ma261031-1214`. The name is
unique to the pass, so the instance never starts from an earlier pass's
notebook. If you replace the instance during the pass, add the next letter to
the name, starting at `b`, skipping any letters the stake's notes already list.
Write the instance note before you run `write-env.sh`. Run the doctor before the
first action.

Sometimes a setup command fails because of the machine, for example when a
shared cache was deleted while it ran or the disk is full. That failure is not
an outcome for any entrypoint. Keep the log under a new name, find the cause,
retry once the cause is gone, and mention it in the report.

## Drive

Drive the features in the order of `rows.md`. Give each feature its own
subagent, one at a time, so each subagent starts with an empty context and your
context keeps only what the subagents return. Give each subagent:

- the path of `<runs>/pass.env` and the instance name;
- the feature id, its rows from `rows.md` and its lines from `sources.txt`;
- the Entrypoints, Rules and Outcomes sections of this file, and steps 1–4
  below.

Tell the subagent that the instance still has whatever earlier features left on
it, so it must remove what its recipe needs gone, as the Runs section of the
feature map README says.

1. Read the whole feature file, and the commits `sources.txt` lists for it.
2. Drive every entrypoint through the file's recipe, as drive-attn's Evidence
   section says. Use `$runs/evidence/<feature>` as the recipe's proof folder
   `P`, and keep each command's output there. Start from the baseline, as the
   README's Runs section says.
3. When a step fails, read the source and the commits before you decide the
   outcome. If the step failed because its own deadline expired, retry it once,
   then decide.
4. Commit each map fix in `$repair`. Return the feature's rows, each with its
   outcome and evidence. For `fixed`, add the map commit. For `drift`,
   `regression` and `harness`, add the cause with its commands and output, but
   don't plant a seed. Also return how many minutes you spent driving, how long
   you waited for the display lock, and whether the instance can still give a
   baseline.

When the subagent returns, plant or reuse seeds as [Seeds](#seeds) says, add
their ids to the rows in `rows.md`, and write the `feature` note. If the
instance can no longer give a baseline, clean it and start a new one before the
next feature.

Native scenarios wait for the shared display lock. Report that waiting time
separately from driving time.

## Clean up

After the last feature, or when you abandon a pass, clean every instance the
pass started:

```bash
. <runs>/pass.env && /usr/bin/time -p .agents/skills/drive-attn/scripts/clean.sh <name> > $runs/evidence/clean-<name>.log 2>&1
```

Write the `cleaned` note after each instance. If `clean.sh` fails, stop
launching instances, keep its output and the instance's env file, and ask the
user with `attn ask`, giving the instance name and the exact error.

`clean.sh` leaves `~/attn-notebook-<name>` behind when the instance created
one. Everything in it came from the pass, so once `clean.sh` succeeds, record
the folder's size and delete it:

```bash
if [ -d ~/attn-notebook-<name> ]; then du -sh ~/attn-notebook-<name> >> $runs/evidence/clean-<name>.log && rm -r ~/attn-notebook-<name>; fi
```

## Repair the map

Commit each map fix in `$repair`, on its branch `map-audit-$pass`, as soon as
you prove it. Land the fixes after cleanup and before you write the report:

1. Run `. <runs>/pass.env && cd $repair && $S scope origin/main`. It must print
   `map-only` and name the repair checkout. If it prints `nothing to land`, the
   pass made no fixes: skip the rest of this section. If it refuses because
   other paths changed, find the change that isn't map prose and remove it.
2. Land from `$repair` with the `land-on-main` skill:
   `. <runs>/pass.env && cd $repair && scripts/land.sh`. For map prose, the
   review step is `verify-docs`. Stop after the push: you report once, in
   [Report](#report), and harvest the stake in [Finish](#finish). The fixes
   landed only if `land.sh` printed `land: main is at <sha>`.
   - If the rebase conflicts with someone else's edit to the same file, keep
     their edit, and reapply yours where the map is still wrong.
   - Landing rebases your commits, so the hashes in `rows.md` are not the ones
     on `main`. Run `. <runs>/pass.env && $S relink $runs/evidence/rows.md $rev`
     to replace each hash with the landed commit that has the same subject.
   - If you can't land the fixes, attach
     `git -C $repair format-patch origin/main --stdout` to a seed for the drift,
     and run `git -C $repair reset --hard origin/main`. Then remove the map
     commits from the rows, and change the `fixed` rows to `drift` with that
     seed.

## Seeds

Before you plant a seed, search for an existing one with
`attn seed search <words>`. If a seed already describes the failure, add a note
to it with `attn seed note <seed> -m "<pass, entrypoint, evidence>"`. Otherwise
plant a new seed with `--discovered-from <stake>`, and write its body as a work
prompt: the entrypoint, the commands, their output, and what would fix it.
Plant one seed per cause, not one per entrypoint.

## Report

Write `$runs/evidence/report-$pass.md`:

```text
# Feature map audit <pass>

Revision <sha>, previous <sha|none>. Machine <model, cores, memory>.
Started <time>, finished <time>. Instances <names>.
<n> entrypoints: <tally by outcome>. <When any row is blocked: "Not a complete sweep.">

## Cost
setup: build, install (wall s, user+sys s each)
display queue: <total wait, or none>
driving: <sum of the feature notes' minutes>
cleanup: <wall s, user+sys s>
agent usage: <usage at done minus usage at start, or unavailable>
CPU time counts only the commands and the child processes they waited for. The
app, daemon and workers run detached, so their CPU time is not counted.

## Map changes
<landed commits, and each entrypoint this pass added with its result>

## Seeds
<seed: entrypoints, new or reused>

## Coverage gaps
<the output of `$S gaps` on this report>

## Cleanup
<instance: clean.sh result, the notebook folder's size when deleted, or none>; anything else left behind

## Entrypoints
<the table from rows.md>
```

Before you attach the report, run `$S check $runs/evidence/report-$pass.md`. It
must pass. It fails when a `fixed` row has no commit on `origin/main`, or when
any row cites a commit that isn't on `origin/main`.

## Finish

Attach the report and the evidence to your stake, then remove the checkouts and
the pass's folder. The stake keeps the evidence. The `test` line stops the
command when the repair branch has commits that `main` doesn't:

```bash
. <runs>/pass.env && git -C $repair fetch -q origin &&
test -z "$(git -C $repair rev-list origin/main..HEAD)" &&
tar -czf $runs/evidence-$pass.tar.gz -C $runs evidence &&
attn seed attach $stake --path $runs/evidence/report-$pass.md --copy &&
attn seed attach $stake --path $runs/evidence-$pass.tar.gz --move &&
git -C $control worktree remove --force $runs/work &&
git -C $control worktree remove --force $repair &&
git -C $control branch -D map-audit-$pass &&
rm -r $runs && echo $rev > $control.runs/previous
```

Write the `done` note, then harvest the stake with
`attn seed harvest $stake -m "pass <pass> at <sha>: <tally>; report attached"`.

## Recover

You recover a pass when its folder exists but its session has ended. The pass's
`pass.env` names its stake, and the stake's notes list its instances and the
features already driven. To take the pass over, set `session=` in `pass.env` to
your session and run `attn seed tend <stake>`. Write the pass's notes on that
stake, and harvest it in Finish. If your own stake is a different seed, harvest
it too, naming the pass you finished.

1. If the work or repair checkout is missing, add it again at the pass's
   revision. Put the repair checkout back on its branch if the branch still
   exists. If a finished feature's evidence is missing, drive that feature
   again and say so in its note.
2. When a recipe starts a background process, it saves the PID in the
   instance's env file, for example as `SRV`. For each saved PID, stop the
   process only if `ps -p <pid> -o command=` still shows the command the recipe
   started. Otherwise leave the process alone and tell the user about it with
   `attn ask`.
3. Clean each instance that has no `cleaned` note, as
   [Clean up](#clean-up) says. If its env file is missing, write it again first
   with
   `. <runs>/pass.env && .agents/skills/drive-attn/scripts/write-env.sh <name>`,
   running `make build` before that if `./attn` is missing. Don't launch
   anything until every instance is clean.
4. Continue from the first feature that has no `feature` note, on a new
   instance. Keep the pass's revision and folder.

Never replace an unfinished pass with a new one. To abandon a pass, clean its
instances, attach what it has to its stake, remove its checkouts and folder as
[Finish](#finish) does, and write `pass <pass> done: abandoned, <reason>`. Then
start the next pass.

## Install

Only the user installs the automation. Skip this section during a pass.

The automation runs this skill from its own control checkout, made with
`git clone`, never from the user's working checkout. Here is its definition.
Fill in the control checkout's path, and the harness, model and effort of the
Builder role in `attn delegate roles`:

```yaml
api_version: attn.dev/automations/v1alpha1
name: Feature map audit
trigger:
  type: manual
prompt:
  In this folder, run git pull --ff-only, then read
  .agents/skills/audit-feature-map/SKILL.md and run a feature-map audit pass.
launch:
  driver: <harness>
  model: <model>
  effort: <effort>
location:
  type: directory
  path: <control checkout>
```

The harness must already trust the control checkout. An unattended Claude Code
run stops at the folder trust prompt, and attn still shows it as working. Start
the harness once in that folder, accept the prompt, and quit.

Stage the automation with
`attn automation apply --file <file> --launch-desktop own --desktop-name "Feature map audit"`,
run it once with `attn automation run <id>`, and read the report. Then run
`attn automation disable <id>`, and apply the definition again with `id: <id>`
and this scheduled trigger:

```yaml
trigger:
  type: scheduled
  schedule:
    cron: <cron>
    time_zone: Europe/Stockholm
  continuity: fresh
  catch_up: latest
```

`continuity: fresh` starts a new agent for every run, so a pass never reads the
conversations of earlier passes. `singleton` would resume the same agent every
run. When the new agent finds an unfinished pass, it finishes that pass,
as Start says.

Applying the definition again keeps the automation disabled: it prints
`"enabled": false` until you run `attn automation enable <id>`. The automation
has no guard, so a pass runs on schedule even when `main` hasn't moved.
