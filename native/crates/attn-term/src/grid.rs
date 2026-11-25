use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};

pub type Rgb = [u8; 3];

pub const BOLD: u8 = 1;
pub const ITALIC: u8 = 2;
pub const UNDERLINE: u8 = 4;
pub const FAINT: u8 = 8;
pub const STRIKETHROUGH: u8 = 16;
pub const INVISIBLE: u8 = 32;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, Default)]
pub enum Width {
    #[default]
    Narrow,
    Wide,
    Spacer,
}

/// One grapheme: a lone code point is its own id; a longer cluster is interned in its grid.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, Default)]
pub struct Grapheme(u32);

const INTERNED: u32 = 0x0011_0000;

impl Grapheme {
    pub fn is(self, c: char) -> bool {
        self.0 == u32::from(c)
    }
}

/// A cell as drawn: inverse is already applied to its colours, and `bg` is None where the
/// terminal's own background shows through.
#[derive(Clone, Copy, Debug, PartialEq, Default)]
pub struct Cell {
    pub grapheme: Grapheme,
    pub fg: Rgb,
    pub bg: Option<Rgb>,
    pub flags: u8,
    pub width: Width,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Default)]
pub enum CursorStyle {
    Bar,
    #[default]
    Block,
    Underline,
    BlockHollow,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Cursor {
    pub col: u16,
    pub row: u16,
    pub style: CursorStyle,
    pub color: Option<Rgb>,
}

/// A color laid over part of one row of the view: a wash over the cells' backgrounds and under
/// their text, a line at the underline's place, or a box's edges.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Overlay {
    pub row: u16,
    pub cols: (u16, u16),
    pub color: Rgb,
    pub alpha: u8,
    pub kind: OverlayKind,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum OverlayKind {
    Wash,
    Underline,
    /// The row's part of a box drawn around several rows: its sides, and its top or bottom
    /// edge where the box starts or ends on this row.
    Outline {
        top: bool,
        bottom: bool,
    },
}

/// A kitty image on the terminal: its pixels, and where it is placed.
#[derive(Clone, Debug)]
pub struct Image {
    pub picture: std::sync::Arc<crate::kitty::Picture>,
    pub placed: crate::kitty::Placed,
}

impl PartialEq for Image {
    fn eq(&self, other: &Image) -> bool {
        self.picture.id == other.picture.id && self.placed == other.placed
    }
}

static LAST_STAMP: AtomicU64 = AtomicU64::new(0);

/// A stamp no grid has carried before.
fn next_stamp() -> u64 {
    LAST_STAMP.fetch_add(1, Ordering::Relaxed) + 1
}

/// The visible screen of one terminal, read out of libghostty-vt by dirty row. Each row, the
/// overlays and the images carry a stamp that changes when they do and that nothing else in any
/// grid has, so a renderer draws what differs from the stamps it drew. Zero is never drawn.
#[derive(Default)]
pub struct Grid {
    pub cols: u16,
    pub rows: u16,
    pub fg: Rgb,
    pub bg: Rgb,
    pub cells: Vec<Cell>,
    stamps: Vec<u64>,
    pub cursor: Option<Cursor>,
    overlays: Vec<Overlay>,
    overlays_stamp: u64,
    images: Vec<Image>,
    images_stamp: u64,
    /// The screen row the view's top row is, which the images' rows count from.
    image_top: u32,
    /// Each row's runs of kitty placeholder cells, whose cells draw no glyph.
    placeholders: Vec<Vec<crate::kitty::Run>>,
    strings: Vec<String>,
    interned: HashMap<String, u32>,
}

impl Grid {
    pub fn text(&self, grapheme: Grapheme) -> std::borrow::Cow<'_, str> {
        match grapheme.0 {
            0 => "".into(),
            id if id >= INTERNED => self.strings[(id - INTERNED) as usize].as_str().into(),
            cp => char::from_u32(cp)
                .map(String::from)
                .unwrap_or_default()
                .into(),
        }
    }

    /// The first and last characters of a cell's text, read without building it.
    pub fn ends(&self, grapheme: Grapheme) -> Option<(char, char)> {
        match grapheme.0 {
            0 => None,
            id if id >= INTERNED => {
                let text = &self.strings[(id - INTERNED) as usize];
                Some((text.chars().next()?, text.chars().next_back()?))
            }
            cp => char::from_u32(cp).map(|c| (c, c)),
        }
    }

    pub(crate) fn intern(&mut self, codepoints: &[u32]) -> Grapheme {
        match codepoints {
            [] | [0] => Grapheme(0),
            [cp] => Grapheme(*cp),
            many => {
                let text: String = many.iter().filter_map(|&cp| char::from_u32(cp)).collect();
                if let Some(&id) = self.interned.get(&text) {
                    return Grapheme(id);
                }
                let id = INTERNED + self.strings.len() as u32;
                self.strings.push(text.clone());
                self.interned.insert(text, id);
                Grapheme(id)
            }
        }
    }

