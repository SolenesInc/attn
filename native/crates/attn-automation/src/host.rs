use std::time::Duration;

use attn_client::Pulse;
use rune::Runtime;

/// Work a host runs on the UI thread with its runtime.
pub type Inspection<S> = Box<dyn FnOnce(&mut Runtime<S>) + Send>;

/// What runs the app for the automation server: the window (`AppHandle::inspect`), a
/// windowless process, or a wire test.
pub trait Host<S: 'static>: Send + Sync + 'static {
    /// Runs `f` with the runtime, handling the input it queues before the next inspection.
    fn inspect(&self, f: Inspection<S>);

    /// Beats when the screen may have changed: after each frame the runtime steps, drawn or
    /// not, except a frame an inspection asked for that neither handled input nor ended a
    /// motion, and after each change from the daemon reaches the state, also in a window that
    /// draws nothing while covered. A request waiting for an element to appear or stop moving
    /// wakes then and sleeps otherwise.
    fn frames(&self) -> &Pulse;

    /// The window a driver outside the process aims at, for hosts that have one.
    fn window(&self) -> Option<WindowInfo> {
        None
    }

    /// The windows popped-out tiles show in beside the main one, by their keys, with their
    /// platform ids as [`WindowInfo::id`]. Whether one shows is the platform's to say.
    fn tile_windows(&self) -> Vec<(String, u64)> {
        Vec::new()
    }

    /// Writes a screenshot of the window to `path` once it has drawn what the runtime last
    /// built, waiting up to `wait` for that frame.
    fn shot(&self, _path: &str, _wait: Duration) -> Result<(), String> {
        Err("this host draws no window to capture".into())
    }
}

/// The app's window as the platform names it: the CGWindowID on macOS, the X11 window id on
/// Linux.
#[derive(Clone, Copy, Debug, serde::Serialize)]
pub struct WindowInfo {
    pub id: u64,
    pub mapped: bool,
}
