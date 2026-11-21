use super::top_level_generated::TOP_LEVEL;
use super::{Run, Stop, USAGE_UNKNOWN_COMMAND, print_stdout};
use attn_protocol::go_values::prompts::SESSION_ATTN_MAP;
use std::fmt::Write as _;
use std::os::unix::process::CommandExt as _;
use std::process::Command;

pub fn is_command(name: &str) -> bool {
    TOP_LEVEL.iter().any(|command| command.name == name)
}

fn help_text() -> String {
    let mut out = format!("usage: attn <command>\n\n{SESSION_ATTN_MAP}\n\nCommands:\n");
    for command in TOP_LEVEL.iter().filter(|command| !command.hidden) {
        let _ = writeln!(out, "  {:<33} {}", command.usage, command.summary);
    }
    out
}

pub fn help() {
    print_stdout(&help_text());
}

fn attnd_version() -> String {
    let attnd = std::env::current_exe()
        .and_then(std::fs::canonicalize)
        .map(|path| path.with_file_name("attnd"));
    let Ok(attnd) = attnd else {
        return "unknown".to_string();
    };
    let output = Command::new(attnd)
        .arg0("attn")
        .arg("--build-info-json")
        .output();
    output
        .ok()
        .and_then(|output| serde_json::from_slice::<serde_json::Value>(&output.stdout).ok())
        .and_then(|info| info["version"].as_str().map(str::to_string))
        .unwrap_or_else(|| "unknown".to_string())
}

pub fn unknown(run: &mut Run<'_>, name: &str) -> Stop {
    run.path = "attn".to_string();
    run.usage_error = Some(USAGE_UNKNOWN_COMMAND);
    eprint!(
        "attn {}: unknown command {}\n\n{}",
        attnd_version(),
        attn::go::quote::quote(name.as_bytes()),
        help_text()
    );
    Stop(1)
}
