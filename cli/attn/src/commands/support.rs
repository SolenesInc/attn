use super::{Run, Stop};
use crate::client::{Client, SendError};
use attn::go::flag::{Flag, FlagDefault, FlagError, FlagSet};
use attn_protocol::CallerSessionMessage;

const SANDBOX_REFUSAL_PREFIX: &str = "the sandbox blocks";
const DAEMON_ERROR_PREFIX: &str = "daemon error: ";

pub const fn bool_flag(name: &'static str) -> Flag<'static> {
    Flag {
        name,
        default: FlagDefault::Bool(false),
        usage: "",
    }
}

pub const fn string_flag(name: &'static str) -> Flag<'static> {
    Flag {
        name,
        default: FlagDefault::String(""),
        usage: "",
    }
}

pub fn has_help_flag(args: &[String]) -> bool {
    args.iter().any(|arg| arg == "-h" || arg == "--help")
}

pub fn parse_flags(
    run: &mut Run<'_>,
    flags: &mut FlagSet<'_>,
    args: &[String],
) -> Result<(), FlagError> {
    let parsed = flags.parse(args);
    if let Err(FlagError::Invalid(message)) = &parsed {
        run.record_flag_error(message);
    }
    parsed
}

pub fn parse_interspersed(
    run: &mut Run<'_>,
    flags: &mut FlagSet<'_>,
    args: &[String],
) -> Result<Vec<String>, FlagError> {
    let mut positionals = Vec::new();
    let mut rest = args.to_vec();
    loop {
        parse_flags(run, flags, &rest)?;
        rest = flags.args().to_vec();
        if rest.is_empty() {
            return Ok(positionals);
        }
        positionals.push(rest.remove(0));
    }
}

pub fn client<'a>(run: &Run<'a>) -> Client<'a> {
    Client::new(run.paths, "", &run.terminal)
}

pub enum SessionLookup {
    Found(String),
    NotShowing(String),
}

pub fn current_session(run: &Run<'_>) -> Result<SessionLookup, SendError> {
    if run.terminal.is_empty() {
        return Ok(SessionLookup::Found(String::new()));
    }
    let mut message = CallerSessionMessage::new();
    message.caller_id = Some(run.terminal.clone());
    let session = client(run)
        .call(&message)?
        .optional_string("caller_session_id");
    if session.is_empty() {
        return Ok(SessionLookup::NotShowing(format!(
            "terminal {} is not showing a session",
            run.terminal
        )));
    }
    Ok(SessionLookup::Found(session))
}

pub fn current_session_or_exit(run: &mut Run<'_>) -> Result<String, Stop> {
    let cause = match current_session(run) {
        Ok(SessionLookup::Found(session)) => return Ok(session),
        Ok(SessionLookup::NotShowing(message)) => message,
        Err(error) => {
            let message = error.to_string();
            if message.starts_with(SANDBOX_REFUSAL_PREFIX) {
                eprintln!("attn: {message}");
                return Err(Stop(1));
            }
            run.refusal = error.refusal;
            message
        }
    };
    eprintln!("attn: resolve current session: {cause}");
    Err(Stop(1))
}

pub fn daemon_message(error: &SendError) -> String {
    let message = error.to_string();
    match message.strip_prefix(DAEMON_ERROR_PREFIX) {
        Some(stripped) => stripped.to_string(),
        None => message,
    }
}
