use std::collections::BTreeMap;

use attn_app::Action;
use attn_protocol::{
    ActorView, GardenSeedsChangedMessage, GardenSeedsUpdatedMessage, InitialStateMessage,
    PartyView, Profile, Seed, SeedDocument, SeedDocumentGetResultMessage, SeedEdge,
    SeedHarvestCondition, SeedNote, SeedPlotProgress, Session, SessionStateChangedMessage,
};
use attn_testkit::{Daemon, Node, Request, Target, World, message};
use rune::{Key, KeyEvent, Mods};
use serde_json::Value;

mod board;
mod checks;
mod tile;

const PROFILE: &str = "work";

pub(super) fn seed(id: &str, title: &str) -> Seed {
    Seed {
        profile_id: PROFILE.into(),
        id: id.into(),
        title: title.into(),
        status: "planted".into(),
        planter: ActorView {
            name: "the user".into(),
            r#ref: "user".into(),
        },
        created_at: "2026-11-13T09:00:00Z".into(),
        updated_at: "2026-11-13T09:00:00Z".into(),
        rev: 1,
        ..Seed::default()
    }
}

fn with(mut seed: Seed, f: impl FnOnce(&mut Seed)) -> Seed {
    f(&mut seed);
    seed
}

fn edge(kind: &str, to: &str) -> SeedEdge {
    SeedEdge {
        kind: kind.into(),
        to: to.into(),
    }
}

fn part_of(parent: &str, id: &str, title: &str) -> Seed {
    with(seed(id, title), |s| s.edges.push(edge("part-of", parent)))
}

fn plot(id: &str, title: &str, parent: &str) -> Seed {
    let crown = if parent.is_empty() {
        seed(id, title)
    } else {
        part_of(parent, id, title)
    };
    with(crown, |s| {
        s.plot_progress = Some(SeedPlotProgress {
            total: 1,
            ready: 1,
            ..SeedPlotProgress::default()
        })
    })
}

fn closed(mut seed: Seed, status: &str) -> Seed {
    seed.status = status.into();
    seed
}

fn session(id: &str, label: &str) -> Session {
    Session {
        id: id.into(),
        label: label.into(),
        agent: "claude".into(),
        state: "idle".into(),
        profile_id: PROFILE.into(),
        ..Session::default()
    }
}

fn initial(seeds: Vec<Seed>, sessions: Vec<Session>) -> InitialStateMessage {
    InitialStateMessage {
        selected_profile_id: Some(PROFILE.into()),
        profiles: Some(vec![Profile {
            id: PROFILE.into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        settings: Some(BTreeMap::new()),
        sessions: Some(sessions),
        seeds_total: Some(seeds.len() as i64),
        seeds: Some(seeds),
        ..InitialStateMessage::new()
    }
}

/// A daemon that answers every seed read with the seed as the garden holds it and no log.
fn daemon(seeds: Vec<Seed>, sessions: Vec<Session>) -> Daemon {
    let known = seeds.clone();
    let daemon = Daemon::scripted().state(initial(seeds, sessions));
    daemon.on("seed_document_get", move |r: &Request| {
        document_reply(r, known.iter().find(|s| s.id == r["seed_id"]).cloned())
    });
    daemon
}

fn document_reply(request: &Request, seed: Option<Seed>) -> Value {
    message(SeedDocumentGetResultMessage {
        request_id: request["request_id"].as_str().unwrap().into(),
        success: seed.is_some(),
        error: seed
            .is_none()
            .then(|| format!("seed {} not found", request["seed_id"])),
        document: seed.map(|seed| SeedDocument {
            seed,
            ..SeedDocument::default()
        }),
        ..SeedDocumentGetResultMessage::new()
    })
}

/// A read's answer: `seed` with one log entry, `note`.
fn logged(request: &Request, seed: Seed, note: &str) -> Value {
    message(SeedDocumentGetResultMessage {
        request_id: request["request_id"].as_str().unwrap().into(),
        success: true,
        document: Some(SeedDocument {
            notes: vec![SeedNote {
                id: note.into(),
                seed_id: seed.id.clone(),
                kind: "note".into(),
                body: format!("{note} body"),
                author: ActorView {
                    name: "the user".into(),
                    r#ref: "user".into(),
                },
                created_at: "2026-11-13T09:00:00Z".into(),
                ..SeedNote::default()
            }],
            notes_total: 1,
            seed,
            ..SeedDocument::default()
        }),
        ..SeedDocumentGetResultMessage::new()
    })
}

fn open_garden(seeds: Vec<Seed>) -> World {
    open_garden_with(daemon(seeds, Vec::new()))
}

fn open_garden_with(daemon: Daemon) -> World {
    open_garden_sized(daemon, (960.0, 640.0))
}

/// The garden in a window wide enough to lay the walk out in two columns once expanded.
const TWO_COLUMNS: (f32, f32) = (1248.0, 800.0);
/// Wide enough for three.
const THREE_COLUMNS: (f32, f32) = (1520.0, 900.0);

fn open_garden_sized(daemon: Daemon, size: (f32, f32)) -> World {
    let mut world = World::start_sized(daemon, size);
    world.gesture(|app| app.key(board()));
    world
}

fn push(world: &mut World, seeds: Vec<Seed>) {
    world.daemon.emit(GardenSeedsUpdatedMessage {
        profile_id: PROFILE.into(),
        total: seeds.len() as i64,
        seeds,
        ..GardenSeedsUpdatedMessage::new()
    });
    world.settle();
}

/// The garden's rows on screen, by title, in order.
fn rows(world: &World) -> Vec<String> {
    world
        .screen()
        .all(Target::role("button"))
        .into_iter()
        .chain(world.screen().all(Target::role("list-box-option")))
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with("garden.row."))
        })
        .filter_map(|n| n.name)
        .collect()
}

