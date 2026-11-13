//! What the Sessions list asks the daemon for, and how its rows read: the typed query, the date
//! window, and the words a row shows. Ported from the Tauri app's `ledgerQuery.ts`,
//! `ledgerTime.ts` and `sessionsLedger.ts`.

use attn_protocol::{SessionLedgerEntry, SessionLedgerFacets, SessionListMessage, SessionReopen};
use rune::time::LocalTime;
use serde::{Deserialize, Serialize};

use crate::calendar::{DAY_S, day_of_date, iso, local_day, local_instant};

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Scope {
    Live,
    Closed,
    #[default]
    All,
}

impl Scope {
    pub const ALL: [Scope; 3] = [Scope::Live, Scope::Closed, Scope::All];

    pub fn label(self) -> &'static str {
        match self {
            Scope::Live => "Live",
            Scope::Closed => "Closed",
            Scope::All => "All",
        }
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum Range {
    #[default]
    #[serde(rename = "any")]
    Any,
    #[serde(rename = "today")]
    Today,
    #[serde(rename = "yesterday")]
    Yesterday,
    #[serde(rename = "7d")]
    Week,
    #[serde(rename = "30d")]
    Month,
    #[serde(rename = "custom")]
    Custom,
}

impl Range {
    fn token(self) -> Option<&'static str> {
        match self {
            Range::Today => Some("today"),
            Range::Yesterday => Some("yesterday"),
            Range::Week => Some("7d"),
            Range::Month => Some("30d"),
            Range::Any | Range::Custom => None,
        }
    }

    fn of_word(word: &str) -> Option<Range> {
        Some(match word {
            "today" => Range::Today,
            "yesterday" => Range::Yesterday,
            "7d" | "week" => Range::Week,
            "30d" | "month" => Range::Month,
            _ => return None,
        })
    }
}

/// What the Sessions list shows, as the `sessions.filters` setting remembers it.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Filters {
    pub scope: Scope,
    pub range: Range,
    pub custom_from: String,
    pub custom_to: String,
    pub profile_id: String,
    pub repository: String,
}

pub const FILTERS_SETTING: &str = "sessions.filters";

impl Filters {
    /// The filters the setting remembers, or the defaults when it holds anything else.
    pub fn remembered(setting: Option<&str>) -> Filters {
        #[derive(Deserialize)]
        #[serde(rename_all = "camelCase", deny_unknown_fields)]
        struct Stored {
            scope: Scope,
            range: Range,
            #[serde(default)]
            custom_from: String,
            #[serde(default)]
            custom_to: String,
            #[serde(default)]
            profile_id: String,
            #[serde(default)]
            repository: String,
        }
        let Some(stored) = setting
            .filter(|s| !s.trim().is_empty())
            .and_then(|s| serde_json::from_str::<Stored>(s).ok())
        else {
            return Filters::default();
        };
        let day = |d: &str| d.is_empty() || day_of_date(d).is_some() && d.len() == 10;
        if !day(&stored.custom_from) || !day(&stored.custom_to) {
            return Filters::default();
        }
        Filters {
            scope: stored.scope,
            range: stored.range,
            custom_from: stored.custom_from,
            custom_to: stored.custom_to,
            profile_id: stored.profile_id,
            repository: stored.repository,
        }
    }

    pub fn setting(&self) -> String {
        serde_json::to_string(self).unwrap_or_default()
    }

