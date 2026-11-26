use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    Desktop, DesktopPane, DesktopTileContentMessage, GardenSeedsUpdatedMessage,
    InitialStateMessage, Profile, ProfileArrangementChangedMessage, Seed, SeedEdge, Session,
};
use attn_testkit::tree::{pane, row};
use attn_testkit::{Daemon, Request, Target, World, key_event};
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

#[test]
fn a_saved_shortcut_takes_precedence_over_markdown_vim_keys() {
    let mut world = start(true, "m");
    world.daemon.emit(attn_protocol::SettingsUpdatedMessage {
        settings: Some(
            [
                ("vim_mode_enabled".into(), "true".into()),
                (
                    "keybindings_config".into(),
                    json!({"version": 1, "overrides": {"terminal.find": {"key": "j"}}}).to_string(),
                ),
            ]
            .into(),
        ),
        ..attn_protocol::SettingsUpdatedMessage::new()
    });
    world.settle();

    typed(&mut world, "j");
    assert_eq!(
        focused(&world).as_deref(),
        Some("tile.m.document.find.input")
    );
    assert_eq!(scrolled(&world), 0.0);
    press(&mut world, Key::Escape, Mods::default());
    typed(&mut world, "G");
    assert!(scrolled(&world) > 0.0);
    rest(&mut world);
}

#[test]
fn document_find_reveals_a_match_inside_collapsed_details() {
    let mut world = start(true, "m");
    let filler = "An unrelated paragraph.\n\n".repeat(30);
    let content = format!(
        "# Plan\n\nVisible needle.\n\n{filler}<details>\n<summary>Setup details</summary>\n\n<details>\n<summary>Nested details</summary>\n\nHidden needle.\n\n</details>\n\n</details>\n\n<details>\n<summary>Other details</summary>\n\nSpare text.\n\n</details>\n\n{filler}",
    );
    world.daemon.emit(DesktopTileContentMessage {
        desktop_id: DESKTOP.into(),
        tile_id: "m".into(),
        tile_kind: "markdown".into(),
        path: PLAN.into(),
        content,
        ..DesktopTileContentMessage::new()
    });
    world.settle();

    press_action(&mut world, &Action::TerminalFind);
    typed(&mut world, "needle");
    assert_eq!(find_count(&world).as_deref(), Some("1/2"));
    let nested = Target::role("button").name("Nested details");
    assert!(world.screen().find(nested.clone()).is_none());

    press(&mut world, Key::Enter, Mods::default());
    assert_eq!(find_count(&world).as_deref(), Some("2/2"));
    rest(&mut world);
    let hidden_on_screen = |world: &World| {
        let screen = world.screen();
        let view = screen.bounds(Target::key(VIEW)).unwrap();
        assert!(
            screen
                .bounds(Target::role("label").name("Hidden needle."))
                .is_some_and(|hit| hit[1] >= view[1] && hit[1] + hit[3] <= view[1] + view[3]),
            "Find should scroll the revealed match into view"
        );
    };
    hidden_on_screen(&world);
    assert!(world.screen().get(nested.clone()).is("expanded"));
    assert!(
        world
            .screen()
            .get(Target::role("button").name("Other details"))
            .is("collapsed")
    );

    press(&mut world, Key::Escape, Mods::default());
    let [x, y, w, h] = world.screen().bounds(nested.clone()).unwrap();
    world.gesture(|app| app.click_at((x + w / 2.0, y + h / 2.0)));
    world.settle();
    assert!(world.screen().get(nested).is("collapsed"));

    press_action(&mut world, &Action::TerminalFind);
    typed(&mut world, "Hidden needle");
    assert_eq!(find_count(&world).as_deref(), Some("1/1"));
    rest(&mut world);
    hidden_on_screen(&world);
}

const SEED_VIEW: &str = "seed-tile.t.viewport";
const READER: &str = "garden.reader";
const STACKED: &str = "garden.viewport";

fn long_seed(id: &str, title: &str) -> Seed {
    Seed {
        body: document(),
        ..super::garden::seed(id, title)
    }
}

fn child_of(plot: &str, seed: Seed) -> Seed {
    let mut seed = seed;
    seed.edges.push(SeedEdge {
        kind: "part-of".into(),
        to: plot.into(),
    });
    seed
}

/// The garden of these tests: a plot holding the seed `s-long01`, whose plan is the long
/// document, beside two loose seeds.
fn garden() -> Vec<Seed> {
    vec![
        super::garden::seed("s-plot01", "The plot"),
        child_of("s-plot01", long_seed("s-long01", "The long seed")),
        long_seed("s-loose1", "A loose seed"),
        super::garden::seed("s-loose2", "Another loose seed"),
    ]
}

