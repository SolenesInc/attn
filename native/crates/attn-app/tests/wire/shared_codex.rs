use std::path::Path;
use std::time::Duration;

use attn_app::Action;
use attn_app::whats_new::{RELEASE, SEEN_PREF};
use attn_protocol::{
    Desktop, GetCommandUsageResultMessage, InitialStateMessage, InstallCodexUpdateResultMessage,
    Profile, ProfileActionResultMessage, ProfileArrangementChangedMessage,
    RestartSharedCodexResultMessage, Session, SharedCodexBlocker, SharedCodexChangedMessage,
    SharedCodexState,
};
use attn_testkit::{Daemon, Target, World, message};
use rune::prefs::Prefs;
use rune::{Key, KeyEvent, Mods};
use serde_json::Value;

const BAND: &str = "codex-updates";
const CONFIRM: &str = "shared-codex-confirm";
const FAILURE: &str = "approval_policy = \"untrusted\" is no longer supported";

fn codex() -> SharedCodexState {
    SharedCodexState {
        running_version: Some("0.162.1".into()),
        installed_version: Some("0.162.1".into()),
        latest_version: Some("0.162.1".into()),
        restart: "none".into(),
        ..SharedCodexState::default()
    }
}

fn installed(version: &str, restart: &str) -> SharedCodexState {
    SharedCodexState {
        installed_version: Some(version.into()),
        restart: restart.into(),
        ..codex()
    }
}

fn latest(version: &str) -> SharedCodexState {
    SharedCodexState {
        latest_version: Some(version.into()),
        ..codex()
    }
}

fn running(version: &str, stale: &[&str]) -> SharedCodexState {
    SharedCodexState {
        running_version: Some(version.into()),
        installed_version: Some(version.into()),
        latest_version: Some(version.into()),
        stale_sessions: Some(stale.iter().map(|s| (*s).to_owned()).collect()),
        ..codex()
    }
}

fn blocked(state: SharedCodexState, blockers: &[(&str, &str)]) -> SharedCodexState {
    SharedCodexState {
        blockers: Some(
            blockers
                .iter()
                .map(|(session, reason)| SharedCodexBlocker {
                    session_id: (*session).into(),
                    reason: (*reason).into(),
                })
                .collect(),
        ),
        ..state
    }
}

fn every_blocker() -> SharedCodexState {
    blocked(
        installed("0.163.0", "pending"),
        &[
            ("fix-auth", "turn"),
            ("docs-sweep", "approval"),
            ("nightly-triage", "question"),
            ("plan-cache", "starting"),
        ],
    )
}

fn session(id: &str, hidden: bool) -> Session {
    Session {
        id: id.into(),
        label: id.into(),
        agent: "codex".into(),
        state: "idle".into(),
        profile_id: "work".into(),
        hidden: hidden.then_some(true),
        ..Session::default()
    }
}

fn profile(id: &str, name: &str, desktop: &str) -> Profile {
    Profile {
        id: id.into(),
        name: name.into(),
        current_desktop_id: desktop.into(),
        ..Profile::default()
    }
}

fn desktop(id: &str, profile: &str) -> Desktop {
    Desktop {
        id: id.into(),
        order_key: id.into(),
        profile_id: profile.into(),
        ..Desktop::default()
    }
}

fn initial(shared_codex: Option<SharedCodexState>) -> InitialStateMessage {
    InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![
            profile("work", "Work", "d1"),
            profile("side", "Side", "s1"),
        ]),
        desktops: Some(vec![desktop("d1", "work"), desktop("s1", "side")]),
        sessions: Some(vec![
            session("fix-auth", false),
            session("docs-sweep", false),
            session("plan-cache", false),
            session("nightly-triage", true),
        ]),
        shared_codex,
        ..InitialStateMessage::new()
    }
}

