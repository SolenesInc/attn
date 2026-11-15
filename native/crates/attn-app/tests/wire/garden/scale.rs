use attn_protocol::SettingsUpdatedMessage;

use super::*;

fn set(world: &mut World, settings: &[(&str, &str)]) {
    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some(
            settings
                .iter()
                .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
                .collect(),
        ),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
}

/// `target`'s drawn box in window points: the accessibility tree gives it in the view's,
/// which the app's text size zooms.
fn drawn(world: &World, target: Target) -> [f32; 4] {
    let screen = world.screen();
    let bounds = screen
        .bounds(target)
        .unwrap_or_else(|| panic!("not on screen:\n{screen}"));
    bounds.map(|v| v * world.runtime().zoom())
}

fn drawn_height(world: &World, target: Target) -> f32 {
    drawn(world, target)[3]
}

fn assert_near(seen: f32, expected: f32, what: &str) {
    assert!(
        (seen - expected).abs() < 0.5,
        "{what}: drawn {seen} points, expected {expected}"
    );
}

#[test]
fn the_garden_draws_at_its_saved_size_in_the_box_it_has_and_matches_the_app_size_until_it_has_one()
{
    let mut world = open_garden(vec![seed("s-cache1", "Cache store migration")]);
    let row = || Target::key("garden.row.s-cache1");
    let unscaled = drawn_height(&world, row());
    let drawer = drawn(&world, Target::key("drawer"));

    set(&mut world, &[("gardenScale", "1.5")]);
    assert_near(
        drawn_height(&world, row()),
        unscaled * 1.5,
        "garden at 150%",
    );
    assert_eq!(drawn(&world, Target::key("drawer")), drawer);
    assert!((world.runtime().zoom() - 1.0).abs() < 1e-6);

    set(&mut world, &[("uiScale", "1.2")]);
    assert_near(
        drawn_height(&world, row()),
        unscaled * 1.2,
        "garden matching the app at 120%",
    );

    set(&mut world, &[("uiScale", "1.2"), ("gardenScale", "0.8")]);
    assert_near(
        drawn_height(&world, row()),
        unscaled * 0.8,
        "garden at 80% in an app at 120%",
    );
    open_row(&mut world, "Cache store migration");
    assert_eq!(heading(&world).as_deref(), Some("Cache store migration"));
    assert!(rests(&mut world));
}

#[test]
fn the_board_draws_its_cards_at_the_saved_garden_size() {
    let mut world = open_garden_sized(
        daemon(vec![seed("s-cache1", "Cache store migration")], Vec::new()),
        THREE_COLUMNS,
    );
    world.gesture(|app| app.key(board()));
    click(&mut world, "board");
    let card = world
        .screen()
        .all(Target::role("button").name("Cache store migration"))
        .into_iter()
        .find_map(|n| n.key.filter(|k| k.starts_with("garden.board.card.")))
        .unwrap_or_else(|| panic!("no card:\n{}", world.screen()));
    let unscaled = drawn_height(&world, Target::key(&card));

    set(&mut world, &[("gardenScale", "1.3")]);
    assert_near(
        drawn_height(&world, Target::key(&card)),
        unscaled * 1.3,
        "card at 130%",
    );
    assert!(rests(&mut world));
}

#[test]
fn a_seed_tile_draws_its_seed_at_the_saved_garden_size_in_the_tile_it_has() {
    let mut world = super::tile::start("s-cache1", vec![super::tile::cache()], |r| {
        super::tile::document(r, super::tile::cache(), Vec::new(), &["picked up"])
    });
    let title = || Target::key("seed-tile.t.head.title");
    let unscaled = drawn_height(&world, title());
    let tile = drawn(&world, Target::key("tile.t"));

    set(&mut world, &[("gardenScale", "1.4")]);
    assert_near(
        drawn_height(&world, title()),
        unscaled * 1.4,
        "seed tile at 140%",
    );
    assert_eq!(drawn(&world, Target::key("tile.t")), tile);
    assert!(rests(&mut world));
}
