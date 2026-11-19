use std::sync::{Arc, Mutex};

use attn_protocol::{SeedArtifact, SeedArtifactTargetResult, SeedArtifactTargetResultMessage};
use attn_testkit::tree::{pane, row};
use attn_testkit::{Request, World, message};
use base64::Engine;
use serde_json::json;

use super::tile::{desktop, document, profile, seed_tile};
use super::*;

const WIDE: &[u8] = include_bytes!("../../fixtures/wide-strip.png");
const SQUARE: &[u8] = include_bytes!("../../fixtures/square.png");

/// The seed's own `chart.png` as the daemon holds it: absent, or these bytes changed at this
/// time.
type ChartFile = Arc<Mutex<Option<(&'static [u8], &'static str)>>>;

fn charted() -> Seed {
    with(seed("s-chart1", "Charted plan"), |s| {
        s.body = "The numbers:\n\n![chart](chart.png)\n".into()
    })
}

/// A daemon starting from `state` that reads the charted seed, and its `chart.png` from
/// `file` as it is at each read.
fn charting(state: InitialStateMessage, file: &ChartFile) -> Daemon {
    let daemon = Daemon::scripted().state(state);
    let held = file.clone();
    daemon.on("seed_document_get", move |r: &Request| {
        let mut reply = document(r, charted(), Vec::new(), &[]);
        if let Some((bytes, modified_at)) = *held.lock().unwrap() {
            reply["document"]["artifacts"] = serde_json::to_value(vec![SeedArtifact {
                filename: "chart.png".into(),
                relative_target: "chart.png".into(),
                size: bytes.len() as i64,
                modified_at: modified_at.into(),
            }])
            .unwrap();
        }
        reply
    });
    let held = file.clone();
    daemon.on("seed_artifact_target", move |r: &Request| {
        let file = *held.lock().unwrap();
        message(SeedArtifactTargetResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: file.is_some(),
            result: file.map(|(bytes, _)| SeedArtifactTargetResult {
                relative_target: "chart.png".into(),
                mime_type: Some("image/png".into()),
                data_base64: Some(base64::engine::general_purpose::STANDARD.encode(bytes)),
                ..SeedArtifactTargetResult::default()
            }),
            error: file.is_none().then(|| "file does not exist".into()),
            ..SeedArtifactTargetResultMessage::new()
        })
    });
    daemon
}

/// The charted seed in a tile beside an agent.
fn in_tile(file: &ChartFile) -> World {
    let daemon = charting(
        InitialStateMessage {
            profiles: Some(vec![profile()]),
            desktops: Some(vec![desktop(
                row(vec![pane("a"), seed_tile("s-chart1")]),
                1,
            )]),
            ..initial(vec![charted()], vec![session("agent", "Agent a")])
        },
        file,
    );
    let mut world = World::start_sized(daemon, (1600.0, 900.0));
    world.open_desktop();
    world.settle();
    world
}

/// The charted seed open on the Garden's page.
fn on_garden_page(file: &ChartFile) -> World {
    let daemon = charting(initial(vec![charted()], Vec::new()), file);
    let mut world = open_garden_sized(daemon, (1600.0, 900.0));
    open_row(&mut world, "Charted plan");
    world.settle();
    world
}

/// The daemon's push after the seed's files changed to `now`.
fn files_change(world: &mut World, file: &ChartFile, now: Option<(&'static [u8], &'static str)>) {
    *file.lock().unwrap() = now;
    world.daemon.emit(GardenSeedsChangedMessage {
        profile_id: PROFILE.into(),
        order: vec!["s-chart1".into()],
        seeds: vec![with(charted(), |s| s.rev = 2)],
        total: 1,
        ..GardenSeedsChangedMessage::new()
    });
    world.settle();
}

/// The chart's drawn width and height, when it draws.
fn chart_size(world: &mut World) -> Option<[f64; 2]> {
    let found = world
        .automate(
            "find",
            json!({"target": {"role": "image", "name": "chart"}}),
        )
        .ok()?;
    let [_, _, w, h]: [f64; 4] = serde_json::from_value(found["rect"].clone()).ok()?;
    Some([w, h])
}

fn draws_a_picture_once_its_file_is_attached(open: fn(&ChartFile) -> World) {
    let file = ChartFile::default();
    let mut world = open(&file);
    assert_eq!(chart_size(&mut world), None);

    files_change(&mut world, &file, Some((SQUARE, "2026-11-19T16:51:00Z")));

    let [w, h] = chart_size(&mut world).expect("the attached chart draws in the open reader");
    assert_eq!(w, h, "the square chart draws square");
}

fn draws_the_new_picture_when_its_file_is_replaced_under_the_same_name(
    open: fn(&ChartFile) -> World,
) {
    let file = ChartFile::new(Mutex::new(Some((WIDE, "2026-11-19T16:51:00Z"))));
    let mut world = open(&file);
    let [w, h] = chart_size(&mut world).expect("the wide chart draws");
    assert!(w > 20.0 * h, "the wide strip draws wide: {w}x{h}");

    files_change(&mut world, &file, Some((SQUARE, "2026-11-19T16:52:00Z")));

    let [w, h] = chart_size(&mut world).expect("the replaced chart draws");
    assert_eq!(
        w, h,
        "the replacement's square pixels draw, not the old strip"
    );
}

#[test]
fn an_open_seed_tile_draws_a_picture_once_its_file_is_attached() {
    draws_a_picture_once_its_file_is_attached(in_tile);
}

#[test]
fn an_open_seed_tile_draws_the_new_picture_when_its_file_is_replaced_under_the_same_name() {
    draws_the_new_picture_when_its_file_is_replaced_under_the_same_name(in_tile);
}

#[test]
fn the_garden_page_draws_a_picture_once_its_file_is_attached() {
    draws_a_picture_once_its_file_is_attached(on_garden_page);
}

#[test]
fn the_garden_page_draws_the_new_picture_when_its_file_is_replaced_under_the_same_name() {
    draws_the_new_picture_when_its_file_is_replaced_under_the_same_name(on_garden_page);
}
