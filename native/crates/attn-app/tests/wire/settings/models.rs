use std::collections::BTreeMap;

use attn_protocol::{
    Harness, HarnessModelsResultMessage, PluginInfo, PluginsUpdatedMessage, SettingsUpdatedMessage,
};
use attn_testkit::{Target, World, message};
use rune::Key;

use super::harness_catalogs::{harness, harnesses, model, reported, serve};
use super::{Settings, accepting, click, named, open, press, rest, state, writes};

const OVERRIDES: &str = "model_tier_overrides";

fn on_models(settings: &[(&str, &str)], listed: Vec<Harness>) -> (World, Settings) {
    let (daemon, stored) = accepting(settings);
    serve(&daemon, &stored, listed);
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.models");
    assert_eq!(state(&mut world)["activeSection"], "models");
    world.settle();
    (world, stored)
}

fn region(world: &World, name: &str) -> bool {
    world
        .screen()
        .find(Target::role("region").name(format!("{name} models")))
        .is_some()
}

fn row(harness: &str, model: &str) -> String {
    format!("settings.models.{harness}.model.{model}")
}

fn orphan(harness: &str, model: &str) -> String {
    format!("settings.models.{harness}.orphan.{model}")
}

fn radio(harness: &str, model: &str, tier: &str) -> String {
    format!("{}.tier.{tier}", row(harness, model))
}

fn said(world: &World, key: &str) -> String {
    named(world, key).unwrap_or_else(|| panic!("nothing shows at {key}"))
}

fn light_default(world: &World) -> String {
    said(world, "settings.models.codex.light-default")
}

fn last_write(world: &World) -> (String, String) {
    writes(world).pop().expect("a setting was written")
}

fn overrides_written(json: &str) -> (String, String) {
    (OVERRIDES.to_owned(), json.to_owned())
}

