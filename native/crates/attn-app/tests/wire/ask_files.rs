use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use attn_protocol::{AttachmentPutResult, AttachmentResult, AttachmentResultMessage};
use attn_testkit::{Request, Target, World, message};
use base64::Engine;
use rune::{ClipboardContent, ClipboardImage};
use serde_json::Value;

use super::asks::{api_working, ask, open_sheet, question, start, update_asks, web_waiting};

const NOTE: &str = "asks.note.field";

/// The bytes the daemon holds for each file, as the app uploaded them.
type Held = Arc<Mutex<HashMap<String, Vec<u8>>>>;

fn upload_answer(request: &Request, next_offset: usize) -> Value {
    message(AttachmentResultMessage {
        request_id: request["request_id"].as_str().unwrap().into(),
        profile_id: "work".into(),
        success: true,
        result: Some(AttachmentResult {
            upload: Some(AttachmentPutResult {
                next_offset: next_offset as i64,
                attachment: None,
            }),
            ..AttachmentResult::default()
        }),
        ..AttachmentResultMessage::new()
    })
}

fn hold(held: &Held, request: &Request) -> Value {
    let id = request["attachment_id"].as_str().unwrap().to_owned();
    let chunk = base64::engine::general_purpose::STANDARD
        .decode(request["data_base64"].as_str().unwrap())
        .unwrap();
    let mut held = held.lock().unwrap();
    let bytes = held.entry(id).or_default();
    bytes.extend(chunk);
    upload_answer(request, bytes.len())
}

fn discarded(request: &Request) -> Value {
    message(AttachmentResultMessage {
        request_id: request["request_id"].as_str().unwrap().into(),
        profile_id: "work".into(),
        success: true,
        result: Some(AttachmentResult {
            discarded: Some(true),
            ..AttachmentResult::default()
        }),
        ..AttachmentResultMessage::new()
    })
}

fn shipping() -> attn_protocol::Ask {
    ask(
        "a-ship",
        "web",
        "Ship it?",
        10,
        vec![question("q1", "one", "Ship today?", &["Yes", "No"])],
    )
}

/// A 4 by 3 pixel PNG.
fn png() -> Vec<u8> {
    const PNG: &str = "iVBORw0KGgoAAAANSUhEUgAAAAQAAAADCAYAAAC09K7GAAAAEklEQVR4nGP4z8DwHxkzEBQAAGHtF+mRbBMEAAAAAElFTkSuQmCC";
    base64::engine::general_purpose::STANDARD
        .decode(PNG)
        .unwrap()
}

/// The tray's files as (id, name, status), in order.
fn tray(world: &World) -> Vec<(String, String, String)> {
    world
        .screen()
        .descendants(Target::key("asks.files"))
        .into_iter()
        .filter_map(|n| {
            let id = n.key.as_deref()?.strip_prefix("asks.file.")?;
            (!id.contains('.')).then(|| {
                (
                    id.to_owned(),
                    n.name.clone().unwrap_or_default(),
                    n.value.clone().unwrap_or_default(),
                )
            })
        })
        .collect()
}

fn paste_into_note(world: &mut World, content: ClipboardContent) {
    world.gesture(|app| app.click(Target::key(NOTE)));
    world.gesture(|app| app.runtime.paste_content(content));
}

fn answers(world: &World) -> Vec<Request> {
    world.daemon.sent_of("ask_answer")
}

