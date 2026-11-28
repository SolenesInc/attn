use attn_app::Action;
use attn_protocol::{
    AttachResultMessage, InitialStateMessage, OutpostInfo, OutpostNotebook, OutpostUpdate,
    OutpostsUpdatedMessage, Profile, ProfileActionResultMessage, Session, SessionRegisteredMessage,
    SettingsUpdatedMessage, TerminalBinding, TerminalBindingsUpdatedMessage, UpdateChangedMessage,
    UpdateOutpostProgress, UpdateRun,
};
use attn_testkit::{Daemon, Target, World, message};
use rune::Key;
use serde_json::{Value, json};

use super::{click, named, open, press, value};
use crate::wire::sidebar::{chord, desktop, pane};
use crate::wire::terminal::output_frame_to;
use crate::wire::terminal_blocks::ran;

const NAME: &str = "settings.machines.name";
const DESTINATION: &str = "settings.machines.destination";
const SUBMIT: &str = "settings.machines.submit";
const ERROR: &str = "settings.machines.error";
const ROW: &str = "settings.machine.outpost-1";
const HOME_ROW: &str = "settings.machine.home";
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

fn on_machines(world: &mut World) {
    open(world);
    click(world, "settings.nav.machines");
}

/// Opens the page with studio's row chosen, so its actions show.
fn on_studio(world: &mut World) {
    on_machines(world);
    click(world, ROW);
}

