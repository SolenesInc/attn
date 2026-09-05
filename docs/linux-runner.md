# Local Linux runner

Run Linux sandbox tests and the packaged app from macOS through a dedicated VM.
The runner supports Lima, OrbStack, and existing SSH machines (including VMs
created in other tools). Each provider supplies lifecycle operations and an SSH
connection. Source sync, Ubuntu provisioning, execution and artifact collection
are shared.

## Lima

Install Lima with `brew install lima`, then run from the repository:

```sh
pnpm --dir app real-app:linux provision
pnpm --dir app real-app:linux run -- bash -c 'cd plugins/attn-pi && bun install --frozen-lockfile && bun test'
pnpm --dir app real-app:linux build
pnpm --dir app real-app:linux test -- --scenario pi-automode
```

`provision` creates or starts `attn-linux` and installs the tools. The template
in `app/scripts/real-app-harness/vm/lima.yaml` uses Ubuntu 24.04 with Apple's VZ
backend, 8 CPUs, 16 GiB RAM, and an 80 GiB disk. It mounts no host directories
and disables SSH agent forwarding. Edit the template before creating another VM;
existing machines keep their settings. Use Lima's own configuration editor to
resize an existing VM.

An Apple Silicon host runs ARM64 Linux. Keep x86_64 verification in CI.

## Other providers

```sh
pnpm --dir app real-app:linux provision --provider orb --name attn-linux
pnpm --dir app real-app:linux provision --provider ssh --target tester@linux
pnpm --dir app real-app:linux test --provider ssh --target tester@linux -- --scenario pi-automode
```

The SSH provider can use `--ssh-config <file>`. It provisions an existing Ubuntu
24.04 machine with passwordless sudo; it does not create or stop that machine.
The Lima and OrbStack providers own creation and start/stop. No adapter deletes VMs.

Set `ATTN_LINUX_PROVIDER`, `ATTN_LINUX_VM`, `ATTN_LINUX_SSH_TARGET`, and
`ATTN_LINUX_SSH_CONFIG` to avoid repeating options. CLI options take precedence.

## Source, profiles and results

`sync`, `provision` and `build` copy the current HEAD and staged, unstaged and
non-ignored new files. Git configuration, hooks, host tool caches and ignored
files are not copied. Git history travels in a bundle, without host credentials.
The guest keeps a separate managed checkout per host checkout under
`~/.local/share/attn-linux-runner/<checkout hash>/source`.

**Sync replaces changes made inside that managed checkout.** Edit on the host.
Guest build caches excluded by the repository's ignore rules survive sync.
A lock prevents sync and commands from changing the same checkout concurrently.

`run` and `test` use the last synced source. Run `sync` after edits for unit tests,
or `build` before packaged-app scenarios. The existing app build fingerprint check
rejects stale installs. Commands run with a clean environment and the named
`linux-<checkout hash>` profile; `--profile` selects another non-production name.
The guest receives no host routing variables or provider credentials.

`test` runs the existing serial matrix under Xvfb and copies its artifacts even
when a scenario fails. It preserves the failing command's exit status. Pass
`--out <directory>` to choose where results land; by default they go under the
host temporary directory in `attn-linux-artifacts/<provider>/<checkout hash>`.
`artifacts` repeats collection after an interrupted run. For shell operators,
use an explicit shell: `run -- bash -c 'command && next-command'`.

```sh
pnpm --dir app real-app:linux status
pnpm --dir app real-app:linux connection
pnpm --dir app real-app:linux clean
pnpm --dir app real-app:linux stop
```

`clean` removes only the selected installed profile using the guest CLI.
`stop` preserves source and caches. `up` starts the VM again without provisioning.

## Remote endpoint scenarios

The `tr*` scenarios run the app locally and connect to a second machine. Install
only their mock-agent fixtures with `real-app:linux fixtures`. Their
`ATTN_HARNESS_REMOTE_SSH_TARGET` must be an alias that ordinary `ssh` can resolve,
since the app's daemon also connects to it.

For Lima, `real-app:linux connection` prints the generated SSH config path.
Include that file in your SSH configuration and use `lima-attn-linux` as the
target. The runner itself passes the config explicitly and needs no SSH config edit.
The legacy `real-app:provision-remote` command still provisions `attn-remote@orb`.

## Extending providers

Add an adapter to `app/scripts/real-app-harness/vm/providers.mjs` with `up`,
`stop`, `status` and `connection`. `connection` returns a target and optional SSH
config path. Keep provider-specific commands there; scenarios and guest setup
should not know which VM manager launched the machine.

The shared `scripts/setup-linux-sandbox.sh` is also used by CI. It grants user
namespaces to `/usr/bin/bwrap` through AppArmor while keeping Ubuntu's global
restriction enabled. Tool versions come from `go.mod`, `rust-toolchain.toml`,
and `.tool-versions`; Node 22 and pnpm 9 match the CI major versions.
