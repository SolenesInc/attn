use std::sync::{Arc, Mutex};

use attn_app::Action;
use attn_protocol::{PairedSender, SenderUnpairResultMessage, SendersUpdatedMessage};
use attn_testkit::{Daemon, Target, World, message};
use rune::Key;
use serde_json::Value;

use super::{accepting, click, edged_as_error, named, open, press, press_action, rest, tab_to};

const ROW: &str = "settings.sender.sender-1";

fn deploy_bot(last_used: Option<&str>) -> PairedSender {
    PairedSender {
        id: "sender-1".into(),
        name: "deploy-bot".into(),
        created_at: "2026-11-01T09:00:00Z".into(),
        last_used_at: last_used.map(str::to_owned),
    }
}

fn listing(senders: Vec<PairedSender>) -> Value {
    message(SendersUpdatedMessage {
        senders,
        ..SendersUpdatedMessage::new()
    })
}

type Listed = Arc<Mutex<Vec<PairedSender>>>;

fn home(senders: Vec<PairedSender>) -> (Daemon, Listed) {
    let (daemon, _) = accepting(&[]);
    let listed = Arc::new(Mutex::new(senders));
    let read = listed.clone();
    daemon.on("senders_get", move |_| {
        listing(read.lock().unwrap().clone())
    });
    (daemon, listed)
}

fn on_senders(world: &mut World) {
    open(world);
    click(world, "settings.nav.senders");
}

fn copy(world: &World) -> String {
    named(world, &format!("{ROW}.copy")).unwrap_or_default()
}

#[test]
fn lists_a_paired_sender_and_removes_it() {
    let (daemon, _) = home(vec![deploy_bot(Some("2026-11-02T10:00:00Z"))]);
    daemon.on("sender_unpair", |_| {
        vec![
            message(SenderUnpairResultMessage {
                success: true,
                ..SenderUnpairResultMessage::new()
            }),
            listing(vec![]),
        ]
    });
    let mut world = World::start(daemon);
    on_senders(&mut world);
    assert_eq!(named(&world, ROW).as_deref(), Some("deploy-bot"));
    assert!(copy(&world).starts_with("Paired "));
    assert!(copy(&world).contains(" · Last used "));

    click(&mut world, &format!("{ROW}.remove"));
    let removed: Vec<_> = world
        .daemon
        .sent_of("sender_unpair")
        .iter()
        .map(|r| r["sender"].as_str().unwrap().to_owned())
        .collect();
    assert_eq!(removed, ["sender-1"]);
    assert!(world.screen().find(Target::key(ROW)).is_none());
    assert_eq!(
        named(&world, "settings.senders.empty").as_deref(),
        Some("No paired senders. A program pairs with attn send pair --name <name>.")
    );
    rest(&mut world);
}

#[test]
fn shows_why_removing_a_sender_failed_and_sends_one_removal_for_a_double_press() {
    let (daemon, _) = home(vec![deploy_bot(None)]);
    daemon.on("sender_unpair", |_| ());
    let mut world = World::start(daemon);
    on_senders(&mut world);
    assert!(copy(&world).ends_with(" · Never used"));

    click(&mut world, &format!("{ROW}.remove"));
    click(&mut world, &format!("{ROW}.remove"));
    let sent = world.daemon.sent_of("sender_unpair");
    assert_eq!(sent.len(), 1);
    world.daemon.reply_to(
        &sent[0],
        SenderUnpairResultMessage {
            success: false,
            error: Some(r#"no paired sender named "deploy-bot""#.into()),
            ..SenderUnpairResultMessage::new()
        },
    );
    world.settle();
    let error = world.screen().get(Target::key("settings.senders.error"));
    assert_eq!(error.role, "alert");
    assert_eq!(
        error.name.as_deref(),
        Some(r#"no paired sender named "deploy-bot""#)
    );
    assert!(edged_as_error(&world, "settings.senders.error"));
}

fn refusing_two() -> World {
    let ci = PairedSender {
        id: "sender-2".into(),
        name: "ci".into(),
        ..deploy_bot(None)
    };
    let (daemon, _) = home(vec![deploy_bot(None), ci]);
    daemon.on("sender_unpair", |_| {
        message(SenderUnpairResultMessage {
            success: false,
            error: Some("the daemon could not write the senders file: permission denied".into()),
            ..SenderUnpairResultMessage::new()
        })
    });
    let mut world = World::start(daemon);
    on_senders(&mut world);
    world
}

#[test]
fn a_refused_removal_keeps_the_keyboard_on_remove_and_space_sends_it_again() {
    let mut world = refusing_two();
    let remove = format!("{ROW}.remove");
    tab_to(&mut world, &remove);
    press(&mut world, Key::Text(" ".into()));
    assert!(named(&world, "settings.senders.error").is_some());
    assert_eq!(
        world.screen().focused().and_then(|n| n.key).as_deref(),
        Some(remove.as_str()),
        "Remove keeps the keyboard through the refusal"
    );
    press(&mut world, Key::Text(" ".into()));
    assert_eq!(world.daemon.sent_of("sender_unpair").len(), 2);
    rest(&mut world);
}

#[test]
fn a_refusal_appearing_moves_no_sender_row() {
    let mut world = refusing_two();
    let remove = format!("{ROW}.remove");
    tab_to(&mut world, &remove);
    let rec = world.record(60.0, 1.0, |_, app| {
        app.key(rune::KeyEvent {
            key: Key::Text(" ".into()),
            mods: rune::Mods::default(),
        })
    });
    assert!(named(&world, "settings.senders.error").is_some());
    let from = rec.inputs[0].0;
    let track = rec
        .tracks
        .iter()
        .find(|t| t.path.ends_with("/settings.sender.sender-2"))
        .expect("the second sender is on screen");
    let places: Vec<[f32; 4]> = track
        .frames
        .iter()
        .filter(|(k, _)| *k >= from)
        .map(|(_, s)| s.rect)
        .collect();
    let moves = places.windows(2).filter(|w| w[0] != w[1]).count();
    assert!(
        moves <= 1,
        "the second sender moved {moves} times on its way to its place: {places:?}"
    );
    rest(&mut world);
}

#[test]
fn shows_a_senders_latest_use_when_settings_reopens() {
    let (daemon, listed) = home(vec![deploy_bot(None)]);
    let mut world = World::start(daemon);
    on_senders(&mut world);
    assert!(copy(&world).ends_with("Never used"));

    *listed.lock().unwrap() = vec![deploy_bot(Some("2026-11-02T15:00:00Z"))];
    press_action(&mut world, Action::UiOpenSettings);
    press_action(&mut world, Action::UiOpenSettings);
    assert!(copy(&world).contains("Last used "));
}