#[test]
fn pasted_and_dropped_files_ride_on_the_answer_but_a_removed_one_does_not() {
    let mut world = start(vec![shipping()], vec![api_working(), web_waiting()]);
    let held = Held::default();
    let h = held.clone();
    world.daemon.on("attachment_put", move |r| hold(&h, r));
    world.daemon.on("attachment_discard", discarded);
    let copied = world.data_dir().with_file_name("hit-rate.txt");
    std::fs::write(&copied, b"cache hit rate 92%\n").unwrap();
    let dropped = world.data_dir().with_file_name("report.pdf");
    let report: Vec<u8> = (0..700_000u32).map(|i| i as u8).collect();
    std::fs::write(&dropped, &report).unwrap();
    open_sheet(&mut world);

    paste_into_note(
        &mut world,
        ClipboardContent::new("").with_image(ClipboardImage::from_png(png()).unwrap()),
    );
    paste_into_note(
        &mut world,
        ClipboardContent::new("").with_files([copied.clone()]),
    );
    let [x, y, w, h] = world.rect("asks.sheet");
    let middle = ((x + w / 2.0) as f32, (y + h / 2.0) as f32);
    world.gesture(|app| app.runtime.drop_files(vec![dropped.clone()], Some(middle)));
    world.settle();

    let files = tray(&world);
    let names: Vec<&str> = files.iter().map(|(_, name, _)| name.as_str()).collect();
    assert_eq!(
        names,
        ["Pasted screenshot.png", "hit-rate.txt", "report.pdf"]
    );
    assert!(
        files.iter().all(|(_, _, status)| status == "ready"),
        "{files:?}"
    );
    let (shot, text, pdf) = (files[0].0.clone(), files[1].0.clone(), files[2].0.clone());
    assert_eq!(held.lock().unwrap()[&shot], png());
    assert_eq!(held.lock().unwrap()[&pdf], report);
    let puts = world.daemon.sent_of("attachment_put");
    assert!(
        puts.iter()
            .all(|p| p["owner"]["kind"] == "ask" && p["owner"]["id"] == "a-ship")
    );

    world.gesture(|app| app.click(Target::key(format!("asks.file.{text}.remove"))));
    world.settle();
    let discards = world.daemon.sent_of("attachment_discard");
    assert_eq!(discards.len(), 1);
    assert_eq!(discards[0]["attachment_id"], text);
    assert_eq!(tray(&world).len(), 2);
    world.gesture(|app| app.click(Target::key(NOTE)));
    let mut walk = Vec::new();
    for _ in 0..3 {
        world.gesture(|app| app.press("tab"));
        walk.push(
            world
                .screen()
                .focused()
                .and_then(|n| n.key)
                .unwrap_or_default(),
        );
    }
    assert_eq!(
        walk,
        [
            format!("asks.file.{shot}.remove"),
            format!("asks.file.{pdf}.remove"),
            "asks.attach".to_owned(),
        ]
    );

    world.gesture(|app| app.click(Target::key("asks.q.q1.choice.0")));
    world.gesture(|app| app.press("mod+enter"));
    world.settle();
    let sent = answers(&world);
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["attachments"], serde_json::json!([shot, pdf]));
}

#[test]
fn send_waits_for_a_file_still_uploading_then_sends_it_though_another_ask_shows() {
    let later = ask(
        "a-later",
        "api",
        "Later",
        1,
        vec![question("q1", "one", "Later?", &["Yes", "No"])],
    );
    let mut world = start(vec![shipping(), later], vec![api_working(), web_waiting()]);
    world.daemon.on("attachment_put", |_| Vec::<Value>::new());
    open_sheet(&mut world);
    paste_into_note(
        &mut world,
        ClipboardContent::new("").with_image(ClipboardImage::from_png(png()).unwrap()),
    );
    world.gesture(|app| app.click(Target::key("asks.q.q1.choice.1")));
    world.gesture(|app| app.press("mod+enter"));
    world.settle();

    assert!(
        answers(&world).is_empty(),
        "the answer left before its file"
    );
    let send = world.screen().get(Target::key("asks.send"));
    assert!(send.name.unwrap_or_default().starts_with("Sending…"));
    let (id, _, status) = tray(&world).remove(0);
    assert_eq!(status, "uploading");

    world.gesture(|app| app.click(Target::key("asks.row.a-later")));
    world.settle();
    let put = world.daemon.sent_of("attachment_put").remove(0);
    world.daemon.emit(upload_answer(&put, png().len()));
    world.settle();
    let sent = answers(&world);
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["ask_id"], "a-ship");
    assert_eq!(sent[0]["attachments"], serde_json::json!([id]));
    let shown = world
        .screen()
        .find(Target::key("asks.ask"))
        .and_then(|n| n.name);
    assert_eq!(
        shown.as_deref(),
        Some("Later"),
        "the sent answer did not pull the sheet back"
    );
}

