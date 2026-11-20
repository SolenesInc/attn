use rune::{El, col, row};
use rune_kit::ScrollBar;

use super::actions::Actions;
use super::block::{Block, inner_gap, spaced};
use crate::system::{Size, Space, Type, door, words};
use crate::{Frame, Tokens};

/// A small form opened from a control and anchored beside it: its title, controls beside the
/// title, a body of blocks and a line of actions, on a panel. Whoever opens it anchors it and
/// says what closes it.
pub struct Popover {
    key: String,
    title: String,
    aside: Vec<El>,
    body: Vec<Block>,
    actions: Option<Actions>,
}

/// A popover's width: its harness segments and its effort segments keep one line each, and a
/// model's name with its mark, "Opus 5.5 · recommended" at 157.8 in `Name`, keeps one.
const WIDTH: f32 = 280.0;

impl Popover {
    /// `key` names the popover, which reads as a dialog named `title`; its title is
    /// `{key}.heading`.
    pub fn new(key: impl Into<String>, title: impl Into<String>) -> Popover {
        Popover {
            key: key.into(),
            title: title.into(),
            aside: Vec::new(),
            body: Vec::new(),
            actions: None,
        }
    }

    /// A control beside the title, such as the one that closes the popover.
    pub fn aside(mut self, control: impl Into<El>) -> Self {
        self.aside.push(control.into());
        self
    }

    pub fn push(mut self, block: Block) -> Self {
        self.body.push(block);
        self
    }

    pub fn push_all(mut self, blocks: impl IntoIterator<Item = Block>) -> Self {
        self.body.extend(blocks);
        self
    }

    pub fn actions(mut self, actions: Actions) -> Self {
        self.actions = Some(actions);
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let title = words(self.title.clone(), Type::Name, t.text_primary)
            .key(format!("{}.heading", self.key))
            .role("heading")
            .wrap();
        let banded = !self.aside.is_empty();
        let title = if banded {
            door::on_band(title, Type::Name, Size::Compact)
        } else {
            title
        };
        let beside = door::gap(row().items_center(), Space::S8).children(self.aside);
        let beside = if banded {
            beside.h(Size::Compact.height())
        } else {
            beside
        };
        let head = door::gap(row().w_full().items_start(), Space::S12)
            .child(col().grow(1.0).min_w(0.0).child(title))
            .child(beside);
        let actions = self
            .actions
            .map(|actions| actions.build().el.key(format!("{}.actions", self.key)));
        let body = (!self.body.is_empty()).then(|| {
            spaced(self.body, inner_gap, None)
                .key(format!("{}.body", self.key))
                .shrink()
                .min_h(0.0)
                .scroll(rune::ScrollSpec {
                    stick_bottom: false,
                })
                .scroll_bar_styled(crate::scroll_bar(t))
        });
        let surface = door::gap(
            col()
                .key(self.key)
                .role("dialog")
                .label(self.title)
                .focusable(true)
                .no_focus_ring()
                .shrink()
                .min_h(0.0)
                .child(head)
                .children(body)
                .children(actions),
            Space::S12,
        );
        Frame::panel(t)
            .draw_clipped(door::inset(surface, Space::S16, Space::S16))
            .w(WIDTH)
            .min_h(0.0)
    }
}
