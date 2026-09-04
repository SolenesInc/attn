use std::collections::{HashMap, HashSet};
use std::fs::{self, OpenOptions};
use std::io::{BufRead, BufReader, BufWriter, Write};
use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::mpsc::{Receiver, SyncSender, sync_channel};
use std::sync::{Arc, Mutex, Weak};
use std::thread;
use std::time::Duration;

use base64::Engine;
use base64::engine::general_purpose::STANDARD as BASE64;
use serde::Serialize;
use serde::de::DeserializeOwned;
use serde_json::{Value, json};

use crate::ghostty::Theme;
use crate::protocol::{
    AttachParams, ERR_BAD_REQUEST, ERR_IMAGE_NOT_FOUND, ERR_IO, ERR_SESSION_NOT_FOUND,
    ERR_SESSION_NOT_RUNNING, ERR_UNAUTHORIZED, ERR_UNSUPPORTED_VERSION, HelloParams, InputParams,
    KittyImageParams, RPC_MAJOR, RPC_MINOR, Request, ResizeParams, SignalParams, SpawnParams,
    error, is_compatible_version, response,
};
use crate::session::{Broadcast, ChildReaper, Cleanup, Session, SessionRuntime, parse_signal};

const CONNECTION_QUEUE_SIZE: usize = 256;
const CONNECTION_STACK_BYTES: usize = 256 * 1024;

#[derive(Clone)]
pub struct Config {
    pub daemon_instance_id: String,
    pub generation: String,
    pub socket_path: String,
    pub registry_dir: String,
    pub host_registry_path: String,
    pub control_token: String,
}

#[derive(Serialize)]
struct HostRegistry<'a> {
    version: u8,
    daemon_instance_id: &'a str,
    host_pid: u32,
    socket_path: &'a str,
    control_token: &'a str,
    executable: String,
    started_at: String,
    snapshot_format: &'static str,
    generation: &'a str,
}

pub struct Host {
    cfg: Config,
    // Lock watchers before state; release state before session callbacks or IO.
    state: Mutex<HostState>,
    conn_seq: AtomicU64,
    watchers: Mutex<HashMap<String, HostWatcher>>,
    reaper: ChildReaper,
}

#[derive(Default)]
struct HostState {
    sessions: HashMap<String, Arc<Session>>,
    spawning: HashSet<String>,
    idle_epoch: u64,
    shutting_down: bool,
}

impl HostState {
    fn begin_spawn(&mut self, id: &str) -> Result<(), String> {
        if id.trim().is_empty() || id.contains('/') || id.contains('\\') || id == "." || id == ".."
        {
            return Err("invalid session id".to_owned());
        }
        if self.shutting_down {
            return Err("host is shutting down".to_owned());
        }
        if self.sessions.contains_key(id) {
            return Err(format!("session {id} already exists"));
        }
        if !self.spawning.insert(id.to_owned()) {
            return Err(format!("session {id} spawn already in progress"));
        }
        self.idle_epoch = self.idle_epoch.wrapping_add(1);
        Ok(())
    }

    fn idle(&self) -> bool {
        !self.shutting_down && self.sessions.is_empty() && self.spawning.is_empty()
    }

    fn schedule_idle(&mut self) -> Option<u64> {
        if !self.idle() {
            return None;
        }
        self.idle_epoch = self.idle_epoch.wrapping_add(1);
        Some(self.idle_epoch)
    }

    fn begin_idle_shutdown(&mut self, epoch: u64) -> bool {
        if self.idle_epoch != epoch || !self.idle() {
            return false;
        }
        self.shutting_down = true;
        true
    }

    fn begin_shutdown(&mut self) -> Result<Vec<Arc<Session>>, String> {
        if !self.spawning.is_empty() || self.shutting_down {
            return Err("host has a spawn or shutdown in progress".to_owned());
        }
        self.shutting_down = true;
        Ok(self.sessions.values().cloned().collect())
    }
}

struct HostWatcher {
    sender: SyncSender<Value>,
    shutdown: UnixStream,
}

