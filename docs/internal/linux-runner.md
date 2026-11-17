# Local Linux runner

Run the packaged app on Linux from macOS through a dedicated VM.
The runner supports Lima, OrbStack, and existing SSH machines (including VMs
created in other tools). Each provider supplies lifecycle operations and an SSH
connection. All providers share source sync, Ubuntu provisioning, execution, and
artifact collection.

## Lima

Install Lima with `brew install lima`, then run from the repository:

```sh
pnpm --dir app real-app:linux provision
pnpm --dir app real-app:linux test -- --scenario linux-shortcuts
```

`provision` creates or starts `attn-linux` and installs the tools. The template
in `app/scripts/real-app-harness/vm/lima.yaml` uses Ubuntu 24.04 with Apple's VZ
backend, 8 CPUs, 16 GiB RAM, and an 80 GiB disk. The template mounts no host
directories and disables SSH agent forwarding. Edit the template before you
create another VM. Existing machines keep their settings. Use Lima's own
configuration editor to resize an existing VM.

An Apple Silicon host runs ARM64 Linux. Keep x86_64 verification in CI.

## Other providers

```sh
pnpm --dir app real-app:linux provision --provider orb --name attn-linux
pnpm --dir app real-app:linux provision --provider ssh --target tester@linux
pnpm --dir app real-app:linux test --provider ssh --target tester@linux -- --scenario linux-shortcuts
```

The SSH provider accepts `--ssh-config <file>`. It provisions an existing Ubuntu 24.04 machine. The machine must allow passwordless sudo. The SSH provider does not create or stop that machine.
The Lima and OrbStack providers create, start, and stop their VMs. No adapter
deletes VMs.

To avoid repeating options, set `ATTN_LINUX_PROVIDER`, `ATTN_LINUX_VM`,
`ATTN_LINUX_SSH_TARGET`, and `ATTN_LINUX_SSH_CONFIG`. CLI options take
precedence.

## Source, instances and results

`sync`, `provision`, and `build` copy the current HEAD and the staged, unstaged,
and non-ignored new files. These commands do not copy Git configuration, hooks,
host tool caches, or ignored files. They send Git history in a bundle, without
host credentials. The guest keeps a separate managed checkout per host checkout
under
`~/.local/share/attn-linux-runner/<checkout>/source`. `<checkout>` is a hash of
the host checkout's path; `--checkout <name>` picks another, so several host
checkouts can share one guest checkout and its build output.

**Sync replaces changes made inside that managed checkout.** Edit on the host.
Guest build caches excluded by the repository's ignore rules survive sync.
A lock prevents sync and commands from changing the same checkout concurrently.
`test` and `soak` take turns on a checkout: a run that finds another one using
it says so and waits, from any host, until that run ends.

`run` uses the last synced source, so run `sync` after you edit. `build` syncs
and installs the packaged app. The app's existing build fingerprint check
rejects stale installs. Commands run with a clean environment and the named
`linux-<checkout>` instance. `--instance` selects another non-production
name. The guest receives no host routing variables or provider credentials.

`test` syncs, removes the guest instance with `attn instance clean`, installs the
instance again, and runs the existing serial matrix under Xvfb, so every run
starts from empty instance data, like CI. `test` copies the artifacts even when a scenario fails, and it exits with the failing command's exit status. Pass `--out <directory>` to choose the results directory. Without `--out`, `test` writes results under the host temporary directory in `attn-linux-artifacts/<provider>/<checkout>`.

`soak --scenario <id>` runs one scenario the way CI's Acceptance soak does: it
syncs, cleans the instance, installs the app the scenario needs (Tauri or
native), and runs the scenario once under Xvfb. It also runs scenarios that the
serial matrix leaves to the soak. Artifacts and the exit status work as for
`test`.

After its scenarios, `test` and `soak` run `scripts/native-build-clean` on the
guest, which keeps native build output within the budget
[Native app](native-app.md#build-output) describes, as `land.sh` does after a
push.

The runner starts Xvfb with `-noreset`, so short-lived window probes cannot reset the display while GTK initializes the app. CI acceptance and soak runs use the same setting.

The runner saves the packaged app's stdout and stderr as `app-launch-<id>.log` in the artifacts directory. For the serial matrix, the artifacts directory is the artifacts root. If the app exits early, the runner reports the exit code, the signal, and the log path.

After an interrupted run, run `artifacts` to collect the artifacts again.

To use shell operators with `run`, start an explicit shell: `run -- bash -c 'command && next-command'`.

```sh
pnpm --dir app real-app:linux status
pnpm --dir app real-app:linux connection
pnpm --dir app real-app:linux clean
pnpm --dir app real-app:linux stop
```

`clean` removes only the selected installed instance, using the guest CLI.
`stop` preserves source and caches. `up` starts the VM again without provisioning.

## Landing runner

`scripts/land.sh` can run a landing's scenarios on a Linux machine of your own
instead of the Acceptance soak workflow. The machine keeps its build output
between landings, so a landing waits for an incremental build instead of a
cold one on a fresh GitHub runner.

1. Take an Ubuntu 24.04 machine you reach with `ssh <target>` without a
   password prompt, and whose user has passwordless sudo.
2. Provision it from a checkout of attn:
   `pnpm --dir app real-app:linux provision --provider ssh --target <target>`.
   It installs CI's app packages from
   `.github/actions/setup-linux-app-deps/packages.txt` and the pinned Go, Node,
   pnpm, bun and Rust. A machine that already has Node keeps it; the runner
   uses its own.
3. In the clone you land from, run `git config attn.landRunner ssh:<target>`.
   Every worktree of the clone uses it. `git config --unset attn.landRunner`
   goes back to GitHub.

Each scenario then runs as `real-app:linux soak --checkout land` on that
machine, from an empty instance. Every landing, from any clone or host, uses
that one guest checkout and its build output, so the machine holds one build
of each app however many worktrees land. Landings take turns on it, one
scenario at a time.

The landing prints where it keeps each scenario's log from the machine.
[What land.sh does](trunk-based-development.md#what-landsh-does) says what
happens when the machine is down or a scenario fails there.

Reprovision after a change to `packages.txt`, `go.mod`, `.tool-versions` or a
`rust-toolchain.toml`.

## Extending providers

Add an adapter to `app/scripts/real-app-harness/vm/providers.mjs` with `up`,
`stop`, `status`, and `connection`. `connection` returns a target and an optional
SSH config path. Keep provider-specific commands in the adapter. Scenarios and
guest setup must not depend on which VM manager launched the machine.

Tool versions come from `go.mod`, `rust-toolchain.toml`, and `.tool-versions`. Node 26 and pnpm 9 match the CI major versions.
