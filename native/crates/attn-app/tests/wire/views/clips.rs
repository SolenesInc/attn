//! A view's sound clips: read through the daemon while drawn, played, paused and moved by keys.
//! Rune plays a clip only in a macOS build, so these tests ask the build's OS.

use super::*;

#[cfg(target_os = "macos")]
const COMPARE: &str =
    include_str!("../../../../../../internal/viewlang/testdata/v1/views/compare.json");

/// The transcript check: one clip at a time, each with what the agent heard.
fn check_transcripts() -> ViewDocument {
    ViewDocument {
        language: 1,
        parts: parts_of(CHECK_TRANSCRIPTS),
        sources: vec![source("clips.jsonl", false)],
        values: vec![ViewValues {
            source: "clips.jsonl".into(),
            values: json!([
                {"id": "clip-03", "file": "clips/03.wav", "transcript": "book a table for two"},
                {"id": "clip-05", "file": "clips/05.wav", "transcript": "at eight"},
            ]),
        }],
        ..ViewDocument::default()
    }
}

/// The clip the one-by-one shows, as assistive tech reads it.
fn clip(world: &World, item: usize) -> String {
    heard(world, &format!("tile.m.view.1.{item}.0"))
}

/// What the app shows for the clip keyed `key`, as assistive tech reads it.
fn heard(world: &World, key: &str) -> String {
    let node = world.screen().get(Target::key(key));
    format!(
        "{}: {}",
        node.name.unwrap_or_default(),
        node.value.unwrap_or_default()
    )
}

fn read_clips(world: &World) -> Vec<(Option<String>, String)> {
    world
        .daemon
        .sent_of("tile_asset_read")
        .iter()
        .map(|r| {
            (
                r["host"].as_str().map(str::to_owned),
                r["path"].as_str().unwrap_or_default().to_owned(),
            )
        })
        .collect()
}

#[cfg(target_os = "linux")]
#[test]
fn on_linux_a_clip_shows_its_name_and_says_audio_does_not_play_there() {
    let mut world = start();
    show(&mut world, check_transcripts());
    assert_eq!(clip(&world, 0), "clip-03: audio does not play on Linux yet");
    press(&mut world, Key::Text(" ".into()));
    assert!(
        read_clips(&world).is_empty(),
        "the app read a clip it cannot play: {:?}",
        read_clips(&world)
    );
}

/// A WAV file of `seconds` of silence, 8 kHz mono 16-bit, as base64.
#[cfg(target_os = "macos")]
fn silent_wav(seconds: u32) -> String {
    use base64::Engine;
    let (rate, data) = (8000u32, 8000 * 2 * seconds);
    let mut wav = Vec::new();
    wav.extend(b"RIFF");
    wav.extend((36 + data).to_le_bytes());
    wav.extend(b"WAVEfmt ");
    wav.extend(16u32.to_le_bytes());
    wav.extend(1u16.to_le_bytes());
    wav.extend(1u16.to_le_bytes());
    wav.extend(rate.to_le_bytes());
    wav.extend((rate * 2).to_le_bytes());
    wav.extend(2u16.to_le_bytes());
    wav.extend(16u16.to_le_bytes());
    wav.extend(b"data");
    wav.extend(data.to_le_bytes());
    wav.resize(wav.len() + data as usize, 0);
    base64::engine::general_purpose::STANDARD.encode(wav)
}

#[cfg(target_os = "macos")]
fn answer_clips(world: &mut World) {
    let wav = silent_wav(20);
    world.daemon.on("tile_asset_read", move |r| {
        serde_json::to_value(attn_protocol::TileAssetReadResultMessage {
            request_id: r["request_id"].as_str().map(str::to_owned),
            success: true,
            data_uri: Some(format!("data:audio/wav;base64,{wav}")),
            ..attn_protocol::TileAssetReadResultMessage::new()
        })
        .unwrap()
    });
}

#[cfg(target_os = "macos")]
fn shift(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods {
                shift: true,
                ..Mods::default()
            },
        })
    });
    world.settle();
}

