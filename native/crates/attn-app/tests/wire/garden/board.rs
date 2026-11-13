use attn_protocol::{
    PartyView, SeedHarvestCondition, SeedNoteResultMessage, SeedTransitionResultMessage,
};
use attn_testkit::{Request, Target, World, message};
use rune::Key;
use serde_json::Value;

use super::*;

fn armed_on(number: u32) -> SeedHarvestCondition {
    SeedHarvestCondition {
        pull_request: format!("github.com:victorarias/attn#{number}"),
        url: format!("https://github.com/victorarias/attn/pull/{number}"),
        ..SeedHarvestCondition::default()
    }
}

fn status(id: &str, title: &str, status: &str) -> Seed {
    with(seed(id, title), |s| s.status = status.into())
}

fn held_by(seed: Seed, reference: &str, name: &str, live: bool) -> Seed {
    with(seed, |s| {
        s.tender = Some(PartyView {
            r#ref: reference.into(),
            name: name.into(),
            ..PartyView::default()
        });
        s.claimed = live;
    })
}

fn garden() -> Vec<Seed> {
    vec![
        with(seed("s-ready1", "pick this up"), |s| s.ready = true),
        seed("s-wait11", "waiting on work"),
        with(plot("s-plot11", "a plot in motion", ""), |s| {
            s.ready = true;
            s.plot_progress = Some(SeedPlotProgress {
                total: 3,
                done: 1,
                growing: 1,
                ready: 1,
                ..SeedPlotProgress::default()
            });
        }),
        held_by(
            status("s-work11", "owned work", "growing"),
            "member:trellis",
            "Trellis",
            true,
        ),
        status("s-park11", "paused on purpose", "parked"),
        with(status("s-armed1", "waiting on the merge", "parked"), |s| {
            s.harvest_when = Some(armed_on(42))
        }),
        with(seed("s-armrdy", "armed but still pickable"), |s| {
            s.ready = true;
            s.harvest_when = Some(armed_on(43));
        }),
        status("s-done11", "finished work", "harvested"),
    ]
}

fn transition_reply(request: &Request) -> Value {
    message(SeedTransitionResultMessage {
        request_id: request["request_id"].as_str().unwrap().into(),
        success: true,
        ..SeedTransitionResultMessage::new()
    })
}

/// The board over the window, with a daemon that accepts every move.
fn open_board(seeds: Vec<Seed>, sessions: Vec<Session>) -> World {
    let daemon = daemon(seeds, sessions);
    daemon.on("seed_transition", transition_reply);
    open_board_with(daemon)
}

fn open_board_with(daemon: Daemon) -> World {
    let mut world = open_garden_sized(daemon, THREE_COLUMNS);
    world.gesture(|app| app.key(board()));
    click(&mut world, "board");
    world
}

fn card_key(world: &World, title: &str) -> String {
    world
        .screen()
        .all(Target::role("button").name(title))
        .into_iter()
        .find_map(|n| n.key.filter(|k| k.starts_with("garden.board.card.")))
        .unwrap_or_else(|| panic!("no card {title}:\n{}", world.screen()))
}

/// The words a card shows, title first.
fn card_words(world: &World, title: &str) -> Vec<String> {
    let key = card_key(world, title);
    world
        .screen()
        .descendants(Target::key(key))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn card_says(world: &World, title: &str, words: &str) -> bool {
    card_words(world, title).iter().any(|w| w == words)
}

/// The cards in the column named `label`, top to bottom.
fn column(world: &World, label: &str) -> Vec<String> {
    let screen = world.screen();
    let region = screen
        .all(Target::role("region"))
        .into_iter()
        .find(|n| {
            n.name
                .as_deref()
                .is_some_and(|name| name.starts_with(&format!("{label}, ")))
        })
        .unwrap_or_else(|| panic!("no column {label}:\n{screen}"));
    screen
        .descendants(Target::key(region.key.unwrap()))
        .into_iter()
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with("garden.board.card."))
        })
        .filter_map(|n| n.name)
        .collect()
}

