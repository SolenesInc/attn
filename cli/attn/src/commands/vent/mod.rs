mod help_generated;
mod table_generated;

use super::{Run, Stop, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use attn_protocol::{Vent as RecordedVent, VentWithdrawResult};
use serde_json::Value as Json;
use std::io::Read;

struct Vent;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &Vent, args)
}

const ABOUT: &str = "say what the vent is about: --about attn for attn itself, including attn blocked inside your harness, or --about tools for your other tooling (skills, other CLIs, harness permissions)";
const SAY_WHAT: &str = r#"say what got in your way: attn vent --about attn "<what happened, what you expected, what you did instead>""#;

fn outside_a_session(parsed: &Parsed) -> bool {
    parsed.session.is_empty() && crate::hooks::terminal_from_env(&env).trim().is_empty()
}

fn env(name: &str) -> String {
    std::env::var(name).unwrap_or_default()
}

fn read_text(parsed: &Parsed) -> Result<String, String> {
    let file = parsed.text("file");
    if !file.is_empty() {
        let bytes = std::fs::read(file)
            .map_err(|error| format!("open {file}: {}", crate::paths::os_error_text(&error)))?;
        return Ok(String::from_utf8_lossy(&bytes).into_owned());
    }
    let mut raw = Vec::new();
    std::io::stdin()
        .read_to_end(&mut raw)
        .map_err(|error| crate::paths::os_error_text(&error))?;
    let text = String::from_utf8_lossy(&raw).into_owned();
    if text.trim().is_empty() {
        return Err(SAY_WHAT.to_string());
    }
    Ok(text)
}

impl Family for Vent {
    fn group(&self) -> &'static str {
        "vent"
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
        Stop(2)
    }

    fn refused_line(&self, verb: &str, message: &str) -> String {
        if verb.is_empty() {
            format!("vent refused: {message}")
        } else {
            format!("{}: {message}", self.label(verb))
        }
    }

    fn before_send(&self, cmd: &Command, parsed: &mut Parsed) -> Result<(), Stop> {
        if cmd.name.is_empty() {
            if !parsed.text("text").is_empty() && parsed.has("file") {
                return Err(self.usage_error(
                    cmd.name,
                    "pass the vent as arguments or with --file, not both",
                ));
            }
            if !["attn", "tools"].contains(&parsed.text("about").trim()) {
                return Err(self.usage_error(cmd.name, ABOUT));
            }
        }
        if outside_a_session(parsed) {
            return Err(self.usage_error(
                "",
                "no session — pass --session <id>, or run inside an attn session",
            ));
        }
        if cmd.name.is_empty() && parsed.text("text").is_empty() {
            let text = read_text(parsed).map_err(|message| self.fail(cmd.name, &message))?;
            parsed.args.insert("text".to_string(), Json::from(text));
        }
        parsed.args.remove("file");
        Ok(())
    }

    fn no_session(&self, _verb: &str, message: &str) -> Stop {
        command::unresolved_session(message)
    }

    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        _parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        if cmd.name.is_empty() {
            let vent: RecordedVent = command::decode(run, self, cmd, &result)?;
            print_stdout(&format!(
                "vent {} recorded. Keep working; nobody replies to vents.\n",
                vent.id
            ));
        } else {
            let withdrawn: VentWithdrawResult = command::decode(run, self, cmd, &result)?;
            print_stdout(&format!("Withdrew {}.\n", withdrawn.id));
        }
        Ok(())
    }
}
