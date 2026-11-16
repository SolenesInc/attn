//! A member's charter as the panel edits it: read when its tab opens, saved after a pause in
//! typing and at once when the user leaves it, against the version it was read at.

use std::collections::BTreeMap;

use attn_protocol::{
    CrewCharterDocument, CrewCharterGetMessage, CrewCharterGetResultMessage, CrewCharterSetMessage,
    CrewCharterSetResultMessage,
};
use rune::{TextEdit, Tx};

use super::autosave::{self, Edits, Phase, Sent, Spec};
use super::member_ref;
use crate::State;

/// How long typing rests before the charter saves, as in the Tauri app.
const PAUSE_SECONDS: f64 = 0.7;

pub(crate) struct Charter;

impl Spec for Charter {
    type Draft = String;
    type Ack = CrewCharterDocument;

    fn equal(draft: &String, ack: &CrewCharterDocument) -> bool {
        *draft == ack.content
    }

    fn settled(ack: &CrewCharterDocument) -> String {
        ack.content.clone()
    }
}

/// The charters the panel has read, the field each is typed into, and when each is due.
#[derive(Default)]
pub(crate) struct Charters {
    pub(crate) edits: Edits<Charter>,
    pub(crate) fields: BTreeMap<String, TextEdit>,
    due: BTreeMap<String, f64>,
}

impl Charters {
    pub(crate) fn field_mut(&mut self, member: &str) -> &mut TextEdit {
        self.fields.entry(member.to_owned()).or_default()
    }

    /// Shows the edit's draft in its field, unless the field already holds it.
    fn show_draft(&mut self, member: &str) {
        let Some(edit) = self.edits.get(member) else {
            return;
        };
        let field = self.fields.entry(member.to_owned()).or_default();
        if field.text != edit.draft {
            *field = TextEdit::new(edit.draft.clone());
        }
    }
}

/// Whether leaving the tab must wait for the charter to land first.
pub(crate) fn unsettled(state: &State, member: &str) -> bool {
    state
        .window
        .crew
        .charters
        .edits
        .get(member)
        .is_some_and(|e| {
            e.acknowledged.is_some()
                && !matches!(e.phase, Phase::Saved | Phase::Error | Phase::Conflict)
        })
}

/// What the status beside the editor says.
pub(crate) fn status(phase: Phase) -> &'static str {
    match phase {
        Phase::Dirty => "Waiting to save",
        Phase::Saving => "Saving…",
        Phase::Conflict => "Changed elsewhere",
        Phase::Error => "Not saved",
        Phase::Loading | Phase::Saved => "Saved",
    }
}

/// Reads `member`'s charter from the daemon.
pub(crate) fn load(state: &mut State, member: &str, tx: &mut Tx) {
    let charters = &mut state.window.crew.charters;
    charters.edits.loading(member, String::new());
    let fence = charters.edits.fence(member);
    let member = member.to_owned();
    let command_member = member_ref(&member);
    state.ask(
        move |request_id| CrewCharterGetMessage {
            member: command_member,
            request_id: Some(request_id),
            ..CrewCharterGetMessage::new()
        },
        move |state, answer, _| {
            let charters = &mut state.window.crew.charters;
            if !charters.edits.current(&member, fence) {
                return;
            }
            let read = match autosave::lost(&answer) {
                Some(why) => Err(why),
                None => match answer.parse::<CrewCharterGetResultMessage>() {
                    Some(r) if r.success => r
                        .charter
                        .ok_or_else(|| "The daemon returned no charter".to_owned()),
                    Some(r) => Err(r
                        .error
                        .filter(|e| !e.is_empty())
                        .unwrap_or_else(|| "The daemon returned no charter".into())),
                    None => Err("The daemon returned no charter".into()),
                },
            };
            match read {
                Ok(charter) => {
                    let edited = charters.edits.get(&member).is_some_and(|e| {
                        e.phase != Phase::Saved
                            && e.acknowledged
                                .as_ref()
                                .is_some_and(|a| a.token != charter.token)
                    });
                    if edited {
                        charters.edits.conflict(&member, charter);
                    } else {
                        charters.edits.adopt(&member, charter);
                        charters.show_draft(&member);
                    }
                }
                Err(why) => charters.edits.fail(&member, why),
            }
        },
        tx,
    );
}

