//! The screenshot tour: a fixed set of the app's screens, each drawn offscreen on the GPU in
//! dark and in light, one PNG each. `scripts/native-screenshot-tour` runs it.
//!
//!     screenshot_tour shoot OUT [SCHEME]   SCHEME sets the color_scheme setting
//!     screenshot_tour compare BASE AFTER DIFF

use std::collections::BTreeMap;
use std::path::PathBuf;

use attn_app::desktop::FocusStyle;
use attn_app::screen::Screens;
use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    ActorView, Ask, AskChoice, AskPart, AskQuestion, AutomationDefinitionResultMessage,
    AutomationDefinitionSummary, AutomationDefinitionsResultMessage, AutomationProvenance,
    AutomationRunSummary, AutomationRunsResultMessage, AutomationSchedulePreviewResultMessage,
    BrowseDirectoryResultMessage, BusConsumerStatus, BusStatusResultMessage,
    CreateWorktreeResultMessage, CrewCharterDocument, CrewCharterGetResultMessage,
    CrewHandoffsGetResultMessage, CrewMember, DelegationPreferences,
    DelegationPreferencesResultMessage, DelegationRole, Desktop, DesktopPane,
    DesktopTileContentMessage, DirectoryEntry, FileActivity, FsIndexResultMessage, GardenReview,
    GardenReviewEvidence, GardenReviewItem, GardenReviewRecipe, GardenReviewRun,
    GetCommandUsageResultMessage, GetRepoInfoResultMessage, Harness, HarnessModel,
    HarnessModelsResultMessage, InitialStateMessage, InspectPathResultMessage,
    InstallCodexUpdateResultMessage, LaunchDesktopResultMessage, LaunchDesktopSetting,
    NotificationListResultMessage, OutpostInfo, OutpostUpdate, PR, PartyView, PathInspection,
    PhoneLink, PhonesUpdatedMessage, Profile, RateLimitedMessage, RecentFilesResultMessage,
    RecentLocation, RecentLocationsResultMessage, RecordCommandUsageResultMessage,
    RefreshPRsResultMessage, RepoInfo, ReviewBundle, ReviewFile, ReviewNotShown, ReviewNote,
    ReviewRow, ReviewStop, Seed, SeedDocument, SeedDocumentGetResultMessage, SeedEdge, SeedNote,
    SeedPlotProgress, SeedReviewResultMessage, Session, SessionAnnotationsGetResultMessage,
    SessionAnnotationsSaveResultMessage, SessionLedgerEntry, SessionListResult,
    SessionListResultMessage, SessionMessage, SessionMessagesGetResultMessage, SessionUsage,
    SessionUsageModel, SharedCodexBlocker, SharedCodexState, UpdateOutpostProgress, UpdateRun,
    Worktree, WorktreeListResult, WorktreeListResultEvent,
};
use attn_term::{FONT_SIZE, Metrics};
use attn_testkit::tree::{pane, row};
use attn_testkit::{Daemon, Request, Target, World, key_event, message};
use rune::render::{Gpu, OFFSCREEN_FORMAT, Offscreen, Renderer, write_png};
use rune::text::pixel_compare::Image;
use rune::{Compositor, Frame, KeyPlatform, Runtime};
use rune::{Key, KeyEvent, Mods};
use serde_json::{Value, json};

const PROFILE: &str = "work";
const DESKTOP: &str = "work/desktop_1";
const SIZE: (f32, f32) = (1440.0, 900.0);

/// A run's window and data: the tour's own, or the worst case a person meets.
#[derive(Clone, Copy)]
struct Mode {
    window: (f32, f32),
    text: f32,
    long: bool,
}

const TOUR: Mode = Mode {
    window: SIZE,
    text: 1.0,
    long: false,
};

thread_local! {
    static MODE: std::cell::Cell<Mode> = const { std::cell::Cell::new(TOUR) };
    static FAILED: std::cell::RefCell<Vec<String>> = std::cell::RefCell::default();
}

fn mode() -> Mode {
    MODE.get()
}

fn window() -> (f32, f32) {
    mode().window
}

/// The scripted daemon every scene starts from, sending the run's text size and, in a long
/// run, each name and error at the longest a person meets.
fn scripted() -> Daemon {
    let run = mode();
    if run.text == 1.0 && !run.long {
        return Daemon::scripted();
    }
    Daemon::scripted().rewriting(move |message| {
        if run.long {
            lengthen(message);
        }
        if run.text != 1.0 && message["event"] == "initial_state" {
            message["settings"]["uiScale"] = json!(run.text.to_string());
        }
    })
}

/// What a long run adds to a name: a worktree's basename, in Latin and in CJK.
const LONG_NAME: &str =
    "build-and-release-platform-本番の権限を移行する作業ツリー-tokyo-region-primary";

/// What a long run adds to an error: a daemon's failure as it reads.
const LONG_ERROR: &str = "the app server exited with status 1 before it answered its first \
     request, after loading a configuration it could not read from \
     ~/.config/attn/outposts/build-and-release-platform-tokyo-region-primary.toml";

/// Each name, label and title in `value` made long, and each error made a long sentence,
/// the same way every time so the app and the scene agree on what it sent.
fn lengthen(value: &mut Value) {
    match value {
        Value::Object(fields) => {
            for (key, field) in fields.iter_mut() {
                match (key.as_str(), &mut *field) {
                    ("label" | "name" | "title" | "display_name", Value::String(said))
                        if !said.is_empty() && !said.ends_with(LONG_NAME) =>
                    {
                        *said = format!("{said}-{LONG_NAME}");
                    }
                    ("error" | "error_message", Value::String(said))
                        if !said.is_empty() && !said.ends_with(LONG_ERROR) =>
                    {
                        *said = format!("{said}: {LONG_ERROR}");
                    }
                    _ => lengthen(field),
                }
            }
        }
        Value::Array(items) => items.iter_mut().for_each(lengthen),
        _ => {}
    }
}

/// Runs one scene. A tour run stops at a scene that fails; a worst-case run records it and
/// goes on, so one scene that cannot run at that size hides no other.
fn scene(name: &str, run: impl FnOnce()) {
    let run = std::panic::AssertUnwindSafe(run);
    if mode().window == SIZE && mode().text == 1.0 && !mode().long {
        run.0();
        return;
    }
    if std::panic::catch_unwind(run).is_err() {
        FAILED.with_borrow_mut(|failed| failed.push(name.to_owned()));
        SCENE.with_borrow_mut(String::clear);
    }
}

const STATES: [&str; 8] = [
    "working",
    "waiting_input",
    "pending_approval",
    "scheduled",
    "idle",
    "recoverable",
    "unknown",
    "working",
];

const NOTES: &str = r#"# Release notes

A short document that exercises every block the reader draws.

## Highlights

- **Bold**, *italic*, `inline code` and a [link](https://example.com).
- A nested list:
  - second level
  - with ~~strikethrough~~
- [x] A finished task
- [ ] An open task

> A blockquote that runs long enough to wrap onto a second line when the tile
> is narrow, so the bar and the indent show.

### Code

```rust
fn main() {
    let total: u32 = (1..=10).sum();
    println!("total = {total}");
}
```

### Table

| Surface | State | Owner |
| --- | --- | --- |
| Sidebar | done | reviewer |
| Garden | in progress | builder |

1. First numbered item
2. Second numbered item

---

Final paragraph with a seed id s-abc123 and a path `docs/plan.md`.
"#;

const DOCUMENT: &str = "# The plan

Colors move into **one module**, and every view reads them from there.

> A quote, with `inline code` and a [link](https://example.com).

```rust
pub fn scheme_for(setting: Option<&str>) -> &'static ColorScheme {
    let id = setting.unwrap_or(\"attn\");
    SCHEMES.iter().find(|s| s.id == id).copied().unwrap_or(&ATTN) // fallback
}
```

| Token | Means |
| --- | --- |
| accent | what you act on |
| error | what failed |

```mermaid
flowchart LR
  T[theme] --> A[appearance]
  S[color scheme] --> C[colors]
  A --> C
  C --> U[views]
  C --> M[terminal]
```
";

const REVIEW: &str = "---
attn: review
frame: brief
repository: /repo
base: 4139a109c
head: 12e1c7870
---

# Name the sum

The total was computed inline. It now comes from `sum_to`, so the limit has a name.

> [!IMPORTANT]
> One line changes behavior: `main` calls `sum_to(10)`.

For example, `sum_to(3)` returns 6.

## main asks the helper

### totals.rs:1-5

- L3!: The inline range is gone.

## The helper owns the range

### totals.rs:7-9

- L8: Same range, with the limit as a parameter.

## Not shown

- README.md: wording only
";

fn settings(dark: bool) -> Option<BTreeMap<String, String>> {
    let theme = if dark { "dark" } else { "light" };
    let mut settings = BTreeMap::from([("theme".to_owned(), theme.to_owned())]);
    if let Some(scheme) = SCHEME.with_borrow(Clone::clone) {
        settings.insert("color_scheme".to_owned(), scheme);
    }
    Some(settings)
}

fn profile() -> Profile {
    Profile {
        id: PROFILE.into(),
        name: "Work".into(),
        current_desktop_id: DESKTOP.into(),
        ..Profile::default()
    }
}

fn session(id: &str, state: &str) -> Session {
    Session {
        id: id.into(),
        label: format!("{state} agent"),
        agent: "claude".into(),
        state: state.into(),
        directory: "/repo".into(),
        profile_id: PROFILE.into(),
        ..Session::default()
    }
}

fn agent_pane(desktop: &str, pane: &str, session: &str) -> DesktopPane {
    DesktopPane {
        desktop_id: desktop.into(),
        pane_id: pane.into(),
        runtime_id: format!("r-{pane}"),
        session_id: session.into(),
        title: format!("Agent {pane}"),
        kind: "agent".into(),
        ..DesktopPane::default()
    }
}

fn desktop(id: &str, order: &str, tree: Value, panes: Vec<DesktopPane>) -> Desktop {
    Desktop {
        id: id.into(),
        profile_id: PROFILE.into(),
        name: format!("Desktop {order}"),
        order_key: order.into(),
        tree_json: tree.to_string(),
        active_pane_id: panes.first().map(|p| p.pane_id.clone()).unwrap_or_default(),
        panes,
        revision: 1,
        ..Desktop::default()
    }
}

fn document_tile(id: &str, path: &str) -> Value {
    json!({
        "type": "tile",
        "tile_id": id,
        "tile_kind": "markdown",
        "tile_params": json!({ "path": path }).to_string(),
    })
}

/// Desktop 1 holds terminal `a` beside `tile`; desktops 2 and up each hold one agent in another
/// state, so the sidebar shows every state.
fn initial(dark: bool, tile: Value, seeds: Vec<Seed>) -> InitialStateMessage {
    let sessions: Vec<Session> = STATES
        .iter()
        .enumerate()
        .map(|(n, state)| session(&format!("s{n}"), state))
        .collect();
    let mut sessions = sessions;
    sessions[1].priority = Some(true);
    sessions[2].dispatcher_session_id = Some("s1".into());
    sessions[2].delegated_from_chief = Some(true);
    sessions[2].delegation_role = Some(attn_protocol::SessionDelegationRole {
        name: "Builder".into(),
        icon: Some("code".into()),
        ..Default::default()
    });
    sessions[3].host = Some("studio".into());
    let mut desktops = vec![desktop(
        DESKTOP,
        "1",
        row(vec![pane("a"), tile]),
        vec![agent_pane(DESKTOP, "a", "s0")],
    )];
    for n in 1..STATES.len() {
        let id = format!("work/desktop_{}", n + 1);
        let pane_id = format!("p{n}");
        desktops.push(desktop(
            &id,
            &(n + 1).to_string(),
            pane(&pane_id),
            vec![agent_pane(&id, &pane_id, &format!("s{n}"))],
        ));
    }
    InitialStateMessage {
        selected_profile_id: Some(PROFILE.into()),
        profiles: Some(vec![profile()]),
        sessions: Some(sessions),
        desktops: Some(desktops),
        settings: settings(dark),
        seeds_total: Some(seeds.len() as i64),
        seeds: Some(seeds),
        daemon_instance_id: Some("daemon-1".into()),
        ..InitialStateMessage::new()
    }
}

fn start(daemon: Daemon) -> World {
    let mut world = World::start_sized(daemon, window());
    world.open_desktop();
    world
}

fn deliver(world: &mut World, path: &str, content: &str, review: Option<ReviewBundle>) {
    deliver_to(world, "m", path, content, review);
}

fn deliver_to(
    world: &mut World,
    tile: &str,
    path: &str,
    content: &str,
    review: Option<ReviewBundle>,
) {
    world.daemon.emit(DesktopTileContentMessage {
        desktop_id: DESKTOP.into(),
        tile_id: tile.into(),
        tile_kind: "markdown".into(),
        path: path.into(),
        content: content.into(),
        review,
        ..DesktopTileContentMessage::new()
    });
    world.settle();
}

/// Every SGR color on terminal `a`: the 16 ANSI colors as backgrounds and foregrounds, bold,
/// and the default colors.
fn print_colors(world: &mut World) {
    let mut text = String::from("\x1b[H\x1b[2J$ attn colors\r\n");
    for base in [40, 100] {
        for n in 0..8 {
            text.push_str(&format!("\x1b[{}m {:>3} \x1b[0m", base + n, base + n));
        }
        text.push_str("\r\n");
    }
    for base in [30, 90] {
        for n in 0..8 {
            text.push_str(&format!("\x1b[{}m {:>3} \x1b[0m", base + n, base + n));
        }
        text.push_str("\r\n");
    }
    text.push_str("\x1b[1mbold\x1b[0m \x1b[2mdim\x1b[0m \x1b[4munderline\x1b[0m \x1b[7minverse\x1b[0m plain\r\n");
    text.push_str("a line to select across the terminal\r\n$ ");
    write(world, &text);
}

/// `text` as terminal `a`'s first output.
fn write(world: &mut World, text: &str) {
    let id = b"r-a";
    let mut frame = vec![0x01, id.len() as u8];
    frame.extend_from_slice(id);
    frame.extend_from_slice(&1u32.to_be_bytes());
    frame.extend_from_slice(text.as_bytes());
    world.daemon.emit_frame(frame);
    world.settle();
}

fn cell(world: &World, col: u16, row: u16) -> (f32, f32) {
    let (rect, _) = world
        .runtime()
        .presented("terminal.r-a")
        .expect("terminal a is drawn");
    let metrics = Metrics::of(FONT_SIZE);
    (
        rect[0] + (f32::from(col) + 0.5) * metrics.cell_w,
        rect[1] + (f32::from(row) + 0.5) * metrics.cell_h,
    )
}

fn select(world: &mut World) {
    let (from, to) = (cell(world, 2, 6), cell(world, 20, 6));
    world.gesture(|app| {
        app.runtime.pointer_down(from.0, from.1);
        app.runtime.pointer_move(to.0, to.1);
        app.runtime.pointer_up(to.0, to.1);
        app.runtime.pointer_leave();
    });
}

fn press(world: &mut World, action: Action) {
    let chord = match Keymap::from_setting(None, KeyPlatform::current()).binding(&action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    };
    world.gesture(|app| app.key(chord));
}

/// Dispatches `action` as a handler would, for an action with no chord of its own.
fn run(world: &mut World, action: Action) {
    world.gesture(|app| {
        app.update(|state, tx| {
            attn_app::dispatch(state, action, tx);
        })
    });
}

fn seed(id: &str, title: &str, status: &str, parent: Option<&str>) -> Seed {
    Seed {
        profile_id: PROFILE.into(),
        id: id.into(),
        title: title.into(),
        status: status.into(),
        planter: ActorView {
            name: "the user".into(),
            r#ref: "user".into(),
        },
        created_at: "2026-11-13T09:00:00Z".into(),
        updated_at: "2026-11-13T09:00:00Z".into(),
        rev: 1,
        edges: parent
            .map(|p| SeedEdge {
                kind: "part-of".into(),
                to: p.into(),
            })
            .into_iter()
            .collect(),
        ..Seed::default()
    }
}

fn seeds() -> Vec<Seed> {
    vec![
        Seed {
            plot_progress: Some(SeedPlotProgress {
                total: 3,
                ready: 1,
                ..SeedPlotProgress::default()
            }),
            body: "Three steps, in order. Each lands on its own.\n\n\
                   - D2. The waiting mark is orange in both themes.\n\
                   - F1. Rows read their state from a dot.\n"
                .into(),
            ..seed("s-plot", "Catppuccin for attn", "growing", None)
        },
        Seed {
            body: "Every view reads its colors from one module.".into(),
            ..seed("s-one", "Colors in one place", "growing", Some("s-plot"))
        },
        seed(
            "s-two",
            "Catppuccin color schemes",
            "planted",
            Some("s-plot"),
        ),
        seed("s-three", "A harvested seed", "harvested", Some("s-plot")),
        seed("s-loose", "A loose seed", "planted", None),
        seed("s-wither", "A withered seed", "withered", None),
    ]
}

fn review_line(kind: &str, old: Option<i64>, now: Option<i64>, text: &str) -> ReviewRow {
    ReviewRow {
        kind: kind.into(),
        old,
        now,
        text: text.into(),
        ..ReviewRow::default()
    }
}

fn review_note(line: i64, at: i64, look: bool, text: &str) -> ReviewNote {
    ReviewNote {
        line,
        from: at,
        to: at,
        now_from: at,
        now_to: at,
        look,
        text: text.into(),
    }
}

fn review() -> ReviewBundle {
    let stop = |heading_line: i64, (now_from, now_to): (i64, i64), rows, notes| ReviewStop {
        heading_line,
        path: "totals.rs".into(),
        from: now_from,
        to: now_to,
        now_from,
        now_to,
        file_hash: "h1".into(),
        rows,
        notes,
        ..ReviewStop::default()
    };
    let file = |path: &str, added, removed| ReviewFile {
        path: path.into(),
        status: "modified".into(),
        added,
        removed,
        ..ReviewFile::default()
    };
    ReviewBundle {
        frame: "brief".into(),
        repository: "/repo".into(),
        base: Some("4139a109c".into()),
        head: "12e1c7870".into(),
        stops: vec![
            stop(
                20,
                (1, 5),
                vec![
                    review_line("-", Some(1), None, "/// Sums the numbers from one to ten."),
                    review_line(
                        "+",
                        None,
                        Some(1),
                        "/// Sums the numbers from one to a limit.",
                    ),
                    review_line(" ", Some(2), Some(2), "fn main() {"),
                    review_line("-", Some(3), None, "    let total: u32 = (1..=10).sum();"),
                    review_line("+", None, Some(3), "    let total = sum_to(10);"),
                    review_line(" ", Some(4), Some(4), "    println!(\"total = {total}\");"),
                    review_line("+", None, Some(5), "}"),
                ],
                vec![review_note(22, 3, true, "The inline range is gone.")],
            ),
            stop(
                26,
                (7, 9),
                vec![
                    review_line("+", None, Some(7), "fn sum_to(limit: u32) -> u32 {"),
                    review_line("+", None, Some(8), "    (1..=limit).sum()"),
                    review_line(" ", Some(5), Some(9), "}"),
                ],
                vec![review_note(
                    28,
                    8,
                    false,
                    "Same range, with the limit as a parameter.",
                )],
            ),
        ],
        files: vec![file("totals.rs", 6, 2), file("README.md", 1, 1)],
        not_shown: vec![ReviewNotShown {
            path: "README.md".into(),
            reason: "wording only".into(),
            line: 32,
        }],
        ..ReviewBundle::default()
    }
}

/// The daemon reads `totals.rs` whole for the file view, as the Tauri audit's walkthrough shows it.
fn serve_totals(daemon: &Daemon) {
    daemon.on("review_file", |r: &Request| {
        let rows = vec![
            review_line("-", Some(1), None, "/// Sums the numbers from one to ten."),
            review_line(
                "+",
                None,
                Some(1),
                "/// Sums the numbers from one to a limit.",
            ),
            review_line(" ", Some(2), Some(2), "fn main() {"),
            review_line("-", Some(3), None, "    let total: u32 = (1..=10).sum();"),
            review_line("+", None, Some(3), "    let total = sum_to(10);"),
            review_line(" ", Some(4), Some(4), "    println!(\"total = {total}\");"),
            review_line("+", None, Some(5), "}"),
            review_line("+", None, Some(6), ""),
            review_line("+", None, Some(7), "fn sum_to(limit: u32) -> u32 {"),
            review_line("+", None, Some(8), "    (1..=limit).sum()"),
            review_line(" ", Some(5), Some(9), "}"),
            review_line(" ", Some(6), Some(10), ""),
            review_line(" ", Some(7), Some(11), "fn label(n: u32) -> String {"),
            review_line(" ", Some(8), Some(12), "    format!(\"item {n}\")"),
            review_line(" ", Some(9), Some(13), "}"),
        ];
        json!({"event": "review_file_result", "success": true, "path": r["path"],
            "rows": rows, "hash": "h1"})
    });
}

fn pixels<S: 'static>(
    rt: &mut Runtime<S>,
    compositor: &mut impl Compositor<S>,
) -> (u32, u32, Vec<u8>) {
    GPU.with(|gpu| draw(gpu, rt, compositor))
}

fn draw<S: 'static>(
    gpu: &Gpu,
    rt: &mut Runtime<S>,
    compositor: &mut impl Compositor<S>,
) -> (u32, u32, Vec<u8>) {
    let (w, h) = rt.window_viewport();
    let scale = rt.env.scale;
    let size = ((w * scale).round() as u32, (h * scale).round() as u32);
    let format = if rt.srgb_blend() {
        OFFSCREEN_FORMAT.remove_srgb_suffix()
    } else {
        OFFSCREEN_FORMAT
    };
    let mut renderer = Renderer::new(&gpu.device, format);
    renderer.set_srgb_blend(rt.srgb_blend());
    renderer.attach_text(&mut rt.text.atlas);
    rt.text.atlas.invalidate();
    let target = Offscreen::with_format(&gpu.device, size.0, size.1, format);
    renderer.upload_images(&gpu.device, &gpu.queue, &mut rt.images);
    renderer.prepare_surfaces(&gpu.device, &gpu.queue, &rt.surfaces, size);
    let mut encoder = gpu.device.create_command_encoder(&Default::default());
    compositor.render(&mut Frame {
        device: &gpu.device,
        queue: &gpu.queue,
        encoder: &mut encoder,
        target: &target.view,
        format,
        size,
        dt: 0.0,
        update_ms: 0.0,
        rt,
        ui: &mut renderer,
    });
    target.copy(&mut encoder);
    renderer.submit(&gpu.queue, encoder);
    (size.0, size.1, target.read(&gpu.device))
}

