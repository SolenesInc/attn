use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_app::Action;
use attn_protocol::{
    CrewMember, CrewUpdatedMessage, InitialStateMessage, Profile, ProfileArrangementChangedMessage,
    QuickCaptureAttachment, QuickCaptureAttachmentPutResult, QuickCaptureDraftAsset,
    QuickCaptureListResult, QuickCaptureReadMessage, QuickCaptureRecord, QuickCaptureResult,
    QuickCaptureResultMessage, QuickCaptureUnreadMessage,
};
use attn_testkit::{Answer, Daemon, Request, Target, World, message};
use base64::Engine;
use rune::{ClipboardContent, ClipboardImage};
use serde_json::{Value, json};

const PROFILE: &str = "profile-0b7f2a3c-1111-4222-8333-444455556666";
const OTHER: &str = "profile-9c8d7e6f-5555-4666-8777-888899990000";

fn member(id: &str, name: &str, profile: &str, chief: bool) -> CrewMember {
    CrewMember {
        id: id.into(),
        name: name.into(),
        profile_id: profile.into(),
        chief: chief.then_some(true),
        ..CrewMember::default()
    }
}

fn crew() -> Vec<CrewMember> {
    vec![
        member("fern", "Fern", PROFILE, false),
        member("ada", "Ada", PROFILE, true),
        member("otto", "Otto", OTHER, true),
    ]
}

fn state(selected: &str) -> InitialStateMessage {
    InitialStateMessage {
        selected_profile_id: Some(selected.into()),
        profiles: Some(vec![
            Profile {
                id: PROFILE.into(),
                name: "Work".into(),
                ..Default::default()
            },
            Profile {
                id: OTHER.into(),
                name: "Home".into(),
                ..Default::default()
            },
        ]),
        crew: Some(crew()),
        ..InitialStateMessage::new()
    }
}

/// The daemon's captures: what it kept, and the files it holds for drafts.
#[derive(Default)]
struct Captures {
    records: Vec<QuickCaptureRecord>,
    assets: Vec<QuickCaptureDraftAsset>,
    bytes: HashMap<String, Vec<u8>>,
}

type Kept = Arc<Mutex<Captures>>;

fn answer(result: QuickCaptureResult) -> Value {
    message(QuickCaptureResultMessage {
        profile_id: PROFILE.into(),
        success: true,
        result: Some(result),
        ..QuickCaptureResultMessage::new()
    })
}

fn refusal(code: &str, error: &str) -> Value {
    message(QuickCaptureResultMessage {
        profile_id: PROFILE.into(),
        success: false,
        error: Some(error.into()),
        error_code: Some(code.into()),
        ..QuickCaptureResultMessage::new()
    })
}

fn kept_send(kept: &Kept, request: &Request) -> Value {
    let mut kept = kept.lock().unwrap();
    let ids: Vec<String> = request["attachment_ids"]
        .as_array()
        .unwrap()
        .iter()
        .map(|i| i.as_str().unwrap().to_owned())
        .collect();
    let attachments = ids
        .iter()
        .map(|id| {
            let asset = kept.assets.iter().find(|a| &a.attachment_id == id).unwrap();
            QuickCaptureAttachment {
                id: id.clone(),
                name: asset.name.clone(),
                media_type: "image/png".into(),
                bytes: kept.bytes[id].len() as i64,
            }
        })
        .collect();
    let record = QuickCaptureRecord {
        id: request["capture_id"].as_str().unwrap().into(),
        mailbox: serde_json::from_value(request["mailbox"].clone()).unwrap(),
        content: request["content"].as_str().unwrap().into(),
        created_at: format!("2026-11-12T09:{:02}:00Z", kept.records.len()),
        attachments,
        read_at: None,
    };
    kept.records.push(record.clone());
    answer(QuickCaptureResult {
        record: Some(record),
        ..QuickCaptureResult::default()
    })
}

fn kept_get(kept: &Kept, request: &Request) -> Value {
    let kept = kept.lock().unwrap();
    match kept.records.iter().find(|r| r.id == request["capture_id"]) {
        Some(record) => answer(QuickCaptureResult {
            record: Some(record.clone()),
            ..QuickCaptureResult::default()
        }),
        None => refusal("quick_capture_not_found", "quick capture not found"),
    }
}

/// Pages the captures newest first; a cursor is the index the page starts at.
fn kept_list(kept: &Kept, request: &Request) -> Value {
    let kept = kept.lock().unwrap();
    let limit = request["limit"].as_u64().unwrap() as usize;
    let start: usize = request["cursor"].as_str().map_or(0, |c| c.parse().unwrap());
    let newest: Vec<_> = kept.records.iter().rev().cloned().collect();
    let end = (start + limit).min(newest.len());
    answer(QuickCaptureResult {
        list: Some(QuickCaptureListResult {
            items: newest[start..end].to_vec(),
            draft_assets: kept.assets.clone(),
            next_cursor: (end < newest.len()).then(|| end.to_string()),
        }),
        ..QuickCaptureResult::default()
    })
}

fn kept_put(kept: &Kept, request: &Request) -> Value {
    let mut kept = kept.lock().unwrap();
    let id = request["attachment_id"].as_str().unwrap().to_owned();
    let data = base64::engine::general_purpose::STANDARD
        .decode(request["data_base64"].as_str().unwrap())
        .unwrap();
    let bytes = kept.bytes.entry(id.clone()).or_default();
    assert_eq!(bytes.len() as u64, request["offset"].as_u64().unwrap());
    bytes.extend(data);
    let next = bytes.len() as i64;
    let state = if request["final"] == true {
        "ready"
    } else {
        "staged"
    };
    kept.assets.retain(|a| a.attachment_id != id);
    kept.assets.push(QuickCaptureDraftAsset {
        capture_id: request["capture_id"].as_str().unwrap().into(),
        attachment_id: id,
        name: request["name"].as_str().unwrap().into(),
        state: state.into(),
        next_offset: next,
    });
    answer(QuickCaptureResult {
        upload: Some(QuickCaptureAttachmentPutResult {
            next_offset: next,
            attachment: None,
        }),
        ..QuickCaptureResult::default()
    })
}

fn kept_discard(kept: &Kept, request: &Request) -> Value {
    let mut kept = kept.lock().unwrap();
    kept.assets
        .retain(|a| a.attachment_id != request["attachment_id"]);
    answer(QuickCaptureResult {
        discarded: Some(true),
        ..QuickCaptureResult::default()
    })
}

