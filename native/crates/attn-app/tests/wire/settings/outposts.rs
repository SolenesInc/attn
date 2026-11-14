use attn_protocol::{
    InitialStateMessage, OutpostInfo, OutpostNotebook, OutpostUpdate, OutpostsUpdatedMessage,
    Profile, ProfileActionResultMessage, Session, SettingsUpdatedMessage, UpdateChangedMessage,
    UpdateOutpostProgress, UpdateRun,
};
use attn_testkit::{Daemon, Target, World, message};
use rune::Key;
use serde_json::{Value, json};

use super::{click, named, open, press, value};
use crate::wire::sidebar::{desktop, pane};

const NAME: &str = "settings.outposts.name";
const DESTINATION: &str = "settings.outposts.destination";
const SUBMIT: &str = "settings.outposts.submit";
const ERROR: &str = "settings.outposts.error";
const ROW: &str = "settings.outpost.outpost-1";
const HOME_BUILD: &str = "0.13.0 (source git:home, protocol current)";

fn studio() -> OutpostInfo {
    OutpostInfo {
        id: "outpost-1".into(),
        name: "studio".into(),
        destination: "studio:2200".into(),
        state: "connected".into(),
        reach: "reachable".into(),
        state_since: "2026-10-18T18:00:00Z".into(),
        daemon_id: Some("studio-daemon".into()),
        host_key_fingerprint: Some("SHA256:studio-key".into()),
        build: Some(HOME_BUILD.into()),
        ..OutpostInfo::default()
    }
}

fn refused() -> OutpostInfo {
    OutpostInfo {
        state: "refused".into(),
        reach: "unreachable".into(),
        ..studio()
    }
}

fn initial(outposts: Vec<OutpostInfo>) -> InitialStateMessage {
    InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        settings: Some([("outposts_enabled".into(), "true".into())].into()),
        outposts: Some(outposts),
        home_build: Some(HOME_BUILD.into()),
        ..InitialStateMessage::new()
    }
}

fn home(outposts: Vec<OutpostInfo>) -> Daemon {
    let daemon = Daemon::scripted().state(initial(outposts));
    for cmd in [
        "add_outpost",
        "repair_outpost",
        "remove_outpost",
        "update_outpost",
    ] {
        daemon.on(cmd, |_| ());
    }
    daemon
}

fn on_outposts(world: &mut World) {
    open(world);
    click(world, "settings.nav.outposts");
}

fn list(world: &mut World, outposts: Vec<OutpostInfo>) {
    world.daemon.emit(OutpostsUpdatedMessage {
        outposts,
        ..OutpostsUpdatedMessage::new()
    });
    world.settle();
}

fn part(name: &str) -> String {
    format!("{ROW}.{name}")
}

