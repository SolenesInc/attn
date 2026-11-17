use std::collections::BTreeMap;

use attn_protocol::SettingsUpdatedMessage;
use attn_testkit::{Daemon, Target, World};
use serde_json::Value;

use super::queue::{one_desktop, queue, turn};
use super::terminal::one_terminal;

const ATTN: &str = include_str!("../../../../../internal/colors/schemes/attn.json");
const CATPPUCCIN: &str = include_str!("../../../../../internal/colors/schemes/catppuccin.json");

fn dark(scheme: &str, group: &str, name: &str) -> String {
    let file: Value = serde_json::from_str(scheme).unwrap();
    file["dark"][group][name].as_str().unwrap().to_owned()
}

fn attn(group: &str, name: &str) -> String {
    dark(ATTN, group, name)
}

fn catppuccin(group: &str, name: &str) -> String {
    dark(CATPPUCCIN, group, name)
}

fn update(world: &mut World, settings: &[(&str, &str)]) {
    let settings: BTreeMap<String, String> = settings
        .iter()
        .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
        .collect();
    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some(settings),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
}

fn window_background(world: &World) -> String {
    let [r, g, b, _] = world.runtime().clear_color();
    let byte = |c: f32| (c * 255.0).round() as u8;
    format!("#{:02x}{:02x}{:02x}", byte(r), byte(g), byte(b))
}

fn terminal_background(world: &World) -> String {
    let themes = world.daemon.sent_of("set_terminal_theme");
    let last = themes.last().expect("the daemon heard the terminal colors");
    last["background"].as_str().unwrap().to_owned()
}

#[test]
fn a_scheme_and_an_override_recolor_the_window_and_its_terminals_as_they_arrive() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    assert_eq!(window_background(&world), attn("ui", "bg_app"));
    assert_eq!(terminal_background(&world), attn("terminal", "bg"));

    update(&mut world, &[("color_scheme", "catppuccin")]);
    assert_eq!(window_background(&world), catppuccin("ui", "bg_app"));
    assert_eq!(terminal_background(&world), catppuccin("terminal", "bg"));

    update(
        &mut world,
        &[
            ("color_scheme", "catppuccin"),
            (
                "color_overrides",
                r##"{"dark":{"ui":{"bg_app":"#24273a"},"terminal":{"bg":"#303446"}},"light":{"ui":{"bg_app":"#ffffff"}}}"##,
            ),
        ],
    );
    assert_eq!(window_background(&world), "#24273a");
    assert_eq!(terminal_background(&world), "#303446");

    update(&mut world, &[]);
    assert_eq!(window_background(&world), attn("ui", "bg_app"));
    assert_eq!(terminal_background(&world), attn("terminal", "bg"));
}

fn menu_fill(world: &World, key: &str) -> String {
    let menu = world
        .runtime()
        .elements()
        .into_iter()
        .find(|e| e.key.as_deref() == Some(key))
        .unwrap_or_else(|| panic!("no {key} on screen"));
    menu.fill.hex()[..7].to_owned()
}

#[test]
fn a_standard_menu_fills_with_the_scheme_s_panel_and_its_override_as_they_arrive() {
    const MENU: &str = "snooze.menu.snooze-menu";
    let mut world =
        World::start(Daemon::scripted().state(queue(one_desktop(vec![turn("t-old", 30)]))));
    world.gesture(|app| app.click(Target::role("button").name("Snooze t-old")));
    assert_eq!(menu_fill(&world, MENU), attn("ui", "bg_panel"));

    let queue_mode = ("queue_mode_enabled", "true");
    update(&mut world, &[queue_mode, ("color_scheme", "catppuccin")]);
    assert_eq!(menu_fill(&world, MENU), catppuccin("ui", "bg_panel"));

    update(
        &mut world,
        &[
            queue_mode,
            ("color_scheme", "catppuccin"),
            (
                "color_overrides",
                r##"{"dark":{"ui":{"bg_panel":"#2a2b3c"}}}"##,
            ),
        ],
    );
    assert_eq!(menu_fill(&world, MENU), "#2a2b3c");
}

fn label_ink(world: &World, label: &str) -> String {
    let text = world
        .runtime()
        .elements()
        .into_iter()
        .find(|e| e.text.as_deref() == Some(label))
        .unwrap_or_else(|| panic!("no {label} on screen"));
    text.text_color.expect("a label has ink").hex()[..7].to_owned()
}

#[test]
fn a_standard_menu_draws_the_row_the_keys_reach_in_primary_ink_and_the_rest_in_secondary() {
    let mut world =
        World::start(Daemon::scripted().state(queue(one_desktop(vec![turn("t-old", 30)]))));
    world.gesture(|app| app.click(Target::role("button").name("Snooze t-old")));
    let (primary, secondary) = (attn("ui", "text_primary"), attn("ui", "text_secondary"));
    assert_eq!(label_ink(&world, "For 30 minutes"), primary);
    assert_eq!(label_ink(&world, "For an hour"), secondary);

    world.gesture(|app| app.key(super::queue::plain(rune::Key::Down)));
    assert_eq!(label_ink(&world, "For 30 minutes"), secondary);
    assert_eq!(label_ink(&world, "For an hour"), primary);
}
