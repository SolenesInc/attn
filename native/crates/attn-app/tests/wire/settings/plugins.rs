use attn_protocol::{PluginActionResultMessage, PluginInfo, PluginIssue, PluginsUpdatedMessage};
use attn_testkit::{Answer, Daemon, Request, Target, World, message};
use rune::{FileDialogKind, Key};
use serde_json::Value;

use attn_app::Action;

use super::{accepting, click, named, open, press_action, rest, retype, value, writes};

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
    daemon.on("reload_plugin", |r| succeeded("reload", r));
    daemon.on("update_plugin", |r| succeeded("update", r));
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
fn a_refused_priority_put_back_to_its_saved_value_is_no_longer_refused() {
    let mut world = World::start(serving(vec![installed()]));
    on_plugins(&mut world);
    let priority = plugin_part("services-pilot-worktrees", "priority");
    let saved = value(&world, &priority);
    retype(&mut world, &priority, "2.5");
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: Key::Enter,
            mods: rune::Mods::default(),
        })
    });
    assert!(named(&world, &format!("{priority}.error")).is_some());

    retype(&mut world, &priority, &saved);
    let rec = world.record(60.0, 0.0, |_, app| {
        app.key(rune::KeyEvent {
            key: Key::Enter,
            mods: rune::Mods::default(),
        })
    });

    let error = format!("/{priority}.error");
    assert!(
        rec.tracks
            .iter()
            .all(|t| !t.path.ends_with(&error) || t.frames.len() <= 1),
        "the error leaves on the frame the saved value is put back"
    );
    assert!(named(&world, &format!("{priority}.error")).is_none());
    let field = world.screen().get(Target::key(&priority));
    assert!(
        !field.states.iter().any(|s| s == "invalid"),
        "the field no longer reads invalid: {:?}",
        field.states
    );
    assert!(world.daemon.sent_of("set_plugin_priority").is_empty());
    rest(&mut world);
}

/// A page of five plugins, at the smallest window and the largest text, so most of it is
/// scrolled out of view.
fn five_plugins() -> World {
    let (daemon, _) = accepting(&[("uiScale", "1.5")]);
    daemon.on("list_plugins", |_| {
        listing(
            [
                "provider-a",
                "provider-b",
                "provider-c",
                "provider-d",
                "provider-e",
            ]
            .into_iter()
            .map(|name| PluginInfo {
                name: name.into(),
                last_exit: Some("exit status 1: could not reach the provider".into()),
                ..installed()
            })
            .collect(),
        )
    });
    let mut world = World::start_sized(daemon, (800.0, 600.0));
    on_plugins(&mut world);
    world
}

fn element(world: &World, key: &str) -> rune::ElementInfo {
    world
        .runtime()
        .elements()
        .into_iter()
        .find(|e| e.key.as_deref() == Some(key))
        .unwrap_or_else(|| panic!("no {key}"))
}

fn whole(e: &rune::ElementInfo) -> bool {
    (e.visible[3] - e.rect[3]).abs() < 0.5
}

/// Wheels the page until `key` sits at the middle of its view.
fn wheel_to_middle(world: &mut World, key: &str) {
    let body = element(world, "settings.body").rect;
    let at = element(world, key).rect;
    let middle = body[1] + body[3] / 2.0;
    world.gesture(|app| {
        app.runtime.wheel(
            body[0] + body[2] / 2.0,
            middle,
            middle - (at[1] + at[3] / 2.0),
        )
    });
    world.settle();
    assert!(whole(&element(world, key)), "{key} is in view");
}

fn click_centre(world: &mut World, key: &str) {
    let [x, y, w, h] = element(world, key).rect;
    world.gesture(|app| app.click_at((x + w / 2.0, y + h / 2.0)));
}

/// Wheels the page until `key` sits just inside the bottom of its view.
fn wheel_to_bottom(world: &mut World, key: &str) {
    let body = element(world, "settings.body").visible;
    let at = element(world, key).rect;
    let bottom = body[1] + body[3] - 2.0;
    world.gesture(|app| {
        app.runtime.wheel(
            body[0] + body[2] / 2.0,
            body[1] + body[3] / 2.0,
            bottom - (at[1] + at[3]),
        )
    });
    world.settle();
    assert!(whole(&element(world, key)), "{key} is in view");
}

