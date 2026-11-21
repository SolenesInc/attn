use super::checks::{render_move, report_evidence};
use super::render::{self, ListView, seed_line};
use super::{Stop, fail, help_generated, usage_error};
use crate::client::Reply;
use crate::command::{self, Arg, Command, Family, Parsed};
use crate::commands::{Run, absolute, print_json, print_stdout};
use attn::garden::plot::{Check, parse_plot_spec};
use attn::garden::section::section;
use attn::go::quote::quote;
use attn_protocol::go_values::NOTE_KIND_HANDOFF;
use attn_protocol::go_values::prompts::AUTHORING_AGENT_SEED_GUIDE;
use attn_protocol::{
    SeedArtifactTransferResult, SeedAuthorizationResult, SeedCheckResult, SeedEditResult,
    SeedExportResult, SeedLinkResult, SeedListResult, SeedNoteResult, SeedNotesResult,
    SeedPlantResult, SeedPlotResult, SeedReadyResult, SeedReminderResult, SeedReviewResult,
    SeedRollbackResult, SeedSearchResult, SeedSendToChiefResult, SeedShowResult, SeedStandbyResult,
    SeedTransitionResult, SeedWatchResult,
};
use serde::Serialize;
use serde_json::{Value as Json, json};
use std::fmt::Write as _;
use std::path::Path;

pub struct Seed;

fn checks_json(checks: &[Check]) -> Json {
    Json::from(
        checks
            .iter()
            .map(|check| {
                let mut definition = json!({"id": check.id, "text": check.text});
                if check.shared {
                    definition["shared"] = Json::from(true);
                }
                definition
            })
            .collect::<Vec<_>>(),
    )
}

fn outlive_close_of_own_session() {
    unsafe {
        libc::signal(libc::SIGTERM, libc::SIG_IGN);
        libc::signal(libc::SIGHUP, libc::SIG_IGN);
    }
}

fn resolve(parsed: &mut Parsed, name: &str, label: &str) -> Result<(), String> {
    let value = parsed.text(name).to_string();
    let resolved = absolute(&value)
        .map_err(|error| format!("{label}: {}", crate::paths::os_error_text(&error)))?;
    parsed.args.insert(name.to_string(), Json::from(resolved));
    Ok(())
}

