use super::support::{client, current_session_or_exit, parse_interspersed, string_flag};
use super::{Run, Stop, absolute, print_stdout};
use crate::paths::os_error_text;
use attn::go::flag::FlagSet;
use attn_protocol::{OpenMarkdownMessage, OpenSeedMessage};

pub const USAGE: &str = "usage: attn open <file.md|seed-id> [--session <id>]\n";

fn usage_failure(message: &str) -> Stop {
    eprint!("attn open: {message}\n{USAGE}");
    Stop(1)
}

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let definitions = [string_flag("session"), string_flag("profile")];
    let mut flags = FlagSet::new("open", &definitions);
    let positionals = parse_interspersed(run, &mut flags, args)
        .map_err(|error| usage_failure(&error.to_string()))?;
    let Some(first) = positionals.first() else {
        return Err(usage_failure("missing <file.md|seed-id> argument"));
    };
    if positionals.len() > 1 {
        return Err(usage_failure(&format!(
            "unexpected extra arguments: [{}]",
            positionals[1..].join(" ")
        )));
    }
    let target = first.trim();
    let mut session = flags.string("session").trim().to_string();
    if session.is_empty() {
        session = current_session_or_exit(run)?;
    }
    let session = (!session.is_empty()).then_some(session);
    if target.starts_with("s-") {
        let profile = flags.string("profile").trim();
        let mut message = OpenSeedMessage::new();
        message.seed_id = target.to_string();
        message.session_id = session;
        message.profile_id = (!profile.is_empty()).then(|| profile.to_string());
        client(run)
            .send(&message)
            .map_err(|error| run.fail_send("open", &error))?;
        print_stdout(&format!("opened {target}\n"));
        return Ok(());
    }
    let path = absolute(target).map_err(|error| {
        eprintln!("open: resolve path: {}", os_error_text(&error));
        Stop(1)
    })?;
    let mut message = OpenMarkdownMessage::new();
    message.path.clone_from(&path);
    message.session_id = session;
    client(run)
        .send(&message)
        .map_err(|error| run.fail_send("open", &error))?;
    print_stdout(&format!("opened {path}\n"));
    Ok(())
}
