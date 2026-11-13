use std::time::Duration;

use attn_protocol::{
    AttachResultMessage, AttachSnapshot, Desktop, DesktopPane, InitialStateMessage, Profile,
    ProfileArrangementChangedMessage, PtyDesyncMessage, PtyResizedMessage, RuntimeRespawnedMessage,
    SessionExitedMessage,
};
use attn_term::{FONT_SIZE, Fonts, SNAPSHOT_FORMAT, Vt};
use attn_testkit::{Answer, Daemon, World, message, tree};
use base64::Engine;
use rune::{Ime, Key, KeyEvent, Mods, PhysicalInput, PointerButton, WheelEvent};

pub(crate) fn pane(id: &str, runtime: &str) -> DesktopPane {
    DesktopPane {
        desktop_id: "d1".into(),
        pane_id: id.into(),
        runtime_id: runtime.into(),
        title: format!("Agent {id}"),
        kind: "agent".into(),
        ..DesktopPane::default()
    }
}

pub(crate) fn desktop(panes: Vec<DesktopPane>) -> Desktop {
    Desktop {
        id: "d1".into(),
        name: "d1".into(),
        active_pane_id: panes.first().map(|p| p.pane_id.clone()).unwrap_or_default(),
        tree_json: tree::panes_in_a_row(
            &panes.iter().map(|p| p.pane_id.as_str()).collect::<Vec<_>>(),
        ),
        panes,
        profile_id: "work".into(),
        ..Desktop::default()
    }
}

pub(crate) fn profile() -> Profile {
    Profile {
        id: "work".into(),
        name: "Work".into(),
        current_desktop_id: "d1".into(),
        ..Profile::default()
    }
}

pub(crate) fn showing(panes: Vec<DesktopPane>, daemon_instance: &str) -> InitialStateMessage {
    InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![profile()]),
        desktops: Some(vec![desktop(panes)]),
        daemon_instance_id: Some(daemon_instance.into()),
        ..InitialStateMessage::new()
    }
}

pub(crate) fn one_terminal() -> Daemon {
    Daemon::scripted().state(showing(vec![pane("t1", "r1")], "daemon-1"))
}

pub(crate) fn output(world: &World, seq: u32, data: &[u8]) {
    let mut frame = vec![0x01, 2];
    frame.extend_from_slice(b"r1");
    frame.extend_from_slice(&seq.to_be_bytes());
    frame.extend_from_slice(data);
    world.daemon.emit_frame(frame);
}

pub(crate) fn attached() -> AttachResultMessage {
    AttachResultMessage {
        id: "r1".into(),
        success: true,
        running: Some(true),
        ..AttachResultMessage::new()
    }
}

pub(crate) fn snapshot_of(text: &[u8], format: &str) -> AttachSnapshot {
    let mut vt = Vt::new(30, 5);
    vt.write(text);
    let bytes = vt.snapshot().expect("a screen at rest encodes");
    AttachSnapshot {
        cols: 30,
        rows: 5,
        format: Some(format.into()),
        snapshot_b64: base64::engine::general_purpose::STANDARD.encode(bytes),
        ..AttachSnapshot::default()
    }
}

/// What the app sent each terminal, with who produced it.
pub(crate) fn inputs(world: &World) -> Vec<(String, String)> {
    world
        .daemon
        .sent_of("pty_input")
        .iter()
        .map(|r| {
            (
                r["data"].as_str().unwrap().to_owned(),
                r["source"].as_str().unwrap_or_default().to_owned(),
            )
        })
        .collect()
}

pub(crate) fn typed(data: &str) -> (String, String) {
    (data.into(), "user".into())
}

fn sizes(world: &World) -> Vec<(i64, i64)> {
    world
        .daemon
        .sent_of("pty_resize")
        .iter()
        .map(|r| (r["cols"].as_i64().unwrap(), r["rows"].as_i64().unwrap()))
        .collect()
}

fn terminal_cmds(world: &World) -> Vec<String> {
    world
        .daemon
        .sent()
        .iter()
        .filter_map(|r| r["cmd"].as_str())
        .filter(|c| ["pty_resize", "attach_session", "detach_session"].contains(c))
        .map(str::to_owned)
        .collect()
}

pub(crate) fn key(key: Key, mods: Mods) -> KeyEvent {
    KeyEvent { key, mods }
}

pub(crate) fn row(world: &mut World, y: usize) -> String {
    world.terminal_text("r1")[y].clone()
}

