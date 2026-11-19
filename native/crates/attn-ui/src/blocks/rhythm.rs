use rune::{El, col, row};

use crate::system::{Space, door};

/// Things one after another across, for a one-off that fits no block, onto further lines when
/// one runs out.
pub struct Inline;

impl Inline {
    /// Close together: the icons of a picker, a chip and its count.
    pub fn tight() -> El {
        door::gaps(row().flex_wrap().items_center(), Space::S4, Space::S4).max_w_frac(1.0)
    }

    /// Apart: a control and what goes with it, such as a field and its Saved mark.
    pub fn loose() -> El {
        door::gaps(row().flex_wrap().items_center(), Space::S8, Space::S8).max_w_frac(1.0)
    }
}

impl Inline {
    /// Apart along their tops, on one line: a mark, words that wrap and take the room left, and
    /// the controls that act on them.
    pub fn top() -> El {
        door::gap(row().w_full().items_start(), Space::S8)
    }
}

/// Things one under another, for a one-off that fits no block.
pub struct Stack;

impl Stack {
    /// Close together: a control and the line under it.
    pub fn tight() -> El {
        door::gap(col(), Space::S4)
    }
}
