use attn_app::Action;
use attn_testkit::{Daemon, Target, World};
use serde_json::{Value, json};

use super::queue::{changes, daemon_shows, one_desktop, queue, turn};
use super::sidebar::{chord, session};

fn launch(analytics: &str) -> World {
    let mut state = queue(one_desktop(vec![turn("s1", 120), turn("s2", 120)]));
    state
        .settings
        .get_or_insert_default()
        .insert("analytics.enabled".into(), analytics.into());
    World::start(Daemon::scripted().state(state))
}

fn use_the_app(world: &mut World) {
    world.gesture(|app| app.click(Target::key("queue-turn.s1")));
    changes(world, session("s1", "idle"));
    world.gesture(|app| app.key(chord(Action::UiOpenSettings)));
    world.gesture(|app| app.click(Target::key("settings.nav.desktop")));
}

fn app_actions(world: &World) -> Vec<Value> {
    world
        .daemon
        .sent_of("record_analytics")
        .iter()
        .map(|r| json!({ "stream": r["stream"], "name": r["name"], "props": r["props"] }))
        .collect()
}

#[test]
fn nothing_is_recorded_while_analytics_is_off() {
    let mut world = launch("false");
    use_the_app(&mut world);

    assert!(
        world
            .screen()
            .find(Target::key("settings-dialog"))
            .is_some()
    );
    assert_eq!(app_actions(&world), Vec::<Value>::new());
}

#[test]
fn shortcuts_settings_sections_and_queue_advances_are_recorded_with_their_wait_while_analytics_is_on()
 {
    let mut world = launch("true");
    use_the_app(&mut world);

    let recorded = app_actions(&world);
    assert_eq!(
        recorded,
        [
            json!({ "stream": "app", "name": "queue_advance", "props": { "reason": "turn_closed", "to": "session", "wait_ms": 2 * 60 * 60 * 1000 } }),
            json!({ "stream": "app", "name": "settings_section", "props": { "section": "connectivity" } }),
            json!({ "stream": "app", "name": "shortcut", "props": { "id": "ui.openSettings" } }),
            json!({ "stream": "app", "name": "settings_section", "props": { "section": "desktop" } }),
        ]
    );
}

fn advances(world: &World) -> Vec<Value> {
    app_actions(world)
        .into_iter()
        .filter(|r| r["name"] == "queue_advance")
        .map(|r| r["props"].clone())
        .collect()
}

fn sections(world: &World) -> Vec<Value> {
    app_actions(world)
        .into_iter()
        .filter(|r| r["name"] == "settings_section")
        .map(|r| r["props"]["section"].clone())
        .collect()
}

#[test]
fn the_queue_records_going_home_after_the_last_turn_and_following_the_next_one_there() {
    let (s1, s2) = (turn("s1", 120), turn("s2", 120));
    let mut world = launch("true");
    world.gesture(|app| app.click(Target::key("queue-turn.s1")));
    changes(&mut world, session("s1", "idle"));
    daemon_shows(&mut world, &[&s1, &s2], "s2");
    changes(&mut world, session("s2", "idle"));
    changes(&mut world, turn("s1", 0));

    assert_eq!(
        advances(&world),
        [
            json!({ "reason": "turn_closed", "to": "session", "wait_ms": 2 * 60 * 60 * 1000 }),
            json!({ "reason": "turn_closed", "to": "dashboard" }),
            json!({ "reason": "follow", "to": "session", "wait_ms": 0 }),
        ]
    );
}

#[test]
fn settings_records_each_opening_and_each_new_section_but_not_the_section_already_shown() {
    let mut world = launch("true");
    world.gesture(|app| app.key(chord(Action::UiOpenSettings)));
    world.gesture(|app| app.click(Target::key("settings.nav.desktop")));
    world.gesture(|app| app.click(Target::key("settings.nav.desktop")));
    world.gesture(|app| app.key(chord(Action::UiOpenSettings)));
    world.gesture(|app| app.key(chord(Action::UiOpenSettings)));

    assert_eq!(
        sections(&world),
        [json!("connectivity"), json!("desktop"), json!("desktop")]
    );
}
