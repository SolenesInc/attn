use std::time::Duration;

use attn_testkit::{Daemon, Target, World};
use serde_json::{Value, json};

use super::queue::one_desktop;
use super::sidebar::session;

const HEARTBEAT: Duration = Duration::from_secs(30);

fn launch() -> World {
    World::start(Daemon::scripted().state(one_desktop(vec![session("s1", "working")])))
}

fn reports(world: &World) -> Vec<Value> {
    world.daemon.sent_of("set_client_presence")
}

#[test]
fn reports_that_home_is_on_screen_as_soon_as_it_connects_and_keeps_reporting_while_nothing_changes()
{
    let mut world = launch();
    assert_eq!(
        reports(&world),
        [json!({"cmd": "set_client_presence", "visible": true, "dashboard_visible": true})]
    );

    world.advance(HEARTBEAT);
    world.advance(HEARTBEAT);

    assert_eq!(reports(&world).len(), 3);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn reports_again_on_each_new_connection() {
    let mut world = launch();

    world.reconnect();

    assert_eq!(reports(&world).len(), 2);
}

#[test]
fn reports_the_moment_the_window_is_hidden_without_waiting_for_the_heartbeat() {
    let mut world = launch();

    world.cover(true);

    assert_eq!(
        reports(&world)[1..],
        [json!({"cmd": "set_client_presence", "visible": false, "dashboard_visible": true})]
    );
}

#[test]
fn reports_leaving_home_for_an_agent() {
    let mut world = launch();

    world.gesture(|app| app.click(Target::key("home.session.s1")));

    assert_eq!(
        reports(&world).last().unwrap(),
        &json!({"cmd": "set_client_presence", "visible": true, "dashboard_visible": false, "idle_seconds": 0.0})
    );
}

#[test]
fn says_how_long_the_user_has_been_idle_only_once_it_has_seen_input() {
    let mut world = launch();
    world.gesture(|app| app.move_to((400.0, 300.0)));

    world.advance(HEARTBEAT);

    let reports = reports(&world);
    assert!(reports[0].get("idle_seconds").is_none());
    assert_eq!(reports.last().unwrap()["idle_seconds"], json!(30.0));
}

#[test]
fn collapses_a_burst_of_input_into_a_single_report() {
    let mut world = launch();
    world.advance(Duration::from_secs(11));
    let before = reports(&world).len();

    world.gesture(|app| {
        for x in 0..20 {
            app.move_to((400.0 + x as f32, 300.0));
        }
    });

    assert_eq!(reports(&world).len() - before, 1);
    assert_eq!(reports(&world).last().unwrap()["idle_seconds"], json!(0.0));
}
