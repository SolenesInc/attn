use std::time::Duration;

use attn_protocol::{
    Ask, DesktopPane, DesktopTileContentMessage, ReviewBundle, ReviewRow, ReviewStop, Seed,
    Session, SessionLedgerEntry,
};
use attn_testkit::{Target, World, key_event, tree};
use rune::{Chord, Key};
use serde_json::{Value, json};

use super::asks::{ask, has, mentions_answered, open_sheet, prose, question, type_into};
use super::garden::document_reply;
use super::ledger::{hold as holds_back, ledger_daemon};
use super::terminal::{inputs, pane, showing};
use super::terminal_ids::{
    BUILDER, BUILDER_SHORT, MAPPER, MAPPER_SHORT, SEED, STRANGER, agent, asked, builder,
    is_at_rest, key, letter, mapped, mapper, resolves, rests, resumed, sessions_shown, shown,
    takes_keyboard, write,
};
use super::terminal_labels::{
    chord_mods, gone_at_once, hold, labels_in, lets_go, modifiers, press, recorded,
};
use crate::wire::{mac, whole_on_its_first_frame};

const TO_BUILDER: &str = "attn-session:3f9a2c4e-8b1d-4c6e-9a7f-2d5e8c1b4a6f";
const TO_MAPPER: &str = "attn-session:c5e81f07-4a36-4f8f-b2d9-7701aa5c9e3d";
const TO_SEED: &str = "attn-seed:s-7k3f9m";
const AGENT_CARD: &str = "words.agent-card";
const SEED_CARD: &str = "words.seed-card";

fn reads(world: &mut World, text: &str) {
    world.daemon.emit(DesktopTileContentMessage {
        desktop_id: "d1".into(),
        tile_id: "m".into(),
        tile_kind: "markdown".into(),
        path: "/work/plan.md".into(),
        content: text.into(),
        ..DesktopTileContentMessage::new()
    });
    world.settle();
}

fn document() -> Value {
    json!({
        "type": "tile",
        "tile_id": "m",
        "tile_kind": "markdown",
        "tile_params": json!({ "path": "/work/plan.md" }).to_string(),
    })
}

/// A window whose desktop shows agent `s1`'s terminal above `tile`, with the `asks` of agent
/// `s1`.
fn start_tile(
    (others, seeds, ledger): (Vec<Session>, Vec<Seed>, Vec<SessionLedgerEntry>),
    asks: Vec<Ask>,
    tile: Value,
) -> World {
    let mut sessions = vec![agent("s1", "Reader")];
    sessions.extend(others);
    let reader = DesktopPane {
        session_id: "s1".into(),
        ..pane("t1", "r1")
    };
    let mut state = showing(vec![reader], "daemon-1");
    let read = seeds.clone();
    state.desktops.as_mut().unwrap()[0].tree_json = tree::below(tree::pane("t1"), tile).to_string();
    state.sessions = Some(sessions);
    state.seeds_total = Some(seeds.len() as i64);
    state.seeds = Some(seeds);
    state.asks = Some(asks);
    let daemon = ledger_daemon(state, ledger.clone());
    daemon.on("seed_document_get", move |r| {
        document_reply(r, read.iter().find(|s| r["seed_id"] == *s.id).cloned())
    });
    let mut world = World::start_sized(daemon, (1400.0, 900.0));
    resolves(&world, ledger);
    world.open_desktop();
    world
}

fn start_with(
    parts: (Vec<Session>, Vec<Seed>, Vec<SessionLedgerEntry>),
    asks: Vec<Ask>,
    text: &str,
) -> World {
    let mut world = start_tile(parts, asks, document());
    reads(&mut world, text);
    world
}

fn start(text: &str) -> World {
    start_with(
        (vec![builder()], vec![mapped()], vec![mapper()]),
        vec![],
        text,
    )
}

fn links(world: &World, within: &str) -> Vec<(String, String)> {
    let parts = world.screen().descendants(Target::key(within));
    parts
        .into_iter()
        .filter(|n| n.role == "link")
        .map(|n| (n.name.unwrap_or_default(), n.key.unwrap_or_default()))
        .collect()
}

fn linked(world: &World) -> Vec<(String, String)> {
    links(world, "tile.m")
}

