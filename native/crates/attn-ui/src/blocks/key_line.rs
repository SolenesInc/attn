use rune::{Color, El, col, row};

use crate::Tokens;
use crate::system::{Radius, Size, Space, Type, door, keycaps, words};

/// A choice a key picks, its caps before its name: drawn on a tint with an edge while it is
/// picked, and read as its keys and its name.
pub fn key_choice(keys: &str, name: &str, on: bool, t: &Tokens) -> El {
    let choice = door::gap(row().items_center(), Space::S4)
        .label(format!("{keys} {name}"))
        .child(keycaps(keys.split(' '), t).a11y_hidden())
        .child(
            words(
                name.to_owned(),
                Type::Small,
                if on { t.text_primary } else { t.text_secondary },
            )
            .a11y_hidden(),
        );
    let choice = door::corner(door::inset(choice, Space::S8, Space::S2), Radius::Tag);
    if on {
        choice.border(1.0, t.border).fill(t.accent.alpha(0.08))
    } else {
        choice.border(1.0, Color::TRANSPARENT)
    }
}

/// A tab a key picks: [`key_choice`] read as a tab, checked while it is picked.
pub fn key_tab(keys: &str, name: &str, on: bool, t: &Tokens) -> El {
    key_choice(keys, name, on, t).role("tab").checked(on)
}

/// How a key line lays out in the room it has, which its caller measures against the line's
/// own widths.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum KeyFit {
    /// Tabs, hints and the end on one line.
    Line,
    /// The tabs and the end on one line, the hints wrapping under them.
    Under,
    /// The tabs and the end alone; the end's key shows the rest.
    Tabs,
}

/// The line under a reader that names its keys: tabs and hints, and one hint at its end, such as
/// the key that shows them all. The tabs and the end always share a line.
pub struct KeyLine {
    key: String,
    label: String,
    tabs: Vec<El>,
    hints: Vec<El>,
    end: Option<El>,
    fit: KeyFit,
}

impl KeyLine {
    pub fn new(key: impl Into<String>, label: impl Into<String>) -> KeyLine {
        KeyLine {
            key: key.into(),
            label: label.into(),
            tabs: Vec::new(),
            hints: Vec::new(),
            end: None,
            fit: KeyFit::Line,
        }
    }

    /// The tabs a key picks, set off by a hairline from the hints that share their line.
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

    pub fn fit(mut self, fit: KeyFit) -> Self {
        self.fit = fit;
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let set_off = self.fit == KeyFit::Line;
        let tabs = (!self.tabs.is_empty()).then(|| {
            let tabs = door::gap(row().role("tablist").label("Frames"), Space::S2);
            let tabs = if set_off {
                door::sides(tabs, Space::S0, Space::S8)
                    .border_sides([0.0, 1.0, 0.0, 0.0], t.border_subtle)
            } else {
                tabs
            };
            tabs.children(self.tabs)
        });
        let hints = |hints: Vec<El>| {
            door::gaps(
                row().grow(1.0).min_w(0.0).flex_wrap().items_center(),
                Space::S4,
                Space::S16,
            )
            .children(hints)
        };
        let line = |el: El| door::gap(el.w_full().items_center(), Space::S16);
        let toolbar = door::inset(
            col()
                .key(self.key)
                .role("toolbar")
                .label(self.label)
                .w_full()
                .min_h(Size::Compact.height())
                .justify_center(),
            Space::S8,
            Space::S4,
        )
        .border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle);
        match self.fit {
            KeyFit::Line => toolbar.child(
                line(row())
                    .children(tabs)
                    .child(hints(self.hints))
                    .children(self.end),
            ),
            KeyFit::Under => door::gap(toolbar, Space::S4)
                .child(
                    line(row())
                        .children(tabs)
                        .child(row().grow(1.0))
                        .children(self.end),
                )
                .child(hints(self.hints).w_full()),
            KeyFit::Tabs => toolbar.child(
                line(row())
                    .children(tabs)
                    .child(row().grow(1.0))
                    .children(self.end),
            ),
        }
    }
}
