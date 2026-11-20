use rune::{El, col, row};

use super::block::{Block, Kind, inner_gap, spaced};
use super::measure::COPY_FLOOR;
use crate::system::{Space, door};

/// Blocks in columns side by side, each as wide as the others, such as the groups of a
/// reference sheet. Where a column would get narrower than words beside a control keep, the
/// columns go one under another.
pub struct Columns {
    key: String,
    columns: Vec<Vec<Block>>,
}

impl Columns {
    pub fn new(key: impl Into<String>) -> Columns {
        Columns {
            key: key.into(),
            columns: Vec::new(),
        }
    }

    pub fn column(mut self, blocks: Vec<Block>) -> Self {
        self.columns.push(blocks);
        self
    }

    pub fn build(self) -> Block {
        let columns: Vec<El> = self
            .columns
            .into_iter()
            .map(|blocks| {
                col()
                    .grow_from(1.0, COPY_FLOOR)
                    .min_w(0.0)
                    .child(spaced(blocks, inner_gap, None))
            })
            .collect();
        Block::new(
            Kind::Custom,
            door::gaps(
                row().key(self.key).w_full().flex_wrap().items_start(),
                Space::S24,
                Space::S32,
            )
            .children(columns),
        )
    }
}
