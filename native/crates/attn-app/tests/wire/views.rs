use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    Desktop, DesktopPane, DesktopTileContentMessage, InitialStateMessage, Profile, Session,
    ViewDocument, ViewProblem, ViewSource, ViewValues,
};
use attn_testkit::tree::{pane, row};
use attn_testkit::{Daemon, Target, WALL_START_MS, World, key_event};
use rune::{Key, KeyEvent, Mods};
use serde_json::{Value, json};

const DESKTOP: &str = "work/desktop_1";
const VIEW: &str = "/work/a/release/view.md";
const EVERY_STATIC_PART: &str =
    include_str!("../../../../../internal/viewlang/testdata/v1/views/every-static-part.json");
const COMMANDS: &str =
    include_str!("../../../../../internal/viewlang/testdata/v1/views/commands.json");
const QUESTION: &str =
    include_str!("../../../../../internal/viewlang/testdata/v1/views/question.json");

fn start() -> World {
    let tile = json!({
        "type": "tile",
        "tile_id": "m",
        "tile_kind": "markdown",
        "tile_params": json!({ "path": VIEW }).to_string(),
    });
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
            tree_json: row(vec![pane("a"), tile]).to_string(),
            active_pane_id: "m".into(),
            panes: vec![DesktopPane {
                desktop_id: DESKTOP.into(),
                pane_id: "a".into(),
                runtime_id: "r-a".into(),
                session_id: "s-a".into(),
                kind: "agent".into(),
                ..DesktopPane::default()
            }],
            revision: 1,
            ..Desktop::default()
        }]),
        ..InitialStateMessage::new()
    });
    let mut world = World::start_sized(daemon, (1600.0, 1400.0));
    world.open_desktop();
    world
}

fn release() -> Value {
    json!({
        "ci": {
            "state": "passed",
            "jobs": [
                {"conclusion": "success", "name": "build", "seconds": 161},
                {"conclusion": "failure", "name": "lint", "seconds": 18},
            ],
        },
        "head": {"sha": "0288d47", "subject": "Check a view", "at": "2026-11-28T10:00:00Z"},
        "landed": 3,
        "landings": [{"session": "views", "subject": "Draw a view", "seconds": 3840}],
        "hours": [{"hour": "09", "temp": 3}, {"hour": "10", "temp": 5}, {"hour": "11", "temp": 4}],
        "repositories": [{"name": "attn", "pulls": [{"title": "Fix the sidebar", "checks": "green"}]}],
    })
}

fn takes() -> Value {
    json!([
        {"name": "one", "file": "one.png", "seconds": 12},
        {"name": "two", "file": "two.png", "seconds": 75},
    ])
}

fn source(id: &str, data: bool) -> ViewSource {
    ViewSource {
        id: id.into(),
        kind: "file".into(),
        data,
        state: "ok".into(),
        ..ViewSource::default()
    }
}

/// The example view that uses every static part, as the daemon checks it, with its two files.
fn every_static_part() -> ViewDocument {
    let mut view = ViewDocument {
        language: 1,
        parts: parts_of(EVERY_STATIC_PART),
        ..ViewDocument::default()
    };
    view.sources = vec![source("release.json", true), source("takes.jsonl", false)];
    view.values = vec![
        ViewValues {
            source: "release.json".into(),
            values: release(),
        },
        ViewValues {
            source: "takes.jsonl".into(),
            values: takes(),
        },
    ];
    view
}

fn parts_of(checked: &str) -> Vec<attn_protocol::ViewPart> {
    let checked: Value = serde_json::from_str(checked).unwrap();
    serde_json::from_value(checked["parts"].clone()).unwrap()
}

fn show(world: &mut World, view: ViewDocument) {
    deliver(world, Some(view));
}

fn deliver(world: &mut World, view: Option<ViewDocument>) {
    world.daemon.emit(DesktopTileContentMessage {
        desktop_id: DESKTOP.into(),
        tile_id: "m".into(),
        tile_kind: "markdown".into(),
        path: VIEW.into(),
        content: "# Release 4.2".into(),
        view,
        ..DesktopTileContentMessage::new()
    });
    world.settle();
}

fn press_action(world: &mut World, action: &Action) {
    let ev = match Keymap::from_setting(None, attn_testkit::platform()).binding(action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    };
    world.gesture(|app| app.key(ev));
    world.settle();
}

/// The text the tile shows, as assistive tech reads it, in reading order.
fn shown(world: &World) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key("tile.m"))
        .into_iter()
        .flat_map(|n| [n.name, n.value])
        .flatten()
        .collect()
}

fn says(world: &World, text: &str) -> bool {
    shown(world).iter().any(|t| t.contains(text))
}

fn press(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
    world.settle();
}

fn typed(world: &mut World, text: &str) {
    world.gesture(|app| app.type_text(text));
    world.settle();
}

/// The key of the element the view's document says is current, which the highlight is on.
fn highlighted(world: &World) -> Option<String> {
    world
        .screen()
        .get(Target::key("tile.m.document"))
        .active_descendant
}

#[test]
fn a_view_draws_every_static_part_from_the_values_of_its_files() {
    let mut world = start();
    show(&mut world, every_static_part());
    let all = shown(&world);
    for drawn in [
        "Release 4.2",
        "passed",
        "Check a view",
        "Landed today",
        "Write the notes",
        "CI on main",
        "build",
        "2m 41s",
        "lint",
        "Draw a view",
        "1h 4m",
        "09: 3",
        "Before",
        "After",
        "Fix the sidebar",
        "1 of 2",
        "Take one runs 12s.",
    ] {
        assert!(
            all.iter().any(|t| t.contains(drawn)),
            "{drawn:?} is not drawn in {all:#?}"
        );
    }
    let refresh = world.screen().get(Target::key("tile.m.view.4.refresh"));
    assert!(refresh.is("disabled"), "{:?}", refresh.states);
    assert!(
        !all.iter().any(|t| t.contains('{')),
        "a field is left unfilled in {all:#?}"
    );
}

