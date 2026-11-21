//! Files an answer carries on its note and its file questions. Each one uploads to the daemon as soon as it is added,
//! owned by its ask, so a send names files the daemon already holds.

use std::path::{Path, PathBuf};
use std::sync::Arc;

use attn_protocol::{
    AskDraftFile, AttachmentDiscardMessage, AttachmentOwner, AttachmentPutMessage,
    AttachmentResultMessage,
};
use base64::Engine;
use rune::{ClipboardContent, FileDialogOptions, Image, Tx};

use super::Stop;
use crate::State;

/// The bytes one upload request carries, as Quick Capture's do.
const CHUNK: u64 = 524_288;

/// The side of a pasted image's tile in the tray.
pub(crate) const TILE: (f32, f32) = (84.0, 58.0);

/// Where a file's bytes come from: a file on disk, read a chunk at a time, a pasted image, or the
/// daemon, which holds a file a saved draft brought back.
enum Source {
    Path(PathBuf),
    Png(Arc<[u8]>),
    Daemon,
}

#[derive(Clone, Debug, PartialEq)]
enum Upload {
    Running,
    Done,
    Failed(String),
}

pub(crate) struct File {
    pub id: String,
    pub name: String,
    pub size: u64,
    pub picture: Option<Image>,
    /// Where it is attached: a file question, or the note.
    pub stop: Stop,
    source: Source,
    upload: Upload,
}

impl File {
    /// A file of a draft the daemon kept. One whose upload had not finished cannot finish now,
    /// since its bytes stayed on this side.
    pub(crate) fn restored(held: &AskDraftFile) -> File {
        File {
            id: held.attachment.id.clone(),
            name: held.attachment.name.clone(),
            size: held.attachment.bytes.max(0) as u64,
            picture: None,
            source: Source::Daemon,
            upload: match held.ready {
                true => Upload::Done,
                false => Upload::Failed("it was still uploading when attn closed".into()),
            },
        }
    }

    pub(crate) fn uploading(&self) -> bool {
        self.upload == Upload::Running
    }

    pub(crate) fn failed(&self) -> Option<&str> {
        match &self.upload {
            Upload::Failed(why) => Some(why),
            _ => None,
        }
    }
}

/// A paste at a stop: copied files attach, else an image, as a screenshot pastes; plain text is
/// left to the field it was pasted in.
pub(crate) fn paste(
    state: &mut State,
    ask: &str,
    to: &Stop,
    content: &ClipboardContent,
    tx: &mut Tx,
) -> bool {
    if !content.files().is_empty() {
        let files = content.files().iter().map(|p| on_disk(p)).collect();
        add(state, ask, to, files, tx);
        return true;
    }
    if let Some(image) = content.image() {
        let png: Arc<[u8]> = image.png().into();
        add(
            state,
            ask,
            to,
            vec![("Pasted screenshot.png".into(), Source::Png(png))],
            tx,
        );
        return true;
    }
    false
}

/// Files dropped on the sheet attach at a stop of the ask it shows.
pub(crate) fn dropped(state: &mut State, ask: &str, to: &Stop, paths: &[PathBuf], tx: &mut Tx) {
    let files = paths.iter().map(|p| on_disk(p)).collect();
    add(state, ask, to, files, tx);
}

/// Asks which file to attach at a stop of the ask.
pub(crate) fn pick(ask: &str, to: Stop, tx: &mut Tx) {
    let ask = ask.to_owned();
    let options = FileDialogOptions {
        title: "Attach a file to your answer".into(),
        ..FileDialogOptions::default()
    };
    tx.open_file(options, move |state: &mut State, chosen, tx| match chosen {
        Ok(Some(path)) => add(state, &ask, &to, vec![on_disk(&path)], tx),
        Ok(None) => {}
        Err(why) => state.window.asks.error = Some((ask, format!("Cannot choose a file: {why}"))),
    });
}

fn on_disk(path: &Path) -> (String, Source) {
    let name = path.file_name().map_or_else(
        || "Dropped file".to_owned(),
        |n| n.to_string_lossy().into_owned(),
    );
    (name, Source::Path(path.to_owned()))
}

fn add(state: &mut State, ask: &str, to: &Stop, files: Vec<(String, Source)>, tx: &mut Tx) {
    for (name, source) in files {
        let size = match &source {
            Source::Path(path) => match std::fs::metadata(path) {
                Ok(meta) if meta.is_file() => meta.len(),
                Ok(_) => {
                    state.window.asks.error = Some((
                        ask.to_owned(),
                        format!("Cannot attach {name}: it is a folder"),
                    ));
                    continue;
                }
                Err(why) => {
                    state.window.asks.error =
                        Some((ask.to_owned(), format!("Cannot attach {name}: {why}")));
                    continue;
                }
            },
            Source::Png(png) => png.len() as u64,
            Source::Daemon => continue,
        };
        let picture = match &source {
            Source::Png(png) => state.pictures.of_png(png.clone(), TILE.0.max(TILE.1)),
            Source::Path(_) | Source::Daemon => None,
        };
        let id = uuid::Uuid::new_v4().to_string();
        state.draft(ask).files.push(File {
            id: id.clone(),
            name,
            size,
            picture,
            stop: to.clone(),
            source,
            upload: Upload::Running,
        });
        put(state, ask.to_owned(), id, 0, tx);
    }
}