thread_local! {
    static GPU: Gpu = Gpu::try_with_features(
        Gpu::instance(),
        None,
        Compositor::<attn_app::State>::features(&Screens::default()),
    )
    .unwrap_or_else(|error| panic!("{error}"));
    static OUT: std::cell::RefCell<PathBuf> = std::cell::RefCell::default();
    static SCHEME: std::cell::RefCell<Option<String>> = std::cell::RefCell::default();
    static ONLY: std::cell::RefCell<Vec<(String, bool)>> = std::cell::RefCell::default();
    static SCENE: std::cell::RefCell<String> = std::cell::RefCell::default();
}

/// Whether this run draws the screen `name`: every screen, or those `--only` names. Until its
/// picture is saved, a failure names this one.
fn drawing(name: &str) -> bool {
    let chosen = ONLY.with_borrow_mut(|only| {
        let mut chosen = only.is_empty();
        for (pattern, matched) in only.iter_mut() {
            if glob(pattern, name) {
                *matched = true;
                chosen = true;
            }
        }
        chosen
    });
    if chosen {
        SCENE.set(name.to_owned());
    }
    chosen
}

/// Whether `name` matches `pattern`, where each `*` stands for any run of characters.
fn glob(pattern: &str, name: &str) -> bool {
    let mut parts = pattern.split('*');
    let Some(mut rest) = name.strip_prefix(parts.next().unwrap_or_default()) else {
        return false;
    };
    let parts: Vec<&str> = parts.collect();
    let Some((last, middle)) = parts.split_last() else {
        return rest.is_empty();
    };
    for part in middle {
        let Some(at) = rest.find(part) else {
            return false;
        };
        rest = &rest[at + part.len()..];
    }
    rest.ends_with(last)
}

fn save(name: &str, dark: bool, (w, h, rgba): (u32, u32, Vec<u8>)) {
    let appearance = if dark { "dark" } else { "light" };
    let path = OUT.with_borrow(|out| out.join(format!("{name}-{appearance}.png")));
    write_png(&path, w, h, &rgba).unwrap_or_else(|e| panic!("{}: {e}", path.display()));
    SCENE.with_borrow_mut(String::clear);
}

fn snap(world: &mut World, name: &str, dark: bool) {
    if !drawing(name) {
        return;
    }
    let mut shot = None;
    let mut cost = None;
    let mut cut = Vec::new();
    let mut reach = None;
    world.gesture(|app| {
        shot = Some(pixels(app.runtime, &mut Screens::default()));
        let elements = app.runtime.elements();
        cost = Some((elements.len(), app.runtime.instances.len()));
        cut = cut_text(&elements);
        reach = Some(reach_of(&elements));
    });
    save(name, dark, shot.expect("the window drew"));
    if let Some((nodes, instances)) = cost {
        record_cost(name, dark, nodes, instances);
    }
    record_cuts(name, dark, &cut);
    if dark && let Some(reach) = reach {
        record_reach(name, &reach);
    }
}

/// Text a clip cuts across its line: what it says and how many points of it the clip hides.
/// Text that truncates fits its box and is never cut, and text scrolled partly out of view is
/// cut only top or bottom. Text in a view that scrolls sideways is wider on purpose: there the
/// clipping element's child on the way to the text is wider than the clip, as a sideways
/// viewport's content takes its natural width. This leaves those out.
fn cut_text(elements: &[rune::ElementInfo]) -> Vec<(String, f32)> {
    let cuts = |e: &rune::ElementInfo| {
        let ([x, _, w, _], [vx, _, vw, _]) = (e.rect, e.visible);
        vx - x > 0.5 || (x + w) - (vx + vw) > 0.5
    };
    let scrolls_sideways = |at: usize| {
        let mut child = at;
        while let Some(parent) = elements[child].parent {
            if !cuts(&elements[parent]) {
                return elements[child].rect[2] > elements[parent].rect[2] + 0.5;
            }
            child = parent;
        }
        false
    };
    elements
        .iter()
        .enumerate()
        .filter(|(_, e)| !e.exiting && e.opacity > 0.0)
        .filter_map(|(at, e)| {
            let said = e.text.as_ref()?;
            let [x, _, w, _] = e.rect;
            let [vx, _, vw, vh] = e.visible;
            if said.trim().is_empty() || vw <= 0.0 || vh <= 0.0 || scrolls_sideways(at) {
                return None;
            }
            let hidden = (vx - x).max(0.0) + ((x + w) - (vx + vw)).max(0.0);
            (hidden > 0.5).then(|| (said.clone(), hidden))
        })
        .collect()
}

/// Each screen's cut text, beside its pictures in `clips.tsv`: one line a cut, and a screen
/// with none still listed with a count of 0.
fn record_cuts(name: &str, dark: bool, cut: &[(String, f32)]) {
    use std::io::Write;
    let appearance = if dark { "dark" } else { "light" };
    let path = OUT.with_borrow(|out| out.join("clips.tsv"));
    let mut file = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(&path)
        .unwrap_or_else(|e| panic!("{}: {e}", path.display()));
    let screen = format!("{name}-{appearance}");
    let mut lines = vec![format!("{screen}\t{}\t\t", cut.len())];
    lines.extend(cut.iter().map(|(said, hidden)| {
        format!(
            "{screen}\t\t{hidden:.1}\t{}",
            said.replace(['\t', '\n'], " ")
        )
    }));
    for line in lines {
        writeln!(file, "{line}").unwrap_or_else(|e| panic!("{}: {e}", path.display()));
    }
}

/// What tests and scripts find a screen by: each Tab stop in order, then every other keyed
/// element, each with its role and accessible name.
fn reach_of(elements: &[rune::ElementInfo]) -> String {
    let line = |e: &rune::ElementInfo| {
        format!(
            "{}\t{}\t{}\n",
            e.key.as_deref().unwrap_or("-"),
            e.role,
            e.label.as_deref().unwrap_or("-").replace('\n', " ")
        )
    };
    let live = elements.iter().filter(|e| !e.exiting && !e.inert);
    let stops: String = live.clone().filter(|e| e.focusable).map(line).collect();
    let mut keyed: Vec<String> = live
        .filter(|e| !e.focusable && e.key.is_some())
        .map(line)
        .collect();
    keyed.sort();
    format!("# tab stops\n{stops}# keyed\n{}", keyed.concat())
}

/// Each screen's Tab stops and keyed elements, in `reach/NAME.tsv`, for diffing two tours.
fn record_reach(name: &str, reach: &str) {
    let dir = OUT.with_borrow(|out| out.join("reach"));
    std::fs::create_dir_all(&dir).unwrap_or_else(|e| panic!("{}: {e}", dir.display()));
    let path = dir.join(format!("{name}.tsv"));
    std::fs::write(&path, reach).unwrap_or_else(|e| panic!("{}: {e}", path.display()));
}

/// Each screen's layout nodes and draw instances, beside its pictures in `cost.tsv`.
fn record_cost(name: &str, dark: bool, nodes: usize, instances: usize) {
    use std::io::Write;
    let appearance = if dark { "dark" } else { "light" };
    let path = OUT.with_borrow(|out| out.join("cost.tsv"));
    let mut file = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(&path)
        .unwrap_or_else(|e| panic!("{}: {e}", path.display()));
    writeln!(file, "{name}-{appearance}\t{nodes}\t{instances}")
        .unwrap_or_else(|e| panic!("{}: {e}", path.display()));
}

/// Draws a window with no terminals: the Quick Capture panel's.
struct Interface;

impl<S: 'static> Compositor<S> for Interface {
    fn render(&mut self, f: &mut Frame<'_, S>) {
        let (device, queue) = (f.device, f.queue);
        let clear = f.rt.clear_color();
        let core: &mut rune::RuntimeCore = f.rt;
        f.ui.prepare(device, queue, &mut core.text.atlas, &core.instances, f.size);
        f.ui.draw_with(f.encoder, f.target, clear, &mut |_, _| {});
    }
}

fn snap_capture(world: &mut World, name: &str, dark: bool) {
    if !drawing(name) {
        return;
    }
    let mut shot = None;
    world.capture_gesture(|panel| shot = Some(pixels(panel.runtime, &mut Interface)));
    save(name, dark, shot.expect("the capture panel drew"));
}

fn snap_tile_window(world: &mut World, name: &str, dark: bool) {
    if !drawing(name) {
        return;
    }
    let windows = world.tile_windows();
    let key = windows.first().expect("a tile window is open").key.clone();
    let mut shot = None;
    world.tile_gesture(&key, |app| {
        shot = Some(pixels(app.runtime, &mut Screens::default()))
    });
    save(name, dark, shot.expect("the tile window drew"));
}

fn sidebar_and_terminal(dark: bool) {
    let daemon = scripted().state(initial(dark, document_tile("m", "/repo/plan.md"), vec![]));
    let mut world = start(daemon);
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    print_colors(&mut world);
    select(&mut world);
    snap(&mut world, "sidebar-terminal-markdown", dark);
}

/// An agent's header while a document tile is active: lit in the brand colour, with its one
/// seed's state in a pill and its open ask's chip, beside the document's header and controls;
/// the agent's terminal draws a rounded box beside a square one. Hovered, the document's Focus
/// pill deepens its tint.
fn tile_headers(dark: bool, hovered: bool) {
    let mut state = tended_agent(dark);
    if let Some(desktop) = state.desktops.iter_mut().flatten().next() {
        desktop.active_pane_id = "m".into();
    }
    let mut world = start(scripted().state(state));
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    write(&mut world, BOXES);
    if hovered {
        world.gesture(|app| {
            let pill = app
                .runtime
                .find("tile.m.focus")
                .expect("the Focus pill is laid out");
            app.move_to(pill);
        });
    }
    let name = if hovered {
        "tile-headers-focus-hovered"
    } else {
        "tile-headers"
    };
    snap(&mut world, name, dark);
}

/// Agent `a` active beside a document tile, marked in each tile focus style.
fn tile_focus(dark: bool) {
    for style in FocusStyle::ALL {
        let mut world = start(scripted().state(tended_agent(dark)));
        deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
        write(&mut world, BOXES);
        run(&mut world, Action::SetTileFocus { style });
        snap(&mut world, &format!("tile-focus-{}", style.name()), dark);
    }
}

const BOXES: &str = "╭───────╮ ┌───────┐\r\n│ round │ │ box   │\r\n╰───────╯ └───────┘\r\n";

/// Agent `a` tends a growing seed and has an open ask, beside a document tile.
fn tended_agent(dark: bool) -> InitialStateMessage {
    let tended = Seed {
        claimed: true,
        tender: Some(PartyView {
            name: "Builder".into(),
            r#ref: "session:s0".into(),
            session_id: Some("s0".into()),
        }),
        ..seed("s-one", "Colors in one place", "growing", None)
    };
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![tended]);
    state.asks = Some(vec![Ask {
        id: "a-cache".into(),
        session_id: "s0".into(),
        subject: "Cache store".into(),
        base_dir: "/repo".into(),
        created_at: "2026-11-17T09:00:00Z".into(),
        ..Ask::default()
    }]);
    state
}

/// The title bar's shelf on a desktop wider than the window, as the audit's desktop: the
/// waiting reviewer active beside a shell and a document, each marked by its state or kind, and
/// two agents off screen as chips; `hovered`, with the pointer on its dots, which show the
/// width control.
fn shelf(dark: bool, hovered: bool) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    let named = |id: &str, label: &str, agent: &str, status: &str| Session {
        label: label.into(),
        agent: agent.into(),
        ..session(id, status)
    };
    state.sessions.get_or_insert_default().extend([
        named("x-r", "reviewer", "claude", "waiting_input"),
        named("x-sh", "shell", "shell", "idle"),
        named("x-w", "builder", "claude", "working"),
        named("x-i", "scout", "codex", "idle"),
    ]);
    if let Some(desktop) = state.desktops.iter_mut().flatten().next() {
        desktop.panes = vec![
            agent_pane(DESKTOP, "r", "x-r"),
            agent_pane(DESKTOP, "sh", "x-sh"),
            agent_pane(DESKTOP, "w", "x-w"),
            agent_pane(DESKTOP, "i", "x-i"),
        ];
        desktop.active_pane_id = "r".into();
        desktop.tree_json = row(vec![
            pane("r"),
            pane("sh"),
            document_tile("m", "/repo/plan.md"),
            pane("w"),
            pane("i"),
        ])
        .to_string();
    }
    if let Some(settings) = state.settings.as_mut() {
        settings.insert("narrowest_tile_cols".into(), "40".into());
    }
    let mut world = start(scripted().state(state));
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    if hovered {
        world.gesture(|app| {
            let dots = app
                .runtime
                .find("shelf.live")
                .expect("the shelf's dots are laid out");
            app.move_to(dots);
        });
    }
    snap(
        &mut world,
        if hovered {
            "shelf-width-control"
        } else {
            "shelf"
        },
        dark,
    );
}

fn review_stop(dark: bool) {
    let daemon = scripted().state(initial(dark, document_tile("m", "/repo/review.md"), vec![]));
    let mut world = start(daemon);
    deliver(&mut world, "/repo/review.md", REVIEW, Some(review()));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    snap(&mut world, "review-stop", dark);
}

fn with_document(dark: bool) -> World {
    with_document_and(dark, |_| {})
}

/// [`with_document`], its daemon also answering what `answers` registers.
fn with_document_and(dark: bool, answers: impl FnOnce(&Daemon)) -> World {
    garden_world_and(dark, seeds(), answers)
}

/// A log entry on a seed's page, written `kind` by an agent.
fn log_note(id: &str, kind: &str, body: &str, at: &str) -> SeedNote {
    SeedNote {
        id: id.into(),
        kind: kind.into(),
        body: body.into(),
        created_at: at.into(),
        author: ActorView {
            name: "reviewer".into(),
            r#ref: "session:s0".into(),
        },
        ..SeedNote::default()
    }
}

/// The document tile's world with the garden holding `seeds`, whose pages the daemon reads with
/// two entries on `s-one`'s log.
fn garden_world(dark: bool, seeds: Vec<Seed>) -> World {
    garden_world_and(dark, seeds, |_| {})
}

