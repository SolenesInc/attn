use std::sync::{Arc, Mutex};

use attn_protocol::{DelegateResult, DelegateResultMessage, SeedContinuation};
use attn_testkit::{Request, Target, World};
use serde_json::json;

use super::*;

const SEED: &str = "Parser rewrite";

fn continued(continuation: SeedContinuation) -> Seed {
    with(seed("s-hand11", SEED), |s| {
        s.continuation = Some(continuation)
    })
}

fn placed() -> SeedContinuation {
    SeedContinuation {
        agent: "claude".into(),
        cwd: "/tmp/placed".into(),
        repository_root: Some("/tmp".into()),
        branch: Some("feature".into()),
        handover_placement: "reuse_cwd".into(),
        directory_state: "present".into(),
        ..SeedContinuation::default()
    }
}

fn handed_over(request: &Request) -> Value {
    message(DelegateResultMessage {
        request_id: request["request_id"].as_str().map(str::to_owned),
        success: true,
        result: Some(DelegateResult {
            session_id: "tender".into(),
            ..DelegateResult::default()
        }),
        ..DelegateResultMessage::new()
    })
}

fn opened(
    seeds: Vec<Seed>,
    delegate: impl Fn(&Request) -> Option<Value> + Send + Sync + 'static,
) -> World {
    let daemon = daemon(seeds, Vec::new());
    daemon.on("delegate", delegate);
    let mut world = open_garden_with(daemon);
    open_row(&mut world, SEED);
    world
}

fn value(world: &World, part: &str) -> Option<String> {
    world
        .screen()
        .get(Target::key(format!("garden.handover.{part}")))
        .value
}

fn write(world: &mut World, part: &str, words: &str) {
    world.gesture(|app| app.click(Target::key(format!("garden.handover.{part}"))));
    world.gesture(|app| app.type_text(words));
}

fn checkout(world: &World) -> Option<String> {
    world
        .screen()
        .descendants(Target::key("garden.handover.checkout"))
        .into_iter()
        .filter(|n| n.role == "radio-button")
        .find(|n| n.is("checked"))
        .and_then(|n| n.name)
}

fn cmd_enter(world: &mut World) {
    key_with(
        world,
        Key::Enter,
        Mods {
            cmd: true,
            ..Mods::default()
        },
    );
}

#[test]
fn hands_a_seed_over_to_a_new_agent_with_the_users_note_and_goes_to_it() {
    let mut world = opened(vec![continued(placed())], |r| Some(handed_over(r)));

    click(&mut world, "Handover");
    assert_eq!(
        focused(&world).and_then(|n| n.key).as_deref(),
        Some("garden.handover.field")
    );
    world.gesture(|app| app.type_text("Continue from the parser tests."));
    cmd_enter(&mut world);

    let sent = world.daemon.sent_of("delegate");
    assert_eq!(sent.len(), 1);
    assert_eq!(
        sent[0]["assignment"],
        json!({"kind": "seed", "seed_id": "s-hand11", "handover": {"note": "Continue from the parser tests."}})
    );
    assert_eq!(
        (&sent[0]["cwd"], &sent[0]["checkout"], &sent[0]["agent"]),
        (
            &json!("/tmp/placed"),
            &json!({"kind": "reuse", "branch": "feature"}),
            &json!("claude")
        )
    );
    assert_eq!(
        world.daemon.sent_of("desktop_show_session").last().unwrap()["session_id"],
        "tender"
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.handover.form"))
            .is_none()
    );
    assert!(rests(&mut world));
}

#[test]
fn places_a_handover_where_the_user_says_when_the_saved_folder_cannot_be_reused() {
    let mut world = opened(
        vec![continued(SeedContinuation {
            handover_placement: "placement_required".into(),
            placement_reason: Some("the old directory is unavailable".into()),
            branch: None,
            ..placed()
        })],
        |r| Some(handed_over(r)),
    );

    click(&mut world, "Handover");
    assert_eq!(value(&world, "cwd").as_deref(), Some(""));
    assert_eq!(checkout(&world).as_deref(), Some("Not a Git folder"));
    write(&mut world, "cwd", "/tmp/new-home");
    world.gesture(|app| {
        app.click(Target::role("radio-button").name("Create a new branch worktree"))
    });
    write(&mut world, "branch", "feature/new-home");
    key(&mut world, Key::Enter);
    assert_eq!(
        world
            .screen()
            .find(Target::key("garden.handover.error"))
            .and_then(|n| n.name)
            .as_deref(),
        Some("Start from is required.")
    );
    assert!(world.daemon.sent_of("delegate").is_empty());
    world.gesture(|app| app.click(Target::key("garden.handover.share")));
    write(&mut world, "from", "origin/next");
    key(&mut world, Key::Enter);

    let sent = world.daemon.sent_of("delegate");
    assert_eq!(
        sent[0]["assignment"],
        json!({"kind": "seed", "seed_id": "s-hand11", "handover": {}})
    );
    assert_eq!(
        (
            &sent[0]["cwd"],
            &sent[0]["checkout"],
            &sent[0]["allow_worktree_reuse"],
            &sent[0]["agent"]
        ),
        (
            &json!("/tmp/new-home"),
            &json!({"kind": "new_worktree", "branch": "feature/new-home", "from": "origin/next"}),
            &json!(true),
            &json!("claude")
        )
    );
    assert!(rests(&mut world));
}

