use std::path::Path;
use std::sync::Mutex;

use rune::{FrameEncoded, FrameOutcome};
use rune_perf::{Config, Frame, Outcome, Recorder};

static RECORDER: Mutex<Option<Recorder>> = Mutex::new(None);

pub fn start(debug_dir: &Path) {
    let build = option_env!("ATTN_BUILD_GIT_COMMIT").unwrap_or("unknown");
    match Recorder::start(Config::new(debug_dir.join("perf"), "attn").build(build)) {
        Ok(recorder) => *recording() = Some(recorder),
        Err(why) => eprintln!("attn-app: frames go unrecorded: {why}"),
    }
}

/// Writes what the recorder holds to `perf.json` and stops it. The app's quit hook calls it,
/// since AppKit ends the process on Quit without returning from the event loop.
pub fn stop() {
    if let Some(recorder) = recording().take() {
        let _ = recorder.shutdown();
    }
}

fn recording() -> std::sync::MutexGuard<'static, Option<Recorder>> {
    RECORDER
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
}

pub fn record(encoded: &FrameEncoded) {
    let Some(recorder) = &*recording() else {
        return;
    };
    let outcome = match encoded.outcome() {
        FrameOutcome::Drawn => Outcome::Drawn,
        FrameOutcome::Unchanged => Outcome::Unchanged,
        _ => Outcome::Skipped,
    };
    recorder.record(
        Frame::new(window_id(encoded.window()), encoded.seq(), outcome)
            .update_ms(encoded.update_ms())
            .render_ms(encoded.encode_ms())
            .allocated_bytes(rune_perf::take_allocated_bytes()),
    );
}

fn window_id(key: Option<&str>) -> u64 {
    key.unwrap_or_default()
        .bytes()
        .fold(0xcbf2_9ce4_8422_2325, |hash, byte| {
            (hash ^ u64::from(byte)).wrapping_mul(0x0100_0000_01b3)
        })
}
