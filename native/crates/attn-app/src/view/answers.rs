//! A view's answers: what the user answered, as the home keeps it, the text they are writing,
//! and the calls that give answers and send them to the agent that opened the view.

use std::collections::HashMap;
use std::path::PathBuf;
use std::rc::Rc;

use attn_client::TileKey;
use attn_protocol::{
    Attachment, AttachmentDiscardMessage, ViewAnswer, ViewAnswerMessage, ViewAnswerResultMessage,
    ViewAnswers, ViewSendMessage, ViewSendResultMessage, ViewSent,
};
use rune::{FileDialogOptions, TextEdit, Tx};

use super::Tiles;
use crate::State;
use crate::asks::attach::{self, Uploaded};

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

/// A file the user chose for a question-file, from when they choose it until the home keeps it.
pub(crate) struct Chosen {
    pub(crate) id: String,
    pub(crate) name: String,
    path: PathBuf,
    uploaded: bool,
    pub(crate) failed: Option<String>,
}

/// The files a window is uploading into each tile's file questions, by tile, then by row and
/// question.
#[derive(Default)]
pub(crate) struct Choosing {
    files: HashMap<String, HashMap<(String, String), Chosen>>,
    /// The tiles whose Send waits for their files to finish uploading.
    sends_after_upload: std::collections::HashSet<String>,
}

impl Choosing {
    fn of(&self, tile: &str, (row, part): (&str, &str)) -> Option<&Chosen> {
        self.files
            .get(tile)?
            .get(&(row.to_owned(), part.to_owned()))
    }

    fn of_mut(&mut self, tile: &str, (row, part): (&str, &str)) -> Option<&mut Chosen> {
        self.files
            .get_mut(tile)?
            .get_mut(&(row.to_owned(), part.to_owned()))
    }

    /// The file `id`, while it is still the one chosen for its question.
    fn still(&self, tile: &str, at: (&str, &str), id: &str) -> Option<&Chosen> {
        self.of(tile, at).filter(|c| c.id == id)
    }

    /// Whether a file chosen in `tile` is still uploading.
    fn uploading(&self, tile: &str) -> bool {
        (self
            .files
            .get(tile)
            .into_iter()
            .flat_map(|files| files.values()))
        .any(|c| !c.uploaded && c.failed.is_none())
    }

    pub(crate) fn keep_only(&mut self, shown: &std::collections::HashSet<String>) {
        self.files.retain(|tile, _| shown.contains(tile));
        self.sends_after_upload.retain(|tile| shown.contains(tile));
    }
}

/// The answers a part holds and gives: the home's drafts, the text being written and the files
/// being uploaded, and the row of an `each` or `one-by-one` the part is in.
#[derive(Clone)]
pub(crate) struct Answers<'a> {
    pub(crate) place: &'a TileKey,
    kept: Option<&'a ViewAnswers>,
    tiles: &'a Tiles,
    pub(crate) row: Option<String>,
}

impl<'a> Answers<'a> {
    pub(crate) fn of(place: &'a TileKey, kept: Option<&'a ViewAnswers>, tiles: &'a Tiles) -> Self {
        Answers {
            place,
            kept,
            tiles,
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
        (self.tiles.writing.of(&self.place.tile, row, part).cloned())
            .or_else(|| self.text_in(row, part).map(TextEdit::new))
    }

    /// The files the home keeps as the answer to `part`.
    pub(crate) fn files(&self, part: &str) -> Vec<&'a Attachment> {
        let (Some(kept), Some(draft)) = (self.kept, self.draft(self.row(), Some(part))) else {
            return Vec::new();
        };
        (draft.attachments.iter().flatten())
            .filter_map(|id| kept.files.iter().find(|f| f.id == *id))
            .collect()
    }

    /// The file the user chose for `part` while the home does not keep it yet.
    pub(crate) fn choosing(&self, part: &str) -> Option<&'a Chosen> {
        let chosen = (self.tiles.choosing).of(&self.place.tile, (self.row(), part))?;
        let kept = self.files(part).iter().any(|f| f.id == chosen.id);
        (!kept).then_some(chosen)
    }

    /// Takes the file off `part`'s answer, whether the home keeps it or it is still uploading.
    pub(crate) fn remove(&self, part: &str) -> impl Fn(&mut State, &mut Tx) + 'static {
        let (place, at) = (self.place.clone(), (self.row().to_owned(), part.to_owned()));
        move |state: &mut State, tx: &mut Tx| remove_file(state, &place, at.clone(), tx)
    }

    /// Asks which file answers `part`, and uploads it to the home once chosen.
    pub(crate) fn choose(&self, part: &str) -> impl Fn(&mut State, &mut Tx) + 'static {
        let (place, at) = (self.place.clone(), (self.row().to_owned(), part.to_owned()));
        move |_: &mut State, tx: &mut Tx| choose_file(&place, at.clone(), tx)
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

/// Asks which file answers `part` of `row`, and uploads it to the home once chosen.
pub(crate) fn choose_file(place: &TileKey, at: (String, String), tx: &mut Tx) {
    let place = place.clone();
    let options = FileDialogOptions {
        title: "Choose a file for your answer".into(),
        ..FileDialogOptions::default()
    };
    tx.open_file(options, move |state: &mut State, chosen, tx| match chosen {
        Ok(Some(path)) => upload(state, &place, at.clone(), path, tx),
        Ok(None) => {}
        Err(why) => {
            (state.views).refused(&place.tile, Some(format!("Cannot choose a file: {why}")))
        }
    });
}

