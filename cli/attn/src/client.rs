use crate::instance::Paths;
use crate::paths::os_error_text;
use attn_protocol::RecordAnalyticsMessage;
use attn_protocol::go_values::{MAX_SOCKET_FRAME_BYTES, REFUSAL_UNCLASSIFIED};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::BTreeMap;
use std::fmt;
use std::fs;
use std::io::{self, Read, Write};
use std::os::unix::net::UnixStream;
use std::time::Duration;

const RECORD_ANALYTICS_DEADLINE: Duration = Duration::from_secs(2);

#[derive(Debug)]
pub struct SendError {
    message: String,
    pub refusal: Option<String>,
}

impl SendError {
    fn plain(message: String) -> Self {
        Self {
            message,
            refusal: None,
        }
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

pub struct Client<'a> {
    paths: &'a Paths<'a>,
    socket_path: String,
    caller: String,
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
        }
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

    pub fn send<M: Serialize>(&self, message: &M) -> Result<(), SendError> {
        let frame = self.frame(message)?;
        let mut connection = self.connect()?;
        let write_result = connection.write_all(&frame);
        let reply = read_reply(&mut connection).map_err(|error| {
            SendError::plain(match write_result {
                Err(ref write_error) => format!("send message: {}", os_error_text(write_error)),
                Ok(()) => format!("receive response: {}", reply_read_text(&error)),
            })
        })?;
        let head: ReplyHead = serde_json::from_slice(&reply)
            .map_err(|error| SendError::plain(format!("receive response: {error}")))?;
        if head.ok {
            return Ok(());
        }
        Err(SendError {
            message: format!("daemon error: {}", head.error.unwrap_or_default()),
            refusal: Some(
                head.refusal
                    .unwrap_or_else(|| REFUSAL_UNCLASSIFIED.to_string()),
            ),
        })
    }

    pub fn record_analytics(
        &self,
        name: &str,
        props: BTreeMap<String, Value>,
    ) -> Result<(), SendError> {
        let mut message = RecordAnalyticsMessage::new();
        message.stream = "cli".to_string();
        message.name = name.to_string();
        message.props = Some(props);
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
