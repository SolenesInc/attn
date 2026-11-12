//! Kitty images as the daemon describes them, kept and placed as the Tauri terminal keeps and
//! places them (`kittyPlacements.ts`). The daemon's worker is the only kitty parser: it sends
//! the whole set of placements whenever one moves, and each set replaces the last.

/// One placement as the daemon describes it, its row relative to the top of the screen.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Described {
    pub image_id: u32,
    pub placement_id: u32,
    pub generation: u64,
    /// Drawn by the program in its own cells, with no position of its own.
    pub virtual_: bool,
    pub z: i32,
    pub screen_row: i32,
    pub col: i32,
    pub pixel_width: i32,
    pub pixel_height: i32,
    pub source: [i32; 4],
}

/// One image placed on the terminal, at a screen row counted from the oldest history row.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub struct Placed {
    pub image_id: u32,
    pub placement_id: u32,
    pub generation: u64,
    pub z: i32,
    pub row: u32,
    pub col: i32,
    /// The size it draws at, in device pixels.
    pub pixel_width: u32,
    pub pixel_height: u32,
    /// The part of the image it shows, in image pixels; a zero size shows all of it.
    pub source: [u32; 4],
}

/// The placements a terminal draws, and the sequence of the set they came from.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Placements {
    placed: Vec<Placed>,
    applied: Option<u32>,
}

impl Placements {
    /// Takes the set the daemon described at `seq`, unless a later one was already taken. A set
    /// at the same sequence is taken, since a resize describes again at the last one. `history`
    /// is the terminal's history rows now. True when the set was taken.
    pub fn apply(&mut self, seq: u32, described: &[Described], history: u32) -> bool {
        if self.applied.is_some_and(|applied| seq < applied) {
            return false;
        }
        self.applied = Some(seq);
        self.placed = place(described, history);
        true
    }

    /// Takes the set a restored screen carries as the whole truth, and any sequence after it.
    pub fn seed(&mut self, described: &[Described], history: u32) {
        self.applied = None;
        self.placed = place(described, history);
    }

    /// The placements, drawn first to last: by z, then placement id.
    pub fn placed(&self) -> &[Placed] {
        &self.placed
    }
}

fn place(described: &[Described], history: u32) -> Vec<Placed> {
    let mut placed: Vec<Placed> = described
        .iter()
        .filter(|d| !d.virtual_ && d.pixel_width > 0 && d.pixel_height > 0)
        .filter_map(|d| {
            let row = u32::try_from(i64::from(history) + i64::from(d.screen_row)).ok()?;
            let unsigned = |n: i32| u32::try_from(n).unwrap_or(0);
            Some(Placed {
                image_id: d.image_id,
                placement_id: d.placement_id,
                generation: d.generation,
                z: d.z,
                row,
                col: d.col,
                pixel_width: d.pixel_width as u32,
                pixel_height: d.pixel_height as u32,
                source: d.source.map(unsigned),
            })
        })
        .collect();
    placed.sort_by_key(|p| (p.z, p.placement_id));
    placed
}

/// Where a placement draws in the grid, clipped to it, and the share of its box that survives
/// the clip, in texture order.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Quad {
    pub x: f32,
    pub y: f32,
    pub width: f32,
    pub height: f32,
    pub u: [f32; 2],
    pub v: [f32; 2],
}

/// The grid a placement draws over: its first row shown, its cell and its size, in the units
/// the quad comes out in, and the device pixels in one of those units.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Viewport {
    pub top: u32,
    pub cell: (f32, f32),
    pub size: (f32, f32),
    pub device_per_unit: f32,
}

/// Where `placed` draws in `viewport`, or None when none of it shows.
pub fn quad(placed: &Placed, viewport: Viewport) -> Option<Quad> {
    let scale = if viewport.device_per_unit > 0.0 {
        viewport.device_per_unit
    } else {
        1.0
    };
    let x = placed.col as f32 * viewport.cell.0;
    let y = (i64::from(placed.row) - i64::from(viewport.top)) as f32 * viewport.cell.1;
    let width = placed.pixel_width as f32 / scale;
    let height = placed.pixel_height as f32 / scale;
    let (left, top) = (x.max(0.0), y.max(0.0));
    let right = (x + width).min(viewport.size.0);
    let bottom = (y + height).min(viewport.size.1);
    if right <= left || bottom <= top {
        return None;
    }
    Some(Quad {
        x: left,
        y: top,
        width: right - left,
        height: bottom - top,
        u: [(left - x) / width, (right - x) / width],
        v: [(top - y) / height, (bottom - y) / height],
    })
}

/// The part of a `texture`-sized image the quad samples, in image pixels: x, y, width, height.
pub fn source_rect(placed: &Placed, quad: &Quad, texture: (u32, u32)) -> [f32; 4] {
    let [sx, sy, sw, sh] = placed.source;
    let (x, y, w, h) = if sw > 0 && sh > 0 {
        (sx as f32, sy as f32, sw as f32, sh as f32)
    } else {
        (0.0, 0.0, texture.0 as f32, texture.1 as f32)
    };
    [
        x + quad.u[0] * w,
        y + quad.v[0] * h,
        (quad.u[1] - quad.u[0]) * w,
        (quad.v[1] - quad.v[0]) * h,
    ]
}

/// How a stored image lays out its pixels.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Format {
    Rgb,
    Rgba,
    GrayAlpha,
    Gray,
}

impl Format {
    fn bytes(self) -> usize {
        match self {
            Format::Rgb => 3,
            Format::Rgba => 4,
            Format::GrayAlpha => 2,
            Format::Gray => 1,
        }
    }
}

/// A stored image's pixels, as RGBA, ready to draw. `id` tells one picture from another for
/// the renderer, which keeps a texture per picture.
#[derive(Debug, PartialEq, Eq)]
pub struct Picture {
    pub id: u64,
    pub width: u32,
    pub height: u32,
    pub rgba: Vec<u8>,
}

impl Picture {
    /// The picture of `pixels` laid out in `format`, or None when they are not `width`×`height`.
    pub fn new(id: u64, width: u32, height: u32, format: Format, pixels: &[u8]) -> Option<Picture> {
        let count = (width as usize).checked_mul(height as usize)?;
        if count == 0 || pixels.len() != count.checked_mul(format.bytes())? {
            return None;
        }
        let rgba = match format {
            Format::Rgba => pixels.to_vec(),
            Format::Rgb => pixels
                .as_chunks::<3>()
                .0
                .iter()
                .flat_map(|&[r, g, b]| [r, g, b, 255])
                .collect(),
            Format::GrayAlpha => pixels
                .as_chunks::<2>()
                .0
                .iter()
                .flat_map(|&[g, a]| [g, g, g, a])
                .collect(),
            Format::Gray => pixels.iter().flat_map(|&g| [g, g, g, 255]).collect(),
        };
        Some(Picture {
            id,
            width,
            height,
            rgba,
        })
    }
}