/// The words a row shows, beside its title.
fn row_words(world: &World, id: &str) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key(format!("garden.row.{id}")))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn row_says(world: &World, id: &str, words: &str) -> bool {
    row_words(world, id).iter().any(|w| w.contains(words))
}

fn has_row(world: &World, title: &str) -> bool {
    rows(world).iter().any(|r| r == title)
}

fn open_row(world: &mut World, title: &str) {
    let screen = world.screen();
    let key = screen
        .all(Target::role("button").name(title))
        .into_iter()
        .chain(screen.all(Target::role("list-box-option").name(title)))
        .find_map(|n| n.key.filter(|k| k.starts_with("garden.row.")))
        .unwrap_or_else(|| panic!("no garden row {title}:\n{}", world.screen()));
    world.gesture(|app| app.click(Target::key(key)));
}

fn click(world: &mut World, name: &str) {
    world.gesture(|app| app.click(Target::role("button").name(name)));
}

fn heading(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("garden.head.title"))
        .and_then(|n| n.name)
}

fn trail(world: &World) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key("garden.trail"))
        .into_iter()
        .filter(|n| n.role == "button")
        .filter_map(|n| n.name)
        .map(|n| {
            if n.starts_with("Show ") && n.ends_with(" more steps") {
                "…".to_owned()
            } else {
                n
            }
        })
        .collect()
}

fn shows(world: &World, words: &str) -> bool {
    world
        .screen()
        .all(Target::role("note"))
        .iter()
        .chain(world.screen().all(Target::role("status")).iter())
        .any(|n| n.name.as_deref().is_some_and(|name| name.contains(words)))
}

fn type_query(world: &mut World, query: &str) {
    world.gesture(|app| {
        app.click(Target::key("garden.search.field"));
        app.type_text(query);
    });
}

fn key(world: &mut World, key: Key) {
    key_with(world, key, Mods::default());
}

fn key_with(world: &mut World, key: Key, mods: Mods) {
    world.gesture(|app| app.key(KeyEvent { key, mods }));
}

fn focused(world: &World) -> Option<Node> {
    world.screen().focused()
}

fn active_answer(world: &World) -> Option<String> {
    world
        .screen()
        .all(Target::role("list-box-option"))
        .into_iter()
        .find(|n| n.is("selected"))
        .and_then(|n| n.name)
}

/// Lets the last change's motion play out on the fake clock, then asks whether the window is
/// idle. Entering a page plays for 0.75 s at most (45 frames at 60 Hz, measured with
/// `World::record`), past what landing motion in zero time finishes.
fn rests(world: &mut World) -> bool {
    world.advance(std::time::Duration::from_secs(1));
    world.runtime_is_at_rest()
}

fn board() -> KeyEvent {
    super::sidebar::chord(Action::BoardOpen)
}

fn focused_row(world: &World) -> Option<String> {
    focused(world)
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with("garden.row."))
        })
        .and_then(|n| n.name)
}

fn shown(world: &World) -> &'static str {
    let screen = world.screen();
    if screen
        .find(Target::role("dialog").name("The garden"))
        .is_some()
    {
        "full"
    } else if screen
        .find(Target::role("region").name("The garden"))
        .is_some()
    {
        "dock"
    } else {
        "closed"
    }
}

fn deep() -> Vec<Seed> {
    vec![
        plot("s-crown1", "the migration", ""),
        plot("s-mid111", "one panel", "s-crown1"),
        plot("s-inn111", "its header", "s-mid111"),
        plot("s-fou111", "the overflow menu", "s-inn111"),
        plot("s-fif111", "the fold threshold", "s-fou111"),
        part_of("s-fif111", "s-leaf11", "the actual work"),
        closed(
            part_of("s-crown1", "s-done11", "already shipped"),
            "harvested",
        ),
        seed("s-alone1", "unrelated work"),
    ]
}

#[test]
fn seeds_the_garden_from_initial_state_and_replaces_it_on_every_push() {
    let mut world = open_garden(vec![seed("s-aaa111", "already planted")]);
    assert_eq!(rows(&world), ["already planted"]);

    push(
        &mut world,
        vec![
            seed("s-bbb222", "just planted"),
            seed("s-aaa111", "already planted"),
        ],
    );

    assert_eq!(rows(&world), ["just planted", "already planted"]);
    assert!(rests(&mut world));
}

#[test]
fn replaces_the_garden_on_a_profile_change_and_ignores_late_pushes_from_the_old_profile() {
    let mut world = open_garden(vec![seed("s-alpha1", "Default work")]);
    let side = |seeds: Vec<Seed>| InitialStateMessage {
        selected_profile_id: Some("side".into()),
        profiles: Some(vec![Profile {
            id: "side".into(),
            name: "Side".into(),
            ..Profile::default()
        }]),
        seeds_total: Some(seeds.len() as i64),
        seeds: Some(seeds),
        ..initial(Vec::new(), Vec::new())
    };
    let beta = with(seed("s-beta11", "Side work"), |s| {
        s.profile_id = "side".into()
    });
    world.daemon.answer_hello_with(side(vec![beta.clone()]));

    world.reconnect();
    assert_eq!(rows(&world), ["Side work"]);

    world.daemon.emit(GardenSeedsUpdatedMessage {
        profile_id: PROFILE.into(),
        seeds: vec![seed("s-alpha1", "Default work")],
        total: 1,
        ..GardenSeedsUpdatedMessage::new()
    });
    world.daemon.emit(GardenSeedsChangedMessage {
        profile_id: PROFILE.into(),
        order: vec!["s-alpha1".into()],
        seeds: vec![seed("s-alpha1", "Default work")],
        total: 1,
        ..GardenSeedsChangedMessage::new()
    });
    world.settle();
    assert_eq!(rows(&world), ["Side work"]);

    world.daemon.emit(GardenSeedsUpdatedMessage {
        profile_id: "side".into(),
        seeds: Vec::new(),
        total: 0,
        ..GardenSeedsUpdatedMessage::new()
    });
    world.settle();
    assert!(rows(&world).is_empty());
    assert!(rests(&mut world));
}

