use attn_protocol::{InitialStateMessage, Profile};
use attn_testkit::{Daemon, World};

fn world() -> World {
    World::start(Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        ..InitialStateMessage::new()
    }))
}

fn left_to_right(world: &mut World, keys: &[&str]) -> Vec<String> {
    let mut placed: Vec<(f64, String)> = keys
        .iter()
        .map(|key| (world.rect(key)[0], (*key).to_owned()))
        .collect();
    placed.sort_by(|a, b| a.0.total_cmp(&b.0));
    placed.into_iter().map(|(_, key)| key).collect()
}

#[test]
fn the_title_bar_keeps_its_tools_left_and_its_panels_right_in_the_tauri_apps_order() {
    let mut world = world();
    let tools = [
        "titlebar.new",
        "titlebar.commands",
        "titlebar.flow",
        "titlebar.sidebar",
        "titlebar.notifications",
    ];
    let panels = [
        "titlebar.garden",
        "titlebar.notebook",
        "titlebar.ledger",
        "titlebar.automations",
        "titlebar.asks",
        "titlebar.profile",
    ];
    let all: Vec<&str> = tools.iter().chain(panels.iter()).copied().collect();
    assert_eq!(left_to_right(&mut world, &all), all);

    let window = world.rect("window");
    let tools_end = world.rect("titlebar.notifications");
    let panels_start = world.rect("titlebar.garden");
    let pill = world.rect("titlebar.profile");
    assert!(
        tools_end[0] + tools_end[2] < window[2] / 2.0 && panels_start[0] > window[2] / 2.0,
        "the tools sit at the window's left and the panels at its right"
    );
    assert!(
        window[2] - (pill[0] + pill[2]) <= 8.0,
        "the profile pill ends the title bar"
    );
}
