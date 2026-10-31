# Instances

Separate installs of attn under a name, for developing and testing without
touching production. An instance is a daemon, an app and a data folder under
one name, with its own socket, ports, bundle id and deep-link scheme.
Production has no instance name, and `dev` is the development sibling.
Maintainers select an instance in a shell, build and install it, start and
stop its daemon, check which build runs, and clean it when they move on.
Every `attn` command acts on the instance its shell selects. The full launch
with the mock agent and the mock GitHub, the doctor and the cleanup are in
[../SKILL.md](../SKILL.md); `docs/internal/instances.md` covers the rest.

## Sub-features

- `instance-select`: `attn instance-env <name>` selects an instance in a shell and clears inherited routing; `--unset` returns the shell to production.
- `instance-status`: `attn instance` shows the selection, its resources and what is running; `attn instance list` shows every instance on the machine.
- `instance-resolve`: `attn instance resolve` prints an instance's resources as JSON, or one `--field`; tooling reads paths from it.
- `instance-install`: `make install INSTANCE=<name>` builds and installs an instance's app, `make install-daemon INSTANCE=<name>` swaps only its daemon, `make run INSTANCE=<name>` installs and opens it, and `make dev` does all of it for `dev`.
- `daemon-lifecycle`: `attn daemon ensure` starts the instance's daemon, or replaces one whose health reply identifies a different build or instance. If health is unavailable, it leaves the daemon running and reports its PID, health timeout and restart commands. `attn daemon stop` stops it.
- `instance-clean`: `attn instance clean <name>` stops the app, daemon and workers and removes the app, its local data and the data folder; `attn instance stop-app` only quits the app.
- `build-identity`: `attn version` names the build and its source fingerprint; the app refuses a daemon of another instance.
- `instance-marker`: a non-production app names its instance in the sidebar: `instance <name>` while expanded, `<name>` while collapsed or on the queue bar.
- `instance-origin`: `attn instance set-origin` records the worktree an install came from, and `list` shows it.
- `linux-scheme`: `attn instance register-scheme` claims the instance's deep-link scheme on Linux.

## How to get to it

### People

- A maintainer's shell: the verbs below.
- Make targets (`Makefile`): `build`, `install INSTANCE=<name>`, `install-daemon INSTANCE=<name>`, `run INSTANCE=<name>`, and for `dev` the aliases `dev`, `install-dev` and `install-daemon-dev`.
- In the app: the instance marker `[data-testid="sidebar-instance-marker"]`, which reads `instance <name>` in an expanded sidebar (`app/src/components/SidebarHeader.tsx`, in both flows) and `<name>` in the queue bar that replaces a collapsed sidebar in queue flow (`app/src/components/QueueBar.tsx`); a collapsed sidebar in desktop flow shows the name with the title `Instance: <name>` and no such `data-testid` (`app/src/components/SidebarChrome.tsx`); the refusal of a daemon from another instance (`app/src/utils/buildInstance.ts`); and why its daemon would not start, while the app retries.

### Agents

- `attn instance status|resolve|list|clean|stop-app|set-origin|tauri-config|register-scheme|env` (`cmd/attn/instance.go`); `attn instance-env <name>|--unset`, with `--fish` for fish (`cmd/attn/instance_env.go`); `attn daemon ensure|stop` and `attn version [--json]` (`cmd/attn/main.go`).
- No prompt attn sends names these verbs. The agents an instance runs reach that instance with their own `attn` commands, because every terminal it starts carries its routing: `ATTN_INSTANCE`, `ATTN_SOCKET_PATH`, `ATTN_DATA_DIR` and the rest (`internal/daemon/spawn_routing.go`).

### Triggers

- An app starts its instance's daemon when it opens and retries until it starts (`ensure_daemon` in `app/src-tauri/core/src/lib.rs`, called from `app/src/hooks/useDaemonSocket.ts`).

## Driving it

Preconditions:

- This checkout built (`make build`) and the env file written, as [../SKILL.md](../SKILL.md)'s Launch says. Pick a name of your own (`[a-z0-9][a-z0-9-]{0,15}`), not `dev`; `<name>` below.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-instances.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.

