use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    CommandUsage, Desktop, DesktopPane, GetCommandUsageResultMessage, InitialStateMessage, Profile,
    ProfileArrangementChangedMessage, RecordCommandUsageResultMessage, Session,
};
use attn_testkit::{Daemon, Request, Target, World, key_event, message};
use rune::{Key, KeyEvent, KeyPlatform, Mods};
use serde_json::{Value, json};

fn profile(id: &str, name: &str, current: &str) -> Profile {
    Profile {
        id: id.into(),
        name: name.into(),
        current_desktop_id: current.into(),
        ..Profile::default()
    }
}

fn desktop(id: &str, profile: &str, slot: i64, panes: Vec<DesktopPane>) -> Desktop {
    Desktop {
        id: id.into(),
        name: String::new(),
        order_key: format!("k{slot}"),
        shortcut_slot: Some(slot),
        active_pane_id: panes.first().map(|p| p.pane_id.clone()).unwrap_or_default(),
        panes,
        profile_id: profile.into(),
        ..Desktop::default()
    }
}

fn agent_pane(session: &str) -> DesktopPane {
    DesktopPane {
        desktop_id: "d1".into(),
        pane_id: format!("p-{session}"),
        session_id: session.into(),
        runtime_id: format!("rt-{session}"),
        kind: "agent".into(),
        title: session.into(),
        ..DesktopPane::default()
    }
}

fn session(id: &str) -> Session {
    Session {
        id: id.into(),
        label: id.into(),
        agent: "claude".into(),
        state: "idle".into(),
        profile_id: "work".into(),
        ..Session::default()
    }
}

fn initial(desktops: Vec<Desktop>, sessions: Vec<Session>) -> InitialStateMessage {
    InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![
            profile("work", "Work", "d1"),
            profile("side", "Side", "s1"),
        ]),
        desktops: Some(desktops),
        sessions: Some(sessions),
        settings: Some(BTreeMap::new()),
        ..InitialStateMessage::new()
    }
}

fn two_desktops() -> Vec<Desktop> {
    vec![
        desktop("d1", "work", 1, vec![]),
        desktop("d2", "work", 2, vec![]),
        desktop("s1", "side", 1, vec![]),
    ]
}

fn used(command: &str, score: f64, at: &str) -> CommandUsage {
    CommandUsage {
        command_id: command.into(),
        score,
        last_used_at: at.into(),
    }
}

fn history(request: &Request, entries: Vec<CommandUsage>) -> Value {
    message(GetCommandUsageResultMessage {
        request_id: request["request_id"].as_str().unwrap().into(),
        profile_id: request["profile_id"].as_str().unwrap().into(),
        success: true,
        entries,
        ..GetCommandUsageResultMessage::new()
    })
}

/// A daemon that answers every history read with `entries`.
fn daemon(state: InitialStateMessage, entries: Vec<CommandUsage>) -> Daemon {
    let daemon = Daemon::scripted().state(state);
    daemon
        .on("get_command_usage", move |r| history(r, entries.clone()))
        .on("record_command_usage", |r| {
            message(RecordCommandUsageResultMessage {
                request_id: r["request_id"].as_str().unwrap().into(),
                success: true,
                ..RecordCommandUsageResultMessage::new()
            })
        });
    daemon
}

fn hold(world: &World, cmd: &str) -> Arc<Mutex<Vec<Request>>> {
    let held: Arc<Mutex<Vec<Request>>> = Arc::default();
    let keep = held.clone();
    world
        .daemon
        .on(cmd, move |r| keep.lock().unwrap().push(r.clone()));
    held
}

fn chord(action: Action) -> KeyEvent {
    match Keymap::from_setting(None, KeyPlatform::current()).binding(&action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    }
}

fn press_action(world: &mut World, action: Action) {
    let ev = chord(action);
    world.gesture(|app| app.key(ev));
}

fn press(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

fn retype(world: &mut World, text: &str) {
    let field = world.screen().get(Target::key("palette.field"));
    for _ in 0..field.value.unwrap_or_default().chars().count() {
        press(world, Key::Backspace);
    }
    world.gesture(|app| app.type_text(text));
}

fn query(world: &World) -> String {
    world
        .screen()
        .get(Target::key("palette.field"))
        .value
        .unwrap_or_default()
}

fn titles(world: &World) -> Vec<String> {
    world
        .screen()
        .all(Target::role("list-box-option"))
        .into_iter()
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with("palette.row."))
        })
        .filter_map(|n| n.name)
        .collect()
}

