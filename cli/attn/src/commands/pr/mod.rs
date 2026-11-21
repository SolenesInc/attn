mod help_generated;
mod table_generated;

use super::{Run, Stop, print_json, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use attn::go::tabwriter;
use attn_protocol::{PullRequestListResult, PullRequestResult, PullRequestWatchResult};
use std::ffi::OsString;
use std::fmt::Write as _;

const EXIT_USAGE: u8 = 2;
const EXIT_ERROR: u8 = 5;

struct Pr;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &Pr, args)
}

pub fn asks_for_help(args: &[OsString]) -> bool {
    matches!(args, [_, second] if second == "-h" || second == "--help")
}

impl Family for Pr {
    fn group(&self) -> &'static str {
        "pr"
    }

    fn flags(&self) -> &'static [&'static str] {
        table_generated::FLAGS
    }

    fn commands(&self) -> &'static [Command] {
        table_generated::COMMANDS
    }

    fn group_help(&self) -> &'static str {
        help_generated::GROUP_HELP
    }

    fn help(&self, verb: &str) -> &'static str {
        help_generated::help(verb)
    }

    fn usage(&self, verb: &str) -> &'static str {
        help_generated::usage(verb)
    }

    fn usage_error(&self, verb: &str, message: &str) -> Stop {
        eprintln!("{}: {message}", self.label(verb));
        Stop(EXIT_USAGE)
    }

    fn fail(&self, verb: &str, message: &str) -> Stop {
        eprintln!("{}: {message}", self.label(verb));
        Stop(EXIT_ERROR)
    }

    fn error_exit(&self) -> Stop {
        Stop(EXIT_ERROR)
    }

    fn present_json(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        _parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        if !matches!(cmd.name, "ls" | "status") {
            return command::print_result(run, self, cmd, &result);
        }
        let raw = result
            .raw_field(&["pull_requests"])
            .map_err(|error| command::send_failed(run, self, cmd, &error))?;
        print_json(raw.as_bytes());
        Ok(())
    }

    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        _parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        let out = match cmd.name {
            "watch" => watched(&command::decode(run, self, cmd, &result)?),
            "ls" | "status" => list(cmd.name, &command::decode(run, self, cmd, &result)?),
            verb => recorded(verb, &command::decode(run, self, cmd, &result)?),
        };
        print_stdout(&out);
        Ok(())
    }
}

fn watched(result: &PullRequestWatchResult) -> String {
    match &result.seed {
        Some(seed) => format!(
            "watching {} in {} mode for seed {seed}; updates reach the seed's tender\n",
            result.url, result.mode
        ),
        None => format!(
            "watching {} in {} mode for session {}; updates arrive in the agent inbox\n",
            result.url, result.mode, result.session_id
        ),
    }
}

fn recorded(verb: &str, result: &PullRequestResult) -> String {
    match (verb, &result.seed) {
        ("unwatch", Some(seed)) => format!("stopped watching {} for seed {seed}\n", result.url),
        ("unwatch", None) => format!(
            "stopped watching {} for session {}\n",
            result.url, result.session_id
        ),
        ("forget", _) => format!("forgot {} for session {}\n", result.url, result.session_id),
        _ => format!(
            "recorded {} for session {}\n",
            result.url, result.session_id
        ),
    }
}

fn list(verb: &str, result: &PullRequestListResult) -> String {
    if result.pull_requests.is_empty() {
        return match (&result.seed, verb) {
            (Some(seed), _) => format!("seed {seed} has no watched pull requests\n"),
            (None, "status") => format!(
                "session {} has no watched pull requests\n",
                result.session_id
            ),
            (None, _) => format!(
                "session {} has opened no pull requests\n",
                result.session_id
            ),
        };
    }
    let mut table = String::from("PULL REQUEST\tSTATE\tMODE\tREADINESS\tHEALTH\tURL\n");
    for entry in &result.pull_requests {
        let _ = writeln!(
            table,
            "{}#{}\t{}\t{}\t{}\t{}\t{}",
            entry.repository,
            entry.number,
            entry.state,
            or_dash(entry.watch_mode.as_deref()),
            or_dash(entry.readiness_state.as_deref()),
            or_dash(entry.watch_health.as_deref()),
            entry.url
        );
    }
    tabwriter::format(&table)
}

fn or_dash(value: Option<&str>) -> &str {
    match value {
        Some(text) if !text.trim().is_empty() => text,
        _ => "-",
    }
}