fn show_details(world: &mut World) {
    click(world, &part("details-toggle"));
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
    on_machines(&mut world);
    assert!(shows(&world, "settings.nav-off.machines"));

    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([("outposts_enabled".into(), "true".into())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert!(!shows(&world, "settings.nav-off.machines"));
    assert_eq!(
        named(&world, HOME_ROW).as_deref(),
        Some(if attn_testkit::mac() {
            "This Mac"
        } else {
            "This machine"
        })
    );
    assert_eq!(
        named(&world, &format!("{HOME_ROW}.home")).as_deref(),
        Some("home")
    );

    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([("outposts_enabled".into(), "false".into())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert!(shows(&world, "settings.nav-off.machines"));
    assert!(
        !world
            .screen()
            .get(Target::key("settings.feature.outposts_enabled"))
            .is("checked")
    );
}

#[test]
fn the_add_fields_and_button_sit_in_a_form_named_add_a_machine() {
    let mut world = World::start(home(vec![]));
    on_machines(&mut world);
    let screen = world.screen();
    let form = screen.get(Target::role("form").name("Add a machine"));
    assert_eq!(form.key.as_deref(), Some("settings.machines.add"));
    let inside: Vec<Option<String>> = screen
        .descendants(Target::key("settings.machines.add"))
        .into_iter()
        .map(|node| node.key)
        .collect();
    for key in [NAME, DESTINATION, SUBMIT] {
        assert!(
            inside.contains(&Some(key.into())),
            "{key} is not in the form"
        );
    }
}

#[test]
fn adds_by_name_and_ssh_destination_and_keeps_the_draft_until_a_list_holds_it() {
    let mut world = World::start(home(vec![]));
    on_machines(&mut world);
    assert!(disabled(&world, SUBMIT));
    type_into(&mut world, NAME, " studio ");
    type_into(&mut world, DESTINATION, " studio:2200 ");
    click(&mut world, SUBMIT);
    assert_eq!(
        sent(&world, "add_outpost"),
        [json!({"name": "studio", "destination": "studio:2200"})]
    );
    assert_eq!(value(&world, DESTINATION), " studio:2200 ");
    let commands = world.daemon.sent().len();

    list(&mut world, vec![studio()]);
    assert_eq!(value(&world, NAME), "");
    assert_eq!(value(&world, DESTINATION), "");

    type_into(&mut world, NAME, "laptop");
    type_into(&mut world, DESTINATION, "laptop:2200");
    click(&mut world, SUBMIT);
    type_into(&mut world, NAME, "the laptop");
    let laptop = OutpostInfo {
        id: "outpost-2".into(),
        name: "laptop".into(),
        destination: "laptop:2200".into(),
        ..studio()
    };
    list(&mut world, vec![studio(), laptop]);
    assert_eq!(
        value(&world, NAME),
        "the laptop",
        "a name typed after Add stays"
    );
    let commands = commands + 1;
    assert_eq!(named(&world, ROW).as_deref(), Some("studio"));
    assert_eq!(named(&world, &part("state")).as_deref(), Some("connected"));
    assert_eq!(named(&world, &part("status")).as_deref(), Some("0.13.0"));
    click(&mut world, ROW);
    show_details(&mut world);
    assert_eq!(
        named(&world, &part("destination")).as_deref(),
        Some("ssh destination studio:2200")
    );
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
    on_machines(&mut world);
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
    on_machines(&mut world);
    list(&mut world, vec![studio()]);
    type_into(&mut world, DESTINATION, "studio:2200");
    click(&mut world, SUBMIT);
    assert_eq!(value(&world, DESTINATION), "studio:2200");
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
    type_into(&mut world, DESTINATION, "laptop:2200");
    click(&mut world, SUBMIT);
    type_into(&mut world, DESTINATION, "next:2200");
    let laptop = OutpostInfo {
        id: "laptop".into(),
        name: "laptop".into(),
        destination: "laptop:2200".into(),
        ..studio()
    };
    list(&mut world, vec![studio(), laptop]);
    assert_eq!(value(&world, DESTINATION), "next:2200");
    assert!(!shows(&world, ERROR));
}

#[test]
fn re_pair_and_remove_name_the_row_and_the_row_stays_until_the_daemon_drops_it() {
    let mut world = World::start(home(vec![studio()]));
    on_studio(&mut world);
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
    on_studio(&mut world);
    click(&mut world, &part("remove"));
    assert!(world.daemon.sent_of("remove_outpost").is_empty());
    assert_eq!(
        named(&world, &part("remove")).as_deref(),
        Some("Remove and forget 1 terminal")
    );
    assert_eq!(
        named(&world, &part("remove-note")).as_deref(),
        Some(
            "studio isn't connected. If ssh cannot stop it, its daemon and shells keep running there. Run this instance's attn enrollment leave and attn daemon stop commands there."
        )
    );

    click(&mut world, &part("cancel"));
    assert_eq!(named(&world, &part("remove")).as_deref(), Some("Remove"));
    assert!(!shows(&world, &part("cancel")));

    click(&mut world, &part("remove"));
    click(&mut world, &part("remove"));
    assert_eq!(sent(&world, "remove_outpost"), [json!({"id": "outpost-1"})]);
}

#[test]
fn a_dial_in_machine_offers_no_re_pair_and_its_remove_says_it_leaves_on_its_next_dial() {
    let laptop = OutpostInfo {
        name: "laptop".into(),
        destination: String::new(),
        dials_home: Some(true),
        ..refused()
    };
    let daemon = Daemon::scripted().state(InitialStateMessage {
        sessions: Some(vec![Session {
            id: "remote".into(),
            label: "remote".into(),
            agent: "shell".into(),
            state: "idle".into(),
            profile_id: "work".into(),
            host: Some("outpost-1".into()),
            ..Session::default()
        }]),
        ..initial(vec![laptop])
    });
    let mut world = World::start(daemon);
    on_studio(&mut world);
    assert!(
        !shows(&world, &part("repair")),
        "the home cannot re-pair a machine that dials it"
    );
    show_details(&mut world);
    assert!(!shows(&world, &part("destination")));
    assert_eq!(
        named(&world, &part("explain")).as_deref(),
        Some(
            "It dials this home, which never reaches it by ssh. Remove closes its terminals and its bridge, and the machine leaves this home at its next dial. Its binary, data and Notebook stay."
        )
    );
    click(&mut world, &part("remove"));
    assert_eq!(
        named(&world, &part("remove-note")).as_deref(),
        Some(
            "laptop isn't connected. Its daemon and shells keep running there until it next dials this home, then it learns it was removed, stops this home's shells and becomes its own home again. To end that sooner, run this instance's attn enrollment leave on laptop."
        )
    );
}

#[test]
fn remove_counts_the_outposts_terminals_in_every_profile_and_keeps_the_keyboard_through_cancel() {
    let remote = |id: &str, profile: &str| Session {
        id: id.into(),
        label: id.into(),
        agent: "shell".into(),
        state: "idle".into(),
        profile_id: profile.into(),
        host: Some("outpost-1".into()),
        ..Session::default()
    };
    let daemon = Daemon::scripted().state(InitialStateMessage {
        sessions: Some(vec![
            remote("remote", "work"),
            remote("other", "other-profile"),
            Session {
                host: None,
                ..remote("local", "work")
            },
        ]),
        ..initial(vec![studio()])
    });
    daemon.on("remove_outpost", |_| ());
    let mut world = World::start(daemon);
    on_studio(&mut world);
    let focused = |world: &World| world.screen().focused().and_then(|n| n.key);
    let tab_and_press = |world: &mut World, key: String| {
        let mut seen = Vec::new();
        while focused(world).as_ref() != Some(&key) {
            assert!(!seen.contains(&focused(world)), "Tab never reaches {key}");
            seen.push(focused(world));
            press(world, Key::Tab);
        }
        press(world, Key::Text(" ".into()));
    };

    tab_and_press(&mut world, part("remove"));
    assert!(world.daemon.sent_of("remove_outpost").is_empty());
    assert_eq!(
        named(&world, &part("remove")).as_deref(),
        Some("Remove and close 2 terminals")
    );
    assert_eq!(focused(&world), Some(part("remove")));
    assert!(
        !shows(&world, &part("remove-note")),
        "a connected outpost stops its own shells"
    );

    tab_and_press(&mut world, part("cancel"));
    assert_eq!(named(&world, &part("remove")).as_deref(), Some("Remove"));
    assert_eq!(focused(&world), Some(part("remove")));
    assert!(world.daemon.sent_of("remove_outpost").is_empty());

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
    on_studio(&mut world);
    let failed = |id: &str, name: &str| UpdateOutpostProgress {
        id: id.into(),
        name: name.into(),
        state: "failed".into(),
        detail: Some("denied".into()),
        ..UpdateOutpostProgress::default()
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
    assert_eq!(named(&world, &part("status")).as_deref(), Some("denied"));
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
    on_studio(&mut world);
    show_details(&mut world);
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
    on_studio(&mut world);
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
    on_machines(&mut world);
    let error = "studio:2200 changed host key: home pinned studio-daemon SHA256:old-key; outpost offered SHA256:new-key. Re-pair this outpost to accept its new identity.\nBoth identities must be checked before reconnecting.";
    let row = OutpostInfo {
        last_error: Some(error.into()),
        last_error_at: Some("2026-10-18T18:01:00Z".into()),
        ..refused()
    };
    list(&mut world, vec![row.clone()]);
    click(&mut world, ROW);
    show_details(&mut world);
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
    on_studio(&mut world);
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
        named(&world, "settings.machines.offline").as_deref(),
        Some("Connect to the home daemon to change machines.")
    );
    let _refused = world.automate("press", json!({"target": {"key": part("repair")}}));
    assert_eq!(world.daemon.sent_of("repair_outpost").len(), 1);
}

#[test]
fn opening_the_page_shows_the_machines_already_listed_without_asking_for_them() {
    let daemon = home(vec![]);
    let mut world = World::start(daemon);
    list(&mut world, vec![studio()]);
    open(&mut world);
    let commands = world.daemon.sent().len();
    click(&mut world, "settings.nav.machines");
    assert!(shows(&world, ROW));
    assert_eq!(world.daemon.sent().len(), commands);
}

#[test]
fn notebook_sync_shows_its_state_what_is_left_its_conflicts_and_the_saved_folder() {
    let mut world = World::start(home(vec![studio()]));
    on_studio(&mut world);
    show_details(&mut world);
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
    on_studio(&mut world);
    let update = part("update-build");
    assert_eq!(named(&world, &update).as_deref(), Some("Update"));
    click(&mut world, &update);
    assert_eq!(sent(&world, "update_outpost"), [json!({"id": "outpost-1"})]);

    let phase = |phase: &str, outcome: Option<&str>| OutpostInfo {
        update: Some(OutpostUpdate {
            phase: phase.into(),
            outcome: outcome.map(str::to_owned),
            cause: None,
        }),
        ..old.clone()
    };
    list(&mut world, vec![phase("downloading", None)]);
    assert_eq!(
        named(&world, &part("status")).as_deref(),
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
        named(&world, &part("status")).as_deref(),
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
    on_studio(&mut world);
    show_details(&mut world);
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
            cause: None,
        }),
        ..refused()
    };
    let mut world = World::start(home(vec![missing]));
    on_studio(&mut world);
    show_details(&mut world);
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
    on_machines(&mut world);
    list(&mut world, vec![studio()]);
    world.daemon.answer_hello_with(InitialStateMessage {
        settings: Some([("outposts_enabled".into(), "false".into())].into()),
        outposts: None,
        ..initial(vec![])
    });
    world.reconnect();
    world.settle();
    assert!(shows(&world, "settings.nav-off.machines"));

    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([("outposts_enabled".into(), "true".into())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert!(!shows(&world, ROW));
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

#[test]
fn a_connection_terminal_stays_out_of_the_queue_and_home_in_either_flow() {
    for queue in ["true", "false"] {
        let connection = Session {
            agent: "shell".into(),
            command_terminal: Some(true),
            label: "Connect to studio".into(),
            ..crate::wire::queue::turn("connection", 5)
        };
        let mut state =
            crate::wire::queue::one_desktop(vec![connection, crate::wire::queue::turn("agent", 5)]);
        state.settings = Some([("queue_mode_enabled".into(), queue.into())].into());
        let world = World::start(Daemon::scripted().state(state));
        let shows = |key: &str| world.screen().find(Target::key(key)).is_some();
        assert!(
            shows("home.session.agent"),
            "queue {queue}: home lost the agent"
        );
        if queue == "true" {
            assert!(shows("queue-turn.agent"), "the agent's turn is missing");
        }
        for hidden in [
            "home.session.connection",
            "queue-turn.connection",
            "queue-settled.connection",
        ] {
            assert!(!shows(hidden), "queue {queue}: {hidden} shows");
        }
        if queue == "false" {
            assert!(
                world.runtime_is_at_rest(),
                "desktop flow has no ages to wake for"
            );
        }
    }
}

fn remote_home(outposts: Vec<OutpostInfo>) -> Daemon {
    let daemon = Daemon::scripted().state(remote_home_state(outposts));
    daemon.on("home_move_status", |_| {
        json!({"event": "home_move_result", "success": true, "home_move": {"step": "none", "checks": [], "remaps": []}})
    });
    daemon
}

/// A home with remote_home_enabled on, whose daemon keeps the desk while it dials its home.
fn remote_home_state(outposts: Vec<OutpostInfo>) -> InitialStateMessage {
    InitialStateMessage {
        daemon_instance_id: Some("laptop-daemon".into()),
        settings: Some(
            [
                ("outposts_enabled".into(), "true".into()),
                ("remote_home_enabled".into(), "true".into()),
            ]
            .into(),
        ),
        ..initial(outposts)
    }
}

fn move_result(home_move: Value) -> Value {
    json!({"event": "home_move_result", "success": true, "home_move": home_move})
}

#[test]
fn move_home_here_checks_the_machine_names_what_fails_and_moves_once_the_checks_pass() {
    let daemon = remote_home(vec![studio()]);
    daemon.on("home_move", |_| {
        move_result(
            json!({"step": "refused", "to": "studio", "remaps": [], "checks": [
                {"name": "connected", "ok": true},
                {"name": "gh", "ok": false, "detail": "studio is not logged in to gh"},
            ]}),
        )
    });
    let mut world = World::start(daemon);
    on_studio(&mut world);
    click(&mut world, &part("move"));
    let asked = sent(&world, "home_move");
    assert_eq!(asked.len(), 1);
    assert_eq!(asked[0]["machine"], "outpost-1");
    assert_eq!(asked[0]["check"], true);
    assert_eq!(
        named(&world, &part("move.checks.passed")).as_deref(),
        Some("✓ 1 check passed")
    );
    assert_eq!(
        named(&world, &part("move.check.gh")).as_deref(),
        Some("✗ gh: studio is not logged in to gh")
    );
    assert!(disabled(&world, &part("move.go")));

    world.daemon.on("home_move", |r| {
        (r["check"] == true).then(|| {
            move_result(json!({"step": "checked", "to": "studio", "sleeps": true,
                "checks": [{"name": "connected", "ok": true}, {"name": "gh", "ok": true}],
                "remaps": [{"from": "/Users/me/attn-notebook", "to": "/home/me/attn-notebook"}]}))
        })
    });
    click(&mut world, &part("move.again"));
    assert_eq!(
        named(&world, &part("move.checks.passed")).as_deref(),
        Some("✓ 2 checks passed")
    );
    assert!(!shows(&world, &part("move.check.gh")));
    assert_eq!(
        named(&world, &part("move.path.0")).as_deref(),
        Some("Notebook /Users/me/attn-notebook → /home/me/attn-notebook")
    );
    assert_eq!(
        named(&world, &part("move.sleeps")).as_deref(),
        Some("While studio sleeps, the home is down.")
    );
    assert_eq!(
        named(&world, &part("move.go")).as_deref(),
        Some("Move home to studio")
    );
    assert!(!disabled(&world, &part("move.go")));

    click(&mut world, &part("move.go"));
    let asked = sent(&world, "home_move");
    assert_eq!(asked.len(), 3);
    assert_eq!(asked[2]["machine"], "outpost-1");
    assert_eq!(
        asked[2].get("check"),
        None,
        "the move itself is not a check"
    );
    let request_id = world.daemon.sent_of("home_move")[2]["request_id"].clone();
    let shown = |world: &World, step: usize| named(world, &part(&format!("move.step.{step}")));
    assert_eq!(shown(&world, 1).as_deref(), Some("… Stop changes"));
    assert_eq!(
        named(&world, &part("status")).as_deref(),
        Some("stop changes")
    );
    for (begun, step) in [("copy", 2), ("build", 3)] {
        world
            .daemon
            .emit(json!({"event": "home_move_progress", "request_id": request_id, "id": "m-1", "step": begun}));
        world.settle();
        assert!(shown(&world, step).unwrap().starts_with('…'));
        assert!(shown(&world, step - 1).unwrap().starts_with('✓'));
    }
    assert_eq!(shown(&world, 3).as_deref(), Some("… Build database"));
    assert_eq!(shown(&world, 6).as_deref(), Some("○ Reconnect"));

    press(&mut world, Key::Escape);
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some("Moving home to studio")
    );
    let step = |world: &World| named(world, "home-move.step");
    assert_eq!(step(&world).as_deref(), Some("3/6 Build database"));
    let committed = json!({"id": "m-1", "side": "from", "step": "committed", "to": "studio", "checks": [], "remaps": []});
    let mut answer = move_result(committed.clone());
    answer["request_id"] = request_id;
    world.daemon.emit(answer);
    world.settle();
    assert_eq!(step(&world).as_deref(), Some("5/6 Start home on studio"));

    world.daemon.on(
        "client_hello",
        |_| json!({"event": "home_unreachable", "home": "studio", "state": "dialing"}),
    );
    redial(&mut world);
    assert_eq!(step(&world).as_deref(), Some("6/6 Reconnect"));

    world
        .daemon
        .on("home_move_status", move |_| move_result(committed.clone()));
    world.daemon.on("client_hello", |_| {
        message(remote_home_state(vec![studio()]))
    });
    redial(&mut world);
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some("Home moved to studio.")
    );
    click(&mut world, "home-move.dismiss");
    assert!(!shows(&world, "home-move.text"));
}

#[test]
fn move_home_here_shows_only_while_remote_home_is_on_and_the_machine_is_connected() {
    let mut world = World::start(home(vec![studio()]));
    on_studio(&mut world);
    assert!(!shows(&world, &part("move")), "remote_home_enabled is off");

    let mut world = World::start(remote_home(vec![refused()]));
    on_studio(&mut world);
    assert!(
        !shows(&world, &part("move")),
        "a machine that is not connected cannot take the home"
    );
    list(&mut world, vec![studio()]);
    assert!(shows(&world, &part("move")));
}

#[test]
fn a_failed_move_says_why_and_cancel_returns_to_the_row() {
    let daemon = remote_home(vec![studio()]);
    daemon.on("home_move", |r| {
        if r["check"] == true {
            move_result(json!({"step": "checked", "to": "studio", "checks": [{"name": "connected", "ok": true}], "remaps": []}))
        } else {
            move_result(json!({"id": "m-1", "side": "from", "step": "aborted", "to": "studio", "error": "studio did not stage the move: disk full", "checks": [], "remaps": []}))
        }
    });
    let mut world = World::start(daemon);
    on_studio(&mut world);
    click(&mut world, &part("move"));
    click(&mut world, &part("move.go"));
    assert_eq!(
        named(&world, &part("move.failed")).as_deref(),
        Some("Move failed: studio did not stage the move: disk full")
    );
    click(&mut world, &part("move.cancel"));
    assert!(!shows(&world, &part("move.failed")));
    assert!(shows(&world, &part("move")));
}

fn redial(world: &mut World) {
    world.daemon.disconnect();
    world.settle();
    world.advance(std::time::Duration::from_secs(1));
    world.settle();
}

#[test]
fn a_move_whose_home_did_not_start_rolls_back_from_its_band() {
    let daemon = remote_home(vec![studio()]);
    let moved = |step: &str| {
        move_result(
            json!({"id": "m-1", "side": "from", "step": step, "to": "studio", "checks": [], "remaps": []}),
        )
    };
    daemon.on("home_move_status", move |_| moved("applied"));
    daemon.on("home_move_rollback", move |_| moved("rolling-back"));
    let mut world = World::start(daemon);
    world.daemon.on("client_hello", |_| {
        json!({"event": "home_unreachable", "home": "studio", "state": "dialing", "reason": "connection refused"})
    });
    redial(&mut world);
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some("Move failed: studio’s home didn’t start."),
        "{}",
        world.screen()
    );
    click(&mut world, "home-move.roll-back");
    assert_eq!(world.daemon.sent_of("home_move_rollback").len(), 1);
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some("Rolling back")
    );

    world
        .daemon
        .on("home_move_status", move |_| moved("rolled-back"));
    world.daemon.on("client_hello", |_| {
        message(remote_home_state(vec![studio()]))
    });
    redial(&mut world);
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some(if attn_testkit::mac() {
            "Rolled back. The home is on this Mac."
        } else {
            "Rolled back. The home is on this machine."
        })
    );
}

