use rune::{El, col, row};

use crate::Tokens;
use crate::system::{Radius, Space, Type, door, focus, layers, words};

/// A short list of things to do to one thing, open under it.
pub struct Menu {
    key: String,
    label: String,
    items: Vec<El>,
}

impl Menu {
    pub fn new(key: impl Into<String>, label: impl Into<String>) -> Menu {
        Menu {
            key: key.into(),
            label: label.into(),
            items: Vec::new(),
        }
    }

    /// One thing to do, keyed `key`; the caller gives it its press.
    pub fn item(key: impl Into<String>, said: impl Into<String>, t: &Tokens) -> El {
        let said = said.into();
        let item = row()
            .key(key.into())
            .role("menuitem")
            .label(said.clone())
            .focusable(true)
            .w_full()
            .child(
                words(said, Type::Body, t.text_primary)
                    .a11y_hidden()
                    .truncate()
                    .shrink()
                    .min_w(0.0),
            );
        let item = door::corner(door::inset(item, Space::S8, Space::S4), Radius::Tag);
        focus::ring(layers::pressable(item, t.bg_elevated, t), t)
    }

    /// An item, or a note when there is nothing to do.
    pub fn push(mut self, item: impl Into<El>) -> Self {
        self.items.push(item.into());
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let menu = col()
            .key(self.key)
            .role("menu")
            .label(self.label)
            .w_full()
            .fill(t.bg_elevated)
            .border(1.0, t.border)
            .children(self.items);
        door::corner(
            door::gap(door::inset(menu, Space::S4, Space::S4), Space::S2),
            Radius::Control,
        )
    }
}
