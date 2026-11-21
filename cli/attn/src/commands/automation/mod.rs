mod help_generated;
mod table_generated;

use super::crew::launch_desktop_text;
use super::{Run, Stop, print_json, print_stdout};
use crate::client::Reply;
use crate::command::{self, Arg, Command, Family, Parsed};
use attn_protocol::{AutomationDefinitionSummary, AutomationShowResult};
use serde_json::Value as Json;
use std::fmt::Write as _;

const FAILED_RUN_STATE: &str = "failed";

struct Automation;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &Automation, args)
}

impl Family for Automation {
    fn group(&self) -> &'static str {
        "automation"
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

    fn decode_file(&self, _cmd: &Command, arg: &Arg, bytes: &[u8]) -> Result<Json, String> {
        if arg.name == "input" {
            return serde_json::from_slice(bytes)
                .map_err(|error| format!("--input-file is not valid JSON: {error}"));
        }
        Ok(Json::from(String::from_utf8_lossy(bytes).into_owned()))
    }

    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        let label = format!("automation {}", cmd.name);
        if cmd.name == "show" && !parsed.json {
            let shown: AutomationShowResult = result
                .whole()
                .map_err(|error| run.fail_send(&label, &error))?;
            print_stdout(&format!(
                "{}{}",
                show_header(&shown.definition),
                shown.spec_yaml
            ));
            return Ok(());
        }
        let path: &[&str] = match cmd.name {
            "list" => &["definitions"],
            "runs" => &["runs"],
            _ => &[],
        };
        let raw = result
            .raw_field(path)
            .map_err(|error| run.fail_send(&label, &error))?;
        print_json(raw.as_bytes());
        Ok(())
    }
}

fn comment_lines(text: &str) -> String {
    text.replace('\n', "\n# ")
}

fn show_header(definition: &AutomationDefinitionSummary) -> String {
    let mut out = format!(
        "# Profile: {}\n# Launch desktop: {}\n",
        definition.profile_name.as_deref().unwrap_or(""),
        launch_desktop_text(definition.launch_desktop.as_ref())
    );
    if let Some(last) = &definition.last_run {
        let session = last.session_id.as_deref().unwrap_or("");
        if !session.is_empty() {
            let _ = write!(out, "# Last run session: {session}");
            let superseded = last
                .automation
                .as_ref()
                .is_some_and(|provenance| provenance.superseded == Some(true));
            if superseded {
                out.push_str(" (superseded; the next occurrence starts a new session)");
            }
            out.push('\n');
        }
        if last.state == FAILED_RUN_STATE {
            let _ = writeln!(
                out,
                "# Last run failed: {}",
                comment_lines(last.last_error.as_deref().unwrap_or(""))
            );
        }
    }
    if let Some(check) = &definition.guard_check {
        let _ = write!(out, "# Guard: {} at {}", check.outcome, check.checked_at);
        match check.detail.as_deref().filter(|detail| !detail.is_empty()) {
            Some(detail) => {
                let _ = write!(out, ": {}", comment_lines(detail));
            }
            None => {
                let _ = write!(
                    out,
                    " ({} checks since the last run)",
                    check.checks_since_run
                );
            }
        }
        out.push('\n');
    }
    out
}
