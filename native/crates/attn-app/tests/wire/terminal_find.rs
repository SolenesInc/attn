use attn_testkit::{Target, World};
use rune::{Key, KeyEvent, Mods, PointerButton};

use super::terminal::{cell, inputs, one_terminal, output, typed};
use super::terminal_view::view;

/// The find key: ⌘F on macOS, Ctrl+Shift+F elsewhere, where Ctrl+F is the shell's.
const FIND: &str = if cfg!(target_os = "macos") {
    "mod+f"
} else {
    "mod+shift+f"
};

const YELLOW: &str = "#f5c542";

fn open(world: &mut World) {
    world.gesture(|app| app.press(FIND));
}

fn count(world: &World) -> String {
    world
        .screen()
        .get(Target::key("terminal.r1.find.count"))
        .name
        .unwrap_or_default()
}

fn key(world: &mut World, key: Key, shift: bool) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods {
                shift,
                ..Mods::default()
            },
        })
    });
}

/// The yellow washes over the view, as `(row, [from, to], alpha)`.
fn matches(world: &World) -> Vec<(u64, [u64; 2], u64)> {
    view(world)["overlays"]
        .as_array()
        .unwrap()
        .iter()
        .filter(|o| o["color"] == YELLOW)
        .map(|o| {
            let cols = o["cols"].as_array().unwrap();
            (
                o["row"].as_u64().unwrap(),
                [cols[0].as_u64().unwrap(), cols[1].as_u64().unwrap()],
                o["alpha"].as_u64().unwrap(),
            )
        })
        .collect()
}

fn started(text: &[u8]) -> World {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, text);
    world.settle();
    world
}

