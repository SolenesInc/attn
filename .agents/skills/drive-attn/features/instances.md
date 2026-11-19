# Instances

Separate installs of attn under a name, for developing and testing without
touching production. An instance is a daemon, an app and a data folder under
one name, with its own socket, ports, bundle id and deep-link scheme.
Production has no instance name, and `dev` is the development sibling.
Maintainers select an instance in a shell, build and install it, start and
stop its daemon, check which build runs, and clean it when they finish with it.
Every `attn` command acts on the instance its shell selects. The full launch
with the mock agent and the mock GitHub, the doctor and the cleanup are in
[../SKILL.md](../SKILL.md). `docs/internal/instances.md` covers the rest.

## Sub-features

- `instance-select`: `attn instance-env <name>` selects an instance in a shell and clears inherited routing. `--unset` returns the shell to production.
- `instance-status`: `attn instance` shows the selection, its resources and what is running. `attn instance list` shows every instance on the machine.
- `instance-resolve`: `attn instance resolve` prints an instance's resources as JSON, or one `--field`. Tooling reads paths from the output.
- `instance-install`: `make install INSTANCE=<name>` builds and installs an instance's app, `make install-daemon INSTANCE=<name>` swaps only its daemon, `make run INSTANCE=<name>` installs and opens it, and `make dev` does all of it for `dev`.
- `daemon-lifecycle`: `attn daemon ensure` starts the instance's daemon, or replaces one whose health reply identifies a different build or instance. If health is unavailable, `attn daemon ensure` leaves the daemon running and reports the daemon's PID, health timeout and restart commands. `attn daemon stop` stops it and leaves the terminals running in their PTY host or workers, so they come back with the next daemon; it names their PIDs. `attn daemon stop --terminals` also ends every terminal and agent of the instance.
- `instance-clean`: `attn instance clean <name>` stops the app, daemon and workers and removes the app, its local data and the data folder. `--keep-app` keeps the installed app and resets only the data. `attn instance stop-app` only quits the app.
- `build-identity`: `attn version` names the build and its source fingerprint. The app refuses a daemon of another instance.
- `instance-marker`: a non-production app shows `<name>` beside the profile pill in the title bar.
- `instance-origin`: `attn instance set-origin` records the worktree an install came from, and `list` shows it.
- `linux-scheme`: `attn instance register-scheme` claims the instance's deep-link scheme on Linux.

## How to get to it

### People

- A maintainer's shell: the verbs below.
- Make targets (`Makefile`): `build`, `install INSTANCE=<name>`, `install-daemon INSTANCE=<name>`, `run INSTANCE=<name>`, and for `dev` the aliases `dev`, `install-dev` and `install-daemon-dev`.
- In the app: the instance marker `titlebar.instance` in the title bar, which reads `<name>` with the description `Instance: <name>` (`native/crates/attn-app/src/shell.rs`). The app also shows its refusal of a daemon from another instance, "This daemon serves another instance" (`native/crates/attn-app/src/connection.rs`), and why its daemon would not start, while the app retries.

### Agents

- `attn instance status|resolve|list|clean|stop-app|set-origin|register-scheme` (`cmd/attnd/instance.go`), `attn instance env` (`cli/attn/src/commands/instance_env.rs`). `attn instance-env <name>|--unset`, with `--fish` for fish (`cli/attn/src/commands/instance_env.rs`). `attn daemon ensure|stop [--terminals]` and `attn version [--json]` (`cmd/attnd/main.go`).
- No prompt attn sends names these verbs. The agents an instance runs reach that instance with their own `attn` commands, because every terminal the instance starts carries the instance's routing: `ATTN_INSTANCE`, `ATTN_SOCKET_PATH`, `ATTN_DATA_DIR` and the rest (`internal/daemon/spawn_routing.go`).

### Triggers

- An app starts its instance's daemon when it opens and retries until it starts (`daemon::ensure` in `native/crates/attn-app/src/daemon.rs`, which runs the bundled `attnd daemon ensure`, wired in `native/crates/attn-app/src/launch.rs`).

## Driving it

Preconditions:

- Build this checkout with `make build` and write the env file, as [../SKILL.md](../SKILL.md)'s Launch says. Pick a name of your own (`[a-z0-9][a-z0-9-]{0,15}`), not `dev`. Below, `<name>` is that name.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-instances.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.

