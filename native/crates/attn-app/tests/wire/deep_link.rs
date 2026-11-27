use std::collections::BTreeMap;
use std::sync::atomic::{AtomicUsize, Ordering};

use attn_protocol::{
    Desktop, DesktopPane, HarnessCatalogMessage, InitialStateMessage, Profile,
    ProfileArrangementChangedMessage, Session, SessionsUpdatedMessage, SpawnResultMessage,
};
use attn_testkit::{Daemon, Request, Target, World, built_in_harnesses, message};
use serde_json::Value;

const PLAN: &str = "attn://spawn?cwd=%2Frepo%2Fplan&label=plan";
/// The harness the user picked in an earlier new-session picker.
const LAST_PICK: (&str, &str) = ("new_session_agent", "claude");

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
    world_picked(current, sessions, &[LAST_PICK])
}

fn world_picked(current: &str, sessions: Vec<Session>, settings: &[(&str, &str)]) -> World {
    world_on(Daemon::scripted(), current, sessions, settings)
}

fn world_on(
    daemon: Daemon,
    current: &str,
    sessions: Vec<Session>,
    settings: &[(&str, &str)],
) -> World {
    World::start(
        daemon.state(InitialStateMessage {
            selected_profile_id: Some("work".into()),
            profiles: Some(vec![profile(current)]),
            desktops: Some(vec![desktop()]),
            sessions: Some(sessions),
            settings: Some(
                settings
                    .iter()
                    .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
                    .collect::<BTreeMap<_, _>>(),
            ),
            ..InitialStateMessage::new()
        }),
    )
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
fn a_spawn_link_waits_for_the_current_desktop_then_starts_the_harness_last_picked_there_once() {
    let mut world = world_picked("", vec![], &[("new_session_agent", "codex")]);
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
    assert_eq!(spawns[0]["agent"], "codex");
    assert!(spawns[0].get("yolo_mode").is_none_or(Value::is_null));
    assert_eq!(spawns[0]["placement"]["desktop_id"], "d1");
    assert_eq!(spawns[0]["placement"]["direction"], "vertical");
    rest(&mut world);
}

#[test]
fn a_cold_spawn_link_waits_for_the_harness_catalog_before_starting_the_saved_harness_with_its_label()
 {
    let mut world = World::start(Daemon::scripted().without_harness_catalog().state(
        InitialStateMessage {
            selected_profile_id: Some("work".into()),
            profiles: Some(vec![profile("d1")]),
            desktops: Some(vec![desktop()]),
            sessions: Some(vec![session("existing", "/repo/existing", "work")]),
            settings: Some(BTreeMap::from([(
                "new_session_agent".into(),
                "codex".into(),
            )])),
            ..InitialStateMessage::new()
        },
    ));

    open(&mut world, "attn://spawn?cwd=%2Frepo%2Fexisting");
    assert_eq!(shown(&world), ["existing"]);

    open(&mut world, PLAN);
    assert!(world.daemon.sent_of("spawn_session").is_empty());
    assert!(
        world
            .screen()
            .find(Target::key("new-session.path.field"))
            .is_none()
    );

    world.daemon.emit(HarnessCatalogMessage {
        harnesses: built_in_harnesses(),
        ..HarnessCatalogMessage::new()
    });
    world.settle();

    let spawns = world.daemon.sent_of("spawn_session");
    assert_eq!(spawns.len(), 1);
    assert_eq!(spawns[0]["cwd"], "/repo/plan");
    assert_eq!(spawns[0]["label"], "plan");
    assert_eq!(spawns[0]["agent"], "codex");
    rest(&mut world);
}

#[test]
fn with_no_harness_picked_yet_a_spawn_link_opens_the_new_session_picker_at_its_folder() {
    let mut world = world_picked("d1", vec![], &[]);
    open(&mut world, PLAN);

    assert!(world.daemon.sent_of("spawn_session").is_empty());
    let field = world.screen().get(Target::key("new-session.path.field"));
    assert_eq!(field.value.as_deref(), Some("/repo/plan"));
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

#[test]
fn with_the_home_on_another_machine_a_spawn_link_starts_nothing_there_and_says_why() {
    let mut world = world_on(
        Daemon::scripted().relaying_to("mac", "oma"),
        "d1",
        vec![session("on-oma", "/repo/plan", "work")],
        &[LAST_PICK],
    );
    open(&mut world, PLAN);

    assert!(shown(&world).is_empty());
    assert!(world.daemon.sent_of("spawn_session").is_empty());
    assert_eq!(
        world
            .screen()
            .find(Target::key("notice.text"))
            .and_then(|n| n.name)
            .as_deref(),
        Some("/repo/plan is on This Mac; oma can't start agents on this machine yet")
    );
    rest(&mut world);
}
