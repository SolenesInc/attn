use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_protocol::{
    Desktop, DesktopPane, DesktopTileContentMessage, InitialStateMessage, Profile,
    ProfileArrangementChangedMessage, Session,
};
use attn_testkit::tree::{pane, row};
use attn_testkit::{Daemon, Target, World};
use serde_json::{Value, json};

const DESKTOP: &str = "work/desktop_1";
const PLAN: &str = "/work/a/plan.md";
const DOC: &str = "# Plan\n\nCache the tile layout before the first paint.\n";

/// The draft the daemon keeps for the document, and how it answers.
#[derive(Default)]
struct Kept {
    marks: Vec<Value>,
    generation: i64,
    /// Reads, or saves, to refuse before answering.
    refuse_reads: usize,
    refuse_saves: usize,
    /// What a send answers: its status and error.
    submit: Option<(&'static str, Option<&'static str>)>,
    /// The session the tile is bound to, when not the first agent's.
    bound: Option<&'static str>,
    /// The tile shows in its own window.
    popped: bool,
    /// The tile shows seed `s-plan` instead of the file, tended by the session named, if any.
    seed: Option<Option<&'static str>>,
}

type Shared = Arc<Mutex<Kept>>;

fn start() -> (World, Shared) {
    start_with(DOC, Kept::default())
}

fn start_with(doc: &str, kept: Kept) -> (World, Shared) {
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: DESKTOP.into(),
            ..Profile::default()
        }]),
        sessions: Some(
            [
                ("s-a", "Agent a", "idle"),
                ("s-b", "Agent b", "pending_approval"),
            ]
            .map(|(id, label, state)| Session {
                id: id.into(),
                label: label.into(),
                state: state.into(),
                directory: "/work/a".into(),
                profile_id: "work".into(),
                ..Session::default()
            })
            .to_vec(),
        ),
        desktops: Some(vec![desktop(
            row(vec![
                pane("a"),
                pane("b"),
                match kept.seed {
                    Some(_) => json!({
                        "type": "tile",
                        "tile_id": "m",
                        "tile_kind": "seed",
                        "tile_params": "s-plan",
                    }),
                    None => json!({
                        "type": "tile",
                        "tile_id": "m",
                        "tile_kind": "markdown",
                        "tile_params": json!({ "path": PLAN }).to_string(),
                        "tile_session_id": kept.bound.unwrap_or("s-a"),
                        "window": kept.popped.then(|| json!({"x": 60.0, "y": 60.0, "width": 900.0, "height": 700.0})),
                    }),
                },
            ]),
            1,
        )]),
        seeds: kept.seed.map(|tender| vec![plan_seed(doc, tender)]),
        ..InitialStateMessage::new()
    });
    if kept.seed.is_some() {
        let (doc, tender) = (doc.to_owned(), kept.seed.flatten());
        daemon.on("seed_document_get", move |r| {
            json!({
                "event": "seed_document_get_result",
                "request_id": r["request_id"],
                "success": true,
                "document": { "seed": plan_seed(&doc, tender), "notes": [], "notes_total": 0, "children": [] },
            })
        });
    }
    let kept = Arc::new(Mutex::new(kept));
    let draft = |kept: &Kept, r: &Value, event: &str, success: bool| {
        json!({
            "event": event,
            "request_id": r["request_id"],
            "document_uri": r["document_uri"],
            "source_kind": r["source_kind"],
            "success": success,
            "error": (!success).then_some("the store is busy"),
            "annotations": kept.marks,
            "generation": kept.generation,
        })
    };
    let read = kept.clone();
    daemon.on("markdown_annotations_get", move |r| {
        let mut kept = read.lock().unwrap();
        let refused = kept.refuse_reads > 0;
        kept.refuse_reads = kept.refuse_reads.saturating_sub(1);
        draft(&kept, r, "markdown_annotations_get_result", !refused)
    });
    let written = kept.clone();
    daemon.on("markdown_annotations_save", move |r| {
        let mut kept = written.lock().unwrap();
        if kept.refuse_saves > 0 {
            kept.refuse_saves -= 1;
            return draft(&kept, r, "markdown_annotations_save_result", false);
        }
        let removed: Vec<&str> = r["removed_ids"]
            .as_array()
            .into_iter()
            .flatten()
            .filter_map(Value::as_str)
            .collect();
        kept.marks
            .retain(|m| !removed.contains(&m["id"].as_str().unwrap_or_default()));
        for upsert in r["upserts"].as_array().into_iter().flatten() {
            kept.marks.retain(|m| m["id"] != upsert["id"]);
            kept.marks.push(upsert.clone());
        }
        kept.generation += 1;
        draft(&kept, r, "markdown_annotations_save_result", true)
    });
    let sending = kept.clone();
    daemon.on("markdown_annotations_submit", move |r| {
        let mut kept = sending.lock().unwrap();
        let (status, error) = match r["target_seed_id"].as_str() {
            Some(_) => ("noted", None),
            None => kept.submit.unwrap_or(("delivered", None)),
        };
        let went = matches!(status, "delivered" | "noted");
        if went {
            kept.marks.clear();
            kept.generation += 1;
        }
        let mut reply = draft(
            &kept,
            r,
            "markdown_annotations_submit_result",
            went || status == "skipped_pending_approval",
        );
        reply["status"] = json!(status);
        reply["error"] = json!(error);
        reply["target_session_id"] = r["target_session_id"].clone();
        if !went {
            reply.as_object_mut().unwrap().remove("generation");
            reply.as_object_mut().unwrap().remove("annotations");
        }
        reply
    });
    daemon.on("desktop_update_tile", |r| {
        json!({ "event": "desktop_update_tile_result", "request_id": r["request_id"], "success": r["tile_session_id"] != "s-refused" })
    });
    let seed = kept.lock().unwrap().seed.is_some();
    let mut world = World::start_sized(daemon, (2400.0, 900.0));
    world.open_desktop();
    if !seed {
        world.daemon.emit(content(doc));
    }
    world.settle();
    (world, kept)
}

