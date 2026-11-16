use attn_protocol::{
    Desktop, DesktopPane, DesktopTileContentMessage, InitialStateMessage, Profile, ReviewBundle,
    ReviewFile, ReviewFileResultMessage, ReviewFlow, ReviewFlowGroup, ReviewFlowMessage,
    ReviewFlowParticipant, ReviewNotShown, ReviewNote, ReviewRow, ReviewSearchHit,
    ReviewSearchResultMessage, ReviewStop, Session,
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
    start_with_session(None)
}

/// The fixture's desktop, with the document tile bound to `session` when given.
fn start_with_session(session: Option<&str>) -> World {
    let mut document = json!({
        "type": "tile",
        "tile_id": "m",
        "tile_kind": "markdown",
        "tile_params": json!({ "path": DOC }).to_string(),
    });
    if let Some(session) = session {
        document["tile_session_id"] = json!(session);
    }
    let tree = row(vec![pane("a"), document]);
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
    assert!(keys.iter().any(|k| k == "← → stop"), "{keys:?}");
    assert!(keys.iter().any(|k| k == "c note"), "{keys:?}");

    world.gesture(|app| app.type_text("?"));
    let sheet: Vec<String> = world
        .screen()
        .descendants(Target::role("dialog").name("Review keys"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(
        sheet
            .iter()
            .any(|k| k.contains("Fill the tile with the file")),
        "{sheet:?}"
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

#[test]
fn walks_on_through_two_stops_that_show_the_same_line() {
    let stop = |heading_line: i64, from: i64, rows: Vec<ReviewRow>| ReviewStop {
        heading_line,
        path: "x.go".into(),
        from,
        to: from + 1,
        now_from: from,
        now_to: from + 1,
        file_hash: "h".into(),
        rows,
        ..ReviewStop::default()
    };
    let review = ReviewBundle {
        frame: "brief".into(),
        repository: "/repo".into(),
        head: "9f3a7e2".into(),
        stops: vec![
            stop(
                7,
                1,
                vec![
                    line(" ", Some(1), Some(1), "a"),
                    line(" ", Some(2), Some(2), "b"),
                ],
            ),
            stop(
                9,
                2,
                vec![
                    line(" ", Some(2), Some(2), "b"),
                    line(" ", Some(3), Some(3), "c"),
                ],
            ),
        ],
        ..ReviewBundle::default()
    };
    let mut world = start();
    deliver(
        &mut world,
        "---\nattn: review\n---\n\n# Overlap\n\n### x.go:1-2\n\n### x.go:2-3\n",
        Some(review),
    );
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    for _ in 0..6 {
        press(&mut world, Key::Down);
    }
    assert_eq!(cursor(&world).name.as_deref(), Some("unchanged line 3: c"));
    press(&mut world, Key::Up);
    assert!(cursor_in_stop(&world, "x.go L2–3"));
    assert_eq!(cursor(&world).name.as_deref(), Some("unchanged line 2: b"));
}

const MAP_DIAGRAMS: &str = "
```mermaid before
sequenceDiagram
  participant poll as Poll
  participant calc as Calc
  poll->>calc: Add(a, b)
  %% stop: calc.go:3-6
```

```mermaid after
sequenceDiagram
  participant poll as Poll
  participant calc as Calc
  participant util as Util
  poll->>calc: Add(a, b)
  %% stop: calc.go:4-5
  calc->>util: helper()
  %% stop: util.go:1-2
  util-->>calc: returns
```

```mermaid
sequenceDiagram
  activate calc
  poll->>calc: Add(a, b)
  deactivate calc
```
";

fn map_document() -> String {
    format!("{DOCUMENT}{MAP_DIAGRAMS}")
}

fn participant(id: &str, label: &str) -> ReviewFlowParticipant {
    ReviewFlowParticipant {
        id: id.into(),
        label: label.into(),
        actor: false,
    }
}

fn message(
    line: i64,
    from: &str,
    to: &str,
    text: &str,
    stop_line: Option<i64>,
) -> ReviewFlowMessage {
    ReviewFlowMessage {
        line,
        from: from.into(),
        to: to.into(),
        arrow: "->>".into(),
        text: text.into(),
        stop_line,
        ..ReviewFlowMessage::default()
    }
}

fn map_bundle(frame: &str) -> ReviewBundle {
    let flow = |line: i64, side: Option<&str>, drawn: bool, participants, messages| ReviewFlow {
        line,
        side: side.map(Into::into),
        drawn,
        participants,
        messages,
        ..ReviewFlow::default()
    };
    ReviewBundle {
        frame: frame.into(),
        flows: vec![
            flow(
                30,
                Some("before"),
                true,
                vec![participant("poll", "Poll"), participant("calc", "Calc")],
                vec![message(34, "poll", "calc", "Add(a, b)", Some(15))],
            ),
            flow(
                38,
                Some("after"),
                true,
                vec![
                    participant("poll", "Poll"),
                    participant("calc", "Calc"),
                    participant("util", "Util"),
                ],
                vec![
                    message(43, "poll", "calc", "Add(a, b)", Some(15)),
                    message(45, "calc", "util", "helper()", Some(22)),
                    message(47, "util", "calc", "returns", None),
                ],
            ),
            flow(
                50,
                None,
                false,
                vec![participant("poll", "poll"), participant("calc", "calc")],
                vec![message(53, "poll", "calc", "Add(a, b)", None)],
            ),
        ],
        ..bundle()
    }
}

fn open_map(review: ReviewBundle) -> World {
    let mut world = start();
    deliver(&mut world, &map_document(), Some(review));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    world
}

/// The frames the key line offers, the current one marked.
fn frame_tabs(world: &World) -> Vec<String> {
    world
        .screen()
        .descendants(Target::role("tab-list").name("Frames"))
        .into_iter()
        .filter(|n| n.role == "tab")
        .map(|n| {
            let name = n.name.clone().unwrap_or_default();
            if n.is("checked") {
                format!("{name} *")
            } else {
                name
            }
        })
        .collect()
}

/// The diagrams the Map shows, by the arrows each draws.
fn map_diagrams(world: &World) -> Vec<Vec<String>> {
    let screen = world.screen();
    screen
        .descendants(Target::key("tile.m.review.map"))
        .into_iter()
        .filter(|n| n.role == "figure")
        .map(|figure| {
            screen
                .descendants(Target::key(figure.key.unwrap()))
                .into_iter()
                .filter(|n| n.role == "row")
                .filter_map(|n| n.name)
                .collect()
        })
        .collect()
}

/// The arrow the Map stands on.
fn map_current(world: &World) -> Option<String> {
    world
        .screen()
        .descendants(Target::key("tile.m.review.map"))
        .into_iter()
        .find(|n| n.role == "row" && n.is("selected"))
        .and_then(|n| n.name)
}

fn arrow_line(world: &World) -> Vec<String> {
    match world
        .screen()
        .find(Target::role("region").name("The arrow"))
    {
        Some(region) => world
            .screen()
            .descendants(Target::key(region.key.unwrap()))
            .into_iter()
            .filter_map(|n| n.name)
            .collect(),
        None => Vec::new(),
    }
}

fn type_key(world: &mut World, typed: &str) {
    world.gesture(|app| app.type_text(typed));
}

#[test]
fn opens_in_the_map_the_document_picks_and_walks_the_messages_tied_to_stops_with_the_step_of_each_beside_the_diagram()
 {
    let mut world = open_map(map_bundle("map"));

    assert_eq!(frame_tabs(&world), ["1 Brief", "2 Walkthrough", "3 Map *"]);
    assert_eq!(map_diagrams(&world), [["Add(a, b)", "helper()", "returns"]]);
    assert_eq!(map_current(&world).as_deref(), Some("Add(a, b)"));
    assert_eq!(
        arrow_line(&world),
        [
            "Arrow 1 of 2 · after the change",
            "Add(a, b)",
            "Poll → Calc"
        ]
    );
    assert!(cursor_in_stop(&world, "calc.go"));
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("unchanged line 3: func Add(a, b int) int {")
    );
    let step: Vec<String> = world
        .screen()
        .descendants(Target::key("tile.m.document"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(step.iter().any(|t| t == "Add keeps its result"), "{step:?}");
    assert!(!step.iter().any(|t| t == "Name the sum"), "{step:?}");

    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("added line 4: \tsum := a + b")
    );

    press(&mut world, Key::Right);
    assert_eq!(map_current(&world).as_deref(), Some("helper()"));
    assert_eq!(
        arrow_line(&world),
        ["Arrow 2 of 2 · after the change", "helper()", "Calc → Util"]
    );
    assert!(cursor_in_stop(&world, "util.go"));
    press(&mut world, Key::Right);
    assert_eq!(map_current(&world).as_deref(), Some("helper()"));

    type_key(&mut world, "b");
    assert_eq!(map_diagrams(&world), [["Add(a, b)"]]);
    assert_eq!(map_current(&world).as_deref(), Some("Add(a, b)"));
    assert_eq!(arrow_line(&world)[0], "Arrow 1 of 1 · before the change");
    type_key(&mut world, "b");
    assert_eq!(map_diagrams(&world), [["Add(a, b)", "helper()", "returns"]]);
    assert_eq!(map_current(&world).as_deref(), Some("Add(a, b)"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn keeps_the_map_on_its_message_when_the_agent_adds_or_removes_a_message_before_it() {
    let mut world = open_map(map_bundle("map"));
    press(&mut world, Key::Right);
    assert_eq!(arrow_line(&world)[0], "Arrow 2 of 2 · after the change");

    let mut added = map_bundle("map");
    let after = &mut added.flows[1];
    for m in &mut after.messages {
        m.line += 2;
    }
    after
        .messages
        .insert(0, message(41, "poll", "calc", "Sum(a, b)", Some(15)));
    deliver(&mut world, &map_document(), Some(added));
    assert_eq!(arrow_line(&world)[0], "Arrow 3 of 3 · after the change");
    assert_eq!(arrow_line(&world)[1], "helper()");
    assert!(cursor_in_stop(&world, "util.go"));

    let mut removed = map_bundle("map");
    removed.flows[1].messages.remove(0);
    deliver(&mut world, &map_document(), Some(removed));
    assert_eq!(arrow_line(&world)[0], "Arrow 1 of 1 · after the change");
    assert_eq!(arrow_line(&world)[1], "helper()");
}

#[test]
fn switches_with_b_to_a_before_diagram_that_ties_no_message_to_a_stop_and_back() {
    let mut review = map_bundle("map");
    review.flows[0].messages[0].stop_line = None;
    let mut world = open_map(review);
    assert_eq!(map_current(&world).as_deref(), Some("Add(a, b)"));

    type_key(&mut world, "b");
    assert_eq!(map_diagrams(&world), [["Add(a, b)"]]);
    let none = world
        .screen()
        .descendants(Target::key("tile.m.review"))
        .into_iter()
        .any(|n| n.name.as_deref() == Some("No arrow in this diagram is tied to a stop."));
    assert!(none);
    press(&mut world, Key::Right);
    assert_eq!(map_diagrams(&world), [["Add(a, b)"]]);
    type_key(&mut world, "b");
    assert_eq!(map_current(&world).as_deref(), Some("Add(a, b)"));
    assert_eq!(map_diagrams(&world).len(), 1);
    assert_eq!(map_diagrams(&world)[0].len(), 3);
}

#[test]
fn switches_frames_with_1_and_3_and_keeps_the_stop_and_line_the_cursor_is_on() {
    let mut world = open_map(map_bundle("brief"));
    assert_eq!(frame_tabs(&world), ["1 Brief *", "2 Walkthrough", "3 Map"]);
    press(&mut world, Key::Right);
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("added line 5: \treturn sum")
    );

    type_key(&mut world, "3");
    assert_eq!(map_current(&world).as_deref(), Some("Add(a, b)"));
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("added line 5: \treturn sum")
    );

    press(&mut world, Key::Right);
    type_key(&mut world, "1");
    assert!(
        world
            .screen()
            .find(Target::key("tile.m.review.map"))
            .is_none()
    );
    assert!(cursor_in_stop(&world, "util.go"));
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("unchanged line 1: package calc")
    );
}

/// The arrow the Brief's cursor stands on, by what it says.
fn cursor_arrow(world: &World) -> Option<String> {
    world
        .screen()
        .get(Target::key("tile.m.document"))
        .active_descendant?;
    let place = cursor(world);
    place
        .key
        .as_deref()
        .is_some_and(|k| k.contains(".arrow."))
        .then(|| place.name.unwrap_or_default())
}

#[test]
fn walks_the_briefs_drawn_diagram_by_its_arrows_and_shares_the_side_with_the_map() {
    let mut world = open_map(map_bundle("brief"));
    let figures = |world: &World| {
        world
            .screen()
            .descendants(Target::key("tile.m.document"))
            .into_iter()
            .filter(|n| {
                n.role == "figure"
                    && n.key
                        .as_deref()
                        .is_some_and(|k| k.starts_with("tile.m.review.part."))
            })
            .count()
    };
    assert_eq!(figures(&world), 1);

    for _ in 0..30 {
        if cursor_arrow(&world).is_some() {
            break;
        }
        press(&mut world, Key::Down);
    }
    assert_eq!(cursor_arrow(&world).as_deref(), Some("Add(a, b)"));
    press(&mut world, Key::Up);
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("go.sum checksums only.")
    );
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    assert_eq!(cursor_arrow(&world).as_deref(), Some("helper()"));
    press(&mut world, Key::Down);
    assert_eq!(cursor_arrow(&world), None);
    press(&mut world, Key::Up);
    press(&mut world, Key::Up);
    assert_eq!(cursor_arrow(&world).as_deref(), Some("Add(a, b)"));

    type_key(&mut world, "b");
    assert_eq!(cursor_arrow(&world).as_deref(), Some("Add(a, b)"));
    let key = cursor(&world).key.unwrap();
    assert!(key.ends_with(".arrow.34"), "{key}");
    type_key(&mut world, "3");
    assert_eq!(map_diagrams(&world), [["Add(a, b)"]]);
    assert_eq!(arrow_line(&world)[0], "Arrow 1 of 1 · before the change");
    type_key(&mut world, "b");
    type_key(&mut world, "1");
    assert!(cursor(&world).key.is_some());
    assert_eq!(figures(&world), 1);
}

#[test]
fn keeps_the_cursor_by_the_diagram_when_b_shows_a_side_with_no_arrow_to_stand_on() {
    let mut review = map_bundle("brief");
    review.flows[0].messages[0].stop_line = None;
    let first = review.flows[1].messages[0].clone();
    review.flows[1].messages = vec![
        first.clone(),
        ReviewFlowMessage { line: 45, ..first },
        review.flows[1].messages[2].clone(),
    ];
    let mut world = open_map(review);
    for _ in 0..30 {
        if cursor_arrow(&world).is_some() {
            break;
        }
        press(&mut world, Key::Down);
    }
    let first = cursor(&world).key.unwrap();
    assert!(first.ends_with(".arrow.43"), "{first}");
    press(&mut world, Key::Down);
    let second = cursor(&world).key.unwrap();
    assert!(second.ends_with(".arrow.45"), "{second}");

    type_key(&mut world, "b");
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("go.sum checksums only.")
    );
}

#[test]
fn leaves_out_a_frame_the_document_cannot_fill() {
    let mut review = map_bundle("map");
    review.flows.drain(..2);
    let mut world = open_map(review);
    assert_eq!(frame_tabs(&world), ["1 Brief *", "2 Walkthrough"]);
    type_key(&mut world, "3");
    assert!(
        world
            .screen()
            .find(Target::key("tile.m.review.map"))
            .is_none()
    );
}

#[test]
fn keeps_the_key_line_inside_the_tile() {
    let world = open();
    let screen = world.screen();
    let tile = screen.bounds(Target::key("tile.m")).unwrap();
    let keys = screen.bounds(Target::key("tile.m.review.keys")).unwrap();
    assert!(
        keys[1] + keys[3] <= tile[1] + tile[3] + 0.5,
        "the key line {keys:?} ends inside the tile {tile:?}"
    );
}

const STORY: &str = "---
attn: review
frame: map
repository: /repo
base: 4be1c0d
head: 9f3a7e2
---

# Name the sum

Add used to return its sum directly.

## Add keeps its result

Add names the sum before it returns it.

### calc.go:3-6

- L4!: The sum gets a name.

## The helper joins

The helper is new, and only Add calls it.

### util.go:1-2

- L2: The helper.

## The map

```mermaid after
sequenceDiagram
  participant poll as Poll<br/>poll.go
  participant calc as Calc<br/>calc.go
  participant util as Util
  Note over poll,util: Add now calls the helper.
  poll->>calc: Add(a, b)
  %% stop: calc.go:3-6
  Note over poll,calc: names the sum
  critical inside Add
    calc->>util: helper()
    %% stop: util.go:1-2
    Note right of util: new
  end
  util--xcalc: no answer
```

## Not shown

- go.sum: checksums only.
";

fn line_of(text: &str) -> i64 {
    STORY.lines().position(|l| l.contains(text)).unwrap() as i64 + 1
}

/// The bundle the daemon reads from STORY.
fn story() -> ReviewBundle {
    let mut review = bundle();
    review.frame = "map".into();
    review.stops[0].heading_line = line_of("### calc.go");
    review.stops[0].notes = vec![note(line_of("- L4!"), 4, true, "The sum gets a name.")];
    review.stops[1].heading_line = line_of("### util.go");
    review.stops[1].notes = vec![note(line_of("- L2:"), 2, false, "The helper.")];
    review.not_shown[0].line = line_of("- go.sum");
    let with_note = |m: ReviewFlowMessage, note: &str| ReviewFlowMessage {
        note: Some(note.into()),
        ..m
    };
    review.flows = vec![ReviewFlow {
        line: line_of("```mermaid after"),
        side: Some("after".into()),
        drawn: true,
        participants: vec![
            participant("poll", "Poll<br/>poll.go"),
            participant("calc", "Calc<br/>calc.go"),
            participant("util", "Util"),
        ],
        note: Some("Add now calls the helper.".into()),
        messages: vec![
            with_note(
                message(
                    line_of("poll->>calc"),
                    "poll",
                    "calc",
                    "Add(a, b)",
                    Some(line_of("### calc.go")),
                ),
                "names the sum",
            ),
            with_note(
                message(
                    line_of("calc->>util"),
                    "calc",
                    "util",
                    "helper()",
                    Some(line_of("### util.go")),
                ),
                "new",
            ),
            ReviewFlowMessage {
                arrow: "--x".into(),
                ..message(line_of("util--xcalc"), "util", "calc", "no answer", None)
            },
        ],
        groups: vec![ReviewFlowGroup {
            line: line_of("critical inside Add"),
            kind: "critical".into(),
            label: "inside Add".into(),
            first_message: line_of("calc->>util"),
            last_message: line_of("calc->>util"),
            ..ReviewFlowGroup::default()
        }],
    }];
    review
}

/// The blocks the Map's document shows: each prose block's text, and `stop <path>` for a stop.
fn step_blocks(world: &World) -> Vec<String> {
    let screen = world.screen();
    screen
        .children(Target::key("tile.m.review.card"))
        .into_iter()
        .map(|part| match (part.role.as_str(), &part.name) {
            ("group", Some(name)) if name.contains(" L") => {
                format!("stop {}", name.split(' ').next().unwrap())
            }
            _ => match &part.key {
                Some(key) => screen
                    .descendants(Target::key(key.as_str()))
                    .into_iter()
                    .find_map(|n| n.name)
                    .unwrap_or_default(),
                None => part.name.clone().unwrap_or_default(),
            },
        })
        .collect()
}

#[test]
fn tells_the_story_beside_the_diagram_captions_groups_and_the_step_that_holds_the_arrows_stop() {
    let mut world = start();
    deliver(&mut world, STORY, Some(story()));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));

    let map: Vec<String> = world
        .screen()
        .descendants(Target::key("tile.m.review.map"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    for expected in [
        "Poll poll.go",
        "Calc calc.go",
        "Util",
        "Add(a, b): names the sum",
        "names the sum",
        "helper(): new",
        "critical  inside Add",
        "no answer",
        "Add now calls the helper.",
        "go.sum checksums only.",
    ] {
        assert!(map.iter().any(|t| t == expected), "{expected} in {map:?}");
    }
    assert_eq!(
        arrow_line(&world),
        ["Arrow 1 of 2", "Add(a, b)", "Poll → Calc"]
    );
    assert_eq!(
        step_blocks(&world),
        [
            "Add keeps its result",
            "Add names the sum before it returns it.",
            "stop calc.go"
        ]
    );
    assert!(cursor_in_stop(&world, "calc.go"));

    press(&mut world, Key::Right);
    assert_eq!(
        arrow_line(&world),
        ["Arrow 2 of 2", "helper()", "Calc → Util"]
    );
    assert_eq!(
        step_blocks(&world),
        [
            "The helper joins",
            "The helper is new, and only Add calls it.",
            "stop util.go"
        ]
    );
    assert!(cursor_in_stop(&world, "util.go"));
    for _ in 0..4 {
        press(&mut world, Key::Up);
    }
    let heading = cursor(&world).key.unwrap();
    let inside: Vec<String> = world
        .screen()
        .descendants(Target::key(heading.as_str()))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert_eq!(inside.first().map(String::as_str), Some("The helper joins"));
    for _ in 0..5 {
        press(&mut world, Key::Down);
    }
    assert!(cursor_in_stop(&world, "util.go"));
    assert!(world.runtime_is_at_rest());
}

/// The review's tile, with the daemon keeping its draft, showing the fixture as a review or,
/// without `review`, as plain markdown, and then `edited` when given; then a comment, started
/// with `?`, which the review's keys would take, written on a drag from `from` to `to`: each a
/// text and a fraction of its width along its first line. What the daemon saved.
fn comment(
    review: Option<ReviewBundle>,
    edited: Option<&str>,
    from: (&str, f32),
    to: (&str, f32),
) -> (World, serde_json::Value) {
    let mut world = start();
    let kept = std::sync::Arc::new(std::sync::Mutex::new(Vec::<serde_json::Value>::new()));
    let draft = |marks: &[serde_json::Value], r: &serde_json::Value, event: &str| {
        json!({
            "event": event,
            "request_id": r["request_id"],
            "document_uri": r["document_uri"],
            "source_kind": r["source_kind"],
            "success": true,
            "annotations": marks,
            "generation": marks.len(),
        })
    };
    let read = kept.clone();
    world.daemon.on("markdown_annotations_get", move |r| {
        draft(&read.lock().unwrap(), r, "markdown_annotations_get_result")
    });
    let written = kept.clone();
    world.daemon.on("markdown_annotations_save", move |r| {
        let mut marks = written.lock().unwrap();
        marks.extend(r["upserts"].as_array().into_iter().flatten().cloned());
        draft(&marks, r, "markdown_annotations_save_result")
    });
    deliver(&mut world, DOCUMENT, review.clone());
    if let Some(edited) = edited {
        deliver(&mut world, edited, review);
    }
    let point = |world: &World, (words, along): (&str, f32)| {
        let [x, y, w, _] = world
            .screen()
            .bounds(Target::role("label").name(words))
            .or_else(|| world.screen().bounds(Target::role("heading").name(words)))
            .unwrap_or_else(|| panic!("no {words:?}:\n{}", world.screen()));
        (x + 1.0 + (w - 2.0) * along, y + 8.0)
    };
    let (start, end) = (point(&world, from), point(&world, to));
    world.gesture(|app| {
        app.drag_from(start, end);
        app.release(end);
    });
    world.settle();
    world.gesture(|app| app.type_text("?"));
    world.settle();
    world.gesture(|app| app.type_text(" Why a name"));
    world.settle();
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
    let saved = kept.lock().unwrap().clone();
    assert_eq!(saved.len(), 1, "one comment is saved: {saved:?}");
    (world, saved[0].clone())
}

const FIRST: &str = "Add used to return its sum directly.";
const LATER: &str = "A paragraph between the stops.";

#[test]
fn a_passage_selected_in_the_briefs_prose_is_commented_on_as_in_plain_markdown_and_takes_the_keys_typed()
 {
    let whole = |review| comment(review, None, (FIRST, 0.0), (FIRST, 1.0));
    let (_, plain) = whole(None);
    let (world, reviewed) = whole(Some(bundle()));
    assert_eq!(reviewed["text"], "? Why a name");
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Review keys"))
            .is_none(),
        "the typed ? went to the comment"
    );
    assert_eq!(reviewed["anchor"]["exact"], FIRST);
    assert_eq!(
        reviewed["anchor"], plain["anchor"],
        "the review anchors the comment where plain markdown does"
    );
    assert!(
        world
            .screen()
            .find(Target::role("button").name(FIRST))
            .is_some(),
        "the mark paints over its passage:\n{}",
        world.screen()
    );
}

#[test]
fn a_selection_in_the_briefs_prose_anchors_as_plain_markdown_after_the_text_above_it_changes() {
    let edited = DOCUMENT.replace(FIRST, "Add used to return the sum of a and b directly.");
    let later = |review| comment(review, Some(&edited), (LATER, 0.0), (LATER, 1.0));
    let (_, plain) = later(None);
    let (_, reviewed) = later(Some(bundle()));
    assert_eq!(reviewed["anchor"]["exact"], LATER);
    assert_eq!(reviewed["anchor"], plain["anchor"]);
}

#[test]
fn a_selection_dragged_across_the_briefs_blocks_keeps_to_the_block_it_starts_in() {
    let across = |review| comment(review, None, (FIRST, 0.15), ("Add keeps its result", 0.1));
    let (_, plain) = across(None);
    let (_, reviewed) = across(Some(bundle()));
    let kept = |anchor: &serde_json::Value| {
        ["block_id", "end", "end_line", "suffix"].map(|field| anchor[field].clone())
    };
    assert_eq!(kept(&reviewed["anchor"]), kept(&plain["anchor"]));
    let exact = reviewed["anchor"]["exact"].as_str().unwrap_or_default();
    assert!(
        FIRST.ends_with(exact) && exact.len() > 10,
        "the passage runs from where the drag started to its block's end: {exact:?}"
    );
}

fn calc_file() -> Vec<ReviewRow> {
    calc_rows()
        .into_iter()
        .map(|mut r| {
            r.head = r.now;
            r
        })
        .collect()
}

fn calc_rows() -> Vec<ReviewRow> {
    let mut rows = vec![
        line(" ", Some(1), Some(1), "package calc"),
        line(" ", Some(2), Some(2), ""),
    ];
    rows.extend(bundle().stops[0].rows.clone());
    rows.extend([
        line(" ", Some(6), Some(7), ""),
        line(" ", Some(7), Some(8), "func Sub(a, b int) int {"),
        line("-", Some(8), None, "\treturn a - b"),
        line("+", None, Some(9), "\treturn b - a"),
        line(" ", Some(9), Some(10), "}"),
    ]);
    rows
}

/// calc.go after someone inserts two lines above Add: every line from 3 on moves down by two.
fn calc_with_two_lines_above() -> Vec<ReviewRow> {
    let shifted: Vec<ReviewRow> = calc_file()
        .into_iter()
        .map(|mut r| {
            if r.now.is_some_and(|n| n >= 3) {
                r.now = r.now.map(|n| n + 2);
            }
            r
        })
        .collect();
    let mut rows = shifted[..2].to_vec();
    rows.push(line("+", None, Some(3), "// Add sums."));
    rows.push(line("+", None, Some(4), "// It names the sum."));
    rows.extend(shifted[2..].to_vec());
    rows
}

type Files = std::sync::Arc<std::sync::Mutex<Vec<ReviewRow>>>;

/// Serves the review's files: calc.go as the returned handle holds it, util.go, and a line
/// naming any other path.
fn serve(world: &World) -> Files {
    let calc: Files = std::sync::Arc::new(std::sync::Mutex::new(calc_file()));
    let held = calc.clone();
    world.daemon.on("review_file", move |r| {
        let path = r["path"].as_str().unwrap_or_default().to_owned();
        let (rows, hash) = match path.as_str() {
            "calc.go" => {
                let rows = held.lock().unwrap().clone();
                let hash = if rows.len() == calc_file().len() {
                    "h1".to_owned()
                } else {
                    format!("h1-{}", rows.len())
                };
                (rows, hash)
            }
            "util.go" => (bundle().stops[1].rows.clone(), "h2".to_owned()),
            "go.sum" => (
                vec![
                    line(" ", Some(1), Some(1), "// go.sum"),
                    line(" ", Some(2), Some(2), &"x".repeat(300)),
                ],
                "h4".to_owned(),
            ),
            other => (
                vec![line(" ", Some(1), Some(1), &format!("// {other}"))],
                "h3".to_owned(),
            ),
        };
        serde_json::to_value(ReviewFileResultMessage {
            success: true,
            path: Some(path),
            rows: Some(rows),
            hash: Some(hash),
            ..ReviewFileResultMessage::new()
        })
        .unwrap()
    });
    calc
}

fn open_served(content: &str, review: ReviewBundle) -> (World, Files) {
    let mut world = start();
    let calc = serve(&world);
    deliver(&mut world, content, Some(review));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    (world, calc)
}

/// The file view, if one shows.
fn file_view(world: &World) -> Option<Vec<String>> {
    world.screen().find(Target::key("tile.m.review.view"))?;
    Some(
        world
            .screen()
            .descendants(Target::key("tile.m.review.view"))
            .into_iter()
            .filter_map(|n| n.name)
            .collect(),
    )
}

/// The row the file view's cursor stands on.
fn file_cursor(world: &World) -> Option<String> {
    world
        .screen()
        .descendants(Target::key("tile.m.review.view"))
        .into_iter()
        .find(|n| n.role == "row" && n.is("selected"))
        .and_then(|n| n.name)
}

fn file_reads(world: &World) -> Vec<String> {
    world
        .daemon
        .sent_of("review_file")
        .iter()
        .map(|r| r["path"].as_str().unwrap_or_default().to_owned())
        .collect()
}

fn ctrl_o(world: &mut World) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Command('o'),
            mods: Mods {
                ctrl: true,
                ..Mods::default()
            },
        })
    });
}