#[test]
fn applies_a_change_onto_the_garden_it_holds_in_the_order_the_daemon_sends() {
    let older = seed("s-aaa111", "older seed");
    let newer = seed("s-bbb222", "newer seed");
    let mut world = open_garden(vec![newer.clone(), older.clone()]);

    world.daemon.emit(GardenSeedsChangedMessage {
        profile_id: PROFILE.into(),
        order: vec!["s-ccc333".into(), newer.id.clone(), older.id.clone()],
        seeds: vec![
            seed("s-ccc333", "just planted"),
            with(newer, |s| s.title = "newer seed, renamed".into()),
        ],
        total: 3,
        ..GardenSeedsChangedMessage::new()
    });
    world.settle();
    assert_eq!(
        rows(&world),
        ["just planted", "newer seed, renamed", "older seed"]
    );

    world.daemon.emit(GardenSeedsChangedMessage {
        profile_id: PROFILE.into(),
        order: vec!["s-ccc333".into(), older.id.clone()],
        seeds: vec![],
        total: 2,
        ..GardenSeedsChangedMessage::new()
    });
    world.settle();
    assert_eq!(rows(&world), ["just planted", "older seed"]);
    assert!(rests(&mut world));
}

#[test]
fn one_changed_seed_rebuilds_only_its_row() {
    let seeds: Vec<Seed> = (0..20)
        .map(|i| seed(&format!("s-row{i:03}"), &format!("seed {i}")))
        .collect();
    let mut world = open_garden(seeds.clone());
    let before = attn_app::garden::rows_built();

    world.daemon.emit(GardenSeedsChangedMessage {
        profile_id: PROFILE.into(),
        order: seeds.iter().map(|s| s.id.clone()).collect(),
        seeds: vec![with(seeds[3].clone(), |s| s.status = "growing".into())],
        total: 20,
        ..GardenSeedsChangedMessage::new()
    });
    world.settle();

    assert_eq!(attn_app::garden::rows_built() - before, 1);
    assert_eq!(
        world
            .screen()
            .get(Target::key("garden.row.s-row003"))
            .value
            .as_deref(),
        Some("growing")
    );
    assert!(rests(&mut world));
}

#[test]
fn reads_a_garden_less_daemon_as_an_empty_garden() {
    let mut world = open_garden(vec![seed("s-aaa111", "already planted")]);
    world
        .daemon
        .answer_hello_with(initial(Vec::new(), Vec::new()));

    world.reconnect();

    assert!(rows(&world).is_empty());
    assert!(shows(&world, "The garden is empty"));
    assert!(rests(&mut world));
}

#[test]
fn carries_how_many_seeds_the_garden_holds_not_just_the_ones_it_sent() {
    let mut world = open_garden(Vec::new());

    world.daemon.emit(GardenSeedsUpdatedMessage {
        profile_id: PROFILE.into(),
        seeds: vec![seed("s-bbb222", "the newest one")],
        total: 1421,
        ..GardenSeedsUpdatedMessage::new()
    });
    world.settle();
    assert!(shows(
        &world,
        "The garden holds 1421 seeds; this panel has the newest 1."
    ));

    push(&mut world, vec![seed("s-bbb222", "the newest one")]);
    assert!(!shows(&world, "The garden holds"));
    assert!(rests(&mut world));
}

#[test]
fn opens_on_crowns_and_loose_seeds_keeping_plot_children_inside_their_plot() {
    let mut seeds = deep();
    seeds.push(part_of("s-gone99", "s-orph11", "crown got capped away"));
    let mut world = open_garden(seeds);

    let shown = rows(&world);
    assert!(shown.contains(&"the migration".to_owned()));
    assert!(shown.contains(&"unrelated work".to_owned()));
    assert!(shown.contains(&"crown got capped away".to_owned()));
    assert!(!shown.contains(&"one panel".to_owned()));
    assert!(!shown.contains(&"already shipped".to_owned()));
    assert!(rests(&mut world));
}

#[test]
fn counts_a_plot_on_its_row_and_spells_it_out_on_its_page() {
    let progressing = with(plot("s-crown1", "ship the thing", ""), |s| {
        s.plot_progress = Some(SeedPlotProgress {
            total: 3,
            done: 1,
            growing: 1,
            ready: 1,
            ..SeedPlotProgress::default()
        })
    });
    let mut world = open_garden(vec![progressing, seed("s-alone1", "unrelated work")]);
    assert!(row_says(&world, "s-crown1", "1/3"));
    assert!(!row_says(&world, "s-alone1", "/"));
    assert!(!shows(&world, "1/3 done"));

    open_row(&mut world, "ship the thing");

    assert!(shows(&world, "1/3 done · 1 growing · 1 ready"));
    assert!(rests(&mut world));
}

#[test]
fn drills_into_a_plot_keeps_its_closed_seeds_behind_a_toggle_and_climbs_back_out() {
    let mut world = open_garden(deep());

    open_row(&mut world, "the migration");
    assert!(has_row(&world, "one panel"));
    assert!(!has_row(&world, "unrelated work"));
    assert!(!has_row(&world, "already shipped"));
    click(&mut world, "1 closed");
    assert!(has_row(&world, "already shipped"));

    click(&mut world, "Garden");

    assert!(has_row(&world, "unrelated work"));
    assert!(rests(&mut world));
}