impl Host {
    pub fn run(cfg: Config) -> Result<(), String> {
        validate_config(&cfg)?;
        fs::create_dir_all(&cfg.registry_dir)
            .map_err(|error| format!("create host registry directory: {error}"))?;
        let socket_parent = Path::new(&cfg.socket_path)
            .parent()
            .ok_or_else(|| format!("socket has no parent: {}", cfg.socket_path))?;
        fs::create_dir_all(socket_parent)
            .map_err(|error| format!("create host socket directory: {error}"))?;
        if Path::new(&cfg.socket_path).exists() {
            return Err(format!("host socket already exists: {}", cfg.socket_path));
        }

        let listener = UnixListener::bind(&cfg.socket_path)
            .map_err(|error| format!("listen on {}: {error}", cfg.socket_path))?;
        fs::set_permissions(&cfg.socket_path, fs::Permissions::from_mode(0o600))
            .map_err(|error| format!("protect host socket: {error}"))?;
        let host = Arc::new(Self {
            cfg,
            state: Mutex::new(HostState::default()),
            conn_seq: AtomicU64::new(0),
            watchers: Mutex::new(HashMap::new()),
            reaper: ChildReaper::start()?,
        });
        host.write_registry()?;
        host.start_shell_poller()?;
        eprintln!(
            "PTY host ready: pid={} socket={} format={}",
            std::process::id(),
            host.cfg.socket_path,
            env!("ATTN_PTY_HOST_SNAPSHOT_FORMAT")
        );

        for incoming in listener.incoming() {
            let stream = match incoming {
                Ok(stream) => stream,
                Err(error) => {
                    eprintln!("PTY host accept failed: {error}");
                    continue;
                }
            };
            let host = Arc::clone(&host);
            let id = host.conn_seq.fetch_add(1, Ordering::Relaxed) + 1;
            if let Err(error) = thread::Builder::new()
                .name(format!("pty-rpc-{id}"))
                .stack_size(CONNECTION_STACK_BYTES)
                .spawn(move || handle_connection(host, stream, id))
            {
                eprintln!("PTY host could not start connection {id}: {error}");
            }
        }
        Ok(())
    }

    fn write_registry(&self) -> Result<(), String> {
        let executable = std::env::current_exe()
            .map_err(|error| format!("resolve host executable: {error}"))?
            .to_string_lossy()
            .into_owned();
        let registry = HostRegistry {
            version: 1,
            daemon_instance_id: &self.cfg.daemon_instance_id,
            host_pid: std::process::id(),
            socket_path: &self.cfg.socket_path,
            control_token: &self.cfg.control_token,
            executable,
            started_at: unix_timestamp().to_string(),
            snapshot_format: env!("ATTN_PTY_HOST_SNAPSHOT_FORMAT"),
            generation: &self.cfg.generation,
        };
        write_json_atomic(&self.cfg.host_registry_path, &registry)
    }

    fn session(&self, id: &str) -> Option<Arc<Session>> {
        self.state
            .lock()
            .expect("host state mutex poisoned")
            .sessions
            .get(id)
            .cloned()
    }

    fn spawn(self: &Arc<Self>, params: SpawnParams) -> Result<Arc<Session>, String> {
        let id = params.session_id.clone();
        self.state
            .lock()
            .expect("host state mutex poisoned")
            .begin_spawn(&id)?;

        let weak = Arc::downgrade(self);
        let cleanup_host = weak.clone();
        let cleanup: Cleanup =
            Arc::new(move |session_id| remove_session(&cleanup_host, &session_id));
        let broadcast: Broadcast = Arc::new(move |event| {
            if let Some(host) = weak.upgrade() {
                host.broadcast_lifecycle(&event);
            }
        });
        let registry_path = Path::new(&self.cfg.registry_dir)
            .join(format!("{id}.json"))
            .to_string_lossy()
            .into_owned();
        let result = Session::spawn(
            params,
            registry_path,
            &self.cfg.daemon_instance_id,
            &self.cfg.socket_path,
            &self.cfg.control_token,
            SessionRuntime::new(cleanup, broadcast, self.reaper.clone()),
        );
        let session = match result {
            Ok(session) => session,
            Err(error) => {
                self.state
                    .lock()
                    .expect("host state mutex poisoned")
                    .spawning
                    .remove(&id);
                self.schedule_idle_if_empty();
                return Err(error);
            }
        };
        {
            let watchers = self.watchers.lock().expect("host watchers mutex poisoned");
            {
                let mut state = self.state.lock().expect("host state mutex poisoned");
                state.sessions.insert(id.clone(), Arc::clone(&session));
                state.spawning.remove(&id);
            }
            for _ in &*watchers {
                session.note_connected();
            }
        }
        for event in session.lifecycle_events() {
            self.broadcast_lifecycle(&event);
        }
        Ok(session)
    }

