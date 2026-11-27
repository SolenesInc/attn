//! A document's local images, read through the daemon (tile_asset_read), decoded off the
//! window's thread, and opened larger in a zoomable preview.

use std::collections::{HashMap, HashSet};
use std::rc::Rc;

use attn_protocol::{
    Ask, AskImageMessage, AskImageResultMessage, SeedArtifactTargetMessage,
    SeedArtifactTargetResultMessage, TileAssetReadMessage, TileAssetReadResultMessage,
};
use base64::Engine;
use pulldown_cmark::{Event, Parser, Tag};
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
    /// A seed's body, from the seed's own files.
    Seed(String),
    /// A note in the notebook or an editor tile's folder: `root` is the folder, `None` the
    /// notebook, and the path is from that folder.
    Notebook { root: Option<String> },
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
    /// Each read seed's files as last followed, whose images are read again when they change.
    seed_files: HashMap<String, u64>,
    /// How many times files changed under each notebook folder whose images it read, so a change
    /// there reads them again.
    notebook_changes: HashMap<Option<String>, u64>,
    shown: Option<Shown>,
    logo: Option<Image>,
    zoom: ImageZoom,
    /// The window's size in points, which the open image and the asks sheet fit in.
    pub(crate) window: [f32; 2],
}

/// What is open larger over the window.
enum Shown {
    /// A document's picture, and the read of its file whole when the document holds it smaller,
    /// shown once it is ready so actual size is sharp.
    Image {
        image: Image,
        alt: String,
        whole: Option<Box<(Source, Load)>>,
    },
    Diagram {
        source: String,
    },
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

    /// A picture of the PNG `png`, decoded for a tile at most `side` points across, as an answer
    /// file pasted into an ask shows; none before the window starts decoding.
    pub(crate) fn of_png(&self, png: std::sync::Arc<[u8]>, side: f32) -> Option<Image> {
        let cache = self.cache.as_ref()?;
        Some(cache.load_mime(png, "image/png", side, crate::PICTURE_SCALE))
    }

    /// Whether a picture is still decoding, which will wake the window when it is done.
    pub fn decoding(&self) -> bool {
        self.loads
            .values()
            .chain(self.whole())
            .any(|l| matches!(l, Load::Decoding(..)))
    }

    fn whole(&self) -> Option<&Load> {
        match &self.shown {
            Some(Shown::Image {
                whole: Some(whole), ..
            }) => Some(&whole.1),
            _ => None,
        }
    }

    /// The pictures of a document on `host`, by the paths its images resolve to: the one each
    /// shows, or `None` for an image that could not be read. An image still loading for the
    /// first time is not there.
    pub(crate) fn of(&self, host: Option<&str>) -> Rc<HashMap<String, Option<Image>>> {
        Rc::new(self.read_by(&Reader::Tile {
            host: host.map(str::to_owned),
        }))
    }

    /// The pictures of ask `ask`, by the paths its images resolve to, as `of` gives a tile's.
    pub(crate) fn of_ask(&self, ask: &str) -> Rc<HashMap<String, Option<Image>>> {
        Rc::new(self.read_by(&Reader::Ask(ask.to_owned())))
    }

    /// The pictures seed `seed`'s body shows, by the file names it gives them.
    pub(crate) fn of_seed(&self, seed: &str) -> Rc<HashMap<String, Option<Image>>> {
        Rc::new(self.read_by(&Reader::Seed(seed.to_owned())))
    }