#[test]
fn an_ask_closed_while_its_send_waits_on_a_file_leaves_send_working_for_the_next() {
    let later = ask(
        "a-later",
        "api",
        "Later",
        1,
        vec![question("q1", "one", "Later?", &["Yes", "No"])],
    );
    let mut world = start(
        vec![shipping(), later.clone()],
        vec![api_working(), web_waiting()],
    );
    world.daemon.on("attachment_put", |_| Vec::<Value>::new());
    world.daemon.on("attachment_discard", discarded);
    open_sheet(&mut world);
    paste_into_note(
        &mut world,
        ClipboardContent::new("").with_image(ClipboardImage::from_png(png()).unwrap()),
    );
    world.gesture(|app| app.click(Target::key("asks.q.q1.choice.1")));
    world.gesture(|app| app.press("mod+enter"));
    world.settle();

    update_asks(&mut world, vec![later]);
    let put = world.daemon.sent_of("attachment_put").remove(0);
    world.daemon.emit(upload_answer(&put, png().len()));
    world.settle();
    assert!(
        answers(&world).is_empty(),
        "a closed ask's waiting send went out"
    );

    world.gesture(|app| app.click(Target::key("asks.q.q1.choice.0")));
    world.gesture(|app| app.press("mod+enter"));
    world.settle();
    let sent = answers(&world);
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["ask_id"], "a-later");
}

/// The files in a tray, as (id, name, status), in order.
fn tray_in(world: &World, key: &str) -> Vec<(String, String, String)> {
    world
        .screen()
        .descendants(Target::key(key))
        .into_iter()
        .filter_map(|n| {
            let id = n.key.as_deref()?.strip_prefix("asks.file.")?;
            (!id.contains('.')).then(|| {
                (
                    id.to_owned(),
                    n.name.clone().unwrap_or_default(),
                    n.value.clone().unwrap_or_default(),
                )
            })
        })
        .collect()
}

fn middle_of(world: &mut World, key: &str) -> (f32, f32) {
    let [x, y, w, h] = world.rect(key);
    ((x + w / 2.0) as f32, (y + h / 2.0) as f32)
}

#[test]
fn a_file_question_takes_what_is_pasted_or_dropped_while_it_is_active_and_sends_it() {
    let screenshot = ask(
        "a-shot",
        "web",
        "Banner",
        10,
        vec![
            question("q1", "one", "Ship the banner?", &["Yes", "No"]),
            question("q2", "file", "If it looks wrong, attach a screenshot", &[]),
        ],
    );
    let mut world = start(vec![screenshot], vec![api_working(), web_waiting()]);
    let held = Held::default();
    let h = held.clone();
    world.daemon.on("attachment_put", move |r| hold(&h, r));
    let console = world.data_dir().with_file_name("console.txt");
    std::fs::write(&console, b"TypeError at banner.js:12\n").unwrap();
    let notes = world.data_dir().with_file_name("notes.txt");
    std::fs::write(&notes, b"looked fine on the Mac\n").unwrap();
    let elsewhere = world.data_dir().with_file_name("elsewhere.txt");
    std::fs::write(&elsewhere, b"dropped beside the zone\n").unwrap();
    open_sheet(&mut world);
    assert!(
        world
            .screen()
            .find(Target::key("asks.q.q2.choice.0"))
            .is_none(),
        "the file question shows choices"
    );

    world.gesture(|app| app.click(Target::key("asks.q.q1.choice.0")));
    world.gesture(|app| app.press("tab"));
    let focused = world.screen().focused().and_then(|n| n.key);
    assert_eq!(focused.as_deref(), Some("asks.q.q2.drop"));
    world.gesture(|app| {
        app.runtime.paste_content(
            ClipboardContent::new("").with_image(ClipboardImage::from_png(png()).unwrap()),
        )
    });
    let zone = middle_of(&mut world, "asks.q.q2.files");
    world.gesture(|app| app.runtime.drop_files(vec![console.clone()], Some(zone)));
    let beside = middle_of(&mut world, "asks.q.q1.prompt");
    world.gesture(|app| {
        app.runtime
            .drop_files(vec![elsewhere.clone()], Some(beside))
    });
    world.settle();

    let on_question = tray_in(&world, "asks.q.q2.files");
    let names: Vec<&str> = on_question.iter().map(|(_, n, _)| n.as_str()).collect();
    assert_eq!(
        names,
        ["Pasted screenshot.png", "console.txt", "elsewhere.txt"]
    );
    assert!(tray_in(&world, "asks.files").is_empty());

    world.gesture(|app| app.click(Target::key(NOTE)));
    world.gesture(|app| app.runtime.drop_files(vec![notes.clone()], Some(beside)));
    world.settle();
    let on_note = tray_in(&world, "asks.files");
    assert_eq!(on_note.len(), 1);
    assert_eq!(on_note[0].1, "notes.txt");
    assert_eq!(tray_in(&world, "asks.q.q2.files").len(), 3);

    world.gesture(|app| app.press("mod+enter"));
    world.settle();
    let sent = answers(&world);
    assert_eq!(sent.len(), 1);
    let ids: Vec<&str> = on_question.iter().map(|(id, _, _)| id.as_str()).collect();
    assert_eq!(
        sent[0]["answers"],
        serde_json::json!([
            {"key": "q1", "picked": ["Yes"]},
            {"key": "q2", "picked": [], "attachments": ids},
        ])
    );
    assert_eq!(sent[0]["attachments"], serde_json::json!([on_note[0].0]));
    assert_eq!(held.lock().unwrap()[&on_question[0].0], png());
}

