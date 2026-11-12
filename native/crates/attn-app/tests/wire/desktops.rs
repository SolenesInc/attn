use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    Desktop, DesktopPane, InitialStateMessage, Profile, ProfileArrangementChangedMessage, Session,
    SessionStateChangedMessage,
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
    let mut world = World::start_sized(daemon(desktops), TWO_FIT);
    world.open_desktop();
    world
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
/// What the app sent for `cmd` after `start` opened the current desktop from home.
fn sent(world: &World, cmd: &str) -> Vec<Value> {
    let opening = usize::from(cmd == "desktop_set_current");
    world
        .daemon
        .sent_of(cmd)
        .into_iter()
        .skip(opening)
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
    assert!(sent(&world, "desktop_show_session").is_empty());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_split_is_placed_by_its_spawn_and_its_shell_takes_the_keyboard_once_it_shows() {
    let mut world = start(vec![desktop(1, "a", Some(pane("a")))]);
    press(&mut world, Action::TerminalSplitVertical);
    let spawned = sent(&world, "spawn_session")[0]["id"]
        .as_str()
        .unwrap()
        .to_owned();
    let desktop_traffic: Vec<_> = world
        .daemon
        .sent()
        .iter()
        .filter_map(|r| r["cmd"].as_str().map(str::to_owned))
        .filter(|cmd| cmd.starts_with("desktop_"))
        .collect();
    assert_eq!(desktop_traffic, ["desktop_set_current"]);

    let mut placed = desktop(1, "n", Some(row([pane("a"), pane("n")])));
    placed.panes[1].session_id = spawned;
    arrange(&mut world, "work/desktop_1", vec![placed]);
    world.gesture(|app| app.type_text("x"));

    let typed: Vec<_> = world
        .daemon
        .sent_of("pty_input")
        .iter()
        .map(|r| r["id"].clone())
        .collect();
    assert_eq!(typed, ["r-n"]);
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
fn a_focused_tile_stays_alone_when_the_daemon_moves_on_and_gives_way_to_a_pick_of_another() {
    let tree = row([pane("a"), pane("b")]);
    let mut world = start(vec![desktop(1, "a", Some(tree.clone()))]);
    press(&mut world, Action::TerminalToggleMaximize);
    assert!(!shows(&world, "Agent b"));

    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "b", Some(tree.clone()))],
    );
    assert!(shows(&world, "Agent a") && !shows(&world, "Agent b"));

    world.gesture(|app| app.click(Target::key("sidebar.session.s-a")));
    assert!(
        !shows(&world, "Agent b"),
        "picking the focused tile keeps it alone"
    );

    world.gesture(|app| app.click(Target::key("sidebar.session.s-b")));
    assert!(shows(&world, "Agent a") && shows(&world, "Agent b"));
}

#[test]
fn a_tile_goes_by_its_sessions_name_as_renamed_and_a_document_by_its_file() {
    let tree = row([pane("a"), document("plan", "a")]);
    let mut world = start(vec![desktop(1, "a", Some(tree))]);
    assert!(shows(&world, "Agent a") && shows(&world, "plan.md"));

    world.daemon.emit(SessionStateChangedMessage {
        session: Session {
            label: "nightly docs sweep".into(),
            ..session("a")
        },
        ..SessionStateChangedMessage::new()
    });
    world.settle();

    assert!(shows(&world, "nightly docs sweep"));
    assert!(!shows(&world, "Agent a"));
    assert!(world.runtime_is_at_rest());
}

fn key_label(action: Action) -> String {
    let keymap = Keymap::from_setting(None, KeyPlatform::current());
    keymap.label(keymap.binding(&action).expect("the action has a key"))
}

fn header_label(world: &World, key: &str) -> Option<String> {
    world.screen().find(Target::key(key)).and_then(|n| n.name)
}

