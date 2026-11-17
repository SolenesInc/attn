use attn_app::Action;
use attn_protocol::{AutomationProvenance, Session};
use attn_testkit::{Daemon, Target, World};

use super::super::palette::{self, run_command};
use super::super::queue::{ago, changes, daemon_shows, one_desktop, queue, sent_ids, turn};
use super::super::sidebar::{chord, session};
use super::{click, has, is};

fn run(id: &str, run_id: &str, owed_minutes_ago: Option<i64>) -> Session {
    let base = match owed_minutes_ago {
        Some(minutes) => turn(id, minutes),
        None => session(id, "working"),
    };
    Session {
        automation: Some(AutomationProvenance {
            definition_id: 1,
            definition_name: "Review".into(),
            run_id: run_id.into(),
            trigger_type: "manual".into(),
            ..AutomationProvenance::default()
        }),
        ..base
    }
}

fn superseded(mut session: Session) -> Session {
    if let Some(automation) = &mut session.automation {
        automation.superseded = Some(true);
    }
    session
}

fn value(world: &World, key: &str) -> String {
    world
        .screen()
        .get(Target::key(key))
        .value
        .unwrap_or_default()
}

fn next_run(world: &mut World) {
    world.gesture(|app| app.key(chord(Action::SessionNextRun)));
}

const GROUP: &str = "queue.runs.group.1";

#[test]
fn in_queue_flow_runs_group_under_their_automation_and_the_next_run_key_opens_the_group() {
    let mut world = World::start(Daemon::scripted().state(queue(one_desktop(vec![
        session("manual", "working"),
        run("run-a", "r1", Some(60)),
        run("run-b", "r2", Some(30)),
    ]))));
    assert_eq!(value(&world, GROUP), "2");
    assert!(!is(&world, GROUP, "expanded"));
    assert!(!has(&world, "queue-run.run-a"));
    assert!(!has(&world, "queue-turn.run-a"));

    next_run(&mut world);
    assert_eq!(sent_ids(&world, "desktop_show_session"), ["run-a"]);
    assert!(is(&world, GROUP, "expanded"));
    assert!(has(&world, "queue-run.run-a"));

    click(&mut world, "queue-run.run-b.settle");
    assert_eq!(sent_ids(&world, "settle_turn"), ["run-b"]);

    click(&mut world, GROUP);
    assert!(!is(&world, GROUP, "expanded"));
    assert!(!has(&world, "queue-run.run-a"));
    click(&mut world, "queue.runs.needing");
    assert!(is(&world, GROUP, "expanded"));
    assert!(has(&world, "queue-run.run-a"));
}

#[test]
fn the_runs_that_need_you_take_their_own_line_under_the_automations_heading() {
    let mut world = World::start(Daemon::scripted().state(queue(one_desktop(vec![
        run("run-a", "r1", Some(60)),
        run("run-b", "r2", Some(30)),
    ]))));
    let [hx, hy, hw, _] = world.rect("queue.runs");
    let [x, y, w, _] = world.rect("queue.runs.needing");
    assert_eq!((x, w), (hx, hw));
    assert!(y > hy, "the button shares the heading's line");
}

#[test]
fn only_the_current_singleton_counts_and_a_superseded_question_stays_reachable() {
    let previous = superseded(Session {
        state: "recoverable".into(),
        ..run("previous", "old", None)
    });
    let older = superseded(Session {
        state: "recoverable".into(),
        ..run("older", "older", None)
    });
    let mut world = World::start(Daemon::scripted().state(queue(one_desktop(vec![
        previous.clone(),
        older,
        run("current", "current", None),
    ]))));
    assert_eq!(value(&world, GROUP), "1");
    click(&mut world, GROUP);
    assert!(has(&world, "queue-run.current"));
    assert!(!has(&world, "queue-run.previous"));
    assert!(!has(&world, "queue-run.older"));

    changes(
        &mut world,
        Session {
            turn_owed: Some(true),
            turn_opened_at: Some(ago(5)),
            ..previous
        },
    );
    assert_eq!(value(&world, GROUP), "1");
    assert!(has(&world, "queue.runs.needing"));
    next_run(&mut world);
    assert_eq!(sent_ids(&world, "desktop_show_session"), ["previous"]);

    changes(&mut world, superseded(run("current", "current", None)));
    world.gesture(|app| app.click(Target::role("button").name("Collapse sidebar")));
    assert_eq!(value(&world, "queue-bar.runs"), "0 runs, 1 need you");
    click(&mut world, "queue-bar.runs");
    assert_eq!(
        sent_ids(&world, "desktop_show_session"),
        ["previous", "previous"]
    );
}

