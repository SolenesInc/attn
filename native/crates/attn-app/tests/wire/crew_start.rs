//! Crew members in the sidebar: every member of the profile in the queue's crew block, a start
//! that takes two clicks for a member between sessions, and Ask to close for a member in one.

use std::time::Duration;

use attn_protocol::{
    CrewCloseResultMessage, CrewMember, CrewStartResultMessage, InitialStateMessage, Session,
};
use attn_testkit::{Daemon, Request, Target, World, message};
use rune::{Key, KeyEvent, Mods};
use serde_json::{Value, json};

use super::queue::{crew, one_desktop, queue};
use super::sidebar::session;

fn member(id: &str) -> CrewMember {
    let mut name = id.to_owned();
    name[..1].make_ascii_uppercase();
    CrewMember {
        id: id.into(),
        name,
        profile_id: "work".into(),
        revision: 1,
        resolved_agent: "claude".into(),
        ..CrewMember::default()
    }
}

fn roster() -> Vec<CrewMember> {
    vec![member("alder"), member("keel"), member("trellis")]
}

fn keel_day() -> Session {
    crew("sess-keel", "keel", false)
}

fn in_queue(sessions: Vec<Session>, crew: Vec<CrewMember>) -> InitialStateMessage {
    InitialStateMessage {
        crew: Some(crew),
        ..queue(one_desktop(sessions))
    }
}

fn start(state: InitialStateMessage) -> World {
    let mut world = World::start(Daemon::scripted().state(state));
    world.settle();
    world
}

fn starting(world: &World) {
    world.daemon.on("crew_start", |r: &Request| {
        let member = r["member"].as_str().unwrap_or_default();
        let member = member.strip_prefix("member:").unwrap_or(member).to_owned();
        message(CrewStartResultMessage {
            success: true,
            session_id: Some(format!("sess-{member}")),
            member: Some(member),
            ..CrewStartResultMessage::new()
        })
    });
}

fn click(world: &mut World, key: &str) {
    world
        .automate("press", json!({"target": {"key": key}}))
        .unwrap_or_else(|why| panic!("press {key}: {why}"));
}

