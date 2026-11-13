//! The Conversations list: attn's own copies of Claude conversations, with the way to keep one
//! forever, let it go, or forget it now.

mod view;

use std::collections::HashMap;

use attn_protocol::{
    KeptConversationCrewLetter, KeptConversationForgetMessage, KeptConversationKeepMessage,
    KeptConversationListMessage, KeptConversationListResult, KeptConversationListResultEvent,
    KeptConversationRow,
};
use rune::{TextEdit, Tx};

use super::Note;
use crate::State;
use crate::requests::Answer;

pub(crate) use view::{back_out, inspector, list, panel_key, status, toolbar};

#[derive(Default)]
pub struct Kept {
    pub include_deleted: bool,
    pub query: TextEdit,
    result: Option<KeptConversationListResult>,
    error: Option<String>,
    /// Rises with every read, so an answer to an earlier one is dropped.
    pub(super) epoch: u64,
    pub selected: Option<String>,
    pub menu: Option<String>,
    /// The row whose Forget waits for the person to confirm it.
    pub confirming: Option<String>,
    notes: HashMap<String, Note>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Verb {
    Keep,
    Unkeep,
    Forget,
    Cancel,
    ForgetNow,
}

impl Verb {
    pub fn label(self) -> &'static str {
        match self {
            Verb::Keep => "Keep forever",
            Verb::Unkeep => "Unkeep",
            Verb::Forget => "Forget…",
            Verb::Cancel => "Cancel",
            Verb::ForgetNow => "Forget now",
        }
    }
}

impl Kept {
    /// Leaving the list drops a Forget that waited for its confirmation.
    pub(super) fn leave(&mut self) {
        self.confirming = None;
        self.menu = None;
    }

    pub(crate) fn note(&self, key: &str) -> Option<&Note> {
        self.notes.get(key)
    }

    pub(crate) fn result(&self) -> Option<&KeptConversationListResult> {
        self.result.as_ref()
    }

    pub(crate) fn error(&self) -> Option<&str> {
        self.error.as_deref()
    }
}

pub(crate) fn key(row: &KeptConversationRow) -> String {
    format!("{}:{}", row.agent, row.resume_id)
}

pub(crate) fn pinned(row: &KeptConversationRow) -> bool {
    row.pinned_at.is_some() || row.kept.as_ref().is_some_and(|k| k.pinned_at.is_some())
}

pub(crate) fn deleted(row: &KeptConversationRow) -> bool {
    row.kept.as_ref().is_some_and(|k| k.deleted_at.is_some())
}

pub(crate) fn verbs(row: &KeptConversationRow, confirming: bool) -> Vec<Verb> {
    if confirming {
        return vec![Verb::Cancel, Verb::ForgetNow];
    }
    let mut verbs = vec![if pinned(row) {
        Verb::Unkeep
    } else {
        Verb::Keep
    }];
    if row.kept.is_some() && !deleted(row) && row.seeds.is_empty() {
        verbs.push(Verb::Forget);
    }
    verbs
}

pub(crate) fn amount(bytes: i64) -> String {
    let b = bytes as f64;
    if b >= 1e6 {
        format!("{:.1} MB", b / 1e6)
    } else if b >= 1e3 {
        format!("{:.1} KB", b / 1e3)
    } else {
        format!("{bytes} B")
    }
}

pub(crate) fn date(iso: &str) -> &str {
    iso.get(..10).unwrap_or(iso)
}

pub(crate) fn letter_label(letter: &KeptConversationCrewLetter) -> String {
    const MONTHS: [&str; 12] = [
        "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
    ];
    let day = date(&letter.occurred_at);
    let when = match (day.get(0..4), day.get(5..7), day.get(8..10)) {
        (Some(y), Some(m), Some(d)) => {
            let month = m
                .parse::<usize>()
                .ok()
                .and_then(|m| MONTHS.get(m.wrapping_sub(1)));
            match (month, d.parse::<u32>()) {
                (Some(month), Ok(d)) => format!("{d} {month} {y}"),
                _ => day.to_owned(),
            }
        }
        _ => day.to_owned(),
    };
    format!(
        "{} · {when}",
        crate::asks::crew_display_name(&letter.member)
    )
}

