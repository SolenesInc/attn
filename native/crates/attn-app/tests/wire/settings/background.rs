use attn_protocol::HarnessModel;
use attn_testkit::{Daemon, Target, World, message};

use super::harness_catalogs::{
    catalog, enter_model, harness, harnesses, model, open_route, options, pick_effort, pick_model,
    picker_open, preview_harness, reported, route_value, serve,
};
use super::{accepting, click, field, keeping, open, press, rest, retype, state, value, writes};

const ACTIVITY: &str = "settings.route.activity";

fn on_background(settings: &[(&str, &str)]) -> World {
    to_background(World::start(serving(settings)))
}

fn serving(settings: &[(&str, &str)]) -> Daemon {
    let (daemon, stored) = accepting(settings);
    serve(&daemon, &stored, harnesses());
    daemon
}

fn to_background(mut world: World) -> World {
    open(&mut world);
    click(&mut world, "settings.nav.backgroundAgents");
    assert_eq!(state(&mut world)["activeSection"], "backgroundAgents");
    world.settle();
    world
}

fn toggle_disabled(world: &World) -> bool {
    world
        .screen()
        .get(Target::key("settings.activity.toggle"))
        .is("disabled")
}

#[test]
fn activity_stays_off_while_browsing_and_enables_once_a_light_default_is_saved() {
    let mut world = on_background(&[]);
    assert!(toggle_disabled(&world));
    open_route(&mut world, "Session activity model");
    preview_harness(&mut world, "Codex");
    assert!(writes(&world).is_empty());
    pick_model(&mut world, "Light default → gpt-6-luna");
    press(&mut world, rune::Key::Escape);
    assert!(!picker_open(&world));
    click(&mut world, "settings.activity.toggle");
    assert_eq!(
        writes(&world),
        [
            (
                "activity.config".to_owned(),
                r#"{"agent":"codex"}"#.to_owned()
            ),
            ("activity.enabled".to_owned(), "true".to_owned()),
        ]
    );
    assert!(!toggle_disabled(&world));
    rest(&mut world);
}

#[test]
fn the_activity_route_saves_as_one_config_and_the_refresh_fields_as_one_pair() {
    let mut world = on_background(&[]);
    open_route(&mut world, "Session activity model");
    preview_harness(&mut world, "Codex");
    pick_model(&mut world, "gpt-6-luna");
    pick_effort(&mut world, "low");
    assert!(!picker_open(&world));
    retype(&mut world, &field("activity.intervals#watching"), "90");
    retype(&mut world, &field("activity.intervals#present"), "600");
    click(&mut world, "settings.heading");
    world.settle();
    assert_eq!(
        writes(&world),
        [
            (
                "activity.config".to_owned(),
                r#"{"agent":"codex","model":"gpt-6-luna"}"#.to_owned()
            ),
            (
                "activity.config".to_owned(),
                r#"{"agent":"codex","model":"gpt-6-luna","effort":"low"}"#.to_owned()
            ),
            (
                "activity.intervals".to_owned(),
                r#"{"watching":90,"present":300}"#.to_owned()
            ),
            (
                "activity.intervals".to_owned(),
                r#"{"watching":90,"present":600}"#.to_owned()
            ),
        ]
    );
    rest(&mut world);
}

#[test]
fn at_any_window_height_the_route_picker_keeps_its_effort_within_reach() {
    for height in (400..=720).step_by(40) {
        let mut world = to_background(World::start_sized(serving(&[]), (960.0, height as f32)));
        open_route(&mut world, "Session activity model");
        preview_harness(&mut world, "Codex");
        pick_model(&mut world, "gpt-6-luna");
        pick_effort(&mut world, "high");
        assert!(!picker_open(&world), "{height} pt");
        assert_eq!(
            writes(&world).last(),
            Some(&(
                "activity.config".to_owned(),
                r#"{"agent":"codex","model":"gpt-6-luna","effort":"high"}"#.to_owned()
            )),
            "{height} pt"
        );
        rest(&mut world);
    }
}

