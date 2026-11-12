use std::cmp::Ordering;
use std::collections::{HashMap, HashSet};

use attn_client::Facts;
use attn_protocol::Session;

use super::model::{desktop_title, ordered_desktops};
use crate::setting;

pub const QUEUE_MODE: &str = "queue_mode_enabled";
pub const QUEUE_CREW: &str = "queue_crew_enabled";
pub const QUEUE_SHOW_HIDDEN: &str = "queue_show_hidden_sessions";

/// One agent in the queue, and the desktop it sits on (none for a hidden session).
#[derive(Clone, Debug)]
pub struct QueueRow<'a> {
    pub session: &'a Session,
    pub desktop: Option<&'a str>,
    pub desktop_title: String,
}

/// The queue sidebar's standing order. Every queue participant lands in one band;
/// automation runs and shells land in none.
#[derive(Clone, Debug, Default)]
pub struct Bands<'a> {
    pub chief: Option<QueueRow<'a>>,
    pub turns: Vec<QueueRow<'a>>,
    pub settled: Vec<QueueRow<'a>>,
    pub crew: Vec<QueueRow<'a>>,
    pub snoozed: Vec<QueueRow<'a>>,
}

/// An RFC 3339 instant as milliseconds since 1970, or None when it does not parse.
pub fn epoch_ms(instant: &str) -> Option<i64> {
    let (date, rest) = instant.split_once('T')?;
    let mut d = date.splitn(3, '-').map(|p| p.parse::<i64>().ok());
    let (y, m, day) = (d.next()??, d.next()??, d.next()??);
    let zone_at = rest.find(['Z', 'z', '+', '-']).unwrap_or(rest.len());
    let (time, zone) = rest.split_at(zone_at);
    let mut t = time.splitn(3, ':');
    let h: i64 = t.next()?.parse().ok()?;
    let min: i64 = t.next()?.parse().ok()?;
    let sec: f64 = t.next().unwrap_or("0").parse().ok()?;
    let offset_min = match zone.chars().next() {
        None | Some('Z' | 'z') => 0,
        Some(sign) => {
            let (zh, zm) = zone[1..].split_once(':').unwrap_or((&zone[1..], "0"));
            let minutes = zh.parse::<i64>().ok()? * 60 + zm.parse::<i64>().ok()?;
            if sign == '-' { -minutes } else { minutes }
        }
    };
    let (y, m) = if m <= 2 { (y - 1, m + 9) } else { (y, m - 3) };
    let era = y.div_euclid(400);
    let yoe = y - era * 400;
    let doy = (153 * m + 2) / 5 + day - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    let days = era * 146_097 + doe - 719_468;
    let seconds = days * 86_400 + h * 3600 + min * 60 - offset_min * 60;
    Some(seconds * 1000 + (sec * 1000.0).round() as i64)
}

pub fn is_snoozed(until: Option<&str>, now_ms: i64) -> bool {
    until.and_then(epoch_ms).is_some_and(|at| at > now_ms)
}

/// How long a turn has been owed, in the coarsest unit that still reads as an age.
pub fn turn_age(opened_at: Option<&str>, now_ms: i64) -> String {
    let Some(opened) = opened_at.and_then(epoch_ms) else {
        return String::new();
    };
    let round = |n: i64, d: i64| (n + d / 2) / d;
    let seconds = round((now_ms - opened).max(0), 1000);
    if seconds < 60 {
        return "now".into();
    }
    let minutes = round(seconds, 60);
    if minutes < 60 {
        return format!("{minutes}m");
    }
    let hours = round(minutes, 60);
    if hours < 24 {
        return format!("{hours}h");
    }
    format!("{}d", round(hours, 24))
}

/// When `turn_age` next reads differently, so the window wakes then and not before. An age
/// only grows, so the first instant that reads differently is found by halving.
pub fn next_age_change(opened_at: Option<&str>, now_ms: i64) -> Option<i64> {
    opened_at.and_then(epoch_ms)?;
    let reads = turn_age(opened_at, now_ms);
    let (mut same, mut differs) = (now_ms, now_ms + 86_400_000);
    if turn_age(opened_at, differs) == reads {
        return Some(differs);
    }
    while differs - same > 1 {
        let mid = same + (differs - same) / 2;
        if turn_age(opened_at, mid) == reads {
            same = mid;
        } else {
            differs = mid;
        }
    }
    Some(differs)
}

fn compare_turn_order(a: &Session, b: &Session) -> Ordering {
    let priority = |s: &Session| s.priority != Some(true);
    priority(a)
        .cmp(&priority(b))
        .then_with(|| {
            a.turn_opened_at
                .as_deref()
                .unwrap_or("")
                .cmp(b.turn_opened_at.as_deref().unwrap_or(""))
        })
        .then_with(|| a.id.cmp(&b.id))
}

