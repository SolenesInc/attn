use rune::{El, el};

use crate::system::{Space, door};

/// Cards side by side in equal columns, onto further rows as they run out, such as the
/// desktops of the overview: every card as wide as the others, one step apart each way.
pub struct Grid;

impl Grid {
    /// `cards` in `columns` columns, keyed `key`.
    pub fn cards(
        key: impl Into<String>,
        columns: usize,
        cards: impl IntoIterator<Item = El>,
    ) -> El {
        door::gap(
            el().key(key.into()).w_full().equal_columns(columns),
            Space::S12,
        )
        .children(cards)
    }
}