- **Select.** Run `./attn instance-env <name>`: it prints an `unset` line for each inherited routing variable, then `export ATTN_INSTANCE=<name>`, the lines the env file opens with. `./attn instance` prints `attn instance: <name>`, then `data dir`, `socket` with `(no daemon socket)`, `ws port`, `bundle id`, `app` with `(not installed)` and `mock gh`, and no `CONFLICT` line.
- **Resolve.** `./attn instance resolve --field dataDir` prints the data folder. `./attn instance resolve` prints JSON with `"instance": "<name>"`, `wsPort`, `appDaemon` and `mockGitHubPort`. `./attn instance resolve --instance other --field wsPort` resolves another name without selecting it. `./attn instance env <name>` prints the same lines as `./attn instance-env <name>`.
- **Claim the port.** `./attn instance claim-port` prints a port and writes it to `ws-port` in the data folder. Run it again: the same port. `./attn instance resolve --field wsPort` then prints that port. `./attn instance claim-port --instance default` prints `9849` and writes no file. When another instance already uses `<name>`'s derived port, the claim prints the next port no other instance uses. `./attn instance clean <name> --keep-app` prints `port     kept <port> for the kept app`, and `resolve --field wsPort` still prints that port.
- **Start and stop the daemon.** `./attn daemon ensure` prints JSON with `"status": "started"`, with the banner on stderr. Run it again: `"status": "already_running"`. `./attn instance` now shows `(daemon socket present)`. `./attn daemon stop` prints `stopped daemon (pid <n>)`, and a second stop prints `daemon not running` with the reason.
- **Stop with the terminals.** Start the daemon, run `wsctl add-session --cwd $P --agent shell --label stop-shell --id stop-shell`, and note the shell's PID from `wsctl list`. `./attn daemon stop` prints `stopped daemon (pid <n>)` and `terminals keep running in pids [<host>]`, and `ps -p <shell-pid>` still lists the shell. `./attn daemon stop --terminals` prints `daemon not running`, then `pty hosts 1 registered (1 terminated)` or `workers  1 registered (1 removed)`; `ps -p <shell-pid>` and `ps -p <host>` list nothing.
- **Which build runs.** `./attn version` prints the version, channel and commit, with `dirty` when the source it ships differs from that commit. `./attn version --json` shows `sourceFingerprint`, equal to what `bash scripts/source-fingerprint.sh` prints for this tree: `git:<commit>` with `dirty` false when only tests, scripts or docs the binary does not embed changed, and `tree:<hash>` with `dirty` true otherwise.
- **List.** `./attn instance list` prints a row for `<name>` marked `*` under `INSTANCE  PORT  DATA  APPDATA  APP  ORIGIN`, beside every other instance on the machine. `--json` adds what is running.
- **Install the app and see the marker.** Launch the app as [../SKILL.md](../SKILL.md) says (`make install INSTANCE=<name>`, then `bridge --launch ping` and `bridge wait '{"target":{"key":"home"}}'`). On Linux, run every command that reaches the app inside one `xvfb-run -a -s '-noreset -screen 0 1600x1000x24' bash -c '…'`. `./attn instance` shows `app` as `(installed)`, and `./attn instance list` shows this worktree's folder name under `ORIGIN`. `bridge find '{"target":{"key":"titlebar.instance"}}'` is named `<name>`, with no session yet and after `wsctl add-session --cwd $P --agent shell --label marker-shell --id marker-shell`.
- **The app starts its daemon.** With the app open, note the PID in `attn.pid` in the data folder (`./attn instance resolve --field dataDir`), then run `./attn daemon stop`: it prints `stopped daemon (pid <n>)`. Re-read `attn.pid` until it holds a new PID, with a deadline. `ps -p <pid> -o command=` shows the app's `appDaemon` running `daemon`, because the app reconnected and started the daemon with the app's environment. Do not run `./attn daemon ensure` meanwhile: it would start this checkout's daemon in the app's place.
- **Swap only the daemon.** After a Go change, `make install-daemon INSTANCE=<name>` prints `Updated bundled daemon at` the instance's daemon path and restarts it. `"$(./attn instance resolve --field appDaemon)" version --json` shows the new `sourceFingerprint`. Without a change, the binary is replaced but the running daemon keeps its PID: `daemon ensure` restarts only a daemon of another build.
- **Record the origin by hand.** Run `./attn instance set-origin <name> --worktree "$PWD"`. It prints `recorded origin for instance <name>:` and the folder, with the branch in parentheses when the checkout is on a branch. `./attn instance list --json` shows the same folder under `origin`. `./attn instance set-origin` with no name exits 1 naming `set-origin requires an instance name`.
- **Quit the app.** `./attn instance stop-app` prints `  app      ` and how the app stopped. `bridge ping` no longer answers.
- **Refusals.** `./attn instance clean` exits 1 with `attn instance: clean requires an instance name`. `./attn profile list` exits 2 with `attn profile was renamed to attn instance. Run: attn instance list`.
- **The Linux scheme.** Run `./attn instance register-scheme`. On macOS it exits 1 with `attn instance: register-scheme is Linux-only`, which is as far as this machine goes. Report the Linux behavior as unreached. On Linux it prints `>>> Registered <scheme>:// for <name>` and the desktop entry's path, and `./attn instance` shows the `handler` row as `(registered)`.
- **Clean.** `./attn instance clean <name>` prints `>>> Cleaning instance <name>`, then a line for the app, the daemon, the workers and each removed folder. `./attn instance` shows `(no daemon socket)` and `(not installed)`. `./attn instance-env --unset` prints the same `unset` lines, then `unset ATTN_INSTANCE`. The instance shell ends when [../SKILL.md](../SKILL.md)'s Cleanup removes the env file.
- **Proof.** In `$P`, keep each command with its stdout, stderr and exit code. Also keep `./attn instance resolve` and `./attn version --json`, and `bridge shot '{"path":"<P>/instance-marker.png"}'` with the marker in view, where `<P>` is the folder's path written out. Name each file for its way in, such as `instances-daemon-ensure.txt`.