    /// The window the range covers at `now_ms`, half-open, as RFC 3339 instants, or why the
    /// custom range names none.
    pub fn window(&self, now_ms: i64) -> Result<Window, String> {
        let midnight = |days_back: i64| iso(local_instant(local_day(now_ms) - days_back, 0));
        Ok(match self.range {
            Range::Any => Window::default(),
            Range::Today => Window::since(midnight(0)),
            Range::Yesterday => Window {
                since: Some(midnight(1)),
                until: Some(midnight(0)),
            },
            Range::Week => Window::since(midnight(6)),
            Range::Month => Window::since(midnight(29)),
            Range::Custom => {
                let named = |d: &str, what: &str| {
                    day_of_date(d).ok_or_else(|| {
                        let d = if d.is_empty() { what } else { d };
                        format!("{d} is not a date like 2026-09-05")
                    })
                };
                let start = named(&self.custom_from, "The start date")?;
                let end = named(&self.custom_to, "The end date")?;
                if end < start {
                    return Err("The range ends before it starts; swap the two dates".into());
                }
                Window {
                    since: Some(iso(local_instant(start, 0))),
                    until: Some(iso(local_instant(end + 1, 0))),
                }
            }
        })
    }

    /// The `session_list` request for one page of these filters at `now_ms`.
    pub fn request(&self, now_ms: i64) -> Result<SessionListMessage, String> {
        let window = self.window(now_ms)?;
        let text = |s: &str| (!s.is_empty()).then(|| s.to_owned());
        Ok(SessionListMessage {
            closed: (self.scope == Scope::Closed).then_some(true),
            all: (self.scope == Scope::All).then_some(true),
            since: window.since,
            until: window.until,
            profile_id: text(&self.profile_id),
            repository: text(&self.repository),
            ..SessionListMessage::new()
        })
    }

    /// Whether a row that closed at `now_ms` belongs on the page these filters show.
    pub fn holds_close(&self, entry: &SessionLedgerEntry, now_ms: i64) -> bool {
        if self.scope == Scope::Live
            || !self.profile_id.is_empty() && entry.profile_id != self.profile_id
            || !self.repository.is_empty()
                && entry.repository.as_deref().unwrap_or_default() != self.repository
        {
            return false;
        }
        let Ok(window) = self.window(now_ms) else {
            return false;
        };
        let at = instant(entry);
        window.since.as_deref().is_none_or(|since| at >= since)
            && window.until.as_deref().is_none_or(|until| at < until)
    }

    fn same_query(&self, other: &Filters) -> bool {
        Filters {
            scope: other.scope,
            ..self.clone()
        } == *other
    }
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Window {
    pub since: Option<String>,
    pub until: Option<String>,
}

impl Window {
    fn since(at: String) -> Window {
        Window {
            since: Some(at),
            until: None,
        }
    }
}

/// A profile the query can name: a live one, or one only the ledger's history remembers.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct ProfileChoice {
    pub profile_id: String,
    pub name: String,
    pub deleted: bool,
}

/// The live profiles, then those only the facets name, then `chosen` when neither does.
pub fn profile_choices(
    live: &[(String, String)],
    facets: Option<&SessionLedgerFacets>,
    chosen: Option<&ProfileChoice>,
) -> Vec<ProfileChoice> {
    let mut choices: Vec<ProfileChoice> = live
        .iter()
        .map(|(profile_id, name)| ProfileChoice {
            profile_id: profile_id.clone(),
            name: name.clone(),
            deleted: false,
        })
        .collect();
    for facet in facets.map(|f| f.profiles.as_slice()).unwrap_or_default() {
        if !live.iter().any(|(id, _)| *id == facet.profile_id) {
            choices.push(ProfileChoice {
                profile_id: facet.profile_id.clone(),
                name: facet.name.clone(),
                deleted: facet.deleted.unwrap_or_default(),
            });
        }
    }
    if let Some(chosen) = chosen
        && !choices.iter().any(|c| c.profile_id == chosen.profile_id)
    {
        choices.push(chosen.clone());
    }
    choices
}

/// The typed query: `repo:attn profile:name 7d from:… to:… dir:… words`. The daemon reads the
/// filters; `dir:` and the words narrow the page it sent.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Parsed {
    pub filters: Filters,
    pub dir: String,
    pub words: Vec<String>,
    /// Tokens that name a repository or profile nothing matches, so the user sees why.
    pub unresolved: Vec<String>,
}

