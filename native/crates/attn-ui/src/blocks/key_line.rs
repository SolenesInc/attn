use rune::{Color, El, col, row};

use crate::Tokens;
use crate::system::{Radius, Size, Space, Type, door, keycaps, keycaps_width, words};

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

/// How wide [`key_choice`] draws `keys` and `name`.
pub fn key_choice_width(keys: &str, name: &str) -> f32 {
    2.0 * Space::S8.pt()
        + keycaps_width(keys.split(' '))
        + Space::S4.pt()
        + door::width(name, Type::Small)
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

impl KeyFit {
    /// The fit for a key line `room` points wide, from the widths its tabs, hints and end draw
    /// at, as [`key_choice_width`] and [`keys_hint_width`] give them: one line when everything
    /// fits on it, the hints under the tabs while they take two lines at most, else the tabs
    /// alone.
    pub fn in_room(room: f32, tabs: &[f32], hints: &[f32], end: f32) -> KeyFit {
        let inside = room - 2.0 * Space::S8.pt();
        let gap = Space::S16.pt();
        let row = |widths: &[f32]| {
            widths.iter().sum::<f32>() + Space::S2.pt() * widths.len().saturating_sub(1) as f32
        };
        let tabs_room = if tabs.is_empty() {
            0.0
        } else {
            row(tabs) + Space::S8.pt() + gap
        };
        let hints_room = hints.iter().sum::<f32>() + gap * hints.len().saturating_sub(1) as f32;
        if tabs_room + hints_room + gap + end <= inside {
            return KeyFit::Line;
        }
        let lines = hints.iter().fold((0, f32::INFINITY), |(lines, used), &w| {
            if used + gap + w <= inside {
                (lines, used + gap + w)
            } else {
                (lines + 1, w)
            }
        });
        if lines.0 <= 2 {
            KeyFit::Under
        } else {
            KeyFit::Tabs
        }
    }
}

/// The line under a reader that names its keys: tabs and hints, and one hint at its end, such as
/// the key that shows them all. The tabs and the end always share a line.
pub struct KeyLine {
    key: String,
    label: String,
    tabs: Vec<El>,
    hints: Vec<El>,
    status: Option<El>,
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
            status: None,
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

    /// What the reader's tools report, with the controls that act on it, such as its language
    /// servers' state: before the hints, and on a line of its own when the hints give way.
    pub fn status(mut self, status: El) -> Self {
        self.status = Some(status);
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
        let (status, shown) = (self.status, self.hints);
        match self.fit {
            KeyFit::Line => toolbar.child(
                line(row())
                    .children(tabs)
                    .child(hints(status.into_iter().chain(shown).collect()))
                    .children(self.end),
            ),
            KeyFit::Under => door::gap(toolbar, Space::S4)
                .child(
                    line(row())
                        .children(tabs)
                        .child(row().grow(1.0))
                        .children(self.end),
                )
                .child(hints(status.into_iter().chain(shown).collect()).w_full()),
            KeyFit::Tabs => door::gap(toolbar, Space::S4)
                .child(
                    line(row())
                        .children(tabs)
                        .child(row().grow(1.0))
                        .children(self.end),
                )
                .children(status.map(|status| hints(vec![status]).w_full())),
        }
    }
}
