use std::collections::BTreeMap;
use std::sync::atomic::{AtomicUsize, Ordering};

use attn_protocol::{
    Desktop, DesktopPane, InitialStateMessage, Profile, ProfileArrangementChangedMessage, Session,
    SessionsUpdatedMessage, SpawnResultMessage,
};
use attn_testkit::{Daemon, Request, World, message};
use serde_json::Value;

const PLAN: &str = "attn://spawn?cwd=%2Frepo%2Fplan&label=plan";

fn profile(current: &str) -> Profile {
    Profile {
        id: "work".into(),
        name: "Work".into(),
        current_desktop_id: current.into(),
        ..Profile::default()
    }
}

fn desktop() -> Desktop {
    Desktop {
        id: "d1".into(),
        name: "Desktop 1".into(),
        active_pane_id: "p1".into(),
        panes: vec![DesktopPane {
            desktop_id: "d1".into(),
            pane_id: "p1".into(),
            session_id: "s1".into(),
            kind: "agent".into(),
            ..DesktopPane::default()
        }],
        profile_id: "work".into(),
        ..Desktop::default()
    }
}

fn session(id: &str, directory: &str, profile: &str) -> Session {
    Session {
        id: id.into(),
        label: id.into(),
        agent: "claude".into(),
        state: "idle".into(),
        directory: directory.into(),
        profile_id: profile.into(),
        ..Session::default()
    }
}

fn world_with(current: &str, sessions: Vec<Session>) -> World {
    World::start(Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![profile(current)]),
        desktops: Some(vec![desktop()]),
        sessions: Some(sessions),
        settings: Some(BTreeMap::new()),
        ..InitialStateMessage::new()
    }))
}

fn started(request: &Request, success: bool) -> Value {
    message(SpawnResultMessage {
        id: request["id"].as_str().unwrap().into(),
        success,
        desktop_id: Some("d1".into()),
        pane_id: success.then(|| "p-new".into()),
        error: (!success).then(|| "spawn failed".into()),
        ..SpawnResultMessage::new()
    })
}

fn open(world: &mut World, url: &str) {
    world.gesture(|app| app.open_link(url));
}

fn shown(world: &World) -> Vec<String> {
    world
        .daemon
        .sent_of("desktop_show_session")
        .iter()
        .map(|r| r["session_id"].as_str().unwrap().to_owned())
        .collect()
}

fn rest(world: &mut World) {
    world.advance(std::time::Duration::from_secs(6));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_spawn_link_waits_for_the_current_desktop_then_starts_its_agent_there_once() {
    let mut world = world_with("", vec![]);
    open(&mut world, PLAN);
    assert!(world.daemon.sent_of("spawn_session").is_empty());

    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile("d1"),
        desktops: vec![desktop()],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();

    let spawns = world.daemon.sent_of("spawn_session");
    assert_eq!(spawns.len(), 1);
    assert_eq!(spawns[0]["cwd"], "/repo/plan");
    assert_eq!(spawns[0]["label"], "plan");
    assert_eq!(spawns[0]["agent"], "claude");
    assert!(spawns[0].get("yolo_mode").is_none_or(Value::is_null));
    assert_eq!(spawns[0]["placement"]["desktop_id"], "d1");
    assert_eq!(spawns[0]["placement"]["direction"], "vertical");
    rest(&mut world);
}

#[test]
fn a_link_without_a_label_names_the_agent_after_its_folder() {
    let mut world = world_with("d1", vec![]);
    open(&mut world, "attn-dev://spawn?cwd=/repo/plan+b");

    let spawns = world.daemon.sent_of("spawn_session");
    assert_eq!(spawns[0]["cwd"], "/repo/plan b");
    assert_eq!(spawns[0]["label"], "plan b");
    rest(&mut world);
}

#[test]
fn a_link_to_a_folder_an_agent_of_this_profile_works_in_shows_that_agent() {
    let mut world = world_with("d1", vec![session("s1", "/tmp/s1", "work")]);
    world.daemon.emit(SessionsUpdatedMessage {
        sessions: Some(vec![
            session("s1", "/tmp/s1", "work"),
            session("late", "/tmp/late", "work"),
        ]),
        ..SessionsUpdatedMessage::new()
    });
    world.settle();

    open(&mut world, "attn://spawn?cwd=%2Ftmp%2Flate");

    assert_eq!(shown(&world), ["late"]);
    assert!(world.daemon.sent_of("spawn_session").is_empty());
    rest(&mut world);
}

#[test]
fn a_link_to_a_folder_only_another_profiles_agent_works_in_starts_a_new_agent() {
    let mut world = world_with("d1", vec![session("s2", "/tmp/s2", "side")]);

    open(&mut world, "attn://spawn?cwd=%2Ftmp%2Fs2");

    assert!(shown(&world).is_empty());
    let spawns = world.daemon.sent_of("spawn_session");
    assert_eq!(spawns.len(), 1);
    assert_eq!(spawns[0]["cwd"], "/tmp/s2");
    rest(&mut world);
}

#[test]
fn the_same_link_later_shows_the_agent_it_started_and_one_that_failed_starts_again() {
    let mut world = world_with("d1", vec![]);
    let answered = AtomicUsize::new(0);
    world.daemon.on("spawn_session", move |r| {
        started(r, answered.fetch_add(1, Ordering::SeqCst) > 0)
    });
    open(&mut world, PLAN);
    open(&mut world, PLAN);
    let spawns = world.daemon.sent_of("spawn_session");
    assert_eq!(spawns.len(), 2);

    let id = spawns[1]["id"].as_str().unwrap().to_owned();
    world.daemon.emit(SessionsUpdatedMessage {
        sessions: Some(vec![session(&id, "/repo/plan", "work")]),
        ..SessionsUpdatedMessage::new()
    });
    world.settle();
    open(&mut world, PLAN);

    assert_eq!(world.daemon.sent_of("spawn_session").len(), 2);
    assert_eq!(shown(&world).last(), Some(&id));
    rest(&mut world);
}

#[test]
fn a_link_that_asks_for_anything_but_a_spawn_with_a_folder_does_nothing() {
    let mut world = world_with("d1", vec![]);
    for url in [
        "attn://open?cwd=%2Frepo",
        "attn://spawn?label=plan",
        "attn://spawn?cwd=",
        "not a link",
    ] {
        open(&mut world, url);
    }

    assert!(world.daemon.sent_of("spawn_session").is_empty());
    assert!(shown(&world).is_empty());
    rest(&mut world);
}
