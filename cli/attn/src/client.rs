use crate::instance::Paths;
use crate::paths::os_error_text;
use attn_protocol::RecordAnalyticsMessage;
use attn_protocol::go_values::{MAX_SOCKET_FRAME_BYTES, REFUSAL_UNCLASSIFIED};
use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use serde_json::value::RawValue;
use std::collections::BTreeMap;
use std::fmt;
use std::fs;
use std::io::{self, Read, Write};
use std::os::unix::net::UnixStream;
use std::time::{Duration, Instant};

const DEADLINE_EXCEEDED: &str = "context deadline exceeded";
const RECORD_ANALYTICS_DEADLINE: Duration = Duration::from_secs(2);

#[derive(Debug)]
pub struct SendError {
    message: String,
    pub refusal: Option<String>,
}

impl SendError {
    pub fn plain(message: String) -> Self {
        Self {
            message,
            refusal: None,
        }
    }

    pub fn deadline_exceeded(&self) -> bool {
        self.message == DEADLINE_EXCEEDED
    }
}

impl fmt::Display for SendError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.message)
    }
}

#[derive(Deserialize)]
struct ReplyHead {
    #[serde(default)]
    ok: bool,
    #[serde(default)]
    error: Option<String>,
    #[serde(default)]
    refusal: Option<String>,
}

#[derive(Deserialize)]
struct AutomationReplyHead {
    #[serde(default)]
    success: bool,
    #[serde(default)]
    error: Option<String>,
}

fn garden_command(cmd: &str) -> bool {
    cmd.starts_with("seed_") || cmd.starts_with("crew_")
}

#[derive(Deserialize)]
struct NamedCmd {
    cmd: String,
}

#[derive(Default)]
pub struct JsonValueEnd {
    depth: i64,
    started: bool,
    in_string: bool,
    escaped: bool,
}

impl JsonValueEnd {
    pub fn scan(&mut self, data: &[u8]) -> Option<usize> {
        for (index, &byte) in data.iter().enumerate() {
            if self.in_string {
                match byte {
                    _ if self.escaped => self.escaped = false,
                    b'\\' => self.escaped = true,
                    b'"' => self.in_string = false,
                    _ => {}
                }
                continue;
            }
            match byte {
                b'"' => self.in_string = true,
                b'{' | b'[' => {
                    self.started = true;
                    self.depth += 1;
                }
                b'}' | b']' => {
                    self.depth -= 1;
                    if self.started && self.depth == 0 {
                        return Some(index + 1);
                    }
                }
                _ => {}
            }
        }
        None
    }
}

pub fn read_json_value(reader: &mut impl Read) -> io::Result<Vec<u8>> {
    let mut chunk = [0_u8; 16 * 1024];
    let mut value = Vec::new();
    let mut end = JsonValueEnd::default();
    loop {
        let read = match reader.read(&mut chunk) {
            Ok(read) => read,
            Err(error) if error.kind() == io::ErrorKind::Interrupted => continue,
            Err(error) => return Err(error),
        };
        if read == 0 {
            return Ok(value);
        }
        if let Some(at) = end.scan(&chunk[..read]) {
            value.extend_from_slice(&chunk[..at]);
            return Ok(value);
        }
        value.extend_from_slice(&chunk[..read]);
    }
}

fn read_reply(connection: &mut UnixStream) -> io::Result<Vec<u8>> {
    let value = read_json_value(connection)?;
    if value.is_empty() {
        return Err(io::Error::from(io::ErrorKind::UnexpectedEof));
    }
    Ok(value)
}

pub struct Reply {
    raw: Vec<u8>,
}

impl Reply {
    pub fn raw_field(&self, path: &[&str]) -> Result<&str, SendError> {
        let mut current = std::str::from_utf8(&self.raw)
            .map_err(|error| SendError::plain(format!("receive response: {error}")))?;
        for name in path {
            let fields: BTreeMap<String, &RawValue> = serde_json::from_str(current)
                .map_err(|error| SendError::plain(format!("receive response: {error}")))?;
            current = fields.get(*name).copied().map_or("null", RawValue::get);
        }
        Ok(current)
    }

