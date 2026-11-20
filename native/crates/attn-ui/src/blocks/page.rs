use rune::{El, col, row};
use rune_kit::ScrollBar;

use super::block::{Block, Kind, page_gap, spaced};
use super::measure::{COPY_FLOOR, READING};
use crate::Tokens;
use crate::system::{Space, Type, door, words};

/// A page: its head, then its blocks in order, each spaced from the one above by what both are,
/// in a column that scrolls.
#[derive(Default)]
pub struct Page {
    head: Option<Head>,
    status: Option<(String, String, Vec<El>)>,
    group: Option<(String, String)>,
    dimmed: bool,
    blocks: Vec<Block>,
}

impl Page {
    pub fn new() -> Page {
        Page::default()
    }

    pub fn head(mut self, head: Head) -> Self {
        self.head = Some(head);
        self
    }

    /// What the page is doing, such as saving, under its head: always there for assistive tools
    /// to read, and taking room only while `parts` say something.
    pub fn status(
        mut self,
        key: impl Into<String>,
        said: impl Into<String>,
        parts: Vec<El>,
    ) -> Self {
        self.status = Some((key.into(), said.into(), parts));
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

    /// Names the page's blocks as one group, as tests and assistive tools find them.
    pub fn group(mut self, key: impl Into<String>, label: impl Into<String>) -> Self {
        self.group = Some((key.into(), label.into()));
        self
    }

    /// The page's feature is off: its blocks are drawn faded and take no input.
    pub fn dimmed(mut self, dimmed: bool) -> Self {
        self.dimmed = dimmed;
        self
    }

    /// The page in a column keyed `key` that scrolls, inset from its edges.
    pub fn build(self, key: impl Into<String>, t: &Tokens) -> El {
        let first = self.blocks.first().map(|b| b.kind);
        let head = self.head.map(|head| {
            let gap = if first == Some(Kind::Prose) {
                Space::S8
            } else {
                Space::S24
            };
            door::below(head.build(t), gap)
        });
        let status = self.status.map(|(key, said, parts)| {
            let shown = !parts.is_empty();
            let line = door::gaps(
                row().w_full().flex_wrap().items_center(),
                Space::S8,
                Space::S8,
            )
            .key(key)
            .role("status")
            .label(said)
            .children(parts);
            if shown {
                door::below(line, Space::S16)
            } else {
                line
            }
        });
        let mut body = spaced(self.blocks, page_gap, Some(t.border_subtle));
        if let Some((key, label)) = self.group {
            body = body.key(key).role("group").label(label);
        }
        if self.dimmed {
            body = body.opacity(DIMMED).inert();
        }
        let page = col()
            .key(key.into())
            .grow(1.0)
            .min_w(0.0)
            .h_full()
            .fill(t.bg_panel);
        door::frame(page, Space::S24, Space::S32, Space::S32)
            .scroll(rune::ScrollSpec {
                stick_bottom: false,
            })
            .children(head)
            .children(status)
            .child(body)
            .scroll_bar_styled(crate::scroll_bar(t))
    }
}

/// How strongly the blocks of a page whose feature is off are drawn.
const DIMMED: f32 = 0.45;

/// The head of a page: its title, what the page is for, small print under that, and controls
/// beside the title, such as the switch that turns the page's feature on.
pub struct Head {
    key: String,
    title: String,
    lead: Option<String>,
    note: Option<El>,
    aside: Vec<El>,
}

impl Head {
    /// `key` names the title, which reads as the page's heading.
    pub fn new(key: impl Into<String>, title: impl Into<String>) -> Head {
        Head {
            key: key.into(),
            title: title.into(),
            lead: None,
            note: None,
            aside: Vec::new(),
        }
    }

    pub fn lead(mut self, lead: impl Into<String>) -> Self {
        let lead = lead.into();
        self.lead = (!lead.is_empty()).then_some(lead);
        self
    }

    /// Small print under the lead, such as when a change takes effect.
    pub fn note(mut self, note: El) -> Self {
        self.note = Some(note);
        self
    }

    /// A control beside the title, centred on its line.
    pub fn aside(mut self, control: impl Into<El>) -> Self {
        self.aside.push(control.into());
        self
    }

    fn build(self, t: &Tokens) -> El {
        let title = words(self.title.clone(), Type::PageTitle, t.text_primary)
            .wrap()
            .key(self.key)
            .role("heading")
            .level(1)
            .label(self.title);
        let lead = self.lead.map(|lead| {
            words(lead, Type::Body, t.text_secondary)
                .wrap()
                .max_w(READING)
        });
        let said = door::gap(col().grow_from(1.0, COPY_FLOOR), Space::S8)
            .child(title)
            .children(lead)
            .children(self.note.map(|note| note.max_w(READING)));
        let aside = (!self.aside.is_empty()).then(|| {
            door::gaps(row().flex_wrap().items_center(), Space::S8, Space::S8)
                .min_h(door::line_height(Type::PageTitle))
                .max_w_frac(1.0)
                .children(self.aside)
        });
        door::gaps(
            row().w_full().flex_wrap().items_start(),
            Space::S12,
            Space::S24,
        )
        .child(said)
        .children(aside)
    }
}