fn focused_card(world: &World) -> Option<String> {
    focused(world)
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with("garden.board.card."))
        })
        .and_then(|n| n.name)
}

fn select_card(world: &mut World, title: &str) {
    let key = card_key(world, title);
    world.gesture(|app| app.click(Target::key(key)));
}

/// Opens `title`'s menu, picks `verb`, writes `words` and presses Return.
fn move_card(world: &mut World, title: &str, verb: &str, prompt: &str, words: &str) {
    select_card(world, title);
    world.gesture(|app| app.click(Target::role("menu-item").name(verb)));
    world.gesture(|app| app.type_text(words));
    assert_eq!(
        focused(world).and_then(|n| n.name).as_deref(),
        Some(prompt),
        "{}",
        world.screen()
    );
    key(world, Key::Enter);
}

#[test]
fn names_active_work_in_progress_and_counts_a_plot_in_motion() {
    let world = &mut open_board(garden(), Vec::new());

    let headings: Vec<String> = world
        .screen()
        .descendants(Target::key("garden.board"))
        .into_iter()
        .filter(|n| n.role == "heading")
        .filter_map(|n| n.name)
        .collect();
    assert_eq!(headings, ["Ready", "In progress", "Parked", "Closed"]);
    assert!(card_says(
        world,
        "a plot in motion",
        "1 in progress · 1 ready · 1/3 done"
    ));
    assert_eq!(column(world, "In progress"), ["owned work"]);
    assert!(card_says(world, "owned work", "Trellis"));
    assert!(rests(world));
}

#[test]
fn puts_parked_work_under_parked_and_counts_closed_work_until_shown() {
    let world = &mut open_board(garden(), Vec::new());

    assert!(column(world, "Parked").contains(&"paused on purpose".to_owned()));
    assert!(card_says(world, "paused on purpose", "parked"));
    assert!(column(world, "Closed").is_empty());
    assert!(shows(world, "1 harvested"));

    click(world, "show");
    assert_eq!(column(world, "Closed"), ["finished work"]);
    assert!(rests(world));
}

#[test]
fn says_what_an_armed_card_waits_on_instead_of_repeating_its_column_and_keeps_it_beside_ready() {
    let world = &mut open_board(garden(), Vec::new());

    assert!(card_says(world, "waiting on the merge", "harvests on #42"));
    assert!(!card_says(world, "waiting on the merge", "parked"));
    let marker = world
        .screen()
        .descendants(Target::key(card_key(world, "waiting on the merge")))
        .into_iter()
        .find(|n| n.name.as_deref() == Some("harvests on #42"))
        .unwrap();
    assert_eq!(
        marker.description.as_deref(),
        Some("harvests when victorarias/attn#42 merges")
    );
    assert!(
        !card_words(world, "paused on purpose")
            .iter()
            .any(|w| w.starts_with("harvests on"))
    );
    assert!(card_says(world, "armed but still pickable", "ready"));
    assert!(card_says(
        world,
        "armed but still pickable",
        "harvests on #43"
    ));
    assert!(rests(world));
}

#[test]
fn sorts_ready_work_first_and_says_how_many_are_not_ready_yet() {
    let world = &mut open_board(garden(), Vec::new());

    assert_eq!(
        column(world, "Ready"),
        [
            "pick this up",
            "a plot in motion",
            "armed but still pickable",
            "waiting on work"
        ]
    );
    let count = world
        .screen()
        .find(Target::role("region").name("Ready, 3"))
        .is_some();
    assert!(count, "{}", world.screen());
    assert!(rests(world));
}