/// Seed `s-plan`, whose body is `body`, tended by session `tender` when there is one.
fn plan_seed(body: &str, tender: Option<&str>) -> attn_protocol::Seed {
    attn_protocol::Seed {
        profile_id: "work".into(),
        id: "s-plan".into(),
        title: "Plan".into(),
        body: body.into(),
        status: "planted".into(),
        claimed: tender.is_some(),
        tender: tender.map(|session| attn_protocol::PartyView {
            name: "Agent b".into(),
            session_id: Some(session.into()),
            ..attn_protocol::PartyView::default()
        }),
        rev: 1,
        ..attn_protocol::Seed::default()
    }
}

fn desktop(tree: Value, revision: i64) -> Desktop {
    Desktop {
        id: DESKTOP.into(),
        profile_id: "work".into(),
        name: "Desktop 1".into(),
        shortcut_slot: Some(1),
        order_key: "1".into(),
        tree_json: tree.to_string(),
        active_pane_id: "a".into(),
        panes: ["a", "b"]
            .map(|p| DesktopPane {
                desktop_id: DESKTOP.into(),
                pane_id: p.into(),
                runtime_id: format!("r-{p}"),
                session_id: format!("s-{p}"),
                kind: "agent".into(),
                ..DesktopPane::default()
            })
            .to_vec(),
        revision,
    }
}

fn content(doc: &str) -> DesktopTileContentMessage {
    DesktopTileContentMessage {
        desktop_id: DESKTOP.into(),
        tile_id: "m".into(),
        tile_kind: "markdown".into(),
        path: PLAN.into(),
        content: doc.into(),
        ..DesktopTileContentMessage::new()
    }
}

/// Lets a pending save go.
fn pause(world: &mut World) {
    world.advance(Duration::from_secs(1));
    world.settle();
}

/// Selects bytes `range` of the document's block `block`, as a drag across them would.
fn select(world: &mut World, block: &str, range: std::ops::Range<usize>) {
    let at = format!("tile.m.document.md/{block}");
    world.gesture(move |app| app.update(move |_, tx| tx.select_text(at, range)));
    world.settle();
}

fn toolbar(world: &World) -> bool {
    world
        .screen()
        .find(Target::key("tile.m.document.toolbar"))
        .is_some()
}

fn click(world: &mut World, part: &str) {
    let key = format!("tile.m.document.{part}");
    world.gesture(|app| app.click(Target::key(key)));
    world.settle();
}

fn marks(kept: &Shared) -> Vec<Value> {
    kept.lock().unwrap().marks.clone()
}

/// Drags across the document's paragraph from `from` to `to`, fractions of its width along
/// its first line, and lets go.
fn select_in_paragraph(world: &mut World, from: f32, to: f32) {
    let [x, y, w, _] = world
        .screen()
        .bounds(Target::role("label").name("Cache the tile layout before the first paint."))
        .unwrap_or_else(|| panic!("no paragraph:\n{}", world.screen()));
    let line = y + 8.0;
    world.gesture(|app| {
        app.drag_from((x + w * from, line), (x + w * to, line));
        app.release((x + w * to, line));
    });
    world.settle();
}

#[test]
fn a_passage_selected_in_a_document_is_labelled_from_the_toolbar_and_kept_in_the_daemons_draft() {
    let (mut world, kept) = start();
    let read = world.daemon.sent_of("markdown_annotations_get");
    assert_eq!(read.len(), 1);
    assert_eq!(
        (
            read[0]["document_uri"].as_str(),
            read[0]["source_kind"].as_str(),
            read[0]["path"].as_str()
        ),
        (
            Some("attn://file/%2Fwork%2Fa%2Fplan.md"),
            Some("file"),
            Some(PLAN)
        )
    );

    select_in_paragraph(&mut world, 0.15, 0.6);
    world.gesture(|app| app.click(Target::key("tile.m.document.toolbar.label.i-agree")));
    pause(&mut world);

    let marks = marks(&kept);
    let [mark] = marks.as_slice() else {
        panic!("one mark: {marks:?}")
    };
    let anchor = &mark["anchor"];
    let exact = anchor["exact"].as_str().unwrap_or_default();
    assert!(!exact.is_empty() && DOC.contains(exact), "{anchor}");
    assert_eq!(
        (
            mark["type"].as_str(),
            mark["quick_label_id"].as_str(),
            mark["quick_label_text"].as_str()
        ),
        (Some("comment"), Some("i-agree"), Some("👍 I agree"))
    );
    assert_eq!(
        (anchor["block_id"].as_str(), anchor["start_line"].as_i64()),
        (Some("b1-paragraph"), Some(3))
    );
    assert!(
        world
            .screen()
            .find(Target::key("tile.m.document.toolbar"))
            .is_none(),
        "the toolbar closed"
    );
    let painted = world.screen().find(Target::role("button").name(exact));
    assert!(
        painted.is_some(),
        "the mark paints over {exact:?}:\n{}",
        world.screen()
    );
}

