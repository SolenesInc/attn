# Native app

The desktop app lives in `native/` (its own Cargo workspace on attn's
[Rune fork](rune-fork.md), pinned to `rust-toolchain.toml`).

## Workspace and builds

| Crate | Holds |
| --- | --- |
| `attn-client` | daemon link and store (depends on `cli/protocol`, not Rune) |
| `attn-term` | `libghostty-vt` bridge, input encoding, cell renderer |
| `attn-ui` | color schemes, fonts, shared controls and page blocks |
| `attn-app` | `attn-app` binary: windows, features, actions, keymap |
| `attn-automation` | loopback automation server |
| `attn-testkit` | scripted daemon and wire-test world |

- Run `cargo` from `native/`. `make test-native` builds `libghostty-vt` if
  missing, then tests the whole workspace.
- A bare `cargo run` refuses to start because the instance name, port and
  bundle id are baked in at build time. Install onto a named instance with
  `make install INSTANCE=<name>` ([Instances](instances.md)), or build the
  release bundle with `make build-app` (`native/target/staged/attn.app`).
- Intermediate build output lives outside the checkout at
  `~/.cargo/attn-native-build/<checkout path>/native`
  (`native/.cargo/config.toml`) so parallel worktrees do not contend on Cargo's
  lock. Never delete another checkout's build directory while builds may run;
  `rust-target-trim` prunes stale ones.
- After editing `native/browser-runtime/`, run `make check-browser-runtime` and
  commit the rebuilt `attn-app/src/browser/runtime.js`.

## Platform checks and tests

- Wire tests run on Rune's windowless `Runtime` using bundled fonts only, and
  act as macOS on every host unless `RUNE_PLATFORM=linux` is set (CI runs both).
- Branch platform behavior on `rune::Platform::current()` (and `wire::mac()` in
  tests), not `cfg(target_os = ...)`. Keep `cfg(target_os = ...)` only for real
  OS bindings (windows, WebKit, Quick Capture), and add such files as `'!'`
  entries in `.github/ci-inputs.yml` (`native_macos`), enforced by
  `scripts/native-host-reads` in `make check-native`.

## UI rules, colors and the door

- Build views from `attn_ui::blocks` (`Page`, `Section`, `SettingRow`, `Field`,
  `Notice`, `Table`, `Banner`, …) and `attn_ui::system` controls. Add missing
  controls or blocks to `attn-ui` with a gallery story and a rule test in
  `attn-ui/tests/rules.rs` or `chrome_rules.rs`.
- `attn_ui::system::door` is the only module allowed to call raw Rune spacing,
  font, corner, icon, color or `Kit` constructors (`native/clippy.toml`). Only
  files listed in `native/clippy-allows.tsv` may carry
  `allow(clippy::disallowed_methods)` (`scripts/native-clippy-allows`).
- Clippy cannot catch `Color::BLACK`, `Color::WHITE`, or cloning a `Kit` and
  mutating its public fields; avoid all three in views.
- Read colors in `attn-app` from `crate::colors` or `crate::color_scheme`
  (never directly from `attn_ui::colors`) so `color_overrides` applies. Scheme
  files live in `internal/colors/schemes`.

## Visual and accessibility diffs

- **State sheets** (every `attn-ui` control and block, no window or VM):
  `cargo run -p attn-ui --example state_sheets -- /tmp/sheets [prefix]`.
- **Screenshot tour & a11y diff** (`attn-app/examples/screenshot_tour.rs`,
  offscreen GPU renderer, writes `cost.tsv`, `clips.tsv`, `overlaps.tsv` and
  `a11y/*.tsv`):

  ```sh
  scripts/native-screenshot-tour shoot /tmp/tour/base origin/main
  scripts/native-screenshot-tour shoot /tmp/tour/after
  scripts/native-screenshot-tour diff /tmp/tour/base /tmp/tour/after /tmp/tour/diff
  scripts/native-a11y-diff /tmp/tour/base /tmp/tour/after
  ```

  Use `--only '<glob>'` while iterating, `--scheme <id>` for another scheme,
  and `--window 800x600 --text 1.5 --long` for worst-case overflow checks.
  Shoot both sides of a diff on the same machine so GPU pixels match, and run
  the full tour (without `--only`) before reporting a clean diff.

## Automation

`attn-automation` serves newline-delimited JSON over loopback TCP
(`{id, token, action, payload}` → `{id, ok, result, error}`). Inspect
`native/crates/attn-automation/src/` for the full action and `state` slice
schema.

- **Targets** match by `{"key": "..."}` or `{"role": "...", "name": "..."}`
  (using roles from `tree`). Combining `key` and `name` matches only while the
  element carries that name. Pointer actions wait for the target to appear and
  stop moving, scroll it into view, and return a hit receipt.
- **Waits** (`wait`, `at_rest`, `state` with `includes` on a `terminal:<id>`
  slice or `expect` on any slice) time out after 30 s (`timeout_ms` overrides).
  Name every field you assert in `expect` so the wait is the assertion.
- **Hosts:**
  - Named instances (or `ATTN_AUTOMATION=1`) serve automation and write
    `debug/ui-automation.json` in the app's data dir (macOS Quick Capture
    serves `debug/ui-automation-capture.json` beside it).
  - Headless for Go stack tests: `attn-app --automation-headless --manifest PATH`
    started via `Stack.StartNativeApp`. The test must be named
    `TestTheNativeApp*` and run through `scripts/test-go.sh` (which supplies
    `ATTN_NATIVE_APP_BIN`). CI runs these in `Native / ubuntu-24.04`, not
    Daemon jobs.
  - `shot` captures in-process on macOS and requires Screen Recording permission;
    without it, capture the platform window id from `native_windows`.
