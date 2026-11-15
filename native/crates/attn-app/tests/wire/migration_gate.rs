use std::collections::BTreeMap;

use attn_app::Action;
use attn_protocol::{InitialStateMessage, MigrationChangedMessage, MigrationState};
use attn_testkit::{Daemon, Target, World};
use rune::prefs::Prefs;

use super::keys::chord;

fn home(phase: &str) -> Daemon {
    Daemon::scripted().state(InitialStateMessage {
        migration_phase: Some(phase.into()),
        settings: Some(BTreeMap::new()),
        ..InitialStateMessage::new()
    })
}

fn step(world: &World, key: &str) -> Option<String> {
    world.screen().find(Target::key(key)).and_then(|n| n.name)
}

fn finish(world: &mut World) {
    world.daemon.emit(MigrationChangedMessage {
        event: "migration_changed".into(),
        state: MigrationState {
            phase: "complete".into(),
            revision: 9,
            profile_id: "default".into(),
            ..MigrationState::default()
        },
    });
    world.settle();
}

#[test]
fn a_home_still_placing_workspaces_opens_on_the_gate_instead_of_its_agents() {
    let mut world = World::start_with_prefs(home("placement_required"), Prefs::memory());

    let screen = world.screen();
    assert_eq!(
        screen
            .find(Target::key("migration-gate.title"))
            .and_then(|n| n.name)
            .as_deref(),
        Some("Finish moving to profiles")
    );
    assert!(
        step(&world, "migration-gate.placement")
            .unwrap()
            .contains("Place each workspace's sessions on desktops")
    );
    assert!(screen.find(Target::key("regions")).is_none());
    assert!(screen.find(Target::key("whats-new-dialog")).is_none());
    assert!(screen.find(Target::key("titlebar.profile")).is_none());

    world.gesture(|app| app.key(chord(&Action::UiCommandPalette)));
    world.gesture(|app| app.key(chord(&Action::SessionNew)));
    assert!(world.screen().find(Target::key("palette.field")).is_none());
    assert!(world.screen().find(Target::key("new-session")).is_none());
}

#[test]
fn a_home_choosing_its_launch_desktops_says_so() {
    let world = World::start(home("launch_required"));

    assert!(
        step(&world, "migration-gate.launch")
            .unwrap()
            .contains("Choose which desktops launch")
    );
}

#[test]
fn the_text_size_keys_still_work_at_the_gate() {
    let mut world = World::start(home("placement_required"));

    world.gesture(|app| app.key(chord(&Action::UiIncreaseFontSize)));

    assert_eq!(world.daemon.sent_of("set_setting").len(), 1);
}

#[test]
fn finishing_the_move_in_another_app_opens_the_agents() {
    let mut world = World::start(home("placement_required"));

    finish(&mut world);

    assert!(world.screen().find(Target::key("migration-gate")).is_none());
    assert!(world.screen().find(Target::key("regions")).is_some());
}

#[test]
fn a_finished_home_never_shows_the_gate() {
    let world = World::start(home("complete"));

    assert!(world.screen().find(Target::key("migration-gate")).is_none());
    assert!(world.screen().find(Target::key("regions")).is_some());
}
