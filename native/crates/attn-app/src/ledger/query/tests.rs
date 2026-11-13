use attn_protocol::{SessionLedgerFacet, SessionLedgerFacets, SessionLedgerProfileFacet};

use super::*;

fn facets(repositories: &[&str], profiles: &[(&str, &str, bool)]) -> SessionLedgerFacets {
    SessionLedgerFacets {
        repositories: repositories
            .iter()
            .map(|r| SessionLedgerFacet {
                value: (*r).into(),
                count: 1,
            })
            .collect(),
        profiles: profiles
            .iter()
            .map(|(id, name, deleted)| SessionLedgerProfileFacet {
                profile_id: (*id).into(),
                name: (*name).into(),
                deleted: deleted.then_some(true),
                count: 1,
            })
            .collect(),
    }
}

fn standard() -> SessionLedgerFacets {
    facets(
        &["/Users/victor/projects/attn"],
        &[
            ("profile-1", "attn work", false),
            ("profile-old", "Old Side", true),
        ],
    )
}

fn history(f: Option<&SessionLedgerFacets>) -> Vec<ProfileChoice> {
    profile_choices(&[], f, None)
}

fn names(pairs: &[(&str, &str)]) -> Vec<(String, String)> {
    pairs
        .iter()
        .map(|(a, b)| ((*a).into(), (*b).into()))
        .collect()
}

fn filters(range: Range, from: &str, to: &str, profile: &str, repository: &str) -> Filters {
    Filters {
        scope: Scope::All,
        range,
        custom_from: from.into(),
        custom_to: to.into(),
        profile_id: profile.into(),
        repository: repository.into(),
    }
}

#[test]
fn splits_tokens_into_daemon_filters_a_directory_and_words() {
    let f = standard();
    let parsed = parse(
        "repo:attn profile:attn-work 7d dir:~/x Ledger  reopen",
        Some(&f),
        &history(Some(&f)),
        "",
    );
    assert_eq!(
        parsed.filters,
        filters(
            Range::Week,
            "",
            "",
            "profile-1",
            "/Users/victor/projects/attn"
        )
    );
    assert_eq!(parsed.dir, "~/x");
    assert_eq!(parsed.words, ["ledger", "reopen"]);
    assert!(parsed.unresolved.is_empty());
}

#[test]
fn fills_the_missing_end_of_a_custom_range_with_the_other_end() {
    let from = parse("from:2026-09-01", None, &[], "").filters;
    assert_eq!(
        (
            from.range,
            from.custom_from.as_str(),
            from.custom_to.as_str()
        ),
        (Range::Custom, "2026-09-01", "2026-09-01")
    );
    let to = parse("to:2026-09-03", None, &[], "").filters;
    assert_eq!(
        (to.custom_from.as_str(), to.custom_to.as_str()),
        ("2026-09-03", "2026-09-03")
    );
}

#[test]
fn keeps_a_token_the_facets_cannot_name_so_the_user_sees_why_nothing_matched() {
    let f = standard();
    let parsed = parse("repo:nope profile:nobody", Some(&f), &history(Some(&f)), "");
    assert_eq!(parsed.filters.repository, "");
    assert_eq!(parsed.filters.profile_id, "");
    assert_eq!(parsed.unresolved, ["repo:nope", "profile:nobody"]);
}

