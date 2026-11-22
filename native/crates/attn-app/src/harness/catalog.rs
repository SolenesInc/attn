//! Reads the daemon's pushed harness catalog (`Facts::harnesses`).

use std::collections::BTreeMap;

use attn_protocol::{Harness, HarnessOptions};

pub(crate) const TERMINAL: &str = "shell";

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Availability {
    Available,
    Checking,
    Unavailable,
}

pub(crate) fn normalize(id: &str) -> String {
    id.trim().to_ascii_lowercase()
}

/// The catalog entry for `id`, whether offered or not.
pub(crate) fn by_id<'a>(harnesses: &'a [Harness], id: &str) -> Option<&'a Harness> {
    let want = normalize(id);
    if want.is_empty() {
        return None;
    }
    harnesses.iter().find(|h| normalize(&h.id) == want)
}

/// Every harness the daemon offers for new sessions, in catalog order.
pub(crate) fn offered(harnesses: &[Harness]) -> impl Iterator<Item = &Harness> {
    harnesses.iter().filter(|h| h.offered)
}

/// `harness`'s installation state as picker and Settings controls show it.
pub(crate) fn availability(harness: &Harness) -> Availability {
    match harness.installation.as_str() {
        "installed" => Availability::Available,
        "checking" => Availability::Checking,
        _ => Availability::Unavailable,
    }
}

pub(crate) fn selectable(harness: &Harness) -> bool {
    harness.offered && availability(harness) != Availability::Unavailable
}

/// Offered harnesses in catalog order, with `preferred` moved first when present.
pub(crate) fn ordered<'a>(harnesses: &'a [Harness], preferred: Option<&str>) -> Vec<&'a Harness> {
    let preferred = preferred.map(normalize).filter(|p| !p.is_empty());
    let mut list: Vec<&Harness> = Vec::new();
    if let Some(ref pref) = preferred
        && let Some(found) = offered(harnesses).find(|h| normalize(&h.id) == *pref)
    {
        list.push(found);
    }
    for h in offered(harnesses) {
        if !list.iter().any(|existing| existing.id == h.id) {
            list.push(h);
        }
    }
    list
}

/// The harness the user picked for new sessions while it is offered and selectable, else empty.
pub(crate) fn picked_default(settings: &BTreeMap<String, String>, harnesses: &[Harness]) -> String {
    let preferred = normalize(settings.get("new_session_agent").map_or("", String::as_str));
    if preferred.is_empty() || preferred == TERMINAL {
        return String::new();
    }
    if by_id(harnesses, &preferred).is_some_and(selectable) {
        preferred
    } else {
        String::new()
    }
}

/// Display name for `id`: the daemon's `Harness.name` when known, else `id` in title-cased words.
pub(crate) fn name(harnesses: &[Harness], id: &str) -> String {
    let normalized = normalize(id);
    if normalized.is_empty() {
        return "Unknown harness".into();
    }
    if let Some(found) = by_id(harnesses, &normalized) {
        let trimmed = found.name.trim();
        if !trimmed.is_empty() {
            return trimmed.to_owned();
        }
    }
    title_case(&normalized)
}

fn title_case(id: &str) -> String {
    id.split(['-', '_', ' '])
        .filter(|part| !part.is_empty())
        .map(|part| {
            let mut chars = part.chars();
            chars
                .next()
                .map(|first| first.to_uppercase().chain(chars).collect::<String>())
                .unwrap_or_default()
        })
        .collect::<Vec<_>>()
        .join(" ")
}

pub(crate) fn options<'a>(harnesses: &'a [Harness], id: &str) -> Option<&'a HarnessOptions> {
    by_id(harnesses, id).map(|h| &h.options)
}
