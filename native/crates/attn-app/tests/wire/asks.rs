use std::collections::BTreeMap;
use std::time::Duration;

use attn_app::Action;
use attn_protocol::{
    Ask, AskAnswer, AskAnswerResultMessage, AskChoice, AskPart, AskQuestion, AskReply,
    AsksUpdatedMessage, InitialStateMessage, Session, SessionStateChangedMessage,
};
use attn_protocol::{PartyView, Seed, SeedDocument, SeedDocumentGetResultMessage};
use attn_testkit::{Daemon, Target, World, message};
use rune::{Key, KeyEvent, Mods};

use super::queue::{ago, one_desktop};
use super::sidebar::{chord, desktop, pane, session, state};

fn choice(label: &str) -> AskChoice {
    AskChoice {
        label: label.into(),
        ..AskChoice::default()
    }
}

fn question(key: &str, kind: &str, prompt: &str, choices: &[&str]) -> AskPart {
    AskPart {
        question: Some(AskQuestion {
            key: key.into(),
            kind: kind.into(),
            prompt: prompt.into(),
            choices: choices.iter().map(|c| choice(c)).collect(),
            ..AskQuestion::default()
        }),
        ..AskPart::default()
    }
}

fn recommending(mut part: AskPart, recommended: &str) -> AskPart {
    part.question.as_mut().unwrap().recommended = Some(recommended.into());
    part
}

fn prose(markdown: &str) -> AskPart {
    AskPart {
        markdown: Some(markdown.into()),
        ..AskPart::default()
    }
}

fn ask(id: &str, session: &str, subject: &str, minutes_ago: i64, parts: Vec<AskPart>) -> Ask {
    Ask {
        id: id.into(),
        session_id: session.into(),
        subject: subject.into(),
        base_dir: format!("/tmp/{session}"),
        created_at: ago(minutes_ago),
        parts,
        ..Ask::default()
    }
}

fn cache_store() -> Ask {
    let mut store = question("q1", "one", "Which store should keep the cache?", &[]);
    store.question.as_mut().unwrap().choices = vec![
        AskChoice {
            label: "Postgres".into(),
            description: Some("already deployed".into()),
        },
        choice("Redis"),
    ];
    ask(
        "a-cache1",
        "api",
        "Cache store",
        30,
        vec![
            prose("Deploys empty the cache."),
            recommending(store, "Postgres"),
            question("q2", "text", "What should the banner say?", &[]),
        ],
    )
}

fn release_day() -> Ask {
    ask(
        "a-release",
        "web",
        "Release day",
        25,
        vec![question(
            "q1",
            "any",
            "Which platforms?",
            &["macOS", "Linux"],
        )],
    )
}

fn rollout() -> Ask {
    ask(
        "a-rollout",
        "api",
        "Rollout",
        28,
        vec![
            recommending(
                question("q1", "one", "Which region first?", &["eu", "us"]),
                "eu",
            ),
            recommending(
                question("q2", "any", "Which checks?", &["smoke", "load"]),
                "smoke",
            ),
            recommending(question("q3", "text", "Which date?", &[]), "Monday"),
            question("q4", "one", "Who approves?", &["Ana", "Bo"]),
        ],
    )
}

fn replied(mut ask: Ask, answers: Vec<AskAnswer>, note: Option<&str>) -> Ask {
    ask.reply = Some(AskReply {
        answers,
        note: note.map(str::to_owned),
        answered_at: ago(1),
    });
    ask
}

fn picked(key: &str, labels: &[&str]) -> AskAnswer {
    AskAnswer {
        key: key.into(),
        picked: labels.iter().map(|l| (*l).to_owned()).collect(),
        ..AskAnswer::default()
    }
}

fn written(key: &str, text: &str) -> AskAnswer {
    AskAnswer {
        key: key.into(),
        text: Some(text.into()),
        ..AskAnswer::default()
    }
}

fn api_working() -> Session {
    session("api", "working")
}

fn web_waiting() -> Session {
    Session {
        turn_owed: Some(true),
        turn_opened_at: Some(ago(20)),
        ..session("web", "waiting_input")
    }
}

fn start(asks: Vec<Ask>, sessions: Vec<Session>) -> World {
    start_with(asks, sessions, &[])
}

fn start_with(asks: Vec<Ask>, sessions: Vec<Session>, settings: &[(&str, &str)]) -> World {
    start_on(one_desktop(sessions), asks, settings)
}

