use std::sync::{Arc, Mutex};

use attn_app::Action;
use attn_protocol::{
    InitialStateMessage, InstallCodexUpdateResultMessage, Notification, NotificationAction,
    NotificationListResultMessage, NotificationsUpdatedMessage, TaskRetryResultMessage,
};
use attn_testkit::{Answer, Request, Target, World, message};
use rune::{Key, KeyEvent, Mods};

use super::queue::one_desktop;
use super::sidebar::{chord, session};

const PANEL: &str = "notifications.panel";
const BELL: &str = "titlebar.notifications";
const STRIP: &str = "sidebar.critical";

fn notification(id: &str, severity: &str, title: &str) -> Notification {
    Notification {
        id: id.into(),
        kind: "task_failed".into(),
        severity: severity.into(),
        title: title.into(),
        body: "body".into(),
        created_at: "2026-08-15T09:00:00Z".into(),
        ..Notification::default()
    }
}

fn read(mut n: Notification) -> Notification {
    n.read_at = "2026-08-15T09:05:00Z".into();
    n
}

fn acting(mut n: Notification, actions: &[(&str, &str, &str)]) -> Notification {
    n.actions = Some(
        actions
            .iter()
            .map(|(kind, label, target)| NotificationAction {
                kind: (*kind).into(),
                label: (*label).into(),
                target_id: (*target).into(),
            })
            .collect(),
    );
    n
}

type Feed = Arc<Mutex<Vec<Notification>>>;

fn listed(request: &Request, feed: &[Notification]) -> serde_json::Value {
    let critical: Vec<&Notification> = feed
        .iter()
        .filter(|n| n.read_at.is_empty() && n.severity == "critical")
        .collect();
    message(NotificationListResultMessage {
        request_id: request["request_id"].as_str().unwrap().into(),
        success: true,
        notifications: Some(feed.to_vec()),
        unread_count: feed.iter().filter(|n| n.read_at.is_empty()).count() as i64,
        unread_critical_count: critical.len() as i64,
        critical_title: critical.first().map(|n| n.title.clone()),
        ..NotificationListResultMessage::new()
    })
}

/// An app whose daemon lists `feed` for each `notification_list`, and changes nothing on its own.
fn start_on(state: InitialStateMessage, feed: Vec<Notification>) -> (World, Feed) {
    let feed: Feed = Arc::new(Mutex::new(feed));
    let daemon = super::palette::daemon(state, vec![]);
    let lists = feed.clone();
    daemon.on("notification_list", move |r| {
        listed(r, &lists.lock().unwrap())
    });
    daemon.on("notification_mark_read", |_| ());
    (World::start(daemon), feed)
}

fn start(feed: Vec<Notification>) -> World {
    start_on(one_desktop(vec![session("s1", "idle")]), feed).0
}

fn click(world: &mut World, key: &str) {
    let key = key.to_owned();
    world.gesture(|app| app.click(Target::key(key)));
}

fn open(world: &mut World) {
    click(world, BELL);
    assert!(has(world, PANEL));
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

fn value(world: &World, key: &str) -> String {
    world
        .screen()
        .get(Target::key(key))
        .value
        .unwrap_or_default()
}

fn disabled(world: &World, key: &str) -> bool {
    world
        .screen()
        .get(Target::key(key))
        .states
        .iter()
        .any(|s| s == "disabled")
}

fn words(world: &World, key: &str) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key(key))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn row(id: &str) -> String {
    format!("notifications.row.{id}")
}

fn expand(world: &mut World, id: &str) {
    click(world, &format!("{}.head", row(id)));
}

fn broadcast(world: &mut World, unread: i64, critical: i64, title: Option<&str>) {
    world.daemon.emit(NotificationsUpdatedMessage {
        unread_count: unread,
        unread_critical_count: critical,
        critical_title: title.map(str::to_owned),
        ..NotificationsUpdatedMessage::new()
    });
    world.settle();
}

fn escape(world: &mut World) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Escape,
            mods: Mods::default(),
        })
    });
}