    fn shutdown_sessions(&self) -> Result<(), String> {
        let sessions = self
            .state
            .lock()
            .expect("host state mutex poisoned")
            .begin_shutdown()?;
        let mut tasks = Vec::new();
        let mut failure = None;
        for session in sessions {
            let child = Arc::clone(&session);
            match thread::Builder::new()
                .name("pty-shutdown".to_owned())
                .stack_size(128 * 1024)
                .spawn(move || child.remove_checked())
            {
                Ok(task) => tasks.push(task),
                Err(_) => {
                    if let Err(error) = session.remove_checked() {
                        failure = Some(error);
                    }
                }
            }
        }
        for task in tasks {
            if let Err(error) = task
                .join()
                .unwrap_or_else(|_| Err("terminal shutdown panicked".to_owned()))
            {
                failure = Some(error);
            }
        }
        if let Some(error) = failure {
            self.state
                .lock()
                .expect("host state mutex poisoned")
                .shutting_down = false;
            return Err(error);
        }
        Ok(())
    }

    fn schedule_idle_if_empty(self: &Arc<Self>) {
        let Some(epoch) = self
            .state
            .lock()
            .expect("host state mutex poisoned")
            .schedule_idle()
        else {
            return;
        };
        let host = Arc::downgrade(self);
        let _ = thread::Builder::new()
            .name("pty-host-idle".to_owned())
            .stack_size(64 * 1024)
            .spawn(move || {
                thread::sleep(std::time::Duration::from_secs(45));
                let Some(host) = host.upgrade() else {
                    return;
                };
                if !host
                    .state
                    .lock()
                    .expect("host state mutex poisoned")
                    .begin_idle_shutdown(epoch)
                {
                    return;
                }
                let _ = fs::remove_file(&host.cfg.host_registry_path);
                let _ = fs::remove_file(&host.cfg.socket_path);
                std::process::exit(0);
            });
    }

    fn host_info(&self) -> Value {
        let mut ids: Vec<String> = self
            .state
            .lock()
            .expect("host state mutex poisoned")
            .sessions
            .keys()
            .cloned()
            .collect();
        ids.sort();
        json!({
            "host_pid": std::process::id(),
            "session_ids": ids,
            "snapshot_format": env!("ATTN_PTY_HOST_SNAPSHOT_FORMAT")
        })
    }

    fn watch_all(&self, watcher_id: String, sender: SyncSender<Value>, shutdown: UnixStream) {
        let mut watchers = self.watchers.lock().expect("host watchers mutex poisoned");
        let sessions = self
            .state
            .lock()
            .expect("host state mutex poisoned")
            .sessions
            .values()
            .cloned()
            .collect::<Vec<_>>();
        for event in sessions
            .iter()
            .flat_map(|session| session.lifecycle_events())
        {
            if sender.send(event).is_err() {
                let _ = shutdown.shutdown(std::net::Shutdown::Both);
                return;
            }
        }
        for session in &sessions {
            session.note_connected();
        }
        watchers.insert(watcher_id, HostWatcher { sender, shutdown });
    }

    fn unwatch_all(&self, watcher_id: &str) {
        let sessions = {
            let mut watchers = self.watchers.lock().expect("host watchers mutex poisoned");
            if watchers.remove(watcher_id).is_none() {
                return;
            }
            self.state
                .lock()
                .expect("host state mutex poisoned")
                .sessions
                .values()
                .cloned()
                .collect::<Vec<_>>()
        };
        for session in sessions {
            session.note_disconnected();
        }
    }