#[test]
fn a_move_that_stopped_before_it_committed_says_so_once_the_window_has_the_home_again() {
    let daemon = remote_home(vec![studio()]);
    daemon.on("home_move_status", |_| {
        move_result(json!({"id": "m-1", "side": "from", "step": "done", "to": "oma", "checks": [], "remaps": []}))
    });
    daemon.on("home_move", |r| {
        (r["check"] == true).then(|| {
            move_result(json!({"step": "checked", "to": "studio", "checks": [{"name": "connected", "ok": true}], "remaps": []}))
        })
    });
    let mut world = World::start(daemon);
    on_studio(&mut world);
    click(&mut world, &part("move"));
    click(&mut world, &part("move.go"));
    redial(&mut world);
    assert_eq!(
        named(&world, &part("move.failed")).as_deref(),
        Some("Move failed: it stopped before it committed, so the home did not move"),
        "the record of the move before this one is not this move's outcome"
    );
    click(&mut world, &part("move.cancel"));
    assert!(shows(&world, &part("move")));
}

#[test]
fn a_move_is_judged_by_its_own_record_even_when_the_one_before_it_was_never_read() {
    let daemon = Daemon::scripted().state(remote_home_state(vec![studio()]));
    daemon.on("home_move", |r| {
        (r["check"] == true).then(|| {
            move_result(json!({"step": "checked", "to": "studio", "checks": [{"name": "connected", "ok": true}], "remaps": []}))
        })
    });
    let mut world = World::start(daemon);
    on_studio(&mut world);
    click(&mut world, &part("move"));
    click(&mut world, &part("move.go"));
    world.daemon.on("home_move_status", |_| {
        move_result(json!({"id": "m-0", "side": "from", "step": "done", "to": "oma", "checks": [], "remaps": []}))
    });
    redial(&mut world);
    assert_eq!(
        named(&world, &part("move.failed")).as_deref(),
        Some("Move failed: it stopped before it committed, so the home did not move")
    );
}