#[test]
fn a_connecting_app_counts_the_feed_and_names_its_newest_critical_notification() {
    let world = start(vec![
        notification("a", "critical", "Plugin stopped"),
        notification("b", "info", "Compaction finished"),
    ]);

    assert_eq!(world.daemon.sent_of("notification_list").len(), 1);
    assert_eq!(value(&world, BELL), "2");
    assert_eq!(
        name(&world, STRIP),
        "1 unread critical notification: Plugin stopped. Open notifications."
    );
}

#[test]
fn the_critical_strip_follows_the_broadcast_and_opens_the_panel() {
    let mut world = start(vec![]);
    assert!(!has(&world, STRIP));

    broadcast(&mut world, 1, 1, Some("Plugin stopped"));
    assert_eq!(words(&world, STRIP), Vec::<String>::new());
    assert_eq!(
        name(&world, STRIP),
        "1 unread critical notification: Plugin stopped. Open notifications."
    );

    broadcast(&mut world, 3, 3, Some("Ticket sync failed"));
    assert_eq!(
        name(&world, STRIP),
        "3 unread critical notifications, newest: Ticket sync failed. Open notifications."
    );

    broadcast(&mut world, 1, 1, Some(""));
    assert_eq!(
        name(&world, STRIP),
        "1 unread critical notification: Critical notification. Open notifications."
    );

    click(&mut world, STRIP);
    assert!(has(&world, PANEL));
    escape(&mut world);
    assert!(!has(&world, PANEL));

    broadcast(&mut world, 2, 0, None);
    assert!(!has(&world, STRIP));
    assert_eq!(value(&world, BELL), "2");
}

#[test]
fn each_notification_shows_at_its_severity_an_unknown_one_as_info_and_only_unread_rows_unread() {
    let mut world = start(vec![
        read(notification("a", "critical", "Plugin stopped")),
        notification("b", "warning", "Ticket reconciliation failed"),
        notification("c", "info", "Compaction finished"),
        notification("d", "catastrophic", "From the future"),
    ]);
    open(&mut world);

    assert_eq!(value(&world, &row("a")), "critical");
    assert_eq!(value(&world, &row("b")), "warning, unread");
    assert_eq!(value(&world, &row("c")), "info, unread");
    assert_eq!(value(&world, &row("d")), "info, unread");
}

#[test]
fn a_failure_shows_its_evidence_and_only_the_actions_the_daemon_supplied() {
    let mut failed = notification("failed", "warning", "Couldn't update activity");
    failed.body = "legacy impact".into();
    failed.trigger = "New output triggered a Home activity summary.".into();
    failed.impact = "The Home activity summary may be stale.".into();
    failed.cause = "The agent process exited with status 2.".into();
    failed.diagnostic = "stderr: authentication failed".into();
    let failed = acting(
        failed,
        &[
            ("open_session", "Open session", "s1"),
            ("retry_task", "Retry", "task-1"),
            ("unknown_kind", "Do something new", "x"),
        ],
    );
    let mut world = start(vec![failed]);
    world.daemon.answer_in_turn("task_retry", [Answer::Hold]);
    open(&mut world);
    assert_eq!(
        name(&world, &format!("{}.preview", row("failed"))),
        "The Home activity summary may be stale."
    );

    expand(&mut world, "failed");
    let marked = world.daemon.sent_of("notification_mark_read");
    assert_eq!(marked.len(), 1);
    assert_eq!(marked[0]["notification_id"], "failed");
    let detail = words(&world, &format!("{}.detail", row("failed")));
    for fact in [
        "New output triggered a Home activity summary.",
        "The Home activity summary may be stale.",
        "The agent process exited with status 2.",
    ] {
        assert!(detail.contains(&fact.to_owned()), "{fact} in {detail:?}");
    }
    assert!(!detail.contains(&"stderr: authentication failed".to_owned()));
    click(&mut world, &format!("{}.diagnostic", row("failed")));
    let detail = words(&world, &format!("{}.detail", row("failed")));
    assert!(detail.contains(&"stderr: authentication failed".to_owned()));
    assert!(!has(&world, &format!("{}.action.2", row("failed"))));

    let retry = format!("{}.action.1", row("failed"));
    click(&mut world, &retry);
    let retries = world.daemon.sent_of("task_retry");
    assert_eq!(retries.len(), 1);
    assert_eq!(retries[0]["task_id"], "task-1");
    assert_eq!(name(&world, &retry), "Retrying…");
    assert!(disabled(&world, &retry));
    world.daemon.reply_to(
        &retries[0],
        TaskRetryResultMessage {
            success: true,
            ..TaskRetryResultMessage::new()
        },
    );
    world.settle();
    assert_eq!(name(&world, &retry), "Retry");

    click(&mut world, &format!("{}.action.0", row("failed")));
    assert!(!has(&world, PANEL));
    let shows = world.daemon.sent_of("desktop_show_session");
    assert_eq!(shows.last().unwrap()["session_id"], "s1");
}