#[test]
fn leaving_a_refused_priority_by_a_click_keeps_the_clicked_field_where_it_was_clicked() {
    for (placed, place) in [
        ("in the middle", wheel_to_middle as fn(&mut World, &str)),
        ("at the bottom edge", wheel_to_bottom),
    ] {
        let mut world = five_plugins();
        let left = plugin_part("provider-a", "priority");
        let clicked = plugin_part("provider-d", "priority");
        retype(&mut world, &left, "2.5");
        place(&mut world, &clicked);
        let before = element(&world, &clicked);

        click_centre(&mut world, &clicked);

        let after = element(&world, &clicked);
        assert_eq!(
            world.screen().focused().and_then(|n| n.key).as_deref(),
            Some(clicked.as_str()),
            "{placed}"
        );
        assert_eq!(
            after.rect[1], before.rect[1],
            "{placed}: the clicked field stays where it was clicked"
        );
        assert!(
            whole(&after),
            "{placed}: the clicked field shows {} of its {} points",
            after.visible[3],
            after.rect[3]
        );
        assert_eq!(
            named(&world, &format!("{left}.error")).as_deref(),
            Some("Plugin priority must be an integer"),
            "{placed}: the refusal stands under the field that was left"
        );
        let left_field = world.screen().get(Target::key(&left));
        assert!(left_field.states.iter().any(|s| s == "invalid"));
        assert!(world.daemon.sent_of("set_plugin_priority").is_empty());
        rest(&mut world);
    }
}

#[test]
fn a_priority_refused_after_the_person_went_to_search_leaves_the_page_where_it_is() {
    let mut world = five_plugins();
    world
        .daemon
        .answer_in_turn("set_plugin_priority", [Answer::Hold]);
    let priority = plugin_part("provider-d", "priority");
    retype(&mut world, &priority, "1000001");
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: Key::Enter,
            mods: rune::Mods::default(),
        })
    });
    let request = world.daemon.sent_of("set_plugin_priority").remove(0);
    let body = element(&world, "settings.body").rect;
    world.gesture(|app| {
        app.runtime
            .wheel(body[0] + body[2] / 2.0, body[1] + body[3] / 2.0, 4000.0)
    });
    world.settle();
    click_centre(&mut world, "settings.search.field");
    let before = element(&world, &priority).scrolled;

    world.daemon.reply_to(
        &request,
        PluginActionResultMessage {
            action: "set_priority".into(),
            name: Some("provider-d".into()),
            error: Some("plugin priority must be between -1000000 and 1000000".into()),
            ..PluginActionResultMessage::new()
        },
    );
    world.settle();

    assert_eq!(
        element(&world, &priority).scrolled,
        before,
        "the page stays where the person is reading"
    );
    assert_eq!(
        named(&world, &format!("{priority}.error")).as_deref(),
        Some("plugin priority must be between -1000000 and 1000000"),
        "the refusal stands under its field"
    );
    rest(&mut world);
}

