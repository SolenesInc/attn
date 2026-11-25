use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_protocol::{
    Desktop, DesktopPane, OutpostInfo, OutpostsUpdatedMessage, Profile,
    ProfileArrangementChangedMessage, ProfilesChangedMessage, ResolveSessionsResultMessage,
    ResolvedSession, Seed, Session, SessionClosedMessage, SessionDelegationRole,
    SessionLedgerEntry, SessionReopen, SessionReopenResult, SessionReopenResultMessage,
    SessionStateChangedMessage, SessionUnregisteredMessage,
};
use attn_testkit::{Daemon, Node, Request, Target, World, message, tree};
use rune::{Cursor, Key, KeyEvent, Mods, PhysicalInput};
use serde_json::{Value, json};

use super::ledger::{closed, entry, hold, ledger_daemon, query_text, refuse_with, rows};
use super::terminal::{cell, desktop, inputs, output, pane, profile, showing};
use super::terminal_view::copied;
use crate::wire::mac;

pub(super) const BUILDER: &str = "3f9a2c4e-8b1d-4c6e-9a7f-2d5e8c1b4a6f";
pub(super) const BUILDER_SHORT: &str = "ag-7yd2rk";
pub(super) const MAPPER: &str = "c5e81f07-4a36-4f8f-b2d9-7701aa5c9e3d";
pub(super) const MAPPER_SHORT: &str = "ag-rqm1y1";
const STRANGER: &str = "9d2e4f61-5b08-4c3a-87e9-f1a60d7c2b54";
pub(super) const SEED: &str = "s-7k3f9m";

pub(super) const AGENT_CARD: &str = "terminal.r1.agent-card";
pub(super) const SEED_CARD: &str = "terminal.r1.seed-card";

pub(super) fn agent(id: &str, label: &str) -> Session {
    Session {
        id: id.into(),
        label: label.into(),
        agent: "claude".into(),
        state: "working".into(),
        profile_id: "work".into(),
        ..Session::default()
    }
}

pub(super) fn builder() -> Session {
    Session {
        delegation_role: Some(SessionDelegationRole {
            name: "Builder".into(),
            ..SessionDelegationRole::default()
        }),
        ..agent(BUILDER, "Fix the retry uploader")
    }
}

pub(super) fn mapper() -> SessionLedgerEntry {
    SessionLedgerEntry {
        branch: Some("upload-retry-map".into()),
        ..closed(entry(MAPPER, "Map where uploads retry", 180), "user")
    }
}

pub(super) fn mapped() -> Seed {
    Seed {
        profile_id: "work".into(),
        id: SEED.into(),
        title: "Map the uploader".into(),
        status: "harvested".into(),
        last_execution_id: Some(MAPPER.into()),
        ..Seed::default()
    }
}

/// A window whose one terminal shows agent `s1`, which reads about the others.
pub(super) fn start(
    others: Vec<Session>,
    seeds: Vec<Seed>,
    ledger: Vec<SessionLedgerEntry>,
) -> World {
    let mut sessions = vec![agent("s1", "Reader")];
    sessions.extend(others);
    let reader = DesktopPane {
        session_id: "s1".into(),
        ..pane("t1", "r1")
    };
    let mut state = showing(vec![reader], "daemon-1");
    state.sessions = Some(sessions);
    state.seeds_total = Some(seeds.len() as i64);
    state.seeds = Some(seeds);
    let mut world = World::start_sized(ledger_daemon(state, ledger.clone()), (1400.0, 900.0));
    world.daemon.on("resolve_sessions", move |r| {
        let found = ledger
            .iter()
            .filter(|e| names(r).contains(&e.id) || names(r).contains(&short(&e.id)))
            .map(|e| resolved(r, e))
            .collect();
        answer(r, found)
    });
    world.open_desktop();
    world
}

fn short(id: &str) -> String {
    attn_protocol::session_id::short(id).into_owned()
}

fn names(request: &Request) -> Vec<String> {
    let refs = request["refs"].as_array().unwrap();
    refs.iter()
        .map(|r| r.as_str().unwrap().to_owned())
        .collect()
}

fn resolved(request: &Request, entry: &SessionLedgerEntry) -> ResolvedSession {
    let named = names(request)
        .into_iter()
        .find(|r| *r == entry.id || *r == short(&entry.id));
    ResolvedSession {
        r#ref: named.unwrap(),
        closed: entry.clone(),
    }
}

fn answer(request: &Request, found: Vec<ResolvedSession>) -> Value {
    message(ResolveSessionsResultMessage {
        request_id: request["request_id"].as_str().unwrap().into(),
        success: true,
        found,
        ..ResolveSessionsResultMessage::new()
    })
}

fn asked(world: &World) -> Vec<Vec<String>> {
    let sent = world.daemon.sent_of("resolve_sessions");
    sent.iter().map(names).collect()
}

pub(super) fn write(world: &mut World, seq: u32, text: &str) {
    output(world, seq, text.as_bytes());
    world.settle();
}

type Cells = (u64, [u64; 2]);

