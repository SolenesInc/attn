use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use attn_protocol::{Seed, SeedCheck, SeedCheckTally, Session};

/// The garden's seeds by id, with each plot's children, each seed's inbound relations and how
/// many open seeds block it. A plot whose crown the push left out leaves its children as roots.
#[derive(Default)]
pub(crate) struct Index {
    pub(crate) by_id: HashMap<String, Arc<Seed>>,
    pub(crate) children: HashMap<String, Vec<Arc<Seed>>>,
    inbound: HashMap<String, Vec<Relation>>,
    blockers: HashMap<String, usize>,
    pub(crate) roots: Vec<Arc<Seed>>,
}

#[derive(Clone)]
pub(crate) struct Relation {
    pub(crate) label: &'static str,
    pub(crate) seed: Arc<Seed>,
}

pub(crate) fn parent_of(seed: &Seed) -> &str {
    seed.edges
        .iter()
        .find(|e| e.kind == "part-of")
        .map_or("", |e| e.to.as_str())
}

pub(crate) fn is_closed(seed: &Seed) -> bool {
    matches!(seed.status.as_str(), "harvested" | "withered")
}

pub(crate) fn is_plot(seed: &Seed) -> bool {
    seed.plot_progress.is_some()
}

impl Index {
    pub(crate) fn of(seeds: &[Arc<Seed>]) -> Index {
        let mut index = Index::default();
        for seed in seeds {
            index.by_id.insert(seed.id.clone(), seed.clone());
        }
        for seed in seeds {
            let crown = parent_of(seed);
            if !crown.is_empty() && index.by_id.contains_key(crown) {
                index
                    .children
                    .entry(crown.to_owned())
                    .or_default()
                    .push(seed.clone());
            } else {
                index.roots.push(seed.clone());
            }
            for edge in &seed.edges {
                let label = match edge.kind.as_str() {
                    "blocks" => "blocked by",
                    "part-of" => "has part",
                    "discovered-from" => "discovered",
                    _ => continue,
                };
                index
                    .inbound
                    .entry(edge.to.clone())
                    .or_default()
                    .push(Relation {
                        label,
                        seed: seed.clone(),
                    });
                if edge.kind == "blocks" && !is_closed(seed) {
                    *index.blockers.entry(edge.to.clone()).or_default() += 1;
                }
            }
        }
        index
    }

    pub(crate) fn blockers(&self, id: &str) -> usize {
        self.blockers.get(id).copied().unwrap_or(0)
    }

    pub(crate) fn children_of(&self, id: &str) -> &[Arc<Seed>] {
        self.children.get(id).map_or(&[], Vec::as_slice)
    }

    /// The seed's edges out, named from its side, then the edges into it, except its parts.
    pub(crate) fn relations(&self, id: &str) -> Vec<Relation> {
        let mut rows = Vec::new();
        if let Some(seed) = self.by_id.get(id) {
            for edge in &seed.edges {
                let Some(other) = self.by_id.get(&edge.to) else {
                    continue;
                };
                let label = match edge.kind.as_str() {
                    "blocks" => "blocks",
                    "part-of" => "part of",
                    "discovered-from" => "discovered from",
                    _ => continue,
                };
                rows.push(Relation {
                    label,
                    seed: other.clone(),
                });
            }
        }
        rows.extend(
            self.inbound
                .get(id)
                .into_iter()
                .flatten()
                .filter(|r| r.label != "has part")
                .cloned(),
        );
        rows
    }

    /// Every seed below `root`, at any depth.
    pub(crate) fn subtree(&self, root: &str) -> HashSet<String> {
        let mut wanted = HashSet::new();
        let mut queue = vec![root.to_owned()];
        while let Some(id) = queue.pop() {
            for child in self.children_of(&id) {
                if wanted.insert(child.id.clone()) {
                    queue.push(child.id.clone());
                }
            }
        }
        wanted
    }

    /// The trail from the root to `id` through each seed's plot, as far up as the garden
    /// holds; a cycle stops it.
    pub(crate) fn path_to(&self, id: &str) -> Vec<String> {
        let mut path = Vec::new();
        let mut seen = HashSet::new();
        let mut cursor = self.by_id.get(id);
        while let Some(seed) = cursor {
            if !seen.insert(seed.id.clone()) {
                break;
            }
            path.insert(0, seed.id.clone());
            let parent = parent_of(seed);
            cursor = (!parent.is_empty())
                .then(|| self.by_id.get(parent))
                .flatten();
        }
        path
    }
}

/// The word a row shows beside its title for where the seed stands, and its tone.
pub(crate) fn signal(seed: &Seed, blockers: usize) -> Option<(String, &'static str)> {
    if blockers > 0 {
        return Some((format!("blocked by {blockers}"), "blocked"));
    }
    let (text, tone) = match seed.status.as_str() {
        "growing" => ("growing", "active"),
        "parked" => ("parked", "parked"),
        "withered" => ("withered", "closed"),
        "harvested" => ("done", "closed"),
        _ => return None,
    };
    Some((text.to_owned(), tone))
}

