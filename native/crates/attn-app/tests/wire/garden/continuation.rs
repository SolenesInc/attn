use attn_protocol::{SeedContinuation, SeedResumeResultMessage, SeedSendToChiefResultMessage};
use attn_testkit::{Request, Target, World};
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
