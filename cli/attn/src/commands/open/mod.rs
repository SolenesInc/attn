mod help_generated;
mod table_generated;

use super::review::problem_lines;
use super::{Run, Stop, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use attn_protocol::OpenResult;

struct Open;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &Open, args)
}

impl Family for Open {
    fn group(&self) -> &'static str {
        "open"
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
        eprintln!("usage: attn open {} (attn open --help)", self.usage(verb));
        Stop(1)
    }

    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        _parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        let opened: OpenResult = command::decode(run, self, cmd, &result)?;
        print_stdout(&format!("opened {}\n", opened.opened));
        let problems = opened.problems.unwrap_or_default();
        if !problems.is_empty() {
            eprint!("{}", problem_lines(&problems, "attn open: warning: "));
        }
        Ok(())
    }
}
