use attn_app::Action;
use attn_protocol::{
    GitOperation, GitOperationFinishedMessage, GitOperationStartedMessage, Session, Worktree,
    WorktreeListResult, WorktreeListResultEvent, WorktreeRepository, WorktreeStateChangedEvent,
    WorktreeSweepEntry, WorktreeSweepLogResult, WorktreeSweepLogResultEvent, WorktreeSweptEvent,
};
use attn_testkit::{Request, World, message};
use rune::Key;

use super::super::queue::ago;
use super::super::sidebar::{chord, desktop, pane, session, state};
use super::{
    click, come_back, entry, focused, hang_up, has, ledger_daemon, letter, name, open, plain,
    query_text, rows, selected, shown_session, text_in, type_query,
};

const MAIN: &str = "/projects/attn";
const PATH: &str = "/projects/attn--feat-one";
const TWO: &str = "/projects/attn--feat-two";
const LIVE: &str = "11111111-2222-3333-4444-555555555555";

fn worktree(path: &str, branch: &str) -> Worktree {
    Worktree {
        path: path.into(),
        branch: branch.into(),
        main_repo: MAIN.into(),
        observed_at: Some(ago(60)),
        sweep_status: Some("scheduled".into()),
        sweep_reason: Some("merged and clean; idle 3 of 14 days".into()),
        sweep_at: Some(ago(-10 * 24 * 60)),
        merged_signal: Some("ancestor".into()),
        ..Worktree::default()
    }
}

fn feat_one() -> Worktree {
    worktree(PATH, "feat/one")
}

fn integration() -> Vec<WorktreeRepository> {
    vec![WorktreeRepository {
        main_repo: MAIN.into(),
        integration_branch: Some("origin/next".into()),
        integration_source: Some("pull_requests".into()),
    }]
}

fn swept(id: &str, path: &str, action: &str, reason: &str, minutes_ago: i64) -> WorktreeSweepEntry {
    WorktreeSweepEntry {
        id: id.into(),
        path: path.into(),
        main_repo: MAIN.into(),
        branch: Some("feat/one".into()),
        action: action.into(),
        reason: Some(reason.into()),
        at: ago(minutes_ago),
    }
}

fn listed(
    request: &Request,
    worktrees: Vec<Worktree>,
    repositories: Vec<WorktreeRepository>,
) -> serde_json::Value {
    message(WorktreeListResultEvent {
        request_id: request["request_id"].as_str().map(str::to_owned),
        success: true,
        worktree_list_result: WorktreeListResult {
            worktrees,
            repositories,
            omitted: 0,
        },
        ..WorktreeListResultEvent::new()
    })
}

fn serve(world: &World, worktrees: Vec<Worktree>, repositories: Vec<WorktreeRepository>) {
    world.daemon.on("worktree_list", move |r| {
        listed(r, worktrees.clone(), repositories.clone())
    });
}

fn serve_log(world: &World, entries: Vec<WorktreeSweepEntry>) {
    world.daemon.on("worktree_sweep_log", move |r| {
        message(WorktreeSweepLogResultEvent {
            request_id: r["request_id"].as_str().map(str::to_owned),
            success: true,
            worktree_sweep_log_result: WorktreeSweepLogResult {
                entries: entries.clone(),
                omitted: 0,
            },
            ..WorktreeSweepLogResultEvent::new()
        })
    });
}

fn answer(world: &World, cmd: &'static str, event: &'static str, extra: serde_json::Value) {
    world.daemon.on(cmd, move |r| {
        let mut reply = serde_json::json!({"event": event, "request_id": r["request_id"]});
        for (k, v) in extra.as_object().unwrap() {
            reply[k] = v.clone();
        }
        if reply["path"].is_null() {
            reply["path"] = r["path"].clone();
        }
        reply
    });
}

fn live_session() -> Session {
    Session {
        label: "one".into(),
        directory: format!("{PATH}/app"),
        ..session(LIVE, "waiting_input")
    }
}

