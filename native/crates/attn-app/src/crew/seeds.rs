//! The seeds a member tends (from the live open push) or planted (paged from the daemon).

use std::collections::hash_map::DefaultHasher;
use std::collections::{HashMap, HashSet};
use std::hash::{Hash, Hasher};
use std::sync::Arc;

use attn_protocol::{CrewMember, GardenSeedsPageMessage, GardenSeedsPageResultMessage, Seed};
use rune::Tx;

use super::member_ref;
use crate::State;
use crate::requests::Answer;

pub(crate) const PAGE_LIMIT: i64 = 50;
const MAX_PAGE_LIMIT: i64 = 200;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(crate) enum SeedFilter {
    #[default]
    Tending,
    Planted,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub(crate) struct PlantedKey {
    pub(crate) profile: String,
    pub(crate) member: String,
    pub(crate) query: String,
}

#[derive(Default)]
pub(crate) struct PlantedPages {
    pub(crate) key: PlantedKey,
    garden_ptr: usize,
    asked_sig: u64,
    pub(crate) seeds: Vec<Arc<Seed>>,
    pub(crate) total: i64,
    unfiltered_total: Option<i64>,
    unfiltered_scope: (String, String),
    pub(crate) next_before: Option<String>,
    retry_before: Option<String>,
    pub(crate) loading: bool,
    pub(crate) loading_more: bool,
    pub(crate) answered: bool,
    pub(crate) error: Option<String>,
    request: u64,
}

impl PlantedPages {
    pub(crate) fn clear(&mut self) {
        self.request += 1;
        self.key = PlantedKey::default();
        self.garden_ptr = 0;
        self.asked_sig = 0;
        self.seeds.clear();
        self.total = 0;
        self.unfiltered_total = None;
        self.unfiltered_scope = (String::new(), String::new());
        self.next_before = None;
        self.retry_before = None;
        self.loading = false;
        self.loading_more = false;
        self.answered = false;
        self.error = None;
    }

    pub(crate) fn badge_count(&self, profile: &str, member: &str) -> Option<i64> {
        if self.unfiltered_scope.0 == profile && self.unfiltered_scope.1 == member {
            return self.unfiltered_total;
        }
        None
    }
}

fn planted_signature(state: &State, profile: &str, member: &str) -> u64 {
    let mut hasher = DefaultHasher::new();
    let garden = &state.facts.garden;
    profile.hash(&mut hasher);
    member.hash(&mut hasher);
    garden.full_push.hash(&mut hasher);
    garden.total.hash(&mut hasher);
    if let Some(counts) = &garden.closed_counts {
        counts.all.harvested.hash(&mut hasher);
        counts.all.withered.hash(&mut hasher);
        counts.automations.harvested.hash(&mut hasher);
        counts.automations.withered.hash(&mut hasher);
        counts.unassigned.harvested.hash(&mut hasher);
        counts.unassigned.withered.hash(&mut hasher);
        for bed in &counts.by_bed {
            bed.bed_id.hash(&mut hasher);
            bed.harvested.hash(&mut hasher);
            bed.withered.hash(&mut hasher);
        }
    }
    let reference = member_ref(member);
    for seed in garden.of(profile) {
        if seed.planter.r#ref == reference {
            seed.id.hash(&mut hasher);
            seed.rev.hash(&mut hasher);
            seed.status.hash(&mut hasher);
        }
    }
    hasher.finish()
}

fn failed(answer: &Answer, error: Option<String>) -> String {
    match answer {
        Answer::NotSent => "not connected to the daemon".into(),
        Answer::Dropped => "the daemon did not answer".into(),
        Answer::Reply(_) => error
            .filter(|e| !e.is_empty())
            .unwrap_or_else(|| "Could not load planted seeds.".into()),
    }
}

