use std::collections::BTreeMap;
use std::time::Duration;

use attn_app::Action;
use attn_protocol::{
    Ask, AskAnswer, AskAnswerResultMessage, AskChoice, AskDefault, AskFollowUpResultMessage,
    AskPart, AskQuestion, AskReply, AskThread, AsksUpdatedMessage, CrewMember, InitialStateMessage,
    Session, SessionStateChangedMessage,
};
use attn_protocol::{
    GardenSeedsUpdatedMessage, PartyView, Seed, SeedDocument, SeedDocumentGetResultMessage,
    SeedStandbyView, StandbyTender,
};
use attn_testkit::{Daemon, Target, WALL_START_MS, World, message};
use rune::{Key, KeyEvent, Mods};

use super::queue::{ago, one_desktop};
use super::sidebar::{chord, desktop, pane, session, state};

fn choice(label: &str) -> AskChoice {
    AskChoice {
        label: label.into(),
        ..AskChoice::default()
    }
}

pub(super) fn question(key: &str, kind: &str, prompt: &str, choices: &[&str]) -> AskPart {
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

pub(super) fn prose(markdown: &str) -> AskPart {
    AskPart {
        markdown: Some(markdown.into()),
        ..AskPart::default()
    }
}

pub(super) fn ask(
    id: &str,
    session: &str,
    subject: &str,
    minutes_ago: i64,
    parts: Vec<AskPart>,
) -> Ask {
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

pub(super) fn cache_store() -> Ask {
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

pub(super) fn release_day() -> Ask {
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
        attachments: None,
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

pub(super) fn api_working() -> Session {
    session("api", "working")
}

pub(super) fn web_waiting() -> Session {
    Session {
        turn_owed: Some(true),
        turn_opened_at: Some(ago(20)),
        ..session("web", "waiting_input")
    }
}

pub(super) fn start(asks: Vec<Ask>, sessions: Vec<Session>) -> World {
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

pub(super) fn open_sheet(world: &mut World) {
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
        .filter(|id| !id.contains('.'))
        .collect()
}

const NOTE: &str = "asks.note.field";

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

pub(super) fn add_note(world: &mut World) {
    scroll_to(world, "asks.note.add");
    click(world, "asks.note.add");
}

pub(super) fn type_into(world: &mut World, key: &str, text: &str) {
    scroll_to(world, key);
    click(world, key);
    let text = text.to_owned();
    world.gesture(|app| app.type_text(&text));
}

/// Scrolls the ask the way a person does, with the wheel over it, until `key` is in view.
fn scroll_to(world: &mut World, key: &str) {
    let [x, y, w, h] = world.rect("asks.ask");
    for _ in 0..20 {
        let [_, top, _, height] = world.rect(key);
        if top + height <= y + h {
            return;
        }
        let at = ((x + w / 2.0) as f32, (y + h / 2.0) as f32);
        world.gesture(|app| {
            app.runtime
                .wheel_event(rune::WheelEvent::new(0.0, -60.0).at(at.0, at.1))
        });
    }
    panic!("{key} stays below the ask's view");
}

pub(super) fn checked(world: &World, key: &str) -> bool {
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

pub(super) fn has(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

fn name(world: &World, key: &str) -> String {
    world
        .screen()
        .get(Target::key(key))
        .name
        .unwrap_or_default()
}

pub(super) fn update_asks(world: &mut World, asks: Vec<Ask>) {
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
fn the_sheet_opens_on_the_ask_of_the_agent_waiting_on_you_first_with_the_keyboard_in_its_first_answer()
 {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );
    open_sheet(&mut world);

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
fn the_sheet_keeps_the_shown_ask_and_focus_when_another_agent_starts_waiting() {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), session("web", "working")],
    );
    open_sheet(&mut world);
    assert_eq!(shown(&world).as_deref(), Some("Cache store"));

    changes(&mut world, web_waiting());

    assert_eq!(rows(&world), ["a-release", "a-cache1"]);
    assert_eq!(shown(&world).as_deref(), Some("Cache store"));
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q1.choice.0"));
}

#[test]
fn the_sheets_letter_keys_work_after_a_click_on_the_ask_itself() {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );
    open_sheet(&mut world);
    click(&mut world, "asks.row.a-cache1");
    click(&mut world, "asks.part.0");

    letter(&mut world, "k");

    assert_eq!(shown(&world).as_deref(), Some("Release day"));
}

/// Drags across the first line of the text keyed `key`, from its start to past its end.
fn drag_across(world: &mut World, key: &str) {
    let [x, y, w, h] = world.rect(key);
    let line = (y + h.min(16.0) / 2.0) as f32;
    let (from, to) = ((x as f32 + 1.0, line), ((x + w) as f32 + 4.0, line));
    world.gesture(|app| {
        app.drag_from(from, to);
        app.release(to);
    });
    world.settle();
}

fn copied(world: &mut World) -> Option<String> {
    world.clipboard.set_text("");
    world.gesture(|app| app.press("mod+c"));
    world.clipboard.text().map(|t| t.to_string())
}

#[test]
fn a_drag_selects_the_subject_a_part_and_a_choice_and_the_copy_key_copies_it() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    open_sheet(&mut world);

    drag_across(&mut world, "asks.subject");
    assert_eq!(copied(&mut world).as_deref(), Some("Cache store"));

    drag_across(&mut world, "asks.part.0");
    assert_eq!(
        copied(&mut world).as_deref(),
        Some("Deploys empty the cache.")
    );

    drag_across(&mut world, "asks.q.q1.choice.1.name");
    assert_eq!(copied(&mut world).as_deref(), Some("Redis"));
    assert!(
        !checked(&world, "asks.q.q1.choice.1"),
        "a drag picks nothing"
    );
}

/// Drags across the first line of the text keyed `key`, from the edge of the ask's column to
/// past the text's end.
fn drag_from_edge(world: &mut World, key: &str) {
    let [x, y, w, h] = world.rect(key);
    let edge = world.rect("asks.subject.block")[0];
    let line = (y + h.min(16.0) / 2.0) as f32;
    let (from, to) = ((edge as f32, line), ((x + w) as f32 + 4.0, line));
    world.gesture(|app| {
        app.drag_from(from, to);
        app.release(to);
    });
    world.settle();
}

#[test]
fn a_drag_that_starts_at_the_edge_of_the_asks_column_beside_a_part_in_a_wide_window_selects_its_first_line()
 {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    world.resize((1200.0, 800.0));
    open_sheet(&mut world);
    add_note(&mut world);
    type_into(&mut world, NOTE, "draft");

    drag_from_edge(&mut world, "asks.part.0");

    assert_eq!(
        copied(&mut world).as_deref(),
        Some("Deploys empty the cache.")
    );
}

#[test]
fn the_copy_key_copies_the_selection_though_a_field_held_the_keyboard() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    open_sheet(&mut world);
    add_note(&mut world);
    type_into(&mut world, NOTE, "draft");
    assert_eq!(focused(&world).as_deref(), Some(NOTE));

    scroll_to(&mut world, "asks.q.q1.choice.0.name");
    drag_across(&mut world, "asks.q.q1.choice.0.name");

    assert_eq!(copied(&mut world).as_deref(), Some("Postgres"));
}

#[test]
fn a_click_on_a_choices_name_still_picks_it() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    open_sheet(&mut world);

    let [x, y, _, h] = world.rect("asks.q.q1.choice.1.name");
    let at = ((x + 4.0) as f32, (y + h / 2.0) as f32);
    world.gesture(|app| app.click_at(at));

    assert!(checked(&world, "asks.q.q1.choice.1"));
}

fn defaulting(mut ask: Ask, choice: &str, in_minutes: i64) -> Ask {
    let question = ask
        .parts
        .iter_mut()
        .find_map(|p| p.question.as_mut().filter(|q| q.kind == "one"))
        .unwrap();
    question.default = Some(AskDefault {
        choice: choice.into(),
        due_at: ago(-in_minutes),
    });
    ask
}

#[test]
fn an_unanswered_default_reads_its_due_time_on_the_users_clock_in_the_ask_and_its_row() {
    let mut world = start(
        vec![defaulting(cache_store(), "Postgres", 90)],
        vec![api_working()],
    );
    open_sheet(&mut world);

    let due = rune::time::LocalTime::at(WALL_START_MS + 90 * 60_000);
    let clock = format!("{}:{:02}", due.hour, due.minute);
    let said = name(&world, "asks.default");
    assert!(
        said.starts_with("No answer by ") && said.contains(&clock),
        "{said}"
    );
    assert!(said.ends_with(", and api goes with Postgres"), "{said}");
    assert!(
        name(&world, "asks.row.a-cache1").contains(&format!("default {clock}")),
        "{}",
        name(&world, "asks.row.a-cache1")
    );

    click(&mut world, "asks.subject");
    world.clipboard.set_text("");
    letter(&mut world, "c");
    let copied = world.clipboard.text().map(|t| t.to_string()).unwrap();
    assert!(
        copied.contains("Recommended: Postgres\nDefault: Postgres after 2h\n:::"),
        "{copied}"
    );

    update_asks(
        &mut world,
        vec![replied(
            defaulting(cache_store(), "Postgres", 90),
            vec![picked("q1", &["Redis"])],
            None,
        )],
    );
    letter(&mut world, "r");
    assert_eq!(rows(&world), ["a-cache1"]);
    assert!(has(&world, "asks.subject"));
    assert!(!has(&world, "asks.default"));
    assert!(!name(&world, "asks.row.a-cache1").contains("default "));
}

#[test]
fn a_replied_answer_selects_and_copies() {
    let answered = replied(
        cache_store(),
        vec![picked("q1", &["Redis"]), written("q2", "Back at noon")],
        None,
    );
    let mut world = start(vec![answered], vec![api_working()]);
    open_sheet(&mut world);
    letter(&mut world, "r");

    drag_across(&mut world, "asks.q.q1.choice.1.name");
    assert_eq!(copied(&mut world).as_deref(), Some("Redis"));

    scroll_to(&mut world, "asks.q.q2.text");
    drag_across(&mut world, "asks.q.q2.text");
    assert_eq!(copied(&mut world).as_deref(), Some("Back at noon"));
}

#[test]
fn the_sheets_letter_keys_work_after_a_drag_across_the_ask_text() {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );
    world.resize((1000.0, 1000.0));
    open_sheet(&mut world);
    click(&mut world, "asks.row.a-cache1");
    add_note(&mut world);
    type_into(&mut world, NOTE, "draft");
    drag_across(&mut world, "asks.subject");

    letter(&mut world, "k");

    assert_eq!(shown(&world).as_deref(), Some("Release day"));
}

#[test]
fn c_copies_the_ask_as_the_markdown_the_agent_wrote_with_its_id_and_asker() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    open_sheet(&mut world);
    click(&mut world, "asks.subject");
    world.clipboard.set_text("");

    letter(&mut world, "c");

    assert_eq!(
        world.clipboard.text().map(|t| t.to_string()).as_deref(),
        Some(
            "# Cache store\n\n\
             Deploys empty the cache.\n\n\
             :::question\n\
             Which store should keep the cache?\n\
             - [ ] Postgres — already deployed\n\
             - [ ] Redis\n\
             Recommended: Postgres\n\
             :::\n\n\
             :::question-text\n\
             What should the banner say?\n\
             :::\n\n\
             Ask a-cache1 from api\n"
        )
    );
}

fn amended(mut ask: Ask, markdown: &str, minutes_ago: i64) -> Ask {
    ask.parts.push(AskPart {
        markdown: Some(markdown.into()),
        amended_at: Some(ago(minutes_ago)),
        ..AskPart::default()
    });
    ask
}

#[test]
fn an_amendment_shows_as_an_update_after_the_questions_and_keeps_the_answer_started() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    open_sheet(&mut world);
    click(&mut world, "asks.q.q1.choice.1");
    type_into(&mut world, "asks.q.q2.text.field", "Welcome back");

    update_asks(
        &mut world,
        vec![amended(
            cache_store(),
            "Redis already runs in staging.\n",
            2,
        )],
    );

    assert_eq!(name(&world, "asks.part.3.amended"), "Updated 2m ago");
    assert!(has(&world, "asks.part.3"));
    assert_eq!(
        world.rect("asks.part.3.amended")[0],
        world.rect("asks.subject.block")[0],
        "the update's label starts on the ask's column"
    );
    assert!(!has(&world, "asks.part.0.amended"));
    assert!(checked(&world, "asks.q.q1.choice.1"));
    assert_eq!(
        world
            .screen()
            .get(Target::key("asks.q.q2.text.field"))
            .value
            .as_deref(),
        Some("Welcome back")
    );
    rest_but_for_ages(&mut world);

    click(&mut world, "asks.subject");
    world.clipboard.set_text("");
    letter(&mut world, "c");
    let copied = world
        .clipboard
        .text()
        .map(|t| t.to_string())
        .unwrap_or_default();
    assert!(
        copied.ends_with(&format!(
            ":::\n\nUpdated {}:\n\nRedis already runs in staging.\n\nAsk a-cache1 from api\n",
            ago(2)
        )),
        "{copied}"
    );
}

