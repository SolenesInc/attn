//! The seeds a member tends or planted, out of the Garden the daemon last sent.

use std::sync::Arc;

use attn_protocol::{CrewMember, Seed};

use super::member_ref;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(crate) enum SeedFilter {
    #[default]
    Tending,
    Planted,
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

/// The seeds `member` planted under its own key.
pub(crate) fn planted<'a>(seeds: &'a [Arc<Seed>], member: &CrewMember) -> Vec<&'a Seed> {
    let reference = member_ref(&member.id);
    seeds
        .iter()
        .map(Arc::as_ref)
        .filter(|seed| seed.planter.r#ref == reference)
        .collect()
}

/// The plot a seed is part of, when the Garden holds it.
pub(crate) fn plot<'a>(seed: &Seed, seeds: &'a [Arc<Seed>]) -> Option<&'a Seed> {
    let parent = seed.edges.iter().find(|e| e.kind == "part-of")?;
    seeds.iter().map(Arc::as_ref).find(|s| s.id == parent.to)
}

/// A plot's progress as `done/total`, or nothing for a seed that is no plot.
pub(crate) fn progress(seed: Option<&Seed>) -> String {
    match seed.and_then(|s| s.plot_progress.as_ref()) {
        Some(p) if p.total > 0 => format!("{}/{}", p.done, p.total),
        _ => String::new(),
    }
}

/// What the tab says when the filter shows no seed.
pub(crate) fn empty(name: &str, filter: SeedFilter, capped: bool) -> String {
    match filter {
        SeedFilter::Tending => format!(
            "{name} isn't tending a seed{}.",
            if capped {
                " in this Garden snapshot"
            } else {
                ""
            }
        ),
        SeedFilter::Planted => format!(
            "No seeds{} explicitly planted by {name}.",
            if capped {
                " in this Garden snapshot are"
            } else {
                " were"
            }
        ),
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
