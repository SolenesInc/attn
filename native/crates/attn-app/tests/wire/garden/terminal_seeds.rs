use std::time::Duration;

use attn_protocol::{
    Desktop, DesktopPane, GardenSeedsUpdatedMessage, OpenSeedResultMessage, OutpostInfo, PartyView,
    ProfileArrangementChangedMessage,
};
use attn_term::{FONT_SIZE, Fonts};
use attn_testkit::{Request, Target, World, message, tree};
use rune::WheelEvent;
use serde_json::{Value, json};

use super::*;

const DESKTOP: &str = "work/desktop_1";
const KNOWN: &str = "s-7k3f9m";
const OTHER: &str = "s-2m8q4v";

fn navigable() -> Seed {
    with(seed(KNOWN, "Make seed IDs navigable"), |s| {
        s.body = "Recognize **valid** ids and [preview](https://example.test) them.".into();
        s.status = "growing".into();
        s.tender = Some(PartyView {
            name: "Trellis".into(),
            r#ref: "member:trellis".into(),
            session_id: None,
        });
    })
}

fn agent_pane(pane: &str, session: &str) -> DesktopPane {
    DesktopPane {
        desktop_id: DESKTOP.into(),
        pane_id: pane.into(),
        runtime_id: format!("r-{pane}"),
        session_id: session.into(),
        kind: "agent".into(),
        ..DesktopPane::default()
    }
}

fn desktop(tree_json: String) -> Desktop {
    Desktop {
        id: DESKTOP.into(),
        profile_id: PROFILE.into(),
        name: "Desktop 1".into(),
        shortcut_slot: Some(1),
        order_key: "1".into(),
        tree_json,
        active_pane_id: "a".into(),
        panes: vec![agent_pane("a", "s1")],
        revision: 1,
    }
}

/// Agent `s1`'s terminal alone on the desktop, beside `others` sessions, over a garden of
/// `seeds`.
fn start(seeds: Vec<Seed>, others: Vec<Session>, outposts: Vec<OutpostInfo>) -> World {
    let mut sessions = vec![session("s1", "Agent a")];
    sessions.extend(others);
    let daemon = Daemon::scripted().state(InitialStateMessage {
        profiles: Some(vec![Profile {
            id: PROFILE.into(),
            name: "Work".into(),
            current_desktop_id: DESKTOP.into(),
            ..Profile::default()
        }]),
        desktops: Some(vec![desktop(tree::pane("a").to_string())]),
        outposts: Some(outposts),
        ..initial(seeds, sessions)
    });
    let mut world = World::start_sized(daemon, (1400.0, 900.0));
    world.open_desktop();
    world
}

fn writing() -> World {
    start(
        vec![navigable(), seed(OTHER, "Another seed")],
        vec![],
        vec![],
    )
}

fn write(world: &mut World, seq: u32, data: &str) {
    let mut frame = vec![0x01, 3];
    frame.extend_from_slice(b"r-a");
    frame.extend_from_slice(&seq.to_be_bytes());
    frame.extend_from_slice(data.as_bytes());
    world.daemon.emit_frame(frame);
    world.settle();
}

fn cols(world: &mut World) -> usize {
    usize::from(world.terminal_size("r-a").0)
}

/// Each marked seed id's underline, as its row and columns on screen.
fn marks(world: &mut World) -> Vec<(u64, [u64; 2])> {
    world.terminal_view("r-a").expect("r-a is drawn")["overlays"]
        .as_array()
        .unwrap()
        .iter()
        .filter(|o| o["kind"] == "Underline")
        .map(|o| {
            let cols = o["cols"].as_array().unwrap();
            (
                o["row"].as_u64().unwrap(),
                [cols[0].as_u64().unwrap(), cols[1].as_u64().unwrap()],
            )
        })
        .collect()
}

fn cell(world: &World, col: u16, row: u16) -> (f32, f32) {
    let (rect, _) = world
        .runtime()
        .presented("terminal.r-a")
        .expect("r-a is drawn");
    let metrics = Fonts::default().metrics(FONT_SIZE);
    (
        rect[0] + (f32::from(col) + 0.5) * metrics.cell_w,
        rect[1] + (f32::from(row) + 0.5) * metrics.cell_h,
    )
}

fn rest_on(world: &mut World, col: u16, row: u16) {
    let at = cell(world, col, row);
    world.gesture(|app| app.runtime.pointer_move(at.0, at.1));
    world.advance(Duration::from_millis(200));
}

