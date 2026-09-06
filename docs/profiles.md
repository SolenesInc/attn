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
On Linux, `make install PROFILE=<name>` stages an unprivileged application tree.
The `App acceptance` CI job uses that same path before running the serial matrix.

For a local Linux VM, see [Local Linux runner](linux-runner.md). It builds and
verifies named profiles through Lima, OrbStack, or an existing SSH machine.

## Linux deep links

macOS registers the profile's resolved `<deepLinkScheme>://` on the app bundle
itself (`attn://` for the default profile; `attn-dev://` or `attn-<name>://`
for others — see `DeepLinkSchemeForProfile`). Elsewhere, `make install` also
runs `attn profile register-scheme --profile <name>`: it writes
`<appName>-handler.desktop` under `~/.local/share/applications` (or
`$XDG_DATA_HOME/applications`) and refreshes the desktop database
(`update-desktop-database`, `xdg-mime`). Missing tools are reported, not
fatal: the entry is still written. Rerun `register-scheme` by hand after
moving the installed executable.

`attn profile resolve --field desktopEntry` reports the handler path (empty
off Linux); `attn profile resolve --field deepLinkScheme` reports the scheme;
`attn profile clean <name>` removes the entry along with everything else.

Launching the app (bare `attn`, or `attn -s <label>`) builds a
`<deepLinkScheme>://spawn?...` deep link (`attn://spawn?...` for the default
profile). On Linux this launches the profile's own app executable, which, via
tauri-plugin-single-instance, hands the URL to an already-running instance
instead of opening a second window.

## Iterate on a bundled plugin

A change under `plugins/attn-pi` does not need `make install`. The driver reads
two environment variables before falling back to the bundled build:

| Variable | Points the driver at |
| --- | --- |
| `ATTN_PI_SUITE_PATH` | the pi extension, e.g. `<checkout>/plugins/attn-pi/suite/index.ts` |
| `ATTN_NISSE_COMMAND` | the nisse host, e.g. `bun <checkout>/plugins/attn-pi/host/index.ts` |

pi loads TypeScript extensions directly, and auto mode and the security
extension are imported by the suite, so one variable covers all three.

The daemon builds the driver's environment from its own environment plus the
login-shell environment it captured at start, so export the variable, then
restart the non-production daemon once. From then on every new pi session runs
what is on disk; sessions already running keep the code they loaded.

`scripts/build-bundled-plugins.sh` stages fresh bundles in seconds, and a daemon
started by hand with `ATTN_BUNDLED_PLUGIN_DIR` pointed at that stage dir serves
them. The app scrubs that variable as a routing override, so it does not reach
an app-launched daemon.

`attn plugin install --path` copies the tree, so it is a remove and reinstall
per change; use the variables instead.

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
the bundle/data and the app's local data dir (Tauri's `app_local_data_dir`:
automation manifest, frontend debug logs, WebKit state). Never delete the data
directory first: the worker registry is needed for cleanup. Inspect any
unconfirmed PIDs the command leaves running.

The app goes first and clean waits for its pid (`<data-dir>/app.pid`, written by
the Tauri shell) to be gone — a macOS quit request only asks — escalating to
SIGTERM then SIGKILL if it will not leave. Ownership is rebuilt from the live
process before every signal: a pid that is not this profile's app executable is
never signalled, and one that is alive but cannot be identified stops the clean
rather than being assumed dead. The shell rewrites `app.pid` on every launch, so
a marker naming a different pid, or one that reappears after the stop, is a
relaunch and aborts the clean. Whenever the app cannot be confirmed gone, clean
removes nothing and names the pid: quit it yourself and re-run. `--force` covers
the production profile, never a live app.

The last gap a marker cannot close — a launch between the final check and the
removals — is closed by `~/.attn.locks/app-<profile>.lock`. Every app process
takes it *shared* at startup and the kernel holds it for that process's
lifetime, so app instances never block each other and the Linux no-bus fallback
can still open a second instance to deliver a deep link. Clean takes it
*exclusive* once the old app is gone and holds it through the last removal, so
it can only run when no app instance of that profile is left. Whoever loses
gives way: an app launched into a clean waits up to 3s for it to finish and then
refuses to start rather than run unlocked, and a clean that finds the lock held
aborts before touching anything. Taking the lock is mandatory for the app: it
will not write `app.pid` or open a window without it, and the reason goes to
stderr. The lock file itself is never removed, and cannot go stale: kernel
ownership disappears with the process.

Use `attn profile stop-app --profile <name>` to stop only the app; it returns
once the app is gone.
`attn profile list --json` reports the install's origin worktree and live workers,
and `appLocalDataDir`/`hasAppLocalData` so a profile whose data dir and app are
already gone still shows up while its app local data lingers.
Installs record `<data-dir>/origin.json`; manual installs can use
`attn profile set-origin <name> --worktree <dir>`.

## UI automation

Named profiles expose a localhost/token bridge through `ui-automation.json`.
Production requires explicit `ATTN_AUTOMATION=1`.
