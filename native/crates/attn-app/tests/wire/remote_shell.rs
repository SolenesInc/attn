use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    Desktop, DesktopPane, InitialStateMessage, OutpostInfo, OutpostsUpdatedMessage, Profile,
    ProfileArrangementChangedMessage, Session,
};
use attn_testkit::{Daemon, Target, World, key_event, message, tree};
use rune::KeyPlatform;
use serde_json::json;

use super::terminal_view::wheel;

const OUTPOST: &str = "studio-id";

fn studio(state: &str, reach: &str) -> OutpostInfo {
    OutpostInfo {
        id: OUTPOST.into(),
        name: "studio".into(),
        destination: "studio:2200".into(),
        state: state.into(),
        reach: reach.into(),
        state_since: "2026-10-18T18:00:00Z".into(),
        ..OutpostInfo::default()
    }
}

fn connected() -> OutpostInfo {
    studio("connected", "reachable")
}

fn lost() -> OutpostInfo {
    studio("lost", "unreachable")
}

fn connecting() -> OutpostInfo {
    studio("connecting", "connecting")
}

fn stalled() -> OutpostInfo {
    studio("stalled", "reachable")
}

fn shell(id: &str, host: Option<&str>) -> Session {
    Session {
        id: id.into(),
        label: id.into(),
        agent: "shell".into(),
        state: "idle".into(),
        directory: format!("/tmp/{id}"),
        host: host.map(str::to_owned),
        profile_id: "work".into(),
        ..Session::default()
    }
}

fn pane(desktop: &str, id: &str, runtime: &str, session: &str) -> DesktopPane {
    DesktopPane {
        desktop_id: desktop.into(),
        pane_id: id.into(),
        runtime_id: runtime.into(),
        session_id: session.into(),
        kind: "agent".into(),
        ..DesktopPane::default()
    }
}

fn solo(desktop: &str, slot: i64, session: &str, runtime: &str) -> Desktop {
    let leaf = format!("t-{session}");
    Desktop {
        id: desktop.into(),
        name: desktop.into(),
        profile_id: "work".into(),
        shortcut_slot: Some(slot),
        order_key: slot.to_string(),
        active_pane_id: leaf.clone(),
        tree_json: tree::pane(&leaf).to_string(),
        panes: vec![pane(desktop, &leaf, runtime, session)],
        ..Desktop::default()
    }
}

/// The remote shell `remote` on desktop d1, with terminal r1, and the local shells beside it
/// on desktops of their own: `local` with terminal r2, then `other` with r3.
fn start(outposts: Vec<OutpostInfo>, enabled: bool) -> World {
    start_as(outposts, enabled, "idle")
}

/// As `start`, with the remote shell in `remote_state`.
fn start_as(outposts: Vec<OutpostInfo>, enabled: bool, remote_state: &str) -> World {
    let desktops = vec![
        solo("d1", 1, "remote", "r1"),
        solo("d2", 2, "local", "r2"),
        solo("d3", 3, "other", "r3"),
    ];
    let arranged = desktops.clone();
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![profile("d1")]),
        sessions: Some(vec![
            Session {
                state: remote_state.into(),
                ..shell("remote", Some(OUTPOST))
            },
            shell("local", None),
            shell("other", None),
        ]),
        desktops: Some(desktops),
        outposts: Some(outposts),
        settings: Some([("outposts_enabled".to_owned(), enabled.to_string())].into()),
        ..InitialStateMessage::new()
    });
    daemon.on("desktop_set_current", move |r| {
        message(ProfileArrangementChangedMessage {
            profile: profile(r["desktop_id"].as_str().unwrap()),
            desktops: arranged.clone(),
            ..ProfileArrangementChangedMessage::new()
        })
    });
    World::start(daemon)
}

fn profile(current: &str) -> Profile {
    Profile {
        id: "work".into(),
        name: "Work".into(),
        current_desktop_id: current.into(),
        ..Profile::default()
    }
}

