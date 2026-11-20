use std::collections::BTreeMap;

use attn_protocol::{Session, SessionStateChangedMessage};
use attn_testkit::{Daemon, Target, World, tree};
use serde_json::json;

use super::sidebar::session;
use super::terminal::{output, pane, showing};

const LONG_DESKTOP: &str =
    "build-and-release-platform-本番の権限を移行する作業ツリー-tokyo-region-primary";
/// The pop-out tile's smallest window, in points before the text scale.
const POPOUT: (f32, f32) = (320.0, 200.0);
/// Rows main's terminal keeps whole under its notices at 800 x 600 with 150% text.
const ROWS_MAIN_KEEPS: usize = 3;

fn agent(stale: bool, shared: bool) -> Session {
    Session {
        terminal_build_stale: Some(stale),
        conversation_shared_with: shared.then(|| vec!["s2".into()]),
        ..session("s1", "working")
    }
}

/// Terminal r1 on desktop 1, its conversation also open on a second desktop with a long name, in
/// a window `size` at text `scale`, popped out into its own window when `popout`.
fn world(size: (f32, f32), scale: &str, popout: bool) -> World {
    let mut state = showing(
        vec![attn_protocol::DesktopPane {
            session_id: "s1".into(),
            ..pane("t1", "r1")
        }],
        "daemon-1",
    );
    let mut desktop = state.desktops.as_mut().unwrap().pop().unwrap();
    if popout {
        let mut tile = tree::pane("t1");
        tile["window"] = json!({"x": 10, "y": 20, "width": POPOUT.0, "height": POPOUT.1});
        desktop.tree_json = tree::row([tile]).to_string();
    }
    let mut peer = desktop.clone();
    peer.id = "d2".into();
    peer.name = LONG_DESKTOP.into();
    peer.order_key = "2".into();
    peer.panes = vec![attn_protocol::DesktopPane {
        desktop_id: "d2".into(),
        session_id: "s2".into(),
        ..pane("t2", "r2")
    }];
    peer.tree_json = tree::row([tree::pane("t2")]).to_string();
    state.desktops = Some(vec![desktop, peer]);
    state.sessions = Some(vec![
        agent(false, false),
        Session {
            label: LONG_DESKTOP.into(),
            ..session("s2", "working")
        },
    ]);
    state.settings = Some(BTreeMap::from([("uiScale".into(), scale.into())]));
    let mut world = World::start_sized(Daemon::scripted().state(state), size);
    world.open_desktop();
    world
}

#[test]
fn a_terminal_under_two_notices_keeps_as_many_whole_rows_as_before_at_the_smallest_window() {
    let mut world = world((800.0, 600.0), "1.5", false);
    world.daemon.emit(SessionStateChangedMessage {
        session: agent(true, true),
        ..SessionStateChangedMessage::new()
    });
    world.settle();
    let [_, top, _, height] = world.rect("terminal.r1");
    let band = world.rect("terminal.r1.notices");
    let notices_end = band[1] + band[3];
    let (_, rows) = world.terminal_size("r1");
    let row = height / f64::from(rows);
    let whole = (0..rows)
        .filter(|i| top + f64::from(*i) * row >= notices_end)
        .count();
    assert!(
        whole >= ROWS_MAIN_KEEPS,
        "{whole} of {rows} rows stand whole under the notices ending at {notices_end}"
    );
    for key in [
        "session.s1.stale-build.reload",
        "session.s1.stale-build.dismiss",
    ] {
        assert!(
            world.screen().find(Target::key(key)).is_some(),
            "{key} is still in reach"
        );
    }
}

#[test]
fn the_shared_conversation_notice_is_announced_politely() {
    let mut world = world((1440.0, 900.0), "1", false);
    world.daemon.emit(SessionStateChangedMessage {
        session: agent(false, true),
        ..SessionStateChangedMessage::new()
    });
    world.settle();
    let notice = world
        .screen()
        .get(Target::key("session.s1.shared-conversation"));
    assert_eq!(notice.role, "status", "{notice:?}");
}

