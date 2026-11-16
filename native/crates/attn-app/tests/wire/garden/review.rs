use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_protocol::{
    DelegateResultMessage, Desktop, DesktopPane, GardenReview, GardenReviewEvidence,
    GardenReviewItem, GardenReviewRecipe, GardenReviewRun, GardenReviewUpdatedMessage,
    SeedContinuation, SeedResumeResultMessage, SeedReviewDraftResultMessage,
    SeedReviewResultMessage, SeedSendToChiefResultMessage, SeedTransitionResultMessage,
};
use attn_testkit::tree::pane;
use attn_testkit::{Request, Target, World};
use serde_json::json;

use super::*;

const DESKTOP: &str = "work/desktop_1";
const SEED: &str = "Review this seed";
const BODY_TEXT: &str = "The implementation is complete. Verify the packaged app.";

fn reviewed() -> Seed {
    with(seed("s-review1", SEED), |s| {
        s.status = "growing".into();
        s.body = BODY_TEXT.into();
        s.rev = 4;
    })
}

fn saved(placement: &str) -> Seed {
    with(reviewed(), |s| {
        s.continuation = Some(SeedContinuation {
            agent: "codex".into(),
            cwd: "/tmp/work".into(),
            repository_root: Some("/tmp/repo".into()),
            branch: Some("feature-x".into()),
            directory_state: "present".into(),
            handover_placement: placement.into(),
            resume_available: true,
            ..SeedContinuation::default()
        })
    })
}

fn item() -> GardenReviewItem {
    GardenReviewItem {
        id: "r-1.s-review1".into(),
        run_id: "r-1".into(),
        seed_id: "s-review1".into(),
        seed_rev: 4,
        evidence_version: "evidence-1".into(),
        title: SEED.into(),
        body: "Check the outcome.".into(),
        evidence: vec![GardenReviewEvidence {
            label: "Review signal".into(),
            text: "The seed is stale.".into(),
        }],
        actions: [
            "handover",
            "send_to_chief",
            "keep_growing",
            "park",
            "harvest",
            "wither",
        ]
        .map(String::from)
        .to_vec(),
        status: "ready".into(),
        resolution: "unresolved".into(),
        recommendation: Some("park".into()),
        explanation: Some("Useful work remains, but it does not need an agent now.".into()),
        ..GardenReviewItem::default()
    }
}

fn advised(f: impl FnOnce(&mut GardenReviewItem)) -> GardenReviewItem {
    let mut item = item();
    f(&mut item);
    item
}

fn run(id: &str, captured_at: &str, items: Vec<GardenReviewItem>) -> GardenReview {
    GardenReview {
        run: GardenReviewRun {
            profile_id: PROFILE.into(),
            id: id.into(),
            candidate_ids: items.iter().map(|i| i.seed_id.clone()).collect(),
            recipe: GardenReviewRecipe {
                agent: "codex".into(),
                model: "gpt-5.6-luna".into(),
                effort: Some("xhigh".into()),
            },
            status: "running".into(),
            captured_at: captured_at.into(),
            ..GardenReviewRun::default()
        },
        items,
    }
}

fn review(items: Vec<GardenReviewItem>) -> GardenReview {
    run("r-1", "2026-11-12T08:00:00Z", items)
}

fn receipt() -> Value {
    json!({ "review_id": "r-1", "evidence_version": "evidence-1" })
}

fn result(r: &Request, operation: &str, review: Option<GardenReview>, count: i64) -> Value {
    message(SeedReviewResultMessage {
        request_id: r["request_id"].as_str().unwrap().into(),
        profile_id: PROFILE.into(),
        operation: operation.into(),
        success: true,
        candidate_count: count,
        review,
        ..SeedReviewResultMessage::new()
    })
}

fn unresolved(review: &Option<GardenReview>) -> i64 {
    review.as_ref().map_or(0, |r| {
        r.items
            .iter()
            .filter(|i| i.resolution == "unresolved")
            .count() as i64
    })
}

fn chief() -> Session {
    Session {
        chief_of_staff: Some(true),
        ..session("chief1", "Chief")
    }
}

fn desktop() -> Desktop {
    Desktop {
        id: DESKTOP.into(),
        profile_id: PROFILE.into(),
        name: "Desktop 1".into(),
        shortcut_slot: Some(1),
        order_key: "1".into(),
        tree_json: pane("a").to_string(),
        active_pane_id: "a".into(),
        panes: vec![DesktopPane {
            desktop_id: DESKTOP.into(),
            pane_id: "a".into(),
            runtime_id: "r-a".into(),
            session_id: "s1".into(),
            kind: "agent".into(),
            ..DesktopPane::default()
        }],
        revision: 1,
    }
}

