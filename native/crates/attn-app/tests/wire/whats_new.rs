use std::path::{Path, PathBuf};
use std::time::Duration;

use attn_app::whats_new::{BANNER_PREF, RELEASE, SEEN_PREF};
use attn_app::{Action, Keymap};
use attn_testkit::{Daemon, Target, World};
use rune::KeyPlatform;
use rune::prefs::Prefs;
use rune::{Key, KeyEvent, Mods};

use super::keys::{chord, state, two_terminals};

const STEPS: usize = 7;

fn daemon(settings: &[(&str, &str)]) -> Daemon {
    super::palette::daemon(state(two_terminals(), settings), vec![])
}

/// A prefs file whose app last showed an earlier release's tour.
fn after_update(dir: &Path) -> PathBuf {
    let path = dir.join("prefs.json");
    let mut prefs = Prefs::at(&path);
    prefs
        .set(SEEN_PREF, &"an-earlier-release".to_owned())
        .unwrap();
    prefs.set(BANNER_PREF, &RELEASE.to_owned()).unwrap();
    path
}

fn launch(path: &Path, settings: &[(&str, &str)]) -> World {
    World::start_with_prefs(daemon(settings), Prefs::at(path))
}

fn open(world: &World) -> bool {
    world
        .screen()
        .find(Target::role("dialog").name("What's new"))
        .is_some()
}

fn name(world: &World, key: &str) -> Option<String> {
    world.screen().find(Target::key(key)).and_then(|n| n.name)
}

fn title(world: &World) -> String {
    name(world, "whats-new.title").expect("the tour shows a step")
}

