use attn_protocol::{
    Desktop, DesktopPane, DesktopTileContentMessage, InitialStateMessage, Profile, ReviewBundle,
    ReviewFile, ReviewNotShown, ReviewNote, ReviewRow, ReviewStop, Session,
};
use attn_testkit::tree::{pane, row};
use attn_testkit::{Daemon, Node, Target, World};
use rune::{Key, KeyEvent, Mods};
use serde_json::json;

const DESKTOP: &str = "work/desktop_1";
const DOC: &str = "/notes/review.md";

const DOCUMENT: &str = "---
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

A paragraph between the stops.

### util.go:1-2

- L2: The helper.

## Not shown

- go.sum: checksums only.
";

fn line(kind: &str, old: Option<i64>, now: Option<i64>, text: &str) -> ReviewRow {
    ReviewRow {
        kind: kind.into(),
        old,
        now,
        text: text.into(),
        ..ReviewRow::default()
    }
}

#[allow(clippy::too_many_arguments)]
fn note(line: i64, at: i64, look: bool, text: &str) -> ReviewNote {
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

fn file(path: &str, added: i64, removed: i64) -> ReviewFile {
    ReviewFile {
        path: path.into(),
        status: "modified".into(),
        added,
        removed,
        ..ReviewFile::default()
    }
}

fn bundle() -> ReviewBundle {
    ReviewBundle {
        frame: "brief".into(),
        repository: "/repo".into(),
        base: Some("4be1c0d".into()),
        head: "9f3a7e2".into(),
        stops: vec![
            ReviewStop {
                heading_line: 15,
                path: "calc.go".into(),
                from: 3,
                to: 6,
                now_from: 3,
                now_to: 6,
                file_hash: "h1".into(),
                rows: vec![
                    line(" ", Some(3), Some(3), "func Add(a, b int) int {"),
                    line("-", Some(4), None, "\treturn a + b"),
                    line("+", None, Some(4), "\tsum := a + b"),
                    line("+", None, Some(5), "\treturn sum"),
                    line(" ", Some(5), Some(6), "}"),
                ],
                notes: vec![
                    note(17, 4, true, "The sum gets a name."),
                    note(18, 5, false, "Then it is returned."),
                ],
                ..ReviewStop::default()
            },
            ReviewStop {
                heading_line: 22,
                path: "util.go".into(),
                from: 1,
                to: 2,
                now_from: 1,
                now_to: 2,
                file_hash: "h2".into(),
                rows: vec![
                    line(" ", Some(1), Some(1), "package calc"),
                    line("+", None, Some(2), "func helper() {}"),
                ],
                notes: vec![note(24, 2, false, "The helper.")],
                ..ReviewStop::default()
            },
        ],
        files: vec![
            file("calc.go", 3, 2),
            file("util.go", 1, 0),
            file("go.sum", 1, 1),
        ],
        not_shown: vec![ReviewNotShown {
            line: 28,
            path: "go.sum".into(),
            reason: "checksums only.".into(),
        }],
        ..ReviewBundle::default()
    }
}

fn start() -> World {
    let tree = row(vec![
        pane("a"),
        json!({
            "type": "tile",
            "tile_id": "m",
            "tile_kind": "markdown",
            "tile_params": json!({ "path": DOC }).to_string(),
        }),
    ]);
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
            directory: "/repo".into(),
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

fn deliver(world: &mut World, content: &str, review: Option<ReviewBundle>) {
    world.daemon.emit(DesktopTileContentMessage {
        desktop_id: DESKTOP.into(),
        tile_id: "m".into(),
        tile_kind: "markdown".into(),
        path: DOC.into(),
        content: content.into(),
        review,
        ..DesktopTileContentMessage::new()
    });
    world.settle();
}

/// The review of the fixture, open in its tile, with the tile holding the keyboard.
fn open() -> World {
    let mut world = start();
    deliver(&mut world, DOCUMENT, Some(bundle()));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    world
}

fn press(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

/// What the tile shows, as assistive tech reads it, in reading order.
fn shown(world: &World) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key("tile.m"))
        .into_iter()
        .filter_map(|n| n.name.or(n.value))
        .collect()
}

/// The place the cursor stands on: the document's current element.
fn cursor(world: &World) -> Node {
    let screen = world.screen();
    let key = screen
        .get(Target::key("tile.m.document"))
        .active_descendant
        .expect("the document has a cursor");
    screen.get(Target::key(key))
}

/// The rows and notes of the stop keyed `stop`, in order.
fn stop_lines(world: &World, stop: &str) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key(stop))
        .into_iter()
        .filter(|n| n.role == "row" || n.role == "note")
        .filter_map(|n| n.name)
        .collect()
}

