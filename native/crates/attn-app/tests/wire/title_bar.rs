use attn_protocol::{InitialStateMessage, Profile, SettingsUpdatedMessage};
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

#[test]
fn the_first_title_bar_control_starts_at_tauris_78_window_points_past_the_lights_at_every_text_size()
 {
    let mut world = world();
    world.window_controls_take(69.0);
    assert_eq!(world.rect("titlebar.new")[0], 78.0);
    for tenths in 7..=15 {
        let scale = format!("{}", f64::from(tenths) / 10.0);
        world.daemon.emit(SettingsUpdatedMessage {
            settings: Some([("uiScale".to_owned(), scale.clone())].into()),
            ..SettingsUpdatedMessage::new()
        });
        world.settle();
        let zoom = f64::from(world.runtime().zoom());
        assert!((zoom - f64::from(tenths) / 10.0).abs() < 1e-6, "{scale}");
        let new_in_window_points = world.rect("titlebar.new")[0] * zoom;
        assert!(
            (new_in_window_points - 78.0).abs() < 0.05,
            "at {scale} New starts {new_in_window_points} window points in, not Tauri's 78"
        );
    }

    world.window_controls_take(0.0);
    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([("uiScale".to_owned(), "1".to_owned())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(world.rect("titlebar.new")[0], 8.0);
}
