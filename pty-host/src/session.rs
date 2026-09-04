use std::collections::HashMap;
use std::fs::{self, File, OpenOptions};
use std::io::{ErrorKind, Read, Write};
use std::os::fd::{AsRawFd, FromRawFd, RawFd};
use std::os::unix::fs::OpenOptionsExt;
use std::os::unix::net::UnixStream;
use std::os::unix::process::CommandExt;
use std::process::{Command, Stdio};
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::mpsc::{SyncSender, TrySendError};
use std::sync::{Arc, Condvar, Mutex, Weak};
use std::thread;
use std::time::{Duration, Instant};

use base64::Engine;
use base64::engine::general_purpose::STANDARD as BASE64;
use serde::Serialize;
use serde_json::{Value, json};

use crate::boundary::safe_boundary;
use crate::ghostty::{Terminal, Theme};
use crate::protocol::{
    PreparedLaunchAttempt, SpawnParams, desync_event, exit_event, kitty_placements_event,
    output_event, state_event,
};
use crate::queries::{
    ColorScheme, TerminalQueries, color_scheme_report, theme_color_scheme,
    track_color_scheme_reports,
};
use crate::signals::SignalObserver;
use crate::wire::{WireFeeder, mint_epoch};

const READER_STACK_BYTES: usize = 256 * 1024;
const EXITED_SESSION_TTL: Duration = Duration::from_secs(45);
const TERMINATE_TIMEOUT: Duration = Duration::from_secs(10);
const SIGTERM_TO_HUP_GRACE: Duration = Duration::from_secs(2);

pub type Cleanup = Arc<dyn Fn(String) + Send + Sync>;
pub type Broadcast = Arc<dyn Fn(Value) + Send + Sync>;

pub struct SessionRuntime {
    cleanup: Cleanup,
    broadcast: Broadcast,
    reaper: ChildReaper,
}

impl SessionRuntime {
    pub fn new(cleanup: Cleanup, broadcast: Broadcast, reaper: ChildReaper) -> Self {
        Self {
            cleanup,
            broadcast,
            reaper,
        }
    }
}

#[derive(Clone)]
pub struct ChildReaper {
    shared: Arc<ChildReaperShared>,
}

struct ChildReaperShared {
    sessions: Mutex<HashMap<i32, Weak<Session>>>,
}

impl ChildReaper {
    pub fn start() -> Result<Self, String> {
        block_sigchld()?;
        let shared = Arc::new(ChildReaperShared {
            sessions: Mutex::new(HashMap::new()),
        });
        let thread_shared = Arc::clone(&shared);
        thread::Builder::new()
            .name("pty-child-reaper".to_owned())
            .stack_size(64 * 1024)
            .spawn(move || reap_children(&thread_shared))
            .map_err(|error| format!("start child reaper: {error}"))?;
        Ok(Self { shared })
    }

    fn register(&self, session: &Arc<Session>) {
        self.shared
            .sessions
            .lock()
            .expect("child reaper mutex poisoned")
            .insert(session.child_pid, Arc::downgrade(session));
        let wake_result = unsafe { libc::kill(libc::getpid(), libc::SIGCHLD) };
        if wake_result != 0 {
            eprintln!(
                "PTY host child reaper wake failed: {}",
                std::io::Error::last_os_error()
            );
        }
    }
}

#[derive(Serialize)]
pub struct RegistryEntry {
    pub version: u8,
    pub daemon_instance_id: String,
    pub session_id: String,
    pub worker_pid: u32,
    pub child_pid: i32,
    pub socket_path: String,
    pub agent: String,
    pub cwd: String,
    pub started_at: String,
    pub control_token: String,
    pub launch_params_recorded: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub yolo_mode: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub approval_route: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub executable: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub claude_executable: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub codex_executable: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub copilot_executable: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub model: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub effort: String,
    #[serde(skip_serializing_if = "Value::is_null")]
    pub unattended_launch: Value,
    pub runtime_kind: &'static str,
}

#[allow(clippy::trivially_copy_pass_by_ref)]
fn is_false(value: &bool) -> bool {
    !*value
}

struct Model {
    wire: WireFeeder,
    signals: SignalObserver,
    theme: Theme,
    reported_scheme: ColorScheme,
    color_scheme_reports: bool,
    seq: u32,
    cols: u16,
    rows: u16,
    cell_width: u16,
    cell_height: u16,
    pixel_width: u16,
    pixel_height: u16,
}

struct Lifecycle {
    running: bool,
    state: String,
    state_detail: String,
    exit_code: Option<i32>,
    exit_signal: Option<String>,
}

struct Subscriber {
    connection_id: String,
    sender: SyncSender<Value>,
    shutdown: UnixStream,
}

pub struct Session {
    pub id: String,
    pub agent: String,
    pub cwd: String,
    pub child_pid: i32,
    pub attempt_index: usize,
    pub registry_path: String,

