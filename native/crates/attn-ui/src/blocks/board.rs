use rune::{El, ScrollSpec, col, row, text};
use rune_kit::ScrollBar;

use super::block::{Block, page_gap, spaced};
use crate::Tokens;
use crate::system::{Radius, Size, Space, Type, door, words};

/// A page of cards, such as Home: its head and any notices, which stay where they are, then its
/// cards side by side, wrapping onto further rows when the window is narrow and scrolling
/// together. Keyed `key`, it reads as the window's main landmark named `label`; its cards lie
/// in `{key}.cards`, which scrolls as `{key}.cards.scroll`, and its head's line is
/// `{key}.header`.
pub struct Board {
    key: String,
    label: String,
    head: Option<Brand>,
    top: Vec<Block>,
    cards: Vec<El>,
}

impl Board {
    pub fn new(key: impl Into<String>, label: impl Into<String>) -> Board {
        Board {
            key: key.into(),
            label: label.into(),
            head: None,
            top: Vec::new(),
            cards: Vec::new(),
        }
    }

    /// The board's head: the app's icon, when there is one, its name as the brand's wordmark,
    /// read as the page's heading, what it is under that, and a control at the line's end.
    pub fn brand(
        mut self,
        icon: Option<El>,
        name: impl Into<String>,
        lead: impl Into<String>,
        aside: impl Into<El>,
    ) -> Self {
        self.head = Some(Brand {
            icon,
            name: name.into(),
            lead: lead.into(),
            aside: aside.into(),
        });
        self
    }

    /// A block between the head and the cards, such as a notice.
    pub fn top(mut self, block: Block) -> Self {
        self.top.push(block);
        self
    }

    pub fn card(mut self, card: El) -> Self {
        self.cards.push(card);
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let head = self
            .head
            .map(|head| head.build(format!("{}.header", self.key), t));
        let top = (!self.top.is_empty()).then(|| spaced(self.top, page_gap, None));
        let above = col()
            .w_full()
            .children(head.map(|head| door::below(head, Space::S24)))
            .children(top.map(|top| door::below(top, Space::S16)));
        let cards = door::gaps(row().w_full().flex_wrap(), Space::S16, Space::S16)
            .key(format!("{}.cards", self.key))
            .children(self.cards);
        let scroll = col()
            .key(format!("{}.cards.scroll", self.key))
            .w_full()
            .grow(1.0)
            .min_h(0.0)
            .scroll(ScrollSpec {
                stick_bottom: false,
            })
            .child(cards)
            .scroll_bar_styled(crate::scroll_bar(t));
        let board = col()
            .key(self.key)
            .role("main")
            .label(self.label)
            .grow(1.0)
            .h_full()
            .min_w(0.0)
            .fill(t.bg_app);
        door::frame(board, Space::S24, Space::S32, Space::S24)
            .child(above)
            .child(scroll)
    }
}

struct Brand {
    icon: Option<El>,
    name: String,
    lead: String,
    aside: El,
}

impl Brand {
    fn build(self, key: String, t: &Tokens) -> El {
        let name = door::styled(text(self.name.clone()), door::WORDMARK)
            .color(t.brand)
            .role("heading")
            .level(1)
            .label(self.name)
            .single_line()
            .truncate()
            .shrink();
        let said = col().grow(1.0).min_w(0.0).child(name).child(
            door::styled(text(self.lead.to_uppercase()), door::TAGLINE)
                .color(t.text_dimmed)
                .label(self.lead)
                .single_line()
                .truncate()
                .shrink(),
        );
        door::gap(row().w_full().items_center(), Space::S12)
            .key(key)
            .children(self.icon)
            .child(said)
            .child(self.aside)
    }
}

/// One card of a [`Board`]: a band holding its heading and actions, ruled off from its blocks.
/// Keyed `key`, it reads as a region named by its heading. The cards of one row of a board are
/// as tall as the tallest, and each keeps the least size its feature gives it.
pub struct BoardCard {
    key: String,
    heading: String,
    actions: Vec<El>,
    blocks: Vec<Block>,
    least: (f32, f32),
}

impl BoardCard {
    pub fn new(key: impl Into<String>, heading: impl Into<String>) -> BoardCard {
        BoardCard {
            key: key.into(),
            heading: heading.into(),
            actions: Vec::new(),
            blocks: Vec::new(),
            least: (0.0, 0.0),
        }
    }

    /// A control at the end of the heading's band, such as a count or New.
    pub fn action(mut self, action: impl Into<El>) -> Self {
        self.actions.push(action.into());
        self
    }

    pub fn push(mut self, block: Block) -> Self {
        self.blocks.push(block);
        self
    }

    /// The narrowest the card gets before the cards beside it move to a row of their own, and
    /// the shortest it stands with little in it. Alone on its row in a board narrower still, it
    /// takes the board's width.
    pub fn least(mut self, width: f32, height: f32) -> Self {
        self.least = (width, height);
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let band = door::gap(row().w_full().items_center(), Space::S8)
            .key(format!("{}.header", self.key))
            .min_h(Size::Compact.height())
            .child(
                words(self.heading.clone(), Type::Label, t.text_tertiary)
                    .role("heading")
                    .label(self.heading.clone())
                    .single_line()
                    .truncate()
                    .shrink()
                    .grow(1.0),
            )
            .children(self.actions);
        let band =
            door::inset(band, Space::S12, Space::S8).border_sides([0.0, 0.0, 1.0, 0.0], t.border);
        let body = door::inset(spaced(self.blocks, page_gap, None), Space::S8, Space::S12);
        let (width, height) = self.least;
        let card = col()
            .key(self.key)
            .role("region")
            .label(self.heading)
            .grow_from(1.0, width)
            .min_h(height)
            .fill(t.bg_panel)
            .border(1.0, t.border)
            .child(band)
            .child(body);
        door::corner(card, Radius::Card)
    }
}