#[test]
fn a_move_held_after_its_home_restarted_as_it_committed_offers_retry_and_roll_back() {
    let daemon = remote_home(vec![studio()]);
    daemon.on("home_move", |r| {
        (r["check"] == true).then(|| {
            move_result(json!({"step": "checked", "to": "studio", "checks": [{"name": "connected", "ok": true}], "remaps": []}))
        })
    });
    let mut world = World::start(daemon);
    on_studio(&mut world);
    click(&mut world, &part("move"));
    click(&mut world, &part("move.go"));
    let request_id = world.daemon.sent_of("home_move")[1]["request_id"].clone();
    world
        .daemon
        .emit(json!({"event": "home_move_progress", "request_id": request_id, "id": "m-1", "step": "commit"}));
    world.settle();
    let committing = json!({"id": "m-1", "side": "from", "step": "committing", "to": "studio", "checks": [], "remaps": []});
    world
        .daemon
        .on("home_move_status", move |_| move_result(committing.clone()));
    redial(&mut world);
    press(&mut world, Key::Escape);
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some("Move to studio didn’t finish."),
        "{}",
        world.screen()
    );
    assert!(shows(&world, "home-move.roll-back"));

    world.daemon.on("home_move", |_| {
        move_result(json!({"id": "m-1", "side": "from", "step": "committing", "to": "studio", "error": "studio is unreachable", "checks": [], "remaps": []}))
    });
    click(&mut world, "home-move.retry");
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some("Move to studio didn’t finish.")
    );
    assert_eq!(
        named(&world, "home-move.why").as_deref(),
        Some("studio is unreachable"),
        "a retry that failed says why"
    );

    world.daemon.on("home_move", |_| {
        move_result(json!({"id": "m-1", "side": "from", "step": "committed", "to": "studio", "checks": [], "remaps": []}))
    });
    click(&mut world, "home-move.retry");
    let asked = sent(&world, "home_move");
    assert_eq!(asked.len(), 4);
    assert_eq!(asked[3]["machine"], "outpost-1");
    assert_eq!(asked[3].get("check"), None);
    assert_eq!(
        named(&world, "home-move.step").as_deref(),
        Some("5/6 Start home on studio")
    );
}

