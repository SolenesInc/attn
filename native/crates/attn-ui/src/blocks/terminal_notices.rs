use rune::{El, col, row};
use rune_kit::ScrollBar;

use super::Block;
use super::measure::DOCUMENT;
use crate::Tokens;
use crate::system::{Radius, Space, door};

/// `notices` laid over the top of a terminal, one under another, centred on a reading line and
/// kept in from the terminal's edges, such as that another terminal has the conversation open.
/// Each stands on the terminal's own ground, so output under it never crosses its words, and is
/// announced politely, since the same notice can stand on several tiles at once. The band covers
/// `room` of the terminal at most, as much as leaves it its rows, and scrolls past that.
pub fn terminal_notices(key: impl Into<String>, notices: Vec<Block>, room: f32, t: &Tokens) -> El {
    let grounded = notices
        .into_iter()
        .map(|notice| {
            let kind = notice.kind;
            let ground = col()
                .w_full()
                .fill(t.bg_tile)
                .child(notice.el.role("status"));
            Block::new(kind, door::corner(ground, Radius::Card))
        })
        .collect::<Vec<_>>();
    let notices = super::stacked(grounded)
        .w_full()
        .max_w(DOCUMENT)
        .max_h((room - 2.0 * Space::S12.pt()).max(0.0))
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .scroll_bar_styled(crate::scroll_bar(t));
    let over = row()
        .key(key.into())
        .absolute(0.0, 0.0)
        .w_full()
        .justify_center()
        .child(col().w_full().items_center().child(notices));
    door::inset(over, Space::S12, Space::S12)
}