/// A window of `size` over the garden, with a desktop whose tiles `tiles` hold the keyboard
/// from `active`, and vim keys on or off.
fn start_garden(vim: bool, size: (f32, f32), tiles: serde_json::Value, active: &str) -> World {
    let seeds = garden();
    let known = seeds.clone();
    let daemon = Daemon::scripted().state(InitialStateMessage {
        settings: Some([("vim_mode_enabled".to_owned(), vim.to_string())].into()),
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![profile(DESKTOP)]),
        desktops: Some(vec![Desktop {
            tree_json: tiles.to_string(),
            active_pane_id: active.into(),
            ..desktop(DESKTOP, 1, active)
        }]),
        sessions: Some(vec![Session {
            id: "s-a".into(),
            label: "Agent a".into(),
            directory: "/work/a".into(),
            profile_id: "work".into(),
            ..Session::default()
        }]),
        seeds_total: Some(seeds.len() as i64),
        seeds: Some(seeds),
        ..InitialStateMessage::new()
    });
    daemon.on("seed_document_get", move |r: &Request| {
        super::garden::document_reply(r, known.iter().find(|s| s.id == r["seed_id"]).cloned())
    });
    let mut world = World::start_sized(daemon, size);
    world.open_desktop();
    world.settle();
    world
}

fn seed_tile(leaf: &str, id: &str) -> serde_json::Value {
    json!({
        "type": "tile",
        "tile_id": leaf,
        "tile_kind": "seed",
        "tile_params": id,
    })
}

/// A desktop of the agent `a` beside a tile of seed `s-long01` holding the keyboard.
fn start_seed_tile(vim: bool) -> World {
    start_garden(
        vim,
        (1600.0, 900.0),
        row(vec![pane("a"), seed_tile("t", "s-long01")]),
        "t",
    )
}

fn scrolled_in(world: &World, view: &str) -> f32 {
    world.screen().scrolled(Target::key(view)).unwrap()
}

/// How far below the top of the view `view` the element keyed `key` sits.
fn below(world: &World, view: &str, key: &str) -> f32 {
    let screen = world.screen();
    let view = screen.bounds(Target::key(view)).unwrap();
    let at = screen.bounds(Target::key(key)).unwrap();
    at[1] - view[1]
}

fn count_in(world: &World, view: &str) -> Option<String> {
    world
        .screen()
        .find(Target::key(format!("{view}.find.count")))
        .and_then(|n| n.name)
}

fn row_focused(world: &World) -> Option<String> {
    focused(world).filter(|k| k.starts_with("garden.row."))
}

#[test]
fn vim_keys_read_a_seed_tile_and_its_find_bar_searches_the_seed() {
    let mut world = start_seed_tile(true);
    typed(&mut world, "jj");
    assert_eq!(scrolled_in(&world, SEED_VIEW), 2.0 * LINE);
    let height = world.screen().bounds(Target::key(SEED_VIEW)).unwrap()[3];
    ctrl(&mut world, 'd');
    assert!((scrolled_in(&world, SEED_VIEW) - (2.0 * LINE + height / 2.0)).abs() < 1.0);
    typed(&mut world, "gg");
    assert_eq!(scrolled_in(&world, SEED_VIEW), 0.0);

    typed(&mut world, "]]");
    assert!(
        below(&world, SEED_VIEW, "seed-tile.t.authorization.heading").abs() < 1.0,
        "the sections' headings are stops too"
    );
    typed(&mut world, "]]");
    assert!(below(&world, SEED_VIEW, "plan").abs() < 1.0);
    typed(&mut world, "]]");
    assert!(below(&world, SEED_VIEW, "setup").abs() < 1.0);
    typed(&mut world, "[[");
    assert!(below(&world, SEED_VIEW, "plan").abs() < 1.0);

    typed(&mut world, "/");
    assert_eq!(
        focused(&world).as_deref(),
        Some("seed-tile.t.viewport.find.input")
    );
    typed(&mut world, "needle");
    assert_eq!(count_in(&world, SEED_VIEW).as_deref(), Some("1/3"));
    press(&mut world, Key::Enter, Mods::default());
    typed(&mut world, "n");
    assert_eq!(count_in(&world, SEED_VIEW).as_deref(), Some("3/3"));
    typed(&mut world, "N");
    assert_eq!(count_in(&world, SEED_VIEW).as_deref(), Some("2/3"));
    rest(&mut world);
}

