use super::support::{
    bool_flag, client, current_session_or_exit, has_help_flag, parse_flags, string_flag,
};
use super::{Run, Stop, USAGE_UNKNOWN_COMMAND, print_reply_field, print_stdout};
use crate::paths::os_error_text;
use attn::go::flag::FlagSet;
use attn::go::quote::quote;
use attn_protocol::{JournalAppendMessage, JournalAppendResult};
use std::io::Read;

const RESULT_FIELD: &str = "journal_append_result";

const HELP: &str = r"usage: attn journal <command>

commands:
  append (--entry <text> | --entry-file <path>) [--date YYYY-MM-DD] [--session <id>] [--json]
        serialized append to the notebook's daily journal (journal/<date>.md)
        through the daemon — the contention-safe way an agent writes the
        journal, instead of editing the file directly with its own file-edit
        tools (which races other agents writing the same file).
        date defaults to today. --json prints rel_path and hash.

The session defaults to the current session.
";

fn usage_failure(message: &str) -> Stop {
    eprint!("journal append: {message}\n{HELP}");
    Stop(2)
}

fn read_entry_file(path: &str) -> Result<String, String> {
    let describe = |op: &str, error: &std::io::Error| {
        format!("read entry file: {op} {path}: {}", os_error_text(error))
    };
    let mut file = std::fs::File::open(path).map_err(|error| describe("open", &error))?;
    let mut content = Vec::new();
    file.read_to_end(&mut content)
        .map_err(|error| describe("read", &error))?;
    Ok(String::from_utf8_lossy(&content).trim().to_string())
}

struct Entry {
    text: String,
    date: String,
    session: String,
    json: bool,
}

fn parse_append(run: &mut Run<'_>, args: &[String]) -> Result<Entry, Stop> {
    let definitions = [
        string_flag("entry"),
        string_flag("entry-file"),
        string_flag("date"),
        string_flag("session"),
        bool_flag("json"),
    ];
    let mut flags = FlagSet::new("journal append", &definitions);
    parse_flags(run, &mut flags, args).map_err(|error| usage_failure(&error.to_string()))?;
    if !flags.args().is_empty() {
        return Err(usage_failure(&format!(
            "unexpected arguments: [{}]",
            flags.args().join(" ")
        )));
    }
    let entry = flags.string("entry").trim();
    let entry_file = flags.string("entry-file");
    if !entry.is_empty() && !entry_file.trim().is_empty() {
        return Err(usage_failure("pass only one of --entry or --entry-file"));
    }
    let text = if entry_file.trim().is_empty() {
        entry.to_string()
    } else {
        read_entry_file(entry_file).map_err(|message| usage_failure(&message))?
    };
    if text.is_empty() {
        return Err(usage_failure("--entry or --entry-file is required"));
    }
    let mut session = flags.string("session").trim().to_string();
    if session.is_empty() {
        session = current_session_or_exit(run)?;
    }
    Ok(Entry {
        text,
        date: flags.string("date").trim().to_string(),
        session,
        json: flags.bool("json"),
    })
}

fn append(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let entry = parse_append(run, args)?;
    let mut message = JournalAppendMessage::new();
    message.entry = entry.text;
    message.source_session_id = (!entry.session.is_empty()).then_some(entry.session);
    message.date = (!entry.date.is_empty()).then_some(entry.date);
    let label = "journal append";
    let reply = client(run)
        .call_field(
            &message,
            RESULT_FIELD,
            "daemon returned no journal append result",
        )
        .map_err(|error| run.fail_send(label, &error))?;
    if entry.json {
        return print_reply_field(&reply, &[RESULT_FIELD])
            .map_err(|error| run.fail_send(label, &error));
    }
    let result: JournalAppendResult = reply
        .value(RESULT_FIELD)
        .map_err(|error| run.fail_send(label, &error))?;
    print_stdout(&format!("appended to {}\n", result.rel_path));
    Ok(())
}

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let Some(verb) = args.first() else {
        print_stdout(HELP);
        return Ok(());
    };
    if verb == "-h" || verb == "--help" {
        print_stdout(HELP);
        return Ok(());
    }
    if verb != "append" {
        run.usage_error = Some(USAGE_UNKNOWN_COMMAND);
        eprint!(
            "journal: unknown command {}\n{HELP}",
            quote(verb.as_bytes())
        );
        return Err(Stop(2));
    }
    run.path = "journal append".to_string();
    if has_help_flag(&args[1..]) {
        print_stdout(HELP);
        return Ok(());
    }
    append(run, &args[1..])
}
