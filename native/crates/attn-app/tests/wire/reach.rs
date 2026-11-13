use std::collections::BTreeSet;

use attn_testkit::{Daemon, Target, World};

use super::sidebar::{desktop, pane, session, state};

/// The buttons on screen that Tab never reaches: the keys of every enabled button, less those
/// that held the keyboard while Tab walked the window once round.
fn unreached_by_tab(world: &mut World) -> Vec<String> {
    let buttons: BTreeSet<String> = world
        .screen()
        .all(Target::role("button"))
        .into_iter()
        .filter(|n| !n.is("disabled"))
        .filter_map(|n| n.key)
        .collect();
    let mut reached = BTreeSet::new();
    let mut first = None;
    for _ in 0..(buttons.len() * 3 + 20) {
        world.gesture(|app| app.press("tab"));
        let Some(key) = world.screen().focused().and_then(|n| n.key) else {
            continue;
        };
        if first.as_ref() == Some(&key) {
            break;
        }
        first.get_or_insert_with(|| key.clone());
        reached.insert(key);
    }
    buttons.difference(&reached).cloned().collect()
}

fn two_desktops_with_agents() -> attn_protocol::InitialStateMessage {
    let mut desktops = vec![
        desktop("d1", "a", "API", vec![pane("d1", "p1", "api")]),
        desktop("d2", "b", "Web", vec![pane("d2", "p2", "web")]),
    ];
    for (slot, d) in desktops.iter_mut().enumerate() {
        d.shortcut_slot = Some(slot as i64 + 1);
    }
    state(
        vec![session("api", "working"), session("web", "waiting_input")],
        desktops,
    )
}

#[test]
fn tab_reaches_every_button_at_home() {
    let mut world = World::start(Daemon::scripted().state(two_desktops_with_agents()));

    assert_eq!(unreached_by_tab(&mut world), Vec::<String>::new());
}

#[test]
fn tab_reaches_every_button_of_the_desktop_sidebar_and_title_bar() {
    let mut world = World::start(Daemon::scripted().state(two_desktops_with_agents()));
    world.open_desktop();

    assert_eq!(unreached_by_tab(&mut world), Vec::<String>::new());
}

#[test]
fn tab_reaches_every_button_of_the_queue_flow() {
    let mut world = World::start(Daemon::scripted().state(two_desktops_with_agents()));
    world.gesture(|app| app.click(Target::role("button").name("Switch to queue flow")));

    assert_eq!(unreached_by_tab(&mut world), Vec::<String>::new());
}

#[test]
fn tab_reaches_every_button_of_the_collapsed_rail() {
    let mut world = World::start(Daemon::scripted().state(two_desktops_with_agents()));
    world.gesture(|app| app.click(Target::role("button").name("Collapse sidebar")));

    assert_eq!(unreached_by_tab(&mut world), Vec::<String>::new());
}

#[test]
fn space_on_the_home_row_reached_by_tab_goes_home() {
    let mut world = World::start(Daemon::scripted().state(two_desktops_with_agents()));
    world.open_desktop();
    let on_row = |world: &World| {
        world.screen().focused().and_then(|n| n.key).as_deref() == Some("sidebar.home")
    };
    for _ in 0..40 {
        if on_row(&world) {
            break;
        }
        world.gesture(|app| app.press("tab"));
    }
    assert!(on_row(&world), "Tab never reached the row");
    world.gesture(|app| app.type_text(" "));

    let row = world.screen().get(Target::key("sidebar.home"));
    assert_eq!(row.value.as_deref(), Some("current"));
}
