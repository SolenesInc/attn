use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    Desktop, DesktopPane, DesktopTileContentMessage, InitialStateMessage,
    OpenMarkdownResultMessage, OutpostInfo, Profile, ProfileArrangementChangedMessage, Session,
    TileAssetReadResultMessage,
};
use attn_testkit::tree::{pane, row, tile};
use attn_testkit::{Daemon, Target, World, key_event};
use rune::KeyPlatform;
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
        desktops: Some(vec![desktop(tree, 1)]),
        ..extra
    });
    let mut world = World::start_sized(daemon, (1600.0, 900.0));
    world.open_desktop();
    world
}

fn desktop(tree: serde_json::Value, revision: i64) -> Desktop {
    Desktop {
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
        revision,
    }
}

fn arrange(world: &mut World, tree: serde_json::Value, revision: i64) {
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: DESKTOP.into(),
            ..Profile::default()
        },
        desktops: vec![desktop(tree, revision)],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
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
        .descendants(Target::key("tile.m.document"))
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

fn escape(world: &mut World) {
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        })
    });
}

fn previewing(world: &World) -> bool {
    world
        .screen()
        .find(Target::key("image-preview.picture"))
        .is_some()
}

#[test]
fn escape_closes_the_opener_over_an_open_image_and_the_next_escape_closes_the_image() {
    let mut world = start();
    world.daemon.on("tile_asset_read", |r| {
        serde_json::to_value(TileAssetReadResultMessage {
            request_id: r["request_id"].as_str().map(str::to_owned),
            success: true,
            data_uri: Some(format!("data:image/png;base64,{RED}")),
            ..TileAssetReadResultMessage::new()
        })
        .unwrap()
    });
    world.daemon.emit(content("![chart](chart.png)"));
    world.settle();
    world.gesture(|app| app.click(Target::role("image").name("chart")));
    let open_file =
        match Keymap::from_setting(None, KeyPlatform::current()).binding(&Action::FileOpen) {
            Some(Binding::Chord(chord)) => key_event(&chord),
            other => panic!("the opener has no single chord: {other:?}"),
        };
    world.gesture(|app| app.key(open_file));
    let opener = Target::role("dialog").name("Open a markdown file");
    assert!(world.screen().find(opener.clone()).is_some());

    escape(&mut world);
    assert!(world.screen().find(opener).is_none());
    assert!(previewing(&world));

    escape(&mut world);
    assert!(!previewing(&world));
    rest(&mut world);
}

/// A 4×3 red PNG.
const RED: &str = "iVBORw0KGgoAAAANSUhEUgAAAAQAAAADCAYAAAC09K7GAAAAEklEQVR4nGP4z8DwHxkzEBQAAGHtF+mRbBMEAAAAAElFTkSuQmCC";

#[test]
fn a_documents_local_images_show_through_the_daemon_and_open_larger() {
    let mut world = start();
    world.daemon.on("tile_asset_read", |r| {
        let found = r["path"] == "/work/a/img/chart.png";
        serde_json::to_value(TileAssetReadResultMessage {
            request_id: r["request_id"].as_str().map(str::to_owned),
            success: found,
            data_uri: found.then(|| format!("data:image/png;base64,{RED}")),
            error: (!found).then(|| "no such file".to_owned()),
            ..TileAssetReadResultMessage::new()
        })
        .unwrap()
    });
    world.daemon.emit(content(
        "![chart](img/chart.png)\n\n![gone](img/gone.png)\n\n![script](run.command)\n\n\
         ![remote](https://example.test/x.png)",
    ));
    world.settle();

    let mut read: Vec<String> = world
        .daemon
        .sent_of("tile_asset_read")
        .iter()
        .map(|r| r["path"].as_str().unwrap_or_default().to_owned())
        .collect();
    read.sort();
    assert_eq!(read, ["/work/a/img/chart.png", "/work/a/img/gone.png"]);
    let pictures: Vec<_> = world
        .screen()
        .descendants(Target::key("tile.m.document"))
        .into_iter()
        .filter(|n| n.role == "image")
        .map(|n| n.name.unwrap_or_default())
        .collect();
    assert_eq!(pictures, ["chart"]);
    let shown = shown(&world);
    for words in [
        "[missing image: gone]",
        "[blocked image: script]",
        "[blocked image: remote]",
    ] {
        assert!(shown.iter().any(|t| t == words), "{words}: {shown:?}");
    }

    world.gesture(|app| app.click(Target::role("image").name("chart")));
    let preview = world.screen().get(Target::key("image-preview.picture"));
    assert_eq!(preview.name.as_deref(), Some("chart"));

    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        })
    });
    assert!(
        world
            .screen()
            .find(Target::key("image-preview.picture"))
            .is_none()
    );

    world.daemon.emit(content("# No pictures now"));
    world.settle();
    assert_eq!(world.daemon.sent_of("tile_asset_read").len(), 2);
    rest(&mut world);
}

