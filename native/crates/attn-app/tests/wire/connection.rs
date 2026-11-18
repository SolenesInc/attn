use std::time::Duration;

use attn_client::{Frame, KittyFormat};
use attn_protocol::{InitialStateMessage, PROTOCOL_VERSION};
use attn_testkit::{CLIENT_TOKEN, Daemon, Target, World};

fn ms(n: u64) -> Duration {
    Duration::from_millis(n)
}

fn refusal(world: &World) -> String {
    world
        .screen()
        .get(Target::key("connection.refused"))
        .name
        .unwrap_or_default()
}

#[test]
fn the_app_says_hello_with_its_token_and_shows_the_daemon_state() {
    let world = World::start(Daemon::scripted());

    let hello = world.daemon.sent_of("client_hello");
    let sent: Vec<_> = world
        .daemon
        .sent()
        .iter()
        .map(|r| r["cmd"].as_str().unwrap_or_default().to_owned())
        .collect();
    assert_eq!(
        sent,
        [
            "client_hello",
            "senders_get",
            "notification_list",
            "set_terminal_theme",
            "set_client_presence"
        ],
        "the app says hello, then asks which program waits to pair, counts its notifications, gives the daemon its terminal colors and says the user is at Home once the handshake completes"
    );
    let hello = &hello[0];
    assert_eq!(hello["client_kind"], "native-app");
    assert_eq!(hello["client_token"], CLIENT_TOKEN);
    assert_eq!(hello["version"], format!("protocol-{PROTOCOL_VERSION}"));
    assert_eq!(
        hello["capabilities"],
        serde_json::json!(["binary_pty_output", "kitty_images"])
    );
    let screen = world.screen();
    assert!(
        screen
            .find(Target::key("connection.reconnecting"))
            .is_none(),
        "no connection status once connected:\n{screen}"
    );
    assert!(screen.find(Target::role("main").name("Home")).is_some());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_lost_daemon_is_retried_from_one_second_up_to_five() {
    let mut world = World::start(Daemon::scripted());
    world.daemon.refuse_connections(true);

    world.daemon.disconnect();
    world.settle();

    assert!(
        world
            .screen()
            .find(Target::key("connection.reconnecting"))
            .is_some(),
        "{}",
        world.screen()
    );
    assert!(
        world
            .screen()
            .find(Target::role("main").name("Home"))
            .is_some(),
        "the last facts stay on screen while reconnecting"
    );
    for gap in [1000, 1500, 2250, 3375, 5000, 5000] {
        let attempts = world.daemon.attempts();
        world.advance(ms(gap - 1));
        assert_eq!(
            world.daemon.attempts(),
            attempts,
            "no retry before {gap} ms"
        );
        world.advance(ms(1));
        assert_eq!(world.daemon.attempts(), attempts + 1, "a retry at {gap} ms");
    }

    world.daemon.refuse_connections(false);
    world.advance(ms(5000));

    assert_eq!(world.daemon.sent_of("client_hello").len(), 2);
    assert!(
        world
            .screen()
            .find(Target::key("connection.reconnecting"))
            .is_none(),
        "{}",
        world.screen()
    );
    assert!(world.runtime_is_at_rest());
}

fn reconnecting(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("connection.reconnecting"))
        .map(|status| {
            assert_eq!(status.role, "status", "the line is announced as it appears");
            status.name.unwrap_or_default()
        })
}