#[test]
fn a_drawn_terminal_sizes_its_pty_to_its_tile_then_attaches_and_shows_its_output() {
    let mut world = World::start(one_terminal());
    world.open_desktop();

    assert_eq!(terminal_cmds(&world), ["pty_resize", "attach_session"]);
    let resize = &world.daemon.sent_of("pty_resize")[0];
    let (cols, rows) = (
        resize["cols"].as_i64().unwrap(),
        resize["rows"].as_i64().unwrap(),
    );
    assert!(
        cols > 20 && rows > 10,
        "a tile's grid, not a default: {cols}x{rows}"
    );
    assert!(resize["xpixel"].as_i64().unwrap() >= cols, "{resize}");
    let attach = &world.daemon.sent_of("attach_session")[0];
    assert_eq!(attach["id"], "r1");
    assert_eq!(attach["attach_policy"], "same_app_remount");
    assert!(
        attach.get("cols").is_none(),
        "only a revive carries its size"
    );

    output(&world, 1, b"hello\r\nworld");
    world.settle();

    assert_eq!(world.terminal_text("r1")[..2], ["hello", "world"]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_snapshot_in_this_builds_format_restores_the_screen_and_the_stream_continues_after_it() {
    let daemon = one_terminal();
    daemon.on("attach_session", |request| {
        message(AttachResultMessage {
            id: request["id"].as_str().unwrap().into(),
            last_seq: Some(5),
            snapshot: Some(snapshot_of(b"restored", SNAPSHOT_FORMAT)),
            ..attached()
        })
    });
    daemon.on("pty_resize", |_| ());
    let mut world = World::start(daemon);
    world.open_desktop();
    assert_eq!(row(&mut world, 0), "restored");
    assert_eq!(world.terminal_size("r1"), (30, 5));

    output(&world, 5, b" again");
    output(&world, 6, b"!");
    world.settle();

    assert_eq!(row(&mut world, 0), "restored!");
}

#[test]
fn a_snapshot_in_another_format_is_refused_and_output_draws_from_the_start() {
    let daemon = one_terminal();
    daemon.on("attach_session", |request| {
        message(AttachResultMessage {
            id: request["id"].as_str().unwrap().into(),
            last_seq: Some(5),
            snapshot: Some(snapshot_of(b"restored", "a-format-from-another-build")),
            ..attached()
        })
    });
    let mut world = World::start(daemon);
    world.open_desktop();
    assert_eq!(row(&mut world, 0), "");

    output(&world, 1, b"live");
    world.settle();

    assert_eq!(row(&mut world, 0), "live");
}

#[test]
fn a_snapshot_that_does_not_decode_keeps_the_output_that_raced_it_and_the_stream_continues() {
    let daemon = one_terminal();
    daemon.answer_in_turn("attach_session", [Answer::Hold]);
    let mut world = World::start(daemon);
    world.open_desktop();

    output(&world, 9, b"raced\r\n");
    world.settle();
    let request = world.daemon.sent_of("attach_session")[0].clone();
    let garbage = AttachSnapshot {
        snapshot_b64: base64::engine::general_purpose::STANDARD.encode([1, 2, 3, 4]),
        ..snapshot_of(b"", SNAPSHOT_FORMAT)
    };
    world.daemon.reply_to(
        &request,
        AttachResultMessage {
            last_seq: Some(10),
            snapshot: Some(garbage),
            ..attached()
        },
    );
    world.settle();
    output(&world, 11, b"live");
    world.settle();

    assert_eq!(world.terminal_text("r1")[..2], ["raced", "live"]);
    assert_eq!(world.daemon.sent_of("attach_session").len(), 1);
}

#[test]
fn output_during_an_attach_waits_for_it_and_output_already_drawn_is_dropped() {
    let daemon = one_terminal();
    daemon.answer_in_turn("attach_session", [Answer::Hold]);
    let mut world = World::start(daemon);
    world.open_desktop();

    output(&world, 1, b"a");
    output(&world, 2, b"b");
    world.settle();
    assert_eq!(
        row(&mut world, 0),
        "",
        "nothing draws before the attach answers"
    );

    let request = world.daemon.sent_of("attach_session")[0].clone();
    world.daemon.reply_to(&request, attached());
    world.settle();
    output(&world, 2, b"X");
    output(&world, 3, b"c");
    world.settle();

    assert_eq!(row(&mut world, 0), "abc");
}

#[test]
fn typing_during_an_attach_reaches_the_pty_once_it_attaches() {
    let daemon = one_terminal();
    daemon.answer_in_turn("attach_session", [Answer::Hold]);
    let mut world = World::start(daemon);
    world.open_desktop();

    world.gesture(|app| app.type_text("ls"));
    assert_eq!(inputs(&world), []);

    let request = world.daemon.sent_of("attach_session")[0].clone();
    world.daemon.reply_to(&request, attached());
    world.settle();

    assert_eq!(inputs(&world), [typed("l"), typed("s")]);
}

#[test]
fn keys_reach_the_pty_encoded_for_the_modes_the_program_set() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    let alt = Mods {
        alt: true,
        ..Mods::default()
    };
    let shift = Mods {
        shift: true,
        ..Mods::default()
    };

    world.gesture(|app| app.key(key(Key::Up, Mods::default())));
    output(&world, 1, b"\x1b[?1h");
    world.settle();
    world.gesture(|app| {
        app.key(key(Key::Up, Mods::default()));
        app.key(key(Key::Text("b".into()), alt));
        app.key(key(Key::Tab, shift));
        app.key(key(Key::Enter, shift));
    });

    assert_eq!(
        inputs(&world),
        [
            typed("\x1b[A"),
            typed("\x1bOA"),
            typed("\x1bb"),
            typed("\x1b[Z"),
            typed("\n"),
        ]
    );
}

#[test]
fn a_paste_is_bracketed_only_when_the_program_asked() {
    let mut world = World::start(one_terminal());
    world.open_desktop();

    world.gesture(|app| app.runtime.paste("one\ntwo"));
    output(&world, 1, b"\x1b[?2004h");
    world.settle();
    world.gesture(|app| app.runtime.paste("three\r\nfour"));

    assert_eq!(
        inputs(&world),
        [typed("one\rtwo"), typed("\x1b[200~three\rfour\x1b[201~")]
    );
}

#[test]
fn an_input_method_commit_reaches_the_pty() {
    let mut world = World::start(one_terminal());
    world.open_desktop();

    world.gesture(|app| {
        app.runtime.ime(Ime::Preedit("´".into(), None));
        app.runtime.ime(Ime::Commit("é".into()));
        app.runtime.ime(Ime::Preedit("にほん".into(), None));
        app.runtime.ime(Ime::Commit("日本".into()));
    });

    assert_eq!(inputs(&world), [typed("é"), typed("日本")]);
}

#[test]
fn an_input_method_composes_at_the_cursor_and_opens_its_candidates_there() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    let starts_at = |rect: [f32; 4], (x, y): (f32, f32)| {
        let metrics = Fonts::default().metrics(FONT_SIZE);
        (rect[0] - (x - metrics.cell_w / 2.0)).abs() < 0.5
            && (rect[1] - (y - metrics.cell_h / 2.0)).abs() < 0.5
    };
    let candidates = |world: &World| world.runtime().ime_area.expect("the input method is on");
    let composition = |world: &World| {
        world
            .runtime()
            .presented("terminal.r1.preedit")
            .expect("the composition is drawn")
            .0
    };

    output(&world, 1, b"$ ");
    world.settle();
    let area = candidates(&world);
    assert!(
        starts_at(area, cell(&world, 2, 0)),
        "the first composition opens its candidates where output left the cursor: {area:?}"
    );

    world.gesture(|app| app.runtime.ime(Ime::Preedit("にほn".into(), None)));
    assert!(inputs(&world).is_empty(), "a composition sends nothing");
    let drawn = composition(&world);
    assert!(
        starts_at(drawn, cell(&world, 2, 0)),
        "drawn at the cursor: {drawn:?}"
    );

    output(&world, 2, b"\x1b[?25l");
    world.settle();
    output(&world, 3, b"\x1b[1;4H");
    world.settle();
    let (area, drawn) = (candidates(&world), composition(&world));
    assert!(
        starts_at(area, cell(&world, 3, 0)) && starts_at(drawn, cell(&world, 3, 0)),
        "the caret and the composition follow the cursor, shown or hidden: {area:?} {drawn:?}"
    );

    world.gesture(|app| app.runtime.ime(Ime::Commit("日本".into())));
    assert_eq!(inputs(&world), [typed("日本")]);
    assert!(
        world.runtime().presented("terminal.r1.preedit").is_none(),
        "a commit ends the composition"
    );
}

