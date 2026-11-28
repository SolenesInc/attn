use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    Desktop, DesktopPane, DesktopTileContentMessage, InitialStateMessage, Profile, Session,
    ViewAnswer, ViewAnswers, ViewDocument, ViewProblem, ViewRunCommand, ViewSent, ViewSource,
    ViewValues,
};
use attn_testkit::tree::{pane, row};
use attn_testkit::{Daemon, Target, WALL_START_MS, World, key_event};
use rune::{Key, KeyEvent, Mods};
use serde_json::{Value, json};

const DESKTOP: &str = "work/desktop_1";
const VIEW: &str = "/work/a/release/view.md";
const EVERY_STATIC_PART: &str =
    include_str!("../../../../../internal/viewlang/testdata/v1/views/every-static-part.json");
const COMMANDS: &str =
    include_str!("../../../../../internal/viewlang/testdata/v1/views/commands.json");
const QUESTION: &str =
    include_str!("../../../../../internal/viewlang/testdata/v1/views/question.json");
const QUESTION_FILE: &str =
    include_str!("../../../../../internal/viewlang/testdata/v1/views/question-file.json");
const CHECK_TRANSCRIPTS: &str =
    include_str!("../../../../../internal/viewlang/testdata/v1/views/check-transcripts.json");
const RESPONSES: &str = "/work/a/release/view.responses.json";

fn start() -> World {
    start_with(None)
}

/// The view tile, opened by agent a.
fn start_opened_by_a() -> World {
    start_with(Some("s-a"))
}

fn start_with(opener: Option<&str>) -> World {
    let mut tile = json!({
        "type": "tile",
        "tile_id": "m",
        "tile_kind": "markdown",
        "tile_params": json!({ "path": VIEW }).to_string(),
    });
    if let Some(opener) = opener {
        tile["tile_session_id"] = json!(opener);
    }
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: DESKTOP.into(),
            ..Profile::default()
        }]),
        sessions: Some(vec![Session {
            id: "s-a".into(),
            label: "Agent a".into(),
            directory: "/work/a".into(),
            profile_id: "work".into(),
            ..Session::default()
        }]),
        desktops: Some(vec![Desktop {
            id: DESKTOP.into(),
            profile_id: "work".into(),
            name: "Desktop 1".into(),
            shortcut_slot: Some(1),
            order_key: "1".into(),
            tree_json: row(vec![pane("a"), tile]).to_string(),
            active_pane_id: "m".into(),
            panes: vec![DesktopPane {
                desktop_id: DESKTOP.into(),
                pane_id: "a".into(),
                runtime_id: "r-a".into(),
                session_id: "s-a".into(),
                kind: "agent".into(),
                ..DesktopPane::default()
            }],
            revision: 1,
            ..Desktop::default()
        }]),
        ..InitialStateMessage::new()
    });
    let mut world = World::start_sized(daemon, (1600.0, 1400.0));
    world.open_desktop();
    world
}

fn release() -> Value {
    json!({
        "ci": {
            "state": "passed",
            "jobs": [
                {"conclusion": "success", "name": "build", "seconds": 161},
                {"conclusion": "failure", "name": "lint", "seconds": 18},
            ],
        },
        "head": {"sha": "0288d47", "subject": "Check a view", "at": "2026-11-28T10:00:00Z"},
        "landed": 3,
        "landings": [{"session": "views", "subject": "Draw a view", "seconds": 3840}],
        "hours": [{"hour": "09", "temp": 3}, {"hour": "10", "temp": 5}, {"hour": "11", "temp": 4}],
        "repositories": [{"name": "attn", "pulls": [{"title": "Fix the sidebar", "checks": "green"}]}],
    })
}

fn takes() -> Value {
    json!([
        {"name": "one", "file": "one.png", "seconds": 12},
        {"name": "two", "file": "two.png", "seconds": 75},
    ])
}

