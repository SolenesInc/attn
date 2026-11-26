use std::collections::BTreeMap;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    Desktop, DesktopPane, InitialStateMessage, Profile, Session, SessionRegisteredMessage,
    SessionStateChangedMessage, SessionUnregisteredMessage, SessionsUpdatedMessage,
};
use attn_testkit::{Daemon, Target, World, key_event};
use rune::KeyEvent;

pub(super) fn chord(action: Action) -> KeyEvent {
    match Keymap::from_setting(None, attn_testkit::platform()).binding(&action) {
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

/// The sidebar's desktop and agent rows top to bottom, as their accessible names.
fn rows(world: &World) -> Vec<String> {
    world
        .screen()
        .all(Target::role("button"))
        .into_iter()
        .filter(|n| {
            n.key.as_deref().is_some_and(|k| {
                k.starts_with("sidebar.")
                    && !["sidebar.home", "sidebar.overview"].contains(&k)
                    && !k.starts_with("sidebar.dock.")
                    && !k.ends_with(".actions")
                    && !k.ends_with(".close")
                    && !k.ends_with(".move")
                    && !k.ends_with(".rename")
                    && !k.ends_with(".settle")
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
        Some("Claude Code · waiting input")
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
    world.advance(std::time::Duration::from_millis(500));
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
            "Reload docs.test",
            "Open a2",
            "Open b",
            "Open b1",
            "Open c",
            "Open c1",
            "Open tile-only",
            "Open tiles.test",
            "Reload tiles.test",
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
    for desktop in ["sidebar.desktop.a", "sidebar.desktop.tile-only"] {
        assert_eq!(
            world.screen().get(Target::key(desktop)).value.as_deref(),
            Some(""),
            "{desktop} has something on it"
        );
    }
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_tile_row_shows_its_tile_and_its_close_button_takes_the_tile_off_its_desktop() {
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

    world.gesture(|app| app.click(Target::role("button").name("Close docs.test")));
    let removed = world.daemon.sent_of("desktop_remove_leaf");
    assert_eq!(removed.len(), 1, "{removed:?}");
    assert_eq!(removed[0]["desktop_id"], "d1");
    assert_eq!(removed[0]["leaf_id"], "tile-docs");
}

fn three_desktops() -> InitialStateMessage {
    let mut state = two_desktops();
    state
        .sessions
        .get_or_insert_default()
        .push(session("side-d", "idle"));
    state.desktops.get_or_insert_default().push(desktop(
        "d3",
        "c",
        "Notes",
        vec![pane("d3", "p4", "side-d")],
    ));
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

    let moved = world.daemon.sent_of("desktop_set_order");
    assert_eq!(moved.len(), 1);
    assert_eq!(
        moved[0]["desktop_ids"],
        serde_json::json!(["d2", "d1", "d3"])
    );
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
    let moved = world.daemon.sent_of("desktop_set_order");
    assert_eq!(
        moved[1]["desktop_ids"],
        serde_json::json!(["d3", "d1", "d2"]),
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

    assert!(world.daemon.sent_of("desktop_set_order").is_empty());
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

    let moved = world.daemon.sent_of("desktop_set_order");
    assert_eq!(moved.len(), 1, "{moved:?}");
    assert_eq!(moved[0]["desktop_ids"], serde_json::json!(["d2", "d1"]));
}

fn keys_of(action: Action) -> String {
    let keymap = Keymap::from_setting(None, attn_testkit::platform());
    keymap.label(keymap.binding(&action).unwrap())
}

fn dock_chips(world: &World) -> Vec<String> {
    let screen = world.screen();
    if screen.find(Target::key("sidebar.dock.chips")).is_none() {
        return Vec::new();
    }
    screen
        .children(Target::key("sidebar.dock.chips"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

#[test]
fn the_sidebar_goes_to_the_desktop_overview_and_docks_the_chosen_shortcuts() {
    let mut world = World::start(Daemon::scripted().state(state(
        vec![session("s1", "idle")],
        vec![desktop("d1", "a", "One", vec![pane("d1", "p1", "s1")])],
    )));
    let screen = world.screen();
    assert_eq!(
        screen.get(Target::key("sidebar.home")).description,
        Some(keys_of(Action::SessionGoToDashboard))
    );
    let overview = screen.get(Target::key("sidebar.overview"));
    assert_eq!(overview.name.as_deref(), Some("Desktop overview"));
    assert_eq!(overview.description, Some(keys_of(Action::DesktopOverview)));

    assert_eq!(
        dock_chips(&world),
        [format!("{} sidebar", keys_of(Action::SessionToggleSidebar))],
        "a chip for an agent's keys waits for an agent on screen"
    );
    world.gesture(|app| app.key(chord(Action::DesktopSelect1)));
    let docked = [
        format!("{} split v", keys_of(Action::TerminalSplitVertical)),
        format!("{} split h", keys_of(Action::TerminalSplitHorizontal)),
        format!("{} session h", keys_of(Action::SessionNewHorizontal)),
        format!("{} zoom", keys_of(Action::TerminalToggleZoom)),
        format!("{} sidebar", keys_of(Action::SessionToggleSidebar)),
    ];
    assert_eq!(dock_chips(&world), docked, "{}", world.screen());

    world
        .automate(
            "press",
            serde_json::json!({"target": {"key": "sidebar.dock.toggle"}}),
        )
        .unwrap();
    assert!(dock_chips(&world).is_empty());
    let written = world.daemon.sent_of("set_setting");
    let config: serde_json::Value =
        serde_json::from_str(written.last().unwrap()["value"].as_str().unwrap()).unwrap();
    assert_eq!(config["dock"]["collapsed"], true);
    assert_eq!(config["dock"]["items"].as_array().unwrap().len(), 5);
    world.gesture(|app| app.click(Target::role("button").name("Show dock")));
    assert_eq!(dock_chips(&world).len(), 5);

    world
        .automate(
            "press",
            serde_json::json!({"target": {"key": "sidebar.overview"}}),
        )
        .unwrap();
    assert!(world.screen().find(Target::key("overview")).is_some());
}

#[test]
fn home_hints_the_keys_the_user_bound_it_to() {
    let mut world = World::start(Daemon::scripted().state(state(
        vec![session("s1", "idle")],
        vec![desktop("d1", "a", "One", vec![pane("d1", "p1", "s1")])],
    )));
    let rebound =
        r#"{"version":1,"overrides":{"session.goToDashboard":{"key":"y","meta":true,"alt":true}}}"#;
    world.daemon.emit(attn_protocol::SettingsUpdatedMessage {
        settings: Some(BTreeMap::from([(
            "keybindings_config".to_owned(),
            rebound.to_owned(),
        )])),
        ..attn_protocol::SettingsUpdatedMessage::new()
    });
    world.settle();

    let keymap = Keymap::from_setting(Some(rebound), attn_testkit::platform());
    let bound = keymap.label(keymap.binding(&Action::SessionGoToDashboard).unwrap());
    assert_ne!(bound, keys_of(Action::SessionGoToDashboard));
    assert_eq!(
        world.screen().get(Target::key("sidebar.home")).description,
        Some(bound)
    );
}

#[test]
fn an_agent_row_leads_with_its_harness_logo_until_the_user_turns_logos_off() {
    let mut world = World::start(Daemon::scripted().state(state(
        vec![session("s1", "working")],
        vec![desktop("d1", "a", "One", vec![pane("d1", "p1", "s1")])],
    )));
    let lead = |world: &World| {
        let screen = world.screen();
        let node = screen.get(Target::key("sidebar.session.s1.state"));
        let [_, _, w, h] = screen
            .bounds(Target::key("sidebar.session.s1.state"))
            .unwrap();
        (node.name.unwrap_or_default(), w, h)
    };
    assert_eq!(lead(&world), ("Claude Code · working".into(), 16.0, 16.0));
    let name_x = |world: &World| {
        world
            .screen()
            .bounds(Target::key("sidebar.session.s1.name"))
            .unwrap()[0]
    };
    let with_logo = name_x(&world);

    world.daemon.emit(attn_protocol::SettingsUpdatedMessage {
        settings: Some(BTreeMap::from([(
            "sidebar_harness_logos_enabled".to_owned(),
            "false".to_owned(),
        )])),
        ..attn_protocol::SettingsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(lead(&world), ("Claude Code · working".into(), 8.0, 8.0));
    assert_eq!(
        name_x(&world),
        with_logo,
        "the dot sits in the logo's column, so the name stays put"
    );
}

#[test]
fn a_desktop_heading_shows_its_keys_and_its_pencil_renames_it_showing_a_refusal_in_place() {
    use super::desktop_close::{desktop, session, start};
    use std::sync::Arc;
    use std::sync::atomic::{AtomicBool, Ordering};

    let mut world = start(
        false,
        vec![session("agent", "claude")],
        desktop("work", 1, "Work", None, &["agent"]),
    );
    let keymap = Keymap::from_setting(None, attn_testkit::platform());
    let select = |action| keymap.label(keymap.binding(&action).unwrap());
    let heading = |world: &World, id: &str| {
        world
            .screen()
            .get(Target::key(format!("sidebar.desktop.{id}")))
            .description
    };
    assert_eq!(
        heading(&world, "work"),
        Some(select(Action::DesktopSelect1))
    );
    assert_eq!(
        heading(&world, "other"),
        Some(select(Action::DesktopSelect2))
    );

    let refuse = Arc::new(AtomicBool::new(true));
    let refusing = refuse.clone();
    world
        .daemon
        .on("desktop_rename", move |r: &attn_testkit::Request| {
            if refusing.load(Ordering::SeqCst) {
                serde_json::json!({
                    "event": "profile_action_result",
                    "request_id": r["request_id"],
                    "action": "desktop_rename",
                    "success": false,
                    "error": "desktop work changed since revision 1 while another window arranged it, so this name was not saved",
                })
            } else {
                serde_json::json!({
                    "event": "profile_action_result",
                    "request_id": r["request_id"],
                    "action": "desktop_rename",
                    "success": true,
                })
            }
        });
    let renamed = |world: &World| -> Vec<String> {
        world
            .daemon
            .sent_of("desktop_rename")
            .iter()
            .map(|c| {
                assert_eq!(c["desktop_id"], "work");
                c["name"].as_str().unwrap().to_owned()
            })
            .collect()
    };

    world.gesture(|app| app.click(Target::role("button").name("Rename Work")));
    world.settle();
    let dialog = world.screen().get(Target::key("desktop.rename"));
    assert_eq!(dialog.name.as_deref(), Some("Rename desktop"));
    let field = world.screen().get(Target::key("desktop.rename.field"));
    assert_eq!(field.value.as_deref(), Some("Work"));
    world.gesture(|app| app.type_text("Reviews"));
    world.gesture(|app| app.press("enter"));
    world.settle();
    assert_eq!(renamed(&world), ["Reviews"]);
    assert_eq!(
        world
            .screen()
            .get(Target::key("desktop.rename.refused"))
            .name
            .as_deref(),
        Some(
            "desktop work changed since revision 1 while another window arranged it, so this name was not saved"
        )
    );
    const TWELVE_POINT_LINE: f32 = 15.0;
    let inside = |outer: [f32; 4], inner: [f32; 4]| {
        inner[0] >= outer[0] && inner[0] + inner[2] <= outer[0] + outer[2] + 0.5
    };
    let popover = world
        .screen()
        .bounds(Target::key("desktop.rename"))
        .unwrap();
    let refused = world
        .screen()
        .bounds(Target::key("desktop.rename.refused"))
        .unwrap();
    assert!(
        inside(popover, refused) && refused[3] > 1.5 * TWELVE_POINT_LINE,
        "the refusal wraps onto more lines inside the popover: {refused:?} in {popover:?}"
    );

    let mut stops = Vec::new();
    for _ in 0..3 {
        world.gesture(|app| app.press("tab"));
        stops.push(world.screen().focused().and_then(|n| n.key));
    }
    assert_eq!(
        stops.last().cloned().flatten().as_deref(),
        Some("desktop.rename.field"),
        "Tab walks the popover's buttons and wraps to its field, never stopping on the popover: {stops:?}"
    );

    refuse.store(false, Ordering::SeqCst);
    world.gesture(|app| app.press("enter"));
    world.settle();
    assert_eq!(renamed(&world), ["Reviews", "Reviews"]);
    assert!(world.screen().find(Target::key("desktop.rename")).is_none());

    world.gesture(|app| app.click(Target::role("button").name("Rename Work")));
    world.settle();
    world.gesture(|app| app.press("backspace"));
    world.gesture(|app| app.press("enter"));
    world.settle();
    assert_eq!(renamed(&world), ["Reviews", "Reviews", ""]);
    assert!(world.screen().find(Target::key("desktop.rename")).is_none());
}

#[test]
fn an_agent_row_names_its_pull_request() {
    use attn_protocol::SessionPullRequest;
    let pr =
        |number: i64, state: &str, ci: Option<&str>, review: Option<&str>| SessionPullRequest {
            repository: "github.com/victorarias/attn".into(),
            number,
            state: state.into(),
            ci_status: ci.map(Into::into),
            review_status: review.map(Into::into),
            created_at: "2026-08-30T12:00:00Z".into(),
            ..SessionPullRequest::default()
        };
    let cases: Vec<(Vec<SessionPullRequest>, Option<&str>)> = vec![
        (
            vec![pr(71, "open", Some("failure"), None)],
            Some("github.com/victorarias/attn#71 · checks failed"),
        ),
        (
            vec![pr(71, "open", None, Some("changes_requested"))],
            Some("github.com/victorarias/attn#71 · changes requested"),
        ),
        (
            vec![pr(72, "open", None, None), pr(71, "merged", None, None)],
            Some("github.com/victorarias/attn#72 · open"),
        ),
        (
            vec![pr(71, "merged", None, None)],
            Some("github.com/victorarias/attn#71 · merged"),
        ),
        (
            vec![pr(71, "open", Some("pending"), None)],
            Some("github.com/victorarias/attn#71 · checks running"),
        ),
        (vec![pr(71, "closed", None, None)], None),
        (vec![], None),
    ];
    for (pull_requests, expected) in cases {
        let agent = Session {
            pull_requests: Some(pull_requests),
            ..session("s1", "idle")
        };
        let world = World::start(Daemon::scripted().state(state(
            vec![agent],
            vec![desktop("d1", "a", "Work", vec![pane("d1", "p1", "s1")])],
        )));
        let badge = world.screen().find(Target::key("sidebar.session.s1.pr"));
        assert_eq!(
            badge.as_ref().and_then(|n| n.name.as_deref()),
            expected,
            "{expected:?}"
        );
        if expected.is_some() {
            assert_eq!(
                badge.unwrap().value.as_deref().map(|v| v.starts_with('#')),
                Some(true)
            );
        }
    }
}

#[test]
fn each_agent_row_names_its_harness_with_fallbacks_for_a_plugin_and_a_missing_one_and_its_state() {
    let rows = [
        ("claude", "claude", "Claude Code", "scheduled"),
        ("codex", "codex", "Codex", "idle"),
        ("sigma", "sigma", "Sigma", "idle"),
        ("shell", "shell", "Shell", "idle"),
        ("plugin", "custom-driver", "Custom Driver", "idle"),
        ("missing", "", "Unknown harness", "idle"),
    ];
    let sessions = rows
        .iter()
        .map(|(id, agent, _, state)| Session {
            agent: (*agent).into(),
            ..session(id, state)
        })
        .collect();
    let panes = rows
        .iter()
        .enumerate()
        .map(|(i, (id, ..))| pane("d1", &format!("p{i}"), id))
        .collect();
    let world = World::start(
        Daemon::scripted().state(state(sessions, vec![desktop("d1", "a", "Work", panes)])),
    );

    let screen = world.screen();
    for (id, _, harness, state) in rows {
        let row = format!("sidebar.session.{id}");
        assert_eq!(
            screen.get(Target::key(format!("{row}.state"))).name,
            Some(format!("{harness} · {state}")),
        );
        assert_eq!(
            screen.get(Target::key(&row)).description.as_deref(),
            Some(harness)
        );
    }
}

#[test]
fn an_agent_row_says_its_harness_on_hover_and_explains_an_unknown_state_the_daemon_gave_a_reason_for()
 {
    let cases = [
        (
            "stuck",
            "Claude Code · Stuck — the agent has stopped reporting anything at all",
        ),
        ("no_evidence", "Claude Code · No signal from this agent yet"),
        ("some_future_clause", "Claude Code · unknown"),
    ];
    for (reason, said) in cases {
        let agent = Session {
            state_reason: Some(reason.into()),
            ..session("s1", "unknown")
        };
        let world = World::start(Daemon::scripted().state(state(
            vec![agent],
            vec![desktop("d1", "a", "Work", vec![pane("d1", "p1", "s1")])],
        )));
        let screen = world.screen();
        assert_eq!(
            screen
                .get(Target::key("sidebar.session.s1.state"))
                .name
                .as_deref(),
            Some(said)
        );
        assert_eq!(
            screen
                .get(Target::key("sidebar.session.s1"))
                .description
                .as_deref(),
            Some(said.replace(" · unknown", "").as_str())
        );
    }
}

#[test]
fn a_run_row_with_a_long_name_and_definition_keeps_room_for_the_name_and_shows_its_pull_request_number()
 {
    use attn_protocol::{AutomationProvenance, PullRequestProvenance};
    let mut run = session("run-a", "idle");
    run.label = "Review the incoming pull request for the feed service".into();
    run.automation = Some(AutomationProvenance {
        run_id: "run-1".into(),
        definition_id: 1,
        definition_name: "Review incoming pull requests across every repository - GPT Sol high"
            .into(),
        trigger_type: "github_review_requested".into(),
        pull_request: Some(PullRequestProvenance {
            repository: "ghe.example.net/audiobook/feed-nexus-web".into(),
            number: 4182,
            url: "https://ghe.example.net/audiobook/feed-nexus-web/pull/4182".into(),
            ..PullRequestProvenance::default()
        }),
        ..AutomationProvenance::default()
    });
    let mut world = World::start(Daemon::scripted().state(state(
        vec![run],
        vec![desktop("d1", "a", "One", vec![pane("d1", "p1", "run-a")])],
    )));
    world.gesture(|app| app.click(Target::key("sidebar.automations.finished")));
    let screen = world.screen();
    let bounds = |key: &str| screen.bounds(Target::key(key)).expect(key);
    let [row_x, _, row_w, _] = bounds("sidebar.session.run-a");
    let [name_x, _, name_w, _] = bounds("sidebar.session.run-a.name");
    let [number_x, _, number_w, _] = bounds("sidebar.session.run-a.run.number");
    assert!(
        name_w >= 4.0 * 13.0,
        "the name keeps room for a few letters: {name_w}"
    );
    assert!(number_x >= name_x + name_w, "the number follows the name");
    assert!(
        number_x + number_w <= row_x + row_w,
        "#4182 shows inside the row: {number_x}+{number_w} past {row_x}+{row_w}"
    );
}

#[test]
fn an_automation_run_row_names_its_definition_and_pull_request_and_settles_when_it_needs_the_user()
{
    use attn_protocol::{AutomationProvenance, PullRequestProvenance};
    let review = |run: &str| AutomationProvenance {
        run_id: run.into(),
        definition_id: 1,
        definition_name: "Requested PR review - GPT Sol medium".into(),
        trigger_type: "github_review_requested".into(),
        pull_request: Some(PullRequestProvenance {
            repository: "ghe.example.net/audiobook/feed-nexus-web".into(),
            number: 101,
            url: "https://ghe.example.net/audiobook/feed-nexus-web/pull/101".into(),
            title: Some("Fix validation race".into()),
            ..PullRequestProvenance::default()
        }),
        ..AutomationProvenance::default()
    };
    let mut world = World::start(Daemon::scripted().state(state(
        vec![
            session("manual", "idle"),
            Session {
                automation: Some(review("run-1")),
                ..session("run-a", "idle")
            },
            Session {
                automation: Some(review("run-2")),
                turn_owed: Some(true),
                ..session("run-b", "waiting_input")
            },
        ],
        vec![desktop(
            "testing",
            "a",
            "Testing automations",
            vec![
                pane("testing", "p1", "manual"),
                pane("testing", "p2", "run-a"),
                pane("testing", "p3", "run-b"),
            ],
        )],
    )));
    assert_eq!(
        rows(&world),
        [
            "Open Testing automations",
            "Open manual",
            "Open run-a",
            "Open run-b"
        ]
    );
    let run = world.screen().get(Target::key("sidebar.session.run-a.run"));
    assert_eq!(
        run.name.as_deref(),
        Some(
            "Automation: Requested PR review - GPT Sol medium · ghe.example.net/audiobook/feed-nexus-web#101 · Fix validation race"
        )
    );
    assert_eq!(run.value.as_deref(), Some("GPT Sol medium #101"));
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.session.manual.run"))
            .is_none()
    );
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.session.run-a.settle"))
            .is_none()
    );
    world.gesture(|app| app.click(Target::key("sidebar.session.run-b.settle")));
    let settled = world.daemon.sent_of("settle_turn");
    assert_eq!(settled.len(), 1);
    assert_eq!(settled[0]["session_id"], "run-b");

    world.gesture(|app| app.key(chord(Action::UiActionMenu)));
    let agents: Vec<String> = world
        .screen()
        .all(Target::role("list-box-option"))
        .into_iter()
        .filter_map(|n| n.key?.strip_prefix("palette.row.agent:").map(str::to_owned))
        .collect();
    assert_eq!(agents, ["manual", "run-a", "run-b"]);
}

fn source_and_target() -> World {
    let world = World::start(Daemon::scripted().state(state(
        vec![
            session("s1", "idle"),
            session("s2", "idle"),
            session("t1", "idle"),
        ],
        vec![
            desktop(
                "source",
                "a",
                "Source",
                vec![
                    pane("source", "pane-s1", "s1"),
                    pane("source", "pane-s2", "s2"),
                ],
            ),
            desktop(
                "target",
                "b",
                "Target",
                vec![pane("target", "pane-t1", "t1")],
            ),
        ],
    )));
    world
        .daemon
        .on("desktop_move_leaf", |r: &attn_testkit::Request| {
            serde_json::json!({
                "event": "profile_action_result",
                "request_id": r["request_id"],
                "action": "desktop_move_leaf",
                "success": true,
            })
        });
    world
}

fn centre_of(world: &World, key: &str) -> (f32, f32) {
    let [x, y, w, h] = world.screen().bounds(Target::key(key)).expect("it shows");
    (x + w / 2.0, y + h / 2.0)
}

#[test]
fn an_agent_row_dragged_onto_another_desktop_moves_there_and_its_own_desktop_takes_no_drop() {
    let mut world = source_and_target();

    let own = centre_of(&world, "sidebar.desktop.source");
    world.gesture(|app| app.drag_over(Target::key("sidebar.session.s2"), own));
    assert_eq!(
        world
            .screen()
            .get(Target::key("drag.ghost"))
            .name
            .as_deref(),
        Some("s2")
    );
    world.gesture(|app| app.release(own));
    assert!(world.daemon.sent_of("desktop_move_leaf").is_empty());
    assert!(world.screen().find(Target::key("drag.ghost")).is_none());

    let other = centre_of(&world, "sidebar.desktop.target");
    world.gesture(|app| app.drag(Target::key("sidebar.session.s2"), other));
    let moved = world.daemon.sent_of("desktop_move_leaf");
    assert_eq!(moved.len(), 1, "{moved:?}");
    assert_eq!(moved[0]["source_desktop_id"], "source");
    assert_eq!(moved[0]["leaf_id"], "pane-s2");
    assert_eq!(moved[0]["target_desktop_id"], "target");
}

#[test]
fn a_press_on_an_agent_row_that_barely_moves_opens_the_agent() {
    let mut world = source_and_target();
    let (x, y) = centre_of(&world, "sidebar.session.t1");
    world.gesture(|app| {
        app.drag_from((x, y), (x + 1.0, y + 2.0));
        app.release((x + 1.0, y + 2.0));
    });
    assert!(world.daemon.sent_of("desktop_move_leaf").is_empty());
    let shown = world.daemon.sent_of("desktop_show_session");
    assert_eq!(shown.len(), 1, "{shown:?}");
    assert_eq!(shown[0]["session_id"], "t1");
}

fn desktops_with_markdown_tile() -> World {
    let mut d1 = desktop(
        "work/desktop_1",
        "a",
        "",
        vec![pane("work/desktop_1", "pane-s1", "s1")],
    );
    d1.shortcut_slot = Some(1);
    d1.tree_json = r#"{"type":"split","split_id":"s-1","direction":"vertical","children":[{"type":"pane","pane_id":"pane-s1"},{"type":"tile","tile_id":"md-1","tile_kind":"markdown","tile_params":"/tmp/notes.md"}]}"#.into();
    let mut d2 = desktop(
        "work/desktop_2",
        "b",
        "Review",
        vec![pane("work/desktop_2", "pane-t1", "t1")],
    );
    d2.shortcut_slot = Some(2);
    let world = World::start(Daemon::scripted().state(state(
        vec![session("s1", "idle"), session("t1", "idle")],
        vec![d1, d2],
    )));
    world
        .daemon
        .on("desktop_move_leaf", |r: &attn_testkit::Request| {
            serde_json::json!({
                "event": "profile_action_result",
                "request_id": r["request_id"],
                "action": "desktop_move_leaf",
                "success": true,
            })
        });
    world
}

#[test]
fn a_tile_row_dragged_onto_another_desktop_moves_there_even_with_slashed_desktop_ids() {
    let mut world = desktops_with_markdown_tile();
    let tile_row = "sidebar.tile.work/desktop_1.md-1";

    let own = centre_of(&world, "sidebar.desktop.work/desktop_1");
    world.gesture(|app| app.drag_over(Target::key(tile_row), own));
    assert_eq!(
        world
            .screen()
            .get(Target::key("drag.ghost"))
            .name
            .as_deref(),
        Some("notes.md")
    );
    world.gesture(|app| app.release(own));
    assert!(world.daemon.sent_of("desktop_move_leaf").is_empty());
    assert!(world.screen().find(Target::key("drag.ghost")).is_none());

    let other = centre_of(&world, "sidebar.desktop.work/desktop_2");
    world.gesture(|app| app.drag(Target::key(tile_row), other));
    let moved = world.daemon.sent_of("desktop_move_leaf");
    assert_eq!(moved.len(), 1, "{moved:?}");
    assert_eq!(moved[0]["source_desktop_id"], "work/desktop_1");
    assert_eq!(moved[0]["leaf_id"], "md-1");
    assert_eq!(moved[0]["target_desktop_id"], "work/desktop_2");

    let (x, y) = centre_of(&world, tile_row);
    world.gesture(|app| app.drag_over(Target::key(tile_row), (x + 12.0, y)));
    let new_drop = centre_of(&world, "sidebar.new_desktop");
    world.gesture(|app| app.move_to(new_drop));
    world.gesture(|app| app.release(new_drop));
    let moved = world.daemon.sent_of("desktop_move_leaf");
    assert_eq!(moved.len(), 2, "{moved:?}");
    assert_eq!(moved[1]["source_desktop_id"], "work/desktop_1");
    assert_eq!(moved[1]["leaf_id"], "md-1");
    assert!(moved[1].get("target_desktop_id").is_none());

    let (x, y) = centre_of(&world, tile_row);
    world.gesture(|app| {
        app.drag_from((x, y), (x + 1.0, y + 2.0));
        app.release((x + 1.0, y + 2.0));
    });
    assert_eq!(world.daemon.sent_of("desktop_move_leaf").len(), 2);
    let shown = world.daemon.sent_of("desktop_show_leaf");
    assert_eq!(shown.len(), 1, "{shown:?}");
    assert_eq!(shown[0]["desktop_id"], "work/desktop_1");
    assert_eq!(shown[0]["leaf_id"], "md-1");
    world.advance(std::time::Duration::from_secs(6));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn moving_a_tile_or_agent_from_the_sidebar_action_picks_a_desktop_and_moves_it() {
    let mut world = desktops_with_markdown_tile();
    let tile_row = "sidebar.tile.work/desktop_1.md-1";
    let move_btn = "sidebar.tile.work/desktop_1.md-1.move";

    let tile_at = centre_of(&world, tile_row);
    world.gesture(|app| app.move_to(tile_at));
    world.gesture(|app| app.click(Target::key(move_btn)));
    assert_eq!(
        world
            .screen()
            .get(Target::key("palette.dialog"))
            .name
            .as_deref(),
        Some("Move notes.md to desktop")
    );
    world.gesture(|app| app.type_text("rev"));
    world.gesture(|app| app.key(rune::Key::Enter.into()));
    assert!(world.screen().find(Target::key("palette.dialog")).is_none());
    let moved = world.daemon.sent_of("desktop_move_leaf");
    assert_eq!(moved.len(), 1, "{moved:?}");
    assert_eq!(moved[0]["source_desktop_id"], "work/desktop_1");
    assert_eq!(moved[0]["leaf_id"], "md-1");
    assert_eq!(moved[0]["target_desktop_id"], "work/desktop_2");

    let tile_at = centre_of(&world, tile_row);
    world.gesture(|app| app.move_to(tile_at));
    world.gesture(|app| app.click(Target::key(move_btn)));
    world.gesture(|app| app.key(rune::Key::Escape.into()));
    assert!(world.screen().find(Target::key("palette.dialog")).is_none());

    let session_at = centre_of(&world, "sidebar.session.s1");
    world.gesture(|app| app.move_to(session_at));
    world.gesture(|app| app.click(Target::key("sidebar.session.s1.actions")));
    world.gesture(|app| app.click(Target::key("session.menu.move")));
    assert_eq!(
        world
            .screen()
            .get(Target::key("palette.dialog"))
            .name
            .as_deref(),
        Some("Move s1 to desktop")
    );
    world.gesture(|app| app.click(Target::key("palette.row.move-desktop:new")));
    let moved = world.daemon.sent_of("desktop_move_leaf");
    assert_eq!(moved.len(), 2, "{moved:?}");
    assert_eq!(moved[1]["source_desktop_id"], "work/desktop_1");
    assert_eq!(moved[1]["leaf_id"], "pane-s1");
    assert!(moved[1].get("target_desktop_id").is_none());
    world.advance(std::time::Duration::from_secs(6));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn moving_a_shared_agent_from_its_row_on_another_desktop_moves_that_rows_tile() {
    let on = |id: &str, order: &str, name: &str, leaf: &str| Desktop {
        tree_json: format!(r#"{{"type":"pane","pane_id":"{leaf}"}}"#),
        active_pane_id: leaf.into(),
        ..desktop(id, order, name, vec![pane(id, leaf, "shared")])
    };
    let mut world = World::start(Daemon::scripted().state(state(
        vec![session("shared", "idle")],
        vec![
            on("work/desktop_1", "a", "First", "first-leaf"),
            on("work/desktop_2", "b", "Second", "second-leaf"),
            desktop("work/desktop_3", "c", "Third", vec![]),
        ],
    )));
    world
        .daemon
        .on("desktop_move_leaf", |r: &attn_testkit::Request| {
            serde_json::json!({
                "event": "profile_action_result",
                "request_id": r["request_id"],
                "action": "desktop_move_leaf",
                "success": true,
            })
        });
    let second_row = second_of(&world, Target::role("button").name("Open shared"));
    let at = centre_of(&world, &second_row);
    world.gesture(|app| app.move_to(at));
    let actions = button_in(&world, &second_row, "sidebar.session.shared.actions");
    world.gesture(|app| app.click_at(actions));
    world.gesture(|app| app.click(Target::key("session.menu.move")));
    let offered: Vec<String> = world
        .screen()
        .all(Target::role("list-box-option"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert_eq!(offered, ["First", "Third", "+ New desktop"]);
    world.gesture(|app| app.click(Target::key("palette.row.move-desktop:work/desktop_3")));

    let moved = world.daemon.sent_of("desktop_move_leaf");
    assert_eq!(moved.len(), 1, "{moved:?}");
    assert_eq!(moved[0]["source_desktop_id"], "work/desktop_2");
    assert_eq!(moved[0]["leaf_id"], "second-leaf");
    assert_eq!(moved[0]["target_desktop_id"], "work/desktop_3");
    world.advance(std::time::Duration::from_secs(6));
    assert!(world.runtime_is_at_rest());
}

fn button_in(world: &World, row: &str, button: &str) -> (f32, f32) {
    let [rx, ry, rw, rh] = world.screen().bounds(Target::key(row)).expect("it shows");
    world
        .runtime()
        .elements()
        .into_iter()
        .filter(|e| e.key.as_deref() == Some(button))
        .map(|e| {
            let [x, y, w, h] = e.rect;
            (x + w / 2.0, y + h / 2.0)
        })
        .find(|&(x, y)| x > rx && x < rx + rw && y > ry && y < ry + rh)
        .expect("the row shows the button")
}

fn second_of(world: &World, target: Target) -> String {
    let found: Vec<String> = world
        .screen()
        .all(target.clone())
        .into_iter()
        .filter_map(|n| n.key)
        .filter(|k| k.starts_with("sidebar.session."))
        .collect();
    assert_eq!(
        found.len(),
        2,
        "{target:?} shows once per desktop in the sidebar"
    );
    found[1].clone()
}

fn many_desktops(n: usize) -> InitialStateMessage {
    let (sessions, desktops) = (0..n)
        .map(|i| {
            let (id, agent) = (format!("d{i:02}"), format!("a{i:02}"));
            let pane = pane(&id, &format!("p{i}"), &agent);
            (session(&agent, "idle"), desktop(&id, &id, "", vec![pane]))
        })
        .unzip();
    state(sessions, desktops)
}

#[test]
fn rows_below_the_dock_scroll_into_view_by_the_wheel_and_by_an_automation_press() {
    let mut world = World::start(Daemon::scripted().state(many_desktops(14)));
    world.resize((1200.0, 800.0));
    world.open_desktop();
    let [x, y, w, h] = world
        .screen()
        .bounds(Target::key("sidebar.scroll"))
        .expect("the sidebar scrolls");
    let below = |world: &World, key: &str| {
        let [_, top, _, height] = world.screen().bounds(Target::key(key)).expect("built");
        top + height - (y + h)
    };
    assert!(
        below(&world, "sidebar.session.a13") > 0.0,
        "the last row starts under the Dock"
    );

    world.gesture(|app| app.runtime.wheel(x + w / 2.0, y + h / 2.0, -2000.0));
    world.settle();
    assert!(
        below(&world, "sidebar.session.a13") <= 0.5,
        "the wheel brings the last row into view"
    );

    world.gesture(|app| app.runtime.wheel(x + w / 2.0, y + h / 2.0, 2000.0));
    world.settle();
    assert!(
        below(&world, "sidebar.session.a13") > 0.0,
        "the wheel takes it back under the Dock"
    );
    world
        .automate(
            "press",
            serde_json::json!({"target": {"key": "sidebar.session.a13"}}),
        )
        .expect("the press scrolls the row into view and lands on it");
    assert!(below(&world, "sidebar.session.a13") <= 0.5);
    let shown = world.daemon.sent_of("desktop_show_session");
    assert_eq!(shown.len(), 1);
    assert_eq!(shown[0]["session_id"], "a13");
}

#[test]
fn an_agent_row_ends_its_marks_at_its_right_edge_and_omits_desktop_priority_and_delegation_pills() {
    use attn_protocol::{SessionDelegationRole, SessionPullRequest};
    let agent = Session {
        priority: Some(true),
        delegated_from_chief: Some(true),
        dispatcher_session_id: Some("b".into()),
        delegation_role: Some(SessionDelegationRole {
            name: "Builder".into(),
            ..SessionDelegationRole::default()
        }),
        pull_requests: Some(vec![SessionPullRequest {
            repository: "github.com/victorarias/attn".into(),
            number: 812,
            state: "open".into(),
            created_at: "2026-08-30T12:00:00Z".into(),
            ..SessionPullRequest::default()
        }]),
        label: "Rework the ledger's sweep across every profile".into(),
        ..session("a", "idle")
    };
    let world = World::start(Daemon::scripted().state(state(
        vec![agent, session("b", "idle")],
        vec![desktop(
            "d1",
            "a",
            "Work",
            vec![pane("d1", "p1", "a"), pane("d1", "p2", "b")],
        )],
    )));
    let screen = world.screen();
    let bounds = |key: &str| screen.bounds(Target::key(key)).expect(key);
    let [row_x, _, row_w, _] = bounds("sidebar.session.a");
    let [chain_x, _, chain_w, _] = bounds("sidebar.session.a.chain");
    let [pr_x, _, pr_w, _] = bounds("sidebar.session.a.pr");
    let [name_x, _, name_w, _] = bounds("sidebar.session.a.name");
    assert_eq!(
        chain_x + chain_w,
        row_x + row_w - 8.0,
        "the role, a control, ends at the row's padding, with nothing hidden after it"
    );
    assert!(pr_x + pr_w <= chain_x, "the marks come before the role");
    assert!(
        name_x + name_w + 8.0 >= pr_x,
        "a long name runs up to the marks: {name_x}+{name_w} against {pr_x}"
    );
    let row_imgs: Vec<String> = {
        let all = world.runtime().elements();
        let parent = all
            .iter()
            .find(|e| e.key.as_deref() == Some("sidebar.session.a"))
            .expect("agent row");
        let prefix = format!("{}/", parent.path);
        all.iter()
            .filter(|e| e.path.starts_with(&prefix) && e.role == "img")
            .filter_map(|e| e.label.clone())
            .collect()
    };
    assert_eq!(
        row_imgs,
        vec![
            "Claude Code · idle".to_owned(),
            "github.com/victorarias/attn#812 · open".to_owned(),
        ],
        "desktop flow shows only the state mark and pull request badge, omitting priority and delegation pills"
    );
    assert_eq!(
        screen
            .find(Target::key("sidebar.session.a.name"))
            .and_then(|n| n.name),
        Some("Rework the ledger's sweep across every profile".into()),
    );

    let mut world = world;
    let [x, y, w, h] = world
        .screen()
        .bounds(Target::key("sidebar.session.b.actions"))
        .unwrap();
    world.gesture(|app| app.move_to((x + w / 2.0, y + h / 2.0)));
    world.gesture(|app| app.click_at((x + w / 2.0, y + h / 2.0)));
    assert!(
        world
            .screen()
            .find(Target::role("menu").name("Actions for b"))
            .is_some(),
        "the actions button stays pressable beside the role"
    );
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Delegation chain"))
            .is_none(),
        "pressing the actions button does not pin the chain"
    );
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        })
    });
    world.gesture(|app| app.click(Target::key("sidebar.session.b.chain")));
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Delegation chain"))
            .is_some(),
        "and so does the role"
    );
}

#[test]
fn a_launching_agent_leads_with_an_emoji_picked_by_its_id_until_logos_are_off() {
    let ids = ["l1", "38205d09-8ac6-4cec-84a3-77079b00f99c"];
    let mut world = World::start(Daemon::scripted().state(state(
        ids.iter().map(|id| session(id, "launching")).collect(),
        vec![desktop(
            "d1",
            "a",
            "One",
            vec![pane("d1", "p1", ids[0]), pane("d1", "p2", ids[1])],
        )],
    )));
    let emoji = |world: &World, id: &str| {
        world
            .screen()
            .find(Target::key(format!("sidebar.session.{id}.emoji")))
            .and_then(|n| n.name)
    };
    assert_eq!(emoji(&world, ids[0]).as_deref(), Some("🦄"));
    assert_eq!(emoji(&world, ids[1]).as_deref(), Some("⚙️"));
    let lead = world
        .screen()
        .bounds(Target::key(format!("sidebar.session.{}.state", ids[0])))
        .unwrap();
    assert_eq!((lead[2], lead[3]), (16.0, 16.0));

    world.daemon.emit(attn_protocol::SettingsUpdatedMessage {
        settings: Some(BTreeMap::from([(
            "sidebar_harness_logos_enabled".to_owned(),
            "false".to_owned(),
        )])),
        ..attn_protocol::SettingsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(
        emoji(&world, ids[0]),
        None,
        "without logos it shows its state's dot"
    );
}

#[test]
fn a_narrow_sidebar_at_large_text_keeps_an_automation_run_s_name_clear_of_its_pull_request() {
    use attn_protocol::{AutomationProvenance, SessionPullRequest};
    let mut agent = session("s1", "idle");
    agent.label = "build-and-release-platform-本番の権限を移行する作業ツリー".into();
    agent.automation = Some(AutomationProvenance {
        run_id: "r1".into(),
        definition_id: 1,
        definition_name: "Nightly verify that deployment artifacts match the signed manifests"
            .into(),
        trigger_type: "scheduled".into(),
        ..AutomationProvenance::default()
    });
    agent.pull_requests = Some(vec![SessionPullRequest {
        repository: "github.com/victorarias/attn".into(),
        number: 812,
        state: "open".into(),
        created_at: "2026-08-30T12:00:00Z".into(),
        ..SessionPullRequest::default()
    }]);
    let mut message = state(
        vec![agent],
        vec![desktop("d1", "a", "Work", vec![pane("d1", "p1", "s1")])],
    );
    let settings = message.settings.as_mut().unwrap();
    settings.insert("uiScale".into(), "1.5".into());
    settings.insert("sidebar_width".into(), "200".into());
    let mut world = World::start_sized(Daemon::scripted().state(message), (800.0, 600.0));
    world.gesture(|app| app.click(Target::key("sidebar.automations.finished")));
    let elements = world.runtime().elements();
    let drawn = |key: &str| {
        elements
            .iter()
            .find(|e| e.key.as_deref() == Some(key))
            .map(|e| e.visible)
            .unwrap_or_else(|| panic!("{key} is drawn"))
    };
    let [row_x, _, row_w, _] = drawn("sidebar.session.s1");
    let parts = [
        "sidebar.session.s1.name",
        "sidebar.session.s1.pr",
        "sidebar.session.s1.run",
    ]
    .map(|key| (key, drawn(key)));
    let [_, _, name_w, _] = parts[0].1;
    assert!(name_w >= 46.0, "the name keeps its first word: {name_w}");
    for (i, (key, [x, _, w, _])) in parts.iter().enumerate() {
        assert!(
            *x >= row_x - 0.5 && x + w <= row_x + row_w + 0.5,
            "{key} at {x}..{} stays in the row at {row_x}..{}",
            x + w,
            row_x + row_w
        );
        for (other, [ox, _, ow, _]) in &parts[i + 1..] {
            let over = (x + w).min(ox + ow) - x.max(*ox);
            assert!(
                over <= 0.5,
                "{other} lies {over} over {key}: {ox}..{} and {x}..{}",
                ox + ow,
                x + w
            );
        }
    }
}

#[test]
fn the_current_desktop_sits_in_a_tonal_well_and_unslotted_desktops_omit_synthetic_numbers() {
    let mut slotted = desktop(
        "d1",
        "a",
        "Work",
        vec![pane("d1", "p1", "s1"), pane("d1", "p2", "s2")],
    );
    slotted.shortcut_slot = Some(1);
    let unslotted = desktop("d2", "b", "Review", vec![pane("d2", "p3", "s3")]);
    let mut world = World::start(Daemon::scripted().state(state(
        vec![
            session("s1", "waiting_input"),
            session("s2", "working"),
            session("s3", "idle"),
        ],
        vec![slotted, unslotted],
    )));
    world.open_desktop();
    let t = attn_ui::colors::scheme_for(None).colors(true).ui;
    let surface_under = |world: &World, key: &str| {
        let rt = world.runtime();
        let scale = rt.env.scale;
        let el = rt
            .elements()
            .into_iter()
            .find(|e| e.key.as_deref() == Some(key))
            .unwrap_or_else(|| panic!("no {key}"));
        let probe = [
            (el.rect[0] + el.rect[2] - 4.0) * scale,
            (el.rect[1] + el.rect[3] / 2.0) * scale,
        ];
        let holds = |r: [f32; 4], [x, y]: [f32; 2]| {
            r[0] <= x && x <= r[0] + r[2] && r[1] <= y && y <= r[1] + r[3]
        };
        let [r, g, b, _] = rt.clear_color();
        let [r, g, b] = rt
            .instances
            .iter()
            .filter(|p| {
                let [x0, y0, x1, y1] = p.clip;
                p.params[1] == rune::render::KIND_RECT
                    && p.params[2] == 0.0
                    && holds(p.rect, probe)
                    && holds([x0, y0, x1 - x0, y1 - y0], probe)
            })
            .fold([r, g, b], |below, p| {
                [0, 1, 2].map(|c| p.fill[c] + below[c] * (1.0 - p.fill[3]))
            })
            .map(rune::theme::srgb_decode);
        rune::Color { r, g, b, a: 1.0 }
    };
    let close = |a: rune::Color, b: rune::Color| {
        [a.r - b.r, a.g - b.g, a.b - b.b]
            .iter()
            .all(|d| d.abs() < 1e-2)
    };
    let active_row = surface_under(&world, "sidebar.session.s1");
    let current_well_row = surface_under(&world, "sidebar.session.s2");
    let other_desktop_row = surface_under(&world, "sidebar.session.s3");
    assert!(
        close(active_row, t.bg_selected),
        "active row paints on bg_selected: {active_row:?} vs {:?}",
        t.bg_selected
    );
    assert!(
        close(current_well_row, t.bg_elevated),
        "inactive row inside current desktop paints on bg_elevated well: {current_well_row:?} vs {:?}",
        t.bg_elevated
    );
    assert!(
        close(other_desktop_row, t.bg_panel),
        "row outside current desktop paints on bg_panel: {other_desktop_row:?} vs {:?}",
        t.bg_panel
    );

    let texts_of = |world: &World, key: &str| -> Vec<String> {
        let all = world.runtime().elements();
        let parent = all
            .iter()
            .find(|e| e.key.as_deref() == Some(key))
            .expect("heading");
        let prefix = format!("{}/", parent.path);
        all.iter()
            .filter(|e| e.path.starts_with(&prefix))
            .filter_map(|e| e.text.clone())
            .collect()
    };
    let d1_texts = texts_of(&world, "sidebar.desktop.d1");
    assert!(
        d1_texts.iter().any(|w| w == "1"),
        "slotted desktop shows its shortcut slot: {d1_texts:?}"
    );
    let d2_texts = texts_of(&world, "sidebar.desktop.d2");
    assert!(
        d2_texts.iter().any(|w| w == "Review"),
        "unslotted desktop shows its name: {d2_texts:?}"
    );
    assert!(
        !d2_texts.iter().any(|w| w == "10"),
        "unslotted named desktop omits synthetic number 10: {d2_texts:?}"
    );

    world.daemon.emit(attn_protocol::SettingsUpdatedMessage {
        settings: Some(BTreeMap::from([(
            "keybindings_config".to_owned(),
            r#"{"version":1,"overrides":{"desktop.select1":{"key":"1","alt":true}}}"#.to_owned(),
        )])),
        ..attn_protocol::SettingsUpdatedMessage::new()
    });
    world.settle();
    let d1_rebound = texts_of(&world, "sidebar.desktop.d1");
    assert!(
        d1_rebound.iter().any(|w| w.contains('1') && w != "1") && d1_rebound != d1_texts,
        "rebinding desktop.select1 updates the heading keys: {d1_rebound:?} vs {d1_texts:?}"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn crew_pills_deduplicate_own_launch_desktops_while_keeping_companion_tiles() {
    use attn_protocol::{CrewMember, LaunchDesktopSetting};

    let chief_session = Session {
        chief_of_staff: Some(true),
        crew_member: Some("chief".into()),
        label: "Chief".into(),
        ..session("sess-chief", "waiting_input")
    };
    let companion = session("sess-comp", "working");
    let work = session("sess-work", "idle");

    let mut d_work = desktop(
        "d-work",
        "a",
        "Work",
        vec![pane("d-work", "p1", "sess-work")],
    );
    d_work.shortcut_slot = Some(1);
    let d_chief_solo = desktop(
        "d-chief",
        "b",
        "Chief",
        vec![pane("d-chief", "p2", "sess-chief")],
    );
    let d_vesta_empty = desktop("d-vesta", "c", "Vesta", vec![]);

    let chief = CrewMember {
        id: "chief".into(),
        name: "Chief".into(),
        profile_id: "work".into(),
        chief: Some(true),
        agent: Some("claude".into()),
        resolved_agent: "claude".into(),
        binding_session: Some("sess-chief".into()),
        launch_desktop: Some(LaunchDesktopSetting {
            desktop_id: Some("d-chief".into()),
            ..LaunchDesktopSetting::default()
        }),
        revision: 1,
        ..CrewMember::default()
    };
    let vesta = CrewMember {
        id: "vesta".into(),
        name: "Vesta".into(),
        profile_id: "work".into(),
        resolved_agent: "claude".into(),
        launch_desktop: Some(LaunchDesktopSetting {
            desktop_id: Some("d-vesta".into()),
            ..LaunchDesktopSetting::default()
        }),
        revision: 1,
        ..CrewMember::default()
    };

    let mut initial = state(
        vec![work.clone(), chief_session.clone()],
        vec![d_work.clone(), d_chief_solo, d_vesta_empty.clone()],
    );
    initial.crew = Some(vec![chief.clone(), vesta.clone()]);
    let mut world = World::start(Daemon::scripted().state(initial));
    world.open_desktop();

    assert!(
        world
            .screen()
            .find(Target::key("sidebar.crew.chief"))
            .is_some(),
        "active Chief appears as a pill in the Crew bar"
    );
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.crew.vesta"))
            .is_some(),
        "resting Vesta appears as a pill in the Crew bar"
    );
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.desktop.d-chief"))
            .is_none(),
        "Chief's solo unslotted launch desktop is omitted from DESKTOPS"
    );
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.session.sess-chief"))
            .is_none(),
        "Chief is not duplicated under DESKTOPS"
    );
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.desktop.d-vesta"))
            .is_none(),
        "resting crew's empty launch desktop is omitted from DESKTOPS"
    );

    let d_chief_with_companion = desktop(
        "d-chief",
        "b",
        "Chief",
        vec![
            pane("d-chief", "p2", "sess-chief"),
            pane("d-chief", "p3", "sess-comp"),
        ],
    );
    let mut with_companion = state(
        vec![work.clone(), chief_session.clone(), companion.clone()],
        vec![
            d_work.clone(),
            d_chief_with_companion.clone(),
            d_vesta_empty.clone(),
        ],
    );
    with_companion.crew = Some(vec![chief.clone(), vesta.clone()]);
    world.daemon.emit(with_companion);
    world.settle();

    assert!(
        world
            .screen()
            .find(Target::key("sidebar.desktop.d-chief"))
            .is_some(),
        "Chief's desktop appears when companion tiles share it"
    );
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.session.sess-comp"))
            .is_some(),
        "companion tile appears under Chief's desktop"
    );
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.session.sess-chief"))
            .is_none(),
        "Chief itself stays deduplicated into the Crew pill bar"
    );

    let unconfigured_chief = CrewMember {
        agent: None,
        binding_session: None,
        ..chief
    };
    let retired_member = CrewMember {
        id: "old".into(),
        name: "Old".into(),
        profile_id: "work".into(),
        retired: true,
        resolved_agent: "claude".into(),
        launch_desktop: Some(LaunchDesktopSetting {
            desktop_id: Some("d-old".into()),
            ..LaunchDesktopSetting::default()
        }),
        revision: 1,
        ..CrewMember::default()
    };
    let d_old = desktop("d-old", "d", "Old", vec![]);
    let stale_chief_session = Session {
        crew_member: None,
        ..chief_session
    };
    let mut mixed = state(
        vec![work, stale_chief_session, companion],
        vec![d_work, d_chief_with_companion, d_vesta_empty, d_old],
    );
    mixed.crew = Some(vec![unconfigured_chief, vesta, retired_member]);
    world.daemon.emit(mixed);
    world.settle();
    let crew_items: Vec<Option<String>> = world
        .screen()
        .children(Target::key("sidebar.crew"))
        .into_iter()
        .map(|n| n.name)
        .collect();
    assert_eq!(
        crew_items,
        vec![Some("Set up Chief".into()), Some("Start Vesta".into())],
        "an unconfigured Chief renders once in the Crew bar even with a stale chief_of_staff session"
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("chief-slot"))
            .value
            .as_deref(),
        Some("unconfigured")
    );
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.session.sess-chief"))
            .is_some(),
        "an unbound live session not shown as an active Crew pill stays visible under DESKTOPS"
    );
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.desktop.d-old"))
            .is_some(),
        "a retired crew member's empty launch desktop is not hidden"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn empty_non_current_desktops_fold_into_dashed_pills_that_open_and_accept_tile_drops() {
    let mut d_work = desktop("d1", "a", "Work", vec![pane("d1", "p1", "s1")]);
    d_work.shortcut_slot = Some(1);
    let mut d_familiar = desktop("d6", "b", "Familiar", vec![]);
    d_familiar.shortcut_slot = Some(6);
    let d_assistant = desktop("d-asst", "c", "assistant", vec![]);

    let mut world = World::start(Daemon::scripted().state(state(
        vec![session("s1", "working")],
        vec![d_work.clone(), d_familiar.clone(), d_assistant.clone()],
    )));
    world
        .daemon
        .on("desktop_move_leaf", |r: &attn_testkit::Request| {
            serde_json::json!({
                "event": "profile_action_result",
                "request_id": r["request_id"],
                "action": "desktop_move_leaf",
                "success": true,
            })
        });
    world.open_desktop();

    let all = world.runtime().elements();
    let strip = all
        .iter()
        .find(|e| e.key.as_deref() == Some("sidebar.empty_desktops"))
        .expect("empty desktops strip is drawn");
    let strip_prefix = format!("{}/", strip.path);
    for key in ["sidebar.desktop.d6", "sidebar.desktop.d-asst"] {
        let pill = all
            .iter()
            .find(|e| e.key.as_deref() == Some(key))
            .unwrap_or_else(|| panic!("{key} is drawn"));
        assert!(
            pill.path.starts_with(&strip_prefix),
            "{key} sits inside the folded empty desktops strip: {}",
            pill.path
        );
    }

    let opened_before = world.daemon.sent_of("desktop_set_current").len();
    let drop_target = centre_of(&world, "sidebar.desktop.d6");
    world.gesture(|app| app.drag_over(Target::key("sidebar.session.s1"), drop_target));
    world.advance(std::time::Duration::from_millis(400));
    assert_eq!(
        world.daemon.sent_of("desktop_set_current").len(),
        opened_before,
        "hovering a folded empty desktop pill during a drag does not switch and shift targets"
    );
    world.gesture(|app| app.release(drop_target));
    let moved = world.daemon.sent_of("desktop_move_leaf");
    assert_eq!(moved.len(), 1, "{moved:?}");
    assert_eq!(moved[0]["source_desktop_id"], "d1");
    assert_eq!(moved[0]["leaf_id"], "p1");
    assert_eq!(moved[0]["target_desktop_id"], "d6");

    world.gesture(|app| app.click(Target::key("sidebar.desktop.d-asst")));
    let opened = world.daemon.sent_of("desktop_set_current");
    assert_eq!(
        opened.last().and_then(|m| m["desktop_id"].as_str()),
        Some("d-asst")
    );
    world
        .daemon
        .emit(attn_protocol::ProfileArrangementChangedMessage {
            profile: Profile {
                id: "work".into(),
                name: "Work".into(),
                current_desktop_id: "d-asst".into(),
                ..Profile::default()
            },
            desktops: vec![d_work, d_familiar, d_assistant],
            ..attn_protocol::ProfileArrangementChangedMessage::new()
        });
    world.settle();
    assert_eq!(
        world
            .screen()
            .get(Target::key("sidebar.desktop.d-asst"))
            .value
            .as_deref(),
        Some("current, Empty desktop")
    );
    assert!(
        world
            .screen()
            .find(Target::role("button").name("Rename assistant"))
            .is_some(),
        "opening an empty desktop expands it into a full heading with rename and close"
    );
    world.advance(std::time::Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn desktop_sidebar_unifies_automation_desktops_folds_finished_runs_and_cleans_rail_and_overview() {
    use attn_protocol::{AutomationProvenance, CalloutShowRequestMessage, CalloutStep};

    let auto = |id: i64, name: &str, run_id: &str| AutomationProvenance {
        run_id: run_id.into(),
        definition_id: id,
        definition_name: name.into(),
        trigger_type: "scheduled".into(),
        ..AutomationProvenance::default()
    };

    let mut work_agent = session("work-1", "working");
    work_agent.label = "Shelf and overview".into();

    let mut waiting_older = session("run-wait-old", "waiting_input");
    waiting_older.label = "CI sheriff: main CI".into();
    waiting_older.turn_owed = Some(true);
    waiting_older.turn_opened_at = Some("2026-09-26T09:00:00Z".into());
    waiting_older.automation = Some(auto(1, "Sheriff", "r-1"));

    let mut waiting_newer = session("run-wait-new", "waiting_input");
    waiting_newer.label = "PR review #412".into();
    waiting_newer.turn_owed = Some(true);
    waiting_newer.turn_opened_at = Some("2026-09-26T10:00:00Z".into());
    waiting_newer.automation = Some(auto(2, "Review main", "r-2"));

    let mut working_run = session("run-work", "working");
    working_run.label = "Feature map audit".into();
    working_run.automation = Some(auto(3, "Feature map audit", "r-3"));

    let mut finished_old = session("run-fin-old", "idle");
    finished_old.label = "Sigma sheriff".into();
    finished_old.state_since = "2026-09-26T08:00:00Z".into();
    finished_old.automation = Some(auto(4, "sigma sheriff", "r-4"));

    let mut finished_new = session("run-fin-new", "idle");
    finished_new.label = "Land drive".into();
    finished_new.state_since = "2026-09-26T11:00:00Z".into();
    finished_new.automation = Some(auto(5, "land drive", "r-5"));

    let mut slotted_run = session("run-on-slot", "idle");
    slotted_run.label = "Pinned check".into();
    slotted_run.automation = Some(auto(6, "Pinned", "r-6"));

    let mut d_work = desktop("d1", "a", "Work", vec![pane("d1", "p-work", "work-1")]);
    d_work.shortcut_slot = Some(1);
    let mut d_slot2 = desktop(
        "d2",
        "b",
        "Checks",
        vec![pane("d2", "p-slot", "run-on-slot")],
    );
    d_slot2.shortcut_slot = Some(2);
    let d_auto_1 = desktop(
        "auto-1",
        "c",
        "Sheriff",
        vec![pane("auto-1", "p-a1", "run-wait-old")],
    );
    let d_auto_2 = desktop(
        "auto-2",
        "d",
        "Review main",
        vec![pane("auto-2", "p-a2", "run-wait-new")],
    );
    let d_auto_3 = desktop(
        "auto-3",
        "e",
        "Feature map audit",
        vec![pane("auto-3", "p-a3", "run-work")],
    );
    let d_auto_4 = desktop(
        "auto-4",
        "f",
        "sigma sheriff",
        vec![pane("auto-4", "p-a4", "run-fin-old")],
    );
    let d_auto_5 = desktop(
        "auto-5",
        "g",
        "land drive",
        vec![pane("auto-5", "p-a5", "run-fin-new")],
    );
    let d_crew_empty = desktop("crew-empty", "h", "Vesta", vec![]);

    let mut d_auto_empty = desktop("auto-empty", "h", "Nightly check", vec![]);
    d_auto_empty.ephemeral = Some(true);
    let mut initial = state(
        vec![
            work_agent,
            waiting_older,
            waiting_newer,
            working_run,
            finished_old,
            finished_new,
            slotted_run,
        ],
        vec![
            d_work.clone(),
            d_slot2.clone(),
            d_auto_1.clone(),
            d_auto_2.clone(),
            d_auto_3.clone(),
            d_auto_4.clone(),
            d_auto_5.clone(),
            d_crew_empty.clone(),
            d_auto_empty.clone(),
        ],
    );
    initial.crew = Some(vec![attn_protocol::CrewMember {
        id: "vesta".into(),
        name: "Vesta".into(),
        profile_id: "work".into(),
        resolved_agent: "claude".into(),
        launch_desktop: Some(attn_protocol::LaunchDesktopSetting {
            desktop_id: Some("crew-empty".into()),
            label: Some("Vesta".into()),
            ..attn_protocol::LaunchDesktopSetting::default()
        }),
        ..attn_protocol::CrewMember::default()
    }]);

    let daemon = Daemon::scripted().state(initial);
    daemon.on("desktop_move_leaf", |r: &attn_testkit::Request| {
        serde_json::json!({
            "event": "profile_action_result",
            "request_id": r["request_id"],
            "action": "desktop_move_leaf",
            "success": true,
        })
    });
    let mut world = World::start(daemon);
    world.open_desktop();

    let screen = world.screen();
    // Automation-only unslotted desktops do not appear under Desktops; slotted user desktops stay.
    assert!(screen.find(Target::key("sidebar.desktop.d1")).is_some());
    assert!(screen.find(Target::key("sidebar.desktop.d2")).is_some());
    assert!(
        screen
            .find(Target::key("sidebar.session.run-on-slot"))
            .is_some()
    );
    for hidden in [
        "auto-1",
        "auto-2",
        "auto-3",
        "auto-4",
        "auto-5",
        "crew-empty",
        "auto-empty",
    ] {
        assert!(
            screen
                .find(Target::key(format!("sidebar.desktop.{hidden}")))
                .is_none(),
            "{hidden} should not appear under Desktops"
        );
    }

    // Unified Automations section shows waiting (oldest first) + working runs and folds finished.
    assert!(
        screen
            .find(Target::key("sidebar.rule.automations"))
            .is_some()
    );
    assert!(
        screen
            .get(Target::key("sidebar.automations.needing"))
            .name
            .as_deref()
            .is_some_and(|n| n.starts_with("2 waiting"))
    );
    let auto_row_keys = |screen: &attn_testkit::Screen| -> Vec<String> {
        screen
            .all(Target::role("button"))
            .into_iter()
            .filter_map(|n| n.key)
            .filter(|k| {
                k.starts_with("sidebar.session.run-")
                    && k != "sidebar.session.run-on-slot"
                    && !k.ends_with(".actions")
                    && !k.ends_with(".settle")
            })
            .collect()
    };
    let shown_auto_keys = auto_row_keys(&screen);
    assert_eq!(
        shown_auto_keys,
        [
            "sidebar.session.run-wait-old",
            "sidebar.session.run-wait-new",
            "sidebar.session.run-work",
        ]
    );
    let toggle = screen.get(Target::key("sidebar.automations.finished"));
    assert_eq!(toggle.name.as_deref(), Some("2 finished runs"));
    assert!(toggle.is("collapsed"));

    // Expanding finished runs reveals newest first; collapsing hides them again.
    world.gesture(|app| app.click(Target::key("sidebar.automations.finished")));
    let expanded_keys = auto_row_keys(&world.screen());
    assert_eq!(
        expanded_keys,
        [
            "sidebar.session.run-wait-old",
            "sidebar.session.run-wait-new",
            "sidebar.session.run-work",
            "sidebar.session.run-fin-new",
            "sidebar.session.run-fin-old",
        ]
    );
    world.gesture(|app| app.click(Target::key("sidebar.automations.finished")));
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.session.run-fin-old"))
            .is_none()
    );

    // Clicking the waiting chip walks runs needing you in sidebar order (oldest first).
    world.gesture(|app| app.click(Target::key("sidebar.automations.needing")));
    let shown = world.daemon.sent_of("desktop_show_session");
    assert_eq!(
        shown.last().and_then(|m| m["session_id"].as_str()),
        Some("run-wait-old")
    );

    // Dragging an automation run onto a user desktop moves its pane there.
    let target = centre_of(&world, "sidebar.desktop.d1");
    world.gesture(|app| app.drag(Target::key("sidebar.session.run-work"), target));
    let moved = world.daemon.sent_of("desktop_move_leaf");
    assert_eq!(moved.len(), 1, "{moved:?}");
    assert_eq!(moved[0]["source_desktop_id"], "auto-3");
    assert_eq!(moved[0]["leaf_id"], "p-a3");
    assert_eq!(moved[0]["target_desktop_id"], "d1");

    // Clicking "all" opens the Automations drawer.
    world.gesture(|app| app.click(Target::key("sidebar.automations.all")));
    assert!(world.screen().find(Target::key("automations")).is_some());
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        })
    });
    assert!(world.screen().find(Target::key("automations")).is_none());

    // A callout targeting a finished off-screen run expands finished runs and anchors to it.
    world.daemon.emit(CalloutShowRequestMessage {
        request_id: "callout-fin".into(),
        profile_id: "work".into(),
        from: "Chief".into(),
        steps: vec![CalloutStep {
            anchor: "agent".into(),
            session_id: Some("run-fin-old".into()),
            text: "Check the finished run".into(),
            ..CalloutStep::default()
        }],
        ..CalloutShowRequestMessage::new()
    });
    world.settle();
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.session.run-fin-old"))
            .is_some(),
        "callout targeting a folded finished run expands finished runs"
    );
    assert!(
        world.screen().find(Target::key("callout")).is_some()
            && world
                .screen()
                .find(Target::key("callout.unanchored"))
                .is_none()
            && world.runtime().presented("callout.ring").is_some(),
        "callout anchors to and rings the expanded finished run"
    );
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        })
    });

    // ⌘G overview skips non-current automation-only desktops and empty crew launch desktops.
    world.gesture(|app| app.key(chord(Action::DesktopOverview)));
    let overview_screen = world.screen();
    assert!(
        overview_screen
            .find(Target::key("overview.strip.d2"))
            .is_some()
    );
    for skipped in [
        "auto-1",
        "auto-2",
        "auto-3",
        "auto-4",
        "auto-5",
        "crew-empty",
        "auto-empty",
    ] {
        assert!(
            overview_screen
                .find(Target::key(format!("overview.strip.{skipped}")))
                .is_none(),
            "overview should skip {skipped}"
        );
    }
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        })
    });

    // Collapsed rail omits automation-only, empty crew, and empty ephemeral desktops, showing one ⚡ chip.
    world.gesture(|app| app.click(Target::role("button").name("Collapse sidebar")));
    let rail_screen = world.screen();
    let rail_desktops: Vec<Option<String>> = rail_screen
        .children(Target::key("rail.desktops"))
        .into_iter()
        .map(|n| n.key)
        .collect();
    assert_eq!(
        rail_desktops,
        [Some("rail.desktop.1".into()), Some("rail.desktop.2".into())]
    );
    let rail_auto = rail_screen.get(Target::key("rail.automations"));
    assert_eq!(
        rail_auto.name.as_deref(),
        Some("Automations · 5 runs, 2 need you")
    );
    let shown_before = world.daemon.sent_of("desktop_show_session").len();
    world.gesture(|app| app.click(Target::key("rail.automations")));
    assert_eq!(
        world.daemon.sent_of("desktop_show_session").len(),
        shown_before + 1
    );

    // When currently viewing a finished automation run, it stays visible in Automations,
    // rail.automations marks current, and ⌘G overview still opens on that desktop.
    world
        .daemon
        .emit(attn_protocol::ProfileArrangementChangedMessage {
            profile: Profile {
                id: "work".into(),
                name: "Work".into(),
                current_desktop_id: "auto-4".into(),
                ..Profile::default()
            },
            desktops: vec![
                d_work,
                d_slot2,
                d_auto_1,
                d_auto_2,
                d_auto_3,
                d_auto_4,
                d_auto_5,
                d_crew_empty,
            ],
            ..attn_protocol::ProfileArrangementChangedMessage::new()
        });
    world.settle();
    assert_eq!(
        world
            .screen()
            .get(Target::key("rail.automations"))
            .value
            .as_deref(),
        Some("current")
    );
    world.gesture(|app| app.key(chord(Action::DesktopOverview)));
    assert!(
        world
            .screen()
            .find(Target::key("overview.miniature.p-a4"))
            .is_some(),
        "overview opens on the current automation desktop when pressed there"
    );
    assert!(
        world
            .screen()
            .find(Target::key("overview.strip.d1"))
            .is_some()
    );
    assert!(
        world
            .screen()
            .find(Target::key("overview.strip.auto-1"))
            .is_none()
    );
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        })
    });

    world.gesture(|app| app.click(Target::role("button").name("Show sidebar")));
    // Collapse finished runs again, then verify the currently viewed finished run stays visible.
    world.gesture(|app| app.click(Target::key("sidebar.automations.finished")));
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.session.run-fin-old"))
            .is_some(),
        "currently viewed finished run remains visible even while finished runs are folded"
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("sidebar.automations.finished"))
            .name
            .as_deref(),
        Some("1 finished run")
    );
    // Advance past unanswered RPC timers and verify the UI reaches rest cleanly.
    world.advance(std::time::Duration::from_secs(20));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn automation_runs_fold_settled_and_snoozed_runs_wake_on_snooze_expiry_and_keep_all_on_screen_panes()
 {
    let work_agent = Session {
        label: "Work".into(),
        ..session("work-1", "idle")
    };
    let auto_meta =
        |def_id: i64, def_name: &str, run_id: &str| attn_protocol::AutomationProvenance {
            run_id: run_id.into(),
            definition_id: def_id,
            definition_name: def_name.into(),
            trigger_type: "scheduled".into(),
            ..attn_protocol::AutomationProvenance::default()
        };
    let settled_waiting = Session {
        label: "Settled check".into(),
        state_since: "2026-09-26T10:00:00Z".into(),
        turn_owed: None,
        automation: Some(auto_meta(1, "Settled", "r-settled")),
        ..session("run-settled", "waiting_input")
    };
    let snoozed_waiting = Session {
        label: "Snoozed check".into(),
        state_since: "2026-09-26T11:00:00Z".into(),
        turn_owed: Some(true),
        turn_opened_at: Some("2026-09-26T10:30:00Z".into()),
        turn_snoozed_until: Some(super::queue::ago(-1)),
        automation: Some(auto_meta(2, "Snoozed", "r-snoozed")),
        ..session("run-snoozed", "waiting_input")
    };
    let finished_left = Session {
        label: "Pair left".into(),
        state_since: "2026-09-26T08:00:00Z".into(),
        automation: Some(auto_meta(3, "Pair", "r-left")),
        ..session("run-left", "idle")
    };
    let finished_right = Session {
        label: "Pair right".into(),
        state_since: "2026-09-26T08:30:00Z".into(),
        automation: Some(auto_meta(3, "Pair", "r-right")),
        ..session("run-right", "idle")
    };

    let mut d_work = desktop("d1", "a", "Work", vec![pane("d1", "p-work", "work-1")]);
    d_work.shortcut_slot = Some(1);
    let d_settled = desktop(
        "auto-settled",
        "b",
        "Settled",
        vec![pane("auto-settled", "p-settled", "run-settled")],
    );
    let d_snoozed = desktop(
        "auto-snoozed",
        "c",
        "Snoozed",
        vec![pane("auto-snoozed", "p-snoozed", "run-snoozed")],
    );
    let d_pair = Desktop {
        id: "auto-pair".into(),
        profile_id: "work".into(),
        name: "Pair".into(),
        order_key: "d".into(),
        active_pane_id: "p-left".into(),
        tree_json: serde_json::json!({
            "type": "split",
            "split_id": "s-pair",
            "direction": "vertical",
            "ratio": 0.5,
            "children": [
                {"type": "pane", "pane_id": "p-left"},
                {"type": "pane", "pane_id": "p-right"}
            ]
        })
        .to_string(),
        panes: vec![
            pane("auto-pair", "p-left", "run-left"),
            pane("auto-pair", "p-right", "run-right"),
        ],
        revision: 1,
        ..Desktop::default()
    };

    let initial = state(
        vec![
            work_agent,
            settled_waiting,
            snoozed_waiting,
            finished_left,
            finished_right,
        ],
        vec![
            d_work.clone(),
            d_settled.clone(),
            d_snoozed.clone(),
            d_pair.clone(),
        ],
    );
    let mut world = World::start_sized(Daemon::scripted().state(initial), (1600.0, 900.0));
    world.open_desktop();

    // Settled and snoozed waiting_input runs fold into finished; needing is 0 so no needing chip.
    let screen = world.screen();
    assert!(
        screen
            .find(Target::key("sidebar.automations.needing"))
            .is_none()
    );
    assert!(
        screen
            .find(Target::key("sidebar.session.run-settled"))
            .is_none()
    );
    assert!(
        screen
            .find(Target::key("sidebar.session.run-snoozed"))
            .is_none()
    );
    assert_eq!(
        screen
            .get(Target::key("sidebar.automations.finished"))
            .name
            .as_deref(),
        Some("4 finished runs")
    );

    // With needing == 0, clicking rail.automations opens the Automations drawer.
    world.gesture(|app| app.click(Target::role("button").name("Collapse sidebar")));
    assert_eq!(
        world
            .screen()
            .get(Target::key("rail.automations"))
            .name
            .as_deref(),
        Some("Automations · 4 runs")
    );
    world.gesture(|app| app.click(Target::key("rail.automations")));
    assert!(world.screen().find(Target::key("automations")).is_some());
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        })
    });
    assert!(world.screen().find(Target::key("automations")).is_none());

    // Advancing past the snooze deadline wakes the snoozed run without any new daemon event.
    world.advance(std::time::Duration::from_secs(61));
    assert_eq!(
        world
            .screen()
            .get(Target::key("rail.automations"))
            .name
            .as_deref(),
        Some("Automations · 4 runs, 1 needs you")
    );
    world.gesture(|app| app.click(Target::role("button").name("Show sidebar")));
    let woken = world.screen();
    assert!(
        woken
            .get(Target::key("sidebar.automations.needing"))
            .name
            .as_deref()
            .is_some_and(|n| n.contains("1 waiting"))
    );
    assert!(
        woken
            .find(Target::key("sidebar.session.run-snoozed"))
            .is_some()
    );
    assert!(
        woken
            .find(Target::key("sidebar.session.run-settled"))
            .is_none()
    );

    // Viewing a multi-pane automation desktop keeps every on-screen finished run visible (not folded).
    world
        .daemon
        .emit(attn_protocol::ProfileArrangementChangedMessage {
            profile: Profile {
                id: "work".into(),
                name: "Work".into(),
                current_desktop_id: "auto-pair".into(),
                ..Profile::default()
            },
            desktops: vec![d_work, d_settled, d_snoozed, d_pair],
            ..attn_protocol::ProfileArrangementChangedMessage::new()
        });
    world.settle();
    let multi = world.screen();
    assert!(
        multi
            .find(Target::key("sidebar.session.run-left"))
            .is_some()
    );
    assert!(
        multi
            .find(Target::key("sidebar.session.run-right"))
            .is_some(),
        "non-focused on-screen finished run must stay visible beside the focused one"
    );
    assert_eq!(
        multi
            .get(Target::key("sidebar.automations.finished"))
            .name
            .as_deref(),
        Some("1 finished run")
    );
    world.advance(std::time::Duration::from_secs(20));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn dragging_a_stacked_desktop_keeps_folded_desktops_in_the_saved_order() {
    let mut initial = three_desktops();
    let desktops = initial.desktops.as_mut().unwrap();
    desktops[1].order_key = "c".into();
    desktops[2].order_key = "d".into();
    desktops.push(desktop("d-mid", "b", "Middle empty", vec![]));
    desktops.push(desktop("d-end", "e", "Trailing empty", vec![]));

    let mut world = World::start(Daemon::scripted().state(initial));
    world.open_desktop();
    let (d1_rect, _) = world
        .runtime()
        .presented("sidebar.desktop.d1")
        .expect("d1 drawn");
    let (d2_rect, _) = world
        .runtime()
        .presented("sidebar.desktop.d2")
        .expect("d2 drawn");
    let (d3_row, _) = world
        .runtime()
        .presented("sidebar.session.side-d")
        .expect("last stacked row drawn");

    let (x, y) = (d1_rect[0] + d1_rect[2] / 2.0, d1_rect[1] + d1_rect[3] / 2.0);
    let bottom_y = d3_row[1] + d3_row[3] + 4.0;
    world.gesture(|app| {
        app.runtime.pointer_down(x, y);
        app.runtime.pointer_move(x, bottom_y);
    });
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.session.side-d.seam"))
            .is_some(),
        "bottom seam attaches to the last stacked desktop's row, not the trailing empty pill"
    );
    world.gesture(|app| app.runtime.pointer_up(x, bottom_y));
    let moved = world.daemon.sent_of("desktop_set_order");
    assert_eq!(moved.len(), 1, "{moved:?}");
    assert_eq!(
        moved[0]["desktop_ids"],
        serde_json::json!(["d-mid", "d2", "d3", "d1", "d-end"])
    );

    let (d3_rect, _) = world
        .runtime()
        .presented("sidebar.desktop.d3")
        .expect("d3 drawn");
    let (dx, dy) = (d3_rect[0] + d3_rect[2] / 2.0, d3_rect[1] + d3_rect[3] / 2.0);
    world.gesture(|app| {
        app.runtime.pointer_down(dx, dy);
        app.runtime.pointer_move(dx, d2_rect[1] + 2.0);
        app.runtime.pointer_up(dx, d2_rect[1] + 2.0);
    });
    let moved = world.daemon.sent_of("desktop_set_order");
    assert_eq!(moved.len(), 2, "{moved:?}");
    assert_eq!(
        moved[1]["desktop_ids"],
        serde_json::json!(["d1", "d-mid", "d3", "d2", "d-end"])
    );
    world.advance(std::time::Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn automation_delegates_on_launch_desktop_stay_unified_until_manual_or_orphaned() {
    use attn_protocol::AutomationProvenance;

    let mut work_agent = session("work-1", "working");
    work_agent.label = "Main work".into();

    let mut auto_root = session("auto-root", "idle");
    auto_root.label = "Review Recent Main Commits".into();
    auto_root.state_since = "2026-09-26T09:00:00Z".into();
    auto_root.automation = Some(AutomationProvenance {
        run_id: "r-review".into(),
        definition_id: 10,
        definition_name: "Review main".into(),
        trigger_type: "scheduled".into(),
        ..AutomationProvenance::default()
    });

    let mut delegate_working = session("del-work", "working");
    delegate_working.label = "Review Garden fullscreen".into();
    delegate_working.dispatcher_session_id = Some("auto-root".into());

    let mut delegate_nested_waiting = session("del-wait", "waiting_input");
    delegate_nested_waiting.label = "Verify edge case".into();
    delegate_nested_waiting.turn_owed = Some(true);
    delegate_nested_waiting.turn_opened_at = Some("2026-09-26T10:00:00Z".into());
    delegate_nested_waiting.dispatcher_session_id = Some("del-work".into());

    let mut delegate_finished = session("del-fin", "idle");
    delegate_finished.label = "Review palette action".into();
    delegate_finished.state_since = "2026-09-26T08:30:00Z".into();
    delegate_finished.dispatcher_session_id = Some("auto-root".into());

    let mut d_work = desktop("d1", "a", "Work", vec![pane("d1", "p-work", "work-1")]);
    d_work.shortcut_slot = Some(1);
    let d_review = desktop(
        "auto-review",
        "b",
        "Review main",
        vec![
            pane("auto-review", "p-root", "auto-root"),
            pane("auto-review", "p-work-del", "del-work"),
            pane("auto-review", "p-wait-del", "del-wait"),
            pane("auto-review", "p-fin-del", "del-fin"),
        ],
    );

    let initial = state(
        vec![
            work_agent.clone(),
            auto_root.clone(),
            delegate_working.clone(),
            delegate_nested_waiting.clone(),
            delegate_finished.clone(),
        ],
        vec![d_work.clone(), d_review.clone()],
    );
    let mut world = World::start(Daemon::scripted().state(initial));
    world.open_desktop();

    let screen = world.screen();
    assert!(
        screen
            .find(Target::key("sidebar.desktop.auto-review"))
            .is_none(),
        "an unslotted automation desktop holding only the run and its delegates must stay in Automations"
    );
    assert!(
        screen
            .find(Target::key("sidebar.session.del-wait"))
            .is_some()
    );
    assert!(
        screen
            .find(Target::key("sidebar.session.del-work"))
            .is_some()
    );
    assert!(
        screen
            .find(Target::key("sidebar.session.auto-root"))
            .is_none(),
        "finished automation root folds behind finished runs"
    );
    assert!(
        screen
            .find(Target::key("sidebar.session.del-fin"))
            .is_none(),
        "finished delegate folds behind finished runs"
    );
    assert_eq!(
        screen
            .get(Target::key("sidebar.automations.finished"))
            .name
            .as_deref(),
        Some("2 finished runs")
    );
    assert!(
        screen
            .get(Target::key("sidebar.automations.needing"))
            .name
            .as_deref()
            .is_some_and(|n| n.starts_with("1 waiting"))
    );

    // Clicking the waiting chip and pressing Cmd+Shift+J both open the waiting delegate.
    world.gesture(|app| app.click(Target::key("sidebar.automations.needing")));
    let shown = world.daemon.sent_of("desktop_show_session");
    assert_eq!(
        shown.last().and_then(|m| m["session_id"].as_str()),
        Some("del-wait")
    );
    world.gesture(|app| app.key(chord(attn_app::Action::SessionNextRun)));
    let shown = world.daemon.sent_of("desktop_show_session");
    assert_eq!(shown.len(), 2);
    assert_eq!(
        shown.last().and_then(|m| m["session_id"].as_str()),
        Some("del-wait")
    );

    // Waiting automation delegates expose the row settle control in desktop flow.
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.session.del-wait.settle"))
            .is_some(),
        "a waiting automation delegate must expose its row settle control"
    );
    world.gesture(|app| app.click(Target::key("sidebar.session.del-wait.settle")));
    let settled = world.daemon.sent_of("settle_turn");
    assert_eq!(
        settled.last().and_then(|m| m["session_id"].as_str()),
        Some("del-wait")
    );

    // Adding a manual (non-delegated) session promotes the desktop into Desktops.
    let mut manual = session("manual-1", "working");
    manual.label = "Manual debug".into();
    let d_mixed = desktop(
        "auto-review",
        "b",
        "Review main",
        vec![
            pane("auto-review", "p-root", "auto-root"),
            pane("auto-review", "p-work-del", "del-work"),
            pane("auto-review", "p-manual", "manual-1"),
        ],
    );
    world.daemon.emit(attn_protocol::SessionsUpdatedMessage {
        sessions: Some(vec![
            work_agent.clone(),
            auto_root.clone(),
            delegate_working.clone(),
            manual,
        ]),
        ..attn_protocol::SessionsUpdatedMessage::new()
    });
    world
        .daemon
        .emit(attn_protocol::ProfileArrangementChangedMessage {
            profile: Profile {
                id: "work".into(),
                name: "Work".into(),
                current_desktop_id: "d1".into(),
                ..Profile::default()
            },
            desktops: vec![d_work.clone(), d_mixed],
            ..attn_protocol::ProfileArrangementChangedMessage::new()
        });
    world.settle();
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.desktop.auto-review"))
            .is_some(),
        "a manual session on an automation desktop promotes it to Desktops"
    );

    // Closing the automation root leaves the surviving delegate visible under Desktops.
    let d_orphaned = desktop(
        "auto-review",
        "b",
        "Review main",
        vec![pane("auto-review", "p-work-del", "del-work")],
    );
    world.daemon.emit(attn_protocol::SessionsUpdatedMessage {
        sessions: Some(vec![work_agent, delegate_working]),
        ..attn_protocol::SessionsUpdatedMessage::new()
    });
    world
        .daemon
        .emit(attn_protocol::ProfileArrangementChangedMessage {
            profile: Profile {
                id: "work".into(),
                name: "Work".into(),
                current_desktop_id: "d1".into(),
                ..Profile::default()
            },
            desktops: vec![d_work, d_orphaned],
            ..attn_protocol::ProfileArrangementChangedMessage::new()
        });
    world.settle();
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.desktop.auto-review"))
            .is_some(),
        "once the automation root closes, a surviving delegate surfaces under Desktops"
    );
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.session.del-work"))
            .is_some()
    );
    world.advance(std::time::Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}
