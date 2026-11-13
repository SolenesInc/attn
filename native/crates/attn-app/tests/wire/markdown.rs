use attn_protocol::{
    Desktop, DesktopPane, DesktopTileContentMessage, InitialStateMessage,
    OpenMarkdownResultMessage, OutpostInfo, Profile, Session,
};
use attn_testkit::tree::{pane, row, tile};
use attn_testkit::{Daemon, Target, World};
use serde_json::json;

const DESKTOP: &str = "work/desktop_1";
const PLAN: &str = "/work/a/plan.md";

fn document_tile(id: &str, path: &str) -> serde_json::Value {
    json!({
        "type": "tile",
        "tile_id": id,
        "tile_kind": "markdown",
        "tile_params": json!({ "path": path }).to_string(),
    })
}

fn start() -> World {
    start_with(row(vec![pane("a"), document_tile("m", PLAN)]))
}

fn start_with(tree: serde_json::Value) -> World {
    start_with_state(tree, InitialStateMessage::new())
}

fn start_with_state(tree: serde_json::Value, extra: InitialStateMessage) -> World {
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: DESKTOP.into(),
            ..Profile::default()
        }]),
        sessions: Some(vec![Session {
            id: "s-a".into(),
            label: "Agent a".into(),
            directory: "/work/a".into(),
            profile_id: "work".into(),
            ..Session::default()
        }]),
        desktops: Some(vec![Desktop {
            id: DESKTOP.into(),
            profile_id: "work".into(),
            name: "Desktop 1".into(),
            shortcut_slot: Some(1),
            order_key: "1".into(),
            tree_json: tree.to_string(),
            active_pane_id: "a".into(),
            panes: vec![DesktopPane {
                desktop_id: DESKTOP.into(),
                pane_id: "a".into(),
                runtime_id: "r-a".into(),
                session_id: "s-a".into(),
                kind: "agent".into(),
                ..DesktopPane::default()
            }],
            revision: 1,
        }]),
        ..extra
    });
    let mut world = World::start_sized(daemon, (1600.0, 900.0));
    world.open_desktop();
    world
}

fn content(text: &str) -> DesktopTileContentMessage {
    DesktopTileContentMessage {
        desktop_id: DESKTOP.into(),
        tile_id: "m".into(),
        tile_kind: "markdown".into(),
        path: PLAN.into(),
        content: text.into(),
        ..DesktopTileContentMessage::new()
    }
}

/// The text the document tile shows, as assistive tech reads it, in reading order.
fn shown(world: &World) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key("tile.m"))
        .into_iter()
        .filter_map(|n| n.name.or(n.value))
        .collect()
}

