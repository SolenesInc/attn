//! What the Analytics page lists, and how it words times, builds and destinations.

use std::collections::HashSet;
use std::sync::Arc;

use attn_protocol::{
    AnalyticsBuild, AnalyticsOutboxRow, GetAnalyticsStatusResultMessage, Seed, Vent,
};

use crate::sidebar::queue::epoch_ms;

pub(super) const VENT_TOOLS: &str = "tools";
pub(super) const VENT_ATTN: &str = "attn";
pub(super) const SEND_QUEUED: &str = "queued";
pub(super) const SEND_SENT: &str = "sent";

/// The lists the page switches between: the vents, every record, then each record stream.
pub(super) const VIEWS: [(&str, &str); 7] = [
    ("vents", "Vents"),
    ("all", "All records"),
    ("bus", "bus"),
    ("app", "app"),
    ("cli", "cli"),
    ("sample", "sample"),
    ("run", "run"),
];

pub(super) const VENTS: usize = 0;
pub(super) const ALL: usize = 1;

/// One stored record, with the fields the list shows read out of it.
pub(super) struct Stored {
    pub(super) id: i64,
    pub(super) raw: String,
    pub(super) stream: String,
    pub(super) seq: i64,
    pub(super) name: String,
    pub(super) at: String,
}

impl Stored {
    pub(super) fn parse(row: AnalyticsOutboxRow) -> Stored {
        let parsed: serde_json::Value = serde_json::from_str(&row.record).unwrap_or_default();
        let field = |name: &str| match &parsed[name] {
            serde_json::Value::String(s) => s.clone(),
            serde_json::Value::Null => String::new(),
            other => other.to_string(),
        };
        Stored {
            id: row.id,
            stream: field("stream"),
            seq: parsed["seq"].as_i64().unwrap_or_default(),
            name: field("name"),
            at: field("at"),
            raw: row.record,
        }
    }
}

pub(super) enum Item<'a> {
    Vent {
        vent: &'a Vent,
        done: bool,
    },
    Record(&'a Stored),
    /// Records of one name in the same second, folded into one row.
    Burst(&'a [Stored]),
}

impl Item<'_> {
    pub(super) fn key(&self) -> String {
        match self {
            Item::Vent { vent, .. } => vent.id.clone(),
            Item::Record(record) => format!("record-{}", record.id),
            Item::Burst(records) => burst_key(records),
        }
    }
}

fn burst_key(records: &[Stored]) -> String {
    format!("burst-{}", records[0].id)
}

pub(super) fn is_done(vent: &Vent) -> bool {
    vent.dismissed_at.is_some() || vent.withdrawn_at.is_some() || vent.planted_seed_id.is_some()
}

/// The new vents in the daemon's order, then the done ones, most recently done first.
pub(super) fn vent_items<'a>(vents: &'a [Vent], seeds: &[Arc<Seed>]) -> Vec<Item<'a>> {
    let done_at = |vent: &Vent| {
        let planted = vent
            .planted_seed_id
            .as_deref()
            .and_then(|id| seeds.iter().find(|s| s.id == id))
            .map(|s| s.created_at.as_str());
        let at = vent
            .withdrawn_at
            .as_deref()
            .or(vent.dismissed_at.as_deref())
            .or(planted)
            .unwrap_or(&vent.created_at);
        epoch_ms(at).unwrap_or_default()
    };
    let mut done: Vec<&Vent> = vents.iter().filter(|v| is_done(v)).collect();
    done.sort_by_key(|v| std::cmp::Reverse(done_at(v)));
    vents
        .iter()
        .filter(|v| !is_done(v))
        .map(|vent| Item::Vent { vent, done: false })
        .chain(done.into_iter().map(|vent| Item::Vent { vent, done: true }))
        .collect()
}

/// The records newest first, with each same-second run of one name folded unless it is open.
pub(super) fn record_items<'a>(records: &'a [Stored], open: &HashSet<String>) -> Vec<Item<'a>> {
    let same_second = |a: &Stored, b: &Stored| {
        a.name == b.name && a.at.get(..19).unwrap_or(&a.at) == b.at.get(..19).unwrap_or(&b.at)
    };
    let mut items = Vec::new();
    let mut i = 0;
    while i < records.len() {
        let mut j = i;
        while j + 1 < records.len() && same_second(&records[j + 1], &records[i]) {
            j += 1;
        }
        let run = &records[i..=j];
        if run.len() > 1 && !open.contains(&burst_key(run)) {
            items.push(Item::Burst(run));
        } else {
            items.extend(run.iter().map(Item::Record));
        }
        i = j + 1;
    }
    items
}

/// Where a vent is: kept here, waiting to go, or sent; and whether x can move it.
pub(super) struct Destination {
    pub(super) label: &'static str,
    pub(super) note: &'static str,
    pub(super) leaving: bool,
    pub(super) movable: bool,
}

