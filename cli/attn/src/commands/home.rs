use super::support::{client, parse_interspersed, parse_or_exit};
use super::{Run, Stop, USAGE_UNKNOWN_COMMAND, print_json, print_stdout};
use attn::go::flag::{Flag, FlagDefault, FlagError, FlagSet};
use attn::go::json;
use attn::go::quote::quote;
use attn_protocol::{HomeMove, HomeMoveMessage, HomeMoveRollbackMessage, HomeMoveStatusMessage};
use serde::Serialize;
use std::fmt::Write;

const HELP: &str = r"usage: attn home <command>

The home is the machine whose daemon owns the sessions, the Garden and the
settings. A move gives that role to a connected outpost and makes this machine
an outpost of it, keeping every session, terminal and seed. It needs
remote_home_enabled on.

commands:
  move [--check] [--via <ssh-destination>] [--json] <machine>
        move the home to the outpost <machine>, by name or id, after checks
        that it can reach every outpost, is logged in to gh, keeps running
        after logout, and holds the Notebook in sync. Both daemons restart.
        With --check, only run the checks. On Linux, --via is the ssh
        destination by which the new home reaches this machine. When a move
        stopped after it began to commit, run it again to finish.

  status [--json]
        show the last move on this machine and its step.

  rollback [--json]
        undo the last move, on the machine the home moved from. A move that
        has not committed is dropped; a committed move restores both machines
        as they were before it, once the other machine bridges again. Until
        then this machine runs no background jobs or phone link; run rollback
        again to run them without word from a machine that is gone for good.
        On the new home, or once the old home has bridged to the new home,
        rollback refuses: move the home back instead.
";

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let Some(verb) = args.first().map(String::as_str) else {
        print_stdout(HELP);
        return Ok(());
    };
    match verb {
        "-h" | "--help" | "help" => {
            print_stdout(HELP);
            Ok(())
        }
        "move" => {
            run.path = "home move".to_string();
            move_home(run, &args[1..])
        }
        "status" => {
            run.path = "home status".to_string();
            status(run, &args[1..])
        }
        "rollback" => {
            run.path = "home rollback".to_string();
            rollback(run, &args[1..])
        }
        _ => {
            run.usage_error = Some(USAGE_UNKNOWN_COMMAND);
            eprint!("home: unknown command {}\n{HELP}", quote(verb.as_bytes()));
            Err(Stop(2))
        }
    }
}

fn json_flag() -> Flag<'static> {
    Flag {
        name: "json",
        default: FlagDefault::Bool(false),
        usage: "print the move as JSON",
    }
}

fn move_home(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut flags = FlagSet::new(
        "home move",
        &[
            json_flag(),
            Flag {
                name: "check",
                default: FlagDefault::Bool(false),
                usage: "only run the checks",
            },
            Flag {
                name: "via",
                default: FlagDefault::String(""),
                usage: "the ssh destination by which the new home reaches this machine",
            },
        ],
    );
    let positionals = match parse_interspersed(run, &mut flags, args) {
        Ok(positionals) => positionals,
        Err(error) => {
            eprint!("{}", flags.output());
            return Err(Stop(u8::from(error != FlagError::Help) * 2));
        }
    };
    let [machine] = positionals.as_slice() else {
        eprintln!("usage: attn home move [--check] [--via <ssh-destination>] [--json] <machine>");
        return Err(Stop(2));
    };
    let via = flags.string("via").to_string();
    let message = HomeMoveMessage {
        machine: machine.clone(),
        check: flags.bool("check").then_some(true),
        via: (!via.is_empty()).then_some(via),
        ..HomeMoveMessage::new()
    };
    let answer = call(run, &message)?;
    report(&flags, &answer)
}

fn status(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut flags = FlagSet::new("home status", &[json_flag()]);
    parse_or_exit(run, &mut flags, args)?;
    let answer = call(run, &HomeMoveStatusMessage::new())?;
    report(&flags, &answer)
}

fn rollback(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut flags = FlagSet::new("home rollback", &[json_flag()]);
    parse_or_exit(run, &mut flags, args)?;
    let answer = call(run, &HomeMoveRollbackMessage::new())?;
    report(&flags, &answer)
}

fn call<T: Serialize>(run: &mut Run<'_>, message: &T) -> Result<HomeMove, Stop> {
    let path = run.path.clone();
    client(run)
        .call(message)
        .and_then(|reply| reply.value::<HomeMove>("home_move"))
        .map_err(|error| {
            run.record_send_error(&error);
            eprintln!("{path}: {error}");
            Stop(1)
        })
}

fn report(flags: &FlagSet, answer: &HomeMove) -> Result<(), Stop> {
    if flags.bool("json") {
        if let Ok(compact) = json::encode(answer) {
            print_json(&compact);
        }
    } else {
        print_stdout(&describe(answer));
    }
    let failed = answer.error.is_some() || answer.step == "refused";
    if failed { Err(Stop(1)) } else { Ok(()) }
}

fn describe(answer: &HomeMove) -> String {
    let mut out = String::new();
    let from = answer.from.as_deref().unwrap_or("");
    let to = answer.to.as_deref().unwrap_or("");
    let _ = match answer.id.as_deref() {
        Some(id) => writeln!(out, "move {id} from {from} to {to}: {}", answer.step),
        None if answer.step == "none" => writeln!(out, "no move on this machine"),
        None => writeln!(out, "move from {from} to {to}: {}", answer.step),
    };
    for check in &answer.checks {
        let mark = if check.ok { "ok" } else { "refused" };
        let _ = match check.detail.as_deref() {
            Some(detail) => writeln!(out, "  {mark:<8}{}: {detail}", check.name),
            None => writeln!(out, "  {mark:<8}{}", check.name),
        };
    }
    for remap in &answer.remaps {
        let _ = writeln!(out, "  path    {} -> {}", remap.from, remap.to);
    }
    if answer.sleeps == Some(true) {
        let _ = writeln!(out, "  note    while {to} sleeps, the home is down");
    }
    if answer.waits_for_target == Some(true) {
        let _ = writeln!(
            out,
            "  note    this home runs no background jobs and no phone link until {to} is its outpost again; run `attn home rollback` again to stop waiting"
        );
    }
    if let Some(error) = answer.error.as_deref() {
        let _ = writeln!(out, "error: {error}");
    }
    out
}
