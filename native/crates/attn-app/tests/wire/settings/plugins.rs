use attn_protocol::{PluginActionResultMessage, PluginInfo, PluginIssue, PluginsUpdatedMessage};
use attn_testkit::{Daemon, Request, Target, World, message};
use rune::{FileDialogKind, Key};
use serde_json::Value;

use super::{accepting, click, named, open, press, rest, retype, value, writes};

const SOURCE: &str = "settings.plugins.source";

fn installed() -> PluginInfo {
    PluginInfo {
        name: "services-pilot-worktrees".into(),
        version: "0.1.0".into(),
        dir: "/tmp/services-pilot-worktrees".into(),
        priority: 10,
        connected: true,
        running: true,
        runtime_state: "connected".into(),
        can_uninstall: true,
        ..PluginInfo::default()
    }
}

fn listing(plugins: Vec<PluginInfo>) -> Value {
    message(PluginsUpdatedMessage {
        plugins,
        issues: Some(vec![]),
        ..PluginsUpdatedMessage::new()
    })
}

fn succeeded(action: &str, r: &Request) -> Value {
    message(PluginActionResultMessage {
        action: action.into(),
        name: r["name"].as_str().map(str::to_owned),
        success: true,
        ..PluginActionResultMessage::new()
    })
}

/// A daemon that lists `plugins` and does every plugin command it is sent.
fn serving(plugins: Vec<PluginInfo>) -> Daemon {
    let (daemon, _) = accepting(&[]);
    daemon.on("list_plugins", move |_| listing(plugins.clone()));
    daemon.on("install_plugin", |r| succeeded("install", r));
    daemon.on("remove_plugin", |r| succeeded("remove", r));
    daemon.on("set_plugin_priority", |r| succeeded("set_priority", r));
    daemon
}

fn on_plugins(world: &mut World) {
    open(world);
    click(world, "settings.nav.plugins");
}

fn lists(world: &World) -> usize {
    world.daemon.sent_of("list_plugins").len()
}

