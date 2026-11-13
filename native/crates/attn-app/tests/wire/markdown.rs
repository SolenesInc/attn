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
    assert_eq!(pictures, ["chart", "gone", "script", "remote"]);

    world.gesture(|app| app.click(Target::role("image").name("gone")));
    assert!(
        world
            .screen()
            .find(Target::key("image-preview.picture"))
            .is_none()
    );

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

/// An 8×2 green PNG.
const GREEN: &str = "iVBORw0KGgoAAAANSUhEUgAAAAgAAAACCAIAAADq9gq6AAAAEElEQVR4nGNg+M+AHeGSAACOsQ/xb1SVcwAAAABJRU5ErkJggg==";

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
