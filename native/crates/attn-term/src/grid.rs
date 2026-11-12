use std::collections::HashMap;

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

/// A color laid over part of one row of the view, as the Tauri renderer's overlays: a wash
/// over the cells' backgrounds and under their text, or a line at the underline's place.
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
}

/// The visible screen of one terminal, read out of libghostty-vt by dirty row. `dirty` marks
/// the rows that changed since the renderer last took them.
#[derive(Default)]
pub struct Grid {
    pub cols: u16,
    pub rows: u16,
    pub fg: Rgb,
    pub bg: Rgb,
    pub cells: Vec<Cell>,
    pub dirty: Vec<bool>,
    pub cursor: Option<Cursor>,
    overlays: Vec<Overlay>,
    /// The overlays changed since the renderer last took them.
    pub overlays_changed: bool,
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

    pub fn overlays(&self) -> &[Overlay] {
        &self.overlays
    }

    /// Lays `overlays` over the view; true when they differ from what it had.
    pub fn set_overlays(&mut self, overlays: Vec<Overlay>) -> bool {
        if overlays == self.overlays {
            return false;
        }
        self.overlays = overlays;
        self.overlays_changed = true;
        true
    }

    pub fn row(&self, y: usize) -> &[Cell] {
        let cols = usize::from(self.cols);
        &self.cells[y * cols..(y + 1) * cols]
    }

    /// The row as text, wide characters once and trailing blanks dropped.
    pub fn row_text(&self, y: usize) -> String {
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
        line.truncate(line.trim_end().len());
        line
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
        self.dirty = vec![true; usize::from(rows)];
    }

    pub(crate) fn mark(&mut self, row: u16) {
        if let Some(dirty) = self.dirty.get_mut(usize::from(row)) {
            *dirty = true;
        }
    }
}
