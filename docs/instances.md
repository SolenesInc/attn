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
changes need green packaged-app CI on the PR head, with scenarios covering the
change. Report missing coverage and ask before merging.

CI runs the scenario matrix; run scenarios locally only to reproduce a CI
failure or develop a scenario, in a non-production instance. Check the installed
build first, not whatever `attn` is on `PATH`:

```bash
"$(./attn instance resolve --field appDaemon)" preflight
```

Before running scenarios, read the [harness guide](../app/scripts/real-app-harness/AGENTS.md).

## Clean up

When you move on, run `attn instance clean <name>` for instances you created.
Never delete a data directory by hand; cleanup needs its worker registry.
If clean reports a live app or PID, quit it and rerun.

## UI automation

Named instances expose a localhost automation bridge. Production needs
`ATTN_AUTOMATION=1`.
