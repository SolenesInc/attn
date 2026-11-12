# Native app

The native app is attn's desktop app rebuilt in Rust on
[Rune](https://github.com/anomalyco/rune). It lives in `native/`, beside the
Tauri app, until it replaces it. No release contains it yet.

## Workspace

`native/` is its own Cargo workspace:

| Crate | Holds |
| --- | --- |
| `attn-client` | the daemon link and the store; depends on `cli/protocol`, not on Rune |
| `attn-term` | terminals: the libghostty-vt bridge, input encoding, the cell renderer |
| `attn-ui` | theme tokens, fonts and shared components |
| `attn-app` | the `attn-app` binary: windows, features, actions and keymap |
| `attn-automation` | the automation server that drives a running app |
| `attn-testkit` | the scripted daemon and the world wire tests run in |

`native/rust-toolchain.toml` pins Rust 1.99.1, which Rune needs. The rest of the
repository keeps its own pin in `rust-toolchain.toml`. Run cargo from `native/`,
so rustup picks the workspace's toolchain:

```sh
cd native
cargo test
cargo run -p attn-app -- --instance <name> \
  --port "$(ATTN_INSTANCE=<name> attn instance resolve --field wsPort)" \
  --data-dir "$(ATTN_INSTANCE=<name> attn instance resolve --field dataDir)"
```

The app connects to the daemon of the named instance, whose daemon must be
running. It refuses the production instance until it carries its instance's
identity from the build.

`native/Cargo.toml` pins one revision of the `attn` branch of attn's Rune fork
and repeats Rune's `[patch.crates-io]` entries.
[The Rune fork](rune-fork.md) says why, what the fork holds, and who moves the
pin.

## Tests

Wire tests run the app's view on Rune's windowless `Runtime`, with its real
daemon link talking to a scripted daemon, and live in each crate's
`tests/wire/`. [Native wire tests](native-wire-tests.md) describes the harness.
No test waits on time: clippy rejects `std::thread::sleep`,
`Instant::now` and `SystemTime::now` anywhere in `native/`
(`native/clippy.toml`). Code reads time through an injected clock, so a test
moves it.

[Test history](test-history.md) records `cargo test` runs as the `cargo`
runner. A landing that changes `native/`, the protocol schema or `cli/protocol`
runs `make check-native` and every test of the workspace, as
[Trunk-based development](trunk-based-development.md#what-landsh-does) says.
CI's `Native (macos-15)` and `Native (ubuntu-24.04)` jobs run the same checks
when those paths change, and on every push to `main`.

## Build output

`native/.cargo/config.toml` puts each checkout's intermediate build output
outside the checkout, in `~/.cargo/attn-native-build/<checkout path>/native`.
Only the final binaries stay in `native/target`. One build directory per
checkout keeps parallel builds in different worktrees from waiting on each
other's cargo lock.

Measured on 2026-11-12 for the workspace's first crates, each build after the
one before it in one checkout, as build directory plus `native/target`:

| After | ai-sandbox (16 cores, load 20 to 48) | Mac (10 cores, load 27 to 43) |
| --- | --- | --- |
| `cargo build` | 1.7 GiB + 124 MiB, 139 s cold | 1.3 GiB + 100 MiB, 174 s cold |
| then `cargo test --no-run` | 1.9 GiB + 124 MiB, 6 s | 1.4 GiB + 100 MiB, 7 s |
| then `cargo build --release` | 3.2 GiB + 165 MiB, 180 s cold | 2.3 GiB + 129 MiB, 458 s cold |

Almost all of the build directory is Rune and its dependencies. Sharing them
between checkouts would need one build directory, and cargo locks a build
directory for the whole build, so every worktree's build would wait for every
other's. Rune's source is already shared: cargo keeps one checkout of each
pinned revision in `~/.cargo/git`.

A checkout that has built all three holds at most 3.4 GiB. Six Builders and an
Orchestrator building at once therefore hold about 24 GiB, and the cleanup's
budget of 64 GiB below leaves room for the workspace to grow. A build removed
after 3 unused days costs one cold rebuild, 85 to 174 s for a debug build.

`scripts/native-build-clean` removes, from the build root:

- the output of a checkout that no longer exists
- a build not built for 3 days
- while the root is over its budget of 64 GiB, the least recently built output

It never removes a build whose cargo lock a running build holds: it takes the
lock first and keeps it while it removes. When it removes anything or the root
is over budget, it prints the budget, the size, each build it removed, and each
build it kept because a running build holds its lock.
`ATTN_NATIVE_BUILD_BUDGET_MIB` raises the budget. `scripts/land.sh` runs it
after every push, so a removed worktree's output goes at the next land on that
machine.

## Edit-to-test loop

An edit to `attn-app`'s view, rebuilt and with its wire test rerun
(`cargo test -p attn-app --test wire`), takes 3.7 to 7.0 s on the Mac at load
33 to 35 on 10 cores, and 1.4 s on ai-sandbox at load 11 to 12 (2026-11-12). The
budget is 15 s on the Mac.
