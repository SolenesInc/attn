mod blocks;
mod boundary;
mod ghostty;
mod host;
mod png_decoder;
mod protocol;
mod queries;
mod segmenter;
mod session;
mod signals;
mod wire;

use std::collections::HashMap;

use host::{Config, Host};

fn main() {
    if let Err(error) = run() {
        eprintln!("attn-pty-host: {error}");
        std::process::exit(1);
    }
}

fn run() -> Result<(), String> {
    let args = parse_args(std::env::args().skip(1))?;
    Host::run(Config {
        daemon_instance_id: required(&args, "daemon-instance-id")?,
        generation: required(&args, "generation")?,
        socket_path: required(&args, "socket-path")?,
        registry_dir: required(&args, "registry-dir")?,
        host_registry_path: required(&args, "host-registry-path")?,
        control_token: required(&args, "control-token")?,
    })
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