impl Family for Seed {
    fn group(&self) -> &'static str {
        "seed"
    }

    fn flags(&self) -> &'static [&'static str] {
        super::table_generated::FLAGS
    }

    fn commands(&self) -> &'static [Command] {
        super::table_generated::COMMANDS
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

    fn answered_here(&self, cmd: &Command) -> Option<&'static str> {
        (cmd.name == "guide").then_some(AUTHORING_AGENT_SEED_GUIDE)
    }

    fn decode_file(&self, _cmd: &Command, _arg: &Arg, bytes: &[u8]) -> Result<Json, String> {
        let spec = parse_plot_spec(bytes)?;
        let children: Vec<Json> = spec
            .children
            .iter()
            .map(|child| {
                json!({"title": child.title, "body": child.body, "checks": checks_json(&child.checks), "after": child.after})
            })
            .collect();
        Ok(json!({
            "title": spec.title,
            "body": spec.body,
            "part_of": spec.part_of,
            "discovered_from": spec.discovered_from,
            "checks": checks_json(&spec.checks),
            "children": children,
        }))
    }

    fn read_stdin_error(&self, _arg: &Arg) -> &'static str {
        "read the plot from stdin"
    }

    fn choice_conflict(&self, _cmd: &Command, _arg: &Arg) -> String {
        "local attach requires exactly one of --move or --copy; --move is recommended".to_string()
    }

    fn before_send(&self, cmd: &Command, parsed: &mut Parsed) -> Result<(), Stop> {
        match cmd.name {
            "check report" => {
                report_evidence(parsed).map_err(|message| usage_error(cmd.name, &message))
            }
            "standby" => {
                if !parsed.on("clear") && !parsed.on("stay") {
                    outlive_close_of_own_session();
                }
                Ok(())
            }
            "attach" => {
                let managed = parsed.has("mode")
                    && !parsed.text("path").is_empty()
                    && parsed.text("repo").trim().is_empty()
                    && parsed.text("notebook").trim().is_empty()
                    && parsed.text("url").trim().is_empty();
                if managed {
                    resolve(parsed, "path", "resolve source path")
                        .map_err(|message| fail(cmd.name, &message))?;
                }
                Ok(())
            }
            "detach" if !parsed.text("to").is_empty() => {
                resolve(parsed, "to", "resolve detach destination")
                    .map_err(|message| fail(cmd.name, &message))
            }
            _ => Ok(()),
        }
    }

    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        let out = match cmd.name {
            "standby" => return standby(run, cmd, parsed, &result),
            "export" => return export(run, cmd, parsed, &result),
            "prime" => render::prime(&command::decode::<SeedReadyResult>(
                run, &Seed, cmd, &result,
            )?),
            _ if parsed.json => return command::print_result(run, &Seed, cmd, &result),
            "search" => render::search(
                parsed.text("words"),
                &command::decode::<SeedSearchResult>(run, &Seed, cmd, &result)?,
            ),
            "plant" => plant(&command::decode(run, &Seed, cmd, &result)?),
            "plot" => plot(&command::decode(run, &Seed, cmd, &result)?),
            "link" | "unlink" => link(cmd, parsed, &command::decode(run, &Seed, cmd, &result)?),
            "ready" => render::ready(&command::decode(run, &Seed, cmd, &result)?),
            "ls" => render::list(
                &command::decode::<SeedListResult>(run, &Seed, cmd, &result)?,
                &ListView {
                    plot_id: parsed.text("plot").trim().to_string(),
                    flat: parsed.on("flat"),
                    stale: parsed.on("stale"),
                    standby: parsed.on("standby"),
                },
            ),
            "show" => show(parsed, &command::decode(run, &Seed, cmd, &result)?)?,
            "notes" => notes(parsed, &command::decode(run, &Seed, cmd, &result)?),
            "note" => note(parsed, &command::decode(run, &Seed, cmd, &result)?),
            "tend" | "park" | "replant" | "wither" | "harvest" => render::transition(
                &command::decode::<SeedTransitionResult>(run, &Seed, cmd, &result)?,
                parsed.on("when_merged") && parsed.on("clear"),
            ),
            "send-to-chief" => format!(
                "{} sent to Chief ({})\n",
                parsed.text("seed").trim(),
                command::decode::<SeedSendToChiefResult>(run, &Seed, cmd, &result)?.detail
            ),
            "watch" | "unwatch" => render::watch_result(
                &command::decode::<SeedWatchResult>(run, &Seed, cmd, &result)?,
                cmd.name == "unwatch",
            ),
            "remind" => remind(&command::decode(run, &Seed, cmd, &result)?),
            "edit" => {
                let result: SeedEditResult = command::decode(run, &Seed, cmd, &result)?;
                format!(
                    "updated {}: body v{}, {} → {} bytes\n",
                    result.seed.id, result.body_version, result.previous_bytes, result.new_bytes
                )
            }
            "rollback" => {
                let result: SeedRollbackResult = command::decode(run, &Seed, cmd, &result)?;
                format!(
                    "rolled {} back to v{} as v{}: {} → {} bytes\n",
                    result.seed.id,
                    result.restored_version,
                    result.body_version,
                    result.previous_bytes,
                    result.new_bytes
                )
            }
            "authorization" => {
                let result: SeedAuthorizationResult = command::decode(run, &Seed, cmd, &result)?;
                let clear = parsed.on("clear");
                let changing = !parsed.text("words").trim().is_empty() || clear;
                format!(
                    "{}\n",
                    render::authorization_reply(&result, changing, clear)
                )
            }
            "attach" | "detach" if !result.present("operation") => {
                association(cmd, parsed, &command::decode(run, &Seed, cmd, &result)?)
            }
            "attach" | "detach" => {
                transfer(cmd, parsed, &command::decode(run, &Seed, cmd, &result)?)
            }
            name if name.starts_with("check ") => render_move(
                &command::decode::<SeedCheckResult>(run, &Seed, cmd, &result)?,
                parsed.on("ring"),
            ),
            name if name.starts_with("review ") => {
                let result: SeedReviewResult = command::decode(run, &Seed, cmd, &result)?;
                match &result.review {
                    None => format!("{} seeds need review\n", result.candidate_count),
                    Some(review) => render::review(review, name == "review show"),
                }
            }
            other => unreachable!("seed {other} has no renderer"),
        };
        print_stdout(&out);
        Ok(())
    }
}