fn to(words: &str, href: &str) -> (String, String) {
    (words.to_owned(), href.to_owned())
}

fn click(world: &mut World, link: &str) {
    world.gesture(|app| app.click(Target::key(link)));
}

fn point_at(world: &mut World, link: &str) {
    let [x, y, w, h] = world.screen().bounds(Target::key(link)).unwrap();
    let at = ((x + w / 2.0), (y + h / 2.0));
    world.gesture(|app| app.runtime.pointer_move(at.0, at.1));
}

fn rest_on(world: &mut World, link: &str) {
    point_at(world, link);
    world.advance(Duration::from_millis(200));
}

fn said(world: &World, part: &str) -> Option<String> {
    let key = format!("{AGENT_CARD}.picked.{part}");
    world.screen().find(Target::key(key))?.name
}

fn opened_seeds(world: &World) -> Vec<String> {
    let sent = world.daemon.sent_of("open_seed");
    sent.iter()
        .map(|r| r["seed_id"].as_str().unwrap().to_owned())
        .collect()
}

#[test]
fn a_document_links_every_form_of_the_ids_attn_knows_and_leaves_the_rest_as_text() {
    let world = start(&format!(
        "Ask **{BUILDER_SHORT}**, {BUILDER} or 3f9a2c4e about {SEED}.\n\n\
         - then {MAPPER_SHORT},\n\n\
         not 0a1b2c3d, s-zzzzzz, x{SEED}, {SEED}x or é{BUILDER_SHORT}é.\n\n\
         `{BUILDER_SHORT}` [{SEED}](https://example.test)\n\n\
         ```\n{BUILDER_SHORT}\n```"
    ));
    assert_eq!(
        linked(&world),
        [
            to(BUILDER_SHORT, TO_BUILDER),
            to(BUILDER, TO_BUILDER),
            to("3f9a2c4e", TO_BUILDER),
            to(SEED, TO_SEED),
            to(MAPPER_SHORT, TO_MAPPER),
            to(BUILDER_SHORT, TO_BUILDER),
            to(SEED, "https://example.test"),
        ]
    );
}

#[test]
fn a_documents_unknown_session_ids_are_asked_about_once_with_a_terminals_and_link_once_found() {
    let mut world = start("Nothing yet");
    write(&mut world, 1, STRANGER);
    let asked_before = asked(&world);
    assert_eq!(asked_before, [[STRANGER]]);
    reads(
        &mut world,
        &format!("{MAPPER_SHORT} and {MAPPER_SHORT}, {STRANGER}, 9d2e4f61"),
    );
    assert_eq!(asked(&world)[1..], [[MAPPER_SHORT]]);
    assert_eq!(
        linked(&world),
        [to(MAPPER_SHORT, TO_MAPPER), to(MAPPER_SHORT, TO_MAPPER)]
    );
    reads(&mut world, &format!("# Again\n\n{STRANGER} and {MAPPER}"));
    assert_eq!(asked(&world).len(), 2);
    assert_eq!(linked(&world), [to(MAPPER, TO_MAPPER)]);
    rests(&mut world);
}

#[test]
fn a_press_on_a_documents_id_opens_a_live_agent_a_closed_ones_card_and_a_seed_as_a_tile() {
    let mut world = start(&format!("{BUILDER_SHORT} {MAPPER_SHORT} {SEED}"));
    click(&mut world, TO_BUILDER);
    assert_eq!(sessions_shown(&world), [BUILDER]);
    assert!(!shown(&world, AGENT_CARD));

    click(&mut world, TO_SEED);
    assert_eq!(opened_seeds(&world), [SEED]);

    click(&mut world, TO_MAPPER);
    assert_eq!(
        said(&world, "name").as_deref(),
        Some("Map where uploads retry")
    );
    key(&mut world, Key::Enter);
    let reopened = world.daemon.sent_of("session_reopen");
    assert_eq!(reopened.len(), 1);
    assert_eq!(reopened[0]["session_id"], MAPPER);
    assert_eq!(inputs(&world), []);
}