/// How long attn keeps the copy, and what holds it.
pub(crate) fn retention(row: &KeptConversationRow) -> String {
    let kept = row.kept.as_ref();
    if let Some(at) = kept.and_then(|k| k.deleted_at.as_deref()) {
        let who = if kept.and_then(|k| k.deleted_by.as_deref()) == Some("user") {
            "You deleted attn’s copy"
        } else {
            "attn deleted its copy"
        };
        return format!("{who} on {}", date(at));
    }
    if let Some(pin) = row
        .pinned_at
        .as_deref()
        .or_else(|| kept.and_then(|k| k.pinned_at.as_deref()))
    {
        return format!("Forever · pinned {}", date(pin));
    }
    if !row.seeds.is_empty() {
        let names: Vec<&str> = row
            .seeds
            .iter()
            .map(|s| {
                if s.slug.is_empty() {
                    s.title.as_str()
                } else {
                    s.slug.as_str()
                }
            })
            .collect();
        return format!("Open: {}", names.join(", "));
    }
    if let Some(letters) = row.crew_letters.as_ref().filter(|l| !l.is_empty()) {
        let names: Vec<String> = letters.iter().map(letter_label).collect();
        return format!("Crew letter: {}", names.join(", "));
    }
    if let Some(after) = kept.and_then(|k| k.delete_after.as_deref()) {
        return format!("Deletes {}", date(after));
    }
    row.pending_reason
        .clone()
        .filter(|r| !r.is_empty())
        .unwrap_or_else(|| "Waiting for the next keep pass".into())
}

/// The rows the deleted toggle and the typed words leave.
pub(crate) fn rows(state: &State) -> Vec<&KeptConversationRow> {
    let kept = &state.window.ledger.conversations;
    let words: Vec<String> = kept
        .query
        .text
        .to_lowercase()
        .split_whitespace()
        .map(str::to_owned)
        .collect();
    kept.result
        .iter()
        .flat_map(|r| &r.rows)
        .filter(|row| kept.include_deleted || !deleted(row))
        .filter(|row| {
            let retention = retention(row);
            let mut haystack: Vec<&str> = vec![&row.title, &row.agent, &row.resume_id, &retention];
            haystack.extend(row.session_ids.iter().map(String::as_str));
            if row.kept.is_none() {
                haystack.push("pending");
            }
            haystack.extend(row.seeds.iter().map(|s| s.title.as_str()));
            super::query::matches_words(&haystack, &words)
        })
        .collect()
}

/// The row the inspector reads and the keys act on: the one chosen, or the first.
pub(crate) fn selected<'a>(
    state: &State,
    rows: &[&'a KeptConversationRow],
) -> Option<&'a KeptConversationRow> {
    let chosen = state.window.ledger.conversations.selected.as_deref();
    rows.iter()
        .find(|r| Some(key(r).as_str()) == chosen)
        .or_else(|| rows.first())
        .copied()
}

/// Reads the list for the deleted toggle shown, replacing it when the daemon answers.
pub(crate) fn read(state: &mut State, tx: &mut Tx) {
    let kept = &mut state.window.ledger.conversations;
    kept.epoch += 1;
    let epoch = kept.epoch;
    let include_deleted = kept.include_deleted;
    state.ask(
        |request_id| KeptConversationListMessage {
            request_id: Some(request_id),
            include_deleted: Some(include_deleted),
            ..KeptConversationListMessage::new()
        },
        move |state, answer, _| {
            let kept = &mut state.window.ledger.conversations;
            if kept.epoch != epoch || matches!(answer, Answer::NotSent | Answer::Dropped) {
                return;
            }
            let reply = answer.parse::<KeptConversationListResultEvent>();
            match reply
                .as_ref()
                .filter(|r| r.success)
                .and_then(|r| r.kept_conversation_list_result.clone())
            {
                Some(result) => {
                    kept.result = Some(result);
                    kept.error = None;
                }
                None => {
                    kept.error = Some(
                        reply
                            .and_then(|r| r.error)
                            .filter(|e| !e.is_empty())
                            .unwrap_or_else(|| "Reading kept conversations failed".into()),
                    )
                }
            }
            keep_selection(state);
        },
        tx,
    );
}