#[test]
fn a_tasks_notification_offers_no_retry_the_daemon_did_not_supply() {
    let mut task = notification("t", "warning", "Something happened");
    task.source_kind = "task".into();
    task.source_id = "task-1".into();
    let mut world = start(vec![task]);
    open(&mut world);

    expand(&mut world, "t");

    assert!(has(&world, &format!("{}.detail", row("t"))));
    assert!(!has(&world, &format!("{}.action.0", row("t"))));
}

fn garden_open(world: &World) -> bool {
    let screen = world.screen();
    ["dialog", "region"]
        .into_iter()
        .any(|role| screen.find(Target::role(role).name("The garden")).is_some())
}

fn search_garden(world: &mut World, words: &str) {
    let words = words.to_owned();
    world.gesture(|app| {
        app.click(Target::key("garden.search.field"));
        app.type_text(&words);
    });
}

fn open_seed_from_notification(world: &mut World) {
    open(world);
    expand(world, "resume");
    click(world, &format!("{}.action.0", row("resume")));
    assert!(!has(world, PANEL));
    let heading = world
        .screen()
        .find(Target::key("garden.head.title"))
        .and_then(|n| n.name);
    assert_eq!(heading.as_deref(), Some("Watch the deploy"));
    let search = world.screen().get(Target::key("garden.search.field")).value;
    assert_eq!(search.as_deref().unwrap_or_default(), "");
}

#[test]
fn a_blocked_tender_resume_opens_its_seed_in_the_garden_past_a_search_open_or_closed() {
    let resume = acting(
        notification("resume", "critical", "s-resume could not resume its tender"),
        &[("open_seed", "Open seed", "s-resume")],
    );
    let mut state = one_desktop(vec![session("s1", "idle")]);
    state.seeds = Some(vec![super::garden::seed("s-resume", "Watch the deploy")]);
    let (mut world, _) = start_on(state, vec![resume]);
    world.gesture(|app| app.key(chord(Action::BoardOpen)));
    search_garden(&mut world, "unrelated");

    open_seed_from_notification(&mut world);

    search_garden(&mut world, "unrelated");
    escape(&mut world);
    assert!(!garden_open(&world));

    open_seed_from_notification(&mut world);
}

#[test]
fn a_codex_update_installs_on_the_current_desktop_and_shows_installing_until_it_finishes() {
    let codex = acting(
        notification("codex", "info", "Codex 0.163.0 is available"),
        &[("install_codex_update", "Install", "")],
    );
    let mut world = start(vec![codex]);
    world
        .daemon
        .answer_in_turn("install_codex_update", [Answer::Hold]);
    open(&mut world);
    expand(&mut world, "codex");

    let install = format!("{}.action.0", row("codex"));
    click(&mut world, &install);
    assert!(!has(&world, PANEL));
    let sent = world.daemon.sent_of("install_codex_update");
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["desktop_id"], "d1");

    open(&mut world);
    expand(&mut world, "codex");
    assert_eq!(name(&world, &install), "Installing…");
    assert!(disabled(&world, &install));

    world.daemon.reply_to(
        &sent[0],
        InstallCodexUpdateResultMessage {
            success: true,
            profile_id: "work".into(),
            ..InstallCodexUpdateResultMessage::new()
        },
    );
    world.settle();
    assert_eq!(name(&world, &install), "Install");
    assert!(!disabled(&world, &install));
}

