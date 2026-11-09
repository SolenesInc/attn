use super::support::{client, parse_or_exit};
use super::worktree::format_table;
use super::{Run, Stop, USAGE_UNKNOWN_COMMAND, print_reply_field, print_stdout};
use crate::client::SendError;
use attn::go::flag::{Flag, FlagDefault, FlagSet};
use attn::go::time::Time;
use attn_protocol::{
    KeptConversationForgetMessage, KeptConversationForgetResult, KeptConversationKeepMessage,
    KeptConversationListMessage, KeptConversationListResult, KeptConversationRow,
};
use std::fmt::Write as _;

const USAGE: &str = r"usage: attn conversation list [--all] [--json]
       attn conversation keep <session-or-conversation-id>
       attn conversation unkeep <session-or-conversation-id>
       attn conversation forget <session-or-conversation-id> [--unlink-crew-letters]

  list      every conversation attn keeps or will keep; --all includes deleted copies
  keep      keep a Claude conversation forever
  unkeep    remove the pin; the next keep pass decides again
  forget    delete attn's copy now, leaving Claude's own files untouched

Use session:<id> or conversation:<id> when an identifier is ambiguous.
Open seeds block forget; harvest or wither them first. If crew letters keep
the copy, forget refuses and names them. --unlink-crew-letters consents to
removing their session records, leaving the letters unchanged, then deleting
attn's copy. The output names the letters that lost their transcript.
";

const fn bool_flag(name: &'static str, usage: &'static str) -> Flag<'static> {
    Flag {
        name,
        default: FlagDefault::Bool(false),
        usage,
    }
}

fn usage_failure() -> Stop {
    eprint!("{USAGE}");
    Stop(1)
}

fn failed(run: &mut Run<'_>, error: &SendError) -> Stop {
    run.fail_send("error", error)
}

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let Some(verb) = args.first().map(String::as_str) else {
        return Err(usage_failure());
    };
    match verb {
        "--help" | "-h" | "help" => {
            print_stdout(USAGE);
            Ok(())
        }
        "list" => {
            run.path = "conversation list".to_string();
            list(run, &args[1..])
        }
        "forget" => {
            run.path = "conversation forget".to_string();
            forget(run, &args[1..])
        }
        "keep" | "unkeep" => {
            run.path = format!("conversation {verb}");
            keep(run, args, verb == "keep")
        }
        _ => {
            run.usage_error = Some(USAGE_UNKNOWN_COMMAND);
            eprint!("unknown conversation command: {verb}\n\n{USAGE}");
            Err(Stop(1))
        }
    }
}

fn keep(run: &mut Run<'_>, args: &[String], keep: bool) -> Result<(), Stop> {
    if args.len() != 2 || args[1].trim().is_empty() {
        return Err(usage_failure());
    }
    let id = &args[1];
    let mut message = KeptConversationKeepMessage::new();
    message.session_id.clone_from(id);
    message.keep = keep;
    client(run)
        .send(&message)
        .map_err(|error| failed(run, &error))?;
    print_stdout(&if keep {
        format!("pinned conversation {id}; attn keeps it forever\n")
    } else {
        format!("unpinned conversation {id}; the next keep pass decides again\n")
    });
    Ok(())
}

fn forget(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut flags = FlagSet::new(
        "conversation forget",
        &[bool_flag(
            "unlink-crew-letters",
            "remove crew letters' session records and delete attn's copy; leave the letters unchanged",
        )],
    );
    parse_or_exit(run, &mut flags, args)?;
    let positionals = flags.args().to_vec();
    let Some(id) = positionals.first().filter(|id| !id.trim().is_empty()) else {
        return Err(usage_failure());
    };
    parse_or_exit(run, &mut flags, &positionals[1..])?;
    if !flags.args().is_empty() {
        return Err(usage_failure());
    }
    let mut message = KeptConversationForgetMessage::new();
    message.session_id.clone_from(id);
    message.unlink_crew_letters = Some(flags.bool("unlink-crew-letters"));
    let reply = client(run)
        .call_field(
            &message,
            "kept_conversation_forget_result",
            "daemon returned no conversation forget result",
        )
        .map_err(|error| failed(run, &error))?;
    let result: KeptConversationForgetResult = reply
        .value("kept_conversation_forget_result")
        .map_err(|error| failed(run, &error))?;
    let mut out =
        format!("deleted attn's copy of conversation {id}; Claude's own files are untouched\n");
    if !result.unlinked_crew_letters.is_empty() {
        out.push_str("letters that lost their transcript (letters are unchanged):\n");
        for letter in &result.unlinked_crew_letters {
            let _ = writeln!(out, "  {} {}", letter.member, letter.filename);
        }
    }
    print_stdout(&out);
    Ok(())
}

