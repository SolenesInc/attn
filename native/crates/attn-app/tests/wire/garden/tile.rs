use attn_protocol::{Desktop, DesktopPane, ProfileArrangementChangedMessage};
use attn_testkit::tree::{pane, row};
use attn_testkit::{Request, Target, World, message};
use serde_json::{Value, json};

use super::*;

const DESKTOP: &str = "work/desktop_1";

fn seed_tile(id: &str) -> Value {
    json!({
        "type": "tile",
        "tile_id": "t",
        "tile_kind": "seed",
        "tile_params": id,
    })
}

fn desktop(tree: Value, revision: i64) -> Desktop {
    Desktop {
        id: DESKTOP.into(),
        profile_id: PROFILE.into(),
        name: "Desktop 1".into(),
        shortcut_slot: Some(1),
        order_key: "1".into(),
        tree_json: tree.to_string(),
        active_pane_id: "a".into(),
        panes: vec![DesktopPane {
            desktop_id: DESKTOP.into(),
            pane_id: "a".into(),
            runtime_id: "r-a".into(),
            session_id: "agent".into(),
            kind: "agent".into(),
            ..DesktopPane::default()
        }],
        revision,
    }
}

fn profile() -> Profile {
    Profile {
        id: PROFILE.into(),
        name: "Work".into(),
        current_desktop_id: DESKTOP.into(),
        ..Profile::default()
    }
}

/// A desktop holding an agent beside a tile of seed `id`, over a garden of `seeds`, with a
/// daemon that answers reads through `read`.
pub(super) fn start(
    id: &str,
    seeds: Vec<Seed>,
    read: impl Fn(&Request) -> Value + Send + Sync + 'static,
) -> World {
    let daemon = Daemon::scripted().state(InitialStateMessage {
        profiles: Some(vec![profile()]),
        desktops: Some(vec![desktop(row(vec![pane("a"), seed_tile(id)]), 1)]),
        ..initial(seeds, vec![session("agent", "Agent a")])
    });
    daemon.on("seed_document_get", read);
    let mut world = World::start_sized(daemon, (1600.0, 900.0));
    world.open_desktop();
    world
}

pub(super) fn document(
    request: &Request,
    seed: Seed,
    children: Vec<Seed>,
    notes: &[&str],
) -> Value {
    message(SeedDocumentGetResultMessage {
        request_id: request["request_id"].as_str().unwrap().into(),
        success: true,
        document: Some(SeedDocument {
            notes: notes
                .iter()
                .map(|n| SeedNote {
                    id: (*n).into(),
                    seed_id: seed.id.clone(),
                    kind: "note".into(),
                    body: format!("{n} body"),
                    author: ActorView {
                        name: "the user".into(),
                        r#ref: "user".into(),
                    },
                    created_at: "2026-11-13T09:00:00Z".into(),
                    ..SeedNote::default()
                })
                .collect(),
            notes_total: notes.len() as i64,
            seed,
            children,
            ..SeedDocument::default()
        }),
        ..SeedDocumentGetResultMessage::new()
    })
}

/// What the tile shows, as assistive tech reads it, in reading order.
fn shown(world: &World) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key("tile.t"))
        .into_iter()
        .filter_map(|n| n.name.or(n.value))
        .collect()
}

fn titled(world: &World, key: &str) -> Option<String> {
    world.screen().get(Target::key(key)).name
}

pub(super) fn cache() -> Seed {
    with(seed("s-cache1", "Cache store migration"), |s| {
        s.body = "Move the cache to Postgres.".into()
    })
}