fn shows(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

fn type_into(world: &mut World, key: &str, text: &str) {
    click(world, key);
    let typed = value(world, key).chars().count();
    for _ in 0..typed {
        press(world, Key::Backspace);
    }
    world.gesture(|app| app.type_text(text));
}

fn sent(world: &World, cmd: &str) -> Vec<Value> {
    world
        .daemon
        .sent_of(cmd)
        .iter()
        .map(|r| {
            let mut fields = r.as_object().unwrap().clone();
            fields.remove("cmd");
            Value::Object(fields)
        })
        .collect()
}

fn disabled(world: &World, key: &str) -> bool {
    world.screen().get(Target::key(key)).is("disabled")
}

#[test]
fn turning_outposts_on_shows_the_empty_list_and_off_marks_the_page_again() {
    let daemon = Daemon::scripted().state(InitialStateMessage {
        settings: Some(Default::default()),
        ..initial(vec![])
    });
    let mut world = World::start(daemon);
    on_outposts(&mut world);
    assert!(shows(&world, "settings.nav-off.outposts"));

    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([("outposts_enabled".into(), "true".into())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert!(!shows(&world, "settings.nav-off.outposts"));
    assert_eq!(
        named(&world, "settings.outposts.empty").as_deref(),
        Some("No outposts added.")
    );

    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([("outposts_enabled".into(), "false".into())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert!(shows(&world, "settings.nav-off.outposts"));
    assert!(
        !world
            .screen()
            .get(Target::key("settings.feature.outposts_enabled"))
            .is("checked")
    );
}

#[test]
fn adds_by_name_and_ssh_destination_and_keeps_the_draft_until_a_list_holds_it() {
    let mut world = World::start(home(vec![]));
    on_outposts(&mut world);
    assert!(disabled(&world, SUBMIT));
    type_into(&mut world, NAME, " studio ");
    type_into(&mut world, DESTINATION, " studio:2200 ");
    click(&mut world, SUBMIT);
    assert_eq!(
        sent(&world, "add_outpost"),
        [json!({"name": "studio", "destination": "studio:2200"})]
    );
    assert_eq!(value(&world, NAME), " studio ");
    let commands = world.daemon.sent().len();

    list(&mut world, vec![studio()]);
    assert_eq!(value(&world, NAME), "");
    assert_eq!(value(&world, DESTINATION), "");
    assert_eq!(named(&world, ROW).as_deref(), Some("studio"));
    assert_eq!(named(&world, &part("state")).as_deref(), Some("connected"));
    assert_eq!(
        named(&world, &part("build")),
        Some(format!("Build {HOME_BUILD}"))
    );
    assert_eq!(
        named(&world, &part("host-key")).as_deref(),
        Some("Host key SHA256:studio-key")
    );
    assert_eq!(
        named(&world, &part("daemon")).as_deref(),
        Some("Daemon studio-daemon")
    );
    assert!(
        named(&world, &part("since"))
            .unwrap()
            .starts_with("Since 10/")
    );
    assert_eq!(world.daemon.sent().len(), commands);
}

#[test]
fn return_in_the_destination_adds_with_the_destination_as_its_name() {
    let mut world = World::start(home(vec![]));
    on_outposts(&mut world);
    type_into(&mut world, DESTINATION, " user@host ");
    press(&mut world, Key::Enter);
    assert_eq!(
        sent(&world, "add_outpost"),
        [json!({"name": "user@host", "destination": "user@host"})]
    );
}

#[test]
fn a_refused_add_keeps_its_draft_and_its_reason_until_the_next_command() {
    let daemon = home(vec![]);
    daemon.on("add_outpost", |_| {
        json!({"event": "command_error", "cmd": "add_outpost", "success": false, "error": "Address studio:2200 is already paired"})
    });
    let mut world = World::start(daemon);
    on_outposts(&mut world);
    list(&mut world, vec![studio()]);
    type_into(&mut world, NAME, "studio");
    type_into(&mut world, DESTINATION, "studio:2200");
    click(&mut world, SUBMIT);
    assert_eq!(value(&world, NAME), "studio");
    let error = world.screen().get(Target::key(ERROR));
    assert_eq!(error.role, "alert");
    assert_eq!(
        error.name.as_deref(),
        Some("Address studio:2200 is already paired")
    );
    list(&mut world, vec![studio()]);
    assert_eq!(
        named(&world, ERROR).as_deref(),
        Some("Address studio:2200 is already paired")
    );
    assert_eq!(value(&world, DESTINATION), "studio:2200");

    world.daemon.on("add_outpost", |_| ());
    type_into(&mut world, NAME, "laptop");
    type_into(&mut world, DESTINATION, "laptop:2200");
    click(&mut world, SUBMIT);
    type_into(&mut world, NAME, "next machine");
    type_into(&mut world, DESTINATION, "next:2200");
    let laptop = OutpostInfo {
        id: "laptop".into(),
        name: "laptop".into(),
        destination: "laptop:2200".into(),
        ..studio()
    };
    list(&mut world, vec![studio(), laptop]);
    assert_eq!(value(&world, NAME), "next machine");
    assert_eq!(value(&world, DESTINATION), "next:2200");
    assert!(!shows(&world, ERROR));
}

#[test]
fn re_pair_and_remove_name_the_row_and_the_row_stays_until_the_daemon_drops_it() {
    let mut world = World::start(home(vec![studio()]));
    on_outposts(&mut world);
    click(&mut world, &part("repair"));
    assert_eq!(sent(&world, "repair_outpost"), [json!({"id": "outpost-1"})]);
    click(&mut world, &part("remove"));
    assert_eq!(sent(&world, "remove_outpost"), [json!({"id": "outpost-1"})]);
    assert!(shows(&world, ROW));
    list(&mut world, vec![]);
    assert!(!shows(&world, ROW));
}

#[test]
fn remove_asks_to_confirm_closing_the_outposts_terminals_and_cancel_keeps_it() {
    let daemon = Daemon::scripted().state(InitialStateMessage {
        sessions: Some(vec![Session {
            id: "remote".into(),
            label: "remote".into(),
            agent: "claude".into(),
            state: "idle".into(),
            profile_id: "work".into(),
            host: Some("outpost-1".into()),
            ..Session::default()
        }]),
        ..initial(vec![refused()])
    });
    daemon.on("remove_outpost", |_| ());
    let mut world = World::start(daemon);
    on_outposts(&mut world);
    click(&mut world, &part("remove"));
    assert!(world.daemon.sent_of("remove_outpost").is_empty());
    assert_eq!(
        named(&world, &part("remove")).as_deref(),
        Some("Remove and forget 1 terminal")
    );
    assert!(
        named(&world, &part("remove-note"))
            .unwrap()
            .starts_with("studio isn't connected.")
    );

    click(&mut world, &part("cancel"));
    assert_eq!(named(&world, &part("remove")).as_deref(), Some("Remove"));
    assert!(!shows(&world, &part("cancel")));

    click(&mut world, &part("remove"));
    click(&mut world, &part("remove"));
    assert_eq!(sent(&world, "remove_outpost"), [json!({"id": "outpost-1"})]);
}

#[test]
fn update_without_it_leaves_out_only_that_failed_outpost() {
    let laptop = OutpostInfo {
        id: "outpost-2".into(),
        name: "laptop".into(),
        ..studio()
    };
    let daemon = home(vec![studio(), laptop]);
    daemon.on(
        "start_update",
        |_| json!({"event": "start_update_result", "success": true}),
    );
    let mut world = World::start(daemon);
    on_outposts(&mut world);
    let failed = |id: &str, name: &str| UpdateOutpostProgress {
        id: id.into(),
        name: name.into(),
        state: "failed".into(),
        detail: Some("denied".into()),
    };
    world.daemon.emit(UpdateChangedMessage {
        run: UpdateRun {
            id: "run-1".into(),
            target_build: "v2".into(),
            install_kind: "source".into(),
            phase: "failed".into(),
            log_path: "/tmp/run.log".into(),
            outposts: vec![failed("outpost-1", "studio"), failed("outpost-2", "laptop")],
            ..UpdateRun::default()
        },
        ..UpdateChangedMessage::new()
    });
    world.settle();
    assert_eq!(named(&world, &part("run")).as_deref(), Some("denied"));
    click(&mut world, &part("without"));
    let without: Vec<_> = world
        .daemon
        .sent_of("start_update")
        .iter()
        .map(|r| r["without_outposts"].clone())
        .collect();
    assert_eq!(without, [json!(["outpost-1"])]);
}

#[test]
fn an_ssh_refusal_offers_a_terminal_and_a_failed_open_shows_in_the_dialog() {
    let error = "Host key verification failed.\nPlease contact your system administrator.\n";
    let row = OutpostInfo {
        connectable: Some(true),
        last_error: Some(error.into()),
        ..refused()
    };
    let daemon = home(vec![row.clone()]);
    daemon.on("connect_outpost_in_terminal", |r| {
        message(ProfileActionResultMessage {
            action: r["cmd"].as_str().unwrap().into(),
            success: false,
            error: Some("Connection terminal unavailable".into()),
            ..ProfileActionResultMessage::new()
        })
    });
    let mut world = World::start(daemon);
    on_outposts(&mut world);
    assert_eq!(named(&world, &part("error.text")).as_deref(), Some(error));
    let connect = world.screen().get(Target::key(part("connect")));
    assert_eq!(connect.name.as_deref(), Some("Connect in a terminal"));
    assert!(!connect.is("disabled"));

    click(&mut world, &part("connect"));
    assert_eq!(
        world.daemon.sent_of("connect_outpost_in_terminal")[0]["id"],
        "outpost-1"
    );
    assert_eq!(
        named(&world, ERROR).as_deref(),
        Some("Connection terminal unavailable")
    );
    assert_eq!(super::state(&mut world)["open"], true);

    list(
        &mut world,
        vec![OutpostInfo {
            connectable: Some(false),
            ..row.clone()
        }],
    );
    assert!(!shows(&world, &part("connect")));
    list(
        &mut world,
        vec![OutpostInfo {
            state: "lost".into(),
            ..row
        }],
    );
    assert!(!shows(&world, &part("connect")));
}

#[test]
fn an_opened_connection_terminal_shows_in_place_of_the_dialog() {
    let after = desktop(
        "desk",
        "a",
        "Desk",
        vec![pane("desk", "pane-connection", "connection")],
    );
    let daemon = Daemon::scripted().state(InitialStateMessage {
        desktops: Some(vec![after.clone()]),
        ..initial(vec![OutpostInfo {
            connectable: Some(true),
            ..refused()
        }])
    });
    daemon.on("connect_outpost_in_terminal", move |r| {
        message(ProfileActionResultMessage {
            action: r["cmd"].as_str().unwrap().into(),
            success: true,
            desktops: Some(vec![after.clone()]),
            pane_id: Some("pane-connection".into()),
            ..ProfileActionResultMessage::new()
        })
    });
    daemon.on("desktop_show_leaf", |r| {
        json!({"event": "desktop_show_leaf_result", "request_id": r["request_id"], "success": true})
    });
    let mut world = World::start(daemon);
    on_outposts(&mut world);
    click(&mut world, &part("connect"));
    assert_eq!(super::state(&mut world)["open"], false);
    let shown: Vec<_> = world
        .daemon
        .sent_of("desktop_show_leaf")
        .iter()
        .map(|r| (r["desktop_id"].clone(), r["leaf_id"].clone()))
        .collect();
    assert_eq!(shown, [("desk".into(), "pane-connection".into())]);
}

#[test]
fn the_last_error_shows_whole_through_reconnecting_until_connected() {
    let mut world = World::start(home(vec![]));
    on_outposts(&mut world);
    let error = "studio:2200 changed host key: home pinned studio-daemon SHA256:old-key; outpost offered SHA256:new-key. Re-pair this outpost to accept its new identity.\nBoth identities must be checked before reconnecting.";
    let row = OutpostInfo {
        last_error: Some(error.into()),
        last_error_at: Some("2026-10-18T18:01:00Z".into()),
        ..refused()
    };
    for state in ["refused", "connecting", "stalled", "lost"] {
        list(
            &mut world,
            vec![OutpostInfo {
                state: state.into(),
                ..row.clone()
            }],
        );
        assert_eq!(named(&world, &part("state")).as_deref(), Some(state));
        assert_eq!(named(&world, &part("error.text")).as_deref(), Some(error));
    }
    list(&mut world, vec![studio()]);
    assert!(!shows(&world, &part("error")));
}

#[test]
fn a_lost_connection_disables_changes_and_a_recovering_daemon_says_so() {
    let daemon = home(vec![studio()]);
    daemon.on("repair_outpost", |_| {
        json!({"event": "command_error", "cmd": "repair_outpost", "success": false, "error": "daemon_recovering"})
    });
    let mut world = World::start(daemon);
    on_outposts(&mut world);
    click(&mut world, &part("repair"));
    assert_eq!(
        named(&world, ERROR).as_deref(),
        Some("The home daemon is recovering. Try again when it is ready.")
    );

    world.daemon.refuse_connections(true);
    world.daemon.disconnect();
    world.settle();
    assert!(disabled(&world, SUBMIT));
    assert!(disabled(&world, &part("repair")));
    assert!(disabled(&world, &part("remove")));
    assert_eq!(
        named(&world, "settings.outposts.offline").as_deref(),
        Some("Connect to the home daemon to change outposts.")
    );
    click(&mut world, &part("repair"));
    assert_eq!(world.daemon.sent_of("repair_outpost").len(), 1);
}

#[test]
fn opening_the_page_shows_the_outposts_already_listed_without_asking_for_them() {
    let daemon = home(vec![]);
    let mut world = World::start(daemon);
    list(&mut world, vec![studio()]);
    open(&mut world);
    let commands = world.daemon.sent().len();
    click(&mut world, "settings.nav.outposts");
    assert!(shows(&world, ROW));
    assert_eq!(world.daemon.sent().len(), commands);
}

#[test]
fn notebook_sync_shows_its_state_what_is_left_its_conflicts_and_the_saved_folder() {
    let mut world = World::start(home(vec![]));
    on_outposts(&mut world);
    let with = |notebook: OutpostNotebook| OutpostInfo {
        notebook: Some(notebook),
        ..studio()
    };
    list(
        &mut world,
        vec![with(OutpostNotebook {
            state: "first_sync".into(),
            need_bytes: Some(3 * 1024 * 1024 / 2),
            need_items: Some(1),
            conflicts: Some(1),
            previous_root: Some("/home/me/notebook.previous".into()),
            ..OutpostNotebook::default()
        })],
    );
    assert_eq!(
        named(&world, &part("notebook.state")).as_deref(),
        Some("Notebook: first sync · 1.5 MB and 1 item left")
    );
    assert_eq!(
        named(&world, &part("notebook.conflicts")).as_deref(),
        Some("1 conflict copy")
    );
    assert_eq!(
        named(&world, &part("notebook.previous")).as_deref(),
        Some("Previous folder saved at /home/me/notebook.previous")
    );

    list(
        &mut world,
        vec![with(OutpostNotebook {
            state: "in_sync".into(),
            ..OutpostNotebook::default()
        })],
    );
    assert_eq!(
        named(&world, &part("notebook.state")).as_deref(),
        Some("Notebook: in sync")
    );
    assert!(!shows(&world, &part("notebook.conflicts")));

    list(
        &mut world,
        vec![with(OutpostNotebook {
            state: "error".into(),
            error: Some("permission denied".into()),
            ..OutpostNotebook::default()
        })],
    );
    let error = world.screen().get(Target::key(part("notebook.state")));
    assert_eq!(error.role, "alert");
    assert_eq!(
        error.name.as_deref(),
        Some("Notebook sync error: permission denied")
    );
    assert!(
        world
            .daemon
            .sent()
            .iter()
            .all(|r| !r["cmd"].as_str().unwrap().contains("outpost"))
    );
}

#[test]
fn a_row_updates_to_the_home_build_and_is_disabled_while_it_pushes() {
    let old = OutpostInfo {
        build: Some("0.13.0 (source git:old, protocol old)".into()),
        ..refused()
    };
    let mut world = World::start(home(vec![old.clone()]));
    on_outposts(&mut world);
    let update = part("update-build");
    assert_eq!(
        named(&world, &update).as_deref(),
        Some("Update to home build")
    );
    click(&mut world, &update);
    assert_eq!(sent(&world, "update_outpost"), [json!({"id": "outpost-1"})]);

    let phase = |phase: &str, outcome: Option<&str>| OutpostInfo {
        update: Some(OutpostUpdate {
            phase: phase.into(),
            outcome: outcome.map(str::to_owned),
        }),
        ..old.clone()
    };
    list(&mut world, vec![phase("downloading", None)]);
    assert_eq!(
        named(&world, &part("update")).as_deref(),
        Some("Downloading attn for Linux.")
    );
    assert_eq!(named(&world, &update).as_deref(), Some("Updating…"));
    assert!(disabled(&world, &update));

    list(
        &mut world,
        vec![phase(
            "failed",
            Some("Outpost database needs a newer build."),
        )],
    );
    assert_eq!(
        named(&world, &part("update")).as_deref(),
        Some("Outpost database needs a newer build.")
    );
    assert!(!disabled(&world, &update));
    assert_eq!(world.daemon.sent_of("update_outpost").len(), 1);
}

#[test]
fn an_outpost_on_the_home_build_without_the_shared_host_offers_to_install_it() {
    let lacking = OutpostInfo {
        host_missing: Some(true),
        ..studio()
    };
    let mut world = World::start(home(vec![lacking]));
    on_outposts(&mut world);
    assert_eq!(
        named(&world, &part("host")).as_deref(),
        Some("Shared terminal host Not installed")
    );
    assert_eq!(
        named(&world, &part("update-build")).as_deref(),
        Some("Install shared terminal host")
    );
    list(&mut world, vec![studio()]);
    assert!(!shows(&world, &part("update-build")));
}

#[test]
fn a_first_install_that_failed_with_no_known_build_still_offers_the_update() {
    let missing = OutpostInfo {
        build: None,
        last_error: Some("nothing installed".into()),
        update: Some(OutpostUpdate {
            phase: "failed".into(),
            outcome: Some("upload failed".into()),
        }),
        ..refused()
    };
    let mut world = World::start(home(vec![missing]));
    on_outposts(&mut world);
    assert_eq!(
        named(&world, &part("build")).as_deref(),
        Some("Build Not yet known")
    );
    click(&mut world, &part("update-build"));
    assert_eq!(sent(&world, "update_outpost"), [json!({"id": "outpost-1"})]);
}

#[test]
fn a_home_that_comes_back_with_outposts_off_drops_the_old_rows_for_its_own() {
    let mut world = World::start(home(vec![]));
    on_outposts(&mut world);
    list(&mut world, vec![studio()]);
    world.daemon.answer_hello_with(InitialStateMessage {
        settings: Some([("outposts_enabled".into(), "false".into())].into()),
        outposts: None,
        ..initial(vec![])
    });
    world.reconnect();
    world.settle();
    assert!(shows(&world, "settings.nav-off.outposts"));

    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([("outposts_enabled".into(), "true".into())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(
        named(&world, "settings.outposts.empty").as_deref(),
        Some("No outposts added.")
    );
    list(
        &mut world,
        vec![OutpostInfo {
            name: "New studio".into(),
            ..studio()
        }],
    );
    assert_eq!(named(&world, ROW).as_deref(), Some("New studio"));
    for cmd in ["add_outpost", "repair_outpost", "remove_outpost"] {
        assert!(world.daemon.sent_of(cmd).is_empty());
    }
}
