//! The screenshot tour: a fixed set of the app's screens, each drawn offscreen on the GPU in
//! dark and in light, one PNG each. `scripts/native-screenshot-tour` runs it.
//!
//!     screenshot_tour shoot OUT [SCHEME]   SCHEME sets the color_scheme setting
//!     screenshot_tour compare BASE AFTER DIFF

use std::collections::BTreeMap;
use std::path::PathBuf;
use std::sync::LazyLock;

use attn_app::desktop::FocusStyle;
use attn_app::screen::Screens;
use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    ActorView, AgentCatalogResult, AgentCatalogResultMessage, Ask, AskChoice, AskPart, AskQuestion,
    AskReference, AutomationDefinitionResultMessage, AutomationDefinitionSummary,
    AutomationDefinitionsResultMessage, AutomationProvenance, AutomationRunSummary,
    AutomationRunsResultMessage, AutomationSchedulePreviewResultMessage,
    BrowseDirectoryResultMessage, BusConsumerStatus, BusStatusResultMessage,
    CalloutShowRequestMessage, CalloutStep, CreateWorktreeResultMessage, CrewCharterDocument,
    CrewCharterGetResultMessage, CrewHandoffsGetResultMessage, CrewMember, DelegationChoice,
    DelegationFallback, DelegationPreferences, DelegationPreferencesResultMessage, DelegationRole,
    DelegationSelection, Desktop, DesktopPane, DesktopTileContentMessage, DirectoryEntry,
    FileActivity, FsIndexResultMessage, GardenBed, GetCommandUsageResultMessage,
    GetRepoInfoResultMessage, Harness, HarnessModel, HarnessModelsResultMessage,
    InitialStateMessage, InspectPathResultMessage, InstallCodexUpdateResultMessage,
    LaunchDesktopResultMessage, LaunchDesktopSetting, Notification, NotificationListResultMessage,
    OutpostInfo, OutpostUpdate, PR, PartyView, PathInspection, PhoneLink, PhonesUpdatedMessage,
    Profile, RateLimitedMessage, RecentFilesResultMessage, RecentLocation,
    RecentLocationsResultMessage, RecordCommandUsageResultMessage, RefreshPRsResultMessage,
    RepoInfo, ResolveSessionsResultMessage, ResolvedSession, ReviewBundle, ReviewFile, ReviewFlow,
    ReviewFlowGroup, ReviewFlowMessage, ReviewFlowParticipant, ReviewNotShown, ReviewNote,
    ReviewRow, ReviewStop, Seed, SeedCheck, SeedContinuation, SeedDocument,
    SeedDocumentGetResultMessage, SeedEdge, SeedNote, SeedPlotProgress, Session,
    SessionAnnotationsGetResultMessage, SessionAnnotationsSaveResultMessage, SessionLedgerEntry,
    SessionListResult, SessionListResultMessage, SessionMessage, SessionMessagesGetResultMessage,
    SessionPullRequest, SessionReopen, SessionReopenResultMessage, SessionUsage, SessionUsageModel,
    SharedCodexBlocker, SharedCodexState, TerminalBinding, UpdateChangedMessage,
    UpdateOutpostProgress, UpdateRun, Worktree, WorktreeListResult, WorktreeListResultEvent,
};
use attn_term::{FONT_SIZE, Metrics};
use attn_testkit::tree::{below, pane, row};
use attn_testkit::{Daemon, Request, Target, World, key_event, message};
use rune::render::{Gpu, OFFSCREEN_FORMAT, Offscreen, Renderer, write_png};
use rune::text::pixel_compare::Image;
use rune::{Compositor, Frame, Runtime};
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

/// What a long run adds to a name, a branch, a machine or a folder: a worktree's basename, in
/// Latin and in CJK.
const LONG_NAME: &str =
    "build-and-release-platform-本番の権限を移行する作業ツリー-tokyo-region-primary";

/// What a long run adds to an error: a daemon's failure as it reads.
const LONG_ERROR: &str = "the app server exited with status 1 before it answered its first \
     request, after loading a configuration it could not read from \
     ~/.config/attn/outposts/build-and-release-platform-tokyo-region-primary.toml";

