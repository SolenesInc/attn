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
| `attn-ui` | color schemes, fonts and shared components |
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

`native/Cargo.toml` pins one revision of `main`, the trunk of attn's Rune fork,
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
CI's `Native / ubuntu-24.04` job runs the same checks when those paths change,
and on every push to `main`. CI macOS's `Native / macos-15` runs them when
those paths change, nightly, and on a release commit.

## Colors

Every color the native app draws comes from `attn_ui::colors`: a color scheme
holds the interface's `Tokens` and the terminals' `TerminalColors`, once for
dark and once for light. The schemes are the files in `internal/colors/schemes`,
which the daemon checks settings against and the Tauri app reads too; the native
app includes them when it builds, and a scheme missing a color fails to load.
Tokens are named by what they mean, such as `state_waiting` or `find`, never by
the view that uses them. Clippy rejects Rune's color constructors, such as
`rune::color::hex`, everywhere else (`native/clippy.toml`). It cannot see
`Color::BLACK` and `Color::WHITE`, so review catches those.

A view in `attn-app` takes its colors from `crate::colors` or
`crate::color_scheme`, never from a scheme in `attn_ui::colors` itself, so the
`color_overrides` setting reaches it.

### Screenshot tour

`scripts/native-screenshot-tour` draws a fixed set of screens in dark and in
light, one PNG each: the sidebar with an agent in every state, a terminal with
the 16 ANSI colors and a selection, a terminal with a comment mark, a seed id, a
selected command block and find matches, a Markdown file with code and a Mermaid
diagram, a review stop with its key line, the review's key sheet and file view,
the Garden fullscreen and in the dock at its root and on a plot, the Chief's
setup, Settings on Appearance, Agents, Analytics, Delegation, Outposts,
Experimental, Phones, Plugins, Updates, Event bus and Files and locations, Quick Capture with a
message typed and with its mailbox list open, the shortcut editor
and the shortcuts sheet, What's New on its first and its queue step, the
close-desktop dialog, the diagnostic report, the command and agent palettes, the profile switcher, the
ledger empty and with a live agent selected, the asks drawer, the fullscreen
notebook empty, with a folder opened in its tree, with a note open and with its
finder over the note, a popped-out tile window, the classic new-session picker, Launcher 2.0
in its dialog, its Where list and on an empty desktop, a new automation's form, its hour
picker and suggested time zones, each with a row reached by an arrow key, Home
with the Chief and a crew member in the sidebar, Manage crew on each of its
four tabs, the command palette offering
the Chief's setup, the notifications panel, also with no notifications, the PRs drawer, an
agent's actions menu, and the other floating surfaces: a session's snooze and
rename popovers, a tile header's usage popover and its seeds popover for one
seed, and the desktop overview. The `screenshot_tour` example of `attn-app`
sets each one up on the wire-test world and draws it on Rune's offscreen GPU
renderer, so it needs no window and no VM, and two runs of the same source match
pixel for pixel. Each screen starts its own world: a second shot of one world
draws only the glyphs its last frame added.

```sh
scripts/native-screenshot-tour shoot /tmp/tour/base origin/main
scripts/native-screenshot-tour shoot /tmp/tour/after
scripts/native-screenshot-tour diff /tmp/tour/base /tmp/tour/after /tmp/tour/diff
scripts/native-screenshot-tour shoot --scheme catppuccin /tmp/tour/catppuccin
```

`--scheme` shoots the tour with the `color_scheme` setting at that id. `shoot`
with a revision builds it in a temporary worktree with this checkout's
tour, then removes the worktree and its build output. `diff` writes an image of
each changed screen, with the changed pixels in red, and fails when any pixel
differs.

## Automation

Tests, scenarios, `scripts/vm drive` and drive-attn drive the native app over
one automation protocol, `attn-automation`. Its envelope is the Tauri app's:
newline-delimited JSON over loopback TCP, `{id, token, action, payload}` in and
`{id, ok, result, error}` out, so one Node client connects to both shells. The
actions are the native app's own.

A target names an element by its key, `{"key": "sidebar"}`, or by its role and
accessible name, `{"role": "group", "name": "Agent t2"}`. A key with a name,
`{"key": "ledger.row.a.verb", "name": "Unkeep"}`, matches only while that
element carries the name, so `wait` on it waits for the name to change. Pointer actions wait
for the element to appear and stop moving, scroll it into view, and reply with a
receipt that says which element the press landed on. Every reply comes after the
app handled the request's input: its handlers ran, and the commands they sent
are on their way to the daemon.