fn plant(result: &SeedPlantResult) -> String {
    let mut out = format!(
        "{}\n",
        seed_line(&result.id, &result.step_slug, &result.title)
    );
    for check in result.checks.as_deref().unwrap_or_default() {
        if check.from.as_deref().unwrap_or_default().is_empty() {
            let _ = writeln!(out, "  {}  {}", check.id, check.text);
        }
    }
    render::similar(
        &mut out,
        result.similar.as_deref().unwrap_or_default(),
        false,
    );
    out
}

fn plot(result: &SeedPlotResult) -> String {
    let crown = &result.crown;
    let mut out = format!("{}\n", seed_line(&crown.id, &crown.step_slug, &crown.title));
    let mut rows = String::new();
    for child in &result.children {
        let _ = writeln!(rows, "  {}\t{}\t{}", child.id, child.step_slug, child.title);
    }
    out.push_str(&attn::go::tabwriter::format(&rows));
    render::similar(&mut out, &result.similar, true);
    out
}

fn link(cmd: &Command, parsed: &Parsed, result: &SeedLinkResult) -> String {
    let (seed, kind, to) = (parsed.text("a"), parsed.text("relation"), parsed.text("b"));
    if cmd.name == "unlink" {
        format!("{seed} no longer {kind} {to}\n")
    } else if !result.changed {
        format!("{seed} already {kind} {to}\n")
    } else {
        format!("{seed} {kind} {to}\n")
    }
}

fn show(parsed: &Parsed, result: &SeedShowResult) -> Result<String, Stop> {
    let seed = &result.seed;
    if parsed.has("section") {
        let part = section(&seed.body, parsed.text("section"))
            .map_err(|error| fail("show", &format!("{}: {error}", seed.id)))?;
        return Ok(format!("{}  {}\n\n{part}\n", seed.id, seed.title));
    }
    let mut out = String::new();
    if let Some(window) = &result.since {
        match &window.expired {
            None => {
                render::since(&mut out, window.after, &window.entries);
                render::bookmark_line(&mut out, "show", &seed.id, result.bookmark, "");
                return Ok(out);
            }
            Some(expired) => render::expired_bookmark(&mut out, window.after, expired),
        }
    }
    out.push_str(&render::show(result, result.bookmark));
    Ok(out)
}

fn notes(parsed: &Parsed, result: &SeedNotesResult) -> String {
    let id = parsed.text("seed");
    let kind = parsed.text("kind").trim();
    let since = parsed.int("since");
    let mut out = String::new();
    if let Some(after) = since
        && result.expired.is_none()
    {
        render::since(&mut out, after, &result.notes);
        render::bookmark_line(&mut out, "notes", id, result.bookmark, kind);
        return out;
    }
    if let Some(expired) = &result.expired {
        render::expired_bookmark(&mut out, since.unwrap_or_default(), expired);
    }
    if !result.notes.is_empty() {
        render::notes(
            &mut out,
            &result.notes,
            id,
            result.total - render::count(result.notes.len()),
        );
    } else if !kind.is_empty() {
        let _ = writeln!(out, "no {kind} entries on this seed's log");
    } else {
        let _ = writeln!(
            out,
            "nothing on this seed's log yet — `attn seed note {id} -m \"what happened\"` starts it"
        );
    }
    out.push('\n');
    render::bookmark_line(&mut out, "notes", id, result.bookmark, kind);
    out
}

fn note(parsed: &Parsed, result: &SeedNoteResult) -> String {
    let mut out = if result.kind == NOTE_KIND_HANDOFF {
        format!(
            "handoff left on {} — whoever tends it next reads this first\n",
            result.seed_id
        )
    } else {
        format!("noted on {}\n", result.seed_id)
    };
    if parsed.on("ring") {
        render::plural_rang(&mut out, &result.seed_id, result.ring_recipients.as_deref());
    }
    out
}

fn remind(result: &SeedReminderResult) -> String {
    let line = if let Some(reminder) = &result.seed.reminder {
        format!(
            "{} reminder {}",
            result.seed.id,
            render::reminder_line(reminder)
        )
    } else if result.changed {
        format!("{} reminder cleared.", result.seed.id)
    } else {
        format!("{} has no reminder.", result.seed.id)
    };
    format!("{line}\n")
}

