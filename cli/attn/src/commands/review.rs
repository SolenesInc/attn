use super::support::{client, has_help_flag, parse_interspersed, string_flag};
use super::{Run, Stop, USAGE_UNKNOWN_COMMAND, absolute, print_stdout};
use crate::paths::os_error_text;
use attn::go::flag::FlagSet;
use attn::go::quote::quote;
use attn_protocol::{
    ReviewCheckMessage, ReviewCheckResult, ReviewProblem, ReviewStartMessage, ReviewStartResult,
};
use std::fmt::Write as _;

const START_FIELD: &str = "review_start_result";
const CHECK_FIELD: &str = "review_check_result";

pub const HELP: &str = r"usage: attn review <command>

Present a change for review as a review document: markdown with `attn: review`
in its frontmatter that `attn open` shows in a tile. Keep the document out of
the repository's tracked tree: in your seed's artifact folder when you tend a
seed, else in your session's scratch folder.

commands:
  start <document> [--base <commit>]
        snapshot the working tree as head, from the repository you run it in,
        and write frame (brief unless the document already picks one),
        repository, base and head into the document's
        frontmatter. Creates the document when it is missing; leaves the text
        below the frontmatter as written. Run it again before presenting a
        revision: it takes a fresh head and keeps the base. --base defaults to
        the head of the last review the user sent on this document, else the
        document's base; without one the review is a tour of unchanged code.

  check <document>
        list the document's problems, each with its line and the fix. Exits 1
        when there are problems. `attn open` prints the same problems as
        warnings.

The document, below the frontmatter:

  # Title, then a summary
  ## A step              prose that explains one part of the change
  ### path:from-to       a stop: those lines of the file, at head
  - L12: note           an agent note on line 12 (L12-14 for a range)
  - L12!: note          the place to look
  ## Not shown          `- path: why it is left out`, for each changed file
                        no stop shows

Every changed file needs a stop or a Not shown entry.

Frames: `frame:` in the frontmatter picks the frame the review opens in; the
user switches with 1, 2 and 3.
  brief         the document top to bottom
  walkthrough   the stops one at a time, your prose beside the file
  map           a sequence diagram's messages, each beside the code of its stop
A pick the document cannot fill opens in brief, and check says what is
missing: walkthrough needs a stop, map a drawn diagram with a message tied to
a stop.

Diagrams: a mermaid sequenceDiagram ties a message to a stop with
`%% stop: path:from-to` on the line after it. attn draws participants, actors
and messages. A diagram with any other line (loop, alt, Note, activate) shows
as a mermaid figure and is not mapped, and check names the line. To show the
flow before and after the change, open one fence with ```mermaid before and one
with ```mermaid after; the user switches between them with b. A fence with no
side shows with both.

Needs the experimental setting code_review_enabled.
";

fn usage_failure(verb: &str, message: &str) -> Stop {
    eprint!("review {verb}: {message}\n{HELP}");
    Stop(2)
}

fn document_argument(
    run: &mut Run<'_>,
    verb: &str,
    flags: &mut FlagSet<'_>,
    args: &[String],
) -> Result<String, Stop> {
    let positionals = parse_interspersed(run, flags, args)
        .map_err(|error| usage_failure(verb, &error.to_string()))?;
    let [document] = positionals.as_slice() else {
        return Err(usage_failure(verb, "expected one <document> argument"));
    };
    absolute(document.trim()).map_err(|error| {
        eprintln!("review {verb}: resolve path: {}", os_error_text(&error));
        Stop(1)
    })
}

fn start(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let definitions = [string_flag("base")];
    let mut flags = FlagSet::new("review start", &definitions);
    let document = document_argument(run, "start", &mut flags, args)?;
    let directory = absolute(".").map_err(|error| {
        eprintln!(
            "review start: resolve the working directory: {}",
            os_error_text(&error)
        );
        Stop(1)
    })?;
    let base = flags.string("base").trim().to_string();
    let mut message = ReviewStartMessage::new();
    message.document.clone_from(&document);
    message.repository = directory;
    message.base = (!base.is_empty()).then_some(base);
    let label = "review start";
    let reply = client(run)
        .call_field(
            &message,
            START_FIELD,
            "daemon returned no review start result",
        )
        .map_err(|error| run.fail_send(label, &error))?;
    let result: ReviewStartResult = reply
        .value(START_FIELD)
        .map_err(|error| run.fail_send(label, &error))?;
    let mut out = String::new();
    if result.created {
        let _ = writeln!(out, "created {}", result.document);
    }
    match &result.base {
        Some(base) => {
            let _ = writeln!(
                out,
                "head {} over base {} in {}",
                short(&result.head),
                short(base),
                result.repository
            );
        }
        None => {
            let _ = writeln!(
                out,
                "head {} in {}, with no base: a tour of unchanged code",
                short(&result.head),
                result.repository
            );
        }
    }
    for left in &result.left_out {
        let _ = writeln!(out, "left out of head: {}: {}", left.path, left.reason);
    }
    let _ = writeln!(
        out,
        "Write the review below the frontmatter, then run `attn review check {0}` and `attn open {0}`.",
        result.document
    );
    print_stdout(&out);
    Ok(())
}

fn short(commit: &str) -> &str {
    &commit[..commit.len().min(12)]
}

pub fn problem_lines(problems: &[ReviewProblem], prefix: &str) -> String {
    let mut out = String::new();
    for problem in problems {
        if problem.line > 0 {
            let _ = writeln!(out, "{prefix}L{}: {}", problem.line, problem.message);
        } else {
            let _ = writeln!(out, "{prefix}{}", problem.message);
        }
    }
    out
}

fn check(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut flags = FlagSet::new("review check", &[]);
    let document = document_argument(run, "check", &mut flags, args)?;
    let mut message = ReviewCheckMessage::new();
    message.document.clone_from(&document);
    let label = "review check";
    let reply = client(run)
        .call_field(
            &message,
            CHECK_FIELD,
            "daemon returned no review check result",
        )
        .map_err(|error| run.fail_send(label, &error))?;
    let result: ReviewCheckResult = reply
        .value(CHECK_FIELD)
        .map_err(|error| run.fail_send(label, &error))?;
    if result.problems.is_empty() {
        print_stdout(&format!("{document}: no problems\n"));
        return Ok(());
    }
    print_stdout(&problem_lines(&result.problems, ""));
    Err(Stop(1))
}

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let Some(verb) = args.first() else {
        print_stdout(HELP);
        return Ok(());
    };
    if verb == "-h" || verb == "--help" || has_help_flag(&args[1..]) {
        print_stdout(HELP);
        return Ok(());
    }
    super::warn_if_stale(run.paths);
    match verb.as_str() {
        "start" | "check" => {
            run.path = format!("review {verb}");
            if verb == "start" {
                start(run, &args[1..])
            } else {
                check(run, &args[1..])
            }
        }
        _ => {
            run.usage_error = Some(USAGE_UNKNOWN_COMMAND);
            eprint!("review: unknown command {}\n{HELP}", quote(verb.as_bytes()));
            Err(Stop(2))
        }
    }
}
