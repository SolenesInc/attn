//! The app on a machine whose daemon relays it to a home elsewhere: the terminals that run on
//! this machine ride the app's link to this machine's own daemon, and stay live while the home
//! is away.

use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Duration;

use attn_protocol::{
    Desktop, DesktopPane, InitialStateMessage, PROTOCOL_VERSION, Profile, Session, TerminalBinding,
};
use attn_testkit::{Daemon, Target, World, message, tree};
use serde_json::json;

const HERE: &str = "laptop-id";
const WIDE_ENOUGH_FOR_BOTH: (f32, f32) = (2400.0, 900.0);

fn shell(id: &str, host: Option<&str>) -> Session {
    Session {
        id: id.into(),
        label: id.into(),
        agent: "shell".into(),
        state: "idle".into(),
        directory: format!("/tmp/{id}"),
        host: host.map(str::to_owned),
        profile_id: "work".into(),
        ..Session::default()
    }
}

fn pane(id: &str, runtime: &str, session: &str) -> DesktopPane {
    DesktopPane {
        desktop_id: "d1".into(),
        pane_id: id.into(),
        runtime_id: runtime.into(),
        session_id: session.into(),
        kind: "shell".into(),
        ..DesktopPane::default()
    }
}

/// The home's state: this machine's shell, with terminal rl, beside the home's own, with rh.
fn home_state() -> InitialStateMessage {
    InitialStateMessage {
        protocol_version: Some(PROTOCOL_VERSION.into()),
        daemon_instance_id: Some("home-daemon".into()),
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: "d1".into(),
            ..Profile::default()
        }]),
        sessions: Some(vec![shell("here", Some(HERE)), shell("home", None)]),
        desktops: Some(vec![Desktop {
            id: "d1".into(),
            name: "d1".into(),
            profile_id: "work".into(),
            shortcut_slot: Some(1),
            order_key: "1".into(),
            active_pane_id: "t-here".into(),
            tree_json: tree::row([tree::pane("t-here"), tree::pane("t-home")]).to_string(),
            panes: vec![pane("t-here", "rl", "here"), pane("t-home", "rh", "home")],
            ..Desktop::default()
        }]),
        ..InitialStateMessage::new()
    }
}

/// This machine's daemon, which knows its own shell and the terminal it runs in.
fn this_machine(home: &Daemon) -> Daemon {
    Daemon::of_this_machine(home).state(InitialStateMessage {
        protocol_version: Some(PROTOCOL_VERSION.into()),
        daemon_instance_id: Some("laptop-daemon".into()),
        sessions: Some(vec![shell("here", None)]),
        terminal_bindings: Some(vec![TerminalBinding {
            session_id: "here".into(),
            terminal_id: "rl".into(),
        }]),
        ..InitialStateMessage::new()
    })
}

/// A home that answers with its state while `answering`, and is unreachable otherwise.
fn home(answering: Arc<AtomicBool>) -> Daemon {
    let daemon = Daemon::scripted();
    daemon.on("client_hello", move |_| {
        if answering.load(Ordering::SeqCst) {
            message(home_state())
        } else {
            json!({"event": "home_unreachable", "home": "oma", "state": "dialing"})
        }
    });
    daemon
}

fn start(answering: bool) -> (World, Arc<AtomicBool>) {
    let answering = Arc::new(AtomicBool::new(answering));
    let home = home(answering.clone());
    let machine = this_machine(&home);
    (
        World::start_relaying(home.relaying_to(HERE, "oma"), machine, WIDE_ENOUGH_FOR_BOTH),
        answering,
    )
}

fn machine(world: &World) -> &Daemon {
    world
        .machine
        .as_ref()
        .expect("the world has this machine's daemon")
}

fn ids(daemon: &Daemon, cmd: &str) -> Vec<String> {
    daemon
        .sent_of(cmd)
        .iter()
        .map(|r| r["id"].as_str().unwrap_or_default().to_owned())
        .collect()
}

fn typed(daemon: &Daemon) -> Vec<(String, String)> {
    daemon
        .sent_of("pty_input")
        .iter()
        .filter(|r| r["source"] == "user")
        .map(|r| {
            (
                r["id"].as_str().unwrap_or_default().to_owned(),
                r["data"].as_str().unwrap_or_default().to_owned(),
            )
        })
        .collect()
}