#[test]
fn walks_a_plot_inside_a_plot_and_climbs_several_levels_at_once() {
    let mut world = open_garden(deep());

    open_row(&mut world, "the migration");
    open_row(&mut world, "one panel");
    assert_eq!(heading(&world).as_deref(), Some("one panel"));
    assert!(has_row(&world, "its header"));
    assert_eq!(trail(&world), ["Garden", "the migration"]);

    click(&mut world, "Garden");

    assert!(has_row(&world, "the migration"));
    assert_eq!(heading(&world), None);
    assert!(rests(&mut world));
}

#[test]
fn climbs_out_on_its_own_when_the_plot_it_is_inside_disappears() {
    let mut world = open_garden(deep());
    open_row(&mut world, "the migration");

    push(&mut world, vec![seed("s-alone1", "unrelated work")]);

    assert_eq!(heading(&world), None);
    assert!(has_row(&world, "unrelated work"));
    assert!(rests(&mut world));
}

#[test]
fn crosses_from_one_plot_into_a_related_one() {
    let linked = with(
        part_of("s-crown1", "s-child3", "holds the next plot up"),
        |s| s.edges.push(edge("blocks", "s-crown2")),
    );
    let mut world = open_garden(vec![
        plot("s-crown1", "the migration", ""),
        linked,
        plot("s-crown2", "the next plot", ""),
        seed("s-alone1", "unrelated work"),
    ]);

    open_row(&mut world, "the migration");
    open_row(&mut world, "holds the next plot up");
    click(&mut world, "the next plot");

    assert_eq!(heading(&world).as_deref(), Some("the next plot"));
    assert_eq!(
        trail(&world),
        ["Garden", "the migration", "holds the next plot up"]
    );
    assert!(rests(&mut world));
}

#[test]
fn names_the_way_in_when_the_garden_is_empty_and_the_way_into_an_empty_plot() {
    let mut world = open_garden(Vec::new());
    assert!(shows(
        &world,
        "The garden is empty. attn seed plant \"what this is\" puts something in it."
    ));

    let empty = with(plot("s-crown9", "nothing in it yet", ""), |s| {
        s.plot_progress = Some(SeedPlotProgress::default())
    });
    push(&mut world, vec![empty]);
    open_row(&mut world, "nothing in it yet");

    assert!(shows(
        &world,
        "attn seed plant \"what this is\" --part-of s-crown9"
    ));
    assert!(rests(&mut world));
}

#[test]
fn opens_a_seed_to_its_own_page_and_the_trail_is_the_way_back() {
    let body = with(seed("s-body11", "has a body"), |s| {
        s.body = "the plan itself".into()
    });
    let mut world = open_garden(vec![body, seed("s-alone1", "unrelated work")]);

    open_row(&mut world, "has a body");
    assert_eq!(heading(&world).as_deref(), Some("has a body"));
    assert!(
        world
            .screen()
            .descendants(Target::role("document").name("Plan"))
            .iter()
            .any(|n| n.name.as_deref() == Some("the plan itself"))
    );
    assert!(!has_row(&world, "unrelated work"));

    click(&mut world, "Garden");
    assert!(has_row(&world, "unrelated work"));
    assert!(rests(&mut world));
}

#[test]
fn opens_a_found_seed_under_the_whole_part_of_path_from_the_garden_root() {
    let mut world = open_garden(deep());
    type_query(&mut world, "actual work");

    open_row(&mut world, "the actual work");

    assert_eq!(heading(&world).as_deref(), Some("the actual work"));
    assert_eq!(
        trail(&world),
        ["Garden", "…", "the overflow menu", "the fold threshold"]
    );
    assert!(rests(&mut world));
}

#[test]
fn opens_a_found_seed_whose_parent_the_push_left_out_as_a_root_of_its_own() {
    let mut world = open_garden(vec![
        part_of("s-missed", "s-leaf11", "the actual work"),
        seed("s-alone1", "unrelated work"),
    ]);
    type_query(&mut world, "actual work");

    open_row(&mut world, "the actual work");

    assert_eq!(heading(&world).as_deref(), Some("the actual work"));
    assert_eq!(trail(&world), ["Garden"]);
    assert!(rests(&mut world));
}

#[test]
fn stacks_in_the_dock_and_lays_the_walk_out_in_columns_in_the_window() {
    let mut world = open_garden_sized(daemon(deep(), Vec::new()), TWO_COLUMNS);
    open_row(&mut world, "the migration");
    open_row(&mut world, "one panel");
    assert!(has_row(&world, "its header"));
    assert!(!has_row(&world, "one panel"));

    click(&mut world, "Expand the garden");
    assert_eq!(heading(&world).as_deref(), Some("one panel"));
    assert!(has_row(&world, "one panel"));
    assert!(has_row(&world, "its header"));

    click(&mut world, "Return the garden to the dock");
    assert_eq!(heading(&world).as_deref(), Some("one panel"));
    assert!(!has_row(&world, "one panel"));
    assert!(has_row(&world, "its header"));
    assert!(rests(&mut world));
}

#[test]
fn draws_the_trailing_levels_the_width_holds_and_names_only_the_ancestors_no_column_shows() {
    let mut world = open_garden_sized(daemon(deep(), Vec::new()), TWO_COLUMNS);
    click(&mut world, "Expand the garden");
    open_row(&mut world, "the migration");
    assert!(has_row(&world, "unrelated work"));
    assert!(has_row(&world, "one panel"));

    open_row(&mut world, "one panel");
    assert!(!has_row(&world, "unrelated work"));
    assert!(has_row(&world, "its header"));
    assert_eq!(trail(&world), ["Garden", "the migration"]);
    assert!(rests(&mut world));
}