#[test]
fn checking_a_machine_while_a_move_to_it_commits_points_to_the_band() {
    let daemon = remote_home(vec![studio()]);
    daemon.on("home_move", |_| {
        move_result(json!({"id": "m-1", "side": "from", "step": "committing", "to": "studio", "checks": [], "remaps": []}))
    });
    let mut world = World::start(daemon);
    on_studio(&mut world);
    click(&mut world, &part("move"));
    assert_eq!(
        named(&world, &part("move.failed")).as_deref(),
        Some(
            "Move failed: the move to studio didn’t finish; retry it or roll it back from its band"
        )
    );
}

#[test]
fn a_home_waiting_for_its_target_after_a_rollback_says_so_and_stops_waiting_from_its_band() {
    let daemon = remote_home(vec![studio()]);
    daemon.on("home_move_status", |_| {
        move_result(json!({"id": "m-1", "side": "from", "step": "rolled-back", "to": "studio", "waits_for_target": true, "checks": [], "remaps": []}))
    });
    let released = json!({"id": "m-1", "side": "from", "step": "rolled-back", "to": "studio", "checks": [], "remaps": []});
    let answer = released.clone();
    daemon.on("home_move_rollback", move |_| move_result(answer.clone()));
    let mut world = World::start(daemon);
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some("Rolled back. Waiting for studio to rejoin as an outpost."),
        "{}",
        world.screen()
    );
    world
        .daemon
        .on("home_move_status", move |_| move_result(released.clone()));
    click(&mut world, "home-move.stop-waiting");
    assert_eq!(world.daemon.sent_of("home_move_rollback").len(), 1);
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some(if attn_testkit::mac() {
            "Rolled back. The home is on this Mac."
        } else {
            "Rolled back. The home is on this machine."
        })
    );
}

#[test]
fn a_window_that_reconnects_while_its_move_runs_its_checks_keeps_following_the_move() {
    let daemon = remote_home(vec![studio()]);
    daemon.on("home_move", |r| {
        (r["check"] == true).then(|| {
            move_result(json!({"step": "checked", "to": "studio", "checks": [{"name": "connected", "ok": true}], "remaps": []}))
        })
    });
    let mut world = World::start(daemon);
    on_studio(&mut world);
    click(&mut world, &part("move"));
    click(&mut world, &part("move.go"));
    world.daemon.on("home_move_status", |r| {
        (r["wait"] != true).then(|| {
            move_result(json!({"id": "m-2", "side": "from", "step": "checking", "to": "studio", "checks": [], "remaps": []}))
        })
    });
    redial(&mut world);
    press(&mut world, Key::Escape);
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some("Moving home to studio"),
        "{}",
        world.screen()
    );
    world.daemon.on("home_move_status", |r| {
        (r["wait"] != true).then(|| {
            move_result(json!({"id": "m-2", "side": "from", "step": "committed", "to": "studio", "checks": [], "remaps": []}))
        })
    });
    redial(&mut world);
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some("Home moved to studio.")
    );
}

#[test]
fn a_window_that_reconnects_once_its_move_staged_keeps_following_the_move() {
    let daemon = remote_home(vec![studio()]);
    daemon.on("home_move", |r| {
        (r["check"] == true).then(|| {
            move_result(json!({"step": "checked", "to": "studio", "checks": [{"name": "connected", "ok": true}], "remaps": []}))
        })
    });
    let mut world = World::start(daemon);
    on_studio(&mut world);
    click(&mut world, &part("move"));
    click(&mut world, &part("move.go"));
    world.daemon.on("home_move_status", |r| {
        (r["wait"] != true).then(|| {
            move_result(json!({"id": "m-2", "side": "from", "step": "staged", "to": "studio", "checks": [], "remaps": []}))
        })
    });
    redial(&mut world);
    press(&mut world, Key::Escape);
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some("Moving home to studio"),
        "{}",
        world.screen()
    );
}

#[test]
fn the_waiting_band_ends_once_the_target_rejoins_as_an_outpost() {
    let daemon = remote_home(vec![refused()]);
    daemon.on("home_move_status", |_| {
        move_result(json!({"id": "m-1", "side": "from", "step": "rolled-back", "to": "studio", "waits_for_target": true, "checks": [], "remaps": []}))
    });
    let mut world = World::start(daemon);
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some("Rolled back. Waiting for studio to rejoin as an outpost.")
    );
    world.daemon.on("home_move_status", |_| {
        move_result(json!({"id": "m-1", "side": "from", "step": "rolled-back", "to": "studio", "checks": [], "remaps": []}))
    });
    list(&mut world, vec![studio()]);
    assert!(!shows(&world, "home-move.text"), "{}", world.screen());
}

fn held_daemon(daemon: Daemon) -> Daemon {
    daemon.on("home_move_status", |_| {
        move_result(json!({"id": "m-1", "side": "from", "step": "committing", "to": "studio", "checks": [], "remaps": []}))
    });
    daemon
}

#[test]
fn rolling_back_a_held_move_on_a_remote_home_names_that_home() {
    let daemon = held_daemon(remote_home(vec![studio()]).relaying_to("outpost-9", "oma"));
    daemon.on("home_move_rollback", |_| {
        move_result(json!({"id": "m-1", "side": "from", "step": "aborted", "to": "studio", "checks": [], "remaps": []}))
    });
    let mut world = World::start(daemon);
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some("Move to studio didn’t finish."),
        "{}",
        world.screen()
    );
    world.daemon.on("home_move_status", |_| {
        move_result(json!({"id": "m-1", "side": "from", "step": "aborted", "to": "studio", "checks": [], "remaps": []}))
    });
    click(&mut world, "home-move.roll-back");
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some("Rolled back. The home is on oma.")
    );
}

