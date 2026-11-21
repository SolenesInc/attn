mod help_generated;
mod table_generated;

use super::{Run, Stop, print_stdout};
use crate::client::Reply;
use crate::command::{self, Answer, Command, Family, Parsed};
use attn::go::quote::quote;
use attn::go::tabwriter;
use attn::go::time::Time;
use attn_protocol::{
    SessionInstructionsResult, SessionLedgerEntry, SessionListResult, SessionReopen,
    SessionReopenResult, SessionShowResult, SessionSnoozeResult, SessionTranscriptResult,
    SessionTreeProfile, SessionUsage,
};
use serde_json::Value as Json;
use std::fmt::Write as _;
use std::io::Write as _;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Duration;

struct Session;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &Session, args)
}

impl Family for Session {
    fn group(&self) -> &'static str {
        "session"
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

    fn exchange(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        parsed: &Parsed,
    ) -> Option<Result<(), Stop>> {
        match cmd.name {
            "transcript" => Some(transcript(run, cmd, parsed)),
            "instructions" => Some(instructions(run, cmd, parsed)),
            _ => None,
        }
    }

    fn present_json(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        _parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        command::print_result(run, self, cmd, &result)?;
        if matches!(cmd.name, "snooze" | "unsnooze") {
            let outcomes: SessionSnoozeResult = command::decode(run, self, cmd, &result)?;
            if outcomes
                .outcomes
                .iter()
                .any(|outcome| outcome.error.is_some())
            {
                return Err(Stop(1));
            }
        }
        Ok(())
    }

    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        let out = match cmd.name {
            "list" => list(parsed, &command::decode(run, self, cmd, &result)?),
            "show" => show(&command::decode(run, self, cmd, &result)?),
            "reopen" => reopened(&command::decode(run, self, cmd, &result)?),
            "snooze" | "unsnooze" => {
                return snoozed(cmd, &command::decode(run, self, cmd, &result)?);
            }
            _ => format!("{}\n", command::decode::<String>(run, self, cmd, &result)?),
        };
        print_stdout(&out);
        Ok(())
    }
}

static INTERRUPTED: AtomicBool = AtomicBool::new(false);

extern "C" fn interrupted(_: libc::c_int) {
    INTERRUPTED.store(true, Ordering::SeqCst);
}

const FOLLOW_WAITS_ON: [&str; 3] = ["transcript_unavailable", "home_unreachable", "home_lost"];