fn start_on(mut state: InitialStateMessage, asks: Vec<Ask>, settings: &[(&str, &str)]) -> World {
    state.asks = Some(asks);
    state.settings = Some(
        settings
            .iter()
            .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
            .collect::<BTreeMap<_, _>>(),
    );
    let daemon = super::palette::daemon(state, vec![]);
    daemon.on("ask_answer", |r| {
        message(AskAnswerResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            ask_id: r["ask_id"].as_str().unwrap().into(),
            success: true,
            ..AskAnswerResultMessage::new()
        })
    });
    World::start(daemon)
}

fn open_drawer(world: &mut World) {
    world.gesture(|app| app.key(chord(Action::DockAsks)));
}

fn shown(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("asks.ask"))
        .and_then(|n| n.name)
}

fn rows(world: &World) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key("asks.list"))
        .into_iter()
        .filter_map(|n| n.key?.strip_prefix("asks.row.").map(str::to_owned))
        .collect()
}

fn focused(world: &World) -> Option<String> {
    world.screen().focused().and_then(|n| n.key)
}

fn letter(world: &mut World, c: &str) {
    let c = c.to_owned();
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Text(c),
            mods: Mods::default(),
        })
    });
}

fn plain(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

fn send_key(world: &mut World) {
    world.gesture(|app| app.press("mod+enter"));
}

fn click(world: &mut World, key: &str) {
    let key = key.to_owned();
    world.gesture(|app| app.click(Target::key(key)));
}

fn type_into(world: &mut World, key: &str, text: &str) {
    click(world, key);
    let text = text.to_owned();
    world.gesture(|app| app.type_text(&text));
}

fn checked(world: &World, key: &str) -> bool {
    world
        .screen()
        .get(Target::key(key))
        .states
        .iter()
        .any(|s| s == "checked")
}

fn disabled(world: &World, key: &str) -> bool {
    world
        .screen()
        .get(Target::key(key))
        .states
        .iter()
        .any(|s| s == "disabled")
}

fn has(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

fn name(world: &World, key: &str) -> String {
    world
        .screen()
        .get(Target::key(key))
        .name
        .unwrap_or_default()
}

fn update_asks(world: &mut World, asks: Vec<Ask>) {
    world.daemon.emit(AsksUpdatedMessage {
        profile_id: "work".into(),
        asks,
        ..AsksUpdatedMessage::new()
    });
    world.settle();
}

fn changes(world: &mut World, session: Session) {
    world.daemon.emit(SessionStateChangedMessage {
        session,
        ..SessionStateChangedMessage::new()
    });
    world.settle();
}

fn sent_answers(world: &World) -> Vec<serde_json::Value> {
    world
        .daemon
        .sent_of("ask_answer")
        .into_iter()
        .map(|r| {
            let mut r = r.clone();
            let object = r.as_object_mut().unwrap();
            object.remove("request_id");
            object.remove("cmd");
            r
        })
        .collect()
}

#[test]
fn the_drawer_opens_on_the_ask_of_the_agent_waiting_on_you_first_with_the_keyboard_in_its_first_answer()
 {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );
    open_drawer(&mut world);

    assert_eq!(rows(&world), ["a-release", "a-cache1"]);
    assert_eq!(shown(&world).as_deref(), Some("Release day"));
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q1.choice.0"));

    letter(&mut world, "j");
    assert_eq!(shown(&world).as_deref(), Some("Cache store"));
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q1.choice.0"));
    assert_eq!(name(&world, "asks.q.q1.choice.0"), "Postgres");
    rest_but_for_ages(&mut world);
}

#[test]
fn the_drawer_keeps_the_shown_ask_and_focus_when_another_agent_starts_waiting() {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), session("web", "working")],
    );
    open_drawer(&mut world);
    assert_eq!(shown(&world).as_deref(), Some("Cache store"));

    changes(&mut world, web_waiting());

    assert_eq!(rows(&world), ["a-release", "a-cache1"]);
    assert_eq!(shown(&world).as_deref(), Some("Cache store"));
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q1.choice.0"));
}

#[test]
fn the_drawers_letter_keys_work_after_a_click_on_the_ask_itself() {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );
    open_drawer(&mut world);
    click(&mut world, "asks.row.a-cache1");
    click(&mut world, "asks.part.0");

    letter(&mut world, "k");

    assert_eq!(shown(&world).as_deref(), Some("Release day"));
}