#[test]
fn shows_a_third_level_once_there_is_room_for_it() {
    let mut world = open_garden_sized(daemon(deep(), Vec::new()), THREE_COLUMNS);
    click(&mut world, "Expand the garden");
    open_row(&mut world, "the migration");
    open_row(&mut world, "one panel");
    assert!(has_row(&world, "unrelated work"));
    assert_eq!(trail(&world), ["Garden"]);

    open_row(&mut world, "its header");
    assert!(!has_row(&world, "unrelated work"));
    assert_eq!(trail(&world), ["Garden", "the migration"]);
    assert!(rests(&mut world));
}

#[test]
fn folds_the_trail_once_it_outruns_three_steps_and_opens_it_again() {
    let mut world = open_garden_sized(daemon(deep(), Vec::new()), TWO_COLUMNS);
    click(&mut world, "Expand the garden");
    for title in [
        "the migration",
        "one panel",
        "its header",
        "the overflow menu",
        "the fold threshold",
    ] {
        open_row(&mut world, title);
    }
    assert_eq!(
        trail(&world),
        ["Garden", "…", "its header", "the overflow menu"]
    );

    click(&mut world, "Show 2 more steps");

    assert_eq!(
        trail(&world),
        [
            "Garden",
            "the migration",
            "one panel",
            "its header",
            "the overflow menu"
        ]
    );
    assert!(rests(&mut world));
}

#[test]
fn switches_siblings_when_a_row_in_an_earlier_column_is_clicked() {
    let mut seeds = deep();
    seeds.push(plot("s-mid222", "another panel", "s-crown1"));
    let mut world = open_garden_sized(daemon(seeds, Vec::new()), THREE_COLUMNS);
    click(&mut world, "Expand the garden");
    open_row(&mut world, "the migration");
    open_row(&mut world, "one panel");

    open_row(&mut world, "another panel");

    assert_eq!(heading(&world).as_deref(), Some("another panel"));
    assert_eq!(trail(&world), ["Garden"]);
    assert!(rests(&mut world));
}

#[test]
fn hands_escape_to_the_frame_without_climbing_the_walk_or_clearing_the_question() {
    let mut world = open_garden(deep());
    open_row(&mut world, "the migration");
    open_row(&mut world, "one panel");
    type_query(&mut world, "header");

    key(&mut world, Key::Escape);
    assert_eq!(shown(&world), "closed");

    world.gesture(|app| app.key(board()));
    assert_eq!(
        world
            .screen()
            .get(Target::key("garden.search.field"))
            .value
            .as_deref(),
        Some("header")
    );
    world.gesture(|app| {
        app.click(Target::key("garden.search.field"));
        app.press("mod+a");
        app.key(KeyEvent {
            key: Key::Backspace,
            mods: Mods::default(),
        });
    });
    assert_eq!(heading(&world).as_deref(), Some("one panel"));

    key(&mut world, Key::Escape);
    assert_eq!(shown(&world), "closed");
    assert!(rests(&mut world));
}

#[test]
fn reopens_in_the_frame_the_user_last_chose_however_it_was_closed() {
    let mut world = open_garden(vec![seed("s-alone1", "unrelated work")]);
    assert_eq!(shown(&world), "dock");
    world.gesture(|app| app.key(board()));
    assert_eq!(shown(&world), "full");

    key(&mut world, Key::Escape);
    assert_eq!(shown(&world), "closed");
    world.gesture(|app| app.key(board()));
    assert_eq!(shown(&world), "full");

    click(&mut world, "Return the garden to the dock");
    click(&mut world, "Close");
    assert_eq!(shown(&world), "closed");
    world.gesture(|app| app.key(board()));
    assert_eq!(shown(&world), "dock");
    assert!(rests(&mut world));
}

#[test]
fn holds_the_window_as_a_modal_only_once_expanded_and_offers_the_board_only_there() {
    let mut world = open_garden(vec![seed("s-alone1", "unrelated work")]);
    assert!(
        world
            .screen()
            .find(Target::role("group").name("Garden view"))
            .is_none()
    );

    assert!(
        !world
            .screen()
            .get(Target::role("region").name("The garden"))
            .is("modal")
    );

    click(&mut world, "Expand the garden");

    assert_eq!(shown(&world), "full");
    assert!(
        world
            .screen()
            .get(Target::role("dialog").name("The garden"))
            .is("modal")
    );
    assert!(
        world
            .screen()
            .find(Target::role("group").name("Garden view"))
            .is_some()
    );
    assert!(rests(&mut world));
}

#[test]
fn opens_and_closes_from_the_title_bar_icon_without_turning_a_close_into_a_frame_change() {
    let mut world = open_garden(vec![seed("s-alone1", "unrelated work")]);
    world.gesture(|app| app.key(board()));
    key(&mut world, Key::Escape);

    click(&mut world, "Show the garden");
    assert_eq!(shown(&world), "full");

    click(&mut world, "Return the garden to the dock");
    click(&mut world, "Hide the garden");
    assert_eq!(shown(&world), "closed");

    click(&mut world, "Show the garden");
    assert_eq!(shown(&world), "dock");
    assert!(rests(&mut world));
}

#[test]
fn opens_the_garden_from_the_palette() {
    let mut world = World::start(super::palette::daemon(
        initial(vec![seed("s-alone1", "unrelated work")], Vec::new()),
        Vec::new(),
    ));

    world.gesture(|app| {
        app.key(super::sidebar::chord(Action::UiCommandPalette));
        app.type_text("Open the garden");
    });
    key(&mut world, Key::Enter);

    assert_eq!(shown(&world), "dock");
    assert!(rests(&mut world));
}

