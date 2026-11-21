use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_protocol::{
    InitialStateMessage, Profile, SenderPairDecideResultMessage, SenderPairingRequest,
    SendersUpdatedMessage,
};
use attn_testkit::{Daemon, Target, World, message};
use rune::{Key, KeyEvent, Mods};
use serde_json::Value;

const DIALOG: &str = "sender-pairing-dialog";

fn request(id: &str, name: &str, code: &str) -> SenderPairingRequest {
    SenderPairingRequest {
        id: id.into(),
        name: name.into(),
        code: code.into(),
        requested_at: "2026-11-02T10:00:00Z".into(),
    }
}

fn deploy_bot() -> SenderPairingRequest {
    request("pair-1", "deploy-bot", "4821")
}

fn senders(request: Option<SenderPairingRequest>) -> Value {
    message(SendersUpdatedMessage {
        request,
        ..SendersUpdatedMessage::new()
    })
}

type Waiting = Arc<Mutex<Option<SenderPairingRequest>>>;

fn home(waiting: Option<SenderPairingRequest>) -> (Daemon, Waiting) {
    let daemon = Daemon::scripted().state(InitialStateMessage {
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        selected_profile_id: Some("work".into()),
        ..InitialStateMessage::new()
    });
    let waiting = Arc::new(Mutex::new(waiting));
    let read = waiting.clone();
    daemon.on("senders_get", move |_| {
        senders(read.lock().unwrap().clone())
    });
    (daemon, waiting)
}

fn dialog(world: &World) -> Option<String> {
    world.screen().find(Target::key(DIALOG))?.name
}

fn said(world: &World, key: &str) -> Option<String> {
    world.screen().find(Target::key(key))?.name
}

fn focused(world: &World) -> Option<String> {
    world.screen().focused().and_then(|n| n.key)
}

fn press(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

fn decisions(world: &World) -> Vec<(String, bool)> {
    world
        .daemon
        .sent_of("sender_pair_decide")
        .iter()
        .map(|r| {
            (
                r["pairing_id"].as_str().unwrap().to_owned(),
                r["allow"].as_bool().unwrap(),
            )
        })
        .collect()
}

fn rest(world: &mut World) {
    world.advance(Duration::from_secs(10));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn asks_about_a_waiting_program_with_its_code_keyboard_first_and_allows_it_with_the_keys() {
    let (daemon, _) = home(None);
    daemon.on("sender_pair_decide", |_| {
        vec![
            message(SenderPairDecideResultMessage {
                success: true,
                ..SenderPairDecideResultMessage::new()
            }),
            senders(Some(request("pair-2", "backup-script", "0937"))),
        ]
    });
    let mut world = World::start(daemon);
    assert_eq!(dialog(&world), None);

    world.daemon.emit(SendersUpdatedMessage {
        request: Some(deploy_bot()),
        ..SendersUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(
        dialog(&world).as_deref(),
        Some("Allow “deploy-bot” to send messages as you?")
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key(DIALOG))
            .description
            .as_deref(),
        Some(
            "This program will be able to send files and messages that agents receive as your own, to the sessions of profile “Work”."
        )
    );
    assert_eq!(
        said(&world, "sender-pairing.match.code").as_deref(),
        Some("4821")
    );
    assert_eq!(
        said(&world, "sender-pairing.match.hint").as_deref(),
        Some("Check that the program shows the same code.")
    );
    assert_eq!(focused(&world).as_deref(), Some("sender-pairing.deny"));

    press(&mut world, Key::Escape);
    assert!(
        dialog(&world).is_some(),
        "Escape leaves the request waiting"
    );
    world.gesture(|app| app.click_at((20.0, 20.0)));
    assert!(
        dialog(&world).is_some(),
        "a click outside leaves it waiting"
    );

    press(&mut world, Key::Tab);
    assert_eq!(focused(&world).as_deref(), Some("sender-pairing.allow"));
    press(&mut world, Key::Enter);
    assert_eq!(decisions(&world), [("pair-1".to_owned(), true)]);
    assert_eq!(
        dialog(&world).as_deref(),
        Some("Allow “backup-script” to send messages as you?")
    );
    assert_eq!(
        said(&world, "sender-pairing.match.code").as_deref(),
        Some("0937")
    );
    assert_eq!(focused(&world).as_deref(), Some("sender-pairing.deny"));

    world.daemon.emit(SendersUpdatedMessage::new());
    world.settle();
    assert_eq!(dialog(&world), None);
    rest(&mut world);
}

#[test]
fn deny_sends_a_refusal_and_a_failed_decision_keeps_the_request_with_its_reason() {
    let (daemon, _) = home(Some(deploy_bot()));
    daemon.on("sender_pair_decide", |_| {
        message(SenderPairDecideResultMessage {
            success: false,
            error: Some(
                "pairing pair-1 is no longer waiting; the program may have stopped asking".into(),
            ),
            ..SenderPairDecideResultMessage::new()
        })
    });
    let mut world = World::start(daemon);
    world.settle();
    assert_eq!(focused(&world).as_deref(), Some("sender-pairing.deny"));

    press(&mut world, Key::Text(" ".into()));
    assert_eq!(decisions(&world), [("pair-1".to_owned(), false)]);
    let error = world.screen().get(Target::key("sender-pairing.error"));
    assert_eq!(error.role, "alert");
    assert_eq!(
        error.name.as_deref(),
        Some("pairing pair-1 is no longer waiting; the program may have stopped asking")
    );
    assert!(dialog(&world).is_some());
    assert_eq!(
        focused(&world).as_deref(),
        Some("sender-pairing.deny"),
        "Deny keeps the keyboard through the refusal"
    );

    world.gesture(|app| app.click(Target::key("sender-pairing.allow")));
    assert_eq!(
        decisions(&world),
        [("pair-1".to_owned(), false), ("pair-1".to_owned(), true)]
    );
    rest(&mut world);
}

#[test]
fn a_reconnected_app_learns_of_a_program_that_asked_while_it_was_away() {
    let (daemon, waiting) = home(None);
    let mut world = World::start(daemon);
    world.settle();
    assert_eq!(dialog(&world), None);

    *waiting.lock().unwrap() = Some(deploy_bot());
    world.reconnect();
    world.settle();
    assert_eq!(
        dialog(&world).as_deref(),
        Some("Allow “deploy-bot” to send messages as you?")
    );
    assert_eq!(world.daemon.sent_of("senders_get").len(), 2);
}
