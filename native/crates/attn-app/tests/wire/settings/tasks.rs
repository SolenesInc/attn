use attn_protocol::{Task, TaskListResultMessage, TaskRetryResultMessage, TasksChangedMessage};
use attn_testkit::{Request, Target, World, message};
use serde_json::Value;

use rune::Key;

use super::{accepting, click, open, press, rest};

const AT: &str = "2026-05-16T16:00:00Z";

fn task(id: &str, state: &str) -> Task {
    Task {
        id: id.into(),
        kind: "session_activity".into(),
        subject: format!("session-{id}"),
        state: state.into(),
        attempts: 5,
        next_attempt_at: AT.into(),
        created_at: AT.into(),
        updated_at: AT.into(),
        ..Task::default()
    }
}

fn listing(tasks: Vec<Task>) -> impl Fn(&Request) -> Value + Send + 'static {
    move |r| {
        message(TaskListResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            tasks: Some(tasks.clone()),
            ..TaskListResultMessage::new()
        })
    }
}

fn on_task_runner(world: &mut World) {
    open(world);
    click(world, "settings.nav.backgroundTasks");
}

fn shows(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

fn reads(world: &World) -> usize {
    world.daemon.sent_of("task_list").len()
}

#[test]
fn a_dead_tasks_cause_shows_and_its_diagnostic_output_waits_behind_an_expander() {
    let (daemon, _) = accepting(&[]);
    daemon.on(
        "task_list",
        listing(vec![Task {
            last_error: Some("The agent process exited with status 2.".into()),
            last_diagnostic: Some("stderr: authentication failed".into()),
            ..task("1", "dead")
        }]),
    );
    let mut world = World::start(daemon);
    on_task_runner(&mut world);

    let error = world.screen().get(Target::key("settings.task.1.error"));
    assert_eq!(
        error.name.as_deref(),
        Some("The agent process exited with status 2.")
    );
    assert!(!shows(&world, "settings.task.1.diagnostic"));

    world
        .automate(
            "press",
            serde_json::json!({"target": {"role": "button", "name": "Diagnostic output"}}),
        )
        .unwrap();

    assert_eq!(
        world
            .screen()
            .get(Target::key("settings.task.1.diagnostic"))
            .name
            .as_deref(),
        Some("stderr: authentication failed")
    );
    assert_eq!(reads(&world), 1);
}

#[test]
fn the_list_reads_again_when_the_daemon_says_tasks_changed_but_only_while_it_shows() {
    let (daemon, _) = accepting(&[]);
    daemon.on("task_list", listing(vec![task("1", "queued")]));
    let mut world = World::start(daemon);
    on_task_runner(&mut world);
    assert_eq!(reads(&world), 1);
    assert!(shows(&world, "settings.task.1"));

    world.daemon.emit(TasksChangedMessage::new());
    world.settle();
    assert_eq!(reads(&world), 2);

    click(&mut world, "settings.nav.general");
    world.daemon.emit(TasksChangedMessage::new());
    world.settle();
    assert_eq!(reads(&world), 2);
}

#[test]
fn retry_asks_the_daemon_once_and_waits_for_its_answer() {
    let (daemon, _) = accepting(&[]);
    daemon.on(
        "task_list",
        listing(vec![task("1", "failed"), task("2", "running")]),
    );
    daemon.on("task_retry", |_| ());
    let mut world = World::start(daemon);
    on_task_runner(&mut world);
    assert!(!shows(&world, "settings.task.2.retry"));

    click(&mut world, "settings.task.1.retry");
    click(&mut world, "settings.task.1.retry");

    let sent = world.daemon.sent_of("task_retry");
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["task_id"], "1");
    assert!(
        world
            .screen()
            .get(Target::key("settings.task.1.retry"))
            .is("disabled")
    );

    world.daemon.reply_to(
        &sent[0],
        TaskRetryResultMessage {
            request_id: sent[0]["request_id"].as_str().unwrap().into(),
            success: true,
            ..TaskRetryResultMessage::new()
        },
    );
    world.settle();
    assert!(
        !world
            .screen()
            .get(Target::key("settings.task.1.retry"))
            .is("disabled")
    );
}

