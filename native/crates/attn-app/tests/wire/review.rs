use attn_protocol::{
    Desktop, DesktopPane, DesktopTileContentMessage, InitialStateMessage, Profile, ReviewBundle,
    ReviewFile, ReviewFileResultMessage, ReviewFlow, ReviewFlowGroup, ReviewFlowMessage,
    ReviewFlowParticipant, ReviewNotShown, ReviewNote, ReviewRow, ReviewStop, Session,
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

    assert_eq!(frame_tabs(&world), ["1 Brief", "3 Map *"]);
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
    assert_eq!(frame_tabs(&world), ["1 Brief *", "3 Map"]);
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
    assert!(
        world
            .screen()
            .find(Target::role("tab-list").name("Frames"))
            .is_none()
    );
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