#[test]
fn a_composition_that_ends_while_a_field_has_focus_is_not_drawn_when_the_terminal_returns() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"$ ");
    world.settle();
    world.gesture(|app| app.runtime.ime(Ime::Preedit("にほ".into(), None)));
    assert!(world.runtime().presented("terminal.r1.preedit").is_some());

    world
        .automate("run", serde_json::json!({"name": "ui.commandPalette"}))
        .unwrap();
    world.gesture(|app| app.runtime.ime(Ime::Commit("日本".into())));
    world
        .automate("run", serde_json::json!({"name": "ui.commandPalette"}))
        .unwrap();

    assert!(
        world.runtime().presented("terminal.r1.preedit").is_none(),
        "the composition ended while the palette had focus"
    );
    assert!(
        inputs(&world).is_empty(),
        "the commit went to the palette's field"
    );
}

#[test]
fn the_terminal_answers_its_programs_queries_except_those_the_daemon_answers() {
    let mut world = World::start(one_terminal());
    world.open_desktop();

    output(&world, 1, b"\x1b[5n\x1b[6n\x1b[c");
    world.settle();

    assert_eq!(inputs(&world), [("\x1b[0n".into(), "response".into())]);
}

#[test]
fn the_screen_takes_the_size_the_daemon_gives_the_pty_without_answering_it() {
    let mut world = World::start(one_terminal());
    let asked = sizes(&world);

    world.daemon.emit(PtyResizedMessage {
        id: "r1".into(),
        cols: 50,
        rows: 12,
        ..PtyResizedMessage::new()
    });
    world.settle();

    assert_eq!(world.terminal_size("r1"), (50, 12));
    assert_eq!(
        sizes(&world),
        asked,
        "the daemon owns the size it settled on"
    );
}

#[test]
fn resizing_the_tile_tells_the_daemon_at_most_every_quarter_second() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    let first = sizes(&world)[0];

    world.gesture(|app| app.runtime.set_viewport((800.0, 600.0), 2.0));
    world.gesture(|app| app.runtime.set_viewport((700.0, 500.0), 2.0));
    let during = sizes(&world);
    world.advance(Duration::from_millis(250));
    let after = sizes(&world);

    assert_eq!(
        during.len(),
        2,
        "the first new size goes at once: {during:?}"
    );
    assert_ne!(during[1], first);
    assert_eq!(
        after.len(),
        3,
        "the last size follows the interval: {after:?}"
    );
    assert!(after[2].0 < during[1].0 && after[2].1 < during[1].1);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn pty_output_draws_without_running_the_view() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    let commits = world.runtime().stats.commits;

    for seq in 1..=50 {
        output(&world, seq, format!("line {seq}\r\n").as_bytes());
    }
    world.settle();

    assert!(world.terminal_text("r1").iter().any(|r| r == "line 50"));
    assert_eq!(world.runtime().stats.commits, commits);
}

fn drawn(world: &mut World) -> Vec<String> {
    let rows = world
        .automate("state", serde_json::json!({"of": "terminal:r1"}))
        .expect("the window drew terminal r1");
    serde_json::from_value(rows).expect("rows of text")
}

fn drew(world: &mut World, text: &str) -> bool {
    drawn(world).iter().any(|row| row.contains(text))
}

