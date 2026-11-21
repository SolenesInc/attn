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
    let right = world.rect("titlebar.right");
    assert_eq!(
        pill[0] + pill[2],
        right[0] + right[2],
        "the profile pill ends the title bar"
    );
}

#[test]
fn the_first_title_bar_control_stands_one_gap_past_the_lights_at_every_text_size_and_the_bar_is_even_without_them()
 {
    let mut world = world();
    let lights = super::MACOS_WINDOW_CONTROLS;
    world.window_controls_take(lights);
    let mut gaps = Vec::new();
    for tenths in 7..=15 {
        let scale = format!("{}", f64::from(tenths) / 10.0);
        world.daemon.emit(SettingsUpdatedMessage {
            settings: Some([("uiScale".to_owned(), scale.clone())].into()),
            ..SettingsUpdatedMessage::new()
        });
        world.settle();
        let zoom = f64::from(world.runtime().zoom());
        assert!((zoom - f64::from(tenths) / 10.0).abs() < 1e-6, "{scale}");
        gaps.push((
            scale,
            world.rect("titlebar.new")[0] * zoom - f64::from(lights),
        ));
    }
    let first = gaps[0].1;
    assert!(first > 0.0, "New starts under the lights: {gaps:?}");
    for (scale, gap) in &gaps {
        assert!(
            (gap - first).abs() < 0.05,
            "at {scale} New stands {gap} window points past the lights, not {first}: {gaps:?}"
        );
    }

    world.window_controls_take(0.0);
    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([("uiScale".to_owned(), "1".to_owned())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    let window = world.rect("window");
    let pill = world.rect("titlebar.profile");
    assert_eq!(
        world.rect("titlebar.new")[0],
        window[2] - (pill[0] + pill[2]),
        "without the lights New starts as far in as the profile pill ends"
    );
}
