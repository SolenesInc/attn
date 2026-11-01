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
`~/.local/share/attn-linux-runner/<checkout hash>/source`.

**Sync replaces changes made inside that managed checkout.** Edit on the host.
Guest build caches excluded by the repository's ignore rules survive sync.
A lock prevents sync and commands from changing the same checkout concurrently.

`run` uses the last synced source, so run `sync` after you edit. `build` syncs
and installs the packaged app. The app's existing build fingerprint check
rejects stale installs. Commands run with a clean environment and the named
`linux-<checkout hash>` instance. `--instance` selects another non-production
name. The guest receives no host routing variables or provider credentials.

`test` syncs, removes the guest instance with `attn instance clean`, installs the
instance again, and runs the existing serial matrix under Xvfb, so every run
starts from empty instance data, like CI. `test` copies the artifacts even when a scenario fails, and it exits with the failing command's exit status. Pass `--out <directory>` to choose the results directory. Without `--out`, `test` writes results under the host temporary directory in `attn-linux-artifacts/<provider>/<checkout hash>`.

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

## Extending providers

Add an adapter to `app/scripts/real-app-harness/vm/providers.mjs` with `up`,
`stop`, `status`, and `connection`. `connection` returns a target and an optional
SSH config path. Keep provider-specific commands in the adapter. Scenarios and
guest setup must not depend on which VM manager launched the machine.

Tool versions come from `go.mod`, `rust-toolchain.toml`, and `.tool-versions`. Node 26 and pnpm 9 match the CI major versions.
