---
name: audit-feature-map
description:
  Use only when the feature-map audit automation starts you, or the user asks
  for the audit.
---

# Audit the feature map

The drive-attn skill (`.agents/skills/drive-attn`) and its feature map tell
agents how to reach and drive each attn feature. Whoever changes a feature
should update its map file, but some changes slip through. This audit finds
and fixes what those changes missed.

Each run of the audit is a pass. In a pass, you read every feature's source,
drive every feature on a live instance, and land the map fixes you proved.
Check each feature as a whole. You do not need a separate proof for every
bullet in a feature file.

## Outcomes

Each pass ends with one of these:

- **clean**: you read and drove every feature, and nothing needed fixing.
  Nothing lands.
- **changed**: you fixed the map, and the fixes land on `main` together.
- **blocked**: you couldn't finish, or your fixes couldn't land. Say exactly
  why.

Your stake is the seed named in the automation's opening message. When the
user asks for the audit directly, plant one. At the end, harvest it with
`attn seed harvest <stake> -m "<outcome> at <revision>: <summary>"`. The next
pass reads the revision from that message.

## What you may change

Change only files in `.agents/skills/drive-attn`: its `SKILL.md`, `features/`
and `scripts/`. Don't change attn's code, tests or this skill.

When the map and attn disagree, either the map is out of date or attn has a
bug. Fix the map in the first case. In the second, report the bug and leave
the map describing what attn should do.

## Pass

### 0. Start

Run `git pull --ff-only`. If this file changed, read it again.

Then check that no other pass is in the way. A pass might still be running, or
might have died halfway. Signs of either are uncommitted changes in the
checkout, or an instance named `audit-*` in `attn instance list`. If you see
either, don't touch what you found, because it might belong to a pass that is
still running. Ask the user with `attn ask` what to do, naming what you
found. Then end your turn and
leave your stake open, because closing the stake closes the ask. Start over
when they answer.

### 1. Check the index

Read `features/README.md` and list the feature files next to it. Fix the
index where it lists a file that doesn't exist, misses a file, or lists one
twice.

### 2. Read each feature's source

Start one subagent per feature file, all at the same time. Each subagent only
reads. It never drives an instance and never edits a file. Tell each subagent
to:

1. Read its feature file and the "Feature entry contract" section of
   `features/README.md`.
2. Read attn's source to learn how people and agents reach the feature today.
3. Check that every shortcut id, palette id, `attn` command, path and scenario
   id the file names still exists.
4. Return a short summary of the feature, the ways in with their source files,
   anything in the file that looks out of date (with the source that shows
   it), and one recipe for driving the feature live.

### 3. Combine the results

Check that every feature file got a summary back. Where several recipes need
the same setup, combine the recipes so you set up once. Check the out-of-date
claims the subagents raised, but don't re-check the parts they found correct.

Then look for ways into features that no map file lists yet. Find the previous
pass's revision with `attn seed search feature map audit`, and read the
commits since then in `cmd/attn`, `app/src/shortcuts`, `app/src/application`
and `internal/prompts/content`. Only count a way in as missing when you can
point at the source that adds it.

### 4. Drive every feature

Do this even when the source reading found nothing. Drive every feature
yourself, one after another, on one instance, as drive-attn describes. Build
with `make build`, and name the instance `audit-$(date +%y%m%d%H%M)`.

Three rules hold for the whole step:

- **Check the instance before trusting it.** Run drive-attn's doctor before
  the first drive and after any drive that fails. Some failures don't show up
  in the doctor, such as an app that stopped responding while the daemon is
  fine. After you run the doctor, go back to the baseline, or clean the instance and launch a new one.
- **Keep the evidence safe.** Save the evidence in a folder made with
  `mktemp -d /tmp/drive-attn-audit.XXXX`. After each cleanup, check that the
  folder is still there.
- **Clean up as you go.** When a drive fails, remove what the drive left on
  the instance before the next drive. Stop processes only with drive-attn's
  `clean.sh`.

When the doctor fails because drive-attn's instructions are out of date, fix
the instructions and try once more. If the doctor still fails, the pass is
blocked.

Don't commit anything until you have finished driving. `bridge` refuses to
drive an app whose source changed since it was built, and a commit counts as a
change. Editing feature files without committing is fine. Editing drive-attn's
`scripts/` is not fine. After you edit the scripts, rebuild and launch again.

Some features can't be driven in a given run. Mark such a feature
unreachable and write down what it needs that the run lacks, and what you
tried. The usual reasons:

- It needs input drive-attn's rules forbid here, such as taking focus on a Mac
  someone is using.
- The baseline instance doesn't have it, such as Pi or an outpost peer.
  `features/README.md` lists what the baseline has.
- It needs a real external service.
- The permissions denied a step.

Don't work around any of these to reach the feature. If the feature file
doesn't say what the feature needs, fix the file.

After you fix drive-attn's scripts, drive the affected features again to
prove the fix. When all driving is done, run `clean.sh <name>` for each
instance you started, then delete `~/attn-notebook-<name>` if it exists.

### 5. Sort the findings

Before you change anything, find out what attn is meant to do: read the
commit that changed the behavior, its seed, `docs/guide` and the glossary.
Then sort each finding:

- **The map is wrong**: the map describes the feature wrongly or misses a way
  in. Fix the feature file.
- **drive-attn can't drive it**: attn works, but the instructions or scripts
  can't reach the feature. Fix the instructions or scripts, and when you add a
  script, say in `SKILL.md` how to run the script.
- **attn has a bug**: attn doesn't do what it is meant to. Search for an
  existing seed with `attn seed search <words>`. Add a note to the existing
  seed, or plant a new seed with `--discovered-from <stake>`. Use one seed per
  cause. Don't fix the bug in this pass.
- **You can't tell what attn is meant to do**: ask the user with `attn ask`,
  and leave the map as it is.

Never make a recipe weaker so that the recipe passes.

### 6. Finish

If you changed anything, read every changed file whole, commit, and land
with the `land-on-main` skill. Otherwise land nothing.

Pack the evidence folder into a `.tar.gz` and attach it to your stake with
`attn seed attach <stake> --path <file> --move`. Add one note to the stake
with the features you covered, the unreachable ones and what they need, the
map fixes, the seeds you planted or noted, and the outcome. Then harvest the
stake.

Use the `attn` on your `PATH` only for your stake, seeds and asks. Run it in a
shell without drive-attn's prefix, because the prefix points `attn` at the
instance, and the instance doesn't have your stake.

## Install

Only the user installs the automation. Skip this section during a pass.

The automation runs from its own `git clone`, not the user's working
checkout. Fill in the clone's path, and the harness, model and effort of the
Builder role in `attn delegate roles`:

```yaml
api_version: attn.dev/automations/v1alpha1
name: Feature map audit
trigger:
  type: scheduled
  schedule:
    cron: <cron>
    time_zone: Europe/Stockholm
  continuity: fresh
  catch_up: latest
prompt:
  In this folder, run git pull --ff-only, then read
  .agents/skills/audit-feature-map/SKILL.md and run a feature-map audit pass.
launch:
  driver: <harness>
  model: <model>
  effort: <effort>
location:
  type: directory
  path: <clone>
```

Before applying the file, start the harness once in the clone, accept the
harness's folder trust prompt, and quit. Otherwise an unattended run waits at
that prompt while attn shows the run as working.

Apply the file with
`attn automation apply --file <file> --launch-desktop own --desktop-name "Feature map audit"`.
The automation starts enabled. Run the automation once with
`attn automation run <id>` and read the note on its stake.
