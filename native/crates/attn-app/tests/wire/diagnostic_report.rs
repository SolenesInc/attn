use std::path::{Path, PathBuf};
use std::time::Duration;

use attn_app::{Action, Folders};
use attn_protocol::{
    AttachResultMessage, Desktop, DesktopPane, InitialStateMessage, Profile,
    ProfileArrangementChangedMessage, Session,
};
use attn_term::SNAPSHOT_FORMAT;
use attn_testkit::{Daemon, Target, World, message, tree};
use rune::Ime;
use serde_json::{Value, json};

use super::keys::chord;
use super::terminal::{attached, snapshot_of};

const FIRST_OUTPUT: &str = "first pane says hi";
const SECOND_OUTPUT: &str = "second pane output";

fn pane(n: u8) -> DesktopPane {
    DesktopPane {
        desktop_id: "d1".into(),
        pane_id: format!("p{n}"),
        runtime_id: format!("r{n}"),
        session_id: format!("s{n}"),
        title: format!("Agent {n}"),
        kind: "agent".into(),
        ..DesktopPane::default()
    }
}

fn desktop(panes: Vec<DesktopPane>) -> Desktop {
    Desktop {
        id: "d1".into(),
        name: "Work desk".into(),
        active_pane_id: "p1".into(),
        tree_json: tree::panes_in_a_row(
            &panes.iter().map(|p| p.pane_id.as_str()).collect::<Vec<_>>(),
        ),
        panes,
        profile_id: "work".into(),
        ..Desktop::default()
    }
}

fn profile() -> Profile {
    Profile {
        id: "work".into(),
        name: "Work".into(),
        current_desktop_id: "d1".into(),
        ..Profile::default()
    }
}

fn session(n: u8) -> Session {
    Session {
        id: format!("s{n}"),
        label: format!("session {n}"),
        directory: format!("/home/me/project-{n}"),
        profile_id: "work".into(),
        ..Session::default()
    }
}

fn daemon() -> Daemon {
    super::palette::daemon(
        InitialStateMessage {
            selected_profile_id: Some("work".into()),
            profiles: Some(vec![profile()]),
            desktops: Some(vec![desktop(vec![pane(1), pane(2)])]),
            sessions: Some(vec![session(1), session(2)]),
            daemon_instance_id: Some("daemon-home".into()),
            ..InitialStateMessage::new()
        },
        Vec::new(),
    )
}

fn answering(daemon: Daemon) -> Daemon {
    daemon.on("attach_session", |request| {
        let id = request["id"].as_str().unwrap();
        let shown = if id == "r1" {
            FIRST_OUTPUT
        } else {
            SECOND_OUTPUT
        };
        message(AttachResultMessage {
            id: id.into(),
            last_seq: Some(1),
            snapshot: Some(snapshot_of(shown.as_bytes(), SNAPSHOT_FORMAT)),
            ..attached()
        })
    });
    snapshotting(&daemon, json!([]));
    daemon
}

/// A daemon whose `support_snapshot` reports `input_traces`.
fn snapshotting(daemon: &Daemon, input_traces: Value) {
    daemon.on("support_snapshot", move |_| {
        json!({
            "event": "support_snapshot_result",
            "protocol_version": "1",
            "daemon_instance_id": "daemon-home",
            "daemon_started_at_unix_ms": 1,
            "captured_at_unix_ms": 2,
            "backend": "worker",
            "warning_codes": [],
            "trace_capacity": 512,
            "trace_total": input_traces.as_array().map_or(0, Vec::len),
            "input_traces": input_traces,
            "runtimes": [],
        })
    });
}

/// Two agents side by side on the desktop the window shows, each with output on screen, and
/// the keyboard in the first.
fn on_the_desktop(daemon: Daemon) -> World {
    let mut world = World::start_sized(daemon, (2400.0, 900.0));
    world.open_desktop();
    world.settle();
    world
}

fn create_report(world: &mut World) {
    world.gesture(|app| app.key(chord(&Action::UiCommandPalette)));
    world.gesture(|app| app.type_text("diagnostic report"));
    world.gesture(|app| app.click(Target::key("palette.row.command:create-diagnostic-report")));
}

