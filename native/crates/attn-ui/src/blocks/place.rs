use rune::El;

use crate::Tokens;
use crate::system::{Space, door};

/// A place in a reader the cursor can stand on, such as a file in a change's list or a line a
/// search found: set in from its sides so a mark has room, and while the cursor is on it,
/// tinted with the accent and railed down its left edge.
pub fn place(content: El, current: bool, t: &Tokens) -> El {
    here(door::inset(content, Space::S6, Space::S2), current, t)
}

/// The mark of where the cursor stands, on a part that sets its own insets, such as a line of
/// code: drawn while `current`.
pub fn here(content: El, current: bool, t: &Tokens) -> El {
    if !current {
        return content;
    }
    let tint = here_tint(t);
    content
        .gradient(tint, tint)
        .border_sides([0.0, 0.0, 0.0, RAIL], t.accent)
        .focus_selected(true)
}

/// The tint under the place the cursor stands on.
pub fn here_tint(t: &Tokens) -> rune::Color {
    t.accent.alpha(0.22)
}

/// The cursor's rail: as wide as a quote's.
const RAIL: f32 = 2.0;
