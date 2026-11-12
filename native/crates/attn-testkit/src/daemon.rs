use std::collections::{HashMap, VecDeque};
use std::net::SocketAddr;
use std::pin::Pin;
use std::sync::{Arc, Mutex};
use std::task::{Context, Poll};
use std::thread::JoinHandle;

use attn_client::Pulse;
use attn_protocol::{InitialStateMessage, PROTOCOL_VERSION};
use futures_util::{SinkExt, StreamExt};
use serde_json::{Value, json};
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt, ReadBuf};
use tokio::net::{TcpListener, TcpStream};
use tokio::sync::{mpsc, watch};
use tokio_tungstenite::tungstenite::Message;

pub const CLIENT_TOKEN: &str = "wire-client-token";
pub const INSTANCE: &str = "wire";

/// A command the app sent, as the daemon read it off the socket.
pub type Request = Value;

type Handler = Box<dyn Fn(&Request) -> Vec<Value> + Send>;

/// What a scripted daemon answers to one request of an `answer_in_turn` command.
pub enum Answer {
    Reply(Value),
    Hold,
}

pub trait IntoReplies {
    fn into_replies(self) -> Vec<Value>;
}

impl IntoReplies for () {
    fn into_replies(self) -> Vec<Value> {
        Vec::new()
    }
}

impl IntoReplies for Value {
    fn into_replies(self) -> Vec<Value> {
        vec![self]
    }
}

impl IntoReplies for Option<Value> {
    fn into_replies(self) -> Vec<Value> {
        self.into_iter().collect()
    }
}

impl IntoReplies for Vec<Value> {
    fn into_replies(self) -> Vec<Value> {
        self
    }
}

/// Any protocol message as the JSON the daemon sends.
pub fn message(message: impl serde::Serialize) -> Value {
    serde_json::to_value(message).expect("a protocol message serializes")
}

/// What the daemon has exchanged with the app on its newest connection, numbered from 1 in
/// the order the app opened them. `queued` counts messages as the script hands them over,
/// and the hang-up; `received` counts commands once the script has answered them.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Traffic {
    pub connection: usize,
    pub queued: u64,
    pub received: u64,
}

enum Out {
    Text(String),
    Binary(Vec<u8>),
    HangUp,
}

struct Connection {
    id: usize,
    out: mpsc::UnboundedSender<Out>,
    greeted: bool,
}

struct Script {
    initial_state: InitialStateMessage,
    instance: String,
    client_token: String,
    refusing: bool,
    handlers: HashMap<String, Handler>,
    in_turn: HashMap<String, VecDeque<Answer>>,
}

#[derive(Default)]
struct Log {
    requests: Vec<(usize, Request)>,
    traffic: Traffic,
    attempts: usize,
    connections: Vec<Connection>,
    opened: usize,
    violations: Vec<String>,
}

struct Shared {
    script: Mutex<Script>,
    log: Mutex<Log>,
    pulse: Pulse,
}

/// The daemon side of a wire test: a WebSocket server on a loopback port that answers the
/// app from a script the test writes. Also serves `/health`, as the real daemon does.
pub struct Daemon {
    shared: Arc<Shared>,
    address: SocketAddr,
    stop: watch::Sender<bool>,
    thread: Option<JoinHandle<()>>,
}

impl Daemon {
    /// A daemon that answers the handshake and attaches every terminal with an empty screen at
    /// the size the app last asked for, until a test scripts `attach_session` itself.
    pub fn scripted() -> Daemon {
        let shared = Arc::new(Shared {
            script: Mutex::new(Script {
                initial_state: InitialStateMessage::new(),
                instance: INSTANCE.into(),
                client_token: CLIENT_TOKEN.into(),
                refusing: false,
                handlers: HashMap::new(),
                in_turn: HashMap::new(),
            }),
            log: Mutex::new(Log::default()),
            pulse: Pulse::default(),
        });
        let listener = std::net::TcpListener::bind("127.0.0.1:0")
            .expect("a loopback port for the scripted daemon");
        listener
            .set_nonblocking(true)
            .expect("a non-blocking listener");
        let address = listener
            .local_addr()
            .expect("the scripted daemon's address");
        let (stop, stopped) = watch::channel(false);
        let serving = shared.clone();
        let thread = std::thread::Builder::new()
            .name("scripted-daemon".into())
            .spawn(move || {
                let runtime = tokio::runtime::Builder::new_current_thread()
                    .enable_all()
                    .build()
                    .expect("the daemon's runtime");
                runtime.block_on(serve(serving, listener, stopped));
            })
            .expect("the daemon's thread");
        let daemon = Daemon {
            shared,
            address,
            stop,
            thread: Some(thread),
        };
        let sizes: Arc<Mutex<HashMap<String, (Value, Value)>>> = Arc::default();
        let resized = sizes.clone();
        daemon.on("pty_resize", move |request| {
            let id = request["id"].as_str().unwrap_or_default().to_owned();
            let size = (request["cols"].clone(), request["rows"].clone());
            resized.lock().unwrap().insert(id, size);
        });
        daemon.on("attach_session", move |request| {
            let size = sizes
                .lock()
                .unwrap()
                .get(request["id"].as_str().unwrap_or_default())
                .cloned()
                .unwrap_or((json!(80), json!(24)));
            json!({
                "event": "attach_result", "id": request["id"], "success": true, "running": true,
                "cols": size.0, "rows": size.1,
            })
        });
        daemon
    }

