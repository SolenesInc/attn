mod help_generated;
mod present;
mod table_generated;

use super::{Run, Stop, USAGE_UNKNOWN_COMMAND, print_stdout};
use crate::command::{self, Family};
use attn::go::quote::quote;
use present::Delegate;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    if args.first().map(String::as_str) != Some("roles") {
        return command::run_group(run, &Delegate, args);
    }
    let words = &args[1..];
    let (name, rest) = match words.first() {
        Some(verb) if verb == "help" => {
            print_stdout(help_generated::help("roles"));
            return Ok(());
        }
        Some(verb) if !verb.starts_with('-') => (format!("roles {verb}"), &words[1..]),
        _ => ("roles".to_string(), words),
    };
    let Some(cmd) = Delegate.commands().iter().find(|cmd| cmd.name == name) else {
        run.path = "delegate roles".to_string();
        run.usage_error = Some(USAGE_UNKNOWN_COMMAND);
        eprintln!(
            "delegate roles: unknown command {}; attn delegate roles --help lists them",
            quote(words[0].as_bytes())
        );
        return Err(Stop(2));
    };
    run.path = Delegate.label(cmd.name);
    command::run_command(run, &Delegate, cmd, rest)
}
