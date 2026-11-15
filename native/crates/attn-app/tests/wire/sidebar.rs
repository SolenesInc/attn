use std::collections::BTreeMap;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    Desktop, DesktopPane, InitialStateMessage, Profile, Session, SessionRegisteredMessage,
    SessionStateChangedMessage, SessionUnregisteredMessage, SessionsUpdatedMessage,
};
use attn_testkit::{Daemon, Target, World, key_event};
use rune::{KeyEvent, KeyPlatform};

pub(super) fn chord(action: Action) -> KeyEvent {
    match Keymap::from_setting(None, KeyPlatform::current()).binding(&action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    }
}

pub(super) fn session(id: &str, state: &str) -> Session {
    Session {
        id: id.into(),
        label: id.into(),
        agent: "claude".into(),
        state: state.into(),
        profile_id: "work".into(),
        ..Session::default()
    }
}

pub(super) fn pane(desktop: &str, pane: &str, session: &str) -> DesktopPane {
    DesktopPane {
        desktop_id: desktop.into(),
        pane_id: pane.into(),
        session_id: session.into(),
        kind: "agent".into(),
        ..DesktopPane::default()
    }
}

/// A desktop whose layout holds `panes` side by side, in that order.
pub(super) fn desktop(id: &str, order: &str, name: &str, panes: Vec<DesktopPane>) -> Desktop {
    let leaves: Vec<String> = panes
        .iter()
        .map(|p| format!(r#"{{"type":"pane","pane_id":"{}"}}"#, p.pane_id))
        .collect();
    let tree_json = match leaves.as_slice() {
        [] => String::new(),
        [one] => one.clone(),
        many => many[1..].iter().fold(many[0].clone(), |tree, leaf| {
            format!(
                r#"{{"type":"split","split_id":"s{order}{}","direction":"vertical","children":[{tree},{leaf}]}}"#,
                tree.len()
            )
        }),
    };
    Desktop {
        id: id.into(),
        name: name.into(),
        order_key: order.into(),
        active_pane_id: panes.first().map(|p| p.pane_id.clone()).unwrap_or_default(),
        panes,
        profile_id: "work".into(),
        tree_json,
        ..Desktop::default()
    }
}

pub(super) fn state(sessions: Vec<Session>, desktops: Vec<Desktop>) -> InitialStateMessage {
    InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: desktops.first().map(|d| d.id.clone()).unwrap_or_default(),
            ..Profile::default()
        }]),
        sessions: Some(sessions),
        desktops: Some(desktops),
        settings: Some(BTreeMap::new()),
        ..InitialStateMessage::new()
    }
}

fn two_desktops() -> InitialStateMessage {
    state(
        vec![
            session("side-a", "working"),
            session("side-b", "waiting_input"),
            session("side-c", "idle"),
        ],
        vec![
            desktop(
                "d1",
                "a",
                "",
                vec![pane("d1", "p1", "side-b"), pane("d1", "p2", "side-a")],
            ),
            desktop("d2", "b", "Review", vec![pane("d2", "p3", "side-c")]),
        ],
    )
}

/// The sidebar's rows top to bottom, as their accessible names.
fn rows(world: &World) -> Vec<String> {
    world
        .screen()
        .all(Target::role("button"))
        .into_iter()
        .filter(|n| {
            n.key.as_deref().is_some_and(|k| {
                k.starts_with("sidebar.")
                    && k != "sidebar.home"
                    && !k.ends_with(".actions")
                    && !k.ends_with(".close")
            })
        })
        .filter_map(|n| n.name)
        .collect()
}

