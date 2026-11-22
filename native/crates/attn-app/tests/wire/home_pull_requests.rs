use std::time::Duration;

use attn_app::Action;
use attn_protocol::{
    AuthorState, AuthorsUpdatedMessage, CreateWorktreeResultMessage, EnsureRepoResultMessage,
    FetchPRDetailsResultMessage, GitHubHostsUpdatedMessage, InitialStateMessage, PR,
    PRActionResultMessage, PRsUpdatedMessage, RateLimitedMessage, RefreshPRsResultMessage,
    RepoState, ReposUpdatedMessage, SpawnResultMessage,
};
use attn_testkit::{Answer, Target, World, message};
use rune::{Key, KeyEvent, Mods};

use super::queue::one_desktop;
use super::sidebar::chord;

const MERGE: &str = "home.prs.merge-dialog";

fn pr(id: &str, number: i64, role: &str, title: &str) -> PR {
    PR {
        id: id.into(),
        number,
        role: role.into(),
        title: title.into(),
        repo: "victorarias/attn".into(),
        author: if role == "author" { "me" } else { "someone" }.into(),
        reason: if role == "author" {
            "ready_to_merge"
        } else {
            "review_requested"
        }
        .into(),
        url: format!("https://github.com/victorarias/attn/pull/{number}"),
        host: "github.com".into(),
        state: "waiting".into(),
        ..PR::default()
    }
}

fn start(mut state: InitialStateMessage, prs: Vec<PR>) -> World {
    state.prs = Some(prs);
    let daemon = super::palette::daemon(state, vec![]);
    daemon.on("mute_pr", |_| ());
    daemon.on("refresh_prs", |_| ());
    World::start(daemon)
}

fn answering(world: &World, cmd: &'static str, action: &'static str, error: Option<&'static str>) {
    world.daemon.on(cmd, move |r| {
        message(PRActionResultMessage {
            action: action.into(),
            id: r["id"].as_str().unwrap().into(),
            success: error.is_none(),
            error: error.map(Into::into),
            ..PRActionResultMessage::new()
        })
    });
}