/// At rest but for the next minute an age in the open drawer reads differently.
fn rest_but_for_ages(world: &mut World) {
    world.advance(Duration::from_secs(1));
    let now = world.now().as_secs_f64();
    let wake = world.runtime().wake_deadline();
    assert!(wake.is_some_and(|at| at - now >= 20.0), "{wake:?} at {now}");
}

fn typed_into(world: &World) -> Vec<String> {
    world
        .daemon
        .sent_of("pty_input")
        .iter()
        .map(|m| m["id"].as_str().unwrap_or_default().to_owned())
        .collect()
}

#[test]
fn escape_closes_the_drawer_and_gives_the_keyboard_back_to_the_agent() {
    let mut state = one_desktop(vec![api_working(), web_waiting()]);
    let desktops = state.desktops.as_mut().expect("one desktop");
    for pane in desktops.iter_mut().flat_map(|d| &mut d.panes) {
        pane.runtime_id = format!("r-{}", pane.session_id);
    }
    desktops[0].active_pane_id = "p0".into();
    let mut world = start_on(state, vec![cache_store(), release_day()], &[]);
    world.open_desktop();
    world.gesture(|app| app.type_text("x"));
    assert_eq!(typed_into(&world), ["r-api"]);

    open_drawer(&mut world);
    plain(&mut world, Key::Escape);
    world.gesture(|app| app.type_text("x"));

    assert!(!has(&world, "asks.drawer"));
    assert_eq!(typed_into(&world), ["r-api", "r-api"]);
}

#[test]
fn sending_sends_the_picked_answers_and_the_note_then_brings_the_next_ask() {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );
    open_drawer(&mut world);
    click(&mut world, "asks.row.a-cache1");

    click(&mut world, "asks.q.q1.choice.0");
    type_into(&mut world, "asks.q.q2.text.field", "Welcome back");
    type_into(&mut world, "asks.note.field", "Keep it boring.");
    click(&mut world, "asks.send");
    update_asks(
        &mut world,
        vec![
            release_day(),
            replied(
                cache_store(),
                vec![picked("q1", &["Postgres"]), written("q2", "Welcome back")],
                Some("Keep it boring."),
            ),
        ],
    );

    assert_eq!(
        sent_answers(&world),
        [serde_json::json!({
            "ask_id": "a-cache1",
            "answers": [
                {"key": "q1", "picked": ["Postgres"]},
                {"key": "q2", "picked": [], "text": "Welcome back"},
            ],
            "note": "Keep it boring.",
        })]
    );
    assert_eq!(shown(&world).as_deref(), Some("Release day"));
    rest_but_for_ages(&mut world);
}

#[test]
fn a_refused_answer_keeps_what_was_written_and_says_why() {
    let mut world = start(vec![release_day()], vec![api_working(), web_waiting()]);
    world.daemon.on("ask_answer", |r| {
        message(AskAnswerResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            ask_id: r["ask_id"].as_str().unwrap().into(),
            success: false,
            error: Some("this ask was already answered or withdrawn".into()),
            ..AskAnswerResultMessage::new()
        })
    });
    open_drawer(&mut world);

    click(&mut world, "asks.q.q1.choice.1");
    type_into(&mut world, "asks.q.q1.other.field", "FreeBSD");
    click(&mut world, "asks.note.field");
    send_key(&mut world);

    assert_eq!(
        name(&world, "asks.error"),
        "this ask was already answered or withdrawn"
    );
    assert!(checked(&world, "asks.q.q1.choice.1"));
    assert_eq!(
        sent_answers(&world)[0]["answers"],
        serde_json::json!([{"key": "q1", "picked": ["Linux"], "other": "FreeBSD"}])
    );
}

#[test]
fn accept_all_recommended_picks_each_recommendation_and_leaves_the_rest_unanswered() {
    let mut world = start(vec![rollout()], vec![api_working()]);
    open_drawer(&mut world);

    click(&mut world, "asks.accept");
    assert!(checked(&world, "asks.q.q1.choice.0"));
    assert!(checked(&world, "asks.q.q2.choice.0"));
    assert!(!checked(&world, "asks.q.q4.choice.0"));

    click(&mut world, "asks.send");
    assert_eq!(
        sent_answers(&world)[0]["answers"],
        serde_json::json!([
            {"key": "q1", "picked": ["eu"]},
            {"key": "q2", "picked": ["smoke"]},
            {"key": "q3", "picked": [], "text": "Monday"},
        ])
    );
}

