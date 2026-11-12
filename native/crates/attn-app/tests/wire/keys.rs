use std::collections::BTreeMap;
use std::time::Duration;

use attn_app::{Action, Binding, Category, Keymap};
use attn_protocol::{
    Desktop, DesktopPane, InitialStateMessage, Profile, ProfileArrangementChangedMessage,
};
use attn_testkit::{Daemon, Target, World, key_event};
use rune::{Key, KeyEvent, KeyPlatform, Mods};

fn pane(id: &str, runtime: &str) -> DesktopPane {
    DesktopPane {
        desktop_id: "d1".into(),
        pane_id: id.into(),
        runtime_id: runtime.into(),
        title: format!("Agent {id}"),
        kind: "agent".into(),
        ..DesktopPane::default()
    }
}

fn desktop(id: &str, active: &str, panes: Vec<DesktopPane>) -> Desktop {
    Desktop {
        id: id.into(),
        name: id.into(),
        active_pane_id: active.into(),
        panes,
        profile_id: "work".into(),
        ..Desktop::default()
    }
}

fn profile(current: &str) -> Profile {
    Profile {
        id: "work".into(),
        name: "Work".into(),
        current_desktop_id: current.into(),
        ..Profile::default()
    }
}

/// One profile whose current desktop shows `desktops[0]`, with these settings.
fn state(desktops: Vec<Desktop>, settings: &[(&str, &str)]) -> InitialStateMessage {
    InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![profile(
            &desktops.first().map(|d| d.id.clone()).unwrap_or_default(),
        )]),
        desktops: Some(desktops),
        settings: Some(
            settings
                .iter()
                .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
                .collect::<BTreeMap<_, _>>(),
        ),
        ..InitialStateMessage::new()
    }
}

fn two_terminals() -> Vec<Desktop> {
    vec![desktop(
        "d1",
        "t1",
        vec![pane("t1", "r1"), pane("t2", "r2")],
    )]
}

/// The keys `action` has by default on this platform.
fn chord(action: &Action) -> KeyEvent {
    match Keymap::from_setting(None, KeyPlatform::current()).binding(action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    }
}

fn press(world: &mut World, action: &Action) {
    let ev = chord(action);
    world.gesture(|app| app.key(ev));
}

/// What each terminal was sent, by runtime id, in order.
fn typed(world: &World) -> Vec<(String, String)> {
    world
        .daemon
        .sent_of("pty_input")
        .iter()
        .map(|r| {
            (
                r["id"].as_str().unwrap().to_owned(),
                r["data"].as_str().unwrap().to_owned(),
            )
        })
        .collect()
}

fn typed_into(runtime: &str, data: &str) -> (String, String) {
    (runtime.to_owned(), data.to_owned())
}

fn shortcut_records(world: &World) -> Vec<String> {
    world
        .daemon
        .sent_of("record_analytics")
        .iter()
        .filter(|r| r["name"] == "shortcut")
        .map(|r| r["props"]["id"].as_str().unwrap().to_owned())
        .collect()
}

fn notice(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("notice.text"))
        .and_then(|n| n.name)
}

fn shows(world: &World, role: &str, name: &str) -> bool {
    world.screen().find(Target::role(role).name(name)).is_some()
}

fn rest(world: &mut World) {
    world.advance(Duration::from_secs(6));
    assert!(world.runtime_is_at_rest());
}

/// Whether `action` runs from its keys only while no terminal holds the keyboard. The
/// others run with a terminal holding it, which the terminal-only actions need.
fn needs_no_terminal(action: &Action) -> bool {
    matches!(
        action,
        Action::SessionClose | Action::MarkdownSendAnnotations | Action::SessionRefreshPrs
    )
}

#[test]
fn every_sessions_shortcut_runs_its_own_action() {
    every_shortcut_runs_its_own_action(Category::Sessions);
}

#[test]
fn every_panes_shortcut_runs_its_own_action() {
    every_shortcut_runs_its_own_action(Category::Panes);
}

#[test]
fn every_markdown_shortcut_runs_its_own_action() {
    every_shortcut_runs_its_own_action(Category::Markdown);
}

#[test]
fn every_review_shortcut_runs_its_own_action() {
    every_shortcut_runs_its_own_action(Category::Review);
}

#[test]
fn every_app_shortcut_runs_its_own_action() {
    every_shortcut_runs_its_own_action(Category::App);
}

