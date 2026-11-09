use super::checks::{
    plot_tally, render_seed_checks, seed_or_plot_tally, tally_long, tally_short, unsettled,
};
use crate::commands::support::or_dash;
use attn::garden::section::id_ranges;
use attn::go::duration;
use attn::go::quote::quote;
use attn::go::tabwriter;
use attn::go::time::Time;
use attn_protocol::go_values::prompts::{
    SESSION_GARDEN_GUIDANCE, SESSION_SEED_ARTIFACTS, SessionGardenReady, SessionGardenRow,
    session_garden_ready, session_garden_row,
};
use attn_protocol::go_values::{
    DEFAULT_STALE_WINDOW_SECONDS, EDGE_PART_OF, NOTE_KIND_NOTE, SEARCH_MAX_RESULTS,
    STATUS_HARVESTED, STATUS_WITHERED,
};
use attn_protocol::{
    ActorView, AgentCloseResult, GardenReview, PartyView, Seed, SeedArtifact,
    SeedArtifactReference, SeedAuthorization, SeedAuthorizationResult, SeedAuthorizationUpdate,
    SeedBoardRow, SeedClosedCount, SeedHarvestCondition, SeedListResult, SeedNote,
    SeedPlotProgress, SeedReadyResult, SeedRelation, SeedReminder, SeedRow, SeedSearchResult,
    SeedShowResult, SeedSimilar, SeedSinceExpired, SeedStandbyView, SeedTransitionResult,
    SeedWatchResult,
};
use std::collections::{BTreeMap, HashSet};
use std::fmt::Write;
use std::time::{SystemTime, UNIX_EPOCH};

const NANOS_PER_SECOND: i128 = 1_000_000_000;
const SECONDS_PER_DAY: i64 = 86_400;
const AGENT_SHORT_ID_LENGTH: usize = 8;
const CLIP_WIDTH: usize = 80;

pub fn format_window_seconds(seconds: i64) -> String {
    if seconds >= SECONDS_PER_DAY && seconds % SECONDS_PER_DAY == 0 {
        return format!("{}d", seconds / SECONDS_PER_DAY);
    }
    duration::format(seconds.saturating_mul(1_000_000_000))
}

fn stale_window_label(seconds: Option<i64>) -> String {
    match seconds {
        Some(value) if value > 0 => format_window_seconds(value),
        _ => format_window_seconds(DEFAULT_STALE_WINDOW_SECONDS),
    }
}

pub fn parse_window_nanos(raw: &str) -> Result<i64, String> {
    if let Some(days) = raw.strip_suffix('d')
        && let Ok(hours) = duration::parse(&format!("{days}h"))
    {
        return Ok(hours.wrapping_mul(24));
    }
    match duration::parse(raw) {
        Ok(window) if window > 0 => Ok(window),
        _ => Err(format!(
            "{} is not a window; say it like 72h or 14d",
            quote(raw.as_bytes())
        )),
    }
}

pub fn short_stamp(stamp: &str) -> String {
    Time::parse_rfc3339(stamp).map_or_else(
        |_| stamp.to_string(),
        |t| t.format_local("2006-01-02 15:04"),
    )
}

#[allow(clippy::cast_precision_loss)]
fn unit_float(nanos: i128, unit: i128) -> f64 {
    let whole = nanos / unit;
    let rest = nanos % unit;
    whole as f64 + rest as f64 / unit as f64
}

fn human_age(nanos: i128) -> String {
    const HOUR: i128 = 3600 * NANOS_PER_SECOND;
    const MINUTE: i128 = 60 * NANOS_PER_SECOND;
    if nanos >= 48 * HOUR {
        format!("{:.0}d", unit_float(nanos, HOUR) / 24.0)
    } else if nanos >= HOUR {
        format!("{:.0}h", unit_float(nanos, HOUR))
    } else if nanos >= MINUTE {
        format!("{:.0}m", unit_float(nanos, MINUTE))
    } else {
        format!("{:.0}s", unit_float(nanos, NANOS_PER_SECOND))
    }
}

fn last_active(value: &str) -> String {
    let Ok(at) = Time::parse_rfc3339_nano(value) else {
        return value.to_string();
    };
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |elapsed| {
            i128::try_from(elapsed.as_nanos()).unwrap_or(i128::MAX)
        });
    human_age((now - at.unix_nanos()).max(0))
}

#[allow(clippy::cast_precision_loss)]
fn human_bytes(bytes: i64) -> String {
    if bytes >= 1 << 20 {
        format!("{:.1} MB", bytes as f64 / f64::from(1 << 20))
    } else if bytes >= 1 << 10 {
        format!("{:.1} KB", bytes as f64 / f64::from(1 << 10))
    } else {
        format!("{bytes} B")
    }
}

#[allow(clippy::cast_precision_loss)]
fn conversation_bytes(bytes: i64) -> String {
    if bytes < 1000 {
        format!("{bytes} B")
    } else if bytes < 1_000_000 {
        format!("{:.1} KB", bytes as f64 / 1000.0)
    } else {
        format!("{:.1} MB", bytes as f64 / 1_000_000.0)
    }
}

fn conversation_date(stamp: &str) -> String {
    Time::parse_rfc3339_nano(stamp).map_or_else(|_| stamp.to_string(), |t| t.format("2006-01-02"))
}

pub fn count(items: usize) -> i64 {
    i64::try_from(items).unwrap_or(i64::MAX)
}

fn table(rows: &str) -> String {
    tabwriter::format(rows)
}

fn q(text: &str) -> String {
    quote(text.as_bytes())
}

fn who_label(reference: &str, name: &str) -> String {
    reference
        .strip_prefix("session:")
        .map_or_else(|| name.to_string(), ToString::to_string)
}