#[test]
fn an_agents_header_shows_its_state_beside_its_name_and_its_pencil_renames_the_session() {
    let mut world = start(vec![desktop(1, "a", Some(pane("a")))]);
    world.daemon.emit(SessionStateChangedMessage {
        session: Session {
            state: "waiting_input".into(),
            agent: "claude".into(),
            ..session("a")
        },
        ..SessionStateChangedMessage::new()
    });
    world.settle();
    let state = world.screen().get(Target::key("tile.a.state"));
    assert_eq!(state.value.as_deref(), Some("waiting_input"));

    assert_eq!(
        header_label(&world, "tile.a.rename").as_deref(),
        Some("Rename session Agent a")
    );
    world.gesture(|app| app.click(Target::key("tile.a.rename")));
    let field = world.screen().get(Target::key("session.rename.field"));
    assert_eq!(field.value.as_deref(), Some("Agent a"));
    world.gesture(|app| app.type_text("ledger sweep"));
    world.gesture(|app| app.press("enter"));
    let renamed: Vec<_> = sent(&world, "rename_session");
    assert_eq!(
        renamed,
        [json(r#"{"session_id":"s-a","label":"ledger sweep"}"#)]
    );
}

#[test]
fn the_header_focuses_its_agent_until_return_to_row_and_leaving_the_desktop_ends_focus() {
    let tree = row([pane("a"), pane("b")]);
    let both = || {
        vec![
            desktop(1, "a", Some(row([pane("a"), pane("b")]))),
            desktop(2, "c", Some(pane("c"))),
        ]
    };
    let mut world = start(vec![
        desktop(1, "a", Some(tree)),
        desktop(2, "c", Some(pane("c"))),
    ]);
    assert_eq!(
        header_label(&world, "tile.b.focus").as_deref(),
        Some(
            format!(
                "Focus agent Agent b ({})",
                key_label(Action::TerminalToggleMaximize)
            )
            .as_str()
        )
    );

    world.gesture(|app| app.click(Target::key("tile.b.focus")));
    assert!(shows(&world, "Agent b") && !shows(&world, "Agent a"));
    assert_eq!(
        sent(&world, "desktop_show_leaf")[0]["leaf_id"],
        "b",
        "the focused agent becomes the active tile"
    );
    let keyboard = world
        .automate("state", serde_json::json!({"of": "window"}))
        .unwrap()["keyboard"]["pane"]
        .clone();
    assert_eq!(keyboard, "b", "the focused agent takes the keyboard");
    assert!(header_label(&world, "tile.b.focus").is_none());
    world.gesture(|app| app.click(Target::key("tile.b.return")));
    assert!(shows(&world, "Agent a") && shows(&world, "Agent b"));
    assert!(header_label(&world, "tile.b.return").is_none());

    world.gesture(|app| app.click(Target::key("tile.a.focus")));
    arrange(&mut world, "work/desktop_2", both());
    arrange(&mut world, "work/desktop_1", both());
    assert!(shows(&world, "Agent a") && shows(&world, "Agent b"));
    assert!(header_label(&world, "tile.a.focus").is_some());
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_agents_header_shows_its_short_session_id_and_copies_the_whole_id() {
    let mut world = start(vec![desktop(1, "a1b2c3d4e5", Some(pane("a1b2c3d4e5")))]);
    let id = world.screen().get(Target::key("tile.a1b2c3d4e5.id"));
    assert_eq!(id.name.as_deref(), Some("Copy session id s-a1b2c3"));

    world.gesture(|app| app.click(Target::key("tile.a1b2c3d4e5.id")));
    assert_eq!(
        world.clipboard.text().map(|t| t.to_string()).as_deref(),
        Some("s-a1b2c3d4e5")
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn close_removes_a_document_tile_and_ends_a_terminal_tile() {
    let tree = row([pane("a"), tile("m", "markdown")]);
    let mut world = start(vec![desktop(1, "m", Some(tree.clone()))]);
    assert!(shows(&world, "markdown"));

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

fn chip(world: &World, leaf: &str) -> Option<attn_testkit::Node> {
    world
        .screen()
        .find(Target::key(format!("shelf.chip.{leaf}")))
}

fn click(world: &mut World, key: &str) {
    let key = key.to_owned();
    world.gesture(|app| app.click(Target::key(key)));
}

fn four_in_a_row(active: &str) -> Desktop {
    desktop(
        1,
        active,
        Some(row([pane("a"), pane("b"), pane("c"), pane("d")])),
    )
}

#[test]
fn the_shelf_shows_a_chip_per_column_off_screen_and_a_dot_per_column_on_it() {
    let tree = row([pane("a"), pane("b"), below(pane("c"), pane("e")), pane("d")]);
    let mut world = start(vec![desktop(1, "a", Some(tree))]);

    assert!(chip(&world, "a").is_none(), "a is on screen");
    assert_eq!(
        chip(&world, "c").unwrap().name.as_deref(),
        Some("Agent c + Agent e")
    );
    assert_eq!(chip(&world, "d").unwrap().name.as_deref(), Some("Agent d"));
    let current = world.screen().get(Target::key("shelf.dot.a"));
    assert_eq!(current.value.as_deref(), Some("current"));
    assert!(world.screen().find(Target::key("shelf.dot.b")).is_some());

    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "a", Some(row([pane("a"), pane("b")])))],
    );
    assert!(
        world.screen().find(Target::key("shelf")).is_none(),
        "no shelf when every column shows"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_chip_says_when_its_agent_needs_you() {
    let desktops = vec![four_in_a_row("a")];
    let mut sessions: Vec<Session> = ["a", "b", "c", "d"].map(session).to_vec();
    sessions[3].state = "waiting_input".into();
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![profile("work/desktop_1")]),
        desktops: Some(desktops),
        sessions: Some(sessions),
        ..InitialStateMessage::new()
    });
    let mut world = World::start_sized(daemon, TWO_FIT);
    world.open_desktop();

    assert_eq!(
        chip(&world, "d").unwrap().value.as_deref(),
        Some("needs you")
    );
    assert_eq!(chip(&world, "c").unwrap().value.as_deref(), Some(""));
}

fn document(id: &str, owner: &str) -> Value {
    let mut doc = tile(id, "markdown");
    doc["tile_session_id"] = format!("s-{owner}").into();
    doc["tile_params"] = format!("{{\"path\":\"/work/{id}.md\"}}").into();
    doc
}

#[test]
fn a_document_arriving_beside_an_agent_off_screen_waits_unseen_for_the_next_jump_on_a_chip_that_flashes()
 {
    let mut world = start(vec![four_in_a_row("a")]);

    let arrived = row([
        pane("a"),
        pane("b"),
        pane("c"),
        pane("d"),
        document("doc", "d"),
    ]);
    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "a", Some(arrived))],
    );

    assert_eq!(shown(&world), ["a", "b"], "the screen stays where it was");
    assert_eq!(
        chip(&world, "doc").unwrap().value.as_deref(),
        Some("needs you, arrived")
    );
    world.advance(Duration::from_secs(1));
    assert_eq!(
        chip(&world, "doc").unwrap().value.as_deref(),
        Some("needs you"),
        "the flash is over and the document is still unseen"
    );

    press(&mut world, Action::SessionGoToDashboard);
    assert!(!shows(&world, "Agent a"));
    press(&mut world, Action::SessionJumpToWaiting);
    assert_eq!(
        sent(&world, "desktop_show_leaf"),
        [json(r#"{"desktop_id":"work/desktop_1","leaf_id":"doc"}"#)],
        "the next waiting thing is the unseen document"
    );
    assert!(
        shows(&world, "Agent a"),
        "the jump leaves Home for its desktop"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_document_arriving_before_its_desktop_first_shows_waits_unseen() {
    let mut world = World::start_sized(daemon(vec![four_in_a_row("a")]), TWO_FIT);

    let arrived = row([
        pane("a"),
        pane("b"),
        pane("c"),
        pane("d"),
        document("doc", "d"),
    ]);
    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "a", Some(arrived))],
    );
    world.open_desktop();

    assert_eq!(
        chip(&world, "doc").unwrap().value.as_deref(),
        Some("needs you, arrived")
    );
    world.advance(Duration::from_secs(1));
    assert_eq!(
        chip(&world, "doc").unwrap().value.as_deref(),
        Some("needs you")
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_document_arriving_beside_its_agent_on_screen_shows_and_the_screen_goes_back_when_it_closes() {
    let mut world = start(vec![four_in_a_row("a")]);
    arrange(&mut world, "work/desktop_1", vec![four_in_a_row("b")]);
    assert_eq!(shown(&world), ["a", "b"]);

    let arrived = row([
        pane("a"),
        pane("b"),
        document("doc", "b"),
        pane("c"),
        pane("d"),
    ]);
    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "b", Some(arrived))],
    );
    assert_eq!(shown(&world), ["b"], "the screen slides to show it");
    assert!(shows(&world, "doc.md"));
    assert_eq!(chip(&world, "a").unwrap().value.as_deref(), Some("arrived"));

    arrange(&mut world, "work/desktop_1", vec![four_in_a_row("b")]);
    assert_eq!(
        shown(&world),
        ["a", "b"],
        "the screen goes back once it closes"
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_chip_brings_its_column_beside_the_active_tile_and_the_anchor_keeps_its_place() {
    let mut world = start(vec![four_in_a_row("a")]);

    click(&mut world, "shelf.chip.d");
    assert_eq!(
        sent(&world, "desktop_move_leaf"),
        [json(
            r#"{"source_desktop_id":"work/desktop_1","target_desktop_id":"work/desktop_1","leaf_id":"d",
                "anchor_id":"a","edge":"right","whole_column":true}"#
        )]
    );
    let moved = row([pane("a"), pane("d"), pane("b"), pane("c")]);
    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "d", Some(moved))],
    );
    assert!(shows(&world, "Agent a") && shows(&world, "Agent d"));
    assert!(chip(&world, "b").is_some() && chip(&world, "c").is_some());

    click(&mut world, "shelf.chip.c");
    assert_eq!(
        sent(&world, "desktop_move_leaf")[1]["edge"],
        "left",
        "beside the rightmost tile the column lands on its left"
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_dot_shows_its_column_where_it_is() {
    let mut world = start(vec![four_in_a_row("a")]);

    click(&mut world, "shelf.dot.b");
    assert_eq!(
        sent(&world, "desktop_show_leaf"),
        [json(r#"{"desktop_id":"work/desktop_1","leaf_id":"b"}"#)]
    );
    assert!(sent(&world, "desktop_move_leaf").is_empty());
}

#[test]
fn the_screen_keeps_where_it_starts_while_the_active_tile_stays_on_it() {
    let mut world = start(vec![four_in_a_row("a")]);

    arrange(&mut world, "work/desktop_1", vec![four_in_a_row("c")]);
    assert!(shows(&world, "Agent b") && shows(&world, "Agent c"));
    arrange(&mut world, "work/desktop_1", vec![four_in_a_row("b")]);
    assert!(
        shows(&world, "Agent b") && shows(&world, "Agent c"),
        "the screen does not slide back to a"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn chips_past_the_title_bars_room_wait_in_a_cap_whose_menu_escape_closes() {
    let ids: Vec<String> = (0..24).map(|i| format!("p{i:02}")).collect();
    let tree = row(ids.iter().map(|id| pane(id)));
    let mut world = start(vec![desktop(1, "p00", Some(tree))]);

    let cap = world.screen().get(Target::key("shelf.cap.after"));
    let held: usize = cap
        .name
        .as_deref()
        .unwrap()
        .split(' ')
        .next()
        .unwrap()
        .parse()
        .unwrap();
    let chips = (2..24)
        .filter(|i| chip(&world, &format!("p{i:02}")).is_some())
        .count();
    assert_eq!(
        chips + held,
        22,
        "every column off screen is a chip or in the cap"
    );
    assert!(
        chip(&world, "p02").is_some(),
        "the chips nearest the screen stay out"
    );

    click(&mut world, "shelf.cap.after");
    let menu = world.screen().get(Target::key("shelf.menu.after"));
    assert_eq!(menu.name, cap.name);
    assert!(
        chip(&world, "p23").is_some(),
        "the menu holds the far chips"
    );

    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        })
    });
    assert!(
        world
            .screen()
            .find(Target::key("shelf.menu.after"))
            .is_none()
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn carrying_moves_the_active_column_one_place_and_says_when_it_is_at_the_end() {
    let mut world = start(vec![four_in_a_row("a")]);

    press(&mut world, Action::ShelfCarryRight);
    assert_eq!(
        sent(&world, "desktop_move_leaf"),
        [json(
            r#"{"source_desktop_id":"work/desktop_1","target_desktop_id":"work/desktop_1","leaf_id":"a",
                "anchor_id":"b","edge":"right","whole_column":true}"#
        )]
    );

    press(&mut world, Action::ShelfCarryLeft);
    assert_eq!(
        notice(&world).as_deref(),
        Some("This tile is already at the start of the row.")
    );
    assert_eq!(sent(&world, "desktop_move_leaf").len(), 1);
}

#[test]
fn sending_to_the_shelf_unstacks_holds_or_moves_the_tile_and_shows_the_next() {
    let mut world = start(vec![desktop(1, "a", Some(pane("a")))]);
    press(&mut world, Action::ShelfSendToShelf);
    assert_eq!(
        notice(&world).as_deref(),
        Some("This is the only tile on screen.")
    );

    let stacked = row([below(pane("a"), pane("b")), pane("c")]);
    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "a", Some(stacked))],
    );
    press(&mut world, Action::ShelfSendToShelf);
    assert_eq!(
        sent(&world, "desktop_show_leaf"),
        [json(r#"{"desktop_id":"work/desktop_1","leaf_id":"b"}"#)]
    );
    assert_eq!(
        sent(&world, "desktop_move_leaf"),
        [json(
            r#"{"source_desktop_id":"work/desktop_1","target_desktop_id":"work/desktop_1","leaf_id":"a",
                "anchor_id":"c","edge":"right","activate":false}"#
        )]
    );

    arrange(&mut world, "work/desktop_1", vec![four_in_a_row("a")]);
    press(&mut world, Action::ShelfSendToShelf);
    assert_eq!(sent(&world, "desktop_show_leaf")[1]["leaf_id"], "b");
    assert_eq!(
        sent(&world, "desktop_move_leaf")[1],
        json(
            r#"{"source_desktop_id":"work/desktop_1","target_desktop_id":"work/desktop_1","leaf_id":"a",
                "anchor_id":"d","edge":"right","whole_column":true,"activate":false}"#
        )
    );

    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "a", Some(row([pane("a"), pane("b")])))],
    );
    press(&mut world, Action::ShelfSendToShelf);
    assert_eq!(sent(&world, "desktop_show_leaf")[2]["leaf_id"], "b");
    assert_eq!(
        sent(&world, "desktop_move_leaf").len(),
        2,
        "a held tile stays where it is in the row"
    );
    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "b", Some(row([pane("a"), pane("b")])))],
    );
    assert!(!shows(&world, "Agent a"), "the window holds a off screen");
    assert!(chip(&world, "a").is_some());
    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "a", Some(row([pane("a"), pane("b")])))],
    );
    assert!(
        shows(&world, "Agent a") && shows(&world, "Agent b"),
        "a held tile comes back when it is active"
    );
    world.advance(Duration::from_secs(10));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_shelf_hides_in_focus_mode_and_on_home() {
    let mut world = start(vec![four_in_a_row("a")]);
    assert!(world.screen().find(Target::key("shelf")).is_some());

    press(&mut world, Action::TerminalToggleMaximize);
    assert!(world.screen().find(Target::key("shelf")).is_none());
    press(&mut world, Action::TerminalToggleMaximize);
    press(&mut world, Action::TerminalToggleZoom);
    assert!(
        world.screen().find(Target::key("shelf")).is_some(),
        "zoom keeps the shelf"
    );
}