#[test]
fn walks_the_rows_of_the_place_it_is_in_and_climbs_back_out() {
    let mut world = open_garden(deep());
    open_row(&mut world, "the migration");

    key(&mut world, Key::Down);
    assert_eq!(focused_row(&world).as_deref(), Some("one panel"));
    key(&mut world, Key::Left);

    assert_eq!(heading(&world), None);
    assert_eq!(focused_row(&world).as_deref(), Some("the migration"));
    assert!(rests(&mut world));
}

#[test]
fn walks_a_column_drills_right_and_climbs_left() {
    let mut world = open_garden_sized(daemon(deep(), Vec::new()), TWO_COLUMNS);
    click(&mut world, "Expand the garden");
    open_row(&mut world, "the migration");
    world.gesture(|app| app.click(Target::key("garden.row.s-mid111")));
    key(&mut world, Key::Left);
    assert_eq!(heading(&world).as_deref(), Some("the migration"));
    assert_eq!(focused_row(&world).as_deref(), Some("one panel"));

    key(&mut world, Key::Right);
    assert_eq!(heading(&world).as_deref(), Some("one panel"));

    key(&mut world, Key::Left);
    key(&mut world, Key::Up);
    assert_eq!(focused_row(&world).as_deref(), Some("one panel"));
    assert!(rests(&mut world));
}

#[test]
fn leaves_the_arrows_to_the_search_field_while_it_has_answers_to_walk() {
    let mut world = open_garden(vec![
        seed("s-one001", "socket one"),
        seed("s-two002", "socket two"),
    ]);
    type_query(&mut world, "socket");

    key(&mut world, Key::Down);

    let field = focused(&world).and_then(|n| n.key);
    assert_eq!(field.as_deref(), Some("garden.search.field"));
    assert_eq!(active_answer(&world).as_deref(), Some("socket two"));
    assert!(rests(&mut world));
}

#[test]
fn hands_the_arrows_back_to_the_walk_when_the_field_has_no_answers_to_walk() {
    let mut world = open_garden(vec![seed("s-one001", "socket one")]);
    world.gesture(|app| app.click(Target::key("garden.search.field")));

    key(&mut world, Key::Down);

    assert_eq!(focused_row(&world).as_deref(), Some("socket one"));
    assert!(rests(&mut world));
}

#[test]
fn returns_to_the_search_field_from_anywhere_in_the_panel() {
    let mut world = open_garden(vec![seed("s-one001", "socket one")]);
    world.gesture(|app| app.click(Target::key("garden.search.field")));
    key(&mut world, Key::Down);
    assert_eq!(focused_row(&world).as_deref(), Some("socket one"));

    world.gesture(|app| app.type_text("/"));

    let field = focused(&world).and_then(|n| n.key);
    assert_eq!(field.as_deref(), Some("garden.search.field"));
    assert!(rests(&mut world));
}

#[test]
fn flattens_the_garden_into_the_answer_out_of_every_plot_at_once() {
    let mut world = open_garden(deep());

    type_query(&mut world, "the");

    let found = rows(&world);
    assert!(found.contains(&"the actual work".to_owned()));
    assert!(found.contains(&"the migration".to_owned()));
    assert!(!found.contains(&"already shipped".to_owned()));
    assert!(rests(&mut world));
}

#[test]
fn shows_the_line_of_body_that_answered_the_query() {
    let mut world = open_garden(vec![with(seed("s-body01", "nothing here"), |s| {
        s.body = "a passing mention of the beacon".into()
    })]);

    type_query(&mut world, "beacon");

    assert!(
        world
            .screen()
            .descendants(Target::key("garden.row.s-body01"))
            .iter()
            .any(|n| n.name.as_deref() == Some("a passing mention of the beacon"))
    );
    assert!(rests(&mut world));
}

#[test]
fn follows_a_tender_session_to_its_new_label() {
    let tended = with(seed("s-tend01", "tended work"), |s| {
        s.tender = Some(PartyView {
            name: "old name".into(),
            r#ref: "session:sess-1".into(),
            session_id: Some("sess-1".into()),
        });
        s.claimed = true;
        s.status = "growing".into();
    });
    let mut world = open_garden_with(daemon(vec![tended], vec![session("sess-1", "builder")]));
    type_query(&mut world, "tender:builder");
    assert_eq!(rows(&world), ["tended work"]);

    world.daemon.emit(SessionStateChangedMessage {
        session: session("sess-1", "reviewer"),
        ..SessionStateChangedMessage::new()
    });
    world.settle();

    assert!(rows(&world).is_empty());
    type_query(&mut world, "");
    world.gesture(|app| {
        app.press("mod+a");
        app.type_text("tender:reviewer");
    });
    assert_eq!(rows(&world), ["tended work"]);
    assert!(rests(&mut world));
}

#[test]
fn names_a_filter_it_does_not_have_rather_than_answering_with_an_empty_list() {
    let mut world = open_garden(vec![seed("s-one001", "socket one")]);

    type_query(&mut world, "is:done ");

    assert!(shows(&world, "no filter called is:done"));
    assert!(rests(&mut world));
}

#[test]
fn offers_the_values_of_an_operator_being_typed_and_puts_one_into_the_query() {
    let mut world = open_garden(vec![seed("s-one001", "socket one")]);
    type_query(&mut world, "socket is:");

    click(&mut world, "ready");

    assert_eq!(
        world
            .screen()
            .get(Target::key("garden.search.field"))
            .value
            .as_deref(),
        Some("socket is:ready ")
    );
    assert!(rests(&mut world));
}