#[test]
fn opens_the_whole_file_at_the_cursor_moves_between_its_changes_and_closes() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    let warmed = file_reads(&world).len();
    press(&mut world, Key::Right);
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);

    press(&mut world, Key::Enter);
    let read = &world.daemon.sent_of("review_file")[warmed];
    assert_eq!(
        (
            read["document"].as_str(),
            read["path"].as_str(),
            read.get("host")
        ),
        (Some(DOC), Some("calc.go"), None)
    );
    let view = file_view(&world).expect("the file view shows");
    assert!(view.iter().any(|t| t == "calc.go"), "{view:?}");
    assert!(view.iter().any(|t| t == "10 lines"), "{view:?}");
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 4: \tsum := a + b")
    );

    press(&mut world, Key::Right);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("removed line 8: \treturn a - b")
    );
    press(&mut world, Key::Left);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("removed line 4: \treturn a + b")
    );
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 5: \treturn sum")
    );

    press(&mut world, Key::Escape);
    assert!(file_view(&world).is_none());
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("added line 4: \tsum := a + b")
    );

    press(&mut world, Key::Enter);
    assert!(file_view(&world).is_some());
    ctrl_o(&mut world);
    assert!(file_view(&world).is_none());
    assert!(world.runtime_is_at_rest());
}