#[test]
fn takes_a_seed_over_from_its_live_tender_when_harvesting_it() {
    let held = held_by(
        status("s-held11", "held work", "growing"),
        "session:live-tender",
        "live-tender",
        true,
    );
    let world = &mut open_board(vec![held], vec![session("live-tender", "live-tender")]);

    select_card(world, "held work");
    world.gesture(|app| app.click(Target::role("menu-item").name("Harvest")));
    assert!(
        world
            .screen()
            .descendants(Target::key("garden.board.compose"))
            .iter()
            .any(|n| n.name.as_deref() == Some("takes it from live-tender"))
    );
    world.gesture(|app| app.type_text("the work is complete"));
    key(world, Key::Enter);

    let sent = world.daemon.sent_of("seed_transition");
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["seed_id"], "s-held11");
    assert_eq!(sent[0]["verb"], "harvest");
    assert_eq!(sent[0]["reason"], "the work is complete");
    assert_eq!(sent[0]["force"], true);
    assert!(
        world
            .screen()
            .find(Target::key("garden.board.compose"))
            .is_none()
    );
    assert!(rests(world));
}

#[test]
fn parks_a_seed_with_its_comment_in_one_move() {
    let world = &mut open_board(
        vec![status("s-quiet1", "quiet work", "growing")],
        Vec::new(),
    );

    move_card(
        world,
        "quiet work",
        "Park",
        "Park s-quiet1: what you are leaving it at",
        "waiting for product input",
    );

    let sent = world.daemon.sent_of("seed_transition");
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["seed_id"], "s-quiet1");
    assert_eq!(sent[0]["verb"], "park");
    assert_eq!(sent[0]["comment"], "waiting for product input");
    assert!(sent[0].get("force").is_none());
    assert!(sent[0].get("reason").is_none());
    assert!(world.daemon.sent_of("seed_note").is_empty());
    assert!(rests(world));
}

#[test]
fn replants_by_writing_the_reason_on_the_log_first() {
    let daemon = daemon(
        vec![status("s-again1", "back again", "withered")],
        Vec::new(),
    );
    daemon.on("seed_transition", transition_reply);
    daemon.on("seed_note", |r: &Request| {
        message(SeedNoteResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            ..SeedNoteResultMessage::new()
        })
    });
    let world = &mut open_board_with(daemon);
    click(world, "show");

    move_card(
        world,
        "back again",
        "Replant",
        "Replant s-again1: why it is open again",
        "the bug came back",
    );

    let notes = world.daemon.sent_of("seed_note");
    assert_eq!(notes.len(), 1);
    assert_eq!(notes[0]["seed_id"], "s-again1");
    assert_eq!(notes[0]["body"], "the bug came back");
    let sent = world.daemon.sent_of("seed_transition");
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["verb"], "replant");
    assert!(sent[0].get("reason").is_none());
    assert!(rests(world));
}

#[test]
fn asks_for_what_got_done_before_harvesting_and_shows_a_refusal() {
    let daemon = daemon(vec![status("s-busy11", "busy work", "growing")], Vec::new());
    daemon.on("seed_transition", |r: &Request| {
        message(SeedTransitionResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: false,
            error: Some("seed s-busy11 has unmet checks".into()),
            ..SeedTransitionResultMessage::new()
        })
    });
    let world = &mut open_board_with(daemon);

    select_card(world, "busy work");
    world.gesture(|app| app.click(Target::role("menu-item").name("Harvest")));
    key(world, Key::Enter);
    assert!(world.daemon.sent_of("seed_transition").is_empty());
    let alert = world
        .screen()
        .find(Target::role("alert"))
        .and_then(|n| n.name);
    assert_eq!(
        alert.as_deref(),
        Some("harvesting s-busy11 records what got done")
    );

    world.gesture(|app| app.type_text("shipped it"));
    key(world, Key::Enter);
    let alert = world
        .screen()
        .find(Target::role("alert"))
        .and_then(|n| n.name);
    assert_eq!(alert.as_deref(), Some("seed s-busy11 has unmet checks"));
    assert!(
        world
            .screen()
            .find(Target::key("garden.board.compose"))
            .is_some()
    );

    click(world, "Dismiss");
    assert!(world.screen().find(Target::role("alert")).is_none());
    assert!(rests(world));
}