#[test]
fn a_stopped_daemon_is_started_again_before_the_first_retry() {
    let mut world = World::start(Daemon::scripted());
    assert_eq!(
        world.daemon.starts(),
        1,
        "the app starts the daemon before connecting"
    );

    world.daemon.stop();
    world.settle();
    assert!(reconnecting(&world).is_some(), "{}", world.screen());
    world.advance(ms(999));
    assert_eq!(world.daemon.starts(), 1, "nothing starts before the retry");
    world.advance(ms(1));

    assert_eq!(world.daemon.starts(), 2);
    assert_eq!(world.daemon.sent_of("client_hello").len(), 2);
    assert_eq!(reconnecting(&world), None, "{}", world.screen());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_daemon_that_will_not_start_at_launch_says_why_and_is_started_once_it_can() {
    let daemon = Daemon::scripted();
    daemon.fail_starts(Some(
        "no attnd beside /Applications/attn.app/Contents/MacOS/attn-app",
    ));
    let mut world = World::start(daemon);

    let status = reconnecting(&world).expect("the window says the daemon is not answering");
    assert!(
        status.contains("starting it failed: no attnd beside /Applications/attn.app"),
        "{status}"
    );
    assert_eq!(
        world.daemon.attempts(),
        0,
        "no socket while the daemon cannot start"
    );
    world.daemon.fail_starts(None);
    world.advance(ms(1000));

    assert_eq!(world.daemon.sent_of("client_hello").len(), 1);
    assert_eq!(reconnecting(&world), None, "{}", world.screen());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_daemon_that_fails_to_restart_says_why_and_is_started_at_the_next_retry() {
    let mut world = World::start(Daemon::scripted());
    world
        .daemon
        .fail_starts(Some("the database migration failed"));

    world.daemon.stop();
    world.settle();
    world.advance(ms(1000));

    let status = reconnecting(&world).expect("the app says it is reconnecting");
    assert!(
        status.contains("starting it failed: the database migration failed"),
        "{status}"
    );
    world.daemon.fail_starts(None);
    world.advance(ms(1500));

    assert_eq!(world.daemon.starts(), 3);
    assert_eq!(reconnecting(&world), None, "{}", world.screen());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_reconnect_after_a_handshake_starts_over_at_one_second() {
    let mut world = World::start(Daemon::scripted());
    world.reconnect();

    world.daemon.disconnect();
    world.settle();
    world.advance(ms(999));
    assert_eq!(world.daemon.connections(), 2);
    world.advance(ms(1));

    assert_eq!(world.daemon.connections(), 3);
    assert!(
        world
            .screen()
            .find(Target::key("connection.reconnecting"))
            .is_none(),
        "{}",
        world.screen()
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_daemon_on_another_protocol_stops_the_app_with_both_versions() {
    let state = InitialStateMessage {
        protocol_version: Some("0ld".into()),
        ..InitialStateMessage::new()
    };
    let mut world = World::start(Daemon::scripted().state(state));

    let alert = refusal(&world);
    assert!(
        alert.contains(&format!(
            "The app speaks protocol {PROTOCOL_VERSION} and the daemon speaks 0ld"
        )),
        "{alert}"
    );
    world.advance(Duration::from_secs(60));
    assert_eq!(world.daemon.connections(), 1, "a mismatch is not retried");
    assert_eq!(
        world.daemon.starts(),
        1,
        "the daemon is not started again after a mismatch"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_daemon_of_another_instance_is_refused_before_the_hello() {
    let mut world = World::start(Daemon::scripted().instance("default"));

    let alert = refusal(&world);
    assert!(alert.contains(r#"This app belongs to instance "wire", but the daemon on its port serves instance "default""#), "{alert}");
    world.advance(Duration::from_secs(60));
    assert_eq!(
        world.daemon.attempts(),
        0,
        "the app never opens the daemon's socket"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_refused_token_stops_the_app_with_the_daemon_reason() {
    let mut world = World::start(Daemon::scripted().client_token("another-token"));

    let alert = refusal(&world);
    assert!(
        alert.contains("client_hello refused: client_token does not match"),
        "{alert}"
    );
    world.advance(Duration::from_secs(60));
    assert_eq!(
        world.daemon.connections(),
        1,
        "a refused token is not retried"
    );
}

#[test]
fn binary_frames_reach_the_terminal_side_decoded() {
    let mut world = World::start(Daemon::scripted());
    let mut pty = vec![0x01, 2];
    pty.extend_from_slice(b"t1");
    pty.extend_from_slice(&7u32.to_be_bytes());
    pty.extend_from_slice(b"hi");
    let mut image = vec![0x02, 2];
    image.extend_from_slice(b"t1");
    image.extend_from_slice(&9u32.to_be_bytes());
    image.extend_from_slice(&3u64.to_be_bytes());
    image.extend_from_slice(&1u32.to_be_bytes());
    image.extend_from_slice(&1u32.to_be_bytes());
    image.push(1);
    image.extend_from_slice(&[1, 2, 3, 4]);

    world.daemon.emit_frame(pty);
    world.daemon.emit_frame(vec![0x09, 1, b'x', 0]);
    world.daemon.emit_frame(image);
    world.settle();

    assert_eq!(
        world.take_frames(),
        [
            Frame::PtyOutput {
                terminal: "t1".into(),
                seq: 7,
                data: b"hi".to_vec()
            },
            Frame::KittyImage {
                terminal: "t1".into(),
                image: 9,
                generation: 3,
                width: 1,
                height: 1,
                format: KittyFormat::Rgba,
                pixels: vec![1, 2, 3, 4]
            },
        ]
    );
    assert_eq!(
        world.daemon.sent_of("client_hello").len(),
        1,
        "a bad frame does not cost the connection"
    );
}

#[test]
fn a_command_lost_with_a_hang_up_does_not_stall_the_reconnect() {
    let mut world = World::start(Daemon::scripted());

    world.daemon.disconnect();
    let _ = world
        .link()
        .send(&serde_json::json!({ "cmd": "get_settings" }));
    world.settle();
    world.advance(ms(1000));

    assert_eq!(world.daemon.connections(), 2);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_daemon_on_another_schema_shows_the_mismatch_even_when_its_state_does_not_decode() {
    let daemon = Daemon::scripted();
    daemon.on("client_hello", |_| {
        serde_json::json!({ "event": "initial_state", "protocol_version": "n3w", "sessions": "a shape this build cannot read" })
    });
    let world = World::start(daemon);

    let alert = refusal(&world);
    assert!(
        alert.contains(&format!(
            "The app speaks protocol {PROTOCOL_VERSION} and the daemon speaks n3w"
        )),
        "{alert}"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_missing_daemon_names_its_install_command_without_marks_and_its_path_as_written() {
    let daemon = Daemon::scripted();
    daemon.fail_starts(Some(
        "no attnd beside /Applications/attn-`canary`.app/Contents/MacOS/attn-app; install the app with make install INSTANCE=<name>",
    ));
    let world = World::start(daemon);

    let status = reconnecting(&world).expect("the window says the daemon is not answering");
    assert_eq!(
        status,
        "The daemon is not answering, and starting it failed: no attnd beside /Applications/attn-`canary`.app/Contents/MacOS/attn-app; install the app with make install INSTANCE=<name>. Retrying."
    );
}

const HEALTH_TIMEOUT: &str = "/home/user/.local/share/attn-ensure-paused/bin/attnd daemon ensure failed (exit status: 1): daemon ensure error: daemon pid 2875852 did not answer health (health_timeout=15s); left running; retry, or restart with `ATTN_INSTANCE=ensure-paused attn daemon stop` then `ATTN_INSTANCE=ensure-paused attn daemon ensure` in the same environment: Get \"http://127.0.0.1:41597/health\": context deadline exceeded (Client.Timeout exceeded while awaiting headers)";

fn shown(world: &World, key: &str) -> [f32; 4] {
    let rt = world.runtime();
    let said = rt
        .elements()
        .iter()
        .find(|e| e.key.as_deref() == Some(key))
        .unwrap_or_else(|| panic!("no {key}"))
        .clone();
    let ink = said.text_ink.expect("a text");
    let within = |outer: [f32; 4], inner: [f32; 4]| {
        inner[0] + 0.5 >= outer[0]
            && inner[1] + 0.5 >= outer[1]
            && inner[0] + inner[2] <= outer[0] + outer[2] + 0.5
            && inner[1] + inner[3] <= outer[1] + outer[3] + 0.5
    };
    assert!(
        within(said.visible, said.rect) && within(said.rect, ink),
        "{key} shows in part: {:?} of {:?}, ink {ink:?}",
        said.visible,
        said.rect
    );
    said.rect
}

#[test]
fn an_unresponsive_daemon_s_reason_and_restart_commands_read_whole_at_the_smallest_window() {
    let state = InitialStateMessage {
        settings: Some([("uiScale".to_owned(), "1.5".to_owned())].into()),
        ..InitialStateMessage::new()
    };
    let mut world = World::start_sized(Daemon::scripted().state(state), (800.0, 600.0));
    world.daemon.fail_starts(Some(HEALTH_TIMEOUT));
    world.daemon.stop();
    world.settle();
    world.advance(ms(1000));
    let whole =
        format!("The daemon is not answering, and starting it failed: {HEALTH_TIMEOUT}. Retrying.");
    assert_eq!(reconnecting(&world).as_deref(), Some(whole.as_str()));
    let bar = world.rect("connection");
    let clamped = shown(&world, "connection.reconnecting");

    let details = world.screen().get(Target::key("connection.details"));
    assert_eq!(details.role, "button", "Details is one press away");
    world.gesture(|app| app.click(Target::key("connection.details")));

    let read = world
        .screen()
        .get(Target::key("whole.connection.reconnecting"))
        .name
        .unwrap_or_default();
    assert_eq!(read, whole);
    let whole_said = shown(&world, "whole.connection.reconnecting");
    assert!(
        whole_said[3] * whole_said[2] > clamped[3] * clamped[2] * 1.5,
        "Details shows more of the reason than the bar's lines: {whole_said:?} against {clamped:?}"
    );
    assert_eq!(world.rect("connection"), bar, "Details changed the bar");

    world.daemon.fail_starts(None);
    world.advance(ms(1500));
    let screen = world.screen();
    assert!(
        screen.find(Target::key("connection")).is_none()
            && screen.find(Target::key("connection.whole")).is_none(),
        "the banner and its Details close once the daemon answers:\n{screen}"
    );
    assert!(world.runtime_is_at_rest());
}
