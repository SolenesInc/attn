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
    assert_eq!(
        world.daemon.sent(),
        hello,
        "the app sends nothing but its hello on connect"
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
    assert!(screen.find(Target::key("placeholder")).is_some());
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
        world.screen().find(Target::key("placeholder")).is_some(),
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
