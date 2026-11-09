use super::Session;
use super::render::{self, actor_label, plural_rang};
use super::{Stop, usage_error};
use crate::commands::support::or_dash;
use attn::garden::valid_check_id;
use attn::go::quote::quote;
use attn::go::time::Time;
use attn_protocol::go_values::{
    CHECK_CONFIRMED, CHECK_OPEN, CHECK_REJECTED, CHECK_REPORTED, CHECK_VERB_ADD, CHECK_VERB_EDIT,
    CHECK_VERB_REPORT, CHECK_VERBS, MAX_CHECK_SUMMARY_CHARS, MAX_NOTE_BYTES,
};
use attn_protocol::{
    Seed, SeedCheck, SeedCheckDefinition, SeedCheckMessage, SeedCheckReport, SeedCheckResult,
    SeedCheckTally, SeedSharedCheck,
};
use std::fmt::Write as _;
use std::fs;
use std::io::ErrorKind;
use std::path::Path;
use std::process::Command;

const CHECK_WRAP_WIDTH: usize = 80;
const NOTE_SEPARATOR: &str = ": ";

pub(super) fn run(run: &mut super::Run<'_>, args: &[String]) -> Result<(), Stop> {
    let first = args.first();
    if first.is_none_or(|arg| arg.starts_with('-')) {
        Session::new(run, "check").parse(args)?;
        return Err(usage_error(
            "check",
            &format!("needs a move: {}", CHECK_VERBS.join(", ")),
        ));
    }
    let raw = first.expect("checked above");
    let verb = raw.trim().to_lowercase();
    if !CHECK_VERBS.contains(&verb.as_str()) {
        return Err(usage_error(
            "check",
            &format!(
                "{} is not something a check does; the moves are {}",
                quote(raw.as_bytes()),
                CHECK_VERBS.join(", ")
            ),
        ));
    }
    let name = format!("check {verb}");
    run.path = format!("seed {name}");
    let mut session = Session::new(run, &name);
    let positionals = session.parse(&args[1..])?;
    let message = message(&mut session, &verb, &positionals)?;
    let json = session.flags.on("json");
    let ring = session.flags.on("ring");
    let reply = session.call(
        &message,
        "seed_check_result",
        "the daemon accepted the check move but returned nothing",
    )?;
    if json {
        return session.print_json(&reply, &["seed_check_result"]);
    }
    let result: SeedCheckResult = session.read_field(&reply, "seed_check_result")?;
    super::super::print_stdout(&render_move(&result, ring));
    Ok(())
}

fn message(
    session: &mut Session<'_, '_>,
    verb: &str,
    positionals: &[String],
) -> Result<SeedCheckMessage, Stop> {
    let defines = verb == CHECK_VERB_ADD || verb == CHECK_VERB_EDIT;
    let Some(seed_id) = positionals.first() else {
        return Err(session.usage("needs the seed id"));
    };
    let mut message = SeedCheckMessage::new();
    message.seed_id.clone_from(seed_id);
    message.verb = verb.to_string();
    let ids = &positionals[1..];
    if defines
        && let Some(last) = ids.last()
        && !valid_check_id(&last.to_uppercase())
    {
        return Err(session.usage(&format!(
            "{} is not a check id; the statement goes in -m",
            quote(last.as_bytes())
        )));
    }
    match ids {
        [only] => message.check_id = Some(only.to_uppercase()),
        [] if verb == CHECK_VERB_ADD => {}
        _ => {
            return Err(session.usage(&format!(
                "needs the seed id and one check id, got {} arguments",
                positionals.len()
            )));
        }
    }
    let source = session.session_id()?;
    if !source.is_empty() {
        message.source_session_id = Some(source);
    }
    let text = session.text()?.trim().to_string();
    if defines && text.is_empty() {
        return Err(session.usage("needs the statement in -m: what this seed must prove"));
    }
    if defines {
        message.text = Some(text);
    } else if !text.is_empty() {
        message.summary = Some(text);
    }
    if session.flags.on("shared") {
        message.shared = Some(true);
    }
    if verb == CHECK_VERB_REPORT {
        add_report_evidence(session, &mut message)?;
    }
    if session.flags.on("ring") {
        message.ring = Some(true);
    }
    Ok(message)
}

