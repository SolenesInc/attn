//! A document's local images, read through the daemon as the Tauri reader reads them
//! (tile_asset_read), decoded off the window's thread, and opened larger in a zoomable preview.

use std::collections::{HashMap, HashSet};
use std::rc::Rc;

use attn_protocol::{
    Ask, AskImageMessage, AskImageResultMessage, TileAssetReadMessage, TileAssetReadResultMessage,
};
use base64::Engine;
use pulldown_cmark::{Event, Parser, Tag};
use rune::tok::radius;
use rune::{El, Icon, Image, ImageCache, Tx, el};
use rune_kit::{ImageZoom, Preview, image_zoom};

use super::links::{self, Target};
use attn_client::TileKey;

use crate::State;
use crate::requests::Answer;

/// An image file a document shows, and whom the window reads it through.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
struct Source {
    reader: Reader,
    path: String,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
enum Reader {
    /// A tile's document, on this machine or on outpost `host`.
    Tile { host: Option<String> },
    /// An ask, from the copy of its images the home keeps.
    Ask(String),
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

/// The window's pictures: attn's logo, those of the documents its tiles show, and the one
/// open larger.
#[derive(Default)]
pub struct Pictures {
    cache: Option<ImageCache>,
    loads: HashMap<Source, Load>,
    /// Moves each time a picture becomes ready or fails, so views that draw them build again.
    pub(crate) revision: u64,
    /// The tile contents and the shown ask the loads follow.
    followed: Option<u64>,
    /// Each shown document's content as last followed, whose images are read again when it
    /// changes.
    documents: HashMap<TileKey, u64>,
    shown: Option<Shown>,
    logo: Option<Image>,
    zoom: ImageZoom,
    /// The window's size in points, which the open image fits in.
    window: [f32; 2],
}

/// What is open larger over the window.
enum Shown {
    Image { image: Image, alt: String },
    Diagram { source: String },
}

static ZOOM_OUT: Icon = Icon {
    name: "zoom-out",
    view: 20.0,
    body: r#"<rect x="5" y="9.5" width="10" height="1" fill="currentColor"/>"#,
};
static ZOOM_IN: Icon = Icon {
    name: "zoom-in",
    view: 20.0,
    body: r#"<rect x="5" y="9.5" width="10" height="1" fill="currentColor"/><rect x="9.5" y="5" width="1" height="10" fill="currentColor"/>"#,
};

impl Pictures {
    /// Decodes on worker threads that call `wake` when a picture is ready.
    pub(crate) fn start(&mut self, wake: rune::Waker) {
        let cache = ImageCache::new(wake);
        self.logo = Some(cache.load(crate::home::LOGO));
        self.cache = Some(cache);
    }

    /// attn's logo, decoded once when the window starts.
    pub(crate) fn logo(&self) -> Option<&Image> {
        self.logo.as_ref()
    }

    /// Whether a picture is still decoding, which will wake the window when it is done.
    pub fn decoding(&self) -> bool {
        self.loads.values().any(|l| matches!(l, Load::Decoding(..)))
    }

    /// The pictures of a document on `host`, by the paths its images resolve to: the one each
    /// shows, or `None` for an image that could not be read. An image still loading for the
    /// first time is not there.
    pub(crate) fn of(&self, host: Option<&str>) -> Rc<HashMap<String, Option<Image>>> {
        Rc::new(self.read_by(&Reader::Tile {
            host: host.map(str::to_owned),
        }))
    }

    /// The ready pictures of ask `ask`, by the paths its images resolve to.
    pub(crate) fn of_ask(&self, ask: &str) -> Rc<HashMap<String, Image>> {
        Rc::new(
            self.read_by(&Reader::Ask(ask.to_owned()))
                .into_iter()
                .filter_map(|(path, image)| Some((path, image?)))
                .collect(),
        )
    }