/// [`garden_world`], its daemon also answering what `answers` registers.
fn garden_world_and(dark: bool, seeds: Vec<Seed>, answers: impl FnOnce(&Daemon)) -> World {
    let daemon = scripted().state(initial(
        dark,
        document_tile("m", "/repo/plan.md"),
        seeds.clone(),
    ));
    answers(&daemon);
    let known = seeds;
    daemon.on("seed_document_get", move |r: &Request| {
        let notes = if r["seed_id"] == "s-one" {
            vec![
                log_note(
                    "n-2",
                    "note",
                    "Tauri row is 28 pt, native row is 29 pt.",
                    "2026-11-13T09:10:00Z",
                ),
                log_note("n-1", "move", "tended", "2026-11-13T09:05:00Z"),
            ]
        } else {
            Vec::new()
        };
        message(SeedDocumentGetResultMessage {
            request_id: r["request_id"].as_str().unwrap_or_default().into(),
            success: true,
            document: known
                .iter()
                .find(|s| s.id == r["seed_id"])
                .cloned()
                .map(|seed| SeedDocument {
                    seed,
                    notes_total: notes.len() as i64,
                    notes,
                    ..SeedDocument::default()
                }),
            ..SeedDocumentGetResultMessage::new()
        })
    });
    daemon.on("get_command_usage", |r: &Request| {
        message(GetCommandUsageResultMessage {
            request_id: r["request_id"].as_str().unwrap_or_default().into(),
            profile_id: r["profile_id"].as_str().unwrap_or_default().into(),
            success: true,
            ..GetCommandUsageResultMessage::new()
        })
    });
    let mut world = start(daemon);
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    world
}

fn garden(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::BoardOpen);
    world.gesture(|app| app.click(Target::role("button").name("Expand the garden")));
    world.gesture(|app| app.click(Target::key("garden.row.s-plot")));
    snap(&mut world, "garden", dark);
}

fn garden_dock(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::BoardOpen);
    snap(&mut world, "garden-dock", dark);
}

/// A seed's page beside the columns that walked to it.
fn garden_seed(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::BoardOpen);
    world.gesture(|app| app.click(Target::role("button").name("Expand the garden")));
    world.gesture(|app| app.click(Target::key("garden.row.s-plot")));
    world.gesture(|app| app.click(Target::key("garden.row.s-one")));
    snap(&mut world, "garden-seed", dark);
}

fn garden_dock_seed(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::BoardOpen);
    world.gesture(|app| app.click(Target::key("garden.row.s-plot")));
    world.gesture(|app| app.click(Target::key("garden.row.s-one")));
    snap(&mut world, "garden-dock-seed", dark);
}

fn garden_empty(dark: bool) {
    let mut world = garden_world(dark, Vec::new());
    press(&mut world, Action::BoardOpen);
    world.gesture(|app| app.click(Target::role("button").name("Expand the garden")));
    snap(&mut world, "garden-empty", dark);
}

fn garden_dock_empty(dark: bool) {
    let mut world = garden_world(dark, Vec::new());
    press(&mut world, Action::BoardOpen);
    snap(&mut world, "garden-dock-empty", dark);
}

fn garden_dock_plot(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::BoardOpen);
    world.gesture(|app| app.click(Target::key("garden.row.s-plot")));
    snap(&mut world, "garden-dock-plot", dark);
}

/// A garden review of two seeds, the first `s-one`, advised or with its advisor still running.
fn garden_review(advised: bool) -> GardenReview {
    let item = |seed: &str, title: &str| GardenReviewItem {
        id: format!("r-1.{seed}"),
        run_id: "r-1".into(),
        seed_id: seed.into(),
        seed_rev: 1,
        evidence_version: "evidence-1".into(),
        title: title.into(),
        evidence: vec![
            GardenReviewEvidence {
                label: "Review signal".into(),
                text: "No log entry for nine days.".into(),
            },
            GardenReviewEvidence {
                label: "Tender".into(),
                text: "reviewer, waiting for input".into(),
            },
        ],
        actions: ["handover", "keep_growing", "park", "harvest", "wither"]
            .map(String::from)
            .to_vec(),
        status: "ready".into(),
        resolution: "unresolved".into(),
        recommendation: Some("park".into()),
        explanation: Some(
            "Useful work remains, but nothing needs an agent until the palette lands.".into(),
        ),
        cited_evidence: Some(vec!["No log entry for nine days.".into()]),
        ..GardenReviewItem::default()
    };
    let mut first = item("s-one", "Colors in one place");
    if !advised {
        first.status = "running".into();
        first.recommendation = None;
        first.explanation = None;
        first.cited_evidence = None;
        first.advisor_attempt = Some(1);
    }
    let mut second = item("s-two", "Catppuccin color schemes");
    second.status = "queued".into();
    second.recommendation = None;
    GardenReview {
        run: GardenReviewRun {
            profile_id: PROFILE.into(),
            id: "r-1".into(),
            candidate_ids: vec!["s-one".into(), "s-two".into()],
            recipe: GardenReviewRecipe {
                agent: "codex".into(),
                model: "gpt-5.6-luna".into(),
                effort: None,
            },
            status: "running".into(),
            captured_at: "2026-11-13T09:00:00Z".into(),
            ..GardenReviewRun::default()
        },
        items: vec![first, second],
    }
}

/// The garden's review of [`garden_review`], opened in the dock or expanded to the window.
fn garden_review_screen(dark: bool, advised: bool, full: bool, name: &str) {
    let shown = garden_review(advised);
    let mut world = with_document_and(dark, move |daemon| {
        daemon.on("seed_review_show", move |r: &Request| {
            message(SeedReviewResultMessage {
                request_id: r["request_id"].as_str().unwrap_or_default().into(),
                profile_id: PROFILE.into(),
                operation: "show".into(),
                success: true,
                candidate_count: 2,
                review: Some(shown.clone()),
                ..SeedReviewResultMessage::new()
            })
        });
    });
    press(&mut world, Action::BoardOpen);
    if full {
        world.gesture(|app| app.click(Target::role("button").name("Expand the garden")));
    }
    world.gesture(|app| app.click(Target::role("button").name("Continue review")));
    snap(&mut world, name, dark);
}

fn settings_page(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::UiOpenSettings);
    world.gesture(|app| app.click(Target::role("button").name("Appearance")));
    snap(&mut world, "settings", dark);
}

/// The Agents page: its switches, the default agent and the seeds-on-standby choices.
fn settings_agents(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::UiOpenSettings);
    world.gesture(|app| app.click(Target::key("settings.nav.agents")));
    snap(&mut world, "settings-agents", dark);
}

fn shortcut_editor(dark: bool) {
    let mut world = with_document(dark);
    run(&mut world, Action::OpenShortcutEditor);
    snap(&mut world, "shortcut-editor", dark);
}

fn close_desktop(dark: bool) {
    let mut world = with_document(dark);
    run(
        &mut world,
        Action::CloseDesktop {
            desktop: DESKTOP.into(),
        },
    );
    snap(&mut world, "close-desktop", dark);
}

fn palette(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::UiCommandPalette);
    snap(&mut world, "palette", dark);
}

/// The command palette for a query nothing matches.
fn palette_no_match(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::UiCommandPalette);
    world.gesture(|app| app.type_text("nothing matches this"));
    snap(&mut world, "palette-no-match", dark);
}

/// The markdown opener over the window, its recent files and the agent's folder listed.
fn opener(dark: bool) {
    let mut world = with_document_and(dark, |daemon| {
        daemon.on("recent_files", |r: &Request| {
            let recent = |path: &str, at: &str| FileActivity {
                path: path.into(),
                last_at: at.into(),
                count: 1,
                source: "open".into(),
                ..FileActivity::default()
            };
            message(RecentFilesResultMessage {
                request_id: r["request_id"].as_str().unwrap_or_default().into(),
                success: true,
                files: vec![
                    recent("/repo/plan.md", "2026-07-24T10:00:00Z"),
                    recent("/repo/docs/colors.md", "2026-07-23T10:00:00Z"),
                ],
                ..RecentFilesResultMessage::new()
            })
        });
        daemon.on("fs_index", |r: &Request| {
            message(FsIndexResultMessage {
                request_id: r["request_id"].as_str().unwrap_or_default().into(),
                success: true,
                root: r["root"].as_str().unwrap_or_default().into(),
                files: ["README.md", "docs/colors.md", "docs/themes.md", "plan.md"]
                    .map(str::to_owned)
                    .to_vec(),
                ..FsIndexResultMessage::new()
            })
        });
    });
    press(&mut world, Action::FileOpen);
    snap(&mut world, "opener", dark);
}

/// The prompt for the agent on screen's context window cap, from the palette.
fn context_cap(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::UiCommandPalette);
    world.gesture(|app| app.type_text("context window"));
    tap(&mut world, Key::Enter);
    snap(&mut world, "context-cap", dark);
}

const MESSAGE: &str = "The cache keeps every row it reads until the window closes.";
const KNOWN_SEED: &str = "s-7k3f9m";

/// Terminal `a` with every overlay its colors paint: a comment mark on the agent's message, a
/// seed id the garden knows, a selected command block, and a find's matches.
fn terminal_overlays(dark: bool) {
    let mut state = initial(
        dark,
        document_tile("m", "/repo/plan.md"),
        vec![seed(
            KNOWN_SEED,
            "A seed the terminal names",
            "planted",
            None,
        )],
    );
    if let Some(sessions) = state.sessions.as_mut() {
        sessions[0].id = "s0".into();
    }
    let daemon = scripted().state(state);
    daemon
        .on("session_messages_get", |_: &Request| {
            message(SessionMessagesGetResultMessage {
                session_id: "s0".into(),
                success: true,
                status: "ready".into(),
                messages: vec![SessionMessage {
                    key: "m1".into(),
                    markdown: MESSAGE.into(),
                }],
                ..SessionMessagesGetResultMessage::new()
            })
        })
        .on("session_annotations_get", |_: &Request| {
            message(SessionAnnotationsGetResultMessage {
                session_id: "s0".into(),
                success: true,
                note: Some(String::new()),
                ..SessionAnnotationsGetResultMessage::new()
            })
        })
        .on("session_annotations_save", |_: &Request| {
            message(SessionAnnotationsSaveResultMessage {
                session_id: "s0".into(),
                success: true,
                ..SessionAnnotationsSaveResultMessage::new()
            })
        });
    let mut world = start(daemon);
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    write(
        &mut world,
        &format!(
            "{MESSAGE}\r\nsee {KNOWN_SEED} for the plan\r\n\x1b]133;A\x07$ \x1b]133;B\x07make test\r\n\x1b]133;C\x07ok apple pie\r\nall apple tests pass\r\n\x1b]133;D;0\x07\x1b]133;A\x07$ "
        ),
    );
    drag(&mut world, None, (5, 4), (5, 4));
    drag(&mut world, Some(alt()), (4, 0), (20, 0));
    world.gesture(|app| {
        app.press(if cfg!(target_os = "macos") {
            "mod+f"
        } else {
            "mod+shift+f"
        })
    });
    world.gesture(|app| app.type_text("apple"));
    let overlays = world.terminal_view("r-a").expect("terminal a is drawn")["overlays"].clone();
    let scheme = SCHEME.with_borrow(Clone::clone);
    let colors = attn_ui::colors::scheme_for(scheme.as_deref())
        .colors(dark)
        .terminal;
    for color in
        [colors.annotation, colors.seed, colors.block, colors.find].map(attn_ui::colors::to_hex)
    {
        let shown = overlays
            .as_array()
            .is_some_and(|all| all.iter().any(|o| o["color"] == color));
        assert!(shown, "terminal a shows no {color} overlay: {overlays}");
    }
    snap(&mut world, "terminal-overlays", dark);
}

fn alt() -> rune::Mods {
    rune::Mods {
        alt: true,
        ..rune::Mods::default()
    }
}

fn drag(world: &mut World, mods: Option<rune::Mods>, from: (u16, u16), to: (u16, u16)) {
    let (a, b) = (cell(world, from.0, from.1), cell(world, to.0, to.1));
    if let Some(mods) = mods {
        world.gesture(|app| app.hold(mods));
    }
    world.gesture(|app| {
        app.runtime.pointer_down(a.0, a.1);
        app.runtime.pointer_move(b.0, b.1);
        app.runtime.pointer_up(b.0, b.1);
    });
    world.gesture(|app| app.hold(rune::Mods::default()));
}

/// Agent `a` popped out into a window of its own, with its seed, its ask and a box in its
/// terminal.
fn tile_window(dark: bool) {
    let mut state = tended_agent(dark);
    let mut popped = pane("a");
    popped["window"] = json!({"x": 10.0, "y": 20.0, "width": 600.0, "height": 868.0});
    if let Some(desktop) = state.desktops.iter_mut().flatten().next() {
        desktop.tree_json = row(vec![popped, document_tile("m", "/repo/plan.md")]).to_string();
    }
    let mut world = start(scripted().state(state));
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    write(&mut world, BOXES);
    snap_tile_window(&mut world, "tile-window", dark);
}

const SHOP: &str = "/home/me/projects/shop";

/// Launcher 2.0 on: Claude picked, the shop repository last started in its main checkout, and
/// its two recents, shop first. Desktop 1 is empty when `empty`, else it holds terminal `a`.
fn launcher_daemon(dark: bool, empty: bool) -> Daemon {
    launcher_daemon_with(dark, empty, true)
}

fn launcher_daemon_with(dark: bool, empty: bool, launcher_2: bool) -> Daemon {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    if let Some(desktops) = state.desktops.as_mut() {
        desktops[0].tree_json = pane("a").to_string();
        if empty {
            desktops[0].tree_json.clear();
            desktops[0].panes.clear();
            desktops[0].active_pane_id.clear();
        }
    }
    if let Some(settings) = state.settings.as_mut() {
        for (key, value) in [
            (
                "new_session_launcher_enabled",
                if launcher_2 { "true" } else { "false" },
            ),
            ("new_session_agent", "claude"),
            ("claude_cap_yolo", "true"),
            ("projects_directory", "/home/me/projects"),
            (
                "new_session_destination_local_/home/me/projects/shop",
                "main_repo",
            ),
        ] {
            settings.insert(key.into(), value.into());
        }
    }
    let daemon = scripted().state(state);
    daemon
        .on("delegation_preferences_get", |_: &Request| {
            message(DelegationPreferencesResultMessage {
                success: true,
                preferences: Some(DelegationPreferences::default()),
                harnesses: Some(
                    ["claude", "codex"]
                        .iter()
                        .map(|id| Harness {
                            id: (*id).into(),
                            name: (*id).into(),
                            available: true,
                            ..Harness::default()
                        })
                        .collect(),
                ),
                ..DelegationPreferencesResultMessage::new()
            })
        })
        .on("get_recent_locations", |_: &Request| {
            message(RecentLocationsResultMessage {
                success: true,
                home_path: Some("/home/me".into()),
                recent_locations: [(SHOP, true), ("/home/me/projects/api", false)]
                    .iter()
                    .map(|(path, repo)| RecentLocation {
                        path: (*path).into(),
                        last_seen: "2026-11-13T08:00:00Z".into(),
                        use_count: 1,
                        repo_root: repo.then(|| (*path).into()),
                        ..RecentLocation::default()
                    })
                    .collect(),
                ..RecentLocationsResultMessage::new()
            })
        })
        .on("browse_directory", |_: &Request| {
            message(BrowseDirectoryResultMessage {
                success: true,
                directory: "/home/me/projects".into(),
                home_path: Some("/home/me".into()),
                entries: ["shop", "site"]
                    .iter()
                    .map(|name| DirectoryEntry {
                        name: (*name).into(),
                        path: format!("/home/me/projects/{name}"),
                        is_dir: true,
                        is_repo: Some(*name == "shop"),
                    })
                    .collect(),
                ..BrowseDirectoryResultMessage::new()
            })
        })
        .on("get_repo_info", |_: &Request| {
            message(GetRepoInfoResultMessage {
                success: true,
                info: Some(RepoInfo {
                    repo: SHOP.into(),
                    current_branch: "main".into(),
                    default_branch: "main".into(),
                    worktrees: vec![Worktree {
                        path: format!("{SHOP}--feature"),
                        branch: "feature".into(),
                        main_repo: SHOP.into(),
                        ..Worktree::default()
                    }],
                    ..RepoInfo::default()
                }),
                ..GetRepoInfoResultMessage::new()
            })
        });
    daemon
}

fn launcher_dialog(dark: bool) {
    let mut world = start(launcher_daemon(dark, false));
    press(&mut world, Action::SessionNew);
    snap(&mut world, "launcher-dialog", dark);
}

/// The shop's Where list with a branch typed, so the name it shows is not the random one: one
/// that its `feature` worktree still matches.
fn launcher_where(dark: bool) {
    let mut world = start(launcher_daemon(dark, false));
    press(&mut world, Action::SessionNew);
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Tab,
            mods: Mods::default(),
        })
    });
    world.gesture(|app| app.type_text("feat"));
    snap(&mut world, "launcher-where", dark);
}

/// Launcher 2.0 browsing the projects folder, with the rest of its first folder ghosted after
/// what was typed.
fn launcher_browse(dark: bool) {
    let mut world = start(launcher_daemon(dark, false));
    press(&mut world, Action::SessionNew);
    world.gesture(|app| app.type_text("~/projects/s"));
    world.advance(std::time::Duration::from_millis(200));
    snap(&mut world, "launcher-browse", dark);
}

/// The classic new-session picker, Launcher 2.0 off: its harness toggle and its key footer.
fn picker_dialog(dark: bool) {
    let mut world = start(launcher_daemon_with(dark, false, false));
    press(&mut world, Action::SessionNew);
    snap(&mut world, "picker-dialog", dark);
}

/// The classic picker an empty desktop shows in place of its tiles: no Esc hint.
fn picker_empty_desktop(dark: bool) {
    let mut world = start(launcher_daemon_with(dark, true, false));
    snap(&mut world, "picker-empty-desktop", dark);
}

fn shortcuts(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::UiShowShortcuts);
    snap(&mut world, "shortcuts", dark);
}

fn agent_palette(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::UiActionMenu);
    snap(&mut world, "agent-palette", dark);
}

fn profile_switcher(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::ProfileSwitch);
    snap(&mut world, "profile-switcher", dark);
}

/// What's new from the palette, `steps` steps in: the first, and the queue's, whose keys have
/// alternatives. Each snap has a world of its own, as a world's second snap lacks the glyphs
/// the first one uploaded.
fn whats_new(dark: bool, steps: usize, name: &str) {
    let mut world = with_document(dark);
    run(&mut world, Action::OpenWhatsNew);
    for _ in 0..steps {
        tap(&mut world, Key::Right);
    }
    world.advance(std::time::Duration::from_secs(2));
    snap(&mut world, name, dark);
}