/// What the daemon answers the overview with, which a test may change.
type Showing = Arc<Mutex<(Option<GardenReview>, i64)>>;

struct Script {
    seeds: Vec<Seed>,
    chief: bool,
    shown: Option<GardenReview>,
    candidates: Option<i64>,
}

impl Script {
    fn of(shown: Option<GardenReview>) -> Script {
        Script {
            seeds: vec![reviewed()],
            chief: true,
            shown,
            candidates: None,
        }
    }
}

/// The garden open beside the agent `s1`, with a daemon that answers the overview with
/// `script.shown`, takes every move, and leaves the other review commands to the test.
fn garden(script: Script) -> (World, Showing) {
    let mut sessions = vec![session("s1", "Agent a")];
    if script.chief {
        sessions.push(chief());
    }
    let known = script.seeds.clone();
    let count = script
        .candidates
        .unwrap_or_else(|| unresolved(&script.shown));
    let showing: Showing = Arc::new(Mutex::new((script.shown, count)));
    let daemon = daemon(script.seeds.clone(), sessions.clone()).state(InitialStateMessage {
        profiles: Some(vec![Profile {
            id: PROFILE.into(),
            name: "Work".into(),
            current_desktop_id: DESKTOP.into(),
            ..Profile::default()
        }]),
        desktops: Some(vec![desktop()]),
        ..initial(script.seeds, sessions)
    });
    let answer = showing.clone();
    daemon
        .on("seed_review_show", move |r: &Request| {
            let (shown, count) = answer.lock().unwrap().clone();
            result(r, "show", shown, count)
        })
        .on("seed_transition", move |r: &Request| {
            message(SeedTransitionResultMessage {
                request_id: r["request_id"].as_str().unwrap().into(),
                success: true,
                seed: known.iter().find(|s| s.id == r["seed_id"]).cloned(),
                ..SeedTransitionResultMessage::new()
            })
        });
    let mut world = World::start_sized(daemon, (1400.0, 900.0));
    world.open_desktop();
    world.gesture(|app| app.key(board()));
    (world, showing)
}

fn open_review(item: GardenReviewItem, seeds: Vec<Seed>) -> (World, Showing) {
    let (mut world, showing) = garden(Script {
        seeds,
        ..Script::of(Some(review(vec![item])))
    });
    click(&mut world, "Continue review");
    (world, showing)
}

fn reviewing(item: GardenReviewItem) -> World {
    open_review(item, vec![reviewed()]).0
}

fn has(world: &World, name: &str) -> bool {
    world
        .screen()
        .find(Target::role("button").name(name))
        .is_some()
}

fn says(world: &World, key: &str) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key(key))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn advice(world: &World) -> Vec<String> {
    says(world, "garden.review.advice")
}

fn decides(world: &World) -> bool {
    world
        .screen()
        .find(Target::role("heading").name("What should happen?"))
        .is_some()
}

fn prompt(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("garden.review-prompt"))
        .and_then(|n| n.name)
}

fn alert(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::role("alert"))
        .and_then(|n| n.name)
}

fn field(world: &World, key: &str) -> Option<String> {
    world
        .screen()
        .find(Target::key(format!("garden.review.{key}")))
        .and_then(|n| n.value)
}

fn type_into(world: &mut World, key: &str, words: &str) {
    world.gesture(|app| app.click(Target::key(format!("garden.review.{key}"))));
    world.gesture(|app| app.type_text(words));
}

fn press(world: &mut World, key: &str) {
    world.gesture(|app| app.click(Target::key(format!("garden.review.{key}"))));
}

fn emit(world: &mut World, review: GardenReview) {
    world.daemon.emit(GardenReviewUpdatedMessage {
        review,
        ..GardenReviewUpdatedMessage::new()
    });
    world.settle();
}

fn transitions(world: &World) -> Vec<Request> {
    world.daemon.sent_of("seed_transition")
}

fn checkout(world: &World) -> Option<String> {
    world
        .screen()
        .descendants(Target::key("garden.review.handover.checkout"))
        .into_iter()
        .filter(|n| n.role == "radio-button")
        .find(|n| n.is("checked"))
        .and_then(|n| n.name)
}