/// The Tauri reader's content hash: FNV-1a over UTF-16 units, low byte first.
fn content_hash(source: &str) -> String {
    let mut hash: u32 = 0x811c_9dc5;
    for unit in source.encode_utf16() {
        for byte in [unit & 0xff, unit >> 8] {
            hash ^= u32::from(byte);
            hash = hash.wrapping_mul(0x0100_0193);
        }
    }
    format!("{hash:08x}")
}

/// A mark the daemon already holds on DOC's words `exact`, as the Tauri reader anchors it.
fn held(id: &str, kind: &str, exact: &str, text: Option<&str>) -> Value {
    let paragraph = "Cache the tile layout before the first paint.";
    let start = paragraph.find(exact).unwrap();
    let end = start + exact.len();
    json!({
        "id": id,
        "type": kind,
        "text": text,
        "created_at": 1,
        "anchor": {
            "block_id": "b1-paragraph",
            "start_line": 3,
            "end_line": 3,
            "exact": exact,
            "prefix": &paragraph[start.saturating_sub(32)..start],
            "suffix": &paragraph[end..],
            "start": start,
            "end": end,
            "content_hash": content_hash(DOC),
        },
    })
}

fn holding(marks: Vec<Value>) -> Kept {
    Kept {
        marks,
        generation: 1,
        ..Kept::default()
    }
}

fn label_of(world: &World, part: &str) -> Option<String> {
    world
        .screen()
        .find(Target::key(format!("tile.m.document.{part}")))
        .and_then(|n| n.name)
}

fn ctrl_enter() -> rune::KeyEvent {
    rune::KeyEvent {
        key: rune::Key::Enter,
        mods: rune::Mods {
            ctrl: true,
            ..rune::Mods::default()
        },
    }
}

fn escape(world: &mut World) {
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        })
    });
    world.settle();
}

#[test]
fn delete_strikes_exactly_the_selected_words_and_a_selection_loses_the_whitespace_at_its_edges() {
    let (mut world, kept) = start();
    select(&mut world, "b1", 9..22);
    assert!(toolbar(&world));
    click(&mut world, "toolbar.delete");
    pause(&mut world);
    assert!(
        !toolbar(&world)
            && world
                .screen()
                .find(Target::key("tile.m.document.composer"))
                .is_none()
    );
    let marks = marks(&kept);
    assert_eq!(marks.len(), 1);
    assert_eq!(
        (
            marks[0]["type"].as_str(),
            marks[0]["anchor"]["exact"].as_str()
        ),
        (Some("deletion"), Some("tile layout"))
    );
    assert_eq!(
        (
            marks[0]["anchor"]["start"].as_i64(),
            marks[0]["anchor"]["end"].as_i64()
        ),
        (Some(10), Some(21))
    );
    assert!(marks[0].get("text").is_none_or(Value::is_null));
    let struck: Vec<_> = world
        .runtime()
        .elements()
        .into_iter()
        .filter(|e| !e.strikes.is_empty())
        .map(|e| (e.text.unwrap_or_default(), e.strikes))
        .collect();
    assert_eq!(
        struck,
        [(
            "Cache the tile layout before the first paint.".to_owned(),
            vec![[10, 21]]
        )],
        "a line runs through the deleted words"
    );

    select(&mut world, "b1", 9..10);
    assert!(!toolbar(&world), "whitespace alone opens nothing");
}

#[test]
fn a_selection_across_blocks_keeps_to_the_block_it_starts_in() {
    let doc = "# Plan\n\nCache the tile layout.\n\nSecond paragraph here.\n";
    let (mut world, kept) = start_with(doc, Kept::default());
    let bounds = |world: &World, name: &str| {
        world
            .screen()
            .bounds(Target::role("label").name(name))
            .unwrap()
    };
    let [x1, y1, _, h1] = bounds(&world, "Cache the tile layout.");
    let [x2, y2, _, h2] = bounds(&world, "Second paragraph here.");
    world.gesture(|app| {
        app.drag_from((x1 + 40.0, y1 + h1 / 2.0), (x2 + 40.0, y2 + h2 / 2.0));
        app.release((x2 + 40.0, y2 + h2 / 2.0));
    });
    world.settle();
    click(&mut world, "toolbar.label.this-is-wrong");
    pause(&mut world);
    let marks = marks(&kept);
    let anchor = &marks[0]["anchor"];
    assert_eq!(
        anchor["block_id"].as_str(),
        Some("b1-paragraph"),
        "{anchor}"
    );
    assert!(
        anchor["exact"].as_str().unwrap().ends_with("layout."),
        "{anchor}"
    );
}

