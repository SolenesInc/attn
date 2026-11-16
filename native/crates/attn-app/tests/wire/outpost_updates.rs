use std::path::Path;
use std::time::Duration;

use attn_app::whats_new::{RELEASE, SEEN_PREF};
use attn_protocol::{
    InitialStateMessage, OutpostInfo, OutpostUpdate, OutpostsUpdatedMessage, Profile,
    SettingsUpdatedMessage,
};
use attn_testkit::{Daemon, Target, World};
use rune::prefs::Prefs;
use serde_json::{Value, json};

const BANNER: &str = "outpost-updates";
const HOME_BUILD: &str = "0.13.0 (source git:home, protocol current)";
const NEXT_BUILD: &str = "0.13.0 (source git:next, protocol current)";

/// Studio, on an older build, refusing the home's pinned reconnect.
fn old() -> OutpostInfo {
    OutpostInfo {
        id: "studio".into(),
        name: "Studio".into(),
        destination: "studio:7777".into(),
        state: "refused".into(),
        reach: "unreachable".into(),
        state_since: "2026-10-26T12:00:00Z".into(),
        build: Some("0.13.0 (source git:old, protocol old)".into()),
        ..OutpostInfo::default()
    }
}

fn at(phase: &str, row: OutpostInfo) -> OutpostInfo {
    OutpostInfo {
        update: Some(OutpostUpdate {
            phase: phase.into(),
            outcome: None,
        }),
        ..row
    }
}

/// Studio after an update to the home's build that ended well.
fn done() -> OutpostInfo {
    at(
        "done",
        OutpostInfo {
            build: Some(HOME_BUILD.into()),
            state: "connected".into(),
            reach: "reachable".into(),
            ..old()
        },
    )
}

fn named(row: OutpostInfo, id: &str, name: &str) -> OutpostInfo {
    OutpostInfo {
        id: id.into(),
        name: name.into(),
        ..row
    }
}

fn initial(outposts: Vec<OutpostInfo>, home_build: &str) -> InitialStateMessage {
    InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        settings: Some([("outposts_enabled".into(), "true".into())].into()),
        outposts: Some(outposts),
        home_build: Some(home_build.into()),
        ..InitialStateMessage::new()
    }
}

fn home(outposts: Vec<OutpostInfo>) -> Daemon {
    Daemon::scripted().state(initial(outposts, HOME_BUILD))
}

/// The app launched on `prefs`, the file a later launch reads again, past this release's
/// tour.
fn launch(daemon: Daemon, prefs: &Path) -> World {
    let mut opened = Prefs::at(prefs);
    opened.set(SEEN_PREF, &RELEASE.to_owned()).unwrap();
    World::start_with_prefs(daemon, opened)
}

fn start(outposts: Vec<OutpostInfo>) -> (World, tempfile::TempDir) {
    let dir = tempfile::tempdir().unwrap();
    let daemon = home(outposts);
    daemon.on("update_outpost", |_| ());
    (launch(daemon, &dir.path().join("prefs.json")), dir)
}

fn list(world: &mut World, outposts: Vec<OutpostInfo>) {
    world.daemon.emit(OutpostsUpdatedMessage {
        outposts,
        ..OutpostsUpdatedMessage::new()
    });
    world.settle();
}

/// What the band reads, its parts in order; `None` while it is hidden.
fn banner(world: &World) -> Option<String> {
    world.screen().find(Target::key(BANNER))?;
    let said: Vec<String> = world
        .screen()
        .descendants(Target::key(BANNER))
        .into_iter()
        .filter(|n| n.role != "button")
        .filter_map(|n| n.name)
        .collect();
    Some(said.join(" "))
}

fn button(world: &World) -> Option<(String, bool)> {
    let node = world.screen().find(Target::key("outpost-updates.update"))?;
    Some((node.name.clone().unwrap_or_default(), node.is("disabled")))
}

fn click(world: &mut World, key: &str) {
    world.gesture(|app| app.click(Target::key(key)));
}

fn updates_sent(world: &World) -> Vec<Value> {
    world
        .daemon
        .sent_of("update_outpost")
        .iter()
        .map(|r| r["id"].clone())
        .collect()
}

fn hover(world: &mut World, inside: bool) {
    let at = if inside {
        let [x, y, w, h] = world.screen().bounds(Target::key(BANNER)).unwrap();
        (x + w / 2.0, y + h / 2.0)
    } else {
        (600.0, 700.0)
    };
    world.gesture(|app| app.move_to(at));
}