#[test]
fn names_the_query_and_the_scope_when_nothing_matches_and_clears_it_on_request() {
    let mut world = open_garden(vec![seed("s-one001", "socket one")]);
    type_query(&mut world, "beacon");
    assert!(shows(&world, "Nothing in the garden matches beacon."));

    click(&mut world, "Clear the search");

    assert_eq!(rows(&world), ["socket one"]);
    assert!(rests(&mut world));
}

#[test]
fn counts_what_a_search_is_hiding_and_takes_it_back_when_asked() {
    let mut world = open_garden(deep());
    type_query(&mut world, "shipped");
    assert!(rows(&world).is_empty());

    click(&mut world, "Include closed seeds 1");

    assert_eq!(rows(&world), ["already shipped"]);
    assert!(rests(&mut world));
}

#[test]
fn searches_the_plot_it_stands_in_and_widens_with_one_key() {
    let mut world = open_garden(deep());
    open_row(&mut world, "the migration");
    type_query(&mut world, "work");
    assert_eq!(rows(&world), ["the actual work"]);
    assert!(
        world
            .screen()
            .find(Target::role("button").name("+1 in the whole garden"))
            .is_some()
    );

    key_with(
        &mut world,
        Key::Enter,
        Mods {
            alt: true,
            ..Mods::default()
        },
    );
    assert_eq!(rows(&world).len(), 2);
    assert!(has_row(&world, "unrelated work"));
    assert_eq!(trail(&world), ["Garden"]);

    key_with(
        &mut world,
        Key::Enter,
        Mods {
            alt: true,
            ..Mods::default()
        },
    );
    assert_eq!(rows(&world), ["the actual work"]);
    assert!(rests(&mut world));
}

#[test]
fn walks_the_answers_with_the_arrows_without_wrapping_past_the_ends() {
    let mut world = open_garden(vec![
        seed("s-one001", "socket one"),
        seed("s-two002", "socket two"),
    ]);
    type_query(&mut world, "socket");
    key(&mut world, Key::Up);
    assert_eq!(active_answer(&world).as_deref(), Some("socket one"));
    key(&mut world, Key::Down);
    key(&mut world, Key::Down);
    assert_eq!(active_answer(&world).as_deref(), Some("socket two"));
    key(&mut world, Key::Enter);

    assert_eq!(heading(&world).as_deref(), Some("socket two"));
    assert!(rests(&mut world));
}

#[test]
fn says_how_many_open_seeds_block_a_seed_and_nothing_once_its_blocker_closes() {
    let blocker = with(seed("s-blok01", "the blocker"), |s| {
        s.edges.push(edge("blocks", "s-held01"))
    });
    let mut world = open_garden(vec![
        blocker.clone(),
        seed("s-held01", "the held one"),
        seed("s-free01", "the free one"),
    ]);
    assert!(row_says(&world, "s-held01", "blocked by 1"));
    assert!(!row_says(&world, "s-free01", "blocked by"));

    push(
        &mut world,
        vec![
            closed(blocker, "harvested"),
            seed("s-held01", "the held one"),
            seed("s-free01", "the free one"),
        ],
    );

    assert!(!row_says(&world, "s-held01", "blocked by"));
    assert!(rests(&mut world));
}

#[test]
fn lists_an_opened_seeds_edges_in_both_directions() {
    let blocker = with(seed("s-blok01", "the blocker"), |s| {
        s.edges.push(edge("blocks", "s-held01"))
    });
    let mut world = open_garden(vec![
        blocker,
        part_of("s-crown1", "s-held01", "the held one"),
        plot("s-crown1", "the crown", ""),
    ]);
    open_row(&mut world, "the crown");

    open_row(&mut world, "the held one");

    let related = |label: &str, id: &str| {
        world
            .screen()
            .find(Target::key(format!("garden.related.{label}.{id}")))
            .and_then(|n| n.name)
    };
    assert_eq!(related("part of", "s-crown1").as_deref(), Some("the crown"));
    assert_eq!(
        related("blocked by", "s-blok01").as_deref(),
        Some("the blocker")
    );
    assert!(rests(&mut world));
}

#[test]
fn says_what_an_armed_row_waits_on_where_a_parked_row_says_parked() {
    let condition = |pull_request: &str| SeedHarvestCondition {
        pull_request: pull_request.into(),
        url: "https://github.com/northwind/atlas/pull/412".into(),
        ..SeedHarvestCondition::default()
    };
    let world = &mut open_garden(vec![
        with(seed("s-arm001", "waiting on the merge"), |s| {
            s.status = "parked".into();
            s.harvest_when = Some(condition("github.com:northwind/atlas#412"));
        }),
        with(seed("s-park01", "put down for now"), |s| {
            s.status = "parked".into()
        }),
        with(seed("s-blank1", "armed with nothing"), |s| {
            s.harvest_when = Some(condition("  "))
        }),
    ]);

    assert!(row_says(world, "s-arm001", "harvests on #412"));
    assert!(!row_says(world, "s-arm001", "parked"));
    let marker = world
        .screen()
        .descendants(Target::key("garden.row.s-arm001"))
        .into_iter()
        .find(|n| n.name.as_deref() == Some("harvests on #412"))
        .and_then(|n| n.description);
    assert_eq!(
        marker.as_deref(),
        Some("harvests when northwind/atlas#412 merges")
    );
    assert!(row_says(world, "s-park01", "parked"));
    assert!(!row_says(world, "s-park01", "harvests on"));
    assert!(!row_says(world, "s-blank1", "harvests on"));
    assert!(rests(world));
}

