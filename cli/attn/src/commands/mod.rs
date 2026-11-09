pub mod automation;
pub mod callout;
pub mod crew;
pub mod instance_env;
pub mod seed;
pub mod settings;
pub mod support;
pub mod ticket;
pub mod top_level;

use crate::client::{Reply, SendError};
use crate::instance::Paths;
use attn::go::json;
use std::io::{self, Write};
use std::path::Path;

pub const USAGE_UNKNOWN_COMMAND: &str = "unknown_command";
const USAGE_UNKNOWN_FLAG: &str = "unknown_flag";
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