fn choice(world: &World, n: u8) -> attn_testkit::Node {
    world
        .screen()
        .get(Target::key(format!("diagnostic-report.pane.p{n}")))
}

fn checked(world: &World, n: u8) -> bool {
    choice(world, n).states.iter().any(|s| s == "checked")
}

fn click(world: &mut World, key: &str) {
    world.gesture(|app| app.click(Target::key(key)));
}

fn save(world: &mut World) {
    click(world, "diagnostic-report.save");
}

fn reports_in(folder: &Path) -> Vec<PathBuf> {
    let mut found: Vec<PathBuf> = std::fs::read_dir(folder)
        .map(|entries| entries.filter_map(|e| Some(e.ok()?.path())).collect())
        .unwrap_or_default();
    found.sort();
    found
}

fn downloads(world: &World) -> PathBuf {
    world.files().join("Downloads")
}

fn read(path: &Path) -> Value {
    serde_json::from_str(&std::fs::read_to_string(path).unwrap()).unwrap()
}

fn only_report(world: &World) -> Value {
    let found = reports_in(&downloads(world));
    assert_eq!(found.len(), 1, "{found:?}");
    read(&found[0])
}

fn has_omission(report: &Value, section: &str, reason: &str) -> bool {
    report["omissions"]
        .as_array()
        .unwrap()
        .iter()
        .any(|o| o["section"] == section && o["reason"] == reason)
}

#[test]
fn it_preselects_the_pane_the_user_was_in_and_saves_the_output_of_exactly_the_ticked_panes() {
    let mut world = on_the_desktop(answering(daemon()));
    create_report(&mut world);

    assert!(checked(&world, 1));
    assert!(!checked(&world, 2));
    click(&mut world, "diagnostic-report.pane.p2");
    save(&mut world);

    let report = only_report(&world);
    assert_eq!(report["schema"], "attn.support-report.v1");
    let panes: Vec<(String, String)> = report["paneContent"]
        .as_array()
        .unwrap()
        .iter()
        .map(|p| {
            (
                p["paneId"].as_str().unwrap().to_owned(),
                p["text"].as_str().unwrap().trim().to_owned(),
            )
        })
        .collect();
    assert_eq!(
        panes,
        [
            ("p1".to_owned(), FIRST_OUTPUT.to_owned()),
            ("p2".to_owned(), SECOND_OUTPUT.to_owned())
        ]
    );
    assert_eq!(report["daemons"][0]["daemon_instance_id"], "daemon-home");
    assert_eq!(report["app"]["sessions"][0]["cwd"], "~/project-1");
    assert_eq!(report["app"]["desktops"][0]["label"], "Work desk");
    assert!(
        world
            .screen()
            .find(Target::key("diagnostic-report-dialog"))
            .is_none()
    );
}

#[test]
fn it_asks_the_daemon_for_evidence_on_the_runtimes_it_can_describe() {
    let mut world = on_the_desktop(answering(daemon()));
    create_report(&mut world);

    let asked = world.daemon.sent_of("support_snapshot");
    assert_eq!(asked.len(), 1);
    assert_eq!(asked[0]["runtime_ids"], json!(["r1", "r2"]));
}

#[test]
fn clearing_the_output_saves_only_metadata_and_names_no_webview_evidence() {
    let mut world = on_the_desktop(answering(daemon()));
    create_report(&mut world);

    click(&mut world, "diagnostic-report.clear");
    save(&mut world);

    let report = only_report(&world);
    assert_eq!(report["paneContent"], json!([]));
    assert_eq!(report["consent"]["paneContent"], "metadata_only");
    let text = report.to_string();
    assert!(!text.contains(FIRST_OUTPUT) && !text.contains(SECOND_OUTPUT));
    for section in [
        "diagnostics.input.native",
        "diagnostics.terminal",
        "diagnostics.ui",
    ] {
        assert!(
            has_omission(&report, section, "not_collected_by_native_app"),
            "{section}"
        );
    }
}

