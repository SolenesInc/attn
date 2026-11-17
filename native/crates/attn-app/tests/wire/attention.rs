use std::time::Duration;

use attn_app::Action;
use attn_protocol::{
    AuthorState, AuthorsUpdatedMessage, CreateWorktreeResultMessage, EnsureRepoResultMessage,
    FetchPRDetailsResultMessage, GitHubHostsUpdatedMessage, InitialStateMessage, PR,
    PRActionResultMessage, PRsUpdatedMessage, RefreshPRsResultMessage, RepoState,
    ReposUpdatedMessage, SettingsUpdatedMessage, SpawnResultMessage,
};
use attn_testkit::{Answer, Target, World, message};
use rune::{Key, KeyEvent, Mods};

use super::queue::{one_desktop, queue, turn};
use super::reach::unreached_by_tab;
use super::sidebar::{chord, session};

const DRAWER: &str = "attention";
const MERGE: &str = "attention.merge-dialog";

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

fn opened(state: InitialStateMessage, prs: Vec<PR>) -> World {
    let mut world = start(state, prs);
    world.gesture(|app| app.key(chord(Action::DockAttention)));
    world
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

fn focused(world: &World) -> Option<String> {
    world.screen().focused().and_then(|n| n.key)
}

fn click(world: &mut World, key: &str) {
    world.gesture(|app| app.click(Target::key(key)));
}

fn listed(world: &World, prefix: &str) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key(DRAWER))
        .into_iter()
        .filter_map(|n| n.key?.strip_prefix(prefix).map(str::to_owned))
        .filter(|rest| !rest.contains('.'))
        .collect()
}

fn sessions(world: &World) -> Vec<String> {
    listed(world, "attention.session.")
}

fn prs(world: &World) -> Vec<String> {
    listed(world, "attention.pr.")
}

fn escape() -> KeyEvent {
    KeyEvent {
        key: Key::Escape,
        mods: Mods::default(),
    }
}

fn waiting_three() -> InitialStateMessage {
    one_desktop(vec![
        session("s1", "working"),
        session("s2", "waiting_input"),
        session("s3", "idle"),
    ])
}

#[test]
fn the_palette_opens_the_drawer_on_the_agents_waiting_for_the_user() {
    let mut world = start(waiting_three(), vec![]);
    assert!(!has(&world, DRAWER));

    world.gesture(|app| app.key(chord(Action::UiCommandPalette)));
    world.gesture(|app| app.type_text("attention"));
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Enter,
            mods: Mods::default(),
        })
    });

    assert!(!has(&world, "palette"));
    assert_eq!(name(&world, DRAWER).as_deref(), Some("Needs Attention"));
    assert_eq!(sessions(&world), ["s2"]);
    assert_eq!(focused(&world).as_deref(), Some(DRAWER));
}

#[test]
fn in_the_queue_the_drawer_lists_the_agents_owed_a_turn() {
    let state = queue(one_desktop(vec![
        turn("owed", 5),
        session("waiting-but-not-owed", "waiting_input"),
        session("busy", "working"),
    ]));
    let world = opened(state, vec![]);
    assert_eq!(sessions(&world), ["owed"]);
}

#[test]
fn pressing_a_waiting_agent_opens_it() {
    let mut world = opened(waiting_three(), vec![]);
    let row = world.screen().get(Target::key("attention.session.s2"));
    assert_eq!(
        (row.role.as_str(), row.states.clone()),
        ("button", Vec::<String>::new())
    );
    click(&mut world, "attention.session.s2");
    assert_eq!(super::ledger::shown_session(&world), ["s2"]);
}

