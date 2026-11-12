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
```

`attn-term` links the libghostty-vt archive. `make test-native` builds it when
it is missing, then runs the whole suite from `native/`.

To run the app, install it on a named instance with
`make install INSTANCE=<name> APP=native`, as [Instances](instances.md) says.
The build bakes the instance's name, port and bundle id into `attn-app`, and the
app starts the `attnd` bundled beside it. A build without them, such as a bare
`cargo run`, refuses to start.

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

## Automation

Tests, scenarios, `scripts/vm drive` and drive-attn drive the native app over
one automation protocol, `attn-automation`. Its envelope is the Tauri app's:
newline-delimited JSON over loopback TCP, `{id, token, action, payload}` in and
`{id, ok, result, error}` out, so one Node client connects to both shells. The
actions are the native app's own.

A target names an element by its key, `{"key": "sidebar"}`, or by its role and
accessible name, `{"role": "group", "name": "Agent t2"}`. Pointer actions wait
for the element to appear and stop moving, scroll it into view, and reply with a
receipt that says which element the press landed on.

| Action | Payload | Does |
| --- | --- | --- |
| `ping` | | `{pong, frontendReady}`: ready once connected to the daemon |
| `tree` | | the accessibility tree |
| `find`, `wait` | `target`, and `gone` for `wait` | the element now, or once it shows and stops moving, or goes |
| `press`, `hover` | `target`, `at` | a click or a pointer move at `at`, fractions of the box (center by default) |
| `drag` | `target`, `at`, then `to` (a target) or `by` (points) | press, move, release |
| `scroll` | `target`, `dy` | turns the wheel over the element by `dy` points, as a mouse wheel does; positive `dy` turns it down, as a DOM wheel's `deltaY` does. The element's own wheel handler hears it before a scroll view does |
| `key` | `chord`, such as `mod+shift+k` | the key the keyboard sends for it on this platform |
| `type` | `text`, `target` | focuses the target first, then types |
| `run` | `name`, an action id such as `session.toggleSidebar` | dispatches the action |
| `state` | `of`, `includes` | a slice of the store or the window: `connection`, `sessions`, `desktops`, `profiles`, `window`, `build`, or `terminal:<runtime id>`, a terminal's rows as the window last drew them, with the runtime id from a pane in `desktops`. With `includes`, waits until one string in the slice, such as one terminal row, contains the text, and waits for a terminal not drawn yet |
| `native_windows`, `shot` | `path` for `shot` | the window's platform id; a screenshot |

A request that waits, `state` with `includes` among them, gives up after 30 s,
or its payload's `timeout_ms`, and says what it waited for.

Hosts run the protocol on the app's `Runtime`:

- In a wire test, `World::automate` answers a request in the test, settling
  between attempts.
- `attn-app --automation-headless --manifest PATH` runs the app with no window
  and no GPU against the daemon that `ATTN_WS_PORT`, `ATTN_DATA_DIR` and
  `ATTN_INSTANCE` name, writes the manifest to `PATH`, and prints
  `ready port=<port>`. Go stack tests start it with `Stack.StartNativeApp`,
  which builds the workspace's debug `attn-app` unless `ATTN_NATIVE_APP_BIN`
  names one. CI's Daemon jobs have no native toolchain and skip these tests;
  `Native (ubuntu-24.04)` runs them.
- The installed app serves it on a named instance, or with `ATTN_AUTOMATION=1`
  (`0` turns it off), and writes its manifest where the Tauri app does:
  `debug/ui-automation.json` in the bundle id's application data directory.
  Requests run on the window's runtime between frames, so they work while the
  window is parked or in the background. A covered window holds its motion
  until it shows, so a request waiting for an element to stop moving waits
  for that too. `shot` captures the window
  in-process, which macOS refuses to an app without Screen Recording
  permission; a driver that has it captures the id `native_windows` reports.

### Scenarios and the VM

A catalog entry in `app/scripts/real-app-harness/scenarioCatalog.mjs` with
`app: 'native'` runs against the native app; entries without it need the Tauri
app. The install writes the shell into the tree's `build-identity.json`, and the
serial matrix leaves out the entries for the other one, or refuses when they
were named. The acceptance soak builds and installs the shell each named
scenario needs. Install a native tree with
`make install INSTANCE=<name> APP=native`.

`createWindowDriver` returns a `NativeAppDriver` for a native tree. Its input
goes through the automation server, so on both platforms it takes no focus and
leaves the pointer alone. It finds, parks, places and captures the window with
the platform driver, from the harness process. `launchFreshNativeAppAndConnect`
in `common.mjs` launches the app and connects. `scenario-native-sidebar-session.mjs`
is the smallest example.

`APP=native scripts/vm up` builds and runs the native app in the macOS VM.
`drive` then takes the native app's actions, and `shot` uses its `shot` action.

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
