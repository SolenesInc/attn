use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_protocol::{
    Ask, AskDraft, AskDraftFile, AskDraftSaveResultMessage, Attachment, AttachmentPutResult,
    AttachmentResult, AttachmentResultMessage,
};
use attn_testkit::{Request, Target, World, message};
use base64::Engine;
use rune::{ClipboardContent, ClipboardImage};
use serde_json::Value;

use super::asks::{
    api_working, ask, checked, field_text, open_sheet, question, start, update_asks, web_waiting,
};

const NOTE: &str = "asks.note.field";

/// The draft the daemon holds, as the app last saved it.
type Kept = Arc<Mutex<Option<AskDraft>>>;

fn keeping(world: &World, kept: &Kept) {
    let kept = kept.clone();
    world.daemon.on("ask_draft_save", move |r| {
        let draft: AskDraft = serde_json::from_value(r["draft"].clone()).unwrap();
        let generation = draft.generation;
        *kept.lock().unwrap() = Some(draft);
        message(AskDraftSaveResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            ask_id: r["ask_id"].as_str().unwrap().into(),
            success: true,
            generation,
            ..AskDraftSaveResultMessage::new()
        })
    });
    world.daemon.on("attachment_put", |r: &Request| {
        let bytes = base64::engine::general_purpose::STANDARD
            .decode(r["data_base64"].as_str().unwrap())
            .unwrap();
        message(AttachmentResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            profile_id: "work".into(),
            success: true,
            result: Some(AttachmentResult {
                upload: Some(AttachmentPutResult {
                    next_offset: r["offset"].as_i64().unwrap() + bytes.len() as i64,
                    attachment: None,
                }),
                ..AttachmentResult::default()
            }),
            ..AttachmentResultMessage::new()
        })
    });
}

fn launch() -> Ask {
    ask(
        "a-launch",
        "web",
        "Launch plan",
        10,
        vec![
            question("q1", "text", "What should the banner say?", &[]),
            question("q2", "one", "Which region first?", &["eu", "us"]),
            question("q3", "any", "Which checks?", &["smoke", "load"]),
        ],
    )
}

fn png() -> Vec<u8> {
    const PNG: &str = "iVBORw0KGgoAAAANSUhEUgAAAAQAAAADCAYAAAC09K7GAAAAEklEQVR4nGP4z8DwHxkzEBQAAGHtF+mRbBMEAAAAAElFTkSuQmCC";
    base64::engine::general_purpose::STANDARD
        .decode(PNG)
        .unwrap()
}

fn tray(world: &World) -> Vec<(String, String)> {
    world
        .screen()
        .descendants(Target::key("asks.files"))
        .into_iter()
        .filter_map(|n| {
            let id = n.key.as_deref()?.strip_prefix("asks.file.")?;
            (!id.contains('.')).then(|| {
                (
                    n.name.clone().unwrap_or_default(),
                    n.value.clone().unwrap_or_default(),
                )
            })
        })
        .collect()
}

fn type_into(world: &mut World, key: &str, text: &str) {
    world.gesture(|app| app.click(Target::key(key)));
    world.gesture(|app| app.type_text(text));
}

fn saves(world: &World) -> Vec<Request> {
    world.daemon.sent_of("ask_draft_save")
}

/// An app started on the daemon's asks, in a window tall enough to show every field of the sheet.
fn launched(asks: Vec<Ask>) -> World {
    let mut world = start(asks, vec![api_working(), web_waiting()]);
    world.resize((1400.0, 1100.0));
    world
}

#[test]
fn a_half_written_answer_is_saved_once_writing_pauses_and_comes_back_after_a_relaunch() {
    let mut world = launched(vec![launch()]);
    let kept = Kept::default();
    keeping(&world, &kept);
    open_sheet(&mut world);
    type_into(&mut world, "asks.q.q1.text.field", "Back by noon");
    world.gesture(|app| app.click(Target::key("asks.q.q2.choice.1")));
    world.gesture(|app| app.click(Target::key("asks.q.q2.condition")));
    world.gesture(|app| app.type_text("after the eu freeze"));
    world.gesture(|app| app.click(Target::key("asks.q.q3.choice.0")));
    type_into(&mut world, "asks.q.q3.other.field", "soak");
    type_into(&mut world, NOTE, "Ask me before Friday");
    world.gesture(|app| {
        app.runtime.paste_content(
            ClipboardContent::new("").with_image(ClipboardImage::from_png(png()).unwrap()),
        )
    });
    world.settle();
    assert!(
        saves(&world).is_empty(),
        "a save went out while writing went on"
    );

    world.advance(Duration::from_millis(400));
    assert_eq!(saves(&world).len(), 1, "one pause saves once");
    let draft = kept.lock().unwrap().clone().expect("the draft was saved");
    assert_eq!(draft.generation, 1);
    let answers = serde_json::to_value(&draft.answers).unwrap();
    assert_eq!(
        answers,
        serde_json::json!([
            {"key": "q1", "picked": [], "text": "Back by noon"},
            {"key": "q2", "picked": ["us"], "condition": "after the eu freeze"},
            {"key": "q3", "picked": ["smoke"], "other": "soak"},
        ])
    );
    assert_eq!(draft.note.as_deref(), Some("Ask me before Friday"));
    let file = draft.attachments.clone().unwrap().remove(0);
    world.advance(Duration::from_secs(2));
    assert_eq!(saves(&world).len(), 1, "an unchanged draft was saved again");
    drop(world);

    let mut held = launch();
    held.draft = Some(draft);
    held.draft_files = Some(vec![AskDraftFile {
        attachment: Attachment {
            id: file,
            name: "Pasted screenshot.png".into(),
            media_type: "image/png".into(),
            bytes: png().len() as i64,
        },
        ready: true,
    }]);
    let mut world = launched(vec![held]);
    open_sheet(&mut world);
    world.settle();
    assert_eq!(field_text(&world, "asks.q.q1.text.field"), "Back by noon");
    assert!(checked(&world, "asks.q.q2.choice.1"));
    assert_eq!(
        field_text(&world, "asks.q.q2.condition.field"),
        "after the eu freeze"
    );
    assert!(checked(&world, "asks.q.q3.choice.0"));
    assert_eq!(field_text(&world, "asks.q.q3.other.field"), "soak");
    assert_eq!(field_text(&world, NOTE), "Ask me before Friday");
    assert_eq!(
        tray(&world),
        [("Pasted screenshot.png".to_owned(), "ready".to_owned())]
    );
    world.advance(Duration::from_secs(2));
    assert!(
        saves(&world).is_empty(),
        "a draft brought back was saved again unchanged"
    );
}

