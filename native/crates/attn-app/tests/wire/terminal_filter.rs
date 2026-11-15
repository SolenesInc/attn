use attn_testkit::{Target, World};
use rune::{Key, KeyEvent, Mods};

use super::terminal::{inputs, one_terminal, output, typed};
use super::terminal_blocks::{pick, ran, right_click};

/// The find key: ⌘F on macOS, Ctrl+Shift+F elsewhere.
const FIND: &str = if cfg!(target_os = "macos") {
    "mod+f"
} else {
    "mod+shift+f"
};

fn started(text: &[u8]) -> World {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, text);
    world.settle();
    world
}

/// Opens the filter on the block under the first output row from the context menu.
fn open_filter(world: &mut World) {
    right_click(world, 1, 1);
    pick(world, "filter-block");
}

fn count(world: &World) -> String {
    world
        .screen()
        .get(Target::key("terminal.r1.filter.count"))
        .name
        .unwrap_or_default()
}

/// The output lines the panel lists, by their place in the block's output.
fn listed(world: &World, lines: u32) -> Vec<u32> {
    let screen = world.screen();
    (0..lines)
        .filter(|n| {
            screen
                .find(Target::key(format!("terminal.r1.filter.line.{n}")))
                .is_some()
        })
        .collect()
}

fn escape(world: &mut World) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Escape,
            mods: Mods::default(),
        })
    });
}

#[test]
fn the_menu_filters_a_blocks_output_and_a_listed_line_scrolls_the_view_to_it() {
    let output: String = (1..=60).map(|n| format!("item-{n}\r\n")).collect();
    let mut world = started(&ran("seq", &output));
    open_filter(&mut world);
    let field = world.screen().get(Target::key("terminal.r1.filter.field"));
    assert!(field.is("focused"), "the filter field takes the keyboard");
    assert!(
        world
            .screen()
            .find(Target::key("terminal.r1.filter.results"))
            .is_none(),
        "an empty query lists nothing"
    );

    world.gesture(|app| app.type_text("item-1"));
    assert_eq!(count(&world), "11 lines");
    assert_eq!(
        listed(&world, 60),
        [0, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18]
    );
    assert_eq!(
        inputs(&world),
        [],
        "typing in the filter reaches no program"
    );

    world.gesture(|app| app.click(Target::key("terminal.r1.filter.line.11")));
    assert_eq!(world.terminal_text("r1")[0], "item-12");

    world.gesture(|app| app.click(Target::key("terminal.r1.filter.close")));
    assert!(
        world
            .screen()
            .find(Target::key("terminal.r1.filter"))
            .is_none()
    );
    world.gesture(|app| app.type_text("x"));
    assert_eq!(
        inputs(&world),
        [typed("x")],
        "typing reaches the terminal again"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn letters_match_whatever_their_case_until_match_case_is_on() {
    let mut world = started(&ran("cat names", "Été\r\nété\r\nete\r\n"));
    open_filter(&mut world);
    world.gesture(|app| app.type_text("été"));
    assert_eq!(count(&world), "2 lines");
    assert_eq!(listed(&world, 3), [0, 1]);

    world.gesture(|app| app.click(Target::key("terminal.r1.filter.case")));
    assert_eq!(count(&world), "1 line");
    assert_eq!(listed(&world, 3), [1]);
    let field = world.screen().get(Target::key("terminal.r1.filter.field"));
    assert!(
        field.is("focused"),
        "Match case gives the keyboard back to the field"
    );

    world.gesture(|app| app.type_text("x"));
    assert_eq!(count(&world), "0 lines");
    let empty = world.screen().get(Target::key("terminal.r1.filter.empty"));
    assert_eq!(empty.name.as_deref(), Some("No matching lines"));

    escape(&mut world);
    assert!(
        world
            .screen()
            .find(Target::key("terminal.r1.filter"))
            .is_none()
    );
    world.gesture(|app| app.type_text("y"));
    assert_eq!(inputs(&world), [typed("y")], "Escape gives typing back");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn find_and_the_filter_take_each_others_place() {
    let mut world = started(&ran("echo hi", "hi\r\n"));
    open_filter(&mut world);
    let at = super::terminal::cell(&world, 1, 3);
    world.gesture(|app| app.click_at(at));
    world.gesture(|app| app.press(FIND));
    let screen = world.screen();
    assert!(screen.find(Target::key("terminal.r1.filter")).is_none());
    assert!(
        screen
            .get(Target::key("terminal.r1.find.field"))
            .is("focused")
    );

    open_filter(&mut world);
    let screen = world.screen();
    assert!(screen.find(Target::key("terminal.r1.find")).is_none());
    assert!(
        screen
            .get(Target::key("terminal.r1.filter.field"))
            .is("focused")
    );
    assert!(world.runtime_is_at_rest());
}