#[test]
fn the_a_key_accepts_every_recommendation_outside_a_text_field() {
    let mut world = start(vec![rollout()], vec![api_working()]);
    open_drawer(&mut world);

    letter(&mut world, "a");

    assert!(checked(&world, "asks.q.q1.choice.0"));
    assert!(checked(&world, "asks.q.q2.choice.0"));
}

#[test]
fn accept_all_recommended_shows_only_when_several_questions_recommend_one() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    open_drawer(&mut world);

    assert!(!has(&world, "asks.accept"));
}

#[test]
fn nothing_is_sent_until_a_question_is_answered_or_a_note_written() {
    let mut world = start(vec![release_day()], vec![web_waiting()]);
    open_drawer(&mut world);

    assert!(disabled(&world, "asks.send"));
    click(&mut world, "asks.note.field");
    send_key(&mut world);

    assert!(world.daemon.sent_of("ask_answer").is_empty());
}

#[test]
fn the_asking_agents_rows_are_marked_and_open_agent_shows_the_agent_and_closes_the_drawer() {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );
    world.open_desktop();

    assert!(has(&world, "sidebar.session.api.ask"));
    assert!(has(&world, "sidebar.session.web.ask"));
    open_drawer(&mut world);
    click(&mut world, "asks.open-agent");

    let shows = world.daemon.sent_of("desktop_show_session");
    assert_eq!(shows.last().unwrap()["session_id"], "web");
    assert!(!has(&world, "asks.drawer"));
}

fn landing() -> Ask {
    Ask {
        seed_id: Some("s-cache1".into()),
        seed_title: Some("Cache store migration".into()),
        seed_slug: Some("cache-migration".into()),
        ..ask(
            "a-landing",
            "delegate",
            "Landing week",
            20,
            vec![question(
                "q1",
                "text",
                "Which week should the migration land?",
                &[],
            )],
        )
    }
}

fn migration(tender: Option<&str>) -> Seed {
    let mut seed = super::garden::seed("s-cache1", "Cache store migration");
    seed.tender = tender.map(|id| PartyView {
        name: id.into(),
        r#ref: format!("session:{id}"),
        session_id: Some(id.into()),
    });
    seed
}

fn start_with_seeds(asks: Vec<Ask>, sessions: Vec<Session>, seeds: Vec<Seed>) -> World {
    let mut state = one_desktop(sessions);
    state.seeds = Some(seeds);
    let world = start_on(state, asks, &[]);
    world.daemon.on("seed_document_get", |r| {
        message(SeedDocumentGetResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            document: Some(SeedDocument {
                seed: migration(Some("successor")),
                ..SeedDocument::default()
            }),
            ..SeedDocumentGetResultMessage::new()
        })
    });
    world
}

