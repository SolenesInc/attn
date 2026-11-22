//! Reads the pushed harness catalog (`Facts::harnesses`) for Settings, and fetches delegation
//! preferences when the Delegation page needs them.

use attn_protocol::{
    DelegationPreferences, DelegationPreferencesGetMessage, DelegationPreferencesResultMessage,
    DelegationRole, Harness,
};
use rune::Tx;

use super::route::RouteHarness;
use crate::State;
use crate::harness::catalog;

/// What the daemon last answered a read of the delegation preferences with.
#[derive(Clone, Debug)]
pub(crate) struct Read {
    pub(crate) preferences: DelegationPreferences,
    pub(crate) templates: Vec<DelegationRole>,
    pub(crate) expanded_roles: Vec<DelegationRole>,
}

type Signature = Vec<(String, bool, String, bool, bool, bool, bool)>;

#[derive(Default)]
pub(crate) struct Harnesses {
    pub(crate) read: Option<Read>,
    pub(crate) error: Option<String>,
    pub(crate) loading: bool,
    /// Rises with each delegation read, so only the newest read's answer lands.
    epoch: u64,
    /// The `Facts::delegation_changed` the last read followed.
    seen: Option<u64>,
    /// The last pushed catalog signature seen, so a harness change rediscovers model catalogs.
    signature: Option<Signature>,
}

fn signature(harnesses: &[Harness]) -> Signature {
    harnesses
        .iter()
        .map(|h| {
            (
                h.id.clone(),
                h.offered,
                h.installation.clone(),
                h.options.initial_prompt,
                h.options.model_pin,
                h.options.effort_pin,
                h.options.discovery,
            )
        })
        .collect()
}

/// Rediscovers model catalogs when the pushed harness catalog changes while a dialog uses them.
pub(super) fn follow_catalog(state: &mut State) {
    if !state.facts.harnesses_loaded {
        return;
    }
    let next = signature(&state.facts.harnesses);
    let changed = state
        .window
        .settings
        .harnesses
        .signature
        .as_ref()
        .is_some_and(|prev| prev != &next);
    state.window.settings.harnesses.signature = Some(next);
    if changed {
        super::catalogs::rediscover(state);
    }
}

/// Reads the delegation preferences when the Delegation page opens or changes arrive.
pub(super) fn ensure(state: &mut State, tx: &mut Tx) -> bool {
    let harnesses = &state.window.settings.harnesses;
    if !(super::is_open(state) && state.window.settings.section == super::Section::Delegation)
        || harnesses.loading
        || state.facts.connection != attn_client::Connection::Connected
        || super::delegation::busy(state)
    {
        return false;
    }
    if harnesses.seen == Some(state.facts.delegation_changed) {
        return false;
    }
    read(state, tx);
    true
}

/// Reads the delegation preferences again once the dialog opens.
pub(super) fn opened(state: &mut State) {
    state.window.settings.harnesses.seen = None;
}

/// Reads the delegation preferences now.
pub(crate) fn read(state: &mut State, tx: &mut Tx) {
    let harnesses = &mut state.window.settings.harnesses;
    harnesses.epoch += 1;
    harnesses.loading = true;
    harnesses.seen = Some(state.facts.delegation_changed);
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
            ..
        }) => Ok(Read {
            preferences,
            templates: templates.unwrap_or_default(),
            expanded_roles: expanded_roles.unwrap_or_default(),
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

/// The offered harnesses that accept an initial prompt, in catalog order.
pub(crate) fn listed(state: &State) -> Vec<Harness> {
    catalog::offered(&state.facts.harnesses)
        .filter(|h| h.options.initial_prompt)
        .cloned()
        .collect()
}

/// The harnesses a background route offers: offered harnesses that can answer one-shot calls here,
/// in catalog order (with preferred first), plus `configured` after them when saved but ineligible.
pub(crate) fn headless(
    state: &State,
    configured: &str,
    default_effort: fn(&Harness) -> Option<&str>,
) -> Vec<RouteHarness> {
    let preferred = catalog::picked_default(&state.facts.settings, &state.facts.harnesses);
    let mut out: Vec<RouteHarness> = catalog::ordered(&state.facts.harnesses, Some(&preferred))
        .into_iter()
        .filter(|h| h.available() && h.options.one_shot)
        .map(|h| {
            let mut route = RouteHarness::from(h);
            route.model_pin = true;
            route.effort_pin = true;
            route.default_effort = default_effort(h).unwrap_or_default().to_owned();
            route
        })
        .collect();
    let configured = catalog::normalize(configured);
    if !configured.is_empty() && !out.iter().any(|h| h.id == configured) {
        let entry = catalog::by_id(&state.facts.harnesses, &configured);
        out.push(RouteHarness {
            id: configured.clone(),
            name: catalog::name(&state.facts.harnesses, &configured),
            available: false,
            model_pin: true,
            effort_pin: true,
            discovery: entry.is_some_and(|h| h.options.discovery),
            default_effort: entry
                .and_then(default_effort)
                .unwrap_or_default()
                .to_owned(),
        });
    }
    out
}
