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
        Some("Open This Mac")
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