#[test]
fn a_ticked_pane_that_left_the_screen_before_saving_is_marked_unavailable() {
    let mut world = on_the_desktop(answering(daemon()));
    create_report(&mut world);
    click(&mut world, "diagnostic-report.pane.p2");

    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile(),
        desktops: vec![desktop(vec![pane(1)])],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
    save(&mut world);

    let report = only_report(&world);
    assert_eq!(report["paneContent"][0]["available"], true);
    assert_eq!(
        report["paneContent"][1],
        json!({"paneId": "p2", "runtimeId": "r2", "available": false})
    );
    assert!(has_omission(&report, "paneContent:p2", "pane_not_mounted"));
}

#[test]
fn it_saves_a_partial_report_naming_what_it_could_not_collect_when_the_daemon_never_answers() {
    let mut world = on_the_desktop(daemon());
    create_report(&mut world);

    save(&mut world);
    assert!(reports_in(&downloads(&world)).is_empty());
    assert_eq!(
        world
            .screen()
            .get(Target::key("diagnostic-report.save"))
            .name
            .as_deref(),
        Some("Saving…")
    );

    world.advance(Duration::from_secs(3));

    let report = only_report(&world);
    assert_eq!(report["daemons"], json!([]));
    assert!(has_omission(&report, "daemons", "snapshots_unavailable"));
}

#[test]
fn it_saves_under_the_next_free_name_in_downloads_and_reveals_it() {
    let mut world = on_the_desktop(answering(daemon()));
    create_report(&mut world);
    save(&mut world);
    create_report(&mut world);
    save(&mut world);

    let found = reports_in(&downloads(&world));
    let names: Vec<String> = found
        .iter()
        .map(|p| p.file_name().unwrap().to_string_lossy().into_owned())
        .collect();
    assert_eq!(names.len(), 2, "{names:?}");
    let first = names
        .iter()
        .find(|n| n.ends_with(".attn-report.json"))
        .unwrap_or_else(|| panic!("{names:?}"));
    let stem = first.strip_suffix(".attn-report.json").unwrap();
    assert!(
        names.contains(&format!("{stem}.attn-report-2.json")),
        "{names:?}"
    );
    let stamp = stem.strip_prefix("attn-").unwrap();
    assert!(
        stamp.len() == 16 && stamp.as_bytes()[8] == b'-' && stamp.ends_with('Z'),
        "{stamp}"
    );
    let revealed = world.revealed_paths();
    assert_eq!(revealed.len(), 2);
    assert!(
        found
            .iter()
            .all(|path| revealed.contains(&path.to_string_lossy().into_owned()))
    );
}

#[test]
fn it_asks_where_to_save_when_downloads_cannot_be_written() {
    let mut world = on_the_desktop(answering(daemon()));
    world.use_folders(Folders {
        downloads: Some(world.files().join("no-such-folder")),
        home: None,
    });
    create_report(&mut world);
    save(&mut world);

    let (_, title) = world.file_dialog().expect("a save dialog");
    assert_eq!(title, "Save diagnostic report");
    let chosen = world.files().join("chosen.json");
    world.answer_file_dialog(Some(&chosen.to_string_lossy()));

    assert_eq!(read(&chosen)["schema"], "attn.support-report.v1");
    assert_eq!(
        world.revealed_paths(),
        [chosen.to_string_lossy().into_owned()]
    );
    assert!(
        world
            .screen()
            .find(Target::key("diagnostic-report-dialog"))
            .is_none()
    );
}

#[test]
fn cancelling_the_save_dialog_keeps_the_report_open_and_says_why() {
    let mut world = on_the_desktop(answering(daemon()));
    world.use_folders(Folders::default());
    create_report(&mut world);
    save(&mut world);

    world.answer_file_dialog(None);

    let error = world.screen().get(Target::key("diagnostic-report.error"));
    assert_eq!(error.role, "alert");
    assert!(error.name.unwrap().contains("Downloads"));
    assert_eq!(
        world
            .screen()
            .get(Target::key("diagnostic-report.save"))
            .name
            .as_deref(),
        Some("Save report")
    );
}