/// A daemon whose profile has a chief, Ada, and a member, Fern, and that keeps captures.
fn home() -> (Daemon, Kept) {
    let kept = Kept::default();
    let daemon = Daemon::scripted().state(state(PROFILE));
    let k = kept.clone();
    daemon.on("quick_capture_send", move |r| kept_send(&k, r));
    let k = kept.clone();
    daemon.on("quick_capture_get", move |r| kept_get(&k, r));
    let k = kept.clone();
    daemon.on("quick_capture_list", move |r| kept_list(&k, r));
    let k = kept.clone();
    daemon.on("quick_capture_attachment_put", move |r| kept_put(&k, r));
    let k = kept.clone();
    daemon.on("quick_capture_attachment_discard", move |r| {
        kept_discard(&k, r)
    });
    (daemon, kept)
}

fn open(world: &mut World) {
    world.gesture(|d| {
        d.update(|state, tx| {
            attn_app::dispatch(state, Action::OpenQuickCapture, tx);
        });
    });
}

fn press(world: &mut World, chord: &str) {
    world.capture_gesture(|d| d.press(chord));
}

fn type_text(world: &mut World, text: &str) {
    world.capture_gesture(|d| d.type_text(text));
}

fn message_text(world: &World) -> String {
    world
        .capture_screen()
        .get(Target::key("capture.message"))
        .value
        .unwrap_or_default()
}

fn to(world: &World) -> String {
    world
        .capture_screen()
        .get(Target::key("capture.to"))
        .name
        .unwrap_or_default()
}

fn shows(world: &World, text: &str) -> bool {
    world.capture_screen().to_string().contains(text)
}

fn alert(world: &World) -> Option<String> {
    world
        .capture_screen()
        .find(Target::role("alert"))
        .and_then(|n| n.name)
}

fn draft_on_disk(world: &World, profile: &str) -> Option<Value> {
    let path = world
        .data_dir()
        .join("quick-capture-drafts")
        .join(profile)
        .join("quick-capture-draft.json");
    std::fs::read(path)
        .ok()
        .map(|b| serde_json::from_slice(&b).unwrap())
}

fn rest(world: &mut World) {
    world.advance(Duration::from_secs(6));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn opens_over_the_app_with_the_message_field_and_escape_hides_it_keeping_the_draft() {
    let (daemon, _) = home();
    let mut world = World::start(daemon);
    assert!(!world.capture_shown());

    open(&mut world);
    assert!(world.capture_shown());
    type_text(&mut world, "Check the drive notes.");
    assert_eq!(message_text(&world), "Check the drive notes.");
    assert_eq!(to(&world), "Send to");
    assert!(shows(&world, "To: Ada"));

    press(&mut world, "escape");
    assert!(!world.capture_shown());
    assert_eq!(
        draft_on_disk(&world, PROFILE).unwrap()["text"],
        "Check the drive notes."
    );

    open(&mut world);
    assert!(world.capture_shown());
    assert_eq!(message_text(&world), "Check the drive notes.");
    rest(&mut world);
}

#[test]
fn sends_once_to_the_member_picked_and_clears_only_after_the_daemon_kept_it() {
    let (daemon, kept) = home();
    let mut world = World::start(daemon);
    open(&mut world);
    type_text(&mut world, "Check the drive notes.");
    press(&mut world, "mod+2");
    assert!(shows(&world, "To: Fern"));

    press(&mut world, "enter");
    let sent = world.daemon.sent_of("quick_capture_send");
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["content"], "Check the drive notes.");
    assert_eq!(sent[0]["profile_id"], PROFILE);
    assert_eq!(
        sent[0]["mailbox"],
        json!({"kind": "crew_member", "member_id": "fern"})
    );
    assert_eq!(kept.lock().unwrap().records.len(), 1);
    assert!(!world.capture_shown(), "a send hides the panel");

    open(&mut world);
    assert_eq!(message_text(&world), "");
    assert!(
        shows(&world, "To: Ada"),
        "a send resets the mailbox to the chief"
    );
    assert!(shows(&world, "Saved for Fern"));
    let fresh = draft_on_disk(&world, PROFILE).unwrap();
    assert_ne!(fresh["id"], sent[0]["capture_id"]);
    assert_eq!(fresh["text"], "");
    rest(&mut world);
}

#[test]
fn shift_enter_breaks_the_line_and_an_empty_draft_does_not_send() {
    let (daemon, _) = home();
    let mut world = World::start(daemon);
    open(&mut world);
    press(&mut world, "enter");
    assert!(world.daemon.sent_of("quick_capture_send").is_empty());
    type_text(&mut world, "one");
    press(&mut world, "shift+enter");
    type_text(&mut world, "two");
    assert_eq!(message_text(&world), "one\ntwo");
    assert!(world.daemon.sent_of("quick_capture_send").is_empty());
    rest(&mut world);
}

#[test]
fn the_mailbox_list_opens_with_cmd_k_and_picks_with_the_arrows() {
    let (daemon, _) = home();
    let mut world = World::start(daemon);
    open(&mut world);
    press(&mut world, "mod+k");
    let options: Vec<_> = world
        .capture_screen()
        .all(Target::role("list-box-option"))
        .into_iter()
        .map(|o| o.name.unwrap_or_default())
        .collect();
    assert_eq!(
        options,
        ["Ada", "Fern"],
        "the chief first, then the profile's members"
    );
    press(&mut world, "down");
    press(&mut world, "enter");
    assert!(
        world
            .capture_screen()
            .find(Target::role("list-box"))
            .is_none()
    );
    assert!(shows(&world, "To: Fern"));

    press(&mut world, "mod+k");
    press(&mut world, "escape");
    assert!(
        world.capture_shown(),
        "Escape closes the list before the panel"
    );
    assert!(
        world
            .capture_screen()
            .find(Target::role("list-box"))
            .is_none()
    );
    rest(&mut world);
}

#[test]
fn a_disconnected_send_stays_editable_without_marking_it_uncertain() {
    let (daemon, _) = home();
    let mut world = World::start(daemon);
    open(&mut world);
    type_text(&mut world, "offline");
    world.daemon.refuse_connections(true);
    world.daemon.disconnect();
    world.settle();
    press(&mut world, "enter");
    assert_eq!(
        alert(&world).as_deref(),
        Some("Quick Capture is not connected. Your draft is retained.")
    );
    assert!(world.daemon.sent_of("quick_capture_send").is_empty());
    type_text(&mut world, " still");
    assert_eq!(message_text(&world), "offline still");
    press(&mut world, "escape");
    assert_eq!(draft_on_disk(&world, PROFILE).unwrap()["uncertain"], false);
}

