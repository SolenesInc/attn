mod help_generated;
mod table_generated;

use super::{Run, Stop, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use attn::go::time::Time;
use attn_protocol::{
    AgentCloseResult, AgentInboxBatchResult, AgentListResult, AgentMsgResult, AgentPeekResult,
    AgentPeerMessage, AgentReloadResult,
};
use serde_json::Value as Json;
use std::fmt::Write as _;

const SHORT_ID: usize = 8;

struct Agent;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &Agent, args)
}

impl Family for Agent {
    fn group(&self) -> &'static str {
        "agent"
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

    fn before_send(&self, cmd: &Command, parsed: &mut Parsed) -> Result<(), Stop> {
        match cmd.name {
            "close" => command::outlive_close_of_own_session(),
            "inbox" if !parsed.json => {
                parsed.args.insert("render".to_string(), Json::from(true));
            }
            _ => {}
        }
        Ok(())
    }

    fn present_json(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        _parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        match cmd.name {
            "list" => print_field(run, cmd, &result, "agents")?,
            "inbox" => {
                let inbox: AgentInboxBatchResult = command::decode(run, self, cmd, &result)?;
                match (&inbox.item, &inbox.message) {
                    (Some(_), _) => print_field(run, cmd, &result, "item")?,
                    (_, Some(_)) => print_field(run, cmd, &result, "message")?,
                    _ => command::print_result(run, self, cmd, &result)?,
                }
            }
            _ => command::print_result(run, self, cmd, &result)?,
        }
        self.settle(run, cmd, &result)
    }

    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        _parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        match cmd.name {
            "list" => print_stdout(&list(&command::decode::<AgentListResult>(
                run, self, cmd, &result,
            )?)),
            "inbox" => {
                let inbox: AgentInboxBatchResult = command::decode(run, self, cmd, &result)?;
                print_stdout(&format!("{}\n", inbox.text.unwrap_or_default()));
            }
            "msg" => {
                let sent: AgentMsgResult = command::decode(run, self, cmd, &result)?;
                print_stdout(&format!("{}\n", msg_line(&sent)));
            }
            "close" => {
                let closed: AgentCloseResult = command::decode(run, self, cmd, &result)?;
                print_stdout(&close(&closed));
            }
            "reload" => {
                let reloaded: AgentReloadResult = command::decode(run, self, cmd, &result)?;
                for outcome in &reloaded.outcomes {
                    if outcome.success {
                        print_stdout(&format!("reloaded session {}\n", outcome.target));
                    } else {
                        eprintln!(
                            "agent reload {}: {}",
                            outcome.target,
                            outcome.error.as_deref().unwrap_or("")
                        );
                    }
                }
            }
            "attachment" => {
                let saved: String = command::decode(run, self, cmd, &result)?;
                print_stdout(&format!("{saved}\n"));
            }
            "peek" => print_stdout(&peek(&command::decode(run, self, cmd, &result)?)),
            _ => {
                let message: AgentPeerMessage = command::decode(run, self, cmd, &result)?;
                print_stdout(&msg_status_line(&message));
            }
        }
        self.settle(run, cmd, &result)
    }
}

impl Agent {
    fn settle(&self, run: &mut Run<'_>, cmd: &Command, result: &Reply) -> Result<(), Stop> {
        match cmd.name {
            "msg" => {
                let sent: AgentMsgResult = command::decode(run, self, cmd, result)?;
                if sent.status == "refused" {
                    return Err(Stop(1));
                }
            }
            "close" => {
                let closed: AgentCloseResult = command::decode(run, self, cmd, result)?;
                if closed.rule == "self" && closed.pending_compact != Some(true) {
                    run.closed_session = Some(closed.target_session_id.clone());
                }
            }
            "reload" => {
                let reloaded: AgentReloadResult = command::decode(run, self, cmd, result)?;
                if reloaded.outcomes.iter().any(|outcome| !outcome.success) {
                    return Err(Stop(1));
                }
            }
            _ => {}
        }
        Ok(())
    }
}

fn print_field(run: &mut Run<'_>, cmd: &Command, result: &Reply, field: &str) -> Result<(), Stop> {
    let raw = result
        .raw_field(&[field])
        .map_err(|error| run.fail_send(&Agent.label(cmd.name), &error))?;
    crate::commands::print_json(raw.as_bytes());
    Ok(())
}

fn short(id: &str) -> String {
    id.chars().take(SHORT_ID).collect()
}

fn cell(value: &str, width: usize) -> String {
    let value = value.trim();
    if value.is_empty() {
        return "-".to_string();
    }
    if value.chars().count() <= width {
        return value.to_string();
    }
    let mut cut: String = value.chars().take(width - 1).collect();
    cut.push('…');
    cut
}