#[test]
fn the_next_run_key_and_the_palette_walk_the_runs_that_need_the_user_oldest_first_and_settle_takes_the_one_shown()
 {
    let sessions = vec![
        session("s1", "working"),
        run("r1", "a", Some(120)),
        run("r2", "b", Some(60)),
        run("r3", "c", None),
    ];
    let mut world = World::start(palette::daemon(one_desktop(sessions.clone()), vec![]));
    assert!(!has(&world, "queue.runs"));

    next_run(&mut world);
    assert_eq!(sent_ids(&world, "desktop_show_session"), ["r1"]);
    let all: Vec<&Session> = sessions.iter().collect();
    daemon_shows(&mut world, &all, "r1");
    world.gesture(|app| app.key(chord(Action::SessionSettle)));
    assert_eq!(sent_ids(&world, "settle_turn"), ["r1"]);
    next_run(&mut world);
    assert_eq!(sent_ids(&world, "desktop_show_session"), ["r1", "r2"]);
    daemon_shows(&mut world, &all, "r2");
    run_command(&mut world, "Next run needing you");
    assert_eq!(sent_ids(&world, "desktop_show_session"), ["r1", "r2", "r1"]);
}

fn run_of(id: &str, definition_id: i64, name: &str, owed: bool) -> Session {
    let mut session = run(id, id, owed.then_some(30));
    if let Some(automation) = &mut session.automation {
        automation.definition_id = definition_id;
        automation.definition_name = name.into();
    }
    session
}

fn collapsed(sessions: Vec<Session>) -> World {
    let mut world = World::start(Daemon::scripted().state(queue(one_desktop(sessions))));
    world.gesture(|app| app.click(Target::role("button").name("Collapse sidebar")));
    world
}

fn hover(world: &mut World, key: &str) {
    let [x, y, w, h] = world.screen().bounds(Target::key(key)).expect("it shows");
    world.gesture(|app| app.move_to((x + w / 2.0, y + h / 2.0)));
}

const PEEK: &str = "queue-bar.runs-peek";

fn text_of(world: &World, key: &str) -> Option<String> {
    world.screen().find(Target::key(key)).and_then(|n| n.name)
}

fn bar_runs() -> Vec<Session> {
    vec![
        run_of("n1", 1, "Nightly docs sweep", true),
        run_of("n2", 1, "Nightly docs sweep", false),
        run_of("p1", 2, "Reviewer", true),
        turn("a", 9),
    ]
}

#[test]
fn the_runs_chip_is_absent_without_runs_and_counts_them_and_walks_them_when_pressed() {
    let world = collapsed(vec![turn("a", 9)]);
    assert!(!has(&world, "queue-bar.runs"));
    assert_eq!(
        text_of(&world, "queue-bar.pill").as_deref(),
        Some("1 waiting · a")
    );

    let mut world = collapsed(bar_runs());
    assert_eq!(value(&world, "queue-bar.runs"), "3 runs, 2 need you");
    hover(&mut world, "queue-bar.runs");
    assert!(has(&world, PEEK));
    click(&mut world, "queue-bar.runs");
    assert_eq!(sent_ids(&world, "desktop_show_session"), ["n1"]);
    assert!(!has(&world, PEEK), "walking closes the peek");
}

#[test]
fn the_runs_peek_lists_every_run_by_automation_and_tags_the_one_the_walk_opens_next() {
    let sessions = bar_runs();
    let mut world = collapsed(sessions.clone());
    let all: Vec<&Session> = sessions.iter().collect();
    next_run(&mut world);
    daemon_shows(&mut world, &all, "n1");
    hover(&mut world, "queue-bar.runs");

    assert_eq!(
        text_of(&world, &format!("{PEEK}.group:1")).as_deref(),
        Some("Nightly docs sweep")
    );
    assert_eq!(
        text_of(&world, &format!("{PEEK}.group:1.detail")).as_deref(),
        Some("1 need you · 2 runs")
    );
    assert_eq!(
        text_of(&world, &format!("{PEEK}.group:2.detail")).as_deref(),
        Some("1 need you · 1 run")
    );
    assert!(has(&world, &format!("{PEEK}.agent:p1.tag")));
    assert!(!has(&world, &format!("{PEEK}.agent:n1.tag")));
    assert!(
        !has(&world, &format!("{PEEK}.agent:a")),
        "turns stay out of it"
    );
    assert!(
        text_of(&world, &format!("{PEEK}.more"))
            .unwrap()
            .starts_with("Automation runs never join the queue")
    );

    click(&mut world, &format!("{PEEK}.agent:n2"));
    assert_eq!(sent_ids(&world, "desktop_show_session"), ["n1", "n2"]);
    assert!(!has(&world, PEEK));
}

#[test]
fn the_runs_peek_closes_with_its_last_run_and_the_chip_comes_back_closed() {
    let mut world = collapsed(bar_runs());
    hover(&mut world, "queue-bar.runs");
    assert!(has(&world, PEEK));

    for session in bar_runs().into_iter().take(3) {
        world
            .daemon
            .emit(attn_protocol::SessionUnregisteredMessage {
                session,
                ..attn_protocol::SessionUnregisteredMessage::new()
            });
    }
    world.settle();
    assert!(!has(&world, "queue-bar.runs"));
    assert!(!has(&world, PEEK));

    world.gesture(|app| app.move_to((600.0, 500.0)));
    for session in bar_runs() {
        changes(&mut world, session);
    }
    assert!(has(&world, "queue-bar.runs"));
    assert!(!has(&world, PEEK));
}