/// At rest but for the next minute an age in the open sheet reads differently.
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
fn escape_closes_the_sheet_and_gives_the_keyboard_back_to_the_agent() {
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

    open_sheet(&mut world);
    plain(&mut world, Key::Escape);
    world.gesture(|app| app.type_text("x"));

    assert!(!has(&world, "asks.sheet"));
    assert_eq!(typed_into(&world), ["r-api", "r-api"]);
}

#[test]
fn sending_sends_the_picked_answers_and_the_note_then_brings_the_next_ask() {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );
    open_sheet(&mut world);
    click(&mut world, "asks.row.a-cache1");

    click(&mut world, "asks.q.q1.choice.0");
    type_into(&mut world, "asks.q.q2.text.field", "Welcome back");
    add_note(&mut world);
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
    open_sheet(&mut world);

    click(&mut world, "asks.q.q1.choice.1");
    type_into(&mut world, "asks.q.q1.other.field", "FreeBSD");
    add_note(&mut world);
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
    open_sheet(&mut world);

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
    open_sheet(&mut world);

    letter(&mut world, "a");

    assert!(checked(&world, "asks.q.q1.choice.0"));
    assert!(checked(&world, "asks.q.q2.choice.0"));
}

#[test]
fn accept_all_recommended_shows_only_when_several_questions_recommend_one() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    open_sheet(&mut world);

    assert!(!has(&world, "asks.accept"));
}

#[test]
fn nothing_is_sent_until_a_question_is_answered_or_a_note_written() {
    let mut world = start(vec![release_day()], vec![web_waiting()]);
    open_sheet(&mut world);

    assert!(disabled(&world, "asks.send"));
    add_note(&mut world);
    click(&mut world, "asks.note.field");
    send_key(&mut world);

    assert!(world.daemon.sent_of("ask_answer").is_empty());
}

#[test]
fn the_asking_agents_rows_are_marked_and_open_agent_closes_the_sheet_and_shows_the_agent() {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );
    world.open_desktop();

    assert!(has(&world, "sidebar.session.api.ask"));
    assert!(has(&world, "sidebar.session.web.ask"));
    open_sheet(&mut world);
    click(&mut world, "asks.row.a-release");
    click(&mut world, "asks.open-agent");

    let shows = world.daemon.sent_of("desktop_show_session");
    assert_eq!(shows.last().unwrap()["session_id"], "web");
    assert!(!has(&world, "asks.sheet"));
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
    open_sheet(&mut world);
    letter(&mut world, "j");
    assert_eq!(shown(&world).as_deref(), Some("Landing week"));
    assert_eq!(name(&world, "asks.from"), "successor");
    let seed = seed_words(&world);
    assert!(seed.contains(&"cache-migration".to_owned()));
    assert!(seed.contains(&"Cache store migration".to_owned()));

    click(&mut world, "asks.open-agent");
    let shows = world.daemon.sent_of("desktop_show_session");
    assert_eq!(shows.last().unwrap()["session_id"], "successor");
}

#[test]
fn a_seed_the_garden_does_not_hold_opens_in_a_tile_from_the_button_and_g() {
    let mut world = start(vec![landing()], vec![api_working()]);
    open_sheet(&mut world);

    click(&mut world, "asks.open-seed");
    assert!(!has(&world, "asks.sheet"));
    assert_eq!(garden_heading(&world), None);
    let opened = world.daemon.sent_of("open_seed");
    assert_eq!(opened.len(), 1);
    assert_eq!(opened[0]["seed_id"], "s-cache1");

    world.daemon.reply_to(
        &opened[0],
        serde_json::json!({
            "event": "open_seed_result",
            "request_id": opened[0]["request_id"],
            "success": true,
        }),
    );
    world.settle();
    open_sheet(&mut world);
    click(&mut world, "asks.from");
    letter(&mut world, "g");
    assert_eq!(world.daemon.sent_of("open_seed").len(), 2);
    assert!(!has(&world, "asks.sheet"));
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
    open_sheet(&mut world);
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
    open_sheet(&mut world);
    assert_eq!(
        name(&world, "asks.from"),
        "waiting for the next tender of Cache store migration"
    );
    assert!(!has(&world, "asks.open-agent"));
    assert!(!has(&world, "asks.snooze"));
}

#[test]
fn an_ask_whose_tender_is_on_standby_names_it_and_says_whether_an_answer_resumes_it() {
    for (resume, blocked, says) in [
        (None, None, "Your answer resumes it."),
        (Some("any"), None, "Your answer resumes it."),
        (
            Some("watches"),
            None,
            "Your answer waits until a reminder or pull request update resumes it.",
        ),
        (
            Some("off"),
            None,
            "Seed mail does not resume it, so your answer waits for it.",
        ),
        (
            None,
            Some("the folder is gone"),
            "Resume blocked: the folder is gone. Your answer waits for it.",
        ),
    ] {
        let mut seed = migration(Some("delegate"));
        seed.standby = Some(SeedStandbyView {
            note: "Waits for the user".into(),
            resume_blocked: blocked.map(str::to_owned),
            tender: Some(StandbyTender {
                session_id: "delegate".into(),
                label: "Migrate the cache".into(),
                agent: "claude".into(),
                ..StandbyTender::default()
            }),
            ..SeedStandbyView::default()
        });
        let mut state = one_desktop(vec![api_working()]);
        state.seeds = Some(vec![seed]);
        let settings: Vec<(&str, &str)> = resume
            .map(|value| ("garden.tender_resume", value))
            .into_iter()
            .collect();
        let mut world = start_on(state, vec![landing()], &settings);
        open_sheet(&mut world);

        assert_eq!(name(&world, "asks.from"), "Migrate the cache · on standby");
        assert_eq!(name(&world, "asks.resumes"), says, "{resume:?} {blocked:?}");
        assert!(!has(&world, "asks.open-agent"));
        assert!(!has(&world, "asks.snooze"));
    }
}

#[test]
fn a_follow_up_to_a_tender_on_standby_says_whether_it_resumes_it() {
    for (resume, says) in [
        (None, "Follow-up resumes Migrate the cache"),
        (
            Some("off"),
            "Follow-up waits for Migrate the cache · on standby",
        ),
    ] {
        let mut seed = migration(Some("delegate"));
        seed.standby = Some(SeedStandbyView {
            tender: Some(StandbyTender {
                session_id: "delegate".into(),
                label: "Migrate the cache".into(),
                agent: "claude".into(),
                ..StandbyTender::default()
            }),
            ..SeedStandbyView::default()
        });
        let mut state = one_desktop(vec![api_working()]);
        state.seeds = Some(vec![seed]);
        let settings: Vec<(&str, &str)> = resume
            .map(|value| ("garden.tender_resume", value))
            .into_iter()
            .collect();
        let answered = replied(landing(), vec![picked("q1", &["Postgres"])], None);
        let mut world = start_on(state, vec![answered], &settings);
        world.daemon.on("ask_follow_up", |r| {
            message(AskFollowUpResultMessage {
                request_id: r["request_id"].as_str().unwrap().into(),
                ask_id: r["ask_id"].as_str().unwrap().into(),
                success: true,
                ..AskFollowUpResultMessage::new()
            })
        });
        open_sheet(&mut world);
        letter(&mut world, "r");
        letter(&mut world, "f");
        world.gesture(|app| app.type_text("Keep the old keys."));
        send_key(&mut world);

        assert_eq!(name(&world, "notice.text"), says, "{resume:?}");
    }
}

#[test]
fn open_in_garden_closes_the_sheet_for_the_seed_and_the_sheet_reopens_on_the_ask() {
    let mut world = start_with_seeds(
        vec![landing()],
        vec![session("successor", "working")],
        vec![migration(Some("successor"))],
    );
    open_sheet(&mut world);

    click(&mut world, "asks.open-seed");
    assert!(!has(&world, "asks.sheet"));
    assert_eq!(
        garden_heading(&world).as_deref(),
        Some("Cache store migration")
    );
    let read = world.daemon.sent_of("seed_document_get");
    assert_eq!(read.last().unwrap()["seed_id"], "s-cache1");

    plain(&mut world, Key::Escape);
    assert_eq!(garden_heading(&world), None);
    open_sheet(&mut world);
    assert_eq!(shown(&world).as_deref(), Some("Landing week"));
    click(&mut world, "asks.from");
    letter(&mut world, "g");
    assert!(!has(&world, "asks.sheet"));
    assert_eq!(
        garden_heading(&world).as_deref(),
        Some("Cache store migration")
    );
}

#[test]
fn the_sheet_opens_over_the_garden_and_escape_closes_the_sheet_before_the_garden() {
    let mut world = start_with_seeds(
        vec![landing()],
        vec![session("successor", "working")],
        vec![migration(Some("successor"))],
    );
    world.gesture(|app| app.key(chord(Action::BoardOpen)));
    world.gesture(|app| app.key(chord(Action::BoardOpen)));
    assert!(has(&world, "garden.frame"));
    open_sheet(&mut world);
    click(&mut world, "asks.from");
    letter(&mut world, "k");
    assert_eq!(shown(&world).as_deref(), Some("Landing week"));

    plain(&mut world, Key::Escape);
    assert!(!has(&world, "asks.sheet"));
    assert!(has(&world, "garden.frame"));

    plain(&mut world, Key::Escape);
    assert!(!has(&world, "garden.frame"));
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

    open_sheet(&mut world);
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
    assert_eq!(name(&world, "asks.count"), "2 open");
    assert!(!name(&world, "asks.row.a-cache1").contains("until "));
    plain(&mut world, Key::Escape);
    assert!(name(&world, "titlebar.asks").starts_with("2 open asks ("));
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

    open_sheet(&mut world);
    assert_eq!(rows(&world), ["a-release", "a-cache1"]);
    assert!(name(&world, "asks.row.a-cache1").starts_with("api"));
    click(&mut world, "asks.row.a-cache1");
    assert_eq!(name(&world, "asks.snooze"), "Wake");
}