#[test]
fn a_closed_agents_card_hangs_from_the_place_pressed_of_an_id_a_document_names_again() {
    let mut world = start(&format!(
        "One {MAPPER_SHORT} here\n\n| Who |\n|---|\n| {MAPPER_SHORT} |\n\n> Ask {MAPPER_SHORT}"
    ));
    let named = world.runtime().elements().into_iter();
    let named: Vec<[f32; 4]> = named
        .filter(|e| e.key.as_deref() == Some(TO_MAPPER))
        .map(|e| e.rect)
        .collect();
    assert_eq!(named.len(), 3);
    for [x, y, w, h] in named {
        world.gesture(|app| {
            app.runtime.pointer_move(x + w / 2.0, y + h / 2.0);
            app.runtime.pointer_down(x + w / 2.0, y + h / 2.0);
            app.runtime.pointer_up(x + w / 2.0, y + h / 2.0);
        });
        let card = world.screen().bounds(Target::key(AGENT_CARD)).unwrap();
        assert!(card[1] >= y + h && card[1] < y + h + 20.0, "{y}: {card:?}");
        key(&mut world, Key::Escape);
        world.advance(Duration::from_secs(1));
    }
}

#[test]
fn a_documents_id_shows_its_card_under_a_resting_pointer_and_takes_the_keyboard() {
    let mut world = start(&format!("# Plan\n\n{BUILDER_SHORT} on {SEED}\n\nand more"));
    point_at(&mut world, TO_BUILDER);
    world.advance(Duration::from_millis(100));
    assert!(
        !shown(&world, AGENT_CARD),
        "the card waits for the pointer to rest"
    );
    world.advance(Duration::from_millis(100));
    assert_eq!(
        said(&world, "name").as_deref(),
        Some("Fix the retry uploader")
    );
    let id = world.screen().bounds(Target::key(TO_BUILDER)).unwrap();
    let card = world.screen().bounds(Target::key(AGENT_CARD)).unwrap();
    assert!(
        card[1] >= id[1] + id[3] && card[1] < id[1] + id[3] + 20.0,
        "the card hangs under the id at {id:?}: {card:?}"
    );

    rest_on(&mut world, TO_SEED);
    world.advance(Duration::from_millis(300));
    rest_on(&mut world, TO_SEED);
    assert!(shown(&world, SEED_CARD));
    takes_keyboard(&mut world);
    key(&mut world, Key::Enter);
    assert_eq!(opened_seeds(&world), [SEED]);
    assert!(!shown(&world, SEED_CARD));
    assert_eq!(inputs(&world), []);
    rests(&mut world);
}

#[test]
fn a_documents_card_goes_once_the_pointer_leaves_its_id_or_the_id_moves() {
    let text = format!("{BUILDER_SHORT}\n\n{}", "more\n\n".repeat(60));
    let mut world = start(&text);
    rest_on(&mut world, TO_BUILDER);
    assert!(shown(&world, AGENT_CARD));
    let [x, y, w, h] = world.screen().bounds(Target::key("tile.m")).unwrap();
    let away = ((x + w / 2.0), (y + h - 20.0));
    world.gesture(|app| app.runtime.pointer_move(away.0, away.1));
    assert!(shown(&world, AGENT_CARD), "the card waits a moment");
    world.advance(Duration::from_millis(300));
    assert!(!shown(&world, AGENT_CARD));

    rest_on(&mut world, TO_BUILDER);
    assert!(shown(&world, AGENT_CARD));
    reads(&mut world, &format!("# Moved\n\n{text}"));
    assert!(!shown(&world, AGENT_CARD), "its id is not where it hangs");
    rests(&mut world);
}

fn labels(world: &World, pane: &str, part: &str) -> Vec<String> {
    let within = Target::key(format!("{pane}.{part}"));
    if world.screen().find(within.clone()).is_none() {
        return Vec::new();
    }
    let prefix = format!("{pane}.label.");
    let nodes = world.screen().descendants(within).into_iter();
    let on_ids =
        nodes.filter(|n| part != "hints" || n.key.as_ref().is_some_and(|k| k.starts_with(&prefix)));
    labels_in(on_ids.collect())
}

const ON_BUILDER: &str = "S Fix the retry uploader, working";
const ON_MAPPER: &str = "D Map where uploads retry, closed";
const ON_SEED: &str = "F Map the uploader, harvested";

