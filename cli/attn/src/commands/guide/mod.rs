mod help_generated;
mod table_generated;

use super::{Run, Stop, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};

struct Guide;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &Guide, args)
}

impl Family for Guide {
    fn group(&self) -> &'static str {
        "guide"
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

    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        _parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let text: String = command::result(reply)
            .whole()
            .map_err(|error| run.fail_send(&self.label(cmd.name), &error))?;
        print_stdout(&text);
        Ok(())
    }
}