#[test]
fn the_sheet_snoozes_the_asking_agent_and_wakes_it_again() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    open_sheet(&mut world);

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
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q1.choice.0"));

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
fn an_agents_open_ask_shows_in_its_tile_header_previews_on_rest_and_opens_in_the_sheet() {
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
fn a_long_choice_wraps_inside_the_preview_clear_of_its_scroll_bar_in_the_smallest_window() {
    let mut first = cache_store();
    let choices = &mut first.parts[1].question.as_mut().unwrap().choices;
    choices[0].label =
        "Postgres-build-and-release-platform-本番の権限を移行する作業ツリー-tokyo-region-primary"
            .into();
    let more = |id: &str, minutes: i64| Ask {
        id: id.into(),
        created_at: ago(minutes),
        ..cache_store()
    };
    let mut world = start(
        vec![first, more("a-cache2", 20), more("a-cache3", 10)],
        vec![api_working()],
    );
    world.resize((533.0, 400.0));
    world.open_desktop();
    rest_on(&mut world, "tile.p0.ask");
    world.advance(Duration::from_millis(160));
    let preview = world.rect("tile.p0.ask.popover");
    let name = world.rect("asks.preview.a-cache1.q.0.choice.0.name");
    let bar = world
        .runtime()
        .elements()
        .into_iter()
        .find(|e| {
            e.path
                .ends_with("tile.p0.ask.popover.body-scroll-area/scroll-bar")
        })
        .expect("three asks overflow the preview, which shows its scroll bar")
        .rect;
    assert!(
        name[0] >= preview[0] && name[0] + name[2] <= f64::from(bar[0]),
        "the choice's name {name:?} runs past the preview {preview:?} or under its bar {bar:?}"
    );
}

#[test]
fn the_wheel_scrolls_a_preview_that_overflows_to_its_last_answer_which_opens_that_ask() {
    let more = |id: &str, subject: &str, minutes: i64| Ask {
        id: id.into(),
        subject: subject.into(),
        created_at: ago(minutes),
        ..cache_store()
    };
    let mut world = start(
        vec![
            cache_store(),
            more("a-cache2", "Cache size", 20),
            more("a-cache3", "Cache region", 10),
        ],
        vec![api_working()],
    );
    world.resize((533.0, 400.0));
    world.open_desktop();
    rest_on(&mut world, "tile.p0.ask");
    world.advance(Duration::from_millis(160));
    rest_on(&mut world, "tile.p0.ask.popover.body");
    let [x, y, w, h] = world.rect("tile.p0.ask.popover.body");
    let (x, y, w, h) = (x as f32, y as f32, w as f32, h as f32);
    assert!(
        world.rect("asks.preview.a-cache3.answer")[1] >= f64::from(y + h),
        "the last Answer starts out of view"
    );
    world.gesture(|app| app.runtime.wheel(x + w / 2.0, y + h / 2.0, -2000.0));
    world.settle();

    let answer = world.rect("asks.preview.a-cache3.answer");
    assert!(
        answer[1] + answer[3] <= f64::from(y + h) + 0.5,
        "the last Answer at {answer:?} stays below the preview's body ending at {}",
        y + h
    );
    click(&mut world, "asks.preview.a-cache3.answer");
    assert_eq!(shown(&world).as_deref(), Some("Cache region"));
    assert!(!has(&world, "tile.p0.ask.popover"));
}

#[test]
fn an_answer_s_focus_ring_stays_whole_in_a_preview_scrolled_to_either_end() {
    let more = |id: &str, subject: &str, minutes: i64| Ask {
        id: id.into(),
        subject: subject.into(),
        created_at: ago(minutes),
        ..cache_store()
    };
    let mut world = start(
        vec![
            cache_store(),
            more("a-cache2", "Cache size", 20),
            more("a-cache3", "Cache region", 10),
        ],
        vec![api_working()],
    );
    world.resize((533.0, 400.0));
    world.open_desktop();
    world.gesture(|app| app.runtime.set_full_keyboard_access(true));
    rest_on(&mut world, "tile.p0.ask");
    world.advance(Duration::from_millis(160));
    rest_on(&mut world, "tile.p0.ask.popover.body");
    let reach = f64::from(attn_ui::system::RING_REACH);
    for answer in [
        "asks.preview.a-cache1.answer",
        "asks.preview.a-cache3.answer",
    ] {
        for _ in 0..60 {
            if focused(&world).as_deref() == Some(answer) {
                break;
            }
            plain(&mut world, Key::Tab);
        }
        assert_eq!(
            focused(&world).as_deref(),
            Some(answer),
            "Tab reaches {answer}"
        );
        world.settle();
        let [bx, by, bw, bh] = world.rect("tile.p0.ask.popover.body");
        let [x, y, w, h] = world.rect(answer);
        assert!(
            x - reach >= bx - 0.5
                && y - reach >= by - 0.5
                && x + w + reach <= bx + bw + 0.5
                && y + h + reach <= by + bh + 0.5,
            "{answer} at {:?} rings past the preview's body at {:?}",
            [x, y, w, h],
            [bx, by, bw, bh]
        );
    }
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
fn a_press_on_the_ask_chip_opens_the_sheet_on_its_ask_and_a_click_outside_closes_it() {
    let beside = |id: &str| session(id, "working");
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), beside("b"), web_waiting()],
    );
    world.resize((1600.0, 900.0));
    world.open_desktop();
    click(&mut world, "tile.p0.ask");
    assert_eq!(shown(&world).as_deref(), Some("Cache store"));
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q1.choice.0"));

    let [x, y, _, _] = world.rect("asks.sheet");
    world.gesture(|app| app.click_at((x as f32 / 2.0, y as f32 + 40.0)));
    assert!(!has(&world, "asks.sheet"));
    assert!(has(&world, "tile.p0"));

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
    open_sheet(&mut world);
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
    open_sheet(&mut world);
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
    assert_eq!(focused(&world).as_deref(), Some("asks.sheet"));

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
fn the_sheet_takes_the_keyboard_with_no_open_asks_so_r_shows_the_replies_instead_of_reaching_the_agent()
 {
    let answered = replied(cache_store(), vec![picked("q1", &["Postgres"])], None);
    let mut world = start(vec![answered], vec![api_working()]);
    world.open_desktop();
    open_sheet(&mut world);
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
    open_sheet(&mut world);
    assert!(!has(&world, "asks.sheet"));

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
        .filter(|id| !id.contains('.'))
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
    assert!(name(&world, "rail.desktop.1").ends_with(", has open asks"));
    assert!(!name(&world, "rail.desktop.2").contains("asks"));
    click(&mut world, "titlebar.asks");
    assert_eq!(shown(&world).as_deref(), Some("Cache store"));
}

fn run_open_asks(world: &mut World) {
    world.gesture(|app| app.key(chord(Action::UiCommandPalette)));
    world.gesture(|app| app.type_text("open asks"));
    click(world, "palette.row.command:asks");
}

#[test]
fn the_palettes_open_asks_opens_the_sheet_and_leaves_an_open_sheet_open() {
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
    open_sheet(&mut world);
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
    open_sheet(&mut world);
    click(&mut world, "asks.q.q1.choice.0");

    type_into(&mut world, "asks.q.q1.other.field", "S");
    assert!(!checked(&world, "asks.q.q1.choice.0"));
    world.gesture(|app| app.press("backspace"));
    assert!(
        !checked(&world, "asks.q.q1.choice.0"),
        "clearing Other brought the pick back"
    );
    world.gesture(|app| app.type_text("S"));
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
fn the_clock_alone_returns_a_woken_askers_ask_to_the_counts_and_moves_the_sheets_ages_on() {
    let api_snoozed = Session {
        turn_snoozed_until: Some(ago(-10)),
        ..api_working()
    };
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_snoozed, web_waiting()],
    );
    open_sheet(&mut world);
    assert_eq!(name(&world, "asks.count"), "1 open");
    let release_before = name(&world, "asks.row.a-release");

    world.advance(Duration::from_secs(11 * 60));

    assert_eq!(name(&world, "asks.count"), "2 open");
    assert!(!name(&world, "asks.row.a-cache1").contains("until "));
    assert_ne!(name(&world, "asks.row.a-release"), release_before);
}

#[test]
fn tab_reaches_every_button_of_the_asks_sheet() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    open_sheet(&mut world);

    assert_eq!(
        super::reach::unreached_by_tab(&mut world),
        Vec::<String>::new()
    );
}

pub(super) fn mentions_answered(world: &World, mentions: &[(&str, &str)]) {
    let mentions: Vec<serde_json::Value> = mentions
        .iter()
        .map(|(text, seed)| serde_json::json!({"text": text, "seed_id": seed}))
        .collect();
    world.daemon.on("ask_seed_mentions", move |r| {
        serde_json::json!({
            "event": "ask_seed_mentions_result",
            "request_id": r["request_id"],
            "success": true,
            "mentions": mentions,
        })
    });
}

fn seed_links(world: &World) -> Vec<(String, String)> {
    world
        .screen()
        .descendants(Target::key("asks.ask"))
        .into_iter()
        .filter(|n| n.role == "link")
        .map(|n| (n.name.unwrap_or_default(), n.key.unwrap_or_default()))
        .collect()
}

