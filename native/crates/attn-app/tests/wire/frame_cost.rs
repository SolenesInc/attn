use attn_protocol::{AttachResultMessage, Desktop, InitialStateMessage};
use attn_testkit::allocations::made_here;
use attn_testkit::{Daemon, World, message, tree};
use serde_json::Value;

use super::terminal::{attached, output, pane, profile};

const OTHER_DESKTOPS: usize = 20;
/// The allocations a streamed frame may add for each desktop the window does not show. Reading
/// the desktops' layouts on every frame cost about 180 each, and reading each desktop's seed
/// tiles about 4; with the seeds the desktops show read once per layout, it adds less than 1.
const PER_OTHER_DESKTOP: u64 = 2;
/// Lines that fill the terminal, so its cursor, which the input method's caret follows, stays
/// on the last row while the agent streams.
const FILLING_LINES: u32 = 60;

fn other_desktop(n: usize) -> Desktop {
    let id = |kind: &str| format!("{kind}{n}");
    let mut seed = tree::tile(&id("seed"), "seed");
    seed["tile_params"] = Value::from("s-abcdef");
    let mut document = tree::tile(&id("doc"), "markdown");
    document["tile_params"] = Value::from(r#"{"path":"/repo/plan.md"}"#);
    let mut columns = vec![seed, document];
    columns.extend((0..4).map(|i| tree::pane(&format!("{}-{i}", id("pane")))));
    Desktop {
        id: id("desktop"),
        name: id("desktop"),
        profile_id: "work".into(),
        order_key: format!("{n:03}"),
        tree_json: tree::row(columns).to_string(),
        ..Desktop::default()
    }
}

/// What each frame costs while an agent streams into the one terminal on screen: the allocations
/// it makes, and the frames that painted the window anew.
struct Streamed {
    allocations: u64,
    paints: u64,
}

fn per_streamed_frame(others: usize) -> Streamed {
    let mut desktops = vec![super::terminal::desktop(vec![pane("t1", "r1")])];
    desktops.extend((0..others).map(other_desktop));
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![profile()]),
        desktops: Some(desktops),
        daemon_instance_id: Some("daemon-1".into()),
        ..InitialStateMessage::new()
    });
    daemon.on("attach_session", |request| {
        message(AttachResultMessage {
            id: request["id"].as_str().unwrap().into(),
            ..attached()
        })
    });
    daemon.on("pty_resize", |_| ());
    let mut world = World::start(daemon);
    world.open_desktop();
    for seq in 1..=FILLING_LINES {
        output(&world, seq, b"warming up\r\n");
        world.settle();
    }
    let (frames, made, paints) = (
        world.terminal_frames(),
        made_here(),
        world.runtime().stats.paints,
    );
    for seq in FILLING_LINES + 1..=FILLING_LINES + 100 {
        output(&world, seq, b"a line the agent wrote\r\n");
        world.settle();
    }
    let frames = (world.terminal_frames() - frames) as u64;
    assert_eq!(frames, 100, "each line drew a frame");
    assert!(world.runtime_is_at_rest());
    Streamed {
        allocations: (made_here() - made) / frames,
        paints: world.runtime().stats.paints - paints,
    }
}

#[test]
fn a_streaming_terminal_frame_does_no_work_for_the_desktops_off_screen() {
    let alone = per_streamed_frame(0);
    let beside_others = per_streamed_frame(OTHER_DESKTOPS);

    assert!(
        beside_others.allocations <= alone.allocations + OTHER_DESKTOPS as u64 * PER_OTHER_DESKTOP,
        "a frame made {} allocations on the only desktop and {} beside {OTHER_DESKTOPS} others",
        alone.allocations,
        beside_others.allocations,
    );
    assert_eq!(
        beside_others.paints, 0,
        "the terminal's cells are all a streamed frame changes, yet {} of 100 frames painted \
         the window again, the sidebar's row for each desktop among it",
        beside_others.paints,
    );
}