#[test]
fn a_synchronized_update_draws_once_the_program_ends_it() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"$ watch\r\n");
    world.settle();

    output(&world, 2, b"\x1b[?2026hcounter 1\r\n");
    world.settle();
    world.advance(Duration::from_millis(500));

    assert!(drew(&mut world, "$ watch"), "{:?}", drawn(&mut world));
    assert!(
        !drew(&mut world, "counter 1"),
        "nothing of the update shows while it lasts: {:?}",
        drawn(&mut world)
    );
    output(&world, 3, b"$ \x1b[?2026l");
    world.settle();

    let rows = drawn(&mut world);
    assert_eq!(rows[..3], ["$ watch", "counter 1", "$"], "{rows:?}");
    world.advance(Duration::from_millis(500));
    assert_eq!(drawn(&mut world), rows);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_synchronized_update_the_program_never_ends_draws_after_a_second() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"\x1b[?2026hstuck");
    world.settle();

    world.advance(Duration::from_millis(999));
    assert!(!drew(&mut world, "stuck"), "{:?}", drawn(&mut world));
    world.advance(Duration::from_millis(1));
    assert!(drew(&mut world, "stuck"), "{:?}", drawn(&mut world));

    output(&world, 2, b" more");
    world.settle();
    assert!(
        drew(&mut world, "stuck more"),
        "output after the limit draws at once: {:?}",
        drawn(&mut world)
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_failed_revive_says_why_on_the_screen_and_is_not_asked_again() {
    let daemon = recoverable();
    daemon.on("attach_session", |request| {
        message(AttachResultMessage {
            id: request["id"].as_str().unwrap().into(),
            success: false,
            error: Some("runtime not found".into()),
            ..AttachResultMessage::new()
        })
    });
    let mut world = World::start(daemon);
    world.open_desktop();
    world.advance(Duration::from_secs(5));

    assert_eq!(
        row(&mut world, 1),
        "[Failed to attach PTY: runtime not found]"
    );
    assert_eq!(policies(&world), ["revive"]);
}

#[test]
fn an_ended_process_leaves_its_notice_on_the_screen() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"$ exit");

    world.daemon.emit(SessionExitedMessage {
        id: "r1".into(),
        session_id: "s1".into(),
        exit_code: 2,
        ..SessionExitedMessage::new()
    });
    world.settle();
    world.daemon.emit(SessionExitedMessage {
        id: "r1".into(),
        session_id: "s1".into(),
        exit_code: 143,
        ..SessionExitedMessage::new()
    });
    world.settle();

    assert_eq!(
        world.terminal_text("r1")[..4],
        [
            "$ exit",
            "[Process exited with code 2]",
            "",
            "[Session ended]"
        ]
    );
}

#[test]
fn a_replaced_stream_starts_the_screen_over_from_a_new_attach() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 7, b"old");
    world.settle();

    world.daemon.emit(RuntimeRespawnedMessage {
        id: "r1".into(),
        ..RuntimeRespawnedMessage::new()
    });
    world.settle();
    assert_eq!(row(&mut world, 0), "");
    output(&world, 1, b"new");
    world.settle();
    world.daemon.emit(PtyDesyncMessage {
        id: "r1".into(),
        reason: "gap".into(),
        ..PtyDesyncMessage::new()
    });
    world.settle();

    assert_eq!(
        policies(&world),
        ["same_app_remount", "relaunch_restore", "none"]
    );
    assert_eq!(row(&mut world, 0), "");
}

#[test]
fn a_reconnect_attaches_the_drawn_terminals_again() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 3, b"before");
    world.settle();

    world.reconnect();
    output(&world, 3, b"X");
    output(&world, 4, b"!");
    world.settle();

    assert_eq!(world.daemon.sent_of("attach_session").len(), 2);
    assert_eq!(
        row(&mut world, 0),
        "before!",
        "the stream continues from what was drawn"
    );
}

#[test]
fn a_new_daemon_starts_every_stream_over() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 3, b"before");
    world.settle();

    world
        .daemon
        .answer_hello_with(showing(vec![pane("t1", "r1")], "daemon-2"));
    world.reconnect();
    output(&world, 1, b" after");
    world.settle();

    assert_eq!(row(&mut world, 0), "before after");
}

#[test]
fn a_terminal_no_longer_drawn_is_detached() {
    let mut world = World::start(one_terminal());
    world.open_desktop();

    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile(),
        desktops: vec![desktop(vec![pane("t2", "r2")])],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();

    let detached = world.daemon.sent_of("detach_session");
    assert_eq!(detached.len(), 1);
    assert_eq!(detached[0]["id"], "r1");
    assert_eq!(world.daemon.sent_of("attach_session")[1]["id"], "r2");
}

#[test]
fn the_daemon_hears_which_terminal_has_the_keyboard() {
    let world = World::start(Daemon::scripted().state(showing(
        vec![pane("t1", "r1"), pane("t2", "r2")],
        "daemon-1",
    )));

    let focused: Vec<_> = world
        .daemon
        .sent_of("terminal_focus")
        .iter()
        .map(|r| r["id"].as_str().unwrap().to_owned())
        .collect();
    assert_eq!(focused, ["r1"]);
}

#[test]
fn a_daemon_attaching_at_another_size_is_told_the_tiles_size_again_without_pixels() {
    let daemon = one_terminal();
    daemon.on("attach_session", |request| {
        message(AttachResultMessage {
            id: request["id"].as_str().unwrap().into(),
            cols: Some(120),
            rows: Some(40),
            ..attached()
        })
    });
    let mut world = World::start(daemon);
    world.open_desktop();

    let resizes = world.daemon.sent_of("pty_resize");
    assert_eq!(resizes.len(), 2, "{resizes:?}");
    assert_eq!(
        (&resizes[1]["cols"], &resizes[1]["rows"]),
        (&resizes[0]["cols"], &resizes[0]["rows"])
    );
    assert!(resizes[0].get("xpixel").is_some());
    assert!(resizes[1].get("xpixel").is_none() && resizes[1].get("ypixel").is_none());
}

