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

Without an instance, `make build-app APP=native` builds the release bundle,
`native/target/staged/attn.app`, with the bundle id and URL scheme the Tauri
release has, in the `release` profile and signed with the hardened runtime.
[Making a release](making-a-release.md#the-macos-app) says how a release signs
and notarizes it. CI macOS's `Native release preflight` builds it and checks it
with `scripts/check-macos-release-app.sh`.

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
and on every push to `main`.

That job also runs the `attn-app` wire suite a second time with
`RUNE_KEY_PLATFORM=mac`, which gives Rune macOS's keymap: its chords, labels
and text-editing keys. This second run catches macOS key mistakes on Linux.
Code and tests that depend on which keys a platform uses ask
`KeyPlatform::current()`, and wire tests ask `wire::mac()`, because
`cfg!(target_os = "macos")` cannot change in the second run. Code that compiles
or behaves differently on macOS for any other reason, such as windows, the
browser's WebKit view and Quick Capture, keeps its `cfg(target_os = "macos")`.

CI macOS's `Native / macos-15` runs the checks nightly, on a release commit, and
when a file its `native_macos` group in `.github/ci-inputs.yml` keeps changes.
Those files cover the code that has such a `cfg`, the Rune pin and the VT
library. Keep a file there with a `'!'` entry when it gains a
`cfg(target_os = "macos")`.

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
the 16 ANSI colors and a selection, an agent's header beside an active document
tile, with a seed's state, an open ask and rounded and square boxes in its
terminal, and again with the document's Focus pill hovered, the title bar's
shelf, and again with its width control showing, a terminal with a
comment mark, a seed id, a selected command block and find matches, a Markdown file with code and a Mermaid diagram, a document
alone in its tile with every block the reader draws, a document whose file is
missing, a review stop with its key line, the review's key sheet and file view,
a review beside its agent, and shown alone beside a second document, with its
Walkthrough, search, send sheet and a missing language server, the Garden fullscreen on a plot and on a seed's page, in the dock at
its root, on a plot and on a seed's page, and empty in both, the garden review
expanded with its advice and while its advisor runs, and in the dock, the Chief's setup,
Settings on Appearance, Agents, its end and a missing agent's tooltip, Analytics, Delegation, Outposts,
Experimental and its end, Phones, Plugins, Updates, Event bus, Files and locations, Attention
queue, Background agents, Task runner, GitHub hosts and Models, Quick Capture with a
message typed and with its mailbox list open, the shortcut editor
and the shortcuts sheet, What's New on its first and its Night step, the
close-desktop dialog, the diagnostic report, the command and agent palettes, the command palette matching nothing, the markdown opener, the context window cap prompt, a program asking to pair and its refused Allow in the smallest window, the profile switcher, the
ledger empty and with a live agent selected, the asks drawer, the fullscreen
notebook empty, with a folder opened in its tree, with a note open, with its
finder over the note and with a note that links to a missing one, an editor tile
empty, with that note open and with its finder over it, a popped-out tile window, the classic new-session picker in its
dialog and on an empty desktop, Launcher 2.0 in its dialog, browsing a folder with a completion
ghosted, its Where list and on an empty desktop, and with recents on two outposts, one lost, in
its dialog, on an empty desktop and browsing a folder on an outpost, the automations drawer empty,
listing two automations and with one selected over its runs, a new automation's form on
its schedule, on a pull request trigger and with Advanced open, an automation's edit form,
the form's hour picker and suggested time zones, each with a row reached by an arrow key, Home
with the Chief and a crew member in the sidebar, Home with no agents and GitHub polling off, Home's pull requests by role, on two hosts, rate limited and after a failed refresh, the queue flow with two turns
owed and an open ask at Home and on the asking agent's desktop, the queue sidebar with
the Chief between sessions and two automation runs that need the user, the queue bar
with the sidebar collapsed, the desktop flow's rail, Manage crew on each of its
four tabs, naming a new desktop from its launch settings, Home's merge confirmation, the shared
Codex restart prompt, the outpost updates, update run and shared Codex banners in each of their
states, the dialog for a worktree that could not be made, the command palette offering
the Chief's setup, the notifications panel, also with no notifications, an
agent's actions menu, and the other floating surfaces: a session's snooze and
rename popovers, a tile header's usage popover and its seeds popover for one
seed, and the desktop overview. The `screenshot_tour` example of `attn-app`
sets each one up on the wire-test world and draws it on Rune's offscreen GPU
renderer, so it needs no window and no VM, and two runs of the same source match
pixel for pixel. Each shot uploads every glyph it draws, so a second shot of one
world draws its text too. Beside the pictures, `cost.tsv` lists each screen's
layout nodes and draw instances, so a change reports what a screen costs before
and after, `clips.tsv` lists each screen's count of text that a clip cuts
across its line without an ellipsis or whose glyphs run past its own box, then
each such text with the points it loses or spills, measured on the box its
lines cover rather than its padded box. Text a clip hides whole is not drawn, so
it is not listed. Text in a view that scrolls sideways is left out, and so is text in a field, which scrolls under its caret,
and text under a modal, which counts on the screens where it is in front. `overlaps.tsv` lists each screen's count of texts drawn
over one another, then each pair: two texts whose boxes cross with nothing
filled or blurred between them, so a sheet or popover over the window pairs with
nothing beneath it. Text in a field, such as a completion drawn inside it, pairs
with nothing.

```sh
scripts/native-screenshot-tour shoot /tmp/tour/base origin/main
scripts/native-screenshot-tour shoot /tmp/tour/after
scripts/native-screenshot-tour diff /tmp/tour/base /tmp/tour/after /tmp/tour/diff
scripts/native-screenshot-tour shoot --scheme catppuccin /tmp/tour/catppuccin
scripts/native-screenshot-tour shoot --only 'settings*' /tmp/tour/settings
```

`--scheme` shoots the tour with the `color_scheme` setting at that id. `--only`
shoots just the screens whose names match its pattern, where `*` stands for any
characters; it can be given more than once, and a pattern that matches no screen
fails the run.

`--window 800x600 --text 1.5 --long` shoots the worst case a person meets: the
smallest window, the largest text size, and every name, label, title, branch
and machine the daemon sends made long in Latin and in CJK, the last folder of
every repository and worktree path made long the same way, and every error and
message a long daemon sentence. Every scene runs there, at 100% and at 150%
text. A worst-case run lists a scene that fails in `failed.txt` and still draws
the rest. In every run a scene clicks where a person would: it scrolls a target
the window shows only in part into view and points at it before it presses, so
a control that shows on hover is there. When the target's middle still shows
something else, the scene fails instead of drawing a state it did not reach.

`shoot`
with a revision checks it out, with this checkout's tour, in the checkout's base
worktree, `native/target/screenshot-tour-base`; a revision that pins a Rune without
`ElementInfo::text_ink` fails to build with this tour. The base worktree stays between
runs with its own build directory, so each run compiles only what changed since
the last. `diff` writes an image of each changed screen, with
the changed pixels in red, and fails when any pixel differs or when the after
run's `clips.tsv` or `overlaps.tsv` lists text the base run's does not. Both build in
release; a debug tour draws the same pixels several times slower.

A pair of the full tour, with the base already built once, takes about two
minutes on a quiet 24-core Linux machine and about six on ai-sandbox under load,
where the GPU is software and shooting competes for the CPU. The first pair of a
checkout also builds the release dependencies and the base, about ten minutes
more. `--only` is for looking at the screens you are changing while you work;
shoot every screen for the pair you report, because the diff is what catches the
screen a change was not expected to touch. Shoot both runs of a pair on the same
machine, since two GPUs need not draw the same pixels.
`cargo clean` removes the base worktree with the rest of `native/target`.

Beside each screen's picture the tour writes `a11y/NAME.tsv`: a line per keyed
node of the screen's accessibility tree, with its role, name, value,
description, states, level, the actions it answers, such as scrolling, and the
key of its nearest keyed ancestor and the role and name of its nearest named
one. `scripts/native-a11y-diff BASE AFTER` compares two tours' dumps and prints
what the after tour lost and what changed, field by field, each difference once
with the first screen that shows it, and exits 1 when there is any. A change
that moves controls diffs the whole node this way, since a key, role and name
can match while the node lost its description, its state, its scrolling or its
place in its item or region.

```sh
scripts/native-screenshot-tour shoot --only 'asks*' /tmp/tour/base origin/main
scripts/native-screenshot-tour shoot --only 'asks*' /tmp/tour/after
scripts/native-a11y-diff /tmp/tour/base /tmp/tour/after
```

## Layout, type and corners

Views do not choose a gap, an inset, a font or a corner. They build pages
from `attn_ui::blocks` and controls from `attn_ui::system`, which carry those
choices once for the whole app:

- **Tokens.** Spacing is a scale of nine steps, 0, 2, 4, 6, 8, 12, 16, 24 and
  32. Only `attn-ui` can name a step: no view can make one or pass one. Text is
  set by what it is, with `words(text, Type::…, ink)`: `PageTitle`, `Heading`,
  `Name`, `Body`, `Small`, `Label`, `Tag`, `Mono` and `Control`; text between
  backticks is set in `Mono`. No text is drawn under 12 points, the size of
  `Small`, `Label` and `Tag`. Icons and the marks of `glyph` come in three
  sizes, named by what they sit beside: `IconSize::Small` (14) beside `Small`,
  `Label` and `Tag` text, `Regular` (16) beside `Body` and `Name` text and
  inside a control, and `Large` (18) alone in an icon-only button, which is a
  compact control's height square. A view draws one with `icon(glyph,
  IconSize::…, ink)`. Corners are `Radius::{Tag, Control, Card, Panel, Round}`.
  A control is `Size::Regular` (32 high, its words 12 in from its edge) or
  `Size::Compact` (26 high, 8 in), and it carries its size in its type,
  `Control`, so whatever holds it can line its words up.
- **Controls.** `Button` in four looks (`Primary`, `Plain`, `Quiet`, `Danger`)
  and two sizes, `switch`, `Segmented`, the field boxes `line_box`,
  `number_box` and `area_box`, `stepper` and the number field's `arrows`,
  `expander`, `tag`, `icon_box`, `Choice`, `press_area`, `disclosure` and
  `Trigger`, which opens a menu or a picker. The chrome adds `icon_button`, a
  square as tall as its size holding an icon at an `IconSize` (14, 16 or 18),
  which grows into an accent-tinted pill when it carries a count; `ListRow`,
  one line of a list (32 high) or two (title in `Name` over a line in
  `Small`), its lead in a 16-point column, its controls shown always, faint
  until it is selected, or on hover at its end on its own ground with its
  words fading out before them, and the row on screen marked by the brand bar
  at its start; `GroupHead`, a list group's heading; `dot`, a state's mark,
  filled, hollow or square at 6 or 8; `keycaps` and `keys_hint`, keys drawn as
  keys; `search_field`; and `number_chip` and `count_badge`. Each wraps rune-kit's control
  where one exists, and a view never writes a field of rune-kit's `Kit`:
  `attn-ui` sets them once. Hover and pressed lighten a dark surface and
  darken a light one, each by the step a list row takes (1.20:1 and 1.66:1
  from rest in dark, 1.17:1 and 1.50:1 in light). Focus draws a 2-point warm
  ring outside the edge, on a field as on a button, and no state moves a
  control's edge. Open fills a control with the selected surface. A control
  that opens what it holds below itself, `expander`, `disclosure` and the head
  of an agent group in Settings, carries `opens_in_place`: a chevron that
  points right while closed and down while open. Disabled draws a control's
  rest look at half strength, every part alike, the strength rune-kit draws a
  disabled field at. A warm part first turns neutral, so no disabled control
  reads as the page's one warm action, except the Primary action itself: it
  keeps its hue with its fill at half strength and its words whole on it, so a
  person sees where the action will be before it is available. A disabled
  control draws no layer and no ring and takes no press, focus or Tab stop,
  whoever attached them, but its tooltip still says why it is unavailable.
- **Blocks.** A page is a `Page`: its `Head` (the title, its lead and an
  aside), the line that says it is saving, then its `Block`s in order. A
  `Block` knows what it is, and each of these builds one: `Section`,
  `SettingRow`, `Field`, `Notice`, `Status`, `Choices`, `Disclosure`,
  `Actions`, `Table` and `Prose`. `Block::control` puts a control on a line of
  its own at its own width, and `Block::custom` takes a piece no block fits,
  built from controls inside the rhythms `Inline` and `Stack`. A section that
  is a form says so with `Section::form`, and assistive tools then find a form
  named by its heading, not a group. A `Refusal`, why a control's value was
  refused, is not a block: it goes beside that control. A refusal the person
  caused while staying where they are (Enter in a field, a button's press, a
  dialog's Save), whether a `Refusal`, an error `Notice` or a `Field`'s error,
  is seen at once: the handler or the daemon's reply that sets it calls
  `blocks::show_refusal` with the body that scrolls it, which lands it whole on
  the frame it appears and then shows the control the person acted in, whole.
  The person stayed when they gave no other input between the action and the
  refusal: the action reads `Tx::inputs` and a later reply compares it, since
  keyboard focus does not say where they are. A refusal caused by leaving a
  field for somewhere else, or one that lands after the person did something
  else, does not move the page: the control they are in stays where it shows
  (`Tx::keep_focus_on_screen`), and the refusal stands under what caused it,
  with the field's invalid state, for when they look back. A refusal that
  arrives unattended, from a poll, a load, a timer, a reconnect or the
  daemon's own data, never moves the page the person is reading. The parent
  spaces its blocks by what they are: 32 and a rule between sections, 12 around a
  notice and above a status, 16 otherwise; inside a section or a disclosure, 8
  between setting rows, 16 between fields, 12 otherwise. A page runs to 800
  points between its sides, room for a setting row with a full reading line
  beside its widest control, and every block ends on that one edge. The
  Settings dialog grows to its navigation beside the widest page, with its
  sides and scroll bar, and no wider. Reading text wraps at 480 points inside
  the page and a field at 504. Words beside a control, in a setting row or
  a page's head, keep 288 before the control moves under them, and then wrap to
  whatever line is left; a notice's action moves under its words at the same
  288. A choice's name keeps 80, the longest word of a scheme's name, before it
  moves under its swatches. A disclosure's tags and a line of controls wrap
  onto further lines before they cut a label short. Nothing a block draws runs
  past its sides, down to the smallest window at 150% text, where the Settings
  page keeps 163 points.
- **Tables.** A `Table` lays its rows on `TableColumns`: the name, which takes
  what the others leave, and fixed columns at widths the caller measured, each
  a `Column` a row places its controls in. A mark column stands first when a
  row has a mark, and a column for the row's own button stands last when a row
  has one, so no control a row gives goes undrawn. Every line of a table, its
  head, rows, sub-rows and details, lays the mark and the name the same way,
  so they wrap at one width and start on one edge. The name keeps 200 before
  the fixed columns wrap under it, and a wrapped line of columns starts on the
  names' edge, leaving the mark column empty beside it. The row's button ends
  the last column's line at the row's trailing edge, or goes under that line at
  the same edge when the line cannot hold it, and below the mark and those 200
  the name takes a line of its own under the mark. A row's name opens it, and
  an open row shows its details, its sub-rows on a rail and its actions on the
  name column. A closed sub-row shows its note, what the person wrote, on up to
  two lines. Opening a row springs the rows below it into place, as Rune springs
  any layout change a press makes; the Delegation page's rows moved this way
  before they were a `Table`. A table that is switched off fades its head and
  what its rows say, and keeps every control's colour, since each control
  still works; its empty state and its footer keep their colour too. An empty
  table's actions wrap one under the other when they do not fit side by side.
- **The banner.** `attn_ui::blocks::Banner` is the bar under the title bar
  that outpost updates, an update run and shared Codex draw. A view gives its
  tone (`Warn`, `Calm`, `Good` or `Bad`), its sentence, its notes, its actions,
  a countdown and dismiss, and the block draws the rest: the tone's glyph, the
  sentence in `Name`, the actions right beside it, the notes after them or,
  when they do not fit there, together on lines of their own under the
  sentence, and dismiss alone at the far edge. The glyph and dismiss sit on
  the sentence's first line, and so do the actions unless they would leave
  the sentence narrower than 250 points or longer than four lines; then they
  take a line of their own under it. A view groups its notes into items with
  `Banner::item`, such as one outpost's step or one blocker with its reason
  and Relaunch. The bar never grows past that: a sentence longer than four
  lines ends in an ellipsis, the notes take at most three lines, holding the
  items that fit whole, the first at least in part, and then "and N more."
  for the rest, and Details, last among the actions, opens the banner whole in
  a dialog, its sentence, every note and its actions, built by
  `Banner::build_details` from the same banner. Details also stands whenever a
  note is wider than its line or holds a link or a button, whose name the bar
  may elide. The banner owns its controls' presses, so pressing one in the
  dialog also closes it. The dialog belongs to the message it opened for, and
  closes once the banner says another. Only the dialog scrolls. A link
  or button in the notes longer than its line ends in an ellipsis and keeps its
  whole name for a screen reader. Its fill is 18% of the tone over
  the app background. A failure fills the bar with the error colour, darkened until the
  app background reads on it at 4.5:1, and its controls' focus ring takes that
  colour.
- **The door.** `attn_ui::system::door` is the one module that calls Rune's raw
  spacing, font, corner and icon methods (`gap`, `padding`, `px`…, `radius`,
  `font`, `TextStyle::new`, `rune::icon` and the rest), the rune-kit setters
  that take a spacing, a font, a corner or an icon size (`TextInput::text_style`,
  `MarkdownStyle::body_font`, `Menu::gutter`, …) and rune-kit's `Kit`
  constructors. `native/clippy.toml` rejects them everywhere else in the
  workspace, as it does Rune's colour constructors, so a view gets its kit from
  `attn_ui::kit` and draws through `attn_ui`. The guard stops raw calls and
  making a kit, not a view that clones that kit and writes one of its public
  fields: a derived `Clone` resolves to `Clone::clone`, which clippy can only
  ban for every type, so review catches it until rune-kit's `Kit` keeps its
  fields private. Only the files
  `native/clippy-allows.tsv` names, each with its reason, may carry
  `allow(clippy::disallowed_methods)`: the door, the colours, the terminal's
  cell font, What's New's illustrations and the tests that state a type role
  apart from the library. `scripts/native-clippy-allows` in
  `make check-native` fails when any other file carries it. Code outside
  `attn-ui` never names a step of `rune::tok::space`, `type_` or `radius`; an
  overlay's offset is a named constant in its feature.
- **The gallery.** `attn_ui::gallery` has a playground story for every
  control and block, with a variant for each state it can be in. `cargo run -p
  attn-ui --example state_sheets -- OUT` draws each state sheet, with no window
  and no VM, in dark and in light. A control's sheet puts every state side by
  side, with a real pointer for hover and pressed and the Tab key for focus. A
  block's sheet draws each state on a row of its own, and each state that takes
  the keyboard again with the keyboard on its first control, or on the part the
  state is about. `-- OUT NAME` draws only the sheets whose name starts with
  `NAME`, such as `banner`, or `block` for every page block.
  `attn-ui/tests/rules.rs` reads the stories' rectangles and what the frame
  draws, and holds the rules the controls keep: one text inset in every field
  box and a long value ending at it, one width for number fields that holds a
  seven-digit value, a stepper's value at one width, a segmented control
  keeping to its own width beside words and inside its column, a field's edge
  where every other control's is, a focus ring never cut or dimmed, hover and
  pressed at their measured contrast, every part of a disabled control at
  half strength, a warm one neutral but a disabled Primary in its own hue with
  its words at 3:1 or more, and a banner's first action 8 after its
  sentence, its glyph and dismiss on its first line, its actions beside its
  sentence or under it, its sentence readable at the smallest window with
  the largest text, a long link elided inside the bar with its ring whole,
  Details offered for a sentence past four lines, its notes beside its actions
  or on a line of their own, its dismiss 12 from its edge, one height in
  every tone, and its fill, words and ring at their measured contrast, a tag
  18 high with its words centred in it, and an icon-only button's `Large` icon
  4 from each of its edges. It holds the page blocks' rules too: one right
  edge on a page,
  blocks spaced by what they are, reading text at its measure, a control moved
  under its words starting on their edge, a page title wrapping short of its
  aside and keeping its line, a choice's name whole, no part of a block past
  its sides, and a control's box and ring taking in everything it draws, at
  any width the stories draw, the smallest included. A table's rules: a row's
  parts on one band, its button ending its control's line or the block under
  it at the trailing edge, an alternative's remove button the same, a closed
  alternative's condition on two lines at most, a row's controls on one left
  edge, a sub-row 12 from its rail, the head, a row's name, its details and a
  wrapped line of its controls on one edge at every width from 160 to 800, an
  open row's details ending with the name column, a narrow table's head naming
  only the columns on its line, the footer on the name column, a tag's edge a
  hover step from its row at rest, hovered, pressed and open, and a
  switched-off table's words at half strength while its controls, its rings
  and what its empty state offers draw as when it is on.
  `attn-ui/tests/chrome_rules.rs` holds the chrome's: one height for every list
  row of one line and one start for every title, nothing past a row's sides, a
  row's hover controls whole on the first frame on the row's own ground with
  its words ending before them, a two-line row's lead and controls on its
  title's line, an icon-only button square with its icon centred and its count
  beside it, and every chrome control's hover, pressed and ring as the other
  controls'.
A control or a block a view needs that `attn-ui` lacks is added there with its
story and, when it keeps a rule, its test.

## Automation

Tests, scenarios, `scripts/vm drive` and drive-attn drive the native app over
one automation protocol, `attn-automation`. Its envelope is the Tauri app's:
newline-delimited JSON over loopback TCP, `{id, token, action, payload}` in and
`{id, ok, result, error}` out, so one Node client connects to both shells. The
actions are the native app's own.

A target names an element by its key, `{"key": "sidebar"}`, or by its role and
accessible name, `{"role": "group", "name": "Agent t2"}`. The role is the one
`tree` prints, such as `list-box-option` for an element the view calls
`option`, and the name is its label, its text or the name `tree` prints. When
several elements match, the first one that shows wins. A key
with a name, `{"key": "ledger.row.a.verb", "name": "Unkeep"}`, matches only
while that element carries the name, so `wait` on it waits for the name to
change. Pointer actions wait for the element to appear and stop moving, scroll
it into view, keyed or not, and reply with a
receipt that says which element the press landed on. Every reply comes after the
app handled the request's input: its handlers ran, and the commands they sent
are on their way to the daemon.

| Action | Payload | Does |
| --- | --- | --- |
| `ping` | | `{pong, frontendReady}`: ready once connected to the daemon |
| `tree` | | the accessibility tree |
| `at_rest` | | once the window has no motion left to draw and nothing still leaving, `{}`; while it waits, says which elements are still leaving |
| `find`, `wait` | `target`, and `gone` for `wait` | the element now, or once it shows and stops moving, or goes |
| `press`, `hover` | `target`, `at` | a click or a pointer move at `at`, fractions of the box (center by default) |
| `drag` | `target`, `at`, then `to` (a target) or `by` (points) | press, move, release |
| `scroll` | `target`, `dy` | turns the wheel over the element by `dy` points, as a mouse wheel does; positive `dy` turns it down, as a DOM wheel's `deltaY` does. The element's own wheel handler hears it before a scroll view does |
| `key` | `chord`, such as `mod+shift+k` | the key the keyboard sends for it on this platform |
| `hold` | `mods`, such as `mod` or `mod+shift`, or empty | holds those modifiers down, as a keyboard does, until the next `hold`; empty lets them all go |
| `type` | `text`, `target` | focuses the target first, then types |
| `run` | `name`, an action id such as `session.toggleSidebar`, or `capture.shortcut=<binding>` | dispatches the action. `capture.shortcut=Control+Alt+KeyB` binds Quick Capture's system-wide shortcut as the shortcut editor does, and fails with the editor's reason when the system or the binding refuses it; nothing after `=` turns it off |
| `state` | `of`, `includes`, `expect` | a slice of the store or the window: `connection`, `sessions`, `desktops`, `outposts`, `profiles`, `window`, `settings`, `build`, `capture-shortcut`, the system-wide shortcut held and why the last binding failed, `terminal:<runtime id>`, a terminal's rows as the window last drew them, with the runtime id from a pane in `desktops`, or `terminal-view:<runtime id>`, its cursor, the cursor's cell as `caret`, [x, y, width, height] in points from the terminal's top-left, what lies over it such as the selection, its images, how far it is scrolled back, what the copy key copies, the selected command block and its annotations, as the terminal holds them when read, whether or not a frame drew them, or `input-trace`, the window's input trace oldest first: each key, paste or composition by `traceId`, with `instantMs` on one monotonic clock for a key's arrival, on its `keydown`, and its `websocket_send`, and for the first output the link heard after the key left, `output_received` when the link heard it, `output_taken` when its terminal took it, and `echo_drawn` when the first frame that shows it was encoded, once any synchronized update holding the terminal's screen has ended. With `includes`, waits until one row of a terminal's text contains the text, and waits for a terminal not drawn yet; `includes` on any other slice fails at once, because it would match the text in any field, such as an id in `current_desktop` while the step asserts `on_screen`. With `expect`, an object, waits until each field it names has its value in the slice, nested objects field by field and arrays item by item, the same length; `{"$some": …}` agrees with a list when one item agrees with it, and `{"$includes": "…"}` with a text that contains it. Name in `expect` every field the step then asserts, so the wait is the assertion |
| `native_windows`, `shot` | `path` for `shot` | the main window's platform id, labelled `main`, then each tile window's, labelled by its key; whether a tile window shows is the platform's to say. `shot` captures the main window |
| `set_window_bounds` | `logicalBounds`: `{x, y, width, height}` in points | moves the main window's top-left corner toward `x, y`, as far as the system allows, and sizes its content to `width` by `height`, then replies `{scaleFactor, minimized, logicalBounds}` with where the window is once the view lays out at that size, as the Tauri app's request does. When the window never reaches the size, such as below its minimum size or taller than the screen's visible area, which macOS fits a window's height into, it fails naming the bounds asked for and the ones it has. Hosts without a window refuse it |
| `get_window_bounds` | none | `{scaleFactor, minimized, logicalBounds}` for where the main window is now, as the Tauri app's request does. A scenario that needs only width widens with `driver.widenWindow(width)`, which keeps the height the screen gave the window. Hosts without a window refuse it |

A request that waits, `state` with `includes` or `expect` among them, gives up after 30 s,
or its payload's `timeout_ms`, and says what it waited for and, for `expect`, what the slice
last read.

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