#[test]
fn a_refresh_outside_its_range_is_refused_beside_the_fields_without_a_write() {
    let mut world = on_background(&[]);
    retype(&mut world, &field("activity.intervals#watching"), "10");
    press(&mut world, rune::Key::Enter);
    let alert = world
        .screen()
        .get(Target::key("settings.refusal.activity.intervals"));
    assert_eq!(
        alert.name.as_deref(),
        Some("watching refresh must be a whole number from 30 to 3600 seconds; entered 10.")
    );
    assert!(writes(&world).is_empty());
    retype(&mut world, &field("activity.intervals#watching"), "45");
    press(&mut world, rune::Key::Enter);
    assert_eq!(
        writes(&world),
        [(
            "activity.intervals".to_owned(),
            r#"{"watching":45,"present":300}"#.to_owned()
        )]
    );
    assert!(
        world
            .screen()
            .find(Target::key("settings.refusal.activity.intervals"))
            .is_none()
    );
    rest(&mut world);
}

#[test]
fn claude_activity_recommends_light_over_a_deep_pick_and_default_effort_labels_follow_the_catalog()
{
    let effort_first = |world: &World| {
        world
            .screen()
            .descendants(Target::key("settings.route-picker.effort"))
            .into_iter()
            .find(|n| n.role == "radio-button")
            .and_then(|n| n.name)
    };
    let mut listed = harnesses();
    listed.push(harness("sigma", "Sigma"));
    let (daemon, stored) =
        accepting(&[("activity.config", r#"{"agent":"claude","model":"opus"}"#)]);
    serve(&daemon, &stored, listed);
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.backgroundAgents");
    world.settle();
    assert!(
        world
            .screen()
            .find(Target::key(format!("{ACTIVITY}.recommended")))
            .is_some()
    );
    open_route(&mut world, "Session activity model");
    assert_eq!(
        effort_first(&world).as_deref(),
        Some("default"),
        "Claude activity has no shipped activity effort override"
    );
    assert!(
        world
            .screen()
            .descendants(Target::key("settings.route-picker.models"))
            .iter()
            .any(|n| n.name.as_deref() == Some("Light recommended"))
    );
    preview_harness(&mut world, "Codex");
    assert_eq!(
        effort_first(&world).as_deref(),
        Some("default (low)"),
        "Codex activity labels its low tier default"
    );
    preview_harness(&mut world, "Sigma");
    assert_eq!(
        effort_first(&world).as_deref(),
        Some("default"),
        "Sigma activity has no low-effort override (s-hd06eq)"
    );
    preview_harness(&mut world, "Claude");
    pick_model(&mut world, "Haiku 5.5");
    assert!(
        world
            .screen()
            .find(Target::key(format!("{ACTIVITY}.recommended")))
            .is_none()
    );
    if picker_open(&world) {
        press(&mut world, rune::Key::Escape);
    }
    rest(&mut world);
}

#[test]
fn a_stored_activity_model_missing_from_discovery_stays_and_an_exact_id_saves() {
    let mut world = on_background(&[(
        "activity.config",
        r#"{"agent":"codex","model":"retired-model","effort":"minimal"}"#,
    )]);
    assert!(route_value(&world, ACTIVITY).contains("retired-model"));
    open_route(&mut world, "Session activity model");
    assert!(
        options(&world, "models")
            .iter()
            .any(|(name, selected, _)| name.starts_with("retired-model") && *selected)
    );
    assert!(writes(&world).is_empty());
    enter_model(&mut world, "custom-model");
    assert_eq!(
        writes(&world).last().unwrap(),
        &(
            "activity.config".to_owned(),
            r#"{"agent":"codex","model":"custom-model"}"#.to_owned()
        )
    );
    rest(&mut world);
}

#[test]
fn activity_keeps_model_unpinned_on_effort_change_and_clears_pins_to_defaults() {
    for agent in ["codex", "claude"] {
        let stored = format!(r#"{{"agent":"{agent}"}}"#);
        let mut world = on_background(&[("activity.config", &stored)]);
        assert!(writes(&world).is_empty());
        open_route(&mut world, "Session activity model");
        pick_effort(&mut world, "high");
        assert_eq!(
            writes(&world).last().unwrap(),
            &(
                "activity.config".to_owned(),
                format!(r#"{{"agent":"{agent}","effort":"high"}}"#)
            )
        );
        rest(&mut world);
    }

    let mut world = on_background(&[(
        "activity.config",
        r#"{"agent":"codex","model":"gpt-6-luna","effort":"high"}"#,
    )]);
    open_route(&mut world, "Session activity model");
    pick_model(&mut world, "Light default → gpt-6-luna");
    assert_eq!(
        writes(&world).last().unwrap().1,
        r#"{"agent":"codex","effort":"high"}"#
    );
    pick_effort(&mut world, "default");
    assert_eq!(writes(&world).last().unwrap().1, r#"{"agent":"codex"}"#);
    rest(&mut world);
}

#[test]
fn an_unavailable_saved_activity_harness_stays_shown_and_cannot_be_picked() {
    let mut listed = harnesses();
    listed[1].installation = "missing".into();
    let (daemon, stored) = accepting(&[(
        "activity.config",
        r#"{"agent":"codex","model":"saved-model"}"#,
    )]);
    serve(&daemon, &stored, listed);
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.backgroundAgents");
    world.settle();
    open_route(&mut world, "Session activity model");
    let codex = options(&world, "harnesses")
        .into_iter()
        .find(|(name, _, _)| name.starts_with("Codex"))
        .unwrap();
    assert!(!codex.2, "Codex is disabled");
    assert!(
        world
            .screen()
            .find(Target::key(
                "settings.route-picker.harness.codex.unavailable"
            ))
            .is_some()
    );
    press(&mut world, rune::Key::Escape);
    assert!(route_value(&world, ACTIVITY).contains("saved-model"));
    assert!(writes(&world).is_empty());
    rest(&mut world);
}

#[test]
fn the_headless_context_cap_shows_its_default_and_saves_only_a_change() {
    let mut world = on_background(&[]);
    let cap = field("headless_context_window_cap");
    assert_eq!(value(&world, &cap), "128000");
    click(&mut world, &cap);
    click(&mut world, "settings.heading");
    assert!(writes(&world).is_empty());
    retype(&mut world, &cap, "64000");
    click(&mut world, "settings.heading");
    world.settle();
    assert_eq!(
        writes(&world),
        [("headless_context_window_cap".to_owned(), "64000".to_owned())]
    );
    rest(&mut world);
}

#[test]
fn background_agents_have_their_own_page_with_no_save_and_no_reviewer_model() {
    let mut world = on_background(&[("reviewer_model", "claude-opus-4-6")]);
    assert!(world.screen().find(Target::key(ACTIVITY)).is_some());
    assert!(
        world
            .screen()
            .find(Target::role("button").name("Save"))
            .is_none()
    );
    for section in ["agents", "backgroundAgents"] {
        click(&mut world, &format!("settings.nav.{section}"));
        assert!(
            world
                .screen()
                .find(Target::key(field("reviewer_model")))
                .is_none(),
            "{section} shows a reviewer model"
        );
    }
    rest(&mut world);
}

#[test]
fn a_refused_activity_pick_shows_why_and_keeps_settings_open_on_it() {
    let (daemon, stored) = keeping(&[], |key, _| {
        (key == "activity.config").then(|| "activity.config is not writable".to_owned())
    });
    serve(&daemon, &stored, harnesses());
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.backgroundAgents");
    world.settle();
    open_route(&mut world, "Session activity model");
    preview_harness(&mut world, "Claude");
    pick_model(&mut world, "Light default → Haiku 5.5");
    if picker_open(&world) {
        press(&mut world, rune::Key::Escape);
    }
    let refusal = world
        .screen()
        .find(Target::key("settings.refusal.activity.config"))
        .expect("the refusal shows");
    assert_eq!(
        refusal.name.as_deref(),
        Some("activity.config is not writable")
    );
    press(&mut world, rune::Key::Escape);
    assert_eq!(state(&mut world)["open"], true);
    assert_eq!(
        world.screen().focused().and_then(|n| n.key).as_deref(),
        Some("settings.retry.activity.config"),
        "a refused close leaves the keyboard on the refusal's Retry"
    );
    rest(&mut world);
}

#[test]
fn a_refused_refresh_window_keeps_both_windows_where_they_are_on_every_frame() {
    let mut world = on_background(&[]);
    for part in ["present", "watching"] {
        let key = field(&format!("activity.intervals#{part}"));
        retype(&mut world, &key, "9999");
        let rec = world.record(60.0, 1.0, |_, app| {
            app.key(rune::KeyEvent {
                key: rune::Key::Enter,
                mods: rune::Mods::default(),
            })
        });
        assert!(
            world
                .screen()
                .find(Target::key("settings.refusal.activity.intervals"))
                .is_some(),
            "{part}: the refusal shows"
        );
        for window in ["watching", "present"] {
            let path = format!("/{}", field(&format!("activity.intervals#{window}")));
            let track = rec
                .tracks
                .iter()
                .find(|t| t.path.ends_with(&path))
                .unwrap_or_else(|| panic!("the {window} window is on screen"));
            let places: Vec<[f32; 4]> = track.frames.iter().map(|(_, s)| s.rect).collect();
            assert!(
                places.windows(2).all(|w| w[0][1] == w[1][1]),
                "refusing {part}: the {window} window moved: {places:?}"
            );
        }
    }
    rest(&mut world);
}

fn sigma_model(provider: &str, id: &str, name: &str, one_shot: &str) -> HarnessModel {
    HarnessModel {
        provider: provider.into(),
        one_shot: one_shot.into(),
        ..model(
            "sigma",
            &format!("{provider}/{id}"),
            name,
            Some("light"),
            &["low", "high"],
        )
    }
}

fn on_sigma_background(settings: &[(&str, &str)]) -> World {
    let (daemon, stored) = accepting(settings);
    let mut listed = harnesses();
    listed.push(harness("sigma", "Sigma"));
    serve(&daemon, &stored, listed);
    daemon.on("harness_models", move |r| {
        let harness = r["harness"].as_str().unwrap_or_default().to_owned();
        let models = if harness == "sigma" {
            vec![
                sigma_model("agy", "gemini-4-neon", "Gemini 4 Neon", "unsupported"),
                sigma_model("openai", "gpt-6-luna", "Luna", "supported"),
                sigma_model("openai-work", "gpt-6-luna", "Work Luna", "supported"),
            ]
        } else {
            catalog(&harness)
        };
        message(reported(&harness, &stored, models))
    });
    to_background(World::start(daemon))
}

#[test]
fn a_sigma_activity_model_saves_and_reads_back_as_its_catalog_id() {
    let mut world = on_sigma_background(&[]);
    open_route(&mut world, "Session activity model");
    preview_harness(&mut world, "Sigma");
    pick_model(&mut world, "Work Luna");
    let saved: serde_json::Value = serde_json::from_str(&writes(&world).last().unwrap().1).unwrap();
    assert_eq!(saved["agent"], "sigma");
    assert_eq!(saved["model"], "openai-work/gpt-6-luna");
    rest(&mut world);

    let mut world = on_sigma_background(&[(
        "activity.config",
        r#"{"agent":"sigma","model":"openai-work/gpt-6-luna"}"#,
    )]);
    open_route(&mut world, "Session activity model");
    let selected: Vec<String> = options(&world, "models")
        .into_iter()
        .filter(|(_, selected, _)| *selected)
        .map(|(name, _, _)| name)
        .collect();
    assert!(
        selected.len() == 1 && selected[0].starts_with("Work Luna"),
        "{selected:?}"
    );
    assert!(writes(&world).is_empty());
    rest(&mut world);
}

#[test]
fn the_background_routes_disable_a_sigma_model_that_cannot_answer_a_one_shot_call() {
    let label = "Session activity model";
    let mut world = on_sigma_background(&[]);
    open_route(&mut world, label);
    preview_harness(&mut world, "Sigma");
    let enabled = |name: &str| {
        options(&world, "models")
            .into_iter()
            .find(|(shown, _, _)| shown.starts_with(name))
            .unwrap_or_else(|| panic!("{label} lists {name}"))
            .2
    };
    assert!(!enabled("Gemini 4 Neon"), "{label} disables agy's model");
    assert!(enabled("Work Luna"), "{label} offers openai-work's model");
    assert!(
        options(&world, "models")
            .iter()
            .any(|(name, _, _)| name.starts_with("Light default → Luna")),
        "{label} defaults to the first light model that answers: {:?}",
        options(&world, "models")
    );
    rest(&mut world);
}
