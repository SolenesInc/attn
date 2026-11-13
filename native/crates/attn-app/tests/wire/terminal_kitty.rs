use attn_protocol::{
    AttachResultMessage, AttachSnapshot, KittyImageResultMessage, KittyPlacement,
    KittyPlacementsMessage,
};
use attn_term::{SNAPSHOT_FORMAT, Vt};
use attn_testkit::{Answer, World};
use base64::Engine;
use serde_json::{Value, json};

use super::terminal::{attached, one_terminal, output};
use super::terminal_view::{view, wheel};

fn placement(placement_id: i64, image_id: i64, generation: i64, row: i64) -> KittyPlacement {
    KittyPlacement {
        image_id,
        placement_id,
        image_generation: generation,
        viewport_row: row,
        viewport_col: 3,
        viewport_visible: true,
        pixel_width: 20,
        pixel_height: 40,
        ..KittyPlacement::default()
    }
}

fn describe(world: &World, seq: i64, placements: Vec<KittyPlacement>) {
    world.daemon.emit(KittyPlacementsMessage {
        event: "kitty_placements".into(),
        id: "r1".into(),
        seq,
        placements,
    });
}

/// Sends an image's pixels as the binary frame the daemon answers `get_kitty_image` with.
fn send_pixels(world: &World, image: u32, generation: u64, size: (u32, u32), rgba: &[u8]) {
    let mut frame = vec![0x02, 2];
    frame.extend_from_slice(b"r1");
    frame.extend_from_slice(&image.to_be_bytes());
    frame.extend_from_slice(&generation.to_be_bytes());
    frame.extend_from_slice(&size.0.to_be_bytes());
    frame.extend_from_slice(&size.1.to_be_bytes());
    frame.push(1);
    frame.extend_from_slice(rgba);
    world.daemon.emit_frame(frame);
}

/// The image ids the app asked the daemon for, in order.
fn asked(world: &World) -> Vec<Value> {
    world
        .daemon
        .sent_of("get_kitty_image")
        .iter()
        .map(|r| r["image_id"].clone())
        .collect()
}

fn drawn(world: &World) -> Value {
    view(world)["images"].clone()
}

fn started() -> World {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"$ icat cat.png\r\n");
    world.settle();
    world
}

