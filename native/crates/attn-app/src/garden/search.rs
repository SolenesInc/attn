use std::sync::Arc;

use attn_protocol::Seed;

/// Values `is:` accepts. Anything else is named back at the reader.
pub(crate) const IS_VALUES: [&str; 7] = [
    "open", "closed", "any", "ready", "blocked", "parked", "standby",
];

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Partial {
    Is,
    Tender,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub(crate) struct Query {
    /// Free-text terms, lowercased. Every term must hit, somewhere.
    pub(crate) terms: Vec<String>,
    /// `is:` values, unioned. Empty means the default lens: open seeds only.
    pub(crate) is: Vec<&'static str>,
    pub(crate) tenders: Vec<String>,
    /// Operator tokens with no such filter, verbatim.
    pub(crate) unknown: Vec<String>,
    /// The operator the reader is mid-way through typing.
    pub(crate) partial: Option<Partial>,
    pub(crate) active: bool,
    /// Text or a tender searches the subtree; a bare `is:` only filters what is on screen.
    pub(crate) searches: bool,
}

pub(crate) fn parse(raw: &str) -> Query {
    let mut q = Query::default();
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return q;
    }
    let tokens: Vec<&str> = trimmed.split_whitespace().collect();
    let open_end = !raw.ends_with(char::is_whitespace);
    for (i, token) in tokens.iter().enumerate() {
        let last = i == tokens.len() - 1 && open_end;
        let op = token.split_once(':').filter(|(name, _)| {
            name.eq_ignore_ascii_case("is") || name.eq_ignore_ascii_case("tender")
        });
        let Some((name, value)) = op else {
            q.terms.push(token.to_lowercase());
            continue;
        };
        let is = name.eq_ignore_ascii_case("is");
        let value = value.to_lowercase();
        if value.is_empty() {
            if last {
                q.partial = Some(if is { Partial::Is } else { Partial::Tender });
            } else {
                q.unknown.push((*token).to_owned());
            }
            continue;
        }
        if !is {
            q.tenders.push(value);
            continue;
        }
        if let Some(known) = IS_VALUES.iter().find(|v| **v == value) {
            q.is.push(known);
            continue;
        }
        if last && IS_VALUES.iter().any(|v| v.starts_with(&value)) {
            q.partial = Some(Partial::Is);
        } else {
            q.unknown.push((*token).to_owned());
        }
    }
    q.searches = !q.terms.is_empty() || !q.tenders.is_empty() || !q.unknown.is_empty();
    q.active = q.searches || !q.is.is_empty();
    q
}

/// Adds `token` to the raw query, or takes it out when it is there in any case.
pub(crate) fn toggle_token(raw: &str, token: &str) -> String {
    let mut tokens: Vec<&str> = raw.split_whitespace().collect();
    match tokens.iter().position(|t| t.eq_ignore_ascii_case(token)) {
        Some(at) => {
            tokens.remove(at);
        }
        None => tokens.push(token),
    }
    tokens.join(" ")
}

pub(crate) type Range = (usize, usize);

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Snippet {
    pub(crate) text: String,
    pub(crate) ranges: Vec<Range>,
}

#[derive(Clone, Debug)]
pub(crate) struct Match {
    pub(crate) seed: Arc<Seed>,
    /// Lower sorts first: id 0, title 10 and its position, tender 30, body 40.
    rank: usize,
    pub(crate) title_ranges: Vec<Range>,
    /// Only for a body-only hit: the line that says why the row is here.
    pub(crate) snippet: Option<Snippet>,
    pub(crate) id_hit: bool,
}

/// What a query reads from outside the seed.
pub(crate) trait Lookups {
    fn tender_of(&self, seed: &Seed) -> String;
    fn blockers_of(&self, seed: &Seed) -> usize;
}

pub(crate) fn satisfies_lens(seed: &Seed, is: &[&str], lookups: &impl Lookups) -> bool {
    let closed = super::model::is_closed(seed);
    if is.is_empty() {
        return !closed;
    }
    is.iter().any(|value| match *value {
        "any" => true,
        "open" => !closed,
        "closed" => closed,
        "ready" => seed.ready && !closed,
        "blocked" => !closed && lookups.blockers_of(seed) > 0,
        "parked" => seed.status == "parked",
        "standby" => seed.standby.is_some() && !closed,
        _ => false,
    })
}

