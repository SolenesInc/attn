//! The harnesses the daemon offers, read with the delegation preferences, as the Agents, Models,
//! Background agents and Delegation pages show them.

use attn_protocol::{
    DelegationPreferences, DelegationPreferencesGetMessage, DelegationPreferencesResultMessage,
    DelegationRole, Harness,
};
use std::collections::BTreeMap;

use attn_ui::Tokens;
use rune::Tx;

use super::route::RouteHarness;
use crate::State;

/// What the daemon last answered a read of the delegation preferences with.
#[derive(Clone, Debug)]
pub(crate) struct Read {
    pub(crate) preferences: DelegationPreferences,
    pub(crate) templates: Vec<DelegationRole>,
    pub(crate) expanded_roles: Vec<DelegationRole>,
    pub(crate) harnesses: Vec<Harness>,
}

#[derive(Default)]
pub(crate) struct Harnesses {
    pub(crate) read: Option<Read>,
    pub(crate) error: Option<String>,
    pub(crate) loading: bool,
    /// Rises with each read, so only the newest read's answer lands.
    epoch: u64,
    /// The `Facts::delegation_changed` the last read followed.
    seen: Option<u64>,
    /// The [`registry`] the last read followed.
    registry: Vec<String>,
}

/// What says which harnesses this machine has and what each can do: the settings that
/// announce a harness and its capabilities, and the plugins that bring harnesses, without their
/// health timestamps. A change to it changes the list the daemon offers.
fn registry(state: &State) -> Vec<String> {
    const CAPABILITIES: &[&str] = &[
        "_cap_initial_prompt",
        "_cap_model_pin",
        "_cap_effort_pin",
        "_cap_model_discovery",
    ];
    let settings = state.facts.settings.iter().filter_map(|(key, value)| {
        if key.ends_with("_available") {
            Some(key.clone())
        } else if CAPABILITIES.iter().any(|c| key.ends_with(c)) {
            Some(format!("{key}={value}"))
        } else {
            None
        }
    });
    let plugins = state.facts.plugins.plugins.iter().map(|p| {
        format!(
            "plugin {:?}",
            (
                &p.name,
                &p.dir,
                &p.link_target,
                &p.version,
                p.connected,
                &p.runtime_state,
                &p.health_status,
            )
        )
    });
    settings.chain(plugins).collect()
}

/// Reads the harnesses each time the dialog opens, and again while it is open each time the
/// daemon says the delegation preferences changed or the harness registry changes; a registry
/// change discovers every catalog again.
pub(super) fn ensure(state: &mut State, tx: &mut Tx) -> bool {
    let harnesses = &state.window.settings.harnesses;
    if !(super::is_open(state) || crate::crew::is_open(state) || crate::chief::setup_shows(state))
        || harnesses.loading
        || state.facts.connection != attn_client::Connection::Connected
        || super::delegation::busy(state)
    {
        return false;
    }
    let registry_changed = harnesses.seen.is_some() && harnesses.registry != registry(state);
    if harnesses.seen == Some(state.facts.delegation_changed) && !registry_changed {
        return false;
    }
    if registry_changed {
        super::catalogs::rediscover(state);
    }
    read(state, tx);
    true
}

/// Reads the harnesses again once the dialog opens.
pub(super) fn opened(state: &mut State) {
    state.window.settings.harnesses.seen = None;
}

/// Reads the harnesses and the delegation preferences now.
pub(crate) fn read(state: &mut State, tx: &mut Tx) {
    let registry = registry(state);
    let harnesses = &mut state.window.settings.harnesses;
    harnesses.epoch += 1;
    harnesses.loading = true;
    harnesses.seen = Some(state.facts.delegation_changed);
    harnesses.registry = registry;
    let epoch = harnesses.epoch;
    state.ask(
        |request_id| DelegationPreferencesGetMessage {
            request_id,
            ..DelegationPreferencesGetMessage::new()
        },
        move |state, answer, tx: &mut Tx| {
            let harnesses = &mut state.window.settings.harnesses;
            if harnesses.epoch != epoch {
                return;
            }
            harnesses.loading = false;
            let read = answer
                .outcome()
                .and_then(|reply| answered(&reply, "Reading delegation preferences failed"));
            match &read {
                Ok(read) => remember(state, read.clone()),
                Err(why) => harnesses.error = Some(why.clone()),
            }
            super::invalidate(state);
            super::delegation::fetched(state, epoch, read.map(|r| r.preferences), tx);
        },
        tx,
    );
}

/// The table a reply to a read or a save of the delegation preferences carries, or `failed`
/// when it carries none.
pub(super) fn answered(reply: &attn_client::Reply, failed: &str) -> Result<Read, String> {
    match reply.parse::<DelegationPreferencesResultMessage>() {
        Some(DelegationPreferencesResultMessage {
            preferences: Some(preferences),
            templates,
            expanded_roles,
            harnesses,
            ..
        }) => Ok(Read {
            preferences,
            templates: templates.unwrap_or_default(),
            expanded_roles: expanded_roles.unwrap_or_default(),
            harnesses: harnesses.unwrap_or_default(),
        }),
        _ => Err(failed.to_owned()),
    }
}