#[test]
fn shows_a_failed_resume_after_its_review_decision_has_settled() {
    let item = advised(|i| {
        i.actions = vec!["resume".into()];
        i.recommendation = Some("resume".into());
    });
    let (mut world, showing) = open_review(item, vec![saved("reuse_cwd")]);

    click(&mut world, "Resume");
    let held = world.daemon.sent_of("seed_resume");
    assert_eq!(held.len(), 1);
    assert_eq!(held[0]["seed_id"], "s-review1");
    assert_eq!(held[0]["review"], receipt());
    let mut completed = review(vec![advised(|i| {
        i.resolution = "resolved".into();
        i.resolved_action = Some("resume".into());
    })]);
    completed.run.status = "complete".into();
    *showing.lock().unwrap() = (Some(completed.clone()), 0);
    emit(&mut world, completed);
    assert!(
        world
            .screen()
            .find(Target::role("heading").name("Garden review complete"))
            .is_some()
    );

    world.daemon.reply_to(
        &held[0],
        SeedResumeResultMessage {
            request_id: held[0]["request_id"].as_str().unwrap().into(),
            success: false,
            error: Some("Worker could not start".into()),
            ..SeedResumeResultMessage::new()
        },
    );
    world.settle();

    assert_eq!(alert(&world).as_deref(), Some("Worker could not start"));
    assert!(
        world
            .screen()
            .find(Target::role("heading").name("Garden review complete"))
            .is_some()
    );
    assert!(rests(&mut world));
}

#[test]
fn counts_the_seeds_that_need_review_and_starts_a_new_review() {
    let (mut world, _) = garden(Script {
        candidates: Some(3),
        ..Script::of(None)
    });
    world.daemon.on("seed_review_start", |r: &Request| {
        result(r, "start", Some(review(vec![item()])), 3)
    });
    assert_eq!(prompt(&world).as_deref(), Some("3 seeds need review"));
    assert!(!decides(&world));

    click(&mut world, "Review garden");

    assert_eq!(world.daemon.sent_of("seed_review_start").len(), 1);
    assert!(decides(&world));
    assert!(rests(&mut world));
}

#[test]
fn waits_for_a_review_start_however_long_model_discovery_takes() {
    let (mut world, _) = garden(Script {
        candidates: Some(3),
        ..Script::of(None)
    });
    click(&mut world, "Review garden");
    world.advance(Duration::from_secs(60));
    assert!(shows(&world, "Starting review…"));
    assert!(!has(&world, "Review garden"));

    let held = world.daemon.sent_of("seed_review_start");
    world.daemon.reply_to(
        &held[0],
        result(&held[0], "start", Some(review(vec![item()])), 3),
    );
    world.settle();

    assert!(decides(&world));
    assert!(!shows(&world, "Starting review…"));
    assert!(rests(&mut world));
}

#[test]
fn opens_the_review_the_daemon_shows_and_takes_its_progressive_updates() {
    let mut world = reviewing(item());
    assert!(advice(&world).iter().any(|w| w == "Park"));

    emit(
        &mut world,
        review(vec![advised(|i| {
            i.recommendation = Some("harvest".into());
            i.explanation = Some("The work is done.".into());
        })]),
    );

    let advice = advice(&world);
    assert!(advice.iter().any(|w| w == "Harvest"), "{advice:?}");
    assert!(
        advice.iter().any(|w| w == "The work is done."),
        "{advice:?}"
    );
    assert!(rests(&mut world));
}

#[test]
fn moves_the_overview_to_a_newer_review_run_and_ignores_an_older_broadcast() {
    let mut old = run("r-old", "2026-11-12T08:00:00Z", Vec::new());
    old.run.status = "completed".into();
    let (mut world, _) = garden(Script::of(Some(old)));
    assert_eq!(prompt(&world), None);

    let newer = advised(|i| {
        i.id = "r-new.s-review1".into();
        i.run_id = "r-new".into();
    });
    emit(
        &mut world,
        run("r-new", "2026-11-12T09:00:00Z", vec![newer]),
    );
    assert_eq!(prompt(&world).as_deref(), Some("1 seed needs review"));

    emit(
        &mut world,
        run(
            "r-old",
            "2026-11-12T08:00:00Z",
            vec![advised(|i| i.recommendation = Some("wither".into()))],
        ),
    );
    click(&mut world, "Continue review");

    assert!(advice(&world).iter().any(|w| w == "Park"));
    assert!(rests(&mut world));
}

#[test]
fn follows_an_outposts_reach_on_an_open_item_and_keeps_its_advice() {
    let unreachable = advised(|i| {
        i.actions = ["send_to_chief", "keep_growing", "park", "harvest", "wither"]
            .map(String::from)
            .to_vec();
        i.evidence = vec![GardenReviewEvidence {
            label: "Working directory".into(),
            text: "The saved working directory is on outpost \"box\", which is unreachable.".into(),
        }];
    });
    let mut world = reviewing(unreachable);
    assert!(!has(&world, "Resume"));
    let known = says(&world, "garden.review.evidence");
    assert!(
        known.iter().any(|w| w.ends_with("which is unreachable.")),
        "{known:?}"
    );

    emit(
        &mut world,
        review(vec![advised(|i| {
            i.actions = [
                "resume",
                "send_to_chief",
                "keep_growing",
                "park",
                "harvest",
                "wither",
            ]
            .map(String::from)
            .to_vec();
            i.outpost = Some("box".into());
            i.evidence = vec![GardenReviewEvidence {
                label: "Working directory".into(),
                text: "The saved working directory is on outpost \"box\".".into(),
            }];
        })]),
    );

    assert!(has(&world, "Resume on box"));
    let known = says(&world, "garden.review.evidence");
    assert!(
        !known.iter().any(|w| w.ends_with("which is unreachable.")),
        "{known:?}"
    );
    assert!(advice(&world).iter().any(|w| w == "Park"));
    assert!(rests(&mut world));
}