#[test]
fn the_seeds_an_ask_names_by_id_or_slug_open_from_its_words_and_code_and_unknown_ids_stay_text() {
    let mut mentioning = ask(
        "a-cache1",
        "api",
        "Review s-cache1",
        30,
        vec![
            prose(
                "Blocked on cache-migration and s-zzzzzz; run `attn seed show s-cache1`.\n\n\
                 ```\ns-cache1\n```",
            ),
            question("q1", "text", "Is cache-migration done?", &[]),
        ],
    );
    mentioning.base_dir = "/tmp/api".into();
    let mut world = start_with_seeds(vec![mentioning], vec![api_working()], vec![migration(None)]);
    mentions_answered(
        &world,
        &[("s-cache1", "s-cache1"), ("cache-migration", "s-cache1")],
    );
    open_sheet(&mut world);

    assert_eq!(
        world.daemon.sent_of("ask_seed_mentions")[0]["ask_id"],
        "a-cache1"
    );
    let links: Vec<String> = seed_links(&world)
        .into_iter()
        .map(|(name, _)| name)
        .collect();
    assert_eq!(links, ["s-cache1", "cache-migration", "cache-migration"]);
    let words: Vec<String> = world
        .screen()
        .descendants(Target::key("asks.ask"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(words.iter().any(|w| w.contains("attn seed show s-cache1")));
    assert!(
        !words
            .iter()
            .any(|w| w.contains('[') || w.contains("attn-seed")),
        "code shows as written: {words:?}"
    );

    let in_prose = seed_links(&world)[1].1.clone();
    world.gesture(|app| app.click(Target::key(in_prose)));
    assert_eq!(
        garden_heading(&world).as_deref(),
        Some("Cache store migration")
    );
}

#[test]
fn a_mentioned_seed_the_garden_does_not_hold_opens_in_a_tile_and_unresolved_names_stay_text() {
    let read = ask(
        "a-cache1",
        "api",
        "Cache store",
        30,
        vec![
            prose("Read s-cache1, old-work, and shared-work."),
            question("q1", "text", "Ready?", &[]),
        ],
    );
    let mut world = start(vec![read], vec![api_working()]);
    mentions_answered(
        &world,
        &[("s-cache1", "s-cache1"), ("old-work", "s-cache1")],
    );
    open_sheet(&mut world);

    let links = seed_links(&world);
    let names: Vec<&str> = links.iter().map(|(name, _)| name.as_str()).collect();
    assert_eq!(names, ["s-cache1", "old-work"]);
    world.gesture(|app| app.click(Target::key(links[1].1.clone())));
    let opened = world.daemon.sent_of("open_seed");
    assert_eq!(opened.len(), 1);
    assert_eq!(opened[0]["seed_id"], "s-cache1");
}

#[test]
fn a_seed_planted_after_the_ask_is_linked_once_the_garden_has_it() {
    let read = ask(
        "a-cache1",
        "api",
        "Cache store",
        30,
        vec![
            prose("Blocked on cache-migration."),
            question("q1", "text", "Ready?", &[]),
        ],
    );
    let mut world = start(vec![read], vec![api_working()]);
    mentions_answered(&world, &[]);
    open_sheet(&mut world);
    assert!(seed_links(&world).is_empty());

    mentions_answered(&world, &[("cache-migration", "s-cache1")]);
    world.daemon.emit(message(GardenSeedsUpdatedMessage {
        profile_id: "work".into(),
        total: 1,
        seeds: vec![migration(None)],
        ..GardenSeedsUpdatedMessage::new()
    }));
    world.settle();

    assert_eq!(world.daemon.sent_of("ask_seed_mentions").len(), 2);
    let names: Vec<String> = seed_links(&world)
        .into_iter()
        .map(|(name, _)| name)
        .collect();
    assert_eq!(names, ["cache-migration"]);
}

const PNG: &str = "iVBORw0KGgoAAAANSUhEUgAAAAQAAAADCAYAAAC09K7GAAAAEklEQVR4nGP4z8DwHxkzEBQAAGHtF+mRbBMEAAAAAElFTkSuQmCC";

/// A grey picture 2000 by 8 pixels.
const WIDE_PNG: &str = "iVBORw0KGgoAAAANSUhEUgAAB9AAAAAICAIAAAAukPPiAAAAVUlEQVR42u3QMQEAAAzDoEqf9InICxLYAQAAAAAA2RQAAAAAAEA3BQAAAAAA0E0BAAAAAAB0UwAAAAAAAN0UAAAAAABANwUAAAAAANBNAQAAAAAAdA/rnsV0d7EvXAAAAABJRU5ErkJggg==";

#[test]
fn a_picture_in_an_ask_runs_wider_than_its_lines_and_centred_with_them() {
    let mut shot = ask(
        "a-cache1",
        "api",
        "Cache store",
        30,
        vec![
            prose("The banner overlaps:\n\n![banner](shots/banner.png)"),
            AskPart {
                question: Some(AskQuestion {
                    key: "q1".into(),
                    kind: "one".into(),
                    prompt: "Ship it?".into(),
                    context: Some("As it shows now:\n\n![now](shots/now.png)".into()),
                    choices: vec![choice("Yes"), choice("No")],
                    ..AskQuestion::default()
                }),
                ..AskPart::default()
            },
        ],
    );
    shot.base_dir = "/tmp/api".into();
    let mut world = start(vec![shot], vec![api_working()]);
    world.daemon.on("ask_image", |r| {
        serde_json::json!({
            "event": "ask_image_result",
            "request_id": r["request_id"],
            "success": true,
            "mime_type": "image/png",
            "data_base64": WIDE_PNG,
        })
    });
    world.resize((1512.0, 945.0));
    open_sheet(&mut world);
    world.settle();

    let subject = world.rect("asks.subject.block");
    let middle = |r: [f64; 4]| r[0] + r[2] / 2.0;
    assert_eq!(subject[2], 620.0, "the ask's lines");
    for name in ["banner", "now"] {
        let picture = world
            .screen()
            .bounds(Target::role("image").name(name))
            .map(|r| r.map(f64::from))
            .expect("the picture shows");
        assert_eq!(picture[2], 980.0, "the picture {name}: {picture:?}");
        assert_eq!(
            middle(picture),
            middle(subject),
            "{name} centred on the lines"
        );
    }
    for key in ["asks.q.q1.prompt", "asks.q.q1.choices"] {
        let line = world.rect(key);
        assert!(line[2] <= 620.0, "{key} on the ask's lines: {line:?}");
        assert_eq!(line[0], subject[0], "{key} starts on the ask's column");
    }
}

#[test]
fn a_picture_or_table_in_an_asks_list_or_quote_runs_past_its_lines_to_the_column_end() {
    let nested = |name: &str| {
        format!(
            "- The {name} banner overlaps the field, and the item wraps on the ask's lines like any other text.\n\n  ![{name} in a list](shots/banner.png)\n\n  | Placement | Before the change | After the change | Points across | Window | Keymap |\n  |---|---|---|---|---|---|\n  | In a list item | Narrower than the column | Out to the column end | Nine hundred | Wide | macOS |\n\n> The {name} banner overlaps the field, and the quote wraps on the ask's lines like any other text.\n>\n> ![{name} in a quote](shots/banner.png)\n"
        )
    };
    let mut shot = ask(
        "a-cache1",
        "api",
        "Cache store",
        30,
        vec![
            prose(&nested("prose")),
            AskPart {
                question: Some(AskQuestion {
                    key: "q1".into(),
                    kind: "one".into(),
                    prompt: "Ship it?".into(),
                    context: Some(nested("context")),
                    choices: vec![choice("Yes"), choice("No")],
                    ..AskQuestion::default()
                }),
                ..AskPart::default()
            },
        ],
    );
    shot.base_dir = "/tmp/api".into();
    let mut world = start(vec![shot], vec![api_working()]);
    world.daemon.on("ask_image", |r| {
        serde_json::json!({
            "event": "ask_image_result",
            "request_id": r["request_id"],
            "success": true,
            "mime_type": "image/png",
            "data_base64": WIDE_PNG,
        })
    });
    world.resize((1512.0, 945.0));
    open_sheet(&mut world);
    world.settle();

    let subject = world.rect("asks.subject.block");
    let column = world.rect("asks.part.0.block");
    let (lines_end, column_end) = (subject[0] + subject[2], column[0] + column[2]);
    let end = |r: [f64; 4]| r[0] + r[2];
    let screen = world.screen();
    for name in ["prose", "context"] {
        for placement in ["list", "quote"] {
            let picture = screen
                .bounds(Target::role("image").name(format!("{name} in a {placement}")))
                .map(|r| r.map(f64::from))
                .expect("the picture shows");
            assert_eq!(
                end(picture),
                column_end,
                "the {name} picture in a {placement}: {picture:?}"
            );
            assert!(
                picture[2] > subject[2],
                "the {name} picture in a {placement} runs wider than the lines: {picture:?}"
            );
        }
    }
    let lines: Vec<_> = world
        .runtime()
        .elements()
        .into_iter()
        .filter(|e| {
            e.text
                .as_deref()
                .is_some_and(|t| t.contains("banner overlaps"))
        })
        .map(|e| e.rect[0] as f64 + e.rect[2] as f64)
        .collect();
    assert_eq!(
        lines.len(),
        4,
        "a list item and a quote in prose and context"
    );
    assert!(
        lines.iter().all(|end| *end <= lines_end),
        "the words keep the ask's lines: {lines:?} past {lines_end}"
    );
    let tables: Vec<_> = world
        .runtime()
        .elements()
        .into_iter()
        .filter(|e| e.role == "table")
        .map(|e| e.rect.map(f64::from))
        .collect();
    assert_eq!(tables.len(), 2, "a table in prose and in context");
    for table in tables {
        assert!(
            end(table) > lines_end,
            "the table runs past the ask's lines: {table:?} past {lines_end}"
        );
        assert!(
            end(table) <= column_end,
            "the table stays in the column: {table:?}"
        );
    }
}

#[test]
fn an_asks_images_show_from_the_copy_the_home_keeps() {
    let mut screenshot = ask(
        "a-cache1",
        "api",
        "Cache store",
        30,
        vec![
            prose(
                "The banner overlaps: ![before](shots/before.png) ![odd](shots/a\\(b\\)&amp;c.png)",
            ),
            AskPart {
                question: Some(AskQuestion {
                    key: "q1".into(),
                    kind: "text".into(),
                    prompt: "Ship it?".into(),
                    context: Some("![after](/elsewhere/after.png)".into()),
                    ..AskQuestion::default()
                }),
                ..AskPart::default()
            },
        ],
    );
    screenshot.base_dir = "/tmp/api".into();
    let mut world = start(vec![screenshot], vec![api_working()]);
    world.daemon.on("ask_image", |r| {
        let found = r["path"] == "/tmp/api/shots/before.png";
        serde_json::json!({
            "event": "ask_image_result",
            "request_id": r["request_id"],
            "success": found,
            "mime_type": found.then_some("image/png"),
            "data_base64": found.then_some(PNG),
            "error": (!found).then(|| format!("ask a-cache1 holds no copy of {}", r["path"])),
        })
    });
    open_sheet(&mut world);
    world.settle();

    let mut asked: Vec<String> = world
        .daemon
        .sent_of("ask_image")
        .iter()
        .inspect(|r| assert_eq!(r["ask_id"], "a-cache1"))
        .map(|r| r["path"].as_str().unwrap().to_owned())
        .collect();
    asked.sort();
    assert_eq!(
        asked,
        [
            "/elsewhere/after.png",
            "/tmp/api/shots/a(b)&c.png",
            "/tmp/api/shots/before.png"
        ]
    );

    let said: Vec<String> = world
        .screen()
        .descendants(Target::key("asks.sheet"))
        .into_iter()
        .filter_map(|n| n.name.or(n.value))
        .collect();
    assert!(
        said.iter().any(|t| t == "[missing image: after]"),
        "an image whose copy the home lacks says so: {said:?}"
    );
    assert!(
        world
            .screen()
            .find(Target::role("image").name("after"))
            .is_none()
    );
    world.gesture(|app| app.click(Target::role("image").name("before")));
    assert_eq!(
        world
            .screen()
            .get(Target::key("image-preview.picture"))
            .name
            .as_deref(),
        Some("before")
    );
    plain(&mut world, Key::Escape);
    assert!(
        world
            .screen()
            .find(Target::key("image-preview.picture"))
            .is_none()
    );
    assert_eq!(shown(&world).as_deref(), Some("Cache store"));
}

#[test]
fn an_asks_wide_diagram_opens_to_zoom_and_its_wide_table_scrolls_sideways() {
    let participants: String = (0..13)
        .map(|i| format!("  participant P{i} as Service number {i} in the path\n"))
        .collect();
    let messages: String = (0..12)
        .map(|i| format!("  P{i}->>P{}: hand over\n", i + 1))
        .collect();
    let columns = 9;
    let row = |cell: &str| format!("|{}\n", format!(" {cell} |").repeat(columns));
    let table = format!(
        "{}{}{}",
        row("Option under comparison"),
        row("---"),
        row("a long reason it wins or loses")
    );
    let mut world = start(
        vec![ask(
            "a-cache1",
            "api",
            "Cache store",
            30,
            vec![prose(&format!(
                "```mermaid\nsequenceDiagram\n{participants}{messages}```\n\n{table}"
            ))],
        )],
        vec![api_working()],
    );
    open_sheet(&mut world);
    world.settle();

    let table_thumbs: Vec<[f32; 4]> = world
        .runtime()
        .elements()
        .into_iter()
        .filter(|e| e.key.as_deref() == Some("thumb") && e.path.contains("wide-"))
        .map(|e| e.rect)
        .collect();
    assert!(
        table_thumbs.iter().any(|thumb| thumb[2] > 0.0),
        "a table wider than the ask scrolls sideways under a thumb: {table_thumbs:?}"
    );
    let [x, y, w, h] = world
        .screen()
        .bounds(Target::key("asks.ask-scroll-area"))
        .expect("the ask is shown");
    world.gesture(|app| {
        app.runtime
            .wheel_event(rune::WheelEvent::new(0.0, 2000.0).at(x + w / 2.0, y + h / 2.0))
    });
    world.settle();
    world.gesture(|app| app.click(Target::role("button").name("Focus diagram")));
    world.settle();
    assert!(
        world
            .screen()
            .find(Target::key("diagram-preview.drawing"))
            .is_some(),
        "a diagram too wide to read fitted opens over the window to zoom"
    );
    plain(&mut world, Key::Escape);
    assert!(
        world
            .screen()
            .find(Target::key("diagram-preview.drawing"))
            .is_none()
    );
    assert_eq!(shown(&world).as_deref(), Some("Cache store"));
}

#[test]
fn an_asker_name_on_home_stays_whole_beside_a_long_subject_while_the_row_has_room() {
    let width_beside = |subject: &str| {
        let world = start(
            vec![Ask {
                subject: subject.into(),
                ..cache_store()
            }],
            vec![api_working()],
        );
        world
            .screen()
            .bounds(Target::key("home.ask.a-cache1.from"))
            .unwrap()[2]
    };
    let short = width_beside("Cache store");
    let long = width_beside(
        "Which store should keep the cache once deploys stop emptying it, and who owns its \
         eviction policy when the catalog doubles in size next quarter?",
    );
    assert_eq!(
        long, short,
        "the asker's name keeps its width beside a long subject"
    );
}

#[test]
fn a_long_asker_name_on_home_ends_in_an_ellipsis_and_leaves_room_for_the_subject() {
    let asker = Session {
        label:
            "waiting for the next tender of Teach the user how the durable kernel works in depth"
                .into(),
        ..api_working()
    };
    let world = start(vec![cache_store()], vec![asker]);
    let screen = world.screen();
    let bounds = |key: &str| screen.bounds(Target::key(key)).unwrap();
    let row = bounds("home.ask.a-cache1");
    let from = bounds("home.ask.a-cache1.from");
    let subject = bounds("home.ask.a-cache1.subject");
    assert!(from[0] + from[2] <= subject[0], "{from:?} {subject:?}");
    assert!(subject[2] > 0.0, "the subject keeps room: {subject:?}");
    assert!(
        subject[0] + subject[2] <= row[0] + row[2],
        "{subject:?} in {row:?}"
    );
}

#[test]
fn an_ask_from_a_member_between_sessions_names_the_member_and_leaves_attn_lowercase() {
    for (member, from) in [
        ("trellis", "Trellis"),
        ("mary-jane", "Mary-jane"),
        ("a", "A"),
        ("Trellis", "Trellis"),
        ("ólafur", "Ólafur"),
        ("attn", "attn"),
    ] {
        let from_member = Ask {
            crew_member: Some(member.into()),
            ..cache_store()
        };
        let mut world = start(vec![from_member], vec![session("s1", "working")]);
        open_sheet(&mut world);
        assert_eq!(
            name(&world, "asks.from"),
            format!("{from} (between sessions)")
        );
    }
}

#[test]
fn the_note_starts_lines_high_and_its_grip_drags_it_taller_but_never_shorter() {
    let mut world = start(vec![release_day()], vec![api_working(), web_waiting()]);
    open_sheet(&mut world);
    add_note(&mut world);
    scroll_to(&mut world, "asks.note.field-grip");
    let height = |world: &mut World| world.rect("asks.note.field-resizable")[3];
    let three_lines = height(&mut world);
    let one_line = world.rect("asks.q.q1.other.field")[3];
    assert!(
        three_lines >= 2.0 * one_line,
        "the note starts {three_lines} high, a one-line field {one_line}"
    );

    let [x, y, w, h] = world.rect("asks.note.field-grip");
    let grip = ((x + w / 2.0) as f32, (y + h / 2.0) as f32);
    world.gesture(|app| app.drag(Target::key("asks.note.field-grip"), (grip.0, grip.1 + 40.0)));
    assert_eq!(height(&mut world), three_lines + 40.0);

    scroll_to(&mut world, "asks.note.field-grip");
    let [x, y, w, h] = world.rect("asks.note.field-grip");
    let grip = ((x + w / 2.0) as f32, (y + h / 2.0) as f32);
    world.gesture(|app| {
        app.drag(
            Target::key("asks.note.field-grip"),
            (grip.0, grip.1 - 200.0),
        )
    });
    assert_eq!(height(&mut world), three_lines);
    rest_but_for_ages(&mut world);
}

#[test]
fn a_prompt_wider_than_its_question_wraps_inside_it() {
    let long = "Which region should we deploy to first, given that the Linux build is still \
                being validated and the macOS build already shipped to the testers last week?";
    let ask = ask(
        "a-region",
        "web",
        "Region",
        25,
        vec![question("q1", "one", long, &["eu", "us"])],
    );
    let mut world = start(vec![ask], vec![api_working(), web_waiting()]);
    open_sheet(&mut world);

    let [card_x, _, card_w, _] = world.rect("asks.q.q1");
    let [x, _, w, h] = world.rect("asks.q.q1.prompt");
    assert!(
        x + w <= card_x + card_w,
        "{x} + {w} past {card_x} + {card_w}"
    );
    assert!(h > 18.0, "the prompt stays one line, {h} high");
}

#[test]
fn a_url_that_holds_a_seeds_name_stays_one_link_that_opens_the_whole_url() {
    let url = "https://spotify.com/cache-migration/s-cache1";
    let mut world = start_with_seeds(
        vec![ask(
            "a-url",
            "api",
            "Links",
            30,
            vec![prose(&format!(
                "See {url}, [the notes]({url}) and cache-migration."
            ))],
        )],
        vec![api_working()],
        vec![migration(None)],
    );
    mentions_answered(
        &world,
        &[("s-cache1", "s-cache1"), ("cache-migration", "s-cache1")],
    );
    open_sheet(&mut world);

    assert_eq!(
        seed_links(&world),
        [
            (url.to_owned(), url.to_owned()),
            ("the notes".to_owned(), url.to_owned()),
            (
                "cache-migration".to_owned(),
                "attn-seed:s-cache1".to_owned()
            ),
        ]
    );
    world.gesture(|app| app.click(Target::key(url)));
    assert_eq!(world.opened_links(), [url]);
}

#[test]
fn a_prompt_and_its_choices_show_their_inline_markdown_with_links_that_open_and_answer_with_the_label_as_written()
 {
    let device = "https://auth.openai.com/codex/device";
    let guide = "https://example.com/guide";
    let mut sign_in = question(
        "q1",
        "one",
        &format!("Open {device} and enter the code **Z5RI-IDLV2**, then answer here."),
        &[],
    );
    sign_in.question.as_mut().unwrap().choices = vec![
        AskChoice {
            label: "**Signed in**".into(),
            description: Some(format!("see [the guide]({guide})")),
        },
        AskChoice {
            label: "Run `attn retry`".into(),
            description: None,
        },
    ];
    let mut world = start(
        vec![ask(
            "a-sign",
            "api",
            "Sign in to *Sigma*",
            30,
            vec![sign_in],
        )],
        vec![api_working()],
    );
    assert_eq!(name(&world, "home.ask.a-sign.subject"), "Sign in to Sigma");
    open_sheet(&mut world);

    assert_eq!(name(&world, "asks.subject"), "Sign in to Sigma");
    assert_eq!(
        name(&world, "asks.q.q1.choice.0"),
        "Signed in",
        "the choice reads without its markup"
    );
    assert_eq!(name(&world, "asks.q.q1.choice.1"), "Run attn retry");
    let links: Vec<(String, String)> = seed_links(&world);
    assert_eq!(
        links,
        [
            (device.to_owned(), device.to_owned()),
            ("the guide".to_owned(), guide.to_owned()),
        ]
    );

    world.gesture(|app| app.click(Target::key(device)));
    world.gesture(|app| app.click(Target::key(guide)));
    assert_eq!(world.opened_links(), [device, guide]);
    assert!(
        !checked(&world, "asks.q.q1.choice.0"),
        "opening a choice's link leaves it unpicked"
    );

    click(&mut world, "asks.q.q1.choice.0");
    click(&mut world, "asks.send");
    assert_eq!(
        sent_answers(&world)[0]["answers"][0]["picked"],
        serde_json::json!(["**Signed in**"])
    );
}

#[test]
fn an_image_in_a_subject_prompt_or_choice_reads_as_its_alt_text_and_only_the_context_shows_a_picture()
 {
    let shot = "![the {name} shot](shots/shot.png)";
    let pictured = |name: &str| shot.replace("{name}", name);
    let mut layout = question(
        "q1",
        "one",
        &format!("Which layout reads easier? {}", pictured("prompt")),
        &[],
    );
    let asked = layout.question.as_mut().unwrap();
    asked.context = Some(format!("Compare: {}", pictured("context")));
    asked.choices = vec![
        AskChoice {
            label: format!("Wide {}", pictured("wide")),
            description: Some(format!("as {} shows", pictured("note"))),
        },
        choice("Narrow"),
    ];
    let mut inline = ask(
        "a-layout",
        "api",
        &format!("Layout {}", pictured("subject")),
        30,
        vec![layout],
    );
    inline.base_dir = "/tmp/api".into();
    let mut world = start(vec![inline], vec![api_working()]);
    world.daemon.on("ask_image", |r| {
        serde_json::json!({
            "event": "ask_image_result",
            "request_id": r["request_id"],
            "success": true,
            "mime_type": "image/png",
            "data_base64": PNG,
        })
    });
    assert_eq!(
        name(&world, "home.ask.a-layout.subject"),
        "Layout the subject shot"
    );
    open_sheet(&mut world);
    world.settle();

    assert_eq!(name(&world, "asks.subject"), "Layout the subject shot");
    let prompt: Vec<String> = world
        .screen()
        .descendants(Target::key("asks.q.q1.prompt"))
        .into_iter()
        .filter_map(|n| n.name.or(n.value))
        .collect();
    assert!(
        prompt
            .iter()
            .any(|t| t == "Which layout reads easier? the prompt shot"),
        "the prompt reads its picture's alt text: {prompt:?}"
    );
    assert_eq!(name(&world, "asks.q.q1.choice.0"), "Wide the wide shot");
    let pictures: Vec<String> = world
        .screen()
        .descendants(Target::key("asks.ask"))
        .into_iter()
        .filter(|n| n.role == "image")
        .filter_map(|n| n.name)
        .collect();
    assert_eq!(
        pictures,
        ["the context shot"],
        "only the context draws its picture"
    );

    scroll_to(&mut world, "asks.q.q1.choice.0");
    click(&mut world, "asks.q.q1.choice.0");
    assert!(checked(&world, "asks.q.q1.choice.0"));
    assert!(
        sent_answers(&world).is_empty(),
        "a click on the choice picks it and sends nothing"
    );
}

const NOTES: &str = "https://example.com/notes";
const MIGRATION_LINK: &str = "attn-seed:s-cache1";

fn start_on_links() -> World {
    let mut world = start_with_seeds(
        vec![ask(
            "a-links",
            "api",
            "Links",
            30,
            vec![
                prose(&format!(
                    "Read [the notes]({NOTES}) before cache-migration lands."
                )),
                question("q1", "text", "Ready?", &[]),
            ],
        )],
        vec![api_working()],
        vec![migration(None)],
    );
    mentions_answered(&world, &[("cache-migration", "s-cache1")]);
    open_sheet(&mut world);
    world
}

/// Drags from the first letter of link `link` to past the end of the text `holder`.
fn drag_from_link(world: &mut World, link: &str, holder: &str) {
    let [x, y, _, h] = world.rect(link);
    let [hx, _, hw, _] = world.rect(holder);
    let line = (y + h / 2.0) as f32;
    let (from, to) = ((x as f32 + 1.0, line), ((hx + hw) as f32 + 4.0, line));
    world.gesture(|app| {
        app.drag_from(from, to);
        app.release(to);
    });
    world.settle();
}

#[test]
fn a_drag_that_starts_on_a_link_selects_and_the_copy_key_copies_it_though_the_note_held_the_keyboard()
 {
    for (link, rest) in [
        (NOTES, "the notes before cache-migration lands."),
        (MIGRATION_LINK, "cache-migration lands."),
    ] {
        let mut world = start_on_links();
        add_note(&mut world);
        type_into(&mut world, NOTE, "draft");
        assert_eq!(focused(&world).as_deref(), Some(NOTE));

        drag_from_link(&mut world, link, "asks.part.0");

        assert_eq!(copied(&mut world).as_deref(), Some(rest), "from {link}");
        assert!(world.opened_links().is_empty(), "a drag opens nothing");
        assert_eq!(garden_heading(&world), None, "a drag opens no seed");
    }
}

#[test]
fn a_click_on_a_link_in_an_asks_words_still_opens_it() {
    let mut world = start_on_links();
    add_note(&mut world);
    type_into(&mut world, NOTE, "draft");

    world.gesture(|app| app.click(Target::key(NOTES)));
    assert_eq!(world.opened_links(), [NOTES]);

    world.gesture(|app| app.click(Target::key(MIGRATION_LINK)));
    assert_eq!(
        garden_heading(&world).as_deref(),
        Some("Cache store migration")
    );
}

/// Three questions, one of each kind the number keys and ⇥ treat differently: one choice, any
/// choices and a written answer with a suggestion.
fn release_plan() -> Ask {
    ask(
        "a-plan",
        "api",
        "Release plan",
        28,
        vec![
            prose("The load test found a regression in the cache warmup."),
            question("q1", "one", "Which region first?", &["eu", "us"]),
            question("q2", "any", "Which checks?", &["smoke", "load", "soak"]),
            recommending(question("q3", "text", "Which date?", &[]), "Monday"),
        ],
    )
}

fn active(world: &World) -> Vec<String> {
    ["q1", "q2", "q3"]
        .into_iter()
        .filter(|q| {
            world
                .screen()
                .get(Target::key(format!("asks.q.{q}")))
                .value
                .as_deref()
                == Some("active")
        })
        .map(str::to_owned)
        .collect()
}

pub(super) fn field_text(world: &World, key: &str) -> String {
    world
        .screen()
        .get(Target::key(key))
        .value
        .unwrap_or_default()
}

#[test]
fn number_keys_pick_in_the_active_question_and_tab_moves_it_to_the_written_field() {
    let mut world = start(vec![release_plan()], vec![api_working()]);
    open_sheet(&mut world);
    assert_eq!(active(&world), ["q1"]);
    let room = |world: &mut World| {
        let rows: Vec<_> = (0..2)
            .map(|i| world.rect(&format!("asks.q.q1.choice.{i}")))
            .collect();
        rows.iter()
            .map(|r| [r[0], r[1] - rows[0][1], r[2], r[3]])
            .collect::<Vec<_>>()
    };
    let rows_before = room(&mut world);

    letter(&mut world, "1");
    assert!(checked(&world, "asks.q.q1.choice.0"), "1 picks eu");
    assert_eq!(active(&world), ["q2"], "a one-choice pick moves on");
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q2.choice.0"));
    assert_eq!(rows_before, room(&mut world), "the keycaps keep their room");

    rest_but_for_ages(&mut world);
    letter(&mut world, "2");
    letter(&mut world, "3");
    assert!(checked(&world, "asks.q.q2.choice.1"), "2 toggles load");
    assert!(checked(&world, "asks.q.q2.choice.2"), "3 toggles soak");
    assert!(!checked(&world, "asks.q.q2.choice.0"));
    assert_eq!(active(&world), ["q2"], "a pick of any keeps the question");

    letter(&mut world, "4");
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q2.other.field"));
    world.gesture(|app| app.type_text("4 nodes"));
    assert_eq!(field_text(&world, "asks.q.q2.other.field"), "4 nodes");

    plain(&mut world, Key::Tab);
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q3.text.field"));
    assert_eq!(active(&world), ["q3"]);
    world.gesture(|app| app.type_text("12 May"));
    assert_eq!(field_text(&world, "asks.q.q3.text.field"), "12 May");

    world.gesture(|app| app.press("shift+tab"));
    assert_eq!(active(&world), ["q2"]);
    assert_eq!(
        focused(&world).as_deref(),
        Some("asks.q.q2.condition"),
        "going back enters a question at its last control"
    );

    send_key(&mut world);
    assert_eq!(
        sent_answers(&world),
        [serde_json::json!({
            "ask_id": "a-plan",
            "answers": [
                {"key": "q1", "picked": ["eu"]},
                {"key": "q2", "picked": ["load", "soak"], "other": "4 nodes"},
                {"key": "q3", "picked": [], "text": "12 May"},
            ],
        })]
    );
}

#[test]
fn tab_walks_the_questions_and_the_note_then_the_sheets_controls_and_never_the_list_of_asks() {
    let mut world = start(
        vec![release_plan(), release_day()],
        vec![api_working(), web_waiting()],
    );
    open_sheet(&mut world);
    click(&mut world, "asks.row.a-plan");
    let mut walk = vec![focused(&world).unwrap_or_default()];
    for _ in 0..8 {
        plain(&mut world, Key::Tab);
        walk.push(focused(&world).unwrap_or_default());
    }
    assert_eq!(
        walk,
        [
            "asks.q.q1.choice.0",
            "asks.q.q2.choice.0",
            "asks.q.q3.text.field",
            "asks.q.q3.suggestion",
            "asks.note.add",
            "asks.attach",
            "asks.keys",
            "asks.replied",
            "asks.close",
        ]
    );
    let mut back = Vec::new();
    for _ in 0..10 {
        world.gesture(|d| d.press("shift+tab"));
        back.push(focused(&world).unwrap_or_default());
    }
    assert_eq!(
        back,
        [
            "asks.replied",
            "asks.keys",
            "asks.attach",
            "asks.note.add",
            "asks.q.q3.suggestion",
            "asks.q.q3.text.field",
            "asks.q.q2.choice.0",
            "asks.q.q1.choice.0",
            "asks.snooze",
            "asks.open-agent",
        ]
    );
}

#[test]
fn a_click_in_a_question_makes_it_the_one_the_number_keys_pick_in() {
    let mut world = start(vec![release_plan()], vec![api_working()]);
    world.resize((1000.0, 1000.0));
    open_sheet(&mut world);
    click(&mut world, "asks.q.q3.prompt");
    assert_eq!(active(&world), ["q3"]);

    click(&mut world, "asks.q.q1.choice.1");
    assert!(checked(&world, "asks.q.q1.choice.1"));
    assert_eq!(active(&world), ["q2"], "a clicked one-choice pick moves on");

    letter(&mut world, "1");
    assert!(checked(&world, "asks.q.q2.choice.0"), "1 picks smoke");
    assert!(!checked(&world, "asks.q.q1.choice.0"));
}

#[test]
fn the_keys_button_and_question_mark_list_the_number_keys_tab_and_quote() {
    let mut world = start(vec![release_plan()], vec![api_working()]);
    open_sheet(&mut world);
    assert!(
        !has(&world, "asks.hints"),
        "the keys wait behind the Keys button"
    );
    click(&mut world, "asks.keys");
    assert!(has(&world, "asks.hints"));
    click(&mut world, "asks.keys");
    assert!(!has(&world, "asks.hints"));
    letter(&mut world, "?");
    let hints = name(&world, "asks.hints");
    assert!(hints.contains("1–2 pick"), "{hints}");
    assert!(hints.contains("⇥ next question"), "{hints}");
    assert!(hints.contains("q quote"), "{hints}");
    assert!(hints.contains("↑ ↓ space move · pick"), "{hints}");
    letter(&mut world, "1");
    assert!(name(&world, "asks.hints").contains("1–3 pick"));
}

#[test]
fn q_quotes_the_selected_passage_into_the_note_and_puts_the_keyboard_there() {
    let mut world = start(vec![release_plan()], vec![api_working()]);
    world.resize((1000.0, 1000.0));
    open_sheet(&mut world);
    add_note(&mut world);
    type_into(&mut world, NOTE, "Looks fine.");
    drag_across(&mut world, "asks.part.0");

    letter(&mut world, "q");

    assert_eq!(
        field_text(&world, NOTE),
        "Looks fine.\n\n> The load test found a regression in the cache warmup.\n"
    );
    assert_eq!(focused(&world).as_deref(), Some(NOTE));
    world.gesture(|app| app.type_text("Which one?"));
    assert!(field_text(&world, NOTE).ends_with("warmup.\nWhich one?"));
}

#[test]
fn clearing_a_quoted_note_keeps_it_open_with_the_keyboard_in_it() {
    let mut world = start(vec![release_plan()], vec![api_working()]);
    world.resize((1000.0, 1000.0));
    open_sheet(&mut world);
    drag_across(&mut world, "asks.subject");
    letter(&mut world, "q");

    world.gesture(|app| app.press("mod+a"));
    world.gesture(|app| app.press("backspace"));
    world.gesture(|app| app.type_text("Ship next week."));

    assert_eq!(focused(&world).as_deref(), Some(NOTE));
    assert_eq!(field_text(&world, NOTE), "Ship next week.");
}

#[test]
fn a_note_stays_open_on_its_ask_while_another_asks_note_is_written() {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );
    world.resize((1000.0, 1000.0));
    open_sheet(&mut world);
    assert_eq!(shown(&world).as_deref(), Some("Release day"));
    drag_across(&mut world, "asks.subject");
    letter(&mut world, "q");
    click(&mut world, "asks.row.a-cache1");
    add_note(&mut world);
    world.gesture(|app| app.type_text("Fine by me."));
    click(&mut world, "asks.row.a-release");
    assert_eq!(shown(&world).as_deref(), Some("Release day"));

    world.gesture(|app| app.press("mod+a"));
    world.gesture(|app| app.press("backspace"));
    world.gesture(|app| app.type_text("Ship next week."));

    assert_eq!(focused(&world).as_deref(), Some(NOTE));
    assert_eq!(field_text(&world, NOTE), "Ship next week.");
}

#[test]
fn the_quote_button_floats_over_a_selection_and_quotes_it() {
    let mut world = start(vec![release_plan()], vec![api_working()]);
    open_sheet(&mut world);
    assert!(!has(&world, "asks.quote"));
    drag_across(&mut world, "asks.subject");
    assert!(has(&world, "asks.quote"), "a selection brings the button");
    rest_but_for_ages(&mut world);
    let [_, by, _, bh] = world.rect("asks.quote");
    let [_, sy, _, _] = world.rect("asks.subject");
    assert!(by + bh <= sy, "the button floats above the selection");

    click(&mut world, "asks.quote");

    assert_eq!(field_text(&world, NOTE), "> Release plan\n");
    assert_eq!(focused(&world).as_deref(), Some(NOTE));
    assert!(!has(&world, "asks.quote"), "quoting lets the selection go");
}

#[test]
fn a_replied_ask_shows_no_quote_button_and_q_does_nothing() {
    let answered = replied(release_plan(), vec![picked("q1", &["eu"])], None);
    let mut world = start(vec![answered], vec![api_working()]);
    open_sheet(&mut world);
    letter(&mut world, "r");
    drag_across(&mut world, "asks.subject");
    assert!(!has(&world, "asks.quote"));
    letter(&mut world, "q");
    assert!(!has(&world, NOTE));
}

#[test]
fn the_open_count_follows_the_title_and_comes_before_the_replied_toggle() {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );
    open_sheet(&mut world);
    let header = world.rect("asks.rail.header");
    let count = world.rect("asks.count");
    let replied = world.rect("asks.replied");
    let title_room = count[0] - header[0];
    assert!(
        title_room < header[2] / 3.0,
        "the count stands {title_room} into the {}-point header, away from its title",
        header[2]
    );
    assert!(
        count[0] + count[2] < replied[0],
        "the count {count:?} does not come before Replied {replied:?}"
    );
}

