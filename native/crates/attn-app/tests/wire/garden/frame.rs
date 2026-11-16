use std::path::Path;

use attn_app::Action;
use attn_app::whats_new::{BANNER_PREF, RELEASE, SEEN_PREF};
use attn_testkit::{Target, World};
use rune::Key;
use rune::prefs::Prefs;

use super::*;

fn seeds() -> Vec<Seed> {
    vec![seed("s-alone1", "unrelated work")]
}

fn toggle_attention(world: &mut World) {
    world.gesture(|app| app.key(super::super::sidebar::chord(Action::DockAttention)));
}

fn attention_open(world: &World) -> bool {
    world.screen().find(Target::key("attention")).is_some()
}

fn board_shown(world: &World) -> bool {
    world
        .screen()
        .find(Target::role("region").name("The garden board"))
        .is_some()
}

fn launch(prefs: &Path) -> World {
    World::start_with_prefs(daemon(seeds(), Vec::new()), Prefs::at(prefs))
}

#[test]
fn preserves_opening_order_after_restoring_the_garden() {
    for (attention_first, board) in [(true, false), (true, true), (false, false), (false, true)] {
        let case = format!("attention first: {attention_first}, board: {board}");
        let mut world = World::start(daemon(seeds(), Vec::new()));
        if attention_first {
            toggle_attention(&mut world);
            click(&mut world, "Show the garden");
        } else {
            click(&mut world, "Show the garden");
            toggle_attention(&mut world);
        }
        click(&mut world, "Expand the garden");
        if board {
            click(&mut world, "board");
            world.gesture(|app| app.key(super::board()));
        } else {
            click(&mut world, "Return the garden to the dock");
        }

        key(&mut world, Key::Escape);

        let expected = if attention_first { "closed" } else { "dock" };
        assert_eq!(shown(&world), expected, "{case}");
        assert_eq!(attention_open(&world), attention_first, "{case}");
        key(&mut world, Key::Escape);
        assert_eq!(shown(&world), "closed", "{case}");
        assert!(!attention_open(&world), "{case}");
        assert!(rests(&mut world), "{case}");
    }
}

#[test]
fn dismisses_an_expanded_garden_before_a_dock_panel_opened_after_its_list() {
    let mut world = open_garden(seeds());
    toggle_attention(&mut world);
    click(&mut world, "Expand the garden");

    key(&mut world, Key::Escape);

    assert_eq!(shown(&world), "closed");
    assert!(attention_open(&world));
    key(&mut world, Key::Escape);
    assert!(!attention_open(&world));
    assert!(rests(&mut world));
}

#[test]
fn keeps_the_chosen_fullscreen_view_through_dismissal_the_dock_and_a_relaunch() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("prefs.json");
    let mut prefs = Prefs::at(&path);
    for key in [SEEN_PREF, BANNER_PREF] {
        prefs.set(key, &RELEASE.to_owned()).unwrap();
    }

    let mut world = launch(&path);
    world.gesture(|app| app.key(super::board()));
    world.gesture(|app| app.key(super::board()));
    click(&mut world, "board");
    assert!(board_shown(&world));

    key(&mut world, Key::Escape);
    assert_eq!(shown(&world), "closed");
    world.gesture(|app| app.key(super::board()));
    assert!(board_shown(&world));

    world.gesture(|app| app.key(super::board()));
    assert_eq!(shown(&world), "dock");
    assert!(!board_shown(&world));

    world.gesture(|app| app.key(super::board()));
    assert!(board_shown(&world));
    assert!(rests(&mut world));
    drop(world);

    let mut relaunched = launch(&path);
    relaunched.gesture(|app| app.key(super::board()));
    assert_eq!(shown(&relaunched), "full");
    assert!(board_shown(&relaunched));
    assert!(rests(&mut relaunched));
}

#[test]
fn a_long_plot_title_in_the_docked_trail_leaves_the_frame_button_clear() {
    let long = "A plot whose title runs on well past what the docked garden can show in one line";
    let inner = "An inner plot whose title also runs on past what the docked garden can show";
    let mut world = open_garden(vec![
        plot("s-crown1", long, ""),
        plot("s-inner1", inner, "s-crown1"),
        part_of("s-inner1", "s-leaf11", "the actual work"),
    ]);
    open_row(&mut world, long);
    open_row(&mut world, inner);

    ends_before_the_frame_button(&world, "garden.trail.1");
    world.gesture(|app| {
        app.click(Target::key("garden.search.field"));
        app.type_text("work");
    });
    ends_before_the_frame_button(&world, "garden.trail.here");
    assert!(rests(&mut world));
}

fn ends_before_the_frame_button(world: &World, crumb: &str) {
    let screen = world.screen();
    let [frame_x, ..] = screen.bounds(Target::key("garden.frame-button")).unwrap();
    let [x, _, w, _] = screen
        .bounds(Target::key(crumb))
        .unwrap_or_else(|| panic!("{crumb} is on screen"));
    assert!(
        x + w <= frame_x,
        "{crumb} ends at {} and the frame button starts at {frame_x}",
        x + w
    );
}
