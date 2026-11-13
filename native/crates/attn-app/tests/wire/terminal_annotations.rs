use std::time::Duration;

use attn_protocol::{
    SessionAnnotation, SessionAnnotationsClearResultMessage, SessionAnnotationsGetResultMessage,
    SessionAnnotationsSaveResultMessage, SessionAnnotationsSubmitResultMessage, SessionMessage,
    SessionMessagesChangedMessage, SessionMessagesGetResultMessage,
};
use attn_testkit::{Answer, Daemon, Target, World, message};
use rune::{Key, KeyEvent, Mods};
use serde_json::{Value, json};

use super::terminal::{cell, output, pane, showing};
use super::terminal_view::{copied, view};

const MESSAGE: &str = "The cache keeps every row it reads until the window closes.";

fn agent_pane() -> attn_protocol::DesktopPane {
    attn_protocol::DesktopPane {
        session_id: "s1".into(),
        ..pane("t1", "r1")
    }
}

fn messages_result(messages: &[(&str, &str)], status: &str) -> SessionMessagesGetResultMessage {
    SessionMessagesGetResultMessage {
        session_id: "s1".into(),
        success: true,
        status: status.into(),
        messages: messages
            .iter()
            .map(|(key, markdown)| SessionMessage {
                key: (*key).into(),
                markdown: (*markdown).into(),
            })
            .collect(),
        ..SessionMessagesGetResultMessage::new()
    }
}

fn draft_result(annotations: Vec<SessionAnnotation>, generation: i64) -> Value {
    message(SessionAnnotationsGetResultMessage {
        session_id: "s1".into(),
        success: true,
        annotations,
        note: Some(String::new()),
        generation,
        ..SessionAnnotationsGetResultMessage::new()
    })
}

fn saved() -> Value {
    message(SessionAnnotationsSaveResultMessage {
        session_id: "s1".into(),
        success: true,
        ..SessionAnnotationsSaveResultMessage::new()
    })
}

/// An agent terminal of session s1, whose daemon holds `messages` and an empty draft.
fn agent(messages: &'static [(&'static str, &'static str)], status: &'static str) -> Daemon {
    let daemon = Daemon::scripted().state(showing(vec![agent_pane()], "daemon-1"));
    daemon
        .on("session_messages_get", move |_| {
            message(messages_result(messages, status))
        })
        .on("session_annotations_get", |_| draft_result(Vec::new(), 0))
        .on("session_annotations_save", |_| saved());
    daemon
}

fn started(daemon: Daemon) -> World {
    let mut world = World::start(daemon);
    world.open_desktop();
    output(&world, 1, format!("{MESSAGE}\r\n").as_bytes());
    world.settle();
    world
}

fn annotations(world: &World) -> Value {
    view(world)["annotations"].clone()
}

fn violet(world: &World) -> Vec<(u64, [u64; 2], String)> {
    view(world)["overlays"]
        .as_array()
        .unwrap()
        .iter()
        .filter(|o| o["color"] == "#a78bfa")
        .map(|o| {
            let cols = o["cols"].as_array().unwrap();
            (
                o["row"].as_u64().unwrap(),
                [cols[0].as_u64().unwrap(), cols[1].as_u64().unwrap()],
                o["kind"].as_str().unwrap().to_owned(),
            )
        })
        .collect()
}

fn alt() -> Mods {
    Mods {
        alt: true,
        ..Mods::default()
    }
}

/// Drags across cells of r1's screen with `mods` held.
fn drag_with(world: &mut World, mods: Mods, from: (u16, u16), to: (u16, u16)) {
    let (a, b) = (cell(world, from.0, from.1), cell(world, to.0, to.1));
    world.gesture(|app| app.hold(mods));
    world.gesture(|app| {
        app.runtime.pointer_down(a.0, a.1);
        app.runtime.pointer_move(b.0, b.1);
        app.runtime.pointer_up(b.0, b.1);
    });
    world.gesture(|app| app.hold(Mods::default()));
}

fn alt_click(world: &mut World, at: (u16, u16)) {
    let at = cell(world, at.0, at.1);
    world.gesture(|app| app.hold(alt()));
    world.gesture(|app| app.click_at(at));
    world.gesture(|app| app.hold(Mods::default()));
}

