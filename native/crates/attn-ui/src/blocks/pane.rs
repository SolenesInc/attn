use rune::{El, ScrollSpec, col};
use rune_kit::ScrollBar;

use crate::Tokens;
use crate::system::{Space, door};

/// A column of a reader laid out across a wide window, such as one level of a walk through a
/// tree, that scrolls on its own, with a rule down its right side.
pub struct Pane {
    key: String,
    width: Width,
    ruled: bool,
    content: El,
}

enum Width {
    Fixed(f32),
    Share { share: f32, least: f32 },
    Rest,
}

impl Pane {
    pub fn new(key: impl Into<String>, content: El) -> Pane {
        Pane {
            key: key.into(),
            width: Width::Rest,
            ruled: false,
            content,
        }
    }

    /// The pane's width, which belongs to the reader that lays it out. Without one, the pane
    /// takes the room the others leave.
    pub fn width(mut self, width: f32) -> Self {
        self.width = Width::Fixed(width);
        self.ruled = true;
        self
    }

    /// The pane takes `share` of the room the fixed panes leave, and no less than `least`.
    pub fn share(mut self, share: f32, least: f32) -> Self {
        self.width = Width::Share { share, least };
        self
    }

    /// A rule down the pane's right side, between it and the pane after it.
    pub fn ruled(mut self) -> Self {
        self.ruled = true;
        self
    }

    /// The pane's words stand as far from its edges as a page's in a wide reader.
    pub fn wide(self, t: &Tokens) -> El {
        self.framed(t, (Space::S24, Space::S32, Space::S32))
    }

    pub fn build(self, t: &Tokens) -> El {
        self.framed(t, (Space::S12, Space::S16, Space::S32))
    }

    fn framed(self, t: &Tokens, (top, x, bottom): (Space, Space, Space)) -> El {
        let pane = col().key(self.key).h_full().min_h(0.0);
        let pane = match self.width {
            Width::Fixed(width) => pane.w(width).min_w(width),
            Width::Share { share, least } => pane.w(0.0).grow(share).min_w(least),
            Width::Rest => pane.grow(1.0).min_w(0.0),
        };
        let pane = if self.ruled {
            pane.border_sides([0.0, 1.0, 0.0, 0.0], t.border_subtle)
        } else {
            pane
        };
        door::frame(pane, top, x, bottom)
            .scroll(ScrollSpec {
                stick_bottom: false,
            })
            .child(self.content)
            .scroll_bar_styled(crate::thin_scroll_bar(t))
    }
}
