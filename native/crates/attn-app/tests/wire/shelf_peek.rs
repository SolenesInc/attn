use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

use attn_protocol::{
    Desktop, DesktopPane, InitialStateMessage, Profile, ProfileArrangementChangedMessage,
    PtyInputProbeResultMessage, Session, SessionStateChangedMessage, TerminalExit,
    TerminalReadTextResultMessage,
};
use attn_testkit::tree::{pane, row, tile};
use attn_testkit::{Daemon, Request, Target, World};
use serde_json::Value;

/// Wide enough for two tiles at the narrowest width, and too narrow for three.
const TWO_FIT: (f32, f32) = (1600.0, 900.0);

fn agent(desktop: &str, id: &str) -> DesktopPane {
    DesktopPane {
        desktop_id: desktop.into(),
        pane_id: id.into(),
        runtime_id: format!("r-{id}"),
        session_id: format!("s-{id}"),
        title: format!("Agent {id}"),
        kind: "agent".into(),
        ..DesktopPane::default()
    }
}

fn session(id: &str) -> Session {
    Session {
        id: format!("s-{id}"),
        label: format!("Agent {id}"),
        directory: format!("/work/{id}"),
        profile_id: "work".into(),
        state: "waiting_input".into(),
        ..Session::default()
    }
}

fn stopped(id: &str) -> Session {
    Session {
        state: "idle".into(),
        terminal_exit: Some(TerminalExit {
            at: "2026-10-07T00:20:04+02:00".into(),
            code: 1,
            ..TerminalExit::default()
        }),
        ..session(id)
    }
}

/// The first desktop, with an agent pane for each pane leaf of `tree` and `a` active.
fn desktop(tree: Value) -> Desktop {
    let id = "work/desktop_1".to_owned();
    let panes = pane_ids(&tree).iter().map(|p| agent(&id, p)).collect();
    Desktop {
        id: id.clone(),
        profile_id: "work".into(),
        name: "Desktop 1".into(),
        shortcut_slot: Some(1),
        order_key: "1".into(),
        tree_json: tree.to_string(),
        active_pane_id: "a".into(),
        panes,
        revision: 1,
    }
}

fn pane_ids(tree: &Value) -> Vec<String> {
    match tree["type"].as_str() {
        Some("pane") => vec![tree["pane_id"].as_str().unwrap().to_owned()],
        Some("split") => tree["children"]
            .as_array()
            .unwrap()
            .iter()
            .flat_map(pane_ids)
            .collect(),
        _ => vec![],
    }
}

fn four_in_a_row() -> Desktop {
    desktop(row([pane("a"), pane("b"), pane("c"), pane("d")]))
}

fn profile() -> Profile {
    Profile {
        id: "work".into(),
        name: "Work".into(),
        current_desktop_id: "work/desktop_1".into(),
        ..Profile::default()
    }
}

/// A daemon holding `desktop` whose agent `c` is `c`, and that shows each terminal's id as
/// its screen and confirms every input.
fn daemon_with(desktop: Desktop, c: Session) -> Daemon {
    let sessions = desktop
        .panes
        .iter()
        .map(|p| {
            if p.pane_id == "c" {
                c.clone()
            } else {
                session(&p.pane_id)
            }
        })
        .collect();
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![profile()]),
        desktops: Some(vec![desktop]),
        sessions: Some(sessions),
        ..InitialStateMessage::new()
    });
    daemon.on("terminal_read_text", |request| {
        attn_testkit::message(TerminalReadTextResultMessage {
            success: true,
            text: Some(format!(
                "screen of {}\n\n",
                request["terminal_id"].as_str().unwrap()
            )),
            ..TerminalReadTextResultMessage::new()
        })
    });
    confirm_input(&daemon, |_| Ok(()));
    daemon
}

/// The daemon confirms each input with what `confirm` answers for its data: success, or the
/// error it carries.
fn confirm_input(daemon: &Daemon, confirm: impl Fn(&str) -> Result<(), String> + Send + 'static) {
    daemon.on("pty_input", move |request| {
        let probe = request["probe_id"].as_str()?;
        let answer = confirm(request["data"].as_str().unwrap());
        Some(attn_testkit::message(PtyInputProbeResultMessage {
            id: request["id"].as_str().unwrap().into(),
            probe_id: probe.into(),
            success: answer.is_ok(),
            error: answer.err(),
            ..PtyInputProbeResultMessage::new()
        }))
    });
}

