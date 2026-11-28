use attn_app::{Action, Binding, Keymap};
use attn_protocol::{Desktop, InitialStateMessage, Profile};
use attn_testkit::tree::{below, row, tile};
use attn_testkit::{Request, Target, World, key_event};
use rune::{Key, KeyEvent, Mods};
use serde_json::{Value, json};
use std::time::Duration;

pub(super) const HOME: &str = "work/home";

fn view(name: &str) -> Value {
    let mut tile = tile(&format!("tile-view-{name}"), "markdown");
    tile["tile_params"] = json!({ "view": name }).to_string().into();
    tile
}

fn desktops() -> Vec<Desktop> {
    vec![
        Desktop {
            id: HOME.into(),
            profile_id: "work".into(),
            kind: "home".into(),
            order_key: "a".into(),
            tree_json: row([below(view("attn"), view("sessions")), view("pull-requests")])
                .to_string(),
            revision: 1,
            ..Desktop::default()
        },
        Desktop {
            id: "one".into(),
            profile_id: "work".into(),
            kind: "desktop".into(),
            shortcut_slot: Some(1),
            order_key: "b".into(),
            revision: 1,
            ..Desktop::default()
        },
    ]
}

pub(super) fn start(current: &str, queue: bool) -> World {
    let daemon = super::palette::daemon(
        InitialStateMessage {
            selected_profile_id: Some("work".into()),
            profiles: Some(vec![Profile {
                id: "work".into(),
                name: "Work".into(),
                current_desktop_id: current.into(),
                ..Profile::default()
            }]),
            desktops: Some(desktops()),
            settings: Some(
                [
                    ("views_enabled".to_owned(), "true".to_owned()),
                    ("queue_mode_enabled".to_owned(), queue.to_string()),
                ]
                .into_iter()
                .collect(),
            ),
            ..InitialStateMessage::new()
        },
        vec![],
    );
    let world = World::start_sized(daemon, (1600.0, 900.0));
    world.daemon.on("desktop_reset_home", |r: &Request| {
        json!({
            "event": "profile_action_result",
            "request_id": r["request_id"],
            "action": "desktop_reset_home",
            "success": true,
        })
    });
    world
}

fn press(world: &mut World, action: &Action) {
    let ev = match Keymap::from_setting(None, attn_testkit::platform()).binding(action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{action:?} has no single chord: {other:?}"),
    };
    world.gesture(|app| app.key(ev));
    world.settle();
}

fn shown_desktops(world: &World) -> Vec<String> {
    world
        .daemon
        .sent_of("desktop_set_current")
        .into_iter()
        .filter_map(|r| r["desktop_id"].as_str().map(str::to_owned))
        .collect()
}

fn has(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

#[test]
fn the_app_opens_on_home_and_lists_it_with_the_desktops_in_place_of_the_desktop_sidebars_home_row()
{
    for queue in [false, true] {
        let world = start(HOME, queue);
        for name in ["attn", "sessions", "pull-requests"] {
            assert!(
                has(&world, &format!("tile.tile-view-{name}")),
                "queue={queue}: Home's {name} tile is not drawn"
            );
        }
        assert_eq!(
            has(&world, "sidebar.home"),
            queue,
            "queue={queue}: only the queue sidebar, which lists no desktops, keeps its Home row"
        );
    }
    let world = start(HOME, false);
    let line = world
        .screen()
        .get(Target::key(format!("sidebar.desktop.{HOME}")));
    assert_eq!(line.name.as_deref(), Some("Open Home"));
    assert!(!has(&world, &format!("sidebar.desktop.{HOME}.close")));
    assert!(!has(&world, &format!("sidebar.desktop.{HOME}.rename")));
}

#[test]
fn the_home_key_shows_the_home_desktop_and_the_first_digit_desktop_1() {
    let mut world = start("one", false);
    press(&mut world, &Action::SessionGoToDashboard);
    press(&mut world, &Action::DesktopSelect1);
    assert_eq!(
        shown_desktops(&world),
        vec![HOME.to_owned(), "one".to_owned()]
    );
}

#[test]
fn the_queue_sidebars_home_row_shows_the_home_desktop() {
    let mut world = start("one", true);
    world.gesture(|app| app.click(Target::key("sidebar.home")));
    world.settle();
    assert_eq!(shown_desktops(&world), vec![HOME.to_owned()]);
}

#[test]
fn the_palette_puts_home_back_to_its_default_and_offers_no_close_on_home() {
    let mut world = start(HOME, false);
    press(&mut world, &Action::UiCommandPalette);
    world.gesture(|app| app.type_text("desktop"));
    world.settle();
    assert!(
        has(&world, "palette.row.command:put-home-back")
            && !has(&world, "palette.row.command:close-desktop"),
        "the palette offers {:?} on Home",
        super::palette::titles(&world)
    );
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Escape,
            mods: Mods::default(),
        })
    });
    press(&mut world, &Action::UiCommandPalette);
    world.gesture(|app| app.type_text("Put Home back"));
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Enter,
            mods: Mods::default(),
        })
    });
    world.settle();
    let sent: Vec<Value> = world
        .daemon
        .sent_of("desktop_reset_home")
        .into_iter()
        .map(|r| r["profile_id"].clone())
        .collect();
    assert_eq!(sent, vec![json!("work")]);
}

#[test]
fn home_draws_nothing_while_nothing_changes() {
    let mut world = start(HOME, false);
    world.advance(Duration::from_secs(5));
    let (commits, paints) = (world.runtime().stats.commits, world.runtime().stats.paints);
    for _ in 0..60 {
        world.advance(Duration::from_secs(1));
    }
    assert_eq!(
        (world.runtime().stats.commits, world.runtime().stats.paints),
        (commits, paints),
        "Home drew frames in a minute with nothing changing"
    );
}