fn output(daemon: &Daemon, runtime: &str, seq: u32, data: &[u8]) {
    let mut frame = vec![0x01, runtime.len() as u8];
    frame.extend_from_slice(runtime.as_bytes());
    frame.extend_from_slice(&seq.to_be_bytes());
    frame.extend_from_slice(data);
    daemon.emit_frame(frame);
}

fn type_into(world: &mut World, runtime: &str, text: &str) {
    world.gesture(|app| app.click(Target::key(format!("terminal.{runtime}"))));
    world.gesture(|app| app.type_text(text));
}

fn reconnect_home(world: &mut World, answering: &AtomicBool, answers: bool) {
    answering.store(answers, Ordering::SeqCst);
    world.daemon.disconnect();
    world.settle();
    world.advance(Duration::from_secs(1));
}

fn tiles(world: &World, runtime: &str) -> usize {
    world
        .screen()
        .all(Target::key(format!("terminal.{runtime}")))
        .len()
}

#[test]
fn a_terminal_on_this_machine_rides_its_own_daemon_and_a_home_terminal_rides_the_home() {
    let (mut world, _) = start(true);
    world.open_desktop();

    assert_eq!(ids(machine(&world), "attach_session"), ["rl"]);
    assert_eq!(ids(&world.daemon, "attach_session"), ["rh"]);

    type_into(&mut world, "rl", "x");
    type_into(&mut world, "rh", "y");
    assert_eq!(typed(machine(&world)), [("rl".to_owned(), "x".to_owned())]);
    assert_eq!(typed(&world.daemon), [("rh".to_owned(), "y".to_owned())]);

    output(machine(&world), "rl", 1, b"from this machine");
    output(&world.daemon, "rl", 2, b"from the home");
    world.settle();
    let screen = world.terminal_text("rl").join("\n");
    assert!(
        screen.contains("from this machine") && !screen.contains("from the home"),
        "a terminal hears its stream only from the link it attached on:\n{screen}"
    );

    world
        .daemon
        .emit(json!({"event": "runtime_respawned", "id": "rl"}));
    world.settle();
    assert_eq!(
        ids(machine(&world), "attach_session"),
        ["rl", "rl"],
        "the home says the session started again, and the terminal attaches anew on this machine"
    );
    world
        .daemon
        .emit(json!({"event": "session_exited", "id": "rl", "session_id": "here", "exit_code": 1}));
    world.settle();
    let screen = world.terminal_text("rl").join("\n");
    assert!(
        screen.contains("[Process exited with code 1]"),
        "the home, which owns sessions, says when this machine's terminal ends:\n{screen}"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn when_the_home_is_lost_this_machines_terminals_stay_live_and_the_homes_wait() {
    let (mut world, answering) = start(true);
    world.open_desktop();
    output(&world.daemon, "rh", 1, b"home screen");
    world.settle();

    reconnect_home(&mut world, &answering, false);

    let band = world
        .screen()
        .find(Target::key("connection.home-lost"))
        .and_then(|n| n.name);
    assert!(
        band.as_deref().is_some_and(|b| b.contains("Lost oma")),
        "the desk stays, under a band that says the home is lost: {band:?}\n{}",
        world.screen()
    );
    assert_eq!(
        world
            .screen()
            .find(Target::key("tile.rh.status"))
            .and_then(|n| n.name)
            .as_deref(),
        Some("Lost oma · typing is not sent")
    );
    assert!(world.terminal_text("rh").join("\n").contains("home screen"));
    type_into(&mut world, "rh", "y");
    type_into(&mut world, "rl", "x");
    assert_eq!(typed(machine(&world)), [("rl".to_owned(), "x".to_owned())]);
    assert!(
        typed(&world.daemon).is_empty(),
        "typing for the home's terminal is not sent"
    );

    world.gesture(|app| app.click(Target::key("connection.home-lost.retry")));
    assert_eq!(world.daemon.sent_of("retry_home").len(), 1);

    reconnect_home(&mut world, &answering, true);
    assert!(
        world
            .screen()
            .find(Target::key("connection.home-lost"))
            .is_none()
    );
    assert_eq!((tiles(&world, "rl"), tiles(&world, "rh")), (1, 1));
    assert_eq!(
        ids(machine(&world), "attach_session"),
        ["rl"],
        "this machine's terminal stayed attached on its own daemon"
    );
    type_into(&mut world, "rl", "z");
    assert_eq!(
        typed(machine(&world)),
        [
            ("rl".to_owned(), "x".to_owned()),
            ("rl".to_owned(), "z".to_owned())
        ]
    );
}

#[test]
fn before_the_home_answers_this_machines_terminals_are_live_in_one_row_the_home_replaces() {
    let (mut world, answering) = start(false);

    let band = world
        .screen()
        .find(Target::key("connection.home-lost"))
        .and_then(|n| n.name);
    assert!(
        band.as_deref()
            .is_some_and(|b| b.contains("Can\u{2019}t reach oma")),
        "{band:?}\n{}",
        world.screen()
    );
    assert_eq!(tiles(&world, "rl"), 1, "{}", world.screen());
    assert_eq!(
        world
            .screen()
            .find(Target::key("sidebar.waiting-for-home"))
            .and_then(|n| n.name)
            .as_deref(),
        Some("Waiting for oma: desktops, queue, garden")
    );
    assert_eq!(
        world
            .screen()
            .find(Target::key("sidebar.desktop."))
            .and_then(|n| n.name)
            .as_deref(),
        Some(if attn_testkit::mac() {
            "Open This Mac"
        } else {
            "Open This machine"
        })
    );
    assert_eq!(ids(machine(&world), "attach_session"), ["rl"]);
    type_into(&mut world, "rl", "x");
    assert_eq!(typed(machine(&world)), [("rl".to_owned(), "x".to_owned())]);
    let homes: Vec<String> = world
        .daemon
        .sent()
        .iter()
        .filter_map(|r| r["cmd"].as_str().map(str::to_owned))
        .filter(|cmd| cmd.starts_with("desktop_"))
        .collect();
    assert!(
        homes.is_empty(),
        "the row is no desktop of the home's: {homes:?}"
    );

    reconnect_home(&mut world, &answering, true);
    assert!(
        world
            .screen()
            .find(Target::key("connection.home-lost"))
            .is_none()
    );
    assert_eq!(
        (tiles(&world, "rl"), tiles(&world, "rh")),
        (1, 1),
        "{}",
        world.screen()
    );
    assert_eq!(
        ids(machine(&world), "attach_session"),
        ["rl"],
        "the home's desktop took over the same live terminal"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn before_the_home_answers_a_machine_without_terminals_of_its_own_shows_only_cant_reach() {
    let answering = Arc::new(AtomicBool::new(false));
    let home = home(answering);
    let machine = Daemon::of_this_machine(&home).state(InitialStateMessage {
        protocol_version: Some(PROTOCOL_VERSION.into()),
        daemon_instance_id: Some("laptop-daemon".into()),
        ..InitialStateMessage::new()
    });
    let world = World::start_relaying(home.relaying_to(HERE, "oma"), machine, WIDE_ENOUGH_FOR_BOTH);

    assert!(
        world
            .screen()
            .find(Target::key("connection.home-unreachable"))
            .is_some()
            && world.screen().find(Target::key("sidebar")).is_none(),
        "{}",
        world.screen()
    );
    assert!(world.runtime_is_at_rest());
}

/// The home's state with this machine's agent, in rl on d1, and its shell, in r2 on d2, where no
/// tile shows it, beside a document that is d2's active tile.
fn one_hidden() -> (InitialStateMessage, InitialStateMessage) {
    let agent = Session {
        agent: "claude".into(),
        ..shell("agent", Some(HERE))
    };
    let desktop = |id: &str, slot: i64, pane: DesktopPane, document: bool| {
        let mut leaves = vec![tree::pane(&pane.pane_id)];
        if document {
            let mut doc = tree::tile("doc", "markdown");
            doc["tile_params"] = json!({"path": "/tmp/there/notes.md"}).to_string().into();
            leaves.insert(0, doc);
        }
        Desktop {
            id: id.into(),
            name: id.into(),
            profile_id: "work".into(),
            shortcut_slot: Some(slot),
            order_key: slot.to_string(),
            active_pane_id: if document {
                "doc".into()
            } else {
                pane.pane_id.clone()
            },
            tree_json: tree::row(leaves).to_string(),
            panes: vec![pane],
            ..Desktop::default()
        }
    };
    let home = InitialStateMessage {
        sessions: Some(vec![agent.clone(), shell("there", Some(HERE))]),
        desktops: Some(vec![
            desktop(
                "d1",
                1,
                DesktopPane {
                    kind: "agent".into(),
                    ..pane("t-agent", "rl", "agent")
                },
                false,
            ),
            desktop(
                "d2",
                2,
                DesktopPane {
                    desktop_id: "d2".into(),
                    ..pane("t-there", "r2", "there")
                },
                true,
            ),
        ]),
        ..home_state()
    };
    let here = InitialStateMessage {
        protocol_version: Some(PROTOCOL_VERSION.into()),
        daemon_instance_id: Some("laptop-daemon".into()),
        sessions: Some(vec![
            Session {
                host: None,
                ..agent
            },
            shell("there", None),
        ]),
        terminal_bindings: Some(
            [("agent", "rl"), ("there", "r2")]
                .map(|(session, terminal)| TerminalBinding {
                    session_id: session.into(),
                    terminal_id: terminal.into(),
                })
                .into(),
        ),
        ..InitialStateMessage::new()
    };
    (home, here)
}

#[test]
fn every_command_for_a_terminal_on_this_machine_goes_to_its_own_daemon() {
    let (home, here) = one_hidden();
    let home = Daemon::scripted().state(home).relaying_to(HERE, "oma");
    let laptop = Daemon::of_this_machine(&home).state(here);
    let mut world = World::start_relaying(home, laptop, WIDE_ENOUGH_FOR_BOTH);
    world.open_desktop();

    let (rect, _) = world
        .runtime()
        .presented("terminal.rl")
        .expect("rl is drawn");
    world.gesture(|app| {
        app.runtime.pointer_move(rect[0] + 20.0, rect[1] + 20.0);
        app.runtime.pointer_move(rect[0] + 40.0, rect[1] + 20.0);
    });
    world.gesture(|app| app.key(super::sidebar::chord(attn_app::Action::DesktopOverview)));

    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Down,
            mods: rune::Mods::default(),
        })
    });
    for cmd in ["terminal_pointer_activity", "get_screen_snapshot"] {
        assert_eq!(
            (
                ids(machine(&world), cmd).is_empty(),
                ids(&world.daemon, cmd)
            ),
            (false, Vec::<String>::new()),
            "{cmd} goes to this machine's daemon, and none to the home"
        );
    }
    assert!(ids(machine(&world), "get_screen_snapshot").contains(&"r2".to_owned()));
}