pub(crate) fn sync(state: &mut State, tx: &mut Tx) {
    if !super::is_open(state) {
        state.window.crew.planted.clear();
        return;
    }
    if state.window.crew.place.tab != super::Tab::Seeds {
        return;
    }
    let Some(member) = super::selected(state).map(|m| m.id.clone()) else {
        state.window.crew.planted.clear();
        return;
    };
    let profile = state.facts.profiles.selected.clone();
    if profile.is_empty() {
        state.window.crew.planted.clear();
        return;
    }
    let query = if state.window.crew.place.seed_filter == SeedFilter::Planted {
        state.window.crew.place.seed_query.text.trim().to_owned()
    } else {
        String::new()
    };
    let key = PlantedKey {
        profile,
        member,
        query,
    };
    let garden_ptr = Arc::as_ptr(&state.facts.garden) as usize;
    if state.window.crew.planted.key == key && state.window.crew.planted.garden_ptr == garden_ptr {
        return;
    }
    let sig = planted_signature(state, &key.profile, &key.member);
    let planted = &mut state.window.crew.planted;
    let key_changed = planted.key != key;
    if !key_changed && planted.asked_sig == sig {
        planted.garden_ptr = garden_ptr;
        return;
    }
    let scope_changed =
        planted.unfiltered_scope.0 != key.profile || planted.unfiltered_scope.1 != key.member;
    if scope_changed {
        planted.unfiltered_total = None;
        planted.unfiltered_scope = (key.profile.clone(), key.member.clone());
    }
    let limit = if key_changed {
        planted.seeds.clear();
        planted.total = 0;
        planted.next_before = None;
        planted.retry_before = None;
        planted.answered = false;
        planted.error = None;
        PAGE_LIMIT
    } else {
        (planted.seeds.len() as i64).clamp(PAGE_LIMIT, MAX_PAGE_LIMIT)
    };
    fetch(state, key, garden_ptr, sig, None, limit, tx);
}

pub(crate) fn load_more(state: &mut State, tx: &mut Tx) -> bool {
    let planted = &state.window.crew.planted;
    if planted.loading || planted.loading_more || planted.error.is_some() {
        return false;
    }
    let Some(cursor) = planted.next_before.clone() else {
        return false;
    };
    let (key, garden_ptr, sig) = (planted.key.clone(), planted.garden_ptr, planted.asked_sig);
    fetch(state, key, garden_ptr, sig, Some(cursor), PAGE_LIMIT, tx);
    true
}

pub(crate) fn retry(state: &mut State, tx: &mut Tx) {
    let planted = &state.window.crew.planted;
    let cursor = planted.retry_before.clone();
    let limit = if cursor.is_some() {
        PAGE_LIMIT
    } else {
        (planted.seeds.len() as i64).clamp(PAGE_LIMIT, MAX_PAGE_LIMIT)
    };
    let (key, garden_ptr, sig) = (planted.key.clone(), planted.garden_ptr, planted.asked_sig);
    if key.profile.is_empty() || key.member.is_empty() {
        sync(state, tx);
        return;
    }
    fetch(state, key, garden_ptr, sig, cursor, limit, tx);
}

