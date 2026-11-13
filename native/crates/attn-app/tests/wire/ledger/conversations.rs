use attn_protocol::{
    KeptConversation, KeptConversationCrewLetter, KeptConversationListResult,
    KeptConversationListResultEvent, KeptConversationRow, KeptConversationSeed,
    KeptConversationsChangedEvent,
};
use attn_testkit::{Request, World, message};
use rune::Key;

use super::super::sidebar::state;
use super::{
    click, closed, come_back, entry, focused, hang_up, has, ledger_daemon, letter, name, open,
    plain, rows, selected, text_in,
};

const AT: &str = "2026-10-01T12:00:00Z";

fn copy() -> KeptConversation {
    KeptConversation {
        bytes: 4200,
        copied_at: AT.into(),
        ..KeptConversation::default()
    }
}

fn conversation(resume: &str, title: &str) -> KeptConversationRow {
    KeptConversationRow {
        agent: "claude".into(),
        resume_id: resume.into(),
        title: title.into(),
        session_ids: vec![format!("s-{resume}")],
        kept: Some(copy()),
        ..KeptConversationRow::default()
    }
}

fn parser_work() -> KeptConversationSeed {
    KeptConversationSeed {
        id: "s-garden".into(),
        slug: "parser-work".into(),
        title: "Parser work".into(),
    }
}

fn fixture() -> KeptConversationListResult {
    let pinned = KeptConversationRow {
        pinned_at: Some(AT.into()),
        kept: Some(KeptConversation {
            pinned_at: Some(AT.into()),
            ..copy()
        }),
        ..conversation("pin", "Pinned parser")
    };
    let seeded = KeptConversationRow {
        seeds: vec![parser_work()],
        ..conversation("seed", "Seed work")
    };
    let spike = KeptConversationRow {
        kept: Some(KeptConversation {
            delete_after: Some("2026-10-13T12:00:00Z".into()),
            ..copy()
        }),
        ..conversation("release", "Old spike")
    };
    let pending_pin = KeptConversationRow {
        pinned_at: Some(AT.into()),
        kept: None,
        pending_reason: Some("waiting for the first copy".into()),
        ..conversation("pending-pin", "Pending pin")
    };
    let pending_seed = KeptConversationRow {
        seeds: vec![parser_work()],
        kept: None,
        pending_reason: Some("waiting for the first copy".into()),
        ..conversation("pending-seed", "Pending seed")
    };
    KeptConversationListResult {
        count: 3,
        stored_bytes: 12600,
        pending_count: 2,
        next_delete_after: Some("2026-10-13T12:00:00Z".into()),
        rows: vec![pinned, seeded, spike, pending_pin, pending_seed],
    }
}

fn answer(request: &Request, result: KeptConversationListResult) -> serde_json::Value {
    message(KeptConversationListResultEvent {
        request_id: request["request_id"].as_str().map(str::to_owned),
        success: true,
        kept_conversation_list_result: Some(result),
        ..KeptConversationListResultEvent::new()
    })
}

fn succeed(world: &World, cmd: &'static str, event: &'static str) {
    world.daemon.on(cmd, move |r| {
        serde_json::json!({"event": event, "request_id": r["request_id"], "success": true})
    });
}

fn refuse(world: &World, cmd: &'static str, event: &'static str, error: &'static str) {
    world.daemon.on(cmd, move |r| {
        serde_json::json!({"event": event, "request_id": r["request_id"], "success": false, "error": error})
    });
}

fn serve(world: &World, result: KeptConversationListResult) {
    world
        .daemon
        .on("kept_conversation_list", move |r| answer(r, result.clone()));
}

fn start(result: KeptConversationListResult) -> World {
    let mut world = World::start(ledger_daemon(state(vec![], vec![]), vec![]));
    serve(&world, result);
    open(&mut world);
    click(&mut world, "ledger.tab.conversations");
    world
}

fn lists(world: &World) -> Vec<Option<bool>> {
    world
        .daemon
        .sent_of("kept_conversation_list")
        .iter()
        .map(|r| r["include_deleted"].as_bool())
        .collect()
}

fn changed(world: &mut World) {
    world.daemon.emit(KeptConversationsChangedEvent::new());
    world.settle();
}

