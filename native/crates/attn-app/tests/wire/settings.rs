use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::go_values::FEATURES;
use attn_protocol::{
    GitHubHostsUpdatedMessage, InitialStateMessage, Profile, SettingsUpdatedMessage,
};
use attn_testkit::{Daemon, Request, Target, World, key_event, message};
use rune::{Key, KeyEvent, KeyPlatform, Mods};
use serde_json::{Value, json};

type Settings = Arc<Mutex<BTreeMap<String, String>>>;

/// A daemon that keeps settings as the real one does: a write it accepts is answered, then
/// broadcast to every client, and one `refuse` turns down is answered with its reason.
fn keeping(
    settings: &[(&str, &str)],
    refuse: impl Fn(&str, &str) -> Option<String> + Send + 'static,
) -> (Daemon, Settings) {
    let stored: Settings = Arc::new(Mutex::new(
        settings
            .iter()
            .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
            .collect(),
    ));
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        settings: Some(stored.lock().unwrap().clone()),
        github_hosts: Some(vec!["github.com".into(), "ghe.example.com".into()]),
        ..InitialStateMessage::new()
    });
    let kept = stored.clone();
    daemon.on("set_setting", move |r| write(&kept, &refuse, r));
    (daemon, stored)
}

fn write(
    stored: &Settings,
    refuse: &impl Fn(&str, &str) -> Option<String>,
    r: &Request,
) -> Vec<Value> {
    let key = r["key"].as_str().unwrap();
    let value = r["value"].as_str().unwrap();
    let request_id = r["request_id"].as_str().map(str::to_owned);
    if let Some(reason) = refuse(key, value) {
        return vec![message(SettingsUpdatedMessage {
            request_id,
            changed_key: Some(key.into()),
            success: Some(false),
            error: Some(reason),
            settings: Some(stored.lock().unwrap().clone()),
            ..SettingsUpdatedMessage::new()
        })];
    }
    let mut settings = stored.lock().unwrap();
    if value.is_empty() {
        settings.remove(key);
    } else {
        settings.insert(key.into(), value.into());
    }
    let broadcast = message(SettingsUpdatedMessage {
        settings: Some(settings.clone()),
        ..SettingsUpdatedMessage::new()
    });
    match request_id {
        Some(_) => {
            let answer = message(SettingsUpdatedMessage {
                request_id,
                changed_key: Some(key.into()),
                success: Some(true),
                ..SettingsUpdatedMessage::new()
            });
            vec![answer, broadcast]
        }
        None => vec![broadcast],
    }
}

fn accepting(settings: &[(&str, &str)]) -> (Daemon, Settings) {
    keeping(settings, |_, _| None)
}

fn chord(action: Action) -> KeyEvent {
    match Keymap::from_setting(None, KeyPlatform::current()).binding(&action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    }
}

fn press_action(world: &mut World, action: Action) {
    let ev = chord(action);
    world.gesture(|app| app.key(ev));
}

fn press(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

fn click(world: &mut World, key: &str) {
    world
        .automate("press", json!({"target": {"key": key}}))
        .unwrap_or_else(|why| panic!("press {key}: {why}"));
}

fn state(world: &mut World) -> Value {
    world
        .automate("state", json!({"of": "settings"}))
        .expect("the settings slice")
}

fn open(world: &mut World) {
    press_action(world, Action::UiOpenSettings);
    assert_eq!(state(world)["open"], true);
}

fn named(world: &World, key: &str) -> Option<String> {
    world.screen().find(Target::key(key)).and_then(|n| n.name)
}

fn writes(world: &World) -> Vec<(String, String)> {
    world
        .daemon
        .sent_of("set_setting")
        .iter()
        .map(|r| {
            (
                r["key"].as_str().unwrap().to_owned(),
                r["value"].as_str().unwrap().to_owned(),
            )
        })
        .collect()
}

fn field(setting: &str) -> String {
    format!("settings.input.{setting}.field")
}

fn value(world: &World, key: &str) -> String {
    world
        .screen()
        .get(Target::key(key))
        .value
        .unwrap_or_default()
}

fn retype(world: &mut World, key: &str, text: &str) {
    click(world, key);
    for _ in 0..value(world, key).chars().count() {
        press(world, Key::Backspace);
    }
    world.gesture(|app| app.type_text(text));
}

fn nav(world: &World) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key("settings.nav"))
        .into_iter()
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with("settings.nav."))
        })
        .filter_map(|n| n.name)
        .collect()
}