#[test]
fn finds_profiles_by_live_name_by_history_and_by_id() {
    let f = standard();
    assert_eq!(
        parse("profile:old-side", Some(&f), &history(Some(&f)), "")
            .filters
            .profile_id,
        "profile-old"
    );

    let reused = facets(
        &[],
        &[
            ("profile-old", "Work", true),
            ("profile-new", "Work", false),
        ],
    );
    assert_eq!(
        parse("profile:work", Some(&reused), &history(Some(&reused)), "")
            .filters
            .profile_id,
        "profile-new"
    );
    assert_eq!(
        parse(
            "profile:profile-old",
            Some(&reused),
            &history(Some(&reused)),
            ""
        )
        .filters
        .profile_id,
        "profile-old"
    );
    let twice = facets(
        &[],
        &[("profile-a", "Work", true), ("profile-b", "Work", true)],
    );
    assert_eq!(
        parse("profile:work", Some(&twice), &history(Some(&twice)), "").unresolved,
        ["profile:work"]
    );

    let empty = facets(&["/x"], &[]);
    let live = names(&[("profile-1", "attn work")]);
    assert_eq!(
        parse(
            "profile:attn-work",
            Some(&empty),
            &profile_choices(&live, Some(&empty), None),
            ""
        )
        .filters
        .profile_id,
        "profile-1"
    );
    assert_eq!(
        parse(
            "profile:attn-work",
            None,
            &profile_choices(&live, None, None),
            ""
        )
        .filters
        .profile_id,
        "profile-1"
    );

    let chosen = ProfileChoice {
        profile_id: "profile-old".into(),
        name: "Old Side".into(),
        deleted: true,
    };
    assert_eq!(
        parse(
            "profile:old-side",
            Some(&empty),
            &profile_choices(&[], Some(&empty), Some(&chosen)),
            ""
        )
        .filters
        .profile_id,
        "profile-old"
    );
    assert_eq!(
        profile_choices(&[], Some(&f), Some(&chosen))
            .iter()
            .filter(|c| c.profile_id == "profile-old")
            .count(),
        1
    );
}

#[test]
fn writes_a_profile_by_id_when_its_name_token_is_shared() {
    let f = filters(Range::Any, "", "", "profile-1", "");
    assert_eq!(
        format(
            &f,
            &names(&[("profile-1", "attn work"), ("profile-2", "attn-work")])
        ),
        "profile:profile-1"
    );
    assert_eq!(format(&f, &[]), "profile:profile-1");
}

#[test]
fn resolves_repositories_by_path_base_name_and_the_one_already_chosen() {
    let two = facets(&["/Users/victor/projects/attn", "/tmp/checkout/attn"], &[]);
    assert_eq!(
        parse("repo:attn", Some(&two), &[], "/tmp/checkout/attn")
            .filters
            .repository,
        "/tmp/checkout/attn"
    );
    let one = facets(&["/Users/victor/projects/attn"], &[]);
    assert_eq!(
        parse("repo:attn", Some(&one), &[], "/tmp/checkout/attn")
            .filters
            .repository,
        "/tmp/checkout/attn"
    );
    assert_eq!(
        parse("repo:attn", Some(&two), &[], "").unresolved,
        ["repo:attn"]
    );
    assert_eq!(
        parse("repo:/Users/victor/projects/attn", Some(&two), &[], "")
            .filters
            .repository,
        "/Users/victor/projects/attn"
    );

    let spaced = "/tmp/attn run/ledger-repo";
    let token = format!(
        "repo-path:{}",
        percent_encoding::utf8_percent_encode(spaced, percent_encoding::NON_ALPHANUMERIC)
    );
    assert!(!token.contains(' '));
    assert_eq!(parse(&token, None, &[], "").filters.repository, spaced);

    let scoped = facets(&["/work/@scope"], &[]);
    assert_eq!(
        parse("repo:@scope", Some(&scoped), &[], "/work/@scope")
            .filters
            .repository,
        "/work/@scope"
    );
}

#[test]
fn round_trips_through_format() {
    let f = filters(
        Range::Custom,
        "2026-08-01",
        "2026-08-03",
        "profile-1",
        "/Users/victor/projects/attn",
    );
    let text = format(&f, &names(&[("profile-1", "attn work")]));
    assert_eq!(
        text,
        "repo:attn profile:attn-work from:2026-08-01 to:2026-08-03"
    );
    let s = standard();
    assert_eq!(parse(&text, Some(&s), &history(Some(&s)), "").filters, f);
}

