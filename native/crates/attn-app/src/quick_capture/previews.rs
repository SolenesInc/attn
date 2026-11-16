//! The pictures of the images the panel shows: each image file of the draft, read from the
//! draft's folder, and each image a listed Recent message sent, read from the daemon a chunk at
//! a time as the Tauri panel reads it.

use std::collections::HashMap;
use std::path::PathBuf;

use attn_protocol::{QuickCaptureAttachmentGetMessage, QuickCaptureRecord};
use base64::Engine;
use rune::{Image, ImageCache, Tx};

use super::{Message, Panel, result};

/// The largest an image draws in the panel, in points, which an SVG rasterizes to fit.
const LARGEST: f32 = 132.0;
const SCALE: f32 = 2.0;

#[derive(Default)]
pub(crate) struct Previews {
    cache: Option<ImageCache>,
    /// The draft's image files by where they are stored.
    drafted: HashMap<PathBuf, Image>,
    /// How many held pictures had finished decoding when the panel last drew.
    finished: usize,
}

/// A sent image's picture, while it downloads, once it does, or why it could not.
pub(crate) enum Sent {
    Loading,
    Shown(Image),
    Failed(String),
}

impl Previews {
    pub(super) fn start(&mut self, wake: rune::Waker) {
        self.cache = Some(ImageCache::new(wake));
    }

    pub(crate) fn drafted(&self, path: &std::path::Path) -> Option<&Image> {
        self.drafted.get(path)
    }
}

impl Panel {
    /// Whether a picture is still decoding, which will wake the panel when it is done.
    pub fn decoding(&self) -> bool {
        self.pictures().any(|image| !image.is_ready())
    }

    fn pictures(&self) -> impl Iterator<Item = &Image> {
        let sent = self.history.images.values().filter_map(|s| match s {
            Sent::Shown(image) => Some(image),
            _ => None,
        });
        self.previews.drafted.values().chain(sent)
    }
}

/// Reads each image file the draft holds and forgets those it no longer holds. Returns whether
/// a picture finished decoding since the panel last drew.
pub(super) fn follow(panel: &mut Panel) -> bool {
    let wanted: Vec<PathBuf> = panel
        .draft
        .files
        .iter()
        .filter_map(|f| f.image().map(PathBuf::from))
        .collect();
    let previews = &mut panel.previews;
    previews.drafted.retain(|path, _| wanted.contains(path));
    if let Some(cache) = &previews.cache {
        for path in wanted {
            previews
                .drafted
                .entry(path)
                .or_insert_with_key(|path| cache.file(path, LARGEST, SCALE));
        }
    }
    let finished = panel.pictures().filter(|image| image.is_ready()).count();
    std::mem::replace(&mut panel.previews.finished, finished) != finished
}

/// Downloads each image the listed messages sent that the history does not hold yet.
pub(super) fn fetch(panel: &mut Panel, tx: &mut Tx) {
    if panel.face != super::Face::Recent {
        return;
    }
    let wanted: Vec<(String, String)> = panel
        .history
        .ordered()
        .into_iter()
        .flat_map(images_of)
        .filter(|key| !panel.history.images.contains_key(key))
        .collect();
    for key in wanted {
        panel.history.images.insert(key.clone(), Sent::Loading);
        download(panel, key, 0, Vec::new(), tx);
    }
}

/// Downloads the sent image `key` names again, after it failed.
pub(crate) fn retry(panel: &mut Panel, key: (String, String), tx: &mut Tx) {
    panel.history.images.insert(key.clone(), Sent::Loading);
    download(panel, key, 0, Vec::new(), tx);
}

fn images_of(record: &QuickCaptureRecord) -> impl Iterator<Item = (String, String)> + '_ {
    record
        .attachments
        .iter()
        .filter(|a| a.media_type.starts_with("image/"))
        .map(|a| (record.id.clone(), a.id.clone()))
}

fn download(
    panel: &mut Panel,
    key: (String, String),
    offset: i64,
    mut bytes: Vec<u8>,
    tx: &mut Tx,
) {
    let generation = panel.history.generation;
    let (capture_id, attachment_id) = key.clone();
    panel.ask(
        |request_id| {
            Message::Download(QuickCaptureAttachmentGetMessage {
                request_id: Some(request_id),
                capture_id,
                attachment_id,
                offset,
                ..QuickCaptureAttachmentGetMessage::new()
            })
        },
        move |panel, answer, tx| {
            if generation != panel.history.generation {
                return;
            }
            let chunk = result(answer)
                .map_err(|failure| failure.say())
                .and_then(|r| {
                    r.download
                        .ok_or_else(|| "The daemon did not return file bytes.".to_owned())
                })
                .and_then(|d| {
                    let data = base64::engine::general_purpose::STANDARD
                        .decode(&d.data_base64)
                        .map_err(|e| format!("The daemon returned unreadable file bytes: {e}"))?;
                    Ok((data, d.next_offset, d.eof))
                });
            let sent = match chunk {
                Ok((data, next, eof)) => {
                    bytes.extend(data);
                    if !eof {
                        return download(panel, key, next, bytes, tx);
                    }
                    match &panel.previews.cache {
                        Some(cache) => Sent::Shown(cache.load(bytes)),
                        None => Sent::Failed("Images cannot be shown here.".to_owned()),
                    }
                }
                Err(why) => Sent::Failed(why),
            };
            panel.history.images.insert(key, sent);
        },
        tx,
    );
}