fn stops(world: &World) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key("tile.m.document"))
        .into_iter()
        .filter(|n| n.role == "group")
        .filter_map(|n| n.name)
        .collect()
}

fn stop_key(world: &World, path: &str) -> String {
    world
        .screen()
        .descendants(Target::key("tile.m.document"))
        .into_iter()
        .find(|n| n.role == "group" && n.name.as_deref().is_some_and(|l| l.starts_with(path)))
        .and_then(|n| n.key)
        .unwrap()
}

fn cursor_in_stop(world: &World, path: &str) -> bool {
    let key = stop_key(world, path);
    let cursor = cursor(world).key.unwrap();
    cursor.starts_with(&format!("{key}.row."))
}

#[test]
fn renders_each_stop_in_place_of_its_heading_and_notes_with_the_change_line_numbers_and_agent_notes_under_their_lines()
 {
    let world = open();

    assert_eq!(stops(&world), ["calc.go L3–6", "util.go L1–2"]);
    assert_eq!(
        stop_lines(&world, &stop_key(&world, "calc.go")),
        [
            "unchanged line 3: func Add(a, b int) int {",
            "removed line 4: \treturn a + b",
            "added line 4: \tsum := a + b",
            "look here L4: The sum gets a name.",
            "added line 5: \treturn sum",
            "agent L5: Then it is returned.",
            "unchanged line 6: }",
        ]
    );
    let text = shown(&world);
    assert!(!text.iter().any(|t| t == "calc.go:3-6"), "{text:?}");
    assert!(!text.iter().any(|t| t.contains("L4!")), "{text:?}");
    assert!(
        text.iter().any(|t| t == "A paragraph between the stops."),
        "{text:?}"
    );
    assert!(text.iter().any(|t| t == "Add keeps its result"), "{text:?}");
    assert!(
        text.iter().any(|t| t == "go.sum checksums only."),
        "{text:?}"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn opens_with_the_title_and_the_changes_facts_under_it_with_the_changed_files_one_enter_away() {
    let mut world = open();
    let document: Vec<Node> = world.screen().descendants(Target::key("tile.m.document"));
    let title = document.iter().position(|n| n.role == "heading").unwrap();
    assert_eq!(document[title].name.as_deref(), Some("Name the sum"));
    let facts = "base 4be1c0d → head 9f3a7e2 · 3 files · +5 −3 · 2 stops show 4 of 8 changed lines · ↵ show the files";
    let next = document[title + 1..]
        .iter()
        .find(|n| n.name.is_some())
        .unwrap();
    assert_eq!(next.name.as_deref(), Some("The change"));
    assert!(document.iter().any(|n| n.name.as_deref() == Some(facts)));
    let files = |world: &World| {
        world
            .screen()
            .descendants(Target::role("region").name("The change"))
            .into_iter()
            .filter(|n| n.role == "row")
            .filter_map(|n| n.name)
            .collect::<Vec<_>>()
    };
    assert!(files(&world).is_empty());

    press(&mut world, Key::Down);
    assert_eq!(cursor(&world).key.as_deref(), Some("tile.m.review.part.0"));
    press(&mut world, Key::Down);
    assert_eq!(cursor(&world).key.as_deref(), Some("tile.m.review.facts"));
    press(&mut world, Key::Enter);
    assert_eq!(
        files(&world),
        [
            "modified calc.go +3 −2",
            "modified util.go +1 −0",
            "modified go.sum +1 −1"
        ]
    );
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("modified util.go +1 −0")
    );

    press(&mut world, Key::Up);
    press(&mut world, Key::Up);
    press(&mut world, Key::Enter);
    assert!(files(&world).is_empty());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn moves_the_cursor_by_block_and_line_with_up_and_down_and_by_stop_with_left_and_right() {
    let mut world = open();

    press(&mut world, Key::Right);
    assert!(cursor_in_stop(&world, "calc.go"));
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("unchanged line 3: func Add(a, b int) int {")
    );
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("added line 4: \tsum := a + b")
    );

    press(&mut world, Key::Right);
    assert!(cursor_in_stop(&world, "util.go"));
    press(&mut world, Key::Up);
    let paragraph = cursor(&world).key.unwrap();
    let inside: Vec<String> = world
        .screen()
        .descendants(Target::key(paragraph.as_str()))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(
        inside.iter().any(|t| t == "A paragraph between the stops."),
        "{inside:?}"
    );
    press(&mut world, Key::Left);
    assert!(cursor_in_stop(&world, "calc.go"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn puts_a_steps_heading_at_the_top_when_right_reaches_its_first_stop() {
    let mut world = start();
    let filler: String = (1..=60)
        .map(|i| format!("\nFiller paragraph {i}.\n"))
        .collect();
    deliver(&mut world, &format!("{DOCUMENT}{filler}"), Some(bundle()));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    press(&mut world, Key::Right);
    let screen = world.screen();
    let view = screen.bounds(Target::key("tile.m.document")).unwrap();
    let heading = screen
        .descendants(Target::key("tile.m.document"))
        .into_iter()
        .find(|n| n.name.as_deref() == Some("Add keeps its result"))
        .and_then(|n| n.key)
        .unwrap();
    let top = screen.bounds(Target::key(heading.as_str())).unwrap();
    assert!(
        (top[1] - view[1]).abs() < 40.0,
        "the heading is at the top: view {view:?}, heading {top:?}"
    );
}

#[test]
fn keeps_the_cursor_on_its_row_when_the_document_changes_above_it() {
    let mut world = open();
    press(&mut world, Key::Right);
    press(&mut world, Key::Right);
    press(&mut world, Key::Down);
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("added line 2: func helper() {}")
    );

    let mut edited = bundle();
    for stop in &mut edited.stops {
        stop.heading_line += 2;
        for note in &mut stop.notes {
            note.line += 2;
        }
    }
    for entry in &mut edited.not_shown {
        entry.line += 2;
    }
    let content = DOCUMENT.replace(
        "Add used to return its sum directly.\n",
        "Add used to return its sum directly.\n\nAn agent added this paragraph.\n",
    );
    deliver(&mut world, &content, Some(edited));
    assert!(
        shown(&world)
            .iter()
            .any(|t| t == "An agent added this paragraph.")
    );
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("added line 2: func helper() {}")
    );

    press(&mut world, Key::Up);
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("unchanged line 1: package calc")
    );
}