/// The read on its way, when one is.
pub(super) fn reading(state: &State) -> Option<u64> {
    let harnesses = &state.window.settings.harnesses;
    harnesses.loading.then_some(harnesses.epoch)
}

/// The newest read sent.
pub(super) fn epoch(state: &State) -> u64 {
    state.window.settings.harnesses.epoch
}

/// Counts every change the daemon announced so far as read: a save's answer carries it.
pub(super) fn caught_up(state: &mut State) {
    state.window.settings.harnesses.seen = Some(state.facts.delegation_changed);
}

/// Takes a read of the delegation preferences, from a read or a save's answer.
pub(crate) fn remember(state: &mut State, read: Read) {
    super::invalidate(state);
    let harnesses = &mut state.window.settings.harnesses;
    harnesses.read = Some(read);
    harnesses.error = None;
}

/// Reads again: the Retry beside a failed read.
pub(super) fn retry(state: &mut State, tx: &mut Tx) {
    read(state, tx);
}

/// The harnesses the daemon offers, with a built-in harness's availability as its settings say
/// now.
pub(crate) fn listed(state: &State) -> Vec<Harness> {
    let Some(read) = &state.window.settings.harnesses.read else {
        return Vec::new();
    };
    read.harnesses
        .iter()
        .map(|harness| {
            let mut harness = harness.clone();
            if builtin(&state.facts.settings, &harness.id) {
                match state
                    .facts
                    .settings
                    .get(&format!("{}_available", harness.id))
                    .map(String::as_str)
                {
                    Some("true") => harness.installation = "installed".into(),
                    Some("false") => harness.installation = "missing".into(),
                    _ => {}
                }
            }
            harness
        })
        .collect()
}

/// Whether the daemon says `agent` has `capability`, from its `<agent>_cap_<capability>` setting.
pub(crate) fn has(settings: &BTreeMap<String, String>, agent: &str, capability: &str) -> bool {
    settings
        .get(&format!("{agent}_cap_{capability}"))
        .is_some_and(|v| v == "true")
}

/// A harness attn launches from its own executable, which a setting may name.
pub(crate) fn builtin(settings: &BTreeMap<String, String>, agent: &str) -> bool {
    has(settings, agent, "executable_setting")
}

/// The Retry the pages show beside a failed read.
pub(super) fn failure(state: &State) -> Option<&str> {
    state.window.settings.harnesses.error.as_deref()
}