fn width_label(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("shelf.width.label"))
        .and_then(|n| n.name)
}

#[test]
fn fewer_and_more_tiles_step_the_narrowest_width_and_say_where_they_stop() {
    let mut world = start(vec![four_in_a_row("a")]);

    press(&mut world, Action::ShelfFewerTiles);
    assert_eq!(
        sent(&world, "set_setting"),
        [json(r#"{"key":"narrowest_tile_cols","value":"76"}"#)],
        "76 columns is the first step that shows fewer than two tiles"
    );
    assert_eq!(width_label(&world).as_deref(), Some("2 tiles"));
    world.advance(Duration::from_millis(1500));
    assert_eq!(width_label(&world), None);

    press(&mut world, Action::ShelfMoreTiles);
    assert_eq!(
        notice(&world).as_deref(),
        Some(
            "3 tiles at the narrowest width (57 cols, 513px) need 1539px. Tiles have 1348px here. Closing the sidebar would make room."
        )
    );
    assert_eq!(sent(&world, "set_setting").len(), 1);
    world.advance(Duration::from_secs(10));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn more_tiles_forgets_a_dragged_width_when_only_it_keeps_a_tile_off_screen() {
    let mut wide = pane("b");
    wide["wanted_cols"] = 120.into();
    let tree = row([pane("a"), wide, pane("c")]);
    let mut world = start(vec![desktop(1, "a", Some(tree))]);
    assert!(
        !shows(&world, "Agent b"),
        "b's dragged width does not fit beside a"
    );

    press(&mut world, Action::ShelfMoreTiles);
    assert_eq!(
        sent(&world, "desktop_set_wanted_cols"),
        [json(
            r#"{"desktop_id":"work/desktop_1","leaf_id":"b","cols":0,"area_cols":0}"#
        )]
    );
    assert!(
        sent(&world, "set_setting").is_empty(),
        "the width it steps to is the one it has"
    );
    assert!(shows(&world, "Agent a") && shows(&world, "Agent b"));
    let label = world.screen().get(Target::key("shelf.width.label"));
    assert_eq!(label.name.as_deref(), Some("2 tiles"));
    assert!(
        world
            .screen()
            .to_string()
            .contains("Cleared the dragged width")
    );
}

/// Wide enough for three tiles at the narrowest width, and too narrow for four.
const THREE_FIT: (f32, f32) = (2100.0, 900.0);

fn five_in_a_row(active: &str) -> Desktop {
    desktop(1, active, Some(row(["a", "b", "c", "d", "e"].map(pane))))
}

fn shown(world: &World) -> Vec<String> {
    ["a", "b", "c", "d", "e"]
        .into_iter()
        .filter(|id| shows(world, &format!("Agent {id}")))
        .map(str::to_owned)
        .collect()
}

#[test]
fn a_column_brought_from_before_the_screen_lands_beside_the_anchor_which_keeps_its_place() {
    let mut world = World::start_sized(daemon(vec![five_in_a_row("a")]), THREE_FIT);
    world.open_desktop();
    arrange(&mut world, "work/desktop_1", vec![five_in_a_row("d")]);
    arrange(&mut world, "work/desktop_1", vec![five_in_a_row("c")]);
    assert_eq!(shown(&world), ["b", "c", "d"]);

    click(&mut world, "shelf.chip.a");
    assert_eq!(sent(&world, "desktop_move_leaf")[0]["anchor_id"], "c");
    let moved = row(["b", "c", "a", "d", "e"].map(pane));
    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "a", Some(moved))],
    );

    assert_eq!(shown(&world), ["a", "b", "c"]);
    let x = |leaf: &str| {
        world
            .runtime()
            .presented(&format!("tile.{leaf}"))
            .unwrap()
            .0[0]
    };
    assert!(
        x("b") < x("c") && x("c") < x("a"),
        "b and c keep their slots and a lands after c"
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_screen_returns_to_where_it_started_after_a_width_change() {
    let mut world = start(vec![four_in_a_row("a")]);
    arrange(&mut world, "work/desktop_1", vec![four_in_a_row("b")]);
    assert_eq!(shown(&world), ["a", "b"]);

    let narrowest = |world: &mut World, cols: &str| {
        world.daemon.emit(attn_protocol::SettingsUpdatedMessage {
            settings: Some([("narrowest_tile_cols".to_owned(), cols.to_owned())].into()),
            ..attn_protocol::SettingsUpdatedMessage::new()
        });
        world.settle();
    };
    narrowest(&mut world, "76");
    assert_eq!(shown(&world), ["b"]);
    narrowest(&mut world, "66");
    assert_eq!(shown(&world), ["a", "b"]);
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_caps_menu_closes_on_a_press_outside_it_and_on_another_desktop() {
    let ids: Vec<String> = (0..24).map(|i| format!("p{i:02}")).collect();
    let tree = row(ids.iter().map(|id| pane(id)));
    let other = {
        let mut d = desktop(2, "p00", Some(row(ids.iter().map(|id| pane(id)))));
        d.panes.iter_mut().for_each(|p| p.desktop_id = d.id.clone());
        d
    };
    let mut world = start(vec![desktop(1, "p00", Some(tree.clone())), other.clone()]);

    click(&mut world, "shelf.cap.after");
    click(&mut world, "shelf.menu.layer");
    assert!(
        world
            .screen()
            .find(Target::key("shelf.menu.after"))
            .is_none()
    );

    click(&mut world, "shelf.cap.after");
    arrange(
        &mut world,
        "work/desktop_2",
        vec![desktop(1, "p00", Some(tree)), other],
    );
    assert!(
        world
            .screen()
            .find(Target::key("shelf.menu.after"))
            .is_none(),
        "the menu does not follow the window to another desktop"
    );
    assert!(world.runtime_is_at_rest());
}

/// A point in `leaf`'s tile at fractions `fx`, `fy` of its width and height.
fn in_tile(world: &World, leaf: &str, fx: f32, fy: f32) -> (f32, f32) {
    let [x, y, w, h] = world
        .runtime()
        .presented(&format!("tile.{leaf}"))
        .unwrap_or_else(|| panic!("{leaf} is drawn"))
        .0;
    (x + w * fx, y + h * fy)
}

fn drag(world: &mut World, from: &str, to: (f32, f32)) {
    let from = from.to_owned();
    world.gesture(|app| app.drag(Target::key(from), to));
}

#[test]
fn a_header_dragged_onto_a_tiles_edge_docks_the_tile_there_taking_the_band_it_showed() {
    let mut world = start(vec![four_in_a_row("a")]);

    let to = in_tile(&world, "b", 0.75, 0.5);
    world.gesture(|app| app.drag_over(Target::key("tile.a.header"), to));
    assert_eq!(
        world
            .screen()
            .get(Target::key("drag.ghost"))
            .name
            .as_deref(),
        Some("Agent a")
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("drag.target"))
            .name
            .as_deref(),
        Some("b right")
    );
    world.gesture(|app| app.release(to));

    assert_eq!(
        sent(&world, "desktop_move_leaf"),
        [json(
            r#"{"source_desktop_id":"work/desktop_1","target_desktop_id":"work/desktop_1","leaf_id":"a",
                "anchor_id":"b","edge":"right","leaf_share":0.25}"#
        )]
    );
    assert!(world.screen().find(Target::key("drag.ghost")).is_none());
    assert!(world.runtime_is_at_rest());
}

#[test]
fn escape_cancels_a_drag_and_a_press_that_does_not_travel_is_a_click() {
    let mut world = start(vec![four_in_a_row("a")]);

    let to = in_tile(&world, "b", 0.75, 0.5);
    world.gesture(|app| {
        app.drag_over(Target::key("tile.a.header"), to);
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        });
        app.release(to);
    });
    assert!(sent(&world, "desktop_move_leaf").is_empty());
    assert!(world.screen().find(Target::key("drag.ghost")).is_none());

    click(&mut world, "tile.b.header");
    assert_eq!(
        sent(&world, "desktop_show_leaf"),
        [json(r#"{"desktop_id":"work/desktop_1","leaf_id":"b"}"#)]
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_chip_dragged_onto_a_tiles_edge_brings_its_whole_column_and_lands_beside_a_tile_too_short_to_stack_on()
 {
    let tree = row([pane("a"), pane("b"), below(pane("c"), pane("e"))]);
    let mut world = start(vec![desktop(1, "a", Some(tree))]);

    let to = in_tile(&world, "b", 0.5, 0.1);
    drag(&mut world, "shelf.chip.c", to);

    let moves = sent(&world, "desktop_move_leaf");
    assert_eq!(moves.len(), 1);
    assert_eq!(moves[0]["anchor_id"], "b");
    assert_eq!(
        moves[0]["edge"], "right",
        "a stack of two needs 960px over b"
    );
    assert_eq!(moves[0]["whole_column"], true);
    let said = notice(&world).unwrap();
    assert!(
        said.starts_with(
            "Stacking here needs 960px to keep every tile at the 320px minimum. This tile has "
        ),
        "{said}"
    );
    assert!(said.ends_with("px, so it moved beside instead."), "{said}");
}

#[test]
fn a_chip_dropped_in_a_tiles_middle_takes_its_place_and_the_tile_takes_the_chips_place() {
    let mut world = start(vec![four_in_a_row("a")]);

    let to = in_tile(&world, "b", 0.5, 0.5);
    drag(&mut world, "shelf.chip.d", to);
    assert_eq!(
        sent(&world, "desktop_move_leaf"),
        [
            json(
                r#"{"source_desktop_id":"work/desktop_1","target_desktop_id":"work/desktop_1","leaf_id":"d",
                    "anchor_id":"b","edge":"left","whole_column":true}"#
            ),
            json(
                r#"{"source_desktop_id":"work/desktop_1","target_desktop_id":"work/desktop_1","leaf_id":"b",
                    "anchor_id":"c","edge":"right","whole_column":true,"activate":false}"#
            ),
        ]
    );

    let first = row(["a", "d", "b", "c"].map(pane));
    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "d", Some(first))],
    );
    assert_eq!(
        shown(&world),
        ["a", "d"],
        "b waits off screen while it moves"
    );
    let landed = row(["a", "d", "c", "b"].map(pane));
    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "d", Some(landed))],
    );
    assert_eq!(shown(&world), ["a", "d"]);
    assert!(
        chip(&world, "b").is_some(),
        "b is back on the shelf where d was"
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_header_dropped_on_a_chip_moves_the_tile_beside_that_column() {
    let mut world = start(vec![four_in_a_row("a")]);

    let [x, y, w, _] = world.runtime().presented("shelf.chip.d").unwrap().0;
    drag(&mut world, "tile.b.header", (x + w * 0.75, y + 4.0));
    assert_eq!(
        sent(&world, "desktop_move_leaf"),
        [json(
            r#"{"source_desktop_id":"work/desktop_1","target_desktop_id":"work/desktop_1","leaf_id":"b",
                "anchor_id":"d","edge":"right"}"#
        )]
    );
    assert_eq!(
        sent(&world, "desktop_show_leaf"),
        [json(r#"{"desktop_id":"work/desktop_1","leaf_id":"b"}"#)],
        "the dropped tile becomes the active one"
    );
}

#[test]
fn a_chip_dropped_on_another_chip_reorders_the_shelf_without_changing_the_screen() {
    let mut world = start(vec![four_in_a_row("a")]);

    let [x, y, w, _] = world.runtime().presented("shelf.chip.d").unwrap().0;
    drag(&mut world, "shelf.chip.c", (x + w * 0.75, y + 4.0));
    assert_eq!(
        sent(&world, "desktop_move_leaf"),
        [json(
            r#"{"source_desktop_id":"work/desktop_1","target_desktop_id":"work/desktop_1","leaf_id":"c",
                "anchor_id":"d","edge":"right","whole_column":true,"activate":false}"#
        )]
    );
}

#[test]
fn a_drag_ends_when_the_tile_it_drags_leaves_the_desktop() {
    let mut world = start(vec![four_in_a_row("a")]);

    let to = in_tile(&world, "b", 0.75, 0.5);
    world.gesture(|app| app.drag_over(Target::key("tile.a.header"), to));
    assert!(world.screen().find(Target::key("drag.ghost")).is_some());
    let without_a = row(["b", "c", "d"].map(pane));
    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "b", Some(without_a))],
    );

    assert!(world.screen().find(Target::key("drag.ghost")).is_none());
    world.gesture(|app| app.release(to));
    assert!(sent(&world, "desktop_move_leaf").is_empty());
}

