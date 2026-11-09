use super::support::{bool_flag, client, current_session_or_exit, parse_interspersed, string_flag};
use super::{Run, Stop, print_json, print_stdout};
use crate::client::{Reply, SendError};
use attn::go::flag::{Flag, FlagDefault, FlagSet};
use attn::go::quote::quote;
use attn::go::tabwriter;
use attn_protocol::{
    PullRequestCreatedMessage, PullRequestForgetMessage, PullRequestUnwatchMessage,
    PullRequestWatchListMessage, PullRequestWatchMessage, QueryMessage, SessionPullRequest,
};
use serde::Deserialize;
use serde_json::value::RawValue;
use std::ffi::OsString;
use std::fmt::Write as _;

const HELP: &str = r"usage: attn pr <command>

commands:
  record <url> [--session <id>]                               record a pull request opened by a session
  ls [--session <id>] [--seed <id>] [--json]                  list a session's pull requests or a seed's watches, with status
  forget <url> [--session <id>]                               stop recording a pull request
  watch <url> [--session <id>] [--seed <id>] [--mode <mode>]  durably watch readiness and feedback
  unwatch <url> [--session <id>] [--seed <id>]                stop a durable watch
  status [<url>] [--session <id>] [--seed <id>] [--json]      show recorded watch/readiness state
  wait-ready <number-or-url> [options]                        wait for the next actionable update

--seed <id> addresses a watch to a seed instead of the session. Updates go to
whoever tends the seed when they arrive, so a handover moves the watch with no
new watch. ls, status and unwatch with --seed act on the seed's watches. watch
refuses an unknown or closed seed. Harvesting or withering the seed stops its
watches and clears their unread updates.

watch reports state changes (ready, changes requested, failed checks, merged,
closed), feedback (comments, review comments, review bodies, reopened review
threads) and monitoring outages. Because agents post as the user, it skips the
feedback the user's GitHub account wrote. Feedback by others, including the
author of a pull request under review, and state changes still reach the agent.
To have watch deliver the user's feedback too, the user runs:
  attn settings set pull_requests.own_comments ring
wait-ready does not skip the user's feedback; --ignore-author skips a login.

wait-ready options:
  --repo [host/]owner/repository  required when the target is a number
  --mode green|codex|formal-review
  --reviewer login                optional only with formal-review
  --timeout duration              default 30m
  --interval duration             default 20s; Codex deadlines wake independently
  --since RFC3339                 report feedback after this time instead of resuming
  --reset                         baseline feedback from the current state
  --ignore-author login           repeatable
  --json                          emit one machine-readable result
";
const EXIT_USAGE: u8 = 2;
const EXIT_ERROR: u8 = 5;
const MODE_GREEN: &str = "green";
const MODE_CODEX: &str = "codex";
const MODE_FORMAL_REVIEW: &str = "formal-review";

fn is_help_arg(arg: &str) -> bool {
    arg == "-h" || arg == "--help"
}

fn is_help_request(args: &[String]) -> bool {
    match args {
        [only] => is_help_arg(only),
        [_, second] => is_help_arg(second),
        _ => false,
    }
}

pub fn asks_for_help(args: &[OsString]) -> bool {
    let args: Vec<String> = args
        .iter()
        .map(|arg| arg.to_string_lossy().into_owned())
        .collect();
    is_help_request(&args)
}

struct Parsed {
    session: String,
    seed: String,
    url: String,
    mode: String,
    reviewer: String,
    json: bool,
}

struct Owner {
    kind: &'static str,
    id: String,
}

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    if is_help_request(args) {
        print_stdout(HELP);
        return Ok(());
    }
    let verb = args.first().map_or("", String::as_str);
    if !matches!(
        verb,
        "record" | "forget" | "ls" | "watch" | "unwatch" | "status"
    ) {
        eprint!("{HELP}");
        return Err(Stop(EXIT_USAGE));
    }
    run.path = format!("pr {verb}");
    let parsed = parse(run, verb, &args[1..])?;
    if !parsed.seed.is_empty() && matches!(verb, "record" | "forget") {
        return Err(usage(
            verb,
            "--seed applies to watch, unwatch, ls and status",
        ));
    }
    match verb {
        "ls" | "status" => list(run, verb, &parsed),
        "watch" => watch(run, &parsed),
        "unwatch" => unwatch(run, &parsed),
        _ => record_or_forget(run, verb, &parsed),
    }
}