/// A 3200×40 strip of a screenshot: in a reader's 760 point measure at 2×, 1600 pixels across show it all.
const WIDE_STRIP: &[u8] = include_bytes!("../fixtures/wide-strip.png");

/// The largest level of the picture the image element labelled `label` draws from.
fn picture_held(world: &World, label: &str) -> Option<(u32, u32)> {
    world
        .runtime()
        .elements()
        .into_iter()
        .find(|e| !e.exiting && e.role == "image" && e.label.as_deref() == Some(label))
        .unwrap_or_else(|| panic!("no image labelled {label} is drawn"))
        .picture
}

#[test]
fn a_documents_large_image_holds_the_size_it_draws_and_its_preview_the_whole_image_while_open() {
    use base64::Engine;
    let mut world = start();
    world.daemon.on("tile_asset_read", |r| {
        let data = base64::engine::general_purpose::STANDARD.encode(WIDE_STRIP);
        serde_json::to_value(TileAssetReadResultMessage {
            request_id: r["request_id"].as_str().map(str::to_owned),
            success: true,
            data_uri: Some(format!("data:image/png;base64,{data}")),
            ..TileAssetReadResultMessage::new()
        })
        .unwrap()
    });
    world.daemon.emit(content("![shot](shot.png)"));
    world.settle();
    assert_eq!(picture_held(&world, "shot"), Some((1600, 20)));

    world.gesture(|app| app.click(Target::role("image").name("shot")));
    world.settle();
    assert_eq!(
        world.daemon.sent_of("tile_asset_read").len(),
        2,
        "the preview reads the file again"
    );
    let preview = world
        .runtime()
        .elements()
        .into_iter()
        .find(|e| e.key.as_deref() == Some("image-preview.picture"))
        .expect("the preview shows the picture");
    assert_eq!(preview.picture, Some((3200, 40)));

    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        })
    });
    assert!(
        world
            .screen()
            .find(Target::key("image-preview.picture"))
            .is_none()
    );
    assert_eq!(picture_held(&world, "shot"), Some((1600, 20)));
    rest(&mut world);
}

#[test]
fn a_large_image_on_a_wide_document_tile_holds_the_width_it_draws_past_the_prose_measure() {
    use base64::Engine;
    let mut world = start();
    world.resize((3600.0, 900.0));
    world.daemon.on("tile_asset_read", |r| {
        let data = base64::engine::general_purpose::STANDARD.encode(WIDE_STRIP);
        serde_json::to_value(TileAssetReadResultMessage {
            request_id: r["request_id"].as_str().map(str::to_owned),
            success: true,
            data_uri: Some(format!("data:image/png;base64,{data}")),
            ..TileAssetReadResultMessage::new()
        })
        .unwrap()
    });
    world.daemon.emit(content("![shot](shot.png)"));
    world.settle();
    let [_, _, drawn, _] = world
        .screen()
        .bounds(Target::role("image").name("shot"))
        .unwrap();
    assert!(
        drawn > 1520.0,
        "the picture takes the wide column, past the measure: {drawn}"
    );
    assert_eq!(
        picture_held(&world, "shot"),
        Some((3200, 40)),
        "drawn {drawn} points across at 2x, the whole picture is held"
    );
    rest(&mut world);
}

/// An 8×2 green PNG.
const GREEN: &str = "iVBORw0KGgoAAAANSUhEUgAAAAgAAAACCAIAAADq9gq6AAAAEElEQVR4nGNg+M+AHeGSAACOsQ/xb1SVcwAAAABJRU5ErkJggg==";

#[test]
fn an_image_that_failed_stays_missing_until_its_document_changes() {
    let mut world = start();
    world.daemon.on("tile_asset_read", |r| {
        serde_json::to_value(TileAssetReadResultMessage {
            request_id: r["request_id"].as_str().map(str::to_owned),
            success: false,
            error: Some("no such file".to_owned()),
            ..TileAssetReadResultMessage::new()
        })
        .unwrap()
    });
    world.daemon.emit(content("![gone](img/gone.png)\n"));
    world.settle();
    world.daemon.emit(content("![gone](img/gone.png)\n"));
    world.settle();
    assert_eq!(world.daemon.sent_of("tile_asset_read").len(), 1);
    assert!(shown(&world).iter().any(|t| t == "[missing image: gone]"));

    world
        .daemon
        .emit(content("![gone](img/gone.png)\n\nNow written.\n"));
    world.settle();
    assert_eq!(world.daemon.sent_of("tile_asset_read").len(), 2);
}