fn tap(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

fn settings_analytics(dark: bool) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), seeds());
    if let Some(settings) = state.settings.as_mut() {
        settings.insert("analytics.enabled".into(), "true".into());
    }
    let mut world = start(scripted().state(state));
    press(&mut world, Action::UiOpenSettings);
    world.gesture(|app| app.click(Target::key("settings.search.field")));
    world.gesture(|app| app.type_text("analytics"));
    world.gesture(|app| app.click(Target::key("settings.nav.analytics")));
    snap(&mut world, "settings-analytics", dark);
}

/// Outposts and Launcher 2.0 turned on, so their pages show their on switches.
const SWITCHED_ON: &[(&str, &str)] = &[
    ("outposts_enabled", "true"),
    ("new_session_launcher_enabled", "true"),
];

/// A Settings page the Tauri audit compared, with its buttons, fields and switches: the
/// settings `settings` set and the daemon answering as `answer` scripts it, reached through the
/// settings search for `search`.
fn settings_section(
    dark: bool,
    section: &str,
    search: &str,
    settings: &[(&str, &str)],
    answer: impl FnOnce(&Daemon),
) {
    let mut world = settings_world(dark, section, search, settings, |_| {}, answer);
    snap(&mut world, &format!("settings-{section}"), dark);
}

/// The Settings dialog on `section`, its state changed by `change` and the daemon answering as
/// `answer` scripts it.
fn settings_world(
    dark: bool,
    section: &str,
    search: &str,
    settings: &[(&str, &str)],
    change: impl FnOnce(&mut InitialStateMessage),
    answer: impl FnOnce(&Daemon),
) -> World {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), seeds());
    if let Some(set) = state.settings.as_mut() {
        set.extend(settings.iter().map(|&(k, v)| (k.to_owned(), v.to_owned())));
    }
    change(&mut state);
    let daemon = scripted().state(state);
    answer(&daemon);
    let mut world = start(daemon);
    press(&mut world, Action::UiOpenSettings);
    world.gesture(|app| app.click(Target::key("settings.search.field")));
    world.gesture(|app| app.type_text(search));
    world.gesture(|app| app.click(Target::key(format!("settings.nav.{section}"))));
    world
}

/// A Settings page scrolled to its end.
fn settings_bottom(dark: bool, section: &str, search: &str, settings: &[(&str, &str)]) {
    let mut world = settings_world(dark, section, search, settings, |_| {}, |_| {});
    world.gesture(|app| app.update(|_, tx| tx.scroll_to_bottom_instant("settings.body")));
    snap(&mut world, &format!("settings-{section}-bottom"), dark);
}

/// Files and locations with a saved projects path, and Appearance at its top, its font sizes
/// and its end, in
/// the smallest window at 150% text, where the page keeps 163 points.
fn settings_smallest(dark: bool) {
    let settings = [
        ("uiScale", "1.5"),
        ("gardenScale", "1.4"),
        (
            "projects_directory",
            "/home/someone/projects/社内プラットフォーム/InternationalEngineeringObservability",
        ),
    ];
    for (section, search) in [("desktop", "files"), ("general", "appearance")] {
        let mut world = settings_world(dark, section, search, &settings, |_| {}, |_| {});
        world.resize((800.0, 600.0));
        snap(&mut world, &format!("settings-smallest-{section}"), dark);
        if section == "general" {
            world.gesture(|app| {
                app.update(|_, tx| {
                    tx.scroll_center_in_instant("settings.body", "settings.block.font-size")
                })
            });
            snap(&mut world, "settings-smallest-general-font-size", dark);
            world.gesture(|app| app.update(|_, tx| tx.scroll_to_bottom_instant("settings.body")));
            snap(&mut world, "settings-smallest-general-bottom", dark);
        }
    }
}

/// GitHub hosts with polling off, so the page shows why.
fn settings_connectivity(dark: bool) {
    let mut world = settings_world(
        dark,
        "connectivity",
        "github",
        &[],
        |state| {
            state.github_polling_off_reason =
                Some("GitHub polling is off: gh is not installed".into());
        },
        |_| {},
    );
    snap(&mut world, "settings-connectivity", dark);
}

/// Models with one harness reporting a model attn gives no tier.
fn settings_models(dark: bool) {
    settings_section(dark, "models", "models", &[], |daemon| {
        daemon.on("delegation_preferences_get", |r: &Request| {
            message(DelegationPreferencesResultMessage {
                request_id: r["request_id"].as_str().unwrap_or_default().into(),
                success: true,
                preferences: Some(DelegationPreferences::default()),
                harnesses: Some(vec![Harness {
                    id: "claude".into(),
                    name: "Claude Code".into(),
                    available: true,
                    model_pin: true,
                    effort_pin: true,
                    discovery: true,
                }]),
                templates: Some(Vec::new()),
                expanded_roles: Some(Vec::new()),
                ..DelegationPreferencesResultMessage::new()
            })
        });
        daemon.on("harness_models", |r: &Request| {
            message(HarnessModelsResultMessage {
                request_id: r["request_id"].as_str().unwrap_or_default().into(),
                success: true,
                models: vec![HarnessModel {
                    harness: "claude".into(),
                    id: "mock-agent-1".into(),
                    name: "Mock agent 1".into(),
                    access: "unknown".into(),
                    effort_support: "unsupported".into(),
                    tier_source: "none".into(),
                    ..HarnessModel::default()
                }],
                ..HarnessModelsResultMessage::new()
            })
        });
    });
}

/// The diagnostic report's choices, its Save the primary action.
fn diagnostic_report(dark: bool) {
    let mut world = with_document(dark);
    world.daemon.on("support_snapshot", |_: &Request| {
        json!({
            "event": "support_snapshot_result",
            "protocol_version": "1",
            "daemon_instance_id": "daemon-home",
            "daemon_started_at_unix_ms": 1,
            "captured_at_unix_ms": 2,
            "backend": "worker",
            "warning_codes": [],
            "trace_capacity": 512,
            "trace_total": 0,
            "input_traces": [],
            "runtimes": [],
        })
    });
    run(&mut world, Action::CreateDiagnosticReport);
    snap(&mut world, "diagnostic-report", dark);
}

/// Quick Capture open on an empty message, so Send is unavailable. It keeps drafts only for a
/// profile whose id is a `profile-` uuid.
fn quick_capture_world(dark: bool) -> World {
    const CAPTURE_PROFILE: &str = "profile-0b7f2a3c-1111-4222-8333-444455556666";
    let crew = crew()
        .into_iter()
        .map(|member| CrewMember {
            profile_id: CAPTURE_PROFILE.into(),
            ..member
        })
        .collect();
    let state = InitialStateMessage {
        selected_profile_id: Some(CAPTURE_PROFILE.into()),
        profiles: Some(vec![Profile {
            id: CAPTURE_PROFILE.into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        crew: Some(crew),
        settings: settings(dark),
        ..InitialStateMessage::new()
    };
    let mut world = World::start_sized(scripted().state(state), window());
    run(&mut world, Action::OpenQuickCapture);
    world
}

fn quick_capture_empty(dark: bool) {
    let mut world = quick_capture_world(dark);
    snap_capture(&mut world, "quick-capture-empty", dark);
}

/// Quick Capture with a message typed, so Send is its primary action.
fn quick_capture(dark: bool) {
    let mut world = quick_capture_world(dark);
    world.capture_gesture(|panel| panel.type_text("Ship the cache fix"));
    snap_capture(&mut world, "quick-capture", dark);
}

/// Quick Capture's mailbox list, with the chosen mailbox marked.
fn quick_capture_mailboxes(dark: bool) {
    let mut world = quick_capture_world(dark);
    world.capture_gesture(|panel| panel.type_text("Ship the cache fix"));
    world.capture_gesture(|panel| panel.click(Target::key("capture.to")));
    snap_capture(&mut world, "quick-capture-mailboxes", dark);
}

/// The fullscreen notebook with a note open from its tree.
fn notebook_open(dark: bool) -> World {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    if let Some(settings) = state.settings.as_mut() {
        settings.insert(
            "notebook.root.effective".to_owned(),
            "/home/me/notebook".to_owned(),
        );
    }
    let daemon = scripted().state(state);
    daemon
        .on("fs_list", |_: &Request| {
            let entry = |path: &str, is_dir: bool| {
                json!({ "path": path, "name": path, "is_dir": is_dir, "size": 1 })
            };
            json!({ "event": "fs_list_result", "success": true,
                "entries": [entry("journal", true), entry("todo.md", false)] })
        })
        .on("fs_index", |_: &Request| {
            json!({ "event": "fs_index_result", "success": true, "root": "/home/me/notebook",
                "files": ["journal/2026-11-14.md", "todo.md"], "truncated": false })
        });
    let mut world = start(daemon);
    world
        .automate("run", json!({ "name": "notebook.openFullscreen" }))
        .unwrap();
    world.settle();
    world.gesture(|app| app.click(Target::key("notebook.file.todo.md")));
    world
}

fn notebook_file(dark: bool) {
    let mut world = notebook_open(dark);
    snap(&mut world, "notebook-file", dark);
}

/// The note the notebook screens open: a heading, a list with inline code, a link to a note
/// that is missing, and an HTML comment.
const NOTE: &str = "---\ntype: note\n---\n# Notebook\n\nThis folder is the journal attn writes on your behalf, and the knowledge it keeps.\n\n- `journal/` holds one `log.md` per day.\n- `knowledge/` holds a note per area, each with a `type`.\n\nLink to [an area note](knowledge/area.md) — not wikilinks.\n\n<!-- okf_version: 0.1 -->\n";

/// A daemon serving a notebook of three folders and `index.md`, which links to a note that is
/// missing, on a desktop that holds `tile` beside an agent, or the editor tile `n` alone.
fn notebook_daemon(dark: bool, tile: Option<Value>) -> Daemon {
    let alone = tile.is_none();
    let mut state = initial(dark, tile.unwrap_or_else(notebook_tile), vec![]);
    if let Some(desktops) = state.desktops.as_mut().filter(|_| alone) {
        desktops[0].tree_json = notebook_tile().to_string();
        desktops[0].panes.clear();
        desktops[0].active_pane_id = "n".into();
    }
    if let Some(settings) = state.settings.as_mut() {
        settings.insert(
            "notebook.root.effective".to_owned(),
            "/home/me/notebook".to_owned(),
        );
    }
    let daemon = scripted().state(state);
    daemon
        .on("fs_list", |req: &Request| {
            let entry = |path: &str, is_dir: bool| {
                let name = path.rsplit('/').next().unwrap_or(path);
                json!({ "path": path, "name": name, "is_dir": is_dir, "size": 1 })
            };
            let entries = match req["path"].as_str().unwrap_or("") {
                "" => vec![
                    entry("crew", true),
                    entry("journal", true),
                    entry("knowledge", true),
                    entry("index.md", false),
                ],
                _ => vec![],
            };
            json!({ "event": "fs_list_result", "success": true, "entries": entries })
        })
        .on("fs_read", |req: &Request| {
            json!({ "event": "fs_read_result", "success": true,
                "result": { "path": req["path"], "content": NOTE, "hash": "h1" } })
        })
        .on("fs_exists_many", |req: &Request| {
            let results: Vec<Value> = req["paths"]
                .as_array()
                .into_iter()
                .flatten()
                .map(|path| json!({ "path": path, "exists": false }))
                .collect();
            json!({ "event": "fs_exists_many_result", "success": true, "results": results })
        })
        .on("notebook_backlinks", |_: &Request| {
            json!({ "event": "notebook_backlinks_result", "success": true, "entries": [] })
        })
        .on("fs_index", |_: &Request| {
            json!({ "event": "fs_index_result", "success": true, "root": "/home/me/notebook",
                "files": ["index.md", "journal/2026-11-14.md", "knowledge/attn.md"],
                "truncated": false })
        });
    daemon
}

fn notebook_tile() -> Value {
    json!({ "type": "tile", "tile_id": "n", "tile_kind": "notebook", "tile_params": "" })
}

/// An editor tile with nothing open in it, its finder closed.
fn notebook_tile_empty(dark: bool) {
    let mut world = start(notebook_daemon(dark, None));
    world.gesture(|app| app.press("escape"));
    snap(&mut world, "notebook-tile-empty", dark);
}

/// An editor tile with `index.md` open from its tree.
fn notebook_tile_open(dark: bool) -> World {
    let mut world = start(notebook_daemon(dark, None));
    world.gesture(|app| app.press("escape"));
    world.gesture(|app| app.click(Target::key("notebook.tile.n.file.index.md")));
    world
}

fn notebook_tile_note(dark: bool) {
    let mut world = notebook_tile_open(dark);
    snap(&mut world, "notebook-tile-note", dark);
}

/// The editor tile's finder over its open note.
fn notebook_tile_finder(dark: bool) {
    let mut world = notebook_tile_open(dark);
    run(&mut world, Action::FileOpen);
    snap(&mut world, "notebook-tile-finder", dark);
}

/// The fullscreen notebook with `index.md` open.
fn notebook_note(dark: bool) {
    let mut world = start(notebook_daemon(
        dark,
        Some(document_tile("m", "/repo/plan.md")),
    ));
    world
        .automate("run", json!({ "name": "notebook.openFullscreen" }))
        .unwrap();
    world.settle();
    world.gesture(|app| app.click(Target::key("notebook.file.index.md")));
    snap(&mut world, "notebook-note", dark);
}

/// The notebook's finder over the open note.
fn notebook_finder(dark: bool) {
    let mut world = notebook_open(dark);
    run(&mut world, Action::FileOpen);
    snap(&mut world, "notebook-finder", dark);
}

/// Delegation with no roles yet and Attn's Builder to add: its Add Attn roles and + Custom
/// role.
fn settings_delegation(dark: bool) {
    settings_section(dark, "delegation", "delegation", &[], |daemon| {
        daemon.on("delegation_preferences_get", |r: &Request| {
            message(DelegationPreferencesResultMessage {
                request_id: r["request_id"].as_str().unwrap_or_default().into(),
                success: true,
                preferences: Some(DelegationPreferences::default()),
                templates: Some(vec![DelegationRole {
                    id: "builder".into(),
                    builtin: Some("builder".into()),
                    ..DelegationRole::default()
                }]),
                expanded_roles: Some(Vec::new()),
                harnesses: Some(Vec::new()),
                workflow_skill_paths: Some(Vec::new()),
                ..DelegationPreferencesResultMessage::new()
            })
        });
    });
}

/// Phones on, with the profile's Pair a phone.
fn settings_phones(dark: bool) {
    settings_section(
        dark,
        "phones",
        "phones",
        &[("phone_access_enabled", "true")],
        |daemon| {
            daemon.on("phones_get", |_: &Request| {
                message(PhonesUpdatedMessage {
                    link: PhoneLink {
                        state: "connected".into(),
                        ..PhoneLink::default()
                    },
                    ..PhonesUpdatedMessage::new()
                })
            });
        },
    );
}

/// A release app with a newer release out, and its Update attn.
fn settings_updates(dark: bool) {
    settings_section(
        dark,
        "updates",
        "updates",
        &[
            ("self_update_enabled", "true"),
            ("update_install_kind", "release_app"),
            ("update_current_version", "0.13.0"),
            ("update_current_commit", "abcdef1234567"),
            ("update_available_version", "0.14.0"),
        ],
        |_| {},
    );
}

/// The daemon's refusal of every setting write, as a long one echoing a path it could not make.
const LONG_REFUSAL: &str = "cannot create directory /Users/someone/プロジェクト/社内プラットフォーム/InternationalEngineeringObservability/ワークツリー: permission denied";

fn refusing(daemon: &Daemon) {
    daemon.on("set_setting", |r: &Request| {
        message(attn_protocol::SettingsUpdatedMessage {
            request_id: r["request_id"].as_str().map(str::to_owned),
            changed_key: r["key"].as_str().map(str::to_owned),
            success: Some(false),
            error: Some(LONG_REFUSAL.into()),
            ..attn_protocol::SettingsUpdatedMessage::new()
        })
    });
}

/// `setting`'s field on a Settings page, typed into and refused, in the window as it is and in
/// the smallest window at 150% text.
fn settings_refused(dark: bool, section: &str, search: &str, setting: &str, typed: &str) {
    for smallest in [false, true] {
        let scale: &[(&str, &str)] = if smallest { &[("uiScale", "1.5")] } else { &[] };
        let mut world = settings_world(dark, section, search, scale, |_| {}, refusing);
        if smallest {
            world.resize((800.0, 600.0));
        }
        let field = format!("settings.input.{setting}.field");
        world.gesture(|app| {
            app.update(|_, tx| tx.scroll_center_in_instant("settings.body", &field))
        });
        world.gesture(|app| app.click(Target::key(field.clone())));
        world.gesture(|app| app.type_text(typed));
        tap(&mut world, Key::Enter);
        world.gesture(|app| {
            app.update(|_, tx| tx.scroll_center_in_instant("settings.body", &field))
        });
        let name = if smallest {
            "settings-smallest"
        } else {
            "settings"
        };
        snap(&mut world, &format!("{name}-{section}-refused"), dark);
    }
}

/// The structural states of the Settings pages that have them: a refused field, a list with
/// its rows, a list with none, a refused action, and each long page's end.
fn settings_page_states(dark: bool) {
    settings_refused(
        dark,
        "desktop",
        "files",
        "projects_directory",
        "/Users/someone/プロジェクト",
    );
    settings_refused(
        dark,
        "hygiene",
        "attention queue",
        "auto_settle_arm_seconds",
        "9999",
    );
    settings_bottom(dark, "desktop", "files", &[]);
    settings_refused(
        dark,
        "backgroundAgents",
        "background agents",
        "activity.intervals#watching",
        "45",
    );
    settings_bottom(dark, "backgroundAgents", "background agents", &[]);
    let mut world = settings_world(
        dark,
        "hygiene",
        "attention queue",
        &[],
        |state| {
            state.repos = Some(
                [
                    "acme/api",
                    "acme/a-repository-with-a-very-long-name-for-its-organisation",
                ]
                .into_iter()
                .map(|repo| attn_protocol::RepoState {
                    repo: repo.into(),
                    muted: true,
                    collapsed: false,
                })
                .collect(),
            );
            state.authors = Some(vec![attn_protocol::AuthorState {
                author: "dependabot".into(),
                muted: true,
            }]);
        },
        |_| {},
    );
    world.gesture(|app| app.update(|_, tx| tx.scroll_to_bottom_instant("settings.body")));
    snap(&mut world, "settings-hygiene-muted", dark);
    settings_bottom(dark, "hygiene", "attention queue", &[]);
    let mut world = settings_world(
        dark,
        "updates",
        "updates",
        &[
            ("self_update_enabled", "true"),
            ("update_install_kind", "release_app"),
            ("update_current_version", "0.13.0"),
            ("update_current_commit", "abcdef1234567"),
            (
                "update_check_error",
                "the release feed at https://example.com/releases/latest.json did not answer",
            ),
        ],
        |_| {},
        |daemon| {
            daemon.on("start_update", |_| {
                message(attn_protocol::StartUpdateResultMessage {
                    success: false,
                    error: Some("the build failed: see /tmp/run-1.log".into()),
                    ..attn_protocol::StartUpdateResultMessage::new()
                })
            });
        },
    );
    world.gesture(|app| app.click(Target::key("settings.updates.start")));
    snap(&mut world, "settings-updates-refused", dark);
    settings_section(
        dark,
        "updates",
        "updates",
        &[
            ("self_update_enabled", "true"),
            ("update_install_kind", "source"),
        ],
        |_| {},
    );
    let mut world = settings_world(
        dark,
        "connectivity",
        "github",
        &[],
        |state| state.github_hosts = Some(Vec::new()),
        |_| {},
    );
    snap(&mut world, "settings-connectivity-empty", dark);
}

/// The event bus with one live consumer, its Disable, and the footer's Refresh.
fn settings_event_bus(dark: bool) {
    settings_section(dark, "eventBus", "event bus", &[], |daemon| {
        daemon.on("bus_status_get", |r: &Request| {
            message(BusStatusResultMessage {
                request_id: r["request_id"].as_str().unwrap_or_default().into(),
                success: true,
                earliest: 1,
                head: 315_188,
                rows: 315_188,
                bytes: 23_815_114,
                oldest_at: "2026-08-02T10:24:55Z".into(),
                newest_at: "2026-08-10T19:28:01Z".into(),
                delivering: true,
                retention_seconds: 30.0 * 86400.0,
                recent_window_seconds: 3600.0,
                baseline_window_seconds: 86400.0,
                surge_rate_per_hour: 1000.0,
                pin_alarm_seconds: 3600.0,
                consumers: vec![BusConsumerStatus {
                    name: "projections".into(),
                    cursor: 315_188,
                    filter: "session.*".into(),
                    enabled: true,
                    updated_at: "2026-08-10T19:00:00Z".into(),
                    live: true,
                    ..BusConsumerStatus::default()
                }],
                ..BusStatusResultMessage::new()
            })
        });
    });
}

/// The ledger with nothing in it: its header's Close and its status bar's keys.
fn ledger(dark: bool) {
    let daemon = scripted().state(initial(dark, document_tile("m", "/repo/plan.md"), vec![]));
    daemon.on("session_list", |r: &Request| {
        message(SessionListResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            result: Some(SessionListResult::default()),
            ..SessionListResultMessage::new()
        })
    });
    let mut world = start(daemon);
    press(&mut world, Action::SessionsOpen);
    snap(&mut world, "ledger", dark);
}

/// The ledger with a working, a waiting, an idle and a closed agent, the waiting one selected:
/// its inspector names the seed it tends, its usage, its conversation and Focus.
fn ledger_seeded(dark: bool) {
    let tended = Seed {
        claimed: true,
        tender: Some(PartyView {
            name: "reviewer".into(),
            r#ref: "session:s1".into(),
            session_id: Some("s1".into()),
        }),
        ..seed("s-one", "Colors in one place", "growing", None)
    };
    let daemon = scripted().state(initial(
        dark,
        document_tile("m", "/repo/plan.md"),
        vec![tended],
    ));
    let entry = |id: &str, label: &str, state: &str, seen: &str| SessionLedgerEntry {
        id: id.into(),
        label: label.into(),
        agent: "claude".into(),
        directory: format!("/home/me/projects/{label}"),
        profile_id: PROFILE.into(),
        profile_name: "Work".into(),
        state: state.into(),
        last_seen: seen.into(),
        ..SessionLedgerEntry::default()
    };
    let entries = vec![
        entry("s0", "builder", "working", "2026-11-17T09:00:00Z"),
        SessionLedgerEntry {
            usage: Some(SessionUsage {
                total_tokens: 6_483,
                models: vec![model_usage(
                    "agent",
                    SessionUsageModel {
                        input_tokens: 6_000,
                        output_tokens: 483,
                        total_tokens: 6_483,
                        ..SessionUsageModel::default()
                    },
                )],
                ..SessionUsage::default()
            }),
            ..entry("s1", "reviewer", "waiting_input", "2026-11-17T08:58:00Z")
        },
        entry("s4", "docs", "idle", "2026-11-17T08:40:00Z"),
        SessionLedgerEntry {
            closed_at: Some("2026-11-17T07:00:00Z".into()),
            closed_by: Some("user".into()),
            ..entry("gone", "old run", "idle", "2026-11-17T07:00:00Z")
        },
    ];
    daemon.on("session_list", move |r: &Request| {
        message(SessionListResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            result: Some(SessionListResult {
                entries: entries.clone(),
                ..SessionListResult::default()
            }),
            ..SessionListResultMessage::new()
        })
    });
    let mut world = start(daemon);
    press(&mut world, Action::SessionsOpen);
    world.gesture(|app| app.click(Target::key("ledger.row.s1")));
    snap(&mut world, "ledger-seeded", dark);
}

/// The ledger's Worktrees list with no worktrees: its scopes, its filter and its refresh.
fn ledger_worktrees(dark: bool) {
    let daemon = scripted().state(initial(dark, document_tile("m", "/repo/plan.md"), vec![]));
    daemon.on("session_list", |r: &Request| {
        message(SessionListResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            result: Some(SessionListResult::default()),
            ..SessionListResultMessage::new()
        })
    });
    daemon.on("worktree_list", |r: &Request| {
        message(WorktreeListResultEvent {
            request_id: r["request_id"].as_str().map(str::to_owned),
            success: true,
            worktree_list_result: WorktreeListResult::default(),
            ..WorktreeListResultEvent::new()
        })
    });
    let mut world = start(daemon);
    press(&mut world, Action::SessionsOpen);
    world.gesture(|app| app.click(Target::key("ledger.tab.worktrees")));
    snap(&mut world, "ledger-worktrees", dark);
}