    fn broadcast_lifecycle(&self, event: &Value) {
        let (dropped, sessions) = {
            let mut watchers = self.watchers.lock().expect("host watchers mutex poisoned");
            let mut dropped = Vec::new();
            for (id, watcher) in &*watchers {
                if watcher.sender.try_send(event.clone()).is_err() {
                    dropped.push(id.clone());
                }
            }
            for id in &dropped {
                if let Some(watcher) = watchers.remove(id) {
                    let _ = watcher.shutdown.shutdown(std::net::Shutdown::Both);
                }
            }
            let sessions = if dropped.is_empty() {
                Vec::new()
            } else {
                self.state
                    .lock()
                    .expect("host state mutex poisoned")
                    .sessions
                    .values()
                    .cloned()
                    .collect::<Vec<_>>()
            };
            (dropped, sessions)
        };
        for session in sessions {
            for _ in &dropped {
                session.note_disconnected();
            }
        }
    }

    fn start_shell_poller(self: &Arc<Self>) -> Result<(), String> {
        let host = Arc::downgrade(self);
        thread::Builder::new()
            .name("pty-shell-state".to_owned())
            .stack_size(64 * 1024)
            .spawn(move || {
                loop {
                    thread::sleep(Duration::from_secs(1));
                    let Some(host) = host.upgrade() else {
                        return;
                    };
                    let sessions = host
                        .state
                        .lock()
                        .expect("host state mutex poisoned")
                        .sessions
                        .values()
                        .filter(|session| session.is_shell() && session.is_running())
                        .cloned()
                        .collect::<Vec<_>>();
                    for session in sessions {
                        session.poll_shell_foreground();
                    }
                }
            })
            .map(|_| ())
            .map_err(|error| format!("start shell state poller: {error}"))
    }
}

enum CloseAction {
    Detach,
    ShutdownHost,
}

struct Connection {
    id: String,
    sender: SyncSender<Value>,
    shutdown: UnixStream,
    selected: Option<Arc<Session>>,
    subscriber_id: String,
    watching: bool,
    watching_all: bool,
    authed: bool,
    snapshot_format: String,
    close_action: CloseAction,
}

impl Connection {
    fn send(&self, value: Value) -> bool {
        self.sender.send(value).is_ok()
    }

    fn fail(&self, id: &str, code: &str, message: impl Into<String>) -> bool {
        self.send(error(id, code, message))
    }

    fn session(&self, host: &Host, request: &Request) -> Result<Arc<Session>, Value> {
        self.selected
            .clone()
            .or_else(|| host.session(&request.session_id))
            .ok_or_else(|| {
                error(
                    &request.id,
                    ERR_SESSION_NOT_FOUND,
                    "session-scoped hello or request session id required",
                )
            })
    }
}

#[allow(clippy::needless_pass_by_value)]
fn handle_connection(host: Arc<Host>, stream: UnixStream, id: u64) {
    let shutdown_stream = match stream.try_clone() {
        Ok(stream) => stream,
        Err(error) => {
            eprintln!("PTY host connection {id} shutdown clone failed: {error}");
            return;
        }
    };
    let writer_stream = match stream.try_clone() {
        Ok(stream) => stream,
        Err(error) => {
            eprintln!("PTY host connection {id} clone failed: {error}");
            return;
        }
    };
    let (sender, receiver) = sync_channel(CONNECTION_QUEUE_SIZE);
    let writer = thread::Builder::new()
        .name(format!("pty-rpc-write-{id}"))
        .stack_size(128 * 1024)
        .spawn(move || write_connection(writer_stream, receiver));
    let Ok(writer) = writer else {
        eprintln!("PTY host connection {id} writer thread failed");
        return;
    };
    let mut connection = Connection {
        id: id.to_string(),
        sender,
        shutdown: shutdown_stream,
        selected: None,
        subscriber_id: String::new(),
        watching: false,
        watching_all: false,
        authed: false,
        snapshot_format: String::new(),
        close_action: CloseAction::Detach,
    };
    let mut reader = BufReader::new(stream);
    let mut line = String::new();
    loop {
        line.clear();
        match reader.read_line(&mut line) {
            Ok(0) => break,
            Ok(_) => {}
            Err(error) => {
                eprintln!("PTY host connection {id} read failed: {error}");
                break;
            }
        }
        let Ok(request) = serde_json::from_str::<Request>(&line) else {
            if !connection.fail("", ERR_BAD_REQUEST, "invalid request JSON") {
                break;
            }
            continue;
        };
        if request.kind != "req" {
            if !connection.fail(&request.id, ERR_BAD_REQUEST, "request type must be req") {
                break;
            }
            continue;
        }
        if !handle_request(&host, &mut connection, request) {
            break;
        }
    }

    if let Some(session) = connection.selected.as_ref() {
        if !connection.subscriber_id.is_empty() {
            session.detach(&connection.subscriber_id, &connection.id);
        }
        if connection.watching {
            session.unwatch(&connection.id);
        }
        session.note_disconnected();
    }
    if connection.watching_all {
        host.unwatch_all(&connection.id);
    }
    drop(connection.sender);
    let _ = writer.join();
    if matches!(connection.close_action, CloseAction::ShutdownHost) {
        let _ = fs::remove_file(&host.cfg.host_registry_path);
        let _ = fs::remove_file(&host.cfg.socket_path);
        std::process::exit(0);
    }
}