fn usage(verb: &str, message: impl std::fmt::Display) -> Stop {
    eprintln!("pr {verb}: {message}");
    Stop(EXIT_USAGE)
}

fn fail(run: &mut Run<'_>, verb: &str, error: &SendError) -> Stop {
    run.refusal.clone_from(&error.refusal);
    eprintln!("pr {verb}: {error}");
    Stop(EXIT_ERROR)
}

fn parse_mode(value: &str) -> Result<String, String> {
    let mode = value.trim().to_lowercase();
    match mode.as_str() {
        "" => Ok(MODE_GREEN.to_string()),
        MODE_GREEN | MODE_CODEX | MODE_FORMAL_REVIEW => Ok(mode),
        _ => Err(format!(
            "mode {} is invalid; use green, codex, or formal-review",
            quote(value.as_bytes())
        )),
    }
}

fn parse(run: &mut Run<'_>, verb: &str, args: &[String]) -> Result<Parsed, Stop> {
    let name = run.path.clone();
    let mut flags = FlagSet::new(
        &name,
        &[
            string_flag("session"),
            string_flag("seed"),
            bool_flag("json"),
            Flag {
                name: "mode",
                default: FlagDefault::String(MODE_GREEN),
                usage: "",
            },
            string_flag("reviewer"),
        ],
    );
    let positionals =
        parse_interspersed(run, &mut flags, args).map_err(|error| usage(verb, error))?;
    let mode = parse_mode(flags.string("mode")).map_err(|message| usage(verb, message))?;
    let mut session = flags.string("session").trim().to_string();
    if session.is_empty() {
        session = current_session_or_exit(run)?;
    }
    if session.is_empty() {
        return Err(usage(
            verb,
            "no session — pass --session <id>, or run inside an attn session",
        ));
    }
    let reviewer = flags.string("reviewer").trim().to_string();
    let url = match verb {
        "ls" => String::new(),
        "status" if positionals.len() > 1 => {
            return Err(usage(verb, "accepts at most one pull request url"));
        }
        "status" => positionals.first().cloned().unwrap_or_default(),
        _ => {
            if verb == "watch" && !reviewer.is_empty() && mode != MODE_FORMAL_REVIEW {
                return Err(usage(
                    verb,
                    "--reviewer is only valid with --mode formal-review",
                ));
            }
            let [url] = positionals.as_slice() else {
                return Err(usage(verb, "needs exactly one pull request url"));
            };
            url.clone()
        }
    };
    Ok(Parsed {
        session,
        seed: flags.string("seed").trim().to_string(),
        url,
        mode,
        reviewer,
        json: flags.bool("json"),
    })
}

fn optional(value: &str) -> Option<String> {
    (!value.is_empty()).then(|| value.to_string())
}

fn watch(run: &mut Run<'_>, parsed: &Parsed) -> Result<(), Stop> {
    let mut message = PullRequestWatchMessage::new();
    message.id.clone_from(&parsed.session);
    message.url.clone_from(&parsed.url);
    message.mode.clone_from(&parsed.mode);
    message.reviewer = optional(&parsed.reviewer);
    message.seed_id = optional(&parsed.seed);
    client(run)
        .call(&message)
        .map_err(|error| fail(run, "watch", &error))?;
    print_stdout(&if parsed.seed.is_empty() {
        format!(
            "watching {} in {} mode for session {}; updates arrive in the agent inbox\n",
            parsed.url, parsed.mode, parsed.session
        )
    } else {
        format!(
            "watching {} in {} mode for seed {}; updates reach the seed's tender\n",
            parsed.url, parsed.mode, parsed.seed
        )
    });
    Ok(())
}

fn unwatch(run: &mut Run<'_>, parsed: &Parsed) -> Result<(), Stop> {
    let mut message = PullRequestUnwatchMessage::new();
    message.id.clone_from(&parsed.session);
    message.url.clone_from(&parsed.url);
    message.seed_id = optional(&parsed.seed);
    client(run)
        .call(&message)
        .map_err(|error| fail(run, "unwatch", &error))?;
    print_stdout(&if parsed.seed.is_empty() {
        format!(
            "stopped watching {} for session {}\n",
            parsed.url, parsed.session
        )
    } else {
        format!("stopped watching {} for seed {}\n", parsed.url, parsed.seed)
    });
    Ok(())
}