pub fn actor_label(actor: &ActorView) -> String {
    who_label(&actor.r#ref, &actor.name)
}

fn party_label(party: Option<&PartyView>) -> String {
    party.map_or_else(String::new, |party| who_label(&party.r#ref, &party.name))
}

pub fn closed_status(status: &str) -> bool {
    status == STATUS_HARVESTED || status == STATUS_WITHERED
}

pub fn seed_line(id: &str, slug: &str, title: &str) -> String {
    format!("{id}  {slug}  {title}")
}

pub fn seed_handle(seed: &Seed) -> String {
    if seed.step_slug.is_empty() {
        seed.id.clone()
    } else {
        format!("{} ({})", seed.id, seed.step_slug)
    }
}

pub fn plural(count: i64, noun: &str) -> String {
    if count == 1 {
        format!("{count} {noun}")
    } else {
        format!("{count} {noun}s")
    }
}

pub fn plural_rang(out: &mut String, seed_id: &str, recipients: Option<&[String]>) {
    match recipients.filter(|list| !list.is_empty()) {
        None => {
            let _ = writeln!(out, "rang nobody: nobody else tends or watches {seed_id}");
        }
        Some(list) => {
            let _ = writeln!(out, "rang {}", list.join(", "));
        }
    }
}

pub fn similar(out: &mut String, hits: &[SeedSimilar], plot: bool) {
    if hits.is_empty() {
        return;
    }
    out.push_str("\nsimilar open seeds\n");
    for hit in hits {
        let _ = write!(
            out,
            "  {}  {} · {}",
            seed_line(&hit.seed.id, &hit.seed.step_slug, &hit.seed.title),
            hit.seed.status,
            last_active(&hit.last_active)
        );
        if plot {
            let _ = write!(out, "  like {}", hit.like);
        }
        out.push('\n');
    }
    let mut seen = HashSet::new();
    for hit in hits {
        if seen.insert(&hit.like) {
            let _ = writeln!(
                out,
                "if one is the same work: attn seed wither {} -m \"duplicate of <id>\"",
                hit.like
            );
        }
    }
}

pub fn search(query: &str, result: &SeedSearchResult) -> String {
    let mut out = String::new();
    let total = result.matched + result.partial;
    if total == 0 {
        let _ = writeln!(
            out,
            "no seed matches {} — searched {}: titles, bodies and every log entry",
            q(query),
            plural(result.searched, "seed")
        );
        return out;
    }
    let verb = if result.matched == 1 {
        "carries"
    } else {
        "carry"
    };
    let _ = writeln!(
        out,
        "{} {verb} every word, {} carry some — best first\n",
        plural(result.matched, "seed"),
        result.partial
    );
    for hit in &result.hits {
        let _ = writeln!(
            out,
            "{}\n  {} · {}  {}  {}",
            seed_line(&hit.seed.id, &hit.seed.step_slug, &hit.seed.title),
            hit.seed.status,
            last_active(&hit.last_active),
            hit.r#where,
            hit.snippet
        );
    }
    if total > count(result.hits.len()) {
        let _ = writeln!(
            out,
            "\nshowing {} of {total} — `--limit <n>` for more, up to {SEARCH_MAX_RESULTS}",
            result.hits.len()
        );
    }
    out
}

struct Placed<'a> {
    row: &'a SeedRow,
    depth: usize,
}

fn row_parent(row: &SeedRow) -> &str {
    row.edges
        .iter()
        .find(|edge| edge.kind == EDGE_PART_OF)
        .map_or("", |edge| edge.to.as_str())
}

fn place(
    at: usize,
    depth: usize,
    children: &BTreeMap<usize, Vec<usize>>,
    placed: &mut [bool],
    rows: &mut Vec<(usize, usize)>,
) {
    if placed[at] {
        return;
    }
    placed[at] = true;
    rows.push((at, depth));
    if let Some(list) = children.get(&at) {
        for &child in list {
            place(child, depth + 1, children, placed, rows);
        }
    }
}

fn place_rows<'a>(flat: bool, groups: &[&'a [SeedRow]]) -> Vec<Placed<'a>> {
    let seeds: Vec<&SeedRow> = groups.iter().flat_map(|group| group.iter()).collect();
    if flat {
        return seeds
            .into_iter()
            .map(|row| Placed { row, depth: 0 })
            .collect();
    }
    let index: BTreeMap<&str, usize> = seeds
        .iter()
        .enumerate()
        .map(|(position, row)| (row.id.as_str(), position))
        .collect();
    let mut children: BTreeMap<usize, Vec<usize>> = BTreeMap::new();
    let mut roots = Vec::with_capacity(seeds.len());
    for (position, row) in seeds.iter().enumerate() {
        let parent = row_parent(row);
        match index.get(parent) {
            Some(&owner) if !parent.is_empty() => children.entry(owner).or_default().push(position),
            _ => roots.push(position),
        }
    }
    let mut placed = vec![false; seeds.len()];
    let mut rows = Vec::with_capacity(seeds.len());
    for root in roots {
        place(root, 0, &children, &mut placed, &mut rows);
    }
    for at in 0..seeds.len() {
        place(at, 0, &children, &mut placed, &mut rows);
    }
    rows.into_iter()
        .map(|(at, depth)| Placed {
            row: seeds[at],
            depth,
        })
        .collect()
}

fn pull_request_label(id: &str) -> &str {
    id.split_once(':').map_or(id, |(_, rest)| rest)
}

fn harvest_when_line(condition: Option<&SeedHarvestCondition>) -> String {
    let Some(condition) = condition else {
        return String::new();
    };
    let mut line = format!(
        "harvests when {} merges",
        pull_request_label(&condition.pull_request)
    );
    if let Some(reason) = condition.reason.as_deref().filter(|r| !r.is_empty()) {
        let _ = write!(line, " with your reason: {reason}");
    }
    line
}

