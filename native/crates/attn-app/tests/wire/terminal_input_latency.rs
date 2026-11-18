use std::time::Duration;

use attn_protocol::PtyInputProbeResultMessage;
use attn_testkit::{Answer, Daemon, World};
use serde_json::Value;

use super::terminal::{output, pane, showing};

fn live_terminal(daemon: Daemon) -> World {
    let mut world = World::start(daemon);
    world.open_desktop();
    output(&world, 1, b"$ ");
    world.settle();
    world
}

fn one_agent() -> Daemon {
    let mut agent = pane("t1", "r1");
    agent.session_id = "s1".into();
    Daemon::scripted().state(showing(vec![agent], "daemon-1"))
}

fn reasons(incidents: &[Value]) -> Vec<&str> {
    incidents
        .iter()
        .map(|i| i["reason"].as_str().unwrap())
        .collect()
}

#[test]
fn a_slow_pty_round_trip_logs_an_incident_naming_the_terminal_and_its_latency() {
    let daemon = one_agent();
    daemon.answer_in_turn("pty_input", [Answer::Hold]);
    let mut world = live_terminal(daemon);

    world.gesture(|app| app.type_text("l"));
    let input = world.daemon.sent_of("pty_input")[0].clone();
    assert!(
        input["probe_id"].is_string(),
        "the first key probes the round trip"
    );
    world.advance(Duration::from_millis(300));
    world.daemon.reply_to(
        &input,
        PtyInputProbeResultMessage {
            id: "r1".into(),
            probe_id: input["probe_id"].as_str().unwrap().into(),
            success: true,
            write_duration_us: 1_200,
            ..PtyInputProbeResultMessage::new()
        },
    );
    world.settle();

    let incidents = world.incidents();
    assert_eq!(reasons(&incidents), ["pty_input_round_trip_delay"]);
    let incident = &incidents[0];
    assert_eq!(incident["pane"], "r1");
    assert_eq!(incident["session"], "s1");
    assert_eq!(incident["detail"]["runtimeId"], "r1");
    assert_eq!(incident["detail"]["latencyMs"], 300.0);
    assert_eq!(incident["detail"]["daemonWriteMs"], 1.2);
    assert_eq!(incident["detail"]["transportAndUiMs"], 298.8);
}

#[test]
fn a_quick_round_trip_logs_nothing_and_typing_probes_again_two_seconds_later() {
    let daemon = one_agent();
    daemon.on("pty_input", |request| {
        let probe = request["probe_id"].as_str()?;
        Some(attn_testkit::message(PtyInputProbeResultMessage {
            id: "r1".into(),
            probe_id: probe.into(),
            success: true,
            ..PtyInputProbeResultMessage::new()
        }))
    });
    let mut world = live_terminal(daemon);

    world.gesture(|app| app.type_text("ls"));
    world.advance(Duration::from_secs(2));
    world.gesture(|app| app.type_text("a"));

    let probed: Vec<bool> = world
        .daemon
        .sent_of("pty_input")
        .iter()
        .map(|input| input["probe_id"].is_string())
        .collect();
    assert_eq!(probed, [true, false, true]);
    assert_eq!(world.incidents(), Vec::<Value>::new());
}

#[test]
fn a_key_that_waited_after_it_arrived_logs_an_incident_with_how_long_from_its_native_time() {
    let mut world = live_terminal(one_agent());

    {
        let _arrival = rune::input_arrived(Duration::from_millis(400));
        world.gesture(|app| app.type_text("l"));
    }
    world.gesture(|app| app.type_text("s"));

    let incidents = world.incidents();
    assert_eq!(reasons(&incidents), ["terminal_key_event_delay"]);
    assert_eq!(incidents[0]["pane"], "r1");
    let waited = incidents[0]["detail"]["latencyMs"].as_f64().unwrap();
    assert!(waited >= 400.0, "it waited {waited} ms from its arrival");
}

#[test]
fn a_probe_the_link_dropped_logs_no_timeout_and_typing_probes_again() {
    let daemon = one_agent();
    daemon.on("pty_input", |_| None::<Value>);
    let mut world = live_terminal(daemon);

    world.gesture(|app| app.type_text("l"));
    world.reconnect();
    output(&world, 2, b"l");
    world.settle();
    world.advance(Duration::from_secs(6));
    world.gesture(|app| app.type_text("s"));

    assert_eq!(world.incidents(), Vec::<Value>::new());
    let last = world.daemon.sent_of("pty_input").last().cloned().unwrap();
    assert!(last["probe_id"].is_string(), "the next key probes afresh");
}

#[test]
fn keys_typed_in_a_tile_window_and_the_main_window_never_share_a_trace_id() {
    let mut out = attn_testkit::tree::pane("t1");
    out["window"] = serde_json::json!({"x": 10.0, "y": 20.0, "width": 900.0, "height": 500.0});
    let desktop = attn_protocol::Desktop {
        tree_json: attn_testkit::tree::row([out, attn_testkit::tree::pane("t2")]).to_string(),
        active_pane_id: "t2".into(),
        ..super::terminal::desktop(vec![pane("t1", "r1"), pane("t2", "r2")])
    };
    let daemon = Daemon::scripted().state(attn_protocol::InitialStateMessage {
        desktops: Some(vec![desktop]),
        ..showing(Vec::new(), "daemon-1")
    });
    let mut world = World::start(daemon);
    world.open_desktop();
    output(&world, 1, b"$ ");
    world.settle();
    let window = world.tile_windows()[0].key.clone();

    world.tile_gesture(&window, |app| app.type_text("ab"));
    world.gesture(|app| app.type_text("cd"));

    let sent = world.daemon.sent_of("pty_input");
    let by_terminal: Vec<&str> = sent.iter().map(|i| i["id"].as_str().unwrap()).collect();
    assert!(
        by_terminal.contains(&"r1") && by_terminal.contains(&"r2"),
        "{by_terminal:?}"
    );
    let mut traces: Vec<&str> = sent
        .iter()
        .map(|i| i["trace_id"].as_str().expect("every key is traced"))
        .collect();
    traces.sort_unstable();
    traces.dedup();
    assert_eq!(traces.len(), sent.len(), "{sent:?}");
}