#[test]
fn it_uses_the_downloads_folder_in_the_home_folder_when_the_platform_names_none() {
    let mut world = on_the_desktop(answering(daemon()));
    let home = world.files().join("home");
    std::fs::create_dir_all(home.join("Downloads")).unwrap();
    world.use_folders(Folders {
        downloads: None,
        home: Some(home.clone()),
    });
    create_report(&mut world);
    save(&mut world);

    assert_eq!(reports_in(&home.join("Downloads")).len(), 1);
    assert!(world.file_dialog().is_none());
}

#[test]
fn escape_closes_the_dialog_without_saving() {
    let mut world = on_the_desktop(answering(daemon()));
    create_report(&mut world);
    assert!(
        world
            .screen()
            .find(Target::key("diagnostic-report-dialog"))
            .is_some()
    );

    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        })
    });

    assert!(
        world
            .screen()
            .find(Target::key("diagnostic-report-dialog"))
            .is_none()
    );
    assert!(reports_in(&downloads(&world)).is_empty());
}

#[test]
fn a_pane_the_window_has_no_output_for_cannot_be_ticked() {
    let mut world = World::start(answering(daemon()));
    world.open_desktop();
    world.settle();
    create_report(&mut world);

    let unshown = choice(&world, 2);
    assert!(
        unshown.states.iter().any(|s| s == "disabled"),
        "{unshown:?}"
    );
    assert_eq!(
        unshown.description.as_deref(),
        Some("session 2, not currently mounted")
    );
    assert!(!choice(&world, 1).states.iter().any(|s| s == "disabled"));
}

fn saved_text(world: &World) -> String {
    let found = reports_in(&downloads(world));
    std::fs::read_to_string(&found[0]).unwrap()
}

fn report_without_output(world: &mut World) -> Value {
    create_report(world);
    click(world, "diagnostic-report.clear");
    save(world);
    only_report(world)
}

fn input_log(world: &World) -> PathBuf {
    world.data_dir().join("debug/terminal-diagnostics.jsonl")
}

fn stages_of<'r>(report: &'r Value, trace: &str) -> Vec<&'r str> {
    report["diagnostics"]["input"]["frontend"]["events"]
        .as_array()
        .unwrap()
        .iter()
        .filter(|e| e["traceId"] == trace)
        .map(|e| e["stage"].as_str().unwrap())
        .collect()
}

#[test]
fn it_traces_a_key_press_from_the_window_to_the_socket_under_one_id_without_the_key() {
    let mut world = on_the_desktop(answering(daemon()));
    world.gesture(|app| app.type_text("ẞ"));
    let trace = world.daemon.sent_of("pty_input").last().unwrap()["trace_id"]
        .as_str()
        .expect("the write names its trace")
        .to_owned();

    let report = report_without_output(&mut world);

    assert_eq!(
        stages_of(&report, &trace),
        ["document", "terminal", "transport"]
    );
    assert!(!saved_text(&world).contains('ẞ'));
}

#[test]
fn the_report_follows_a_key_to_the_daemon_s_write_under_its_trace_id() {
    let mut world = on_the_desktop(answering(daemon()));
    world.gesture(|app| app.type_text("l"));
    let trace = world.daemon.sent_of("pty_input")[0]["trace_id"]
        .as_str()
        .unwrap()
        .to_owned();
    snapshotting(
        &world.daemon,
        json!([{
            "sequence": 1,
            "trace_id": trace,
            "runtime_id": "r1",
            "received_at_unix_ms": 5,
            "source": "user",
            "byte_count": 1,
            "write_duration_us": 80,
            "write_result": "accepted",
        }]),
    );

    let report = report_without_output(&mut world);

    let journeys = report["diagnostics"]["input"]["journeys"]
        .as_array()
        .unwrap();
    let journey = journeys.iter().find(|j| j["traceId"] == trace).unwrap();
    assert_eq!(journey["runtimeId"], "r1");
    assert_eq!(journey["terminalOutcome"], "sent");
    assert_eq!(journey["transportReady"], true);
    assert_eq!(
        journey["daemonWrite"],
        json!({"result": "accepted", "durationUs": 80})
    );
    assert_eq!(
        journey["conclusion"],
        "pty_write_succeeded_no_agent_evidence"
    );
}

