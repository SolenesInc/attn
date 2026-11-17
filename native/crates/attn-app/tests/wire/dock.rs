use attn_app::Action;
use attn_testkit::{Target, World};

use super::queue::one_desktop;
use super::sidebar::chord;

const WINDOW: (f32, f32) = (1440.0, 900.0);

fn open(world: &mut World, action: Action) {
    world.gesture(|app| app.key(chord(action)));
}

fn bounds(world: &World, key: &str) -> [f32; 4] {
    world
        .screen()
        .bounds(Target::key(key))
        .unwrap_or_else(|| panic!("{key} is on screen"))
}

#[test]
fn drawers_lie_over_home_at_the_tauri_widths_and_stack_leftward_from_the_window_edge() {
    let mut world = World::start_sized(super::palette::daemon(one_desktop(vec![]), vec![]), WINDOW);
    let sessions = bounds(&world, "home.sessions");
    let prs = bounds(&world, "home.prs");

    open(&mut world, Action::DockAttention);
    let attention = bounds(&world, "attention");
    assert_eq!(attention[2], 600.0, "48% of 1440, capped at 600");
    assert_eq!(
        attention[0] + attention[2],
        WINDOW.0,
        "against the window's edge"
    );
    assert_eq!(
        bounds(&world, "home.sessions"),
        sessions,
        "Home does not move"
    );
    assert_eq!(bounds(&world, "home.prs"), prs, "Home does not move");

    open(&mut world, Action::DockAsks);
    let asks = bounds(&world, "asks.drawer");
    assert_eq!(asks[2], 1440.0 * 0.46, "46% of 1440");
    assert_eq!(
        asks[0] + asks[2],
        attention[0],
        "the newer drawer to the older one's left"
    );
    assert_eq!(bounds(&world, "attention"), attention);

    open(&mut world, Action::DockAttention);
    let asks = bounds(&world, "asks.drawer");
    assert_eq!(
        asks[0] + asks[2],
        WINDOW.0,
        "the edge is free once attention closes"
    );
    assert_eq!(bounds(&world, "home.sessions"), sessions);

    world.resize((900.0, 900.0));
    assert_eq!(bounds(&world, "asks.drawer")[2], 420.0, "never under 420");
}
