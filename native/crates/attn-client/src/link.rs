use std::sync::{Arc, Mutex};
use std::thread::JoinHandle;
use std::time::Duration;

use attn_protocol::{
    AsksUpdatedMessage, AttachResultMessage, ClientHelloMessage, CommandErrorMessage,
    GardenSeedsChangedMessage, GardenSeedsUpdatedMessage, GitHubHostsUpdatedMessage,
    GitOperationStartedMessage, InitialStateMessage, KittyImageResultMessage,
    KittyPlacementsMessage, NotificationListResultMessage, NotificationsUpdatedMessage,
    OutpostsUpdatedMessage, PROTOCOL_VERSION, ProfileArrangementChangedMessage,
    ProfilesChangedMessage, PtyDesyncMessage, PtyResizedMessage, RuntimeRespawnedMessage,
    SessionClosedMessage, SessionExitedMessage, SessionMessagesChangedMessage,
    SessionRegisteredMessage, SessionUnregisteredMessage, SessionsUpdatedMessage,
    SettingsUpdatedMessage, UpdateChangedMessage, VentsUpdatedMessage, WorktreeStateChangedEvent,
    WorktreeSweptEvent, go_values,
};
use futures_util::{SinkExt, StreamExt};
use serde::{Deserialize, Serialize};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;
use tokio::sync::{mpsc, watch};
use tokio_tungstenite::tungstenite::Message;

use crate::frame::Frame;
use crate::{CLIENT_KIND, Clock, Connection, Pulse, Refusal, Store};

const FIRST_RETRY: Duration = Duration::from_secs(1);
const LAST_RETRY: Duration = Duration::from_secs(5);
const CAPABILITIES: [&str; 2] = ["binary_pty_output", "kitty_images"];

/// Where the link hands what it hears about terminals.
pub type TerminalSink = Box<dyn Fn(TerminalEvent) + Send + Sync>;
pub type ReplySink = Box<dyn Fn(Reply) + Send + Sync>;
/// Starts the daemon when it is not running, and returns once it answers, or says why it could
/// not start.
pub type StartDaemon = Arc<dyn Fn() -> Result<(), String> + Send + Sync>;

pub struct LinkConfig {
    /// The daemon's `host:port`; the link speaks WebSocket on `/ws` and reads `/health`.
    pub daemon: String,
    pub client_token: String,
    /// The app's own credential, `<data-dir>/browser-host-token`: with it, and no web origin,
    /// the daemon trusts this client as the attn app.
    pub host_token: Option<String>,
    /// The instance this app was built for, `default` for production.
    pub instance: String,
    /// The profile to ask for until the daemon has said which one this client is scoped to.
    pub profile_id: Option<String>,
    pub client_id: String,
    pub app_pid: u32,
    pub clock: Clock,
    pub pulse: Pulse,
    /// What the daemon says about terminals, on the link's thread, in arrival order.
    pub terminals: TerminalSink,
    /// Every message that answers a request by its `request_id`, and every `command_error`, on
    /// the link's thread.
    pub replies: ReplySink,
    /// Run before every connection, the first included, so a stopped daemon comes back. Its
    /// failure is shown and retried, and no connection is tried. `None` leaves starting the
    /// daemon to whoever runs it.
    pub start_daemon: Option<StartDaemon>,
}

/// A daemon message that names the request it answers.
#[derive(Debug, Clone)]
pub struct Reply {
    /// Empty for a `command_error`, which names only its command.
    pub request_id: String,
    pub event: String,
    pub body: serde_json::Value,
}

impl Reply {
    /// The reply as the protocol type its event names.
    pub fn parse<T: serde::de::DeserializeOwned>(&self) -> Option<T> {
        serde_json::from_value(self.body.clone())
            .inspect_err(|why| eprintln!("attn-app: dropped an unreadable {}: {why}", self.event))
            .ok()
    }
}