#[test]
fn a_tile_resized_during_the_attach_sends_only_its_latest_size_once_attached() {
    let daemon = one_terminal();
    daemon.answer_in_turn("attach_session", [Answer::Hold]);
    let mut world = World::start(daemon);
    world.open_desktop();

    world.gesture(|app| app.runtime.set_viewport((800.0, 600.0), 2.0));
    world.gesture(|app| app.runtime.set_viewport((700.0, 500.0), 2.0));
    assert_eq!(sizes(&world).len(), 1, "held while the attach is in flight");

    let first = sizes(&world)[0];
    let request = world.daemon.sent_of("attach_session")[0].clone();
    world.daemon.reply_to(
        &request,
        AttachResultMessage {
            cols: Some(first.0),
            rows: Some(first.1),
            ..attached()
        },
    );
    world.settle();

    let after = sizes(&world);
    assert_eq!(after.len(), 2, "{after:?}");
    assert!(after[1].0 < first.0 && after[1].1 < first.1);
}

#[test]
fn a_tile_laid_out_to_nothing_leaves_the_pty_its_size() {
    let mut world = World::start(one_terminal());
    let before = sizes(&world);

    world.gesture(|app| app.runtime.set_viewport((0.0, 0.0), 2.0));
    world.advance(Duration::from_secs(1));

    assert_eq!(sizes(&world), before);
}

#[test]
fn taking_the_keyboard_claims_the_ptys_size_for_this_tile() {
    let mut world = World::start_sized(
        Daemon::scripted().state(showing(
            vec![pane("t1", "r1"), pane("t2", "r2")],
            "daemon-1",
        )),
        (1600.0, 900.0),
    );
    world.open_desktop();
    world.daemon.emit(PtyResizedMessage {
        id: "r2".into(),
        cols: 200,
        rows: 50,
        ..PtyResizedMessage::new()
    });
    world.settle();
    let before = world.daemon.sent_of("pty_resize").len();

    world.gesture(|app| app.click(attn_testkit::Target::role("group").name("Agent t2")));

    let resizes = world.daemon.sent_of("pty_resize");
    assert_eq!(resizes.len(), before + 1);
    assert_eq!(resizes[before]["id"], "r2");
    assert_eq!(
        world.daemon.sent_of("terminal_focus").last().unwrap()["id"],
        "r2"
    );
}

#[test]
fn an_attach_refused_while_the_worker_restarts_is_asked_again() {
    let daemon = one_terminal();
    let refused = AttachResultMessage {
        id: "r1".into(),
        success: false,
        error: Some("dial unix /tmp/worker.sock: connect: no such file or directory".into()),
        ..AttachResultMessage::new()
    };
    daemon.answer_in_turn(
        "attach_session",
        [
            Answer::Reply(message(refused.clone())),
            Answer::Reply(message(attached())),
        ],
    );
    let mut world = World::start(daemon);
    world.open_desktop();
    assert_eq!(world.daemon.sent_of("attach_session").len(), 1);

    world.advance(Duration::from_millis(150));
    output(&world, 1, b"up");
    world.settle();

    assert_eq!(world.daemon.sent_of("attach_session").len(), 2);
    assert_eq!(row(&mut world, 0), "up");
}

fn recoverable() -> Daemon {
    Daemon::scripted().state(session_in("recoverable"))
}

fn session_in(state_name: &str) -> InitialStateMessage {
    let mut state = showing(
        vec![DesktopPane {
            session_id: "s1".into(),
            ..pane("t1", "r1")
        }],
        "daemon-1",
    );
    state.sessions = Some(vec![attn_protocol::Session {
        id: "s1".into(),
        state: state_name.into(),
        ..attn_protocol::Session::default()
    }]);
    state
}

fn policies(world: &World) -> Vec<String> {
    world
        .daemon
        .sent_of("attach_session")
        .iter()
        .map(|r| r["attach_policy"].as_str().unwrap_or("none").to_owned())
        .collect()
}

fn respawn(world: &mut World) {
    world.daemon.emit(RuntimeRespawnedMessage {
        id: "r1".into(),
        ..RuntimeRespawnedMessage::new()
    });
    world.settle();
}

#[test]
fn a_terminal_whose_attach_failed_comes_back_when_its_runtime_is_replaced() {
    let daemon = one_terminal();
    let failed = AttachResultMessage {
        id: "r1".into(),
        success: false,
        error: Some("runtime not found".into()),
        ..AttachResultMessage::new()
    };
    daemon.answer_in_turn(
        "attach_session",
        [
            Answer::Reply(message(failed)),
            Answer::Reply(message(attached())),
        ],
    );
    let mut world = World::start(daemon);
    world.open_desktop();

    respawn(&mut world);
    output(&world, 1, b"back");
    world.settle();

    assert_eq!(policies(&world), ["same_app_remount", "relaunch_restore"]);
    assert_eq!(row(&mut world, 0), "back");
}

#[test]
fn a_reconnect_attaches_a_terminal_whose_process_ended() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    world.daemon.emit(SessionExitedMessage {
        id: "r1".into(),
        session_id: "s1".into(),
        exit_code: 0,
        ..SessionExitedMessage::new()
    });
    world.settle();

    world.reconnect();

    assert_eq!(world.daemon.sent_of("attach_session").len(), 2);
}