    master: Mutex<Option<File>>,
    model: Mutex<Model>,
    lifecycle: Mutex<Lifecycle>,
    lifecycle_changed: Condvar,
    subscribers: Mutex<HashMap<String, Subscriber>>,
    watchers: Mutex<HashMap<String, SyncSender<Value>>>,
    cleanup_dir: String,
    cleanup: Cleanup,
    broadcast: Broadcast,
    connections: AtomicUsize,
    cleanup_scheduled: AtomicBool,
    cleaned: AtomicBool,
}

impl Session {
    pub fn spawn(
        params: SpawnParams,
        registry_path: String,
        daemon_instance_id: &str,
        socket_path: &str,
        control_token: &str,
        runtime: SessionRuntime,
    ) -> Result<Arc<Self>, String> {
        let cols = params.cols.max(1);
        let rows = params.rows.max(1);
        if params.session_id.trim().is_empty() {
            return Err("missing session id".to_owned());
        }
        if params.attempts.is_empty() {
            return Err("no prepared launch attempts".to_owned());
        }

        let mut terminal = Terminal::new(cols, rows)?;
        terminal.set_theme(&params.theme)?;
        let theme = params.theme.clone();
        let reported_scheme = theme_color_scheme(&theme);
        let (master, child_pid, attempt_index) = spawn_attempts(&params.attempts, cols, rows)?;
        cleanup_unused_attempts(&params.attempts, attempt_index);

        let pty_reader = master
            .try_clone()
            .map_err(|error| format!("clone PTY master: {error}"))?;
        let SessionRuntime {
            cleanup,
            broadcast,
            reaper,
        } = runtime;
        let cleanup_dir = params.attempts[attempt_index].cleanup_dir.clone();
        let session = Arc::new(Self {
            id: params.session_id.clone(),
            agent: params.agent.clone(),
            cwd: params.cwd.clone(),
            child_pid,
            attempt_index,
            registry_path,
            master: Mutex::new(Some(master)),
            model: Mutex::new(Model {
                wire: WireFeeder::new(terminal, mint_epoch()),
                signals: SignalObserver::new(&params.agent),
                theme,
                reported_scheme,
                color_scheme_reports: false,
                seq: 0,
                cols,
                rows,
                cell_width: 0,
                cell_height: 0,
                pixel_width: 0,
                pixel_height: 0,
            }),
            lifecycle: Mutex::new(Lifecycle {
                running: true,
                state: "working".to_owned(),
                state_detail: String::new(),
                exit_code: None,
                exit_signal: None,
            }),
            lifecycle_changed: Condvar::new(),
            subscribers: Mutex::new(HashMap::new()),
            watchers: Mutex::new(HashMap::new()),
            cleanup_dir,
            cleanup,
            broadcast,
            connections: AtomicUsize::new(0),
            cleanup_scheduled: AtomicBool::new(false),
            cleaned: AtomicBool::new(false),
        });

        let entry = RegistryEntry {
            version: 1,
            daemon_instance_id: daemon_instance_id.to_owned(),
            session_id: params.session_id,
            worker_pid: std::process::id(),
            child_pid,
            socket_path: socket_path.to_owned(),
            agent: params.agent,
            cwd: params.cwd,
            started_at: unix_timestamp().to_string(),
            control_token: control_token.to_owned(),
            launch_params_recorded: true,
            yolo_mode: params.yolo_mode,
            approval_route: params.approval_route,
            executable: params.executable,
            claude_executable: params.claude_executable,
            codex_executable: params.codex_executable,
            copilot_executable: params.copilot_executable,
            model: params.model,
            effort: params.effort,
            unattended_launch: params.unattended_launch,
            runtime_kind: "rust_host",
        };
        if let Err(error) = write_registry(&session.registry_path, &entry) {
            abort_spawn(&session);
            return Err(error);
        }

        if let Err(error) = start_reader(Arc::clone(&session), pty_reader) {
            abort_spawn(&session);
            return Err(error);
        }
        reaper.register(&session);
        Ok(session)
    }

    pub fn note_connected(&self) {
        self.connections.fetch_add(1, Ordering::AcqRel);
    }

    pub fn note_disconnected(self: &Arc<Self>) {
        let previous = self.connections.fetch_sub(1, Ordering::AcqRel);
        debug_assert!(previous > 0);
        if previous == 1 && !self.is_running() {
            self.schedule_cleanup();
        }
    }

    pub fn info(&self) -> Value {
        let model = self.model.lock().expect("model mutex poisoned");
        let lifecycle = self.lifecycle.lock().expect("lifecycle mutex poisoned");
        let mut result = json!({
            "running": lifecycle.running,
            "agent": self.agent,
            "cwd": self.cwd,
            "cols": model.cols,
            "rows": model.rows,
            "worker_pid": std::process::id(),
            "child_pid": self.child_pid,
            "last_seq": model.seq,
            "state": lifecycle.state
        });
        if !lifecycle.state.is_empty() {
            result["last_signal_claim"] = Value::String(lifecycle.state.clone());
            result["last_signal_source"] = Value::String("heartbeat".to_owned());
            result["last_signal_detail"] = Value::String(lifecycle.state_detail.clone());
        }
        add_exit_fields(&mut result, &lifecycle);
        result
    }