fn every_shortcut_runs_its_own_action(category: Category) {
    let settings = [("analytics.enabled", "true")];
    let mut with_terminal =
        World::start(Daemon::scripted().state(state(two_terminals(), &settings)));
    let mut without = World::start(Daemon::scripted().state(state(vec![], &settings)));
    for action in Action::ALL.iter().filter(|a| a.category() == category) {
        let world = if needs_no_terminal(action) {
            &mut without
        } else {
            &mut with_terminal
        };
        let before = shortcut_records(world).len();

        press(world, action);

        let recorded = shortcut_records(world)[before..].to_vec();
        let pending = format!("Not in the native app yet: {}", action.label());
        match (recorded.as_slice(), notice(world)) {
            ([id], None) => {
                assert_eq!(id, action.id());
                if *action != Action::AppQuit {
                    press(world, action);
                }
            }
            ([], Some(text)) => {
                assert_eq!(text, pending, "{}", action.id());
                world.advance(Duration::from_secs(6));
            }
            other => panic!("{} ran {other:?}", action.id()),
        }
        assert!(
            typed(world).is_empty(),
            "{} reached a terminal",
            action.id()
        );
        assert_eq!(
            world.screen().find(Target::role("dialog")),
            None,
            "{} left a layer open",
            action.id()
        );
    }
    rest(&mut with_terminal);
    rest(&mut without);
}