    /// What `client_hello` answers with. A `protocol_version` left out is this build's.
    pub fn state(self, initial_state: InitialStateMessage) -> Daemon {
        self.answer_hello_with(initial_state);
        self
    }

    /// What `client_hello` answers with from now on, as a daemon that restarted with another
    /// state answers the next connection.
    pub fn answer_hello_with(&self, initial_state: InitialStateMessage) {
        self.shared.script.lock().unwrap().initial_state = initial_state;
    }

    /// The instance `/health` reports. The world's app belongs to `INSTANCE`.
    pub fn instance(self, instance: &str) -> Daemon {
        instance.clone_into(&mut self.shared.script.lock().unwrap().instance);
        self
    }

    /// The token this daemon minted. The world's app presents `CLIENT_TOKEN`.
    pub fn client_token(self, token: &str) -> Daemon {
        token.clone_into(&mut self.shared.script.lock().unwrap().client_token);
        self
    }

    pub fn address(&self) -> SocketAddr {
        self.address
    }

    pub fn pulse(&self) -> Pulse {
        self.shared.pulse.clone()
    }

    /// Answers each `cmd` the app sends. The replies get the request's `request_id`.
    pub fn on<R: IntoReplies>(
        &self,
        cmd: &str,
        reply: impl Fn(&Request) -> R + Send + 'static,
    ) -> &Daemon {
        self.shared.script.lock().unwrap().handlers.insert(
            cmd.into(),
            Box::new(move |request| reply(request).into_replies()),
        );
        self
    }

    /// Answers successive `cmd` requests in order. `Answer::Hold` leaves one unanswered, for
    /// `reply_to`; a request past the last answer breaks the script.
    pub fn answer_in_turn(&self, cmd: &str, answers: impl IntoIterator<Item = Answer>) -> &Daemon {
        self.shared
            .script
            .lock()
            .unwrap()
            .in_turn
            .insert(cmd.into(), answers.into_iter().collect());
        self
    }

    /// Pushes an event to the open connection.
    pub fn emit(&self, event: impl serde::Serialize) {
        let event = message(event);
        let open = self
            .shared
            .log
            .lock()
            .unwrap()
            .connections
            .last()
            .map(|c| (c.id, c.out.clone()));
        let open = open.expect("the daemon has no open connection to emit to");
        queue(&self.shared, &open, Out::Text(event.to_string()));
    }

    /// Pushes a binary frame (PTY output, a kitty image) to the open connection, as the bytes
    /// go on the wire.
    pub fn emit_frame(&self, frame: Vec<u8>) {
        let open = self
            .shared
            .log
            .lock()
            .unwrap()
            .connections
            .last()
            .map(|c| (c.id, c.out.clone()));
        let open = open.expect("the daemon has no open connection to emit to");
        queue(&self.shared, &open, Out::Binary(frame));
    }

    /// Answers a request the script held.
    pub fn reply_to(&self, request: &Request, reply: impl serde::Serialize) {
        let reply = correlate(request, message(reply));
        let open = {
            let log = self.shared.log.lock().unwrap();
            let origin = log
                .requests
                .iter()
                .find(|(_, r)| r == request)
                .map(|(id, _)| *id)
                .expect("the app never sent this request");
            log.connections
                .iter()
                .find(|c| c.id == origin)
                .map(|c| (c.id, c.out.clone()))
                .expect("the request's connection has closed")
        };
        queue(&self.shared, &open, Out::Text(reply.to_string()));
    }

    /// Hangs up on the app, as a daemon restart does.
    pub fn disconnect(&self) {
        let open = self.shared.log.lock().unwrap().connections.pop();
        let open = open.expect("the daemon has no open connection to hang up");
        queue(&self.shared, &(open.id, open.out), Out::HangUp);
    }

