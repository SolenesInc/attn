//! Files attached to the draft: copied into the draft's folder off the UI thread, then uploaded
//! to the daemon in chunks before the draft is sent, so a send names files the daemon holds.

use std::path::{Path, PathBuf};
use std::sync::Arc;

use attn_protocol::{
    QuickCaptureAttachmentDiscardMessage, QuickCaptureAttachmentPutMessage, QuickCaptureListMessage,
};
use base64::Engine;
use rune::Tx;

use super::draft::{Folder, StoredFile, commit};
use super::{Busy, Draft, Message, Panel, Window, result};

/// The bytes one upload request carries, as the Tauri app sends them.
const CHUNK: u64 = 524_288;

/// Where an attached file's bytes come from.
pub(crate) enum Source {
    Path(PathBuf),
    Png(Arc<[u8]>),
}

#[derive(Clone, Debug, PartialEq)]
pub(crate) enum Phase {
    /// Being copied into the draft's folder.
    Copying,
    /// In the draft's folder.
    Stored {
        size: u64,
        media_type: String,
        path: PathBuf,
    },
}

/// A file being copied into the folder of the draft it was attached to, whichever profile the
/// panel shows when the copy ends. The draft on disk names the file only once its bytes are
/// there, as the Tauri app reads it.
pub(super) struct Copy {
    profile: String,
    draft: String,
    id: String,
    name: String,
    /// Why the copy failed while another profile's draft was showing, for the draft's return.
    failed: Option<String>,
}