#[test]
fn a_part_with_a_problem_shows_it_in_its_place_while_the_others_draw() {
    let mut world = start();
    let mut view = every_static_part();
    let table = view.parts.iter_mut().find(|p| p.name == "table").unwrap();
    let problem = ViewProblem {
        line: table.line,
        text: "The table has 3 headers but 2 cells.".into(),
        fix: "Give each header a cell.".into(),
    };
    table.problem = Some(problem.clone());
    view.problems = vec![problem];
    show(&mut world, view);
    assert!(says(
        &world,
        "Line 33: The table has 3 headers but 2 cells. Give each header a cell."
    ));
    assert!(!says(&world, "Draw a view"));
    assert!(says(&world, "Fix the sidebar") && says(&world, "1 of 2") && says(&world, "build"));
}

#[test]
fn a_file_that_cannot_be_read_says_so_where_its_items_go() {
    let mut world = start();
    let mut view = every_static_part();
    view.values.retain(|v| v.source != "takes.jsonl");
    view.sources[1].state = "failed".into();
    view.sources[1].notice = Some("takes.jsonl: line 2 is not JSON".into());
    show(&mut world, view);
    assert!(says(&world, "takes.jsonl: line 2 is not JSON"));
    assert!(says(&world, "Fix the sidebar"));
}

#[test]
fn keys_move_the_highlight_fold_a_list_and_step_through_one_by_one() {
    let mut world = start();
    show(&mut world, every_static_part());
    const LIST: &str = "tile.m.view.8";

    typed(&mut world, "j");
    assert_eq!(
        highlighted(&world).as_deref(),
        Some(format!("{LIST}.head").as_str())
    );
    press(&mut world, Key::Enter);
    assert!(
        !says(&world, "lint"),
        "the folded list still shows its rows"
    );
    press(&mut world, Key::Enter);
    assert!(says(&world, "lint"));

    typed(&mut world, "j");
    assert_eq!(
        highlighted(&world).as_deref(),
        Some(format!("{LIST}.row.0").as_str())
    );
    press(&mut world, Key::Down);
    assert_eq!(
        highlighted(&world).as_deref(),
        Some(format!("{LIST}.row.1").as_str())
    );
    typed(&mut world, "k");
    assert_eq!(
        highlighted(&world).as_deref(),
        Some(format!("{LIST}.row.0").as_str())
    );

    for _ in 0..6 {
        typed(&mut world, "j");
    }
    assert_eq!(highlighted(&world).as_deref(), Some("tile.m.view.13"));
    press(&mut world, Key::Enter);
    assert!(says(&world, "2 of 2") && says(&world, "Take two runs 1m 15s."));
    press(&mut world, Key::Left);
    assert!(says(&world, "1 of 2 · kept"));
    press(&mut world, Key::Right);
    typed(&mut world, "s");
    assert!(says(&world, "All 2 seen: 1 kept, 1 skipped"));

    world.advance(Duration::from_secs(1));
    let now = world.now().as_secs_f64();
    let wake = world.runtime().wake_deadline().map(|at| at - now);
    assert!(
        !world.runtime().animating && wake.is_some_and(|s| s > 58.0),
        "the view repaints while nothing changes: it wakes in {wake:?} s"
    );
}

#[test]
fn a_views_ages_move_on_while_the_window_is_idle() {
    let mut world = start();
    let mut view = every_static_part();
    view.values[0].values["head"]["at"] = json!((WALL_START_MS - 30_000) / 1000);
    show(&mut world, view);
    assert!(says(&world, "just now"));
    world.advance(Duration::from_secs(61));
    assert!(says(&world, "2m ago"), "{:#?}", shown(&world));
}

#[test]
fn parts_attn_cannot_run_yet_say_so_in_their_place() {
    let mut world = start();
    let mut parts = parts_of(COMMANDS);
    parts.extend(parts_of(QUESTION));
    let view = ViewDocument {
        language: 1,
        parts,
        sources: vec![ViewSource {
            id: "scripts/main-status.sh".into(),
            kind: "run".into(),
            data: true,
            state: "unavailable".into(),
            ..ViewSource::default()
        }],
        ..ViewDocument::default()
    };
    show(&mut world, view);
    for said in [
        "run=\"scripts/main-status.sh\" is not available yet",
        "Running a view's own commands is not available yet",
        "Answering \"Which size should the icon be?\" in a view is not available yet",
    ] {
        assert!(
            says(&world, said),
            "{said:?} is not said in {:#?}",
            shown(&world)
        );
    }
}

#[test]
fn the_overview_shows_a_view_tile_by_its_card() {
    let mut world = start();
    show(&mut world, every_static_part());
    press_action(&mut world, &Action::DesktopOverview);
    let screen = world.screen();
    assert!(
        screen
            .find(Target::key("overview.miniature.m.card"))
            .is_some()
    );
    assert!(
        screen
            .find(Target::key("overview.miniature.m.picture"))
            .is_none()
    );
}

#[test]
fn the_find_chord_on_a_view_leaves_no_find_bar_behind() {
    let mut world = start();
    show(&mut world, every_static_part());
    press_action(&mut world, &Action::TerminalFind);
    deliver(&mut world, None);
    assert!(says(&world, "Release 4.2"));
    assert!(
        world
            .screen()
            .find(Target::key("tile.m.document.find"))
            .is_none()
    );
}
