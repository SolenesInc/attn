use std::process::Command;
use std::sync::mpsc;

use attn_automation::{Host, Inspection, WindowInfo};
use attn_client::Pulse;
use rune::AppHandle;

use crate::State;

/// The automation host for the app's window: inspections run on the UI thread through
/// `AppHandle::inspect`, and `frames` beats from the app's `on_frame_encoded`.
pub struct WindowHost {
    pub handle: AppHandle<State>,
    pub frames: Pulse,
}

impl Host<State> for WindowHost {
    fn inspect(&self, f: Inspection<State>) {
        self.handle.inspect(f);
    }

    fn frames(&self) -> &Pulse {
        &self.frames
    }

    fn window(&self) -> Option<WindowInfo> {
        let (reply, answer) = mpsc::channel();
        self.handle.inspect(move |_| {
            let _ = reply.send(crate::frame::main_window());
        });
        answer.recv().ok().flatten()
    }

    fn shot(&self, path: &str) -> Result<(), String> {
        let window = self
            .window()
            .ok_or("shot: the app has no window open to capture")?;
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