#[test]
fn in_a_narrow_window_the_list_of_asks_sets_its_words_on_the_line_of_the_ask_under_it() {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );
    world.resize((700.0, 800.0));
    open_sheet(&mut world);
    let id = rows(&world).into_iter().next().expect("an ask is listed");
    let title = world.rect(&format!("asks.row.{id}.title"));
    let subject = world.rect("asks.subject.block");
    assert!(
        (title[0] - subject[0]).abs() <= 0.5,
        "the row's words start at {}, the ask's subject at {}",
        title[0],
        subject[0]
    );
}

#[test]
fn the_replied_toggle_reads_unchecked_until_it_shows_the_replied_asks() {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );
    open_sheet(&mut world);
    let replied = |world: &World| world.screen().get(Target::key("asks.replied"));
    assert!(
        replied(&world).is("unchecked"),
        "{:?}",
        replied(&world).states
    );
    world.gesture(|app| app.click(Target::key("asks.replied")));
    assert!(
        replied(&world).is("checked"),
        "{:?}",
        replied(&world).states
    );
}

#[test]
fn a_click_on_the_title_bar_s_asks_button_closes_the_open_sheet_as_a_click_outside() {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );
    let button = middle(&world, "titlebar.asks");
    world.gesture(|app| app.click_at(button));
    assert!(has(&world, "asks.sheet"));

    world.gesture(|app| app.click_at(button));
    assert!(!has(&world, "asks.sheet"));
}