fn highlighted(world: &World) -> Option<String> {
    world
        .screen()
        .all(Target::role("list-box-option"))
        .into_iter()
        .find(|n| n.is("selected"))
        .and_then(|n| n.name)
}

fn palette_named(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("palette.dialog"))
        .and_then(|n| n.name)
}

fn text_of(world: &World, key: &str) -> Option<String> {
    world.screen().find(Target::key(key)).and_then(|n| n.name)
}

fn records(world: &World) -> Vec<(String, String)> {
    world
        .daemon
        .sent_of("record_command_usage")
        .iter()
        .map(|r| {
            (
                r["profile_id"].as_str().unwrap().to_owned(),
                r["command_id"].as_str().unwrap().to_owned(),
            )
        })
        .collect()
}

fn notice(world: &World) -> Option<String> {
    text_of(world, "notice.text")
}

fn rest(world: &mut World) {
    world.advance(Duration::from_secs(6));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_palette_keeps_the_query_while_history_loads_and_offers_commands_once_it_arrives() {
    let mut world = World::start(daemon(initial(two_desktops(), vec![]), vec![]));
    let asked = hold(&world, "get_command_usage");
    press_action(&mut world, Action::UiCommandPalette);
    assert_eq!(
        text_of(&world, "palette.empty").as_deref(),
        Some("Loading command history…")
    );

    world.gesture(|app| app.type_text("keyboard"));
    press(&mut world, Key::Enter);
    assert!(records(&world).is_empty());

    let request = asked.lock().unwrap().remove(0);
    world.daemon.reply_to(&request, history(&request, vec![]));
    world.settle();

    assert_eq!(query(&world), ">keyboard");
    assert_eq!(titles(&world), ["Keyboard shortcuts"]);
    assert_eq!(text_of(&world, "palette.count").as_deref(), Some("1 of 12"));
    assert_eq!(world.daemon.sent_of("get_command_usage").len(), 1);
    assert!(records(&world).is_empty());
    rest(&mut world);
}

#[test]
fn history_holds_still_while_the_palette_is_open_and_each_opening_reads_it_afresh() {
    let mut world = World::start(daemon(initial(two_desktops(), vec![]), vec![]));
    let asked = hold(&world, "get_command_usage");
    press_action(&mut world, Action::UiCommandPalette);
    let first = asked.lock().unwrap().remove(0);
    world.daemon.reply_to(
        &first,
        history(
            &first,
            vec![used("keyboard-shortcuts", 5.0, "2026-10-04T12:00:00Z")],
        ),
    );
    world.settle();
    assert_eq!(titles(&world)[0], "Keyboard shortcuts");

    press(&mut world, Key::Escape);
    press_action(&mut world, Action::UiCommandPalette);
    assert!(titles(&world).is_empty());
    let second = asked.lock().unwrap().remove(0);
    world.daemon.reply_to(
        &second,
        history(&second, vec![used("home", 8.0, "2026-10-04T12:00:00Z")]),
    );
    world.settle();

    assert_eq!(titles(&world)[0], "Home");
    assert_eq!(world.daemon.sent_of("get_command_usage").len(), 2);
    rest(&mut world);
}

#[test]
fn a_command_picked_by_enter_or_by_pointer_counts_once_and_runs() {
    for by_pointer in [false, true] {
        let mut world = World::start(daemon(initial(two_desktops(), vec![]), vec![]));
        if by_pointer {
            press_action(&mut world, Action::UiActionMenu);
            world.gesture(|app| app.type_text(">keyboard"));
            world.gesture(|app| app.click(Target::key("palette.row.command:keyboard-shortcuts")));
        } else {
            press_action(&mut world, Action::UiCommandPalette);
            world.gesture(|app| app.type_text("keyboard"));
            press(&mut world, Key::Enter);
        }

        assert_eq!(
            records(&world),
            [("work".to_owned(), "keyboard-shortcuts".to_owned())]
        );
        assert!(world.screen().find(Target::key("palette")).is_none());
        assert!(
            world
                .screen()
                .find(Target::role("dialog").name("Keyboard shortcuts"))
                .is_some()
        );
        assert_eq!(world.daemon.sent_of("get_command_usage").len(), 1);
        rest(&mut world);
    }
}

#[test]
fn a_stronger_match_beats_pins_and_use_and_the_agent_on_screen_pins_its_commands() {
    let mut world = World::start(daemon(
        initial(
            vec![desktop("d1", "work", 1, vec![agent_pane("s1")])],
            vec![session("s1")],
        ),
        vec![
            used("home", 100.0, "2026-10-04T12:00:00Z"),
            used("new-agent", 20.0, "2026-10-04T12:00:00Z"),
        ],
    ));
    world.open_desktop();
    press_action(&mut world, Action::UiCommandPalette);

    let all = titles(&world);
    let at = |title: &str| all.iter().position(|t| t == title).unwrap();
    assert!(at("Mark as priority") < at("Home"), "{all:?}");
    assert!(at("Home") < at("New agent"), "{all:?}");

    world.gesture(|app| app.type_text("agent"));
    let matched = titles(&world);
    let at = |title: &str| matched.iter().position(|t| t == title).unwrap();
    assert!(at("New agent") < at("Mark as priority"), "{matched:?}");
    assert_eq!(world.daemon.sent_of("get_command_usage").len(), 1);
    rest(&mut world);
}

#[test]
fn equal_use_goes_by_recency_and_unused_commands_keep_their_declared_order() {
    let mut world = World::start(daemon(
        initial(two_desktops(), vec![]),
        vec![
            used("home", 2.0, "2026-10-01T12:00:00Z"),
            used("keyboard-shortcuts", 2.0, "2026-10-04T12:00:00Z"),
        ],
    ));
    press_action(&mut world, Action::UiCommandPalette);

    assert_eq!(
        titles(&world)[..4],
        [
            "Keyboard shortcuts",
            "Home",
            "New agent",
            "Jump to the oldest turn"
        ]
    );
    rest(&mut world);
}

#[test]
fn a_late_answer_is_dropped_and_failed_reads_and_writes_say_so_without_blocking_commands() {
    let mut world = World::start(daemon(initial(two_desktops(), vec![]), vec![]));
    let asked = hold(&world, "get_command_usage");
    press_action(&mut world, Action::UiCommandPalette);
    press(&mut world, Key::Escape);
    press_action(&mut world, Action::UiCommandPalette);
    let stale = asked.lock().unwrap().remove(0);
    world.daemon.reply_to(&stale, history(&stale, vec![]));
    world.settle();
    assert!(titles(&world).is_empty());

    let current = asked.lock().unwrap().remove(0);
    world.daemon.reply_to(
        &current,
        message(GetCommandUsageResultMessage {
            request_id: current["request_id"].as_str().unwrap().into(),
            profile_id: "work".into(),
            success: false,
            error: Some("History unavailable".into()),
            ..GetCommandUsageResultMessage::new()
        }),
    );
    world.settle();
    assert_eq!(titles(&world)[0], "New agent");
    assert_eq!(
        notice(&world).as_deref(),
        Some("Command history: History unavailable")
    );

    world.daemon.on("record_command_usage", |r| {
        message(RecordCommandUsageResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: false,
            error: Some("History write refused".into()),
            ..RecordCommandUsageResultMessage::new()
        })
    });
    world.gesture(|app| app.type_text("keyboard"));
    press(&mut world, Key::Enter);

    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Keyboard shortcuts"))
            .is_some()
    );
    assert_eq!(
        notice(&world).as_deref(),
        Some("Command history: History write refused")
    );
    assert_eq!(records(&world).len(), 1);
    assert_eq!(world.daemon.sent_of("get_command_usage").len(), 2);
    rest(&mut world);
}

