use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    Desktop, DesktopPane, InitialStateMessage, Profile, ProfileArrangementChangedMessage, Session,
};
use attn_testkit::tree::{below, pane, row, tile};
use attn_testkit::{Daemon, Request, Target, World, key_event};
use rune::KeyPlatform;
use serde_json::Value;

/// Wide enough for two tiles at the narrowest width, and too narrow for three.
const TWO_FIT: (f32, f32) = (1600.0, 900.0);

fn agent(desktop: &str, id: &str) -> DesktopPane {
    DesktopPane {
        desktop_id: desktop.into(),
        pane_id: id.into(),
        runtime_id: format!("r-{id}"),
        session_id: format!("s-{id}"),
        title: format!("Agent {id}"),
        kind: "agent".into(),
        ..DesktopPane::default()
    }
}

fn session(id: &str) -> Session {
    Session {
        id: format!("s-{id}"),
        label: format!("Agent {id}"),
        directory: format!("/work/{id}"),
        profile_id: "work".into(),
        ..Session::default()
    }
}

/// A desktop on `slot` whose tree is `tree`, with an agent pane for each pane leaf.
fn desktop(slot: u32, active: &str, tree: Option<Value>) -> Desktop {
    let id = format!("work/desktop_{slot}");
    let panes = tree
        .as_ref()
        .map(|t| pane_ids(t).iter().map(|p| agent(&id, p)).collect())
        .unwrap_or_default();
    Desktop {
        id: id.clone(),
        profile_id: "work".into(),
        name: format!("Desktop {slot}"),
        shortcut_slot: Some(slot as i64),
        order_key: format!("{slot}"),
        tree_json: tree.map(|t| t.to_string()).unwrap_or_default(),
        active_pane_id: active.into(),
        panes,
        revision: 1,
    }
}

fn pane_ids(tree: &Value) -> Vec<String> {
    match tree["type"].as_str() {
        Some("pane") => vec![tree["pane_id"].as_str().unwrap().to_owned()],
        Some("split") => tree["children"]
            .as_array()
            .unwrap()
            .iter()
            .flat_map(pane_ids)
            .collect(),
        _ => vec![],
    }
}

fn profile(current: &str) -> Profile {
    Profile {
        id: "work".into(),
        name: "Work".into(),
        current_desktop_id: current.into(),
        ..Profile::default()
    }
}

fn daemon(desktops: Vec<Desktop>) -> Daemon {
    let sessions = desktops
        .iter()
        .flat_map(|d| d.panes.iter())
        .map(|p| session(&p.pane_id))
        .collect();
    Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![profile(&desktops[0].id)]),
        desktops: Some(desktops),
        sessions: Some(sessions),
        ..InitialStateMessage::new()
    })
}

fn start(desktops: Vec<Desktop>) -> World {
    World::start_sized(daemon(desktops), TWO_FIT)
}

/// The daemon tells the app the profile's desktops and its current desktop.
fn arrange(world: &mut World, current: &str, desktops: Vec<Desktop>) {
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile(current),
        desktops,
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
}

fn press(world: &mut World, action: Action) {
    let ev = match Keymap::from_setting(None, KeyPlatform::current()).binding(&action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    };
    world.gesture(|app| app.key(ev));
}

fn shows(world: &World, title: &str) -> bool {
    world
        .screen()
        .find(Target::role("group").name(title))
        .is_some()
}

fn width(world: &World, leaf: &str) -> f32 {
    world
        .runtime()
        .presented(&format!("tile.{leaf}"))
        .unwrap_or_else(|| panic!("{leaf} is drawn"))
        .0[2]
}

/// Each command `cmd` the app sent, without its request id.
fn sent(world: &World, cmd: &str) -> Vec<Value> {
    world
        .daemon
        .sent_of(cmd)
        .into_iter()
        .map(|mut r: Request| {
            r.as_object_mut().unwrap().remove("request_id");
            r.as_object_mut().unwrap().remove("cmd");
            r
        })
        .collect()
}

fn json(text: &str) -> Value {
    serde_json::from_str(text).unwrap()
}

fn notice(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("notice.text"))
        .and_then(|n| n.name)
}