#[test]
fn an_agent_alone_on_its_desktop_does_not_drag() {
    let mut world = start(vec![desktop(1, "a", Some(pane("a")))]);

    let to = in_tile(&world, "a", 0.9, 0.5);
    world.gesture(|app| app.drag_over(Target::key("tile.a.header"), to));
    assert!(world.screen().find(Target::key("drag.ghost")).is_none());
    world.gesture(|app| app.release(to));
    assert!(sent(&world, "desktop_move_leaf").is_empty());
}

fn two_desktops() -> Vec<Desktop> {
    vec![
        four_in_a_row("a"),
        desktop(2, "x", Some(row([pane("x"), pane("y")]))),
    ]
}

/// The middle of desktop `slot`'s row in the sidebar.
fn in_sidebar(world: &World, slot: u32) -> (f32, f32) {
    let [x, y, w, h] = world
        .runtime()
        .presented(&format!("sidebar.desktop.work/desktop_{slot}"))
        .unwrap_or_else(|| panic!("desktop {slot} is in the sidebar"))
        .0;
    (x + w / 2.0, y + h / 2.0)
}

#[test]
fn a_tile_dropped_on_another_desktop_in_the_sidebar_moves_to_its_left_with_delegates_on_option() {
    let mut world = start(two_desktops());

    let own = in_sidebar(&world, 1);
    drag(&mut world, "tile.a.header", own);
    assert!(
        sent(&world, "desktop_move_leaf").is_empty(),
        "the desktop a tile comes from takes no drop"
    );

    let other = in_sidebar(&world, 2);
    drag(&mut world, "tile.a.header", other);
    world.gesture(|app| {
        app.hold(rune::Mods {
            alt: true,
            ..rune::Mods::default()
        });
        app.drag(Target::key("tile.b.header"), other);
    });
    world.gesture(|app| app.hold(rune::Mods::default()));
    assert_eq!(
        sent(&world, "desktop_move_leaf"),
        [
            json(
                r#"{"source_desktop_id":"work/desktop_1","target_desktop_id":"work/desktop_2","leaf_id":"a",
                    "edge":"left","leaf_share":0.32}"#
            ),
            json(
                r#"{"source_desktop_id":"work/desktop_1","target_desktop_id":"work/desktop_2","leaf_id":"b",
                    "edge":"left","leaf_share":0.32,"with_delegates":true}"#
            ),
        ],
        "Option at the drop takes the agent's delegates along"
    );
    assert_eq!(
        sent(&world, "desktop_set_current")[0],
        json(r#"{"profile_id":"work","desktop_id":"work/desktop_2"}"#)
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_drag_resting_on_a_sidebar_desktop_switches_to_it_and_lands_on_its_tiles() {
    let mut world = start(two_desktops());

    let over = in_sidebar(&world, 2);
    world.gesture(|app| app.drag_over(Target::key("tile.a.header"), over));
    world.advance(Duration::from_millis(200));
    assert!(sent(&world, "desktop_set_current").is_empty());
    world.advance(Duration::from_millis(200));
    assert_eq!(
        sent(&world, "desktop_set_current"),
        [json(
            r#"{"profile_id":"work","desktop_id":"work/desktop_2"}"#
        )]
    );

    arrange(&mut world, "work/desktop_2", two_desktops());
    assert!(shows(&world, "Agent x"));
    assert!(
        world.screen().find(Target::key("drag.ghost")).is_some(),
        "the drag carries on over the desktop it switched to"
    );
    let over = in_sidebar(&world, 2);
    world.gesture(|app| app.move_to((over.0 + 5.0, over.1)));
    world.advance(Duration::from_secs(1));
    assert_eq!(
        sent(&world, "desktop_set_current").len(),
        1,
        "resting on the desktop the window shows switches nothing"
    );
    let to = in_tile(&world, "y", 0.9, 0.5);
    world.gesture(|app| {
        app.move_to(to);
        app.release(to);
    });

    let moved = sent(&world, "desktop_move_leaf");
    assert_eq!(moved.len(), 1);
    assert_eq!(moved[0]["source_desktop_id"], "work/desktop_1");
    assert_eq!(moved[0]["target_desktop_id"], "work/desktop_2");
    assert_eq!(moved[0]["leaf_id"], "a");
    assert_eq!(moved[0]["anchor_id"], "y");
    assert_eq!(moved[0]["edge"], "right");
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_tile_dropped_on_new_desktop_moves_to_a_desktop_made_for_it_and_the_window_shows_it() {
    let mut world = start(two_desktops());
    world.daemon.on("desktop_move_leaf", |_| {
        let mut made = desktop(3, "a", Some(pane("a")));
        made.panes = vec![agent("work/desktop_3", "a")];
        serde_json::json!({
            "event": "profile_action_result",
            "action": "desktop_move_leaf",
            "success": true,
            "desktops": [desktop(1, "b", Some(row([pane("b"), pane("c"), pane("d")]))), made],
            "pane_id": "a",
        })
    });
    let new_desktop = Target::key("sidebar.new_desktop");
    assert!(world.screen().find(new_desktop.clone()).is_none());

    let near = in_tile(&world, "b", 0.5, 0.5);
    world.gesture(|app| app.drag_over(Target::key("tile.a.header"), near));
    let [x, y, w, h] = world
        .runtime()
        .presented("sidebar.new_desktop")
        .expect("a drag shows the sidebar's drop for a new desktop")
        .0;
    let to = (x + w / 2.0, y + h / 2.0);
    world.gesture(|app| {
        app.move_to(to);
        app.release(to);
    });
    world.settle();

    assert_eq!(
        sent(&world, "desktop_move_leaf"),
        [json(
            r#"{"source_desktop_id":"work/desktop_1","leaf_id":"a","edge":"left","leaf_share":0.32}"#
        )]
    );
    assert_eq!(
        sent(&world, "desktop_set_current"),
        [json(
            r#"{"profile_id":"work","desktop_id":"work/desktop_3"}"#
        )]
    );
    assert!(world.screen().find(new_desktop).is_none());
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

/// The middle of the element `key` names.
fn middle(world: &World, key: &str) -> (f32, f32) {
    let [x, y, w, h] = world
        .runtime()
        .presented(key)
        .unwrap_or_else(|| panic!("{key} is drawn"))
        .0;
    (x + w / 2.0, y + h / 2.0)
}

#[test]
fn in_queue_flow_desktop_chips_take_a_dragged_tile_in_the_sidebar_and_in_the_title_bar() {
    let desktops = two_desktops();
    let sessions = desktops
        .iter()
        .flat_map(|d| d.panes.iter())
        .map(|p| session(&p.pane_id))
        .collect();
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![profile("work/desktop_1")]),
        desktops: Some(desktops),
        sessions: Some(sessions),
        settings: Some(
            [("queue_mode_enabled".to_owned(), "true".to_owned())]
                .into_iter()
                .collect(),
        ),
        ..InitialStateMessage::new()
    });
    let mut world = World::start_sized(daemon, TWO_FIT);
    world.gesture(|app| app.click(Target::key("queue.desktop.1")));
    world.settle();
    assert!(shows(&world, "Agent a"));
    let opened = world.daemon.sent_of("desktop_set_current").len();

    let chip = middle(&world, "queue.desktop.2");
    drag(&mut world, "tile.a.header", chip);
    assert_eq!(
        sent(&world, "desktop_move_leaf"),
        [json(
            r#"{"source_desktop_id":"work/desktop_1","target_desktop_id":"work/desktop_2","leaf_id":"a",
                "edge":"left","leaf_share":0.32}"#
        )]
    );

    world.gesture(|app| app.click(Target::role("button").name("Collapse sidebar")));
    let chip = middle(&world, "queue-bar.desktop.2");
    world.gesture(|app| app.drag_over(Target::key("tile.b.header"), chip));
    world.advance(Duration::from_millis(400));
    let switched = world.daemon.sent_of("desktop_set_current");
    assert_eq!(
        switched.len(),
        opened + 2,
        "the drop and then the rest on the chip each switch to desktop 2"
    );
    assert_eq!(switched[opened + 1]["desktop_id"], "work/desktop_2");
    world.gesture(|app| app.release(chip));
    assert_eq!(sent(&world, "desktop_move_leaf")[1]["leaf_id"], "b");
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.new_desktop"))
            .is_none(),
        "only the desktop sidebar has a drop for a new desktop"
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_swapped_out_tile_comes_back_when_the_row_changes_without_the_swap() {
    let mut world = start(vec![four_in_a_row("a")]);

    let to = in_tile(&world, "b", 0.5, 0.5);
    drag(&mut world, "shelf.chip.c", to);
    assert_eq!(
        sent(&world, "desktop_move_leaf").len(),
        1,
        "beside its neighbour the chip needs one move"
    );
    assert!(
        !shows(&world, "Agent b"),
        "b holds off screen while c takes its place"
    );

    let refused = row(["a", "b", "c", "d", "e"].map(pane));
    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "a", Some(refused))],
    );
    assert!(
        shows(&world, "Agent b"),
        "b is no longer held once the row changed without c moving"
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

fn divider_at(world: &World, split: &str) -> (f32, f32) {
    let [x, y, w, h] = world
        .runtime()
        .presented(&format!("divider.{split}"))
        .unwrap_or_else(|| panic!("the divider of {split} is drawn"))
        .0;
    (x + w / 2.0, y + h / 2.0)
}

#[test]
fn dragging_a_column_divider_widens_one_column_at_its_neighbours_cost_and_saves_both_widths() {
    let mut world = start(vec![four_in_a_row("a")]);
    assert_eq!(width(&world, "a"), width(&world, "b"));

    let (x, y) = divider_at(&world, "row:a");
    world.gesture(|app| {
        app.drag_from((x, y), (x + 50.0, y));
        app.release((x + 50.0, y));
    });

    assert_eq!(
        sent(&world, "desktop_set_wanted_cols"),
        [
            json(r#"{"desktop_id":"work/desktop_1","leaf_id":"a","cols":80,"area_cols":149}"#),
            json(r#"{"desktop_id":"work/desktop_1","leaf_id":"b","cols":69,"area_cols":149}"#),
        ]
    );
    assert!(width(&world, "a") > width(&world, "b") + 90.0);
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn escape_puts_a_column_divider_back_and_saves_nothing() {
    let mut world = start(vec![four_in_a_row("a")]);
    let before = width(&world, "a");

    let (x, y) = divider_at(&world, "row:a");
    world.gesture(|app| app.drag_from((x, y), (x + 50.0, y)));
    assert!(
        width(&world, "a") > before,
        "the columns follow the divider"
    );
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        });
        app.release((x + 50.0, y));
    });

    assert!(sent(&world, "desktop_set_wanted_cols").is_empty());
    assert_eq!(width(&world, "a"), before);
}