#[test]
fn a_file_still_uploading_when_the_app_quit_comes_back_failed_and_holds_the_send() {
    let mut held = launch();
    held.draft = Some(AskDraft {
        generation: 4,
        answers: vec![],
        note: Some("See the shot".into()),
        attachments: Some(vec!["f-half".into()]),
    });
    held.draft_files = Some(vec![AskDraftFile {
        attachment: Attachment {
            id: "f-half".into(),
            name: "half.png".into(),
            ..Attachment::default()
        },
        ready: false,
    }]);
    let mut world = launched(vec![held]);
    let kept = Kept::default();
    keeping(&world, &kept);
    open_sheet(&mut world);
    world.settle();
    assert_eq!(tray(&world), [("half.png".to_owned(), "failed".to_owned())]);
    world.gesture(|app| app.press("mod+enter"));
    world.settle();
    assert!(
        world.daemon.sent_of("ask_answer").is_empty(),
        "an answer went with a file that never finished uploading"
    );

    world.gesture(|app| app.click(Target::key("asks.file.f-half.remove")));
    world.advance(Duration::from_millis(400));
    let draft = kept.lock().unwrap().clone().expect("the draft was saved");
    assert_eq!(
        draft.generation, 5,
        "the save goes past the generation brought back"
    );
    assert_eq!(draft.attachments, None);
}

#[test]
fn an_answered_or_closed_ask_saves_no_draft() {
    let other = ask(
        "a-other",
        "api",
        "Other",
        5,
        vec![question("q1", "one", "Other?", &["Yes", "No"])],
    );
    let mut world = start(
        vec![launch(), other.clone()],
        vec![api_working(), web_waiting()],
    );
    let kept = Kept::default();
    keeping(&world, &kept);
    open_sheet(&mut world);
    world.gesture(|app| app.click(Target::key("asks.row.a-launch")));
    world.gesture(|app| app.click(Target::key("asks.q.q2.choice.0")));
    world.gesture(|app| app.press("mod+enter"));
    world.settle();
    assert_eq!(world.daemon.sent_of("ask_answer").len(), 1);
    let mut answered = launch();
    answered.reply = Some(attn_protocol::AskReply::default());
    update_asks(&mut world, vec![answered, other.clone()]);

    world.gesture(|app| app.click(Target::key("asks.row.a-other")));
    type_into(&mut world, NOTE, "Not yet");
    update_asks(&mut world, vec![]);
    world.advance(Duration::from_millis(400));
    assert!(
        saves(&world).is_empty(),
        "saved {:?} for asks already answered or closed",
        saves(&world)
            .iter()
            .map(|s| s["ask_id"].clone())
            .collect::<Vec<Value>>()
    );
}

#[test]
fn a_save_the_daemon_never_answered_is_sent_again_once_it_is_back() {
    let mut world = launched(vec![launch()]);
    world.daemon.on("ask_draft_save", |_| Vec::<Value>::new());
    open_sheet(&mut world);
    type_into(&mut world, NOTE, "Ask me before Friday");
    world.advance(Duration::from_millis(400));
    assert_eq!(saves(&world).len(), 1);

    let kept = Kept::default();
    keeping(&world, &kept);
    world.reconnect();
    world.advance(Duration::from_millis(400));
    let draft = kept
        .lock()
        .unwrap()
        .clone()
        .expect("the draft was saved again");
    assert_eq!(draft.note.as_deref(), Some("Ask me before Friday"));
    assert_eq!(
        saves(&world).len(),
        2,
        "the unanswered save went again once, and only once"
    );
}