#[test]
fn the_picker_numbers_the_other_labels_and_digits_or_alt_digits_pick_them() {
    let (mut world, kept) = start();
    select(&mut world, "b1", 10..21);
    click(&mut world, "toolbar.zap");
    let picks: Vec<String> = world
        .screen()
        .descendants(Target::key("tile.m.document.toolbar.picker"))
        .into_iter()
        .filter(|n| n.role == "button")
        .filter_map(|n| n.name)
        .collect();
    assert_eq!(
        picks,
        [
            "Exactly this",
            "I don't love this",
            "Verify this",
            "Show the receipt",
            "Give me an example",
            "Consider alternatives",
            "Cut this",
            "Your call",
            "Ask me first"
        ]
    );
    escape(&mut world);
    assert!(
        toolbar(&world)
            && world
                .screen()
                .find(Target::key("tile.m.document.toolbar.picker"))
                .is_none(),
        "Escape closes the list first"
    );
    click(&mut world, "toolbar.zap");
    world.gesture(|app| app.type_text("2"));
    world.settle();

    select(&mut world, "b1", 33..44);
    world.gesture(|app| app.press("alt+3"));
    world.settle();
    pause(&mut world);
    let labels: Vec<_> = marks(&kept)
        .iter()
        .map(|m| {
            (
                m["anchor"]["exact"].as_str().unwrap().to_owned(),
                m["quick_label_id"].as_str().unwrap().to_owned(),
            )
        })
        .collect();
    assert_eq!(
        labels,
        [
            ("tile layout".to_owned(), "dont-love-this".to_owned()),
            ("first paint".to_owned(), "verify-this".to_owned())
        ]
    );
    assert!(
        marks(&kept)[1]["quick_label_tip"]
            .as_str()
            .is_some_and(|t| !t.is_empty()),
        "a tipped label carries its tip"
    );

    select(&mut world, "b1", 0..5);
    world.gesture(|app| app.press("alt+0"));
    click(&mut world, "toolbar.zap");
    world.gesture(|app| app.type_text("0"));
    pause(&mut world);
    assert_eq!(marks(&kept).len(), 2, "0 labels nothing");
    click(&mut world, "toolbar.pick.cut-this");
    pause(&mut world);
    let cut = marks(&kept);
    assert_eq!(
        (
            cut[2]["anchor"]["exact"].as_str(),
            cut[2]["quick_label_id"].as_str()
        ),
        (Some("Cache"), Some("cut-this"))
    );

    select(&mut world, "b1", 22..28);
    escape(&mut world);
    assert!(!toolbar(&world));
}

