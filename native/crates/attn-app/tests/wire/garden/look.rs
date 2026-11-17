use attn_protocol::SeedContinuation;
use attn_testkit::{Target, World};

use super::*;

fn pressed(world: &World, key: &str) -> bool {
    world
        .screen()
        .get(Target::key(key))
        .states
        .iter()
        .any(|s| s == "checked")
}

fn bounds(world: &World, key: &str) -> [f32; 4] {
    world
        .screen()
        .bounds(Target::key(key))
        .unwrap_or_else(|| panic!("{key} is on screen"))
}

#[test]
fn the_title_bar_garden_button_reads_pressed_while_the_garden_shows() {
    let mut world = World::start(daemon(vec![seed("s-aaa111", "some work")], Vec::new()));
    assert!(!pressed(&world, "titlebar.garden"));

    click(&mut world, "Show the garden");
    assert!(pressed(&world, "titlebar.garden"));

    click(&mut world, "Hide the garden");
    assert!(!pressed(&world, "titlebar.garden"));
    assert!(rests(&mut world));
}

#[test]
fn the_empty_garden_says_all_of_its_sentence_inside_the_dock() {
    let mut world = open_garden(Vec::new());

    let [frame_x, _, frame_w, _] = bounds(&world, "drawer");
    let [x, _, w, h] = bounds(&world, "garden.empty");
    assert!(
        x + w <= frame_x + frame_w,
        "the sentence ends at {} past the dock's edge at {}",
        x + w,
        frame_x + frame_w
    );
    let [_, _, _, line] = bounds(&world, "garden.trail.here");
    assert!(
        h > line * 1.5,
        "the sentence wraps: {h} tall, a line is {line}"
    );
    assert!(rests(&mut world));
}

#[test]
fn the_expanded_garden_lays_its_root_out_as_one_area_until_a_seed_is_walked_to() {
    let mut world = open_garden_sized(
        daemon(
            vec![
                plot("s-crown1", "a plot", ""),
                part_of("s-crown1", "s-leaf11", "a leaf"),
            ],
            Vec::new(),
        ),
        TWO_COLUMNS,
    );
    click(&mut world, "Expand the garden");

    let [_, _, columns, _] = bounds(&world, "garden.columns");
    let [_, _, root, _] = bounds(&world, "garden.column.0");
    assert_eq!(root, columns);

    open_row(&mut world, "a plot");
    let [_, _, root, _] = bounds(&world, "garden.column.0");
    assert!(
        root < columns / 2.0,
        "the walked root is a column: {root} of {columns}"
    );
    assert!(rests(&mut world));
}

#[test]
fn a_seeds_short_title_keeps_one_line_beside_its_actions_in_the_dock() {
    let mut world = open_garden(vec![seed("s-aaa111", "Measure row heights")]);
    open_row(&mut world, "Measure row heights");

    let [_, title_y, _, title_h] = bounds(&world, "garden.head.title");
    let [_, tile_y, _, _] = bounds(&world, "garden.open-tile");
    assert!(title_h < 30.0, "the title wraps: {title_h} tall");
    assert!(
        (tile_y - title_y).abs() < 6.0,
        "Open as tile sits beside the title"
    );
    assert!(rests(&mut world));
}

#[test]
fn every_action_beside_a_long_title_stays_inside_the_docked_page() {
    let title = "Move the cache store to the new region before the quarter closes";
    let seed = with(seed("s-aaa111", title), |s| {
        s.status = "growing".into();
        s.continuation = Some(SeedContinuation {
            agent: "claude".into(),
            cwd: "/tmp/placed".into(),
            resume_available: true,
            directory_state: "present".into(),
            ..SeedContinuation::default()
        });
    });
    let chief = Session {
        chief_of_staff: Some(true),
        ..session("chief1", "Chief")
    };
    let mut world = open_garden_with(daemon(vec![seed], vec![chief]));
    open_row(&mut world, title);

    let [page_x, _, page_w, _] = bounds(&world, "garden.page");
    for key in [
        "garden.open-tile",
        "garden.resume",
        "garden.handover.open",
        "garden.chief.open",
    ] {
        let [x, _, w, _] = bounds(&world, key);
        assert!(
            x >= page_x && x + w <= page_x + page_w,
            "{key} spans {x}..{} outside the page {page_x}..{}",
            x + w,
            page_x + page_w
        );
    }
    let [_, _, title_w, _] = bounds(&world, "garden.head.title");
    assert!(title_w >= 160.0, "the title keeps room to read: {title_w}");
    assert!(rests(&mut world));
}
