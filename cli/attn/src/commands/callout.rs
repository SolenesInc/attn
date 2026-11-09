use super::support::{
    bool_flag, client, current_session_or_exit, daemon_message, has_help_flag, parse_interspersed,
    string_flag,
};
use super::{Run, Stop, USAGE_UNKNOWN_COMMAND, print_stdout};
use crate::client::SendError;
use attn::go::flag::FlagSet;
use attn::go::quote::quote;
use attn::go::tabwriter;
use attn_protocol::{
    CalloutAnchorsMessage, CalloutAnchorsResult, CalloutShowMessage, CalloutShowStep,
};
use std::fmt::Write as _;

const HELP: &str = r#"Point at the attn app with short callouts.

Usage:
  attn callout anchors [--json]
  attn callout show <anchor> <text> [<anchor> <text>]...

List anchors first to see which are on screen. Agent anchors accept a session
id, short id or crew name (agent:<who>); desktop anchors accept a slot digit,
name or id (desktop:<which>). On the last step, Enter or → goes there; on an
earlier step it moves on.

A callout has at most 20 steps, each at most 160 characters. A new callout
replaces the one showing. The command returns when the app accepts it; the
user steps through it with Enter or →. Esc or the card's × ends it, and so
does a click outside a one-step callout.
A missing fixed anchor refuses the whole callout. Every refusal exits 1.
Use --session <id> to point from a specific session.

Examples:
  attn callout anchors
  attn callout show garden "Work you and your agents track"
  attn callout show agent:a1b2 "Needs your review on the migration plan"
"#;

struct Callout {
    command: String,
}

impl Callout {
    fn fail(&self, message: &str) -> Stop {
        eprintln!("callout {}: {message}", self.command);
        Stop(1)
    }

    fn fail_send(&self, run: &mut Run<'_>, error: &SendError) -> Stop {
        run.refusal.clone_from(&error.refusal);
        self.fail(&daemon_message(error))
    }
}

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    if args.is_empty() || has_help_flag(args) {
        print_stdout(HELP);
        return Ok(());
    }
    let callout = Callout {
        command: args[0].clone(),
    };
    let anchors = callout.command == "anchors";
    if !anchors && callout.command != "show" {
        run.usage_error = Some(USAGE_UNKNOWN_COMMAND);
        return Err(callout.fail(&format!(
            "unknown command {}; use show or anchors",
            quote(callout.command.as_bytes())
        )));
    }
    run.path = format!("callout {}", callout.command);
    let mut definitions = vec![string_flag("session")];
    if anchors {
        definitions.push(bool_flag("json"));
    }
    let name = run.path.clone();
    let mut flags = FlagSet::new(&name, &definitions);
    let positionals = parse_interspersed(run, &mut flags, &args[1..])
        .map_err(|error| callout.fail(&error.to_string()))?;
    if anchors && let Some(first) = positionals.first() {
        return Err(callout.fail(&format!("unexpected argument {}", quote(first.as_bytes()))));
    }
    if !anchors {
        if positionals.len() % 2 != 0 {
            let last = &positionals[positionals.len() - 1];
            return Err(callout.fail(&format!(
                "an anchor needs its text: {} is the last argument",
                quote(last.as_bytes())
            )));
        }
        if positionals.is_empty() {
            return Err(callout.fail("a callout needs at least one anchor and its text"));
        }
    }
    let mut session = flags.string("session").trim().to_string();
    if session.is_empty() {
        session = current_session_or_exit(run)?;
    }
    if session.is_empty() {
        return Err(callout.fail("no session; pass --session <id>, or run inside an attn session"));
    }
    if !anchors {
        let mut message = CalloutShowMessage::new();
        message.session_id = session;
        message.steps = positionals
            .chunks(2)
            .map(|pair| CalloutShowStep {
                anchor: pair[0].clone(),
                text: pair[1].clone(),
            })
            .collect();
        let reply = client(run)
            .call(&message)
            .map_err(|error| callout.fail_send(run, &error))?;
        print_stdout(&format!("{}\n", reply.optional_string("data")));
        return Ok(());
    }
    let mut message = CalloutAnchorsMessage::new();
    message.session_id = session;
    let reply = client(run)
        .call(&message)
        .map_err(|error| callout.fail_send(run, &error))?;
    let field = "callout_anchors_result";
    let result: CalloutAnchorsResult = reply
        .value(field)
        .map_err(|error| callout.fail_send(run, &error))?;
    if flags.bool("json") {
        let raw = reply
            .raw_field(&[field])
            .map_err(|error| callout.fail_send(run, &error))?;
        print_stdout(&format!("{raw}\n"));
    } else {
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
        print_stdout(&tabwriter::format(&table));
    }
    if !result.app_showing_profile {
        return Err(callout.fail("the attn app is not showing your profile"));
    }
    Ok(())
}