#[test]
fn resumes_on_the_outpost_the_button_names() {
    let item = advised(|i| {
        i.actions = vec!["resume".into(), "park".into()];
        i.outpost = Some("box".into());
    });
    let mut world = open_review(item, vec![saved("reuse_cwd")]).0;

    click(&mut world, "Resume on box");

    let resumed = world.daemon.sent_of("seed_resume");
    assert_eq!(resumed.len(), 1);
    assert_eq!(resumed[0]["seed_id"], "s-review1");
    assert_eq!(resumed[0]["review"], receipt());
    assert!(rests(&mut world));
}

#[test]
fn offers_only_the_actions_the_captured_item_allows() {
    let mut world = reviewing(advised(|i| {
        i.actions = vec!["handover".into(), "park".into()];
        i.recommendation = Some("handover".into());
    }));

    assert!(has(&world, "Handover"));
    assert!(has(&world, "Park"));
    for absent in [
        "Resume",
        "Harvest",
        "Wither",
        "Keep growing",
        "Send to Chief",
    ] {
        assert!(!has(&world, absent), "{absent}");
    }
    assert!(rests(&mut world));
}

#[test]
fn harvests_with_the_advisors_guidance_when_it_recommended_harvesting_and_the_completion_statement_otherwise()
 {
    let mut world = reviewing(advised(|i| {
        i.recommendation = Some("harvest".into());
        i.explanation = Some("The outcome is complete and verified.".into());
    }));
    click(&mut world, "Harvest");
    let sent = transitions(&world);
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["seed_id"], "s-review1");
    assert_eq!(sent[0]["verb"], "harvest");
    assert_eq!(sent[0]["reason"], "The outcome is complete and verified.");
    assert_eq!(sent[0]["review"], receipt());

    let mut world = reviewing(item());
    click(&mut world, "Harvest");
    let sent = transitions(&world);
    assert_eq!(
        sent[0]["reason"],
        "The seed's stated outcome and required verification are complete."
    );
    assert_eq!(sent[0]["review"], receipt());
    assert!(rests(&mut world));
}

#[test]
fn prefills_the_optional_park_and_wither_comments_from_matching_advice() {
    for (action, part) in [("Park", "park"), ("Wither", "wither")] {
        let explanation = format!("{action} this seed because its saved context explains why.");
        let mut world = reviewing(advised(|i| {
            i.recommendation = Some(part.into());
            i.explanation = Some(explanation.clone());
        }));

        click(&mut world, action);

        assert_eq!(field(&world, &format!("{part}.field")), Some(explanation));
        assert!(rests(&mut world));
    }
}

#[test]
fn sends_the_review_receipt_with_a_park_comment_in_one_move() {
    let mut world = reviewing(advised(|i| i.recommendation = None));

    click(&mut world, "Park");
    world.gesture(|app| app.type_text("Waiting for input."));
    press(&mut world, "park.send");

    let sent = transitions(&world);
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["seed_id"], "s-review1");
    assert_eq!(sent[0]["verb"], "park");
    assert_eq!(sent[0]["comment"], "Waiting for input.");
    assert_eq!(sent[0]["review"], receipt());
    assert!(sent[0].get("reason").is_none_or(Value::is_null));
    assert!(rests(&mut world));
}

#[test]
fn keeps_a_reviewed_seed_growing_with_its_evidence_receipt_and_reads_the_review_again() {
    let mut world = reviewing(item());
    world.daemon.on("seed_review_keep", |r: &Request| {
        result(r, "keep", Some(review(vec![item()])), 1)
    });
    let shows_before = world.daemon.sent_of("seed_review_show").len();

    click(&mut world, "Keep growing");

    let kept = world.daemon.sent_of("seed_review_keep");
    assert_eq!(kept.len(), 1);
    assert_eq!(kept[0]["seed_id"], "s-review1");
    assert_eq!(kept[0]["review"], receipt());
    let shows = world.daemon.sent_of("seed_review_show");
    assert_eq!(shows.len(), shows_before + 1);
    assert_eq!(shows.last().unwrap()["review_id"], "r-1");
    assert!(rests(&mut world));
}

