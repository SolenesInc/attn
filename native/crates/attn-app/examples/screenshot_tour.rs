//! The screenshot tour: a fixed set of the app's screens, each drawn offscreen on the GPU in
//! dark and in light, one PNG each. `scripts/native-screenshot-tour` runs it.
//!
//!     screenshot_tour shoot OUT [SCHEME]   SCHEME sets the color_scheme setting
//!     screenshot_tour compare BASE AFTER DIFF

use std::collections::BTreeMap;
use std::path::PathBuf;

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
    DesktopTileContentMessage, DirectoryEntry, GetCommandUsageResultMessage,
    GetRepoInfoResultMessage, Harness, HarnessModel, HarnessModelsResultMessage,
    InitialStateMessage, InspectPathResultMessage, LaunchDesktopResultMessage,
    LaunchDesktopSetting, NotificationListResultMessage, PR, PartyView, PathInspection, PhoneLink,
    PhonesUpdatedMessage, Profile, RecentLocation, RecentLocationsResultMessage,
    RecordCommandUsageResultMessage, RepoInfo, ReviewBundle, ReviewFile, ReviewNote, ReviewRow,
    ReviewStop, Seed, SeedDocument, SeedDocumentGetResultMessage, SeedEdge, SeedNote,
    SeedPlotProgress, Session, SessionAnnotationsGetResultMessage,
    SessionAnnotationsSaveResultMessage, SessionLedgerEntry, SessionListResult,
    SessionListResultMessage, SessionMessage, SessionMessagesGetResultMessage, SessionUsage,
    SessionUsageModel, SharedCodexBlocker, SharedCodexState, Worktree, WorktreeListResult,
    WorktreeListResultEvent,
};
use attn_term::{FONT_SIZE, Fonts};
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
base: 4be1c0d
head: 9f3a7e2
---

# Name the sum

Add used to return its sum directly.

## Add keeps its result

### calc.go:3-6

- L4!: The sum gets a name.
- L5: Then it is returned.
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
    let mut world = World::start_sized(daemon, SIZE);
    world.open_desktop();
    world
}