#[test]
fn saves_after_one_idle_second_and_within_five_seconds_of_continuous_edits() {
    let (daemon, _) = home();
    let mut world = World::start(daemon);
    open(&mut world);
    type_text(&mut world, "a");
    world.advance(Duration::from_millis(900));
    assert!(draft_on_disk(&world, PROFILE).is_none());
    world.advance(Duration::from_millis(100));
    assert_eq!(draft_on_disk(&world, PROFILE).unwrap()["text"], "a");

    for _ in 0..8 {
        type_text(&mut world, "b");
        world.advance(Duration::from_millis(600));
    }
    assert_eq!(draft_on_disk(&world, PROFILE).unwrap()["text"], "a");
    type_text(&mut world, "b");
    world.advance(Duration::from_millis(600));
    assert_eq!(
        draft_on_disk(&world, PROFILE).unwrap()["text"],
        "abbbbbbbbb",
        "a save lands five seconds after the first edit, though edits never paused a second"
    );
    rest(&mut world);
}

#[test]
fn a_draft_and_its_mailbox_survive_a_restart() {
    let (daemon, _) = home();
    let mut world = World::start(daemon);
    open(&mut world);
    type_text(&mut world, "Draft for later");
    press(&mut world, "mod+2");
    world.advance(Duration::from_secs(1));

    world.restart_capture();
    open(&mut world);
    assert_eq!(message_text(&world), "Draft for later");
    assert!(shows(&world, "To: Fern"));
    rest(&mut world);
}

#[test]
fn resolves_a_lost_acknowledgment_by_id_and_never_submits_a_duplicate() {
    let (daemon, kept) = home();
    let k = kept.clone();
    daemon.answer_in_turn("quick_capture_send", [Answer::Hold]);
    let mut world = World::start(daemon);
    open(&mut world);
    type_text(&mut world, "Sent once");
    press(&mut world, "enter");
    let held = world.daemon.sent_of("quick_capture_send");
    assert_eq!(held.len(), 1);
    assert!(shows(&world, "Saving…"));
    assert_eq!(draft_on_disk(&world, PROFILE).unwrap()["uncertain"], true);
    kept_send(&k, &held[0]);

    world.reconnect();
    assert_eq!(world.daemon.sent_of("quick_capture_get").len(), 1);
    assert_eq!(world.daemon.sent_of("quick_capture_send").len(), 1);
    assert_eq!(message_text(&world), "");
    assert!(shows(&world, "Saved for Ada"));
    rest(&mut world);
}

#[test]
fn a_send_the_daemon_never_kept_is_the_persons_to_send_again() {
    let (daemon, _) = home();
    daemon.answer_in_turn("quick_capture_send", [Answer::Hold]);
    let mut world = World::start(daemon);
    open(&mut world);
    type_text(&mut world, "Lost");
    press(&mut world, "enter");
    world.reconnect();
    assert_eq!(
        alert(&world).as_deref(),
        Some("Not yet saved. You can edit or send this draft again.")
    );
    assert_eq!(message_text(&world), "Lost");
    assert_eq!(draft_on_disk(&world, PROFILE).unwrap()["uncertain"], false);
    rest(&mut world);
}

/// A 1 by 1 pixel PNG.
fn png() -> Vec<u8> {
    const PIXEL: &str = "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c4890000000d49444154789c63000100000500010d0a2db40000000049454e44ae426082";
    (0..PIXEL.len())
        .step_by(2)
        .map(|i| u8::from_str_radix(&PIXEL[i..i + 2], 16).unwrap())
        .collect()
}

#[test]
fn pastes_a_screenshot_through_the_draft_folder_and_uploads_its_bytes_before_sending() {
    let (daemon, kept) = home();
    let mut world = World::start(daemon);
    open(&mut world);
    let image = ClipboardImage::from_png(png()).unwrap();
    world.capture_gesture(|d| {
        d.runtime
            .paste_content(ClipboardContent::new("").with_image(image));
    });
    let file = world
        .capture_screen()
        .get(Target::role("list-item").name("Pasted screenshot"));
    let id = file
        .key
        .unwrap()
        .trim_start_matches("capture.file.")
        .to_owned();
    let stored = world
        .data_dir()
        .join("quick-capture-drafts")
        .join(PROFILE)
        .join("capture-draft-files")
        .join(format!("{id}.file"));
    assert_eq!(std::fs::read(&stored).unwrap(), png());
    assert_eq!(
        kept.lock().unwrap().bytes[&id],
        png(),
        "an idle draft uploads its files"
    );

    press(&mut world, "enter");
    let sent = world.daemon.sent_of("quick_capture_send");
    assert_eq!(sent[0]["attachment_ids"], json!([id]));
    assert_eq!(sent[0]["content"], "");
    assert_eq!(
        world.daemon.sent_of("quick_capture_attachment_put").len(),
        1
    );
    assert!(!stored.exists(), "a kept send forgets the draft's files");
    rest(&mut world);
}

#[test]
fn drops_a_multi_megabyte_file_and_uploads_it_in_chunks() {
    let (daemon, kept) = home();
    let mut world = World::start(daemon);
    let dropped = world.data_dir().with_file_name("report.pdf");
    let bytes: Vec<u8> = (0..3_000_000u32).map(|i| i as u8).collect();
    std::fs::write(&dropped, &bytes).unwrap();
    open(&mut world);
    world.capture_gesture(|d| {
        d.runtime
            .drop_files(vec![dropped.clone()], Some((200.0, 200.0)))
    });
    let file = world
        .capture_screen()
        .get(Target::role("list-item").name("report.pdf"));
    let id = file
        .key
        .unwrap()
        .trim_start_matches("capture.file.")
        .to_owned();
    assert_eq!(kept.lock().unwrap().bytes[&id], bytes);
    let puts = world.daemon.sent_of("quick_capture_attachment_put");
    assert_eq!(puts.len(), 6, "3 MB in 512 KiB chunks");
    assert!(puts.iter().all(|p| p.to_string().len() < 800_000));
    let stored = draft_on_disk(&world, PROFILE).unwrap();
    assert_eq!(stored["files"], json!([{"id": id, "name": "report.pdf"}]));
    rest(&mut world);
}

