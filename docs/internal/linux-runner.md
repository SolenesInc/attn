# Local Linux runner

`pnpm --dir app real-app:linux <cmd>` runs the Linux packaged app and scenarios
on a Lima VM, OrbStack VM, or SSH Ubuntu 24.04 host (passwordless sudo
required). Run `pnpm --dir app real-app:linux --help` for subcommands and
provider flags (`ATTN_LINUX_PROVIDER`, `ATTN_LINUX_VM`,
`ATTN_LINUX_SSH_TARGET`, `ATTN_LINUX_SSH_CONFIG`).

```sh
pnpm --dir app real-app:linux provision --provider ssh --target attn-linux
pnpm --dir app real-app:linux test --provider ssh --target attn-linux -- --scenario linux-shortcuts
pnpm --dir app real-app:linux soak --provider ssh --target attn-linux --scenario native-sidebar-session
```

## Traps

- **Sync overwrites guest edits:** `sync`, `provision`, `build`, `test` and
  `soak` copy HEAD plus working-tree changes into
  `~/.local/share/attn-linux-runner/<checkout>/source`. Always edit on the
  host; ignored build caches survive sync.
- **Sync evicts idle checkouts:** each sync first removes the other checkout
  folders on the guest whose lock, turn file and folder have gone 7 days
  without use, unless a run holds the checkout or a process works inside it.
  A checkout that comes back after that starts from a fresh clone and cold
  build caches.
- **Keep diagnostic runs off the landing runner:** send repeat runs, soaks and
  reproductions to `attn-linux` (`--provider ssh --target attn-linux`), never
  to the primary landing runner, which landings queue for.
- **Apple Silicon Lima/OrbStack VMs run ARM64 Linux:** leave x86_64
  verification to CI.
- **Reprovision after toolchain changes:** rerun `provision` after edits to
  `.github/actions/setup-linux-app-deps/packages.txt`, `go.mod`,
  `.tool-versions` or `rust-toolchain.toml`.
- **Shell operators in `run`:** pass an explicit shell, e.g.
  `run -- bash -c 'cmd1 && cmd2'`.

## Landing runner

`scripts/land-linux.sh` runs a landing's selected scenarios on a warm SSH
machine instead of GitHub's Acceptance soak workflow when configured:

```sh
git config attn.landRunner ssh:<target>          # first choice (applies to all worktrees of the clone)
git config --add attn.landRunner ssh:<target2>   # fallback machine (put shared/busy hosts last)
git config --unset-all attn.landRunner           # return to GitHub Actions
```

Landings share `--checkout land` and take turns (waiting up to 654 s by
default, override with `ATTN_LAND_RUNNER_TURN_LIMIT=<seconds>`, before falling
back to GitHub Actions). A scenario that fails on the landing runner is re-run
on GitHub's workflow, and only that run blocks the landing.