fn plot_suffix(progress: Option<&SeedPlotProgress>) -> String {
    progress.map_or_else(String::new, |p| {
        format!(
            "  [{}/{} done · {} growing · {} ready · {} blocked]",
            p.done, p.total, p.growing, p.ready, p.blocked
        )
    })
}

fn row_tally(row: &SeedRow) -> attn_protocol::SeedCheckTally {
    if row.plot_progress.is_some() {
        return plot_tally(row.plot_progress.as_ref());
    }
    row.check_tally.clone().unwrap_or_default()
}

fn reminder_suffix(reminder: Option<&SeedReminder>) -> String {
    reminder.map_or_else(String::new, |r| {
        format!("  [reminder {}]", short_stamp(&r.at))
    })
}

fn standby_suffix(standby: Option<&SeedStandbyView>, reminder: Option<&SeedReminder>) -> String {
    let Some(standby) = standby else {
        return reminder_suffix(reminder);
    };
    let mut waits: Vec<String> = standby
        .pull_requests
        .iter()
        .map(|pr| {
            let state = if pr.state == "open" && !pr.readiness_state.is_empty() {
                &pr.readiness_state
            } else {
                &pr.state
            };
            format!("{} {state}", pull_request_label(&pr.pr_id))
                .trim()
                .to_string()
        })
        .collect();
    if let Some(reminder) = reminder {
        waits.push(format!("reminder {}", short_stamp(&reminder.at)));
    }
    let mut line = "standby".to_string();
    if !waits.is_empty() {
        let _ = write!(line, ": {}", waits.join(", "));
    }
    if let Some(blocked) = standby.resume_blocked.as_deref().filter(|b| !b.is_empty()) {
        let _ = write!(line, " · resume blocked: {blocked}");
    }
    format!("  [{line}]")
}

fn row_suffix(row: &SeedRow) -> String {
    let harvest = harvest_when_line(row.harvest_when.as_ref());
    let harvest = if harvest.is_empty() {
        String::new()
    } else {
        format!("  [{harvest}]")
    };
    format!(
        "{}{}{harvest}{}",
        plot_suffix(row.plot_progress.as_ref()),
        tally_short(&row_tally(row)),
        standby_suffix(row.standby.as_ref(), row.reminder.as_ref())
    )
}

pub struct ListView {
    pub plot_id: String,
    pub flat: bool,
    pub stale: bool,
    pub standby: bool,
}

pub fn list(result: &SeedListResult, view: &ListView) -> String {
    let mut out = String::new();
    if !view.plot_id.is_empty() && result.seeds.is_empty() {
        let _ = writeln!(
            out,
            "{0} has no children — `attn seed plant \"<title>\" --part-of {0}` adds one",
            view.plot_id
        );
        return out;
    }
    if view.stale {
        let _ = writeln!(
            out,
            "open seeds whose log has not moved for {} — no note, no move, no edge. This is a query, not a reaper: tend, note or park what still matters.\n",
            stale_window_label(result.stale_window_seconds)
        );
    }
    if result.seeds.is_empty() {
        out.push_str(if view.stale {
            "none — every open seed has moved inside the window\n"
        } else if view.standby {
            "no seed is on standby — its tender puts one there with `attn seed standby <id> -m \"<what to do next>\"`\n"
        } else {
            "the garden is empty — `attn seed plant \"what this is\"` puts something in it\n"
        });
        return out;
    }
    let mut rows = String::from("ID\tSLUG\tSTATUS\tTENDER\tPLANTED\tTITLE\n");
    for placed in place_rows(view.flat || !view.plot_id.is_empty(), &[&result.seeds]) {
        let row = placed.row;
        let _ = writeln!(
            rows,
            "{}\t{}\t{}\t{}\t{}\t{}{}{}",
            row.id,
            row.step_slug,
            row.status,
            or_dash(&party_label(row.tender.as_ref())),
            short_stamp(&row.created_at),
            "  ".repeat(placed.depth),
            row.title,
            row_suffix(row)
        );
    }
    out.push_str(&table(&rows));
    if result.total > count(result.seeds.len()) {
        let shown = result.seeds.len();
        let _ = writeln!(
            out,
            "\nshowing the newest {shown} of {} seeds — one read is capped at {shown}. The {} not shown are the oldest; `attn seed show <id>` still reaches any of them.",
            result.total,
            result.total - count(shown)
        );
    }
    out
}

pub fn review(review: &GardenReview, detailed: bool) -> String {
    let mut out = String::new();
    let run = &review.run;
    let _ = writeln!(
        out,
        "review {}\t{}\t{} seeds",
        run.id,
        run.status,
        review.items.len()
    );
    let _ = write!(
        out,
        "captured {}\tadvisor {} / {}",
        run.captured_at, run.recipe.agent, run.recipe.model
    );
    if let Some(effort) = run.recipe.effort.as_deref().filter(|e| !e.is_empty()) {
        let _ = write!(out, " / {effort}");
    }
    out.push('\n');
    if review.items.is_empty() {
        out.push_str("no seeds needed review when this run started\n");
        return out;
    }
    if !detailed {
        out.push('\n');
        let mut rows = String::from("SEED\tSTATUS\tRECOMMENDATION\tTITLE\n");
        for item in &review.items {
            let _ = writeln!(
                rows,
                "{}\t{}\t{}\t{}",
                item.seed_id,
                item.status,
                or_dash(item.recommendation.as_deref().unwrap_or_default()),
                item.title
            );
        }
        out.push_str(&table(&rows));
        return out;
    }
    for item in &review.items {
        let _ = writeln!(out, "\n{}  {}", item.seed_id, item.title);
        let _ = writeln!(out, "status\t{}", item.status);
        if let Some(recommendation) = item.recommendation.as_deref().filter(|r| !r.is_empty()) {
            let _ = writeln!(
                out,
                "advice\t{recommendation}: {}",
                item.explanation.as_deref().unwrap_or_default()
            );
        }
        if item.resolution != "unresolved" {
            let _ = writeln!(out, "resolution\t{}", item.resolution);
        }
        if !item.actions.is_empty() {
            let _ = writeln!(out, "actions\t{}", item.actions.join(", "));
        }
        if let Some(error) = item.error.as_deref().filter(|e| !e.is_empty()) {
            let _ = writeln!(out, "error\t{error}");
        }
        for evidence in &item.evidence {
            let _ = writeln!(out, "{}\t{}", evidence.label, evidence.text);
        }
    }
    out
}