fn outposts(world: &mut World, outposts: Vec<OutpostInfo>) {
    world.daemon.emit(OutpostsUpdatedMessage {
        outposts,
        ..OutpostsUpdatedMessage::new()
    });
    world.settle();
}

fn output(world: &mut World, runtime: &str, seq: u32, data: &[u8]) {
    let mut frame = vec![0x01, runtime.len() as u8];
    frame.extend_from_slice(runtime.as_bytes());
    frame.extend_from_slice(&seq.to_be_bytes());
    frame.extend_from_slice(data);
    world.daemon.emit_frame(frame);
    world.settle();
}

fn refuse(world: &mut World, cmd: &str) {
    world.daemon.emit(json!({
        "event": "command_error",
        "cmd": cmd,
        "success": false,
        "error": "studio is unreachable; this needs it now",
        "terminal_id": "r1",
    }));
    world.settle();
}

fn go_to(world: &mut World, slot: u8) {
    let action = match slot {
        1 => Action::DesktopSelect1,
        2 => Action::DesktopSelect2,
        _ => Action::DesktopSelect3,
    };
    press(world, &action);
}

fn press(world: &mut World, action: &Action) {
    let ev = match Keymap::from_setting(None, KeyPlatform::current()).binding(action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    };
    world.gesture(|app| app.key(ev));
}

/// What the app sent each terminal, by runtime id, with who produced it.
fn sent_input(world: &World) -> Vec<(String, String, String)> {
    world
        .daemon
        .sent_of("pty_input")
        .iter()
        .map(|r| {
            (
                r["id"].as_str().unwrap().to_owned(),
                r["data"].as_str().unwrap().to_owned(),
                r["source"].as_str().unwrap_or_default().to_owned(),
            )
        })
        .collect()
}

fn attaches(world: &World) -> Vec<String> {
    world
        .daemon
        .sent_of("attach_session")
        .iter()
        .map(|r| r["id"].as_str().unwrap().to_owned())
        .collect()
}

fn name_of(world: &World, key: &str) -> Option<String> {
    world.screen().find(Target::key(key)).and_then(|n| n.name)
}

/// The tile's status strip, as the Tauri app's `desktop-pane-offline-strip` reads.
fn strip(world: &World) -> Option<String> {
    name_of(world, "tile.r1.status")
}

/// The flash over r1's tile while it plays, by its key.
fn flash(world: &World) -> Option<String> {
    world
        .runtime()
        .elements()
        .into_iter()
        .filter(|e| !e.exiting)
        .filter_map(|e| e.key)
        .find(|k| k.starts_with("tile.refused.r1.flash."))
}

fn notice(world: &World) -> Option<String> {
    name_of(world, "notice.text")
}

fn screen_text(world: &mut World, runtime: &str) -> String {
    world.terminal_text(runtime).join("\n")
}

