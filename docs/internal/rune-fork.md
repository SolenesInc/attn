# The Rune fork

The native app in `native/` builds on Rune, through attn's fork of it:
[SolenesInc/rune](https://github.com/SolenesInc/rune). The fork is attn's own
library. We change it whenever that makes attn simpler or better, and we delete
from it what attn does not use. Upstream,
[anomalyco/rune](https://github.com/anomalyco/rune), is a source of changes we
cherry-pick. We do not merge upstream into the fork.

Do not open issues, pull requests or discussions on `anomalyco/rune`.

## Branches

- `main` is the trunk and the fork's default branch. `native/` pins one
  revision of it. Nobody rebases or force-pushes `main`, because every revision
  an app has pinned must stay reachable.
- `dev` mirrors upstream's `dev`. It only fast-forwards and takes no commits of
  ours. Picks come from it.
- Work branches start from `main`. Other apps' branches, such as `lens-fixes`
  for Attn Lens, belong to their own work. Leave them alone.

`FORK.md` at the root of `main` is the ledger. It has three parts: our changes,
each with its tests; the upstream commits we picked; and the last upstream
commit someone reviewed for picks. `scripts/rune-bump` runs every test it
names, and fails when `FORK.md` does not record a commit on `main` that
upstream's `dev` lacks. A commit records itself by changing `FORK.md`. Changes
made before that rule list their commits by hash.

The fork's `.gitattributes` merges `FORK.md` by union, so branches that each
append a row merge without a conflict. Union also keeps both versions of a line
that both sides changed, such as a row two branches extend, so
`scripts/rune-bump` refuses a `main` whose `FORK.md` still holds both versions,
and names them. Make each pair into one line on the branch, in any later commit.

## Change the library

Put a change in the fork when it makes attn simpler or better there than in
`native/`: a bug fix, an API the app needs, or code that removes plumbing from
the app. Deleting code attn does not use counts too; delete whole areas rather
than pieces, since a deleted area can no longer take picks.

1. Clone the fork, or add it as a remote to a clone of upstream:
   `git remote add solenes git@github.com:SolenesInc/rune.git`.
2. Branch from `solenes/main`.
3. Write the change with a test that fails without it. Put tests in the sibling
   test file the fork's `AGENTS.md` names; overlay tests go in
   `crates/rune/src/overlay/fork_tests.rs`.
4. Run `scripts/test.sh -p rune -p rune-kit -p rune-mermaid --features rune-kit/markdown,rune-kit/mermaid`
   for the areas you touched. `scripts/test.sh` passes its arguments to
   `cargo test` and lets one run at a time per machine, as
   [Test builds and memory](#test-builds-and-memory) explains; it says whose
   run it waits for. A test that runs only on macOS runs in the fork's
   `fork macOS tests` workflow (`.github/workflows/fork-macos.yml`), on each
   push to `main` and on request.
5. Add a row under "Our changes" in `FORK.md`: what the change does, why it
   lives in Rune, and its tests in backticks, followed by `(macOS)` when a test
   runs only on macOS. Name a unit test by its path in its crate, such as
   `overlay::tests::a_press_before_the_first_frame_after_show_reaches_the_view`, and an integration test by its
   file, such as `crates/rune/tests/press_keeps_focus.rs`, which runs the whole
   file, or with `::<test>` after the file to run one of its tests. Commit the
   row with the change; a later commit to the same change extends its row.
6. Push the branch and name it on your seed. The Orchestrator brings it into
   `main` with `scripts/rune-bump --branch <branch>`.

## Take a change from upstream

Take upstream changes one named commit at a time, by cherry-pick, never by
merging `dev`. Watch the areas where upstream's work helps attn most: text and
IME, Linux and Wayland, and frame pacing.

A Builder asked for an upstream change picks it on a branch from `main`:
`git cherry-pick -x <commit>`, then adds a row under "Picked from upstream" in
`FORK.md` in the same commit, pushes the branch and names it on the seed, as for
a change of our own. When the commit no longer applies, write it as our own
change instead, with a test and a row under "Our changes" that names the upstream
commit it follows.

The Orchestrator takes picks straight onto `main`, from an attn checkout on a
clean `main`:

```sh
scripts/rune-bump --pick <upstream commit>...
```

It works in a clone of Rune at `~/projects/rune` (set `RUNE_CLONE` for another)
and fetches upstream and the fork by URL, so the clone's remotes do not matter.
In order, it:

1. refuses a commit that is not on upstream's `dev`, then fast-forwards the
   fork's `dev` to upstream's;
2. cherry-picks each commit, in the order given, onto `main` in a temporary
   worktree with `git cherry-pick -x`, and records it under "Picked from
   upstream" in `FORK.md` in the same commit;
3. checks that `FORK.md` records every commit on `main`;
4. runs every test `FORK.md` names, through the new `main`'s
   `scripts/test.sh` on this machine's OS, and fails when one fails or does not
   run, so a stale row is caught;
5. pushes `main`;
6. moves the pin, as below.

A pick that conflicts stops the script, and nothing is pushed. Port it on a
branch as a Builder would, then run `scripts/rune-bump --branch <branch>`.

When you read upstream's new commits for picks, move the "Last upstream commit
reviewed" line in `FORK.md` to the last one you read.

## Move the pin

Builders never move the pin. The Orchestrator moves it, and a move whose native
suite fails does not land.

- `scripts/rune-bump --pick <commit>...` picks from upstream, then moves the pin.
- `scripts/rune-bump --branch <fork-branch>` merges a fork branch, such as a
  Builder's, into `main`, checks the ledger, runs its tests, pushes `main` and
  moves the pin. It refuses a branch that carries commits of upstream's `dev`
  that `main` lacks, and one whose `FORK.md` keeps two versions of a line.
  When the merge conflicts, bring the branch up to `main`, resolve, and push it
  again.
- `scripts/rune-bump --rev <rev>` moves the pin to a revision already on
  `main`. It refuses a revision that is not on `main` or whose ledger lacks a
  row.

Before moving the pin, the script fails when the crates Rune's
`[patch.crates-io]` names at the new revision differ from those
`native/Cargo.toml` repeats. An app that depends on Rune repeats those entries
in its own workspace, because cargo ignores a dependency's `[patch]` section.
Copy Rune's entries into `native/Cargo.toml`, pointed at the fork's new
revision, commit, and run `scripts/rune-bump --rev <new rev>`.

The script then replaces the rev in `native/Cargo.toml`, runs
`cargo update -p rune` and the native suite (`make test-native`), and leaves the
pin move uncommitted. Commit it and land it with `scripts/land.sh`, which runs
the native suite again on CI's Linux. The script stops at the first step that
fails, names it and prints its output.

## Test builds and memory

Compiling `rune`'s lib tests is one `rustc` that peaks at 24 GB of resident
memory and takes about 9 minutes on ai-sandbox (22 cores, 64 GB), measured
2026-11-13 on `main` at `caf8af7d` and `b9d85323`. The rest of the ledger's
build stays near 2 GB. Several agents running the ledger at once exhausted
ai-sandbox's memory, so `scripts/test.sh` lets one run at a time per machine,
under a lock in `~/.cargo`: Rune's test builds on a machine now take 24 GB at
most, not 24 GB for each agent that runs one.

The peak comes from monomorphization. The runtime is generic over the app
state, `Runtime<S>`, and the tests use 271 state types, so the test build
instantiates 2.43 million items where the library alone instantiates 78,000.
Collecting them takes 160 s and 6.8 GB, and generating LLVM IR for them takes
the rest. Build settings barely move it:

| Setting | Peak | Wall time |
|---|---|---|
| defaults (`debug = "line-tables-only"`, incremental) | 24.0 GB | 595 s |
| `debug = false` | 21.6 GB | 486 s |
| incremental off | 28.7 GB | 475 s |
| `debug = false`, incremental off | 22.6 GB | 403 s |
| `-j8` or `-j4` | 24.4 GB, 24.1 GB | 537 s, 648 s |
| `codegen-units = 1024` | 24.3 GB | 591 s |

Lowering the peak means taking the work that does not need `S` out of the
generic runtime, as `runtime/commit/text_layout.rs` does for Taffy's layout.
