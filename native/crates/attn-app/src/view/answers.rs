//! A view's answers: what the user answered, as the home keeps it, the text they are writing,
//! and the calls that give answers and send them to the agent that opened the view.

use std::collections::HashMap;

use attn_client::TileKey;
use attn_protocol::{
    ViewAnswer, ViewAnswerMessage, ViewAnswerResultMessage, ViewAnswers, ViewSendMessage,
    ViewSendResultMessage, ViewSent,
};
use rune::{TextEdit, Tx};

use crate::State;

pub(crate) const KEPT: &str = "kept";
pub(crate) const SKIPPED: &str = "skipped";

/// The text a window is writing into a view's questions, by tile, then by row and question.
#[derive(Default)]
pub(crate) struct Writing {
    edits: HashMap<String, HashMap<(String, String), Written>>,
}

/// Text being written into a question, and the `Value:` text the view gave the question.
struct Written {
    edit: TextEdit,
    value: Option<String>,
}

impl Writing {
    pub(crate) fn of(&self, tile: &str, row: &str, part: &str) -> Option<&TextEdit> {
        let written = self
            .edits
            .get(tile)?
            .get(&(row.to_owned(), part.to_owned()))?;
        Some(&written.edit)
    }

    fn edit(
        &mut self,
        tile: &str,
        (row, part): (&str, &str),
        starting: &str,
        value: Option<String>,
    ) -> &mut TextEdit {
        let written = (self.edits.entry(tile.to_owned()).or_default())
            .entry((row.to_owned(), part.to_owned()))
            .or_insert_with(|| Written {
                edit: TextEdit::new(starting),
                value,
            });
        &mut written.edit
    }

    /// The answer the text written into `part` of `row` gives: the text, or none when it reads
    /// as the question's `Value:` text, or is blank where the question has none.
    fn answer(&self, tile: &str, row: &str, part: &str) -> Option<ViewAnswer> {
        let written = self
            .edits
            .get(tile)?
            .get(&(row.to_owned(), part.to_owned()))?;
        let text = &written.edit.text;
        let unchanged = match &written.value {
            Some(value) => value == text,
            None => text.trim().is_empty(),
        };
        Some(ViewAnswer {
            part: Some(part.to_owned()),
            row: (!row.is_empty()).then(|| row.to_owned()),
            text: (!unchanged).then(|| text.clone()),
            ..Default::default()
        })
    }

    pub(crate) fn keep_only(&mut self, shown: &std::collections::HashSet<String>) {
        self.edits.retain(|tile, _| shown.contains(tile));
    }
}

/// The answers a part holds and gives: the home's drafts, the text being written, and the row
/// of an `each` or `one-by-one` the part is in.
#[derive(Clone)]
pub(crate) struct Answers<'a> {
    pub(crate) place: &'a TileKey,
    kept: Option<&'a ViewAnswers>,
    writing: &'a Writing,
    pub(crate) row: Option<String>,
}

impl<'a> Answers<'a> {
    pub(crate) fn of(
        place: &'a TileKey,
        kept: Option<&'a ViewAnswers>,
        writing: &'a Writing,
    ) -> Self {
        Answers {
            place,
            kept,
            writing,
            row: None,
        }
    }

    /// The answers inside the row `row`.
    pub(crate) fn within(&self, row: String) -> Answers<'a> {
        Answers {
            row: Some(row),
            ..self.clone()
        }
    }

    fn row(&self) -> &str {
        self.row.as_deref().unwrap_or_default()
    }

    fn draft(&self, row: &str, part: Option<&str>) -> Option<&'a ViewAnswer> {
        let kept = self.kept?;
        kept.drafts
            .iter()
            .find(|d| d.row.as_deref().unwrap_or_default() == row && d.part.as_deref() == part)
    }

    /// What the user did with the row `row` of a `one-by-one`: kept or skipped.
    pub(crate) fn step(&self, row: &str) -> Option<&'a str> {
        self.draft(row, None)?.step.as_deref()
    }

    pub(crate) fn picked(&self, part: &str) -> &'a [String] {
        self.draft(self.row(), Some(part))
            .and_then(|d| d.picked.as_deref())
            .unwrap_or_default()
    }

    /// The text the user gave `part` in the row `row`, as the home keeps it.
    pub(crate) fn text_in(&self, row: &str, part: &str) -> Option<&'a str> {
        self.draft(row, Some(part))?.text.as_deref()
    }

    /// The text being written into `part`, or else the one the home keeps.
    pub(crate) fn written(&self, part: &str) -> Option<TextEdit> {
        let row = self.row();
        (self.writing.of(&self.place.tile, row, part).cloned())
            .or_else(|| self.text_in(row, part).map(TextEdit::new))
    }

    /// When the user last sent the view's answers.
    pub(crate) fn sent(&self) -> Option<&'a ViewSent> {
        self.kept?.sent.as_ref()
    }

    /// Picks `label` of `part`: the one answer of a question, or one more of a question-multi,
    /// where picking it again takes it back.
    pub(crate) fn pick(
        &self,
        part: &str,
        label: &str,
        many: bool,
    ) -> impl Fn(&mut State, &mut Tx) + 'static {
        let mut picked = self.picked(part).to_vec();
        let label = label.to_owned();
        if many {
            match picked.iter().position(|p| *p == label) {
                Some(at) => {
                    picked.remove(at);
                }
                None => picked.push(label),
            }
        } else {
            picked = vec![label];
        }
        let answer = ViewAnswer {
            part: Some(part.to_owned()),
            row: self.row.clone(),
            picked: (!picked.is_empty()).then_some(picked),
            ..Default::default()
        };
        let place = self.place.clone();
        move |state: &mut State, tx: &mut Tx| give(state, &place, answer.clone(), tx)
    }

    /// Where the text the user writes into `part` lives while they write it, starting from
    /// `starting`; `value` is the question's `Value:` text.
    pub(crate) fn writes(
        &self,
        part: &str,
        starting: &str,
        value: Option<String>,
    ) -> impl for<'s> Fn(&'s mut State) -> &'s mut TextEdit + 'static {
        let (tile, row, part, starting) = (
            self.place.tile.clone(),
            self.row().to_owned(),
            part.to_owned(),
            starting.to_owned(),
        );
        move |state: &mut State| {
            state.views.revision += 1;
            state
                .views
                .writing
                .edit(&tile, (&row, &part), &starting, value.clone())
        }
    }

    /// Keeps the text the user wrote into `part` once they leave it.
    pub(crate) fn wrote(&self, part: &str) -> impl Fn(&mut State, &mut Tx) + 'static {
        let (place, row, part) = (self.place.clone(), self.row().to_owned(), part.to_owned());
        move |state: &mut State, tx: &mut Tx| keep_written(state, &place, (&row, &part), tx)
    }
}