fn scripted(shared_codex: Option<SharedCodexState>) -> Daemon {
    let daemon = Daemon::scripted().state(initial(shared_codex));
    daemon.on("restart_shared_codex", |r| {
        message(RestartSharedCodexResultMessage {
            request_id: r["request_id"].as_str().map(str::to_owned),
            profile_id: r["profile_id"].as_str().unwrap().into(),
            success: true,
            ..RestartSharedCodexResultMessage::new()
        })
    });
    daemon.on("reload_session", |_| ());
    daemon.on("get_command_usage", |r| {
        message(GetCommandUsageResultMessage {
            request_id: r["request_id"].as_str().unwrap_or_default().into(),
            profile_id: r["profile_id"].as_str().unwrap_or_default().into(),
            success: true,
            ..GetCommandUsageResultMessage::new()
        })
    });
    daemon.on("install_codex_update", |_| ());
    daemon
}

/// The app launched on `prefs`, the file a later launch reads again, past this release's
/// tour.
fn launch(daemon: Daemon, prefs: &Path) -> World {
    let mut opened = Prefs::at(prefs);
    opened.set(SEEN_PREF, &RELEASE.to_owned()).unwrap();
    World::start_with_prefs(daemon, opened)
}

fn start(shared_codex: Option<SharedCodexState>) -> (World, tempfile::TempDir) {
    let dir = tempfile::tempdir().unwrap();
    let world = launch(scripted(shared_codex), &dir.path().join("prefs.json"));
    (world, dir)
}

fn change(world: &mut World, profile: &str, shared_codex: Option<SharedCodexState>) {
    world.daemon.emit(SharedCodexChangedMessage {
        profile_id: profile.into(),
        shared_codex,
        ..SharedCodexChangedMessage::new()
    });
    world.settle();
}

fn install_result(error: Option<&str>, shared_codex: SharedCodexState) -> Value {
    message(InstallCodexUpdateResultMessage {
        success: error.is_none(),
        error: error.map(str::to_owned),
        profile_id: "work".into(),
        shared_codex: Some(shared_codex),
        ..InstallCodexUpdateResultMessage::new()
    })
}

/// What the band reads, buttons named in brackets, in order; `None` while it is hidden.
fn banner(world: &World) -> Option<String> {
    world.screen().find(Target::key(BAND))?;
    let said: Vec<String> = world
        .screen()
        .children(Target::key(BAND))
        .into_iter()
        .filter_map(|n| match n.role.as_str() {
            "button" => n.name.map(|name| format!("[{name}]")),
            _ => n.name,
        })
        .collect();
    Some(said.join(" "))
}

fn click(world: &mut World, key: &str) {
    world.gesture(|app| app.click(Target::key(key)));
}

fn click_button(world: &mut World, name: &str) {
    let key = world
        .screen()
        .descendants(Target::key(BAND))
        .into_iter()
        .find(|n| n.role == "button" && n.name.as_deref() == Some(name))
        .and_then(|n| n.key)
        .unwrap_or_else(|| panic!("no {name} in the band:\n{}", world.screen()));
    click(world, &key);
}

fn press(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

fn dialog(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key(format!("{CONFIRM}-dialog")))?
        .name
}

