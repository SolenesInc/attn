# Instances

An instance has its own data, socket, ports, app, and bundle id.

```bash
eval "$(attn instance-env <name>)"   # fish: attn instance-env --fish <name> | source
attn instance                        # show the selection
attn instance list --json
eval "$(attn instance-env --unset)"  # fish: attn instance-env --fish --unset | source
```

Tooling reads paths from `attn instance resolve --field <field>` and does not
derive them. Tests scope their paths with `ATTN_DATA_DIR` alone.

## Build and install

Follow [production safety](../../AGENTS.md#you-are-probably-running-inside-attn).

| Change | Dev | Named instance |
| --- | --- | --- |
| Go only | `make install-daemon-dev` | `make install-daemon INSTANCE=<name>` |
| Anything else | `make dev` | `make install INSTANCE=<name>` |

Open a named app with `make run INSTANCE=<name>`. Named instances build a faster,
less optimized shell. Set `ATTN_APP_CARGO_PROFILE=release` to build the shipping
profile. Full macOS installs need to run outside the sandbox, so signing keeps permissions. For a Linux VM, see [Local Linux runner](linux-runner.md).

## GitHub polling

Named instances do not poll GitHub with your `gh` credentials, so they cannot
spend production's API budget. To test against real GitHub, start the daemon
with `ATTN_GITHUB_POLLING=on`. Without it, harness runs use the mock GitHub.

## Iterate on the pi plugin

```bash
attn plugin uninstall attn-pi
attn plugin link --path <checkout>/plugins/attn-pi
```

New pi sessions run the checkout. After you change the driver (`src/`), restart
the daemon. `attn plugin install-bundled attn-pi` restores the bundled copy.

## Verification requirements

Cover lifecycle, protocol, PTY, background-runner, timing, and other
app-observable changes with scenarios. A pull request needs green packaged-app
CI on its head. Report missing coverage and ask before merging. Verify a landing
locally. After the landing, `main`'s full scenario matrix verifies it again. See
[Verifying before landing](trunk-based-development.md#verifying-before-landing).

CI runs the scenario matrix. Run scenarios locally, in a non-production
instance, to check a change before landing, reproduce a CI failure, or develop
a scenario. Check the installed build first, not whatever `attn` is on `PATH`:

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

## UI automation

Named instances run a localhost server in the app that scripts use to drive the
UI. Production runs it only when `ATTN_AUTOMATION=1`.