pub(super) fn destination(vent: &Vent, sharing: bool) -> Destination {
    let withdrawn = vent.withdrawn_at.is_some();
    let send = vent.send.as_deref();
    let kept = |note, movable| Destination {
        label: "Kept on this machine",
        note,
        leaving: false,
        movable,
    };
    if send == Some(SEND_SENT) {
        return Destination {
            label: "Sent to attn's maintainers",
            note: "",
            leaving: true,
            movable: false,
        };
    }
    if vent.kind == VENT_TOOLS {
        return kept("tool vents never leave it", false);
    }
    if !withdrawn && send == Some(SEND_QUEUED) {
        return Destination {
            label: if sharing {
                "Sending with the next batch"
            } else {
                "Sending once sharing is on"
            },
            note: "",
            leaving: true,
            movable: true,
        };
    }
    let note = if withdrawn {
        "withdrawn"
    } else if sharing {
        ""
    } else {
        "sharing is off"
    };
    kept(note, sharing && !withdrawn)
}

const WEEKDAYS: [&str; 7] = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const MONTHS: [&str; 12] = [
    "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
];

/// "14:02:11" on the user's clock.
pub(super) fn clock_time(at: &str) -> String {
    let Some(ms) = epoch_ms(at) else {
        return String::new();
    };
    let local = rune::time::LocalTime::at(ms);
    format!("{:02}:{:02}:{:02}", local.hour, local.minute, local.second)
}

/// "Sat, Oct 24, 14:02:11" on the user's calendar and clock.
pub(super) fn when(at: &str) -> String {
    let Some(ms) = epoch_ms(at) else {
        return at.to_owned();
    };
    let local = rune::time::LocalTime::at(ms);
    let weekday = WEEKDAYS[crate::calendar::weekday(crate::calendar::local_day(ms)) as usize];
    let month = MONTHS[(local.month as usize).saturating_sub(1).min(11)];
    format!("{weekday}, {month} {}, {}", local.day, clock_time(at))
}

/// How old `at` is in its largest whole unit: "now", "12m", "3h", "2d".
pub(super) fn age(at: &str, now_ms: i64) -> String {
    let Some(ms) = epoch_ms(at) else {
        return String::new();
    };
    let seconds = (now_ms - ms).max(0) / 1000;
    match seconds {
        s if s < 60 => "now".into(),
        s if s < 3600 => format!("{}m", s / 60),
        s if s < 86_400 => format!("{}h", s / 3600),
        s => format!("{}d", s / 86_400),
    }
}

/// `age` in words: "just now", "12 min ago", "3 h ago", "2 days ago".
pub(super) fn long_ago(at: &str, now_ms: i64) -> String {
    let short = age(at, now_ms);
    if short == "now" || short.is_empty() {
        return "just now".into();
    }
    let (n, unit) = short.split_at(short.len() - 1);
    match unit {
        "m" => format!("{n} min ago"),
        "h" => format!("{n} h ago"),
        _ => format!("{n} days ago"),
    }
}

/// "Source a3f9e1c, uncommitted changes" or "Stable 0.42.1".
pub(super) fn build_label(build: &AnalyticsBuild) -> String {
    let mut chars = build.channel.chars();
    let channel = match chars.next() {
        Some(first) => first.to_uppercase().chain(chars).collect(),
        None => "Build".to_owned(),
    };
    let version = if build.channel == "source" && !build.commit.is_empty() {
        build.commit.chars().take(7).collect()
    } else {
        build.version.clone()
    };
    let dirty = if build.dirty {
        ", uncommitted changes"
    } else {
        ""
    };
    format!("{channel} {version}{dirty}")
}

/// The model an agent ran, and the one asked for when they differ.
pub(super) fn model_label(reported: Option<&str>, requested: Option<&str>) -> String {
    match (reported, requested) {
        (reported, None) => reported.unwrap_or_default().to_owned(),
        (Some(reported), Some(requested)) if reported == requested => reported.to_owned(),
        (None, Some(requested)) => format!("requested {requested}"),
        (Some(reported), Some(requested)) => format!("{reported} (requested {requested})"),
    }
}

/// "asks_enabled" as "Asks".
pub(super) fn setting_name(key: &str) -> String {
    let bare = key
        .strip_suffix(".enabled")
        .or_else(|| key.strip_suffix("_enabled"))
        .unwrap_or(key);
    let words = bare.replace(['.', '_'], " ");
    let mut chars = words.chars();
    match chars.next() {
        Some(first) => first.to_uppercase().chain(chars).collect(),
        None => String::new(),
    }
}

/// "Last sent 14:00:12 · 12 waiting", or "Nothing sent yet · 0 waiting".
pub(super) fn sharing_line(status: &GetAnalyticsStatusResultMessage) -> String {
    let sent = match status.last_sent_at.as_deref() {
        Some(at) => format!("Last sent {}", clock_time(at)),
        None => "Nothing sent yet".to_owned(),
    };
    format!("{sent} · {} waiting", count(status.waiting))
}

pub(super) fn record_count(n: i64) -> String {
    format!("{} record{}", count(n), if n == 1 { "" } else { "s" })
}

pub(super) use super::super::bus::{bytes, count};
