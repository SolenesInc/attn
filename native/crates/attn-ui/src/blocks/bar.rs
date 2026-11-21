use rune::{El, row};

use crate::Tokens;
use crate::system::{Size, Space, door};

/// The narrowest a bar's field gets: its search mark, and room for a short query of about ten
/// characters in `Body`.
const FIELD_FLOOR: f32 = 120.0;

/// The strip across the top of a panel that holds where the panel stands and what it can do,
/// with a rule under it. Its first part takes the room the others leave. A bar of words stands as
/// tall as one with compact buttons.
pub struct Bar {
    key: String,
    start: Vec<El>,
    lead: Option<El>,
    items: Vec<El>,
    slim: bool,
}

impl Bar {
    pub fn new(key: impl Into<String>) -> Bar {
        Bar {
            key: key.into(),
            start: Vec::new(),
            lead: None,
            items: Vec::new(),
            slim: false,
        }
    }

    /// What grows to fill the bar, such as a trail.
    pub fn lead(mut self, lead: El) -> Self {
        self.lead = Some(lead.min_w(0.0));
        self
    }

    /// A field that grows to fill the bar, such as a find field, keeping room for a short query
    /// where the bar is narrow.
    pub fn field(mut self, field: El) -> Self {
        self.lead = Some(field.min_w(FIELD_FLOOR));
        self
    }

    /// A mark before the lead, such as a search field's prompt.
    pub fn start(mut self, mark: El) -> Self {
        self.start.push(mark);
        self
    }

    /// No taller than what it holds, such as the head over a file view, which leaves the file
    /// the room.
    pub fn slim(mut self) -> Self {
        self.slim = true;
        self
    }

    pub fn start_all(mut self, marks: impl IntoIterator<Item = El>) -> Self {
        self.start.extend(marks);
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
            .min_h(if self.slim {
                0.0
            } else {
                Size::Compact.height()
            })
            .items_center()
            .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
            .children(self.start)
            .children(self.lead.map(|lead| lead.grow(1.0)))
            .children(self.items);
        let tall = if self.slim { Space::S0 } else { Space::S6 };
        door::gap(door::inset(bar, Space::S16, tall), Space::S8)
    }
}