#[test]
fn the_chord_labels_the_ids_in_a_document_with_the_letters_the_terminal_gives_them() {
    let mut world = start(&format!("{SEED} for {BUILDER_SHORT} and {BUILDER}"));
    write(&mut world, 1, &format!("{BUILDER_SHORT} {MAPPER_SHORT}"));
    hold(&mut world);
    assert_eq!(
        labels(&world, "terminal.r1", "hints"),
        [ON_BUILDER, ON_MAPPER]
    );
    assert_eq!(
        labels(&world, "terminal.r1", "labels"),
        [ON_BUILDER, ON_MAPPER, ON_SEED],
        "the keyboard's terminal lists them all"
    );
    let in_words = [ON_SEED, ON_BUILDER, ON_BUILDER];
    assert_eq!(labels(&world, "words.1", "hints"), in_words);
    rests(&mut world);

    press(&mut world, 'f', false);
    lets_go(&mut world);
    assert!(shown(&world, SEED_CARD));
    key(&mut world, Key::Escape);
    assert!(!shown(&world, SEED_CARD));
    letter(&mut world, "x");
    assert_eq!(inputs(&world), [("x".to_owned(), "user".to_owned())]);
    rests(&mut world);
}

#[test]
fn the_labels_over_an_ask_and_the_list_show_and_go_in_one_step() {
    let mut world = asking();
    modifiers(&mut world, chord_mods());
    let rec = recorded(&mut world, 'l');
    for part in ["hints", "label.0", "labels"] {
        whole_on_its_first_frame(&rec, &format!("words.0.{part}"));
    }

    let rec = recorded(&mut world, 's');
    assert!(shown(&world, AGENT_CARD));
    gone_at_once(&rec, "words.0.hints");
}

#[test]
fn labels_follow_a_document_that_changes_and_go_when_the_chord_does() {
    let mut world = start(&format!("{BUILDER_SHORT} {SEED}"));
    hold(&mut world);
    assert_eq!(labels(&world, "words.1", "hints").len(), 2);
    reads(&mut world, &format!("# Moved\n\nOnly {SEED}"));
    assert_eq!(
        labels(&world, "words.1", "hints"),
        ["D Map the uploader, harvested"]
    );
    let [x, y, w, h] = world.screen().bounds(Target::key(TO_SEED)).unwrap();
    let elements = world.runtime().elements();
    let label = elements
        .iter()
        .find(|e| e.key.as_deref() == Some("words.1.label.0"));
    let [lx, ly, lw, lh] = label.unwrap().rect;
    assert!(
        lx < x + w && x < lx + lw && ly < y + h + lh && y < ly + 2.0 * lh,
        "the label is at its id"
    );
    lets_go(&mut world);
    assert!(world.screen().find(Target::key("words.1.hints")).is_none());
    rests(&mut world);
}

#[test]
fn a_seeds_body_links_the_agents_and_seeds_it_names_and_opens_them() {
    let plan = Seed {
        id: "s-plan11".into(),
        body: format!("{BUILDER_SHORT} follows {SEED}"),
        ..mapped()
    };
    let tile = json!({
        "type": "tile",
        "tile_id": "m",
        "tile_kind": "seed",
        "tile_params": "s-plan11",
    });
    let mut world = start_tile(
        (vec![builder()], vec![mapped(), plan], vec![]),
        vec![],
        tile,
    );
    let found = linked(&world);
    assert!(found.contains(&to(BUILDER_SHORT, TO_BUILDER)), "{found:?}");
    rest_on(&mut world, TO_BUILDER);
    assert!(shown(&world, AGENT_CARD));
    click(&mut world, TO_SEED);
    assert_eq!(opened_seeds(&world), [SEED]);
    click(&mut world, TO_BUILDER);
    assert_eq!(sessions_shown(&world), [BUILDER]);
}

