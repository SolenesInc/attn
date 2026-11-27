# Corrected failure classes

Check this file only when the exact same failure mechanism has repeated on
`main` and you are deciding whether to plant a
[Correction](../../../internal/prompts/content/skills/attn-workflow/references/correction.md)
seed.

## Fast-landing boundary

- Plant a Correction seed only when a fix prevents the class with **no
  meaningful time added to normal landings**: a product fix, a test or harness
  fix, a CI fix, a compile or lint check, or a test in `scripts/landing-tests`
  that runs in seconds.
- Do not plant a Correction seed when catching the class before push would slow
  down most landings (for example, running untouched scenarios or extra
  cross-platform test suites on every push). Repair those breaks as they happen.
  - **Precedent (`s-fyn95b`, 2026-11-20)**: a product change to shared code
    alters what an untouched scenario expects and fails on `main` (such as
    `060f446b9` restyling the close-desktop dialog and failing
    `native-close-desktop`). Every rule that caught shared-code scenario breaks
    added minutes to native landings, and the user declined that cost.

When a repeating class passes this boundary, plant the Correction seed with
`--discovered-from <failure seed>`, state the fast-landing constraint in the
seed body (and name the broken guard if the class is listed below), and send
the seed to the chief with `attn seed send-to-chief <seed>`.

## Guarded classes

If a failure belongs to a class below, the guard did not hold.

| Class | Instance | What prevents it |
|---|---|---|
| A package host a job is not testing turns App acceptance red | Run `41902160029`, shard 2/10: `apt download exceeded 300s`, no test failed | `setup-linux-app-deps`: shards restore a frozen apt layer and install with network sources removed. Only the build jobs and Native reach the mirror on a cache miss |
| A landing adds or changes a scenario that passed only on the author's Mac and fails on Linux CI | `729fdd236`, `SHELF-MOVES`: `the active tile sits at the end of the screen` | `scripts/land-linux.sh`: `land` (via `scripts/land-gate.sh`) runs each added or changed catalog scenario on Linux before pushing |
| A scenario leaves a home setting changed and breaks a later scenario in its shard | `68c6748f8`, `NATIVE-SESSION-CREATION`: `native-settings` left `projects_directory` set | `run-serial-matrix.mjs` restores every stored home setting a scenario changed through the daemon and fails the scenario when a restored value differs |
| A native test passes on Linux and fails on macOS | `56bc85c94`, `shelf_peek`: `at (800.0, 600.0) the shelf keeps the cap over the columns after the screen` | One platform value: the wire suite acts as a Mac on every host through `rune::Platform` (`RUNE_PLATFORM=linux` for CI's Linux run). Repository fonts: no installed font loads in a wire test. `scripts/native-host-reads` in `make check-native` fails on a host read outside the OS files `native_macos` lists |
| A path loses its machine before a filesystem lookup | `028fc6ce9` fixed the home's probes of outpost worktrees. `ce7a3ec85` passed a home symlink resolver to Garden for saved outpost paths. | Use the path's machine for symlink resolution. `scripts/remotepathlint` rejects raw resolver references outside the daemon's machine API and unguarded filesystem calls on saved binding paths. |
| A `cli/` change passes `land` and fails clippy on CI's CLI jobs | `2113370ce`, `cli/attn/src/commands/home.rs`: 8 errors, such as `` `format!(..)` appended to existing `String` `` | `make check-cli` runs the CLI job's fmt and clippy commands, and `scripts/land-gate.sh` runs it when a landing changes `cli/` |
