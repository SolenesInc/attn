//! The pictures of the images the panel shows: each image file of the draft, read from the
//! draft's folder, and each image a listed Recent message sent, read from the daemon a chunk at
//! a time as the Tauri panel reads it.

use std::collections::HashMap;
use std::path::PathBuf;

use attn_protocol::{QuickCaptureAttachmentGetMessage, QuickCaptureRecord};
use base64::Engine;
use rune::{Image, ImageCache, Tx};

use super::{Message, Panel, result};

/// The largest an image draws in the panel, in points at the largest text size: each picture
/// holds only the pixels that fill a square this size, however large the image.
const LARGEST: f32 = 132.0 * crate::settings::LARGEST_TEXT_SCALE;

#[derive(Default)]
pub(crate) struct Previews {
    cache: Option<ImageCache>,
    /// The draft's image files by where they are stored.
    drafted: HashMap<PathBuf, Picture>,
}

/// A sent image's picture, while it downloads, once it does, or why it could not.
pub(crate) enum Sent {
    Loading,
    Downloaded(Picture),
    Failed(String),
}

/// An image as the panel's own frames have seen it: a decode another thread finished counts
/// once the panel's poll sees it, so what the panel draws and says changes on its own thread.
pub(crate) enum Picture {
    Decoding(Image),
    Ready(Image),
    Broken,
}

impl Picture {
    /// Whether the decode finished since the panel last looked.
    fn see(&mut self) -> bool {
        let Picture::Decoding(image) = self else {
            return false;
        };
        if !image.is_ready() {
            return false;
        }
        *self = match image.error() {
            None => Picture::Ready(image.clone()),
            Some(_) => Picture::Broken,
        };
        true
    }
}

impl Previews {
    pub(super) fn start(&mut self, wake: rune::Waker) {
        self.cache = Some(ImageCache::new(wake));
    }

    pub(crate) fn drafted(&self, path: &std::path::Path) -> Option<&Picture> {
        self.drafted.get(path)
    }
}

impl Panel {
    /// Whether a picture is still decoding, which will wake the panel when it is done.
    pub fn decoding(&self) -> bool {
        let sent = self.history.images.values().filter_map(|s| match s {
            Sent::Downloaded(picture) => Some(picture),
            _ => None,
        });
        self.previews
            .drafted
            .values()
            .chain(sent)
            .any(|picture| matches!(picture, Picture::Decoding(_)))
    }
}

/// Reads each image file the draft holds while the panel shows, and forgets those it no longer
/// holds or shows. Returns whether a picture finished decoding since the panel last looked.
pub(super) fn follow(panel: &mut Panel) -> bool {
    let wanted: Vec<PathBuf> = panel
        .draft
        .files
        .iter()
        .filter(|_| panel.shown)
        .filter_map(|f| f.image().map(PathBuf::from))
        .collect();
    let previews = &mut panel.previews;
    previews.drafted.retain(|path, _| wanted.contains(path));
    if let Some(cache) = &previews.cache {
        for path in wanted {
            previews.drafted.entry(path).or_insert_with_key(|path| {
                Picture::Decoding(cache.file(path, LARGEST, crate::PICTURE_SCALE))
            });
        }
    }
    let sent = panel.history.images.values_mut().filter_map(|s| match s {
        Sent::Downloaded(picture) => Some(picture),
        _ => None,
    });
    previews
        .drafted
        .values_mut()
        .chain(sent)
        .fold(false, |seen, picture| picture.see() | seen)
}

/// Downloads each image the listed messages sent that the history does not hold yet.
pub(super) fn fetch(panel: &mut Panel, tx: &mut Tx) {
    if panel.face != super::Face::Recent {
        return;
    }
    let wanted: Vec<((String, String), String)> = panel
        .history
        .ordered()
        .into_iter()
        .flat_map(images_of)
        .filter(|(key, _)| !panel.history.images.contains_key(key))
        .collect();
    for (key, media_type) in wanted {
        panel.history.images.insert(key.clone(), Sent::Loading);
        download(panel, key, media_type, 0, Vec::new(), tx);
    }
}

/// Downloads the sent image `key` names, of `media_type`, again after it failed.
pub(crate) fn retry(panel: &mut Panel, key: (String, String), media_type: String, tx: &mut Tx) {
    panel.history.images.insert(key.clone(), Sent::Loading);
    download(panel, key, media_type, 0, Vec::new(), tx);
}

fn images_of(record: &QuickCaptureRecord) -> impl Iterator<Item = ((String, String), String)> + '_ {
    record
        .attachments
        .iter()
        .filter(|a| a.media_type.starts_with("image/"))
        .map(|a| ((record.id.clone(), a.id.clone()), a.media_type.clone()))
}

fn download(
    panel: &mut Panel,
    key: (String, String),
    media_type: String,
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
                        return download(panel, key, media_type, next, bytes, tx);
                    }
                    match &panel.previews.cache {
                        Some(cache) => Sent::Downloaded(Picture::Decoding(cache.load_mime(
                            bytes,
                            &media_type,
                            LARGEST,
                            crate::PICTURE_SCALE,
                        ))),
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
