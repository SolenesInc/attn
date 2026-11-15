//! Each harness's model catalog, discovered once per executable and kept while the daemon's tier
//! overrides stay the same: the pages and the model picker share it.

use std::collections::HashMap;

use attn_protocol::{HarnessModel, HarnessModelsMessage, HarnessModelsResultMessage, TierDefaults};
use rune::Tx;

use crate::State;

pub(crate) const TIER_OVERRIDES: &str = "model_tier_overrides";

#[derive(Clone, Debug, Default)]
pub(crate) struct Catalog {
    pub(crate) models: Vec<HarnessModel>,
    pub(crate) detail: String,
    pub(crate) tier_defaults: TierDefaults,
}

impl Catalog {
    pub(crate) fn tier_default(&self, tier: &str) -> Option<&str> {
        match tier {
            "light" => self.tier_defaults.light.as_deref(),
            "standard" => self.tier_defaults.standard.as_deref(),
            "deep" => self.tier_defaults.deep.as_deref(),
            _ => None,
        }
    }
}

#[derive(Default)]
pub(crate) struct Entry {
    pub(crate) catalog: Option<Catalog>,
    pub(crate) error: Option<String>,
    pub(crate) loading: bool,
    executable: String,
    generation: u64,
    epoch: u64,
}

#[derive(Default)]
pub(crate) struct Catalogs {
    entries: HashMap<String, Entry>,
    /// Rises each time the tier overrides change: every catalog is discovered again.
    generation: u64,
    overrides: Option<String>,
    epoch: u64,
}

/// What the pages read of a harness's catalog.
#[derive(Clone, Copy, Default)]
pub(crate) struct Discovered<'a> {
    pub(crate) catalog: Option<&'a Catalog>,
    pub(crate) loading: bool,
    pub(crate) error: Option<&'a str>,
}

fn executable(state: &State, harness: &str) -> String {
    state
        .facts
        .settings
        .get(&format!("{harness}_executable"))
        .cloned()
        .unwrap_or_default()
}

/// The catalog of `harness` as discovered for its executable now.
pub(crate) fn of<'a>(state: &'a State, harness: &str, discovery: bool) -> Discovered<'a> {
    if !discovery {
        return Discovered::default();
    }
    let exec = executable(state, harness);
    match state.window.settings.catalogs.entries.get(harness) {
        Some(entry) if entry.executable == exec => Discovered {
            catalog: entry.catalog.as_ref(),
            loading: entry.loading,
            error: entry.error.as_deref(),
        },
        _ => Discovered::default(),
    }
}

/// Starts over when the daemon's tier overrides changed: every catalog is discovered again.
pub(super) fn follow(state: &mut State) {
    let overrides = state.facts.settings.get(TIER_OVERRIDES).cloned();
    let catalogs = &mut state.window.settings.catalogs;
    if catalogs.overrides != overrides {
        catalogs.overrides = overrides;
        catalogs.generation += 1;
    }
}

/// Discovers every catalog again.
pub(super) fn rediscover(state: &mut State) {
    state.window.settings.catalogs.generation += 1;
}

/// Discovers `harness`'s models unless its catalog for this executable is here or on its way;
/// `refresh` asks the harness again. Whether it asked.
pub(crate) fn discover(state: &mut State, harness: &str, refresh: bool, tx: &mut Tx) -> bool {
    if state.facts.connection != attn_client::Connection::Connected || harness.is_empty() {
        return false;
    }
    let exec = executable(state, harness);
    let catalogs = &mut state.window.settings.catalogs;
    let generation = catalogs.generation;
    let existing = catalogs.entries.get(harness);
    if let Some(entry) = existing
        && entry.generation == generation
        && entry.executable == exec
        && (entry.loading || (!refresh && (entry.catalog.is_some() || entry.error.is_some())))
    {
        return false;
    }
    let kept = existing
        .filter(|entry| entry.executable == exec)
        .and_then(|entry| entry.catalog.clone());
    catalogs.epoch += 1;
    let epoch = catalogs.epoch;
    catalogs.entries.insert(
        harness.to_owned(),
        Entry {
            catalog: kept,
            error: None,
            loading: true,
            executable: exec,
            generation,
            epoch,
        },
    );
    let id = harness.to_owned();
    state.ask(
        |request_id| HarnessModelsMessage {
            request_id,
            harness: id.clone(),
            refresh: refresh.then_some(true),
            ..HarnessModelsMessage::new()
        },
        {
            let id = harness.to_owned();
            move |state, answer, _: &mut Tx| {
                let catalogs = &mut state.window.settings.catalogs;
                let current = catalogs.generation;
                let Some(entry) = catalogs.entries.get_mut(&id).filter(|e| e.epoch == epoch) else {
                    return;
                };
                entry.loading = false;
                if entry.generation != current {
                    return;
                }
                match answer.outcome() {
                    Ok(reply) => match reply.parse::<HarnessModelsResultMessage>() {
                        Some(result) => {
                            entry.catalog = Some(Catalog {
                                models: result.models,
                                detail: result.detail,
                                tier_defaults: result.tier_defaults,
                            });
                            entry.error = None;
                        }
                        None => {
                            entry.catalog = None;
                            entry.error = Some("Discovering models failed".into());
                        }
                    },
                    Err(why) => {
                        entry.catalog = None;
                        entry.error = Some(why);
                    }
                }
            }
        },
        tx,
    );
    true
}

/// Whether discovery of `harness` has run in this process, so a picker opened over it follows a
/// new executable.
pub(crate) fn asked(state: &State, harness: &str) -> bool {
    state.window.settings.catalogs.entries.contains_key(harness)
}
