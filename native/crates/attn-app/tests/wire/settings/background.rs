use attn_protocol::DelegationPreferencesResultMessage;
use attn_testkit::{Target, World, message};

use super::harness_catalogs::{
    enter_model, harnesses, open_route, options, pick_effort, pick_model, picker_open,
    preview_harness, route_value, serve,
};
use super::{accepting, click, field, keeping, open, press, rest, retype, state, value, writes};

const HEADLESS: &[(&str, &str)] = &[
    ("codex_cap_headless_task", "true"),
    ("claude_cap_headless_task", "true"),
];

const ACTIVITY: &str = "settings.route.activity";
const ADVISOR: &str = "settings.route.garden-advisor";

fn on_background(settings: &[(&str, &str)]) -> World {
    let all: Vec<(&str, &str)> = HEADLESS.iter().chain(settings).copied().collect();
    let (daemon, stored) = accepting(&all);
    serve(&daemon, &stored, harnesses());
    let mut world = World::start(daemon);
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
fn claude_activity_hides_effort_and_recommends_light_over_a_deep_pick() {
    let mut world = on_background(&[("activity.config", r#"{"agent":"claude","model":"opus"}"#)]);
    assert!(
        world
            .screen()
            .find(Target::key(format!("{ACTIVITY}.recommended")))
            .is_some()
    );
    open_route(&mut world, "Session activity model");
    assert!(
        world
            .screen()
            .find(Target::key("settings.route-picker.effort"))
            .is_none()
    );
    assert!(
        world
            .screen()
            .descendants(Target::key("settings.route-picker.models"))
            .iter()
            .any(|n| n.name.as_deref() == Some("Light recommended"))
    );
    pick_model(&mut world, "Haiku 5.5");
    assert!(
        world
            .screen()
            .find(Target::key(format!("{ACTIVITY}.recommended")))
            .is_none()
    );
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
    enter_model(&mut world, "custom-model", None);
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
fn the_advisor_shows_its_light_default_at_codex_s_default_effort_without_writing() {
    let mut world = on_background(&[]);
    let shown = route_value(&world, ADVISOR);
    assert!(shown.contains("Light default → gpt-6-luna"), "{shown}");
    assert!(shown.contains("xhigh"), "{shown}");
    assert!(writes(&world).is_empty());
    rest(&mut world);
}

#[test]
fn the_advisor_browses_claude_without_saving_then_saves_its_light_default() {
    let mut world = on_background(&[]);
    open_route(&mut world, "Garden advisor model");
    preview_harness(&mut world, "Claude");
    assert!(writes(&world).is_empty());
    pick_model(&mut world, "Light default → Haiku 5.5");
    pick_effort(&mut world, "default (medium)");
    assert_eq!(
        writes(&world).last().unwrap(),
        &(
            "garden.advisor".to_owned(),
            r#"{"agent":"claude","model":""}"#.to_owned()
        )
    );
    rest(&mut world);
}

#[test]
fn the_advisor_keeps_its_model_unpinned_when_only_effort_changes() {
    for agent in ["codex", "claude"] {
        let stored = format!(r#"{{"agent":"{agent}","model":"","effort":""}}"#);
        let mut world = on_background(&[("garden.advisor", &stored)]);
        assert!(writes(&world).is_empty());
        open_route(&mut world, "Garden advisor model");
        pick_effort(&mut world, "high");
        assert_eq!(
            writes(&world).last().unwrap(),
            &(
                "garden.advisor".to_owned(),
                format!(r#"{{"agent":"{agent}","model":"","effort":"high"}}"#)
            )
        );
        rest(&mut world);
    }
}

#[test]
fn the_advisor_clears_its_pins_back_to_the_discovered_defaults() {
    let mut world = on_background(&[(
        "garden.advisor",
        r#"{"agent":"codex","model":"gpt-6-luna","effort":"high"}"#,
    )]);
    open_route(&mut world, "Garden advisor model");
    pick_model(&mut world, "Light default → gpt-6-luna");
    assert_eq!(
        writes(&world).last().unwrap().1,
        r#"{"agent":"codex","model":"","effort":"high"}"#
    );
    pick_effort(&mut world, "default");
    assert_eq!(
        writes(&world).last().unwrap().1,
        r#"{"agent":"codex","model":""}"#
    );
    rest(&mut world);
}

#[test]
fn the_advisor_saves_an_explicit_model_and_effort_and_shows_the_model_s_tier() {
    let mut world = on_background(&[]);
    open_route(&mut world, "Garden advisor model");
    pick_model(&mut world, "gpt-6-luna");
    pick_effort(&mut world, "max");
    let shown = route_value(&world, ADVISOR);
    assert!(shown.contains("light") && shown.contains("max"), "{shown}");
    assert_eq!(
        writes(&world).last().unwrap(),
        &(
            "garden.advisor".to_owned(),
            r#"{"agent":"codex","model":"gpt-6-luna","effort":"max"}"#.to_owned()
        )
    );
    rest(&mut world);
}

#[test]
fn a_failed_harness_list_keeps_each_route_editable_and_retry_reads_it_again() {
    for (setting, block, label) in [
        ("activity.config", "activity", "Session activity model"),
        ("garden.advisor", "garden-advisor", "Garden advisor model"),
    ] {
        let all: Vec<(&str, &str)> = HEADLESS
            .iter()
            .copied()
            .chain([(
                setting,
                r#"{"agent":"codex","model":"saved-model","effort":"low"}"#,
            )])
            .collect();
        let (daemon, stored) = accepting(&all);
        serve(&daemon, &stored, harnesses());
        daemon.on("delegation_preferences_get", |_| {
            message(DelegationPreferencesResultMessage {
                success: false,
                error: Some("Harness list unavailable".into()),
                ..DelegationPreferencesResultMessage::new()
            })
        });
        let mut world = World::start(daemon);
        open(&mut world);
        click(&mut world, "settings.nav.backgroundAgents");
        world.settle();
        let alert = world
            .screen()
            .get(Target::key(format!("settings.{block}.harness-error")));
        assert_eq!(alert.name.as_deref(), Some("Harness list unavailable"));
        open_route(&mut world, label);
        enter_model(&mut world, "manual-model", None);
        let saved: serde_json::Value =
            serde_json::from_str(&writes(&world).last().unwrap().1).unwrap();
        assert_eq!(saved["model"], "manual-model");
        preview_harness(&mut world, "Claude");
        pick_model(&mut world, "Light default");
        if picker_open(&world) {
            press(&mut world, rune::Key::Escape);
        }
        let saved: serde_json::Value =
            serde_json::from_str(&writes(&world).last().unwrap().1).unwrap();
        assert_eq!(saved["agent"], "claude");

        serve(&world.daemon, &stored, harnesses());
        click(&mut world, &format!("settings.{block}.harness-retry"));
        assert!(
            world
                .screen()
                .find(Target::key(format!("settings.{block}.harness-error")))
                .is_none()
        );
        assert!(
            route_value(
                &world,
                &format!(
                    "settings.route.{}",
                    if block == "activity" {
                        "activity"
                    } else {
                        "garden-advisor"
                    }
                )
            )
            .starts_with("Claude")
        );
        assert_eq!(world.daemon.sent_of("delegation_preferences_get").len(), 2);
        rest(&mut world);
    }
}

#[test]
fn an_unavailable_saved_advisor_stays_shown_and_cannot_be_picked() {
    let mut world = on_background(&[
        ("codex_available", "false"),
        (
            "garden.advisor",
            r#"{"agent":"codex","model":"saved-model"}"#,
        ),
    ]);
    open_route(&mut world, "Garden advisor model");
    let codex = options(&world, "harnesses")
        .into_iter()
        .find(|(name, _, _)| name.starts_with("Codex"))
        .unwrap();
    assert!(!codex.2, "Codex is disabled");
    press(&mut world, rune::Key::Escape);
    assert!(route_value(&world, ADVISOR).contains("saved-model"));
    assert!(
        world
            .screen()
            .find(Target::key("settings.garden-advisor.unavailable"))
            .is_some()
    );
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
    assert!(world.screen().find(Target::key(ADVISOR)).is_some());
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
fn a_refused_advisor_pick_shows_why_and_keeps_settings_open_on_it() {
    let all: Vec<(&str, &str)> = HEADLESS.to_vec();
    let (daemon, stored) = keeping(&all, |key, _| {
        (key == "garden.advisor").then(|| "garden.advisor is not writable".to_owned())
    });
    serve(&daemon, &stored, harnesses());
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.backgroundAgents");
    world.settle();
    open_route(&mut world, "Garden advisor model");
    preview_harness(&mut world, "Claude");
    pick_model(&mut world, "Light default → Haiku 5.5");
    if picker_open(&world) {
        press(&mut world, rune::Key::Escape);
    }
    let refusal = world
        .screen()
        .find(Target::key("settings.refusal.garden.advisor"))
        .expect("the refusal shows");
    assert_eq!(
        refusal.name.as_deref(),
        Some("garden.advisor is not writable")
    );
    press(&mut world, rune::Key::Escape);
    assert_eq!(state(&mut world)["open"], true);
    assert_eq!(
        world.screen().focused().and_then(|n| n.key).as_deref(),
        Some("settings.retry.garden.advisor"),
        "a refused close leaves the keyboard on the refusal's Retry"
    );
    rest(&mut world);
}