    pub fn present(&self, field: &str) -> bool {
        self.raw_field(&[field]).is_ok_and(|value| value != "null")
    }

    pub fn whole<T: DeserializeOwned>(&self) -> Result<T, SendError> {
        serde_json::from_slice(&self.raw)
            .map_err(|error| SendError::plain(format!("receive response: {error}")))
    }

    pub fn value<T: DeserializeOwned>(&self, field: &str) -> Result<T, SendError> {
        serde_json::from_str(self.raw_field(&[field])?)
            .map_err(|error| SendError::plain(format!("receive response: {error}")))
    }

    pub fn optional_string(&self, field: &str) -> String {
        self.raw_field(&[field])
            .ok()
            .and_then(|raw| serde_json::from_str::<Option<String>>(raw).ok())
            .flatten()
            .unwrap_or_default()
    }
}

pub struct Client<'a> {
    paths: &'a Paths<'a>,
    socket_path: String,
    caller: String,
    garden_scope: Option<(String, String)>,
}

impl<'a> Client<'a> {
    pub fn new(paths: &'a Paths<'a>, socket_override: &str, caller: &str) -> Self {
        let socket_path = if socket_override.is_empty() {
            paths.socket_path()
        } else {
            socket_override.to_string()
        };
        Self {
            paths,
            socket_path,
            caller: caller.trim().to_string(),
            garden_scope: None,
        }
    }

    pub fn with_garden_scope(mut self, profile: &str, session: &str) -> Self {
        self.garden_scope = (!profile.is_empty() || !session.is_empty())
            .then(|| (profile.to_string(), session.to_string()));
        self
    }

    fn scoped_frame<M: Serialize>(
        &self,
        message: &M,
        in_scope: fn(&str) -> bool,
    ) -> Result<Vec<u8>, SendError> {
        let Some((profile, session)) = &self.garden_scope else {
            return self.frame(message);
        };
        let mut fields =
            serde_json::to_value(message).map_err(|error| SendError::plain(error.to_string()))?;
        if let Some(object) = fields.as_object_mut()
            && object
                .get("cmd")
                .and_then(Value::as_str)
                .is_some_and(in_scope)
        {
            for (key, value) in [("profile_id", profile), ("source_session_id", session)] {
                let empty = match object.get(key) {
                    None | Some(Value::Null) => true,
                    Some(existing) => existing.as_str() == Some(""),
                };
                if empty {
                    object.insert(key.to_string(), Value::from(value.as_str()));
                }
            }
        }
        self.frame(&fields)
    }

    pub fn call<M: Serialize>(&self, message: &M) -> Result<Reply, SendError> {
        self.call_until(message, None)
    }

    pub fn call_until<M: Serialize>(
        &self,
        message: &M,
        deadline: Option<Instant>,
    ) -> Result<Reply, SendError> {
        let frame = self.scoped_frame(message, garden_command)?;
        self.exchange(&frame, deadline).map(|raw| Reply { raw })
    }

    pub fn call_automation<M: Serialize>(&self, message: &M) -> Result<Reply, SendError> {
        let frame = self.scoped_frame(message, |_| true)?;
        let raw = self.round_trip(&frame, None)?;
        let head: AutomationReplyHead = serde_json::from_slice(&raw)
            .map_err(|error| SendError::plain(format!("receive response: {error}")))?;
        if head.success {
            return Ok(Reply { raw });
        }
        Err(SendError {
            message: format!("daemon error: {}", head.error.unwrap_or_default()),
            refusal: Some(REFUSAL_UNCLASSIFIED.to_string()),
        })
    }