fn press_shifted(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods {
                shift: true,
                ..Mods::default()
            },
        })
    });
}

/// The review in `frame`, its tile holding the keyboard, with every read of calc.go answered
/// by `calc` and the other files served as usual.
fn open_with_calc(
    frame: &str,
    calc: impl Fn() -> Option<serde_json::Value> + Send + 'static,
) -> World {
    let mut world = start();
    world
        .daemon
        .on("review_file", move |r| match r["path"].as_str() {
            Some("calc.go") => calc(),
            Some(path) => Some(file_answer(
                path,
                vec![line(" ", Some(1), Some(1), "//")],
                "h",
            )),
            None => None,
        });
    deliver(
        &mut world,
        DOCUMENT,
        Some(ReviewBundle {
            frame: frame.into(),
            ..bundle()
        }),
    );
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    if frame == "brief" {
        press(&mut world, Key::Right);
        press(&mut world, Key::Down);
        press(&mut world, Key::Down);
        press(&mut world, Key::Enter);
    }
    world
}

#[test]
fn the_file_views_keys_leave_an_empty_a_loading_and_an_unreadable_file_open_with_no_cursor() {
    let unreadable = || {
        Some(
            serde_json::to_value(ReviewFileResultMessage {
                success: false,
                error: Some("open calc.go: permission denied".into()),
                ..ReviewFileResultMessage::new()
            })
            .unwrap(),
        )
    };
    type Answer = fn() -> Option<serde_json::Value>;
    let files: [(&str, Answer, &str); 3] = [
        (
            "empty",
            || Some(file_answer("calc.go", vec![], "h0")),
            "0 lines",
        ),
        ("loading", || None, "Reading calc.go…"),
        ("unreadable", unreadable, "open calc.go: permission denied"),
    ];
    for frame in ["brief", "walkthrough"] {
        for (file, answer, shows) in files {
            let mut world = open_with_calc(frame, answer);
            let keys = [Key::Up, Key::Down, Key::PageUp, Key::PageDown];
            for key in keys {
                press(&mut world, key.clone());
                press_shifted(&mut world, key);
            }
            if frame == "brief" {
                press(&mut world, Key::Left);
                press(&mut world, Key::Right);
            }
            press_shifted(&mut world, Key::Left);
            press_shifted(&mut world, Key::Right);
            let view = file_view(&world).unwrap_or_else(|| panic!("{frame}, {file}: no file view"));
            assert!(view.iter().any(|t| t == shows), "{frame}, {file}: {view:?}");
            assert_eq!(file_cursor(&world), None, "{frame}, {file}");
            if frame == "walkthrough" {
                assert_eq!(stage_stop(&world).as_deref(), Some("calc.go"), "{file}");
            }
        }
    }
}

fn calc_with_a_long_line() -> Vec<ReviewRow> {
    let mut rows = calc_file();
    rows.push(line(
        " ",
        Some(10),
        Some(11),
        &format!("// {}", "x".repeat(400)),
    ));
    rows
}

const FILE_BODY: &str = "tile.m.review.view.body";

fn sideways(world: &World) -> f32 {
    world
        .screen()
        .scrolled_sideways(Target::key(FILE_BODY))
        .unwrap_or(0.0)
}

#[test]
fn shift_left_and_right_scroll_a_long_line_sideways_by_half_the_view_in_the_file_view_and_the_walkthrough()
 {
    for frame in ["brief", "walkthrough"] {
        let mut world = open_with_calc(frame, || {
            Some(file_answer("calc.go", calc_with_a_long_line(), "h"))
        });
        let cursor = file_cursor(&world);
        assert_eq!(
            cursor.as_deref(),
            Some("added line 4: \tsum := a + b"),
            "{frame}"
        );
        let half = world.screen().bounds(Target::key(FILE_BODY)).unwrap()[2] / 2.0;
        assert_eq!(sideways(&world), 0.0, "{frame}");

        press_shifted(&mut world, Key::Right);
        assert!(
            (sideways(&world) - half).abs() < 1.0,
            "{frame}: {} for {half}",
            sideways(&world)
        );
        press_shifted(&mut world, Key::Right);
        assert!((sideways(&world) - 2.0 * half).abs() < 1.0, "{frame}");
        assert_eq!(
            file_cursor(&world),
            cursor,
            "{frame}: the cursor stays on its row"
        );
        if frame == "walkthrough" {
            assert_eq!(stage_stop(&world).as_deref(), Some("calc.go"));
        }

        press_shifted(&mut world, Key::Left);
        assert!((sideways(&world) - half).abs() < 1.0, "{frame}");
        press_shifted(&mut world, Key::Left);
        press_shifted(&mut world, Key::Left);
        assert_eq!(sideways(&world), 0.0, "{frame}");
        assert_eq!(file_cursor(&world), cursor, "{frame}");
        world.advance(std::time::Duration::from_secs(5));
        assert!(world.runtime_is_at_rest(), "{frame}");
    }
}

#[test]
fn shows_the_notes_of_every_stop_in_the_file_under_it_on_the_cursors_line() {
    let document = DOCUMENT.replace(
        "## Not shown",
        "### calc.go:9-10\n\n- L9: Order flipped.\n\n## Not shown",
    );
    let mut review = bundle();
    let rows = calc_file();
    review.stops.push(ReviewStop {
        heading_line: 26,
        path: "calc.go".into(),
        from: 9,
        to: 10,
        now_from: 9,
        now_to: 10,
        file_hash: "h1".into(),
        rows: rows[rows.len() - 4..].to_vec(),
        notes: vec![note(28, 9, false, "Order flipped.")],
        ..ReviewStop::default()
    });
    review.not_shown[0].line = 32;
    let (mut world, _) = open_served(&document, review);
    press(&mut world, Key::Right);
    press(&mut world, Key::Enter);
    let strip = |world: &World| {
        world
            .screen()
            .find(Target::role("region").name("Marks on this line"))
            .map(|r| {
                world
                    .screen()
                    .descendants(Target::key(r.key.unwrap_or_default()))
                    .into_iter()
                    .filter_map(|n| n.name)
                    .collect::<Vec<_>>()
            })
            .unwrap_or_default()
    };
    for _ in 0..12 {
        if file_cursor(&world).is_some_and(|c| c.starts_with("added line 9")) {
            break;
        }
        press(&mut world, Key::Down);
    }
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 9: \treturn b - a")
    );
    let notes = strip(&world);
    assert!(
        notes.iter().any(|t| t == "agent L9: Order flipped."),
        "{notes:?}"
    );
    assert!(
        !notes.iter().any(|t| t.contains("The sum gets a name.")),
        "{notes:?}"
    );
}

#[test]
fn keeps_an_open_file_view_current_a_new_bundle_reads_the_file_again_and_the_cursor_stays_on_its_line()
 {
    let (mut world, calc) = open_served(DOCUMENT, bundle());
    press(&mut world, Key::Right);
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    press(&mut world, Key::Enter);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 4: \tsum := a + b")
    );
    let before = file_reads(&world)
        .iter()
        .filter(|p| *p == "calc.go")
        .count();

    *calc.lock().unwrap() = calc_with_two_lines_above();
    deliver(
        &mut world,
        DOCUMENT,
        Some(ReviewBundle {
            head: "aaaaaaa".into(),
            ..bundle()
        }),
    );
    let after = file_reads(&world)
        .iter()
        .filter(|p| *p == "calc.go")
        .count();
    assert!(after > before, "{before} reads, then {after}");
    let view = file_view(&world).unwrap();
    assert!(
        view.iter()
            .any(|t| t == "added line 4: // It names the sum."),
        "{view:?}"
    );
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 6: \tsum := a + b")
    );
    press(&mut world, Key::Down);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 7: \treturn sum")
    );
}

#[test]
fn keeps_the_cursor_on_a_repeated_line_of_a_file_with_no_base_when_the_bundle_is_recomputed() {
    let (mut world, calc) = open_served(DOCUMENT, bundle());
    *calc.lock().unwrap() = ["package calc", "", "func A() {", "\tx()", "", "\ty()", "}"]
        .iter()
        .zip(1..)
        .map(|(text, at)| line("+", None, Some(at), text))
        .collect();
    press(&mut world, Key::Right);
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    press(&mut world, Key::Enter);
    press(&mut world, Key::Down);
    assert_eq!(file_cursor(&world).as_deref(), Some("added line 5: "));

    deliver(
        &mut world,
        DOCUMENT,
        Some(ReviewBundle {
            head: "aaaaaaa".into(),
            ..bundle()
        }),
    );
    assert_eq!(file_cursor(&world).as_deref(), Some("added line 5: "));
}