/// Each name, label, title, machine and branch in `value` made long, each repository and
/// worktree path given a long last folder, and each error and message made a long sentence, the
/// same way every time so the app and the scene agree on what it sent.
fn lengthen(value: &mut Value) {
    match value {
        Value::Object(fields) => {
            for (key, field) in fields.iter_mut() {
                let Value::String(said) = &mut *field else {
                    lengthen(field);
                    continue;
                };
                if said.is_empty() || said.ends_with(LONG_NAME) || said.ends_with(LONG_ERROR) {
                    continue;
                }
                match key.as_str() {
                    "label" | "name" | "title" | "display_name" | "profile_name"
                    | "desktop_name" | "definition_name" | "subject" | "destination"
                    | "host_name" | "branch" | "current_branch" | "default_branch" => {
                        *said = format!("{said}-{LONG_NAME}");
                    }
                    "path" | "cwd" | "repo" | "main_repo" | "repo_root" | "worktree_path"
                        if said.starts_with('/')
                            && said
                                .rsplit('/')
                                .next()
                                .is_some_and(|last| !last.is_empty() && !last.contains('.')) =>
                    {
                        *said = format!("{said}-{LONG_NAME}");
                    }
                    "error" | "error_message" | "message" | "detail" => {
                        *said = format!("{said}: {LONG_ERROR}");
                    }
                    _ => {}
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
    let chord = match Keymap::from_setting(None, attn_testkit::platform()).binding(&action) {
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

/// The daemon reads `totals.rs` whole for the file view.
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
    draw(&GPU, rt, compositor)
}

fn draw<S: 'static>(
    gpu: &Gpu,
    rt: &mut Runtime<S>,
    compositor: &mut impl Compositor<S>,
) -> (u32, u32, Vec<u8>) {
    let (w, h) = rt.window_viewport();
    let scale = rt.window_scale();
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

static GPU: LazyLock<Gpu> = LazyLock::new(|| {
    Gpu::try_with_features(
        Gpu::instance(),
        None,
        Compositor::<attn_app::State>::features(&Screens::default()),
    )
    .unwrap_or_else(|error| panic!("{error}"))
});

thread_local! {
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
    let tree: Value =
        serde_json::from_str(&world.screen().to_string()).unwrap_or_else(|e| panic!("{name}: {e}"));
    let sideways = sideways_views(&tree);
    let mut cut = Vec::new();
    let mut overprinted = Vec::new();
    let mut reach = None;
    world.gesture(|app| {
        shot = Some(pixels(app.runtime, &mut Screens::default()));
        let elements = app.runtime.elements();
        cost = Some((elements.len(), app.runtime.instances.len()));
        cut = cut_text(&elements, &sideways);
        overprinted = overprinted_text(&elements);
        reach = Some(reach_of(&elements));
    });
    save(name, dark, shot.expect("the window drew"));
    if let Some((nodes, instances)) = cost {
        record_cost(name, dark, nodes, instances);
    }
    record_cuts(name, dark, &cut);
    record_overprints(name, dark, &overprinted);
    if dark && let Some(reach) = reach {
        record_reach(name, &reach);
        record_a11y(name, &world.screen().to_string());
    }
}

/// Each screen's accessibility tree as assistive technology reads it, one keyed node a line:
/// key, role, name, value, description, states, level, the actions it answers, such as
/// scrolling, and the key of its nearest keyed ancestor and the role and name of its nearest
/// named one, so a node that leaves its item or its region shows, in `a11y/NAME.tsv`, for
/// diffing two tours beyond what `reach` holds.
fn record_a11y(name: &str, tree: &str) {
    let tree: Value = serde_json::from_str(tree).unwrap_or_else(|e| panic!("{name}: {e}"));
    let mut lines = Vec::new();
    let mut stack = vec![(&tree, "-".to_owned(), "-".to_owned())];
    while let Some((node, keyed, named)) = stack.pop() {
        let field = |f: &str| {
            node.get(f)
                .map(|v| v.as_str().map_or_else(|| v.to_string(), str::to_owned))
                .unwrap_or_else(|| "-".to_owned())
                .replace(['\n', '\t'], " ")
        };
        if node.get("key").is_some() {
            let mut line = [
                "key",
                "role",
                "name",
                "value",
                "description",
                "states",
                "level",
                "actions",
            ]
            .map(field)
            .to_vec();
            line.push(keyed.clone());
            line.push(named.clone());
            lines.push(line.join("\t"));
        }
        let keyed = node
            .get("key")
            .and_then(Value::as_str)
            .map_or(keyed, str::to_owned);
        let named = node.get("name").and_then(Value::as_str).map_or(named, |n| {
            format!("{} {}", field("role"), n.replace(['\n', '\t'], " "))
        });
        if let Some(children) = node.get("children").and_then(Value::as_array) {
            stack.extend(
                children
                    .iter()
                    .rev()
                    .map(|child| (child, keyed.clone(), named.clone())),
            );
        }
    }
    lines.sort();
    let dir = OUT.with_borrow(|out| out.join("a11y"));
    std::fs::create_dir_all(&dir).unwrap_or_else(|e| panic!("{}: {e}", dir.display()));
    let path = dir.join(format!("{name}.tsv"));
    std::fs::write(&path, lines.join("\n") + "\n")
        .unwrap_or_else(|e| panic!("{}: {e}", path.display()));
}

/// Whether the element at `at` is a text field or inside one. A field's text scrolls under its
/// caret and a completion draws inside it, both on purpose.
fn in_field(elements: &[rune::ElementInfo], mut at: usize) -> bool {
    loop {
        if matches!(elements[at].role, "textbox" | "searchbox") {
            return true;
        }
        match elements[at].parent {
            Some(parent) => at = parent,
            None => return false,
        }
    }
}

/// Text a clip cuts across its line, or whose glyphs run past its own box: what it says and how
/// many points of its ink the clip hides or its box does not hold. Text that truncates fits its
/// box and is never cut, text scrolled partly out of view is cut only top or bottom, and text a
/// clip hides whole, its padding showing at most, is not drawn, so it is not cut. Text under a modal
/// is left out, as it is judged where it is in front, and so is text in a field. Text inside a view
/// that scrolls sideways is wider on purpose, and is left out: a scroll view with room to scroll
/// sideways, which `sideways` lists by key and box, as accessibility offers it scrolling left.
fn cut_text(elements: &[rune::ElementInfo], sideways: &[(String, [f32; 4])]) -> Vec<(String, f32)> {
    let is_sideways = |e: &rune::ElementInfo| {
        let near = |a: [f32; 4], b: [f32; 4]| (0..4).all(|i| (a[i] - b[i]).abs() < 0.5);
        sideways.iter().any(|(key, bounds)| {
            e.key.as_deref() == Some(key.as_str())
                && (near(e.rect, *bounds) || near(e.visible, *bounds))
        })
    };
    let scrolls_sideways = |mut at: usize| {
        while let Some(parent) = elements[at].parent {
            if is_sideways(&elements[parent]) {
                return true;
            }
            at = parent;
        }
        false
    };
    elements
        .iter()
        .enumerate()
        .filter(|(_, e)| !e.inert && e.opacity > 0.0)
        .filter_map(|(at, e)| {
            let said = e.text.as_ref()?;
            let [x, _, w, _] = e.rect;
            let [vx, _, vw, vh] = e.visible;
            if said.trim().is_empty() || vw <= 0.0 || vh <= 0.0 {
                return None;
            }
            if scrolls_sideways(at) || in_field(elements, at) {
                return None;
            }
            let [ix, _, iw, _] = e.text_ink.unwrap_or(e.rect);
            let across = |from: f32, to: f32| (ix + iw).min(to) - ix.max(from);
            if across(vx, vx + vw) <= 0.5 {
                return None;
            }
            let hidden = iw - across(vx.max(x), (vx + vw).min(x + w)).max(0.0);
            (hidden > 0.5).then(|| (said.clone(), hidden))
        })
        .collect()
}

/// The scroll views of an accessibility tree with room to scroll sideways, by key and box.
fn sideways_views(tree: &Value) -> Vec<(String, [f32; 4])> {
    let mut found = Vec::new();
    let mut stack = vec![tree];
    while let Some(node) = stack.pop() {
        let offers_left = node["actions"]
            .as_array()
            .is_some_and(|actions| actions.iter().any(|a| a == "scroll-left"));
        if let (true, Some(key), Some(bounds)) =
            (offers_left, node["key"].as_str(), node["bounds"].as_array())
        {
            let at = |i: usize| bounds.get(i).and_then(Value::as_f64).unwrap_or_default() as f32;
            found.push((key.to_owned(), [at(0), at(1), at(2), at(3)]));
        }
        if let Some(children) = node["children"].as_array() {
            stack.extend(children);
        }
    }
    found
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

/// Pairs of texts drawn over one another: two texts whose visible boxes cross with nothing
/// between them. An element that holds the later text but not the earlier, and fills or blurs
/// what is behind it across where they cross, as a sheet, a popover or a panel does, hides the
/// earlier one, so that pair is not listed. Text in a field, and a link's or a highlight's box,
/// which lies over its own text, pair with nothing.
fn overprinted_text(elements: &[rune::ElementInfo]) -> Vec<(String, String)> {
    let holders = |mut at: usize| {
        let mut up = vec![at];
        while let Some(parent) = elements[at].parent {
            up.push(parent);
            at = parent;
        }
        up
    };
    let hides = |e: &rune::ElementInfo, [x, y, w, h]: [f32; 4]| {
        let [vx, vy, vw, vh] = e.visible;
        (e.fill.a > 0.99 || e.backdrop_blur > 0.0)
            && vx <= x + 0.5
            && vy <= y + 0.5
            && vx + vw + 0.5 >= x + w
            && vy + vh + 0.5 >= y + h
    };
    let texts: Vec<usize> = elements
        .iter()
        .enumerate()
        .filter(|(_, e)| !e.exiting && e.opacity > 0.0 && e.visible[2] > 0.0 && e.visible[3] > 0.0)
        .filter(|(_, e)| e.text.as_ref().is_some_and(|t| !t.trim().is_empty()))
        .filter(|(at, e)| {
            !e.path.contains("#link-") && !e.path.contains("#mark-") && !in_field(elements, *at)
        })
        .map(|(at, _)| at)
        .collect();
    let mut pairs = Vec::new();
    for (n, &a) in texts.iter().enumerate() {
        let under = holders(a);
        for &b in &texts[n + 1..] {
            let ([ax, ay, aw, ah], [bx, by, bw, bh]) = (elements[a].visible, elements[b].visible);
            let (x, y) = (ax.max(bx), ay.max(by));
            let crossed = [x, y, (ax + aw).min(bx + bw) - x, (ay + ah).min(by + bh) - y];
            if crossed[2] <= 0.5 || crossed[3] <= 0.5 || under.contains(&b) {
                continue;
            }
            let over = holders(b);
            if over.contains(&a) {
                continue;
            }
            let covered = over
                .iter()
                .filter(|at| !under.contains(at))
                .any(|&at| hides(&elements[at], crossed));
            if !covered {
                let said = |at: usize| elements[at].text.clone().unwrap_or_default();
                pairs.push((said(a), said(b)));
            }
        }
    }
    pairs
}

/// Each screen's overprinted text, beside its pictures in `overlaps.tsv`, as `clips.tsv` lists
/// cut text: one line a pair, and a screen with none still listed with a count of 0.
fn record_overprints(name: &str, dark: bool, pairs: &[(String, String)]) {
    use std::io::Write;
    let appearance = if dark { "dark" } else { "light" };
    let path = OUT.with_borrow(|out| out.join("overlaps.tsv"));
    let mut file = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(&path)
        .unwrap_or_else(|e| panic!("{}: {e}", path.display()));
    let screen = format!("{name}-{appearance}");
    let flat = |said: &str| said.replace(['\t', '\n'], " ");
    let mut lines = vec![format!("{screen}\t{}\t\t", pairs.len())];
    lines.extend(
        pairs
            .iter()
            .map(|(a, b)| format!("{screen}\t\t{}\t{}", flat(a), flat(b))),
    );
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

fn sidebar_crew_and_empty(dark: bool) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    let chief_session = Session {
        crew_member: Some("chief".into()),
        chief_of_staff: Some(true),
        label: "Chief".into(),
        ..session("chief-day", "waiting_input")
    };
    state.sessions.get_or_insert_default().push(chief_session);
    let desktops = state.desktops.get_or_insert_default();
    desktops.truncate(3);
    if let Some(first) = desktops.first_mut() {
        first.shortcut_slot = Some(1);
    }
    if let Some(second) = desktops.get_mut(1) {
        second.shortcut_slot = Some(4);
        second.name = "MCP".into();
    }
    if let Some(third) = desktops.get_mut(2) {
        third.shortcut_slot = Some(8);
        third.name = "Agents".into();
    }
    desktops.push(Desktop {
        id: "work/desktop_chief".into(),
        profile_id: PROFILE.into(),
        name: "Chief".into(),
        order_key: "85".into(),
        active_pane_id: "p-chief".into(),
        tree_json: pane("p-chief").to_string(),
        panes: vec![agent_pane("work/desktop_chief", "p-chief", "chief-day")],
        ..Desktop::default()
    });
    desktops.push(Desktop {
        id: "work/desktop_6".into(),
        profile_id: PROFILE.into(),
        name: "Familiar".into(),
        order_key: "90".into(),
        shortcut_slot: Some(6),
        ..Desktop::default()
    });
    desktops.push(Desktop {
        id: "work/desktop_asst".into(),
        profile_id: PROFILE.into(),
        name: "assistant".into(),
        order_key: "95".into(),
        ..Desktop::default()
    });
    let member =
        |id: &str, name: &str, chief: bool, binding: Option<&str>, desktop: Option<&str>| {
            CrewMember {
                id: id.into(),
                name: name.into(),
                chief: Some(chief),
                agent: Some("claude".into()),
                resolved_agent: "claude".into(),
                profile_id: PROFILE.into(),
                binding_session: binding.map(str::to_owned),
                launch_desktop: desktop.map(|d| LaunchDesktopSetting {
                    desktop_id: Some(d.into()),
                    ..LaunchDesktopSetting::default()
                }),
                revision: 1,
                ..CrewMember::default()
            }
        };
    state.crew = Some(vec![
        member(
            "chief",
            "Chief",
            true,
            Some("chief-day"),
            Some("work/desktop_chief"),
        ),
        member("vesta", "Vesta", false, None, None),
        member("tendril", "Tendril", false, None, None),
    ]);
    let mut world = start(scripted().state(state));
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    snap(&mut world, "sidebar-crew-and-empty", dark);
}

fn sidebar_automations(dark: bool) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    let auto = |id: i64, name: &str, run_id: &str| AutomationProvenance {
        run_id: run_id.into(),
        definition_id: id,
        definition_name: name.into(),
        trigger_type: "scheduled".into(),
        ..AutomationProvenance::default()
    };
    let sessions = state.sessions.get_or_insert_default();
    sessions.push(Session {
        crew_member: Some("chief".into()),
        chief_of_staff: Some(true),
        label: "Chief".into(),
        ..session("chief-day", "waiting_input")
    });
    sessions.push(Session {
        label: "CI sheriff: main CI".into(),
        turn_owed: Some(true),
        turn_opened_at: Some("2026-09-26T09:00:00Z".into()),
        automation: Some(auto(1, "Sheriff", "r-1")),
        ..session("auto-sheriff", "waiting_input")
    });
    sessions.push(Session {
        label: "Feature map audit".into(),
        automation: Some(AutomationProvenance {
            pull_request: Some(attn_protocol::PullRequestProvenance {
                repository: "SolenesInc/attn".into(),
                number: 948,
                url: "https://github.com/SolenesInc/attn/pull/948".into(),
                ..attn_protocol::PullRequestProvenance::default()
            }),
            ..auto(2, "Feature map audit", "r-2")
        }),
        ..session("auto-audit", "working")
    });
    sessions.push(Session {
        label: "Reviewing Recent".into(),
        state_since: "2026-09-26T11:00:00Z".into(),
        automation: Some(auto(3, "Review main", "r-3")),
        ..session("auto-review", "idle")
    });
    sessions.push(Session {
        label: "Sheriff: land drive".into(),
        state_since: "2026-09-26T10:00:00Z".into(),
        automation: Some(auto(4, "sigma sheriff", "r-4")),
        ..session("auto-sigma", "idle")
    });
    sessions.push(Session {
        label: "Land feature map".into(),
        state_since: "2026-09-26T09:00:00Z".into(),
        automation: Some(auto(5, "land feature map drive", "r-5")),
        ..session("auto-land", "idle")
    });
    let desktops = state.desktops.get_or_insert_default();
    desktops.truncate(2);
    if let Some(first) = desktops.first_mut() {
        first.shortcut_slot = Some(1);
    }
    if let Some(second) = desktops.get_mut(1) {
        second.shortcut_slot = Some(4);
        second.name = "MCP".into();
    }
    desktops.push(Desktop {
        id: "work/desktop_chief".into(),
        profile_id: PROFILE.into(),
        name: "Chief".into(),
        order_key: "85".into(),
        active_pane_id: "p-chief".into(),
        tree_json: pane("p-chief").to_string(),
        panes: vec![agent_pane("work/desktop_chief", "p-chief", "chief-day")],
        ..Desktop::default()
    });
    desktops.push(Desktop {
        id: "work/desktop_6".into(),
        profile_id: PROFILE.into(),
        name: "Familiar".into(),
        order_key: "90".into(),
        shortcut_slot: Some(6),
        ..Desktop::default()
    });
    for (id, order, name, pid, sid) in [
        ("auto-d1", "91", "Sheriff", "p-a1", "auto-sheriff"),
        ("auto-d2", "92", "Feature map audit", "p-a2", "auto-audit"),
        ("auto-d3", "93", "Review main", "p-a3", "auto-review"),
        ("auto-d4", "94", "sigma sheriff", "p-a4", "auto-sigma"),
        ("auto-d5", "95", "land drive", "p-a5", "auto-land"),
    ] {
        desktops.push(Desktop {
            id: id.into(),
            profile_id: PROFILE.into(),
            name: name.into(),
            order_key: order.into(),
            active_pane_id: pid.into(),
            tree_json: pane(pid).to_string(),
            panes: vec![agent_pane(id, pid, sid)],
            ..Desktop::default()
        });
    }
    let member =
        |id: &str, name: &str, chief: bool, binding: Option<&str>, desktop: Option<&str>| {
            CrewMember {
                id: id.into(),
                name: name.into(),
                chief: Some(chief),
                agent: Some("claude".into()),
                resolved_agent: "claude".into(),
                profile_id: PROFILE.into(),
                binding_session: binding.map(str::to_owned),
                launch_desktop: desktop.map(|d| LaunchDesktopSetting {
                    desktop_id: Some(d.into()),
                    ..LaunchDesktopSetting::default()
                }),
                revision: 1,
                ..CrewMember::default()
            }
        };
    state.crew = Some(vec![
        member(
            "chief",
            "Chief",
            true,
            Some("chief-day"),
            Some("work/desktop_chief"),
        ),
        member("vesta", "Vesta", false, None, None),
        member("tendril", "Tendril", false, None, None),
    ]);
    let mut world = start(scripted().state(state));
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    snap(&mut world, "sidebar-automations", dark);
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

/// Agent `a` with the attention supervisor's chip on its tile: the reason for a turn the
/// supervisor opened, or, when `finished`, the finished chip and the notice offering Reopen for
/// an agent the supervisor closed.
fn supervisor_chip(dark: bool, finished: bool) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), Vec::new());
    let (verdict, label, reason) = if finished {
        ("finished", None, "Nothing is left for anyone")
    } else {
        (
            "open",
            Some("unread report"),
            "Its 02:10 report hasn't reached you",
        )
    };
    if let Some(agent) = state.sessions.iter_mut().flatten().next() {
        agent.attention = Some(attn_protocol::SessionAttention {
            verdict: verdict.into(),
            decider: "rule:unread_report".into(),
            reason: reason.into(),
            label: label.map(str::to_owned),
            at: "2026-11-27T21:00:00Z".into(),
        });
    }
    let mut world = start(scripted().state(state));
    write(&mut world, BOXES);
    if finished {
        world.daemon.emit(attn_protocol::SessionClosedMessage {
            session_ledger_entry: SessionLedgerEntry {
                id: "s-closed".into(),
                label: "Finish the remaining steps".into(),
                agent: "claude".into(),
                profile_id: PROFILE.into(),
                closed_at: Some("2026-11-27T21:00:00Z".into()),
                closed_by: Some("supervisor".into()),
                close_reason: Some("settled when finished".into()),
                last_seen: "2026-11-27T21:00:00Z".into(),
                ..SessionLedgerEntry::default()
            },
            ..attn_protocol::SessionClosedMessage::new()
        });
        world.settle();
    }
    let name = if finished {
        "supervisor-finished-reopen"
    } else {
        "supervisor-opened"
    };
    snap(&mut world, name, dark);
}

/// Terminal `a` beside a browser tile: its address in the header's title, Reload, and the page
/// area, which says where no engine runs pages.
fn browser_tile(dark: bool) {
    let tile = json!({
        "type": "tile",
        "tile_id": "w",
        "tile_kind": "browser",
        "tile_params": "https://docs.example.com/attn/guide/desktops-and-tiles",
    });
    let mut world = start(scripted().state(initial(dark, tile, Vec::new())));
    write(&mut world, BOXES);
    snap(&mut world, "browser-tile", dark);
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
/// waiting reviewer active beside a shell and a document, and two agents off screen; `hovered`,
/// with the pointer on its frame, which shows the width control.
fn shelf(dark: bool, hovered: bool) {
    let mut world = shelf_world(dark);
    if hovered {
        world.gesture(|app| {
            if let Some(frame) = app.runtime.find("shelf.frame") {
                app.move_to(frame);
            }
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

/// The audit's desktop: the waiting reviewer active beside a shell and a document, and two
/// agents off screen.
fn shelf_world(dark: bool) -> World {
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
    world
}

/// The shelf of [`shelf`] with the pointer resting on an entry, whose peek opens; with no shelf,
/// the title bar that left it no room.
fn shelf_peek(dark: bool) {
    let mut world = shelf_world(dark);
    world.resize((window().0.min(900.0), 600.0));
    let entry = ["w", "i", "sh", "r", "m"]
        .iter()
        .map(|leaf| format!("shelf.entry.{leaf}"))
        .find(|key| world.runtime().presented(key).is_some());
    if let Some(entry) = entry {
        world.gesture(|app| {
            let at = app.runtime.find(&entry).expect("the entry is laid out");
            app.move_to(at);
        });
    }
    world.advance(std::time::Duration::from_millis(400));
    snap(&mut world, "shelf-peek", dark);
}

/// The shelf of a row with two stacks, the second member of the second one in use between a
/// terminal, an approval and a document, beside a document and before an agent off screen; and
/// in a window too narrow for it, where the ends fold into counts.
fn shelf_stack(dark: bool) {
    let role = |builtin: &str| {
        Some(attn_protocol::SessionDelegationRole {
            name: builtin.into(),
            builtin: Some(builtin.into()),
            ..Default::default()
        })
    };
    let agents = [
        ("ch", "chief", "idle", role("orchestrator")),
        ("rv", "reviewer", "working", role("reviewer")),
        ("sc", "scout", "waiting_input", role("pathfinder")),
        ("bd", "builder", "working", role("builder")),
        ("ld", "land-on-main: verify perf", "pending_approval", None),
    ];
    let shells = [("ts", "tests"), ("sh", "shell"), ("bl", "builder logs")];
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    let sessions = state.sessions.get_or_insert_default();
    sessions.extend(agents.iter().map(|(id, label, status, role)| Session {
        label: (*label).into(),
        agent: "claude".into(),
        delegation_role: role.clone(),
        ..session(&format!("x-{id}"), status)
    }));
    sessions.extend(shells.map(|(id, label)| Session {
        label: label.into(),
        agent: "shell".into(),
        ..session(&format!("x-{id}"), "idle")
    }));
    if let Some(desktop) = state.desktops.iter_mut().flatten().next() {
        desktop.panes = agents
            .iter()
            .map(|(id, ..)| *id)
            .chain(shells.iter().map(|(id, _)| *id))
            .map(|id| agent_pane(DESKTOP, id, &format!("x-{id}")))
            .collect();
        desktop.active_pane_id = "bl".into();
        desktop.tree_json = row(vec![
            pane("ch"),
            below(pane("rv"), pane("ts")),
            pane("sh"),
            document_tile("m", "/repo/plan.md"),
            below(
                pane("bd"),
                below(
                    pane("bl"),
                    below(pane("ld"), document_tile("n", "/repo/notes.md")),
                ),
            ),
            pane("sc"),
        ])
        .to_string();
    }
    let mut world = start(scripted().state(state));
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    deliver_to(&mut world, "n", "/repo/notes.md", DOCUMENT, None);
    snap(&mut world, "shelf-stack", dark);
    world.resize((900.0, 600.0));
    snap(&mut world, "shelf-folded", dark);
}

/// The shelf of [`shelf`] and the label fewer tiles shows.
fn shelf_width_label(dark: bool) {
    let mut world = shelf_world(dark);
    run(&mut world, Action::ShelfFewerTiles);
    snap(&mut world, "shelf-width-label", dark);
}

/// Gives the review the keyboard as a click on its key line does: on "? all keys", which ends
/// the line's first row however the line fits, clear of the frame tabs.
fn focus_review(world: &mut World) {
    let [x, y, w, _] = world
        .screen()
        .bounds(Target::key("tile.m.review.keys"))
        .expect("the review's key line");
    world.gesture(|app| app.click_at((x + w - 12.0, y + 12.0)));
}

fn review_stop(dark: bool) {
    let mut world = review_alone(dark);
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
    garden_world_beside(dark, document_tile("m", "/repo/plan.md"), seeds, answers)
}

/// [`garden_world_and`] with `tile` beside terminal `a` in place of the document.
fn garden_world_beside(
    dark: bool,
    tile: Value,
    seeds: Vec<Seed>,
    answers: impl FnOnce(&Daemon),
) -> World {
    let daemon = scripted().state(initial(dark, tile, seeds.clone()));
    answers(&daemon);
    let known = seeds;
    daemon.on("seed_document_get", move |r: &Request| {
        let notes = if r["seed_id"] == "s-one" {
            vec![
                log_note(
                    "n-2",
                    "note",
                    "A menu row is 29 pt.",
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
    click(&mut world, Target::role("button").name("Expand the garden"));
    click(&mut world, Target::key("garden.row.s-plot"));
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
    click(&mut world, Target::role("button").name("Expand the garden"));
    click(&mut world, Target::key("garden.row.s-plot"));
    click(&mut world, Target::key("garden.row.s-one"));
    snap(&mut world, "garden-seed", dark);
}

fn garden_dock_seed(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::BoardOpen);
    click(&mut world, Target::key("garden.row.s-plot"));
    click(&mut world, Target::key("garden.row.s-one"));
    snap(&mut world, "garden-dock-seed", dark);
}

fn garden_empty(dark: bool) {
    let mut world = garden_world(dark, Vec::new());
    press(&mut world, Action::BoardOpen);
    click(&mut world, Target::role("button").name("Expand the garden"));
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
    click(&mut world, Target::key("garden.row.s-plot"));
    snap(&mut world, "garden-dock-plot", dark);
}

/// The garden as a board of lanes, expanded.
fn garden_board(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::BoardOpen);
    click(&mut world, Target::role("button").name("Expand the garden"));
    click(&mut world, Target::key("garden.view.board"));
    snap(&mut world, "garden-board", dark);
}

/// The board inside a plot it drilled into, the trail back on its own line, Tab on the first
/// of the view controls.
fn garden_board_plot(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::BoardOpen);
    world.gesture(|app| app.click(Target::role("button").name("Expand the garden")));
    world.gesture(|app| app.click(Target::key("garden.view.board")));
    world.gesture(|app| app.click(Target::key("garden.board.drill.s-plot")));
    tap(&mut world, Key::Tab);
    snap(&mut world, "garden-board-plot", dark);
}

fn garden_v2_world(dark: bool) -> World {
    let beds = vec![
        GardenBed {
            id: "b-attn01".into(),
            profile_id: PROFILE.into(),
            name: "attn".into(),
            slug: "attn".into(),
            note: Some("Desktop app, daemon & CLI".into()),
            repos: vec!["attn".into()],
            created_at: "2026-11-13T09:00:00Z".into(),
            updated_at: "2026-11-13T09:00:00Z".into(),
        },
        GardenBed {
            id: "b-rel001".into(),
            profile_id: PROFILE.into(),
            name: "relay".into(),
            slug: "relay".into(),
            note: Some("Cloud relay & gateway".into()),
            repos: vec!["cloud-relay".into()],
            created_at: "2026-11-13T09:00:00Z".into(),
            updated_at: "2026-11-13T09:00:00Z".into(),
        },
        GardenBed {
            id: "b-mob001".into(),
            profile_id: PROFILE.into(),
            name: "mobile".into(),
            slug: "mobile".into(),
            note: None,
            repos: vec!["mobile".into()],
            created_at: "2026-11-13T09:00:00Z".into(),
            updated_at: "2026-11-13T09:00:00Z".into(),
        },
    ];
    let v2_seeds = vec![
        Seed {
            id: "s-g7k2m1".into(),
            profile_id: PROFILE.into(),
            title: "Garden organization & Beds".into(),
            status: "growing".into(),
            claimed: true,
            bed_id: Some("b-attn01".into()),
            bed: Some("attn".into()),
            pulse: Some("Step 4 UI verified · awaiting final sign-off".into()),
            body: "Keep multi-project gardens calm with curated Beds, Now/Triage/All lenses, and one-line seed pulses.".into(),
            tender: Some(PartyView {
                name: "waiting_input agent".into(),
                r#ref: "session:s1".into(),
                session_id: Some("s1".into()),
            }),
            plot_progress: Some(SeedPlotProgress {
                total: 2,
                done: 1,
                growing: 1,
                ..SeedPlotProgress::default()
            }),
            created_at: "2026-11-13T08:00:00Z".into(),
            updated_at: "2026-11-13T09:18:00Z".into(),
            rev: 3,
            ..Seed::default()
        },
        Seed {
            id: "s-g7k2c1".into(),
            profile_id: PROFILE.into(),
            edges: vec![SeedEdge {
                kind: "part-of".into(),
                to: "s-g7k2m1".into(),
            }],
            title: "Curated Beds & repo auto-routing".into(),
            status: "harvested".into(),
            bed_id: Some("b-attn01".into()),
            bed: Some("attn".into()),
            created_at: "2026-11-13T08:05:00Z".into(),
            updated_at: "2026-11-13T08:45:00Z".into(),
            rev: 2,
            ..Seed::default()
        },
        Seed {
            id: "s-g7k2c2".into(),
            profile_id: PROFILE.into(),
            edges: vec![SeedEdge {
                kind: "part-of".into(),
                to: "s-g7k2m1".into(),
            }],
            title: "Calm List, Board & Dock UI".into(),
            status: "growing".into(),
            claimed: true,
            bed_id: Some("b-attn01".into()),
            bed: Some("attn".into()),
            pulse: Some("Visual polish verified in dark and light".into()),
            tender: Some(PartyView {
                name: "waiting_input agent".into(),
                r#ref: "session:s1".into(),
                session_id: Some("s1".into()),
            }),
            created_at: "2026-11-13T08:50:00Z".into(),
            updated_at: "2026-11-13T09:18:00Z".into(),
            rev: 1,
            ..Seed::default()
        },
        Seed {
            id: "s-r4n9p2".into(),
            profile_id: PROFILE.into(),
            title: "Home gateway reconnect storms".into(),
            status: "growing".into(),
            claimed: true,
            bed_id: Some("b-rel001".into()),
            bed: Some("relay".into()),
            pulse: Some("Waiting for approval to roll out prod relay".into()),
            tender: Some(PartyView {
                name: "pending_approval agent".into(),
                r#ref: "session:s2".into(),
                session_id: Some("s2".into()),
            }),
            created_at: "2026-11-13T07:30:00Z".into(),
            updated_at: "2026-11-13T09:05:00Z".into(),
            rev: 2,
            ..Seed::default()
        },
        Seed {
            id: "s-m3q8d1".into(),
            profile_id: PROFILE.into(),
            title: "TestFlight push notifications".into(),
            status: "growing".into(),
            claimed: true,
            bed_id: Some("b-mob001".into()),
            bed: Some("mobile".into()),
            pulse: Some("Building archive for build 142".into()),
            tender: Some(PartyView {
                name: "working agent".into(),
                r#ref: "session:s0".into(),
                session_id: Some("s0".into()),
            }),
            created_at: "2026-11-13T06:00:00Z".into(),
            updated_at: "2026-11-13T09:12:00Z".into(),
            rev: 1,
            ..Seed::default()
        },
        Seed {
            id: "s-f2w6b8".into(),
            profile_id: PROFILE.into(),
            title: "Worktree sweep edge case on detached HEAD".into(),
            status: "growing".into(),
            claimed: false,
            bed_id: Some("b-attn01".into()),
            bed: Some("attn".into()),
            created_at: "2026-11-08T09:00:00Z".into(),
            updated_at: "2026-11-08T09:00:00Z".into(),
            rev: 1,
            ..Seed::default()
        },
        Seed {
            id: "s-q5j3h7".into(),
            profile_id: PROFILE.into(),
            title: "Outpost binary auto-prune".into(),
            status: "planted".into(),
            ready: true,
            created_at: "2026-11-10T09:00:00Z".into(),
            updated_at: "2026-11-10T09:00:00Z".into(),
            rev: 1,
            ..Seed::default()
        },
        Seed {
            id: "s-a101x".into(),
            profile_id: PROFILE.into(),
            title: "Daily main review".into(),
            status: "growing".into(),
            claimed: true,
            automation_id: Some(7),
            pulse: Some("Reading 6 commits landed since yesterday".into()),
            created_at: "2026-11-13T09:00:00Z".into(),
            updated_at: "2026-11-13T09:15:00Z".into(),
            rev: 1,
            ..Seed::default()
        },
    ];
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), v2_seeds.clone());
    if let Some(ref mut map) = state.settings {
        map.insert("garden_v2_enabled".into(), "true".into());
    }
    state.garden_beds = Some(beds);
    let daemon = scripted().state(state);
    let known = v2_seeds;
    daemon.on("seed_document_get", move |r: &Request| {
        message(SeedDocumentGetResultMessage {
            request_id: r["request_id"].as_str().unwrap_or_default().into(),
            success: true,
            document: known
                .iter()
                .find(|s| s.id == r["seed_id"])
                .cloned()
                .map(|seed| SeedDocument {
                    seed,
                    notes_total: 1,
                    notes: vec![log_note(
                        "n-v2",
                        "note",
                        "Step 3 landed; native List, Board and Dock wired behind garden_v2_enabled.",
                        "2026-11-13T09:16:00Z",
                    )],
                    ..SeedDocument::default()
                }),
            ..SeedDocumentGetResultMessage::new()
        })
    });
    let mut world = start(daemon);
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    world
}

fn garden_v2_list(dark: bool) {
    let mut world = garden_v2_world(dark);
    press(&mut world, Action::BoardOpen);
    click(&mut world, Target::role("button").name("Expand the garden"));
    click(&mut world, Target::key("garden.row.s-g7k2m1"));
    snap(&mut world, "garden-v2-list", dark);
}

fn garden_v2_board(dark: bool) {
    let mut world = garden_v2_world(dark);
    press(&mut world, Action::BoardOpen);
    click(&mut world, Target::role("button").name("Expand the garden"));
    click(&mut world, Target::key("garden.view.board"));
    snap(&mut world, "garden-v2-board", dark);
}

fn garden_v2_dock(dark: bool) {
    let mut world = garden_v2_world(dark);
    press(&mut world, Action::BoardOpen);
    snap(&mut world, "garden-v2-dock", dark);
}

/// A seed whose agent runs on an outpost out of reach, Tab on its unavailable Resume.
fn garden_seed_resume_unreachable(dark: bool) {
    let mut seeds = seeds();
    let one = seeds
        .iter_mut()
        .find(|s| s.id == "s-one")
        .expect("the fixture holds s-one");
    one.continuation = Some(SeedContinuation {
        agent: "codex".into(),
        cwd: "/repo".into(),
        directory_state: "unreachable".into(),
        outpost: Some("studio".into()),
        resume_available: false,
        resume_reason: Some(
            "outpost \"studio\" is unreachable; Resume returns when it is back".into(),
        ),
        ..SeedContinuation::default()
    });
    let mut world = garden_world(dark, seeds);
    press(&mut world, Action::BoardOpen);
    world.gesture(|app| app.click(Target::role("button").name("Expand the garden")));
    world.gesture(|app| app.click(Target::key("garden.row.s-plot")));
    world.gesture(|app| app.click(Target::key("garden.row.s-one")));
    for _ in 0..80 {
        if world.screen().focused().and_then(|n| n.name).as_deref() == Some("Resume on studio") {
            break;
        }
        tap(&mut world, Key::Tab);
    }
    snap(&mut world, "garden-seed-resume-unreachable", dark);
}

/// The board with a card picked, its move menu open under it.
fn garden_board_menu(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::BoardOpen);
    click(&mut world, Target::role("button").name("Expand the garden"));
    click(&mut world, Target::key("garden.view.board"));
    click(&mut world, Target::key("garden.board.card.s-loose"));
    snap(&mut world, "garden-board-menu", dark);
}

/// A search in the docked garden whose answers fill the list and run past it.
fn garden_dock_search_full(dark: bool) {
    let many = (0..40)
        .map(|n| {
            seed(
                &format!("s-row{n:02}"),
                &format!("Every row of the list reads its state from one place, part {n}"),
                if n % 3 == 0 { "growing" } else { "planted" },
                None,
            )
        })
        .collect();
    let mut world = garden_world(dark, many);
    press(&mut world, Action::BoardOpen);
    click(&mut world, Target::key("garden.search.field"));
    world.gesture(|app| app.type_text("row"));
    snap(&mut world, "garden-dock-search-full", dark);
}

/// A search in the docked garden: its answers with what matched, then a filter being typed.
fn garden_dock_search(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::BoardOpen);
    click(&mut world, Target::key("garden.search.field"));
    world.gesture(|app| app.type_text("row"));
    snap(&mut world, "garden-dock-search", dark);
    world.gesture(|app| app.type_text(" is:"));
    snap(&mut world, "garden-dock-search-filter", dark);
}

/// Two plots named alike up to their region, each with a child "Verify the rollout" whose body
/// answers "smoke", and a long passage and a CJK one beside them, so each answer shows which seed
/// and plot it is.
fn body_answers() -> Vec<Seed> {
    let check = |id: &str, state: &str| SeedCheck {
        id: id.into(),
        state: state.into(),
        text: "The smoke test passes on the new build.".into(),
        ..SeedCheck::default()
    };
    vec![
        seed(
            "s-cache",
            "Roll out the cache to the production region in Tokyo 東京本番環境",
            "growing",
            None,
        ),
        seed(
            "s-queue",
            "Roll out the cache to the production region in Osaka 大阪本番環境",
            "growing",
            None,
        ),
        Seed {
            body: "Run the smoke test".into(),
            tender: Some(PartyView {
                name: "Builder".into(),
                r#ref: "session:s0".into(),
                session_id: Some("s0".into()),
            }),
            checks: Some(vec![check("C1", "confirmed"), check("C2", "open")]),
            ..seed("s-twinc", "Verify the rollout", "growing", Some("s-cache"))
        },
        Seed {
            body: "Run the smoke test".into(),
            ..seed("s-twinq", "Verify the rollout", "planted", Some("s-queue"))
        },
        Seed {
            body: "Before the switch, every region drains its old workers, and the deploy waits \
                   on the health endpoint for each of them. Then the smoke test runs against the \
                   canary for ten minutes, reading error rates, cold-start latency and the \
                   cache's hit ratio from the dashboards before anyone widens the rollout."
                .into(),
            ..seed(
                "s-canary",
                "Canary the new region before the whole fleet moves",
                "planted",
                Some("s-cache"),
            )
        },
        Seed {
            body: "切换之前，每个区域先排空旧的工作进程，部署会等待每个实例的健康检查通过。\
                   然后对金丝雀实例运行 smoke 测试十分钟，并从仪表盘读取错误率、冷启动延迟\
                   和缓存命中率，确认无误后才扩大发布范围。"
                .into(),
            ..seed("s-cjk", "区域发布检查", "planted", Some("s-queue"))
        },
    ]
}

/// A search answered from seeds' bodies, docked or expanded.
fn garden_search_body(dark: bool, expanded: bool) {
    let mut world = garden_world(dark, body_answers());
    press(&mut world, Action::BoardOpen);
    if expanded {
        click(&mut world, Target::role("button").name("Expand the garden"));
    }
    click(&mut world, Target::key("garden.search.field"));
    world.gesture(|app| app.type_text("smoke"));
    snap(
        &mut world,
        if expanded {
            "garden-search-body"
        } else {
            "garden-dock-search-body"
        },
        dark,
    );
}

/// A seed's page as a tile beside the agent.
fn garden_seed_tile(dark: bool) {
    let tile = json!({
        "type": "tile",
        "tile_id": "seed",
        "tile_kind": "seed",
        "tile_params": "s-one",
        "tile_session_id": "s0",
    });
    let mut world = garden_world_beside(dark, tile, seeds(), |_| {});
    snap(&mut world, "garden-seed-tile", dark);
}

fn settings_page(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::UiOpenSettings);
    click(&mut world, Target::role("button").name("Appearance"));
    snap(&mut world, "settings", dark);
}

/// The Agents page: its switches, the default agent and the seeds-on-standby choices.
fn settings_agents(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::UiOpenSettings);
    click(&mut world, Target::key("settings.nav.agents"));
    snap(&mut world, "settings-agents", dark);
}

/// The Agents page with Claude missing and Codex installed, the pointer resting on the dimmed
/// Claude option until its tooltip says why it is unavailable.
fn settings_agents_missing_hovered(dark: bool) {
    let mut world = settings_world(
        dark,
        "agents",
        "agents",
        &[("new_session_agent", "codex")],
        |_| {},
        |daemon| {
            let mut list = attn_testkit::built_in_harnesses();
            list[0].installation = "missing".into();
            daemon.set_harnesses(list);
        },
    );
    world.gesture(|app| {
        let claude = app
            .runtime
            .find("settings.default-agent.claude")
            .expect("the Claude option is laid out");
        app.move_to(claude);
    });
    world.advance(std::time::Duration::from_secs(2));
    snap(&mut world, "settings-agents-missing-hovered", dark);
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

/// The markdown opener with two outposts, ai-sandbox connected and field-lab lost: every
/// machine's recent files, each tagged with its machine and its latest touch, or, when `scoped`,
/// ai-sandbox's alone, its chip in the field.
fn opener_outposts(dark: bool, scoped: bool) {
    let outpost = |id: &str, name: &str, state: &str, reach: &str| OutpostInfo {
        id: id.into(),
        name: name.into(),
        destination: format!("{name}:2200"),
        state: state.into(),
        reach: reach.into(),
        state_since: "2026-11-13T08:00:00Z".into(),
        ..OutpostInfo::default()
    };
    let mut world = popover_world(
        dark,
        |state| {
            if let Some(settings) = state.settings.as_mut() {
                settings.insert("outposts_enabled".into(), "true".into());
            }
            state.outposts = Some(vec![
                outpost("outpost-ai", "ai-sandbox", "connected", "reachable"),
                outpost("outpost-field", "field-lab", "lost", "unreachable"),
            ]);
        },
        |daemon| {
            daemon.on("recent_files", |r: &Request| {
                let recent =
                    |path: &str, at: &str, source: &str, host: Option<&str>| FileActivity {
                        path: path.into(),
                        last_at: at.into(),
                        count: 1,
                        source: source.into(),
                        host: host.map(str::to_owned),
                        ..FileActivity::default()
                    };
                message(RecentFilesResultMessage {
                    request_id: r["request_id"].as_str().unwrap_or_default().into(),
                    success: true,
                    files: vec![
                        recent(
                            "/home/me/shop/docs/checkout.md",
                            "2026-11-12T08:56:00Z",
                            "edited",
                            Some("outpost-ai"),
                        ),
                        recent("/repo/plan.md", "2026-11-12T06:40:00Z", "open", None),
                        recent(
                            "/home/me/sensor-data/README.md",
                            "2026-11-11T16:00:00Z",
                            "open",
                            Some("outpost-field"),
                        ),
                        recent(
                            "/home/me/shop/notes/launch.md",
                            "2026-11-10T09:00:00Z",
                            "open",
                            Some("outpost-ai"),
                        ),
                        recent(
                            "/repo/docs/colors.md",
                            "2026-11-09T12:00:00Z",
                            "edited",
                            None,
                        ),
                    ],
                    ..RecentFilesResultMessage::new()
                })
            });
        },
    );
    press(&mut world, Action::FileOpen);
    if scoped {
        world.gesture(|app| app.press("shift+tab"));
        world.gesture(|app| app.press("shift+tab"));
    }
    let name = if scoped {
        "opener-outpost-scope"
    } else {
        "opener-outposts"
    };
    snap(&mut world, name, dark);
}

/// The prompt for the agent on screen's context window cap, from the palette.
fn context_cap(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::UiCommandPalette);
    world.gesture(|app| app.type_text("context window"));
    tap(&mut world, Key::Enter);
    snap(&mut world, "context-cap", dark);
}

/// A program asking to pair, with the longest real sender name, over the agent on screen; then
/// with Allow refused, in the smallest window at the largest text.
fn sender_pairing(dark: bool) {
    let refuse = |daemon: &Daemon| {
        daemon.on("sender_pair_decide", |_: &Request| {
            message(attn_protocol::SenderPairDecideResultMessage {
                success: false,
                error: Some(
                    "profile \"Work\" already has a sender named \"engineering-observability-staging-deploy-bot\"; remove it from Paired senders, then allow again"
                        .into(),
                ),
                ..attn_protocol::SenderPairDecideResultMessage::new()
            })
        });
    };
    let ask = |world: &mut World| {
        world.daemon.emit(attn_protocol::SendersUpdatedMessage {
            request: Some(attn_protocol::SenderPairingRequest {
                id: "pair-1".into(),
                name: "engineering-observability-staging-deploy-bot".into(),
                code: "4821".into(),
                requested_at: "2026-11-12T09:00:00Z".into(),
            }),
            ..attn_protocol::SendersUpdatedMessage::new()
        });
        world.settle();
    };
    let mut world = popover_world(dark, |_| {}, refuse);
    ask(&mut world);
    snap(&mut world, "sender-pairing", dark);

    let mut world = popover_world(
        dark,
        |state| {
            if let Some(settings) = state.settings.as_mut() {
                settings.insert("uiScale".into(), SMALLEST_TEXT.into());
            }
        },
        refuse,
    );
    world.resize((800.0, 600.0));
    ask(&mut world);
    click(&mut world, Target::key("sender-pairing.allow"));
    snap(&mut world, "sender-pairing-smallest-refused", dark);
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
        app.press(if attn_testkit::mac() {
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
    world.gesture(|app| app.click(Target::key("terminal.r-a.annotation.more")));
    snap(&mut world, "terminal-annotation-labels", dark);
    world.gesture(|app| app.click(Target::key("terminal.r-a.annotation.more")));
    world.gesture(|app| app.click(Target::key("terminal.r-a.annotation.write")));
    snap(&mut world, "terminal-annotation-comment", dark);
}

/// Terminal `a` whose conversation another terminal has open, running an older build than the
/// daemon, on a machine being reconnected: both notices over its top and the strip under it.
fn tile_notices(dark: bool) {
    let mut world = popover_world(
        dark,
        |state| {
            let first = first_session(state);
            first.conversation_shared_with = Some(vec!["s1".into()]);
            first.terminal_build_stale = Some(true);
            first.host = Some("studio".into());
            state.outposts = Some(vec![OutpostInfo {
                reach: "connecting".into(),
                ..outpost("studio", HOME_BUILD, None, None)
            }]);
        },
        |_| {},
    );
    snap(&mut world, "tile-notices", dark);
}

/// The window with the document tile beside terminal `a`, connected.
fn document_world(dark: bool, daemon: impl FnOnce(&Daemon)) -> World {
    let scripted = scripted().state(initial(
        dark,
        document_tile("m", "/repo/plan.md"),
        Vec::new(),
    ));
    daemon(&scripted);
    start(scripted)
}

/// The daemon stopped answering: the band under the title bar over the last facts, while the
/// window retries.
fn daemon_reconnecting(dark: bool) {
    let mut world = document_world(dark, |_| {});
    world.daemon.refuse_connections(true);
    world.daemon.disconnect();
    world.settle();
    snap(&mut world, "daemon-reconnecting", dark);
}

/// The daemon stopped, and starting it again failed: the band says why.
fn daemon_start_failed(dark: bool) {
    let mut world = document_world(dark, |_| {});
    world.daemon.fail_starts(Some(
        "no attnd beside /Applications/attn.app/Contents/MacOS/attn-app",
    ));
    world.daemon.stop();
    world.settle();
    world.advance(std::time::Duration::from_millis(1000));
    snap(&mut world, "daemon-start-failed", dark);
}

/// The daemon took the socket but never answered health, and starting it again failed: the band
/// says why and how to restart it by hand, the longest reason it shows.
fn daemon_unresponsive(dark: bool) -> World {
    let mut world = document_world(dark, |_| {});
    world.daemon.fail_starts(Some(
        "/home/user/.local/share/attn-ensure-paused/bin/attnd daemon ensure failed (exit status: 1): daemon ensure error: daemon pid 2875852 did not answer health (health_timeout=15s); left running; retry, or restart with `ATTN_INSTANCE=ensure-paused attn daemon stop` then `ATTN_INSTANCE=ensure-paused attn daemon ensure` in the same environment: Get \"http://127.0.0.1:41597/health\": context deadline exceeded (Client.Timeout exceeded while awaiting headers)",
    ));
    world.daemon.stop();
    world.settle();
    world.advance(std::time::Duration::from_millis(1000));
    snap(&mut world, "daemon-unresponsive", dark);
    world
}

/// The unresponsive daemon's band with its Details open, at a window too small for the band to
/// say it whole: the reason and both commands whole.
fn daemon_unresponsive_details(dark: bool) {
    let mut world = daemon_unresponsive(dark);
    if world
        .screen()
        .find(Target::key("connection.details"))
        .is_none()
    {
        return;
    }
    click(&mut world, Target::key("connection.details"));
    snap(&mut world, "daemon-unresponsive-details", dark);
}

/// A daemon on another protocol: the window says so and stops.
fn daemon_refused(dark: bool) {
    let state = InitialStateMessage {
        protocol_version: Some("0ld".into()),
        ..initial(dark, document_tile("m", "/repo/plan.md"), Vec::new())
    };
    let mut world = World::start_sized(scripted().state(state), window());
    snap(&mut world, "daemon-refused", dark);
}

/// The daemon on the app's port cannot reach the home it passes the app to.
fn home_unreachable(dark: bool) {
    let daemon = scripted();
    daemon.on("client_hello", |_| {
        json!({
            "event": "home_unreachable", "home": "oma", "state": "dialing",
            "reason": "ssh: connect to host oma port 22: No route to host",
        })
    });
    let mut world = World::start_sized(daemon, window());
    snap(&mut world, "home-unreachable", dark);
}

/// The app on a machine whose home refuses its newer build, with a shell of its own on the desk:
/// the update band alone says so, offering Update, with no band repeating it in daemon ids.
fn home_refused_build(dark: bool) {
    let home = scripted();
    home.on("client_hello", |_| {
        json!({
            "event": "home_unreachable", "home": "oma", "state": "refused", "refusal": "build",
            "reason": "daemon d-d2820b7d runs build 0.16.1 (source git:a25a05a, protocol e4f7a94); daemon d-6ec1676f runs build 0.16.0 (source git:9f0c1e2, protocol 77b0d3a)",
        })
    });
    let mut initial: InitialStateMessage = serde_json::from_value(json!({
        "event": "initial_state",
        "protocol_version": attn_protocol::PROTOCOL_VERSION,
        "daemon_instance_id": "laptop-daemon",
        "update_run": {
            "id": "h1", "install_kind": "home", "phase": "failed", "log_path": "/tmp/h1.log",
            "target_build": "0.16.1 (source git:a25a05a, protocol e4f7a94)",
            "requesters": [], "outposts": [
                {"id": "home", "name": "oma", "role": "home", "state": "done", "build": "0.16.0 (source git:9f0c1e2, protocol 77b0d3a)"},
                {"id": "laptop-id", "name": "laptop", "role": "app_machine", "state": "skipped",
                 "build": "0.16.1 (source git:a25a05a, protocol e4f7a94)", "detail": "oma runs another build", "action": "Update"},
            ],
        },
    }))
    .expect("an initial state");
    initial.sessions = Some(vec![attn_protocol::Session {
        id: "here".into(),
        label: "here".into(),
        agent: "shell".into(),
        state: "idle".into(),
        directory: "/tmp/here".into(),
        profile_id: "work".into(),
        ..attn_protocol::Session::default()
    }]);
    initial.terminal_bindings = Some(vec![attn_protocol::TerminalBinding {
        ask_id: None,
        session_id: "here".into(),
        terminal_id: "rl".into(),
    }]);
    let machine = Daemon::of_this_machine(&home).state(initial);
    let dir = tempfile::tempdir().expect("a folder for the app's data");
    drop(World::start_with_data_dir(
        scripted().state(InitialStateMessage {
            protocol_version: Some(attn_protocol::PROTOCOL_VERSION.into()),
            selected_profile_id: Some("work".into()),
            profiles: Some(vec![attn_protocol::Profile {
                id: "work".into(),
                name: "Work".into(),
                ..attn_protocol::Profile::default()
            }]),
            ..InitialStateMessage::new()
        }),
        dir.path().to_path_buf(),
    ));
    let mut world = World::start_relaying_in(
        home.relaying_to("laptop-id", "oma"),
        machine,
        window(),
        dir.path().to_path_buf(),
    );
    snap(&mut world, "home-refused-build", dark);
}

/// The app on a machine whose home runs on oma: the terminal's tile names oma, and Open in
/// editor says the folder is there instead of opening one here.
fn remote_home_desktop(dark: bool) {
    let daemon = scripted().relaying_to("mac", "oma").state(initial(
        dark,
        document_tile("m", "/repo/plan.md"),
        seeds(),
    ));
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
    press(&mut world, Action::UiCommandPalette);
    world.gesture(|app| app.type_text("open in editor"));
    world.settle();
    tap(&mut world, Key::Enter);
    world.settle();
    snap(&mut world, "remote-home-desktop", dark);
}

/// Browse beside the projects folder in Settings on a machine whose home runs on oma: the
/// launcher lists oma's folders instead of the system's dialog opening this machine's.
fn remote_home_folder_chooser(dark: bool) {
    let daemon = scripted().relaying_to("mac", "oma").state(initial(
        dark,
        document_tile("m", "/repo/plan.md"),
        seeds(),
    ));
    daemon
        .on("get_recent_locations", |_: &Request| {
            message(RecentLocationsResultMessage {
                success: true,
                home_path: Some("/home/me".into()),
                ..RecentLocationsResultMessage::new()
            })
        })
        .on("browse_directory", |r: &Request| {
            let input = r["input_path"].as_str().unwrap_or_default().to_owned();
            message(BrowseDirectoryResultMessage {
                success: true,
                input_path: input,
                home_path: Some("/home/me".into()),
                directory: "/home/me".into(),
                entries: ["attn-notebook", "code", "projects", "scratch"]
                    .iter()
                    .map(|name| DirectoryEntry {
                        name: (*name).into(),
                        path: format!("/home/me/{name}"),
                        is_dir: true,
                        is_repo: Some(false),
                    })
                    .collect(),
                ..BrowseDirectoryResultMessage::new()
            })
        });
    let mut world = start(daemon);
    press(&mut world, Action::UiOpenSettings);
    click(&mut world, Target::key("settings.nav.desktop"));
    click(
        &mut world,
        Target::key("settings.browse.projects_directory"),
    );
    snap(&mut world, "remote-home-folder-chooser", dark);
}

/// A command that failed: the notice at the window's foot, at its end.
fn notice_failed(dark: bool) {
    let mut world = with_document_and(dark, |daemon| {
        daemon.on("desktop_create", |r: &Request| {
            json!({
                "event": "profile_action_result",
                "request_id": r["request_id"],
                "action": "desktop_create",
                "success": false,
                "error": "no room",
            })
        });
    });
    press(&mut world, Action::UiCommandPalette);
    world.gesture(|app| app.type_text("new desktop"));
    world.settle();
    tap(&mut world, Key::Enter);
    world.settle();
    snap(&mut world, "notice-failed", dark);
}

/// The first key of a sequence pressed: the keys it waits for at the window's foot.
fn leader(dark: bool) {
    let palette = match Keymap::from_setting(None, attn_testkit::platform())
        .binding(&Action::UiCommandPalette)
    {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("the palette has no single chord: {other:?}"),
    };
    let key = match &palette.key {
        Key::Command(c) => c.to_string(),
        Key::Text(t) => t.clone(),
        other => panic!("no stored key for {other:?}"),
    };
    let leads = json!({
        "key": key,
        "meta": true,
        "alt": palette.mods.alt,
        "shift": palette.mods.shift,
    });
    let config = json!({
        "version": 1,
        "overrides": {"ui.showShortcuts": {"leader": leads, "then": {"key": "j"}}},
        "dock": {"collapsed": false, "items": []},
    });
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), Vec::new());
    state
        .settings
        .get_or_insert_with(BTreeMap::new)
        .insert("keybindings_config".into(), config.to_string());
    let mut world = start(scripted().state(state));
    world.gesture(|app| app.key(palette.clone()));
    snap(&mut world, "leader", dark);
}

/// An agent's callout pointing at the sidebar: the ring around it and the card beside it.
fn callout(dark: bool) {
    let mut world = document_world(dark, |_| {});
    world.daemon.emit(CalloutShowRequestMessage {
        request_id: "c1".into(),
        profile_id: PROFILE.into(),
        from: "Fern".into(),
        steps: vec![
            CalloutStep {
                anchor: "sidebar".into(),
                text: "Your agents wait here, the ones that need you first.".into(),
                ..CalloutStep::default()
            },
            CalloutStep {
                anchor: "garden".into(),
                text: "The garden holds the work they tend.".into(),
                ..CalloutStep::default()
            },
        ],
        ..CalloutShowRequestMessage::new()
    });
    world.settle();
    snap(&mut world, "callout", dark);
}

/// Terminal `a`'s filter over a block's output, typed into, listing the lines that match.
fn terminal_filter(dark: bool) {
    let mut world = plain(dark);
    let output: String = (1..=40)
        .map(|n| format!("test desktop::view::tile_{n} ... ok\r\n"))
        .collect();
    write(
        &mut world,
        &format!(
            "\x1b]133;A\x07$ \x1b]133;B\x07cargo test\r\n\x1b]133;C\x07{output}\x1b]133;D;0\x07\x1b]133;A\x07$ "
        ),
    );
    let at = cell(&world, 1, 2);
    world.gesture(|app| {
        app.runtime.pointer_move(at.0, at.1);
        app.runtime
            .button_down(at.0, at.1, rune::PointerButton::Secondary);
        app.runtime
            .button_up(at.0, at.1, rune::PointerButton::Secondary);
    });
    world.gesture(|app| app.runtime.resolve_popup(Some("filter-block")).unwrap());
    world.gesture(|app| app.type_text("tile_1"));
    snap(&mut world, "terminal-filter", dark);
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
    launcher_serving(launcher_state(dark, empty, launcher_2))
}

fn launcher_state(dark: bool, empty: bool, launcher_2: bool) -> InitialStateMessage {
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
            ("projects_directory", "/home/me/projects"),
            (
                "new_session_destination_local_/home/me/projects/shop",
                "main_repo",
            ),
        ] {
            settings.insert(key.into(), value.into());
        }
    }
    state
}

fn launcher_serving(state: InitialStateMessage) -> Daemon {
    let daemon = scripted().state(state);
    daemon
        .on("delegation_preferences_get", |_: &Request| {
            message(DelegationPreferencesResultMessage {
                success: true,
                preferences: Some(DelegationPreferences::default()),
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

/// Six recents and Up from the first onto the last, which the list scrolls to when it is
/// shorter than its rows.
fn launcher_dialog_last(dark: bool) {
    let daemon = launcher_daemon(dark, false);
    daemon.on("get_recent_locations", |_: &Request| {
        message(RecentLocationsResultMessage {
            success: true,
            home_path: Some("/home/me".into()),
            recent_locations: ["shop", "api", "site", "docs", "infra", "mobile"]
                .iter()
                .map(|name| RecentLocation {
                    path: format!("/home/me/projects/{name}"),
                    last_seen: "2026-11-13T08:00:00Z".into(),
                    use_count: 1,
                    ..RecentLocation::default()
                })
                .collect(),
            ..RecentLocationsResultMessage::new()
        })
    });
    let mut world = start(daemon);
    press(&mut world, Action::SessionNew);
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Up,
            mods: Mods::default(),
        })
    });
    snap(&mut world, "launcher-dialog-last", dark);
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

/// Launcher 2.0 after a click on the YOLO switch's caption, which turns it on.
fn launcher_yolo_caption(dark: bool) {
    let mut world = start(launcher_daemon(dark, false));
    press(&mut world, Action::SessionNew);
    world.gesture(|app| {
        let [x, y, w, h] = app
            .runtime
            .elements()
            .into_iter()
            .find(|e| e.text.as_deref() == Some("YOLO"))
            .expect("the YOLO caption is drawn")
            .visible;
        app.click_at((x + w / 2.0, y + h / 2.0));
        app.move_to((10.0, 10.0));
    });
    snap(&mut world, "launcher-yolo-caption", dark);
}

/// Launcher 2.0 on a recent on ai-sandbox, which lacks Claude, the harness chosen.
fn launcher_missing_harness(dark: bool) {
    const SANDBOX: &str = "outpost-ai";
    let mut state = launcher_state(dark, false, true);
    state.outposts = Some(vec![OutpostInfo {
        id: SANDBOX.into(),
        name: "ai-sandbox".into(),
        destination: "ai-sandbox:2200".into(),
        state: "connected".into(),
        reach: "reachable".into(),
        state_since: "2026-11-13T08:00:00Z".into(),
        ..OutpostInfo::default()
    }]);
    if let Some(settings) = state.settings.as_mut() {
        settings.insert("outposts_enabled".into(), "true".into());
    }
    let daemon = launcher_serving(state);
    daemon
        .on("get_recent_locations", |_: &Request| {
            message(RecentLocationsResultMessage {
                success: true,
                home_path: Some("/home/me".into()),
                recent_locations: vec![RecentLocation {
                    host: Some(SANDBOX.into()),
                    path: SHOP.into(),
                    repo_root: Some(SHOP.into()),
                    last_seen: "2026-11-13T08:00:00Z".into(),
                    use_count: 1,
                }],
                ..RecentLocationsResultMessage::new()
            })
        })
        .on("agent_catalog", |r: &Request| {
            let machine = r["host"].as_str().unwrap_or_default().to_owned();
            message(AgentCatalogResultMessage {
                success: true,
                host: machine.clone(),
                catalog: Some(AgentCatalogResult {
                    machine,
                    harnesses: ["claude", "codex"]
                        .iter()
                        .map(|id| Harness {
                            id: (*id).into(),
                            name: (*id).into(),
                            installation: if *id == "codex" {
                                "installed"
                            } else {
                                "missing"
                            }
                            .into(),
                            ..Harness::default()
                        })
                        .collect(),
                    ..AgentCatalogResult::default()
                }),
                ..AgentCatalogResultMessage::new()
            })
        });
    let mut world = start(daemon);
    press(&mut world, Action::SessionNew);
    snap(&mut world, "launcher-missing-harness", dark);
}

/// What the app shows for `name` the daemon sent: the name itself, or in a long run the name
/// made long.
fn shown(name: &str) -> String {
    if mode().long {
        format!("{name}-{LONG_NAME}")
    } else {
        name.to_owned()
    }
}

/// Launcher 2.0 with two outposts, ai-sandbox connected and field-lab lost, and recents on
/// both and on this machine, in its dialog or, when `inline`, on an empty desktop. With `typed`,
/// browsing that folder on ai-sandbox.
fn launcher_outposts(dark: bool, inline: bool, typed: Option<&str>) {
    launcher_outposts_from(dark, inline, typed, false);
}

/// [`launcher_outposts`] in its dialog on a machine whose home runs on oma: the home's recents
/// name oma where they would name This Mac.
fn remote_home_launcher(dark: bool) {
    launcher_outposts_from(dark, false, None, true);
}

fn launcher_outposts_from(dark: bool, inline: bool, typed: Option<&str>, relaying: bool) {
    let outpost = |id: &str, name: &str, state: &str, reach: &str| OutpostInfo {
        id: id.into(),
        name: name.into(),
        destination: format!("{name}:2200"),
        state: state.into(),
        reach: reach.into(),
        state_since: "2026-11-13T08:00:00Z".into(),
        ..OutpostInfo::default()
    };
    let mut state = launcher_state(dark, inline, true);
    state.outposts = Some(vec![
        outpost("outpost-ai", "ai-sandbox", "connected", "reachable"),
        outpost("outpost-field", "field-lab", "lost", "unreachable"),
    ]);
    if let Some(settings) = state.settings.as_mut() {
        settings.insert("outposts_enabled".into(), "true".into());
    }
    let daemon = launcher_serving(state);
    let daemon = if relaying {
        daemon.relaying_to("mac", "oma")
    } else {
        daemon
    };
    daemon
        .on("get_recent_locations", |_: &Request| {
            let recent = |host: Option<&str>, path: &str, repo: bool| RecentLocation {
                host: host.map(str::to_owned),
                path: path.into(),
                repo_root: repo.then(|| path.into()),
                last_seen: "2026-11-13T08:00:00Z".into(),
                use_count: 1,
            };
            message(RecentLocationsResultMessage {
                success: true,
                home_path: Some("/home/me".into()),
                recent_locations: vec![
                    recent(Some("outpost-ai"), SHOP, true),
                    recent(None, SHOP, true),
                    recent(Some("outpost-field"), "/home/me/sensor-data", false),
                    recent(None, "/home/me/notes", false),
                ],
                ..RecentLocationsResultMessage::new()
            })
        })
        .on("agent_catalog", |r: &Request| {
            let machine = r["host"].as_str().unwrap_or_default().to_owned();
            message(AgentCatalogResultMessage {
                success: true,
                host: machine.clone(),
                catalog: Some(AgentCatalogResult {
                    machine,
                    harnesses: ["claude", "codex"]
                        .iter()
                        .map(|id| Harness {
                            id: (*id).into(),
                            name: (*id).into(),
                            installation: "installed".into(),
                            ..Harness::default()
                        })
                        .collect(),
                    ..AgentCatalogResult::default()
                }),
                ..AgentCatalogResultMessage::new()
            })
        });
    let mut world = start(daemon);
    if !inline {
        press(&mut world, Action::SessionNew);
    }
    let name = match typed {
        Some(folder) => {
            world.gesture(|app| app.type_text(&format!("{}:{folder}", shown("ai-sandbox"))));
            world.advance(std::time::Duration::from_millis(200));
            "launcher-outposts-browse"
        }
        None if relaying => "remote-home-launcher",
        None if inline => "launcher-outposts-inline",
        None => "launcher-outposts",
    };
    snap(&mut world, name, dark);
}

/// The classic picker's new worktree in a repository whose default and current branches differ
/// only at their ends; with `worktrees` to open, scrolled down to the last of them.
fn picker_start_from(dark: bool, worktrees: usize) {
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
        .on("get_repo_info", move |_: &Request| {
            message(GetRepoInfoResultMessage {
                success: true,
                info: Some(RepoInfo {
                    repo: SHOP.into(),
                    current_branch: "release/customer-platform-2026.21".into(),
                    default_branch: "release/customer-platform-2026.12".into(),
                    worktrees: (1..=worktrees)
                        .map(|i| {
                            let branch = format!("release/customer-platform-2026.21-hotfix-{i}");
                            Worktree {
                                path: format!("{SHOP}--hotfix-{i}"),
                                main_repo: SHOP.into(),
                                branch,
                                ..Worktree::default()
                            }
                        })
                        .collect(),
                    ..RepoInfo::default()
                }),
                ..GetRepoInfoResultMessage::new()
            })
        });
    let mut world = start(daemon);
    press(&mut world, Action::SessionNew);
    world.gesture(|app| app.type_text("shop"));
    world.advance(std::time::Duration::from_millis(200));
    tap(&mut world, Key::Enter);
    if worktrees == 0 {
        snap(&mut world, "picker-start-from", dark);
        return;
    }
    for _ in 0..=worktrees {
        tap(&mut world, Key::Down);
    }
    snap(&mut world, "picker-start-from-worktrees", dark);
}

/// The classic new-session picker, Launcher 2.0 off: its harness toggle and its key footer.
fn picker_dialog(dark: bool) {
    let mut world = start(launcher_daemon_with(dark, false, false));
    press(&mut world, Action::SessionNew);
    snap(&mut world, "picker-dialog", dark);
}

/// The classic picker with no harness ever picked, after Enter: the toggle asks for one.
fn picker_no_harness(dark: bool) {
    let mut state = launcher_state(dark, false, false);
    if let Some(settings) = state.settings.as_mut() {
        settings.remove("new_session_agent");
    }
    let daemon = launcher_serving(state);
    daemon.on("inspect_path", |r: &Request| {
        let path = r["path"].as_str().unwrap_or_default().to_owned();
        message(InspectPathResultMessage {
            success: true,
            inspection: Some(PathInspection {
                exists: true,
                is_directory: true,
                input_path: path.clone(),
                resolved_path: path,
                home_path: Some("/home/me".into()),
                ..PathInspection::default()
            }),
            ..InspectPathResultMessage::new()
        })
    });
    let mut world = start(daemon);
    press(&mut world, Action::SessionNew);
    world.settle();
    world.gesture(|app| app.press("enter"));
    world.settle();
    snap(&mut world, "picker-no-harness", dark);
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
    click(&mut world, Target::key("settings.search.field"));
    world.gesture(|app| app.type_text("analytics"));
    click(&mut world, Target::key("settings.nav.analytics"));
    snap(&mut world, "settings-analytics", dark);
}

/// Analytics with a new vent of the longest real text and a done one, sharing on and its
/// status read: the list with its chosen vent, then the machine settings open. `smallest` shows
/// it at 800x600 at the largest text, top and bottom.
/// A vent written in Japanese, long enough to run the selected row's column past a screen; its
/// scene also has sharing refused, the review's worst case for Send.
const LONG_CJK_VENT: &str = "`attn seed tend` が拒否されました。シードは二時間前に終了したセッションが保持しており、`attn seed park` には担当者のセッション ID が必要ですが、拒否メッセージにはその ID がどこにも書かれていません。社内プラットフォームの作業ツリー /Users/someone/プロジェクト/社内プラットフォーム/InternationalEngineeringObservability で docs/internal/garden.md と CLI のソースを読み、古い担当はセッションが閉じられたときにだけ外れることをやっと見つけました。次の担当者が同じ調査を繰り返さないように、拒否メッセージに担当者のセッションと、それを閉じる方法を書いてほしいです。";

/// Which vent the populated Analytics page has chosen.
#[derive(Clone, Copy, PartialEq)]
enum Chosen {
    Usual,
    /// The long vent in Japanese, with sharing refused.
    Japanese,
    /// A tool vent past the length that folds, opened with Show all.
    Folded,
}

fn settings_analytics_populated(dark: bool, smallest: bool) {
    analytics_populated(dark, smallest, Chosen::Usual);
}

/// The populated page at the regular size with a vent past the folding length opened with
/// Show all.
fn settings_analytics_show_all(dark: bool) {
    analytics_populated(dark, false, Chosen::Folded);
}

/// The populated page at the regular size with a long vent in Japanese chosen, its Send under
/// the vent's text.
fn settings_analytics_cjk(dark: bool) {
    analytics_populated(dark, false, Chosen::Japanese);
}

fn analytics_populated(dark: bool, smallest: bool, chosen: Chosen) {
    let cjk = chosen == Chosen::Japanese;
    let folded = "The daemon refused to write the settings after the user opened Analytics while its outpost was unreachable. ".repeat(6);
    let build = attn_protocol::AnalyticsBuild {
        version: "0.42.1".into(),
        commit: "a3f9e1c22".into(),
        channel: "source".into(),
        dirty: true,
        os: "darwin".into(),
        arch: "arm64".into(),
        ..attn_protocol::AnalyticsBuild::default()
    };
    let vent = |id: &str, label: &str, text: &str, done: bool| attn_protocol::Vent {
        id: id.into(),
        session_id: "s1".into(),
        kind: "attn".into(),
        text: text.into(),
        created_at: "2026-11-19T14:02:11Z".into(),
        dismissed_at: done.then(|| "2026-11-19T15:00:00Z".into()),
        context: attn_protocol::VentContext {
            session_label: label.into(),
            harness: "claude".into(),
            harness_version: Some("2.4.1".into()),
            model: Some("claude-opus-5-5".into()),
            model_requested: Some("opus".into()),
            build: build.clone(),
            experimental: vec!["asks_enabled".into(), "sigma_enabled".into()],
            ..attn_protocol::VentContext::default()
        },
        ..attn_protocol::Vent::default()
    };
    let mut settings = vec![
        ("analytics.enabled", "true"),
        ("analytics.sharing_enabled", "true"),
    ];
    if smallest {
        settings.push(("uiScale", "1.5"));
    }
    let mut world = settings_world(
        dark,
        "analytics",
        "analytics",
        &settings,
        |state| {
            state.vents = Some(vec![
                vent(
                    "v-new",
                    "engineering observability staging review of the garden bell staleness",
                    if cjk {
                        LONG_CJK_VENT
                    } else if chosen == Chosen::Folded {
                        &folded
                    } else {
                        "`attn seed tend s-f68sja` refused: the seed is held by a session that ended two hours ago, and attn seed park needs the tender's session id, which the refusal never names. I had to read docs/internal/garden.md and the CLI source at /Users/someone/projects/victor/attn--a-worktree-with-a-long-name/cli/src/seed/tend.rs to find that a stale claim clears only when its session is closed."
                    },
                    false,
                ),
                vent(
                    "v-done",
                    "songcheck deploy",
                    "kubectl cannot reach the cluster.",
                    true,
                ),
            ]);
        },
        move |daemon| {
            daemon.on("get_analytics_status", move |r: &Request| {
                message(attn_protocol::GetAnalyticsStatusResultMessage {
                    request_id: r["request_id"].as_str().unwrap_or_default().into(),
                    success: true,
                    records: 40,
                    bytes: 4096,
                    waiting: 12,
                    last_sent_at: Some("2026-11-19T14:00:12Z".into()),
                    ingest_url: "https://attn-telemetry.caketroll.cloud".into(),
                    last_refusal: cjk.then(|| {
                        "Analytics upload returned 413: this outpost exceeded the server's request size limit. 接続設定を確認してから同期を再試行してください。".into()
                    }),
                    ..attn_protocol::GetAnalyticsStatusResultMessage::new()
                })
            });
        },
    );
    let name = if smallest {
        "settings-smallest-analytics"
    } else if cjk {
        "settings-analytics-cjk"
    } else if chosen == Chosen::Folded {
        "settings-analytics-show-all"
    } else {
        "settings-analytics-populated"
    };
    if smallest {
        world.resize((800.0, 600.0));
    }
    if chosen == Chosen::Folded {
        world.gesture(|app| app.click(Target::key("settings.analytics.vent.more")));
    }
    snap(&mut world, name, dark);
    if chosen != Chosen::Usual {
        return;
    }
    tap(&mut world, Key::Text("m".into()));
    world.gesture(|app| {
        app.update(|_, tx| tx.scroll_into_view_in("settings.body", "settings.analytics.machine"))
    });
    snap(&mut world, &format!("{name}-machine"), dark);
    if smallest {
        world.gesture(|app| app.update(|_, tx| tx.scroll_to_bottom_instant("settings.body")));
        snap(&mut world, &format!("{name}-bottom"), dark);
    }
}

/// Outposts and Launcher 2.0 turned on, so their pages show their on switches.
const SWITCHED_ON: &[(&str, &str)] = &[
    ("outposts_enabled", "true"),
    ("new_session_launcher_enabled", "true"),
];

/// A Settings page with its buttons, fields and switches: the settings `settings` set and the
/// daemon answering as `answer` scripts it, reached through the settings search for `search`.
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

/// Settings → Machines with studio chosen and its move checked, then the band of the move once
/// it copies to studio.
fn settings_machines_move(dark: bool) {
    let build = "0.13.0 (source git:home, protocol current)";
    let mut world = settings_world(
        dark,
        "machines",
        "machines",
        &[
            ("outposts_enabled", "true"),
            ("remote_home_enabled", "true"),
        ],
        |state| {
            state.home_build = Some(build.into());
            state.outposts = Some(vec![OutpostInfo {
                id: "studio".into(),
                name: "studio".into(),
                destination: "me@studio".into(),
                state: "connected".into(),
                reach: "reachable".into(),
                state_since: "2026-10-18T18:00:00Z".into(),
                build: Some(build.into()),
                ..OutpostInfo::default()
            }]);
        },
        |daemon| {
            daemon.on("home_move_status", |_: &Request| {
                json!({"event": "home_move_result", "success": true, "home_move": {"step": "none", "checks": [], "remaps": []}})
            });
            daemon.on("home_move", |r: &Request| {
                (r["check"] == true).then(|| {
                    json!({"event": "home_move_result", "success": true, "home_move": {
                        "step": "checked", "to": "studio", "sleeps": true,
                        "checks": [
                            {"name": "connected", "ok": true}, {"name": "build", "ok": true},
                            {"name": "gh", "ok": true}, {"name": "disk", "ok": true},
                        ],
                        "remaps": [{"from": "/Users/me/attn-notebook", "to": "/home/me/attn-notebook"}],
                    }})
                })
            });
        },
    );
    click(&mut world, Target::key("settings.machine.studio"));
    click(&mut world, Target::key("settings.machine.studio.move"));
    snap(&mut world, "settings-machines-move", dark);
    click(&mut world, Target::key("settings.machine.studio.move.go"));
    let request_id = world.daemon.sent_of("home_move")[1]["request_id"].clone();
    world
        .daemon
        .emit(json!({"event": "home_move_progress", "request_id": request_id, "id": "m-1", "step": "copy"}));
    world.settle();
    snap(&mut world, "settings-machines-moving", dark);
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        })
    });
    world.settle();
    snap(&mut world, "home-move-band", dark);
}