#[test]
fn a_choice_s_line_under_its_name_is_its_description() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    open_sheet(&mut world);
    let postgres = world.screen().get(Target::key("asks.q.q1.choice.0"));
    assert_eq!(postgres.description.as_deref(), Some("already deployed"));
}

#[test]
fn in_the_smallest_window_the_list_gives_way_so_the_ask_has_room_to_read_and_its_note_s_grip_shows()
{
    let mut world = start(
        vec![rollout(), cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );
    world.resize((533.0, 400.0));
    open_sheet(&mut world);
    const MAIN_S_ROOM: f64 = 85.0;
    let ask = world.rect("asks.ask");
    assert!(
        ask[3] >= MAIN_S_ROOM,
        "the ask has {} points to show itself",
        ask[3]
    );
    add_note(&mut world);
    scroll_to(&mut world, "asks.note.field-grip");
    let grip = world.rect("asks.note.field-grip");
    assert!(
        grip[1] >= ask[1] && grip[1] + grip[3] <= ask[1] + ask[3],
        "the grip {grip:?} is outside the ask's view {ask:?}"
    );
}

#[test]
fn an_assistive_scroll_moves_the_list_of_asks_when_it_overflows_as_on_main() {
    let asks = (0..10)
        .map(|i| {
            let mut a = cache_store();
            a.id = format!("a-many{i}");
            a.created_at = ago(30 + i);
            a
        })
        .collect();
    let mut world = start(asks, vec![api_working()]);
    world.resize((533.0, 400.0));
    open_sheet(&mut world);
    world.gesture(|d| {
        assert!(
            d.runtime
                .accessibility_action("asks.list", rune::accesskit::Action::ScrollDown, None),
            "asks.list does not scroll down"
        );
    });
}

#[test]
fn the_other_answer_sits_in_its_question_s_group_of_answers_as_on_main() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    open_sheet(&mut world);
    let answers = world.screen().descendants(Target::key("asks.q.q1.choices"));
    assert!(
        answers
            .iter()
            .any(|n| n.key.as_deref() == Some("asks.q.q1.other.field")),
        "the Other field is outside the question's answers"
    );
}

fn release_announcement() -> Ask {
    ask(
        "a-text",
        "web",
        "Release announcement",
        45,
        vec![
            prose(
                "The nightly export has finished. We can send the report after the follow-up check.",
            ),
            question(
                "q1",
                "text",
                "What should the release announcement say?",
                &[],
            ),
        ],
    )
}

fn start_large_text(size: (f32, f32), theme: &str) -> World {
    let mut state = one_desktop(vec![web_waiting()]);
    state.asks = Some(vec![release_announcement()]);
    state.settings = Some(BTreeMap::from([
        ("uiScale".to_owned(), "1.5".to_owned()),
        ("theme".to_owned(), theme.to_owned()),
    ]));
    World::start_sized(super::palette::daemon(state, vec![]), size)
}

/// The focused answer's whole line and its painted ring are inside the ask's view.
fn assert_focused_answer_in_view(world: &World, case: &str) {
    assert_eq!(
        focused(world).as_deref(),
        Some("asks.q.q1.text.field"),
        "{case}"
    );
    if let Err(why) = super::reach::focus_shows_whole(
        world,
        "asks.ask",
        super::reach::Ring::Drawn {
            dark: !case.contains("light"),
        },
    ) {
        panic!("{case}: {why}");
    }
}

const SMALLEST: (f32, f32) = (800.0, 600.0);

#[test]
fn reopened_on_a_draft_after_the_window_shrank_the_focused_answer_shows_whole() {
    for theme in ["dark", "light"] {
        let mut world = start_large_text((1050.0, 780.0), theme);
        open_sheet(&mut world);
        assert_eq!(focused(&world).as_deref(), Some("asks.q.q1.text.field"));
        world.gesture(|app| app.type_text("Response text."));
        plain(&mut world, Key::Escape);
        assert!(world.screen().find(Target::key("asks.sheet")).is_none());
        world.resize(SMALLEST);
        open_sheet(&mut world);
        assert_eq!(
            world
                .screen()
                .get(Target::key("asks.q.q1.text.field"))
                .value
                .as_deref(),
            Some("Response text.")
        );
        assert_focused_answer_in_view(&world, &format!("reopened, {theme}"));
    }
}

#[test]
fn opened_in_the_smallest_window_at_large_text_the_focused_answer_shows_whole() {
    for theme in ["dark", "light"] {
        let mut world = start_large_text(SMALLEST, theme);
        open_sheet(&mut world);
        assert_focused_answer_in_view(&world, &format!("opened, {theme}"));
    }
}

#[test]
fn the_sheet_keeps_its_ask_when_it_closes_and_its_key_and_escape_both_close_it() {
    let mut world = start(
        vec![cache_store(), release_day()],
        vec![api_working(), web_waiting()],
    );
    open_sheet(&mut world);
    letter(&mut world, "j");
    assert_eq!(shown(&world).as_deref(), Some("Cache store"));

    plain(&mut world, Key::Escape);
    assert!(!has(&world, "asks.sheet"));
    open_sheet(&mut world);
    assert_eq!(shown(&world).as_deref(), Some("Cache store"));

    open_sheet(&mut world);
    assert!(!has(&world, "asks.sheet"));
    open_sheet(&mut world);
    assert_eq!(shown(&world).as_deref(), Some("Cache store"));
}

#[test]
fn the_sheet_key_and_open_asks_open_on_the_agent_on_screens_ask_the_shown_one_when_it_is_that_agents_else_its_oldest()
 {
    let mut world = start(
        vec![cache_store(), rollout(), release_day()],
        vec![api_working(), web_waiting()],
    );
    world.open_desktop();
    open_sheet(&mut world);
    assert_eq!(rows(&world), ["a-release", "a-cache1", "a-rollout"]);
    assert_eq!(shown(&world).as_deref(), Some("Cache store"));

    click(&mut world, "asks.row.a-rollout");
    plain(&mut world, Key::Escape);
    open_sheet(&mut world);
    assert_eq!(shown(&world).as_deref(), Some("Rollout"));

    click(&mut world, "asks.row.a-release");
    plain(&mut world, Key::Escape);
    open_sheet(&mut world);
    assert_eq!(shown(&world).as_deref(), Some("Cache store"));

    click(&mut world, "asks.row.a-release");
    plain(&mut world, Key::Escape);
    run_open_asks(&mut world);
    assert_eq!(shown(&world).as_deref(), Some("Cache store"));
}