#[test]
fn the_screen_shows_the_columns_that_fit_around_the_active_tile_with_stacks_in_their_column() {
    let tree = row([below(pane("a"), pane("b")), pane("c"), pane("d")]);
    let mut world = start(vec![desktop(1, "a", Some(tree.clone()))]);

    assert!(shows(&world, "Agent a") && shows(&world, "Agent b") && shows(&world, "Agent c"));
    assert!(
        !shows(&world, "Agent d"),
        "the fourth column waits off screen"
    );
    let a = world.runtime().presented("tile.a").unwrap().0;
    let b = world.runtime().presented("tile.b").unwrap().0;
    assert_eq!(a[0], b[0], "b stacks below a in its column");
    assert!(b[1] > a[1]);

    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "d", Some(tree))],
    );
    assert!(
        shows(&world, "Agent d"),
        "the screen slides to the active tile"
    );
    assert!(!shows(&world, "Agent a"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_digit_goes_to_its_desktop_creates_a_missing_one_and_goes_back_from_the_current_one() {
    let mut world = start(vec![
        desktop(1, "a", Some(pane("a"))),
        desktop(2, "b", Some(pane("b"))),
    ]);

    press(&mut world, Action::DesktopSelect2);
    assert_eq!(
        sent(&world, "desktop_set_current"),
        [json(
            r#"{"profile_id":"work","desktop_id":"work/desktop_2"}"#
        )]
    );
    arrange(
        &mut world,
        "work/desktop_2",
        vec![
            desktop(1, "a", Some(pane("a"))),
            desktop(2, "b", Some(pane("b"))),
        ],
    );
    assert!(shows(&world, "Agent b"));

    press(&mut world, Action::DesktopSelect2);
    assert_eq!(
        sent(&world, "desktop_set_current")[1]["desktop_id"],
        "work/desktop_1",
        "the current desktop's digit goes back"
    );

    press(&mut world, Action::DesktopSelect4);
    assert_eq!(
        sent(&world, "desktop_create"),
        [json(r#"{"profile_id":"work","shortcut_slot":4}"#)]
    );
    assert_eq!(
        sent(&world, "desktop_set_current")[2]["desktop_id"],
        "work/desktop_4"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn up_and_down_step_through_desktops_in_their_order_round_the_end() {
    let mut second = desktop(2, "b", Some(pane("b")));
    second.order_key = "0".into();
    let world_desktops = vec![desktop(1, "a", Some(pane("a"))), second];
    let mut world = start(world_desktops);

    press(&mut world, Action::SessionNext);
    press(&mut world, Action::SessionPrev);
    let to: Vec<_> = sent(&world, "desktop_set_current")
        .iter()
        .map(|r| r["desktop_id"].as_str().unwrap().to_owned())
        .collect();
    assert_eq!(to, ["work/desktop_2", "work/desktop_2"]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn arrows_show_the_neighbouring_tile_and_leave_the_desktop_past_the_row_end() {
    let tree = row([pane("a"), below(pane("b"), pane("c")), pane("d")]);
    let mut world = start(vec![
        desktop(1, "c", Some(tree.clone())),
        desktop(2, "x", Some(pane("x"))),
    ]);

    press(&mut world, Action::TerminalFocusUp);
    press(&mut world, Action::TerminalFocusLeft);
    press(&mut world, Action::TerminalFocusRight);
    let shown: Vec<_> = sent(&world, "desktop_show_leaf")
        .iter()
        .map(|r| r["leaf_id"].as_str().unwrap().to_owned())
        .collect();
    assert_eq!(shown, ["b", "a", "d"]);

    arrange(
        &mut world,
        "work/desktop_1",
        vec![
            desktop(1, "d", Some(tree)),
            desktop(2, "x", Some(pane("x"))),
        ],
    );
    press(&mut world, Action::TerminalFocusRight);
    assert_eq!(
        sent(&world, "desktop_set_current"),
        [json(
            r#"{"profile_id":"work","desktop_id":"work/desktop_2"}"#
        )]
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_tile_column_takes_its_most_recently_used_tile_when_arrows_reach_it() {
    let tree = row([below(pane("a"), pane("b")), pane("c")]);
    let mut world = start(vec![desktop(1, "b", Some(tree.clone()))]);
    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "c", Some(tree))],
    );

    press(&mut world, Action::TerminalFocusLeft);
    assert_eq!(sent(&world, "desktop_show_leaf")[0]["leaf_id"], "b");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn send_moves_the_active_tile_to_a_desktop_and_follows_it_only_when_asked() {
    let tree = row([pane("a"), pane("b")]);
    let mut world = start(vec![desktop(1, "b", Some(tree))]);

    press(&mut world, Action::DesktopSendStay3);
    press(&mut world, Action::DesktopSend3);
    press(&mut world, Action::DesktopSend1);
    let moved =
        json(r#"{"source_desktop_id":"work/desktop_1","target_shortcut_slot":3,"leaf_id":"b"}"#);
    assert_eq!(sent(&world, "desktop_move_leaf"), [moved.clone(), moved]);
    assert_eq!(
        sent(&world, "desktop_set_current"),
        [json(
            r#"{"profile_id":"work","desktop_id":"work/desktop_3"}"#
        )],
        "only send follows, and the current desktop's own digit sends nothing"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn split_opens_a_shell_beside_or_below_the_active_terminal_in_its_folder() {
    let mut world = start(vec![desktop(1, "a", Some(pane("a")))]);

    press(&mut world, Action::TerminalSplitVertical);
    press(&mut world, Action::TerminalSplitHorizontal);
    let spawned = sent(&world, "spawn_session");
    assert_eq!(spawned.len(), 2);
    for (spawn, direction) in spawned.iter().zip(["vertical", "horizontal"]) {
        assert_eq!(spawn["agent"], "shell");
        assert_eq!(spawn["cwd"], "/work/a");
        assert_eq!(spawn["spawned_from"], "s-a");
        assert_eq!(
            spawn["placement"],
            json(&format!(
                r#"{{"desktop_id":"work/desktop_1","anchor_pane_id":"a","direction":"{direction}"}}"#
            ))
        );
    }
    let shown: Vec<_> = sent(&world, "desktop_show_session")
        .iter()
        .map(|r| r["session_id"].clone())
        .collect();
    assert_eq!(shown, [spawned[0]["id"].clone(), spawned[1]["id"].clone()]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_column_as_tall_as_its_tiles_allow_opens_the_shell_beside_and_says_why() {
    let tree = row([below(pane("a"), pane("b"))]);
    let mut world = start(vec![desktop(1, "b", Some(tree))]);

    press(&mut world, Action::TerminalSplitHorizontal);
    assert_eq!(
        sent(&world, "spawn_session")[0]["placement"]["direction"],
        "vertical"
    );
    let text = notice(&world).expect("a notice says why");
    assert!(
        text.starts_with("3 stacked tiles at the 320px minimum need 960px. Tiles have "),
        "{text}"
    );
    assert!(
        text.ends_with("px here, so it opened beside instead."),
        "{text}"
    );
}

#[test]
fn zoom_widens_the_active_tile_and_focus_shows_it_alone_each_until_pressed_again() {
    let tree = row([pane("a"), pane("b")]);
    let mut world = start(vec![desktop(1, "a", Some(tree))]);
    let even = width(&world, "a");
    assert!((even - width(&world, "b")).abs() < 1.0);

    press(&mut world, Action::TerminalToggleZoom);
    assert!(width(&world, "a") > 2.0 * width(&world, "b"));
    press(&mut world, Action::TerminalToggleZoom);
    assert!((width(&world, "a") - even).abs() < 1.0);

    press(&mut world, Action::TerminalToggleMaximize);
    assert!(shows(&world, "Agent a") && !shows(&world, "Agent b"));
    press(&mut world, Action::TerminalToggleMaximize);
    assert!(shows(&world, "Agent b"));
    world.advance(Duration::from_secs(1));
    assert!(
        world.runtime_is_at_rest(),
        "the resized terminals told the daemon and rest"
    );
}

#[test]
fn escape_leaves_focus_before_the_terminal_hears_it() {
    let tree = row([pane("a"), pane("b")]);
    let mut world = start(vec![desktop(1, "a", Some(tree))]);
    press(&mut world, Action::TerminalToggleMaximize);

    world.gesture(|app| app.press("escape"));
    assert!(shows(&world, "Agent b"));
    assert!(world.daemon.sent_of("pty_input").is_empty());

    world.gesture(|app| app.press("escape"));
    assert_eq!(
        world.daemon.sent_of("pty_input").len(),
        1,
        "out of focus, Escape types"
    );
}

#[test]
fn focus_ends_when_another_tile_becomes_active() {
    let tree = row([pane("a"), pane("b")]);
    let mut world = start(vec![desktop(1, "a", Some(tree.clone()))]);
    press(&mut world, Action::TerminalToggleMaximize);
    assert!(!shows(&world, "Agent b"));

    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "b", Some(tree))],
    );
    assert!(shows(&world, "Agent a") && shows(&world, "Agent b"));
}

#[test]
fn close_removes_a_document_tile_and_ends_a_terminal_tile() {
    let tree = row([pane("a"), tile("m", "markdown")]);
    let mut world = start(vec![desktop(1, "m", Some(tree.clone()))]);
    assert!(shows(&world, "Markdown"));

    press(&mut world, Action::SessionClose);
    assert_eq!(
        sent(&world, "desktop_remove_leaf"),
        [json(r#"{"desktop_id":"work/desktop_1","leaf_id":"m"}"#)]
    );
    assert!(sent(&world, "desktop_close_tile").is_empty());

    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "a", Some(pane("a")))],
    );
    press(&mut world, Action::SessionClose);
    assert_eq!(
        sent(&world, "desktop_close_tile"),
        [json(r#"{"desktop_id":"work/desktop_1","tile_id":"a"}"#)]
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn history_walks_back_and_forward_through_the_tiles_this_window_showed() {
    let first = || desktop(1, "a", Some(row([pane("a"), pane("b")])));
    let second = || desktop(2, "c", Some(pane("c")));
    let mut world = start(vec![first(), second()]);
    let mut b_active = first();
    b_active.active_pane_id = "b".into();
    arrange(
        &mut world,
        "work/desktop_1",
        vec![b_active.clone(), second()],
    );
    arrange(
        &mut world,
        "work/desktop_2",
        vec![b_active.clone(), second()],
    );

    press(&mut world, Action::SessionHistoryBack);
    arrange(&mut world, "work/desktop_1", vec![b_active, second()]);
    press(&mut world, Action::SessionHistoryBack);
    press(&mut world, Action::SessionHistoryForward);
    let shown: Vec<_> = sent(&world, "desktop_show_leaf")
        .iter()
        .map(|r| {
            format!(
                "{}:{}",
                r["desktop_id"].as_str().unwrap(),
                r["leaf_id"].as_str().unwrap()
            )
        })
        .collect();
    assert_eq!(
        shown,
        ["work/desktop_1:b", "work/desktop_1:a", "work/desktop_1:b"]
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn history_keeps_its_place_when_a_tile_it_passed_closes() {
    let with =
        |ids: &[&str], active: &str| desktop(1, active, Some(row(ids.iter().map(|id| pane(id)))));
    let mut world = start(vec![with(&["a", "b", "c"], "a")]);
    arrange(
        &mut world,
        "work/desktop_1",
        vec![with(&["a", "b", "c"], "b")],
    );
    arrange(
        &mut world,
        "work/desktop_1",
        vec![with(&["a", "b", "c"], "c")],
    );
    press(&mut world, Action::SessionHistoryBack);
    arrange(
        &mut world,
        "work/desktop_1",
        vec![with(&["a", "b", "c"], "b")],
    );
    arrange(&mut world, "work/desktop_1", vec![with(&["b", "c"], "b")]);

    press(&mut world, Action::SessionHistoryForward);
    let shown: Vec<_> = sent(&world, "desktop_show_leaf")
        .iter()
        .map(|r| r["leaf_id"].as_str().unwrap().to_owned())
        .collect();
    assert_eq!(
        shown,
        ["b", "c"],
        "forward from b still reaches c after a closed"
    );
}

#[test]
fn clicking_a_tile_makes_it_the_active_tile_and_gives_it_the_keyboard() {
    let tree = row([pane("a"), pane("b")]);
    let mut world = start(vec![desktop(1, "a", Some(tree))]);

    world.gesture(|app| app.click(Target::role("group").name("Agent b")));
    assert_eq!(
        sent(&world, "desktop_show_leaf"),
        [json(r#"{"desktop_id":"work/desktop_1","leaf_id":"b"}"#)]
    );
    let keyboard = world
        .automate("state", serde_json::json!({"of": "window"}))
        .unwrap()["keyboard"]["pane"]
        .clone();
    assert_eq!(keyboard, "b");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn palette_rows_go_to_a_desktop_open_a_tile_and_move_an_agent_with_its_delegates() {
    let mut world = start(vec![
        desktop(1, "a", Some(row([pane("a"), tile("m", "markdown")]))),
        desktop(2, "b", Some(pane("b"))),
    ]);

    world.gesture(|app| {
        app.update(|state, tx| {
            let actions = [
                Action::GoToDesktop {
                    desktop: "work/desktop_2".into(),
                },
                Action::OpenTile {
                    desktop: "work/desktop_1".into(),
                    tile: "m".into(),
                },
                Action::MoveWithDelegates {
                    desktop: "work/desktop_2".into(),
                },
            ];
            for action in actions {
                attn_app::dispatch(state, action, tx);
            }
        })
    });

    assert_eq!(
        sent(&world, "desktop_set_current"),
        [json(
            r#"{"profile_id":"work","desktop_id":"work/desktop_2"}"#
        )]
    );
    assert_eq!(
        sent(&world, "desktop_show_leaf"),
        [json(r#"{"desktop_id":"work/desktop_1","leaf_id":"m"}"#)]
    );
    assert_eq!(
        sent(&world, "desktop_move_leaf"),
        [json(
            r#"{"source_desktop_id":"work/desktop_1","target_desktop_id":"work/desktop_2","leaf_id":"a","with_delegates":true}"#
        )]
    );
    assert!(world.runtime_is_at_rest());
}
