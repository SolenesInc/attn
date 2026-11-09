use super::{Run, Stop, USAGE_UNKNOWN_COMMAND, print_stdout};
use attn_protocol::go_values::prompts::SESSION_ATTN_MAP;
use std::fmt::Write as _;
use std::os::unix::process::CommandExt as _;
use std::process::Command;

struct Listed {
    name: &'static str,
    args: &'static str,
    summary: &'static str,
}

const LISTED: &[Listed] = &[
    Listed {
        name: "presence",
        args: "[--session-id|--seed-ids]",
        summary: "check whether the shell runs inside attn, or print only its session or seeds",
    },
    Listed {
        name: "ask",
        args: "[list|close]",
        summary: "ask the user something they answer from attn",
    },
    Listed {
        name: "turn",
        args: "open|settle",
        summary: "put yourself in the user's queue, or take yourself out",
    },
    Listed {
        name: "vent",
        args: "[withdraw]",
        summary: "report what got in your way",
    },
    Listed {
        name: "handoff",
        args: "-m \"<letter>\"",
        summary: "file this crew member's letter; the day turns over",
    },
    Listed {
        name: "onboarding",
        args: "[done|again]",
        summary: "show, finish or restart the Chief's onboarding",
    },
    Listed {
        name: "skill",
        args: "[--reference <name>|--list]",
        summary: "print the map above, or a capability's rules",
    },
    Listed {
        name: "guide",
        args: "[<topic>]",
        summary: "read the user guide",
    },
    Listed {
        name: "send",
        args: "<command>",
        summary: "send files and a message to a session as the user; pair, senders, unpair",
    },
    Listed {
        name: "list",
        args: "",
        summary: "list sessions and profiles",
    },
    Listed {
        name: "activity",
        args: "[clear <id>]",
        summary: "what each agent is doing right now",
    },
    Listed {
        name: "state",
        args: "explain <id>",
        summary: "replay why a session's state is what it is",
    },
    Listed {
        name: "conversation",
        args: "<command>",
        summary: "list, keep forever or forget attn's conversation copies",
    },
    Listed {
        name: "worktree",
        args: "<command>",
        summary: "every tracked worktree, the sweep and the keep pin",
    },
    Listed {
        name: "preflight",
        args: "",
        summary: "diagnose tools, paths, routing, and launch settings",
    },
    Listed {
        name: "codex",
        args: "status|update|restart",
        summary: "shared Codex versions, its update and restart",
    },
    Listed {
        name: "update",
        args: "status [--json]",
        summary: "how attn was installed and whether an update is available",
    },
    Listed {
        name: "plugin",
        args: "<command>",
        summary: "install, link, list or remove plugins",
    },
    Listed {
        name: "debug",
        args: "<command>",
        summary: "probe debug artifacts (incidents, logs)",
    },
    Listed {
        name: "db",
        args: "<command>",
        summary: "database maintenance (restore from backup)",
    },
    Listed {
        name: "bus",
        args: "<command>",
        summary: "event bus: consumer cursors, lag, kill switch",
    },
    Listed {
        name: "enrollment",
        args: "<command>",
        summary: "this data folder's enrollment: status, leave",
    },
    Listed {
        name: "doc",
        args: "<command>",
        summary: "document store: collections, documents, live queries",
    },
    Listed {
        name: "vision-check",
        args: "<image> <question>",
        summary: "answer a question about an image (single LLM call)",
    },
    Listed {
        name: "daemon",
        args: "[ensure|stop]",
        summary: "run the daemon, ensure it is running, or stop it",
    },
    Listed {
        name: "instance",
        args: "<status|resolve|list>",
        summary: "show / resolve the active instance's resources",
    },
    Listed {
        name: "instance-env",
        args: "<instance|--unset>",
        summary: "print shell commands for selecting an instance",
    },
    Listed {
        name: "prompts",
        args: "<command>",
        summary: "inspect prompt sources and scenario composition",
    },
    Listed {
        name: "version",
        args: "[--json]",
        summary: "print version, channel and commit",
    },
];

const UNLISTED_ATTND_COMMANDS: &[&str] = &[
    "delegate",
    "pr",
    "agent",
    "session",
    "profile",
    "profile-env",
    "pty-worker",
    "_outpost-install",
    "_bridge",
    "_probe-tui",
];

pub fn attnd_owns(name: &str) -> bool {
    name.starts_with('-')
        || UNLISTED_ATTND_COMMANDS.contains(&name)
        || LISTED.iter().any(|command| command.name == name)
}

fn help_text() -> String {
    let mut out = format!("usage: attn <command>\n\n{SESSION_ATTN_MAP}\n\nOther commands:\n");
    for command in LISTED {
        let usage = format!("{} {}", command.name, command.args);
        let _ = writeln!(out, "  {:<33} {}", usage.trim(), command.summary);
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
