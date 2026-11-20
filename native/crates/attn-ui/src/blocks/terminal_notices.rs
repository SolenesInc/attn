use rune::{El, col, row};

use super::Block;
use super::measure::DOCUMENT;
use crate::system::{Space, door};

/// `notices` laid over the top of a terminal, one under another, centred on a reading line and
/// kept in from the terminal's edges, such as that another terminal has the conversation open.
pub fn terminal_notices(key: impl Into<String>, notices: Vec<Block>) -> El {
    let notices = super::stacked(notices).w_full().max_w(DOCUMENT);
    let over = row()
        .key(key.into())
        .absolute(0.0, 0.0)
        .w_full()
        .justify_center()
        .child(col().w_full().items_center().child(notices));
    door::inset(over, Space::S12, Space::S12)
}
