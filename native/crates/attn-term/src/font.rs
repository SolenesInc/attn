use rune::{Family, TextStyle};

/// The terminal's font stack, with the Nerd Font the app registers for icon glyphs.
pub const FAMILY: &str = "Iosevka, Menlo, Monaco, \"DejaVu Sans Mono\", \"Courier New\", \"Symbols Nerd Font Mono\", monospace";
pub const FONT_SIZE: f32 = 14.0;

/// A terminal cell's size in points: as wide as
/// `M`, rounded up, and 1.45 lines of the font size tall.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Metrics {
    pub font_size: f32,
    pub cell_w: f32,
    pub cell_h: f32,
}

impl Metrics {
    #[allow(clippy::disallowed_methods)]
    pub fn of(font_size: f32) -> Metrics {
        let m = rune::text::advance(
            "M",
            TextStyle::new(font_size, 400.0).family(Family::Named(FAMILY)),
        );
        Metrics {
            font_size,
            cell_w: m.ceil().max(1.0),
            cell_h: (font_size * 1.45).ceil().max(1.0),
        }
    }

    /// The grid that fits `w`×`h` points at `scale` device pixels per point, at least one cell
    /// each way.
    pub fn fit(&self, w: f32, h: f32, scale: f32) -> (u16, u16) {
        let (cw, ch) = self.device_cell(scale);
        let count = |len: f32, cell: f32| {
            (len * scale / cell).floor().clamp(1.0, f32::from(u16::MAX)) as u16
        };
        (count(w, cw), count(h, ch))
    }

    /// A cell's size in device pixels at `scale`, as the renderer draws it.
    pub fn device_cell(&self, scale: f32) -> (f32, f32) {
        (
            (self.cell_w * scale).round().max(1.0),
            (self.cell_h * scale).round().max(1.0),
        )
    }

    /// A drawn cell's size in view points at `scale`.
    pub fn cell_points(&self, scale: f32) -> (f32, f32) {
        let (cw, ch) = self.device_cell(scale);
        (cw / scale, ch / scale)
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

    /// The cell under `(x, y)` as a selection ends at it, when the point is in the slot: past the
    /// last column is the column after it.
    pub fn cell_in(
        &self,
        (cols, rows): (u16, u16),
        (w, h): (f32, f32),
        scale: f32,
        (x, y): (f32, f32),
    ) -> Option<(u16, u16)> {
        if !(0.0..w).contains(&x) || !(0.0..h).contains(&y) {
            return None;
        }
        let (cw, ch) = self.device_cell(scale);
        let fit = fit((cols, rows), (cw, ch), (w * scale, h * scale));
        let at = |p: f32, offset: f32, cell: f32, last: u16| {
            ((p * scale - offset) / (cell * fit.zoom))
                .floor()
                .clamp(0.0, f32::from(last)) as u16
        };
        Some((
            at(x, fit.offset[0], cw, cols),
            at(y, fit.offset[1], ch, rows.saturating_sub(1)),
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

    /// The scale nearest `scale` at which a row is a whole number of device pixels high.
    pub(crate) fn whole_row_scale(&self, scale: f32) -> f32 {
        (self.cell_h * scale).round().max(1.0) / self.cell_h
    }

    /// The pixel size `cols`×`rows` cells take at `scale` device pixels per point.
    pub fn pixels(&self, (cols, rows): (u16, u16), scale: f32) -> (u32, u32) {
        let (cw, ch) = self.device_cell(scale);
        (u32::from(cols) * cw as u32, u32::from(rows) * ch as u32)
    }
}

pub(crate) struct Fit {
    pub(crate) zoom: f32,
    pub(crate) offset: [f32; 2],
}

/// The zoom across and down, and the offset, that show a whole grid of `drawn` cells in a slot at
/// the proportions `metrics` give its cells, centered. The two zooms differ by what rounding the
/// drawn cell to whole pixels took.
pub(crate) fn contain(
    (cols, rows): (u16, u16),
    metrics: Metrics,
    drawn: (f32, f32),
    (w, h): (f32, f32),
) -> ([f32; 2], [f32; 2]) {
    let (cols, rows) = (f32::from(cols), f32::from(rows));
    let scale = (w / (cols * metrics.cell_w)).min(h / (rows * metrics.cell_h));
    let (cw, ch) = (metrics.cell_w * scale, metrics.cell_h * scale);
    (
        [cw / drawn.0, ch / drawn.1],
        [(w - cols * cw) / 2.0, (h - rows * ch) / 2.0],
    )
}

/// How a grid sits in its slot: a PTY smaller than the slot, sized by another client, is scaled up
/// to fit and centered.
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
