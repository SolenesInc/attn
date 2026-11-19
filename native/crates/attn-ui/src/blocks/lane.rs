use rune::{El, ScrollSpec, col, row};
use rune_kit::ScrollBar;

use crate::Tokens;
use crate::system::{Radius, Space, Type, door, words};

/// One of a board's columns: its name and count over a column of cards that scrolls on its own.
pub struct Lane {
    key: String,
    title: String,
    count: usize,
    aside: Vec<El>,
    body_key: String,
    cards: Vec<El>,
    lit: bool,
    faded: bool,
}

impl Lane {
    /// `key` names the lane, and its cards' scroll `body_key`.
    pub fn new(
        key: impl Into<String>,
        body_key: impl Into<String>,
        title: impl Into<String>,
        count: usize,
    ) -> Lane {
        Lane {
            key: key.into(),
            title: title.into(),
            count,
            aside: Vec::new(),
            body_key: body_key.into(),
            cards: Vec::new(),
            lit: false,
            faded: false,
        }
    }

    /// A control after the count, such as the button that shows what the lane folds away.
    pub fn aside(mut self, control: impl Into<El>) -> Self {
        self.aside.push(control.into());
        self
    }

    pub fn push(mut self, card: impl Into<El>) -> Self {
        self.cards.push(card.into());
        self
    }

    /// Something carried over the lane can land in it.
    pub fn lit(mut self, lit: bool) -> Self {
        self.lit = lit;
        self
    }

    /// Something carried can't land in the lane.
    pub fn faded(mut self, faded: bool) -> Self {
        self.faded = faded;
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let head = door::gap(row().items_center(), Space::S8)
            .child(
                words(self.title.clone(), Type::Name, t.text_primary)
                    .role("heading")
                    .label(self.title.clone()),
            )
            .child(words(self.count.to_string(), Type::Small, t.text_tertiary))
            .children(self.aside);
        let cards = door::gap(col().w_full(), Space::S6).children(self.cards);
        let body = col()
            .key(self.body_key)
            .w_full()
            .grow(1.0)
            .min_h(0.0)
            .scroll(ScrollSpec {
                stick_bottom: false,
            })
            .child(cards)
            .scroll_bar_styled(crate::thin_scroll_bar(t));
        let lane = col()
            .key(self.key)
            .role("region")
            .label(format!("{}, {}", self.title, self.count))
            .w(0.0)
            .grow(1.0)
            .h_full()
            .min_h(0.0)
            .fill(if self.lit { t.bg_selected } else { t.bg_app })
            .opacity(if self.faded { FADED } else { 1.0 })
            .child(head)
            .child(body);
        door::corner(
            door::gap(door::inset(lane, Space::S8, Space::S8), Space::S8),
            Radius::Control,
        )
    }
}

/// How a lane draws that can't take what is carried: as an unavailable control draws.
const FADED: f32 = crate::system::disabled::STRENGTH;

/// A board: what it holds one under another, the lanes taking the height left.
pub fn board(key: impl Into<String>, parts: impl IntoIterator<Item = El>) -> El {
    let board = col()
        .key(key.into())
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .children(parts);
    door::gap(door::inset(board, Space::S8, Space::S8), Space::S8)
}

/// A board's [`Lane`]s side by side, sharing its width.
pub fn lanes(key: impl Into<String>, lanes: impl IntoIterator<Item = El>) -> El {
    let row = row()
        .key(key.into())
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .children(lanes);
    door::gap(row, Space::S8)
}