#[test]
fn a_revive_carries_its_size_and_is_not_asked_again_for_the_runtime_it_revives() {
    let daemon = recoverable();
    daemon.answer_in_turn("attach_session", [Answer::Hold]);
    let mut world = World::start(daemon);
    world.open_desktop();

    respawn(&mut world);
    assert_eq!(
        terminal_cmds(&world),
        ["attach_session"],
        "a revive sizes itself"
    );
    let attach = &world.daemon.sent_of("attach_session")[0];
    assert!(attach["cols"].as_i64().unwrap() > 20 && attach["rows"].as_i64().unwrap() > 10);
    let revive = world.daemon.sent_of("attach_session")[0].clone();
    world.daemon.reply_to(&revive, attached());
    world.settle();
    output(&world, 1, b"revived");
    world.settle();

    assert_eq!(policies(&world), ["revive"]);
    assert_eq!(row(&mut world, 0), "revived");
}

#[test]
fn an_ordinary_attach_in_flight_is_asked_again_when_its_runtime_is_replaced() {
    let daemon = one_terminal();
    daemon.answer_in_turn(
        "attach_session",
        [Answer::Hold, Answer::Reply(message(attached()))],
    );
    let mut world = World::start(daemon);
    world.open_desktop();

    respawn(&mut world);
    output(&world, 1, b"new");
    world.settle();

    assert_eq!(policies(&world), ["same_app_remount", "relaunch_restore"]);
    assert_eq!(row(&mut world, 0), "new");
}

#[test]
fn a_revived_terminal_reattaches_as_a_remount_sized_to_its_tile() {
    let mut world = World::start(recoverable());
    world.open_desktop();
    respawn(&mut world);
    let before = terminal_cmds(&world).len();

    world.daemon.answer_hello_with(session_in("working"));
    world.reconnect();

    assert_eq!(
        terminal_cmds(&world)[before..before + 2],
        ["pty_resize", "attach_session"]
    );
    assert_eq!(
        policies(&world).last().map(String::as_str),
        Some("same_app_remount")
    );
}

#[test]
fn the_daemon_hears_the_terminal_colors_after_each_handshake() {
    let mut world = World::start(one_terminal());
    world.reconnect();

    let dark = serde_json::json!({
        "cmd": "set_terminal_theme",
        "foreground": "#d4d4d4",
        "background": "#1e1e1e",
        "cursor": "#d4d4d4",
        "ansi_palette": [
            "#000000", "#cd3131", "#0dbc79", "#e5e510",
            "#2472c8", "#bc3fbc", "#11a8cd", "#e5e5e5",
            "#666666", "#f14c4c", "#23d18b", "#f5f543",
            "#3b8eea", "#d670d6", "#29b8db", "#ffffff",
        ],
    });
    let themes = world.daemon.sent_of("set_terminal_theme");
    assert_eq!(themes.len(), 2);
    for theme in themes {
        let mut theme = theme.clone();
        theme.as_object_mut().unwrap().remove("request_id");
        assert_eq!(theme, dark);
    }
}

/// The window point at the middle of cell (`col`, `row`) of terminal r1, both zero-based.
pub(crate) fn cell(world: &World, col: u16, row: u16) -> (f32, f32) {
    let (rect, _) = world
        .runtime()
        .presented("terminal.r1")
        .expect("r1 is drawn");
    let metrics = Fonts::default().metrics(FONT_SIZE);
    (
        rect[0] + (f32::from(col) + 0.5) * metrics.cell_w,
        rect[1] + (f32::from(row) + 0.5) * metrics.cell_h,
    )
}

pub(crate) fn pointed(data: &str) -> (String, String) {
    (data.into(), "pointer".into())
}

pub(crate) fn live_with_modes(modes: &[u8]) -> World {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, modes);
    world.settle();
    world
}

#[test]
fn a_program_that_tracks_the_mouse_hears_presses_drags_and_releases_in_the_encoding_it_chose() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    let (a, b) = (cell(&world, 2, 1), cell(&world, 4, 1));
    world.gesture(|app| {
        app.runtime.pointer_down(a.0, a.1);
        app.runtime.pointer_up(a.0, a.1);
    });
    assert_eq!(
        inputs(&world),
        [],
        "a program that does not track the mouse hears nothing"
    );

    output(&world, 1, b"\x1b[?1002h\x1b[?1006h");
    world.settle();
    world.gesture(|app| {
        app.runtime.pointer_down(a.0, a.1);
        app.runtime.pointer_move(b.0, b.1);
    });
    world.gesture(|app| {
        app.runtime.physical_input(PhysicalInput::Modifiers(Mods {
            shift: true,
            ..Mods::default()
        }))
    });
    world.gesture(|app| app.runtime.pointer_up(b.0, b.1));
    world.gesture(|app| {
        app.runtime
            .physical_input(PhysicalInput::Modifiers(Mods::default()))
    });
    output(&world, 2, b"\x1b[?1006l");
    world.settle();
    world.gesture(|app| {
        app.runtime.pointer_move(a.0, a.1);
        app.runtime.button_down(a.0, a.1, PointerButton::Middle);
        app.runtime.button_up(a.0, a.1, PointerButton::Middle);
    });

    assert_eq!(
        inputs(&world),
        [
            pointed("\x1b[<0;3;2M"),
            pointed("\x1b[<32;5;2M"),
            pointed("\x1b[<7;5;2m"),
            pointed("\x1b[M!#\""),
            pointed("\x1b[M##\""),
        ]
    );
}

#[test]
fn motion_with_no_button_held_reports_only_under_any_event_tracking() {
    let mut world = live_with_modes(b"\x1b[?1002h\x1b[?1006h");
    let (a, b) = (cell(&world, 0, 0), cell(&world, 1, 2));
    world.gesture(|app| app.runtime.pointer_move(a.0, a.1));
    output(&world, 2, b"\x1b[?1003h");
    world.settle();
    let commits = world.runtime().stats.commits;
    world.gesture(|app| app.runtime.pointer_move(b.0, b.1));

    assert_eq!(inputs(&world), [pointed("\x1b[<35;2;3M")]);
    assert_eq!(
        world.runtime().stats.commits,
        commits,
        "motion runs no view"
    );
}