    /// While set, the daemon closes every connection before answering, as a port with no
    /// daemon behind it refuses one.
    pub fn refuse_connections(&self, refusing: bool) {
        self.shared.script.lock().unwrap().refusing = refusing;
    }

    /// Every command the app sent, in order, across connections.
    pub fn sent(&self) -> Vec<Request> {
        self.shared
            .log
            .lock()
            .unwrap()
            .requests
            .iter()
            .map(|(_, r)| r.clone())
            .collect()
    }

    pub fn sent_of(&self, cmd: &str) -> Vec<Request> {
        self.sent()
            .into_iter()
            .filter(|r| r["cmd"] == cmd)
            .collect()
    }

    /// WebSocket connections the app opened.
    pub fn connections(&self) -> usize {
        self.shared.log.lock().unwrap().opened
    }

    /// Times the app tried to open one, refused or not.
    pub fn attempts(&self) -> usize {
        self.shared.log.lock().unwrap().attempts
    }

    pub fn traffic(&self) -> Traffic {
        self.shared.log.lock().unwrap().traffic
    }

    /// How the app broke the script, for the world to fail the test with.
    pub fn violations(&self) -> Vec<String> {
        self.shared.log.lock().unwrap().violations.clone()
    }
}

impl Drop for Daemon {
    fn drop(&mut self) {
        let _ = self.stop.send(true);
        if let Some(thread) = self.thread.take() {
            let _ = thread.join();
        }
    }
}

fn queue(shared: &Shared, (id, to): &(usize, mpsc::UnboundedSender<Out>), out: Out) {
    if to.send(out).is_ok() {
        let mut log = shared.log.lock().unwrap();
        if log.traffic.connection == *id {
            log.traffic.queued += 1;
        }
        drop(log);
        shared.pulse.beat();
    }
}

fn correlate(request: &Request, mut reply: Value) -> Value {
    if let (Some(id), Some(fields)) = (request.get("request_id"), reply.as_object_mut()) {
        fields.entry("request_id").or_insert_with(|| id.clone());
    }
    reply
}

async fn serve(
    shared: Arc<Shared>,
    listener: std::net::TcpListener,
    mut stopped: watch::Receiver<bool>,
) {
    let listener = TcpListener::from_std(listener).expect("a tokio listener");
    loop {
        tokio::select! {
            accepted = listener.accept() => {
                let Ok((stream, _)) = accepted else { continue };
                tokio::spawn(connection(shared.clone(), stream));
            }
            _ = stopped.wait_for(|stop| *stop) => return,
        }
    }
}