- **Select.** Run `./attn instance-env <name>`: it prints an `unset` line for each inherited routing variable, then `export ATTN_INSTANCE=<name>`, the lines the env file opens with. `./attn instance` prints `attn instance: <name>`, then `data dir`, `socket` with `(no daemon socket)`, `ws port`, `bundle id`, `app` with `(not installed)` and `mock gh`, and no `CONFLICT` line.
- **Resolve.** `./attn instance resolve --field dataDir` prints the data folder. `./attn instance resolve` prints JSON with `"instance": "<name>"`, `wsPort`, `appDaemon` and `mockGitHubPort`. `./attn instance resolve --instance other --field wsPort` resolves another name without selecting it. `./attn instance tauri-config` prints the build's config overlay: `productName` and `identifier` match `appName` and `bundleId`, and the deep-link scheme is `deepLinkScheme`. `./attn instance env <name>` prints the same lines as `./attn instance-env <name>`.
- **Start and stop the daemon.** `./attn daemon ensure` prints JSON with `"status": "started"`, with the banner on stderr. Run it again: `"status": "already_running"`. `./attn instance` now shows `(daemon socket present)`. `./attn daemon stop` prints `stopped daemon (pid <n>)`, and a second stop prints `daemon not running` with the reason.
- **Which build runs.** `./attn version` prints the version, channel and commit, with `dirty` for an uncommitted tree. `./attn version --json` shows `sourceFingerprint`, equal to what `bash scripts/source-fingerprint.sh` prints for this tree.
- **List.** `./attn instance list` prints a row for `<name>` marked `*` under `INSTANCE  PORT  DATA  APPDATA  APP  ORIGIN`, beside every other instance on the machine; `--json` adds what is running.
- **Install the app and see the marker.** Launch the app as [../SKILL.md](../SKILL.md) says (`make install INSTANCE=<name>`, then `bridge --launch --wait-ready ping`). `./attn instance` shows `app` as `(installed)`, and `./attn instance list` shows this worktree's folder name under `ORIGIN`. With no session yet the sidebar starts collapsed: `bridge dom_text '{"selector":".sidebar-instance-marker--collapsed"}'` reads `<name>`. Start a shell with `wsctl add-session --cwd $P --agent shell --label marker-shell --id marker-shell`, which expands the sidebar: `bridge dom_text '{"selector":"[data-testid=\"sidebar-instance-marker\"]"}'` reads `instance <name>`.
- **The app starts its daemon.** With the app open, `./attn daemon stop` prints `stopped daemon (pid <n>)`. Re-read `./attn instance` until it shows `(daemon socket present)` again, with a deadline: the app reconnected and started the daemon with its own environment.
- **Swap only the daemon.** After a Go change, `make install-daemon INSTANCE=<name>` prints `Updated bundled daemon at` the instance's daemon path and restarts it. `"$(./attn instance resolve --field appDaemon)" version --json` shows the new `sourceFingerprint`.
- **Record the origin by hand.** Run `./attn instance set-origin <name> --worktree "$PWD"`. It prints `recorded origin for instance <name>:` and the folder, with the branch in parentheses. `./attn instance list --json` shows the same folder under `origin`. `./attn instance set-origin` with no name exits 1 naming `set-origin requires an instance name`.
- **Quit the app.** `./attn instance stop-app` prints `  app      ` and how the app stopped; `bridge ping` no longer answers.
- **Refusals.** `./attn instance clean` exits 1 with `attn instance: clean requires an instance name`. `./attn profile list` exits 2 with `attn profile was renamed to attn instance. Run: attn instance list`.
- **The Linux scheme.** Run `./attn instance register-scheme`. On macOS it exits 1 with `attn instance: register-scheme is Linux-only`, which is as far as this machine goes; report the Linux behavior unreached. On Linux it prints `>>> Registered <scheme>:// for <name>` and the desktop entry's path, and `./attn instance` shows the `handler` row as `(registered)`.
- **Clean.** `./attn instance clean <name>` prints `>>> Cleaning instance <name>`, then a line for the app, the daemon, the workers and each removed folder. `./attn instance` shows `(no daemon socket)` and `(not installed)`. `./attn instance-env --unset` prints the same `unset` lines, then `unset ATTN_INSTANCE`; the instance shell ends when [../SKILL.md](../SKILL.md)'s Cleanup removes the env file.
- **Proof.** In `$P`, keep each command with its stdout, stderr and exit code; `./attn instance resolve` and `./attn version --json`; and `bridge capture_screenshot '{"path":"<P>/instance-marker.png"}'` with the marker in view, where `<P>` is the folder's path written out. Put the way in in each file's name, such as `instances-daemon-ensure.txt`.