    pub fn attach(
        &self,
        subscriber_id: String,
        connection_id: String,
        sender: SyncSender<Value>,
        shutdown: UnixStream,
        omit_replay: bool,
        expected_snapshot_format: &str,
    ) -> Value {
        let mut model = self.model.lock().expect("model mutex poisoned");
        let portable_replay = !omit_replay
            && !expected_snapshot_format.is_empty()
            && expected_snapshot_format != env!("ATTN_PTY_HOST_SNAPSHOT_FORMAT");
        if portable_replay {
            model.seq = model.seq.wrapping_add(1);
            let mut replay = b"\x1bc".to_vec();
            replay.extend(model.wire.terminal().vt_dump());
            let _ = sender.try_send(output_event(&self.id, model.seq, &BASE64.encode(replay)));
        }
        let replaced = self
            .subscribers
            .lock()
            .expect("subscribers mutex poisoned")
            .insert(
                subscriber_id,
                Subscriber {
                    connection_id,
                    sender,
                    shutdown,
                },
            );
        if let Some(replaced) = replaced {
            let _ = replaced.shutdown.shutdown(std::net::Shutdown::Both);
        }
        let lifecycle = self.lifecycle.lock().expect("lifecycle mutex poisoned");
        let mut result = json!({
            "last_seq": model.seq,
            "cols": model.cols,
            "rows": model.rows,
            "pid": self.child_pid,
            "running": lifecycle.running,
            "ghostty_snapshot_format": env!("ATTN_PTY_HOST_SNAPSHOT_FORMAT"),
            "ghostty_blocks": model.wire.snapshot_blocks(),
            "ghostty_placements": model.wire.snapshot_placements().unwrap_or_default(),
            "ghostty_scrollback_truncated": false
        });
        if !omit_replay && !portable_replay {
            let snapshot = model.wire.terminal().snapshot();
            if !snapshot.is_empty() {
                result["ghostty_snapshot"] = Value::String(BASE64.encode(snapshot));
            }
        }
        add_exit_fields(&mut result, &lifecycle);
        result
    }

    pub fn detach(&self, subscriber_id: &str, connection_id: &str) {
        let mut subscribers = self.subscribers.lock().expect("subscribers mutex poisoned");
        let owned = subscribers
            .get(subscriber_id)
            .is_some_and(|subscriber| subscriber.connection_id == connection_id);
        if owned {
            subscribers.remove(subscriber_id);
        }
    }

    #[allow(clippy::needless_pass_by_value)]
    pub fn watch(&self, watcher_id: String, sender: SyncSender<Value>) -> Value {
        self.watchers
            .lock()
            .expect("watchers mutex poisoned")
            .insert(watcher_id, sender.clone());
        let lifecycle = self.lifecycle.lock().expect("lifecycle mutex poisoned");
        let _ = sender.try_send(state_event(
            &self.id,
            &lifecycle.state,
            &lifecycle.state_detail,
            "worker_info",
        ));
        if !lifecycle.running {
            let _ = sender.try_send(exit_event(
                &self.id,
                lifecycle.exit_code.unwrap_or_default(),
                lifecycle.exit_signal.as_deref(),
            ));
        }
        json!({"ok": true})
    }

    pub fn unwatch(&self, watcher_id: &str) {
        self.watchers
            .lock()
            .expect("watchers mutex poisoned")
            .remove(watcher_id);
    }

    pub fn lifecycle_events(&self) -> Vec<Value> {
        let lifecycle = self.lifecycle.lock().expect("lifecycle mutex poisoned");
        let mut events = vec![state_event(
            &self.id,
            &lifecycle.state,
            &lifecycle.state_detail,
            "worker_info",
        )];
        if !lifecycle.running {
            events.push(exit_event(
                &self.id,
                lifecycle.exit_code.unwrap_or_default(),
                lifecycle.exit_signal.as_deref(),
            ));
        }
        events
    }

    pub fn input(&self, data: &[u8]) -> Result<(), String> {
        if !self.is_running() {
            return Err("session not running".to_owned());
        }
        self.write_master(data)
    }