/// The drafts the app saved, without their request ids or mark ids.
fn saves(world: &World) -> Vec<Value> {
    world
        .daemon
        .sent_of("session_annotations_save")
        .into_iter()
        .map(|mut r| {
            let r = r.as_object_mut().unwrap();
            r.remove("request_id");
            r.remove("cmd");
            for mark in r["annotations"].as_array_mut().unwrap() {
                mark.as_object_mut().unwrap().remove("id");
            }
            Value::Object(r.clone())
        })
        .collect()
}

fn click(world: &mut World, key: &str) {
    let key = key.to_owned();
    world.gesture(|app| app.click(Target::key(key)));
}

fn mark_on_cache(world: &mut World) {
    drag_with(world, alt(), (4, 0), (20, 0));
}

#[test]
fn an_alt_drag_over_the_agents_message_marks_it_and_a_label_saves_the_draft() {
    let mut world = started(agent(&[("m1", MESSAGE)], "ready"));
    assert_eq!(world.daemon.sent_of("session_messages_get").len(), 1);
    assert_eq!(world.daemon.sent_of("session_annotations_get").len(), 1);

    mark_on_cache(&mut world);
    let state = annotations(&world);
    assert_eq!(state["marks"][0]["quote"], "cache keeps every");
    assert_eq!(state["marks"][0]["label"], "");
    assert_eq!(state["popup"]["writing"], false);
    assert_eq!(
        view(&world)["copies"],
        Value::Null,
        "marking copies nothing"
    );
    assert_eq!(copied(&world), None);
    assert_eq!(
        violet(&world),
        [
            (0, [4, 21], "Wash".to_owned()),
            (0, [4, 21], "Underline".to_owned())
        ]
    );
    assert!(
        saves(&world).is_empty(),
        "a mark is kept once it says something"
    );

    click(&mut world, "terminal.r1.annotation.label.i-agree");
    assert_eq!(annotations(&world)["popup"], Value::Null);
    assert_eq!(
        saves(&world),
        [json!({
            "session_id": "s1",
            "annotations": [{
                "message_key": "m1",
                "start": 4,
                "end": 21,
                "quote": "cache keeps every",
                "quick_label_id": "i-agree",
                "comment": "",
            }],
            "note": "",
            "generation": 1,
        })]
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn dismissing_the_popup_of_a_mark_that_says_nothing_takes_the_mark_away() {
    let mut world = started(agent(&[("m1", MESSAGE)], "ready"));
    mark_on_cache(&mut world);
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Escape,
            mods: Mods::default(),
        })
    });
    assert_eq!(annotations(&world)["marks"], json!([]));
    assert_eq!(annotations(&world)["popup"], Value::Null);
    assert_eq!(violet(&world), []);
    assert_eq!(saves(&world)[0]["annotations"], json!([]));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_press_elsewhere_in_the_terminal_dismisses_the_popup() {
    let mut world = started(agent(&[("m1", MESSAGE)], "ready"));
    mark_on_cache(&mut world);
    let elsewhere = cell(&world, 2, 5);
    world.gesture(|app| app.click_at(elsewhere));
    assert_eq!(annotations(&world)["popup"], Value::Null);
    assert_eq!(annotations(&world)["marks"], json!([]));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_alt_click_on_a_mark_opens_it_and_a_comment_keeps_it() {
    let mut world = started(agent(&[("m1", MESSAGE)], "ready"));
    mark_on_cache(&mut world);
    click(&mut world, "terminal.r1.annotation.label.this-is-wrong");

    alt_click(&mut world, (10, 0));
    assert_eq!(annotations(&world)["popup"]["writing"], false);
    click(&mut world, "terminal.r1.annotation.write");
    world.gesture(|app| app.type_text("Rows go after a minute"));
    world.gesture(|app| app.press("mod+enter"));

    let state = annotations(&world);
    assert_eq!(state["popup"], Value::Null);
    assert_eq!(state["marks"][0]["label"], "this-is-wrong");
    assert_eq!(state["marks"][0]["comment"], "Rows go after a minute");
    let last = saves(&world).pop().unwrap();
    assert_eq!(last["annotations"][0]["comment"], "Rows go after a minute");
    assert_eq!(last["generation"], 2);

    alt_click(&mut world, (10, 0));
    let popup = &annotations(&world)["popup"];
    assert_eq!(
        popup["writing"], true,
        "a mark with a comment opens writing"
    );
    assert_eq!(popup["comment"], "Rows go after a minute");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn picking_the_mark_label_again_takes_it_off_and_a_mark_left_bare_goes() {
    let mut world = started(agent(&[("m1", MESSAGE)], "ready"));
    mark_on_cache(&mut world);
    click(&mut world, "terminal.r1.annotation.more");
    assert_eq!(annotations(&world)["popup"]["picking"], true);
    world.gesture(|app| app.type_text("1"));
    assert_eq!(annotations(&world)["marks"][0]["label"], "exactly-this");
    assert_eq!(annotations(&world)["popup"], Value::Null);

    alt_click(&mut world, (10, 0));
    click(&mut world, "terminal.r1.annotation.more");
    click(&mut world, "terminal.r1.annotation.pick.exactly-this");
    assert_eq!(annotations(&world)["marks"], json!([]));
    assert_eq!(saves(&world).len(), 2);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_alt_drag_outside_the_agents_messages_says_why_and_copies_what_it_selected() {
    let mut world = started(agent(&[("m1", MESSAGE)], "ready"));
    output(&world, 2, "❯ restart the cache\r\n".as_bytes());
    world.settle();
    drag_with(&mut world, alt(), (2, 1), (8, 1));
    assert_eq!(
        annotations(&world)["notice"],
        "Only what the agent wrote can be annotated. This text is the TUI’s own, your own, or from a turn that has scrolled out of the window."
    );
    assert_eq!(annotations(&world)["marks"], json!([]));
    assert_eq!(copied(&world).as_deref(), Some("restart"));
    world.advance(Duration::from_secs(5));
    assert_eq!(annotations(&world)["notice"], Value::Null);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_alt_drag_before_the_agent_wrote_says_what_the_transcript_is_doing() {
    for (status, says) in [
        (
            "discovering",
            "The agent’s first completed message is still being recorded. Try again in a moment.",
        ),
        (
            "ready",
            "The agent has not written a message to annotate yet.",
        ),
        (
            "unavailable",
            "No exact transcript is available for this session.",
        ),
    ] {
        let mut world = started(agent(&[], status));
        drag_with(&mut world, alt(), (4, 0), (20, 0));
        assert_eq!(annotations(&world)["notice"], says, "{status}");
        assert_eq!(copied(&world).as_deref(), Some("cache keeps every"));
        world.advance(Duration::from_secs(5));
        assert!(world.runtime_is_at_rest());
    }
}

#[test]
fn a_drag_without_alt_selects_and_copies_over_the_agents_message() {
    let mut world = started(agent(&[("m1", MESSAGE)], "ready"));
    drag_with(&mut world, Mods::default(), (4, 0), (20, 0));
    assert_eq!(copied(&world).as_deref(), Some("cache keeps every"));
    assert_eq!(annotations(&world)["marks"], json!([]));
    assert!(world.runtime_is_at_rest());
}

fn stored_mark(label: &str) -> SessionAnnotation {
    SessionAnnotation {
        id: "a1".into(),
        message_key: "m1".into(),
        start: 4,
        end: 21,
        quote: "cache keeps every".into(),
        quick_label_id: Some(label.into()),
        comment: String::new(),
        emoji: None,
    }
}

#[test]
fn a_stored_draft_draws_on_its_message_and_a_mark_whose_text_left_the_screen_is_not_drawn() {
    let daemon = Daemon::scripted().state(showing(vec![agent_pane()], "daemon-1"));
    daemon
        .on("session_messages_get", |_| {
            message(messages_result(&[("m1", MESSAGE)], "ready"))
        })
        .on("session_annotations_get", |_| {
            draft_result(vec![stored_mark("cut-this")], 4)
        });
    let mut world = started(daemon);
    assert_eq!(annotations(&world)["generation"], 4);
    assert_eq!(violet(&world).len(), 2);

    output(
        &world,
        2,
        b"\x1b[H\x1b[2J\x1b[3JThe cache drops every row\r\n",
    );
    world.settle();
    assert_eq!(violet(&world), [], "the rows no longer say what was quoted");
    assert_eq!(annotations(&world)["marks"][0]["label"], "cut-this");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_draft_stored_with_an_emoji_reads_its_label() {
    let daemon = Daemon::scripted().state(showing(vec![agent_pane()], "daemon-1"));
    daemon
        .on("session_messages_get", |_| {
            message(messages_result(&[("m1", MESSAGE)], "ready"))
        })
        .on("session_annotations_get", |_| {
            draft_result(
                vec![SessionAnnotation {
                    quick_label_id: None,
                    emoji: Some("🪓".into()),
                    ..stored_mark("")
                }],
                1,
            )
        });
    let world = started(daemon);
    assert_eq!(annotations(&world)["marks"][0]["label"], "cut-this");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_changed_message_list_is_fetched_again_and_a_mark_draws_once_its_message_arrives() {
    let daemon = Daemon::scripted().state(showing(vec![agent_pane()], "daemon-1"));
    daemon
        .answer_in_turn(
            "session_messages_get",
            [
                Answer::Reply(message(messages_result(&[], "discovering"))),
                Answer::Reply(message(messages_result(&[("m1", MESSAGE)], "ready"))),
            ],
        )
        .on("session_annotations_get", |_| {
            draft_result(vec![stored_mark("i-agree")], 1)
        });
    let mut world = started(daemon);
    assert_eq!(violet(&world), []);

    world.daemon.emit(SessionMessagesChangedMessage {
        session_id: "s1".into(),
        ..SessionMessagesChangedMessage::new()
    });
    world.settle();
    assert_eq!(world.daemon.sent_of("session_messages_get").len(), 2);
    assert_eq!(violet(&world).len(), 2);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_reconnect_fetches_the_messages_and_the_draft_again() {
    let mut world = started(agent(&[("m1", MESSAGE)], "ready"));
    world.reconnect();
    world.settle();
    assert_eq!(world.daemon.sent_of("session_messages_get").len(), 2);
    assert_eq!(world.daemon.sent_of("session_annotations_get").len(), 2);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_save_the_daemon_holds_a_newer_draft_for_reads_that_draft() {
    let daemon = Daemon::scripted().state(showing(vec![agent_pane()], "daemon-1"));
    daemon
        .on("session_messages_get", |_| {
            message(messages_result(&[("m1", MESSAGE)], "ready"))
        })
        .answer_in_turn(
            "session_annotations_get",
            [
                Answer::Reply(draft_result(Vec::new(), 0)),
                Answer::Reply(draft_result(vec![stored_mark("cut-this")], 7)),
            ],
        )
        .on("session_annotations_save", |_| {
            message(SessionAnnotationsSaveResultMessage {
                session_id: "s1".into(),
                success: false,
                stale: Some(true),
                ..SessionAnnotationsSaveResultMessage::new()
            })
        });
    let mut world = started(daemon);
    mark_on_cache(&mut world);
    click(&mut world, "terminal.r1.annotation.label.i-agree");
    let state = annotations(&world);
    assert_eq!(state["generation"], 7);
    assert_eq!(state["marks"][0]["id"], "a1");
    assert_eq!(state["marks"][0]["label"], "cut-this");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn alt_over_a_mark_brightens_it() {
    let mut world = started(agent(&[("m1", MESSAGE)], "ready"));
    mark_on_cache(&mut world);
    click(&mut world, "terminal.r1.annotation.label.i-agree");
    let (over, off) = (cell(&world, 10, 0), cell(&world, 40, 3));
    world.gesture(|app| app.hold(alt()));
    world.gesture(|app| app.runtime.pointer_move(over.0, over.1));
    let alphas = |world: &World| -> Vec<u64> {
        view(world)["overlays"]
            .as_array()
            .unwrap()
            .iter()
            .filter(|o| o["color"] == "#a78bfa")
            .map(|o| o["alpha"].as_u64().unwrap())
            .collect()
    };
    assert_eq!(alphas(&world), [107, 255]);
    world.gesture(|app| app.runtime.pointer_move(off.0, off.1));
    assert_eq!(alphas(&world), [61, 191]);
    world.gesture(|app| app.hold(Mods::default()));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_mark_after_a_character_of_two_utf16_units_quotes_the_words_it_covers() {
    const ROCKET: &str = "🚀 cache keeps every row it reads until the window closes.";
    let daemon = agent(&[("m1", ROCKET)], "ready");
    let mut world = World::start(daemon);
    world.open_desktop();
    output(&world, 1, format!("{ROCKET}\r\n").as_bytes());
    world.settle();
    drag_with(&mut world, alt(), (3, 0), (19, 0));
    let mark = &annotations(&world)["marks"][0];
    assert_eq!(mark["quote"], "cache keeps every");
    assert_eq!(
        (mark["start"].clone(), mark["end"].clone()),
        (json!(3), json!(20))
    );
    assert!(world.runtime_is_at_rest());
}

fn submitted(world: &World) -> Vec<String> {
    world
        .daemon
        .sent_of("session_annotations_submit")
        .iter()
        .map(|r| r["text"].as_str().unwrap().to_owned())
        .collect()
}

fn submit_answers(daemon: &Daemon, success: bool, status: &'static str) {
    daemon.on("session_annotations_submit", move |_| {
        message(SessionAnnotationsSubmitResultMessage {
            session_id: "s1".into(),
            success,
            status: status.into(),
            ..SessionAnnotationsSubmitResultMessage::new()
        })
    });
}

fn cleared(world: &World) -> Vec<Value> {
    world
        .daemon
        .sent_of("session_annotations_clear")
        .iter()
        .map(|r| r["generation"].clone())
        .collect()
}

/// The send key: ⌘Enter on macOS, Ctrl+Shift+Enter elsewhere.
const SEND: &str = if cfg!(target_os = "macos") {
    "mod+enter"
} else {
    "mod+shift+enter"
};

#[test]
fn the_send_key_sends_the_marks_and_the_note_as_one_message_and_clears_the_draft() {
    let daemon = agent(&[("m1", MESSAGE)], "ready");
    submit_answers(&daemon, true, "delivered");
    daemon.on("session_annotations_clear", |r| {
        message(SessionAnnotationsClearResultMessage {
            session_id: "s1".into(),
            success: true,
            generation: r["generation"].as_i64().unwrap(),
            ..SessionAnnotationsClearResultMessage::new()
        })
    });
    let mut world = started(daemon);
    mark_on_cache(&mut world);
    click(&mut world, "terminal.r1.annotation.label.this-is-wrong");
    assert!(
        world
            .screen()
            .find(Target::key("terminal.r1.annotation.panel"))
            .is_some()
    );

    click(&mut world, "terminal.r1.annotation.note.field");
    world.gesture(|app| app.type_text("Look again"));
    world.advance(Duration::from_millis(400));
    assert_eq!(saves(&world).last().unwrap()["note"], "Look again");

    let terminal = cell(&world, 30, 4);
    world.gesture(|app| app.click_at(terminal));
    world.gesture(|app| app.press(SEND));
    assert_eq!(
        submitted(&world),
        [payload(
            &[("cache keeps every", "this-is-wrong", "")],
            "Look again"
        )]
    );
    let state = annotations(&world);
    assert_eq!(state["marks"], json!([]));
    assert_eq!(state["note"], "");
    assert_eq!(
        state["outcome"],
        json!({"kind": "sent", "count": 1, "kept": 0})
    );
    assert_eq!(cleared(&world), [json!(3)]);
    assert_eq!(violet(&world), []);

    world.advance(Duration::from_millis(2200));
    assert_eq!(annotations(&world)["outcome"], Value::Null);
    assert!(
        world
            .screen()
            .find(Target::key("terminal.r1.annotation.panel"))
            .is_none()
    );
    assert!(world.runtime_is_at_rest());
}

/// What the app sends for marks of (quote, label, comment) and `note`, as the prompts render it.
pub(crate) fn payload(marks: &[(&str, &str, &str)], note: &str) -> String {
    let quoted: Vec<attn_app::annotations::Quoted> = marks
        .iter()
        .enumerate()
        .map(
            |(i, (quote, label, comment))| attn_app::annotations::Quoted {
                quote,
                label,
                comment,
                start: i,
            },
        )
        .collect();
    attn_app::annotations::terminal_payload(&quoted, note).unwrap()
}

#[test]
fn a_session_waiting_on_an_approval_keeps_the_marks_and_says_why() {
    let daemon = agent(&[("m1", MESSAGE)], "ready");
    submit_answers(&daemon, false, "skipped_pending_approval");
    let mut world = started(daemon);
    mark_on_cache(&mut world);
    click(&mut world, "terminal.r1.annotation.label.i-agree");
    click(&mut world, "terminal.r1.annotation.send");
    let state = annotations(&world);
    assert_eq!(state["outcome"], json!({"kind": "skipped"}));
    assert_eq!(state["marks"].as_array().unwrap().len(), 1);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_session_that_did_not_take_the_feedback_keeps_the_marks() {
    let daemon = agent(&[("m1", MESSAGE)], "ready");
    submit_answers(&daemon, true, "error");
    let mut world = started(daemon);
    mark_on_cache(&mut world);
    click(&mut world, "terminal.r1.annotation.label.i-agree");
    click(&mut world, "terminal.r1.annotation.send");
    let state = annotations(&world);
    assert_eq!(
        state["outcome"],
        json!({"kind": "error", "message": "The session did not take the feedback. Nothing was sent."})
    );
    assert_eq!(state["marks"].as_array().unwrap().len(), 1);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_mark_changed_while_the_send_was_in_flight_stays_and_is_saved() {
    let daemon = agent(&[("m1", MESSAGE)], "ready");
    daemon.answer_in_turn("session_annotations_submit", [Answer::Hold]);
    let mut world = started(daemon);
    mark_on_cache(&mut world);
    click(&mut world, "terminal.r1.annotation.label.i-agree");
    click(&mut world, "terminal.r1.annotation.send");
    assert_eq!(annotations(&world)["outcome"], json!({"kind": "sending"}));

    alt_click(&mut world, (10, 0));
    click(&mut world, "terminal.r1.annotation.label.this-is-wrong");
    let request = world.daemon.sent_of("session_annotations_submit")[0].clone();
    world.daemon.reply_to(
        &request,
        SessionAnnotationsSubmitResultMessage {
            session_id: "s1".into(),
            success: true,
            status: "delivered".into(),
            ..SessionAnnotationsSubmitResultMessage::new()
        },
    );
    world.settle();
    let state = annotations(&world);
    assert_eq!(
        state["outcome"],
        json!({"kind": "sent", "count": 1, "kept": 1})
    );
    assert_eq!(state["marks"][0]["label"], "this-is-wrong");
    assert_eq!(
        saves(&world).last().unwrap()["annotations"][0]["quick_label_id"],
        "this-is-wrong"
    );
    assert!(world.daemon.sent_of("session_annotations_clear").is_empty());
    world.advance(Duration::from_millis(2200));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_card_opens_its_mark_to_write_and_its_cross_removes_it() {
    let mut world = started(agent(&[("m1", MESSAGE)], "ready"));
    mark_on_cache(&mut world);
    click(&mut world, "terminal.r1.annotation.label.i-agree");
    let mark = annotations(&world)["marks"][0]["id"]
        .as_str()
        .unwrap()
        .to_owned();

    click(&mut world, &format!("terminal.r1.annotation.card.{mark}"));
    assert_eq!(annotations(&world)["popup"]["writing"], true);
    world.gesture(|app| app.press("Escape"));
    assert_eq!(annotations(&world)["popup"], Value::Null);
    assert_eq!(annotations(&world)["marks"].as_array().unwrap().len(), 1);

    click(
        &mut world,
        &format!("terminal.r1.annotation.card.{mark}.remove"),
    );
    assert_eq!(annotations(&world)["marks"], json!([]));
    assert_eq!(saves(&world).last().unwrap()["annotations"], json!([]));
    assert!(world.runtime_is_at_rest());
}
