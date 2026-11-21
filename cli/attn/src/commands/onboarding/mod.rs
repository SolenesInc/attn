mod help_generated;
mod table_generated;

use super::{Run, Stop, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use attn_protocol::OnboardingResult;

struct Onboarding;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &Onboarding, args)
}

fn said(cmd: &Command, result: &OnboardingResult) -> String {
    let chief = &result.chief;
    let mut out = match (cmd.name, result.pending.as_deref()) {
        ("done", _) => format!(
            "{chief}'s onboarding is done. `attn onboarding again` runs the full path again.\n"
        ),
        ("again", _) => format!(
            "{chief}'s onboarding is pending again, on the full path. The Chief's next session opens with it; a Chief in session can start it now.\n"
        ),
        (_, None | Some("")) => format!(
            "{chief} has no onboarding pending. `attn onboarding again` runs the full path again.\n"
        ),
        (_, Some("tour")) => format!(
            "{chief}'s onboarding is pending. It takes the tour, because the user used attn before the Chief.\n"
        ),
        _ => format!("{chief}'s onboarding is pending. It takes the full path.\n"),
    };
    if !result.configured {
        out.push_str("The Chief has no harness yet. Pick its harness and model in the sidebar, or run `attn crew set chief --agent <harness> --model <model>`.\n");
    }
    out
}

impl Family for Onboarding {
    fn group(&self) -> &'static str {
        "onboarding"
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

    fn refused_line(&self, _verb: &str, message: &str) -> String {
        format!("daemon error: {message}")
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
        print_stdout(&said(cmd, &command::decode(run, self, cmd, &result)?));
        Ok(())
    }
}