#[allow(clippy::needless_pass_by_value, clippy::too_many_lines)]
fn handle_request(host: &Arc<Host>, connection: &mut Connection, request: Request) -> bool {
    if request.method == "hello" {
        return handle_hello(host, connection, &request);
    }
    if !connection.authed {
        connection.fail(
            &request.id,
            ERR_UNAUTHORIZED,
            "hello required before method calls",
        );
        return false;
    }

    match request.method.as_str() {
        "shutdown" => {
            if connection.selected.is_some() {
                return connection.fail(
                    &request.id,
                    ERR_BAD_REQUEST,
                    "shutdown requires a host-level hello",
                );
            }
            if let Err(error) = host.shutdown_sessions() {
                return connection.fail(&request.id, ERR_IO, error);
            }
            connection.close_action = CloseAction::ShutdownHost;
            connection.send(response(&request.id, json!({"ok": true})));
            false
        }
        "spawn" => {
            if connection.selected.is_some() {
                return connection.fail(
                    &request.id,
                    ERR_BAD_REQUEST,
                    "spawn requires a host-level hello",
                );
            }
            let params: SpawnParams = match decode_params(&request) {
                Ok(params) => params,
                Err(message) => return connection.fail(&request.id, ERR_BAD_REQUEST, message),
            };
            match host.spawn(params) {
                Ok(session) => connection.send(response(
                    &request.id,
                    json!({
                        "host_pid": std::process::id(),
                        "child_pid": session.child_pid,
                        "attempt_index": session.attempt_index
                    }),
                )),
                Err(message) => connection.fail(&request.id, ERR_IO, message),
            }
        }
        "host_info" => {
            if connection.selected.is_some() {
                return connection.fail(
                    &request.id,
                    ERR_BAD_REQUEST,
                    "host_info requires a host-level hello",
                );
            }
            connection.send(response(&request.id, host.host_info()))
        }
        "info" => with_session(host, connection, &request, Session::info),
        "snapshot" => with_session(host, connection, &request, Session::screen_snapshot),
        "attach" => {
            if connection.selected.is_none() {
                return connection.fail(
                    &request.id,
                    ERR_BAD_REQUEST,
                    "attach requires a session-scoped hello",
                );
            }
            let params: AttachParams = match decode_params(&request) {
                Ok(params) => params,
                Err(message) => return connection.fail(&request.id, ERR_BAD_REQUEST, message),
            };
            let Ok(session) = connection.session(host, &request) else {
                return connection.fail(&request.id, ERR_SESSION_NOT_FOUND, "session not found");
            };
            if !connection.subscriber_id.is_empty() {
                session.detach(&connection.subscriber_id, &connection.id);
            }
            let subscriber_id = if params.subscriber_id.trim().is_empty() {
                format!("conn-{}", connection.id)
            } else {
                params.subscriber_id
            };
            let result = session.attach(
                subscriber_id.clone(),
                connection.id.clone(),
                connection.sender.clone(),
                connection
                    .shutdown
                    .try_clone()
                    .expect("connection clone already succeeded"),
                params.omit_replay,
                &connection.snapshot_format,
            );
            connection.subscriber_id = subscriber_id;
            connection.send(response(&request.id, result))
        }
        "detach" => {
            if connection.selected.is_none() {
                return connection.fail(
                    &request.id,
                    ERR_BAD_REQUEST,
                    "detach requires a session-scoped hello",
                );
            }
            if let Some(session) = connection.selected.as_ref() {
                session.detach(&connection.subscriber_id, &connection.id);
            }
            connection.subscriber_id.clear();
            connection.send(response(&request.id, json!({"ok": true})))
        }
        "input" => {
            let params: InputParams = match decode_params(&request) {
                Ok(params) => params,
                Err(message) => return connection.fail(&request.id, ERR_BAD_REQUEST, message),
            };
            let Ok(data) = BASE64.decode(params.data) else {
                return connection.fail(
                    &request.id,
                    ERR_BAD_REQUEST,
                    "invalid base64 input payload",
                );
            };
            let Ok(session) = connection.session(host, &request) else {
                return connection.fail(&request.id, ERR_SESSION_NOT_FOUND, "session not found");
            };
            match session.input(&data) {
                Ok(()) => connection.send(response(&request.id, json!({"ok": true}))),
                Err(message) if message.contains("not running") => {
                    connection.fail(&request.id, ERR_SESSION_NOT_RUNNING, message)
                }
                Err(message) => connection.fail(&request.id, ERR_IO, message),
            }
        }
        "resize" => {
            let params: ResizeParams = match decode_params(&request) {
                Ok(params) => params,
                Err(message) => return connection.fail(&request.id, ERR_BAD_REQUEST, message),
            };
            let Ok(session) = connection.session(host, &request) else {
                return connection.fail(&request.id, ERR_SESSION_NOT_FOUND, "session not found");
            };
            match session.resize(params.cols, params.rows, params.xpixel, params.ypixel) {
                Ok(changed) => connection.send(response(
                    &request.id,
                    json!({"ok": true, "changed": changed}),
                )),
                Err(message) => connection.fail(&request.id, ERR_IO, message),
            }
        }
        "set_theme" => {
            let theme: Theme = match decode_params(&request) {
                Ok(theme) => theme,
                Err(message) => return connection.fail(&request.id, ERR_BAD_REQUEST, message),
            };
            let Ok(session) = connection.session(host, &request) else {
                return connection.fail(&request.id, ERR_SESSION_NOT_FOUND, "session not found");
            };
            match session.set_theme(&theme) {
                Ok(()) => connection.send(response(&request.id, json!({"ok": true}))),
                Err(message) => connection.fail(&request.id, ERR_IO, message),
            }
        }
        "signal" => {
            let params: SignalParams = match decode_params(&request) {
                Ok(params) => params,
                Err(message) => return connection.fail(&request.id, ERR_BAD_REQUEST, message),
            };
            let Ok(session) = connection.session(host, &request) else {
                return connection.fail(&request.id, ERR_SESSION_NOT_FOUND, "session not found");
            };
            match session.signal(parse_signal(&params.signal)) {
                Ok(()) => connection.send(response(&request.id, json!({"ok": true}))),
                Err(message) => connection.fail(&request.id, ERR_IO, message),
            }
        }
        "remove" => {
            let Ok(session) = connection.session(host, &request) else {
                return connection.fail(&request.id, ERR_SESSION_NOT_FOUND, "session not found");
            };
            let sent = connection.send(response(&request.id, json!({"ok": true})));
            session.remove();
            sent
        }
        "watch" => {
            if connection.selected.is_none() {
                return connection.fail(
                    &request.id,
                    ERR_BAD_REQUEST,
                    "watch requires a session-scoped hello",
                );
            }
            let Ok(session) = connection.session(host, &request) else {
                return connection.fail(&request.id, ERR_SESSION_NOT_FOUND, "session not found");
            };
            if !connection.send(response(&request.id, json!({"ok": true}))) {
                return false;
            }
            session.watch(connection.id.clone(), connection.sender.clone());
            connection.watching = true;
            true
        }
        "watch_all" => {
            if connection.selected.is_some() || !request.session_id.is_empty() {
                return connection.fail(
                    &request.id,
                    ERR_BAD_REQUEST,
                    "watch_all requires a host-level connection",
                );
            }
            if !connection.send(response(&request.id, json!({"ok": true}))) {
                return false;
            }
            host.watch_all(
                connection.id.clone(),
                connection.sender.clone(),
                connection
                    .shutdown
                    .try_clone()
                    .expect("connection clone already succeeded"),
            );
            connection.watching_all = true;
            true
        }
        "health" => {
            let Ok(session) = connection.session(host, &request) else {
                return connection.fail(&request.id, ERR_SESSION_NOT_FOUND, "session not found");
            };
            connection.send(response(
                &request.id,
                json!({"ok": true, "running": session.is_running()}),
            ))
        }
        "kitty_image" => {
            let params: KittyImageParams = match decode_params(&request) {
                Ok(params) => params,
                Err(message) => return connection.fail(&request.id, ERR_BAD_REQUEST, message),
            };
            let Ok(session) = connection.session(host, &request) else {
                return connection.fail(&request.id, ERR_SESSION_NOT_FOUND, "session not found");
            };
            let Some(image) = session.kitty_image(params.image_id) else {
                return connection.fail(&request.id, ERR_IMAGE_NOT_FOUND, "kitty image not found");
            };
            connection.send(response(
                &request.id,
                json!({
                    "image_id": image.image_id,
                    "width": image.width,
                    "height": image.height,
                    "format": image.format,
                    "generation": image.generation,
                    "data": BASE64.encode(image.data)
                }),
            ))
        }
        "upgrade" => connection.fail(&request.id, ERR_BAD_REQUEST, "host upgrade is not required"),
        _ => connection.fail(&request.id, ERR_BAD_REQUEST, "unknown method"),
    }
}

