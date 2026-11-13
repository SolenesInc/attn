//! A document's local images, read through the daemon as the Tauri reader reads them
//! (tile_asset_read), decoded off the window's thread, and opened larger in a zoomable preview.

use std::collections::{HashMap, HashSet};
use std::rc::Rc;

use attn_protocol::{TileAssetReadMessage, TileAssetReadResultMessage};
use base64::Engine;
use pulldown_cmark::{Event, Parser, Tag};
use rune::{El, Image, ImageCache, Tx, el};
use rune_kit::{ImageZoom, Preview, image_zoom};

use super::links::{self, Target};
use attn_client::TileKey;

use crate::State;
use crate::requests::Answer;

/// An image file a document shows: its path on this machine, or on outpost `host`.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
struct Source {
    host: Option<String>,
    path: String,
}

/// A picture's read, with the picture read before it, shown until this one is ready.
enum Load {
    Asked(Option<Image>),
    Decoding(Image, Option<Image>),
    Ready(Image),
    Failed,
}

impl Load {
    fn shown(&self) -> Option<&Image> {
        match self {
            Load::Ready(image) | Load::Asked(Some(image)) | Load::Decoding(_, Some(image)) => {
                Some(image)
            }
            _ => None,
        }
    }
}

/// The window's pictures for the documents its tiles show, and the one open larger.
#[derive(Default)]
pub struct Pictures {
    cache: Option<ImageCache>,
    loads: HashMap<Source, Load>,
    /// Moves each time a picture becomes ready or fails, so views that draw them build again.
    pub(crate) revision: u64,
    /// The tile contents revision the loads follow.
    followed: Option<u64>,
    /// Each shown document's content as last followed, whose images are read again when it
    /// changes.
    documents: HashMap<TileKey, u64>,
    shown: Option<Shown>,
    zoom: ImageZoom,
    /// The window's size in points, which the open image fits in.
    window: [f32; 2],
}

struct Shown {
    image: Image,
    alt: String,
}

impl Pictures {
    /// Decodes on worker threads that call `wake` when a picture is ready.
    pub(crate) fn start(&mut self, wake: rune::Waker) {
        self.cache = Some(ImageCache::new(wake));
    }

    /// Whether a picture is still decoding, which will wake the window when it is done.
    pub fn decoding(&self) -> bool {
        self.loads.values().any(|l| matches!(l, Load::Decoding(..)))
    }

    /// The ready pictures of a document on `host`, by the paths its images resolve to.
    pub(crate) fn of(&self, host: Option<&str>) -> Rc<HashMap<String, Image>> {
        Rc::new(
            self.loads
                .iter()
                .filter(|(source, _)| source.host.as_deref() == host)
                .filter_map(|(source, load)| Some((source.path.clone(), load.shown()?.clone())))
                .collect(),
        )
    }
}

/// The local image files `source`, a document at `document`, shows.
fn images_in(document: &str, source: &str) -> Vec<String> {
    Parser::new_ext(source, super::anchors::OPTIONS)
        .filter_map(|event| match event {
            Event::Start(Tag::Image { dest_url, .. }) => {
                match links::resolve(document, &dest_url) {
                    Some(Target::Local(path)) if links::is_image(&path) => Some(path),
                    _ => None,
                }
            }
            _ => None,
        })
        .collect()
}

/// Asks the daemon for each image the shown documents hold that the window has not read, and
/// forgets the pictures no document shows any more. Returns whether a picture became ready.
pub(crate) fn follow(state: &mut State, now: f64, tx: &mut Tx) -> bool {
    let contents = state.facts.tile_contents.clone();
    if state.pictures.followed != Some(contents.revision) {
        state.pictures.followed = Some(contents.revision);
        let mut wanted = HashSet::new();
        let mut changed = HashSet::new();
        let mut documents = HashMap::new();
        for (key, c) in &contents.tiles {
            let c = &c.message;
            if c.error.as_deref().is_some_and(|e| !e.is_empty()) {
                continue;
            }
            let host = c.host.clone().filter(|h| !h.is_empty());
            let read = hash(&(&c.path, &host, &c.content));
            let fresh = state.pictures.documents.get(key) != Some(&read);
            documents.insert(key.clone(), read);
            for path in images_in(&c.path, &c.content) {
                let source = Source {
                    host: host.clone(),
                    path,
                };
                if fresh {
                    changed.insert(source.clone());
                }
                wanted.insert(source);
            }
        }
        state.pictures.documents = documents;
        state
            .pictures
            .loads
            .retain(|source, _| wanted.contains(source));
        for source in wanted {
            let unread = matches!(state.pictures.loads.get(&source), None | Some(Load::Failed));
            if unread || changed.contains(&source) {
                read(state, source, tx);
            }
        }
    }
    let mut changed = false;
    for load in state.pictures.loads.values_mut() {
        if let Load::Decoding(image, _) = load
            && image.is_ready()
        {
            *load = match image.error() {
                None => Load::Ready(image.clone()),
                Some(_) => Load::Failed,
            };
            changed = true;
        }
    }
    if changed {
        state.pictures.revision += 1;
    }
    changed | state.pictures.zoom.poll(now, tx)
}