fn seed_words(world: &World) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key("asks.seed"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

#[test]
fn an_ask_about_a_seed_waits_on_the_agent_whose_stake_it_is_and_names_the_seed() {
    let successor = Session {
        seed_id: Some("s-cache1".into()),
        ..session("successor", "working")
    };
    let mut world = start(
        vec![cache_store(), landing()],
        vec![api_working(), successor],
    );
    world.open_desktop();

    assert!(has(&world, "sidebar.session.successor.ask"));
    open_drawer(&mut world);
    letter(&mut world, "j");
    assert_eq!(shown(&world).as_deref(), Some("Landing week"));
    assert_eq!(name(&world, "asks.from"), "successor");
    let seed = seed_words(&world);
    assert!(seed.contains(&"cache-migration".to_owned()));
    assert!(seed.contains(&"Cache store migration".to_owned()));
    assert!(!has(&world, "asks.open-seed"));

    click(&mut world, "asks.open-agent");
    let shows = world.daemon.sent_of("desktop_show_session");
    assert_eq!(shows.last().unwrap()["session_id"], "successor");
}

#[test]
fn an_ask_about_a_seed_waits_on_whoever_the_garden_says_tends_it() {
    let delegate = Session {
        seed_id: Some("s-cache1".into()),
        ..session("delegate", "working")
    };
    let mut world = start_with_seeds(
        vec![landing()],
        vec![delegate, session("successor", "working")],
        vec![migration(Some("successor"))],
    );
    world.open_desktop();

    assert!(has(&world, "sidebar.session.successor.ask"));
    assert!(!has(&world, "sidebar.session.delegate.ask"));
    open_drawer(&mut world);
    assert_eq!(name(&world, "asks.from"), "successor");
}

#[test]
fn an_ask_about_a_seed_nobody_live_tends_waits_for_its_next_tender() {
    let mut world = start_with_seeds(
        vec![landing()],
        vec![api_working()],
        vec![migration(Some("delegate"))],
    );
    world.open_desktop();

    assert!(!has(&world, "sidebar.session.api.ask"));
    open_drawer(&mut world);
    assert_eq!(
        name(&world, "asks.from"),
        "waiting for the next tender of Cache store migration"
    );
    assert!(!has(&world, "asks.open-agent"));
    assert!(!has(&world, "asks.snooze"));
}

#[test]
fn open_in_garden_shows_the_seed_beside_the_drawer_and_escape_returns_to_the_ask() {
    let mut world = start_with_seeds(
        vec![landing()],
        vec![session("successor", "working")],
        vec![migration(Some("successor"))],
    );
    open_drawer(&mut world);

    click(&mut world, "asks.open-seed");
    assert_eq!(
        garden_heading(&world).as_deref(),
        Some("Cache store migration")
    );
    let read = world.daemon.sent_of("seed_document_get");
    assert_eq!(read.last().unwrap()["seed_id"], "s-cache1");
    assert_eq!(shown(&world).as_deref(), Some("Landing week"));

    plain(&mut world, Key::Escape);
    assert_eq!(garden_heading(&world), None);
    assert_eq!(shown(&world).as_deref(), Some("Landing week"));
    assert_eq!(focused(&world).as_deref(), Some("asks.drawer"));

    letter(&mut world, "g");
    assert_eq!(
        garden_heading(&world).as_deref(),
        Some("Cache store migration")
    );
}

#[test]
fn escape_closes_the_newest_drawer_whatever_holds_the_keyboard() {
    let mut world = start_with_seeds(
        vec![landing()],
        vec![session("successor", "working")],
        vec![migration(Some("successor"))],
    );
    world.gesture(|app| app.key(chord(Action::BoardOpen)));
    open_drawer(&mut world);
    click(&mut world, "asks.from");
    letter(&mut world, "g");
    assert_eq!(
        garden_heading(&world).as_deref(),
        Some("Cache store migration")
    );
    assert!(focused(&world).is_some_and(|k| k.starts_with("garden.")));

    plain(&mut world, Key::Escape);
    assert!(!has(&world, "asks.drawer"));
    assert_eq!(
        garden_heading(&world).as_deref(),
        Some("Cache store migration")
    );

    plain(&mut world, Key::Escape);
    assert_eq!(garden_heading(&world), None);
}

fn garden_heading(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("garden.head.title"))
        .and_then(|n| n.name)
}

#[test]
fn a_snoozed_agents_ask_waits_under_snoozed_out_of_the_counts_until_the_agent_wakes() {
    let api_snoozed = Session {
        turn_snoozed_until: Some("2999-01-04T09:00:00Z".into()),
        ..api_working()
    };
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_snoozed, web_waiting()],
    );
    assert!(name(&world, "titlebar.asks").starts_with("1 open ask ("));

    open_drawer(&mut world);
    assert_eq!(name(&world, "asks.count"), "1 open");
    assert_eq!(rows(&world), ["a-release", "a-cache1"]);
    assert!(
        world
            .screen()
            .descendants(Target::key("asks.list"))
            .iter()
            .any(|n| n.name.as_deref() == Some("Snoozed"))
    );
    assert!(name(&world, "asks.row.a-cache1").contains("until "));

    changes(&mut world, api_working());
    assert!(name(&world, "titlebar.asks").starts_with("2 open asks ("));
    assert_eq!(name(&world, "asks.count"), "2 open");
    assert!(!name(&world, "asks.row.a-cache1").contains("until "));
}

