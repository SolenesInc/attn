use super::render::{self, actor_label, plural_rang};
use crate::commands::support::or_dash;
use attn::go::time::Time;
use attn_protocol::go_values::{CHECK_CONFIRMED, CHECK_OPEN, CHECK_REJECTED, CHECK_REPORTED};
use attn_protocol::{
    Seed, SeedCheck, SeedCheckReport, SeedCheckResult, SeedCheckTally, SeedSharedCheck,
};
use std::fmt::Write as _;

const CHECK_WRAP_WIDTH: usize = 80;
const NOTE_SEPARATOR: &str = ": ";

fn note_headline(body: &str) -> &str {
    let headline = body.split_once(NOTE_SEPARATOR).map_or(body, |(h, _)| h);
    headline.split_once(", ").map_or(headline, |(m, _)| m)
}

pub(super) fn render_move(result: &SeedCheckResult, rang: bool) -> String {
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
    render::live_delegate_nudge(&mut line, result.live_delegate.as_deref());
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
