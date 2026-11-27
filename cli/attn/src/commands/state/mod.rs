mod help_generated;
mod table_generated;

use super::{Run, Stop, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use attn::go::quote::quote;
use attn::go::time::Time;
use attn_protocol::session_id::short;
use attn_protocol::{StateExplainEntry, StateExplainResult};
use std::fmt::Write as _;

struct State;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &State, args)
}

impl Family for State {
    fn group(&self) -> &'static str {
        "state"
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
        print_stdout(&explain(&command::decode(run, self, cmd, &result)?));
        Ok(())
    }
}

fn explain(result: &StateExplainResult) -> String {
    let mut out = String::new();
    let _ = writeln!(
        out,
        "session {} ({})",
        short(&result.session_id),
        result.agent
    );
    let _ = write!(out, "state:   {}", result.state);
    if let Some(since) = present(result.state_since.as_deref()) {
        let _ = write!(out, " since {}", clock(since));
    }
    out.push('\n');
    if let Some(request) = present(result.last_model_request_at.as_deref()) {
        let _ = writeln!(out, "model request: {}", clock(request));
    }
    if let Some(verdict) = &result.verdict {
        let label = present(verdict.label.as_deref())
            .map_or_else(String::new, |label| format!(" ({label})"));
        let _ = writeln!(
            out,
            "latest verdict: {}{label} by {} at {}: {}",
            verdict.verdict,
            verdict.decider,
            clock(&verdict.at),
            verdict.reason
        );
    }
    if let Some(facts) = &result.facts {
        let facts = serde_json::to_string_pretty(facts).unwrap_or_default();
        let _ = writeln!(out, "\nfacts the attention supervisor reads:\n{facts}");
    }
    if result.observations.is_empty() {
        out.push_str("\nNo state observations recorded. The daemon has seen no state evidence\n");
        out.push_str("for this session since it started — a state shown now predates the trace.\n");
        return out;
    }
    if result.capacity > 0
        && i64::try_from(result.observations.len()).unwrap_or(i64::MAX) >= result.capacity
    {
        let _ = writeln!(
            out,
            "\n(showing the most recent {} observations; older ones were evicted)",
            result.capacity
        );
    }
    let _ = writeln!(
        out,
        "\n{:<24}  {:<18}  {:<16}  {:<14}  WHY",
        "OBSERVED", "SOURCE", "CLAIM", "OUTCOME"
    );
    for obs in &result.observations {
        let _ = writeln!(
            out,
            "{:<24}  {:<18}  {:<16}  {:<14}  {}",
            clock(&obs.observed_at),
            placeholder(&obs.source),
            placeholder(&obs.claim),
            outcome(obs),
            why(obs)
        );
    }
    out
}

fn present(value: Option<&str>) -> Option<&str> {
    value.filter(|value| !value.trim().is_empty())
}

fn outcome(obs: &StateExplainEntry) -> String {
    match obs.repeats {
        Some(repeats) if repeats > 0 => format!("{} ×{}", obs.outcome, repeats + 1),
        _ => obs.outcome.clone(),
    }
}

fn why(obs: &StateExplainEntry) -> String {
    let mut parts = Vec::new();
    if let Some(reason) = present(obs.reason.as_deref()) {
        parts.push(reason.trim().to_string());
    }
    if let Some(detail) = present(obs.detail.as_deref()) {
        parts.push(quote(detail.trim().as_bytes()));
    }
    if let Some(cause) = present(obs.cause.as_deref()) {
        parts.push(format!("cause={}", cause.trim()));
    }
    parts.join("  ")
}

fn placeholder(value: &str) -> &str {
    if value.trim().is_empty() { "-" } else { value }
}

fn clock(value: &str) -> String {
    Time::parse_rfc3339_nano(value.trim()).map_or_else(
        |_| placeholder(value).to_string(),
        |time| time.format_local("15:04:05.000"),
    )
}
