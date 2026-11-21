mod help_generated;
mod table_generated;

use super::{Run, Stop, absolute, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use crate::paths::os_error_text;
use attn_protocol::{BrowserFileResult, BrowserOpenResult, BrowserResult};
use serde_json::Value as Json;

struct Browser;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &Browser, args)
}

fn default_capture(verb: &str) -> Option<&'static str> {
    match verb {
        "screenshot" => Some("attn-browser.png"),
        "pdf" => Some("attn-browser.pdf"),
        _ => None,
    }
}

impl Family for Browser {
    fn group(&self) -> &'static str {
        "browser"
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
        let label = self.label(verb);
        eprintln!("{label}: {message}");
        eprintln!(
            "usage: attn browser {} (attn {label} --help)",
            self.usage(verb)
        );
        Stop(1)
    }

    fn before_send(&self, cmd: &Command, parsed: &mut Parsed) -> Result<(), Stop> {
        if cmd.name == "command" || cmd.name == "pdf" {
            parsed.args.insert("wait".to_string(), Json::from(true));
        }
        let Some(fallback) = default_capture(cmd.name) else {
            return Ok(());
        };
        let given = parsed.text("path").to_string();
        let path = if given.is_empty() { fallback } else { &given };
        let resolved = absolute(path).map_err(|error| {
            self.fail(
                cmd.name,
                &format!("resolve path: {}", os_error_text(&error)),
            )
        })?;
        parsed.args.insert("path".to_string(), Json::from(resolved));
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
        if parsed.json {
            return command::print_result(run, self, cmd, &result);
        }
        let line = match cmd.name {
            "open" => {
                let opened: BrowserOpenResult = command::decode(run, self, cmd, &result)?;
                format!("opened {}", opened.url)
            }
            "screenshot" | "pdf" => {
                let saved: BrowserFileResult = command::decode(run, self, cmd, &result)?;
                saved.path
            }
            _ => {
                let answered: BrowserResult = command::decode(run, self, cmd, &result)?;
                answered.data
            }
        };
        print_stdout(&format!("{line}\n"));
        Ok(())
    }
}
