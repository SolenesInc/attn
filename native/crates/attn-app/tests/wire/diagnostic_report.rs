use std::path::{Path, PathBuf};
use std::time::Duration;

use attn_app::{Action, Folders};
use attn_protocol::{
    AttachResultMessage, Desktop, DesktopPane, InitialStateMessage, Profile,
    ProfileArrangementChangedMessage, Session,
};
use attn_term::SNAPSHOT_FORMAT;
use attn_testkit::{Daemon, Target, World, message, tree};
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
    daemon.on("support_snapshot", |_| {
        json!({
            "event": "support_snapshot_result",
            "protocol_version": "1",
            "daemon_instance_id": "daemon-home",
            "daemon_started_at_unix_ms": 1,
            "captured_at_unix_ms": 2,
            "backend": "worker",
            "warning_codes": [],
            "trace_capacity": 512,
            "trace_total": 0,
            "input_traces": [],
            "runtimes": [],
        })
    });
    daemon
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
        "diagnostics.input",
        "diagnostics.terminal",
        "diagnostics.ui",
    ] {
        assert!(
            has_omission(&report, section, "not_collected_by_native_app"),
            "{section}"
        );
    }
    assert!(report.get("diagnostics").is_none());
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