/// The Chief's setup, opened from its row while it has no harness yet: Claude Code and Codex
/// to choose from, Claude Code's models with the deep one recommended, its efforts and the
/// Notebook folder.
fn chief_setup(dark: bool) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    state.crew = Some(vec![CrewMember {
        id: "chief-key".into(),
        name: "Captain".into(),
        chief: Some(true),
        profile_id: PROFILE.into(),
        revision: 1,
        resolved_agent: "claude".into(),
        ..CrewMember::default()
    }]);
    if let Some(settings) = state.settings.as_mut() {
        for (key, value) in [
            ("claude_available", "true"),
            ("codex_available", "true"),
            ("claude_cap_launch_instructions", "true"),
            ("codex_cap_launch_instructions", "true"),
            ("new_session_agent", "claude"),
            ("queue_mode_enabled", "true"),
            ("notebook.root.effective", "/home/me/attn-notebook"),
        ] {
            settings.insert(key.into(), value.into());
        }
    }
    let daemon = scripted().state(state);
    daemon.on("delegation_preferences_get", |r: &Request| {
        let harness = |id: &str, name: &str| Harness {
            id: id.into(),
            name: name.into(),
            available: true,
            model_pin: true,
            effort_pin: true,
            discovery: true,
        };
        message(DelegationPreferencesResultMessage {
            request_id: r["request_id"].as_str().unwrap_or_default().into(),
            success: true,
            preferences: Some(DelegationPreferences::default()),
            harnesses: Some(vec![
                harness("claude", "Claude Code"),
                harness("codex", "Codex"),
            ]),
            templates: Some(Vec::new()),
            expanded_roles: Some(Vec::new()),
            ..DelegationPreferencesResultMessage::new()
        })
    });
    daemon.on("harness_models", |r: &Request| {
        let model = |id: &str, name: &str, tier: &str| HarnessModel {
            harness: "claude".into(),
            id: id.into(),
            name: name.into(),
            access: "unknown".into(),
            effort_support: "supported".into(),
            effort_levels: ["low", "medium", "high"].map(String::from).to_vec(),
            tier: Some(tier.into()),
            shipped_tier: Some(tier.into()),
            tier_source: "shipped".into(),
            ..HarnessModel::default()
        };
        message(HarnessModelsResultMessage {
            request_id: r["request_id"].as_str().unwrap_or_default().into(),
            success: true,
            models: vec![
                model("opus", "Opus 5.5", "deep"),
                model("sonnet", "Sonnet 5.5", "standard"),
            ],
            tier_defaults: attn_protocol::TierDefaults {
                deep: Some("opus".into()),
                standard: Some("sonnet".into()),
                light: None,
            },
            ..HarnessModelsResultMessage::new()
        })
    });
    let mut world = World::start_sized(daemon, window());
    world.settle();
    world.gesture(|app| app.click(Target::key("chief-slot")));
    snap(&mut world, "chief-setup", dark);
}

/// The asks drawer showing one ask from the waiting agent, with its footer's keys and Send.
fn asks(dark: bool) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    state.asks = Some(vec![Ask {
        id: "a-cache".into(),
        session_id: "s1".into(),
        subject: "Cache store".into(),
        base_dir: "/repo".into(),
        created_at: "2026-11-17T09:00:00Z".into(),
        parts: vec![
            AskPart {
                markdown: Some("Deploys empty the cache.".into()),
                ..AskPart::default()
            },
            AskPart {
                question: Some(AskQuestion {
                    key: "q1".into(),
                    kind: "text".into(),
                    prompt: "What should the banner say?".into(),
                    ..AskQuestion::default()
                }),
                ..AskPart::default()
            },
        ],
        ..Ask::default()
    }]);
    let mut world = start(scripted().state(state));
    press(&mut world, Action::DockAsks);
    snap(&mut world, "asks", dark);
}

/// The asks drawer with nothing open, under the asks setting.
fn asks_empty(dark: bool) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    state
        .settings
        .get_or_insert_with(Default::default)
        .insert("asks_enabled".into(), "true".into());
    let mut world = start(scripted().state(state));
    press(&mut world, Action::DockAsks);
    snap(&mut world, "asks-empty", dark);
}

/// The asks drawer on an ask with a question of choices: its context, a recommended choice
/// with a description, Other and the note.
fn asks_choices(dark: bool) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    let choice = |label: &str, description: &str| AskChoice {
        label: label.into(),
        description: Some(description.into()),
    };
    state.asks = Some(vec![Ask {
        id: "a-cache".into(),
        session_id: "s1".into(),
        subject: "Cache store".into(),
        base_dir: "/repo".into(),
        created_at: "2026-11-17T09:00:00Z".into(),
        parts: vec![
            AskPart {
                markdown: Some(
                    "Deploys empty the cache, so the first requests after one are slow.".into(),
                ),
                ..AskPart::default()
            },
            AskPart {
                question: Some(AskQuestion {
                    key: "store".into(),
                    kind: "one".into(),
                    prompt: "Which store should keep the cache?".into(),
                    context: Some(
                        "Postgres runs already; Redis is faster but adds a service to operate."
                            .into(),
                    ),
                    choices: vec![
                        choice("Postgres", "already deployed"),
                        choice("Redis", "faster, one more service"),
                    ],
                    recommended: Some("Postgres".into()),
                }),
                ..AskPart::default()
            },
        ],
        ..Ask::default()
    }]);
    let mut world = start(scripted().state(state));
    press(&mut world, Action::DockAsks);
    snap(&mut world, "asks-choices", dark);
}

fn notebook(dark: bool) {
    let mut world = with_document(dark);
    world
        .automate("run", json!({ "name": "notebook.openFullscreen" }))
        .unwrap();
    world.settle();
    snap(&mut world, "notebook", dark);
}

/// The fullscreen notebook over a folder of two folders and a note, one folder opened.
fn notebook_tree(dark: bool) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    if let Some(settings) = state.settings.as_mut() {
        settings.insert(
            "notebook.root.effective".to_owned(),
            "/home/me/notebook".to_owned(),
        );
    }
    let daemon = scripted().state(state);
    daemon.on("fs_list", |req| {
        let entry = |path: &str, is_dir: bool| {
            let name = path.rsplit('/').next().unwrap_or(path);
            json!({ "path": path, "name": name, "is_dir": is_dir, "size": 1 })
        };
        let entries = match req["path"].as_str().unwrap_or("") {
            "" => vec![
                entry("journal", true),
                entry("knowledge", true),
                entry("todo.md", false),
            ],
            "knowledge" => vec![entry("knowledge/index.md", false)],
            _ => vec![],
        };
        json!({ "event": "fs_list_result", "success": true, "entries": entries })
    });
    let mut world = start(daemon);
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    world
        .automate("run", json!({ "name": "notebook.openFullscreen" }))
        .unwrap();
    world.settle();
    world.gesture(|app| app.click(Target::key("notebook.file.knowledge")));
    snap(&mut world, "notebook-tree", dark);
}

/// The review's file view, opened from the brief's stop: its back bar and its keys.
fn review_file(dark: bool) {
    let daemon = scripted().state(initial(dark, document_tile("m", "/repo/review.md"), vec![]));
    serve_totals(&daemon);
    let mut world = start(daemon);
    deliver(&mut world, "/repo/review.md", REVIEW, Some(review()));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    tap(&mut world, Key::Right);
    tap(&mut world, Key::Enter);
    snap(&mut world, "review-file", dark);
}

fn review_keys(dark: bool) {
    let daemon = scripted().state(initial(dark, document_tile("m", "/repo/review.md"), vec![]));
    let mut world = start(daemon);
    deliver(&mut world, "/repo/review.md", REVIEW, Some(review()));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    world.gesture(|app| app.type_text("?"));
    snap(&mut world, "review-keys", dark);
}

/// The review shown alone on its desktop, bound to agent a's session, beside a second open
/// document, as the Tauri audit shot it: its tabs over the tile, its header and its brief.
fn review_alone(dark: bool) -> World {
    let mut bound = document_tile("m", "/repo/review.md");
    bound["tile_session_id"] = json!("s0");
    let tiles = row(vec![bound, document_tile("n", "/repo/notes.md")]);
    let mut state = initial(dark, tiles, vec![]);
    if let Some(desktop) = state.desktops.as_mut().and_then(|d| d.first_mut()) {
        desktop.active_pane_id = "m".into();
    }
    let daemon = scripted().state(state);
    serve_totals(&daemon);
    let mut world = start(daemon);
    deliver(&mut world, "/repo/review.md", REVIEW, Some(review()));
    deliver_to(&mut world, "n", "/repo/notes.md", "# Notes\n", None);
    world.gesture(|app| app.click(Target::key("tile.m.focus")));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    world
}

fn review_focused(dark: bool) {
    let mut world = review_alone(dark);
    snap(&mut world, "review-focused", dark);
}

fn review_walkthrough(dark: bool) {
    let mut world = review_alone(dark);
    world.gesture(|app| app.type_text("2"));
    snap(&mut world, "review-walkthrough", dark);
}

fn review_search(dark: bool) {
    let mut world = review_alone(dark);
    world.gesture(|app| app.type_text("/"));
    snap(&mut world, "review-search", dark);
}

fn review_send_sheet(dark: bool) {
    let mut world = review_alone(dark);
    press(&mut world, Action::MarkdownSendAnnotations);
    snap(&mut world, "review-send-sheet", dark);
}

fn launcher_empty_desktop(dark: bool) {
    let mut world = start(launcher_daemon(dark, true));
    snap(&mut world, "launcher-empty-desktop", dark);
}

/// A new scheduled automation's form, with nothing defined yet and the daemon previewing
/// every schedule it is asked about.
fn automation_form(dark: bool) -> World {
    let daemon = scripted().state(initial(dark, document_tile("m", "/repo/plan.md"), vec![]));
    daemon
        .on("automation_definitions_get", |_: &Request| {
            message(AutomationDefinitionsResultMessage {
                success: true,
                definitions: Some(Vec::new()),
                ..AutomationDefinitionsResultMessage::new()
            })
        })
        .on("automation_schedule_preview", |_: &Request| {
            message(AutomationSchedulePreviewResultMessage {
                success: true,
                times: Some(vec!["2030-06-03T09:00:00Z".into()]),
                ..AutomationSchedulePreviewResultMessage::new()
            })
        });
    let mut world = start(daemon);
    for key in [
        "titlebar.automations",
        "automations.new",
        "automations.form.trigger-scheduled",
    ] {
        world.gesture(|app| app.click(Target::key(key)));
    }
    world
}

/// A new scheduled automation's form at its top: its name, prompt and repository fields.
fn automation_new(dark: bool) {
    let mut world = automation_form(dark);
    snap(&mut world, "automation-new", dark);
}

/// The hour picker opened from the keyboard on its hour, with Up on the one before.
fn automation_hour(dark: bool) {
    let mut world = automation_form(dark);
    world.gesture(|app| app.click(Target::key("automations.form.schedule-days-hour-trigger")));
    world.gesture(|app| app.press("escape"));
    world.gesture(|app| app.press("down"));
    world.gesture(|app| app.press("up"));
    snap(&mut world, "automation-hour", dark);
}

/// The zones that match what is typed, with Down on the first.
fn automation_time_zones(dark: bool) {
    let mut world = automation_form(dark);
    world.gesture(|app| {
        app.click(Target::key("automations.form.time-zone"));
        app.press("mod+a");
        app.type_text("america/n");
        app.press("down");
    });
    snap(&mut world, "automation-time-zones", dark);
}