fn list(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut flags = FlagSet::new(
        "conversation list",
        &[
            bool_flag("all", "include deleted copies"),
            bool_flag("json", "print the raw result"),
        ],
    );
    parse_or_exit(run, &mut flags, args)?;
    if !flags.args().is_empty() {
        return Err(usage_failure());
    }
    let mut message = KeptConversationListMessage::new();
    message.include_deleted = Some(flags.bool("all"));
    let reply = client(run)
        .call_field(
            &message,
            "kept_conversation_list_result",
            "daemon returned no conversation list",
        )
        .map_err(|error| failed(run, &error))?;
    if flags.bool("json") {
        return print_reply_field(&reply, &["kept_conversation_list_result"])
            .map_err(|error| failed(run, &error));
    }
    let result: KeptConversationListResult = reply
        .value("kept_conversation_list_result")
        .map_err(|error| failed(run, &error))?;
    print_stdout(&render(&result));
    Ok(())
}

fn render(result: &KeptConversationListResult) -> String {
    let mut out = format!(
        "{} kept · {}",
        result.count,
        format_bytes(result.stored_bytes)
    );
    if let Some(date) = result
        .next_delete_after
        .as_deref()
        .filter(|date| !date.is_empty())
    {
        let _ = write!(out, " · next deletion {}", date_of(date));
    }
    if result.pending_count > 0 {
        let _ = write!(out, " · {} pending", result.pending_count);
    }
    out.push('\n');
    if result.rows.is_empty() {
        return out;
    }
    let mut rows = vec![
        [
            "CONVERSATION",
            "ID",
            "AGENT",
            "SIZE",
            "KEPT BECAUSE",
            "SESSION",
        ]
        .map(String::from)
        .to_vec(),
    ];
    for row in &result.rows {
        let size = row
            .kept
            .as_ref()
            .map_or_else(|| "-".to_string(), |kept| format_bytes(kept.bytes));
        rows.push(vec![
            row.title.clone(),
            row.resume_id.clone(),
            row.agent.clone(),
            size,
            reasons(row).join("; "),
            row.session_ids.join(", "),
        ]);
    }
    out.push_str(&format_table(&rows));
    out
}

fn reasons(row: &KeptConversationRow) -> Vec<String> {
    let Some(kept) = &row.kept else {
        return vec![row.pending_reason.clone().unwrap_or_default()];
    };
    if let Some(deleted) = &kept.deleted_at {
        let who = if kept.deleted_by.as_deref() == Some("user") {
            "you deleted"
        } else {
            "attn deleted"
        };
        return vec![format!("{who} {}", date_of(deleted))];
    }
    let mut reasons = Vec::new();
    if let Some(pinned) = &kept.pinned_at {
        reasons.push(format!("pinned {}", date_of(pinned)));
    }
    for seed in &row.seeds {
        reasons.push(format!("open seed {} ({})", seed.slug, seed.id));
    }
    for letter in row.crew_letters.iter().flatten() {
        reasons.push(format!("crew letter {} {}", letter.member, letter.filename));
    }
    if let Some(after) = &kept.delete_after {
        reasons.push(format!("deletes {}", date_of(after)));
    }
    if reasons.is_empty() {
        reasons.push("awaiting next keep pass".to_string());
    }
    reasons
}

#[allow(clippy::cast_precision_loss)]
fn format_bytes(bytes: i64) -> String {
    if bytes < 1000 {
        format!("{bytes} B")
    } else if bytes < 1_000_000 {
        format!("{:.1} KB", bytes as f64 / 1000.0)
    } else {
        format!("{:.1} MB", bytes as f64 / 1_000_000.0)
    }
}

fn date_of(stamp: &str) -> String {
    Time::parse_rfc3339_nano(stamp).map_or_else(|_| stamp.to_string(), |at| at.format("2006-01-02"))
}