#[test]
fn reconciles_a_ready_file_after_a_restart_and_sends_without_uploading_it_again() {
    let (daemon, kept) = home();
    let mut world = World::start(daemon);
    open(&mut world);
    let image = ClipboardImage::from_png(png()).unwrap();
    world.capture_gesture(|d| {
        d.runtime
            .paste_content(ClipboardContent::new("").with_image(image));
    });
    assert_eq!(
        world.daemon.sent_of("quick_capture_attachment_put").len(),
        1
    );

    world.restart_capture();
    open(&mut world);
    assert!(
        world
            .capture_screen()
            .find(Target::role("list-item").name("Pasted screenshot"))
            .is_some()
    );
    press(&mut world, "enter");
    assert_eq!(
        world.daemon.sent_of("quick_capture_attachment_put").len(),
        1,
        "the daemon said it holds the file"
    );
    assert_eq!(kept.lock().unwrap().records[0].attachments.len(), 1);
    rest(&mut world);
}

#[test]
fn removing_a_file_saves_the_draft_without_it_then_has_the_daemon_forget_it() {
    let (daemon, kept) = home();
    let mut world = World::start(daemon);
    open(&mut world);
    let image = ClipboardImage::from_png(png()).unwrap();
    world.capture_gesture(|d| {
        d.runtime
            .paste_content(ClipboardContent::new("").with_image(image));
    });
    world.capture_gesture(|d| {
        d.click(Target::role("button").name("Remove Pasted screenshot"));
    });
    assert!(
        world
            .capture_screen()
            .find(Target::role("list-item").name("Pasted screenshot"))
            .is_none()
    );
    assert_eq!(draft_on_disk(&world, PROFILE).unwrap()["files"], json!([]));
    assert_eq!(
        world
            .daemon
            .sent_of("quick_capture_attachment_discard")
            .len(),
        1
    );
    assert!(kept.lock().unwrap().assets.is_empty());
    rest(&mut world);
}

fn send(world: &mut World, text: &str) {
    open(world);
    type_text(world, text);
    press(world, "enter");
}

#[test]
fn recent_shows_sent_and_read_history_and_follows_read_receipts() {
    let (daemon, _) = home();
    let mut world = World::start(daemon);
    send(&mut world, "first");
    send(&mut world, "second");
    let first = world.daemon.sent_of("quick_capture_send")[0]["capture_id"]
        .as_str()
        .unwrap()
        .to_owned();
    open(&mut world);
    world.capture_gesture(|d| d.click(Target::role("button").name("Recent messages")));
    let rows = world.capture_screen().all(Target::role("list-item"));
    let texts: Vec<_> = rows.iter().map(|r| r.name.clone().unwrap()).collect();
    assert_eq!(texts, ["to Ada", "to Ada"]);
    assert!(shows(&world, "first"));
    assert!(shows(&world, "second"));
    let status = |world: &World| {
        world
            .capture_screen()
            .get(Target::key(format!("capture.history.item.{first}")))
            .value
            .unwrap()
    };
    assert!(status(&world).starts_with("Sent "));

    world.daemon.emit(message(QuickCaptureReadMessage {
        profile_id: PROFILE.into(),
        capture_id: first.clone(),
        read_at: "2026-11-12T09:30:00Z".into(),
        ..QuickCaptureReadMessage::new()
    }));
    world.settle();
    assert!(status(&world).starts_with("Read "));
    world.daemon.emit(message(QuickCaptureUnreadMessage {
        profile_id: PROFILE.into(),
        capture_id: first.clone(),
        ..QuickCaptureUnreadMessage::new()
    }));
    world.settle();
    assert!(status(&world).starts_with("Sent "));

    press(&mut world, "escape");
    assert!(
        world.capture_shown(),
        "Escape leaves the history before the panel"
    );
    assert!(
        world
            .capture_screen()
            .find(Target::key("capture.history"))
            .is_none()
    );
    rest(&mut world);
}

#[test]
fn recent_pages_older_messages_on_request() {
    let (daemon, _) = home();
    let mut world = World::start(daemon);
    for text in ["one", "two", "three", "four", "five", "six"] {
        send(&mut world, text);
    }
    open(&mut world);
    world.capture_gesture(|d| d.click(Target::role("button").name("Recent messages")));
    assert_eq!(
        world.capture_screen().all(Target::role("list-item")).len(),
        4
    );
    assert!(!shows(&world, "\"one\""));
    world.capture_gesture(|d| {
        let (x, y) = d.runtime.find("capture.history.list").unwrap();
        d.runtime.wheel(x, y, 2000.0);
    });
    world.capture_gesture(|d| d.click(Target::role("button").name("Show older messages")));
    assert_eq!(
        world.capture_screen().all(Target::role("list-item")).len(),
        6
    );
    assert!(
        world
            .capture_screen()
            .find(Target::role("button").name("Show older messages"))
            .is_none()
    );
    rest(&mut world);
}

#[test]
fn refreshes_only_the_newest_page_after_sending_from_a_closed_recent_view() {
    let (daemon, _) = home();
    let mut world = World::start(daemon);
    send(&mut world, "one");
    let lists = world.daemon.sent_of("quick_capture_list");
    assert_eq!(lists.len(), 1);
    assert!(lists[0].get("cursor").is_none());
    rest(&mut world);
}

#[test]
fn discarding_a_draft_from_recent_starts_a_fresh_one() {
    let (daemon, _) = home();
    let mut world = World::start(daemon);
    open(&mut world);
    type_text(&mut world, "never mind");
    world.capture_gesture(|d| d.click(Target::role("button").name("Recent messages")));
    world.capture_gesture(|d| d.click(Target::role("button").name("Discard draft")));
    assert!(
        world
            .capture_screen()
            .find(Target::role("button").name("Discard draft"))
            .is_none()
    );
    world.capture_gesture(|d| d.click(Target::role("button").name("Back to draft")));
    assert_eq!(message_text(&world), "");
    assert_eq!(draft_on_disk(&world, PROFILE).unwrap()["text"], "");
    rest(&mut world);
}