fn freshest_handoffs(notes: Option<&[SeedNote]>) -> BTreeMap<&str, &SeedNote> {
    let mut freshest = BTreeMap::new();
    for note in notes.unwrap_or_default() {
        freshest.entry(note.seed_id.as_str()).or_insert(note);
    }
    freshest
}

fn first_line(body: &str) -> &str {
    let trimmed = body.trim();
    trimmed.split_once('\n').map_or(trimmed, |(first, _)| first)
}

fn ready_scope(result: &SeedReadyResult) -> String {
    if result.scope == "plot" {
        format!("in the plot under {}", result.scope_id)
    } else {
        "in the garden".to_string()
    }
}

pub fn ready(result: &SeedReadyResult) -> String {
    let mut out = String::new();
    if let Some(crown) = &result.crown {
        let _ = writeln!(
            out,
            "{}{}{}\n",
            seed_line(&crown.id, &crown.step_slug, &crown.title),
            plot_suffix(crown.plot_progress.as_ref()),
            tally_short(&row_tally(crown))
        );
    }
    if result.seeds.is_empty() {
        let _ = writeln!(
            out,
            "nothing is ready {} — `attn seed ls` shows what is planted and what holds it",
            ready_scope(result)
        );
        return out;
    }
    let handoffs = freshest_handoffs(result.handoffs.as_deref());
    let plots = result.plots.as_deref().unwrap_or_default();
    let mut rows = String::from("ID\tSLUG\tSTATUS\tPLANTED\tTITLE\n");
    let placed = if result.scope == "garden" && !plots.is_empty() {
        place_rows(false, &[plots, &result.seeds])
    } else {
        place_rows(true, &[&result.seeds])
    };
    let plot_ids: HashSet<&str> = plots.iter().map(|plot| plot.id.as_str()).collect();
    for entry in placed {
        let row = entry.row;
        let status = if plot_ids.contains(row.id.as_str()) {
            "plot"
        } else {
            row.status.as_str()
        };
        let _ = writeln!(
            rows,
            "{}\t{}\t{status}\t{}\t{}{}{}",
            row.id,
            row.step_slug,
            short_stamp(&row.created_at),
            "  ".repeat(entry.depth),
            row.title,
            row_suffix(row)
        );
        if let Some(handoff) = handoffs.get(row.id.as_str()) {
            let _ = writeln!(
                rows,
                "\t\t\t\t↳ {}: {}",
                or_dash(&actor_label(&handoff.author)),
                first_line(&handoff.body)
            );
        }
    }
    out.push_str(&table(&rows));
    let _ = writeln!(
        out,
        "\n{} ready {} — `attn seed tend <id>` claims one",
        result.seeds.len(),
        ready_scope(result)
    );
    out
}

pub fn prime(ready: &SeedReadyResult) -> String {
    let handoffs = freshest_handoffs(ready.handoffs.as_deref());
    let mut rows = String::new();
    for seed in &ready.seeds {
        let (body, author) = handoffs.get(seed.id.as_str()).map_or_else(
            || (String::new(), String::new()),
            |handoff| (handoff.body.clone(), actor_label(&handoff.author)),
        );
        rows.push_str(&session_garden_row(&SessionGardenRow {
            author: &author,
            handoff: &body,
            seed_id: &seed.id,
            slug: &seed.step_slug,
            title: &seed.title,
        }));
    }
    let count = ready.seeds.len().to_string();
    let crown = ready.crown.as_ref();
    let seed_id = crown.map_or("", |crown| crown.id.as_str());
    let title = crown.map_or_else(String::new, |crown| q(&crown.title));
    let tail = session_garden_ready(&SessionGardenReady {
        count: &count,
        has_crown: crown.is_some(),
        has_ready: !ready.seeds.is_empty(),
        is_plot: crown.is_some_and(|crown| crown.plot_progress.is_some()),
        one_ready: ready.seeds.len() == 1,
        rows: &rows,
        seed_id,
        title: &title,
    });
    format!("{SESSION_GARDEN_GUIDANCE}\n\n{tail}")
}

pub fn bookmark_line(out: &mut String, verb: &str, seed_id: &str, at: i64, kind: &str) {
    let kind = if kind.is_empty() {
        String::new()
    } else {
        format!(" --kind {kind}")
    };
    let _ = writeln!(
        out,
        "bookmark  attn seed {verb} {seed_id}{kind} --since {at}"
    );
}

pub fn since(out: &mut String, after: i64, entries: &[SeedNote]) {
    if entries.is_empty() {
        let _ = writeln!(out, "since {after}: nothing new");
        return;
    }
    let seeds: HashSet<&str> = entries.iter().map(|entry| entry.seed_id.as_str()).collect();
    let mut count = if entries.len() > 1 {
        format!("{} new entries", entries.len())
    } else {
        "1 new entry".to_string()
    };
    if seeds.len() > 1 {
        let _ = write!(count, " on {} seeds", seeds.len());
    }
    let _ = writeln!(out, "since {after}: {count}");
    for entry in entries {
        let body = entry.body.trim_end_matches('\n');
        let mut lines = body.split('\n');
        let _ = writeln!(
            out,
            "  {}  {}  {}  {}  {}",
            short_stamp(&entry.created_at),
            or_dash(&actor_label(&entry.author)),
            entry.seed_id,
            entry.kind,
            lines.next().unwrap_or("")
        );
        for line in lines {
            let _ = writeln!(out, "    {line}");
        }
    }
}