/// Answers an input the script held, with `error` when it failed.
fn answer(world: &mut World, input: &Request, error: Option<&str>) {
    world.daemon.reply_to(
        input,
        PtyInputProbeResultMessage {
            id: input["id"].as_str().unwrap().into(),
            probe_id: input["probe_id"].as_str().unwrap().into(),
            success: error.is_none(),
            error: error.map(str::to_owned),
            ..PtyInputProbeResultMessage::new()
        },
    );
    world.settle();
}

fn start(daemon: Daemon) -> World {
    let mut world = World::start_sized(daemon, TWO_FIT);
    world.open_desktop();
    world
}

fn peeking() -> World {
    start(daemon_with(four_in_a_row(), session("c")))
}

/// The daemon shows the first desktop as `desktop` now holds it, with `active` active.
fn arrange(world: &mut World, desktop: Desktop, active: &str) {
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile(),
        desktops: vec![Desktop {
            active_pane_id: active.into(),
            ..desktop
        }],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
}

fn session_changes(world: &mut World, session: Session) {
    world.daemon.emit(SessionStateChangedMessage {
        session,
        ..SessionStateChangedMessage::new()
    });
    world.settle();
}

/// The middle of the element `key` names.
fn middle(world: &World, key: &str) -> (f32, f32) {
    let [x, y, w, h] = world
        .runtime()
        .presented(key)
        .unwrap_or_else(|| panic!("{key} is drawn"))
        .0;
    (x + w / 2.0, y + h / 2.0)
}

fn move_to(world: &mut World, at: (f32, f32)) {
    world.gesture(|app| app.move_to(at));
}

fn rest_on(world: &mut World, key: &str) {
    let at = middle(world, key);
    move_to(world, at);
}

/// Rests on the chip of `leaf` until its peek opens.
fn peek_at(world: &mut World, leaf: &str) {
    rest_on(world, &format!("shelf.chip.{leaf}"));
    world.advance(Duration::from_millis(200));
}

/// Moves off the shelf and its peek, onto the middle of tile `a`.
fn move_away(world: &mut World) {
    rest_on(world, "tile.a");
}

fn click(world: &mut World, key: &str) {
    let key = key.to_owned();
    world.gesture(|app| app.click(Target::key(key)));
}

fn key(world: &mut World, key: rune::Key) {
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key,
            mods: rune::Mods::default(),
        })
    });
}

fn peek(world: &World) -> Option<attn_testkit::Node> {
    world.screen().find(Target::key("shelf.peek"))
}

fn peek_screen(world: &World) -> String {
    world.screen().children(Target::key("shelf.peek.screen"))[0]
        .name
        .clone()
        .unwrap_or_default()
}

fn peek_notice(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("shelf.peek.notice"))
        .and_then(|n| n.name)
}

/// What the open peek's reply field to agent `c` holds.
fn field(world: &World) -> String {
    world
        .screen()
        .get(Target::key("shelf.peek.c.field"))
        .value
        .unwrap_or_default()
}

fn reply(world: &mut World, words: &str) {
    let words = words.to_owned();
    world.gesture(|app| {
        app.click(Target::key("shelf.peek.c.field"));
        app.type_text(&words);
    });
}

/// The window's notice, which the app raises without waiting for a peek.
fn notice(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("notice.text"))
        .and_then(|n| n.name)
}

/// The unsent reply on the tile of agent `c`, when it shows one.
fn unsent_on_tile(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("tile.unsent.r-c"))
        .map(|_| {
            world.screen().children(Target::key("tile.unsent.r-c"))[0]
                .name
                .clone()
                .unwrap_or_default()
        })
}

/// What the app typed into the terminal `runtime` for the person, in order.
fn typed(world: &World, runtime: &str) -> Vec<String> {
    world
        .daemon
        .sent_of("pty_input")
        .into_iter()
        .filter(|input| input["id"] == runtime && input["source"] == "user")
        .map(|input| input["data"].as_str().unwrap().to_owned())
        .collect()
}

