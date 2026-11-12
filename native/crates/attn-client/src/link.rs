use std::sync::{Arc, Mutex};
use std::thread::JoinHandle;
use std::time::Duration;

use attn_protocol::{
    ClientHelloMessage, CommandErrorMessage, InitialStateMessage, PROTOCOL_VERSION, go_values,
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

pub struct LinkConfig {
    /// The daemon's `host:port`; the link speaks WebSocket on `/ws` and reads `/health`.
    pub daemon: String,
    pub client_token: String,
    /// The instance this app was built for, `default` for production.
    pub instance: String,
    /// The profile to ask for until the daemon has said which one this client is scoped to.
    pub profile_id: Option<String>,
    pub client_id: String,
    pub app_pid: u32,
    pub clock: Clock,
    pub pulse: Pulse,
    /// PTY output and kitty images, on the link's thread, in arrival order.
    pub frames: Box<dyn Fn(Frame) + Send + Sync>,
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

/// What the link has exchanged with the daemon. `sent` counts commands as they are handed to
/// `send`, before they reach the socket; `received` counts messages once applied, and the end
/// of a connection the daemon closed.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Traffic {
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

struct Shared {
    traffic: Mutex<Traffic>,
    outbox: Mutex<Option<mpsc::UnboundedSender<String>>>,
    pulse: Pulse,
}

impl Shared {
    fn count(&self, f: impl FnOnce(&mut Traffic)) {
        f(&mut self.traffic.lock().unwrap());
        self.pulse.beat();
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
            traffic: Mutex::new(Traffic {
                sent: 0,
                received: 0,
                parked: Parked::Busy,
            }),
            outbox: Mutex::new(None),
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

    pub fn send(&self, command: &impl Serialize) -> Result<(), NotConnected> {
        let text = serde_json::to_string(command).expect("a protocol command serializes");
        let outbox = self.shared.outbox.lock().unwrap();
        let outbox = outbox.as_ref().ok_or(NotConnected)?;
        self.shared.count(|t| t.sent += 1);
        outbox.send(text).map_err(|_| NotConnected)
    }

    pub fn traffic(&self) -> Traffic {
        *self.shared.traffic.lock().unwrap()
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
}

impl Task {
    async fn run(self) {
        let mut stopped = self.stopped.clone();
        let mut retry = FIRST_RETRY;
        loop {
            let ended = tokio::select! {
                ended = self.session() => ended,
                _ = stopped.wait_for(|stop| *stop) => Ended::Stopped,
            };
            *self.shared.outbox.lock().unwrap() = None;
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
            self.store.set_connection(Connection::Reconnecting);
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
        let Ok((socket, _)) = tokio_tungstenite::connect_async(url).await else {
            return Ended::Lost { greeted: false };
        };
        let (mut write, mut read) = socket.split();
        let hello = serde_json::to_string(&self.hello()).expect("client_hello serializes");
        self.shared.count(|t| t.sent += 1);
        if write.send(Message::text(hello)).await.is_err() {
            return Ended::Lost { greeted: false };
        }
        let (outbox, mut commands) = mpsc::unbounded_channel();
        *self.shared.outbox.lock().unwrap() = Some(outbox);
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
                                Ok(frame) => (self.config.frames)(frame),
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
        match header.event.as_str() {
            "initial_state" => {
                let state: InitialStateMessage = match serde_json::from_str(text) {
                    Ok(state) => state,
                    Err(why) => {
                        eprintln!("attn-app: dropped an unreadable initial_state: {why}");
                        return None;
                    }
                };
                if let Some(daemon) = state
                    .protocol_version
                    .as_ref()
                    .filter(|v| !v.is_empty() && *v != PROTOCOL_VERSION)
                {
                    return Some(Refusal::ProtocolMismatch {
                        app: PROTOCOL_VERSION.into(),
                        daemon: daemon.clone(),
                    });
                }
                *greeted = true;
                self.store.apply_initial_state(state);
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