/// Settings → Machines removing laptop, a machine that dials the home and is out of reach, with
/// one of its terminals open, so the remove waits on its confirm and its note.
fn settings_machines_remove_dial_in(dark: bool) {
    let mut world = settings_world(
        dark,
        "machines",
        "machines",
        SWITCHED_ON,
        |state| {
            state.outposts = Some(vec![OutpostInfo {
                id: "laptop".into(),
                name: "laptop".into(),
                state: "refused".into(),
                reach: "unreachable".into(),
                state_since: "2026-10-18T18:00:00Z".into(),
                dials_home: Some(true),
                ..OutpostInfo::default()
            }]);
            state.sessions.get_or_insert_with(Vec::new).push(Session {
                id: "laptop-shell".into(),
                label: "laptop shell".into(),
                agent: "shell".into(),
                state: "idle".into(),
                profile_id: "work".into(),
                host: Some("laptop".into()),
                ..Session::default()
            });
        },
        |_| {},
    );
    click(&mut world, Target::key("settings.machine.laptop"));
    click(&mut world, Target::key("settings.machine.laptop.remove"));
    snap(&mut world, "settings-machines-remove-dial-in", dark);
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
    click(&mut world, Target::key("settings.search.field"));
    world.gesture(|app| app.type_text(search));
    click(&mut world, Target::key(format!("settings.nav.{section}")));
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
        daemon.set_harnesses(vec![attn_testkit::built_in_harnesses().remove(0)]);
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

/// The daemon's answer to the report's request for its evidence, with nothing to report.
fn snapshot_answer(daemon: &Daemon) {
    daemon.on("support_snapshot", |_: &Request| {
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
}

/// The diagnostic report's choices, its Save the primary action.
fn diagnostic_report(dark: bool) {
    let mut world = with_document_and(dark, snapshot_answer);
    run(&mut world, Action::CreateDiagnosticReport);
    snap(&mut world, "diagnostic-report", dark);
}

/// A daemon listing one plugin whose provider fails, under a long name, and a folder that failed
/// to load.
fn failing_plugin(daemon: &Daemon) {
    daemon.on("list_plugins", |_| {
            message(attn_protocol::PluginsUpdatedMessage {
                plugins: vec![attn_protocol::PluginInfo {
                    name: "services-pilot-worktrees-with-a-long-provider-name".into(),
                    version: "0.1.0".into(),
                    dir: "/Users/someone/src/attn-plugins/services-pilot-worktrees-with-a-long-provider-name".into(),
                    priority: 10,
                    runtime_state: "failed".into(),
                    health_status: Some("unhealthy".into()),
                    last_exit: Some("exit status 1: could not reach the provider's API at https://pilot.example.com/v2/worktrees after 3 attempts".into()),
                    can_uninstall: true,
                    ..attn_protocol::PluginInfo::default()
                }],
                issues: Some(vec![attn_protocol::PluginIssue {
                    path: "/Users/someone/src/attn-plugins/broken-plugin/attn-plugin.json".into(),
                    error: "manifest names a harness that no binary on PATH provides".into(),
                }]),
                ..attn_protocol::PluginsUpdatedMessage::new()
            })
        });
}

/// The Plugins page in the smallest window at the largest text, after the daemon refused a
/// priority typed into the plugin's field.
fn settings_plugin_priority_refused(dark: bool) {
    let mut world = settings_world(
        dark,
        "plugins",
        "plugins",
        &[("uiScale", "1.5")],
        |_| {},
        |daemon| {
            failing_plugin(daemon);
            daemon.on("set_plugin_priority", |r| {
                message(attn_protocol::PluginActionResultMessage {
                    action: "set_priority".into(),
                    name: r["name"].as_str().map(str::to_owned),
                    success: false,
                    error: Some("plugin priority must be between -1000000 and 1000000".into()),
                    ..attn_protocol::PluginActionResultMessage::new()
                })
            });
        },
    );
    world.resize((800.0, 600.0));
    let field = "settings.plugin.services-pilot-worktrees-with-a-long-provider-name.priority";
    world.gesture(|app| app.update(|_, tx| tx.scroll_into_view(field)));
    world.gesture(|app| app.click(Target::key(field)));
    for key in [Key::End, Key::Backspace, Key::Backspace] {
        world.gesture(|app| {
            app.key(KeyEvent {
                key,
                mods: Mods::default(),
            })
        });
    }
    world.gesture(|app| app.type_text("1000001"));
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Enter,
            mods: Mods::default(),
        })
    });
    snap(
        &mut world,
        "settings-smallest-plugins-priority-refused",
        dark,
    );
}