fn handle_hello(host: &Host, connection: &mut Connection, request: &Request) -> bool {
    let params: HelloParams = match decode_params(request) {
        Ok(params) => params,
        Err(message) => return connection.fail(&request.id, ERR_BAD_REQUEST, message),
    };
    if !is_compatible_version(params.rpc_major, params.rpc_minor) {
        connection.fail(
            &request.id,
            ERR_UNSUPPORTED_VERSION,
            format!(
                "rpc version incompatible: got={}.{} supported-major={RPC_MAJOR}",
                params.rpc_major, params.rpc_minor
            ),
        );
        return false;
    }
    if params.daemon_instance_id != host.cfg.daemon_instance_id
        || params.control_token != host.cfg.control_token
    {
        connection.fail(
            &request.id,
            ERR_UNAUTHORIZED,
            "daemon identity or control token mismatch",
        );
        return false;
    }
    if connection.authed {
        return connection.fail(&request.id, ERR_BAD_REQUEST, "hello already completed");
    }

    if !params.session_id.is_empty() {
        let Some(session) = host.session(&params.session_id) else {
            connection.fail(&request.id, ERR_SESSION_NOT_FOUND, "session not found");
            return false;
        };
        session.note_connected();
        connection.selected = Some(session);
    }
    connection.authed = true;
    connection.snapshot_format = params.snapshot_format;
    connection.send(response(
        &request.id,
        json!({
            "worker_version": "attn-rust",
            "rpc_major": RPC_MAJOR,
            "rpc_minor": RPC_MINOR,
            "daemon_instance_id": host.cfg.daemon_instance_id,
            "session_id": params.session_id,
            "snapshot_format": env!("ATTN_PTY_HOST_SNAPSHOT_FORMAT")
        }),
    ))
}