/// A terminal's output and the daemon's answers about it, plus the connection's start and
/// end: a terminal attachment lasts only as long as the connection that made it.
#[derive(Debug, Clone)]
pub enum TerminalEvent {
    Frame(Frame),
    Attached(Box<AttachResultMessage>),
    Resized(PtyResizedMessage),
    Placements(KittyPlacementsMessage),
    ImageResult(KittyImageResultMessage),
    Desync(PtyDesyncMessage),
    Respawned(RuntimeRespawnedMessage),
    Exited(SessionExitedMessage),
    /// The agent's messages changed, so marks on them are found again.
    MessagesChanged(SessionMessagesChangedMessage),
}

/// Where the link's thread waits, for a caller that needs to know the link has nothing left
/// to do until the daemon or the clock moves.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Parked {
    Busy,
    Reading,
    Until(Duration),
    Stopped,
}

/// What the link has exchanged with the daemon on its current connection. `connection`
/// counts the WebSocket connections the link has opened, and the counts start over with each.
/// `sent` counts commands as they are handed over, the hello included, before they reach the
/// socket; `received` counts messages once applied, and the daemon's hang-up once seen.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Traffic {
    pub connection: u64,
    pub sent: u64,
    pub received: u64,
    pub parked: Parked,
}

#[derive(Debug)]
pub struct NotConnected;

impl std::fmt::Display for NotConnected {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("the daemon is not connected")
    }
}

struct State {
    traffic: Traffic,
    outbox: Option<mpsc::UnboundedSender<String>>,
}

struct Shared {
    state: Mutex<State>,
    pulse: Pulse,
}

impl Shared {
    fn count(&self, f: impl FnOnce(&mut Traffic)) {
        f(&mut self.state.lock().unwrap().traffic);
        self.pulse.beat();
    }

    fn send(&self, text: String) -> Result<(), NotConnected> {
        let mut state = self.state.lock().unwrap();
        let outbox = state.outbox.as_ref().ok_or(NotConnected)?;
        outbox.send(text).map_err(|_| NotConnected)?;
        state.traffic.sent += 1;
        drop(state);
        self.pulse.beat();
        Ok(())
    }

    /// Opens a connection whose first command is `hello`: the outbox is handed to senders
    /// only once the hello is in it, so nothing reaches the daemon ahead of it.
    fn open(&self, outbox: mpsc::UnboundedSender<String>, hello: String) {
        outbox
            .send(hello)
            .expect("the session holds the outbox's receiver");
        let mut state = self.state.lock().unwrap();
        let connection = state.traffic.connection + 1;
        state.traffic = Traffic {
            connection,
            sent: 1,
            received: 0,
            parked: Parked::Busy,
        };
        state.outbox = Some(outbox);
        drop(state);
        self.pulse.beat();
    }

    fn close(&self) {
        self.state.lock().unwrap().outbox = None;
    }
}

/// Sends commands to the daemon on the link it came from. The default one belongs to no link
/// and is never connected.
#[derive(Clone, Default)]
pub struct Commands(Option<Arc<Shared>>);

impl Commands {
    pub fn send(&self, command: &impl Serialize) -> Result<(), NotConnected> {
        let shared = self.0.as_ref().ok_or(NotConnected)?;
        shared.send(serde_json::to_string(command).expect("a protocol command serializes"))
    }
}

/// The connection to the daemon, on a thread of its own. Dropping it closes the connection
/// and joins the thread.
pub struct Link {
    shared: Arc<Shared>,
    stop: watch::Sender<bool>,
    thread: Option<JoinHandle<()>>,
}

impl Link {
    pub fn start(config: LinkConfig, store: Store) -> Link {
        let shared = Arc::new(Shared {
            state: Mutex::new(State {
                traffic: Traffic {
                    connection: 0,
                    sent: 0,
                    received: 0,
                    parked: Parked::Busy,
                },
                outbox: None,
            }),
            pulse: config.pulse.clone(),
        });
        let (stop, stopped) = watch::channel(false);
        let task = Task {
            config,
            store,
            shared: shared.clone(),
            stopped,
        };
        let thread = std::thread::Builder::new()
            .name("attn-daemon-link".into())
            .spawn(move || {
                let runtime = tokio::runtime::Builder::new_current_thread()
                    .enable_all()
                    .build()
                    .expect("the link's runtime");
                runtime.block_on(task.run());
            })
            .expect("the link's thread");
        Link {
            shared,
            stop,
            thread: Some(thread),
        }
    }