#[test]
fn a_shortcut_pressed_directly_counts_toward_nothing() {
    let mut world = World::start(daemon(initial(two_desktops(), vec![]), vec![]));
    press_action(&mut world, Action::UiShowShortcuts);

    assert!(records(&world).is_empty());
    assert!(world.daemon.sent_of("get_command_usage").is_empty());
    rest(&mut world);
}

#[test]
fn the_palette_turns_to_commands_on_a_leading_angle_and_its_keys_turn_or_close_it() {
    let mut world = World::start(daemon(initial(two_desktops(), vec![]), vec![]));
    press_action(&mut world, Action::UiActionMenu);
    assert_eq!(palette_named(&world).as_deref(), Some("Agents"));
    assert_eq!(
        text_of(&world, "palette.empty").as_deref(),
        Some("No match")
    );

    world.gesture(|app| app.type_text(">home"));
    assert_eq!(palette_named(&world).as_deref(), Some("Commands"));
    assert_eq!(titles(&world), ["Home"]);
    assert_eq!(text_of(&world, "palette.count").as_deref(), Some("1 of 12"));

    press_action(&mut world, Action::UiActionMenu);
    assert_eq!(query(&world), "home");
    assert_eq!(palette_named(&world).as_deref(), Some("Agents"));
    press_action(&mut world, Action::UiCommandPalette);
    assert_eq!(query(&world), ">home");
    press_action(&mut world, Action::UiCommandPalette);
    assert!(world.screen().find(Target::key("palette")).is_none());

    press_action(&mut world, Action::UiCommandPalette);
    assert_eq!(query(&world), ">");
    press(&mut world, Key::Backspace);
    assert_eq!(palette_named(&world).as_deref(), Some("Agents"));
    assert_eq!(world.daemon.sent_of("get_command_usage").len(), 2);
    rest(&mut world);
}