fn deliver(world: &mut World, path: &str, content: &str, review: Option<ReviewBundle>) {
    world.daemon.emit(DesktopTileContentMessage {
        desktop_id: DESKTOP.into(),
        tile_id: "m".into(),
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
    let metrics = Fonts::default().metrics(FONT_SIZE);
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
    ReviewBundle {
        frame: "brief".into(),
        repository: "/repo".into(),
        base: Some("4be1c0d".into()),
        head: "9f3a7e2".into(),
        stops: vec![ReviewStop {
            heading_line: 15,
            path: "calc.go".into(),
            from: 3,
            to: 6,
            now_from: 3,
            now_to: 6,
            file_hash: "h1".into(),
            rows: vec![
                review_line(" ", Some(3), Some(3), "func Add(a, b int) int {"),
                review_line("-", Some(4), None, "\treturn a + b // the old way"),
                review_line("+", None, Some(4), "\tsum := a + b"),
                review_line("+", None, Some(5), "\treturn sum"),
                review_line(" ", Some(5), Some(6), "}"),
            ],
            notes: vec![
                review_note(17, 4, true, "The sum gets a name."),
                review_note(18, 5, false, "Then it is returned."),
            ],
            ..ReviewStop::default()
        }],
        files: vec![ReviewFile {
            path: "calc.go".into(),
            status: "modified".into(),
            added: 2,
            removed: 1,
            ..ReviewFile::default()
        }],
        ..ReviewBundle::default()
    }
}

fn pixels<S: 'static>(
    rt: &mut Runtime<S>,
    compositor: &mut impl Compositor<S>,
) -> (u32, u32, Vec<u8>) {
    let gpu = Gpu::headless();
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
    static OUT: std::cell::RefCell<PathBuf> = std::cell::RefCell::default();
    static SCHEME: std::cell::RefCell<Option<String>> = std::cell::RefCell::default();
}

fn save(name: &str, dark: bool, (w, h, rgba): (u32, u32, Vec<u8>)) {
    let appearance = if dark { "dark" } else { "light" };
    let path = OUT.with_borrow(|out| out.join(format!("{name}-{appearance}.png")));
    write_png(&path, w, h, &rgba).unwrap_or_else(|e| panic!("{}: {e}", path.display()));
}

fn snap(world: &mut World, name: &str, dark: bool) {
    let mut shot = None;
    world.gesture(|app| shot = Some(pixels(app.runtime, &mut Screens::default())));
    save(name, dark, shot.expect("the window drew"));
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
    let mut shot = None;
    world.capture_gesture(|panel| shot = Some(pixels(panel.runtime, &mut Interface)));
    save(name, dark, shot.expect("the capture panel drew"));
}

fn snap_tile_window(world: &mut World, name: &str, dark: bool) {
    let windows = world.tile_windows();
    let key = windows.first().expect("a tile window is open").key.clone();
    let mut shot = None;
    world.tile_gesture(&key, |app| {
        shot = Some(pixels(app.runtime, &mut Screens::default()))
    });
    save(name, dark, shot.expect("the tile window drew"));
}

fn sidebar_and_terminal(dark: bool) {
    let daemon =
        Daemon::scripted().state(initial(dark, document_tile("m", "/repo/plan.md"), vec![]));
    let mut world = start(daemon);
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    print_colors(&mut world);
    select(&mut world);
    snap(&mut world, "sidebar-terminal-markdown", dark);
}

/// An agent's header while a document tile is active: lit in the brand colour, with its one
/// seed's state in a pill and its open ask's chip, beside the document's header and controls;
/// the agent's terminal draws a rounded box beside a square one.
fn tile_headers(dark: bool) {
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
    if let Some(desktop) = state.desktops.iter_mut().flatten().next() {
        desktop.active_pane_id = "m".into();
    }
    let mut world = start(Daemon::scripted().state(state));
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    write(
        &mut world,
        "╭───────╮ ┌───────┐\r\n│ round │ │ box   │\r\n╰───────╯ └───────┘\r\n",
    );
    snap(&mut world, "tile-headers", dark);
}

fn review_stop(dark: bool) {
    let daemon =
        Daemon::scripted().state(initial(dark, document_tile("m", "/repo/review.md"), vec![]));
    let mut world = start(daemon);
    deliver(&mut world, "/repo/review.md", REVIEW, Some(review()));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    snap(&mut world, "review-stop", dark);
}

fn with_document(dark: bool) -> World {
    garden_world(dark, seeds())
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
    let daemon = Daemon::scripted().state(initial(
        dark,
        document_tile("m", "/repo/plan.md"),
        seeds.clone(),
    ));
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
    let daemon = Daemon::scripted().state(state);
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

fn tile_window(dark: bool) {
    let mut popped = document_tile("m", "/repo/plan.md");
    popped["window"] = json!({"x": 10.0, "y": 20.0, "width": 900.0, "height": 700.0});
    let daemon = Daemon::scripted().state(initial(dark, popped, vec![]));
    let mut world = start(daemon);
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
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
    let daemon = Daemon::scripted().state(state);
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
    let mut world = start(Daemon::scripted().state(state));
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
    let daemon = Daemon::scripted().state(state);
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

/// Quick Capture open with a message typed, so Send is its primary action. It keeps drafts
/// only for a profile whose id is a `profile-` uuid.
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
    let mut world = World::start_sized(Daemon::scripted().state(state), SIZE);
    run(&mut world, Action::OpenQuickCapture);
    world.capture_gesture(|panel| panel.type_text("Ship the cache fix"));
    world
}

fn quick_capture(dark: bool) {
    let mut world = quick_capture_world(dark);
    snap_capture(&mut world, "quick-capture", dark);
}

/// Quick Capture's mailbox list, with the chosen mailbox marked.
fn quick_capture_mailboxes(dark: bool) {
    let mut world = quick_capture_world(dark);
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
    let daemon = Daemon::scripted().state(state);
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
    let daemon = Daemon::scripted().state(state);
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
        .on("fs_exists", |req: &Request| {
            json!({ "event": "fs_exists_result", "success": true,
                "result": { "path": req["path"], "exists": false } })
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
    let daemon =
        Daemon::scripted().state(initial(dark, document_tile("m", "/repo/plan.md"), vec![]));
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
    let daemon = Daemon::scripted().state(initial(
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
    let daemon =
        Daemon::scripted().state(initial(dark, document_tile("m", "/repo/plan.md"), vec![]));
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

/// The Chief's setup, opened from its row while it has no harness yet.
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
        ] {
            settings.insert(key.into(), value.into());
        }
    }
    let mut world = World::start_sized(Daemon::scripted().state(state), SIZE);
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
    let mut world = start(Daemon::scripted().state(state));
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
    let mut world = start(Daemon::scripted().state(state));
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
    let mut world = start(Daemon::scripted().state(state));
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
    let daemon = Daemon::scripted().state(state);
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
    let daemon =
        Daemon::scripted().state(initial(dark, document_tile("m", "/repo/review.md"), vec![]));
    let mut world = start(daemon);
    deliver(&mut world, "/repo/review.md", REVIEW, Some(review()));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    tap(&mut world, Key::Right);
    tap(&mut world, Key::Enter);
    snap(&mut world, "review-file", dark);
}

fn review_keys(dark: bool) {
    let daemon =
        Daemon::scripted().state(initial(dark, document_tile("m", "/repo/review.md"), vec![]));
    let mut world = start(daemon);
    deliver(&mut world, "/repo/review.md", REVIEW, Some(review()));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    world.gesture(|app| app.type_text("?"));
    snap(&mut world, "review-keys", dark);
}

fn launcher_empty_desktop(dark: bool) {
    let mut world = start(launcher_daemon(dark, true));
    snap(&mut world, "launcher-empty-desktop", dark);
}

/// A new scheduled automation's form, with nothing defined yet and the daemon previewing
/// every schedule it is asked about.
fn automation_form(dark: bool) -> World {
    let daemon =
        Daemon::scripted().state(initial(dark, document_tile("m", "/repo/plan.md"), vec![]));
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
    let daemon =
        Daemon::scripted().state(initial(dark, document_tile("m", "/repo/plan.md"), vec![]));
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
    let mut world = start(Daemon::scripted().state(state));
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    world
}

/// Home, with the crew in the sidebar.
fn home(dark: bool) {
    let mut world = with_crew(dark);
    press(&mut world, Action::SessionGoToDashboard);
    snap(&mut world, "home", dark);
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
    let daemon = Daemon::scripted().state(state);
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
            json!({"target": {"role": "option", "name": "A new desktop…"}}),
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

fn attention(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::DockAttention);
    snap(&mut world, "attention", dark);
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
    let mut world = World::start_sized(Daemon::scripted().state(state), SIZE);
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
fn queue_seeded(dark: bool, home: bool, sidebar_hidden: bool) -> World {
    let mut state = initial(dark, document_tile("m", "/repo/plan.md"), vec![]);
    state.crew = Some(crew());
    if let Some(settings) = state.settings.as_mut() {
        settings.insert("queue_mode_enabled".to_owned(), "true".to_owned());
    }
    for (n, desktop) in state.desktops.iter_mut().flatten().enumerate() {
        desktop.shortcut_slot = (n < 3).then_some(n as i64 + 1);
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
    let mut world = World::start_sized(Daemon::scripted().state(state), SIZE);
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
    let mut world = queue_seeded(dark, true, false);
    snap(&mut world, "queue-seeded-home", dark);
}

/// The queue sidebar with the Chief between sessions and two automation runs that stopped
/// with a question.
fn queue_runs(dark: bool) {
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
    let mut world = World::start_sized(Daemon::scripted().state(state), SIZE);
    press(&mut world, Action::SessionGoToDashboard);
    snap(&mut world, "queue-runs", dark);
}

/// The seeded queue with the asking agent's desktop open: its row in view, its desktop current.
fn queue_seeded_agent(dark: bool) {
    let mut world = queue_seeded(dark, false, false);
    snap(&mut world, "queue-seeded-agent", dark);
}

/// The seeded queue with the sidebar collapsed: the queue bar in the title bar.
fn queue_bar(dark: bool) {
    let mut world = queue_seeded(dark, false, true);
    snap(&mut world, "queue-bar", dark);
}

/// The desktop flow with the sidebar collapsed to its rail.
fn rail(dark: bool) {
    let mut world =
        start(Daemon::scripted().state(initial(dark, document_tile("m", "/repo/plan.md"), vec![])));
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
        start(Daemon::scripted().state(initial(dark, document_tile("m", "/repo/plan.md"), seeds)));
    deliver(&mut world, "/repo/plan.md", DOCUMENT, None);
    world.gesture(|app| app.click(Target::key("tile.a.seed")));
    snap(&mut world, "session-seeds", dark);
}

/// A word of the document selected, with the annotation toolbar over it.
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
    let daemon = Daemon::scripted().state(state);
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
    let daemon = Daemon::scripted().state(state);
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
    let mut world = World::start_sized(daemon, SIZE);
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

/// The overview over two desktops: terminal `a` beside the document, and one agent.
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

fn desktop_overview(dark: bool) {
    let mut world = popover_world(
        dark,
        |state| {
            state
                .desktops
                .as_mut()
                .expect("the tour's desktops")
                .truncate(2);
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

fn shoot(out: PathBuf, scheme: Option<String>) {
    if let Some(id) = &scheme {
        let ids: Vec<&str> = attn_ui::colors::SCHEMES.iter().map(|s| s.id).collect();
        assert!(
            ids.contains(&id.as_str()),
            "{id} is not a color scheme; schemes: {ids:?}"
        );
    }
    OUT.set(out);
    SCHEME.set(scheme);
    for dark in [true, false] {
        sidebar_and_terminal(dark);
        tile_headers(dark);
        review_stop(dark);
        garden(dark);
        garden_dock(dark);
        garden_dock_plot(dark);
        garden_seed(dark);
        garden_dock_seed(dark);
        garden_empty(dark);
        garden_dock_empty(dark);
        settings_page(dark);
        settings_agents(dark);
        shortcut_editor(dark);
        close_desktop(dark);
        palette(dark);
        terminal_overlays(dark);
        tile_window(dark);
        launcher_dialog(dark);
        launcher_where(dark);
        launcher_empty_desktop(dark);
        launcher_browse(dark);
        automation_hour(dark);
        automation_time_zones(dark);
        picker_dialog(dark);
        picker_empty_desktop(dark);
        shortcuts(dark);
        agent_palette(dark);
        profile_switcher(dark);
        whats_new(dark, 0, "whats-new");
        whats_new(dark, 3, "whats-new-queue");
        settings_analytics(dark);
        ledger(dark);
        asks(dark);
        asks_empty(dark);
        asks_choices(dark);
        notebook(dark);
        notebook_tree(dark);
        review_keys(dark);
        review_file(dark);
        ledger_seeded(dark);
        ledger_worktrees(dark);
        chief_setup(dark);
        home(dark);
        palette_chief(dark);
        manage_crew(dark, "chief", "launch", "crew-launch");
        manage_crew(dark, "scout", "charter", "crew-charter");
        manage_crew(dark, "scout", "handoffs", "crew-handoffs");
        manage_crew(dark, "scout", "seeds", "crew-seeds");
        notifications(dark);
        attention(dark);
        session_menu(dark);
        queue_home(dark);
        queue_agent_list(dark);
        queue_seeded_home(dark);
        queue_runs(dark);
        queue_seeded_agent(dark);
        queue_bar(dark);
        rail(dark);
        session_seeds(dark);
        markdown_selection(dark);
        annotation_selection(dark);
        annotation_comment(dark);
        annotation_saved_shot(dark);
        annotation_notes(dark);
        snooze_menu(dark);
        rename_session(dark);
        session_usage(dark);
        session_seed_context(dark);
        notifications_empty(dark);
        desktop_overview(dark);
        settings_delegation(dark);
        settings_section(dark, "outposts", "outposts", SWITCHED_ON, |_| {});
        settings_section(dark, "experimental", "experimental", SWITCHED_ON, |_| {});
        settings_phones(dark);
        settings_section(dark, "plugins", "plugins", &[], |_| {});
        settings_updates(dark);
        settings_event_bus(dark);
        settings_section(dark, "desktop", "files", &[], |_| {});
        settings_section(dark, "hygiene", "attention queue", &[], |_| {});
        settings_section(dark, "backgroundAgents", "background agents", &[], |_| {});
        settings_section(dark, "backgroundTasks", "task runner", &[], |_| {});
        settings_connectivity(dark);
        settings_models(dark);
        settings_bottom(dark, "agents", "agents", &[]);
        settings_bottom(dark, "experimental", "experimental", SWITCHED_ON);
        automation_new(dark);
        automations_empty(dark);
        automations_listed(dark);
        automations_selected(dark);
        automation_edit(dark);
        automation_github(dark);
        automation_advanced(dark);
        diagnostic_report(dark);
        quick_capture(dark);
        quick_capture_mailboxes(dark);
        notebook_file(dark);
        notebook_finder(dark);
        notebook_note(dark);
        notebook_tile_empty(dark);
        notebook_tile_note(dark);
        notebook_tile_finder(dark);
        merge_confirmation(dark);
        desktop_naming(dark);
        shared_codex_restart(dark);
        session_not_created(dark);
    }
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
    let mut changed = Vec::new();
    for name in &names {
        let read =
            |dir: &PathBuf| Image::read_png(&dir.join(name)).unwrap_or_else(|e| panic!("{e}"));
        let (b, a) = (read(&base), read(&after));
        if (b.width, b.height) != (a.width, a.height) {
            changed.push(format!(
                "{name}: {}x{} became {}x{}",
                b.width, b.height, a.width, a.height
            ));
            continue;
        }
        let (differ, image) = marked(&b, &a);
        if differ > 0 {
            changed.push(format!("{name}: {differ} pixels differ"));
            std::fs::create_dir_all(&diff).expect("the diff folder");
            image.write_png(&diff.join(name)).expect("a diff image");
        }
        println!("{name}: {differ} pixels differ");
    }
    for change in &changed {
        eprintln!("{change}");
    }
    changed.is_empty()
}

fn main() -> std::process::ExitCode {
    // SAFETY: no other thread runs yet. A new schedule takes this machine's zone.
    unsafe { std::env::set_var("TZ", "Europe/Stockholm") };
    let args: Vec<PathBuf> = std::env::args_os().skip(1).map(PathBuf::from).collect();
    let words: Vec<&str> = args.iter().filter_map(|a| a.to_str()).collect();
    match (words.first().copied(), &args[..]) {
        (Some("shoot"), [_, out]) => shoot(out.clone(), None),
        (Some("shoot"), [_, out, scheme]) => shoot(out.clone(), scheme.to_str().map(str::to_owned)),
        (Some("compare"), [_, base, after, diff]) => {
            if !compare(base.clone(), after.clone(), diff.clone()) {
                return std::process::ExitCode::FAILURE;
            }
        }
        _ => {
            eprintln!("usage: screenshot_tour shoot OUT [SCHEME] | compare BASE AFTER DIFF");
            return std::process::ExitCode::from(2);
        }
    }
    std::process::ExitCode::SUCCESS
}