fn source(id: &str, data: bool) -> ViewSource {
    ViewSource {
        id: id.into(),
        kind: "file".into(),
        data,
        state: "ok".into(),
        ..ViewSource::default()
    }
}

/// The example view that uses every static part, as the daemon checks it, with its two files.
fn every_static_part() -> ViewDocument {
    let mut view = ViewDocument {
        language: 1,
        parts: parts_of(EVERY_STATIC_PART),
        ..ViewDocument::default()
    };
    view.sources = vec![source("release.json", true), source("takes.jsonl", false)];
    view.values = vec![
        ViewValues {
            source: "release.json".into(),
            values: release(),
        },
        ViewValues {
            source: "takes.jsonl".into(),
            values: takes(),
        },
    ];
    view
}

fn parts_of(checked: &str) -> Vec<attn_protocol::ViewPart> {
    let checked: Value = serde_json::from_str(checked).unwrap();
    serde_json::from_value(checked["parts"].clone()).unwrap()
}

fn show(world: &mut World, view: ViewDocument) {
    deliver(world, Some(view));
}

fn deliver(world: &mut World, view: Option<ViewDocument>) {
    world.daemon.emit(content(view, None));
    world.settle();
}

fn content(view: Option<ViewDocument>, answers: Option<ViewAnswers>) -> DesktopTileContentMessage {
    DesktopTileContentMessage {
        desktop_id: DESKTOP.into(),
        tile_id: "m".into(),
        tile_kind: "markdown".into(),
        path: VIEW.into(),
        content: "# Release 4.2".into(),
        view,
        answers,
        ..DesktopTileContentMessage::new()
    }
}

/// The home's part in answering `view`: it keeps each answer as a draft and shows the tile
/// again with its drafts, and a send writes the responses and tells the agent. What it keeps.
fn home_keeps_answers(world: &World, view: &ViewDocument) -> Arc<Mutex<ViewAnswers>> {
    let kept = Arc::new(Mutex::new(ViewAnswers::default()));
    let (k, shown) = (kept.clone(), view.clone());
    world.daemon.on("view_answer", move |r| {
        let answer: ViewAnswer = serde_json::from_value(r["answer"].clone()).unwrap();
        let mut kept = k.lock().unwrap();
        kept.drafts
            .retain(|d| (&d.row, &d.part) != (&answer.row, &answer.part));
        if answer.picked.is_some() || answer.text.is_some() || answer.step.is_some() {
            kept.drafts.push(answer);
        }
        let reply =
            json!({"event": "view_answer_result", "request_id": r["request_id"], "success": true});
        vec![
            reply,
            json!(content(Some(shown.clone()), Some(kept.clone()))),
        ]
    });
    let (k, shown) = (kept.clone(), view.clone());
    world.daemon.on("view_send", move |r| {
        let mut kept = k.lock().unwrap();
        let sent = ViewSent {
            at: "2026-11-28T10:00:00Z".into(),
            responses: RESPONSES.into(),
            told: true,
        };
        kept.sent = Some(sent.clone());
        let reply = json!({"event": "view_send_result", "request_id": r["request_id"], "success": true, "sent": sent});
        vec![reply, json!(content(Some(shown.clone()), Some(kept.clone())))]
    });
    kept
}

/// The view that checks three transcripts one by one, then asks the icon's size.
fn three_transcripts() -> ViewDocument {
    let mut parts = parts_of(CHECK_TRANSCRIPTS);
    parts.extend(parts_of(QUESTION));
    ViewDocument {
        language: 1,
        parts,
        sources: vec![source("clips.jsonl", false)],
        values: vec![ViewValues {
            source: "clips.jsonl".into(),
            values: json!([
                {"id": "clip-1", "file": "clip-1.m4a", "transcript": "one said"},
                {"id": "clip-2", "file": "clip-2.m4a", "transcript": "two said"},
                {"id": "clip-3", "file": "clip-3.m4a", "transcript": "three said"},
            ]),
        }],
        ..ViewDocument::default()
    }
}

