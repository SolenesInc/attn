use attn_protocol::{DaemonWarning, InitialStateMessage};
use attn_testkit::{Daemon, Target, World};

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
