use std::collections::BTreeMap;
use std::time::Duration;

use attn_protocol::{AppPerformanceRequestMessage, InitialStateMessage, SettingsUpdatedMessage};
use attn_testkit::{Daemon, World};
use serde_json::Value;

fn analytics(on: bool) -> Option<BTreeMap<String, String>> {
    Some(BTreeMap::from([(
        "analytics.enabled".to_string(),
        on.to_string(),
    )]))
}

fn starting_with_analytics(on: bool) -> World {
    let daemon = Daemon::scripted().state(InitialStateMessage {
        settings: analytics(on),
        ..InitialStateMessage::new()
    });
    daemon.on("app_performance_result", |_| ());
    World::start(daemon)
}

fn ask(world: &mut World, id: &str) -> Value {
    world.daemon.emit(AppPerformanceRequestMessage {
        request_id: id.into(),
        ..AppPerformanceRequestMessage::new()
    });
    world.settle();
    world
        .daemon
        .sent_of("app_performance_result")
        .into_iter()
        .find(|answer| answer["request_id"] == id)
        .expect("the app answered the daemon's request")["performance"]
        .clone()
}

#[test]
fn the_app_answers_a_performance_request_with_what_it_measured_since_it_last_answered() {
    let mut world = starting_with_analytics(true);
    ask(&mut world, "first");

    for ms in [1.0, 2.0, 2.0, 3.0, 40.0] {
        world.performance.drawn(ms);
    }
    world.performance.unchanged();
    world.performance.skipped();
    world.performance.skipped();
    world.advance(Duration::from_secs(1200));
    let commits = world.runtime().stats.commits;
    let measured = ask(&mut world, "second");

    assert_eq!(measured["period_ms"], 1_200_000);
    assert_eq!(
        (
            &measured["frames_drawn"],
            &measured["frames_unchanged"],
            &measured["frames_skipped"]
        ),
        (&Value::from(5), &Value::from(1), &Value::from(2))
    );
    let frame_ms = &measured["frame_ms"];
    let near = |field: &str, want: f64| {
        let got = frame_ms[field].as_f64().expect(field);
        assert!(
            (got - want).abs() <= want * 0.02,
            "{field} = {got}, want {want}"
        );
    };
    near("p50", 2.0);
    near("p95", 40.0);
    near("p99", 40.0);
    assert_eq!(frame_ms["max"], 40.0);
    assert!(measured["cpu_ms"].as_i64().expect("cpu_ms") >= 0);
    assert_eq!(
        world.runtime().stats.commits,
        commits,
        "answering changed nothing in the window"
    );

    let next = ask(&mut world, "third");
    assert_eq!(
        (
            &next["period_ms"],
            &next["frames_drawn"],
            &next["frames_skipped"]
        ),
        (&Value::from(0), &Value::from(0), &Value::from(0))
    );
    assert!(
        next.get("frame_ms").is_none(),
        "no frame times without drawn frames: {next}"
    );
}

#[test]
fn a_performance_answer_holds_nothing_from_before_analytics_turned_on() {
    let mut world = starting_with_analytics(false);
    for ms in [1.0, 2.0, 30.0] {
        world.performance.drawn(ms);
    }
    world.performance.unchanged();
    world.performance.skipped();
    world.advance(Duration::from_secs(600));

    world.daemon.emit(SettingsUpdatedMessage {
        settings: analytics(true),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    world.advance(Duration::from_secs(60));
    world.performance.drawn(4.0);
    let measured = ask(&mut world, "after turning on");

    assert_eq!(
        (
            &measured["period_ms"],
            &measured["frames_drawn"],
            &measured["frames_unchanged"],
            &measured["frames_skipped"],
            &measured["frame_ms"]["max"],
        ),
        (
            &Value::from(60_000),
            &Value::from(1),
            &Value::from(0),
            &Value::from(0),
            &Value::from(4.0),
        ),
        "the answer covers only the time since analytics turned on"
    );
}