    pub fn resize(
        &self,
        cols: u16,
        rows: u16,
        mut xpixel: u16,
        mut ypixel: u16,
    ) -> Result<bool, String> {
        if cols == 0 || rows == 0 {
            return Err("cols and rows must be > 0".to_owned());
        }
        let mut model = self.model.lock().expect("model mutex poisoned");
        let mut cell_width = 0;
        let mut cell_height = 0;
        if xpixel > 0 && ypixel > 0 {
            cell_width = xpixel / cols;
            cell_height = ypixel / rows;
        }
        if cell_width == 0 || cell_height == 0 {
            cell_width = model.cell_width;
            cell_height = model.cell_height;
            if cell_width > 0 && cell_height > 0 {
                xpixel = cols.saturating_mul(cell_width);
                ypixel = rows.saturating_mul(cell_height);
            }
        }
        let changed = model.cols != cols
            || model.rows != rows
            || model.cell_width != cell_width
            || model.cell_height != cell_height
            || model.pixel_width != xpixel
            || model.pixel_height != ypixel;
        if !changed {
            return Ok(false);
        }
        model.wire.terminal_mut().resize_no_reflow(
            cols,
            rows,
            u32::from(cell_width.max(1)),
            u32::from(cell_height.max(1)),
        )?;
        let placements = model.wire.snapshot_placements();
        let placement_seq = model.seq;
        model.cols = cols;
        model.rows = rows;
        model.cell_width = cell_width;
        model.cell_height = cell_height;
        model.pixel_width = xpixel;
        model.pixel_height = ypixel;
        drop(model);

        if let Some(placements) = placements {
            self.broadcast_placements(placement_seq, &placements);
        }

        let winsize = libc::winsize {
            ws_row: rows,
            ws_col: cols,
            ws_xpixel: xpixel,
            ws_ypixel: ypixel,
        };
        let master = self.master.lock().expect("master mutex poisoned");
        if let Some(file) = master.as_ref() {
            let rc = unsafe { libc::ioctl(file.as_raw_fd(), libc::TIOCSWINSZ, &winsize) };
            if rc != 0 {
                return Err(format!("resize PTY: {}", std::io::Error::last_os_error()));
            }
        }
        Ok(true)
    }

    pub fn set_theme(&self, theme: &crate::ghostty::Theme) -> Result<(), String> {
        let report = {
            let mut model = self.model.lock().expect("model mutex poisoned");
            model.wire.terminal_mut().set_theme(theme)?;
            model.theme = theme.clone();
            let scheme = theme_color_scheme(theme);
            let report = model.color_scheme_reports && scheme != model.reported_scheme;
            model.reported_scheme = scheme;
            report.then(|| color_scheme_report(scheme).to_vec())
        };
        if let Some(report) = report {
            self.write_master(&report)?;
        }
        Ok(())
    }

    pub fn screen_snapshot(&self) -> Value {
        let model = self.model.lock().expect("model mutex poisoned");
        let lifecycle = self.lifecycle.lock().expect("lifecycle mutex poisoned");
        let screen = model.wire.terminal().viewport_vt();
        json!({
            "last_seq": model.seq,
            "cols": model.cols,
            "rows": model.rows,
            "running": lifecycle.running,
            "screen_snapshot": BASE64.encode(screen),
            "screen_text": model.wire.terminal().viewport_text(model.rows),
            "screen_cols": model.cols,
            "screen_rows": model.rows
        })
    }

    pub fn signal(&self, signal: i32) -> Result<(), String> {
        if !self.is_running() {
            return Ok(());
        }
        let signal = if self.is_shell() && signal == libc::SIGTERM {
            libc::SIGHUP
        } else {
            signal
        };
        if unsafe { libc::kill(-self.child_pid, signal) } != 0 {
            let error = std::io::Error::last_os_error();
            if error.raw_os_error() != Some(libc::ESRCH) {
                return Err(format!("signal process group {}: {error}", self.child_pid));
            }
        }
        let deadline = Instant::now() + TERMINATE_TIMEOUT;
        if signal == libc::SIGTERM && !self.wait_for_exit(SIGTERM_TO_HUP_GRACE) {
            self.publish_escalation(signal, libc::SIGHUP);
            let _ = unsafe { libc::kill(-self.child_pid, libc::SIGHUP) };
        }
        if !self.wait_for_exit(deadline.saturating_duration_since(Instant::now())) {
            self.publish_escalation(signal, libc::SIGKILL);
            let _ = unsafe { libc::kill(-self.child_pid, libc::SIGKILL) };
            if !self.wait_for_exit(TERMINATE_TIMEOUT) {
                return Err("timed out waiting for terminal process to exit".to_owned());
            }
        }
        Ok(())
    }

    pub fn remove(self: &Arc<Self>) {
        if let Err(error) = self.remove_checked() {
            eprintln!("terminal {} cleanup failed: {error}", self.id);
        }
    }

    pub fn remove_checked(self: &Arc<Self>) -> Result<(), String> {
        self.signal(libc::SIGTERM)?;
        self.finish_cleanup();
        Ok(())
    }

    fn publish_escalation(&self, requested: i32, escalated: i32) {
        let event = json!({"type": "evt", "event": "teardown_escalated", "session_id": self.id,
            "reason": signal_name(requested), "exit_signal": signal_name(escalated)});
        self.broadcast_watch(event.clone());
        (self.broadcast)(event);
    }

