use std::collections::BTreeMap;
use std::time::Duration;

use attn_app::{Action, Binding, Category, Keymap};
use attn_protocol::{
    Desktop, DesktopPane, InitialStateMessage, Profile, ProfileArrangementChangedMessage,
};
use attn_testkit::{Daemon, Target, World, key_event, tree};
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
        tree_json: tree::panes_in_a_row(
            &panes.iter().map(|p| p.pane_id.as_str()).collect::<Vec<_>>(),
        ),
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
pub(super) fn state(desktops: Vec<Desktop>, settings: &[(&str, &str)]) -> InitialStateMessage {
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

pub(super) fn two_terminals() -> Vec<Desktop> {
    vec![desktop(
        "d1",
        "t1",
        vec![pane("t1", "r1"), pane("t2", "r2")],
    )]
}

/// The keys `action` has by default on this platform.
pub(super) fn chord(action: &Action) -> KeyEvent {
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

pub(super) fn shortcut_records(world: &World) -> Vec<String> {
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
    with_terminal.open_desktop();
    let mut without = World::start(Daemon::scripted().state(state(vec![], &settings)));
    for action in Action::ALL.iter().filter(|a| a.category() == category) {
        let world = if needs_no_terminal(action) {
            &mut without
        } else {
            &mut with_terminal
        };
        let before = shortcut_records(world).len();
        let typed_before = typed(world).len();

        let left = world.press_menu_key(chord(action));

        let recorded = shortcut_records(world)[before..].to_vec();
        let reached_terminal = typed(world).len() > typed_before;
        let pending = format!("Not in the native app yet: {}", action.label());
        match (recorded.as_slice(), notice(world)) {
            ([id], said) => {
                assert_eq!(id, action.id());
                assert_ne!(said.as_ref(), Some(&pending), "{}", action.id());
                assert!(!reached_terminal, "{} reached a terminal", action.id());
                if *action != Action::AppQuit {
                    press(world, action);
                }
                let screen = world.screen();
                if screen.find(Target::key("new-session")).is_some()
                    || screen.find(Target::key("garden.full")).is_some()
                    || screen.find(Target::key("notebook")).is_some()
                {
                    world.gesture(|app| {
                        app.key(KeyEvent {
                            key: Key::Escape,
                            mods: Mods::default(),
                        })
                    });
                }
                world.advance(Duration::from_secs(6));
            }
            ([], None) => assert!(
                left || !needs_no_terminal(action),
                "{} ran nothing yet kept its keys from the menu bar",
                action.id()
            ),
            other => panic!("{} ran {other:?}", action.id()),
        }
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
        Some(">ab")
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
    world.open_desktop();

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

#[test]
fn keys_the_daemon_refuses_are_counted_under_their_tile_until_a_typed_key_goes_through() {
    let world = World::start(Daemon::scripted().state(state(two_terminals(), &[])));
    world.daemon.on("pty_input", |r| {
        (r["data"] == "x").then(|| {
            serde_json::json!({
                "event": "command_error",
                "cmd": "pty_input",
                "terminal_id": r["id"],
                "success": false,
                "error": "the outpost is unreachable",
            })
        })
    });
    let mut world = world;
    world.open_desktop();
    let strip = |world: &World| {
        world
            .screen()
            .find(Target::key("tile.refused.r1"))
            .and_then(|n| n.name)
    };

    world.gesture(|app| app.type_text("x"));
    assert_eq!(
        strip(&world).as_deref(),
        Some("1 key may not have been sent")
    );
    world.gesture(|app| app.type_text("xx"));
    assert_eq!(
        strip(&world).as_deref(),
        Some("2 keys may not have been sent"),
        "the count starts again with each key typed, as the refusals come back after it"
    );
    assert_eq!(notice(&world), None, "a refused key is the tile's to show");
    assert_eq!(world.screen().find(Target::key("tile.refused.r2")), None);

    world.gesture(|app| app.type_text("a"));
    assert_eq!(
        strip(&world),
        None,
        "a key that went through clears the count"
    );
}

#[cfg(not(target_os = "macos"))]
#[test]
fn plain_ctrl_and_a_letter_belong_to_the_shell_on_linux() {
    let rebound = r#"{"version":1,"overrides":{"ui.showShortcuts":{"key":"c","meta":true}},"dock":{"collapsed":false,"items":[]}}"#;
    let mut world = World::start(
        Daemon::scripted().state(state(two_terminals(), &[("keybindings_config", rebound)])),
    );
    world.open_desktop();

    world.gesture(|app| app.press("ctrl+c"));

    assert_eq!(typed(&world), [typed_into("r1", "\u{3}")]);
    assert!(!shows(&world, "dialog", "Keyboard Shortcuts"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn close_ends_the_active_terminal_and_on_an_empty_desktop_closes_the_desktop() {
    let mut world = World::start(Daemon::scripted().state(state(two_terminals(), &[])));
    world.open_desktop();
    press(&mut world, &Action::TerminalClose);
    let closed = world.daemon.sent_of("desktop_close_tile");
    assert_eq!(closed.len(), 1);
    assert_eq!(
        (&closed[0]["desktop_id"], &closed[0]["tile_id"]),
        (&"d1".into(), &"t1".into())
    );
    assert_eq!(notice(&world), None);
    rest(&mut world);

    let mut empty = desktop("d1", "", vec![]);
    empty.revision = 7;
    let mut world = World::start(Daemon::scripted().state(state(vec![empty], &[])));
    world.open_desktop();
    press(&mut world, &Action::SessionClose);
    let closed = world.daemon.sent_of("desktop_close");
    assert_eq!(closed.len(), 1);
    assert_eq!(closed[0]["desktop_id"], "d1");
    assert_eq!(closed[0]["expected_revision"], 7);
    rest(&mut world);
}

#[test]
fn a_close_the_daemon_refuses_or_that_cannot_be_sent_shows_why_in_the_notice() {
    let mut world = World::start(Daemon::scripted().state(state(two_terminals(), &[])));
    world.open_desktop();
    world.daemon.on("desktop_close_tile", |r| {
        serde_json::json!({
            "event": "profile_action_result",
            "request_id": r["request_id"],
            "action": "desktop_close_tile",
            "success": false,
            "error": "the terminal is still starting",
        })
    });

    press(&mut world, &Action::TerminalClose);
    world.settle();
    assert_eq!(
        notice(&world).as_deref(),
        Some("Could not close that tile: the terminal is still starting")
    );

    world.daemon.refuse_connections(true);
    world.daemon.disconnect();
    world.settle();
    press(&mut world, &Action::TerminalClose);
    assert_eq!(
        notice(&world).as_deref(),
        Some("Could not close that tile: not connected to the daemon")
    );
}

#[test]
fn when_the_keyboards_tile_closes_the_keyboard_goes_to_the_tile_the_daemon_shows_next() {
    let three = vec![pane("t1", "r1"), pane("t2", "r2"), pane("t3", "r3")];
    let mut world =
        World::start(Daemon::scripted().state(state(vec![desktop("d1", "t3", three)], &[])));
    world.open_desktop();
    world.gesture(|app| app.click(Target::role("group").name("Agent t3")));

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
    world.gesture(|app| app.type_text("a"));

    assert_eq!(typed(&world), [typed_into("r2", "a")]);
    assert!(world.daemon.sent_of("desktop_show_leaf").is_empty());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_keyboard_stays_on_its_tile_when_the_active_tile_moves_elsewhere() {
    let mut world = World::start(Daemon::scripted().state(state(two_terminals(), &[])));
    world.open_desktop();
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
    world.open_desktop();
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
        world.open_desktop();
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
    assert!(!shows(&world, "dialog", "Keyboard Shortcuts"));
    world.advance(Duration::from_millis(599));
    world.gesture(|app| app.type_text("j"));

    assert!(shows(&world, "dialog", "Keyboard Shortcuts"));
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
    world.open_desktop();

    world.gesture(|app| app.press("mod+shift+y"));
    world.advance(Duration::from_millis(600));
    assert!(world.screen().find(Target::key("leader.text")).is_none());
    world.gesture(|app| app.type_text("j"));

    assert!(!shows(&world, "dialog", "Keyboard Shortcuts"));
    assert_eq!(typed(&world), [typed_into("r1", "j")]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn escape_cancels_a_leader_and_the_leader_again_keeps_it_armed() {
    let mut world = World::start(
        Daemon::scripted().state(state(two_terminals(), &[("keybindings_config", SEQUENCE)])),
    );
    world.open_desktop();

    world.gesture(|app| app.press("mod+shift+y"));
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Escape,
            mods: Mods::default(),
        })
    });
    world.gesture(|app| app.type_text("j"));
    assert!(!shows(&world, "dialog", "Keyboard Shortcuts"));
    assert_eq!(typed(&world), [typed_into("r1", "j")]);

    world.gesture(|app| app.press("mod+shift+y"));
    world.advance(Duration::from_millis(400));
    world.gesture(|app| app.press("mod+shift+y"));
    world.advance(Duration::from_millis(400));
    world.gesture(|app| app.type_text("j"));
    assert!(shows(&world, "dialog", "Keyboard Shortcuts"));
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

    assert!(shows(&world, "dialog", "Keyboard Shortcuts"));
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

    assert!(shows(&world, "dialog", "Keyboard Shortcuts"));
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
    assert!(!shows(&world, "dialog", "Keyboard Shortcuts"));
    rest(&mut world);
}

#[cfg(not(target_os = "macos"))]
#[test]
fn a_rebind_saved_with_the_shifted_character_takes_the_keys_of_its_physical_key() {
    let config = r#"{"version":1,"overrides":{"ui.showShortcuts":{"key":"!","code":"Digit1","meta":true,"shift":true}},"dock":{"collapsed":false,"items":[]}}"#;
    let mut world =
        World::start(Daemon::scripted().state(state(vec![], &[("keybindings_config", config)])));

    world.gesture(|app| app.press("mod+shift+1"));

    assert!(shows(&world, "dialog", "Keyboard Shortcuts"));
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

    assert!(!shows(&world, "dialog", "Keyboard Shortcuts"));
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

#[test]
fn a_key_that_types_text_goes_to_the_terminal_holding_the_keyboard_even_when_bound() {
    let tilde = KeyEvent {
        key: Key::Text("~".into()),
        mods: Mods {
            shift: true,
            ..Mods::default()
        },
    };
    let bound = r#"{"version":1,"overrides":{"ui.showShortcuts":{"key":"~","shift":true}},"dock":{"collapsed":false,"items":[]}}"#;
    for settings in [vec![], vec![("keybindings_config", bound)]] {
        let mut world = World::start(Daemon::scripted().state(state(two_terminals(), &settings)));
        world.open_desktop();

        world.gesture(|app| app.key(tilde.clone()));

        assert_eq!(typed(&world), [typed_into("r1", "~")], "{settings:?}");
        assert_eq!(notice(&world), None, "{settings:?}");
        assert!(
            !shows(&world, "dialog", "Keyboard Shortcuts"),
            "{settings:?}"
        );
        rest(&mut world);
    }

    let mut world = World::start(
        Daemon::scripted().state(state(two_terminals(), &[("keybindings_config", bound)])),
    );
    world.open_desktop();
    world.gesture(|app| app.click(Target::key("sidebar.home")));
    world.gesture(|app| app.key(tilde.clone()));
    world.gesture(|app| app.type_text("x"));
    assert!(
        shows(&world, "dialog", "Keyboard Shortcuts"),
        "on home no terminal takes the key, so it runs its action"
    );
    assert!(typed(&world).is_empty(), "home types into no terminal");
}

#[test]
fn the_close_keys_close_nothing_behind_home_an_overlay_or_a_dialog_and_never_reach_the_menu_bar() {
    let fronts = [
        ("home", None),
        ("command palette", Some(Action::UiCommandPalette)),
        ("action menu", Some(Action::UiActionMenu)),
        ("overview", Some(Action::DesktopOverview)),
        ("file opener", Some(Action::FileOpen)),
        ("new session", Some(Action::SessionNew)),
    ];
    for (front, open) in fronts {
        let mut world = World::start(Daemon::scripted().state(state(two_terminals(), &[])));
        world.open_desktop();
        match &open {
            None => world.gesture(|app| app.click(Target::key("sidebar.home"))),
            Some(action) => press(&mut world, action),
        }

        for close in [Action::SessionClose, Action::TerminalClose] {
            assert!(
                !world.press_menu_key(chord(&close)),
                "{front}: {} reached the menu bar, whose Close closes the window",
                close.id()
            );
        }

        for cmd in [
            "desktop_close_tile",
            "desktop_remove_leaf",
            "desktop_close",
            "unregister",
        ] {
            assert!(world.daemon.sent_of(cmd).is_empty(), "{front}: sent {cmd}");
        }
    }
}

#[test]
fn a_bound_chord_whose_action_does_not_run_goes_on_as_a_key_nothing_binds() {
    let mut world =
        World::start(Daemon::scripted().state(state(vec![], &[("asks_enabled", "false")])));

    assert!(
        world.press_menu_key(chord(&Action::DockAsks)),
        "the asks key with no asks kept its keys"
    );
    assert_eq!(notice(&world), None);
    assert!(world.screen().find(Target::key("drawer.asks")).is_none());
    rest(&mut world);
}

#[test]
fn a_click_on_a_title_bar_button_leaves_the_keyboard_with_the_terminal() {
    let mut world = World::start(Daemon::scripted().state(state(two_terminals(), &[])));
    world.open_desktop();

    world.gesture(|app| app.click(Target::role("button").name("Collapse sidebar")));
    world.gesture(|app| app.type_text("x"));

    assert_eq!(typed(&world), [typed_into("r1", "x")]);
}

#[test]
fn the_last_shortcut_scrolls_into_the_cheat_sheet_by_wheel_and_by_end() {
    let in_view = |world: &World, target: Target| {
        let screen = world.screen();
        let list = screen.bounds(Target::key("shortcuts.list")).unwrap();
        let row = screen.bounds(target).unwrap();
        row[1] >= list[1] && row[1] + row[3] <= list[1] + list[3]
    };
    let mut world = World::start(Daemon::scripted().state(state(vec![], &[])));
    world.gesture(|app| app.key(chord(&Action::UiShowShortcuts)));
    let last = world
        .screen()
        .children(Target::key("shortcuts.App"))
        .pop()
        .and_then(|n| n.key)
        .map(Target::key)
        .unwrap();
    assert!(!in_view(&world, last.clone()));

    let [x, y, w, h] = world
        .screen()
        .bounds(Target::key("shortcuts.list"))
        .unwrap();
    world.gesture(|app| {
        app.runtime
            .wheel_event(rune::WheelEvent::new(0.0, -10_000.0).at(x + w / 2.0, y + h / 2.0))
    });
    assert!(in_view(&world, last.clone()));

    world.gesture(|app| app.press("home"));
    assert!(!in_view(&world, last.clone()));
    world.gesture(|app| app.press("end"));
    assert!(in_view(&world, last));
    rest(&mut world);
}

#[test]
fn the_cheat_sheet_lists_the_tauri_rows_with_paired_keys_and_notes() {
    let mut world = World::start(Daemon::scripted().state(state(vec![], &[])));
    world.gesture(|app| app.key(chord(&Action::UiShowShortcuts)));
    let keymap = Keymap::from_setting(None, KeyPlatform::current());
    let keys = |action: Action| keymap.label(keymap.binding(&action).expect("bound"));
    let item = |world: &World, key: &str| world.screen().get(Target::key(key));

    let step = item(&world, "shortcut.session.prev");
    assert_eq!(
        step.name,
        Some(format!(
            "Previous / next desktop (queue mode: agent in the queue), {} / {}",
            keys(Action::SessionPrev),
            keys(Action::SessionNext)
        ))
    );

    let digits = item(&world, "shortcut.desktop.select1");
    assert!(
        digits
            .name
            .as_deref()
            .is_some_and(|n| n.starts_with("Switch to desktop 1–9, ") && n.ends_with("1–9")),
        "{:?}",
        digits.name
    );
    assert_eq!(
        digits.description.as_deref(),
        Some("The digit of the current desktop goes back to the previous one.")
    );

    let shelf = item(&world, "shortcut.shelf.sendToShelf");
    assert_eq!(
        shelf.description,
        Some(format!(
            "A held tile returns with a chip, {} or a restart.",
            keys(Action::SessionJumpToWaiting)
        ))
    );
    assert!(
        world
            .screen()
            .find(Target::key("shortcut.terminal.focusRight"))
            .is_none(),
        "the four focus keys share one row"
    );
}

/// The dialogs and menus in front, by name.
fn fronts(world: &World) -> Vec<String> {
    let screen = world.screen();
    ["dialog", "menu"]
        .into_iter()
        .flat_map(|role| screen.all(Target::role(role)))
        .map(|n| n.name.unwrap_or_default())
        .collect()
}

/// A desktop with two terminals, analytics counting shortcuts.
fn counting() -> World {
    let settings = [("analytics.enabled", "true")];
    let mut world = World::start(Daemon::scripted().state(state(two_terminals(), &settings)));
    world.open_desktop();
    world
}

/// Opens `open`'s layer in front.
fn open_layer(world: &mut World, open: &Action) {
    world.gesture(|app| {
        app.update(|state, tx| {
            attn_app::dispatch(state, open.clone(), tx);
        })
    });
    assert!(!fronts(world).is_empty(), "{} opened nothing", open.id());
}

/// Presses `action`'s keys and checks they did nothing: no action ran, nothing opened or
/// closed, nothing reached the daemon or a terminal, and the menu bar never heard them unless
/// a focused field keeps them.
fn held(world: &mut World, layer: &Action, action: &Action) {
    let keymap = Keymap::from_setting(None, KeyPlatform::current());
    let front = fronts(world);
    let records = shortcut_records(world).len();
    let sent = world.daemon.sent().len();
    let typed_before = typed(world).len();
    let left = world.press_menu_key(chord(action));
    let what = format!("{} under {}", action.id(), layer.id());
    assert!(
        !left || keymap.kept_by_fields(action),
        "{what} reached the menu bar"
    );
    assert_eq!(shortcut_records(world).len(), records, "{what} ran");
    assert_eq!(fronts(world), front, "{what}");
    assert_eq!(world.daemon.sent().len(), sent, "{what} reached the daemon");
    assert_eq!(
        typed(world).len(),
        typed_before,
        "{what} reached a terminal"
    );
}

#[test]
fn every_overlay_that_captures_navigation_holds_the_app_shortcuts_but_not_quit() {
    let layers = [
        Action::UiCommandPalette,
        Action::UiActionMenu,
        Action::FileOpen,
        Action::OpenShortcutEditor,
        Action::DesktopOverview,
        Action::SessionsOpen,
        Action::ProfileSwitch,
        Action::OpenWhatsNew,
        Action::NotebookOpenFullscreen,
        Action::OpenCrew,
    ];
    let mut world = counting();
    for layer in layers {
        open_layer(&mut world, &layer);
        for action in [
            Action::SessionNew,
            Action::DesktopSelect1,
            Action::BoardOpen,
        ] {
            held(&mut world, &layer, &action);
        }
        world.press_menu_key(chord(&Action::AppQuit));
        assert_eq!(
            shortcut_records(&world).last().map(String::as_str),
            Some("app.quit"),
            "Quit was held under {}",
            layer.id()
        );
        world.gesture(|app| {
            app.key(KeyEvent {
                key: Key::Escape,
                mods: Mods::default(),
            })
        });
        assert!(fronts(&world).is_empty(), "Escape left {} open", layer.id());
    }
}

#[test]
fn the_palette_holds_every_app_shortcut_tauri_turns_off_under_it() {
    let kept = [
        Action::AppQuit,
        Action::UiActionMenu,
        Action::UiCommandPalette,
        Action::UiOpenSettings,
        Action::UiShowShortcuts,
        Action::UiIncreaseFontSize,
        Action::UiDecreaseFontSize,
        Action::UiResetFontSize,
        Action::SessionsOpen,
        Action::SidebarWider,
        Action::SidebarNarrower,
        Action::SidebarAgentList,
        Action::SessionSettle,
        Action::SessionPriority,
        Action::SessionSnooze,
    ];
    let keymap = Keymap::from_setting(None, KeyPlatform::current());
    let layer = Action::UiCommandPalette;
    let mut world = counting();
    open_layer(&mut world, &layer);
    let mut pressed = 0;
    for action in Action::ALL.iter().filter(|a| {
        !kept.contains(a)
            && !matches!(a.category(), Category::Panes | Category::Markdown)
            && matches!(keymap.binding(a), Some(Binding::Chord(_)))
    }) {
        held(&mut world, &layer, action);
        pressed += 1;
    }
    assert!(pressed > 40, "only {pressed} held keys pressed");

    world.press_menu_key(chord(&Action::UiOpenSettings));
    assert_eq!(
        shortcut_records(&world).last().map(String::as_str),
        Some("ui.openSettings"),
        "Settings was held under the palette"
    );
}
