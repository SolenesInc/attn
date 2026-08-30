# Profiles

## Select and inspect

A profile isolates data, socket, ports, app, and bundle id.
Select it through `profile-env` to clear inherited routing overrides:

```bash
eval "$(attn profile-env <name>)" # bash/zsh
attn profile-env <name> | source # fish
attn profile
attn profile list --json
attn profile resolve --json
```

- Check the `[attn profile=…]` banner before lifecycle commands.
- Build/test tooling reads `attn profile resolve --field <field>`; do not
  duplicate path/port derivation from `internal/config`.
- `ATTN_DATA_DIR` overrides runtime paths. `profile resolve` and `profile clean`
  still use canonical profile paths. Use bare `ATTN_DATA_DIR` for test scoping.
- `ATTN_PROFILE` plus another profile's routing paths/port is rejected.
  Reselect with `profile-env`; never bypass the check.
- Clear selection with `eval "$(attn profile-env --unset)"`.

## Build and install

Follow [production safety](../AGENTS.md#you-are-probably-running-inside-attn).
Select the named profile before installing; `ATTN_PROFILE` must match `PROFILE`.

| Change | Dev | Named profile |
| --- | --- | --- |
| Go-only | `make install-daemon-dev` | `make install-daemon PROFILE=<name>` |
| App, plugins, protocol, bundle metadata | `make dev` | `make install PROFILE=<name>` |

Use a full build when unsure or when the daemon-only build misses the change.
Open a named app with `make run PROFILE=<name>`.
Full macOS builds/installs run outside the sandbox for keychain-backed signing;
ad-hoc signing loses persistent permissions.

## Verification requirements

- Non-trivial PRs need live verification from their branch in a non-production profile.
- Exempt trivial docs/comments/renames/log strings, or isolated changes fully
  covered by unit tests with no lifecycle, protocol, PTY, runner, timing, or UI
  behavior. State the exemption.
- Self-contained CLI behavior: built binary plus unit tests.
- Daemon-only verification: only when no behavior reaches the app.
- App-observable changes, including daemon state/broadcasts, PTYs, and PR/git
  flows: exercise the running app. Visible changes need a recording.
- Lifecycle, protocol, PTY, background-runner, and UI changes always need live
  verification. If unavailable, ask before merging.

## Verify the installed build

Run the selected profile's bundled CLI, with the scenario's launch settings:

```bash
profile_cli="$(./attn profile resolve --field appDaemon)"
"$profile_cli" preflight
"$profile_cli" preflight --agent codex --model <model> --effort high --json
```

Fix tool/path/routing/daemon/protocol failures before collecting evidence.
A different `attn` on `PATH` does not verify the installed build.
Read [harness guidance](../app/scripts/real-app-harness/AGENTS.md) before
packaged-app scenarios or recordings; those scenarios run serially.
Go and frontend e2e suites may run concurrently in distinct worktree profiles.

## Clean up

Clean temporary profiles when finished with `attn profile clean <name>`.
It stops workers, conversation hosts, plugins, daemon, and app before removing
the bundle/data. Never delete the data directory first: the worker registry
is needed for cleanup. Inspect any unconfirmed PIDs the command leaves running.

Use `attn profile stop-app --profile <name>` to stop only the app.
`attn profile list --json` reports the install's origin worktree and live workers.
Installs record `<data-dir>/origin.json`; manual installs can use
`attn profile set-origin <name> --worktree <dir>`.

## UI automation

Named profiles expose a localhost/token bridge through `ui-automation.json`.
Production requires explicit `ATTN_AUTOMATION=1`.
