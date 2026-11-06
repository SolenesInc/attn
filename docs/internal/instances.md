# Instances

An instance has its own data, socket, ports, app, and bundle id.

```bash
eval "$(attn instance-env <name>)"   # fish: attn instance-env --fish <name> | source
attn instance                        # show the selection
attn instance list --json
eval "$(attn instance-env --unset)"  # fish: attn instance-env --fish --unset | source
```

Tooling reads paths from `attn instance resolve --field <field>` and does not
derive them. Tests scope their paths with `ATTN_DATA_DIR` alone: a daemon whose
`ATTN_DATA_DIR` is not its instance's own folder keeps its default Notebook in
`<data folder>/notebook`, away from the user's real one.

## Build and install

Follow [production safety](../../AGENTS.md#you-are-probably-running-inside-attn).

| Change | Dev | Named instance |
| --- | --- | --- |
| Go only | `make install-daemon-dev` | `make install-daemon INSTANCE=<name>` |
| Anything else | `make dev` | `make install INSTANCE=<name>` |

Open a named app with `make run INSTANCE=<name>`. Named instances build a
faster, less optimized shell. Set `ATTN_APP_CARGO_PROFILE=release` to build the
shipping profile. Run full macOS installs outside the sandbox. Signing then finds
the machine's code-signing identity with `security find-identity` and does not
fall back to an ad-hoc signature, so macOS privacy grants such as Screen
Recording stay attached to the app across rebuilds. For a Linux VM, see [Local Linux runner](linux-runner.md).

## GitHub polling

Named instances do not poll GitHub with your `gh` credentials, so they cannot
spend production's API budget. To test against real GitHub, start the daemon
with `ATTN_GITHUB_POLLING=on`. Without it, harness runs use the mock GitHub.

## Verification requirements

Cover lifecycle, protocol, PTY, background-runner, timing, and other
app-observable changes with scenarios. A pull request needs green packaged-app
CI on its head. Report missing coverage and ask before merging. Verify a landing
locally. After the landing, CI verifies it again with `main`'s full scenario
matrix. See
[Verifying before landing](trunk-based-development.md#verifying-before-landing).

CI runs the scenario matrix. Run scenarios locally, in a non-production
instance, to reproduce a CI failure or develop a scenario. When the machine has
the capacity, you may also run them to check an app-observable change before
landing.

Check the installed build first, not the `attn` on `PATH`:

```bash
"$(./attn instance resolve --field appDaemon)" preflight
```

Before running scenarios, read the [harness guide](../../app/scripts/real-app-harness/AGENTS.md).

## Clean up

When you move on, run `attn instance clean <name>` for instances you
created, including runs you abandon. `clean` stops the instance's app, daemon,
workers, and plugin drivers, and removes the instance's data. `clean` takes the
instance from its argument alone, never from routing variables, and refuses the
default (production) instance without `--force`.
Never delete a data directory by hand, because cleanup needs the worker registry.

If `clean` reports a live app, quit the app and rerun `clean`.

If `clean` reports a process it could not confirm or stop:

- If you are an agent, leave that process alone and bring the output of `clean`
  to the user.
- If you are a person, check the process with `ps -p <pid>`. Stop it only if it
  is a stale attn worker, then rerun `clean`.

Worker removal waits for the worker's child to exit. If a worker cannot be
removed, `clean` exits non-zero and keeps the instance data and worker registry
for diagnosis.

## Rehearsing the Chief's onboarding

The `onboarding` instance repeats the Chief's onboarding in any install state,
in the real app with real models, so you can talk to the Chief, change the text
and run it again:

```bash
node app/scripts/onboarding-rehearsal/rehearse.mjs                  # list the scenarios and when each last ran
node app/scripts/onboarding-rehearsal/rehearse.mjs <scenario>       # stop the instance, restore the scenario, open the app
node app/scripts/onboarding-rehearsal/rehearse.mjs <scenario> --rebuild
```

`--rebuild` installs the daemon first, which is what a prompt change needs.
`--rebuild-app` installs the whole app, for a change under `app/src`.
`--fresh-world` builds the scenario's saved state again. Saved state is
migrated forward like any old install, so a schema change doesn't need this.
The `upgrader` state needs the old chief-of-staff session role, so it can't be
rebuilt once that role is removed. Don't use `--fresh-world` for it after that.

The Chief runs with its own harness config in `~/attn-onboarding-rehearsal/tool-home`,
without your instructions, memory or skills. Log each harness in once:

```bash
node app/scripts/onboarding-rehearsal/rehearse.mjs login claude
node app/scripts/onboarding-rehearsal/rehearse.mjs login codex
```

The logins survive every run. The Notebook lives in `/Users/Shared`, because
Claude Code walks up from its folder and would read `~/.claude/CLAUDE.md` from
anywhere under your home. When the Chief slot asks for a Notebook folder, keep that default for the same
reason. Analytics uploads go nowhere because this isn't production, and skills
install only into the tool home.

When you're done, run `attn instance clean onboarding`, then delete
`~/attn-onboarding-rehearsal` and `/Users/Shared/attn-onboarding-notebook*`.

## UI automation

Named instances run a localhost server in the app that scripts use to drive the
UI. Production runs it only when `ATTN_AUTOMATION=1`.