#[test]
fn a_focused_field_takes_typing_and_the_keys_it_keeps() {
    let mut world = World::start(Daemon::scripted().state(state(two_terminals(), &[])));
    press(&mut world, &Action::UiCommandPalette);
    assert!(shows(&world, "dialog", "Commands"));

    world.gesture(|app| app.type_text("ab"));
    press(&mut world, &Action::TerminalFind);

    assert_eq!(
        world
            .screen()
            .get(Target::key("palette.field"))
            .value
            .as_deref(),
        Some("ab")
    );
    assert!(typed(&world).is_empty(), "typing stays in the field");
    assert_eq!(notice(&world), None, "the field keeps Find's keys");

    press(&mut world, &Action::UiCommandPalette);
    press(&mut world, &Action::UiOpenSettings);
    assert!(
        shows(&world, "dialog", "Settings"),
        "the field passes on keys it does not keep"
    );
    assert!(!shows(&world, "dialog", "Commands"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_terminal_holding_the_keyboard_gets_what_the_keymap_does_not_take() {
    let mut world = World::start(Daemon::scripted().state(state(two_terminals(), &[])));

    world.gesture(|app| {
        app.type_text("ls");
        app.key(KeyEvent {
            key: Key::Enter,
            mods: Mods::default(),
        });
    });
    press(&mut world, &Action::SessionToggleSidebar);
    press(&mut world, &Action::SessionRefreshPrs);

    assert!(
        !shows(&world, "navigation", "Sidebar"),
        "the keymap runs before the terminal"
    );
    assert_eq!(
        typed(&world)[..3],
        [
            typed_into("r1", "l"),
            typed_into("r1", "s"),
            typed_into("r1", "\r")
        ],
    );
    assert_eq!(
        notice(&world),
        None,
        "Refresh PRs leaves its keys to the terminal"
    );
    assert!(world.runtime_is_at_rest());
}

#[cfg(not(target_os = "macos"))]
#[test]
fn plain_ctrl_and_a_letter_belong_to_the_shell_on_linux() {
    let rebound = r#"{"version":1,"overrides":{"ui.showShortcuts":{"key":"c","meta":true}},"dock":{"collapsed":false,"items":[]}}"#;
    let mut world = World::start(
        Daemon::scripted().state(state(two_terminals(), &[("keybindings_config", rebound)])),
    );

    world.gesture(|app| app.press("ctrl+c"));

    assert_eq!(typed(&world), [typed_into("r1", "\u{3}")]);
    assert!(!shows(&world, "dialog", "Keyboard shortcuts"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn close_closes_the_pane_while_a_terminal_holds_the_keyboard_and_the_session_otherwise() {
    let mut world = World::start(Daemon::scripted().state(state(two_terminals(), &[])));
    press(&mut world, &Action::SessionClose);
    assert_eq!(
        notice(&world).as_deref(),
        Some("Not in the native app yet: Close focused pane")
    );
    rest(&mut world);

    let mut world = World::start(Daemon::scripted().state(state(vec![], &[])));
    press(&mut world, &Action::SessionClose);
    assert_eq!(
        notice(&world).as_deref(),
        Some("Not in the native app yet: Close session, focused pane or empty desktop")
    );
    rest(&mut world);
}

#[test]
fn the_keyboard_stays_on_its_tile_when_the_active_tile_moves_elsewhere() {
    let mut world = World::start(Daemon::scripted().state(state(two_terminals(), &[])));
    world.gesture(|app| app.type_text("a"));

    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile("d1"),
        desktops: vec![desktop(
            "d1",
            "t2",
            vec![pane("t1", "r1"), pane("t2", "r2")],
        )],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
    world.gesture(|app| app.type_text("b"));

    world.gesture(|app| app.click(Target::role("group").name("Agent t2")));
    world.gesture(|app| app.type_text("c"));

    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile("d2"),
        desktops: vec![
            desktop("d1", "t2", vec![pane("t1", "r1"), pane("t2", "r2")]),
            desktop("d2", "t4", vec![pane("t3", "r3"), pane("t4", "r4")]),
        ],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
    world.gesture(|app| app.type_text("d"));

    assert_eq!(
        typed(&world),
        [
            typed_into("r1", "a"),
            typed_into("r1", "b"),
            typed_into("r2", "c"),
            typed_into("r4", "d"),
        ],
        "the keyboard moves with a click and a desktop switch, not with the active tile"
    );
    assert!(world.daemon.sent_of("desktop_set_active_pane").is_empty());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn closing_the_palette_gives_the_keyboard_back_to_its_tile() {
    let mut world = World::start(Daemon::scripted().state(state(two_terminals(), &[])));
    press(&mut world, &Action::UiCommandPalette);
    world.gesture(|app| app.type_text("x"));

    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Escape,
            mods: Mods::default(),
        })
    });
    world.gesture(|app| app.type_text("y"));

    assert!(!shows(&world, "dialog", "Commands"));
    assert_eq!(typed(&world), [typed_into("r1", "y")]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_open_dialog_keeps_typing_from_the_terminal_behind_it() {
    for action in [Action::UiShowShortcuts, Action::UiOpenSettings] {
        let mut world = World::start(Daemon::scripted().state(state(two_terminals(), &[])));
        press(&mut world, &action);
        world.gesture(|app| app.type_text("ls"));
        world.gesture(|app| {
            app.key(KeyEvent {
                key: Key::Enter,
                mods: Mods::default(),
            })
        });
        world.gesture(|app| app.press("ctrl+c"));

        assert!(
            typed(&world).is_empty(),
            "{} let typing through",
            action.id()
        );
        world.gesture(|app| {
            app.key(KeyEvent {
                key: Key::Escape,
                mods: Mods::default(),
            })
        });
        world.gesture(|app| app.type_text("y"));
        assert_eq!(typed(&world), [typed_into("r1", "y")]);
        assert!(world.runtime_is_at_rest());
    }
}

const SEQUENCE: &str = r#"{"version":1,"overrides":{"ui.showShortcuts":{"leader":{"key":"y","meta":true,"shift":true},"then":{"key":"j"}}},"dock":{"collapsed":false,"items":[]}}"#;

#[test]
fn a_sequence_runs_when_its_second_key_follows_the_leader_in_time() {
    let mut world = World::start(
        Daemon::scripted().state(state(two_terminals(), &[("keybindings_config", SEQUENCE)])),
    );

    world.gesture(|app| app.press("mod+shift+y"));
    let hint = world
        .screen()
        .get(Target::key("leader.text"))
        .name
        .unwrap_or_default();
    assert!(hint.ends_with("then…"), "{hint}");
    assert!(!shows(&world, "dialog", "Keyboard shortcuts"));
    world.advance(Duration::from_millis(599));
    world.gesture(|app| app.type_text("j"));

    assert!(shows(&world, "dialog", "Keyboard shortcuts"));
    assert!(world.screen().find(Target::key("leader.text")).is_none());
    assert!(
        typed(&world).is_empty(),
        "the leader and its key never reach the terminal"
    );
    world.advance(Duration::from_millis(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_sequence_lapses_after_its_timeout_and_the_next_key_types() {
    let mut world = World::start(
        Daemon::scripted().state(state(two_terminals(), &[("keybindings_config", SEQUENCE)])),
    );

    world.gesture(|app| app.press("mod+shift+y"));
    world.advance(Duration::from_millis(600));
    assert!(world.screen().find(Target::key("leader.text")).is_none());
    world.gesture(|app| app.type_text("j"));

    assert!(!shows(&world, "dialog", "Keyboard shortcuts"));
    assert_eq!(typed(&world), [typed_into("r1", "j")]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn escape_cancels_a_leader_and_the_leader_again_keeps_it_armed() {
    let mut world = World::start(
        Daemon::scripted().state(state(two_terminals(), &[("keybindings_config", SEQUENCE)])),
    );

    world.gesture(|app| app.press("mod+shift+y"));
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Escape,
            mods: Mods::default(),
        })
    });
    world.gesture(|app| app.type_text("j"));
    assert!(!shows(&world, "dialog", "Keyboard shortcuts"));
    assert_eq!(typed(&world), [typed_into("r1", "j")]);

    world.gesture(|app| app.press("mod+shift+y"));
    world.advance(Duration::from_millis(400));
    world.gesture(|app| app.press("mod+shift+y"));
    world.advance(Duration::from_millis(400));
    world.gesture(|app| app.type_text("j"));
    assert!(shows(&world, "dialog", "Keyboard shortcuts"));
    assert_eq!(typed(&world), [typed_into("r1", "j")]);
    rest(&mut world);
}

#[test]
fn a_rebind_moves_the_action_and_takes_its_keys_from_the_default_holding_them() {
    let toggle_sidebar = chord(&Action::SessionToggleSidebar);
    let config = format!(
        r#"{{"version":1,"overrides":{{"ui.showShortcuts":{}}},"dock":{{"collapsed":false,"items":[]}}}}"#,
        stored(&toggle_sidebar)
    );
    let mut world =
        World::start(Daemon::scripted().state(state(vec![], &[("keybindings_config", &config)])));

    world.gesture(|app| app.key(toggle_sidebar.clone()));

    assert!(shows(&world, "dialog", "Keyboard shortcuts"));
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Escape,
            mods: Mods::default(),
        })
    });
    assert!(shows(&world, "navigation", "Sidebar"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_sequence_that_leads_with_a_default_takes_its_keys() {
    let palette = chord(&Action::UiCommandPalette);
    let config = format!(
        r#"{{"version":1,"overrides":{{"ui.showShortcuts":{{"leader":{},"then":{{"key":"j"}}}}}},"dock":{{"collapsed":false,"items":[]}}}}"#,
        stored(&palette)
    );
    let mut world =
        World::start(Daemon::scripted().state(state(vec![], &[("keybindings_config", &config)])));

    world.gesture(|app| app.key(palette.clone()));
    assert!(world.screen().find(Target::key("leader.text")).is_some());
    world.gesture(|app| app.type_text("j"));

    assert!(shows(&world, "dialog", "Keyboard shortcuts"));
    assert!(!shows(&world, "dialog", "Commands"));
    rest(&mut world);
}

#[cfg(not(target_os = "macos"))]
#[test]
fn a_ctrl_letter_after_a_leader_ends_it_and_never_reaches_the_shell() {
    let mut world = World::start(
        Daemon::scripted().state(state(two_terminals(), &[("keybindings_config", SEQUENCE)])),
    );

    world.gesture(|app| app.press("mod+shift+y"));
    world.gesture(|app| app.press("ctrl+c"));

    assert!(world.screen().find(Target::key("leader.text")).is_none());
    assert!(typed(&world).is_empty());
    assert!(!shows(&world, "dialog", "Keyboard shortcuts"));
    rest(&mut world);
}

#[cfg(not(target_os = "macos"))]
#[test]
fn a_rebind_saved_with_the_shifted_character_takes_the_keys_of_its_physical_key() {
    let config = r#"{"version":1,"overrides":{"ui.showShortcuts":{"key":"!","code":"Digit1","meta":true,"shift":true}},"dock":{"collapsed":false,"items":[]}}"#;
    let mut world =
        World::start(Daemon::scripted().state(state(vec![], &[("keybindings_config", config)])));

    world.gesture(|app| app.press("mod+shift+1"));

    assert!(shows(&world, "dialog", "Keyboard shortcuts"));
    rest(&mut world);
}

#[test]
fn a_rebind_arriving_from_the_daemon_applies_at_once() {
    let mut world = World::start(Daemon::scripted().state(state(vec![], &[])));
    let show_shortcuts = chord(&Action::UiShowShortcuts);
    let config = r#"{"version":1,"overrides":{"ui.showShortcuts":null},"dock":{"collapsed":false,"items":[]}}"#;
    world.daemon.emit(attn_protocol::SettingsUpdatedMessage {
        settings: Some([("keybindings_config".to_owned(), config.to_owned())].into()),
        ..attn_protocol::SettingsUpdatedMessage::new()
    });
    world.settle();

    world.gesture(|app| app.key(show_shortcuts));

    assert!(!shows(&world, "dialog", "Keyboard shortcuts"));
    assert!(world.runtime_is_at_rest());
}

/// `ev` as the Tauri app stores a binding in `keybindings_config`.
fn stored(ev: &KeyEvent) -> String {
    let key = match &ev.key {
        Key::Command(c) => c.to_string(),
        Key::Text(t) => t.clone(),
        other => panic!("no stored key for {other:?}"),
    };
    serde_json::json!({
        "key": key,
        "meta": true,
        "alt": ev.mods.alt,
        "shift": ev.mods.shift,
    })
    .to_string()
}
