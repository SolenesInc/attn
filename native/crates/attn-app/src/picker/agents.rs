use std::collections::{BTreeMap, BTreeSet};

use attn_protocol::Harness;

pub(crate) use crate::harness::catalog::{Availability, TERMINAL};

/// Which harnesses this machine offers, from the daemon's pushed catalog, with Terminal always
/// there and in the daemon's catalog order.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct Agents {
    availability: BTreeMap<String, Availability>,
    order: Vec<String>,
}

impl Agents {
    pub(crate) fn from_catalog(harnesses: &[Harness]) -> Agents {
        let mut availability = BTreeMap::new();
        let mut order = Vec::new();
        for h in crate::harness::catalog::offered(harnesses) {
            let id = crate::harness::catalog::normalize(&h.id);
            if id.is_empty() || id == TERMINAL {
                continue;
            }
            if !availability.contains_key(&id) {
                order.push(id.clone());
            }
            availability.insert(id, crate::harness::catalog::availability(h));
        }
        availability.insert(TERMINAL.into(), Availability::Available);
        Agents {
            availability,
            order,
        }
    }

    /// These agents with the harnesses another machine has installed offered too, except
    /// those this machine is still checking for.
    pub(crate) fn with_installed(mut self, installed: impl IntoIterator<Item = String>) -> Agents {
        for agent in installed {
            let id = crate::harness::catalog::normalize(&agent);
            if id.is_empty() {
                continue;
            }
            if self.of(&id) != Availability::Checking {
                self.availability.insert(id, Availability::Available);
            }
        }
        self
    }

    /// Every harness these agents know of, Terminal among them.
    pub(crate) fn ids(&self) -> impl Iterator<Item = &String> {
        self.availability.keys()
    }

    /// The harnesses that are there or still being checked for.
    pub(crate) fn installed(&self) -> BTreeSet<String> {
        self.availability
            .keys()
            .filter(|id| self.selectable(id))
            .cloned()
            .collect()
    }

    pub(crate) fn of(&self, agent: &str) -> Availability {
        self.availability
            .get(&crate::harness::catalog::normalize(agent))
            .copied()
            .unwrap_or(Availability::Unavailable)
    }

    pub(crate) fn selectable(&self, agent: &str) -> bool {
        self.of(agent) != Availability::Unavailable
    }

    /// The agents the picker lists: Terminal, then the harness catalog in the daemon's order,
    /// then any outpost-only harnesses by name. Before the catalog arrives, only Terminal.
    pub(crate) fn listed(&self, loaded: bool) -> Vec<String> {
        let mut listed = vec![TERMINAL.to_owned()];
        if !loaded {
            return listed;
        }
        let mut rest: Vec<&String> = self
            .availability
            .keys()
            .filter(|k| *k != TERMINAL && !self.order.contains(k))
            .collect();
        rest.sort();
        for agent in self.order.iter().chain(rest) {
            if !listed.contains(agent) {
                listed.push(agent.clone());
            }
        }
        listed
    }

    /// The agent the picker starts on: `preferred` while it is there, else none.
    pub(crate) fn resolve(&self, preferred: &str) -> String {
        let preferred = crate::harness::catalog::normalize(preferred);
        if self.selectable(&preferred) {
            preferred
        } else {
            String::new()
        }
    }
}

/// What the harness toggle says once a start found no harness chosen, with the keys that choose one.
pub(crate) fn choose_one() -> String {
    let keys = if rune::Platform::current() == rune::Platform::Mac {
        "⌥1–⌥9"
    } else {
        "Alt+1–Alt+9"
    };
    format!("Choose a harness ({keys})")
}

pub(crate) fn named(harnesses: &[Harness], agent: &str) -> String {
    if crate::harness::catalog::normalize(agent) == TERMINAL {
        "Terminal".into()
    } else {
        crate::harness::catalog::name(harnesses, agent)
    }
}

/// What the YOLO switch says for `agent`, a harness without the mode.
pub(crate) fn no_yolo(harnesses: &[Harness], agent: &str) -> String {
    format!(
        "{} does not support yolo mode",
        crate::harness::catalog::name(harnesses, agent)
    )
}

/// Whether the user last left YOLO on.
pub(crate) fn saved_yolo(settings: &BTreeMap<String, String>) -> bool {
    ["new_session_yolo_local", "new_session_yolo"]
        .iter()
        .find_map(
            |key| match settings.get(*key)?.trim().to_lowercase().as_str() {
                "true" => Some(true),
                "false" => Some(false),
                _ => None,
            },
        )
        .unwrap_or(false)
}
