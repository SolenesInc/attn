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

fn annotations(world: &mut World) -> Value {
    view(world)["annotations"].clone()
}

fn violet(world: &mut World) -> Vec<(u64, [u64; 2], String)> {
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
    let state = annotations(&mut world);
    assert_eq!(state["marks"][0]["quote"], "cache keeps every");
    assert_eq!(state["marks"][0]["label"], "");
    assert_eq!(state["popup"]["writing"], false);
    assert_eq!(
        view(&mut world)["copies"],
        Value::Null,
        "marking copies nothing"
    );
    assert_eq!(copied(&mut world), None);
    assert_eq!(
        violet(&mut world),
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
    assert_eq!(annotations(&mut world)["popup"], Value::Null);
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
    assert_eq!(annotations(&mut world)["marks"], json!([]));
    assert_eq!(annotations(&mut world)["popup"], Value::Null);
    assert_eq!(violet(&mut world), []);
    assert_eq!(saves(&world)[0]["annotations"], json!([]));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_press_elsewhere_in_the_terminal_dismisses_the_popup() {
    let mut world = started(agent(&[("m1", MESSAGE)], "ready"));
    mark_on_cache(&mut world);
    let elsewhere = cell(&world, 2, 5);
    world.gesture(|app| app.click_at(elsewhere));
    assert_eq!(annotations(&mut world)["popup"], Value::Null);
    assert_eq!(annotations(&mut world)["marks"], json!([]));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_alt_click_on_a_mark_opens_it_and_a_comment_keeps_it() {
    let mut world = started(agent(&[("m1", MESSAGE)], "ready"));
    mark_on_cache(&mut world);
    click(&mut world, "terminal.r1.annotation.label.this-is-wrong");

    alt_click(&mut world, (10, 0));
    assert_eq!(annotations(&mut world)["popup"]["writing"], false);
    click(&mut world, "terminal.r1.annotation.write");
    world.gesture(|app| app.type_text("Rows go after a minute"));
    world.gesture(|app| app.press("mod+enter"));

    let state = annotations(&mut world);
    assert_eq!(state["popup"], Value::Null);
    assert_eq!(state["marks"][0]["label"], "this-is-wrong");
    assert_eq!(state["marks"][0]["comment"], "Rows go after a minute");
    let last = saves(&world).pop().unwrap();
    assert_eq!(last["annotations"][0]["comment"], "Rows go after a minute");
    assert_eq!(last["generation"], 2);

    alt_click(&mut world, (10, 0));
    let popup = &annotations(&mut world)["popup"];
    assert_eq!(
        popup["writing"], true,
        "a mark with a comment opens writing"
    );
    assert_eq!(popup["comment"], "Rows go after a minute");
    world.advance(Duration::from_millis(400));
    assert_eq!(
        saves(&world).len(),
        2,
        "the comment's save took the pending one"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn picking_the_mark_label_again_takes_it_off_and_a_mark_left_bare_goes() {
    let mut world = started(agent(&[("m1", MESSAGE)], "ready"));
    mark_on_cache(&mut world);
    click(&mut world, "terminal.r1.annotation.more");
    assert_eq!(annotations(&mut world)["popup"]["picking"], true);
    world.gesture(|app| app.type_text("1"));
    assert_eq!(annotations(&mut world)["marks"][0]["label"], "exactly-this");
    assert_eq!(annotations(&mut world)["popup"], Value::Null);

    alt_click(&mut world, (10, 0));
    click(&mut world, "terminal.r1.annotation.more");
    click(&mut world, "terminal.r1.annotation.pick.exactly-this");
    assert_eq!(annotations(&mut world)["marks"], json!([]));
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
        annotations(&mut world)["notice"],
        "Only what the agent wrote can be annotated. This text is the TUI’s own, your own, or from a turn that has scrolled out of the window."
    );
    assert_eq!(annotations(&mut world)["marks"], json!([]));
    assert_eq!(copied(&mut world).as_deref(), Some("restart"));
    world.advance(Duration::from_secs(5));
    assert_eq!(annotations(&mut world)["notice"], Value::Null);
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
        assert_eq!(annotations(&mut world)["notice"], says, "{status}");
        assert_eq!(copied(&mut world).as_deref(), Some("cache keeps every"));
        world.advance(Duration::from_secs(5));
        assert!(world.runtime_is_at_rest());
    }
}

#[test]
fn a_drag_without_alt_selects_and_copies_over_the_agents_message() {
    let mut world = started(agent(&[("m1", MESSAGE)], "ready"));
    drag_with(&mut world, Mods::default(), (4, 0), (20, 0));
    assert_eq!(copied(&mut world).as_deref(), Some("cache keeps every"));
    assert_eq!(annotations(&mut world)["marks"], json!([]));
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
    assert_eq!(annotations(&mut world)["generation"], 4);
    assert_eq!(violet(&mut world).len(), 2);

    output(
        &world,
        2,
        b"\x1b[H\x1b[2J\x1b[3JThe cache drops every row\r\n",
    );
    world.settle();
    assert_eq!(
        violet(&mut world),
        [],
        "the rows no longer say what was quoted"
    );
    assert_eq!(annotations(&mut world)["marks"][0]["label"], "cut-this");
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
    let mut world = started(daemon);
    assert_eq!(annotations(&mut world)["marks"][0]["label"], "cut-this");
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
    assert_eq!(violet(&mut world), []);

    world.daemon.emit(SessionMessagesChangedMessage {
        session_id: "s1".into(),
        ..SessionMessagesChangedMessage::new()
    });
    world.settle();
    assert_eq!(world.daemon.sent_of("session_messages_get").len(), 2);
    assert_eq!(violet(&mut world).len(), 2);
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
    let state = annotations(&mut world);
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
    let alphas = |world: &mut World| -> Vec<u64> {
        view(world)["overlays"]
            .as_array()
            .unwrap()
            .iter()
            .filter(|o| o["color"] == "#a78bfa")
            .map(|o| o["alpha"].as_u64().unwrap())
            .collect()
    };
    assert_eq!(alphas(&mut world), [107, 255]);
    world.gesture(|app| app.runtime.pointer_move(off.0, off.1));
    assert_eq!(alphas(&mut world), [61, 191]);
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
    let mark = &annotations(&mut world)["marks"][0];
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
    let state = annotations(&mut world);
    assert_eq!(state["marks"], json!([]));
    assert_eq!(state["note"], "");
    assert_eq!(
        state["outcome"],
        json!({"kind": "sent", "count": 1, "kept": 0})
    );
    assert_eq!(cleared(&world), [json!(3)]);
    assert_eq!(violet(&mut world), []);

    world.advance(Duration::from_millis(2200));
    assert_eq!(annotations(&mut world)["outcome"], Value::Null);
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
    let state = annotations(&mut world);
    assert_eq!(state["outcome"], json!({"kind": "skipped"}));
    assert_eq!(state["marks"].as_array().unwrap().len(), 1);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_comment_being_written_when_a_send_is_refused_is_saved_with_the_draft() {
    let daemon = agent(&[("m1", MESSAGE)], "ready");
    submit_answers(&daemon, false, "skipped_pending_approval");
    let mut world = started(daemon);
    mark_on_cache(&mut world);
    click(&mut world, "terminal.r1.annotation.label.this-is-wrong");
    alt_click(&mut world, (10, 0));
    click(&mut world, "terminal.r1.annotation.write");
    world.gesture(|app| app.type_text("Rows go after a minute"));

    click(&mut world, "terminal.r1.annotation.send");
    let state = annotations(&mut world);
    assert_eq!(state["outcome"], json!({"kind": "skipped"}));
    assert_eq!(state["marks"][0]["comment"], "Rows go after a minute");
    let last = saves(&world).pop().unwrap();
    assert_eq!(
        last["annotations"][0]["comment"], "Rows go after a minute",
        "the daemon holds the comment, so a relaunch or another terminal reads it back"
    );
    assert_eq!(submitted(&world).len(), 1);
    world.advance(Duration::from_millis(400));
    assert!(world.runtime_is_at_rest());
}

type Stored = std::sync::Arc<std::sync::Mutex<(Vec<SessionAnnotation>, i64)>>;

fn remembering() -> Daemon {
    remembering_in(Stored::default())
}

/// An agent terminal of session s1 whose daemon keeps the last draft saved in `stored` and
/// hands it back.
fn remembering_in(stored: Stored) -> Daemon {
    let (kept, read) = (stored.clone(), stored);
    let daemon = agent(&[("m1", MESSAGE)], "ready");
    daemon
        .on("session_annotations_save", move |r| {
            let annotations = serde_json::from_value(r["annotations"].clone()).unwrap();
            *kept.lock().unwrap() = (annotations, r["generation"].as_i64().unwrap());
            saved()
        })
        .on("session_annotations_get", move |_| {
            let (annotations, generation) = read.lock().unwrap().clone();
            draft_result(annotations, generation)
        });
    daemon
}

/// Types `comment` into a new mark's popup, without keeping it.
fn typing_a_comment(world: &mut World, comment: &str) {
    mark_on_cache(world);
    click(world, "terminal.r1.annotation.label.this-is-wrong");
    alt_click(world, (10, 0));
    click(world, "terminal.r1.annotation.write");
    world.gesture(|app| app.type_text(comment));
}

fn arranged(world: &mut World, current: &str, panes: Vec<attn_protocol::DesktopPane>) {
    world
        .daemon
        .emit(attn_protocol::ProfileArrangementChangedMessage {
            profile: attn_protocol::Profile {
                current_desktop_id: current.into(),
                ..super::terminal::profile()
            },
            desktops: vec![
                super::terminal::desktop(panes),
                attn_protocol::Desktop {
                    id: "d2".into(),
                    name: "d2".into(),
                    ..super::terminal::desktop(Vec::new())
                },
            ],
            ..attn_protocol::ProfileArrangementChangedMessage::new()
        });
    world.settle();
}

#[test]
fn a_comment_being_typed_is_kept_through_a_desktop_switch_and_back() {
    let mut world = started(remembering());
    typing_a_comment(&mut world, "Rows go after a minute");

    arranged(&mut world, "d2", vec![agent_pane()]);
    assert!(
        world.terminal_view("r1").is_none(),
        "the terminal left with its desktop"
    );
    arranged(&mut world, "d1", vec![agent_pane()]);
    world.settle();

    let state = annotations(&mut world);
    assert_eq!(state["marks"][0]["comment"], "Rows go after a minute");
    assert_eq!(state["marks"][0]["label"], "this-is-wrong");
    world.advance(Duration::from_millis(400));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_comment_being_typed_is_kept_when_the_session_moves_to_another_terminal() {
    let mut world = started(remembering());
    typing_a_comment(&mut world, "Rows go after a minute");

    let moved = attn_protocol::DesktopPane {
        session_id: "s1".into(),
        ..pane("t2", "r2")
    };
    arranged(&mut world, "d1", vec![moved]);
    world.settle();

    let moved_view = world.terminal_view("r2").expect("r2 is drawn");
    let marks = &moved_view["annotations"]["marks"];
    assert_eq!(marks[0]["comment"], "Rows go after a minute");
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_comment_being_typed_is_saved_once_typing_pauses_so_a_relaunch_reads_it() {
    let stored = Stored::default();
    let mut world = started(remembering_in(stored.clone()));
    typing_a_comment(&mut world, "Rows go after a minute");
    world.advance(Duration::from_millis(400));
    let last = saves(&world).pop().unwrap();
    assert_eq!(last["annotations"][0]["comment"], "Rows go after a minute");
    assert!(world.runtime_is_at_rest());
    drop(world);

    let mut relaunched = started(remembering_in(stored));
    let state = annotations(&mut relaunched);
    assert_eq!(state["marks"][0]["comment"], "Rows go after a minute");
    assert_eq!(state["popup"], Value::Null);
    assert!(relaunched.runtime_is_at_rest());
}

#[test]
fn escape_while_writing_puts_back_the_comment_the_mark_had() {
    let mut world = started(remembering());
    typing_a_comment(&mut world, "Rows go after a minute");
    world.gesture(|app| app.press("mod+enter"));

    alt_click(&mut world, (10, 0));
    world.gesture(|app| app.type_text(" and more"));
    assert_eq!(
        annotations(&mut world)["marks"][0]["comment"],
        "Rows go after a minute and more"
    );
    world.gesture(|app| app.press("escape"));

    let state = annotations(&mut world);
    assert_eq!(state["popup"], Value::Null);
    assert_eq!(state["marks"][0]["comment"], "Rows go after a minute");
    let last = saves(&world).pop().unwrap();
    assert_eq!(last["annotations"][0]["comment"], "Rows go after a minute");
    world.advance(Duration::from_millis(400));
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
    let state = annotations(&mut world);
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
    assert_eq!(
        annotations(&mut world)["outcome"],
        json!({"kind": "sending"})
    );

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
    let state = annotations(&mut world);
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
    let mark = annotations(&mut world)["marks"][0]["id"]
        .as_str()
        .unwrap()
        .to_owned();

    click(&mut world, &format!("terminal.r1.annotation.card.{mark}"));
    assert_eq!(annotations(&mut world)["popup"]["writing"], true);
    world.gesture(|app| app.press("Escape"));
    assert_eq!(annotations(&mut world)["popup"], Value::Null);
    assert_eq!(
        annotations(&mut world)["marks"].as_array().unwrap().len(),
        1
    );

    click(
        &mut world,
        &format!("terminal.r1.annotation.card.{mark}.remove"),
    );
    assert_eq!(annotations(&mut world)["marks"], json!([]));
    assert_eq!(saves(&world).last().unwrap()["annotations"], json!([]));
    assert!(world.runtime_is_at_rest());
}

fn panel_shows(world: &mut World) -> bool {
    world
        .screen()
        .find(Target::key("terminal.r1.annotation.panel"))
        .is_some()
}

#[test]
fn a_note_typed_just_before_clear_is_saved_to_its_session_and_the_new_session_shows_its_own() {
    let daemon = agent(&[("m1", MESSAGE)], "ready");
    daemon
        .on("session_messages_get", |r| {
            message(SessionMessagesGetResultMessage {
                session_id: r["session_id"].as_str().unwrap().into(),
                ..messages_result(&[("m1", MESSAGE)], "ready")
            })
        })
        .on("session_annotations_get", |r| {
            let session = r["session_id"].as_str().unwrap();
            message(SessionAnnotationsGetResultMessage {
                session_id: session.into(),
                success: true,
                note: Some(if session == "s2" { "their own" } else { "" }.into()),
                ..SessionAnnotationsGetResultMessage::new()
            })
        });
    let mut world = started(daemon);
    mark_on_cache(&mut world);
    click(&mut world, "terminal.r1.annotation.label.i-agree");
    click(&mut world, "terminal.r1.annotation.note.field");
    world.gesture(|app| app.type_text("Look again"));

    world
        .daemon
        .emit(attn_protocol::ProfileArrangementChangedMessage {
            profile: super::terminal::profile(),
            desktops: vec![super::terminal::desktop(vec![attn_protocol::DesktopPane {
                session_id: "s2".into(),
                ..agent_pane()
            }])],
            ..attn_protocol::ProfileArrangementChangedMessage::new()
        });
    world.settle();
    let last = saves(&world).last().unwrap().clone();
    assert_eq!(
        (&last["session_id"], &last["note"], &last["generation"]),
        (&json!("s1"), &json!("Look again"), &json!(2)),
        "the note goes to the session it was typed for"
    );
    let state = annotations(&mut world);
    assert_eq!(
        (&state["session"], &state["note"], &state["marks"]),
        (&json!("s2"), &json!("their own"), &json!([]))
    );
    world.advance(Duration::from_millis(400));
    assert_eq!(saves(&world).len(), 2, "nothing more is saved");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_send_while_one_is_unanswered_sends_nothing_and_a_note_rewritten_meanwhile_stays() {
    let daemon = agent(&[("m1", MESSAGE)], "ready");
    daemon.answer_in_turn("session_annotations_submit", [Answer::Hold]);
    let mut world = started(daemon);
    mark_on_cache(&mut world);
    click(&mut world, "terminal.r1.annotation.label.i-agree");
    click(&mut world, "terminal.r1.annotation.note.field");
    world.gesture(|app| app.type_text("First"));
    world.advance(Duration::from_millis(400));
    click(&mut world, "terminal.r1.annotation.send");
    click(&mut world, "terminal.r1.annotation.send");
    click(&mut world, "terminal.r1.annotation.note.field");
    world.gesture(|app| app.press("mod+enter"));
    assert_eq!(submitted(&world).len(), 1, "one send at a time");

    world.gesture(|app| app.type_text(", then more"));
    world.advance(Duration::from_millis(400));
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
    let state = annotations(&mut world);
    assert_eq!(
        (&state["marks"], &state["note"]),
        (&json!([]), &json!("First, then more"))
    );
    let last = saves(&world).last().unwrap().clone();
    assert_eq!(
        (&last["note"], &last["annotations"]),
        (&json!("First, then more"), &json!([]))
    );
    assert!(world.daemon.sent_of("session_annotations_clear").is_empty());
    world.advance(Duration::from_millis(2200));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_refused_send_keeps_the_marks_and_the_note_and_says_the_daemons_reason() {
    let daemon = agent(&[("m1", MESSAGE)], "ready");
    daemon.on("session_annotations_submit", |_| {
        message(SessionAnnotationsSubmitResultMessage {
            session_id: "s1".into(),
            success: false,
            error: Some("pty write failed".into()),
            ..SessionAnnotationsSubmitResultMessage::new()
        })
    });
    let mut world = started(daemon);
    mark_on_cache(&mut world);
    click(&mut world, "terminal.r1.annotation.label.i-agree");
    click(&mut world, "terminal.r1.annotation.note.field");
    world.gesture(|app| app.type_text("Keep me"));
    world.advance(Duration::from_millis(400));
    click(&mut world, "terminal.r1.annotation.send");
    let state = annotations(&mut world);
    assert_eq!(
        state["outcome"],
        json!({"kind": "error", "message": "pty write failed"})
    );
    assert_eq!(
        (state["marks"].as_array().unwrap().len(), &state["note"]),
        (1, &json!("Keep me"))
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_save_that_fails_for_a_reason_other_than_a_newer_draft_keeps_the_marks_without_reading_again() {
    let daemon = agent(&[("m1", MESSAGE)], "ready");
    daemon.on("session_annotations_save", |_| {
        message(SessionAnnotationsSaveResultMessage {
            session_id: "s1".into(),
            success: false,
            error: Some("disk full".into()),
            ..SessionAnnotationsSaveResultMessage::new()
        })
    });
    let mut world = started(daemon);
    mark_on_cache(&mut world);
    click(&mut world, "terminal.r1.annotation.label.i-agree");
    assert_eq!(
        annotations(&mut world)["marks"].as_array().unwrap().len(),
        1
    );
    assert_eq!(world.daemon.sent_of("session_annotations_get").len(), 1);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn with_nothing_marked_no_panel_shows_and_the_send_key_does_nothing() {
    let mut world = started(agent(&[("m1", MESSAGE)], "ready"));
    assert!(!panel_shows(&mut world));
    let terminal = cell(&world, 30, 4);
    world.gesture(|app| app.click_at(terminal));
    world.gesture(|app| app.press(SEND));
    assert!(submitted(&world).is_empty());
    assert!(super::terminal::inputs(&world).is_empty());
    assert!(
        world.screen().find(Target::key("notice.text")).is_none(),
        "the key is the send key, with nothing to send"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn only_this_sessions_changed_window_is_read_again_and_an_older_read_answering_last_is_dropped() {
    let daemon = agent(&[("m1", MESSAGE)], "ready");
    let mut world = started(daemon);
    let reads = |world: &World| world.daemon.sent_of("session_messages_get").len();
    let before = reads(&world);
    world.daemon.emit(SessionMessagesChangedMessage {
        session_id: "s2".into(),
        ..SessionMessagesChangedMessage::new()
    });
    world.settle();
    assert_eq!(
        reads(&world),
        before,
        "another session's window is not read"
    );

    world
        .daemon
        .answer_in_turn("session_messages_get", [Answer::Hold, Answer::Hold]);
    for _ in 0..2 {
        world.daemon.emit(SessionMessagesChangedMessage {
            session_id: "s1".into(),
            ..SessionMessagesChangedMessage::new()
        });
        world.settle();
    }
    let asks = world.daemon.sent_of("session_messages_get");
    let (older, newer) = (asks[asks.len() - 2].clone(), asks[asks.len() - 1].clone());
    world
        .daemon
        .reply_to(&newer, messages_result(&[("m2", MESSAGE)], "ready"));
    world.settle();
    world
        .daemon
        .reply_to(&older, messages_result(&[("m1", MESSAGE)], "ready"));
    world.settle();
    mark_on_cache(&mut world);
    assert_eq!(annotations(&mut world)["marks"][0]["message"], "m2");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_window_the_daemon_cannot_read_says_no_transcript_could_be_read() {
    let daemon = agent(&[], "ready");
    daemon.on("session_messages_get", |_| {
        message(SessionMessagesGetResultMessage {
            session_id: "s1".into(),
            success: false,
            error: Some("no transcript".into()),
            ..SessionMessagesGetResultMessage::new()
        })
    });
    let mut world = started(daemon);
    mark_on_cache(&mut world);
    assert_eq!(
        annotations(&mut world)["notice"],
        "No transcript could be read for this session, so there is nothing to annotate. The daemon log names the lookup that failed."
    );
    world.advance(Duration::from_secs(5));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_mark_whose_message_was_not_yet_drawn_draws_once_the_agent_prints_it_under_long_output() {
    let daemon = Daemon::scripted().state(showing(vec![agent_pane()], "daemon-1"));
    daemon
        .on("session_messages_get", |_| {
            message(messages_result(&[("m1", MESSAGE)], "ready"))
        })
        .on("session_annotations_get", |_| {
            draft_result(vec![stored_mark("i-agree")], 1)
        });
    let mut world = World::start(daemon);
    world.open_desktop();
    let lines: Vec<u8> = (0..300)
        .flat_map(|n| format!("build step {n}\r\n").into_bytes())
        .collect();
    output(&world, 1, &lines);
    world.settle();
    assert_eq!(violet(&mut world), [], "the message is not on the rows yet");

    output(&world, 2, b"more output\r\n");
    world.settle();
    output(&world, 3, format!("{MESSAGE}\r\n").as_bytes());
    world.settle();
    assert_eq!(
        violet(&mut world).len(),
        2,
        "the mark draws on the message once printed"
    );
    assert!(world.runtime_is_at_rest());
}