#[test]
fn a_question_of_a_kind_the_app_does_not_know_is_not_shown_as_a_choice_question() {
    let ranked = ask(
        "a-rank",
        "web",
        "Rank",
        10,
        vec![question("q1", "rank", "Rank these", &["A", "B"])],
    );
    let mut world = start(vec![ranked], vec![api_working(), web_waiting()]);
    open_sheet(&mut world);
    let screen = world.screen();
    assert!(screen.find(Target::key("asks.q.q1.choice.0")).is_none());
    assert!(screen.find(Target::key("asks.q.q1.unknown")).is_some());
    world.gesture(|app| app.press("1"));
    assert!(answers(&world).is_empty());
}

fn three_kinds() -> attn_protocol::Ask {
    ask(
        "a-three",
        "web",
        "Banner",
        10,
        vec![
            question("q1", "one", "Ship the banner?", &["Yes", "No"]),
            question("q2", "file", "If it looks wrong, attach a screenshot", &[]),
            question("q3", "text", "What did you see?", &[]),
        ],
    )
}

/// ⌘V on macOS, Ctrl+V on Linux, as a window reports it: by its code, with the key it types.
fn press_paste(world: &mut World, content: ClipboardContent) {
    world.clipboard.set_content(content);
    let typed = attn_testkit::key_event(&rune::Chord::parse("mod+v").unwrap());
    world.gesture(|app| {
        for pressed in [true, false] {
            app.runtime.physical_key_input(
                rune::PhysicalInput::Key {
                    key: rune::PhysicalKeyCode::KeyV,
                    mods: typed.mods,
                    pressed,
                    repeat: false,
                },
                typed.clone(),
                Some('v'),
            );
        }
    });
    world.settle();
}

fn focused_key(world: &World) -> Option<String> {
    world.screen().focused().and_then(|n| n.key)
}

fn field_text(world: &World, key: &str) -> String {
    world
        .screen()
        .find(Target::key(key))
        .and_then(|n| n.value)
        .unwrap_or_default()
}