| Action | Payload | Does |
| --- | --- | --- |
| `ping` | | `{pong, frontendReady}`: ready once connected to the daemon |
| `tree` | | the accessibility tree |
| `find`, `wait` | `target`, and `gone` for `wait` | the element now, or once it shows and stops moving, or goes |
| `press`, `hover` | `target`, `at` | a click or a pointer move at `at`, fractions of the box (center by default) |
| `drag` | `target`, `at`, then `to` (a target) or `by` (points) | press, move, release |
| `scroll` | `target`, `dy` | turns the wheel over the element by `dy` points, as a mouse wheel does; positive `dy` turns it down, as a DOM wheel's `deltaY` does. The element's own wheel handler hears it before a scroll view does |
| `key` | `chord`, such as `mod+shift+k` | the key the keyboard sends for it on this platform |
| `hold` | `mods`, such as `mod` or `mod+shift`, or empty | holds those modifiers down, as a keyboard does, until the next `hold`; empty lets them all go |
| `type` | `text`, `target` | focuses the target first, then types |
| `run` | `name`, an action id such as `session.toggleSidebar`, or `capture.shortcut=<binding>` | dispatches the action. `capture.shortcut=Control+Alt+KeyB` binds Quick Capture's system-wide shortcut as the shortcut editor does, and fails with the editor's reason when the system or the binding refuses it; nothing after `=` turns it off |
| `state` | `of`, `includes`, `expect` | a slice of the store or the window: `connection`, `sessions`, `desktops`, `outposts`, `profiles`, `window`, `settings`, `build`, `capture-shortcut`, the system-wide shortcut held and why the last binding failed, `terminal:<runtime id>`, a terminal's rows as the window last drew them, with the runtime id from a pane in `desktops`, or `terminal-view:<runtime id>`, its cursor, what lies over it such as the selection, its images, how far it is scrolled back, what the copy key copies, the selected command block and its annotations, as the terminal holds them when read, whether or not a frame drew them. With `includes`, waits until one string in the slice, such as one terminal row, contains the text, and waits for a terminal not drawn yet. With `expect`, an object, waits until each field it names has its value in the slice, nested objects field by field |
| `native_windows`, `shot` | `path` for `shot` | the main window's platform id, labelled `main`, then each tile window's, labelled by its key; whether a tile window shows is the platform's to say. `shot` captures the main window |
| `set_window_bounds` | `logicalBounds`: `{x, y, width, height}` in points | moves the main window's top-left corner toward `x, y`, as far as the system allows, and sizes its content to `width` by `height`, then replies `{scaleFactor, minimized, logicalBounds}` with where the window is once the view lays out at that size, as the Tauri app's request does. When the window never reaches the size, such as below its minimum size, it fails naming the bounds asked for and the ones it has. Hosts without a window refuse it |

A request that waits, `state` with `includes` or `expect` among them, gives up after 30 s,
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
  `Native / ubuntu-24.04` runs them.
- The installed app serves it on a named instance, or with `ATTN_AUTOMATION=1`
  (`0` turns it off), and writes its manifest where the Tauri app does:
  `debug/ui-automation.json` in the bundle id's application data directory.
  On macOS the Quick Capture panel serves its own, with the manifest
  `debug/ui-automation-capture.json` beside the app's. Its `run` takes
  `dismiss`, which hides the panel, and its `state` one slice, `capture`: the
  draft's id (`captureId`, which Recent lists it by once sent), text, mailbox
  and files, whether the panel shows, holds the keyboard and has the message
  field focused, its view and its recent rows.
  Requests run on the window's runtime between frames, so they work while the
  window is parked or in the background. A covered window holds its motion
  until it shows, so a request waiting for an element to stop moving waits
  for that too; a change from the daemon wakes a waiting request whether or not
  the window draws it. `shot` waits until the window drew what the app last
  built, then captures it in-process, which macOS refuses to an app without
  Screen Recording permission; a driver that has it captures the id
  `native_windows` reports.

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

Those first measurements no longer hold. On 2026-11-15 a cold debug, test,
`fast` and release build of the whole workspace wrote 6.8 GiB of build
directory and 0.8 GiB of `native/target` on oma. A checkout's build directory
keeps growing with its incremental builds: the largest measured 22.3 GiB on oma
and 14.2 GiB on ai-sandbox. A build removed after 3 unused days costs one cold
rebuild.

`scripts/native-build-clean` removes, from the build root:

- the output of a checkout that no longer exists
- a build not built for 3 days
- while the root is over its budget of 64 GiB, the least recently built output
- while the disk holding the root has less than its free-space floor of 32 GiB
  free, the least recently built output

The floor leaves room for the largest checkout measured to grow back while
another checkout builds cold. It is what keeps a disk shared with other work
from filling: the day ai-sandbox's 600 GB disk filled, the build root held
65 GiB, within its budget.

It never removes a build whose cargo lock a running build holds: it takes the
lock first and keeps it while it removes. When it removes anything, or the root
is over budget or the disk under its floor, it prints the budget, the floor, the
size, each build it removed and why, each build it kept because a running build
holds its lock, and the free space it left. It warns when every build left is in
use and the root is still over budget or the disk still under its floor.
`ATTN_NATIVE_BUILD_BUDGET_MIB` sets the budget and
`ATTN_NATIVE_BUILD_FREE_FLOOR_MIB` the floor. `scripts/land.sh` runs it
after every push, so a removed worktree's output goes at the next land on that
machine.

## Edit-to-test loop

An edit to `attn-app`'s view, rebuilt and with its wire test rerun
(`cargo test -p attn-app --test wire`), takes 3.7 to 7.0 s on the Mac at load
33 to 35 on 10 cores, and 1.4 s on ai-sandbox at load 11 to 12 (2026-11-12). The
budget is 15 s on the Mac.