#[test]
fn a_double_click_on_a_column_divider_forgets_the_dragged_widths_beside_it() {
    let mut wide = pane("a");
    wide["wanted_cols"] = 70.into();
    let mut world = start(vec![desktop(
        1,
        "a",
        Some(row([wide, pane("b"), pane("c")])),
    )]);

    let at = divider_at(&world, "row:a");
    world.gesture(|app| {
        app.click_at(at);
        app.click_at(at);
    });

    assert_eq!(
        sent(&world, "desktop_set_wanted_cols"),
        [json(
            r#"{"desktop_id":"work/desktop_1","leaf_id":"a","cols":0,"area_cols":0}"#
        )]
    );
    assert_eq!(width(&world, "a"), width(&world, "b"));
}

#[test]
fn dragging_a_stacks_divider_sets_its_split_ratio_once_let_go() {
    let tree = row([below(pane("a"), pane("c")), pane("b")]);
    let mut world = start(vec![desktop(1, "a", Some(tree))]);
    let height = |world: &World, leaf: &str| {
        world
            .runtime()
            .presented(&format!("tile.{leaf}"))
            .unwrap()
            .0[3]
    };
    assert_eq!(height(&world, "a"), height(&world, "c"));

    let (x, y) = divider_at(&world, "horizontal:a");
    world.gesture(|app| app.drag_from((x, y), (x, y + 100.0)));
    assert!(
        sent(&world, "desktop_set_split_ratio").is_empty(),
        "nothing goes out while it moves"
    );
    assert!(height(&world, "a") > height(&world, "c"));
    world.gesture(|app| app.release((x, y + 100.0)));

    let ratios = sent(&world, "desktop_set_split_ratio");
    assert_eq!(ratios.len(), 1);
    assert_eq!(ratios[0]["split_id"], "horizontal:a");
    let ratio = ratios[0]["ratio"].as_f64().unwrap();
    assert!(ratio > 0.55 && ratio < 0.65, "{ratio}");
    assert!(
        height(&world, "a") > height(&world, "c"),
        "the stack keeps the dragged ratio until the daemon has it"
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_column_drag_that_shelves_the_first_column_follows_the_pointer_back_and_saves_on_release() {
    let three = || desktop(1, "c", Some(row(["a", "b", "c"].map(pane))));
    let mut world = World::start_sized(daemon(vec![three()]), THREE_FIT);
    world.open_desktop();
    assert_eq!(shown(&world), ["a", "b", "c"]);
    let (x, y) = divider_at(&world, "row:b");

    world.gesture(|app| app.drag_from((x, y), (x - 250.0, y)));
    assert_eq!(shown(&world), ["b", "c"], "a leaves for the shelf");
    world.gesture(|app| {
        app.move_to((x, y));
        app.release((x, y));
    });
    assert_eq!(shown(&world), ["a", "b", "c"], "dragged back, a returns");
    assert!(sent(&world, "desktop_set_wanted_cols").is_empty());

    world.gesture(|app| {
        app.drag_from((x, y), (x - 250.0, y));
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        });
        app.release((x - 250.0, y));
    });
    assert_eq!(shown(&world), ["a", "b", "c"], "Escape puts a back");
    assert!(sent(&world, "desktop_set_wanted_cols").is_empty());

    world.gesture(|app| {
        app.drag_from((x, y), (x - 250.0, y));
        app.release((x - 250.0, y));
    });
    let saved = sent(&world, "desktop_set_wanted_cols");
    let leaves: Vec<&str> = saved
        .iter()
        .map(|w| w["leaf_id"].as_str().unwrap())
        .collect();
    assert_eq!(leaves, ["b", "c"]);
    assert_eq!(shown(&world), ["b", "c"]);
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

/// A daemon that shows each terminal's id as its screen, and confirms each input `confirm`
/// answers for with that answer: success, or the error it carries.
fn peeking(confirm: impl Fn(&str) -> Result<(), String> + Send + 'static) -> World {
    let daemon = daemon(vec![four_in_a_row("a")]);
    daemon.on("terminal_read_text", |request| {
        attn_testkit::message(attn_protocol::TerminalReadTextResultMessage {
            success: true,
            text: Some(format!(
                "screen of {}\n\n",
                request["terminal_id"].as_str().unwrap()
            )),
            ..attn_protocol::TerminalReadTextResultMessage::new()
        })
    });
    daemon.on("pty_input", move |request| {
        let probe = request["probe_id"].as_str()?;
        let answer = confirm(request["data"].as_str().unwrap());
        Some(attn_testkit::message(
            attn_protocol::PtyInputProbeResultMessage {
                id: request["id"].as_str().unwrap().into(),
                probe_id: probe.into(),
                success: answer.is_ok(),
                error: answer.err(),
                ..attn_protocol::PtyInputProbeResultMessage::new()
            },
        ))
    });
    let mut world = World::start_sized(daemon, TWO_FIT);
    world.open_desktop();
    world
}

fn peek(world: &World) -> Option<attn_testkit::Node> {
    world.screen().find(Target::key("shelf.peek"))
}

fn peek_screen(world: &World) -> String {
    world.screen().children(Target::key("shelf.peek.screen"))[0]
        .name
        .clone()
        .unwrap_or_default()
}

fn rest_on(world: &mut World, key: &str) {
    let at = middle(world, key);
    world.gesture(|app| app.move_to(at));
}

fn enter(world: &mut World) {
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Enter,
            mods: rune::Mods::default(),
        })
    });
}