#[test]
fn mod_v_attaches_a_pasted_image_or_file_wherever_the_keyboard_is_in_the_sheet() {
    let screenshot =
        || ClipboardContent::new("").with_image(ClipboardImage::from_png(png()).unwrap());
    let landing: [(&str, &[&str], &str); 8] = [
        (
            "the sheet, after a press on the file question's text",
            &["asks.q.q2.prompt"],
            "asks.q.q2.files",
        ),
        (
            "the sheet, after a press on another question's text",
            &["asks.q.q1.prompt"],
            "asks.files",
        ),
        ("a choice", &["asks.q.q1.choice.0"], "asks.files"),
        ("a button", &["asks.copy"], "asks.files"),
        (
            "the file question's Attach file button",
            &["asks.q.q2.drop"],
            "asks.q.q2.files",
        ),
        ("the Other field", &["asks.q.q1.other.field"], "asks.files"),
        (
            "the written answer",
            &["asks.q.q3.text.field"],
            "asks.files",
        ),
        (
            "the condition field",
            &["asks.q.q1.choice.0", "asks.q.q1.condition"],
            "asks.files",
        ),
    ];
    for (place, presses, tray) in landing {
        let mut world = start(vec![three_kinds()], vec![api_working(), web_waiting()]);
        let held = Held::default();
        let h = held.clone();
        world.daemon.on("attachment_put", move |r| hold(&h, r));
        open_sheet(&mut world);
        for &key in presses.iter() {
            world.gesture(|app| app.click(Target::key(key)));
        }
        if place == "the condition field" {
            world.gesture(|app| app.click(Target::key("asks.q.q1.condition.field")));
        }
        let focused = world.screen().focused().and_then(|n| n.key);
        press_paste(&mut world, screenshot());
        let names: Vec<String> = tray_in(&world, tray)
            .into_iter()
            .map(|(_, n, _)| n)
            .collect();
        assert_eq!(
            names,
            ["Pasted screenshot.png"],
            "a screenshot pasted with focus on {place} ({focused:?}) went elsewhere"
        );
        let file = world.data_dir().with_file_name("console.txt");
        std::fs::write(&file, b"TypeError\n").unwrap();
        press_paste(&mut world, ClipboardContent::new("").with_files([file]));
        let names: Vec<String> = tray_in(&world, tray)
            .into_iter()
            .map(|(_, n, _)| n)
            .collect();
        assert_eq!(
            names,
            ["Pasted screenshot.png", "console.txt"],
            "a file copied with focus on {place} went elsewhere"
        );
    }
}

#[test]
fn mod_v_of_text_and_an_image_into_a_field_of_the_sheet_types_the_text() {
    for (field, presses) in [
        ("asks.q.q1.other.field", &["asks.q.q1.other.field"][..]),
        ("asks.q.q3.text.field", &["asks.q.q3.text.field"][..]),
        (
            "asks.q.q1.condition.field",
            &[
                "asks.q.q1.choice.0",
                "asks.q.q1.condition",
                "asks.q.q1.condition.field",
            ][..],
        ),
        (NOTE, &["asks.q.q3.text.field"][..]),
    ] {
        let mut world = start(vec![three_kinds()], vec![api_working(), web_waiting()]);
        open_sheet(&mut world);
        for &key in presses.iter() {
            world.gesture(|app| app.click(Target::key(key)));
        }
        while field == NOTE && focused_key(&world).as_deref() != Some(NOTE) {
            world.gesture(|app| app.press("tab"));
        }
        assert_eq!(focused_key(&world).as_deref(), Some(field));
        press_paste(&mut world, ClipboardContent::new("only on Safari"));
        assert_eq!(
            field_text(&world, field),
            "only on Safari",
            "{field} did not take the text"
        );
        assert!(tray_in(&world, "asks.files").is_empty());
        assert!(tray_in(&world, "asks.q.q2.files").is_empty());
    }
}

#[test]
fn mod_v_of_text_and_an_image_with_no_field_focused_attaches_the_image() {
    let mut world = start(vec![three_kinds()], vec![api_working(), web_waiting()]);
    let held = Held::default();
    let h = held.clone();
    world.daemon.on("attachment_put", move |r| hold(&h, r));
    open_sheet(&mut world);
    world.gesture(|app| app.click(Target::key("asks.q.q1.choice.0")));
    let cell = ClipboardContent::new("only on Safari")
        .with_image(ClipboardImage::from_png(png()).unwrap());
    press_paste(&mut world, cell);
    let names: Vec<String> = tray_in(&world, "asks.files")
        .into_iter()
        .map(|(_, n, _)| n)
        .collect();
    assert_eq!(names, ["Pasted screenshot.png"]);
}
