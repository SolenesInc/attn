use std::collections::BTreeMap;

pub(crate) const TERMINAL: &str = "shell";
const FALLBACK: &str = "codex";

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Availability {
    Available,
    Checking,
    Unavailable,
}

/// Which harnesses this machine has, from the daemon's `<harness>_available` settings, with
/// Terminal always there.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct Agents(BTreeMap<String, Availability>);

impl Agents {
    pub(crate) fn from_settings(settings: &BTreeMap<String, String>) -> Agents {
        let mut agents: BTreeMap<String, Availability> = [
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
            let availability = match value.trim().to_lowercase().as_str() {
                "true" => Availability::Available,
                "false" => Availability::Unavailable,
                "checking" => Availability::Checking,
                _ => continue,
            };
            agents.insert(agent.to_owned(), availability);
        }
        agents.insert(TERMINAL.into(), Availability::Available);
        Agents(agents)
    }

    pub(crate) fn of(&self, agent: &str) -> Availability {
        self.0
            .get(agent)
            .copied()
            .unwrap_or(Availability::Unavailable)
    }

    pub(crate) fn selectable(&self, agent: &str) -> bool {
        self.of(agent) != Availability::Unavailable
    }

    /// The agents the picker lists: Terminal, then the harness catalog in the daemon's order,
    /// then the other harnesses the daemon reports, by name. Before the catalog arrives, only
    /// Terminal.
    pub(crate) fn listed(&self, catalog: Option<&[String]>) -> Vec<String> {
        let mut listed = vec![TERMINAL.to_owned()];
        let Some(catalog) = catalog else {
            return listed;
        };
        let mut rest: Vec<&String> = self.0.keys().filter(|k| !catalog.contains(k)).collect();
        rest.sort();
        for agent in catalog.iter().chain(rest) {
            if !listed.contains(agent) {
                listed.push(agent.clone());
            }
        }
        listed
    }

    /// The agent the picker starts on or falls back to: Terminal only when asked for or when
    /// no harness is there, else `preferred` when it is there, else the first harness that is.
    pub(crate) fn resolve(&self, preferred: &str) -> String {
        if preferred == TERMINAL && self.selectable(TERMINAL) {
            return TERMINAL.into();
        }
        let harnesses = Agents(
            self.0
                .iter()
                .filter(|(agent, _)| *agent != TERMINAL)
                .map(|(a, v)| (a.clone(), *v))
                .collect(),
        );
        if harnesses.0.keys().any(|agent| harnesses.selectable(agent)) {
            return harnesses.preferred_or_first(preferred);
        }
        if self.selectable(TERMINAL) {
            return TERMINAL.into();
        }
        self.preferred_or_first(preferred)
    }

    fn preferred_or_first(&self, preferred: &str) -> String {
        let preferred = normalize(preferred);
        if !preferred.is_empty() && self.selectable(&preferred) {
            return preferred;
        }
        let mut ordered: Vec<String> = vec![FALLBACK.into(), "claude".into()];
        let mut rest: Vec<String> = self
            .0
            .keys()
            .filter(|k| !ordered.contains(k))
            .cloned()
            .collect();
        rest.sort();
        ordered.extend(rest);
        ordered
            .into_iter()
            .find(|agent| self.selectable(agent))
            .unwrap_or_else(|| FALLBACK.into())
    }
}

fn normalize(agent: &str) -> String {
    agent.trim().to_lowercase()
}

pub(crate) fn label(agent: &str) -> String {
    match normalize(agent).as_str() {
        TERMINAL => "Terminal".into(),
        "claude" => "Claude".into(),
        "codex" => "Codex".into(),
        other => other
            .split(['-', '_', ' '])
            .filter(|part| !part.is_empty())
            .map(|part| {
                let mut chars = part.chars();
                chars
                    .next()
                    .map(|first| first.to_uppercase().chain(chars).collect::<String>())
                    .unwrap_or_default()
            })
            .collect::<Vec<_>>()
            .join(" "),
    }
}

/// How the YOLO switch's title names an agent: the shell as the shell.
pub(crate) fn yolo_label(agent: &str) -> String {
    if agent == TERMINAL {
        "Shell".into()
    } else {
        label(agent)
    }
}

pub(crate) fn supports_yolo(settings: &BTreeMap<String, String>, agent: &str) -> bool {
    settings
        .get(&format!("{agent}_cap_yolo"))
        .is_some_and(|v| v.trim().eq_ignore_ascii_case("true"))
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