#[test]
fn shows_more_lines_around_a_stop_with_e_fills_the_tile_with_z_and_opens_a_not_shown_file() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    press(&mut world, Key::Right);
    assert_eq!(stops(&world)[0], "calc.go L3–6");

    world.gesture(|app| app.type_text("e"));
    assert_eq!(stops(&world)[0], "calc.go L1–10");
    let rows = stop_lines(&world, &stop_key(&world, "calc.go"));
    assert_eq!(
        rows.first().map(String::as_str),
        Some("unchanged line 1: package calc")
    );
    assert_eq!(
        rows.last().map(String::as_str),
        Some("unchanged line 10: }")
    );

    world.gesture(|app| app.type_text("z"));
    let tile = world.screen().bounds(Target::key("tile.m")).unwrap();
    let filled = world
        .screen()
        .bounds(Target::key("tile.m.review.view"))
        .unwrap();
    assert!(
        (filled[2] - tile[2]).abs() < 2.0,
        "filled {filled:?} in {tile:?}"
    );
    world.gesture(|app| app.type_text("z"));
    let beside = world
        .screen()
        .bounds(Target::key("tile.m.review.view"))
        .unwrap();
    assert!(beside[2] < tile[2] * 0.7, "beside {beside:?} in {tile:?}");
    press(&mut world, Key::Escape);
    assert!(file_view(&world).is_none());

    for _ in 0..30 {
        if cursor(&world).name.as_deref() == Some("go.sum checksums only.") {
            break;
        }
        press(&mut world, Key::Down);
    }
    press(&mut world, Key::Enter);
    assert_eq!(
        file_reads(&world).last().map(String::as_str),
        Some("go.sum")
    );
    let view = file_view(&world).unwrap();
    assert!(
        view.iter().any(|t| t == "unchanged line 1: // go.sum"),
        "{view:?}"
    );
    let long = world
        .screen()
        .descendants(Target::key("tile.m.review.view"))
        .into_iter()
        .find(|n| {
            n.name
                .as_deref()
                .is_some_and(|t| t.starts_with("unchanged line 2: xxx"))
        })
        .and_then(|n| n.key)
        .unwrap();
    let row = world.screen().bounds(Target::key(long.as_str())).unwrap();
    assert!(
        row[3] <= 21.0,
        "a long line keeps one row in the file view: {row:?}"
    );
}

fn file_answer(path: &str, rows: Vec<ReviewRow>, hash: &str) -> serde_json::Value {
    serde_json::to_value(ReviewFileResultMessage {
        success: true,
        path: Some(path.into()),
        rows: Some(rows),
        hash: Some(hash.into()),
        ..ReviewFileResultMessage::new()
    })
    .unwrap()
}

#[test]
fn an_older_read_of_an_open_file_never_lands_over_a_newer_one() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    press(&mut world, Key::Right);
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    press(&mut world, Key::Enter);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 4: \tsum := a + b")
    );

    world.daemon.on("review_file", |_| ());
    for head in ["aaaaaaa", "bbbbbbb"] {
        deliver(
            &mut world,
            DOCUMENT,
            Some(ReviewBundle {
                head: head.into(),
                ..bundle()
            }),
        );
    }
    let reads: Vec<_> = world
        .daemon
        .sent_of("review_file")
        .into_iter()
        .filter(|r| r["path"] == "calc.go")
        .collect();
    let (older, newer) = (&reads[reads.len() - 2], &reads[reads.len() - 1]);
    world.daemon.reply_to(
        newer,
        file_answer("calc.go", calc_with_two_lines_above(), "h-new"),
    );
    world.settle();
    world
        .daemon
        .reply_to(older, file_answer("calc.go", calc_file(), "h-old"));
    world.settle();
    let view = file_view(&world).unwrap();
    assert!(
        view.iter()
            .any(|t| t == "added line 4: // It names the sum."),
        "{view:?}"
    );
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 6: \tsum := a + b")
    );
}

#[test]
fn colors_each_stops_file_once_however_often_the_bundle_changes_while_it_reads() {
    let mut world = start();
    world.daemon.on("review_file", |_| ());
    deliver(&mut world, DOCUMENT, Some(bundle()));
    deliver(
        &mut world,
        DOCUMENT,
        Some(ReviewBundle {
            head: "aaaaaaa".into(),
            ..bundle()
        }),
    );
    assert_eq!(file_reads(&world), ["calc.go"]);

    let first = world.daemon.sent_of("review_file")[0].clone();
    world
        .daemon
        .reply_to(&first, file_answer("calc.go", calc_file(), "h1"));
    world.settle();
    assert_eq!(file_reads(&world), ["calc.go", "util.go"]);
    let second = world.daemon.sent_of("review_file")[1].clone();
    world.daemon.reply_to(
        &second,
        file_answer("util.go", bundle().stops[1].rows.clone(), "h2"),
    );
    world.settle();

    deliver(
        &mut world,
        DOCUMENT,
        Some(ReviewBundle {
            head: "bbbbbbb".into(),
            ..bundle()
        }),
    );
    assert_eq!(file_reads(&world), ["calc.go", "util.go"]);
}

/// The stop the Walkthrough's stage shows, by its path: the script's marked stop.
fn stage_stop(world: &World) -> Option<String> {
    world.screen().find(Target::key("tile.m.document"))?;
    world
        .screen()
        .descendants(Target::key("tile.m.document"))
        .into_iter()
        .find(|n| n.role == "list-item" && n.is("selected"))
        .and_then(|n| n.name)
        .map(|name| name.split(' ').next().unwrap_or_default().to_owned())
}

fn marks_strip(world: &World) -> Vec<String> {
    match world.screen().find(Target::key("tile.m.review.view.marks")) {
        Some(_) => world
            .screen()
            .descendants(Target::key("tile.m.review.view.marks"))
            .into_iter()
            .filter_map(|n| n.name)
            .collect(),
        None => Vec::new(),
    }
}

#[test]
fn opens_in_the_walkthrough_the_document_picks_the_stops_as_a_script_beside_the_file_of_the_current_stop()
 {
    let (mut world, _) = open_served(
        DOCUMENT,
        ReviewBundle {
            frame: "walkthrough".into(),
            ..bundle()
        },
    );
    assert_eq!(frame_tabs(&world), ["1 Brief", "2 Walkthrough *"]);
    assert_eq!(stage_stop(&world).as_deref(), Some("calc.go"));
    let script: Vec<String> = world
        .screen()
        .descendants(Target::key("tile.m.document"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(
        script.iter().any(|t| t == "Add keeps its result"),
        "{script:?}"
    );
    assert!(
        !script.iter().any(|t| t.starts_with("added line")),
        "{script:?}"
    );
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 4: \tsum := a + b")
    );
    assert!(
        marks_strip(&world)
            .iter()
            .any(|t| t == "look here L4: The sum gets a name.")
    );

    press(&mut world, Key::Down);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 5: \treturn sum")
    );
    let strip = marks_strip(&world);
    assert!(
        strip.iter().any(|t| t == "agent L5: Then it is returned."),
        "{strip:?}"
    );
    assert!(
        !strip.iter().any(|t| t.contains("The sum gets a name.")),
        "{strip:?}"
    );

    press(&mut world, Key::Right);
    assert_eq!(stage_stop(&world).as_deref(), Some("util.go"));
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 2: func helper() {}")
    );
    press(&mut world, Key::Right);
    assert_eq!(stage_stop(&world).as_deref(), Some("util.go"));
    press(&mut world, Key::Left);
    assert_eq!(stage_stop(&world).as_deref(), Some("calc.go"));

    world.gesture(|app| app.type_text("z"));
    assert!(stage_stop(&world).is_none(), "the script hides");
    world.gesture(|app| app.type_text("z"));
    assert_eq!(stage_stop(&world).as_deref(), Some("calc.go"));

    ctrl_o(&mut world);
    press(&mut world, Key::Escape);
    assert!(file_view(&world).is_some());
    assert_eq!(stage_stop(&world).as_deref(), Some("calc.go"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn follows_the_agent_editing_the_document_the_stage_shows_the_stop_that_takes_the_place_of_a_removed_one()
 {
    let (mut world, _) = open_served(
        DOCUMENT,
        ReviewBundle {
            frame: "walkthrough".into(),
            ..bundle()
        },
    );
    assert_eq!(stage_stop(&world).as_deref(), Some("calc.go"));

    let mut util = bundle().stops[1].clone();
    util.heading_line -= 4;
    for n in &mut util.notes {
        n.line -= 4;
    }
    let mut edited = ReviewBundle {
        frame: "walkthrough".into(),
        stops: vec![util],
        ..bundle()
    };
    for entry in &mut edited.not_shown {
        entry.line -= 4;
    }
    let content = DOCUMENT.replace(
        "### calc.go:3-6\n\n- L4!: The sum gets a name.\n- L5: Then it is returned.\n",
        "",
    );
    deliver(&mut world, &content, Some(edited));
    assert_eq!(stage_stop(&world).as_deref(), Some("util.go"));
    assert!(file_view(&world).unwrap().iter().any(|t| t == "util.go"));
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 2: func helper() {}")
    );
}

#[test]
fn switches_frames_with_1_2_and_3_and_keeps_the_stop_and_line_the_cursor_is_on() {
    let (mut world, _) = open_served(&map_document(), map_bundle("brief"));
    assert_eq!(frame_tabs(&world), ["1 Brief *", "2 Walkthrough", "3 Map"]);
    press(&mut world, Key::Right);
    press(&mut world, Key::Right);
    press(&mut world, Key::Down);
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("added line 2: func helper() {}")
    );

    type_key(&mut world, "2");
    assert_eq!(stage_stop(&world).as_deref(), Some("util.go"));
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 2: func helper() {}")
    );

    press(&mut world, Key::Left);
    press(&mut world, Key::Down);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 5: \treturn sum")
    );
    type_key(&mut world, "3");
    assert_eq!(map_current(&world).as_deref(), Some("Add(a, b)"));
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("added line 5: \treturn sum")
    );

    press(&mut world, Key::Right);
    type_key(&mut world, "1");
    assert!(
        world
            .screen()
            .find(Target::key("tile.m.review.map"))
            .is_none()
    );
    assert!(cursor_in_stop(&world, "util.go"));
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("unchanged line 1: package calc")
    );
}

fn serve_search(world: &World) {
    world.daemon.on("review_search", |r| {
        let calc = r["query"] == "calc";
        serde_json::to_value(ReviewSearchResultMessage {
            success: true,
            files: Some(if calc { vec!["calc.go".into()] } else { vec![] }),
            hits: Some(vec![ReviewSearchHit {
                path: "calc.go".into(),
                line: 9,
                text: "\treturn b - a".into(),
            }]),
            ..ReviewSearchResultMessage::new()
        })
        .unwrap()
    });
}

fn options(world: &World) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key("review.search.list"))
        .into_iter()
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with("review.search.option."))
        })
        .filter_map(|n| n.name)
        .collect()
}

fn search(world: &mut World, query: &str) {
    world.gesture(|app| app.type_text(query));
}

#[test]
fn lists_the_changed_files_in_an_empty_search_searches_the_repository_by_name_and_text_and_opens_what_it_finds()
 {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    serve_search(&world);
    type_key(&mut world, "/");
    let changed = ["calc.go +3 −2", "util.go +1 −0", "go.sum +1 −1"];
    assert_eq!(options(&world), changed);
    assert!(world.daemon.sent_of("review_search").is_empty());

    search(&mut world, "calc");
    let asked: Vec<(String, String)> = world
        .daemon
        .sent_of("review_search")
        .iter()
        .map(|r| {
            (
                r["document"].as_str().unwrap_or_default().to_owned(),
                r["query"].as_str().unwrap_or_default().to_owned(),
            )
        })
        .collect();
    assert_eq!(asked.last(), Some(&(DOC.to_owned(), "calc".to_owned())));
    assert_eq!(options(&world), ["calc.go file", "return b - a calc.go:9"]);

    press(&mut world, Key::Down);
    press(&mut world, Key::Enter);
    assert!(world.screen().find(Target::key("review.search")).is_none());
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 9: \treturn b - a")
    );
}

#[test]
fn opens_a_file_found_over_the_one_open_and_goes_back_to_it() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    serve_search(&world);
    press(&mut world, Key::Right);
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    press(&mut world, Key::Enter);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 4: \tsum := a + b")
    );
    press(&mut world, Key::Right);
    press(&mut world, Key::Left);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("removed line 4: \treturn a + b")
    );

    type_key(&mut world, "/");
    search(&mut world, "sub");
    press(&mut world, Key::Enter);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 9: \treturn b - a")
    );
    let back = world
        .screen()
        .descendants(Target::key("tile.m.review.view"))
        .into_iter()
        .any(|n| {
            n.name
                .as_deref()
                .is_some_and(|t| t.starts_with("← back ") && t != "← back")
        });
    assert!(back);

    ctrl_o(&mut world);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("removed line 4: \treturn a + b")
    );
    press(&mut world, Key::Escape);
    assert!(file_view(&world).is_none());
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("added line 4: \tsum := a + b")
    );
}

#[test]
fn reads_every_file_on_the_stack_again_not_only_the_top_one() {
    let (mut world, calc) = open_served(
        DOCUMENT,
        ReviewBundle {
            frame: "walkthrough".into(),
            ..bundle()
        },
    );
    serve_search(&world);
    press(&mut world, Key::Down);
    type_key(&mut world, "/");
    search(&mut world, "sub");
    press(&mut world, Key::Enter);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 9: \treturn b - a")
    );

    *calc.lock().unwrap() = calc_with_two_lines_above();
    deliver(
        &mut world,
        DOCUMENT,
        Some(ReviewBundle {
            frame: "walkthrough".into(),
            head: "aaaaaaa".into(),
            ..bundle()
        }),
    );
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 11: \treturn b - a")
    );

    ctrl_o(&mut world);
    assert_eq!(stage_stop(&world).as_deref(), Some("calc.go"));
    assert!(
        file_view(&world)
            .unwrap()
            .iter()
            .any(|t| t == "added line 4: // It names the sum.")
    );
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 7: \treturn sum")
    );
}

