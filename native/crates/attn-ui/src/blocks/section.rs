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
    heading: Option<El>,
    description: String,
    aside: Option<String>,
    controls: Vec<El>,
    form: bool,
    blocks: Vec<Block>,
}

impl Section {
    pub fn new(key: impl Into<String>, title: impl Into<String>) -> Section {
        Section {
            key: key.into(),
            title: title.into(),
            heading: None,
            description: String::new(),
            aside: None,
            controls: Vec::new(),
            form: false,
            blocks: Vec::new(),
        }
    }

    /// The title as its feature builds it, such as a question whose words link to what they
    /// name; `title` still names the section.
    pub fn heading(mut self, heading: El) -> Self {
        self.heading = Some(heading);
        self
    }

    pub fn description(mut self, said: impl Into<String>) -> Self {
        self.description = said.into();
        self
    }

    /// A quiet word beside the title that sums the section up, such as how many are on. The
    /// section is announced with it.
    pub fn aside(mut self, said: impl Into<String>) -> Self {
        self.aside = Some(said.into());
        self
    }

    /// A control at the end of the title's line, such as the button that closes the section.
    pub fn control(mut self, control: impl Into<El>) -> Self {
        self.controls.push(control.into());
        self
    }

    /// Assistive tools find the section as a form named by its heading, not as a group.
    pub fn form(mut self) -> Self {
        self.form = true;
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
        let label = match &self.aside {
            Some(aside) => format!("{}, {aside}", self.title),
            None => self.title.clone(),
        };
        let title = door::gaps(row().flex_wrap().items_end(), Space::S2, Space::S8)
            .child(match self.heading {
                Some(heading) => heading.shrink().min_w(0.0),
                None => words(self.title, Type::Heading, t.text_primary)
                    .wrap()
                    .shrink(),
            })
            .children(
                self.aside
                    .map(|aside| words(aside, Type::Small, t.text_tertiary)),
            );
        let title = if self.controls.is_empty() {
            title
        } else {
            door::gap(row().w_full().items_center(), Space::S8)
                .child(title.grow(1.0).min_w(0.0))
                .children(self.controls)
        };
        let head = door::gap(col().w_full(), Space::S4).child(title).children(
            (!self.description.is_empty()).then(|| {
                words(self.description, Type::Small, t.text_tertiary)
                    .wrap()
                    .max_w(READING)
            }),
        );
        let section = col()
            .key(self.key)
            .role(if self.form { "form" } else { "group" })
            .label(label)
            .w_full()
            .child(head)
            .children((!self.blocks.is_empty()).then(|| spaced(self.blocks, inner_gap, None)));
        Block::new(Kind::Section, door::gap(section, Space::S12))
    }
}