#[test]
fn the_find_key_opens_the_bar_and_typing_washes_every_match_with_the_newest_focused() {
    let mut world = started(b"one apple\r\ntwo Apple\r\nthree\r\n");
    open(&mut world);
    let field = world.screen().get(Target::key("terminal.r1.find.field"));
    assert!(field.is("focused"), "the find field takes the keyboard");

    world.gesture(|app| app.type_text("apple"));
    assert_eq!(count(&world), "2/2");
    assert_eq!(matches(&world), [(0, [4, 9], 71), (1, [4, 9], 153)]);
    assert_eq!(inputs(&world), [], "typing in the bar reaches no program");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn enter_steps_to_older_matches_and_shift_enter_to_newer_wrapping_around() {
    let mut world = started(b"apple\r\napple\r\napple\r\n");
    open(&mut world);
    world.gesture(|app| app.type_text("apple"));
    assert_eq!(count(&world), "3/3");

    key(&mut world, Key::Enter, false);
    assert_eq!(count(&world), "2/3");
    assert_eq!(matches(&world)[1].2, 153);
    key(&mut world, Key::Enter, false);
    key(&mut world, Key::Enter, false);
    assert_eq!(
        count(&world),
        "3/3",
        "stepping past the oldest wraps to the newest"
    );
    key(&mut world, Key::Enter, true);
    assert_eq!(count(&world), "1/3", "and past the newest to the oldest");

    world.gesture(|app| app.click(Target::key("terminal.r1.find.next")));
    assert_eq!(count(&world), "2/3");
    world.gesture(|app| app.click(Target::key("terminal.r1.find.prev")));
    assert_eq!(count(&world), "1/3");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn match_case_keeps_only_the_matches_with_the_querys_case() {
    let mut world = started(b"one apple\r\ntwo Apple\r\n");
    open(&mut world);
    world.gesture(|app| app.type_text("Apple"));
    assert_eq!(count(&world), "2/2");

    world.gesture(|app| app.click(Target::key("terminal.r1.find.case")));
    assert_eq!(count(&world), "1/1");
    assert_eq!(matches(&world), [(1, [4, 9], 153)]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_match_that_wraps_washes_both_rows() {
    let mut world = started(b"");
    let cols = world.terminal_size("r1").0 as usize;
    let mut text = "x".repeat(cols - 3).into_bytes();
    text.extend_from_slice(b"needle\r\n");
    output(&world, 2, &text);
    world.settle();

    open(&mut world);
    world.gesture(|app| app.type_text("needle"));
    assert_eq!(count(&world), "1/1");
    let cols = cols as u64;
    assert_eq!(
        matches(&world),
        [(0, [cols - 3, cols], 153), (1, [0, 3], 153)]
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn stepping_to_a_match_in_the_history_scrolls_the_view_to_it() {
    let mut lines: String = "needle at the top\r\n".into();
    lines.extend((0..200).map(|n| format!("filler {n}\r\n")));
    lines.push_str("needle at the bottom\r\n");
    let mut world = started(lines.as_bytes());
    open(&mut world);
    world.gesture(|app| app.type_text("needle"));
    assert_eq!(
        view(&world)["scrolled_back"],
        0,
        "the newest match already shows"
    );

    key(&mut world, Key::Enter, false);
    assert_eq!(world.terminal_text("r1")[0], "needle at the top");
    assert_eq!(matches(&world), [(0, [0, 6], 153)]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn output_while_the_bar_is_open_counts_its_matches_and_closed_it_commits_nothing() {
    let mut world = started(b"apple\r\n");
    open(&mut world);
    world.gesture(|app| app.type_text("apple"));
    assert_eq!(count(&world), "1/1");

    output(&world, 2, b"apple again\r\n");
    world.settle();
    assert_eq!(count(&world), "1/2", "the focused match stays where it was");

    key(&mut world, Key::Escape, false);
    assert!(
        world
            .screen()
            .find(Target::key("terminal.r1.find"))
            .is_none()
    );
    assert_eq!(matches(&world), []);
    let commits = world.runtime().stats.commits;
    output(&world, 3, b"apple once more\r\n");
    world.settle();
    assert_eq!(
        world.runtime().stats.commits,
        commits,
        "output with the bar closed runs no view"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn escape_gives_typing_back_to_the_terminal_and_the_bar_opens_again_on_its_query() {
    let mut world = started(b"apple\r\n");
    open(&mut world);
    world.gesture(|app| app.type_text("apple"));
    key(&mut world, Key::Escape, false);

    world.gesture(|app| app.type_text("l"));
    assert_eq!(inputs(&world), [typed("l")]);

    open(&mut world);
    let field = world.screen().get(Target::key("terminal.r1.find.field"));
    assert_eq!(field.value.as_deref(), Some("apple"));
    assert_eq!(count(&world), "1/1");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_context_menus_find_opens_the_bar() {
    let mut world = started(b"apple\r\n");
    let at = cell(&world, 1, 3);
    world.gesture(|app| {
        app.runtime.pointer_move(at.0, at.1);
        app.runtime
            .button_down(at.0, at.1, PointerButton::Secondary);
        app.runtime.button_up(at.0, at.1, PointerButton::Secondary);
    });
    world.gesture(|app| app.runtime.resolve_popup(Some("find")).unwrap());

    let field = world.screen().get(Target::key("terminal.r1.find.field"));
    assert!(field.is("focused"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_press_on_the_terminal_beside_the_bar_still_selects() {
    let mut world = started(b"alpha beta\r\n");
    open(&mut world);
    let (a, b) = (cell(&world, 0, 0), cell(&world, 4, 0));
    world.gesture(|app| {
        app.runtime.pointer_down(a.0, a.1);
        app.runtime.pointer_move(b.0, b.1);
        app.runtime.pointer_up(b.0, b.1);
    });
    assert_eq!(view(&world)["copies"], "alpha");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_focused_match_stays_on_its_text_when_the_oldest_history_is_dropped() {
    let mut world = started(b"");
    let line = |n: u32| format!("line-{n:07}-{}\r\n", "x".repeat(60));
    let full = 8 * 1024 * 1024 / 70 + 10_000;
    for (seq, from) in (0..full).step_by(10_000).enumerate() {
        let chunk: String = (from..(from + 10_000).min(full)).map(line).collect();
        output(&world, seq as u32 + 2, chunk.as_bytes());
    }
    output(&world, 100, b"needle one\r\nneedle two\r\nneedle three\r\n");
    world.settle();
    open(&mut world);
    world.gesture(|app| app.type_text("needle"));
    key(&mut world, Key::Enter, false);
    assert_eq!(count(&world), "2/3");

    let more: String = (full..full + 600).map(line).collect();
    output(&world, 101, more.as_bytes());
    world.settle();
    assert_eq!(count(&world), "2/3", "the focus stays on 'needle two'");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_click_on_the_terminal_gives_it_typing_with_the_bar_still_open() {
    let mut world = started(b"alpha\r\n");
    open(&mut world);
    let at = cell(&world, 1, 3);
    world.gesture(|app| app.click_at(at));
    world.gesture(|app| app.type_text("x"));
    assert_eq!(inputs(&world), [typed("x")]);
    let field = world.screen().get(Target::key("terminal.r1.find.field"));
    assert!(!field.is("focused"));
    assert!(world.runtime_is_at_rest());
}