#[test]
fn a_press_with_alt_held_and_its_drag_stay_with_the_terminal() {
    let mut world = live_with_modes(b"\x1b[?1002h\x1b[?1006h");
    let (a, b) = (cell(&world, 0, 0), cell(&world, 3, 1));
    world.gesture(|app| {
        app.runtime.physical_input(PhysicalInput::Modifiers(Mods {
            alt: true,
            ..Mods::default()
        }))
    });
    world.gesture(|app| {
        app.runtime.pointer_move(a.0, a.1);
        app.runtime.pointer_down(a.0, a.1);
        app.runtime.pointer_move(b.0, b.1);
        app.runtime.pointer_up(b.0, b.1);
    });

    assert_eq!(inputs(&world), []);
}

#[test]
fn a_reported_press_is_released_where_the_pointer_left_the_terminal_and_when_the_window_loses_focus()
 {
    let mut world = live_with_modes(b"\x1b[?1000h\x1b[?1006h");
    let a = cell(&world, 3, 2);
    world.gesture(|app| {
        app.runtime.pointer_down(a.0, a.1);
        app.runtime.pointer_up(1.0, 1.0);
        app.runtime.pointer_down(a.0, a.1);
        app.runtime.window_focus(false);
    });

    assert_eq!(
        inputs(&world),
        [
            pointed("\x1b[<0;4;3M"),
            pointed("\x1b[<3;4;3m"),
            pointed("\x1b[<0;4;3M"),
            pointed("\x1b[<3;4;3m"),
        ]
    );
}

#[test]
fn a_terminal_that_goes_away_with_a_reported_press_releases_it() {
    let mut world = live_with_modes(b"\x1b[?1000h\x1b[?1006h");
    let a = cell(&world, 1, 1);
    world.gesture(|app| app.runtime.pointer_down(a.0, a.1));
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile(),
        desktops: vec![desktop(vec![])],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();

    assert_eq!(
        inputs(&world),
        [pointed("\x1b[<0;2;2M"), pointed("\x1b[<3;2;2m")]
    );
}

#[test]
fn the_wheel_reports_to_a_tracking_program_and_turns_into_arrow_keys_on_the_alternate_screen() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    let a = cell(&world, 1, 0);
    let row = Fonts::default().metrics(FONT_SIZE).cell_h;
    let wheel = |world: &mut World, rows: f32| {
        world.gesture(|app| {
            app.runtime
                .wheel_event(WheelEvent::new(0.0, -rows * row).at(a.0, a.1))
        });
        world.advance(Duration::from_secs(1));
    };
    wheel(&mut world, 2.0);
    output(&world, 1, b"\x1b[?1049h");
    world.settle();
    for rows in [0.4, 0.4, 0.4, -0.4, -0.4, -0.4, 8.0] {
        wheel(&mut world, rows);
    }
    output(&world, 2, b"\x1b[?1000h\x1b[?1006h");
    world.settle();
    wheel(&mut world, 9.0);
    wheel(&mut world, -1.0);
    output(&world, 3, b"\x1b[?1006l");
    world.settle();
    wheel(&mut world, -1.0);

    assert_eq!(
        inputs(&world),
        [
            typed("\x1b[B"),
            typed("\x1b[A"),
            typed(&"\x1b[B".repeat(5)),
            pointed(&"\x1b[<65;2;1M".repeat(5)),
            pointed("\x1b[<64;2;1M"),
            pointed("\x1b[M`\"!"),
        ]
    );
}

#[test]
fn pointer_motion_over_an_agent_tells_the_daemon_at_most_every_quarter_second_while_connected() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    let (a, b) = (cell(&world, 0, 0), cell(&world, 1, 0));
    world.gesture(|app| {
        app.runtime.pointer_move(a.0, a.1);
        app.runtime.pointer_move(b.0, b.1);
    });
    world.advance(Duration::from_millis(250));
    world.gesture(|app| app.runtime.pointer_move(a.0, a.1));
    world.daemon.on("client_hello", |_| ());
    world.daemon.disconnect();
    world.settle();
    world.advance(Duration::from_secs(1));
    world.advance(Duration::from_millis(250));
    let c = cell(&world, 2, 0);
    world.gesture(|app| app.runtime.pointer_move(c.0, c.1));

    let activity = world.daemon.sent_of("terminal_pointer_activity");
    let once = serde_json::json!({ "cmd": "terminal_pointer_activity", "id": "r1" });
    assert_eq!(
        activity,
        [once.clone(), once],
        "only while the handshake holds"
    );
}

#[test]
fn pointer_motion_over_a_shell_tells_the_daemon_nothing() {
    let shell = DesktopPane {
        kind: "shell".into(),
        ..pane("t1", "r1")
    };
    let mut world = World::start(Daemon::scripted().state(showing(vec![shell], "daemon-1")));
    world.open_desktop();
    let a = cell(&world, 0, 0);
    world.gesture(|app| app.runtime.pointer_move(a.0, a.1));

    assert!(world.daemon.sent_of("terminal_pointer_activity").is_empty());
}

