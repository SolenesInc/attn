mod help_generated;
mod table_generated;

use super::{Run, Stop, print_compact_line, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use attn::go::tabwriter;
use attn_protocol::{CalloutAnchorsResult, CalloutShowResult};
use std::fmt::Write as _;

struct Callout;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &Callout, args)
}

fn anchors(result: &CalloutAnchorsResult) -> String {
    let mut table = String::new();
    for anchor in &result.anchors {
        let visibility = match anchor.on_screen {
            Some(true) => "on screen",
            Some(false) => "not on screen",
            None => "",
        };
        let _ = writeln!(
            table,
            "{}\t{visibility}\t{}",
            anchor.name, anchor.description
        );
    }
    tabwriter::format(&table)
}

fn shown(result: &CalloutShowResult) -> String {
    let steps = if result.steps == 1 { "step" } else { "steps" };
    format!(
        "showing a callout with {} {steps}; the user steps through it\n",
        result.steps
    )
}

impl Callout {
    fn print_raw(&self, run: &mut Run<'_>, cmd: &Command, result: &Reply) -> Result<(), Stop> {
        let raw = result
            .raw_field(&[])
            .map_err(|error| run.fail_send(&self.label(cmd.name), &error))?;
        print_compact_line(raw);
        Ok(())
    }
}

impl Family for Callout {
    fn group(&self) -> &'static str {
        "callout"
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
            "usage: attn callout {} (attn {label} --help)",
            self.usage(verb)
        );
        Stop(1)
    }

    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        if cmd.name == "show" {
            if parsed.json {
                return self.print_raw(run, cmd, &result);
            }
            print_stdout(&shown(&command::decode(run, self, cmd, &result)?));
            return Ok(());
        }
        let listed: CalloutAnchorsResult = command::decode(run, self, cmd, &result)?;
        if parsed.json {
            self.print_raw(run, cmd, &result)?;
        } else {
            print_stdout(&anchors(&listed));
        }
        if !listed.app_showing_profile {
            return Err(self.fail(cmd.name, "the attn app is not showing your profile"));
        }
        Ok(())
    }
}