#[test]
fn typing_over_a_selection_writes_a_comment_that_saves_only_with_words_and_waits_for_its_passage() {
    let (mut world, kept) = start();
    let composing = |world: &World| {
        world
            .screen()
            .find(Target::key("tile.m.document.composer"))
            .is_some()
    };
    for chord in ["ctrl+h", "alt+h", "cmd+h", "tab", "enter", "left"] {
        select(&mut world, "b1", 10..21);
        world.gesture(|app| app.press(chord));
        world.settle();
        assert!(!composing(&world), "{chord} starts no comment");
        escape(&mut world);
    }
    select(&mut world, "b1", 10..21);
    world.gesture(|app| app.type_text("N"));
    world.settle();
    world.gesture(|app| app.type_text("ice"));
    world.settle();
    let field = world
        .screen()
        .get(Target::key("tile.m.document.composer.field"));
    assert_eq!(field.value.as_deref(), Some("Nice"));
    let heading: Vec<String> = world
        .screen()
        .descendants(Target::key("tile.m.document.composer"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(
        heading.iter().any(|h| h == "\u{201c}tile layout\u{201d}"),
        "the box quotes its passage: {heading:?}"
    );
    world.gesture(|app| app.key(ctrl_enter()));
    pause(&mut world);
    assert_eq!(marks(&kept)[0]["text"].as_str(), Some("Nice"));
    assert!(
        world
            .screen()
            .find(Target::key("tile.m.document.composer"))
            .is_none()
    );

    select(&mut world, "b1", 33..44);
    click(&mut world, "toolbar.comment");
    click(&mut world, "composer.save");
    assert!(
        world
            .screen()
            .find(Target::key("tile.m.document.composer"))
            .is_some(),
        "an empty comment does not save"
    );
    click(&mut world, "composer.field");
    world.gesture(|app| app.type_text("half a thought"));
    world.settle();
    escape(&mut world);
    pause(&mut world);
    assert_eq!(marks(&kept).len(), 1, "Escape discards the composer");

    select(&mut world, "b1", 33..44);
    click(&mut world, "toolbar.comment");
    assert_eq!(
        world
            .screen()
            .get(Target::key("tile.m.document.composer.field"))
            .value
            .as_deref(),
        Some("half a thought")
    );
    escape(&mut world);
    select(&mut world, "b1", 0..5);
    click(&mut world, "toolbar.comment");
    assert_eq!(
        world
            .screen()
            .get(Target::key("tile.m.document.composer.field"))
            .value
            .as_deref()
            .unwrap_or_default(),
        ""
    );
}

#[test]
fn review_notes_list_marks_in_reading_order_and_remove_or_clear_them() {
    let (mut world, kept) = start_with(
        DOC,
        holding(vec![
            json!({ "id": "g", "type": "global", "text": "Overall fine", "created_at": 5 }),
            held("late", "comment", "first paint", Some("Measure it")),
            held("early", "deletion", "tile layout", None),
        ]),
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("tile.m.document.notes"))
            .name
            .as_deref(),
        Some("Notes 3")
    );
    click(&mut world, "notes");
    let cards: Vec<String> = world
        .screen()
        .descendants(Target::key("tile.m.document.notes.dialog"))
        .into_iter()
        .filter(|n| n.key.as_deref().is_some_and(|k| k.contains(".notes.card.")))
        .filter_map(|n| n.key)
        .collect();
    assert_eq!(
        cards,
        [
            "tile.m.document.notes.card.early",
            "tile.m.document.notes.card.late",
            "tile.m.document.notes.card.g"
        ]
    );

    click(&mut world, "notes.remove.late");
    pause(&mut world);
    assert_eq!(marks(&kept).len(), 2);
    assert_eq!(
        world
            .daemon
            .sent_of("markdown_annotations_save")
            .last()
            .unwrap()["removed_ids"],
        json!(["late"])
    );

    click(&mut world, "notes.clear");
    assert_eq!(label_of(&world, "notes.clear").as_deref(), Some("Confirm?"));
    world.advance(Duration::from_secs(4));
    world.settle();
    assert_eq!(
        label_of(&world, "notes.clear").as_deref(),
        Some("Clear all"),
        "the confirm disarms"
    );
    click(&mut world, "notes.clear");
    click(&mut world, "notes.clear");
    world.settle();
    assert!(marks(&kept).is_empty());
    let shown: Vec<String> = world
        .screen()
        .descendants(Target::key("tile.m.document.notes.dialog"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(
        shown.iter().any(|t| t == "No annotations yet."),
        "{shown:?}"
    );
}

#[test]
fn a_card_brings_its_mark_into_view_and_an_overall_note_is_written_from_the_header() {
    let (mut world, kept) = start_with(
        DOC,
        holding(vec![held("early", "deletion", "tile layout", None)]),
    );
    click(&mut world, "notes");
    click(&mut world, "notes.card.early");
    assert!(
        world
            .screen()
            .find(Target::key("tile.m.document.notes.dialog"))
            .is_none(),
        "the dialog closes"
    );

    click(&mut world, "overall");
    assert_eq!(
        world
            .screen()
            .get(Target::key("tile.m.document.composer"))
            .name
            .as_deref(),
        Some("Overall Note")
    );
    world.gesture(|app| app.type_text("Ship it"));
    click(&mut world, "composer.save");
    pause(&mut world);
    let global: Vec<_> = marks(&kept)
        .into_iter()
        .filter(|m| m["type"] == "global")
        .collect();
    assert_eq!(
        (global.len(), global[0]["text"].as_str()),
        (1, Some("Ship it"))
    );
    assert!(global[0].get("anchor").is_none_or(Value::is_null));
}

#[test]
fn held_marks_paint_without_saving_and_follow_their_words_when_the_document_changes() {
    let (mut world, kept) = start_with(
        DOC,
        holding(vec![held("m1", "comment", "tile layout", Some("why"))]),
    );
    pause(&mut world);
    assert!(
        world.daemon.sent_of("markdown_annotations_save").is_empty(),
        "nothing changed here"
    );
    assert!(
        world
            .screen()
            .find(Target::role("button").name("tile layout"))
            .is_some(),
        "{}",
        world.screen()
    );

    world.daemon.emit(content(&format!("Intro line.\n\n{DOC}")));
    world.settle();
    pause(&mut world);
    let anchor = marks(&kept)[0]["anchor"].clone();
    assert_eq!(
        (anchor["start_line"].as_i64(), anchor["block_id"].as_str()),
        (Some(5), Some("b2-paragraph")),
        "{anchor}"
    );

    world.daemon.emit(content("# Plan\n\nPaint first.\n"));
    world.settle();
    click(&mut world, "notes");
    let shown: Vec<String> = world
        .screen()
        .descendants(Target::key("tile.m.document.notes.dialog"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(
        shown.iter().any(|t| t == "⚠ moved") && shown.iter().any(|t| t == "~line 5 (moved)"),
        "{shown:?}"
    );
    assert!(
        world
            .screen()
            .find(Target::role("button").name("tile layout"))
            .is_none()
    );
}

#[test]
fn nothing_is_saved_until_the_draft_is_read_and_a_refused_read_or_save_is_tried_again() {
    let (mut world, kept) = start_with(
        DOC,
        Kept {
            refuse_reads: 1,
            refuse_saves: 1,
            ..holding(vec![held("m1", "comment", "first paint", Some("hm"))])
        },
    );
    select(&mut world, "b1", 10..21);
    click(&mut world, "toolbar.delete");
    pause(&mut world);
    assert!(
        world.daemon.sent_of("markdown_annotations_save").is_empty(),
        "not before the draft is read"
    );
    world.advance(Duration::from_secs(2));
    world.settle();
    pause(&mut world);
    assert_eq!(world.daemon.sent_of("markdown_annotations_get").len(), 2);
    assert_eq!(
        world.daemon.sent_of("markdown_annotations_save").len(),
        1,
        "the first save was refused"
    );
    world.advance(Duration::from_secs(5));
    world.settle();
    let marks = marks(&kept);
    assert_eq!(
        marks.len(),
        2,
        "the held mark and the one made meanwhile: {marks:?}"
    );
}

#[test]
fn the_draft_is_read_again_when_the_daemon_says_it_changed() {
    let (mut world, kept) = start();
    kept.lock().unwrap().marks.push(held(
        "agent",
        "comment",
        "first paint",
        Some("from elsewhere"),
    ));
    kept.lock().unwrap().generation = 4;
    world.daemon.emit(DesktopTileContentMessage {
        annotations_generation: Some(4),
        ..content(DOC)
    });
    world.settle();
    assert_eq!(world.daemon.sent_of("markdown_annotations_get").len(), 2);
    assert_eq!(
        world
            .screen()
            .get(Target::key("tile.m.document.notes"))
            .name
            .as_deref(),
        Some("Notes 1")
    );
}

#[test]
fn send_delivers_to_the_bound_session_then_says_sent_and_offers_the_destination_again() {
    let (mut world, kept) = start_with(
        DOC,
        holding(vec![held("m1", "comment", "first paint", Some("hm"))]),
    );
    assert_eq!(
        label_of(&world, "send").as_deref(),
        Some("Send 1 to Agent a")
    );
    select(&mut world, "b1", 10..21);
    click(&mut world, "toolbar.delete");
    click(&mut world, "send");
    let drafted: Vec<String> = world
        .daemon
        .sent()
        .iter()
        .filter_map(|r| r["cmd"].as_str())
        .filter(|cmd| cmd.starts_with("markdown_annotations_"))
        .map(str::to_owned)
        .collect();
    assert_eq!(
        drafted,
        [
            "markdown_annotations_get",
            "markdown_annotations_save",
            "markdown_annotations_submit"
        ],
        "the edit made just before Send is saved first, and nothing is read or saved after"
    );
    let submitted = world.daemon.sent_of("markdown_annotations_submit");
    assert_eq!(submitted.len(), 1);
    assert_eq!(
        (
            submitted[0]["target_session_id"].as_str(),
            submitted[0]["path"].as_str()
        ),
        (Some("s-a"), Some(PLAN))
    );
    assert_eq!(label_of(&world, "send").as_deref(), Some("Sent ✓"));
    assert!(marks(&kept).is_empty());
    world.advance(Duration::from_secs(5));
    world.settle();
    assert_eq!(
        world.daemon.sent_of("markdown_annotations_save").len()
            + world.daemon.sent_of("markdown_annotations_get").len(),
        2,
        "delivery leaves nothing to read again or save"
    );
    assert_eq!(
        label_of(&world, "send.to").as_deref(),
        Some("Annotation destination: Agent a")
    );
}

#[test]
fn a_send_the_session_could_not_take_keeps_the_marks_and_says_why() {
    for (status, error, label, status_line) in [
        (
            "skipped_pending_approval",
            None,
            "! Approval needed to Agent a",
            "Target is waiting for approval — not sent",
        ),
        (
            "error",
            Some("The session is gone"),
            "! Send failed to Agent a",
            "The session is gone",
        ),
    ] {
        let (mut world, kept) = start_with(
            DOC,
            Kept {
                submit: Some((status, error)),
                ..holding(vec![held("m1", "comment", "first paint", Some("hm"))])
            },
        );
        click(&mut world, "send");
        assert_eq!(label_of(&world, "send").as_deref(), Some(label), "{status}");
        assert_eq!(
            label_of(&world, "send.status").as_deref(),
            Some(status_line),
            "{status}"
        );
        assert_eq!(marks(&kept).len(), 1, "{status}");
    }
}

#[test]
fn the_send_key_sends_the_marks_of_the_document_tile_that_holds_the_keyboard() {
    let (mut world, _) = start_with(
        DOC,
        holding(vec![held("m1", "comment", "first paint", Some("hm"))]),
    );
    world.gesture(|app| app.click(Target::key("tile.m.header")));
    world.settle();
    let chord = if cfg!(target_os = "macos") {
        "cmd+enter"
    } else {
        "ctrl+shift+enter"
    };
    world.gesture(|app| app.press(chord));
    world.settle();
    assert_eq!(world.daemon.sent_of("markdown_annotations_submit").len(), 1);
}

#[test]
fn the_destination_menu_lists_the_desktops_sessions_and_retargets_the_tile_at_once() {
    let (mut world, _) = start_with(
        DOC,
        holding(vec![held("m1", "comment", "first paint", Some("hm"))]),
    );
    click(&mut world, "send.menu");
    let items: Vec<(String, Vec<String>)> = world
        .screen()
        .descendants(Target::key("tile.m.document.send.menu.list"))
        .into_iter()
        .filter(|n| n.role == "menu-item-radio")
        .map(|n| (n.name.unwrap_or_default(), n.states))
        .collect();
    assert_eq!(
        items.iter().map(|(n, _)| n.as_str()).collect::<Vec<_>>(),
        ["Agent a", "Agent b"]
    );
    assert!(items[0].1.iter().any(|s| s == "checked"));
    let shown: Vec<String> = world
        .screen()
        .descendants(Target::key("tile.m.document.send.menu.list"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(shown.iter().any(|s| s == "approval"), "{shown:?}");

    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Down,
            mods: rune::Mods::default(),
        })
    });
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Enter,
            mods: rune::Mods::default(),
        })
    });
    world.settle();
    let retarget = world.daemon.sent_of("desktop_update_tile");
    assert_eq!(
        (
            retarget[0]["tile_id"].as_str(),
            retarget[0]["tile_session_id"].as_str()
        ),
        (Some("m"), Some("s-b"))
    );
    assert_eq!(
        label_of(&world, "send").as_deref(),
        Some("Send 1 to Agent b")
    );
    click(&mut world, "send");
    assert_eq!(
        world.daemon.sent_of("markdown_annotations_submit")[0]["target_session_id"].as_str(),
        Some("s-b")
    );
}