pub fn parse(
    text: &str,
    facets: Option<&SessionLedgerFacets>,
    profiles: &[ProfileChoice],
    preferred_repository: &str,
) -> Parsed {
    let mut parsed = Parsed::default();
    let filters = &mut parsed.filters;
    for token in text.split_whitespace() {
        let lower = token.to_lowercase();
        let (key, value) = match token.find(':') {
            Some(colon) if colon > 0 => (lower[..colon].to_owned(), &token[colon + 1..]),
            _ => (String::new(), ""),
        };
        if let Some(range) = Range::of_word(&lower) {
            filters.range = range;
        } else if key == "from" || key == "to" {
            filters.range = Range::Custom;
            let end = if key == "from" {
                &mut filters.custom_from
            } else {
                &mut filters.custom_to
            };
            *end = value.to_owned();
        } else if key == "repo-path" {
            filters.repository = percent_encoding::percent_decode_str(&value.replace('+', " "))
                .decode_utf8_lossy()
                .into_owned();
        } else if key == "repo" {
            let repositories = facets
                .map(|f| f.repositories.as_slice())
                .unwrap_or_default();
            let wanted = value.to_lowercase();
            let named: Vec<_> = repositories
                .iter()
                .filter(|f| base_name(&f.value).to_lowercase() == wanted)
                .collect();
            let found = repositories
                .iter()
                .find(|f| f.value == value)
                .map(|f| f.value.clone())
                .or_else(|| {
                    (base_name(preferred_repository).to_lowercase() == wanted)
                        .then(|| preferred_repository.to_owned())
                })
                .or_else(|| (named.len() == 1).then(|| named[0].value.clone()));
            match found {
                Some(repository) => filters.repository = repository,
                None => parsed.unresolved.push(token.to_owned()),
            }
        } else if key == "profile" {
            match resolve_profile(profiles, value) {
                Some(id) => filters.profile_id = id,
                None => parsed.unresolved.push(token.to_owned()),
            }
        } else if key == "dir" {
            parsed.dir = value.to_owned();
        } else {
            parsed.words.push(lower);
        }
    }
    if filters.range == Range::Custom {
        if filters.custom_to.is_empty() {
            filters.custom_to = filters.custom_from.clone();
        }
        if filters.custom_from.is_empty() {
            filters.custom_from = filters.custom_to.clone();
        }
    }
    parsed
}

/// The query that reads back as `filters`.
pub fn format(filters: &Filters, live: &[(String, String)]) -> String {
    let mut tokens = Vec::new();
    if !filters.repository.is_empty() {
        tokens.push(format!("repo:{}", base_name(&filters.repository)));
    }
    if !filters.profile_id.is_empty() {
        tokens.push(format!(
            "profile:{}",
            profile_token(&filters.profile_id, live)
        ));
    }
    match filters.range {
        Range::Custom => {
            if !filters.custom_from.is_empty() {
                tokens.push(format!("from:{}", filters.custom_from));
            }
            if !filters.custom_to.is_empty() {
                tokens.push(format!("to:{}", filters.custom_to));
            }
        }
        range => tokens.extend(range.token().map(str::to_owned)),
    }
    tokens.join(" ")
}

/// The filters `parsed` asks for, keeping what `current` has for a repository or profile token
/// the facets have not arrived to name yet.
pub fn applied(current: &Filters, parsed: &Parsed, facets_known: bool) -> Filters {
    let pending = |prefix: &str| {
        !facets_known
            && parsed
                .unresolved
                .iter()
                .any(|t| t.to_lowercase().starts_with(prefix))
    };
    let next = Filters {
        scope: current.scope,
        repository: if pending("repo:") {
            current.repository.clone()
        } else {
            parsed.filters.repository.clone()
        },
        profile_id: if pending("profile:") {
            current.profile_id.clone()
        } else {
            parsed.filters.profile_id.clone()
        },
        ..parsed.filters.clone()
    };
    if next.same_query(current) {
        current.clone()
    } else {
        next
    }
}

