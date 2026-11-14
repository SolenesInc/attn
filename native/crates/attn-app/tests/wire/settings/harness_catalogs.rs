//! A daemon that reports harnesses and their model catalogs, and the model picker as a person
//! drives it.

use attn_protocol::{
    DelegationPreferences, DelegationPreferencesResultMessage, Harness, HarnessModel,
    HarnessModelsResultMessage, TierDefaults,
};
use attn_testkit::{Daemon, Target, World, message};
use serde_json::{Value, json};

use super::{Settings, click};

pub(super) fn harness(id: &str, name: &str) -> Harness {
    Harness {
        id: id.into(),
        name: name.into(),
        available: true,
        model_pin: true,
        effort_pin: true,
        discovery: true,
    }
}

pub(super) fn harnesses() -> Vec<Harness> {
    vec![
        harness("claude", "Claude"),
        harness("codex", "Codex"),
        harness("plug", "Plug"),
    ]
}

const EFFORTS: &[&str] = &["low", "medium", "high", "xhigh", "max"];

pub(super) fn model(
    harness: &str,
    id: &str,
    name: &str,
    tier: Option<&str>,
    efforts: &[&str],
) -> HarnessModel {
    HarnessModel {
        harness: harness.into(),
        provider: String::new(),
        id: id.into(),
        name: name.into(),
        access: "unknown".into(),
        effort_support: if efforts.is_empty() {
            "unsupported"
        } else {
            "supported"
        }
        .into(),
        effort_levels: efforts.iter().map(|e| (*e).to_owned()).collect(),
        tier: tier.map(str::to_owned),
        shipped_tier: tier.map(str::to_owned),
        tier_source: if id == "default" {
            "alias"
        } else if tier.is_some() {
            "shipped"
        } else {
            "none"
        }
        .into(),
        ..HarnessModel::default()
    }
}

pub(super) fn catalog(harness: &str) -> Vec<HarnessModel> {
    match harness {
        "claude" => vec![
            model("claude", "default", "Default (recommended)", None, EFFORTS),
            model("claude", "opus", "Opus 5.5", Some("deep"), EFFORTS),
            model("claude", "sonnet", "Sonnet 5.5", Some("standard"), EFFORTS),
            model("claude", "haiku", "Haiku 5.5", Some("light"), EFFORTS),
        ],
        "codex" => vec![
            model(
                "codex",
                "gpt-6.1-sol",
                "gpt-6.1-sol",
                Some("deep"),
                &["low", "medium", "high", "xhigh", "max", "ultra"],
            ),
            model("codex", "gpt-6-astra", "gpt-6-astra", Some("deep"), EFFORTS),
            model("codex", "gpt-6-luna", "gpt-6-luna", Some("light"), EFFORTS),
            model(
                "codex",
                "gpt-5.6-terra",
                "gpt-5.6-terra",
                Some("standard"),
                EFFORTS,
            ),
            model(
                "codex",
                "gpt-5.6-luna",
                "gpt-5.6-luna",
                Some("light"),
                EFFORTS,
            ),
            model("codex", "new-model", "New model", None, EFFORTS),
        ],
        _ => Vec::new(),
    }
}

/// The models a harness reports, with the tiers `model_tier_overrides` gives them, and the
/// first supported model of each tier as its default.
pub(super) fn reported(
    harness: &str,
    settings: &Settings,
    models: Vec<HarnessModel>,
) -> HarnessModelsResultMessage {
    let overrides: Vec<Value> = settings
        .lock()
        .unwrap()
        .get("model_tier_overrides")
        .and_then(|raw| serde_json::from_str(raw).ok())
        .unwrap_or_default();
    let models: Vec<HarnessModel> = models
        .into_iter()
        .map(|mut m| {
            let own = overrides.iter().find(|o| {
                o["harness"] == harness
                    && o["provider"] == m.provider.as_str()
                    && o["model"] == m.id.as_str()
            });
            if let Some(own) = own
                && m.tier_source != "alias"
            {
                m.tier = own["tier"].as_str().map(str::to_owned);
                m.tier_source = "override".into();
            }
            m
        })
        .collect();
    let first = |tier: &str| {
        models
            .iter()
            .find(|m| m.tier.as_deref() == Some(tier) && m.access != "unsupported")
            .map(|m| m.id.clone())
    };
    HarnessModelsResultMessage {
        success: true,
        models: models.clone(),
        tier_defaults: TierDefaults {
            light: first("light"),
            standard: first("standard"),
            deep: first("deep"),
        },
        detail: "Reported by the test harness".into(),
        ..HarnessModelsResultMessage::new()
    }
}