#[test]
fn with_no_agent_on_screen_the_sheet_key_opens_on_the_ask_shown_last_or_the_first_once_it_is_gone()
{
    let mut world = start(
        vec![cache_store(), rollout(), release_day()],
        vec![api_working(), web_waiting()],
    );
    open_sheet(&mut world);
    assert_eq!(shown(&world).as_deref(), Some("Release day"));

    click(&mut world, "asks.row.a-rollout");
    plain(&mut world, Key::Escape);
    open_sheet(&mut world);
    assert_eq!(shown(&world).as_deref(), Some("Rollout"));

    plain(&mut world, Key::Escape);
    update_asks(&mut world, vec![cache_store(), release_day()]);
    open_sheet(&mut world);
    assert_eq!(shown(&world).as_deref(), Some("Release day"));
}

#[test]
fn the_sheet_is_centred_under_the_title_bar_with_its_list_beside_the_ask_read_in_a_centred_column()
{
    for (window, sheet, lines, words) in [
        ((1512.0, 945.0), [48.0, 84.0, 1416.0, 817.0], 620.0, 980.0),
        (
            (3440.0, 1400.0),
            [1000.0, 84.0, 1440.0, 1272.0],
            620.0,
            980.0,
        ),
        ((800.0, 600.0), [48.0, 80.0, 704.0, 480.0], 400.0, 400.0),
    ] {
        let mut world = start(vec![cache_store()], vec![api_working()]);
        world.resize(window);
        open_sheet(&mut world);
        assert_eq!(world.rect("asks.sheet"), sheet, "in a {window:?} window");
        let rail = world.rect("asks.rail");
        let reading = world.rect("asks.reading");
        assert_eq!(rail[2], 264.0, "the rail in a {window:?} window");
        assert_eq!(reading[0], rail[0] + rail[2], "the ask beside the rail");
        assert_eq!(
            reading[0] + reading[2],
            sheet[0] + sheet[2],
            "the ask to the edge"
        );
        let middle = |r: [f64; 4]| r[0] + r[2] / 2.0;
        let ask = world.rect("asks.ask");
        let subject = world.rect("asks.subject.block");
        let prose = world.rect("asks.part.0.block");
        assert_eq!(subject[2], lines, "the ask's lines in a {window:?} window");
        assert_eq!(prose[2], words, "the room its words and pictures take");
        assert_eq!(
            middle(subject),
            middle(prose),
            "its lines and pictures share a middle"
        );
        if lines < words {
            assert_eq!(middle(subject), middle(ask), "the ask's lines centred");
        }
    }
}

#[test]
fn a_condition_added_under_a_pick_is_sent_with_it_and_shown_once_replied() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    open_sheet(&mut world);

    assert!(!has(&world, "asks.q.q1.condition"));
    click(&mut world, "asks.q.q1.choice.0");
    click(&mut world, "asks.q.q1.condition");
    assert_eq!(
        focused(&world).as_deref(),
        Some("asks.q.q1.condition.field")
    );
    assert_eq!(
        name(&world, "asks.q.q1.condition.field"),
        "Condition on Postgres"
    );
    type_into(
        &mut world,
        "asks.q.q1.condition.field",
        "skip the Windows VM step",
    );
    click(&mut world, "asks.send");

    assert_eq!(
        sent_answers(&world)[0]["answers"],
        serde_json::json!([{"key": "q1", "picked": ["Postgres"], "condition": "skip the Windows VM step"}])
    );

    let answered = AskAnswer {
        condition: Some("skip the Windows VM step".into()),
        ..picked("q1", &["Postgres"])
    };
    update_asks(
        &mut world,
        vec![replied(cache_store(), vec![answered], None)],
    );
    letter(&mut world, "r");
    assert_eq!(
        world
            .screen()
            .get(Target::key("asks.q.q1.condition"))
            .value
            .as_deref(),
        Some("Condition: skip the Windows VM step")
    );
}

#[test]
fn a_new_pick_in_a_one_choice_question_drops_the_condition_written_for_the_old_one() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    open_sheet(&mut world);

    click(&mut world, "asks.q.q1.choice.0");
    click(&mut world, "asks.q.q1.condition");
    type_into(&mut world, "asks.q.q1.condition.field", "only after VACUUM");
    click(&mut world, "asks.q.q1.choice.1");

    assert!(!has(&world, "asks.q.q1.condition.field"));
    assert!(has(&world, "asks.q.q1.condition"));
    click(&mut world, "asks.send");
    assert_eq!(
        sent_answers(&world)[0]["answers"],
        serde_json::json!([{"key": "q1", "picked": ["Redis"]}])
    );
}

#[test]
fn a_number_key_pick_and_a_typed_answer_each_drop_the_condition() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    open_sheet(&mut world);

    letter(&mut world, "1");
    click(&mut world, "asks.q.q1.condition");
    type_into(&mut world, "asks.q.q1.condition.field", "only after VACUUM");
    click(&mut world, "asks.q.q1.choice.0");
    assert!(
        has(&world, "asks.q.q1.condition.field"),
        "the same pick keeps it"
    );
    letter(&mut world, "2");
    assert!(checked(&world, "asks.q.q1.choice.1"), "2 picks Redis");
    assert!(!has(&world, "asks.q.q1.condition.field"));
    assert!(has(&world, "asks.q.q1.condition"));

    click(&mut world, "asks.q.q1.condition");
    type_into(&mut world, "asks.q.q1.condition.field", "only today");
    type_into(&mut world, "asks.q.q1.other.field", "SQLite");
    click(&mut world, "asks.q.q1.choice.0");
    assert!(!has(&world, "asks.q.q1.condition.field"));
    click(&mut world, "asks.send");
    assert_eq!(
        sent_answers(&world)[0]["answers"],
        serde_json::json!([{"key": "q1", "picked": ["Postgres"]}])
    );
}

#[test]
fn a_multi_choice_condition_stays_while_any_pick_remains_and_goes_with_the_last() {
    let mut world = start(vec![release_day()], vec![api_working()]);
    open_sheet(&mut world);

    click(&mut world, "asks.q.q1.choice.0");
    click(&mut world, "asks.q.q1.choice.1");
    click(&mut world, "asks.q.q1.condition");
    assert_eq!(
        name(&world, "asks.q.q1.condition.field"),
        "Condition on macOS, Linux"
    );
    type_into(&mut world, "asks.q.q1.condition.field", "after the freeze");

    click(&mut world, "asks.q.q1.choice.1");
    assert_eq!(
        name(&world, "asks.q.q1.condition.field"),
        "Condition on macOS"
    );
    click(&mut world, "asks.q.q1.choice.0");
    assert!(!has(&world, "asks.q.q1.condition.field"));

    click(&mut world, "asks.q.q1.choice.1");
    assert!(!has(&world, "asks.q.q1.condition.field"));
    click(&mut world, "asks.send");
    assert_eq!(
        sent_answers(&world)[0]["answers"],
        serde_json::json!([{"key": "q1", "picked": ["Linux"]}])
    );
}

#[test]
fn accept_all_recommended_keeps_a_multi_choice_condition_and_drops_a_one_choice_one_for_a_new_pick()
{
    let mut world = start(vec![rollout()], vec![api_working()]);
    open_sheet(&mut world);

    click(&mut world, "asks.q.q1.choice.1");
    click(&mut world, "asks.q.q1.condition");
    type_into(&mut world, "asks.q.q1.condition.field", "only in the day");
    click(&mut world, "asks.q.q2.choice.0");
    click(&mut world, "asks.q.q2.choice.1");
    scroll_to(&mut world, "asks.q.q2.condition");
    click(&mut world, "asks.q.q2.condition");
    type_into(&mut world, "asks.q.q2.condition.field", "after the freeze");
    click(&mut world, "asks.accept");

    assert!(checked(&world, "asks.q.q2.choice.0"));
    assert!(!checked(&world, "asks.q.q2.choice.1"));
    assert!(has(&world, "asks.q.q2.condition.field"));
    assert!(checked(&world, "asks.q.q1.choice.0"));
    assert!(!has(&world, "asks.q.q1.condition.field"));
    click(&mut world, "asks.send");
    assert_eq!(
        sent_answers(&world)[0]["answers"],
        serde_json::json!([
            {"key": "q1", "picked": ["eu"]},
            {"key": "q2", "picked": ["smoke"], "condition": "after the freeze"},
            {"key": "q3", "picked": [], "text": "Monday"},
        ])
    );
}

#[test]
fn typing_another_answer_drops_the_pick_and_its_condition() {
    let mut world = start(vec![cache_store()], vec![api_working()]);
    open_sheet(&mut world);

    click(&mut world, "asks.q.q1.choice.0");
    click(&mut world, "asks.q.q1.condition");
    type_into(&mut world, "asks.q.q1.condition.field", "only today");
    type_into(&mut world, "asks.q.q1.other.field", "SQLite");

    assert!(!has(&world, "asks.q.q1.condition.field"));
    click(&mut world, "asks.send");
    assert_eq!(
        sent_answers(&world)[0]["answers"],
        serde_json::json!([{"key": "q1", "picked": [], "other": "SQLite"}])
    );
}