fn press(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

/// Puts the pointer over the row keyed `key`, which shows its buttons.
pub(super) fn hover(world: &mut World, key: &str) {
    let [x, y, w, h] = world
        .screen()
        .bounds(Target::key(key))
        .unwrap_or_else(|| panic!("{key} is on screen"));
    world.gesture(|app| app.move_to((x + w / 2.0, y + h / 2.0)));
}

fn row(member: &str) -> String {
    format!("queue-crew.member.{member}")
}

fn sun(member: &str) -> String {
    format!("{}.start", row(member))
}

fn named(world: &World, key: &str) -> Option<String> {
    world.screen().find(Target::key(key)).and_then(|n| n.name)
}

fn shows(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

fn armed(world: &World, member: &str) -> bool {
    world
        .screen()
        .get(Target::key(row(member)))
        .value
        .as_deref()
        == Some("armed")
}

fn starts(world: &World) -> Vec<Value> {
    world
        .daemon
        .sent_of("crew_start")
        .iter()
        .map(|r| r["member"].clone())
        .collect()
}

fn notice(world: &World) -> Option<String> {
    named(world, "notice.text")
}

fn crew_lines(world: &World) -> Vec<String> {
    world
        .screen()
        .children(Target::key("sidebar.queue"))
        .into_iter()
        .filter_map(|n| n.key)
        .filter(|k| k.starts_with("queue-crew."))
        .collect()
}

#[test]
fn the_crew_block_draws_every_member_by_name_in_session_or_not_under_a_manage_link() {
    let mut world = start(in_queue(vec![session("s1", "idle"), keel_day()], roster()));
    assert_eq!(
        crew_lines(&world),
        [
            "queue-crew.member.alder",
            "queue-crew.sess-keel",
            "queue-crew.member.trellis"
        ]
    );
    assert_eq!(
        named(&world, &sun("trellis")).as_deref(),
        Some("Start Trellis")
    );
    assert!(!shows(&world, &sun("keel")));
    assert!(shows(&world, "queue-crew.sess-keel.ask-close"));
    click(&mut world, "queue.rule.crew.manage");
    assert!(shows(&world, "crew-dialog"));
}

#[test]
fn a_member_in_session_shows_once_on_its_own_row_while_its_turn_is_owed() {
    let owed = Session {
        turn_owed: Some(true),
        turn_opened_at: Some(super::queue::ago(5)),
        ..keel_day()
    };
    let world = start(in_queue(vec![session("s1", "idle"), owed], roster()));
    let keel: Vec<String> = world
        .screen()
        .children(Target::key("sidebar.queue"))
        .into_iter()
        .filter_map(|n| n.key)
        .filter(|k| k.contains("keel"))
        .collect();
    assert_eq!(keel, ["queue-crew.sess-keel"]);
}

#[test]
fn a_bound_member_whose_session_is_not_yet_in_the_queue_keeps_its_row() {
    let world = start(in_queue(
        vec![session("s1", "idle")],
        vec![CrewMember {
            binding_session: Some("sess-keel".into()),
            ..member("keel")
        }],
    ));
    assert_eq!(crew_lines(&world), ["queue-crew.member.keel"]);
}

#[test]
fn a_day_whose_member_left_the_roster_still_has_its_row() {
    let world = start(in_queue(
        vec![session("s1", "idle"), crew("sess-ghost", "sable", false)],
        roster(),
    ));
    assert!(crew_lines(&world).contains(&"queue-crew.sess-ghost".to_owned()));
}

#[test]
fn with_the_queue_off_members_between_sessions_sit_above_the_desktops() {
    let mut state = one_desktop(vec![keel_day()]);
    state.crew = Some(vec![
        member("alder"),
        CrewMember {
            binding_session: Some("sess-keel".into()),
            ..member("keel")
        },
    ]);
    let mut world = start(state);
    assert!(shows(&world, "sidebar.crew.alder"));
    assert!(!shows(&world, "sidebar.crew.keel"));
    click(&mut world, "sidebar.rule.crew.manage");
    assert!(shows(&world, "crew-dialog"));
}

#[test]
fn starting_takes_a_second_click_that_says_so_and_shows_the_session_the_daemon_names() {
    let mut world = start(in_queue(vec![session("s1", "idle")], roster()));
    starting(&world);
    click(&mut world, &sun("trellis"));
    assert!(starts(&world).is_empty());
    assert!(armed(&world, "trellis"));
    assert_eq!(
        named(&world, &sun("trellis")).as_deref(),
        Some("Start Trellis — click again to confirm")
    );
    assert!(shows(&world, &format!("{}.confirm", row("trellis"))));
    click(&mut world, &sun("trellis"));
    assert_eq!(starts(&world), [json!("member:trellis")]);
}

#[test]
fn the_row_arms_and_the_sun_confirms_as_one_gesture() {
    let mut world = start(in_queue(vec![session("s1", "idle")], roster()));
    starting(&world);
    click(&mut world, &row("trellis"));
    assert!(starts(&world).is_empty());
    click(&mut world, &sun("trellis"));
    assert_eq!(starts(&world), [json!("member:trellis")]);
}

#[test]
fn clicks_on_the_breaking_sun_start_the_member_once() {
    let mut world = start(in_queue(vec![session("s1", "idle")], roster()));
    starting(&world);
    for _ in 0..4 {
        click(&mut world, &sun("trellis"));
    }
    assert_eq!(starts(&world), [json!("member:trellis")]);
}

type StandDown = fn(&mut World);

#[test]
fn an_armed_start_stands_down_on_escape_a_press_elsewhere_another_member_or_its_timeout() {
    let stand_downs: [(&str, StandDown); 4] = [
        ("escape", |w| press(w, Key::Escape)),
        ("a press elsewhere", |w| {
            w.gesture(|app| app.click(Target::key("queue.waiting-head")))
        }),
        ("another member", |w| click(w, &row("alder"))),
        ("the timeout", |w| w.advance(Duration::from_secs(4))),
    ];
    for (why, stand_down) in stand_downs {
        let mut world = start(in_queue(vec![session("s1", "idle")], roster()));
        starting(&world);
        click(&mut world, &sun("trellis"));
        stand_down(&mut world);
        assert!(!armed(&world, "trellis"), "{why} stands the start down");
        click(&mut world, &sun("trellis"));
        assert!(
            starts(&world).is_empty(),
            "{why}: the next click arms again"
        );
    }
}

#[test]
fn an_armed_start_holds_until_just_before_its_timeout() {
    let mut world = start(in_queue(vec![session("s1", "idle")], roster()));
    click(&mut world, &sun("trellis"));
    world.advance(Duration::from_millis(3990));
    assert!(armed(&world, "trellis"));
    world.advance(Duration::from_millis(20));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_start_that_could_not_be_shown_and_a_refused_start_each_say_why() {
    let mut world = start(in_queue(vec![session("s1", "idle")], roster()));
    world.daemon.answer_in_turn(
        "crew_start",
        [
            attn_testkit::Answer::Reply(message(CrewStartResultMessage {
                success: true,
                session_id: Some("sess-keel".into()),
                show_error: Some("Session closed before it could be shown".into()),
                ..CrewStartResultMessage::new()
            })),
            attn_testkit::Answer::Reply(message(CrewStartResultMessage {
                success: false,
                error: Some("keel launches in /gone, which is not there".into()),
                ..CrewStartResultMessage::new()
            })),
        ],
    );
    click(&mut world, &sun("keel"));
    click(&mut world, &sun("keel"));
    assert_eq!(
        notice(&world).as_deref(),
        Some(
            "The member's session started, but showing it failed: Session closed before it could be shown"
        )
    );
    world.advance(Duration::from_secs(1));
    click(&mut world, &sun("keel"));
    click(&mut world, &sun("keel"));
    assert_eq!(
        notice(&world).as_deref(),
        Some("keel launches in /gone, which is not there")
    );
}

#[test]
fn ask_to_close_asks_the_member_in_session_and_a_refusal_says_why() {
    let mut world = start(in_queue(vec![session("s1", "idle"), keel_day()], roster()));
    world.daemon.answer_in_turn(
        "crew_close",
        [
            attn_testkit::Answer::Reply(message(CrewCloseResultMessage {
                success: true,
                ..CrewCloseResultMessage::new()
            })),
            attn_testkit::Answer::Reply(message(CrewCloseResultMessage {
                success: false,
                error: Some("session sess-keel cannot receive agent messages".into()),
                ..CrewCloseResultMessage::new()
            })),
        ],
    );
    assert_eq!(
        named(&world, "queue-crew.sess-keel.ask-close").as_deref(),
        Some("Ask Keel to close")
    );
    hover(&mut world, "queue-crew.sess-keel");
    click(&mut world, "queue-crew.sess-keel.ask-close");
    assert_eq!(
        world.daemon.sent_of("crew_close")[0]["member"],
        "member:keel"
    );
    assert_eq!(notice(&world), None);
    click(&mut world, "queue-crew.sess-keel.ask-close");
    assert_eq!(
        notice(&world).as_deref(),
        Some("session sess-keel cannot receive agent messages")
    );
}

#[test]
fn a_members_details_open_from_its_row_in_session_or_not() {
    let mut world = start(in_queue(vec![session("s1", "idle"), keel_day()], roster()));
    hover(&mut world, &row("alder"));
    click(&mut world, &format!("{}.actions", row("alder")));
    click(&mut world, "crew-member-menu.details");
    assert_eq!(named(&world, "crew.heading").as_deref(), Some("Alder"));
    press(&mut world, Key::Escape);
    hover(&mut world, "queue-crew.sess-keel");
    click(&mut world, "queue-crew.sess-keel.actions");
    click(&mut world, "session.menu.member-details");
    assert_eq!(named(&world, "crew.heading").as_deref(), Some("Keel"));
}

#[test]
fn a_member_in_sessions_row_opens_its_day() {
    let mut world = start(in_queue(vec![session("s1", "idle"), keel_day()], roster()));
    click(&mut world, "queue-crew.sess-keel");
    let shown: Vec<Value> = world
        .daemon
        .sent_of("desktop_show_session")
        .iter()
        .map(|m| m["session_id"].clone())
        .collect();
    assert_eq!(shown, [json!("sess-keel")]);
}
