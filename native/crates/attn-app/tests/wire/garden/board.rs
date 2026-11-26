use attn_protocol::{
    PartyView, SeedHarvestCondition, SeedNoteResultMessage, SeedTransitionResultMessage,
};
use attn_testkit::{Request, Target, World, message};
use rune::Key;
use serde_json::Value;

use super::*;

pub(super) fn armed_on(number: u32) -> SeedHarvestCondition {
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
    for count in ["1 in progress", "1 ready", "1/3 done"] {
        assert!(card_says(world, "a plot in motion", count), "{count}");
    }
    assert_eq!(column(world, "In progress"), ["owned work"]);
    assert!(card_says(world, "owned work", "Trellis"));
    assert!(world.runtime_is_at_rest());
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
    assert!(world.runtime_is_at_rest());
}

#[test]
fn keeps_a_closed_plot_on_the_board_while_work_below_it_is_open_and_folds_it_once_that_closes() {
    let world = &mut open_board(shipped(), Vec::new());

    assert_eq!(column(world, "Ready"), ["unrelated work"]);
    assert_eq!(column(world, "Closed"), ["shipped plot"]);
    assert!(shows(world, "1 harvested"));
    let counted = world
        .screen()
        .find(Target::role("region").name("Closed, 2"))
        .is_some();
    assert!(counted, "{}", world.screen());

    click(world, "Open the plot under shipped plot");
    assert_eq!(column(world, "Closed"), ["shipped part"]);
    assert!(shows(world, "0 harvested · 1 withered"));
    click(world, "Open the plot under shipped part");
    assert_eq!(column(world, "Ready"), ["open follow-up"]);
    assert!(column(world, "Closed").is_empty());

    click(world, "Garden");
    push_change(world, &shipped(), last_follow_up_harvested());
    assert!(column(world, "Closed").is_empty());
    assert!(shows(world, "2 harvested"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn shows_the_folded_closed_work_beside_the_plot_the_board_keeps() {
    let world = &mut open_board(shipped(), Vec::new());

    click(world, "show");
    assert_eq!(column(world, "Closed"), ["shipped plot", "finished plot"]);
    click(world, "hide");
    assert_eq!(column(world, "Closed"), ["shipped plot"]);

    push(
        world,
        shipped()
            .into_iter()
            .filter(|s| s.id != "s-over11" && s.id != "s-last11")
            .collect(),
    );
    assert_eq!(column(world, "Closed"), ["shipped plot"]);
    for gone in [
        Target::role("button").name("show"),
        Target::key("garden.board.closed.summary"),
    ] {
        assert!(world.screen().find(gone).is_none(), "nothing is folded");
    }
    assert!(world.runtime_is_at_rest());
}

#[test]
fn walks_the_arrows_to_the_follow_up_below_a_closed_plot_the_board_keeps() {
    let world = &mut open_board(shipped(), Vec::new());

    key(world, Key::Down);
    assert_eq!(focused_card(world).as_deref(), Some("unrelated work"));
    key(world, Key::Right);
    assert_eq!(focused_card(world).as_deref(), Some("shipped plot"));
    key(world, Key::Down);
    assert_eq!(focused_card(world).as_deref(), Some("shipped plot"));
    key(world, Key::Right);
    key(world, Key::Down);
    assert_eq!(focused_card(world).as_deref(), Some("shipped part"));
    key(world, Key::Right);
    key(world, Key::Down);
    assert_eq!(focused_card(world).as_deref(), Some("open follow-up"));
    assert!(world.runtime_is_at_rest());
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
    assert!(world.runtime_is_at_rest());
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
    assert!(world.runtime_is_at_rest());
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
    assert!(world.runtime_is_at_rest());
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
    assert!(world.runtime_is_at_rest());
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
    assert!(world.runtime_is_at_rest());
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
    assert!(world.runtime_is_at_rest());
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
    assert!(world.runtime_is_at_rest());
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
    assert!(world.runtime_is_at_rest());
}

#[test]
fn tab_after_opening_a_plot_reaches_the_views_and_close_before_the_way_back() {
    let world = &mut open_board(
        vec![
            plot("s-crown1", "the migration", ""),
            with(part_of("s-crown1", "s-part11", "one panel"), |s| {
                s.status = "growing".into()
            }),
        ],
        Vec::new(),
    );
    key(world, Key::Down);
    key(world, Key::Right);

    let order: Vec<String> = (0..4)
        .map(|_| {
            key(world, Key::Tab);
            focused(world).and_then(|n| n.name).unwrap_or_default()
        })
        .collect();
    assert_eq!(order, ["list", "board", "Close", "Garden"]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn opening_a_plot_and_climbing_back_put_the_board_in_place_on_the_frame_of_the_key() {
    let world = &mut open_board(
        vec![
            plot("s-crown1", "the migration", ""),
            with(part_of("s-crown1", "s-part11", "one panel"), |s| {
                s.status = "growing".into()
            }),
        ],
        Vec::new(),
    );
    key(world, Key::Down);
    for (way, step) in [("in", Key::Right), ("back", Key::Left)] {
        let lane = "garden.board.column.ready";
        let recording = world.record(60.0, 0.5, |_, app| {
            app.key(KeyEvent {
                key: step,
                mods: Mods::default(),
            })
        });
        let settled = world.rect(lane);
        let suffix = format!("/{lane}");
        let track = recording
            .tracks
            .iter()
            .find(|t| t.path.ends_with(&suffix))
            .unwrap_or_else(|| panic!("the ready lane was not drawn going {way}"));
        let pressed = recording.inputs[0].0;
        let moved: Vec<_> = track
            .frames
            .iter()
            .filter(|(frame, at)| {
                *frame >= pressed
                    && at
                        .rect
                        .iter()
                        .zip(settled)
                        .any(|(drawn, still)| (f64::from(*drawn) - still).abs() > 0.5)
            })
            .map(|(frame, at)| (*frame, at.rect))
            .collect();
        assert!(
            moved.is_empty(),
            "going {way}, the ready lane moves on its way to {settled:?}: {moved:?}"
        );
    }
    assert!(world.runtime_is_at_rest());
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
    assert!(world.runtime_is_at_rest());
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
    assert!(world.runtime_is_at_rest());
}

#[test]
fn drops_a_card_onto_each_closing_move_beside_more_kept_plots_than_the_column_holds() {
    let mut seeds = vec![held_by(
        status("s-work11", "owned work", "growing"),
        "member:trellis",
        "Trellis",
        true,
    )];
    for i in 0..16 {
        let id = format!("s-kept{i:02}");
        seeds.push(tallied(
            closed(plot(&id, &format!("shipped plot {i}"), ""), "harvested"),
            3,
            0,
            1,
        ));
        seeds.push(part_of(&id, &format!("s-next{i:02}"), "open follow-up"));
    }
    for (verb, asks) in [
        ("harvest", "Harvest s-work11: what got done"),
        ("wither", "Wither s-work11: why nobody should pick this up"),
    ] {
        let world = &mut open_board(seeds.clone(), Vec::new());
        let from = card_key(world, "owned work");
        let closed = world
            .runtime()
            .find("garden.board.column.closed")
            .expect("the Closed column is laid out");
        world.gesture(|app| app.drag_over(Target::key(from.clone()), closed));
        let zone = world
            .runtime()
            .find(&format!("garden.board.zone.{verb}"))
            .expect("the zone is laid out");
        world.gesture(|app| {
            app.move_to(zone);
            app.release(zone);
        });
        assert_eq!(
            focused(world).and_then(|n| n.name).as_deref(),
            Some(asks),
            "{}",
            world.screen()
        );
        assert!(world.runtime_is_at_rest());
    }
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
    assert!(world.runtime_is_at_rest());
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
    assert!(world.runtime_is_at_rest());
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
    assert!(world.runtime_is_at_rest());
}

#[test]
fn tab_reaches_every_button_of_the_board_with_a_cards_menu_open() {
    let mut world = open_board(garden(), Vec::new());
    select_card(&mut world, "pick this up");
    for key in [
        "garden.board.drill.s-plot11",
        "garden.board.menu-of.s-ready1",
    ] {
        assert!(world.screen().find(Target::key(key)).is_some(), "{key}");
    }
    assert_eq!(
        super::super::reach::unreached_by_tab(&mut world),
        Vec::<String>::new()
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_plot_cards_counts_wrap_inside_it_in_the_smallest_window_at_the_largest_text() {
    let mut world = open_garden_sized(daemon(garden(), Vec::new()), (800.0, 600.0));
    world.daemon.emit(attn_protocol::SettingsUpdatedMessage {
        settings: Some([("uiScale".to_owned(), "1.5".to_owned())].into()),
        ..attn_protocol::SettingsUpdatedMessage::new()
    });
    world.settle();
    world.gesture(|app| app.key(board()));
    click(&mut world, "board");
    let key = card_key(&world, "a plot in motion");
    let screen = world.screen();
    assert!(
        screen
            .find(Target::role("button").name("Open the plot under a plot in motion"))
            .is_some(),
        "the plot's action keeps its name:\n{screen}"
    );
    let [x, _, w, _] = screen.bounds(Target::key(&key)).expect("the plot's card");
    for count in ["1 in progress", "1 ready", "1/3 done"] {
        let [px, _, pw, _] = screen
            .bounds(Target::role("label").name(count))
            .unwrap_or_else(|| panic!("no {count}:\n{screen}"));
        assert!(
            px >= x - 0.5 && px + pw <= x + w + 0.5,
            "{count} at {px}+{pw} runs past its card at {x}+{w}"
        );
    }
}

#[test]
fn a_cards_open_menu_lies_on_its_own_raised_surface_apart_from_its_lane() {
    let mut world = open_board(garden(), Vec::new());
    select_card(&mut world, "pick this up");
    let elements = world.runtime().elements();
    let fill_of = |key: &str| {
        elements
            .iter()
            .find(|e| e.key.as_deref() == Some(key))
            .unwrap_or_else(|| panic!("no {key} on screen"))
            .fill
            .hex()
    };
    let menu = fill_of("garden.board.menu-of.s-ready1");
    assert_ne!(&menu[7..], "00", "the menu has no surface of its own");
    assert_ne!(menu, fill_of("garden.board.column.ready"));
}

#[test]
fn shows_exact_folded_closed_counts_without_cap_banner_and_pages_expanded_closed_cards() {
    let open = with(seed("s-open01", "active task"), |s| s.ready = true);
    let archived_plot = with(
        closed(plot("s-aplt01", "Archived release", ""), "harvested"),
        |s| {
            s.plot_progress = Some(SeedPlotProgress {
                total: 1,
                done: 1,
                withered: 0,
                growing: 0,
                parked: 0,
                ready: 0,
                blocked: 0,
                checks: None,
            });
        },
    );
    let archived_plot_child = closed(
        part_of("s-aplt01", "s-akid01", "Archived step"),
        "harvested",
    );
    let mut page1: Vec<Seed> = vec![archived_plot.clone()];
    page1.extend((0..79).map(|i| {
        closed(
            seed(&format!("s-ac{i:04}"), &format!("archived card {i:02}")),
            "harvested",
        )
    }));
    let page2 = vec![closed(seed("s-ac0099", "older archived"), "withered")];
    let d = Daemon::scripted().state(InitialStateMessage {
        garden_closed_counts: Some(GardenClosedCounts {
            all: GardenClosedTally {
                harvested: 85,
                withered: 5,
            },
            unassigned: GardenClosedTally {
                harvested: 85,
                withered: 5,
            },
            ..GardenClosedCounts::default()
        }),
        seeds_total: Some(91),
        ..initial(vec![open.clone()], Vec::new())
    });
    let (p1, p2, kid) = (page1.clone(), page2.clone(), archived_plot_child.clone());
    d.on("garden_seeds_page", move |r: &Request| {
        if r["plot_id"].as_str() == Some("s-aplt01") {
            return message(GardenSeedsPageResultMessage {
                request_id: r["request_id"].as_str().unwrap().into(),
                success: true,
                seeds: vec![kid.clone()],
                total: 1,
                harvested: 1,
                ..GardenSeedsPageResultMessage::new()
            });
        }
        let (seeds, next) = match r["before"].as_str() {
            None => (p1.clone(), Some("2026-11-12T00:00:00Z|s-ac0078".into())),
            Some("2026-11-12T00:00:00Z|s-ac0078") => (p2.clone(), None),
            _ => (Vec::new(), None),
        };
        message(GardenSeedsPageResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            seeds,
            next_before: next,
            total: 90,
            harvested: 85,
            withered: 5,
            ..GardenSeedsPageResultMessage::new()
        })
    });
    d.on("seed_transition", transition_reply);
    let mut world = open_board_with(d);

    assert!(!shows(&world, "The garden holds"));
    assert_eq!(
        world
            .screen()
            .get(Target::key("garden.board.total"))
            .name
            .as_deref(),
        Some("91")
    );
    assert!(shows(&world, "85 harvested · 5 withered"));
    assert!(column(&world, "Closed").is_empty());

    select_card(&mut world, "active task");
    world.gesture(|app| app.click(Target::key("garden.board.menu.harvest")));
    assert!(shows(&world, "85 harvested · 5 withered"));
    assert!(!shows(&world, "Nothing has finished here yet."));
    key(&mut world, Key::Escape);

    let before_rows = attn_app::garden::rows_built();
    click(&mut world, "show");
    let closed_built = attn_app::garden::rows_built() - before_rows;
    assert!(
        closed_built < 50,
        "Closed column should virtualize rows, built {closed_built}"
    );
    let pages = world.daemon.sent_of("garden_seeds_page");
    assert_eq!(pages.len(), 1);
    assert!(pages[0].get("before").is_none());
    assert!(world.screen().find(Target::key("garden.more")).is_some());
    assert!(world.runtime_is_at_rest());

    let [cx, cy, cw, ch] = world
        .screen()
        .bounds(Target::key("garden.board.body.closed"))
        .expect("closed lane body bounds");
    let center = (cx + cw / 2.0, cy + ch / 2.0);
    for _ in 0..40 {
        if world.daemon.sent_of("garden_seeds_page").len() >= 2 {
            break;
        }
        world.gesture(|app| {
            app.runtime
                .wheel_event(rune::WheelEvent::new(0.0, -300.0).at(center.0, center.1))
        });
    }
    let pages = world.daemon.sent_of("garden_seeds_page");
    assert_eq!(pages.len(), 2);
    assert_eq!(pages[1]["before"], "2026-11-12T00:00:00Z|s-ac0078");
    world.gesture(|app| {
        app.runtime
            .wheel_event(rune::WheelEvent::new(0.0, 20_000.0).at(center.0, center.1))
    });

    select_card(&mut world, "archived card 00");
    assert!(
        world
            .screen()
            .find(Target::key("garden.board.menu.replant"))
            .is_some()
    );
    key(&mut world, Key::Escape);

    world.daemon.emit(GardenSeedsChangedMessage {
        profile_id: PROFILE.into(),
        order: vec!["s-open01".into(), "s-ac0000".into()],
        seeds: vec![with(seed("s-ac0000", "archived card 00"), |s| {
            s.ready = true
        })],
        total: 91,
        closed_counts: Some(GardenClosedCounts {
            all: GardenClosedTally {
                harvested: 84,
                withered: 5,
            },
            unassigned: GardenClosedTally {
                harvested: 84,
                withered: 5,
            },
            ..GardenClosedCounts::default()
        }),
        ..GardenSeedsChangedMessage::new()
    });
    world.settle();
    assert!(column(&world, "Ready").contains(&"archived card 00".to_owned()));
    assert!(!column(&world, "Closed").contains(&"archived card 00".to_owned()));

    world.gesture(|app| app.click(Target::key("garden.board.card.s-aplt01")));
    assert_eq!(column(&world, "Closed"), ["Archived step"]);
    let trail_names: Vec<String> = world
        .screen()
        .descendants(Target::key("garden.board.trail"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(
        trail_names.iter().any(|n| n == "Archived release"),
        "{trail_names:?}"
    );
    assert!(
        world
            .daemon
            .sent_of("garden_seeds_page")
            .iter()
            .any(|r| r["plot_id"] == "s-aplt01")
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn refreshes_closed_history_when_an_old_child_changes_state_outside_the_window() {
    use std::sync::{Arc, Mutex};

    let root = tallied(plot("s-root01", "Release", ""), 1, 0, 0);
    let child = part_of("s-root01", "s-arch01", "Historical check");
    let mut live = vec![root.clone(), plot("s-other1", "Unrelated active plot", "")];
    live.extend((0..998).map(|i| part_of("s-other1", &format!("s-o{i:05}"), "An active step")));
    let total = live.len() as i64 + 1;
    let live_order: Vec<String> = live.iter().map(|seed| seed.id.clone()).collect();
    let mut replanted_order = live_order.clone();
    replanted_order.push(child.id.clone());

    let d = Daemon::scripted().state(InitialStateMessage {
        garden_closed_counts: Some(GardenClosedCounts::default()),
        seeds_total: Some(total),
        ..initial(live, Vec::new())
    });
    let archived = Arc::new(Mutex::new(Some(closed(child.clone(), "harvested"))));
    let page_data = archived.clone();
    d.on("garden_seeds_page", move |r: &Request| {
        assert_eq!(r["plot_id"], "s-root01");
        let seeds: Vec<_> = page_data.lock().unwrap().clone().into_iter().collect();
        message(GardenSeedsPageResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            total: seeds.len() as i64,
            harvested: i64::from(seeds.iter().any(|seed| seed.status == "harvested")),
            withered: i64::from(seeds.iter().any(|seed| seed.status == "withered")),
            seeds,
            ..GardenSeedsPageResultMessage::new()
        })
    });
    let mut world = open_board_with(d);
    assert!(world.daemon.sent_of("garden_seeds_page").is_empty());
    world.gesture(|app| app.click(Target::key("garden.board.card.s-root01")));
    assert!(shows(&world, "1 harvested"));
    click(&mut world, "show");
    assert_eq!(column(&world, "Closed"), ["Historical check"]);

    *archived.lock().unwrap() = None;
    world.daemon.emit(GardenSeedsChangedMessage {
        profile_id: PROFILE.into(),
        order: replanted_order,
        seeds: vec![tallied(root.clone(), 0, 0, 1), child.clone()],
        total,
        closed_counts: Some(GardenClosedCounts::default()),
        ..GardenSeedsChangedMessage::new()
    });
    world.settle();
    assert!(column(&world, "Closed").is_empty());

    *archived.lock().unwrap() = Some(closed(child, "withered"));
    world.daemon.emit(GardenSeedsChangedMessage {
        profile_id: PROFILE.into(),
        order: live_order,
        seeds: vec![tallied(root, 0, 1, 0)],
        total,
        closed_counts: Some(GardenClosedCounts::default()),
        ..GardenSeedsChangedMessage::new()
    });
    world.settle();
    assert_eq!(column(&world, "Closed"), ["Historical check"]);
    click(&mut world, "hide");
    assert!(
        shows(&world, "0 harvested · 1 withered"),
        "{}",
        world.screen()
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn folded_closed_lane_counts_only_direct_children() {
    let root = tallied(plot("s-root01", "Release", ""), 1, 0, 1);
    let nested = tallied(plot("s-nest01", "Verification", "s-root01"), 1, 0, 0);
    let grandchild = closed(part_of("s-nest01", "s-done01", "Smoke test"), "harvested");
    let mut world = open_board(vec![root, nested, grandchild], Vec::new());
    world.gesture(|app| app.click(Target::key("garden.board.card.s-root01")));
    assert!(column(&world, "Closed").is_empty());
    assert!(
        world
            .screen()
            .find(Target::role("region").name("Closed, 0"))
            .is_some()
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.board.closed.reveal"))
            .is_none()
    );

    world.gesture(|app| app.click(Target::key("garden.board.card.s-nest01")));
    assert!(shows(&world, "1 harvested"));
    click(&mut world, "show");
    assert_eq!(column(&world, "Closed"), ["Smoke test"]);
    assert!(world.runtime_is_at_rest());
}
