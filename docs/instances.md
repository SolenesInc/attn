# Instances

An instance isolates data, socket, ports, app, and bundle id.

```bash
eval "$(attn instance-env <name>)"   # fish: attn instance-env --fish <name> | source
attn instance                        # show the selection
attn instance list --json
eval "$(attn instance-env --unset)"  # fish: attn instance-env --fish --unset | source
```

Tooling reads paths from `attn instance resolve --field <field>` instead of
deriving them. Tests scope with bare `ATTN_DATA_DIR`.

## Build and install

Follow [production safety](../AGENTS.md#you-are-probably-running-inside-attn).

| Change | Dev | Named instance |
| --- | --- | --- |
| Go only | `make install-daemon-dev` | `make install-daemon INSTANCE=<name>` |
| Anything else | `make dev` | `make install INSTANCE=<name>` |

Open a named app with `make run INSTANCE=<name>`. Named instances build a faster,
less optimized shell; `ATTN_APP_CARGO_PROFILE=release` builds the shipping way.
Full macOS installs run outside the sandbox so signing keeps permissions. For a
Linux VM, see [Local Linux runner](linux-runner.md).

## GitHub polling

Named instances do not poll GitHub with your `gh` credentials, so they cannot
spend production's API budget. To test against real GitHub, start the daemon
with `ATTN_GITHUB_POLLING=on`; harness runs use the mock GitHub without it.

## Iterate on the pi plugin

```bash
attn plugin uninstall attn-pi
attn plugin link --path <checkout>/plugins/attn-pi
```

New pi sessions run the checkout; driver changes (`src/`) need a daemon restart.
`attn plugin install-bundled attn-pi` restores the bundled copy.

## Verification requirements

Lifecycle, protocol, PTY, background-runner, timing, and other app-observable
changes need scenarios covering them. A pull request needs green packaged-app
CI on its head; report missing coverage and ask before merging. A landing is
verified locally and by `main`'s full scenario matrix after it lands; see
[Verifying before landing](trunk-based-development.md#verifying-before-landing).

CI runs the scenario matrix. Run scenarios locally, in a non-production
instance, to check a change before landing, reproduce a CI failure, or develop
a scenario. Check the installed build first, not whatever `attn` is on `PATH`:

```bash
"$(./attn instance resolve --field appDaemon)" preflight
```

Before running scenarios, read the [harness guide](../app/scripts/real-app-harness/AGENTS.md).

## Clean up

When you move on, run `attn instance clean <name>` for instances you created,
including runs you abandon. It stops the instance's app, daemon, workers and
plugin drivers, and removes its data. It takes the instance from its argument
alone, never from routing variables, and refuses the default (production)
instance without `--force`.
Never delete a data directory by hand; cleanup needs its worker registry.
If clean reports a live app, quit it and rerun. If it reports a process it
could not confirm or stop, leave that process alone: an agent brings clean's
output to the user, and a person checks it with `ps -p <pid>`, stops it only
if it is a stale attn worker, then reruns clean.
Worker removal waits for the worker's child to exit. When a worker cannot be
removed, clean exits non-zero and keeps the instance data and worker registry
for diagnosis.

## UI automation

Named instances expose a localhost automation bridge. Production needs
`ATTN_AUTOMATION=1`.