fn compare_wake_order(a: &Session, b: &Session) -> Ordering {
    a.turn_snoozed_until
        .as_deref()
        .unwrap_or("")
        .cmp(b.turn_snoozed_until.as_deref().unwrap_or(""))
        .then_with(|| a.id.cmp(&b.id))
}

/// Whether an agent takes part in the queue at all: never a command terminal or an automation
/// run, and a crew member only when the crew queues.
pub fn participates(session: &Session, crew_in_queue: bool) -> bool {
    session.command_terminal != Some(true)
        && session.automation.is_none()
        && (session.crew_member.as_deref().unwrap_or("").is_empty() || crew_in_queue)
}

/// What the queue lets the user do to an agent, as the Tauri app's `queueActions`.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub struct QueueActions {
    pub settle: bool,
    pub snooze: bool,
}

pub fn actions(facts: &Facts, session: &Session, now_ms: i64) -> QueueActions {
    if is_snoozed(session.turn_snoozed_until.as_deref(), now_ms) {
        return QueueActions::default();
    }
    let settings = &facts.settings;
    let in_queue = setting::on(settings, QUEUE_MODE)
        && participates(session, setting::on(settings, QUEUE_CREW));
    QueueActions {
        settle: session.turn_owed == Some(true) && (in_queue || session.automation.is_some()),
        snooze: in_queue,
    }
}

/// The queue the selected profile's sessions make at `now_ms`, in the Tauri app's
/// `buildQueueBands` order.
pub fn bands(facts: &Facts, now_ms: i64) -> Bands<'_> {
    let settings = &facts.settings;
    let crew_in_queue = setting::on(settings, QUEUE_CREW);
    let desktops = ordered_desktops(&facts.desktops);
    let desktop_of: HashMap<&str, &str> = desktops
        .iter()
        .flat_map(|d| {
            d.panes
                .iter()
                .map(|p| (p.session_id.as_str(), d.id.as_str()))
        })
        .collect();
    let mut places: Vec<(Option<&str>, String, Vec<&Session>)> = desktops
        .iter()
        .map(|d| {
            let sessions = facts
                .sessions
                .iter()
                .filter(|s| desktop_of.get(s.id.as_str()) == Some(&d.id.as_str()))
                .collect();
            (
                Some(d.id.as_str()),
                desktop_title(d, &facts.desktops),
                sessions,
            )
        })
        .collect();
    if setting::on(settings, QUEUE_SHOW_HIDDEN) {
        let hidden = facts
            .sessions
            .iter()
            .filter(|s| s.hidden == Some(true) && !desktop_of.contains_key(s.id.as_str()))
            .collect();
        places.push((None, String::new(), hidden));
    }

    let mut bands = Bands::default();
    for (desktop, title, sessions) in &places {
        for &session in sessions {
            if session.command_terminal == Some(true) {
                continue;
            }
            let row = QueueRow {
                session,
                desktop: *desktop,
                desktop_title: title.clone(),
            };
            if session.chief_of_staff == Some(true) && bands.chief.is_none() {
                bands.chief = Some(row.clone());
            }
            if session.automation.is_some() {
                continue;
            }
            if !session.crew_member.as_deref().unwrap_or("").is_empty() {
                bands.crew.push(row.clone());
                if !crew_in_queue {
                    continue;
                }
            }
            if session.parent_session_id.as_deref().is_some_and(|parent| {
                desktop_of.get(parent).copied() == *desktop && desktop.is_some()
            }) {
                continue;
            }
            if is_snoozed(session.turn_snoozed_until.as_deref(), now_ms) {
                bands.snoozed.push(row);
            } else if session.turn_owed == Some(true) {
                bands.turns.push(row);
            } else {
                bands.settled.push(row);
            }
        }
    }
    bands
        .turns
        .sort_by(|a, b| compare_turn_order(a.session, b.session));
    bands
        .snoozed
        .sort_by(|a, b| compare_wake_order(a.session, b.session));
    bands.crew.sort_by(|a, b| {
        a.session
            .crew_member
            .cmp(&b.session.crew_member)
            .then_with(|| a.session.id.cmp(&b.session.id))
    });
    bands
}