## Contexts

- **Durability across restarts**: an instance's sessions, garden and settings live in its data folder and survive restarts of its daemon, app and machine. `clean` removes them. The notebook folder, `~/attn-notebook-<name>`, survives `clean`, and a later instance with the same name starts with it.
- **Linux**: the app is a directory tree instead of a macOS bundle. `attn instance register-scheme` claims the deep-link scheme in the desktop database, and `make run` starts the app in the background, logging to `<data-dir>/app.log`.
- **Profiles**: profiles live inside an instance. A new instance starts with its own profiles and sees none of production's.
- **Compatibility across versions**: `attn profile` and `attn profile-env` were renamed to `attn instance` and `attn instance-env`, and exit 2 naming the new command. `attn daemon ensure` restarts a daemon that does not match the binary, and the CLI warns when its release differs from the running daemon's.
- **Flow**: an expanded sidebar shows `instance <name>` in either flow. A collapsed sidebar in queue flow becomes the queue bar, which shows `<name>` under the same `data-testid`, so a check that reads `instance <name>` needs the sidebar expanded.

## Tests

- Stack: `cmd/attn/daemon_ensure_stack_test.go`, `cmd/attn/daemon_stop_stack_test.go`, `cmd/attn/daemon_stack_test.go`, `cmd/attn/daemon_startup_stack_test.go`, `cmd/attn/daemon_socket_stack_test.go`, `cmd/attn/pidlock_stack_test.go`, `cmd/attn/instance_env_stack_test.go`, `cmd/attn/version_stack_test.go`.
- Kernel: `cmd/attn/instance_test.go`, `cmd/attn/instance_origin_test.go`, `cmd/attn/instance_pty_host_test.go`, `cmd/attn/instance_app_stop_test.go`, `internal/daemonctl/*_test.go`, `internal/desktopentry/*_test.go`.
- App: `app/src/App.daemonEnsure.test.tsx`, `app/src/App.instanceMismatch.test.tsx`, `app/src/App.releaseUpdates.test.tsx`.

## Gotchas

- In the collapsed desktop sidebar, the `drive-6` marker can render as `dr…` even though `dom_text` returns `drive-6`. The full name is not visible in that screenshot. Expand the sidebar for instance proof; the expanded marker shows `instance drive-6`.
- Bare `make install`, `make run` and `make install-daemon` act on production, and need the user's approval. Always pass `INSTANCE=<name>`.
- `attn instance clean` with no instance name is refused; the default instance, production, is cleaned only with `--force`. Never pass it.
- `./attn` in a shell with no instance selected, or the `attn` on `PATH`, talks to production. Most verbs print `[attn instance=<name> socket=<socket> port=<port>]` on stderr while an instance is selected (`internal/config/banner.go`); no banner means none. `./attn instance` shows the selection.
- A routing override left in the shell, such as `ATTN_DATA_DIR` or `ATTN_SOCKET_PATH`, makes every other `attn` command refuse to run, and `./attn instance` prints `CONFLICT`. Select the instance again with `attn instance-env`, which clears them.
- On macOS, `make run INSTANCE=<name>` opens the app through Launch Services, which drops the instance shell's environment. Launch with `bridge --launch` when the daemon needs the mock variables.
- `make build` replaces `./attn` for every maintainer sharing the checkout. Build in a checkout of your own when others are driving.
- `attn instance list` shows other maintainers' instances too. Never stop, drive or clean an instance this run did not start.
- An app built from this checkout never asks GitHub for a newer release, so the "Version … is available on GitHub." notice cannot show on an instance. `app/src/App.releaseUpdates.test.tsx` covers it.
- `dev` installs the attn skill into your harness homes; a named instance does not.
- Working but wrong: the instance-mismatch refusal tells the user to launch `attn.app` or `attn-dev.app`, though a named instance's app has a name of its own.