    pub fn call_field<M: Serialize>(
        &self,
        message: &M,
        field: &str,
        missing: &str,
    ) -> Result<Reply, SendError> {
        let reply = self.call(message)?;
        if !reply.present(field) {
            return Err(SendError::plain(missing.to_string()));
        }
        Ok(reply)
    }

    fn frame<M: Serialize>(&self, message: &M) -> Result<Vec<u8>, SendError> {
        let mut frame =
            serde_json::to_vec(message).map_err(|error| SendError::plain(error.to_string()))?;
        if !self.caller.is_empty() {
            let caller = serde_json::to_string(&self.caller)
                .map_err(|error| SendError::plain(error.to_string()))?;
            frame.pop();
            frame.extend_from_slice(br#","caller_id":"#);
            frame.extend_from_slice(caller.as_bytes());
            frame.push(b'}');
        }
        let limit = usize::try_from(MAX_SOCKET_FRAME_BYTES).unwrap_or(usize::MAX);
        if frame.len() > limit {
            let cmd = serde_json::from_slice::<NamedCmd>(&frame)
                .map(|named| named.cmd)
                .unwrap_or_default();
            return Err(SendError::plain(format!(
                "{cmd} request: max_socket_frame_bytes={MAX_SOCKET_FRAME_BYTES}, asked for {} bytes",
                frame.len()
            )));
        }
        frame.push(b'\n');
        Ok(frame)
    }

    fn connect(&self) -> Result<UnixStream, SendError> {
        UnixStream::connect(&self.socket_path)
            .map_err(|error| SendError::plain(self.explain_connect_error(&error)))
    }

    fn explain_connect_error(&self, error: &io::Error) -> String {
        let socket = self.paths.collapse_home(&self.socket_path);
        let instance = self.paths.instance_label();
        let cause = format!(
            "dial unix {}: connect: {}",
            self.socket_path,
            os_error_text(error)
        );
        if error.kind() == io::ErrorKind::PermissionDenied && error.raw_os_error() == Some(1) {
            return format!(
                "the sandbox blocks attn's socket at {socket} (instance={instance}): {cause}\n  run this command with escalated permissions; in Codex, allow its prefix in ~/.codex/rules/default.rules for future runs"
            );
        }
        let base = format!("connect to daemon at {socket} (instance={instance}): {cause}");
        match self.cross_instance_hint() {
            Some(hint) => format!("{base}\n  {hint}"),
            None => base,
        }
    }

    fn cross_instance_hint(&self) -> Option<String> {
        let (other, describe, command) = if self.paths.instance().is_empty() {
            (
                self.paths.socket_path_for_instance("dev"),
                "a dev daemon",
                "attn instance-env dev",
            )
        } else {
            (
                self.paths.socket_path_for_instance(""),
                "the default daemon",
                "attn instance-env --unset",
            )
        };
        socket_live(&other).then(|| {
            format!(
                "hint: {describe} is listening at {} — run `eval \"$({command})\"` to switch this shell",
                self.paths.collapse_home(&other)
            )
        })
    }

    fn round_trip(&self, frame: &[u8], deadline: Option<Instant>) -> Result<Vec<u8>, SendError> {
        let mut connection = self.connect()?;
        if let Some(deadline) = deadline {
            let remaining = deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                return Err(SendError::plain(DEADLINE_EXCEEDED.to_string()));
            }
            connection
                .set_read_timeout(Some(remaining))
                .map_err(|error| SendError::plain(os_error_text(&error)))?;
        }
        let write_result = connection.write_all(frame);
        read_reply(&mut connection).map_err(|error| {
            if matches!(
                error.kind(),
                io::ErrorKind::WouldBlock | io::ErrorKind::TimedOut
            ) {
                return SendError::plain(DEADLINE_EXCEEDED.to_string());
            }
            SendError::plain(match write_result {
                Err(ref write_error) => format!("send message: {}", os_error_text(write_error)),
                Ok(()) => format!("receive response: {}", reply_read_text(&error)),
            })
        })
    }