fn limit(value: i64) -> usize {
    usize::try_from(value).unwrap_or(usize::MAX)
}

fn report_text_problem(text: &str) -> Option<String> {
    if text.len() > limit(MAX_NOTE_BYTES) {
        return Some(format!(
            "max_note_bytes={MAX_NOTE_BYTES}, and this report's -m is {} bytes; the detail says what a reviewer needs to judge the check, and long output goes in an --evidence file",
            text.len()
        ));
    }
    let trimmed = text.trim();
    let summary = trimmed.split('\n').next().unwrap_or("").trim();
    let chars = summary.chars().count();
    (chars > limit(MAX_CHECK_SUMMARY_CHARS)).then(|| format!(
        "max_check_summary_chars={MAX_CHECK_SUMMARY_CHARS}, and this report's first line is {chars}; the first line of -m is the summary `show` prints, and the lines after it are detail kept in the report's check note"
    ))
}

fn resolve_commit(rev: &str) -> Result<String, String> {
    let quoted = quote(rev.as_bytes());
    let output = Command::new("git")
        .args(["rev-parse", "--verify", &format!("{rev}^{{commit}}")])
        .output();
    let failure = match output {
        Ok(output) if output.status.success() => {
            return Ok(String::from_utf8_lossy(&output.stdout).trim().to_string());
        }
        Ok(output) => match output.status.code() {
            Some(code) => format!("exit status {code}"),
            None => "signal: killed".to_string(),
        },
        Err(error) if error.kind() == ErrorKind::NotFound => {
            "exec: \"git\": executable file not found in $PATH".to_string()
        }
        Err(error) => crate::paths::os_error_text(&error),
    };
    Err(format!(
        "--commit {quoted} is not a commit in this directory's repository (git rev-parse): {failure}"
    ))
}

fn looks_like_url(value: &str) -> bool {
    let Some((scheme, rest)) = value.split_once("://") else {
        return false;
    };
    (scheme.eq_ignore_ascii_case("http") || scheme.eq_ignore_ascii_case("https"))
        && rest.split('/').next().is_some_and(|host| !host.is_empty())
}

enum Evidence {
    Cited,
    File(String),
}

fn local_evidence_file(value: &str) -> Result<Evidence, String> {
    if looks_like_url(value) {
        return Ok(Evidence::Cited);
    }
    let metadata = match fs::metadata(value) {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == ErrorKind::NotFound => {
            if value.starts_with('/') || value.starts_with("./") || value.starts_with("../") {
                return Err(format!("--evidence {value}: no file at that path"));
            }
            return Ok(Evidence::Cited);
        }
        Err(error) => {
            return Err(format!(
                "--evidence {value}: stat {value}: {}",
                crate::paths::os_error_text(&error)
            ));
        }
    };
    if !metadata.is_file() {
        return Err(format!(
            "--evidence {value} is not a regular file; cite a file, an artifact on the seed, or an http(s) URL"
        ));
    }
    fs::File::open(value).map_err(|error| {
        format!(
            "--evidence {value}: open {value}: {}",
            crate::paths::os_error_text(&error)
        )
    })?;
    super::super::absolute(value)
        .map(Evidence::File)
        .map_err(|error| crate::paths::os_error_text(&error))
}

