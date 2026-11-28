//! Files an answer carries on its note and its file questions. Each one uploads to the daemon as soon as it is added,
//! owned by its ask, so a send names files the daemon already holds.

use std::path::{Path, PathBuf};
use std::rc::Rc;
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
    pub(crate) fn restored(held: &AskDraftFile, stop: Stop) -> File {
        File {
            id: held.attachment.id.clone(),
            name: held.attachment.name.clone(),
            size: held.attachment.bytes.max(0) as u64,
            picture: None,
            stop,
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
/// Where a paste lands: a field, which types the clipboard's text before an image beside it,
/// or the sheet, where nothing types.
#[derive(Clone, Copy, PartialEq, Eq)]
pub(crate) enum Into {
    Field,
    Sheet,
}

pub(crate) fn paste(
    state: &mut State,
    ask: &str,
    to: &Stop,
    content: &ClipboardContent,
    into: Into,
    tx: &mut Tx,
) -> bool {
    if !content.files().is_empty() {
        let files = content.files().iter().map(|p| on_disk(p)).collect();
        add(state, ask, to, files, tx);
        return true;
    }
    if into == Into::Field && !content.text().is_empty() {
        return false;
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
    (file_name(path), Source::Path(path.to_owned()))
}

/// The name a file at `path` attaches as.
pub(crate) fn file_name(path: &Path) -> String {
    path.file_name().map_or_else(
        || "Dropped file".to_owned(),
        |n| n.to_string_lossy().into_owned(),
    )
}

/// The size of the file at `path`, or why `name` cannot be attached from there.
pub(crate) fn file_size(path: &Path, name: &str) -> Result<u64, String> {
    match std::fs::metadata(path) {
        Ok(meta) if meta.is_file() => Ok(meta.len()),
        Ok(_) => Err(format!("Cannot attach {name}: it is a folder")),
        Err(why) => Err(format!("Cannot attach {name}: {why}")),
    }
}

fn add(state: &mut State, ask: &str, to: &Stop, files: Vec<(String, Source)>, tx: &mut Tx) {
    for (name, source) in files {
        let size = match &source {
            Source::Path(path) => match file_size(path, &name) {
                Ok(size) => size,
                Err(why) => {
                    state.window.asks.error = Some((ask.to_owned(), why));
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
        put(state, ask.to_owned(), id, tx);
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

/// Reads at most a chunk of a file's bytes, from an offset.
pub(crate) type ReadChunk = Box<dyn Fn(&State, u64) -> Result<Vec<u8>, String>>;

/// Hears how an upload ended.
pub(crate) type Ended = Box<dyn Fn(&mut State, Uploaded, &mut Tx)>;

/// A file going up to the daemon a chunk at a time, and what its owner does around it.
pub(crate) struct Chunked {
    pub(crate) owner: AttachmentOwner,
    pub(crate) id: String,
    pub(crate) name: String,
    pub(crate) size: u64,
    /// Whether the owner still wants the file; the upload stops once it does not.
    pub(crate) wanted: Box<dyn Fn(&State) -> bool>,
    pub(crate) read: ReadChunk,
    pub(crate) ended: Ended,
}

/// How an upload ended.
pub(crate) enum Uploaded {
    Done,
    Failed(String),
    /// The owner no longer wanted the file, so the daemon should forget it.
    Unwanted,
}

/// Sends the chunk of `file` that starts at `offset`, and each chunk after it.
pub(crate) fn upload(state: &mut State, file: Rc<Chunked>, offset: u64, tx: &mut Tx) {
    if !(file.wanted)(state) {
        return (file.ended)(state, Uploaded::Unwanted, tx);
    }
    let bytes = match (file.read)(state, offset) {
        Ok(bytes) => bytes,
        Err(why) => return (file.ended)(state, Uploaded::Failed(why), tx),
    };
    let message = AttachmentPutMessage {
        owner: file.owner.clone(),
        attachment_id: file.id.clone(),
        name: file.name.clone(),
        offset: offset as i64,
        data_base64: base64::engine::general_purpose::STANDARD.encode(bytes),
        r#final: offset + CHUNK >= file.size,
        ..AttachmentPutMessage::new()
    };
    state.ask(
        |request_id| AttachmentPutMessage {
            request_id: Some(request_id),
            ..message
        },
        move |state, answer, tx| {
            if !(file.wanted)(state) {
                return (file.ended)(state, Uploaded::Unwanted, tx);
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
                Ok(next) if next < file.size => upload(state, file, next, tx),
                Ok(_) => (file.ended)(state, Uploaded::Done, tx),
                Err(why) => (file.ended)(state, Uploaded::Failed(why), tx),
            }
        },
        tx,
    );
}

/// Uploads file `id` of `ask`'s answer, owned by the ask.
fn put(state: &mut State, ask: String, id: String, tx: &mut Tx) {
    let Some(file) = held(state, &ask, &id) else {
        return discard(state, ask, id, tx);
    };
    let (name, size) = (file.name.clone(), file.size);
    let (a, i) = (ask.clone(), id.clone());
    let wanted = Box::new(move |state: &State| held(state, &a, &i).is_some());
    let (a, i) = (ask.clone(), id.clone());
    let read =
        Box::new(
            move |state: &State, offset: u64| match held(state, &a, &i).map(|f| &f.source) {
                Some(Source::Path(path)) => read_chunk(path, offset),
                Some(Source::Png(png)) => {
                    let start = (offset as usize).min(png.len());
                    let end = (start + CHUNK as usize).min(png.len());
                    Ok(png[start..end].to_vec())
                }
                _ => Err("the daemon already holds it".to_owned()),
            },
        );
    let (a, i) = (ask.clone(), id.clone());
    let ended = Box::new(
        move |state: &mut State, how: Uploaded, tx: &mut Tx| match how {
            Uploaded::Done => {
                set(state, &a, &i, Upload::Done);
                super::uploaded(state, &a, tx);
            }
            Uploaded::Failed(why) => failed(state, &a, &i, why, tx),
            Uploaded::Unwanted => discard(state, a.clone(), i.clone(), tx),
        },
    );
    let owner = AttachmentOwner {
        kind: "ask".into(),
        id: ask,
    };
    let chunked = Chunked {
        owner,
        id,
        name,
        size,
        wanted,
        read,
        ended,
    };
    upload(state, Rc::new(chunked), 0, tx);
}

pub(crate) fn read_chunk(path: &Path, offset: u64) -> Result<Vec<u8>, String> {
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