fn world_with(
    sessions: Vec<Session>,
    worktrees: Vec<Worktree>,
    repositories: Vec<WorktreeRepository>,
) -> World {
    let desktops = sessions
        .iter()
        .enumerate()
        .map(|(at, s)| {
            let id = format!("d{at}");
            let p = format!("p{at}");
            desktop(&id, &format!("a{at}"), "One", vec![pane(&id, &p, &s.id)])
        })
        .collect();
    let world = World::start(ledger_daemon(state(sessions, desktops), vec![]));
    serve(&world, worktrees, repositories);
    serve_log(&world, vec![]);
    world
}

fn start(worktrees: Vec<Worktree>) -> World {
    let mut world = world_with(vec![], worktrees, integration());
    open(&mut world);
    click(&mut world, "ledger.tab.worktrees");
    world
}

fn meta(world: &World, path: &str) -> String {
    name(world, &format!("ledger.row.{path}.meta"))
}

fn status(world: &World) -> String {
    text_in(world, "ledger.status")
}

fn sent(world: &World, cmd: &str) -> Vec<serde_json::Value> {
    world
        .daemon
        .sent_of(cmd)
        .into_iter()
        .map(|mut r| {
            let fields = r.as_object_mut().unwrap();
            fields.remove("request_id");
            fields.remove("cmd");
            r
        })
        .collect()
}

#[test]
fn asks_once_and_groups_worktrees_under_their_repository_and_the_branch_they_merge_into() {
    let world = start(vec![feat_one(), worktree(TWO, "feat/two")]);

    assert_eq!(rows(&world), [PATH, TWO]);
    assert_eq!(name(&world, &format!("ledger.group.{MAIN}")), "attn");
    assert_eq!(
        name(&world, &format!("ledger.group.{MAIN}.meta")),
        "merges into origin/next"
    );
    assert_eq!(world.daemon.sent_of("worktree_list").len(), 1);
    assert!(status(&world).contains("2 worktrees"));
}

#[test]
fn says_what_the_sweep_decided_in_a_word_on_the_row_and_with_its_reason_in_the_inspector() {
    let world = start(vec![feat_one()]);

    assert!(meta(&world, PATH).contains("removing in 10d"));
    assert!(meta(&world, PATH).contains("merged · ancestor"));
    assert_eq!(
        name(&world, "ledger.inspector.sweep-reason"),
        "merged and clean; idle 3 of 14 days"
    );
}

#[test]
fn shows_every_reason_a_worktree_is_kept_as_its_own_word() {
    let world = start(vec![Worktree {
        dirty: Some(true),
        dirty_files: Some(3),
        stashes: Some(2),
        unpushed: Some(1),
        prunable: Some(true),
        sweep_status: Some("kept_dirty".into()),
        sweep_reason: Some("3 uncommitted or untracked file(s)".into()),
        ..feat_one()
    }]);

    let words: Vec<String> = meta(&world, PATH)
        .split("  ·  ")
        .map(str::to_owned)
        .collect();
    for word in ["stale", "dirty 3", "2 stashed", "1 ahead", "kept dirty"] {
        assert!(words.contains(&word.to_owned()), "{word} in {words:?}");
    }
    assert!(status(&world).contains("1 dirty"));
}

#[test]
fn names_a_live_session_in_the_worktree_by_its_title_and_goes_to_it() {
    let mut world = world_with(
        vec![live_session()],
        vec![Worktree {
            sweep_status: Some("kept_live_session".into()),
            sweep_reason: Some(format!("{LIVE} is running in it")),
            ..feat_one()
        }],
        integration(),
    );
    open(&mut world);
    click(&mut world, "ledger.tab.worktrees");

    assert!(meta(&world, PATH).contains("1 live session"));
    assert_eq!(
        name(&world, "ledger.inspector.sweep-reason"),
        "one is running in it"
    );
    click(&mut world, &format!("ledger.inspector.session.{LIVE}"));

    assert!(!has(&world, "ledger.panel"));
    assert_eq!(shown_session(&world), [LIVE]);
}

