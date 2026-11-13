use super::send::{failed, read_sender_token, usage};
use super::support::{bool_flag, client, has_help_flag, parse_flags, string_flag};
use super::{Run, Stop, print_compact_line, print_stdout};
use attn::go::flag::{Flag, FlagDefault, FlagSet};
use attn_protocol::{SenderRefineMessage, SenderRefineResult};
use std::io::{self, Read};

const RESULT_FIELD: &str = "sender_refine_result";

pub const HELP: &str = r"usage: attn refine --token-stdin [--duration-ms <n>] [--format plain|markdown] [--json]

Clean up a dictated transcript with attn's dictation refinement, as a program
the user paired with attn send pair. The refinement runs tool-free on the
light model of the harness the dictation.refinement_agent setting names
(claude when empty). It needs headless tasks on.

  --token-stdin      read the paired sender's token from the first line of
                     stdin and the raw transcript from the rest
  --duration-ms <n>  how long the recording was, in milliseconds. attn gives
                     the refinement 15 s plus a quarter of it. Defaults to 0
  --format <format>  where the text goes: plain for fields and terminals,
                     markdown for editors that take paragraphs and lists.
                     Defaults to plain
  --json             print one JSON object: profile_id, success, then
                     refined_text, or error and, when the refinement ran past
                     attn's limit, limit_ms

The refined text goes to stdout and the command exits 0. When the refinement
fails or runs past its limit, the command prints attn's error, exits 1, and
the caller keeps the raw transcript. A token attn does not know is refused as
attn send refuses it, on stderr with exit 1. Stopping the command cancels the
refinement.
";

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    const USAGE: &str =
        "usage: attn refine --token-stdin [--duration-ms <n>] [--format plain|markdown] [--json]";
    if has_help_flag(args) {
        print_stdout(HELP);
        return Ok(());
    }
    let mut flags = FlagSet::new(
        "refine",
        &[
            bool_flag("token-stdin"),
            bool_flag("json"),
            string_flag("format"),
            Flag {
                name: "duration-ms",
                default: FlagDefault::Int(0),
                usage: "",
            },
        ],
    );
    let parsed = parse_flags(run, &mut flags, args);
    if parsed.is_err() || !flags.args().is_empty() {
        return Err(usage(USAGE));
    }
    if !flags.bool("token-stdin") {
        return Err(usage(
            "refine: --token-stdin is required; pipe in the token attn send pair printed, then the transcript",
        ));
    }
    let token = read_sender_token("refine")?;
    let mut raw_text = String::new();
    if let Err(error) = io::stdin().lock().read_to_string(&mut raw_text) {
        eprintln!("refine: read the transcript from stdin: {error}");
        return Err(Stop(2));
    }
    let mut message = SenderRefineMessage::new();
    message.sender_token = token;
    message.raw_text = raw_text;
    message.recording_duration_ms = flags.int("duration-ms");
    message.format = match flags.string("format") {
        "" => "plain".to_string(),
        format => format.to_string(),
    };
    let reply = client(run)
        .call_field(&message, RESULT_FIELD, "daemon returned no refinement")
        .map_err(|error| failed(run, "refine", &error))?;
    let result: SenderRefineResult = reply
        .value(RESULT_FIELD)
        .map_err(|error| failed(run, "refine", &error))?;
    if flags.bool("json") {
        let raw = reply
            .raw_field(&[RESULT_FIELD])
            .map_err(|error| failed(run, "refine", &error))?;
        print_compact_line(raw);
    } else if let Some(text) = &result.refined_text {
        print_compact_line(text);
    }
    if result.success {
        return Ok(());
    }
    if !flags.bool("json") {
        eprintln!("refine: {}", result.error.unwrap_or_default());
    }
    Err(Stop(1))
}
