use std::sync::{Arc, Mutex};

use attn_protocol::{KeptConversation, SeedContinuation};
use attn_testkit::{Request, Target, World};
use serde_json::json;

use super::*;

const TITLE: &str = "Keep the parser conversation";
const KEPT_LINE: &str = "conversation kept by attn (1.6 MB) while an open seed points at it";

fn kept() -> KeptConversation {
    KeptConversation {
        bytes: 1_600_000,
        copied_at: "2026-10-01T12:00:00Z".into(),
        ..KeptConversation::default()
    }
}

fn continuation(kept: Option<KeptConversation>) -> SeedContinuation {
    SeedContinuation {
        agent: "claude".into(),
        cwd: "/tmp/parser".into(),
        directory_state: "present".into(),
        execution_id: "execution-1".into(),
        resume_available: true,
        source: "last_execution".into(),
        handover_placement: "reuse_cwd".into(),
        kept_conversation: kept,
        ..SeedContinuation::default()
    }
}

fn held() -> Seed {
    seed("s-kept11", TITLE)
}

fn read_as(continuation: SeedContinuation) -> Seed {
    with(held(), |s| s.continuation = Some(continuation))
}

/// The garden holds the seed without its continuation, as a push carries it, and each read
/// answers with `document`, which a test changes as the daemon would.
struct Kept {
    world: World,
    document: Arc<Mutex<Seed>>,
}

impl Kept {
    fn open(document: Seed) -> Self {
        let world_daemon = Daemon::scripted().state(initial(vec![held()], Vec::new()));
        let document = Arc::new(Mutex::new(document));
        let mut kept = Kept {
            world: open_garden_with(world_daemon),
            document,
        };
        kept.answer_reads();
        open_row(&mut kept.world, TITLE);
        kept
    }

    fn answer_reads(&mut self) {
        let document = self.document.clone();
        self.world
            .daemon
            .on("seed_document_get", move |r: &Request| {
                document_reply(r, Some(document.lock().unwrap().clone()))
            });
    }

    fn hold_reads(&mut self) {
        self.world.daemon.on("seed_document_get", |_: &Request| ());
    }

    fn read_as(&mut self, document: Seed) {
        *self.document.lock().unwrap() = document;
    }

    fn push(&mut self, seeds: Vec<Seed>) {
        push(&mut self.world, seeds);
    }

    fn reads(&self) -> usize {
        assert!(self.world.daemon.sent_of("seed_resume").is_empty());
        self.world.daemon.sent_of("seed_document_get").len()
    }

    fn kept_line(&self) -> Option<String> {
        self.note("garden.head.kept")
    }

    fn resume_reason(&self) -> Option<String> {
        self.note("garden.head.resume-reason")
    }

    fn note(&self, key: &str) -> Option<String> {
        self.world
            .screen()
            .find(Target::key(key))
            .and_then(|n| n.name)
    }

    fn offers(&self, name: &str) -> bool {
        self.world
            .screen()
            .find(Target::role("button").name(name))
            .is_some()
    }
}

#[test]
fn renders_the_daemons_retention_state() {
    let day = |d: &str| format!("2026-10-{d}");
    let cases = [
        (kept(), KEPT_LINE.to_owned()),
        (
            KeptConversation {
                bytes: 4_200,
                ..kept()
            },
            "conversation kept by attn (4.2 KB) while an open seed points at it".into(),
        ),
        (
            KeptConversation {
                bytes: 42,
                ..kept()
            },
            "conversation kept by attn (42 B) while an open seed points at it".into(),
        ),
        (
            KeptConversation {
                pinned_at: Some(format!("{}T12:00:00Z", day("01"))),
                ..kept()
            },
            "conversation kept by attn (1.6 MB) forever; pinned".into(),
        ),
        (
            KeptConversation {
                delete_after: Some(format!("{}T23:30:00Z", day("13"))),
                ..kept()
            },
            "conversation kept by attn (1.6 MB) until 2026-10-13; replant to keep it".into(),
        ),
        (
            KeptConversation {
                delete_after: Some(format!("{}T23:30:00Z", day("13"))),
                deleted_at: Some(format!("{}T01:00:00Z", day("14"))),
                ..kept()
            },
            "conversation attn deleted its copy on 2026-10-14".into(),
        ),
        (
            KeptConversation {
                deleted_at: Some(format!("{}T01:00:00Z", day("14"))),
                deleted_by: Some("user".into()),
                ..kept()
            },
            "conversation you deleted attn’s copy on 2026-10-14".into(),
        ),
    ];
    for (retention, line) in cases {
        let mut kept = Kept::open(read_as(continuation(Some(retention))));
        assert_eq!(kept.kept_line(), Some(line));
        assert_eq!(kept.reads(), 1);
        assert!(rests(&mut kept.world));
    }
}

#[test]
fn says_why_resume_is_unavailable_including_the_daemons_deletion_date() {
    let reason = "attn deleted its copy of conversation original on 2026-10-14, 14 days after no open seed pointed at it";
    let kept = Kept::open(read_as(SeedContinuation {
        resume_available: false,
        resume_reason: Some(reason.into()),
        ..continuation(Some(KeptConversation {
            deleted_at: Some("2026-10-14T01:00:00Z".into()),
            ..self::kept()
        }))
    }));
    assert!(!kept.offers("Resume"));
    assert_eq!(kept.resume_reason().as_deref(), Some(reason));
    assert_eq!(kept.reads(), 1);
}

