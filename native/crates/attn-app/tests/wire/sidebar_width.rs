use std::time::Duration;

use attn_app::Action;
use attn_protocol::{InitialStateMessage, SettingsUpdatedMessage};
use attn_testkit::{Daemon, Target, World};

use super::sidebar::{chord, desktop, pane, session, state};

fn with_width(px: Option<&str>) -> World {
    let mut message: InitialStateMessage = state(
        vec![session("a", "working")],
        vec![desktop("d1", "a", "", vec![pane("d1", "p1", "a")])],
    );
    if let Some(px) = px {
        message.settings = Some([("sidebar_width".to_owned(), px.to_owned())].into());
    }
    World::start(Daemon::scripted().state(message))
}

fn drawn_width(world: &World) -> f32 {
    world
        .runtime()
        .presented("sidebar")
        .expect("the sidebar is drawn")
        .0[2]
}

fn sent_widths(world: &World) -> Vec<String> {
    world
        .daemon
        .sent_of("set_setting")
        .iter()
        .filter(|m| m["key"] == "sidebar_width")
        .map(|m| m["value"].as_str().unwrap_or_default().to_owned())
        .collect()
}

/// The daemon stores the width the app sent and says so, as it does for any setting.
fn daemon_stores(world: &mut World, px: &str) {
    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([("sidebar_width".to_owned(), px.to_owned())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
}

fn notice(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("notice.text"))
        .and_then(|n| n.name)
}

#[test]
fn the_sidebar_shows_at_the_stored_width_and_the_default_without_one() {
    assert_eq!(drawn_width(&with_width(None)), 240.0);
    assert_eq!(drawn_width(&with_width(Some("360"))), 360.0);
}

#[test]
fn the_width_keys_step_forty_points_show_the_width_for_a_moment_and_name_the_limit_they_meet() {
    let mut world = with_width(None);

    world.gesture(|app| app.key(chord(Action::SidebarWider)));
    assert_eq!(sent_widths(&world), ["280"]);
    assert_eq!(drawn_width(&world), 280.0);
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.width-label"))
            .is_some()
    );
    daemon_stores(&mut world, "280");
    assert_eq!(drawn_width(&world), 280.0);

    world.advance(Duration::from_millis(1600));
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.width-label"))
            .is_none()
    );
    assert!(world.runtime_is_at_rest());

    let mut world = with_width(Some("200"));
    world.gesture(|app| app.key(chord(Action::SidebarNarrower)));
    assert!(sent_widths(&world).is_empty());
    assert_eq!(
        notice(&world).as_deref(),
        Some("The sidebar can't go below 200px.")
    );
}

#[test]
fn the_width_keys_say_the_sidebar_is_hidden_instead_of_changing_it() {
    let mut world = with_width(None);
    world.gesture(|app| app.key(chord(Action::SessionToggleSidebar)));

    world.gesture(|app| app.key(chord(Action::SidebarWider)));

    assert!(sent_widths(&world).is_empty());
    assert_eq!(
        notice(&world).as_deref(),
        Some("The sidebar is hidden. Show it to change its width.")
    );
}

#[test]
fn dragging_the_edge_follows_the_pointer_within_the_limits_and_the_release_sends_the_width() {
    let mut world = with_width(None);
    let (rect, _) = world
        .runtime()
        .presented("sidebar.resize")
        .expect("the edge is drawn");
    let (x, y) = (rect[0] + rect[2] / 2.0, rect[1] + rect[3] / 2.0);

    world.gesture(|app| {
        app.runtime.pointer_down(x, y);
        app.runtime.pointer_move(x + 60.0, y);
    });
    assert_eq!(drawn_width(&world), 300.0);
    assert_eq!(
        world
            .screen()
            .get(Target::key("sidebar.width-label"))
            .name
            .as_deref(),
        Some("300px")
    );
    world.gesture(|app| app.runtime.pointer_move(x - 400.0, y));
    assert_eq!(drawn_width(&world), 200.0, "no narrower than the narrowest");
    assert_eq!(
        world
            .screen()
            .get(Target::key("sidebar.width-label"))
            .name
            .as_deref(),
        Some("200px is the narrowest")
    );
    world.gesture(|app| {
        app.runtime.pointer_move(x + 100.0, y);
        app.runtime.pointer_up(x + 100.0, y);
    });

    assert_eq!(sent_widths(&world), ["340"]);
    assert_eq!(drawn_width(&world), 340.0, "until the daemon has it");
}

#[test]
fn escape_during_a_drag_puts_the_width_back_and_sends_nothing() {
    let mut world = with_width(None);
    let (rect, _) = world
        .runtime()
        .presented("sidebar.resize")
        .expect("the edge is drawn");
    let (x, y) = (rect[0] + rect[2] / 2.0, rect[1] + rect[3] / 2.0);
    world.gesture(|app| {
        app.runtime.pointer_down(x, y);
        app.runtime.pointer_move(x + 60.0, y);
    });

    world.gesture(|app| app.key(super::queue::plain(rune::Key::Escape)));
    world.gesture(|app| app.runtime.pointer_up(x + 60.0, y));

    assert!(sent_widths(&world).is_empty());
    assert_eq!(drawn_width(&world), 240.0);
}

#[test]
fn a_double_click_on_the_edge_goes_back_to_the_default_width() {
    let mut world = with_width(Some("300"));
    let (rect, _) = world
        .runtime()
        .presented("sidebar.resize")
        .expect("the edge is drawn");
    let (x, y) = (rect[0] + rect[2] / 2.0, rect[1] + rect[3] / 2.0);

    world.gesture(|app| {
        app.runtime.pointer_down(x, y);
        app.runtime.pointer_up(x, y);
        app.runtime.pointer_down(x, y);
        app.runtime.pointer_up(x, y);
    });

    assert_eq!(sent_widths(&world), [""]);
    assert_eq!(drawn_width(&world), 240.0);
}

#[test]
fn a_width_set_elsewhere_shows_at_once_and_waits_for_a_live_drag_to_end() {
    let mut world = with_width(None);
    daemon_stores(&mut world, "360");
    assert_eq!(drawn_width(&world), 360.0);

    let (rect, _) = world
        .runtime()
        .presented("sidebar.resize")
        .expect("the edge is drawn");
    let (x, y) = (rect[0] + rect[2] / 2.0, rect[1] + rect[3] / 2.0);
    world.gesture(|app| {
        app.runtime.pointer_down(x, y);
        app.runtime.pointer_move(x + 20.0, y);
    });
    daemon_stores(&mut world, "420");
    assert_eq!(drawn_width(&world), 380.0);

    world.gesture(|app| app.key(super::queue::plain(rune::Key::Escape)));
    assert_eq!(drawn_width(&world), 420.0);
}
