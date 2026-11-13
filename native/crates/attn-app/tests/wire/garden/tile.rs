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
fn start(
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

fn document(request: &Request, seed: Seed, children: Vec<Seed>, notes: &[&str]) -> Value {
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

fn cache() -> Seed {
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