fn wait_for_poll() -> bool {
    for _ in 0..10 {
        if INTERRUPTED.load(Ordering::SeqCst) {
            return false;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    !INTERRUPTED.load(Ordering::SeqCst)
}

fn stop_following_on_interrupt() {
    unsafe {
        libc::signal(
            libc::SIGINT,
            interrupted as extern "C" fn(libc::c_int) as libc::sighandler_t,
        );
        libc::signal(
            libc::SIGTERM,
            interrupted as extern "C" fn(libc::c_int) as libc::sighandler_t,
        );
    }
}

fn transcript(run: &mut Run<'_>, cmd: &Command, parsed: &Parsed) -> Result<(), Stop> {
    let follow = parsed.on("follow");
    if follow {
        stop_following_on_interrupt();
    }
    let mut ask = Parsed {
        args: parsed.args.clone(),
        json: parsed.json,
        profile: parsed.profile.clone(),
        session: parsed.session.clone(),
    };
    let mut cursor = parsed.text("after").trim().to_string();
    let done = |cursor: &str| {
        if !cursor.is_empty() {
            eprintln!("cursor: {cursor}");
        }
        Ok(())
    };
    loop {
        if cursor.is_empty() {
            ask.args.remove("after");
        } else {
            ask.args
                .insert("after".to_string(), Json::from(cursor.clone()));
        }
        let answer = match command::send(run, &Session, cmd, &ask) {
            Ok(reply) => command::answer(run, reply),
            Err(_) if follow => {
                if !wait_for_poll() {
                    return done(&cursor);
                }
                continue;
            }
            Err(error) => {
                if !cursor.is_empty() {
                    eprintln!("cursor: {cursor}");
                }
                return Err(run.fail_send(&Session.label(cmd.name), &error));
            }
        };
        match answer {
            Answer::Refused(_, refusal)
                if follow && FOLLOW_WAITS_ON.contains(&refusal.code.as_str()) =>
            {
                if !wait_for_poll() {
                    return done(&cursor);
                }
            }
            Answer::Refused(_, refusal) if refusal.code == command::CODE_USAGE => {
                return Err(
                    Session.usage_error(cmd.name, &command::render_mentions(cmd, &refusal.message))
                );
            }
            Answer::Refused(_, refusal) => {
                if !cursor.is_empty() {
                    eprintln!("cursor: {cursor}");
                }
                eprintln!("{}", refusal.message);
                return Err(Stop(1));
            }
            Answer::Result(reply) => {
                let result = command::result(&reply);
                let page: SessionTranscriptResult = command::decode(run, &Session, cmd, &result)?;
                let mut out = String::new();
                if parsed.json {
                    let raw: Json = command::decode(run, &Session, cmd, &result)?;
                    for event in raw["events"].as_array().into_iter().flatten() {
                        let line = attn::go::json::encode(event).unwrap_or_default();
                        out.push_str(&String::from_utf8_lossy(&line));
                        out.push('\n');
                    }
                } else {
                    for event in &page.events {
                        transcript_event(&mut out, event);
                    }
                }
                let written = std::io::stdout()
                    .lock()
                    .write_all(out.as_bytes())
                    .and_then(|()| std::io::stdout().lock().flush());
                if written.is_err() {
                    return done(&cursor);
                }
                cursor = page.next_cursor;
                if !page.at_end {
                    continue;
                }
                if !follow || !wait_for_poll() {
                    return done(&cursor);
                }
            }
        }
    }
}

fn transcript_event(out: &mut String, event: &attn_protocol::SessionTranscriptEvent) {
    let timestamp = trimmed(event.timestamp.as_deref()).unwrap_or("-");
    let mut label = event.kind.to_uppercase();
    if event.is_error == Some(true) && event.kind != "error" {
        label.push_str(" ERROR");
    }
    let _ = write!(out, "{timestamp}  {label:<12}");
    if let Some(tool) = trimmed(event.tool_name.as_deref()) {
        let _ = write!(out, " {tool}");
    }
    if let Some(call) = trimmed(event.tool_call_id.as_deref()) {
        let _ = write!(out, " ({call})");
    }
    out.push('\n');
    if let Some(text) = trimmed(event.text.as_deref()) {
        for line in text.split('\n') {
            let _ = writeln!(out, "  {line}");
        }
    }
}

fn instructions(run: &mut Run<'_>, cmd: &Command, parsed: &Parsed) -> Result<(), Stop> {
    match command::ask(run, &Session, cmd, parsed)? {
        Answer::Refused(_, refusal) if refusal.code == command::CODE_USAGE => {
            Err(Session.usage_error(cmd.name, &command::render_mentions(cmd, &refusal.message)))
        }
        Answer::Refused(_, refusal) => {
            if parsed.json {
                let error = serde_json::json!({"error": {"code": refusal.code, "message": refusal.message}});
                let line = attn::go::json::encode(&error).unwrap_or_default();
                print_stdout(&format!("{}\n", String::from_utf8_lossy(&line)));
            } else {
                eprintln!("{}", refusal.message);
            }
            Err(Stop(1))
        }
        Answer::Result(reply) => {
            let result = command::result(&reply);
            if parsed.json {
                return command::print_result(run, &Session, cmd, &result);
            }
            let answer: SessionInstructionsResult = command::decode(run, &Session, cmd, &result)?;
            let mut out = format!("{}\n", answer.answer);
            for evidence in &answer.evidence {
                let _ = write!(out, "\n{}", evidence.author);
                if let Some(at) = trimmed(evidence.timestamp.as_deref()) {
                    let _ = write!(out, " — {}", evidence.timestamp.as_deref().unwrap_or(at));
                }
                let _ = write!(out, "\n  {}\n", quote(evidence.quote.as_bytes()));
            }
            let _ = writeln!(out, "\nTranscript: {}", answer.transcript_path);
            print_stdout(&out);
            Ok(())
        }
    }
}

fn snoozed(cmd: &Command, result: &SessionSnoozeResult) -> Result<(), Stop> {
    let mut out = String::new();
    let mut failed = false;
    for outcome in &result.outcomes {
        match (&outcome.error, &outcome.until) {
            (Some(error), _) => {
                print_stdout(&out);
                out.clear();
                eprintln!("session {} {}: {error}", cmd.name, outcome.session_id);
                failed = true;
            }
            (None, Some(until)) => {
                let _ = writeln!(out, "{} snoozed until {until}", outcome.session_id);
            }
            (None, None) => {
                let _ = writeln!(out, "{} unsnoozed", outcome.session_id);
            }
        }
    }
    print_stdout(&out);
    if failed { Err(Stop(1)) } else { Ok(()) }
}

fn trimmed(value: Option<&str>) -> Option<&str> {
    value.map(str::trim).filter(|value| !value.is_empty())
}

fn or_dash(value: &str) -> &str {
    if value.trim().is_empty() { "-" } else { value }
}

fn short_stamp(stamp: &str) -> String {
    Time::parse_rfc3339(stamp).map_or_else(
        |_| stamp.to_string(),
        |at| at.format_local("2006-01-02 15:04"),
    )
}

fn ledger_state(entry: &SessionLedgerEntry) -> String {
    if entry.closed_at.as_deref().is_some_and(|at| !at.is_empty()) {
        return "closed".to_string();
    }
    if entry.hidden == Some(true) {
        return format!("{} (hidden)", entry.state);
    }
    entry.state.clone()
}

fn filtered(parsed: &Parsed) -> bool {
    ["profile", "repository", "last", "since", "until"]
        .iter()
        .any(|name| !parsed.text(name).trim().is_empty())
}

fn empty_list(parsed: &Parsed) -> &'static str {
    if !parsed.text("before").trim().is_empty() {
        "no sessions past that page"
    } else if filtered(parsed) {
        "no sessions match those filters — drop one to widen the search"
    } else if parsed.on("closed") {
        "no closed sessions yet — closing one records it here"
    } else if parsed.on("all") {
        "no sessions in the ledger yet"
    } else {
        "no live sessions — `attn session list --closed` reads the ones that ended"
    }
}

fn list(parsed: &Parsed, result: &SessionListResult) -> String {
    if parsed.on("tree") {
        return tree(parsed, result.tree.as_deref().unwrap_or_default());
    }
    if result.entries.is_empty() {
        return format!("{}\n", empty_list(parsed));
    }
    let reopen = parsed.on("reopen");
    let verdicts = result.reopen.as_deref().unwrap_or_default();
    let mut table = String::from("ID\tAGENT\tSTATE\tSNOOZED UNTIL\tWHEN\tCLOSED BY\t");
    if reopen {
        table.push_str("REOPEN\t");
    }
    table.push_str("LABEL\n");
    for entry in &result.entries {
        let when = entry
            .closed_at
            .as_deref()
            .filter(|at| !at.is_empty())
            .unwrap_or(&entry.last_seen);
        let _ = write!(
            table,
            "{}\t{}\t{}\t{}\t{}\t{}\t",
            entry.id,
            entry.agent,
            ledger_state(entry),
            or_dash(entry.turn_snoozed_until.as_deref().unwrap_or("")),
            short_stamp(when),
            or_dash(entry.closed_by.as_deref().unwrap_or(""))
        );
        if reopen {
            let column = match verdicts.iter().find(|v| v.session_id == entry.id) {
                None => "-",
                Some(v) => v.reopen.actions.first().map_or("no", String::as_str),
            };
            let _ = write!(table, "{column}\t");
        }
        let mut label = entry
            .member_name
            .clone()
            .filter(|name| !name.is_empty())
            .unwrap_or_else(|| entry.label.clone());
        if entry.priority == Some(true) {
            label = format!("⚑ {label}");
        }
        let _ = writeln!(table, "{label}");
    }
    let mut out = tabwriter::format(&table);
    for entry in &result.entries {
        if let Some(reason) = trimmed(entry.close_reason.as_deref()) {
            let _ = write!(out, "\n{} closed because: {reason}\n", entry.id);
        }
    }
    if result.omitted > 0 {
        let _ = write!(
            out,
            "\nshowing {}, {} omitted, paginate with --before {}\n",
            result.entries.len(),
            result.omitted,
            result.next_before.as_deref().unwrap_or("")
        );
    }
    out
}

fn tree(parsed: &Parsed, profiles: &[SessionTreeProfile]) -> String {
    if profiles.is_empty() {
        return format!("{}\n", empty_list(parsed));
    }
    let mut out = String::new();
    for (index, profile) in profiles.iter().enumerate() {
        if index > 0 {
            out.push('\n');
        }
        let _ = writeln!(out, "{} ({})", profile.profile_name, profile.profile_id);
        let mut table = String::from("SESSION\tAGENT\tSTATE\tTURN\tSEED\n");
        for row in &profile.rows {
            let short: String = row.session_id.chars().take(8).collect();
            let _ = writeln!(
                table,
                "{}{short}  {}\t{}\t{}\t{}\t{}",
                row.prefix,
                row.label,
                row.agent,
                row.state,
                row.turn,
                or_dash(row.seed.as_deref().unwrap_or(""))
            );
        }
        out.push_str(&tabwriter::format(&table));
    }
    out
}

fn usage_text(usage: &SessionUsage) -> String {
    let mut text = format!("{} tokens", usage.total_tokens);
    match usage.cost_usd {
        Some(cost) if cost > 0.0 && cost < 0.01 => text.push_str(", <$0.01"),
        Some(cost) => {
            let _ = write!(text, ", ${cost:.2}");
        }
        None => {}
    }
    if usage.has_unpriced_usage {
        text.push_str(", some usage has no price");
    }
    if usage.measurement_incomplete == Some(true) {
        text.push_str(", measurement incomplete");
    }
    text
}

fn ledger_profile(entry: &SessionLedgerEntry) -> String {
    if entry.profile_id.is_empty() {
        return "-".to_string();
    }
    let mut shown = entry.profile_id.clone();
    if !entry.profile_name.is_empty() {
        shown = format!("{} ({})", entry.profile_name, entry.profile_id);
    }
    if entry.profile_deleted == Some(true) {
        shown.push_str(", deleted");
    }
    shown
}

fn show(result: &SessionShowResult) -> String {
    let entry = &result.entry;
    let mut out = String::new();
    let _ = writeln!(out, "{}  {}", entry.id, entry.label);
    let _ = writeln!(out, "agent      {}", entry.agent);
    let _ = writeln!(out, "state      {}", ledger_state(entry));
    if entry.priority == Some(true) {
        out.push_str("priority   on\n");
    }
    if let Some(until) = entry
        .turn_snoozed_until
        .as_deref()
        .filter(|u| !u.is_empty())
    {
        let _ = writeln!(out, "snoozed until {until}");
    }
    let _ = writeln!(out, "directory  {}", entry.directory);
    if let Some(branch) = entry.branch.as_deref().filter(|b| !b.is_empty()) {
        let _ = writeln!(out, "branch     {branch}");
    }
    if entry.is_worktree == Some(true) {
        let _ = writeln!(
            out,
            "worktree   yes, of {}",
            or_dash(entry.main_repo.as_deref().unwrap_or(""))
        );
    }
    if let Some(repository) = entry.repository.as_deref().filter(|r| !r.is_empty()) {
        let _ = writeln!(out, "repository {repository}");
    }
    let _ = writeln!(out, "profile    {}", ledger_profile(entry));
    let _ = writeln!(out, "last seen  {}", short_stamp(&entry.last_seen));
    if let Some(usage) = &entry.usage {
        let _ = writeln!(out, "usage      {}", usage_text(usage));
    }
    if let Some(closed_at) = entry.closed_at.as_deref().filter(|at| !at.is_empty()) {
        let _ = writeln!(
            out,
            "closed     {} by {}",
            short_stamp(closed_at),
            or_dash(entry.closed_by.as_deref().unwrap_or(""))
        );
        if let Some(reason) = trimmed(entry.close_reason.as_deref()) {
            let _ = writeln!(out, "because    {reason}");
        }
    }
    if let Some(reopen) = &result.reopen {
        verdict(&mut out, &entry.id, reopen);
    }
    out
}

fn verdict(out: &mut String, session: &str, reopen: &SessionReopen) {
    let mut verdict = if reopen.reopenable { "yes" } else { "no" }.to_string();
    if let Some(reason) = trimmed(reopen.reason.as_deref()) {
        let _ = write!(verdict, ": {reason}");
    }
    let _ = writeln!(out, "reopen     {verdict}");
    if let Some(warning) = trimmed(reopen.warning.as_deref()) {
        let _ = writeln!(out, "warning    {warning}");
    }
    if reopen.profile_deleted {
        let _ = writeln!(
            out,
            "lands in   profile {} is gone; this session cannot reopen",
            reopen.profile_id
        );
    } else {
        let _ = writeln!(
            out,
            "lands in   profile {}, last or current desktop",
            reopen.profile_id
        );
    }
    let _ = write!(out, "place      directory {}", reopen.directory_state);
    if let Some(branch) = trimmed(reopen.branch_state.as_deref()) {
        let _ = write!(out, ", branch {branch}");
    }
    out.push('\n');
    let Some(first) = reopen.actions.first() else {
        out.push_str("actions    none\n");
        return;
    };
    let _ = writeln!(out, "actions    {}", reopen.actions.join(", "));
    let _ = writeln!(
        out,
        "           attn session reopen {session} --action {first}"
    );
}

fn reopened(result: &SessionReopenResult) -> String {
    if result.already_running == Some(true) {
        return format!(
            "{} is already running in profile {}\n",
            result.session_id, result.profile_id
        );
    }
    let mut out = String::new();
    if let Some(created) = result.worktree_created.as_deref().filter(|c| !c.is_empty()) {
        let _ = writeln!(out, "recreated worktree {created}");
    }
    let _ = writeln!(
        out,
        "{} reopened in {} (profile {}, last or current desktop, {})",
        result.session_id, result.directory, result.profile_id, result.action
    );
    out
}