const CARD: &str = "terminal.r-a.seed-card";

fn card(world: &World) -> Option<attn_testkit::Node> {
    world.screen().find(Target::key(CARD))
}

fn card_says(world: &World) -> Vec<(String, String)> {
    world
        .screen()
        .descendants(Target::key(CARD))
        .into_iter()
        .filter_map(|n| Some((n.role, n.name?)))
        .collect()
}

fn card_heading(world: &World) -> String {
    card_says(world)
        .into_iter()
        .find(|(role, _)| role == "heading")
        .map(|(_, name)| name)
        .expect("the seed card is open")
}

fn card_buttons(world: &World) -> Vec<String> {
    card_says(world)
        .into_iter()
        .filter(|(role, _)| role == "button")
        .map(|(_, name)| name)
        .collect()
}

#[test]
fn marks_the_seed_ids_the_garden_knows_as_whole_words_on_every_row_a_wrapped_one_spans() {
    let mut world = start(vec![navigable()], vec![], vec![]);
    let pad = " ".repeat(cols(&mut world) - 4);
    write(
        &mut world,
        1,
        &format!("known {KNOWN}; unknown {OTHER}; x{KNOWN} {KNOWN}x é{KNOWN}\r\n{pad}{KNOWN}"),
    );
    let last = cols(&mut world) as u64;
    assert_eq!(
        marks(&mut world),
        [
            (0, [6, 14]),
            (0, [55, 63]),
            (1, [last - 4, last]),
            (2, [0, 4])
        ],
        "a known id is marked, one the garden does not know or glued to a word is not, and a combining mark ends the word before it"
    );

    world.daemon.emit(message(GardenSeedsUpdatedMessage {
        profile_id: PROFILE.into(),
        seeds: vec![navigable(), seed(OTHER, "Another seed")],
        total: 2,
        ..GardenSeedsUpdatedMessage::new()
    }));
    world.settle();
    assert!(
        marks(&mut world).contains(&(0, [24, 32])),
        "an id the garden learns about is marked"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn marks_an_id_that_wraps_between_its_s_and_its_dash_and_one_deep_in_a_long_line() {
    let mut world = writing();
    let cols = cols(&mut world);
    write(
        &mut world,
        1,
        &format!("{}{KNOWN}\r\n", " ".repeat(cols - 1)),
    );
    assert_eq!(
        marks(&mut world),
        [(0, [cols as u64 - 1, cols as u64]), (1, [0, 7])]
    );

    write(
        &mut world,
        2,
        &format!("{}{KNOWN}", " ".repeat(cols * 7 - 3)),
    );
    assert!(marks(&mut world).contains(&(8, [cols as u64 - 3, cols as u64])));
    assert!(marks(&mut world).contains(&(9, [0, 5])));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn follows_the_ids_a_writing_terminal_scrolls_into_and_out_of_view_and_hover_opens_the_one_under_the_pointer()
 {
    let mut world = writing();
    let rows = u64::from(world.terminal_size("r-a").1);
    write(&mut world, 1, &format!("first {KNOWN}\r\n"));
    assert_eq!(marks(&mut world), [(0, [6, 14])]);

    write(
        &mut world,
        2,
        &format!("{}second {OTHER}\r\n", "\r\n".repeat(10)),
    );
    assert_eq!(marks(&mut world), [(0, [6, 14]), (11, [7, 15])]);

    write(&mut world, 3, &"\r\n".repeat(rows as usize - 4));
    assert_eq!(
        marks(&mut world),
        [(2, [7, 15])],
        "the first id scrolled out of view"
    );

    write(&mut world, 4, &"\r\n".repeat(rows as usize));
    assert_eq!(marks(&mut world), []);

    write(&mut world, 5, &format!("third {KNOWN}"));
    let bottom = rows - 1;
    assert_eq!(marks(&mut world), [(bottom, [6, 14])]);

    rest_on(&mut world, 8, bottom as u16);
    assert_eq!(card_heading(&world), "Make seed IDs navigable");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn marks_ids_in_history_while_the_user_reads_it_and_the_terminal_keeps_writing() {
    let mut world = writing();
    let rows = usize::from(world.terminal_size("r-a").1);
    write(
        &mut world,
        1,
        &format!("old {KNOWN}\r\n{}", "\r\n".repeat(rows + 6)),
    );
    assert_eq!(marks(&mut world), []);

    let at = cell(&world, 1, 1);
    let row = Fonts::default().metrics(FONT_SIZE).cell_h;
    world.gesture(|app| {
        app.runtime
            .wheel_event(WheelEvent::new(0.0, (rows as f32) * row).at(at.0, at.1))
    });
    world.settle();
    assert_eq!(marks(&mut world).len(), 1, "the id in history is marked");

    write(&mut world, 2, "more output\r\n");
    assert_eq!(
        marks(&mut world).len(),
        1,
        "it stays marked as output arrives"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn previews_a_marked_seed_on_hover_and_opens_it_for_the_session() {
    let mut world = start(vec![navigable()], vec![], vec![]);
    write(&mut world, 1, &format!("known {KNOWN}"));

    let at = cell(&world, 8, 0);
    world.gesture(|app| app.runtime.pointer_move(at.0, at.1));
    world.advance(Duration::from_millis(100));
    assert!(
        card(&world).is_none(),
        "the card waits for the pointer to rest"
    );
    world.advance(Duration::from_millis(100));
    assert_eq!(card_heading(&world), "Make seed IDs navigable");
    let shown: Vec<String> = card_says(&world)
        .into_iter()
        .map(|(_, name)| name)
        .collect();
    assert!(
        shown
            .iter()
            .any(|t| t == "Recognize valid ids and preview them."),
        "{shown:?}"
    );
    assert!(shown.iter().any(|t| t == "Trellis"), "{shown:?}");
    assert_eq!(
        marks(&mut world),
        [(0, [6, 14])],
        "the previewed id stays marked"
    );

    world.gesture(|app| app.click(Target::key(format!("{CARD}.open"))));
    let sent = world.daemon.sent_of("open_seed");
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["seed_id"], KNOWN);
    assert_eq!(sent[0]["session_id"], "s1");
    assert!(card(&world).is_none());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_card_closes_once_the_pointer_leaves_the_id_and_the_card() {
    let mut world = start(vec![navigable()], vec![], vec![]);
    write(&mut world, 1, &format!("known {KNOWN}"));
    rest_on(&mut world, 8, 0);
    assert!(card(&world).is_some());

    rest_on(&mut world, 100, 30);
    assert!(
        card(&world).is_some(),
        "the card waits a moment before it closes"
    );
    world.advance(Duration::from_millis(100));
    assert!(card(&world).is_none());
    assert!(world.runtime_is_at_rest());
}

fn docked() -> Desktop {
    let tile = json!({
        "type": "tile",
        "tile_id": "tile-seed",
        "tile_kind": "seed",
        "tile_params": KNOWN,
        "tile_session_id": "s1",
    });
    Desktop {
        revision: 2,
        ..desktop(tree::row([tree::pane("a"), tile]).to_string())
    }
}

fn opened(request: &Request) -> OpenSeedResultMessage {
    OpenSeedResultMessage {
        request_id: request["request_id"].as_str().map(str::to_owned),
        success: true,
        seed_id: KNOWN.into(),
        desktop_id: Some(DESKTOP.into()),
        tile_id: Some("tile-seed".into()),
        ..OpenSeedResultMessage::new()
    }
}

fn arranged() -> Value {
    message(ProfileArrangementChangedMessage {
        profile: Profile {
            id: PROFILE.into(),
            name: "Work".into(),
            current_desktop_id: DESKTOP.into(),
            ..Profile::default()
        },
        desktops: vec![docked()],
        ..ProfileArrangementChangedMessage::new()
    })
}

/// Answers the open with a seed tile docked beside `s1`'s pane.
fn opens_a_tile(world: &mut World) {
    world
        .daemon
        .on("open_seed", |r| vec![message(opened(r)), arranged()]);
}

fn focused(world: &World) -> Option<String> {
    world.screen().focused().and_then(|n| n.key)
}

#[test]
fn shows_the_seed_tile_it_opened_with_one_request_and_puts_the_keyboard_in_it() {
    let mut world = start(vec![navigable()], vec![], vec![]);
    opens_a_tile(&mut world);
    write(&mut world, 1, &format!("known {KNOWN}"));
    rest_on(&mut world, 8, 0);

    let set_before = world.daemon.sent_of("desktop_set_current").len();
    world.gesture(|app| app.click(Target::key(format!("{CARD}.open"))));
    world.settle();

    let shown = world.daemon.sent_of("desktop_show_leaf");
    assert_eq!(shown.len(), 1);
    assert_eq!(shown[0]["leaf_id"], "tile-seed");
    assert_eq!(
        world.daemon.sent_of("desktop_set_current").len(),
        set_before
    );
    let window = world.automate("state", json!({"of": "window"})).unwrap();
    assert_eq!(window["keyboard"]["pane"], "tile-seed");
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn opens_the_seed_tile_but_leaves_the_keyboard_on_a_control_the_user_moved_to_while_it_opened() {
    let mut world = start(vec![navigable()], vec![], vec![]);
    world.daemon.on("open_seed", |_| Vec::<Value>::new());
    write(&mut world, 1, &format!("known {KNOWN}"));
    rest_on(&mut world, 8, 0);
    world.gesture(|app| app.click(Target::key(format!("{CARD}.open"))));

    world.gesture(|app| app.key(super::board()));
    let moved = focused(&world);
    let holder = |world: &mut World| {
        world.automate("state", json!({"of": "window"})).unwrap()["keyboard"]["pane"].clone()
    };
    let held = holder(&mut world);
    assert!(
        moved.as_deref().is_some_and(|k| k.starts_with("garden")),
        "{moved:?}"
    );
    let request = world.daemon.sent_of("open_seed").remove(0);
    world.daemon.reply_to(&request, opened(&request));
    world.daemon.emit(arranged());
    world.settle();

    assert_eq!(world.daemon.sent_of("desktop_show_leaf").len(), 1);
    assert_eq!(focused(&world), moved);
    assert_eq!(holder(&mut world), held);
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

fn beside_reviewer(seed: Seed, reviewer: Session, outposts: Vec<OutpostInfo>) -> World {
    let mut world = start(vec![seed], vec![reviewer], outposts);
    write(&mut world, 1, &format!("known {KNOWN}"));
    rest_on(&mut world, 8, 0);
    world
}

fn reviewer() -> Session {
    Session {
        state: "working".into(),
        ..session("s2", "Seed reviewer")
    }
}

fn tended_by(session: &str, live: bool) -> impl FnOnce(&mut Seed) {
    let session = session.to_owned();
    move |s| {
        s.claimed = live;
        s.tender = live.then(|| PartyView {
            name: "Seed reviewer".into(),
            r#ref: format!("session:{session}"),
            session_id: Some(session.clone()),
        });
    }
}

fn goes_to_reviewer(mut world: World) {
    world.gesture(|app| app.click(Target::key(format!("{CARD}.agent"))));
    let shows = world.daemon.sent_of("desktop_show_session");
    assert_eq!(shows.len(), 1);
    assert_eq!(shows[0]["session_id"], "s2");
    assert!(card(&world).is_none());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn goes_from_the_seed_card_to_the_session_tending_the_seed() {
    let seed = with(navigable(), tended_by("s2", true));
    goes_to_reviewer(beside_reviewer(seed, reviewer(), vec![]));
}

#[test]
fn goes_from_the_card_of_a_seed_nobody_tends_to_the_last_session_that_tended_it_while_that_session_is_open()
 {
    let seed = with(navigable(), |s| {
        tended_by("s2", false)(s);
        s.status = "parked".into();
        s.last_execution_id = Some("s2".into());
    });
    goes_to_reviewer(beside_reviewer(seed, reviewer(), vec![]));
}

#[test]
fn goes_from_the_seed_card_to_a_tender_on_an_outpost() {
    let seed = with(navigable(), tended_by("s2", true));
    let on_outpost = Session {
        host: Some("studio-id".into()),
        ..reviewer()
    };
    let studio = OutpostInfo {
        id: "studio-id".into(),
        name: "studio".into(),
        destination: "studio:2200".into(),
        state: "connected".into(),
        reach: "reachable".into(),
        ..OutpostInfo::default()
    };
    goes_to_reviewer(beside_reviewer(seed, on_outpost, vec![studio]));
}

#[test]
fn offers_no_agent_on_the_seed_card_when_its_last_tender_closed_nobody_tended_it_or_its_tender_is_the_terminal_showing_it()
 {
    let closed = with(navigable(), |s| {
        tended_by("s2", false)(s);
        s.status = "harvested".into();
        s.last_execution_id = Some("closed-session".into());
    });
    let untended = with(navigable(), |s| {
        tended_by("s2", false)(s);
        s.status = "planted".into();
    });
    let here = with(navigable(), tended_by("s1", true));
    for (case, seed) in [
        ("its last tender closed", closed),
        ("nobody tended it", untended),
        ("its tender is the terminal showing it", here),
    ] {
        let world = beside_reviewer(seed, reviewer(), vec![]);
        assert_eq!(card_buttons(&world), ["Open as tile"], "{case}");
    }
}
