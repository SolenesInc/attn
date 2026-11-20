use rune::{El, col};

use crate::Tokens;
use crate::system::{Radius, Space, door, focus, layers};

/// How a card stands among the cards around it.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Standing {
    /// One of many.
    Resting,
    /// The one the reader picked.
    Picked,
    /// Lifted off the rest: in the hand, or being written on.
    Lifted,
}

/// One thing among others in a column, such as a seed on a board: its parts one under another on
/// a bordered card. A card the reader presses is a button that takes hover and the ring.
pub struct Card {
    key: String,
    label: Option<String>,
    role: Option<&'static str>,
    parts: Vec<El>,
    standing: Standing,
}

impl Card {
    pub fn new(key: impl Into<String>) -> Card {
        Card {
            key: key.into(),
            label: None,
            role: None,
            parts: Vec::new(),
            standing: Standing::Resting,
        }
    }

    /// The card is a control: `role` and `label` say what it is to assistive tools, and it takes
    /// focus, hover and the ring. The caller gives it its press.
    pub fn control(mut self, role: &'static str, label: impl Into<String>) -> Self {
        self.role = Some(role);
        self.label = Some(label.into());
        self
    }

    pub fn push(mut self, part: impl Into<El>) -> Self {
        self.parts.push(part.into());
        self
    }

    pub fn push_all(mut self, parts: impl IntoIterator<Item = El>) -> Self {
        self.parts.extend(parts);
        self
    }

    pub fn standing(mut self, standing: Standing) -> Self {
        self.standing = standing;
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let (fill, edge) = match self.standing {
            Standing::Resting => (t.bg_panel, t.border_subtle),
            Standing::Picked => (t.bg_selected, t.accent),
            Standing::Lifted => (t.bg_elevated, t.accent),
        };
        let card = col()
            .key(self.key)
            .w_full()
            .fill(fill)
            .border(1.0, edge)
            .children(self.parts);
        let card = door::corner(
            door::gap(door::inset(card, Space::S8, Space::S8), Space::S4),
            Radius::Card,
        );
        match self.role {
            Some(role) => {
                let card = card
                    .role(role)
                    .label(self.label.unwrap_or_default())
                    .focusable(true);
                focus::ring(layers::pressable(card, fill, t), t)
            }
            None => card,
        }
    }
}
