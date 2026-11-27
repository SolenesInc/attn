//! The commands an ask offers open in terminals the ask holds, drawn in its panel.

use attn_app::Action;
use attn_protocol::{
    Ask, AskReference, AttachResultMessage, InitialStateMessage, OutpostInfo, Session,
    SessionRegisteredMessage, TerminalBinding, TerminalBindingsUpdatedMessage,
};
use attn_testkit::{Target, World, message};

use super::asks::{
    api_working, ask, click, has, letter, name, open_sheet, prose, question, start_on,
};
use super::queue::one_desktop;
use super::sidebar::chord;
use super::terminal::output_frame_to;

const ASK: &str = "a-far";

/// An ask from an agent on outpost far: one command addressed to a folder there, one at the
/// ask's own place.
fn asked() -> Ask {
    let mut asked = ask(
        ASK,
        "api",
        "Check the shop",
        5,
        vec![
            question("q1", "single", "Ship it?", &["Yes", "No"]),
            prose(
                "Run the checks:\n\n```sh far:/srv/shop\nmake check\n```\n\nThen list it:\n\n```bash\nls -la\n```\n",
            ),
        ],
    );
    asked.base_dir = "/far/agent".into();
    asked.host = Some("host-1".into());
    asked.cwd = Some("/far/agent".into());
    asked.references = Some(vec![AskReference {
        kind: "command".into(),
        text: "far:/srv/shop".into(),
        host: Some("host-1".into()),
        path: "/srv/shop".into(),
        line: 3,
    }]);
    asked
}

fn far_state() -> InitialStateMessage {
    let mut state = one_desktop(vec![Session {
        host: Some("host-1".into()),
        ..api_working()
    }]);
    state.outposts = Some(vec![OutpostInfo {
        id: "host-1".into(),
        name: "far".into(),
        destination: "far".into(),
        state: "connected".into(),
        reach: "reachable".into(),
        ..OutpostInfo::default()
    }]);
    state
}

fn start(state: InitialStateMessage) -> World {
    let world = start_on(state, vec![asked()], &[]);
    world.daemon.on("attach_session", |r| {
        message(AttachResultMessage {
            id: r["id"].as_str().unwrap().into(),
            success: true,
            running: Some(true),
            ..AttachResultMessage::new()
        })
    });
    world
}

fn shell(id: &str, directory: &str) -> Session {
    Session {
        id: id.into(),
        label: id.into(),
        agent: "shell".into(),
        state: "idle".into(),
        directory: directory.into(),
        host: Some("host-1".into()),
        profile_id: "work".into(),
        ..Session::default()
    }
}

fn held(session: &str, terminal: &str) -> TerminalBinding {
    TerminalBinding {
        ask_id: Some(ASK.into()),
        session_id: session.into(),
        terminal_id: terminal.into(),
    }
}

/// The home answers a spawn into the ask: the shell's session, then the ask's binding to it.
fn bound(world: &mut World, session: &str, directory: &str, terminal: &str) {
    world.daemon.emit(SessionRegisteredMessage {
        session: shell(session, directory),
        ..SessionRegisteredMessage::new()
    });
    world.daemon.emit(TerminalBindingsUpdatedMessage {
        terminal_bindings: vec![held(session, terminal)],
        ..TerminalBindingsUpdatedMessage::new()
    });
    world.settle();
}

fn spawns(world: &World) -> Vec<serde_json::Value> {
    world.daemon.sent_of("spawn_session")
}

fn slots(world: &World, terminal: &str) -> usize {
    world
        .screen()
        .all(Target::key(format!("terminal.{terminal}")))
        .len()
}

fn typed(world: &World, terminal: &str) -> String {
    (world.daemon.sent_of("pty_input").iter())
        .filter(|r| r["id"] == terminal)
        .map(|r| r["data"].as_str().unwrap_or_default().to_owned())
        .collect()
}