fn listed(world: &World) -> Vec<String> {
    world
        .screen()
        .children(Target::key(format!("{CONFIRM}.list")))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn sent(world: &World, cmd: &str) -> Vec<Value> {
    world.daemon.sent_of(cmd)
}

/// Switches to Side from the palette, answered as the daemon does: Side's arrangement, then
/// the result.
fn switch_to_side(world: &mut World) {
    world.daemon.on("profile_select", |_| {
        let side = profile("side", "Side", "s1");
        vec![
            message(ProfileArrangementChangedMessage {
                profile: side.clone(),
                desktops: vec![desktop("s1", "side")],
                ..ProfileArrangementChangedMessage::new()
            }),
            message(ProfileActionResultMessage {
                action: "profile_select".into(),
                success: true,
                profile: Some(side),
                ..ProfileActionResultMessage::new()
            }),
        ]
    });
    run_command(world, "Switch to Side");
}

fn restarts(world: &World) -> Vec<bool> {
    sent(world, "restart_shared_codex")
        .iter()
        .map(|r| r["now"].as_bool().unwrap())
        .collect()
}

fn reloads(world: &World) -> Vec<String> {
    sent(world, "reload_session")
        .iter()
        .map(|r| r["id"].as_str().unwrap().to_owned())
        .collect()
}

fn palette(world: &mut World, query: &str) {
    let chord = super::keys::chord(&Action::UiCommandPalette);
    world.gesture(|app| app.key(chord));
    world.gesture(|app| app.type_text(query));
}

fn titles(world: &mut World, query: &str) -> Vec<String> {
    palette(world, query);
    let titles = world
        .screen()
        .all(Target::role("list-box-option"))
        .into_iter()
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with("palette.row."))
        })
        .filter_map(|n| n.name)
        .collect();
    press(world, Key::Escape);
    titles
}

fn run_command(world: &mut World, title: &str) {
    palette(world, title);
    press(world, Key::Enter);
}

#[test]
fn the_band_stays_hidden_without_shared_codex_state_or_while_everything_runs_the_installed_codex() {
    let (mut world, _prefs) = start(None);
    assert_eq!(banner(&world), None);
    change(&mut world, "work", Some(codex()));
    assert_eq!(banner(&world), None);
    change(
        &mut world,
        "work",
        Some(SharedCodexState {
            latest_version: Some("0.164.0".into()),
            ..running("0.163.0", &["fix-auth", "docs-sweep"])
        }),
    );
    assert!(banner(&world).is_some());
    change(&mut world, "work", None);
    assert_eq!(banner(&world), None);
}

#[test]
fn each_stage_shows_over_every_lower_one() {
    let lower = |state: SharedCodexState| SharedCodexState {
        latest_version: Some("0.164.0".into()),
        stale_sessions: Some(vec!["fix-auth".into(), "docs-sweep".into()]),
        ..state
    };
    let cases = [
        (
            lower(SharedCodexState {
                error: Some(format!("{FAILURE}.")),
                ..installed("0.163.0", "failed")
            }),
            format!(
                "Codex 0.163.0 didn’t start: {FAILURE}. Still on 0.162.1, nothing was interrupted. [Retry]"
            ),
        ),
        (
            lower(blocked(
                SharedCodexState {
                    force_at: Some("2026-11-12T09:00:42Z".into()),
                    ..installed("0.163.0", "restarting")
                },
                &[("fix-auth", "turn")],
            )),
            "Restarting Codex now: waiting for fix-auth to finish. Force stop in 0:42.".into(),
        ),
        (
            lower(installed("0.163.0", "restarting")),
            "Restarting Codex on 0.163.0. Tiles reconnect on their own.".into(),
        ),
        (
            lower(installed("0.163.0", "pending")),
            "Codex 0.163.0 installed. Restarts when agents are idle.".into(),
        ),
        (
            SharedCodexState {
                stale_sessions: Some(vec!["fix-auth".into()]),
                ..latest("0.163.0")
            },
            "Codex 0.163.0 is available. [Install] [Dismiss Codex update]".into(),
        ),
        (
            running("0.163.0", &["fix-auth", "docs-sweep"]),
            "Codex 0.163.0 is running. 2 tiles still run an older Codex. [Reload tiles] [Dismiss old Codex tiles notice]".into(),
        ),
    ];
    for (state, said) in cases {
        let (world, _prefs) = start(Some(state));
        assert_eq!(banner(&world), Some(said));
    }
}