/// The overlays on screen, each as its kind and the row and columns it covers.
fn overlays_in(world: &mut World, runtime: &str) -> Vec<(String, Cells)> {
    let view = world.terminal_view(runtime).expect("it is drawn");
    let overlays = view["overlays"].as_array().unwrap().iter();
    overlays
        .map(|o| {
            let cols = o["cols"].as_array().unwrap();
            (
                o["kind"].as_str().unwrap().to_owned(),
                (
                    o["row"].as_u64().unwrap(),
                    [cols[0].as_u64().unwrap(), cols[1].as_u64().unwrap()],
                ),
            )
        })
        .collect()
}

/// The role icons on screen, each with the row and columns it stands over.
fn icons(world: &mut World) -> Vec<(String, Cells)> {
    let overlays = overlays_in(world, "r1").into_iter();
    overlays
        .filter_map(|(kind, at)| {
            let name = kind.strip_prefix("Sprite(")?.strip_suffix(')')?;
            Some((name.to_owned(), at))
        })
        .collect()
}

/// Each marked id's underline, and the role icon before it, as its row and columns on screen.
fn marks_in(world: &mut World, runtime: &str) -> Vec<Cells> {
    let overlays = overlays_in(world, runtime);
    let icon_before = |(row, [start, _]): Cells| {
        let icon = |(kind, (r, cols)): &(String, Cells)| {
            (kind.starts_with("Sprite") && *r == row && cols[1] == start).then_some(cols[0])
        };
        overlays.iter().find_map(icon)
    };
    let underlines = overlays.iter().filter(|(kind, _)| kind == "Underline");
    underlines
        .map(|&(_, at)| (at.0, [icon_before(at).unwrap_or(at.1[0]), at.1[1]]))
        .collect()
}

fn marks(world: &mut World) -> Vec<(u64, [u64; 2])> {
    marks_in(world, "r1")
}

/// The marked words on screen, each row's part of a wrapped one on its own.
fn marked(world: &mut World) -> Vec<String> {
    let lines = world.terminal_text("r1");
    marks(world)
        .into_iter()
        .map(|(row, [start, end])| {
            let cells = lines[row as usize].chars().filter(|c| *c != '\u{301}');
            let cells = cells.skip(start as usize);
            cells.take((end - start) as usize).collect()
        })
        .collect()
}

pub(super) fn rest_on(world: &mut World, col: u16, row: u16) {
    let at = cell(world, col, row);
    world.gesture(|app| app.runtime.pointer_move(at.0, at.1));
    world.advance(Duration::from_millis(200));
}

fn accelerator(world: &mut World, held: bool) {
    let mods = Mods {
        logo: held && mac(),
        cmd: held,
        ctrl: held && !mac(),
        ..Mods::default()
    };
    world.gesture(|app| app.runtime.physical_input(PhysicalInput::Modifiers(mods)));
}

fn opens(world: &mut World, col: u16, row: u16) {
    let at = cell(world, col, row);
    world.gesture(|app| app.runtime.pointer_move(at.0, at.1));
    accelerator(world, true);
    world.gesture(|app| {
        app.runtime.pointer_down(at.0, at.1);
        app.runtime.pointer_up(at.0, at.1);
    });
    accelerator(world, false);
}

pub(super) fn key(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

pub(super) fn letter(world: &mut World, c: &str) {
    key(world, Key::Text(c.into()));
}

fn into_card() -> &'static str {
    if mac() { "mod+i" } else { "mod+shift+i" }
}

fn takes_keyboard(world: &mut World) {
    world.gesture(|app| app.press(into_card()));
}

pub(super) fn shown(world: &World, card: &str) -> bool {
    world.screen().find(Target::key(card)).is_some()
}

pub(super) fn said(world: &World, part: &str) -> Option<String> {
    let key = format!("{AGENT_CARD}.picked.{part}");
    world.screen().find(Target::key(key))?.name
}

fn buttons(world: &World, card: &str) -> Vec<String> {
    let parts: Vec<Node> = world.screen().descendants(Target::key(card));
    parts
        .into_iter()
        .filter(|n| n.role == "button")
        .filter_map(|n| n.name)
        .collect()
}

pub(super) fn sessions_shown(world: &World) -> Vec<String> {
    super::ledger::shown_session(world)
}

pub(super) fn rests(world: &mut World) {
    world.advance(Duration::from_millis(250));
    assert!(world.runtime_is_at_rest());
}

fn closes_builder(world: &mut World) {
    world.daemon.emit(message(SessionClosedMessage {
        session_ledger_entry: closed(entry(BUILDER, "Fix the retry uploader", 1), "user"),
        ..SessionClosedMessage::new()
    }));
    world.daemon.emit(message(SessionUnregisteredMessage {
        session: builder(),
        ..SessionUnregisteredMessage::new()
    }));
    world.settle();
}