#[cfg(target_os = "macos")]
#[test]
fn space_plays_and_pauses_the_shown_clip_and_shift_arrows_move_it() {
    let mut world = start();
    answer_clips(&mut world);
    show(&mut world, check_transcripts());
    assert_eq!(
        read_clips(&world),
        [(None, "/work/a/release/clips/03.wav".to_owned())],
        "the app reads only the clip the one-by-one shows"
    );
    assert_eq!(clip(&world, 0), "clip-03: Paused, 0:00 / 0:20");

    press(&mut world, Key::Text(" ".into()));
    world.advance(Duration::from_millis(3200));
    assert_eq!(clip(&world, 0), "clip-03: Playing, 0:03 / 0:20");
    let now = world.now().as_secs_f64();
    let wake = world.runtime().wake_deadline().map(|at| at - now);
    assert!(
        wake.is_some_and(|s| s <= 1.0),
        "a playing clip's time stands still: the window wakes in {wake:?} s"
    );

    shift(&mut world, Key::Right);
    assert_eq!(clip(&world, 0), "clip-03: Playing, 0:08 / 0:20");
    shift(&mut world, Key::Left);
    shift(&mut world, Key::Left);
    assert_eq!(clip(&world, 0), "clip-03: Playing, 0:00 / 0:20");

    press(&mut world, Key::Text(" ".into()));
    assert_eq!(clip(&world, 0), "clip-03: Paused, 0:00 / 0:20");
    world.advance(Duration::from_secs(5));
    assert_eq!(clip(&world, 0), "clip-03: Paused, 0:00 / 0:20");
    let now = world.now().as_secs_f64();
    let wake = world.runtime().wake_deadline().map(|at| at - now);
    assert!(
        !world.runtime().animating && wake.is_none_or(|s| s > 58.0),
        "a paused clip repaints the view: it wakes in {wake:?} s"
    );

    typed(&mut world, "j");
    press(&mut world, Key::Enter);
    assert_eq!(clip(&world, 1), "clip-05: Paused, 0:00 / 0:20");
    assert_eq!(
        read_clips(&world).last(),
        Some(&(None, "/work/a/release/clips/05.wav".to_owned()))
    );
}

#[cfg(target_os = "macos")]
#[test]
fn a_clip_in_a_view_on_an_outpost_is_read_from_that_outpost() {
    let mut world = start();
    answer_clips(&mut world);
    world.daemon.emit(DesktopTileContentMessage {
        host: Some("far".into()),
        ..content(Some(check_transcripts()), None)
    });
    world.settle();
    assert_eq!(
        read_clips(&world),
        [(
            Some("far".to_owned()),
            "/work/a/release/clips/03.wav".to_owned()
        )]
    );
    press(&mut world, Key::Text(" ".into()));
    world.advance(Duration::from_millis(1200));
    assert_eq!(clip(&world, 0), "clip-03: Playing, 0:01 / 0:20");
}

/// Whether the window wakes within a minute while nothing happens.
#[cfg(target_os = "macos")]
fn wakes_soon(world: &World) -> Option<f64> {
    let now = world.now().as_secs_f64();
    let wake = world.runtime().wake_deadline().map(|at| at - now);
    wake.filter(|s| *s <= 58.0)
}

#[cfg(target_os = "macos")]
#[test]
fn a_clip_that_could_not_be_read_is_read_again_when_its_view_arrives_again() {
    let mut world = start();
    let wav = silent_wav(20);
    let asked = Arc::new(Mutex::new(0));
    let count = asked.clone();
    world.daemon.on("tile_asset_read", move |r| {
        let mut asked = count.lock().unwrap();
        *asked += 1;
        let found = *asked > 1;
        serde_json::to_value(attn_protocol::TileAssetReadResultMessage {
            request_id: r["request_id"].as_str().map(str::to_owned),
            success: found,
            data_uri: found.then(|| format!("data:audio/wav;base64,{wav}")),
            error: (!found).then(|| "no such file".to_owned()),
            ..attn_protocol::TileAssetReadResultMessage::new()
        })
        .unwrap()
    });
    show(&mut world, check_transcripts());
    assert!(
        clip(&world, 0).contains("no such file"),
        "{}",
        clip(&world, 0)
    );

    show(&mut world, check_transcripts());
    assert_eq!(clip(&world, 0), "clip-03: Paused, 0:00 / 0:20");
    assert_eq!(*asked.lock().unwrap(), 2);
}