fn escape(world: &mut World) {
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        })
    });
}

fn reply(world: &mut World, words: &str) {
    let words = words.to_owned();
    world.gesture(|app| {
        app.click(Target::key("shelf.peek.c.field"));
        app.type_text(&words);
    });
}

/// What the app typed into the terminal `runtime` for the person, in order.
fn typed(world: &World, runtime: &str) -> Vec<String> {
    world
        .daemon
        .sent_of("pty_input")
        .into_iter()
        .filter(|input| input["id"] == runtime && input["source"] == "user")
        .map(|input| input["data"].as_str().unwrap().to_owned())
        .collect()
}

#[test]
fn resting_on_a_chip_peeks_at_its_terminal_and_a_reply_reaches_it_followed_by_return() {
    let mut world = peeking(|_| Ok(()));

    rest_on(&mut world, "shelf.chip.c");
    world.advance(Duration::from_millis(150));
    assert!(peek(&world).is_none(), "the peek waits a moment");
    world.advance(Duration::from_millis(50));
    assert_eq!(peek(&world).unwrap().name.as_deref(), Some("Agent c"));
    assert_eq!(peek_screen(&world), "screen of r-c");
    assert_eq!(shown(&world), ["a", "b"], "the layout stays");

    reply(&mut world, "yes");
    enter(&mut world);

    assert_eq!(typed(&world, "r-c"), ["yes", "\r"]);
    assert!(peek(&world).is_none(), "a sent reply closes the peek");
    assert_eq!(shown(&world), ["a", "b"]);
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_reply_the_terminal_did_not_take_stays_with_its_tile_to_copy_or_discard() {
    let mut world = peeking(|data| match data {
        "\r" => Ok(()),
        _ => Err("the terminal is gone".to_owned()),
    });

    rest_on(&mut world, "shelf.chip.c");
    world.advance(Duration::from_millis(200));
    reply(&mut world, "yes");
    enter(&mut world);
    assert_eq!(typed(&world, "r-c"), ["yes"], "Return waits for the text");

    rest_on(&mut world, "shelf.chip.d");
    rest_on(&mut world, "shelf.chip.c");
    world.advance(Duration::from_millis(200));
    assert_eq!(
        world
            .screen()
            .get(Target::key("shelf.peek.notice"))
            .name
            .as_deref(),
        Some(
            "This reply may not have arrived: the terminal is gone. Check the screen; ↵ sends it again."
        )
    );

    click(&mut world, "shelf.peek.open");
    arrange(&mut world, "work/desktop_1", vec![four_in_a_row("c")]);
    assert!(shows(&world, "Agent c"));
    assert!(
        world
            .screen()
            .find(Target::key("tile.unsent.r-c"))
            .is_some(),
        "the tile keeps the unsent reply"
    );
    click(&mut world, "tile.unsent.r-c.discard");
    assert!(
        world
            .screen()
            .find(Target::key("tile.unsent.r-c"))
            .is_none()
    );
    world.advance(Duration::from_secs(7));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_peek_closes_when_the_pointer_leaves_and_escape_forgets_a_draft() {
    let mut world = peeking(|_| Ok(()));

    rest_on(&mut world, "shelf.chip.c");
    world.advance(Duration::from_millis(200));
    rest_on(&mut world, "tile.a");
    world.advance(Duration::from_millis(200));
    assert!(peek(&world).is_none(), "leaving the chip closes the peek");

    rest_on(&mut world, "shelf.chip.c");
    world.advance(Duration::from_millis(200));
    reply(&mut world, "draft");
    rest_on(&mut world, "tile.a");
    world.advance(Duration::from_millis(200));
    assert!(peek(&world).is_some(), "a peek being replied in stays");

    escape(&mut world);
    assert!(peek(&world).is_none());
    rest_on(&mut world, "shelf.chip.c");
    world.advance(Duration::from_millis(200));
    assert_eq!(
        world
            .screen()
            .get(Target::key("shelf.peek.c.field"))
            .value
            .as_deref()
            .unwrap_or(""),
        "",
        "Escape forgot the draft"
    );
    escape(&mut world);
    assert!(typed(&world, "r-c").is_empty());
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn resting_on_a_tiles_mark_on_a_stacked_chip_peeks_at_that_tile() {
    let tree = row([pane("a"), pane("b"), below(pane("c"), pane("e")), pane("d")]);
    let mut world = start(vec![desktop(1, "a", Some(tree))]);

    rest_on(&mut world, "shelf.chip.c.member.1");
    world.advance(Duration::from_millis(200));
    assert_eq!(peek(&world).unwrap().name.as_deref(), Some("Agent e"));

    rest_on(&mut world, "shelf.chip.c.member.0");
    world.advance(Duration::from_millis(60));
    assert_eq!(
        peek(&world).unwrap().name.as_deref(),
        Some("Agent c"),
        "a peek already open switches sooner"
    );
    escape(&mut world);
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

fn confirm(world: &World, input: &attn_testkit::Request) {
    world.daemon.reply_to(
        input,
        attn_protocol::PtyInputProbeResultMessage {
            id: input["id"].as_str().unwrap().into(),
            probe_id: input["probe_id"].as_str().unwrap().into(),
            success: true,
            ..attn_protocol::PtyInputProbeResultMessage::new()
        },
    );
}

#[test]
fn a_peek_opened_while_its_reply_is_on_its_way_empties_once_it_arrives_and_sends_nothing_again() {
    let daemon = daemon(vec![four_in_a_row("a")]);
    daemon.answer_in_turn(
        "pty_input",
        [attn_testkit::Answer::Hold, attn_testkit::Answer::Hold],
    );
    let mut world = World::start_sized(daemon, TWO_FIT);
    world.open_desktop();

    rest_on(&mut world, "shelf.chip.c");
    world.advance(Duration::from_millis(200));
    reply(&mut world, "yes");
    enter(&mut world);
    rest_on(&mut world, "shelf.chip.d");
    rest_on(&mut world, "shelf.chip.c");
    world.advance(Duration::from_millis(200));
    assert_eq!(
        world
            .screen()
            .get(Target::key("shelf.peek.notice"))
            .name
            .as_deref(),
        Some("Sending…")
    );

    let text = world.daemon.sent_of("pty_input")[0].clone();
    confirm(&world, &text);
    world.settle();
    let enter_key = world.daemon.sent_of("pty_input")[1].clone();
    confirm(&world, &enter_key);
    world.settle();

    assert_eq!(
        world
            .screen()
            .get(Target::key("shelf.peek.c.field"))
            .value
            .as_deref()
            .unwrap_or(""),
        "",
        "the field empties once the reply arrived"
    );
    world.gesture(|app| app.click(Target::key("shelf.peek.c.field")));
    enter(&mut world);
    assert_eq!(
        typed(&world, "r-c"),
        ["yes", "\r"],
        "Return in the empty field sends nothing"
    );
    assert!(peek(&world).is_none(), "it opens the tile instead");
    assert_eq!(sent(&world, "desktop_show_leaf").len(), 1);
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_peek_being_replied_in_stays_while_the_pointer_crosses_another_chip() {
    let mut world = peeking(|_| Ok(()));

    rest_on(&mut world, "shelf.chip.c");
    world.advance(Duration::from_millis(200));
    reply(&mut world, "for c");
    rest_on(&mut world, "shelf.chip.d");
    world.advance(Duration::from_millis(200));
    assert_eq!(peek(&world).unwrap().name.as_deref(), Some("Agent c"));

    world.gesture(|app| app.type_text(" too"));
    enter(&mut world);
    assert_eq!(typed(&world, "r-c"), ["for c too", "\r"]);
    assert!(typed(&world, "r-d").is_empty());
    assert!(
        typed(&world, "r-a").is_empty(),
        "nothing reaches the tile holding the keyboard"
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

fn key(world: &mut World, key: rune::Key, shift: bool) {
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key,
            mods: rune::Mods {
                shift,
                ..rune::Mods::default()
            },
        })
    });
}

fn overview_card(world: &World, slot: u32) -> attn_testkit::Node {
    world
        .screen()
        .get(Target::key(format!("overview.card.work/desktop_{slot}")))
}

#[test]
fn the_overview_moves_between_desktops_by_arrow_and_switches_or_sends_the_active_tile() {
    let mut world = start(two_desktops());

    press(&mut world, Action::DesktopOverview);
    let dialog = world.screen().get(Target::key("overview.dialog"));
    assert_eq!(dialog.name.as_deref(), Some("Desktop overview"));
    assert_eq!(
        overview_card(&world, 1).value.as_deref(),
        Some("current, focused")
    );

    key(&mut world, rune::Key::Right, false);
    assert_eq!(overview_card(&world, 2).value.as_deref(), Some("focused"));
    key(&mut world, rune::Key::Enter, true);
    let moved = sent(&world, "desktop_move_leaf");
    assert_eq!(moved.len(), 1, "Shift-Return sends the active tile");
    assert_eq!(moved[0]["leaf_id"], "a");
    assert_eq!(moved[0]["target_desktop_id"], "work/desktop_2");
    assert!(
        world
            .screen()
            .find(Target::key("overview.dialog"))
            .is_none()
    );

    press(&mut world, Action::DesktopOverview);
    key(&mut world, rune::Key::Text("2".into()), false);
    let current = sent(&world, "desktop_set_current");
    assert_eq!(
        current.last().unwrap()["desktop_id"],
        "work/desktop_2",
        "a digit switches"
    );
    assert!(
        world
            .screen()
            .find(Target::key("overview.dialog"))
            .is_none()
    );

    press(&mut world, Action::DesktopOverview);
    key(&mut world, rune::Key::Escape, false);
    assert!(
        world
            .screen()
            .find(Target::key("overview.dialog"))
            .is_none()
    );

    press(&mut world, Action::DesktopOverview);
    click(&mut world, "overview.card.work/desktop_1");
    let current = sent(&world, "desktop_set_current");
    assert_eq!(
        current.last().unwrap()["desktop_id"],
        "work/desktop_1",
        "a card switches"
    );
    assert!(
        world
            .screen()
            .find(Target::key("overview.dialog"))
            .is_none()
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_overviews_new_desktop_card_makes_a_desktop_and_switches_to_it() {
    let daemon = daemon(two_desktops());
    daemon.on("desktop_create", |_| {
        attn_testkit::message(attn_protocol::ProfileActionResultMessage {
            success: true,
            desktops: Some(vec![desktop(3, "", None)]),
            ..attn_protocol::ProfileActionResultMessage::new()
        })
    });
    let mut world = World::start_sized(daemon, TWO_FIT);
    world.open_desktop();

    press(&mut world, Action::DesktopOverview);
    click(&mut world, "overview.new");

    assert_eq!(sent(&world, "desktop_create").len(), 1);
    let current = sent(&world, "desktop_set_current");
    assert_eq!(current.last().unwrap()["desktop_id"], "work/desktop_3");
    assert!(
        world
            .screen()
            .find(Target::key("overview.dialog"))
            .is_none()
    );
}

#[test]
fn a_desktop_the_daemon_could_not_make_says_why() {
    let daemon = daemon(two_desktops());
    daemon.on("desktop_create", |_| {
        attn_testkit::message(attn_protocol::ProfileActionResultMessage {
            success: false,
            error: Some("too many desktops".into()),
            ..attn_protocol::ProfileActionResultMessage::new()
        })
    });
    let mut world = World::start_sized(daemon, TWO_FIT);
    world.open_desktop();

    press(&mut world, Action::DesktopOverview);
    click(&mut world, "overview.new");

    assert_eq!(
        world
            .screen()
            .get(Target::key("notice.text"))
            .name
            .as_deref(),
        Some("Could not create a desktop: too many desktops")
    );
    assert!(
        world
            .screen()
            .find(Target::key("overview.dialog"))
            .is_none()
    );
}

fn popped(mut leaf: Value, frame: (f64, f64, f64, f64)) -> Value {
    let (x, y, width, height) = frame;
    leaf["window"] = serde_json::json!({"x": x, "y": y, "width": width, "height": height});
    leaf
}

#[test]
fn the_pop_out_key_pops_the_active_tile_out_where_it_shows_and_brings_it_back() {
    let tree = row([pane("a"), document("m", "a")]);
    let mut world = start(vec![desktop(1, "m", Some(tree))]);
    let [x, y, w, h] = world.runtime().presented("tile.m").unwrap().0;

    press(&mut world, Action::TilePopOut);
    assert_eq!(
        sent(&world, "desktop_pop_out_leaf"),
        [serde_json::json!({
            "desktop_id": "work/desktop_1",
            "leaf_id": "m",
            "frame": {"x": x, "y": y, "width": w, "height": h},
        })]
    );

    let out = row([
        pane("a"),
        popped(document("m", "a"), (10.0, 20.0, 500.0, 400.0)),
    ]);
    arrange(
        &mut world,
        "work/desktop_1",
        vec![desktop(1, "m", Some(out))],
    );
    assert!(shows(&world, "Agent a"));
    assert!(
        !shows(&world, "m.md"),
        "a popped tile leaves the desktop's columns"
    );

    press(&mut world, Action::TilePopOut);
    assert_eq!(
        sent(&world, "desktop_bring_back_leaf"),
        [serde_json::json!({"desktop_id": "work/desktop_1", "leaf_id": "m"})]
    );
    assert_eq!(sent(&world, "desktop_pop_out_leaf").len(), 1);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_tiles_header_button_pops_it_out() {
    let tree = row([pane("a"), document("m", "a")]);
    let mut world = start(vec![desktop(1, "a", Some(tree))]);

    let button = world
        .screen()
        .get(Target::key("tile.m.popout"))
        .name
        .clone();
    assert_eq!(button.as_deref(), Some("Pop out tile (Ctrl+Shift+O)"));
    world.gesture(|app| app.click(Target::key("tile.m.popout")));
    let leaves: Vec<_> = sent(&world, "desktop_pop_out_leaf")
        .into_iter()
        .map(|r| r["leaf_id"].clone())
        .collect();
    assert_eq!(leaves, ["m"]);
    assert!(world.runtime_is_at_rest());
}

const OUT: (f64, f64, f64, f64) = (10.0, 20.0, 500.0, 400.0);

fn frame((x, y, width, height): (f64, f64, f64, f64)) -> attn_app::desktop::layout::Frame {
    attn_app::desktop::layout::Frame {
        x,
        y,
        width,
        height,
    }
}

/// Agent a beside document m, with m popped out at `out` or in its column without.
fn with_document(out: Option<(f64, f64, f64, f64)>) -> Desktop {
    let m = match out {
        Some(at) => popped(document("m", "a"), at),
        None => document("m", "a"),
    };
    desktop(1, "a", Some(row([pane("a"), m])))
}

fn only_window(world: &World) -> attn_testkit::TileWindowView {
    let windows = world.tile_windows();
    assert_eq!(windows.len(), 1, "{windows:?}");
    windows[0].clone()
}

#[test]
fn a_popped_tile_shows_alone_in_its_own_window_where_the_daemon_keeps_it() {
    let world = start(vec![with_document(Some(OUT))]);

    let window = only_window(&world);
    assert!(window.shown);
    assert_eq!(window.frame, frame(OUT));
    assert_eq!(window.showing, Some(("work/desktop_1".into(), "m".into())));
    let screen = world.tile_screen(&window.key);
    assert!(screen.find(Target::role("group").name("m.md")).is_some());
    assert_eq!(
        screen.get(Target::key("tile.m.popout")).name.as_deref(),
        Some("Bring back tile (Ctrl+Shift+O)")
    );
    assert!(!shows(&world, "m.md"));
    assert!(world.runtime_is_at_rest() && world.tile_windows_are_at_rest());
}

#[test]
fn a_tile_brought_back_hides_its_window_and_the_next_pop_out_reuses_it() {
    let mut world = start(vec![with_document(Some(OUT))]);
    let key = only_window(&world).key;

    arrange(&mut world, "work/desktop_1", vec![with_document(None)]);
    assert!(!only_window(&world).shown);
    assert!(shows(&world, "m.md"));

    let moved = (40.0, 50.0, 600.0, 300.0);
    arrange(
        &mut world,
        "work/desktop_1",
        vec![with_document(Some(moved))],
    );
    let window = only_window(&world);
    assert_eq!(window.key, key);
    assert!(window.shown);
    assert_eq!(window.frame, frame(moved));
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest() && world.tile_windows_are_at_rest());
}

#[test]
fn a_reused_window_shows_the_tile_it_was_given() {
    let tiles = |out_m: bool, out_n: bool| {
        let leaf = |id: &str, out: bool| match out {
            true => popped(document(id, "a"), OUT),
            false => document(id, "a"),
        };
        desktop(
            1,
            "a",
            Some(row([pane("a"), leaf("m", out_m), leaf("n", out_n)])),
        )
    };
    let mut world = start(vec![tiles(true, false)]);
    arrange(&mut world, "work/desktop_1", vec![tiles(false, false)]);
    arrange(&mut world, "work/desktop_1", vec![tiles(false, true)]);

    let window = only_window(&world);
    assert_eq!(window.showing, Some(("work/desktop_1".into(), "n".into())));
    let screen = world.tile_screen(&window.key);
    assert!(screen.find(Target::role("group").name("n.md")).is_some());
    assert!(screen.find(Target::role("group").name("m.md")).is_none());
}

#[test]
fn the_pop_out_key_and_close_pressed_in_a_tile_window_bring_its_tile_back() {
    let mut world = start(vec![with_document(Some(OUT))]);
    let key = only_window(&world).key;
    let keymap = Keymap::from_setting(None, KeyPlatform::current());
    let chord = |action: &Action| match keymap.binding(action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    };

    world.tile_gesture(&key, |w| w.key(chord(&Action::TilePopOut)));
    world.tile_gesture(&key, |w| w.key(chord(&Action::SessionClose)));

    let back = serde_json::json!({"desktop_id": "work/desktop_1", "leaf_id": "m"});
    assert_eq!(
        sent(&world, "desktop_bring_back_leaf"),
        [back.clone(), back]
    );
    assert!(sent(&world, "desktop_pop_out_leaf").is_empty());
    assert!(sent(&world, "desktop_remove_leaf").is_empty());
}

#[test]
fn closing_a_tile_window_brings_its_tile_back_and_the_next_pop_out_opens_a_new_one() {
    let mut world = start(vec![with_document(Some(OUT))]);
    let key = only_window(&world).key;

    world.close_tile_window(&key);
    assert_eq!(
        sent(&world, "desktop_bring_back_leaf"),
        [serde_json::json!({"desktop_id": "work/desktop_1", "leaf_id": "m"})]
    );
    world.daemon.emit(SessionStateChangedMessage {
        session: Session {
            label: "renamed before the tile came back".into(),
            ..session("a")
        },
        ..SessionStateChangedMessage::new()
    });
    world.settle();
    assert!(
        world.tile_windows().is_empty(),
        "the closed window does not come back"
    );

    arrange(&mut world, "work/desktop_1", vec![with_document(None)]);
    arrange(&mut world, "work/desktop_1", vec![with_document(Some(OUT))]);
    let window = only_window(&world);
    assert_ne!(window.key, key);
    assert!(window.shown);
}

#[test]
fn a_tile_window_the_person_moved_tells_the_daemon_where_it_is_now() {
    let mut world = start(vec![with_document(Some(OUT))]);
    let key = only_window(&world).key;

    world.move_tile_window(&key, frame(OUT));
    assert!(
        sent(&world, "desktop_pop_out_leaf").is_empty(),
        "it did not move"
    );

    world.move_tile_window(&key, frame((300.0, 200.0, 640.0, 480.0)));
    assert_eq!(
        sent(&world, "desktop_pop_out_leaf"),
        [serde_json::json!({
            "desktop_id": "work/desktop_1",
            "leaf_id": "m",
            "frame": {"x": 300.0, "y": 200.0, "width": 640.0, "height": 480.0},
        })]
    );
}

#[test]
fn row_shortcuts_in_a_tile_window_leave_the_desktop_and_a_split_opens_beside_its_tile() {
    let out = row([popped(pane("a"), OUT), pane("b")]);
    let mut world = start(vec![
        desktop(1, "b", Some(out)),
        desktop(2, "c", Some(pane("c"))),
    ]);
    press(&mut world, Action::DesktopSelect2);
    let key = only_window(&world).key;
    let keymap = Keymap::from_setting(None, KeyPlatform::current());
    let chord = |action: &Action| match keymap.binding(action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    };
    let before = world.daemon.sent().len();

    for action in [
        Action::TerminalFocusLeft,
        Action::TerminalFocusRight,
        Action::TerminalToggleZoom,
        Action::TerminalToggleMaximize,
        Action::ShelfCarryLeft,
        Action::ShelfSendToShelf,
        Action::ShelfFewerTiles,
    ] {
        world.tile_gesture(&key, |w| w.key(chord(&action)));
    }
    assert_eq!(
        world.daemon.sent().len(),
        before,
        "{:?}",
        world.daemon.sent()
    );
    assert!(
        shows(&world, "Agent b"),
        "the main window still shows its tile"
    );

    world.tile_gesture(&key, |w| w.key(chord(&Action::UiShowShortcuts)));
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Keyboard shortcuts"))
            .is_some(),
        "other keys run in the main window"
    );
    world.gesture(|app| app.key(chord(&Action::UiShowShortcuts)));

    world.tile_gesture(&key, |w| w.key(chord(&Action::TerminalSplitVertical)));
    let spawned = &sent(&world, "spawn_session")[0];
    assert_eq!(spawned["spawned_from"], "s-a");
    assert_eq!(spawned["placement"]["desktop_id"], "work/desktop_1");
    assert_eq!(spawned["placement"]["anchor_pane_id"], "a");
}

#[test]
fn quitting_leaves_popped_tiles_out_for_the_next_launch() {
    let mut world = start(vec![with_document(Some(OUT))]);

    world.quit_tile_windows();

    assert!(sent(&world, "desktop_bring_back_leaf").is_empty());
}

#[test]
fn a_tile_window_leaves_the_main_windows_terminals_alone() {
    let attached = |out: bool| {
        let mut world = start(vec![with_document(out.then_some(OUT))]);
        world.advance(Duration::from_secs(1));
        (
            world.daemon.sent_of("attach_session").len(),
            world.daemon.sent_of("pty_resize").len(),
            world.daemon.sent_of("terminal_focus").len(),
        )
    };
    assert_eq!(attached(true), attached(false));
}

#[test]
fn a_terminal_tile_stays_on_its_desktop() {
    let tree = row([pane("a"), document("m", "a")]);
    let mut world = start(vec![desktop(1, "a", Some(tree))]);

    press(&mut world, Action::TilePopOut);

    assert!(sent(&world, "desktop_pop_out_leaf").is_empty());
    assert!(world.screen().find(Target::key("tile.a.popout")).is_none());
    assert!(world.screen().find(Target::key("tile.m.popout")).is_some());
}