#[test]
fn enter_on_a_not_shown_folder_searches_its_path() {
    let mut review = bundle();
    review.not_shown[0].path = "vendor/".into();
    let document = DOCUMENT.replace("- go.sum: checksums only.", "- vendor/: copied as is.");
    let (mut world, _) = open_served(&document, review);
    serve_search(&world);
    for _ in 0..30 {
        press(&mut world, Key::Down);
        let at = cursor(&world).name.unwrap_or_default();
        if at.starts_with("vendor/") {
            break;
        }
    }
    press(&mut world, Key::Enter);
    let field = world.screen().get(Target::key("review.search.field"));
    assert_eq!(field.value.as_deref(), Some("vendor/"), "{field:?}");
    assert_eq!(
        world
            .daemon
            .sent_of("review_search")
            .last()
            .map(|r| r["query"].clone()),
        Some(json!("vendor/"))
    );
}

type Marks = std::sync::Arc<std::sync::Mutex<Vec<serde_json::Value>>>;

/// Keeps the review document's annotation draft as the daemon does, and records each send.
fn keep_drafts(world: &World) -> (Marks, Marks, Generation) {
    let kept: Marks = Default::default();
    let sends: Marks = Default::default();
    let answer =
        |marks: &[serde_json::Value], r: &serde_json::Value, event: &str, generation: usize| {
            json!({
                "event": event,
                "request_id": r["request_id"],
                "document_uri": r["document_uri"],
                "source_kind": r["source_kind"],
                "success": true,
                "status": "delivered",
                "annotations": marks,
                "generation": generation,
            })
        };
    let count = std::sync::Arc::new(std::sync::atomic::AtomicUsize::new(1));
    let (read, c) = (kept.clone(), count.clone());
    world.daemon.on("markdown_annotations_get", move |r| {
        let g = c.load(std::sync::atomic::Ordering::SeqCst);
        answer(
            &read.lock().unwrap(),
            r,
            "markdown_annotations_get_result",
            g,
        )
    });
    let (written, c) = (kept.clone(), count.clone());
    world.daemon.on("markdown_annotations_save", move |r| {
        let mut marks = written.lock().unwrap();
        let removed: Vec<_> = r["removed_ids"].as_array().cloned().unwrap_or_default();
        marks.retain(|m| !removed.contains(&m["id"]));
        for up in r["upserts"].as_array().into_iter().flatten() {
            marks.retain(|m| m["id"] != up["id"]);
            marks.push(up.clone());
        }
        let g = c.fetch_add(1, std::sync::atomic::Ordering::SeqCst) + 1;
        answer(&marks, r, "markdown_annotations_save_result", g)
    });
    let (left, sent, c) = (kept.clone(), sends.clone(), count.clone());
    world.daemon.on("markdown_annotations_submit", move |r| {
        sent.lock().unwrap().push(r.clone());
        let mut marks = left.lock().unwrap();
        if r["only_ids"].is_null() {
            marks.clear();
        }
        let g = c.fetch_add(1, std::sync::atomic::Ordering::SeqCst) + 1;
        answer(&marks, r, "markdown_annotations_submit_result", g)
    });
    (kept, sends, count)
}

type Generation = std::sync::Arc<std::sync::atomic::AtomicUsize>;

/// The fixture with each row's line at head as its line now, as a review just presented has.
fn presented() -> ReviewBundle {
    let mut review = bundle();
    for stop in &mut review.stops {
        for r in &mut stop.rows {
            r.head = r.now;
        }
    }
    review
}

fn open_marking(review: ReviewBundle) -> (World, Marks, Marks) {
    let (world, kept, sends, _) = open_marking_counted(review);
    (world, kept, sends)
}

fn open_marking_counted(review: ReviewBundle) -> (World, Marks, Marks, Generation) {
    let mut world = start_with_session(Some("s-a"));
    serve(&world);
    let (kept, sends, generation) = keep_drafts(&world);
    deliver(&mut world, DOCUMENT, Some(review));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    (world, kept, sends, generation)
}

/// Presses the send key as this platform binds it.
fn send_key(world: &mut World) {
    let ev = match attn_app::Keymap::from_setting(None, rune::KeyPlatform::current())
        .binding(&attn_app::Action::MarkdownSendAnnotations)
    {
        Some(attn_app::Binding::Chord(chord)) => attn_testkit::key_event(&chord),
        other => panic!("the send has no single chord: {other:?}"),
    };
    world.gesture(|app| app.key(ev));
}

fn shift(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods {
                shift: true,
                ..Mods::default()
            },
        })
    });
}

fn write(world: &mut World, words: &str) {
    world.gesture(|app| app.type_text(words));
    press(world, Key::Enter);
    world.advance(std::time::Duration::from_secs(1));
    world.settle();
}

#[test]
fn writes_a_note_on_the_selected_lines_of_a_stop_quoting_them_and_shows_it_under_the_last_line() {
    let (mut world, kept, _) = open_marking(presented());
    press(&mut world, Key::Right);
    press(&mut world, Key::Down);
    shift(&mut world, Key::Down);
    type_key(&mut world, "c");
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Write a note"))
            .is_some()
    );
    write(&mut world, "Name it once");

    let marks = kept.lock().unwrap().clone();
    assert_eq!(marks.len(), 1, "{marks:?}");
    let anchor = &marks[0]["anchor"];
    assert_eq!(anchor["file"], "calc.go");
    assert_eq!(
        (
            anchor["file_start_line"].clone(),
            anchor["file_end_line"].clone()
        ),
        (json!(4), json!(4))
    );
    assert_eq!(anchor["exact"], "-\treturn a + b\n+\tsum := a + b");
    assert_eq!(marks[0]["type"], "comment");
    let lines = stop_lines(&world, &stop_key(&world, "calc.go"));
    let under = lines
        .iter()
        .position(|l| l == "you L4: Name it once")
        .unwrap();
    assert_eq!(
        lines[under - 2],
        "added line 4: \tsum := a + b",
        "{lines:?}"
    );
}

#[test]
fn asks_a_question_at_once_keeps_it_and_shows_the_answer_when_the_daemon_says_the_draft_moved() {
    let (mut world, kept, sends, generation) = open_marking_counted(presented());
    press(&mut world, Key::Right);
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    type_key(&mut world, "a");
    let dialog = world
        .screen()
        .get(Target::role("dialog").name("Ask a question"));
    let said: Vec<String> = world
        .screen()
        .descendants(Target::key(dialog.key.unwrap()))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(said.iter().any(|t| t == "Sent to s-a at once"), "{said:?}");
    write(&mut world, "Why a name?");

    let sent = sends.lock().unwrap().clone();
    assert_eq!(sent.len(), 1, "{sent:?}");
    let id = kept.lock().unwrap()[0]["id"].clone();
    assert_eq!(sent[0]["only_ids"], json!([id]));
    assert_eq!(sent[0]["target_session_id"], "s-a");
    let waiting = shown(&world);
    assert!(
        waiting
            .iter()
            .any(|t| t == "Asked — waiting for the answer"),
        "{waiting:?}"
    );

    kept.lock().unwrap()[0]["answer"] = json!("It reads better.");
    world.daemon.emit(DesktopTileContentMessage {
        desktop_id: DESKTOP.into(),
        tile_id: "m".into(),
        tile_kind: "markdown".into(),
        path: DOC.into(),
        content: DOCUMENT.into(),
        review: Some(presented()),
        annotations_generation: Some(
            generation.fetch_add(1, std::sync::atomic::Ordering::SeqCst) as i64 + 1,
        ),
        ..DesktopTileContentMessage::new()
    });
    world.settle();
    world.advance(std::time::Duration::from_secs(1));
    let answered = shown(&world);
    assert!(
        answered.iter().any(|t| t == "answer: It reads better."),
        "{answered:?}"
    );
}

#[test]
fn sends_the_review_with_the_verdict_picked_in_the_sheet() {
    let (mut world, _, sends) = open_marking(presented());
    press(&mut world, Key::Right);
    type_key(&mut world, "c");
    write(&mut world, "Fine");
    send_key(&mut world);
    let sheet = world
        .screen()
        .get(Target::role("dialog").name("Send the review"));
    let said: Vec<String> = world
        .screen()
        .descendants(Target::key(sheet.key.unwrap()))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(
        said.iter().any(|t| t == "note calc.go L3: Fine"),
        "{said:?}"
    );
    let picked = world
        .screen()
        .all(Target::role("radio-button"))
        .into_iter()
        .find(|n| n.is("checked"))
        .and_then(|n| n.name);
    assert_eq!(picked.as_deref(), Some("request changes"));

    type_key(&mut world, "a");
    press(&mut world, Key::Enter);
    let sent = sends.lock().unwrap().clone();
    assert_eq!(
        sent.last().map(|s| s["verdict"].clone()),
        Some(json!("approve"))
    );
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Send the review"))
            .is_none()
    );
}

#[test]
fn sends_a_verdict_with_no_notes() {
    let (mut world, _, sends) = open_marking(presented());
    press(&mut world, Key::Down);
    send_key(&mut world);
    let picked = world
        .screen()
        .all(Target::role("radio-button"))
        .into_iter()
        .find(|n| n.is("checked"))
        .and_then(|n| n.name);
    assert_eq!(
        picked.as_deref(),
        Some("approve"),
        "with no notes, approve is picked"
    );
    press(&mut world, Key::Enter);
    let sent = sends.lock().unwrap().clone();
    assert_eq!(sent.len(), 1, "{sent:?}");
    assert_eq!(sent[0]["verdict"], "approve");
}

#[test]
fn a_review_bound_to_no_session_keeps_its_send_and_asks_nothing() {
    let mut world = start();
    serve(&world);
    let (_, sends, _) = keep_drafts(&world);
    deliver(&mut world, DOCUMENT, Some(presented()));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    press(&mut world, Key::Right);
    type_key(&mut world, "a");
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Ask a question"))
            .is_none()
    );

    send_key(&mut world);
    press(&mut world, Key::Enter);
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Send the review"))
            .is_some()
    );
    assert!(sends.lock().unwrap().is_empty());
}

#[test]
fn closes_the_composer_first_on_escape_then_the_selection_then_the_file_view() {
    let (mut world, _, _) = open_marking(presented());
    press(&mut world, Key::Right);
    press(&mut world, Key::Enter);
    shift(&mut world, Key::Down);
    type_key(&mut world, "c");
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Write a note"))
            .is_some()
    );
    press(&mut world, Key::Escape);
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Write a note"))
            .is_none()
    );
    assert!(file_view(&world).is_some());
    press(&mut world, Key::Escape);
    assert!(file_view(&world).is_some(), "the selection goes first");
    press(&mut world, Key::Escape);
    assert!(file_view(&world).is_none());
}

