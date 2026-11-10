pub mod automation;
pub mod browser;
pub mod callout;
pub mod codex;
pub mod conversation;
pub mod crew;
pub mod doc;
pub mod enrollment;
pub mod instance_env;
pub mod instance_resolve;
pub mod journal;
pub mod open;
pub mod pr;
pub mod review;
pub mod seed;
pub mod send;
pub mod settings;
pub mod support;
pub mod terminal;
pub mod ticket;
pub mod top_level;
pub mod worktree;

use crate::client::{Reply, SendError};
use crate::instance::Paths;
use attn::go::json;
use attn_protocol::PROTOCOL_VERSION;
use std::io::{self, Write};
use std::net::{Ipv4Addr, SocketAddr, TcpStream};
use std::path::Path;
use std::time::Duration;

pub const USAGE_UNKNOWN_COMMAND: &str = "unknown_command";
pub const USAGE_UNKNOWN_FLAG: &str = "unknown_flag";
const USAGE_MISSING_VALUE: &str = "missing_value";
const USAGE_INVALID_VALUE: &str = "invalid_value";

pub struct Stop(pub u8);

pub struct Run<'a> {
    pub paths: &'a Paths<'a>,
    pub terminal: String,
    pub path: String,
    pub usage_error: Option<&'static str>,
    pub refusal: Option<String>,
    pub closed_session: Option<String>,
}

impl Run<'_> {
    pub fn fail_send(&mut self, label: &str, error: &SendError) -> Stop {
        if let Some(refusal) = &error.refusal {
            self.refusal = Some(refusal.clone());
        }
        eprintln!("{label}: {error}");
        Stop(1)
    }

    pub fn record_flag_error(&mut self, message: &str) {
        self.usage_error = Some(flag_error_kind(message));
    }
}

fn flag_error_kind(message: &str) -> &'static str {
    if message.starts_with("flag provided but not defined") {
        USAGE_UNKNOWN_FLAG
    } else if message.starts_with("flag needs an argument") {
        USAGE_MISSING_VALUE
    } else {
        USAGE_INVALID_VALUE
    }
}

pub fn print_stdout(text: &str) {
    let mut stdout = io::stdout().lock();
    let _ = stdout.write_all(text.as_bytes());
    let _ = stdout.flush();
}

pub fn print_json(raw: &[u8]) {
    let mut output = json::indent(raw);
    output.push(b'\n');
    let mut stdout = io::stdout().lock();
    let _ = stdout.write_all(&output);
    let _ = stdout.flush();
}

pub fn print_compact_line(raw: &str) {
    print_stdout(raw);
    print_stdout("\n");
}

pub fn print_reply_field(reply: &Reply, path: &[&str]) -> Result<(), SendError> {
    print_json(reply.raw_field(path)?.as_bytes());
    Ok(())
}

pub fn absolute(path: &str) -> Result<String, io::Error> {
    if path.starts_with('/') {
        return Ok(crate::paths::clean(path));
    }
    Ok(crate::paths::join(&working_directory()?, path))
}

fn working_directory() -> Result<String, io::Error> {
    use std::os::unix::fs::MetadataExt;
    if let Some(pwd) = std::env::var_os("PWD")
        && Path::new(&pwd).is_absolute()
        && let (Ok(claimed), Ok(actual)) = (std::fs::metadata(&pwd), std::fs::metadata("."))
        && claimed.dev() == actual.dev()
        && claimed.ino() == actual.ino()
    {
        return Ok(pwd.to_string_lossy().into_owned());
    }
    Ok(std::env::current_dir()?.to_string_lossy().into_owned())
}

const HEALTH_DEADLINE: Duration = Duration::from_millis(400);

pub fn warn_if_stale(paths: &Paths<'_>) {
    let daemon = daemon_protocol(&paths.ws_port()).unwrap_or_default();
    let daemon = daemon.trim();
    if !daemon.is_empty() && daemon != PROTOCOL_VERSION {
        eprintln!(
            "attn: warning: this CLI speaks protocol {PROTOCOL_VERSION} but the running attn app speaks {daemon}. You may be running a stale attn — check `which -a attn`, then reinstall or use $ATTN_WRAPPER_PATH."
        );
    }
}

fn daemon_protocol(port: &str) -> Option<String> {
    let port: u16 = port.parse().ok()?;
    let address = SocketAddr::from((Ipv4Addr::LOCALHOST, port));
    let mut stream = TcpStream::connect_timeout(&address, HEALTH_DEADLINE).ok()?;
    stream.set_read_timeout(Some(HEALTH_DEADLINE)).ok()?;
    stream.set_write_timeout(Some(HEALTH_DEADLINE)).ok()?;
    stream
        .write_all(b"GET /health HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
        .ok()?;
    let mut response = Vec::new();
    std::io::Read::read_to_end(&mut stream, &mut response).ok()?;
    let text = std::str::from_utf8(&response).ok()?;
    let (head, body) = text.split_once("\r\n\r\n")?;
    if head.split_ascii_whitespace().nth(1) != Some("200") {
        return None;
    }
    let health: serde_json::Value = serde_json::from_str(body).ok()?;
    health["protocol"].as_str().map(str::to_string)
}