/// The user typed into `member`'s charter: it saves once typing rests.
pub(crate) fn edited(state: &mut State, member: &str, tx: &mut Tx) {
    let charters = &mut state.window.crew.charters;
    let text = charters.field_mut(member).text.clone();
    charters.due.remove(member);
    if charters.edits.get(member).is_some_and(|e| e.draft == text) {
        return;
    }
    if charters.edits.update(member, text) {
        let at = tx.clock() + PAUSE_SECONDS;
        charters.due.insert(member.to_owned(), at);
        tx.wake_at(at);
    }
}

/// Saves `member`'s charter now.
pub(crate) fn flush(state: &mut State, member: &str, tx: &mut Tx) {
    state.window.crew.charters.due.remove(member);
    pump(state, member, tx);
}

/// Saves each charter whose pause has run out; whether one did.
pub(crate) fn poll(state: &mut State, now: f64, tx: &mut Tx) -> bool {
    let due: Vec<String> = state
        .window
        .crew
        .charters
        .due
        .iter()
        .filter(|(_, at)| now >= **at)
        .map(|(m, _)| m.clone())
        .collect();
    for member in &due {
        flush(state, member, tx);
    }
    !due.is_empty()
}

fn pump(state: &mut State, member: &str, tx: &mut Tx) {
    let Some(write) = state.window.crew.charters.edits.start(member) else {
        super::settled(state, tx);
        return;
    };
    let generation = write.generation;
    let member = member.to_owned();
    let command_member = member_ref(&member);
    state.ask(
        move |request_id| CrewCharterSetMessage {
            member: command_member,
            content: write.draft,
            expected_token: write.acknowledged.token,
            request_id: Some(request_id),
            ..CrewCharterSetMessage::new()
        },
        move |state, answer, tx| {
            let sent = match autosave::lost(&answer) {
                Some(why) => Sent::Lost(why),
                None => match answer.parse::<CrewCharterSetResultMessage>() {
                    Some(r) if r.success && r.conflict => match r.charter {
                        Some(c) => Sent::Conflict(c),
                        None => Sent::Refused("The daemon returned no saved charter".into(), None),
                    },
                    Some(r) if r.success => match r.charter {
                        Some(c) => Sent::Ack(c),
                        None => Sent::Refused("The daemon returned no saved charter".into(), None),
                    },
                    Some(r) => Sent::Refused(
                        r.error
                            .filter(|e| !e.is_empty())
                            .unwrap_or_else(|| "The charter was not saved.".into()),
                        None,
                    ),
                    None => Sent::Refused("The charter was not saved.".into(), None),
                },
            };
            let charters = &mut state.window.crew.charters;
            let again = charters.edits.finish(&member, generation, sent);
            if again {
                pump(state, &member, tx);
            } else {
                super::settled(state, tx);
            }
        },
        tx,
    );
}

/// Takes the file's version over the user's edit.
pub(crate) fn use_file(state: &mut State, member: &str) {
    let charters = &mut state.window.crew.charters;
    charters.edits.use_external(member);
    charters.show_draft(member);
}

/// Keeps the user's edit over the file's version, and saves it.
pub(crate) fn keep_mine(state: &mut State, member: &str, tx: &mut Tx) {
    if state.window.crew.charters.edits.keep_mine(member) {
        pump(state, member, tx);
    }
}

/// Writes again each charter the lost connection took; the shown one is read again as the
/// panel enters its tab once more.
pub(crate) fn reconnected(state: &mut State, tx: &mut Tx) {
    for member in state.window.crew.charters.edits.lost_to_the_connection() {
        pump(state, &member, tx);
    }
}