#[test]
fn walks_the_cards_with_the_arrows_and_opens_a_card_menu_with_return() {
    let world = &mut open_board(garden(), Vec::new());

    key(world, Key::Down);
    assert_eq!(focused_card(world).as_deref(), Some("pick this up"));
    key(world, Key::Down);
    assert_eq!(focused_card(world).as_deref(), Some("a plot in motion"));
    key(world, Key::Left);
    key(world, Key::Up);
    key(world, Key::Right);
    assert_eq!(focused_card(world).as_deref(), Some("owned work"));
    key(world, Key::Right);
    assert_eq!(focused_card(world).as_deref(), Some("paused on purpose"));
    key(world, Key::Down);
    assert_eq!(focused_card(world).as_deref(), Some("waiting on the merge"));
    key(world, Key::Right);
    assert_eq!(
        focused_card(world).as_deref(),
        Some("waiting on the merge"),
        "Closed stays folded"
    );

    key(world, Key::Enter);
    let focus = focused(world).unwrap();
    assert_eq!(
        (focus.role.as_str(), focus.name.as_deref()),
        ("menu-item", Some("Harvest"))
    );
    key(world, Key::Down);
    assert_eq!(
        focused(world).and_then(|n| n.name).as_deref(),
        Some("Wither")
    );
    key(world, Key::Enter);
    assert_eq!(
        focused(world).and_then(|n| n.name).as_deref(),
        Some("Wither s-armed1: why nobody should pick this up")
    );
    assert!(rests(world));
}

#[test]
fn opens_a_plot_with_the_right_arrow_and_climbs_out_with_the_left() {
    let world = &mut open_board(
        vec![
            plot("s-crown1", "the migration", ""),
            with(part_of("s-crown1", "s-part11", "one panel"), |s| {
                s.status = "growing".into()
            }),
            seed("s-other1", "elsewhere"),
        ],
        Vec::new(),
    );

    key(world, Key::Down);
    assert_eq!(focused_card(world).as_deref(), Some("the migration"));
    key(world, Key::Right);
    assert_eq!(column(world, "In progress"), ["one panel"]);
    assert!(column(world, "Ready").is_empty());
    assert!(shows(
        world,
        "Nothing to pick up — everything open is being worked."
    ));
    let steps: Vec<String> = world
        .screen()
        .descendants(Target::key("garden.board.trail"))
        .into_iter()
        .filter(|n| n.role == "button")
        .filter_map(|n| n.name)
        .collect();
    assert_eq!(steps, ["Garden", "the migration"]);

    key(world, Key::Left);
    assert_eq!(column(world, "Ready"), ["the migration", "elsewhere"]);
    assert!(rests(world));
}

#[test]
fn puts_away_the_compose_line_then_the_menu_then_the_garden_with_escape() {
    let world = &mut open_board(garden(), Vec::new());

    select_card(world, "owned work");
    world.gesture(|app| app.click(Target::role("menu-item").name("Park")));
    key(world, Key::Escape);
    assert!(
        world
            .screen()
            .find(Target::key("garden.board.compose"))
            .is_none()
    );
    assert_eq!(focused_card(world).as_deref(), Some("owned work"));

    key(world, Key::Enter);
    assert!(world.screen().find(Target::role("menu")).is_some());
    key(world, Key::Escape);
    assert!(world.screen().find(Target::role("menu")).is_none());
    assert_eq!(shown(world), "full");

    key(world, Key::Escape);
    assert_eq!(shown(world), "closed");
    assert!(rests(world));
}