    fn wait_for_exit(&self, timeout: Duration) -> bool {
        let lifecycle = self.lifecycle.lock().expect("lifecycle mutex poisoned");
        if !lifecycle.running {
            return true;
        }
        let (lifecycle, _) = self
            .lifecycle_changed
            .wait_timeout_while(lifecycle, timeout, |state| state.running)
            .expect("lifecycle mutex poisoned");
        !lifecycle.running
    }

    pub fn is_running(&self) -> bool {
        self.lifecycle
            .lock()
            .expect("lifecycle mutex poisoned")
            .running
    }

    fn write_master(&self, data: &[u8]) -> Result<(), String> {
        let mut master = self.master.lock().expect("master mutex poisoned");
        let Some(file) = master.as_mut() else {
            return Err("session not running".to_owned());
        };
        file.write_all(data)
            .map_err(|error| format!("write PTY: {error}"))
    }

    fn observe_output(&self, data: &[u8]) {
        let (seq, wire, placements, resync, responses, observations) = {
            let mut model = self.model.lock().expect("model mutex poisoned");
            let queries = TerminalQueries::detect(data);
            track_color_scheme_reports(data, &mut model.color_scheme_reports);
            let mut responses = queries.replies_before_feed(&model.theme);
            let feed = model.wire.feed(data);
            let drained = model.wire.terminal_mut().drain_responses();
            responses.extend(queries.replies_after_feed(model.wire.terminal(), &drained));
            let observations = model.signals.observe(data);
            model.seq = model.seq.wrapping_add(1);
            (
                model.seq,
                feed.wire,
                feed.placements,
                feed.resync,
                responses,
                observations,
            )
        };
        if !responses.is_empty() {
            let _ = self.write_master(&responses);
        }
        if !wire.is_empty() {
            self.broadcast_output(seq, &wire);
        }
        if let Some(placements) = placements {
            self.broadcast_placements(seq, &placements);
        }
        if let Some(reason) = resync {
            self.force_resync(reason);
        }
        for observation in observations {
            self.publish_state(observation.claim, &observation.detail, "heartbeat");
        }
    }

    fn broadcast_output(&self, seq: u32, data: &[u8]) {
        let mut subscribers = self.subscribers.lock().expect("subscribers mutex poisoned");
        if subscribers.is_empty() {
            return;
        }
        let event = output_event(&self.id, seq, &BASE64.encode(data));
        let mut dropped = Vec::new();
        for (id, subscriber) in &*subscribers {
            match subscriber.sender.try_send(event.clone()) {
                Ok(()) => {}
                Err(TrySendError::Full(_) | TrySendError::Disconnected(_)) => {
                    dropped.push(id.clone());
                }
            }
        }
        for id in dropped {
            if let Some(subscriber) = subscribers.remove(&id) {
                let _ = subscriber.shutdown.shutdown(std::net::Shutdown::Both);
            }
        }
    }

    fn broadcast_placements(&self, seq: u32, placements: &[crate::ghostty::KittyPlacement]) {
        self.broadcast_subscriber_event(kitty_placements_event(&self.id, seq, placements));
    }

    fn force_resync(&self, reason: &str) {
        let event = desync_event(&self.id, reason);
        let mut subscribers = self.subscribers.lock().expect("subscribers mutex poisoned");
        for (_, subscriber) in subscribers.drain() {
            let _ = subscriber.sender.try_send(event.clone());
            let _ = subscriber.shutdown.shutdown(std::net::Shutdown::Both);
        }
    }

    #[allow(clippy::needless_pass_by_value)]
    fn broadcast_subscriber_event(&self, event: Value) {
        let mut subscribers = self.subscribers.lock().expect("subscribers mutex poisoned");
        let mut dropped = Vec::new();
        for (id, subscriber) in &*subscribers {
            if subscriber.sender.try_send(event.clone()).is_err() {
                dropped.push(id.clone());
            }
        }
        for id in dropped {
            if let Some(subscriber) = subscribers.remove(&id) {
                let _ = subscriber.shutdown.shutdown(std::net::Shutdown::Both);
            }
        }
    }

    pub fn kitty_image(&self, image_id: u32) -> Option<crate::ghostty::KittyImage> {
        self.model
            .lock()
            .expect("model mutex poisoned")
            .wire
            .kitty_image(image_id)
    }

    fn publish_state(&self, claim: &str, detail: &str, source: &str) {
        {
            let mut lifecycle = self.lifecycle.lock().expect("lifecycle mutex poisoned");
            lifecycle.state.clear();
            lifecycle.state.push_str(claim);
            lifecycle.state_detail.clear();
            lifecycle.state_detail.push_str(detail);
        }
        let event = state_event(&self.id, claim, detail, source);
        self.broadcast_watch(event.clone());
        (self.broadcast)(event);
    }

