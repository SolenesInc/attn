use std::process::Command;
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::mpsc;
use std::time::Duration;

use attn_automation::{Host, Inspection, WindowInfo};
use attn_client::{Clock, Pulse};
use rune::{AppHandle, Runtime};

use crate::State;

/// The automation host for the app's window: inspections run on the UI thread through
/// `AppHandle::inspect`. `frames` beats from the app's `on_frame_encoded` and
/// `on_frame_drawn`, and from `attn_app::connect`; `drawn` is the runtime's commit count as
/// its last drawn frame presented it.
pub struct WindowHost {
    pub handle: AppHandle<State>,
    pub frames: Pulse,
    pub drawn: Arc<AtomicU64>,
}

impl WindowHost {
    fn read<T: Send + 'static>(
        &self,
        f: impl FnOnce(&Runtime<State>) -> T + Send + 'static,
    ) -> Option<T> {
        let (reply, answer) = mpsc::channel();
        self.handle.inspect(move |rt| {
            let _ = reply.send(f(rt));
        });
        answer.recv().ok()
    }

    fn draws(&self, commits: u64, wait: Duration) -> Result<(), String> {
        let clock = Clock::system();
        loop {
            let seen = self.frames.count();
            if self.drawn.load(Ordering::Acquire) >= commits {
                return Ok(());
            }
            let left = wait.saturating_sub(clock.now());
            if left.is_zero() || !self.frames.wait_past(seen, left) {
                return Err(format!(
                    "shot: waited {} ms (timeout_ms) for the window to draw its latest view; a covered or minimised window draws nothing until it shows",
                    wait.as_millis()
                ));
            }
        }
    }
}

impl Host<State> for WindowHost {
    fn inspect(&self, f: Inspection<State>) {
        self.handle.inspect(f);
    }

    fn frames(&self) -> &Pulse {
        &self.frames
    }

    fn window(&self) -> Option<WindowInfo> {
        self.read(|_| crate::frame::main_window()).flatten()
    }

    fn shot(&self, path: &str, wait: Duration) -> Result<(), String> {
        let (window, commits) = self
            .read(|rt| (crate::frame::main_window(), rt.stats.commits))
            .ok_or("shot: the app closed before it answered")?;
        let window = window.ok_or("shot: the app has no window open to capture")?;
        self.draws(commits, wait)?;
        if let Some(dir) = std::path::Path::new(path).parent() {
            std::fs::create_dir_all(dir).map_err(|e| format!("create {}: {e}", dir.display()))?;
        }
        let mut capture = capture(window.id, path);
        let program = capture.get_program().to_string_lossy().into_owned();
        let output = capture
            .output()
            .map_err(|e| format!("shot: run {program}: {e}"))?;
        if !output.status.success() {
            return Err(format!(
                "shot: {program} failed for window {}: {}{}",
                window.id,
                String::from_utf8_lossy(&output.stderr).trim(),
                CAPTURE_PERMISSION
            ));
        }
        Ok(())
    }
}

#[cfg(target_os = "macos")]
const CAPTURE_PERMISSION: &str = "; macOS refuses window captures to an app without Screen Recording permission (System Settings, Privacy & Security): grant it to this app, or capture from a process that has it with `screencapture -l<id>` and the id `native_windows` reports";
#[cfg(not(target_os = "macos"))]
const CAPTURE_PERMISSION: &str = "";

#[cfg(target_os = "macos")]
fn capture(window: u64, path: &str) -> Command {
    let mut command = Command::new("/usr/sbin/screencapture");
    command.args(["-x", "-o", &format!("-l{window}"), path]);
    command
}

#[cfg(not(target_os = "macos"))]
fn capture(window: u64, path: &str) -> Command {
    let mut command = Command::new("import");
    command.args(["-window", &window.to_string(), path]);
    command
}