/// The home's state with three of this machine's shells side by side on d1, and this
/// machine's daemon, which runs them.
fn three_here() -> (InitialStateMessage, InitialStateMessage) {
    let ids = ["a", "b", "c"];
    let home = InitialStateMessage {
        sessions: Some(ids.map(|id| shell(id, Some(HERE))).into()),
        desktops: Some(vec![Desktop {
            id: "d1".into(),
            name: "d1".into(),
            profile_id: "work".into(),
            shortcut_slot: Some(1),
            order_key: "1".into(),
            active_pane_id: "t-a".into(),
            tree_json: tree::row(ids.map(|id| tree::pane(&format!("t-{id}")))).to_string(),
            panes: ids
                .map(|id| pane(&format!("t-{id}"), &format!("r{id}"), id))
                .into(),
            ..Desktop::default()
        }]),
        ..home_state()
    };
    let here = InitialStateMessage {
        protocol_version: Some(PROTOCOL_VERSION.into()),
        daemon_instance_id: Some("laptop-daemon".into()),
        sessions: Some(ids.map(|id| shell(id, None)).into()),
        terminal_bindings: Some(
            ids.map(|id| TerminalBinding {
                session_id: id.into(),
                terminal_id: format!("r{id}"),
            })
            .into(),
        ),
        ..InitialStateMessage::new()
    };
    (home, here)
}

