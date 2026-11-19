use rune::col;

use super::block::{Block, Kind, inner_gap, spaced};
use crate::Tokens;
use crate::system::{Radius, Space, door};

/// Something being written on a page, apart from what the page shows: its fields and its
/// actions on a raised card.
pub struct Form {
    key: String,
    label: String,
    blocks: Vec<Block>,
}

impl Form {
    /// `label` names the form for assistive tools.
    pub fn new(key: impl Into<String>, label: impl Into<String>) -> Form {
        Form {
            key: key.into(),
            label: label.into(),
            blocks: Vec::new(),
        }
    }

    pub fn push(mut self, block: Block) -> Self {
        self.blocks.push(block);
        self
    }

    pub fn push_all(mut self, blocks: impl IntoIterator<Item = Block>) -> Self {
        self.blocks.extend(blocks);
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        let form = col()
            .key(self.key)
            .role("form")
            .label(self.label)
            .w_full()
            .fill(t.bg_elevated)
            .border(1.0, t.border_subtle)
            .child(spaced(self.blocks, inner_gap, None));
        let form = door::corner(door::inset(form, Space::S16, Space::S16), Radius::Card);
        Block::new(Kind::Custom, form)
    }
}