fn standby(run: &mut Run<'_>, cmd: &Command, parsed: &Parsed, result: &Reply) -> Result<(), Stop> {
    let raw: SeedStandbyResult = command::decode(run, &Seed, cmd, result)?;
    if let Some(close) = raw
        .close
        .as_ref()
        .filter(|c| c.pending_compact != Some(true))
    {
        run.closed_session = Some(close.target_session_id.clone());
    }
    if parsed.json {
        return command::print_result(run, &Seed, cmd, result);
    }
    let mut out = String::new();
    if let Some(standby) = &raw.seed.standby {
        let _ = writeln!(
            out,
            "{} on standby: {}",
            raw.seed.id,
            quote(standby.note.as_bytes())
        );
    } else if raw.changed {
        let _ = writeln!(out, "{} is off standby.", raw.seed.id);
    } else {
        let _ = writeln!(out, "{} was not on standby.", raw.seed.id);
    }
    let timed = parsed.has("in") || parsed.has("at");
    if let Some(reminder) = raw.seed.reminder.as_ref().filter(|_| timed) {
        let _ = writeln!(
            out,
            "{} reminder {}",
            raw.seed.id,
            render::reminder_line(reminder)
        );
    }
    if let Some(close) = &raw.close {
        render::agent_close(&mut out, close);
    }
    print_stdout(&out);
    Ok(())
}

#[derive(Serialize)]
struct ExportedFile<'a> {
    seed_id: &'a str,
    path: &'a str,
    bytes: i64,
}

fn write_file(path: &str, contents: &[u8]) -> std::io::Result<()> {
    use std::io::Write as _;
    use std::os::unix::fs::OpenOptionsExt;
    let mut file = std::fs::OpenOptions::new()
        .write(true)
        .create(true)
        .truncate(true)
        .mode(0o644)
        .open(path)?;
    file.write_all(contents)
}

fn export(run: &mut Run<'_>, cmd: &Command, parsed: &Parsed, result: &Reply) -> Result<(), Stop> {
    let exported: SeedExportResult = command::decode(run, &Seed, cmd, result)?;
    let text = exported.text.unwrap_or_default();
    let out = parsed.text("out").trim().to_string();
    if out == "-" {
        print_stdout(&text);
        return Ok(());
    }
    let path = if out.is_empty() {
        format!("{}.md", exported.seed_id)
    } else {
        out
    };
    if let Err(error) = write_file(&path, text.as_bytes()) {
        return Err(fail(
            "export",
            &format!(
                "writing {path}: open {path}: {}",
                crate::paths::os_error_text(&error)
            ),
        ));
    }
    let resolved = absolute(&path).unwrap_or_else(|_| path.clone());
    if parsed.json {
        let encoded = attn::go::json::encode(&ExportedFile {
            seed_id: &exported.seed_id,
            path: &resolved,
            bytes: exported.bytes,
        })
        .expect("a plain struct always encodes");
        print_json(&encoded);
        return Ok(());
    }
    print_stdout(&format!(
        "wrote {resolved}; edit it with any tool, keep its first line, then: attn seed edit {} -m - < {resolved}\n",
        exported.seed_id
    ));
    Ok(())
}

fn association(cmd: &Command, parsed: &Parsed, result: &SeedNoteResult) -> String {
    let verb = if cmd.name == "detach" {
        "detached"
    } else {
        "attached"
    };
    let path = parsed.text("path").trim();
    let repo = parsed.text("repo").trim();
    let label = [
        path,
        parsed.text("notebook").trim(),
        parsed.text("url").trim(),
        repo,
    ]
    .into_iter()
    .find(|value| !value.is_empty())
    .unwrap_or_default();
    let moved = if !repo.is_empty() && !path.is_empty() {
        format!("{verb} {path} ({repo})")
    } else {
        format!("{verb} {label}")
    };
    let mut out = format!("{} {moved}\n", parsed.text("seed"));
    let body = result.body.trim();
    if !body.is_empty() && body != moved {
        let _ = writeln!(out, "{body}");
    }
    out
}

fn transfer(cmd: &Command, parsed: &Parsed, result: &SeedArtifactTransferResult) -> String {
    let operation = if cmd.name == "detach" {
        "detach"
    } else {
        parsed.text("mode")
    };
    let mut out = format!(
        "{operation} {}\nto {}\n",
        result.source_path, result.destination_path
    );
    if result.replaced == Some(true) {
        let base = Path::new(&result.destination_path)
            .file_name()
            .map_or_else(String::new, |name| name.to_string_lossy().into_owned());
        let _ = writeln!(out, "replaced the seed's earlier {base}");
    }
    if operation != "detach" {
        let _ = writeln!(out, "markdown target {}", result.relative_target);
    }
    out
}