#[test]
fn a_command_opens_in_a_terminal_the_ask_holds_where_its_block_names_typed_and_holding_the_keys() {
    let mut world = start(far_state());
    open_sheet(&mut world);
    assert_eq!(name(&world, "asks.command.0.place"), "far · /srv/shop");
    assert_eq!(
        name(&world, "asks.command.1.place"),
        "far · /far/agent",
        "a block with no address runs at the ask's place"
    );

    letter(&mut world, "t");
    let sent = spawns(&world);
    assert_eq!(sent.len(), 1, "t opens the first command");
    let spawn = &sent[0];
    assert_eq!(spawn["agent"], "shell");
    assert_eq!(spawn["host"], "host-1");
    assert_eq!(spawn["cwd"], "/srv/shop");
    assert_eq!(spawn["typed"], "make check");
    assert_eq!(spawn["placement"]["ask_id"], ASK);
    assert!(spawn["placement"]["desktop_id"].is_null(), "{spawn}");
    assert!(has(&world, "asks.panel.tab.0"));
    assert_eq!(name(&world, "asks.panel.note"), "Starting a shell…");

    let session = spawn["id"].as_str().unwrap().to_owned();
    bound(&mut world, &session, "/srv/shop", "rt-1");
    assert_eq!(
        slots(&world, "rt-1"),
        1,
        "one slot, in the panel\n{}",
        world.screen()
    );
    assert_eq!(name(&world, "asks.panel.tab.0"), "shop · far");
    assert!(has(&world, "asks.sheet"), "Asks stays open");
    assert!(
        world
            .daemon
            .sent_of("attach_session")
            .iter()
            .any(|r| r["id"] == "rt-1"),
        "the panel's terminal attaches"
    );

    world.gesture(|app| app.type_text("x"));
    world.gesture(|app| app.runtime.paste("p"));
    world.gesture(|app| app.press("Tab"));
    world.gesture(|app| app.press("Escape"));
    world.settle();
    assert_eq!(
        typed(&world, "rt-1"),
        "xp\t\u{1b}",
        "keys, a paste, Tab and Escape are the shell's"
    );
    assert!(has(&world, "asks.sheet"), "Escape stays in the shell");

    world.gesture(|app| app.key(chord(Action::DockAsks)));
    world.settle();
    assert!(
        has(&world, "asks.sheet"),
        "the Asks chord hands the keys back, Asks stays"
    );
    letter(&mut world, "y");
    assert_eq!(
        typed(&world, "rt-1"),
        "xp\t\u{1b}",
        "the question holds the keys again"
    );

    click(&mut world, "asks.command.1.open");
    let sent = spawns(&world);
    assert_eq!(sent.len(), 2);
    assert_eq!(sent[1]["cwd"], "/far/agent");
    assert_eq!(sent[1]["typed"], "ls -la");
    assert!(has(&world, "asks.panel.tab.1"));

    click(&mut world, "asks.command.0.open");
    assert_eq!(
        spawns(&world).len(),
        2,
        "a command already open shows its terminal again"
    );
    assert_eq!(slots(&world, "rt-1"), 1);
}

#[test]
fn the_send_button_reads_send_and_stop_while_a_command_runs_in_an_ask_terminal() {
    let mut world = start(far_state());
    open_sheet(&mut world);
    letter(&mut world, "t");
    let session = spawns(&world)[0]["id"].as_str().unwrap().to_owned();
    bound(&mut world, &session, "/srv/shop", "rt-1");
    assert!(
        name(&world, "asks.send").starts_with("Send "),
        "{}",
        name(&world, "asks.send")
    );
    assert!(!name(&world, "asks.send").contains("stop"));

    world.daemon.emit_frame(output_frame_to(
        "rt-1",
        1,
        b"\x1b]133;A\x07$ \x1b]133;B\x07make check\r\n\x1b]133;C\x07running\r\n",
    ));
    world.settle();
    assert!(
        name(&world, "asks.send").starts_with("Send and stop"),
        "{}",
        name(&world, "asks.send")
    );

    world.daemon.emit_frame(output_frame_to(
        "rt-1",
        2,
        b"\x1b]133;D;0\x07\x1b]133;A\x07$ ",
    ));
    world.settle();
    assert!(
        !name(&world, "asks.send").contains("stop"),
        "{}",
        name(&world, "asks.send")
    );
}

#[test]
fn move_to_desktop_asks_the_home_to_give_the_shell_a_tile_and_its_tab_goes() {
    let mut world = start(far_state());
    open_sheet(&mut world);
    letter(&mut world, "t");
    let session = spawns(&world)[0]["id"].as_str().unwrap().to_owned();
    bound(&mut world, &session, "/srv/shop", "rt-1");

    click(&mut world, "asks.panel.move");
    let moved = world.daemon.sent_of("desktop_show_session");
    assert_eq!(moved.len(), 1);
    assert_eq!(moved[0]["session_id"], session.as_str());
    assert!(
        !has(&world, "asks.panel"),
        "the tab goes at once\n{}",
        world.screen()
    );
}

#[test]
fn closing_a_terminal_tab_ends_its_shell() {
    let mut world = start(far_state());
    open_sheet(&mut world);
    letter(&mut world, "t");
    let session = spawns(&world)[0]["id"].as_str().unwrap().to_owned();
    bound(&mut world, &session, "/srv/shop", "rt-1");

    click(&mut world, "asks.panel.close-tab");
    let ended = world.daemon.sent_of("unregister");
    assert_eq!(ended.len(), 1);
    assert_eq!(ended[0]["id"], session.as_str());
    assert!(!has(&world, "asks.panel"));
}

#[test]
fn a_restarted_app_shows_the_terminals_an_ask_holds_in_its_panel() {
    let mut state = far_state();
    state
        .sessions
        .get_or_insert_default()
        .push(shell("held", "/srv/shop"));
    state.terminal_bindings = Some(vec![held("held", "rt-9")]);
    let mut world = start(state);
    open_sheet(&mut world);
    assert!(has(&world, "asks.panel.tab.0"), "{}", world.screen());
    assert_eq!(slots(&world, "rt-9"), 1, "{}", world.screen());
}
