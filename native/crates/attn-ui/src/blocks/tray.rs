use rune::{El, col};

use crate::Tokens;
use crate::system::{Space, door};

/// The strip along the foot of a panel that holds what belongs to where the panel's cursor
/// stands, such as the notes on its line, with a rule over it. It sets its parts in as far as
/// a [`super::Bar`] does, so a panel's head and foot line up. Assistive tools find it as a
/// landmark named `name`.
pub struct Tray {
    key: String,
    name: String,
    items: Vec<El>,
}

impl Tray {
    pub fn new(key: impl Into<String>, name: impl Into<String>) -> Tray {
        Tray {
            key: key.into(),
            name: name.into(),
            items: Vec::new(),
        }
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
        let tray = col()
            .key(self.key)
            .role("region")
            .label(self.name)
            .w_full()
            .children(self.items);
        door::rule_above(
            door::gap(door::inset(tray, Space::S16, Space::S8), Space::S4),
            t.border_subtle,
        )
    }
}