    /// The pictures notes under `root` show, by their paths from it; `None` is the notebook.
    pub(crate) fn of_notebook(&self, root: Option<&str>) -> HashMap<String, Option<Image>> {
        self.read_by(&Reader::Notebook {
            root: root.map(str::to_owned),
        })
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

/// The files of its seed that a seed's `body` shows as images.
fn images_of_seed(body: &str) -> Vec<String> {
    Parser::new_ext(body, super::anchors::OPTIONS)
        .filter_map(|event| match event {
            Event::Start(Tag::Image { dest_url, .. }) => links::seed_file(&dest_url),
            _ => None,
        })
        .filter(|file| links::is_image(file))
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
    let shown_ask = crate::asks::is_open(state)
        .then(|| state.window.asks.selected.clone())
        .flatten();
    let notebook = crate::notebook::images(state);
    let seeds = crate::garden::read_bodies(state);
    let panel = crate::asks::panel::shown_markdown(state);
    let followed = hash(&(
        &panel,
        &seeds,
        contents.revision,
        state.readings.reviews.widenings(),
        std::sync::Arc::as_ptr(&state.facts.asks) as usize,
        &shown_ask,
        &notebook,
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
            let stops = c.review.as_ref().map_or_else(Vec::new, |bundle| {
                crate::review::documents(&state.readings.reviews, &key.tile, bundle)
            });
            let read = hash(&(&c.path, &host, &c.content, &stops));
            let fresh = state.pictures.documents.get(key) != Some(&read);
            documents.insert(key.clone(), read);
            let source = c
                .view
                .as_ref()
                .map_or_else(|| c.content.clone(), crate::view::shown_markdown);
            let shown = std::iter::once((c.path.as_str(), source.as_str())).chain(
                stops
                    .iter()
                    .map(|(path, text)| (path.as_str(), text.as_str())),
            );
            for path in shown.flat_map(|(document, source)| images_in(document, source)) {
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
        if let Some((host, document, source)) = &panel {
            for path in images_in(document, source) {
                wanted.insert(Source {
                    reader: Reader::Tile { host: host.clone() },
                    path,
                });
            }
        }
        if let Some(ask) = shown_ask.and_then(|id| state.facts.asks.iter().find(|a| a.id == id)) {
            for path in images_of_ask(ask) {
                wanted.insert(Source {
                    reader: Reader::Ask(ask.id.clone()),
                    path,
                });
            }
        }
        let mut seed_files = HashMap::new();
        for read in &seeds {
            let files = hash(&read.files);
            let fresh = state.pictures.seed_files.get(&read.seed) != Some(&files);
            seed_files.insert(read.seed.clone(), files);
            for path in images_of_seed(&read.body) {
                let source = Source {
                    reader: Reader::Seed(read.seed.clone()),
                    path,
                };
                if fresh {
                    changed.insert(source.clone());
                }
                wanted.insert(source);
            }
        }
        state.pictures.seed_files = seed_files;
        let mut seen = HashMap::new();
        for (root, path, changes) in notebook {
            let source = Source {
                reader: Reader::Notebook { root: root.clone() },
                path,
            };
            if state
                .pictures
                .notebook_changes
                .get(&root)
                .is_some_and(|c| *c != changes)
            {
                changed.insert(source.clone());
            }
            seen.insert(root, changes);
            wanted.insert(source);
        }
        state.pictures.notebook_changes = seen;
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
    let widths = crate::notebook::image_widths(state);
    let tiles = state.readings.widest();
    let zoom = crate::settings::text_scale(&state.facts.settings);
    let blurred: Vec<Source> = state
        .pictures
        .loads
        .iter()
        .filter(|(source, load)| match load {
            Load::Ready(image) => holds_less(image, widest(source, &widths, tiles, zoom)),
            _ => false,
        })
        .map(|(source, _)| source.clone())
        .collect();
    for source in blurred {
        read(state, source, tx);
    }
    let pictures = &mut state.pictures;
    let whole = match &mut pictures.shown {
        Some(Shown::Image {
            whole: Some(whole), ..
        }) => Some(&mut whole.1),
        _ => None,
    };
    let mut changed = false;
    for load in pictures.loads.values_mut().chain(whole) {
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

/// The widest `source` draws, in the window's points at text size `zoom`: a note's column, a
/// tile's widest document view, as a picture takes the column up to its own size, or the
/// reader's prose measure.
fn widest(
    source: &Source,
    notes: &HashMap<Option<String>, f32>,
    tiles: Option<f32>,
    zoom: f32,
) -> f32 {
    let column = match &source.reader {
        Reader::Notebook { root } => notes.get(root).copied(),
        Reader::Tile { .. } | Reader::Seed(_) => tiles,
        Reader::Ask(_) => None,
    };
    column.unwrap_or(super::PROSE_MEASURE) * zoom
}

/// Whether `image` holds a smaller picture than drawing it `width` points across shows, while
/// its file has a larger one.
fn holds_less(image: &Image, width: f32) -> bool {
    let need = (width * crate::PICTURE_SCALE).ceil();
    match (image.pixels().sharpest(), image.size()) {
        (Some((held, _)), Some([own, _])) => held < own && (held as f32) < need,
        _ => false,
    }
}

fn read(state: &mut State, source: Source, tx: &mut Tx) {
    let before = state.pictures.loads.remove(&source);
    let previous = before.as_ref().and_then(Load::shown).cloned();
    state
        .pictures
        .loads
        .insert(source.clone(), Load::Asked(previous));
    fetch(state, source, arrived, tx);
}

/// Reads `source`'s file through the daemon, and hands `then` its bytes, or `None` when it could
/// not be read.
fn fetch(
    state: &mut State,
    source: Source,
    then: fn(&mut State, Source, Option<Vec<u8>>),
    tx: &mut Tx,
) {
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
                then(state, source, bytes);
            },
            tx,
        ),
        Reader::Notebook { root } => state.ask(
            |id| attn_protocol::FsReadAssetMessage {
                path,
                root,
                request_id: Some(id),
                ..attn_protocol::FsReadAssetMessage::new()
            },
            move |state, answer: Answer, _| {
                let bytes = answer
                    .outcome()
                    .ok()
                    .and_then(|reply| reply.parse::<attn_protocol::FsReadAssetResultMessage>())
                    .and_then(|result| result.result)
                    .and_then(|r| {
                        base64::engine::general_purpose::STANDARD
                            .decode(r.data_base64)
                            .ok()
                    });
                then(state, source, bytes);
            },
            tx,
        ),
        Reader::Seed(seed_id) => state.ask(
            |id| SeedArtifactTargetMessage {
                seed_id,
                relative_target: path,
                purpose: "image".into(),
                request_id: id,
                ..SeedArtifactTargetMessage::new()
            },
            move |state, answer: Answer, _| {
                let bytes = answer
                    .outcome()
                    .ok()
                    .and_then(|reply| reply.parse::<SeedArtifactTargetResultMessage>())
                    .filter(|result| result.success)
                    .and_then(|result| result.result?.data_base64)
                    .and_then(|data| base64::engine::general_purpose::STANDARD.decode(data).ok());
                then(state, source, bytes);
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
                then(state, source, bytes);
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
    let zoom = crate::settings::text_scale(&state.facts.settings);
    let width = widest(
        &source,
        &crate::notebook::image_widths(state),
        state.readings.widest(),
        zoom,
    );
    let load = match (bytes, &state.pictures.cache) {
        (Some(bytes), Some(cache)) => Load::Decoding(
            cache.load_across(bytes, width, crate::PICTURE_SCALE),
            previous,
        ),
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

/// Opens `image` larger, over the window, reading its file again to decode it whole when the
/// document holds it smaller.
pub(crate) fn show(state: &mut State, image: Image, alt: &str, tx: &mut Tx) {
    state.pictures.zoom.reset();
    let smaller = holds_less(&image, f32::INFINITY);
    let source = state
        .pictures
        .loads
        .iter()
        .find(|(_, load)| load.shown().is_some_and(|i| i.id() == image.id()))
        .map(|(source, _)| source.clone())
        .filter(|_| smaller);
    state.pictures.shown = Some(Shown::Image {
        image,
        alt: alt.to_owned(),
        whole: source.clone().map(|s| Box::new((s, Load::Asked(None)))),
    });
    if let Some(source) = source {
        fetch(state, source, opened, tx);
    }
    tx.focus(image_zoom::KEY, false);
}

fn opened(state: &mut State, source: Source, bytes: Option<Vec<u8>>) {
    let pictures = &mut state.pictures;
    let Some(Shown::Image {
        whole: Some(whole), ..
    }) = &mut pictures.shown
    else {
        return;
    };
    let (shown, load) = &mut **whole;
    if *shown != source || !matches!(load, Load::Asked(_)) {
        return;
    }
    *load = match (bytes, &pictures.cache) {
        (Some(bytes), Some(cache)) => Load::Decoding(cache.load(bytes), None),
        _ => Load::Failed,
    };
}

/// Opens diagram `source`, `size` points at its natural size, over the window at 100% from its
/// start.
pub(crate) fn show_diagram(state: &mut State, source: &str, size: [f32; 2], tx: &mut Tx) {
    state.pictures.zoom.open_actual(size, state.pictures.window);
    state.pictures.shown = Some(Shown::Diagram {
        source: source.to_owned(),
    });
    tx.focus(image_zoom::KEY, false);
}

/// The window's `root` with its `layers` and the open image, which it measures for the
/// preview. The image lies under the layers, so one opened after it, such as the file opener,
/// lies over it; while the asks sheet is open, the image opened from it and lies over it.
pub(crate) fn over(
    root: El,
    layers: Vec<El>,
    state: &State,
    reduced_motion: bool,
    t: &attn_ui::Tokens,
) -> El {
    let root = root.on_layout_tx(|state: &mut State, [_, _, w, h], tx: &mut Tx| {
        if state.pictures.window == [w, h] {
            tx.live_only();
            return;
        }
        state.pictures.window = [w, h];
    });
    let preview = preview(state, reduced_motion, t);
    if crate::asks::is_open(state) {
        root.children(layers).children(preview)
    } else {
        root.children(preview).children(layers)
    }
}

fn preview(state: &State, reduced_motion: bool, t: &attn_ui::Tokens) -> Option<El> {
    fn lens(s: &mut State) -> &mut ImageZoom {
        &mut s.pictures.zoom
    }
    let preview = match state.pictures.shown.as_ref()? {
        Shown::Image { image, alt, whole } => {
            let image = match whole.as_deref() {
                Some((_, Load::Ready(whole))) => whole,
                _ => image,
            };
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
            let drawn = attn_ui::blocks::Surface::ground(
                el().key("diagram-preview.drawing")
                    .size(drawing.size[0], drawing.size[1])
                    .child(drawing.el),
                drawing.surface,
                drawing.surface_border,
            );
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