impl Bands<'_> {
    /// The turn at the head of the queue.
    pub fn head(&self) -> Option<&Session> {
        self.turns.first().map(|row| row.session)
    }

    /// The agent `step` rows from `from` in the order the sidebar lists the queue:
    /// waiting, working, snoozed. An agent off the queue steps onto its first or last row.
    pub fn step(&self, from: Option<&str>, step: isize) -> Option<&Session> {
        let order: Vec<&Session> = self
            .turns
            .iter()
            .chain(&self.settled)
            .chain(&self.snoozed)
            .map(|row| row.session)
            .collect();
        if order.is_empty() {
            return None;
        }
        let n = order.len() as isize;
        let next = match order.iter().position(|s| Some(s.id.as_str()) == from) {
            None if step > 0 => 0,
            None => n - 1,
            Some(at) => (at as isize + step).rem_euclid(n),
        };
        Some(order[next as usize])
    }

    /// Where the user lands when the turn of the agent on screen closed without them asking:
    /// `Some(None)` is home, `None` is stay put.
    pub fn advance_after_turn_closed(
        &self,
        previous_turns: &[String],
        on_screen: Option<&str>,
    ) -> Option<Option<String>> {
        let id = on_screen?;
        if !previous_turns.iter().any(|t| t == id) {
            return None;
        }
        let closed = self
            .settled
            .iter()
            .chain(&self.snoozed)
            .find(|row| row.session.id == id)?;
        if closed.session.turn_by_agent == Some(true) {
            return None;
        }
        if let Some(head) = self.head().filter(|h| h.priority == Some(true)) {
            return Some(Some(head.id.clone()));
        }
        let still: HashSet<&str> = self.turns.iter().map(|r| r.session.id.as_str()).collect();
        let at = previous_turns
            .iter()
            .position(|t| t == id)
            .map_or(0, |at| at + 1);
        let n = previous_turns.len();
        let next = (0..n)
            .map(|offset| &previous_turns[(at + offset) % n])
            .find(|t| *t != id && still.contains(t.as_str()))
            .cloned()
            .or_else(|| self.head().map(|h| h.id.clone()));
        Some(next)
    }
}

const WEEKDAYS: [&str; 7] = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

/// When a snooze ends, as the queue row shows it: "14:30" today, "tmrw 9:00", or
/// "Sat 9:00", in the user's time zone.
pub fn wake_time_short(until: Option<&str>, now_ms: i64) -> String {
    let Some(at) = until.and_then(epoch_ms) else {
        return String::new();
    };
    let local = rune::time::LocalTime::at(at);
    let time = format!("{}:{:02}", local.hour, local.minute);
    let day = |ms: i64| {
        let l = rune::time::LocalTime::at(ms);
        (l.year, l.month, l.day)
    };
    let date = day(at);
    if date == day(now_ms) {
        return time;
    }
    if date == day(now_ms + 86_400_000) {
        return format!("tmrw {time}");
    }
    let days = (at.div_euclid(1000) + i64::from(local_offset(at))).div_euclid(86_400);
    let weekday = WEEKDAYS[(days + 4).rem_euclid(7) as usize];
    format!("{weekday} {time}")
}

fn local_offset(at: i64) -> i32 {
    let local = rune::time::LocalTime::at(at);
    let utc = rune::time::LocalTime::at_offset(at, 0);
    let minutes = |l: rune::time::LocalTime| i64::from(l.hour) * 60 + i64::from(l.minute);
    let mut diff = minutes(local) - minutes(utc);
    if (local.year, local.month, local.day) > (utc.year, utc.month, utc.day) {
        diff += 24 * 60;
    } else if (local.year, local.month, local.day) < (utc.year, utc.month, utc.day) {
        diff -= 24 * 60;
    }
    (diff * 60) as i32
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_turn_age_reads_how_long_the_turn_has_been_owed_as_format_turn_age_does() {
        let opened = epoch_ms("2026-07-26T10:00:00Z").unwrap();
        for (stamp, after_ms, reads) in [
            (Some("2026-07-26T10:00:00Z"), 30_000, "now"),
            (Some("2026-07-26T10:00:00Z"), 12 * 60_000, "12m"),
            (Some("2026-07-26T10:00:00Z"), 3 * 3_600_000, "3h"),
            (Some("2026-07-26T10:00:00Z"), 50 * 3_600_000, "2d"),
            (Some("2026-07-26T12:00:00+02:00"), 12 * 60_000, "12m"),
            (Some("2026-07-26T09:30:00.250-00:30"), 12 * 60_000, "12m"),
            (Some("2026-07-26T10:00:00Z"), -60_000, "now"),
            (None, 0, ""),
            (Some("not a date"), 0, ""),
        ] {
            assert_eq!(
                turn_age(stamp, opened + after_ms),
                reads,
                "{stamp:?} +{after_ms}"
            );
        }
    }

    #[test]
    fn the_next_age_change_is_the_first_instant_that_reads_differently() {
        let stamp = Some("2026-07-26T10:00:00Z");
        let opened = epoch_ms("2026-07-26T10:00:00Z").unwrap();
        for after_ms in [
            0,
            29_499,
            29_500,
            89_499,
            3_570_000,
            86_000_000,
            200_000_000,
        ] {
            let now = opened + after_ms;
            let next = next_age_change(stamp, now).unwrap();
            assert_ne!(turn_age(stamp, next), turn_age(stamp, now), "+{after_ms}");
            assert_eq!(
                turn_age(stamp, next - 1),
                turn_age(stamp, now),
                "+{after_ms}"
            );
        }
    }
}
