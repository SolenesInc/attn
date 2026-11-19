use rune::{Color, El, row};

use crate::Tokens;
use crate::keycap::Keys;
use crate::system::{Radius, Size, Space, Type, door, words};

/// `keys` as keycaps, one cap a key: "f r h" is three caps.
pub fn caps(keys: &str, t: &Tokens) -> El {
    Keys::outlined(&keys.split(' ').collect::<Vec<_>>())
        .colors(t.text_secondary, t.border, Color::TRANSPARENT)
        .build(t)
}

/// A key and what it does: its caps, then the words.
pub fn hint(keys: &str, what: impl Into<String>, t: &Tokens) -> El {
    let what = what.into();
    door::gap(row().items_center(), Space::S4)
        .label(format!("{keys} {what}"))
        .child(caps(keys, t).a11y_hidden())
        .child(words(what, Type::Small, t.text_tertiary).a11y_hidden())
}

/// A tab a key picks, its caps before its name: drawn on a tint with an edge while it is picked.
pub fn key_tab(keys: &str, name: &str, on: bool, t: &Tokens) -> El {
    let tab = door::gap(row().items_center(), Space::S4)
        .role("tab")
        .label(format!("{keys} {name}"))
        .checked(on)
        .child(caps(keys, t).a11y_hidden())
        .child(
            words(
                name,
                Type::Small,
                if on { t.text_primary } else { t.text_secondary },
            )
            .a11y_hidden(),
        );
    let tab = door::corner(door::inset(tab, Space::S8, Space::S2), Radius::Tag);
    if on {
        tab.border(1.0, t.border).fill(t.accent.alpha(0.08))
    } else {
        tab.border(1.0, Color::TRANSPARENT)
    }
}

/// The line under a reader that names its keys: tabs and hints that wrap, and one hint at its
/// end, such as the key that shows them all.
pub struct KeyLine {
    key: String,
    label: String,
    tabs: Vec<El>,
    hints: Vec<El>,
    end: Option<El>,
}

impl KeyLine {
    pub fn new(key: impl Into<String>, label: impl Into<String>) -> KeyLine {
        KeyLine {
            key: key.into(),
            label: label.into(),
            tabs: Vec::new(),
            hints: Vec::new(),
            end: None,
        }
    }

    /// The tabs a key picks, named `label`, set off from the hints by a hairline.
    pub fn tabs(mut self, tabs: impl IntoIterator<Item = El>) -> Self {
        self.tabs.extend(tabs);
        self
    }

    pub fn push(mut self, hint: impl Into<El>) -> Self {
        self.hints.push(hint.into());
        self
    }

    pub fn end(mut self, hint: El) -> Self {
        self.end = Some(hint);
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let tabs = (!self.tabs.is_empty()).then(|| {
            door::sides(
                door::gap(row().role("tablist").label("Frames"), Space::S2),
                Space::S0,
                Space::S8,
            )
            .border_sides([0.0, 1.0, 0.0, 0.0], t.border_subtle)
            .children(self.tabs)
        });
        let hints = door::gaps(
            row().grow(1.0).min_w(0.0).flex_wrap().items_center(),
            Space::S4,
            Space::S16,
        )
        .children(tabs)
        .children(self.hints);
        door::gap(
            door::inset(
                row()
                    .key(self.key)
                    .role("toolbar")
                    .label(self.label)
                    .w_full()
                    .min_h(Size::Compact.height())
                    .items_center(),
                Space::S8,
                Space::S4,
            ),
            Space::S16,
        )
        .border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle)
        .child(hints)
        .children(self.end)
    }
}