    fn read_by(&self, reader: &Reader) -> HashMap<String, Option<Image>> {
        self.loads
            .iter()
            .filter(|(source, _)| source.reader == *reader)
            .filter_map(|(source, load)| match load {
                Load::Failed => Some((source.path.clone(), None)),
                _ => Some((source.path.clone(), Some(load.shown()?.clone()))),
            })
            .collect()
    }
}

/// Where the image files of `ask` resolve from: a document in the folder it was asked from.
pub(crate) fn ask_document(ask: &Ask) -> String {
    format!("{}/{}.md", ask.base_dir, ask.id)
}

/// The local image files `ask`'s markdown and its questions' context show.
fn images_of_ask(ask: &Ask) -> Vec<String> {
    let document = ask_document(ask);
    ask.parts
        .iter()
        .flat_map(|part| {
            part.markdown
                .iter()
                .chain(part.question.iter().filter_map(|q| q.context.as_ref()))
        })
        .flat_map(|source| images_in(&document, source))
        .collect()
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
    let shown_ask = state
        .window
        .shows(crate::shell::Drawer::Asks)
        .then(|| state.window.asks.selected.clone())
        .flatten();
    let followed = hash(&(
        contents.revision,
        std::sync::Arc::as_ptr(&state.facts.asks) as usize,
        &shown_ask,
    ));
    if state.pictures.followed != Some(followed) {
        state.pictures.followed = Some(followed);
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
                    reader: Reader::Tile { host: host.clone() },
                    path,
                };
                if fresh {
                    changed.insert(source.clone());
                }
                wanted.insert(source);
            }
        }
        state.pictures.documents = documents;
        if let Some(ask) = shown_ask.and_then(|id| state.facts.asks.iter().find(|a| a.id == id)) {
            for path in images_of_ask(ask) {
                wanted.insert(Source {
                    reader: Reader::Ask(ask.id.clone()),
                    path,
                });
            }
        }
        state
            .pictures
            .loads
            .retain(|source, _| wanted.contains(source));
        for source in wanted {
            if !state.pictures.loads.contains_key(&source) || changed.contains(&source) {
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
    let path = source.path.clone();
    match source.reader.clone() {
        Reader::Tile { host } => state.ask(
            |id| TileAssetReadMessage {
                host,
                path,
                request_id: Some(id),
                ..TileAssetReadMessage::new()
            },
            move |state, answer: Answer, _| {
                let bytes = answer
                    .outcome()
                    .ok()
                    .and_then(|reply| reply.parse::<TileAssetReadResultMessage>())
                    .and_then(|result| result.data_uri)
                    .and_then(|uri| decoded(&uri));
                arrived(state, source, bytes);
            },
            tx,
        ),
        Reader::Ask(ask_id) => state.ask(
            |id| AskImageMessage {
                ask_id,
                path,
                request_id: id,
                ..AskImageMessage::new()
            },
            move |state, answer: Answer, _| {
                let bytes = answer
                    .outcome()
                    .ok()
                    .and_then(|reply| reply.parse::<AskImageResultMessage>())
                    .filter(|result| result.success)
                    .and_then(|result| result.data_base64)
                    .and_then(|data| base64::engine::general_purpose::STANDARD.decode(data).ok());
                arrived(state, source, bytes);
            },
            tx,
        ),
    }
}

fn arrived(state: &mut State, source: Source, bytes: Option<Vec<u8>>) {
    let Some(Load::Asked(previous)) = state.pictures.loads.get(&source) else {
        return;
    };
    let previous = previous.clone();
    let load = match (bytes, &state.pictures.cache) {
        (Some(bytes), Some(cache)) => Load::Decoding(cache.load(bytes), previous),
        _ => Load::Failed,
    };
    if matches!(load, Load::Failed) {
        state.pictures.revision += 1;
    }
    state.pictures.loads.insert(source, load);
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
    state.pictures.shown = Some(Shown::Image {
        image,
        alt: alt.to_owned(),
    });
    tx.focus(image_zoom::KEY, false);
}

/// Opens diagram `source`, `size` points at its natural size, over the window at 100% from its
/// start, as the Tauri reader's Focus diagram does.
pub(crate) fn show_diagram(state: &mut State, source: &str, size: [f32; 2], tx: &mut Tx) {
    state.pictures.zoom.open_actual(size, state.pictures.window);
    state.pictures.shown = Some(Shown::Diagram {
        source: source.to_owned(),
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
    fn lens(s: &mut State) -> &mut ImageZoom {
        &mut s.pictures.zoom
    }
    let preview = match state.pictures.shown.as_ref()? {
        Shown::Image { image, alt } => {
            let [w, h] = image.size()?;
            let label = if alt.is_empty() {
                "Image preview"
            } else {
                alt.as_str()
            };
            let picture = el()
                .key("image-preview.picture")
                .role("image")
                .label(label)
                .pixels(image.pixels());
            Preview::new(lens, [w as f32, h as f32], picture)
        }
        Shown::Diagram { source } => {
            let drawing = super::natural(source, t)?;
            let drawn = el()
                .key("diagram-preview.drawing")
                .size(drawing.size[0], drawing.size[1])
                .radius(radius::MD)
                .border(1.0, drawing.surface_border)
                .fill(drawing.surface)
                .child(drawing.el);
            Preview::new(lens, drawing.size, drawn)
                .scale_content()
                .actual_size("Actual size")
                .escape_returns()
                .format_level(|_| "Fit".to_owned())
        }
    };
    let preview = preview
        .icons(&ZOOM_OUT, &ZOOM_IN)
        .on_close(|s: &mut State, _| s.pictures.shown = None)
        .build(
            &state.pictures.zoom,
            attn_ui::kit(t),
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