#[test]
fn a_follow_up_ask_says_what_it_follows_and_unfolds_to_the_earlier_answer() {
    let mut follow_up = ask(
        "a-follow",
        "web",
        "Postgres version",
        5,
        vec![question("q1", "one", "Which version?", &["16", "17"])],
    );
    follow_up.re = Some(AskThread {
        ask_id: "a-cache1".into(),
        subject: Some("Cache store".into()),
        answer: Some(
            "**1. Which store should keep the cache?**\n\nAnswer: Postgres (the recommended choice)\n\n> Keep it boring.".into(),
        ),
        answered_at: Some(ago(10)),
    });
    let mut after_gone = ask(
        "a-after",
        "web",
        "Pool size",
        4,
        vec![question("q1", "one", "How many?", &["10", "20"])],
    );
    after_gone.re = Some(AskThread {
        ask_id: "a-gone".into(),
        ..AskThread::default()
    });
    let mut world = start(vec![follow_up, after_gone], vec![web_waiting()]);
    open_sheet(&mut world);

    assert_eq!(shown(&world).as_deref(), Some("Postgres version"));
    assert_eq!(
        name(&world, "asks.follows"),
        "Follows Cache store · you answered 10m ago"
    );
    assert!(!has(&world, "asks.follows.answer"));
    click(&mut world, "asks.follows");
    let answer: Vec<String> = world
        .screen()
        .descendants(Target::key("asks.follows.answer"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(
        answer
            .iter()
            .any(|said| said.contains("Answer: Postgres (the recommended choice)")),
        "{answer:?}"
    );
    click(&mut world, "asks.follows");
    assert!(!has(&world, "asks.follows.answer"));

    letter(&mut world, "j");
    assert_eq!(shown(&world).as_deref(), Some("Pool size"));
    assert_eq!(name(&world, "asks.follows"), "Follows a-gone");
    rest_but_for_ages(&mut world);
}

fn long_question(count: usize) -> World {
    let choices: Vec<_> = (1..=count).map(|i| format!("Choice {i}")).collect();
    let labels: Vec<_> = choices.iter().map(String::as_str).collect();
    let plan = ask(
        "a-long",
        "api",
        "Approval",
        28,
        vec![
            question("q1", "any", "Which checks?", &labels),
            question("q2", "one", "Which region?", &["eu", "us"]),
        ],
    );
    start(vec![plan], vec![api_working()])
}

fn down(world: &mut World, times: usize) {
    for _ in 0..times {
        plain(world, Key::Down);
    }
}

#[test]
fn down_reaches_every_choice_of_a_long_question_then_its_condition_and_other_and_space_picks() {
    for full_keyboard_access in [false, true] {
        let mut world = long_question(12);
        world.gesture(|app| app.runtime.set_full_keyboard_access(full_keyboard_access));
        open_sheet(&mut world);
        assert_eq!(focused(&world).as_deref(), Some("asks.q.q1.choice.0"));

        down(&mut world, 11);
        assert_eq!(focused(&world).as_deref(), Some("asks.q.q1.choice.11"));
        assert!(
            !checked(&world, "asks.q.q1.choice.11"),
            "moving picks nothing"
        );
        letter(&mut world, " ");
        assert!(
            checked(&world, "asks.q.q1.choice.11"),
            "space picks choice 12"
        );

        down(&mut world, 1);
        assert_eq!(focused(&world).as_deref(), Some("asks.q.q1.condition"));
        plain(&mut world, Key::Up);
        assert_eq!(focused(&world).as_deref(), Some("asks.q.q1.choice.11"));
        down(&mut world, 1);
        letter(&mut world, " ");
        assert_eq!(
            focused(&world).as_deref(),
            Some("asks.q.q1.condition.field")
        );
        world.gesture(|app| app.type_text("on main"));
        down(&mut world, 1);
        assert_eq!(
            focused(&world).as_deref(),
            Some("asks.q.q1.condition.field"),
            "the field keeps its arrows"
        );

        plain(&mut world, Key::Tab);
        assert_eq!(
            focused(&world).as_deref(),
            Some("asks.q.q1.other.field"),
            "past the condition's field, where ↓ stays, ⇥ reaches Other"
        );
        world.gesture(|app| app.type_text("canary"));
        plain(&mut world, Key::Tab);
        assert_eq!(focused(&world).as_deref(), Some("asks.q.q2.choice.0"));

        send_key(&mut world);
        assert_eq!(
            sent_answers(&world),
            [serde_json::json!({
                "ask_id": "a-long",
                "answers": [
                    {"key": "q1", "picked": ["Choice 12"], "other": "canary", "condition": "on main"},
                ],
            })],
            "case: full keyboard access {full_keyboard_access}"
        );
    }
}

#[test]
fn down_alone_reaches_other_in_a_nine_and_a_twelve_choice_question() {
    for (count, full_keyboard_access) in [(9, false), (9, true), (12, false), (12, true)] {
        let mut world = long_question(count);
        world.gesture(|app| app.runtime.set_full_keyboard_access(full_keyboard_access));
        open_sheet(&mut world);
        down(&mut world, count);
        assert_eq!(
            focused(&world).as_deref(),
            Some("asks.q.q1.other.field"),
            "case: {count} choices, full keyboard access {full_keyboard_access}"
        );
    }
}

#[test]
fn the_arrows_stay_in_a_written_answer_and_walk_the_note_s_controls() {
    let mut world = start(vec![release_plan()], vec![api_working()]);
    open_sheet(&mut world);
    letter(&mut world, "1");
    plain(&mut world, Key::Tab);
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q3.text.field"));
    plain(&mut world, Key::Down);
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q3.text.field"));

    plain(&mut world, Key::Tab);
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q3.suggestion"));
    plain(&mut world, Key::Up);
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q3.text.field"));

    plain(&mut world, Key::Tab);
    plain(&mut world, Key::Tab);
    assert_eq!(focused(&world).as_deref(), Some("asks.note.add"));
    world.gesture(|d| d.press("space"));
    assert_eq!(focused(&world).as_deref(), Some(NOTE));
    world.gesture(|app| app.type_text("thanks"));
    plain(&mut world, Key::Down);
    assert_eq!(focused(&world).as_deref(), Some(NOTE));
    plain(&mut world, Key::Tab);
    assert_eq!(focused(&world).as_deref(), Some("asks.attach"));
    plain(&mut world, Key::Up);
    assert_eq!(focused(&world).as_deref(), Some(NOTE));
}

#[test]
fn the_note_waits_behind_add_a_note_which_n_or_a_press_opens_with_the_keyboard_in_it() {
    let mut world = start(vec![release_plan()], vec![api_working()]);
    world.resize((1000.0, 1000.0));
    open_sheet(&mut world);
    assert!(!has(&world, NOTE), "an empty note waits behind Add a note");
    letter(&mut world, "n");
    assert_eq!(focused(&world).as_deref(), Some(NOTE));
    world.gesture(|app| app.type_text("thanks"));
    click(&mut world, "asks.q.q1.choice.0");
    assert_eq!(
        field_text(&world, NOTE),
        "thanks",
        "a written note stays open"
    );

    let mut world = start(vec![release_plan()], vec![api_working()]);
    open_sheet(&mut world);
    add_note(&mut world);
    assert_eq!(focused(&world).as_deref(), Some(NOTE));
}

#[test]
fn an_ask_with_no_question_shows_its_note_open() {
    let heads_up = ask(
        "a-heads-up",
        "api",
        "Shipping the cache fix",
        3,
        vec![prose("I will ship the cache fix after lunch.")],
    );
    let mut world = start(vec![heads_up], vec![api_working()]);
    open_sheet(&mut world);
    assert!(has(&world, NOTE), "the note is the only way to answer");
    assert!(!has(&world, "asks.note.add"));
}

#[test]
fn tab_reaches_a_picks_condition_inside_its_question() {
    let mut world = start(vec![release_plan()], vec![api_working()]);
    open_sheet(&mut world);
    letter(&mut world, "1");
    assert_eq!(active(&world), ["q2"]);

    world.gesture(|d| d.press("shift+tab"));
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q1.condition"));
    assert_eq!(active(&world), ["q1"]);
    world.gesture(|d| d.press("space"));
    assert_eq!(
        focused(&world).as_deref(),
        Some("asks.q.q1.condition.field")
    );
    world.gesture(|app| app.type_text("only eu-west"));

    plain(&mut world, Key::Tab);
    assert_eq!(
        focused(&world).as_deref(),
        Some("asks.q.q1.other.field"),
        "past the condition's field, where ↓ stays, ⇥ reaches Other"
    );
    plain(&mut world, Key::Tab);
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q2.choice.0"));
    assert_eq!(active(&world), ["q2"]);
    world.gesture(|d| d.press("shift+tab"));
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q1.other.field"));
    assert_eq!(active(&world), ["q1"]);
    world.gesture(|d| d.press("shift+tab"));
    assert_eq!(
        focused(&world).as_deref(),
        Some("asks.q.q1.condition.field")
    );
    world.gesture(|d| d.press("shift+tab"));
    assert_eq!(focused(&world).as_deref(), Some("asks.q.q1.choice.0"));
}

#[test]
fn a_replied_ask_takes_a_follow_up_sent_with_the_send_chord_and_says_who_it_went_to() {
    let answered = replied(cache_store(), vec![picked("q1", &["Postgres"])], None);
    let mut world = start(vec![answered], vec![api_working()]);
    world.daemon.on("ask_follow_up", |r| {
        message(AskFollowUpResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            ask_id: r["ask_id"].as_str().unwrap().into(),
            success: true,
            holder: Some("api".into()),
            ..AskFollowUpResultMessage::new()
        })
    });
    open_sheet(&mut world);
    letter(&mut world, "r");
    assert!(!has(&world, "asks.follow-up.field"));

    letter(&mut world, "f");
    assert_eq!(focused(&world).as_deref(), Some("asks.follow-up.field"));
    assert!(disabled(&world, "asks.follow-up.send"));
    world.gesture(|app| app.type_text("Redis after all: the cache is hot."));
    send_key(&mut world);

    let sent: Vec<_> = world
        .daemon
        .sent_of("ask_follow_up")
        .into_iter()
        .map(|r| (r["ask_id"].clone(), r["text"].clone()))
        .collect();
    assert_eq!(
        sent,
        [(
            serde_json::json!("a-cache1"),
            serde_json::json!("Redis after all: the cache is hot.")
        )]
    );
    assert!(world.daemon.sent_of("ask_answer").is_empty());
    assert!(!has(&world, "asks.follow-up.field"));
    assert!(has(&world, "asks.follow-up"));
    assert_eq!(name(&world, "notice.text"), "Follow-up sent to api");
}

#[test]
fn text_typed_while_a_follow_up_sends_stays_in_the_field_after_the_daemon_takes_it() {
    let answered = replied(cache_store(), vec![picked("q1", &["Postgres"])], None);
    let mut world = start(vec![answered], vec![api_working()]);
    world.daemon.on("ask_follow_up", |_| ());
    open_sheet(&mut world);
    letter(&mut world, "r");
    letter(&mut world, "f");
    world.gesture(|app| app.type_text("Use Redis."));
    send_key(&mut world);
    let sent = world.daemon.sent_of("ask_follow_up");
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["text"], "Use Redis.");

    world.gesture(|app| app.type_text(" After the backup finishes."));
    world.daemon.reply_to(
        &sent[0],
        message(AskFollowUpResultMessage {
            ask_id: "a-cache1".into(),
            success: true,
            holder: Some("api".into()),
            ..AskFollowUpResultMessage::new()
        }),
    );
    world.settle();

    assert_eq!(
        world
            .screen()
            .find(Target::key("asks.follow-up.field"))
            .and_then(|n| n.value)
            .as_deref(),
        Some("Use Redis. After the backup finishes.")
    );
    assert_eq!(focused(&world).as_deref(), Some("asks.follow-up.field"));
    assert_eq!(name(&world, "notice.text"), "Follow-up sent to api");
    assert_eq!(world.daemon.sent_of("ask_follow_up").len(), 1);
}

#[test]
fn a_follow_up_no_session_holds_says_who_it_waits_for() {
    let about_seed = Ask {
        seed_id: Some("s-cache".into()),
        seed_title: Some("Cache migration".into()),
        ..cache_store()
    };
    let from_member = Ask {
        crew_member: Some("ólafur".into()),
        ..cache_store()
    };
    for (ask, says) in [
        (
            about_seed,
            "Follow-up waits for the next tender of Cache migration",
        ),
        (from_member, "Follow-up waits for Ólafur's next session"),
    ] {
        let answered = replied(ask, vec![picked("q1", &["Postgres"])], None);
        let mut world = start(vec![answered], vec![]);
        world.daemon.on("ask_follow_up", |r| {
            message(AskFollowUpResultMessage {
                request_id: r["request_id"].as_str().unwrap().into(),
                ask_id: r["ask_id"].as_str().unwrap().into(),
                success: true,
                ..AskFollowUpResultMessage::new()
            })
        });
        open_sheet(&mut world);
        letter(&mut world, "r");
        letter(&mut world, "f");
        world.gesture(|app| app.type_text("Use Redis."));
        send_key(&mut world);

        assert_eq!(world.daemon.sent_of("ask_follow_up").len(), 1);
        assert!(!has(&world, "asks.follow-up.field"));
        assert_eq!(name(&world, "notice.text"), says);
    }
}

fn stranded_cache_store() -> Ask {
    Ask {
        seed_id: Some("s-cache".into()),
        seed_title: Some("Cache migration".into()),
        stranded: Some(true),
        ..ask(
            "a-cache1",
            "api",
            "Cache store",
            30,
            vec![question(
                "q1",
                "one",
                "Which store?",
                &["Postgres", "Redis"],
            )],
        )
    }
}

fn chief_of(profile: &str) -> CrewMember {
    CrewMember {
        id: "chief".into(),
        name: "Chief".into(),
        profile_id: profile.into(),
        chief: Some(true),
        ..CrewMember::default()
    }
}

fn start_with_crew(asks: Vec<Ask>, crew: Vec<CrewMember>) -> World {
    let mut state = one_desktop(vec![]);
    state.crew = Some(crew);
    start_on(state, asks, &[])
}

#[test]
fn a_stranded_ask_says_no_agent_will_get_the_answer_and_sends_it_to_the_chief() {
    let mut world = start_with_crew(vec![stranded_cache_store()], vec![chief_of("work")]);
    open_sheet(&mut world);

    assert_eq!(name(&world, "asks.from"), "No agent will get this answer");
    assert!(name(&world, "asks.send").starts_with("Send to Chief"));
    click(&mut world, "asks.q.q1.choice.1");
    send_key(&mut world);

    let sent = sent_answers(&world);
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["to_chief"], serde_json::json!(true));
}

#[test]
fn a_stranded_ask_keeps_send_when_the_profile_has_no_chief() {
    for crew in [vec![], vec![chief_of("home")]] {
        let mut world = start_with_crew(vec![stranded_cache_store()], crew);
        open_sheet(&mut world);

        assert_eq!(name(&world, "asks.from"), "No agent will get this answer");
        let send = name(&world, "asks.send");
        assert!(
            send.starts_with("Send ") && !send.contains("Chief"),
            "{send}"
        );
        click(&mut world, "asks.q.q1.choice.1");
        send_key(&mut world);

        let sent = sent_answers(&world);
        assert_eq!(sent.len(), 1);
        assert!(sent[0].get("to_chief").is_none_or(|v| v.is_null()));
    }
}

#[test]
fn a_held_ask_sends_to_its_agent_even_with_a_chief() {
    let held = Ask {
        stranded: None,
        ..stranded_cache_store()
    };
    let mut state = one_desktop(vec![api_working()]);
    state.crew = Some(vec![chief_of("work")]);
    let mut world = start_on(state, vec![held], &[]);
    open_sheet(&mut world);

    assert_ne!(name(&world, "asks.from"), "No agent will get this answer");
    assert!(!name(&world, "asks.send").contains("Chief"));
    click(&mut world, "asks.q.q1.choice.1");
    send_key(&mut world);

    assert!(
        sent_answers(&world)[0]
            .get("to_chief")
            .is_none_or(|v| v.is_null())
    );
}

#[test]
fn a_follow_up_on_a_stranded_replied_ask_goes_to_the_chief() {
    let answered = replied(stranded_cache_store(), vec![picked("q1", &["Redis"])], None);
    let mut world = start_with_crew(vec![answered], vec![chief_of("work")]);
    world.daemon.on("ask_follow_up", |r| {
        message(AskFollowUpResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            ask_id: r["ask_id"].as_str().unwrap().into(),
            success: true,
            ..AskFollowUpResultMessage::new()
        })
    });
    open_sheet(&mut world);
    letter(&mut world, "r");

    assert_eq!(name(&world, "asks.from"), "No agent will get a follow-up");
    letter(&mut world, "f");
    world.gesture(|app| app.type_text("Use Redis."));
    assert!(name(&world, "asks.follow-up.send").starts_with("Send to Chief"));
    send_key(&mut world);

    let sent = world.daemon.sent_of("ask_follow_up");
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["to_chief"], serde_json::json!(true));
    assert_eq!(name(&world, "notice.text"), "Follow-up sent to Chief");
}