fn focus_right_twice_and_type(world: &mut World) {
    for _ in 0..2 {
        world.gesture(|app| app.key(super::sidebar::chord(attn_app::Action::TerminalFocusRight)));
    }
    world.gesture(|app| app.type_text("z"));
}

#[test]
fn while_the_home_is_away_the_keyboard_moves_between_this_machines_terminals_here() {
    for mid_session in [false, true] {
        let answering = Arc::new(AtomicBool::new(mid_session));
        let (state, here) = three_here();
        let home = Daemon::scripted();
        let answers = answering.clone();
        home.on("client_hello", move |_| {
            if answers.load(Ordering::SeqCst) {
                message(state.clone())
            } else {
                json!({"event": "home_unreachable", "home": "oma", "state": "dialing"})
            }
        });
        let laptop = Daemon::of_this_machine(&home).state(here);
        let mut world =
            World::start_relaying(home.relaying_to(HERE, "oma"), laptop, WIDE_ENOUGH_FOR_BOTH);
        if mid_session {
            world.open_desktop();
            reconnect_home(&mut world, &answering, false);
        }

        focus_right_twice_and_type(&mut world);

        assert_eq!(
            typed(machine(&world)).last(),
            Some(&("rc".to_owned(), "z".to_owned())),
            "mid-session {mid_session}: the keyboard reached the third terminal"
        );
        assert!(
            world.daemon.sent_of("desktop_show_leaf").is_empty()
                && world.daemon.sent_of("desktop_show_session").is_empty(),
            "mid-session {mid_session}: nothing goes to the away home"
        );
        assert!(
            world.screen().find(Target::key("notice.text")).is_none(),
            "mid-session {mid_session}: no notice says the show failed:\n{}",
            world.screen()
        );

        reconnect_home(&mut world, &answering, true);
        world.gesture(|app| app.type_text("h"));
        assert_eq!(
            typed(machine(&world)).last(),
            Some(&("ra".to_owned(), "h".to_owned())),
            "mid-session {mid_session}: once the home answers, its active tile has the keyboard"
        );
    }
}