/// The diagnostic report in the smallest window at the largest text, after the person cancelled
/// the save picker: why the save failed shows, though it lands below the pane list.
fn diagnostic_report_save_failed(dark: bool) {
    let mut world = popover_world(
        dark,
        |state| {
            if let Some(settings) = state.settings.as_mut() {
                settings.insert("uiScale".into(), SMALLEST_TEXT.into());
            }
        },
        snapshot_answer,
    );
    world.resize((800.0, 600.0));
    world.use_folders(attn_app::Folders::default());
    run(&mut world, Action::CreateDiagnosticReport);
    world.gesture(|app| app.click(Target::key("diagnostic-report.save")));
    world.answer_file_dialog(None);
    snap(&mut world, "diagnostic-report-save-failed", dark);
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
    unfold_tree(&mut world, "notebook");
    click(&mut world, Target::key("notebook.file.todo.md"));
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
    unfold_tree(&mut world, "notebook.tile.n");
    click(&mut world, Target::key("notebook.tile.n.file.index.md"));
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

/// The editor tile's find bar over its open note, with a query that matches.
fn notebook_tile_find(dark: bool) {
    let mut world = notebook_tile_open(dark);
    click(&mut world, Target::key("notebook.tile.n.editor"));
    world.gesture(|app| app.press("mod+f"));
    world.settle();
    world.gesture(|app| app.type_text("note"));
    world.settle();
    snap(&mut world, "notebook-tile-find", dark);
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
    unfold_tree(&mut world, "notebook");
    click(&mut world, Target::key("notebook.file.index.md"));
    snap(&mut world, "notebook-note", dark);
}

/// A long note: a title, then sixty sections of a heading and a paragraph.
fn long_note() -> String {
    let sections: String = (1..=60)
        .map(|i| format!("## Step {i}\n\nWhat step {i} changes, and why it comes here.\n\n"))
        .collect();
    format!("---\ntype: note\n---\n# The plan\n\n{sections}")
}

#[derive(Clone, Copy)]
enum Failure {
    Conflict,
    WriteError,
    LongWriteError,
}

/// `index.md`, a long note, in the fullscreen notebook or editor tile `n` alone, scrolled far
/// down and typed into there, after the save of that edit found the file changed on disk or could
/// not write it, with a short error or one naming a long nested path.
fn notebook_failed_save(dark: bool, tile: bool, failure: Failure) {
    let daemon = notebook_daemon(dark, (!tile).then(|| document_tile("m", "/repo/plan.md")));
    daemon
        .on("fs_read", |req: &Request| {
            json!({ "event": "fs_read_result", "success": true,
                "result": { "path": req["path"], "content": long_note(), "hash": "h1" } })
        })
        .on("fs_write", move |req: &Request| match failure {
            Failure::Conflict => json!({ "event": "fs_write_result", "success": true,
                "result": { "path": req["path"], "conflict": true, "current_hash": "h2" } }),
            Failure::WriteError => json!({ "event": "fs_write_result", "success": false,
                "error": "fs_write: no space left on device" }),
            Failure::LongWriteError => json!({ "event": "fs_write_result", "success": false,
                "error": "open /tmp/notebook-save-review.WzYy6o/notebook-2693475008/engineering-platform-and-infrastructure/production-rollout-and-deployment/customer-migration-and-backfill/architecture-review-and-acceptance/europe-stockholm-hardware-replacement/dependencies-and-migration-state/release-coordination-and-communications/team-ownership-and-followup/本番環境のクラスタにおけるバックアップと復旧手順/内部インフラのリリース運用変更の検証タスク一覧/東京リージョン向けの障害復旧と段階的な移行作業/operator-guides-and-incident-reports/shift-handoff-and-investigation-notes/.index.md.tmp.1716.1795179264032749842: permission denied" }),
        });
    let mut world = start(daemon);
    let notebook = if tile {
        world.gesture(|app| app.press("escape"));
        "notebook.tile.n"
    } else {
        world
            .automate("run", json!({ "name": "notebook.openFullscreen" }))
            .unwrap();
        world.settle();
        "notebook"
    };
    let folded = world
        .screen()
        .find(Target::key(format!("{notebook}.files")))
        .is_none();
    unfold_tree(&mut world, notebook);
    world.gesture(|app| app.click(Target::key(format!("{notebook}.file.index.md"))));
    if folded {
        world.gesture(|app| app.click(Target::key(format!("{notebook}.fold.tree"))));
    }
    let scroll = format!("{notebook}.scroll");
    let [x, y, w, h] = world.screen().bounds(Target::key(&scroll)).unwrap();
    world.gesture(|app| app.runtime.wheel(x + w / 2.0, y + h / 2.0, -4000.0));
    let line = world
        .screen()
        .descendants(Target::key(&scroll))
        .into_iter()
        .filter_map(|n| n.key)
        .filter(|k| k.starts_with(&format!("{notebook}.editor.line.")))
        .find(|k| {
            world
                .screen()
                .bounds(Target::key(k))
                .is_some_and(|[_, top, _, _]| top > y + h / 2.0 && top < y + h - 40.0)
        })
        .expect("a line of the note in view");
    world.gesture(|app| {
        app.click(Target::key(line));
        app.press("end");
        app.type_text(" Typed far down.");
    });
    world.advance(std::time::Duration::from_millis(700));
    let name = match (tile, failure) {
        (false, Failure::Conflict) => "notebook-conflict",
        (false, Failure::WriteError) => "notebook-save-error",
        (true, Failure::Conflict) => "notebook-tile-conflict",
        (true, Failure::WriteError) => "notebook-tile-save-error",
        (false, Failure::LongWriteError) => "notebook-long-save-error",
        (true, Failure::LongWriteError) => "notebook-tile-long-save-error",
    };
    snap(&mut world, name, dark);
}

/// The notebook's finder over the open note.
fn notebook_finder(dark: bool) {
    let mut world = notebook_open(dark);
    run(&mut world, Action::FileOpen);
    snap(&mut world, "notebook-finder", dark);
}

/// Delegation with no roles yet and Attn's Builder to add: its Add Attn roles and + Custom
/// role, and the same in the smallest window at 150% text.
fn settings_delegation(dark: bool) {
    let mut world = empty_delegation_world(dark, &[]);
    snap(&mut world, "settings-delegation", dark);
    let mut world = empty_delegation_world(dark, &[("uiScale", "1.5")]);
    world.resize((800.0, 600.0));
    world.gesture(|app| {
        app.update(|_, tx| {
            tx.scroll_center_in_instant("settings.body", "settings.delegation.adopt")
        })
    });
    snap(&mut world, "settings-delegation-empty-smallest", dark);
}

fn empty_delegation_world(dark: bool, settings: &[(&str, &str)]) -> World {
    settings_world(
        dark,
        "delegation",
        "delegation",
        settings,
        |_| {},
        |daemon| {
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
                    workflow_skill_paths: Some(Vec::new()),
                    ..DelegationPreferencesResultMessage::new()
                })
            });
        },
    )
}

fn route(harness: &str, model: &str, effort: &str) -> DelegationSelection {
    DelegationSelection {
        harness: harness.into(),
        model: model.into(),
        effort: effort.into(),
    }
}

fn delegation_role(
    id: &str,
    name: &str,
    icon: &str,
    description: &str,
    choices: Vec<(&str, &str, &str, DelegationSelection)>,
) -> DelegationRole {
    DelegationRole {
        id: id.into(),
        name: name.into(),
        icon: icon.into(),
        enabled: true,
        description: description.into(),
        instructions:
            "Implement the agreed brief. Report what you verified and what you could not.".into(),
        default_choice_id: choices.first().map_or_else(String::new, |c| c.0.into()),
        choices: choices
            .into_iter()
            .map(|(id, name, when, selection)| DelegationChoice {
                id: id.into(),
                name: name.into(),
                when: when.into(),
                selection,
            })
            .collect(),
        builtin: None,
    }
}

/// A delegation table as people keep one: a maintained Builder with two alternatives, a role
/// that still needs a model, one turned off, and the fallback.
fn delegation_roles() -> Vec<DelegationRole> {
    let builder = DelegationRole {
        builtin: Some("builder".into()),
        ..delegation_role(
            "builder",
            "Builder",
            "code",
            "Implements an agreed brief or plan, and reports what it verified.",
            vec![
                ("default", "", "", route("claude", "opus", "high")),
                (
                    "fast",
                    "Fast",
                    "Small edits with one clear answer, such as a rename or a copy change.",
                    route("claude", "sonnet", "medium"),
                ),
                ("unnamed", "", "", route("codex", "", "")),
            ],
        )
    };
    let orchestrator = delegation_role(
        "orchestrator",
        "Orchestrator",
        "layers",
        "Runs an agreed plan through Builders and reviews their work.",
        vec![("default", "", "", DelegationSelection::default())],
    );
    let reviewer = DelegationRole {
        enabled: false,
        ..delegation_role(
            "reviewer",
            "Reviewer",
            "search",
            "Reviews a change against its plan before it lands.",
            vec![
                ("default", "", "", route("claude", "opus", "high")),
                (
                    "second",
                    "Second opinion",
                    "A change that is costly to reverse.",
                    route("codex", "", ""),
                ),
            ],
        )
    };
    vec![builder, orchestrator, reviewer]
}

/// Settings on Delegation, its table read as `preferences` say, or refused with `failure`,
/// then `then` done to it, shot as `name`.
fn delegation_scene(
    dark: bool,
    name: &str,
    enabled: bool,
    failure: Option<&'static str>,
    settings: &[(&str, &str)],
    then: impl FnOnce(&mut World),
) {
    let mut world = settings_world(
        dark,
        "delegation",
        "delegation",
        settings,
        |_| {},
        move |daemon| {
            delegation_preferences(daemon, enabled, failure);
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
                    ..HarnessModelsResultMessage::new()
                })
            });
        },
    );
    then(&mut world);
    snap(&mut world, name, dark);
}

fn delegation_preferences(daemon: &Daemon, enabled: bool, failure: Option<&'static str>) {
    daemon.set_harnesses(attn_testkit::built_in_harnesses());
    daemon.on("delegation_preferences_get", move |r: &Request| {
        let roles = delegation_roles();
        message(DelegationPreferencesResultMessage {
            request_id: r["request_id"].as_str().unwrap_or_default().into(),
            success: failure.is_none(),
            error: failure.map(String::from),
            preferences: failure.is_none().then(|| DelegationPreferences {
                enabled,
                revision: 3,
                workflow_skill_enabled: true,
                roles: roles.clone(),
                fallback: DelegationFallback {
                    instructions: String::new(),
                    selection: route("claude", "sonnet", "medium"),
                },
            }),
            templates: Some(vec![roles[0].clone()]),
            expanded_roles: Some(roles),
            workflow_skill_paths: Some(Vec::new()),
            ..DelegationPreferencesResultMessage::new()
        })
    });
}

fn delegation_answers(daemon: &Daemon) {
    delegation_preferences(daemon, true, None);
    picker_models(daemon);
}

fn open_builder(world: &mut World) {
    world.gesture(|app| app.click(Target::key("settings.delegation.role.builder")));
}

/// Delegation's table in each state a person meets it in: rows at rest, a row open, an
/// alternative open, the narrowest window, the smallest window with the largest text and the
/// open row focused, the table switched off, and a load that failed.
fn settings_delegation_table(dark: bool) {
    delegation_scene(dark, "settings-delegation-roles", true, None, &[], |_| {});
    delegation_scene(
        dark,
        "settings-delegation-open",
        true,
        None,
        &[],
        open_builder,
    );
    delegation_scene(
        dark,
        "settings-delegation-alternative",
        true,
        None,
        &[],
        |world| {
            open_builder(world);
            world.gesture(|app| {
                app.click(Target::key("settings.delegation.alternative.builder/fast"))
            });
        },
    );
    delegation_scene(
        dark,
        "settings-delegation-narrow",
        true,
        None,
        &[],
        |world| {
            world.resize((800.0, SIZE.1));
            open_builder(world);
        },
    );
    delegation_scene(
        dark,
        "settings-delegation-smallest-open",
        true,
        None,
        &[("uiScale", "1.5")],
        |world| {
            world.resize((800.0, 600.0));
            open_builder(world);
            world.gesture(|app| {
                app.update(|_, tx| {
                    tx.scroll_center_in_instant(
                        "settings.body",
                        "settings.delegation.role.builder",
                    );
                    tx.focus("settings.delegation.role.builder", true);
                })
            });
            snap(world, "settings-delegation-smallest-focused", dark);
            world.gesture(|app| {
                app.update(|_, tx| {
                    tx.scroll_center_in_instant(
                        "settings.body",
                        "settings.delegation.alternative.builder/fast",
                    )
                })
            });
        },
    );
    delegation_scene(dark, "settings-delegation-off", false, None, &[], |_| {});
    delegation_scene(
        dark,
        "settings-delegation-failed",
        true,
        Some("Couldn't read the delegation table: the daemon is restarting."),
        &[],
        |_| {},
    );
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
        let field = format!("settings.setting.{setting}");
        world.gesture(|app| {
            app.update(|_, tx| tx.scroll_center_in_instant("settings.body", &field))
        });
        click(&mut world, Target::key(field.clone()));
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

/// The model price overrides on the Agents page, opened: a saved override for the longest real
/// model id with one rate refused, a saved override that no longer parses, and the new override's
/// form holding a model that already has one, each brought into view; then the billed-as list open.
fn settings_pricing(dark: bool) {
    const LONG_MODEL: &str =
        "anthropic.claude-opus-5-5-20261101-v1:0-with-extended-thinking-eu-north-1";
    let rates = r#"{"input_usd_per_mtok":15,"output_usd_per_mtok":75,"cache_read_usd_per_mtok":1.5,"cache_write_5m_usd_per_mtok":18.75,"cache_write_1h_usd_per_mtok":30}"#;
    let long = format!("session_cost.price.{LONG_MODEL}");
    let settings = [
        (long.as_str(), rates),
        (
            "session_cost.price.gpt-6-astra",
            r#"{"input_usd_per_mtok":3}"#,
        ),
        ("session_cost.billed_as.codex-auto-review", "gpt-6-sol"),
    ];
    let mut world = settings_world(dark, "agents", "agents", &settings, |_| {}, refusing);
    let focus = |world: &mut World, key: String| {
        world.gesture(move |app| app.update(move |_, tx| tx.focus(key, true)));
    };
    focus(&mut world, "settings.pricing".to_owned());
    tap(&mut world, Key::Text(" ".into()));
    let rate = format!("settings.setting.{long}#cache_read_usd_per_mtok");
    focus(&mut world, rate.clone());
    world.gesture(|app| app.type_text("5"));
    tap(&mut world, Key::Enter);
    world.gesture(|app| app.update(|_, tx| tx.scroll_center_in_instant("settings.body", &rate)));
    snap(&mut world, "settings-agents-pricing-refused", dark);
    for (key, typed) in [
        ("settings.price-new.model", "gpt-6-astra"),
        ("settings.price-new.input_usd_per_mtok", "2"),
    ] {
        focus(&mut world, key.to_owned());
        world.gesture(|app| app.type_text(typed));
    }
    world.gesture(|app| app.update(|_, tx| tx.scroll_to_bottom_instant("settings.body")));
    snap(&mut world, "settings-agents-pricing-new", dark);
    let trigger = "settings.price.billed-as-trigger";
    focus(&mut world, trigger.to_owned());
    tap(&mut world, Key::Text(" ".into()));
    snap(&mut world, "settings-agents-pricing-billed-as", dark);
}

/// Paired senders and Phones with the person's Remove and Revoke refused, each refusal under
/// the row it was pressed on: the longest real sender and phone names and a daemon's sentence.
fn settings_refused_rows(dark: bool) {
    let mut world = settings_world(
        dark,
        "senders",
        "senders",
        &[],
        |_| {},
        |daemon| {
            daemon.on("senders_get", |_: &Request| {
                let sender = |id: &str, name: &str| attn_protocol::PairedSender {
                    id: id.into(),
                    name: name.into(),
                    created_at: "2026-11-01T09:00:00Z".into(),
                    last_used_at: Some("2026-11-19T17:45:00Z".into()),
                };
                message(attn_protocol::SendersUpdatedMessage {
                    senders: vec![
                        sender("s1", "engineering-observability-staging-deploy-bot"),
                        sender("s2", "ci"),
                    ],
                    ..attn_protocol::SendersUpdatedMessage::new()
                })
            });
            daemon.on("sender_unpair", |_: &Request| {
                message(attn_protocol::SenderUnpairResultMessage {
                    success: false,
                    error: Some(LONG_REFUSAL.into()),
                    ..attn_protocol::SenderUnpairResultMessage::new()
                })
            });
        },
    );
    world.gesture(|app| {
        app.update(|_, tx| {
            tx.scroll_center_in_instant("settings.body", "settings.sender.s1.remove")
        })
    });
    world.gesture(|app| app.click(Target::key("settings.sender.s1.remove")));
    snap(&mut world, "settings-senders-refused", dark);
    let mut world = settings_world(
        dark,
        "phones",
        "phones",
        &[("phone_access_enabled", "true")],
        |_| {},
        |daemon| {
            daemon.on("phones_get", |_: &Request| {
                let phone =
                    |device: &str, name: &str, connected: bool| attn_protocol::PhonePairing {
                        device_id: device.into(),
                        profile_id: PROFILE.into(),
                        name: name.into(),
                        created_at: "2026-11-02T10:00:00Z".into(),
                        last_seen_at: "2026-11-18T21:10:00Z".into(),
                        connected,
                    };
                message(PhonesUpdatedMessage {
                    link: PhoneLink {
                        state: "connected".into(),
                        ..PhoneLink::default()
                    },
                    pairings: vec![
                        phone(
                            "d1",
                            "Alexandra Konstantinopoulou’s iPhone 17 Pro Max",
                            false,
                        ),
                        phone("d2", "iPad", true),
                    ],
                    ..PhonesUpdatedMessage::new()
                })
            });
            daemon.on("phone_pairing_revoke", |_: &Request| {
                message(attn_protocol::PhonePairingRevokeResultMessage {
                    success: false,
                    error: Some(LONG_REFUSAL.into()),
                    ..attn_protocol::PhonePairingRevokeResultMessage::new()
                })
            });
        },
    );
    let revoke = format!("settings.phone.{PROFILE}.d1.revoke");
    world.gesture(|app| app.update(|_, tx| tx.scroll_center_in_instant("settings.body", &revoke)));
    world.gesture(|app| app.click(Target::key(revoke.clone())));
    snap(&mut world, "settings-phones-refused", dark);
}

/// Claude's route picker on the Agents page: as it opens on its discovered models, the longest
/// real model id among them; with a filter that matches nothing; and with an exact model id typed;
/// the first and the last again in the smallest window at the largest text.
fn settings_route_picker(dark: bool) {
    const LONG_MODEL: &str =
        "anthropic.claude-opus-5-5-20261101-v1:0-with-extended-thinking-eu-north-1";
    let shots: [(&str, Option<&str>, bool); 5] = [
        ("settings-route-picker", None, false),
        (
            "settings-route-picker-no-match",
            Some("zz-no-such-model"),
            false,
        ),
        ("settings-route-picker-exact", Some(LONG_MODEL), false),
        ("settings-smallest-route-picker", None, true),
        (
            "settings-smallest-route-picker-exact",
            Some(LONG_MODEL),
            true,
        ),
    ];
    for (name, typed, smallest) in shots {
        let scale: &[(&str, &str)] = if smallest { &[("uiScale", "1.5")] } else { &[] };
        let mut world = settings_world(
            dark,
            "agents",
            "agents",
            scale,
            |_| {},
            |daemon| {
                daemon.set_harnesses(attn_testkit::built_in_harnesses());
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
                            model(LONG_MODEL, LONG_MODEL, "deep"),
                        ],
                        ..HarnessModelsResultMessage::new()
                    })
                });
            },
        );
        if smallest {
            world.resize((800.0, 600.0));
        }
        let chip = "settings.route.default-claude";
        world.gesture(|app| app.update(|_, tx| tx.scroll_center_in_instant("settings.body", chip)));
        world.gesture(move |app| app.update(move |_, tx| tx.focus(chip.to_owned(), true)));
        tap(&mut world, Key::Text(" ".into()));
        if let Some(typed) = typed {
            world.gesture(|app| {
                app.update(|_, tx| tx.focus("settings.route-picker.filter".to_owned(), true))
            });
            world.gesture(|app| app.type_text(typed));
        }
        snap(&mut world, name, dark);
    }
}

/// Every route chip's model picker: closed, open,
/// a query that matches some, an exact model id, and one that matches nothing.
fn route_picker_callers(dark: bool) {
    let settings = |section: &'static str, search: &'static str| {
        move || settings_world(dark, section, search, &[], |_| {}, delegation_answers)
    };
    picker_states(
        dark,
        "agents",
        "settings.route.default-claude",
        Some("settings.body"),
        None,
        settings("agents", "agents"),
    );
    picker_states(
        dark,
        "delegation-role",
        "settings.route.delegation.builder/default",
        Some("settings.body"),
        None,
        move || {
            let mut world = settings_world(
                dark,
                "delegation",
                "delegation",
                &[],
                |_| {},
                delegation_answers,
            );
            open_builder(&mut world);
            world
        },
    );
    picker_states(
        dark,
        "delegation-alternative",
        "settings.route.delegation.builder/fast",
        Some("settings.body"),
        None,
        move || {
            let mut world = settings_world(
                dark,
                "delegation",
                "delegation",
                &[],
                |_| {},
                delegation_answers,
            );
            open_builder(&mut world);
            world.gesture(|app| {
                app.update(|_, tx| {
                    tx.scroll_center_in_instant(
                        "settings.body",
                        "settings.delegation.alternative.builder/fast",
                    );
                    tx.focus(
                        "settings.delegation.alternative.builder/fast".to_owned(),
                        true,
                    )
                })
            });
            tap(&mut world, Key::Enter);
            world
        },
    );
    picker_states(
        dark,
        "delegation-fallback",
        "settings.route.delegation.fallback",
        Some("settings.body"),
        None,
        move || {
            settings_world(
                dark,
                "delegation",
                "delegation",
                &[],
                |_| {},
                delegation_answers,
            )
        },
    );
    picker_states(
        dark,
        "activity",
        "settings.route.activity",
        Some("settings.body"),
        Some("claude"),
        settings("backgroundAgents", "background agents"),
    );
    picker_states(
        dark,
        "automation",
        "settings.route.automation",
        None,
        Some("claude"),
        move || automation_form(dark),
    );
    picker_states(
        dark,
        "crew-launch",
        "settings.route.crew.chief",
        None,
        Some("claude"),
        move || {
            let mut world = crew_page(dark);
            click(&mut world, Target::key("crew.roster.chief"));
            click(&mut world, Target::key("crew.tab.launch"));
            world
        },
    );
}

const PICKER_LONG_MODEL: &str =
    "anthropic.claude-opus-5-5-20261101-v1:0-with-extended-thinking-eu-north-1";

fn picker_states(
    dark: bool,
    caller: &str,
    chip: &'static str,
    body: Option<&'static str>,
    harness: Option<&'static str>,
    world: impl Fn() -> World,
) {
    let states: [(&str, bool, Option<&str>); 5] = [
        ("closed", false, None),
        ("open", true, None),
        ("typed", true, Some("son")),
        ("exact", true, Some(PICKER_LONG_MODEL)),
        ("no-match", true, Some("zz-no-such-model")),
    ];
    for (state, open, typed) in states {
        let name = format!("picker-{caller}-{state}");
        if !drawing(&name) {
            continue;
        }
        let mut world = world();
        picker_models(&world.daemon);
        if let Some(body) = body {
            world.gesture(move |app| {
                app.update(move |_, tx| tx.scroll_center_in_instant(body, chip))
            });
        }
        world.gesture(move |app| app.update(move |_, tx| tx.focus(chip.to_owned(), true)));
        if open {
            tap(&mut world, Key::Text(" ".into()));
            let listed = harness
                .map(|h| Target::key(format!("settings.route-picker.harness.{h}")))
                .filter(|target| world.screen().find(target.clone()).is_some());
            if let Some(target) = listed {
                click(&mut world, target);
            }
        }
        if let Some(typed) = typed {
            world.gesture(|app| {
                app.update(|_, tx| tx.focus("settings.route-picker.filter".to_owned(), true))
            });
            world.gesture(|app| app.type_text(typed));
        }
        snap(&mut world, &name, dark);
    }
}

fn picker_models(daemon: &Daemon) {
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
                model(PICKER_LONG_MODEL, PICKER_LONG_MODEL, "deep"),
            ],
            ..HarnessModelsResultMessage::new()
        })
    });
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
    click(&mut world, Target::key("settings.updates.start"));
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

/// The empty and failed states of the Settings pages no other scene shows: Paired senders with
/// none, the Event bus with nothing on it and with its read failed, the Task runner with its
/// read failed, Models and Background agents with the harness list's read failed, a refused
/// plugin install, a refused outpost, and Analytics' records read failed.
fn settings_missing_states(dark: bool) {
    let mut world = settings_world(
        dark,
        "senders",
        "senders",
        &[],
        |_| {},
        |daemon| {
            daemon.on("senders_get", |_: &Request| {
                message(attn_protocol::SendersUpdatedMessage {
                    senders: Vec::new(),
                    ..attn_protocol::SendersUpdatedMessage::new()
                })
            });
        },
    );
    snap(&mut world, "settings-senders-empty", dark);

    let mut world = settings_world(
        dark,
        "eventBus",
        "event bus",
        &[],
        |_| {},
        |daemon| {
            daemon.on("bus_status_get", |r: &Request| {
                message(BusStatusResultMessage {
                    request_id: r["request_id"].as_str().unwrap_or_default().into(),
                    success: true,
                    ..BusStatusResultMessage::new()
                })
            });
        },
    );
    snap(&mut world, "settings-eventBus-empty", dark);

    let mut world = settings_world(
        dark,
        "eventBus",
        "event bus",
        &[],
        |_| {},
        |daemon| {
            daemon.on("bus_status_get", |r: &Request| {
                message(BusStatusResultMessage {
                    request_id: r["request_id"].as_str().unwrap_or_default().into(),
                    success: false,
                    error: Some("reading the event log failed: database is locked".into()),
                    ..BusStatusResultMessage::new()
                })
            });
        },
    );
    snap(&mut world, "settings-eventBus-failed", dark);

    let mut world = settings_world(
        dark,
        "backgroundTasks",
        "task runner",
        &[],
        |_| {},
        |daemon| {
            daemon.on("task_list", |r: &Request| {
                message(attn_protocol::TaskListResultMessage {
                    request_id: r["request_id"].as_str().unwrap_or_default().into(),
                    success: false,
                    error: Some("reading the task queue failed: database is locked".into()),
                    ..attn_protocol::TaskListResultMessage::new()
                })
            });
        },
    );
    snap(&mut world, "settings-backgroundTasks-failed", dark);

    for (section, search) in [
        ("models", "models"),
        ("backgroundAgents", "background agents"),
    ] {
        let mut world = settings_world(
            dark,
            section,
            search,
            &[],
            |_| {},
            |daemon| {
                daemon.on("delegation_preferences_get", |r: &Request| {
                    message(DelegationPreferencesResultMessage {
                        request_id: r["request_id"].as_str().unwrap_or_default().into(),
                        success: false,
                        error: Some(
                            "reading delegation preferences failed: the daemon is restarting"
                                .into(),
                        ),
                        ..DelegationPreferencesResultMessage::new()
                    })
                });
            },
        );
        snap(&mut world, &format!("settings-{section}-failed"), dark);
    }

    let mut world = settings_world(
        dark,
        "plugins",
        "plugins",
        &[],
        |_| {},
        |daemon| {
            daemon.on("list_plugins", |_: &Request| {
                message(attn_protocol::PluginsUpdatedMessage::new())
            });
            daemon.on("install_plugin", |_: &Request| {
                message(attn_protocol::PluginActionResultMessage {
                    action: "install".into(),
                    success: false,
                    error: Some(
                        "clone failed: repository git@host:team/missing.git not found".into(),
                    ),
                    ..attn_protocol::PluginActionResultMessage::new()
                })
            });
        },
    );
    click(&mut world, Target::key("settings.plugins.source"));
    world.gesture(|app| app.type_text("git@host:team/missing.git"));
    click(&mut world, Target::key("settings.plugins.install"));
    snap(&mut world, "settings-plugins-refused", dark);

    let mut world = settings_world(
        dark,
        "machines",
        "machines",
        SWITCHED_ON,
        |_| {},
        |daemon| {
            daemon.on("add_outpost", |_: &Request| {
                json!({"event": "command_error", "cmd": "add_outpost", "success": false, "error": "Address studio:2200 is already paired"})
            });
        },
    );
    click(&mut world, Target::key("settings.machines.name"));
    world.gesture(|app| app.type_text("studio"));
    click(&mut world, Target::key("settings.machines.destination"));
    world.gesture(|app| app.type_text("studio:2200"));
    click(&mut world, Target::key("settings.machines.submit"));
    snap(&mut world, "settings-machines-refused", dark);

    let mut world = settings_world(
        dark,
        "analytics",
        "analytics",
        &[("analytics.enabled", "true")],
        |_| {},
        |daemon| {
            daemon.on("list_analytics_records", |r: &Request| {
                message(attn_protocol::ListAnalyticsRecordsResultMessage {
                    request_id: r["request_id"].as_str().unwrap_or_default().into(),
                    success: false,
                    error: Some("reading the analytics outbox failed: database is locked".into()),
                    ..attn_protocol::ListAnalyticsRecordsResultMessage::new()
                })
            });
        },
    );
    click(&mut world, Target::key("settings.analytics.views.all"));
    snap(&mut world, "settings-analytics-records-failed", dark);
}

/// A Settings page in the smallest window at the largest text, its state changed by `change` and
/// the daemon answering as `answer` scripts it.
fn settings_smallest_page(
    dark: bool,
    section: &str,
    search: &str,
    settings: &[(&str, &str)],
    change: impl FnOnce(&mut InitialStateMessage),
    answer: impl FnOnce(&Daemon),
) {
    let mut all = vec![("uiScale", "1.5")];
    all.extend_from_slice(settings);
    let mut world = settings_world(dark, section, search, &all, change, answer);
    world.resize((800.0, 600.0));
    snap(&mut world, &format!("settings-smallest-{section}"), dark);
    world.gesture(|app| app.update(|_, tx| tx.scroll_to_bottom_instant("settings.body")));
    snap(
        &mut world,
        &format!("settings-smallest-{section}-bottom"),
        dark,
    );
}

