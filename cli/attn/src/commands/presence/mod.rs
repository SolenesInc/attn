mod help_generated;
mod table_generated;

use super::{Run, Stop, print_json, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use attn_protocol::PresenceResult;
use serde_json::Value as Json;

struct Presence;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &Presence, args)
}

const NOT_IN_A_SESSION: &[u8] = br#"{"session_id":"","seed_ids":[]}"#;

fn env(name: &str) -> String {
    std::env::var(name).unwrap_or_default()
}

fn lines_or_exit(ids: &[String]) -> Result<(), Stop> {
    let ids: Vec<&String> = ids.iter().filter(|id| !id.is_empty()).collect();
    if ids.is_empty() {
        return Err(Stop(1));
    }
    for id in ids {
        print_stdout(&format!("{id}\n"));
    }
    Ok(())
}

impl Family for Presence {
    fn group(&self) -> &'static str {
        "presence"
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
        format!("attn: resolve the session's seeds: {message}")
    }

    fn before_send(&self, cmd: &Command, parsed: &mut Parsed) -> Result<(), Stop> {
        let (session_id, seed_ids) = (parsed.on("session_id"), parsed.on("seed_ids"));
        if session_id && seed_ids {
            return Err(self.usage_error(cmd.name, "pass --session-id or --seed-ids, not both"));
        }
        let terminal = crate::hooks::terminal_from_env(&env);
        let terminal = terminal.trim();
        if terminal.is_empty()
            && let Err(message) = crate::hooks::server_caller_from_env()
        {
            eprintln!("attn: resolve current session: {message}");
            return Err(Stop(1));
        }
        if env("ATTN_INSIDE_APP") != "1" && !terminal.starts_with("server-env:") {
            if parsed.json {
                print_json(NOT_IN_A_SESSION);
            } else if !session_id && !seed_ids {
                print_stdout("not running inside attn\n");
            }
            return Err(Stop(1));
        }
        if terminal.is_empty() {
            if parsed.json {
                print_json(NOT_IN_A_SESSION);
                return Err(Stop(0));
            }
            if session_id || seed_ids {
                return Err(Stop(1));
            }
            print_stdout("running inside attn\n");
            return Err(Stop(0));
        }
        if !parsed.json && !seed_ids {
            parsed
                .args
                .insert("session_only".to_string(), Json::from(true));
        }
        Ok(())
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
        let presence: PresenceResult = command::decode(run, self, cmd, &result)?;
        if parsed.on("session_id") {
            return lines_or_exit(&[presence.session_id]);
        }
        if parsed.on("seed_ids") {
            return lines_or_exit(&presence.seed_ids);
        }
        if presence.session_id.is_empty() {
            print_stdout("running inside attn\n");
        } else {
            print_stdout(&format!(
                "running inside attn (session {})\n",
                presence.session_id
            ));
        }
        Ok(())
    }
}
