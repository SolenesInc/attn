# The Rune fork

`native/` builds on attn's own fork, `SolenesInc/rune`. Change it whenever that
makes attn simpler or better, and delete whole unused areas rather than pieces
(deleted areas can no longer take upstream picks). Cherry-pick from upstream
`anomalyco/rune`; never merge upstream into the fork.

**Never open issues, pull requests or discussions on `anomalyco/rune`.**

## Branches and `FORK.md`

- `main`: fork trunk; `native/Cargo.toml` pins a commit here. Never rebase or
  force-push `main`.
- `dev`: fast-forward mirror of upstream `dev` (picks come from here).
- Other apps' branches (such as `lens-fixes`): leave alone.
- `FORK.md` at the fork root is the ledger ("Our changes", "Picked from
  upstream", "Last upstream commit reviewed"). Every commit on `main` not in
  upstream `dev` must record itself by updating `FORK.md` (with its test names
  in backticks, suffixed `(macOS)` or `(Linux)` if OS-only).
- **Union-merge trap:** `.gitattributes` merges `FORK.md` by union, which keeps
  both versions of an edited line. `scripts/rune-bump` rejects duplicate lines;
  collapse any pair into one line on your branch before bumping.

## Changing Rune and moving the pin

Run `scripts/rune-bump --help` for full behavior (`RUNE_CLONE` defaults to
`~/projects/rune`).

1. **Own change or conflicting pick:** branch from `solenes/main`, add a test
   that fails without the change (overlay tests go in
   `crates/rune/src/overlay/fork_tests.rs`), run
   `scripts/test.sh -p rune -p rune-kit -p rune-mermaid -p rune-perf --features rune-kit/markdown,rune-kit/mermaid`,
   add the `FORK.md` row in the same commit, push the branch, then from your
   attn checkout run:

   ```sh
   scripts/rune-bump --branch <fork-branch>
   ```

2. **Clean upstream pick (Orchestrator):** from a clean attn checkout on `main`:

   ```sh
   scripts/rune-bump --pick <upstream-commit>...
   ```

   If a pick conflicts, port it as an own change on a branch instead.
3. **Existing `main` revision:** `scripts/rune-bump --rev <rev>`.
4. Commit the uncommitted pin move together with the attn-side change and land
   both with `land`. Land promptly after `rune-bump` pushes `main` so other
   agents merging onto `main` do not build against a breaking Rune change
   without your attn-side update.

## Traps

- **`[patch.crates-io]` sync:** Cargo ignores a dependency's `[patch]` section,
  so `native/Cargo.toml` repeats Rune's `[patch.crates-io]` entries. If
  `scripts/rune-bump` fails on a mismatch, copy the new entries into
  `native/Cargo.toml` at the currently pinned rev, commit, and rerun.
- **No `impl<S> Runtime<S>`:** write runtime methods on `impl RuntimeCore`
  (non-generic) instead of `Runtime<S>` outside `runtime/app_side.rs`
  (enforced by `scripts/state-free.sh`). Generic `Runtime<S>` monomorphized
  across 271 test state types and spiked builds to 24.8 GB.
- **Cold test memory:** a cold checkout peaks near ~19 GB RAM building
  integration tests in parallel; pass `-j` to `scripts/test.sh` if multiple
  cold runs share a 64 GB host.
- **Weekly upstream review:** `.agents/skills/rune-upstream-review` uses
  `scripts/rune-upstream list` and `scripts/rune-upstream reviewed <commit>`.