#[test]
fn an_unreachable_outposts_shell_keeps_its_screen_is_marked_offline_and_refuses_typing_close_split_and_reload()
 {
    for state in ["lost", "refused"] {
        let mut world = start(vec![connected()], true);
        world.open_desktop();
        output(&mut world, "r1", 1, b"last remote screen");

        outposts(&mut world, vec![studio(state, "unreachable")]);

        assert_eq!(
            strip(&world).as_deref(),
            Some("studio unreachable · typing is not sent"),
            "{state}: a tile that showed its screen does not say the screen is elsewhere"
        );
        assert!(screen_text(&mut world, "r1").contains("last remote screen"));
        assert_eq!(
            name_of(&world, "tile.t-remote.host").as_deref(),
            Some("studio")
        );
        for mark in ["tile.t-remote.state", "sidebar.session.remote.state"] {
            assert_eq!(name_of(&world, mark).as_deref(), Some("offline"), "{mark}");
        }
        assert_ne!(
            name_of(&world, "sidebar.session.local.state").as_deref(),
            Some("offline")
        );

        world.gesture(|app| app.type_text("x"));
        assert!(sent_input(&world).is_empty(), "typing for it is dropped");
        let first = flash(&world).expect("a refused key flashes the tile");
        world.advance(Duration::from_millis(100));
        world.gesture(|app| app.type_text("y"));
        assert_ne!(flash(&world), Some(first), "each refused key flashes again");
        assert!(
            !strip(&world).unwrap().contains("may not have been sent"),
            "keys refused here never left"
        );

        press(&mut world, &Action::TerminalSplitVertical);
        assert_eq!(
            notice(&world).as_deref(),
            Some("studio is unreachable; this needs it now")
        );
        press(&mut world, &Action::SessionClose);
        world.gesture(|app| {
            app.update(|state, tx| {
                attn_app::dispatch(
                    state,
                    Action::ReloadSession {
                        session: "remote".into(),
                    },
                    tx,
                );
            })
        });
        for cmd in [
            "spawn_session",
            "unregister",
            "desktop_close_tile",
            "desktop_close",
            "reload_session",
        ] {
            assert!(world.daemon.sent_of(cmd).is_empty(), "{state}: sent {cmd}");
        }
        assert_eq!(
            notice(&world).as_deref(),
            Some("studio is unreachable; this needs it now")
        );

        go_to(&mut world, 2);
        assert_eq!(world.screen().find(Target::key("tile.t-local.host")), None);
        world.gesture(|app| app.type_text("l"));
        assert_eq!(
            sent_input(&world),
            [("r2".into(), "l".into(), "user".into())],
            "a local shell still takes typing"
        );
        world.advance(Duration::from_secs(10));
        assert!(world.runtime_is_at_rest());
    }
}

#[test]
fn an_unreachable_shell_reads_offline_on_home_and_says_its_screen_is_on_the_outpost_while_outposts_is_off()
 {
    let mut world = start(vec![lost()], false);
    assert_eq!(
        name_of(&world, "home.session.remote.state").as_deref(),
        Some("offline")
    );
    assert_ne!(
        name_of(&world, "home.session.local.state").as_deref(),
        Some("offline")
    );

    world.open_desktop();
    assert_eq!(
        name_of(&world, "tile.t-remote.host").as_deref(),
        Some("studio")
    );
    assert_eq!(
        strip(&world).as_deref(),
        Some("studio unreachable · typing is not sent The screen is on studio.")
    );
    world.gesture(|app| app.type_text("x"));
    assert!(flash(&world).is_some());
    press(&mut world, &Action::SessionClose);
    assert!(attaches(&world).is_empty(), "nothing attaches to it");
    assert!(sent_input(&world).is_empty());
    assert!(world.daemon.sent_of("desktop_close_tile").is_empty());
    assert_eq!(
        notice(&world).as_deref(),
        Some("studio is unreachable; this needs it now")
    );
}

#[test]
fn a_shell_whose_host_has_no_outpost_row_is_unreachable() {
    let mut world = start(vec![], true);
    world.open_desktop();

    assert_eq!(
        name_of(&world, "tile.t-remote.state").as_deref(),
        Some("offline")
    );
    assert!(attaches(&world).is_empty());
    world.gesture(|app| app.type_text("x"));
    assert!(sent_input(&world).is_empty());
    assert!(flash(&world).is_some());
}

