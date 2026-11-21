use attn_testkit::{Target, World};

use super::queue::one_desktop;

const WINDOW: (f32, f32) = (1440.0, 900.0);

fn toggle_automations(world: &mut World) {
    world.gesture(|app| app.click(Target::key("titlebar.automations")));
}

fn bounds(world: &World, key: &str) -> [f32; 4] {
    world
        .screen()
        .bounds(Target::key(key))
        .unwrap_or_else(|| panic!("{key} is on screen"))
}

#[test]
fn drawers_lie_over_home_at_their_widths_and_stack_leftward_from_the_window_edge() {
    let mut world = World::start_sized(super::palette::daemon(one_desktop(vec![]), vec![]), WINDOW);
    let sessions = bounds(&world, "home.sessions");
    let prs = bounds(&world, "home.prs");

    toggle_automations(&mut world);
    let automations = bounds(&world, "automations");
    assert_eq!(automations[2], 1440.0 * 0.42, "42% of 1440");
    assert_eq!(
        automations[0] + automations[2],
        WINDOW.0,
        "against the window's edge"
    );
    assert_eq!(
        bounds(&world, "home.sessions"),
        sessions,
        "Home does not move"
    );
    assert_eq!(bounds(&world, "home.prs"), prs, "Home does not move");

    world.gesture(|app| app.click(Target::key("titlebar.garden")));
    let garden = bounds(&world, "drawer");
    assert_eq!(garden[2], 1440.0 * 0.34, "34% of 1440");
    assert_eq!(
        garden[0] + garden[2],
        automations[0],
        "the newer drawer to the older one's left"
    );
    assert_eq!(bounds(&world, "automations"), automations);

    toggle_automations(&mut world);
    let garden = bounds(&world, "drawer");
    assert_eq!(
        garden[0] + garden[2],
        WINDOW.0,
        "the edge is free once automations closes"
    );
    assert_eq!(bounds(&world, "home.sessions"), sessions);

    world.resize((900.0, 900.0));
    assert_eq!(bounds(&world, "drawer")[2], 380.0, "never under 380");
}