fn add_report_evidence(
    session: &mut Session<'_, '_>,
    message: &mut SeedCheckMessage,
) -> Result<(), Stop> {
    let mut problems = Vec::new();
    if let Some(problem) = message.summary.as_deref().and_then(report_text_problem) {
        problems.push(problem);
    }
    let commit = session.flags.string("commit").trim().to_string();
    if !commit.is_empty() {
        match resolve_commit(&commit) {
            Ok(full) => message.commit = Some(full),
            Err(problem) => problems.push(problem),
        }
    }
    let mut files: Vec<(String, String)> = Vec::new();
    let mut evidence = Vec::new();
    let mut evidence_files = Vec::new();
    for raw in session.flags.repeated("evidence").to_vec() {
        let value = raw.trim().to_string();
        match local_evidence_file(&value) {
            Err(problem) => problems.push(problem),
            Ok(Evidence::Cited) => evidence.push(value),
            Ok(Evidence::File(path)) => {
                let base = Path::new(&path)
                    .file_name()
                    .map(|name| name.to_string_lossy().into_owned())
                    .unwrap_or_default();
                match files.iter().find(|(name, _)| *name == base) {
                    None => {
                        files.push((base, path.clone()));
                        evidence_files.push(path);
                    }
                    Some((_, held)) if *held == path => {}
                    Some((_, held)) => problems.push(format!(
                        "--evidence {held} and {path} are both named {base}, and a seed keeps one file per name; rename one"
                    )),
                }
            }
        }
    }
    if !evidence.is_empty() {
        message.evidence = Some(evidence);
    }
    if !evidence_files.is_empty() {
        message.evidence_files = Some(evidence_files);
    }
    match problems.as_slice() {
        [] => Ok(()),
        [only] => Err(session.usage(only)),
        many => {
            let lines: Vec<String> = many.iter().map(|p| format!("  - {p}")).collect();
            Err(session.usage(&format!(
                "{} problems; fix each and run the same command again:\n{}",
                many.len(),
                lines.join("\n")
            )))
        }
    }
}

fn note_headline(body: &str) -> &str {
    let headline = body.split_once(NOTE_SEPARATOR).map_or(body, |(h, _)| h);
    headline.split_once(", ").map_or(headline, |(m, _)| m)
}

fn render_move(result: &SeedCheckResult, rang: bool) -> String {
    let mut line = format!(
        "{} on {}",
        note_headline(&result.note.body),
        render::seed_handle(&result.seed)
    );
    if let Some(attached) = result.attached.as_ref().filter(|items| !items.is_empty()) {
        let names: Vec<String> = attached
            .iter()
            .map(|item| {
                if item.replaced {
                    format!("{} (replacing the earlier file)", item.filename)
                } else {
                    item.filename.clone()
                }
            })
            .collect();
        line.push_str(" · attached ");
        line.push_str(&names.join(" and "));
    }
    let tally = tally_long(&seed_or_plot_tally(&result.seed));
    if !tally.is_empty() {
        line.push_str(" · checks ");
        line.push_str(&tally);
    }
    line.push('\n');
    if rang {
        plural_rang(
            &mut line,
            &result.seed.id,
            result.ring_recipients.as_deref(),
        );
    }
    if render::closed_status(&result.seed.status) {
        let _ = writeln!(
            line,
            "{} stays {}: a check never reopens a seed",
            result.seed.id, result.seed.status
        );
    }
    line
}

pub fn seed_check_tally(checks: &[SeedCheck]) -> SeedCheckTally {
    let mut tally = SeedCheckTally::default();
    for check in checks.iter().filter(|check| !check.removed) {
        tally.total += 1;
        match check.state.as_str() {
            CHECK_REPORTED => tally.reported += 1,
            CHECK_CONFIRMED => tally.confirmed += 1,
            CHECK_REJECTED => tally.rejected += 1,
            _ => {}
        }
    }
    tally
}

pub fn tally_long(tally: &SeedCheckTally) -> String {
    if tally.total == 0 {
        return String::new();
    }
    let mut parts = vec![format!("{} of {} confirmed", tally.confirmed, tally.total)];
    if tally.reported > 0 {
        parts.push(format!("{} to judge", tally.reported));
    }
    if tally.rejected > 0 {
        parts.push(format!("{} rejected", tally.rejected));
    }
    let open = tally.total - tally.confirmed - tally.reported - tally.rejected;
    if open > 0 {
        parts.push(format!("{open} open"));
    }
    parts.join(" · ")
}

