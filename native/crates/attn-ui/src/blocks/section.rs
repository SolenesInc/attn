use rune::col;

use super::block::{Block, Kind, inner_gap, spaced};
use super::measure::READING;
use crate::Tokens;
use crate::system::{Space, Type, door, words};

/// A titled part of a page: its name, what it is for, then its blocks. Sections that follow
/// each other on a page are set apart by a rule.
pub struct Section {
    key: String,
    title: String,
    description: String,
    blocks: Vec<Block>,
}

impl Section {
    pub fn new(key: impl Into<String>, title: impl Into<String>) -> Section {
        Section {
            key: key.into(),
            title: title.into(),
            description: String::new(),
            blocks: Vec::new(),
        }
    }

    pub fn description(mut self, said: impl Into<String>) -> Self {
        self.description = said.into();
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

    pub fn build(self, t: &Tokens) -> Block {
        let head = door::gap(col().w_full(), Space::S4)
            .child(words(self.title.clone(), Type::Heading, t.text_primary))
            .children((!self.description.is_empty()).then(|| {
                words(self.description, Type::Small, t.text_tertiary)
                    .wrap()
                    .max_w(READING)
            }));
        let section = col()
            .key(self.key)
            .role("group")
            .label(self.title)
            .w_full()
            .child(head)
            .children((!self.blocks.is_empty()).then(|| spaced(self.blocks, inner_gap, None)));
        Block::new(Kind::Section, door::gap(section, Space::S12))
    }
}
