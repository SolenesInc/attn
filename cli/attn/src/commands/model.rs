use super::send::{failed, read_sender_token, usage};
use super::support::{bool_flag, client, has_help_flag, parse_flags};
use super::{Run, Stop, print_compact_line, print_stdout};
use attn::go::flag::FlagSet;
use attn_protocol::{SenderModelCallMessage, SenderModelCallResult};
use serde::Deserialize;
use std::io::{self, Read};

const RESULT_FIELD: &str = "sender_model_call_result";

pub const HELP: &str = r#"usage: attn model call --token-stdin

Call a model through attn, as a program the user paired with attn send pair.
attn runs your instructions and input unchanged on a harness's model, with no
tools and no memory of other calls, and prints the model's answer. It needs
headless tasks on.

  --token-stdin  read the paired sender's token from the first line of stdin
                 and one JSON request from the rest

The request:

  {"purpose": "changelog-summary",
   "system": "Summarize the text in one line.",
   "input": "...",
   "limit_ms": 20000}

  purpose   a short label for the call, shown in attn's log
  system    the instructions: the model's system prompt
  input     the text the model answers
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
"#;

const USAGE: &str = "usage: attn model call --token-stdin";

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