#[test]
fn a_refused_retarget_sends_where_the_tile_is_bound_and_a_tile_bound_elsewhere_has_no_session() {
    let (mut world, _) = start_with(
        DOC,
        holding(vec![held("m1", "comment", "first paint", Some("hm"))]),
    );
    world.daemon.on("desktop_update_tile", |r| json!({ "event": "desktop_update_tile_result", "request_id": r["request_id"], "success": false, "error": "no" }));
    click(&mut world, "send.menu");
    click(&mut world, "send.menu.1");
    assert_eq!(
        label_of(&world, "send").as_deref(),
        Some("Send 1 to Agent a")
    );

    let (world, _) = start_with(
        DOC,
        Kept {
            bound: Some("s-gone"),
            ..holding(vec![held("m1", "comment", "first paint", Some("hm"))])
        },
    );
    let send = world.screen().get(Target::key("tile.m.document.send"));
    assert_eq!(send.name.as_deref(), Some("Send 1 to No session"));
    assert!(send.states.iter().any(|s| s == "disabled"));
}

#[test]
fn a_mark_made_just_before_its_tile_closes_is_still_saved() {
    let (mut world, kept) = start();
    select(&mut world, "b1", 9..22);
    click(&mut world, "toolbar.delete");
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: DESKTOP.into(),
            ..Profile::default()
        },
        desktops: vec![desktop(row(vec![pane("a"), pane("b")]), 2)],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
    assert!(world.screen().find(Target::key("tile.m")).is_none());
    pause(&mut world);
    let marks = marks(&kept);
    assert_eq!(marks.len(), 1);
    assert_eq!(marks[0]["anchor"]["exact"].as_str(), Some("tile layout"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_empty_comment_box_closes_on_a_press_outside_it_and_one_with_words_stays() {
    let (mut world, _) = start();
    let composer = |world: &World| {
        world
            .screen()
            .find(Target::key("tile.m.document.composer"))
            .is_some()
    };
    let [x, y, _, _] = world
        .screen()
        .bounds(Target::role("label").name("Cache the tile layout before the first paint."))
        .unwrap();
    let press_document = |world: &mut World| world.gesture(|app| app.click_at((x + 4.0, y + 8.0)));
    click(&mut world, "overall");
    assert!(composer(&world));
    press_document(&mut world);
    assert!(!composer(&world), "an empty box closes");

    click(&mut world, "overall");
    world.gesture(|app| app.type_text("Ship it"));
    press_document(&mut world);
    assert!(composer(&world), "a box with words stays");
    let field = world
        .screen()
        .get(Target::key("tile.m.document.composer.field"));
    assert_eq!(field.value.as_deref(), Some("Ship it"));
    escape(&mut world);
    assert!(!composer(&world));
    world.advance(Duration::from_secs(7));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_document_in_its_own_window_offers_no_annotations() {
    let (mut world, kept) = start_with(
        DOC,
        Kept {
            popped: true,
            ..holding(vec![held("m1", "comment", "first paint", Some("hm"))])
        },
    );
    let windows = world.tile_windows();
    assert_eq!(windows.len(), 1, "{windows:?}");
    let window = windows[0].key.clone();
    world.tile_gesture(&window, |app| {
        app.update(|_, tx| tx.select_text("tile.m.document/b1", 10..21))
    });
    let screen = world.tile_screen(&window);
    assert!(
        screen
            .find(Target::role("label").name("Cache the tile layout before the first paint."))
            .is_some(),
        "{screen}"
    );
    for part in ["toolbar", "send", "overall", "notes"] {
        assert!(
            screen
                .find(Target::key(format!("tile.m.document.{part}")))
                .is_none(),
            "{part}: {screen}"
        );
    }
    world.tile_gesture(&window, |app| app.type_text("N"));
    pause(&mut world);
    assert_eq!(marks(&kept).len(), 1);
    assert!(world.daemon.sent_of("markdown_annotations_save").is_empty());
}

#[test]
fn a_comment_box_stays_with_its_document_when_the_desktop_changes() {
    let (mut world, _) = start();
    let arrange = |world: &mut World, current: &str| {
        let mut away = desktop(row(vec![pane("c")]), 1);
        away.id = "work/desktop_2".into();
        away.name = "Desktop 2".into();
        away.shortcut_slot = Some(2);
        away.order_key = "2".into();
        away.active_pane_id = "c".into();
        away.panes = vec![DesktopPane {
            desktop_id: away.id.clone(),
            pane_id: "c".into(),
            runtime_id: "r-c".into(),
            session_id: "s-b".into(),
            kind: "agent".into(),
            ..DesktopPane::default()
        }];
        let mut here = desktop(
            row(vec![
                pane("a"),
                pane("b"),
                json!({
                    "type": "tile",
                    "tile_id": "m",
                    "tile_kind": "markdown",
                    "tile_params": json!({ "path": PLAN }).to_string(),
                    "tile_session_id": "s-a",
                }),
            ]),
            1,
        );
        here.active_pane_id = "m".into();
        world.daemon.emit(ProfileArrangementChangedMessage {
            profile: Profile {
                id: "work".into(),
                name: "Work".into(),
                current_desktop_id: current.into(),
                ..Profile::default()
            },
            desktops: vec![here, away],
            ..ProfileArrangementChangedMessage::new()
        });
        world.settle();
    };
    let shown = |world: &World, part: &str| {
        world
            .screen()
            .find(Target::key(format!("tile.m.document.{part}")))
    };
    let typed_into = |world: &World| -> Vec<String> {
        world
            .daemon
            .sent_of("pty_input")
            .iter()
            .map(|m| m["id"].as_str().unwrap_or_default().to_owned())
            .collect()
    };

    select(&mut world, "b1", 10..21);
    world.gesture(|app| app.type_text("Nice"));
    world.settle();
    assert!(shown(&world, "composer").is_some());

    arrange(&mut world, "work/desktop_2");
    assert!(
        shown(&world, "composer").is_none(),
        "the box leaves with its document:\n{}",
        world.screen()
    );
    world.gesture(|app| app.type_text("x"));
    world.settle();
    assert_eq!(typed_into(&world), ["r-c"], "keys go to the desktop shown");

    arrange(&mut world, "work/desktop_1");
    let field = shown(&world, "composer.field").expect("the box comes back with its document");
    assert_eq!(field.value.as_deref(), Some("Nice"));
    assert!(field.is("focused"), "the box holds the keyboard again");
    world.gesture(|app| app.type_text("!"));
    world.settle();
    assert_eq!(
        shown(&world, "composer.field")
            .and_then(|f| f.value)
            .as_deref(),
        Some("Nice!")
    );
    escape(&mut world);

    select(&mut world, "b1", 10..21);
    assert!(toolbar(&world));
    arrange(&mut world, "work/desktop_2");
    assert!(!toolbar(&world), "the toolbar leaves with its document");
    world.gesture(|app| app.type_text("y"));
    world.settle();
    assert_eq!(typed_into(&world), ["r-c", "r-c"]);
    arrange(&mut world, "work/desktop_1");
    assert!(
        shown(&world, "composer").is_none(),
        "a key typed on another desktop starts no comment"
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_toolbar_and_the_comment_box_move_with_their_passage_when_the_document_scrolls() {
    let doc = format!("{DOC}\n{}", "A paragraph that takes room.\n\n".repeat(60));
    let (mut world, _kept) = start_with(&doc, Kept::default());
    select(&mut world, "b6", 2..11);
    let document = Target::key("tile.m.document");
    let moved = |world: &mut World, part: &str| {
        let shown = Target::key(format!("tile.m.document.{part}"));
        let screen = world.screen();
        let [x, y, w, h] = screen.bounds(document.clone()).unwrap();
        let (offset, before) = (
            screen.scrolled(document.clone()).unwrap(),
            screen.bounds(shown.clone()).unwrap(),
        );
        world.gesture(|app| app.runtime.wheel(x + w / 2.0, y + h / 2.0, -40.0));
        world.settle();
        let screen = world.screen();
        let scrolled = screen.scrolled(document.clone()).unwrap() - offset;
        let after = screen.bounds(shown).unwrap();
        assert!(
            scrolled > 10.0,
            "the document scrolled {scrolled} under the {part}"
        );
        assert!(
            (before[1] - after[1] - scrolled).abs() < 1.0,
            "the {part} moved {} as the document scrolled {scrolled}",
            before[1] - after[1]
        );
    };
    moved(&mut world, "toolbar");
    click(&mut world, "toolbar.comment");
    moved(&mut world, "composer");
}

#[test]
fn a_seeds_marks_go_to_its_tender_or_onto_the_seed_as_a_note() {
    let (mut world, kept) = start_with(
        DOC,
        Kept {
            seed: Some(None),
            ..Kept::default()
        },
    );
    select(&mut world, "b1", 10..21);
    assert!(
        toolbar(&world),
        "a passage of the seed's body opens the toolbar"
    );
    click(&mut world, "toolbar.delete");
    pause(&mut world);
    let saved = world.daemon.sent_of("markdown_annotations_save");
    assert_eq!(
        (
            saved[0]["document_uri"].as_str(),
            saved[0]["source_kind"].as_str(),
            saved[0]["seed_id"].as_str()
        ),
        (Some("attn://seed/s-plan"), Some("seed"), Some("s-plan"))
    );
    assert_eq!(
        marks(&kept)[0]["anchor"]["exact"].as_str(),
        Some("tile layout")
    );
    assert_eq!(
        label_of(&world, "send").as_deref(),
        Some("Note on seed 1"),
        "a seed nobody tends offers only the note"
    );
    assert!(
        world
            .screen()
            .find(Target::key("tile.m.document.send.menu"))
            .is_none()
    );
    click(&mut world, "send");
    let submitted = world.daemon.sent_of("markdown_annotations_submit");
    assert_eq!(
        (
            submitted[0]["target_seed_id"].as_str(),
            submitted[0]["target_session_id"].as_str()
        ),
        (Some("s-plan"), None)
    );
    assert_eq!(label_of(&world, "send").as_deref(), Some("Noted ✓"));

    let (mut world, _) = start_with(
        DOC,
        Kept {
            seed: Some(Some("s-b")),
            ..Kept::default()
        },
    );
    select(&mut world, "b1", 10..21);
    click(&mut world, "toolbar.delete");
    assert_eq!(label_of(&world, "send").as_deref(), Some("Send 1"));
    click(&mut world, "send");
    let submitted = world.daemon.sent_of("markdown_annotations_submit");
    assert_eq!(
        submitted[0]["target_session_id"].as_str(),
        Some("s-b"),
        "a tended seed's marks go to its tender"
    );
    assert_eq!(label_of(&world, "send").as_deref(), Some("Sent ✓"));

    world.advance(Duration::from_secs(5));
    world.settle();
    select(&mut world, "b1", 10..21);
    click(&mut world, "toolbar.delete");
    click(&mut world, "send.menu");
    world.gesture(|app| app.click(Target::role("menu-item").name("Note on seed")));
    world.settle();
    let submitted = world.daemon.sent_of("markdown_annotations_submit");
    assert_eq!(
        submitted.last().unwrap()["target_seed_id"].as_str(),
        Some("s-plan"),
        "or onto the seed from the menu"
    );
    assert_eq!(label_of(&world, "send").as_deref(), Some("Noted ✓"));
}