/// Every occurrence of every term in `haystack`, merged so overlaps highlight once. Offsets are
/// bytes of `haystack`; a string whose lowercase changes its length has none.
fn ranges_of(haystack: &str, terms: &[String]) -> Vec<Range> {
    let lower = haystack.to_lowercase();
    if lower.len() != haystack.len() {
        return Vec::new();
    }
    let mut found: Vec<Range> = Vec::new();
    for term in terms.iter().filter(|t| !t.is_empty()) {
        let mut from = 0;
        while let Some(at) = lower[from..].find(term.as_str()) {
            let start = from + at;
            found.push((start, start + term.len()));
            from = start + term.len();
        }
    }
    found.sort_unstable();
    let mut merged: Vec<Range> = Vec::new();
    for range in found {
        match merged.last_mut() {
            Some(tail) if range.0 <= tail.1 => tail.1 = tail.1.max(range.1),
            _ => merged.push(range),
        }
    }
    merged
}

const SNIPPET_WIDTH: usize = 132;

fn floor_char(s: &str, mut at: usize) -> usize {
    while at > 0 && !s.is_char_boundary(at) {
        at -= 1;
    }
    at
}

/// The stretch of body around the first term it holds, cut before its whitespace collapses.
fn snippet_of(body: &str, body_lower: &str, terms: &[String]) -> Option<Snippet> {
    if body_lower.len() != body.len() {
        return None;
    }
    let at = terms
        .iter()
        .filter_map(|t| body_lower.find(t.as_str()))
        .min()?;
    let from = floor_char(body, at.saturating_sub(SNIPPET_WIDTH / 3));
    let to = floor_char(body, (from + SNIPPET_WIDTH + 40).min(body.len()));
    let window = body[from..to]
        .split_whitespace()
        .collect::<Vec<_>>()
        .join(" ");
    let text = format!(
        "{}{window}{}",
        if from > 0 { "…" } else { "" },
        if to < body.len() { "…" } else { "" }
    );
    let ranges = ranges_of(&text, terms);
    Some(Snippet { text, ranges })
}

pub(crate) fn match_seed(seed: &Arc<Seed>, q: &Query, lookups: &impl Lookups) -> Option<Match> {
    if !q.unknown.is_empty() || !satisfies_lens(seed, &q.is, lookups) {
        return None;
    }
    let tender = lookups.tender_of(seed).to_lowercase();
    if !q.tenders.is_empty()
        && (tender.is_empty() || !q.tenders.iter().any(|name| tender.contains(name.as_str())))
    {
        return None;
    }
    let found = |seed: Arc<Seed>, rank| Match {
        seed,
        rank,
        title_ranges: Vec::new(),
        snippet: None,
        id_hit: false,
    };
    if q.terms.is_empty() {
        return Some(found(seed.clone(), 20));
    }
    let title = seed.title.to_lowercase();
    let id = seed.id.to_lowercase();
    let body = seed.body.to_lowercase();
    let every = |field: &str| q.terms.iter().all(|t| field.contains(t.as_str()));
    let hits = q.terms.iter().all(|t| {
        let t = t.as_str();
        title.contains(t) || id.contains(t) || tender.contains(t) || body.contains(t)
    });
    if !hits {
        return None;
    }
    let (id_hit, title_hit, tender_hit) = (every(&id), every(&title), every(&tender));
    let rank = if id_hit {
        0
    } else if title_hit {
        10 + q
            .terms
            .iter()
            .filter_map(|t| title.find(t.as_str()))
            .min()
            .unwrap_or(0)
    } else if tender_hit {
        30
    } else {
        40
    };
    let mut hit = found(seed.clone(), rank);
    hit.id_hit = id_hit;
    if title_hit || rank == 0 {
        hit.title_ranges = ranges_of(&seed.title, &q.terms);
    }
    if rank == 40 {
        hit.snippet = snippet_of(&seed.body, &body, &q.terms);
    }
    Some(hit)
}

/// Every seed in `pool` that answers the query, best first; ties keep the pool's order.
pub(crate) fn search<'a>(
    pool: impl IntoIterator<Item = &'a Arc<Seed>>,
    q: &Query,
    lookups: &impl Lookups,
) -> Vec<Match> {
    let mut matches: Vec<Match> = pool
        .into_iter()
        .filter_map(|seed| match_seed(seed, q, lookups))
        .collect();
    matches.sort_by_key(|m| m.rank);
    matches
}

#[cfg(test)]
mod tests {
    use std::collections::HashMap;

    use attn_protocol::PartyView;

    use super::*;

    #[derive(Default)]
    struct Fixed {
        blockers: HashMap<String, usize>,
    }

    impl Lookups for Fixed {
        fn tender_of(&self, seed: &Seed) -> String {
            seed.tender
                .as_ref()
                .map(|t| t.name.clone())
                .unwrap_or_default()
        }
        fn blockers_of(&self, seed: &Seed) -> usize {
            self.blockers.get(&seed.id).copied().unwrap_or(0)
        }
    }

