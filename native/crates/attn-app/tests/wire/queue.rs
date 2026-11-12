use std::collections::BTreeMap;

use std::time::Duration;

use attn_app::Action;
use attn_protocol::{Desktop, InitialStateMessage, Session};
use attn_testkit::{Daemon, Target, WALL_START_MS, World};

use super::sidebar::{chord, desktop, pane, session, state};

/// An RFC 3339 instant `minutes` before every world starts.
fn ago(minutes: i64) -> String {
    let at = WALL_START_MS / 1000 - minutes * 60;
    let (days, secs) = (at.div_euclid(86_400), at.rem_euclid(86_400));
    let (y, m, d) = civil(days);
    format!(
        "{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}Z",
        secs / 3600,
        secs / 60 % 60,
        secs % 60
    )
}

fn civil(days: i64) -> (i64, i64, i64) {
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z - era * 146_097;
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    (yoe + era * 400 + i64::from(m <= 2), m, d)
}

fn turn(id: &str, opened_minutes_ago: i64) -> Session {
    Session {
        turn_owed: Some(true),
        turn_opened_at: Some(ago(opened_minutes_ago)),
        ..session(id, "waiting_input")
    }
}

fn queue(mut message: InitialStateMessage) -> InitialStateMessage {
    message.settings = Some(BTreeMap::from([(
        "queue_mode_enabled".to_owned(),
        "true".to_owned(),
    )]));
    message
}

fn crew(id: &str, member: &str, chief: bool) -> Session {
    Session {
        crew_member: Some(member.into()),
        chief_of_staff: chief.then_some(true),
        ..session(id, "working")
    }
}

fn snoozed(id: &str, minutes_ahead: i64) -> Session {
    Session {
        turn_owed: Some(true),
        turn_opened_at: Some(ago(30)),
        turn_snoozed_until: Some(ago(-minutes_ahead)),
        ..session(id, "idle")
    }
}

fn slotted(mut desktop: Desktop, slot: i64) -> Desktop {
    desktop.shortcut_slot = Some(slot);
    desktop
}

/// Every agent on one desktop, in the order given.
fn one_desktop(sessions: Vec<Session>) -> InitialStateMessage {
    let panes = sessions
        .iter()
        .enumerate()
        .map(|(i, s)| pane("d1", &format!("p{i}"), &s.id))
        .collect();
    state(sessions, vec![slotted(desktop("d1", "a", "", panes), 1)])
}

fn the_queue() -> InitialStateMessage {
    queue(one_desktop(vec![
        crew("chief", "chief", true),
        turn("t-new", 5),
        session("w1", "working"),
        turn("t-old", 120),
        snoozed("s1", 360),
    ]))
}

/// The queue list's lines top to bottom, by key.
fn lines(world: &World) -> Vec<String> {
    world
        .screen()
        .children(Target::key("sidebar.queue"))
        .into_iter()
        .filter_map(|n| n.key)
        .collect()
}

fn name(world: &World, key: &str) -> String {
    world
        .screen()
        .get(Target::key(key))
        .name
        .unwrap_or_default()
}

fn shown(world: &World) -> Vec<String> {
    world
        .daemon
        .sent_of("desktop_show_session")
        .iter()
        .map(|m| m["session_id"].as_str().unwrap_or_default().to_owned())
        .collect()
}

#[test]
fn the_queue_sidebar_anchors_the_chief_above_the_owed_turns_oldest_first_and_keeps_the_rest_behind_the_list()
 {
    let world = World::start(Daemon::scripted().state(the_queue()));

    assert_eq!(
        lines(&world),
        [
            "queue.rule.crew",
            "queue-chief.chief",
            "queue.waiting-head",
            "queue-turn.t-old",
            "queue-turn.t-new",
            "queue.agents-toggle",
            "queue.agents-counts",
        ]
    );
    assert_eq!(name(&world, "queue.waiting-head"), "2 waiting");
    assert_eq!(name(&world, "queue-turn.t-old.age"), "2h");
    assert_eq!(name(&world, "queue-turn.t-new.age"), "5m");
    assert_eq!(name(&world, "queue-turn.t-old.slot"), "1");
    assert_eq!(name(&world, "queue.agents-toggle"), "2 more agents");
    assert_eq!(name(&world, "queue.agents-counts"), "1 working  1 snoozed");
}