#[test]
fn a_popped_out_terminal_on_this_machine_stays_out_while_the_home_is_lost_and_the_homes_hides() {
    let answering = Arc::new(AtomicBool::new(true));
    let popped = |mut leaf: serde_json::Value, x: f64| {
        leaf["window"] = json!({"x": x, "y": 20.0, "width": 500.0, "height": 400.0});
        leaf
    };
    let mut state = home_state();
    let mut desktops = state.desktops.take().unwrap();
    desktops[0].active_pane_id = "t-home".into();
    desktops[0].tree_json = tree::row([
        popped(tree::pane("t-here"), 10.0),
        popped(tree::pane("t-home"), 600.0),
    ])
    .to_string();
    let mut brought_back = desktops[0].clone();
    brought_back.tree_json =
        tree::row([tree::pane("t-here"), popped(tree::pane("t-home"), 600.0)]).to_string();
    let profile = state.profiles.clone().unwrap().remove(0);
    state.desktops = Some(desktops);
    let home = Daemon::scripted();
    let answers = answering.clone();
    home.on("client_hello", move |_| {
        if answers.load(Ordering::SeqCst) {
            message(state.clone())
        } else {
            json!({"event": "home_unreachable", "home": "oma", "state": "dialing"})
        }
    });
    let laptop = this_machine(&home);
    let mut world =
        World::start_relaying(home.relaying_to(HERE, "oma"), laptop, WIDE_ENOUGH_FOR_BOTH);
    let shown = |world: &World| {
        let mut xs: Vec<f64> = world
            .tile_windows()
            .iter()
            .filter(|w| w.shown)
            .map(|w| w.frame.x)
            .collect();
        xs.sort_by(f64::total_cmp);
        xs
    };
    assert_eq!(shown(&world), [10.0, 600.0]);

    reconnect_home(&mut world, &answering, false);
    assert_eq!(
        shown(&world),
        [10.0],
        "this machine's terminal keeps its window, and the home's hides"
    );
    let here_window = world
        .tile_windows()
        .into_iter()
        .find(|w| w.shown && w.frame.x == 10.0)
        .expect("this machine's terminal has its window")
        .key;
    world.tile_gesture(&here_window, |app| app.click(Target::key("terminal.rl")));
    world.close_tile_window(&here_window);
    world.settle();
    assert_eq!(
        shown(&world),
        [10.0],
        "the away home cannot take the tile back, so its window shows again"
    );
    reconnect_home(&mut world, &answering, true);
    assert_eq!(shown(&world), [10.0, 600.0]);
    world.tile_gesture(&here_window, |app| app.type_text("w"));
    assert_eq!(
        typed(machine(&world)).last(),
        Some(&("rl".to_owned(), "w".to_owned())),
        "a press in the window while the home was away leaves it holding its own terminal"
    );
    assert_eq!(
        ids(machine(&world), "attach_session"),
        ["rl"],
        "the terminal on this machine stayed attached through the outage"
    );

    world
        .daemon
        .emit(attn_protocol::ProfileArrangementChangedMessage {
            profile,
            desktops: vec![brought_back],
            ..attn_protocol::ProfileArrangementChangedMessage::new()
        });
    world.settle();
    assert_eq!(
        shown(&world),
        [600.0],
        "the window that showed again hides once its tile comes back"
    );
}