#[test]
fn with_vim_keys_off_a_seed_tile_ignores_them_and_its_find_bar_still_opens() {
    let mut world = start_seed_tile(false);
    typed(&mut world, "jG");
    assert_eq!(scrolled_in(&world, SEED_VIEW), 0.0);
    press_action(&mut world, &Action::TerminalFind);
    typed(&mut world, "needle");
    assert_eq!(count_in(&world, SEED_VIEW).as_deref(), Some("1/3"));
    rest(&mut world);
}

#[test]
fn escape_in_a_seed_tile_drops_a_sequence_then_closes_find_then_climbs_to_the_plot() {
    let mut world = start_seed_tile(true);
    typed(&mut world, "g");
    press(&mut world, Key::Escape, Mods::default());
    typed(&mut world, "/");
    press(&mut world, Key::Escape, Mods::default());
    assert!(count_in(&world, SEED_VIEW).is_none());
    assert!(world.daemon.sent_of("desktop_update_tile").is_empty());

    press(&mut world, Key::Escape, Mods::default());
    let moves = world.daemon.sent_of("desktop_update_tile");
    assert_eq!(moves.len(), 1);
    assert_eq!(moves[0]["tile_params"], "s-plot01");
    rest(&mut world);
}

#[test]
fn a_selection_in_a_seed_tile_still_takes_a_typed_key_as_a_comment() {
    let mut world = start_seed_tile(true);
    world.gesture(|app| app.update(|_, tx| tx.select_text("tile.t.document.md/b1", 0..4)));
    world.settle();
    typed(&mut world, "j");
    assert!(
        world
            .screen()
            .find(Target::key("tile.t.document.composer"))
            .is_some(),
        "{}",
        world.screen()
    );
    assert_eq!(scrolled_in(&world, SEED_VIEW), 0.0);
    rest(&mut world);
}

#[test]
fn a_sequence_started_in_one_tile_does_not_finish_in_another() {
    let tiles = row(vec![seed_tile("t", "s-long01"), seed_tile("u", "s-loose1")]);
    let mut world = start_garden(true, (1600.0, 900.0), tiles, "t");
    typed(&mut world, "G");
    let bottom = scrolled_in(&world, SEED_VIEW);
    assert!(bottom > 1000.0, "{bottom}");

    world.gesture(|app| app.click(Target::key("seed-tile.u.head.title")));
    world.settle();
    typed(&mut world, "g");
    world.gesture(|app| app.click(Target::key("seed-tile.t.head.title")));
    world.settle();
    typed(&mut world, "g");
    assert_eq!(scrolled_in(&world, SEED_VIEW), bottom);
    rest(&mut world);
}

#[test]
fn a_tile_that_closes_takes_its_find_bar_with_it() {
    let mut world = start_seed_tile(true);
    typed(&mut world, "/");
    assert!(count_in(&world, SEED_VIEW).is_some());

    for tree in [pane("a"), row(vec![pane("a"), seed_tile("t", "s-long01")])] {
        world.daemon.emit(ProfileArrangementChangedMessage {
            profile: profile(DESKTOP),
            desktops: vec![Desktop {
                tree_json: tree.to_string(),
                ..desktop(DESKTOP, 1, "a")
            }],
            ..ProfileArrangementChangedMessage::new()
        });
        world.settle();
    }
    assert!(world.screen().find(Target::key(SEED_VIEW)).is_some());
    assert!(count_in(&world, SEED_VIEW).is_none());
    rest(&mut world);
}

/// The garden open in the dock, or holding the window, over a desktop of the agent alone.
fn open_garden(vim: bool, full: bool) -> World {
    let size = if full {
        (1520.0, 900.0)
    } else {
        (1100.0, 800.0)
    };
    let mut world = start_garden(vim, size, pane("a"), "a");
    world.gesture(|app| app.key(super::sidebar::chord(Action::BoardOpen)));
    world.settle();
    if full {
        world.gesture(|app| app.click(Target::role("button").name("Expand the garden")));
        world.settle();
    }
    world
}

fn open_row(world: &mut World, id: &str) {
    world.gesture(|app| app.click(Target::key(format!("garden.row.{id}"))));
    world.settle();
}