pub fn expired_bookmark(out: &mut String, after: i64, expired: &SeedSinceExpired) {
    let _ = writeln!(
        out,
        "bookmark {after} is older than the event log keeps (save {} onward, {} retention), so this is the full read\n",
        expired.earliest,
        format_window_seconds(expired.retention_seconds)
    );
}

fn note_kind_suffix(kind: &str) -> String {
    if kind.is_empty() || kind == NOTE_KIND_NOTE {
        String::new()
    } else {
        format!("  {kind}")
    }
}

pub fn notes(out: &mut String, notes: &[SeedNote], seed_id: &str, withheld: i64) {
    for (index, note) in notes.iter().enumerate() {
        if index > 0 {
            out.push('\n');
        }
        let _ = writeln!(
            out,
            "{}  {}{}",
            short_stamp(&note.created_at),
            or_dash(&actor_label(&note.author)),
            note_kind_suffix(&note.kind)
        );
        let _ = writeln!(out, "{}", note.body.trim_end_matches('\n'));
    }
    if withheld > 0 {
        let _ = writeln!(out, "\n{withheld} more — `attn seed notes {seed_id}`");
    }
}

fn authorization_update_state(update: &SeedAuthorizationUpdate) -> String {
    let recipient = recipient_label(&update.recipient);
    if let Some(typed) = &update.typed_at {
        format!("typed into {recipient} at {}", short_stamp(typed))
    } else if update.needs_approval == Some(true) {
        format!("waiting for {recipient}, which needs approval")
    } else {
        format!("waiting for {recipient}")
    }
}

fn recipient_label(party: &PartyView) -> String {
    let id = party.session_id.as_deref().unwrap_or_default();
    let id = if id.len() > 8 { &id[..8] } else { id };
    if id.is_empty() || party.name.starts_with(id) {
        party.name.clone()
    } else {
        format!("{} ({id})", party.name)
    }
}

fn authorization_summary(authorization: &SeedAuthorization) -> String {
    let mut summary = if authorization.text.is_empty() {
        format!(
            "cleared by {} at {}",
            actor_label(&authorization.set_by),
            short_stamp(&authorization.set_at)
        )
    } else {
        format!(
            "{}  set by {} at {}",
            q(&authorization.text),
            actor_label(&authorization.set_by),
            short_stamp(&authorization.set_at)
        )
    };
    if let Some(update) = &authorization.update {
        let _ = write!(summary, " · {}", authorization_update_state(update));
    }
    summary
}

fn capitalized(text: &str) -> String {
    let mut chars = text.chars();
    match chars.next() {
        Some(first) if first.is_ascii() => {
            format!("{}{}", first.to_ascii_uppercase(), chars.as_str())
        }
        _ => text.to_string(),
    }
}

pub fn authorization_reply(
    result: &SeedAuthorizationResult,
    changing: bool,
    clear: bool,
) -> String {
    let seed = &result.seed;
    if !changing {
        return match &seed.authorization {
            None => format!("{} has no authorization.", seed.id),
            Some(a) if a.text.is_empty() => {
                format!(
                    "{} has no authorization: {}",
                    seed.id,
                    authorization_summary(a)
                )
            }
            Some(a) => format!("{} authorization {}", seed.id, authorization_summary(a)),
        };
    }
    if !result.changed {
        if clear {
            return format!("{} has no authorization.", seed.id);
        }
        let mut reply = format!("{} already has this authorization.", seed.id);
        if let Some(update) = &result.update {
            let _ = write!(
                reply,
                " {}.",
                capitalized(&authorization_update_state(update))
            );
        }
        return reply;
    }
    let reply = if clear {
        format!("{} authorization cleared.", seed.id)
    } else {
        format!("{} authorization set.", seed.id)
    };
    match &result.update {
        Some(update) if update.typed_at.is_some() => {
            format!("{reply} Typed into {}.", recipient_label(&update.recipient))
        }
        Some(update) if update.needs_approval == Some(true) => format!(
            "{reply} Waiting for {}, which needs approval. attn types it in when it can take input.",
            recipient_label(&update.recipient)
        ),
        Some(update) => format!(
            "{reply} Waiting for {}. attn types it in when it can take input.",
            recipient_label(&update.recipient)
        ),
        None if result.caller_tends == Some(true) => {
            format!("{reply} You tend {}, so nothing is typed.", seed.id)
        }
        None if clear => format!("{reply} Nobody tends {}.", seed.id),
        None => format!(
            "{reply} Nobody tends {}; the next delegation's opening includes it.",
            seed.id
        ),
    }
}

pub fn reminder_line(reminder: &SeedReminder) -> String {
    format!(
        "at {}: {}  set by {}",
        short_stamp(&reminder.at),
        q(&reminder.message),
        or_dash(&actor_label(&reminder.set_by))
    )
}

fn board_holder(reference: &str, name: &str) -> String {
    reference
        .strip_prefix("session:")
        .map_or_else(|| name.to_string(), |id| id.chars().take(8).collect())
}

fn clip_line(line: &str, limit: usize) -> String {
    if line.chars().count() <= limit {
        return line.to_string();
    }
    let mut clipped: String = line.chars().take(limit - 1).collect();
    clipped.push('…');
    clipped
}

