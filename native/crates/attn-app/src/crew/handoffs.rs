//! A member's letters, newest first, read when the Handoffs tab opens, one letter at a time.

use std::collections::BTreeMap;

use attn_protocol::{
    CrewHandoffDocument, CrewHandoffGetMessage, CrewHandoffGetResultMessage, CrewHandoffSummary,
    CrewHandoffsGetMessage, CrewHandoffsGetResultMessage,
};
use rune::Tx;

use super::member_ref;
use crate::State;
use crate::requests::Answer;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Reading {
    Loading,
    Ready,
    Failed,
}

pub(crate) struct Letter {
    pub(crate) reading: Reading,
    pub(crate) filename: String,
    pub(crate) document: Option<CrewHandoffDocument>,
    pub(crate) error: Option<String>,
    request: u64,
}

pub(crate) struct History {
    pub(crate) reading: Reading,
    pub(crate) handoffs: Vec<CrewHandoffSummary>,
    pub(crate) selected: Option<String>,
    pub(crate) letter: Option<Letter>,
    pub(crate) error: Option<String>,
    request: u64,
    /// The connection it was read on.
    connection: u64,
}

impl History {
    /// The letter the reader shows: the selected one, or the newest.
    pub(crate) fn shown(&self) -> Option<&CrewHandoffSummary> {
        self.handoffs
            .iter()
            .find(|h| Some(&h.filename) == self.selected.as_ref())
            .or_else(|| self.handoffs.first())
    }
}

#[derive(Default)]
pub(crate) struct Histories {
    pub(crate) by_member: BTreeMap<String, History>,
    requests: u64,
    /// Rises with each reconnect, so a history read on an earlier connection reads again.
    pub(crate) connection: u64,
}

fn failed(answer: &Answer, fallback: &str, error: Option<String>) -> String {
    match answer {
        Answer::NotSent => "not connected to the daemon".into(),
        Answer::Dropped => "the daemon did not answer".into(),
        Answer::Reply(_) => error
            .filter(|e| !e.is_empty())
            .unwrap_or_else(|| fallback.to_owned()),
    }
}

/// Reads `member`'s letters, unless they were read on this connection; `force` reads again.
pub(crate) fn load(state: &mut State, member: &str, force: bool, tx: &mut Tx) {
    let histories = &mut state.window.crew.handoffs;
    let connection = histories.connection;
    let current = histories
        .by_member
        .get(member)
        .is_some_and(|e| e.reading != Reading::Failed && e.connection == connection);
    if current && !force {
        return;
    }
    let existing = histories.by_member.remove(member);
    histories.requests += 1;
    let request = histories.requests;
    let (handoffs, selected, letter) = match existing {
        Some(e) => (e.handoffs, e.selected, if force { None } else { e.letter }),
        None => (Vec::new(), None, None),
    };
    histories.by_member.insert(
        member.to_owned(),
        History {
            reading: Reading::Loading,
            handoffs,
            selected,
            letter,
            error: None,
            request,
            connection,
        },
    );
    let member = member.to_owned();
    let command_member = member_ref(&member);
    state.ask(
        move |request_id| CrewHandoffsGetMessage {
            member: command_member,
            request_id: Some(request_id),
            ..CrewHandoffsGetMessage::new()
        },
        move |state, answer, tx| {
            let Some(history) = state.window.crew.handoffs.by_member.get_mut(&member) else {
                return;
            };
            if history.request != request {
                return;
            }
            let result = answer.parse::<CrewHandoffsGetResultMessage>();
            match result {
                Some(r) if r.success => {
                    let selected = history
                        .selected
                        .clone()
                        .filter(|s| r.handoffs.iter().any(|h| &h.filename == s))
                        .or_else(|| r.handoffs.first().map(|h| h.filename.clone()));
                    let letter = history
                        .letter
                        .take()
                        .filter(|l| Some(&l.filename) == selected.as_ref());
                    let wants_letter = letter.is_none();
                    history.reading = Reading::Ready;
                    history.handoffs = r.handoffs;
                    history.selected = selected.clone();
                    history.letter = letter;
                    if wants_letter && let Some(selected) = selected {
                        load_letter(state, &member, &selected, tx);
                    }
                }
                other => {
                    history.reading = Reading::Failed;
                    history.error = Some(failed(
                        &answer,
                        "The daemon returned no handoff history",
                        other.and_then(|r| r.error),
                    ));
                }
            }
        },
        tx,
    );
}

/// Reads one letter of `member`'s.
pub(crate) fn load_letter(state: &mut State, member: &str, filename: &str, tx: &mut Tx) {
    let histories = &mut state.window.crew.handoffs;
    histories.requests += 1;
    let request = histories.requests;
    let Some(history) = histories.by_member.get_mut(member) else {
        return;
    };
    history.letter = Some(Letter {
        reading: Reading::Loading,
        filename: filename.to_owned(),
        document: None,
        error: None,
        request,
    });
    let member = member.to_owned();
    let (command_member, file) = (member_ref(&member), filename.to_owned());
    state.ask(
        move |request_id| CrewHandoffGetMessage {
            member: command_member,
            filename: file,
            request_id: Some(request_id),
            ..CrewHandoffGetMessage::new()
        },
        move |state, answer, _| {
            let Some(letter) = state
                .window
                .crew
                .handoffs
                .by_member
                .get_mut(&member)
                .and_then(|h| h.letter.as_mut())
                .filter(|l| l.request == request)
            else {
                return;
            };
            match answer.parse::<CrewHandoffGetResultMessage>() {
                Some(r) if r.success && r.handoff.is_some() => {
                    letter.reading = Reading::Ready;
                    letter.document = r.handoff;
                }
                other => {
                    letter.reading = Reading::Failed;
                    letter.error = Some(failed(
                        &answer,
                        "The daemon returned no handoff",
                        other.and_then(|r| r.error),
                    ));
                }
            }
        },
        tx,
    );
}

/// Shows another of `member`'s letters.
pub(crate) fn select(state: &mut State, member: &str, filename: &str, tx: &mut Tx) {
    let Some(history) = state.window.crew.handoffs.by_member.get_mut(member) else {
        return;
    };
    if history.selected.as_deref() == Some(filename) {
        return;
    }
    history.selected = Some(filename.to_owned());
    if history
        .letter
        .as_ref()
        .is_none_or(|l| l.filename != filename)
    {
        load_letter(state, member, filename, tx);
    }
}