#[test]
fn review_requests_then_the_users_own_prs_each_with_repo_number_title_and_why() {
    let world = opened(
        one_desktop(vec![]),
        vec![
            pr("r1", 10, "reviewer", "review this"),
            pr("a1", 20, "author", "my change"),
        ],
    );

    assert_eq!(
        name(&world, "attention.count").as_deref(),
        Some("2 need attention")
    );
    assert_eq!(
        name(&world, "attention.review").as_deref(),
        Some("Review Requested")
    );
    assert_eq!(name(&world, "attention.yours").as_deref(), Some("Your PRs"));
    assert_eq!(
        name(&world, "attention.pr.r1.open").as_deref(),
        Some("attn #10: review this")
    );
    assert_eq!(
        name(&world, "attention.pr.a1.reason").as_deref(),
        Some("ready to merge")
    );
    let screen = world.screen();
    let [_, review_y, _, _] = screen.bounds(Target::key("attention.review")).unwrap();
    let [_, yours_y, _, _] = screen.bounds(Target::key("attention.yours")).unwrap();
    assert!(review_y < yours_y);
    assert!(!has(&world, "attention.empty"));
}

#[test]
fn pressing_a_pr_opens_it_on_github() {
    let mut world = opened(
        one_desktop(vec![]),
        vec![pr("r1", 10, "reviewer", "review this")],
    );
    click(&mut world, "attention.pr.r1.open");
    assert_eq!(
        world.opened_links(),
        ["https://github.com/victorarias/attn/pull/10"]
    );
}