#[test]
fn marks_every_form_of_a_live_sessions_id_as_a_whole_word_on_every_row_it_wraps_over() {
    let mut world = start(vec![builder()], vec![], vec![]);
    let cols = usize::from(world.terminal_size("r1").0);
    write(
        &mut world,
        1,
        &format!(
            "{BUILDER_SHORT} 3f9a2c4e {BUILDER}\r\nx{BUILDER_SHORT} {BUILDER_SHORT}x 13f9a2c4e 3f9a2c4e_ e\u{301}{BUILDER_SHORT} {BUILDER_SHORT}e\u{301}\r\ncommit 1d4e6f90 AG-7YD2RK\r\n{}{BUILDER}",
            " ".repeat(cols - 10)
        ),
    );
    assert_eq!(
        marked(&mut world),
        [
            BUILDER_SHORT,
            "3f9a2c4e",
            BUILDER,
            BUILDER_SHORT,
            &BUILDER[..10],
            &BUILDER[10..],
        ],
        "a word glued to an id leaves it unmarked, and a combining mark ends the word before it"
    );
    let cols = cols as u64;
    assert_eq!(
        marks(&mut world)[3..],
        [
            (1, [43, 52]),
            (3, [cols - 10, cols]),
            (4, [0, BUILDER.len() as u64 - 10])
        ]
    );
    assert_eq!(asked(&world), Vec::<Vec<String>>::new());
    assert!(world.runtime_is_at_rest());
}

fn named(role: &str, builtin: Option<&str>) -> Session {
    let mut session = builder();
    let given = session.delegation_role.as_mut().unwrap();
    given.name = role.into();
    given.builtin = builtin.map(Into::into);
    session
}

fn changes(world: &mut World, session: Session) {
    world.daemon.emit(message(SessionStateChangedMessage {
        session,
        ..SessionStateChangedMessage::new()
    }));
    world.settle();
}

#[test]
fn a_live_agents_role_icon_stands_over_its_short_ids_prefix_and_follows_the_role() {
    let plain = agent(STRANGER, "No role");
    let mut world = start(
        vec![named("Builder", Some("builder")), plain],
        vec![mapped()],
        vec![mapper()],
    );
    write(
        &mut world,
        1,
        &format!(
            "by {BUILDER_SHORT}, {MAPPER_SHORT} {SEED}\r\n3f9a2c4e {BUILDER} {}\r\n",
            short(STRANGER)
        ),
    );
    assert_eq!(
        icons(&mut world),
        [("role-code".into(), (0, [3, 6]))],
        "a closed agent, one with no role, a seed and the longer forms keep the plain mark"
    );
    assert_eq!(marked(&mut world).len(), 6);

    changes(&mut world, named("Reviewer", Some("reviewer")));
    assert_eq!(icons(&mut world), [("role-list".into(), (0, [3, 6]))]);
    changes(&mut world, named("gardener", None));
    assert_eq!(icons(&mut world), [("G".into(), (0, [3, 6]))]);
    closes_builder(&mut world);
    assert_eq!(icons(&mut world), []);
    assert_eq!(marked(&mut world).len(), 6);
    rests(&mut world);
}