#[test]
fn shows_a_mark_as_moved_when_its_code_is_gone_and_sends_it_marked_as_moved() {
    let (mut world, kept, sends) = open_marking(presented());
    press(&mut world, Key::Right);
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    type_key(&mut world, "c");
    write(&mut world, "This line");
    let id = kept.lock().unwrap()[0]["id"].clone();

    let mut changed = presented();
    changed.stops[0].rows.retain(|r| r.head != Some(4));
    deliver(&mut world, DOCUMENT, Some(changed));
    let moved: Vec<String> = world
        .screen()
        .descendants(Target::key("tile.m.review.moved"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(moved.iter().any(|t| t == "you L4: This line"), "{moved:?}");

    send_key(&mut world);
    press(&mut world, Key::Enter);
    let sent = sends.lock().unwrap().clone();
    assert_eq!(
        sent.last().map(|s| s["orphaned_ids"].clone()),
        Some(json!([id]))
    );
}

/// The first Before and After switch's tabs, the selected one marked.
fn side_tabs(world: &World) -> Vec<String> {
    world
        .screen()
        .descendants(Target::role("tab-list").name("Sides"))
        .into_iter()
        .filter(|n| n.role == "tab")
        .take(2)
        .map(|n| {
            let name = n.name.clone().unwrap_or_default();
            if n.is("selected") {
                format!("{name} *")
            } else {
                name
            }
        })
        .collect()
}

#[test]
fn switches_sides_when_before_or_after_is_clicked_as_b_does_in_the_map_and_the_brief() {
    let mut world = open_map(map_bundle("map"));
    assert_eq!(side_tabs(&world), ["Before", "After *"]);
    press(&mut world, Key::Right);
    assert_eq!(map_current(&world).as_deref(), Some("helper()"));

    world.gesture(|app| app.click(Target::role("tab").name("Before")));
    assert_eq!(side_tabs(&world), ["Before *", "After"]);
    assert_eq!(map_diagrams(&world), [["Add(a, b)"]]);
    assert_eq!(arrow_line(&world)[0], "Arrow 1 of 1 · before the change");
    world.gesture(|app| app.click(Target::role("tab").name("Before")));
    assert_eq!(side_tabs(&world), ["Before *", "After"]);
    world.gesture(|app| app.click(Target::role("tab").name("After")));
    assert_eq!(side_tabs(&world), ["Before", "After *"]);
    assert_eq!(map_diagrams(&world), [["Add(a, b)", "helper()", "returns"]]);
    assert_eq!(map_current(&world).as_deref(), Some("Add(a, b)"));
    assert!(cursor_in_stop(&world, "calc.go"));
    press(&mut world, Key::Right);
    assert_eq!(map_current(&world).as_deref(), Some("helper()"));

    type_key(&mut world, "1");
    for _ in 0..30 {
        if cursor_arrow(&world).is_some() {
            break;
        }
        press(&mut world, Key::Down);
    }
    assert_eq!(cursor_arrow(&world).as_deref(), Some("Add(a, b)"));
    world.gesture(|app| app.click(Target::role("tab").name("Before")));
    assert_eq!(side_tabs(&world), ["Before *", "After"]);
    assert_eq!(cursor_arrow(&world).as_deref(), Some("Add(a, b)"));
    let key = cursor(&world).key.unwrap();
    assert!(key.ends_with(".arrow.34"), "{key}");
    world.gesture(|app| app.click(Target::role("tab").name("After")));
    assert_eq!(side_tabs(&world), ["Before", "After *"]);
    assert_eq!(cursor_arrow(&world).as_deref(), Some("Add(a, b)"));
    type_key(&mut world, "b");
    assert_eq!(side_tabs(&world), ["Before *", "After"]);
    world.advance(std::time::Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

/// What the send sheet says, as assistive tech reads it.
fn sheet_says(world: &World) -> Vec<String> {
    let sheet = world
        .screen()
        .get(Target::role("dialog").name("Send the review"));
    world
        .screen()
        .descendants(Target::key(sheet.key.unwrap()))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn picked_verdict(world: &World) -> Option<String> {
    world
        .screen()
        .all(Target::role("radio-button"))
        .into_iter()
        .find(|n| n.is("checked"))
        .and_then(|n| n.name)
}

#[test]
fn a_review_skipped_for_an_approval_says_it_did_not_go_and_keeps_the_notes_and_the_verdict() {
    let (mut world, kept, _) = open_marking(presented());
    press(&mut world, Key::Right);
    type_key(&mut world, "c");
    write(&mut world, "Fine");
    let sends: Marks = Default::default();
    let tried = sends.clone();
    world.daemon.on("markdown_annotations_submit", move |r| {
        tried.lock().unwrap().push(r.clone());
        json!({
            "event": "markdown_annotations_submit_result",
            "request_id": r["request_id"],
            "document_uri": r["document_uri"],
            "source_kind": r["source_kind"],
            "success": false,
            "status": "skipped_pending_approval",
        })
    });

    send_key(&mut world);
    type_key(&mut world, "c");
    press(&mut world, Key::Enter);
    world.settle();

    assert_eq!(sends.lock().unwrap().len(), 1);
    let said = sheet_says(&world);
    assert!(
        said.iter()
            .any(|t| t.starts_with("Not sent — the session is waiting on an approval")),
        "the sheet stays and says why: {said:?}"
    );
    assert_eq!(picked_verdict(&world).as_deref(), Some("comment"));
    assert!(
        shown(&world).iter().any(|t| t.contains("Approval needed")),
        "the header says the send needs an approval: {:?}",
        shown(&world)
    );
    assert_eq!(kept.lock().unwrap().len(), 1, "the note stays in the draft");

    press(&mut world, Key::Escape);
    world.gesture(|app| app.click(Target::key("tile.m.document.send")));
    assert_eq!(
        picked_verdict(&world).as_deref(),
        Some("comment"),
        "the header's Send opens the sheet again with the verdict picked"
    );
    press(&mut world, Key::Enter);
    world.settle();
    let sent = sends.lock().unwrap().clone();
    assert_eq!(sent.len(), 2, "{sent:?}");
    assert_eq!(sent[1]["verdict"], "comment");
}

#[test]
fn the_send_sheet_lists_prose_comments_and_overall_notes_and_requests_changes_for_them() {
    let mut world = start_with_session(Some("s-a"));
    serve(&world);
    let (kept, sends, _) = keep_drafts(&world);
    let prose = attn_protocol::MarkdownAnnotation {
        id: "prose".into(),
        r#type: "comment".into(),
        text: Some("Say why".into()),
        anchor: Some(attn_protocol::MarkdownAnnotationAnchor {
            start_line: 3,
            end_line: 3,
            exact: "the reason".into(),
            ..attn_protocol::MarkdownAnnotationAnchor::default()
        }),
        ..attn_protocol::MarkdownAnnotation::default()
    };
    let overall = attn_protocol::MarkdownAnnotation {
        id: "overall".into(),
        r#type: "global".into(),
        text: Some("Good overall".into()),
        ..attn_protocol::MarkdownAnnotation::default()
    };
    kept.lock()
        .unwrap()
        .extend([prose, overall].map(|m| serde_json::to_value(m).unwrap()));
    deliver(&mut world, DOCUMENT, Some(presented()));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    world.advance(std::time::Duration::from_secs(1));
    world.settle();

    send_key(&mut world);
    let said = sheet_says(&world);
    assert!(said.iter().any(|t| t == "note line 3: Say why"), "{said:?}");
    assert!(
        said.iter().any(|t| t == "note the document: Good overall"),
        "{said:?}"
    );
    assert_eq!(picked_verdict(&world).as_deref(), Some("request changes"));

    press(&mut world, Key::Enter);
    world.settle();
    let sent = sends.lock().unwrap().clone();
    assert_eq!(sent.len(), 1, "{sent:?}");
    assert_eq!(sent[0]["verdict"], "request_changes");
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Send the review"))
            .is_none(),
        "the sheet closes once the review went"
    );
}

/// The key of the row named `name` among those of the element keyed `within`.
fn row_in(world: &World, within: &str, name: &str) -> Target {
    let key = world
        .screen()
        .descendants(Target::key(within))
        .into_iter()
        .find(|n| n.role == "row" && n.name.as_deref() == Some(name))
        .and_then(|n| n.key)
        .unwrap_or_else(|| panic!("no row {name:?} in {within}:\n{}", world.screen()));
    Target::key(key)
}

fn click(world: &mut World, target: Target) {
    world.gesture(|app| app.click(target));
}

/// Another tile takes the keyboard, so a click in the review has to take it back.
fn keyboard_elsewhere(world: &mut World) {
    click(world, Target::key("tile.a"));
}

#[test]
fn a_click_on_a_file_view_row_puts_the_cursor_there_and_the_keys_go_on_from_it() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    press(&mut world, Key::Right);
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    press(&mut world, Key::Enter);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 4: \tsum := a + b")
    );
    keyboard_elsewhere(&mut world);

    let row = row_in(&world, "tile.m.review.view", "added line 9: \treturn b - a");
    click(&mut world, row);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 9: \treturn b - a")
    );
    press(&mut world, Key::Down);
    assert_eq!(file_cursor(&world).as_deref(), Some("unchanged line 10: }"));
}

#[test]
fn a_click_on_a_row_of_the_walkthroughs_stage_moves_its_cursor_and_the_notes_under_it() {
    let (mut world, _) = open_served(
        DOCUMENT,
        ReviewBundle {
            frame: "walkthrough".into(),
            ..bundle()
        },
    );
    assert!(
        marks_strip(&world)
            .iter()
            .any(|t| t == "look here L4: The sum gets a name.")
    );
    keyboard_elsewhere(&mut world);

    let row = row_in(&world, "tile.m.review.view", "added line 5: \treturn sum");
    click(&mut world, row);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 5: \treturn sum")
    );
    assert!(
        marks_strip(&world)
            .iter()
            .any(|t| t == "agent L5: Then it is returned.")
    );
    press(&mut world, Key::Up);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 4: \tsum := a + b")
    );
}

#[test]
fn a_click_on_a_stop_in_the_walkthroughs_script_puts_its_file_on_the_stage() {
    let (mut world, _) = open_served(
        DOCUMENT,
        ReviewBundle {
            frame: "walkthrough".into(),
            ..bundle()
        },
    );
    assert_eq!(stage_stop(&world).as_deref(), Some("calc.go"));
    keyboard_elsewhere(&mut world);

    click(&mut world, Target::role("list-item").name("util.go L1–2"));
    assert_eq!(stage_stop(&world).as_deref(), Some("util.go"));
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 2: func helper() {}")
    );
    press(&mut world, Key::Left);
    assert_eq!(stage_stop(&world).as_deref(), Some("calc.go"));
}

#[test]
fn a_click_on_a_search_result_opens_it_as_enter_does() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    serve_search(&world);
    type_key(&mut world, "/");
    search(&mut world, "calc");
    assert_eq!(options(&world), ["calc.go file", "return b - a calc.go:9"]);

    click(&mut world, Target::key("review.search.option.1"));
    assert!(world.screen().find(Target::key("review.search")).is_none());
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 9: \treturn b - a")
    );
}

#[test]
fn a_click_in_the_brief_moves_the_cursor_and_acts_on_the_facts_a_changed_file_and_a_frame_tab() {
    let mut world = open();
    let (_, warmed) = (serve(&world), file_reads(&world).len());
    keyboard_elsewhere(&mut world);

    let stop = stop_key(&world, "calc.go");
    let row = row_in(&world, &stop, "added line 5: \treturn sum");
    click(&mut world, row);
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("added line 5: \treturn sum")
    );
    press(&mut world, Key::Up);
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("added line 4: \tsum := a + b")
    );

    click(&mut world, Target::key("tile.m.review.facts"));
    assert!(
        world
            .screen()
            .find(Target::key("tile.m.review.file.1"))
            .is_some()
    );
    click(&mut world, Target::key("tile.m.review.file.1"));
    assert_eq!(file_reads(&world)[warmed..], ["util.go"]);
    let view = file_view(&world).expect("the file view shows");
    assert!(view.iter().any(|t| t == "util.go"), "{view:?}");
    click(&mut world, Target::role("button").name("close esc"));
    assert!(file_view(&world).is_none());
    click(&mut world, Target::key("tile.m.review.file.1"));
    assert!(file_view(&world).is_some());
    click(&mut world, Target::key("tile.m.review.view.back"));
    assert!(
        file_view(&world).is_none(),
        "back from the first file leaves the file view"
    );

    click(&mut world, Target::role("tab").name("2 Walkthrough"));
    assert_eq!(stage_stop(&world).as_deref(), Some("calc.go"));
}

#[test]
fn a_drag_across_a_file_view_row_selects_its_text_and_leaves_the_cursor() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    press(&mut world, Key::Right);
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    press(&mut world, Key::Enter);
    let row = row_in(&world, "tile.m.review.view", "added line 9: \treturn b - a");
    let [x, y, w, h] = world.screen().bounds(row).expect("the row is laid out");
    let (from, to) = ((x + w * 0.3, y + h / 2.0), (x + w * 0.6, y + h / 2.0));
    world.gesture(|app| {
        app.drag_from(from, to);
        app.release(to);
    });
    world.settle();
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 4: \tsum := a + b")
    );
    assert!(
        world
            .runtime()
            .selected_text()
            .is_some_and(|s| !s.is_empty()),
        "the drag selected text"
    );
}

#[test]
fn a_click_on_a_verdict_in_the_send_sheet_picks_it_as_its_key_does() {
    let (mut world, _, sends) = open_marking(presented());
    press(&mut world, Key::Down);
    send_key(&mut world);
    click(&mut world, Target::key("review.send.verdict.c"));
    let picked = world
        .screen()
        .all(Target::role("radio-button"))
        .into_iter()
        .find(|n| n.is("checked"))
        .and_then(|n| n.name);
    assert_eq!(picked.as_deref(), Some("comment"));
    press(&mut world, Key::Enter);
    let sent = sends.lock().unwrap().clone();
    assert_eq!(
        sent.last().map(|s| s["verdict"].clone()),
        Some(json!("comment"))
    );
}

#[test]
fn a_click_on_before_takes_the_keyboard_back_from_another_tile() {
    let mut world = open_map(map_bundle("map"));
    keyboard_elsewhere(&mut world);
    click(&mut world, Target::role("tab").name("Before"));
    assert_eq!(side_tabs(&world), ["Before *", "After"]);
    type_key(&mut world, "b");
    assert_eq!(side_tabs(&world), ["Before", "After *"]);
}

/// Applies `review_edit` to the served calc.go as the daemon does: the lines now `from` to `to`
/// take the text, unless the file's hash moved on since the edit read it.
fn serve_edits(world: &World, calc: &Files) -> Marks {
    let asked: Marks = Default::default();
    let (held, seen) = (calc.clone(), asked.clone());
    world.daemon.on("review_edit", move |r| {
        seen.lock().unwrap().push(r.clone());
        let mut rows = held.lock().unwrap();
        let hash = if rows.len() == calc_file().len() && rows[3].text == calc_file()[3].text {
            "h1".to_owned()
        } else {
            format!("h1-{}", rows.len())
        };
        if r["base_hash"] != json!(hash) {
            return json!({"event": "review_edit_result", "success": true, "conflict": true, "current_hash": hash});
        }
        let (from, to) = (r["from"].as_i64().unwrap(), r["to"].as_i64().unwrap());
        let text = r["text"].as_str().unwrap_or_default();
        let at = rows.iter().position(|x| x.now == Some(from)).unwrap();
        let end = rows.iter().position(|x| x.now == Some(to)).unwrap();
        let taken: Vec<ReviewRow> = text
            .split('\n')
            .zip(from..)
            .map(|(t, n)| line("+", None, Some(n), t))
            .collect();
        let grew = taken.len() as i64 - (to - from + 1);
        let mut after: Vec<ReviewRow> = rows[end + 1..].to_vec();
        for r in &mut after {
            r.now = r.now.map(|n| n + grew);
        }
        rows.truncate(at);
        rows.extend(taken);
        rows.extend(after);
        json!({"event": "review_edit_result", "success": true, "conflict": false, "hash": "edited"})
    });
    asked
}