#[cfg(target_os = "macos")]
#[test]
fn a_clip_stops_when_the_user_leaves_its_desktop() {
    let mut world = start();
    answer_clips(&mut world);
    show(&mut world, check_transcripts());
    press(&mut world, Key::Text(" ".into()));
    world.advance(Duration::from_millis(1200));
    assert_eq!(clip(&world, 0), "clip-03: Playing, 0:01 / 0:20");

    let on = |current: &str, revision: i64| attn_protocol::ProfileArrangementChangedMessage {
        profile: Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: current.into(),
            ..Profile::default()
        },
        desktops: vec![
            Desktop {
                id: DESKTOP.into(),
                profile_id: "work".into(),
                name: "Desktop 1".into(),
                order_key: "1".into(),
                tree_json: row(vec![
                    pane("a"),
                    json!({"type": "tile", "tile_id": "m", "tile_kind": "markdown",
                        "tile_params": json!({ "path": VIEW }).to_string()}),
                ])
                .to_string(),
                active_pane_id: "m".into(),
                panes: vec![DesktopPane {
                    desktop_id: DESKTOP.into(),
                    pane_id: "a".into(),
                    runtime_id: "r-a".into(),
                    session_id: "s-a".into(),
                    kind: "agent".into(),
                    ..DesktopPane::default()
                }],
                revision,
                ..Desktop::default()
            },
            Desktop {
                id: "work/desktop_2".into(),
                profile_id: "work".into(),
                name: "Desktop 2".into(),
                order_key: "2".into(),
                tree_json: attn_testkit::tree::panes_in_a_row(&[]),
                revision,
                ..Desktop::default()
            },
        ],
        ..attn_protocol::ProfileArrangementChangedMessage::new()
    };
    world.daemon.emit(on("work/desktop_2", 2));
    world.settle();
    world.advance(Duration::from_secs(3));
    assert_eq!(
        wakes_soon(&world),
        None,
        "a clip on a desktop the user left still plays"
    );

    world.daemon.emit(on(DESKTOP, 3));
    world.settle();
    assert_eq!(clip(&world, 0), "clip-03: Paused, 0:00 / 0:20");
    assert_eq!(read_clips(&world).len(), 2);
}

#[cfg(target_os = "macos")]
#[test]
fn the_keys_reach_either_clip_of_a_compare() {
    let mut world = start();
    answer_clips(&mut world);
    show(
        &mut world,
        ViewDocument {
            language: 1,
            parts: parts_of(&COMPARE.replace(".png", ".wav")),
            ..ViewDocument::default()
        },
    );
    typed(&mut world, "j");
    typed(&mut world, "j");
    press(&mut world, Key::Text(" ".into()));
    world.advance(Duration::from_millis(1200));
    assert_eq!(
        heard(&world, "tile.m.view.1.0"),
        "Before: Paused, 0:00 / 0:20"
    );
    assert_eq!(
        heard(&world, "tile.m.view.1.1"),
        "After: Playing, 0:01 / 0:20"
    );
}

#[cfg(target_os = "macos")]
#[test]
fn a_window_that_closes_leaves_another_windows_clip_playing() {
    let mut kept = start();
    answer_clips(&mut kept);
    show(&mut kept, check_transcripts());
    let mut closed = start();
    answer_clips(&mut closed);
    show(&mut closed, check_transcripts());
    drop(closed);

    press(&mut kept, Key::Text(" ".into()));
    kept.advance(Duration::from_millis(1200));
    assert_eq!(clip(&kept, 0), "clip-03: Playing, 0:01 / 0:20");
}
