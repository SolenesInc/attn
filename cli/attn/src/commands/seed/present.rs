use super::render;
use super::{Stop, fail, help_generated, usage_error};
use crate::client::Reply;
use crate::command::{self, Arg, Command, Family, Parsed};
use crate::commands::{Run, print_json, print_stdout};
use attn::garden::section::section;
use attn_protocol::go_values::NOTE_KIND_HANDOFF;
use attn_protocol::{SeedNoteResult, SeedNotesResult, SeedReadyResult, SeedShowResult};
use serde::de::DeserializeOwned;
use serde_json::Value as Json;
use std::fmt::Write as _;

pub const MOVED: &[&str] = &["show", "notes", "note", "ready", "prime"];

pub struct Seed;

fn decode<T: DeserializeOwned>(
    run: &mut Run<'_>,
    cmd: &Command,
    result: &Reply,
) -> Result<T, Stop> {
    result
        .whole()
        .map_err(|error| run.fail_send(&format!("seed {}", cmd.name), &error))
}

fn print_field(
    run: &mut Run<'_>,
    cmd: &Command,
    result: &Reply,
    path: &[&str],
) -> Result<(), Stop> {
    let raw = result
        .raw_field(path)
        .map_err(|error| run.fail_send(&format!("seed {}", cmd.name), &error))?;
    print_json(raw.as_bytes());
    Ok(())
}

impl Family for Seed {
    fn group(&self) -> &'static str {
        "seed"
    }

    fn help(&self, verb: &str) -> &'static str {
        help_generated::help(verb)
    }

    fn usage_error(&self, verb: &str, message: &str) -> Stop {
        usage_error(verb, message)
    }

    fn fail(&self, verb: &str, message: &str) -> Stop {
        fail(verb, message)
    }

    fn decode_file(&self, _cmd: &Command, _arg: &Arg, _bytes: &[u8]) -> Result<Json, String> {
        unreachable!("no moved seed command reads a file yet")
    }

    fn read_stdin_error(&self, _arg: &Arg) -> &'static str {
        "reading stdin"
    }

    fn choice_conflict(&self, _cmd: &Command, _arg: &Arg) -> String {
        unreachable!("no moved seed command takes a choice yet")
    }

    fn stamp(&self, _cmd: &Command, _parsed: &mut Parsed) -> Result<(), String> {
        Ok(())
    }

    fn before_send(
        &self,
        _run: &mut Run<'_>,
        _cmd: &Command,
        _parsed: &Parsed,
    ) -> Result<(), Stop> {
        Ok(())
    }

    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        match cmd.name {
            "note" if parsed.json => print_field(run, cmd, &result, &["note"]),
            "prime" => {
                let ready: SeedReadyResult = decode(run, cmd, &result)?;
                print_stdout(&render::prime(&ready));
                Ok(())
            }
            _ if parsed.json => print_field(run, cmd, &result, &[]),
            "show" => show(run, cmd, parsed, &result),
            "notes" => notes(run, cmd, parsed, &result),
            "note" => note(run, cmd, parsed, &result),
            "ready" => {
                let ready: SeedReadyResult = decode(run, cmd, &result)?;
                print_stdout(&render::ready(&ready));
                Ok(())
            }
            other => unreachable!("seed {other} has no renderer"),
        }
    }
}

fn show(run: &mut Run<'_>, cmd: &Command, parsed: &Parsed, result: &Reply) -> Result<(), Stop> {
    let result: SeedShowResult = decode(run, cmd, result)?;
    let seed = &result.seed;
    if parsed.has("section") {
        let part = section(&seed.body, parsed.text("section"))
            .map_err(|error| fail("show", &format!("{}: {error}", seed.id)))?;
        print_stdout(&format!("{}  {}\n\n{part}\n", seed.id, seed.title));
        return Ok(());
    }
    let mut out = String::new();
    if let Some(window) = &result.since {
        match &window.expired {
            None => {
                render::since(&mut out, window.after, &window.entries);
                render::bookmark_line(&mut out, "show", &seed.id, result.bookmark, "");
                print_stdout(&out);
                return Ok(());
            }
            Some(expired) => render::expired_bookmark(&mut out, window.after, expired),
        }
    }
    out.push_str(&render::show(&result, result.bookmark));
    print_stdout(&out);
    Ok(())
}

fn notes(run: &mut Run<'_>, cmd: &Command, parsed: &Parsed, result: &Reply) -> Result<(), Stop> {
    let result: SeedNotesResult = decode(run, cmd, result)?;
    let id = parsed.text("seed");
    let kind = parsed.text("kind").trim();
    let since = parsed.int("since");
    let mut out = String::new();
    if let Some(after) = since
        && result.expired.is_none()
    {
        render::since(&mut out, after, &result.notes);
        render::bookmark_line(&mut out, "notes", id, result.bookmark, kind);
        print_stdout(&out);
        return Ok(());
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
    print_stdout(&out);
    Ok(())
}

fn note(run: &mut Run<'_>, cmd: &Command, parsed: &Parsed, result: &Reply) -> Result<(), Stop> {
    let result: SeedNoteResult = decode(run, cmd, result)?;
    let mut out = if result.note.kind == NOTE_KIND_HANDOFF {
        format!(
            "handoff left on {} — whoever tends it next reads this first\n",
            result.note.seed_id
        )
    } else {
        format!("noted on {}\n", result.note.seed_id)
    };
    if parsed.on("ring") {
        render::plural_rang(
            &mut out,
            &result.note.seed_id,
            result.ring_recipients.as_deref(),
        );
    }
    print_stdout(&out);
    Ok(())
}
