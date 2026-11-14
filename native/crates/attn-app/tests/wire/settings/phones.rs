use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_protocol::{
    InitialStateMessage, PhoneLink, PhonePairBeginResultMessage, PhonePairCancelResultMessage,
    PhonePairing, PhonePairingCode, PhonePairingRevokeResultMessage, PhonesUpdatedMessage, Profile,
};
use attn_testkit::{Daemon, Target, World, message};
use rune::Key;
use serde_json::Value;

use attn_app::Action;

use super::{click, named, open, press, press_action};

/// Ten minutes after the world's wall clock starts: a fresh code's expiry.
const EXPIRES_AT: &str = "2026-11-12T09:10:00Z";
const PAIR: &str = "settings.phones.work.pair";
const CODE: &str = "settings.phones.work.code";
const QR: &str = "settings.phones.work.code.qr";

fn link() -> PhoneLink {
    PhoneLink {
        state: "connected".into(),
        cloud_url: "https://cloud.attn.codes".into(),
        home_id: Some("home-1".into()),
        ..PhoneLink::default()
    }
}

fn phones(codes: Vec<PhonePairingCode>, pairings: Vec<PhonePairing>) -> PhonesUpdatedMessage {
    PhonesUpdatedMessage {
        link: link(),
        pairing_codes: codes,
        pairings,
        ..PhonesUpdatedMessage::new()
    }
}

fn code() -> PhonePairingCode {
    PhonePairingCode {
        profile_id: "work".into(),
        expires_at: EXPIRES_AT.into(),
        qr: r#"{"v":1,"pairing_id":"p1","secret":"s"}"#.into(),
    }
}

fn phone(connected: bool) -> PhonePairing {
    PhonePairing {
        device_id: "device-1".into(),
        profile_id: "work".into(),
        name: "Victor’s iPhone".into(),
        created_at: "2026-11-02T10:00:00Z".into(),
        last_seen_at: "2026-11-02T10:00:00Z".into(),
        connected,
    }
}

type Listed = Arc<Mutex<PhonesUpdatedMessage>>;

/// A home with phone access on and the profile Work, whose daemon lists `listed` on each read.
fn home(listed: PhonesUpdatedMessage) -> (Daemon, Listed) {
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        settings: Some([("phone_access_enabled".into(), "true".into())].into()),
        ..InitialStateMessage::new()
    });
    let listed = Arc::new(Mutex::new(listed));
    let read = listed.clone();
    daemon.on("phones_get", move |_| message(read.lock().unwrap().clone()));
    (daemon, listed)
}

fn on_phones(world: &mut World) {
    open(world);
    click(world, "settings.nav.phones");
}

fn cancels(world: &World) -> Vec<String> {
    world
        .daemon
        .sent_of("phone_pair_cancel")
        .iter()
        .map(|r| r["profile_id"].as_str().unwrap().to_owned())
        .collect()
}

fn cancelled() -> Value {
    message(PhonePairCancelResultMessage {
        success: true,
        ..PhonePairCancelResultMessage::new()
    })
}

