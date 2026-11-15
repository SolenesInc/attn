use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{Desktop, InitialStateMessage, Profile, ProfileArrangementChangedMessage};
use attn_testkit::{Node, Request, Target, World, key_event, message};
use rune::{Key, KeyEvent, KeyPlatform, Mods};
use serde_json::{Value, json};

use super::queue::{changes, one_desktop, queue, sent_ids, turn};
use super::sidebar::session;

fn desktop(id: &str, order_key: &str, slot: Option<i64>, name: &str) -> Desktop {
    Desktop {
        id: id.into(),
        profile_id: "work".into(),
        name: name.into(),
        shortcut_slot: slot,
        order_key: order_key.into(),
        revision: 1,
        ..Desktop::default()
    }
}

fn desktops() -> Vec<Desktop> {
    vec![
        desktop("unnamed2", "a", None, ""),
        desktop("nine", "b", Some(9), "aardvark"),
        desktop("named10", "c", None, "Review 10"),
        desktop("one", "d", Some(1), "Zebra"),
        desktop("named2", "e", None, "review 2"),
        desktop("unnamed1", "f", None, "  "),
        desktop("alpha", "g", None, "Alpha"),
    ]
}

const SORTED: [&str; 7] = [
    "one", "nine", "alpha", "named2", "named10", "unnamed2", "unnamed1",
];
const BEFORE: [&str; 7] = [
    "unnamed2", "nine", "named10", "one", "named2", "unnamed1", "alpha",
];

fn start(queue: bool, refusal: Option<&'static str>) -> World {
    let daemon = super::palette::daemon(
        InitialStateMessage {
            selected_profile_id: Some("work".into()),
            profiles: Some(vec![Profile {
                id: "work".into(),
                name: "Work".into(),
                current_desktop_id: "one".into(),
                ..Profile::default()
            }]),
            desktops: Some(desktops()),
            settings: Some(
                [("queue_mode_enabled".to_owned(), queue.to_string())]
                    .into_iter()
                    .collect(),
            ),
            ..InitialStateMessage::new()
        },
        vec![],
    );
    let world = World::start(daemon);
    world.daemon.on("desktop_set_order", move |r: &Request| {
        let mut answer = json!({
            "event": "profile_action_result",
            "request_id": r["request_id"],
            "action": "desktop_set_order",
            "success": refusal.is_none(),
        });
        if let Some(error) = refusal {
            answer["error"] = error.into();
        }
        answer
    });
    world
}

fn sort(world: &mut World) {
    let ev = match Keymap::from_setting(None, KeyPlatform::current())
        .binding(&Action::UiCommandPalette)
    {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("the palette has no single chord: {other:?}"),
    };
    world.gesture(|app| app.key(ev));
    world.gesture(|app| app.type_text("Sort desktops"));
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Enter,
            mods: Mods::default(),
        })
    });
    world.settle();
}

fn orders(world: &World) -> Vec<Value> {
    world
        .daemon
        .sent_of("desktop_set_order")
        .into_iter()
        .map(|r| json!({"profile_id": r["profile_id"], "desktop_ids": r["desktop_ids"]}))
        .collect()
}

fn order(ids: [&str; 7]) -> Value {
    json!({"profile_id": "work", "desktop_ids": ids})
}

fn offer(world: &World, name: &str) -> Option<Node> {
    world.screen().find(Target::role("button").name(name))
}

#[test]
fn sorting_sends_the_sorted_order_once_and_undo_sends_the_whole_previous_order() {
    for queue in [false, true] {
        let mut world = start(queue, None);
        sort(&mut world);
        assert_eq!(orders(&world), vec![order(SORTED)], "queue={queue}");

        world.gesture(|app| app.click(Target::role("button").name("Desktops sorted Undo")));
        world.settle();
        assert_eq!(
            orders(&world),
            vec![order(SORTED), order(BEFORE)],
            "queue={queue}"
        );
        let done = offer(&world, "Desktops sorted ✓ Done").expect("the notice says Undo is done");
        assert!(done.is("disabled"), "queue={queue}");

        world.gesture(|app| app.click(Target::role("button").name("Desktops sorted ✓ Done")));
        world.settle();
        assert_eq!(orders(&world).len(), 2, "a done Undo sends nothing more");
    }
}

#[test]
fn a_refused_sort_shows_the_refusal_and_offers_no_undo() {
    let mut world = start(false, Some("Desktop list changed"));
    sort(&mut world);
    let shown = world
        .screen()
        .find(Target::key("notice.text"))
        .and_then(|n| n.name);
    assert_eq!(shown.as_deref(), Some("Desktop list changed"));
    assert!(offer(&world, "Desktops sorted Undo").is_none());
}

#[test]
fn the_sorted_notice_stays_while_the_pointer_rests_on_it_and_leaves_after_it_goes() {
    let mut world = start(false, None);
    sort(&mut world);
    let [x, y, w, h] = world
        .runtime()
        .presented("notice.offer")
        .expect("the sorted notice is drawn")
        .0;
    world.gesture(|app| app.move_to((x + w / 2.0, y + h / 2.0)));
    world.advance(Duration::from_secs(20));
    assert!(offer(&world, "Desktops sorted Undo").is_some());

    world.gesture(|app| app.move_to((x + w / 2.0, y - 200.0)));
    world.advance(Duration::from_secs(5));
    assert!(offer(&world, "Desktops sorted Undo").is_some());
    world.advance(Duration::from_secs(2));
    assert!(offer(&world, "Desktops sorted Undo").is_none());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn following_the_next_turn_from_home_survives_a_sort_and_its_undo() {
    for undo in [false, true] {
        let sessions = vec![session("s1", "working"), session("s2", "working")];
        let initial = queue(one_desktop(sessions));
        let arranged = message(ProfileArrangementChangedMessage {
            profile: initial.profiles.clone().unwrap().remove(0),
            desktops: initial.desktops.clone().unwrap(),
            ..ProfileArrangementChangedMessage::new()
        });
        let mut world = World::start(super::palette::daemon(initial, vec![]));
        world.daemon.on("desktop_set_order", move |r: &Request| {
            vec![
                json!({
                    "event": "profile_action_result",
                    "request_id": r["request_id"],
                    "action": "desktop_set_order",
                    "success": true,
                }),
                arranged.clone(),
            ]
        });
        let follow = || Target::key("home.follow");
        if !undo {
            world.gesture(|app| app.click(follow()));
        }
        sort(&mut world);
        if undo {
            world.gesture(|app| app.click(follow()));
            world.gesture(|app| app.click(Target::role("button").name("Desktops sorted Undo")));
            world.settle();
            assert_eq!(orders(&world).len(), 2);
        }

        changes(&mut world, turn("s2", 0));

        assert_eq!(
            sent_ids(&world, "desktop_show_session"),
            ["s2"],
            "undo={undo}"
        );
    }
}
