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

/// A markdown tile `id` showing `path`, bound to `session` when given.
fn document_tile(id: &str, path: &str, session: Option<&str>) -> serde_json::Value {
    let mut document = json!({
        "type": "tile",
        "tile_id": id,
        "tile_kind": "markdown",
        "tile_params": json!({ "path": path }).to_string(),
    });
    if let Some(session) = session {
        document["tile_session_id"] = json!(session);
    }
    document
}

/// The fixture's desktop, with the document tile bound to `session` when given.
fn start_with_session(session: Option<&str>) -> World {
    start_with_tiles(vec![document_tile("m", DOC, session)])
}

/// The fixture's desktop: agent `a`, then `tiles`.
fn start_with_tiles(tiles: Vec<serde_json::Value>) -> World {
    let tree = row(std::iter::once(pane("a")).chain(tiles).collect::<Vec<_>>());
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
    let (mut world, _) = open_served(DOCUMENT, bundle());
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
    press(&mut world, Key::Enter);
    assert_eq!(
        file_reads(&world).last().map(String::as_str),
        Some("util.go")
    );
    let view = file_view(&world).expect("util.go opens");
    assert!(
        view.iter().any(|t| t == "added line 2: func helper() {}"),
        "{view:?}"
    );
    press(&mut world, Key::Escape);
    assert!(file_view(&world).is_none());

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
    let (mut world, _) = open_served(DOCUMENT, bundle());
    press(&mut world, Key::Down);
    let before = cursor(&world).key;
    world.gesture(|app| app.click(Target::key("tile.a")));
    press(&mut world, Key::Down);
    assert_eq!(cursor(&world).key, before);

    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    press(&mut world, Key::Right);
    press(&mut world, Key::Enter);
    assert!(file_view(&world).is_some());
    world.gesture(|app| app.click(Target::key("tile.a")));
    press(&mut world, Key::Escape);
    assert!(file_view(&world).is_some());
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    press(&mut world, Key::Escape);
    assert!(file_view(&world).is_none());
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
    assert!(
        keys.iter().any(|k| k == "f r h definition, uses, type"),
        "{keys:?}"
    );

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
    let [rx, ry, rw, rh] = world.rect("tile.m.review");
    let [sx, sy, sw, _] = world.rect("tile.m.review.sheet");
    assert_eq!(sw, 560.0_f64.min(rw * 0.9), "the sheet floats at its width");
    assert!(
        (sx + sw / 2.0 - (rx + rw / 2.0)).abs() <= 0.5,
        "the sheet is centered across the reader: {sx} {sw} {rx} {rw}"
    );
    assert!(
        (sy - (ry + rh * 0.1)).abs() <= 0.5,
        "the sheet starts a tenth of the way down: {sy} {ry} {rh}"
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
    let mut world = start();
    serve(&world);
    deliver(&mut world, &map_document(), Some(map_bundle("map")));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));

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

    press(&mut world, Key::Enter);
    let view = file_view(&world).expect("the arrow's stop file opens");
    assert!(view.iter().any(|t| t == "util.go"), "{view:?}");
    press(&mut world, Key::Escape);
    assert!(file_view(&world).is_none());

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
fn draws_a_sections_divider_above_a_block_that_opens_on_the_sections_first_arrow() {
    let group =
        |line: i64, kind: &str, label: &str, (first, last): (i64, i64), block| ReviewFlowGroup {
            line,
            kind: kind.into(),
            label: label.into(),
            first_message: first,
            last_message: last,
            block,
        };
    let mut review = map_bundle("map");
    let after = &mut review.flows[1];
    after
        .messages
        .insert(2, message(46, "calc", "util", "helper again", None));
    after.groups = vec![
        group(100, "alt", "the sum is new", (45, 46), None),
        group(101, "else", "the sum is known", (46, 46), Some(100)),
        group(102, "loop", "each retry", (46, 46), None),
    ];
    let world = open_map(review);
    let top = |title: &str| {
        world
            .screen()
            .bounds(Target::role("label").name(title))
            .unwrap_or_else(|| panic!("{title:?} is drawn:\n{}", world.screen()))[1]
    };
    let (alt, divider, inner) = (
        top("alt  the sum is new"),
        top("else  the sum is known"),
        top("loop  each retry"),
    );
    assert!(alt < divider, "alt at {alt}, else at {divider}");
    assert!(divider < inner, "else at {divider}, loop at {inner}");
}

#[test]
fn keeps_the_map_on_its_arrow_and_step_when_the_document_changes_above_it() {
    let mut world = open_map(map_bundle("map"));
    press(&mut world, Key::Right);
    assert_eq!(arrow_line(&world)[0], "Arrow 2 of 2 · after the change");

    let content = map_document().replace(
        "Add used to return its sum directly.",
        "Add used to return its sum directly.\n\nIt reads better with a name.",
    );
    let mut moved = map_bundle("map");
    for stop in &mut moved.stops {
        stop.heading_line += 2;
        for note in &mut stop.notes {
            note.line += 2;
        }
    }
    moved.not_shown[0].line += 2;
    for flow in &mut moved.flows {
        flow.line += 2;
        for m in &mut flow.messages {
            m.line += 2;
            m.stop_line = m.stop_line.map(|l| l + 2);
        }
    }
    deliver(&mut world, &content, Some(moved));
    assert_eq!(
        arrow_line(&world)[..2],
        ["Arrow 2 of 2 · after the change", "helper()"]
    );
    assert!(cursor_in_stop(&world, "util.go"));
    let step: Vec<String> = world
        .screen()
        .descendants(Target::key("tile.m.document"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(step.iter().any(|t| t == "Add keeps its result"), "{step:?}");
    assert!(
        !step.iter().any(|t| t == "It reads better with a name."),
        "{step:?}"
    );
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
fn walks_the_briefs_drawn_diagram_by_its_arrows_opens_an_arrows_stop_and_shares_the_side_with_the_map()
 {
    let mut world = start();
    serve(&world);
    deliver(&mut world, &map_document(), Some(map_bundle("brief")));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
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

    press(&mut world, Key::Enter);
    let view = file_view(&world).expect("the arrow's stop file opens");
    assert!(view.iter().any(|t| t == "calc.go"), "{view:?}");
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("unchanged line 3: func Add(a, b int) int {")
    );
    press(&mut world, Key::Escape);
    assert!(file_view(&world).is_none());

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
    assert_eq!(
        document_diagrams(&world),
        [["Add(a, b)", "helper()", "returns"]]
    );

    type_key(&mut world, "2");
    assert_eq!(
        document_diagrams(&world),
        [["Add(a, b)", "helper()", "returns"]]
    );
    type_key(&mut world, "b");
    assert_eq!(document_diagrams(&world), [["Add(a, b)"]]);
}

/// The drawn diagrams the document shows, by the arrows each draws.
fn document_diagrams(world: &World) -> Vec<Vec<String>> {
    let screen = world.screen();
    screen
        .descendants(Target::key("tile.m.document"))
        .into_iter()
        .filter(|n| {
            n.role == "figure"
                && n.key
                    .as_deref()
                    .is_some_and(|k| k.starts_with("tile.m.review.part."))
        })
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
fn keeps_the_picked_result_in_view_as_down_moves_it_down_a_long_search() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    world.daemon.on("review_search", |_| {
        serde_json::to_value(ReviewSearchResultMessage {
            success: true,
            files: Some(vec![]),
            hits: Some(
                (1..=60)
                    .map(|i| ReviewSearchHit {
                        path: "calc.go".into(),
                        line: i,
                        text: format!("line {i}"),
                    })
                    .collect(),
            ),
            ..ReviewSearchResultMessage::new()
        })
        .unwrap()
    });
    type_key(&mut world, "/");
    search(&mut world, "line");
    let list = "review.search.list";
    assert!(!shows_inside(&world, list, "review.search.option.40"));
    for _ in 0..40 {
        press(&mut world, Key::Down);
    }
    world.settle();
    assert!(shows_inside(&world, list, "review.search.option.40"));
    for _ in 0..30 {
        press(&mut world, Key::Up);
    }
    world.settle();
    assert!(shows_inside(&world, list, "review.search.option.10"));
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

fn headed(
    kind: &str,
    old: Option<i64>,
    now: Option<i64>,
    text: &str,
    head: Option<i64>,
) -> ReviewRow {
    ReviewRow {
        head,
        ..line(kind, old, now, text)
    }
}

/// Serves calc.go as the two lines above Add and then `rows`, with `hash`.
fn serve_calc_rows(world: &World, rows: Vec<ReviewRow>, hash: &'static str) {
    world.daemon.on("review_file", move |r| {
        let mut file = vec![
            headed(" ", Some(1), Some(1), "package calc", Some(1)),
            headed(" ", Some(2), Some(2), "", Some(2)),
        ];
        file.extend(rows.clone());
        file_answer(r["path"].as_str().unwrap_or_default(), file, hash)
    });
}

/// The calc.go stop's rows and the user's marks, without the agent's notes, and where the
/// mark `text` shows among them.
fn mark_after(world: &World, text: &str) -> (Vec<String>, usize) {
    let lines: Vec<String> = stop_lines(world, &stop_key(world, "calc.go"))
        .into_iter()
        .filter(|l| !l.starts_with("agent ") && !l.starts_with("look here "))
        .collect();
    let at = lines
        .iter()
        .position(|l| l.ends_with(text))
        .unwrap_or_else(|| panic!("{text:?} in {lines:?}"));
    (lines, at)
}

/// Writes "Return a + b directly." on the stop's added lines 4 and 5.
fn note_the_added_lines(world: &mut World) {
    press(world, Key::Right);
    press(world, Key::Down);
    press(world, Key::Down);
    shift(world, Key::Down);
    type_key(world, "c");
    write(world, "Return a + b directly.");
}

#[test]
fn quotes_a_removed_line_with_its_diff_sign_and_places_it_at_the_head_line_it_sat_before() {
    let (mut world, kept, _) = open_marking(presented());
    press(&mut world, Key::Right);
    press(&mut world, Key::Down);
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("removed line 4: \treturn a + b")
    );
    type_key(&mut world, "c");
    write(&mut world, "Keep this line.");
    let anchor = kept.lock().unwrap()[0]["anchor"].clone();
    assert_eq!(
        (
            anchor["exact"].clone(),
            anchor["file"].clone(),
            anchor["file_start_line"].clone(),
            anchor["file_end_line"].clone()
        ),
        (
            json!("-\treturn a + b"),
            json!("calc.go"),
            json!(4),
            json!(4)
        )
    );
}

#[test]
fn writes_a_note_on_the_selected_lines_of_the_file_the_walkthrough_shows() {
    let (mut world, kept, _) = open_marking(ReviewBundle {
        frame: "walkthrough".into(),
        ..presented()
    });
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 4: \tsum := a + b")
    );
    shift(&mut world, Key::Down);
    type_key(&mut world, "c");
    write(&mut world, "Return a + b directly.");
    let anchor = kept.lock().unwrap()[0]["anchor"].clone();
    assert_eq!(
        (
            anchor["start_line"].clone(),
            anchor["exact"].clone(),
            anchor["file"].clone(),
            anchor["file_start_line"].clone(),
            anchor["file_end_line"].clone()
        ),
        (
            json!(15),
            json!("\tsum := a + b\n\treturn sum"),
            json!("calc.go"),
            json!(4),
            json!(5)
        )
    );
    assert!(
        marks_strip(&world)
            .iter()
            .any(|t| t == "you L4–5: Return a + b directly."),
        "{:?}",
        marks_strip(&world)
    );
}

#[test]
fn keeps_the_file_view_cursor_on_its_marked_line_when_the_agent_adds_a_copy_of_that_line_above_it()
{
    let walkthrough = ReviewBundle {
        frame: "walkthrough".into(),
        ..presented()
    };
    let (mut world, _, _) = open_marking(walkthrough.clone());
    press(&mut world, Key::Down);
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 5: \treturn sum")
    );
    type_key(&mut world, "c");
    write(&mut world, "Return a + b directly.");

    let copied = vec![
        headed(" ", Some(3), Some(3), "func Add(a, b int) int {", Some(3)),
        headed("-", Some(4), None, "\treturn a + b", None),
        headed("+", None, Some(4), "\tsum := a + b", Some(4)),
        headed("+", None, Some(5), "\treturn sum", None),
        headed("+", None, Some(6), "\treturn sum", Some(5)),
        headed(" ", Some(5), Some(7), "}", Some(6)),
    ];
    serve_calc_rows(&world, copied.clone(), "h2");
    let mut review = walkthrough;
    review.stops[0].file_hash = "h2".into();
    review.stops[0].now_to = 7;
    review.stops[0].rows = copied;
    deliver(&mut world, DOCUMENT, Some(review));
    world.advance(std::time::Duration::from_secs(1));
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 6: \treturn sum")
    );
    assert!(
        marks_strip(&world)
            .iter()
            .any(|t| t == "you L5: Return a + b directly."),
        "{:?}",
        marks_strip(&world)
    );
}