#[test]
fn each_profile_keeps_its_own_draft_and_roster() {
    let (daemon, _) = home();
    let mut world = World::start(daemon);
    open(&mut world);
    type_text(&mut world, "for work");
    world.daemon.emit(message(ProfileArrangementChangedMessage {
        profile: Profile {
            id: OTHER.into(),
            name: "Home".into(),
            ..Default::default()
        },
        ..ProfileArrangementChangedMessage::new()
    }));
    world.settle();
    assert_eq!(message_text(&world), "");
    assert!(shows(&world, "To: Otto"));
    assert_eq!(draft_on_disk(&world, PROFILE).unwrap()["text"], "for work");
    type_text(&mut world, "for home");

    world.daemon.emit(message(ProfileArrangementChangedMessage {
        profile: Profile {
            id: PROFILE.into(),
            name: "Work".into(),
            ..Default::default()
        },
        ..ProfileArrangementChangedMessage::new()
    }));
    world.settle();
    assert_eq!(message_text(&world), "for work");
    assert_eq!(draft_on_disk(&world, OTHER).unwrap()["text"], "for home");
    rest(&mut world);
}

#[test]
fn the_roster_follows_the_crew() {
    let (daemon, _) = home();
    let mut world = World::start(daemon);
    open(&mut world);
    let mut members = crew();
    members[0].binding_session = Some("s-1".into());
    members.push(member("moss", "Moss", PROFILE, false));
    world.daemon.emit(message(CrewUpdatedMessage {
        members,
        ..CrewUpdatedMessage::new()
    }));
    world.settle();
    press(&mut world, "mod+k");
    let options: Vec<_> = world
        .capture_screen()
        .all(Target::role("list-box-option"))
        .into_iter()
        .map(|o| o.name.unwrap_or_default())
        .collect();
    assert_eq!(options, ["Ada", "Fern", "Moss"]);
    assert!(shows(&world, "In session"));
    rest(&mut world);
}

fn switch_to(world: &mut World, id: &str, name: &str) {
    world.daemon.emit(message(ProfileArrangementChangedMessage {
        profile: Profile {
            id: id.into(),
            name: name.into(),
            ..Default::default()
        },
        ..ProfileArrangementChangedMessage::new()
    }));
    world.settle();
}

#[test]
fn a_late_acceptance_leaves_another_profiles_draft_and_settles_on_return() {
    let (daemon, kept) = home();
    daemon.answer_in_turn("quick_capture_send", [Answer::Hold]);
    let mut world = World::start(daemon);
    open(&mut world);
    type_text(&mut world, "A request");
    press(&mut world, "enter");
    let held = world.daemon.sent_of("quick_capture_send")[0].clone();

    switch_to(&mut world, OTHER, "Home");
    type_text(&mut world, "B draft");
    let accepted = kept_send(&kept, &held);
    world.daemon.reply_to(&held, accepted);
    world.settle();
    assert!(
        world.capture_shown(),
        "an answer for another profile hides nothing"
    );
    assert_eq!(message_text(&world), "B draft");
    let a = draft_on_disk(&world, PROFILE).unwrap();
    assert_eq!(
        (a["text"].as_str(), a["uncertain"].as_bool()),
        (Some("A request"), Some(true))
    );

    switch_to(&mut world, PROFILE, "Work");
    let checked = world.daemon.sent_of("quick_capture_get");
    assert_eq!(checked.len(), 1);
    assert_eq!(checked[0]["capture_id"], held["capture_id"]);
    assert_eq!(checked[0]["profile_id"], PROFILE);
    assert_eq!(message_text(&world), "");
    assert_eq!(draft_on_disk(&world, OTHER).unwrap()["text"], "B draft");
    assert_eq!(world.daemon.sent_of("quick_capture_send").len(), 1);
    assert!(world.capture_shown());
    rest(&mut world);
}

#[test]
fn a_file_uploading_when_the_profile_changes_stays_with_its_draft_and_finishes_on_return() {
    let (daemon, kept) = home();
    let holding = std::sync::atomic::AtomicBool::new(true);
    let k = kept.clone();
    daemon.on("quick_capture_attachment_put", move |r| {
        if holding.swap(false, std::sync::atomic::Ordering::Relaxed) {
            vec![]
        } else {
            vec![kept_put(&k, r)]
        }
    });
    let mut world = World::start(daemon);
    let dropped = world.data_dir().with_file_name("a.pdf");
    let bytes: Vec<u8> = (0..600_000u32).map(|i| i as u8).collect();
    std::fs::write(&dropped, &bytes).unwrap();
    open(&mut world);
    type_text(&mut world, "A retained draft");
    world.capture_gesture(|d| {
        d.runtime
            .drop_files(vec![dropped.clone()], Some((200.0, 200.0)))
    });
    let first = world.daemon.sent_of("quick_capture_attachment_put")[0].clone();
    assert_eq!(
        (first["profile_id"].as_str(), first["offset"].as_u64()),
        (Some(PROFILE), Some(0))
    );

    switch_to(&mut world, OTHER, "Home");
    assert_eq!(message_text(&world), "");
    assert!(
        world
            .capture_screen()
            .find(Target::role("list-item").name("a.pdf"))
            .is_none()
    );
    type_text(&mut world, "B retained draft");
    let late = kept_put(&kept, &first);
    world.daemon.reply_to(&first, late);
    world.settle();
    assert_eq!(
        world.daemon.sent_of("quick_capture_attachment_put").len(),
        1
    );
    assert_eq!(message_text(&world), "B retained draft");
    let a = draft_on_disk(&world, PROFILE).unwrap();
    assert_eq!(a["text"], "A retained draft");
    assert_eq!(a["files"][0]["name"], "a.pdf");

    switch_to(&mut world, PROFILE, "Work");
    assert_eq!(message_text(&world), "A retained draft");
    let puts = world.daemon.sent_of("quick_capture_attachment_put");
    assert_eq!(
        puts.len(),
        2,
        "the upload resumes where the daemon says it stopped"
    );
    assert_eq!(
        (puts[1]["offset"].as_u64(), puts[1]["final"].as_bool()),
        (Some(524_288), Some(true))
    );
    let id = first["attachment_id"].as_str().unwrap();
    let held = kept.lock().unwrap().bytes[id].clone();
    assert_eq!(held.len(), bytes.len());
    assert!(held == bytes, "the daemon holds the file's bytes");
    assert!(world.daemon.sent_of("quick_capture_send").is_empty());
    rest(&mut world);
}

fn folder(world: &World) -> std::path::PathBuf {
    world.data_dir().join("quick-capture-drafts").join(PROFILE)
}

