use super::{Run, Stop, print_stdout};
use crate::instance::validate_instance_name;
use attn_protocol::go_values::ROUTING_OVERRIDE_ENV;
use std::fmt::Write as _;

const HELP: &str = r#"attn instance-env — emit shell commands to set or clear ATTN_INSTANCE

Usage:
  eval "$(attn instance-env dev)"              # bash/zsh: export ATTN_INSTANCE=dev
  eval "$(attn instance-env --unset)"           # bash/zsh: unset ATTN_INSTANCE
  attn instance-env --fish dev | source         # fish: set -gx ATTN_INSTANCE dev
  attn instance-env --fish --unset | source     # fish: set -e ATTN_INSTANCE

Instance names must match [a-z0-9][a-z0-9-]{0,15}. "dev" is reserved for the
development sibling install (port 29849, data dir ~/.attn-dev). Selecting or
clearing an instance also clears inherited ATTN data-dir, socket, database, config,
websocket port, and plugin-directory overrides so the selected instance is
authoritative — one of those left behind makes every attn command refuse to run."#;

pub fn run(_run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut fish = false;
    let mut remaining = Vec::new();
    for arg in args {
        match arg.as_str() {
            "--fish" => fish = true,
            "-h" | "--help" => {
                eprintln!("{HELP}");
                return Ok(());
            }
            _ => remaining.push(arg),
        }
    }
    let Some(first) = remaining.first() else {
        eprintln!("{HELP}");
        return Err(Stop(1));
    };
    let name = first.trim();
    if matches!(name, "--unset" | "none" | "default") {
        print_stdout(&script("", fish));
        return Ok(());
    }
    if let Err(message) = validate_instance_name(name) {
        eprintln!("{message}");
        return Err(Stop(1));
    }
    print_stdout(&script(name, fish));
    Ok(())
}

fn script(instance: &str, fish: bool) -> String {
    let mut out = String::new();
    for name in ROUTING_OVERRIDE_ENV {
        if fish {
            let _ = writeln!(out, "set -e {name}");
        } else {
            let _ = writeln!(out, "unset {name}");
        }
    }
    let _ = match (fish, instance.is_empty()) {
        (true, true) => writeln!(out, "set -e ATTN_INSTANCE"),
        (true, false) => writeln!(out, "set -gx ATTN_INSTANCE {instance}"),
        (false, true) => writeln!(out, "unset ATTN_INSTANCE"),
        (false, false) => writeln!(out, "export ATTN_INSTANCE={instance}"),
    };
    out
}