#[test]
fn find_keeps_close_and_its_ring_whole_in_the_smallest_popped_out_tile() {
    let mut world = world((800.0, 600.0), "1.5", true);
    let id = world
        .tile_windows()
        .first()
        .map(|tile| tile.key.clone())
        .unwrap();
    output(&world, 1, "needle 東京\r\nneedle 東京\r\n".as_bytes());
    world.settle();
    let find = if super::mac() { "mod+f" } else { "mod+shift+f" };
    world.tile_gesture(&id, |app| app.press(find));
    world.tile_gesture(&id, |app| app.type_text("needle"));
    let screen = world.tile_screen(&id);
    let [x, _, w, _] = screen
        .bounds(Target::key("terminal.r1.find.close"))
        .expect("Close find is drawn");
    let window = POPOUT.0 / 1.5;
    let ring = attn_ui::system::RING_REACH;
    assert!(
        x + w + ring <= window + 0.01,
        "Close find ends at {} with its ring at {}, past the window's edge at {window}",
        x + w,
        x + w + ring
    );
}

/// Terminals r1 over r2 on desktop 1, r1's conversation also open on a second desktop and its
/// build older than the daemon's, at 150% text in a window `size`.
fn stacked(size: (f32, f32)) -> World {
    let mut state = showing(
        vec![
            attn_protocol::DesktopPane {
                session_id: "s1".into(),
                ..pane("t1", "r1")
            },
            attn_protocol::DesktopPane {
                session_id: "s3".into(),
                ..pane("t3", "r3")
            },
        ],
        "daemon-1",
    );
    let mut desktop = state.desktops.as_mut().unwrap().pop().unwrap();
    desktop.tree_json = tree::below(tree::pane("t1"), tree::pane("t3")).to_string();
    let mut peer = desktop.clone();
    peer.id = "d2".into();
    peer.name = LONG_DESKTOP.into();
    peer.order_key = "2".into();
    peer.panes = vec![attn_protocol::DesktopPane {
        desktop_id: "d2".into(),
        session_id: "s2".into(),
        ..pane("t2", "r2")
    }];
    peer.tree_json = tree::row([tree::pane("t2")]).to_string();
    state.desktops = Some(vec![desktop, peer]);
    state.sessions = Some(vec![
        agent(true, true),
        Session {
            label: LONG_DESKTOP.into(),
            ..session("s2", "working")
        },
        session("s3", "working"),
    ]);
    state.settings = Some(BTreeMap::from([("uiScale".into(), "1.5".into())]));
    let mut world = World::start_sized(Daemon::scripted().state(state), size);
    world.open_desktop();
    world
}

#[test]
fn notices_take_the_room_a_grown_terminal_has_once_its_new_grid_arrives_after_layout() {
    let large = (1440.0, 900.0);
    let mut grown = stacked((800.0, 600.0));
    grown.daemon.on("pty_resize", |request| {
        (request["id"] != "r1").then(|| {
            json!({
                "event": "pty_resized",
                "id": request["id"],
                "cols": request["cols"],
                "rows": request["rows"],
            })
        })
    });
    grown.resize(large);
    grown.settle();
    let asked = grown
        .daemon
        .sent_of("pty_resize")
        .into_iter()
        .rfind(|r| r["id"] == "r1")
        .expect("the grown terminal asks for a larger grid");
    let (cols, rows) = (
        asked["cols"].as_i64().unwrap(),
        asked["rows"].as_i64().unwrap(),
    );
    grown.daemon.emit(attn_protocol::PtyResizedMessage {
        id: "r1".into(),
        cols,
        rows,
        ..attn_protocol::PtyResizedMessage::new()
    });
    grown.settle();
    assert_eq!(grown.terminal_size("r1"), (cols as u16, rows as u16));
    let mut opened = stacked(large);
    let band = grown.rect("terminal.r1.notices");
    let fresh = opened.rect("terminal.r1.notices");
    assert!(
        (band[3] - fresh[3]).abs() < 0.5,
        "the notices stand {} high once the grid arrives, {} in a window opened at that size",
        band[3],
        fresh[3]
    );
}