#[test]
fn drags_a_card_onto_a_move_its_target_column_offers() {
    let world = &mut open_board(garden(), Vec::new());
    let from = card_key(world, "owned work");
    let closed = world
        .runtime()
        .find("garden.board.column.closed")
        .expect("the Closed column is laid out");

    world.gesture(|app| app.drag_over(Target::key(from.clone()), closed));
    let zones: Vec<String> = world
        .screen()
        .descendants(Target::key("garden.board.column.closed"))
        .into_iter()
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with("garden.board.zone."))
        })
        .filter_map(|n| n.name)
        .collect();
    assert_eq!(zones, ["Harvest s-work11", "Wither s-work11"]);
    assert!(
        world
            .screen()
            .descendants(Target::key("garden.board.column.growing"))
            .iter()
            .all(|n| !n
                .key
                .as_deref()
                .is_some_and(|k| k.starts_with("garden.board.zone."))),
        "a column the card cannot move to offers nothing"
    );

    let wither = world
        .runtime()
        .find("garden.board.zone.wither")
        .expect("the wither zone is laid out");
    world.gesture(|app| {
        app.move_to(wither);
        app.release(wither);
    });
    assert_eq!(
        focused(world).and_then(|n| n.name).as_deref(),
        Some("Wither s-work11: why nobody should pick this up")
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.board.carry"))
            .is_none()
    );
    assert!(rests(world));
}

#[test]
fn one_changed_seed_rebuilds_only_its_card() {
    let seeds: Vec<Seed> = (0..20)
        .map(|i| seed(&format!("s-card{i:02}"), &format!("seed {i}")))
        .collect();
    let world = &mut open_board(seeds.clone(), Vec::new());
    let before = attn_app::garden::rows_built();

    world.daemon.emit(GardenSeedsChangedMessage {
        profile_id: PROFILE.into(),
        order: seeds.iter().map(|s| s.id.clone()).collect(),
        seeds: vec![status("s-card03", "seed 3", "growing")],
        total: 20,
        ..GardenSeedsChangedMessage::new()
    });
    world.settle();

    assert_eq!(attn_app::garden::rows_built() - before, 1);
    assert_eq!(column(world, "In progress"), ["seed 3"]);
    assert!(rests(world));
}

#[test]
fn keeps_the_keys_on_the_board_once_a_harvested_card_folds_into_closed() {
    let seeds = vec![
        status("s-fini11", "about done", "growing"),
        status("s-next11", "still going", "growing"),
    ];
    let daemon = daemon(seeds.clone(), Vec::new());
    daemon.on("seed_transition", move |r: &Request| {
        let moved = vec![
            status("s-fini11", "about done", "harvested"),
            seeds[1].clone(),
        ];
        vec![
            transition_reply(r),
            message(GardenSeedsUpdatedMessage {
                profile_id: PROFILE.into(),
                total: 2,
                seeds: moved,
                ..GardenSeedsUpdatedMessage::new()
            }),
        ]
    });
    let world = &mut open_board_with(daemon);

    move_card(
        world,
        "about done",
        "Harvest",
        "Harvest s-fini11: what got done",
        "shipped",
    );

    assert!(column(world, "Closed").is_empty());
    assert_eq!(
        focused(world).and_then(|n| n.key).as_deref(),
        Some("garden.board")
    );
    key(world, Key::Down);
    assert_eq!(focused_card(world).as_deref(), Some("still going"));
    assert!(rests(world));
}

#[test]
fn lets_go_of_a_card_a_push_takes_away() {
    let world = &mut open_board(
        vec![seed("s-gone11", "about to go"), seed("s-stay11", "staying")],
        Vec::new(),
    );
    key(world, Key::Down);
    assert_eq!(focused_card(world).as_deref(), Some("about to go"));

    push(world, vec![seed("s-stay11", "staying")]);
    key(world, Key::Enter);
    assert!(world.screen().find(Target::role("menu")).is_none());
    key(world, Key::Down);
    assert_eq!(focused_card(world).as_deref(), Some("staying"));
    assert!(rests(world));
}