#[test]
fn recreates_a_missing_branch_checkout_from_its_repository_context() {
    let mut world = opened(
        vec![continued(SeedContinuation {
            cwd: "/tmp/missing-worktree/subdir".into(),
            directory_state: "missing".into(),
            handover_placement: "recreate_branch".into(),
            repository_root: Some("/tmp/repo".into()),
            repository_subdir: Some("subdir".into()),
            branch: Some("feature/recreate".into()),
            ..placed()
        })],
        |_| None,
    );

    click(&mut world, "Handover");

    assert_eq!(value(&world, "cwd").as_deref(), Some("/tmp/repo/subdir"));
    assert_eq!(
        checkout(&world).as_deref(),
        Some("Create a worktree for an existing branch")
    );
    assert_eq!(value(&world, "branch").as_deref(), Some("feature/recreate"));
    assert!(rests(&mut world));
}

#[test]
fn retries_an_unanswered_handover_under_the_same_request_and_a_refused_one_under_a_new_one() {
    let calls = Arc::new(Mutex::new(0));
    let counted = calls.clone();
    let mut world = opened(vec![continued(placed())], move |r| {
        let mut calls = counted.lock().unwrap();
        *calls += 1;
        (*calls == 2).then(|| {
            message(DelegateResultMessage {
                request_id: r["request_id"].as_str().map(str::to_owned),
                success: false,
                error: Some("the folder is in use".into()),
                ..DelegateResultMessage::new()
            })
        })
    });
    let failure = |world: &World| {
        world
            .screen()
            .find(Target::key("garden.handover.error"))
            .and_then(|n| n.name)
    };

    click(&mut world, "Handover");
    cmd_enter(&mut world);
    world.daemon.disconnect();
    world.settle();
    world.advance(std::time::Duration::from_secs(1));
    assert_eq!(
        failure(&world).as_deref(),
        Some("the daemon did not answer")
    );
    world.gesture(|app| app.click(Target::key("garden.handover.send")));
    assert_eq!(failure(&world).as_deref(), Some("the folder is in use"));
    world.gesture(|app| app.click(Target::key("garden.handover.send")));

    let sent = world.daemon.sent_of("delegate");
    assert_eq!(sent.len(), 3);
    assert_eq!(sent[1]["request_id"], sent[0]["request_id"]);
    assert_ne!(sent[2]["request_id"], sent[1]["request_id"]);
}

#[test]
fn a_handover_after_the_app_restarts_starts_a_new_agent() {
    let operations: Arc<Mutex<Vec<String>>> = Arc::default();
    let hand_over_once = |operations: &Arc<Mutex<Vec<String>>>| {
        let operations = operations.clone();
        let mut world = opened(vec![continued(placed())], move |r| {
            let id = r["request_id"].as_str().unwrap().to_owned();
            let mut operations = operations.lock().unwrap();
            let operation = match operations.iter().position(|known| *known == id) {
                Some(earlier) => earlier,
                None => {
                    operations.push(id.clone());
                    operations.len() - 1
                }
            };
            Some(message(DelegateResultMessage {
                request_id: Some(id),
                success: true,
                result: Some(DelegateResult {
                    session_id: format!("tender-{operation}"),
                    ..DelegateResult::default()
                }),
                ..DelegateResultMessage::new()
            }))
        });
        click(&mut world, "Handover");
        cmd_enter(&mut world);
        world.daemon.sent_of("desktop_show_session").last().unwrap()["session_id"].clone()
    };

    assert_eq!(hand_over_once(&operations), "tender-0");
    assert_eq!(hand_over_once(&operations), "tender-1");
}

#[test]
fn waits_for_the_seeds_current_revision_before_offering_handover() {
    let holding = Arc::new(Mutex::new(false));
    let held = holding.clone();
    let daemon = daemon(Vec::new(), Vec::new());
    let current = Arc::new(Mutex::new(continued(placed())));
    let read = current.clone();
    daemon.on("seed_document_get", move |r: &Request| {
        (!*held.lock().unwrap()).then(|| document_reply(r, Some(read.lock().unwrap().clone())))
    });
    let mut world = World::start_sized(
        daemon.state(initial(vec![continued(placed())], Vec::new())),
        (960.0, 640.0),
    );
    world.gesture(|app| app.key(board()));
    open_row(&mut world, SEED);
    assert!(
        world
            .screen()
            .find(Target::role("button").name("Handover"))
            .is_some()
    );

    *holding.lock().unwrap() = true;
    let newer = with(continued(placed()), |s| s.rev = 2);
    *current.lock().unwrap() = newer.clone();
    push(&mut world, vec![newer]);

    assert!(
        world
            .screen()
            .find(Target::role("button").name("Handover"))
            .is_none()
    );
}
