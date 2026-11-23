use rune::{Color, row};

use super::{Control, Radius, Size, Space, Type, door, focus, layers, words};
use crate::Tokens;

/// Words that act, such as a step of a trail or the title of a related seed. The words sit on
/// the line of the text around them: the box that takes hover and the ring reaches past them by
/// [`Link::REACH`] on each side and hangs out by as much.
pub struct Link {
    key: String,
    shown: String,
    kind: Type,
    ink: Option<Color>,
    wrap: bool,
}

impl Link {
    const REACH: Space = Space::S4;

    pub fn new(key: impl Into<String>, shown: impl Into<String>) -> Link {
        Link {
            key: key.into(),
            shown: shown.into(),
            kind: Type::Small,
            ink: None,
            wrap: false,
        }
    }

    pub fn kind(mut self, kind: Type) -> Self {
        self.kind = kind;
        self
    }

    pub fn ink(mut self, ink: Color) -> Self {
        self.ink = Some(ink);
        self
    }

    /// The words wrap onto further lines instead of ending in an ellipsis.
    pub fn wrap(mut self) -> Self {
        self.wrap = true;
        self
    }

    pub fn build(self, t: &Tokens) -> Control {
        let ink = self.ink.unwrap_or(t.text_secondary);
        let said = words(self.shown.clone(), self.kind, ink).a11y_hidden();
        let said = if self.wrap {
            said.wrap().min_w(0.0).shrink()
        } else {
            said.truncate()
        };
        let link = row()
            .key(self.key)
            .role("button")
            .label(self.shown)
            .focusable(true)
            .min_w(0.0)
            .shrink()
            .items_center()
            .child(said);
        let link = door::reach(link, Self::REACH);
        let link = layers::pressable(door::corner(link, Radius::Tag), t.bg_panel, t);
        Control::new(focus::ring(link, t), Size::Compact)
    }
}