const STEPS: &str = "tile.m.view.1";
const SECOND_TRANSCRIPT: &str = "tile.m.view.1.1.1.text";

fn given(world: &World) -> Vec<ViewAnswer> {
    (world.daemon.sent_of("view_answer").iter())
        .map(|r| serde_json::from_value(r["answer"].clone()).unwrap())
        .collect()
}

fn focused(world: &World) -> Option<String> {
    world.screen().focused().and_then(|n| n.key)
}

fn step(row: &str, step: &str) -> Value {
    json!({"row": row, "step": step})
}

fn press_action(world: &mut World, action: &Action) {
    let ev = match Keymap::from_setting(None, attn_testkit::platform()).binding(action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    };
    world.gesture(|app| app.key(ev));
    world.settle();
}

/// The text the tile shows, as assistive tech reads it, in reading order.
fn shown(world: &World) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key("tile.m"))
        .into_iter()
        .flat_map(|n| [n.name, n.value])
        .flatten()
        .collect()
}

fn says(world: &World, text: &str) -> bool {
    shown(world).iter().any(|t| t.contains(text))
}

fn press(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
    world.settle();
}

fn typed(world: &mut World, text: &str) {
    world.gesture(|app| app.type_text(text));
    world.settle();
}

/// The key of the element the view's document says is current, which the highlight is on.
fn highlighted(world: &World) -> Option<String> {
    world
        .screen()
        .get(Target::key("tile.m.document"))
        .active_descendant
}

#[test]
fn a_view_draws_every_static_part_from_the_values_of_its_files() {
    let mut world = start();
    show(&mut world, every_static_part());
    let all = shown(&world);
    for drawn in [
        "Release 4.2",
        "passed",
        "Check a view",
        "Landed today",
        "Write the notes",
        "CI on main",
        "build",
        "2m 41s",
        "lint",
        "Draw a view",
        "1h 4m",
        "09: 3",
        "Before",
        "After",
        "Fix the sidebar",
        "1 of 2",
        "Take one runs 12s.",
    ] {
        assert!(
            all.iter().any(|t| t.contains(drawn)),
            "{drawn:?} is not drawn in {all:#?}"
        );
    }
    let refresh = world.screen().get(Target::key("tile.m.view.4.refresh"));
    assert!(refresh.is("disabled"), "{:?}", refresh.states);
    assert!(
        !all.iter().any(|t| t.contains('{')),
        "a field is left unfilled in {all:#?}"
    );
}

#[test]
fn a_part_with_a_problem_shows_it_in_its_place_while_the_others_draw() {
    let mut world = start();
    let mut view = every_static_part();
    let table = view.parts.iter_mut().find(|p| p.name == "table").unwrap();
    let problem = ViewProblem {
        line: table.line,
        text: "The table has 3 headers but 2 cells.".into(),
        fix: "Give each header a cell.".into(),
    };
    table.problem = Some(problem.clone());
    view.problems = vec![problem];
    show(&mut world, view);
    assert!(says(
        &world,
        "Line 33: The table has 3 headers but 2 cells. Give each header a cell."
    ));
    assert!(!says(&world, "Draw a view"));
    assert!(says(&world, "Fix the sidebar") && says(&world, "1 of 2") && says(&world, "build"));
}

#[test]
fn a_file_that_cannot_be_read_says_so_where_its_items_go() {
    let mut world = start();
    let mut view = every_static_part();
    view.values.retain(|v| v.source != "takes.jsonl");
    view.sources[1].state = "failed".into();
    view.sources[1].notice = Some("takes.jsonl: line 2 is not JSON".into());
    show(&mut world, view);
    assert!(says(&world, "takes.jsonl: line 2 is not JSON"));
    assert!(says(&world, "Fix the sidebar"));
}