/// Two automations, a manual one and a scheduled one on a desktop of its own, the scheduled
/// one with a run that failed and one delivered to its agent.
fn automations_daemon(dark: bool, definitions: Vec<AutomationDefinitionSummary>) -> Daemon {
    let daemon = scripted().state(initial(dark, document_tile("m", "/repo/plan.md"), vec![]));
    daemon
        .on("automation_definitions_get", move |_: &Request| {
            message(AutomationDefinitionsResultMessage {
                success: true,
                definitions: Some(definitions.clone()),
                ..AutomationDefinitionsResultMessage::new()
            })
        })
        .on("automation_runs_get", |r: &Request| {
            let run = |id: &str, state: &str, created: &str, delivered: Option<&str>| {
                AutomationRunSummary {
                    id: id.into(),
                    definition_id: 2,
                    state: state.into(),
                    created_at: created.into(),
                    updated_at: created.into(),
                    delivered_at: delivered.map(Into::into),
                    occurrence_key: Some(format!("scheduled:{created}")),
                    session_id: Some("s0".into()),
                    last_error: (state == "failed")
                        .then(|| "the agent exited before it answered".into()),
                    ..AutomationRunSummary::default()
                }
            };
            message(AutomationRunsResultMessage {
                success: true,
                definition_id: r["definition_id"].as_i64().unwrap_or_default(),
                runs: Some(vec![
                    run(
                        "r1",
                        "delivered",
                        "2030-06-03T07:00:00Z",
                        Some("2030-06-03T07:00:04Z"),
                    ),
                    run("r2", "failed", "2030-06-02T07:00:00Z", None),
                ]),
                ..AutomationRunsResultMessage::new()
            })
        })
        .on("automation_definition_get", |_: &Request| {
            message(AutomationDefinitionResultMessage {
                success: true,
                spec_json: Some(
                    json!({
                        "api_version": "attn.dev/automations/v1alpha1",
                        "id": 2,
                        "name": "Morning digest",
                        "trigger": {"type": "scheduled", "schedule": {"cron": "0 9 * * 1-5", "time_zone": "UTC"}, "continuity": "fresh", "catch_up": "latest"},
                        "prompt": "Summarise what changed overnight.",
                        "launch": {"driver": "codex", "model": "gpt-5.6-luna", "effort": "medium"},
                        "location": {"type": "directory", "path": "/repo"},
                    })
                    .to_string(),
                ),
                spec_yaml: Some(String::new()),
                definition: Some(scheduled_automation()),
                ..AutomationDefinitionResultMessage::new()
            })
        })
        .on("automation_schedule_preview", |_: &Request| {
            message(AutomationSchedulePreviewResultMessage {
                success: true,
                times: Some(vec![
                    "2030-06-03T09:00:00Z".into(),
                    "2030-06-04T09:00:00Z".into(),
                    "2030-06-05T09:00:00Z".into(),
                ]),
                ..AutomationSchedulePreviewResultMessage::new()
            })
        });
    daemon
}

fn scheduled_automation() -> AutomationDefinitionSummary {
    AutomationDefinitionSummary {
        id: 2,
        profile_id: PROFILE.into(),
        name: "Morning digest".into(),
        enabled: true,
        revision: 1,
        trigger_type: "scheduled".into(),
        schedule_cron: Some("0 9 * * 1-5".into()),
        schedule_time_zone: Some("UTC".into()),
        launch_desktop: Some(LaunchDesktopSetting {
            label: Some("Morning digest (new)".into()),
            ..LaunchDesktopSetting::default()
        }),
        updated_at: "2030-06-01T07:00:00Z".into(),
        ..AutomationDefinitionSummary::default()
    }
}

fn automations() -> Vec<AutomationDefinitionSummary> {
    vec![
        AutomationDefinitionSummary {
            id: 1,
            profile_id: PROFILE.into(),
            name: "Review the queue".into(),
            enabled: true,
            revision: 1,
            trigger_type: "manual".into(),
            launch_desktop: Some(LaunchDesktopSetting {
                label: Some("Review the queue (new)".into()),
                ..LaunchDesktopSetting::default()
            }),
            updated_at: "2030-06-01T07:00:00Z".into(),
            ..AutomationDefinitionSummary::default()
        },
        scheduled_automation(),
    ]
}

fn automations_open(dark: bool, definitions: Vec<AutomationDefinitionSummary>) -> World {
    let mut world = start(automations_daemon(dark, definitions));
    world.gesture(|app| app.click(Target::key("titlebar.automations")));
    world
}

/// The drawer with no automations defined.
fn automations_empty(dark: bool) {
    let mut world = automations_open(dark, Vec::new());
    snap(&mut world, "automations-empty", dark);
}

/// The drawer listing a manual and a scheduled automation.
fn automations_listed(dark: bool) {
    let mut world = automations_open(dark, automations());
    snap(&mut world, "automations-listed", dark);
}

/// The scheduled automation selected, with its runs under the list.
fn automations_selected(dark: bool) {
    let mut world = automations_open(dark, automations());
    world.gesture(|app| app.click(Target::key("automations.select.2")));
    snap(&mut world, "automations-selected", dark);
}

/// The scheduled automation's form, read from the daemon.
fn automation_edit(dark: bool) {
    let mut world = automations_open(dark, automations());
    world.gesture(|app| app.click(Target::key("automations.edit.2")));
    snap(&mut world, "automation-edit", dark);
}

/// A new automation's form on a pull request trigger.
fn automation_github(dark: bool) {
    let mut world = automations_open(dark, Vec::new());
    for key in ["automations.new", "automations.form.trigger-github"] {
        world.gesture(|app| app.click(Target::key(key)));
    }
    snap(&mut world, "automation-github", dark);
}

/// A new manual automation's form with Advanced open.
fn automation_advanced(dark: bool) {
    let mut world = automations_open(dark, Vec::new());
    for key in ["automations.new", "automations.form.advanced"] {
        world.gesture(|app| app.click(Target::key(key)));
    }
    snap(&mut world, "automation-advanced", dark);
}

/// The Chief before its setup, and a member between sessions.
fn crew() -> Vec<CrewMember> {
    let member = |id: &str, name: &str, chief: bool| CrewMember {
        id: id.into(),
        name: name.into(),
        chief: Some(chief),
        profile_id: PROFILE.into(),
        revision: 1,
        resolved_agent: "claude".into(),
        ..CrewMember::default()
    };
    vec![
        member("chief", "Chief", true),
        member("scout", "scout", false),
    ]
}

fn with_crew(dark: bool) -> World {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    state.crew = Some(crew());
    let mut world = start(scripted().state(state));
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    world
}

/// Home, with the crew in the sidebar.
fn home(dark: bool) {
    let mut world = with_crew(dark);
    press(&mut world, Action::SessionGoToDashboard);
    snap(&mut world, "home", dark);
}

/// Home with no agents and GitHub polling off: both cards empty, the reason wrapping in its own.
fn home_empty(dark: bool) {
    let state = InitialStateMessage {
        sessions: Some(vec![]),
        desktops: Some(vec![]),
        github_polling_off_reason: Some(
            "GitHub polling is off for instance tour. Start its daemon with \
             ATTN_GITHUB_POLLING=on to poll with your gh credentials."
                .into(),
        ),
        ..initial(dark, pane("b"), vec![])
    };
    let mut world = World::start_sized(scripted().state(state), window());
    press(&mut world, Action::SessionGoToDashboard);
    snap(&mut world, "home-empty", dark);
}

/// Manage crew with the Chief and scout, open on `member` at `tab`: a daemon that lists Claude
/// Code and Codex, and holds an empty charter and no handoffs.
fn manage_crew(dark: bool, member: &str, tab: &str, name: &str) {
    let mut world = crew_page(dark);
    world.gesture(|app| app.click(Target::key(format!("crew.roster.{member}"))));
    world.gesture(|app| app.click(Target::key(format!("crew.tab.{tab}"))));
    snap(&mut world, name, dark);
}

/// Manage crew open, over a daemon that also lists the desktops a member can launch on.
fn crew_page(dark: bool) -> World {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    state.crew = Some(
        crew()
            .into_iter()
            .map(|m| CrewMember {
                resolved_model: Some("fable".into()),
                ..m
            })
            .collect(),
    );
    if let Some(settings) = state.settings.as_mut() {
        for (key, value) in [
            ("claude_available", "true"),
            ("codex_available", "true"),
            ("default_model_claude", "fable"),
        ] {
            settings.insert(key.into(), value.into());
        }
    }
    let daemon = scripted().state(state);
    daemon
        .on("delegation_preferences_get", |_: &Request| {
            message(DelegationPreferencesResultMessage {
                success: true,
                preferences: Some(DelegationPreferences::default()),
                harnesses: Some(
                    [("claude", "Claude Code"), ("codex", "Codex")]
                        .iter()
                        .map(|(id, name)| Harness {
                            id: (*id).into(),
                            name: (*name).into(),
                            available: true,
                            model_pin: true,
                            effort_pin: true,
                            ..Harness::default()
                        })
                        .collect(),
                ),
                ..DelegationPreferencesResultMessage::new()
            })
        })
        .on("crew_charter_get", |r: &Request| {
            message(CrewCharterGetResultMessage {
                success: true,
                member: r["member"].as_str().map(Into::into),
                charter: Some(CrewCharterDocument {
                    content: String::new(),
                    token: "t1".into(),
                }),
                ..CrewCharterGetResultMessage::new()
            })
        })
        .on("crew_handoffs_get", |r: &Request| {
            message(CrewHandoffsGetResultMessage {
                success: true,
                member: r["member"].as_str().map(Into::into),
                ..CrewHandoffsGetResultMessage::new()
            })
        })
        .on("launch_desktop_get", |r: &Request| {
            message(LaunchDesktopResultMessage {
                request_id: request_id(r),
                action: "launch_desktop_get".into(),
                success: true,
                desktops: Some(vec![Desktop {
                    id: DESKTOP.into(),
                    profile_id: PROFILE.into(),
                    name: "Work".into(),
                    shortcut_slot: Some(1),
                    ..Desktop::default()
                }]),
                ..LaunchDesktopResultMessage::new()
            })
        });
    let mut world = start(daemon);
    run(&mut world, Action::OpenCrew);
    world
}

/// Naming the new desktop the Chief launches on, from its Desktop list.
fn desktop_naming(dark: bool) {
    let mut world = crew_page(dark);
    world.gesture(|app| app.click(Target::key("crew.tab.launch")));
    world.gesture(|app| app.click(Target::key("crew.launch.chief.desktop-trigger")));
    world
        .automate(
            "press",
            json!({"target": {"role": "list-box-option", "name": "A new desktop…"}}),
        )
        .expect("the Desktop list offers a new desktop");
    snap(&mut world, "desktop-naming", dark);
}

/// The palette offering to set up the Chief.
fn palette_chief(dark: bool) {
    let mut world = with_crew(dark);
    press(&mut world, Action::UiCommandPalette);
    snap(&mut world, "palette-chief", dark);
}

fn notifications(dark: bool) {
    let mut world = with_document(dark);
    world.gesture(|app| app.click(Target::key("titlebar.notifications")));
    snap(&mut world, "notifications", dark);
}

fn session_menu(dark: bool) {
    let mut world = with_document(dark);
    world.gesture(|app| app.click(Target::key("sidebar.session.s0.actions")));
    snap(&mut world, "session-menu", dark);
}

fn queue_flow(dark: bool) -> World {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    state.crew = Some(crew());
    if let Some(settings) = state.settings.as_mut() {
        settings.insert("queue_mode_enabled".to_owned(), "true".to_owned());
    }
    let mut world = World::start_sized(scripted().state(state), window());
    press(&mut world, Action::SessionGoToDashboard);
    world
}

/// The queue flow's Home with the crew.
fn queue_home(dark: bool) {
    let mut world = queue_flow(dark);
    snap(&mut world, "queue-home", dark);
}

/// The queue flow's agent list, opened.
fn queue_agent_list(dark: bool) {
    let mut world = queue_flow(dark);
    world.gesture(|app| app.click(Target::key("queue.agents-toggle")));
    snap(&mut world, "queue-agent-list", dark);
}

/// The queue flow with turns owed, as the audit seeded it: two agents waiting, the first with
/// an open ask, the first three desktops on shortcut slots, and the crew.
fn queue_seeded(dark: bool, home: bool, sidebar_hidden: bool, slots: usize) -> World {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    state.crew = Some(crew());
    if let Some(settings) = state.settings.as_mut() {
        settings.insert("queue_mode_enabled".to_owned(), "true".to_owned());
    }
    let desktops = state.desktops.get_or_insert_default();
    for n in desktops.len()..slots {
        let id = format!("work/desktop_{}", n + 1);
        let mut empty = desktop(&id, &format!("9{n}"), Value::Null, vec![]);
        empty.tree_json = String::new();
        empty.name = format!("Desktop {}", n + 1);
        desktops.push(empty);
    }
    for (n, desktop) in desktops.iter_mut().enumerate() {
        desktop.shortcut_slot = (n < slots).then_some(n as i64 + 1);
    }
    for (session, opened) in state
        .sessions
        .iter_mut()
        .flatten()
        .zip(["2026-11-12T08:55:00Z", "2026-11-12T07:00:00Z"])
    {
        session.state = "waiting_input".into();
        session.turn_owed = Some(true);
        session.turn_opened_at = Some(opened.into());
    }
    state.asks = Some(vec![Ask {
        id: "a-cache".into(),
        session_id: "s0".into(),
        subject: "Cache store".into(),
        base_dir: "/repo".into(),
        created_at: "2026-11-17T09:00:00Z".into(),
        ..Ask::default()
    }]);
    let mut world = World::start_sized(scripted().state(state), window());
    press(&mut world, Action::SessionGoToDashboard);
    if !home {
        world.gesture(|app| app.click(Target::key("queue.desktop.1")));
        world.gesture(|app| app.move_to((1100.0, 700.0)));
    }
    if sidebar_hidden {
        world.gesture(|app| app.click(Target::key("titlebar.sidebar")));
    }
    world
}

/// The seeded queue at Home: the waiting card, the ask on a row, the desktop strip.
fn queue_seeded_home(dark: bool) {
    let mut world = queue_seeded(dark, true, false, 3);
    snap(&mut world, "queue-seeded-home", dark);
}

/// The queue sidebar with the Chief between sessions and two automation runs that stopped
/// with a question, their group closed, or open with the pointer on a run and its settle button.
fn queue_runs(dark: bool, open: bool, name: &str) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    let mut members = crew();
    members[0].agent = Some("claude".into());
    members[0].model = Some("opus".into());
    state.crew = Some(members);
    if let Some(settings) = state.settings.as_mut() {
        settings.insert("queue_mode_enabled".to_owned(), "true".to_owned());
    }
    for (session, (run_id, opened)) in state.sessions.iter_mut().flatten().zip([
        ("r1", "2026-11-12T08:55:00Z"),
        ("r2", "2026-11-12T07:00:00Z"),
    ]) {
        session.state = "waiting_input".into();
        session.turn_owed = Some(true);
        session.turn_opened_at = Some(opened.into());
        session.automation = Some(AutomationProvenance {
            definition_id: 1,
            definition_name: "Morning digest".into(),
            run_id: run_id.into(),
            trigger_type: "scheduled".into(),
            ..AutomationProvenance::default()
        });
    }
    let mut world = World::start_sized(scripted().state(state), window());
    press(&mut world, Action::SessionGoToDashboard);
    if open {
        world.gesture(|app| app.click(Target::key("queue.runs.group.1")));
        world.gesture(|app| {
            let run = app.runtime.find("queue-run.s1").expect("the run is listed");
            app.move_to(run);
        });
    }
    snap(&mut world, name, dark);
}

/// The seeded queue with the asking agent's desktop open: its row in view, its desktop current.
fn queue_seeded_agent(dark: bool) {
    let mut world = queue_seeded(dark, false, false, 3);
    snap(&mut world, "queue-seeded-agent", dark);
}

/// The seeded queue with the sidebar collapsed: the queue bar in the title bar.
fn queue_bar(dark: bool) {
    let mut world = queue_seeded(dark, false, true, 3);
    snap(&mut world, "queue-bar", dark);
}

/// The queue bar in the narrowest window, too narrow for a chip per desktop: the window's
/// desktop's chip and the rest gathered into the extras chip.
fn queue_bar_crowded(dark: bool) {
    let mut world = queue_seeded(dark, false, true, 11);
    world.resize((800.0, window().1));
    assert!(
        world
            .screen()
            .find(Target::key("queue-bar.desktop.2"))
            .is_none(),
        "the bar crowds"
    );
    snap(&mut world, "queue-bar-crowded", dark);
}

/// The desktop flow with the sidebar collapsed to its rail.
fn rail(dark: bool) {
    let mut world =
        start(scripted().state(initial(dark, document_tile("m", "/repo/plan.md"), vec![])));
    world.gesture(|app| app.click(Target::key("titlebar.sidebar")));
    snap(&mut world, "rail", dark);
}

/// The working agent tends two seeds of a plot: its header's chip, then the list it pins.
fn session_seeds(dark: bool) {
    let tended = |seed: Seed| Seed {
        claimed: true,
        tender: Some(PartyView {
            name: "Builder".into(),
            r#ref: "session:s0".into(),
            session_id: Some("s0".into()),
        }),
        ..seed
    };
    let seeds = seeds()
        .into_iter()
        .map(|seed| match seed.id.as_str() {
            "s-one" | "s-two" => tended(seed),
            _ => seed,
        })
        .collect();
    let mut world =
        start(scripted().state(initial(dark, document_tile("m", "/repo/plan.md"), seeds)));
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    world.gesture(|app| app.click(Target::key("tile.a.seed")));
    snap(&mut world, "session-seeds", dark);
}

/// A word of the document selected, with the annotation toolbar over it.
/// The tour's world with desktop 1 holding document tile `m` alone, at `path`.
fn document_alone(dark: bool, path: &str) -> World {
    let mut state = initial(dark, document_tile("m", path), vec![]);
    if let Some(first) = state.desktops.as_mut().and_then(|d| d.first_mut()) {
        first.tree_json = document_tile("m", path).to_string();
        first.panes.clear();
        first.active_pane_id = "m".into();
    }
    start(scripted().state(state))
}

fn markdown_reader(dark: bool) {
    let mut world = document_alone(dark, "/repo/notes.md");
    deliver(&mut world, "/repo/notes.md", NOTES, None);
    snap(&mut world, "markdown-reader", dark);
}

fn markdown_missing(dark: bool) {
    let mut world = document_alone(dark, "/repo/notes.md");
    world.daemon.emit(DesktopTileContentMessage {
        desktop_id: DESKTOP.into(),
        tile_id: "m".into(),
        tile_kind: "markdown".into(),
        path: "/repo/notes.md".into(),
        error: Some("file not found: /repo/notes.md".into()),
        ..DesktopTileContentMessage::new()
    });
    world.settle();
    snap(&mut world, "markdown-missing", dark);
}

fn markdown_selection(dark: bool) {
    let mut world = with_document(dark);
    world.gesture(|app| {
        app.update(|_, tx| tx.select_text("tile.m.document.md/b1".to_owned(), 0..6))
    });
    world.settle();
    snap(&mut world, "markdown-selection", dark);
}

const RELEASE_NOTES: &str = "# Release notes

A short document that exercises every block the reader draws.

Colors move into one module, and every view reads them from there.
";

