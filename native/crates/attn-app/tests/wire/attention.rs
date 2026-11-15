use std::time::Duration;

use attn_app::Action;
use attn_protocol::{
    AuthorState, AuthorsUpdatedMessage, InitialStateMessage, PR, PRActionResultMessage,
    PRsUpdatedMessage, RefreshPRsResultMessage, RepoState, ReposUpdatedMessage,
};
use attn_testkit::{Target, World, message};
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