#[test]
fn keeps_a_mark_on_its_line_when_the_document_changes_above_the_stop() {
    let (mut world, _, _) = open_marking(presented());
    note_the_added_lines(&mut world);

    let mut moved = presented();
    for stop in &mut moved.stops {
        stop.heading_line += 2;
        for note in &mut stop.notes {
            note.line += 2;
        }
    }
    moved.not_shown[0].line += 2;
    let content = DOCUMENT.replace(
        "Add used to return its sum directly.",
        "Add used to return its sum directly.\n\nIt reads better with a name.",
    );
    deliver(&mut world, &content, Some(moved));
    world.advance(std::time::Duration::from_secs(1));
    assert!(
        shown(&world)
            .iter()
            .any(|t| t == "It reads better with a name.")
    );
    let (lines, at) = mark_after(&world, "Return a + b directly.");
    assert_eq!(
        (&lines[at - 1][..], &lines[at + 1][..]),
        ("added line 5: \treturn sum", "unchanged line 6: }"),
        "{lines:?}"
    );
}

#[test]
fn keeps_a_mark_on_its_lines_at_head_when_the_agent_adds_lines_above_them() {
    let (mut world, _, _) = open_marking(presented());
    note_the_added_lines(&mut world);
    let saves = world.daemon.sent_of("markdown_annotations_save").len();

    let mut edited = presented();
    edited.stops[0].now_to = 7;
    edited.stops[0].rows = vec![
        headed(" ", Some(3), Some(3), "func Add(a, b int) int {", Some(3)),
        headed("+", None, Some(4), "\t// Add names its sum.", None),
        headed("-", Some(4), None, "\treturn a + b", None),
        headed("+", None, Some(5), "\tsum := a + b", Some(4)),
        headed("+", None, Some(6), "\treturn sum", Some(5)),
        headed(" ", Some(5), Some(7), "}", Some(6)),
    ];
    for note in &mut edited.stops[0].notes {
        (note.now_from, note.now_to) = (note.now_from + 1, note.now_to + 1);
    }
    deliver(&mut world, DOCUMENT, Some(edited));
    world.advance(std::time::Duration::from_secs(1));
    let (lines, at) = mark_after(&world, "Return a + b directly.");
    assert_eq!(
        (&lines[at - 1][..], &lines[at + 1][..]),
        ("added line 6: \treturn sum", "unchanged line 7: }"),
        "{lines:?}"
    );
    assert_eq!(
        world.daemon.sent_of("markdown_annotations_save").len(),
        saves
    );
}

#[test]
fn keeps_a_mark_on_context_lines_shown_around_a_stop_and_shows_it_as_moved_when_those_lines_change()
{
    let (mut world, _, _) = open_marking(presented());
    press(&mut world, Key::Right);
    type_key(&mut world, "e");
    for _ in 0..10 {
        if cursor(&world).name.as_deref() == Some("unchanged line 1: package calc") {
            break;
        }
        press(&mut world, Key::Up);
    }
    type_key(&mut world, "c");
    write(&mut world, "Name the package for what it does.");
    let (lines, at) = mark_after(&world, "Name the package for what it does.");
    assert_eq!(lines[at - 1], "unchanged line 1: package calc", "{lines:?}");

    let mut rows = vec![
        headed("-", Some(1), None, "package calc", None),
        headed("+", None, Some(1), "package arith", None),
        headed(" ", Some(2), Some(2), "", Some(2)),
    ];
    rows.extend(presented().stops[0].rows.clone());
    world.daemon.on("review_file", move |r| {
        file_answer(r["path"].as_str().unwrap_or_default(), rows.clone(), "h2")
    });
    let mut changed = presented();
    changed.stops[0].file_hash = "h2".into();
    deliver(&mut world, DOCUMENT, Some(changed));
    world.advance(std::time::Duration::from_secs(1));
    let moved = moved_notes(&world).expect("the mark moved");
    assert!(
        moved
            .iter()
            .any(|t| t == "you L1: Name the package for what it does."),
        "{moved:?}"
    );
}

#[test]
fn clears_the_sent_marks_when_the_review_was_delivered_but_its_sent_ref_could_not_be_written() {
    let (mut world, kept, _, generation) = open_marking_counted(presented());
    press(&mut world, Key::Right);
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    type_key(&mut world, "c");
    write(&mut world, "Return a + b directly.");
    let (left, g) = (kept.clone(), generation.clone());
    world.daemon.on("markdown_annotations_submit", move |r| {
        left.lock().unwrap().clear();
        json!({
            "event": "markdown_annotations_submit_result",
            "request_id": r["request_id"],
            "document_uri": r["document_uri"],
            "source_kind": r["source_kind"],
            "success": true,
            "status": "delivered",
            "error": "delivered; the next review start cannot show what changed since: no repository",
            "annotations": [],
            "generation": g.fetch_add(1, std::sync::atomic::Ordering::SeqCst) + 1,
        })
    });
    send_key(&mut world);
    press(&mut world, Key::Enter);
    world.settle();
    assert!(
        stop_lines(&world, &stop_key(&world, "calc.go"))
            .iter()
            .all(|l| !l.ends_with("Return a + b directly.")),
        "{:?}",
        stop_lines(&world, &stop_key(&world, "calc.go"))
    );
    let screen = format!("{}", world.screen());
    assert!(
        screen.contains("the next review start cannot show what changed since"),
        "{screen}"
    );
}