/// `text` with each `profile:` token that named a profile under `before` naming it as `after`
/// does.
pub fn rename_profile_tokens(
    text: &str,
    before: &[(String, String)],
    after: &[(String, String)],
) -> String {
    let lived = profile_choices(before, None, None);
    let mut out = String::new();
    let mut rest = text;
    while !rest.is_empty() {
        let split = rest.find(char::is_whitespace).unwrap_or(rest.len());
        let (word, tail) = rest.split_at(split);
        let space = tail.len() - tail.trim_start().len();
        let renamed = word
            .get(..8)
            .filter(|p| p.eq_ignore_ascii_case("profile:"))
            .and_then(|_| resolve_profile(&lived, &word[8..]))
            .filter(|id| after.iter().any(|(a, _)| a == id))
            .map(|id| format!("profile:{}", profile_token(&id, after)));
        out.push_str(renamed.as_deref().unwrap_or(word));
        out.push_str(&tail[..space]);
        rest = &tail[space..];
    }
    out
}

fn resolve_profile(profiles: &[ProfileChoice], value: &str) -> Option<String> {
    if profiles.iter().any(|c| c.profile_id == value) {
        return Some(value.to_owned());
    }
    let wanted = value.to_lowercase();
    let namesakes: Vec<_> = profiles
        .iter()
        .filter(|c| name_token(&c.name) == wanted)
        .collect();
    let only = |of: Vec<&&ProfileChoice>| (of.len() == 1).then(|| of[0].profile_id.clone());
    only(namesakes.iter().collect())
        .or_else(|| only(namesakes.iter().filter(|c| !c.deleted).collect()))
}

fn profile_token(profile_id: &str, live: &[(String, String)]) -> String {
    let Some((_, name)) = live.iter().find(|(id, _)| id == profile_id) else {
        return profile_id.to_owned();
    };
    let token = name_token(name);
    let shared = live
        .iter()
        .any(|(id, other)| id != profile_id && name_token(other) == token);
    if shared { profile_id.to_owned() } else { token }
}

fn name_token(label: &str) -> String {
    label
        .split_whitespace()
        .collect::<Vec<_>>()
        .join("-")
        .to_lowercase()
}

pub fn base_name(path: &str) -> &str {
    let trimmed = path.trim_end_matches('/');
    trimmed.rsplit('/').next().unwrap_or(trimmed)
}

/// Whether every word is somewhere in `haystack`, case-blind.
pub fn matches_words(haystack: &[&str], words: &[String]) -> bool {
    let joined = haystack.join(" ").to_lowercase();
    words.iter().all(|w| joined.contains(w.as_str()))
}

/// Whether `directory` is `dir` or inside it, `dir` typed as the row shows it (`~/…`) or
/// pasted absolute.
pub fn matches_dir(directory: &str, dir: &str) -> bool {
    let wanted = dir.trim_end_matches('/');
    wanted.is_empty()
        || [directory.to_owned(), tilde_path(directory)]
            .iter()
            .any(|form| {
                form == wanted
                    || form
                        .strip_prefix(wanted)
                        .is_some_and(|rest| rest.starts_with('/'))
            })
}

pub fn remove_token(text: &str, token: &str) -> String {
    text.split_whitespace()
        .filter(|part| *part != token)
        .collect::<Vec<_>>()
        .join(" ")
}

/// Whether a query token shows as a chip: a filter or a range word.
pub fn is_chip(token: &str) -> bool {
    token.contains(':') || Range::of_word(&token.to_lowercase()).is_some()
}

pub fn tilde_path(path: &str) -> String {
    for home in ["/Users/", "/home/"] {
        if let Some(rest) = path.strip_prefix(home) {
            let user_end = rest.find('/').unwrap_or(rest.len());
            if user_end > 0 {
                return format!("~{}", &rest[user_end..]);
            }
        }
    }
    path.to_owned()
}