#[test]
fn an_outpost_on_the_home_build_without_the_shared_host_is_offered_it_until_installed() {
    let lacking = OutpostInfo {
        build: Some(HOME_BUILD.into()),
        state: "connected".into(),
        reach: "reachable".into(),
        host_missing: Some(true),
        ..old()
    };
    let (mut world, _prefs) = start(vec![lacking.clone()]);
    assert_eq!(
        banner(&world).as_deref(),
        Some("Studio is missing the shared terminal host.")
    );

    click(&mut world, "outpost-updates.update");
    assert_eq!(updates_sent(&world), [json!("studio")]);

    list(
        &mut world,
        vec![at(
            "done",
            OutpostInfo {
                host_missing: None,
                ..lacking
            },
        )],
    );
    assert!(banner(&world).unwrap().starts_with("Updated Studio."));
    assert_eq!(button(&world), None);
}

#[test]
fn update_outposts_updates_only_the_differing_ones_once_and_follows_each_step() {
    let current = OutpostInfo {
        build: Some(HOME_BUILD.into()),
        ..named(old(), "current", "Current")
    };
    let (mut world, _prefs) = start(vec![old(), current]);
    assert_eq!(
        banner(&world).as_deref(),
        Some("Studio runs a different build than home.")
    );

    click(&mut world, "outpost-updates.update");
    assert_eq!(updates_sent(&world), [json!("studio")]);
    for (phase, said) in [
        (
            "building",
            "Building attn for Linux. The first build takes about a minute.",
        ),
        ("sending", "Copying it over."),
        ("verifying", "Checking it runs."),
        ("restarting", "Restarting."),
    ] {
        list(&mut world, vec![at(phase, old())]);
        assert_eq!(
            banner(&world),
            Some(format!("Updating outposts. Studio: {said}"))
        );
        assert_eq!(button(&world), Some(("Updating…".into(), true)));
    }

    list(&mut world, vec![done()]);
    assert_eq!(
        banner(&world).as_deref(),
        Some("Updated Studio. Closing in 5s")
    );
    assert_eq!(button(&world), None);
    assert_eq!(updates_sent(&world).len(), 1);
}

#[test]
fn the_first_press_shows_the_update_started_and_further_presses_send_nothing() {
    let dir = tempfile::tempdir().unwrap();
    let daemon = home(vec![old()]);
    daemon.on("update_outpost", |_| {
        serde_json::to_value(OutpostsUpdatedMessage {
            outposts: vec![at("connecting", old())],
            ..OutpostsUpdatedMessage::new()
        })
        .unwrap()
    });
    let mut world = launch(daemon, &dir.path().join("prefs.json"));

    click(&mut world, "outpost-updates.update");
    assert_eq!(
        banner(&world).as_deref(),
        Some("Updating outposts. Studio: Connecting.")
    );
    assert_eq!(button(&world), Some(("Updating…".into(), true)));
    click(&mut world, "outpost-updates.update");
    click(&mut world, "outpost-updates.update");
    assert_eq!(updates_sent(&world).len(), 1);
}

#[test]
fn a_refused_update_says_why_in_the_band_and_can_be_tried_again() {
    let dir = tempfile::tempdir().unwrap();
    let daemon = home(vec![old()]);
    daemon.on("update_outpost", |_| {
        json!({
            "event": "command_error",
            "cmd": "update_outpost",
            "success": false,
            "error": "outpost studio not found",
        })
    });
    let mut world = launch(daemon, &dir.path().join("prefs.json"));

    click(&mut world, "outpost-updates.update");
    assert_eq!(
        banner(&world).as_deref(),
        Some("Studio runs a different build than home. outpost studio not found")
    );
    assert_eq!(button(&world), Some(("Update outposts".into(), false)));
}

#[test]
fn a_dismissal_lasts_through_relaunches_until_the_home_build_changes() {
    let dir = tempfile::tempdir().unwrap();
    let prefs = dir.path().join("prefs.json");
    let mut world = launch(home(vec![old()]), &prefs);
    click(&mut world, "outpost-updates.dismiss");
    assert_eq!(banner(&world), None);
    drop(world);

    let world = launch(home(vec![old()]), &prefs);
    assert_eq!(banner(&world), None, "a relaunch keeps the band dismissed");
    world.daemon.emit(initial(vec![old()], NEXT_BUILD));
    let mut world = world;
    world.settle();
    assert_eq!(
        banner(&world).as_deref(),
        Some("Studio runs a different build than home.")
    );
}

