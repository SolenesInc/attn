use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    AutomationProvenance, CommandUsage, Desktop, DesktopPane, GetCommandUsageResultMessage,
    InitialStateMessage, Profile, ProfileArrangementChangedMessage,
    RecordCommandUsageResultMessage, Session, SessionStateChangedMessage,
    SessionUnregisteredMessage,
};
use attn_testkit::{Daemon, Request, Target, World, key_event, message};
use rune::{Key, KeyEvent, KeyPlatform, Mods};
use serde_json::{Value, json};

use super::queue::ago;

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
pub(super) fn daemon(state: InitialStateMessage, entries: Vec<CommandUsage>) -> Daemon {
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

fn shortcuts_used(world: &World) -> Vec<String> {
    world
        .daemon
        .sent_of("record_analytics")
        .iter()
        .filter(|r| r["name"] == "shortcut")
        .map(|r| r["props"]["id"].as_str().unwrap().to_owned())
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
    assert_eq!(
        titles(&world),
        [
            "Keyboard shortcuts",
            "Customize keyboard shortcuts",
            "Create diagnostic report"
        ]
    );
    assert!(text_of(&world, "palette.count").is_some_and(|c| c.starts_with("3 of ")));
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
        let mut state = initial(two_desktops(), vec![]);
        state.settings = Some(BTreeMap::from([(
            "analytics.enabled".to_owned(),
            "true".to_owned(),
        )]));
        let mut world = World::start(daemon(state, vec![]));
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
        let opened_by = if by_pointer {
            Action::UiActionMenu
        } else {
            Action::UiCommandPalette
        };
        assert_eq!(shortcuts_used(&world), [opened_by.id()]);
        rest(&mut world);
    }
}

#[test]
fn the_title_bars_commands_and_new_agent_buttons_open_the_command_palette_and_the_picker() {
    let mut state = initial(two_desktops(), vec![]);
    state.settings = Some(BTreeMap::from([(
        "analytics.enabled".to_owned(),
        "true".to_owned(),
    )]));
    let mut world = World::start(daemon(state, vec![]));

    world.gesture(|app| app.click(Target::key("titlebar.commands")));
    assert_eq!(palette_named(&world).as_deref(), Some("Commands"));
    press(&mut world, Key::Escape);
    assert!(world.screen().find(Target::key("palette")).is_none());

    world.gesture(|app| app.click(Target::key("titlebar.new")));
    assert!(world.screen().find(Target::key("new-session")).is_some());
    assert!(shortcuts_used(&world).is_empty());
    rest(&mut world);
}