#[test]
fn a_hidden_agent_holds_its_ask_so_its_snooze_takes_the_ask_out_of_the_counts() {
    let hidden = Session {
        hidden: Some(true),
        turn_snoozed_until: Some("2999-01-04T09:00:00Z".into()),
        ..api_working()
    };
    let mut state = one_desktop(vec![web_waiting()]);
    state.sessions.as_mut().unwrap().push(hidden);
    state.asks = Some(vec![cache_store(), release_day()]);
    let mut world = World::start(Daemon::scripted().state(state));
    assert!(name(&world, "titlebar.asks").starts_with("1 open ask ("));

    open_drawer(&mut world);
    assert_eq!(rows(&world), ["a-release", "a-cache1"]);
    assert!(name(&world, "asks.row.a-cache1").starts_with("api"));
    click(&mut world, "asks.row.a-cache1");
    assert_eq!(name(&world, "asks.snooze"), "Wake");
}

#[test]
fn the_drawer_snoozes_the_asking_agent_and_wakes_it_again() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    open_drawer(&mut world);

    letter(&mut world, "s");
    assert_eq!(
        world.screen().get(Target::role("menu")).name.as_deref(),
        Some("Snooze api")
    );
    let first = focused(&world).expect("a snooze choice takes focus");
    click(&mut world, &first);
    let sent = world.daemon.sent_of("snooze_turn");
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["session_id"], "api");
    assert_eq!(focused(&world).as_deref(), Some("asks.snooze"));

    let until = sent[0]["until"].as_str().unwrap().to_owned();
    changes(
        &mut world,
        Session {
            turn_snoozed_until: Some(until),
            ..api_working()
        },
    );
    assert_eq!(name(&world, "asks.snooze"), "Wake");

    letter(&mut world, "s");
    let woke = world.daemon.sent_of("wake_turn");
    assert_eq!(woke.len(), 1);
    assert_eq!(woke[0]["session_id"], "api");
}

fn middle(world: &World, key: &str) -> (f32, f32) {
    let [x, y, w, h] = world
        .runtime()
        .presented(key)
        .unwrap_or_else(|| panic!("{key} is drawn"))
        .0;
    (x + w / 2.0, y + h / 2.0)
}

fn rest_on(world: &mut World, key: &str) {
    let at = middle(world, key);
    world.gesture(|app| app.move_to(at));
}

#[test]
fn an_agents_open_ask_shows_in_its_tile_header_previews_on_rest_and_opens_in_the_drawer() {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );
    world.open_desktop();

    assert_eq!(name(&world, "tile.p0.ask"), "Open ask: Cache store");
    rest_on(&mut world, "tile.p0.ask");
    world.advance(Duration::from_millis(160));
    let preview: Vec<String> = world
        .screen()
        .descendants(Target::key("tile.p0.ask.popover"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(preview.contains(&"Which store should keep the cache?".to_owned()));
    assert!(preview.contains(&"Written answer".to_owned()));

    click(&mut world, "asks.preview.a-cache1.answer");

    assert_eq!(shown(&world).as_deref(), Some("Cache store"));
    assert!(!has(&world, "tile.p0.ask.popover"));
}

#[test]
fn the_preview_stays_closed_when_the_agent_asks_again_after_its_last_ask_closed_under_the_pointer()
{
    let mut world = start(vec![cache_store()], vec![api_working()]);
    world.open_desktop();
    rest_on(&mut world, "tile.p0.ask");
    world.advance(Duration::from_millis(160));
    assert!(has(&world, "tile.p0.ask.popover"));

    update_asks(&mut world, vec![]);
    let mut follow_up = cache_store();
    follow_up.id = "a-follow-up".into();
    follow_up.subject = "Cache size".into();
    update_asks(&mut world, vec![follow_up]);
    world.advance(Duration::from_secs(1));

    assert_eq!(name(&world, "tile.p0.ask"), "Open ask: Cache size");
    assert!(!has(&world, "tile.p0.ask.popover"));
}

#[test]
fn a_press_on_the_ask_chip_takes_the_keyboard_even_when_the_drawer_shows_that_ask() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    world.open_desktop();
    click(&mut world, "tile.p0.ask");
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q1.choice.0"));

    world.gesture(|app| app.click(Target::key("tile.p0")));
    assert_ne!(focused(&world).as_deref(), Some("asks.q.q1.choice.0"));
    click(&mut world, "tile.p0.ask");

    assert_eq!(shown(&world).as_deref(), Some("Cache store"));
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q1.choice.0"));
}