/// Leaves a draft on disk as an earlier run of the panel did, and relaunches the panel on it.
fn relaunch_with(world: &mut World, text: &str, files: &[(&str, &str, &[u8])]) -> String {
    let id = "7d1c3a4e-0000-4000-8000-00000000c0de".to_owned();
    let folder = folder(world);
    std::fs::create_dir_all(folder.join("capture-draft-files")).unwrap();
    for (file, _, bytes) in files {
        std::fs::write(
            folder
                .join("capture-draft-files")
                .join(format!("{file}.file")),
            bytes,
        )
        .unwrap();
    }
    let stored = json!({
        "id": id,
        "text": text,
        "mailbox": "chief",
        "files": files.iter().map(|(file, name, _)| json!({"id": file, "name": name})).collect::<Vec<_>>(),
        "uncertain": false,
    });
    std::fs::write(folder.join("quick-capture-draft.json"), stored.to_string()).unwrap();
    world.restart_capture();
    open(world);
    id
}

const IMAGE: &str = "a3e1f0c2-1111-4111-8111-0000000000aa";
const SECOND: &str = "a3e1f0c2-2222-4222-8222-0000000000bb";

fn held_puts(daemon: &Daemon, kept: &Kept) -> Arc<std::sync::atomic::AtomicBool> {
    let holding = Arc::new(std::sync::atomic::AtomicBool::new(true));
    let (h, k) = (holding.clone(), kept.clone());
    daemon.on("quick_capture_attachment_put", move |r| {
        if h.load(std::sync::atomic::Ordering::Relaxed) {
            vec![]
        } else {
            vec![kept_put(&k, r)]
        }
    });
    holding
}

/// Makes the draft folder refuse writes until the returned guard drops.
struct ReadOnly(std::path::PathBuf);

impl ReadOnly {
    fn on(path: std::path::PathBuf) -> ReadOnly {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o555)).unwrap();
        ReadOnly(path)
    }
}

impl Drop for ReadOnly {
    fn drop(&mut self) {
        use std::os::unix::fs::PermissionsExt;
        let _ = std::fs::set_permissions(&self.0, std::fs::Permissions::from_mode(0o755));
    }
}

#[test]
fn text_pastes_into_the_message_without_attaching_anything() {
    for content in [
        ClipboardContent::new(" and more"),
        ClipboardContent::new(" and more").with_html("<b> and more</b>"),
    ] {
        let (daemon, _) = home();
        let mut world = World::start(daemon);
        open(&mut world);
        type_text(&mut world, "Existing message");
        world.capture_gesture(|d| d.runtime.paste_content(content));
        assert_eq!(message_text(&world), "Existing message and more");
        assert!(
            world
                .capture_screen()
                .find(Target::key("capture.files"))
                .is_none()
        );
        assert!(
            world
                .daemon
                .sent_of("quick_capture_attachment_put")
                .is_empty()
        );
        rest(&mut world);
    }
}

#[test]
fn a_copied_web_image_attaches_beside_its_html_and_leaves_the_message_alone() {
    let (daemon, kept) = home();
    let mut world = World::start(daemon);
    open(&mut world);
    type_text(&mut world, "Existing message");
    let image = ClipboardImage::from_png(png()).unwrap();
    world.capture_gesture(|d| {
        d.runtime.paste_content(
            ClipboardContent::new("https://example.invalid/copied.png")
                .with_html("<img src=\"https://example.invalid/copied.png\">")
                .with_image(image),
        );
    });
    assert_eq!(message_text(&world), "Existing message");
    let put = &world.daemon.sent_of("quick_capture_attachment_put")[0];
    assert_eq!(put["name"], "Pasted screenshot");
    assert_eq!(
        kept.lock().unwrap().bytes[put["attachment_id"].as_str().unwrap()],
        png()
    );
    rest(&mut world);
}

#[test]
fn a_pasted_pdf_attaches_and_sends_with_its_bytes() {
    let (daemon, kept) = home();
    let mut world = World::start(daemon);
    let pdf = world.data_dir().with_file_name("notes.pdf");
    std::fs::write(&pdf, b"%PDF-1.4\nQuick Capture PDF fixture\n%%EOF\n").unwrap();
    open(&mut world);
    world.capture_gesture(|d| {
        d.runtime
            .paste_content(ClipboardContent::new("").with_files([pdf.clone()]))
    });
    assert!(
        world
            .capture_screen()
            .find(Target::role("list-item").name("notes.pdf"))
            .is_some()
    );
    press(&mut world, "enter");
    let sent = &world.daemon.sent_of("quick_capture_send")[0];
    let id = sent["attachment_ids"][0].as_str().unwrap().to_owned();
    assert_eq!(
        kept.lock().unwrap().bytes[&id],
        std::fs::read(&pdf).unwrap()
    );
    rest(&mut world);
}

#[test]
fn enter_while_composing_stays_with_the_input_method() {
    let (daemon, _) = home();
    let mut world = World::start(daemon);
    open(&mut world);
    world.capture_gesture(|d| d.runtime.ime(rune::Ime::Preedit("にほん".into(), None)));
    press(&mut world, "enter");
    world.capture_gesture(|d| d.runtime.ime(rune::Ime::Commit("日本語".into())));
    press(&mut world, "shift+enter");
    assert_eq!(message_text(&world), "日本語\n");
    assert!(world.daemon.sent_of("quick_capture_send").is_empty());
    rest(&mut world);
}

#[test]
fn a_failed_upload_keeps_the_restored_image_and_sends_nothing() {
    let (daemon, _) = home();
    daemon.on("quick_capture_attachment_put", |_| {
        refusal("internal", "Upload storage unavailable")
    });
    let mut world = World::start(daemon);
    let capture = relaunch_with(&mut world, "", &[(IMAGE, "screenshot.png", &png())]);
    press(&mut world, "enter");
    let put = &world.daemon.sent_of("quick_capture_attachment_put")[0];
    assert_eq!(put["capture_id"], capture.as_str());
    assert_eq!(put["final"], true);
    assert!(world.daemon.sent_of("quick_capture_send").is_empty());
    assert_eq!(
        alert(&world).as_deref(),
        Some("File upload: Upload storage unavailable")
    );
    assert!(
        world
            .capture_screen()
            .find(Target::role("list-item").name("screenshot.png"))
            .is_some()
    );
    assert_eq!(
        draft_on_disk(&world, PROFILE).unwrap()["files"][0]["id"],
        IMAGE
    );
    rest(&mut world);
}