#[test]
fn a_seed_tile_shows_the_seed_its_plan_and_log_and_names_itself_after_the_seed() {
    let world = start("s-cache1", vec![cache()], |r| {
        document(r, cache(), Vec::new(), &["picked up"])
    });

    let seen = shown(&world);
    assert!(
        seen.contains(&"Cache store migration".to_owned()),
        "{seen:?}"
    );
    assert!(
        seen.contains(&"Move the cache to Postgres.".to_owned()),
        "{seen:?}"
    );
    assert!(seen.contains(&"picked up body".to_owned()), "{seen:?}");
    assert_eq!(
        titled(&world, "tile.t").as_deref(),
        Some("Cache store migration")
    );
    assert_eq!(
        titled(&world, &format!("sidebar.tile.{DESKTOP}.t")).as_deref(),
        Some("Open Cache store migration")
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_seed_the_garden_does_not_hold_shows_once_the_daemon_reads_it_and_a_failed_read_says_why() {
    let world = start("s-old111", Vec::new(), |r| {
        document(r, seed("s-old111", "An older seed"), Vec::new(), &[])
    });
    let seen = shown(&world);
    assert!(seen.contains(&"An older seed".to_owned()), "{seen:?}");
    assert!(
        world
            .screen()
            .find(Target::key("seed-tile.t.open-garden"))
            .is_none()
    );

    let world = start("s-gone11", Vec::new(), |r| document_reply(r, None));
    let seen = shown(&world);
    assert!(
        seen.contains(&"seed \"s-gone11\" not found".to_owned()),
        "{seen:?}"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_garden_push_reads_the_tiles_seed_again() {
    let notes = std::sync::Arc::new(std::sync::Mutex::new(vec!["first"]));
    let said = notes.clone();
    let mut world = start("s-cache1", vec![cache()], move |r| {
        document(r, cache(), Vec::new(), &said.lock().unwrap())
    });
    assert!(shown(&world).contains(&"first body".to_owned()));

    notes.lock().unwrap().push("second");
    push(&mut world, vec![with(cache(), |s| s.rev = 2)]);
    let seen = shown(&world);
    assert!(seen.contains(&"second body".to_owned()), "{seen:?}");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_plot_tile_walks_to_a_child_in_place_and_opens_in_the_garden() {
    let crown = plot("s-plot11", "Native app", "");
    let child = part_of("s-plot11", "s-kid111", "Garden unit");
    let (c, k) = (crown.clone(), child.clone());
    let mut world = start("s-plot11", vec![crown.clone(), child.clone()], move |r| {
        if r["seed_id"] == "s-plot11" {
            document(r, c.clone(), vec![k.clone()], &[])
        } else {
            document(r, k.clone(), Vec::new(), &["unit note"])
        }
    });

    world.gesture(|app| app.click(Target::key("seed-tile.t.plot.s-kid111")));
    let moves = world.daemon.sent_of("desktop_update_tile");
    assert_eq!(moves.len(), 1);
    assert_eq!(
        (
            &moves[0]["desktop_id"],
            &moves[0]["tile_id"],
            &moves[0]["tile_params"]
        ),
        (&json!(DESKTOP), &json!("t"), &json!("s-kid111"))
    );

    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile(),
        desktops: vec![desktop(row(vec![pane("a"), seed_tile("s-kid111")]), 2)],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
    let seen = shown(&world);
    assert!(seen.contains(&"unit note body".to_owned()), "{seen:?}");
    assert_eq!(titled(&world, "tile.t").as_deref(), Some("Garden unit"));

    world.gesture(|app| app.click(Target::key("seed-tile.t.back")));
    assert_eq!(
        titled(&world, "seed-tile.t.back").as_deref(),
        Some("Back to Native app")
    );
    let moves = world.daemon.sent_of("desktop_update_tile");
    assert_eq!(moves.last().unwrap()["tile_params"], "s-plot11");

    world.gesture(|app| app.click(Target::key("seed-tile.t.open-garden")));
    assert_eq!(
        titled(&world, "garden.head.title").as_deref(),
        Some("Garden unit")
    );
    world.advance(std::time::Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn escape_climbs_a_seed_tiles_plots_only_while_the_tile_holds_the_keyboard() {
    let crown = plot("s-plot11", "Native app", "");
    let child = part_of("s-plot11", "s-kid111", "Garden unit");
    let seeds = vec![crown, child];
    let known = seeds.clone();
    let mut world = start("s-kid111", seeds, move |r| {
        document_reply(r, known.iter().find(|s| s.id == r["seed_id"]).cloned())
    });

    key(&mut world, Key::Escape);
    assert!(world.daemon.sent_of("desktop_update_tile").is_empty());

    world.gesture(|app| app.click(Target::key("seed-tile.t.head.title")));
    key(&mut world, Key::Escape);
    let moves = world.daemon.sent_of("desktop_update_tile");
    assert_eq!(moves.len(), 1);
    assert_eq!(moves[0]["tile_params"], "s-plot11");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_seed_tile_judges_its_checks_and_shows_the_reason_as_it_is_typed() {
    let checked = with(cache(), |s| {
        s.checks = Some(vec![attn_protocol::SeedCheck {
            id: "C1".into(),
            text: "the cache moves".into(),
            state: "reported".into(),
            ..attn_protocol::SeedCheck::default()
        }])
    });
    let read = checked.clone();
    let mut world = start("s-cache1", vec![checked], move |r| {
        document(r, read.clone(), Vec::new(), &[])
    });

    world.gesture(|app| app.click(Target::role("button").name("Reject C1")));
    world.gesture(|app| app.type_text("a run on Linux"));
    assert_eq!(
        world
            .screen()
            .get(Target::key("seed-tile.t.check.C1.field"))
            .value
            .as_deref(),
        Some("a run on Linux")
    );
    key(&mut world, Key::Enter);
    let sent = world.daemon.sent_of("seed_check");
    assert_eq!(
        (&sent[0]["seed_id"], &sent[0]["verb"], &sent[0]["summary"]),
        (
            &json!("s-cache1"),
            &json!("reject"),
            &json!("a run on Linux")
        )
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_seed_tile_offers_send_to_chief_once_a_chief_starts() {
    let mut world = start("s-cache1", vec![cache()], |r| {
        document(r, cache(), Vec::new(), &[])
    });
    assert!(
        world
            .screen()
            .find(Target::key("seed-tile.t.chief.open"))
            .is_none()
    );

    world.daemon.emit(attn_protocol::SessionRegisteredMessage {
        session: Session {
            chief_of_staff: Some(true),
            ..session("chief1", "Chief")
        },
        ..attn_protocol::SessionRegisteredMessage::new()
    });
    world.settle();

    assert_eq!(
        titled(&world, "seed-tile.t.chief.open").as_deref(),
        Some("Send to Chief")
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_seed_tile_hands_its_seed_over_and_leaves_the_keyboard_with_the_agent() {
    let held = with(cache(), |s| {
        s.continuation = Some(attn_protocol::SeedContinuation {
            agent: "claude".into(),
            cwd: "/tmp/placed".into(),
            handover_placement: "reuse_cwd".into(),
            directory_state: "present".into(),
            ..attn_protocol::SeedContinuation::default()
        })
    });
    let read = held.clone();
    let mut world = start("s-cache1", vec![held], move |r| {
        document(r, read.clone(), Vec::new(), &[])
    });
    world.daemon.on("delegate", |r| {
        message(attn_protocol::DelegateResultMessage {
            request_id: r["request_id"].as_str().map(str::to_owned),
            success: true,
            result: Some(attn_protocol::DelegateResult {
                session_id: "agent".into(),
                ..attn_protocol::DelegateResult::default()
            }),
            ..attn_protocol::DelegateResultMessage::new()
        })
    });

    world.gesture(|app| app.click(Target::key("seed-tile.t.handover.open")));
    world.gesture(|app| app.type_text("Pick up the cache work."));
    key_with(
        &mut world,
        Key::Enter,
        Mods {
            cmd: true,
            ..Mods::default()
        },
    );

    assert_eq!(world.daemon.sent_of("delegate").len(), 1);
    assert!(
        world
            .screen()
            .find(Target::key("seed-tile.t.handover.form"))
            .is_none()
    );
    assert_ne!(
        focused(&world).and_then(|n| n.key).as_deref(),
        Some("seed-tile.t.handover.open")
    );
    assert!(rests(&mut world));
}

#[test]
fn a_press_on_a_plot_row_in_an_inactive_seed_tile_takes_the_tile_so_escape_climbs_back() {
    let crown = plot("s-plot11", "Native app", "");
    let child = part_of("s-plot11", "s-kid111", "Garden unit");
    let (c, k) = (crown.clone(), child.clone());
    let mut world = start("s-plot11", vec![crown, child], move |r| {
        if r["seed_id"] == "s-plot11" {
            document(r, c.clone(), vec![k.clone()], &[])
        } else {
            document(r, k.clone(), Vec::new(), &[])
        }
    });

    world.gesture(|app| app.click(Target::key("seed-tile.t.plot.s-kid111")));
    let shown = world.daemon.sent_of("desktop_show_leaf");
    assert_eq!(shown.last().map(|s| s["leaf_id"].clone()), Some(json!("t")));
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile(),
        desktops: vec![desktop(row(vec![pane("a"), seed_tile("s-kid111")]), 2)],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();

    key(&mut world, Key::Escape);
    let moves = world.daemon.sent_of("desktop_update_tile");
    assert_eq!(
        moves
            .iter()
            .map(|m| m["tile_params"].clone())
            .collect::<Vec<_>>(),
        [json!("s-kid111"), json!("s-plot11")]
    );
}

#[test]
fn reads_a_seed_tile_from_its_status_through_its_plot_to_its_plan_and_a_log_of_what_is_withheld() {
    let crown = with(plot("s-plan11", "Plan the reader", ""), |s| {
        s.body = "# Rendered plan\n\nThe steps.".into();
        s.plot_progress = Some(SeedPlotProgress {
            total: 1,
            done: 1,
            ..SeedPlotProgress::default()
        });
    });
    let child = closed(
        part_of("s-plan11", "s-step11", "Build the reader"),
        "harvested",
    );
    let read = crown.clone();
    let world = start("s-plan11", vec![crown, child.clone()], move |r| {
        let mut answer = document(r, read.clone(), vec![child.clone()], &[]);
        answer["document"]["notes"] = json!([{
            "id": "n-one111", "seed_id": "s-plan11", "kind": "note",
            "body": "Verified the **reader**.",
            "author": {"name": "the user", "ref": "user"},
            "created_at": "2026-11-13T09:00:00Z",
        }]);
        answer["document"]["notes_total"] = json!(2);
        answer
    });

    let seen = shown(&world);
    let at = |words: &str| {
        seen.iter()
            .position(|w| w == words)
            .unwrap_or_else(|| panic!("no {words:?} in {seen:?}"))
    };
    assert!(at("Plan the reader") < at("Plot"), "{seen:?}");
    assert!(at("Plot") < at("Rendered plan"), "{seen:?}");
    assert_eq!(seen[at("Build the reader") + 1], "done", "{seen:?}");
    assert!(
        seen.iter().any(|w| w.contains("Verified the reader")),
        "{seen:?}"
    );
    assert!(!seen.iter().any(|w| w.contains("**")), "{seen:?}");
    assert!(
        seen.contains(&"1 more entry on the log.".to_owned()),
        "{seen:?}"
    );
    assert!(world.runtime_is_at_rest());
}

fn plan() -> Seed {
    with(seed("s-plan11", "Plan the reader"), |s| {
        s.body = "# Rendered plan\n\nThe steps.".into()
    })
}

#[test]
fn lists_a_seed_tiles_checks_under_its_plan_with_the_header_tally_and_moves_one_from_the_tile() {
    let checked = with(plan(), |s| {
        s.checks = Some(vec![
            attn_protocol::SeedCheck {
                report: Some(attn_protocol::SeedCheckReport {
                    summary: "Reader wire tests pass".into(),
                    by: ActorView {
                        name: "Trellis".into(),
                        r#ref: "member:trellis".into(),
                    },
                    at: "2026-08-15T09:00:00Z".into(),
                    ..attn_protocol::SeedCheckReport::default()
                }),
                id: "C1".into(),
                text: "Wire tests cover the reader.".into(),
                state: "reported".into(),
                ..attn_protocol::SeedCheck::default()
            },
            attn_protocol::SeedCheck {
                id: "C2".into(),
                text: "The reader is recorded.".into(),
                state: "open".into(),
                ..attn_protocol::SeedCheck::default()
            },
        ])
    });
    let read = checked.clone();
    let mut world = start("s-plan11", vec![checked], move |r| {
        document(r, read.clone(), Vec::new(), &[])
    });
    world.daemon.on("seed_check", |_: &Request| ());

    let tally = world
        .screen()
        .descendants(Target::key("tile.t"))
        .into_iter()
        .find(|n| n.name.as_deref() == Some("Checks: 0 of 2 confirmed · 1 to judge"));
    assert!(tally.is_some(), "{:?}", shown(&world));
    let seen = shown(&world);
    let at = |words: &str| {
        seen.iter()
            .position(|w| w.contains(words))
            .unwrap_or_else(|| panic!("no {words:?} in {seen:?}"))
    };
    assert!(
        at("Rendered plan") < at("Wire tests cover the reader."),
        "{seen:?}"
    );
    at("Reader wire tests pass");

    world.gesture(|app| app.click(Target::role("button").name("Confirm C1")));
    let sent = world.daemon.sent_of("seed_check");
    assert_eq!(sent.len(), 1);
    assert_eq!(
        (&sent[0]["seed_id"], &sent[0]["check_id"], &sent[0]["verb"]),
        (&json!("s-plan11"), &json!("C1"), &json!("confirm"))
    );
    assert!(rests(&mut world));
}

fn press_a_shared_checks_seed(update_tile: impl Fn(&Request) -> Value + Send + 'static) -> World {
    let leaf = with(part_of("s-plot11", "s-leaf11", "The view"), |s| {
        s.checks = Some(vec![attn_protocol::SeedCheck {
            id: "C4".into(),
            text: "CI is green on the head.".into(),
            state: "open".into(),
            from: Some("s-plot11".into()),
            ..attn_protocol::SeedCheck::default()
        }])
    });
    let crown = with(plot("s-plot11", "The monitor plot", ""), |s| {
        s.body = "# A long plan".into();
        s.shared_checks = Some(vec![attn_protocol::SeedSharedCheck {
            id: "C4".into(),
            text: "CI is green on the head.".into(),
            seeds: vec![attn_protocol::SeedCheckRollupEntry {
                seed_id: "s-leaf11".into(),
                state: "open".into(),
                has_report: false,
            }],
            ..attn_protocol::SeedSharedCheck::default()
        }]);
    });
    let (read_crown, read_leaf) = (crown.clone(), leaf.clone());
    let mut world = start("s-plot11", vec![crown.clone()], move |r| {
        if r["seed_id"] == "s-leaf11" {
            document(r, read_leaf.clone(), Vec::new(), &[])
        } else {
            document(r, read_crown.clone(), vec![read_leaf.clone()], &[])
        }
    });
    world.daemon.emit(attn_protocol::GardenSeedsChangedMessage {
        profile_id: PROFILE.into(),
        order: vec!["s-plot11".into(), "s-leaf11".into()],
        seeds: vec![leaf],
        total: 2,
        ..attn_protocol::GardenSeedsChangedMessage::new()
    });
    world.settle();
    world.daemon.on("desktop_update_tile", update_tile);

    world.gesture(|app| {
        app.click(Target::role("button").name("C4 CI is green on the head. · 0 of 1 confirmed"))
    });
    world.gesture(|app| app.click(Target::role("button").name("The view · planted · open")));
    let updated = world.daemon.sent_of("desktop_update_tile");
    assert_eq!(updated.len(), 1);
    assert_eq!(updated[0]["tile_params"], "s-leaf11");
    world
}

fn walk_the_tile_to_the_leaf(world: &mut World) {
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile(),
        desktops: vec![desktop(row(vec![pane("a"), seed_tile("s-leaf11")]), 2)],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
}

fn focused_name(world: &World) -> Option<String> {
    world.screen().focused().and_then(|n| n.name)
}

#[test]
fn opens_an_inheriting_seeds_check_from_a_plot_tiles_shared_check_row() {
    let mut world = press_a_shared_checks_seed(|_| Value::Null);

    walk_the_tile_to_the_leaf(&mut world);

    assert_eq!(focused_name(&world).as_deref(), Some("Confirm C4"));
    assert!(rests(&mut world));
}

#[test]
fn leaves_the_keyboard_alone_on_a_later_visit_when_the_tile_refused_to_go_to_the_check() {
    let mut world = press_a_shared_checks_seed(
        |r| json!({"event": "desktop_update_tile_result", "request_id": r["request_id"], "success": false, "error": "no"}),
    );
    assert!(shows(&world, "Could not open the seed: no"));

    walk_the_tile_to_the_leaf(&mut world);

    assert!(
        world
            .screen()
            .find(Target::role("button").name("Confirm C4"))
            .is_some()
    );
    assert_ne!(focused_name(&world).as_deref(), Some("Confirm C4"));
    world.advance(std::time::Duration::from_secs(6));
    assert!(rests(&mut world));
}

#[test]
fn refreshes_a_seed_tile_on_garden_changes_that_carry_no_seed_on_reconnect_and_ignores_obsolete_responses()
 {
    let notes = std::sync::Arc::new(std::sync::Mutex::new(vec!["before the keep pass"]));
    let said = notes.clone();
    let mut world = start("s-plan11", vec![plan()], move |r| {
        document(r, plan(), Vec::new(), &said.lock().unwrap())
    });
    let changed = |world: &mut World| {
        world.daemon.emit(attn_protocol::GardenSeedsChangedMessage {
            profile_id: PROFILE.into(),
            order: vec!["s-plan11".into()],
            seeds: Vec::new(),
            total: 1,
            ..attn_protocol::GardenSeedsChangedMessage::new()
        });
        world.settle();
    };
    let kept_changed = |world: &mut World| {
        world
            .daemon
            .emit(json!({"event": "kept_conversations_changed"}));
        world.settle();
    };
    let reads = |world: &World| world.daemon.sent_of("seed_document_get");

    world.daemon.on("seed_document_get", |_: &Request| ());
    kept_changed(&mut world);
    assert_eq!(reads(&world).len(), 1);
    changed(&mut world);
    let older = reads(&world)[1].clone();

    *notes.lock().unwrap() = vec!["after the keep pass"];
    let said = notes.clone();
    world.daemon.on("seed_document_get", move |r: &Request| {
        document(r, plan(), Vec::new(), &said.lock().unwrap())
    });
    kept_changed(&mut world);
    assert_eq!(reads(&world).len(), 2);
    changed(&mut world);
    world.daemon.reply_to(
        &older,
        document(&older, plan(), Vec::new(), &["superseded detail"]),
    );
    world.settle();
    let seen = shown(&world);
    assert!(
        seen.contains(&"after the keep pass body".to_owned()),
        "{seen:?}"
    );
    assert!(
        !seen.contains(&"superseded detail body".to_owned()),
        "{seen:?}"
    );
    assert_eq!(reads(&world).len(), 3);
    assert!(reads(&world).iter().all(|r| r["seed_id"] == "s-plan11"));

    *notes.lock().unwrap() = vec!["after reconnect"];
    world.reconnect();
    let seen = shown(&world);
    assert!(
        seen.contains(&"after reconnect body".to_owned()),
        "{seen:?}"
    );
    assert_eq!(reads(&world).len(), 4);
    assert!(rests(&mut world));
}

#[test]
fn shows_no_artifacts_and_no_harvest_condition_for_a_seed_that_has_neither() {
    let world = start("s-plan11", vec![plan()], |r| {
        document(r, plan(), Vec::new(), &["no attachment here"])
    });
    let seen = shown(&world);
    assert!(
        seen.contains(&"no attachment here body".to_owned()),
        "{seen:?}"
    );
    assert!(!seen.contains(&"Artifacts".to_owned()), "{seen:?}");
    assert!(
        !seen.iter().any(|w| w.contains("harvests when")),
        "{seen:?}"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn says_the_harvest_condition_beside_the_state_and_opens_the_pull_request() {
    let url = "https://github.com/victorarias/attn/pull/42";
    let armed = with(plan(), |s| {
        s.status = "parked".into();
        s.harvest_when = Some(SeedHarvestCondition {
            pull_request: "github.com:victorarias/attn#42".into(),
            url: url.into(),
            ..SeedHarvestCondition::default()
        });
    });
    let read = armed.clone();
    let mut world = start("s-plan11", vec![armed], move |r| {
        document(r, read.clone(), Vec::new(), &[])
    });

    let link = Target::role("link").name("harvests when victorarias/attn#42 merges");
    let meta: Vec<_> = world
        .screen()
        .descendants(Target::key("seed-tile.t.head.meta"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert_eq!(meta.first().map(String::as_str), Some("parked"), "{meta:?}");
    assert!(
        meta.contains(&"harvests when victorarias/attn#42 merges".to_owned()),
        "{meta:?}"
    );
    world.gesture(|app| app.click(link));
    assert_eq!(world.opened_links(), [url]);
}

#[test]
fn keeps_the_lines_a_log_entry_was_written_in_and_leaves_its_code_alone() {
    let world = start("s-plan11", vec![plan()], |r| {
        let mut answer = document(r, plan(), Vec::new(), &[]);
        answer["document"]["notes"] = json!([{
            "id": "n-lines", "seed_id": "s-plan11", "kind": "note",
            "body": "Checked A\nChecked B\n\n```\nx\ny\n```",
            "author": {"name": "the user", "ref": "user"},
            "created_at": "2026-11-13T09:00:00Z",
        }]);
        answer["document"]["notes_total"] = json!(1);
        answer
    });
    let read: Vec<_> = world
        .screen()
        .descendants(Target::key("seed-tile.t.log.n-lines"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(
        read.contains(&"Checked A\nChecked B".to_owned()),
        "{read:?}"
    );
    assert!(read.contains(&"x\ny".to_owned()), "{read:?}");
    assert!(world.runtime_is_at_rest());
}