    /// Hands a command to the open connection. A command handed over as the connection drops
    /// is lost with it, as on any socket; the daemon's next `initial_state` says what holds.
    pub fn send(&self, command: &impl Serialize) -> Result<(), NotConnected> {
        self.commands().send(command)
    }

    /// A handle that sends commands on this link, for a window that outlives no link.
    pub fn commands(&self) -> Commands {
        Commands(Some(self.shared.clone()))
    }

    pub fn traffic(&self) -> Traffic {
        self.shared.state.lock().unwrap().traffic
    }
}

impl Drop for Link {
    fn drop(&mut self) {
        let _ = self.stop.send(true);
        if let Some(thread) = self.thread.take() {
            let _ = thread.join();
        }
    }
}

struct Task {
    config: LinkConfig,
    store: Store,
    shared: Arc<Shared>,
    stopped: watch::Receiver<bool>,
}

enum Ended {
    Lost { greeted: bool },
    Refused(Refusal),
    Stopped,
}

#[derive(Deserialize)]
struct Header {
    #[serde(default)]
    event: String,
    #[serde(default)]
    request_id: Option<String>,
    #[serde(default)]
    id: Option<String>,
    #[serde(default)]
    probe_id: Option<String>,
    #[serde(default)]
    protocol_version: Option<String>,
}

impl Header {
    /// The request this message answers: its `request_id`, for `spawn_result`,
    /// `rename_result` and `reload_session_result` the session id the request named, and for
    /// `pty_input_probe_result` the input's `probe_id`. The arrangement a spawn changes carries
    /// that id too, ahead of `spawn_result`, and answers nothing.
    fn answers(&self) -> Option<String> {
        match self.event.as_str() {
            "spawn_result" | "rename_result" | "reload_session_result" => self.id.clone(),
            "pty_input_probe_result" => self.probe_id.clone(),
            "profile_arrangement_changed" => None,
            _ => self.request_id.clone(),
        }
    }
}

impl Task {
    async fn run(self) {
        let mut stopped = self.stopped.clone();
        let mut retry = FIRST_RETRY;
        loop {
            let start_failed = tokio::select! {
                failed = self.start_daemon() => failed,
                _ = stopped.wait_for(|stop| *stop) => return,
            };
            if start_failed.is_none() {
                let ended = tokio::select! {
                    ended = self.session() => ended,
                    _ = stopped.wait_for(|stop| *stop) => Ended::Stopped,
                };
                self.shared.close();
                match ended {
                    Ended::Stopped => return,
                    Ended::Refused(refusal) => {
                        self.store.set_connection(Connection::Refused(refusal));
                        self.park(Parked::Stopped);
                        let _ = stopped.wait_for(|stop| *stop).await;
                        return;
                    }
                    Ended::Lost { greeted } => {
                        if greeted {
                            retry = FIRST_RETRY;
                        }
                    }
                }
            }
            self.store
                .set_connection(Connection::Reconnecting { start_failed });
            let at = self.config.clock.now() + retry;
            self.park(Parked::Until(at));
            tokio::select! {
                () = self.config.clock.sleep_until(at) => {}
                _ = stopped.wait_for(|stop| *stop) => return,
            }
            self.park(Parked::Busy);
            retry = (retry * 3 / 2).min(LAST_RETRY);
        }
    }

    /// Why the daemon could not be started, when it could not. The start runs on a thread of
    /// its own, so a link stopped meanwhile does not wait for it.
    async fn start_daemon(&self) -> Option<String> {
        let start = self.config.start_daemon.clone()?;
        let (started, result) = tokio::sync::oneshot::channel();
        std::thread::Builder::new()
            .name("attn-daemon-start".into())
            .spawn(move || {
                let _ = started.send(start());
            })
            .expect("the daemon start's thread");
        result
            .await
            .unwrap_or_else(|_| Err("starting the daemon panicked".into()))
            .err()
    }