#[test]
fn muted_prs_repos_and_authors_and_approved_prs_without_new_changes_are_left_out() {
    let approved = PR {
        approved_by_me: true,
        ..pr("p5", 5, "reviewer", "approved already")
    };
    let mut world = opened(
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
            approved.clone(),
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

    world.daemon.emit(PRsUpdatedMessage {
        prs: Some(vec![
            pr("p4", 4, "reviewer", "still wanted"),
            PR {
                has_new_changes: true,
                ..approved
            },
        ]),
        ..PRsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(prs(&world), ["p4", "p5"]);
}

#[test]
fn with_nothing_waiting_the_drawer_says_so() {
    let mut world = opened(
        one_desktop(vec![]),
        vec![pr("r1", 10, "reviewer", "review this")],
    );
    assert!(!has(&world, "attention.empty"));

    world.daemon.emit(PRsUpdatedMessage {
        prs: Some(vec![]),
        ..PRsUpdatedMessage::new()
    });
    world.settle();

    assert_eq!(
        name(&world, "attention.empty").as_deref(),
        Some("Nothing needs attention")
    );
    assert_eq!(
        name(&world, "attention.count").as_deref(),
        Some("0 need attention")
    );
}

#[test]
fn approve_shows_its_check_for_a_moment_and_mute_sends_the_pr() {
    let mut world = opened(
        one_desktop(vec![]),
        vec![pr("p1", 1, "reviewer", "review this")],
    );
    answering(&world, "approve_pr", "approve", None);

    click(&mut world, "attention.pr.p1.approve");
    assert_eq!(world.daemon.sent_of("approve_pr").len(), 1);
    assert_eq!(world.daemon.sent_of("approve_pr")[0]["id"], "p1");
    assert_eq!(
        name(&world, "attention.pr.p1.approve").as_deref(),
        Some("Approved")
    );

    click(&mut world, "attention.pr.p1.approve");
    assert_eq!(world.daemon.sent_of("approve_pr").len(), 1);

    world.advance(Duration::from_millis(1600));
    assert_eq!(
        name(&world, "attention.pr.p1.approve").as_deref(),
        Some("Approve")
    );
    click(&mut world, "attention.pr.p1.mute");
    assert_eq!(world.daemon.sent_of("mute_pr").len(), 1);
    assert_eq!(world.daemon.sent_of("mute_pr")[0]["id"], "p1");
}

#[test]
fn a_muted_pr_leaves_at_once_so_mute_cannot_be_pressed_twice() {
    let mut world = opened(
        one_desktop(vec![]),
        vec![
            pr("p1", 1, "reviewer", "noisy"),
            pr("p2", 2, "reviewer", "wanted"),
        ],
    );
    click(&mut world, "attention.pr.p1.mute");
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
    let mut world = opened(
        one_desktop(vec![]),
        vec![pr("p1", 1, "reviewer", "review this")],
    );
    answering(
        &world,
        "approve_pr",
        "approve",
        Some("review is not allowed on your own PR"),
    );

    click(&mut world, "attention.pr.p1.approve");
    assert_eq!(
        name(&world, "notice.text").as_deref(),
        Some("review is not allowed on your own PR")
    );

    click(&mut world, "attention.pr.p1.approve");
    assert_eq!(world.daemon.sent_of("approve_pr").len(), 2);
}

#[test]
fn merge_asks_first_and_squashes_on_yes() {
    let mut world = opened(one_desktop(vec![]), vec![pr("a1", 7, "author", "ship it")]);
    answering(&world, "merge_pr", "merge", None);

    click(&mut world, "attention.pr.a1.merge");
    assert_eq!(name(&world, MERGE).as_deref(), Some("Merge PR #7?"));
    click(&mut world, "attention.merge.cancel");
    assert!(!has(&world, MERGE));
    assert_eq!(focused(&world).as_deref(), Some("attention.pr.a1.merge"));
    assert!(world.daemon.sent_of("merge_pr").is_empty());

    click(&mut world, "attention.pr.a1.merge");
    world.gesture(|app| app.key(escape()));
    assert!(!has(&world, MERGE));
    assert!(world.daemon.sent_of("merge_pr").is_empty());

    click(&mut world, "attention.pr.a1.merge");
    click(&mut world, "attention.merge.confirm");
    assert!(!has(&world, MERGE));
    let sent = world.daemon.sent_of("merge_pr");
    assert_eq!(sent.len(), 1);
    assert_eq!(
        (&sent[0]["id"], &sent[0]["method"]),
        (&"a1".into(), &"squash".into())
    );
}

#[test]
fn the_refresh_key_polls_github_and_a_failed_poll_says_why() {
    let mut world = start(one_desktop(vec![]), vec![]);
    world.gesture(|app| app.key(chord(Action::SessionRefreshPrs)));
    assert_eq!(world.daemon.sent_of("refresh_prs").len(), 1);

    world.daemon.emit(RefreshPRsResultMessage {
        success: false,
        error: Some("gh is not signed in".into()),
        ..RefreshPRsResultMessage::new()
    });
    world.settle();
    assert_eq!(
        name(&world, "notice.text").as_deref(),
        Some("Refresh failed: gh is not signed in")
    );
}

#[test]
fn escape_closes_the_drawer_and_every_control_is_reached_by_tab() {
    let mut world = opened(
        waiting_three(),
        vec![
            pr("r1", 10, "reviewer", "review this"),
            pr("a1", 20, "author", "my change"),
        ],
    );
    assert_eq!(unreached_by_tab(&mut world), Vec::<String>::new());

    world.gesture(|app| app.key(escape()));
    assert!(!has(&world, DRAWER));

    world.gesture(|app| app.key(chord(Action::DockAttention)));
    assert!(has(&world, DRAWER));
    world.gesture(|app| app.key(chord(Action::DockAttention)));
    assert!(!has(&world, DRAWER));
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
    click(&mut world, "attention.merge.cancel");
    assert_eq!(focused(&world).as_deref(), Some("home.pr.a1.merge"));
    click(&mut world, "home.pr.a1.merge");
    click(&mut world, "attention.merge.confirm");
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

    click(&mut world, "home.pr.p1.open");
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

fn with_projects(mut state: InitialStateMessage) -> InitialStateMessage {
    state.settings = Some(
        [(
            "projects_directory".to_owned(),
            "/home/me/projects/".to_owned(),
        )]
        .into(),
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
    let mut world = start(one_desktop(vec![]), vec![unknown.clone()]);
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

    try_it(
        &mut world,
        "no projects directory",
        "Configure your Projects Directory in Settings first",
        &[],
    );

    world.daemon.emit(SettingsUpdatedMessage {
        settings: with_projects(InitialStateMessage::new()).settings,
        ..SettingsUpdatedMessage::new()
    });
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