#[test]
fn leaves_the_keys_to_the_rest_of_the_app_while_another_tile_holds_the_keyboard() {
    let mut world = open();
    press(&mut world, Key::Down);
    let before = cursor(&world).key;
    world.gesture(|app| app.click(Target::key("tile.a")));
    press(&mut world, Key::Down);
    assert_eq!(cursor(&world).key, before);
}

#[test]
fn shows_the_keys_in_a_key_line_and_a_sheet() {
    let mut world = open();
    let keys: Vec<String> = world
        .screen()
        .descendants(Target::key("tile.m.review.keys"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(keys.iter().any(|k| k == "↑ ↓ line"), "{keys:?}");
    assert!(keys.iter().any(|k| k == "? all keys"), "{keys:?}");

    world.gesture(|app| app.type_text("?"));
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Review keys"))
            .is_some()
    );
    press(&mut world, Key::Escape);
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Review keys"))
            .is_none()
    );
}

#[test]
fn renders_a_review_document_as_plain_markdown_with_no_keys_when_the_tile_carries_no_review() {
    let mut world = start();
    deliver(&mut world, DOCUMENT, None);
    world.gesture(|app| app.click(Target::key("tile.m.document")));
    assert!(
        world
            .screen()
            .find(Target::key("tile.m.review.keys"))
            .is_none()
    );
    let text = shown(&world);
    assert!(text.iter().any(|t| t.contains("calc.go:3")), "{text:?}");
    press(&mut world, Key::Down);
    let document = world.screen().get(Target::key("tile.m.document"));
    assert_eq!(document.active_descendant, None);
}