fn keys(world: &World) -> Vec<String> {
    world
        .screen()
        .children(Target::key("whats-new.keys"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn press(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

fn press_action(world: &mut World, action: &Action) {
    let ev = chord(action);
    world.gesture(|app| app.key(ev));
}

fn replay_from_palette(world: &mut World) {
    press_action(world, &Action::UiCommandPalette);
    world.gesture(|app| app.type_text("What's new"));
    press(world, Key::Enter);
}

#[test]
fn the_first_launch_after_an_update_opens_on_the_first_step_and_an_up_to_date_launch_never_does() {
    let dir = tempfile::tempdir().unwrap();
    let world = launch(&after_update(dir.path()), &[]);
    assert!(open(&world));
    assert_eq!(title(&world), "One profile per world");
    assert_eq!(
        name(&world, "whats-new.count").as_deref(),
        Some(format!("1 of {STEPS}").as_str())
    );
    assert!(world.runtime_is_at_rest());
    drop(world);

    let world = World::start(daemon(&[]));
    assert!(!open(&world));
}

#[test]
fn right_enter_and_next_step_forward_and_left_and_back_step_back() {
    let dir = tempfile::tempdir().unwrap();
    let mut world = launch(&after_update(dir.path()), &[]);

    press(&mut world, Key::Right);
    assert_eq!(title(&world), "Desktops arrange your agents");
    press(&mut world, Key::Enter);
    assert_eq!(title(&world), "Arrange each desktop your way");
    world.gesture(|app| app.click(Target::key("whats-new.primary")));
    assert_eq!(title(&world), "The queue decides what's next");

    press(&mut world, Key::Left);
    assert_eq!(title(&world), "Arrange each desktop your way");
    world.gesture(|app| app.click(Target::key("whats-new.back")));
    assert_eq!(title(&world), "Desktops arrange your agents");
    press(&mut world, Key::Left);
    press(&mut world, Key::Left);
    assert_eq!(title(&world), "One profile per world");
    assert!(world.screen().find(Target::key("whats-new.back")).is_none());
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn got_it_ends_the_tour_and_a_relaunch_keeps_it_closed() {
    let dir = tempfile::tempdir().unwrap();
    let path = after_update(dir.path());
    let mut world = launch(&path, &[]);
    for _ in 1..STEPS {
        press(&mut world, Key::Right);
    }
    assert_eq!(title(&world), "Keep your hands on the keyboard");
    assert_eq!(name(&world, "whats-new.primary").as_deref(), Some("Got it"));

    press(&mut world, Key::Right);
    assert_eq!(title(&world), "Keep your hands on the keyboard");

    world.gesture(|app| app.click(Target::key("whats-new.primary")));
    assert!(!open(&world));
    drop(world);

    let world = launch(&path, &[]);
    assert!(!open(&world));
}

#[test]
fn escape_part_way_marks_the_release_seen() {
    let dir = tempfile::tempdir().unwrap();
    let path = after_update(dir.path());
    let mut world = launch(&path, &[]);
    press(&mut world, Key::Right);

    press(&mut world, Key::Escape);
    assert!(!open(&world));
    drop(world);

    let world = launch(&path, &[]);
    assert!(!open(&world));
}

#[test]
fn view_all_shortcuts_hands_off_to_the_shortcut_list() {
    let dir = tempfile::tempdir().unwrap();
    let mut world = launch(&after_update(dir.path()), &[]);

    world.gesture(|app| app.click(Target::key("whats-new.shortcuts")));

    assert!(!open(&world));
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Keyboard shortcuts"))
            .is_some()
    );
}

#[test]
fn the_command_palette_replays_the_tour_from_the_first_step() {
    let dir = tempfile::tempdir().unwrap();
    let mut world = launch(&after_update(dir.path()), &[]);
    press(&mut world, Key::Right);
    press(&mut world, Key::Escape);
    assert!(!open(&world));

    replay_from_palette(&mut world);

    assert_eq!(title(&world), "One profile per world");
    assert!(world.runtime_is_at_rest());
}

fn key_label(keybindings: Option<&str>, action: Action) -> String {
    let keymap = Keymap::from_setting(keybindings, KeyPlatform::current());
    keymap.label(keymap.binding(&action).expect("the action has a key"))
}

#[test]
fn keys_and_the_scene_show_the_users_own_bindings() {
    let dir = tempfile::tempdir().unwrap();
    let rebound =
        r#"{"version":1,"overrides":{"profile.switch":{"key":"y","meta":true,"alt":true}}}"#;
    let switch = key_label(Some(rebound), Action::ProfileSwitch);
    let mut world = launch(
        &after_update(dir.path()),
        &[("keybindings_config", rebound)],
    );
    assert_ne!(switch, key_label(None, Action::ProfileSwitch));
    assert_eq!(keys(&world), [format!("{switch} Switch profile")]);
    let press_keys = |world: &World| {
        world
            .screen()
            .find(Target::key("whats-new.scene.press"))
            .and_then(|n| n.name)
    };
    assert_eq!(press_keys(&world), Some(switch));

    press(&mut world, Key::Right);
    assert_eq!(
        press_keys(&world),
        Some(key_label(None, Action::DesktopSelect2))
    );
    assert_eq!(
        name(&world, "whats-new.scene").as_deref(),
        Some("Pressing the desktop 2 shortcut swaps the panes for desktop 2's arrangement")
    );
}

#[test]
fn a_digit_key_reads_as_the_range_of_desktops() {
    let dir = tempfile::tempdir().unwrap();
    let mut world = launch(&after_update(dir.path()), &[]);

    press(&mut world, Key::Right);
    assert_eq!(
        keys(&world),
        [
            format!(
                "{}–9 Switch desktop",
                key_label(None, Action::DesktopSelect1)
            ),
            format!(
                "{} See all desktops",
                key_label(None, Action::DesktopOverview)
            ),
        ]
    );
}

#[test]
fn its_arrow_keys_stay_away_from_the_terminal_underneath() {
    let mut world = World::start(daemon(&[]));
    world.open_desktop();
    replay_from_palette(&mut world);
    let before = world.daemon.sent_of("pty_input").len();

    press(&mut world, Key::Right);
    press(&mut world, Key::Left);

    assert_eq!(title(&world), "One profile per world");
    assert!(world.daemon.sent_of("pty_input")[before..].is_empty());
}

/// A prefs file for someone who saw this release's tour but not its banner.
fn banner_unseen(dir: &Path) -> PathBuf {
    let path = dir.join("prefs.json");
    Prefs::at(&path)
        .set(SEEN_PREF, &RELEASE.to_owned())
        .unwrap();
    path
}

#[test]
fn homes_banner_offers_a_new_release_and_replays_it_from_the_first_step() {
    let dir = tempfile::tempdir().unwrap();
    let mut world = launch(&banner_unseen(dir.path()), &[]);
    assert!(!open(&world));

    world.gesture(|app| app.click(Target::key("home.intro-banner.replay")));

    assert_eq!(title(&world), "One profile per world");
}

#[test]
fn a_dismissed_banner_stays_away_across_launches_and_the_palette_still_replays() {
    let dir = tempfile::tempdir().unwrap();
    let path = banner_unseen(dir.path());
    let mut world = launch(&path, &[]);

    world.gesture(|app| app.click(Target::key("home.intro-banner.dismiss")));
    assert!(
        world
            .screen()
            .find(Target::key("home.intro-banner"))
            .is_none()
    );
    drop(world);

    let mut world = launch(&path, &[]);
    assert!(
        world
            .screen()
            .find(Target::key("home.intro-banner"))
            .is_none()
    );
    replay_from_palette(&mut world);
    assert_eq!(title(&world), "One profile per world");
}