impl Copy {
    pub(super) fn running(&self) -> bool {
        self.failed.is_none()
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
enum Upload {
    #[default]
    No,
    Running,
    Done,
}

#[derive(Clone, Debug)]
pub(crate) struct Attachment {
    pub id: String,
    pub name: String,
    pub phase: Phase,
    upload: Upload,
}

impl Attachment {
    pub(crate) fn ready(&self) -> bool {
        matches!(self.phase, Phase::Stored { .. })
    }

    pub(crate) fn size(&self) -> u64 {
        match self.phase {
            Phase::Stored { size, .. } => size,
            Phase::Copying => 0,
        }
    }

    pub(crate) fn image(&self) -> Option<&Path> {
        match &self.phase {
            Phase::Stored {
                media_type, path, ..
            } if media_type.starts_with("image/") => Some(path),
            _ => None,
        }
    }
}

/// The draft's files as the disk names them, then those still copying into it. A copy that
/// failed while another profile's draft showed says so now.
pub(super) fn restored(
    panel: &mut Panel,
    folder: &Folder,
    files: Vec<StoredFile>,
) -> Vec<Attachment> {
    let mut kept: Vec<Attachment> = files
        .into_iter()
        .map(|StoredFile { id, name }| {
            let path = folder.file(&id);
            Attachment {
                phase: Phase::Stored {
                    size: std::fs::metadata(&path).map_or(0, |m| m.len()),
                    media_type: media_type(&path, &name),
                    path,
                },
                id,
                name,
                upload: Upload::No,
            }
        })
        .collect();
    let (profile, draft) = (&panel.draft.profile, &panel.draft.id);
    let (ours, others): (Vec<Copy>, Vec<Copy>) = std::mem::take(&mut panel.copies)
        .into_iter()
        .partition(|c| &c.profile == profile && &c.draft == draft);
    panel.copies = others;
    for copy in ours {
        match copy.failed {
            None => {
                kept.push(Attachment {
                    id: copy.id.clone(),
                    name: copy.name.clone(),
                    phase: Phase::Copying,
                    upload: Upload::No,
                });
                panel.copies.push(copy);
            }
            Some(why) => panel.error = Some(format!("Cannot attach {}: {why}", copy.name)),
        }
    }
    kept
}

/// The ids of the files still copying into `profile`'s draft folder.
pub(super) fn copying_into(panel: &Panel, profile: &str) -> Vec<String> {
    panel
        .copies
        .iter()
        .filter(|c| c.profile == profile && c.running())
        .map(|c| c.id.clone())
        .collect()
}

/// Attaches `files` to the draft: each shows at once and becomes ready once its bytes are in
/// the draft's folder.
pub(crate) fn add(panel: &mut Panel, files: Vec<(String, Source)>, tx: &mut Tx) {
    if !panel.draft.restored {
        panel.error =
            Some("Draft is still loading. Try adding the file again when it is ready.".into());
        return;
    }
    if !panel.editable() || files.is_empty() {
        return;
    }
    let (Ok(folder), Some(window)) = (panel.folder(), panel.window.clone()) else {
        return;
    };
    panel.saved_for = None;
    for (name, source) in files {
        let id = uuid::Uuid::new_v4().to_string();
        let target = folder.file(&id);
        panel.draft.files.push(Attachment {
            id: id.clone(),
            name: name.clone(),
            phase: Phase::Copying,
            upload: Upload::No,
        });
        panel.copies.push(Copy {
            profile: panel.draft.profile.clone(),
            draft: panel.draft.id.clone(),
            id: id.clone(),
            name: name.clone(),
            failed: None,
        });
        let profile = panel.draft.profile.clone();
        copy_off_thread(window.clone(), profile, id, name, source, target);
    }
    panel.edited(tx);
}

fn copy_off_thread(
    window: Arc<dyn Window>,
    profile: String,
    id: String,
    name: String,
    from: Source,
    to: PathBuf,
) {
    std::thread::spawn(move || {
        let copied = match &from {
            Source::Path(path) => std::fs::read(path)
                .map_err(|e| format!("Cannot read {}: {e}", path.display()))
                .and_then(|bytes| commit(&to, &bytes)),
            Source::Png(png) => commit(&to, png),
        }
        .map(|()| Phase::Stored {
            size: std::fs::metadata(&to).map_or(0, |m| m.len()),
            media_type: media_type(&to, &name),
            path: to,
        });
        window.update(Box::new(move |panel, tx| {
            arrived(panel, &profile, &id, copied, tx);
        }));
    });
}

/// A copy ended. When another profile's draft shows, the draft the file was attached to names
/// it on disk now, and a failure waits for that draft's return.
fn arrived(panel: &mut Panel, profile: &str, id: &str, copied: Result<Phase, String>, tx: &mut Tx) {
    let Some(copy) = panel
        .copies
        .iter()
        .position(|c| c.profile == profile && c.id == id)
    else {
        return;
    };
    if panel.draft.profile != profile {
        let named = copied.and_then(|_| {
            let Copy {
                draft, id, name, ..
            } = &panel.copies[copy];
            Folder::of(&panel.data_dir, profile)?.name(
                draft,
                StoredFile {
                    id: id.clone(),
                    name: name.clone(),
                },
            )
        });
        match named {
            Ok(()) => drop(panel.copies.remove(copy)),
            Err(why) => panel.copies[copy].failed = Some(why),
        }
        return;
    }
    panel.copies.remove(copy);
    let Some(at) = panel.draft.files.iter().position(|f| f.id == id) else {
        return;
    };
    match copied {
        Ok(phase) => {
            panel.draft.files[at].phase = phase;
            panel.edited(tx);
            stage(panel, tx);
        }
        Err(why) => {
            let file = panel.draft.files.remove(at);
            panel.error = Some(format!("Cannot attach {}: {why}", file.name));
        }
    }
}

/// Removes a file from the draft: the draft without it is saved first, then the daemon forgets
/// its bytes.
pub(crate) fn remove(panel: &mut Panel, id: &str, tx: &mut Tx) {
    if !panel.editable() {
        return;
    }
    let Some(at) = panel.draft.files.iter().position(|f| f.id == id) else {
        return;
    };
    let removed = panel.draft.files.remove(at);
    if let Err(why) = panel.write() {
        panel.draft.files.insert(at, removed);
        panel.error = Some(format!("Cannot remove file: {why}"));
        return;
    }
    panel.busy = Busy::Saving;
    if removed.upload != Upload::Running {
        discard_removed(panel, removed.id, tx);
    }
}

/// Has the daemon forget a file the person removed, once no upload of it is in flight, and
/// frees the draft when it answers.
fn discard_removed(panel: &mut Panel, id: String, tx: &mut Tx) {
    let capture = panel.draft.id.clone();
    discard_one(
        panel,
        capture,
        id,
        |panel, failure| {
            panel.busy = Busy::Idle;
            if let Some(why) = failure {
                panel.error = Some(format!("Cannot remove file: {why}"));
            }
        },
        tx,
    );
}

fn removed(panel: &Panel, id: &str) -> bool {
    !panel.draft.files.iter().any(|f| f.id == id)
}

/// Has the daemon forget every file of a draft that was discarded.
pub(super) fn forget(panel: &mut Panel, discarded: &Draft, tx: &mut Tx) {
    for file in &discarded.files {
        discard_one(
            panel,
            discarded.id.clone(),
            file.id.clone(),
            |panel, failure| {
                if failure.is_some() {
                    panel.error =
                        Some("Draft discarded. Uploaded files could not be cleaned up.".into());
                }
            },
            tx,
        );
    }
}

fn discard_one(
    panel: &mut Panel,
    capture: String,
    attachment: String,
    then: impl FnOnce(&mut Panel, Option<String>) + 'static,
    tx: &mut Tx,
) {
    panel.ask(
        |request_id| {
            Message::Discard(QuickCaptureAttachmentDiscardMessage {
                request_id: Some(request_id),
                capture_id: capture,
                attachment_id: attachment,
                ..QuickCaptureAttachmentDiscardMessage::new()
            })
        },
        move |panel, answer, _| then(panel, result(answer).err().map(|f| f.say())),
        tx,
    );
}

/// Whether the daemon holds every file of the draft.
pub(super) fn staged(panel: &Panel) -> bool {
    panel.draft.files.iter().all(|f| f.upload == Upload::Done)
}

/// Uploads the draft's files the daemon does not hold yet, then runs `then`.
pub(super) fn stage_then(panel: &mut Panel, tx: &mut Tx, then: fn(&mut Panel, &mut Tx)) {
    panel.after_staging = Some(then);
    upload(panel, tx);
}

/// Starts uploading the draft's files while the draft sits idle and connected, so a send
/// later waits on less.
pub(super) fn stage(panel: &mut Panel, tx: &mut Tx) {
    if panel.editable()
        && panel.connected()
        && !panel.draft.files.is_empty()
        && panel.draft.files.iter().all(Attachment::ready)
    {
        upload(panel, tx);
    }
}

fn upload(panel: &mut Panel, tx: &mut Tx) {
    let waiting: Vec<String> = panel
        .draft
        .files
        .iter()
        .filter(|f| f.upload == Upload::No && f.ready())
        .map(|f| f.id.clone())
        .collect();
    if waiting.is_empty() {
        if staged(panel) {
            finish(panel, tx);
        }
        return;
    }
    if let Err(why) = panel.write() {
        return fail(panel, why);
    }
    for file in &mut panel.draft.files {
        if waiting.contains(&file.id) {
            file.upload = Upload::Running;
        }
    }
    let capture = panel.draft.id.clone();
    panel.ask(
        |request_id| {
            Message::List(QuickCaptureListMessage {
                request_id: Some(request_id),
                limit: 1,
                ..QuickCaptureListMessage::new()
            })
        },
        move |panel, answer, tx| {
            if panel.draft.id != capture {
                return;
            }
            let (gone, waiting): (Vec<_>, Vec<_>) =
                waiting.into_iter().partition(|id| removed(panel, id));
            for id in gone {
                discard_removed(panel, id, tx);
            }
            let assets = match result(answer) {
                Ok(result) => result.list.map(|l| l.draft_assets).unwrap_or_default(),
                Err(failure) => return fail(panel, failure.say()),
            };
            for id in waiting {
                let asset = assets
                    .iter()
                    .find(|a| a.capture_id == capture && a.attachment_id == id);
                match asset {
                    Some(a) if a.state == "ready" => done(panel, &id, tx),
                    found => {
                        let offset = found.map_or(0, |a| a.next_offset.max(0) as u64);
                        put(panel, id, offset, tx);
                    }
                }
            }
        },
        tx,
    );
}

/// Sends the chunk of file `id` that starts at `offset`.
fn put(panel: &mut Panel, id: String, offset: u64, tx: &mut Tx) {
    let Some(file) = panel.draft.files.iter().find(|f| f.id == id) else {
        return discard_removed(panel, id, tx);
    };
    let Phase::Stored { size, path, .. } = &file.phase else {
        return;
    };
    let (size, name) = (*size, file.name.clone());
    let bytes = match read_chunk(path, offset) {
        Ok(bytes) => bytes,
        Err(why) => return fail(panel, why),
    };
    let capture = panel.draft.id.clone();
    let message = QuickCaptureAttachmentPutMessage {
        capture_id: capture.clone(),
        attachment_id: id.clone(),
        name: name.clone(),
        offset: offset as i64,
        data_base64: base64::engine::general_purpose::STANDARD.encode(bytes),
        r#final: offset + CHUNK >= size,
        ..QuickCaptureAttachmentPutMessage::new()
    };
    panel.ask(
        |request_id| {
            Message::Put(QuickCaptureAttachmentPutMessage {
                request_id: Some(request_id),
                ..message
            })
        },
        move |panel, answer, tx| {
            if panel.draft.id != capture {
                return;
            }
            if removed(panel, &id) {
                return discard_removed(panel, id, tx);
            }
            match result(answer).map(|r| r.upload) {
                Ok(Some(upload)) => {
                    let next = upload.next_offset.max(0) as u64;
                    if next < size {
                        put(panel, id, next, tx);
                    } else {
                        done(panel, &id, tx);
                    }
                }
                Ok(None) => fail(
                    panel,
                    format!("No upload receipt for {name}. Your draft is retained."),
                ),
                Err(failure) => fail(panel, failure.say()),
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

fn done(panel: &mut Panel, id: &str, tx: &mut Tx) {
    if let Some(file) = panel.draft.files.iter_mut().find(|f| f.id == id) {
        file.upload = Upload::Done;
    }
    if staged(panel) {
        finish(panel, tx);
    }
}

fn finish(panel: &mut Panel, tx: &mut Tx) {
    if let Some(then) = panel.after_staging.take() {
        then(panel, tx);
    }
}

fn fail(panel: &mut Panel, why: String) {
    for file in &mut panel.draft.files {
        if file.upload == Upload::Running {
            file.upload = Upload::No;
        }
    }
    if panel.after_staging.take().is_some() {
        panel.busy = Busy::Idle;
    }
    panel.error = Some(format!("File upload: {why}"));
}

/// Forgets which uploads ran: a new connection may hold none of them, so the next stage asks
/// the daemon again.
pub(super) fn disconnected(panel: &mut Panel) {
    for file in &mut panel.draft.files {
        if file.upload == Upload::Running {
            file.upload = Upload::No;
        }
    }
}

/// The type of a file, from its first bytes or else its name.
fn media_type(path: &Path, name: &str) -> String {
    use std::io::Read;
    let mut head = [0u8; 12];
    let read = std::fs::File::open(path)
        .and_then(|mut f| f.read(&mut head))
        .unwrap_or(0);
    let head = &head[..read];
    let by_bytes = if head.starts_with(b"\x89PNG") {
        Some("image/png")
    } else if head.starts_with(&[0xFF, 0xD8, 0xFF]) {
        Some("image/jpeg")
    } else if head.starts_with(b"GIF8") {
        Some("image/gif")
    } else if head.len() >= 12 && &head[..4] == b"RIFF" && &head[8..12] == b"WEBP" {
        Some("image/webp")
    } else if head.starts_with(b"%PDF") {
        Some("application/pdf")
    } else {
        None
    };
    let extension = name.rsplit_once('.').map(|(_, e)| e.to_ascii_lowercase());
    let by_name = match extension.as_deref() {
        Some("png") => "image/png",
        Some("jpg" | "jpeg") => "image/jpeg",
        Some("gif") => "image/gif",
        Some("webp") => "image/webp",
        Some("pdf") => "application/pdf",
        Some("txt" | "md") => "text/plain",
        _ => "application/octet-stream",
    };
    by_bytes.unwrap_or(by_name).to_owned()
}
