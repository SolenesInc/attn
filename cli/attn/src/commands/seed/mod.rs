mod checks;
mod help_generated;
mod present;
mod render;
mod table_generated;

use super::{Run, Stop, print_stdout};
use crate::command::{Command, Family, flags_of};
use attn::go::flag::{FlagError, FlagSet};
use attn::go::quote::quote;
use attn_protocol::go_values::CHECK_VERBS;
use table_generated::COMMANDS;

const REVIEW_VERBS: &str = "start, show, cancel, retry, or keep";

const GROUP: Command = Command {
    name: "",
    result: false,
    usage: &[],
    args: &[],
};

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let Some(verb) = args.first() else {
        print_stdout(help_generated::GROUP_HELP);
        return Ok(());
    };
    if verb == "-h" || verb == "--help" {
        print_stdout(help_generated::GROUP_HELP);
        return Ok(());
    }
    let rest = &args[1..];
    let known = COMMANDS
        .iter()
        .any(|cmd| cmd.name.split(' ').next() == Some(verb.as_str()));
    if known && verb != "authorization" {
        run.path = format!("seed {verb}");
    }
    let name = match verb.as_str() {
        "check" => check_move(run, rest)?,
        "review" => review_move(run, rest)?,
        _ => verb.clone(),
    };
    let Some(cmd) = COMMANDS.iter().find(|cmd| cmd.name == name) else {
        run.path = "seed".to_string();
        run.usage_error = Some(super::USAGE_UNKNOWN_COMMAND);
        eprintln!("seed: unknown command {}", quote(verb.as_bytes()));
        eprintln!("usage: attn seed <command> (attn seed --help)");
        return Err(Stop(2));
    };
    if name.contains(' ') {
        run.path = format!("seed {name}");
    }
    let words = if name.contains(' ') { &rest[1..] } else { rest };
    crate::command::run_command(run, &present::Seed, cmd, words)
}

fn group_flags(run: &mut Run<'_>, group: &str, args: &[String]) -> Result<(), Stop> {
    let mut fs = FlagSet::new("", &flags_of(table_generated::FLAGS, &GROUP));
    match fs.parse(args) {
        Ok(()) => Ok(()),
        Err(FlagError::Help) => {
            print_stdout(help_generated::help(group));
            Err(Stop(0))
        }
        Err(FlagError::Invalid(message)) => {
            run.record_flag_error(&message);
            Err(usage_error(group, &message))
        }
    }
}

fn check_move(run: &mut Run<'_>, args: &[String]) -> Result<String, Stop> {
    let first = args.first();
    if first.is_none_or(|arg| arg.starts_with('-')) {
        group_flags(run, "check", args)?;
        return Err(usage_error(
            "check",
            &format!("needs a move: {}", CHECK_VERBS.join(", ")),
        ));
    }
    let raw = first.expect("checked above");
    let verb = raw.trim().to_lowercase();
    if !CHECK_VERBS.contains(&verb.as_str()) {
        return Err(usage_error(
            "check",
            &format!(
                "{} is not something a check does; the moves are {}",
                quote(raw.as_bytes()),
                CHECK_VERBS.join(", ")
            ),
        ));
    }
    Ok(format!("check {verb}"))
}

fn review_move(run: &mut Run<'_>, args: &[String]) -> Result<String, Stop> {
    let Some(verb) = args.first().filter(|first| !first.starts_with('-')) else {
        group_flags(run, "review", args)?;
        return Err(usage_error("review", &format!("needs {REVIEW_VERBS}")));
    };
    if !["start", "show", "cancel", "retry", "keep"].contains(&verb.as_str()) {
        run.usage_error = Some(super::USAGE_UNKNOWN_COMMAND);
        return Err(usage_error(
            "review",
            &format!(
                "unknown command {}; use {REVIEW_VERBS}",
                quote(verb.as_bytes())
            ),
        ));
    }
    Ok(format!("review {verb}"))
}

fn usage_error(name: &str, message: &str) -> Stop {
    Family::usage_error(&present::Seed, name, message)
}

fn fail(name: &str, message: &str) -> Stop {
    Family::fail(&present::Seed, name, message)
}