#[test]
fn while_the_home_is_away_a_desktop_switch_reaches_this_machines_terminal_there() {
    let answering = Arc::new(AtomicBool::new(true));
    let (state, here) = one_hidden();
    let home = Daemon::scripted();
    let answers = answering.clone();
    home.on("client_hello", move |_| {
        if answers.load(Ordering::SeqCst) {
            message(state.clone())
        } else {
            json!({"event": "home_unreachable", "home": "oma", "state": "dialing"})
        }
    });
    let laptop = Daemon::of_this_machine(&home).state(here);
    let mut world =
        World::start_relaying(home.relaying_to(HERE, "oma"), laptop, WIDE_ENOUGH_FOR_BOTH);
    world.open_desktop();
    reconnect_home(&mut world, &answering, false);
    let before = world.daemon.sent().len();

    world.gesture(|app| app.key(super::sidebar::chord(attn_app::Action::DesktopSelect2)));
    assert!(
        world.screen().find(Target::key("terminal.r2")).is_some(),
        "the switch shows d2, whose active tile is a document:\n{}",
        world.screen()
    );
    world.gesture(|app| app.key(super::sidebar::chord(attn_app::Action::TerminalFocusRight)));
    world.gesture(|app| app.type_text("q"));

    assert_eq!(
        typed(machine(&world)).last(),
        Some(&("r2".to_owned(), "q".to_owned()))
    );
    assert_eq!(
        ids(machine(&world), "terminal_focus")
            .last()
            .map(String::as_str),
        Some("r2"),
        "this machine's daemon hears which of its terminals has the keyboard"
    );

    world.gesture(|app| app.click(Target::key("sidebar.session.agent")));
    world.gesture(|app| app.type_text("k"));
    assert_eq!(
        typed(machine(&world)).last(),
        Some(&("rl".to_owned(), "k".to_owned()))
    );
    assert_eq!(
        ids(machine(&world), "terminal_focus")
            .last()
            .map(String::as_str),
        Some("rl"),
        "an agent opened on another desktop tells this machine's daemon it has the keyboard"
    );

    world.gesture(|app| app.key(super::sidebar::chord(attn_app::Action::DesktopSelect1)));
    world.gesture(|app| app.type_text("p"));
    assert_eq!(
        typed(machine(&world)).last(),
        Some(&("r2".to_owned(), "p".to_owned())),
        "the current desktop's own digit goes back to the desktop shown before it"
    );
    assert_eq!(
        world.daemon.sent()[before..]
            .iter()
            .map(|r| r["cmd"].clone())
            .filter(|cmd| cmd != "set_client_presence")
            .collect::<Vec<_>>(),
        Vec::<serde_json::Value>::new(),
        "nothing goes to the away home"
    );
    assert!(
        world.screen().find(Target::key("notice.text")).is_none(),
        "{}",
        world.screen()
    );
}