fn sent(world: &World, cmd: &str) -> Vec<serde_json::Value> {
    world
        .daemon
        .sent_of(cmd)
        .into_iter()
        .map(|mut r| {
            r.as_object_mut().unwrap().remove("request_id");
            r
        })
        .collect()
}

#[test]
fn totals_reasons_and_pending_copies_read_as_the_daemon_counts_them() {
    let mut world = start(fixture());

    assert_eq!(name(&world, "ledger.counts"), "3 kept  ·  12.6 KB");
    assert_eq!(name(&world, "ledger.pending"), "2 pending");
    assert_eq!(
        name(&world, "ledger.next-deletion"),
        "next deletion 2026-10-13"
    );
    assert!(name(&world, "ledger.row.claude:pin.meta").ends_with("Forever · pinned 2026-10-01"));
    assert!(name(&world, "ledger.row.claude:seed.meta").ends_with("Open: parser-work"));
    assert!(name(&world, "ledger.row.claude:release.meta").ends_with("Deletes 2026-10-13"));
    assert!(name(&world, "ledger.row.claude:pending-pin.meta").contains("Pending copy"));
    assert!(!name(&world, "ledger.row.claude:pending-seed.meta").contains("0 B"));

    click(&mut world, "ledger.row.claude:seed");
    assert!(!text_in(&world, "ledger.inspector").contains("Forget…"));
    assert!(
        text_in(&world, "ledger.inspector")
            .contains("Harvest or wither these seeds before forgetting the copy.")
    );
    assert_eq!(lists(&world), [Some(false)]);
}

#[test]
fn forgetting_a_crew_copy_names_the_letters_that_lose_their_transcript_first() {
    let crew = KeptConversationRow {
        crew_letters: Some(vec![KeptConversationCrewLetter {
            member: "trellis".into(),
            filename: "2026-10-27T23-58Z-trellis_01.000000000-12345.md".into(),
            occurred_at: "2026-10-27T23:58:00Z".into(),
        }]),
        ..conversation("crew", "Trellis yesterday")
    };
    let mut world = start(KeptConversationListResult {
        count: 1,
        stored_bytes: 4200,
        rows: vec![crew],
        ..KeptConversationListResult::default()
    });
    succeed(
        &world,
        "kept_conversation_forget",
        "kept_conversation_forget_result",
    );
    assert!(
        name(&world, "ledger.row.claude:crew.meta").ends_with("Crew letter: Trellis · 27 Oct 2026")
    );

    click(&mut world, "ledger.inspector.action.1");
    assert_eq!(
        name(&world, "ledger.inspector.letters-stay"),
        "These letters stay, but will no longer point to this transcript: Trellis · 27 Oct 2026."
    );
    assert_eq!(
        name(&world, "ledger.inspector.confirm"),
        "Delete attn’s copy of Trellis yesterday (4.2 KB)? Claude’s own files are not touched."
    );
    click(&mut world, "ledger.inspector.action.0");
    assert!(!has(&world, "ledger.inspector.confirm"));
    assert!(sent(&world, "kept_conversation_forget").is_empty());

    click(&mut world, "ledger.inspector.action.1");
    click(&mut world, "ledger.inspector.action.1");
    assert_eq!(
        sent(&world, "kept_conversation_forget"),
        [
            serde_json::json!({"cmd": "kept_conversation_forget", "session_id": "conversation:crew", "unlink_crew_letters": true})
        ]
    );
    assert_eq!(lists(&world), [Some(false)]);
}

#[test]
fn forget_waits_for_its_confirmation_which_enter_and_escape_cancel_and_says_why_the_daemon_refused()
{
    let mut world = start(fixture());
    refuse(
        &world,
        "kept_conversation_forget",
        "kept_conversation_forget_result",
        "Open seed parser-work keeps this conversation",
    );
    click(&mut world, "ledger.row.claude:release");
    letter(&mut world, "2");
    assert!(has(&world, "ledger.inspector.confirm"));

    plain(&mut world, Key::Enter);
    assert!(!has(&world, "ledger.inspector.confirm"));
    letter(&mut world, "2");
    plain(&mut world, Key::Escape);
    assert!(!has(&world, "ledger.inspector.confirm"));
    assert!(
        has(&world, "ledger.panel"),
        "Escape closed the ledger instead of the question"
    );
    assert!(sent(&world, "kept_conversation_forget").is_empty());

    letter(&mut world, "2");
    letter(&mut world, "2");
    assert_eq!(
        sent(&world, "kept_conversation_forget"),
        [
            serde_json::json!({"cmd": "kept_conversation_forget", "session_id": "conversation:release", "unlink_crew_letters": false})
        ]
    );
    assert_eq!(
        name(&world, "ledger.inspector.note"),
        "Open seed parser-work keeps this conversation"
    );
}

