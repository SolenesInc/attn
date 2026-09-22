mod blocks;
mod boundary;
mod ghostty;
mod host;
mod png_decoder;
mod probe_child;
mod protocol;
mod queries;
mod segmenter;
mod session;
mod signals;
mod wire;

use std::collections::HashMap;
use std::time::Duration;

use host::{Config, Host};

const DEFAULT_IDLE_TIMEOUT: Duration = Duration::from_secs(45);

fn main() {
    let result = if std::env::args().nth(1).as_deref() == Some(probe_child::FLAG) {
        probe_child::run()
    } else {
        run()
    };
    if let Err(error) = result {
        eprintln!("attn-pty-host: {error}");
        std::process::exit(1);
    }
}

fn run() -> Result<(), String> {
    let args = parse_args(std::env::args().skip(1))?;
    Host::run(Config {
        daemon_instance_id: required(&args, "daemon-instance-id")?,
        artifact: required(&args, "generation")?,
        incarnation: args.get("incarnation").cloned().unwrap_or_default(),
        socket_path: required(&args, "socket-path")?,
        registry_dir: required(&args, "registry-dir")?,
        host_registry_path: required(&args, "host-registry-path")?,
        control_token: required(&args, "control-token")?,
        idle_timeout: idle_timeout(&args)?,
    })
}

fn idle_timeout(args: &HashMap<String, String>) -> Result<Duration, String> {
    let Some(value) = args.get("idle-timeout-ms") else {
        return Ok(DEFAULT_IDLE_TIMEOUT);
    };
    value
        .parse::<u64>()
        .map(Duration::from_millis)
        .map_err(|error| format!("invalid --idle-timeout-ms {value}: {error}"))
}

fn parse_args(args: impl Iterator<Item = String>) -> Result<HashMap<String, String>, String> {
    let mut values = HashMap::new();
    let mut args = args.peekable();
    while let Some(flag) = args.next() {
        let Some(name) = flag.strip_prefix("--") else {
            return Err(format!("unexpected argument {flag}"));
        };
        let Some(value) = args.next() else {
            return Err(format!("missing value for {flag}"));
        };
        if value.starts_with("--") {
            return Err(format!("missing value for {flag}"));
        }
        if values.insert(name.to_owned(), value).is_some() {
            return Err(format!("duplicate argument {flag}"));
        }
    }
    Ok(values)
}

fn required(args: &HashMap<String, String>, name: &str) -> Result<String, String> {
    args.get(name)
        .filter(|value| !value.trim().is_empty())
        .cloned()
        .ok_or_else(|| format!("missing --{name}"))
}