    fn exchange(&self, frame: &[u8], deadline: Option<Instant>) -> Result<Vec<u8>, SendError> {
        let reply = self.round_trip(frame, deadline)?;
        check_reply(reply)
    }

    pub fn open_stream<M: Serialize>(&self, message: &M) -> Result<ReplyStream, SendError> {
        let frame = self.frame(message)?;
        let mut connection = self.connect()?;
        connection.write_all(&frame).map_err(|error| {
            SendError::plain(format!("send message: {}", os_error_text(&error)))
        })?;
        Ok(ReplyStream {
            connection,
            buffer: Vec::new(),
        })
    }

    pub fn is_running(&self) -> bool {
        UnixStream::connect(&self.socket_path).is_ok()
    }

    pub fn send<M: Serialize>(&self, message: &M) -> Result<(), SendError> {
        let frame = self.frame(message)?;
        self.exchange(&frame, None).map(|_| ())
    }

    pub fn record_analytics(
        &self,
        name: &str,
        session: Option<String>,
        props: BTreeMap<String, Value>,
    ) -> Result<(), SendError> {
        let mut message = RecordAnalyticsMessage::new();
        message.stream = "cli".to_string();
        message.name = name.to_string();
        message.props = Some(props);
        message.session_id = session;
        let frame = self.frame(&message)?;
        let mut connection = self.connect()?;
        let deadline = Some(RECORD_ANALYTICS_DEADLINE);
        let exchange = connection
            .set_read_timeout(deadline)
            .and_then(|()| connection.set_write_timeout(deadline))
            .and_then(|()| connection.write_all(&frame))
            .and_then(|()| io::copy(&mut connection, &mut io::sink()).map(|_| ()));
        exchange.map_err(|error| SendError::plain(os_error_text(&error)))
    }
}

fn check_reply(reply: Vec<u8>) -> Result<Vec<u8>, SendError> {
    let head: ReplyHead = serde_json::from_slice(&reply)
        .map_err(|error| SendError::plain(format!("receive response: {error}")))?;
    if head.ok {
        return Ok(reply);
    }
    Err(SendError {
        message: format!("daemon error: {}", head.error.unwrap_or_default()),
        refusal: Some(
            head.refusal
                .unwrap_or_else(|| REFUSAL_UNCLASSIFIED.to_string()),
        ),
    })
}

pub struct ReplyStream {
    connection: UnixStream,
    buffer: Vec<u8>,
}

impl ReplyStream {
    pub fn next_reply(&mut self) -> Result<Reply, SendError> {
        let value = self.next_value().map_err(|error| {
            SendError::plain(format!("receive response: {}", reply_read_text(&error)))
        })?;
        check_reply(value).map(|raw| Reply { raw })
    }

    fn next_value(&mut self) -> io::Result<Vec<u8>> {
        let mut chunk = [0_u8; 16 * 1024];
        loop {
            if let Some(at) = JsonValueEnd::default().scan(&self.buffer) {
                return Ok(self.buffer.drain(..at).collect());
            }
            match self.connection.read(&mut chunk) {
                Ok(0) => return Err(io::Error::from(io::ErrorKind::UnexpectedEof)),
                Ok(read) => self.buffer.extend_from_slice(&chunk[..read]),
                Err(error) if error.kind() == io::ErrorKind::Interrupted => {}
                Err(error) => return Err(error),
            }
        }
    }
}

fn reply_read_text(error: &io::Error) -> String {
    if error.kind() == io::ErrorKind::UnexpectedEof {
        return "EOF".to_string();
    }
    os_error_text(error)
}

fn socket_live(path: &str) -> bool {
    if path.trim().is_empty() || fs::metadata(path).is_err() {
        return false;
    }
    UnixStream::connect(path).is_ok()
}
