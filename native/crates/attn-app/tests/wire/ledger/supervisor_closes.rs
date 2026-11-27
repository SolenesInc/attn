use std::time::Duration;

use attn_protocol::{SessionLedgerEntry, SessionReopenResult, SessionReopenResultMessage};
use attn_testkit::World;

use super::{close_event, closed, entry, has, hold, name, open, shown_session, world};

fn closed_by_supervisor(reason: &str) -> SessionLedgerEntry {
    let mut row = closed(entry("docs", "Docs pass", 0), "supervisor");
    row.close_reason = Some(reason.into());
    row
}

fn offer(world: &World) -> Option<String> {
    has(world, "notice.offer").then(|| name(world, "notice.offer"))
}

#[test]
fn a_close_the_supervisor_made_offers_reopen_which_brings_the_agent_back_to_a_desktop() {
    for reason in ["settled when finished", "stake harvested, nothing running"] {
        let mut world = world();
        let asked = hold(&world, "session_reopen");
        close_event(&mut world, closed_by_supervisor(reason));
        assert_eq!(
            offer(&world).as_deref(),
            Some("attn closed Docs pass Reopen"),
            "{reason}"
        );

        super::click(&mut world, "notice.offer");
        let asked = asked.lock().unwrap().clone();
        assert_eq!(asked.len(), 1, "{reason}");
        assert_eq!(
            (asked[0]["session_id"].as_str(), asked[0]["action"].as_str()),
            (Some("docs"), Some("reopen")),
            "{reason}"
        );
        world.daemon.reply_to(
            &asked[0],
            SessionReopenResultMessage {
                success: true,
                result: Some(SessionReopenResult {
                    session_id: "docs".into(),
                    action: "reopen".into(),
                    ..SessionReopenResult::default()
                }),
                ..SessionReopenResultMessage::new()
            },
        );
        world.settle();
        assert_eq!(shown_session(&world), ["docs"], "{reason}");
    }
}

#[test]
fn the_reopen_offer_stays_ten_seconds() {
    let mut world = world();
    close_event(&mut world, closed_by_supervisor("settled when finished"));
    world.advance(Duration::from_millis(9_900));
    assert!(
        offer(&world).is_some(),
        "still offered just short of ten seconds"
    );
    world.advance(Duration::from_millis(200));
    assert_eq!(offer(&world), None);
}

#[test]
fn a_close_by_the_user_an_agent_or_attn_itself_offers_nothing() {
    for by in ["user", "s-orchestrator", "attn"] {
        let mut world = world();
        close_event(&mut world, closed(entry("docs", "Docs pass", 0), by));
        assert_eq!(offer(&world), None, "closed by {by}");
    }
}

#[test]
fn a_close_the_supervisor_made_in_another_profile_offers_nothing_here() {
    let mut world = world();
    let mut row = closed_by_supervisor("settled when finished");
    row.profile_id = "home".into();
    close_event(&mut world, row);
    assert_eq!(offer(&world), None);
}

#[test]
fn a_later_close_after_opening_the_ledger_does_not_offer_an_old_close_again() {
    let mut world = world();
    close_event(&mut world, closed_by_supervisor("settled when finished"));
    world.advance(Duration::from_secs(11));
    assert_eq!(offer(&world), None);
    open(&mut world);
    close_event(&mut world, closed(entry("api", "API work", 0), "user"));
    assert_eq!(offer(&world), None);
}
