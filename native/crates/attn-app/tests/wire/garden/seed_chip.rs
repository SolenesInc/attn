use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    CommandUsage, Desktop, DesktopPane, GardenSeedsUpdatedMessage, GetCommandUsageResultMessage,
    PartyView, RecordCommandUsageResultMessage, SeedPlotProgress, SessionStateChangedMessage,
};
use attn_testkit::tree::pane;
use attn_testkit::{Request, Target, World, key_event};
use rune::KeyPlatform;

use super::*;

const DESKTOP: &str = "work/desktop_1";

fn tended_by(seed: Seed, session: &str) -> Seed {
    with(seed, |s| {
        s.status = "growing".into();
        s.claimed = true;
        s.tender = Some(PartyView {
            name: "Builder".into(),
            r#ref: format!("session:{session}"),
            session_id: Some(session.into()),
        });
    })
}

fn claimed_for(seed: Seed, member: &str, session: Option<&str>) -> Seed {
    with(seed, |s| {
        s.status = "growing".into();
        s.claimed = true;
        s.tender = Some(PartyView {
            name: member.into(),
            r#ref: format!("member:{member}"),
            session_id: session.map(str::to_owned),
        });
    })
}

fn agent(seed_id: Option<&str>) -> Session {
    Session {
        seed_id: seed_id.map(str::to_owned),
        ..session("s1", "Agent a")
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

/// The agent `session` alone on a desktop, over a garden of `seeds`, with a daemon that leaves
/// seed reads for the test to answer.
fn start(seeds: Vec<Seed>, session: Session) -> World {
    let daemon = Daemon::scripted().state(InitialStateMessage {
        profiles: Some(vec![Profile {
            id: PROFILE.into(),
            name: "Work".into(),
            current_desktop_id: DESKTOP.into(),
            ..Profile::default()
        }]),
        desktops: Some(vec![desktop()]),
        ..initial(seeds, vec![session])
    });
    daemon
        .on("get_command_usage", |r| {
            message(GetCommandUsageResultMessage {
                request_id: r["request_id"].as_str().unwrap().into(),
                profile_id: r["profile_id"].as_str().unwrap().into(),
                success: true,
                entries: Vec::<CommandUsage>::new(),
                ..GetCommandUsageResultMessage::new()
            })
        })
        .on("record_command_usage", |r| {
            message(RecordCommandUsageResultMessage {
                request_id: r["request_id"].as_str().unwrap().into(),
                success: true,
                ..RecordCommandUsageResultMessage::new()
            })
        });
    let mut world = World::start_sized(daemon, (1400.0, 900.0));
    world.open_desktop();
    world
}

fn chip(world: &World) -> Option<String> {
    world.screen().find(Target::key("tile.a.seed"))?.name
}

fn opened(world: &World) -> Vec<(String, String)> {
    world
        .daemon
        .sent_of("open_seed")
        .iter()
        .map(|r| {
            (
                r["seed_id"].as_str().unwrap_or_default().to_owned(),
                r["session_id"].as_str().unwrap_or_default().to_owned(),
            )
        })
        .collect()
}

fn listed(world: &World) -> Option<Vec<String>> {
    world
        .screen()
        .find(Target::role("list-box").name("Seeds this agent is tending"))?;
    Some(
        world
            .screen()
            .all(Target::role("list-box-option"))
            .into_iter()
            .filter_map(|n| n.name)
            .collect(),
    )
}

fn context(world: &World) -> Option<Vec<String>> {
    world
        .screen()
        .find(Target::role("dialog").name("Seed context"))?;
    Some(
        world
            .screen()
            .descendants(Target::key("tile.a.seed.popover"))
            .into_iter()
            .filter_map(|n| n.name)
            .collect(),
    )
}

fn says(world: &World, words: &str) -> bool {
    context(world).is_some_and(|said| said.iter().any(|w| w == words))
}

fn show_seeds(world: &mut World) {
    let ev = match Keymap::from_setting(None, KeyPlatform::current())
        .binding(&Action::UiCommandPalette)
    {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("the command palette has no single chord: {other:?}"),
    };
    world.gesture(|app| app.key(ev));
    world.gesture(|app| app.type_text("Show Agent a's seeds"));
    key(world, Key::Enter);
}

fn key(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

fn note(id: &str, kind: &str, body: &str, at: &str) -> SeedNote {
    SeedNote {
        id: id.into(),
        kind: kind.into(),
        body: body.into(),
        created_at: at.into(),
        author: ActorView {
            name: "the user".into(),
            r#ref: "user".into(),
        },
        ..SeedNote::default()
    }
}

fn answer(world: &mut World, request: &Request, seed: Seed, notes: Vec<SeedNote>) {
    world.daemon.reply_to(
        request,
        SeedDocumentGetResultMessage {
            request_id: request["request_id"].as_str().unwrap().into(),
            success: true,
            document: Some(SeedDocument {
                notes_total: notes.len() as i64,
                notes,
                seed,
                ..SeedDocument::default()
            }),
            ..SeedDocumentGetResultMessage::new()
        },
    );
    world.settle();
}

fn reads(world: &World) -> Vec<Request> {
    world.daemon.sent_of("seed_document_get")
}

fn rests(world: &mut World) -> bool {
    world.advance(Duration::from_secs(2));
    world.runtime_is_at_rest()
}

#[test]
fn an_agents_header_names_the_seed_its_crew_member_tends_and_opens_it_beside_the_agent() {
    let mut world = start(
        vec![claimed_for(
            seed("s-work11", "move the wire"),
            "fern",
            Some("s1"),
        )],
        agent(None),
    );
    assert_eq!(
        chip(&world).as_deref(),
        Some("move the wire, Growing. Open seed")
    );

    world.gesture(|app| app.click(Target::key("tile.a.seed")));
    assert_eq!(opened(&world), [("s-work11".into(), "s1".into())]);

    world.daemon.emit(GardenSeedsUpdatedMessage {
        profile_id: PROFILE.into(),
        seeds: vec![closed(seed("s-work11", "move the wire"), "harvested")],
        total: 1,
        ..GardenSeedsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(
        chip(&world),
        None,
        "a claim released leaves nothing to name"
    );
    assert!(rests(&mut world));
}

#[test]
fn an_agent_that_reports_to_a_seed_the_garden_does_not_hold_names_it_by_id_opens_it_and_drops_it_when_it_stops()
 {
    let mut world = start(Vec::new(), agent(Some("s-late11")));
    assert_eq!(
        chip(&world).as_deref(),
        Some("s-late11, Unknown. Open seed")
    );
    world.gesture(|app| app.click(Target::key("tile.a.seed")));
    assert_eq!(opened(&world), [("s-late11".into(), "s1".into())]);

    world.daemon.emit(SessionStateChangedMessage {
        session: agent(None),
        ..SessionStateChangedMessage::new()
    });
    world.settle();
    assert_eq!(chip(&world), None);
    assert!(rests(&mut world));
}

#[test]
fn a_plot_chip_counts_its_seeds_and_a_press_pins_their_list_until_a_second_press() {
    let mut world = start(
        vec![
            with(seed("s-plot11", "the arc"), |s| {
                s.plot_progress = Some(SeedPlotProgress {
                    done: 2,
                    total: 5,
                    ready: 1,
                    growing: 1,
                    blocked: 1,
                    ..SeedPlotProgress::default()
                })
            }),
            tended_by(part_of("s-plot11", "s-a", "first step"), "s1"),
            tended_by(part_of("s-plot11", "s-b", "second step"), "s1"),
        ],
        agent(None),
    );
    assert_eq!(
        chip(&world).as_deref(),
        Some("the arc, 2/5 harvested. Show seeds")
    );

    world.gesture(|app| app.click(Target::key("tile.a.seed")));
    assert_eq!(
        listed(&world).expect("a press pins the list"),
        [
            "the arc · 2/5",
            "first step · Growing",
            "second step · Growing"
        ]
    );
    let [x, y, w, h] = world
        .runtime()
        .presented("tile.a.seed")
        .expect("the chip is drawn")
        .0;
    world.gesture(|app| app.click_at((x + w / 2.0, y + h / 2.0)));
    assert_eq!(listed(&world), None);
    assert!(opened(&world).is_empty());
    assert!(rests(&mut world));
}

#[test]
fn the_palette_pins_an_agents_seeds_and_the_keyboard_opens_one() {
    let mut world = start(
        vec![
            tended_by(seed("s-a", "first"), "s1"),
            tended_by(seed("s-b", "second"), "s1"),
        ],
        agent(None),
    );
    assert_eq!(
        chip(&world).as_deref(),
        Some("tending 2, Growing. Show seeds")
    );

    show_seeds(&mut world);
    assert_eq!(
        listed(&world).expect("the command pins the list"),
        ["first · Growing", "second · Growing"]
    );
    let [x, y, _, _] = world
        .runtime()
        .presented("tile.a.seed.popover")
        .expect("the list is drawn")
        .0;
    world.gesture(|app| app.click_at((x + 2.0, y + 2.0)));
    assert!(listed(&world).is_some(), "a press inside keeps the list");
    world.gesture(|app| app.click_at((700.0, 600.0)));
    assert_eq!(listed(&world), None, "a press outside lets the list go");

    show_seeds(&mut world);
    key(&mut world, Key::Down);
    key(&mut world, Key::Enter);
    assert_eq!(opened(&world), [("s-b".into(), "s1".into())]);
    assert_eq!(listed(&world), None);
    assert!(rests(&mut world));
}

#[test]
fn the_list_names_the_seed_the_agent_reports_to_after_what_it_tends_and_once_when_it_tends_it() {
    for (reports_to, rows) in [
        (
            "s-crown1",
            vec!["first · Growing", "second · Growing", "the plan · Parked"],
        ),
        ("s-first", vec!["first · Growing", "second · Growing"]),
    ] {
        let mut world = start(
            vec![
                tended_by(seed("s-first", "first"), "s1"),
                tended_by(seed("s-second", "second"), "s1"),
                closed(seed("s-crown1", "the plan"), "parked"),
            ],
            agent(Some(reports_to)),
        );
        show_seeds(&mut world);
        assert_eq!(listed(&world).expect("the list is pinned"), rows);
        assert!(rests(&mut world));
    }
}

#[test]
fn the_chip_counts_only_seeds_tended_in_this_session_by_it_or_its_crew_member_and_finds_their_plot_even_through_a_cycle()
 {
    let cases = [
        (
            vec![
                tended_by(seed("s-mine", "Session work"), "s1"),
                tended_by(seed("s-other", "Other work"), "s-oak"),
                with(seed("s-idle", "Member work"), |s| {
                    s.tender = Some(PartyView {
                        name: "fern".into(),
                        r#ref: "member:fern".into(),
                        session_id: None,
                    })
                }),
            ],
            "Session work, Growing. Open seed",
        ),
        (
            vec![
                claimed_for(seed("s-member", "Member work"), "fern", Some("s1")),
                tended_by(seed("s-session", "Session work"), "s1"),
                claimed_for(seed("s-oak", "Other work"), "oak", Some("s-oak")),
            ],
            "tending 2, Growing. Show seeds",
        ),
        (
            vec![
                claimed_for(seed("s-member", "Member work"), "fern", Some("s1")),
                tended_by(seed("s-oldday", "Old day work"), "sess-old"),
            ],
            "Member work, Growing. Open seed",
        ),
        (
            vec![
                tended_by(seed("s-plot11", "the arc"), "s1"),
                tended_by(part_of("s-plot11", "s-a", "a"), "s1"),
            ],
            "the arc, Growing. Show seeds",
        ),
        (
            vec![
                tended_by(part_of("s-b", "s-a", "a"), "s1"),
                tended_by(part_of("s-a", "s-b", "b"), "s1"),
            ],
            "a, Growing. Show seeds",
        ),
    ];
    for (seeds, name) in cases {
        let mut world = start(seeds, agent(None));
        assert_eq!(chip(&world).as_deref(), Some(name));
        assert!(rests(&mut world));
    }
}

#[test]
fn the_chip_leaves_out_a_seed_title_the_agents_own_title_already_says() {
    let mut world = start(
        vec![tended_by(seed("s-work11", "Agent a's work"), "s1")],
        agent(None),
    );
    assert_eq!(
        chip(&world).as_deref(),
        Some("Agent a's work, Growing. Open seed")
    );
    assert!(
        world
            .screen()
            .find(Target::key("tile.a.seed.label"))
            .is_none()
    );
    assert!(rests(&mut world));
}

#[test]
fn a_seed_whose_tending_ends_stays_as_the_seed_the_agent_reports_to_with_its_new_state() {
    let work = tended_by(seed("s-work11", "Garden icons"), "s1");
    let mut world = start(vec![work.clone()], agent(Some("s-work11")));
    let released = with(work, |s| {
        s.status = "withered".into();
        s.tender = None;
        s.claimed = false;
    });
    world.daemon.emit(GardenSeedsUpdatedMessage {
        profile_id: PROFILE.into(),
        seeds: vec![released.clone()],
        total: 1,
        ..GardenSeedsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(
        chip(&world).as_deref(),
        Some("Garden icons, Withered. Open seed")
    );

    show_seeds(&mut world);
    let read = reads(&world)
        .pop()
        .expect("the open popover reads its seed");
    answer(&mut world, &read, released, Vec::new());
    assert!(says(&world, "Withered"));
    assert!(says(&world, "This agent reports to this seed."));
    assert!(says(&world, "No notes yet."));
    assert!(rests(&mut world));
}

#[test]
fn the_seed_context_reads_its_latest_note_only_while_open_and_leaves_out_activity() {
    let done = with(seed("s-work11", "Garden icons"), |s| {
        s.status = "harvested".into();
        s.reason = Some("All five states are legible.".into());
    });
    let mut world = start(vec![done.clone()], agent(Some("s-work11")));
    assert!(reads(&world).is_empty());

    show_seeds(&mut world);
    let read = reads(&world)
        .pop()
        .expect("the open popover reads its seed");
    assert!(says(&world, "Loading latest note…"));
    answer(
        &mut world,
        &read,
        done,
        vec![
            note(
                "n-1",
                "note",
                "Leaves look good at **header** size.",
                "2026-11-13T09:00:00Z",
            ),
            note(
                "n-2",
                "attach",
                "attached screenshot",
                "2099-01-01T00:00:00Z",
            ),
            note(
                "n-3",
                "move",
                "harvested: All five states are legible.",
                "2099-01-01T00:00:01Z",
            ),
            note("n-4", "link", "now blocks s-next11", "2099-01-01T00:00:02Z"),
        ],
    );
    assert!(says(&world, "Leaves look good at header size."));
    assert!(says(&world, "Outcome"));
    assert!(says(&world, "All five states are legible."));
    assert!(!says(&world, "attached screenshot"));
    assert!(!says(&world, "now blocks s-next11"));
    assert_eq!(reads(&world).len(), 1);

    key(&mut world, Key::Escape);
    assert_eq!(context(&world), None);
    assert!(rests(&mut world));
}

#[test]
fn an_open_context_follows_each_garden_push_and_drops_an_answer_a_later_read_replaced() {
    let value = tended_by(seed("s-work11", "Garden icons"), "s1");
    let mut world = start(vec![value.clone()], agent(None));
    show_seeds(&mut world);
    let early = reads(&world)
        .pop()
        .expect("the open popover reads its seed");

    let revised = with(value.clone(), |s| s.rev = 2);
    world.daemon.emit(GardenSeedsChangedMessage {
        profile_id: PROFILE.into(),
        order: vec![value.id.clone()],
        seeds: Vec::new(),
        total: 1,
        ..GardenSeedsChangedMessage::new()
    });
    world.settle();
    let late = reads(&world)
        .pop()
        .expect("a garden change that carries no seed, as a new note's, reads it again");
    assert_ne!(late["request_id"], early["request_id"]);
    answer(
        &mut world,
        &late,
        revised,
        vec![note(
            "n-2",
            "note",
            "Finished and verified.",
            "2026-11-13T10:00:00Z",
        )],
    );
    answer(
        &mut world,
        &early,
        value,
        vec![note(
            "n-1",
            "note",
            "Still exploring.",
            "2026-11-13T09:00:00Z",
        )],
    );
    assert!(says(&world, "Finished and verified."));
    assert!(!says(&world, "Still exploring."));
    assert!(rests(&mut world));
}

#[test]
fn a_failed_read_says_the_note_is_unavailable_and_return_still_opens_the_seed() {
    let mut world = start(
        vec![tended_by(seed("s-work11", "Garden icons"), "s1")],
        agent(None),
    );
    show_seeds(&mut world);
    let read = reads(&world)
        .pop()
        .expect("the open popover reads its seed");
    world.daemon.reply_to(
        &read,
        SeedDocumentGetResultMessage {
            request_id: read["request_id"].as_str().unwrap().into(),
            success: false,
            error: Some("offline".into()),
            ..SeedDocumentGetResultMessage::new()
        },
    );
    world.settle();
    assert!(says(&world, "Latest note unavailable."));

    key(&mut world, Key::Enter);
    assert_eq!(opened(&world), [("s-work11".into(), "s1".into())]);
    assert_eq!(context(&world), None);
    assert!(rests(&mut world));
}