async fn connection(shared: Arc<Shared>, mut stream: TcpStream) {
    let Some(head) = read_head(&mut stream).await else {
        return;
    };
    let (instance, refusing) = {
        let script = shared.script.lock().unwrap();
        (script.instance.clone(), script.refusing)
    };
    if head.starts_with(b"GET /health ") {
        if refusing {
            return;
        }
        if !addressed_to_an_ip(&head) {
            let _ = stream
                .write_all(b"HTTP/1.0 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
                .await;
            return;
        }
        let body = json!({ "instance": instance }).to_string();
        let response = format!(
            "HTTP/1.0 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\n\r\n{body}",
            body.len()
        );
        let _ = stream.write_all(response.as_bytes()).await;
        return;
    }
    shared.log.lock().unwrap().attempts += 1;
    shared.pulse.beat();
    if refusing {
        return;
    }
    let Ok(socket) = tokio_tungstenite::accept_async(Replay {
        head,
        at: 0,
        stream,
    })
    .await
    else {
        return;
    };
    let (mut write, mut read) = socket.split();
    let (out, mut outgoing) = mpsc::unbounded_channel();
    let id = {
        let mut log = shared.log.lock().unwrap();
        log.opened += 1;
        let id = log.opened;
        log.traffic = Traffic {
            connection: id,
            ..Traffic::default()
        };
        log.connections.push(Connection {
            id,
            out,
            greeted: false,
        });
        id
    };
    shared.pulse.beat();
    loop {
        tokio::select! {
            incoming = read.next() => match incoming {
                Some(Ok(Message::Text(text))) => receive(&shared, id, text.as_str()),
                Some(Ok(Message::Ping(_) | Message::Pong(_) | Message::Binary(_) | Message::Frame(_))) => {}
                Some(Ok(Message::Close(_)) | Err(_)) | None => break,
            },
            Some(out) = outgoing.recv() => match out {
                Out::Text(text) => {
                    if write.send(Message::text(text)).await.is_err() {
                        break;
                    }
                }
                Out::Binary(bytes) => {
                    if write.send(Message::binary(bytes)).await.is_err() {
                        break;
                    }
                }
                Out::HangUp => {
                    let _ = write.close().await;
                    break;
                }
            },
        }
    }
    shared
        .log
        .lock()
        .unwrap()
        .connections
        .retain(|c| c.id != id);
}

fn receive(shared: &Shared, id: usize, text: &str) {
    let request: Request =
        serde_json::from_str(text).unwrap_or_else(|_| json!({ "cmd": "", "unreadable": text }));
    let cmd = request["cmd"].as_str().unwrap_or_default().to_owned();
    let mut log = shared.log.lock().unwrap();
    log.requests.push((id, request.clone()));
    let Some(at) = log.connections.iter().position(|c| c.id == id) else {
        return;
    };
    let mut replies = Vec::new();
    let mut hang_up = false;
    if !log.connections[at].greeted {
        if cmd == "client_hello" {
            let script = shared.script.lock().unwrap();
            if request["client_token"] == script.client_token.as_str() {
                log.connections[at].greeted = true;
                replies = match script.handlers.get("client_hello") {
                    Some(handler) => handler(&request),
                    None => vec![initial_state(&script)],
                };
            } else {
                replies.push(json!({
                    "event": "command_error", "cmd": "client_hello", "success": false,
                    "error_code": "unauthorized_client",
                    "error": "client_hello refused: client_token does not match this daemon's",
                }));
                hang_up = true;
            }
        } else {
            log.violations
                .push(format!("{cmd} arrived before client_hello"));
        }
    } else {
        let mut script = shared.script.lock().unwrap();
        if let Some(answers) = script.in_turn.get_mut(&cmd) {
            match answers.pop_front() {
                Some(Answer::Reply(reply)) => replies.push(reply),
                Some(Answer::Hold) => {}
                None => log.violations.push(format!(
                    "{cmd} arrived after the last of its answers in turn"
                )),
            }
        } else if let Some(handler) = script.handlers.get(&cmd) {
            replies = handler(&request);
        }
    }
    let out = (id, log.connections[at].out.clone());
    drop(log);
    for reply in replies {
        queue(
            shared,
            &out,
            Out::Text(correlate(&request, reply).to_string()),
        );
    }
    if hang_up {
        let _ = out.1.send(Out::HangUp);
    }
    let mut log = shared.log.lock().unwrap();
    if log.traffic.connection == id {
        log.traffic.received += 1;
    }
    drop(log);
    shared.pulse.beat();
}

fn initial_state(script: &Script) -> Value {
    let mut state = script.initial_state.clone();
    state
        .protocol_version
        .get_or_insert_with(|| PROTOCOL_VERSION.into());
    message(state)
}

/// The real daemon answers only requests whose `Host` is localhost or an IP address, so a
/// request without one is refused here too.
fn addressed_to_an_ip(head: &[u8]) -> bool {
    String::from_utf8_lossy(head).lines().any(|line| {
        line.split_once(':').is_some_and(|(name, value)| {
            let value = value.trim();
            let host = value.rsplit_once(':').map_or(value, |(host, _)| host);
            name.eq_ignore_ascii_case("host")
                && (host == "localhost" || host.parse::<std::net::IpAddr>().is_ok())
        })
    })
}

async fn read_head(stream: &mut TcpStream) -> Option<Vec<u8>> {
    let mut head = Vec::new();
    let mut chunk = [0u8; 1024];
    while !head.windows(4).any(|w| w == b"\r\n\r\n") {
        let n = stream.read(&mut chunk).await.ok()?;
        if n == 0 {
            return None;
        }
        head.extend_from_slice(&chunk[..n]);
    }
    Some(head)
}

/// A stream that gives back the request head the daemon already read to route it.
struct Replay {
    head: Vec<u8>,
    at: usize,
    stream: TcpStream,
}

impl AsyncRead for Replay {
    fn poll_read(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &mut ReadBuf<'_>,
    ) -> Poll<std::io::Result<()>> {
        if self.at < self.head.len() {
            let n = buf.remaining().min(self.head.len() - self.at);
            let at = self.at;
            buf.put_slice(&self.head[at..at + n]);
            self.at += n;
            return Poll::Ready(Ok(()));
        }
        Pin::new(&mut self.stream).poll_read(cx, buf)
    }
}

impl AsyncWrite for Replay {
    fn poll_write(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<std::io::Result<usize>> {
        Pin::new(&mut self.stream).poll_write(cx, buf)
    }

    fn poll_flush(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<std::io::Result<()>> {
        Pin::new(&mut self.stream).poll_flush(cx)
    }

    fn poll_shutdown(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<std::io::Result<()>> {
        Pin::new(&mut self.stream).poll_shutdown(cx)
    }
}