/// The content the tile at `place` shows.
pub(crate) fn content_of<'a>(
    state: &'a State,
    place: &TileKey,
) -> Option<&'a attn_client::TileContent> {
    state.facts.tile_contents.get(place)
}

/// Gives the home the text written into `part` of `row` when it is not the answer it keeps.
fn keep_written(state: &mut State, place: &TileKey, (row, part): (&str, &str), tx: &mut Tx) {
    let Some(answer) = state.views.writing.answer(&place.tile, row, part) else {
        return;
    };
    let kept = (content_of(state, place).and_then(|c| c.message.answers.as_ref()))
        .and_then(|a| {
            a.drafts.iter().find(|d| {
                d.row.as_deref().unwrap_or_default() == row && d.part.as_deref() == Some(part)
            })
        })
        .and_then(|d| d.text.clone());
    if answer.text != kept {
        give(state, place, answer, tx);
    }
}

/// Gives `answer` to the home, which keeps it as a draft at once.
pub(crate) fn give(state: &mut State, place: &TileKey, answer: ViewAnswer, tx: &mut Tx) {
    let place = place.clone();
    let (desktop_id, tile_id) = (place.desktop.clone(), place.tile.clone());
    state.ask(
        move |request_id| ViewAnswerMessage {
            request_id,
            desktop_id,
            tile_id,
            answer,
            ..ViewAnswerMessage::new()
        },
        move |state, answer, _| {
            let refused = match answer.parse::<ViewAnswerResultMessage>() {
                Some(r) if r.success => None,
                Some(r) => Some(
                    r.error
                        .unwrap_or_else(|| "the daemon refused the answer".into()),
                ),
                None => Some("the daemon did not answer, so the answer may not be kept".into()),
            };
            state.views.refused(&place.tile, refused);
        },
        tx,
    );
}

/// What the user did with the row `row` of a `one-by-one`.
pub(crate) fn step(state: &mut State, place: &TileKey, row: String, step: &str, tx: &mut Tx) {
    let answer = ViewAnswer {
        row: Some(row),
        step: Some(step.to_owned()),
        ..Default::default()
    };
    give(state, place, answer, tx);
}

/// Sends the view's answers to the agent that opened it, with the text still being written.
pub(crate) fn send(state: &mut State, place: &TileKey, tx: &mut Tx) {
    let writing: Vec<(String, String)> = (state.views.writing.edits.get(&place.tile))
        .map(|edits| edits.keys().cloned().collect())
        .unwrap_or_default();
    for (row, part) in writing {
        keep_written(state, place, (&row, &part), tx);
    }
    let place = place.clone();
    let (desktop_id, tile_id) = (place.desktop.clone(), place.tile.clone());
    state.ask(
        move |request_id| ViewSendMessage {
            request_id,
            desktop_id,
            tile_id,
            ..ViewSendMessage::new()
        },
        move |state, answer, _| {
            let refused = match answer.parse::<ViewSendResultMessage>() {
                Some(r) if r.success => None,
                Some(r) => Some(
                    r.error
                        .unwrap_or_else(|| "the daemon refused to send".into()),
                ),
                None => Some("the daemon did not answer, so the answers may not have gone".into()),
            };
            state.views.refused(&place.tile, refused);
        },
        tx,
    );
}