#[test]
fn an_image_says_it_is_loading_until_the_daemon_answers() {
    let mut world = start();
    world.daemon.emit(content("![chart](img/chart.png)\n"));
    world.settle();
    let shown = shown(&world);
    assert!(
        shown.iter().any(|t| t == "[loading image: chart]"),
        "{shown:?}"
    );
}

#[test]
fn a_reopened_document_shows_its_image_as_the_file_is_now() {
    let mut world = start();
    let file = std::sync::Arc::new(std::sync::Mutex::new(RED));
    let on_disk = file.clone();
    world.daemon.on("tile_asset_read", move |r| {
        serde_json::to_value(TileAssetReadResultMessage {
            request_id: r["request_id"].as_str().map(str::to_owned),
            success: true,
            data_uri: Some(format!("data:image/png;base64,{}", on_disk.lock().unwrap())),
            ..TileAssetReadResultMessage::new()
        })
        .unwrap()
    });
    let chart_shape = |world: &World| {
        let [_, _, w, h] = world
            .screen()
            .bounds(Target::role("image").name("chart"))
            .unwrap();
        w / h
    };
    world.daemon.emit(content("![chart](chart.png)"));
    world.settle();
    assert_eq!(chart_shape(&world), 4.0 / 3.0);

    arrange(&mut world, row(vec![pane("a")]), 2);
    *file.lock().unwrap() = GREEN;
    arrange(
        &mut world,
        row(vec![pane("a"), document_tile("m", PLAN)]),
        3,
    );
    world.daemon.emit(content("![chart](chart.png)"));
    world.settle();

    assert_eq!(chart_shape(&world), 4.0);
    rest(&mut world);
}

#[test]
fn an_edited_document_shows_its_image_as_the_file_is_now() {
    let mut world = start();
    let file = std::sync::Arc::new(std::sync::Mutex::new(RED));
    let on_disk = file.clone();
    world.daemon.on("tile_asset_read", move |r| {
        serde_json::to_value(TileAssetReadResultMessage {
            request_id: r["request_id"].as_str().map(str::to_owned),
            success: true,
            data_uri: Some(format!("data:image/png;base64,{}", on_disk.lock().unwrap())),
            ..TileAssetReadResultMessage::new()
        })
        .unwrap()
    });
    let chart_shape = |world: &World| {
        let [_, _, w, h] = world
            .screen()
            .bounds(Target::role("image").name("chart"))
            .unwrap();
        w / h
    };
    world.daemon.emit(content("![chart](chart.png)"));
    world.settle();
    assert_eq!(chart_shape(&world), 4.0 / 3.0);

    *file.lock().unwrap() = GREEN;
    world.daemon.emit(content("# Chart\n\n![chart](chart.png)"));
    world.settle();

    assert_eq!(chart_shape(&world), 4.0);
    rest(&mut world);
}