pub fn short_path(path: &str) -> String {
    let tilde = tilde_path(path);
    if tilde.chars().count() <= 36 {
        return tilde;
    }
    let parts: Vec<_> = tilde.split('/').filter(|p| !p.is_empty()).collect();
    if parts.len() > 2 {
        format!("…/{}", parts[parts.len() - 2..].join("/"))
    } else {
        tilde
    }
}

const MONTHS: [&str; 12] = [
    "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
];
const WEEKDAYS: [&str; 7] = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

/// How long ago `at_ms` was, in the fewest characters that still say when.
pub fn relative_stamp(at_ms: i64, now_ms: i64) -> String {
    let seconds = (now_ms - at_ms + 500).div_euclid(1000);
    let today = local_day(now_ms);
    let day = local_day(at_ms);
    if seconds < 45 {
        "now".into()
    } else if seconds < 3600 {
        format!("{}m", ((seconds as f64 / 60.0).round() as i64).max(1))
    } else if seconds < DAY_S && day == today {
        format!("{}h", (seconds as f64 / 3600.0).round() as i64)
    } else if day == today - 1 {
        "yesterday".into()
    } else if seconds < 7 * DAY_S {
        format!("{}d", (seconds as f64 / DAY_S as f64).round() as i64)
    } else {
        month_day(at_ms)
    }
}

/// How long until `at_ms`, in the fewest characters that still say when.
pub fn until_stamp(at_ms: i64, now_ms: i64) -> String {
    let seconds = (at_ms - now_ms + 500).div_euclid(1000);
    if seconds < 45 {
        "now".into()
    } else if seconds < 3600 {
        format!("{}m", ((seconds as f64 / 60.0).round() as i64).max(1))
    } else if seconds < DAY_S {
        format!("{}h", (seconds as f64 / 3600.0).round() as i64)
    } else if seconds < 30 * DAY_S {
        format!("{}d", (seconds as f64 / DAY_S as f64).round() as i64)
    } else {
        month_day(at_ms)
    }
}

fn month_day(at_ms: i64) -> String {
    let t = LocalTime::at(at_ms);
    format!(
        "{} {}",
        MONTHS[(t.month as usize).saturating_sub(1) % 12],
        t.day
    )
}

/// The instant whole: weekday, date and clock.
pub fn full_stamp(at_ms: i64) -> String {
    let t = LocalTime::at(at_ms);
    format!(
        "{}, {}, {:02}:{:02}",
        WEEKDAYS[crate::calendar::weekday(local_day(at_ms)) as usize],
        month_day(at_ms),
        t.hour,
        t.minute
    )
}

/// `text` with session ids replaced by what `label` names them, a conversation id by "its
/// conversation", and an id nothing names by "a session", so people read titles, never ids.
pub fn name_ids(text: &str, label: impl Fn(&str) -> Option<String>) -> String {
    let mut out = String::new();
    let mut rest = text;
    while let Some(at) = next_uuid(rest) {
        let before = &rest[..at];
        let id = &rest[at..at + 36];
        match before.len().checked_sub(13).map(|c| &before[c..]) {
            Some(lead) if lead.eq_ignore_ascii_case("conversation ") => {
                out.push_str(&before[..before.len() - 13]);
                out.push_str("its conversation");
            }
            _ => {
                out.push_str(before);
                out.push_str(&label(id).unwrap_or_else(|| "a session".into()));
            }
        }
        rest = &rest[at + 36..];
    }
    out.push_str(rest);
    out
}

fn next_uuid(text: &str) -> Option<usize> {
    let b = text.as_bytes();
    (0..b.len().saturating_sub(35)).find(|&i| {
        (0..36).all(|j| {
            let c = b[i + j];
            if matches!(j, 8 | 13 | 18 | 23) {
                c == b'-'
            } else {
                c.is_ascii_hexdigit()
            }
        })
    })
}

