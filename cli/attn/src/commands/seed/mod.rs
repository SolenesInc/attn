mod checks;
mod flags;
mod help_generated;
mod render;
mod table;
mod verbs;

use super::{Run, Stop, print_stdout};
use crate::client::{Client, Reply, SendError};
use attn::go::quote::quote;
use attn_protocol::CallerSessionMessage;
use attn_protocol::go_values::CODEX_THREAD_PREFIX;
use flags::{Flags, ParseStop};
use std::io::Read;
use table::VERBS;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let Some(verb) = args.first() else {
        print_stdout(help_generated::GROUP_HELP);
        return Ok(());
    };
    if verb == "-h" || verb == "--help" {
        print_stdout(help_generated::GROUP_HELP);
        return Ok(());
    }
    let rest = &args[1..];
    let known = VERBS
        .iter()
        .any(|entry| entry.name == verb && !entry.name.contains(' '));
    if !known {
        run.path = "seed".to_string();
        run.usage_error = Some(super::USAGE_UNKNOWN_COMMAND);
        eprintln!("seed: unknown command {}", quote(verb.as_bytes()));
        eprintln!("usage: attn seed <command> (attn seed --help)");
        return Err(Stop(2));
    }
    if verb != "authorization" {
        run.path = format!("seed {verb}");
    }
    verbs::dispatch(run, verb, rest)
}

fn usage_error(name: &str, message: &str) -> Stop {
    eprintln!("seed {name}: {message}");
    eprintln!(
        "usage: attn seed {} (attn seed {name} --help)",
        help_generated::usage(name)
    );
    Stop(2)
}

fn fail(name: &str, message: &str) -> Stop {
    eprintln!("seed {name}: {message}");
    Stop(1)
}

struct Session<'r, 'a> {
    run: &'r mut Run<'a>,
    name: String,
    flags: Flags,
}

impl<'r, 'a> Session<'r, 'a> {
    fn new(run: &'r mut Run<'a>, name: &str) -> Self {
        Self {
            run,
            name: name.to_string(),
            flags: Flags::new(name),
        }
    }

    fn parse(&mut self, args: &[String]) -> Result<Vec<String>, Stop> {
        match self.flags.parse(args) {
            Ok(positionals) => Ok(positionals),
            Err(ParseStop::Help) => {
                print_stdout(help_generated::help(&self.name));
                Err(Stop(0))
            }
            Err(ParseStop::Invalid(message)) => {
                self.run.record_flag_error(&message);
                Err(self.usage(&message))
            }
        }
    }

    fn usage(&self, message: &str) -> Stop {
        usage_error(&self.name, message)
    }

    fn fail(&self, message: &str) -> Stop {
        fail(&self.name, message)
    }

    fn fail_send(&mut self, error: &SendError) -> Stop {
        let label = format!("seed {}", self.name);
        self.run.fail_send(&label, error)
    }

    fn bare_client(&self) -> Client<'a> {
        Client::new(self.run.paths, "", &self.run.terminal)
    }

    fn scope(&mut self) -> Result<(String, String), Stop> {
        if self.flags.caller.is_none() {
            let resolved = self.resolve_caller()?;
            self.flags.caller = Some(resolved);
        }
        Ok(self.flags.caller.clone().expect("caller was just set"))
    }

    fn resolve_caller(&mut self) -> Result<(String, String), Stop> {
        let profile = self.flags.string("profile").trim().to_string();
        let session = self.flags.string("session").trim().to_string();
        if !session.is_empty() {
            return Ok((profile, session));
        }
        let terminal = self.run.terminal.clone();
        if terminal.is_empty() {
            crate::hooks::server_caller_from_env().map_err(|message| self.fail(&message))?;
            return Ok((profile, String::new()));
        }
        let mut message = CallerSessionMessage::new();
        message.caller_id = Some(terminal.clone());
        let reply = self
            .bare_client()
            .call(&message)
            .map_err(|error| self.fail_send(&error))?;
        let found = reply.optional_string("caller_session_id");
        if !found.is_empty() {
            return Ok((profile, found));
        }
        if self.flags.verb.read_only {
            if !profile.is_empty() {
                return Ok((profile, String::new()));
            }
            if let Some(codex_profile) = codex_profile(&terminal) {
                return Ok((codex_profile, String::new()));
            }
        }
        Err(self.fail(&format!(
            "terminal {terminal} is not showing a session; pass --session <id> to identify the caller; --profile <name|id> selects a garden and does not identify a session"
        )))
    }

    fn session_id(&mut self) -> Result<String, Stop> {
        Ok(self.scope()?.1)
    }

    fn client(&mut self) -> Result<Client<'a>, Stop> {
        let (profile, session) = self.scope()?;
        Ok(self.bare_client().with_garden_scope(&profile, &session))
    }

    fn call<M: serde::Serialize>(
        &mut self,
        message: &M,
        field: &str,
        missing: &str,
    ) -> Result<Reply, Stop> {
        let client = self.client()?;
        client
            .call_field(message, field, missing)
            .map_err(|error| self.fail_send(&error))
    }

    fn read_field<T: serde::de::DeserializeOwned>(
        &mut self,
        reply: &Reply,
        field: &str,
    ) -> Result<T, Stop> {
        reply.value(field).map_err(|error| self.fail_send(&error))
    }

    fn text(&mut self) -> Result<String, Stop> {
        let message = self.flags.string("m").to_string();
        if message != "-" {
            return Ok(message);
        }
        let mut raw = Vec::new();
        if let Err(error) = std::io::stdin().read_to_end(&mut raw) {
            return Err(self.fail(&format!(
                "reading stdin: {}",
                crate::paths::os_error_text(&error)
            )));
        }
        let text = String::from_utf8_lossy(&raw).into_owned();
        if text.trim().is_empty() {
            return Err(self.usage("-m - read nothing from stdin; provide nonempty text"));
        }
        Ok(text)
    }

    fn print_json(&mut self, reply: &Reply, path: &[&str]) -> Result<(), Stop> {
        super::print_reply_field(reply, path).map_err(|error| self.fail_send(&error))
    }
}

fn codex_profile(terminal: &str) -> Option<String> {
    let rest = terminal.trim().strip_prefix(CODEX_THREAD_PREFIX)?;
    let index = rest.rfind(':')?;
    if rest[index + 1..].is_empty() {
        return None;
    }
    Some(rest[..index].to_string())
}
