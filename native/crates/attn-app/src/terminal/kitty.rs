use std::collections::{HashMap, HashSet, VecDeque};
use std::sync::Arc;

use attn_client::KittyFormat;
use attn_protocol::{GetKittyImageMessage, KittyImageResultMessage, KittyPlacement};
use attn_term::Image;
use attn_term::kitty::{Described, Format, Picture, strip};

use super::{Term, Terminals};

/// The decoded pixels kept across terminals before the least recently drawn that no placement
/// shows go. Measured
/// 2026-08-03 with kitten icat, timg and chafa: the largest stored image held 6.48 MB of
/// pixels, typical ones 1.9 to 6 MB, so 64 MB is about ten of the largest, past what a screen
/// shows at once.
const CACHE_BYTES: usize = 64 * 1024 * 1024;

/// A terminal, an image id in it, and that image's generation.
type Key = (String, u32, u64);

enum Entry {
    Present(Arc<Picture>),
    /// The daemon could not give the image: evicted, deleted, or never sent.
    Failed,
}

/// The images the terminals' placements show, pulled from the daemon on a miss and kept,
/// least recently drawn first, up to `CACHE_BYTES` of images no placement shows.
#[derive(Default)]
pub(super) struct Images {
    entries: HashMap<Key, Entry>,
    order: VecDeque<Key>,
    bytes: usize,
    /// The generation asked for, per terminal and image id: the daemon answers by image id.
    asked: HashMap<(String, u32), u64>,
    next_picture: u64,
    /// The placed images alone are past `CACHE_BYTES`.
    over: bool,
}

impl Images {
    fn insert(&mut self, key: Key, entry: Entry) {
        self.remove(&key);
        if let Entry::Present(picture) = &entry {
            self.bytes += picture.rgba.len();
        }
        self.order.push_back(key.clone());
        self.entries.insert(key, entry);
    }

    /// Lets the least recently drawn images no placement shows go until the rest fit
    /// `CACHE_BYTES`, and says once, as they first stop fitting, when the placed ones alone
    /// do not.
    fn trim(&mut self, placed: &HashSet<Key>) -> Option<String> {
        let unplaced: Vec<Key> = self
            .order
            .iter()
            .filter(|key| !placed.contains(*key))
            .cloned()
            .collect();
        for key in unplaced {
            if self.bytes <= CACHE_BYTES {
                break;
            }
            self.remove(&key);
        }
        let over = self.bytes > CACHE_BYTES;
        let say = over && !self.over;
        self.over = over;
        say.then(|| {
            format!(
                "The terminals' images hold {} MB of pixels, past the {} MB budget; they stay drawn",
                self.bytes.div_ceil(1024 * 1024),
                CACHE_BYTES / (1024 * 1024)
            )
        })
    }

    fn remove(&mut self, key: &Key) {
        if let Some(entry) = self.entries.remove(key) {
            self.order.retain(|k| k != key);
            if let Entry::Present(picture) = entry {
                self.bytes -= picture.rgba.len();
            }
        }
    }

    fn touch(&mut self, key: &Key) {
        if let Some(at) = self.order.iter().position(|k| k == key) {
            let key = self.order.remove(at).expect("the key is in the order");
            self.order.push_back(key);
        }
    }

    /// Forgets the images terminal `id` asked for and was not answered, as it attaches: an ask
    /// made on a connection that dropped is never answered.
    pub(super) fn unanswered(&mut self, id: &str) {
        self.asked.retain(|(terminal, _), _| terminal != id);
    }

    /// Drops what terminal `id` kept, as its pane goes.
    pub(super) fn forget(&mut self, id: &str) {
        let keys: Vec<Key> = self
            .entries
            .keys()
            .filter(|(terminal, ..)| terminal == id)
            .cloned()
            .collect();
        for key in keys {
            self.remove(&key);
        }
        self.asked.retain(|(terminal, _), _| terminal != id);
    }
}

impl Terminals {
    /// The daemon described terminal `id`'s whole set of placements at `seq`.
    pub(super) fn placements_described(
        &mut self,
        id: &str,
        seq: i64,
        placements: &[KittyPlacement],
    ) {
        let Some(term) = self.panes.get_mut(id) else {
            return;
        };
        let history = term.vt.history();
        let seq = u32::try_from(seq).unwrap_or(0);
        if term.placements.apply(seq, &described(placements), history) {
            self.undrawn = true;
            self.ask_images(id);
            self.trim_images();
        }
    }

    /// A restored screen carries the placements on it as the whole truth.
    pub(super) fn seed_placements(term: &mut Term, placements: &[KittyPlacement]) {
        let history = term.vt.history();
        term.placements.seed(&described(placements), history);
    }