#[test]
fn says_the_whole_condition_on_the_seed_it_opens() {
    let armed = with(seed("s-arm001", "armed work"), |s| {
        s.status = "parked".into();
        s.harvest_when = Some(SeedHarvestCondition {
            pull_request: "github.com:northwind/atlas#412".into(),
            url: "https://github.com/northwind/atlas/pull/412".into(),
            ..SeedHarvestCondition::default()
        });
    });
    let mut world = open_garden(vec![armed]);

    open_row(&mut world, "armed work");

    assert!(
        world
            .screen()
            .descendants(Target::key("garden.head.meta"))
            .iter()
            .any(|n| n.name.as_deref() == Some("harvests when northwind/atlas#412 merges"))
    );
    assert!(rests(&mut world));
}

#[test]
fn follows_a_seed_through_its_life_and_says_why_it_closed_once_opened() {
    let work = seed("s-life01", "the work");
    let mut world = open_garden(vec![work.clone()]);
    assert!(!row_says(&world, "s-life01", "planted"));

    push(
        &mut world,
        vec![with(work.clone(), |s| {
            s.status = "growing".into();
            s.claimed = true;
            s.tender = Some(PartyView {
                name: "Trellis".into(),
                r#ref: "crew:trellis".into(),
                session_id: None,
            });
        })],
    );
    assert!(row_says(&world, "s-life01", "growing"));
    assert!(row_says(&world, "s-life01", "tended by Trellis"));

    let done = with(work, |s| {
        s.status = "harvested".into();
        s.reason = Some("shipped it".into());
    });
    push(&mut world, vec![done]);
    assert!(rows(&world).is_empty());
    assert!(shows(
        &world,
        "Nothing open here. 1 closed seed is behind the closed toggle above."
    ));

    click(&mut world, "1 closed");
    assert!(row_says(&world, "s-life01", "done"));
    assert!(!row_says(&world, "s-life01", "tended by"));
    assert!(!shows(&world, "shipped it"));
    open_row(&mut world, "the work");
    assert!(shows(&world, "shipped it"));
    assert!(rests(&mut world));
}

#[test]
fn reads_the_open_seed_again_on_a_garden_push_and_shows_its_log() {
    let work = seed("s-plan11", "The plan");
    let mut world = World::start(Daemon::scripted().state(initial(vec![work.clone()], Vec::new())));
    let reads = std::sync::Arc::new(std::sync::atomic::AtomicUsize::new(0));
    let counted = reads.clone();
    let answer = work.clone();
    world.daemon.on("seed_document_get", move |r: &Request| {
        counted.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
        message(SeedDocumentGetResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            document: Some(SeedDocument {
                seed: answer.clone(),
                notes: vec![SeedNote {
                    id: "n1".into(),
                    seed_id: answer.id.clone(),
                    kind: "note".into(),
                    body: "first note".into(),
                    author: ActorView {
                        name: "the user".into(),
                        r#ref: "user".into(),
                    },
                    created_at: "2026-11-13T09:00:00Z".into(),
                    ..SeedNote::default()
                }],
                notes_total: 3,
                ..SeedDocument::default()
            }),
            ..SeedDocumentGetResultMessage::new()
        })
    });
    world.gesture(|app| app.key(board()));
    open_row(&mut world, "The plan");
    assert_eq!(reads.load(std::sync::atomic::Ordering::SeqCst), 1);
    assert!(world.screen().find(Target::key("garden.log.n1")).is_some());
    assert!(shows(&world, "2 more entries on the log."));

    push(&mut world, vec![work]);

    assert_eq!(reads.load(std::sync::atomic::Ordering::SeqCst), 2);
    assert!(rests(&mut world));
}

#[test]
fn shows_the_daemons_error_when_the_seed_cannot_be_read() {
    let mut world =
        World::start(Daemon::scripted().state(initial(vec![seed("s-gone01", "gone")], Vec::new())));
    world
        .daemon
        .on("seed_document_get", |r: &Request| document_reply(r, None));
    world.gesture(|app| app.key(board()));

    open_row(&mut world, "gone");

    assert!(shows(&world, "seed \"s-gone01\" not found"));
    assert!(rests(&mut world));
}

#[test]
fn keeps_the_seed_it_opened_when_an_earlier_read_answers_late() {
    let first = with(seed("s-first1", "first seed"), |s| {
        s.body = "the first plan".into()
    });
    let second = with(seed("s-secnd1", "second seed"), |s| {
        s.body = "the second plan".into()
    });
    let mut world = World::start(
        Daemon::scripted().state(initial(vec![first.clone(), second.clone()], Vec::new())),
    );
    world.daemon.answer_in_turn(
        "seed_document_get",
        [attn_testkit::Answer::Hold, attn_testkit::Answer::Hold],
    );
    world.gesture(|app| app.key(board()));
    open_row(&mut world, "first seed");
    key(&mut world, Key::Left);
    open_row(&mut world, "second seed");
    let reads = world.daemon.sent_of("seed_document_get");
    assert_eq!(reads.len(), 2);
    assert_ne!(reads[0]["request_id"], reads[1]["request_id"]);

    world
        .daemon
        .reply_to(&reads[0], logged(&reads[0], first, "n-first"));
    world
        .daemon
        .reply_to(&reads[1], logged(&reads[1], second, "n-second"));
    world.settle();

    assert_eq!(heading(&world).as_deref(), Some("second seed"));
    assert!(
        world
            .screen()
            .find(Target::key("garden.log.n-second"))
            .is_some()
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.log.n-first"))
            .is_none()
    );
    assert!(rests(&mut world));
}

#[test]
fn tab_reaches_every_button_of_the_garden_and_a_plots_page() {
    let mut world = open_garden(deep());
    assert_eq!(
        super::reach::unreached_by_tab(&mut world),
        Vec::<String>::new()
    );

    open_row(&mut world, "the migration");
    click(&mut world, "Expand the garden");
    assert_eq!(
        super::reach::unreached_by_tab(&mut world),
        Vec::<String>::new()
    );
    assert!(world.runtime_is_at_rest());
}