fn list(result: &AgentListResult) -> String {
    if result.agents.is_empty() {
        return "No sessions on this daemon.\n".to_string();
    }
    let mut out = format!(
        "{:<SHORT_ID$}  {:<18}  {:<8}  {:<10}  {:<20}  {:<16}  TURN\n",
        "ID", "NAME", "AGENT", "MEMBER", "PROFILE", "STATE"
    );
    for row in &result.agents {
        let _ = writeln!(
            out,
            "{:<SHORT_ID$}  {:<18}  {:<8}  {:<10}  {:<20}  {:<16}  {}",
            short(&row.id),
            cell(&row.label, 18),
            cell(&row.agent, 8),
            cell(&row.member, 10),
            cell(&row.profile, 20),
            cell(&row.state, 16),
            if row.turn_owed { "owed" } else { "-" }
        );
    }
    out.push_str("\nAn ID, or the MEMBER of a member in session, here works with `attn agent peek <target>`; --json carries full ids.\n");
    out
}

fn clock(value: &str) -> String {
    let value = value.trim();
    let Ok(at) = Time::parse_rfc3339_nano(value) else {
        return value.to_string();
    };
    if at.format_local("2006-01-02") == Time::now_utc().format_local("2006-01-02") {
        at.format_local("15:04:05")
    } else {
        at.format_local("2006-01-02 15:04:05")
    }
}

fn trimmed(value: Option<&str>) -> Option<&str> {
    value.map(str::trim).filter(|value| !value.is_empty())
}

fn peek(result: &AgentPeekResult) -> String {
    let mut out = format!(
        "session {} ({}) — {}\n",
        result.session_id, result.agent, result.label
    );
    if let Some(member) = trimmed(result.crew_member_name.as_deref()) {
        let _ = writeln!(out, "crew member: this session is {member} today");
    }
    if let Some(profile) = trimmed(result.profile_name.as_deref()) {
        let _ = writeln!(out, "profile: {profile}");
    }
    let _ = write!(out, "state: {}", result.state);
    if let Some(reason) = trimmed(result.state_reason.as_deref()) {
        let _ = write!(out, " ({reason})");
    }
    let since = clock(&result.state_since);
    if !since.is_empty() {
        let _ = write!(out, " since {since}");
    }
    out.push('\n');
    if result.turn_owed == Some(true) {
        out.push_str("turn: owed to this session\n");
    }
    if let Some(message) = trimmed(result.last_assistant_message.as_deref()) {
        out.push_str("\nlast assistant message:\n");
        for line in message.split('\n') {
            let _ = writeln!(out, "  {line}");
        }
    }
    let mut title = "screen";
    if let Some(exit) = &result.exit {
        let _ = write!(out, "\nprocess exited with code {}", exit.code);
        if let Some(signal) = trimmed(exit.signal.as_deref()) {
            let _ = write!(out, " ({signal})");
        }
        let at = clock(&exit.at);
        if !at.is_empty() {
            let _ = write!(out, " at {at}");
        }
        out.push('\n');
        title = "screen at exit";
    }
    match &result.screen {
        Some(screen) => {
            let _ = writeln!(out, "\n{title} ({}x{}):", screen.cols, screen.rows);
            for line in screen.text.trim_end_matches('\n').split('\n') {
                let _ = writeln!(out, "  {line}");
            }
            if screen.composer_suggestion == Some(true) {
                out.push_str("\ncomposer: the text on the ❯ line is Claude's suggestion, not typed by the user\n");
            }
        }
        None => out.push_str("\nscreen unavailable\n"),
    }
    out
}

fn msg_status_line(message: &AgentPeerMessage) -> String {
    let mut line = format!(
        "{}: message {} to session {}",
        message.state,
        message.message_id,
        short(&message.target_session_id)
    );
    let with_article = |turn: &str| {
        let article = if turn == "interrupt" { "an" } else { "a" };
        format!("{article} {turn}")
    };
    match (&message.delivered_as, &message.turn) {
        (Some(turn), _) => {
            let _ = write!(line, "; its running turn took it as {}", with_article(turn));
        }
        (None, Some(turn)) if message.state == "read" => {
            let _ = write!(
                line,
                "; it arrived as its next prompt, not as {}",
                with_article(turn)
            );
        }
        _ => {}
    }
    if message.as_user == Some(true) {
        line.push_str("; sent as the user");
    }
    line.push('\n');
    line
}

fn msg_line(result: &AgentMsgResult) -> String {
    if result.message_id.is_empty() {
        format!("{}: {}", result.status, result.detail)
    } else {
        format!(
            "{}: {} (id {})",
            result.status, result.detail, result.message_id
        )
    }
}

fn close(result: &AgentCloseResult) -> String {
    let id = short(&result.target_session_id);
    if result.pending_compact == Some(true) {
        return format!(
            "session {id} ({}) closes after this turn, once its conversation is compacted: {}\nend your turn now\n",
            result.label, result.reason
        );
    }
    let mut out = format!(
        "closed session {id} ({}): {}\n",
        result.label, result.reason
    );
    for seed in &result.seed_ids {
        let _ = writeln!(out, "noted on {seed}, which it was tending");
    }
    let _ = writeln!(
        out,
        "the session is kept: `attn session show {id}` reads it back"
    );
    out
}