#[test]
fn a_documents_front_matter_shows_as_a_card_of_its_top_level_fields() {
    let mut world = start();
    world.daemon.emit(content(
        "---\ntitle: \"Launch plan\"\ntags: [native, 'reader']\nowners:\n  - ana\n  - bo\n\
         nested:\n  deep: hidden\n# a comment\n---\n# Body\n",
    ));
    world.settle();
    let card: Vec<String> = world
        .screen()
        .descendants(Target::role("note").name("Front matter"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert_eq!(
        card,
        [
            "title:",
            "Launch plan",
            "tags:",
            "native",
            "reader",
            "owners:",
            "ana",
            "bo"
        ]
    );
    let shown = shown(&world);
    assert!(shown.iter().any(|t| t == "Body"), "{shown:?}");
    assert!(
        !shown
            .iter()
            .any(|t| t.contains("hidden") || t.contains("title: ")),
        "{shown:?}"
    );

    world
        .daemon
        .emit(content("---\n# only a comment\n---\n# Body\n"));
    world.settle();
    assert!(
        world
            .screen()
            .find(Target::role("note").name("Front matter"))
            .is_none()
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_long_front_matter_value_wraps_inside_its_card() {
    let mut world = start();
    let repository = "/srv/checkouts/attn--compare-code-review-prototype/14087774-f9b4-45e0-9633-7747f63a8141/scratchpad/subject";
    world.daemon.emit(content(&format!(
        "---\nframe: brief\nrepository: {repository}\n---\n# Body\n"
    )));
    world.settle();
    let screen = world.screen();
    let [_, _, _, line] = screen
        .bounds(Target::role("label").name("brief"))
        .expect("a short value");
    let [_, _, _, value] = screen
        .bounds(Target::role("label").name(repository))
        .expect("the repository value");
    assert!(
        value > line * 1.5,
        "the value is {value} high where a one-line value is {line}: it runs past the card"
    );
}

#[test]
fn a_long_code_line_stays_one_line() {
    let mut world = start();
    let long = "plugins/attn-pi/src/driver.ts      PiDriver (620 lines): the contract with the daemon lives here, and every report it sends goes through it";
    world
        .daemon
        .emit(content(&format!("```\nshort\n```\n\n```\n{long}\n```\n")));
    world.settle();
    let screen = world.screen();
    let height = |name: &str| {
        screen
            .bounds(Target::role("label").name(name))
            .unwrap_or_else(|| panic!("{name} is shown"))
    };
    let [_, _, _, short] = height("short");
    let [_, _, _, wide] = height(long);
    assert!(
        wide < short * 1.5,
        "the long line wraps: {wide} high, a short one {short}"
    );
}

/// The thumbs of the document's sideways scroll bars, in document order: where each draws and
/// how visible it is.
fn wide_thumbs(world: &World) -> Vec<([f32; 4], f32)> {
    world
        .runtime()
        .elements()
        .into_iter()
        .filter(|e| e.key.as_deref() == Some("thumb") && e.path.contains("wide-"))
        .map(|e| (e.rect, e.opacity))
        .collect()
}

#[test]
fn a_code_block_wider_than_the_document_shows_its_scroll_bar_and_one_that_fits_shows_none() {
    let mut world = start();
    let long = format!(
        "let wide = [{}];",
        (0..80)
            .map(|i| i.to_string())
            .collect::<Vec<_>>()
            .join(", ")
    );
    world
        .daemon
        .emit(content(&format!("```\nshort\n```\n\n```\n{long}\n```\n")));
    world.settle();
    let thumbs = wide_thumbs(&world);
    assert_eq!(thumbs.len(), 2, "a bar on each code block: {thumbs:?}");
    let [(short, _), (wide, _)] = thumbs[..] else {
        unreachable!()
    };
    assert_eq!(short[2], 0.0, "a block that fits shows no thumb");
    let line = world
        .screen()
        .bounds(Target::role("label").name(&long))
        .expect("the long line is shown");
    assert!(
        wide[2] > 0.0 && wide[2] < line[2],
        "the thumb is as long as the share of the line shown: {wide:?} of {line:?}"
    );
    assert!(
        wide[1] + wide[3] <= line[1] + line[3] + 24.0,
        "the thumb runs along the block's bottom edge: {wide:?} under {line:?}"
    );

    let at = ((line[0] + 40.0) as f32, (line[1] + 4.0) as f32);
    world.gesture(|app| {
        app.runtime
            .wheel_event(rune::WheelEvent::new(-200.0, 0.0).at(at.0, at.1))
    });
    world.settle();
    let [_, (scrolled, _)] = wide_thumbs(&world)[..] else {
        unreachable!()
    };
    assert!(
        scrolled[0] > wide[0],
        "scrolling the block sideways moves its thumb along: {wide:?} -> {scrolled:?}"
    );

    world.daemon.emit(content(&format!(
        "```\nshort\n```\n\n```\n{long}\n```\n\nEdited.\n"
    )));
    world.settle();
    let [_, (edited, _)] = wide_thumbs(&world)[..] else {
        unreachable!()
    };
    assert_eq!(
        edited[0], scrolled[0],
        "an edit to the file keeps the block where it was scrolled"
    );
    rest(&mut world);
}

#[test]
fn a_wide_table_wraps_its_cells_inside_the_document() {
    let mut world = start();
    let cell = |n: usize| {
        format!(
            "cell {n} holds a sentence long enough that three of them cannot share one line of the page"
        )
    };
    world.daemon.emit(content(&format!(
        "| One | Two | Three |\n| --- | --- | --- |\n| {} | {} | {} |\n",
        cell(1),
        cell(2),
        cell(3)
    )));
    world.settle();
    let screen = world.screen();
    let [x, _, w, _] = screen.bounds(Target::key("tile.m.document")).unwrap();
    for n in 1..=3 {
        let [cx, _, cw, ch] = screen
            .bounds(Target::role("label").name(cell(n)))
            .unwrap_or_else(|| panic!("cell {n} is shown"));
        assert!(
            cx + cw <= x + w,
            "cell {n} ends at {} past the document's {}",
            cx + cw,
            x + w
        );
        assert!(ch > 30.0, "cell {n} wraps: {ch} high");
    }
}

/// Three subgraphs side by side, too wide to read across: the kit turns it top-down.
const FLOW: &str = "flowchart LR\n    subgraph Plain[Plain session]\n        P0[Launch instructions] --> P1[SessionStart startup]\n        P1 --> P2[SessionStart resume clear compact]\n    end\n\n    subgraph Delegate[Delegate]\n        D0[Bind seed] --> D1[Launch instructions]\n        D1 --> D2[First prompt leaf identity + brief + reporting tail]\n        D1 --> D3[SessionStart live garden tail]\n        D2 -.-> DN[No guaranteed order between first prompt and SessionStart]\n        D3 -.-> DN\n    end\n\n    subgraph Crew[Crew day]\n        C0[Wake fresh] --> C1[Launch with crew priming]\n        C1 --> C2{First prompt}\n        C2 -->|ordinary wake| C3[Cold-wake greeting]\n        C2 -->|addressed while asleep| C4[Agent message replaces greeting]\n        C3 --> C5[Work or wait]\n        C4 --> C5\n        C5 --> C6[Heartbeat / auto-sleep / context handoff / requested sleep]\n        C6 --> C7[File predecessor letter]\n        C7 -->|sleep| C8[No successor]\n        C7 -->|nap or presence turnover| C9[Fresh successor]\n        C9 --> C1\n    end";

#[test]
fn a_wide_diagram_that_turns_to_fit_stays_turned_at_its_size() {
    let mut world = start();
    world
        .daemon
        .emit(content(&format!("```mermaid\n{FLOW}\n```\n")));
    world.settle();
    let screen = world.screen();
    assert!(
        screen
            .find(Target::role("button").name("Focus diagram"))
            .is_some(),
        "it is large"
    );
    let at = |name: &str| {
        screen
            .bounds(Target::role("label").name(name))
            .unwrap_or_else(|| panic!("{name} is shown"))
    };
    let centre = |[x, y, w, _]: [f32; 4]| (x + w / 2.0, y);
    let ((x0, y0), (x1, y1)) = (
        centre(at("Wake fresh")),
        centre(at("Launch with crew priming")),
    );
    assert!(
        y1 > y0 && (x1 - x0).abs() < 2.0,
        "the next node sits below, not beside: {x0},{y0} then {x1},{y1}"
    );
}

#[test]
fn a_details_block_opens_as_its_tag_says_and_a_press_on_its_summary_opens_or_closes_it_through_edits()
 {
    let mut world = start();
    let doc = |lead: &str| {
        content(&format!(
            "{lead}<details>\n<summary>More</summary>\n\nHidden body\n\n</details>\n\n\
             <details open>\n<summary>Open one</summary>\n\nShown body\n\n</details>\n"
        ))
    };
    world.daemon.emit(doc(""));
    world.settle();
    let says = |world: &World, words: &str| shown(world).iter().any(|t| t == words);
    assert!(!says(&world, "Hidden body") && says(&world, "Shown body"));

    let press = |world: &mut World, summary: &str| {
        let [x, y, _, h] = world
            .screen()
            .bounds(Target::role("button").name(summary))
            .unwrap();
        world.gesture(|app| app.click_at((x + 4.0, y + h / 2.0)));
    };
    press(&mut world, "More");
    press(&mut world, "Open one");
    assert!(says(&world, "Hidden body") && !says(&world, "Shown body"));

    world.daemon.emit(doc("A new opening line.\n\n"));
    world.settle();
    assert!(says(&world, "A new opening line."));
    assert!(
        says(&world, "Hidden body") && !says(&world, "Shown body"),
        "an edit above the blocks keeps what was opened and closed"
    );
    rest(&mut world);
}

#[test]
fn a_diagram_that_draws_without_a_statement_it_cannot_read_says_which() {
    let mut world = start();
    world.daemon.emit(content(
        "```mermaid\nflowchart LR\n  A --> B\n  this is %%% nonsense\n```\n\n\
         ```mermaid\nflowchart LR\n  C --> D\n```\n",
    ));
    world.settle();
    let shown = shown(&world);
    assert!(shown.iter().any(|t| t == "D"), "{shown:?}");
    let said: Vec<_> = shown
        .iter()
        .skip_while(|t| *t != "This diagram leaves out what it cannot read:")
        .take(2)
        .collect();
    assert_eq!(
        said,
        [
            "This diagram leaves out what it cannot read:",
            "this is %%% nonsense"
        ]
    );
    assert_eq!(
        shown
            .iter()
            .filter(|t| t.starts_with("This diagram leaves out"))
            .count(),
        1
    );
    rest(&mut world);
}

#[test]
fn a_diagram_too_wide_to_read_fitted_shows_at_its_size_and_opens_over_the_window() {
    let mut world = start();
    world.daemon.emit(content(
        "```mermaid\nflowchart LR\n  A --> B\n```\n\n```mermaid\nsequenceDiagram\n  participant B as Browser on a laptop\n  participant E as Edge cache in front\n  participant A as Auth service cluster\n  participant P as Payments API server\n  participant W as Billing worker pool\n  participant S as Search index shard\n  participant L as Long-running audit log\n  participant Q as Queue of retried jobs\n  participant M as Metrics collector node\n  participant N as Notification fan-out\n  participant R as Read replica in region two\n  participant X as Export worker for the nightly batch\n  participant Y as Archive store on cold storage\n  B->>E: a request arrives at the edge\n  E->>A: is this session token still valid?\n  A-->>E: yes\n  E->>P: forward the call\n  P->>W: enqueue the invoice\n  W->>S: index it\n  W->>L: record the change\n  L->>Q: retry later\n  Q->>M: count it\n  M->>N: tell someone\n  N->>R: read it back\n  R->>X: export it\n  X->>Y: archive it\n```\n",
    ));
    world.settle();
    let shown = shown(&world);
    let labels = shown.iter().filter(|t| *t == "diagram").count();
    assert_eq!(labels, 2, "each diagram has its bar: {shown:?}");
    let large =
        |world: &World, name: &str| world.screen().all(Target::role("label").name(name)).len();
    assert_eq!(
        large(&world, "Large diagram · 100%"),
        1,
        "only the wide one"
    );
    assert_eq!(
        world
            .screen()
            .all(Target::role("button").name("Focus diagram"))
            .len(),
        1
    );

    let width = |world: &World, target: Target| world.screen().bounds(target).expect("drawn")[2];
    let in_document = width(&world, Target::role("figure").name("Sequence diagram"));
    world.gesture(|app| app.click(Target::role("button").name("Focus diagram")));
    world.settle();
    let focused = width(&world, Target::key("diagram-preview.drawing"));
    assert!(
        (in_document - focused).abs() < 1.0,
        "it opens at 100%, as wide as its natural size in the document: {in_document} and {focused}"
    );
    assert!(
        world
            .screen()
            .find(Target::role("button").name("100%"))
            .is_some()
    );
    escape(&mut world);
    assert!(
        world
            .screen()
            .find(Target::key("diagram-preview.drawing"))
            .is_none()
    );
}

/// The thumbs of the oversized diagram's scroll bars, keyed by bar: `scroll-bar` down its end
/// edge, `scroll-bar-x` along its bottom.
fn diagram_thumbs(world: &World) -> Vec<(String, [f32; 4])> {
    world
        .runtime()
        .elements()
        .into_iter()
        .filter(|e| e.key.as_deref() == Some("thumb") && e.path.contains("diagram-"))
        .map(|e| {
            let bar = e
                .path
                .split('/')
                .find(|s| s.starts_with("scroll-bar"))
                .unwrap_or_default()
                .to_owned();
            (bar, e.rect)
        })
        .collect()
}

#[test]
fn a_diagram_too_large_both_ways_carries_a_scroll_bar_for_each() {
    let mut world = start();
    let participants: String = (0..12)
        .map(|i| format!("  participant P{i} as Participant number {i} with a long name\n"))
        .collect();
    let messages: String = (0..40)
        .map(|i| format!("  P{}->>P{}: message {i}\n", i % 12, (i + 5) % 12))
        .collect();
    world.daemon.emit(content(&format!(
        "```mermaid\nsequenceDiagram\n{participants}{messages}```\n"
    )));
    world.settle();
    let thumbs = diagram_thumbs(&world);
    let thumb = |bar: &str| {
        thumbs
            .iter()
            .find(|(b, _)| b == bar)
            .unwrap_or_else(|| panic!("a {bar} on the diagram: {thumbs:?}"))
            .1
    };
    let (down, across) = (thumb("scroll-bar"), thumb("scroll-bar-x"));
    assert!(
        down[3] > 0.0 && across[2] > 0.0,
        "a diagram taller and wider than its frame shows a thumb on each axis: {thumbs:?}"
    );
    assert!(
        down[1] + down[3] <= across[1],
        "the end bar stops above the bottom one: {down:?} over {across:?}"
    );
}

#[test]
fn a_code_blocks_copy_button_copies_its_code_and_says_so_a_while() {
    let mut world = start();
    world.daemon.emit(content(
        "Run it:\n\n```sh\nmake check\n```\n\n```rust\nfn main() {}\n```\n",
    ));
    world.settle();
    let buttons =
        |world: &World, name: &str| world.screen().all(Target::role("button").name(name)).len();
    assert_eq!(buttons(&world, "Copy code"), 2, "one on each code block");
    world.gesture(|app| app.click(Target::role("button").name("Copy code")));
    world.settle();
    assert_eq!(
        world.clipboard.text().map(|t| t.to_string()).as_deref(),
        Some("make check")
    );
    assert_eq!(
        (buttons(&world, "Copied!"), buttons(&world, "Copy code")),
        (1, 1)
    );
    world.advance(std::time::Duration::from_secs(3));
    world.settle();
    assert_eq!(
        (buttons(&world, "Copied!"), buttons(&world, "Copy code")),
        (0, 2)
    );
}

#[test]
fn a_diagram_that_cannot_draw_says_so_above_its_source() {
    let mut world = start();
    world.daemon.emit(content(
        "```mermaid\nflowchart LR\n  A --> B\n```\n\n```mermaid\nkanban\n  Todo\n```\n",
    ));
    world.settle();
    let shown = shown(&world);
    assert!(
        shown.iter().any(|t| t == "B"),
        "the flowchart draws: {shown:?}"
    );
    let said: Vec<_> = shown
        .iter()
        .skip_while(|t| *t != "Diagram failed to render")
        .take(2)
        .collect();
    assert!(
        said.len() == 2 && said[1].starts_with("kanban"),
        "the note sits right above the source it could not draw: {shown:?}"
    );
    assert_eq!(
        shown
            .iter()
            .filter(|t| *t == "Diagram failed to render")
            .count(),
        1
    );
}

#[test]
fn a_pressed_document_tile_keeps_the_keyboard_when_its_file_changes() {
    let mut world = start();
    world.daemon.emit(content("# Plan\n\nFirst step."));
    world.settle();
    world.gesture(|app| app.click(Target::key("tile.m.document")));
    let holder = |world: &mut World| {
        world.automate("state", json!({"of": "window"})).unwrap()["keyboard"]["pane"].clone()
    };
    assert_eq!(holder(&mut world), "m");

    world.daemon.emit(content("# Plan\n\nSecond step."));
    world.settle();
    assert_eq!(holder(&mut world), "m");
}

#[test]
fn a_document_popped_out_into_its_own_window_shows_its_local_images() {
    let mut popped = document_tile("m", PLAN);
    popped["window"] = json!({"x": 60.0, "y": 60.0, "width": 900.0, "height": 700.0});
    let mut world = start_with(row(vec![pane("a"), popped]));
    world.daemon.on("tile_asset_read", |r| {
        serde_json::to_value(TileAssetReadResultMessage {
            request_id: r["request_id"].as_str().map(str::to_owned),
            success: true,
            data_uri: Some(format!("data:image/png;base64,{RED}")),
            ..TileAssetReadResultMessage::new()
        })
        .unwrap()
    });
    world.daemon.emit(content("![chart](img/chart.png)"));
    world.settle();

    let windows = world.tile_windows();
    assert_eq!(windows.len(), 1, "{windows:?}");
    let screen = world.tile_screen(&windows[0].key);
    assert!(
        screen.find(Target::role("image").name("chart")).is_some(),
        "{screen}"
    );
    rest(&mut world);
}

#[test]
fn a_document_tile_that_returns_waits_for_its_content_again_instead_of_showing_what_it_held() {
    let mut world = start();
    world.daemon.emit(content("# Notes"));
    world.settle();
    assert!(
        shown(&world).iter().any(|t| t == "Notes"),
        "{:?}",
        shown(&world)
    );

    arrange(&mut world, pane("a"), 2);
    arrange(
        &mut world,
        row(vec![pane("a"), document_tile("m", PLAN)]),
        3,
    );
    let returned = shown(&world);
    assert!(returned.iter().any(|t| t == "Loading…"), "{returned:?}");
    assert!(!returned.iter().any(|t| t == "Notes"), "{returned:?}");

    world.daemon.emit(content("# Notes"));
    world.settle();
    assert!(
        shown(&world).iter().any(|t| t == "Notes"),
        "{:?}",
        shown(&world)
    );
    world.advance(std::time::Duration::from_secs(2));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_press_on_a_control_in_an_inactive_document_tile_takes_the_tile_and_the_control_still_acts() {
    let mut world = start();
    world
        .daemon
        .emit(content("Run it:\n\n```sh\nmake check\n```\n"));
    world.settle();

    world.gesture(|app| app.click(Target::role("button").name("Copy code")));

    assert_eq!(
        world.clipboard.text().map(|t| t.to_string()).as_deref(),
        Some("make check")
    );
    let shown = world.daemon.sent_of("desktop_show_leaf");
    assert_eq!(shown.last().map(|s| s["leaf_id"].clone()), Some(json!("m")));
    assert_eq!(
        world.automate("state", json!({"of": "window"})).unwrap()["keyboard"]["pane"],
        "m"
    );
}

const MEASURED: &str = "---\ntitle: Launch plan\n---\n# The plan\n\nA paragraph that runs long enough to wrap well before it could reach across a wide tile, so its width is the reading measure and not the words in it.\n\n> a quote that also runs long enough to wrap before it reaches across a wide tile, as prose does\n\n| One | Two |\n| --- | --- |\n| 1 | 2 |\n\n```sh\nmake check\n```\n";

/// The document's prose (heading, paragraph, quote) and its wide blocks (front matter, table,
/// code block), each as its left edge and width, and the document view's, in a window `width`
/// wide.
fn measured_at(width: f32) -> (Vec<[f32; 2]>, Vec<[f32; 2]>, [f32; 2]) {
    let mut world = start_with(document_tile("m", PLAN));
    world.resize((width, 900.0));
    world.daemon.emit(content(MEASURED));
    world.settle();
    let screen = world.screen();
    let span_of = |target: Target| {
        let described = format!("{target:?}");
        let [x, _, w, _] = screen
            .bounds(target)
            .unwrap_or_else(|| panic!("{described} is drawn"));
        [x, w]
    };
    let prose = vec![
        span_of(Target::role("heading").name("The plan")),
        span_of(Target::role("label").name(
            "A paragraph that runs long enough to wrap well before it could reach across a wide tile, so its width is the reading measure and not the words in it.",
        )),
        span_of(Target::role("blockquote")),
    ];
    let wide = vec![
        span_of(Target::role("note").name("Front matter")),
        span_of(Target::role("table")),
        span_of(Target::role("code")),
    ];
    let document = span_of(Target::key("tile.m.document"));
    (prose, wide, document)
}

fn centered_on(spans: &[[f32; 2]], axis: f32) {
    for [x, w] in spans {
        assert!(
            (x + w / 2.0 - axis).abs() < 1.0,
            "every block is centered on {axis}: {spans:?}"
        );
    }
}

#[test]
fn a_wide_document_tile_centers_prose_at_its_reading_measure_and_wide_blocks_across_the_card() {
    let (prose, wide, [x, document]) = measured_at(2100.0);
    assert!(document > 1700.0, "the tile is wide: {document}");
    let text = wide[0][1];
    assert!(
        document - text < 120.0,
        "the card fills the tile: its text is {text} of {document}"
    );
    for [_, w] in &wide {
        assert!(
            (w - text).abs() < 1.0,
            "a wide block takes the card: {wide:?}"
        );
    }
    assert_eq!(
        prose.iter().map(|[_, w]| *w).collect::<Vec<_>>(),
        [760.0; 3],
        "prose keeps the reading measure"
    );
    let axis = x + document / 2.0;
    centered_on(&prose, axis);
    centered_on(&wide, axis);

    let (prose, wide, [x, document]) = measured_at(900.0);
    assert!(document < 760.0, "the tile is narrow: {document}");
    let text = wide[0][1];
    for [_, w] in prose.iter().chain(&wide) {
        assert!(
            (w - text).abs() < 1.0,
            "every block shares the card: {prose:?} {wide:?}"
        );
    }
    centered_on(&prose, x + document / 2.0);
}

#[test]
fn a_screen_reader_is_on_the_terminal_or_document_that_holds_the_keyboard() {
    let mut world = start();
    world.daemon.emit(content("# Plan\n\nFirst step."));
    world.settle();
    let focused = |world: &World| {
        let node = world.screen().focused()?;
        Some((node.key?, node.name?))
    };

    world.gesture(|app| app.click(Target::key("terminal.r-a")));
    assert_eq!(
        focused(&world),
        Some(("terminal.r-a".to_owned(), "Agent a · unknown".to_owned()))
    );
    world.gesture(|app| app.click(Target::key("tile.m.document")));
    assert_eq!(
        focused(&world),
        Some(("tile.m.document".to_owned(), "Plan".to_owned()))
    );
}