    fn park(&self, parked: Parked) {
        self.shared.count(|t| t.parked = parked);
    }

    async fn session(&self) -> Ended {
        if let Ok(daemon) = self.health_instance().await
            && daemon != self.config.instance
        {
            return Ended::Refused(Refusal::InstanceMismatch {
                app: self.config.instance.clone(),
                daemon,
            });
        }
        let url = format!("ws://{}/ws", self.config.daemon);
        let Ok((socket, _)) =
            tokio_tungstenite::connect_async_with_config(url, Some(socket_config()), false).await
        else {
            return Ended::Lost { greeted: false };
        };
        let (mut write, mut read) = socket.split();
        let (outbox, mut commands) = mpsc::unbounded_channel();
        let hello = serde_json::to_string(&self.hello()).expect("client_hello serializes");
        self.shared.open(outbox, hello);
        let mut greeted = false;
        loop {
            self.park(Parked::Reading);
            tokio::select! {
                message = read.next() => {
                    self.park(Parked::Busy);
                    let refusal = match message {
                        Some(Ok(Message::Text(text))) => self.apply(text.as_str(), &mut greeted),
                        Some(Ok(Message::Binary(bytes))) => {
                            match Frame::decode(&bytes) {
                                Ok(frame) => (self.config.terminals)(TerminalEvent::Frame(frame)),
                                Err(why) => eprintln!("attn-app: dropped {why}"),
                            }
                            None
                        }
                        Some(Ok(Message::Ping(_) | Message::Pong(_) | Message::Frame(_))) => continue,
                        Some(Ok(Message::Close(_)) | Err(_)) | None => {
                            self.shared.count(|t| t.received += 1);
                            return Ended::Lost { greeted };
                        }
                    };
                    self.shared.count(|t| t.received += 1);
                    if let Some(refusal) = refusal {
                        let _ = write.close().await;
                        return Ended::Refused(refusal);
                    }
                }
                Some(command) = commands.recv() => {
                    if write.send(Message::text(command)).await.is_err() {
                        return Ended::Lost { greeted };
                    }
                }
            }
        }
    }

    fn hello(&self) -> ClientHelloMessage {
        let selected = self.store.facts().profiles.selected.clone();
        ClientHelloMessage {
            client_kind: CLIENT_KIND.into(),
            version: format!("protocol-{PROTOCOL_VERSION}"),
            capabilities: CAPABILITIES.map(String::from).to_vec(),
            client_token: Some(self.config.client_token.clone()),
            browser_host_token: self.config.host_token.clone(),
            client_id: Some(self.config.client_id.clone()),
            profile_id: if selected.is_empty() {
                self.config.profile_id.clone()
            } else {
                Some(selected)
            },
            app_pid: Some(i64::from(self.config.app_pid)),
            ..ClientHelloMessage::new()
        }
    }