fn fetch(
    state: &mut State,
    key: PlantedKey,
    garden_ptr: usize,
    sig: u64,
    before: Option<String>,
    limit: i64,
    tx: &mut Tx,
) {
    let append = before.is_some();
    let failed_before = before.clone();
    let planted = &mut state.window.crew.planted;
    planted.request += 1;
    let request = planted.request;
    planted.key = key.clone();
    planted.garden_ptr = garden_ptr;
    planted.asked_sig = sig;
    planted.error = None;
    planted.retry_before = None;
    if append {
        planted.loading_more = true;
    } else {
        planted.loading = true;
        planted.loading_more = false;
    }
    let planter = member_ref(&key.member);
    let query = (!key.query.is_empty()).then(|| key.query.clone());
    state.ask(
        move |request_id| GardenSeedsPageMessage {
            request_id,
            planter: Some(planter),
            status: Some("all".into()),
            query,
            before,
            limit: Some(limit),
            ..GardenSeedsPageMessage::new()
        },
        move |state, answer, _| {
            let planted = &mut state.window.crew.planted;
            if planted.request != request {
                return;
            }
            planted.loading = false;
            planted.loading_more = false;
            match answer.parse::<GardenSeedsPageResultMessage>() {
                Some(reply) if reply.success => {
                    planted.answered = true;
                    planted.total = reply.total;
                    if planted.key.query.is_empty() {
                        planted.unfiltered_total = Some(reply.total);
                        planted.unfiltered_scope =
                            (planted.key.profile.clone(), planted.key.member.clone());
                    }
                    planted.error = None;
                    planted.retry_before = None;
                    if append {
                        planted.next_before = reply.next_before;
                        let mut seen: HashSet<String> =
                            planted.seeds.iter().map(|s| s.id.clone()).collect();
                        for seed in reply.seeds {
                            if seen.insert(seed.id.clone()) {
                                planted.seeds.push(Arc::new(seed));
                            }
                        }
                    } else if planted.seeds.len() > MAX_PAGE_LIMIT as usize
                        && reply.next_before.is_some()
                    {
                        let mut merged: Vec<Arc<Seed>> =
                            reply.seeds.into_iter().map(Arc::new).collect();
                        let seen: HashSet<&str> = merged.iter().map(|s| s.id.as_str()).collect();
                        let tail: Vec<Arc<Seed>> = planted
                            .seeds
                            .iter()
                            .filter(|s| !seen.contains(s.id.as_str()))
                            .cloned()
                            .collect();
                        merged.extend(tail);
                        planted.seeds = merged;
                    } else {
                        planted.next_before = reply.next_before;
                        planted.seeds = reply.seeds.into_iter().map(Arc::new).collect();
                    }
                }
                other => {
                    planted.retry_before = failed_before;
                    planted.error = Some(failed(&answer, other.and_then(|r| r.error)));
                }
            }
        },
        tx,
    );
}

/// The seeds `member` tends: by its key, or by its session's claims.
pub(crate) fn tended<'a>(seeds: &'a [Arc<Seed>], member: &CrewMember) -> Vec<&'a Seed> {
    let reference = member_ref(&member.id);
    let session = member.binding_session.as_deref().unwrap_or_default();
    seeds
        .iter()
        .map(Arc::as_ref)
        .filter(|seed| {
            seed.tender.as_ref().is_some_and(|t| {
                t.r#ref == reference
                    || (!session.is_empty() && t.session_id.as_deref() == Some(session))
            })
        })
        .collect()
}

pub(crate) fn by_id(live: &[Arc<Seed>], extra: &[Arc<Seed>]) -> HashMap<String, Arc<Seed>> {
    let mut map = HashMap::with_capacity(live.len() + extra.len());
    for seed in live {
        map.entry(seed.id.clone()).or_insert_with(|| seed.clone());
    }
    for seed in extra {
        map.entry(seed.id.clone()).or_insert_with(|| seed.clone());
    }
    map
}

/// The plot a seed is part of, when either the live garden or the loaded page holds it.
pub(crate) fn plot<'a>(seed: &Seed, map: &'a HashMap<String, Arc<Seed>>) -> Option<&'a Seed> {
    let parent = seed.edges.iter().find(|e| e.kind == "part-of")?;
    map.get(&parent.to).map(Arc::as_ref)
}

/// A plot's progress as `done/total`, or nothing for a seed that is no plot.
pub(crate) fn progress(seed: Option<&Seed>) -> String {
    match seed.and_then(|s| s.plot_progress.as_ref()) {
        Some(p) if p.total > 0 => format!("{}/{}", p.done, p.total),
        _ => String::new(),
    }
}

/// What the tab says when the filter shows no seed.
pub(crate) fn empty(name: &str, filter: SeedFilter) -> String {
    match filter {
        SeedFilter::Tending => format!("{name} isn't tending a seed."),
        SeedFilter::Planted => format!("No seeds were explicitly planted by {name}."),
    }
}

/// Whether a seed's title or id holds `query`, ignoring case.
pub(crate) fn matches(seed: &Seed, query: &str) -> bool {
    let needle = query.trim().to_lowercase();
    needle.is_empty()
        || format!("{} {}", seed.title, seed.id)
            .to_lowercase()
            .contains(&needle)
}