#[test]
fn mark_all_read_marks_every_notification_and_the_broadcast_reads_the_feed_again() {
    let (mut world, feed) = start_on(
        one_desktop(vec![session("s1", "idle")]),
        vec![
            notification("a", "warning", "One"),
            notification("b", "info", "Two"),
        ],
    );
    open(&mut world);
    let lists = world.daemon.sent_of("notification_list").len();

    click(&mut world, "notifications.mark-all");
    let marked = world.daemon.sent_of("notification_mark_read");
    assert_eq!(marked.len(), 1);
    assert!(marked[0].get("notification_id").is_none());

    feed.lock()
        .unwrap()
        .iter_mut()
        .for_each(|n| *n = read(n.clone()));
    broadcast(&mut world, 0, 0, None);
    assert_eq!(world.daemon.sent_of("notification_list").len(), lists + 1);
    assert_eq!(value(&world, &row("a")), "warning");
    assert!(disabled(&world, "notifications.mark-all"));
    escape(&mut world);
    assert_eq!(value(&world, BELL), "0");
}

#[test]
fn a_failed_read_says_why_and_try_again_reads_once_more() {
    let mut world = start(vec![notification("a", "info", "One")]);
    world.daemon.on("notification_list", |r| {
        message(NotificationListResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: false,
            error: Some("temporary list failure".into()),
            ..NotificationListResultMessage::new()
        })
    });
    open(&mut world);
    assert_eq!(
        name(&world, "notifications.state"),
        "temporary list failure"
    );

    let feed = vec![notification("a", "info", "One")];
    world
        .daemon
        .on("notification_list", move |r| listed(r, &feed));
    click(&mut world, "notifications.retry");
    assert!(has(&world, &row("a")));
}

#[test]
fn another_profiles_feed_never_shows_while_the_new_one_loads_or_after_it_fails() {
    let (mut world, _) = start_on(
        one_desktop(vec![session("s1", "idle")]),
        vec![notification("conflict", "warning", "Default's conflict")],
    );
    open(&mut world);
    assert!(has(&world, &row("conflict")));
    escape(&mut world);

    let mut side = one_desktop(vec![]);
    side.selected_profile_id = Some("side".into());
    side.profiles.as_mut().unwrap()[0].id = "side".into();
    world.daemon.answer_hello_with(side);
    world
        .daemon
        .answer_in_turn("notification_list", [Answer::Hold, Answer::Hold]);
    world.reconnect();
    open(&mut world);

    assert_eq!(name(&world, "notifications.state"), "Loading…");
    assert!(!has(&world, &row("conflict")));
    let pending = world.daemon.sent_of("notification_list");
    world.daemon.reply_to(
        pending.last().unwrap(),
        NotificationListResultMessage {
            success: false,
            error: Some("temporary list failure".into()),
            ..NotificationListResultMessage::new()
        },
    );
    world.settle();
    assert_eq!(
        name(&world, "notifications.state"),
        "temporary list failure"
    );
    assert!(!has(&world, &row("conflict")));
    assert!(world.daemon.sent_of("notification_mark_read").is_empty());
}

#[test]
fn the_palette_opens_the_panel_and_escape_hands_the_keyboard_back() {
    let mut world = start(vec![]);
    assert_eq!(name(&world, BELL), "Show Notifications");

    world.gesture(|app| app.key(chord(Action::UiCommandPalette)));
    world.gesture(|app| app.type_text("show notifications"));
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Enter,
            mods: Mods::default(),
        })
    });
    assert!(has(&world, PANEL));
    assert_eq!(name(&world, "notifications.state"), "No notifications.");

    escape(&mut world);
    assert!(!has(&world, PANEL));
    assert_eq!(name(&world, BELL), "Show Notifications");
}
