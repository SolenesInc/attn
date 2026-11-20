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

    /// Close together on one line that never wraps, its parts giving way as they shrink: a mark
    /// and a name that truncates.
    pub fn joined() -> El {
        door::gap(row().items_center(), Space::S4).min_w(0.0)
    }

    /// Apart: a control and what goes with it, such as a field and its Saved mark.
    pub fn loose() -> El {
        door::gaps(row().flex_wrap().items_center(), Space::S8, Space::S8).max_w_frac(1.0)
    }

    /// Fields that share a line, such as the rates of one price ([`super::Field::el`]): as far
    /// apart across and down as fields one under another, onto further lines when one runs out.
    pub fn fields() -> El {
        door::gaps(row().flex_wrap().items_start(), Space::S16, Space::S16).max_w_frac(1.0)
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

    /// Apart: the parts of a one-off panel, such as the route picker's filter, its models and
    /// its effort.
    pub fn loose() -> El {
        door::gap(col(), Space::S8)
    }
}