    fn seed(id: &str, title: &str) -> Seed {
        Seed {
            id: id.into(),
            title: title.into(),
            status: "planted".into(),
            ..Seed::default()
        }
    }

    fn with(mut seed: Seed, f: impl FnOnce(&mut Seed)) -> Arc<Seed> {
        f(&mut seed);
        Arc::new(seed)
    }

    fn hazel(seed: &mut Seed) {
        seed.tender = Some(PartyView {
            name: "Hazel".into(),
            r#ref: "member:hazel".into(),
            session_id: None,
        });
        seed.claimed = true;
    }

    fn titles(seeds: &[Arc<Seed>], raw: &str, lookups: &Fixed) -> Vec<String> {
        search(seeds, &parse(raw), lookups)
            .into_iter()
            .map(|m| m.seed.title.clone())
            .collect()
    }

    #[test]
    fn separates_operators_from_the_words_that_are_just_words() {
        let q = parse("reconnect is:ready tender:hazel socket");
        assert_eq!(q.terms, ["reconnect", "socket"]);
        assert_eq!(q.is, ["ready"]);
        assert_eq!(q.tenders, ["hazel"]);
        assert!(q.unknown.is_empty());
    }

    #[test]
    fn names_a_value_its_operator_does_not_have() {
        let q = parse("is:done");
        assert_eq!(q.unknown, ["is:done"]);
        assert!(q.terms.is_empty());
    }

    #[test]
    fn leaves_every_other_colon_alone() {
        let q = parse("GardenPanel.tsx:250");
        assert_eq!(q.terms, ["gardenpanel.tsx:250"]);
        assert!(q.unknown.is_empty());
    }

    #[test]
    fn treats_the_operator_being_typed_as_a_request_for_the_values() {
        assert_eq!(parse("is:").partial, Some(Partial::Is));
        assert_eq!(parse("is:re").partial, Some(Partial::Is));
        assert_eq!(parse("tender:").partial, Some(Partial::Tender));
        assert!(parse("is:").unknown.is_empty());
    }

    #[test]
    fn stops_forgiving_a_half_typed_value_once_the_reader_moved_past_it() {
        assert_eq!(parse("is:re socket").unknown, ["is:re"]);
        assert_eq!(parse("is:re socket").partial, None);
    }

    #[test]
    fn separates_a_search_from_a_lens() {
        assert!(parse("is:any").active);
        assert!(!parse("is:any").searches);
        assert!(parse("socket").searches);
        assert!(parse("tender:hazel").searches);
        assert!(!parse("   ").active);
    }

    #[test]
    fn toggles_a_token_and_leaves_the_rest_of_the_query_alone() {
        assert_eq!(toggle_token("socket", "is:any"), "socket is:any");
        assert_eq!(toggle_token("socket is:any", "is:any"), "socket");
        assert_eq!(toggle_token("", "is:any"), "is:any");
        assert_eq!(
            toggle_token("socket IS:ANY reconnect", "is:any"),
            "socket reconnect"
        );
    }

    fn lens_pool() -> Vec<Arc<Seed>> {
        vec![
            Arc::new(seed("s-open01", "open work")),
            with(seed("s-done01", "finished work"), |s| {
                s.status = "harvested".into()
            }),
            with(seed("s-drop01", "abandoned work"), |s| {
                s.status = "withered".into()
            }),
            with(seed("s-redy01", "ready work"), |s| s.ready = true),
            with(seed("s-dorm01", "dormant work"), |s| {
                s.status = "parked".into()
            }),
        ]
    }

    #[test]
    fn hides_closed_seeds_until_a_token_asks_for_them() {
        let all = lens_pool();
        let none = Fixed::default();
        assert_eq!(
            titles(&all, "work", &none),
            ["open work", "ready work", "dormant work"]
        );
        assert_eq!(titles(&all, "work is:any", &none).len(), 5);
        assert_eq!(
            titles(&all, "work is:closed", &none),
            ["finished work", "abandoned work"]
        );
    }

    #[test]
    fn unions_several_lenses_rather_than_intersecting_them() {
        assert_eq!(
            titles(&lens_pool(), "work is:ready is:parked", &Fixed::default()),
            ["ready work", "dormant work"]
        );
    }

    #[test]
    fn counts_a_seed_blocked_only_while_something_open_blocks_it() {
        let mut all = lens_pool();
        all.push(Arc::new(seed("s-blok01", "blocked work")));
        let blocked = Fixed {
            blockers: HashMap::from([("s-blok01".to_owned(), 1)]),
        };
        assert_eq!(titles(&all, "work is:blocked", &blocked), ["blocked work"]);
    }