#[test]
fn a_review_links_the_ids_in_its_prose_and_in_a_stop_on_a_markdown_file() {
    let mut world = start("");
    let kept = format!("Kept by {BUILDER_SHORT}.");
    let row = |kind: &str, old: Option<i64>, now: Option<i64>, text: &str| ReviewRow {
        kind: kind.into(),
        old,
        now,
        head: now,
        text: text.into(),
    };
    let rows = vec![
        row(" ", Some(1), Some(1), &kept),
        row(" ", Some(2), Some(2), ""),
        row("-", Some(3), None, "The map is made by one agent today."),
        row(
            "+",
            None,
            Some(3),
            &format!("The map is made by {MAPPER_SHORT}, today."),
        ),
    ];
    let stop = ReviewStop {
        heading_line: 8,
        path: "docs/guide.md".into(),
        from: 1,
        to: 3,
        now_from: 1,
        now_to: 3,
        file_hash: "h1".into(),
        rows,
        ..ReviewStop::default()
    };
    world.daemon.emit(DesktopTileContentMessage {
        desktop_id: "d1".into(),
        tile_id: "m".into(),
        tile_kind: "markdown".into(),
        path: "/work/plan.md".into(),
        content: format!(
            "---\nattn: review\nrepository: /repo\n---\n\nAbout {SEED}.\n\n### docs/guide.md:1-3\n"
        ),
        review: Some(ReviewBundle {
            frame: "brief".into(),
            repository: "/repo".into(),
            base: Some("4be1c0d".into()),
            head: "9f3a7e2".into(),
            stops: vec![stop],
            ..ReviewBundle::default()
        }),
        ..DesktopTileContentMessage::new()
    });
    world.settle();
    assert_eq!(
        linked(&world),
        [
            to(SEED, TO_SEED),
            to(BUILDER_SHORT, TO_BUILDER),
            to(MAPPER_SHORT, TO_MAPPER),
        ]
    );
    rest_on(&mut world, TO_BUILDER);
    assert_eq!(
        said(&world, "name").as_deref(),
        Some("Fix the retry uploader")
    );
    rest_on(&mut world, TO_MAPPER);
    world.advance(Duration::from_millis(300));
    rest_on(&mut world, TO_MAPPER);
    assert_eq!(
        said(&world, "name").as_deref(),
        Some("Map where uploads retry")
    );
    rests(&mut world);
}

#[test]
fn a_popped_out_documents_card_and_labels_are_in_its_own_window() {
    let mut popped = document();
    popped["window"] = json!({"x": 60.0, "y": 60.0, "width": 900.0, "height": 700.0});
    let parts = (vec![builder()], vec![mapped()], vec![]);
    let mut world = start_tile(parts, vec![], popped);
    reads(&mut world, &format!("{BUILDER_SHORT} on {SEED}"));
    let window = world.tile_windows()[0].key.clone();
    let [x, y, w, h] = world
        .tile_screen(&window)
        .bounds(Target::key(TO_BUILDER))
        .unwrap();
    world.tile_gesture(&window, |app| {
        app.runtime.pointer_move(x + w / 2.0, y + h / 2.0);
    });
    world.advance(Duration::from_millis(200));
    let card = world.tile_screen(&window).bounds(Target::key(AGENT_CARD));
    assert!(
        card.is_some_and(|card| card[1] >= y + h && card[1] < y + h + 20.0),
        "{card:?}"
    );
    assert!(!shown(&world, AGENT_CARD));
    assert!(is_at_rest(&mut world));

    world.tile_gesture(&window, |app| app.click(Target::key(TO_BUILDER)));
    assert_eq!(sessions_shown(&world), [BUILDER]);
    let card = world.tile_screen(&window).find(Target::key(AGENT_CARD));
    assert!(card.is_none());

    let l = Chord::parse(if mac() { "mod+l" } else { "mod+shift+l" }).unwrap();
    world.tile_gesture(&window, |app| {
        app.hold(chord_mods());
        app.key(key_event(&l));
    });
    let labelled = world
        .tile_screen(&window)
        .descendants(Target::key("words.0.hints"));
    let names = [ON_BUILDER, "D Map the uploader, harvested"];
    assert_eq!(labels_in(labelled)[..2], names);
    assert!(is_at_rest(&mut world));
}

fn asking() -> World {
    let asks = vec![ask(
        "a-1",
        "s1",
        &format!("Is {BUILDER_SHORT} done?"),
        30,
        vec![
            prose(&format!(
                "{BUILDER} took over {MAPPER_SHORT} on {SEED}; `{BUILDER_SHORT}`"
            )),
            question(
                "q1",
                "one",
                &format!("Keep {MAPPER_SHORT}?"),
                &["Yes", "No"],
            ),
            question("q2", "text", "Anything else?", &[]),
        ],
    )];
    let known = (vec![builder()], vec![mapped()], vec![mapper()]);
    let mut world = start_with(known, asks, "");
    mentions_answered(&world, &[(SEED, SEED)]);
    open_sheet(&mut world);
    world
}