#[test]
fn a_refused_removal_shows_its_reason_beside_the_remove_the_person_pressed() {
    let mut world = five_plugins();
    world.daemon.answer_in_turn("remove_plugin", [Answer::Hold]);
    click_centre(&mut world, "settings.search.field");
    let remove = plugin_part("provider-d", "remove");
    wheel_to_bottom(&mut world, &remove);
    click_centre(&mut world, &remove);
    let request = world.daemon.sent_of("remove_plugin").remove(0);

    let rec = world.record(60.0, 0.5, |daemon, _| {
        daemon.reply_to(
            &request,
            PluginActionResultMessage {
                action: "remove".into(),
                name: Some("provider-d".into()),
                error: Some(r#"plugin "provider-d" owns 1 open server session(s)"#.into()),
                ..PluginActionResultMessage::new()
            },
        )
    });

    crate::wire::whole_on_its_first_frame(&rec, &plugin_part("provider-d", "error"));
    let pressed = element(&world, &remove);
    assert!(
        whole(&pressed),
        "Remove shows {} of its {} points",
        pressed.visible[3],
        pressed.rect[3]
    );
    rest(&mut world);
}

#[test]
fn an_install_pressed_while_typing_in_the_source_shows_its_refusal_and_the_button_whole() {
    let mut world = five_plugins();
    click(&mut world, SOURCE);
    let rec = world.record(60.0, 0.5, |_, app| {
        app.click(Target::key("settings.plugins.install"))
    });
    crate::wire::whole_on_its_first_frame(&rec, "settings.plugins.error");
    assert!(whole(&element(&world, "settings.plugins.install")));
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

    let rec = world.record(60.0, 0.5, |_, app| {
        app.click(Target::key("settings.plugins.install"))
    });
    crate::wire::whole_on_its_first_frame(&rec, "settings.plugins.error");
    assert!(world.daemon.sent_of("install_plugin").is_empty());
    assert_eq!(
        named(&world, "settings.plugins.error").as_deref(),
        Some("Plugin source is required")
    );

    click(&mut world, SOURCE);
    world.gesture(|app| app.type_text("git@host:team/missing.git"));
    let rec = world.record(60.0, 0.5, |_, app| {
        app.click(Target::key("settings.plugins.install"))
    });
    crate::wire::whole_on_its_first_frame(&rec, "settings.plugins.error");
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
fn a_priority_saves_when_the_settings_shortcut_closes_the_dialog_with_the_focus_in_it() {
    let mut world = World::start(serving(vec![installed()]));
    on_plugins(&mut world);
    let priority = plugin_part("services-pilot-worktrees", "priority");

    retype(&mut world, &priority, "25");
    press_action(&mut world, Action::UiOpenSettings);

    assert!(world.screen().find(Target::key(&priority)).is_none());
    let sent: Vec<_> = world
        .daemon
        .sent_of("set_plugin_priority")
        .iter()
        .map(|r| r["priority"].clone())
        .collect();
    assert_eq!(sent, [Value::from(25)]);
    rest(&mut world);
}

#[test]
fn a_priority_that_is_not_a_whole_number_is_refused_before_sending() {
    let mut world = World::start(serving(vec![installed()]));
    on_plugins(&mut world);
    let priority = plugin_part("services-pilot-worktrees", "priority");

    retype(&mut world, &priority, "2.5");
    let rec = world.record(60.0, 0.5, |_, app| {
        app.key(rune::KeyEvent {
            key: Key::Enter,
            mods: rune::Mods::default(),
        })
    });
    crate::wire::whole_on_its_first_frame(&rec, &format!("{priority}.error"));

    assert!(world.daemon.sent_of("set_plugin_priority").is_empty());
    assert_eq!(
        named(&world, &format!("{priority}.error")).as_deref(),
        Some("Plugin priority must be an integer")
    );
    rest(&mut world);
}

#[test]
fn a_priority_the_daemon_refuses_shows_why_under_the_focused_field_in_the_smallest_window() {
    let (daemon, _) = accepting(&[("uiScale", "1.5")]);
    daemon.on("list_plugins", |_| {
        message(PluginsUpdatedMessage {
            plugins: vec![PluginInfo {
                last_exit: Some("exit status 1: could not reach the provider's API at https://pilot.example.com/v2/worktrees after 3 attempts".into()),
                ..installed()
            }],
            issues: Some(vec![PluginIssue {
                path: "/Users/someone/src/attn-plugins/broken-plugin/attn-plugin.json".into(),
                error: "manifest names a harness that no binary on PATH provides".into(),
            }]),
            ..PluginsUpdatedMessage::new()
        })
    });
    daemon.on("set_plugin_priority", |r| {
        message(PluginActionResultMessage {
            action: "set_priority".into(),
            name: r["name"].as_str().map(str::to_owned),
            success: false,
            error: Some("plugin priority must be between -1000000 and 1000000".into()),
            ..PluginActionResultMessage::new()
        })
    });
    let mut world = World::start_sized(daemon, (800.0, 600.0));
    on_plugins(&mut world);
    let priority = plugin_part("services-pilot-worktrees", "priority");

    retype(&mut world, &priority, "1000001");
    let rec = world.record(60.0, 0.5, |_, app| {
        app.key(rune::KeyEvent {
            key: Key::Enter,
            mods: rune::Mods::default(),
        })
    });
    crate::wire::whole_on_its_first_frame(&rec, &format!("{priority}.error"));

    assert_eq!(world.daemon.sent_of("set_plugin_priority").len(), 1);
    assert_eq!(
        named(&world, &format!("{priority}.error")).as_deref(),
        Some("plugin priority must be between -1000000 and 1000000")
    );
    for key in [format!("{priority}.field"), format!("{priority}.error")] {
        let e = world
            .runtime()
            .elements()
            .into_iter()
            .find(|e| e.key.as_deref() == Some(key.as_str()))
            .unwrap_or_else(|| panic!("no {key}"));
        assert!(
            (e.visible[3] - e.rect[3]).abs() < 0.5,
            "{key} shows {} of its {} points",
            e.visible[3],
            e.rect[3]
        );
    }
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

#[test]
fn tab_shows_every_plugin_whole_in_the_smallest_window() {
    let plugins = (0..12)
        .map(|i| PluginInfo {
            name: format!("plugin-{i:02}"),
            dir: format!("/tmp/plugin-{i:02}"),
            ..installed()
        })
        .collect();
    let mut world = World::start(serving(plugins));
    world.resize((533.0, 400.0));
    on_plugins(&mut world);
    let reached = super::super::reach::tab_round_shows_whole(&mut world, "settings.")
        .unwrap_or_else(|why| panic!("{why}"));
    assert!(
        reached.iter().any(|k| k.contains("plugin-11")),
        "Tab never reached the last plugin: {reached:?}"
    );
}

fn linked() -> PluginInfo {
    PluginInfo {
        name: "attn-snipe".into(),
        dir: "/Users/me/.attn/plugins/attn-snipe".into(),
        link_target: Some("/Users/me/src/attn-snipe".into()),
        ..installed()
    }
}

fn managed_from_git() -> PluginInfo {
    PluginInfo {
        source: Some("https://ghe.example.com/victora/attn-snipe.git".into()),
        source_kind: Some("git".into()),
        ..installed()
    }
}

fn sent_names(world: &World, cmd: &str) -> Vec<String> {
    world
        .daemon
        .sent_of(cmd)
        .iter()
        .map(|r| r["name"].as_str().unwrap().to_owned())
        .collect()
}

#[test]
fn reload_on_a_linked_plugin_reloads_it_and_reads_the_list_again() {
    let mut world = World::start(serving(vec![linked()]));
    on_plugins(&mut world);
    let before = lists(&world);
    assert!(
        world
            .screen()
            .find(Target::key(plugin_part("attn-snipe", "update")))
            .is_none()
    );

    click(&mut world, &plugin_part("attn-snipe", "reload"));

    assert_eq!(sent_names(&world, "reload_plugin"), ["attn-snipe"]);
    assert!(world.daemon.sent_of("reload_plugin")[0]["skip_install"].is_null());
    assert!(lists(&world) > before);
    rest(&mut world);
}

#[test]
fn update_on_a_plugin_installed_from_a_source_updates_it_and_shows_the_source() {
    let mut world = World::start(serving(vec![managed_from_git()]));
    on_plugins(&mut world);
    let name = "services-pilot-worktrees";
    let before = lists(&world);
    assert_eq!(
        named(&world, &plugin_part(name, "source")).as_deref(),
        Some("Source https://ghe.example.com/victora/attn-snipe.git")
    );
    assert!(
        world
            .screen()
            .find(Target::key(plugin_part(name, "reload")))
            .is_none()
    );

    click(&mut world, &plugin_part(name, "update"));

    assert_eq!(sent_names(&world, "update_plugin"), [name]);
    assert!(lists(&world) > before);
    rest(&mut world);
}

#[test]
fn a_plugin_installed_before_sources_were_recorded_offers_neither_reload_nor_update() {
    let mut world = World::start(serving(vec![installed()]));
    on_plugins(&mut world);
    let name = "services-pilot-worktrees";
    for part in ["reload", "update", "source"] {
        assert!(
            world
                .screen()
                .find(Target::key(plugin_part(name, part)))
                .is_none(),
            "{part} shows on a plugin with no recorded source"
        );
    }
    assert!(
        world
            .screen()
            .find(Target::key(plugin_part(name, "remove")))
            .is_some()
    );
    rest(&mut world);
}

#[test]
fn a_refused_reload_or_update_shows_its_reason_under_the_plugin_and_frees_the_row() {
    for (plugin, cmd, action, why) in [
        (
            linked(),
            "reload_plugin",
            "reload",
            r#"plugin "attn-snipe" exited before it connected: signal: killed"#,
        ),
        (
            managed_from_git(),
            "update_plugin",
            "update",
            "fetch failed: repository not found",
        ),
    ] {
        let name = plugin.name.clone();
        let daemon = serving(vec![plugin]);
        daemon.on(cmd, move |r| {
            message(PluginActionResultMessage {
                action: action.into(),
                name: r["name"].as_str().map(str::to_owned),
                success: false,
                error: Some(why.into()),
                ..PluginActionResultMessage::new()
            })
        });
        let mut world = World::start(daemon);
        on_plugins(&mut world);
        let pressed = plugin_part(&name, action);

        let rec = world.record(60.0, 0.5, |_, app| app.click(Target::key(&pressed)));

        crate::wire::whole_on_its_first_frame(&rec, &plugin_part(&name, "error"));
        assert_eq!(
            named(&world, &plugin_part(&name, "error")).as_deref(),
            Some(why),
            "{action}"
        );
        assert!(
            named(&world, "settings.plugins.error").is_none(),
            "{action}"
        );
        assert!(
            !world.screen().get(Target::key(&pressed)).is("disabled"),
            "{action}"
        );
        rest(&mut world);
    }
}