#[test]
fn a_roll_back_the_home_refuses_shows_why_on_the_band() {
    let daemon = held_daemon(remote_home(vec![studio()]));
    daemon.on("home_move_rollback", |_| {
        json!({"event": "home_move_result", "success": false, "error": "studio did not abort its side: connection lost"})
    });
    let mut world = World::start(daemon);
    click(&mut world, "home-move.roll-back");
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some("Move to studio didn’t finish.")
    );
    assert_eq!(
        named(&world, "home-move.why").as_deref(),
        Some("studio did not abort its side: connection lost")
    );

    world.daemon.on("home_move_status", |_| {
        move_result(json!({"id": "m-1", "side": "from", "step": "rolled-back", "to": "studio", "waits_for_target": true, "checks": [], "remaps": []}))
    });
    redial(&mut world);
    assert_eq!(
        named(&world, "home-move.why").as_deref(),
        Some("Until then, this home runs no background jobs and no phone link."),
        "the band of a move rolled back elsewhere no longer shows why the window's Roll back failed"
    );
}

#[test]
fn a_roll_back_of_a_home_that_did_not_start_shows_why_it_failed() {
    let daemon = remote_home(vec![studio()]);
    daemon.on("home_move_status", |_| {
        move_result(json!({"id": "m-1", "side": "from", "step": "applied", "to": "studio", "checks": [], "remaps": []}))
    });
    daemon.on("home_move_rollback", |_| {
        json!({"event": "home_move_result", "success": false, "error": "move m-1 is done; run attn home move to bring the home back"})
    });
    let mut world = World::start(daemon);
    world.daemon.on("client_hello", |_| {
        json!({"event": "home_unreachable", "home": "studio", "state": "dialing", "reason": "connection refused"})
    });
    redial(&mut world);
    click(&mut world, "home-move.roll-back");
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some("Move failed: studio’s home didn’t start.")
    );
    assert_eq!(
        named(&world, "home-move.why").as_deref(),
        Some("move m-1 is done; run attn home move to bring the home back")
    );
}

#[test]
fn a_window_that_reconnected_during_its_moves_checks_hears_how_the_move_ended() {
    let mut world = reconnected_during_checks(|r| {
        (r["wait"] != true).then(|| {
            move_result(json!({"id": "m-2", "side": "from", "step": "checking", "to": "studio", "checks": [], "remaps": []}))
        })
    });
    world.daemon.on("home_move_status", |r| {
        Some(if r["wait"] == true {
            move_result(json!({"id": "m-2", "step": "aborted", "to": "studio", "error": "studio did not stage the move: disk full", "checks": [], "remaps": []}))
        } else {
            move_result(json!({"id": "m-2", "side": "from", "step": "checking", "to": "studio", "checks": [], "remaps": []}))
        })
    });
    redial(&mut world);
    assert_eq!(
        waits(&world),
        2,
        "a window asks again for the move's answer once the connection it asked on drops"
    );
    assert_eq!(
        named(&world, &part("move.failed")).as_deref(),
        Some("Move failed: studio did not stage the move: disk full")
    );
    press(&mut world, Key::Escape);
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some("Move to studio failed.")
    );
    assert_eq!(
        named(&world, "home-move.why").as_deref(),
        Some("studio did not stage the move: disk full")
    );
    click(&mut world, "home-move.dismiss");
    assert!(!shows(&world, "home-move.text"));
}

#[test]
fn a_move_that_stops_while_it_commits_holds_on_the_band() {
    let daemon = remote_home(vec![studio()]);
    daemon.on("home_move", |r| {
        (r["check"] == true).then(|| {
            move_result(json!({"step": "checked", "to": "studio", "checks": [{"name": "connected", "ok": true}], "remaps": []}))
        })
    });
    let mut world = World::start(daemon);
    on_studio(&mut world);
    click(&mut world, &part("move"));
    click(&mut world, &part("move.go"));
    let request_id = world.daemon.sent_of("home_move").last().unwrap()["request_id"].clone();
    world.daemon.on("home_move_status", |_| {
        move_result(json!({"id": "m-2", "side": "from", "step": "committing", "to": "studio", "checks": [], "remaps": []}))
    });
    let mut answer = move_result(
        json!({"id": "m-2", "side": "from", "step": "committing", "to": "studio", "error": "studio did not confirm the commit: connection lost", "checks": [], "remaps": []}),
    );
    answer["request_id"] = request_id;
    world.daemon.emit(answer);
    world.settle();
    press(&mut world, Key::Escape);
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some("Move to studio didn’t finish."),
        "{}",
        world.screen()
    );
    assert_eq!(
        named(&world, "home-move.why").as_deref(),
        Some("studio did not confirm the commit: connection lost")
    );
}

#[test]
fn a_window_that_hears_its_move_committed_while_it_waited_keeps_following_the_move() {
    let mut world = reconnected_during_checks(|r| {
        Some(if r["wait"] == true {
            move_result(
                json!({"id": "m-2", "step": "committed", "to": "studio", "checks": [], "remaps": []}),
            )
        } else {
            move_result(
                json!({"id": "m-2", "side": "from", "step": "checking", "to": "studio", "checks": [], "remaps": []}),
            )
        })
    });
    assert_eq!(waits(&world), 1);
    press(&mut world, Key::Escape);
    assert_eq!(
        named(&world, "home-move.step").as_deref(),
        Some("5/6 Start home on studio"),
        "{}",
        world.screen()
    );
}

/// Starts a move to studio, then reconnects the window while the move runs its checks, with status answering.
fn reconnected_during_checks(
    status: impl Fn(&serde_json::Value) -> Option<serde_json::Value> + Send + Sync + 'static,
) -> World {
    let daemon = remote_home(vec![studio()]);
    daemon.on("home_move", |r| {
        (r["check"] == true).then(|| {
            move_result(json!({"step": "checked", "to": "studio", "checks": [{"name": "connected", "ok": true}], "remaps": []}))
        })
    });
    let mut world = World::start(daemon);
    on_studio(&mut world);
    click(&mut world, &part("move"));
    click(&mut world, &part("move.go"));
    world.daemon.on("home_move_status", status);
    redial(&mut world);
    world
}