## Contexts

- **Durability across restarts**: an instance's sessions, garden and settings live in its data folder and survive restarts of its daemon, app and machine. `clean` removes them. The notebook folder, `~/attn-notebook-<name>`, survives `clean`, and a later instance with the same name starts with it.
- **Linux**: the app is a directory tree instead of a macOS bundle. `attn instance register-scheme` claims the deep-link scheme in the desktop database, and `make run` starts the app in the background, logging to `<data-dir>/app.log`.
- **Profiles**: profiles live inside an instance. A new instance starts with its own profiles and sees none of production's.
- **Compatibility across versions**: `attn profile` and `attn profile-env` were renamed to `attn instance` and `attn instance-env`, and exit 2 naming the new command. `attn daemon ensure` restarts a daemon that does not match the binary, and the CLI warns when its release differs from the running daemon's.
- **Flow**: the marker sits in the title bar in either flow.

## Tests

- Stack: `cmd/attnd/daemon_ensure_stack_test.go`, `cmd/attnd/daemon_stop_stack_test.go`, `cmd/attnd/daemon_stack_test.go`, `cmd/attnd/daemon_startup_stack_test.go`, `cmd/attnd/daemon_socket_stack_test.go`, `cmd/attnd/pidlock_stack_test.go`, `cmd/attnd/instance_env_stack_test.go`, `cmd/attnd/version_stack_test.go`.
- Kernel: `cmd/attnd/instance_test.go`, `cmd/attnd/instance_origin_test.go`, `cmd/attnd/instance_pty_host_test.go`, `cmd/attnd/instance_app_stop_test.go`, `internal/daemonctl/*_test.go`, `internal/desktopentry/*_test.go`.
- Wire: `native/crates/attn-app/tests/wire/connection.rs` (a daemon of another instance is refused).

## Gotchas

- The marker cuts a long name short at 96 points, so a screenshot can show `dr…` while `bridge find` names the whole name. Prove the name with `bridge find`.
- Bare `make install`, `make run` and `make install-daemon` act on production, and need the user's approval. Always pass `INSTANCE=<name>`.
- `attn instance clean` with no instance name is refused. The default instance, production, is cleaned only with `--force`. Never pass `--force`.
- `./attn` in a shell with no instance selected, or the `attn` on `PATH`, talks to production. Most verbs print `[attn instance=<name> socket=<socket> port=<port>]` on stderr while an instance is selected (`internal/config/banner.go`). No banner means no instance is selected. `./attn instance` shows the selection.
- A routing override left in the shell, such as `ATTN_DATA_DIR` or `ATTN_SOCKET_PATH`, makes every other `attn` command refuse to run, and `./attn instance` prints `CONFLICT`. Select the instance again with `attn instance-env`, which clears them.
- Inside a session on the default home, a binary other than that home's daemon binary cannot run `attn daemon`, `attn daemon ensure`, `attn _bridge` or `attn db restore` on the home, and a session whose routing points only partly at its home cannot run them from any binary. A named instance is unaffected (`docs/internal/instances.md`).
- On macOS, `make run INSTANCE=<name>` opens the app through Launch Services, which drops the instance shell's environment. Launch with `bridge --launch` when the daemon needs the mock variables.
- `make build` replaces `./attn` for every maintainer sharing the checkout. Build in a checkout of your own when others are driving.
- `(daemon socket present)` only means the socket file exists, and the file can outlive a daemon that just stopped. Tell a restarted daemon by the PID in `attn.pid`.
- `attn instance list` shows other maintainers' instances too. Never stop, drive or clean an instance this run did not start.
- An app built from this checkout never asks GitHub for a newer release, so a newer-release notice cannot show on an instance.
- `dev` installs the enabled workflow skill into your harness homes. A named instance does not.