fn edit_box(world: &World) -> Option<Vec<String>> {
    world
        .screen()
        .find(Target::role("dialog").name("Edit the file"))?;
    Some(
        world
            .screen()
            .descendants(Target::key("review.edit"))
            .into_iter()
            .filter_map(|n| n.name.or(n.value))
            .collect(),
    )
}

fn open_editing() -> (World, Files, Marks, Marks) {
    let mut world = start_with_session(Some("s-a"));
    let calc = serve(&world);
    let (kept, _, _) = keep_drafts(&world);
    let edits = serve_edits(&world, &calc);
    deliver(&mut world, DOCUMENT, Some(presented()));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    press(&mut world, Key::Right);
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    press(&mut world, Key::Enter);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 4: \tsum := a + b")
    );
    (world, calc, kept, edits)
}

fn append(world: &mut World, text: &str) {
    world.gesture(|app| app.type_text(text));
}

#[test]
fn edits_the_line_at_the_cursor_saves_it_against_the_hash_it_read_and_lists_it_under_the_line() {
    let (mut world, calc, kept, edits) = open_editing();
    type_key(&mut world, "i");
    let shown = edit_box(&world).expect("the edit box opens");
    assert!(
        shown.iter().any(|t| t == "Editing calc.go: line 4"),
        "{shown:?}"
    );
    append(&mut world, " // named");
    press(&mut world, Key::Enter);
    world.advance(std::time::Duration::from_secs(1));
    world.settle();

    let asked = edits.lock().unwrap().clone();
    assert_eq!(asked.len(), 1);
    assert_eq!(
        (
            asked[0]["base_hash"].clone(),
            asked[0]["from"].clone(),
            asked[0]["to"].clone(),
            asked[0]["text"].clone()
        ),
        (
            json!("h1"),
            json!(4),
            json!(4),
            json!("\tsum := a + b // named")
        )
    );
    assert_eq!(calc.lock().unwrap()[4].text, "\tsum := a + b // named");
    assert!(edit_box(&world).is_none());
    let mark = kept
        .lock()
        .unwrap()
        .iter()
        .find(|m| m["type"] == "edit")
        .cloned()
        .unwrap();
    assert_eq!(
        (mark["edit_was"].clone(), mark["edit_now"].clone()),
        (json!("\tsum := a + b"), json!("\tsum := a + b // named"))
    );
    let view = file_view(&world).unwrap();
    assert!(
        view.iter()
            .any(|t| t == "added line 4: \tsum := a + b // named"),
        "{view:?}"
    );
    assert!(
        marks_strip(&world)
            .iter()
            .any(|t| t == "you edited L4: it was \tsum := a + b"),
        "{:?}",
        marks_strip(&world)
    );
}

#[test]
fn refuses_an_edit_of_a_file_that_changed_underneath_shows_why_and_records_nothing() {
    let (mut world, calc, kept, _) = open_editing();
    type_key(&mut world, "i");
    calc.lock().unwrap()[3].text = "func Add(x, y int) int {".into();
    append(&mut world, " // named");
    press(&mut world, Key::Enter);
    let shown = edit_box(&world).expect("the box stays open");
    assert!(
        shown
            .iter()
            .any(|t| t
                .starts_with("calc.go changed since this edit opened, so nothing was written.")),
        "{shown:?}"
    );
    assert!(kept.lock().unwrap().iter().all(|m| m["type"] != "edit"));
    press(&mut world, Key::Escape);
    assert!(edit_box(&world).is_none());
}

#[test]
fn undoes_an_edit_with_u_writing_the_old_text_back_and_dropping_it_from_the_review() {
    let (mut world, calc, kept, edits) = open_editing();
    type_key(&mut world, "i");
    append(&mut world, " // named");
    press(&mut world, Key::Enter);
    world.advance(std::time::Duration::from_secs(1));
    world.settle();
    assert_eq!(calc.lock().unwrap()[4].text, "\tsum := a + b // named");

    type_key(&mut world, "u");
    world.advance(std::time::Duration::from_secs(1));
    world.settle();
    let asked = edits.lock().unwrap().clone();
    assert_eq!(
        (
            asked[1]["from"].clone(),
            asked[1]["to"].clone(),
            asked[1]["text"].clone()
        ),
        (json!(4), json!(4), json!("\tsum := a + b"))
    );
    assert_eq!(calc.lock().unwrap()[4].text, "\tsum := a + b");
    assert!(kept.lock().unwrap().iter().all(|m| m["type"] != "edit"));
}

#[test]
fn says_why_i_does_nothing_on_a_removed_line() {
    let (mut world, _, _, edits) = open_editing();
    press(&mut world, Key::Up);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("removed line 4: \treturn a + b")
    );
    type_key(&mut world, "i");
    assert!(edit_box(&world).is_none());
    let notice = world
        .screen()
        .find(Target::role("status"))
        .and_then(|n| n.name)
        .unwrap_or_default();
    let shown = shown(&world);
    assert!(
        notice.contains("This line was removed")
            || shown.iter().any(|t| t.contains("This line was removed")),
        "{shown:?}"
    );
    assert!(edits.lock().unwrap().is_empty());
}

#[test]
fn lists_edits_apart_from_notes_in_the_send() {
    let (mut world, _, _, _) = open_editing();
    type_key(&mut world, "c");
    write(&mut world, "Fine");
    type_key(&mut world, "i");
    append(&mut world, " // named");
    press(&mut world, Key::Enter);
    world.advance(std::time::Duration::from_secs(1));
    world.settle();
    send_key(&mut world);
    let edits: Vec<String> = world
        .screen()
        .descendants(Target::role("list").name("Your edits, saved to the files"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(
        edits.iter().any(|t| t.starts_with("edit 1 ")
            && t.ends_with("it was \tsum := a + b · it is now \tsum := a + b // named")),
        "{edits:?}"
    );
    let marks: Vec<String> = world
        .screen()
        .descendants(Target::role("list").name("Marks"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(marks.iter().any(|t| t.starts_with("note ")), "{marks:?}");
    assert!(
        !marks
            .iter()
            .any(|t| t.starts_with("note ") && t.contains("named")),
        "{marks:?}"
    );
}

#[test]
fn a_mark_and_an_edit_leave_the_review_from_their_remove_button_and_the_file_stays() {
    let (mut world, calc, kept, edits) = open_editing();
    type_key(&mut world, "c");
    write(&mut world, "Fine");
    type_key(&mut world, "i");
    append(&mut world, " // named");
    press(&mut world, Key::Enter);
    world.advance(std::time::Duration::from_secs(1));
    world.settle();
    let ids: Vec<String> = kept
        .lock()
        .unwrap()
        .iter()
        .map(|m| m["id"].as_str().unwrap().to_owned())
        .collect();
    assert_eq!(ids.len(), 2);
    for id in &ids {
        world.gesture(|app| app.click(Target::key(format!("review.mark.{id}.remove"))));
    }
    world.advance(std::time::Duration::from_secs(1));
    world.settle();
    assert!(kept.lock().unwrap().is_empty());
    assert_eq!(
        edits.lock().unwrap().len(),
        1,
        "dropping an edit writes nothing"
    );
    assert_eq!(calc.lock().unwrap()[4].text, "\tsum := a + b // named");
}

#[test]
fn a_wide_table_in_a_review_wraps_its_cells_inside_the_document() {
    let cell = |n: usize| {
        format!(
            "cell {n} holds a sentence long enough that three of them cannot share one line of the page"
        )
    };
    let table = format!(
        "\n| One | Two | Three |\n| --- | --- | --- |\n| {} | {} | {} |\n",
        cell(1),
        cell(2),
        cell(3)
    );
    let mut world = start();
    deliver(&mut world, &format!("{DOCUMENT}{table}"), Some(bundle()));
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

#[test]
fn says_why_i_edits_nothing_on_prose() {
    let (mut world, _, _) = open_marking(presented());
    press(&mut world, Key::Down);
    type_key(&mut world, "i");
    assert!(edit_box(&world).is_none());
    let screen = format!("{}", world.screen());
    assert!(
        screen.contains("Put the cursor on a line of code to edit it."),
        "{screen}"
    );
}

fn place(path: &str, line: i64, character: i64, text: &str) -> serde_json::Value {
    json!({
        "path": path, "outside": path.starts_with('/'), "line": line, "character": character,
        "end_line": line, "end_character": character + 3, "text": text,
    })
}

fn nav_answer(locations: Vec<serde_json::Value>) -> serde_json::Value {
    json!({"event": "code_nav_result", "success": true, "language": "go", "locations": locations})
}

/// The labels a row draws while a name is being picked on it.
fn labels_on(world: &World) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key("tile.m"))
        .into_iter()
        .filter_map(|n| n.description)
        .find_map(|d| d.strip_prefix("labels: ").map(str::to_owned))
        .map(|d| {
            d.split(", ")
                .map(|l| l.split(' ').next().unwrap().to_owned())
                .collect()
        })
        .unwrap_or_default()
}

fn asked_rows(world: &World) -> usize {
    world
        .screen()
        .descendants(Target::key("tile.m"))
        .into_iter()
        .filter(|n| n.description.as_deref() == Some("asked, waiting for the answer"))
        .count()
}

fn nav_note(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("review.nav.note"))
        .and_then(|n| n.name)
}

fn offer(world: &World) -> Option<String> {
    world.screen().find(Target::key("review.nav.offer"))?;
    world
        .screen()
        .descendants(Target::key("review.nav.offer"))
        .into_iter()
        .find_map(|n| n.name)
}

fn to_return_sum(world: &mut World) {
    press(world, Key::Right);
    press(world, Key::Down);
    press(world, Key::Down);
    press(world, Key::Down);
    assert_eq!(
        cursor(world).name.as_deref(),
        Some("added line 5: \treturn sum")
    );
}

#[test]
fn labels_the_names_on_the_line_with_f_and_goes_to_the_definition_of_the_one_whose_label_is_typed()
{
    let (mut world, _) = open_served(DOCUMENT, bundle());
    world.daemon.on("code_nav", |_| {
        nav_answer(vec![place("util.go", 2, 5, "func helper() {}")])
    });
    press(&mut world, Key::Right);
    type_key(&mut world, "f");
    assert_eq!(labels_on(&world), ["a", "s", "d", "f", "g", "h"]);
    assert!(world.daemon.sent_of("code_nav").is_empty());

    type_key(&mut world, "s");
    let asked = world.daemon.sent_of("code_nav");
    assert_eq!(
        (
            asked[0]["document"].clone(),
            asked[0]["path"].clone(),
            asked[0]["line"].clone(),
            asked[0]["character"].clone(),
            asked[0]["kind"].clone()
        ),
        (
            json!(DOC),
            json!("calc.go"),
            json!(3),
            json!(5),
            json!("definition")
        )
    );
    assert!(labels_on(&world).is_empty());
    assert!(file_view(&world).unwrap().iter().any(|t| t == "util.go"));
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 2: func helper() {}")
    );
}

#[test]
fn lists_the_uses_of_a_name_with_f_on_its_own_definition_and_opens_the_one_picked() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    world.daemon.on("code_nav", |r| {
        if r["kind"] == "definition" {
            nav_answer(vec![place("calc.go", 3, 5, "func Add(a, b int) int {")])
        } else {
            nav_answer(
                (0..30)
                    .map(|i| place(&format!("pkg{i}.go"), i + 1, 2, "\tAdd(1, 2)"))
                    .collect(),
            )
        }
    });
    press(&mut world, Key::Right);
    type_key(&mut world, "f");
    type_key(&mut world, "s");
    let kinds: Vec<_> = world
        .daemon
        .sent_of("code_nav")
        .iter()
        .map(|r| r["kind"].clone())
        .collect();
    assert_eq!(kinds, [json!("definition"), json!("references")]);
    let list = world
        .screen()
        .get(Target::role("dialog").name("Uses of Add"));
    let said: Vec<String> = world
        .screen()
        .descendants(Target::key(list.key.unwrap()))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(said.iter().any(|t| t == "30"), "{said:?}");
    assert!(file_view(&world).is_none());

    press(&mut world, Key::Down);
    press(&mut world, Key::Enter);
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Uses of Add"))
            .is_none()
    );
    assert_eq!(
        file_reads(&world).last().map(String::as_str),
        Some("pkg1.go")
    );
}

#[test]
fn follows_an_answer_that_arrives_in_time_with_the_asked_line_marked_until_it_does() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    world.daemon.on("code_nav", |_| ());
    to_return_sum(&mut world);
    type_key(&mut world, "f");
    type_key(&mut world, "s");
    assert_eq!(asked_rows(&world), 1);
    assert_eq!(
        nav_note(&world).as_deref(),
        Some("Finding the definition of sum…")
    );
    world.advance(std::time::Duration::from_secs(2));
    let asked = world.daemon.sent_of("code_nav")[0].clone();
    world.daemon.reply_to(
        &asked,
        nav_answer(vec![place("calc.go", 4, 1, "\tsum := a + b")]),
    );
    world.settle();
    assert!(offer(&world).is_none());
    assert_eq!(asked_rows(&world), 0);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 4: \tsum := a + b")
    );
}