#[test]
fn stamps_relative_time_in_the_fewest_characters_that_still_say_when() {
    let day = crate::calendar::local_day(1_788_000_000_000);
    let now = crate::calendar::local_instant(day, 12 * 3600);
    let ago = |s: i64| relative_stamp(now - s * 1000, now);
    assert_eq!(ago(20), "now");
    assert_eq!(ago(180), "3m");
    assert_eq!(ago(2 * 3600), "2h");
    assert_eq!(
        relative_stamp(crate::calendar::local_instant(day - 1, 3600), now),
        "yesterday"
    );
    assert_eq!(
        relative_stamp(crate::calendar::local_instant(day - 3, 10 * 3600), now),
        "3d"
    );
    assert!(ago(20 * 86_400).contains(' '), "{}", ago(20 * 86_400));
}

#[test]
fn shortens_paths_to_a_home_tilde_and_then_to_the_last_two_components() {
    assert_eq!(tilde_path("/Users/victor/projects/attn"), "~/projects/attn");
    assert_eq!(tilde_path("/home/victor"), "~");
    assert_eq!(short_path("/Users/victor/projects/attn"), "~/projects/attn");
    assert_eq!(
        short_path("/private/tmp/very/long/path/to/some/fixtures/wt/present"),
        "…/wt/present"
    );
    assert_eq!(
        compact_paths("the folder /private/tmp/a/b/wt is gone"),
        "the folder …/b/wt is gone"
    );
}

#[test]
fn rewrites_daemon_prose_so_people_read_titles_never_ids() {
    let title =
        |id: &str| (id == "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee").then(|| "Fixture run".to_owned());
    assert_eq!(
        name_ids(
            "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee is running in it",
            title
        ),
        "Fixture run is running in it"
    );
    assert_eq!(
        name_ids(
            "11111111-2222-3333-4444-555555555555 is running in it",
            title
        ),
        "a session is running in it"
    );
    assert_eq!(
        name_ids(
            "conversation 12345678-1234-1234-1234-123456789abc is gone",
            title
        ),
        "its conversation is gone"
    );
}

#[test]
fn remembers_filters_and_forgets_a_setting_it_cannot_read() {
    let f = filters(Range::Custom, "2026-08-01", "2026-08-03", "profile-1", "/r");
    assert_eq!(Filters::remembered(Some(&f.setting())), f);
    for bad in [
        None,
        Some(""),
        Some("not json"),
        Some(r#"{"scope":"weird","range":"any"}"#),
        Some(r#"{"scope":"all","range":"custom","customFrom":"yesterday"}"#),
    ] {
        assert_eq!(Filters::remembered(bad), Filters::default(), "{bad:?}");
    }
}

#[test]
fn resolves_range_words_into_local_midnights_and_counts_both_ends_of_a_custom_range() {
    let day = crate::calendar::day_of_date("2026-09-06").unwrap();
    let now = crate::calendar::local_instant(day, 15 * 3600);
    let midnight = |d: i64| Some(iso(local_instant(d, 0)));
    let window = |range| filters(range, "", "", "", "").window(now).unwrap();
    assert_eq!(
        window(Range::Today),
        Window {
            since: midnight(day),
            until: None
        }
    );
    assert_eq!(
        window(Range::Yesterday),
        Window {
            since: midnight(day - 1),
            until: midnight(day)
        }
    );
    assert_eq!(window(Range::Week).since, midnight(day - 6));
    assert_eq!(window(Range::Month).since, midnight(day - 29));

    let custom = filters(Range::Custom, "2026-09-01", "2026-09-03", "", "")
        .window(now)
        .unwrap();
    assert_eq!(
        custom,
        Window {
            since: midnight(day - 5),
            until: midnight(day - 2)
        }
    );
    assert_eq!(
        filters(Range::Custom, "2026-09-03", "2026-09-01", "", "").window(now),
        Err("The range ends before it starts; swap the two dates".into())
    );
    assert_eq!(
        filters(Range::Custom, "2026-9-1", "2026-09-01", "", "").window(now),
        Err("2026-9-1 is not a date like 2026-09-05".into())
    );
}
