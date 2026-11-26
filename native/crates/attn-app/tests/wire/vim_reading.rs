use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    Desktop, DesktopPane, DesktopTileContentMessage, InitialStateMessage, Profile,
    ProfileArrangementChangedMessage, Session,
};
use attn_testkit::tree::{pane, row};
use attn_testkit::{Target, World, key_event};
use rune::{Key, KeyEvent, Mods};
use serde_json::json;

const DESKTOP: &str = "work/desktop_1";
const AWAY: &str = "work/desktop_2";
const PLAN: &str = "/work/a/plan.md";
const VIEW: &str = "tile.m.document";
const LINE: f32 = 24.0;

fn document_tile() -> serde_json::Value {
    json!({
        "type": "tile",
        "tile_id": "m",
        "tile_kind": "markdown",
        "tile_params": json!({ "path": PLAN }).to_string(),
    })
}

/// A desktop with a terminal `a` beside the document tile `m`, `active` holding the keyboard.
fn desktop(id: &str, slot: i64, active: &str) -> Desktop {
    Desktop {
        id: id.into(),
        profile_id: "work".into(),
        name: format!("Desktop {slot}"),
        shortcut_slot: Some(slot),
        order_key: slot.to_string(),
        tree_json: row(vec![pane("a"), document_tile()]).to_string(),
        active_pane_id: active.into(),
        panes: vec![DesktopPane {
            desktop_id: id.into(),
            pane_id: "a".into(),
            runtime_id: "r-a".into(),
            session_id: "s-a".into(),
            kind: "agent".into(),
            ..DesktopPane::default()
        }],
        revision: 1,
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

fn start(vim: bool, active: &str) -> World {
    let daemon = super::palette::daemon(
        InitialStateMessage {
            settings: Some([("vim_mode_enabled".to_owned(), vim.to_string())].into()),
            selected_profile_id: Some("work".into()),
            profiles: Some(vec![profile(DESKTOP)]),
            sessions: Some(vec![Session {
                id: "s-a".into(),
                label: "Agent a".into(),
                directory: "/work/a".into(),
                profile_id: "work".into(),
                ..Session::default()
            }]),
            desktops: Some(vec![desktop(DESKTOP, 1, active)]),
            ..InitialStateMessage::new()
        },
        vec![],
    );
    let mut world = World::start_sized(daemon, (1600.0, 900.0));
    world.open_desktop();
    world.daemon.emit(DesktopTileContentMessage {
        desktop_id: DESKTOP.into(),
        tile_id: "m".into(),
        tile_kind: "markdown".into(),
        path: PLAN.into(),
        content: document(),
        ..DesktopTileContentMessage::new()
    });
    world.settle();
    world
}

/// Three headings with room between them, and "needle" three times.
fn document() -> String {
    let filler = "A paragraph that takes room.\n\n".repeat(30);
    format!(
        "# Plan\n\nFind the needle here.\n\n{filler}## Setup\n\nAnother needle.\n\n{filler}## Ship\n\nThe last needle.\n\n{filler}"
    )
}

fn scrolled(world: &World) -> f32 {
    world.screen().scrolled(Target::key(VIEW)).unwrap()
}

fn typed(world: &mut World, text: &str) {
    world.gesture(|app| app.type_text(text));
    world.settle();
}

fn press(world: &mut World, key: Key, mods: Mods) {
    world.gesture(|app| app.key(KeyEvent { key, mods }));
    world.settle();
}

fn ctrl(world: &mut World, letter: char) {
    let mods = Mods {
        ctrl: true,
        ..Mods::default()
    };
    press(world, Key::Command(letter), mods);
}

fn press_action(world: &mut World, action: &Action) {
    let ev = match Keymap::from_setting(None, attn_testkit::platform()).binding(action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    };
    world.gesture(|app| app.key(ev));
    world.settle();
}

/// How far below the document view's top the element keyed `key` sits.
fn below_top(world: &World, key: &str) -> f32 {
    let screen = world.screen();
    let view = screen.bounds(Target::key(VIEW)).unwrap();
    let at = screen.bounds(Target::key(key)).unwrap();
    at[1] - view[1]
}

fn focused(world: &World) -> Option<String> {
    world.screen().focused().and_then(|n| n.key)
}

fn find_count(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key(format!("{VIEW}.find.count")))
        .and_then(|n| n.name)
}

fn rest(world: &mut World) {
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn with_vim_keys_off_a_document_ignores_them_and_its_find_bar_still_opens() {
    let mut world = start(false, "m");
    typed(&mut world, "jjG/");
    ctrl(&mut world, 'd');
    assert_eq!(scrolled(&world), 0.0);
    assert!(find_count(&world).is_none());

    press_action(&mut world, &Action::TerminalFind);
    assert_eq!(
        focused(&world).as_deref(),
        Some("tile.m.document.find.input")
    );
    typed(&mut world, "needle");
    assert_eq!(find_count(&world).as_deref(), Some("1/3"));
    press(&mut world, Key::Enter, Mods::default());
    assert_eq!(find_count(&world).as_deref(), Some("2/3"));
    press(&mut world, Key::Escape, Mods::default());
    assert!(find_count(&world).is_none());
    assert_eq!(focused(&world).as_deref(), Some(VIEW));
    rest(&mut world);
}

#[test]
fn vim_keys_scroll_a_document_by_lines_half_pages_ends_and_headings() {
    let mut world = start(true, "m");
    typed(&mut world, "jj");
    assert_eq!(scrolled(&world), 2.0 * LINE);
    typed(&mut world, "k");
    assert_eq!(scrolled(&world), LINE);

    let height = world.screen().bounds(Target::key(VIEW)).unwrap()[3];
    ctrl(&mut world, 'd');
    assert!((scrolled(&world) - (LINE + height / 2.0)).abs() < 1.0);
    ctrl(&mut world, 'u');
    assert!((scrolled(&world) - LINE).abs() < 1.0);

    typed(&mut world, "G");
    let bottom = scrolled(&world);
    assert!(bottom > 1000.0, "{bottom}");
    typed(&mut world, "gg");
    assert_eq!(scrolled(&world), 0.0);

    typed(&mut world, "]]");
    assert!(below_top(&world, "plan").abs() < 1.0);
    typed(&mut world, "]]");
    assert!(below_top(&world, "setup").abs() < 1.0);
    typed(&mut world, "]]");
    assert!(below_top(&world, "ship").abs() < 1.0);
    typed(&mut world, "[[");
    assert!(below_top(&world, "setup").abs() < 1.0);
    rest(&mut world);
}

#[test]
fn a_vim_sequence_waits_for_its_second_key_only_so_long_and_escape_drops_it() {
    let mut world = start(true, "m");
    typed(&mut world, "G");
    let bottom = scrolled(&world);

    typed(&mut world, "g");
    world.advance(Duration::from_millis(700));
    typed(&mut world, "g");
    assert_eq!(scrolled(&world), bottom, "the first g lapsed");

    world.advance(Duration::from_millis(700));
    typed(&mut world, "g");
    press(&mut world, Key::Escape, Mods::default());
    typed(&mut world, "g");
    assert_eq!(scrolled(&world), bottom, "Escape dropped the first g");

    world.advance(Duration::from_millis(700));
    typed(&mut world, "gk");
    assert_eq!(scrolled(&world), bottom - LINE, "k after g still scrolls");
    rest(&mut world);
}

#[test]
fn slash_finds_in_the_document_and_n_and_shift_n_step_through_the_matches_wrapping() {
    let mut world = start(true, "m");
    typed(&mut world, "/");
    assert_eq!(
        focused(&world).as_deref(),
        Some("tile.m.document.find.input")
    );
    typed(&mut world, "needle");
    assert_eq!(find_count(&world).as_deref(), Some("1/3"));

    press(&mut world, Key::Enter, Mods::default());
    assert_eq!(find_count(&world).as_deref(), Some("2/3"));
    assert_eq!(
        focused(&world).as_deref(),
        Some(VIEW),
        "Enter hands back the keyboard"
    );
    world.advance(Duration::from_secs(1));
    let at_second = scrolled(&world);
    assert!(at_second > 0.0);

    typed(&mut world, "n");
    assert_eq!(find_count(&world).as_deref(), Some("3/3"));
    world.advance(Duration::from_secs(1));
    assert!(scrolled(&world) > at_second);
    typed(&mut world, "n");
    assert_eq!(find_count(&world).as_deref(), Some("1/3"));
    typed(&mut world, "N");
    assert_eq!(find_count(&world).as_deref(), Some("3/3"));

    press(&mut world, Key::Escape, Mods::default());
    assert!(find_count(&world).is_none());
    typed(&mut world, "n");
    assert!(
        find_count(&world).is_none(),
        "n finds nothing with the bar closed"
    );
    rest(&mut world);
}

#[test]
fn a_desktop_whose_active_tile_is_a_document_takes_vim_keys_as_soon_as_it_shows() {
    let mut world = start(true, "a");
    let mut away = desktop(AWAY, 2, "m");
    away.panes[0].runtime_id = "r-b".into();
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile(AWAY),
        desktops: vec![desktop(DESKTOP, 1, "a"), away],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
    world.daemon.emit(DesktopTileContentMessage {
        desktop_id: AWAY.into(),
        tile_id: "m".into(),
        tile_kind: "markdown".into(),
        path: PLAN.into(),
        content: document(),
        ..DesktopTileContentMessage::new()
    });
    world.settle();
    typed(&mut world, "j");
    assert_eq!(scrolled(&world), LINE);
    assert!(world.daemon.sent_of("pty_input").is_empty());
    rest(&mut world);
}

#[test]
fn vim_keys_leave_terminals_fields_and_a_selection_to_comment_alone() {
    let mut world = start(true, "a");
    typed(&mut world, "j");
    assert_eq!(scrolled(&world), 0.0);
    assert!(
        !world.daemon.sent_of("pty_input").is_empty(),
        "j typed into the terminal"
    );

    world.gesture(|app| app.click(Target::key(VIEW)));
    world.settle();
    typed(&mut world, "/");
    typed(&mut world, "jk");
    let field = world
        .screen()
        .get(Target::key("tile.m.document.find.input"));
    assert_eq!(field.value.as_deref(), Some("jk"));
    assert_eq!(scrolled(&world), 0.0);
    press(&mut world, Key::Escape, Mods::default());

    world.gesture(|app| app.update(|_, tx| tx.select_text("tile.m.document.md/b1", 0..4)));
    world.settle();
    typed(&mut world, "j");
    assert!(
        world
            .screen()
            .find(Target::key("tile.m.document.composer"))
            .is_some(),
        "{}",
        world.screen()
    );
    assert_eq!(scrolled(&world), 0.0);
    rest(&mut world);
}

#[test]
fn the_palette_turns_vim_keys_on_and_off() {
    for (on, title, saved) in [
        (false, "Turn on vim keys for documents", "true"),
        (true, "Turn off vim keys for documents", "false"),
    ] {
        let mut world = start(on, "m");
        super::palette::run_command(&mut world, title);
        world.settle();
        let sent = world.daemon.sent_of("set_setting");
        let last = sent.last().expect("a setting was saved");
        assert_eq!(
            (last["key"].as_str(), last["value"].as_str()),
            (Some("vim_mode_enabled"), Some(saved))
        );
        rest(&mut world);
    }
}