    fn mark_exited(self: &Arc<Self>, status: i32) {
        let (exit_code, signal) = decode_wait_status(status);
        {
            let mut lifecycle = self.lifecycle.lock().expect("lifecycle mutex poisoned");
            if !lifecycle.running {
                return;
            }
            lifecycle.running = false;
            lifecycle.exit_code = Some(exit_code);
            lifecycle.exit_signal.clone_from(&signal);
        }
        self.lifecycle_changed.notify_all();
        let event = exit_event(&self.id, exit_code, signal.as_deref());
        self.broadcast_watch(event.clone());
        (self.broadcast)(event);
        if self.connections.load(Ordering::Acquire) == 0 {
            self.schedule_cleanup();
        }
    }

    #[allow(clippy::needless_pass_by_value)]
    fn broadcast_watch(&self, event: Value) {
        let mut watchers = self.watchers.lock().expect("watchers mutex poisoned");
        watchers.retain(|_, sender| sender.try_send(event.clone()).is_ok());
    }

    fn schedule_cleanup(self: &Arc<Self>) {
        if self.cleanup_scheduled.swap(true, Ordering::AcqRel) {
            return;
        }
        let session = Arc::clone(self);
        let _ = thread::Builder::new()
            .name(format!("pty-cleanup-{}", self.id))
            .stack_size(64 * 1024)
            .spawn(move || {
                thread::sleep(EXITED_SESSION_TTL);
                if session.connections.load(Ordering::Acquire) == 0 && !session.is_running() {
                    session.finish_cleanup();
                } else {
                    session.cleanup_scheduled.store(false, Ordering::Release);
                }
            });
    }

    fn finish_cleanup(&self) {
        if self.cleaned.swap(true, Ordering::AcqRel) {
            return;
        }
        self.master.lock().expect("master mutex poisoned").take();
        let _ = fs::remove_file(&self.registry_path);
        if !self.cleanup_dir.is_empty() {
            let _ = fs::remove_dir_all(&self.cleanup_dir);
        }
        (self.cleanup)(self.id.clone());
    }

    pub(crate) fn is_shell(&self) -> bool {
        self.agent.eq_ignore_ascii_case("shell")
    }

    pub(crate) fn poll_shell_foreground(&self) {
        let foreground = {
            let master = self.master.lock().expect("master mutex poisoned");
            let Some(file) = master.as_ref() else {
                return;
            };
            let mut pgid: libc::pid_t = 0;
            if unsafe { libc::ioctl(file.as_raw_fd(), libc::TIOCGPGRP, &mut pgid) } != 0 {
                return;
            }
            pgid
        };
        let observation = self
            .model
            .lock()
            .expect("model mutex poisoned")
            .signals
            .observe_shell_poll(self.child_pid, foreground);
        if let Some(observation) = observation {
            self.publish_state(observation.claim, &observation.detail, "heartbeat");
        }
    }
}

fn start_reader(session: Arc<Session>, mut reader: File) -> Result<(), String> {
    thread::Builder::new()
        .name(format!("pty-read-{}", session.id))
        .stack_size(READER_STACK_BYTES)
        .spawn(move || {
            let mut buffer = vec![0_u8; 4 * 1024];
            let mut carry = Vec::with_capacity(64);
            loop {
                match reader.read(&mut buffer) {
                    Ok(0) => {
                        if !carry.is_empty() {
                            session.observe_output(&carry);
                        }
                        return;
                    }
                    Ok(read) => {
                        carry.extend_from_slice(&buffer[..read]);
                        let boundary = safe_boundary(&carry);
                        if boundary > 0 {
                            session.observe_output(&carry[..boundary]);
                            carry.drain(..boundary);
                        }
                    }
                    Err(error) if error.kind() == ErrorKind::Interrupted => {}
                    Err(error) if error.raw_os_error() == Some(libc::EIO) => return,
                    Err(_) => return,
                }
            }
        })
        .map(|_| ())
        .map_err(|error| format!("start PTY reader: {error}"))
}

fn reap_children(shared: &ChildReaperShared) {
    let signals = sigchld_set();
    loop {
        let mut signal = 0;
        let result = unsafe { libc::sigwait(&raw const signals, &raw mut signal) };
        if result != 0 {
            eprintln!(
                "PTY host child reaper signal wait failed: {}",
                std::io::Error::from_raw_os_error(result)
            );
            return;
        }
        reap_registered_children(shared);
    }
}

