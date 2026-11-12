use attn_protocol::{Desktop, DesktopPane, InitialStateMessage, Profile};
use attn_testkit::{Daemon, Target, World};
use serde_json::json;

fn pane(id: &str) -> DesktopPane {
    DesktopPane {
        desktop_id: "d1".into(),
        pane_id: id.into(),
        runtime_id: format!("r-{id}"),
        title: format!("Agent {id}"),
        kind: "agent".into(),
        ..DesktopPane::default()
    }
}

fn two_tiles() -> World {
    World::start(Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: "d1".into(),
            ..Profile::default()
        }]),
        desktops: Some(vec![Desktop {
            id: "d1".into(),
            name: "d1".into(),
            active_pane_id: "t1".into(),
            panes: vec![pane("t1"), pane("t2")],
            profile_id: "work".into(),
            ..Desktop::default()
        }]),
        ..InitialStateMessage::new()
    }))
}

fn keyboard(world: &mut World) -> serde_json::Value {
    world.automate("state", json!({"of": "window"})).unwrap()["keyboard"]["pane"].clone()
}

#[test]
fn a_press_aimed_by_role_and_name_lands_on_that_element_and_says_so() {
    let mut world = two_tiles();
    assert_eq!(keyboard(&mut world), "t1");

    let receipt = world
        .automate(
            "press",
            json!({"target": {"role": "group", "name": "Agent t2"}}),
        )
        .unwrap();

    assert_eq!(receipt["matched"], true, "{receipt}");
    assert_eq!(receipt["target"]["key"], "tile.t2");
    assert_eq!(keyboard(&mut world), "t2", "the press took the keyboard");
    let inside = world
        .automate("hover", json!({"target": {"key": "tiles"}}))
        .unwrap();
    assert_eq!(
        inside["matched"], true,
        "a point on a child is inside its parent: {inside}"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_chord_and_an_action_id_reach_the_same_action() {
    let mut world = two_tiles();
    let sidebar = json!({"target": {"role": "navigation", "name": "Sidebar"}});

    world
        .automate("key", json!({"chord": toggle_sidebar()}))
        .unwrap();
    world
        .automate("wait", json!({"target": sidebar["target"], "gone": true}))
        .unwrap();
    world
        .automate("run", json!({"name": "session.toggleSidebar"}))
        .unwrap();

    assert_eq!(world.automate("wait", sidebar).unwrap()["key"], "sidebar");
}

fn toggle_sidebar() -> &'static str {
    if cfg!(target_os = "macos") {
        "mod+b"
    } else {
        "mod+alt+b"
    }
}

#[test]
fn a_request_names_what_it_cannot_find_or_do() {
    let mut world = two_tiles();

    let missing = world
        .automate("press", json!({"target": {"key": "tile.t9"}}))
        .unwrap_err();
    assert!(missing.contains("tile.t9"), "{missing}");

    let unknown = world
        .automate("run", json!({"name": "tile.nope"}))
        .unwrap_err();
    assert!(
        unknown.contains("tile.nope") && unknown.contains("session.toggleSidebar"),
        "{unknown}"
    );

    let slices = world
        .automate("state", json!({"of": "garden"}))
        .unwrap_err();
    assert!(slices.contains("sessions"), "{slices}");

    assert!(
        world.screen().find(Target::key("tile.t1")).is_some(),
        "a failed request changes nothing"
    );
}
