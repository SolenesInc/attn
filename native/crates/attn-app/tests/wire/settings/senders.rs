use std::sync::{Arc, Mutex};

use attn_app::Action;
use attn_protocol::{PairedSender, SenderUnpairResultMessage, SendersUpdatedMessage};
use attn_testkit::{Daemon, Target, World, message};
use serde_json::Value;

use super::{accepting, click, named, open, press_action, rest};

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
