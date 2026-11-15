use attn_protocol::{SeedCheck, SeedCheckResultMessage, SeedContinuation};
use attn_testkit::{Request, Target, World};

use super::*;

const SEED: &str = "Cache store migration";

fn page() -> World {
    let seed = with(seed("s-draft1", SEED), |s| {
        s.checks = Some(
            ["C1", "C2"]
                .map(|id| SeedCheck {
                    id: id.into(),
                    text: format!("{id} holds"),
                    state: "open".into(),
                    ..SeedCheck::default()
                })
                .to_vec(),
        );
        s.continuation = Some(SeedContinuation {
            agent: "claude".into(),
            cwd: "/tmp/placed".into(),
            handover_placement: "reuse_cwd".into(),
            directory_state: "present".into(),
            ..SeedContinuation::default()
        });
    });
    let chief = Session {
        chief_of_staff: Some(true),
        ..session("chief1", "Chief")
    };
    let mut world = open_garden_with(daemon(vec![seed], vec![chief]));
    open_row(&mut world, SEED);
    world
}

fn confirm(world: &mut World, check: &str) {
    click(world, &format!("Confirm {check}"));
    let request: Request = world.daemon.sent_of("seed_check").last().unwrap().clone();
    assert_eq!(request["check_id"], check);
    world.daemon.reply_to(
        &request,
        SeedCheckResultMessage {
            request_id: request["request_id"].as_str().unwrap().into(),
            success: true,
            ..SeedCheckResultMessage::new()
        },
    );
    world.settle();
}

fn field(world: &World, part: &str) -> Option<String> {
    world
        .screen()
        .find(Target::key(format!("garden.{part}.field")))
        .and_then(|n| n.value)
}

/// Opens `part`'s field with `open`, writes into it, confirms `elsewhere`, and finds the words
/// still in the field, which keeps the keyboard once the user went back to it.
fn keeps_draft_through_a_check(part: &str, open: &str, elsewhere: &str) -> World {
    let mut world = page();
    click(&mut world, open);
    world.gesture(|app| app.type_text("Half a thought"));

    confirm(&mut world, elsewhere);
    assert_eq!(field(&world, part).as_deref(), Some("Half a thought"));

    click(&mut world, "Confirm C2");
    let target = Target::key(format!("garden.{part}.field"));
    world.gesture(|app| app.click(target));
    let request: Request = world.daemon.sent_of("seed_check").last().unwrap().clone();
    world.daemon.reply_to(
        &request,
        SeedCheckResultMessage {
            request_id: request["request_id"].as_str().unwrap().into(),
            success: true,
            ..SeedCheckResultMessage::new()
        },
    );
    world.settle();
    assert_eq!(
        focused(&world).and_then(|n| n.key),
        Some(format!("garden.{part}.field"))
    );
    world.gesture(|app| app.type_text(", finished."));
    assert_eq!(
        field(&world, part).as_deref(),
        Some("Half a thought, finished.")
    );
    world
}

#[test]
fn the_authorization_being_written_survives_a_check_judged_on_the_page() {
    let mut world = keeps_draft_through_a_check("authorization", "Set", "C1");
    key(&mut world, Key::Enter);
    let sent = world.daemon.sent_of("seed_authorization");
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["text"], "Half a thought, finished.");
    assert!(rests(&mut world));
}

#[test]
fn the_note_to_the_chief_being_written_survives_a_check_judged_on_the_page() {
    let mut world = keeps_draft_through_a_check("chief", "Send to Chief", "C1");
    assert!(rests(&mut world));
}

#[test]
fn the_handover_note_being_written_survives_a_check_judged_on_the_page() {
    let mut world = keeps_draft_through_a_check("handover", "Handover", "C1");
    assert_eq!(
        world
            .screen()
            .get(Target::key("garden.handover.cwd"))
            .value
            .as_deref(),
        Some("/tmp/placed")
    );
    assert!(rests(&mut world));
}

#[test]
fn a_rejection_being_written_survives_another_check_judged_on_the_page() {
    let mut world = keeps_draft_through_a_check("check.C1", "Reject C1", "C2");
    assert!(rests(&mut world));
}