fn with_session(
    host: &Host,
    connection: &Connection,
    request: &Request,
    call: impl FnOnce(&Session) -> Value,
) -> bool {
    let Ok(session) = connection.session(host, request) else {
        return connection.fail(&request.id, ERR_SESSION_NOT_FOUND, "session not found");
    };
    connection.send(response(&request.id, call(&session)))
}

fn decode_params<T: DeserializeOwned>(request: &Request) -> Result<T, String> {
    serde_json::from_value(request.params.clone())
        .map_err(|error| format!("invalid {} params: {error}", request.method))
}

fn write_connection(stream: UnixStream, receiver: Receiver<Value>) {
    let mut writer = BufWriter::new(stream);
    for value in receiver {
        if serde_json::to_writer(&mut writer, &value).is_err()
            || writer.write_all(b"\n").is_err()
            || writer.flush().is_err()
        {
            return;
        }
    }
}

fn remove_session(host: &Weak<Host>, session_id: &str) {
    let Some(host) = host.upgrade() else {
        return;
    };
    host.state
        .lock()
        .expect("host state mutex poisoned")
        .sessions
        .remove(session_id);
    host.schedule_idle_if_empty();
}

fn validate_config(cfg: &Config) -> Result<(), String> {
    for (name, value) in [
        ("daemon instance id", &cfg.daemon_instance_id),
        ("generation", &cfg.generation),
        ("socket path", &cfg.socket_path),
        ("registry directory", &cfg.registry_dir),
        ("host registry path", &cfg.host_registry_path),
        ("control token", &cfg.control_token),
    ] {
        if value.trim().is_empty() {
            return Err(format!("missing {name}"));
        }
    }
    Ok(())
}