    #[test]
    fn is_the_same_predicate_the_panel_filters_a_plain_list_with() {
        let done = with(seed("s-done01", "finished work"), |s| {
            s.status = "harvested".into()
        });
        let none = Fixed::default();
        assert!(!satisfies_lens(&done, &[], &none));
        assert!(satisfies_lens(&done, &["any"], &none));
        assert!(!satisfies_lens(&done, &["open"], &none));
    }

    fn matching_pool() -> Vec<Arc<Seed>> {
        vec![
            with(seed("s-abc123", "reconnect the socket"), |s| {
                s.body = "the daemon drops the socket".into()
            }),
            with(seed("s-def456", "garden search"), |s| {
                s.body = "search over the pushed snapshot".into()
            }),
            with(seed("s-ghi789", "unrelated"), |s| {
                s.body = "nothing to do with any of it".into();
                hazel(s);
            }),
        ]
    }

    #[test]
    fn looks_in_the_title_the_body_the_id_and_the_tender() {
        let seeds = matching_pool();
        let none = Fixed::default();
        assert_eq!(titles(&seeds, "socket", &none), ["reconnect the socket"]);
        assert_eq!(titles(&seeds, "snapshot", &none), ["garden search"]);
        assert_eq!(titles(&seeds, "s-ghi789", &none), ["unrelated"]);
        assert_eq!(titles(&seeds, "hazel", &none), ["unrelated"]);
    }

    #[test]
    fn requires_every_term_to_hit_though_not_all_in_the_same_place() {
        let seeds = matching_pool();
        let none = Fixed::default();
        assert_eq!(
            titles(&seeds, "socket daemon", &none),
            ["reconnect the socket"]
        );
        assert!(titles(&seeds, "socket garden", &none).is_empty());
    }

    #[test]
    fn ranks_an_id_over_a_title_a_title_over_a_tender_and_a_tender_over_a_body() {
        let pool = vec![
            with(seed("s-body01", "only in the body"), |s| {
                s.body = "a passing mention of hazel".into()
            }),
            with(seed("s-tend01", "nothing either"), hazel),
            Arc::new(seed("s-titl01", "hazel plants a thing")),
            Arc::new(seed("s-hazel1", "nothing at all")),
        ];
        assert_eq!(
            titles(&pool, "hazel", &Fixed::default()),
            [
                "nothing at all",
                "hazel plants a thing",
                "nothing either",
                "only in the body"
            ]
        );
    }

    #[test]
    fn breaks_ties_by_keeping_the_order_the_garden_pushed() {
        let pool = vec![
            Arc::new(seed("s-one001", "socket one")),
            Arc::new(seed("s-two002", "socket two")),
        ];
        assert_eq!(
            titles(&pool, "socket", &Fixed::default()),
            ["socket one", "socket two"]
        );
    }

    #[test]
    fn gives_a_snippet_only_to_a_row_whose_title_does_not_explain_itself() {
        let pool = matching_pool();
        let none = Fixed::default();
        let body_hit = match_seed(&pool[1], &parse("pushed"), &none).unwrap();
        let title_hit = match_seed(&pool[1], &parse("garden"), &none).unwrap();
        assert!(body_hit.snippet.unwrap().text.contains("pushed"));
        assert!(title_hit.snippet.is_none());
        assert_eq!(title_hit.title_ranges, [(0, 6)]);
    }

    #[test]
    fn collapses_the_whitespace_of_the_snippet_it_cuts() {
        let wordy = with(seed("s-wrap01", "nothing"), |s| {
            s.body =
                "a paragraph\n\n    that wraps around the\n    word beacon and keeps going".into()
        });
        let text = match_seed(&wordy, &parse("beacon"), &Fixed::default())
            .unwrap()
            .snippet
            .unwrap()
            .text;
        let chars: Vec<char> = text.chars().collect();
        assert!(
            !chars
                .windows(2)
                .any(|w| w[0].is_whitespace() && w[1].is_whitespace())
        );
        assert!(text.contains("word beacon and"));
    }

    #[test]
    fn finds_nothing_when_the_query_names_a_filter_that_does_not_exist() {
        assert!(titles(&matching_pool(), "socket is:done", &Fixed::default()).is_empty());
    }

    #[test]
    fn merges_overlapping_hits_so_a_highlight_never_doubles_up() {
        let seed = Arc::new(seed("s-ovl001", "searching search"));
        let hit = match_seed(&seed, &parse("sea search"), &Fixed::default()).unwrap();
        assert_eq!(hit.title_ranges, [(0, 6), (10, 16)]);
    }
}
