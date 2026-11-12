# The Rune fork

The native app in `native/` builds on [Rune](https://github.com/anomalyco/rune).
Upstream lands about 99 commits a day on `dev`, never rewrites that branch, and
takes no pull requests. attn therefore builds against its own fork,
[SolenesInc/rune](https://github.com/SolenesInc/rune), and pins one revision of
it.

## Branches

- `dev` mirrors upstream's `dev`. It only fast-forwards.
- `attn` is upstream's `dev` with attn's patches on top. `native/` pins a
  revision of this branch. Upstream is merged into it and it is never rebased
  or force-pushed, because every revision an app has pinned must stay
  reachable.
- The fork's other branches belong to other work, such as `lens-fixes`, which
  Attn Lens pins. Leave them alone.

`FORK.md` at the root of the `attn` branch lists every patch: what it changes,
why the app cannot do it, and its test.

## Decide whether a change belongs in the fork

Put a change in the fork only when the app cannot do the job through Rune's
public API or the raw window handle. Everything else lives in `native/`. Each
patch costs a merge risk every week: when upstream makes a patch unneeded,
remove the patch and its line in `FORK.md` at the next merge.

Do not open issues, pull requests or discussions on `anomalyco/rune`.

## Add a patch

1. Clone the fork, or add it as a remote to a clone of upstream:
   `git remote add solenes git@github.com:SolenesInc/rune.git`.
2. Branch from `solenes/attn`.
3. Write the change with a test that fails without it. Put tests in the sibling
   test file Rune's `AGENTS.md` names; overlay tests of fork patches go in
   `crates/rune/src/overlay/fork_tests.rs`.
4. Run `cargo test -p rune -p rune-kit` for the areas you touched. A test that
   runs only on macOS runs in the fork's `fork macOS tests` workflow
   (`.github/workflows/fork-macos.yml`), on each push to `attn` and on request.
5. Add the patch's line to `FORK.md`.
6. Merge your branch into `attn` and push. Do not rewrite `attn`.
7. Name the new revision on your seed. Do not change the pin in `native/`.

## Merge upstream every week

1. Fast-forward the fork's `dev` to upstream's `dev` and push it.
2. Merge `dev` into `attn`. Resolve conflicts in the patched areas, and drop a
   patch whose bug upstream has fixed.
3. Run the tests of every patch in `FORK.md`, on Linux and in the macOS
   workflow.
4. Push `attn`.

## Move the pin

Builders never move the pin. The weekly bump or the Orchestrator moves it, and
a bump that fails the native suite does not land.

An app that depends on Rune repeats Rune's `[patch.crates-io]` entries in its
own workspace, because cargo ignores a dependency's `[patch]` section. Copy
them from the pinned revision's `Cargo.toml` whenever the pin moves.