#[test]
fn in_columns_the_list_walks_with_vim_keys_and_the_reader_reads_once_it_has_focus() {
    let mut world = open_garden(true, true);
    open_row(&mut world, "s-loose1");
    assert_eq!(row_focused(&world), None);
    typed(&mut world, "j");
    let first = row_focused(&world).expect("j walks to a row");
    typed(&mut world, "G");
    let last = row_focused(&world).expect("G walks to the last row");
    assert_ne!(first, last);
    typed(&mut world, "gg");
    assert_eq!(row_focused(&world), Some(first));

    ctrl(&mut world, 'd');
    assert!(
        scrolled_in(&world, READER) > 0.0,
        "Ctrl-d scrolls the reader"
    );
    ctrl(&mut world, 'u');
    assert_eq!(scrolled_in(&world, READER), 0.0);

    for _ in 0..3 {
        if row_focused(&world).as_deref() != Some("garden.row.s-loose1") {
            typed(&mut world, "j");
        }
    }
    typed(&mut world, "l");
    assert_eq!(focused(&world).as_deref(), Some(READER));
    typed(&mut world, "jj");
    assert_eq!(scrolled_in(&world, READER), 2.0 * LINE);
    typed(&mut world, "]]]]");
    assert!(below(&world, READER, "plan").abs() < 1.0);

    typed(&mut world, "/");
    assert_eq!(focused(&world).as_deref(), Some("garden.reader.find.input"));
    typed(&mut world, "needle");
    press(&mut world, Key::Enter, Mods::default());
    assert_eq!(
        focused(&world).as_deref(),
        Some(READER),
        "Enter hands the keyboard back"
    );
    typed(&mut world, "n");
    assert_eq!(count_in(&world, READER).as_deref(), Some("3/3"));

    press(&mut world, Key::Escape, Mods::default());
    assert!(count_in(&world, READER).is_none());
    assert_eq!(focused(&world).as_deref(), Some(READER));
    typed(&mut world, "h");
    assert_eq!(row_focused(&world).as_deref(), Some("garden.row.s-loose1"));

    press(&mut world, Key::Enter, Mods::default());
    assert_eq!(focused(&world).as_deref(), Some(READER));
    press(&mut world, Key::Escape, Mods::default());
    assert_eq!(row_focused(&world).as_deref(), Some("garden.row.s-loose1"));
    rest(&mut world);
}

#[test]
fn a_press_in_the_columns_reader_gives_it_the_keyboard_and_cmd_f_finds_in_the_seed() {
    let mut world = open_garden(false, true);
    open_row(&mut world, "s-loose1");
    world.gesture(|app| app.click(Target::key("garden.head.title")));
    world.settle();
    assert_eq!(focused(&world).as_deref(), Some(READER));

    let accel = if attn_testkit::platform() == rune::Platform::Mac {
        Mods {
            cmd: true,
            ..Mods::default()
        }
    } else {
        Mods {
            ctrl: true,
            ..Mods::default()
        }
    };
    press(&mut world, Key::Command('f'), accel);
    typed(&mut world, "needle");
    assert_eq!(count_in(&world, READER).as_deref(), Some("1/3"));
    rest(&mut world);
}

#[test]
fn in_the_dock_the_list_walks_until_the_walk_stands_on_a_seed_whose_page_vim_keys_read() {
    let mut world = open_garden(true, false);
    typed(&mut world, "j");
    assert!(row_focused(&world).is_some(), "j walks the root list");

    open_row(&mut world, "s-loose1");
    typed(&mut world, "jj");
    assert_eq!(scrolled_in(&world, STACKED), 2.0 * LINE);
    typed(&mut world, "/");
    assert_eq!(
        focused(&world).as_deref(),
        Some("garden.viewport.find.input")
    );
    typed(&mut world, "needle");
    assert_eq!(count_in(&world, STACKED).as_deref(), Some("1/3"));
    press(&mut world, Key::Escape, Mods::default());
    assert!(count_in(&world, STACKED).is_none());
    assert!(
        world
            .screen()
            .find(Target::role("region").name("The garden"))
            .is_some(),
        "Escape closed the find bar, not the garden"
    );
    rest(&mut world);
}

#[test]
fn in_columns_l_opens_a_walked_row_and_a_second_l_reads_it() {
    let mut world = open_garden(true, true);
    for _ in 0..3 {
        if row_focused(&world).as_deref() != Some("garden.row.s-loose1") {
            typed(&mut world, "j");
        }
    }
    typed(&mut world, "l");
    assert!(world.screen().find(Target::key(READER)).is_some());
    typed(&mut world, "l");
    assert_eq!(focused(&world).as_deref(), Some(READER));
    typed(&mut world, "j");
    assert_eq!(scrolled_in(&world, READER), LINE);
    rest(&mut world);
}