#[test]
fn the_waiting_head_and_the_jump_key_open_the_oldest_turn_and_say_so_when_nothing_is_owed() {
    let mut world = World::start(Daemon::scripted().state(the_queue()));

    world.gesture(|app| app.click(Target::key("queue.waiting-head")));
    world.gesture(|app| app.key(chord(Action::SessionJumpToWaiting)));

    assert_eq!(shown(&world), ["t-old", "t-old"]);
    assert!(world.screen().find(Target::key("tiles")).is_some());

    let mut world =
        World::start(Daemon::scripted().state(queue(one_desktop(vec![session("w1", "working")]))));
    assert_eq!(name(&world, "queue.waiting-head"), "Nothing owed");
    world.gesture(|app| app.click(Target::key("queue.waiting-head")));
    world.gesture(|app| app.key(chord(Action::SessionJumpToWaiting)));
    assert!(shown(&world).is_empty());
}

#[test]
fn the_opened_list_shows_working_and_snoozed_agents_and_its_filter_narrows_them_never_the_turns() {
    let mut world = World::start(Daemon::scripted().state(the_queue()));

    world.gesture(|app| app.click(Target::key("queue.agents-toggle")));
    assert_eq!(
        lines(&world)[5..],
        [
            "queue.agents-toggle",
            "queue.filter",
            "queue.rule.working",
            "queue-settled.w1",
            "queue.rule.snoozed",
            "queue-snoozed.s1",
        ]
    );
    assert_eq!(name(&world, "queue.rule.working"), "Working 1");
    assert_eq!(name(&world, "queue.rule.snoozed"), "Snoozed 1");
    assert!(!name(&world, "queue-snoozed.s1.age").is_empty());

    world.gesture(|app| app.type_text("s1"));
    assert_eq!(
        lines(&world)[2..],
        [
            "queue.waiting-head",
            "queue-turn.t-old",
            "queue-turn.t-new",
            "queue.agents-toggle",
            "queue.filter",
            "queue.rule.working",
            "queue.empty.working",
            "queue.rule.snoozed",
            "queue-snoozed.s1",
        ]
    );

    world.gesture(|app| app.click(Target::key("queue.agents-toggle")));
    world.gesture(|app| app.click(Target::key("queue.agents-toggle")));
    assert!(
        lines(&world).contains(&"queue-settled.w1".to_owned()),
        "closing forgets the filter"
    );
}

#[test]
fn a_turn_age_moves_on_when_it_would_read_differently_and_not_before() {
    let mut world = World::start(Daemon::scripted().state(the_queue()));
    let five_and_a_half_minutes = 29.5;

    assert_eq!(
        world.runtime().wake_deadline(),
        Some(five_and_a_half_minutes)
    );
    world.advance(Duration::from_millis(29_499));
    assert_eq!(name(&world, "queue-turn.t-new.age"), "5m");
    world.advance(Duration::from_millis(1));
    assert_eq!(name(&world, "queue-turn.t-new.age"), "6m");
    assert_eq!(world.runtime().wake_deadline(), Some(89.5));
}