#[test]
fn shows_the_advisors_progress_without_making_the_user_wait_to_act() {
    let mut world = reviewing(advised(|i| {
        i.status = "queued".into();
        i.recommendation = None;
        i.explanation = None;
        i.advisor_state = Some("retrying".into());
        i.advisor_attempt = Some(2);
        i.advisor_max_attempts = Some(3);
        i.advisor_retry_at = Some("2099-08-30T09:02:00Z".into());
        i.advisor_error = Some("The last answer did not match the expected format.".into());
    }));

    assert!(shows(
        &world,
        "Attempt 2 of 3 did not produce usable advice."
    ));
    assert!(shows(
        &world,
        "The last answer did not match the expected format."
    ));
    assert!(shows(
        &world,
        "You can choose an action now. Advice will appear here if it arrives first."
    ));
    assert!(shows(&world, "Advice ready for 0 of 1 seeds."));
    assert!(has(&world, "Harvest"));
    assert!(has(&world, "Handover"));
    assert!(rests(&mut world));
}

#[test]
fn offers_a_retry_for_changed_advice_without_lifecycle_actions() {
    let mut world = reviewing(advised(|i| {
        i.status = "invalidated".into();
        i.recommendation = None;
        i.explanation = None;
        i.error = Some("The seed changed during classification.".into());
    }));
    world.daemon.on("seed_review_retry", |r: &Request| {
        result(r, "retry", Some(review(vec![item()])), 1)
    });
    assert!(
        world
            .screen()
            .find(Target::role("heading").name("This seed changed"))
            .is_some()
    );
    assert!(shows(&world, "The seed changed during classification."));
    assert!(!has(&world, "Harvest"));

    click(&mut world, "Try again");

    let retried = world.daemon.sent_of("seed_review_retry");
    assert_eq!(retried.len(), 1);
    assert_eq!(retried[0]["review_id"], "r-1");
    assert_eq!(retried[0]["seed_id"], "s-review1");
    assert!(advice(&world).iter().any(|w| w == "Park"));
    assert!(rests(&mut world));
}

#[test]
fn reads_the_reviewed_seeds_body_and_log_without_a_way_to_mark_it() {
    let (mut world, _) = garden(Script::of(Some(review(vec![item()]))));
    world.daemon.on("seed_document_get", |r: &Request| {
        logged(r, reviewed(), "n-review1")
    });
    click(&mut world, "Continue review");

    let log = says(&world, "garden.review.log");
    assert!(log.iter().any(|w| w == "n-review1 body"), "{log:?}");
    assert!(
        world
            .screen()
            .find(Target::key("garden.review.plan.s-review1"))
            .is_some()
    );
    assert!(!has(&world, "Overall note"));
    assert!(!has(&world, "+ Overall note"));
    assert!(rests(&mut world));
}

#[test]
fn walks_up_and_down_a_plot_and_returns_to_the_reviewed_seed() {
    let reviewed = with(reviewed(), |s| s.edges.push(edge("part-of", "s-plot11")));
    let sibling = with(part_of("s-plot11", "s-sibling", "Check the sibling"), |s| {
        s.body = "Sibling context.".into()
    });
    let plot = with(plot("s-plot11", "Ship the review garden", ""), |s| {
        s.body = "The whole plot.".into()
    });
    let seeds = vec![reviewed.clone(), sibling.clone(), plot.clone()];
    let (mut world, _) = open_review(item(), seeds.clone());
    let children = vec![reviewed.clone(), sibling.clone()];
    world.daemon.on("seed_document_get", move |r: &Request| {
        let seed = seeds
            .iter()
            .find(|s| s.id == r["seed_id"])
            .cloned()
            .unwrap();
        let mut reply = document_reply(r, Some(seed.clone()));
        if seed.id == "s-plot11" {
            reply["document"]["children"] = serde_json::to_value(&children).unwrap();
        }
        reply
    });

    click(&mut world, "Ship the review garden");
    click(&mut world, "Check the sibling");

    assert!(
        world
            .screen()
            .find(Target::key("garden.review.plan.s-sibling"))
            .is_some()
    );
    assert_eq!(
        world
            .screen()
            .find(Target::key("garden.review.head.title"))
            .and_then(|n| n.name)
            .as_deref(),
        Some("Check the sibling")
    );
    let context = says(&world, "garden.review.context");
    assert!(
        context
            .iter()
            .any(|w| w == "You are looking around the plot")
    );
    assert!(
        context
            .iter()
            .any(|w| w == "The review decision still belongs to Review this seed.")
    );
    assert!(!decides(&world));
    let reads = world.daemon.sent_of("seed_document_get").len();

    click(&mut world, "‹ Review seed");

    assert_eq!(
        world
            .screen()
            .find(Target::key("garden.review.head.title"))
            .and_then(|n| n.name)
            .as_deref(),
        Some(SEED)
    );
    assert!(decides(&world));
    let read = world.daemon.sent_of("seed_document_get");
    assert!(read[reads..].iter().any(|r| r["seed_id"] == "s-review1"));
    assert!(rests(&mut world));
}