fn reads(world: &World) -> usize {
    world.daemon.sent_of("terminal_read_text").len()
}

#[test]
fn a_chip_that_arrives_under_a_still_pointer_peeks_only_once_the_pointer_moves() {
    let mut world = peeking();
    let at = middle(&world, "shelf.chip.c");
    arrange(&mut world, desktop(row([pane("a"), pane("b")])), "a");
    move_to(&mut world, at);

    arrange(&mut world, four_in_a_row(), "a");
    let [x, y, w, h] = world.runtime().presented("shelf.chip.c").unwrap().0;
    assert!(
        (x..x + w).contains(&at.0) && (y..y + h).contains(&at.1),
        "the chip came under the pointer"
    );
    world.advance(Duration::from_secs(1));
    assert!(peek(&world).is_none(), "a still pointer peeks at nothing");
    assert_eq!(reads(&world), 0);

    move_to(&mut world, (at.0 + 1.0, at.1));
    world.advance(Duration::from_millis(200));
    assert_eq!(peek(&world).unwrap().name.as_deref(), Some("Agent c"));
    key(&mut world, rune::Key::Escape);
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn moving_on_a_chip_keeps_the_delay_from_the_first_move_and_after_escape_only_coming_back_peeks() {
    let mut world = peeking();
    let at = middle(&world, "shelf.chip.c");

    move_to(&mut world, at);
    world.advance(Duration::from_millis(150));
    move_to(&mut world, (at.0 + 1.0, at.1));
    world.advance(Duration::from_millis(40));
    assert!(peek(&world).is_some(), "the delay ran from the first move");

    key(&mut world, rune::Key::Escape);
    move_to(&mut world, at);
    world.advance(Duration::from_secs(1));
    assert!(peek(&world).is_none(), "the peek stays closed on the chip");

    move_away(&mut world);
    peek_at(&mut world, "c");
    assert_eq!(peek(&world).unwrap().name.as_deref(), Some("Agent c"));
    key(&mut world, rune::Key::Escape);
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_open_peek_reads_its_terminal_every_second_and_a_closed_one_reads_nothing() {
    let daemon = daemon_with(four_in_a_row(), session("c"));
    let count = Arc::new(AtomicUsize::new(0));
    let counted = count.clone();
    daemon.on("terminal_read_text", move |_| {
        let n = counted.fetch_add(1, Ordering::SeqCst) + 1;
        attn_testkit::message(TerminalReadTextResultMessage {
            success: true,
            text: Some(format!("read {n}")),
            ..TerminalReadTextResultMessage::new()
        })
    });
    let mut world = start(daemon);

    peek_at(&mut world, "c");
    assert_eq!(peek_screen(&world), "read 1");
    world.advance(Duration::from_secs(1));
    assert_eq!(peek_screen(&world), "read 2");

    key(&mut world, rune::Key::Escape);
    assert!(peek(&world).is_none());
    let when_closed = reads(&world);
    world.advance(Duration::from_secs(5));
    assert_eq!(reads(&world), when_closed);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_press_outside_closes_the_peek_and_the_pointer_on_the_peek_keeps_it_open() {
    let mut world = peeking();

    peek_at(&mut world, "c");
    click(&mut world, "tile.a");
    assert!(peek(&world).is_none(), "a press outside closes the peek");

    move_away(&mut world);
    peek_at(&mut world, "c");
    move_away(&mut world);
    world.advance(Duration::from_millis(150));
    assert!(
        peek(&world).is_some(),
        "the peek waits a moment after leaving"
    );
    rest_on(&mut world, "shelf.peek");
    world.advance(Duration::from_secs(1));
    assert!(peek(&world).is_some(), "the pointer on the peek keeps it");

    move_away(&mut world);
    world.advance(Duration::from_millis(170));
    assert!(peek(&world).is_none(), "leaving the peek closes it");
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_documents_peek_shows_its_title_and_kind_and_reads_no_terminal() {
    let mut doc = tile("notes", "markdown");
    doc["tile_session_id"] = "s-a".into();
    doc["tile_params"] = r#"{"path":"/work/notes.md"}"#.into();
    let tree = row([pane("a"), pane("b"), pane("c"), doc]);
    let mut world = start(daemon_with(desktop(tree), session("c")));

    peek_at(&mut world, "notes");

    assert_eq!(peek(&world).unwrap().name.as_deref(), Some("notes.md"));
    let shown: Vec<String> = world
        .screen()
        .all(Target::role("label"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(shown.iter().any(|t| t == "Markdown"), "the kind shows");
    assert!(
        world
            .screen()
            .find(Target::key("shelf.peek.notes.field"))
            .is_none()
            && world
                .screen()
                .find(Target::key("shelf.peek.screen"))
                .is_none(),
        "no screen and no reply field"
    );
    assert_eq!(reads(&world), 0);
    key(&mut world, rune::Key::Escape);
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_peek_says_why_it_cannot_read_its_terminal() {
    let daemon = daemon_with(four_in_a_row(), session("c"));
    daemon.on("terminal_read_text", |_| {
        attn_testkit::message(TerminalReadTextResultMessage {
            success: false,
            error: Some("host unreachable".into()),
            ..TerminalReadTextResultMessage::new()
        })
    });
    let mut world = start(daemon);

    peek_at(&mut world, "c");

    assert_eq!(
        peek_screen(&world),
        "Could not read this terminal: host unreachable"
    );
    key(&mut world, rune::Key::Escape);
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_reply_to_a_stopped_agent_stays_unsent_says_why_and_survives_closing() {
    let mut world = start(daemon_with(four_in_a_row(), stopped("c")));

    peek_at(&mut world, "c");
    assert_eq!(
        peek_notice(&world).as_deref(),
        Some("Agent stopped. Open its tile to resume it; your reply stays there.")
    );
    reply(&mut world, "ship it");
    key(&mut world, rune::Key::Enter);

    assert!(typed(&world, "r-c").is_empty());
    assert!(peek(&world).is_some());
    assert_eq!(field(&world), "ship it");

    key(&mut world, rune::Key::Escape);
    move_away(&mut world);
    peek_at(&mut world, "c");
    assert_eq!(field(&world), "ship it", "the reply survives closing");
    key(&mut world, rune::Key::Escape);
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn open_takes_a_reply_to_a_stopped_agent_onto_its_tile_where_discard_ends_it() {
    let mut world = start(daemon_with(four_in_a_row(), stopped("c")));

    peek_at(&mut world, "c");
    reply(&mut world, "ship it");
    click(&mut world, "shelf.peek.open");
    arrange(&mut world, four_in_a_row(), "c");

    assert_eq!(
        unsent_on_tile(&world).as_deref(),
        Some("Unsent reply: ship it")
    );
    click(&mut world, "tile.unsent.r-c.discard");
    assert!(unsent_on_tile(&world).is_none());
    assert!(typed(&world, "r-c").is_empty());
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_refused_reply_is_reported_at_once_and_enter_on_the_reopened_peek_sends_it_whole() {
    let mut world = peeking();
    confirm_input(&world.daemon, |_| {
        Err("studio is unreachable; this needs it now".to_owned())
    });

    peek_at(&mut world, "c");
    reply(&mut world, "ship it");
    key(&mut world, rune::Key::Enter);
    assert_eq!(typed(&world, "r-c"), ["ship it"]);
    let reported = notice(&world).unwrap_or_default();
    assert!(
        reported.contains("may not have arrived") && reported.contains("studio is unreachable"),
        "{reported}"
    );

    confirm_input(&world.daemon, |_| Ok(()));
    move_away(&mut world);
    peek_at(&mut world, "c");
    assert!(
        peek_notice(&world)
            .unwrap_or_default()
            .contains("studio is unreachable")
    );
    assert_eq!(field(&world), "ship it");
    world.gesture(|app| app.click(Target::key("shelf.peek.c.field")));
    key(&mut world, rune::Key::Enter);
    assert_eq!(typed(&world, "r-c"), ["ship it", "ship it", "\r"]);

    move_away(&mut world);
    peek_at(&mut world, "c");
    assert_eq!(field(&world), "");
    key(&mut world, rune::Key::Escape);
    world.advance(Duration::from_secs(7));
    assert!(world.runtime_is_at_rest());
}

/// Replies to agent `c` with a reply whose text lands and whose Return fails for `reason`.
/// With `stop_first`, the agent stops before the failure arrives. Later inputs succeed.
fn reply_whose_return_fails(world: &mut World, reason: &str, stop_first: bool) {
    world.daemon.on("pty_input", |request| {
        let probe = request["probe_id"].as_str()?;
        (request["data"] != "\r").then(|| {
            attn_testkit::message(PtyInputProbeResultMessage {
                id: request["id"].as_str().unwrap().into(),
                probe_id: probe.into(),
                success: true,
                ..PtyInputProbeResultMessage::new()
            })
        })
    });
    peek_at(world, "c");
    reply(world, "ship it");
    key(world, rune::Key::Enter);
    let enter = world.daemon.sent_of("pty_input").pop().unwrap();
    assert_eq!(enter["data"], "\r");
    confirm_input(&world.daemon, |_| Ok(()));
    if stop_first {
        session_changes(world, stopped("c"));
    }
    answer(world, &enter, Some(reason));
    assert!(
        notice(world)
            .unwrap_or_default()
            .contains("typed but not submitted"),
        "{:?}",
        notice(world)
    );
}

fn open_keeps_the_reply_on_the_tile_of_an_agent_that_stopped(stop_first: bool) {
    let mut world = peeking();
    reply_whose_return_fails(&mut world, "session not running", stop_first);
    if !stop_first {
        session_changes(&mut world, stopped("c"));
    }

    move_away(&mut world);
    peek_at(&mut world, "c");
    assert!(
        peek_notice(&world)
            .unwrap_or_default()
            .contains("your reply stays there")
    );
    click(&mut world, "shelf.peek.open");
    arrange(&mut world, four_in_a_row(), "c");

    assert_eq!(
        unsent_on_tile(&world).as_deref(),
        Some("Unsent reply: ship it")
    );
    assert_eq!(typed(&world, "r-c"), ["ship it", "\r"]);
    click(&mut world, "tile.unsent.r-c.discard");
    assert!(unsent_on_tile(&world).is_none());
    world.advance(Duration::from_secs(7));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn open_keeps_a_reply_whose_return_failed_on_the_tile_of_an_agent_that_stopped_after() {
    open_keeps_the_reply_on_the_tile_of_an_agent_that_stopped(false);
}

#[test]
fn open_keeps_a_reply_whose_return_failed_on_the_tile_of_an_agent_that_stopped_before_the_failure_arrived()
 {
    open_keeps_the_reply_on_the_tile_of_an_agent_that_stopped(true);
}

#[test]
fn the_tile_of_a_stopped_agent_whose_return_failed_offers_the_reply_when_reached_from_its_chip() {
    let mut world = peeking();
    reply_whose_return_fails(&mut world, "session not running", false);
    session_changes(&mut world, stopped("c"));

    click(&mut world, "shelf.chip.c");
    arrange(&mut world, four_in_a_row(), "c");

    assert!(peek(&world).is_none());
    assert_eq!(
        unsent_on_tile(&world).as_deref(),
        Some("Unsent reply: ship it")
    );
    world.advance(Duration::from_secs(7));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn after_a_resume_the_peek_offers_the_whole_reply_and_enter_sends_it_to_the_new_process() {
    let mut world = peeking();
    reply_whose_return_fails(&mut world, "session not running", false);
    session_changes(&mut world, stopped("c"));
    session_changes(&mut world, session("c"));

    move_away(&mut world);
    peek_at(&mut world, "c");
    assert_eq!(typed(&world, "r-c"), ["ship it", "\r"], "nothing more yet");
    assert!(
        peek_notice(&world)
            .unwrap_or_default()
            .contains("may not have arrived: session not running. Check the screen")
    );
    assert_eq!(field(&world), "ship it");
    world.gesture(|app| {
        app.click(Target::key("shelf.peek.c.field"));
        app.type_text("!");
    });
    assert_eq!(field(&world), "ship it!", "the reply can be edited");
    key(&mut world, rune::Key::Backspace);

    key(&mut world, rune::Key::Enter);
    assert_eq!(typed(&world, "r-c"), ["ship it", "\r", "ship it", "\r"]);
    move_away(&mut world);
    peek_at(&mut world, "c");
    assert_eq!(field(&world), "");
    key(&mut world, rune::Key::Escape);
    world.advance(Duration::from_secs(7));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn open_leaves_a_reply_whose_return_failed_to_the_live_terminal_that_holds_its_text() {
    let mut world = peeking();
    reply_whose_return_fails(&mut world, "the terminal did not answer", false);

    move_away(&mut world);
    peek_at(&mut world, "c");
    click(&mut world, "shelf.peek.open");
    arrange(&mut world, four_in_a_row(), "c");

    assert!(unsent_on_tile(&world).is_none());
    assert_eq!(typed(&world, "r-c"), ["ship it", "\r"]);
    world.advance(Duration::from_secs(7));
    assert!(world.runtime_is_at_rest());
}

/// A world whose reply to agent `c` landed and whose Return failed once, peeking at `c`
/// again with the keyboard in its reply field.
fn repeeking_a_reply_whose_return_failed() -> World {
    let mut world = peeking();
    let returns = Arc::new(AtomicUsize::new(0));
    let counted = returns.clone();
    confirm_input(&world.daemon, move |data| match data {
        "\r" if counted.fetch_add(1, Ordering::SeqCst) == 0 => {
            Err("the bridge ended before the answer".to_owned())
        }
        _ => Ok(()),
    });
    peek_at(&mut world, "c");
    reply(&mut world, "ship it");
    key(&mut world, rune::Key::Enter);
    assert!(
        notice(&world)
            .unwrap_or_default()
            .contains("typed but not submitted")
    );
    move_away(&mut world);
    peek_at(&mut world, "c");
    world.gesture(|app| app.click(Target::key("shelf.peek.c.field")));
    world
}

#[test]
fn after_the_text_lands_and_return_fails_enter_on_the_reopened_peek_sends_only_return() {
    let mut world = repeeking_a_reply_whose_return_failed();
    assert!(
        peek_notice(&world)
            .unwrap_or_default()
            .contains("↵ sends Return")
    );
    assert_eq!(field(&world), "ship it");

    key(&mut world, rune::Key::Enter);
    assert_eq!(typed(&world, "r-c"), ["ship it", "\r", "\r"]);

    move_away(&mut world);
    peek_at(&mut world, "c");
    assert_eq!(field(&world), "");
    key(&mut world, rune::Key::Escape);
    world.advance(Duration::from_secs(7));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_field_of_a_reply_whose_return_failed_ignores_typing_and_keeps_the_keyboard() {
    let mut world = repeeking_a_reply_whose_return_failed();

    world.gesture(|app| app.type_text("!"));
    assert_eq!(field(&world), "ship it", "the typed text cannot change");
    key(&mut world, rune::Key::Enter);

    assert_eq!(typed(&world, "r-c"), ["ship it", "\r", "\r"]);
    assert!(typed(&world, "r-a").is_empty());
    world.advance(Duration::from_secs(7));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn enter_sends_nothing_more_while_a_reply_is_unanswered_and_a_dropped_daemon_leaves_it_unsent() {
    let mut world = peeking();
    world.daemon.on("pty_input", |_| ());

    peek_at(&mut world, "c");
    reply(&mut world, "ship it");
    key(&mut world, rune::Key::Enter);
    assert!(peek(&world).is_none());

    move_away(&mut world);
    peek_at(&mut world, "c");
    assert_eq!(peek_notice(&world).as_deref(), Some("Sending…"));
    world.gesture(|app| app.click(Target::key("shelf.peek.c.field")));
    key(&mut world, rune::Key::Enter);
    assert_eq!(typed(&world, "r-c"), ["ship it"]);

    world.reconnect();
    assert!(
        peek_notice(&world)
            .unwrap_or_default()
            .contains("the daemon did not answer"),
        "{:?}",
        peek_notice(&world)
    );
    assert_eq!(field(&world), "ship it");
    world.advance(Duration::from_secs(10));
    assert_eq!(
        typed(&world, "r-c"),
        ["ship it"],
        "reconnecting resends nothing"
    );
    key(&mut world, rune::Key::Escape);
    world.advance(Duration::from_secs(7));
    assert!(world.runtime_is_at_rest());
}
