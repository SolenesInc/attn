//! What the person has written for an open ask and not sent, as the daemon keeps it: saved whole
//! at the next generation once editing pauses, and read back from the ask for an ask this window
//! holds no draft for, as at start.

use std::collections::BTreeMap;

use attn_protocol::{AskAnswer, AskDraft, AskDraftSaveMessage, AskDraftSaveResultMessage};
use rune::{TextEdit, Tx};
use serde_json::Value;

use super::{Answer, Draft, attach};
use crate::State;

/// How long editing pauses before the draft is saved: an annotation note's pause, measured to
/// clear an ordinary gap between keystrokes, 100 to 250 ms.
const PAUSE: f64 = 0.4;

/// Seeds a draft from each open ask the daemon holds one for and this window does not.
pub(super) fn restore(state: &mut State) {
    for ask in state.facts.asks.iter().filter(|a| a.reply.is_none()) {
        let Some(stored) = &ask.draft else {
            continue;
        };
        if state.window.asks.drafts.contains_key(&ask.id) {
            continue;
        }
        let held = ask.draft_files.as_deref().unwrap_or_default();
        let files = stored
            .attachments
            .iter()
            .flatten()
            .filter_map(|id| held.iter().find(|f| &f.attachment.id == id))
            .map(attach::File::restored)
            .collect();
        let mut draft = Draft {
            answers: restored_answers(&stored.answers),
            note: TextEdit::new(stored.note.clone().unwrap_or_default()),
            files,
            generation: stored.generation,
            ..Draft::default()
        };
        draft.saved = draft.form();
        state.window.asks.drafts.insert(ask.id.clone(), draft);
    }
}

fn restored_answers(answers: &[AskAnswer]) -> BTreeMap<String, Answer> {
    answers
        .iter()
        .map(|a| {
            let answer = Answer {
                picked: a.picked.clone(),
                other: TextEdit::new(a.other.clone().unwrap_or_default()),
                text: TextEdit::new(a.text.clone().unwrap_or_default()),
                condition: a.condition.clone().map(TextEdit::new),
            };
            (a.key.clone(), answer)
        })
        .collect()
}

/// Saves each draft whose writing has paused for `PAUSE` since it last changed, and wakes the
/// window when the next one is due.
pub(crate) fn poll(state: &mut State, now: f64, tx: &mut Tx) {
    let mut due = Vec::new();
    let mut wake = f64::INFINITY;
    for (id, draft) in &mut state.window.asks.drafts {
        let form = draft.form();
        if form == draft.saved {
            draft.pending = None;
            continue;
        }
        match &draft.pending {
            Some((pending, at)) if *pending == form => {
                if now >= *at {
                    due.push(id.clone());
                } else {
                    wake = wake.min(*at);
                }
            }
            _ => {
                draft.pending = Some((form, now + PAUSE));
                wake = wake.min(now + PAUSE);
            }
        }
    }
    for id in due {
        save(state, &id, tx);
    }
    if wake.is_finite() {
        tx.wake_at(wake);
    }
}

/// Saves the draft for ask `id` at the next generation. A daemon that took a later generation
/// from elsewhere refuses it; this window's draft is the one being written, so it saves again past
/// that generation.
fn save(state: &mut State, id: &str, tx: &mut Tx) {
    let Some(draft) = state.window.asks.drafts.get_mut(id) else {
        return;
    };
    draft.generation += 1;
    draft.saved = draft.form();
    draft.pending = None;
    let message = AskDraftSaveMessage {
        ask_id: id.to_owned(),
        draft: draft.kept(),
        ..AskDraftSaveMessage::new()
    };
    let id = id.to_owned();
    state.ask(
        move |request_id| AskDraftSaveMessage {
            request_id,
            ..message
        },
        move |state, answer, tx| {
            let Some(stored) = answer
                .parse::<AskDraftSaveResultMessage>()
                .filter(|r| r.stale == Some(true))
                .map(|r| r.generation)
            else {
                return;
            };
            let Some(draft) = state.window.asks.drafts.get_mut(&id) else {
                return;
            };
            if stored >= draft.generation {
                draft.generation = stored;
                save(state, &id, tx);
            }
        },
        tx,
    );
}

impl Draft {
    /// The draft as the daemon keeps it: every answer with something written, the note and the
    /// note's files, as written.
    pub(super) fn kept(&self) -> AskDraft {
        AskDraft {
            generation: self.generation,
            answers: self
                .answers
                .iter()
                .filter_map(|(key, answer)| answer.kept(key))
                .collect(),
            note: written(&self.note),
            attachments: (!self.files.is_empty())
                .then(|| self.files.iter().map(|f| f.id.clone()).collect()),
        }
    }

    /// What `kept` holds apart from its generation, and nothing for a draft with nothing written.
    fn form(&self) -> Value {
        let kept = AskDraft {
            generation: 0,
            ..self.kept()
        };
        if kept.answers.is_empty() && kept.note.is_none() && kept.attachments.is_none() {
            return Value::Null;
        }
        serde_json::to_value(kept).unwrap_or(Value::Null)
    }
}

impl Answer {
    fn kept(&self, key: &str) -> Option<AskAnswer> {
        let answer = AskAnswer {
            key: key.to_owned(),
            picked: self.picked.clone(),
            other: written(&self.other),
            text: written(&self.text),
            condition: self.condition.as_ref().and_then(written),
        };
        let empty = answer.picked.is_empty()
            && answer.other.is_none()
            && answer.text.is_none()
            && answer.condition.is_none();
        (!empty).then_some(answer)
    }
}

fn written(edit: &TextEdit) -> Option<String> {
    (!edit.text.is_empty()).then(|| edit.text.clone())
}
