mod help_generated;
mod table_generated;

use super::{Run, Stop, print_compact_line, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use attn::go::tabwriter;
use attn_protocol::session_id::short;
use attn_protocol::{
    TerminalEntry, TerminalListResult, TerminalMoveResult, TerminalOpenResult, TerminalReadResult,
    TerminalRunResult, TerminalWaitResult,
};
use std::fmt::Write as _;
use std::time::{Duration, Instant};

struct Terminal;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &Terminal, args)
}

fn table(entries: &[TerminalEntry]) -> String {
    let mut table = String::from("ID\tNAME\tAGENT\tSTATE\tDESKTOP\tPOSITION\tOPENED BY\n");
    for entry in entries {
        let _ = writeln!(
            table,
            "{}\t{}\t{}\t{}\t{}\t{}\t{}",
            entry.short_id,
            entry.name,
            entry.agent,
            entry.state,
            entry.desktop_label,
            entry.position.as_deref().unwrap_or_default(),
            short(entry.opened_by.as_deref().unwrap_or_default()),
        );
    }
    tabwriter::format(&table)
}

fn moved(result: &TerminalMoveResult) -> String {
    let outcome = if result.unchanged.unwrap_or(false) {
        "already is on"
    } else {
        "moved to"
    };
    format!(
        "{} {outcome} desktop {}\n",
        result.terminal_id, result.desktop_id
    )
}

fn exit_status(
    block: Option<&attn_protocol::TerminalBlock>,
    exited: Option<bool>,
    code: Option<i64>,
) -> Result<i64, String> {
    if let Some(block) = block {
        return block
            .exit_code
            .ok_or_else(|| "command finished without an exit code".to_string());
    }
    if exited.unwrap_or(false) {
        return Ok(code.unwrap_or(0));
    }
    Ok(0)
}

impl Terminal {
    fn print_raw(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        result: &Reply,
        path: &[&str],
    ) -> Result<(), Stop> {
        let raw = result
            .raw_field(path)
            .map_err(|error| run.fail_send(&self.label(cmd.name), &error))?;
        print_compact_line(raw);
        Ok(())
    }

    fn finish(
        &self,
        cmd: &Command,
        block: Option<&attn_protocol::TerminalBlock>,
        exited: Option<bool>,
        code: Option<i64>,
        plain: bool,
    ) -> Result<(), Stop> {
        if plain && let Some(block) = block {
            print_stdout(&format!("{}\n", block.output));
        }
        match exit_status(block, exited, code) {
            Ok(0) => Ok(()),
            Ok(code) => Err(Stop(u8::try_from(code & 0xff).unwrap_or(1))),
            Err(message) => Err(self.fail(cmd.name, &message)),
        }
    }
}

impl Family for Terminal {
    fn group(&self) -> &'static str {
        "terminal"
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

    fn choice_conflict(&self, _cmd: &Command, _arg: &crate::command::Arg) -> String {
        "choose --last or --scrollback".to_string()
    }

    fn fail(&self, _verb: &str, message: &str) -> Stop {
        eprintln!("terminal: {message}");
        Stop(1)
    }

    fn refused_line(&self, _verb: &str, message: &str) -> String {
        format!("terminal: {message}")
    }

    fn before_send(&self, cmd: &Command, parsed: &mut Parsed) -> Result<(), Stop> {
        if matches!(cmd.name, "open" | "run") {
            parsed
                .args
                .insert("wait_for_shell".to_string(), serde_json::Value::Bool(true));
        }
        if cmd.name == "move"
            && !parsed.has("desktop")
            && let Some(only) = parsed.args.remove("terminal")
        {
            parsed.args.insert("desktop".to_string(), only);
        }
        Ok(())
    }

    fn deadline(&self, _cmd: &Command, parsed: &Parsed) -> Option<Instant> {
        let seconds = parsed
            .text("timeout")
            .parse::<f64>()
            .ok()
            .filter(|seconds| *seconds > 0.0)?;
        Some(Instant::now() + Duration::try_from_secs_f64(seconds).ok()?)
    }

    fn present_json(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        _parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        let path: &[&str] = if cmd.name == "ls" {
            &["terminals"]
        } else {
            &[]
        };
        self.print_raw(run, cmd, &result, path)?;
        match cmd.name {
            "run" => {
                let ran: TerminalRunResult = command::decode(run, self, cmd, &result)?;
                self.finish(cmd, ran.block.as_ref(), ran.exited, ran.exit_code, false)
            }
            "wait" => {
                let waited: TerminalWaitResult = command::decode(run, self, cmd, &result)?;
                self.finish(
                    cmd,
                    waited.block.as_ref(),
                    waited.exited,
                    waited.exit_code,
                    false,
                )
            }
            _ => Ok(()),
        }
    }

    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        _parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        match cmd.name {
            "ls" => {
                let listed: TerminalListResult = command::decode(run, self, cmd, &result)?;
                print_stdout(&table(&listed.terminals));
            }
            "open" => {
                let opened: TerminalOpenResult = command::decode(run, self, cmd, &result)?;
                print_stdout(&format!("{}\n", opened.terminal_id));
            }
            "move" => {
                print_stdout(&moved(&command::decode(run, self, cmd, &result)?));
            }
            "read" => {
                let read: TerminalReadResult = command::decode(run, self, cmd, &result)?;
                print_stdout(&format!("{}\n", read.text));
            }
            "run" => {
                let ran: TerminalRunResult = command::decode(run, self, cmd, &result)?;
                return self.finish(cmd, ran.block.as_ref(), ran.exited, ran.exit_code, true);
            }
            "wait" => {
                let waited: TerminalWaitResult = command::decode(run, self, cmd, &result)?;
                return self.finish(
                    cmd,
                    waited.block.as_ref(),
                    waited.exited,
                    waited.exit_code,
                    true,
                );
            }
            _ => {}
        }
        Ok(())
    }
}