#[test]
fn a_first_install_in_progress_says_so_until_it_ends_well() {
    let installing = at(
        "building",
        OutpostInfo {
            build: None,
            state: "connecting".into(),
            ..old()
        },
    );
    let (mut world, _prefs) = start(vec![installing]);
    assert_eq!(
        banner(&world).as_deref(),
        Some(
            "Updating outposts. Studio: Building attn for Linux. The first build takes about a minute."
        )
    );
    list(&mut world, vec![done()]);
    assert!(banner(&world).unwrap().starts_with("Updated Studio."));
}

#[test]
fn outposts_updating_at_the_same_step_are_named_together() {
    let (world, _prefs) = start(vec![
        at("sending", old()),
        at("sending", named(old(), "oma", "oma")),
    ]);
    assert_eq!(
        banner(&world).as_deref(),
        Some("Updating outposts. Studio and oma: Copying it over.")
    );
}

#[test]
fn the_band_stays_hidden_with_every_outpost_current_or_with_outposts_off() {
    let current = OutpostInfo {
        build: Some(HOME_BUILD.into()),
        ..old()
    };
    let (mut world, _prefs) = start(vec![current]);
    assert_eq!(banner(&world), None);

    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([("outposts_enabled".to_owned(), "false".to_owned())].into()),
        ..SettingsUpdatedMessage::new()
    });
    list(&mut world, vec![old()]);
    assert_eq!(banner(&world), None);
}

#[test]
fn a_success_counts_down_holds_under_the_pointer_and_closes_until_a_new_update() {
    let dir = tempfile::tempdir().unwrap();
    let prefs = dir.path().join("prefs.json");
    let mut world = launch(home(vec![old()]), &prefs);
    list(&mut world, vec![done()]);
    assert_eq!(
        banner(&world).as_deref(),
        Some("Updated Studio. Closing in 5s")
    );

    world.advance(Duration::from_secs(2));
    assert_eq!(
        banner(&world).as_deref(),
        Some("Updated Studio. Closing in 3s")
    );
    hover(&mut world, true);
    world.advance(Duration::from_secs(10));
    assert_eq!(
        banner(&world).as_deref(),
        Some("Updated Studio. Closing in 3s")
    );
    hover(&mut world, false);
    world.advance(Duration::from_secs(2));
    assert_eq!(
        banner(&world).as_deref(),
        Some("Updated Studio. Closing in 1s")
    );
    world.advance(Duration::from_secs(1));
    assert_eq!(banner(&world), None);
    assert!(world.runtime_is_at_rest());
    drop(world);

    let mut world = launch(home(vec![done()]), &prefs);
    assert_eq!(banner(&world), None, "a closed success stays closed");
    list(&mut world, vec![done(), named(done(), "lab", "Lab")]);
    assert_eq!(
        banner(&world).as_deref(),
        Some("Updated Studio and Lab. Closing in 5s")
    );
}

#[test]
fn dismissing_a_success_closes_it_at_once_and_the_next_update_of_that_outpost_shows() {
    let (mut world, _prefs) = start(vec![done()]);
    hover(&mut world, true);
    click(&mut world, "outpost-updates.dismiss");
    assert_eq!(banner(&world), None);

    list(&mut world, vec![at("building", old())]);
    assert!(banner(&world).unwrap().starts_with("Updating outposts."));
    hover(&mut world, false);
    list(&mut world, vec![done()]);
    assert_eq!(
        banner(&world).as_deref(),
        Some("Updated Studio. Closing in 5s")
    );
    world.advance(Duration::from_secs(5));
    assert_eq!(banner(&world), None);
}

#[test]
fn an_update_in_progress_a_failure_or_a_different_build_never_closes_by_itself() {
    let (mut world, _prefs) = start(vec![old()]);
    let failed = OutpostInfo {
        update: Some(OutpostUpdate {
            phase: "failed".into(),
            outcome: Some("ssh: connection refused".into()),
        }),
        ..old()
    };
    for (row, said) in [
        (old(), "Studio runs a different build than home."),
        (
            at("verifying", old()),
            "Updating outposts. Studio: Checking it runs.",
        ),
        (
            failed,
            "Couldn’t update Studio. Studio: ssh: connection refused",
        ),
    ] {
        list(&mut world, vec![row]);
        world.advance(Duration::from_secs(60));
        assert_eq!(banner(&world).as_deref(), Some(said));
    }
}