#[test]
fn replied_asks_stay_out_of_the_way_until_asked_for_and_show_their_answers() {
    let answered = replied(
        cache_store(),
        vec![picked("q1", &["Postgres"]), written("q2", "Welcome back")],
        Some("Keep it boring."),
    );
    let mut world = start(
        vec![answered, release_day()],
        vec![api_working(), web_waiting()],
    );
    world.open_desktop();
    assert!(!has(&world, "sidebar.session.api.ask"));
    open_drawer(&mut world);
    assert_eq!(rows(&world), ["a-release"]);
    assert!(!checked(&world, "asks.replied"));

    letter(&mut world, "r");
    assert!(checked(&world, "asks.replied"));
    assert_eq!(
        world.screen().get(Target::key("asks.list")).name.as_deref(),
        Some("Open and replied asks")
    );
    assert_eq!(rows(&world), ["a-release", "a-cache1"]);
    plain(&mut world, Key::Text("j".into()));

    assert_eq!(shown(&world).as_deref(), Some("Cache store"));
    assert!(checked(&world, "asks.q.q1.choice.0"));
    assert!(disabled(&world, "asks.q.q1.choice.0"));
    assert_eq!(
        world
            .screen()
            .get(Target::key("asks.q.q2.text"))
            .value
            .as_deref(),
        Some("Welcome back")
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("asks.note"))
            .value
            .as_deref(),
        Some("Keep it boring.")
    );
    assert!(!has(&world, "asks.send"));
    assert_eq!(name(&world, "asks.replied-when"), "Replied 1m ago");

    letter(&mut world, "k");
    assert_eq!(shown(&world).as_deref(), Some("Release day"));
    click(&mut world, "asks.replied");
    assert_eq!(rows(&world), ["a-release"]);
}

#[test]
fn the_answer_just_sent_shows_when_it_was_the_last_open_ask_and_replied_asks_show() {
    let mut world = start(vec![release_day()], vec![web_waiting()]);
    open_drawer(&mut world);
    letter(&mut world, "r");

    click(&mut world, "asks.q.q1.choice.1");
    click(&mut world, "asks.send");
    update_asks(
        &mut world,
        vec![replied(release_day(), vec![picked("q1", &["Linux"])], None)],
    );

    assert_eq!(shown(&world).as_deref(), Some("Release day"));
    assert!(checked(&world, "asks.q.q1.choice.1"));
    assert!(disabled(&world, "asks.q.q1.choice.1"));
    assert_eq!(focused(&world).as_deref(), Some("asks.drawer"));

    letter(&mut world, "r");
    assert!(has(&world, "asks.empty"));
    let empty: Vec<String> = world
        .screen()
        .descendants(Target::key("asks.empty"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(empty.contains(&"No open asks.".to_owned()));
    assert!(empty.contains(&"r shows the one you replied to in the last day".to_owned()));
}

#[test]
fn the_drawer_takes_the_keyboard_with_no_open_asks_so_r_shows_the_replies_instead_of_reaching_the_agent()
 {
    let answered = replied(cache_store(), vec![picked("q1", &["Postgres"])], None);
    let mut world = start(vec![answered], vec![api_working()]);
    world.open_desktop();
    open_drawer(&mut world);
    assert!(has(&world, "asks.empty"));

    letter(&mut world, "r");

    assert_eq!(shown(&world).as_deref(), Some("Cache store"));
}

#[test]
fn the_asks_follow_the_selected_profile_and_close_when_the_daemon_closes_them() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    world.open_desktop();
    world.daemon.emit(AsksUpdatedMessage {
        profile_id: "another-profile".into(),
        asks: vec![],
        ..AsksUpdatedMessage::new()
    });
    world.settle();
    assert!(has(&world, "sidebar.session.api.ask"));

    update_asks(&mut world, vec![]);

    assert!(!has(&world, "sidebar.session.api.ask"));
    assert!(!has(&world, "tile.p0.ask"));
}

#[test]
fn the_asks_button_and_its_shortcut_exist_only_while_asks_are_on_or_some_ask_exists() {
    let mut world = start_with(vec![], vec![api_working()], &[("asks_enabled", "false")]);
    assert!(!has(&world, "titlebar.asks"));
    open_drawer(&mut world);
    assert!(!has(&world, "asks.drawer"));

    update_asks(&mut world, vec![cache_store()]);
    assert!(has(&world, "titlebar.asks"));
    click(&mut world, "titlebar.asks");
    assert_eq!(shown(&world).as_deref(), Some("Cache store"));
    rest_but_for_ages(&mut world);
}

#[test]
fn the_home_screen_lists_the_open_asks_and_opens_the_one_picked() {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );

    let listed: Vec<String> = world
        .screen()
        .descendants(Target::key("home.group.asks"))
        .into_iter()
        .filter_map(|n| n.key?.strip_prefix("home.ask.").map(str::to_owned))
        .collect();
    assert_eq!(listed, ["a-release", "a-cache1"]);

    click(&mut world, "home.ask.a-cache1");

    assert_eq!(shown(&world).as_deref(), Some("Cache store"));
    rest_but_for_ages(&mut world);
}