#[test]
fn a_failed_read_says_why_and_try_again_reads_again() {
    let (daemon, _) = accepting(&[]);
    daemon.on("task_list", |r: &Request| {
        message(TaskListResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: false,
            error: Some("the task store is closed".into()),
            ..TaskListResultMessage::new()
        })
    });
    let mut world = World::start(daemon);
    on_task_runner(&mut world);
    assert_eq!(
        world
            .screen()
            .get(Target::key("settings.tasks.error"))
            .name
            .as_deref(),
        Some("the task store is closed")
    );

    click(&mut world, "settings.tasks.try-again");
    assert_eq!(reads(&world), 2);
}

#[test]
fn an_empty_runner_says_so() {
    let (daemon, _) = accepting(&[]);
    daemon.on("task_list", listing(vec![]));
    let mut world = World::start(daemon);
    on_task_runner(&mut world);
    assert_eq!(
        world
            .screen()
            .get(Target::key("settings.tasks.empty"))
            .name
            .as_deref(),
        Some("No background tasks.")
    );
}

/// Opens a dead task's diagnostic output by keyboard in the smallest window at the largest text,
/// and finds its control, ring and all, and the start of the output in the page's view.
fn opens_in_view(diagnostic: String) {
    let (daemon, _) = accepting(&[("uiScale", "1.5")]);
    daemon.on(
        "task_list",
        listing(vec![Task {
            last_error: Some(
                "The agent process exited with status 2 after the provider refused its \
                 credentials three times in a row, while the daemon was still pairing."
                    .into(),
            ),
            last_diagnostic: Some(diagnostic),
            ..task("1", "dead")
        }]),
    );
    let mut world = World::start(daemon);
    world.resize((800.0, 600.0));
    on_task_runner(&mut world);
    let control = "settings.task.1.diagnostics";
    for _ in 0..60 {
        if world.screen().focused().and_then(|n| n.key).as_deref() == Some(control) {
            break;
        }
        press(&mut world, Key::Tab);
    }
    assert_eq!(
        world.screen().focused().and_then(|n| n.key).as_deref(),
        Some(control),
        "Tab reaches Diagnostic output"
    );
    press(&mut world, Key::Text(" ".into()));
    let body = world.rect("settings.body");
    let ring = 2.0;
    let shown = world.rect(control);
    assert!(
        shown[1] - ring >= body[1] && shown[1] + shown[3] + ring <= body[1] + body[3],
        "the opened control at {shown:?}, with its ring, lies inside the page's view {body:?}"
    );
    let output = world
        .automate(
            "find",
            serde_json::json!({"target": {"key": "settings.task.1.diagnostic"}}),
        )
        .expect("the output is on the page")["rect"]
        .as_array()
        .map(|r| {
            r.iter()
                .filter_map(serde_json::Value::as_f64)
                .collect::<Vec<_>>()
        });
    assert!(
        output
            .as_ref()
            .is_some_and(|r| r[1] >= shown[1] + shown[3] && r[1] < body[1] + body[3]),
        "the output at {output:?} starts under the control and inside the page's view {body:?}"
    );
    rest(&mut world);
}

#[test]
fn opening_a_short_diagnostic_by_keyboard_shows_its_control_and_output_in_the_smallest_window() {
    opens_in_view("stderr: authentication failed\nstderr: token expired".into());
}

#[test]
fn opening_a_diagnostic_taller_than_the_view_shows_its_control_and_where_it_starts() {
    let lines: Vec<String> = (1..=200)
        .map(|n| format!("stderr: line {n}: retrying the provider handshake after a refusal"))
        .collect();
    let diagnostic = lines.join("\n");
    assert!(
        diagnostic.len() < 16 * 1024,
        "under the daemon's diagnostic cap"
    );
    opens_in_view(diagnostic);
}