#[test]
fn arrows_move_the_highlight_without_wrapping_and_an_edit_puts_it_back_on_top() {
    let mut world = World::start(daemon(initial(two_desktops(), vec![]), vec![]));
    press_action(&mut world, Action::UiCommandPalette);
    world.gesture(|app| app.type_text("go to"));
    assert_eq!(titles(&world), ["Go to Desktop 1", "Go to Desktop 2"]);
    assert_eq!(highlighted(&world).as_deref(), Some("Go to Desktop 1"));

    press(&mut world, Key::Up);
    assert_eq!(highlighted(&world).as_deref(), Some("Go to Desktop 1"));
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    assert_eq!(highlighted(&world).as_deref(), Some("Go to Desktop 2"));

    world.gesture(|app| app.type_text(" "));
    assert_eq!(highlighted(&world).as_deref(), Some("Go to Desktop 1"));
    press(&mut world, Key::Down);
    press(&mut world, Key::Enter);

    let current: Vec<String> = world
        .daemon
        .sent_of("desktop_set_current")
        .iter()
        .map(|r| r["desktop_id"].as_str().unwrap().to_owned())
        .collect();
    assert_eq!(current, ["d2"]);
    rest(&mut world);
}

#[test]
fn the_highlight_stays_on_its_command_as_rows_arrive_and_enter_does_nothing_with_none() {
    let mut world = World::start(daemon(initial(two_desktops(), vec![]), vec![]));
    press_action(&mut world, Action::UiCommandPalette);
    world.gesture(|app| app.type_text("go to"));
    press(&mut world, Key::Down);
    assert_eq!(highlighted(&world).as_deref(), Some("Go to Desktop 2"));

    let mut grown = two_desktops();
    grown.insert(0, desktop("d0", "work", 0, vec![]));
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile("work", "Work", "d1"),
        desktops: grown,
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
    assert_eq!(titles(&world)[0], "Go to Desktop 10");
    assert_eq!(highlighted(&world).as_deref(), Some("Go to Desktop 2"));

    retype(&mut world, ">nothing matches this");
    assert_eq!(
        text_of(&world, "palette.empty").as_deref(),
        Some("No matching commands")
    );
    press(&mut world, Key::Enter);
    assert!(records(&world).is_empty());
    assert!(world.screen().find(Target::key("palette")).is_some());
    rest(&mut world);
}

#[test]
fn the_pointer_highlights_the_row_it_moves_over() {
    let mut world = World::start(daemon(initial(two_desktops(), vec![]), vec![]));
    press_action(&mut world, Action::UiCommandPalette);
    world.gesture(|app| app.type_text("go to"));

    world
        .automate(
            "hover",
            json!({"target": {"key": "palette.row.command:desktop-d2"}}),
        )
        .unwrap();
    world.settle();

    assert_eq!(highlighted(&world).as_deref(), Some("Go to Desktop 2"));
    rest(&mut world);
}

#[test]
fn every_command_the_palette_offers_runs_in_the_native_app() {
    let mut world = World::start(daemon(
        initial(
            vec![desktop("d1", "work", 1, vec![agent_pane("s1")])],
            vec![session("s1")],
        ),
        vec![],
    ));
    world.open_desktop();
    press_action(&mut world, Action::UiCommandPalette);
    let offered = titles(&world);
    press(&mut world, Key::Escape);
    assert!(offered.len() >= 12, "{offered:?}");

    for title in offered {
        press_action(&mut world, Action::UiCommandPalette);
        let at = titles(&world).iter().position(|t| *t == title).unwrap();
        for _ in 0..at {
            press(&mut world, Key::Down);
        }
        press(&mut world, Key::Enter);
        let said = notice(&world).unwrap_or_default();
        assert!(
            !said.starts_with("Not in the native app yet"),
            "{title}: {said}"
        );
        while world.screen().find(Target::role("dialog")).is_some() {
            press(&mut world, Key::Escape);
        }
    }
    rest(&mut world);
}