#[test]
fn removing_a_file_mid_upload_discards_it_after_the_upload_answers() {
    let (daemon, kept) = home();
    let holding = held_puts(&daemon, &kept);
    let mut world = World::start(daemon);
    let capture = relaunch_with(&mut world, "", &[(IMAGE, "remove.png", &png())]);
    let upload = world.daemon.sent_of("quick_capture_attachment_put")[0].clone();
    world.capture_gesture(|d| d.click(Target::role("button").name("Remove remove.png")));
    assert!(
        world
            .daemon
            .sent_of("quick_capture_attachment_discard")
            .is_empty()
    );

    holding.store(false, std::sync::atomic::Ordering::Relaxed);
    let answer = kept_put(&kept, &upload);
    world.daemon.reply_to(&upload, answer);
    world.settle();
    let discarded = world.daemon.sent_of("quick_capture_attachment_discard");
    assert_eq!(discarded.len(), 1);
    assert_eq!(
        (
            discarded[0]["capture_id"].as_str(),
            discarded[0]["attachment_id"].as_str()
        ),
        (Some(capture.as_str()), Some(IMAGE))
    );
    assert!(kept.lock().unwrap().assets.is_empty());
    assert_eq!(draft_on_disk(&world, PROFILE).unwrap()["files"], json!([]));
    assert!(world.daemon.sent_of("quick_capture_send").is_empty());
    rest(&mut world);
}

#[test]
fn a_send_waits_until_the_daemon_forgot_a_removed_file() {
    let (daemon, kept) = home();
    daemon.answer_in_turn("quick_capture_attachment_discard", [Answer::Hold]);
    let mut world = World::start(daemon);
    let capture = relaunch_with(
        &mut world,
        "Send without this file",
        &[(IMAGE, "removed.png", &png())],
    );
    world.capture_gesture(|d| d.click(Target::role("button").name("Remove removed.png")));
    let discard = world.daemon.sent_of("quick_capture_attachment_discard")[0].clone();
    press(&mut world, "enter");
    assert!(world.daemon.sent_of("quick_capture_send").is_empty());

    let answer = kept_discard(&kept, &discard);
    world.daemon.reply_to(&discard, answer);
    world.settle();
    press(&mut world, "enter");
    let sent = world.daemon.sent_of("quick_capture_send");
    assert_eq!(sent.len(), 1);
    assert_eq!(
        (sent[0]["capture_id"].as_str(), &sent[0]["attachment_ids"]),
        (Some(capture.as_str()), &json!([]))
    );
    rest(&mut world);
}

#[test]
fn a_removal_the_disk_refuses_keeps_the_file_through_edits_and_a_relaunch() {
    let (daemon, _) = home();
    let mut world = World::start(daemon);
    relaunch_with(
        &mut world,
        "Retain this file",
        &[(IMAGE, "retained.png", &png())],
    );
    {
        let _refusing = ReadOnly::on(folder(&world));
        world.capture_gesture(|d| d.click(Target::role("button").name("Remove retained.png")));
        assert!(alert(&world).unwrap().starts_with("Cannot remove file: "));
    }
    assert!(
        world
            .daemon
            .sent_of("quick_capture_attachment_discard")
            .is_empty()
    );
    press(&mut world, "mod+a");
    type_text(&mut world, "Edited after storage recovered");
    assert!(
        world
            .capture_screen()
            .find(Target::role("list-item").name("retained.png"))
            .is_some()
    );
    world.advance(Duration::from_secs(1));

    world.restart_capture();
    open(&mut world);
    assert_eq!(message_text(&world), "Edited after storage recovered");
    press(&mut world, "enter");
    assert_eq!(
        world.daemon.sent_of("quick_capture_send")[0]["attachment_ids"],
        json!([IMAGE])
    );
    rest(&mut world);
}

#[test]
fn edits_and_a_removal_made_while_another_file_uploads_are_kept() {
    let (daemon, kept) = home();
    let holding = held_puts(&daemon, &kept);
    let mut world = World::start(daemon);
    relaunch_with(
        &mut world,
        "Before upload",
        &[
            (IMAGE, "first.pdf", b"%PDF-1.4\n"),
            (SECOND, "second.pdf", b"%PDF-1.4\n"),
        ],
    );
    let puts = world.daemon.sent_of("quick_capture_attachment_put");
    type_text(&mut world, " edited while uploading");
    world.capture_gesture(|d| d.click(Target::role("button").name("Remove first.pdf")));
    holding.store(false, std::sync::atomic::Ordering::Relaxed);
    for put in &puts {
        let answer = kept_put(&kept, put);
        world.daemon.reply_to(put, answer);
        world.settle();
    }
    world.advance(Duration::from_secs(1));
    let stored = draft_on_disk(&world, PROFILE).unwrap();
    assert_eq!(stored["text"], "Before upload edited while uploading");
    assert_eq!(
        stored["files"],
        json!([{"id": SECOND, "name": "second.pdf"}])
    );
    assert_eq!(
        world.daemon.sent_of("quick_capture_attachment_discard")[0]["attachment_id"],
        IMAGE
    );
    rest(&mut world);
}

#[test]
fn a_disconnect_mid_upload_keeps_the_files_and_staging_resumes_on_reconnect() {
    let (daemon, kept) = home();
    let holding = held_puts(&daemon, &kept);
    let mut world = World::start(daemon);
    relaunch_with(
        &mut world,
        "",
        &[
            (IMAGE, "first.pdf", b"%PDF-1.4\n"),
            (SECOND, "second.pdf", b"%PDF-1.4\n"),
        ],
    );
    assert_eq!(
        world.daemon.sent_of("quick_capture_attachment_put").len(),
        2
    );
    world.daemon.disconnect();
    world.settle();
    assert!(alert(&world).is_some());
    press(&mut world, "escape");
    assert_eq!(
        draft_on_disk(&world, PROFILE).unwrap()["files"]
            .as_array()
            .unwrap()
            .len(),
        2
    );

    holding.store(false, std::sync::atomic::Ordering::Relaxed);
    world.advance(Duration::from_secs(30));
    assert_eq!(world.daemon.connections(), 2);
    let puts = world.daemon.sent_of("quick_capture_attachment_put");
    assert_eq!(
        puts.len(),
        4,
        "each file uploads again on the new connection"
    );
    assert!(
        kept.lock()
            .unwrap()
            .assets
            .iter()
            .all(|a| a.state == "ready")
    );
    rest(&mut world);
}

