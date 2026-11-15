use attn_protocol::{Task, TaskListResultMessage, TaskRetryResultMessage, TasksChangedMessage};
use attn_testkit::{Request, Target, World, message};
use serde_json::Value;

use super::{accepting, click, open};

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
