use std::collections::BTreeMap;
use std::time::Duration;

use attn_protocol::{AutomationProvenance, InitialStateMessage, Session, SettingsUpdatedMessage};
use attn_testkit::{Daemon, Target, WALL_START_MS, World};

use super::queue::{ago, changes, crew, one_desktop, sent_ids, snoozed, turn};
use super::sidebar::session;

const QUEUE: [(&str, &str); 1] = [("queue_mode_enabled", "true")];

fn agent(id: &str, state: &str) -> Session {
    session(id, state)
}

fn home(sessions: Vec<Session>, settings: &[(&str, &str)]) -> World {
    let mut message: InitialStateMessage = one_desktop(sessions);
    message.settings = Some(
        settings
            .iter()
            .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
            .collect(),
    );
    World::start(Daemon::scripted().state(message))
}

/// The agents a home group lists, top to bottom.
fn rows_in(world: &World, group: &str) -> Vec<String> {
    world
        .screen()
        .children(Target::key(format!("home.group.{group}")))
        .into_iter()
        .filter_map(|n| n.key?.strip_prefix("home.session.").map(str::to_owned))
        .collect()
}

fn has(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

fn text_of(world: &World, key: &str) -> String {
    let node = world.screen().get(Target::key(key));
    node.name.or(node.value).unwrap_or_default()
}

fn with_activity(id: &str, minutes_ago: i64) -> Session {
    Session {
        activity: Some("running the frontend test suite".into()),
        activity_at: Some(ago(minutes_ago)),
        ..agent(id, "working")
    }
}

fn expand_snoozed(world: &mut World) {
    world.gesture(|app| app.click(Target::key("home.group.snoozed.toggle")));
}

fn checked(world: &World, key: &str) -> bool {
    world
        .screen()
        .get(Target::key(key))
        .states
        .iter()
        .any(|s| s == "checked")
}

#[test]
fn home_groups_sessions_by_what_they_are_doing() {
    let world = home(
        vec![
            agent("busy", "working"),
            agent("asking", "pending_approval"),
            agent("also-asking", "pending_approval"),
            agent("loop", "scheduled"),
            agent("restartable", "recoverable"),
        ],
        &[],
    );

    assert_eq!(rows_in(&world, "working"), ["busy"]);
    assert_eq!(rows_in(&world, "pending"), ["asking", "also-asking"]);
    assert_eq!(rows_in(&world, "scheduled"), ["loop"]);
    assert_eq!(rows_in(&world, "recoverable"), ["restartable"]);
}

#[test]
fn the_home_header_leads_with_attns_logo() {
    let world = home(vec![], &[]);

    let header = world.screen().children(Target::key("home.header"));
    let logo = header.first().expect("the home header is empty");
    assert_eq!(logo.role, "image");
    assert_eq!(logo.name.as_deref(), Some("attn"));
}

#[test]
fn home_says_so_when_there_are_no_sessions() {
    let world = home(vec![], &[]);

    assert!(has(&world, "home.empty"));
}

#[test]
fn an_activity_line_shows_beside_the_name_and_turns_stale_once_it_stops_being_refreshed() {
    let mut world = home(vec![with_activity("s1", 0)], &[]);
    assert_eq!(
        text_of(&world, "home.session.s1.activity"),
        "running the frontend test suite"
    );

    world.advance(Duration::from_secs(16 * 60));

    let at = rune::time::LocalTime::at(WALL_START_MS);
    assert_eq!(
        text_of(&world, "home.session.s1.activity"),
        format!(
            "running the frontend test suite, as of {}:{:02}",
            at.hour, at.minute
        )
    );
}

#[test]
fn the_staleness_window_follows_the_configured_cadence() {
    let mut world = home(
        vec![with_activity("s1", 0)],
        &[("activity.intervals", r#"{"watching":1800,"present":1800}"#)],
    );

    world.advance(Duration::from_secs(20 * 60));

    assert_eq!(
        text_of(&world, "home.session.s1.activity"),
        "running the frontend test suite"
    );
}

#[test]
fn a_session_without_activity_shows_no_line() {
    let world = home(vec![agent("s1", "working")], &[]);

    assert!(!has(&world, "home.session.s1.activity"));
}

#[test]
fn in_queue_flow_home_leads_with_the_owed_turns_oldest_first_and_keeps_them_out_of_the_state_groups()
 {
    let world = home(
        vec![
            Session {
                state: "waiting_input".into(),
                ..turn("newer", 10)
            },
            Session {
                state: "pending_approval".into(),
                ..turn("older", 60)
            },
            agent("settled-but-waiting", "waiting_input"),
        ],
        &QUEUE,
    );

    assert_eq!(rows_in(&world, "turns"), ["older", "newer"]);
    assert_eq!(rows_in(&world, "waiting"), ["settled-but-waiting"]);
    assert!(has(&world, "home.settled"));
    assert!(!has(&world, "home.all-settled"));
    assert_eq!(text_of(&world, "home.session.older.when"), "1h");
}

#[test]
fn automation_and_crew_stay_out_of_the_turns_unless_the_crew_joins_the_queue() {
    let automation = AutomationProvenance {
        run_id: "run-1".into(),
        definition_name: "Review with Sol".into(),
        ..AutomationProvenance::default()
    };
    let mut world = home(
        vec![
            Session {
                automation: Some(automation.clone()),
                ..turn("automation-owed", 60)
            },
            Session {
                automation: Some(automation),
                ..agent("automation-settled", "working")
            },
            Session {
                crew_member: Some("keel".into()),
                ..turn("crew-owed", 50)
            },
            crew("crew-settled", "marlowe", false),
            turn("ordinary", 40),
        ],
        &[
            ("queue_mode_enabled", "true"),
            ("queue_crew_enabled", "false"),
        ],
    );

    assert_eq!(rows_in(&world, "turns"), ["ordinary"]);
    assert_eq!(rows_in(&world, "waiting"), ["automation-owed", "crew-owed"]);
    assert_eq!(
        rows_in(&world, "working"),
        ["automation-settled", "crew-settled"]
    );

    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some(BTreeMap::from([
            ("queue_mode_enabled".to_owned(), "true".to_owned()),
            ("queue_crew_enabled".to_owned(), "true".to_owned()),
        ])),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();

    assert_eq!(rows_in(&world, "turns"), ["crew-owed", "ordinary"]);
    assert_eq!(rows_in(&world, "waiting"), ["automation-owed"]);
}

#[test]
fn all_settled_says_what_is_still_running_once_nothing_is_owed() {
    let world = home(
        vec![
            agent("busy", "working"),
            agent("busy-too", "working"),
            agent("later", "scheduled"),
        ],
        &QUEUE,
    );

    assert_eq!(
        text_of(&world, "home.all-settled.running"),
        "2 working · 1 scheduled"
    );
    assert!(!has(&world, "home.group.turns"));
}

#[test]
fn all_settled_says_nothing_is_running_when_everything_settled_is_parked() {
    let world = home(vec![agent("parked", "idle")], &QUEUE);

    assert_eq!(
        text_of(&world, "home.all-settled.running"),
        "Nothing is running."
    );
}

#[test]
fn the_chief_stays_out_of_the_turns() {
    let world = home(
        vec![Session {
            crew_member: Some("chief-key".into()),
            chief_of_staff: Some(true),
            ..turn("chief", 60)
        }],
        &QUEUE,
    );

    assert!(!has(&world, "home.group.turns"));
    assert!(has(&world, "home.all-settled"));
}

#[test]
fn with_the_queue_off_home_groups_only_by_state() {
    let world = home(vec![turn("asking", 60)], &[]);

    assert!(!has(&world, "home.group.turns"));
    assert!(!has(&world, "home.settled"));
    assert!(!has(&world, "home.all-settled"));
    assert_eq!(rows_in(&world, "waiting"), ["asking"]);
}

#[test]
fn home_reached_by_settling_the_last_turn_follows_the_next_until_switched_off() {
    let mut world = home(vec![turn("s1", 60), agent("s2", "working")], &QUEUE);
    world.gesture(|app| app.click(Target::key("home.session.s1")));
    changes(&mut world, agent("s1", "waiting_input"));
    assert_eq!(
        world
            .screen()
            .get(Target::key("sidebar.home"))
            .value
            .as_deref(),
        Some("current")
    );
    assert!(checked(&world, "home.follow"));

    world.gesture(|app| app.click(Target::key("home.follow")));
    assert!(!checked(&world, "home.follow"));
    changes(&mut world, turn("s2", 0));

    assert_eq!(sent_ids(&world, "desktop_show_session"), ["s1"]);
}

#[test]
fn following_is_off_and_still_offered_when_the_user_walked_home() {
    let mut world = home(vec![agent("s1", "working")], &QUEUE);

    assert!(!checked(&world, "home.follow"));

    world.gesture(|app| app.click(Target::key("home.follow")));
    changes(&mut world, turn("s1", 0));
    assert_eq!(sent_ids(&world, "desktop_show_session"), ["s1"]);
}

#[test]
fn following_is_not_offered_while_a_turn_is_owed() {
    let world = home(vec![turn("s1", 60)], &QUEUE);

    assert!(!has(&world, "home.follow"));
}

#[test]
fn snoozed_agents_sit_in_a_collapsed_group_that_lists_each_wake_time_soonest_first() {
    let mut world = home(
        vec![
            Session {
                state: "idle".into(),
                ..snoozed("back-later", 24 * 60 + 45)
            },
            Session {
                state: "working".into(),
                ..snoozed("back-sooner", 24 * 60)
            },
            agent("running", "working"),
        ],
        &QUEUE,
    );
    assert_eq!(rows_in(&world, "working"), ["running"]);
    assert_eq!(
        world
            .screen()
            .get(Target::key("home.group.snoozed.toggle"))
            .states,
        ["collapsed"]
    );
    assert!(!has(&world, "home.session.back-later"));

    expand_snoozed(&mut world);

    assert_eq!(rows_in(&world, "snoozed"), ["back-sooner", "back-later"]);
    assert!(text_of(&world, "home.session.back-sooner.when").starts_with("tomorrow "));
    assert!(has(&world, "home.session.back-sooner.unsnooze"));
}

#[test]
fn a_snoozed_agent_unsnoozes_early_with_the_queue_off_too() {
    let mut world = home(vec![snoozed("deferred", 60)], &[]);
    assert!(!has(&world, "home.group.idle"));
    expand_snoozed(&mut world);

    world.gesture(|app| app.click(Target::role("button").name("Unsnooze deferred")));

    assert_eq!(sent_ids(&world, "wake_turn"), ["deferred"]);
    assert!(sent_ids(&world, "desktop_show_session").is_empty());
}

#[test]
fn a_snoozed_chief_is_listed_once_whether_or_not_the_crew_queues() {
    for crew_in_queue in ["true", "false"] {
        let mut world = home(
            vec![Session {
                crew_member: Some("chief-key".into()),
                chief_of_staff: Some(true),
                ..snoozed("chief", 60)
            }],
            &[
                ("queue_mode_enabled", "true"),
                ("queue_crew_enabled", crew_in_queue),
            ],
        );

        expand_snoozed(&mut world);

        assert_eq!(
            rows_in(&world, "snoozed"),
            ["chief"],
            "crew {crew_in_queue}"
        );
    }
}

#[test]
fn a_lapsed_snooze_leaves_the_agent_where_its_state_puts_it() {
    let world = home(
        vec![Session {
            turn_snoozed_until: Some(ago(1)),
            ..agent("unsnoozed", "working")
        }],
        &QUEUE,
    );

    assert!(!has(&world, "home.group.snoozed"));
    assert_eq!(rows_in(&world, "working"), ["unsnoozed"]);
}

#[test]
fn the_settings_button_opens_settings() {
    let mut world = home(vec![], &[]);

    world.gesture(|app| app.click(Target::key("home.settings")));

    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Settings"))
            .is_some()
    );
}