pub fn tally_short(tally: &SeedCheckTally) -> String {
    if tally.total == 0 {
        return String::new();
    }
    let mut parts = vec![format!("checks ✓{}/{}", tally.confirmed, tally.total)];
    if tally.reported > 0 {
        parts.push(format!("{} to judge", tally.reported));
    }
    if tally.rejected > 0 {
        parts.push(format!("{} rejected", tally.rejected));
    }
    format!("  [{}]", parts.join(" · "))
}

pub fn plot_tally(progress: Option<&attn_protocol::SeedPlotProgress>) -> SeedCheckTally {
    progress
        .and_then(|progress| progress.checks.clone())
        .unwrap_or_default()
}

pub fn seed_or_plot_tally(seed: &Seed) -> SeedCheckTally {
    if seed.plot_progress.is_some() {
        return plot_tally(seed.plot_progress.as_ref());
    }
    seed_check_tally(seed.checks.as_deref().unwrap_or_default())
}

pub fn unsettled(checks: &[SeedCheck]) -> String {
    let parts: Vec<String> = checks
        .iter()
        .filter(|check| {
            !check.removed && (check.state == CHECK_OPEN || check.state == CHECK_REJECTED)
        })
        .map(|check| format!("{} is {}.", check.id, check.state))
        .collect();
    parts.join(" ")
}

pub fn render_seed_checks(out: &mut String, seed: &Seed) {
    let checks = seed.checks.as_deref().unwrap_or_default();
    let shared = seed.shared_checks.as_deref().unwrap_or_default();
    if checks.is_empty() && shared.is_empty() {
        return;
    }
    out.push('\n');
    let mut removed: Vec<&str> = Vec::new();
    let mut live = 0;
    for check in checks {
        if check.removed {
            removed.push(&check.id);
        } else {
            live += 1;
        }
    }
    if live > 0 {
        let _ = writeln!(out, "checks  {}", tally_long(&seed_check_tally(checks)));
        for check in checks.iter().filter(|check| !check.removed) {
            render_check(out, check);
        }
    }
    if !shared.is_empty() {
        out.push_str("  shared by every seed below\n");
        for entry in shared {
            if entry.removed {
                removed.push(&entry.id);
                continue;
            }
            render_shared(out, entry);
        }
    }
    if !removed.is_empty() {
        let _ = writeln!(
            out,
            "  removed  {} — attn seed check restore {} <C#> brings one back",
            removed.join(", "),
            seed.id
        );
    }
    if live > 0 {
        let _ = writeln!(
            out,
            "  report with: attn seed check report {} <C#> -m \"<summary line>\" --evidence <file|artifact|url>",
            seed.id
        );
    }
}

fn check_stamp(at: &str) -> String {
    Time::parse_rfc3339(at).map_or_else(|_| at.to_string(), |t| t.format_local("Jan 2 15:04"))
}

fn render_check(out: &mut String, check: &SeedCheck) {
    let mut status = check.state.clone();
    if let Some(verdict) = &check.verdict {
        status.push_str(" by ");
        status.push_str(&or_dash(&actor_label(&verdict.by)));
        if check.report.is_none() {
            status.push_str(", no report");
        }
    }
    if let Some(from) = check.from.as_deref().filter(|from| !from.is_empty()) {
        status.push_str(" · shared from ");
        status.push_str(from);
    }
    let _ = writeln!(out, "  {}  {status}", check.id);
    let indent = " ".repeat(check.id.len() + 4);
    wrapped(out, &indent, &check.text);
    if let Some(report) = &check.report {
        labeled(out, &indent, "report  ", &report.summary);
        labeled(out, &indent, "        ", &provenance(report));
    }
    if let Some(verdict) = &check.verdict
        && let Some(reason) = verdict
            .reason
            .as_deref()
            .filter(|reason| !reason.is_empty())
    {
        let by = or_dash(&actor_label(&verdict.by));
        labeled(
            out,
            &indent,
            &format!("{:<8}", check.state),
            &format!("{reason} · {by}, {}", check_stamp(&verdict.at)),
        );
    }
}

