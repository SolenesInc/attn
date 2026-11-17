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
