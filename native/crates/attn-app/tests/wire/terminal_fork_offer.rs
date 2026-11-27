use attn_protocol::{
    Desktop, DesktopPane, InitialStateMessage, ProfileActionResultMessage,
    ProfileArrangementChangedMessage, Session, SessionStateChangedMessage,
};
use std::time::Duration;

use attn_testkit::{Target, World, message};

use super::palette::{run_command, titles};
use super::sidebar::session;
use super::terminal::{desktop, pane, profile, showing};

const NOTICE: &str = "session.f.fork-offer";
const OPEN: &str = "Open original beside this";
const DISMISS: &str = "Dismiss fork offer";

fn tile_of(session: &str) -> DesktopPane {
    DesktopPane {
        session_id: session.into(),
        ..pane(&format!("p-{session}"), &format!("r-{session}"))
    }
}

fn fork(offer: Option<&str>) -> Session {
    Session {
        fork_offer: offer.map(str::to_owned),
        ..session("f", "idle")
    }
}

fn original() -> Session {
    Session {
        label: "Plan the migration".into(),
        ..session("o", "idle")
    }
}

fn world(tiles: Vec<DesktopPane>, sessions: Vec<Session>) -> World {
    let state = InitialStateMessage {
        sessions: Some(sessions),
        ..showing(tiles, "daemon-1")
    };
    let mut world = World::start(super::palette::daemon(state, vec![]));
    world.open_desktop();
    world
}

fn hidden_original() -> World {
    world(vec![tile_of("f")], vec![fork(Some("o")), original()])
}

fn said(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key(NOTICE))
        .and_then(|n| n.name)
}

fn palette_offers(world: &mut World) -> [bool; 2] {
    world.gesture(|app| app.key(super::palette::chord(attn_app::Action::UiCommandPalette)));
    let all = titles(world);
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        })
    });
    [
        all.iter().any(|t| t == OPEN),
        all.iter().any(|t| t == DISMISS),
    ]
}

fn sent_for(world: &World, cmd: &str) -> Vec<String> {
    world
        .daemon
        .sent_of(cmd)
        .iter()
        .map(|r| r["session_id"].as_str().unwrap().to_owned())
        .collect()
}

fn arrange(world: &mut World, desktop: Desktop) {
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile(),
        desktops: vec![desktop],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
    world.advance(Duration::from_secs(6));
}

fn keyboard(world: &mut World) -> serde_json::Value {
    world
        .automate("state", serde_json::json!({"of": "window"}))
        .unwrap()["keyboard"]["pane"]
        .clone()
}

#[test]
fn a_fork_names_its_original_and_offers_it_in_the_palette_while_no_tile_shows_the_original() {
    let mut world = hidden_original();
    assert_eq!(
        said(&world).as_deref(),
        Some("Forked from “Plan the migration”.")
    );
    assert_eq!(palette_offers(&mut world), [true, true]);
    world.advance(Duration::from_secs(6));
    assert!(
        world.runtime_is_at_rest(),
        "the notice draws once and stays still"
    );

    arrange(&mut world, desktop(vec![tile_of("f"), tile_of("o")]));
    assert_eq!(said(&world), None, "a tile shows the original");
    assert_eq!(palette_offers(&mut world), [false, false]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_fork_whose_original_closed_says_it_came_from_an_earlier_conversation() {
    let world = world(vec![tile_of("f")], vec![fork(Some("o"))]);
    assert_eq!(
        said(&world).as_deref(),
        Some("Forked from an earlier conversation.")
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_session_with_no_offer_shows_no_notice_and_no_palette_entries() {
    let mut world = world(vec![tile_of("f")], vec![fork(None), original()]);
    assert_eq!(said(&world), None);
    assert_eq!(palette_offers(&mut world), [false, false]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn open_on_the_notice_asks_the_daemon_and_the_keyboard_follows_the_original_beside_the_fork() {
    let mut world = hidden_original();
    let beside = || {
        let mut d = desktop(vec![tile_of("f"), tile_of("o")]);
        d.active_pane_id = "p-o".into();
        d
    };
    world.daemon.on("fork_offer_open", move |r| {
        message(ProfileActionResultMessage {
            action: "fork_offer_open".into(),
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            pane_id: Some("p-o".into()),
            desktops: Some(vec![beside()]),
            ..ProfileActionResultMessage::new()
        })
    });

    world.gesture(|app| app.click(Target::key("session.f.fork-offer.open")));
    arrange(&mut world, beside());
    world.daemon.emit(SessionStateChangedMessage {
        session: fork(None),
        ..SessionStateChangedMessage::new()
    });
    world.settle();

    assert_eq!(sent_for(&world, "fork_offer_open"), ["f"]);
    assert_eq!(keyboard(&mut world), "p-o");
    assert_eq!(said(&world), None);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_open_the_daemon_refuses_says_why_and_keeps_the_offer() {
    let mut world = hidden_original();
    world.daemon.on("fork_offer_open", |r| {
        message(ProfileActionResultMessage {
            action: "fork_offer_open".into(),
            request_id: r["request_id"].as_str().unwrap().into(),
            success: false,
            error: Some("the original is no longer in the ledger".into()),
            ..ProfileActionResultMessage::new()
        })
    });

    run_command(&mut world, OPEN);

    assert_eq!(sent_for(&world, "fork_offer_open"), ["f"]);
    assert_eq!(
        world
            .screen()
            .find(Target::key("notice.text"))
            .and_then(|n| n.name)
            .as_deref(),
        Some("Could not open the original: the original is no longer in the ledger")
    );
    assert!(said(&world).is_some());
}

#[test]
fn dismiss_by_pointer_or_palette_asks_the_daemon_which_ends_the_offer_for_every_window() {
    let mut world = hidden_original();
    world.gesture(|app| app.click(Target::key("session.f.fork-offer.dismiss")));
    run_command(&mut world, DISMISS);
    assert_eq!(sent_for(&world, "fork_offer_dismiss"), ["f", "f"]);

    world.daemon.emit(SessionStateChangedMessage {
        session: fork(None),
        ..SessionStateChangedMessage::new()
    });
    world.settle();
    assert_eq!(said(&world), None);
    assert!(world.runtime_is_at_rest());
}