#[test]
fn offers_an_answer_that_arrived_late_once_the_cursor_moved_and_enter_opens_it() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    world.daemon.on("code_nav", |_| ());
    to_return_sum(&mut world);
    type_key(&mut world, "f");
    type_key(&mut world, "s");
    press(&mut world, Key::Up);
    world.advance(std::time::Duration::from_secs(2));
    let asked = world.daemon.sent_of("code_nav")[0].clone();
    world.daemon.reply_to(
        &asked,
        nav_answer(vec![place("calc.go", 4, 1, "\tsum := a + b")]),
    );
    world.settle();
    assert!(file_view(&world).is_none());
    assert_eq!(asked_rows(&world), 0);
    assert_eq!(
        offer(&world).as_deref(),
        Some("sum is in calc.go:4 · ↵ open")
    );

    press(&mut world, Key::Down);
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("added line 5: \treturn sum")
    );
    assert!(offer(&world).is_some());
    press(&mut world, Key::Enter);
    assert!(offer(&world).is_none());
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 4: \tsum := a + b")
    );
}

#[test]
fn drops_an_offer_on_escape_and_twenty_seconds_after_it_shows() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    world.daemon.on("code_nav", |_| ());
    let late = |world: &mut World| {
        type_key(world, "f");
        type_key(world, "s");
        press(world, Key::Up);
        world.advance(std::time::Duration::from_secs(2));
        let asked = world.daemon.sent_of("code_nav").last().cloned().unwrap();
        world.daemon.reply_to(
            &asked,
            nav_answer(vec![place("calc.go", 4, 1, "\tsum := a + b")]),
        );
        world.settle();
        press(world, Key::Down);
        assert!(offer(world).is_some());
    };
    to_return_sum(&mut world);
    late(&mut world);
    press(&mut world, Key::Escape);
    assert!(offer(&world).is_none());

    late(&mut world);
    world.advance(std::time::Duration::from_secs(21));
    assert!(offer(&world).is_none());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_answer_to_an_older_question_leaves_the_newer_one_waiting() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    world.daemon.on("code_nav", |_| ());
    to_return_sum(&mut world);
    type_key(&mut world, "f");
    type_key(&mut world, "s");
    type_key(&mut world, "f");
    type_key(&mut world, "s");
    let asked = world.daemon.sent_of("code_nav");
    assert_eq!(asked.len(), 2);
    world.daemon.reply_to(
        &asked[0],
        nav_answer(vec![place("calc.go", 4, 1, "\tsum := a + b")]),
    );
    world.settle();
    assert!(file_view(&world).is_none());
    assert_eq!(asked_rows(&world), 1);
    assert_eq!(
        nav_note(&world).as_deref(),
        Some("Finding the definition of sum…")
    );

    world.daemon.reply_to(
        &asked[1],
        nav_answer(vec![place("calc.go", 4, 1, "\tsum := a + b")]),
    );
    world.settle();
    assert_eq!(asked_rows(&world), 0);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 4: \tsum := a + b")
    );
}

#[test]
fn a_click_takes_or_drops_an_offer_and_opens_or_closes_a_list_of_places() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    world.daemon.on("code_nav", |_| ());
    let late = |world: &mut World, answer: serde_json::Value| {
        type_key(world, "f");
        type_key(world, "s");
        press(world, Key::Up);
        world.advance(std::time::Duration::from_secs(2));
        let asked = world.daemon.sent_of("code_nav").last().cloned().unwrap();
        world.daemon.reply_to(&asked, answer);
        world.settle();
        press(world, Key::Down);
        assert!(offer(world).is_some());
    };
    let uses = || {
        nav_answer(vec![
            place("pkg0.go", 1, 2, "\tsum++"),
            place("pkg1.go", 2, 2, "\tsum--"),
        ])
    };
    let list = || Target::role("dialog").name("Definition of sum");
    to_return_sum(&mut world);

    late(&mut world, uses());
    click(&mut world, Target::key("review.nav.offer.drop"));
    assert!(offer(&world).is_none());

    late(&mut world, uses());
    click(&mut world, Target::key("review.nav.offer"));
    assert!(offer(&world).is_none());
    assert!(world.screen().find(list()).is_some());
    click(&mut world, Target::key("review.nav.panel.close"));
    assert!(world.screen().find(list()).is_none());

    late(&mut world, uses());
    click(&mut world, Target::key("review.nav.offer"));
    click(&mut world, Target::key("review.nav.place.1"));
    assert!(world.screen().find(list()).is_none());
    assert_eq!(
        file_reads(&world).last().map(String::as_str),
        Some("pkg1.go")
    );
}

#[test]
fn shows_the_type_with_h_and_opens_a_definition_outside_the_repository_read_only() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    world.daemon.on("code_nav", |r| {
        if r["kind"] == "hover" {
            json!({"event": "code_nav_result", "success": true, "text": "var sum int"})
        } else {
            nav_answer(vec![place(
                "/usr/lib/go/src/fmt/print.go",
                12,
                5,
                "func Sum() {}",
            )])
        }
    });
    to_return_sum(&mut world);
    type_key(&mut world, "h");
    type_key(&mut world, "s");
    let shown = world
        .screen()
        .get(Target::role("dialog").name("Type of sum"));
    let said: Vec<String> = world
        .screen()
        .descendants(Target::key(shown.key.unwrap()))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(said.iter().any(|t| t == "var sum int"), "{said:?}");
    press(&mut world, Key::Escape);

    type_key(&mut world, "f");
    type_key(&mut world, "s");
    let view = file_view(&world).unwrap();
    assert!(
        view.iter()
            .any(|t| t == "outside the repository · read-only"),
        "{view:?}"
    );
    type_key(&mut world, "c");
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Write a note"))
            .is_none()
    );
}

#[test]
fn says_why_a_question_got_no_answer_and_asks_nothing_about_a_removed_line_or_prose() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    world.daemon.on(
        "code_nav",
        |_| json!({"event": "code_nav_result", "success": true, "locations": []}),
    );
    press(&mut world, Key::Down);
    type_key(&mut world, "f");
    assert_eq!(
        nav_note(&world).as_deref(),
        Some("Put the cursor on a line of code to ask about it.")
    );
    press(&mut world, Key::Right);
    press(&mut world, Key::Down);
    type_key(&mut world, "f");
    assert_eq!(
        nav_note(&world).as_deref(),
        Some("This line was removed; ask about the code as it is now.")
    );
    assert!(world.daemon.sent_of("code_nav").is_empty());

    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    type_key(&mut world, "f");
    type_key(&mut world, "s");
    assert_eq!(
        nav_note(&world).as_deref(),
        Some("The language server knows no definition of sum.")
    );
}

fn server(language: &str, state: &str) -> attn_protocol::ReviewNavigation {
    attn_protocol::ReviewNavigation {
        language: language.into(),
        file: format!("main.{language}"),
        state: state.into(),
        ..Default::default()
    }
}

fn with_servers(navigation: Vec<attn_protocol::ReviewNavigation>) -> ReviewBundle {
    ReviewBundle {
        navigation,
        ..bundle()
    }
}

/// What the key line says about the language servers, entry by entry.
fn servers(world: &World) -> Vec<String> {
    if world
        .screen()
        .find(Target::key("tile.m.review.servers"))
        .is_none()
    {
        return Vec::new();
    }
    world
        .screen()
        .descendants(Target::key("tile.m.review.servers"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

#[test]
fn says_each_language_server_state_and_counts_the_seconds_one_has_been_starting() {
    let mut world = start();
    deliver(
        &mut world,
        DOCUMENT,
        Some(with_servers(vec![
            attn_protocol::ReviewNavigation {
                started_at: Some(super::queue::ago(0)),
                detail: Some("indexing 40%".into()),
                ..server("go", "starting")
            },
            server("rust", "ready"),
            attn_protocol::ReviewNavigation {
                detail: Some("gopls exited".into()),
                ..server("ts", "failed")
            },
        ])),
    );
    assert_eq!(
        servers(&world),
        [
            "go loading 0s: indexing 40%",
            "rust ready",
            "ts failed: gopls exited",
            "set up",
            "not here"
        ]
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("tile.m.review.server.ts"))
            .description
            .as_deref(),
        Some("gopls exited")
    );
    world.advance(std::time::Duration::from_secs(3));
    assert_eq!(servers(&world)[0], "go loading 3s: indexing 40%");

    deliver(
        &mut world,
        DOCUMENT,
        Some(with_servers(vec![server("go", "ready")])),
    );
    assert_eq!(servers(&world), ["go ready"]);
    world.advance(std::time::Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn s_starts_an_agent_that_sets_up_a_missing_server_and_says_why_it_could_not() {
    let mut world = start();
    let answers = std::sync::Arc::new(std::sync::Mutex::new(vec![
        json!({"event": "code_nav_setup_result", "success": false, "error": "no agent fits"}),
        json!({"event": "code_nav_setup_result", "success": true, "session_id": "s-setup"}),
    ]));
    world
        .daemon
        .on("code_nav_setup", move |_| answers.lock().unwrap().remove(0));
    deliver(
        &mut world,
        DOCUMENT,
        Some(with_servers(vec![
            server("go", "ready"),
            attn_protocol::ReviewNavigation {
                detail: Some("pyright not found".into()),
                ..server("python", "missing")
            },
        ])),
    );
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));

    type_key(&mut world, "s");
    let asked = world.daemon.sent_of("code_nav_setup");
    assert_eq!(
        (
            asked[0]["desktop_id"].clone(),
            asked[0]["document"].clone(),
            asked[0]["repository"].clone(),
            asked[0]["language"].clone(),
            asked[0]["file"].clone(),
            asked[0]["state"].clone(),
            asked[0]["detail"].clone(),
        ),
        (
            json!(DESKTOP),
            json!(DOC),
            json!("/repo"),
            json!("python"),
            json!("main.python"),
            json!("missing"),
            json!("pyright not found"),
        )
    );
    assert_eq!(
        nav_note(&world).as_deref(),
        Some("Could not start setup: no agent fits")
    );

    click(&mut world, Target::key("tile.m.review.setup.start"));
    assert_eq!(world.daemon.sent_of("code_nav_setup").len(), 2);
    assert_eq!(nav_note(&world), None);
}

#[test]
fn shows_the_agent_setting_up_a_server_and_a_click_opens_it() {
    let mut world = start();
    let mut setup = server("python", "missing");
    setup.setup_session_id = Some("s-setup".into());
    deliver(&mut world, DOCUMENT, Some(with_servers(vec![setup])));
    world.daemon.emit(attn_protocol::SessionsUpdatedMessage {
        sessions: Some(vec![
            Session {
                id: "s-a".into(),
                label: "Agent a".into(),
                directory: "/repo".into(),
                profile_id: "work".into(),
                ..Session::default()
            },
            Session {
                id: "s-setup".into(),
                label: "Python setup".into(),
                directory: "/repo".into(),
                profile_id: "work".into(),
                state: "working".into(),
                ..Session::default()
            },
        ]),
        ..attn_protocol::SessionsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(servers(&world), ["python setting up in Python setup"]);

    click(&mut world, Target::key("tile.m.review.server.python"));
    let shown = world.daemon.sent_of("desktop_show_session");
    assert_eq!(shown.last().unwrap()["session_id"], json!("s-setup"));
}

#[test]
fn x_stops_offering_setup_in_the_repository_and_s_still_sets_up() {
    let mut world = start();
    world.daemon.on(
        "code_nav_setup",
        |_| json!({"event": "code_nav_setup_result", "success": true}),
    );
    deliver(
        &mut world,
        DOCUMENT,
        Some(with_servers(vec![server("python", "missing")])),
    );
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));

    type_key(&mut world, "x");
    let set = world.daemon.sent_of("set_setting");
    assert_eq!(
        (set[0]["key"].clone(), set[0]["value"].clone()),
        (json!("code_review_setup_offer_local_/repo"), json!("off"))
    );
    assert_eq!(
        nav_note(&world).as_deref(),
        Some("Setup is no longer offered in this repository; s still sets up.")
    );
    world.daemon.emit(attn_protocol::SettingsUpdatedMessage {
        settings: Some(std::collections::BTreeMap::from([(
            "code_review_setup_offer_local_/repo".to_owned(),
            "off".to_owned(),
        )])),
        ..attn_protocol::SettingsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(servers(&world), ["python no server", "offer setup"]);

    type_key(&mut world, "s");
    assert_eq!(world.daemon.sent_of("code_nav_setup").len(), 1);

    click(&mut world, Target::key("tile.m.review.setup.offer"));
    let set = world.daemon.sent_of("set_setting");
    assert_eq!(set[1]["value"], json!(""));
    assert_eq!(
        nav_note(&world).as_deref(),
        Some("Setup is offered again in this repository.")
    );
}

#[test]
fn offers_setup_again_once_the_setup_agent_is_gone_or_waits_without_a_server() {
    let mut world = start();
    let mut gone = server("go", "missing");
    gone.setup_session_id = Some("s-closed".into());
    let mut waits = server("python", "missing");
    waits.setup_session_id = Some("s-a".into());
    deliver(&mut world, DOCUMENT, Some(with_servers(vec![gone, waits])));
    assert_eq!(
        servers(&world),
        ["go no server", "set up", "not here", "python no server"]
    );
}