#[test]
fn a_key_draws_no_terminal_frame_until_its_echo_arrives() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"$ ");
    world.settle();
    let frames = world.terminal_frames();

    world.gesture(|app| app.type_text("l"));
    assert_eq!(inputs(&world), [typed("l")]);
    assert_eq!(world.terminal_frames(), frames, "the key changed no screen");

    output(&world, 2, b"l");
    world.settle();
    assert_eq!(world.terminal_frames(), frames + 1, "its echo did");
    assert_eq!(row(&mut world, 0), "$ l");
}

#[test]
fn an_emoji_sequence_takes_one_cell_even_after_a_program_resets_the_terminal_or_turns_clustering_off()
 {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    let family = "👨\u{200d}👩\u{200d}👧";
    let cluster_then_x = format!("{family}\x1b[1;3HX");

    output(&world, 1, cluster_then_x.as_bytes());
    world.settle();
    assert_eq!(row(&mut world, 0), format!("{family}X"));

    output(&world, 2, b"\x1b");
    output(&world, 3, format!("c{cluster_then_x}").as_bytes());
    world.settle();
    assert_eq!(
        row(&mut world, 0),
        format!("{family}X"),
        "after a reset split across writes"
    );

    output(&world, 4, format!("\x1bc{cluster_then_x}").as_bytes());
    world.settle();
    assert_eq!(
        row(&mut world, 0),
        format!("{family}X"),
        "after a reset inside one write"
    );

    output(&world, 5, b"\x1b[?2027l");
    output(
        &world,
        6,
        format!("\x1b[2J\x1b[H{cluster_then_x}").as_bytes(),
    );
    world.settle();
    assert_eq!(
        row(&mut world, 0),
        format!("{family}X"),
        "after the program turned it off"
    );
}

#[test]
fn automation_reads_the_terminal_screen_the_window_drew_and_waits_for_text_in_it() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"$ echo hi\r\nhi");
    world.settle();

    let rows = world
        .automate(
            "state",
            serde_json::json!({"of": "terminal:r1", "includes": "hi"}),
        )
        .unwrap();
    assert_eq!(rows[0], "$ echo hi");
    assert_eq!(rows[1], "hi");

    let waiting = world
        .automate(
            "state",
            serde_json::json!({"of": "terminal:r1", "includes": "bye"}),
        )
        .unwrap_err();
    assert!(
        waiting.contains("still waits for state \"terminal:r1\" to include \"bye\""),
        "{waiting}"
    );

    let undrawn = world
        .automate(
            "state",
            serde_json::json!({"of": "terminal:r9", "includes": "hi"}),
        )
        .unwrap_err();
    assert!(
        undrawn.contains("still waits for state \"terminal:r9\" to be there"),
        "{undrawn}"
    );

    let mistyped = world
        .automate(
            "state",
            serde_json::json!({"of": "windows", "includes": "hi"}),
        )
        .unwrap_err();
    assert!(
        !mistyped.contains("waits"),
        "a slice that cannot exist fails at once: {mistyped}"
    );
    assert!(mistyped.contains("terminal:<runtime id>"), "{mistyped}");
}

#[test]
fn an_automation_scroll_over_a_terminal_turns_the_wheel_its_program_hears() {
    let mut world = live_with_modes(b"\x1b[?1000h\x1b[?1006h");

    world
        .automate(
            "scroll",
            serde_json::json!({"target": {"key": "terminal.r1"}, "dy": 100.0}),
        )
        .unwrap();

    let sent = inputs(&world);
    assert_eq!(sent.len(), 1, "{sent:?}");
    assert!(
        sent[0].0.contains("\x1b[<65;") && !sent[0].0.contains("\x1b[<64;"),
        "the wheel turned down: {sent:?}"
    );
}

#[test]
fn an_automation_scroll_right_after_another_goes_to_its_own_target() {
    let mut world = live_with_modes(b"\x1b[?1000h\x1b[?1006h");
    world
        .automate(
            "scroll",
            serde_json::json!({"target": {"key": "terminal.r1"}, "dy": 100.0}),
        )
        .unwrap();
    let before = inputs(&world).len();

    world
        .automate(
            "scroll",
            serde_json::json!({"target": {"key": "sidebar"}, "dy": 100.0}),
        )
        .unwrap();

    assert_eq!(
        inputs(&world).len(),
        before,
        "the sidebar's turn did not reach the terminal"
    );
}

#[test]
fn a_right_button_press_and_its_release_reach_a_program_that_tracks_the_mouse() {
    let mut world = live_with_modes(b"\x1b[?1000h\x1b[?1006h");
    let a = cell(&world, 2, 3);
    world.gesture(|app| {
        app.runtime.pointer_move(a.0, a.1);
        app.runtime.button_down(a.0, a.1, PointerButton::Secondary);
        app.runtime.button_up(a.0, a.1, PointerButton::Secondary);
    });

    assert_eq!(
        inputs(&world),
        [pointed("\x1b[<2;3;4M"), pointed("\x1b[<3;3;4m")]
    );
}

#[test]
fn a_drag_that_began_outside_the_terminal_is_not_reported_as_motion() {
    let mut world = live_with_modes(b"\x1b[?1003h\x1b[?1006h");
    let (tile, _) = world.runtime().presented("tile.t1").expect("t1 is drawn");
    let title = (tile[0] + 20.0, tile[1] + 12.0);
    let (a, b) = (cell(&world, 0, 0), cell(&world, 1, 0));
    world.gesture(|app| {
        app.runtime.pointer_move(title.0, title.1);
        app.runtime.pointer_down(title.0, title.1);
        app.runtime.pointer_move(a.0, a.1);
        app.runtime.pointer_up(a.0, a.1);
        app.runtime.pointer_move(b.0, b.1);
    });

    assert_eq!(inputs(&world), [pointed("\x1b[<35;2;1M")]);
}
