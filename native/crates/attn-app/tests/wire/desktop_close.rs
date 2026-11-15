use attn_app::{Action, Binding, Keymap};
use attn_protocol::{Desktop, DesktopPane, InitialStateMessage, Profile, Session};
use attn_testkit::tree::{pane, row, tile};
use attn_testkit::{Request, Target, World, key_event};
use rune::{Key, KeyEvent, KeyPlatform, Mods};
use serde_json::{Value, json};

fn session(id: &str, agent: &str) -> Session {
    Session {
        id: id.into(),
        label: format!("Agent {id}"),
        agent: agent.into(),
        directory: format!("/work/{id}"),
        profile_id: "work".into(),
        state: "working".into(),
        ..Session::default()
    }
}

fn desktop(id: &str, slot: i64, name: &str, tree: Option<Value>, sessions: &[&str]) -> Desktop {
    Desktop {
        id: id.into(),
        profile_id: "work".into(),
        name: name.into(),
        shortcut_slot: Some(slot),
        order_key: format!("{slot}"),
        tree_json: tree.map(|t| t.to_string()).unwrap_or_default(),
        active_pane_id: sessions
            .first()
            .map(|s| format!("p-{s}"))
            .unwrap_or_default(),
        panes: sessions
            .iter()
            .map(|s| DesktopPane {
                desktop_id: id.into(),
                pane_id: format!("p-{s}"),
                runtime_id: format!("r-{s}"),
                session_id: (*s).into(),
                title: format!("Agent {s}"),
                kind: "agent".into(),
                ..DesktopPane::default()
            })
            .collect(),
        revision: 1,
    }
}

fn start(queue: bool, sessions: Vec<Session>, work: Desktop) -> World {
    let other = desktop("other", 2, "", None, &[]);
    let daemon = super::palette::daemon(
        InitialStateMessage {
            selected_profile_id: Some("work".into()),
            profiles: Some(vec![Profile {
                id: "work".into(),
                name: "Work".into(),
                current_desktop_id: work.id.clone(),
                ..Profile::default()
            }]),
            desktops: Some(vec![work, other]),
            sessions: Some(sessions),
            settings: Some(
                [("queue_mode_enabled".to_owned(), queue.to_string())]
                    .into_iter()
                    .collect(),
            ),
            ..InitialStateMessage::new()
        },
        vec![],
    );
    let mut world = World::start(daemon);
    world.daemon.on("desktop_close", |r: &Request| {
        json!({
            "event": "profile_action_result",
            "request_id": r["request_id"],
            "action": "desktop_close",
            "success": true,
        })
    });
    if queue {
        click(&mut world, Target::key("queue.desktop.1"));
    } else {
        world.open_desktop();
    }
    world
}

fn work_with_tiles() -> Desktop {
    let tree = row([
        pane("p-agent"),
        pane("p-shell"),
        tile("reference", "browser"),
    ]);
    desktop("work", 1, "Work", Some(tree), &["agent", "shell"])
}

fn close_key() -> KeyEvent {
    match Keymap::from_setting(None, KeyPlatform::current()).binding(&Action::SessionClose) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("close has no single chord: {other:?}"),
    }
}

fn press(world: &mut World, action: Action) {
    let ev = match Keymap::from_setting(None, KeyPlatform::current()).binding(&action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    };
    world.gesture(|app| app.key(ev));
    world.settle();
}

fn key(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
    world.settle();
}

fn click(world: &mut World, target: Target) {
    world.gesture(|app| app.click(target));
    world.settle();
}

#[derive(Clone, Copy, Debug)]
enum Entry {
    Sidebar,
    DesktopPalette,
    QueuePalette,
}

fn ask_to_close(world: &mut World, entry: Entry) {
    match entry {
        Entry::Sidebar => click(world, Target::role("button").name("Close Work")),
        Entry::DesktopPalette | Entry::QueuePalette => {
            press(world, Action::UiCommandPalette);
            world.gesture(|app| app.type_text("Close desktop"));
            key(world, Key::Enter);
        }
    }
}

fn confirmation(world: &World) -> Option<Vec<String>> {
    let dialog = Target::role("dialog").name("Close Work?");
    world.screen().find(dialog.clone())?;
    Some(
        world
            .screen()
            .descendants(dialog)
            .into_iter()
            .filter_map(|n| n.name)
            .collect(),
    )
}