/// Takes a file off the answer, and has the daemon forget it once no chunk of it is in flight.
pub(crate) fn remove(state: &mut State, ask: &str, id: &str, tx: &mut Tx) {
    let Some(draft) = state.window.asks.drafts.get_mut(ask) else {
        return;
    };
    let Some(at) = draft.files.iter().position(|f| f.id == id) else {
        return;
    };
    let removed = draft.files.remove(at);
    if !removed.uploading() {
        discard(state, ask.to_owned(), removed.id, tx);
    }
    super::uploaded(state, ask, tx);
}

fn discard(state: &mut State, ask: String, id: String, tx: &mut Tx) {
    state.ask(
        |request_id| AttachmentDiscardMessage {
            request_id: Some(request_id),
            attachment_id: id,
            ..AttachmentDiscardMessage::new()
        },
        move |state, answer, _| {
            if let Err(why) = answer.outcome() {
                state.window.asks.error = Some((ask, format!("Cannot remove the file: {why}")));
            }
        },
        tx,
    );
}

/// Sends the chunk of file `id` that starts at `offset`.
fn put(state: &mut State, ask: String, id: String, offset: u64, tx: &mut Tx) {
    let Some(file) = held(state, &ask, &id) else {
        return discard(state, ask, id, tx);
    };
    let (size, name) = (file.size, file.name.clone());
    let chunk = match &file.source {
        Source::Path(path) => read_chunk(path, offset),
        Source::Png(png) => {
            let start = (offset as usize).min(png.len());
            let end = (start + CHUNK as usize).min(png.len());
            Ok(png[start..end].to_vec())
        }
        Source::Daemon => Err("the daemon already holds it".to_owned()),
    };
    let bytes = match chunk {
        Ok(bytes) => bytes,
        Err(why) => return failed(state, &ask, &id, why, tx),
    };
    let message = AttachmentPutMessage {
        owner: AttachmentOwner {
            kind: "ask".into(),
            id: ask.clone(),
        },
        attachment_id: id.clone(),
        name,
        offset: offset as i64,
        data_base64: base64::engine::general_purpose::STANDARD.encode(bytes),
        r#final: offset + CHUNK >= size,
        ..AttachmentPutMessage::new()
    };
    state.ask(
        |request_id| AttachmentPutMessage {
            request_id: Some(request_id),
            ..message
        },
        move |state, answer, tx| {
            if held(state, &ask, &id).is_none() {
                return discard(state, ask, id, tx);
            }
            let next = answer
                .outcome()
                .and_then(|reply| {
                    reply
                        .parse::<AttachmentResultMessage>()
                        .and_then(|m| m.result)
                        .and_then(|r| r.upload)
                        .ok_or_else(|| "the daemon sent no upload receipt".to_owned())
                })
                .map(|upload| upload.next_offset.max(0) as u64);
            match next {
                Ok(next) if next < size => put(state, ask, id, next, tx),
                Ok(_) => {
                    set(state, &ask, &id, Upload::Done);
                    super::uploaded(state, &ask, tx);
                }
                Err(why) => failed(state, &ask, &id, why, tx),
            }
        },
        tx,
    );
}

fn read_chunk(path: &Path, offset: u64) -> Result<Vec<u8>, String> {
    use std::io::{Read, Seek, SeekFrom};
    let io = |e: std::io::Error| format!("{}: {e}", path.display());
    let mut file = std::fs::File::open(path).map_err(io)?;
    file.seek(SeekFrom::Start(offset)).map_err(io)?;
    let mut bytes = Vec::new();
    file.take(CHUNK).read_to_end(&mut bytes).map_err(io)?;
    Ok(bytes)
}

/// File `id` of `ask`'s answer, while the ask's draft holds it.
fn held<'a>(state: &'a State, ask: &str, id: &str) -> Option<&'a File> {
    state
        .window
        .asks
        .drafts
        .get(ask)?
        .files
        .iter()
        .find(|f| f.id == id)
}

fn set(state: &mut State, ask: &str, id: &str, upload: Upload) {
    let draft = state.window.asks.drafts.get_mut(ask);
    if let Some(file) = draft.and_then(|d| d.files.iter_mut().find(|f| f.id == id)) {
        file.upload = upload;
    }
}

fn failed(state: &mut State, ask: &str, id: &str, why: String, tx: &mut Tx) {
    if let Some(file) = held(state, ask, id) {
        let said = format!("Cannot attach {}: {why}", file.name);
        state.window.asks.error = Some((ask.to_owned(), said));
    }
    set(state, ask, id, Upload::Failed(why));
    super::uploaded(state, ask, tx);
}
