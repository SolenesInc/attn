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
5. Add the patch's line to `FORK.md`. Name each test in the Test column by its
   path in backticks, followed by `(macOS)` when it runs only on macOS:
   `scripts/rune-bump` runs exactly those names.
6. Merge your branch into `attn` and push. Do not rewrite `attn`.
7. Name the new revision on your seed. Do not change the pin in `native/`.

## Merge upstream every week

`scripts/rune-bump` merges upstream and moves the pin in one command, from an
attn checkout on a clean `main`:

```sh
scripts/rune-bump
```

It works in a clone of Rune at `~/projects/rune` (set `RUNE_CLONE` for another)
and fetches upstream and the fork by URL, so the clone's remotes do not matter.
In order, it:

1. fast-forwards the fork's `dev` to upstream's `dev` and pushes it;
2. merges `dev` into `attn` in a temporary worktree of the clone;
3. runs every test `FORK.md` names, on this machine's OS, and fails when one
   fails or does not run, so a stale ledger line is caught;
4. pushes `attn`;
5. fails when the crates Rune's `[patch.crates-io]` names at the new revision
   differ from those `native/Cargo.toml` repeats;
6. replaces the rev in `native/Cargo.toml`, runs `cargo update -p rune` and the
   native suite (`make test-native`).

It stops at the first step that fails, names it and prints its output. Nothing
is pushed to `attn` before its tests pass. The pin move is left uncommitted:
commit it and land it with `scripts/land.sh`, which also runs the native suite
on CI's Linux. A move whose native suite fails does not land.

When the merge conflicts, the script stops. Merge by hand in a worktree of
`solenes/attn`: resolve the patched areas, and drop a patch whose bug upstream
has fixed, with its line in `FORK.md`. Push `attn`, then move the pin with
`scripts/rune-bump --rev <merged rev>`. The `fork macOS tests` workflow runs the
macOS-only patch tests on that push.

## Move the pin

Builders never move the pin. The weekly bump or the Orchestrator moves it, and
a bump that fails the native suite does not land.

- `scripts/rune-bump --rev <rev>` moves the pin to a revision already on the
  fork's `attn`, without merging. It refuses a revision that is not on `attn`.
- `scripts/rune-bump --branch <fork-branch>` merges a fork branch, such as a
  Builder's patch branch, into `attn`, tests and pushes it, then moves the pin.

An app that depends on Rune repeats Rune's `[patch.crates-io]` entries in its
own workspace, because cargo ignores a dependency's `[patch]` section. When
Rune's entries change, the script stops before moving the pin: copy them into
`native/Cargo.toml`, pointed at the fork's pinned revision, commit, and run it
again with `--rev <new rev>`.