fn provenance(report: &SeedCheckReport) -> String {
    let mut parts = Vec::new();
    if report.detail == Some(true) {
        parts.push("detail in its note".to_string());
    }
    let labels: Vec<String> = report
        .evidence
        .iter()
        .map(|cited| {
            let mut label = cited.artifact.clone().unwrap_or_default();
            label.push_str(cited.url.as_deref().unwrap_or_default());
            if cited.changed_since_report == Some(true) {
                label.push_str(" (changed since this report)");
            }
            label
        })
        .collect();
    let cited = labels.join(", ");
    if !cited.is_empty() {
        parts.push(cited);
    }
    if let Some(commit) = report.commit.as_deref().filter(|c| !c.is_empty()) {
        let short = if commit.len() > 7 {
            &commit[..7]
        } else {
            commit
        };
        parts.push(format!("@{short}"));
    }
    parts.push(format!(
        "{}, {}",
        or_dash(&actor_label(&report.by)),
        check_stamp(&report.at)
    ));
    parts.join(" · ")
}

fn render_shared(out: &mut String, shared: &SeedSharedCheck) {
    let confirmed = shared
        .seeds
        .iter()
        .filter(|entry| entry.state == CHECK_CONFIRMED)
        .count();
    let _ = writeln!(
        out,
        "  {}  {confirmed} of {} confirmed",
        shared.id,
        shared.seeds.len()
    );
    let indent = " ".repeat(shared.id.len() + 4);
    wrapped(out, &indent, &shared.text);
    for state in [CHECK_CONFIRMED, CHECK_REPORTED, CHECK_REJECTED, CHECK_OPEN] {
        let seeds: Vec<&str> = shared
            .seeds
            .iter()
            .filter(|entry| entry.state == state)
            .map(|entry| entry.seed_id.as_str())
            .collect();
        if !seeds.is_empty() {
            let label = if state == CHECK_REPORTED {
                "to judge"
            } else {
                state
            };
            labeled(out, &indent, &format!("{label:<10} "), &seeds.join(" "));
        }
    }
}

fn labeled(out: &mut String, indent: &str, label: &str, text: &str) {
    let width =
        render::count(CHECK_WRAP_WIDTH) - render::count(indent.len()) - render::count(label.len());
    for (index, line) in wrap_words(text, width).iter().enumerate() {
        if index == 0 {
            let _ = writeln!(out, "{indent}{label}{line}");
        } else {
            let _ = writeln!(out, "{indent}{}{line}", " ".repeat(label.len()));
        }
    }
}

fn wrapped(out: &mut String, indent: &str, text: &str) {
    let width = render::count(CHECK_WRAP_WIDTH) - render::count(indent.len());
    for line in wrap_words(text, width) {
        out.push_str(indent);
        out.push_str(&line);
        out.push('\n');
    }
}

fn wrap_words(text: &str, width: i64) -> Vec<String> {
    let mut lines = Vec::new();
    let trimmed = text.trim();
    for paragraph in trimmed.split('\n') {
        let mut line = String::new();
        for word in paragraph.split_whitespace() {
            if line.is_empty() {
                line.push_str(word);
            } else if render::count(line.len()) + 1 + render::count(word.len()) > width {
                lines.push(std::mem::take(&mut line));
                line.push_str(word);
            } else {
                line.push(' ');
                line.push_str(word);
            }
        }
        lines.push(line);
    }
    lines
}

pub fn parse_plant_checks(raw: &[String]) -> Vec<SeedCheckDefinition> {
    raw.iter()
        .map(|entry| {
            let mut definition = SeedCheckDefinition {
                text: entry.trim().to_string(),
                ..SeedCheckDefinition::default()
            };
            if let Some((id, text)) = entry.split_once('=') {
                let id = id.trim().to_uppercase();
                if valid_check_id(&id) {
                    definition.id = Some(id);
                    definition.text = text.trim().to_string();
                }
            }
            definition
        })
        .collect()
}