#[test]
fn keep_and_unkeep_name_the_conversation_even_for_a_pin_without_a_copy() {
    let mut world = start(fixture());
    succeed(
        &world,
        "kept_conversation_keep",
        "kept_conversation_keep_result",
    );
    click(&mut world, "ledger.row.claude:release.verb");
    click(&mut world, "ledger.row.claude:pending-pin.verb");
    assert_eq!(
        sent(&world, "kept_conversation_keep"),
        [
            serde_json::json!({"cmd": "kept_conversation_keep", "session_id": "conversation:release", "keep": true}),
            serde_json::json!({"cmd": "kept_conversation_keep", "session_id": "conversation:pending-pin", "keep": false}),
        ]
    );
    assert_eq!(lists(&world), [Some(false)]);
}

#[test]
fn a_change_or_a_reconnect_reads_the_list_again_and_an_earlier_answer_is_dropped() {
    let mut world = start(fixture());
    world
        .daemon
        .on("kept_conversation_list", |_| None::<serde_json::Value>);
    changed(&mut world);
    let older = world.daemon.sent_of("kept_conversation_list")[1].clone();
    serve(
        &world,
        KeptConversationListResult {
            rows: vec![],
            ..KeptConversationListResult::default()
        },
    );
    changed(&mut world);
    world.daemon.reply_to(&older, answer(&older, fixture()));
    world.settle();
    assert_eq!(rows(&world), Vec::<String>::new());
    assert_eq!(name(&world, "ledger.counts"), "0 kept  ·  0 B");

    world.daemon.on("kept_conversation_list", |r| {
        serde_json::json!({"event": "kept_conversation_list_result", "request_id": r["request_id"], "success": false, "error": "store is busy"})
    });
    changed(&mut world);
    assert_eq!(name(&world, "ledger.error"), "store is busy");

    hang_up(&mut world);
    come_back(&mut world);
    assert_eq!(lists(&world).len(), 5);
}

#[test]
fn the_keyboard_stays_on_a_surviving_row_after_a_forget() {
    let mut world = start(fixture());
    succeed(
        &world,
        "kept_conversation_forget",
        "kept_conversation_forget_result",
    );
    letter(&mut world, "2");
    letter(&mut world, "2");
    let mut after = fixture();
    after.rows.remove(0);
    serve(&world, after);
    changed(&mut world);

    assert_eq!(selected(&world).as_deref(), Some("claude:seed"));
    assert_eq!(focused(&world).as_deref(), Some("ledger.list"));
    plain(&mut world, Key::Down);
    assert_eq!(selected(&world).as_deref(), Some("claude:release"));
    assert_eq!(
        sent(&world, "kept_conversation_forget"),
        [
            serde_json::json!({"cmd": "kept_conversation_forget", "session_id": "conversation:pin", "unlink_crew_letters": false})
        ]
    );
}

#[test]
fn deleted_copies_are_asked_for_only_while_shown_and_say_who_deleted_them() {
    let mut world = start(fixture());
    let mut with_gone = fixture();
    with_gone.rows.push(KeptConversationRow {
        kept: Some(KeptConversation {
            deleted_at: Some(AT.into()),
            deleted_by: Some("user".into()),
            ..copy()
        }),
        ..conversation("gone", "Forgotten work")
    });
    serve(&world, with_gone);

    click(&mut world, "ledger.deleted");
    assert!(
        name(&world, "ledger.row.claude:gone.meta")
            .ends_with("You deleted attn’s copy on 2026-10-01")
    );
    click(&mut world, "ledger.deleted");
    assert!(!rows(&world).contains(&"claude:gone".to_owned()));
    assert_eq!(lists(&world), [Some(false), Some(true), Some(false)]);
}

