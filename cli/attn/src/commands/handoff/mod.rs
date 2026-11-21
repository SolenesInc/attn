mod help_generated;
mod table_generated;

use super::{Run, Stop, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use attn_protocol::CrewHandoffResult;

struct Handoff;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &Handoff, args)
}

fn short(id: &str) -> &str {
    id.get(..8).unwrap_or(id)
}

impl Family for Handoff {
    fn group(&self) -> &'static str {
        "handoff"
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
        let letter = parsed.text("letter").trim().to_string();
        match (parsed.on("retry"), letter.is_empty()) {
            (true, false) => Err(self.usage_error(
                cmd.name,
                "--retry finishes the handoff with the letter you already filed, so it takes no -m; drop one of the two",
            )),
            (false, true) => Err(self.usage_error(
                cmd.name,
                r#"the letter is the handoff — pass it with -m "<your letter>", or -m - to pipe it in"#,
            )),
            _ => Ok(()),
        }
    }

    fn no_session(&self, _verb: &str, message: &str) -> Stop {
        command::unresolved_session(message)
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
        let handed: CrewHandoffResult = command::decode(run, self, cmd, &result)?;
        let name = &handed.name;
        if parsed.on("retry") {
            print_stdout(&format!(
                "{name}'s letter was already filed at {}.\n",
                handed.path
            ));
        } else {
            print_stdout(&format!("{name}'s letter is filed at {}.\n", handed.path));
        }
        let successor_error = handed.successor_error.as_deref().unwrap_or("").trim();
        if !successor_error.is_empty() {
            eprintln!(
                "handoff: the next session did not start: {successor_error}\nThis session is still running and {name} is still bound to it. `attn handoff --retry` tries again. The letter above is filed, so do not write another."
            );
            return Err(Stop(1));
        }
        if handed.outcome.as_deref() == Some("closed") {
            print_stdout(&format!(
                "{name} is between sessions. attn started no session behind you; the sidebar has {name} one click from a new session.\n"
            ));
            return Ok(());
        }
        print_stdout(&format!(
            "{name}'s next session is {} on its new harness, starting now. This one ends here.\n",
            short(handed.session_id.as_deref().unwrap_or(""))
        ));
        Ok(())
    }
}
