# Native app

The native app is attn's desktop app rebuilt in Rust on
[Rune](https://github.com/anomalyco/rune). It lives in `native/`, beside the
Tauri app, until it replaces it. No release contains it yet.

## Build output

`native/.cargo/config.toml` puts each checkout's intermediate build output
outside the checkout, in `~/.cargo/attn-native-build/<checkout path>/native`.
Only the final binaries stay in `native/target`. One build directory per
checkout keeps parallel builds in different worktrees from waiting on each
other's cargo lock.

Measured on ai-sandbox (16 cores, load 20 to 48) on 2026-11-12, for the
workspace's first crates on Rune `f3ab74c1`:

| After | Build directory | `native/target` | Time |
| --- | --- | --- | --- |
| `cargo build` | 1.7 GiB | 124 MiB | 139 s cold |
| then `cargo test --no-run` | 1.9 GiB | 124 MiB | 6 s |
| then `cargo build --release` | 3.2 GiB | 165 MiB | 180 s cold |

Almost all of the build directory is Rune and its dependencies. Sharing them
between checkouts would need one build directory, and cargo locks a build
directory for the whole build, so every worktree's build would wait for every
other's. Rune's
source is already shared: cargo keeps one checkout of each pinned revision in
`~/.cargo/git`.

A 400-line Rune app's `target/` held 4.9 GiB after a debug and a release build
on the Mac. Six Builders and an Orchestrator building at once therefore hold
about 35 GiB, under the cleanup's budget of 64 GiB below. A build removed
after 3 unused days costs one cold rebuild, 85 to 139 s for a debug build on
ai-sandbox.

`scripts/native-build-clean` removes, from the build root:

- the output of a checkout that no longer exists
- a build not built for 3 days
- while the root is over its budget of 64 GiB, the least recently built output

It never removes a build whose cargo lock a running build holds: it takes the
lock first and keeps it while it removes. When it removes anything or the root
is over budget, it prints the budget, the size, each build it removed, and each
build it kept because a running build holds its lock.
`ATTN_NATIVE_BUILD_BUDGET_MIB` raises the budget. `scripts/land.sh` runs it
after every push.