    /// Asks the daemon for each image terminal `id`'s placements show that is neither kept nor
    /// asked for already.
    pub(super) fn ask_images(&mut self, id: &str) {
        let Some(term) = self.panes.get(id) else {
            return;
        };
        let daemon = self.links.to(term.via);
        for (image, generation) in term.placements.images() {
            let key = (id.to_owned(), image, generation);
            let asked = (id.to_owned(), image);
            if self.images.entries.contains_key(&key)
                || self.images.asked.get(&asked) == Some(&generation)
            {
                continue;
            }
            self.images.asked.insert(asked, generation);
            let _ = daemon.send(&GetKittyImageMessage {
                id: id.into(),
                image_id: i64::from(image),
                ..GetKittyImageMessage::new()
            });
        }
    }

    /// The daemon sent an image's pixels.
    pub(super) fn image_arrived(
        &mut self,
        id: &str,
        image: u32,
        generation: u64,
        size: (u32, u32),
        format: KittyFormat,
        pixels: &[u8],
    ) {
        self.images.asked.remove(&(id.to_owned(), image));
        let format = match format {
            KittyFormat::Rgb => Format::Rgb,
            KittyFormat::Rgba => Format::Rgba,
            KittyFormat::GrayAlpha => Format::GrayAlpha,
            KittyFormat::Gray => Format::Gray,
        };
        self.images.next_picture += 1;
        let picture = Picture::new(self.images.next_picture, size.0, size.1, format, pixels);
        let entry = picture.map_or(Entry::Failed, |p| Entry::Present(Arc::new(p)));
        self.images
            .insert((id.to_owned(), image, generation), entry);
        self.trim_images();
        self.undrawn = true;
    }

    /// The daemon answered an image request without pixels: it could not give the image.
    pub(super) fn image_refused(&mut self, result: &KittyImageResultMessage) {
        if result.success {
            return;
        }
        let image = u32::try_from(result.image_id).unwrap_or(0);
        let asked = self.images.asked.remove(&(result.id.clone(), image));
        let generation = result
            .generation
            .and_then(|g| u64::try_from(g).ok())
            .or(asked);
        if let Some(generation) = generation {
            self.images
                .insert((result.id.clone(), image, generation), Entry::Failed);
        }
    }

    fn trim_images(&mut self) {
        let placed: HashSet<Key> = self
            .panes
            .iter()
            .flat_map(|(id, term)| {
                term.placements
                    .images()
                    .map(|(image, generation)| (id.clone(), image, generation))
            })
            .collect();
        if let Some(said) = self.images.trim(&placed) {
            self.said = Some(said);
        }
    }

    /// The images terminal `id` draws, its view's top row at screen row `top`, in `cell`-sized
    /// cells of device pixels: each placement whose pixels are kept, and the strip of a virtual
    /// placement each run of placeholder cells in view shows.
    pub(super) fn kitty_images(
        images: &mut Images,
        id: &str,
        term: &Term,
        top: u32,
        cell: (u32, u32),
    ) -> Vec<Image> {
        let mut drawn = Vec::new();
        let mut picture = |image: u32, generation: u64| {
            let key = (id.to_owned(), image, generation);
            let Some(Entry::Present(picture)) = images.entries.get(&key) else {
                return None;
            };
            let picture = picture.clone();
            images.touch(&key);
            Some(picture)
        };
        for placed in term.placements.placed() {
            if let Some(picture) = picture(placed.image_id, placed.generation) {
                drawn.push(Image {
                    picture,
                    placed: *placed,
                });
            }
        }
        for (row, run) in term.grid.placeholders() {
            let Some(target) = term.placements.target(run) else {
                continue;
            };
            let Some(picture) = picture(target.image_id, target.generation) else {
                continue;
            };
            let size = (picture.width, picture.height);
            if let Some(placed) = strip(run, target, size, cell, top + u32::from(row)) {
                drawn.push(Image { picture, placed });
            }
        }
        drawn
    }
}

fn described(placements: &[KittyPlacement]) -> Vec<Described> {
    let int = |n: i64| i32::try_from(n).unwrap_or(0);
    placements
        .iter()
        .map(|p| Described {
            image_id: u32::try_from(p.image_id).unwrap_or(0),
            placement_id: u32::try_from(p.placement_id).unwrap_or(0),
            generation: u64::try_from(p.image_generation).unwrap_or(0),
            virtual_: p.r#virtual,
            grid_cols: int(p.grid_cols),
            grid_rows: int(p.grid_rows),
            z: int(p.z),
            screen_row: int(p.viewport_row),
            col: int(p.viewport_col),
            pixel_width: int(p.pixel_width),
            pixel_height: int(p.pixel_height),
            source: [
                int(p.source_x),
                int(p.source_y),
                int(p.source_width),
                int(p.source_height),
            ],
        })
        .collect()
}