fn shows(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

#[test]
fn shows_the_link_a_pairing_code_for_the_profile_then_the_paired_phone_and_revokes_it() {
    let (daemon, _) = home(phones(vec![], vec![]));
    daemon.on("phone_pair_begin", |_| {
        vec![
            message(PhonePairBeginResultMessage {
                success: true,
                pairing_code: Some(code()),
                ..PhonePairBeginResultMessage::new()
            }),
            message(phones(vec![code()], vec![])),
        ]
    });
    daemon.on("phone_pairing_revoke", |_| {
        vec![
            message(PhonePairingRevokeResultMessage {
                success: true,
                ..PhonePairingRevokeResultMessage::new()
            }),
            message(phones(vec![], vec![])),
        ]
    });
    let mut world = World::start(daemon);
    on_phones(&mut world);
    assert_eq!(
        named(&world, "settings.phones.link").as_deref(),
        Some("Connected to Attn Cloud at https://cloud.attn.codes")
    );
    assert_eq!(
        named(&world, "settings.block.phones.work").as_deref(),
        Some("Phones for Work")
    );

    click(&mut world, PAIR);
    let begun: Vec<_> = world
        .daemon
        .sent_of("phone_pair_begin")
        .iter()
        .map(|r| r["profile_id"].as_str().unwrap().to_owned())
        .collect();
    assert_eq!(begun, ["work"]);
    let qr = world.screen().get(Target::key(QR));
    assert_eq!(qr.role, "image");
    assert_eq!(qr.name.as_deref(), Some("Pairing code for Work"));
    assert_eq!(
        named(&world, PAIR).as_deref(),
        Some("Show a new pairing code")
    );

    world.daemon.emit(phones(vec![], vec![phone(true)]));
    world.settle();
    assert!(!shows(&world, CODE));
    let row = "settings.phone.work.device-1";
    assert_eq!(named(&world, row).as_deref(), Some("Victor’s iPhone"));
    assert_eq!(
        named(&world, &format!("{row}.seen")).as_deref(),
        Some("Connected now")
    );

    click(&mut world, &format!("{row}.revoke"));
    let revoked: Vec<_> = world
        .daemon
        .sent_of("phone_pairing_revoke")
        .iter()
        .map(|r| (r["device_id"].clone(), r["profile_id"].clone()))
        .collect();
    assert_eq!(revoked, [("device-1".into(), "work".into())]);
    assert!(!shows(&world, row));
    assert_eq!(world.daemon.sent_of("phones_get").len(), 1);
    world.advance(Duration::from_secs(10 * 60));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_second_revoke_before_the_answer_sends_nothing() {
    let (daemon, _) = home(phones(vec![], vec![phone(true)]));
    daemon.on("phone_pairing_revoke", |_| ());
    let mut world = World::start(daemon);
    on_phones(&mut world);
    click(&mut world, "settings.phone.work.device-1.revoke");
    click(&mut world, "settings.phone.work.device-1.revoke");
    assert_eq!(world.daemon.sent_of("phone_pairing_revoke").len(), 1);
}

#[test]
fn cancel_takes_the_shown_code_away() {
    let (daemon, listed) = home(phones(vec![code()], vec![]));
    let kept = listed.clone();
    daemon.on("phone_pair_cancel", move |_| {
        *kept.lock().unwrap() = phones(vec![], vec![]);
        vec![cancelled(), message(phones(vec![], vec![]))]
    });
    let mut world = World::start(daemon);
    on_phones(&mut world);
    click(&mut world, &format!("{CODE}.cancel"));
    assert_eq!(cancels(&world), ["work"]);
    assert!(!shows(&world, CODE));
    let pair = world.screen().get(Target::key(PAIR));
    assert_eq!(pair.name.as_deref(), Some("Pair a phone"));
    assert!(!pair.is("disabled"));
}

#[test]
fn leaving_the_page_cancels_the_shown_code_and_staying_does_not() {
    let (daemon, _) = home(phones(vec![code()], vec![]));
    daemon.on("phone_pair_cancel", |_| cancelled());
    let mut world = World::start(daemon);
    on_phones(&mut world);
    world.daemon.emit(phones(vec![code()], vec![phone(true)]));
    world.settle();
    assert!(cancels(&world).is_empty());

    click(&mut world, "settings.nav.experimental");
    assert_eq!(cancels(&world), ["work"]);
}

#[test]
fn closing_the_dialog_cancels_the_shown_code() {
    let (daemon, _) = home(phones(vec![code()], vec![]));
    daemon.on("phone_pair_cancel", |_| cancelled());
    let mut world = World::start(daemon);
    on_phones(&mut world);
    press(&mut world, Key::Escape);
    assert_eq!(cancels(&world), ["work"]);
}

#[test]
fn another_layer_opened_over_settings_cancels_the_shown_code() {
    let (daemon, _) = home(phones(vec![code()], vec![]));
    daemon.on("phone_pair_cancel", |_| cancelled());
    let mut world = World::start(daemon);
    on_phones(&mut world);
    assert!(cancels(&world).is_empty());

    press_action(&mut world, Action::ProfileSwitch);
    assert!(
        world
            .screen()
            .find(Target::key("profile-switcher"))
            .is_some()
    );
    assert_eq!(cancels(&world), ["work"]);
}

#[test]
fn a_code_that_arrives_after_the_person_left_is_cancelled() {
    let (daemon, _) = home(phones(vec![], vec![]));
    daemon.on("phone_pair_begin", |_| ());
    daemon.on("phone_pair_cancel", |_| cancelled());
    let mut world = World::start(daemon);
    on_phones(&mut world);
    click(&mut world, PAIR);
    click(&mut world, "settings.nav.experimental");
    assert!(cancels(&world).is_empty());

    let begun = world.daemon.sent_of("phone_pair_begin")[0].clone();
    world.daemon.reply_to(
        &begun,
        PhonePairBeginResultMessage {
            success: true,
            pairing_code: Some(code()),
            ..PhonePairBeginResultMessage::new()
        },
    );
    world.settle();
    assert_eq!(cancels(&world), ["work"]);
}

#[test]
fn a_pairing_code_says_it_expired_after_its_ten_minutes() {
    let (daemon, _) = home(phones(vec![code()], vec![]));
    let mut world = World::start(daemon);
    on_phones(&mut world);
    assert_eq!(named(&world, QR).as_deref(), Some("Pairing code for Work"));

    world.advance(Duration::from_secs(10 * 60));
    assert!(!shows(&world, CODE));
    assert_eq!(
        named(&world, &format!("{CODE}.expired")).as_deref(),
        Some("Pairing code expired after 10 min; show a new one.")
    );
    let pair = world.screen().get(Target::key(PAIR));
    assert_eq!(pair.name.as_deref(), Some("Show a new pairing code"));
    assert!(!pair.is("disabled"));
    assert!(world.daemon.sent_of("phone_pair_begin").is_empty());
}

#[test]
fn shows_why_the_link_is_down_and_why_a_code_could_not_be_shown() {
    let down = PhoneLink {
        state: "connecting".into(),
        error: Some(
            "dialing the Cloud Relay at wss://cloud.attn.codes/v1/homes/x/home: connection refused"
                .into(),
        ),
        ..link()
    };
    let (daemon, _) = home(PhonesUpdatedMessage {
        link: down,
        ..phones(vec![], vec![])
    });
    daemon.on("phone_pair_begin", |_| {
        message(PhonePairBeginResultMessage {
            success: false,
            error: Some("this home has not registered with Attn Cloud yet (connection refused); a QR code needs its home id".into()),
            ..PhonePairBeginResultMessage::new()
        })
    });
    let mut world = World::start(daemon);
    on_phones(&mut world);
    assert!(
        named(&world, "settings.phones.link")
            .unwrap()
            .ends_with("Last error: dialing the Cloud Relay at wss://cloud.attn.codes/v1/homes/x/home: connection refused")
    );
    click(&mut world, PAIR);
    let error = world
        .screen()
        .get(Target::key("settings.phones.work.error"));
    assert_eq!(error.role, "alert");
    assert!(
        error
            .name
            .unwrap()
            .starts_with("this home has not registered with Attn Cloud yet")
    );
}

#[test]
fn phone_access_off_offers_no_pairing() {
    let (daemon, _) = home(PhonesUpdatedMessage {
        link: PhoneLink {
            state: "off".into(),
            ..link()
        },
        ..phones(vec![], vec![])
    });
    let mut world = World::start(daemon);
    on_phones(&mut world);
    assert_eq!(
        named(&world, "settings.phones.link").as_deref(),
        Some("Phone access is off, so this home holds no connection to Attn Cloud.")
    );
    assert!(world.screen().get(Target::key(PAIR)).is("disabled"));
}

#[test]
fn reads_the_phones_again_after_the_connection_comes_back() {
    let (daemon, listed) = home(phones(vec![code()], vec![]));
    let mut world = World::start(daemon);
    on_phones(&mut world);
    assert!(shows(&world, CODE));

    *listed.lock().unwrap() = phones(vec![], vec![phone(false)]);
    world.reconnect();
    world.settle();
    assert!(!shows(&world, CODE));
    assert!(
        named(&world, "settings.phone.work.device-1.seen")
            .unwrap()
            .starts_with("Last seen ")
    );
    assert_eq!(world.daemon.sent_of("phones_get").len(), 2);
}