/// `text` with each absolute path shortened as a row shows paths.
pub fn compact_paths(text: &str) -> String {
    let mut out = String::new();
    let mut rest = text;
    while let Some(at) = rest.find('/') {
        let end = rest[at..]
            .find(|c: char| c.is_whitespace() || matches!(c, ',' | ';' | ':'))
            .map_or(rest.len(), |e| at + e);
        let path = &rest[at..end];
        out.push_str(&rest[..at]);
        if path.matches('/').count() >= 2 && !path.ends_with('/') {
            let parts: Vec<_> = path.split('/').filter(|p| !p.is_empty()).collect();
            if parts.len() > 2 {
                out.push_str(&format!("…/{}", parts[parts.len() - 2..].join("/")));
            } else {
                out.push_str(path);
            }
        } else {
            out.push_str(path);
        }
        rest = &rest[end..];
    }
    out.push_str(rest);
    out
}

/// When the row happened: its close, or its last sighting while live.
pub fn instant(entry: &SessionLedgerEntry) -> &str {
    entry
        .closed_at
        .as_deref()
        .filter(|c| !c.is_empty())
        .unwrap_or(&entry.last_seen)
}

pub fn is_closed(entry: &SessionLedgerEntry) -> bool {
    entry.closed_at.as_deref().is_some_and(|c| !c.is_empty())
}

/// Who closed the row: "you" for the user, the closing session by `label`, or its id.
pub fn closed_by(entry: &SessionLedgerEntry, label: impl Fn(&str) -> Option<String>) -> String {
    match entry.closed_by.as_deref().unwrap_or_default() {
        "user" => "you".into(),
        by => label(by).unwrap_or_else(|| by.to_owned()),
    }
}

pub fn action_label(action: &str) -> &str {
    match action {
        "reopen" => "Reopen",
        "recreate_worktree_and_reopen" => "Recreate the worktree",
        "fetch_recreate_and_reopen" => "Fetch, recreate, reopen",
        other => other,
    }
}

/// What a refused reopen offers: its words and the actions it would take instead.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct Verdict {
    pub summary: String,
    pub reopenable: bool,
    pub actions: Vec<String>,
    pub reason: Option<String>,
    pub warning: Option<String>,
    pub directory_state: String,
    pub branch_state: Option<String>,
    pub profile_deleted: bool,
}

impl Verdict {
    pub fn of(reopen: &SessionReopen) -> Verdict {
        let some = |s: &Option<String>| s.clone().filter(|s| !s.is_empty());
        Verdict {
            summary: some(&reopen.reason)
                .or_else(|| some(&reopen.warning))
                .unwrap_or_else(|| "it can be reopened where it ran".into()),
            reopenable: reopen.reopenable,
            actions: reopen.actions.clone(),
            reason: some(&reopen.reason),
            warning: some(&reopen.warning),
            directory_state: reopen.directory_state.clone(),
            branch_state: some(&reopen.branch_state),
            profile_deleted: reopen.profile_deleted,
        }
    }

    pub fn placement(&self) -> &'static str {
        if self.profile_deleted {
            "its profile was deleted; reopening asks which profile to land it in"
        } else {
            "lands on its last or current desktop"
        }
    }

    /// The note a refused `action` leaves on its row.
    pub fn refusal(&self, action: &str) -> String {
        let refused = format!("{} was refused", action_label(action));
        match self.actions.first() {
            Some(offer) => format!("{refused}; it offers {} instead", action_label(offer)),
            None => refused,
        }
    }
}

pub fn directory_state_label(state: &str) -> &str {
    match state {
        "present" => "directory is there",
        "missing" => "directory is gone",
        "unavailable" => "directory cannot be opened",
        "unknown" => "no directory saved",
        other => other,
    }
}

pub fn branch_state_label(state: &str) -> &str {
    match state {
        "local" => "branch is local",
        "remote_only" => "branch is only on the remote",
        "gone" => "branch is gone everywhere",
        "merged" => "branch was merged and is gone",
        "unknown" => "branch is being checked",
        other => other,
    }
}

#[cfg(test)]
mod tests;