#[test]
fn keys_move_the_highlight_fold_a_list_and_step_through_one_by_one() {
    let mut world = start();
    home_keeps_answers(&world, &every_static_part());
    show(&mut world, every_static_part());
    const LIST: &str = "tile.m.view.8";

    typed(&mut world, "j");
    assert_eq!(
        highlighted(&world).as_deref(),
        Some(format!("{LIST}.head").as_str())
    );
    press(&mut world, Key::Enter);
    assert!(
        !says(&world, "lint"),
        "the folded list still shows its rows"
    );
    press(&mut world, Key::Enter);
    assert!(says(&world, "lint"));

    typed(&mut world, "j");
    assert_eq!(
        highlighted(&world).as_deref(),
        Some(format!("{LIST}.row.0").as_str())
    );
    press(&mut world, Key::Down);
    assert_eq!(
        highlighted(&world).as_deref(),
        Some(format!("{LIST}.row.1").as_str())
    );
    typed(&mut world, "k");
    assert_eq!(
        highlighted(&world).as_deref(),
        Some(format!("{LIST}.row.0").as_str())
    );

    for _ in 0..6 {
        typed(&mut world, "j");
    }
    assert_eq!(highlighted(&world).as_deref(), Some("tile.m.view.13"));
    press(&mut world, Key::Enter);
    assert!(says(&world, "2 of 2") && says(&world, "Take two runs 1m 15s."));
    press(&mut world, Key::Left);
    assert!(says(&world, "1 of 2 · kept"));
    press(&mut world, Key::Right);
    typed(&mut world, "s");
    assert!(says(&world, "All 2: 1 kept, 0 changed, 1 skipped"));

    world.advance(Duration::from_secs(1));
    let now = world.now().as_secs_f64();
    let wake = world.runtime().wake_deadline().map(|at| at - now);
    assert!(
        !world.runtime().animating && wake.is_some_and(|s| s > 58.0),
        "the view repaints while nothing changes: it wakes in {wake:?} s"
    );
}

#[test]
fn a_views_ages_move_on_while_the_window_is_idle() {
    let mut world = start();
    let mut view = every_static_part();
    view.values[0].values["head"]["at"] = json!((WALL_START_MS - 30_000) / 1000);
    show(&mut world, view);
    assert!(says(&world, "just now"));
    world.advance(Duration::from_secs(61));
    assert!(says(&world, "2m ago"), "{:#?}", shown(&world));
}

#[test]
fn parts_attn_cannot_run_yet_say_so_in_their_place() {
    let mut world = start();
    let mut parts = parts_of(COMMANDS);
    parts.extend(parts_of(QUESTION_FILE));
    let view = ViewDocument {
        language: 1,
        parts,
        ..ViewDocument::default()
    };
    show(&mut world, view);
    for said in [
        "Running a view's own commands is not available yet",
        "Answering \"Which screenshot shows the problem?\" with a file in a view is not available yet",
    ] {
        assert!(
            says(&world, said),
            "{said:?} is not said in {:#?}",
            shown(&world)
        );
    }
}

#[test]
fn the_overview_shows_a_view_tile_by_its_card() {
    let mut world = start();
    show(&mut world, every_static_part());
    press_action(&mut world, &Action::DesktopOverview);
    let screen = world.screen();
    assert!(
        screen
            .find(Target::key("overview.miniature.m.card"))
            .is_some()
    );
    assert!(
        screen
            .find(Target::key("overview.miniature.m.picture"))
            .is_none()
    );
}

#[test]
fn the_find_chord_on_a_view_leaves_no_find_bar_behind() {
    let mut world = start();
    show(&mut world, every_static_part());
    press_action(&mut world, &Action::TerminalFind);
    deliver(&mut world, None);
    assert!(says(&world, "Release 4.2"));
    assert!(
        world
            .screen()
            .find(Target::key("tile.m.document.find"))
            .is_none()
    );
}