#[test]
fn narrows_by_repo_and_words_and_says_how_many_the_query_hides() {
    let other = Worktree {
        main_repo: "/projects/other".into(),
        ..worktree("/projects/other--x", "x")
    };
    let mut world = world_with(
        vec![],
        vec![feat_one(), other],
        vec![
            WorktreeRepository {
                main_repo: MAIN.into(),
                ..WorktreeRepository::default()
            },
            WorktreeRepository {
                main_repo: "/projects/other".into(),
                ..WorktreeRepository::default()
            },
        ],
    );
    open(&mut world);
    click(&mut world, "ledger.tab.worktrees");

    type_query(&mut world, "repo:attn");
    assert_eq!(rows(&world), [PATH]);
    assert!(status(&world).contains("1 hidden by the query"));

    plain(&mut world, Key::Escape);
    type_query(&mut world, "nothing-like-this");
    assert_eq!(name(&world, "ledger.empty"), "Nothing matches the query.");
    assert_eq!(world.daemon.sent_of("worktree_list").len(), 1);
}

#[test]
fn says_why_the_list_could_not_be_read_instead_of_showing_it_empty() {
    let mut world = world_with(vec![], vec![], vec![]);
    world.daemon.on("worktree_list", |r| {
        serde_json::json!({"event": "command_error", "cmd": "worktree_list", "request_id": r["request_id"], "success": false, "error": "git is not installed"})
    });
    open(&mut world);
    click(&mut world, "ledger.tab.worktrees");

    assert_eq!(name(&world, "ledger.error"), "git is not installed");
}

#[test]
fn follows_a_worktree_the_daemon_reports_changed_and_drops_one_it_reports_swept() {
    let mut world = start(vec![feat_one()]);

    world.daemon.emit(WorktreeStateChangedEvent {
        worktrees: vec![Worktree {
            dirty: Some(true),
            dirty_files: Some(4),
            sweep_status: Some("kept_dirty".into()),
            ..feat_one()
        }],
        ..WorktreeStateChangedEvent::new()
    });
    world.settle();
    assert!(meta(&world, PATH).contains("dirty 4"));

    world.daemon.emit(WorktreeSweptEvent {
        sweep_entry: swept("entry-1", PATH, "removed", "merged", 0),
        ..WorktreeSweptEvent::new()
    });
    world.settle();
    assert!(rows(&world).is_empty());
    assert_eq!(world.daemon.sent_of("worktree_list").len(), 1);
}

#[test]
fn a_change_pushed_while_the_list_is_closed_shows_when_it_opens_and_a_reconnect_reads_again() {
    let mut world = start(vec![feat_one()]);
    letter(&mut world, "[");
    world.daemon.emit(WorktreeStateChangedEvent {
        worktrees: vec![worktree(TWO, "feat/two")],
        ..WorktreeStateChangedEvent::new()
    });
    world.settle();
    serve(
        &world,
        vec![feat_one(), worktree(TWO, "feat/two")],
        integration(),
    );
    letter(&mut world, "]");
    assert_eq!(rows(&world), [PATH, TWO]);

    hang_up(&mut world);
    serve(&world, vec![feat_one()], integration());
    come_back(&mut world);
    assert_eq!(rows(&world), [PATH]);
    assert_eq!(world.daemon.sent_of("worktree_list").len(), 3);
}

#[test]
fn keeps_a_worktree_and_offers_the_way_back_out() {
    let mut world = start(vec![feat_one()]);
    world.daemon.on("worktree_keep", |r| {
        let keep = r["keep"] == true;
        serde_json::json!({
            "event": "worktree_keep_result",
            "request_id": r["request_id"],
            "success": true,
            "worktrees": [Worktree {
                pinned: Some(keep),
                sweep_status: Some(if keep { "pinned" } else { "scheduled" }.into()),
                ..feat_one()
            }],
        })
    });

    click(&mut world, &format!("ledger.row.{PATH}.verb"));
    assert!(meta(&world, PATH).contains("kept"));
    assert_eq!(name(&world, &format!("ledger.row.{PATH}.verb")), "Unpin");
    click(&mut world, &format!("ledger.row.{PATH}.verb"));

    assert_eq!(name(&world, &format!("ledger.row.{PATH}.verb")), "Keep");
    assert_eq!(
        sent(&world, "worktree_keep"),
        [
            serde_json::json!({"path": PATH, "keep": true}),
            serde_json::json!({"path": PATH, "keep": false}),
        ]
    );
}

#[test]
fn shows_the_daemons_own_words_on_the_row_when_it_refuses_to_keep_a_worktree() {
    let mut world = start(vec![feat_one()]);
    answer(
        &world,
        "worktree_keep",
        "worktree_keep_result",
        serde_json::json!({"success": false, "error": "no worktree at that path"}),
    );

    click(&mut world, &format!("ledger.row.{PATH}.verb"));

    assert_eq!(
        name(&world, &format!("ledger.row.{PATH}.note")),
        "no worktree at that path"
    );
}