    /// The stamp of each row.
    pub fn stamps(&self) -> &[u64] {
        &self.stamps
    }

    /// The stamp of the overlays, zero until there are any.
    pub(crate) fn overlays_stamp(&self) -> u64 {
        self.overlays_stamp
    }

    /// The stamp of the images and the row the view starts at, zero until there are any.
    pub(crate) fn images_stamp(&self) -> u64 {
        self.images_stamp
    }

    pub fn overlays(&self) -> &[Overlay] {
        &self.overlays
    }

    /// Lays `overlays` over the view; true when they differ from what it had.
    pub fn set_overlays(&mut self, overlays: Vec<Overlay>) -> bool {
        if overlays == self.overlays {
            return false;
        }
        self.overlays = overlays;
        self.overlays_stamp = next_stamp();
        true
    }

    /// The runs of kitty placeholder cells in view, by the row of the view they are on.
    pub fn placeholders(&self) -> impl Iterator<Item = (u16, &crate::kitty::Run)> {
        self.placeholders
            .iter()
            .enumerate()
            .flat_map(|(row, runs)| runs.iter().map(move |run| (row as u16, run)))
    }

    /// The kitty images placed on the terminal, drawn first to last.
    pub fn images(&self) -> (&[Image], u32) {
        (&self.images, self.image_top)
    }

    /// Places `images` on the terminal, its view's top row at screen row `top`.
    pub fn set_images(&mut self, images: Vec<Image>, top: u32) {
        if images == self.images && (images.is_empty() || top == self.image_top) {
            return;
        }
        self.images = images;
        self.image_top = top;
        self.images_stamp = next_stamp();
    }

    pub fn row(&self, y: usize) -> &[Cell] {
        let cols = usize::from(self.cols);
        &self.cells[y * cols..(y + 1) * cols]
    }

    /// The row as text, wide characters once and trailing blanks dropped.
    pub fn row_text(&self, y: usize) -> String {
        self.row_text_keeping(y, 0)
    }

    /// The row as text, wide characters once and blanks dropped from its end, all but its first
    /// `keep` bytes.
    fn row_text_keeping(&self, y: usize, keep: usize) -> String {
        let mut line = String::new();
        for cell in self.row(y) {
            if cell.width == Width::Spacer {
                continue;
            }
            match self.text(cell.grapheme) {
                t if t.is_empty() => line.push(' '),
                t => line.push_str(&t),
            }
        }
        line.truncate(line.trim_end().len().max(keep));
        line
    }

    /// The screen as text, a line per row, and where the cursor sits in it as a byte offset:
    /// at the end of the text while the cursor is hidden. The cursor's row keeps its blanks up
    /// to the cursor.
    pub fn text_and_cursor(&self) -> (String, usize) {
        let mut text = String::new();
        let mut cursor = None;
        for y in 0..usize::from(self.rows) {
            if y > 0 {
                text.push('\n');
            }
            let at = self
                .cursor
                .as_ref()
                .filter(|c| usize::from(c.row) == y)
                .map(|c| self.byte_of_col(y, c.col));
            if let Some(at) = at {
                cursor = Some(text.len() + at);
            }
            text.push_str(&self.row_text_keeping(y, at.unwrap_or(0)));
        }
        let end = text.len();
        (text, cursor.unwrap_or(end))
    }

    fn byte_of_col(&self, y: usize, col: u16) -> usize {
        self.row(y)
            .iter()
            .take(usize::from(col))
            .filter(|cell| cell.width != Width::Spacer)
            .map(|cell| self.text(cell.grapheme).len().max(1))
            .sum()
    }

    /// Every row as text, one per line.
    pub fn text_lines(&self) -> Vec<String> {
        (0..usize::from(self.rows))
            .map(|y| self.row_text(y))
            .collect()
    }

    pub(crate) fn reshape(&mut self, cols: u16, rows: u16) {
        self.cols = cols;
        self.rows = rows;
        self.cells = vec![Cell::default(); usize::from(cols) * usize::from(rows)];
        self.stamps = (0..rows).map(|_| next_stamp()).collect();
        self.placeholders = vec![Vec::new(); usize::from(rows)];
    }

    pub(crate) fn set_placeholders(&mut self, row: usize, runs: Vec<crate::kitty::Run>) {
        self.placeholders[row] = runs;
    }

    pub(crate) fn mark(&mut self, row: u16) {
        if let Some(stamp) = self.stamps.get_mut(usize::from(row)) {
            *stamp = next_stamp();
        }
    }
}