fn reap_registered_children(shared: &ChildReaperShared) {
    let pids = shared
        .sessions
        .lock()
        .expect("child reaper mutex poisoned")
        .keys()
        .copied()
        .collect::<Vec<_>>();
    for pid in pids {
        let mut status = 0;
        loop {
            let result = unsafe { libc::waitpid(pid, &raw mut status, libc::WNOHANG) };
            if result == pid {
                mark_reaped(shared, pid, status);
                break;
            }
            if result == 0 {
                break;
            }
            let error = std::io::Error::last_os_error();
            if error.kind() == ErrorKind::Interrupted {
                continue;
            }
            if error.raw_os_error() == Some(libc::ECHILD) {
                eprintln!("PTY host child {pid} was reaped outside the host reaper");
                mark_reaped(shared, pid, 1 << 8);
            } else {
                eprintln!("PTY host child {pid} reap failed: {error}");
            }
            break;
        }
    }
}

fn mark_reaped(shared: &ChildReaperShared, pid: i32, status: i32) {
    let session = shared
        .sessions
        .lock()
        .expect("child reaper mutex poisoned")
        .remove(&pid)
        .and_then(|session| session.upgrade());
    if let Some(session) = session {
        session.mark_exited(status);
    }
}

fn sigchld_set() -> libc::sigset_t {
    let mut signals = unsafe { std::mem::zeroed::<libc::sigset_t>() };
    unsafe {
        libc::sigemptyset(&raw mut signals);
        libc::sigaddset(&raw mut signals, libc::SIGCHLD);
    }
    signals
}

fn block_sigchld() -> Result<(), String> {
    // macOS ignores SIGCHLD by default, but sigwait only receives a blocked
    // signal with a caught disposition.
    let handler = note_sigchld as *const () as libc::sighandler_t;
    if unsafe { libc::signal(libc::SIGCHLD, handler) } == libc::SIG_ERR {
        return Err(format!(
            "install SIGCHLD disposition: {}",
            std::io::Error::last_os_error()
        ));
    }
    let signals = sigchld_set();
    let result =
        unsafe { libc::pthread_sigmask(libc::SIG_BLOCK, &raw const signals, std::ptr::null_mut()) };
    if result != 0 {
        return Err(format!(
            "block SIGCHLD: {}",
            std::io::Error::from_raw_os_error(result)
        ));
    }
    Ok(())
}

extern "C" fn note_sigchld(_: libc::c_int) {}

fn unblock_sigchld() -> std::io::Result<()> {
    let signals = sigchld_set();
    if unsafe { libc::sigprocmask(libc::SIG_UNBLOCK, &raw const signals, std::ptr::null_mut()) }
        != 0
    {
        return Err(std::io::Error::last_os_error());
    }
    Ok(())
}

fn abort_spawn(session: &Session) {
    let _ = unsafe { libc::kill(-session.child_pid, libc::SIGKILL) };
    let mut status = 0;
    loop {
        let result = unsafe { libc::waitpid(session.child_pid, &raw mut status, 0) };
        if result == session.child_pid
            || result < 0 && std::io::Error::last_os_error().kind() != ErrorKind::Interrupted
        {
            break;
        }
    }
    session.finish_cleanup();
    let _ = fs::remove_file(format!("{}.tmp", session.registry_path));
}

fn spawn_attempts(
    attempts: &[PreparedLaunchAttempt],
    cols: u16,
    rows: u16,
) -> Result<(File, i32, usize), String> {
    let mut failures = Vec::new();
    for (index, attempt) in attempts.iter().enumerate() {
        match spawn_attempt(attempt, cols, rows) {
            Ok((master, pid)) => return Ok((master, pid, index)),
            Err(error) => failures.push(format!("{}: {error}", attempt.executable)),
        }
    }
    Err(format!(
        "all prepared launch attempts failed: {}",
        failures.join("; ")
    ))
}

fn spawn_attempt(
    attempt: &PreparedLaunchAttempt,
    cols: u16,
    rows: u16,
) -> Result<(File, i32), String> {
    if attempt.executable.trim().is_empty() {
        return Err("empty executable".to_owned());
    }
    let mut winsize = libc::winsize {
        ws_row: rows,
        ws_col: cols,
        ws_xpixel: 0,
        ws_ypixel: 0,
    };
    let mut master_fd: RawFd = -1;
    let mut slave_fd: RawFd = -1;
    if unsafe {
        libc::openpty(
            &raw mut master_fd,
            &raw mut slave_fd,
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            &raw mut winsize,
        )
    } != 0
    {
        return Err(format!("openpty: {}", std::io::Error::last_os_error()));
    }

    let master = unsafe { File::from_raw_fd(master_fd) };
    let slave = unsafe { File::from_raw_fd(slave_fd) };
    spawn_with_files(attempt, master, slave)
}