const NEXT: &str = "Review the next seed";

fn next() -> Seed {
    with(seed("s-review2", NEXT), |s| {
        s.status = "growing".into();
        s.body = "The next seed's plan.".into();
    })
}

fn next_item() -> GardenReviewItem {
    GardenReviewItem {
        id: "r-1.s-review2".into(),
        seed_id: "s-review2".into(),
        title: NEXT.into(),
        recommendation: Some("harvest".into()),
        explanation: Some("The next seed's work is done.".into()),
        ..item()
    }
}

fn reviewing_two() -> (World, Showing) {
    let (mut world, showing) = garden(Script {
        seeds: vec![reviewed(), next()],
        ..Script::of(Some(review(vec![item(), next_item()])))
    });
    world.daemon.on("seed_document_get", |r: &Request| {
        let seed = [reviewed(), next()]
            .into_iter()
            .find(|s| s.id == r["seed_id"]);
        document_reply(r, seed)
    });
    click(&mut world, "Continue review");
    (world, showing)
}

fn read_title(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("garden.review.head.title"))
        .and_then(|n| n.name)
}

fn reads_the_next_seed_and_decides_on_it(world: &World) {
    assert_eq!(read_title(world).as_deref(), Some(NEXT));
    assert!(
        world
            .screen()
            .find(Target::key("garden.review.plan.s-review2"))
            .is_some()
    );
    assert!(decides(world));
    let advice = advice(world);
    assert!(
        advice.iter().any(|w| w == "The next seed's work is done."),
        "{advice:?}"
    );
}

#[test]
fn reads_and_decides_on_the_item_chosen_from_the_rail() {
    let (mut world, _) = reviewing_two();
    assert_eq!(read_title(&world).as_deref(), Some(SEED));

    press(&mut world, "rail.r-1.s-review2");

    reads_the_next_seed_and_decides_on_it(&world);
    assert!(rests(&mut world));
}

#[test]
fn reads_and_decides_on_the_next_item_once_one_is_resolved() {
    let (mut world, showing) = reviewing_two();
    world
        .daemon
        .on("seed_review_keep", |r: &Request| result(r, "keep", None, 1));
    let kept = review(vec![advised(|i| i.resolution = "kept".into()), next_item()]);
    *showing.lock().unwrap() = (Some(kept), 1);

    click(&mut world, "Keep growing");

    reads_the_next_seed_and_decides_on_it(&world);
    assert!(rests(&mut world));
}

#[test]
fn stays_on_the_plot_seed_the_user_went_to_while_the_review_updates() {
    let reviewed = with(reviewed(), |s| s.edges.push(edge("part-of", "s-plot11")));
    let sibling = part_of("s-plot11", "s-sibling", "Check the sibling");
    let plot = plot("s-plot11", "Ship the review garden", "");
    let seeds = vec![reviewed.clone(), sibling.clone(), plot.clone()];
    let (mut world, _) = open_review(item(), seeds.clone());
    let children = vec![reviewed, sibling];
    world.daemon.on("seed_document_get", move |r: &Request| {
        let seed = seeds.iter().find(|s| s.id == r["seed_id"]).cloned();
        let mut reply = document_reply(r, seed.clone());
        if seed.is_some_and(|s| s.id == "s-plot11") {
            reply["document"]["children"] = serde_json::to_value(&children).unwrap();
        }
        reply
    });
    click(&mut world, "Ship the review garden");
    click(&mut world, "Check the sibling");

    emit(
        &mut world,
        review(vec![advised(|i| {
            i.explanation = Some("Advice that arrived later.".into());
        })]),
    );

    assert_eq!(read_title(&world).as_deref(), Some("Check the sibling"));
    assert!(!decides(&world));
    click(&mut world, "‹ Review seed");
    assert_eq!(read_title(&world).as_deref(), Some(SEED));
    assert!(
        advice(&world)
            .iter()
            .any(|w| w == "Advice that arrived later.")
    );
    assert!(rests(&mut world));
}

fn handover_review(item: GardenReviewItem, seed: Seed) -> World {
    open_review(item, vec![seed]).0
}