#[test]
fn a_role_icon_comes_with_the_frame_that_draws_its_id_and_none_follows() {
    let mut world = start(vec![builder()], vec![], vec![]);
    write(&mut world, 1, "waiting\r\n");
    let (frames, paints) = (world.terminal_frames(), world.runtime().stats.paints);
    write(&mut world, 2, &format!("by {BUILDER_SHORT}\r\n"));
    assert_eq!(icons(&mut world), [("B".into(), (1, [3, 6]))]);
    world.advance(Duration::from_secs(5));
    assert_eq!(world.terminal_frames(), frames + 1);
    assert_eq!(world.runtime().stats.paints, paints);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_id_under_a_role_icon_reads_and_copies_as_its_text() {
    let mut world = start(vec![builder()], vec![], vec![]);
    write(&mut world, 1, &format!("by {BUILDER_SHORT} now\r\n"));
    assert_eq!(icons(&mut world).len(), 1);
    assert_eq!(
        world.terminal_text("r1")[0],
        format!("by {BUILDER_SHORT} now")
    );
    let (from, to) = (cell(&world, 3, 0), cell(&world, 11, 0));
    world.gesture(|app| {
        app.runtime.pointer_down(from.0, from.1);
        app.runtime.pointer_move(to.0, to.1);
        app.runtime.pointer_up(to.0, to.1);
    });
    assert_eq!(copied(&mut world).as_deref(), Some(BUILDER_SHORT));
    assert_eq!(icons(&mut world).len(), 1, "a selected id keeps its icon");
}

#[test]
fn the_text_under_a_role_icon_shows_while_the_cursor_is_on_it() {
    let mut world = start(vec![builder()], vec![], vec![]);
    write(&mut world, 1, &format!("{BUILDER_SHORT} x\r\x1b[2C"));
    assert_eq!(icons(&mut world), []);
    assert_eq!(marked(&mut world), [BUILDER_SHORT]);
    write(&mut world, 2, "\x1b[C");
    assert_eq!(icons(&mut world).len(), 1);
    write(&mut world, 3, "\x1b[D");
    assert_eq!(icons(&mut world), []);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn marks_an_id_that_wraps_between_its_prefix_and_its_body() {
    let mut world = start(vec![builder()], vec![], vec![]);
    let cols = usize::from(world.terminal_size("r1").0);
    write(
        &mut world,
        1,
        &format!("{}{BUILDER_SHORT}", " ".repeat(cols * 2 - 2)),
    );
    let cols = cols as u64;
    assert_eq!(marks(&mut world), [(1, [cols - 2, cols]), (2, [0, 7])]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn follows_the_sessions_of_the_profile_as_they_start_and_close_and_marks_no_other_profiles() {
    let stranger = Session {
        profile_id: "side".into(),
        ..agent(STRANGER, "Elsewhere")
    };
    let mut world = start(vec![stranger], vec![], vec![]);
    write(
        &mut world,
        1,
        &format!("{STRANGER} 9d2e4f61 ag-kmq4yr 3f9a2c4e {BUILDER_SHORT}"),
    );
    assert_eq!(marked(&mut world), Vec::<String>::new());

    world.daemon.emit(message(SessionStateChangedMessage {
        session: builder(),
        ..SessionStateChangedMessage::new()
    }));
    world.settle();
    assert_eq!(marked(&mut world), ["3f9a2c4e", BUILDER_SHORT]);

    closes_builder(&mut world);
    assert_eq!(
        marked(&mut world),
        ["3f9a2c4e", BUILDER_SHORT],
        "a session that closed stays marked"
    );
    rest_on(&mut world, 58, 0);
    assert!(said(&world, "state").is_some_and(|s| s.starts_with("closed")));
    world.daemon.emit(message(SessionStateChangedMessage {
        session: builder(),
        ..SessionStateChangedMessage::new()
    }));
    world.settle();
    assert_eq!(
        said(&world, "state").as_deref(),
        Some("working"),
        "the card of a session that came back shows it live"
    );
    rests(&mut world);
}

#[test]
fn a_short_id_two_live_sessions_share_is_not_marked_and_their_longer_ids_are() {
    let twin = "3f9a2c4d-0000-4000-8000-000000000001";
    let mut world = start(vec![builder(), agent(twin, "Twin")], vec![], vec![]);
    write(
        &mut world,
        1,
        &format!("{BUILDER_SHORT} 3f9a2c4e 3f9a2c4d {twin}"),
    );
    assert_eq!(marked(&mut world), ["3f9a2c4e", "3f9a2c4d", twin]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn asks_once_in_one_request_about_the_explicit_ids_it_does_not_know_and_marks_the_closed_ones() {
    let mut world = start(vec![builder()], vec![], vec![mapper()]);
    let unknown = "00000000-0000-4000-8000-000000000000";
    write(
        &mut world,
        1,
        &format!("{MAPPER_SHORT} {unknown} ag-zzzzzz 1d4e6f90 c5e81f07 {BUILDER_SHORT}\r\n"),
    );
    assert_eq!(asked(&world), [[unknown, MAPPER_SHORT, "ag-zzzzzz"]]);
    assert_eq!(
        marked(&mut world),
        [MAPPER_SHORT, "c5e81f07", BUILDER_SHORT],
        "once the app knows a closed session, its eight hex digits mark too"
    );

    write(&mut world, 2, &format!("{unknown} ag-zzzzzz {MAPPER}\r\n"));
    assert_eq!(asked(&world).len(), 1, "an id is asked about once");
    assert_eq!(marked(&mut world).last().unwrap(), MAPPER);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn asks_again_about_an_id_no_answer_came_for_once_the_text_changes_and_one_request_at_a_time() {
    let mut world = start(vec![], vec![], vec![mapper()]);
    let held = hold(&world, "resolve_sessions");
    write(&mut world, 1, &format!("{MAPPER_SHORT}\r\n"));
    write(&mut world, 2, "ag-zzzzzz\r\n");
    assert_eq!(asked(&world), [[MAPPER_SHORT]]);
    assert_eq!(marked(&mut world), Vec::<String>::new());

    world.daemon.on("resolve_sessions", |r| {
        let found = match names(r).contains(&MAPPER_SHORT.to_owned()) {
            true => vec![resolved(r, &mapper())],
            false => vec![],
        };
        answer(r, found)
    });
    let request = held.lock().unwrap().remove(0);
    world.daemon.reply_to(
        &request,
        json!({"event": "resolve_sessions_result", "success": false, "error": "busy", "found": []}),
    );
    world.settle();
    assert_eq!(
        asked(&world)[1..],
        [[MAPPER_SHORT, "ag-zzzzzz"]],
        "the text changed while the daemon had not answered"
    );
    assert_eq!(marked(&mut world), [MAPPER_SHORT]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_profile_switch_asks_again_and_marks_only_what_the_new_profile_holds() {
    let stranger = Session {
        profile_id: "side".into(),
        ..agent(STRANGER, "Elsewhere")
    };
    let mut world = start(vec![builder(), stranger], vec![], vec![mapper()]);
    let text = format!("{BUILDER_SHORT} {MAPPER_SHORT} 9d2e4f61");
    write(&mut world, 1, &text);
    assert_eq!(marked(&mut world), [BUILDER_SHORT, MAPPER_SHORT]);

    world.daemon.on("resolve_sessions", |r| answer(r, vec![]));
    let side = Profile {
        id: "side".into(),
        name: "Side".into(),
        current_desktop_id: "d2".into(),
        ..Profile::default()
    };
    world.daemon.emit(message(ProfilesChangedMessage {
        profiles: vec![profile(), side.clone()],
        ..ProfilesChangedMessage::new()
    }));
    world.daemon.emit(message(ProfileArrangementChangedMessage {
        profile: side,
        desktops: vec![Desktop {
            id: "d2".into(),
            profile_id: "side".into(),
            ..desktop(vec![DesktopPane {
                desktop_id: "d2".into(),
                ..pane("t2", "r2")
            }])
        }],
        ..ProfileArrangementChangedMessage::new()
    }));
    world.settle();
    let mut frame = vec![0x01, 2];
    frame.extend_from_slice(b"r2");
    frame.extend_from_slice(&1u32.to_be_bytes());
    frame.extend_from_slice(text.as_bytes());
    world.daemon.emit_frame(frame);
    world.settle();

    assert_eq!(
        asked(&world).last().unwrap(),
        &[BUILDER_SHORT, MAPPER_SHORT]
    );
    let start = (BUILDER_SHORT.len() + MAPPER_SHORT.len() + 2) as u64;
    assert_eq!(marks_in(&mut world, "r2"), [(0, [start, start + 8])]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn resting_on_a_live_sessions_id_shows_its_agent_card_and_return_opens_the_agent() {
    let mut world = start(vec![builder()], vec![], vec![]);
    write(&mut world, 1, &format!("ask {BUILDER_SHORT}"));
    let at = cell(&world, 6, 0);
    world.gesture(|app| app.runtime.pointer_move(at.0, at.1));
    world.advance(Duration::from_millis(100));
    assert!(
        !shown(&world, AGENT_CARD),
        "the card waits for the pointer to rest"
    );
    world.advance(Duration::from_millis(100));
    assert_eq!(
        said(&world, "name").as_deref(),
        Some("Fix the retry uploader")
    );
    let [x, y, ..] = world.screen().bounds(Target::key(AGENT_CARD)).unwrap();
    let id = cell(&world, 4, 0);
    assert!(
        x < id.0 && y > id.1,
        "the card hangs under the id's first cell at {id:?}: {x}, {y}"
    );

    takes_keyboard(&mut world);
    key(&mut world, Key::Enter);
    assert_eq!(sessions_shown(&world), [BUILDER]);
    assert!(!shown(&world, AGENT_CARD));
    assert_eq!(
        inputs(&world),
        [],
        "the card kept its keys from the terminal"
    );
    rests(&mut world);
}

#[test]
fn the_card_closes_once_the_pointer_leaves_the_id_and_stays_while_it_is_on_the_card() {
    let mut world = start(vec![builder()], vec![], vec![]);
    write(&mut world, 1, BUILDER_SHORT);
    rest_on(&mut world, 3, 0);
    let [x, y, w, h] = world.screen().bounds(Target::key(AGENT_CARD)).unwrap();
    let inside = (x + w / 2.0, y + h / 2.0);
    world.gesture(|app| app.runtime.pointer_move(inside.0, inside.1));
    world.advance(Duration::from_secs(1));
    assert!(shown(&world, AGENT_CARD));

    let away = cell(&world, 100, 40);
    for step in 0..3 {
        let at = (away.0 + 20.0 * step as f32, away.1);
        world.gesture(|app| app.runtime.pointer_move(at.0, at.1));
        assert!(
            shown(&world, AGENT_CARD),
            "the card waits a moment before it closes"
        );
        world.advance(Duration::from_millis(100));
    }
    assert!(
        !shown(&world, AGENT_CARD),
        "a moving pointer does not put it off"
    );
    rests(&mut world);
}

#[test]
fn the_pointer_on_an_open_card_leaves_the_ids_it_covers_alone() {
    let mut world = start(vec![builder()], vec![mapped()], vec![mapper()]);
    write(
        &mut world,
        1,
        &format!("{BUILDER_SHORT}\r\n{MAPPER_SHORT}\r\n{SEED}\r\n{MAPPER_SHORT}"),
    );
    rest_on(&mut world, 3, 0);
    let named = said(&world, "name");
    assert!(named.is_some());
    for row in [1, 2, 3] {
        let at = cell(&world, 3, row);
        for step in 0..3 {
            world.gesture(|app| app.runtime.pointer_move(at.0 + step as f32, at.1));
            world.advance(Duration::from_millis(300));
            assert_eq!(said(&world, "name"), named);
            assert!(!shown(&world, SEED_CARD));
        }
    }

    key(&mut world, Key::Escape);
    assert!(!shown(&world, AGENT_CARD));
    let under = cell(&world, 5, 2);
    world.gesture(|app| app.runtime.pointer_move(under.0, under.1));
    world.advance(Duration::from_millis(200));
    assert!(shown(&world, SEED_CARD), "a closed card covers nothing");
    rests(&mut world);
}

#[test]
fn moving_off_an_open_card_straight_onto_another_id_opens_its_card() {
    let mut world = start(vec![builder()], vec![mapped()], vec![]);
    write(
        &mut world,
        1,
        &format!("{BUILDER_SHORT}{}{SEED}", "\r\n".repeat(20)),
    );
    rest_on(&mut world, 3, 0);
    let [x, y, w, h] = world.screen().bounds(Target::key(AGENT_CARD)).unwrap();
    let inside = (x + w / 2.0, y + h / 2.0);
    world.gesture(|app| app.runtime.pointer_move(inside.0, inside.1));
    world.advance(Duration::from_millis(50));
    rest_on(&mut world, 3, 20);
    assert!(shown(&world, SEED_CARD));
    assert!(!shown(&world, AGENT_CARD));
    rests(&mut world);
}

#[test]
fn passing_over_another_id_leaves_the_open_card_where_it_is() {
    let mut world = start(vec![builder()], vec![mapped()], vec![]);
    write(&mut world, 1, &format!("{SEED} {BUILDER_SHORT}"));
    rest_on(&mut world, 3, 0);
    assert!(shown(&world, SEED_CARD));
    for col in [12, 3] {
        let at = cell(&world, col, 0);
        world.gesture(|app| app.runtime.pointer_move(at.0, at.1));
        world.advance(Duration::from_millis(50));
    }
    world.advance(Duration::from_secs(1));
    assert!(shown(&world, SEED_CARD));
    assert!(!shown(&world, AGENT_CARD));
    rests(&mut world);
}

#[test]
fn a_click_with_the_accelerator_opens_a_live_agent_and_a_plain_one_does_not() {
    let mut world = start(vec![builder()], vec![], vec![]);
    write(
        &mut world,
        1,
        &format!("\u{1b}[?1000h{BUILDER_SHORT} plain"),
    );
    let at = cell(&world, 3, 0);
    world.gesture(|app| app.runtime.pointer_move(at.0, at.1));
    assert_eq!(world.runtime().cursor(), Cursor::Text);
    accelerator(&mut world, true);
    assert_eq!(world.runtime().cursor(), Cursor::Pointer);
    accelerator(&mut world, false);
    assert_eq!(world.runtime().cursor(), Cursor::Text);

    opens(&mut world, 3, 0);
    assert_eq!(sessions_shown(&world), [BUILDER]);
    assert_eq!(
        inputs(&world),
        [],
        "the program hears no press that opened an agent"
    );

    world.gesture(|app| {
        app.runtime.pointer_down(at.0, at.1);
        app.runtime.pointer_up(at.0, at.1);
    });
    assert_eq!(sessions_shown(&world).len(), 1);
    assert!(!inputs(&world).is_empty());
    rests(&mut world);
}

#[test]
fn a_live_agent_on_an_outpost_shows_its_outpost_on_its_card_and_opens() {
    let there = Session {
        host: Some("host-1".into()),
        ..builder()
    };
    let mut world = start(vec![there], vec![], vec![]);
    world.daemon.emit(message(OutpostsUpdatedMessage {
        outposts: vec![OutpostInfo {
            id: "host-1".into(),
            name: "studio".into(),
            state: "connected".into(),
            reach: "reachable".into(),
            ..OutpostInfo::default()
        }],
        ..OutpostsUpdatedMessage::new()
    }));
    write(&mut world, 1, BUILDER_SHORT);
    rest_on(&mut world, 3, 0);
    let words: Vec<String> = world
        .screen()
        .descendants(Target::key(AGENT_CARD))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(words.iter().any(|w| w.ends_with("· studio")), "{words:?}");
    opens(&mut world, 3, 0);
    assert_eq!(sessions_shown(&world), [BUILDER]);
    rests(&mut world);
}

#[test]
fn a_click_with_the_accelerator_on_a_seed_id_opens_the_seed_as_a_tile_beside_the_agent() {
    let mut world = start(vec![], vec![mapped()], vec![]);
    write(&mut world, 1, &format!("\u{1b}[?1000h{SEED}"));
    let at = cell(&world, 3, 0);
    world.gesture(|app| app.runtime.pointer_move(at.0, at.1));
    accelerator(&mut world, true);
    assert_eq!(world.runtime().cursor(), Cursor::Pointer);
    accelerator(&mut world, false);

    rest_on(&mut world, 3, 0);
    assert!(shown(&world, SEED_CARD));
    opens(&mut world, 3, 0);
    let opened = world.daemon.sent_of("open_seed");
    assert_eq!(opened.len(), 1);
    assert_eq!(
        (&opened[0]["seed_id"], &opened[0]["session_id"]),
        (&json!(SEED), &json!("s1"))
    );
    assert!(!shown(&world, SEED_CARD));
    rest_on(&mut world, 30, 10);
    world.advance(Duration::from_millis(250));
    assert_eq!(inputs(&world), []);
    rests(&mut world);
}

fn on_closed_card() -> World {
    let mut world = start(vec![], vec![mapped()], vec![mapper()]);
    write(&mut world, 1, &format!("from {MAPPER_SHORT}"));
    rest_on(&mut world, 8, 0);
    world
}

#[test]
fn resting_on_a_closed_sessions_id_shows_who_it_was_and_offers_resume_its_ledger_row_and_its_seed()
{
    let mut world = on_closed_card();
    assert_eq!(
        said(&world, "name").as_deref(),
        Some("Map where uploads retry")
    );
    assert_eq!(said(&world, "state").as_deref(), Some("closed 3h"));
    assert_eq!(said(&world, "doing").as_deref(), Some("upload-retry-map"));
    assert_eq!(
        buttons(&world, AGENT_CARD),
        [
            "Resume: Map where uploads retry",
            "Show Map where uploads retry in the ledger",
            "Open seed Map the uploader",
        ]
    );

    takes_keyboard(&mut world);
    letter(&mut world, "s");
    let opened = world.daemon.sent_of("open_seed");
    assert_eq!(opened.len(), 1);
    assert_eq!(opened[0]["seed_id"], SEED);
    assert_eq!(
        opened[0]["session_id"], "s1",
        "beside the agent that names it"
    );
    rests(&mut world);
}

fn resumed(session: &str) -> SessionReopenResultMessage {
    SessionReopenResultMessage {
        success: true,
        result: Some(SessionReopenResult {
            session_id: session.into(),
            action: "reopen".into(),
            ..SessionReopenResult::default()
        }),
        ..SessionReopenResultMessage::new()
    }
}

fn reopens(world: &World) -> Vec<(String, String)> {
    let word = |r: &Request, field: &str| r[field].as_str().unwrap_or_default().to_owned();
    let sent = world.daemon.sent_of("session_reopen");
    sent.iter()
        .map(|r| (word(r, "session_id"), word(r, "action")))
        .collect()
}

#[test]
fn return_and_r_resume_a_closed_agent_from_its_card_once_and_open_it() {
    for press in [Key::Enter, Key::Text("r".into())] {
        let mut world = on_closed_card();
        let held: Arc<Mutex<Vec<Request>>> = hold(&world, "session_reopen");
        takes_keyboard(&mut world);
        key(&mut world, press.clone());
        key(&mut world, press);
        assert_eq!(reopens(&world), [(MAPPER.into(), "reopen".into())]);
        assert_eq!(said(&world, "doing").as_deref(), Some("Resuming…"));

        let request = held.lock().unwrap()[0].clone();
        world.daemon.reply_to(&request, resumed(MAPPER));
        world.settle();
        assert_eq!(sessions_shown(&world), [MAPPER]);
        assert!(!shown(&world, AGENT_CARD));
        rests(&mut world);
    }
}

#[test]
fn a_refused_resume_says_the_ledgers_reason_on_the_card_and_offers_what_the_session_takes_instead()
{
    let mut world = on_closed_card();
    refuse_with(
        &world,
        Some(SessionReopen {
            reason: Some("the worktree /private/tmp/a/b/wt is gone".into()),
            actions: vec!["recreate_worktree_and_reopen".into()],
            profile_id: "work".into(),
            ..SessionReopen::default()
        }),
        "the worktree is gone",
    );
    takes_keyboard(&mut world);
    letter(&mut world, "r");
    assert_eq!(
        said(&world, "doing").as_deref(),
        Some("the worktree …/b/wt is gone")
    );
    assert_eq!(
        buttons(&world, AGENT_CARD)[0],
        "Recreate the worktree: Map where uploads retry"
    );
    assert!(
        world
            .screen()
            .descendants(Target::key(AGENT_CARD))
            .into_iter()
            .filter_map(|n| n.name)
            .any(|words| words == "↵ recreate the worktree")
    );
    key(&mut world, Key::Enter);
    assert_eq!(
        reopens(&world).last(),
        Some(&(MAPPER.into(), "recreate_worktree_and_reopen".into()))
    );

    refuse_with(
        &world,
        Some(SessionReopen {
            reason: Some("its repository is gone".into()),
            profile_id: "work".into(),
            ..SessionReopen::default()
        }),
        "its repository is gone",
    );
    letter(&mut world, "r");
    assert_eq!(
        said(&world, "doing").as_deref(),
        Some("its repository is gone")
    );
    assert_eq!(
        buttons(&world, AGENT_CARD),
        [
            "Show Map where uploads retry in the ledger",
            "Open seed Map the uploader",
        ],
        "a session nothing can resume offers no resume"
    );
    let asks = reopens(&world).len();
    key(&mut world, Key::Enter);
    assert_eq!(reopens(&world).len(), asks);
    key(&mut world, Key::Escape);
    assert!(!shown(&world, AGENT_CARD));
    rests(&mut world);
}

#[test]
fn a_resume_refused_after_its_card_closed_says_so_in_a_notice() {
    let mut world = on_closed_card();
    let held = hold(&world, "session_reopen");
    takes_keyboard(&mut world);
    letter(&mut world, "r");
    key(&mut world, Key::Escape);
    let request = held.lock().unwrap()[0].clone();
    world.daemon.reply_to(
        &request,
        SessionReopenResultMessage {
            error: Some("the outpost is asleep".into()),
            ..SessionReopenResultMessage::new()
        },
    );
    world.settle();
    let screen = world.screen();
    let said: Vec<String> = screen
        .descendants(Target::key("notice.card"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(
        said.iter().any(|w| w.contains("the outpost is asleep")),
        "{said:?}"
    );
}

#[test]
fn a_click_with_the_accelerator_on_a_closed_id_gives_its_card_the_keyboard() {
    let mut world = start(vec![], vec![], vec![mapper()]);
    write(&mut world, 1, MAPPER_SHORT);
    opens(&mut world, 3, 0);
    assert_eq!(
        said(&world, "name").as_deref(),
        Some("Map where uploads retry")
    );
    letter(&mut world, "r");
    assert_eq!(reopens(&world), [(MAPPER.into(), "reopen".into())]);
    assert_eq!(inputs(&world), []);
}

#[test]
fn l_opens_the_ledger_on_the_closed_session_and_leaves_the_saved_filters_alone() {
    let old = mapper();
    let newer = closed(entry("docs", "Docs pass", 30), "user");
    let mut world = start(vec![], vec![], vec![old]);
    world.daemon.on("session_list", move |r| {
        super::ledger::page(r, vec![newer.clone()])
    });
    write(&mut world, 1, MAPPER_SHORT);
    rest_on(&mut world, 3, 0);
    takes_keyboard(&mut world);
    letter(&mut world, "l");
    assert!(!shown(&world, AGENT_CARD));
    assert_eq!(query_text(&world), MAPPER_SHORT);
    assert_eq!(
        rows(&world),
        [MAPPER],
        "a session older than the ledger's first page is there"
    );
    assert_eq!(world.daemon.sent_of("set_setting").len(), 0);

    super::ledger::click(&mut world, "ledger.query.field");
    world.gesture(|app| app.press("mod+a"));
    key(&mut world, Key::Backspace);
    assert_eq!(
        rows(&world),
        ["docs", MAPPER],
        "clearing the query shows the rest"
    );
    assert_eq!(world.daemon.sent_of("set_setting").len(), 0);
}

#[test]
fn the_open_card_of_a_live_agent_becomes_its_closed_card_when_it_closes() {
    let mut world = start(vec![builder()], vec![], vec![]);
    write(&mut world, 1, BUILDER_SHORT);
    rest_on(&mut world, 3, 0);
    takes_keyboard(&mut world);
    closes_builder(&mut world);
    assert_eq!(said(&world, "state").as_deref(), Some("closed 1m"));
    assert_eq!(
        buttons(&world, AGENT_CARD)[0],
        "Resume: Fix the retry uploader"
    );
}

#[test]
fn a_seed_card_takes_the_keyboard_opens_its_seed_with_return_and_closes_with_escape() {
    let mut world = start(vec![], vec![mapped()], vec![]);
    write(&mut world, 1, SEED);
    rest_on(&mut world, 3, 0);
    assert!(shown(&world, SEED_CARD));
    takes_keyboard(&mut world);
    key(&mut world, Key::Escape);
    assert!(!shown(&world, SEED_CARD));

    rest_on(&mut world, 30, 10);
    rest_on(&mut world, 3, 0);
    takes_keyboard(&mut world);
    key(&mut world, Key::Enter);
    let opened = world.daemon.sent_of("open_seed");
    assert_eq!(opened.len(), 1);
    assert_eq!(
        (&opened[0]["seed_id"], &opened[0]["session_id"]),
        (&json!(SEED), &json!("s1"))
    );
    assert!(!shown(&world, SEED_CARD));
    assert_eq!(inputs(&world), []);
    rests(&mut world);
}

#[test]
fn a_popped_out_terminal_shows_the_card_in_its_own_window() {
    let reader = DesktopPane {
        session_id: "s1".into(),
        ..pane("t1", "r1")
    };
    let mut state = showing(vec![reader], "daemon-1");
    state.sessions = Some(vec![agent("s1", "Reader"), builder()]);
    let mut tile = tree::pane("t1");
    tile["window"] = json!({"x": 10, "y": 20, "width": 900, "height": 600});
    state.desktops.as_mut().unwrap()[0].tree_json = tree::row([tile]).to_string();
    let mut world = World::start(Daemon::scripted().state(state));
    world.open_desktop();
    let window = world.tile_windows()[0].key.clone();
    write(&mut world, 1, BUILDER_SHORT);

    world.tile_gesture(&window, |app| {
        let (rect, _) = app.runtime.presented("terminal.r1").expect("r1 is drawn");
        app.runtime.pointer_move(rect[0] + 20.0, rect[1] + 8.0);
    });
    world.advance(Duration::from_millis(200));
    let card = |world: &World| {
        let screen = world.tile_screen(&window);
        screen.find(Target::key(AGENT_CARD)).is_some()
    };
    assert!(card(&world));
    assert!(!shown(&world, AGENT_CARD), "the main window shows no card");

    world.tile_gesture(&window, |app| app.press(into_card()));
    world.tile_gesture(&window, |app| {
        app.key(KeyEvent {
            key: Key::Enter,
            mods: Mods::default(),
        })
    });
    assert_eq!(sessions_shown(&world), [BUILDER]);
    assert!(!card(&world));
}