fn read(state: &mut State, source: Source, tx: &mut Tx) {
    let before = state.pictures.loads.remove(&source);
    let previous = before.as_ref().and_then(Load::shown).cloned();
    state
        .pictures
        .loads
        .insert(source.clone(), Load::Asked(previous));
    let asked = source.clone();
    state.ask(
        |id| TileAssetReadMessage {
            host: asked.host,
            path: asked.path,
            request_id: Some(id),
            ..TileAssetReadMessage::new()
        },
        move |state, answer: Answer, _| {
            let Some(Load::Asked(previous)) = state.pictures.loads.get(&source) else {
                return;
            };
            let previous = previous.clone();
            let bytes = answer
                .outcome()
                .ok()
                .and_then(|reply| reply.parse::<TileAssetReadResultMessage>())
                .and_then(|result| result.data_uri)
                .and_then(|uri| decoded(&uri));
            let load = match (bytes, &state.pictures.cache) {
                (Some(bytes), Some(cache)) => Load::Decoding(cache.load(bytes), previous),
                _ => Load::Failed,
            };
            if matches!(load, Load::Failed) {
                state.pictures.revision += 1;
            }
            state.pictures.loads.insert(source, load);
        },
        tx,
    );
}

fn hash(value: &impl std::hash::Hash) -> u64 {
    use std::hash::{BuildHasher, BuildHasherDefault, DefaultHasher};
    BuildHasherDefault::<DefaultHasher>::default().hash_one(value)
}

fn decoded(data_uri: &str) -> Option<Vec<u8>> {
    let (_, data) = data_uri.split_once(";base64,")?;
    base64::engine::general_purpose::STANDARD.decode(data).ok()
}

/// Opens `image` larger, over the window.
pub(crate) fn show(state: &mut State, image: Image, alt: &str, tx: &mut Tx) {
    state.pictures.zoom.reset();
    state.pictures.shown = Some(Shown {
        image,
        alt: alt.to_owned(),
    });
    tx.focus(image_zoom::KEY, false);
}

/// The window's `root` with the open image over it, which it measures for the preview.
pub(crate) fn over(root: El, state: &State, reduced_motion: bool, t: &attn_ui::Tokens) -> El {
    root.on_layout_tx(|state: &mut State, [_, _, w, h], tx: &mut Tx| {
        if state.pictures.window == [w, h] {
            tx.live_only();
            return;
        }
        state.pictures.window = [w, h];
    })
    .children(preview(state, reduced_motion, t))
}

fn preview(state: &State, reduced_motion: bool, t: &attn_ui::Tokens) -> Option<El> {
    let shown = state.pictures.shown.as_ref()?;
    let [w, h] = shown.image.size()?;
    let label = if shown.alt.is_empty() {
        "Image preview"
    } else {
        shown.alt.as_str()
    };
    let picture = el()
        .key("image-preview.picture")
        .role("image")
        .label(label)
        .pixels(shown.image.pixels());
    let preview = Preview::new(
        |s: &mut State| &mut s.pictures.zoom,
        [w as f32, h as f32],
        picture,
    )
    .on_close(|s: &mut State, _| s.pictures.shown = None)
    .build(
        &state.pictures.zoom,
        &attn_ui::kit(t),
        state.pictures.window,
        reduced_motion,
    );
    Some(
        el().key("image-preview.scrim")
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .fill(t.bg_overlay)
            .child(preview),
    )
}
