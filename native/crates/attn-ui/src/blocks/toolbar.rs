use rune::{El, col, el, row};

use super::Surface;
use crate::Tokens;
use crate::system::{Size, Space, door};

/// A strip of icon buttons raised over what it acts on, such as the one a selection opens: its
/// groups of buttons with a rule between each two, and what one of them opens under the strip,
/// such as a list to pick from.
pub struct Toolbar {
    key: String,
    label: String,
    groups: Vec<Vec<El>>,
    under: Option<El>,
}

impl Toolbar {
    /// `key` names the toolbar, which assistive tools read as a toolbar named `label`.
    pub fn new(key: impl Into<String>, label: impl Into<String>) -> Toolbar {
        Toolbar {
            key: key.into(),
            label: label.into(),
            groups: Vec::new(),
            under: None,
        }
    }

    /// Buttons that go together, set apart from the next group by a rule.
    pub fn group(mut self, buttons: impl IntoIterator<Item = impl Into<El>>) -> Self {
        self.groups
            .push(buttons.into_iter().map(Into::into).collect());
        self
    }

    /// What a button opened under the strip.
    pub fn under(mut self, part: Option<El>) -> Self {
        self.under = part;
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let mut strip = door::gap(row().items_center(), Space::S2);
        for (at, group) in self.groups.into_iter().enumerate() {
            if at > 0 {
                strip = strip.child(rule(t));
            }
            strip = strip.children(group);
        }
        let toolbar = door::inset(
            col()
                .key(self.key)
                .role("toolbar")
                .label(self.label)
                .child(strip)
                .children(self.under),
            Space::S4,
            Space::S4,
        );
        Surface::raised(toolbar, t)
    }
}

fn rule(t: &Tokens) -> El {
    door::inset(
        row().child(el().w(1.0).h(Size::Small.height()).fill(t.border)),
        Space::S4,
        Space::S0,
    )
}
