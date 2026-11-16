//! The screenshot tour: a fixed set of the app's screens, each drawn offscreen on the GPU in
//! dark and in light, one PNG each. `scripts/native-screenshot-tour` runs it.
//!
//!     screenshot_tour shoot OUT
//!     screenshot_tour compare BASE AFTER DIFF

use std::collections::BTreeMap;
use std::path::PathBuf;

use attn_app::screen::Screens;
use attn_app::{Action, Binding, Keymap, State};
use attn_protocol::{
    ActorView, DelegationPreferences, DelegationPreferencesResultMessage, Desktop, DesktopPane,
    DesktopTileContentMessage, GetCommandUsageResultMessage, GetRepoInfoResultMessage, Harness,
    InitialStateMessage, Profile, RecentLocation, RecentLocationsResultMessage, RepoInfo,
    ReviewBundle, ReviewFile, ReviewNote, ReviewRow, ReviewStop, Seed, SeedDocument,
    SeedDocumentGetResultMessage, SeedEdge, SeedPlotProgress, Session,
    SessionAnnotationsGetResultMessage, SessionAnnotationsSaveResultMessage, SessionMessage,
    SessionMessagesGetResultMessage, Worktree,
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
    Some(BTreeMap::from([("theme".to_owned(), theme.to_owned())]))
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
            ..seed("s-plot", "Catppuccin for attn", "growing", None)
        },
        seed("s-one", "Colors in one place", "growing", Some("s-plot")),
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

fn pixels(rt: &mut Runtime<State>) -> (u32, u32, Vec<u8>) {
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
    Screens::default().render(&mut Frame {
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
}

fn save(name: &str, dark: bool, (w, h, rgba): (u32, u32, Vec<u8>)) {
    let appearance = if dark { "dark" } else { "light" };
    let path = OUT.with_borrow(|out| out.join(format!("{name}-{appearance}.png")));
    write_png(&path, w, h, &rgba).unwrap_or_else(|e| panic!("{}: {e}", path.display()));
}

fn snap(world: &mut World, name: &str, dark: bool) {
    let mut shot = None;
    world.gesture(|app| shot = Some(pixels(app.runtime)));
    save(name, dark, shot.expect("the window drew"));
}

fn snap_tile_window(world: &mut World, name: &str, dark: bool) {
    let windows = world.tile_windows();
    let key = windows.first().expect("a tile window is open").key.clone();
    let mut shot = None;
    world.tile_gesture(&key, |app| shot = Some(pixels(app.runtime)));
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

fn review_stop(dark: bool) {
    let daemon =
        Daemon::scripted().state(initial(dark, document_tile("m", "/repo/review.md"), vec![]));
    let mut world = start(daemon);
    deliver(&mut world, "/repo/review.md", REVIEW, Some(review()));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    snap(&mut world, "review-stop", dark);
}

fn with_document(dark: bool) -> World {
    let daemon =
        Daemon::scripted().state(initial(dark, document_tile("m", "/repo/plan.md"), seeds()));
    let known = seeds();
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

fn settings_page(dark: bool) {
    let mut world = with_document(dark);
    press(&mut world, Action::UiOpenSettings);
    world.gesture(|app| app.click(Target::role("button").name("Appearance")));
    snap(&mut world, "settings", dark);
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
    for color in ["#a78bfa", "#3b82f6", "#4d9de0", "#f5c542"] {
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
            ("new_session_launcher_enabled", "true"),
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

fn launcher_empty_desktop(dark: bool) {
    let mut world = start(launcher_daemon(dark, true));
    snap(&mut world, "launcher-empty-desktop", dark);
}

fn shoot(out: PathBuf) {
    OUT.set(out);
    for dark in [true, false] {
        sidebar_and_terminal(dark);
        review_stop(dark);
        garden(dark);
        settings_page(dark);
        palette(dark);
        terminal_overlays(dark);
        tile_window(dark);
        launcher_dialog(dark);
        launcher_where(dark);
        launcher_empty_desktop(dark);
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
    let args: Vec<PathBuf> = std::env::args_os().skip(1).map(PathBuf::from).collect();
    let words: Vec<&str> = args.iter().filter_map(|a| a.to_str()).collect();
    match (words.first().copied(), &args[..]) {
        (Some("shoot"), [_, out]) => shoot(out.clone()),
        (Some("compare"), [_, base, after, diff]) => {
            if !compare(base.clone(), after.clone(), diff.clone()) {
                return std::process::ExitCode::FAILURE;
            }
        }
        _ => {
            eprintln!("usage: screenshot_tour shoot OUT | compare BASE AFTER DIFF");
            return std::process::ExitCode::from(2);
        }
    }
    std::process::ExitCode::SUCCESS
}