#[test]
fn a_view_is_answered_by_keys_alone_and_sent_back_to_the_agent_that_opened_it() {
    let mut world = start_opened_by_a();
    let view = three_transcripts();
    home_keeps_answers(&world, &view);
    show(&mut world, view);

    typed(&mut world, "j");
    assert_eq!(highlighted(&world).as_deref(), Some(STEPS));
    press(&mut world, Key::Enter);
    assert!(says(&world, "2 of 3"), "{:#?}", shown(&world));

    typed(&mut world, "e");
    assert_eq!(focused(&world).as_deref(), Some(SECOND_TRANSCRIPT));
    typed(&mut world, ", corrected");
    press(&mut world, Key::Escape);
    assert_ne!(focused(&world).as_deref(), Some(SECOND_TRANSCRIPT));
    press(&mut world, Key::Enter);
    typed(&mut world, "s");
    assert!(
        says(&world, "All 3: 1 kept, 1 changed, 1 skipped"),
        "{:#?}",
        shown(&world)
    );

    typed(&mut world, "j");
    typed(&mut world, "2");
    press_action(&mut world, &Action::MarkdownSendAnnotations);

    let given: Vec<Value> = given(&world).iter().map(|a| json!(a)).collect();
    let written = given[1]["text"].as_str().unwrap_or_default().to_owned();
    assert!(
        written.contains(", corrected") && written.contains("two said"),
        "{given:#?}"
    );
    assert_eq!(
        given,
        vec![
            step("clip-1", "kept"),
            json!({"row": "clip-2", "part": "transcript", "text": written}),
            step("clip-2", "kept"),
            step("clip-3", "skipped"),
            json!({"part": "size", "picked": ["64 px"]}),
        ]
    );
    assert_eq!(world.daemon.sent_of("view_send").len(), 1);
    assert!(
        says(
            &world,
            &format!("Sent. The agent has your answers in {RESPONSES}.")
        ),
        "{:#?}",
        shown(&world)
    );
}

#[test]
fn sending_while_writing_an_answer_sends_the_text_written() {
    let mut world = start_opened_by_a();
    let view = three_transcripts();
    home_keeps_answers(&world, &view);
    show(&mut world, view);
    typed(&mut world, "j");
    press(&mut world, Key::Enter);
    typed(&mut world, "e");
    typed(&mut world, ", corrected");
    assert_eq!(focused(&world).as_deref(), Some(SECOND_TRANSCRIPT));

    world.gesture(|app| app.click(Target::key("tile.m.view.send.button")));
    world.settle();

    let commands: Vec<String> = (world.daemon.sent().iter())
        .filter(|r| r["cmd"] == "view_answer" || r["cmd"] == "view_send")
        .map(|r| {
            format!(
                "{} {}",
                r["cmd"].as_str().unwrap_or_default(),
                r["answer"]["text"]
            )
        })
        .collect();
    assert_eq!(
        commands,
        vec![
            "view_answer null".to_owned(),
            "view_answer \"two said, corrected\"".to_owned(),
            "view_send null".to_owned(),
        ]
    );
}

#[test]
fn a_view_shows_the_answers_the_home_kept_and_rests_on_the_first_item_left() {
    let mut world = start_opened_by_a();
    let view = three_transcripts();
    world.daemon.emit(content(
        Some(view),
        Some(ViewAnswers {
            drafts: vec![
                serde_json::from_value(step("clip-1", "kept")).unwrap(),
                serde_json::from_value(
                    json!({"row": "clip-2", "part": "transcript", "text": "two, corrected"}),
                )
                .unwrap(),
                serde_json::from_value(json!({"part": "size", "picked": ["48 px"]})).unwrap(),
            ],
            sent: None,
        }),
    ));
    world.settle();

    assert!(says(&world, "2 of 3 · changed"), "{:#?}", shown(&world));
    let field = world.screen().get(Target::key(SECOND_TRANSCRIPT));
    assert_eq!(field.value.as_deref(), Some("two, corrected"));
    let picked = world.screen().get(Target::key("tile.m.view.2.choice.0"));
    assert!(picked.is("checked") || picked.is("selected"), "{picked:#?}");

    typed(&mut world, "j");
    typed(&mut world, "e");
    world.advance(Duration::from_secs(1));
    let now = world.now().as_secs_f64();
    let wake = world.runtime().wake_deadline().map(|at| at - now);
    assert!(
        !world.runtime().animating && wake.is_none_or(|s| s > 58.0),
        "the view repaints while the user writes nothing: it wakes in {wake:?} s"
    );
}