#[test]
fn desktop_flow_shows_no_ages_and_never_wakes_for_them() {
    let world = World::start(Daemon::scripted().state(one_desktop(vec![turn("t-new", 5)])));

    assert!(world.screen().find(Target::key("sidebar.queue")).is_none());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn collapsing_the_queue_sidebar_moves_the_waiting_pill_and_the_desktops_into_the_title_bar() {
    let mut world = World::start(Daemon::scripted().state(queue(one_desktop(vec![
        turn("a", 50),
        turn("b", 40),
        turn("c", 30),
        turn("d", 20),
    ]))));

    world.gesture(|app| app.click(Target::role("button").name("Collapse sidebar")));
    assert!(world.screen().find(Target::key("sidebar")).is_none());
    assert_eq!(name(&world, "queue-bar.pill"), "4 waiting · a › b › c +1");
    assert_eq!(
        name(&world, "queue-bar.desktop.1"),
        "Desktop 1, has turns waiting"
    );

    world.gesture(|app| app.click(Target::role("button").name("Show sidebar")));
    assert!(world.screen().find(Target::key("queue-bar")).is_none());
    assert!(world.screen().find(Target::key("sidebar.queue")).is_some());

    let mut world =
        World::start(Daemon::scripted().state(queue(one_desktop(vec![session("w1", "working")]))));
    world.gesture(|app| app.click(Target::role("button").name("Collapse sidebar")));
    assert_eq!(name(&world, "queue-bar.pill"), "Nothing owed");
}

#[test]
fn collapsing_the_desktop_sidebar_leaves_the_rail_with_home_and_the_desktops() {
    let mut message = one_desktop(vec![turn("t1", 5)]);
    message.desktops.as_mut().unwrap().extend([
        slotted(desktop("d2", "b", "Review", vec![]), 2),
        desktop("d3", "c", "Loose", vec![]),
    ]);
    let mut world = World::start(Daemon::scripted().state(message));

    world.gesture(|app| app.click(Target::role("button").name("Collapse sidebar")));
    let screen = world.screen();
    assert!(screen.find(Target::key("sidebar")).is_none());
    assert_eq!(
        screen.get(Target::key("rail.home")).value.as_deref(),
        Some("current")
    );
    assert_eq!(name(&world, "rail.desktop.2"), "Review");
    assert_eq!(
        name(&world, "rail.desktop-extras"),
        "1 more desktop without a shortcut"
    );

    world.gesture(|app| app.click(Target::key("rail.desktop.2")));
    let set = world.daemon.sent_of("desktop_set_current");
    assert_eq!(set.last().unwrap()["desktop_id"], "d2");
    assert_eq!(
        world
            .screen()
            .get(Target::key("rail.home"))
            .value
            .as_deref(),
        Some("")
    );

    world.gesture(|app| app.click(Target::role("button").name("Expand sidebar")));
    assert!(world.screen().find(Target::key("rail")).is_none());
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.desktops"))
            .is_some()
    );
}

#[test]
fn the_title_bar_switches_flow_through_the_daemon_setting() {
    let mut world = World::start(Daemon::scripted().state(one_desktop(vec![])));

    world.gesture(|app| app.click(Target::role("button").name("Switch to queue flow")));

    let set = world.daemon.sent_of("set_setting");
    assert_eq!(set.len(), 1);
    assert_eq!(set[0]["key"], "queue_mode_enabled");
    assert_eq!(set[0]["value"], "true");
}

#[test]
fn the_previous_and_next_keys_walk_the_queue_from_the_agent_on_screen_and_wrap() {
    let mut world = World::start(Daemon::scripted().state(queue(one_desktop(vec![
        turn("t-old", 120),
        turn("t-new", 5),
        session("w1", "working"),
        snoozed("s1", 360),
    ]))));
    world.gesture(|app| app.click(Target::key("queue-turn.t-old")));

    world.gesture(|app| app.key(chord(Action::SessionNext)));
    world.gesture(|app| app.key(chord(Action::SessionPrev)));

    assert_eq!(shown(&world), ["t-old", "t-new", "s1"]);
}

#[test]
fn the_desktop_strip_puts_each_slotted_desktop_on_a_chip_marked_where_a_turn_waits() {
    let mut message = queue(one_desktop(vec![turn("t1", 5)]));
    message.desktops.as_mut().unwrap().extend([
        slotted(desktop("d2", "b", "Review", vec![]), 2),
        desktop("d3", "c", "Loose", vec![]),
    ]);
    let mut world = World::start(Daemon::scripted().state(message));

    assert_eq!(
        world
            .screen()
            .children(Target::key("queue.desktops"))
            .into_iter()
            .filter_map(|n| n.name)
            .collect::<Vec<_>>(),
        [
            "Desktop 1, has turns waiting",
            "Review",
            "1 more desktop without a shortcut"
        ]
    );

    world.gesture(|app| app.click(Target::key("queue.desktop.2")));
    let set = world.daemon.sent_of("desktop_set_current");
    assert_eq!(set.last().unwrap()["desktop_id"], "d2");
}

#[test]
fn a_hidden_agent_owing_a_turn_queues_by_default_and_a_crew_member_does_not() {
    let mut message = queue(one_desktop(vec![crew("mate", "mate", false)]));
    message.sessions.as_mut().unwrap().push(Session {
        hidden: Some(true),
        ..turn("h1", 5)
    });
    let world = World::start(Daemon::scripted().state(message));

    assert_eq!(
        lines(&world),
        [
            "queue.rule.crew",
            "queue-crew.mate",
            "queue.waiting-head",
            "queue-turn.h1",
            "queue.agents-toggle",
        ]
    );
}
