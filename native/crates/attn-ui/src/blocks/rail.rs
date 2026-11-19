use rune::{El, col, el};

use crate::Tokens;
use crate::system::{Size, Space, door};

/// A narrow column of compact buttons down a window's edge, such as the sidebar collapsed.
pub struct Rail;

impl Rail {
    /// The rail, as wide as a compact button and the room either side of it.
    pub fn column() -> El {
        let rail = door::gap(col().items_center(), Space::S4);
        door::rows(
            door::sides(rail, Space::S6, Space::S6),
            Space::S8,
            Space::S8,
        )
    }

    /// A line across the rail between groups of its buttons.
    pub fn rule(t: &Tokens) -> El {
        door::rows(
            col().child(el().w(Size::Compact.height()).h(1.0).fill(t.border)),
            Space::S4,
            Space::S4,
        )
    }
}