/// The pages whose structure moved onto blocks before their lists, each at the smallest size
/// with the longest real content it meets.
fn settings_smallest_pages(dark: bool) {
    settings_smallest_page(
        dark,
        "agents",
        "agents",
        &[(
            "default_model_claude",
            "anthropic.claude-opus-5-5-20261101-v1:0-with-extended-thinking-eu-north-1",
        )],
        |_| {},
        |daemon| {
            let mut list = attn_testkit::built_in_harnesses();
            list[0].installation = "checking".into();
            list[1].installation = "missing".into();
            list.push(Harness {
                id: "snipe".into(),
                name: "Engineering observability staging review agent with a long name".into(),
                installation: "installed".into(),
                offered: true,
                options: attn_protocol::HarnessOptions {
                    initial_prompt: true,
                    model_pin: true,
                    effort_pin: true,
                    discovery: true,
                    ..attn_protocol::HarnessOptions::default()
                },
                ..Harness::default()
            });
            daemon.set_harnesses(list);
        },
    );
    settings_smallest_page(
        dark,
        "machines",
        "machines",
        SWITCHED_ON,
        |state| {
            state.outposts = Some(vec![attn_protocol::OutpostInfo {
                id: "outpost-1".into(),
                name: "engineering-observability-staging-builder".into(),
                destination: "deploy-automation@builder-07.eu-north-1.internal.example.com".into(),
                state: "error".into(),
                reach: "unreachable".into(),
                state_since: "2026-11-12T08:00:00Z".into(),
                last_error: Some("ssh: connect to host builder-07.eu-north-1.internal.example.com port 22: Connection timed out after 30 seconds while the daemon was still pairing".into()),
                ..attn_protocol::OutpostInfo::default()
            }]);
        },
        |_| {},
    );
    settings_smallest_page(
        dark,
        "eventBus",
        "event bus",
        &[],
        |_| {},
        |daemon| {
            daemon.on("bus_status_get", |r: &Request| {
                message(crowded_bus(
                    r["request_id"].as_str().unwrap_or_default().into(),
                ))
            });
        },
    );
    settings_smallest_page(
        dark,
        "backgroundTasks",
        "task runner",
        &[],
        |_| {},
        |daemon| {
            daemon.on("task_list", |r: &Request| {
                let task = |id: &str, state: &str, error: Option<&str>| attn_protocol::Task {
                    id: id.into(),
                    kind: "session_activity".into(),
                    subject: format!("engineering-observability-staging-builder-session-{id}"),
                    state: state.into(),
                    attempts: 12,
                    next_attempt_at: "2026-11-20T09:00:00Z".into(),
                    created_at: "2026-11-20T08:00:00Z".into(),
                    updated_at: "2026-11-20T08:30:00Z".into(),
                    last_error: error.map(str::to_owned),
                    last_diagnostic: error.map(|_| "stderr: authentication failed for https://api.example.com/v1/sessions: token expired".to_owned()),
                };
                message(attn_protocol::TaskListResultMessage {
                    request_id: r["request_id"].as_str().unwrap_or_default().into(),
                    success: true,
                    tasks: Some(vec![
                        task("1", "dead", Some("The agent process exited with status 2 after the provider refused its credentials three times in a row.")),
                        task("2", "running", None),
                        task("3", "queued", None),
                    ]),
                    ..attn_protocol::TaskListResultMessage::new()
                })
            });
        },
    );
    settings_smallest_page(
        dark,
        "senders",
        "senders",
        &[],
        |_| {},
        |daemon| {
            daemon.on("senders_get", |_: &Request| {
                let sender =
                    |id: &str, name: &str, used: Option<&str>| attn_protocol::PairedSender {
                        id: id.into(),
                        name: name.into(),
                        created_at: "2026-11-01T09:00:00Z".into(),
                        last_used_at: used.map(str::to_owned),
                    };
                message(attn_protocol::SendersUpdatedMessage {
                    senders: vec![
                        sender(
                            "s1",
                            "engineering-observability-staging-deploy-bot",
                            Some("2026-11-19T17:45:00Z"),
                        ),
                        sender("s2", "ci", None),
                    ],
                    ..attn_protocol::SendersUpdatedMessage::new()
                })
            });
        },
    );
    settings_smallest_page(
        dark,
        "phones",
        "phones",
        SWITCHED_ON,
        |_| {},
        |daemon| {
            daemon.on("phones_get", |_: &Request| {
                let phone =
                    |device: &str, name: &str, connected: bool| attn_protocol::PhonePairing {
                        device_id: device.into(),
                        profile_id: PROFILE.into(),
                        name: name.into(),
                        created_at: "2026-11-02T10:00:00Z".into(),
                        last_seen_at: "2026-11-18T21:10:00Z".into(),
                        connected,
                    };
                message(PhonesUpdatedMessage {
                    link: PhoneLink {
                        state: "connected".into(),
                        ..PhoneLink::default()
                    },
                    pairings: vec![
                        phone(
                            "d1",
                            "Alexandra Konstantinopoulou’s iPhone 17 Pro Max",
                            false,
                        ),
                        phone("d2", "iPad", true),
                    ],
                    ..PhonesUpdatedMessage::new()
                })
            });
        },
    );
    settings_smallest_page(dark, "plugins", "plugins", &[], |_| {}, failing_plugin);
    settings_smallest_page(
        dark,
        "models",
        "models",
        &[(
            "model_tier_overrides",
            r#"[{"harness":"codex","model":"openrouter/gpt-5.1-codex-max-preview-2026-09-30-with-extended-context","tier":"deep"}]"#,
        )],
        |_| {},
        |daemon| {
            let mut list = attn_testkit::built_in_harnesses();
            list.push(Harness {
                id: "plug".into(),
                name: "Build `東京` _staging_ *driver*".into(),
                installation: "installed".into(),
                offered: true,
                options: attn_protocol::HarnessOptions {
                    initial_prompt: true,
                    model_pin: true,
                    effort_pin: true,
                    discovery: true,
                    ..attn_protocol::HarnessOptions::default()
                },
                ..Harness::default()
            });
            daemon.set_harnesses(list);
            daemon.on("harness_models", |r: &Request| {
                let request_id: String = r["request_id"].as_str().unwrap_or_default().into();
                if r["harness"] == "plug" {
                    return message(HarnessModelsResultMessage {
                        request_id,
                        success: true,
                        models: vec![HarnessModel {
                            harness: "plug".into(),
                            id: "driver-1".into(),
                            name: "Driver 1".into(),
                            access: "unknown".into(),
                            effort_support: "unsupported".into(),
                            tier_source: "none".into(),
                            ..HarnessModel::default()
                        }],
                        ..HarnessModelsResultMessage::new()
                    });
                }
                if r["harness"] == "codex" {
                    return message(HarnessModelsResultMessage {
                        request_id,
                        success: false,
                        error: Some("codex models: exec: /opt/agents/codex-`canary`: executable file not found in $PATH while listing the provider catalog".into()),
                        ..HarnessModelsResultMessage::new()
                    });
                }
                message(HarnessModelsResultMessage {
                    request_id,
                    success: true,
                    models: vec![HarnessModel {
                        harness: "claude".into(),
                        provider: "bedrock-eu-north-1".into(),
                        id: "anthropic.claude-opus-5-5-20261101-v1:0-with-extended-thinking".into(),
                        name: "Claude Opus 5.5 with extended thinking (Bedrock, EU North)".into(),
                        access: "unknown".into(),
                        effort_support: "unsupported".into(),
                        tier_source: "none".into(),
                        ..HarnessModel::default()
                    }],
                    ..HarnessModelsResultMessage::new()
                })
            });
        },
    );
}

/// The event bus with one live consumer, its Disable, and the footer's Refresh.
fn settings_event_bus(dark: bool) {
    settings_section(dark, "eventBus", "event bus", &[], |daemon| {
        daemon.on("bus_status_get", |r: &Request| {
            message(crowded_bus(
                r["request_id"].as_str().unwrap_or_default().into(),
            ))
        });
    });
}

/// A bus with the longest real content: health findings, a loud producer, quiet classes, long
/// names and filters, and consumers that are disabled, lagging, pinning, stalled and not running.
fn crowded_bus(request_id: String) -> BusStatusResultMessage {
    let producer =
        |name: &str, events: i64, share: f64, surging: bool| attn_protocol::BusProducerStatus {
            name: name.into(),
            events,
            share,
            subjects: events / 40,
            recent_per_hour: if surging { 18_432.5 } else { 112.0 },
            baseline_per_hour: 96.25,
            surging,
            ..attn_protocol::BusProducerStatus::default()
        };
    let consumer = |name: &str, filter: &str| BusConsumerStatus {
        name: name.into(),
        cursor: 1_048_000,
        filter: filter.into(),
        enabled: true,
        live: true,
        updated_at: "2026-08-10T19:00:00Z".into(),
        ..BusConsumerStatus::default()
    };
    BusStatusResultMessage {
        request_id,
        success: true,
        earliest: 1_048_576,
        head: 1_049_000,
        rows: 1_048_576_000,
        bytes: 987_654_321_000,
        oldest_at: "2025-08-02T10:24:55Z".into(),
        newest_at: "2026-08-10T19:28:01Z".into(),
        delivering: true,
        retention_seconds: 365.0 * 86400.0,
        recent_window_seconds: 3600.0,
        baseline_window_seconds: 86400.0,
        surge_rate_per_hour: 1000.0,
        pin_alarm_seconds: 3600.0,
        health: vec![
            attn_protocol::BusHealthEntry {
                level: "error".into(),
                message: "Consumer engineering-observability-staging-notifier has pinned 1.2 GB of the log for 3 days; retention cannot trim below its cursor".into(),
                ..attn_protocol::BusHealthEntry::default()
            },
            attn_protocol::BusHealthEntry {
                level: "warning".into(),
                message: "session.state.changed published 18,432 events in the last hour against a usual 96".into(),
                ..attn_protocol::BusHealthEntry::default()
            },
        ],
        producers: vec![
            producer("session.state.changed.across.every.profile.and.outpost", 1_048_000_000, 0.62, true),
            producer("garden.seed.note.added", 210_000_000, 0.2, false),
            producer("pr.review.thread.updated", 105_000_000, 0.1, false),
            producer("notebook.sync.conflict.resolved", 2_000_000, 0.002, false),
            producer("outpost.reach.changed", 1_000_000, 0.001, false),
        ],
        consumers: vec![
            BusConsumerStatus {
                lag: 1_048_576,
                oldest_unread_at: "2026-08-07T19:00:00Z".into(),
                holds_retention_floor: true,
                pin_alarm: true,
                pinned_bytes: 1_234_567_890,
                ..consumer("engineering-observability-staging-notifier", "session.state.changed.across.every.profile.and.outpost")
            },
            BusConsumerStatus { enabled: false, ..consumer("ticketwatch", "pr.*") },
            BusConsumerStatus { stalled: "handler returned: connection refused by https://hooks.example.com/attn".into(), ..consumer("webhook-relay", "") },
            BusConsumerStatus { live: false, ..consumer("projections", "session.*") },
        ],
        ..BusStatusResultMessage::new()
    }
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
    let mut world = ledger_seeded_world(dark, false);
    snap(&mut world, "ledger-seeded", dark);
    click(&mut world, Target::key("ledger.row.s1.more"));
    snap(&mut world, "ledger-menu", dark);
    world.gesture(|app| app.press("escape"));
    click(&mut world, Target::key("ledger.keys"));
    snap(&mut world, "ledger-keys", dark);
}

/// The seeded ledger in the smallest window at 150% text, with the longest session name.
fn ledger_smallest(dark: bool) {
    let mut world = ledger_seeded_world(dark, true);
    snap(&mut world, "ledger-smallest", dark);
}

/// The ledger when the daemon could not list the sessions.
fn ledger_failed(dark: bool) {
    let daemon = scripted().state(initial(dark, document_tile("m", "/repo/plan.md"), vec![]));
    daemon.on("session_list", |r: &Request| {
        message(SessionListResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: false,
            error: Some("session_list failed: database is locked".into()),
            ..SessionListResultMessage::new()
        })
    });
    let mut world = start(daemon);
    press(&mut world, Action::SessionsOpen);
    snap(&mut world, "ledger-failed", dark);
}

fn ledger_seeded_world(dark: bool, smallest: bool) -> World {
    let tended = Seed {
        claimed: true,
        tender: Some(PartyView {
            name: "reviewer".into(),
            r#ref: "session:s1".into(),
            session_id: Some("s1".into()),
        }),
        ..seed("s-one", "Colors in one place", "growing", None)
    };
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![tended]);
    if smallest && let Some(settings) = state.settings.as_mut() {
        settings.insert("uiScale".into(), "1.5".into());
    }
    let daemon = scripted().state(state);
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
        entry(
            "s5",
            "observability-dashboard-migration-for-international-engineering",
            "working",
            "2026-11-17T08:30:00Z",
        ),
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
    if smallest {
        world.resize((800.0, 600.0));
    }
    press(&mut world, Action::SessionsOpen);
    click(&mut world, Target::key("ledger.row.s1"));
    world
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
    click(&mut world, Target::key("ledger.tab.worktrees"));
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
            ("new_session_agent", "claude"),
            ("queue_mode_enabled", "true"),
            ("notebook.root.effective", "/home/me/attn-notebook"),
        ] {
            settings.insert(key.into(), value.into());
        }
    }
    let daemon = scripted().state(state);
    daemon.set_harnesses(attn_testkit::built_in_harnesses());
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
    click(&mut world, Target::key("chief-slot"));
    snap(&mut world, "chief-setup", dark);
}

/// An ask's file, web link and commands labelled while the label chord is held.
fn asks_labels(dark: bool) {
    let seeds = vec![seed("s-7k3f9m", "Colors in one place", "growing", None)];
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), seeds);
    let prose = "Read [the plan](plan.md) and [the notes](https://example.com/notes) on s-7k3f9m.\n\n\
                 ```sh\nmake check\n```\n\nThen list what it left:\n\n```bash\nls -la\n```\n";
    state.asks = Some(vec![Ask {
        id: "a-ship".into(),
        session_id: "s1".into(),
        subject: "Ship the colors".into(),
        base_dir: "/repo".into(),
        created_at: "2026-11-17T09:00:00Z".into(),
        parts: vec![
            AskPart {
                markdown: Some(prose.into()),
                ..AskPart::default()
            },
            AskPart {
                question: Some(AskQuestion {
                    key: "q1".into(),
                    kind: "text".into(),
                    prompt: "Ship it?".into(),
                    ..AskQuestion::default()
                }),
                ..AskPart::default()
            },
        ],
        references: Some(vec![attn_protocol::AskReference {
            kind: "file".into(),
            text: "plan.md".into(),
            host: None,
            path: "/repo/plan.md".into(),
            line: 1,
        }]),
        ..Ask::default()
    }]);
    let mut world = start(scripted().state(state));
    press(&mut world, Action::DockAsks);
    let mac = attn_testkit::mac();
    world.gesture(|app| {
        app.hold(rune::Mods {
            cmd: true,
            logo: mac,
            ctrl: !mac,
            shift: !mac,
            ..rune::Mods::default()
        })
    });
    press(&mut world, Action::TerminalLabelIds);
    snap(&mut world, "asks-labels", dark);
}

/// The asks sheet showing one ask from the waiting agent, with its footer's keys and Send.
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

/// The asks sheet on an ask that offers commands at the top level, in a numbered list's item and
/// in a block quote, each with Open in terminal and the place it runs in.
fn asks_commands(dark: bool) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    state.asks = Some(vec![Ask {
        id: "a-shop".into(),
        session_id: "s1".into(),
        subject: "Check the shop".into(),
        base_dir: "/repo".into(),
        created_at: "2026-11-17T09:00:00Z".into(),
        parts: vec![
            AskPart {
                markdown: Some(
                    "Run the checks:\n\n```sh\nmake check\n```\n\n1. Look at the failing test:\n\n   ```bash\n   go test ./shop -run TestCart\n   ```\n\n2. Then tell me what you see.\n\n> If it is the cache again:\n>\n> ```sh\n> make clean-cache\n> ```\n"
                        .into(),
                ),
                ..AskPart::default()
            },
            AskPart {
                question: Some(AskQuestion {
                    key: "q1".into(),
                    kind: "text".into(),
                    prompt: "What failed?".into(),
                    ..AskQuestion::default()
                }),
                ..AskPart::default()
            },
        ],
        ..Ask::default()
    }]);
    let mut world = start(scripted().state(state));
    press(&mut world, Action::DockAsks);
    snap(&mut world, "asks-commands", dark);
}

/// The asks sheet reopened on a written draft after the window shrank to the smallest at the
/// largest text: the answer that takes the keyboard shows with its ring.
fn asks_reopened(dark: bool) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    if let Some(settings) = state.settings.as_mut() {
        settings.insert("uiScale".into(), "1.5".into());
    }
    state.asks = Some(vec![Ask {
        id: "a-text".into(),
        session_id: "s1".into(),
        subject: "Release announcement".into(),
        base_dir: "/repo".into(),
        created_at: "2026-11-17T09:00:00Z".into(),
        parts: vec![
            AskPart {
                markdown: Some(
                    "The nightly export has finished. We can send the report after the follow-up check."
                        .into(),
                ),
                ..AskPart::default()
            },
            AskPart {
                question: Some(AskQuestion {
                    key: "q1".into(),
                    kind: "text".into(),
                    prompt: "What should the release announcement say?".into(),
                    ..AskQuestion::default()
                }),
                ..AskPart::default()
            },
        ],
        ..Ask::default()
    }]);
    let mut world = World::start_sized(scripted().state(state), (1050.0, 780.0));
    world.open_desktop();
    press(&mut world, Action::DockAsks);
    world.gesture(|app| app.type_text("Response text."));
    world.gesture(|app| app.press("escape"));
    world.resize((800.0, 600.0));
    press(&mut world, Action::DockAsks);
    snap(&mut world, "asks-reopened", dark);
}

/// The asks sheet with nothing open, under the asks setting.
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

/// The title bar's asks button while `count` asks wait and the sheet is closed: the accent pill
/// with its count.
fn asks_waiting(dark: bool, count: usize, name: &str) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    state.asks = Some(
        (0..count)
            .map(|i| Ask {
                id: format!("a-{i}"),
                ..cache_ask("s1")
            })
            .collect(),
    );
    let mut world = start(scripted().state(state));
    snap(&mut world, name, dark);
}

/// The asks sheet on an ask with a question of choices: its context, a recommended choice
/// with a description, Other and the note.
fn asks_choices(dark: bool) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    state.asks = Some(vec![cache_ask("s1")]);
    let mut world = start(scripted().state(state));
    press(&mut world, Action::DockAsks);
    snap(&mut world, "asks-choices", dark);
}

/// The asks sheet listing every section: an agent waiting on the person, a snoozed agent's ask
/// and an ask replied to in the last day, each section's divider beside its rows, in a window
/// wide enough to set the list beside the ask, or so narrow it lists them over it.
fn asks_sections(dark: bool, stacked: bool) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    let sessions = state.sessions.get_or_insert_default();
    sessions[1].turn_owed = Some(true);
    sessions[1].turn_opened_at = Some(minutes_ago(12));
    sessions.push(Session {
        turn_owed: Some(true),
        turn_opened_at: Some(minutes_ago(30)),
        turn_snoozed_until: Some(minutes_ago(-90)),
        ..session("nap", "idle")
    });
    let ask = |id: &str, session: &str, subject: &str| Ask {
        id: id.into(),
        session_id: session.into(),
        subject: subject.into(),
        ..cache_ask(session)
    };
    state.asks = Some(vec![
        ask("a-cache", "s1", "Cache store"),
        ask("a-nap", "nap", "Ship on that date?"),
        Ask {
            reply: Some(attn_protocol::AskReply {
                answered_at: minutes_ago(40),
                ..attn_protocol::AskReply::default()
            }),
            ..ask("a-done", "s4", "Rename the export")
        },
    ]);
    let mut world = if stacked {
        let mut world = World::start_sized(scripted().state(state), (640.0, 720.0));
        world.open_desktop();
        world
    } else {
        start(scripted().state(state))
    };
    press(&mut world, Action::DockAsks);
    click(&mut world, Target::key("asks.replied"));
    let name = if stacked {
        "asks-sections-stacked"
    } else {
        "asks-sections"
    };
    snap(&mut world, name, dark);
}

/// The ask of a one-choice question, its choices described and one recommended, from `session`.
fn cache_ask(session: &str) -> Ask {
    let choice = |label: &str, description: &str| AskChoice {
        label: label.into(),
        description: Some(description.into()),
    };
    Ask {
        id: "a-cache".into(),
        session_id: session.into(),
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
                    default: None,
                }),
                ..AskPart::default()
            },
        ],
        ..Ask::default()
    }
}

/// The preview of agent `a`'s two open asks, a choice and a written answer, under the pointer
/// resting on its tile's ask chip, which counts the second.
fn asks_preview(dark: bool) {
    let mut state = tended_agent(dark);
    state.asks = Some(vec![
        cache_ask("s0"),
        Ask {
            id: "a-name".into(),
            session_id: "s0".into(),
            subject: "Release name".into(),
            base_dir: "/repo".into(),
            created_at: "2026-11-17T09:05:00Z".into(),
            parts: vec![AskPart {
                question: Some(AskQuestion {
                    key: "name".into(),
                    kind: "text".into(),
                    prompt: "What should the release be called?".into(),
                    ..AskQuestion::default()
                }),
                ..AskPart::default()
            }],
            ..Ask::default()
        },
    ]);
    let mut world = start(scripted().state(state));
    world.gesture(|app| {
        let chip = app
            .runtime
            .find("tile.a.ask")
            .expect("the ask chip is laid out");
        app.move_to(chip);
    });
    world.advance(std::time::Duration::from_secs(1));
    snap(&mut world, "asks-preview", dark);
}

/// The panel's tabs under the title bar's shelf: a shell that runs a command, shown, then a seed
/// and markdown files; with `many`, more files than the strip holds, folded at both ends.
fn asks_panel_tabs(dark: bool, many: bool) {
    let mut state = initial(
        dark,
        document_tile("m", "/repo/plan.md"),
        vec![seed("s-shop", "Ship the shop checks", "growing", None)],
    );
    let mut files = vec!["release-notes.md", "plan.md"];
    if many {
        files.extend([
            "north-wing.md",
            "south-wing.md",
            "cellar.md",
            "attic.md",
            "garage.md",
        ]);
    }
    let links: Vec<String> = files.iter().map(|f| format!("[{f}]({f})")).collect();
    state.asks = Some(vec![Ask {
        id: "a-shop".into(),
        session_id: "s0".into(),
        subject: "Ship the shop".into(),
        base_dir: "/repo".into(),
        seed_id: Some("s-shop".into()),
        created_at: "2026-11-17T09:00:00Z".into(),
        parts: vec![
            AskPart {
                markdown: Some(format!("Read {} before you choose.", links.join(", "))),
                ..AskPart::default()
            },
            AskPart {
                question: Some(AskQuestion {
                    key: "q1".into(),
                    kind: "text".into(),
                    prompt: "Ship it today?".into(),
                    ..AskQuestion::default()
                }),
                ..AskPart::default()
            },
        ],
        references: Some(
            files
                .iter()
                .map(|f| AskReference {
                    kind: "file".into(),
                    text: (*f).into(),
                    host: None,
                    path: format!("/repo/{f}"),
                    line: 1,
                })
                .collect(),
        ),
        ..Ask::default()
    }]);
    state.sessions.get_or_insert_default().push(Session {
        id: "shell-shop".into(),
        label: "shell-shop".into(),
        agent: "shell".into(),
        state: "idle".into(),
        directory: SHOP.into(),
        profile_id: PROFILE.into(),
        ..Session::default()
    });
    state.terminal_bindings = Some(vec![TerminalBinding {
        ask_id: Some("a-shop".into()),
        session_id: "shell-shop".into(),
        terminal_id: "rt-shop".into(),
    }]);
    let mut world = start(scripted().state(state));
    press(&mut world, Action::DockAsks);
    let mut frame = vec![0x01, b"rt-shop".len() as u8];
    frame.extend_from_slice(b"rt-shop");
    frame.extend_from_slice(&1u32.to_be_bytes());
    frame.extend_from_slice(
        b"\x1b]133;A\x07$ \x1b]133;B\x07make check\r\n\x1b]133;C\x07running\r\n",
    );
    world.daemon.emit_frame(frame);
    world.settle();
    click(&mut world, Target::key("asks.open-seed"));
    for f in &files {
        world.gesture(|app| app.click(Target::key(*f)));
    }
    let (step, times) = if many {
        ("mod+shift+[", 4)
    } else {
        ("mod+shift+]", 1)
    };
    for _ in 0..times {
        world.gesture(|app| app.press(step));
    }
    snap(
        &mut world,
        if many {
            "asks-panel-tabs-folded"
        } else {
            "asks-panel-tabs"
        },
        dark,
    );
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
    unfold_tree(&mut world, "notebook");
    click(&mut world, Target::key("notebook.file.knowledge"));
    snap(&mut world, "notebook-tree", dark);
}

/// The review's file view, opened from the brief's stop: its back bar and its keys.
fn review_file(dark: bool) {
    let mut world = review_alone(dark);
    tap(&mut world, Key::Right);
    tap(&mut world, Key::Enter);
    snap(&mut world, "review-file", dark);
}

fn review_keys(dark: bool) {
    let mut world = review_alone(dark);
    world.gesture(|app| app.type_text("?"));
    snap(&mut world, "review-keys", dark);
}

/// The review shown alone on its desktop, bound to agent a's session, beside a second open
/// document: its tabs over the tile, its header and its brief.
fn review_alone(dark: bool) -> World {
    review_alone_with(dark, review())
}

/// [`review_alone`] showing `bundle`.
fn review_alone_with(dark: bool, bundle: ReviewBundle) -> World {
    let mut world = review_beside_agent_with(dark, bundle);
    click(&mut world, Target::key("tile.m.focus"));
    focus_review(&mut world);
    world
}

/// The review alone, its Go code with no language server to read it, and the keys that set
/// one up beside that.
fn review_server_missing(dark: bool) {
    let mut bundle = review();
    bundle.navigation = vec![attn_protocol::ReviewNavigation {
        language: "go".into(),
        file: "main.go".into(),
        state: "missing".into(),
        ..Default::default()
    }];
    let mut world = review_alone_with(dark, bundle);
    snap(&mut world, "review-server-missing", dark);
}

/// The review in its tile beside the agent it reviews for, as a desktop first shows it.
fn review_beside_agent(dark: bool) -> World {
    review_beside_agent_with(dark, review())
}

/// [`review_beside_agent`] showing `bundle`.
fn review_beside_agent_with(dark: bool, bundle: ReviewBundle) -> World {
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
    deliver(&mut world, "/repo/review.md", REVIEW, Some(bundle));
    deliver_to(&mut world, "n", "/repo/notes.md", "# Notes\n", None);
    world
}

/// The review popped out into a window 400 points wide, with a sequence diagram whose arrow
/// points to a stop, so its key line offers all three frames.
fn review_popped_narrow(dark: bool) {
    let fence = REVIEW.lines().count() as i64 + 2;
    let document = format!(
        "{REVIEW}\n```mermaid\nsequenceDiagram\n  participant poll as Poll\n  participant totals as Totals\n  poll->>totals: Sum(rows)\n```\n"
    );
    let mut bundle = review();
    let stop = bundle.stops[0].heading_line;
    bundle.flows = vec![ReviewFlow {
        line: fence,
        drawn: true,
        participants: ["poll", "totals"]
            .map(|id| ReviewFlowParticipant {
                id: id.into(),
                label: id.into(),
                actor: false,
            })
            .to_vec(),
        messages: vec![ReviewFlowMessage {
            line: fence + 4,
            from: "poll".into(),
            to: "totals".into(),
            arrow: "->>".into(),
            text: "Sum(rows)".into(),
            stop_line: Some(stop),
            ..ReviewFlowMessage::default()
        }],
        ..ReviewFlow::default()
    }];
    let mut tile = document_tile("m", "/repo/review.md");
    tile["window"] = json!({"x": 10.0, "y": 20.0, "width": 400.0, "height": 760.0});
    let daemon = scripted().state(initial(dark, row(vec![pane("a"), tile]), vec![]));
    serve_totals(&daemon);
    let mut world = start(daemon);
    deliver(&mut world, "/repo/review.md", &document, Some(bundle));
    snap_tile_window(&mut world, "review-popped-narrow", dark);
}

