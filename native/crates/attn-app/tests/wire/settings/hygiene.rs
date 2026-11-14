use std::time::Duration;

use attn_protocol::{
    AuthorState, AuthorsUpdatedMessage, InitialStateMessage, Profile, RepoState,
    ReposUpdatedMessage, SettingsUpdatedMessage,
};
use attn_testkit::{Daemon, Target, World};
use rune::Key;

use super::{
    accepting, click, field, named, nav, open, press, press_close, rest, retype, value, writes,
};

const DAEMON_WINDOWS: &[(&str, &str)] = &[
    ("auto_settle_arm_seconds", "30"),
    ("auto_settle_countdown_seconds", "15"),
];

fn on_hygiene(world: &mut World) {
    open(world);
    click(world, "settings.nav.hygiene");
}

fn checked(world: &World, setting: &str) -> bool {
    world
        .screen()
        .get(Target::key(format!("settings.switch.{setting}")))
        .is("checked")
}

fn pills(world: &World) -> Vec<String> {
    world
        .screen()
        .children(Target::key("settings.pills"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn listed(world: &World, list: &str) -> Vec<String> {
    world
        .screen()
        .children(Target::key(list))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

#[test]
fn the_auto_settle_windows_follow_the_daemon_and_leaving_an_unchanged_one_writes_nothing() {
    let (daemon, _) = accepting(DAEMON_WINDOWS);
    let mut world = World::start(daemon);
    on_hygiene(&mut world);
    let arm = field("auto_settle_arm_seconds");
    let countdown = field("auto_settle_countdown_seconds");
    assert_eq!(value(&world, &arm), "30");
    assert_eq!(value(&world, &countdown), "15");

    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some(
            [
                ("auto_settle_arm_seconds".to_owned(), "60".to_owned()),
                ("auto_settle_countdown_seconds".to_owned(), "20".to_owned()),
            ]
            .into(),
        ),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(value(&world, &arm), "60");
    assert_eq!(value(&world, &countdown), "20");

    click(&mut world, &arm);
    click(&mut world, "settings.heading");
    assert!(writes(&world).is_empty());
    rest(&mut world);
}

#[test]
fn auto_settle_turns_on_from_its_switch_and_its_windows_save_trimmed() {
    let (daemon, _) = accepting(DAEMON_WINDOWS);
    let mut world = World::start(daemon);
    on_hygiene(&mut world);
    assert!(!checked(&world, "auto_settle_enabled"));
    assert_eq!(pills(&world)[0], "auto-settle off");

    click(&mut world, "settings.switch.auto_settle_enabled");
    assert!(checked(&world, "auto_settle_enabled"));
    assert_eq!(pills(&world)[0], "auto-settle on");

    retype(&mut world, &field("auto_settle_countdown_seconds"), " 25 ");
    press(&mut world, Key::Enter);
    assert_eq!(
        writes(&world),
        [
            ("auto_settle_enabled".to_owned(), "true".to_owned()),
            ("auto_settle_countdown_seconds".to_owned(), "25".to_owned()),
        ]
    );
    rest(&mut world);
}

#[test]
fn quiet_notifications_are_on_by_default_and_turning_them_off_saves_false() {
    let (daemon, _) = accepting(DAEMON_WINDOWS);
    let mut world = World::start(daemon);
    on_hygiene(&mut world);
    let switch = "settings.switch.queue_quiet_notifications";
    assert_eq!(
        named(&world, switch).as_deref(),
        Some("Quiet notifications in the queue")
    );
    assert!(checked(&world, "queue_quiet_notifications"));

    click(&mut world, switch);
    assert!(!checked(&world, "queue_quiet_notifications"));
    assert_eq!(
        writes(&world),
        [("queue_quiet_notifications".to_owned(), "false".to_owned())]
    );
    rest(&mut world);
}

#[test]
fn hidden_sessions_join_the_queue_by_default_from_the_attention_queue_page_and_save_both_ways() {
    let (daemon, _) = accepting(DAEMON_WINDOWS);
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.nav.experimental");
    let switch = "settings.switch.queue_show_hidden_sessions";
    assert!(world.screen().find(Target::key(switch)).is_none());

    click(&mut world, "settings.nav.hygiene");
    assert_eq!(
        named(&world, switch).as_deref(),
        Some("Show hidden sessions in the queue")
    );
    assert!(checked(&world, "queue_show_hidden_sessions"));
    click(&mut world, switch);
    assert!(!checked(&world, "queue_show_hidden_sessions"));
    click(&mut world, switch);
    assert!(checked(&world, "queue_show_hidden_sessions"));
    assert_eq!(
        writes(&world),
        [
            ("queue_show_hidden_sessions".to_owned(), "false".to_owned()),
            ("queue_show_hidden_sessions".to_owned(), "true".to_owned()),
        ]
    );
    rest(&mut world);
}

#[test]
fn searching_crew_finds_the_attention_queue_and_its_crew_switch_saves_both_ways() {
    let (daemon, _) = accepting(DAEMON_WINDOWS);
    let mut world = World::start(daemon);
    open(&mut world);
    click(&mut world, "settings.search.field");
    world.gesture(|app| app.type_text("crew"));
    assert!(nav(&world).contains(&"Attention queue".to_owned()));
    click(&mut world, "settings.nav.hygiene");

    let switch = "settings.switch.queue_crew_enabled";
    assert_eq!(
        named(&world, switch).as_deref(),
        Some("Put the crew in the queue")
    );
    assert!(!checked(&world, "queue_crew_enabled"));
    click(&mut world, switch);
    assert!(checked(&world, "queue_crew_enabled"));
    click(&mut world, switch);
    assert!(!checked(&world, "queue_crew_enabled"));
    assert_eq!(
        writes(&world),
        [
            ("queue_crew_enabled".to_owned(), "true".to_owned()),
            ("queue_crew_enabled".to_owned(), "false".to_owned()),
        ]
    );
    rest(&mut world);
}

/// A daemon with `acme/api` and `dependabot` muted, that takes mute commands without answering.
fn muting() -> Daemon {
    let (daemon, _) = accepting(&[]);
    daemon.answer_hello_with(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        settings: Some(
            DAEMON_WINDOWS
                .iter()
                .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
                .collect(),
        ),
        repos: Some(vec![
            RepoState {
                repo: "acme/api".into(),
                muted: true,
                collapsed: false,
            },
            RepoState {
                repo: "acme/web".into(),
                muted: false,
                collapsed: true,
            },
        ]),
        authors: Some(vec![AuthorState {
            author: "dependabot".into(),
            muted: true,
        }]),
        ..InitialStateMessage::new()
    });
    daemon.on("mute_repo", |_| ());
    daemon.on("mute_author", |_| ());
    daemon
}

fn unmutes_sent(world: &World, cmd: &str, field: &str) -> Vec<String> {
    world
        .daemon
        .sent_of(cmd)
        .iter()
        .map(|r| r[field].as_str().unwrap().to_owned())
        .collect()
}

#[test]
fn muted_repositories_and_authors_list_with_unmute_leave_at_once_and_follow_the_daemon() {
    let daemon = muting();
    let mut world = World::start(daemon);
    on_hygiene(&mut world);

    assert_eq!(listed(&world, "settings.muted.repo"), ["acme/api"]);
    assert_eq!(listed(&world, "settings.muted.author"), ["dependabot"]);
    assert_eq!(&pills(&world)[1..], ["1 repos", "1 authors"]);

    click(&mut world, "settings.unmute.repo.acme/api");
    click(&mut world, "settings.unmute.author.dependabot");
    assert_eq!(
        named(&world, "settings.muted.repo.empty").as_deref(),
        Some("No muted repositories")
    );
    assert_eq!(
        named(&world, "settings.muted.author.empty").as_deref(),
        Some("No muted authors")
    );
    assert_eq!(unmutes_sent(&world, "mute_repo", "repo"), ["acme/api"]);
    assert_eq!(
        unmutes_sent(&world, "mute_author", "author"),
        ["dependabot"]
    );

    world.daemon.emit(ReposUpdatedMessage {
        repos: Some(vec![RepoState {
            repo: "acme/api".into(),
            muted: false,
            collapsed: false,
        }]),
        ..ReposUpdatedMessage::new()
    });
    world.daemon.emit(AuthorsUpdatedMessage {
        authors: Some(vec![]),
        ..AuthorsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(
        named(&world, "settings.muted.repo.empty").as_deref(),
        Some("No muted repositories")
    );
    assert_eq!(
        named(&world, "settings.muted.author.empty").as_deref(),
        Some("No muted authors")
    );
    assert_eq!(&pills(&world)[1..], ["0 repos", "0 authors"]);

    world.daemon.emit(ReposUpdatedMessage {
        repos: Some(vec![RepoState {
            repo: "acme/api".into(),
            muted: true,
            collapsed: false,
        }]),
        ..ReposUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(listed(&world, "settings.muted.repo"), ["acme/api"]);
    assert!(writes(&world).is_empty());
    rest(&mut world);
}

#[test]
fn an_unmute_lost_with_the_connection_shows_muted_again_and_can_be_pressed_again() {
    let mut world = World::start(muting());
    on_hygiene(&mut world);
    click(&mut world, "settings.unmute.repo.acme/api");
    click(&mut world, "settings.unmute.author.dependabot");
    assert!(
        world
            .screen()
            .find(Target::key("settings.muted.repo.empty"))
            .is_some()
    );

    world.reconnect();
    assert_eq!(listed(&world, "settings.muted.repo"), ["acme/api"]);
    assert_eq!(listed(&world, "settings.muted.author"), ["dependabot"]);
    assert_eq!(&pills(&world)[1..], ["1 repos", "1 authors"]);

    click(&mut world, "settings.unmute.repo.acme/api");
    click(&mut world, "settings.unmute.author.dependabot");
    assert_eq!(
        unmutes_sent(&world, "mute_repo", "repo"),
        ["acme/api", "acme/api"]
    );
    assert_eq!(
        unmutes_sent(&world, "mute_author", "author"),
        ["dependabot", "dependabot"]
    );
    assert!(
        world
            .screen()
            .find(Target::key("settings.muted.repo.empty"))
            .is_some()
    );
    assert!(
        world
            .screen()
            .find(Target::key("settings.muted.author.empty"))
            .is_some()
    );
    rest(&mut world);
}

#[test]
fn an_unmute_pressed_while_reconnecting_keeps_the_row_and_sends_once_connected() {
    let mut world = World::start(muting());
    on_hygiene(&mut world);
    world.daemon.refuse_connections(true);
    world.daemon.disconnect();
    world.settle();

    click(&mut world, "settings.unmute.repo.acme/api");
    assert_eq!(listed(&world, "settings.muted.repo"), ["acme/api"]);
    assert!(world.daemon.sent_of("mute_repo").is_empty());

    world.daemon.refuse_connections(false);
    world.advance(Duration::from_secs(1));
    assert_eq!(world.daemon.connections(), 2);
    assert_eq!(listed(&world, "settings.muted.repo"), ["acme/api"]);

    click(&mut world, "settings.unmute.repo.acme/api");
    assert_eq!(unmutes_sent(&world, "mute_repo", "repo"), ["acme/api"]);
    assert!(
        world
            .screen()
            .find(Target::key("settings.muted.repo.empty"))
            .is_some()
    );
    rest(&mut world);
}

#[test]
fn reopening_settings_shows_an_unanswered_unmute_as_muted_again() {
    let mut world = World::start(muting());
    on_hygiene(&mut world);
    click(&mut world, "settings.unmute.repo.acme/api");
    assert!(
        world
            .screen()
            .find(Target::key("settings.muted.repo.empty"))
            .is_some()
    );

    press_close(&mut world);
    open(&mut world);
    assert_eq!(listed(&world, "settings.muted.repo"), ["acme/api"]);
    rest(&mut world);
}
