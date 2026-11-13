use attn_protocol::{
    Desktop, DesktopPane, DesktopTileContentMessage, InitialStateMessage, Profile, Session,
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
        ..InitialStateMessage::new()
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
