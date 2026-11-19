use attn_protocol::{
    DaemonWarning, Desktop, InitialStateMessage, Profile, ProfileArrangementChangedMessage,
};
use attn_testkit::tree::tile;
use attn_testkit::{Daemon, Target, World};
use serde_json::json;

fn warned(warnings: &[(&str, &str)]) -> World {
    World::start(
        Daemon::scripted().state(InitialStateMessage {
            warnings: Some(
                warnings
                    .iter()
                    .map(|(code, message)| DaemonWarning {
                        code: (*code).into(),
                        message: (*message).into(),
                    })
                    .collect(),
            ),
            ..InitialStateMessage::new()
        }),
    )
}

fn banner_text(world: &World) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key("daemon-warnings"))
        .into_iter()
        .filter(|n| {
            n.key.as_deref().is_some_and(|k| {
                k.starts_with("daemon-warnings.") && k != "daemon-warnings.dismiss"
            })
        })
        .filter_map(|n| n.name.clone())
        .collect()
}

#[test]
fn the_daemons_start_warnings_show_until_dismissed_and_the_daemon_is_told_they_were_seen() {
    let mut world = warned(&[(
        "worker_recovery_partial",
        "PTY recovery skipped 2 workers due to transient unavailability.",
    )]);

    assert_eq!(world.daemon.sent_of("clear_warnings").len(), 1);
    assert_eq!(
        world
            .screen()
            .get(Target::key("daemon-warnings.text"))
            .name
            .as_deref(),
        Some("PTY recovery skipped 2 workers due to transient unavailability.")
    );

    world.gesture(|app| app.click(Target::key("daemon-warnings.dismiss")));

    assert!(
        world
            .screen()
            .find(Target::key("daemon-warnings"))
            .is_none()
    );
    assert_eq!(world.daemon.sent_of("clear_warnings").len(), 1);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_warning_raised_after_the_handshake_survives_a_tile_popping_out_and_shows_on_the_next_connection()
 {
    let profile = Profile {
        id: "work".into(),
        name: "Work".into(),
        current_desktop_id: "work/desktop_1".into(),
        ..Profile::default()
    };
    let mut notes = tile("notes", "markdown");
    notes["tile_params"] = json!({"path": "/work/notes.md"}).to_string().into();
    let mut desktop = Desktop {
        id: profile.current_desktop_id.clone(),
        profile_id: profile.id.clone(),
        name: "Desktop 1".into(),
        tree_json: notes.to_string(),
        active_pane_id: "notes".into(),
        revision: 1,
        ..Desktop::default()
    };
    let state = |warning: &str| InitialStateMessage {
        profiles: Some(vec![profile.clone()]),
        selected_profile_id: Some(profile.id.clone()),
        desktops: Some(vec![desktop.clone()]),
        warnings: Some(vec![DaemonWarning {
            code: "worker_recovery_partial".into(),
            message: warning.into(),
        }]),
        ..InitialStateMessage::new()
    };
    let mut world = World::start(Daemon::scripted().state(state("Skipped 2 workers.")));
    assert_eq!(world.daemon.sent_of("clear_warnings").len(), 1);

    world
        .daemon
        .answer_hello_with(state("Recovered 1 session late."));
    notes["window"] = json!({"x": 10, "y": 20, "width": 500, "height": 400});
    desktop.tree_json = notes.to_string();
    desktop.revision += 1;
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile.clone(),
        desktops: vec![desktop.clone()],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();

    let windows = world.tile_windows();
    assert_eq!(windows.len(), 1);
    assert!(
        world
            .tile_screen(&windows[0].key)
            .find(Target::key("daemon-warnings"))
            .is_none()
    );
    assert_eq!(world.daemon.sent_of("clear_warnings").len(), 1);

    world.reconnect();

    assert_eq!(
        world
            .screen()
            .get(Target::key("daemon-warnings.text"))
            .name
            .as_deref(),
        Some("Recovered 1 session late.")
    );
    assert_eq!(world.daemon.sent_of("clear_warnings").len(), 2);
}

#[test]
fn a_handshake_without_warnings_shows_no_banner_and_clears_nothing() {
    let world = World::start(Daemon::scripted());

    assert!(
        world
            .screen()
            .find(Target::key("daemon-warnings"))
            .is_none()
    );
    assert!(world.daemon.sent_of("clear_warnings").is_empty());
}

#[test]
fn shortens_the_links_in_a_github_cli_warning_to_their_host_and_opens_the_full_address() {
    let guide = "https://github.com/cli/cli/blob/trunk/docs/install_linux.md";
    let mut world = warned(&[
        (
            "gh_not_installed",
            &format!("GitHub CLI not installed. PR monitoring disabled. See {guide}"),
        ),
        (
            "gh_version_too_old",
            "See https://cli.github.com, then retry.",
        ),
    ]);

    assert_eq!(
        banner_text(&world),
        [
            "GitHub CLI not installed. PR monitoring disabled. See",
            "Details",
            "github.com/…",
            "See",
            "cli.github.com",
            ", then retry."
        ]
    );

    world.gesture(|app| app.click(Target::role("button").name("github.com/…")));
    world.gesture(|app| app.click(Target::role("button").name("cli.github.com")));

    assert_eq!(world.opened_links(), [guide, "https://cli.github.com"]);
}

const EVERY_START_WARNING: [(&str, &str); 7] = [
    (
        "pty_backend_fallback",
        "Shared PTY host is unavailable (dial unix /Users/sam/.attn/pty-hosts/daemon-1/host.sock: connect: no such file or directory). New terminals will keep using dedicated workers.",
    ),
    (
        "worker_recovery_partial",
        "PTY recovery skipped 3 workers due to transient unavailability.",
    ),
    (
        "worker_recovery_partial",
        "Deferred marking 4 tracked sessions idle because PTY recovery was incomplete.",
    ),
    (
        "stale_session_missing_worker",
        "2 sessions can be recovered from a previous daemon run.",
    ),
    (
        "stale_sessions_pruned",
        "Removed 5 non-recoverable sessions from a previous daemon run.",
    ),
    (
        "stale_session_missing_worker",
        "6 tracked sessions were expected to be running but no worker was recovered; they were marked idle.",
    ),
    (
        "worker_recovery_partial",
        "Recovered workers were missing metadata for 1 sessions.",
    ),
];

const SMALLEST_WORK_AREA: f64 = 368.0;

#[test]
fn every_start_warning_at_once_leaves_the_smallest_window_usable_and_details_reads_them_whole() {
    let mut world = World::start_sized(
        Daemon::scripted().state(InitialStateMessage {
            warnings: Some(
                EVERY_START_WARNING
                    .iter()
                    .map(|(code, message)| DaemonWarning {
                        code: (*code).into(),
                        message: (*message).into(),
                    })
                    .collect(),
            ),
            settings: Some([("uiScale".to_owned(), "1.5".to_owned())].into()),
            ..InitialStateMessage::new()
        }),
        (800.0, 600.0),
    );
    let whole = EVERY_START_WARNING
        .iter()
        .map(|(_, message)| *message)
        .collect::<Vec<_>>()
        .join(" ");
    assert_eq!(
        world
            .screen()
            .get(Target::key("daemon-warnings.text"))
            .name
            .as_deref(),
        Some(whole.as_str())
    );
    let closed = world.rect("regions")[3];
    assert!(
        closed >= SMALLEST_WORK_AREA * 2.0 / 3.0,
        "the warnings leave the work area {closed} of {SMALLEST_WORK_AREA}"
    );
    let [bx, by, bw, bh] = world.rect("daemon-warnings");
    for key in ["daemon-warnings.details", "daemon-warnings.dismiss"] {
        let [x, y, w, h] = world.rect(key);
        assert!(
            x >= bx && y >= by && x + w <= bx + bw && y + h <= by + bh,
            "{key} at {:?} is outside the band at {:?}",
            [x, y, w, h],
            [bx, by, bw, bh]
        );
    }

    world.gesture(|app| app.click(Target::key("daemon-warnings.details")));
    assert_eq!(
        world
            .screen()
            .get(Target::key("whole.daemon-warnings.text"))
            .name
            .as_deref(),
        Some(whole.as_str())
    );
    assert_eq!(
        world.rect("daemon-warnings")[3],
        bh,
        "Details changed the bar"
    );
    world.gesture(|app| app.press("escape"));
    assert!(
        world
            .screen()
            .find(Target::key("daemon-warnings.whole-dialog"))
            .is_none()
    );

    world.gesture(|app| app.click(Target::key("daemon-warnings.dismiss")));
    assert!(
        world
            .screen()
            .find(Target::key("daemon-warnings"))
            .is_none()
    );
}