#[test]
fn an_answer_the_home_refuses_says_why_on_the_tile() {
    let mut world = start_opened_by_a();
    world.daemon.on("view_answer", |r| {
        json!({"event": "view_answer_result", "request_id": r["request_id"], "success": false,
            "error": "the view is no longer shown"})
    });
    show(&mut world, three_transcripts());
    typed(&mut world, "j");
    press(&mut world, Key::Enter);
    assert!(
        says(&world, "the view is no longer shown"),
        "{:#?}",
        shown(&world)
    );
}

#[test]
fn a_view_no_agent_opened_has_no_send() {
    let mut world = start();
    show(&mut world, three_transcripts());
    assert!(
        world
            .screen()
            .find(Target::key("tile.m.view.send.button"))
            .is_none()
    );
    press_action(&mut world, &Action::MarkdownSendAnnotations);
    assert!(world.daemon.sent_of("view_send").is_empty());
}

/// The CI view of the `commands` fixture, whose one command stands as `state`, with the values
/// of its last run when it has them.
fn ci_view(state: &str, source: &str, values: Option<Value>) -> ViewDocument {
    ViewDocument {
        language: 1,
        parts: parts_of(COMMANDS),
        sources: vec![ViewSource {
            id: "scripts/main-status.sh".into(),
            kind: "run".into(),
            data: true,
            state: source.into(),
            ..ViewSource::default()
        }],
        values: values
            .map(|values| ViewValues {
                source: "scripts/main-status.sh".into(),
                values,
            })
            .into_iter()
            .collect(),
        commands: vec![ViewRunCommand {
            line: "scripts/main-status.sh".into(),
            every: Some("30s".into()),
            script_sha256: "5e1f".into(),
            state: state.into(),
            change: (state == "changed").then(|| "changed since you started it".into()),
        }],
        ..ViewDocument::default()
    }
}

#[test]
fn a_view_names_its_command_runs_nothing_until_r_and_says_when_its_script_changed() {
    let mut world = start();
    show(&mut world, ci_view("waiting", "waiting", None));
    for said in [
        "This view runs a command on This Mac. It runs once you start it.",
        "scripts/main-status.sh every 30s",
    ] {
        assert!(
            says(&world, said),
            "{said:?} is not said in {:#?}",
            shown(&world)
        );
    }
    assert!(world.daemon.sent_of("view_start").is_empty());

    typed(&mut world, "r");
    let started = world.daemon.sent_of("view_start");
    assert_eq!(started.len(), 1, "{started:?}");
    assert_eq!(
        (&started[0]["desktop_id"], &started[0]["tile_id"]),
        (&json!(DESKTOP), &json!("m"))
    );

    let jobs = json!({"ci": {"jobs": [{"name": "build"}, {"name": "lint"}]}});
    show(&mut world, ci_view("started", "ok", Some(jobs.clone())));
    assert!(says(&world, "lint"));
    assert!(!says(&world, "runs once you start it"));
    typed(&mut world, "r");
    assert_eq!(world.daemon.sent_of("view_start").len(), 1);

    show(&mut world, ci_view("changed", "out_of_date", Some(jobs)));
    for said in [
        "attn stopped running a command that changed. It runs again once you start it.",
        "scripts/main-status.sh changed since you started it",
        "lint",
    ] {
        assert!(
            says(&world, said),
            "{said:?} is not said in {:#?}",
            shown(&world)
        );
    }
    typed(&mut world, "r");
    assert_eq!(world.daemon.sent_of("view_start").len(), 2);
    assert!(world.runtime_is_at_rest());
}