fn board(out: &mut String, plot_id: &str, rows: &[SeedBoardRow], closed: Option<&SeedClosedCount>) {
    let Some(closed) = closed else {
        return;
    };
    let _ = writeln!(out, "\nboard  {} open", rows.len());
    let mut lines = String::new();
    for row in rows {
        let child = &row.seed;
        let mut status = child.status.clone();
        if let Some(progress) = &child.plot_progress {
            let _ = write!(status, " · {}/{} done", progress.done, progress.total);
        }
        let waits = if row.waits_for.is_empty() {
            "-".to_string()
        } else {
            format!("waits for {}", row.waits_for.join(" "))
        };
        let newest = row.newest.as_ref().map_or_else(
            || "no entries".to_string(),
            |note| {
                format!(
                    "{}  {}{}  {}",
                    short_stamp(&note.created_at),
                    or_dash(&board_holder(&note.author.r#ref, &note.author.name)),
                    note_kind_suffix(&note.kind),
                    clip_line(first_line(&note.body), CLIP_WIDTH)
                )
            },
        );
        let tender = child
            .tender
            .as_ref()
            .map_or_else(String::new, |party| board_holder(&party.r#ref, &party.name));
        let tally = tally_short(&seed_or_plot_tally(child));
        let _ = writeln!(
            lines,
            "  {}\t{}\t{status}\t{}\t{waits}\t{}\t{newest}",
            child.id,
            child.step_slug,
            or_dash(&tender),
            or_dash(tally.trim().trim_matches(['[', ']']))
        );
    }
    out.push_str(&table(&lines));
    let mut folded = Vec::new();
    if closed.harvested > 0 {
        folded.push(format!("{} harvested", closed.harvested));
    }
    if closed.withered > 0 {
        folded.push(format!("{} withered", closed.withered));
    }
    if !folded.is_empty() {
        let _ = writeln!(
            out,
            "  and {} — attn seed ls --plot {plot_id}",
            folded.join(", ")
        );
    }
}

fn header(seed: &Seed, watching: bool) -> String {
    let mut rows = String::new();
    let _ = writeln!(rows, "{}\t{}", seed.id, seed.title);
    let _ = writeln!(rows, "slug\t{}", seed.step_slug);
    let _ = writeln!(rows, "status\t{}", seed.status);
    let _ = writeln!(
        rows,
        "planted\t{} by {}",
        short_stamp(&seed.created_at),
        or_dash(&actor_label(&seed.planter))
    );
    let _ = writeln!(
        rows,
        "tender\t{}",
        or_dash(&party_label(seed.tender.as_ref()))
    );
    if let Some(p) = &seed.plot_progress {
        let _ = writeln!(
            rows,
            "plot\t{} of {} done — {} growing, {} ready, {} blocked, {} parked, {} withered",
            p.done, p.total, p.growing, p.ready, p.blocked, p.parked, p.withered
        );
        let tally = tally_long(&plot_tally(Some(p)));
        if !tally.is_empty() {
            let _ = writeln!(rows, "plot checks\t{tally}");
        }
    }
    if seed.template {
        rows.push_str("packet\tyes\n");
    }
    if seed.gate {
        rows.push_str("gate\tyes\n");
    }
    if let Some(reason) = seed.reason.as_deref().filter(|r| !r.is_empty()) {
        let _ = writeln!(rows, "reason\t{reason}");
    }
    if let Some(condition) = &seed.harvest_when {
        let _ = writeln!(
            rows,
            "harvests when\t{} merges",
            pull_request_label(&condition.pull_request)
        );
        if let Some(reason) = condition.reason.as_deref().filter(|r| !r.is_empty()) {
            let _ = writeln!(rows, "harvest reason\t{reason}");
        }
        let checked = condition.checked_at.as_deref().unwrap_or_default().trim();
        let checked = if checked.is_empty() {
            "not yet".to_string()
        } else {
            short_stamp(checked)
        };
        let _ = writeln!(rows, "PR last checked\t{checked}");
    }
    if let Some(authorization) = &seed.authorization {
        let _ = writeln!(
            rows,
            "authorization\t{}",
            authorization_summary(authorization)
        );
    }
    if let Some(reminder) = &seed.reminder {
        let _ = writeln!(rows, "reminder\t{}", reminder_line(reminder));
    }
    if let Some(standby) = &seed.standby {
        let _ = writeln!(
            rows,
            "standby\t{}  set by {}",
            q(&standby.note),
            or_dash(&actor_label(&standby.set_by))
        );
        if let Some(blocked) = standby.resume_blocked.as_deref().filter(|b| !b.is_empty()) {
            let _ = writeln!(rows, "resume blocked\t{blocked}");
        }
    }
    if seed.ready {
        rows.push_str("ready\tyes\n");
    }
    if watching {
        rows.push_str("watching\tyes\n");
    }
    table(&rows)
}

fn relations_table(out: &mut String, relations: &[&SeedRelation]) {
    let mut rows = String::new();
    for relation in relations {
        let tally = relation
            .checks
            .as_ref()
            .map_or_else(String::new, tally_short);
        let _ = writeln!(
            rows,
            "{}\t{}\t{}\t{}{tally}",
            relation.label, relation.seed_id, relation.status, relation.title
        );
    }
    out.push_str(&table(&rows));
}

fn show_relations(out: &mut String, relations: &[SeedRelation]) {
    let mut edges = Vec::new();
    let mut discovered = Vec::new();
    let mut open_discovered = Vec::new();
    for relation in relations {
        match relation.label.as_str() {
            "has-part" => {}
            "discovered" => {
                discovered.push(relation.seed_id.as_str());
                if !closed_status(&relation.status) {
                    open_discovered.push(relation.seed_id.as_str());
                }
            }
            _ => edges.push(relation),
        }
    }
    if edges.is_empty() && discovered.is_empty() {
        return;
    }
    out.push('\n');
    relations_table(out, &edges);
    if !discovered.is_empty() {
        let mut line = format!("discovered  {}", plural(count(discovered.len()), "seed"));
        if !open_discovered.is_empty() {
            let _ = write!(line, ", open: {}", open_discovered.join(" "));
        }
        let _ = writeln!(out, "{line}");
    }
}

fn parent_outline(out: &mut String, result: &SeedShowResult) {
    let parts = result.parent_outline.as_deref().unwrap_or_default();
    let parent = result.relations.iter().find(|r| r.label == EDGE_PART_OF);
    let Some(parent) = parent.filter(|_| !parts.is_empty()) else {
        return;
    };
    let _ = writeln!(out, "\noutline of {}", parent.seed_id);
    let width = parts
        .iter()
        .map(|part| part.heading.chars().count())
        .max()
        .unwrap_or(0);
    for part in parts {
        let pad = " ".repeat(width - part.heading.chars().count());
        let line = format!(
            "  {}{pad}  {:>7}  {}",
            part.heading,
            human_bytes(part.bytes),
            id_ranges(&part.ids)
        );
        let _ = writeln!(out, "{}", line.trim_end_matches(' '));
    }
    let _ = writeln!(
        out,
        "  read one part: attn seed show {} --section <heading|id>",
        parent.seed_id
    );
}

fn handoff_pointer(out: &mut String, result: &SeedShowResult) {
    let Some(handoff) = &result.handoff else {
        return;
    };
    if result.notes.iter().any(|note| note.id == handoff.id) {
        return;
    }
    let back = match result.handoff_newer {
        None => String::new(),
        Some(1) => ", 1 entry back".to_string(),
        Some(newer) => format!(", {newer} entries back"),
    };
    let _ = writeln!(
        out,
        "\nhandoff  {} old{back} — attn seed notes {} --kind handoff",
        last_active(&handoff.created_at),
        result.seed.id
    );
}

fn resume_line(out: &mut String, seed: &Seed) {
    let Some(continuation) = &seed.continuation else {
        return;
    };
    if continuation.session_live || closed_status(&seed.status) {
        return;
    }
    let outpost = continuation.outpost.as_deref().unwrap_or_default();
    if !continuation.resume_available {
        if let Some(reason) = continuation
            .resume_reason
            .as_deref()
            .filter(|r| !r.is_empty())
        {
            let _ = writeln!(out, "resume  unavailable: {reason}");
        }
    } else if !outpost.is_empty() {
        let _ = writeln!(out, "resume  on outpost {outpost}");
    } else {
        out.push_str("resume  available\n");
    }
}

fn kept_conversation_line(out: &mut String, seed: &Seed) {
    let Some(kept) = seed
        .continuation
        .as_ref()
        .and_then(|continuation| continuation.kept_conversation.as_ref())
    else {
        return;
    };
    let deleted_at = kept.deleted_at.as_deref().unwrap_or_default();
    let pinned_at = kept.pinned_at.as_deref().unwrap_or_default();
    let delete_after = kept.delete_after.as_deref().unwrap_or_default();
    if !deleted_at.is_empty() && kept.deleted_by.as_deref() == Some("user") {
        let _ = writeln!(
            out,
            "conversation  you deleted attn's copy on {}",
            conversation_date(deleted_at)
        );
    } else if !deleted_at.is_empty() {
        let _ = writeln!(
            out,
            "conversation  attn deleted its copy on {}",
            conversation_date(deleted_at)
        );
    } else if !pinned_at.is_empty() {
        let _ = writeln!(
            out,
            "conversation  kept by attn ({}) forever (pinned {})",
            conversation_bytes(kept.bytes),
            conversation_date(pinned_at)
        );
    } else if !delete_after.is_empty() {
        let _ = writeln!(
            out,
            "conversation  kept by attn ({}) until {}; replant to keep it",
            conversation_bytes(kept.bytes),
            conversation_date(delete_after)
        );
    } else {
        let _ = writeln!(
            out,
            "conversation  kept by attn ({}) while an open seed points at it",
            conversation_bytes(kept.bytes)
        );
    }
}

fn watch_coverage(out: &mut String, seed_ids: &[String]) {
    for id in seed_ids {
        let _ = writeln!(
            out,
            "watching via {id} (remove with attn seed unwatch {id})"
        );
    }
}

fn artifacts(out: &mut String, items: &[SeedArtifact]) {
    let _ = writeln!(out, "artifacts: {SESSION_SEED_ARTIFACTS}");
    for item in items {
        let _ = writeln!(out, "  file  {}  {} bytes", item.filename, item.size);
    }
}

fn artifact_references(out: &mut String, items: &[SeedArtifactReference]) {
    let _ = writeln!(out, "linked artifacts: {SESSION_SEED_ARTIFACTS}");
    for item in items {
        let path = item.path.as_deref().unwrap_or_default();
        let notebook = item.notebook_document_id.as_deref().unwrap_or_default();
        let url = item.url.as_deref().unwrap_or_default();
        let repository = item.repository.as_deref().unwrap_or_default();
        let mut line = [path, notebook, url, repository]
            .into_iter()
            .find(|value| !value.is_empty())
            .unwrap_or_default()
            .to_string();
        if !repository.is_empty() && repository != line {
            let _ = write!(line, " ({repository})");
        }
        let _ = writeln!(out, "  {}  {line}", item.kind);
    }
}

fn holdings(out: &mut String, result: &SeedShowResult) {
    out.push('\n');
    if let Some(directory) = &result.artifact_dir {
        let _ = writeln!(out, "artifact folder  {directory}");
    }
    resume_line(out, &result.seed);
    kept_conversation_line(out, &result.seed);
    watch_coverage(out, &result.watching_via);
    if !result.artifacts.is_empty() {
        artifacts(out, &result.artifacts);
    }
    if !result.references.is_empty() {
        artifact_references(out, &result.references);
    }
}

pub fn show(result: &SeedShowResult, bookmark_at: i64) -> String {
    let seed = &result.seed;
    let mut out = header(seed, result.watching);
    bookmark_line(&mut out, "show", &seed.id, bookmark_at, "");
    if !result.notes.is_empty() {
        out.push('\n');
        notes(
            &mut out,
            &result.notes,
            &seed.id,
            result.notes_total - count(result.notes.len()),
        );
    }
    handoff_pointer(&mut out, result);
    show_relations(&mut out, &result.relations);
    parent_outline(&mut out, result);
    board(
        &mut out,
        &seed.id,
        result.board.as_deref().unwrap_or_default(),
        result.closed_children.as_ref(),
    );
    render_seed_checks(&mut out, seed);
    holdings(&mut out, result);
    let body = seed.body.trim_end_matches('\n');
    if !body.is_empty() {
        let _ = writeln!(out, "\n{body}");
    }
    out
}

pub fn watch_result(result: &SeedWatchResult, unwatch: bool) -> String {
    let mut out = String::new();
    if unwatch {
        if result.changed {
            let _ = writeln!(out, "removed watch on {}", result.seed_id);
        } else {
            let _ = writeln!(out, "no direct watch on {}", result.seed_id);
        }
        if result.watching {
            watch_coverage(&mut out, &result.watching_via);
        } else {
            let _ = writeln!(out, "no remaining watch covers {}", result.seed_id);
        }
        return out;
    }
    if result.changed {
        let _ = writeln!(out, "watching {} and its descendants", result.seed_id);
    } else {
        let _ = writeln!(out, "already watching {}", result.seed_id);
    }
    out
}

fn unblocked(out: &mut String, seeds: &[Seed]) {
    if seeds.is_empty() {
        return;
    }
    let _ = writeln!(out, "this unblocked {} seed(s):", seeds.len());
    let mut free = false;
    let mut rows = String::new();
    for seed in seeds {
        let tender = party_label(seed.tender.as_ref());
        let held = if tender.is_empty() {
            free = true;
            String::new()
        } else {
            format!(" — tended by {tender}")
        };
        let _ = writeln!(
            rows,
            "  {}\t{}\t{}{held}",
            seed.id,
            or_dash(&seed.step_slug),
            seed.title
        );
    }
    out.push_str(&table(&rows));
    if free {
        out.push_str("`attn seed tend <id>` claims one\n");
    }
}

fn transition_line(seed: &Seed) -> String {
    let mut line = format!("{} is {}", seed_handle(seed), seed.status);
    let tender = party_label(seed.tender.as_ref());
    if !tender.is_empty() {
        let _ = write!(line, ", tended by {tender}");
    }
    if let Some(reason) = seed.reason.as_deref().filter(|r| !r.is_empty()) {
        let _ = write!(line, " — {reason}");
    }
    line
}

fn handoff_block(out: &mut String, handoff: &SeedNote) {
    let _ = writeln!(
        out,
        "handoff — {}, {}",
        or_dash(&actor_label(&handoff.author)),
        short_stamp(&handoff.created_at)
    );
    for line in handoff.body.trim_end_matches('\n').split('\n') {
        let _ = writeln!(out, "  {line}");
    }
    out.push('\n');
}

pub fn transition(result: &SeedTransitionResult, cleared: bool) -> String {
    let mut out = String::new();
    let seed = &result.seed;
    let not_moved = result.moved == Some(false);
    if not_moved {
        let _ = write!(
            out,
            "{} was already {} at {}",
            seed_handle(seed),
            seed.status,
            short_stamp(&seed.state_changed_at)
        );
        if let Some(reason) = seed.reason.as_deref().filter(|r| !r.is_empty()) {
            let _ = write!(out, " — {reason}");
        }
        out.push('\n');
    } else {
        let _ = writeln!(out, "{}", transition_line(seed));
    }
    if let Some(audit) = &result.audit {
        let _ = writeln!(out, "{}", audit.body);
    }
    if result.noted.is_some() {
        let _ = writeln!(
            out,
            "your reason is on its log: attn seed notes {}",
            seed.id
        );
    }
    let harvest = harvest_when_line(seed.harvest_when.as_ref());
    if !harvest.is_empty() {
        let _ = writeln!(out, "{harvest}");
        out.push_str("this command merges nothing\n");
    } else if cleared {
        out.push_str("harvest-on-merge cleared\n");
    }
    let open = unsettled(seed.checks.as_deref().unwrap_or_default());
    if !open.is_empty() && closed_status(&seed.status) {
        let _ = writeln!(
            out,
            "checks: {open} Checks never block a move; the tally stays on the seed."
        );
    }
    if let Some(plot) = &result.plot_emptied {
        if closed_status(&plot.status) {
            let _ = writeln!(
                out,
                "{} has no open child seeds left and stays {}",
                seed_handle(plot),
                plot.status
            );
        } else {
            let _ = writeln!(
                out,
                "{} has no open child seeds left and stays open: close it or plan its next step",
                seed_handle(plot)
            );
        }
    }
    unblocked(&mut out, result.unblocked.as_deref().unwrap_or_default());
    if not_moved {
        out.push_str("rang nobody: the seed did not move\n");
    } else {
        plural_rang(&mut out, &seed.id, result.ring_recipients.as_deref());
    }
    if let Some(handoff) = &result.handoff {
        out.push('\n');
        handoff_block(&mut out, handoff);
    }
    out
}

fn short_session(id: &str) -> &str {
    if id.len() <= AGENT_SHORT_ID_LENGTH {
        id
    } else {
        &id[..AGENT_SHORT_ID_LENGTH]
    }
}

pub fn agent_close(out: &mut String, result: &AgentCloseResult) {
    let id = short_session(&result.target_session_id);
    if result.pending_compact == Some(true) {
        let _ = writeln!(
            out,
            "session {id} ({}) closes after this turn, once its conversation is compacted: {}",
            result.label, result.reason
        );
        out.push_str("end your turn now\n");
        return;
    }
    let _ = writeln!(
        out,
        "closed session {id} ({}): {}",
        result.label, result.reason
    );
    for seed_id in &result.seed_ids {
        let _ = writeln!(out, "noted on {seed_id}, which it was tending");
    }
    let _ = writeln!(
        out,
        "the session is kept: `attn session show {id}` reads it back"
    );
}
