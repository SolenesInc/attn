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

#[test]
fn a_saved_scheme_attn_no_longer_has_draws_attn_and_the_other_settings_still_apply() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    update(&mut world, &[("color_scheme", "night"), ("theme", "light")]);
    let light = |group: &str, name: &str| {
        let file: Value = serde_json::from_str(ATTN).unwrap();
        file["light"][group][name].as_str().unwrap().to_owned()
    };
    assert_eq!(window_background(&world), light("ui", "bg_app"));
    assert_eq!(terminal_background(&world), light("terminal", "bg"));
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

/// The contrast of every word drawn in a `fill` inside the element keyed `within`, the element
/// itself included.
fn words_on(world: &World, within: &str, fill: rune::Color) -> Vec<f32> {
    let all = world.runtime().elements();
    let inside = |e: &rune::ElementInfo, outer: usize| {
        std::iter::successors(Some(e), |e| e.parent.map(|p| &all[p]))
            .any(|a| a.path == all[outer].path)
    };
    let root = all
        .iter()
        .position(|e| e.key.as_deref() == Some(within))
        .unwrap_or_else(|| panic!("no {within} on screen"));
    all.iter()
        .enumerate()
        .filter(|(_, e)| e.fill == fill && inside(e, root))
        .flat_map(|(i, _)| {
            all.iter()
                .filter(move |w| w.text_color.is_some() && inside(w, i))
                .map(move |w| attn_ui::colors::contrast(w.text_color.unwrap(), fill))
        })
        .collect()
}

type Status = fn(&attn_ui::Tokens) -> rune::Color;

/// Each place in `world` that draws words on a status fill, as what it is, the element it lies
/// in and the fill, measured in every scheme on both sides with `settings` kept.
fn unread_in_every_scheme(
    world: &mut World,
    settings: &[(&str, &str)],
    places: &[(&str, &str, Status)],
) -> Vec<String> {
    let mut unread = Vec::new();
    for scheme in attn_ui::colors::SCHEMES.iter() {
        for (side, dark) in [("dark", true), ("light", false)] {
            let mut all = vec![("color_scheme", scheme.id), ("theme", side)];
            all.extend_from_slice(settings);
            update(world, &all);
            let t = scheme.colors(dark).ui;
            for &(place, within, fill) in places {
                let reads = words_on(world, within, fill(&t));
                assert!(!reads.is_empty(), "{place} draws no words on its fill");
                for read in reads.into_iter().filter(|&r| r < 4.5) {
                    unread.push(format!("{}, {side}: {place} reads {read:.2}:1", scheme.id));
                }
            }
        }
    }
    unread
}

#[test]
fn a_count_or_mark_on_a_status_fill_reads_at_4_5_to_1_in_every_scheme_dark_and_light() {
    let mut bar = World::start(Daemon::scripted().state(queue(super::queue::crowding_desktops(3))));
    bar.gesture(|app| app.click(Target::role("button").name("Collapse sidebar")));
    bar.resize((1400.0, 900.0));
    let mut unread = unread_in_every_scheme(
        &mut bar,
        &[("queue_mode_enabled", "true")],
        &[
            ("the queue bar's desktop count", "queue-bar.desktops", |t| {
                t.warning
            }),
        ],
    );

    let mut asks = super::asks::start(
        vec![super::asks::cache_store(), super::asks::release_day()],
        vec![super::asks::api_working(), super::asks::web_waiting()],
    );
    asks.open_desktop();
    super::asks::open_drawer(&mut asks);
    unread.extend(unread_in_every_scheme(
        &mut asks,
        &[],
        &[
            ("the asks drawer's count", "asks.count", |t| t.accent),
            ("an asking agent's mark", "sidebar.session.web.ask", |t| {
                t.accent
            }),
        ],
    ));

    let mut vents = super::settings::analytics::triage();
    unread.extend(unread_in_every_scheme(
        &mut vents,
        &[("analytics.enabled", "true")],
        &[(
            "the vents tab's count",
            "settings.analytics.views.vents",
            |t| t.brand,
        )],
    ));

    assert!(
        unread.is_empty(),
        "words under 4.5:1 on a status fill: {unread:#?}"
    );
}