fn change_settings(world: &mut World, settings: &[(&str, &str)]) {
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

fn discovered(world: &World) -> Vec<String> {
    world
        .daemon
        .sent_of("harness_models")
        .iter()
        .map(|r| r["harness"].as_str().unwrap_or_default().to_owned())
        .collect()
}

fn preference_reads(world: &World) -> usize {
    world.daemon.sent_of("delegation_preferences_get").len()
}

#[test]
fn shows_reported_order_flags_untiered_models_and_names_live_light_and_deep_defaults() {
    let (mut world, _) = on_models(&[], harnesses());
    let groups: Vec<String> = world
        .screen()
        .descendants(Target::key("settings.models.codex"))
        .into_iter()
        .filter(|n| n.role == "radio-group")
        .filter_map(|n| n.name)
        .collect();
    assert_eq!(
        groups,
        [
            "Tier for gpt-6.1-sol",
            "Tier for gpt-6-astra",
            "Tier for gpt-6-luna",
            "Tier for gpt-5.6-terra",
            "Tier for gpt-5.6-luna",
            "Tier for New model",
        ]
    );
    assert_eq!(light_default(&world), "Light default → gpt-6-luna");
    assert_eq!(
        said(&world, "settings.models.codex.deep-default"),
        "Deep default → gpt-6.1-sol"
    );
    assert_eq!(
        said(&world, &format!("{}.tier", row("codex", "new-model"))),
        "no tier"
    );
    assert_eq!(
        named(&world, &format!("{}.tier", row("codex", "gpt-6-luna"))),
        None
    );
    assert_eq!(preference_reads(&world), 1);
    assert_eq!(discovered(&world), ["claude", "codex", "plug"]);
    rest(&mut world);
}

#[test]
fn a_tier_updates_the_mapping_and_the_live_default_and_reset_returns_to_the_shipped_tier() {
    let (mut world, _) = on_models(&[], harnesses());
    click(&mut world, &radio("codex", "gpt-6-luna", "standard"));
    world.settle();
    assert_eq!(
        last_write(&world),
        overrides_written(
            r#"[{"harness":"codex","provider":"","model":"gpt-6-luna","tier":"standard"}]"#
        )
    );
    assert_eq!(light_default(&world), "Light default → gpt-5.6-luna");
    assert_eq!(
        said(&world, &format!("{}.detail", row("codex", "gpt-6-luna"))),
        "Your tier · attn: light"
    );
    let reset = format!("{}.reset", row("codex", "gpt-6-luna"));
    assert_eq!(
        said(&world, &reset),
        "Reset tier for gpt-6-luna",
        "Reset is announced with its model"
    );
    click(&mut world, &reset);
    world.settle();
    assert_eq!(last_write(&world), overrides_written("[]"));
    assert_eq!(light_default(&world), "Light default → gpt-6-luna");
    rest(&mut world);
}

#[test]
fn a_missing_models_override_stays_visible_and_remove_drops_only_it() {
    let overrides = r#"[{"harness":"codex","provider":"","model":"retired-model","tier":"deep"},{"harness":"codex","provider":"","model":"gpt-6-luna","tier":"standard"}]"#;
    let (mut world, _) = on_models(&[(OVERRIDES, overrides)], harnesses());
    assert_eq!(
        said(&world, "settings.models.codex.orphans"),
        "No longer reported"
    );
    let retired = orphan("codex", "retired-model");
    assert_eq!(said(&world, &format!("{retired}.name")), "retired-model");
    assert_eq!(
        said(&world, &format!("{retired}.detail")),
        "Your tier: deep"
    );
    click(&mut world, &format!("{retired}.remove"));
    world.settle();
    assert_eq!(
        last_write(&world),
        overrides_written(
            r#"[{"harness":"codex","provider":"","model":"gpt-6-luna","tier":"standard"}]"#
        )
    );
    rest(&mut world);
}

/// Moves the focus with Tab until it reaches `key`, as a keyboard user gets there.
fn tab_to(world: &mut World, key: &str) {
    let mut seen = Vec::new();
    loop {
        let focused = world.screen().focused().and_then(|n| n.key);
        if focused.as_deref() == Some(key) {
            return;
        }
        assert!(
            !seen.contains(&focused),
            "Tab never reaches {key}; it went {seen:?}"
        );
        seen.push(focused);
        press(world, Key::Tab);
    }
}

#[test]
fn delete_resets_a_model_without_changing_its_neighbors() {
    let (mut world, _) = on_models(
        &[(
            OVERRIDES,
            r#"[{"harness":"codex","provider":"","model":"gpt-6-luna","tier":"deep"}]"#,
        )],
        harnesses(),
    );
    let deep = radio("codex", "gpt-6-luna", "deep");
    tab_to(&mut world, &deep);
    press(&mut world, Key::Delete);
    world.settle();
    assert_eq!(writes(&world), [overrides_written("[]")]);
    assert!(
        world
            .screen()
            .get(Target::key(radio("codex", "gpt-6-luna", "light")))
            .is("checked")
    );
    rest(&mut world);
}

#[test]
fn an_override_for_a_known_harness_without_discovery_can_be_removed() {
    let listed = vec![
        harness("claude", "Claude"),
        harness("codex", "Codex"),
        Harness {
            discovery: false,
            ..harness("plug", "Plug")
        },
    ];
    let (mut world, _) = on_models(
        &[(
            OVERRIDES,
            r#"[{"harness":"plug","provider":"","model":"old-model","tier":"deep"}]"#,
        )],
        listed,
    );
    assert_eq!(
        said(&world, "settings.models.plug.no-catalog"),
        "This harness does not report a model catalog."
    );
    assert_eq!(discovered(&world), ["claude", "codex"]);
    click(
        &mut world,
        &format!("{}.remove", orphan("plug", "old-model")),
    );
    world.settle();
    assert_eq!(last_write(&world), overrides_written("[]"));
    rest(&mut world);
}

#[test]
fn saved_overrides_stay_removable_when_model_discovery_fails() {
    let overrides = r#"[{"harness":"codex","provider":"","model":"gpt-6-luna","tier":"deep"}]"#;
    let (mut world, _) = on_models(&[(OVERRIDES, overrides)], harnesses());
    world.daemon.on("harness_models", |_| {
        message(HarnessModelsResultMessage {
            success: false,
            detail: "The configured executable cannot report models.".into(),
            ..HarnessModelsResultMessage::new()
        })
    });
    change_settings(
        &mut world,
        &[
            (OVERRIDES, overrides),
            ("codex_executable", "/missing/codex"),
            ("codex_available", "false"),
        ],
    );
    assert_eq!(
        said(&world, "settings.models.codex.orphans"),
        "Saved overrides"
    );
    click(
        &mut world,
        &format!("{}.remove", orphan("codex", "gpt-6-luna")),
    );
    world.settle();
    assert_eq!(last_write(&world), overrides_written("[]"));
    rest(&mut world);
}

#[test]
fn a_new_executable_rediscovers_the_harness_models() {
    let (mut world, stored) = on_models(&[], harnesses());
    let codex = |world: &World| discovered(world).iter().filter(|h| *h == "codex").count();
    let before = codex(&world);
    world.daemon.on("harness_models", move |r| {
        let harness = r["harness"].as_str().unwrap_or_default().to_owned();
        message(reported(
            &harness,
            &stored,
            vec![model(
                &harness,
                "replacement-luna",
                "Replacement Luna",
                Some("light"),
                &[],
            )],
        ))
    });
    change_settings(&mut world, &[("codex_executable", "/another/codex")]);
    assert_eq!(codex(&world), before + 1);
    assert_eq!(light_default(&world), "Light default → Replacement Luna");
    assert!(
        world
            .screen()
            .find(Target::key(row("codex", "gpt-6-luna")))
            .is_none()
    );
    rest(&mut world);
}

#[test]
fn a_driver_registering_on_a_connected_plugin_joins_the_harness_list() {
    let (mut world, stored) = on_models(&[], harnesses());
    let reads = preference_reads(&world);
    let mut listed = harnesses();
    listed.push(Harness {
        discovery: false,
        ..harness("new-driver", "New driver")
    });
    serve(&world.daemon, &stored, listed);
    change_settings(
        &mut world,
        &[
            ("new-driver_available", "true"),
            ("new-driver_cap_model_pin", "true"),
        ],
    );
    assert!(region(&world, "New driver"));
    assert_eq!(preference_reads(&world), reads + 1);
    change_settings(
        &mut world,
        &[
            ("new-driver_available", "true"),
            ("new-driver_cap_model_pin", "true"),
            ("new-driver_display_name", "Renamed"),
        ],
    );
    assert_eq!(
        preference_reads(&world),
        reads + 1,
        "a setting outside the registry reads nothing"
    );
    rest(&mut world);
}

#[test]
fn a_driver_losing_initial_prompt_support_leaves_the_harness_list() {
    let (mut world, stored) = on_models(
        &[
            ("plug_available", "true"),
            ("plug_cap_initial_prompt", "true"),
        ],
        harnesses(),
    );
    assert!(region(&world, "Plug"));
    let listed: Vec<Harness> = harnesses().into_iter().filter(|h| h.id != "plug").collect();
    serve(&world.daemon, &stored, listed);
    change_settings(&mut world, &[("plug_cap_initial_prompt", "false")]);
    assert!(!region(&world, "Plug"));
    rest(&mut world);
}

fn plugins_now(world: &mut World, plugins: Vec<PluginInfo>) {
    world.daemon.emit(PluginsUpdatedMessage {
        plugins,
        issues: Some(vec![]),
        ..PluginsUpdatedMessage::new()
    });
    world.settle();
}

#[test]
fn a_plugin_change_reads_the_harnesses_again_and_a_health_timestamp_does_not() {
    let (mut world, stored) = on_models(&[], harnesses());
    let plugin = PluginInfo {
        name: "demo".into(),
        dir: "/tmp/demo".into(),
        version: "1".into(),
        connected: true,
        running: true,
        runtime_state: "running".into(),
        can_uninstall: true,
        ..PluginInfo::default()
    };
    let mut listed = harnesses();
    listed.push(Harness {
        discovery: false,
        ..harness("demo", "Demo")
    });
    serve(&world.daemon, &stored, listed);
    plugins_now(&mut world, vec![plugin.clone()]);
    assert!(region(&world, "Demo"));
    let reads = preference_reads(&world);
    plugins_now(
        &mut world,
        vec![PluginInfo {
            last_health_at: Some("2026-10-09T02:00:00Z".into()),
            ..plugin
        }],
    );
    assert_eq!(preference_reads(&world), reads);
    serve(&world.daemon, &stored, harnesses());
    plugins_now(&mut world, Vec::new());
    assert!(!region(&world, "Demo"));
    assert_eq!(preference_reads(&world), reads + 1);
    rest(&mut world);
}
