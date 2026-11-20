use rune::{El, row};

use crate::Tokens;
use crate::system::{Radius, Space, door};

/// Words about one thing on a round edged ground, read together, such as what a route runs:
/// the harness, the model with its tier and the effort. The parts stand side by side and go onto
/// further lines when they run out of room, so a long model id wraps inside the pill.
pub fn pill(parts: impl IntoIterator<Item = El>, t: &Tokens) -> El {
    let line = door::gaps(
        row().flex_wrap().items_end().shrink().min_w(0.0),
        Space::S2,
        Space::S6,
    )
    .fill(t.bg_panel)
    .border(1.0, t.border)
    .children(parts);
    door::corner(door::inset(line, Space::S8, Space::S4), Radius::Round)
}