fn waits(world: &World) -> usize {
    world
        .daemon
        .sent_of("home_move_status")
        .iter()
        .filter(|r| r["wait"] == true)
        .count()
}

fn refused_linger() -> Value {
    move_result(
        json!({"step": "refused", "to": "studio", "remaps": [], "checks": [
            {"name": "connected", "ok": true},
            {"name": "linger", "ok": false, "detail": "linger is off for victor, so the home would stop when the user logs out; run loginctl enable-linger victor", "fix": "loginctl enable-linger victor"},
            {"name": "systemd", "ok": false, "detail": "systemctl --user show-environment: exit status 1"},
        ]}),
    )
}

fn fix_shell(session: &str) -> Session {
    Session {
        id: session.into(),
        label: session.into(),
        agent: "shell".into(),
        state: "idle".into(),
        directory: "/home/victor".into(),
        host: Some("outpost-1".into()),
        profile_id: "work".into(),
        ..Session::default()
    }
}

/// The home answers the fix's spawn: the shell's session, then the linger check's binding to it.
fn fix_bound(world: &mut World, session: &str, terminal: &str) {
    world.daemon.emit(SessionRegisteredMessage {
        session: fix_shell(session),
        ..SessionRegisteredMessage::new()
    });
    world.daemon.emit(TerminalBindingsUpdatedMessage {
        terminal_bindings: vec![TerminalBinding {
            ask_id: None,
            move_check: Some("linger".into()),
            session_id: session.into(),
            terminal_id: terminal.into(),
        }],
        ..TerminalBindingsUpdatedMessage::new()
    });
    world.settle();
}

fn fixing_world() -> World {
    let daemon = remote_home(vec![studio()]);
    daemon.on("home_move", |_| refused_linger());
    daemon.on("attach_session", |r| {
        message(AttachResultMessage {
            id: r["id"].as_str().unwrap().into(),
            success: true,
            running: Some(true),
            ..AttachResultMessage::new()
        })
    });
    let mut world = World::start(daemon);
    on_studio(&mut world);
    click(&mut world, &part("move"));
    world
}

fn typed_into(world: &World, terminal: &str) -> String {
    (world.daemon.sent_of("pty_input").iter())
        .filter(|r| r["id"] == terminal)
        .map(|r| r["data"].as_str().unwrap_or_default().to_owned())
        .collect()
}

fn slots(world: &World, terminal: &str) -> usize {
    world
        .screen()
        .all(Target::key(format!("terminal.{terminal}")))
        .len()
}

#[test]
fn a_failed_check_with_a_fix_opens_it_typed_in_a_shell_on_the_target_under_the_check() {
    let mut world = fixing_world();
    assert_eq!(
        named(&world, &part("move.check.linger.open")).as_deref(),
        Some("Open in terminal")
    );
    assert!(
        !shows(&world, &part("move.check.systemd.open")),
        "a check the daemon names no fix for offers none"
    );

    click(&mut world, &part("move.check.linger.open"));
    let spawned = sent(&world, "spawn_session");
    assert_eq!(spawned.len(), 1);
    let spawn = &spawned[0];
    assert_eq!(spawn["agent"], "shell");
    assert_eq!(spawn["host"], "outpost-1");
    assert_eq!(spawn["cwd"], "~", "the shell opens in the target's home");
    assert_eq!(spawn["typed"], "loginctl enable-linger victor");
    assert_eq!(spawn["placement"]["move_check"], "linger");
    assert!(spawn["placement"]["desktop_id"].is_null(), "{spawn}");
    assert_eq!(
        named(&world, &part("move.check.linger.starting")).as_deref(),
        Some("Starting a shell…")
    );

    let session = spawn["id"].as_str().unwrap().to_owned();
    fix_bound(&mut world, &session, "rt-fix");
    assert_eq!(slots(&world, "rt-fix"), 1, "{}", world.screen());
    assert!(
        (world.daemon.sent_of("attach_session").iter()).any(|r| r["id"] == "rt-fix"),
        "the fix's terminal attaches"
    );
    assert!(
        shows(&world, "settings.nav.machines"),
        "Settings stays open"
    );
    assert_eq!(
        named(&world, &part("move.check.linger.place")).as_deref(),
        Some("studio · /home/victor")
    );

    world.gesture(|app| app.type_text("x"));
    world.gesture(|app| app.press("Escape"));
    world.settle();
    assert_eq!(
        typed_into(&world, "rt-fix"),
        "x\u{1b}",
        "the shell holds the keys"
    );
    assert!(
        shows(&world, "settings.nav.machines"),
        "Escape stays in the shell"
    );

    world.gesture(|app| app.key(chord(Action::UiOpenSettings)));
    world.settle();
    assert!(
        shows(&world, "settings.nav.machines"),
        "the Settings chord hands the keys back"
    );
    world.gesture(|app| app.type_text("z"));
    world.settle();
    assert_eq!(
        typed_into(&world, "rt-fix"),
        "x\u{1b}",
        "the checks hold the keys again"
    );

    click(&mut world, "terminal.rt-fix");
    world.gesture(|app| app.type_text("y"));
    world.settle();
    assert_eq!(
        typed_into(&world, "rt-fix"),
        "x\u{1b}y",
        "a click gives the shell the keys"
    );

    assert!(
        !shows(&world, &part("move.check.linger.open")),
        "the open shell stands in for Open in terminal"
    );
}