#[test]
fn shows_retention_on_a_closed_seed_whose_conversation_another_open_seed_keeps() {
    let mut kept = Kept::open(read_as(continuation(Some(self::kept()))));
    let harvested = |s: Seed| {
        with(s, |s| {
            s.rev = 2;
            s.status = "harvested".into();
        })
    };
    kept.read_as(harvested(read_as(continuation(Some(self::kept())))));
    kept.push(vec![harvested(held())]);
    assert_eq!(kept.kept_line().as_deref(), Some(KEPT_LINE));
    assert!(!kept.offers("Resume"));
    assert_eq!(kept.reads(), 2);
}

#[test]
fn omits_retention_when_the_daemon_has_no_kept_copy() {
    let kept = Kept::open(read_as(SeedContinuation {
        resume_reason: Some("An obsolete refusal".into()),
        ..continuation(None)
    }));
    assert_eq!(kept.kept_line(), None);
    assert_eq!(kept.resume_reason(), None);
    assert!(kept.offers("Resume"));
    assert_eq!(kept.reads(), 1);
}

#[test]
fn waits_for_the_current_seed_document_before_describing_its_conversation() {
    let mut kept = Kept::open(read_as(continuation(Some(self::kept()))));
    let second = |s: Seed| with(s, |s| s.rev = 2);
    kept.hold_reads();
    kept.read_as(second(read_as(continuation(Some(self::kept())))));
    kept.push(vec![second(held())]);
    assert_eq!(kept.kept_line(), None);

    let read = kept.world.daemon.sent_of("seed_document_get")[1].clone();
    let answer = document_reply(&read, Some(kept.document.lock().unwrap().clone()));
    kept.world.daemon.reply_to(&read, answer);
    kept.world.settle();
    assert_eq!(kept.kept_line().as_deref(), Some(KEPT_LINE));
    assert_eq!(kept.reads(), 2);
    assert!(rests(&mut kept.world));
}

#[test]
fn invalidates_retention_on_a_same_revision_push_even_when_the_read_fails() {
    let mut kept = Kept::open(read_as(continuation(Some(self::kept()))));
    let reason = "attn deleted its copy on 2026-10-14";
    kept.hold_reads();
    kept.read_as(read_as(SeedContinuation {
        resume_available: false,
        resume_reason: Some(reason.into()),
        ..continuation(Some(KeptConversation {
            deleted_at: Some("2026-10-14T01:00:00Z".into()),
            ..self::kept()
        }))
    }));
    kept.push(vec![held()]);
    assert_eq!(kept.kept_line(), None);

    let read = kept.world.daemon.sent_of("seed_document_get")[1].clone();
    kept.world.daemon.reply_to(
        &read,
        json!({"event": "seed_document_get_result", "success": false, "error": "document unavailable"}),
    );
    kept.world.settle();
    assert_eq!(kept.kept_line(), None);

    kept.answer_reads();
    kept.push(vec![held()]);
    assert_eq!(
        kept.kept_line().as_deref(),
        Some("conversation attn deleted its copy on 2026-10-14")
    );
    assert_eq!(kept.resume_reason().as_deref(), Some(reason));
    assert!(!kept.offers("Resume"));
    assert_eq!(kept.reads(), 3);
}

#[test]
fn refreshes_the_kept_line_once_from_the_push_that_follows_a_conversation_fact() {
    let mut kept = Kept::open(read_as(continuation(Some(self::kept()))));
    kept.read_as(read_as(continuation(Some(KeptConversation {
        delete_after: Some("2026-10-15T12:00:00Z".into()),
        ..self::kept()
    }))));
    kept.world
        .daemon
        .emit(json!({"event": "kept_conversations_changed"}));
    kept.world.settle();
    assert_eq!(kept.reads(), 1);

    kept.push(vec![held()]);
    assert_eq!(
        kept.kept_line().as_deref(),
        Some("conversation kept by attn (1.6 MB) until 2026-10-15; replant to keep it")
    );
    assert!(kept.offers("Resume"));
    assert_eq!(kept.reads(), 2);
}

#[test]
fn keeps_continuation_actions_and_their_keyboard_focus_during_an_unrelated_push() {
    let mut kept = Kept::open(read_as(continuation(Some(self::kept()))));
    let resume = kept
        .world
        .screen()
        .get(Target::role("button").name("Resume"))
        .key
        .unwrap();
    let focusing = resume.clone();
    kept.world
        .gesture(|app| app.update(|_, tx| tx.focus(&focusing, false)));
    assert_eq!(
        focused(&kept.world).map(|n| n.key),
        Some(Some(resume.clone()))
    );

    kept.hold_reads();
    kept.push(vec![held(), seed("s-other1", "Other work")]);
    assert_eq!(focused(&kept.world).map(|n| n.key), Some(Some(resume)));
    assert!(kept.offers("Handover"));
    assert_eq!(kept.reads(), 2);
}
