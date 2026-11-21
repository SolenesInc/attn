use rune::{El, col, el, row};
use rune_kit::ScrollBar;

use super::Surface;
use crate::Tokens;
use crate::system::{Size, Space, door};

/// A strip of icon buttons raised over what it acts on, such as the one a selection opens: its
/// groups of buttons with a rule between each two, what stands above the strip, such as the grip
/// that moves it, and what one of them opens under the strip, such as a list to pick from.
pub struct Toolbar {
    key: String,
    label: String,
    head: Option<El>,
    groups: Vec<Vec<El>>,
    under: Option<El>,
    max_height: Option<f32>,
}

impl Toolbar {
    /// `key` names the toolbar, which assistive tools read as a toolbar named `label`.
    pub fn new(key: impl Into<String>, label: impl Into<String>) -> Toolbar {
        Toolbar {
            key: key.into(),
            label: label.into(),
            head: None,
            groups: Vec::new(),
            under: None,
            max_height: None,
        }
    }

    /// Buttons that go together, set apart from the next group by a rule.
    pub fn group(mut self, buttons: impl IntoIterator<Item = impl Into<El>>) -> Self {
        self.groups
            .push(buttons.into_iter().map(Into::into).collect());
        self
    }

    /// What stands above the strip, on the same ground, such as the grip that moves it.
    pub fn head(mut self, part: Option<El>) -> Self {
        self.head = part;
        self
    }

    /// What a button opened under the strip.
    pub fn under(mut self, part: Option<El>) -> Self {
        self.under = part;
        self
    }

    /// The tallest the toolbar stands, such as the room the surface under it leaves: what stands
    /// under the strip scrolls past that.
    pub fn max_height(mut self, height: f32) -> Self {
        self.max_height = Some(height);
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
        let under = match self.max_height {
            Some(_) => self.under.map(|under| {
                col()
                    .shrink()
                    .min_h(0.0)
                    .child(under)
                    .scroll(rune::ScrollSpec {
                        stick_bottom: false,
                    })
                    .scroll_bar_styled(crate::scroll_bar(t))
            }),
            None => self.under,
        };
        let body = col()
            .key(self.key)
            .role("toolbar")
            .label(self.label)
            .min_h(0.0)
            .children(self.head)
            .child(strip)
            .children(under);
        let body = match self.max_height {
            Some(height) => body.max_h((height - 2.0 * Space::S4.pt()).max(0.0)),
            None => body,
        };
        let toolbar = door::inset(body, Space::S4, Space::S4);
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