#[test]
fn a_document_tile_renders_what_the_daemon_sends_and_follows_each_change() {
    let mut world = start();
    assert!(
        shown(&world).iter().any(|t| t == "Loading…"),
        "{:?}",
        shown(&world)
    );

    world.daemon.emit(content("# Plan\n\nFirst step."));
    world.settle();
    let first = shown(&world);
    assert!(first.iter().any(|t| t == "Plan"), "{first:?}");
    assert!(first.iter().any(|t| t == "First step."), "{first:?}");
    assert!(!first.iter().any(|t| t == "Loading…"), "{first:?}");
    let document = world.screen().get(Target::key("tile.m.document"));
    assert_eq!(
        (document.role.as_str(), document.name.as_deref()),
        ("document", Some("Plan"))
    );

    world
        .daemon
        .emit(content("# Plan\n\nAn edit the next one replaces."));
    world.daemon.emit(content("# Plan\n\nSecond step."));
    world.settle();
    let second = shown(&world);
    assert!(second.iter().any(|t| t == "Second step."), "{second:?}");
    assert!(!second.iter().any(|t| t == "First step."), "{second:?}");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_document_tile_says_when_its_file_is_empty_or_unreadable() {
    let mut world = start();
    world.daemon.emit(content("  \n"));
    world.settle();
    assert!(
        shown(&world).iter().any(|t| t == "This file is empty."),
        "{:?}",
        shown(&world)
    );

    world.daemon.emit(DesktopTileContentMessage {
        error: Some("open /work/a/plan.md: no such file or directory".into()),
        ..content("")
    });
    world.settle();
    assert!(
        shown(&world)
            .iter()
            .any(|t| t == "open /work/a/plan.md: no such file or directory"),
        "{:?}",
        shown(&world)
    );
    assert!(world.runtime_is_at_rest());
}

/// The name the document tile goes by on its frame and in the sidebar.
fn titles(world: &World) -> (String, String) {
    let screen = world.screen();
    let frame = screen.get(Target::key("tile.m")).name.unwrap_or_default();
    let row = screen
        .get(Target::key("sidebar.tile.work/desktop_1.m"))
        .name
        .unwrap_or_default();
    (frame, row)
}

#[test]
fn a_document_tile_is_named_by_its_heading_or_first_line_else_its_file() {
    let mut world = start();
    assert_eq!(titles(&world), ("plan.md".into(), "Open plan.md".into()));

    let long = format!("# {}", "word ".repeat(40).trim());
    let truncated = format!("{}…", "word ".repeat(16).trim());
    let cases: &[(&str, &str)] = &[
        ("# Project notes\n\nbody", "Project notes"),
        ("## **Setup** `steps`", "Setup steps"),
        ("### Closed heading ###", "Closed heading"),
        (
            "\n\nJust some plain notes here",
            "Just some plain notes here",
        ),
        ("---\ntitle: ignored\n---\n# Real title\n", "Real title"),
        ("---\nstill text", "still text"),
        (
            "See [the plan](plan.md) and ![a chart](c.png)",
            "See the plan and a chart",
        ),
        (&long, &truncated),
        (
            "\u{feff}# Saved with a byte order mark",
            "Saved with a byte order mark",
        ),
        ("   \n  ", "plan.md"),
    ];
    for (document, title) in cases {
        world.daemon.emit(content(document));
        world.settle();
        assert_eq!(
            titles(&world),
            (title.to_string(), format!("Open {title}")),
            "for {document:?}"
        );
    }
    assert!(truncated.chars().count() <= 80);

    world.daemon.emit(DesktopTileContentMessage {
        error: Some("boom".into()),
        ..content("# Unread heading")
    });
    world.settle();
    assert_eq!(titles(&world), ("plan.md".into(), "Open plan.md".into()));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_tile_with_no_file_goes_by_its_kind_and_an_empty_editor_by_editor() {
    let world = start_with(row(vec![
        pane("a"),
        tile("x", "markdown"),
        tile("n", "notebook"),
    ]));
    let screen = world.screen();
    let row = |tile: &str| {
        screen
            .get(Target::key(format!("sidebar.tile.work/desktop_1.{tile}")))
            .name
    };
    assert_eq!(row("x").as_deref(), Some("Open markdown"));
    assert_eq!(row("n").as_deref(), Some("Open Editor"));
}

/// Lets the pointer's tooltip and any notice run their course.
fn rest(world: &mut World) {
    world.advance(std::time::Duration::from_secs(7));
    assert!(world.runtime_is_at_rest());
}

fn notice(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("notice.text"))
        .and_then(|n| n.name)
}

#[test]
fn a_documents_links_open_what_is_safe_to_open_and_leave_the_rest_as_text() {
    let project = tempfile::tempdir().unwrap();
    let root = project.path().canonicalize().unwrap();
    std::fs::create_dir_all(root.join("docs")).unwrap();
    std::fs::create_dir_all(root.join("scripts")).unwrap();
    std::fs::write(root.join("docs/setup.md"), "# Setup").unwrap();
    std::fs::write(root.join("scripts/setup.command"), "rm -rf ~").unwrap();
    let readme = root.join("README.md").to_string_lossy().into_owned();
    let mut world = start();
    world.daemon.emit(DesktopTileContentMessage {
        path: readme,
        ..content(
            "[guide](docs/setup.md) [site](https://example.test/docs) \
             [run](scripts/setup.command) [script](javascript:alert(1)) [gone](docs/gone.md)",
        )
    });
    world.settle();

    world.gesture(|app| app.click(Target::key("docs/setup.md")));
    world.gesture(|app| app.click(Target::key("https://example.test/docs")));
    assert_eq!(
        world.opened_links(),
        [
            format!("file://{}/docs/setup.md", root.display()),
            "https://example.test/docs".into()
        ]
    );

    let [x, y, w, h] = world
        .screen()
        .bounds(Target::key("scripts/setup.command"))
        .unwrap();
    world.gesture(|app| app.move_to((x + w / 2.0, y + h / 2.0)));
    world.advance(std::time::Duration::from_secs(2));
    let blocked = format!(
        "Blocked local target: {}/scripts/setup.command",
        root.display()
    );
    assert!(
        world
            .screen()
            .find(Target::role("tooltip").name(blocked.clone()))
            .is_some(),
        "{blocked}:\n{}",
        world.screen()
    );
    world.gesture(|app| app.click(Target::key("scripts/setup.command")));
    world.gesture(|app| app.click(Target::key("javascript:alert(1)")));
    assert!(world.opened_links().is_empty());
    assert_eq!(notice(&world), None);

    world.gesture(|app| app.click(Target::key("docs/gone.md")));
    assert!(world.opened_links().is_empty());
    let gone = root.join("docs/gone.md");
    assert!(
        notice(&world)
            .is_some_and(|n| n.starts_with(&format!("Could not open {}: ", gone.display()))),
        "{:?}",
        notice(&world)
    );
    rest(&mut world);
}

#[test]
fn a_fragment_link_brings_its_heading_to_the_top_and_repeated_headings_count_up() {
    let mut world = start();
    let filler = "A paragraph that takes room.\n\n".repeat(40);
    world.daemon.emit(content(&format!(
        "[Jump](#setup-1)\n\n## ![logo](logo.png) Setup\n\n{filler}## Setup\n\n{filler}"
    )));
    world.settle();
    let screen = world.screen();
    let headings: Vec<_> = screen
        .children(Target::key("tile.m.document"))
        .into_iter()
        .filter(|n| n.role == "heading")
        .map(|n| n.key.unwrap_or_default())
        .collect();
    assert_eq!(headings, ["setup", "setup-1"]);
    assert_eq!(screen.scrolled(Target::key("tile.m.document")), Some(0.0));

    world.gesture(|app| app.click(Target::key("#setup-1")));
    let screen = world.screen();
    let view = screen.bounds(Target::key("tile.m.document")).unwrap();
    let heading = screen.bounds(Target::key("setup-1")).unwrap();
    assert!(screen.scrolled(Target::key("tile.m.document")).unwrap() > 0.0);
    assert!(
        (heading[1] - view[1]).abs() < 1.0,
        "{heading:?} in {view:?}"
    );
    assert!(world.opened_links().is_empty());
    rest(&mut world);
}

#[test]
fn a_link_in_a_document_on_an_outpost_opens_its_markdown_there_and_says_why_it_could_not() {
    let mut opened_for_a = document_tile("m", "/far/notes.md");
    opened_for_a["tile_session_id"] = json!("s-a");
    let mut world = start_with_state(
        row(vec![pane("a"), opened_for_a]),
        InitialStateMessage {
            outposts: Some(vec![OutpostInfo {
                id: "host-1".into(),
                name: "far".into(),
                destination: "far".into(),
                ..OutpostInfo::default()
            }]),
            ..InitialStateMessage::new()
        },
    );
    world.daemon.on("open_markdown", |r| {
        serde_json::to_value(OpenMarkdownResultMessage {
            request_id: r["request_id"].as_str().map(str::to_owned),
            path: r["path"].as_str().unwrap_or_default().into(),
            success: false,
            error: Some("far is unreachable; this needs it now".into()),
            ..OpenMarkdownResultMessage::new()
        })
        .unwrap()
    });
    world.daemon.emit(DesktopTileContentMessage {
        host: Some("host-1".into()),
        path: "/far/notes.md".into(),
        ..content("[next](/far/next.md) [log](/far/run.log)")
    });
    world.settle();

    world.gesture(|app| app.click(Target::key("/far/run.log")));
    assert!(world.daemon.sent_of("open_markdown").is_empty());

    world.gesture(|app| app.click(Target::key("/far/next.md")));
    let sent = world.daemon.sent_of("open_markdown");
    assert_eq!(sent.len(), 1);
    assert_eq!(
        (&sent[0]["path"], &sent[0]["host"], &sent[0]["session_id"]),
        (&json!("/far/next.md"), &json!("host-1"), &json!("s-a"))
    );
    assert_eq!(
        notice(&world).as_deref(),
        Some("Could not open /far/next.md from far: far is unreachable; this needs it now")
    );
    assert!(world.opened_links().is_empty());
    rest(&mut world);
}
