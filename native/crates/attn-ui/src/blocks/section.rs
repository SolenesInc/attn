use rune::{El, col, row};

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
    region: bool,
    reachable: bool,
    aside: Vec<El>,
}

impl Section {
    pub fn new(key: impl Into<String>, title: impl Into<String>) -> Section {
        Section {
            key: key.into(),
            title: title.into(),
            description: String::new(),
            blocks: Vec::new(),
            region: false,
            reachable: false,
            aside: Vec::new(),
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

    /// The section is a landmark a reader moves between, as a part of a document is, rather
    /// than a group of settings.
    pub fn region(mut self) -> Self {
        self.region = true;
        self
    }

    /// A control elsewhere on the page goes to this section: its heading takes the keyboard
    /// without being a stop on Tab.
    pub fn reachable(mut self) -> Self {
        self.reachable = true;
        self
    }

    /// A control beside the heading, such as the button that changes what the section holds.
    pub fn aside(mut self, control: impl Into<El>) -> Self {
        self.aside.push(control.into());
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        let heading = words(self.title.clone(), Type::Heading, t.text_primary)
            .key(format!("{}.heading", self.key))
            .role("heading");
        let heading = if self.reachable {
            heading.focusable(true).tab_index(-1)
        } else {
            heading
        };
        let heading = if self.aside.is_empty() {
            heading
        } else {
            door::gaps(
                row().w_full().flex_wrap().items_center(),
                Space::S4,
                Space::S8,
            )
            .child(heading)
            .children(self.aside)
        };
        let head = door::gap(col().w_full(), Space::S4)
            .child(heading)
            .children((!self.description.is_empty()).then(|| {
                words(self.description, Type::Small, t.text_tertiary)
                    .wrap()
                    .max_w(READING)
            }));
        let section = col()
            .key(self.key)
            .role(if self.region { "region" } else { "group" })
            .label(self.title)
            .w_full()
            .child(head)
            .children((!self.blocks.is_empty()).then(|| spaced(self.blocks, inner_gap, None)));
        Block::new(Kind::Section, door::gap(section, Space::S12))
    }
}