#[test]
fn a_placement_asks_for_its_image_once_and_draws_it_when_the_pixels_arrive() {
    let mut world = started();
    let commits = world.runtime().stats.commits;

    describe(&world, 2, vec![placement(1, 7, 1, 1)]);
    world.settle();
    assert_eq!(asked(&world), [json!(7)]);
    assert_eq!(
        drawn(&world),
        json!([]),
        "nothing draws before the pixels come"
    );

    describe(&world, 3, vec![placement(1, 7, 1, 2)]);
    world.settle();
    assert_eq!(
        asked(&world),
        [json!(7)],
        "an image asked for is not asked again"
    );

    send_pixels(&world, 7, 1, (2, 1), &[255, 0, 0, 255, 0, 0, 255, 255]);
    world.settle();
    assert_eq!(
        drawn(&world),
        json!([{"image": 7, "placement": 1, "generation": 1, "row": 2, "col": 3,
                "size": [20, 40], "z": 0, "pixels": [2, 1]}])
    );

    describe(&world, 4, vec![placement(1, 7, 2, 2)]);
    world.settle();
    assert_eq!(
        asked(&world),
        [json!(7), json!(7)],
        "a new generation is a new image"
    );

    describe(&world, 5, vec![]);
    world.settle();
    assert_eq!(
        drawn(&world),
        json!([]),
        "the empty set takes the image away"
    );
    assert_eq!(
        world.runtime().stats.commits,
        commits,
        "images change what draws, never the view"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_image_the_daemon_cannot_give_is_asked_for_again_only_once_it_is_sent_anew() {
    let mut world = started();
    describe(&world, 2, vec![placement(1, 7, 1, 0)]);
    world.settle();
    world.daemon.emit(KittyImageResultMessage {
        id: "r1".into(),
        image_id: 7,
        success: false,
        error: Some("kitty image 7: not found".into()),
        ..KittyImageResultMessage::new()
    });
    world.settle();

    describe(&world, 3, vec![placement(1, 7, 1, 0)]);
    world.settle();
    assert_eq!(asked(&world), [json!(7)]);

    describe(&world, 4, vec![placement(1, 7, 2, 0)]);
    world.settle();
    assert_eq!(
        asked(&world),
        [json!(7), json!(7)],
        "a new generation is a new image"
    );
    assert_eq!(drawn(&world), json!([]));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_restored_screen_brings_back_its_placements_and_asks_for_their_pixels() {
    let daemon = one_terminal();
    daemon.answer_in_turn("attach_session", [Answer::Hold]);
    let mut world = World::start(daemon);
    world.open_desktop();
    let (cols, rows) = world.terminal_size("r1");
    let mut vt = Vt::new(cols, rows);
    vt.write(b"$ icat cat.png\r\n");
    let snapshot = vt.snapshot().expect("a screen at rest encodes");
    let request = world.daemon.sent_of("attach_session")[0].clone();
    world.daemon.reply_to(
        &request,
        AttachResultMessage {
            last_seq: Some(1),
            snapshot: Some(AttachSnapshot {
                cols: i64::from(cols),
                rows: i64::from(rows),
                format: Some(SNAPSHOT_FORMAT.into()),
                snapshot_b64: base64::engine::general_purpose::STANDARD.encode(snapshot),
                placements: Some(vec![placement(4, 9, 3, 1)]),
                ..AttachSnapshot::default()
            }),
            ..attached()
        },
    );
    world.settle();
    assert_eq!(asked(&world), [json!(9)]);

    send_pixels(&world, 9, 3, (1, 1), &[0, 255, 0, 255]);
    world.settle();
    assert_eq!(drawn(&world)[0]["placement"], 4);
    assert_eq!(drawn(&world)[0]["row"], 1);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_image_asked_for_on_a_connection_that_dropped_is_asked_for_again_on_the_next() {
    let mut world = started();
    describe(&world, 2, vec![placement(1, 7, 1, 0)]);
    world.settle();
    assert_eq!(asked(&world), [json!(7)]);

    world.reconnect();
    assert_eq!(
        asked(&world),
        [json!(7), json!(7)],
        "the answer to the first ask went with the connection"
    );
    send_pixels(&world, 7, 1, (1, 1), &[0, 255, 0, 255]);
    world.settle();
    assert_eq!(drawn(&world)[0]["image"], 7);
    assert!(world.runtime_is_at_rest());
}

/// A 2048x2048 RGBA image's pixels: 16 MiB, so five are past the 64 MiB the app keeps.
fn large(world: &World, image: u32) {
    send_pixels(
        world,
        image,
        1,
        (2048, 2048),
        &vec![image as u8; 2048 * 2048 * 4],
    );
}

fn notice(world: &World) -> Option<String> {
    world
        .screen()
        .find(attn_testkit::Target::key("notice.text"))
        .and_then(|n| n.name)
}

#[test]
fn placed_images_past_the_budget_stay_drawn_and_say_so_and_one_no_longer_placed_goes() {
    let mut world = started();
    describe(
        &world,
        2,
        (1..=5).map(|i| placement(i, i, 1, i - 1)).collect(),
    );
    world.settle();
    for image in 1..=5 {
        large(&world, image as u32);
    }
    world.settle();
    assert_eq!(
        notice(&world).as_deref(),
        Some("The terminals' images hold 80 MB of pixels, past the 64 MB budget; they stay drawn")
    );

    let lines: Vec<u8> = (0..40)
        .flat_map(|n| format!("line {n}\r\n").into_bytes())
        .collect();
    output(&world, 3, &lines);
    world.settle();
    let cursor_row = view(&world)["cursor"][1]
        .as_i64()
        .expect("the cursor shows");
    let scrolled = 1 + 40 - cursor_row;
    describe(
        &world,
        4,
        (1..=5)
            .map(|i| placement(i, i, 1, i - 1 - scrolled))
            .collect(),
    );
    world.settle();
    wheel(&mut world, scrolled as f32);
    world.settle();
    let images: Vec<(Value, Value)> = drawn(&world)
        .as_array()
        .expect("the view lists its images")
        .iter()
        .map(|image| (image["image"].clone(), image["row"].clone()))
        .collect();
    assert_eq!(
        images,
        (1..=5)
            .map(|i| (json!(i), json!(i - 1)))
            .collect::<Vec<_>>(),
        "every placed image draws where it was when scrolled back to"
    );
    assert_eq!(asked(&world).len(), 5, "nothing is asked for twice");

    describe(
        &world,
        5,
        (2..=6)
            .map(|i| placement(i, i, 1, i - 1 - scrolled))
            .collect(),
    );
    world.settle();
    large(&world, 6);
    world.settle();
    describe(
        &world,
        6,
        (1..=6)
            .map(|i| placement(i, i, 1, i - 1 - scrolled))
            .collect(),
    );
    world.settle();
    assert_eq!(
        asked(&world).last(),
        Some(&json!(1)),
        "an image no placement showed went, and is asked for once placed again"
    );
    world.advance(std::time::Duration::from_secs(6));
    assert!(world.runtime_is_at_rest());
}