#[test]
fn a_peek_at_this_machines_shelved_agent_reads_and_replies_on_its_own_daemon() {
    let leaves = ["a", "b", "c", "d"];
    let agent = |id: &str| Session {
        agent: "claude".into(),
        state: "waiting_input".into(),
        ..shell(id, Some(HERE))
    };
    let state = InitialStateMessage {
        sessions: Some(leaves.iter().map(|id| agent(id)).collect()),
        desktops: Some(vec![Desktop {
            id: "d1".into(),
            name: "d1".into(),
            profile_id: "work".into(),
            shortcut_slot: Some(1),
            order_key: "1".into(),
            active_pane_id: "a".into(),
            tree_json: tree::row(leaves.map(tree::pane)).to_string(),
            panes: leaves
                .iter()
                .map(|id| DesktopPane {
                    kind: "agent".into(),
                    ..pane(id, &format!("r-{id}"), id)
                })
                .collect(),
            ..Desktop::default()
        }]),
        ..home_state()
    };
    let home = Daemon::scripted().state(state);
    let laptop = Daemon::of_this_machine(&home).state(InitialStateMessage {
        protocol_version: Some(PROTOCOL_VERSION.into()),
        daemon_instance_id: Some("laptop-daemon".into()),
        sessions: Some(leaves.iter().map(|id| agent(id)).collect()),
        terminal_bindings: Some(
            leaves
                .iter()
                .map(|id| TerminalBinding {
                    session_id: (*id).into(),
                    terminal_id: format!("r-{id}"),
                })
                .collect(),
        ),
        ..InitialStateMessage::new()
    });
    laptop.on("terminal_read_text", |request| {
        message(attn_protocol::TerminalReadTextResultMessage {
            success: true,
            text: Some(format!(
                "screen of {}",
                request["terminal_id"].as_str().unwrap()
            )),
            ..attn_protocol::TerminalReadTextResultMessage::new()
        })
    });
    laptop.on("pty_input", |request| {
        let probe = request["probe_id"].as_str()?;
        if request["data"] == "held" {
            return None;
        }
        Some(message(attn_protocol::PtyInputProbeResultMessage {
            id: request["id"].as_str().unwrap().into(),
            probe_id: probe.into(),
            success: true,
            ..attn_protocol::PtyInputProbeResultMessage::new()
        }))
    });
    let mut world = World::start_relaying(home.relaying_to(HERE, "oma"), laptop, (1600.0, 900.0));
    world.open_desktop();

    let [x, y, w, h] = world
        .runtime()
        .presented("shelf.entry.c")
        .unwrap_or_else(|| panic!("c waits on the shelf: {}", world.screen()))
        .0;
    world.gesture(|app| app.move_to((x + w / 2.0, y + h / 2.0)));
    world.advance(Duration::from_millis(200));
    let screen = world.screen().children(Target::key("shelf.peek.screen"))[0]
        .name
        .clone()
        .unwrap_or_default();
    assert_eq!(screen, "screen of r-c");
    world.gesture(|app| {
        app.click(Target::key("shelf.peek.c.field"));
        app.type_text("ship it");
    });
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Enter,
            mods: rune::Mods::default(),
        })
    });

    assert!(
        typed(machine(&world))
            .iter()
            .any(|(id, data)| id == "r-c" && data.contains("ship it")),
        "{:?}",
        typed(machine(&world))
    );
    assert!(
        world.daemon.sent_of("pty_input").is_empty()
            && world.daemon.sent_of("terminal_read_text").is_empty(),
        "nothing for this machine's terminal goes through the home"
    );

    let [x, y, w, h] = world.runtime().presented("tile.b").unwrap().0;
    world.gesture(|app| app.move_to((x + w / 2.0, y + h / 2.0)));
    let [x, y, w, h] = world.runtime().presented("shelf.entry.c").unwrap().0;
    world.gesture(|app| app.move_to((x + w / 2.0, y + h / 2.0)));
    world.advance(Duration::from_millis(200));
    world.gesture(|app| {
        app.click(Target::key("shelf.peek.c.field"));
        app.type_text("held");
        app.key(rune::KeyEvent {
            key: rune::Key::Enter,
            mods: rune::Mods::default(),
        });
    });
    machine(&world).disconnect();
    world.settle();
    assert!(
        world.screen().to_string().contains("may not have arrived"),
        "a reply this machine's dropped link never answered says so:\n{}",
        world.screen()
    );
}