fn record_or_forget(run: &mut Run<'_>, verb: &str, parsed: &Parsed) -> Result<(), Stop> {
    let sent = if verb == "forget" {
        let mut message = PullRequestForgetMessage::new();
        message.id.clone_from(&parsed.session);
        message.url.clone_from(&parsed.url);
        client(run).call(&message)
    } else {
        let mut message = PullRequestCreatedMessage::new();
        message.id.clone_from(&parsed.session);
        message.url.clone_from(&parsed.url);
        client(run).call(&message)
    };
    sent.map_err(|error| fail(run, verb, &error))?;
    let done = if verb == "forget" {
        "forgot"
    } else {
        "recorded"
    };
    print_stdout(&format!(
        "{done} {} for session {}\n",
        parsed.url, parsed.session
    ));
    Ok(())
}

#[derive(Deserialize)]
struct SessionEntries {
    id: String,
    #[serde(default)]
    pull_requests: Option<Vec<Box<RawValue>>>,
}

fn raw_entries(reply: &Reply, field: &str) -> Result<Vec<Box<RawValue>>, SendError> {
    let entries: Option<Vec<Box<RawValue>>> = reply.value(field)?;
    Ok(entries.unwrap_or_default())
}

fn entries_to_list(
    run: &mut Run<'_>,
    verb: &str,
    parsed: &Parsed,
) -> Result<(Vec<Box<RawValue>>, Owner), Stop> {
    if !parsed.seed.is_empty() {
        let mut message = PullRequestWatchListMessage::new();
        message.id.clone_from(&parsed.session);
        message.seed_id.clone_from(&parsed.seed);
        let entries = client(run)
            .call(&message)
            .and_then(|reply| raw_entries(&reply, "seed_pull_requests"))
            .map_err(|error| fail(run, verb, &error))?;
        let owner = Owner {
            kind: "seed",
            id: parsed.seed.clone(),
        };
        return Ok((entries, owner));
    }
    let sessions: Option<Vec<SessionEntries>> = client(run)
        .call(&QueryMessage::new())
        .and_then(|reply| reply.value("sessions"))
        .map_err(|error| fail(run, verb, &error))?;
    let Some(session) = sessions
        .unwrap_or_default()
        .into_iter()
        .find(|session| session.id == parsed.session)
    else {
        eprintln!("pr {verb}: unknown session {}", parsed.session);
        return Err(Stop(EXIT_ERROR));
    };
    let owner = Owner {
        kind: "session",
        id: session.id,
    };
    Ok((session.pull_requests.unwrap_or_default(), owner))
}

fn list(run: &mut Run<'_>, verb: &str, parsed: &Parsed) -> Result<(), Stop> {
    let (entries, owner) = entries_to_list(run, verb, parsed)?;
    let watches_only = verb == "status";
    let mut kept = Vec::with_capacity(entries.len());
    for raw in entries {
        let entry: SessionPullRequest = serde_json::from_str(raw.get()).map_err(|error| {
            fail(
                run,
                verb,
                &SendError::plain(format!("receive response: {error}")),
            )
        })?;
        let watched = entry.watching == Some(true);
        if (watches_only && !watched) || (!parsed.url.is_empty() && entry.url != parsed.url) {
            continue;
        }
        kept.push((raw, entry));
    }
    if parsed.json {
        let items: Vec<&str> = kept.iter().map(|(raw, _)| raw.get()).collect();
        print_json(format!("[{}]", items.join(",")).as_bytes());
        return Ok(());
    }
    if kept.is_empty() {
        print_stdout(&if watches_only || owner.kind == "seed" {
            format!("{} {} has no watched pull requests\n", owner.kind, owner.id)
        } else {
            format!("session {} has opened no pull requests\n", owner.id)
        });
        return Ok(());
    }
    let mut table = String::from("PULL REQUEST\tSTATE\tMODE\tREADINESS\tHEALTH\tURL\n");
    for (_, entry) in &kept {
        let _ = writeln!(
            table,
            "{}#{}\t{}\t{}\t{}\t{}\t{}",
            entry.repository,
            entry.number,
            entry.state,
            or_dash(entry.watch_mode.as_deref()),
            or_dash(entry.readiness_state.as_deref()),
            or_dash(entry.watch_health.as_deref()),
            entry.url
        );
    }
    print_stdout(&tabwriter::format(&table));
    Ok(())
}

fn or_dash(value: Option<&str>) -> &str {
    match value {
        Some(text) if !text.trim().is_empty() => text,
        _ => "-",
    }
}