#[test]
fn it_keeps_what_the_user_typed_into_the_find_field_composed_or_pasted_out_of_the_report() {
    let mut world = on_the_desktop(answering(daemon()));
    world.gesture(|app| app.key(chord(&Action::TerminalFind)));
    world.gesture(|app| app.type_text("SECRET_FIND"));
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        })
    });
    world.gesture(|app| {
        app.runtime.ime(Ime::Preedit("SECRET_PREEDIT".into(), None));
        app.runtime.ime(Ime::Commit("SECRET_PREEDIT".into()));
    });
    world.gesture(|app| app.runtime.paste("SECRET_CLIPBOARD"));
    world.advance(Duration::from_secs(31));
    world.gesture(|app| app.type_text("a"));

    let report = report_without_output(&mut world);

    let targets: Vec<&str> = report["diagnostics"]["input"]["frontend"]["events"]
        .as_array()
        .unwrap()
        .iter()
        .filter_map(|e| e["target"].as_str())
        .collect();
    assert!(targets.contains(&"terminal_find"), "{targets:?}");
    let events: Vec<&str> = report["diagnostics"]["input"]["frontend"]["events"]
        .as_array()
        .unwrap()
        .iter()
        .map(|e| e["event"].as_str().unwrap())
        .collect();
    assert!(
        events.contains(&"compositionend") && events.contains(&"paste"),
        "{events:?}"
    );
    let saved = saved_text(&world);
    let log = std::fs::read_to_string(input_log(&world)).unwrap();
    for secret in ["SECRET_FIND", "SECRET_PREEDIT", "SECRET_CLIPBOARD"] {
        assert!(!saved.contains(secret), "{secret} in the report");
        assert!(!log.contains(secret), "{secret} in the input log");
    }
}

#[test]
fn it_carries_the_input_samples_logged_before_and_since_the_app_started_and_nothing_else_from_that_log()
 {
    let mut world = on_the_desktop(answering(daemon()));
    let log = input_log(&world);
    std::fs::create_dir_all(log.parent().unwrap()).unwrap();
    std::fs::write(
        &log,
        [
            r#"{"kind":"input","pane":"before-restart"}"#,
            r#"{"kind":"paint","text":"PRIVATE_OUTPUT","context":[{"kind":"input"}]}"#,
            r#"{"kind":"input","#,
            "null",
            "",
        ]
        .join("\n"),
    )
    .unwrap();
    world.gesture(|app| app.type_text("a"));

    let report = report_without_output(&mut world);

    let historical = &report["diagnostics"]["input"]["historical"];
    let panes: Vec<&str> = historical["events"]
        .as_array()
        .unwrap()
        .iter()
        .map(|e| e["pane"].as_str().unwrap())
        .collect();
    assert_eq!(panes, ["before-restart", "p1"]);
    assert_eq!(historical["events"][1]["runtimeId"], "r1");
    assert_eq!(historical["events"][1]["counts"]["keydown:sent"], 1);
    assert!(!saved_text(&world).contains("PRIVATE_OUTPUT"));
}

#[test]
fn it_saves_an_empty_input_history_when_there_is_no_log_on_disk() {
    let mut world = on_the_desktop(answering(daemon()));
    create_report(&mut world);
    click(&mut world, "diagnostic-report.clear");
    let _ = std::fs::remove_file(input_log(&world));

    save(&mut world);

    let report = only_report(&world);
    let historical = &report["diagnostics"]["input"]["historical"];
    assert_eq!(historical["total"], 0);
    assert_eq!(historical["events"], json!([]));
    assert!(has_omission(
        &report,
        "historicalInput",
        "no_persisted_samples"
    ));
}

/// The first agent popped out into its own window, the second on the desktop the main window
/// shows with its keyboard.
fn with_the_first_agent_popped_out() -> (World, String) {
    let mut popped = tree::pane("p1");
    popped["window"] = json!({"x": 10.0, "y": 20.0, "width": 900.0, "height": 500.0});
    let desk = Desktop {
        active_pane_id: "p2".into(),
        tree_json: tree::row([popped, tree::pane("p2")]).to_string(),
        ..desktop(vec![pane(1), pane(2)])
    };
    let daemon = super::palette::daemon(
        InitialStateMessage {
            selected_profile_id: Some("work".into()),
            profiles: Some(vec![profile()]),
            desktops: Some(vec![desk]),
            sessions: Some(vec![session(1), session(2)]),
            daemon_instance_id: Some("daemon-home".into()),
            ..InitialStateMessage::new()
        },
        Vec::new(),
    );
    let world = on_the_desktop(answering(daemon));
    let window = world.tile_windows()[0].key.clone();
    (world, window)
}

