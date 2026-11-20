use rune::El;

use crate::Tokens;
use crate::system::{Space, door};

/// A place in a reader the cursor can stand on, such as a file in a change's list or a line a
/// search found: set in from its sides so a mark has room, and while the cursor is on it,
/// drawn as a list row in view: the selected surface, with the brand bar down its left edge.
pub fn place(content: El, current: bool, t: &Tokens) -> El {
    here(door::inset(content, Space::S6, Space::S2), current, t)
}

/// A place with a mark of change down its left edge in `edge`, such as a block a review changed
/// or removed: set in as [`place`] sets in, and while the cursor is on it, the cursor's bar takes
/// the edge.
pub fn edged(content: El, edge: rune::Color, current: bool, t: &Tokens) -> El {
    here(
        door::inset(content, Space::S8, Space::S4).border_sides([0.0, 0.0, 0.0, MARK], edge),
        current,
        t,
    )
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
        .border_sides([0.0, 0.0, 0.0, RAIL], t.brand)
        .focus_selected(true)
}

/// The surface under the place the cursor stands on, as under a list row in view.
pub fn here_tint(t: &Tokens) -> rune::Color {
    t.bg_elevated
}

/// The surface under the place the cursor stands on in a list raised over the window, such as
/// a search's answers: the selected surface, since a raised list already lies on the elevated
/// one.
pub fn raised_here_tint(t: &Tokens) -> rune::Color {
    t.bg_selected
}

/// The cursor's bar: as wide as a list row's in view.
const RAIL: f32 = 2.0;

/// The mark of change: a step wider than the cursor's bar, so the two read apart.
const MARK: f32 = 3.0;