#[test]
fn fills_the_handover_composer_with_the_advisory_draft() {
    let mut world = handover_review(item(), saved("reuse_cwd"));
    world.daemon.on("seed_review_draft", |r: &Request| {
        message(SeedReviewDraftResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            handoff: Some("Inspect the remaining edge and run the focused test.".into()),
            ..SeedReviewDraftResultMessage::new()
        })
    });

    click(&mut world, "Handover");
    click(&mut world, "Draft");

    assert_eq!(
        field(&world, "handover.field").as_deref(),
        Some("Inspect the remaining edge and run the focused test.")
    );
    let drafted = world.daemon.sent_of("seed_review_draft");
    assert_eq!(drafted.len(), 1);
    assert_eq!(drafted[0]["seed_id"], "s-review1");
    assert_eq!(drafted[0]["review"], receipt());
    assert!(rests(&mut world));
}

#[test]
fn keeps_the_users_edits_when_the_advisory_draft_arrives_late() {
    let mut world = handover_review(item(), saved("reuse_cwd"));
    click(&mut world, "Handover");
    click(&mut world, "Draft");
    assert!(shows(&world, "Drafting…"));
    type_into(&mut world, "handover.field", "My own handoff");

    let drafted = world.daemon.sent_of("seed_review_draft");
    world.daemon.reply_to(
        &drafted[0],
        SeedReviewDraftResultMessage {
            request_id: drafted[0]["request_id"].as_str().unwrap().into(),
            success: true,
            handoff: Some("Late generated handoff".into()),
            ..SeedReviewDraftResultMessage::new()
        },
    );
    world.settle();

    assert_eq!(
        field(&world, "handover.field").as_deref(),
        Some("My own handoff")
    );
    assert!(shows(&world, "Draft ready. Your edits were kept."));
    click(&mut world, "Use draft");
    assert_eq!(
        field(&world, "handover.field").as_deref(),
        Some("Late generated handoff")
    );
    assert!(!shows(&world, "Draft ready. Your edits were kept."));
    assert!(rests(&mut world));
}

#[test]
fn starts_a_handover_from_the_saved_working_context_or_asks_for_placement_when_it_cannot_be_reused()
{
    let mut world = handover_review(item(), saved("reuse_cwd"));
    click(&mut world, "Handover");
    assert_eq!(field(&world, "handover.cwd").as_deref(), Some("/tmp/work"));
    assert_eq!(checkout(&world).as_deref(), Some("Reuse this checkout"));
    assert_eq!(
        field(&world, "handover.branch").as_deref(),
        Some("feature-x")
    );
    assert_eq!(field(&world, "handover.agent").as_deref(), Some("codex"));

    let mut world = handover_review(item(), saved("placement_required"));
    click(&mut world, "Handover");
    assert_eq!(field(&world, "handover.cwd").as_deref(), Some(""));
    assert_eq!(checkout(&world).as_deref(), Some("Not a Git folder"));
    assert!(rests(&mut world));
}

#[test]
fn keeps_the_handoff_text_when_the_new_agent_fails_to_launch() {
    let mut world = handover_review(item(), saved("reuse_cwd"));
    world.daemon.on("delegate", |r: &Request| {
        message(DelegateResultMessage {
            request_id: r["request_id"].as_str().map(str::to_owned),
            success: false,
            error: Some("Worker could not start".into()),
            ..DelegateResultMessage::new()
        })
    });
    click(&mut world, "Handover");
    world.gesture(|app| app.type_text("Keep this text"));
    press(&mut world, "handover.share");

    press(&mut world, "handover.send");

    assert_eq!(alert(&world).as_deref(), Some("Worker could not start"));
    assert_eq!(
        field(&world, "handover.field").as_deref(),
        Some("Keep this text")
    );
    let sent = world.daemon.sent_of("delegate");
    assert_eq!(sent.len(), 1);
    assert_eq!(
        sent[0]["assignment"],
        json!({ "kind": "seed", "seed_id": "s-review1", "handover": { "note": "Keep this text" } })
    );
    assert_eq!(sent[0]["cwd"], "/tmp/work");
    assert_eq!(
        sent[0]["checkout"],
        json!({ "kind": "reuse", "branch": "feature-x" })
    );
    assert_eq!(sent[0]["allow_worktree_reuse"], true);
    assert_eq!(sent[0]["review"], receipt());
    assert!(rests(&mut world));
}

#[test]
fn offers_send_to_chief_only_while_the_selected_profile_has_a_chief() {
    let (mut world, _) = garden(Script {
        chief: false,
        ..Script::of(Some(review(vec![item()])))
    });
    click(&mut world, "Continue review");

    assert!(has(&world, "Park"));
    assert!(!has(&world, "Send to Chief"));
    assert!(rests(&mut world));
}

