use attn_protocol::{
    Desktop, DesktopPane, InitialStateMessage, OpenSeedResultMessage, Profile,
    ProfileArrangementChangedMessage, SeedContinuation, SeedResumeResultMessage,
    SeedSendToChiefResultMessage,
};
use attn_testkit::{Request, Target, World, tree};
use serde_json::json;

use super::*;

fn continued(continuation: SeedContinuation) -> Seed {
    with(seed("s-cont11", "Cache store migration"), |s| {
        s.rev = 3;
        s.continuation = Some(continuation)
    })
}

fn resumable() -> SeedContinuation {
    SeedContinuation {
        resume_available: true,
        directory_state: "present".into(),
        ..SeedContinuation::default()
    }
}

fn chief() -> Session {
    Session {
        chief_of_staff: Some(true),
        ..session("chief1", "Chief")
    }
}

fn notice(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("notice.text"))
        .and_then(|n| n.name)
}

fn button(world: &World, name: &str) -> bool {
    world
        .screen()
        .find(Target::role("button").name(name))
        .is_some()
}

fn resume_reply(error: Option<&'static str>) -> impl Fn(&Request) -> Value + Send + Sync {
    move |r: &Request| {
        message(SeedResumeResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: error.is_none(),
            error: error.map(str::to_owned),
            session_id: error.is_none().then(|| "tender".to_owned()),
            ..SeedResumeResultMessage::new()
        })
    }
}

#[test]
fn resumes_a_seeds_agent_and_goes_to_the_session_the_daemon_reopened() {
    let world_daemon = daemon(vec![continued(resumable())], Vec::new());
    world_daemon.on("seed_resume", resume_reply(None));
    let mut world = open_garden_with(world_daemon);
    open_row(&mut world, "Cache store migration");

    click(&mut world, "Resume");

    let resumed = world.daemon.sent_of("seed_resume");
    assert_eq!(resumed.len(), 1);
    assert_eq!(resumed[0]["seed_id"], "s-cont11");
    let shown = world.daemon.sent_of("desktop_show_session");
    assert_eq!(shown.last().unwrap()["session_id"], "tender");
    assert!(rests(&mut world));
}

#[test]
fn says_why_the_daemon_could_not_resume_a_seeds_agent() {
    let world_daemon = daemon(vec![continued(resumable())], Vec::new());
    world_daemon.on(
        "seed_resume",
        resume_reply(Some("seed has no agent session to reopen")),
    );
    let mut world = open_garden_with(world_daemon);
    open_row(&mut world, "Cache store migration");

    click(&mut world, "Resume");

    assert_eq!(
        notice(&world).as_deref(),
        Some("seed has no agent session to reopen")
    );
    assert!(world.daemon.sent_of("desktop_show_session").is_empty());
    world.advance(std::time::Duration::from_secs(7));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn offers_resume_only_while_the_agent_can_come_back_and_names_an_unreachable_outpost() {
    let mut world = open_garden(vec![continued(SeedContinuation {
        resume_available: false,
        resume_reason: Some("the original conversation is no longer available".into()),
        ..resumable()
    })]);
    open_row(&mut world, "Cache store migration");
    assert!(!button(&world, "Resume"));

    let mut world = open_garden(vec![continued(SeedContinuation {
        resume_available: false,
        directory_state: "unreachable".into(),
        outpost: Some("studio".into()),
        resume_reason: Some("outpost \"studio\" is unreachable".into()),
        ..resumable()
    })]);
    open_row(&mut world, "Cache store migration");
    let resume = world
        .screen()
        .get(Target::role("button").name("Resume on studio"));
    assert!(resume.is("disabled"), "{resume:?}");
    assert!(rests(&mut world));
}

#[test]
fn sends_a_seed_to_the_chief_with_the_users_note_once_the_profile_has_a_chief() {
    let mut world = open_garden(vec![continued(resumable())]);
    open_row(&mut world, "Cache store migration");
    assert!(!button(&world, "Send to Chief"));

    let world_daemon = daemon(vec![continued(resumable())], vec![chief()]);
    world_daemon.on("seed_send_to_chief", |r: &Request| {
        message(SeedSendToChiefResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            ..SeedSendToChiefResultMessage::new()
        })
    });
    let mut world = open_garden_with(world_daemon);
    open_row(&mut world, "Cache store migration");
    click(&mut world, "Send to Chief");
    assert_eq!(
        focused(&world).and_then(|n| n.name).as_deref(),
        Some("What should Chief know? (optional)")
    );
    world.gesture(|app| app.type_text("It needs a Mac."));
    key(&mut world, Key::Enter);
    assert!(world.daemon.sent_of("seed_send_to_chief").is_empty());
    key_with(
        &mut world,
        Key::Enter,
        Mods {
            cmd: true,
            ..Mods::default()
        },
    );

    let sent = world.daemon.sent_of("seed_send_to_chief");
    assert_eq!(
        (
            &sent[0]["seed_id"],
            &sent[0]["expected_rev"],
            &sent[0]["guidance"]
        ),
        (&json!("s-cont11"), &json!(3), &json!("It needs a Mac."))
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.chief.form"))
            .is_none()
    );
    assert_eq!(
        focused(&world).and_then(|n| n.key).as_deref(),
        Some("garden.chief.open")
    );
    assert!(rests(&mut world));
}

