use attn_protocol::{InitialStateMessage, Profile};
use attn_testkit::{Daemon, World};

#[test]
fn a_reconnected_app_asks_for_the_profile_the_daemon_scoped_it_to() {
    let daemon = Daemon::scripted().state(InitialStateMessage {
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        selected_profile_id: Some("work".into()),
        ..InitialStateMessage::new()
    });
    let mut world = World::start(daemon);

    world.reconnect();

    let hellos = world.daemon.sent_of("client_hello");
    assert_eq!(hellos.len(), 2);
    assert!(hellos[0].get("profile_id").is_none());
    assert_eq!(hellos[1]["profile_id"], "work");
    assert!(world.runtime_is_at_rest());
}