/// The trace id of the key the daemon received for `runtime`, which the daemon's own trace
/// then reports as written.
fn received(world: &World, runtime: &str) -> String {
    let trace = world
        .daemon
        .sent_of("pty_input")
        .iter()
        .find(|m| m["id"] == runtime)
        .expect("the key reached the daemon")["trace_id"]
        .as_str()
        .unwrap()
        .to_owned();
    snapshotting(
        &world.daemon,
        json!([{
            "sequence": 1,
            "trace_id": trace,
            "runtime_id": runtime,
            "received_at_unix_ms": 5,
            "source": "user",
            "byte_count": 1,
            "write_duration_us": 80,
            "write_result": "accepted",
        }]),
    );
    trace
}

fn joined_journey<'r>(report: &'r Value, trace: &str) -> &'r Value {
    let journeys = report["diagnostics"]["input"]["journeys"]
        .as_array()
        .unwrap();
    let journey = journeys
        .iter()
        .find(|j| j["traceId"] == trace)
        .unwrap_or_else(|| panic!("no journey for {trace}: {journeys:?}"));
    assert_eq!(journey["terminalOutcome"], "sent", "{journey}");
    assert_eq!(
        journey["daemonWrite"],
        json!({"result": "accepted", "durationUs": 80}),
        "{journey}"
    );
    journey
}

#[test]
fn a_key_typed_in_a_popped_out_window_joins_its_daemon_write_in_a_report_from_the_main_window() {
    let (mut world, window) = with_the_first_agent_popped_out();
    world.tile_gesture(&window, |app| app.type_text("x"));
    let trace = received(&world, "r1");

    let report = report_without_output(&mut world);

    assert_eq!(joined_journey(&report, &trace)["runtimeId"], "r1");
}

#[test]
fn a_key_typed_in_the_main_window_joins_its_daemon_write_in_a_report_from_a_popped_out_window() {
    let (mut world, window) = with_the_first_agent_popped_out();
    world.gesture(|app| app.type_text("x"));
    let trace = received(&world, "r2");

    world.tile_gesture(&window, |app| app.key(chord(&Action::UiCommandPalette)));
    world.gesture(|app| app.type_text("diagnostic report"));
    world.gesture(|app| app.click(Target::key("palette.row.command:create-diagnostic-report")));
    click(&mut world, "diagnostic-report.clear");
    save(&mut world);
    let report = only_report(&world);

    assert_eq!(joined_journey(&report, &trace)["runtimeId"], "r2");
}

#[test]
fn after_the_close_button_tab_reaches_clear_before_the_first_pane() {
    let mut world = on_the_desktop(answering(daemon()));
    create_report(&mut world);
    let tab = |world: &mut World| {
        world.gesture(|app| {
            app.key(rune::KeyEvent {
                key: rune::Key::Tab,
                mods: rune::Mods::default(),
            })
        });
        world.screen().focused().and_then(|n| n.key)
    };
    let mut reached = Vec::new();
    for _ in 0..12 {
        let key = tab(&mut world).unwrap_or_default();
        if key == "diagnostic-report.pane.p1" {
            break;
        }
        reached.push(key);
    }
    let close = reached
        .iter()
        .position(|k| k == "close")
        .expect("Tab reaches the close button");
    assert_eq!(
        reached.get(close + 1).map(String::as_str),
        Some("diagnostic-report.clear"),
        "after the close button Tab went {reached:?}"
    );
}

#[test]
fn what_the_report_includes_on_its_own_is_said_once_and_not_announced() {
    let mut world = on_the_desktop(answering(daemon()));
    create_report(&mut world);
    let included = world
        .screen()
        .get(Target::key("diagnostic-report.included"));
    assert_ne!(
        included.role, "status",
        "a status is announced as the report opens"
    );
    assert_ne!(included.role, "alert");
}
