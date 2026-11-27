use rune::{El, col, row};

use super::block::{Block, Kind};
use crate::Tokens;
use crate::system::{Size, Space, Type, door, hover_layer, words};

/// Rows of data under the names of their columns, each column as wide as the others, such as
/// the landings a view lists. One row may be the one the keyboard is on.
pub struct DataTable {
    key: String,
    label: String,
    columns: Vec<String>,
    rows: Vec<(String, Vec<String>, bool)>,
}

impl DataTable {
    /// A table keyed `key`, read as `label`, with columns named `columns`.
    pub fn new(
        key: impl Into<String>,
        label: impl Into<String>,
        columns: Vec<String>,
    ) -> DataTable {
        DataTable {
            key: key.into(),
            label: label.into(),
            columns,
            rows: Vec::new(),
        }
    }

    /// A row keyed `key` of `cells`, one per column; `selected` is the row the keyboard is on.
    pub fn row(mut self, key: impl Into<String>, cells: Vec<String>, selected: bool) -> Self {
        self.rows.push((key.into(), cells, selected));
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        Block::new(Kind::Table, self.el(t))
    }

    pub fn el(self, t: &Tokens) -> El {
        let n = self.columns.len().max(1);
        let line = |key: String, cells: Vec<El>| {
            door::gap(row(), Space::S12)
                .key(key)
                .w_full()
                .min_h(Size::Small.height())
                .items_center()
                .equal_columns(n)
                .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
                .children(cells)
        };
        let head = line(
            format!("{}.head", self.key),
            (self.columns.iter())
                .map(|name| {
                    words(name.clone(), Type::Label, t.text_tertiary)
                        .single_line()
                        .truncate()
                })
                .collect(),
        )
        .role("row")
        .label("Columns");
        let rows = self.rows.into_iter().map(|(key, cells, selected)| {
            let said = cells.join(", ");
            let shown = line(
                key,
                cells
                    .into_iter()
                    .map(|cell| {
                        words(cell, Type::Body, t.text_primary)
                            .single_line()
                            .truncate()
                    })
                    .collect(),
            )
            .role("row")
            .label(said);
            let shown = if selected {
                shown.fill(hover_layer(t.bg_tile, t)).a11y_value("selected")
            } else {
                shown
            };
            door::inset(shown, Space::S8, Space::S0)
        });
        col()
            .key(self.key)
            .role("table")
            .label(self.label)
            .w_full()
            .child(door::inset(head, Space::S8, Space::S0))
            .children(rows)
    }
}