#[test]
fn a_stalled_shell_says_it_is_not_responding_still_takes_typing_and_clears_on_recovery() {
    let mut world = start(vec![stalled()], true);
    world.open_desktop();

    assert_ne!(
        name_of(&world, "tile.t-remote.state").as_deref(),
        Some("offline")
    );
    assert_ne!(
        name_of(&world, "sidebar.session.remote.state").as_deref(),
        Some("offline")
    );
    assert_eq!(strip(&world).as_deref(), Some("studio not responding"));
    world.gesture(|app| app.type_text("x"));
    assert_eq!(
        sent_input(&world),
        [("r1".into(), "x".into(), "user".into())]
    );
    assert_eq!(flash(&world), None);

    outposts(&mut world, vec![connected()]);
    assert_eq!(strip(&world), None);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_connecting_shell_says_typing_is_not_sent_and_attaches_once_reachable() {
    let mut world = start(vec![connecting()], true);
    world.open_desktop();

    assert_ne!(
        name_of(&world, "tile.t-remote.state").as_deref(),
        Some("offline")
    );
    assert_eq!(
        strip(&world).as_deref(),
        Some("Connecting to studio · typing is not sent")
    );
    world.gesture(|app| app.type_text("x"));
    assert!(flash(&world).is_some());
    assert!(attaches(&world).is_empty());
    assert!(world.daemon.sent_of("pty_resize").is_empty());
    assert!(sent_input(&world).is_empty());

    world.daemon.on("desktop_close_tile", |r| {
        json!({
            "event": "profile_action_result",
            "request_id": r["request_id"],
            "action": "desktop_close_tile",
            "success": false,
            "error": "studio is unreachable; this needs it now",
        })
    });
    press(&mut world, &Action::SessionClose);
    world.settle();
    assert_eq!(
        world.daemon.sent_of("desktop_close_tile").len(),
        1,
        "a connecting outpost is not offline, so the close goes to the daemon"
    );
    assert_eq!(
        notice(&world).as_deref(),
        Some("Could not close that tile: studio is unreachable; this needs it now")
    );

    outposts(&mut world, vec![connected()]);
    assert_eq!(strip(&world), None);
    assert_eq!(attaches(&world), ["r1"]);
    world.gesture(|app| app.type_text("z"));
    assert_eq!(
        sent_input(&world),
        [("r1".into(), "z".into(), "user".into())],
        "the key typed while connecting never follows"
    );
}

#[test]
fn a_shell_drawn_unreachable_says_its_screen_is_on_the_outpost_and_attaches_on_recovery() {
    let mut world = start(vec![lost()], true);
    world.open_desktop();

    assert!(attaches(&world).is_empty());
    assert!(world.daemon.sent_of("pty_resize").is_empty());
    assert_eq!(screen_text(&mut world, "r1").trim(), "");
    assert_eq!(
        name_of(&world, "tile.r1.screen-on").as_deref(),
        Some("The screen is on studio.")
    );
    world.gesture(|app| app.type_text("x"));

    outposts(&mut world, vec![connected()]);
    assert_eq!(attaches(&world), ["r1"]);
    assert!(
        sent_input(&world).is_empty(),
        "a refused key is never replayed"
    );
    assert_eq!(strip(&world), None);
}

#[test]
fn a_shell_drawn_while_connecting_that_turns_unreachable_says_its_screen_is_on_the_outpost() {
    let mut world = start(vec![connecting()], true);
    world.open_desktop();

    outposts(&mut world, vec![lost()]);

    assert_eq!(
        strip(&world).as_deref(),
        Some("studio unreachable · typing is not sent The screen is on studio.")
    );
}

#[test]
fn an_unreachable_shell_neither_flashes_nor_sends_the_wheel() {
    let mut world = start(vec![connected()], true);
    world.open_desktop();
    output(&mut world, "r1", 1, b"\x1b[?1000h\x1b[?1006h");
    outposts(&mut world, vec![lost()]);

    wheel(&mut world, -1.0);

    assert!(sent_input(&world).is_empty());
    assert_eq!(flash(&world), None);
}

#[test]
fn a_stalled_bridge_that_turns_lost_keeps_the_refused_count_and_sends_the_wheel_once_back() {
    let mut world = start(vec![stalled()], true);
    world.open_desktop();
    world.gesture(|app| app.type_text("ls"));
    let typed = sent_input(&world).len();
    assert_eq!(typed, 2);
    for _ in 0..typed {
        refuse(&mut world, "pty_input");
    }
    assert!(flash(&world).is_some());

    outposts(&mut world, vec![lost()]);
    assert_eq!(
        strip(&world).as_deref(),
        Some("studio unreachable · typing is not sent 2 keys may not have been sent")
    );

    outposts(&mut world, vec![connected()]);
    output(&mut world, "r1", 1, b"\x1b[?1000h\x1b[?1006h");
    wheel(&mut world, -1.0);
    let wheeled = sent_input(&world);
    assert_eq!(wheeled.len(), typed + 1);
    assert_eq!(wheeled[typed].2, "pointer");
    assert_eq!(
        name_of(&world, "tile.refused.r1").as_deref(),
        Some("2 keys may not have been sent"),
        "the pointer is no typed key"
    );

    world.gesture(|app| app.type_text("z"));
    assert_eq!(name_of(&world, "tile.refused.r1"), None);
}

#[test]
fn the_count_of_keys_not_sent_waits_while_its_tile_is_on_another_desktop() {
    let mut world = start(vec![stalled()], true);
    world.open_desktop();
    world.gesture(|app| app.type_text("x"));
    refuse(&mut world, "pty_input");
    outposts(&mut world, vec![lost()]);

    go_to(&mut world, 2);
    go_to(&mut world, 3);
    assert_eq!(world.screen().find(Target::key("tile.t-remote")), None);
    outposts(&mut world, vec![connected()]);
    go_to(&mut world, 1);

    assert_eq!(
        name_of(&world, "tile.refused.r1").as_deref(),
        Some("1 key may not have been sent")
    );
}

#[test]
fn a_re_pair_waits_through_connecting_before_attaching_again() {
    let mut world = start(vec![connected()], true);
    world.open_desktop();
    assert_eq!(attaches(&world), ["r1"]);

    outposts(&mut world, vec![studio("refused", "unreachable")]);
    outposts(&mut world, vec![connecting()]);
    assert_ne!(
        name_of(&world, "tile.t-remote.state").as_deref(),
        Some("offline")
    );
    assert_eq!(attaches(&world), ["r1"]);

    outposts(&mut world, vec![connected()]);
    assert_eq!(attaches(&world), ["r1", "r1"]);
}

#[test]
fn an_attach_answered_after_the_outpost_was_lost_leaves_the_recovered_terminal_typing() {
    let mut world = start(vec![connected()], true);
    world.daemon.on("attach_session", |_| ());
    world.open_desktop();
    assert_eq!(attaches(&world), ["r1"]);

    outposts(&mut world, vec![lost()]);
    outposts(&mut world, vec![connected()]);
    assert_eq!(attaches(&world), ["r1", "r1"]);

    for _ in 0..2 {
        world.daemon.emit(json!({
            "event": "attach_result", "id": "r1", "success": true, "running": true,
            "cols": 80, "rows": 24,
        }));
    }
    world.settle();
    assert!(!screen_text(&mut world, "r1").contains("Failed to attach"));
    world.gesture(|app| app.type_text("z"));
    assert_eq!(
        sent_input(&world),
        [("r1".into(), "z".into(), "user".into())]
    );
}

#[test]
fn a_recoverable_shell_on_an_unreachable_outpost_closes_but_neither_reloads_nor_splits() {
    let mut world = start_as(vec![lost()], true, "recoverable");
    world.open_desktop();

    world.gesture(|app| {
        app.update(|state, tx| {
            attn_app::dispatch(
                state,
                Action::ReloadSession {
                    session: "remote".into(),
                },
                tx,
            );
        })
    });
    assert!(world.daemon.sent_of("reload_session").is_empty());
    assert_eq!(
        notice(&world).as_deref(),
        Some("Failed to reload session: studio is unreachable; this needs it now")
    );

    press(&mut world, &Action::TerminalSplitVertical);
    assert!(world.daemon.sent_of("spawn_session").is_empty());
    assert_eq!(
        notice(&world).as_deref(),
        Some("studio is unreachable; this needs it now")
    );

    press(&mut world, &Action::SessionClose);
    assert_eq!(world.daemon.sent_of("desktop_close_tile").len(), 1);
}
