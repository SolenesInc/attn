use rune::{El, col, row};

use crate::Tokens;
use crate::system::{Radius, Said, Size, Space, Tone, Type, door, inks, words};

struct Cell {
    label: String,
    mark: Option<El>,
    said: Said,
    tone: Tone,
}

/// A few things side by side in cells of one width, each its mark and its word, such as a pull
/// request's checks, review and merge. A cell that reads Warn or Bad is tinted.
pub struct Cells {
    key: String,
    cells: Vec<Cell>,
}

impl Cells {
    pub fn new(key: impl Into<String>) -> Cells {
        Cells {
            key: key.into(),
            cells: Vec::new(),
        }
    }

    /// A cell that shows `mark` before `said` and is read as `label`.
    pub fn marked(
        mut self,
        label: impl Into<String>,
        mark: El,
        said: impl Into<Said>,
        tone: Tone,
    ) -> Self {
        self.cells.push(Cell {
            label: label.into(),
            mark: Some(mark),
            said: said.into(),
            tone,
        });
        self
    }

    /// A cell that says one thing in place of the others, such as "Merged".
    pub fn said(mut self, said: impl Into<Said>) -> Self {
        let said = said.into();
        self.cells.push(Cell {
            label: said.text(),
            mark: None,
            said,
            tone: Tone::Neutral,
        });
        self
    }

    /// How tall the cells stand.
    pub fn height() -> f32 {
        Size::Regular.height()
    }

    pub fn el(self, t: &Tokens) -> El {
        let columns = self.cells.len().max(1);
        let cells = self.cells.into_iter().enumerate().map(|(i, cell)| {
            let fill = match cell.tone {
                Tone::Warn | Tone::Bad => inks(cell.tone, t).1,
                _ => rune::Color::TRANSPARENT,
            };
            let ink = match cell.tone {
                Tone::Warn | Tone::Bad => t.text_primary,
                _ => t.text_secondary,
            };
            let line = door::gap(row().h_full().items_center().justify_center(), Space::S6)
                .min_w(0.0)
                .children(cell.mark.map(|mark| mark.a11y_hidden()))
                .child(
                    words(cell.said, Type::Small, ink)
                        .single_line()
                        .truncate()
                        .shrink()
                        .a11y_hidden(),
                );
            col()
                .key(format!("{}.{i}", self.key))
                .role("img")
                .label(cell.label)
                .h(Cells::height())
                .min_w(0.0)
                .fill(fill)
                .border_sides(
                    [0.0, 0.0, 0.0, if i > 0 { 1.0 } else { 0.0 }],
                    t.border_hover,
                )
                .child(line)
        });
        let line = col()
            .key(self.key.clone())
            .w_full()
            .equal_columns(columns)
            .border(1.0, t.border_hover)
            .children(cells);
        door::clip_corner(door::corner(line, Radius::Control), Radius::Control)
    }
}
