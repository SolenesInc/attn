mod help_generated;
mod table_generated;

use super::{Run, Stop, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use attn::go::time::Time;
use attn_protocol::ActivityStatusResult;
use std::fmt::Write as _;

struct Activity;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &Activity, args)
}

impl Family for Activity {
    fn group(&self) -> &'static str {
        "activity"
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

    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        _parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        if cmd.name == "clear" {
            let cleared: String = command::decode(run, self, cmd, &result)?;
            print_stdout(&format!("{cleared}\n"));
            return Ok(());
        }
        print_stdout(&status(&command::decode(run, self, cmd, &result)?));
        Ok(())
    }
}

fn status(status: &ActivityStatusResult) -> String {
    let mut out = String::new();
    let _ = writeln!(out, "presence: {}", status.presence_tier);
    if status.presence_tier == "away" {
        out.push_str("  nothing is generated while nobody can see it\n");
    }
    if !status.enabled {
        out.push_str("activity: off (Settings › Background agents › Session activity)\n");
        return out;
    }
    if let Some(reason) = trimmed(status.error.as_deref()) {
        let _ = writeln!(out, "activity: on, but not runnable: {reason}");
        return out;
    }
    out.push_str("activity: on\n");
    if status.sessions.is_empty() {
        out.push_str("no sessions generate activity lines\n");
        return out;
    }
    for session in &status.sessions {
        let line = trimmed(session.activity.as_deref()).unwrap_or("—");
        let _ = writeln!(
            out,
            "  {:<24} {line}{}",
            session.label,
            age_suffix(session.activity_at.as_deref())
        );
        if let Some(reason) = trimmed(session.error.as_deref()) {
            let _ = writeln!(out, "  {:<24}   last run failed: {reason}", "");
        }
    }
    out
}

fn trimmed(value: Option<&str>) -> Option<&str> {
    value.map(str::trim).filter(|value| !value.is_empty())
}

fn age_suffix(stamp: Option<&str>) -> String {
    let Some(at) = trimmed(stamp).and_then(|raw| Time::parse_rfc3339(raw).ok()) else {
        return String::new();
    };
    let minutes = (Time::now_utc().unix_nanos() - at.unix_nanos()) / 60_000_000_000;
    match minutes {
        ..1 => " (just now)".to_string(),
        1..60 => format!(" ({minutes}m ago)"),
        _ => format!(" ({}h ago)", minutes / 60),
    }
}