const DESKTOP: &str = "work/desktop_1";

fn agent_pane(pane: &str, session: &str) -> DesktopPane {
    DesktopPane {
        desktop_id: DESKTOP.into(),
        pane_id: pane.into(),
        runtime_id: format!("r-{pane}"),
        session_id: session.into(),
        kind: "agent".into(),
        ..DesktopPane::default()
    }
}

fn seed_tile(session: &str) -> Value {
    json!({
        "type": "tile",
        "tile_id": "tile-seed",
        "tile_kind": "seed",
        "tile_params": "s-cont11",
        "tile_session_id": session,
    })
}

fn profile() -> Profile {
    Profile {
        id: PROFILE.into(),
        name: "Work".into(),
        current_desktop_id: DESKTOP.into(),
        ..Profile::default()
    }
}

/// Agent `s1`'s pane with the tiles of `tiles` docked beside it, and `active` the pane the
/// desktop acts on.
fn desktop(tiles: Vec<Value>, active: &str) -> Desktop {
    let tree = if tiles.is_empty() {
        tree::pane("a")
    } else {
        tree::row(
            std::iter::once(tree::pane("a"))
                .chain(tiles)
                .collect::<Vec<_>>(),
        )
    };
    Desktop {
        id: DESKTOP.into(),
        profile_id: PROFILE.into(),
        name: "Desktop 1".into(),
        shortcut_slot: Some(1),
        order_key: "1".into(),
        tree_json: tree.to_string(),
        active_pane_id: active.into(),
        panes: vec![agent_pane("a", "s1")],
        revision: 1,
    }
}

/// `desktop(tiles, active)` as the current desktop, then the garden over it, open on its seed.
fn on_desktop(tiles: Vec<Value>, active: &str, sessions: Vec<Session>) -> World {
    let world_daemon = Daemon::scripted().state(InitialStateMessage {
        profiles: Some(vec![profile()]),
        desktops: Some(vec![desktop(tiles, active)]),
        ..initial(vec![continued(resumable())], sessions)
    });
    let known = continued(resumable());
    world_daemon.on("seed_document_get", move |r: &Request| {
        document_reply(r, Some(known.clone()))
    });
    let mut world = World::start_sized(world_daemon, (1248.0, 800.0));
    world.open_desktop();
    world.gesture(|app| app.key(board()));
    open_row(&mut world, "Cache store migration");
    world
}

#[test]
fn opens_a_seed_as_a_tile_beside_the_session_the_user_is_in_and_shows_it() {
    let mut world = on_desktop(Vec::new(), "a", vec![session("s1", "Agent a")]);
    let docked = desktop(vec![seed_tile("s1")], "a");
    world.daemon.on("open_seed", move |r: &Request| {
        vec![
            message(OpenSeedResultMessage {
                request_id: r["request_id"].as_str().map(str::to_owned),
                success: true,
                seed_id: "s-cont11".into(),
                desktop_id: Some(DESKTOP.into()),
                tile_id: Some("tile-seed".into()),
                ..OpenSeedResultMessage::new()
            }),
            message(ProfileArrangementChangedMessage {
                profile: profile(),
                desktops: vec![Desktop {
                    revision: 2,
                    ..docked.clone()
                }],
                ..ProfileArrangementChangedMessage::new()
            }),
        ]
    });

    click(&mut world, "Open as tile");

    let opened = world.daemon.sent_of("open_seed");
    assert_eq!(opened.len(), 1);
    assert_eq!(opened[0]["seed_id"], "s-cont11");
    assert_eq!(opened[0]["session_id"], "s1");
    assert!(opened[0].get("standalone").is_none());
    let leaves = world.daemon.sent_of("desktop_show_leaf");
    assert_eq!(leaves.last().unwrap()["leaf_id"], "tile-seed");
    assert_eq!(shown(&world), "closed");
    let window = world.automate("state", json!({"of": "window"})).unwrap();
    assert_eq!(window["keyboard"]["pane"], "tile-seed");
    assert!(rests(&mut world));
}

#[test]
fn opens_a_seed_from_a_session_bound_seed_tile_beside_the_session_that_tile_is_bound_to() {
    let mut world = on_desktop(
        vec![seed_tile("tender")],
        "tile-seed",
        vec![session("s1", "Agent a"), session("tender", "Tender")],
    );
    world.daemon.on("open_seed", |_: &Request| ());

    click(&mut world, "Open as tile");

    let opened = world.daemon.sent_of("open_seed");
    assert_eq!(opened.len(), 1);
    assert_eq!(opened[0]["session_id"], "tender");
}