pub(super) fn preferences_result(harnesses: Vec<Harness>) -> DelegationPreferencesResultMessage {
    DelegationPreferencesResultMessage {
        success: true,
        preferences: Some(DelegationPreferences::default()),
        harnesses: Some(harnesses),
        templates: Some(Vec::new()),
        expanded_roles: Some(Vec::new()),
        ..DelegationPreferencesResultMessage::new()
    }
}

/// Answers the harness list with `harnesses` and each catalog read from [`catalog`].
pub(super) fn serve(daemon: &Daemon, settings: &Settings, harnesses: Vec<Harness>) {
    daemon.on("delegation_preferences_get", move |_| {
        message(preferences_result(harnesses.clone()))
    });
    let settings = settings.clone();
    daemon.on("harness_models", move |r| {
        let harness = r["harness"].as_str().unwrap_or_default().to_owned();
        message(reported(&harness, &settings, catalog(&harness)))
    });
}

pub(super) const PICKER: &str = "settings.route-picker";

/// Opens the picker from the route button named `label`.
pub(super) fn open_route(world: &mut World, label: &str) {
    world
        .automate(
            "press",
            json!({"target": {"role": "button", "name": label}}),
        )
        .unwrap_or_else(|why| panic!("press {label}: {why}"));
    assert!(picker_open(world), "the picker opened from {label}");
}

pub(super) fn picker_open(world: &World) -> bool {
    world.screen().find(Target::key(PICKER)).is_some()
}

/// The options of the picker's list keyed `list`, by name, with whether each is selected and
/// enabled.
pub(super) fn options(world: &World, list: &str) -> Vec<(String, bool, bool)> {
    world
        .screen()
        .descendants(Target::key(format!("settings.route-picker.{list}")))
        .into_iter()
        .filter(|n| n.role == "list-box-option")
        .map(|n| {
            (
                n.name.clone().unwrap_or_default(),
                n.is("selected"),
                !n.is("disabled"),
            )
        })
        .collect()
}

fn option_key(world: &World, list: &str, prefix: &str) -> String {
    world
        .screen()
        .descendants(Target::key(format!("settings.route-picker.{list}")))
        .into_iter()
        .find(|n| {
            n.role == "list-box-option"
                && n.name
                    .as_deref()
                    .is_some_and(|name| name.starts_with(prefix))
        })
        .and_then(|n| n.key)
        .unwrap_or_else(|| {
            panic!(
                "no {list} option starts with {prefix}: {:?}",
                options(world, list)
            )
        })
}

/// Browses the harness whose name starts with `name`.
pub(super) fn preview_harness(world: &mut World, name: &str) {
    let key = option_key(world, "harnesses", name);
    click(world, &key);
}

/// Picks the model whose name starts with `name`.
pub(super) fn pick_model(world: &mut World, name: &str) {
    let key = option_key(world, "models", name);
    click(world, &key);
}

/// Picks the effort level named `name`.
pub(super) fn pick_effort(world: &mut World, name: &str) {
    let key = world
        .screen()
        .descendants(Target::key("settings.route-picker.effort"))
        .into_iter()
        .find(|n| n.role == "button" && n.name.as_deref() == Some(name))
        .and_then(|n| n.key)
        .unwrap_or_else(|| panic!("no effort {name}"));
    click(world, &key);
}

/// Types `id` into the filter and uses it as the model, with `provider` when one is given.
pub(super) fn enter_model(world: &mut World, id: &str, provider: Option<&str>) {
    click(world, "settings.route-picker.filter");
    world.gesture(|app| app.type_text(id));
    if let Some(provider) = provider {
        click(world, "settings.route-picker.provider");
        world.gesture(|app| app.type_text(provider));
    }
    click(world, "settings.route-picker.manual");
}

/// What the route button named by `key` shows: harness, model and effort.
pub(super) fn route_value(world: &World, key: &str) -> String {
    world
        .screen()
        .get(Target::key(key))
        .value
        .unwrap_or_default()
}