    fn apply(&self, text: &str, greeted: &mut bool) -> Option<Refusal> {
        let Ok(header) = serde_json::from_str::<Header>(text) else {
            eprintln!("attn-app: dropped a daemon message that is not a JSON object");
            return None;
        };
        let request_id = header.answers().filter(|id| !id.is_empty());
        if (request_id.is_some() || header.event == "command_error")
            && let Ok(body) = serde_json::from_str(text)
        {
            (self.config.replies)(Reply {
                request_id: request_id.unwrap_or_default(),
                event: header.event.clone(),
                body,
            });
        }
        match header.event.as_str() {
            "initial_state" => {
                if let Some(daemon) = header
                    .protocol_version
                    .filter(|v| !v.is_empty() && v != PROTOCOL_VERSION)
                {
                    return Some(Refusal::ProtocolMismatch {
                        app: PROTOCOL_VERSION.into(),
                        daemon,
                    });
                }
                let state: InitialStateMessage = match serde_json::from_str(text) {
                    Ok(state) => state,
                    Err(why) => {
                        eprintln!("attn-app: dropped an unreadable initial_state: {why}");
                        return None;
                    }
                };
                *greeted = true;
                self.store.apply_initial_state(state);
                None
            }
            "desktop_tile_content" => {
                self.store.set_tile_content(parse(&header.event, text)?);
                None
            }
            "settings_updated" => {
                let message: SettingsUpdatedMessage = parse(&header.event, text)?;
                if let Some(settings) = message.settings {
                    self.store.set_settings(settings);
                }
                None
            }
            "profile_arrangement_changed" => {
                let message: ProfileArrangementChangedMessage = parse(&header.event, text)?;
                self.store
                    .apply_arrangement(message.profile, message.desktops);
                None
            }
            "garden_seeds_updated" => {
                let message: GardenSeedsUpdatedMessage = parse(&header.event, text)?;
                self.store
                    .set_garden(message.profile_id, message.seeds, message.total);
                None
            }
            "garden_seeds_changed" => {
                let message: GardenSeedsChangedMessage = parse(&header.event, text)?;
                self.store.change_garden(
                    message.profile_id,
                    message.order,
                    message.seeds,
                    message.total,
                );
                None
            }
            "github_hosts_updated" => {
                let message: GitHubHostsUpdatedMessage = parse(&header.event, text)?;
                self.store.set_github(crate::store::GitHub {
                    hosts: message.github_hosts,
                    polling_off_reason: message.github_polling_off_reason.filter(|r| !r.is_empty()),
                });
                None
            }
            "notifications_updated" => {
                let m: NotificationsUpdatedMessage = parse(&header.event, text)?;
                self.store.count_notifications(
                    count(m.unread_count),
                    count(m.unread_critical_count),
                    m.critical_title,
                    true,
                );
                None
            }
            "notification_list_result" => {
                let m: NotificationListResultMessage = parse(&header.event, text)?;
                if m.success {
                    self.store.count_notifications(
                        count(m.unread_count),
                        count(m.unread_critical_count),
                        m.critical_title,
                        false,
                    );
                }
                None
            }
            "outposts_updated" => {
                let message: OutpostsUpdatedMessage = parse(&header.event, text)?;
                self.store.set_outposts(message.outposts);
                None
            }
            "asks_updated" => {
                let message: AsksUpdatedMessage = parse(&header.event, text)?;
                self.store.set_asks(&message.profile_id, message.asks);
                None
            }
            "profiles_changed" => {
                let message: ProfilesChangedMessage = parse(&header.event, text)?;
                self.store.set_profiles(message.profiles);
                None
            }
            "session_registered" | "session_state_changed" => {
                let message: SessionRegisteredMessage = parse(&header.event, text)?;
                self.store.upsert_session(message.session);
                None
            }
            "vents_updated" => {
                let message: VentsUpdatedMessage = parse(&header.event, text)?;
                self.store.set_vents(&message.profile_id, message.vents);
                None
            }
            "update_changed" => {
                let message: UpdateChangedMessage = parse(&header.event, text)?;
                self.store.set_update_run(message.run);
                None
            }
            "tasks_changed" => {
                self.store.tasks_changed();
                None
            }
            "kept_conversations_changed" => {
                self.store.kept_conversations_changed();
                None
            }
            "worktree_state_changed" => {
                let message: WorktreeStateChangedEvent = parse(&header.event, text)?;
                self.store.observe_worktrees(message.worktrees);
                None
            }
            "worktree_swept" => {
                let message: WorktreeSweptEvent = parse(&header.event, text)?;
                self.store.sweep_worktree(message.sweep_entry);
                None
            }
            "git_operation_started" | "git_operation_finished" => {
                let message: GitOperationStartedMessage = parse(&header.event, text)?;
                self.store.git_operation(message.operation);
                None
            }
            "session_closed" => {
                let message: SessionClosedMessage = parse(&header.event, text)?;
                self.store.add_close(message.session_ledger_entry);
                None
            }
            "session_unregistered" => {
                let message: SessionUnregisteredMessage = parse(&header.event, text)?;
                self.store.remove_session(&message.session.id);
                None
            }
            "sessions_updated" => {
                let message: SessionsUpdatedMessage = parse(&header.event, text)?;
                self.store
                    .set_sessions(message.sessions.unwrap_or_default());
                None
            }
            "attach_result" => {
                let message = parse(&header.event, text)?;
                (self.config.terminals)(TerminalEvent::Attached(Box::new(message)));
                None
            }
            "pty_resized" => {
                (self.config.terminals)(TerminalEvent::Resized(parse(&header.event, text)?));
                None
            }
            "kitty_placements" => {
                (self.config.terminals)(TerminalEvent::Placements(parse(&header.event, text)?));
                None
            }
            "kitty_image_result" => {
                (self.config.terminals)(TerminalEvent::ImageResult(parse(&header.event, text)?));
                None
            }
            "pty_desync" => {
                (self.config.terminals)(TerminalEvent::Desync(parse(&header.event, text)?));
                None
            }
            "runtime_respawned" => {
                (self.config.terminals)(TerminalEvent::Respawned(parse(&header.event, text)?));
                None
            }
            "session_messages_changed" => {
                (self.config.terminals)(TerminalEvent::MessagesChanged(parse(
                    &header.event,
                    text,
                )?));
                None
            }
            "session_exited" => {
                (self.config.terminals)(TerminalEvent::Exited(parse(&header.event, text)?));
                None
            }
            "command_error" => {
                let error: CommandErrorMessage = serde_json::from_str(text).ok()?;
                (error.error_code.as_deref() == Some(go_values::ERROR_CODE_UNAUTHORIZED_CLIENT))
                    .then_some(Refusal::Unauthorized(error.error))
            }
            _ => None,
        }
    }