#[test]
fn only_the_available_and_old_tiles_stages_can_be_closed() {
    let (mut world, _prefs) = start(None);
    for state in [
        SharedCodexState {
            error: Some(FAILURE.into()),
            ..installed("0.163.0", "failed")
        },
        SharedCodexState {
            force_at: Some("2026-11-12T09:00:42Z".into()),
            ..installed("0.163.0", "restarting")
        },
        installed("0.163.0", "restarting"),
        blocked(installed("0.163.0", "pending"), &[("fix-auth", "turn")]),
    ] {
        change(&mut world, "work", Some(state));
        let said = banner(&world).unwrap();
        assert!(!said.contains("Dismiss"), "{said}");
    }
}

#[test]
fn the_band_says_why_each_blocker_holds_the_restart_opens_its_tile_and_relaunches_one_still_starting()
 {
    let (mut world, _prefs) = start(Some(every_blocker()));
    assert_eq!(
        banner(&world).unwrap(),
        "Codex 0.163.0 installed. Restarts when agents are idle.  Waiting on \
         [Go to fix-auth] (working) , \
         [Go to docs-sweep] (waiting on an approval) , \
         [Go to nightly-triage] (asking a question) , \
         [Go to plan-cache] (still starting or asking whether to trust its folder) [Relaunch plan-cache] . \
         [Restart now]"
    );
    click_button(&mut world, "Go to docs-sweep");
    let shown = sent(&world, "desktop_show_session");
    assert_eq!(shown.last().unwrap()["session_id"], "docs-sweep");

    click_button(&mut world, "Relaunch plan-cache");
    assert_eq!(reloads(&world), ["plan-cache"]);
    assert_eq!(restarts(&world), Vec::<bool>::new());
}

#[test]
fn restart_now_asks_first_listing_what_each_blocker_loses() {
    let (mut world, _prefs) = start(Some(every_blocker()));
    click_button(&mut world, "Restart now");
    assert_eq!(dialog(&world).as_deref(), Some("Restart shared Codex now?"));
    let description = world
        .screen()
        .descendants(Target::key(format!("{CONFIRM}-dialog")))
        .into_iter()
        .filter_map(|n| n.name)
        .any(|n| {
            n == "Every Codex tile in this profile goes offline until 0.163.0 is up. What you typed stays."
        });
    assert!(description, "{}", world.screen());
    assert_eq!(
        listed(&world),
        [
            "fix-auth · Turn running. Gets up to 60s to finish, then it is stopped.",
            "docs-sweep · Waiting for your approval. The request is lost.",
            "nightly-triage (hidden) · Asking you a question. The question is lost.",
            "plan-cache · Still starting. Its tile relaunches.",
        ]
    );
    assert_eq!(
        world.screen().focused().and_then(|n| n.key).as_deref(),
        Some("shared-codex-confirm.cancel")
    );

    press(&mut world, Key::Escape);
    assert_eq!(dialog(&world), None);
    click_button(&mut world, "Restart now");
    click(&mut world, "shared-codex-confirm.cancel");
    assert_eq!(dialog(&world), None);
    assert_eq!(restarts(&world), Vec::<bool>::new());

    click_button(&mut world, "Restart now");
    click(&mut world, "shared-codex-confirm.confirm");
    assert_eq!(dialog(&world), None);
    let sent = sent(&world, "restart_shared_codex");
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["profile_id"], "work");
    assert_eq!(sent[0]["now"], true);
}

#[test]
fn the_restart_confirm_closes_once_the_restart_no_longer_waits_on_anyone() {
    let (mut world, _prefs) = start(Some(blocked(
        installed("0.163.0", "pending"),
        &[("fix-auth", "turn")],
    )));
    click_button(&mut world, "Restart now");
    assert!(dialog(&world).is_some());
    change(&mut world, "work", Some(installed("0.163.0", "restarting")));
    assert_eq!(dialog(&world), None);
    assert_eq!(restarts(&world), Vec::<bool>::new());
}