/// The release notes beside agent `a`, the tile active and bound to the agent, over a daemon that keeps the
/// document's marks as it is asked to.
fn annotated_document(dark: bool) -> World {
    let mut tile = document_tile("m", "/repo/release.md");
    tile["tile_session_id"] = json!("s0");
    let mut state = initial(dark, tile, vec![]);
    if let Some(desktops) = state.desktops.as_mut() {
        desktops[0].active_pane_id = "m".into();
    }
    let daemon = scripted().state(state);
    let kept = std::sync::Arc::new(std::sync::Mutex::new((Vec::<Value>::new(), 0_i64)));
    let draft = |(marks, generation): &(Vec<Value>, i64), r: &Request, event: &str| {
        json!({
            "event": event,
            "request_id": r["request_id"],
            "document_uri": r["document_uri"],
            "source_kind": r["source_kind"],
            "success": true,
            "annotations": marks,
            "generation": generation,
        })
    };
    let read = kept.clone();
    daemon.on("markdown_annotations_get", move |r: &Request| {
        draft(&read.lock().unwrap(), r, "markdown_annotations_get_result")
    });
    daemon.on("markdown_annotations_save", move |r: &Request| {
        let mut kept = kept.lock().unwrap();
        for upsert in r["upserts"].as_array().into_iter().flatten() {
            kept.0.retain(|m| m["id"] != upsert["id"]);
            kept.0.push(upsert.clone());
        }
        kept.1 += 1;
        draft(&kept, r, "markdown_annotations_save_result")
    });
    let mut world = start(daemon);
    deliver(&mut world, "/repo/release.md", RELEASE_NOTES, None);
    world
}

/// The release notes, their tile active, with their first paragraph's words "short document
/// that exercises every block" selected and let go, so the annotation toolbar shows over them.
fn annotation_selected(dark: bool) -> World {
    let mut world = annotated_document(dark);
    world.gesture(|app| {
        app.update(|_, tx| tx.select_text("tile.m.document.md/b1".to_owned(), 2..44))
    });
    world.settle();
    world
}

fn annotation_composing(dark: bool) -> World {
    let mut world = annotation_selected(dark);
    world.gesture(|app| app.click(Target::key("tile.m.document.toolbar.comment")));
    world.gesture(|app| app.type_text("Name the blocks it skips."));
    world.settle();
    world
}

fn annotation_saved(dark: bool) -> World {
    let mut world = annotation_composing(dark);
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Enter,
            mods: Mods {
                ctrl: true,
                ..Mods::default()
            },
        })
    });
    world.advance(std::time::Duration::from_secs(1));
    world.settle();
    world
}

fn annotation_selection(dark: bool) {
    let mut world = annotation_selected(dark);
    snap(&mut world, "annotation-selection", dark);
}

fn annotation_comment(dark: bool) {
    let mut world = annotation_composing(dark);
    snap(&mut world, "annotation-comment", dark);
}

fn annotation_saved_shot(dark: bool) {
    let mut world = annotation_saved(dark);
    snap(&mut world, "annotation-saved", dark);
}

fn annotation_notes(dark: bool) {
    let mut world = annotation_saved(dark);
    world.gesture(|app| app.click(Target::key("tile.m.document.notes")));
    world.settle();
    snap(&mut world, "annotation-notes-list", dark);
}

fn request_id(r: &Request) -> String {
    r["request_id"].as_str().unwrap_or_default().into()
}

/// The tour's daemon with `edit` made to its first state and `serve` adding to what it answers,
/// which always includes the palette's history.
fn popover_daemon(
    dark: bool,
    edit: impl FnOnce(&mut InitialStateMessage),
    serve: impl FnOnce(&Daemon),
) -> Daemon {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    edit(&mut state);
    let daemon = scripted().state(state);
    daemon
        .on("get_command_usage", |r: &Request| {
            message(GetCommandUsageResultMessage {
                request_id: request_id(r),
                profile_id: r["profile_id"].as_str().unwrap_or_default().into(),
                success: true,
                ..GetCommandUsageResultMessage::new()
            })
        })
        .on("record_command_usage", |r: &Request| {
            message(RecordCommandUsageResultMessage {
                request_id: request_id(r),
                success: true,
                ..RecordCommandUsageResultMessage::new()
            })
        });
    serve(&daemon);
    daemon
}

/// The tour's window on desktop 1 over [`popover_daemon`].
fn popover_world(
    dark: bool,
    edit: impl FnOnce(&mut InitialStateMessage),
    serve: impl FnOnce(&Daemon),
) -> World {
    let mut world = start(popover_daemon(dark, edit, serve));
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    world
}

fn plain(dark: bool) -> World {
    popover_world(dark, |_| {}, |_| {})
}

fn first_session(state: &mut InitialStateMessage) -> &mut Session {
    &mut state.sessions.as_mut().expect("the tour's sessions")[0]
}

/// The queue with two owed turns; the times the menu offers count from the world's fixed clock.
fn snooze_menu(dark: bool) {
    let daemon = popover_daemon(
        dark,
        |state| {
            if let Some(settings) = state.settings.as_mut() {
                settings.insert("queue_mode_enabled".into(), "true".into());
            }
            for (session, opened) in state
                .sessions
                .iter_mut()
                .flatten()
                .zip(["2026-11-12T08:55:00Z", "2026-11-12T07:00:00Z"])
            {
                session.state = "waiting_input".into();
                session.turn_owed = Some(true);
                session.turn_opened_at = Some(opened.into());
            }
        },
        |_| {},
    );
    let mut world = World::start_sized(daemon, window());
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    let name = "Snooze working agent";
    world.gesture(|app| app.click(Target::role("button").name(name)));
    snap(&mut world, "snooze-menu", dark);
}

fn rename_session(dark: bool) {
    let mut world = plain(dark);
    world.gesture(|app| app.click(Target::key("tile.a.rename")));
    snap(&mut world, "rename-session", dark);
}

fn model_usage(purpose: &str, counts: SessionUsageModel) -> SessionUsageModel {
    SessionUsageModel {
        model: "claude-opus-5".into(),
        purpose: purpose.into(),
        ..counts
    }
}

/// The agent's usage with a guardian beside it and a part that has no price.
fn usage() -> SessionUsage {
    SessionUsage {
        total_tokens: 186_197,
        cost_usd: Some(1.2345),
        has_unpriced_usage: true,
        models: vec![
            model_usage(
                "agent",
                SessionUsageModel {
                    input_tokens: 12_345,
                    output_tokens: 6_789,
                    cache_read_tokens: 150_000,
                    cache_write_5m_tokens: 10_000,
                    cache_write_1h_tokens: 5_000,
                    cache_write_unclassified_tokens: 187,
                    total_tokens: 184_321,
                    cost_usd: Some(1.234),
                    has_unpriced_usage: true,
                    unpriced_reason: Some("Cache write duration is unavailable.".into()),
                    ..SessionUsageModel::default()
                },
            ),
            model_usage(
                "guardian",
                SessionUsageModel {
                    input_tokens: 1_746,
                    output_tokens: 130,
                    total_tokens: 1_876,
                    cost_usd: Some(0.0005),
                    ..SessionUsageModel::default()
                },
            ),
        ],
        ..SessionUsage::default()
    }
}

fn session_usage(dark: bool) {
    let mut world = popover_world(
        dark,
        |state| first_session(state).usage = Some(usage()),
        |_| {},
    );
    world.gesture(|app| app.click(Target::key("tile.a.usage")));
    snap(&mut world, "session-usage", dark);
}

/// Terminal `a`'s agent tends one seed, whose latest note the popover reads.
fn session_seed_context(dark: bool) {
    let tended = Seed {
        status: "growing".into(),
        claimed: true,
        tender: Some(PartyView {
            name: "Builder".into(),
            r#ref: "session:s0".into(),
            session_id: Some("s0".into()),
        }),
        ..seed("s-one", "Colors in one place", "growing", None)
    };
    let known = tended.clone();
    let mut world = popover_world(
        dark,
        |state| {
            state.seeds_total = Some(1);
            state.seeds = Some(vec![tended]);
        },
        move |daemon| {
            daemon.on("seed_document_get", move |r: &Request| {
                message(SeedDocumentGetResultMessage {
                    request_id: request_id(r),
                    success: true,
                    document: Some(SeedDocument {
                        seed: known.clone(),
                        notes_total: 1,
                        notes: vec![SeedNote {
                            id: "n-1".into(),
                            kind: "note".into(),
                            body: "Every view now reads its colors from **one module** in attn-ui."
                                .into(),
                            created_at: "2026-11-12T08:30:00Z".into(),
                            author: ActorView {
                                name: "Builder".into(),
                                r#ref: "session:s0".into(),
                            },
                            ..SeedNote::default()
                        }],
                        ..SeedDocument::default()
                    }),
                    ..SeedDocumentGetResultMessage::new()
                })
            });
        },
    );
    press(&mut world, Action::UiCommandPalette);
    world.gesture(|app| app.type_text("Show working agent's seeds"));
    world.gesture(|app| app.press("enter"));
    snap(&mut world, "session-seed-context", dark);
}

fn notifications_empty(dark: bool) {
    let mut world = popover_world(
        dark,
        |_| {},
        |daemon| {
            daemon.on("notification_list", |r: &Request| {
                message(NotificationListResultMessage {
                    request_id: request_id(r),
                    success: true,
                    notifications: Some(Vec::new()),
                    ..NotificationListResultMessage::new()
                })
            });
        },
    );
    world.gesture(|app| app.click(Target::key("titlebar.notifications")));
    snap(&mut world, "notifications-empty", dark);
}

/// Home's merge button on a pull request of the user's that is ready, asking first.
fn merge_confirmation(dark: bool) {
    let mut world = popover_world(
        dark,
        |state| {
            state.prs = Some(vec![PR {
                id: "a1".into(),
                number: 7,
                role: "author".into(),
                title: "Route every key through the keymap".into(),
                repo: "victorarias/attn".into(),
                author: "me".into(),
                reason: "ready_to_merge".into(),
                url: "https://github.com/victorarias/attn/pull/7".into(),
                host: "github.com".into(),
                state: "waiting".into(),
                ..PR::default()
            }])
        },
        |_| {},
    );
    press(&mut world, Action::SessionGoToDashboard);
    world.gesture(|app| app.click(Target::key("home.pr.a1.merge")));
    snap(&mut world, "merge-confirmation", dark);
}

/// Home's pull requests while GitHub rate-limits the daemon and a refresh failed: the user's
/// own, a person's and a bot's review requests, and a repository on two GitHub hosts.
fn home_pull_requests(dark: bool) {
    let pr =
        |id: &str, number: i64, role: &str, author: &str, repo: &str, host: &str, title: &str| PR {
            id: id.into(),
            number,
            role: role.into(),
            title: title.into(),
            repo: repo.into(),
            author: author.into(),
            reason: if role == "author" {
                "ready_to_merge"
            } else {
                "review_requested"
            }
            .into(),
            url: format!("https://{host}/{repo}/pull/{number}"),
            host: host.into(),
            state: "waiting".into(),
            ..PR::default()
        };
    let mut world = popover_world(
        dark,
        |state| {
            state.prs = Some(vec![
                pr(
                    "a1",
                    7,
                    "author",
                    "me",
                    "victorarias/attn",
                    "github.com",
                    "Route every key through the keymap",
                ),
                pr(
                    "r1",
                    12,
                    "reviewer",
                    "someone",
                    "org/shop",
                    "github.com",
                    "Cache the catalog",
                ),
                pr(
                    "r2",
                    31,
                    "reviewer",
                    "renovate-bot",
                    "org/shop",
                    "ghe.corp.example",
                    "Bump serde",
                ),
                pr(
                    "r3",
                    4,
                    "reviewer",
                    "someone",
                    "org/api",
                    "github.com",
                    "Add the orders endpoint",
                ),
            ])
        },
        |daemon| {
            daemon.on("refresh_prs", |_| {
                message(RefreshPRsResultMessage {
                    success: false,
                    error: Some("gh is not signed in".into()),
                    ..RefreshPRsResultMessage::new()
                })
            });
        },
    );
    press(&mut world, Action::SessionGoToDashboard);
    world.daemon.emit(RateLimitedMessage {
        rate_limit_resource: "core".into(),
        rate_limit_reset_at: "2026-11-12T09:12:30Z".into(),
        ..RateLimitedMessage::new()
    });
    world.gesture(|app| app.click(Target::key("home.prs.refresh")));
    snap(&mut world, "home-pull-requests", dark);
}

/// Restarting shared Codex while two agents hold it, from the update band.
fn shared_codex_restart(dark: bool) {
    let mut world = popover_world(
        dark,
        |state| {
            state.shared_codex = Some(SharedCodexState {
                running_version: Some("0.162.1".into()),
                installed_version: Some("0.163.0".into()),
                latest_version: Some("0.163.0".into()),
                restart: "pending".into(),
                blockers: Some(
                    [("s0", "turn"), ("s2", "approval")]
                        .iter()
                        .map(|(session, reason)| SharedCodexBlocker {
                            session_id: (*session).into(),
                            reason: (*reason).into(),
                        })
                        .collect(),
                ),
                ..SharedCodexState::default()
            })
        },
        |_| {},
    );
    world.gesture(|app| app.click(Target::role("button").name("Restart now")));
    snap(&mut world, "shared-codex-restart", dark);
}

const HOME_BUILD: &str = "0.14.0 (source git:home, protocol current)";
const OLD_BUILD: &str = "0.13.0 (source git:old, protocol old)";

/// An outpost on `build`, at `phase` of an update that says `outcome` when it ends.
fn outpost(id: &str, build: &str, phase: Option<&str>, outcome: Option<&str>) -> OutpostInfo {
    OutpostInfo {
        id: id.into(),
        name: id.into(),
        destination: format!("{id}:7777"),
        state: "connected".into(),
        reach: "reachable".into(),
        state_since: "2026-11-12T08:00:00Z".into(),
        build: Some(build.into()),
        update: phase.map(|phase| OutpostUpdate {
            phase: phase.into(),
            outcome: outcome.map(Into::into),
        }),
        ..OutpostInfo::default()
    }
}

/// The outpost updates band over the window, with outposts on and `outposts` listed.
fn outposts_banner(dark: bool, name: &str, outposts: Vec<OutpostInfo>) {
    let mut world = popover_world(
        dark,
        |state| {
            if let Some(settings) = state.settings.as_mut() {
                settings.insert("outposts_enabled".into(), "true".into());
            }
            state.home_build = Some(HOME_BUILD.into());
            state.outposts = Some(outposts);
        },
        |_| {},
    );
    snap(&mut world, name, dark);
}

fn outposts_banners(dark: bool) {
    outposts_banner(
        dark,
        "banner-outposts-differ",
        vec![outpost("studio", OLD_BUILD, None, None)],
    );
    outposts_banner(
        dark,
        "banner-outposts-updating",
        vec![
            outpost("studio", OLD_BUILD, Some("building"), None),
            outpost("ai-sandbox", OLD_BUILD, Some("sending"), None),
        ],
    );
    outposts_banner(
        dark,
        "banner-outposts-failed",
        vec![outpost(
            "studio",
            OLD_BUILD,
            Some("failed"),
            Some("ssh: connect to host studio port 22: Connection timed out"),
        )],
    );
    outposts_banner(
        dark,
        "banner-outposts-done",
        vec![outpost("studio", HOME_BUILD, Some("done"), None)],
    );
}

fn update_outpost(id: &str, state: &str, detail: Option<&str>) -> UpdateOutpostProgress {
    UpdateOutpostProgress {
        id: id.into(),
        name: id.into(),
        state: state.into(),
        detail: detail.map(Into::into),
    }
}

/// The update run band over the window, following a run at `phase` that says `outcome`.
fn update_banner(
    dark: bool,
    name: &str,
    phase: &str,
    outcome: Option<&str>,
    outposts: Vec<UpdateOutpostProgress>,
) {
    let run = UpdateRun {
        id: "u1".into(),
        install_kind: "release_app".into(),
        log_path: "/tmp/u1.log".into(),
        phase: phase.into(),
        outcome: outcome.map(Into::into),
        outposts,
        ..UpdateRun::default()
    };
    let mut world = popover_world(dark, |state| state.update_run = Some(run), |_| {});
    snap(&mut world, name, dark);
}

fn update_banners(dark: bool) {
    update_banner(
        dark,
        "banner-update-running",
        "outposts",
        None,
        vec![
            update_outpost("studio", "staging", Some("building")),
            update_outpost("ai-sandbox", "switching", None),
        ],
    );
    update_banner(
        dark,
        "banner-update-failed",
        "failed",
        Some("The update failed: studio could not switch to the new build."),
        vec![
            update_outpost("studio", "failed", Some("ssh: Connection timed out")),
            update_outpost("ai-sandbox", "done", None),
        ],
    );
    update_banner(
        dark,
        "banner-update-done",
        "done",
        Some("attn is up to date."),
        vec![update_outpost("studio", "done", None)],
    );
    update_banner(
        dark,
        "banner-update-skipped",
        "done",
        Some("attn is up to date. studio was skipped: it was offline."),
        vec![update_outpost("studio", "skipped", None)],
    );
}

fn codex(restart: &str) -> SharedCodexState {
    SharedCodexState {
        running_version: Some("0.162.1".into()),
        installed_version: Some("0.163.0".into()),
        latest_version: Some("0.163.0".into()),
        restart: restart.into(),
        ..SharedCodexState::default()
    }
}

/// The shared Codex band over the window, with Codex at `codex`, after `then` drives it.
fn codex_banner(
    dark: bool,
    name: &str,
    codex: SharedCodexState,
    serve: impl FnOnce(&Daemon),
    then: impl FnOnce(&mut World),
) {
    let mut world = popover_world(dark, |state| state.shared_codex = Some(codex), serve);
    then(&mut world);
    snap(&mut world, name, dark);
}

