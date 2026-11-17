use attn_protocol::{AttachResultMessage, Desktop, InitialStateMessage};
use attn_testkit::allocations::made_here;
use attn_testkit::{Daemon, World, message, tree};
use serde_json::Value;

use super::terminal::{attached, output, pane, profile};

const OTHER_DESKTOPS: usize = 20;
/// The allocations a streamed frame may add for each desktop the window does not show. Reading
/// the desktops' layouts on every frame cost about 180 each; reading the leaves the app parsed
/// when each layout arrived costs about 4.
const PER_OTHER_DESKTOP: u64 = 20;

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

/// The allocations each frame makes while an agent streams into the one terminal on screen.
fn per_streamed_frame(others: usize) -> u64 {
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
    for seq in 1..20 {
        output(&world, seq, b"warming up\r\n");
        world.settle();
    }
    let (frames, made) = (world.terminal_frames(), made_here());
    for seq in 20..120 {
        output(&world, seq, b"a line the agent wrote\r\n");
        world.settle();
    }
    let frames = (world.terminal_frames() - frames) as u64;
    assert_eq!(frames, 100, "each line drew a frame");
    assert!(world.runtime_is_at_rest());
    (made_here() - made) / frames
}

#[test]
fn a_streaming_terminal_frame_does_not_read_again_the_layouts_of_the_desktops_off_screen() {
    let alone = per_streamed_frame(0);
    let beside_others = per_streamed_frame(OTHER_DESKTOPS);

    assert!(
        beside_others <= alone + OTHER_DESKTOPS as u64 * PER_OTHER_DESKTOP,
        "a frame made {alone} allocations on the only desktop and {beside_others} beside \
         {OTHER_DESKTOPS} others"
    );
}