#[test]
fn the_brackets_walk_the_three_lists_both_ways_and_land_on_the_rows() {
    let mut world = start(fixture());
    plain(&mut world, Key::Text("]".into()));
    assert!(has(&world, "ledger.scope") && !has(&world, "ledger.refresh"));
    assert_eq!(focused(&world).as_deref(), Some("ledger.list"));
    plain(&mut world, Key::Text("]".into()));
    assert!(has(&world, "ledger.refresh"));
    assert_eq!(focused(&world).as_deref(), Some("ledger.list"));
    plain(&mut world, Key::Text("]".into()));
    assert!(has(&world, "ledger.deleted"));
    plain(&mut world, Key::Text("[".into()));
    assert!(has(&world, "ledger.refresh"));
    plain(&mut world, Key::Escape);
    open(&mut world);
    assert!(
        has(&world, "ledger.scope"),
        "the sessions shortcut opened another list"
    );
}

#[test]
fn claude_rows_at_the_home_keep_their_conversation_and_a_change_reads_the_sessions_again() {
    let mut codex = entry("codex", "Codex work", 4);
    codex.agent = "codex".into();
    let mut outpost = entry("far", "Far away", 3);
    outpost.host = Some("host-1".into());
    let mut world = World::start(ledger_daemon(
        state(vec![], vec![]),
        vec![
            entry("claude", "Claude work", 1),
            closed(entry("done", "Done", 2), "user"),
            codex,
            outpost,
        ],
    ));
    succeed(
        &world,
        "kept_conversation_keep",
        "kept_conversation_keep_result",
    );
    open(&mut world);

    assert_eq!(name(&world, "ledger.row.claude.verb"), "Keep conversation");
    click(&mut world, "ledger.row.done.more");
    assert!(text_in(&world, "ledger.row.done.menu").contains("Keep conversation"));
    plain(&mut world, Key::Escape);
    for id in ["codex", "far"] {
        click(&mut world, &format!("ledger.row.{id}"));
        assert!(
            !has(&world, "ledger.inspector.keep"),
            "{id} offered to keep"
        );
    }

    click(&mut world, "ledger.row.claude");
    click(&mut world, "ledger.inspector.keep");
    let pinned_at = Some("2026-10-01T12:00:00Z".to_owned());
    let mut kept = entry("claude", "Claude work", 1);
    kept.conversation_pinned_at = pinned_at.clone();
    let mut kept_done = closed(entry("done", "Done", 2), "user");
    kept_done.conversation_pinned_at = pinned_at;
    world.daemon.on("session_list", move |r| {
        super::page(r, vec![kept.clone(), kept_done.clone()])
    });
    let before = super::reads(&world).len();
    changed(&mut world);
    assert_eq!(super::reads(&world).len(), before + 1);
    click(&mut world, "ledger.row.done.more");
    assert!(text_in(&world, "ledger.row.done.menu").contains("Unkeep"));
    plain(&mut world, Key::Escape);
    click(&mut world, "ledger.row.claude");
    click(&mut world, "ledger.inspector.keep");
    assert_eq!(
        sent(&world, "kept_conversation_keep"),
        [
            serde_json::json!({"cmd": "kept_conversation_keep", "session_id": "session:claude", "keep": true}),
            serde_json::json!({"cmd": "kept_conversation_keep", "session_id": "session:claude", "keep": false}),
        ]
    );
}

#[test]
fn a_refused_pin_shows_on_the_row_until_a_reopen_takes_its_place() {
    let mut world = World::start(ledger_daemon(
        state(vec![], vec![]),
        vec![closed(entry("closed", "Closed work", 2), "user")],
    ));
    refuse(
        &world,
        "kept_conversation_keep",
        "kept_conversation_keep_result",
        "nothing left to keep",
    );
    let held = super::hold(&world, "session_reopen");
    open(&mut world);

    click(&mut world, "ledger.inspector.keep");
    assert_eq!(
        name(&world, "ledger.row.closed.note"),
        "nothing left to keep"
    );
    click(&mut world, "ledger.row.closed.verb");
    assert_eq!(name(&world, "ledger.row.closed.verb"), "reopening…");
    plain(&mut world, Key::Enter);
    let asked = held.lock().unwrap().clone();
    assert_eq!(asked.len(), 1, "a busy row reopened twice");
    world.daemon.reply_to(
        &asked[0],
        serde_json::json!({
            "event": "session_reopen_result",
            "request_id": asked[0]["request_id"],
            "success": false,
            "error": "the session changed after this row was listed",
        }),
    );
    world.settle();
    assert_eq!(
        name(&world, "ledger.row.closed.note"),
        "the session changed after this row was listed"
    );
}
