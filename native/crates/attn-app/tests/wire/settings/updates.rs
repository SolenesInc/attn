use std::collections::BTreeMap;

use attn_protocol::{
    InitialStateMessage, StartUpdateResultMessage, UpdateChangedMessage, UpdateOutpostProgress,
    UpdateRun,
};
use attn_testkit::{Daemon, Target, World, message};

use super::{accepting, click, named, open};

const RELEASE_APP: &[(&str, &str)] = &[
    ("self_update_enabled", "true"),
    ("update_install_kind", "release_app"),
    ("update_current_version", "0.13.0"),
    ("update_current_commit", "abcdef1234567"),
    ("update_available_version", "0.14.0"),
];

fn run(phase: &str) -> UpdateRun {
    UpdateRun {
        id: "run-1".into(),
        install_kind: "release_app".into(),
        log_path: "/tmp/run-1.log".into(),
        phase: phase.into(),
        ..UpdateRun::default()
    }
}

fn on_updates(world: &mut World) {
    open(world);
    click(world, "settings.nav.updates");
}

fn fact(world: &World, key: &str) -> String {
    named(world, &format!("settings.updates.{key}")).unwrap_or_default()
}

/// The Update attn button's name, and whether it is disabled.
fn action(world: &World) -> Option<(String, bool)> {
    world
        .screen()
        .find(Target::key("settings.updates.start"))
        .map(|n| (n.name.clone().unwrap_or_default(), n.is("disabled")))
}

#[test]
fn shows_the_daemon_reported_install_build_and_available_release() {
    let (daemon, _) = accepting(RELEASE_APP);
    let mut world = World::start(daemon);
    on_updates(&mut world);
    assert_eq!(fact(&world, "install"), "Installed as Release app");
    assert_eq!(fact(&world, "build"), "Build 0.13.0 (abcdef123)");
    assert_eq!(fact(&world, "available"), "Available 0.14.0");
}

#[test]
fn shows_why_availability_is_unknown_after_a_failed_check() {
    let (daemon, _) = accepting(&[
        ("self_update_enabled", "true"),
        ("update_install_kind", "release_binary"),
        ("update_current_version", "0.13.0"),
        ("update_current_commit", "abcdef1"),
        ("update_check_error", "network is down"),
    ]);
    let mut world = World::start(daemon);
    on_updates(&mut world);
    assert_eq!(
        fact(&world, "available"),
        "Available Unknown. The last check failed: network is down"
    );
}

#[test]
fn update_attn_starts_one_run_and_offers_again_only_once_the_run_ends() {
    let (daemon, _) = accepting(RELEASE_APP);
    daemon.on("start_update", |_| ());
    let mut world = World::start(daemon);
    on_updates(&mut world);
    assert_eq!(action(&world), Some(("Update attn".into(), false)));

    click(&mut world, "settings.updates.start");
    assert_eq!(world.daemon.sent_of("start_update").len(), 1);

    world.daemon.emit(UpdateChangedMessage {
        run: run("staging"),
        ..UpdateChangedMessage::new()
    });
    world.settle();
    assert_eq!(action(&world), Some(("Updating…".into(), true)));

    world.daemon.emit(UpdateChangedMessage {
        run: run("done"),
        ..UpdateChangedMessage::new()
    });
    world.settle();
    assert_eq!(action(&world), Some(("Update attn".into(), false)));
}

#[test]
fn a_run_already_open_when_the_app_connects_shows_as_updating() {
    let settings: BTreeMap<String, String> = RELEASE_APP
        .iter()
        .map(|(k, v)| ((*k).into(), (*v).into()))
        .collect();
    let daemon = Daemon::scripted().state(InitialStateMessage {
        settings: Some(settings),
        update_run: Some(run("applying")),
        ..InitialStateMessage::new()
    });
    let mut world = World::start(daemon);
    on_updates(&mut world);
    assert_eq!(action(&world), Some(("Updating…".into(), true)));
}

#[test]
fn a_refused_start_says_why_until_the_next_press() {
    let (daemon, _) = accepting(RELEASE_APP);
    daemon.on("start_update", |_| {
        message(StartUpdateResultMessage {
            success: false,
            error: Some("the build failed: see /tmp/run-1.log".into()),
            ..StartUpdateResultMessage::new()
        })
    });
    let mut world = World::start(daemon);
    on_updates(&mut world);
    click(&mut world, "settings.updates.start");
    assert_eq!(
        named(&world, "settings.updates.error").as_deref(),
        Some("the build failed: see /tmp/run-1.log")
    );

    world.daemon.on("start_update", |_| ());
    click(&mut world, "settings.updates.start");
    assert_eq!(named(&world, "settings.updates.error"), None);
}

#[test]
fn an_install_attn_cannot_update_offers_no_update() {
    let (daemon, _) = accepting(&[
        ("self_update_enabled", "true"),
        ("update_install_kind", "outpost"),
        ("update_current_version", "0.13.0"),
        ("update_current_commit", "abcdef1"),
    ]);
    let mut world = World::start(daemon);
    on_updates(&mut world);
    assert_eq!(fact(&world, "install"), "Installed as Outpost");
    assert_eq!(action(&world), None);
}

#[test]
fn an_app_machine_lists_its_home_and_itself_with_why_and_the_one_action() {
    let row = |role: &str,
               build: &str,
               reason: Option<&str>,
               detail: Option<&str>,
               action: Option<&str>| UpdateOutpostProgress {
        id: role.into(),
        name: "oma".into(),
        role: Some(role.into()),
        state: if reason.is_some() { "failed" } else { "done" }.into(),
        build: Some(format!("{build} (source git:{build}, protocol p)")),
        reason: reason.map(Into::into),
        detail: detail.map(Into::into),
        action: action.map(Into::into),
    };
    let (daemon, _) = accepting(&[
        ("self_update_enabled", "true"),
        ("update_install_kind", "outpost"),
        ("update_current_version", "0.16.1"),
    ]);
    daemon.on("start_update", |_| ());
    let mut world = World::start(daemon);
    on_updates(&mut world);
    world.daemon.emit(UpdateChangedMessage {
        run: UpdateRun {
            install_kind: "home".into(),
            outposts: vec![
                row(
                    "home",
                    "0.16.0",
                    Some("unreachable"),
                    Some("ssh: Connection timed out"),
                    Some("Update"),
                ),
                row("app_machine", "0.16.1", None, None, None),
                UpdateOutpostProgress {
                    id: "box".into(),
                    name: "box".into(),
                    role: Some("outpost".into()),
                    state: "skipped".into(),
                    detail: Some("Left out of this update.".into()),
                    action: Some("Update outpost".into()),
                    ..UpdateOutpostProgress::default()
                },
            ],
            ..run("failed")
        },
        ..UpdateChangedMessage::new()
    });
    world.settle();
    assert_eq!(
        fact(&world, "machine.home"),
        "oma · home unreachable: ssh: Connection timed out"
    );
    assert_eq!(fact(&world, "machine.app_machine"), "This Mac 0.16.1");
    assert_eq!(fact(&world, "machine.box"), "box Left out of this update.");
    assert_eq!(action(&world), None);
    let button = world
        .screen()
        .find(Target::key("settings.updates.machine-action"))
        .expect("the row's action");
    assert!(!button.is("disabled"), "{button:?}");
    click(&mut world, "settings.updates.machine-action");
    assert_eq!(world.daemon.sent_of("start_update").len(), 1);
}