fn keep_selection(state: &mut State) {
    let keys: Vec<String> = rows(state).iter().map(|r| key(r)).collect();
    let kept = &mut state.window.ledger.conversations;
    for chosen in [&mut kept.selected, &mut kept.menu, &mut kept.confirming] {
        if chosen.as_ref().is_some_and(|k| !keys.contains(k)) {
            *chosen = None;
        }
    }
}

pub(crate) fn set_include_deleted(state: &mut State, include: bool, tx: &mut Tx) {
    let kept = &mut state.window.ledger.conversations;
    kept.include_deleted = include;
    kept.confirming = None;
    read(state, tx);
}

pub(crate) fn query_edited(state: &mut State) {
    state.window.ledger.conversations.confirming = None;
    keep_selection(state);
}

pub(crate) fn select(state: &mut State, key: &str) {
    let kept = &mut state.window.ledger.conversations;
    if kept.selected.as_deref() != Some(key) {
        kept.menu = None;
    }
    if kept.confirming.as_deref() != Some(key) {
        kept.confirming = None;
    }
    kept.selected = Some(key.to_owned());
}

/// Moves the selection `by` rows, stopping at either end.
pub(crate) fn step(state: &mut State, by: isize) {
    let keys: Vec<String> = rows(state).iter().map(|r| key(r)).collect();
    if keys.is_empty() {
        return;
    }
    let current = state
        .window
        .ledger
        .conversations
        .selected
        .as_ref()
        .and_then(|s| keys.iter().position(|k| k == s))
        .unwrap_or(0);
    let next = current.saturating_add_signed(by).min(keys.len() - 1);
    select(state, &keys[next]);
}

/// Runs the row's verb at `index`, its first for Enter.
pub(crate) fn run_nth(state: &mut State, key: &str, index: usize, tx: &mut Tx) {
    let confirming = state.window.ledger.conversations.confirming.as_deref() == Some(key);
    let verb = rows(state)
        .into_iter()
        .find(|r| self::key(r) == key)
        .and_then(|r| verbs(r, confirming).get(index).copied());
    if let Some(verb) = verb {
        run(state, key, verb, tx);
    }
}

pub(crate) fn run(state: &mut State, key: &str, verb: Verb, tx: &mut Tx) {
    let Some(row) = rows(state)
        .into_iter()
        .find(|r| self::key(r) == key)
        .cloned()
    else {
        return;
    };
    let kept = &mut state.window.ledger.conversations;
    if matches!(kept.notes.get(key), Some(Note::Busy(_))) {
        return;
    }
    kept.selected = Some(key.to_owned());
    kept.menu = None;
    match verb {
        Verb::Cancel => {
            kept.confirming = None;
            return;
        }
        Verb::Forget => {
            kept.confirming = Some(key.to_owned());
            return;
        }
        Verb::ForgetNow if kept.confirming.as_deref() != Some(key) => return,
        _ => {}
    }
    kept.confirming = None;
    let busy = if verb == Verb::ForgetNow {
        "forgetting…"
    } else {
        "changing pin…"
    };
    kept.notes.insert(key.to_owned(), Note::Busy(busy.into()));
    let session_id = format!("conversation:{}", row.resume_id);
    let key = key.to_owned();
    let done = move |state: &mut State, answer: Answer, _: &mut Tx| {
        let notes = &mut state.window.ledger.conversations.notes;
        match answer.outcome() {
            Ok(_) => notes.remove(&key),
            Err(why) => notes.insert(key, Note::Refused(why)),
        };
    };
    if verb == Verb::ForgetNow {
        let unlink = row.crew_letters.as_ref().is_some_and(|l| !l.is_empty());
        state.ask(
            |request_id| KeptConversationForgetMessage {
                request_id: Some(request_id),
                session_id,
                unlink_crew_letters: Some(unlink),
                ..KeptConversationForgetMessage::new()
            },
            done,
            tx,
        );
    } else {
        state.ask(
            |request_id| KeptConversationKeepMessage {
                request_id: Some(request_id),
                session_id,
                keep: verb == Verb::Keep,
                ..KeptConversationKeepMessage::new()
            },
            done,
            tx,
        );
    }
}