fn closes(world: &World) -> Vec<(String, i64)> {
    world
        .daemon
        .sent_of("desktop_close")
        .iter()
        .map(|r| {
            (
                r["desktop_id"].as_str().unwrap().to_owned(),
                r["expected_revision"].as_i64().unwrap(),
            )
        })
        .collect()
}

#[test]
fn closing_a_desktop_with_tiles_asks_once_and_closes_only_on_confirm_from_each_entry() {
    for entry in [Entry::Sidebar, Entry::DesktopPalette, Entry::QueuePalette] {
        let sessions = vec![session("agent", "claude"), session("shell", "shell")];
        let mut world = start(
            matches!(entry, Entry::QueuePalette),
            sessions,
            work_with_tiles(),
        );
        if matches!(entry, Entry::Sidebar)
            && world
                .screen()
                .find(Target::role("button").name("Close Work"))
                .is_none()
        {
            panic!(
                "{entry:?}: the sidebar offers no close for Work:\n{}",
                world.screen()
            );
        }

        ask_to_close(&mut world, entry);
        let shown = confirmation(&world).unwrap_or_else(|| panic!("{entry:?}: no confirmation"));
        assert!(
            shown
                .iter()
                .any(|n| n == "Close this desktop and everything inside it?"),
            "{entry:?}: {shown:?}"
        );
        assert!(closes(&world).is_empty(), "{entry:?}");

        assert!(
            !world.press_menu_key(close_key()),
            "{entry:?}: the close key reached the menu bar, whose Close quits the app"
        );
        assert!(world.daemon.sent_of("unregister").is_empty(), "{entry:?}");
        assert!(
            world.daemon.sent_of("desktop_close_tile").is_empty(),
            "{entry:?}"
        );
        assert!(
            world.daemon.sent_of("desktop_remove_leaf").is_empty(),
            "{entry:?}"
        );
        assert!(closes(&world).is_empty(), "{entry:?}");

        click(&mut world, Target::role("button").name("Cancel"));
        assert!(confirmation(&world).is_none(), "{entry:?}");
        assert!(closes(&world).is_empty(), "{entry:?}");

        ask_to_close(&mut world, entry);
        click(&mut world, Target::role("button").name("Close desktop"));
        assert!(confirmation(&world).is_none(), "{entry:?}");
        assert_eq!(closes(&world), [("work".to_owned(), 1)], "{entry:?}");
    }
}

#[test]
fn a_shell_only_desktop_asks_the_same_confirmation_and_escape_leaves_it_open() {
    let mut world = start(
        false,
        vec![session("shell", "shell")],
        desktop("work", 1, "Work", Some(pane("p-shell")), &["shell"]),
    );
    ask_to_close(&mut world, Entry::Sidebar);
    let shown = confirmation(&world).expect("a confirmation");
    assert!(
        shown
            .iter()
            .any(|n| n == "Close this desktop and everything inside it?")
    );
    assert!(closes(&world).is_empty());
    assert!(
        world
            .screen()
            .get(Target::role("button").name("Cancel"))
            .is("focused"),
        "Cancel holds the keyboard"
    );

    key(&mut world, Key::Escape);
    assert!(confirmation(&world).is_none());
    assert!(closes(&world).is_empty());
}

#[test]
fn a_desktop_holding_a_crew_members_session_names_it_and_sends_nothing() {
    for (chief, member) in [(true, "chief"), (false, "alder")] {
        let protected = Session {
            label: "Coordinator".into(),
            crew_member: Some(member.into()),
            chief_of_staff: Some(chief),
            ..session("s1", "claude")
        };
        let mut world = start(
            false,
            vec![protected],
            desktop("work", 1, "Work", Some(pane("p-s1")), &["s1"]),
        );
        ask_to_close(&mut world, Entry::Sidebar);
        let notice = world
            .screen()
            .find(Target::key("notice.text"))
            .and_then(|n| n.name);
        assert_eq!(
            notice.as_deref(),
            Some("Cannot close desktop: move these protected sessions first: Coordinator."),
            "{member}"
        );
        assert!(confirmation(&world).is_none(), "{member}");
        assert!(closes(&world).is_empty(), "{member}");
    }
}