fn upload(state: &mut State, place: &TileKey, at: (String, String), path: PathBuf, tx: &mut Tx) {
    let name = attach::file_name(&path);
    let size = match attach::file_size(&path, &name) {
        Ok(size) => size,
        Err(why) => return state.views.refused(&place.tile, Some(why)),
    };
    let Some(owner) = (content_of(state, place).and_then(|c| c.message.answers.as_ref()))
        .map(|a| a.owner.clone())
    else {
        let why = "the home has not said where the view keeps its files".to_owned();
        return state
            .views
            .refused(&place.tile, Some(format!("Cannot attach {name}: {why}")));
    };
    let id = uuid::Uuid::new_v4().to_string();
    let chosen = Chosen {
        id: id.clone(),
        name: name.clone(),
        path,
        uploaded: false,
        failed: None,
    };
    let replaced = (state
        .views
        .choosing
        .files
        .entry(place.tile.clone())
        .or_default())
    .insert(at.clone(), chosen);
    state.views.revision += 1;
    if let Some(replaced) = replaced.filter(|r| r.failed.is_some()) {
        discard(state, replaced.id, tx);
    }
    let (tile, held, i) = (place.tile.clone(), at.clone(), id.clone());
    let wanted = Box::new(move |state: &State| {
        (state.views.choosing)
            .still(&tile, (&held.0, &held.1), &i)
            .is_some()
    });
    let (tile, held, i) = (place.tile.clone(), at.clone(), id.clone());
    let read = Box::new(move |state: &State, offset: u64| {
        match (state.views.choosing).still(&tile, (&held.0, &held.1), &i) {
            Some(chosen) => attach::read_chunk(&chosen.path, offset),
            None => Err("the file is no longer chosen".to_owned()),
        }
    });
    let (place, i) = (place.clone(), id.clone());
    let ended = Box::new(
        move |state: &mut State, how: Uploaded, tx: &mut Tx| match how {
            Uploaded::Done => {
                if let Some(chosen) = state.views.choosing.of_mut(&place.tile, (&at.0, &at.1)) {
                    chosen.uploaded = true;
                }
                let answer = ViewAnswer {
                    part: Some(at.1.clone()),
                    row: (!at.0.is_empty()).then(|| at.0.clone()),
                    attachments: Some(vec![i.clone()]),
                    ..Default::default()
                };
                give(state, &place, answer, tx);
                uploaded(state, &place, tx);
            }
            Uploaded::Failed(why) => {
                failed(state, &place, &at, why);
                uploaded(state, &place, tx);
            }
            Uploaded::Unwanted => discard(state, i.clone(), tx),
        },
    );
    let chunked = attach::Chunked {
        owner,
        id,
        name,
        size,
        wanted,
        read,
        ended,
    };
    attach::upload(state, Rc::new(chunked), 0, tx);
}

fn failed(state: &mut State, place: &TileKey, (row, part): &(String, String), why: String) {
    if let Some(chosen) = state.views.choosing.of_mut(&place.tile, (row, part)) {
        chosen.failed = Some(why);
        state.views.revision += 1;
    }
}

/// Has the home forget a file no answer will name.
fn discard(state: &mut State, id: String, tx: &mut Tx) {
    state.ask(
        |request_id| AttachmentDiscardMessage {
            request_id: Some(request_id),
            attachment_id: id,
            ..AttachmentDiscardMessage::new()
        },
        |_, _, _| {},
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

/// Takes the file off `part` of `row`: forgets the one still uploading, and has the home take
/// back the answer it keeps.
pub(crate) fn remove_file(
    state: &mut State,
    place: &TileKey,
    (row, part): (String, String),
    tx: &mut Tx,
) {
    let removed = (state.views.choosing.files.get_mut(&place.tile))
        .and_then(|files| files.remove(&(row.clone(), part.clone())));
    state.views.revision += 1;
    let answering = removed.as_ref().is_some_and(|r| r.uploaded);
    if let Some(removed) = removed.filter(|r| r.failed.is_some()) {
        discard(state, removed.id, tx);
    }
    let kept = answering
        || (content_of(state, place).and_then(|c| c.message.answers.as_ref())).is_some_and(|a| {
            a.drafts.iter().any(|d| {
                d.row.as_deref().unwrap_or_default() == row
                    && d.part.as_deref() == Some(part.as_str())
                    && d.attachments.is_some()
            })
        });
    if kept {
        let answer = ViewAnswer {
            part: Some(part),
            row: (!row.is_empty()).then_some(row),
            ..Default::default()
        };
        give(state, place, answer, tx);
    }
    uploaded(state, place, tx);
}

/// Sends the tile's answers when its Send waited for uploads, and the last one has ended; a file
/// that could not be attached holds the send, and says why on the tile.
fn uploaded(state: &mut State, place: &TileKey, tx: &mut Tx) {
    let choosing = &mut state.views.choosing;
    if choosing.uploading(&place.tile) || !choosing.sends_after_upload.remove(&place.tile) {
        return;
    }
    let failed = (choosing
        .files
        .get(&place.tile)
        .into_iter()
        .flat_map(|f| f.values()))
    .any(|c| c.failed.is_some());
    if !failed {
        send(state, place, tx);
    }
}

/// Sends the view's answers to the agent that opened it, with the text still being written,
/// once the files still uploading are in.
pub(crate) fn send(state: &mut State, place: &TileKey, tx: &mut Tx) {
    if state.views.choosing.uploading(&place.tile) {
        state
            .views
            .choosing
            .sends_after_upload
            .insert(place.tile.clone());
        return;
    }
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