fn spawn_with_files(
    attempt: &PreparedLaunchAttempt,
    master: File,
    slave: File,
) -> Result<(File, i32), String> {
    set_cloexec(master.as_raw_fd())?;
    set_cloexec(slave.as_raw_fd())?;
    let stdout_fd = unsafe { libc::dup(slave.as_raw_fd()) };
    let stderr_fd = unsafe { libc::dup(slave.as_raw_fd()) };
    if stdout_fd < 0 || stderr_fd < 0 {
        if stdout_fd >= 0 {
            unsafe { libc::close(stdout_fd) };
        }
        if stderr_fd >= 0 {
            unsafe { libc::close(stderr_fd) };
        }
        return Err(format!(
            "duplicate PTY slave: {}",
            std::io::Error::last_os_error()
        ));
    }
    let stdout = unsafe { File::from_raw_fd(stdout_fd) };
    let stderr = unsafe { File::from_raw_fd(stderr_fd) };
    set_cloexec(stdout.as_raw_fd())?;
    set_cloexec(stderr.as_raw_fd())?;

    let mut command = Command::new(&attempt.executable);
    if let Some(arg0) = attempt.args.first() {
        command.arg0(arg0);
    }
    command.args(attempt.args.iter().skip(1));
    command.current_dir(&attempt.cwd);
    command.env_clear();
    for entry in &attempt.env {
        if let Some((key, value)) = entry.split_once('=') {
            command.env(key, value);
        }
    }
    unsafe {
        command.stdin(Stdio::from(slave));
        command.stdout(Stdio::from(stdout));
        command.stderr(Stdio::from(stderr));
        command.pre_exec(|| {
            unblock_sigchld()?;
            if libc::setsid() < 0 {
                return Err(std::io::Error::last_os_error());
            }
            if libc::ioctl(libc::STDIN_FILENO, libc::c_ulong::from(libc::TIOCSCTTY), 0) < 0 {
                return Err(std::io::Error::last_os_error());
            }
            Ok(())
        });
    }
    let child = command
        .spawn()
        .map_err(|error| format!("spawn {}: {error}", attempt.executable))?;
    let pid = i32::try_from(child.id()).map_err(|_| "child pid does not fit i32".to_owned())?;
    drop(child);
    Ok((master, pid))
}

fn set_cloexec(fd: RawFd) -> Result<(), String> {
    let flags = unsafe { libc::fcntl(fd, libc::F_GETFD) };
    if flags < 0 || unsafe { libc::fcntl(fd, libc::F_SETFD, flags | libc::FD_CLOEXEC) } < 0 {
        return Err(format!(
            "set close-on-exec: {}",
            std::io::Error::last_os_error()
        ));
    }
    Ok(())
}

fn cleanup_unused_attempts(attempts: &[PreparedLaunchAttempt], keep: usize) {
    for (index, attempt) in attempts.iter().enumerate() {
        if index != keep && !attempt.cleanup_dir.is_empty() {
            let _ = fs::remove_dir_all(&attempt.cleanup_dir);
        }
    }
}

fn write_registry(path: &str, entry: &RegistryEntry) -> Result<(), String> {
    let parent = std::path::Path::new(path)
        .parent()
        .ok_or_else(|| format!("registry has no parent: {path}"))?;
    fs::create_dir_all(parent).map_err(|error| format!("create registry directory: {error}"))?;
    let payload =
        serde_json::to_vec_pretty(entry).map_err(|error| format!("encode registry: {error}"))?;
    let temporary = format!("{path}.tmp");
    let mut file = OpenOptions::new()
        .create(true)
        .truncate(true)
        .write(true)
        .mode(0o600)
        .open(&temporary)
        .map_err(|error| format!("write registry: {error}"))?;
    file.write_all(&payload)
        .map_err(|error| format!("write registry: {error}"))?;
    drop(file);
    fs::rename(&temporary, path).map_err(|error| format!("publish registry: {error}"))
}

fn add_exit_fields(result: &mut Value, lifecycle: &Lifecycle) {
    if let Some(exit_code) = lifecycle.exit_code {
        result["exit_code"] = json!(exit_code);
    }
    if let Some(signal) = lifecycle.exit_signal.as_ref() {
        result["exit_signal"] = Value::String(signal.clone());
    }
}

fn decode_wait_status(status: i32) -> (i32, Option<String>) {
    if libc::WIFEXITED(status) {
        return (libc::WEXITSTATUS(status), None);
    }
    if libc::WIFSIGNALED(status) {
        let signal = libc::WTERMSIG(status);
        return (128 + signal, Some(signal_name(signal).to_owned()));
    }
    (0, None)
}

pub fn parse_signal(value: &str) -> i32 {
    match value.trim().to_ascii_uppercase().as_str() {
        "SIGINT" | "INT" => libc::SIGINT,
        "SIGHUP" | "HUP" => libc::SIGHUP,
        "SIGKILL" | "KILL" => libc::SIGKILL,
        _ => libc::SIGTERM,
    }
}

fn signal_name(signal: i32) -> &'static str {
    match signal {
        libc::SIGINT => "SIGINT",
        libc::SIGHUP => "SIGHUP",
        libc::SIGKILL => "SIGKILL",
        libc::SIGTERM => "SIGTERM",
        _ => "SIGNAL",
    }
}

fn unix_timestamp() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs()
}