#[test]
fn the_checks_run_again_once_the_fix_finishes_cleanly_and_its_shell_goes_once_the_check_passes() {
    let mut world = fixing_world();
    click(&mut world, &part("move.check.linger.open"));
    let session = sent(&world, "spawn_session")[0]["id"]
        .as_str()
        .unwrap()
        .to_owned();
    fix_bound(&mut world, &session, "rt-fix");
    let checks = |world: &World| sent(world, "home_move").len();
    assert_eq!(checks(&world), 1);

    world.daemon.emit_frame(output_frame_to(
        "rt-fix",
        1,
        b"\x1b]133;A\x07$ \x1b]133;B\x07loginctl enable-linger victor\r\n\x1b]133;C\x07Access denied\r\n\x1b]133;D;1\x07\x1b]133;A\x07$ ",
    ));
    world.settle();
    assert_eq!(checks(&world), 1, "a fix that failed checks nothing again");
    assert_eq!(
        named(&world, &part("move.check.linger.hint")).as_deref(),
        Some("It exited with 1.")
    );

    let answer = std::sync::Arc::new(std::sync::Mutex::new(None::<Value>));
    let held = answer.clone();
    world.daemon.on("home_move", move |r| {
        let mut held = held.lock().unwrap();
        *held = Some(r.clone());
        None::<Value>
    });
    world.daemon.emit_frame(output_frame_to(
        "rt-fix",
        2,
        &ran("loginctl enable-linger victor", ""),
    ));
    world.settle();
    assert_eq!(checks(&world), 2, "a clean exit checks again");
    assert_eq!(sent(&world, "home_move")[1]["check"], true);
    assert_eq!(
        named(&world, &part("move.checks.passed")).as_deref(),
        Some("✓ 1 check passed · Checking again…"),
        "the checks stay while they run again"
    );
    assert_eq!(slots(&world, "rt-fix"), 1, "the shell stays while they run");

    let request_id = answer.lock().unwrap().clone().unwrap()["request_id"].clone();
    let mut passed = move_result(
        json!({"step": "refused", "to": "studio", "remaps": [], "checks": [
            {"name": "connected", "ok": true},
            {"name": "linger", "ok": true},
            {"name": "systemd", "ok": false, "detail": "systemctl --user show-environment: exit status 1"},
        ]}),
    );
    passed["request_id"] = request_id;
    world.daemon.emit(passed);
    world.settle();
    assert_eq!(
        named(&world, &part("move.checks.passed")).as_deref(),
        Some("✓ 2 checks passed")
    );
    assert!(!shows(&world, &part("move.check.linger")));
    assert_eq!(
        slots(&world, "rt-fix"),
        0,
        "the shell goes once its check passes"
    );
}

#[test]
fn close_terminal_and_cancel_end_the_fix_shell() {
    let mut world = fixing_world();
    click(&mut world, &part("move.check.linger.open"));
    let session = sent(&world, "spawn_session")[0]["id"]
        .as_str()
        .unwrap()
        .to_owned();
    fix_bound(&mut world, &session, "rt-fix");

    click(&mut world, &part("move.check.linger.close"));
    let ended = sent(&world, "unregister");
    assert_eq!(ended.len(), 1);
    assert_eq!(ended[0]["id"], session.as_str());
    assert_eq!(slots(&world, "rt-fix"), 0);
    assert!(shows(&world, &part("move.check.linger.open")));

    click(&mut world, &part("move.check.linger.open"));
    let again = sent(&world, "spawn_session")[1]["id"]
        .as_str()
        .unwrap()
        .to_owned();
    fix_bound(&mut world, &again, "rt-fix-2");
    click(&mut world, &part("move.cancel"));
    let ended = sent(&world, "unregister");
    assert_eq!(ended.len(), 2, "Cancel ends the fix shells of the move");
    assert_eq!(ended[1]["id"], again.as_str());
}

#[test]
fn a_shell_a_check_still_holds_shows_under_it_again_after_settings_reopens() {
    let mut world = fixing_world();
    click(&mut world, &part("move.check.linger.open"));
    let session = sent(&world, "spawn_session")[0]["id"]
        .as_str()
        .unwrap()
        .to_owned();
    fix_bound(&mut world, &session, "rt-fix");

    for _ in 0..2 {
        world.gesture(|app| app.key(chord(Action::UiOpenSettings)));
        world.settle();
    }
    assert!(
        !shows(&world, "settings.nav.machines"),
        "the second chord closes Settings"
    );
    assert!(
        sent(&world, "unregister").is_empty(),
        "closing Settings keeps the shell"
    );
    assert_eq!(slots(&world, "rt-fix"), 0);

    on_machines(&mut world);
    assert_eq!(slots(&world, "rt-fix"), 1, "{}", world.screen());
}

#[test]
fn the_home_moved_band_asks_the_chief_to_check_the_paths_only_the_old_home_has() {
    let daemon = remote_home(vec![studio()]);
    daemon.on("home_move", |r| {
        (r["check"] == true).then(|| {
            move_result(json!({"step": "checked", "to": "studio", "checks": [{"name": "connected", "ok": true}], "remaps": []}))
        })
    });
    daemon.on("quick_capture_send", |r| {
        json!({"event": "quick_capture_result", "request_id": r["request_id"], "profile_id": "work",
            "success": false, "error_code": "crew_member_not_found", "error": "the Chief is not set up"})
    });
    let mut world = World::start(daemon);
    on_studio(&mut world);
    click(&mut world, &part("move"));
    click(&mut world, &part("move.go"));
    for step in ["checking", "committed"] {
        world.daemon.on("home_move_status", move |r| {
            (r["wait"] != true).then(|| {
                move_result(json!({"id": "m-2", "side": "from", "step": step, "from": "laptop", "to": "studio", "checks": [], "remaps": []}))
            })
        });
        redial(&mut world);
    }
    press(&mut world, Key::Escape);
    assert_eq!(
        named(&world, "home-move.text").as_deref(),
        Some("Home moved to studio.")
    );
    assert_eq!(
        named(&world, "home-move.paths").as_deref(),
        Some("Automations and crew members may still launch in folders on laptop.")
    );

    click(&mut world, "home-move.chief");
    assert_eq!(
        named(&world, "home-move.paths").as_deref(),
        Some("Could not ask the Chief: the Chief is not set up")
    );
    assert_eq!(
        named(&world, "home-move.chief").as_deref(),
        Some("Ask again")
    );

    world.daemon.on("quick_capture_send", |r| {
        json!({"event": "quick_capture_result", "request_id": r["request_id"], "profile_id": "work", "success": true,
            "result": {"record": {"id": r["capture_id"], "mailbox": {"kind": "crew_member", "member_id": "chief"},
                "content": r["content"], "attachments": [], "created_at": "2026-11-27T22:00:00Z"}}})
    });
    click(&mut world, "home-move.chief");
    let asked = sent(&world, "quick_capture_send");
    assert_eq!(asked.len(), 2);
    let ask = &asked[1];
    assert_eq!(
        ask["mailbox"],
        json!({"kind": "crew_member", "member_id": "chief"})
    );
    assert_eq!(ask["attachment_ids"], json!([]));
    let content = ask["content"].as_str().unwrap();
    assert!(
        content.starts_with("The home moved from laptop to studio."),
        "{content}"
    );
    assert!(content.contains("doesn't exist on studio"), "{content}");
    assert_eq!(
        named(&world, "home-move.paths").as_deref(),
        Some("The Chief is checking for paths that don’t exist on studio.")
    );
    assert!(!shows(&world, "home-move.chief"));
}