#[test]
fn the_collapsed_rail_counts_open_asks_and_marks_the_desktops_that_hold_them() {
    let sessions = vec![api_working(), session("idle", "idle")];
    let desktops = vec![
        desktop("d1", "a", "API", vec![pane("d1", "p1", "api")]),
        desktop("d2", "b", "Idle", vec![pane("d2", "p2", "idle")]),
    ]
    .into_iter()
    .zip([1, 2])
    .map(|(mut d, slot)| {
        d.shortcut_slot = Some(slot);
        d
    })
    .collect();
    let mut world = start_on(state(sessions, desktops), vec![cache_store()], &[]);

    world.gesture(|app| app.click(Target::role("button").name("Collapse sidebar")));

    assert!(name(&world, "titlebar.asks").starts_with("1 open ask"));
    assert!(has(&world, "rail.desktop.1.ask"));
    assert!(!has(&world, "rail.desktop.2.ask"));
    click(&mut world, "titlebar.asks");
    assert_eq!(shown(&world).as_deref(), Some("Cache store"));
}

fn run_open_asks(world: &mut World) {
    world.gesture(|app| app.key(chord(Action::UiCommandPalette)));
    world.gesture(|app| app.type_text("open asks"));
    click(world, "palette.row.command:asks");
}

#[test]
fn the_palettes_open_asks_opens_the_drawer_and_leaves_an_open_drawer_open() {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );

    run_open_asks(&mut world);
    assert_eq!(shown(&world).as_deref(), Some("Release day"));

    run_open_asks(&mut world);
    assert_eq!(shown(&world).as_deref(), Some("Release day"));
}

#[test]
fn space_picks_the_focused_choice_and_the_send_key_sends_it() {
    let mut world = start(vec![release_day()], vec![web_waiting()]);
    open_drawer(&mut world);
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q1.choice.0"));

    letter(&mut world, " ");
    assert!(checked(&world, "asks.q.q1.choice.0"));
    send_key(&mut world);

    assert_eq!(
        sent_answers(&world),
        [serde_json::json!({
            "ask_id": "a-release",
            "answers": [{"key": "q1", "picked": ["macOS"]}],
        })]
    );
}

#[test]
fn a_one_choice_questions_other_answer_replaces_its_pick_from_the_first_character() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    open_drawer(&mut world);
    click(&mut world, "asks.q.q1.choice.0");

    type_into(&mut world, "asks.q.q1.other.field", "S");
    assert!(!checked(&world, "asks.q.q1.choice.0"));
    send_key(&mut world);

    assert_eq!(
        sent_answers(&world),
        [serde_json::json!({
            "ask_id": "a-cache1",
            "answers": [{"key": "q1", "picked": [], "other": "S"}],
        })]
    );
}

#[test]
fn the_clock_alone_returns_a_woken_askers_ask_to_the_counts_and_moves_the_drawers_ages_on() {
    let api_snoozed = Session {
        turn_snoozed_until: Some(ago(-10)),
        ..api_working()
    };
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_snoozed, web_waiting()],
    );
    open_drawer(&mut world);
    assert!(name(&world, "titlebar.asks").starts_with("1 open ask ("));
    let release_before = name(&world, "asks.row.a-release");

    world.advance(Duration::from_secs(11 * 60));

    assert!(name(&world, "titlebar.asks").starts_with("2 open asks ("));
    assert!(!name(&world, "asks.row.a-cache1").contains("until "));
    assert_ne!(name(&world, "asks.row.a-release"), release_before);
}
