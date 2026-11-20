use super::send::{failed, read_sender_token, usage};
use super::support::{bool_flag, client, has_help_flag, or_dash, parse_flags, string_flag};
use super::{Run, Stop, print_compact_line, print_stdout};
use attn::go::flag::FlagSet;
use attn::go::tabwriter;
use attn_protocol::{
    ModelCall, ModelCallLogMessage, SenderModelCallMessage, SenderModelCallResult,
};
use serde::Deserialize;
use std::fmt::Write as _;
use std::io::{self, Read};

const RESULT_FIELD: &str = "sender_model_call_result";

pub const HELP: &str = r#"usage: attn model call --token-stdin
       attn model log [--sender <name>] [--profile <name|id>] [--json]

call
Call a model through attn, as a program the user paired with attn send pair.
attn runs your instructions and input unchanged on a harness's model, with no
tool that reaches outside the call and no memory of other calls, and prints
the model's answer. It needs headless tasks on.

  --token-stdin  read the paired sender's token from the first line of stdin
                 and one JSON request from the rest

The request:

  {"purpose": "changelog-summary",
   "system": "Summarize the text in one line.",
   "input": "...",
   "limit_ms": 20000}

  purpose   a short label for the call, shown in attn's log
  system    the instructions: the model's system prompt
  input     the text the model answers. Codex takes at most 1,048,576
            characters
  limit_ms  how long to wait for the answer, in milliseconds. Required
  harness   optional: claude or codex. Default: the model_calls.agent setting,
            or the harness for new sessions when that is empty
  tier      optional: light, standard or deep, resolved through the model
            tiers the user keeps for the harness
  model     optional: a model id. Name a tier or a model, not both. Default:
            the model_calls.model setting, or the standard tier when empty

The answer is one JSON line on stdout with exit 0:

  {"harness":"claude","model":"sonnet","text":"...","model_ms":1180}

model_ms is the model's own time, absent when the harness does not report it.
When the call runs and fails, or passes limit_ms, the line has error instead
of text, and limit_ms when the limit passed; the command exits 1. A request
attn refuses before it runs, such as an unknown tier or a harness that is not
installed, prints one line on stderr and exits 1. A token attn does not know is
refused as attn send refuses it. Stopping the command ends the call.

log
List the model calls the profile's paired senders made in the last 30 days,
newest first: when each arrived, the sender, its purpose, the harness and
model, how long the call took and the model's own part of it, the tokens in
and out, the cost, and how it ended: answered, failed, limit or stopped. attn
keeps no instructions, input or answer, so the log has none.

  --sender <name>       only the calls of the paired sender with this name
  --profile <name|id>   the profile whose calls to list; the session's profile
                        inside attn, or the only profile
  --json                one JSON array of the calls, with took_ms, model_ms
                        (absent when the harness did not report it),
                        tokens_in, tokens_out and cost_usd
"#;

const USAGE: &str = "usage: attn model call --token-stdin
       attn model log [--sender <name>] [--profile <name|id>] [--json]";

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Request {
    purpose: String,
    system: String,
    input: String,
    limit_ms: i64,
    #[serde(default)]
    harness: Option<String>,
    #[serde(default)]
    tier: Option<String>,
    #[serde(default)]
    model: Option<String>,
}

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    if args.is_empty() || has_help_flag(args) {
        print_stdout(HELP);
        return Ok(());
    }
    match args[0].as_str() {
        "call" => {
            run.path = "model call".to_string();
            call(run, &args[1..])
        }
        "log" => {
            run.path = "model log".to_string();
            log(run, &args[1..])
        }
        other => {
            eprintln!("model: unknown command {other:?}");
            Err(usage(USAGE))
        }
    }
}

fn call(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut flags = FlagSet::new("model call", &[bool_flag("token-stdin")]);
    let parsed = parse_flags(run, &mut flags, args);
    if parsed.is_err() || !flags.args().is_empty() {
        return Err(usage(USAGE));
    }
    if !flags.bool("token-stdin") {
        return Err(usage(
            "model call: --token-stdin is required; pipe in the token attn send pair printed, then the JSON request",
        ));
    }
    let token = read_sender_token("model call")?;
    let mut body = String::new();
    if let Err(error) = io::stdin().lock().read_to_string(&mut body) {
        eprintln!("model call: read the request from stdin: {error}");
        return Err(Stop(2));
    }
    let request: Request = match serde_json::from_str(&body) {
        Ok(request) => request,
        Err(error) => {
            eprintln!("model call: the request after the token line is not valid: {error}");
            return Err(Stop(2));
        }
    };
    let mut message = SenderModelCallMessage::new();
    message.sender_token = token;
    message.purpose = request.purpose;
    message.system = request.system;
    message.input = request.input;
    message.limit_ms = request.limit_ms;
    message.harness = request.harness;
    message.tier = request.tier;
    message.model = request.model;
    let reply = client(run)
        .call_field(
            &message,
            RESULT_FIELD,
            "daemon returned no model call result",
        )
        .map_err(|error| failed(run, "model call", &error))?;
    let result: SenderModelCallResult = reply
        .value(RESULT_FIELD)
        .map_err(|error| failed(run, "model call", &error))?;
    let raw = reply
        .raw_field(&[RESULT_FIELD])
        .map_err(|error| failed(run, "model call", &error))?;
    print_compact_line(raw);
    if result.error.is_some() {
        return Err(Stop(1));
    }
    Ok(())
}

fn log(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut flags = FlagSet::new(
        "model log",
        &[
            bool_flag("json"),
            string_flag("sender"),
            string_flag("profile"),
        ],
    );
    let parsed = parse_flags(run, &mut flags, args);
    if parsed.is_err() || !flags.args().is_empty() {
        return Err(usage(
            "usage: attn model log [--sender <name>] [--profile <name|id>] [--json]",
        ));
    }
    let mut message = ModelCallLogMessage::new();
    message.profile_id = Some(flags.string("profile").trim().to_string());
    message.sender = Some(flags.string("sender").trim().to_string());
    let reply = client(run)
        .call(&message)
        .map_err(|error| failed(run, "model log", &error))?;
    if flags.bool("json") {
        let raw = reply
            .raw_field(&["model_calls"])
            .map_err(|error| failed(run, "model log", &error))?;
        print_compact_line(if raw == "null" { "[]" } else { raw });
        return Ok(());
    }
    let calls: Vec<ModelCall> = reply
        .value::<Option<Vec<ModelCall>>>("model_calls")
        .map_err(|error| failed(run, "model log", &error))?
        .unwrap_or_default();
    if calls.is_empty() {
        print_stdout("No model calls in the last 30 days.\n");
        return Ok(());
    }
    let mut table = String::from(
        "TIME\tSENDER\tPURPOSE\tHARNESS\tMODEL\tTOOK\tMODEL TIME\tTOKENS IN\tTOKENS OUT\tCOST\tOUTCOME\n",
    );
    for call in &calls {
        let _ = writeln!(
            table,
            "{}\t{}\t{}\t{}\t{}\t{}\t{}\t{}\t{}\t${:.4}\t{}",
            call.at,
            call.sender,
            or_dash(&call.purpose),
            or_dash(&call.harness),
            or_dash(&call.model),
            seconds(call.took_ms),
            call.model_ms.map_or_else(|| "-".to_string(), seconds),
            call.tokens_in,
            call.tokens_out,
            call.cost_usd,
            call.outcome,
        );
    }
    print_stdout(&tabwriter::format(&table));
    Ok(())
}

fn seconds(ms: i64) -> String {
    format!("{}.{}s", ms / 1000, ms % 1000 / 100)
}