fn codex_banners(dark: bool) {
    let available = SharedCodexState {
        installed_version: Some("0.162.1".into()),
        latest_version: Some("0.164.0".into()),
        ..codex("none")
    };
    let install = |world: &mut World| {
        world.gesture(|app| app.click(Target::key("codex-updates.install")));
    };
    codex_banner(
        dark,
        "banner-codex-available",
        available.clone(),
        |_| {},
        |_| {},
    );
    codex_banner(
        dark,
        "banner-codex-installing",
        available.clone(),
        |daemon| {
            daemon.on("install_codex_update", |_| ());
        },
        install,
    );
    codex_banner(
        dark,
        "banner-codex-install-failed",
        available,
        |daemon| {
            daemon.on("install_codex_update", |r: &Request| {
                message(InstallCodexUpdateResultMessage {
                    request_id: Some(request_id(r)),
                    success: false,
                    error: Some("codex update exited 1".into()),
                    ..InstallCodexUpdateResultMessage::new()
                })
            });
        },
        install,
    );
    let blockers = [("s0", "turn"), ("s2", "approval"), ("s1", "starting")]
        .iter()
        .map(|(session, reason)| SharedCodexBlocker {
            session_id: (*session).into(),
            reason: (*reason).into(),
        })
        .collect::<Vec<_>>();
    codex_banner(
        dark,
        "banner-codex-waiting",
        SharedCodexState {
            blockers: Some(blockers.clone()),
            ..codex("pending")
        },
        |_| {},
        |_| {},
    );
    codex_banner(
        dark,
        "banner-codex-draining",
        SharedCodexState {
            blockers: Some(blockers[..2].to_vec()),
            force_at: Some("2026-11-12T09:01:05Z".into()),
            ..codex("restarting")
        },
        |_| {},
        |_| {},
    );
    codex_banner(
        dark,
        "banner-codex-restarting",
        codex("restarting"),
        |_| {},
        |_| {},
    );
    codex_banner(
        dark,
        "banner-codex-failed",
        SharedCodexState {
            error: Some("approval_policy = \"untrusted\" is no longer supported".into()),
            ..codex("failed")
        },
        |_| {},
        |_| {},
    );
    codex_banner(
        dark,
        "banner-codex-old-tiles",
        SharedCodexState {
            running_version: Some("0.163.0".into()),
            stale_sessions: Some(vec!["s0".into(), "s2".into()]),
            ..codex("none")
        },
        |_| {},
        |_| {},
    );
}

/// The classic picker's new worktree in the api project that git refused, opened from the corner.
fn session_not_created(dark: bool) {
    let daemon = launcher_daemon_with(dark, false, false);
    daemon
        .on("inspect_path", |r: &Request| {
            let path = r["path"].as_str().unwrap_or_default().to_owned();
            message(InspectPathResultMessage {
                success: true,
                inspection: Some(PathInspection {
                    exists: true,
                    is_directory: true,
                    input_path: path.clone(),
                    repo_root: Some(path.clone()),
                    resolved_path: path,
                    home_path: Some("/home/me".into()),
                }),
                ..InspectPathResultMessage::new()
            })
        })
        .on("create_worktree", |_: &Request| {
            message(CreateWorktreeResultMessage {
                success: false,
                error: Some("fatal: a branch named 'taken' already exists".into()),
                ..CreateWorktreeResultMessage::new()
            })
        });
    let mut world = start(daemon);
    press(&mut world, Action::SessionNew);
    world.gesture(|app| app.type_text("api"));
    world.advance(std::time::Duration::from_millis(200));
    tap(&mut world, Key::Enter);
    world.gesture(|app| app.type_text("taken"));
    tap(&mut world, Key::Enter);
    world.gesture(|app| app.click(Target::key("worktree-setup")));
    snap(&mut world, "session-not-created", dark);
}

/// The overview over three desktops: terminal `a` beside the document, one agent, and an empty
/// one.
fn desktop_overview(dark: bool) {
    let mut world = popover_world(
        dark,
        |state| {
            let desktops = state.desktops.as_mut().expect("the tour's desktops");
            desktops.truncate(2);
            desktops.push(Desktop {
                tree_json: String::new(),
                ..desktop("d-empty", "3", Value::Null, vec![])
            });
            state
                .sessions
                .as_mut()
                .expect("the tour's sessions")
                .truncate(2);
        },
        |_| {},
    );
    press(&mut world, Action::DesktopOverview);
    snap(&mut world, "desktop-overview", dark);
}

fn shoot(out: PathBuf, scheme: Option<String>, only: Vec<String>) -> bool {
    if let Some(id) = &scheme {
        let ids: Vec<&str> = attn_ui::colors::SCHEMES.iter().map(|s| s.id).collect();
        assert!(
            ids.contains(&id.as_str()),
            "{id} is not a color scheme; schemes: {ids:?}"
        );
    }
    OUT.set(out);
    SCHEME.set(scheme);
    ONLY.set(only.into_iter().map(|pattern| (pattern, false)).collect());
    let report = std::panic::take_hook();
    std::panic::set_hook(Box::new(move |panic| {
        SCENE.with_borrow(|scene| {
            if !scene.is_empty() {
                eprintln!("screenshot_tour: drawing {scene} failed");
            }
        });
        report(panic);
    }));
    for dark in [true, false] {
        scene("sidebar_and_terminal", || sidebar_and_terminal(dark));
        scene("tile_headers", || tile_headers(dark, false));
        scene("tile_headers", || tile_headers(dark, true));
        scene("tile_focus", || tile_focus(dark));
        scene("shelf", || shelf(dark, false));
        scene("shelf", || shelf(dark, true));
        scene("review_stop", || review_stop(dark));
        scene("garden", || garden(dark));
        scene("garden_dock", || garden_dock(dark));
        scene("garden_dock_plot", || garden_dock_plot(dark));
        scene("garden_seed", || garden_seed(dark));
        scene("garden_dock_seed", || garden_dock_seed(dark));
        scene("garden_empty", || garden_empty(dark));
        scene("garden_dock_empty", || garden_dock_empty(dark));
        scene("garden_review_screen", || {
            garden_review_screen(dark, true, true, "garden-review")
        });
        scene("garden_review_screen", || {
            garden_review_screen(dark, false, true, "garden-review-waiting")
        });
        scene("garden_review_screen", || {
            garden_review_screen(dark, true, false, "garden-review-dock")
        });
        scene("settings_page", || settings_page(dark));
        scene("settings_agents", || settings_agents(dark));
        scene("shortcut_editor", || shortcut_editor(dark));
        scene("close_desktop", || close_desktop(dark));
        scene("palette", || palette(dark));
        scene("palette_no_match", || palette_no_match(dark));
        scene("opener", || opener(dark));
        scene("context_cap", || context_cap(dark));
        scene("terminal_overlays", || terminal_overlays(dark));
        scene("tile_window", || tile_window(dark));
        scene("launcher_dialog", || launcher_dialog(dark));
        scene("launcher_where", || launcher_where(dark));
        scene("launcher_empty_desktop", || launcher_empty_desktop(dark));
        scene("launcher_browse", || launcher_browse(dark));
        scene("automation_hour", || automation_hour(dark));
        scene("automation_time_zones", || automation_time_zones(dark));
        scene("picker_dialog", || picker_dialog(dark));
        scene("picker_empty_desktop", || picker_empty_desktop(dark));
        scene("shortcuts", || shortcuts(dark));
        scene("agent_palette", || agent_palette(dark));
        scene("profile_switcher", || profile_switcher(dark));
        scene("whats_new", || whats_new(dark, 0, "whats-new"));
        scene("whats_new", || whats_new(dark, 1, "whats-new-night"));
        scene("settings_analytics", || settings_analytics(dark));
        scene("ledger", || ledger(dark));
        scene("asks", || asks(dark));
        scene("asks_empty", || asks_empty(dark));
        scene("asks_choices", || asks_choices(dark));
        scene("notebook", || notebook(dark));
        scene("notebook_tree", || notebook_tree(dark));
        scene("review_keys", || review_keys(dark));
        scene("review_file", || review_file(dark));
        scene("review_focused", || review_focused(dark));
        scene("review_walkthrough", || review_walkthrough(dark));
        scene("review_search", || review_search(dark));
        scene("review_send_sheet", || review_send_sheet(dark));
        scene("ledger_seeded", || ledger_seeded(dark));
        scene("ledger_worktrees", || ledger_worktrees(dark));
        scene("chief_setup", || chief_setup(dark));
        scene("home", || home(dark));
        scene("home_empty", || home_empty(dark));
        scene("home_pull_requests", || home_pull_requests(dark));
        scene("palette_chief", || palette_chief(dark));
        scene("manage_crew", || {
            manage_crew(dark, "chief", "launch", "crew-launch")
        });
        scene("manage_crew", || {
            manage_crew(dark, "scout", "charter", "crew-charter")
        });
        scene("manage_crew", || {
            manage_crew(dark, "scout", "handoffs", "crew-handoffs")
        });
        scene("manage_crew", || {
            manage_crew(dark, "scout", "seeds", "crew-seeds")
        });
        scene("notifications", || notifications(dark));
        scene("session_menu", || session_menu(dark));
        scene("queue_home", || queue_home(dark));
        scene("queue_agent_list", || queue_agent_list(dark));
        scene("queue_seeded_home", || queue_seeded_home(dark));
        scene("queue_runs", || queue_runs(dark, false, "queue-runs"));
        scene("queue_runs", || queue_runs(dark, true, "queue-runs-open"));
        scene("queue_seeded_agent", || queue_seeded_agent(dark));
        scene("queue_bar", || queue_bar(dark));
        scene("queue_bar_crowded", || queue_bar_crowded(dark));
        scene("rail", || rail(dark));
        scene("session_seeds", || session_seeds(dark));
        scene("markdown_selection", || markdown_selection(dark));
        scene("annotation_selection", || annotation_selection(dark));
        scene("annotation_comment", || annotation_comment(dark));
        scene("annotation_saved_shot", || annotation_saved_shot(dark));
        scene("annotation_notes", || annotation_notes(dark));
        scene("markdown_reader", || markdown_reader(dark));
        scene("markdown_missing", || markdown_missing(dark));
        scene("snooze_menu", || snooze_menu(dark));
        scene("rename_session", || rename_session(dark));
        scene("session_usage", || session_usage(dark));
        scene("session_seed_context", || session_seed_context(dark));
        scene("notifications_empty", || notifications_empty(dark));
        scene("desktop_overview", || desktop_overview(dark));
        scene("settings_delegation", || settings_delegation(dark));
        scene("settings_section", || {
            settings_section(dark, "outposts", "outposts", SWITCHED_ON, |_| {})
        });
        scene("settings_section", || {
            settings_section(dark, "experimental", "experimental", SWITCHED_ON, |_| {})
        });
        scene("settings_phones", || settings_phones(dark));
        scene("settings_section", || {
            settings_section(dark, "plugins", "plugins", &[], |_| {})
        });
        scene("settings_updates", || settings_updates(dark));
        scene("settings_event_bus", || settings_event_bus(dark));
        scene("settings_section", || {
            settings_section(dark, "desktop", "files", &[], |_| {})
        });
        scene("settings_smallest", || settings_smallest(dark));
        scene("settings_section", || {
            settings_section(dark, "hygiene", "attention queue", &[], |_| {})
        });
        scene("settings_section", || {
            settings_section(dark, "backgroundAgents", "background agents", &[], |_| {})
        });
        scene("settings_section", || {
            settings_section(dark, "backgroundTasks", "task runner", &[], |_| {})
        });
        scene("settings_connectivity", || settings_connectivity(dark));
        scene("settings_models", || settings_models(dark));
        scene("settings_bottom", || {
            settings_bottom(dark, "agents", "agents", &[])
        });
        scene("settings_bottom", || {
            settings_bottom(dark, "experimental", "experimental", SWITCHED_ON)
        });
        scene("settings_page_states", || settings_page_states(dark));
        scene("automation_new", || automation_new(dark));
        scene("automations_empty", || automations_empty(dark));
        scene("automations_listed", || automations_listed(dark));
        scene("automations_selected", || automations_selected(dark));
        scene("automation_edit", || automation_edit(dark));
        scene("automation_github", || automation_github(dark));
        scene("automation_advanced", || automation_advanced(dark));
        scene("diagnostic_report", || diagnostic_report(dark));
        scene("quick_capture_empty", || quick_capture_empty(dark));
        scene("quick_capture", || quick_capture(dark));
        scene("quick_capture_mailboxes", || quick_capture_mailboxes(dark));
        scene("notebook_file", || notebook_file(dark));
        scene("notebook_finder", || notebook_finder(dark));
        scene("notebook_note", || notebook_note(dark));
        scene("notebook_tile_empty", || notebook_tile_empty(dark));
        scene("notebook_tile_note", || notebook_tile_note(dark));
        scene("notebook_tile_finder", || notebook_tile_finder(dark));
        scene("merge_confirmation", || merge_confirmation(dark));
        scene("desktop_naming", || desktop_naming(dark));
        scene("shared_codex_restart", || shared_codex_restart(dark));
        scene("session_not_created", || session_not_created(dark));
        scene("outposts_banners", || outposts_banners(dark));
        scene("update_banners", || update_banners(dark));
        scene("codex_banners", || codex_banners(dark));
    }
    let unmatched: Vec<String> = ONLY.with_borrow(|only| {
        only.iter()
            .filter(|(_, matched)| !matched)
            .map(|(pattern, _)| pattern.clone())
            .collect()
    });
    for pattern in &unmatched {
        eprintln!("screenshot_tour: --only {pattern} names no screen");
    }
    FAILED.with_borrow(|failed| {
        if !failed.is_empty() {
            let path = OUT.with_borrow(|out| out.join("failed.txt"));
            std::fs::write(&path, failed.join("\n") + "\n")
                .unwrap_or_else(|e| panic!("{}: {e}", path.display()));
            eprintln!(
                "screenshot_tour: {} scenes could not run, listed in {}",
                failed.len(),
                path.display()
            );
        }
    });
    unmatched.is_empty()
}

fn pngs(dir: &std::path::Path) -> Vec<String> {
    let mut names: Vec<String> = std::fs::read_dir(dir)
        .unwrap_or_else(|e| panic!("{}: {e}", dir.display()))
        .filter_map(|e| e.ok()?.file_name().into_string().ok())
        .filter(|n| n.ends_with(".png"))
        .collect();
    names.sort();
    names
}

/// Each pixel that differs in red over a faded copy of the baseline.
fn marked(base: &Image, after: &Image) -> (usize, Image) {
    let mut differ = 0;
    let rgb = base
        .rgb
        .as_chunks::<3>()
        .0
        .iter()
        .zip(after.rgb.as_chunks::<3>().0)
        .flat_map(|(b, a)| {
            if a == b {
                b.iter().map(|c| 192 + c / 4).collect::<Vec<_>>()
            } else {
                differ += 1;
                vec![255, 0, 0]
            }
        })
        .collect();
    (
        differ,
        Image {
            width: base.width,
            height: base.height,
            rgb,
        },
    )
}

fn compare(base: PathBuf, after: PathBuf, diff: PathBuf) -> bool {
    let names = pngs(&base);
    assert_eq!(names, pngs(&after), "the two runs shot different screens");
    let workers = std::thread::available_parallelism().map_or(1, usize::from);
    let per_worker = names.len().div_ceil(workers).max(1);
    let results: Vec<Result<usize, String>> = std::thread::scope(|threads| {
        let workers: Vec<_> = names
            .chunks(per_worker)
            .map(|chunk| {
                let (base, after, diff) = (&base, &after, &diff);
                threads.spawn(move || {
                    chunk
                        .iter()
                        .map(|name| compare_screen(name, base, after, diff))
                        .collect::<Vec<_>>()
                })
            })
            .collect();
        workers
            .into_iter()
            .flat_map(|w| w.join().expect("a compare worker"))
            .collect()
    });
    let mut changed = Vec::new();
    for (name, result) in names.iter().zip(results) {
        match result {
            Err(resized) => changed.push(resized),
            Ok(differ) => {
                if differ > 0 {
                    changed.push(format!("{name}: {differ} pixels differ"));
                }
                println!("{name}: {differ} pixels differ");
            }
        }
    }
    for change in &changed {
        eprintln!("{change}");
    }
    changed.is_empty()
}

/// The number of pixels that differ, with an image of them in DIFF, or how the size changed.
fn compare_screen(
    name: &str,
    base: &std::path::Path,
    after: &std::path::Path,
    diff: &std::path::Path,
) -> Result<usize, String> {
    let read =
        |dir: &std::path::Path| Image::read_png(&dir.join(name)).unwrap_or_else(|e| panic!("{e}"));
    let (b, a) = (read(base), read(after));
    if (b.width, b.height) != (a.width, a.height) {
        return Err(format!(
            "{name}: {}x{} became {}x{}",
            b.width, b.height, a.width, a.height
        ));
    }
    let (differ, image) = marked(&b, &a);
    if differ > 0 {
        std::fs::create_dir_all(diff).expect("the diff folder");
        image.write_png(&diff.join(name)).expect("a diff image");
    }
    Ok(differ)
}

fn main() -> std::process::ExitCode {
    // SAFETY: no other thread runs yet. A new schedule takes this machine's zone.
    unsafe { std::env::set_var("TZ", "Europe/Stockholm") };
    let args: Vec<PathBuf> = std::env::args_os().skip(1).map(PathBuf::from).collect();
    let words: Vec<&str> = args.iter().filter_map(|a| a.to_str()).collect();
    match (words.first().copied(), &args[..]) {
        (Some("shoot"), [_, out, ..]) if words.len() == args.len() => {
            let mut scheme = None;
            let mut only = Vec::new();
            let mut run = TOUR;
            let mut rest = &words[2..];
            while let Some((flag, after)) = rest.split_first() {
                rest = after;
                if *flag == "--long" {
                    run.long = true;
                    continue;
                }
                let Some((value, after)) = rest.split_first() else {
                    return usage_error();
                };
                rest = after;
                match *flag {
                    "--scheme" => scheme = Some((*value).to_owned()),
                    "--only" => only.push((*value).to_owned()),
                    "--window" => match value
                        .split_once('x')
                        .and_then(|(w, h)| Some((w.parse().ok()?, h.parse().ok()?)))
                    {
                        Some(window) => run.window = window,
                        None => return usage_error(),
                    },
                    "--text" => match value.parse() {
                        Ok(text) => run.text = text,
                        Err(_) => return usage_error(),
                    },
                    _ => return usage_error(),
                }
            }
            MODE.set(run);
            if !shoot(out.clone(), scheme, only) {
                return std::process::ExitCode::FAILURE;
            }
        }
        (Some("compare"), [_, base, after, diff]) => {
            if !compare(base.clone(), after.clone(), diff.clone()) {
                return std::process::ExitCode::FAILURE;
            }
        }
        _ => return usage_error(),
    }
    std::process::ExitCode::SUCCESS
}

fn usage_error() -> std::process::ExitCode {
    eprintln!(
        "usage: screenshot_tour shoot OUT [--scheme ID] [--only PATTERN]... [--window WxH] [--text SCALE] [--long] | compare BASE AFTER DIFF"
    );
    std::process::ExitCode::from(2)
}