#[test]
fn asks_before_deleting_and_stands_down_when_the_user_moves_on() {
    let mut world = start(vec![feat_one()]);
    answer(
        &world,
        "delete_worktree",
        "delete_worktree_result",
        serde_json::json!({"success": true}),
    );

    click(&mut world, &format!("ledger.row.{PATH}.more"));
    assert_eq!(
        name(&world, &format!("ledger.row.{PATH}.menu.2")),
        "Delete…"
    );
    click(&mut world, &format!("ledger.row.{PATH}.menu.2"));
    assert!(world.daemon.sent_of("delete_worktree").is_empty());
    assert_eq!(
        name(&world, &format!("ledger.row.{PATH}.verb")),
        "Delete for real"
    );
    letter(&mut world, "2");

    assert_eq!(name(&world, &format!("ledger.row.{PATH}.verb")), "Keep");
    click(&mut world, "ledger.inspector.action.1");
    plain(&mut world, Key::Escape);
    assert_eq!(name(&world, &format!("ledger.row.{PATH}.verb")), "Keep");
    assert!(has(&world, "ledger.panel"));
    assert!(world.daemon.sent_of("delete_worktree").is_empty());
}

#[test]
fn deletes_a_clean_worktree_on_the_second_word_and_forces_a_dirty_one_while_saying_it_loses_changes()
 {
    let mut world = start(vec![
        feat_one(),
        Worktree {
            dirty: Some(true),
            dirty_files: Some(2),
            sweep_status: Some("kept_dirty".into()),
            ..worktree(TWO, "feat/two")
        },
    ]);
    answer(
        &world,
        "delete_worktree",
        "delete_worktree_result",
        serde_json::json!({"success": true}),
    );

    click(&mut world, "ledger.inspector.action.1");
    click(&mut world, &format!("ledger.row.{PATH}.verb"));
    assert_eq!(rows(&world), [PATH, TWO]);

    click(&mut world, &format!("ledger.row.{TWO}"));
    click(&mut world, "ledger.inspector.action.1");
    assert_eq!(
        name(&world, &format!("ledger.row.{TWO}.verb")),
        "Delete, losing changes"
    );
    click(&mut world, &format!("ledger.row.{TWO}.verb"));

    assert_eq!(
        sent(&world, "delete_worktree"),
        [
            serde_json::json!({"path": PATH}),
            serde_json::json!({"path": TWO, "force": true}),
        ]
    );
    world.daemon.emit(WorktreeSweptEvent {
        sweep_entry: swept("entry-1", PATH, "deleted", "at your request", 0),
        ..WorktreeSweptEvent::new()
    });
    world.settle();
    assert_eq!(rows(&world), [TWO]);
}

#[test]
fn keeps_a_worktree_whose_delete_the_daemon_refused_and_says_why_on_its_row() {
    let mut world = start(vec![feat_one()]);
    answer(
        &world,
        "delete_worktree",
        "delete_worktree_result",
        serde_json::json!({"success": false, "error": "worktree has uncommitted changes"}),
    );

    click(&mut world, "ledger.inspector.action.1");
    click(&mut world, &format!("ledger.row.{PATH}.verb"));

    assert_eq!(rows(&world), [PATH]);
    assert_eq!(
        name(&world, &format!("ledger.row.{PATH}.note")),
        "worktree has uncommitted changes"
    );
}

#[test]
fn asks_the_daemon_to_refresh_in_the_background_and_says_how_it_went() {
    for (queued, words) in [(true, "asked now"), (false, "no job queue")] {
        let mut world = start(vec![feat_one()]);
        answer(
            &world,
            "worktree_refresh",
            "worktree_refresh_result",
            serde_json::json!({"success": queued}),
        );

        click(&mut world, "ledger.refresh");

        assert_eq!(world.daemon.sent_of("worktree_refresh").len(), 1);
        assert!(
            status(&world).contains(words),
            "{words} in {}",
            status(&world)
        );
    }
}

