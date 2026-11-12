use std::collections::BTreeMap;

use std::time::Duration;

use attn_app::Action;
use attn_protocol::{
    Desktop, InitialStateMessage, Profile, ProfileArrangementChangedMessage, Session,
    SessionStateChangedMessage, SessionUnregisteredMessage,
};
use attn_testkit::{Daemon, Target, WALL_START_MS, World};

use super::sidebar::{chord, desktop, pane, session, state};

/// An RFC 3339 instant `minutes` before every world starts.
pub(super) fn ago(minutes: i64) -> String {
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

pub(super) fn turn(id: &str, opened_minutes_ago: i64) -> Session {
    Session {
        turn_owed: Some(true),
        turn_opened_at: Some(ago(opened_minutes_ago)),
        ..session(id, "waiting_input")
    }
}

pub(super) fn queue(mut message: InitialStateMessage) -> InitialStateMessage {
    message.settings = Some(BTreeMap::from([(
        "queue_mode_enabled".to_owned(),
        "true".to_owned(),
    )]));
    message
}

pub(super) fn crew(id: &str, member: &str, chief: bool) -> Session {
    Session {
        crew_member: Some(member.into()),
        chief_of_staff: chief.then_some(true),
        ..session(id, "working")
    }
}

pub(super) fn snoozed(id: &str, minutes_ahead: i64) -> Session {
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
pub(super) fn one_desktop(sessions: Vec<Session>) -> InitialStateMessage {
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
fn a_hidden_agent_owing_a_turn_queues_by_default_and_a_crew_member_or_another_profile_s_does_not() {
    let mut message = queue(one_desktop(vec![crew("mate", "mate", false)]));
    message.sessions.as_mut().unwrap().extend([
        Session {
            hidden: Some(true),
            ..turn("h1", 5)
        },
        Session {
            hidden: Some(true),
            profile_id: "home".into(),
            ..turn("elsewhere", 5)
        },
    ]);
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

pub(super) fn sent_ids(world: &World, cmd: &str) -> Vec<String> {
    world
        .daemon
        .sent_of(cmd)
        .iter()
        .map(|m| m["session_id"].as_str().unwrap_or_default().to_owned())
        .collect()
}

/// The daemon shows `session` on desktop d1, as it does once the app asked it to.
pub(super) fn daemon_shows(world: &mut World, sessions: &[&Session], session: &str) {
    let panes: Vec<_> = sessions
        .iter()
        .enumerate()
        .map(|(i, s)| pane("d1", &format!("p{i}"), &s.id))
        .collect();
    let mut d1 = slotted(desktop("d1", "a", "", panes), 1);
    d1.active_pane_id = format!(
        "p{}",
        sessions.iter().position(|s| s.id == session).unwrap()
    );
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: "d1".into(),
            ..Profile::default()
        },
        desktops: vec![d1],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
}

pub(super) fn changes(world: &mut World, session: Session) {
    world.daemon.emit(SessionStateChangedMessage {
        session,
        ..SessionStateChangedMessage::new()
    });
    world.settle();
}

fn settled(id: &str) -> Session {
    session(id, "working")
}

#[test]
fn a_row_settles_its_turn_and_unsnoozes_its_agent_without_opening_it() {
    let mut world = World::start(Daemon::scripted().state(the_queue()));

    world.gesture(|app| app.click(Target::role("button").name("Settle t-old")));
    world.gesture(|app| app.click(Target::key("queue.agents-toggle")));
    world.gesture(|app| app.click(Target::role("button").name("Unsnooze s1")));

    assert_eq!(sent_ids(&world, "settle_turn"), ["t-old"]);
    assert_eq!(sent_ids(&world, "wake_turn"), ["s1"]);
    assert!(shown(&world).is_empty());
    assert!(
        world
            .screen()
            .find(Target::key("queue-turn.t-new.settle"))
            .is_some()
    );
    assert!(
        world
            .screen()
            .find(Target::key("queue-settled.w1.settle"))
            .is_none()
    );
    assert!(
        world
            .screen()
            .find(Target::key("queue-chief.chief.settle"))
            .is_none()
    );
}

#[test]
fn when_the_turn_on_screen_closes_the_window_moves_to_the_next_turn_then_home_to_follow_the_next() {
    let (a, b) = (turn("a", 30), turn("b", 20));
    let mut world =
        World::start(Daemon::scripted().state(queue(one_desktop(vec![a.clone(), b.clone()]))));
    world.gesture(|app| app.click(Target::key("queue-turn.a")));
    assert_eq!(shown(&world), ["a"]);

    changes(&mut world, settled("a"));
    assert_eq!(shown(&world), ["a", "b"], "the next turn opens");

    daemon_shows(&mut world, &[&a, &b], "b");
    changes(&mut world, settled("b"));
    assert_eq!(
        world
            .screen()
            .get(Target::key("sidebar.home"))
            .value
            .as_deref(),
        Some("current"),
        "home after the last turn"
    );

    changes(&mut world, turn("a", 0));
    assert_eq!(
        shown(&world),
        ["a", "b", "a"],
        "home follows the next turn to open"
    );
}

#[test]
fn a_turn_the_agent_closes_itself_keeps_the_window_where_it_is() {
    let (a, b) = (turn("a", 30), turn("b", 20));
    let mut world =
        World::start(Daemon::scripted().state(queue(one_desktop(vec![a.clone(), b.clone()]))));
    world.gesture(|app| app.click(Target::key("queue-turn.a")));

    changes(
        &mut world,
        Session {
            turn_by_agent: Some(true),
            ..settled("a")
        },
    );

    assert_eq!(shown(&world), ["a"]);
}

#[test]
fn going_home_by_hand_ends_following_the_next_turn() {
    let a = turn("a", 30);
    let mut world = World::start(Daemon::scripted().state(queue(one_desktop(vec![
        a.clone(),
        session("w1", "working"),
    ]))));
    world.gesture(|app| app.click(Target::key("queue-turn.a")));
    changes(&mut world, settled("a"));
    world.gesture(|app| app.click(Target::key("queue.desktop.1")));
    world.gesture(|app| app.click(Target::key("sidebar.home")));

    changes(&mut world, turn("w1", 0));

    assert_eq!(shown(&world), ["a"]);
}

#[test]
fn the_settle_key_settles_the_turn_on_screen_and_moves_on_from_an_agent_with_none() {
    let (a, w) = (turn("a", 30), session("w1", "working"));
    let mut world =
        World::start(Daemon::scripted().state(queue(one_desktop(vec![a.clone(), w.clone()]))));
    world.gesture(|app| app.click(Target::key("queue-turn.a")));
    world.gesture(|app| app.key(chord(Action::SessionSettle)));
    assert_eq!(sent_ids(&world, "settle_turn"), ["a"]);

    daemon_shows(&mut world, &[&a, &w], "w1");
    world.gesture(|app| app.key(chord(Action::SessionSettle)));
    assert_eq!(sent_ids(&world, "settle_turn"), ["a"]);
    assert_eq!(
        shown(&world),
        ["a", "a"],
        "a settled agent moves on to the head of the queue"
    );
}

#[test]
fn the_priority_key_puts_the_agent_on_screen_ahead_of_the_other_turns_and_back() {
    let a = turn("a", 30);
    let mut world = World::start(Daemon::scripted().state(queue(one_desktop(vec![a.clone()]))));
    world.gesture(|app| app.click(Target::key("queue-turn.a")));

    world.gesture(|app| app.key(chord(Action::SessionPriority)));
    changes(
        &mut world,
        Session {
            priority: Some(true),
            ..a
        },
    );
    world.gesture(|app| app.key(chord(Action::SessionPriority)));

    let sent: Vec<_> = world
        .daemon
        .sent_of("set_session_priority")
        .iter()
        .map(|m| m["priority"].clone())
        .collect();
    assert_eq!(sent, [true, false]);
}

#[test]
fn the_settle_key_goes_home_from_a_settled_agent_when_no_turn_is_owed() {
    let mut world =
        World::start(Daemon::scripted().state(queue(one_desktop(vec![session("w1", "working")]))));
    world.gesture(|app| app.click(Target::key("queue.desktop.1")));

    world.gesture(|app| app.key(chord(Action::SessionSettle)));

    assert_eq!(
        world
            .screen()
            .get(Target::key("sidebar.home"))
            .value
            .as_deref(),
        Some("current")
    );
    assert!(world.daemon.sent_of("settle_turn").is_empty());
}

#[test]
fn a_closed_turn_in_the_middle_of_the_queue_moves_on_to_a_priority_head() {
    let (a, b, c) = (turn("a", 30), turn("b", 20), turn("c", 10));
    let mut world = World::start(Daemon::scripted().state(queue(one_desktop(vec![
        a.clone(),
        b.clone(),
        c.clone(),
    ]))));
    world.gesture(|app| app.click(Target::key("queue-turn.a")));
    daemon_shows(&mut world, &[&a, &b, &c], "b");
    changes(
        &mut world,
        Session {
            priority: Some(true),
            ..c
        },
    );

    changes(&mut world, settled("b"));

    assert_eq!(shown(&world), ["a", "c"]);
}

fn untils(world: &World, cmd: &str) -> Vec<(String, String)> {
    world
        .daemon
        .sent_of(cmd)
        .iter()
        .map(|m| {
            (
                m["session_id"].as_str().unwrap_or_default().to_owned(),
                m["until"].as_str().unwrap_or_default().to_owned(),
            )
        })
        .collect()
}

#[test]
fn a_row_snoozes_its_agent_for_the_time_the_menu_shows() {
    let mut world = World::start(Daemon::scripted().state(the_queue()));

    world.gesture(|app| app.click(Target::role("button").name("Snooze t-old")));
    let menu = world
        .screen()
        .get(Target::role("menu").name("Snooze t-old"));
    assert_eq!(menu.key.as_deref(), Some("snooze.menu.snooze"));
    let choices: Vec<_> = world
        .screen()
        .all(Target::role("menu-item"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert_eq!(
        choices,
        [
            "For 30 minutes",
            "For an hour",
            "For 2 hours",
            "For 4 hours",
            "For 8 hours",
            "Until tomorrow",
            "Until Saturday",
            "Until Monday",
            "Standby until…",
        ]
    );

    world.gesture(|app| app.click(Target::role("menu-item").name("For an hour")));

    assert_eq!(
        untils(&world, "snooze_turn"),
        [("t-old".to_owned(), "2026-11-12T10:00:00.000Z".to_owned())]
    );
    assert!(world.screen().find(Target::role("menu")).is_none());
    assert!(shown(&world).is_empty());
}

#[test]
fn standby_from_the_snooze_menu_puts_the_agent_on_standby_until_the_choice() {
    let mut world = World::start(Daemon::scripted().state(the_queue()));

    world.gesture(|app| app.click(Target::role("button").name("Snooze t-new")));
    world.gesture(|app| app.click(Target::role("menu-item").name("Standby until…")));
    assert!(
        world
            .screen()
            .find(Target::role("menu").name("Standby t-new until"))
            .is_some()
    );
    world.gesture(|app| app.click(Target::role("menu-item").name("For 30 minutes")));

    assert_eq!(
        untils(&world, "standby_session"),
        [("t-new".to_owned(), "2026-11-12T09:30:00.000Z".to_owned())]
    );
    assert!(untils(&world, "snooze_turn").is_empty());
}

#[test]
fn escape_closes_the_snooze_menu_and_snoozes_nothing() {
    let mut world = World::start(Daemon::scripted().state(the_queue()));

    world.gesture(|app| app.click(Target::role("button").name("Snooze t-old")));
    world.gesture(|app| app.key(plain(rune::Key::Escape)));

    assert!(world.screen().find(Target::role("menu")).is_none());
    assert!(untils(&world, "snooze_turn").is_empty());
}

#[test]
fn the_snooze_key_opens_the_menu_for_the_agent_on_screen_and_the_arrows_choose() {
    let mut world = World::start(Daemon::scripted().state(queue(one_desktop(vec![
        turn("t-old", 120),
        turn("t-new", 5),
    ]))));
    world.gesture(|app| app.click(Target::key("queue-turn.t-old")));

    world.gesture(|app| app.key(chord(Action::SessionSnooze)));
    assert!(
        world
            .screen()
            .find(Target::role("menu").name("Snooze chief"))
            .is_none()
    );
    assert!(
        world
            .screen()
            .find(Target::role("menu").name("Snooze t-old"))
            .is_some()
    );
    world.gesture(|app| app.key(plain(rune::Key::Down)));
    world.gesture(|app| app.key(plain(rune::Key::Enter)));

    assert_eq!(
        untils(&world, "snooze_turn"),
        [("t-old".to_owned(), "2026-11-12T10:00:00.000Z".to_owned())]
    );
}

#[test]
fn the_snooze_menu_reads_the_time_it_opens_at_after_a_quiet_daemon() {
    let mut world =
        World::start(Daemon::scripted().state(queue(one_desktop(vec![session("w1", "working")]))));
    world.gesture(|app| app.click(Target::key("queue.desktop.1")));
    world.advance(Duration::from_secs(2 * 3600));

    world.gesture(|app| app.key(chord(Action::SessionSnooze)));
    world.gesture(|app| app.key(plain(rune::Key::Down)));
    world.gesture(|app| app.key(plain(rune::Key::Enter)));

    assert_eq!(
        untils(&world, "snooze_turn"),
        [("w1".to_owned(), "2026-11-12T12:00:00.000Z".to_owned())]
    );
}

#[test]
fn the_snooze_menu_closes_when_its_agent_goes_away() {
    let mut world = World::start(Daemon::scripted().state(the_queue()));
    world.gesture(|app| app.click(Target::role("button").name("Snooze t-old")));

    world.daemon.emit(SessionUnregisteredMessage {
        session: turn("t-old", 120),
        ..SessionUnregisteredMessage::new()
    });
    world.settle();

    assert!(world.screen().find(Target::role("menu")).is_none());
}

fn plain(key: rune::Key) -> rune::KeyEvent {
    rune::KeyEvent {
        key,
        mods: rune::Mods::default(),
    }
}

fn settling(id: &str, minutes_ahead: i64) -> Session {
    Session {
        auto_settle_fires_at: Some(ago(-minutes_ahead)),
        ..turn(id, 5)
    }
}

#[test]
fn an_agent_whose_tile_is_off_screen_shows_its_countdown_on_its_row_until_it_settles() {
    let mut world = World::start(Daemon::scripted().state(one_desktop(vec![
        settling("a", 1),
        Session {
            auto_settle_held: Some(true),
            ..turn("b", 5)
        },
    ])));

    assert_eq!(name(&world, "sidebar.session.a.settling"), "Settling");
    assert_eq!(
        name(&world, "sidebar.session.b.settling"),
        "Settling paused"
    );

    world.advance(Duration::from_secs(61));
    assert!(
        world.runtime_is_at_rest(),
        "the drain stops at the deadline"
    );

    world.open_desktop();
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.session.a.settling"))
            .is_none(),
        "the tile shows it"
    );
}

#[test]
fn the_cancel_countdown_key_stops_the_countdowns_on_screen_or_keeps_the_next_turn_of_the_agent_shown()
 {
    let mut world =
        World::start(Daemon::scripted().state(one_desktop(vec![settling("a", 1), turn("b", 5)])));
    world.gesture(|app| app.key(chord(Action::SessionCancelCountdown)));
    assert!(
        sent_ids(&world, "cancel_countdown").is_empty(),
        "nothing is on screen at home"
    );

    world.open_desktop();
    world.gesture(|app| app.key(chord(Action::SessionCancelCountdown)));
    assert_eq!(sent_ids(&world, "cancel_countdown"), ["a"]);

    changes(&mut world, turn("a", 5));
    world.gesture(|app| app.key(chord(Action::SessionCancelCountdown)));
    assert_eq!(
        sent_ids(&world, "cancel_countdown"),
        ["a", "a"],
        "with nothing counting, the agent shown keeps its next turn"
    );
}

#[test]
fn a_countdown_that_leaves_the_screen_drains_only_the_time_it_has_left() {
    let mut world = World::start(Daemon::scripted().state(one_desktop(vec![turn("a", 5)])));
    world.open_desktop();
    changes(&mut world, settling("a", 1));
    world.advance(Duration::from_secs(50));

    world.gesture(|app| app.click(Target::key("sidebar.home")));
    assert_eq!(name(&world, "sidebar.session.a.settling"), "Settling");
    world.advance(Duration::from_secs(11));

    assert!(
        world.runtime_is_at_rest(),
        "the bar empties at the deadline"
    );
}

fn actions_for(world: &mut World, row: &str) {
    world.gesture(|app| app.click(Target::key(format!("{row}.actions"))));
}

#[test]
fn a_row_s_actions_menu_marks_priority_reloads_and_closes_the_agent() {
    let mut world = World::start(Daemon::scripted().state(the_queue()));
    world.gesture(|app| app.click(Target::key("queue.agents-toggle")));
    for row in [
        "queue-chief.chief",
        "queue-turn.t-old",
        "queue-settled.w1",
        "queue-snoozed.s1",
    ] {
        assert!(
            world
                .screen()
                .find(Target::key(format!("{row}.actions")))
                .is_some(),
            "{row}"
        );
    }

    actions_for(&mut world, "queue-turn.t-old");
    assert!(
        world
            .screen()
            .find(Target::role("menu").name("Actions for t-old"))
            .is_some()
    );
    world.gesture(|app| app.click(Target::key("session.menu.priority")));
    let priority = world.daemon.sent_of("set_session_priority");
    assert_eq!(
        (
            priority[0]["session_id"].as_str(),
            priority[0]["priority"].as_bool()
        ),
        (Some("t-old"), Some(true))
    );
    assert!(world.screen().find(Target::role("menu")).is_none());

    actions_for(&mut world, "queue-turn.t-old");
    world.gesture(|app| app.click(Target::key("session.menu.reload")));
    assert_eq!(
        world.daemon.sent_of("reload_session")[0]["id"].as_str(),
        Some("t-old")
    );

    actions_for(&mut world, "queue-turn.t-new");
    world.gesture(|app| app.click(Target::key("session.menu.close")));
    let closed: Vec<_> = world
        .daemon
        .sent_of("unregister")
        .iter()
        .map(|m| m["id"].as_str().unwrap_or_default().to_owned())
        .collect();
    assert_eq!(closed, ["t-new"]);
}

#[test]
fn closing_the_agent_on_screen_in_queue_flow_opens_the_next_turn() {
    let mut world = World::start(
        Daemon::scripted().state(queue(one_desktop(vec![turn("a", 30), turn("b", 20)]))),
    );
    world.gesture(|app| app.click(Target::key("queue-turn.a")));
    daemon_shows(&mut world, &[&turn("a", 30), &turn("b", 20)], "a");

    actions_for(&mut world, "queue-turn.a");
    world.gesture(|app| app.click(Target::key("session.menu.close")));

    assert_eq!(shown(&world), ["a", "b"]);
}

#[test]
fn a_crew_member_is_protected_from_closing_and_the_chief_offers_no_close() {
    let mut world = World::start(Daemon::scripted().state(the_queue()));

    actions_for(&mut world, "queue-chief.chief");
    assert!(
        world
            .screen()
            .find(Target::key("session.menu.close"))
            .is_none()
    );
    world.gesture(|app| app.key(plain(rune::Key::Escape)));

    let mut world = World::start(
        Daemon::scripted().state(queue(one_desktop(vec![crew("mate", "mate", false)]))),
    );
    actions_for(&mut world, "queue-crew.mate");
    world.gesture(|app| app.click(Target::key("session.menu.close")));
    assert!(world.daemon.sent_of("unregister").is_empty());
    assert_eq!(
        world
            .screen()
            .find(Target::key("notice.text"))
            .and_then(|n| n.name)
            .as_deref(),
        Some("mate is protected — ask mate to close to end its session.")
    );
}

#[test]
fn rename_session_sends_the_typed_name_and_refuses_an_empty_one() {
    let mut world = World::start(Daemon::scripted().state(the_queue()));
    actions_for(&mut world, "queue-turn.t-old");
    world.gesture(|app| app.click(Target::key("session.menu.rename")));

    world.gesture(|app| app.type_text("   "));
    world.gesture(|app| app.key(plain(rune::Key::Enter)));
    assert!(
        world
            .screen()
            .find(Target::key("session.rename.refused"))
            .is_some()
    );
    assert!(world.daemon.sent_of("rename_session").is_empty());

    world.gesture(|app| app.key(plain(rune::Key::Escape)));
    actions_for(&mut world, "queue-turn.t-old");
    world.gesture(|app| app.click(Target::key("session.menu.rename")));
    world.gesture(|app| app.type_text("docs pass"));
    world.gesture(|app| app.key(plain(rune::Key::Enter)));

    let renamed = world.daemon.sent_of("rename_session");
    assert_eq!(
        (
            renamed[0]["session_id"].as_str(),
            renamed[0]["label"].as_str()
        ),
        (Some("t-old"), Some("docs pass"))
    );
    assert!(world.screen().find(Target::role("dialog")).is_none());
}

#[test]
fn the_actions_menu_closes_when_its_agent_goes_away() {
    let mut world = World::start(Daemon::scripted().state(the_queue()));
    actions_for(&mut world, "queue-turn.t-old");

    world.daemon.emit(SessionUnregisteredMessage {
        session: turn("t-old", 120),
        ..SessionUnregisteredMessage::new()
    });
    world.settle();

    assert!(world.screen().find(Target::role("menu")).is_none());
}