    /// The instance the daemon on our port says it serves. An unreachable `/health` is not a
    /// mismatch: the WebSocket connect that follows fails the same way and retries.
    async fn health_instance(&self) -> Result<String, ()> {
        #[derive(Deserialize)]
        struct Health {
            #[serde(default)]
            instance: String,
        }
        let mut stream = TcpStream::connect(&self.config.daemon)
            .await
            .map_err(drop)?;
        stream
            .write_all(
                format!(
                    "GET /health HTTP/1.0\r\nHost: {}\r\n\r\n",
                    self.config.daemon
                )
                .as_bytes(),
            )
            .await
            .map_err(drop)?;
        let mut response = Vec::new();
        stream.read_to_end(&mut response).await.map_err(drop)?;
        let mut headers = [httparse::EMPTY_HEADER; 32];
        let mut head = httparse::Response::new(&mut headers);
        let httparse::Status::Complete(body) = head.parse(&response).map_err(drop)? else {
            return Err(());
        };
        if head.code != Some(200) {
            return Err(());
        }
        let health: Health = serde_json::from_slice(&response[body..]).map_err(drop)?;
        Ok(if health.instance.is_empty() {
            "default".into()
        } else {
            health.instance
        })
    }
}

fn parse<T: serde::de::DeserializeOwned>(event: &str, text: &str) -> Option<T> {
    serde_json::from_str(text)
        .inspect_err(|why| eprintln!("attn-app: dropped an unreadable {event}: {why}"))
        .ok()
}

/// The largest message the daemon sends is one kitty image's pixels, which the daemon caps at
/// 400 MiB (`maxKittyImageBytes` in `internal/ghosttyvt/kitty_png.go`) and sends as one frame.
const DAEMON_MESSAGE_BYTES: usize = 512 * 1024 * 1024;

fn socket_config() -> tokio_tungstenite::tungstenite::protocol::WebSocketConfig {
    tokio_tungstenite::tungstenite::protocol::WebSocketConfig::default()
        .max_message_size(Some(DAEMON_MESSAGE_BYTES))
        .max_frame_size(Some(DAEMON_MESSAGE_BYTES))
}

fn count(n: i64) -> u32 {
    u32::try_from(n).unwrap_or(0)
}