#[test]
fn uploads_a_restored_file_from_where_the_daemon_says_it_stopped() {
    for (length, offset) in [(0usize, 0u64), (524_291, 0), (524_292, 1), (524_293, 2)] {
        let (daemon, kept) = home();
        let bytes: Vec<u8> = (0..length).map(|i| i as u8).collect();
        {
            let mut held = kept.lock().unwrap();
            held.bytes
                .insert(IMAGE.into(), bytes[..offset as usize].to_vec());
            if offset > 0 {
                held.assets.push(QuickCaptureDraftAsset {
                    capture_id: "7d1c3a4e-0000-4000-8000-00000000c0de".into(),
                    attachment_id: IMAGE.into(),
                    name: "retained.bin".into(),
                    state: "staged".into(),
                    next_offset: offset as i64,
                });
            }
        }
        let mut world = World::start(daemon);
        relaunch_with(
            &mut world,
            "Binary attachment",
            &[(IMAGE, "retained.bin", &bytes)],
        );
        press(&mut world, "enter");
        let puts = world.daemon.sent_of("quick_capture_attachment_put");
        assert_eq!(puts[0]["offset"].as_u64(), Some(offset), "length {length}");
        assert_eq!(puts.last().unwrap()["final"], true);
        assert!(
            kept.lock().unwrap().bytes[IMAGE] == bytes,
            "length {length}"
        );
        assert_eq!(
            world.daemon.sent_of("quick_capture_send")[0]["attachment_ids"],
            json!([IMAGE])
        );
        rest(&mut world);
    }
}

#[test]
fn a_read_receipt_that_lands_while_a_page_loads_still_shows() {
    let (daemon, _) = home();
    let mut world = World::start(daemon);
    send(&mut world, "first");
    let first = world.daemon.sent_of("quick_capture_send")[0]["capture_id"]
        .as_str()
        .unwrap()
        .to_owned();
    world
        .daemon
        .answer_in_turn("quick_capture_list", [Answer::Hold]);
    open(&mut world);
    world.capture_gesture(|d| d.click(Target::role("button").name("Recent messages")));
    let held = world
        .daemon
        .sent_of("quick_capture_list")
        .last()
        .unwrap()
        .clone();
    world.daemon.emit(message(QuickCaptureReadMessage {
        profile_id: PROFILE.into(),
        capture_id: first.clone(),
        read_at: "2026-11-12T09:30:00Z".into(),
        ..QuickCaptureReadMessage::new()
    }));
    world.settle();
    let page = answer(QuickCaptureResult {
        list: Some(QuickCaptureListResult {
            items: vec![QuickCaptureRecord {
                id: first.clone(),
                mailbox: serde_json::from_value(json!({"kind": "crew_member", "member_id": "ada"}))
                    .unwrap(),
                content: "first".into(),
                created_at: "2026-11-12T09:00:00Z".into(),
                attachments: vec![],
                read_at: None,
            }],
            draft_assets: vec![],
            next_cursor: None,
        }),
        ..QuickCaptureResult::default()
    });
    world.daemon.reply_to(&held, page);
    world.settle();
    let status = world
        .capture_screen()
        .get(Target::key(format!("capture.history.item.{first}")))
        .value
        .unwrap();
    assert!(status.starts_with("Read "), "{status}");
    rest(&mut world);
}

#[test]
fn a_discarded_draft_stays_cleared_when_the_daemon_could_not_forget_its_files() {
    let (daemon, _) = home();
    daemon.on("quick_capture_attachment_discard", |_| {
        refusal("internal", "Storage offline")
    });
    let mut world = World::start(daemon);
    relaunch_with(&mut world, "Throw away", &[(IMAGE, "gone.png", &png())]);
    world.capture_gesture(|d| d.click(Target::role("button").name("Recent messages")));
    world.capture_gesture(|d| d.click(Target::role("button").name("Discard draft")));
    assert_eq!(
        world
            .daemon
            .sent_of("quick_capture_attachment_discard")
            .len(),
        1
    );
    world.restart_capture();
    open(&mut world);
    assert_eq!(message_text(&world), "");
    assert!(
        world
            .capture_screen()
            .find(Target::role("list-item").name("gone.png"))
            .is_none()
    );
    rest(&mut world);
}

#[test]
fn a_clear_the_disk_refuses_keeps_the_draft_and_asks_the_daemon_nothing() {
    let (daemon, _) = home();
    let mut world = World::start(daemon);
    relaunch_with(&mut world, "Keep me", &[(IMAGE, "kept.png", &png())]);
    world.capture_gesture(|d| d.click(Target::role("button").name("Recent messages")));
    {
        let _refusing = ReadOnly::on(folder(&world));
        world.capture_gesture(|d| d.click(Target::role("button").name("Discard draft")));
    }
    assert!(alert(&world).unwrap().starts_with("Cannot clear draft: "));
    assert!(
        world
            .daemon
            .sent_of("quick_capture_attachment_discard")
            .is_empty()
    );
    world.capture_gesture(|d| d.click(Target::role("button").name("Back to draft")));
    assert_eq!(message_text(&world), "Keep me");
    rest(&mut world);
}

#[test]
fn a_file_attached_after_the_draft_was_saved_keeps_its_bytes_and_uploads_them() {
    let (daemon, kept) = home();
    let mut world = World::start(daemon);
    open(&mut world);
    type_text(&mut world, "hello");
    world.advance(Duration::from_secs(2));
    assert_eq!(draft_on_disk(&world, PROFILE).unwrap()["text"], "hello");
    let image = ClipboardImage::from_png(png()).unwrap();
    world.capture_gesture(|d| {
        d.runtime
            .paste_content(ClipboardContent::new("").with_image(image));
    });
    let put = &world.daemon.sent_of("quick_capture_attachment_put")[0];
    let id = put["attachment_id"].as_str().unwrap().to_owned();
    assert_eq!(
        std::fs::read(
            folder(&world)
                .join("capture-draft-files")
                .join(format!("{id}.file"))
        )
        .unwrap(),
        png()
    );
    assert_eq!(kept.lock().unwrap().bytes[&id], png());
    assert!(alert(&world).is_none());
    rest(&mut world);
}