#[test]
fn sends_a_seed_to_chief_from_the_active_session_with_optional_guidance() {
    let mut world = reviewing(item());
    world.daemon.on("seed_send_to_chief", |r: &Request| {
        message(SeedSendToChiefResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            ..SeedSendToChiefResultMessage::new()
        })
    });

    click(&mut world, "Send to Chief");
    assert_eq!(field(&world, "handover.cwd"), None);
    world.gesture(|app| app.type_text("Use branch feature/special under /tmp/special."));
    press(&mut world, "chief.send");

    assert_eq!(field(&world, "chief.field"), None);
    let sent = world.daemon.sent_of("seed_send_to_chief");
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["source_session_id"], "s1");
    assert_eq!(sent[0]["seed_id"], "s-review1");
    assert_eq!(sent[0]["expected_rev"], 4);
    assert_eq!(
        sent[0]["guidance"],
        "Use branch feature/special under /tmp/special."
    );
    assert_eq!(sent[0]["review"], receipt());
    assert!(rests(&mut world));
}

#[test]
fn closes_the_composer_before_leaving_the_review_on_escape() {
    let mut world = handover_review(item(), saved("reuse_cwd"));
    click(&mut world, "Handover");

    key(&mut world, Key::Escape);
    assert_eq!(field(&world, "handover.field"), None);
    assert!(decides(&world));

    key(&mut world, Key::Escape);
    assert!(!decides(&world));
    assert_eq!(prompt(&world).as_deref(), Some("1 seed needs review"));
    assert_eq!(shown(&world), "dock");
    assert!(rests(&mut world));
}

#[test]
fn reads_the_overview_when_the_garden_opens_and_when_a_row_changes_never_on_a_push_that_changes_none()
 {
    let other = seed("s-other1", "Another seed");
    let (mut world, _) = garden(Script {
        seeds: vec![reviewed(), other.clone()],
        ..Script::of(Some(review(vec![item()])))
    });
    let overviews = |world: &World| world.daemon.sent_of("seed_review_show").len();
    assert_eq!(overviews(&world), 1);

    let order = vec!["s-review1".to_owned(), "s-other1".to_owned()];
    let change = |seeds: Vec<Seed>| GardenSeedsChangedMessage {
        profile_id: PROFILE.into(),
        order: order.clone(),
        seeds,
        total: 2,
        ..GardenSeedsChangedMessage::new()
    };
    world.daemon.emit(change(Vec::new()));
    world.settle();
    assert_eq!(overviews(&world), 1);

    world.daemon.emit(change(vec![with(other, |s| s.rev = 2)]));
    world.settle();
    assert_eq!(overviews(&world), 2);

    world.gesture(|app| app.click(Target::key("garden.close")));
    assert_eq!(shown(&world), "closed");
    world
        .daemon
        .emit(change(vec![with(reviewed(), |s| s.rev = 5)]));
    world.settle();
    assert_eq!(overviews(&world), 2);

    world.gesture(|app| app.key(board()));
    assert_eq!(overviews(&world), 3);
    assert!(
        world
            .daemon
            .sent_of("seed_review_show")
            .iter()
            .all(|r| r.get("review_id").is_none_or(Value::is_null))
    );
    assert!(rests(&mut world));
}

#[test]
fn reads_the_overview_again_each_time_an_empty_garden_opens() {
    let (mut world, _) = garden(Script {
        seeds: Vec::new(),
        ..Script::of(None)
    });
    assert_eq!(world.daemon.sent_of("seed_review_show").len(), 1);

    world.gesture(|app| app.click(Target::key("garden.close")));
    world.gesture(|app| app.key(board()));

    assert_eq!(world.daemon.sent_of("seed_review_show").len(), 2);
    assert!(rests(&mut world));
}

#[test]
fn a_profile_switch_drops_the_review_another_profile_was_shown() {
    let (mut world, _) = garden(Script::of(Some(review(vec![item()]))));
    emit(&mut world, review(vec![item()]));
    assert_eq!(prompt(&world).as_deref(), Some("1 seed needs review"));

    let side = Profile {
        id: "side".into(),
        name: "Side".into(),
        current_desktop_id: "side/desktop_1".into(),
        ..Profile::default()
    };
    world.daemon.emit(attn_protocol::ProfilesChangedMessage {
        profiles: vec![
            Profile {
                id: PROFILE.into(),
                name: "Work".into(),
                ..Profile::default()
            },
            side.clone(),
        ],
        ..attn_protocol::ProfilesChangedMessage::new()
    });
    world
        .daemon
        .emit(attn_protocol::ProfileArrangementChangedMessage {
            profile: side,
            desktops: vec![Desktop {
                id: "side/desktop_1".into(),
                profile_id: "side".into(),
                name: "Desktop 1".into(),
                order_key: "1".into(),
                tree_json: json!({}).to_string(),
                revision: 1,
                ..Desktop::default()
            }],
            ..attn_protocol::ProfileArrangementChangedMessage::new()
        });
    world.settle();

    assert_eq!(prompt(&world), None);
    assert!(!has(&world, "Continue review"));
    assert!(rests(&mut world));
}