/// Whether the first read is still out.
pub(super) fn first_loading(state: &State) -> bool {
    let harnesses = &state.window.settings.harnesses;
    harnesses.read.is_none() && harnesses.loading
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Availability {
    Available,
    Unavailable,
    Checking,
}

/// Each harness's availability from its `<harness>_available` setting; Codex and Claude count
/// as available until the daemon says otherwise.
pub(crate) fn availability(settings: &BTreeMap<String, String>) -> BTreeMap<String, Availability> {
    let mut out: BTreeMap<String, Availability> = [
        ("codex".to_owned(), Availability::Available),
        ("claude".to_owned(), Availability::Available),
    ]
    .into();
    for (key, value) in settings {
        let key = key.trim().to_lowercase();
        let Some(agent) = key.strip_suffix("_available").map(str::trim) else {
            continue;
        };
        if agent.is_empty() {
            continue;
        }
        let parsed = match value.trim().to_lowercase().as_str() {
            "true" => Availability::Available,
            "false" => Availability::Unavailable,
            "checking" => Availability::Checking,
            _ => continue,
        };
        out.insert(agent.to_owned(), parsed);
    }
    out
}

pub(crate) fn of(availability: &BTreeMap<String, Availability>, agent: &str) -> Availability {
    availability
        .get(agent)
        .copied()
        .unwrap_or(Availability::Unavailable)
}

pub(crate) fn selectable(availability: &BTreeMap<String, Availability>, agent: &str) -> bool {
    of(availability, agent) != Availability::Unavailable
}

fn normalize(agent: &str, fallback: &str) -> String {
    let agent = agent.trim().to_lowercase();
    if agent.is_empty() {
        fallback.to_owned()
    } else {
        agent
    }
}

/// The agents in the order the pages list them: `preferred`, then Codex and Claude, then the
/// rest by name.
pub(crate) fn ordered(
    availability: &BTreeMap<String, Availability>,
    preferred: Option<&str>,
) -> Vec<String> {
    let mut all: Vec<String> = availability.keys().cloned().collect();
    if !all.iter().any(|a| a == "codex") {
        all.push("codex".into());
    }
    let preferred = preferred.map(|p| normalize(p, "codex"));
    if let Some(p) = &preferred
        && !all.contains(p)
    {
        all.push(p.clone());
    }
    let mut ordered: Vec<String> = Vec::new();
    for agent in preferred
        .iter()
        .cloned()
        .chain(["codex".into(), "claude".into()])
    {
        if all.contains(&agent) && !ordered.contains(&agent) {
            ordered.push(agent);
        }
    }
    let mut rest: Vec<String> = all.into_iter().filter(|a| !ordered.contains(a)).collect();
    rest.sort();
    ordered.extend(rest);
    ordered
}

/// The agent the user picked for new sessions while it is there, else none.
pub(crate) fn picked_default(settings: &BTreeMap<String, String>) -> String {
    let preferred = normalize(
        settings.get("new_session_agent").map_or("", String::as_str),
        "",
    );
    if selectable(&availability(settings), &preferred) {
        preferred
    } else {
        String::new()
    }
}

/// Each agent's capabilities, from its `<agent>_cap_<capability>` settings.
pub(crate) fn capabilities(
    settings: &BTreeMap<String, String>,
) -> BTreeMap<String, BTreeMap<String, bool>> {
    let mut out: BTreeMap<String, BTreeMap<String, bool>> = BTreeMap::new();
    for (key, value) in settings {
        let key = key.trim().to_lowercase();
        let Some(at) = key.find("_cap_").filter(|at| *at > 0) else {
            continue;
        };
        let agent = key[..at].trim();
        let capability = key[at + "_cap_".len()..].trim();
        if agent.is_empty() || capability.is_empty() {
            continue;
        }
        let on = match value.trim().to_lowercase().as_str() {
            "true" => true,
            "false" => false,
            _ => continue,
        };
        out.entry(agent.to_owned())
            .or_default()
            .insert(capability.to_owned(), on);
    }
    out
}

/// The harnesses `agents` name, as a route offers them: the daemon's metadata where it has
/// some, each available while this machine has it.
pub(crate) fn choices(state: &State, agents: &[String]) -> Vec<RouteHarness> {
    let availability = availability(&state.facts.settings);
    let listed = listed(state);
    let reported: Vec<&Harness> = listed.iter().filter(|h| agents.contains(&h.id)).collect();
    let ids: Vec<String> = reported
        .iter()
        .map(|h| h.id.clone())
        .chain(
            agents
                .iter()
                .filter(|a| !reported.iter().any(|h| h.id == **a))
                .cloned(),
        )
        .collect();
    ids.into_iter()
        .map(|id| {
            let metadata = reported.iter().find(|h| h.id == id);
            RouteHarness {
                name: metadata
                    .map(|h| h.name.clone())
                    .filter(|n| !n.is_empty())
                    .unwrap_or_else(|| crate::picker::agents::label(&id)),
                available: selectable(&availability, &id) && metadata.is_none_or(|h| h.available()),
                model_pin: metadata.is_none_or(|h| h.model_pin),
                effort_pin: metadata.is_none_or(|h| h.effort_pin),
                discovery: metadata.is_none_or(|h| h.discovery),
                id,
            }
        })
        .collect()
}

/// [`choices`] for a background task: only a built-in harness that runs headless tasks here
/// is available, and each takes a model and an effort.
pub(crate) fn headless(state: &State, agents: &[String]) -> Vec<RouteHarness> {
    choices(state, agents)
        .into_iter()
        .map(|h| RouteHarness {
            available: h.available
                && builtin(&state.facts.settings, &h.id)
                && has(&state.facts.settings, &h.id, "headless_task"),
            model_pin: true,
            effort_pin: true,
            ..h
        })
        .collect()
}

/// The built-in agents that run headless tasks here, in the pages' order, with `configured`
/// after them when it is not one.
pub(crate) fn headless_agents(
    settings: &BTreeMap<String, String>,
    configured: &str,
) -> Vec<String> {
    let availability = availability(settings);
    let capabilities = capabilities(settings);
    let preferred = picked_default(settings);
    let mut eligible: Vec<String> = ordered(&availability, Some(&preferred))
        .into_iter()
        .filter(|a| {
            builtin(settings, a)
                && selectable(&availability, a)
                && capabilities
                    .get(a)
                    .and_then(|c| c.get("headless_task"))
                    .copied()
                    == Some(true)
        })
        .collect();
    if !configured.is_empty() && !eligible.iter().any(|a| a == configured) {
        eligible.push(configured.to_owned());
    }
    eligible
}

/// The harness list's failure with its Retry, as each block that lists harnesses shows it.
/// Why the harness list could not be read, with Retry, keyed for the part of the page `block`.
pub(super) fn failure_notice(
    state: &State,
    block: &str,
    t: &Tokens,
) -> Option<attn_ui::blocks::Block> {
    let error = failure(state)?;
    Some(
        attn_ui::blocks::Notice::error(format!("settings.{block}.harness-error"), error.to_owned())
            .action(
                attn_ui::system::Button::new("Retry")
                    .on_press_tx(retry)
                    .build(t)
                    .key(format!("settings.{block}.harness-retry")),
            )
            .build(t),
    )
}