#[test]
fn a_desktop_command_shows_the_keys_of_its_slot() {
    let mut world = World::start(daemon(initial(two_desktops(), vec![]), vec![]));
    press_action(&mut world, Action::UiCommandPalette);
    world.gesture(|app| app.type_text("go to"));

    let keymap = Keymap::from_setting(None, KeyPlatform::current());
    for (desktop, select) in [
        ("d1", Action::DesktopSelect1),
        ("d2", Action::DesktopSelect2),
    ] {
        assert_eq!(
            text_of(
                &world,
                &format!("palette.row.command:desktop-{desktop}.shortcut")
            ),
            keymap.binding(&select).map(|b| keymap.label(b))
        );
    }
    rest(&mut world);
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
    assert!(text_of(&world, "palette.count").is_some_and(|c| c.starts_with("1 of ")));

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
    world.gesture(|app| app.type_text("go to desk"));
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

fn crew_and_queue() -> InitialStateMessage {
    let member = |id: &str, chief: bool| Session {
        crew_member: Some(if chief { "chief" } else { id }.into()),
        chief_of_staff: chief.then_some(true),
        ..session(id)
    };
    let turn = |id: &str, minutes: i64| Session {
        turn_owed: Some(true),
        turn_opened_at: Some(ago(minutes)),
        state: "waiting_input".into(),
        ..session(id)
    };
    let run = Session {
        automation: Some(AutomationProvenance {
            definition_name: "nightly".into(),
            run_id: "r1".into(),
            trigger_type: "schedule".into(),
            ..AutomationProvenance::default()
        }),
        ..session("run-1")
    };
    let snoozed = Session {
        turn_owed: Some(true),
        turn_snoozed_until: Some("2099-01-01T00:00:00Z".into()),
        ..session("snoozed-agent")
    };
    let sessions = vec![
        member("chief-1", true),
        member("gardener", false),
        turn("newer-turn", 10),
        settled_working("settled-agent"),
        run,
        turn("older-turn", 120),
        snoozed,
    ];
    let on = |desktop: &str, ids: &[&str]| -> Vec<DesktopPane> {
        ids.iter()
            .map(|id| DesktopPane {
                desktop_id: desktop.into(),
                ..agent_pane(id)
            })
            .collect()
    };
    let mut d1 = desktop(
        "d1",
        "work",
        1,
        on(
            "d1",
            &[
                "chief-1",
                "gardener",
                "older-turn",
                "settled-agent",
                "run-1",
            ],
        ),
    );
    d1.tree_json = r#"{"type":"tile","tile_id":"tile-docs","tile_kind":"browser","tile_params":"https://docs.test"}"#.into();
    let mut d2 = desktop("d2", "work", 0, on("d2", &["newer-turn", "snoozed-agent"]));
    d2.shortcut_slot = None;
    let mut state = initial(vec![d1, d2], sessions);
    state.settings = Some(BTreeMap::from([(
        "queue_mode_enabled".to_owned(),
        "true".to_owned(),
    )]));
    state
}

fn settled_working(id: &str) -> Session {
    Session {
        state: "working".into(),
        ..session(id)
    }
}

/// At rest but for the next minute a turn's age reads differently, as the queue sidebar
/// beside the palette wakes for it.
fn rest_but_for_ages(world: &mut World) {
    world.advance(Duration::from_secs(6));
    let now = world.now().as_secs_f64();
    let wake = world.runtime().wake_deadline();
    assert!(wake.is_some_and(|at| at - now >= 20.0), "{wake:?} at {now}");
}

fn open_agents(world: &mut World) {
    press_action(world, Action::UiActionMenu);
}

fn row_text(world: &World, row: &str, part: &str) -> Option<String> {
    text_of(world, &format!("palette.row.{row}.{part}"))
}

fn hints(world: &World) -> Vec<String> {
    (0..)
        .map_while(|i| text_of(world, &format!("palette.hint.{i}")))
        .collect()
}

#[test]
fn the_agents_face_lists_the_crew_then_turns_agents_runs_snoozed_agents_and_tiles() {
    let mut world = World::start(daemon(crew_and_queue(), vec![]));
    open_agents(&mut world);

    assert_eq!(
        titles(&world),
        [
            "chief-1",
            "gardener",
            "older-turn",
            "newer-turn",
            "settled-agent",
            "run-1",
            "snoozed-agent",
            "docs.test"
        ]
    );
    assert_eq!(text_of(&world, "palette.count").as_deref(), Some("8 of 8"));
    assert_eq!(highlighted(&world).as_deref(), Some("chief-1"));

    let keymap = Keymap::from_setting(None, KeyPlatform::current());
    let label = |action: Action| keymap.binding(&action).map(|b| keymap.label(b));
    assert_eq!(
        row_text(&world, "agent:older-turn", "tag"),
        label(Action::SessionJumpToWaiting)
    );
    assert_eq!(row_text(&world, "agent:newer-turn", "tag"), None);
    assert_eq!(
        row_text(&world, "agent:older-turn", "age").as_deref(),
        Some("2h")
    );
    assert_eq!(
        row_text(&world, "agent:older-turn", "slot"),
        label(Action::DesktopSelect1)
    );
    assert_eq!(
        row_text(&world, "agent:newer-turn", "slot").as_deref(),
        Some("·")
    );
    let statuses: Vec<_> = [
        "chief-1",
        "older-turn",
        "settled-agent",
        "run-1",
        "snoozed-agent",
    ]
    .iter()
    .map(|id| row_text(&world, &format!("agent:{id}"), "status").unwrap())
    .collect();
    assert_eq!(statuses, ["chief", "waiting", "working", "idle", "snoozed"]);
    assert_eq!(
        row_text(&world, "agent:gardener", "crew").as_deref(),
        Some("· crew")
    );
    assert_eq!(row_text(&world, "agent:chief-1", "crew"), None);
    assert_eq!(
        row_text(&world, "tile:d1:tile-docs", "kind").as_deref(),
        Some("WEB")
    );
    rest_but_for_ages(&mut world);
}

#[test]
fn a_query_filters_every_kind_of_row_and_drops_the_queue_tag() {
    let mut world = World::start(daemon(crew_and_queue(), vec![]));
    open_agents(&mut world);

    world.gesture(|app| app.type_text("nightly"));
    assert_eq!(titles(&world), ["run-1"]);
    assert_eq!(text_of(&world, "palette.count").as_deref(), Some("1 of 8"));

    retype(&mut world, "web");
    assert_eq!(titles(&world), ["docs.test"]);

    retype(&mut world, "gardener");
    assert_eq!(titles(&world), ["gardener"]);

    retype(&mut world, "turn");
    assert_eq!(titles(&world), ["older-turn", "newer-turn"]);
    assert_eq!(row_text(&world, "agent:older-turn", "tag"), None);

    retype(&mut world, "nobody");
    assert_eq!(
        text_of(&world, "palette.empty").as_deref(),
        Some("No match")
    );
    rest_but_for_ages(&mut world);
}

#[test]
fn arrows_step_over_the_divider_and_a_pick_shows_the_agent_or_tile_and_records_nothing() {
    let mut world = World::start(daemon(crew_and_queue(), vec![]));
    open_agents(&mut world);
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    assert_eq!(highlighted(&world).as_deref(), Some("older-turn"));
    press(&mut world, Key::Up);
    assert_eq!(highlighted(&world).as_deref(), Some("gardener"));
    press(&mut world, Key::Down);
    press(&mut world, Key::Enter);

    assert!(world.screen().find(Target::key("palette")).is_none());
    let shown = world.daemon.sent_of("desktop_show_session");
    assert_eq!(shown.len(), 1);
    assert_eq!(shown[0]["session_id"], "older-turn");

    open_agents(&mut world);
    world.gesture(|app| app.type_text("docs"));
    world.gesture(|app| app.click(Target::key("palette.row.tile:d1:tile-docs")));
    let leaves = world.daemon.sent_of("desktop_show_leaf");
    assert_eq!(leaves.len(), 1);
    assert_eq!(leaves[0]["leaf_id"], "tile-docs");
    assert!(records(&world).is_empty());
    rest_but_for_ages(&mut world);
}

#[test]
fn the_queue_keys_act_on_the_highlighted_agent_and_leave_the_palette_open() {
    let mut world = World::start(daemon(crew_and_queue(), vec![]));
    open_agents(&mut world);
    world.gesture(|app| app.type_text("older"));

    let footer = hints(&world);
    assert!(footer.iter().any(|h| h.ends_with(" settle")), "{footer:?}");
    assert!(footer.iter().any(|h| h.ends_with(" snooze")), "{footer:?}");

    press_action(&mut world, Action::SessionSettle);
    press_action(&mut world, Action::SessionPriority);

    let settled = world.daemon.sent_of("settle_turn");
    assert_eq!(settled.len(), 1);
    assert_eq!(settled[0]["session_id"], "older-turn");
    let priority = world.daemon.sent_of("set_session_priority");
    assert_eq!(priority.len(), 1);
    assert_eq!(priority[0]["session_id"], "older-turn");
    assert_eq!(priority[0]["priority"], true);
    assert_eq!(palette_named(&world).as_deref(), Some("Agents"));

    retype(&mut world, "run");
    let footer = hints(&world);
    assert!(!footer.iter().any(|h| h.contains("settle")), "{footer:?}");
    press_action(&mut world, Action::SessionSettle);
    assert_eq!(world.daemon.sent_of("settle_turn").len(), 1);
    rest_but_for_ages(&mut world);
}

#[test]
fn snoozing_from_the_palette_offers_the_choices_and_comes_back_to_the_agent() {
    let mut world = World::start(daemon(crew_and_queue(), vec![]));
    open_agents(&mut world);
    world.gesture(|app| app.type_text("older"));

    press_action(&mut world, Action::SessionSnooze);
    assert_eq!(palette_named(&world).as_deref(), Some("Snooze older-turn"));
    assert_eq!(query(&world), "");
    assert_eq!(
        titles(&world),
        [
            "For 30 minutes",
            "For an hour",
            "For 2 hours",
            "For 4 hours",
            "For 8 hours",
            "Until tomorrow",
            "Until Saturday",
            "Until Monday"
        ]
    );
    press(&mut world, Key::Down);
    press(&mut world, Key::Enter);

    let snoozed = world.daemon.sent_of("snooze_turn");
    assert_eq!(snoozed.len(), 1);
    assert_eq!(snoozed[0]["session_id"], "older-turn");
    assert_eq!(palette_named(&world).as_deref(), Some("Agents"));
    assert_eq!(query(&world), "older");
    assert_eq!(highlighted(&world).as_deref(), Some("older-turn"));

    press_action(&mut world, Action::SessionSnooze);
    press(&mut world, Key::Escape);
    assert_eq!(palette_named(&world).as_deref(), Some("Agents"));
    assert_eq!(query(&world), "older");

    press_action(&mut world, Action::SessionSnooze);
    world.daemon.emit(SessionUnregisteredMessage {
        session: session("older-turn"),
        ..SessionUnregisteredMessage::new()
    });
    world.settle();
    assert_eq!(palette_named(&world).as_deref(), Some("Agents"));
    assert_eq!(query(&world), "older");
    assert_eq!(world.daemon.sent_of("snooze_turn").len(), 1);
    rest_but_for_ages(&mut world);
}

#[test]
fn the_highlight_stays_on_its_agent_when_the_queue_reorders() {
    let mut world = World::start(daemon(crew_and_queue(), vec![]));
    open_agents(&mut world);
    world.gesture(|app| app.type_text("turn"));
    press(&mut world, Key::Down);
    assert_eq!(highlighted(&world).as_deref(), Some("newer-turn"));

    world.daemon.emit(SessionStateChangedMessage {
        session: Session {
            priority: Some(true),
            turn_owed: Some(true),
            turn_opened_at: Some(ago(10)),
            state: "waiting_input".into(),
            ..session("newer-turn")
        },
        ..SessionStateChangedMessage::new()
    });
    world.settle();

    assert_eq!(titles(&world), ["newer-turn", "older-turn"]);
    assert_eq!(highlighted(&world).as_deref(), Some("newer-turn"));
    rest_but_for_ages(&mut world);
}

#[test]
fn either_palette_key_leaves_the_snooze_choices_for_its_face() {
    let mut world = World::start(daemon(crew_and_queue(), vec![]));
    open_agents(&mut world);
    world.gesture(|app| app.type_text("older"));

    press_action(&mut world, Action::SessionSnooze);
    press_action(&mut world, Action::UiCommandPalette);
    assert_eq!(palette_named(&world).as_deref(), Some("Commands"));
    assert_eq!(query(&world), ">older");

    press_action(&mut world, Action::UiActionMenu);
    press_action(&mut world, Action::SessionSnooze);
    assert_eq!(palette_named(&world).as_deref(), Some("Snooze older-turn"));
    press_action(&mut world, Action::UiActionMenu);
    assert_eq!(palette_named(&world).as_deref(), Some("Agents"));
    assert_eq!(query(&world), "older");
    rest_but_for_ages(&mut world);
}

#[test]
fn a_settle_bound_to_a_sequence_says_it_has_no_keys_here_and_does_nothing() {
    let settle = chord(Action::SessionSettle);
    let mut state = crew_and_queue();
    state.settings.get_or_insert_default().insert(
        "keybindings_config".into(),
        r#"{"version":1,"overrides":{"session.settle":{"leader":{"key":"g","meta":true},"then":{"key":"e"}}},"dock":{"collapsed":false,"items":[]}}"#.into(),
    );
    let mut world = World::start(daemon(state, vec![]));
    open_agents(&mut world);
    world.gesture(|app| app.type_text("older"));

    let footer = hints(&world);
    assert!(
        footer.contains(&"settle needs a single-key shortcut (chords don't work here)".to_owned()),
        "{footer:?}"
    );
    assert!(
        footer
            .iter()
            .any(|h| h.ends_with(" snooze") && !h.contains("single-key"))
    );
    world.gesture(|app| app.key(settle));
    assert!(world.daemon.sent_of("settle_turn").is_empty());
    rest_but_for_ages(&mut world);
}

#[test]
fn an_age_reads_true_when_the_palette_opens_and_moves_on_while_it_shows() {
    let mut state = crew_and_queue();
    state.settings = Some(BTreeMap::new());
    let mut world = World::start(daemon(state, vec![]));
    world.advance(Duration::from_secs(2 * 3600));

    open_agents(&mut world);
    assert_eq!(
        row_text(&world, "agent:newer-turn", "age").as_deref(),
        Some("2h")
    );

    world.advance(Duration::from_secs(3600));
    assert_eq!(
        row_text(&world, "agent:newer-turn", "age").as_deref(),
        Some("3h")
    );
    rest_but_for_ages(&mut world);
}

#[test]
fn the_queue_keys_do_nothing_on_the_commands_face_or_among_snooze_choices() {
    let mut world = World::start(daemon(crew_and_queue(), vec![]));
    press_action(&mut world, Action::SessionJumpToWaiting);
    press_action(&mut world, Action::UiCommandPalette);
    for action in [
        Action::SessionSettle,
        Action::SessionPriority,
        Action::SessionSnooze,
    ] {
        press_action(&mut world, action);
    }
    assert_eq!(palette_named(&world).as_deref(), Some("Commands"));

    press_action(&mut world, Action::UiActionMenu);
    world.gesture(|app| app.type_text("older"));
    press_action(&mut world, Action::SessionSnooze);
    press_action(&mut world, Action::SessionSettle);
    press_action(&mut world, Action::SessionSnooze);
    assert_eq!(palette_named(&world).as_deref(), Some("Snooze older-turn"));

    assert!(world.daemon.sent_of("settle_turn").is_empty());
    assert!(world.daemon.sent_of("set_session_priority").is_empty());
    assert!(world.screen().find(Target::role("menu")).is_none());
    rest_but_for_ages(&mut world);
}

fn one_agent(settings: &[(&str, &str)]) -> InitialStateMessage {
    let mut state = initial(
        vec![desktop("d1", "work", 1, vec![agent_pane("s1")])],
        vec![session("s1")],
    );
    state.settings = Some(
        settings
            .iter()
            .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
            .collect(),
    );
    state
}

fn run_command(world: &mut World, query: &str) {
    press_action(world, Action::UiCommandPalette);
    world.gesture(|app| app.type_text(query));
    press(world, Key::Enter);
}

fn settings_sent(world: &World) -> Vec<(String, String)> {
    world
        .daemon
        .sent_of("set_setting")
        .iter()
        .map(|r| {
            (
                r["key"].as_str().unwrap().to_owned(),
                r["value"].as_str().unwrap().to_owned(),
            )
        })
        .collect()
}

#[test]
fn rename_from_the_palette_prefills_the_name_and_shows_the_daemons_refusal_until_a_name_is_taken() {
    let world_daemon = daemon(one_agent(&[]), vec![]);
    let refusal = "name is 49 characters, over the 48-character limit";
    world_daemon.on("rename_session", move |r| {
        let label = r["label"].as_str().unwrap();
        message(attn_protocol::RenameResultMessage {
            cmd: "rename_session".into(),
            id: r["session_id"].as_str().unwrap().into(),
            success: label.chars().count() <= 48,
            error: (label.chars().count() > 48).then(|| refusal.to_owned()),
            ..attn_protocol::RenameResultMessage::new()
        })
    });
    let mut world = World::start(world_daemon);
    press_action(&mut world, Action::UiCommandPalette);
    world.gesture(|app| app.type_text("rename"));
    assert!(titles(&world).is_empty());
    press(&mut world, Key::Escape);

    world.open_desktop();
    run_command(&mut world, "rename");
    press(&mut world, Key::Escape);
    assert!(world.screen().find(Target::key("session.rename")).is_none());
    run_command(&mut world, "rename");
    let field = world.screen().get(Target::key("session.rename.field"));
    assert_eq!(field.value.as_deref(), Some("s1"));

    world.gesture(|app| app.type_text(&"x".repeat(49)));
    press(&mut world, Key::Enter);
    assert_eq!(
        text_of(&world, "session.rename.refused").as_deref(),
        Some(refusal)
    );
    assert!(world.screen().find(Target::key("session.rename")).is_some());

    retype_field(&mut world, "session.rename.field", &"🙂".repeat(48));
    press(&mut world, Key::Enter);
    let labels: Vec<_> = world
        .daemon
        .sent_of("rename_session")
        .iter()
        .map(|r| r["label"].as_str().unwrap().to_owned())
        .collect();
    assert_eq!(labels, ["x".repeat(49), "🙂".repeat(48)]);
    assert!(world.screen().find(Target::key("session.rename")).is_none());
    rest(&mut world);
}

fn retype_field(world: &mut World, key: &str, text: &str) {
    let field = world.screen().get(Target::key(key));
    for _ in 0..field.value.unwrap_or_default().chars().count() {
        press(world, Key::Backspace);
    }
    world.gesture(|app| app.type_text(text));
}

#[test]
fn a_feature_toggle_shows_only_for_a_search_and_flips_its_setting() {
    let mut world = World::start(daemon(
        one_agent(&[("new_session_launcher_enabled", "true")]),
        vec![],
    ));
    press_action(&mut world, Action::UiCommandPalette);
    assert!(!titles(&world).iter().any(|t| t.contains("Launcher 2.0")));
    let count = text_of(&world, "palette.count").unwrap();
    let (shown, all) = count.split_once(" of ").unwrap();
    assert!(
        shown.parse::<usize>().unwrap() < all.parse::<usize>().unwrap(),
        "{count}"
    );
    press(&mut world, Key::Escape);

    run_command(&mut world, "turn off launcher 2.0");
    run_command(&mut world, "turn on outposts");
    assert_eq!(
        settings_sent(&world),
        [
            (
                "new_session_launcher_enabled".to_owned(),
                "false".to_owned()
            ),
            ("outposts_enabled".to_owned(), "true".to_owned())
        ]
    );
    rest(&mut world);
}

#[test]
fn the_queue_and_sidebar_settings_flip_from_the_palette() {
    let mut world = World::start(daemon(
        one_agent(&[("queue_crew_enabled", "true"), ("sidebar_width", "320")]),
        vec![],
    ));
    run_command(&mut world, "turn on the agent queue");
    run_command(&mut world, "take the crew out");
    run_command(&mut world, "turn on auto-settle");
    run_command(&mut world, "reset sidebar width");
    assert_eq!(
        settings_sent(&world),
        [
            ("queue_mode_enabled".to_owned(), "true".to_owned()),
            ("queue_crew_enabled".to_owned(), "false".to_owned()),
            ("auto_settle_enabled".to_owned(), "true".to_owned()),
            ("sidebar_width".to_owned(), String::new()),
        ]
    );
    rest(&mut world);
}

#[test]
fn the_agent_on_screen_can_be_reloaded_or_unsnoozed_from_the_palette() {
    let mut state = one_agent(&[("queue_mode_enabled", "true")]);
    state.sessions = Some(vec![Session {
        turn_owed: Some(true),
        turn_snoozed_until: Some("2099-01-01T00:00:00Z".into()),
        ..session("s1")
    }]);
    let mut world = World::start(daemon(state, vec![]));
    world.gesture(|app| {
        app.update(|state, tx| {
            attn_app::dispatch(
                state,
                Action::OpenAgent {
                    session: "s1".into(),
                },
                tx,
            );
        })
    });

    press_action(&mut world, Action::UiCommandPalette);
    world.gesture(|app| app.type_text("snooze"));
    assert_eq!(titles(&world), ["Unsnooze this agent"]);
    press(&mut world, Key::Enter);
    run_command(&mut world, "reload this");

    let woken = world.daemon.sent_of("wake_turn");
    assert_eq!(woken.len(), 1);
    assert_eq!(woken[0]["session_id"], "s1");
    let reloaded = world.daemon.sent_of("reload_session");
    assert_eq!(reloaded.len(), 1);
    assert_eq!(reloaded[0]["id"], "s1");
    rest_but_for_ages(&mut world);
}