fn review_beside(dark: bool) {
    let mut world = review_beside_agent(dark);
    focus_review(&mut world);
    snap(&mut world, "review-beside", dark);
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

/// A path two folders deep under a worktree whose basename is long, in Latin and in CJK.
const LONG_PATH: &str =
    "crates/build-and-release-platform-本番の権限を移行する作業ツリー/src/totals.rs";

/// The review's search for `query`, the daemon answering `answer`.
fn review_search_for(dark: bool, query: &str, answer: Value) -> World {
    let mut world = review_alone(dark);
    world
        .daemon
        .on("review_search", move |_: &Request| answer.clone());
    world.gesture(|app| app.type_text("/"));
    world.gesture(|app| app.type_text(query));
    world.settle();
    world
}

/// What a query finds: files by name and lines by text, the longest path and line, and the
/// limit the daemon says it reached.
fn review_search_found(dark: bool) {
    let mut world = review_search_for(
        dark,
        "sum",
        json!({"event": "review_search_result", "success": true,
            "files": ["totals.rs", LONG_PATH],
            "hits": [
                {"path": "totals.rs", "line": 3, "text": "    let total = sum_to(10);"},
                {"path": "totals.rs", "line": 7, "text": "fn sum_to(limit: u32) -> u32 {"},
                {"path": LONG_PATH, "line": 128, "text": "    // 合計は sum_to が上限まで数えて返す。上限は呼び出し側が名前で渡すので、範囲を二か所で書かずに済む。"},
                {"path": "README.md", "line": 12, "text": "`sum_to(3)` returns 6."}
            ],
            "problem": "over 200 lines match; the first 200 are shown. Search for something more specific."}),
    );
    snap(&mut world, "review-search-found", dark);
}

fn review_search_nothing(dark: bool) {
    let mut world = review_search_for(
        dark,
        "subtotal",
        json!({"event": "review_search_result", "success": true, "files": [], "hits": []}),
    );
    snap(&mut world, "review-search-nothing", dark);
}

fn review_search_failed(dark: bool) {
    let mut world = review_search_for(
        dark,
        "sum",
        json!({"event": "review_search_result", "success": false,
            "error": "git grep could not read the repository"}),
    );
    snap(&mut world, "review-search-failed", dark);
}

/// The review alone with its cursor on `let total = sum_to(10);`, whose names are labeled
/// a, s and d, the daemon answering code navigation with `answer`.
fn review_on_sum_to<R: attn_testkit::IntoReplies>(
    dark: bool,
    answer: impl Fn(&Request) -> R + Send + 'static,
) -> World {
    let mut world = review_alone(dark);
    world.daemon.on("code_nav", answer);
    tap(&mut world, Key::Right);
    for _ in 0..4 {
        tap(&mut world, Key::Down);
    }
    world
}

fn nav_place(path: &str, line: i64, text: &str) -> Value {
    json!({"path": path, "outside": path.starts_with('/'), "line": line, "character": 4,
        "end_line": line, "end_character": 10, "text": text})
}

/// The uses of `sum_to` listed, one outside the repository and one of a long path.
fn review_nav_uses(dark: bool) {
    let mut world = review_on_sum_to(dark, |_: &Request| {
        let mut places = vec![
            nav_place("totals.rs", 3, "    let total = sum_to(10);"),
            nav_place(LONG_PATH, 128, "    let 合計 = sum_to(上限);"),
            nav_place(
                "/usr/lib/rustlib/src/rust/library/core/src/iter/traits/accum.rs",
                149,
                "        fn sum<I: Iterator<Item = Self>>(iter: I) -> Self {",
            ),
        ];
        places.extend((0..30).map(|i| {
            nav_place(
                &format!("benches/sum_{i}.rs"),
                i + 10,
                "    b.iter(|| sum_to(black_box(1_000)));",
            )
        }));
        json!({"event": "code_nav_result", "success": true, "language": "rust", "locations": places})
    });
    world.gesture(|app| app.type_text("gr"));
    world.gesture(|app| app.type_text("d"));
    snap(&mut world, "review-nav-uses", dark);
}

fn review_nav_type(dark: bool) {
    let mut world = review_on_sum_to(dark, |_: &Request| {
        json!({"event": "code_nav_result", "success": true, "language": "rust",
            "text": "pub fn sum_to(limit: u32) -> u32\n\nSums the numbers from one to `limit`, both ends included. Returns 0 when `limit` is 0."})
    });
    world.gesture(|app| app.type_text("K"));
    world.gesture(|app| app.type_text("d"));
    snap(&mut world, "review-nav-type", dark);
}

/// The note line while the names on the line are labeled.
fn review_nav_picking(dark: bool) {
    let mut world = review_on_sum_to(dark, |_: &Request| json!({}));
    world.gesture(|app| app.type_text("gd"));
    snap(&mut world, "review-nav-picking", dark);
}

/// The note line saying why a question got no answer.
fn review_nav_failed(dark: bool) {
    let mut world = review_on_sum_to(dark, |_: &Request| {
        json!({"event": "code_nav_result", "success": false,
            "error": "rust-analyzer stopped before it answered"})
    });
    world.gesture(|app| app.type_text("gd"));
    world.gesture(|app| app.type_text("d"));
    snap(&mut world, "review-nav-failed", dark);
}

/// The note line offering an answer that arrived after the cursor moved on.
fn review_nav_offer(dark: bool) {
    let mut world = review_on_sum_to(dark, |_: &Request| ());
    world.gesture(|app| app.type_text("gd"));
    world.gesture(|app| app.type_text("d"));
    tap(&mut world, Key::Up);
    world.advance(std::time::Duration::from_secs(2));
    let asked = world.daemon.sent_of("code_nav")[0].clone();
    world.daemon.reply_to(
        &asked,
        json!({"event": "code_nav_result", "success": true, "language": "rust",
            "locations": [nav_place("totals.rs", 7, "fn sum_to(limit: u32) -> u32 {")]}),
    );
    world.settle();
    snap(&mut world, "review-nav-offer", dark);
}

/// The review's file view with the edit box open on `let total = sum_to(10);`, the daemon
/// answering the save with `answer`.
fn review_editing(dark: bool, answer: Value) -> World {
    let mut world = review_alone(dark);
    world
        .daemon
        .on("review_edit", move |_: &Request| answer.clone());
    tap(&mut world, Key::Right);
    tap(&mut world, Key::Enter);
    for _ in 0..4 {
        tap(&mut world, Key::Down);
    }
    world.gesture(|app| app.type_text("i"));
    world
}

fn review_edit(dark: bool) {
    let mut world = review_editing(dark, json!({}));
    snap(&mut world, "review-edit", dark);
}

fn review_edit_refused(dark: bool) {
    let mut world = review_editing(
        dark,
        json!({"event": "review_edit_result", "success": false,
            "error": "cannot write totals.rs: permission denied"}),
    );
    world.gesture(|app| app.type_text(" // named"));
    tap(&mut world, Key::Enter);
    snap(&mut world, "review-edit-refused", dark);
}

/// The review's document with a sequence diagram of every part the diagram draws: a person,
/// a name on two lines, a block with a section, a caption, a call to itself, a dashed answer
/// and a lost one, and a label long enough to wrap.
const REVIEW_MAP: &str = "
```mermaid after
sequenceDiagram
  actor you as Reader
  participant main as main<br/>totals.rs
  participant sum as sum_to
  you->>main: cargo run
  alt limit given
  main->>sum: sum_to(10)
  sum->>sum: (1..=limit).sum() over every number from one up to the limit
  else no limit
  main-xsum: sum_to()
  end
  sum-->>main: 55
```
";

fn review_map_document() -> String {
    format!("{REVIEW}{REVIEW_MAP}")
}

fn review_map_line(text: &str) -> i64 {
    review_map_document()
        .lines()
        .position(|l| l.contains(text))
        .expect("a line of the map's document") as i64
        + 1
}

fn review_map_bundle() -> ReviewBundle {
    let line = review_map_line;
    let participant = |id: &str, label: &str, actor: bool| ReviewFlowParticipant {
        id: id.into(),
        label: label.into(),
        actor,
    };
    let message = |at: &str, from: &str, to: &str, arrow: &str, text: &str, stop: Option<i64>| {
        ReviewFlowMessage {
            line: line(at),
            from: from.into(),
            to: to.into(),
            arrow: arrow.into(),
            text: text.into(),
            stop_line: stop,
            ..ReviewFlowMessage::default()
        }
    };
    ReviewBundle {
        frame: "map".into(),
        flows: vec![ReviewFlow {
            line: line("```mermaid after"),
            side: Some("after".into()),
            drawn: true,
            participants: vec![
                participant("you", "Reader", true),
                participant("main", "main<br/>totals.rs", false),
                participant("sum", "sum_to", false),
            ],
            note: Some("main names the limit once; sum_to owns the range.".into()),
            messages: vec![
                message("you->>main", "you", "main", "->>", "cargo run", None),
                ReviewFlowMessage {
                    note: Some(
                        "the inline range moved into the helper, which names its limit".into(),
                    ),
                    ..message("main->>sum", "main", "sum", "->>", "sum_to(10)", Some(20))
                },
                message(
                    "sum->>sum",
                    "sum",
                    "sum",
                    "->>",
                    "(1..=limit).sum() over every number from one up to the limit",
                    Some(26),
                ),
                message("main-xsum", "main", "sum", "-x", "sum_to()", None),
                message("sum-->>main", "sum", "main", "-->>", "55", None),
            ],
            groups: vec![
                ReviewFlowGroup {
                    line: line("alt limit given"),
                    kind: "alt".into(),
                    label: "limit given".into(),
                    first_message: line("main->>sum"),
                    last_message: line("main-xsum"),
                    ..ReviewFlowGroup::default()
                },
                ReviewFlowGroup {
                    line: line("else no limit"),
                    kind: "else".into(),
                    label: "no limit".into(),
                    first_message: line("main-xsum"),
                    last_message: line("main-xsum"),
                    block: Some(line("alt limit given")),
                },
            ],
        }],
        ..review()
    }
}

/// The review's Map: its sequence diagram beside the story, the cursor on an arrow.
fn review_diagram(dark: bool) {
    let mut world = review_beside_agent(dark);
    deliver(
        &mut world,
        "/repo/review.md",
        &review_map_document(),
        Some(review_map_bundle()),
    );
    world.gesture(|app| app.click(Target::key("tile.m.focus")));
    focus_review(&mut world);
    tap(&mut world, Key::Right);
    snap(&mut world, "review-diagram", dark);
}

/// A note being written on the review's first block, the review alone.
fn review_composer(dark: bool) {
    let mut world = review_alone(dark);
    write_review_note(&mut world);
    snap(&mut world, "review-composer", dark);
}

/// A note being written on the review's first block, the review in its tile beside the agent.
fn review_composer_beside(dark: bool) {
    let mut world = review_beside_agent(dark);
    focus_review(&mut world);
    write_review_note(&mut world);
    snap(&mut world, "review-composer-beside", dark);
}

fn write_review_note(world: &mut World) {
    tap(world, Key::Down);
    world.gesture(|app| app.type_text("c"));
    world.gesture(|app| app.type_text("Name the case this misses."));
}

/// The send of the review, the review in its tile beside the agent.
fn review_send_beside(dark: bool) {
    let mut world = review_beside_agent(dark);
    focus_review(&mut world);
    press(&mut world, Action::MarkdownSendAnnotations);
    snap(&mut world, "review-send-beside", dark);
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
        click(&mut world, Target::key(key));
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
    click(
        &mut world,
        Target::key("automations.form.schedule-days-hour-trigger"),
    );
    world.gesture(|app| app.press("escape"));
    world.gesture(|app| app.press("down"));
    world.gesture(|app| app.press("up"));
    snap(&mut world, "automation-hour", dark);
}

/// The zones that match what is typed, with Down on the first.
fn automation_time_zones(dark: bool) {
    let mut world = automation_form(dark);
    click(&mut world, Target::key("automations.form.time-zone"));
    world.gesture(|app| {
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
    click(&mut world, Target::key("titlebar.automations"));
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
    click(&mut world, Target::key("automations.select.2"));
    snap(&mut world, "automations-selected", dark);
}

/// The scheduled automation's form, read from the daemon.
fn automation_edit(dark: bool) {
    let mut world = automations_open(dark, automations());
    click(&mut world, Target::key("automations.edit.2"));
    snap(&mut world, "automation-edit", dark);
}

/// A new automation's form on a pull request trigger.
fn automation_github(dark: bool) {
    let mut world = automations_open(dark, Vec::new());
    for key in ["automations.new", "automations.form.trigger-github"] {
        click(&mut world, Target::key(key));
    }
    snap(&mut world, "automation-github", dark);
}

/// A new manual automation's form with Advanced open.
fn automation_advanced(dark: bool) {
    let mut world = automations_open(dark, Vec::new());
    for key in ["automations.new", "automations.form.advanced"] {
        click(&mut world, Target::key(key));
    }
    snap(&mut world, "automation-advanced", dark);
}

/// Automations in trouble: one that works in a pull request's worktree, asked to run with no
/// pull request given, and one whose guard and last run failed, with a fix offered.
fn troubled_automations() -> Vec<AutomationDefinitionSummary> {
    vec![
        AutomationDefinitionSummary {
            id: 1,
            profile_id: PROFILE.into(),
            name: "Nightly dependency upgrade across every service repository".into(),
            enabled: true,
            revision: 1,
            trigger_type: "manual".into(),
            location_type: Some("repository_worktree".into()),
            updated_at: "2030-06-01T07:00:00Z".into(),
            ..AutomationDefinitionSummary::default()
        },
        AutomationDefinitionSummary {
            guard_check: Some(attn_protocol::AutomationGuardCheck {
                outcome: "failed".into(),
                checked_at: "2030-06-01T06:58:00Z".into(),
                checks_since_run: 3,
                detail: Some("git rev-parse HEAD: fatal: not a git repository (or any of the parent directories): .git".into()),
            }),
            last_run: Some(AutomationRunSummary {
                id: "r9".into(),
                definition_id: 2,
                state: "failed".into(),
                created_at: "2030-06-01T07:00:00Z".into(),
                updated_at: "2030-06-01T07:00:00Z".into(),
                last_error: Some("Codex does not trust /home/someone/projects/InternationalEngineeringObservability".into()),
                fix: Some(attn_protocol::AutomationRunFix {
                    agent: "codex".into(),
                    directory: "/home/someone/projects/InternationalEngineeringObservability".into(),
                }),
                ..AutomationRunSummary::default()
            }),
            ..scheduled_automation()
        },
    ]
}

/// The drawer's rows in trouble, with Run now's pull request prompt refused for want of a URL.
fn automations_troubled(world: &mut World, name: &str, dark: bool) {
    for key in ["automations.run-now.1", "automations.pr-run.1"] {
        click(world, Target::key(key));
    }
    snap(world, name, dark);
}

fn automations_trouble(dark: bool) {
    let mut world = automations_open(dark, troubled_automations());
    automations_troubled(&mut world, "automations-trouble", dark);
}

/// The smallest window at 150% text, where a drawer keeps 420 of 533 points: the rows in
/// trouble, then a new scheduled automation's form.
fn automations_smallest(dark: bool) {
    let mut daemon = automations_daemon(dark, troubled_automations());
    daemon = daemon.state({
        let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
        if let Some(set) = state.settings.as_mut() {
            set.insert("uiScale".into(), "1.5".into());
        }
        state
    });
    let mut world = start(daemon);
    world.resize((800.0, 600.0));
    click(&mut world, Target::key("titlebar.automations"));
    automations_troubled(&mut world, "automations-smallest", dark);
    for key in ["automations.new", "automations.form.trigger-scheduled"] {
        click(&mut world, Target::key(key));
    }
    snap(&mut world, "automation-smallest", dark);
}

/// The drawer when the daemon could not list the automations.
fn automations_failed(dark: bool) {
    let daemon = automations_daemon(dark, Vec::new());
    daemon.on("automation_definitions_get", |_: &Request| {
        message(AutomationDefinitionsResultMessage {
            success: false,
            error: Some("automation_definitions_get failed: database is locked".into()),
            ..AutomationDefinitionsResultMessage::new()
        })
    });
    let mut world = start(daemon);
    click(&mut world, Target::key("titlebar.automations"));
    snap(&mut world, "automations-failed", dark);
}

/// An automation's form when the daemon could not read it, with Retry.
fn automation_load_failed(dark: bool) {
    let daemon = automations_daemon(dark, automations());
    daemon.on("automation_definition_get", |_: &Request| {
        message(AutomationDefinitionResultMessage {
            success: false,
            error: Some("automation_definition_get failed: no automation 2 in this profile".into()),
            ..AutomationDefinitionResultMessage::new()
        })
    });
    let mut world = start(daemon);
    click(&mut world, Target::key("titlebar.automations"));
    click(&mut world, Target::key("automations.edit.2"));
    snap(&mut world, "automation-load-failed", dark);
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

/// When a time stands `minutes` before the tour's clock, as the daemon writes it.
fn minutes_ago(minutes: i64) -> String {
    let at = attn_testkit::WALL_START_MS / 1000 - minutes * 60;
    let (days, secs) = (at.div_euclid(86_400), at.rem_euclid(86_400));
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z - era * 146_097;
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = yoe + era * 400 + i64::from(m <= 2);
    format!(
        "{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}Z",
        secs / 3600,
        secs / 60 % 60,
        secs % 60
    )
}

/// Home at its fullest: an ask, an agent whose machine is unreachable, a fresh and a stale
/// activity line, the Chief, the snoozed group open with Unsnooze, and pull requests with every
/// mark, one repository folded away.
fn home_full(dark: bool) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    state.crew = Some(crew());
    let sessions = state.sessions.get_or_insert_default();
    sessions[0].activity = Some("running the frontend test suite".into());
    sessions[0].activity_at = Some(minutes_ago(0));
    sessions[4].activity = Some("waiting on the review of the landing".into());
    sessions[4].activity_at = Some(minutes_ago(45));
    sessions.push(Session {
        crew_member: Some("chief".into()),
        chief_of_staff: Some(true),
        ..session("chief", "working")
    });
    sessions.push(Session {
        turn_owed: Some(true),
        turn_opened_at: Some(minutes_ago(30)),
        turn_snoozed_until: Some(minutes_ago(-90)),
        ..session("nap", "idle")
    });
    state.desktops.get_or_insert_default().push(desktop(
        "work/desktop_home",
        "9",
        row(vec![pane("pc"), pane("pn")]),
        vec![
            agent_pane("work/desktop_home", "pc", "chief"),
            agent_pane("work/desktop_home", "pn", "nap"),
        ],
    ));
    state.asks = Some(vec![Ask {
        id: "a-cache".into(),
        session_id: "s0".into(),
        subject: "Which store should keep the cache once deploys stop emptying it?".into(),
        base_dir: "/repo".into(),
        created_at: "2026-11-17T09:00:00Z".into(),
        ..Ask::default()
    }]);
    let pr = |id: &str, number: i64, role: &str, repo: &str, title: &str| PR {
        id: id.into(),
        number,
        role: role.into(),
        title: title.into(),
        repo: repo.into(),
        author: if role == "author" { "me" } else { "someone" }.into(),
        reason: if role == "author" {
            "ready_to_merge"
        } else {
            "review_requested"
        }
        .into(),
        url: format!("https://github.com/{repo}/pull/{number}"),
        host: "github.com".into(),
        state: "waiting".into(),
        ..PR::default()
    };
    state.prs = Some(vec![
        PR {
            has_new_changes: true,
            ci_status: Some("failure".into()),
            ..pr(
                "a1",
                7,
                "author",
                "victorarias/attn",
                "Route every key through the keymap, so a rebinding reaches the palette, the \
                 sidebar and every dialog that answers a key of its own",
            )
        },
        PR {
            approved_by_me: true,
            ci_status: Some("success".into()),
            ..pr("r1", 12, "reviewer", "org/shop", "Cache the catalog")
        },
        PR {
            has_new_changes: true,
            approved_by_me: true,
            ci_status: Some("pending".into()),
            ..pr("r2", 13, "reviewer", "org/shop", "Bump serde")
        },
        pr("r3", 4, "reviewer", "org/api", "Add the orders endpoint"),
        PR {
            host: "ghe.corp.example".into(),
            url: "https://ghe.corp.example/org/shop/pull/12".into(),
            ..pr(
                "r4",
                12,
                "reviewer",
                "org/shop",
                "Cache the catalog on the mirror",
            )
        },
    ]);
    let mut world = World::start_sized(scripted().state(state), window());
    press(&mut world, Action::SessionGoToDashboard);
    world.gesture(|app| app.click(Target::key("home.group.snoozed.toggle")));
    world.gesture(|app| app.click(Target::key("home.prs.repo.org/api.fold")));
    world.gesture(|app| app.move_to((1.0, 1.0)));
    snap(&mut world, "home-full", dark);
    let [cx, cy, cw, ch] = world
        .screen()
        .bounds(Target::key("home.cards.scroll"))
        .expect("home's cards are on screen");
    while world
        .screen()
        .bounds(Target::key("home.pr.r4.open"))
        .is_some_and(|[_, y, _, h]| y + h > cy + ch)
    {
        let scrolled = world.screen().scrolled(Target::key("home.cards.scroll"));
        world.gesture(|app| app.runtime.wheel(cx + cw / 2.0, cy + ch / 2.0, -60.0));
        world.settle();
        assert_ne!(
            world.screen().scrolled(Target::key("home.cards.scroll")),
            scrolled,
            "the wheel scrolls home's cards toward the second host's pull request"
        );
    }
    let [x, y, w, h] = world.rect("home.pr.r4.host");
    world.gesture(|app| app.move_to(((x + w / 2.0) as f32, (y + h / 2.0) as f32)));
    world.advance(std::time::Duration::from_secs(2));
    snap(&mut world, "home-full-host-hover", dark);
}

/// Home on the first launch of a release whose tour the user already met: the notice that
/// offers it again, over the crew's Home.
fn home_whats_new(dark: bool) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    state.crew = Some(crew());
    let mut prefs = rune::prefs::Prefs::memory();
    prefs
        .set(
            attn_app::whats_new::SEEN_PREF,
            &attn_app::whats_new::RELEASE.to_owned(),
        )
        .expect("memory prefs keep every value");
    let mut world = World::start_sized_with_prefs(scripted().state(state), window(), prefs);
    press(&mut world, Action::SessionGoToDashboard);
    snap(&mut world, "home-whats-new", dark);
}

/// Manage crew with the Chief and scout, open on `member` at `tab`: a daemon that lists Claude
/// Code and Codex, and holds an empty charter and no handoffs.
fn manage_crew(dark: bool, member: &str, tab: &str, name: &str) {
    let mut world = crew_page(dark);
    click(&mut world, Target::key(format!("crew.roster.{member}")));
    click(&mut world, Target::key(format!("crew.tab.{tab}")));
    snap(&mut world, name, dark);
}

/// Manage crew open, over a daemon that also lists the desktops a member can launch on.
fn crew_page(dark: bool) -> World {
    crew_page_with(dark, false)
}

/// Manage crew in the smallest window at 150% text, then renaming a member there.
fn crew_smallest(dark: bool) {
    let mut world = crew_page_with(dark, true);
    snap(&mut world, "crew-smallest", dark);
    click(&mut world, Target::key("crew.rename"));
    snap(&mut world, "crew-renaming", dark);
}

fn crew_page_with(dark: bool, smallest: bool) -> World {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    if smallest && let Some(settings) = state.settings.as_mut() {
        settings.insert("uiScale".into(), "1.5".into());
    }
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
        settings.insert("default_model_claude".into(), "fable".into());
    }
    let daemon = scripted().state(state);
    daemon.set_harnesses(attn_testkit::built_in_harnesses());
    daemon
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
    picker_models(&daemon);
    let mut world = start(daemon);
    if smallest {
        world.resize((800.0, 600.0));
    }
    run(&mut world, Action::OpenCrew);
    world
}

/// Naming the new desktop the Chief launches on, from its Desktop list.
fn desktop_naming(dark: bool) {
    let mut world = crew_page(dark);
    click(&mut world, Target::key("crew.tab.launch"));
    world
        .automate(
            "press",
            json!({"target": {"key": "crew.launch.chief.desktop-trigger"}}),
        )
        .expect("the Desktop list opens");
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
    click(&mut world, Target::key("titlebar.notifications"));
    snap(&mut world, "notifications", dark);
}

fn session_menu(dark: bool) {
    let mut world = with_document(dark);
    click(&mut world, Target::key("sidebar.session.s0.actions"));
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
    click(&mut world, Target::key("queue.agents-toggle"));
    snap(&mut world, "queue-agent-list", dark);
}

/// The queue flow with turns owed, as the audit seeded it: two agents waiting, the first with
/// an open ask, the first three desktops on shortcut slots, and the crew.
fn queue_seeded(dark: bool, home: bool, sidebar_hidden: bool, slots: usize) -> World {
    queue_seeded_and(dark, home, sidebar_hidden, slots, |_| {})
}

/// [`queue_seeded`], its daemon also answering what `answers` registers.
fn queue_seeded_and(
    dark: bool,
    home: bool,
    sidebar_hidden: bool,
    slots: usize,
    answers: impl FnOnce(&Daemon),
) -> World {
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
    let daemon = scripted().state(state);
    answers(&daemon);
    let mut world = World::start_sized(daemon, window());
    press(&mut world, Action::SessionGoToDashboard);
    if !home {
        click(&mut world, Target::key("queue.desktop.1"));
        world.gesture(|app| app.move_to((1100.0, 700.0)));
    }
    if sidebar_hidden {
        click(&mut world, Target::key("titlebar.sidebar"));
        world.gesture(|app| app.move_to((1100.0, 700.0)));
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

/// The queue bar's runs peek over an automation whose name is longer than its row. A bar too
/// crowded for the runs chip leaves it out, and then shows no peek.
fn queue_bar_runs_peek(dark: bool) {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    if let Some(settings) = state.settings.as_mut() {
        settings.insert("queue_mode_enabled".to_owned(), "true".to_owned());
    }
    for session in state.sessions.iter_mut().flatten().take(1) {
        session.state = "waiting_input".into();
        session.turn_owed = Some(true);
        session.turn_opened_at = Some("2026-11-12T08:55:00Z".into());
        session.automation = Some(AutomationProvenance {
            definition_id: 1,
            definition_name: "Nightly verify that deployment artifacts match the signed manifests and publish the staging release report".into(),
            run_id: "r1".into(),
            trigger_type: "scheduled".into(),
            ..AutomationProvenance::default()
        });
    }
    let mut world = World::start_sized(scripted().state(state), window());
    click(&mut world, Target::role("button").name("Collapse sidebar"));
    world.gesture(|app| {
        if let Some(chip) = app.runtime.find("queue-bar.runs") {
            app.move_to(chip);
        }
    });
    snap(&mut world, "queue-bar-runs-peek", dark);
}

/// The seeded queue with the sidebar collapsed: the queue bar in the title bar.
fn queue_bar(dark: bool) {
    let mut world = queue_seeded(dark, false, true, 3);
    snap(&mut world, "queue-bar", dark);
}

/// The queue bar with the pointer on its waiting pill: the agents under it in its peek.
fn queue_bar_peek(dark: bool) {
    let mut world = queue_seeded(dark, false, true, 3);
    world.gesture(|app| {
        let pill = app
            .runtime
            .find("queue-bar.pill")
            .expect("the bar shows its pill");
        app.move_to(pill);
    });
    assert!(
        world.screen().find(Target::key("queue-bar.peek")).is_some(),
        "the peek opens"
    );
    snap(&mut world, "queue-bar-peek", dark);
}

/// The seeded queue with an unread critical notification whose title runs long and an unread
/// warning: the bell's dot, and the critical strip in the queue bar with the sidebar collapsed or
/// above the sidebar's rows.
fn title_bar_critical(dark: bool, sidebar_hidden: bool) {
    let feed = vec![
        Notification {
            id: "n1".into(),
            kind: "task_failed".into(),
            severity: "critical".into(),
            title: "The nightly review automation failed three times in a row and stopped retrying"
                .into(),
            body: "exit status 1".into(),
            created_at: "2026-11-12T08:00:00Z".into(),
            ..Notification::default()
        },
        Notification {
            id: "n2".into(),
            kind: "task_failed".into(),
            severity: "warning".into(),
            title: "A delegate's worktree could not be cleaned".into(),
            body: "busy".into(),
            created_at: "2026-11-12T07:00:00Z".into(),
            ..Notification::default()
        },
    ];
    let mut world = queue_seeded_and(dark, false, sidebar_hidden, 3, move |daemon| {
        daemon.on("notification_list", move |r: &Request| {
            message(NotificationListResultMessage {
                request_id: r["request_id"].as_str().unwrap_or_default().into(),
                success: true,
                notifications: Some(feed.clone()),
                unread_count: 2,
                unread_critical_count: 1,
                critical_title: Some(feed[0].title.clone()),
                ..NotificationListResultMessage::new()
            })
        });
    });
    assert!(
        sidebar_hidden
            || world
                .screen()
                .find(Target::key("sidebar.critical"))
                .is_some(),
        "the critical strip shows above the sidebar's rows"
    );
    let name = if sidebar_hidden {
        "title-bar-critical-bar"
    } else {
        "title-bar-critical-sidebar"
    };
    snap(&mut world, name, dark);
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

/// The sidebar's rows in the smallest window at 150% text with the longest real titles, a
/// worktree's folder and a CJK title, in desktop flow and in queue flow: at rest, with the
/// pointer on the first agent's row, and with the keyboard on it.
fn chrome_smallest(dark: bool) {
    for queue in [false, true] {
        let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
        state.crew = Some(crew());
        let sessions = state.sessions.get_or_insert_default();
        sessions[0].label = "attn--ds-page-blocks-delegation-appearance-landing-two".into();
        sessions[1].label = "社内プラットフォームの観測性をすべてのプロファイルで見直す".into();
        if queue {
            for session in sessions.iter_mut().take(2) {
                session.state = "waiting_input".into();
                session.turn_owed = Some(true);
                session.turn_opened_at = Some("2026-11-12T08:55:00Z".into());
            }
        }
        if let Some(desktop) = state.desktops.get_or_insert_default().get_mut(1) {
            desktop.name = "Design system review with the Orchestrator and five Builders".into();
        }
        let settings = state.settings.get_or_insert_default();
        settings.insert("uiScale".to_owned(), "1.5".to_owned());
        if queue {
            settings.insert("queue_mode_enabled".to_owned(), "true".to_owned());
        }
        let mut world = World::start_sized(scripted().state(state), (800.0, 600.0));
        let (flow, row) = if queue {
            ("queue", "queue-turn.s0")
        } else {
            world.open_desktop();
            ("desktop", "sidebar.session.s0")
        };
        snap(&mut world, &format!("chrome-smallest-{flow}"), dark);
        let [x, y, w, h] = world.rect(row).map(|v| v as f32);
        world.gesture(|app| app.move_to((x + w / 4.0, y + h / 2.0)));
        snap(&mut world, &format!("chrome-smallest-{flow}-hover"), dark);
        world.gesture(|app| app.move_to((790.0, 590.0)));
        let target = if queue { "sidebar.queue" } else { row }.to_owned();
        world.gesture(|app| app.update(move |_, tx| tx.focus(target.clone(), true)));
        snap(&mut world, &format!("chrome-smallest-{flow}-focus"), dark);
    }
}

/// The desktop flow with the sidebar collapsed to its rail.
fn rail(dark: bool) {
    let mut world =
        start(scripted().state(initial(dark, document_tile("m", "/repo/plan.md"), vec![])));
    click(&mut world, Target::key("titlebar.sidebar"));
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
    click(&mut world, Target::key("tile.a.seed"));
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
    click(&mut world, Target::key("tile.m.document.toolbar.comment"));
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
    click(&mut world, Target::key("tile.m.document.notes"));
    world.settle();
    snap(&mut world, "annotation-notes-list", dark);
}

/// The menu of the Send of a document with one note: where else the notes can go.
fn annotation_send_menu(dark: bool) {
    let mut world = annotation_saved(dark);
    world.gesture(|app| app.click(Target::key("tile.m.document.send.menu")));
    world.settle();
    snap(&mut world, "annotation-send-menu", dark);
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
    let snooze = Target::key("queue-turn.s0.snooze");
    if world.screen().find(snooze.clone()).is_some() {
        click(&mut world, snooze);
    } else {
        press(&mut world, Action::SessionSnooze);
    }
    snap(&mut world, "snooze-menu", dark);
}

fn rename_session(dark: bool) {
    let mut world = plain(dark);
    click(&mut world, Target::key("tile.a.rename"));
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

/// The delegation chain pinned beside the sidebar by the Builder's role on its row.
fn session_chain_row(dark: bool) {
    let mut world = plain(dark);
    click(&mut world, Target::key("sidebar.session.s2.chain"));
    snap(&mut world, "session-chain-row", dark);
}

/// The delegation chain the pointer shows beside the sidebar, its dispatcher picked under the
/// pointer.
fn session_chain_hover(dark: bool) {
    let mut world = plain(dark);
    for key in [
        "sidebar.session.s2",
        "sidebar.session.s2.chain.popover.agent.0",
    ] {
        world.gesture(|app| {
            let [x, y, w, h] = app.runtime.presented(key).unwrap().0;
            app.move_to((x + w / 2.0, y + h / 2.0));
        });
    }
    snap(&mut world, "session-chain-hover", dark);
}

/// Terminal `a`'s agent was started by an automation run on a pull request and opened two of
/// its own, the newest with a title longer than the popover; its details pinned open.
fn session_pr_details(dark: bool) {
    let pull_request = |number: i64, state: &str, title: &str| SessionPullRequest {
        repository: "github.com/victorarias/attn".into(),
        number,
        url: format!("https://github.com/victorarias/attn/pull/{number}"),
        title: Some(title.into()),
        created_at: "2026-11-12T08:00:00Z".into(),
        state: state.into(),
        ..SessionPullRequest::default()
    };
    let mut world = popover_world(
        dark,
        |state| {
            let session = first_session(state);
            session.pull_requests = Some(vec![
                SessionPullRequest {
                    ci_status: Some("pending".into()),
                    review_status: Some("pending".into()),
                    mergeable_state: Some("clean".into()),
                    status_fetched_at: Some("2026-11-12T08:30:00Z".into()),
                    ..pull_request(
                        812,
                        "open",
                        "Chrome: the overview, the shelf, the peek and the seed chips draw through the blocks",
                    )
                },
                pull_request(790, "merged", "Header pieces measure what they draw"),
            ]);
            session.automation = Some(AutomationProvenance {
                run_id: "run-1".into(),
                definition_id: 1,
                definition_name: "Requested PR review - GPT Sol medium".into(),
                trigger_type: "github_review_requested".into(),
                ..AutomationProvenance::default()
            });
        },
        |_| {},
    );
    world.gesture(|app| app.click(Target::key("tile.a.details")));
    snap(&mut world, "session-pr-details", dark);
}

/// Terminal `a`'s agent orchestrates a chain three deep, its delegates named after their
/// worktrees; the role in its header pins the chain open.
fn session_chain_tile(dark: bool) {
    let mut world = popover_world(
        dark,
        |state| {
            let sessions = state.sessions.as_mut().expect("the tour's sessions");
            sessions[0].delegation_role = Some(attn_protocol::SessionDelegationRole {
                name: "Orchestrator".into(),
                icon: Some("compass".into()),
                ..Default::default()
            });
            sessions[1].dispatcher_session_id = Some("s0".into());
            sessions[1].label = "attn--ds-page-blocks-delegation-appearance".into();
            sessions[2].label = "attn--ds-chrome-overview-shelf-peek-seed-chips".into();
            sessions[3].dispatcher_session_id = Some("s0".into());
        },
        |_| {},
    );
    world.gesture(|app| app.click(Target::key("tile.a.role")));
    snap(&mut world, "session-chain-tile", dark);
}

/// Terminal `a`'s agent orchestrates through a sync planner that closed while its two Builders
/// still work: the planner keeps its place as a quiet closed row between them.
fn session_chain_closed(dark: bool) {
    let mut world = popover_world(
        dark,
        |state| {
            let sessions = state.sessions.as_mut().expect("the tour's sessions");
            sessions[0].delegation_role = Some(attn_protocol::SessionDelegationRole {
                name: "Orchestrator".into(),
                icon: Some("compass".into()),
                ..Default::default()
            });
            let planner = attn_protocol::ClosedDispatcher {
                entry: attn_protocol::SessionLedgerEntry {
                    id: "s-planner".into(),
                    label: "attn--notebook-sync-plan".into(),
                    agent: "claude".into(),
                    directory: "/repo".into(),
                    profile_id: sessions[0].profile_id.clone(),
                    closed_at: Some("2026-11-12T07:00:00Z".into()),
                    ..Default::default()
                },
                delegation_role: Some(attn_protocol::SessionDelegationRole {
                    name: "Pathfinder".into(),
                    icon: Some("map".into()),
                    ..Default::default()
                }),
                dispatcher_session_id: Some("s0".into()),
                ..Default::default()
            };
            for (session, label) in sessions[1..3]
                .iter_mut()
                .zip(["attn--notebook-sync-push", "attn--notebook-sync-folders"])
            {
                session.dispatcher_session_id = Some("s-planner".into());
                session.closed_dispatchers = Some(vec![planner.clone()]);
                session.label = label.into();
            }
        },
        |_| {},
    );
    world.gesture(|app| app.click(Target::key("tile.a.role")));
    snap(&mut world, "session-chain-closed", dark);
}

/// Terminal `a`'s agent is alone in its chain, pinned open by the role in its header.
fn session_chain_lone(dark: bool) {
    let mut world = popover_world(
        dark,
        |state| {
            let session = first_session(state);
            session.delegation_role = Some(attn_protocol::SessionDelegationRole {
                name: "Builder".into(),
                icon: Some("code".into()),
                ..Default::default()
            });
        },
        |_| {},
    );
    world.gesture(|app| app.click(Target::key("tile.a.role")));
    snap(&mut world, "session-chain-lone", dark);
}

/// A chain of nine agents four levels deep pinned beside the sidebar: delegates named after their
/// dispatcher's worktree, every badge, a role with no icon whose name runs past any width, an
/// agent on an outpost, and a closed delegate on standby whose resume is blocked. The picked agent
/// waits for the user with a seed, a pull request and an ask.
fn session_chain_wide(dark: bool) {
    let mut world = popover_world(
        dark,
        |state| {
            let role = |name: &str, builtin: Option<&str>| attn_protocol::SessionDelegationRole {
                name: name.into(),
                builtin: builtin.map(Into::into),
                ..Default::default()
            };
            let sessions = state.sessions.as_mut().expect("the tour's sessions");
            for (i, label, dispatcher, agent_state, role) in [
                (
                    0,
                    "attn--native-rewrite",
                    None,
                    "working",
                    Some(role("Orchestrator", Some("orchestrator"))),
                ),
                (
                    1,
                    "attn--native-rewrite-sidebar",
                    Some("s0"),
                    "working",
                    Some(role("Builder", Some("builder"))),
                ),
                (
                    2,
                    "attn--native-rewrite-sidebar-dock",
                    Some("s1"),
                    "pending_approval",
                    Some(role("Reviewer", Some("reviewer"))),
                ),
                (
                    3,
                    "attn--native-rewrite-sidebar-dock-keys",
                    Some("s2"),
                    "idle",
                    None,
                ),
                (
                    4,
                    "attn--native-rewrite-tiles",
                    Some("s0"),
                    "waiting_input",
                    Some(role("Builder", Some("builder"))),
                ),
                (
                    5,
                    "build-and-release-platform-本番の権限を移行する作業ツリー-osaka-secondary",
                    Some("s4"),
                    "working",
                    Some(role("Data-migration verifier for the Tokyo region", None)),
                ),
                (
                    6,
                    "attn--native-rewrite-tiles-codex",
                    Some("s4"),
                    "unknown",
                    None,
                ),
                (
                    7,
                    "attn--native-rewrite-settings",
                    Some("s0"),
                    "scheduled",
                    Some(role("Prototyper", Some("prototyper"))),
                ),
            ] {
                let session = &mut sessions[i];
                session.label = label.into();
                session.dispatcher_session_id = dispatcher.map(Into::into);
                session.state = agent_state.into();
                session.delegation_role = role;
            }
            sessions[1].host = Some("osaka-build-02".into());
            sessions[6].agent = "codex".into();
            let tiles = &mut sessions[4];
            tiles.turn_owed = Some(true);
            tiles.activity = Some(
                "Comparing each tile header piece with the width it measured at 150% text".into(),
            );
            tiles.seed_id = Some("s-tiles".into());
            tiles.pull_requests = Some(vec![SessionPullRequest {
                repository: "github.com/victorarias/attn".into(),
                number: 851,
                url: "https://github.com/victorarias/attn/pull/851".into(),
                title: Some("Tiles: the header measures what it draws".into()),
                created_at: "2026-11-12T07:00:00Z".into(),
                state: "open".into(),
                ci_status: Some("failure".into()),
                status_fetched_at: Some("2026-11-12T08:30:00Z".into()),
                ..SessionPullRequest::default()
            }]);
            let tiles_seed = Seed {
                step_slug: "tiles-measure-their-header".into(),
                ..seed("s-tiles", "Tiles measure their header", "growing", None)
            };
            let notices = Seed {
                step_slug: "notices-land-in-the-inbox".into(),
                standby: Some(attn_protocol::SeedStandbyView {
                    note: "Merge once CI is green".into(),
                    resume_blocked: Some("the harness is not installed here".into()),
                    set_at: "2026-11-12T07:30:00Z".into(),
                    tender: Some(attn_protocol::StandbyTender {
                        session_id: "s-gone".into(),
                        label: "attn--native-rewrite-settings-notices".into(),
                        agent: "claude".into(),
                        dispatcher_session_id: Some("s7".into()),
                        ..Default::default()
                    }),
                    ..Default::default()
                }),
                ..seed("s-notices", "Notices land in the inbox", "growing", None)
            };
            let seeds = state.seeds.get_or_insert_with(Vec::new);
            seeds.extend([tiles_seed, notices]);
            state.seeds_total = Some(seeds.len() as i64);
            state.asks = Some(vec![cache_ask("s4")]);
        },
        |_| {},
    );
    click(&mut world, Target::key("sidebar.session.s4.chain"));
    snap(&mut world, "session-chain-wide", dark);
}

/// Nine pull requests across three repositories on two hosts, pinned open: the picked one
/// watched for Codex while GitHub rate-limits the watch and Codex's review settles, one blocked
/// three ways, a draft with no title, and a title past two lines in two scripts.
fn session_pr_wide(dark: bool) {
    let pr = |repository: &str, number: i64, state: &str, created: &str, title: Option<&str>| {
        SessionPullRequest {
            repository: repository.into(),
            number,
            url: format!("https://{repository}/pull/{number}"),
            title: title.map(Into::into),
            created_at: created.into(),
            state: state.into(),
            status_fetched_at: Some("2026-11-12T08:30:00Z".into()),
            ..SessionPullRequest::default()
        }
    };
    let (api, web, infra) = (
        "github.com/acme/api",
        "github.com/acme/web",
        "ghe.acme.dev/platform/infra",
    );
    let mut world = popover_world(
        dark,
        |state| {
            first_session(state).pull_requests = Some(vec![
                SessionPullRequest {
                    ci_status: Some("success".into()),
                    review_status: Some("pending".into()),
                    mergeable_state: Some("clean".into()),
                    watching: Some(true),
                    watch_mode: Some("codex".into()),
                    readiness_state: Some("waiting".into()),
                    readiness_reason: Some("codex_settling".into()),
                    settling_until: Some("2026-11-12T09:03:00Z".into()),
                    watch_health: Some("delayed".into()),
                    watch_error: Some("GitHub rate limit reached; resets at 14:20".into()),
                    watch_last_checked_at: Some("2026-11-12T08:51:00Z".into()),
                    ..pr(
                        api,
                        4521,
                        "open",
                        "2026-11-12T08:40:00Z",
                        Some("Rate-limit the export endpoint per token"),
                    )
                },
                SessionPullRequest {
                    ci_status: Some("failure".into()),
                    review_status: Some("changes_requested".into()),
                    mergeable_state: Some("dirty".into()),
                    watching: Some(true),
                    watch_mode: Some("formal-review".into()),
                    watch_reviewer: Some("octo-reviewer".into()),
                    readiness_state: Some("blocked".into()),
                    readiness_reason: Some("merge_conflict".into()),
                    ..pr(
                        web,
                        1207,
                        "open",
                        "2026-11-12T08:20:00Z",
                        Some("Export button shows the job's progress"),
                    )
                },
                SessionPullRequest {
                    ci_status: Some("pending".into()),
                    ..pr(
                        api,
                        4519,
                        "open",
                        "2026-11-12T08:00:00Z",
                        Some(
                            "Permissions: migrate the production deploy role in Tokyo, Osaka and Singapore to scoped grants (本番環境のデプロイ権限をスコープ付きの付与に移行する) and remove the legacy role",
                        ),
                    )
                },
                pr(infra, 88, "draft", "2026-11-12T07:40:00Z", None),
                SessionPullRequest {
                    ci_status: Some("success".into()),
                    review_status: Some("approved".into()),
                    mergeable_state: Some("clean".into()),
                    ..pr(
                        infra,
                        87,
                        "open",
                        "2026-11-12T07:20:00Z",
                        Some("Alert on export queue depth"),
                    )
                },
                pr(
                    web,
                    1206,
                    "open",
                    "2026-11-12T07:00:00Z",
                    Some("Fix: export modal loses focus on close"),
                ),
                pr(
                    api,
                    4502,
                    "merged",
                    "2026-11-11T12:00:00Z",
                    Some("Export: stream rows instead of buffering"),
                ),
                pr(
                    web,
                    1199,
                    "merged",
                    "2026-11-11T10:00:00Z",
                    Some("Export history page"),
                ),
                pr(
                    api,
                    4490,
                    "closed",
                    "2026-11-10T10:00:00Z",
                    Some("Spike: export as Parquet"),
                ),
            ]);
        },
        |_| {},
    );
    world.gesture(|app| app.click(Target::key("tile.a.details")));
    snap(&mut world, "session-pr-wide", dark);
}

fn session_usage(dark: bool) {
    let mut world = popover_world(
        dark,
        |state| first_session(state).usage = Some(usage()),
        |_| {},
    );
    click(&mut world, Target::key("tile.a.usage"));
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
    world.gesture(|app| app.type_text("Show working agent"));
    world.gesture(|app| app.press("enter"));
    snap(&mut world, "session-seed-context", dark);
}

/// A seed id the garden knows in agent a's terminal, the pointer resting on it, so its card
/// opens over the terminal.
fn terminal_seed_card(dark: bool) {
    let known = Seed {
        body: "Every view reads its colors from **one module** in attn-ui, so a theme \
               changes them all at once."
            .into(),
        tender: Some(PartyView {
            name: "Builder".into(),
            r#ref: "session:s1".into(),
            session_id: Some("s1".into()),
        }),
        claimed: true,
        ..seed("s-7k3f9m", "Colors in one place", "growing", None)
    };
    let mut world = popover_world(
        dark,
        |state| {
            state.seeds_total = Some(1);
            state.seeds = Some(vec![known]);
        },
        |_| {},
    );
    write(&mut world, "$ attn seed show s-7k3f9m\r\n");
    let at = cell(&world, 20, 0);
    world.gesture(|app| app.runtime.pointer_move(at.0, at.1));
    world.advance(std::time::Duration::from_millis(400));
    snap(&mut world, "terminal-seed-card", dark);
}

const LIVE_AGENT: &str = "3f9a2c4e-8b1d-4c6e-9a7f-2d5e8c1b4a6f";
const CLOSED_AGENT: &str = "c5e81f07-4a36-4f8f-b2d9-7701aa5c9e3d";

/// Agent a's terminal naming agent `s1` by a real id and a closed agent by its short one.
fn naming_agents(dark: bool) -> World {
    let mapped = Seed {
        last_execution_id: Some(CLOSED_AGENT.into()),
        ..seed("s-7k3f9m", "Map where uploads retry", "harvested", None)
    };
    let mut world = popover_world(
        dark,
        |state| {
            let sessions = state.sessions.as_mut().expect("the tour's sessions");
            sessions[1].id = LIVE_AGENT.into();
            sessions[1].delegation_role = Some(attn_protocol::SessionDelegationRole {
                name: "Builder".into(),
                builtin: Some("builder".into()),
                ..Default::default()
            });
            state.seeds_total = Some(1);
            state.seeds = Some(vec![mapped]);
        },
        |daemon| {
            daemon.on("resolve_sessions", |r: &Request| {
                message(ResolveSessionsResultMessage {
                    request_id: request_id(r),
                    success: true,
                    found: vec![ResolvedSession {
                        r#ref: attn_protocol::session_id::short(CLOSED_AGENT).into_owned(),
                        closed: SessionLedgerEntry {
                            id: CLOSED_AGENT.into(),
                            label: "Map where uploads retry".into(),
                            agent: "claude".into(),
                            branch: Some("upload-retry-map".into()),
                            profile_id: PROFILE.into(),
                            last_seen: "2026-11-12T06:00:00Z".into(),
                            closed_at: Some("2026-11-12T06:00:00Z".into()),
                            ..SessionLedgerEntry::default()
                        },
                    }],
                    ..ResolveSessionsResultMessage::new()
                })
            });
        },
    );
    let live = attn_protocol::session_id::short(LIVE_AGENT);
    let closed = attn_protocol::session_id::short(CLOSED_AGENT);
    write(
        &mut world,
        &format!("{live} builds the fix\r\n{closed} mapped it; its id is {CLOSED_AGENT}\r\n"),
    );
    world
}

/// A live agent's id in agent a's terminal with the pointer resting on it, so its card opens
/// there.
fn terminal_agent_card(dark: bool) {
    let mut world = naming_agents(dark);
    let at = cell(&world, 4, 0);
    world.gesture(|app| app.runtime.pointer_move(at.0, at.1));
    world.advance(std::time::Duration::from_millis(400));
    snap(&mut world, "terminal-agent-card", dark);
}

/// Agent a's terminal naming agents whose role has an icon, only a letter or that have none, and
/// the other forms of id, beside a commit hash.
fn terminal_role_icons(dark: bool) {
    const REVIEWER: &str = "24a1ae04-5d2c-4f7e-8b19-aa01bb02cc03";
    const GARDENER: &str = "ded1de86-3c4a-4b5e-9f60-718293a4b5c6";
    let role = |name: &str, builtin: Option<&str>| attn_protocol::SessionDelegationRole {
        name: name.into(),
        builtin: builtin.map(Into::into),
        ..Default::default()
    };
    let mut world = popover_world(
        dark,
        |state| {
            let sessions = state.sessions.as_mut().expect("the tour's sessions");
            sessions[1].id = LIVE_AGENT.into();
            sessions[1].delegation_role = Some(role("Builder", Some("builder")));
            sessions[2].id = REVIEWER.into();
            sessions[2].delegation_role = Some(role("Reviewer", Some("reviewer")));
            sessions[3].id = GARDENER.into();
            sessions[3].delegation_role = Some(role("Gardener", None));
            sessions[4].id = CLOSED_AGENT.into();
            state.seeds_total = Some(1);
            state.seeds = Some(vec![seed("s-7k3f9m", "Retry uploads", "growing", None)]);
        },
        |_| {},
    );
    let [builder, reviewer, gardener, plain] =
        [LIVE_AGENT, REVIEWER, GARDENER, CLOSED_AGENT].map(attn_protocol::session_id::short);
    write(
        &mut world,
        &format!(
            "Dispatched {builder} on s-7k3f9m; {reviewer} reviews it.\r\n\
             {gardener} tends it once {builder} lands.\r\n\
             {plain} has no role.\r\n\
             Old forms still work: {} and {REVIEWER}.\r\n\
             A commit 1d4e6f90 stays plain.\r\n",
            &LIVE_AGENT[..8]
        ),
    );
    snap(&mut world, "terminal-role-icons", dark);
}

fn terminal_labels(dark: bool) {
    const CHIEF: &str = "52617e5f-1f0a-4c11-8a51-3f2b6d1c9a01";
    const BUILDER: &str = "ded1de86-0c44-4b0a-9a3e-70f1a2b3c4d5";
    const REVIEWER: &str = "24a1ae04-5d2c-4f7e-8b19-aa01bb02cc03";
    let mut world = popover_world(
        dark,
        |state| {
            let sessions = state.sessions.as_mut().expect("the tour's sessions");
            sessions[0].label = "Agents' ids people can see, find and jump to".into();
            let named = [
                (CHIEF, "Chief", ("Chief", "spark")),
                (BUILDER, "Fix the retry uploader", ("Builder", "code")),
                (REVIEWER, "Review the upload fix", ("Reviewer", "list")),
            ];
            for (session, (id, label, (role, icon))) in sessions[1..].iter_mut().zip(named) {
                session.id = id.into();
                session.label = label.into();
                session.dispatcher_session_id = None;
                session.delegation_role = Some(attn_protocol::SessionDelegationRole {
                    name: role.into(),
                    icon: Some(icon.into()),
                    ..Default::default()
                });
            }
            state.seeds_total = Some(1);
            state.seeds = Some(vec![seed(
                "s-7k3f9m",
                "Colors in one place",
                "growing",
                None,
            )]);
        },
        |daemon| {
            daemon.on("resolve_sessions", |r: &Request| {
                message(ResolveSessionsResultMessage {
                    request_id: request_id(r),
                    success: true,
                    found: vec![ResolvedSession {
                        r#ref: attn_protocol::session_id::short(CLOSED_AGENT).into_owned(),
                        closed: SessionLedgerEntry {
                            id: CLOSED_AGENT.into(),
                            label: "Map where uploads retry".into(),
                            agent: "claude".into(),
                            branch: Some("upload-retry-map".into()),
                            profile_id: PROFILE.into(),
                            last_seen: "2026-11-12T06:00:00Z".into(),
                            closed_at: Some("2026-11-12T06:00:00Z".into()),
                            ..SessionLedgerEntry::default()
                        },
                    }],
                    ..ResolveSessionsResultMessage::new()
                })
            });
        },
    );
    let [chief, builder, reviewer, closed] =
        [CHIEF, BUILDER, REVIEWER, CLOSED_AGENT].map(attn_protocol::session_id::short);
    write(
        &mut world,
        &format!(
            "Dispatched {builder} on s-7k3f9m; {reviewer} reviews it.\r\n\
             {chief} asked for a summary once {builder} lands.\r\n\
             The map came from {closed}, closed this morning.\r\n\
             Old forms still work: {} and {REVIEWER}\r\n\
             A commit 1d4e6f90 stays plain.\r\n\r\n\
             $ attn agent list\r\n\
             {builder}  builder   wants approval  Fix the retry uploader\r\n\
             {reviewer}  reviewer  scheduled       Review the upload fix\r\n\
             {chief}  chief     waiting         Chief\r\n\
             {closed}  builder   closed          Map where uploads retry\r\n",
            &BUILDER[..8]
        ),
    );
    let mac = attn_testkit::mac();
    world.gesture(|app| {
        app.hold(rune::Mods {
            cmd: true,
            logo: mac,
            ctrl: !mac,
            shift: !mac,
            ..rune::Mods::default()
        })
    });
    press(&mut world, Action::TerminalLabelIds);
    snap(&mut world, "terminal-labels-lantern", dark);
    let theme = if dark { "dark" } else { "light" };
    world.daemon.emit(attn_protocol::SettingsUpdatedMessage {
        settings: Some(
            [("id_labels", "name"), ("theme", theme)]
                .map(|(key, value)| (key.to_owned(), value.into()))
                .into(),
        ),
        ..attn_protocol::SettingsUpdatedMessage::new()
    });
    world.settle();
    snap(&mut world, "terminal-labels-name", dark);
}

/// A closed agent's id with its card pinned, then the same card after its resume was refused.
fn terminal_closed_card(dark: bool) {
    let mut world = naming_agents(dark);
    world.daemon.on("session_reopen", |r: &Request| {
        message(SessionReopenResultMessage {
            request_id: request_id(r),
            error: Some("the worktree is gone".into()),
            reopen: Some(SessionReopen {
                reason: Some("the worktree ~/projects/api--upload-retry-map is gone".into()),
                actions: vec!["recreate_worktree_and_reopen".into()],
                ..SessionReopen::default()
            }),
            ..SessionReopenResultMessage::new()
        })
    });
    let at = cell(&world, 4, 1);
    world.gesture(|app| app.runtime.pointer_move(at.0, at.1));
    world.advance(std::time::Duration::from_millis(400));
    snap(&mut world, "terminal-closed-card", dark);
    world.gesture(|app| app.press("mod+i"));
    world.gesture(|app| app.press("r"));
    snap(&mut world, "terminal-closed-card-refused", dark);
}

/// A critical failure with its evidence and actions, a warning and a read notice; then the
/// failure opened.
fn notifications_listed(dark: bool) {
    let notification = |id: &str, severity: &str, title: &str| attn_protocol::Notification {
        id: id.into(),
        kind: "task_failed".into(),
        severity: severity.into(),
        title: title.into(),
        body: "The nightly dependency upgrade stopped before it opened its pull request.".into(),
        created_at: "2030-06-01T07:00:00Z".into(),
        ..attn_protocol::Notification::default()
    };
    let failed = attn_protocol::Notification {
        trigger: "Scheduled run of Nightly dependency upgrade".into(),
        impact: "No pull request was opened for the services repository tonight.".into(),
        cause: "Codex does not trust /home/someone/projects/InternationalEngineeringObservability"
            .into(),
        diagnostic: "stderr: error: this folder is not trusted; run codex trust to allow it".into(),
        actions: Some(vec![attn_protocol::NotificationAction {
            kind: "retry_task".into(),
            label: "Retry".into(),
            target_id: "task-1".into(),
        }]),
        ..notification("failed", "critical", "Nightly dependency upgrade failed")
    };
    let feed = vec![
        failed,
        notification("warn", "warning", "Disk is 92% full on studio"),
        attn_protocol::Notification {
            read_at: "2030-06-01T07:05:00Z".into(),
            ..notification("info", "info", "Codex 0.42 is ready to install")
        },
    ];
    let mut world = popover_world(
        dark,
        |_| {},
        move |daemon| {
            let feed = feed.clone();
            daemon.on("notification_list", move |r: &Request| {
                message(NotificationListResultMessage {
                    request_id: request_id(r),
                    success: true,
                    notifications: Some(feed.clone()),
                    ..NotificationListResultMessage::new()
                })
            });
        },
    );
    click(&mut world, Target::key("titlebar.notifications"));
    snap(&mut world, "notifications-listed", dark);
    click(&mut world, Target::key("notifications.row.failed.head"));
    snap(&mut world, "notifications-open", dark);
}

/// The panel when the daemon could not list the notifications.
fn notifications_failed(dark: bool) {
    let mut world = popover_world(
        dark,
        |_| {},
        |daemon| {
            daemon.on("notification_list", |r: &Request| {
                message(NotificationListResultMessage {
                    request_id: request_id(r),
                    success: false,
                    error: Some("notification_list failed: database is locked".into()),
                    ..NotificationListResultMessage::new()
                })
            });
        },
    );
    click(&mut world, Target::key("titlebar.notifications"));
    snap(&mut world, "notifications-failed", dark);
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
    click(&mut world, Target::key("titlebar.notifications"));
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
    click(&mut world, Target::key("home.pr.a1.merge"));
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
    click(&mut world, Target::key("home.prs.refresh"));
    world.gesture(|app| app.move_to((1.0, 1.0)));
    world.advance(std::time::Duration::from_secs(2));
    snap(&mut world, "home-pull-requests", dark);
    let [x, y, w, h] = world.rect("home.pr.r1");
    world.gesture(|app| app.move_to(((x + w / 2.0) as f32, (y + h / 2.0) as f32)));
    world.advance(std::time::Duration::from_secs(2));
    snap(&mut world, "home-pull-requests-hover", dark);
    world.gesture(|app| app.move_to((1.0, 1.0)));
    world.gesture(|app| app.update(|_, tx| tx.focus("home.pr.r1.open".to_owned(), true)));
    world.advance(std::time::Duration::from_secs(2));
    snap(&mut world, "home-pull-requests-selected", dark);
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
    click(&mut world, Target::role("button").name("Restart now"));
    snap(&mut world, "shared-codex-restart", dark);
}

/// The finders, the switcher and Quick Capture in their other states: a file search that
/// matches nothing, the switcher naming and deleting a profile, and Quick
/// Capture's recent messages.
fn floating_states(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::FileOpen);
    world.gesture(|app| app.type_text("nothing matches this"));
    snap(&mut world, "opener-no-match", dark);

    let mut world = with_document(dark);
    press(&mut world, Action::ProfileSwitch);
    world.gesture(|app| app.type_text("n"));
    snap(&mut world, "profile-switcher-new", dark);

    let mut world = with_document(dark);
    press(&mut world, Action::ProfileSwitch);
    tap(&mut world, Key::Backspace);
    snap(&mut world, "profile-switcher-delete", dark);

    let mut world = quick_capture_world(dark);
    world.capture_gesture(|panel| panel.click(Target::key("capture.recent")));
    snap_capture(&mut world, "quick-capture-recent", dark);
}

/// The smallest window at the largest text, where a dialog keeps 501 points.
const SMALLEST_TEXT: &str = "1.5";

/// The dialogs at their worst: the smallest window at 150% text, with the longest names and
/// refusals they meet.
fn dialogs_smallest(dark: bool) {
    let long =
        "pending_approval agent in worktree attn--ds-dialogs-palette-with-a-long-branch-name";
    let mut world = popover_world(
        dark,
        |state| {
            if let Some(settings) = state.settings.as_mut() {
                settings.insert("uiScale".into(), SMALLEST_TEXT.into());
            }
            first_session(state).label = long.into();
            state.shared_codex = Some(SharedCodexState {
                running_version: Some("0.162.1".into()),
                installed_version: Some("0.163.0-alpha.7+linux.landing-runner".into()),
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
    world.resize((800.0, 600.0));
    click(&mut world, Target::role("button").name("Restart now"));
    snap(&mut world, "shared-codex-restart-smallest", dark);

    let mut world = popover_world(
        dark,
        |state| {
            if let Some(settings) = state.settings.as_mut() {
                settings.insert("uiScale".into(), SMALLEST_TEXT.into());
            }
            first_session(state).label = long.into();
        },
        |_| {},
    );
    world.resize((800.0, 600.0));
    press(&mut world, Action::UiCommandPalette);
    world.gesture(|app| app.type_text("context window"));
    tap(&mut world, Key::Enter);
    world.gesture(|app| app.type_text("eight hundred thousand"));
    tap(&mut world, Key::Enter);
    snap(&mut world, "context-cap-smallest-refused", dark);
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
            cause: None,
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
        ..UpdateOutpostProgress::default()
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

/// The smallest window, 800 x 600, at the largest text size, 150%.
fn at_the_smallest_window(state: &mut InitialStateMessage) {
    state
        .settings
        .get_or_insert_default()
        .insert("uiScale".into(), "1.5".into());
}

const NO_SPACE: &str = "cat: write error: No space left on device";

/// A run that could not stage `hosts`, worded as the daemon words it.
fn could_not_stage(hosts: &[&str]) -> UpdateRun {
    let each: Vec<String> = hosts.iter().map(|h| format!("{h}: {NO_SPACE}")).collect();
    UpdateRun {
        id: "u1".into(),
        install_kind: "release_app".into(),
        log_path: "/tmp/u1.log".into(),
        phase: "failed".into(),
        outcome: Some(format!(
            "could not stage {}. attn did not restart. Log: /Users/sam/.attn/update/run-20261119.log",
            each.join("; ")
        )),
        outposts: hosts
            .iter()
            .map(|h| UpdateOutpostProgress {
                id: (*h).into(),
                name: (*h).into(),
                state: "failed".into(),
                detail: Some(NO_SPACE.into()),
                ..UpdateOutpostProgress::default()
            })
            .collect(),
        ..UpdateRun::default()
    }
}

/// Presses `key` when the window shows it, as a revision without that control leaves it out.
fn press_if_shown(world: &mut World, key: &str) {
    if world.screen().find(Target::key(key)).is_some() {
        click(world, Target::key(key));
    }
}

fn smallest_update_banner(dark: bool, name: &str, hosts: &[&str], open: bool) {
    let run = could_not_stage(hosts);
    let mut world = popover_world(
        dark,
        |state| {
            at_the_smallest_window(state);
            state.update_run = Some(run);
        },
        |_| {},
    );
    world.resize((800.0, 600.0));
    if open {
        press_if_shown(&mut world, "update-run.details");
    }
    snap(&mut world, name, dark);
}

/// An open run at the smallest window, its outposts as `outposts` says.
fn smallest_open_run(dark: bool, name: &str, phase: &str, outposts: Vec<UpdateOutpostProgress>) {
    let run = UpdateRun {
        id: "u1".into(),
        install_kind: "release_app".into(),
        log_path: "/tmp/u1.log".into(),
        phase: phase.into(),
        outposts,
        ..UpdateRun::default()
    };
    let mut world = start(popover_daemon(dark, at_the_smallest_window, |_| {}));
    world.resize((800.0, 600.0));
    world.daemon.emit(UpdateChangedMessage {
        run,
        ..UpdateChangedMessage::new()
    });
    world.settle();
    snap(&mut world, name, dark);
}

/// `count` outposts that each failed for a reason of its own.
fn distinct_errors(count: usize) -> Vec<UpdateOutpostProgress> {
    (1..=count)
        .map(|n| UpdateOutpostProgress {
            id: format!("o{n}"),
            name: format!("ci-runner-{n:02}.build-and-validation.platform.example.org"),
            state: "failed".into(),
            detail: Some(format!(
                "ssh: connect to host ci-runner-{n:02}.example.org port 22: Connection timed out"
            )),
            ..UpdateOutpostProgress::default()
        })
        .collect()
}

/// The shared Codex restart waiting on `count` sessions still starting, each named by a long
/// worktree, at the smallest window; with `open`, its details too.
fn smallest_codex_wait(dark: bool, name: &str, count: usize, open: bool) {
    let mut world = start(popover_daemon(
        dark,
        |state| {
            at_the_smallest_window(state);
            let sessions = state.sessions.get_or_insert_default();
            let template = sessions[0].clone();
            sessions.clear();
            let mut blockers = Vec::new();
            for n in 1..=count {
                let id = format!("waiting-{n}");
                sessions.push(Session {
                    id: id.clone(),
                    label: format!(
                        "attn--fix-native-terminal-focus-when-reopening-an-old-codex-session-{n}"
                    ),
                    ..template.clone()
                });
                blockers.push(SharedCodexBlocker {
                    session_id: id,
                    reason: "starting".into(),
                });
            }
            state.shared_codex = Some(SharedCodexState {
                blockers: Some(blockers),
                ..codex("pending")
            });
        },
        |_| {},
    ));
    world.resize((800.0, 600.0));
    if open {
        press_if_shown(&mut world, "codex-updates.details");
    }
    snap(&mut world, name, dark);
}

fn smallest_banners(dark: bool) {
    for count in [1, 12, 18, 100] {
        smallest_open_run(
            dark,
            &format!("banner-update-errors-{count}-smallest"),
            "outposts",
            distinct_errors(count),
        );
    }
    smallest_open_run(
        dark,
        "banner-update-staging-smallest",
        "staging",
        [
            "connecting",
            "building",
            "downloading",
            "sending",
            "verifying",
        ]
        .iter()
        .enumerate()
        .map(|(i, step)| UpdateOutpostProgress {
            id: format!("o{i}"),
            name: format!("ci-runner-{i:02}.build-and-validation.platform.example.org"),
            state: "staging".into(),
            detail: Some((*step).into()),
            ..UpdateOutpostProgress::default()
        })
        .collect(),
    );
    smallest_codex_wait(dark, "banner-codex-1-smallest", 1, false);
    smallest_codex_wait(dark, "banner-codex-12-smallest", 12, false);
    smallest_codex_wait(dark, "banner-codex-12-open-smallest", 12, true);
    let many = [
        "build-linux-primary.us-east-1.example.dev",
        "ci-runner-07.build-and-validation.platform.example.org",
        "studio",
    ];
    smallest_update_banner(dark, "banner-update-failed-smallest", &many[..1], false);
    smallest_update_banner(dark, "banner-update-many-failed-smallest", &many, false);
    smallest_update_banner(dark, "banner-update-many-failed-open-smallest", &many, true);
    let twelve: Vec<String> = (1..=12)
        .map(|n| format!("ci-runner-{n:02}.build-and-validation.platform.example.org"))
        .collect();
    let twelve: Vec<&str> = twelve.iter().map(String::as_str).collect();
    smallest_update_banner(dark, "banner-update-twelve-failed-smallest", &twelve, false);
    smallest_update_banner(
        dark,
        "banner-update-twelve-failed-open-smallest",
        &twelve,
        true,
    );
    let mut world = popover_world(
        dark,
        |state| {
            at_the_smallest_window(state);
            let mut blockers = Vec::new();
            for (n, session) in state.sessions.iter_mut().flatten().enumerate() {
                session.label = format!(
                    "attn--fix-native-terminal-focus-when-reopening-an-old-codex-session-{}",
                    n + 1
                );
                blockers.push(SharedCodexBlocker {
                    session_id: session.id.clone(),
                    reason: "starting".into(),
                });
            }
            state.shared_codex = Some(SharedCodexState {
                blockers: Some(blockers),
                ..codex("pending")
            });
        },
        |_| {},
    );
    world.resize((800.0, 600.0));
    snap(&mut world, "banner-codex-many-smallest", dark);
    press_if_shown(&mut world, "codex-updates.details");
    for _ in 0..40 {
        let focused = world.screen().focused().and_then(|n| n.key);
        if focused.is_some_and(|k| k.ends_with(".go") && k.contains("-4.")) {
            break;
        }
        world.gesture(|app| app.press("tab"));
    }
    snap(&mut world, "banner-codex-many-open-smallest", dark);
    let names = [
        "attn--fix-native-terminal-focus-when-reopening-an-old-codex-session-in-another-desktop",
        "共有エージェントの更新で保留中の承認要求を再起動後も保持するための回帰テスト追加",
    ];
    let mut world = popover_world(
        dark,
        |state| {
            at_the_smallest_window(state);
            for (session, name) in state.sessions.iter_mut().flatten().zip(names) {
                session.label = name.into();
            }
            let ids: Vec<String> = state
                .sessions
                .iter()
                .flatten()
                .take(2)
                .map(|s| s.id.clone())
                .collect();
            state.shared_codex = Some(SharedCodexState {
                blockers: Some(
                    ids.iter()
                        .zip(["starting", "turn"])
                        .map(|(id, reason)| SharedCodexBlocker {
                            session_id: id.clone(),
                            reason: reason.into(),
                        })
                        .collect(),
                ),
                ..codex("pending")
            });
        },
        |_| {},
    );
    world.resize((800.0, 600.0));
    snap(&mut world, "banner-codex-waiting-smallest", dark);
    for (name, open) in [
        ("banner-daemon-warnings-smallest", false),
        ("banner-daemon-warnings-open-smallest", true),
    ] {
        let mut world = popover_world(
            dark,
            |state| {
                at_the_smallest_window(state);
                state.warnings = Some(
                    START_WARNINGS
                        .iter()
                        .map(|message| attn_protocol::DaemonWarning {
                            code: "worker_recovery_partial".into(),
                            message: (*message).into(),
                        })
                        .collect(),
                );
            },
            |_| {},
        );
        world.resize((800.0, 600.0));
        if open {
            press_if_shown(&mut world, "daemon-warnings.details");
        }
        snap(&mut world, name, dark);
    }
}

/// Every start warning the daemon can send at once, worded as it words them.
const START_WARNINGS: [&str; 6] = [
    "PTY recovery skipped 3 workers due to transient unavailability.",
    "Deferred marking 4 tracked sessions idle because PTY recovery was incomplete.",
    "2 sessions can be recovered from a previous daemon run.",
    "Removed 5 non-recoverable sessions from a previous daemon run.",
    "6 tracked sessions were expected to be running but no worker was recovered; they were marked idle.",
    "Recovered workers were missing metadata for 1 sessions.",
];

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
        click(world, Target::key("codex-updates.install"));
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
    click(&mut world, Target::key("worktree-setup"));
    snap(&mut world, "session-not-created", dark);
}

/// The overview over a row of terminal `a`, the document, a stack of an agent over a seed, a
/// browser and an agent that waits, between a desktop above and an empty one below; then with the
/// empty desktop previewed.
fn overview(dark: bool) {
    let tile = |kind: &str, id: &str, params: &str| json!({"type": "tile", "tile_id": id, "tile_kind": kind, "tile_params": params});
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), seeds());
    {
        let desktops = state.desktops.as_mut().expect("the tour's desktops");
        let mut panes = vec![agent_pane(DESKTOP, "a", "s0")];
        panes.extend((1..4).map(|n| agent_pane(DESKTOP, &format!("p{n}"), &format!("s{n}"))));
        let stack = below(pane("p1"), tile("seed", "g", "s-one"));
        desktops[0] = desktop(
            DESKTOP,
            "2",
            row(vec![
                pane("a"),
                document_tile("m", "/repo/plan.md"),
                stack,
                tile("browser", "w", "https://docs.example.com/attn"),
                pane("p2"),
                pane("p3"),
            ]),
            panes,
        );
        desktops.truncate(1);
        let others = (4..STATES.len())
            .map(|n| agent_pane("d-above", &format!("p{n}"), &format!("s{n}")))
            .collect::<Vec<_>>();
        let row_above = row((4..STATES.len()).map(|n| pane(&format!("p{n}"))));
        desktops.push(Desktop {
            shortcut_slot: Some(1),
            ..desktop("d-above", "1", row_above, others)
        });
        desktops.push(Desktop {
            tree_json: String::new(),
            shortcut_slot: Some(3),
            ..desktop("d-empty", "3", Value::Null, vec![])
        });
    }
    let daemon = scripted().state(state);
    daemon.on("seed_document_get", |r: &Request| {
        message(SeedDocumentGetResultMessage {
            request_id: request_id(r),
            success: true,
            document: seeds()
                .into_iter()
                .find(|s| s.id == r["seed_id"])
                .map(|seed| SeedDocument {
                    seed,
                    ..SeedDocument::default()
                }),
            ..SeedDocumentGetResultMessage::new()
        })
    });
    let mut world = start(daemon);
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    write(&mut world, BOXES);
    press(&mut world, Action::DesktopOverview);
    snap(&mut world, "overview", dark);
    tap(&mut world, Key::Down);
    snap(&mut world, "overview-empty", dark);
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
        scene("sidebar_crew_and_empty", || sidebar_crew_and_empty(dark));
        scene("sidebar_automations", || sidebar_automations(dark));
        scene("tile_headers", || tile_headers(dark, false));
        scene("browser_tile", || browser_tile(dark));
        scene("tile_headers", || tile_headers(dark, true));
        scene("supervisor_chip", || supervisor_chip(dark, false));
        scene("supervisor_chip", || supervisor_chip(dark, true));
        scene("tile_focus", || tile_focus(dark));
        scene("shelf", || shelf(dark, false));
        scene("shelf", || shelf(dark, true));
        scene("shelf_peek", || shelf_peek(dark));
        scene("shelf_width_label", || shelf_width_label(dark));
        scene("shelf_stack", || shelf_stack(dark));
        scene("review_stop", || review_stop(dark));
        scene("garden", || garden(dark));
        scene("garden_dock", || garden_dock(dark));
        scene("garden_dock_plot", || garden_dock_plot(dark));
        scene("garden_seed", || garden_seed(dark));
        scene("garden_dock_seed", || garden_dock_seed(dark));
        scene("garden_empty", || garden_empty(dark));
        scene("garden_dock_empty", || garden_dock_empty(dark));
        scene("garden_board", || garden_board(dark));
        scene("garden_dock_search", || garden_dock_search(dark));
        scene("garden_dock_search_full", || garden_dock_search_full(dark));
        scene("garden_search_body", || garden_search_body(dark, false));
        scene("garden_search_body", || garden_search_body(dark, true));
        scene("garden_board_menu", || garden_board_menu(dark));
        scene("garden_board_plot", || garden_board_plot(dark));
        scene("garden_v2_list", || garden_v2_list(dark));
        scene("garden_v2_board", || garden_v2_board(dark));
        scene("garden_v2_dock", || garden_v2_dock(dark));
        scene("garden_seed_resume_unreachable", || {
            garden_seed_resume_unreachable(dark)
        });
        scene("garden_seed_tile", || garden_seed_tile(dark));
        scene("settings_page", || settings_page(dark));
        scene("settings_agents", || settings_agents(dark));
        scene("settings_agents_missing_hovered", || {
            settings_agents_missing_hovered(dark)
        });
        scene("shortcut_editor", || shortcut_editor(dark));
        scene("close_desktop", || close_desktop(dark));
        scene("palette", || palette(dark));
        scene("palette_no_match", || palette_no_match(dark));
        scene("opener", || opener(dark));
        scene("opener_outposts", || opener_outposts(dark, false));
        scene("opener_outpost_scope", || opener_outposts(dark, true));
        scene("context_cap", || context_cap(dark));
        scene("sender_pairing", || sender_pairing(dark));
        scene("terminal_overlays", || terminal_overlays(dark));
        scene("terminal_filter", || terminal_filter(dark));
        scene("tile_notices", || tile_notices(dark));
        scene("daemon_reconnecting", || daemon_reconnecting(dark));
        scene("daemon_start_failed", || daemon_start_failed(dark));
        scene("daemon_unresponsive", || {
            daemon_unresponsive(dark);
        });
        scene("daemon_unresponsive_details", || {
            daemon_unresponsive_details(dark)
        });
        scene("daemon_refused", || daemon_refused(dark));
        scene("home_unreachable", || home_unreachable(dark));
        scene("home_refused_build", || home_refused_build(dark));
        scene("remote_home_desktop", || remote_home_desktop(dark));
        scene("remote_home_launcher", || remote_home_launcher(dark));
        scene("remote_home_folder_chooser", || {
            remote_home_folder_chooser(dark)
        });
        scene("notice_failed", || notice_failed(dark));
        scene("leader", || leader(dark));
        scene("callout", || callout(dark));
        scene("tile_window", || tile_window(dark));
        scene("launcher_dialog", || launcher_dialog(dark));
        scene("launcher_dialog_last", || launcher_dialog_last(dark));
        scene("launcher_where", || launcher_where(dark));
        scene("launcher_empty_desktop", || launcher_empty_desktop(dark));
        scene("launcher_browse", || launcher_browse(dark));
        scene("automation_hour", || automation_hour(dark));
        scene("automation_time_zones", || automation_time_zones(dark));
        scene("picker_dialog", || picker_dialog(dark));
        scene("picker_no_harness", || picker_no_harness(dark));
        scene("picker_start_from", || picker_start_from(dark, 0));
        scene("picker_start_from_worktrees", || picker_start_from(dark, 4));
        scene("launcher_yolo_caption", || launcher_yolo_caption(dark));
        scene("launcher_missing_harness", || {
            launcher_missing_harness(dark)
        });
        scene("launcher_outposts", || launcher_outposts(dark, false, None));
        scene("launcher_outposts_inline", || {
            launcher_outposts(dark, true, None)
        });
        scene("launcher_outposts_browse", || {
            launcher_outposts(dark, false, Some("~/projects/"))
        });
        scene("picker_empty_desktop", || picker_empty_desktop(dark));
        scene("shortcuts", || shortcuts(dark));
        scene("agent_palette", || agent_palette(dark));
        scene("profile_switcher", || profile_switcher(dark));
        scene("whats_new", || whats_new(dark, 0, "whats-new"));
        scene("whats_new", || whats_new(dark, 1, "whats-new-more-colors"));
        scene("settings_analytics", || settings_analytics(dark));
        scene("settings_analytics_populated", || {
            settings_analytics_populated(dark, false)
        });
        scene("settings_analytics_cjk", || settings_analytics_cjk(dark));
        scene("settings_analytics_show_all", || {
            settings_analytics_show_all(dark)
        });
        scene("settings_analytics_smallest", || {
            settings_analytics_populated(dark, true)
        });
        scene("ledger", || ledger(dark));
        scene("asks", || asks(dark));
        scene("asks_commands", || asks_commands(dark));
        scene("asks_reopened", || asks_reopened(dark));
        scene("asks_empty", || asks_empty(dark));
        scene("asks_choices", || asks_choices(dark));
        scene("asks_sections", || asks_sections(dark, false));
        scene("asks_sections_stacked", || asks_sections(dark, true));
        scene("asks_waiting_one", || {
            asks_waiting(dark, 1, "asks-waiting-one")
        });
        scene("asks_waiting_many", || {
            asks_waiting(dark, 12, "asks-waiting-many")
        });
        scene("asks_preview", || asks_preview(dark));
        scene("asks_labels", || asks_labels(dark));
        scene("asks_panel_tabs", || asks_panel_tabs(dark, false));
        scene("asks_panel_tabs", || asks_panel_tabs(dark, true));
        scene("notebook", || notebook(dark));
        scene("notebook_tree", || notebook_tree(dark));
        scene("review_keys", || review_keys(dark));
        scene("review_file", || review_file(dark));
        scene("review_focused", || review_focused(dark));
        scene("review_walkthrough", || review_walkthrough(dark));
        scene("review_beside", || review_beside(dark));
        scene("review_popped_narrow", || review_popped_narrow(dark));
        scene("review_server_missing", || review_server_missing(dark));
        scene("review_search", || review_search(dark));
        scene("review_send_sheet", || review_send_sheet(dark));
        scene("review_search_found", || review_search_found(dark));
        scene("review_search_nothing", || review_search_nothing(dark));
        scene("review_search_failed", || review_search_failed(dark));
        scene("review_nav_uses", || review_nav_uses(dark));
        scene("review_nav_type", || review_nav_type(dark));
        scene("review_nav_picking", || review_nav_picking(dark));
        scene("review_nav_failed", || review_nav_failed(dark));
        scene("review_nav_offer", || review_nav_offer(dark));
        scene("review_edit", || review_edit(dark));
        scene("review_edit_refused", || review_edit_refused(dark));
        scene("review_diagram", || review_diagram(dark));
        scene("review_send_beside", || review_send_beside(dark));
        scene("review_composer", || review_composer(dark));
        scene("review_composer_beside", || review_composer_beside(dark));
        scene("ledger_seeded", || ledger_seeded(dark));
        scene("ledger_worktrees", || ledger_worktrees(dark));
        scene("ledger_smallest", || ledger_smallest(dark));
        scene("ledger_failed", || ledger_failed(dark));
        scene("chief_setup", || chief_setup(dark));
        scene("home", || home(dark));
        scene("home_empty", || home_empty(dark));
        scene("home_pull_requests", || home_pull_requests(dark));
        scene("home_full", || home_full(dark));
        scene("home_whats_new", || home_whats_new(dark));
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
        scene("queue_bar_runs_peek", || queue_bar_runs_peek(dark));
        scene("queue_bar_crowded", || queue_bar_crowded(dark));
        scene("queue_bar_peek", || queue_bar_peek(dark));
        scene("title_bar_critical_bar", || title_bar_critical(dark, true));
        scene("title_bar_critical_sidebar", || {
            title_bar_critical(dark, false)
        });
        scene("rail", || rail(dark));
        scene("chrome_smallest", || chrome_smallest(dark));
        scene("session_seeds", || session_seeds(dark));
        scene("markdown_selection", || markdown_selection(dark));
        scene("annotation_selection", || annotation_selection(dark));
        scene("annotation_comment", || annotation_comment(dark));
        scene("annotation_saved_shot", || annotation_saved_shot(dark));
        scene("annotation_notes", || annotation_notes(dark));
        scene("annotation_send_menu", || annotation_send_menu(dark));
        scene("markdown_reader", || markdown_reader(dark));
        scene("markdown_missing", || markdown_missing(dark));
        scene("snooze_menu", || snooze_menu(dark));
        scene("rename_session", || rename_session(dark));
        scene("session_chain_row", || session_chain_row(dark));
        scene("session_chain_hover", || session_chain_hover(dark));
        scene("session_chain_tile", || session_chain_tile(dark));
        scene("session_chain_closed", || session_chain_closed(dark));
        scene("session_chain_lone", || session_chain_lone(dark));
        scene("session_pr_details", || session_pr_details(dark));
        scene("session_chain_wide", || session_chain_wide(dark));
        scene("session_pr_wide", || session_pr_wide(dark));
        scene("session_usage", || session_usage(dark));
        scene("session_seed_context", || session_seed_context(dark));
        scene("terminal_seed_card", || terminal_seed_card(dark));
        scene("terminal_agent_card", || terminal_agent_card(dark));
        scene("terminal_role_icons", || terminal_role_icons(dark));
        scene("terminal_closed_card", || terminal_closed_card(dark));
        scene("terminal_labels", || terminal_labels(dark));
        scene("notifications_empty", || notifications_empty(dark));
        scene("notifications_listed", || notifications_listed(dark));
        scene("notifications_failed", || notifications_failed(dark));
        scene("overview", || overview(dark));
        scene("settings_delegation", || settings_delegation(dark));
        scene("settings_delegation_table", || {
            settings_delegation_table(dark)
        });
        scene("settings_bottom", || {
            settings_bottom(dark, "general", "appearance", &[])
        });
        scene("settings_section", || {
            settings_section(dark, "machines", "machines", SWITCHED_ON, |_| {})
        });
        scene("settings_machines_move", || settings_machines_move(dark));
        scene("settings_machines_remove_dial_in", || {
            settings_machines_remove_dial_in(dark)
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
        scene("settings_missing_states", || settings_missing_states(dark));
        scene("settings_pricing", || settings_pricing(dark));
        scene("settings_route_picker", || settings_route_picker(dark));
        scene("route_picker_callers", || route_picker_callers(dark));
        scene("settings_refused_rows", || settings_refused_rows(dark));
        scene("settings_smallest_pages", || settings_smallest_pages(dark));
        scene("settings_plugin_priority_refused", || {
            settings_plugin_priority_refused(dark)
        });
        scene("automation_new", || automation_new(dark));
        scene("automations_empty", || automations_empty(dark));
        scene("automations_listed", || automations_listed(dark));
        scene("automations_selected", || automations_selected(dark));
        scene("automation_edit", || automation_edit(dark));
        scene("automation_github", || automation_github(dark));
        scene("automation_advanced", || automation_advanced(dark));
        scene("automations_trouble", || automations_trouble(dark));
        scene("automations_smallest", || automations_smallest(dark));
        scene("automations_failed", || automations_failed(dark));
        scene("automation_load_failed", || automation_load_failed(dark));
        scene("diagnostic_report", || diagnostic_report(dark));
        scene("diagnostic_report_save_failed", || {
            diagnostic_report_save_failed(dark)
        });
        scene("quick_capture_empty", || quick_capture_empty(dark));
        scene("quick_capture", || quick_capture(dark));
        scene("quick_capture_mailboxes", || quick_capture_mailboxes(dark));
        scene("notebook_file", || notebook_file(dark));
        scene("notebook_finder", || notebook_finder(dark));
        scene("notebook_note", || notebook_note(dark));
        scene("notebook_tile_empty", || notebook_tile_empty(dark));
        scene("notebook_tile_note", || notebook_tile_note(dark));
        scene("notebook_tile_finder", || notebook_tile_finder(dark));
        scene("notebook_tile_find", || notebook_tile_find(dark));
        for (tile, failure) in [
            (false, Failure::Conflict),
            (false, Failure::WriteError),
            (true, Failure::Conflict),
            (true, Failure::WriteError),
            (false, Failure::LongWriteError),
            (true, Failure::LongWriteError),
        ] {
            scene("notebook_failed_save", || {
                notebook_failed_save(dark, tile, failure)
            });
        }
        scene("merge_confirmation", || merge_confirmation(dark));
        scene("desktop_naming", || desktop_naming(dark));
        scene("crew_smallest", || crew_smallest(dark));
        scene("shared_codex_restart", || shared_codex_restart(dark));
        scene("session_not_created", || session_not_created(dark));
        scene("outposts_banners", || outposts_banners(dark));
        scene("update_banners", || update_banners(dark));
        scene("codex_banners", || codex_banners(dark));
        scene("smallest_banners", || smallest_banners(dark));
        scene("dialogs_smallest", || dialogs_smallest(dark));
        scene("floating_states", || floating_states(dark));
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
    let added: Vec<String> = ["clips.tsv", "overlaps.tsv"]
        .iter()
        .flat_map(|list| added_findings(&base, &after, list))
        .collect();
    for finding in &added {
        eprintln!("{finding}");
    }
    changed.is_empty() && added.is_empty()
}

/// The findings `list` holds in AFTER that it does not hold in BASE: each text `clips.tsv` lists
/// as cut, without the points it loses, and each pair `overlaps.tsv` lists as overprinted.
fn added_findings(base: &std::path::Path, after: &std::path::Path, list: &str) -> Vec<String> {
    let findings = |dir: &std::path::Path| -> std::collections::BTreeSet<String> {
        let lines = std::fs::read_to_string(dir.join(list)).unwrap_or_default();
        lines
            .lines()
            .filter_map(|line| match line.split('\t').collect::<Vec<_>>()[..] {
                [screen, "", _hidden, said] if list == "clips.tsv" => {
                    Some(format!("{list}: {screen}: {said}"))
                }
                [screen, "", a, b] => Some(format!("{list}: {screen}: {a} / {b}")),
                _ => None,
            })
            .collect()
    };
    findings(after)
        .difference(&findings(base))
        .map(|finding| format!("{finding} is new"))
        .collect()
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

/// Clicks `target` where a person would: scrolled into view when the window shows it only in
/// part, and pointed at before the press, so a control that shows on hover is there. Its middle
/// must then show the target itself or what it holds; a target still hidden or covered fails the
/// scene instead of clicking what lies there.
fn click(world: &mut World, target: Target) {
    let key = world
        .screen()
        .get(target.clone())
        .key
        .unwrap_or_else(|| panic!("{target:?} has no key to click by"));
    let seen = |app: &mut attn_testkit::Driver| {
        app.runtime
            .elements()
            .into_iter()
            .find(|e| e.key.as_deref() == Some(key.as_str()) && !e.exiting)
            .unwrap_or_else(|| panic!("{key} is not laid out"))
    };
    let mut middle = (0.0, 0.0);
    world.gesture(|app| {
        let it = seen(app);
        if it.visible != it.rect {
            app.update(|_, tx| tx.scroll_into_view(key.clone()));
        }
    });
    world.gesture(|app| {
        let [x, y, w, h] = seen(app).visible;
        middle = (x + w / 2.0, y + h / 2.0);
        app.move_to(middle);
    });
    world.gesture(|app| {
        let it = seen(app);
        let hit = app.runtime.element_at(middle.0, middle.1);
        assert!(
            it.visible[2] > 0.0
                && it.visible[3] > 0.0
                && hit
                    .as_ref()
                    .is_some_and(|hit| hit.path() == it.path
                        || hit.path().starts_with(&format!("{}/", it.path))),
            "{key} is out of view: a click at its middle lands on {:?}",
            hit.as_ref().map(|hit| hit.path().to_owned())
        );
        app.click_at(middle);
    });
}

/// Shows the notebook's file tree where its width has folded it away, as a person presses the
/// fold handle, so a scene can pick from the tree at any window size.
fn unfold_tree(world: &mut World, notebook: &str) {
    if world
        .screen()
        .find(Target::key(format!("{notebook}.files")))
        .is_none()
    {
        let fold = format!("{notebook}.fold.tree");
        click(world, Target::key(fold));
    }
}
