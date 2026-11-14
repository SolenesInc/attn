use attn_protocol::{
    Desktop, DesktopPane, DesktopTileContentMessage, InitialStateMessage, Profile, ReviewBundle,
    ReviewFile, ReviewFlow, ReviewFlowGroup, ReviewFlowMessage, ReviewFlowParticipant,
    ReviewNotShown, ReviewNote, ReviewRow, ReviewStop, Session,
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
