use std::borrow::Cow;
use std::sync::Arc;

use parley::{FontContext, FontFamily, LayoutContext, StyleProperty};

/// The Tauri terminal's font stack, with the bundled Nerd Font for icon glyphs.
pub const FAMILY: &str = "Iosevka, Menlo, Monaco, \"DejaVu Sans Mono\", \"Courier New\", \"Symbols Nerd Font Mono\", monospace";
pub const FONT_SIZE: f32 = 14.0;
const NERD_FONT: &[u8] =
    include_bytes!("../../../../app/public/fonts/SymbolsNerdFontMono-Regular.ttf");

/// A terminal cell's size in points, measured as the Tauri terminal measures it: as wide as
/// `M`, rounded up, and 1.45 lines of the font size tall.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Metrics {
    pub font_size: f32,
    pub cell_w: f32,
    pub cell_h: f32,
}

impl Metrics {
    /// The grid that fits `w`×`h` points, at least one cell each way.
    pub fn fit(&self, w: f32, h: f32) -> (u16, u16) {
        let count =
            |len: f32, cell: f32| (len / cell).floor().clamp(1.0, f32::from(u16::MAX)) as u16;
        (count(w, self.cell_w), count(h, self.cell_h))
    }

    /// A cell's size in device pixels at `scale`, as the renderer draws it.
    pub(crate) fn device_cell(&self, scale: f32) -> (f32, f32) {
        ((self.cell_w * scale).round(), (self.cell_h * scale).round())
    }

    /// The zero-based cell drawn under (`x`, `y`) when a `shape` grid draws in a `slot` at
    /// `scale`, both in points from the slot's top-left; None off the grid.
    pub fn cell_at(
        &self,
        (cols, rows): (u16, u16),
        (w, h): (f32, f32),
        scale: f32,
        (x, y): (f32, f32),
    ) -> Option<(u16, u16)> {
        let (cw, ch) = self.device_cell(scale);
        let fit = fit((cols, rows), (cw, ch), (w * scale, h * scale));
        let at = |p: f32, offset: f32, cell: f32, count: u16| {
            let i = ((p * scale - offset) / (cell * fit.zoom)).floor();
            (i >= 0.0 && i < f32::from(count)).then_some(i as u16)
        };
        Some((
            at(x, fit.offset[0], cw, cols)?,
            at(y, fit.offset[1], ch, rows)?,
        ))
    }

    /// Where `cell` draws, as x, y, width and height in points from the slot's top-left, when a
    /// `shape` grid draws in a `slot` at `scale`.
    pub fn cell_rect(
        &self,
        (cols, rows): (u16, u16),
        (w, h): (f32, f32),
        scale: f32,
        (col, row): (u16, u16),
    ) -> [f32; 4] {
        let (cw, ch) = self.device_cell(scale);
        let fit = fit((cols, rows), (cw, ch), (w * scale, h * scale));
        let (cw, ch) = (cw * fit.zoom, ch * fit.zoom);
        [
            (fit.offset[0] + f32::from(col) * cw) / scale,
            (fit.offset[1] + f32::from(row) * ch) / scale,
            cw / scale,
            ch / scale,
        ]
    }

    /// The pixel size `cols`×`rows` cells take at `scale` device pixels per point.
    pub fn pixels(&self, (cols, rows): (u16, u16), scale: f32) -> (u32, u32) {
        let px = |n: u16, cell: f32| (f32::from(n) * cell * scale).round() as u32;
        (px(cols, self.cell_w), px(rows, self.cell_h))
    }
}

/// The fonts a terminal draws with.
pub struct Fonts {
    fcx: FontContext,
    lcx: LayoutContext<()>,
}

impl Default for Fonts {
    fn default() -> Self {
        let mut fcx = FontContext::new();
        fcx.collection
            .register_fonts(parley::fontique::Blob::new(Arc::new(NERD_FONT)), None);
        Fonts {
            fcx,
            lcx: LayoutContext::new(),
        }
    }
}

impl Fonts {
    pub(crate) fn contexts(&mut self) -> (&mut FontContext, &mut LayoutContext<()>) {
        (&mut self.fcx, &mut self.lcx)
    }

    pub fn metrics(&mut self, font_size: f32) -> Metrics {
        let mut builder = self.lcx.ranged_builder(&mut self.fcx, "M", 1.0, true);
        builder.push_default(StyleProperty::FontFamily(FontFamily::Source(
            Cow::Borrowed(FAMILY),
        )));
        builder.push_default(StyleProperty::FontSize(font_size));
        let mut layout = builder.build("M");
        layout.break_all_lines(None);
        Metrics {
            font_size,
            cell_w: layout.full_width().ceil().max(1.0),
            cell_h: (font_size * 1.45).ceil().max(1.0),
        }
    }
}

pub(crate) struct Fit {
    pub(crate) zoom: f32,
    pub(crate) offset: [f32; 2],
}

/// How a grid sits in its slot: a PTY smaller than the slot, sized by another client, is
/// scaled up to fit and centered, as the Tauri terminal's `scaledToFitTransform` does.
pub(crate) fn fit((cols, rows): (u16, u16), (cw, ch): (f32, f32), (w, h): (f32, f32)) -> Fit {
    let room = ((w / cw).floor(), (h / ch).floor());
    let (cols, rows) = (f32::from(cols), f32::from(rows));
    let spare = cols <= room.0 && rows <= room.1 && (cols < room.0 || rows < room.1);
    if !spare {
        return Fit {
            zoom: 1.0,
            offset: [0.0, 0.0],
        };
    }
    let (gw, gh) = (cols * cw, rows * ch);
    let zoom = (w / gw).min(h / gh);
    Fit {
        zoom,
        offset: [(w - gw * zoom) / 2.0, (h - gh * zoom) / 2.0],
    }
}
