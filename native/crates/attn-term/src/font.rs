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