fn rest(world: &mut World) {
    world.advance(Duration::from_secs(7));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_settings_shortcut_opens_the_dialog_on_github_hosts_and_closes_it() {
    let (daemon, _) = accepting(&[]);
    let mut world = World::start(daemon);

    open(&mut world);
    assert_eq!(state(&mut world)["activeSection"], "connectivity");
    assert_eq!(
        named(&world, "settings.heading").as_deref(),
        Some("GitHub hosts")
    );
    let hosts: Vec<_> = world
        .screen()
        .children(Target::key("settings.github.hosts"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert_eq!(hosts, ["github.com", "ghe.example.com"]);

    press_action(&mut world, Action::UiCommandPalette);
    assert!(world.screen().find(Target::key("palette.dialog")).is_none());
    assert_eq!(state(&mut world)["open"], true);

    press_action(&mut world, Action::UiOpenSettings);
    assert_eq!(state(&mut world)["open"], false);
    assert!(writes(&world).is_empty());
    rest(&mut world);
}

#[test]
fn escape_the_close_button_and_the_backdrop_each_close_the_dialog() {
    let (daemon, _) = accepting(&[]);
    let mut world = World::start(daemon);

    open(&mut world);
    press(&mut world, Key::Escape);
    assert_eq!(state(&mut world)["open"], false);

    open(&mut world);
    click(&mut world, "settings.close");
    assert_eq!(state(&mut world)["open"], false);

    open(&mut world);
    world.gesture(|app| app.click_at((4.0, 200.0)));
    assert_eq!(state(&mut world)["open"], false);

    open(&mut world);
    click(&mut world, "settings.heading");
    assert_eq!(state(&mut world)["open"], true);
    rest(&mut world);
}

#[test]
fn the_palette_and_the_home_gear_open_the_dialog() {
    let (daemon, _) = accepting(&[]);
    let mut world = World::start(daemon);
    world.daemon.on("get_command_usage", |r| {
        message(attn_protocol::GetCommandUsageResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            profile_id: r["profile_id"].as_str().unwrap().into(),
            success: true,
            ..attn_protocol::GetCommandUsageResultMessage::new()
        })
    });
    world.daemon.on("record_command_usage", |_| ());

    press_action(&mut world, Action::UiCommandPalette);
    world.gesture(|app| app.type_text("Settings"));
    press(&mut world, Key::Enter);
    assert_eq!(state(&mut world)["open"], true);
    press(&mut world, Key::Escape);

    click(&mut world, "home.settings");
    assert_eq!(state(&mut world)["open"], true);
    rest(&mut world);
}

#[test]
fn the_hosts_follow_what_the_daemon_rediscovers_and_its_reason_once_polling_stops() {
    let (daemon, _) = accepting(&[]);
    let mut world = World::start(daemon);
    open(&mut world);

    world.daemon.emit(GitHubHostsUpdatedMessage {
        github_hosts: vec!["github.com".into()],
        ..GitHubHostsUpdatedMessage::new()
    });
    world.settle();
    let hosts = world
        .screen()
        .children(Target::key("settings.github.hosts"));
    assert_eq!(hosts.len(), 1);

    world.daemon.emit(GitHubHostsUpdatedMessage {
        github_hosts: vec![],
        github_polling_off_reason: Some("GitHub polling is off: gh is not installed".into()),
        ..GitHubHostsUpdatedMessage::new()
    });
    world.settle();
    assert!(
        world
            .screen()
            .find(Target::key("settings.github.hosts"))
            .is_none()
    );
    let reason = world
        .screen()
        .get(Target::key("settings.github.polling-off"));
    assert_eq!(
        reason.name.as_deref(),
        Some("GitHub polling is off: gh is not installed")
    );
    rest(&mut world);
}

#[test]
fn search_leaves_the_sections_whose_words_match_and_says_when_none_do() {
    let (daemon, _) = accepting(&[]);
    let mut world = World::start(daemon);
    open(&mut world);
    assert_eq!(
        nav(&world),
        [
            "Appearance",
            "Files and locations",
            "Attention queue",
            "Agents and models",
            "Models",
            "Background agents",
            "Delegation",
            "GitHub hosts",
            "Outposts",
            "Phones",
            "Paired senders",
            "Plugins",
            "Updates",
            "Experimental",
            "Task runner",
            "Event bus",
            "Analytics",
        ]
    );

    click(&mut world, "settings.search.field");
    world.gesture(|app| app.type_text("sweep"));
    assert_eq!(nav(&world), ["Files and locations"]);
    assert_eq!(state(&mut world)["search"], "sweep");

    for feature in [
        "Launcher 2.0",
        "Outposts",
        "Phone access",
        "Usage analytics",
    ] {
        retype(&mut world, "settings.search.field", feature);
        assert!(
            nav(&world).contains(&"Experimental".to_owned()),
            "{feature} finds Experimental"
        );
    }
    retype(&mut world, "settings.search.field", "iphone");
    assert!(nav(&world).contains(&"Phones".to_owned()));

    retype(&mut world, "settings.search.field", "zzz");
    assert!(nav(&world).is_empty());
    assert!(
        world
            .screen()
            .descendants(Target::key("settings.nav"))
            .iter()
            .any(|n| n.name.as_deref() == Some("No matching settings."))
    );
    rest(&mut world);
}

#[test]
fn every_section_is_headed_by_its_navigation_name() {
    let (daemon, _) = accepting(&[]);
    let mut world = World::start(daemon);
    open(&mut world);
    let sections: Vec<(String, String)> = world
        .screen()
        .descendants(Target::key("settings.nav"))
        .into_iter()
        .filter_map(|n| Some((n.key?, n.name?)))
        .filter(|(key, _)| key.starts_with("settings.nav."))
        .collect();
    assert_eq!(sections.len(), 17);
    for (key, name) in sections {
        click(&mut world, &key);
        assert_eq!(named(&world, "settings.heading"), Some(name.clone()));
        let headings = world
            .screen()
            .descendants(Target::key("settings.body"))
            .into_iter()
            .filter(|n| n.role == "heading")
            .count();
        assert_eq!(headings, 1, "{name} has one heading");
    }
    rest(&mut world);
}

#[test]
fn a_theme_click_saves_the_theme_and_the_window_follows_it() {
    let (daemon, stored) = accepting(&[]);
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.general");
    assert_eq!(state(&mut world)["activeSection"], "general");
    let dark = world
        .screen()
        .get(Target::key("settings.choice.theme.dark"));
    assert!(dark.is("checked"));

    click(&mut world, "settings.choice.theme.light");

    assert_eq!(writes(&world), [("theme".to_owned(), "light".to_owned())]);
    assert_eq!(stored.lock().unwrap()["theme"], "light");
    let light = world
        .screen()
        .get(Target::key("settings.choice.theme.light"));
    assert!(light.is("checked"));
    rest(&mut world);
}

#[test]
fn the_text_size_keys_step_the_scale_by_tenths_within_its_range_and_the_view_zooms_by_it() {
    let (daemon, _) = accepting(&[]);
    let mut world = World::start(daemon);

    press_action(&mut world, Action::UiIncreaseFontSize);
    assert_eq!(writes(&world).last().unwrap().1, "1.1");
    world.settle();
    assert!((world.runtime().zoom() - 1.1).abs() < 1e-6);

    open(&mut world);
    click(&mut world, "settings.nav.general");
    assert_eq!(
        named(&world, "settings.scale.app.value").as_deref(),
        Some("110%")
    );

    press_action(&mut world, Action::UiDecreaseFontSize);
    press_action(&mut world, Action::UiDecreaseFontSize);
    assert_eq!(writes(&world).last().unwrap().1, "0.9");
    assert_eq!(
        named(&world, "settings.scale.app.value").as_deref(),
        Some("90%")
    );

    press_action(&mut world, Action::UiResetFontSize);
    assert_eq!(writes(&world).last().unwrap().1, "1");
    assert!((world.runtime().zoom() - 1.0).abs() < 1e-6);
    assert!(
        world
            .screen()
            .find(Target::key("settings.scale.app.reset"))
            .is_none()
    );
    rest(&mut world);
}

#[test]
fn the_app_size_buttons_step_it_and_reset_shows_only_off_the_default() {
    let (daemon, _) = accepting(&[("uiScale", "1.3")]);
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.general");
    assert_eq!(
        named(&world, "settings.scale.app.value").as_deref(),
        Some("130%")
    );

    click(&mut world, "settings.scale.app.up");
    assert_eq!(writes(&world).last().unwrap().1, "1.4");
    click(&mut world, "settings.scale.app.down");
    click(&mut world, "settings.scale.app.down");
    assert_eq!(writes(&world).last().unwrap().1, "1.2");
    assert_eq!(
        named(&world, "settings.scale.app.value").as_deref(),
        Some("120%")
    );

    click(&mut world, "settings.scale.app.reset");
    assert_eq!(writes(&world).last().unwrap().1, "1");
    assert_eq!(
        named(&world, "settings.scale.app.value").as_deref(),
        Some("100%")
    );
    assert!(
        world
            .screen()
            .find(Target::key("settings.scale.app.reset"))
            .is_none()
    );
    rest(&mut world);
}

#[test]
fn the_text_size_stops_at_its_bounds_and_ignores_a_stored_size_outside_them() {
    let (daemon, _) = accepting(&[("uiScale", "1.5")]);
    let mut world = World::start(daemon);
    press_action(&mut world, Action::UiIncreaseFontSize);
    assert!(writes(&world).is_empty());

    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([("uiScale".to_owned(), "2".to_owned())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert!((world.runtime().zoom() - 1.0).abs() < 1e-6);
    rest(&mut world);
}

#[test]
fn the_garden_matches_the_app_until_it_is_stepped_from_the_app_size_and_match_app_clears_it() {
    let (daemon, _) = accepting(&[("uiScale", "1.2")]);
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.general");
    assert_eq!(
        named(&world, "settings.scale.garden.value").as_deref(),
        Some("Match app")
    );
    assert!(
        world
            .screen()
            .find(Target::key("settings.scale.garden.reset"))
            .is_none()
    );

    click(&mut world, "settings.scale.garden.up");
    assert_eq!(
        writes(&world).last().unwrap(),
        &("gardenScale".to_owned(), "1.3".to_owned())
    );
    assert_eq!(
        named(&world, "settings.scale.garden.value").as_deref(),
        Some("130%")
    );

    click(&mut world, "settings.scale.garden.reset");
    assert_eq!(
        writes(&world).last().unwrap(),
        &("gardenScale".to_owned(), String::new())
    );
    assert_eq!(
        named(&world, "settings.scale.garden.value").as_deref(),
        Some("Match app")
    );
    rest(&mut world);
}

#[test]
fn a_field_saves_when_focus_leaves_it_and_shows_saved_only_once_the_daemon_acknowledged() {
    let (daemon, stored) = accepting(&[("narrowest_tile_cols", "66")]);
    let held: Arc<Mutex<Vec<Request>>> = Arc::default();
    let keep = held.clone();
    daemon.on("set_setting", move |r| keep.lock().unwrap().push(r.clone()));
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.general");
    let key = field("narrowest_tile_cols");
    assert_eq!(value(&world, &key), "66");

    retype(&mut world, &key, " 80 ");
    assert!(held.lock().unwrap().is_empty());
    click(&mut world, "settings.heading");

    let request = held.lock().unwrap().remove(0);
    assert_eq!(request["value"], "80");
    assert_eq!(
        named(&world, "settings.autosave").as_deref(),
        Some("Saving…")
    );

    let refuse = |_: &str, _: &str| None;
    for reply in write(&stored, &refuse, &request) {
        world.daemon.emit(reply);
    }
    world.settle();
    assert_eq!(named(&world, "settings.autosave").as_deref(), Some("Saved"));
    assert!(
        world
            .screen()
            .find(Target::key("settings.saved.narrowest_tile_cols"))
            .is_some()
    );

    click(&mut world, &key);
    click(&mut world, "settings.heading");
    assert!(held.lock().unwrap().is_empty());
    rest(&mut world);
    assert!(
        world
            .screen()
            .find(Target::key("settings.saved.narrowest_tile_cols"))
            .is_none()
    );
}

#[test]
fn a_refused_value_is_sent_once_per_enter_shows_its_reason_beside_the_field_and_retry_resends() {
    let (daemon, _) = keeping(&[], |key, value| {
        (key == "sidebar_width" && value == "9000")
            .then(|| "sidebar_width must be between 200 and 600".to_owned())
    });
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.general");
    let key = field("sidebar_width");

    retype(&mut world, &key, "9000");
    press(&mut world, Key::Enter);
    assert_eq!(writes(&world).len(), 1);
    let refusal = world
        .screen()
        .get(Target::key("settings.refusal.sidebar_width"));
    assert_eq!(
        refusal.name.as_deref(),
        Some("sidebar_width must be between 200 and 600")
    );
    assert!(
        world
            .screen()
            .descendants(Target::key("settings.autosave"))
            .iter()
            .all(|n| n.role != "alert")
    );

    click(&mut world, "settings.heading");
    assert_eq!(writes(&world).len(), 1);

    click(&mut world, "settings.retry.sidebar_width");
    assert_eq!(writes(&world).len(), 2);

    retype(&mut world, &key, "320");
    click(&mut world, "settings.heading");
    assert_eq!(writes(&world).last().unwrap().1, "320");
    assert!(
        world
            .screen()
            .find(Target::key("settings.refusal.sidebar_width"))
            .is_none()
    );
    rest(&mut world);
}

#[test]
fn closing_commits_the_focused_field_and_a_refusal_keeps_the_dialog_open() {
    let (daemon, stored) = keeping(&[], |key, value| {
        (key == "narrowest_tile_cols" && value == "0")
            .then(|| "narrowest_tile_cols must be at least 1".to_owned())
    });
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.general");
    let key = field("narrowest_tile_cols");

    retype(&mut world, &key, "0");
    press(&mut world, Key::Escape);
    assert_eq!(state(&mut world)["open"], true);
    assert_eq!(
        writes(&world),
        [("narrowest_tile_cols".to_owned(), "0".to_owned())]
    );
    assert!(
        world
            .screen()
            .find(Target::key("settings.refusal.narrowest_tile_cols"))
            .is_some()
    );
    assert_eq!(
        world.screen().focused().and_then(|n| n.key),
        Some(key.clone()),
        "a refused close leaves the keyboard in the field to fix"
    );
    world.gesture(|app| app.type_text("1"));
    assert_eq!(value(&world, &key), "01");

    retype(&mut world, &key, "0");
    click(&mut world, "settings.retry.narrowest_tile_cols");
    assert_eq!(writes(&world).len(), 2);
    click(&mut world, "settings.close");
    assert_eq!(state(&mut world)["open"], true);

    retype(&mut world, &key, "90");
    click(&mut world, "settings.close");
    assert_eq!(state(&mut world)["open"], false);
    assert_eq!(stored.lock().unwrap()["narrowest_tile_cols"], "90");

    open(&mut world);
    assert_eq!(value(&world, &key), "90");
    retype(&mut world, &key, "91");
    press_action(&mut world, Action::UiOpenSettings);
    assert_eq!(state(&mut world)["open"], false);
    assert_eq!(stored.lock().unwrap()["narrowest_tile_cols"], "91");
    rest(&mut world);
}

#[test]
fn a_field_keeps_what_is_typed_when_another_setting_changes_and_follows_its_own_value_otherwise() {
    let (daemon, _) = accepting(&[("narrowest_tile_cols", "66"), ("sidebar_width", "300")]);
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.general");
    retype(&mut world, &field("narrowest_tile_cols"), "7");

    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some(
            [
                ("narrowest_tile_cols".to_owned(), "66".to_owned()),
                ("sidebar_width".to_owned(), "360".to_owned()),
                ("theme".to_owned(), "light".to_owned()),
            ]
            .into(),
        ),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();

    assert_eq!(value(&world, &field("narrowest_tile_cols")), "7");
    assert_eq!(value(&world, &field("sidebar_width")), "360");
    assert!(writes(&world).is_empty());
    rest(&mut world);
}

#[test]
fn a_change_made_before_the_first_write_lands_is_saved_after_it() {
    let (daemon, stored) = accepting(&[]);
    let held: Arc<Mutex<Vec<Request>>> = Arc::default();
    let (keep, kept) = (held.clone(), stored.clone());
    daemon.on("set_setting", move |r| {
        let mut held = keep.lock().unwrap();
        if held.is_empty() {
            held.push(r.clone());
            return Vec::new();
        }
        write(&kept, &|_: &str, _: &str| None, r)
    });
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.general");
    let key = field("sidebar_width");

    retype(&mut world, &key, "300");
    press(&mut world, Key::Enter);
    retype(&mut world, &key, "320");
    press(&mut world, Key::Enter);
    assert_eq!(writes(&world).len(), 1);

    let first = held.lock().unwrap()[0].clone();
    let refuse = |_: &str, _: &str| None;
    for reply in write(&stored, &refuse, &first) {
        world.daemon.emit(reply);
    }
    world.settle();

    assert_eq!(writes(&world).last().unwrap().1, "320");
    assert_eq!(stored.lock().unwrap()["sidebar_width"], "320");
    assert_eq!(value(&world, &key), "320");
    rest(&mut world);
}

#[test]
fn an_acknowledgement_leaves_what_was_typed_since_and_does_not_write_it() {
    let (daemon, stored) = accepting(&[]);
    let held: Arc<Mutex<Vec<Request>>> = Arc::default();
    let keep = held.clone();
    daemon.on("set_setting", move |r| keep.lock().unwrap().push(r.clone()));
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.general");
    let key = field("sidebar_width");

    retype(&mut world, &key, "300");
    click(&mut world, "settings.heading");
    retype(&mut world, &key, "320");

    let first = held.lock().unwrap().remove(0);
    let refuse = |_: &str, _: &str| None;
    for reply in write(&stored, &refuse, &first) {
        world.daemon.emit(reply);
    }
    world.settle();

    assert_eq!(value(&world, &key), "320");
    assert_eq!(writes(&world).len(), 1);

    click(&mut world, "settings.heading");
    assert_eq!(writes(&world).last().unwrap().1, "320");
    rest(&mut world);
}

#[test]
fn use_default_clears_the_sidebar_width() {
    let (daemon, stored) = accepting(&[("sidebar_width", "320")]);
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.general");

    click(&mut world, "settings.sidebar-width.default");

    assert_eq!(
        writes(&world),
        [("sidebar_width".to_owned(), String::new())]
    );
    assert!(!stored.lock().unwrap().contains_key("sidebar_width"));
    assert_eq!(value(&world, &field("sidebar_width")), "");
    assert!(
        world
            .screen()
            .find(Target::key("settings.sidebar-width.default"))
            .is_none()
    );
    rest(&mut world);
}

#[test]
fn experimental_lists_each_feature_by_group_with_how_many_are_on_and_its_switch_saves_both_ways() {
    let (daemon, _) = accepting(&[("outposts_enabled", "true")]);
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.experimental");

    let groups: Vec<_> = world
        .screen()
        .descendants(Target::key("settings.section.experimental"))
        .into_iter()
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with("settings.feature-group."))
        })
        .filter_map(|n| n.name)
        .collect();
    assert!(groups.contains(&"Connections, 1 of 2 on".to_owned()));
    assert!(
        world
            .screen()
            .find(Target::key("settings.feature.asks_enabled"))
            .is_none()
    );
    assert!(
        world
            .screen()
            .find(Target::key("settings.feature.phone_access_enabled"))
            .is_some()
    );
    assert_eq!(
        named(&world, "settings.feature-note.phone_access_enabled").as_deref(),
        Some("Turning it off disconnects every phone.")
    );

    for feature in FEATURES.iter().filter(|f| f.stage == "experimental") {
        assert!(
            world
                .screen()
                .find(Target::key(format!("settings.feature.{}", feature.key)))
                .is_some(),
            "{} has a switch",
            feature.name
        );
        let note: Vec<_> = [feature.note, feature.timing]
            .into_iter()
            .filter(|s| !s.is_empty())
            .collect();
        let shown = named(&world, &format!("settings.feature-note.{}", feature.key));
        assert_eq!(
            shown,
            (!note.is_empty()).then(|| note.join(" ")),
            "{} mentions its note and timing only when it has them",
            feature.name
        );
    }

    for setting in ["new_session_launcher_enabled", "analytics.enabled"] {
        let switch = format!("settings.feature.{setting}");
        assert!(!world.screen().get(Target::key(&switch)).is("checked"));
        click(&mut world, &switch);
        assert_eq!(
            writes(&world).last().unwrap(),
            &(setting.to_owned(), "true".to_owned())
        );
        assert!(world.screen().get(Target::key(&switch)).is("checked"));
        assert_eq!(named(&world, "settings.autosave").as_deref(), Some("Saved"));
        click(&mut world, &switch);
        assert_eq!(
            writes(&world).last().unwrap(),
            &(setting.to_owned(), "false".to_owned())
        );
    }
    rest(&mut world);
}

#[test]
fn a_feature_page_carries_its_switch_and_is_dimmed_while_the_feature_is_off() {
    let (daemon, _) = accepting(&[]);
    let mut world = World::start(daemon);
    open(&mut world);
    assert!(
        world
            .screen()
            .find(Target::key("settings.nav-off.outposts"))
            .is_some()
    );

    click(&mut world, "settings.nav.outposts");
    let switch = world
        .screen()
        .get(Target::key("settings.feature.outposts_enabled"));
    assert!(!switch.is("checked"));

    click(&mut world, "settings.feature.outposts_enabled");
    assert_eq!(
        writes(&world),
        [("outposts_enabled".to_owned(), "true".to_owned())]
    );
    assert!(
        world
            .screen()
            .find(Target::key("settings.nav-off.outposts"))
            .is_none()
    );

    click(&mut world, "settings.nav.phones");
    assert_eq!(
        named(&world, "settings.feature-note.phone_access_enabled").as_deref(),
        Some("Turning it off disconnects every phone.")
    );
    rest(&mut world);
}

#[test]
fn the_garden_size_holds_at_its_bounds() {
    for (stored, control, shown) in [
        ("1.5", "settings.scale.garden.up", "150%"),
        ("0.7", "settings.scale.garden.down", "70%"),
    ] {
        let (daemon, _) = accepting(&[("gardenScale", stored)]);
        let mut world = World::start(daemon);
        open(&mut world);
        click(&mut world, "settings.nav.general");

        click(&mut world, control);

        assert!(writes(&world).iter().all(|(_, v)| v == stored));
        assert_eq!(
            named(&world, "settings.scale.garden.value").as_deref(),
            Some(shown)
        );
        rest(&mut world);
    }
}
