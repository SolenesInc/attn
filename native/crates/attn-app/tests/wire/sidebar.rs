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

fn keys_of(action: Action) -> String {
    let keymap = Keymap::from_setting(None, KeyPlatform::current());
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
        [
            format!("{} PRs", keys_of(Action::DockAttention)),
            format!("{} sidebar", keys_of(Action::SessionToggleSidebar)),
        ],
        "a chip for an agent's keys waits for an agent on screen"
    );
    world.gesture(|app| app.key(chord(Action::DesktopSelect1)));
    let docked = [
        format!("{} PRs", keys_of(Action::DockAttention)),
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
            serde_json::json!({"target": {"key": "sidebar.dock.chip.dock.attention"}}),
        )
        .unwrap();
    let prs = world
        .screen()
        .get(Target::key("sidebar.dock.chip.dock.attention"));
    assert_eq!(prs.value.as_deref(), Some("active"));
    assert!(world.screen().find(Target::key("attention")).is_some());

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
    assert_eq!(config["dock"]["items"].as_array().unwrap().len(), 6);
    world.gesture(|app| app.click(Target::role("button").name("Show dock")));
    assert_eq!(dock_chips(&world).len(), 6);

    world
        .automate(
            "press",
            serde_json::json!({"target": {"key": "sidebar.overview"}}),
        )
        .unwrap();
    assert!(
        world
            .screen()
            .find(Target::key("overview.dialog"))
            .is_some()
    );
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

    let keymap = Keymap::from_setting(Some(rebound), KeyPlatform::current());
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
    assert_eq!(lead(&world), ("Claude · working".into(), 14.0, 14.0));

    world.daemon.emit(attn_protocol::SettingsUpdatedMessage {
        settings: Some(BTreeMap::from([(
            "sidebar_harness_logos_enabled".to_owned(),
            "false".to_owned(),
        )])),
        ..attn_protocol::SettingsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(lead(&world), ("Claude · working".into(), 8.0, 8.0));
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
    let keymap = Keymap::from_setting(None, KeyPlatform::current());
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
        inside(popover, refused) && refused[3] > 1.5 * rune::tok::type_::LABEL.line_px(),
        "the refusal wraps onto more lines inside the popover: {refused:?} in {popover:?}"
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
fn an_agent_row_names_its_pull_request_and_marks_the_chief_and_what_the_chief_delegated() {
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

    let world = World::start(Daemon::scripted().state(state(
        vec![
            Session {
                chief_of_staff: Some(true),
                ..session("chief", "idle")
            },
            Session {
                delegated_from_chief: Some(true),
                ..session("s1", "idle")
            },
            session("s2", "idle"),
        ],
        vec![desktop(
            "d1",
            "a",
            "Work",
            vec![
                pane("d1", "p1", "chief"),
                pane("d1", "p2", "s1"),
                pane("d1", "p3", "s2"),
            ],
        )],
    )));
    let label = |key: &str| world.screen().find(Target::key(key)).and_then(|n| n.name);
    assert_eq!(
        label("sidebar.session.chief.chief").as_deref(),
        Some("Chief of staff")
    );
    assert_eq!(
        label("sidebar.session.s1.delegated").as_deref(),
        Some("Delegated from chief of staff")
    );
    assert_eq!(label("sidebar.session.s2.delegated"), None);
    assert_eq!(label("sidebar.session.s2.chief"), None);
}

#[test]
fn an_agent_row_says_its_harness_on_hover_and_explains_an_unknown_state_the_daemon_gave_a_reason_for()
 {
    let cases = [
        (
            "stuck",
            "Claude · Stuck — the agent has stopped reporting anything at all",
        ),
        ("no_evidence", "Claude · No signal from this agent yet"),
        ("some_future_clause", "Claude · unknown"),
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
    let world = World::start(Daemon::scripted().state(state(
        vec![run],
        vec![desktop("d1", "a", "One", vec![pane("d1", "p1", "run-a")])],
    )));
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
