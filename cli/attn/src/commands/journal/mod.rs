mod help_generated;
mod table_generated;

use super::{Run, Stop, print_json, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use crate::paths::os_error_text;
use attn_protocol::JournalAppendResult;
use serde_json::Value as Json;

struct Journal;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    if args.first().is_some_and(|verb| !verb.starts_with('-')) {
        super::warn_if_stale(run.paths);
    }
    command::run_group(run, &Journal, args)
}

fn read_entry_file(path: &str) -> Result<String, String> {
    let bytes = std::fs::read(path)
        .map_err(|error| format!("read entry file: open {path}: {}", os_error_text(&error)))?;
    Ok(String::from_utf8_lossy(&bytes).trim().to_string())
}

impl Family for Journal {
    fn group(&self) -> &'static str {
        "journal"
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
        let usage = |message: &str| self.usage_error(cmd.name, message);
        let entry = parsed.text("entry").trim().to_string();
        let file = parsed.text("entry_file").trim().to_string();
        parsed.args.remove("entry_file");
        if !entry.is_empty() && !file.is_empty() {
            return Err(usage("pass only one of --entry or --entry-file"));
        }
        let text = if file.is_empty() {
            entry
        } else {
            read_entry_file(&file).map_err(|message| usage(&message))?
        };
        if text.is_empty() {
            return Err(usage("--entry or --entry-file is required"));
        }
        parsed.args.insert("entry".to_string(), Json::from(text));
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
        let label = format!("journal {}", cmd.name);
        if parsed.json {
            let raw = result
                .raw_field(&[])
                .map_err(|error| run.fail_send(&label, &error))?;
            print_json(raw.as_bytes());
            return Ok(());
        }
        let appended: JournalAppendResult = result
            .whole()
            .map_err(|error| run.fail_send(&label, &error))?;
        print_stdout(&format!("appended to {}\n", appended.rel_path));
        Ok(())
    }
}