fn pills(world: &World) -> Vec<String> {
    world
        .screen()
        .children(Target::key("settings.pills"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn plugin_part(name: &str, part: &str) -> String {
    format!("settings.plugin.{name}.{part}")
}

#[test]
fn opening_the_dialog_reads_the_plugins_once_and_writes_nothing() {
    let mut world = World::start(serving(vec![]));
    open(&mut world);
    assert_eq!(lists(&world), 1);
    click(&mut world, "settings.nav.plugins");
    assert_eq!(lists(&world), 1);
    assert_eq!(
        named(&world, "settings.plugins.empty").as_deref(),
        Some("No plugins installed.")
    );
    assert!(writes(&world).is_empty());
    rest(&mut world);
}

#[test]
fn installs_a_plugin_from_the_typed_source_and_reads_the_list_again() {
    let mut world = World::start(serving(vec![]));
    on_plugins(&mut world);
    let before = lists(&world);

    click(&mut world, SOURCE);
    world.gesture(|app| app.type_text("git@ghe.example.com:victora/attn-snipe.git"));
    click(&mut world, "settings.plugins.install");

    let sources: Vec<_> = world
        .daemon
        .sent_of("install_plugin")
        .iter()
        .map(|r| r["source"].as_str().unwrap().to_owned())
        .collect();
    assert_eq!(sources, ["git@ghe.example.com:victora/attn-snipe.git"]);
    assert!(lists(&world) > before);
    assert_eq!(value(&world, SOURCE), "");
    rest(&mut world);
}

#[test]
fn an_empty_source_is_refused_before_sending_and_a_refused_install_shows_the_daemon_reason() {
    let daemon = serving(vec![]);
    daemon.on("install_plugin", |_| {
        message(PluginActionResultMessage {
            action: "install".into(),
            success: false,
            error: Some("clone failed: repository not found".into()),
            ..PluginActionResultMessage::new()
        })
    });
    let mut world = World::start(daemon);
    on_plugins(&mut world);

    click(&mut world, "settings.plugins.install");
    assert!(world.daemon.sent_of("install_plugin").is_empty());
    assert_eq!(
        named(&world, "settings.plugins.error").as_deref(),
        Some("Plugin source is required")
    );

    click(&mut world, SOURCE);
    world.gesture(|app| app.type_text("git@host:team/missing.git"));
    click(&mut world, "settings.plugins.install");
    assert_eq!(
        named(&world, "settings.plugins.error").as_deref(),
        Some("clone failed: repository not found")
    );
    assert_eq!(value(&world, SOURCE), "git@host:team/missing.git");
    assert!(
        !world
            .screen()
            .get(Target::key("settings.plugins.install"))
            .is("disabled")
    );
    rest(&mut world);
}

#[test]
fn browse_puts_the_chosen_folder_in_the_source_without_installing() {
    let mut world = World::start(serving(vec![]));
    on_plugins(&mut world);

    click(&mut world, "settings.plugins.browse");
    assert_eq!(
        world.file_dialog(),
        Some((FileDialogKind::Folder, "Select Plugin Directory".to_owned()))
    );
    world.answer_file_dialog(Some("/Users/me/src/my-attn-plugin"));

    assert_eq!(value(&world, SOURCE), "/Users/me/src/my-attn-plugin");
    assert!(world.daemon.sent_of("install_plugin").is_empty());
    rest(&mut world);
}

#[test]
fn a_linked_plugin_shows_its_checkout() {
    let linked = PluginInfo {
        name: "attn-plug".into(),
        version: "0.2.0".into(),
        dir: "/Users/me/.attn/plugins/attn-plug".into(),
        link_target: Some("/Users/me/src/attn/plugins/attn-plug".into()),
        priority: 0,
        ..installed()
    };
    let mut world = World::start(serving(vec![linked]));
    on_plugins(&mut world);

    assert_eq!(
        named(&world, &plugin_part("attn-plug", "linked")).as_deref(),
        Some("Linked")
    );
    assert_eq!(
        named(&world, &plugin_part("attn-plug", "linked-to")).as_deref(),
        Some("Linked to /Users/me/src/attn/plugins/attn-plug")
    );
    assert_eq!(
        named(&world, &plugin_part("attn-plug", "version")).as_deref(),
        Some("v0.2.0")
    );
    rest(&mut world);
}

#[test]
fn removes_a_plugin_and_reads_the_list_again() {
    let mut world = World::start(serving(vec![installed()]));
    on_plugins(&mut world);
    let before = lists(&world);

    click(
        &mut world,
        &plugin_part("services-pilot-worktrees", "remove"),
    );

    let removed: Vec<_> = world
        .daemon
        .sent_of("remove_plugin")
        .iter()
        .map(|r| r["name"].as_str().unwrap().to_owned())
        .collect();
    assert_eq!(removed, ["services-pilot-worktrees"]);
    assert!(lists(&world) > before);
    rest(&mut world);
}

#[test]
fn a_plugin_that_cannot_be_uninstalled_offers_no_remove() {
    let busy = PluginInfo {
        can_uninstall: false,
        ..installed()
    };
    let mut world = World::start(serving(vec![busy]));
    on_plugins(&mut world);

    assert!(
        world
            .screen()
            .get(Target::key(plugin_part(
                "services-pilot-worktrees",
                "remove"
            )))
            .is("disabled")
    );
    rest(&mut world);
}

#[test]
fn a_priority_saves_when_focus_leaves_its_field_and_shows_saved() {
    let mut world = World::start(serving(vec![installed()]));
    on_plugins(&mut world);
    let priority = plugin_part("services-pilot-worktrees", "priority");
    assert_eq!(
        named(&world, &priority).as_deref(),
        Some("services-pilot-worktrees priority")
    );
    assert_eq!(value(&world, &priority), "10");

    retype(&mut world, &priority, "25");
    click(&mut world, "settings.heading");

    let sent: Vec<_> = world
        .daemon
        .sent_of("set_plugin_priority")
        .iter()
        .map(|r| {
            (
                r["name"].as_str().unwrap().to_owned(),
                r["priority"].clone(),
            )
        })
        .collect();
    assert_eq!(
        sent,
        [("services-pilot-worktrees".to_owned(), Value::from(25))]
    );
    assert!(
        world
            .screen()
            .find(Target::key(plugin_part(
                "services-pilot-worktrees",
                "saved"
            )))
            .is_some()
    );
    rest(&mut world);
    assert!(
        world
            .screen()
            .find(Target::key(plugin_part(
                "services-pilot-worktrees",
                "saved"
            )))
            .is_none()
    );
}

#[test]
fn a_priority_that_is_not_a_whole_number_is_refused_before_sending() {
    let mut world = World::start(serving(vec![installed()]));
    on_plugins(&mut world);
    let priority = plugin_part("services-pilot-worktrees", "priority");

    retype(&mut world, &priority, "2.5");
    press(&mut world, Key::Enter);

    assert!(world.daemon.sent_of("set_plugin_priority").is_empty());
    assert_eq!(
        named(&world, "settings.plugins.error").as_deref(),
        Some("Plugin priority must be an integer")
    );
    rest(&mut world);
}

#[test]
fn a_plugin_follows_the_status_the_daemon_pushes() {
    let starting = PluginInfo {
        priority: 0,
        connected: false,
        runtime_phase: Some("starting".into()),
        runtime_state: "starting".into(),
        health_status: Some("unknown".into()),
        ..installed()
    };
    let mut world = World::start(serving(vec![starting.clone()]));
    on_plugins(&mut world);
    let runtime = plugin_part("services-pilot-worktrees", "runtime");
    assert_eq!(named(&world, &runtime).as_deref(), Some("starting"));

    world.daemon.emit(PluginsUpdatedMessage {
        plugins: vec![PluginInfo {
            running: false,
            runtime_phase: Some("backoff".into()),
            runtime_state: "degraded".into(),
            restart_attempt: Some(2),
            next_restart_at: Some("2026-07-15T22:20:00Z".into()),
            last_exit: Some("2026-07-15T22:19:59Z: exit code 1".into()),
            ..starting.clone()
        }],
        ..PluginsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(named(&world, &runtime).as_deref(), Some("degraded"));
    assert_eq!(
        named(
            &world,
            &plugin_part("services-pilot-worktrees", "restart-attempt")
        )
        .as_deref(),
        Some("Restart attempt 2")
    );
    assert_eq!(
        named(
            &world,
            &plugin_part("services-pilot-worktrees", "last-exit")
        )
        .as_deref(),
        Some("Last exit: 2026-07-15T22:19:59Z: exit code 1")
    );
    assert_eq!(pills(&world), ["healthy", "0/1 running"]);

    world.daemon.emit(PluginsUpdatedMessage {
        plugins: vec![PluginInfo {
            connected: true,
            runtime_phase: Some("connected".into()),
            runtime_state: "connected".into(),
            health_status: Some("healthy".into()),
            ..starting
        }],
        ..PluginsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(named(&world, &runtime).as_deref(), Some("connected"));
    assert_eq!(
        named(&world, &plugin_part("services-pilot-worktrees", "health")).as_deref(),
        Some("healthy")
    );
    assert_eq!(pills(&world), ["healthy", "1/1 running"]);
    rest(&mut world);
}

#[test]
fn the_pills_count_problems_from_unhealthy_plugins_and_folders_that_failed_to_load() {
    let (daemon, _) = accepting(&[]);
    daemon.on("list_plugins", |_| {
        message(PluginsUpdatedMessage {
            plugins: vec![
                PluginInfo {
                    health_status: Some("unhealthy".into()),
                    health_message: Some("provider timed out".into()),
                    ..installed()
                },
                PluginInfo {
                    name: "idle".into(),
                    connected: false,
                    running: false,
                    runtime_state: "stopped".into(),
                    ..installed()
                },
            ],
            issues: Some(vec![PluginIssue {
                path: "/plugins/broken".into(),
                error: "plugin.json: missing name".into(),
            }]),
            ..PluginsUpdatedMessage::new()
        })
    });
    let mut world = World::start(daemon);
    on_plugins(&mut world);

    assert_eq!(pills(&world), ["2 issues", "1/2 running"]);
    assert_eq!(
        named(&world, "settings.plugins.issue./plugins/broken").as_deref(),
        Some("/plugins/broken: plugin.json: missing name")
    );
    assert_eq!(
        named(
            &world,
            &plugin_part("services-pilot-worktrees", "healthcheck")
        )
        .as_deref(),
        Some("Healthcheck: provider timed out")
    );
    rest(&mut world);
}

#[test]
fn a_list_the_daemon_never_answers_shows_loading_and_the_link_dropping_says_why() {
    let (daemon, _) = accepting(&[]);
    let mut world = World::start(daemon);
    on_plugins(&mut world);
    assert_eq!(
        named(&world, "settings.plugins.empty").as_deref(),
        Some("Loading plugins...")
    );

    world.daemon.disconnect();
    world.settle();
    assert_eq!(
        named(&world, "settings.plugins.error").as_deref(),
        Some("the daemon did not answer")
    );
}
