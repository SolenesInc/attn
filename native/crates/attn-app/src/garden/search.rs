use attn_protocol::Seed;

/// Values `is:` accepts. Anything else is named back at the reader.
pub(crate) const IS_VALUES: [&str; 7] = [
    "open", "closed", "any", "ready", "blocked", "parked", "standby",
];

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Partial {
    Is,
    Tender,
    Bed,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub(crate) struct Query {
    /// Free-text terms, lowercased. Every term must hit, somewhere.
    pub(crate) terms: Vec<String>,
    /// `is:` values, unioned. Empty means the default lens: open seeds only.
    pub(crate) is: Vec<&'static str>,
    pub(crate) tenders: Vec<String>,
    pub(crate) beds: Vec<String>,
    /// Operator tokens with no such filter, verbatim.
    pub(crate) unknown: Vec<String>,
    /// The operator the reader is mid-way through typing.
    pub(crate) partial: Option<Partial>,
    pub(crate) active: bool,
    /// Text, a tender or a bed searches the subtree; a bare `is:` only filters what is on screen.
    pub(crate) searches: bool,
}

pub(crate) fn parse(raw: &str, bed_op: bool) -> Query {
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
            name.eq_ignore_ascii_case("is")
                || name.eq_ignore_ascii_case("tender")
                || (bed_op && name.eq_ignore_ascii_case("bed"))
        });
        let Some((name, value)) = op else {
            q.terms.push(token.to_lowercase());
            continue;
        };
        let is = name.eq_ignore_ascii_case("is");
        let is_bed = name.eq_ignore_ascii_case("bed");
        let value = value.to_lowercase();
        if value.is_empty() {
            if last {
                q.partial = Some(if is {
                    Partial::Is
                } else if is_bed {
                    Partial::Bed
                } else {
                    Partial::Tender
                });
            } else {
                q.unknown.push((*token).to_owned());
            }
            continue;
        }
        if is_bed {
            q.beds.push(value);
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
    q.searches =
        !q.terms.is_empty() || !q.tenders.is_empty() || !q.beds.is_empty() || !q.unknown.is_empty();
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

/// What a query reads from outside the seed.
pub(crate) trait Lookups {
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

#[cfg(test)]
mod tests {
    use std::collections::HashMap;

    use super::*;

    #[derive(Default)]
    struct Fixed {
        blockers: HashMap<String, usize>,
    }

    impl Lookups for Fixed {
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

    #[test]
    fn separates_operators_from_the_words_that_are_just_words() {
        let q = parse("reconnect is:ready tender:hazel socket", false);
        assert_eq!(q.terms, ["reconnect", "socket"]);
        assert_eq!(q.is, ["ready"]);
        assert_eq!(q.tenders, ["hazel"]);
        assert!(q.unknown.is_empty());
    }

    #[test]
    fn names_a_value_its_operator_does_not_have() {
        let q = parse("is:done", false);
        assert_eq!(q.unknown, ["is:done"]);
        assert!(q.terms.is_empty());
    }

    #[test]
    fn leaves_every_other_colon_alone() {
        let q = parse("GardenPanel.tsx:250", false);
        assert_eq!(q.terms, ["gardenpanel.tsx:250"]);
        assert!(q.unknown.is_empty());
    }

    #[test]
    fn treats_the_operator_being_typed_as_a_request_for_the_values() {
        assert_eq!(parse("is:", false).partial, Some(Partial::Is));
        assert_eq!(parse("is:re", false).partial, Some(Partial::Is));
        assert_eq!(parse("tender:", false).partial, Some(Partial::Tender));
        assert!(parse("is:", false).unknown.is_empty());
    }

    #[test]
    fn stops_forgiving_a_half_typed_value_once_the_reader_moved_past_it() {
        assert_eq!(parse("is:re socket", false).unknown, ["is:re"]);
        assert_eq!(parse("is:re socket", false).partial, None);
    }

    #[test]
    fn separates_a_search_from_a_lens() {
        assert!(parse("is:any", false).active);
        assert!(!parse("is:any", false).searches);
        assert!(parse("socket", false).searches);
        assert!(parse("tender:hazel", false).searches);
        assert!(!parse("   ", false).active);
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

    #[test]
    fn satisfies_lens_matches_open_closed_ready_parked_and_blocked() {
        let open = seed("s-open01", "open work");
        let mut done = seed("s-done01", "finished work");
        done.status = "harvested".into();
        let mut ready = seed("s-redy01", "ready work");
        ready.ready = true;
        let mut parked = seed("s-dorm01", "dormant work");
        parked.status = "parked".into();
        let blocked_seed = seed("s-blok01", "blocked work");
        let blockers = Fixed {
            blockers: HashMap::from([("s-blok01".to_owned(), 1)]),
        };

        assert!(satisfies_lens(&open, &[], &blockers));
        assert!(!satisfies_lens(&done, &[], &blockers));
        assert!(satisfies_lens(&done, &["any"], &blockers));
        assert!(!satisfies_lens(&done, &["open"], &blockers));
        assert!(satisfies_lens(&done, &["closed"], &blockers));
        assert!(satisfies_lens(&ready, &["ready", "parked"], &blockers));
        assert!(satisfies_lens(&parked, &["ready", "parked"], &blockers));
        assert!(!satisfies_lens(&open, &["ready", "parked"], &blockers));
        assert!(satisfies_lens(&blocked_seed, &["blocked"], &blockers));
        assert!(!satisfies_lens(&open, &["blocked"], &blockers));
    }
}
