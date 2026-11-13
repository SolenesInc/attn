use std::process::Command;
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::mpsc;
use std::time::Duration;

use attn_automation::{Bounds, Host, Inspection, WindowBounds, WindowInfo};
use attn_client::{Clock, Pulse};
use rune::{AppHandle, Runtime};

use crate::State;
use crate::desktop::layout::Frame;

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

    fn tile_windows(&self) -> Vec<(String, u64)> {
        self.read(|_| crate::frame::tile_windows())
            .unwrap_or_default()
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

    fn set_window_bounds(&self, asked: Bounds, wait: Duration) -> Result<WindowBounds, String> {
        const CLOSED: &str = "set_window_bounds: the app closed before it answered";
        self.read(move |_| crate::frame::move_main(asked))
            .ok_or(CLOSED)?
            .map_err(|e| format!("set_window_bounds: {e}"))?;
        let clock = Clock::system();
        loop {
            let seen = self.frames.count();
            let (got, laid_out) = self
                .read(|rt| (crate::frame::main_bounds(), rt.window_viewport()))
                .ok_or(CLOSED)?;
            let got = got.ok_or("set_window_bounds: the app has no window open")?;
            let at = got.logical_bounds;
            let reached = [
                (at.width, asked.width),
                (at.height, asked.height),
                (f64::from(laid_out.0), at.width),
                (f64::from(laid_out.1), at.height),
            ]
            .iter()
            .all(|(a, b)| (a - b).abs() <= 1.0);
            if reached {
                return Ok(got);
            }
            let left = wait.saturating_sub(clock.now());
            if left.is_zero() || !self.frames.wait_past(seen, left) {
                return Err(format!(
                    "set_window_bounds: asked for {asked:?}; after {} ms (timeout_ms) the window is at {at:?} and its view lays out at {} x {} points; the window's minimum size or the screen may hold its size back",
                    wait.as_millis(),
                    laid_out.0,
                    laid_out.1
                ));
            }
        }
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

/// Opens tile windows as native windows beside the main one, each bound to the main window's
/// store and sending on its link.
pub struct NativeOpener {
    pub store: attn_client::Store,
    pub daemon: attn_client::Commands,
    pub epoch_ms: i64,
}

struct NativeWindow {
    window: rune::WindowHandle,
    handle: AppHandle<State>,
    _bound: attn_client::Subscription,
}

impl crate::desktop::Opener for NativeOpener {
    fn open(
        &self,
        key: &str,
        frame: &Frame,
        setup: crate::desktop::Setup,
    ) -> Box<dyn crate::desktop::Opened> {
        let app = crate::tile_window_app((frame.width as f32, frame.height as f32));
        let handle = app.handle();
        let created = *frame;
        let spec = rune::WindowSpec::from_app(key, app)
            .options(rune::WindowOptions {
                visible: false,
                min_size: Some((320.0, 200.0)),
                ..rune::WindowOptions::default()
            })
            .on_created(move |window| place(&window, &created));
        let window = rune::open_window(spec);
        crate::frame::tile_window_opened(&window);
        let placed = window.clone();
        handle.update(setup.into_update(Box::new(move || content_frame(&placed))));
        let bound = crate::bind(
            &self.store,
            self.daemon.clone(),
            crate::TerminalInbox::default(),
            crate::ReplyInbox::default(),
            self.epoch_ms,
            {
                let handle = handle.clone();
                move |update| handle.update(update)
            },
        );
        Box::new(NativeWindow {
            window,
            handle,
            _bound: bound,
        })
    }
}

impl crate::desktop::Opened for NativeWindow {
    fn place(&self, frame: &Frame) {
        place(&self.window, frame);
    }

    fn hide(&self) {
        self.window.hide();
    }

    fn update(&self, update: crate::Update) {
        self.handle.update(update);
    }
}

/// Moves `window`'s content to `frame` and shows it. Before the native window exists, the
/// window's `on_created` places it.
fn place(window: &rune::WindowHandle, frame: &Frame) {
    let Some(p) = window.placement() else {
        return;
    };
    let (dx, dy) = (
        p.content_position.0 - p.position.0,
        p.content_position.1 - p.position.1,
    );
    window.set_position((
        (frame.x * p.scale) as i32 - dx,
        (frame.y * p.scale) as i32 - dy,
    ));
    window.resize_instant((frame.width as f32, frame.height as f32));
    window.show();
}

/// Where `window`'s content is on the desktop, in points.
fn content_frame(window: &rune::WindowHandle) -> Option<Frame> {
    let p = window.placement()?;
    Some(Frame {
        x: f64::from(p.content_position.0) / p.scale,
        y: f64::from(p.content_position.1) / p.scale,
        width: f64::from(p.content_size.0) / p.scale,
        height: f64::from(p.content_size.1) / p.scale,
    })
}