pub(crate) fn progress_words(seed: &Seed) -> String {
    let Some(p) = &seed.plot_progress else {
        return String::new();
    };
    let mut parts = vec![format!("{}/{} done", p.done, p.total)];
    for (count, word) in [
        (p.growing, "growing"),
        (p.ready, "ready"),
        (p.blocked, "blocked"),
        (p.parked, "parked"),
    ] {
        if count > 0 {
            parts.push(format!("{count} {word}"));
        }
    }
    parts.join(" · ")
}

/// The sessions' names by id, as a seed's tender and log name a session.
pub(crate) type SessionNames = HashMap<String, String>;

pub(crate) fn session_names(sessions: &[Session]) -> SessionNames {
    sessions
        .iter()
        .map(|s| (s.id.clone(), crate::sidebar::model::display_name(s)))
        .collect()
}

/// Who `reference` names: a live session by its current name, else the name the daemon gave.
pub(crate) fn who_name(reference: &str, name: &str, names: &SessionNames) -> String {
    let live = reference
        .strip_prefix("session:")
        .and_then(|id| names.get(id))
        .map(|n| n.trim())
        .filter(|n| !n.is_empty());
    if let Some(live) = live {
        return live.to_owned();
    }
    if !name.trim().is_empty() {
        return name.trim().to_owned();
    }
    reference.to_owned()
}

pub(crate) fn tender_of(seed: &Seed, names: &SessionNames) -> String {
    seed.tender
        .as_ref()
        .map(|t| who_name(&t.r#ref, &t.name, names))
        .unwrap_or_default()
}

/// The seed's checks by state, or its plot's: none when it has no check.
pub(crate) fn check_tally(seed: &Seed) -> Option<SeedCheckTally> {
    if let Some(progress) = &seed.plot_progress {
        return progress.checks.clone();
    }
    tally_of(seed.checks.iter().flatten())
}

/// The checks that are not removed, by state: none when every check is removed.
pub(crate) fn tally_of<'a>(
    checks: impl IntoIterator<Item = &'a SeedCheck>,
) -> Option<SeedCheckTally> {
    tally_states(
        checks
            .into_iter()
            .filter(|c| !c.removed)
            .map(|c| c.state.as_str()),
    )
}

pub(crate) fn tally_states<'a>(
    states: impl IntoIterator<Item = &'a str>,
) -> Option<SeedCheckTally> {
    let mut tally = SeedCheckTally::default();
    for state in states {
        tally.total += 1;
        match state {
            "reported" => tally.reported += 1,
            "confirmed" => tally.confirmed += 1,
            "rejected" => tally.rejected += 1,
            _ => {}
        }
    }
    (tally.total > 0).then_some(tally)
}

pub(crate) fn tally_sentence(tally: &SeedCheckTally) -> String {
    let mut parts = vec![format!("{} of {} confirmed", tally.confirmed, tally.total)];
    if tally.reported > 0 {
        parts.push(format!("{} to judge", tally.reported));
    }
    if tally.rejected > 0 {
        parts.push(format!("{} rejected", tally.rejected));
    }
    parts.join(" · ")
}

pub(crate) fn tally_words(tally: &SeedCheckTally) -> String {
    let mut parts = vec![format!("✓{}/{}", tally.confirmed, tally.total)];
    if tally.reported > 0 {
        parts.push(format!("{} to judge", tally.reported));
    }
    if tally.rejected > 0 {
        parts.push(format!("{} rejected", tally.rejected));
    }
    parts.join(" · ")
}

/// `owner/repo#n` and `#n` from a pull request id `host:owner/repo#n`.
pub(crate) fn pull_request_ref(id: &str) -> (String, String) {
    let pull_request = id.split_once(':').map_or(id, |(_, rest)| rest).to_owned();
    let number = pull_request
        .rfind('#')
        .map_or(pull_request.clone(), |at| pull_request[at..].to_owned());
    (pull_request, number)
}

/// What an armed seed waits on: the whole sentence, and the word a row has room for.
pub(crate) fn harvest_when(seed: &Seed) -> Option<(String, String)> {
    let id = seed.harvest_when.as_ref()?.pull_request.trim();
    if id.is_empty() {
        return None;
    }
    let (pull_request, number) = pull_request_ref(id);
    Some((
        format!("harvests when {pull_request} merges"),
        format!("harvests on {number}"),
    ))
}

/// How long ago `iso` was, as a row says it.
pub(crate) fn ago(iso: &str, now_ms: i64) -> String {
    let Some(at) = crate::sidebar::queue::epoch_ms(iso) else {
        return String::new();
    };
    let seconds = ((now_ms - at) as f64 / 1000.0).round() as i64;
    if seconds < 60 {
        "just now".into()
    } else if seconds < 3600 {
        format!("{}m ago", (seconds as f64 / 60.0).round() as i64)
    } else if seconds < 86_400 {
        format!("{}h ago", (seconds as f64 / 3600.0).round() as i64)
    } else {
        format!("{}d ago", (seconds as f64 / 86_400.0).round() as i64)
    }
}