#[test]
fn reads_the_sweep_log_only_when_removed_is_opened_and_says_who_removed_each_worktree() {
    let mut world = start(vec![feat_one()]);
    serve_log(
        &world,
        vec![
            swept(
                "entry-gone",
                "/projects/attn--feat-gone",
                "removed",
                "merged (ancestor) and clean, idle 19 days",
                120,
            ),
            swept(
                "entry-hand",
                "/projects/attn--feat-byhand",
                "deleted",
                "at your request",
                60,
            ),
        ],
    );
    assert!(world.daemon.sent_of("worktree_sweep_log").is_empty());

    click(&mut world, "ledger.scope.removed");

    assert_eq!(
        sent(&world, "worktree_sweep_log"),
        [serde_json::json!({"limit": 100})]
    );
    assert_eq!(rows(&world), ["entry-hand", "entry-gone"]);
    assert!(meta(&world, "entry-gone").contains("merged (ancestor) and clean, idle 19 days"));
    assert!(meta(&world, "entry-hand").contains("deleted  ·  at your request"));
    assert!(status(&world).contains("2 removed"));
}

#[test]
fn marks_a_worktree_refreshing_from_the_git_operation_on_it_until_that_operation_finishes() {
    let mut world = start(vec![feat_one()]);
    let operation = GitOperation {
        id: "op-1".into(),
        kind: "refresh_repository".into(),
        path: Some(PATH.into()),
        started_at: ago(1),
        status: "running".into(),
        ..GitOperation::default()
    };

    world.daemon.emit(GitOperationStartedMessage {
        operation: operation.clone(),
        ..GitOperationStartedMessage::new()
    });
    world.settle();
    assert!(meta(&world, PATH).contains("refreshing…"));

    world.daemon.emit(GitOperationFinishedMessage {
        operation: GitOperation {
            status: "succeeded".into(),
            finished_at: Some(ago(0)),
            ..operation
        },
        ..GitOperationFinishedMessage::new()
    });
    world.settle();
    assert!(!meta(&world, PATH).contains("refreshing…"));
}

#[test]
fn shows_a_sessions_worktree_and_a_worktrees_sessions_across_the_two_lists() {
    let in_worktree = super::closed(
        attn_protocol::SessionLedgerEntry {
            directory: TWO.into(),
            is_worktree: Some(true),
            ..entry("wt", "run wt", 10)
        },
        "user",
    );
    let elsewhere = super::closed(entry("plain", "run plain", 20), "user");
    let mut world = World::start(ledger_daemon(
        state(vec![], vec![]),
        vec![in_worktree, elsewhere],
    ));
    serve(
        &world,
        vec![feat_one(), worktree(TWO, "feat/two")],
        integration(),
    );
    serve_log(&world, vec![]);
    open(&mut world);
    click(&mut world, "ledger.scope.closed");
    click(&mut world, "ledger.row.plain");
    letter(&mut world, ".");
    assert!(!text_in(&world, "ledger.row.plain-menu").contains("Show worktree"));
    plain(&mut world, Key::Escape);
    click(&mut world, "ledger.row.wt");
    letter(&mut world, ".");
    assert_eq!(name(&world, "ledger.row.wt.menu.1"), "Show worktree");
    letter(&mut world, "2");

    assert!(has(&world, "ledger.refresh"));
    assert_eq!(selected(&world).as_deref(), Some(TWO));
    assert_eq!(focused(&world).as_deref(), Some("ledger.list"));

    letter(&mut world, "2");

    assert!(!has(&world, "ledger.refresh"));
    assert_eq!(query_text(&world), format!("dir:{TWO}"));
    assert_eq!(rows(&world), ["wt"]);
}

#[test]
fn the_palette_opens_the_worktrees_list_and_asks_for_no_sessions() {
    let mut world = world_with(vec![], vec![feat_one()], integration());
    world.gesture(|app| app.key(chord(Action::UiCommandPalette)));
    world.gesture(|app| app.type_text("open worktrees"));
    click(&mut world, "palette.row.command:worktrees");

    assert_eq!(rows(&world), [PATH]);
    assert_eq!(focused(&world).as_deref(), Some("ledger.list"));
    assert!(world.daemon.sent_of("session_list").is_empty());
    letter(&mut world, "[");
    assert!(has(&world, "ledger.scope") && !has(&world, "ledger.refresh"));
}