#[test]
fn escape_in_the_reader_of_a_seed_whose_row_left_the_list_goes_to_the_garden() {
    let mut world = open_garden(true, true);
    open_row(&mut world, "s-loose1");
    world.gesture(|app| app.click(Target::key("garden.head.title")));
    world.settle();
    assert_eq!(focused(&world).as_deref(), Some(READER));

    let mut seeds = garden();
    seeds[2].status = "harvested".into();
    world.daemon.emit(GardenSeedsUpdatedMessage {
        profile_id: "work".into(),
        total: seeds.len() as i64,
        seeds,
        ..GardenSeedsUpdatedMessage::new()
    });
    world.settle();
    assert!(
        world
            .screen()
            .find(Target::key("garden.row.s-loose1"))
            .is_none()
    );

    press(&mut world, Key::Escape, Mods::default());
    assert_eq!(focused(&world).as_deref(), Some("garden.body"));
    press(&mut world, Key::Escape, Mods::default());
    assert!(
        world.screen().find(Target::key(READER)).is_none(),
        "the garden closed"
    );
    rest(&mut world);
}

#[test]
fn saved_shortcuts_take_precedence_over_garden_reading_keys() {
    for (full, view) in [(false, STACKED), (true, READER)] {
        for (binding, leader) in [
            (json!({"key": "j"}), None),
            (
                json!({
                    "leader": {"key": "y", "meta": true, "shift": true},
                    "then": {"key": "j"}
                }),
                Some("mod+shift+y"),
            ),
        ] {
            let mut world = open_garden(true, full);
            open_row(&mut world, "s-loose1");
            if full {
                world.gesture(|app| app.click(Target::key("garden.head.title")));
                world.settle();
            }
            bind_keyboard_shortcuts(&mut world, binding);

            if let Some(leader) = leader {
                world.gesture(|app| app.press(leader));
            }
            typed(&mut world, "j");
            assert!(
                world
                    .screen()
                    .find(Target::role("dialog").name("Keyboard Shortcuts"))
                    .is_some(),
                "saved shortcut should open Keyboard Shortcuts while reading in {view}, leader {leader:?}"
            );
            press(&mut world, Key::Escape, Mods::default());
            assert_eq!(scrolled_in(&world, view), 0.0);
            rest(&mut world);
        }
    }
}

#[test]
fn a_saved_chord_takes_enter_before_the_focused_garden_row_does() {
    let mut world = open_garden(true, true);
    open_row(&mut world, "s-loose1");
    for _ in 0..3 {
        if row_focused(&world).as_deref() != Some("garden.row.s-loose1") {
            typed(&mut world, "j");
        }
    }
    assert_eq!(row_focused(&world).as_deref(), Some("garden.row.s-loose1"));
    bind_keyboard_shortcuts(
        &mut world,
        json!({
            "leader": {"key": "y", "meta": true, "shift": true},
            "then": {"key": "Enter"}
        }),
    );

    world.gesture(|app| app.press("mod+shift+y"));
    press(&mut world, Key::Enter, Mods::default());
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Keyboard Shortcuts"))
            .is_some()
    );
    rest(&mut world);
}

fn bind_keyboard_shortcuts(world: &mut World, binding: serde_json::Value) {
    world.daemon.emit(attn_protocol::SettingsUpdatedMessage {
        settings: Some(
            [
                ("vim_mode_enabled".into(), "true".into()),
                (
                    "keybindings_config".into(),
                    json!({"version": 1, "overrides": {"ui.showShortcuts": binding}}).to_string(),
                ),
            ]
            .into(),
        ),
        ..attn_protocol::SettingsUpdatedMessage::new()
    });
    world.settle();
}

#[test]
fn escape_drops_a_saved_chord_before_leaving_the_garden_reader() {
    let mut world = open_garden(true, true);
    open_row(&mut world, "s-loose1");
    world.gesture(|app| app.click(Target::key("garden.head.title")));
    world.settle();
    bind_keyboard_shortcuts(
        &mut world,
        json!({
            "leader": {"key": "y", "meta": true, "shift": true},
            "then": {"key": "j"}
        }),
    );

    world.gesture(|app| app.press("mod+shift+y"));
    press(&mut world, Key::Escape, Mods::default());
    assert_eq!(focused(&world).as_deref(), Some(READER));
    typed(&mut world, "j");
    assert_eq!(scrolled_in(&world, READER), LINE);
    rest(&mut world);
}