const ANSWER: &str = "asks.q.q2.text.field";

#[test]
fn an_asks_sessions_are_links_beside_its_seeds_and_code_stays_as_written() {
    let world = asking();
    assert_eq!(
        links(&world, "asks.ask"),
        [
            to(BUILDER_SHORT, TO_BUILDER),
            to(BUILDER, TO_BUILDER),
            to(MAPPER_SHORT, TO_MAPPER),
            to(SEED, TO_SEED),
            to(MAPPER_SHORT, TO_MAPPER),
        ]
    );
}

#[test]
fn a_live_agent_opened_from_an_ask_shows_and_what_was_typed_and_picked_is_there_when_it_reopens() {
    let mut world = asking();
    type_into(&mut world, ANSWER, "half a thou");
    let choice = "asks.q.q1.choice.0";
    world.gesture(|app| app.click(Target::key(choice)));
    let link = links(&world, "asks.ask")[1].1.clone();
    click(&mut world, &link);
    assert!(!has(&world, "asks.ask"), "the sheet gives way to the agent");
    assert_eq!(sessions_shown(&world), [BUILDER]);
    assert_eq!(world.daemon.sent_of("ask_answer").len(), 0);

    open_sheet(&mut world);
    let field = world.screen().get(Target::key(ANSWER));
    assert_eq!(field.value.as_deref(), Some("half a thou"));
    assert!(super::asks::checked(&world, choice));
}

#[test]
fn an_asks_session_shows_its_card_over_the_sheet_and_a_closed_one_resumes_from_it() {
    let mut world = asking();
    rest_on(&mut world, TO_BUILDER);
    assert_eq!(
        said(&world, "name").as_deref(),
        Some("Fix the retry uploader")
    );

    rest_on(&mut world, TO_SEED);
    world.advance(Duration::from_secs(1));
    assert!(!shown(&world, AGENT_CARD));
    assert!(!shown(&world, SEED_CARD), "a seed keeps its tooltip");

    let held = holds_back(&world, "session_reopen");
    click(&mut world, TO_MAPPER);
    assert_eq!(
        said(&world, "name").as_deref(),
        Some("Map where uploads retry")
    );
    key(&mut world, Key::Escape);
    assert!(!shown(&world, AGENT_CARD));
    assert!(
        has(&world, "asks.ask"),
        "Escape on the card leaves the sheet"
    );

    click(&mut world, TO_MAPPER);
    key(&mut world, Key::Enter);
    let request = held.lock().unwrap()[0].clone();
    world.daemon.reply_to(&request, resumed(MAPPER));
    world.settle();
    assert_eq!(sessions_shown(&world), [MAPPER]);
    assert!(!shown(&world, AGENT_CARD));
    assert!(!has(&world, "asks.ask"), "the sheet gives way to the agent");
}

#[test]
fn the_chord_labels_an_asks_sessions_and_seeds_and_a_label_opens_the_card() {
    let mut world = asking();
    type_into(&mut world, ANSWER, "half a thou");
    hold(&mut world);
    let all = [ON_BUILDER, ON_BUILDER, ON_MAPPER, ON_SEED, ON_MAPPER];
    assert_eq!(labels(&world, "words.0", "hints"), all);
    assert_eq!(
        labels(&world, "words.0", "labels"),
        [ON_BUILDER, ON_MAPPER, ON_SEED]
    );
    press(&mut world, 'f', false);
    lets_go(&mut world);
    assert!(shown(&world, SEED_CARD));
    key(&mut world, Key::Escape);
    assert!(!shown(&world, SEED_CARD));
    letter(&mut world, "x");
    let field = world.screen().get(Target::key(ANSWER));
    assert_eq!(field.value.as_deref(), Some("half a thoux"));

    hold(&mut world);
    press(&mut world, 'f', false);
    lets_go(&mut world);
    key(&mut world, Key::Enter);
    assert_eq!(opened_seeds(&world), [SEED]);
    assert!(!has(&world, "asks.ask"), "the sheet gives way to the seed");
}
