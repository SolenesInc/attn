use rune::{El, col, row};

use super::block::{Block, page_gap, spaced};
use super::page::{Head, head_gap};
use crate::Tokens;
use crate::system::{Space, door};

/// How much room a page is read in, which decides how far its words stand from the edges.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Room {
    /// A dock beside the work, or a tile.
    Narrow,
    /// A column of a window given over to reading.
    Wide,
}

/// A page read in a panel, a column or a tile, inside a scroll its place owns: its head, then
/// its blocks spaced as a page spaces them.
#[derive(Default)]
pub struct Reader {
    above: Vec<El>,
    head: Option<Head>,
    blocks: Vec<Block>,
}

impl Reader {
    pub fn new() -> Reader {
        Reader::default()
    }

    /// A way back to where the reader came from, on a line above the head.
    pub fn above(mut self, way_back: impl Into<El>) -> Self {
        self.above.push(way_back.into());
        self
    }

    pub fn head(mut self, head: Head) -> Self {
        self.head = Some(head);
        self
    }

    pub fn push(mut self, block: Block) -> Self {
        self.blocks.push(block);
        self
    }

    pub fn push_all(mut self, blocks: impl IntoIterator<Item = Block>) -> Self {
        self.blocks.extend(blocks);
        self
    }

    /// The page without its frame, for a place that insets it itself, such as a pane.
    pub fn bare(self, t: &Tokens) -> El {
        self.page(None, t)
    }

    pub fn build(self, key: impl Into<String>, room: Room, t: &Tokens) -> El {
        let page = self.page(Some(t.border_subtle), t).key(key.into());
        match room {
            Room::Narrow => door::frame(page, Space::S16, Space::S16, Space::S32),
            Room::Wide => door::frame(page, Space::S24, Space::S32, Space::S32),
        }
    }

    fn page(self, rule: Option<rune::Color>, t: &Tokens) -> El {
        let first = self.blocks.first().map(|b| b.kind);
        let above = (!self.above.is_empty()).then(|| {
            door::below(
                door::gap(row().w_full().flex_wrap().items_center(), Space::S4)
                    .children(self.above),
                Space::S12,
            )
        });
        let ruled = rule.filter(|_| self.head.is_some() && first.is_some());
        let head = self.head.map(|head| match ruled {
            Some(_) => head.build(t),
            None => door::below(head.build(t), head_gap(first)),
        });
        let blocks = spaced(self.blocks, page_gap, rule);
        let blocks = match ruled {
            Some(ink) => door::ruled_above(blocks, head_gap(first), ink),
            None => blocks,
        };
        col().w_full().children(above).children(head).child(blocks)
    }
}