fn has(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

fn name(world: &World, key: &str) -> Option<String> {
    world.screen().find(Target::key(key)).and_then(|n| n.name)
}

fn description(world: &World, key: &str) -> Option<String> {
    world
        .screen()
        .find(Target::key(key))
        .and_then(|n| n.description)
}

fn press(world: &mut World, action: Action) {
    world.gesture(|app| app.key(chord(action)));
}

fn focused(world: &World) -> Option<String> {
    world.screen().focused().and_then(|n| n.key)
}

fn click(world: &mut World, key: &str) {
    world.gesture(|app| app.click(Target::key(key)));
}

fn prs(world: &World) -> Vec<String> {
    home_listed(world, "home.prs")
}

#[test]
fn home_drops_prs_the_moment_their_repo_or_author_is_muted() {
    let mut world = start(
        one_desktop(vec![]),
        vec![
            PR {
                repo: "org/noisy".into(),
                ..pr("p1", 1, "reviewer", "from the noisy repo")
            },
            PR {
                author: "chatty-bot".into(),
                ..pr("p2", 2, "reviewer", "from the noisy author")
            },
            PR {
                muted: true,
                ..pr("p3", 3, "reviewer", "muted on its own")
            },
            pr("p4", 4, "reviewer", "still wanted"),
        ],
    );
    assert_eq!(prs(&world), ["p1", "p2", "p4"]);

    world.daemon.emit(ReposUpdatedMessage {
        repos: Some(vec![RepoState {
            repo: "org/noisy".into(),
            muted: true,
            collapsed: false,
        }]),
        ..ReposUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(prs(&world), ["p2", "p4"]);

    world.daemon.emit(AuthorsUpdatedMessage {
        authors: Some(vec![AuthorState {
            author: "chatty-bot".into(),
            muted: true,
        }]),
        ..AuthorsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(prs(&world), ["p4"]);
}

#[test]
fn approve_shows_its_check_for_a_moment_and_mute_sends_the_pr() {
    let mut world = start(
        one_desktop(vec![]),
        vec![pr("p1", 1, "reviewer", "review this")],
    );
    answering(&world, "approve_pr", "approve", None);

    click(&mut world, "home.pr.p1.approve");
    assert_eq!(world.daemon.sent_of("approve_pr").len(), 1);
    assert_eq!(world.daemon.sent_of("approve_pr")[0]["id"], "p1");
    assert_eq!(
        name(&world, "home.pr.p1.approve").as_deref(),
        Some("Approved")
    );

    click(&mut world, "home.pr.p1.approve");
    assert_eq!(world.daemon.sent_of("approve_pr").len(), 1);

    world.advance(Duration::from_millis(1600));
    assert_eq!(
        name(&world, "home.pr.p1.approve").as_deref(),
        Some("Approve")
    );
    click(&mut world, "home.pr.p1.mute");
    assert_eq!(world.daemon.sent_of("mute_pr").len(), 1);
    assert_eq!(world.daemon.sent_of("mute_pr")[0]["id"], "p1");
}

#[test]
fn a_muted_pr_leaves_at_once_so_mute_cannot_be_pressed_twice() {
    let mut world = start(
        one_desktop(vec![]),
        vec![
            pr("p1", 1, "reviewer", "noisy"),
            pr("p2", 2, "reviewer", "wanted"),
        ],
    );
    click(&mut world, "home.pr.p1.mute");
    assert_eq!(prs(&world), ["p2"]);
    assert_eq!(world.daemon.sent_of("mute_pr").len(), 1);

    let update = |p1_muted: bool| PRsUpdatedMessage {
        prs: Some(vec![
            PR {
                muted: p1_muted,
                ..pr("p1", 1, "reviewer", "noisy")
            },
            pr("p2", 2, "reviewer", "wanted"),
        ]),
        ..PRsUpdatedMessage::new()
    };
    world.daemon.emit(update(true));
    world.settle();
    world.daemon.emit(update(false));
    world.settle();
    assert_eq!(prs(&world), ["p1", "p2"]);
}

#[test]
fn a_refused_approve_says_why_and_can_be_tried_again() {
    let mut world = start(
        one_desktop(vec![]),
        vec![pr("p1", 1, "reviewer", "review this")],
    );
    answering(
        &world,
        "approve_pr",
        "approve",
        Some("review is not allowed on your own PR"),
    );

    click(&mut world, "home.pr.p1.approve");
    assert_eq!(
        name(&world, "notice.text").as_deref(),
        Some("review is not allowed on your own PR")
    );

    click(&mut world, "home.pr.p1.approve");
    assert_eq!(world.daemon.sent_of("approve_pr").len(), 2);
}

#[test]
fn merge_asks_first_and_squashes_on_yes() {
    let mut world = start(one_desktop(vec![]), vec![pr("a1", 7, "author", "ship it")]);
    answering(&world, "merge_pr", "merge", None);

    click(&mut world, "home.pr.a1.merge");
    assert_eq!(name(&world, MERGE).as_deref(), Some("Merge PR #7?"));
    click(&mut world, "home.prs.merge.cancel");
    assert!(!has(&world, MERGE));
    assert_eq!(focused(&world).as_deref(), Some("home.pr.a1.merge"));
    assert!(world.daemon.sent_of("merge_pr").is_empty());

    click(&mut world, "home.pr.a1.merge");
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Escape,
            mods: Mods::default(),
        })
    });
    assert!(!has(&world, MERGE));
    assert!(world.daemon.sent_of("merge_pr").is_empty());

    click(&mut world, "home.pr.a1.merge");
    click(&mut world, "home.prs.merge.confirm");
    assert!(!has(&world, MERGE));
    let sent = world.daemon.sent_of("merge_pr");
    assert_eq!(sent.len(), 1);
    assert_eq!(
        (&sent[0]["id"], &sent[0]["method"]),
        (&"a1".into(), &"squash".into())
    );
}

#[test]
fn a_refresh_shows_dots_until_github_answers_and_a_failed_one_says_why_on_its_button() {
    let mut world = start(one_desktop(vec![]), vec![]);
    press(&mut world, Action::SessionGoToDashboard);
    let idle_help = description(&world, "home.prs.refresh");
    assert!(
        idle_help
            .as_deref()
            .is_some_and(|h| h.starts_with("Refresh PRs"))
    );

    world.gesture(|app| app.key(chord(Action::SessionRefreshPrs)));
    assert_eq!(world.daemon.sent_of("refresh_prs").len(), 1);
    let button = world.screen().get(Target::key("home.prs.refresh"));
    assert_eq!(button.name.as_deref(), Some("Refreshing PRs"));
    assert!(button.is("disabled"));
    world.gesture(|app| app.key(chord(Action::SessionRefreshPrs)));
    assert_eq!(world.daemon.sent_of("refresh_prs").len(), 1);

    world.daemon.emit(RefreshPRsResultMessage {
        success: false,
        error: Some("gh is not signed in".into()),
        ..RefreshPRsResultMessage::new()
    });
    world.settle();
    assert_eq!(
        name(&world, "home.prs.refresh").as_deref(),
        Some("Refresh PRs")
    );
    assert_eq!(
        description(&world, "home.prs.refresh").as_deref(),
        Some("gh is not signed in")
    );
    assert!(!has(&world, "notice.text"));

    click(&mut world, "home.prs.refresh");
    assert_eq!(world.daemon.sent_of("refresh_prs").len(), 2);
    world.daemon.emit(RefreshPRsResultMessage {
        success: true,
        ..RefreshPRsResultMessage::new()
    });
    world.settle();
    assert_eq!(description(&world, "home.prs.refresh"), idle_help);
}

#[test]
fn a_running_refresh_keeps_its_buttons_box_and_help_so_the_pointer_still_finds_it() {
    let mut world = start(one_desktop(vec![]), vec![]);
    press(&mut world, Action::SessionGoToDashboard);
    let idle = world
        .screen()
        .bounds(Target::key("home.prs.refresh"))
        .unwrap();
    let idle_help = description(&world, "home.prs.refresh");

    world.gesture(|app| app.key(chord(Action::SessionRefreshPrs)));
    let screen = world.screen();
    assert_eq!(
        screen.bounds(Target::key("home.prs.refresh")).unwrap(),
        idle,
        "the running refresh stands in the idle button's box"
    );
    assert_eq!(description(&world, "home.prs.refresh"), idle_help);
}

#[test]
fn the_refresh_dots_bounce_in_scale_and_opacity() {
    let mut world = start(one_desktop(vec![]), vec![]);
    press(&mut world, Action::SessionGoToDashboard);
    let refreshing = world.record(60.0, 3.0, |_, app| {
        app.key(chord(Action::SessionRefreshPrs))
    });
    let dots: Vec<_> = refreshing
        .tracks
        .iter()
        .filter(|t| t.path.contains("home.prs.refresh/") && t.frames.len() > 60)
        .filter(|t| t.frames.iter().any(|(_, s)| s.scale < 0.99))
        .collect();
    assert_eq!(dots.len(), 3, "three dots bounce");
    for dot in dots {
        let (scale, opacity): (Vec<f32>, Vec<f32>) =
            dot.frames.iter().map(|(_, s)| (s.scale, s.op)).unzip();
        let span = |v: &[f32]| {
            v.iter()
                .copied()
                .fold((f32::MAX, f32::MIN), |(lo, hi), x| (lo.min(x), hi.max(x)))
        };
        let (small, full) = span(&scale);
        assert!(
            (small - 0.6).abs() < 0.02 && (full - 1.0).abs() < 0.02,
            "{}: scale {small}..{full}",
            dot.path
        );
        let (faint, solid) = span(&opacity[opacity.len() / 2..]);
        assert!(
            faint < 0.55 && solid > 0.95,
            "{}: opacity {faint}..{solid}",
            dot.path
        );
    }
}

#[test]
fn each_pr_opens_with_its_role_and_names_its_host_when_its_repo_spans_two() {
    let mut world = start(
        one_desktop(vec![]),
        vec![
            pr("mine", 1, "author", "my change"),
            pr("asked", 2, "reviewer", "a person asks"),
            PR {
                author: "Dependabot[bot]".into(),
                ..pr("bot", 3, "reviewer", "a bot asks")
            },
            PR {
                host: "ghe.corp.example".into(),
                ..pr("enterprise", 4, "reviewer", "the same repo on another host")
            },
            home_pr("other", 5, "reviewer", "org/elsewhere", "one host only"),
        ],
    );
    press(&mut world, Action::SessionGoToDashboard);
    let role = |id: &str| name(&world, &format!("home.pr.{id}.role"));
    let open = |world: &World, id: &str| name(world, &format!("home.pr.{id}.open"));
    assert_eq!(role("mine").as_deref(), Some("Yours"));
    assert_eq!(role("asked").as_deref(), Some("Review requested"));
    assert_eq!(role("bot").as_deref(), Some("Review requested by a bot"));
    assert_eq!(
        open(&world, "asked").as_deref(),
        Some("#2 on github.com: a person asks")
    );
    assert_eq!(
        open(&world, "enterprise").as_deref(),
        Some("#4 on ghe.corp.example: the same repo on another host")
    );
    assert_eq!(open(&world, "other").as_deref(), Some("#5: one host only"));

    world.daemon.emit(PRsUpdatedMessage {
        prs: Some(vec![
            pr("asked", 2, "reviewer", "a person asks"),
            home_pr("other", 5, "reviewer", "org/elsewhere", "one host only"),
        ]),
        ..PRsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(open(&world, "asked").as_deref(), Some("#2: a person asks"));
}

#[test]
fn home_counts_down_to_when_github_lets_the_daemon_poll_again() {
    let mut world = start(one_desktop(vec![]), vec![]);
    press(&mut world, Action::SessionGoToDashboard);
    let banner = |world: &World| name(world, "home.rate-limit");
    world.daemon.emit(RateLimitedMessage {
        rate_limit_resource: "search".into(),
        rate_limit_reset_at: "2026-11-12T08:59:00Z".into(),
        ..RateLimitedMessage::new()
    });
    world.settle();
    assert_eq!(banner(&world), None);

    world.daemon.emit(RateLimitedMessage {
        rate_limit_resource: "core".into(),
        rate_limit_reset_at: "2026-11-12T09:02:05Z".into(),
        ..RateLimitedMessage::new()
    });
    world.settle();
    assert_eq!(
        banner(&world).as_deref(),
        Some("GitHub rate limited. Resuming in 2m 5s")
    );
    world.advance(Duration::from_secs(1));
    assert_eq!(
        banner(&world).as_deref(),
        Some("GitHub rate limited. Resuming in 2m 4s")
    );
    world.advance(Duration::from_secs(124));
    assert_eq!(banner(&world), None);
}

fn home_pr(id: &str, number: i64, role: &str, repo: &str, title: &str) -> PR {
    PR {
        repo: repo.into(),
        ..pr(id, number, role, title)
    }
}

fn home_listed(world: &World, within: &str) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key(within))
        .into_iter()
        .filter_map(|n| n.key?.strip_prefix("home.pr.").map(str::to_owned))
        .filter(|rest| !rest.contains('.'))
        .collect()
}

#[test]
fn home_lists_the_users_prs_by_repo_name_then_review_requests_grouped_by_repo() {
    let mut world = start(
        one_desktop(vec![]),
        vec![
            home_pr("r1", 10, "reviewer", "victorarias/attn", "review this"),
            home_pr("a1", 20, "author", "victorarias/attn", "my attn change"),
            home_pr("a2", 21, "author", "other/tool", "my tool change"),
        ],
    );
    world.daemon.on("mute_repo", |_| ());
    let screen = world.screen();
    let cards = screen.bounds(Target::key("home.cards")).unwrap();
    let sessions = screen.bounds(Target::key("home.sessions")).unwrap();
    let prs = screen.bounds(Target::key("home.prs")).unwrap();
    assert!(
        prs[0] >= sessions[0] + sessions[2],
        "side by side: {sessions:?} {prs:?} in {cards:?}"
    );

    assert_eq!(home_listed(&world, "home.prs.yours"), ["a1", "a2"]);
    assert_eq!(
        name(&world, "home.pr.a1.open").as_deref(),
        Some("attn #20: my attn change")
    );
    assert_eq!(
        name(&world, "home.pr.a2.open").as_deref(),
        Some("tool #21: my tool change")
    );
    assert_eq!(
        home_listed(&world, "home.prs.repo.victorarias/attn"),
        ["r1"]
    );
    assert_eq!(
        name(&world, "home.pr.r1.open").as_deref(),
        Some("#10: review this")
    );
    assert_eq!(
        name(&world, "home.prs.repo.victorarias/attn.fold").as_deref(),
        Some("attn, 1 review")
    );
    assert_eq!(
        name(&world, "home.prs.count").as_deref(),
        Some("3 need attention")
    );

    click(&mut world, "home.prs.repo.victorarias/attn.fold");
    assert!(home_listed(&world, "home.prs.repo.victorarias/attn").is_empty());
    click(&mut world, "home.prs.repo.victorarias/attn.mute");
    let muted = world.daemon.sent_of("mute_repo");
    assert_eq!(muted.len(), 1);
    assert_eq!(muted[0]["repo"], "victorarias/attn");

    world.resize((900.0, 900.0));
    let screen = world.screen();
    let sessions = screen.bounds(Target::key("home.sessions")).unwrap();
    let prs = screen.bounds(Target::key("home.prs")).unwrap();
    assert!(
        prs[1] >= sessions[1] + sessions[3],
        "stacked when narrow: {sessions:?} {prs:?}"
    );
}

#[test]
fn home_counts_only_prs_needing_the_user_and_takes_a_merged_one_off_after_its_check() {
    let approved = PR {
        approved_by_me: true,
        ..pr("p1", 1, "reviewer", "approved already")
    };
    let mut world = start(
        one_desktop(vec![]),
        vec![approved.clone(), pr("a1", 7, "author", "ship it")],
    );
    answering(&world, "merge_pr", "merge", None);
    assert_eq!(
        name(&world, "home.prs.count").as_deref(),
        Some("1 need attention")
    );
    assert!(has(&world, "home.pr.p1"));

    world.daemon.emit(PRsUpdatedMessage {
        prs: Some(vec![
            PR {
                has_new_changes: true,
                ..approved
            },
            pr("a1", 7, "author", "ship it"),
        ]),
        ..PRsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(
        name(&world, "home.prs.count").as_deref(),
        Some("2 need attention")
    );

    click(&mut world, "home.pr.a1.merge");
    click(&mut world, "home.prs.merge.cancel");
    assert_eq!(focused(&world).as_deref(), Some("home.pr.a1.merge"));
    click(&mut world, "home.pr.a1.merge");
    click(&mut world, "home.prs.merge.confirm");
    assert!(has(&world, "home.pr.a1"));
    world.advance(Duration::from_millis(1600));
    assert!(!has(&world, "home.pr.a1"));
}

#[test]
fn home_leaves_out_an_empty_section_and_says_when_no_pr_needs_the_user_or_github_is_not_polled() {
    let mut world = start(
        one_desktop(vec![]),
        vec![pr("r1", 1, "reviewer", "review this")],
    );
    assert!(has(&world, "home.prs.review"));
    assert!(!has(&world, "home.prs.yours"));
    assert!(!has(&world, "home.prs.empty"));

    world.daemon.emit(PRsUpdatedMessage {
        prs: Some(vec![]),
        ..PRsUpdatedMessage::new()
    });
    world.settle();
    assert!(!has(&world, "home.prs.review"));
    assert_eq!(
        name(&world, "home.prs.empty").as_deref(),
        Some("No PRs need attention")
    );
    let one_line = world
        .screen()
        .bounds(Target::key("home.prs.empty"))
        .unwrap()[3];
    world.daemon.emit(PRsUpdatedMessage {
        prs: Some(vec![pr("r1", 1, "reviewer", "review this")]),
        ..PRsUpdatedMessage::new()
    });
    let reason = "GitHub polling is off for instance vm-mac-check-b8e4. Start its daemon with \
                  ATTN_GITHUB_POLLING=on to poll with your gh credentials.";
    world.daemon.emit(GitHubHostsUpdatedMessage {
        github_polling_off_reason: Some(reason.into()),
        ..GitHubHostsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(name(&world, "home.prs.empty").as_deref(), Some(reason));
    assert!(
        !has(&world, "home.prs.count"),
        "no count beside a list it does not show"
    );
    let said = world
        .screen()
        .bounds(Target::key("home.prs.empty"))
        .unwrap();
    assert!(
        said[3] > one_line,
        "the reason wraps onto more lines inside the card: {said:?}"
    );
}

#[test]
fn opening_a_pr_from_home_marks_it_visited_so_the_daemon_clears_its_new_changes() {
    let updated = PR {
        approved_by_me: true,
        has_new_changes: true,
        ..pr("p1", 1, "reviewer", "changed since approval")
    };
    let mut world = start(one_desktop(vec![]), vec![updated.clone()]);
    world.daemon.on("pr_visited", |_| ());
    assert_eq!(
        name(&world, "home.prs.count").as_deref(),
        Some("1 need attention")
    );

    click(&mut world, "home.pr.p1.role");
    let visited = world.daemon.sent_of("pr_visited");
    assert_eq!(visited.len(), 1);
    assert_eq!(visited[0]["id"], "p1");
    assert_eq!(
        world.opened_links(),
        ["https://github.com/victorarias/attn/pull/1"]
    );

    world.daemon.emit(PRsUpdatedMessage {
        prs: Some(vec![PR {
            has_new_changes: false,
            ..updated
        }]),
        ..PRsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(
        name(&world, "home.prs.count").as_deref(),
        Some("0 need attention")
    );
    assert!(!has(&world, "home.pr.p1.updated"));
}

#[test]
fn home_marks_each_pr_updated_approved_and_with_its_ci() {
    let world = start(
        one_desktop(vec![]),
        vec![
            PR {
                has_new_changes: true,
                ci_status: Some("failure".into()),
                ..pr("u1", 1, "reviewer", "moved")
            },
            PR {
                approved_by_me: true,
                ci_status: Some("success".into()),
                ..pr("a1", 2, "reviewer", "approved")
            },
            PR {
                ci_status: Some("pending".into()),
                ..pr("y1", 3, "author", "mine")
            },
            PR {
                ci_status: Some("none".into()),
                ..pr("n1", 4, "reviewer", "no ci")
            },
        ],
    );
    let marks = |id: &str| {
        ["updated", "approved", "ci"]
            .into_iter()
            .filter_map(|mark| name(&world, &format!("home.pr.{id}.{mark}")))
            .collect::<Vec<_>>()
    };
    assert_eq!(marks("u1"), ["Updated", "CI failure"]);
    assert_eq!(marks("a1"), ["Approved", "CI success"]);
    assert_eq!(marks("y1"), ["CI pending"]);
    assert!(marks("n1").is_empty());
}

#[test]
fn home_mutes_every_pr_by_an_author_from_one_of_their_prs() {
    let mut world = start(
        one_desktop(vec![]),
        vec![
            PR {
                author: "chatty-bot".into(),
                ..pr("b1", 1, "reviewer", "bump a")
            },
            PR {
                author: "chatty-bot".into(),
                ..pr("b2", 2, "reviewer", "bump b")
            },
            pr("r1", 3, "reviewer", "real work"),
        ],
    );
    world.daemon.on("mute_author", |_| ());
    assert_eq!(
        name(&world, "home.pr.b1.mute-author").as_deref(),
        Some("Mute all PRs by chatty-bot")
    );

    click(&mut world, "home.pr.b1.mute-author");
    let muted = world.daemon.sent_of("mute_author");
    assert_eq!(muted.len(), 1);
    assert_eq!(muted[0]["author"], "chatty-bot");

    world.daemon.emit(AuthorsUpdatedMessage {
        authors: Some(vec![AuthorState {
            author: "chatty-bot".into(),
            muted: true,
        }]),
        ..AuthorsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(
        home_listed(&world, "home.prs.repo.victorarias/attn"),
        ["r1"]
    );
}

/// The harness the user picked in an earlier new-session picker.
const LAST_PICK: (&str, &str) = ("new_session_agent", "claude");

fn with_projects(state: InitialStateMessage) -> InitialStateMessage {
    with_projects_and(state, &[LAST_PICK])
}

fn with_projects_and(mut state: InitialStateMessage, more: &[(&str, &str)]) -> InitialStateMessage {
    state.settings = Some(
        std::iter::once(("projects_directory", "/home/me/projects/"))
            .chain(more.iter().copied())
            .map(|(k, v)| (k.to_owned(), v.to_owned()))
            .collect(),
    );
    state
}

fn ensured(request: &attn_testkit::Request, success: bool) -> serde_json::Value {
    message(EnsureRepoResultMessage {
        success: Some(success),
        error: (!success).then(|| "clone failed: no access".into()),
        target_path: request["target_path"].as_str().map(str::to_owned),
        ..EnsureRepoResultMessage::new()
    })
}

#[test]
fn a_new_worktree_session_from_home_checks_out_the_prs_branch_and_starts_the_agent_in_it() {
    let mut world = start(
        with_projects(one_desktop(vec![])),
        vec![pr("r1", 10, "reviewer", "review this")],
    );
    world
        .daemon
        .on("fetch_pr_details", |r| {
            message(FetchPRDetailsResultMessage {
                success: true,
                prs: Some(vec![PR {
                    head_branch: Some("feature".into()),
                    ..pr(r["id"].as_str().unwrap(), 10, "reviewer", "review this")
                }]),
                ..FetchPRDetailsResultMessage::new()
            })
        })
        .on("ensure_repo", |r| ensured(r, true))
        .on("create_worktree_from_branch", |_| {
            message(CreateWorktreeResultMessage {
                success: true,
                path: Some("/home/me/projects/attn--feature".into()),
                ..CreateWorktreeResultMessage::new()
            })
        })
        .on("spawn_session", |r| {
            message(SpawnResultMessage {
                id: r["id"].as_str().unwrap().into(),
                success: true,
                ..SpawnResultMessage::new()
            })
        });
    assert_eq!(
        name(&world, "home.pr.r1.new").as_deref(),
        Some("New worktree session")
    );

    click(&mut world, "home.pr.r1.new");

    let fetched = world.daemon.sent_of("fetch_pr_details");
    assert_eq!(fetched.len(), 1);
    assert_eq!(fetched[0]["id"], "r1");
    let ensured = world.daemon.sent_of("ensure_repo");
    assert_eq!(ensured.len(), 1);
    assert_eq!(ensured[0]["target_path"], "/home/me/projects/attn");
    assert_eq!(
        ensured[0]["clone_url"],
        "https://github.com/victorarias/attn.git"
    );
    let created = world.daemon.sent_of("create_worktree_from_branch");
    assert_eq!(created.len(), 1);
    assert_eq!(created[0]["main_repo"], "/home/me/projects/attn");
    assert_eq!(created[0]["branch"], "origin/feature");
    let spawned = world.daemon.sent_of("spawn_session");
    assert_eq!(spawned.len(), 1);
    assert_eq!(spawned[0]["cwd"], "/home/me/projects/attn--feature");
    assert_eq!(spawned[0]["label"], "attn#10");
    assert_eq!(spawned[0]["agent"], "claude");
    assert_eq!(spawned[0]["placement"]["desktop_id"], "d1");
    assert!(!has(&world, "worktree-setup"));
}

#[test]
fn a_new_worktree_session_with_no_harness_picked_yet_says_to_pick_one_and_starts_nothing() {
    let mut world = start(
        with_projects_and(one_desktop(vec![]), &[]),
        vec![PR {
            head_branch: Some("feature".into()),
            ..pr("r1", 10, "reviewer", "review this")
        }],
    );

    click(&mut world, "home.pr.r1.new");

    assert_eq!(
        name(&world, "notice.text").as_deref(),
        Some("Choose a harness in a new session first: this starts the one you last picked.")
    );
    for cmd in [
        "fetch_pr_details",
        "ensure_repo",
        "create_worktree_from_branch",
        "spawn_session",
    ] {
        assert!(world.daemon.sent_of(cmd).is_empty(), "{cmd}");
    }
    assert!(!has(&world, "worktree-setup"));
}

#[test]
fn a_new_worktree_session_names_the_pr_and_the_step_it_is_on() {
    let mut world = start(
        with_projects(one_desktop(vec![])),
        vec![PR {
            head_branch: Some("feature".into()),
            ..pr("r1", 10, "reviewer", "review this")
        }],
    );
    world.daemon.on("ensure_repo", |r| ensured(r, true));

    click(&mut world, "home.pr.r1.new");

    assert_eq!(
        name(&world, "worktree-setup").as_deref(),
        Some("Creating worktree: attn#10")
    );
    assert!(world.daemon.sent_of("fetch_pr_details").is_empty());
}

/// The reason the failed corner opens to, dismissed after reading, or the notice when no
/// corner showed.
fn reason_shown(world: &mut World) -> Option<String> {
    if !has(world, "worktree-setup") {
        return name(world, "notice.text");
    }
    click(world, "worktree-setup");
    let reason = name(world, "worktree-setup.error");
    click(world, "worktree-setup.dismiss");
    reason
}

#[test]
fn a_new_worktree_session_that_cannot_finish_says_why_starts_nothing_and_can_be_tried_again() {
    let steps = [
        "fetch_pr_details",
        "ensure_repo",
        "create_worktree_from_branch",
        "spawn_session",
    ];
    let unknown = pr("r1", 10, "reviewer", "review this");
    let known = PR {
        head_branch: Some("feature".into()),
        ..unknown.clone()
    };
    let mut world = start(with_projects(one_desktop(vec![])), vec![unknown.clone()]);
    let try_it = |world: &mut World, case: &str, said: &str, sent: &[&str]| {
        let before: Vec<usize> = steps
            .iter()
            .map(|c| world.daemon.sent_of(c).len())
            .collect();
        click(world, "home.pr.r1.new");
        let reason = reason_shown(world);
        assert!(
            reason.as_deref().is_some_and(|r| r.contains(said)),
            "{case}: {reason:?}"
        );
        let went: Vec<&str> = steps
            .iter()
            .zip(before)
            .filter(|(cmd, n)| world.daemon.sent_of(cmd).len() > *n)
            .map(|(cmd, _)| *cmd)
            .collect();
        assert_eq!(went, sent, "{case}");
    };

    world.daemon.answer_in_turn(
        "fetch_pr_details",
        [
            Answer::Reply(message(FetchPRDetailsResultMessage {
                success: false,
                error: Some("API rate limit exceeded".into()),
                ..FetchPRDetailsResultMessage::new()
            })),
            Answer::Reply(message(FetchPRDetailsResultMessage {
                success: true,
                prs: Some(vec![unknown]),
                ..FetchPRDetailsResultMessage::new()
            })),
        ],
    );
    world.settle();
    try_it(
        &mut world,
        "fetching details fails",
        "Failed to fetch PR details. API rate limit exceeded",
        &["fetch_pr_details"],
    );
    try_it(
        &mut world,
        "GitHub has no branch",
        "PR branch information not available.",
        &["fetch_pr_details"],
    );

    world.daemon.emit(PRsUpdatedMessage {
        prs: Some(vec![known]),
        ..PRsUpdatedMessage::new()
    });
    world.daemon.on("ensure_repo", |r| ensured(r, false));
    world.settle();
    try_it(
        &mut world,
        "the clone fails",
        "Failed to clone repository victorarias/attn.",
        &["ensure_repo"],
    );

    world
        .daemon
        .on("ensure_repo", |r| ensured(r, true))
        .on("create_worktree_from_branch", |_| {
            message(CreateWorktreeResultMessage {
                success: false,
                error: Some("fatal: 'feature' already exists".into()),
                ..CreateWorktreeResultMessage::new()
            })
        });
    try_it(
        &mut world,
        "the worktree exists",
        "A worktree for this branch may already exist.",
        &["ensure_repo", "create_worktree_from_branch"],
    );
}

#[test]
fn the_hostname_beside_a_pr_number_opens_the_pr_and_says_so_on_hover() {
    let mut world = start(
        one_desktop(vec![]),
        vec![
            pr("public", 1, "reviewer", "on github.com"),
            PR {
                host: "ghe.corp.example".into(),
                url: "https://ghe.corp.example/victorarias/attn/pull/4".into(),
                ..pr("enterprise", 4, "reviewer", "the same repo on another host")
            },
        ],
    );
    world.daemon.on("pr_visited", |_| ());
    press(&mut world, Action::SessionGoToDashboard);
    let [x, y, w, h] = world.rect("home.pr.enterprise.host");
    let at = ((x + w / 2.0) as f32, (y + h / 2.0) as f32);

    world.gesture(|app| app.move_to(at));
    world.advance(Duration::from_secs(2));
    assert!(
        world
            .screen()
            .find(Target::role("tooltip").name("Open #4 on GitHub"))
            .is_some(),
        "hovering the hostname shows the Open tooltip:\n{}",
        world.screen()
    );

    world.gesture(|app| app.click_at(at));
    let visited = world.daemon.sent_of("pr_visited");
    assert_eq!(visited.len(), 1);
    assert_eq!(visited[0]["id"], "enterprise");
    assert_eq!(
        world.opened_links(),
        ["https://ghe.corp.example/victorarias/attn/pull/4"]
    );
}

const ROW_ACTIONS: [&str; 5] = ["new", "approve", "merge", "mute", "mute-author"];

fn tour_prs() -> Vec<PR> {
    let at = |id: &str, number, role, author: &str, repo: &str, host: &str, title: &str| PR {
        author: author.into(),
        host: host.into(),
        url: format!("https://{host}/{repo}/pull/{number}"),
        ..home_pr(id, number, role, repo, title)
    };
    let marked = |pr: PR, updated: bool, approved: bool, ci: &str| PR {
        has_new_changes: updated,
        approved_by_me: approved,
        ci_status: Some(ci.into()),
        ..pr
    };
    vec![
        marked(
            at(
                "a1",
                7,
                "author",
                "me",
                "victorarias/attn",
                "github.com",
                "Route every key through the keymap",
            ),
            true,
            false,
            "failure",
        ),
        marked(
            at(
                "r1",
                12,
                "reviewer",
                "someone",
                "org/shop",
                "github.com",
                "Cache the catalog so the storefront answers before the warehouse wakes up",
            ),
            false,
            true,
            "success",
        ),
        marked(
            at(
                "r2",
                31,
                "reviewer",
                "renovate-bot",
                "org/shop",
                "ghe.corp.example",
                "Bump serde",
            ),
            true,
            true,
            "pending",
        ),
        at(
            "r3",
            4,
            "reviewer",
            "someone",
            "org/api",
            "github.com",
            "Add the orders endpoint",
        ),
    ]
}

/// Home with the tour's pull requests, at the default window and text size, or at the smallest
/// window with the largest text.
fn home_at(smallest: bool) -> World {
    home_with(smallest, tour_prs())
}

fn home_with(smallest: bool, prs: Vec<PR>) -> World {
    let mut state = one_desktop(vec![]);
    state.prs = Some(prs);
    if smallest {
        state
            .settings
            .get_or_insert_default()
            .insert("uiScale".to_owned(), "1.5".to_owned());
    }
    let daemon = super::palette::daemon(state, vec![]);
    for cmd in [
        "pr_visited",
        "approve_pr",
        "merge_pr",
        "mute_pr",
        "mute_author",
        "ensure_repo",
    ] {
        daemon.on(cmd, |_| ());
    }
    let mut world = if smallest {
        World::start_sized(daemon, (800.0, 600.0))
    } else {
        World::start(daemon)
    };
    press(&mut world, Action::SessionGoToDashboard);
    world.gesture(|app| app.move_to((1.0, 1.0)));
    world.settle();
    world
}

/// Scrolls Home's cards until the pull request `id`'s row is whole in view.
fn into_view(world: &mut World, id: &str) {
    let row = format!("home.pr.{id}");
    for _ in 0..40 {
        let screen = world.screen();
        let [cx, cy, cw, ch] = screen
            .bounds(Target::key("home.cards.scroll"))
            .expect("home's cards are on screen");
        let [_, y, _, h] = screen
            .bounds(Target::key(row.as_str()))
            .unwrap_or_else(|| panic!("{row} is listed:\n{screen}"));
        if y >= cy && y + h <= cy + ch {
            return;
        }
        let toward = if y < cy { 60.0 } else { -60.0 };
        world.gesture(|app| app.runtime.wheel(cx + cw / 2.0, cy + ch / 2.0, toward));
        world.settle();
    }
    panic!("{row} never scrolls into view");
}

/// What the window draws of the element keyed `key`: how strongly, and the part of it its
/// clips leave visible.
fn drawn(world: &World, key: &str) -> (f32, [f32; 4]) {
    let element = world
        .runtime()
        .elements()
        .into_iter()
        .find(|e| e.key.as_deref() == Some(key))
        .unwrap_or_else(|| panic!("{key} is drawn"));
    (element.opacity, element.visible)
}

/// Whether the control keyed `key` shows: drawn whole in strength, some of it in view.
fn shows(world: &World, key: &str) -> bool {
    let (opacity, [_, _, w, h]) = drawn(world, key);
    opacity > 0.5 && w > 0.5 && h > 0.5
}

struct RowLook {
    height: f32,
    title_start: f32,
}

fn row_look(world: &World, id: &str) -> RowLook {
    let screen = world.screen();
    let row = screen
        .bounds(Target::key(format!("home.pr.{id}")))
        .unwrap_or_else(|| panic!("home.pr.{id} is listed"));
    let title = screen
        .bounds(Target::key(format!("home.pr.{id}.title")))
        .unwrap_or_else(|| panic!("home.pr.{id} has its title:\n{screen}"));
    RowLook {
        height: row[3],
        title_start: title[0],
    }
}

#[test]
fn each_pr_rests_as_one_line_with_its_actions_and_author_out_of_sight() {
    for smallest in [false, true] {
        let mut world = home_at(smallest);
        for id in ["a1", "r1", "r2", "r3"] {
            into_view(&mut world, id);
            let height = world
                .screen()
                .bounds(Target::key(format!("home.pr.{id}")))
                .unwrap_or_else(|| panic!("home.pr.{id} is listed"))[3];
            assert!(
                (height - attn_ui::system::ListRow::height()).abs() < 0.5,
                "smallest {smallest}: {id} stands {} tall, not one row's {}",
                height,
                attn_ui::system::ListRow::height()
            );
            let (_, row) = drawn(&world, &format!("home.pr.{id}"));
            let parts: Vec<(&str, [f32; 4])> = [
                "role", "host", "title", "updated", "approved", "ci", "reason",
            ]
            .into_iter()
            .filter_map(|part| {
                let key = format!("home.pr.{id}.{part}");
                let element = world
                    .runtime()
                    .elements()
                    .into_iter()
                    .find(|e| e.key.as_deref() == Some(key.as_str()))?;
                (element.opacity > 0.01 && element.visible[2] > 0.5 && element.visible[3] > 0.5)
                    .then_some((part, element.visible))
            })
            .collect();
            for (part, [x, y, w, h]) in &parts {
                assert!(
                    *x >= row[0] - 0.5
                        && x + w <= row[0] + row[2] + 0.5
                        && *y >= row[1] - 0.5
                        && y + h <= row[1] + row[3] + 0.5,
                    "smallest {smallest}: {id}'s {part} at {:?} runs out of its row {row:?}",
                    [x, y, w, h]
                );
            }
            for (i, (a, ra)) in parts.iter().enumerate() {
                for (b, rb) in &parts[i + 1..] {
                    let apart = ra[0] + ra[2] <= rb[0] + 0.5
                        || rb[0] + rb[2] <= ra[0] + 0.5
                        || ra[1] + ra[3] <= rb[1] + 0.5
                        || rb[1] + rb[3] <= ra[1] + 0.5;
                    assert!(
                        apart,
                        "smallest {smallest}: {id}'s {a} {ra:?} overlaps its {b} {rb:?}"
                    );
                }
            }
            for action in ROW_ACTIONS {
                assert!(
                    !shows(&world, &format!("home.pr.{id}.{action}")),
                    "smallest {smallest}: {id}'s {action} shows on a row at rest"
                );
            }
        }
    }
}

#[test]
fn hovering_or_focusing_a_pr_shows_its_actions_and_author_beside_its_title_never_over_it() {
    for smallest in [false, true] {
        for by_keyboard in [false, true] {
            let mut world = home_at(smallest);
            let case = format!("smallest {smallest}, keyboard {by_keyboard}");
            for id in ["a1", "r1", "r2"] {
                into_view(&mut world, id);
                let rest = row_look(&world, id);
                if by_keyboard {
                    let open = format!("home.pr.{id}.open");
                    world.gesture(|app| app.update(|_, tx| tx.focus(open, true)));
                } else {
                    let [x, y, w, h] = world.rect(&format!("home.pr.{id}"));
                    world.gesture(|app| app.move_to(((x + w / 4.0) as f32, (y + h / 2.0) as f32)));
                }
                world.advance(Duration::from_secs(1));
                let shown = row_look(&world, id);
                assert!(
                    (shown.height - rest.height).abs() < 0.5,
                    "{case}: {id} grows from {} to {} as its actions show",
                    rest.height,
                    shown.height
                );
                assert!(
                    (shown.title_start - rest.title_start).abs() < 0.5,
                    "{case}: {id}'s title moves from {} to {}",
                    rest.title_start,
                    shown.title_start
                );
                let mut first_action = f32::INFINITY;
                for action in ROW_ACTIONS {
                    let key = format!("home.pr.{id}.{action}");
                    assert!(shows(&world, &key), "{case}: {id}'s {action} does not show");
                    first_action = first_action.min(drawn(&world, &key).1[0]);
                }
                let (_, [x, _, w, _]) = drawn(&world, &format!("home.pr.{id}.title"));
                assert!(
                    w <= 0.0 || x + w <= first_action + 0.5,
                    "{case}: {id}'s title shows to {} under its first action at {first_action}",
                    x + w
                );
            }
        }
    }
}

#[test]
fn every_prs_action_keeps_its_key_name_and_tab_stop() {
    let mut world = home_at(false);
    let names = [
        (
            "open",
            "#12 on github.com: Cache the catalog so the storefront answers before the warehouse wakes up",
        ),
        ("new", "New worktree session"),
        ("approve", "Approve"),
        ("merge", "Merge"),
        ("mute", "Mute this PR"),
        ("mute-author", "Mute all PRs by someone"),
    ];
    for (action, said) in names {
        assert_eq!(
            name(&world, &format!("home.pr.r1.{action}")).as_deref(),
            Some(said),
            "{action}"
        );
    }
    assert_eq!(
        name(&world, "home.prs.repo.org/shop.mute").as_deref(),
        Some("Mute all PRs from this repo")
    );
    world.gesture(|app| app.update(|_, tx| tx.focus("home.pr.r1.open".to_owned(), true)));
    let mut stops = vec![focused(&world)];
    for _ in 0..6 {
        world.gesture(|app| {
            app.key(KeyEvent {
                key: Key::Tab,
                mods: Mods::default(),
            })
        });
        stops.push(focused(&world));
    }
    let r1 = |part: &str| Some(format!("home.pr.r1.{part}"));
    assert_eq!(
        stops,
        [
            r1("open"),
            r1("new"),
            r1("approve"),
            r1("merge"),
            r1("mute"),
            r1("mute-author"),
            Some("home.pr.r2.open".to_owned()),
        ]
    );
}

const ACTION_COMMANDS: [&str; 5] = [
    "approve_pr",
    "merge_pr",
    "mute_pr",
    "mute_author",
    "ensure_repo",
];

fn acted(world: &World) -> Vec<&'static str> {
    let mut acted: Vec<&'static str> = ACTION_COMMANDS
        .into_iter()
        .filter(|cmd| !world.daemon.sent_of(cmd).is_empty())
        .collect();
    if has(world, MERGE) {
        acted.push("merge dialog");
    }
    acted
}

#[test]
fn a_press_where_a_prs_title_shows_or_on_its_open_key_opens_it_and_acts_on_nothing() {
    let prs = tour_prs();
    for smallest in [false, true] {
        for pr in &prs[..3] {
            for along in [0.5, 0.95] {
                let mut world = home_at(smallest);
                let case = format!("smallest {smallest}, {} at {along}", pr.id);
                into_view(&mut world, &pr.id);
                let (_, [x, y, w, h]) = drawn(&world, &format!("home.pr.{}.title", pr.id));
                assert!(w > 0.5, "{case}: the title shows at rest");
                let at = (x + w * along, y + h / 2.0);
                world.gesture(|app| app.click_at(at));
                assert_eq!(acted(&world), Vec::<&str>::new(), "{case}");
                assert_eq!(
                    world.opened_links(),
                    std::slice::from_ref(&pr.url),
                    "{case}"
                );
            }
            let mut world = home_at(smallest);
            into_view(&mut world, &pr.id);
            let [x, y, w, h] = world.rect(&format!("home.pr.{}", pr.id));
            world.gesture(|app| app.move_to(((x + w / 4.0) as f32, (y + h / 2.0) as f32)));
            world.advance(Duration::from_secs(1));
            let open = format!("home.pr.{}.open", pr.id);
            world
                .automate("press", serde_json::json!({"target": {"key": open}}))
                .unwrap_or_else(|why| panic!("{why}"));
            let case = format!("smallest {smallest}, {} by its open key", pr.id);
            assert_eq!(acted(&world), Vec::<&str>::new(), "{case}");
            assert_eq!(
                world.opened_links(),
                std::slice::from_ref(&pr.url),
                "{case}"
            );
        }
    }
}

#[test]
fn a_long_author_keeps_every_action_whole_in_its_row_and_its_full_name() {
    for author in [
        "dependabot[bot]",
        "chatgpt-codex-connector[bot]",
        "microsoft-github-policy-service[bot]",
    ] {
        for smallest in [false, true] {
            let mut prs = tour_prs();
            prs[1].author = author.into();
            let mut world = home_with(smallest, prs);
            let case = format!("{author}, smallest {smallest}");
            into_view(&mut world, "r1");
            let [x, y, w, h] = world.rect("home.pr.r1");
            world.gesture(|app| app.move_to(((x + w / 4.0) as f32, (y + h / 2.0) as f32)));
            world.advance(Duration::from_secs(1));
            let (_, row) = drawn(&world, "home.pr.r1");
            for action in ROW_ACTIONS {
                let key = format!("home.pr.r1.{action}");
                let element = world
                    .runtime()
                    .elements()
                    .into_iter()
                    .find(|e| e.key.as_deref() == Some(key.as_str()))
                    .unwrap_or_else(|| panic!("{case}: {key} is drawn"));
                let [ex, _, ew, _] = element.rect;
                assert!(
                    element.opacity > 0.5
                        && (element.visible[2] - ew).abs() < 0.5
                        && ex >= row[0] - 0.5
                        && ex + ew <= row[0] + row[2] + 0.5,
                    "{case}: {action} at {:?}, visible {:?}, is not whole in its row {row:?}",
                    element.rect,
                    element.visible
                );
            }
            assert_eq!(
                name(&world, "home.pr.r1.mute-author"),
                Some(format!("Mute all PRs by {author}")),
                "{case}"
            );
        }
    }
}