#[test]
fn names_the_send_key_as_the_platform_binds_it_in_the_key_line_and_the_key_sheet_and_sends_on_it() {
    let (mut world, _, sends) = open_marking(presented());
    let keymap = attn_app::Keymap::from_setting(None, rune::KeyPlatform::current());
    let label = keymap
        .binding(&attn_app::Action::MarkdownSendAnnotations)
        .map(|b| keymap.label(b))
        .expect("the send has a key");
    let keys: Vec<String> = world
        .screen()
        .descendants(Target::key("tile.m.review.keys"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(keys.contains(&format!("{label} send")), "{keys:?}");
    type_key(&mut world, "?");
    let sheet: Vec<String> = world
        .screen()
        .descendants(Target::role("dialog").name("Review keys"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(
        sheet
            .iter()
            .any(|k| k.starts_with(&format!("{label}: Send the review"))),
        "{sheet:?}"
    );
    type_key(&mut world, "?");
    send_key(&mut world);
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Send the review"))
            .is_some()
    );
    press(&mut world, Key::Enter);
    let sent = sends.lock().unwrap().clone();
    assert_eq!(sent.len(), 1, "{sent:?}");
    assert_eq!(sent[0]["verdict"], "approve");
}

#[test]
fn offers_no_marks_questions_or_verdicts_on_a_document_without_a_review_bundle() {
    let mut world = start_with_session(Some("s-a"));
    serve(&world);
    let (_, sends, _) = keep_drafts(&world);
    deliver(&mut world, DOCUMENT, None);
    world.gesture(|app| app.click(Target::key("tile.m.document")));
    type_key(&mut world, "c");
    type_key(&mut world, "a");
    send_key(&mut world);
    world.settle();
    assert!(world.screen().find(Target::role("dialog")).is_none());
    assert!(sends.lock().unwrap().is_empty());
    assert!(
        world
            .daemon
            .sent_of("markdown_annotations_submit")
            .is_empty()
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
fn a_click_in_the_brief_moves_the_cursor_and_acts_on_the_facts_a_changed_file_a_stops_file_button_and_a_frame_tab()
 {
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

    click(&mut world, Target::key(format!("{stop}.file")));
    assert_eq!(
        file_reads(&world).last().map(String::as_str),
        Some("calc.go")
    );
    let view = file_view(&world).expect("the stop's file button opens its file");
    assert!(view.iter().any(|t| t == "calc.go"), "{view:?}");
    click(&mut world, Target::role("button").name("close esc"));

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
fn a_click_on_a_verdict_in_the_send_sheet_picks_it_as_its_key_does_and_send_sends_as_enter_does() {
    let (mut world, _, sends) = open_marking(presented());
    press(&mut world, Key::Down);
    send_key(&mut world);
    let sheet: Vec<String> = world
        .screen()
        .descendants(Target::key("review.send"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(
        sheet.iter().any(|t| t == "Send the review to Agent a"),
        "the sheet names the session the review goes to: {sheet:?}"
    );
    click(&mut world, Target::key("review.send.verdict.c"));
    let picked = world
        .screen()
        .all(Target::role("radio-button"))
        .into_iter()
        .find(|n| n.is("checked"))
        .and_then(|n| n.name);
    assert_eq!(picked.as_deref(), Some("comment"));
    click(&mut world, Target::key("review.send.go"));
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
        apply_edit(&held, r)
    });
    asked
}

fn apply_edit(calc: &Files, r: &serde_json::Value) -> serde_json::Value {
    let mut rows = calc.lock().unwrap();
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
fn an_edit_saved_as_its_box_is_dismissed_lands_in_the_review_and_leaves_a_newer_box_open() {
    let (mut world, calc, kept, _) = open_editing();
    world.daemon.on("review_edit", |_| ());
    type_key(&mut world, "i");
    append(&mut world, " // named");
    press(&mut world, Key::Enter);
    press(&mut world, Key::Escape);
    assert!(edit_box(&world).is_none());
    type_key(&mut world, "i");
    assert!(edit_box(&world).is_some());

    let asked = world.daemon.sent_of("review_edit")[0].clone();
    world.daemon.reply_to(&asked, apply_edit(&calc, &asked));
    world.settle();
    world.advance(std::time::Duration::from_secs(1));
    world.settle();
    assert!(edit_box(&world).is_some(), "the newer box stays open");
    assert!(kept.lock().unwrap().iter().any(|m| m["type"] == "edit"));
    press(&mut world, Key::Escape);
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

    serve_edits(&world, &calc);
    type_key(&mut world, "u");
    world.advance(std::time::Duration::from_secs(1));
    world.settle();
    assert_eq!(calc.lock().unwrap()[4].text, "\tsum := a + b");
    assert!(kept.lock().unwrap().iter().all(|m| m["type"] != "edit"));
}

#[test]
fn an_edit_refused_after_its_box_is_dismissed_says_why() {
    let (mut world, calc, kept, _) = open_editing();
    world.daemon.on("review_edit", |_| ());
    type_key(&mut world, "i");
    append(&mut world, " // named");
    press(&mut world, Key::Enter);
    press(&mut world, Key::Escape);
    assert!(edit_box(&world).is_none());

    calc.lock().unwrap()[3].text = "func Add(x, y int) int {".into();
    let asked = world.daemon.sent_of("review_edit")[0].clone();
    world.daemon.reply_to(&asked, apply_edit(&calc, &asked));
    world.settle();
    let notices: Vec<String> = world
        .screen()
        .all(Target::role("status"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(
        notices
            .iter()
            .any(|t| t
                .starts_with("calc.go changed since this edit opened, so nothing was written.")),
        "{notices:?}"
    );
    assert!(kept.lock().unwrap().iter().all(|m| m["type"] != "edit"));
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
    let (edit, note): (Vec<&String>, Vec<&String>) =
        ids.iter().partition(|id| id.starts_with("review-edit-"));
    world.gesture(|app| app.click(Target::key(format!("review.mark.{}.remove", edit[0]))));
    press(&mut world, Key::Escape);
    world.gesture(|app| app.click(Target::key(format!("review.mark.{}.remove", note[0]))));
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
fn a_table_in_a_review_takes_the_cards_width_past_the_prose_column() {
    let cell = |n: usize| format!("cell {n} of a table that is wider than a paragraph");
    let table = format!(
        "\n| One | Two | Three |\n| --- | --- | --- |\n| {} | {} | {} |\n",
        cell(1),
        cell(2),
        cell(3)
    );
    let mut world = start();
    deliver(&mut world, &format!("{DOCUMENT}{table}"), Some(bundle()));
    click(&mut world, Target::key("tile.m.focus"));
    let screen = world.screen();
    let [x, _, _, _] = screen.bounds(Target::key("tile.m.document")).unwrap();
    let [cx, _, cw, _] = screen.bounds(Target::role("label").name(cell(3))).unwrap();
    assert!(
        cx + cw - x > 800.0,
        "the table runs past the 68-character prose column: its last cell ends {} in",
        cx + cw - x
    );
}

#[test]
fn says_why_i_edits_nothing_on_prose() {
    let (mut world, _, _) = open_marking(presented());
    press(&mut world, Key::Down);
    type_key(&mut world, "i");
    assert!(edit_box(&world).is_none());
    let screen = format!("{}", world.screen());
    assert!(
        screen.contains("Put the cursor on a line of code or a document stop to edit it."),
        "{screen}"
    );
}

const CALC_DOCUMENT: &str = "---
attn: review
frame: brief
repository: /repo
base: 4be1c0d
head: 9f3a7e2
---

# Name the sum

Add used to return its sum directly.

## Add keeps its result

### calc.go:3-10

- L4!: The sum gets a name.
";

const CALC_HEAD: [&str; 10] = [
    "package calc",
    "",
    "func Add(a, b int) int {",
    "\tsum := a + b",
    "\treturn sum",
    "}",
    "",
    "const query = `",
    "SELECT 1",
    "`",
];

/// Each line's line at head, as the daemon gives it: through the longest run of lines head and
/// now share.
fn heads_of(lines: &[String]) -> Vec<Option<i64>> {
    let (h, n) = (CALC_HEAD.len(), lines.len());
    let mut table = vec![vec![0usize; n + 1]; h + 1];
    for i in (0..h).rev() {
        for j in (0..n).rev() {
            table[i][j] = if CALC_HEAD[i] == lines[j] {
                table[i + 1][j + 1] + 1
            } else {
                table[i + 1][j].max(table[i][j + 1])
            };
        }
    }
    let mut heads = vec![None; n];
    let (mut i, mut j) = (0, 0);
    while i < h && j < n {
        if CALC_HEAD[i] == lines[j] {
            i += 1;
            heads[j] = Some(i as i64);
            j += 1;
        } else if table[i + 1][j] >= table[i][j + 1] {
            i += 1;
        } else {
            j += 1;
        }
    }
    heads
}

/// calc.go on the review's machine: `review_file` reads it, `review_edit` writes it while its
/// hash holds. Lines up to `paired_through` written over a line of head show as that line
/// removed and the new one added, as git shows them.
struct Calc {
    lines: Vec<String>,
    version: usize,
    conflict_next: bool,
    paired_through: i64,
}

type SharedCalc = std::sync::Arc<std::sync::Mutex<Calc>>;

impl Calc {
    fn hash(&self) -> String {
        format!("h{}", self.version)
    }

    fn added(&self) -> Vec<String> {
        self.lines[3..self.lines.len() - 5].to_vec()
    }

    fn rows(&self) -> Vec<ReviewRow> {
        let heads = heads_of(&self.lines);
        let n = self.lines.len();
        let added = n - 8;
        let at = |kind: &str, old: Option<i64>, i: usize| ReviewRow {
            head: heads[i],
            ..line(kind, old, Some(i as i64 + 1), &self.lines[i])
        };
        let mut rows: Vec<ReviewRow> = (0..3).map(|i| at(" ", Some(i as i64 + 1), i)).collect();
        rows.push(line("-", Some(4), None, "\treturn a + b"));
        rows.extend((3..3 + added).map(|i| at("+", None, i)));
        rows.extend((3 + added..n).map(|i| at(" ", Some((i + 2 - added) as i64), i)));
        rows.into_iter()
            .flat_map(|r| {
                let head = r.now.and_then(|now| CALC_HEAD.get(now as usize - 1));
                match (r.kind.as_str(), r.now, head) {
                    (" ", Some(now), Some(head))
                        if now <= self.paired_through && *head != r.text =>
                    {
                        vec![
                            line("-", Some(now), None, head),
                            line("+", None, Some(now), &r.text),
                        ]
                    }
                    _ => vec![r],
                }
            })
            .collect()
    }

    fn write(&mut self, from: usize, to: usize, text: &str) {
        self.lines
            .splice(from - 1..to, text.split('\n').map(str::to_owned));
        self.version += 1;
    }

    fn edit(&mut self, r: &serde_json::Value) -> serde_json::Value {
        if self.conflict_next || r["base_hash"] != json!(self.hash()) {
            self.conflict_next = false;
            self.version += 1;
            return json!({"event": "review_edit_result", "success": true, "conflict": true, "current_hash": self.hash()});
        }
        let (from, to) = (r["from"].as_u64().unwrap(), r["to"].as_u64().unwrap());
        self.write(
            from as usize,
            to as usize,
            r["text"].as_str().unwrap_or_default(),
        );
        json!({"event": "review_edit_result", "success": true, "conflict": false, "hash": self.hash()})
    }

    fn read(&self, r: &serde_json::Value) -> serde_json::Value {
        serde_json::to_value(ReviewFileResultMessage {
            success: true,
            path: r["path"].as_str().map(str::to_owned),
            rows: Some(self.rows()),
            hash: Some(self.hash()),
            ..ReviewFileResultMessage::new()
        })
        .unwrap()
    }
}

fn calc_bundle(calc: &Calc, lines_above: i64, not_shown: Vec<ReviewNotShown>) -> ReviewBundle {
    let rows = calc.rows();
    let last = rows.iter().rev().find_map(|r| r.now).unwrap();
    ReviewBundle {
        frame: "brief".into(),
        repository: "/repo".into(),
        base: Some("4be1c0d".into()),
        head: "9f3a7e2".into(),
        stops: vec![ReviewStop {
            heading_line: 15 + lines_above,
            path: "calc.go".into(),
            from: 3,
            to: 10,
            now_from: 3,
            now_to: last,
            file_hash: calc.hash(),
            rows: rows
                .into_iter()
                .filter(|r| r.now.unwrap_or(4) >= 3)
                .collect(),
            notes: vec![note(17 + lines_above, 4, true, "The sum gets a name.")],
            ..ReviewStop::default()
        }],
        files: vec![file("calc.go", 2, 1)],
        not_shown,
        ..ReviewBundle::default()
    }
}

/// A review of calc.go open in its tile, bound to a session, with its file and its draft
/// served.
struct Edited {
    world: World,
    calc: SharedCalc,
    kept: Marks,
    document: String,
    not_shown: Vec<ReviewNotShown>,
    frame: &'static str,
}

fn open_calc() -> Edited {
    open_calc_with("", Vec::new(), "brief")
}

fn open_calc_with(trailer: &str, not_shown: Vec<ReviewNotShown>, frame: &'static str) -> Edited {
    let world = start_with_session(Some("s-a"));
    let calc: SharedCalc = std::sync::Arc::new(std::sync::Mutex::new(Calc {
        lines: CALC_HEAD.map(str::to_owned).to_vec(),
        version: 1,
        conflict_next: false,
        paired_through: 0,
    }));
    let read = calc.clone();
    world
        .daemon
        .on("review_file", move |r| read.lock().unwrap().read(r));
    let (kept, _, _) = keep_drafts(&world);
    let written = calc.clone();
    world
        .daemon
        .on("review_edit", move |r| written.lock().unwrap().edit(r));
    let mut edited = Edited {
        world,
        calc,
        kept,
        document: format!("{CALC_DOCUMENT}{trailer}"),
        not_shown,
        frame,
    };
    edited.show();
    edited
        .world
        .gesture(|app| app.click(Target::key("tile.m.review.keys")));
    edited
}

impl Edited {
    /// Delivers the document with a bundle of calc.go as it is now.
    fn show(&mut self) {
        let document = self.document.clone();
        self.show_content(&document);
    }

    fn show_content(&mut self, content: &str) {
        let above = content.split('\n').count() as i64 - self.document.split('\n').count() as i64;
        let review = ReviewBundle {
            frame: self.frame.into(),
            ..calc_bundle(&self.calc.lock().unwrap(), above, self.not_shown.clone())
        };
        deliver(&mut self.world, content, Some(review));
    }

    /// Moves the document's cursor into the stop, then to the row of line `line`.
    fn walk_to_row(&mut self, line: i64) {
        press(&mut self.world, Key::Right);
        let mut passed = Vec::new();
        while cursor_line(&self.world) != Some(line) {
            assert!(
                passed.len() <= 40,
                "the cursor never reached line {line}; it passed {passed:?}"
            );
            let at = cursor_line(&self.world);
            passed.push(at);
            let key = if at.is_some_and(|at| at > line) {
                Key::Up
            } else {
                Key::Down
            };
            press(&mut self.world, key);
        }
    }

    /// Moves the file view's cursor up to the row whose text holds `text`.
    fn file_up_to(&mut self, text: &str) {
        for _ in 0..12 {
            if file_cursor(&self.world).is_some_and(|c| c.contains(text)) {
                return;
            }
            press(&mut self.world, Key::Up);
        }
        panic!(
            "the file view's cursor never reached {text:?}: {:?}",
            file_cursor(&self.world)
        );
    }

    fn edit_count(&self) -> usize {
        self.world.daemon.sent_of("review_edit").len()
    }

    fn edits_kept(&self) -> Vec<serde_json::Value> {
        self.kept
            .lock()
            .unwrap()
            .iter()
            .filter(|m| m["type"] == "edit")
            .cloned()
            .collect()
    }
}

/// The line now of the row the document's cursor stands on; none on a removed row.
fn cursor_line(world: &World) -> Option<i64> {
    let name = cursor(world).name?;
    if name.starts_with("removed ") {
        return None;
    }
    let (_, after) = name.split_once("line ")?;
    after.split(':').next()?.parse().ok()
}

/// The text in the edit box.
fn edit_text(world: &World) -> String {
    world
        .screen()
        .get(Target::key("review.edit.field"))
        .value
        .unwrap_or_default()
}

/// Replaces the edit box's text with `text`, its lines broken as a person breaks them.
fn retype(world: &mut World, text: &str) {
    let erase = edit_text(world).chars().count();
    world.gesture(|app| {
        for _ in 0..erase {
            app.key(KeyEvent {
                key: Key::Backspace,
                mods: Mods::default(),
            });
        }
    });
    for (i, line) in text.split('\n').enumerate() {
        if i > 0 {
            shift(world, Key::Enter);
        }
        append(world, line);
    }
}

fn settle_draft(world: &mut World) {
    world.advance(std::time::Duration::from_secs(1));
    world.settle();
}

/// Saves the edit box with Enter, as one line saves.
fn save_line(world: &mut World) {
    press(world, Key::Enter);
    settle_draft(world);
}

/// The chord that saves an edit of several lines, as this platform binds it.
fn save_several_chord() -> rune::Chord {
    rune::Chord::parse("mod+enter").unwrap()
}

fn save_several(world: &mut World) {
    let ev = attn_testkit::key_event(&save_several_chord());
    world.gesture(|app| app.key(ev));
    settle_draft(world);
}

fn alt(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods {
                alt: true,
                ..Mods::default()
            },
        })
    });
}

fn moved_notes(world: &World) -> Option<Vec<String>> {
    world.screen().find(Target::key("tile.m.review.moved"))?;
    Some(
        world
            .screen()
            .descendants(Target::key("tile.m.review.moved"))
            .into_iter()
            .filter_map(|n| n.name)
            .collect(),
    )
}

/// The marks shown in calc.go's stop, in order.
fn stop_marks(world: &World) -> Vec<String> {
    stop_lines(world, &stop_key(world, "calc.go"))
        .into_iter()
        .filter(|l| l.starts_with("you "))
        .collect()
}

fn notices(world: &World) -> Vec<String> {
    world
        .screen()
        .all(Target::role("status"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

#[test]
fn takes_a_whole_raw_string_of_several_lines_and_alt_up_takes_in_the_line_above() {
    let mut e = open_calc();
    e.walk_to_row(9);
    type_key(&mut e.world, "i");
    let said = edit_box(&e.world).expect("the edit box opens");
    assert!(
        said.iter()
            .any(|t| t == "Editing calc.go: this string, 3 lines"),
        "{said:?}"
    );
    assert_eq!(edit_text(&e.world), "const query = `\nSELECT 1\n`");

    alt(&mut e.world, Key::Up);
    let said = edit_box(&e.world).unwrap();
    assert!(
        said.iter().any(|t| t == "Editing calc.go: lines 7–10"),
        "{said:?}"
    );
    assert_eq!(edit_text(&e.world), "\nconst query = `\nSELECT 1\n`");
}

#[test]
fn names_the_save_key_of_several_lines_as_the_platform_binds_it_and_saves_on_it() {
    let mut e = open_calc();
    e.walk_to_row(9);
    type_key(&mut e.world, "i");
    let label = save_several_chord().label_on(rune::KeyPlatform::current());
    let said = edit_box(&e.world).unwrap();
    assert!(
        said.iter().any(|t| *t == format!("{label} save")),
        "{said:?}"
    );
    retype(&mut e.world, "const query = `\nSELECT 2\n`");
    save_several(&mut e.world);
    assert_eq!(e.edit_count(), 1);
    assert_eq!(e.calc.lock().unwrap().lines[8], "SELECT 2");
}

#[test]
fn keeps_typed_text_above_a_line_alt_down_takes_in_even_from_a_blank_line() {
    let mut e = open_calc();
    e.walk_to_row(7);
    type_key(&mut e.world, "i");
    assert_eq!(edit_text(&e.world), "");
    append(&mut e.world, "// the query");
    alt(&mut e.world, Key::Down);
    assert_eq!(edit_text(&e.world), "// the query\nconst query = `");
}

#[test]
fn undoes_the_newest_edit_of_a_line_and_moves_an_edit_the_file_no_longer_holds_to_the_moved_notes_where_dropping_it_writes_nothing()
 {
    let mut e = open_calc();
    e.walk_to_row(4);
    type_key(&mut e.world, "i");
    retype(&mut e.world, "\ttotal := a + b");
    save_line(&mut e.world);
    e.show();
    type_key(&mut e.world, "i");
    retype(&mut e.world, "\tresult := a + b");
    save_line(&mut e.world);
    e.show();

    type_key(&mut e.world, "u");
    settle_draft(&mut e.world);
    let undo = e.world.daemon.sent_of("review_edit")[2].clone();
    assert_eq!(
        (
            undo["from"].clone(),
            undo["to"].clone(),
            undo["text"].clone()
        ),
        (json!(4), json!(4), json!("\ttotal := a + b"))
    );
    assert_eq!(e.calc.lock().unwrap().added()[0], "\ttotal := a + b");
    let kept = e.edits_kept();
    assert_eq!(kept.len(), 1, "{kept:?}");
    assert_eq!(
        (kept[0]["edit_was"].clone(), kept[0]["edit_now"].clone()),
        (json!("\tsum := a + b"), json!("\ttotal := a + b"))
    );
    e.show();
    assert_eq!(
        stop_marks(&e.world),
        ["you edited L4: it was \tsum := a + b"]
    );

    e.calc.lock().unwrap().write(4, 4, "\tagent := a + b");
    e.show();
    let moved = moved_notes(&e.world).expect("the edit moves to the moved notes");
    assert!(
        moved
            .iter()
            .any(|t| t == "you edited L4: it was \tsum := a + b"),
        "{moved:?}"
    );
    type_key(&mut e.world, "u");
    settle_draft(&mut e.world);
    assert_eq!(e.edit_count(), 3);
    let id = kept[0]["id"].as_str().unwrap().to_owned();
    click(
        &mut e.world,
        Target::key(format!("review.mark.{id}.remove")),
    );
    settle_draft(&mut e.world);
    assert!(e.edits_kept().is_empty());
    assert_eq!(e.edit_count(), 3);
    assert_eq!(e.calc.lock().unwrap().added()[0], "\tagent := a + b");
}

#[test]
fn shows_an_edit_at_once_in_the_open_file_outside_any_stop_and_undoes_the_newer_of_two_edits_there()
{
    let mut e = open_calc();
    e.walk_to_row(4);
    press(&mut e.world, Key::Enter);
    e.file_up_to("package");

    type_key(&mut e.world, "i");
    retype(&mut e.world, "package sums");
    save_line(&mut e.world);
    assert_eq!(
        file_cursor(&e.world).as_deref(),
        Some("unchanged line 1: package sums")
    );
    type_key(&mut e.world, "i");
    retype(&mut e.world, "package totals");
    save_line(&mut e.world);
    assert_eq!(
        file_cursor(&e.world).as_deref(),
        Some("unchanged line 1: package totals")
    );
    assert!(
        file_view(&e.world)
            .unwrap()
            .iter()
            .any(|t| t.starts_with("you edited")),
        "{:?}",
        file_view(&e.world)
    );

    type_key(&mut e.world, "u");
    settle_draft(&mut e.world);
    let undo = e.world.daemon.sent_of("review_edit")[2].clone();
    assert_eq!(
        (
            undo["from"].clone(),
            undo["to"].clone(),
            undo["text"].clone()
        ),
        (json!(1), json!(1), json!("package sums"))
    );
    assert_eq!(e.calc.lock().unwrap().lines[0], "package sums");
    assert_eq!(
        file_cursor(&e.world).as_deref(),
        Some("unchanged line 1: package sums")
    );
}

#[test]
fn never_moves_an_edit_onto_another_line_with_the_same_text_and_u_there_writes_nothing() {
    let mut e = open_calc();
    e.walk_to_row(4);
    type_key(&mut e.world, "i");
    retype(&mut e.world, "}");
    save_line(&mut e.world);
    e.show();
    type_key(&mut e.world, "i");
    retype(&mut e.world, "\tdone := true");
    save_line(&mut e.world);
    e.show();
    assert_eq!(
        e.calc
            .lock()
            .unwrap()
            .lines
            .iter()
            .filter(|l| *l == "}")
            .count(),
        1
    );

    let moved = moved_notes(&e.world).expect("the first edit moved");
    assert!(
        moved
            .iter()
            .any(|t| t == "you edited L4: it was \tsum := a + b"),
        "{moved:?}"
    );
    e.walk_to_row(6);
    assert_eq!(
        cursor(&e.world).name.as_deref(),
        Some("unchanged line 6: }")
    );
    type_key(&mut e.world, "u");
    settle_draft(&mut e.world);
    assert_eq!(e.edit_count(), 2);
    assert_eq!(e.calc.lock().unwrap().lines[5], "}");
    assert_eq!(e.edits_kept().len(), 2);
}

#[test]
fn keeps_an_edit_on_its_line_when_the_line_above_is_edited_and_the_agent_adds_lines_above_both_and_u_finds_it_there()
 {
    let mut e = open_calc();
    e.walk_to_row(5);
    type_key(&mut e.world, "i");
    retype(&mut e.world, "\treturn sum // named");
    save_line(&mut e.world);
    e.show();
    e.walk_to_row(4);
    type_key(&mut e.world, "i");
    retype(&mut e.world, "\ttotal := a + b");
    save_line(&mut e.world);
    e.calc
        .lock()
        .unwrap()
        .write(1, 1, "// Package calc adds.\npackage calc");
    e.show();
    assert_eq!(moved_notes(&e.world), None);
    assert_eq!(
        stop_marks(&e.world),
        [
            "you edited L4: it was \tsum := a + b",
            "you edited L5: it was \treturn sum"
        ]
    );

    e.walk_to_row(6);
    type_key(&mut e.world, "u");
    settle_draft(&mut e.world);
    let undo = e.world.daemon.sent_of("review_edit")[2].clone();
    assert_eq!(
        (
            undo["from"].clone(),
            undo["to"].clone(),
            undo["text"].clone()
        ),
        (json!(6), json!(6), json!("\treturn sum"))
    );
    assert_eq!(
        e.calc.lock().unwrap().lines[4..6],
        ["\ttotal := a + b", "\treturn sum"]
    );
}

#[test]
fn edits_a_line_of_a_crlf_file_without_its_carriage_return() {
    let mut e = open_calc();
    e.calc
        .lock()
        .unwrap()
        .write(4, 5, "\tsum := a + b\r\n\treturn sum\r");
    e.show();
    e.walk_to_row(4);
    type_key(&mut e.world, "i");
    assert_eq!(edit_text(&e.world), "\tsum := a + b");
}

#[test]
fn keeps_the_cursor_on_the_edited_line_when_the_edit_changes_the_stop_under_it() {
    let mut e = open_calc();
    e.walk_to_row(5);
    type_key(&mut e.world, "i");
    retype(&mut e.world, "\t// the sum\n\treturn sum");
    save_several(&mut e.world);
    let grown = e.document.replace(
        "Add used to return its sum directly.",
        "Add used to return its sum directly.\n\nNow it is named.",
    );
    e.show_content(&grown);
    assert!(shown(&e.world).iter().any(|t| t == "Now it is named."));
    assert_eq!(cursor_line(&e.world), Some(5));
    assert_eq!(
        cursor(&e.world).name.as_deref(),
        Some("added line 5: \t// the sum")
    );

    press(&mut e.world, Key::Enter);
    type_key(&mut e.world, "i");
    retype(&mut e.world, "\t// the named sum");
    save_line(&mut e.world);
    e.show_content(&grown);
    assert_eq!(
        file_cursor(&e.world).as_deref(),
        Some("added line 5: \t// the named sum")
    );
}

#[test]
fn puts_the_cursor_on_the_new_text_after_editing_an_unchanged_line_and_says_why_i_does_nothing_on_a_removed_line()
 {
    let mut e = open_calc();
    e.calc.lock().unwrap().paired_through = 3;
    press(&mut e.world, Key::Right);
    type_key(&mut e.world, "e");
    e.walk_to_row(3);
    type_key(&mut e.world, "i");
    retype(&mut e.world, "func Add(a, b int) int { // named");
    save_line(&mut e.world);
    e.show();
    assert_eq!(
        cursor(&e.world).name.as_deref(),
        Some("added line 3: func Add(a, b int) int { // named")
    );
    type_key(&mut e.world, "i");
    assert_eq!(edit_text(&e.world), "func Add(a, b int) int { // named");
    press(&mut e.world, Key::Escape);

    press(&mut e.world, Key::Up);
    assert_eq!(
        cursor(&e.world).name.as_deref(),
        Some("removed line 3: func Add(a, b int) int {")
    );
    type_key(&mut e.world, "i");
    assert!(edit_box(&e.world).is_none());
    assert!(
        notices(&e.world)
            .iter()
            .any(|t| t == "This line was removed. i edits the code as it is now."),
        "{:?}",
        notices(&e.world)
    );

    press(&mut e.world, Key::Enter);
    e.file_up_to("package calc");
    type_key(&mut e.world, "i");
    retype(&mut e.world, "package sum");
    save_line(&mut e.world);
    e.show();
    assert_eq!(
        file_cursor(&e.world).as_deref(),
        Some("added line 1: package sum")
    );
    assert_eq!(e.edit_count(), 2);
}

#[test]
fn keeps_the_lines_e_showed_when_an_edit_there_changes_the_file_with_the_cursor_and_the_edit_on_them()
 {
    let mut e = open_calc();
    press(&mut e.world, Key::Right);
    type_key(&mut e.world, "e");
    e.walk_to_row(1);
    type_key(&mut e.world, "i");
    retype(&mut e.world, "package sum");
    save_line(&mut e.world);
    e.show();
    assert_eq!(
        cursor(&e.world).name.as_deref(),
        Some("unchanged line 1: package sum")
    );
    assert_eq!(stop_marks(&e.world), ["you edited L1: it was package calc"]);
}

#[test]
fn keeps_an_open_edit_on_top_when_the_reader_is_clicked_and_the_send_waits_for_it() {
    let mut e = open_calc();
    e.walk_to_row(4);
    type_key(&mut e.world, "i");
    retype(&mut e.world, "\ttotal := a + b");

    let window = e.world.screen().bounds(Target::key("window")).unwrap();
    e.world
        .gesture(|app| app.click_at((window[2] - 40.0, window[3] - 40.0)));
    assert!(edit_box(&e.world).is_some());
    assert!(
        e.world
            .screen()
            .find(Target::role("dialog").name("Search the repository"))
            .is_none()
    );
    assert_eq!(edit_text(&e.world), "\ttotal := a + b");

    e.world
        .automate("run", json!({"name": "markdown.sendAnnotations"}))
        .unwrap();
    e.world.settle();
    assert!(
        e.world
            .screen()
            .find(Target::role("dialog").name("Send the review"))
            .is_none()
    );
    assert!(
        e.world
            .daemon
            .sent_of("markdown_annotations_submit")
            .is_empty()
    );
    assert_eq!(edit_text(&e.world), "\ttotal := a + b");
    assert!(
        e.world
            .screen()
            .get(Target::key("review.edit.field"))
            .is("focused")
    );
    click(&mut e.world, Target::key("review.edit.cancel"));
    assert!(edit_box(&e.world).is_none());
}

#[test]
fn moves_only_the_view_an_edit_was_made_in_and_a_view_of_the_same_file_below_it_keeps_its_line() {
    let mut e = open_calc_with(
        "\n## Not shown\n\n- calc.go: also here\n",
        vec![ReviewNotShown {
            line: 21,
            path: "calc.go".into(),
            reason: "also here".into(),
        }],
        "brief",
    );
    e.walk_to_row(9);
    press(&mut e.world, Key::Enter);
    assert_eq!(
        file_cursor(&e.world).as_deref(),
        Some("unchanged line 9: SELECT 1")
    );

    let entry = e
        .world
        .screen()
        .descendants(Target::key("tile.m.document"))
        .into_iter()
        .find_map(|n| n.key.filter(|k| k.ends_with(".entry.0")))
        .expect("the Not shown entry");
    click(&mut e.world, Target::key(entry));
    assert_eq!(
        file_cursor(&e.world).as_deref(),
        Some("unchanged line 1: package calc")
    );
    type_key(&mut e.world, "i");
    retype(&mut e.world, "package sums");
    save_line(&mut e.world);
    assert_eq!(
        file_cursor(&e.world).as_deref(),
        Some("unchanged line 1: package sums")
    );

    ctrl_o(&mut e.world);
    assert_eq!(
        file_cursor(&e.world).as_deref(),
        Some("unchanged line 9: SELECT 1")
    );
}

#[test]
fn puts_the_file_view_cursor_on_the_new_text_when_a_delivery_arrives_before_the_read_after_the_edit_answers()
 {
    let mut e = open_calc();
    e.calc.lock().unwrap().paired_through = i64::MAX;
    e.walk_to_row(4);
    press(&mut e.world, Key::Enter);
    e.file_up_to("package");

    let holding = std::sync::Arc::new(std::sync::atomic::AtomicBool::new(false));
    let (read, hold) = (e.calc.clone(), holding.clone());
    e.world.daemon.on("review_file", move |r| {
        (!hold.load(std::sync::atomic::Ordering::SeqCst)).then(|| read.lock().unwrap().read(r))
    });
    type_key(&mut e.world, "i");
    let asked = e.world.daemon.sent_of("review_file").len();
    holding.store(true, std::sync::atomic::Ordering::SeqCst);
    retype(&mut e.world, "package sum");
    save_line(&mut e.world);
    let held: Vec<_> = e.world.daemon.sent_of("review_file")[asked..].to_vec();
    assert!(!held.is_empty());
    holding.store(false, std::sync::atomic::Ordering::SeqCst);
    e.show();
    assert_eq!(
        file_cursor(&e.world).as_deref(),
        Some("added line 1: package sum")
    );

    for r in &held {
        let answer = e.calc.lock().unwrap().read(r);
        e.world.daemon.reply_to(r, answer);
    }
    e.show();
    assert_eq!(
        file_cursor(&e.world).as_deref(),
        Some("added line 1: package sum")
    );
}

#[test]
fn puts_the_stage_cursor_on_the_new_text_when_a_delivery_refreshes_the_walkthrough_stage_while_the_edit_is_saving()
 {
    let mut e = open_calc_with("", Vec::new(), "walkthrough");
    e.calc.lock().unwrap().paired_through = i64::MAX;
    e.file_up_to("package calc");
    e.world.daemon.on("review_edit", |_| ());
    type_key(&mut e.world, "i");
    retype(&mut e.world, "package sum");
    press(&mut e.world, Key::Enter);
    let review = ReviewBundle {
        frame: "walkthrough".into(),
        head: "aaaaaaa".into(),
        ..calc_bundle(&e.calc.lock().unwrap(), 0, Vec::new())
    };
    deliver(&mut e.world, CALC_DOCUMENT, Some(review));
    let saving = e.world.daemon.sent_of("review_edit")[0].clone();
    let answer = e.calc.lock().unwrap().edit(&saving);
    e.world.daemon.reply_to(&saving, answer);
    settle_draft(&mut e.world);
    e.show();
    assert_eq!(
        file_cursor(&e.world).as_deref(),
        Some("added line 1: package sum")
    );
}

#[test]
fn puts_the_cursor_on_the_changed_line_of_an_edit_whose_first_line_stays_as_it_was() {
    let mut e = open_calc();
    e.calc.lock().unwrap().paired_through = i64::MAX;
    e.walk_to_row(9);
    type_key(&mut e.world, "i");
    retype(&mut e.world, "const query = `\nSELECT 2\n`");
    save_several(&mut e.world);
    e.show();
    assert_eq!(
        cursor(&e.world).name.as_deref(),
        Some("added line 9: SELECT 2")
    );
}

#[test]
fn drops_the_lines_e_showed_when_their_stop_leaves_the_review_and_never_gives_them_to_the_stop_that_takes_its_place()
 {
    let mut e = open_calc();
    let other: Vec<ReviewRow> = (1..=30)
        .map(|n| ReviewRow {
            head: Some(n),
            ..line(" ", Some(n), Some(n), &format!("other line {n}"))
        })
        .collect();
    let (read, served) = (e.calc.clone(), other.clone());
    e.world.daemon.on("review_file", move |r| {
        if r["path"] == "other.go" {
            file_answer("other.go", served.clone(), "o1")
        } else {
            read.lock().unwrap().read(r)
        }
    });
    let section = "### other.go:14-16\n\n- L15: Another stop.\n";
    let other_stop = |heading: i64| ReviewStop {
        heading_line: heading,
        path: "other.go".into(),
        from: 14,
        to: 16,
        now_from: 14,
        now_to: 16,
        file_hash: "o1".into(),
        rows: other[13..16].to_vec(),
        notes: vec![note(heading + 2, 15, false, "Another stop.")],
        ..ReviewStop::default()
    };
    let review = |stops: Vec<ReviewStop>| {
        let mut review = calc_bundle(&e.calc.lock().unwrap(), 0, Vec::new());
        review.stops = stops;
        review.files.push(file("other.go", 0, 0));
        review
    };
    let both = format!("{CALC_DOCUMENT}\n{section}");
    let heading = both
        .split('\n')
        .position(|l| l == "### other.go:14-16")
        .unwrap() as i64
        + 1;
    let calc_stop = calc_bundle(&e.calc.lock().unwrap(), 0, Vec::new()).stops[0].clone();
    let first = review(vec![calc_stop, other_stop(heading)]);
    deliver(&mut e.world, &both, Some(first));
    press(&mut e.world, Key::Right);
    type_key(&mut e.world, "e");
    assert!(
        stop_lines(&e.world, &stop_key(&e.world, "calc.go"))
            .iter()
            .any(|l| l == "unchanged line 1: package calc")
    );

    let only_other =
        CALC_DOCUMENT.replace("### calc.go:3-10\n\n- L4!: The sum gets a name.\n", section);
    let second = review(vec![other_stop(15)]);
    deliver(&mut e.world, &only_other, Some(second));
    assert!(shown(&e.world).iter().any(|t| t == "Another stop."));
    let lines: Vec<String> = stop_lines(&e.world, &stop_key(&e.world, "other.go"))
        .into_iter()
        .filter(|l| l.starts_with("unchanged line"))
        .collect();
    assert_eq!(
        lines,
        [
            "unchanged line 14: other line 14",
            "unchanged line 15: other line 15",
            "unchanged line 16: other line 16"
        ]
    );
}

#[test]
fn offers_no_edit_on_a_document_without_a_review_bundle() {
    let mut world = start();
    serve(&world);
    keep_drafts(&world);
    deliver(&mut world, DOCUMENT, None);
    world.gesture(|app| app.click(Target::key("tile.m.document")));
    type_key(&mut world, "i");
    type_key(&mut world, "u");
    settle_draft(&mut world);
    assert!(edit_box(&world).is_none());
    assert!(world.daemon.sent_of("review_file").is_empty());
    assert!(world.daemon.sent_of("review_edit").is_empty());
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

/// Whether the element keyed `row` shows whole inside the scroll view keyed `list`.
fn shows_inside(world: &World, list: &str, row: &str) -> bool {
    let screen = world.screen();
    let (Some([_, ly, _, lh]), Some([_, ry, _, rh])) = (
        screen.bounds(Target::key(list)),
        screen.bounds(Target::key(row)),
    ) else {
        return false;
    };
    ry >= ly - 0.5 && ry + rh <= ly + lh + 0.5
}

#[test]
fn keeps_the_picked_place_in_view_as_the_keys_move_it_down_a_long_list_of_uses() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    world.daemon.on("code_nav", |r| {
        if r["kind"] == "definition" {
            nav_answer(vec![place("calc.go", 3, 5, "func Add(a, b int) int {")])
        } else {
            nav_answer(
                (0..60)
                    .map(|i| place(&format!("pkg{i}.go"), i + 1, 2, "\tAdd(1, 2)"))
                    .collect(),
            )
        }
    });
    press(&mut world, Key::Right);
    type_key(&mut world, "f");
    type_key(&mut world, "s");
    let list = "tile.m.review.nav.list";
    assert!(!shows_inside(&world, list, "review.nav.place.59"));

    for (key, picked) in [
        (Key::PageDown, 20),
        (Key::PageDown, 40),
        (Key::Down, 41),
        (Key::PageDown, 59),
        (Key::PageUp, 39),
        (Key::PageUp, 19),
        (Key::Up, 18),
    ] {
        press(&mut world, key.clone());
        world.settle();
        assert!(
            shows_inside(&world, list, &format!("review.nav.place.{picked}")),
            "place {picked} after {key:?}"
        );
    }
    press(&mut world, Key::Enter);
    assert_eq!(
        file_reads(&world).last().map(String::as_str),
        Some("pkg18.go")
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

#[test]
fn labels_the_names_on_the_file_views_line_as_the_walkthrough_shows_it_and_asks_about_the_one_picked()
 {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    world.daemon.on("code_nav", |_| {
        nav_answer(vec![place("calc.go", 9, 1, "\treturn b - a")])
    });
    type_key(&mut world, "2");
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 4: \tsum := a + b")
    );

    type_key(&mut world, "f");
    assert_eq!(labels_on(&world), ["a", "s", "d"]);
    type_key(&mut world, "d");
    let asked = world.daemon.sent_of("code_nav");
    assert_eq!(
        (
            asked[0]["path"].clone(),
            asked[0]["line"].clone(),
            asked[0]["character"].clone()
        ),
        (json!("calc.go"), json!(4), json!(12))
    );
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("added line 9: \treturn b - a")
    );
}

#[test]
fn lists_the_uses_of_a_name_with_f_on_its_own_definition_and_opens_the_one_picked_from_thousands() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    world.daemon.on("code_nav", |r| {
        if r["kind"] == "definition" {
            nav_answer(vec![place("calc.go", 3, 5, "func Add(a, b int) int {")])
        } else {
            nav_answer(
                (0..3000)
                    .map(|i| place(&format!("pkg{i}.go"), i + 1, 2, &format!("\tAdd({i}, 1)")))
                    .collect(),
            )
        }
    });
    press(&mut world, Key::Right);
    type_key(&mut world, "f");
    let built = attn_app::places_built();
    type_key(&mut world, "s");
    let list = world
        .screen()
        .get(Target::role("dialog").name("Uses of Add"));
    let said: Vec<String> = world
        .screen()
        .descendants(Target::key(list.key.unwrap()))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(said.iter().any(|t| t == "3000"), "{said:?}");
    let built = attn_app::places_built() - built;
    assert!(built < 300, "{built} of 3000 places built");

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
    assert_eq!(
        file_cursor(&world).as_deref(),
        Some("unchanged line 1: // pkg1.go")
    );
}

#[test]
fn marks_the_asked_line_in_the_file_view_while_its_question_waits() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    world.daemon.on("code_nav", |_| ());
    type_key(&mut world, "2");
    type_key(&mut world, "f");
    type_key(&mut world, "d");
    let asked: Vec<String> = world
        .screen()
        .descendants(Target::key("tile.m.review.view"))
        .into_iter()
        .filter(|n| n.description.as_deref() == Some("asked, waiting for the answer"))
        .filter_map(|n| n.name)
        .collect();
    assert_eq!(asked, ["added line 4: \tsum := a + b"]);
    press(&mut world, Key::Escape);
    assert_eq!(asked_rows(&world), 0);
}

#[test]
fn says_in_plain_words_that_a_question_asked_while_the_daemon_is_unreachable_was_not_asked() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    press(&mut world, Key::Right);
    world.daemon.disconnect();
    world.settle();
    type_key(&mut world, "f");
    type_key(&mut world, "s");
    let note = world.screen().get(Target::key("review.nav.note"));
    assert_eq!(
        (note.role.as_str(), note.name.as_deref()),
        (
            "alert",
            Some("The daemon is not reachable, so nothing was asked. Ask again once it is back.")
        )
    );
    assert_eq!(asked_rows(&world), 0);
}

#[test]
fn opens_nothing_for_i_c_or_a_in_a_file_outside_the_repository_and_says_why() {
    let (mut world, _) = open_served(DOCUMENT, bundle());
    world.daemon.on("code_nav", |_| {
        nav_answer(vec![place(
            "/usr/lib/go/src/builtin/builtin.go",
            90,
            5,
            "type int int",
        )])
    });
    press(&mut world, Key::Right);
    type_key(&mut world, "f");
    type_key(&mut world, "g");
    let view = file_view(&world).unwrap();
    assert!(
        view.iter()
            .any(|t| t == "outside the repository · read-only"),
        "{view:?}"
    );
    let keys: Vec<String> = world
        .screen()
        .descendants(Target::key("tile.m.review.keys"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(!keys.iter().any(|k| k.ends_with(" edit")), "{keys:?}");

    for key in ["i", "c", "a"] {
        type_key(&mut world, key);
        assert!(world.screen().find(Target::role("dialog")).is_none());
        assert!(
            notices(&world).iter().any(|t| t
                == "/usr/lib/go/src/builtin/builtin.go is outside the repository, so it takes no edits or notes."),
            "{:?}",
            notices(&world)
        );
    }
    type_key(&mut world, "?");
    let sheet: Vec<String> = world
        .screen()
        .descendants(Target::role("dialog").name("Review keys"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(
        sheet
            .iter()
            .any(|t| t.starts_with("This file is outside the repository and read-only")),
        "{sheet:?}"
    );
}

#[test]
fn keeps_expanded_lines_reading_no_file_when_only_a_language_servers_state_changes_and_the_file_again_when_it_changes()
 {
    let starting = attn_protocol::ReviewNavigation {
        file: "calc.go".into(),
        ..server("go", "starting")
    };
    let ready = attn_protocol::ReviewNavigation {
        file: "calc.go".into(),
        ..server("go", "ready")
    };
    let (mut world, _) = open_served(DOCUMENT, with_servers(vec![starting]));
    let rows = |world: &World| {
        stop_lines(world, &stop_key(world, "calc.go"))
            .iter()
            .filter(|l| l.contains(" line "))
            .count()
    };
    press(&mut world, Key::Right);
    type_key(&mut world, "e");
    assert_eq!(rows(&world), 12);
    let reads = file_reads(&world).len();

    deliver(
        &mut world,
        DOCUMENT,
        Some(with_servers(vec![ready.clone()])),
    );
    assert!(
        servers(&world).iter().any(|t| t == "go ready"),
        "{:?}",
        servers(&world)
    );
    assert_eq!(rows(&world), 12);
    assert_eq!(file_reads(&world).len(), reads);

    deliver(
        &mut world,
        DOCUMENT,
        Some(ReviewBundle {
            head: "a1b2c3d".into(),
            ..with_servers(vec![ready.clone()])
        }),
    );
    assert_eq!(rows(&world), 12);
    let mut changed = with_servers(vec![ready]);
    changed.stops[0].file_hash = "h2".into();
    deliver(&mut world, DOCUMENT, Some(changed));
    assert_eq!(rows(&world), 12);
    assert_eq!(file_reads(&world).len(), reads + 1);
}

#[test]
fn sets_up_a_review_on_an_outpost_on_that_outpost_and_stops_offering_it_there_with_x() {
    let mut world = start();
    world.daemon.on(
        "code_nav_setup",
        |_| json!({"event": "code_nav_setup_result", "success": true, "session_id": "setup-1"}),
    );
    world.daemon.emit(DesktopTileContentMessage {
        desktop_id: DESKTOP.into(),
        tile_id: "m".into(),
        tile_kind: "markdown".into(),
        path: DOC.into(),
        host: Some("outpost-1".into()),
        content: DOCUMENT.into(),
        review: Some(with_servers(vec![server("go", "missing")])),
        ..DesktopTileContentMessage::new()
    });
    world.settle();
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));

    type_key(&mut world, "s");
    let setup = world.daemon.sent_of("code_nav_setup");
    assert_eq!(setup.len(), 1);
    assert_eq!(
        (
            setup[0]["host"].clone(),
            setup[0]["repository"].clone(),
            setup[0]["language"].clone()
        ),
        (json!("outpost-1"), json!("/repo"), json!("go"))
    );
    type_key(&mut world, "x");
    let set = world.daemon.sent_of("set_setting");
    assert_eq!(
        (set[0]["key"].clone(), set[0]["value"].clone()),
        (
            json!("code_review_setup_offer_outpost-1_/repo"),
            json!("off")
        )
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

const GUIDE_DOCUMENT: &str = "---
attn: review
frame: brief
repository: /repo
base: 4be1c0d
head: 9f3a7e2
---

# Explain the guide

The guide says how a review reads.

### docs/guide.md:1-9

- L3: Reworded.
";

/// docs/guide.md now: a heading, a reworded paragraph, a rewritten one, a list with a reworded
/// item, an added paragraph, a hard-wrapped paragraph with one word changed, and a paragraph
/// deleted before one kept as it was.
fn guide_now() -> Vec<String> {
    [
        "# Reviews",
        "",
        "Press `c` to leave a note on the line.",
        "",
        "Questions reach the agent at once and come back answered.",
        "",
        "- one",
        "- two",
        "- three",
        "",
        "A paragraph added at the end.",
        "",
        "Hard-wrapped lines",
        "keep their words",
        "when one of them changes.",
        "",
        "A paragraph kept as it was.",
    ]
    .map(str::to_owned)
    .to_vec()
}

/// The guide's rows against its base, with each line's head the line it is now.
fn guide_rows(now: &[String]) -> Vec<ReviewRow> {
    let row = |kind: &str, old: Option<i64>, n: Option<i64>, text: &str| ReviewRow {
        head: n,
        ..line(kind, old, n, text)
    };
    let mut rows = vec![
        row(" ", Some(1), Some(1), &now[0]),
        row(" ", Some(2), Some(2), &now[1]),
        row("-", Some(3), None, "Press `c` to write a note on a line."),
        row("+", None, Some(3), &now[2]),
        row(" ", Some(4), Some(4), &now[3]),
        row(
            "-",
            Some(5),
            None,
            "Nothing else is sent until you send the review.",
        ),
        row("+", None, Some(5), &now[4]),
    ];
    for (i, text) in now[5..].iter().enumerate() {
        let n = 6 + i as i64;
        match text.as_str() {
            "A paragraph kept as it was." => rows.extend([
                row("-", Some(n), None, "A paragraph that was deleted."),
                row("-", Some(n), None, ""),
                row(" ", Some(n), Some(n), text),
            ]),
            "keep their words" => rows.extend([
                row("-", Some(n), None, "keep all words"),
                row("+", None, Some(n), text),
            ]),
            "- two" => rows.extend([
                row("-", Some(n), None, "- to"),
                row("+", None, Some(n), text),
            ]),
            t if t.starts_with("A paragraph added") => rows.push(row("+", None, Some(n), text)),
            _ => rows.push(row(" ", Some(n), Some(n), text)),
        }
    }
    rows
}

fn guide_bundle(rows: Vec<ReviewRow>, lines_above: i64) -> ReviewBundle {
    let now_to = rows.iter().filter_map(|r| r.now).max().unwrap_or(0);
    ReviewBundle {
        frame: "brief".into(),
        repository: "/repo".into(),
        base: Some("4be1c0d".into()),
        head: "9f3a7e2".into(),
        stops: vec![ReviewStop {
            heading_line: 13 + lines_above,
            path: "docs/guide.md".into(),
            from: 1,
            to: 9,
            now_from: 1,
            now_to,
            file_hash: "h1".into(),
            rows,
            notes: vec![ReviewNote {
                line: 15 + lines_above,
                from: 3,
                to: 3,
                now_from: 3,
                now_to: 3,
                look: false,
                text: "Reworded.".into(),
            }],
            ..ReviewStop::default()
        }],
        files: vec![ReviewFile {
            path: "docs/guide.md".into(),
            status: "modified".into(),
            added: 2,
            removed: 2,
            ..ReviewFile::default()
        }],
        ..ReviewBundle::default()
    }
}

type Guide = std::sync::Arc<std::sync::Mutex<Vec<String>>>;

/// The guide's review open in its tile, with the file and its edits served from `guide` and
/// the user's marks kept. The stop shows `rows_of` the guide; the file reads whole.
fn open_guide() -> (World, Guide, Marks) {
    open_guide_with(guide_rows)
}

fn open_guide_with(rows_of: fn(&[String]) -> Vec<ReviewRow>) -> (World, Guide, Marks) {
    let mut world = start_with_session(Some("s-a"));
    let guide: Guide = std::sync::Arc::new(std::sync::Mutex::new(guide_now()));
    let version = std::sync::Arc::new(std::sync::atomic::AtomicUsize::new(1));
    let (read, v) = (guide.clone(), version.clone());
    world.daemon.on("review_file", move |r| {
        let hash = format!("h{}", v.load(std::sync::atomic::Ordering::SeqCst));
        json!({"event": "review_file_result", "success": true, "path": r["path"],
            "rows": guide_rows(&read.lock().unwrap()), "hash": hash})
    });
    let (written, v) = (guide.clone(), version.clone());
    world.daemon.on("review_edit", move |r| {
        let (from, to) = (r["from"].as_i64().unwrap() as usize, r["to"].as_i64().unwrap() as usize);
        let text = r["text"].as_str().unwrap_or_default();
        let mut lines = written.lock().unwrap();
        lines.splice(from - 1..to, text.split('\n').map(str::to_owned));
        let n = v.fetch_add(1, std::sync::atomic::Ordering::SeqCst) + 1;
        json!({"event": "review_edit_result", "success": true, "conflict": false, "hash": format!("h{n}")})
    });
    let (kept, _, _) = keep_drafts(&world);
    let rows = rows_of(&guide.lock().unwrap());
    deliver(&mut world, GUIDE_DOCUMENT, Some(guide_bundle(rows, 0)));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    (world, guide, kept)
}

/// The blocks of the document stop, as their names read.
fn guide_blocks(world: &World) -> Vec<(String, String)> {
    world
        .screen()
        .descendants(Target::key("tile.m"))
        .into_iter()
        .filter_map(|n| Some((n.name.filter(|s| s.contains(" block, line"))?, n.key?)))
        .collect()
}

fn to_block(world: &mut World, line: &str) {
    press(world, Key::Right);
    for _ in 0..12 {
        if cursor(world)
            .name
            .as_deref()
            .is_some_and(|n| n.ends_with(line))
        {
            return;
        }
        press(world, Key::Down);
    }
    panic!("no block {line}");
}

#[test]
fn renders_a_markdown_stop_as_a_document_with_a_light_rewording_s_words_and_a_rewrite_s_old_text_struck()
 {
    let (world, _, _) = open_guide();
    let blocks = guide_blocks(&world);
    let names: Vec<&str> = blocks.iter().map(|(n, _)| n.as_str()).collect();
    assert_eq!(
        names,
        [
            "unchanged block, line 1",
            "changed block, line 3",
            "changed block, line 5",
            "unchanged block, line 7",
            "changed block, line 8",
            "unchanged block, line 9",
            "changed block, line 11",
            "changed block, lines 13–15",
            "unchanged block, line 17",
        ]
    );
    let node = |key: String| world.screen().get(Target::key(key));
    let words = node(format!("{}.words", blocks[1].1));
    assert_eq!(
        (words.name.as_deref(), words.description.as_deref()),
        (
            Some("Press c to write leave a note on a the line."),
            Some("added: leave the; removed: write a")
        )
    );
    assert_eq!(
        node(format!("{}.words", blocks[7].1))
            .description
            .as_deref(),
        Some("added: their; removed: all")
    );
    let said = |key: String| {
        world
            .screen()
            .descendants(Target::key(key))
            .into_iter()
            .filter_map(|n| n.name)
            .fold(Vec::<String>::new(), |mut said, name| {
                if said.last() != Some(&name) {
                    said.push(name);
                }
                said
            })
            .join(" ")
    };
    assert_eq!(
        said(format!("{}.was", blocks[2].1)),
        "Nothing else is sent until you send the review."
    );
    assert_eq!(said(format!("{}.was", blocks[4].1)), "to");
    assert!(
        world
            .screen()
            .find(Target::key(format!("{}.was", blocks[6].1)))
            .is_none()
    );
    assert_eq!(
        said(format!("{}.deleted", blocks[8].1)),
        "A paragraph that was deleted."
    );
    let read = shown(&world);
    let at = |t: &str| {
        read.iter()
            .position(|s| s == t)
            .unwrap_or_else(|| panic!("{t:?} in {read:?}"))
    };
    assert!(at("Reworded.") > at("Press c to write leave a note on a the line."));
    assert!(at("Reworded.") < at("Nothing else is sent until you send the review."));
}

#[test]
fn edits_a_block_as_markdown_with_i_and_keeps_the_cursor_on_it_when_the_stop_and_the_document_change()
 {
    let (mut world, guide, kept) = open_guide();
    to_block(&mut world, "line 3");
    type_key(&mut world, "i");
    let shown_box = edit_box(&world).expect("the edit box opens");
    assert!(
        shown_box
            .iter()
            .any(|t| t == "Editing docs/guide.md: this block, as markdown"),
        "{shown_box:?}"
    );
    append(&mut world, " Then send.");
    press(&mut world, Key::Enter);
    assert!(edit_box(&world).is_some());
    append(&mut world, "It waits.");
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
    let edited = "Press `c` to leave a note on the line. Then send.\nIt waits.";
    assert_eq!(guide.lock().unwrap()[2..4].join("\n"), edited);
    let mark = kept
        .lock()
        .unwrap()
        .iter()
        .find(|m| m["type"] == "edit")
        .cloned()
        .expect("the edit is listed");
    assert_eq!(
        (
            mark["edit_now"].clone(),
            mark["anchor"]["file_start_line"].clone(),
            mark["anchor"]["file_end_line"].clone()
        ),
        (json!(edited), json!(3), json!(4))
    );

    let rows = guide_rows(&guide.lock().unwrap());
    let content = GUIDE_DOCUMENT.replace(
        "The guide says how a review reads.",
        "The guide says how a review reads.\n\nIt changed today.",
    );
    deliver(&mut world, &content, Some(guide_bundle(rows, 2)));
    assert!(shown(&world).iter().any(|t| t == "It changed today."));
    assert_eq!(
        cursor(&world).name.as_deref(),
        Some("changed block, lines 3–4")
    );
}

#[test]
fn writes_a_note_on_a_whole_block() {
    let (mut world, _, kept) = open_guide();
    to_block(&mut world, "line 5");
    type_key(&mut world, "c");
    write(&mut world, "Say when.");
    let marks = kept.lock().unwrap().clone();
    assert_eq!(marks.len(), 1, "{marks:?}");
    let anchor = &marks[0]["anchor"];
    assert_eq!(
        (
            marks[0]["type"].clone(),
            anchor["file"].clone(),
            anchor["file_start_line"].clone(),
            anchor["file_end_line"].clone(),
            anchor["start_line"].clone()
        ),
        (
            json!("comment"),
            json!("docs/guide.md"),
            json!(5),
            json!(5),
            json!(13)
        )
    );
    let read = shown(&world);
    let at = |t: &str| {
        read.iter()
            .position(|s| s.contains(t))
            .unwrap_or_else(|| panic!("{t:?} in {read:?}"))
    };
    assert!(at("Say when.") > at("Questions reach the agent"));
    assert!(at("Say when.") < at("one"));
}

#[test]
fn says_a_document_block_is_not_code_when_asked_about_a_name() {
    let (mut world, _, _) = open_guide();
    to_block(&mut world, "line 3");
    type_key(&mut world, "f");
    assert_eq!(
        nav_note(&world).as_deref(),
        Some("Put the cursor on a line of code to ask about it.")
    );
}

/// The guide's rows after two lines were added below its first line, past the review's head.
fn guide_grown_since_head(now: &[String]) -> Vec<ReviewRow> {
    let rows = guide_rows(now);
    let mut grown = vec![rows[0].clone()];
    grown.extend(
        ["", "Added since the head."]
            .iter()
            .zip(2..)
            .map(|(t, n)| line("+", None, Some(n), t)),
    );
    grown.extend(rows[1..].iter().cloned().map(|mut r| {
        r.now = r.now.map(|n| n + 2);
        r
    }));
    grown
}

#[test]
fn anchors_a_note_on_a_block_at_its_lines_at_head_when_the_file_gained_lines_above_it_since() {
    let (mut world, _, kept) = open_guide_with(guide_grown_since_head);
    to_block(&mut world, "line 5");
    assert!(
        shown(&world)
            .iter()
            .any(|t| t.starts_with("Press c to write leave")),
    );
    type_key(&mut world, "c");
    write(&mut world, "Say which line.");
    let marks = kept.lock().unwrap().clone();
    let anchor = &marks[0]["anchor"];
    assert_eq!(
        (
            anchor["file"].clone(),
            anchor["file_start_line"].clone(),
            anchor["file_end_line"].clone()
        ),
        (json!("docs/guide.md"), json!(3), json!(3))
    );
    let read = shown(&world);
    let at = |t: &str| read.iter().position(|s| s.contains(t)).unwrap();
    assert!(at("Say which line.") > at("Press c to write leave"));
    assert!(at("Say which line.") < at("Questions reach the agent"));
}

#[test]
fn shows_a_changed_link_destination_as_the_old_link_struck_and_the_new_one_added() {
    let (world, _, _) = open_guide_with(|_| {
        vec![
            line(" ", Some(1), Some(1), "# Links"),
            line(" ", Some(2), Some(2), ""),
            line("-", Some(3), None, "Read the [guide](old.md) first."),
            line("+", None, Some(3), "Read the [guide](new.md) first."),
        ]
    });
    let blocks = guide_blocks(&world);
    let words = world
        .screen()
        .get(Target::key(format!("{}.words", blocks[1].1)));
    assert_eq!(
        (words.name.as_deref(), words.description.as_deref()),
        (
            Some("Read the guide guide first."),
            Some("added: guide; removed: guide")
        )
    );
}

#[test]
fn opens_a_document_on_this_machine_in_the_editor_tile_rooted_at_the_repository() {
    let (mut world, _, _) = open_guide();
    world.daemon.on(
        "desktop_dock_tile",
        |r| json!({"event": "command_result", "request_id": r["request_id"], "success": true}),
    );
    to_block(&mut world, "line 5");
    type_key(&mut world, "o");
    click(&mut world, Target::key("tile.m.review.part.3.editor"));
    let docked = world.daemon.sent_of("desktop_dock_tile");
    assert_eq!(docked.len(), 2);
    for d in &docked {
        let params: serde_json::Value =
            serde_json::from_str(d["tile_params"].as_str().unwrap()).unwrap();
        assert_eq!(
            (d["tile_kind"].clone(), params),
            (
                json!("notebook"),
                json!({"root": "/repo", "path": "docs/guide.md"})
            )
        );
    }
}

#[test]
fn offers_no_editor_tile_for_a_document_on_an_outpost_and_reads_and_saves_its_edits_there() {
    let (mut world, guide, _) = open_guide();
    let rows = guide_rows(&guide.lock().unwrap());
    world.daemon.emit(DesktopTileContentMessage {
        desktop_id: DESKTOP.into(),
        tile_id: "m".into(),
        tile_kind: "markdown".into(),
        path: DOC.into(),
        host: Some("outpost-1".into()),
        content: GUIDE_DOCUMENT.into(),
        review: Some(guide_bundle(rows, 0)),
        ..DesktopTileContentMessage::new()
    });
    world.settle();
    assert!(
        world
            .screen()
            .find(Target::key("tile.m.review.part.3.editor"))
            .is_none()
    );
    to_block(&mut world, "line 5");
    type_key(&mut world, "o");
    assert!(world.daemon.sent_of("desktop_dock_tile").is_empty());

    type_key(&mut world, "i");
    assert!(edit_box(&world).is_some());
    append(&mut world, " Fast.");
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
    let reads: Vec<_> = world
        .daemon
        .sent_of("review_file")
        .iter()
        .map(|r| (r["host"].clone(), r["path"].clone()))
        .collect();
    assert!(
        reads.contains(&(json!("outpost-1"), json!("docs/guide.md"))),
        "{reads:?}"
    );
    let edit = world.daemon.sent_of("review_edit");
    assert_eq!(
        (
            edit[0]["host"].clone(),
            edit[0]["from"].clone(),
            edit[0]["to"].clone()
        ),
        (json!("outpost-1"), json!(5), json!(5))
    );
}

#[test]
fn shows_an_edit_and_a_note_under_a_block_at_the_first_row_of_its_stop() {
    let (mut world, guide, _) = open_guide_with(|now| guide_rows(now)[2..5].to_vec());
    to_block(&mut world, "line 3");
    type_key(&mut world, "i");
    append(&mut world, " Then send.");
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
    let rows = guide_rows(&guide.lock().unwrap())[2..5].to_vec();
    deliver(&mut world, GUIDE_DOCUMENT, Some(guide_bundle(rows, 0)));
    type_key(&mut world, "c");
    write(&mut world, "Why now?");

    let read = shown(&world);
    let at = |t: &str| read.iter().position(|s| s.contains(t));
    assert!(at("you edited").is_some(), "{read:?}");
    assert!(at("Why now?").is_some(), "{read:?}");
    assert!(
        world
            .screen()
            .find(Target::key("tile.m.review.moved"))
            .is_none(),
        "{read:?}"
    );
}

/// A review whose one stop is on `docs/guide.md` in `repository`: an unchanged paragraph with
/// a link to a file beside it and one to the web, a lightly reworded paragraph whose link is
/// unchanged, and an unchanged image beside the file.
fn linked_guide(repository: &str) -> ReviewBundle {
    let rows = vec![
        line(
            " ",
            Some(1),
            Some(1),
            "See [setup](setup.md) and [the site](https://example.test/site).",
        ),
        line(" ", Some(2), Some(2), ""),
        line(
            "-",
            Some(3),
            None,
            "Read the old [release notes](notes.md) first.",
        ),
        line(
            "+",
            None,
            Some(3),
            "Read the new [release notes](notes.md) first.",
        ),
        line(" ", Some(4), Some(4), ""),
        line(" ", Some(5), Some(5), "![chart](img/chart.png)"),
    ];
    ReviewBundle {
        frame: "brief".into(),
        repository: repository.into(),
        base: Some("4be1c0d".into()),
        head: "9f3a7e2".into(),
        stops: vec![ReviewStop {
            heading_line: 13,
            path: "docs/guide.md".into(),
            from: 1,
            to: 5,
            now_from: 1,
            now_to: 5,
            file_hash: "h1".into(),
            rows,
            ..ReviewStop::default()
        }],
        files: vec![file("docs/guide.md", 1, 1)],
        ..ReviewBundle::default()
    }
}

/// The review of `linked_guide` open in its tile, its repository a folder that holds the files
/// the guide links to.
fn open_linked_guide() -> (World, tempfile::TempDir) {
    let project = tempfile::tempdir().unwrap();
    let root = project.path().canonicalize().unwrap();
    std::fs::create_dir_all(root.join("docs")).unwrap();
    std::fs::write(root.join("docs/setup.md"), "# Setup").unwrap();
    std::fs::write(root.join("docs/notes.md"), "# Notes").unwrap();
    let mut world = start();
    deliver(
        &mut world,
        GUIDE_DOCUMENT,
        Some(linked_guide(&root.to_string_lossy())),
    );
    (world, project)
}

#[test]
fn a_markdown_stops_links_open_from_its_file() {
    let (mut world, project) = open_linked_guide();
    let root = project.path().canonicalize().unwrap();
    world.gesture(|app| app.click(Target::key("setup.md")));
    world.gesture(|app| app.click(Target::key("https://example.test/site")));
    assert_eq!(
        world.opened_links(),
        [
            format!("file://{}/docs/setup.md", root.display()),
            "https://example.test/site".into(),
        ]
    );
}

#[test]
fn a_link_in_a_reworded_block_of_a_markdown_stop_opens_from_its_file() {
    let (mut world, project) = open_linked_guide();
    let root = project.path().canonicalize().unwrap();
    world.gesture(|app| app.click(Target::key("notes.md")));
    assert_eq!(
        world.opened_links(),
        [format!("file://{}/docs/notes.md", root.display())]
    );
}

#[test]
fn a_markdown_stops_local_image_shows_from_beside_its_file() {
    let mut world = start();
    world.daemon.on("tile_asset_read", |r| {
        let found = r["path"] == "/repo/docs/img/chart.png";
        json!({
            "event": "tile_asset_read_result",
            "request_id": r["request_id"],
            "success": found,
            "data_uri": found.then_some("data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAQAAAADCAYAAAC09K7GAAAAEklEQVR4nGP4z8DwHxkzEBQAAGHtF+mRbBMEAAAAAElFTkSuQmCC"),
        })
    });
    deliver(&mut world, GUIDE_DOCUMENT, Some(linked_guide("/repo")));

    let read: Vec<_> = world
        .daemon
        .sent_of("tile_asset_read")
        .iter()
        .map(|r| r["path"].clone())
        .collect();
    assert_eq!(read, [json!("/repo/docs/img/chart.png")]);
    let pictures: Vec<_> = world
        .screen()
        .descendants(Target::key("tile.m"))
        .into_iter()
        .filter(|n| n.role == "image")
        .map(|n| n.name.unwrap_or_default())
        .collect();
    assert_eq!(pictures, ["chart"], "{}", world.screen());
}

/// The words of the place the document's cursor stands on.
fn cursor_words(world: &World) -> Vec<String> {
    let key = cursor(world).key.unwrap();
    world
        .screen()
        .descendants(Target::key(key))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn painted(world: &World, words: &str) -> bool {
    world
        .screen()
        .find(Target::role("button").name(words))
        .is_some()
}

#[test]
fn keeps_a_note_written_on_the_steps_prose_in_the_map_on_that_prose_in_the_brief() {
    let mut world = start_with_session(Some("s-a"));
    let (kept, _, _) = keep_drafts(&world);
    deliver(&mut world, STORY, Some(story()));
    world.gesture(|app| app.click(Target::key("tile.m.review.keys")));
    const STEP: &str = "The helper is new, and only Add calls it.";
    press(&mut world, Key::Right);
    for _ in 0..6 {
        if cursor_words(&world).iter().any(|w| w == STEP) {
            break;
        }
        press(&mut world, Key::Up);
    }
    assert!(cursor_words(&world).iter().any(|w| w == STEP));

    type_key(&mut world, "c");
    write(&mut world, "Why only Add?");
    let marks = kept.lock().unwrap().clone();
    assert_eq!(
        (
            marks[0]["type"].clone(),
            marks[0]["text"].clone(),
            marks[0]["anchor"]["exact"].clone()
        ),
        (json!("comment"), json!("Why only Add?"), json!(STEP))
    );
    assert!(painted(&world, STEP), "{}", world.screen());

    type_key(&mut world, "1");
    assert!(painted(&world, STEP));
    assert!(!painted(&world, "Add names the sum before it returns it."));
}

#[test]
fn keeps_an_answer_that_arrives_while_the_reader_still_holds_an_unsaved_copy_of_its_question() {
    let (mut world, kept, sends, generation) = open_marking_counted(presented());
    press(&mut world, Key::Down);
    type_key(&mut world, "a");
    write(&mut world, "Why name it?");
    let id = kept.lock().unwrap()[0]["id"].clone();
    assert_eq!(sends.lock().unwrap()[0]["only_ids"], json!([id]));
    let question = kept.lock().unwrap()[0].clone();
    assert_eq!(question["type"], "question");
    assert!(
        question["anchor"]["block_id"]
            .as_str()
            .is_some_and(|b| !b.is_empty()),
        "{question}"
    );

    kept.lock().unwrap()[0]["answer"] = json!("So the return reads as a value.");
    world.daemon.emit(DesktopTileContentMessage {
        desktop_id: DESKTOP.into(),
        tile_id: "m".into(),
        tile_kind: "markdown".into(),
        path: DOC.into(),
        content: DOCUMENT.replace("head: 9f3a7e2", "head: 1c2d3e4"),
        review: Some(presented()),
        annotations_generation: Some(
            generation.fetch_add(1, std::sync::atomic::Ordering::SeqCst) as i64 + 1,
        ),
        ..DesktopTileContentMessage::new()
    });
    world.settle();
    world.advance(std::time::Duration::from_secs(1));
    world.settle();
    click(&mut world, Target::key("tile.m.document.notes"));
    assert_eq!(
        kept.lock().unwrap()[0]["answer"],
        json!("So the return reads as a value.")
    );
    let screen = format!("{}", world.screen());
    assert!(
        screen.contains("So the return reads as a value."),
        "{screen}"
    );
}

#[test]
fn a_review_tiles_header_offers_the_review_with_no_notes_and_its_button_opens_the_send_sheet() {
    let (mut world, _, sends) = open_marking(presented());
    let review = world.screen().get(Target::key("tile.m.document.send"));
    assert_eq!(review.name.as_deref(), Some("Review to Agent a"));
    assert!(!review.is("disabled"), "a review sends with no notes");

    click(&mut world, Target::key("tile.m.document.send"));
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Send the review"))
            .is_some(),
        "the button opens the send sheet rather than sending"
    );
    assert!(sends.lock().unwrap().is_empty());
}

#[test]
fn a_document_shown_alone_among_others_tabs_each_and_a_tab_shows_its_document_alone() {
    let mut world = start_with_tiles(vec![
        document_tile("m", DOC, None),
        document_tile("n", "/notes/notes.md", None),
    ]);
    deliver(&mut world, DOCUMENT, Some(bundle()));
    let tabs = |world: &World| -> Vec<String> {
        world
            .screen()
            .all(Target::role("tab"))
            .into_iter()
            .filter(|n| {
                n.key
                    .as_deref()
                    .is_some_and(|k| k.starts_with("review.tab."))
            })
            .map(|n| {
                let name = n.name.clone().unwrap_or_default();
                if n.is("checked") {
                    format!("{name} *")
                } else {
                    name
                }
            })
            .collect()
    };
    assert!(
        tabs(&world).is_empty(),
        "no tabs while the documents share the row"
    );

    click(&mut world, Target::key("tile.m.focus"));
    assert_eq!(tabs(&world), ["review.md *", "notes.md"]);

    click(&mut world, Target::key("review.tab.n"));
    assert_eq!(tabs(&world), ["review.md", "notes.md *"]);
    assert!(world.screen().find(Target::key("tile.n")).is_some());
    assert!(
        world.screen().find(Target::key("tile.m")).is_none(),
        "the tab shows its document alone"
    );
}