#[test]
fn retry_restarts_a_failed_codex_once_agents_are_idle() {
    let (mut world, _prefs) = start(Some(SharedCodexState {
        error: Some(FAILURE.into()),
        ..installed("0.163.0", "failed")
    }));
    click_button(&mut world, "Retry");
    let sent = sent(&world, "restart_shared_codex");
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0]["profile_id"], "work");
    assert_eq!(sent[0]["now"], false);
}

#[test]
fn the_force_stop_counts_down_while_it_waits_and_the_window_sleeps_once_the_restart_moves_on() {
    let (mut world, _prefs) = start(Some(blocked(
        SharedCodexState {
            force_at: Some("2026-11-12T09:01:05Z".into()),
            ..installed("0.163.0", "restarting")
        },
        &[("fix-auth", "turn"), ("docs-sweep", "approval")],
    )));
    assert_eq!(
        banner(&world).unwrap(),
        "Restarting Codex now: waiting for fix-auth, docs-sweep to finish. Force stop in 1:05."
    );
    world.advance(Duration::from_secs(1));
    assert!(banner(&world).unwrap().ends_with("Force stop in 1:04."));
    world.advance(Duration::from_secs(64));
    assert!(banner(&world).unwrap().ends_with("Force stop in 0:00."));
    assert!(world.runtime_is_at_rest());

    change(&mut world, "work", Some(installed("0.163.0", "restarting")));
    assert_eq!(
        banner(&world).unwrap(),
        "Restarting Codex on 0.163.0. Tiles reconnect on their own."
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn install_runs_in_a_tile_on_the_current_desktop_and_a_failure_shows_where_the_update_was_offered()
{
    let (mut world, _prefs) = start(Some(latest("0.163.0")));
    click_button(&mut world, "Install");
    let first = world.daemon.sent_of("install_codex_update").remove(0);
    assert_eq!(first["desktop_id"], "d1");
    assert_eq!(
        banner(&world).unwrap(),
        "Installing Codex 0.163.0 in a terminal tile…"
    );
    world.advance(Duration::from_secs(5 * 60));
    assert_eq!(
        banner(&world).unwrap(),
        "Installing Codex 0.163.0 in a terminal tile…"
    );

    world.daemon.reply_to(
        &first,
        install_result(Some("codex update exited 1"), latest("0.163.0")),
    );
    world.settle();
    assert_eq!(
        banner(&world).unwrap(),
        "Codex 0.163.0 is available. Install failed: codex update exited 1. [Install] [Dismiss Codex update]"
    );

    click_button(&mut world, "Install");
    assert_eq!(
        banner(&world).unwrap(),
        "Installing Codex 0.163.0 in a terminal tile…"
    );
    let second = world.daemon.sent_of("install_codex_update").remove(1);
    let done = SharedCodexState {
        latest_version: Some("0.163.0".into()),
        ..installed("0.163.0", "pending")
    };
    world.daemon.reply_to(&second, install_result(None, done));
    world.settle();
    assert_eq!(
        banner(&world).unwrap(),
        "Codex 0.163.0 installed. Restarts when agents are idle."
    );
}

#[test]
fn a_restart_shows_over_an_outstanding_install_which_lasts_until_its_result() {
    let (mut world, _prefs) = start(Some(latest("0.163.0")));
    click_button(&mut world, "Install");
    let done = SharedCodexState {
        latest_version: Some("0.163.0".into()),
        ..installed("0.163.0", "pending")
    };
    change(&mut world, "work", Some(done));
    assert_eq!(
        banner(&world).unwrap(),
        "Codex 0.163.0 installed. Restarts when agents are idle."
    );
    change(&mut world, "work", Some(running("0.163.0", &[])));
    assert_eq!(
        banner(&world).unwrap(),
        "Installing Codex 0.163.0 in a terminal tile…"
    );
}

#[test]
fn a_disconnect_forgets_an_outstanding_install() {
    let (mut world, _prefs) = start(Some(latest("0.163.0")));
    click_button(&mut world, "Install");
    world.reconnect();
    assert_eq!(
        banner(&world).unwrap(),
        "Codex 0.163.0 is available. [Install] [Dismiss Codex update]"
    );
}

#[test]
fn a_closed_update_stays_closed_for_that_version_across_launches_until_a_newer_one() {
    let dir = tempfile::tempdir().unwrap();
    let prefs = dir.path().join("prefs.json");
    let mut world = launch(scripted(Some(latest("0.163.0"))), &prefs);
    click_button(&mut world, "Dismiss Codex update");
    assert_eq!(banner(&world), None);
    drop(world);

    let mut world = launch(scripted(Some(latest("0.163.0"))), &prefs);
    assert_eq!(banner(&world), None);
    assert_eq!(
        titles(&mut world, "Install Codex update"),
        ["Install Codex update"]
    );
    change(&mut world, "work", Some(latest("0.164.0")));
    assert_eq!(
        banner(&world).unwrap(),
        "Codex 0.164.0 is available. [Install] [Dismiss Codex update]"
    );
}

#[test]
fn a_closed_update_comes_back_to_say_why_an_install_from_the_palette_failed() {
    let (mut world, _prefs) = start(Some(latest("0.163.0")));
    world.daemon.on("install_codex_update", |_| {
        install_result(Some("codex update exited 1"), latest("0.163.0"))
    });
    click_button(&mut world, "Dismiss Codex update");
    run_command(&mut world, "Install Codex update");
    assert_eq!(
        banner(&world).unwrap(),
        "Codex 0.163.0 is available. Install failed: codex update exited 1. [Install] [Dismiss Codex update]"
    );
    click_button(&mut world, "Dismiss Codex update");
    assert_eq!(banner(&world), None);
}

#[test]
fn old_tiles_stay_closed_until_more_tiles_fall_behind_or_the_running_codex_changes() {
    let (mut world, _prefs) = start(Some(running("0.163.0", &["docs-sweep", "fix-auth"])));
    click_button(&mut world, "Dismiss old Codex tiles notice");
    assert_eq!(banner(&world), None);

    change(&mut world, "work", Some(running("0.163.0", &["fix-auth"])));
    assert_eq!(banner(&world), None);
    change(
        &mut world,
        "work",
        Some(running(
            "0.163.0",
            &["docs-sweep", "fix-auth", "plan-cache"],
        )),
    );
    assert!(
        banner(&world)
            .unwrap()
            .contains("3 tiles still run an older Codex.")
    );

    click_button(&mut world, "Dismiss old Codex tiles notice");
    change(&mut world, "work", Some(running("0.164.0", &["fix-auth"])));
    assert!(
        banner(&world)
            .unwrap()
            .starts_with("Codex 0.164.0 is running. 1 tile still runs an older Codex.")
    );
}

#[test]
fn reload_tiles_reloads_each_old_tile_after_the_confirm() {
    let (mut world, _prefs) = start(Some(running("0.163.0", &["docs-sweep", "fix-auth"])));
    click_button(&mut world, "Reload tiles");
    assert_eq!(dialog(&world).as_deref(), Some("Reload 2 Codex tiles?"));
    let description = world
        .screen()
        .descendants(Target::key(format!("{CONFIRM}-dialog")))
        .into_iter()
        .filter_map(|n| n.name)
        .any(|n| {
            n == "Each tile restarts its Codex on 0.163.0 and resumes its conversation. Text you typed and haven’t sent in them is lost."
        });
    assert!(description, "{}", world.screen());
    assert_eq!(listed(&world), ["docs-sweep", "fix-auth"]);
    click(&mut world, "shared-codex-confirm.cancel");
    assert_eq!(reloads(&world), Vec::<String>::new());

    click_button(&mut world, "Reload tiles");
    click(&mut world, "shared-codex-confirm.confirm");
    assert_eq!(dialog(&world), None);
    assert_eq!(reloads(&world), ["docs-sweep", "fix-auth"]);
}

#[test]
fn the_palette_offers_each_action_only_while_it_applies() {
    let (mut world, _prefs) = start(Some(codex()));
    assert_eq!(titles(&mut world, "codex update"), Vec::<String>::new());

    change(&mut world, "work", Some(latest("0.163.0")));
    assert_eq!(titles(&mut world, "codex update"), ["Install Codex update"]);
    run_command(&mut world, "Install Codex update");
    let install = world.daemon.sent_of("install_codex_update").remove(0);
    assert_eq!(install["desktop_id"], "d1");
    assert_eq!(titles(&mut world, "codex update"), Vec::<String>::new());
    let done = SharedCodexState {
        latest_version: Some("0.163.0".into()),
        ..installed("0.163.0", "pending")
    };
    world
        .daemon
        .reply_to(&install, install_result(None, done.clone()));
    world.settle();

    change(
        &mut world,
        "work",
        Some(blocked(done, &[("fix-auth", "turn")])),
    );
    assert_eq!(
        titles(&mut world, "codex update"),
        ["Restart shared Codex now"]
    );
    run_command(&mut world, "Restart shared Codex now");
    click(&mut world, "shared-codex-confirm.confirm");
    assert_eq!(restarts(&world), [true]);

    change(&mut world, "work", Some(running("0.163.0", &["fix-auth"])));
    click_button(&mut world, "Dismiss old Codex tiles notice");
    assert_eq!(
        titles(&mut world, "codex update"),
        ["Reload old Codex tiles"]
    );
    run_command(&mut world, "Reload old Codex tiles");
    assert_eq!(dialog(&world).as_deref(), Some("Reload 1 Codex tile?"));
    let action = world
        .screen()
        .find(Target::key("shared-codex-confirm.confirm"))
        .and_then(|n| n.name);
    assert_eq!(action.as_deref(), Some("Reload tile"));
    click(&mut world, "shared-codex-confirm.confirm");
    assert_eq!(reloads(&world), ["fix-auth"]);
}

#[test]
fn the_band_shows_only_the_profile_on_screen() {
    let (mut world, _prefs) = start(Some(latest("0.163.0")));
    assert!(banner(&world).is_some());
    switch_to_side(&mut world);
    assert_eq!(
        sent(&world, "profile_select").last().unwrap()["profile_id"],
        "side"
    );
    assert_eq!(banner(&world), None);

    let done = SharedCodexState {
        latest_version: Some("0.163.0".into()),
        ..installed("0.163.0", "pending")
    };
    change(&mut world, "side", Some(done));
    assert_eq!(
        banner(&world).unwrap(),
        "Codex 0.163.0 installed. Restarts when agents are idle."
    );
}

#[test]
fn an_install_from_another_profile_finishing_keeps_the_profile_on_screen() {
    let (mut world, _prefs) = start(Some(latest("0.163.0")));
    click_button(&mut world, "Install");
    let install = world.daemon.sent_of("install_codex_update").remove(0);
    switch_to_side(&mut world);
    change(&mut world, "side", Some(latest("0.164.0")));

    world.daemon.reply_to(
        &install,
        install_result(Some("codex update exited 1"), latest("0.163.0")),
    );
    world.settle();
    assert_eq!(
        banner(&world).unwrap(),
        "Codex 0.164.0 is available. Install failed: codex update exited 1. [Install] [Dismiss Codex update]"
    );
}

#[test]
fn the_palette_retries_a_restart_while_the_installed_codex_refuses_to_start() {
    let (mut world, _prefs) = start(Some(SharedCodexState {
        latest_version: Some("0.163.0".into()),
        error: Some(FAILURE.into()),
        ..installed("0.163.0", "failed")
    }));
    run_command(&mut world, "Retry shared Codex restart");
    assert_eq!(restarts(&world), [false]);
}
