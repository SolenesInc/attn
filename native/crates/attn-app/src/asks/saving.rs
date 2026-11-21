//! What the person has written for an open ask and not sent, as the daemon keeps it: saved whole
//! at the next generation once editing pauses, and read back from the ask for an ask this window
//! holds no draft for, as at start.

use std::collections::{BTreeMap, BTreeSet};

use attn_client::Connection;
use attn_protocol::{AskAnswer, AskDraft, AskDraftSaveMessage, AskDraftSaveResultMessage};
use rune::{TextEdit, Tx};
use serde_json::Value;

use super::{Answer, Draft, Stop, attach};
use crate::State;

/// How long editing pauses before the draft is saved: an annotation note's pause, measured to
/// clear an ordinary gap between keystrokes, 100 to 250 ms.
const PAUSE: f64 = 0.4;

/// What the daemon holds of a draft as far as this window knows: what it was last sent, or
/// nothing known once a save never reached it, which the next poll saves again, or what a refused
/// save held, which waits for the next edit.
#[derive(Debug, PartialEq)]
pub(super) enum Held {
    Sent(Value),
    Unknown,
    Refused { form: Value, why: String },
}

impl Held {
    fn settled(&self, form: &Value) -> bool {
        matches!(self, Held::Sent(sent) | Held::Refused { form: sent, .. } if sent == form)
    }
}

impl Default for Held {
    fn default() -> Self {
        Held::Sent(Value::Null)
    }
}

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
        let on_note = stored
            .attachments
            .iter()
            .flatten()
            .map(|id| (id, Stop::Note));
        let on_questions = stored.answers.iter().flat_map(|a| {
            a.attachments
                .iter()
                .flatten()
                .map(|id| (id, Stop::Question(a.key.clone())))
        });
        let files = on_note
            .chain(on_questions)
            .filter_map(|(id, stop)| {
                let file = held.iter().find(|f| &f.attachment.id == id)?;
                Some(attach::File::restored(file, stop))
            })
            .collect();
        let mut draft = Draft {
            answers: restored_answers(&stored.answers),
            note: TextEdit::new(stored.note.clone().unwrap_or_default()),
            files,
            generation: stored.generation,
            ..Draft::default()
        };
        draft.saved = Held::Sent(draft.form());
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
/// window when the next one is due. While the daemon is away nothing is due: the connection
/// coming back runs the poll again.
pub(crate) fn poll(state: &mut State, now: f64, tx: &mut Tx) {
    if state.facts.connection != Connection::Connected {
        return;
    }
    let mut due = Vec::new();
    let mut wake = f64::INFINITY;
    for (id, draft) in &mut state.window.asks.drafts {
        let form = draft.form();
        if draft.saved.settled(&form) {
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
/// that generation. A save the daemon never answered leaves what it holds unknown, and one it
/// refused leaves the reason for the sheet to show, unless the ask left meanwhile; the next edit
/// saves again.
fn save(state: &mut State, id: &str, tx: &mut Tx) {
    let Some(draft) = state.window.asks.drafts.get_mut(id) else {
        return;
    };
    draft.generation += 1;
    let form = draft.form();
    draft.saved = Held::Sent(form.clone());
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
            let result = answer.parse::<AskDraftSaveResultMessage>();
            let Some(draft) = state.window.asks.drafts.get_mut(&id) else {
                return;
            };
            match result {
                None => draft.saved = Held::Unknown,
                Some(r) if r.stale == Some(true) && r.generation >= draft.generation => {
                    draft.generation = r.generation;
                    save(state, &id, tx);
                }
                Some(r) if !r.success => {
                    let open = state
                        .facts
                        .asks
                        .iter()
                        .any(|a| a.id == id && a.reply.is_none());
                    if open {
                        draft.saved = Held::Refused {
                            form,
                            why: r.error.unwrap_or_else(|| "the daemon refused it".into()),
                        };
                    }
                }
                Some(_) => {}
            }
        },
        tx,
    );
}

impl Draft {
    /// Why the daemon refused the last save, until a save is taken.
    pub(super) fn refusal(&self) -> Option<&str> {
        match &self.saved {
            Held::Refused { why, .. } => Some(why),
            _ => None,
        }
    }

    /// The draft as the daemon keeps it: every answer with something written, the note and the
    /// note's files, as written.
    pub(super) fn kept(&self) -> AskDraft {
        AskDraft {
            generation: self.generation,
            answers: self.kept_answers(),
            note: written(&self.note),
            attachments: self.file_ids(&Stop::Note),
        }
    }

    /// Each question with something written or a file attached, in key order.
    fn kept_answers(&self) -> Vec<AskAnswer> {
        let attached = self.files.iter().filter_map(|f| match &f.stop {
            Stop::Question(key) => Some(key),
            Stop::Note => None,
        });
        let keys: BTreeSet<&String> = self.answers.keys().chain(attached).collect();
        keys.into_iter()
            .filter_map(|key| {
                let attachments = self.file_ids(&Stop::Question(key.clone()));
                let written = self.answers.get(key).and_then(|a| a.kept(key));
                match (written, attachments) {
                    (Some(answer), attachments) => Some(AskAnswer {
                        attachments,
                        ..answer
                    }),
                    (None, Some(attachments)) => Some(AskAnswer {
                        key: key.clone(),
                        attachments: Some(attachments),
                        ..AskAnswer::default()
                    }),
                    (None, None) => None,
                }
            })
            .collect()
    }

    fn file_ids(&self, stop: &Stop) -> Option<Vec<String>> {
        let ids: Vec<String> = self.files_on(stop).map(|f| f.id.clone()).collect();
        (!ids.is_empty()).then_some(ids)
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
            attachments: None,
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