fn write_json_atomic(path: &str, value: &impl Serialize) -> Result<(), String> {
    let path = PathBuf::from(path);
    let parent = path
        .parent()
        .ok_or_else(|| format!("registry has no parent: {}", path.display()))?;
    fs::create_dir_all(parent).map_err(|error| format!("create registry directory: {error}"))?;
    let temporary = path.with_extension("tmp");
    let mut file = OpenOptions::new()
        .create(true)
        .truncate(true)
        .write(true)
        .mode(0o600)
        .open(&temporary)
        .map_err(|error| format!("write host registry: {error}"))?;
    serde_json::to_writer_pretty(&mut file, value)
        .map_err(|error| format!("encode host registry: {error}"))?;
    file.write_all(b"\n")
        .map_err(|error| format!("finish host registry: {error}"))?;
    drop(file);
    fs::rename(&temporary, &path).map_err(|error| format!("publish host registry: {error}"))
}

fn unix_timestamp() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs()
}

#[cfg(test)]
mod tests {
    use super::HostState;

    #[test]
    fn idle_retirement_closes_spawn_admission() {
        let mut state = HostState::default();
        let epoch = state.schedule_idle().unwrap();
        assert!(state.begin_idle_shutdown(epoch));
        assert!(
            state.begin_spawn("late-terminal").is_err(),
            "retiring host admitted a new terminal"
        );
    }

    #[test]
    fn pending_spawn_prevents_retirement_and_shutdown() {
        let mut state = HostState::default();
        let epoch = state.schedule_idle().unwrap();
        state.begin_spawn("new-terminal").unwrap();
        assert!(!state.begin_idle_shutdown(epoch));
        assert!(!state.begin_idle_shutdown(state.idle_epoch));
        assert!(state.schedule_idle().is_none());
        assert!(state.begin_shutdown().is_err());
        assert!(!state.shutting_down);
    }

    #[test]
    fn cancelled_spawn_can_retire_on_a_fresh_timer_only() {
        let mut state = HostState::default();
        let old_epoch = state.schedule_idle().unwrap();
        state.begin_spawn("failed-terminal").unwrap();
        state.spawning.remove("failed-terminal");
        let new_epoch = state.schedule_idle().unwrap();
        assert!(!state.begin_idle_shutdown(old_epoch));
        assert!(state.begin_idle_shutdown(new_epoch));
    }

    #[test]
    fn shutdown_closes_spawn_admission_and_rejects_idle_retirement() {
        let mut state = HostState::default();
        let epoch = state.schedule_idle().unwrap();
        assert!(state.begin_shutdown().unwrap().is_empty());
        assert!(state.begin_spawn("late-terminal").is_err());
        assert!(state.begin_shutdown().is_err());
        assert!(state.schedule_idle().is_none());
        assert!(!state.begin_idle_shutdown(epoch));
    }

    #[test]
    fn duplicate_spawn_keeps_the_original_reservation() {
        let mut state = HostState::default();
        state.begin_spawn("terminal").unwrap();
        assert!(state.begin_spawn("terminal").is_err());
        assert!(state.spawning.contains("terminal"));
        assert!(state.schedule_idle().is_none());
    }

    #[test]
    fn invalid_spawn_does_not_cancel_idle_retirement() {
        for id in ["", " ", ".", "..", "a/b", "a\\b"] {
            let mut state = HostState::default();
            let epoch = state.schedule_idle().unwrap();
            assert!(state.begin_spawn(id).is_err());
            assert!(state.begin_idle_shutdown(epoch));
        }
    }
}
