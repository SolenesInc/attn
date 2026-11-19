use rune::{El, row};

use crate::Tokens;
use crate::system::{Size, Space, door};

/// The strip across the top of a panel that holds where the panel stands and what it can do,
/// with a rule under it. Its first part takes the room the others leave. A bar of words stands as
/// tall as one with compact buttons.
pub struct Bar {
    key: String,
    start: Vec<El>,
    lead: Option<El>,
    items: Vec<El>,
}

impl Bar {
    pub fn new(key: impl Into<String>) -> Bar {
        Bar {
            key: key.into(),
            start: Vec::new(),
            lead: None,
            items: Vec::new(),
        }
    }

    /// What grows to fill the bar, such as a trail or a field.
    pub fn lead(mut self, lead: El) -> Self {
        self.lead = Some(lead);
        self
    }

    /// A mark before the lead, such as a search field's prompt.
    pub fn start(mut self, mark: El) -> Self {
        self.start.push(mark);
        self
    }

    pub fn push(mut self, item: impl Into<El>) -> Self {
        self.items.push(item.into());
        self
    }

    pub fn push_all(mut self, items: impl IntoIterator<Item = El>) -> Self {
        self.items.extend(items);
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let bar = row()
            .key(self.key)
            .w_full()
            .min_h(Size::Compact.height())
            .items_center()
            .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
            .children(self.start)
            .children(self.lead.map(|lead| lead.grow(1.0).min_w(0.0)))
            .children(self.items);
        door::gap(door::inset(bar, Space::S16, Space::S6), Space::S8)
    }
}