#[test]
fn the_desktop_sidebar_lists_each_desktop_with_its_agents_in_layout_order() {
    let mut world = World::start(Daemon::scripted().state(two_desktops()));
    world.open_desktop();

    assert_eq!(
        rows(&world),
        [
            "Open Desktop 10",
            "Open side-b",
            "Open side-a",
            "Open Review",
            "Open side-c"
        ]
    );
    let screen = world.screen();
    assert_eq!(
        screen
            .get(Target::key("sidebar.session.side-b.state"))
            .name
            .as_deref(),
        Some("Claude · waiting input")
    );
    assert_eq!(
        screen
            .get(Target::key("sidebar.desktop.d1"))
            .value
            .as_deref(),
        Some("current")
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn clicking_an_agent_row_asks_the_daemon_to_show_that_agent() {
    let mut world = World::start(Daemon::scripted().state(two_desktops()));

    world.gesture(|app| app.click(Target::role("button").name("Open side-c")));

    let shown = world.daemon.sent_of("desktop_show_session");
    assert_eq!(shown.len(), 1);
    assert_eq!(shown[0]["session_id"], "side-c");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn clicking_a_desktop_row_makes_it_current() {
    let mut world = World::start(Daemon::scripted().state(two_desktops()));

    world.gesture(|app| app.click(Target::role("button").name("Open Review")));

    let set = world.daemon.sent_of("desktop_set_current");
    assert_eq!(set.len(), 1);
    assert_eq!(set[0]["desktop_id"], "d2");
    assert_eq!(set[0]["profile_id"], "work");
}

#[test]
fn agents_the_daemon_registers_changes_and_removes_after_the_hello_reach_the_sidebar() {
    let mut world = World::start(Daemon::scripted().state(two_desktops()));

    world.daemon.emit(SessionRegisteredMessage {
        session: session("side-d", "launching"),
        ..SessionRegisteredMessage::new()
    });
    world.settle();
    assert!(
        !rows(&world).contains(&"Open side-d".to_string()),
        "an agent on no desktop has no row in desktop flow"
    );
    let mut d2 = desktop(
        "d2",
        "b",
        "Review",
        vec![pane("d2", "p3", "side-c"), pane("d2", "p4", "side-d")],
    );
    d2.active_pane_id = "p3".into();
    world
        .daemon
        .emit(attn_protocol::ProfileArrangementChangedMessage {
            profile: Profile {
                id: "work".into(),
                name: "Work".into(),
                current_desktop_id: "d1".into(),
                ..Profile::default()
            },
            desktops: vec![
                desktop(
                    "d1",
                    "a",
                    "",
                    vec![pane("d1", "p1", "side-b"), pane("d1", "p2", "side-a")],
                ),
                d2,
            ],
            ..attn_protocol::ProfileArrangementChangedMessage::new()
        });
    world.settle();
    assert_eq!(rows(&world).last().map(String::as_str), Some("Open side-d"));

    world.daemon.emit(SessionStateChangedMessage {
        session: session("side-d", "pending_approval"),
        ..SessionStateChangedMessage::new()
    });
    world.settle();
    assert_eq!(
        world
            .screen()
            .get(Target::key("sidebar.session.side-d"))
            .value
            .as_deref(),
        Some("pending_approval")
    );

    world.daemon.emit(SessionUnregisteredMessage {
        session: session("side-d", "idle"),
        ..SessionUnregisteredMessage::new()
    });
    world.settle();
    assert!(!rows(&world).contains(&"Open side-d".to_string()));

    world.daemon.emit(SessionsUpdatedMessage {
        sessions: Some(vec![session("side-a", "working")]),
        ..SessionsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(
        rows(&world),
        ["Open Desktop 10", "Open side-a", "Open Review"]
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn one_agent_changing_state_rebuilds_only_its_row() {
    let mut world = World::start(Daemon::scripted().state(two_desktops()));
    let before = attn_app::sidebar::rows_built();

    world.daemon.emit(SessionStateChangedMessage {
        session: session("side-a", "waiting_input"),
        ..SessionStateChangedMessage::new()
    });
    world.settle();

    assert_eq!(attn_app::sidebar::rows_built() - before, 1);
    assert_eq!(
        world
            .screen()
            .get(Target::key("sidebar.session.side-a"))
            .value
            .as_deref(),
        Some("waiting_input")
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn home_shows_from_its_row_and_from_the_key_and_a_row_click_leaves_it() {
    let mut world = World::start(Daemon::scripted().state(two_desktops()));

    world.gesture(|app| app.click(Target::role("button").name("Home")));
    assert!(
        world
            .screen()
            .find(Target::role("main").name("Home"))
            .is_some()
    );
    assert!(
        world
            .screen()
            .find(Target::role("main").name("Tiles"))
            .is_none()
    );

    world.gesture(|app| app.click(Target::role("button").name("Open side-a")));
    assert!(
        world
            .screen()
            .find(Target::role("main").name("Tiles"))
            .is_some()
    );
    world.advance(std::time::Duration::from_secs(1));
    assert!(world.runtime_is_at_rest(), "the tiles' arrival settles");

    let home = chord(Action::SessionGoToDashboard);
    world.gesture(|app| app.key(home));
    assert_eq!(
        world
            .screen()
            .get(Target::key("sidebar.home"))
            .value
            .as_deref(),
        Some("current")
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn desktops_list_in_order_with_their_agents_and_tiles_in_layout_order() {
    let raw = |id: &str, order: &str, tree: &str, panes: Vec<DesktopPane>| Desktop {
        tree_json: tree.into(),
        ..desktop(id, order, id, panes)
    };
    let a_tree = r#"{"type":"split","split_id":"split-a","direction":"vertical","ratio":0.5,"children":[
        {"type":"pane","pane_id":"pane-a1"},
        {"type":"tile","tile_id":"tile-docs","tile_kind":"browser","tile_params":"https://docs.test"},
        {"type":"pane","pane_id":"pane-a2"}]}"#;
    let daemon = Daemon::scripted().state(state(
        ["c1", "a2", "a1", "unplaced", "b1"]
            .into_iter()
            .map(|id| session(id, "idle"))
            .collect(),
        vec![
            raw("c", "a2", r#"{"type":"pane","pane_id":"pc"}"#, vec![pane("c", "pc", "c1")]),
            raw(
                "a",
                "a0",
                a_tree,
                vec![pane("a", "pane-a1", "a1"), pane("a", "pane-a2", "a2")],
            ),
            raw("b", "a1", r#"{"type":"pane","pane_id":"pb"}"#, vec![pane("b", "pb", "b1")]),
            raw(
                "tile-only",
                "a3",
                r#"{"type":"tile","tile_id":"tile-browser","tile_kind":"browser","tile_params":"https://tiles.test"}"#,
                vec![],
            ),
            raw("empty", "a4", "", vec![]),
        ],
    ));
    let world = World::start(daemon);

    assert_eq!(
        rows(&world),
        [
            "Open a",
            "Open a1",
            "Open docs.test",
            "Open a2",
            "Open b",
            "Open b1",
            "Open c",
            "Open c1",
            "Open tile-only",
            "Open tiles.test",
            "Open empty",
        ]
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("sidebar.desktop.empty"))
            .value
            .as_deref(),
        Some("Empty desktop")
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn clicking_a_tile_row_asks_the_daemon_to_show_that_tile() {
    let tree = r#"{"type":"split","split_id":"s","direction":"vertical","children":[
        {"type":"pane","pane_id":"p1"},
        {"type":"tile","tile_id":"tile-docs","tile_kind":"browser","tile_params":"https://docs.test/a"}]}"#;
    let mut d1 = desktop("d1", "a", "", vec![pane("d1", "p1", "side-a")]);
    d1.tree_json = tree.into();
    let mut world =
        World::start(Daemon::scripted().state(state(vec![session("side-a", "idle")], vec![d1])));

    world.gesture(|app| app.click(Target::role("button").name("Open docs.test")));

    let shown = world.daemon.sent_of("desktop_show_leaf");
    assert_eq!(shown.len(), 1);
    assert_eq!(shown[0]["desktop_id"], "d1");
    assert_eq!(shown[0]["leaf_id"], "tile-docs");
}

fn three_desktops() -> InitialStateMessage {
    let mut state = two_desktops();
    state
        .desktops
        .get_or_insert_default()
        .push(desktop("d3", "c", "Notes", vec![]));
    state
}

#[test]
fn dragging_a_desktop_shows_where_it_lands_and_the_release_places_it_between_its_new_neighbours() {
    let mut world = World::start(Daemon::scripted().state(three_desktops()));
    world.open_desktop();
    let (rect, _) = world
        .runtime()
        .presented("sidebar.desktop.d1")
        .expect("the first desktop is drawn");
    let (x, y) = (rect[0] + rect[2] / 2.0, rect[1] + rect[3] / 2.0);
    let (below, _) = world
        .runtime()
        .presented("sidebar.desktop.d3")
        .expect("the last desktop is drawn");

    let opened = world.daemon.sent_of("desktop_set_current").len();

    world.gesture(|app| {
        app.runtime.pointer_down(x, y);
        app.runtime.pointer_move(x, y + 3.0);
    });
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.desktop.d3.seam"))
            .is_none(),
        "inside the threshold it is still a press"
    );
    world.gesture(|app| app.runtime.pointer_move(x, below[1] - rect[1] + y - 8.0));
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.desktop.d3.seam"))
            .is_some()
    );
    world.gesture(|app| app.runtime.pointer_up(x, below[1] - rect[1] + y - 8.0));

    let moved = world.daemon.sent_of("desktop_reorder");
    assert_eq!(moved.len(), 1);
    assert_eq!(moved[0]["desktop_id"], "d1");
    assert_eq!(moved[0]["previous_desktop_id"], "d2");
    assert_eq!(moved[0]["next_desktop_id"], "d3");
    assert_eq!(
        world.daemon.sent_of("desktop_set_current").len(),
        opened,
        "a drag is not a click"
    );
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.desktop.d3.seam"))
            .is_none()
    );
    let (last, _) = world
        .runtime()
        .presented("sidebar.desktop.d3")
        .expect("the last desktop is drawn");
    let (lx, ly) = (last[0] + last[2] / 2.0, last[1] + last[3] / 2.0);
    world.gesture(|app| {
        app.runtime.pointer_down(lx, ly);
        app.runtime.pointer_move(lx, rect[1] + 2.0);
        app.runtime.pointer_up(lx, rect[1] + 2.0);
    });
    let moved = world.daemon.sent_of("desktop_reorder");
    assert_eq!(
        (
            moved[1]["desktop_id"].as_str(),
            moved[1]["previous_desktop_id"].as_str(),
            moved[1]["next_desktop_id"].as_str()
        ),
        (Some("d3"), None, Some("d1")),
        "dragged up to the top"
    );
    world.advance(std::time::Duration::from_secs(1));
    assert!(
        world.runtime_is_at_rest(),
        "nothing keeps painting after the drop"
    );
}

#[test]
fn a_desktop_dropped_beside_itself_or_with_escape_stays_put() {
    let mut world = World::start(Daemon::scripted().state(three_desktops()));
    world.open_desktop();
    let (rect, _) = world
        .runtime()
        .presented("sidebar.desktop.d2")
        .expect("the desktop is drawn");
    let (x, y) = (rect[0] + rect[2] / 2.0, rect[1] + rect[3] / 2.0);

    world.gesture(|app| {
        app.runtime.pointer_down(x, y);
        app.runtime.pointer_move(x, y + 10.0);
        app.runtime.pointer_up(x, y + 10.0);
    });
    world.gesture(|app| {
        app.runtime.pointer_down(x, y);
        app.runtime.pointer_move(x, y - 200.0);
    });
    world.gesture(|app| app.key(super::queue::plain(rune::Key::Escape)));
    world.gesture(|app| app.runtime.pointer_up(x, y - 200.0));

    assert!(world.daemon.sent_of("desktop_reorder").is_empty());
}

#[test]
fn a_desktop_dragged_after_the_rows_above_it_changed_lands_where_the_pointer_shows() {
    let mut world = World::start(Daemon::scripted().state(two_desktops()));
    world.open_desktop();
    world
        .daemon
        .emit(attn_protocol::ProfileArrangementChangedMessage {
            profile: Profile {
                id: "work".into(),
                name: "Work".into(),
                current_desktop_id: "d1".into(),
                ..Profile::default()
            },
            desktops: vec![
                desktop("d1", "a", "", vec![pane("d1", "p1", "side-b")]),
                desktop("d2", "b", "Review", vec![pane("d2", "p3", "side-c")]),
            ],
            ..attn_protocol::ProfileArrangementChangedMessage::new()
        });
    world.settle();
    let (first, _) = world
        .runtime()
        .presented("sidebar.desktop.d1")
        .expect("the first desktop is drawn");
    let (dragged, _) = world
        .runtime()
        .presented("sidebar.desktop.d2")
        .expect("the second desktop is drawn");
    let (x, y) = (dragged[0] + dragged[2] / 2.0, dragged[1] + dragged[3] / 2.0);

    world.gesture(|app| {
        app.runtime.pointer_down(x, y);
        app.runtime.pointer_move(x, first[1] + 2.0);
        app.runtime.pointer_up(x, first[1] + 2.0);
    });

    let moved = world.daemon.sent_of("desktop_reorder");
    assert_eq!(moved.len(), 1, "{moved:?}");
    assert_eq!(
        (
            moved[0]["desktop_id"].as_str(),
            moved[0]["previous_desktop_id"].as_str(),
            moved[0]["next_desktop_id"].as_str()
        ),
        (Some("d2"), None, Some("d1"))
    );
}
