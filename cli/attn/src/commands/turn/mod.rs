mod help_generated;
mod table_generated;

use super::{Run, Stop, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use attn_protocol::AgentTurnResult;

struct Turn;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    if args.is_empty() {
        eprint!("{}", help_generated::GROUP_HELP);
        return Err(Stop(2));
    }
    command::run_group(run, &Turn, args)
}

fn outcome_text(outcome: &str) -> &'static str {
    match outcome {
        "opens_at_stop" => {
            "Your turn opens when you stop; it then shows in the user's queue and notifies their phone, if they use them."
        }
        "opened" => {
            "Your turn is open; it shows in the user's queue and notifies their phone, if they use them."
        }
        "snoozed" => {
            "The user snoozed you; your turn opens once the snooze ends and you have stopped."
        }
        "settled" => "Your turn is settled.",
        "withdrawn" => "Your turn will not open when you stop.",
        _ => "No turn was open; nothing changed.",
    }
}

impl Family for Turn {
    fn group(&self) -> &'static str {
        "turn"
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

    fn refused_line(&self, verb: &str, message: &str) -> String {
        format!("attn turn {verb}: daemon error: {message}")
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
        let turn: AgentTurnResult = command::decode(run, self, cmd, &result)?;
        print_stdout(outcome_text(&turn.outcome));
        print_stdout("\n");
        Ok(())
    }
}
